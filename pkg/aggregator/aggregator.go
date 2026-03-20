// Package aggregator scrapes Prometheus-formatted metrics from multiple
// endpoints and merges them into a single output.
package aggregator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
)

// centralised port definitions
const (
	DefaultAggregatorPort       = "9090"
	metricsEnvVariableName      = "METRICS_ENDPOINTS"
	securityModeEnvVariableName = "METRICS_SECURITY_MODE"
	securityModeStrict          = "strict"
	// Deprecated: legacy mode will be removed in a future release.
	securityModeLegacy = "legacy"
	defaultMetricsPath = "/metrics"
	maxBodySize        = 10 << 20 // 10 MiB
)

var (
	validName      = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	sampleLine     = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*(\{(?:[^"{}\\]|"(?:[^"\\]|\\.)*")*\})?\s+(?:NaN|[+-]?Inf|[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?)(?:\s+\d+)?$`)
	originLabelKey = regexp.MustCompile(`[{,]\s*origin_container\s*=`)
)

type contextKey int

const (
	// ContextKeyRequestID carries the X-Request-Id value for forwarding to scrape targets.
	ContextKeyRequestID contextKey = iota
	// ContextKeyTraceparent carries the traceparent header value for forwarding.
	ContextKeyTraceparent
)

// Endpoint represents a Prometheus /metrics endpoint.
type Endpoint struct {
	Name string
	URL  string
}

type scrapeResult struct {
	lines          []string
	success        bool
	duration       time.Duration
	invalidSamples int
}

// Aggregator scrapes and merges Prometheus metrics from multiple endpoints.
type Aggregator struct {
	endpoints          []Endpoint
	client             *http.Client
	logger             zerolog.Logger
	requestsTotal      atomic.Int64
	errorsTotal        atomic.Int64
	scrapeErrors       []atomic.Int64
	scrapeDurationHist map[string]*Histogram
}

// NewAggregator parses endpointsConfig (JSON map or comma-separated URLs)
// and returns an initialised Aggregator.
func NewAggregator(endpointsConfig string) (*Aggregator, error) {
	if strings.TrimSpace(endpointsConfig) == "" {
		return nil, fmt.Errorf("%s not defined", metricsEnvVariableName)
	}
	securityMode := currentSecurityMode()

	var parsed []Endpoint

	// 1) try to parse as JSON map
	var endpointMap map[string]string
	if err := json.Unmarshal([]byte(endpointsConfig), &endpointMap); err == nil {
		for name, url := range endpointMap {
			parsed = append(parsed, Endpoint{Name: name, URL: url})
		}
	} else {
		// 2) fallback: comma-separated URLs
		log.Warn().Msg("METRICS_ENDPOINTS is not valid JSON; trying comma-separated URL list")

		for i, url := range strings.Split(endpointsConfig, ",") {
			url = strings.TrimSpace(url)
			if url != "" {
				parsed = append(parsed, Endpoint{
					Name: fmt.Sprintf("endpoint%d", i+1),
					URL:  url,
				})
			}
		}
	}

	// Validate all endpoint names (C2: reject chars that break label values)
	for i, ep := range parsed {
		if !validName.MatchString(ep.Name) {
			return nil, fmt.Errorf("invalid endpoint name %q: must match [a-zA-Z0-9_-]+", ep.Name)
		}
		validatedURL, err := validateEndpointURL(ep.URL, securityMode)
		if err != nil {
			return nil, fmt.Errorf("invalid endpoint URL for %q: %w", ep.Name, err)
		}
		parsed[i].URL = validatedURL
	}

	if len(parsed) == 0 {
		return nil, fmt.Errorf("no valid endpoints found in %s", metricsEnvVariableName)
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	scrapeDurationHist := make(map[string]*Histogram, len(parsed))
	for _, ep := range parsed {
		scrapeDurationHist[ep.Name] = NewHistogram(DefaultBuckets())
	}

	agg := &Aggregator{
		endpoints:          parsed,
		client:             client,
		logger:             log.Logger,
		scrapeErrors:       make([]atomic.Int64, len(parsed)),
		scrapeDurationHist: scrapeDurationHist,
	}

	agg.logger.Info().Msg("aggregating metrics from configured endpoints")
	for _, ep := range agg.endpoints {
		agg.logger.Info().
			Str("name", ep.Name).
			Str("url", sanitizeURLForLog(ep.URL)).
			Msg("endpoint registered")
	}
	return agg, nil
}

// getEndpoints returns the configured endpoints (read-only copy).
func (a *Aggregator) getEndpoints() []Endpoint {
	out := make([]Endpoint, len(a.endpoints))
	copy(out, a.endpoints)
	return out
}

// AggregateMetrics fetches metrics concurrently, strips metadata lines,
// injects origin_container labels, and merges them with self-instrumentation.
// The context carries the request-scoped logger (if any) for correlated log output.
func (a *Aggregator) AggregateMetrics(ctx context.Context) (string, error) {
	a.requestsTotal.Add(1)

	// Use request-scoped logger if available, fall back to a.logger.
	logger := zerolog.Ctx(ctx)
	if logger.GetLevel() == zerolog.Disabled {
		logger = &a.logger
	}

	if len(a.endpoints) == 0 {
		a.errorsTotal.Add(1)
		return "", fmt.Errorf("no endpoints configured")
	}

	results := make([]scrapeResult, len(a.endpoints))
	var wg sync.WaitGroup

	for i, ep := range a.endpoints {
		wg.Add(1)
		go func(idx int, ep Endpoint) {
			defer wg.Done()
			scrapeCtx, span := otel.Tracer("metrics-aggregator").Start(ctx, "scrape "+ep.Name)
			span.SetAttributes(attribute.String("endpoint.name", ep.Name))
			defer span.End()

			start := time.Now()
			sanitizedURL := sanitizeURLForLog(ep.URL)
			failed := true
			defer func() {
				dur := time.Since(start)
				a.scrapeDurationHist[ep.Name].Observe(dur.Seconds())
				if failed {
					a.scrapeErrors[idx].Add(1)
					span.SetStatus(otelcodes.Error, "scrape failed")
				} else {
					span.SetStatus(otelcodes.Ok, "")
				}
			}()

			req, err := http.NewRequestWithContext(scrapeCtx, http.MethodGet, ep.URL, nil)
			if err != nil {
				logger.Error().Err(err).Str("url", sanitizedURL).Msg("request creation failed")
				span.RecordError(err)
				results[idx] = scrapeResult{duration: time.Since(start)}
				return
			}
			if rid, ok := ctx.Value(ContextKeyRequestID).(string); ok && rid != "" {
				req.Header.Set("X-Request-Id", rid)
			}
			// Inject the active scrape span context so targets receive the correct
			// child span ID for distributed tracing. When tracing is disabled the
			// propagator is a no-op, so fall back to forwarding the validated
			// inbound traceparent for passive log correlation.
			otel.GetTextMapPropagator().Inject(scrapeCtx, propagation.HeaderCarrier(req.Header))
			if req.Header.Get("traceparent") == "" {
				if tp, ok := ctx.Value(ContextKeyTraceparent).(string); ok && tp != "" {
					req.Header.Set("traceparent", tp)
				}
			}

			resp, err := a.client.Do(req)
			if err != nil {
				logger.Error().Err(err).Str("url", sanitizedURL).Msg("HTTP GET failed")
				span.RecordError(err)
				results[idx] = scrapeResult{duration: time.Since(start)}
				return
			}
			if resp.StatusCode != http.StatusOK {
				logger.Warn().Int("status_code", resp.StatusCode).Str("url", sanitizedURL).Msg("non-200 response")
				span.RecordError(fmt.Errorf("non-200 status: %d", resp.StatusCode))
				resp.Body.Close()
				results[idx] = scrapeResult{duration: time.Since(start)}
				return
			}

			body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize+1))
			resp.Body.Close()
			if err != nil {
				logger.Error().Err(err).Str("url", sanitizedURL).Msg("read body failed")
				span.RecordError(err)
				results[idx] = scrapeResult{duration: time.Since(start)}
				return
			}
			if len(body) > maxBodySize {
				logger.Warn().
					Str("endpoint", ep.Name).
					Str("url", sanitizedURL).
					Int("max_body_size", maxBodySize).
					Msg("scrape body exceeded size limit; discarding endpoint payload")
				results[idx] = scrapeResult{duration: time.Since(start)}
				return
			}

			// C1: strip comment/metadata lines; only keep metric data lines
			var lines []string
			invalidSamples := 0
			for _, line := range strings.Split(string(body), "\n") {
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				labeled := addCustomLabel(line, ep.Name)
				if !isValidPrometheusSampleLine(labeled) {
					invalidSamples++
					continue
				}
				lines = append(lines, labeled)
			}
			if invalidSamples > 0 {
				logger.Warn().
					Str("endpoint", ep.Name).
					Str("url", sanitizedURL).
					Int("dropped_samples", invalidSamples).
					Msg("dropped invalid scrape samples")
			}
			failed = len(lines) == 0
			results[idx] = scrapeResult{
				lines:          lines,
				success:        len(lines) > 0,
				duration:       time.Since(start),
				invalidSamples: invalidSamples,
			}
		}(i, ep)
	}
	wg.Wait()

	// Pre-allocate merged slice: 6 HELP/TYPE headers + 3 lines per endpoint
	// + 4 counter HELP/TYPE/value lines + scraped lines.
	totalLines := 10 + 3*len(a.endpoints)
	for _, r := range results {
		totalLines += len(r.lines)
	}
	merged := make([]string, 0, totalLines)

	// Build self-instrumentation metrics (M1)
	merged = append(merged,
		"# HELP metrics_aggregator_scrape_success Whether the last scrape of an endpoint succeeded.",
		"# TYPE metrics_aggregator_scrape_success gauge",
	)
	for i, ep := range a.endpoints {
		val := 0
		if results[i].success {
			val = 1
		}
		merged = append(merged, fmt.Sprintf("metrics_aggregator_scrape_success{endpoint=%q} %d", ep.Name, val))
	}
	merged = append(merged, RenderHeader("metrics_aggregator_scrape_duration_seconds", "Duration of endpoint scrapes in seconds."))
	for _, ep := range a.endpoints {
		merged = append(merged, RenderSamples("metrics_aggregator_scrape_duration_seconds", fmt.Sprintf("endpoint=%q", ep.Name), a.scrapeDurationHist[ep.Name]))
	}
	merged = append(merged,
		"# HELP metrics_aggregator_scrape_errors_total Total number of failed scrapes per endpoint.",
		"# TYPE metrics_aggregator_scrape_errors_total counter",
	)
	for i, ep := range a.endpoints {
		merged = append(merged, fmt.Sprintf("metrics_aggregator_scrape_errors_total{endpoint=%q} %d", ep.Name, a.scrapeErrors[i].Load()))
	}
	merged = append(merged,
		"# HELP metrics_aggregator_scrape_invalid_samples Number of invalid scrape samples dropped from the last scrape.",
		"# TYPE metrics_aggregator_scrape_invalid_samples gauge",
	)
	for i, ep := range a.endpoints {
		merged = append(merged, fmt.Sprintf("metrics_aggregator_scrape_invalid_samples{endpoint=%q} %d", ep.Name, results[i].invalidSamples))
	}
	merged = append(merged,
		"# HELP metrics_aggregator_requests_total Total number of aggregation executions (excludes cache hits).",
		"# TYPE metrics_aggregator_requests_total counter",
		fmt.Sprintf("metrics_aggregator_requests_total %d", a.requestsTotal.Load()),
		"# HELP metrics_aggregator_errors_total Total number of failed aggregation executions (all endpoints down).",
		"# TYPE metrics_aggregator_errors_total counter",
		fmt.Sprintf("metrics_aggregator_errors_total %d", a.errorsTotal.Load()),
	)

	// Append scraped metric lines
	for _, r := range results {
		merged = append(merged, r.lines...)
	}

	// Check if any scrape succeeded
	anySuccess := false
	for _, r := range results {
		if r.success {
			anySuccess = true
			break
		}
	}
	if !anySuccess {
		a.errorsTotal.Add(1)
		return "", fmt.Errorf("no metrics collected")
	}

	return strings.Join(merged, "\n") + "\n", nil
}

// addCustomLabel injects origin_container into a metric line.
func addCustomLabel(metric, name string) string {
	parts := strings.SplitN(metric, " ", 2)
	if len(parts) != 2 {
		return metric
	}
	lbls, val := parts[0], parts[1]

	// C3: skip injection if origin_container is already a label key
	if originLabelKey.MatchString(lbls) {
		return metric
	}

	if strings.Contains(lbls, "{") {
		lbls = strings.Replace(lbls, "{", fmt.Sprintf("{origin_container=%q,", name), 1)
	} else {
		lbls = fmt.Sprintf("%s{origin_container=%q}", lbls, name)
	}
	return fmt.Sprintf("%s %s", lbls, val)
}

func currentSecurityMode() string {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv(securityModeEnvVariableName)))
	switch mode {
	case "", securityModeStrict:
		return securityModeStrict
	case securityModeLegacy:
		return securityModeLegacy
	default:
		return securityModeStrict
	}
}

func validateEndpointURL(rawURL, securityMode string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid URL format")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("unsupported scheme %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("missing host")
	}
	// Always reject link-local addresses, even in legacy mode.
	if err := rejectLinkLocal(parsed.Host); err != nil {
		return "", err
	}
	if securityMode == securityModeLegacy {
		log.Warn().Msg("METRICS_SECURITY_MODE=legacy is deprecated; switch to strict mode")
		return parsed.String(), nil
	}
	if parsed.User != nil {
		return "", fmt.Errorf("userinfo is not allowed")
	}
	if parsed.RawQuery != "" {
		return "", fmt.Errorf("query parameters are not allowed")
	}
	if parsed.Fragment != "" {
		return "", fmt.Errorf("fragments are not allowed")
	}
	path := parsed.EscapedPath()
	if path != "" && path != "/" && path != defaultMetricsPath {
		return "", fmt.Errorf("path must be %q", defaultMetricsPath)
	}
	if path == "" || path == "/" {
		parsed.Path = defaultMetricsPath
	}
	return parsed.String(), nil
}

func sanitizeURLForLog(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "<invalid-url>"
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.Redacted()
}

// rejectLinkLocal returns an error if the host resolves to a link-local address (169.254.0.0/16).
// Returns nil on DNS error — let the HTTP request fail naturally.
func rejectLinkLocal(host string) error {
	hostname := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		hostname = h
	}
	linkLocal := &net.IPNet{
		IP:   net.ParseIP("169.254.0.0"),
		Mask: net.CIDRMask(16, 32),
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(context.Background(), hostname)
	if err != nil {
		// DNS failure — let the HTTP client handle it
		return nil
	}
	for _, addr := range addrs {
		if linkLocal.Contains(addr.IP) {
			return fmt.Errorf("link-local address %s is not allowed", addr.IP)
		}
	}
	return nil
}

func isValidPrometheusSampleLine(line string) bool {
	return sampleLine.MatchString(line)
}
