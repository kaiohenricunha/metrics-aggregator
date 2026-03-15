// Package aggregator scrapes Prometheus-formatted metrics from multiple
// endpoints and merges them into a single output.
package aggregator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// centralised port definitions
const (
	DefaultAggregatorPort  = "9090"
	metricsEnvVariableName = "METRICS_ENDPOINTS"
	maxBodySize            = 10 << 20 // 10 MiB
)

var validName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// Endpoint represents a Prometheus /metrics endpoint.
type Endpoint struct {
	Name string
	URL  string
}

type scrapeResult struct {
	lines    []string
	success  bool
	duration time.Duration
}

// Aggregator scrapes and merges Prometheus metrics from multiple endpoints.
type Aggregator struct {
	endpoints     []Endpoint
	client        *http.Client
	logger        zerolog.Logger
	requestsTotal atomic.Int64
	errorsTotal   atomic.Int64
}

// NewAggregator parses endpointsConfig (JSON map or comma-separated URLs)
// and returns an initialised Aggregator.
func NewAggregator(endpointsConfig string) (*Aggregator, error) {
	if strings.TrimSpace(endpointsConfig) == "" {
		return nil, fmt.Errorf("%s not defined", metricsEnvVariableName)
	}

	var parsed []Endpoint

	// 1) try to parse as JSON map
	var endpointMap map[string]string
	if err := json.Unmarshal([]byte(endpointsConfig), &endpointMap); err == nil {
		for name, url := range endpointMap {
			parsed = append(parsed, Endpoint{Name: name, URL: url})
		}
	} else {
		// 2) fallback: comma-separated URLs
		log.Warn().
			Err(err).
			Str("env", endpointsConfig).
			Msg("failed JSON parse, trying comma-separated list")

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
	for _, ep := range parsed {
		if !validName.MatchString(ep.Name) {
			return nil, fmt.Errorf("invalid endpoint name %q: must match [a-zA-Z0-9_-]+", ep.Name)
		}
	}

	if len(parsed) == 0 {
		return nil, fmt.Errorf("no valid endpoints found in %s", metricsEnvVariableName)
	}

	agg := &Aggregator{
		endpoints: parsed,
		client:    &http.Client{Timeout: 5 * time.Second},
		logger:    log.Logger,
	}

	agg.logger.Info().Msg("aggregating metrics from configured endpoints")
	for _, ep := range agg.endpoints {
		agg.logger.Info().Str("name", ep.Name).Str("url", ep.URL).Msg("endpoint registered")
	}
	return agg, nil
}

// Endpoints returns the configured endpoints (read-only copy).
func (a *Aggregator) Endpoints() []Endpoint {
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
			start := time.Now()

			resp, err := a.client.Get(ep.URL)
			if err != nil {
				logger.Error().Err(err).Str("url", ep.URL).Msg("HTTP GET failed")
				results[idx] = scrapeResult{duration: time.Since(start)}
				return
			}
			if resp.StatusCode != http.StatusOK {
				logger.Warn().Int("status_code", resp.StatusCode).Str("url", ep.URL).Msg("non-200 response")
				resp.Body.Close()
				results[idx] = scrapeResult{duration: time.Since(start)}
				return
			}

			body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
			resp.Body.Close()
			if err != nil {
				logger.Error().Err(err).Str("url", ep.URL).Msg("read body failed")
				results[idx] = scrapeResult{duration: time.Since(start)}
				return
			}

			// C1: strip comment/metadata lines; only keep metric data lines
			var lines []string
			for _, line := range strings.Split(string(body), "\n") {
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				lines = append(lines, addCustomLabel(line, ep.Name))
			}
			results[idx] = scrapeResult{
				lines:    lines,
				success:  true,
				duration: time.Since(start),
			}
		}(i, ep)
	}
	wg.Wait()

	// Build self-instrumentation metrics (M1)
	var merged []string
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
	merged = append(merged,
		"# HELP metrics_aggregator_scrape_duration_seconds Duration of the last scrape in seconds.",
		"# TYPE metrics_aggregator_scrape_duration_seconds gauge",
	)
	for i, ep := range a.endpoints {
		merged = append(merged, fmt.Sprintf("metrics_aggregator_scrape_duration_seconds{endpoint=%q} %.3f", ep.Name, results[i].duration.Seconds()))
	}
	merged = append(merged,
		"# HELP metrics_aggregator_requests_total Total number of /metrics requests served.",
		"# TYPE metrics_aggregator_requests_total counter",
		fmt.Sprintf("metrics_aggregator_requests_total %d", a.requestsTotal.Load()),
		"# HELP metrics_aggregator_errors_total Total number of failed /metrics requests (all endpoints down).",
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

	// C3: skip injection if origin_container already present
	if strings.Contains(lbls, "origin_container=") {
		return metric
	}

	if strings.Contains(lbls, "{") {
		lbls = strings.Replace(lbls, "{", fmt.Sprintf("{origin_container=%q,", name), 1)
	} else {
		lbls = fmt.Sprintf("%s{origin_container=%q}", lbls, name)
	}
	return fmt.Sprintf("%s %s", lbls, val)
}
