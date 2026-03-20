// Package main runs the metrics-aggregator HTTP server.
package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/kaiohenricunha/metrics-aggregator/pkg/aggregator"
	"github.com/kaiohenricunha/metrics-aggregator/pkg/tracing"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
)

const (
	defaultReadHeaderTimeout = 2 * time.Second
	defaultReadTimeout       = 5 * time.Second
	defaultWriteTimeout      = 10 * time.Second
	defaultIdleTimeout       = 60 * time.Second
	defaultMaxHeaderBytes    = 1 << 20
	defaultCacheTTL          = 1 * time.Second
	defaultMaxInflight       = 32
)

func init() {
	zerolog.TimeFieldFormat = time.RFC3339
	level, err := zerolog.ParseLevel(os.Getenv("LOG_LEVEL"))
	if err != nil {
		level = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(level)
}

// statusRecorder wraps http.ResponseWriter to capture the status code.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

type httpServerConfig struct {
	readHeaderTimeout time.Duration
	readTimeout       time.Duration
	writeTimeout      time.Duration
	idleTimeout       time.Duration
	maxHeaderBytes    int
	cacheTTL          time.Duration
	maxInflight       int
}

type metricsCache struct {
	ttl       time.Duration
	mu        sync.Mutex
	value     string
	expiresAt time.Time
	inFlight  chan struct{}
}

var (
	randRead                 = rand.Read
	requestIDFallbackCounter atomic.Uint64
)

func (sr *statusRecorder) WriteHeader(code int) {
	sr.status = code
	sr.ResponseWriter.WriteHeader(code)
}

func (sr *statusRecorder) Write(p []byte) (int, error) {
	if sr.status == 0 {
		sr.status = http.StatusOK
	}
	n, err := sr.ResponseWriter.Write(p)
	sr.bytes += n
	return n, err
}

// generateRequestID returns a 32-char hex string from crypto/rand.
func generateRequestID() string {
	b := make([]byte, 16)
	n, err := randRead(b)
	if err != nil || n != len(b) {
		fallback := make([]byte, 16)
		binary.BigEndian.PutUint64(fallback[:8], uint64(time.Now().UnixNano()))
		binary.BigEndian.PutUint64(fallback[8:], requestIDFallbackCounter.Add(1))
		log.Warn().Err(err).Int("bytes_read", n).Msg("crypto/rand unavailable; using fallback request ID generator")
		return hex.EncodeToString(fallback)
	}
	return hex.EncodeToString(b)
}

// sanitizeRequestID strips non-printable characters from a request ID and truncates to 128 chars.
func sanitizeRequestID(id string) string {
	if len(id) > 128 {
		id = id[:128]
	}
	var buf strings.Builder
	for _, c := range id {
		if c >= 0x20 && c <= 0x7E {
			buf.WriteRune(c)
		}
	}
	return buf.String()
}

// requestIDMiddleware injects a request ID into the response and logger context.
// It also parses the W3C traceparent header for log correlation and downstream forwarding.
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			id = generateRequestID()
		} else {
			id = sanitizeRequestID(id)
			if id == "" {
				id = generateRequestID()
			}
		}
		w.Header().Set("X-Request-Id", id)

		logger := log.With().Str("request_id", id).Logger()
		ctx := logger.WithContext(r.Context())
		ctx = context.WithValue(ctx, aggregator.ContextKeyRequestID, id)

		// Parse W3C traceparent header: "00-<trace_id>-<span_id>-<flags>"
		// Only enrich the logger and forward the header when the value is fully valid.
		if tp := r.Header.Get("traceparent"); tp != "" {
			if traceID, spanID, ok := parseTraceparent(tp); ok {
				logger = logger.With().Str("trace_id", traceID).Str("span_id", spanID).Logger()
				ctx = logger.WithContext(ctx)
				ctx = context.WithValue(ctx, aggregator.ContextKeyTraceparent, tp)
			}
		}

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// parseTraceparent extracts trace_id and span_id from a W3C traceparent header.
// Format: "version-trace_id-span_id-flags" (e.g. "00-<32hex>-<16hex>-01")
// Validates all four fields per the W3C trace-context spec.
func parseTraceparent(tp string) (traceID, spanID string, ok bool) {
	parts := strings.Split(tp, "-")
	if len(parts) != 4 {
		return "", "", false
	}
	version, flags := parts[0], parts[3]
	// version: 2 lowercase hex chars; "ff" is reserved/invalid
	if len(version) != 2 || version == "ff" {
		return "", "", false
	}
	for _, c := range version {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return "", "", false
		}
	}
	traceID = parts[1]
	spanID = parts[2]
	if len(traceID) != 32 || len(spanID) != 16 {
		return "", "", false
	}
	for _, c := range traceID {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return "", "", false
		}
	}
	for _, c := range spanID {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return "", "", false
		}
	}
	// Reject all-zero IDs — invalid per W3C trace-context spec
	if traceID == "00000000000000000000000000000000" || spanID == "0000000000000000" {
		return "", "", false
	}
	// flags: 2 lowercase hex chars
	if len(flags) != 2 {
		return "", "", false
	}
	for _, c := range flags {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return "", "", false
		}
	}
	return traceID, spanID, true
}

func (c *metricsCache) getOrFetch(ctx context.Context, fetch func(context.Context) (string, error)) (string, error) {
	for {
		c.mu.Lock()
		if c.ttl > 0 && c.value != "" && time.Now().Before(c.expiresAt) {
			value := c.value
			c.mu.Unlock()
			return value, nil
		}
		if c.inFlight == nil {
			c.inFlight = make(chan struct{})
			c.mu.Unlock()
			break
		}
		wait := c.inFlight
		c.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	value, err := fetch(ctx)

	c.mu.Lock()
	if err == nil {
		if c.ttl > 0 {
			c.value = value
			c.expiresAt = time.Now().Add(c.ttl)
		} else {
			c.value = ""
			c.expiresAt = time.Time{}
		}
	}
	wait := c.inFlight
	c.inFlight = nil
	close(wait)
	c.mu.Unlock()

	return value, err
}

func makeMetricsHandler(agg *aggregator.Aggregator, cfg httpServerConfig) http.HandlerFunc {
	cache := &metricsCache{ttl: cfg.cacheTTL}
	inflightLimiter := make(chan struct{}, cfg.maxInflight)
	var httpRequests atomic.Int64
	httpDurationHist := aggregator.NewHistogram(aggregator.DefaultBuckets())

	return func(w http.ResponseWriter, r *http.Request) {
		httpRequests.Add(1)
		start := time.Now()
		defer func() { httpDurationHist.Observe(time.Since(start).Seconds()) }()
		logger := zerolog.Ctx(r.Context())

		w.Header().Set("Content-Type", "text/plain")
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		select {
		case inflightLimiter <- struct{}{}:
			defer func() { <-inflightLimiter }()
		default:
			http.Error(rec, "too many concurrent scrape requests", http.StatusServiceUnavailable)
			rec.status = http.StatusServiceUnavailable
			logger.Warn().
				Str("method", r.Method).
				Str("path", r.URL.Path).
				Int("status", rec.status).
				Dur("duration", time.Since(start)).
				Int("bytes", rec.bytes).
				Msg("request rejected by concurrency limit")
			return
		}

		metrics, err := cache.getOrFetch(r.Context(), agg.AggregateMetrics)
		if err != nil {
			logger.Error().Err(err).Msg("aggregation failure")
			http.Error(rec, "failed to aggregate metrics", http.StatusInternalServerError)
			rec.status = http.StatusInternalServerError
			logger.Info().
				Str("method", r.Method).
				Str("path", r.URL.Path).
				Int("status", rec.status).
				Dur("duration", time.Since(start)).
				Int("bytes", rec.bytes).
				Msg("request completed")
			return
		}

		// Append HTTP-level metrics (increment on every request, including cache hits)
		metrics += fmt.Sprintf(
			"# HELP metrics_aggregator_http_requests_total Total number of HTTP requests to the /metrics endpoint.\n"+
				"# TYPE metrics_aggregator_http_requests_total counter\n"+
				"metrics_aggregator_http_requests_total %d\n",
			httpRequests.Load(),
		)
		metrics += aggregator.RenderHeader("metrics_aggregator_http_request_duration_seconds", "Duration of HTTP requests to the /metrics endpoint in seconds.") + "\n"
		metrics += aggregator.RenderSamples("metrics_aggregator_http_request_duration_seconds", "", httpDurationHist) + "\n"

		_, _ = fmt.Fprint(rec, metrics)
		logger.Info().
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Int("status", rec.status).
			Dur("duration", time.Since(start)).
			Int("bytes", rec.bytes).
			Msg("request completed")
	}
}

// validatePort checks that raw is a valid TCP port number (1-65535).
func validatePort(raw string) (string, error) {
	n, err := strconv.Atoi(raw)
	if err != nil {
		return "", fmt.Errorf("invalid port %q: must be a number", raw)
	}
	if n <= 0 || n > 65535 {
		return "", fmt.Errorf("invalid port %d: must be in range 1-65535", n)
	}
	return raw, nil
}

func parseDurationEnv(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value < 0 {
		log.Warn().Str("env", name).Str("value", raw).Msg("invalid duration env; using default")
		return fallback
	}
	return value
}

func parsePositiveIntEnv(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		log.Warn().Str("env", name).Str("value", raw).Msg("invalid integer env; using default")
		return fallback
	}
	return value
}

func loadHTTPServerConfigFromEnv() httpServerConfig {
	return httpServerConfig{
		readHeaderTimeout: parseDurationEnv("METRICS_SERVER_READ_HEADER_TIMEOUT", defaultReadHeaderTimeout),
		readTimeout:       parseDurationEnv("METRICS_SERVER_READ_TIMEOUT", defaultReadTimeout),
		writeTimeout:      parseDurationEnv("METRICS_SERVER_WRITE_TIMEOUT", defaultWriteTimeout),
		idleTimeout:       parseDurationEnv("METRICS_SERVER_IDLE_TIMEOUT", defaultIdleTimeout),
		maxHeaderBytes:    parsePositiveIntEnv("METRICS_SERVER_MAX_HEADER_BYTES", defaultMaxHeaderBytes),
		cacheTTL:          parseDurationEnv("METRICS_CACHE_TTL", defaultCacheTTL),
		maxInflight:       parsePositiveIntEnv("METRICS_MAX_INFLIGHT", defaultMaxInflight),
	}
}

// tracingMiddleware extracts inbound W3C trace context and creates a span for each request.
func tracingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		tracer := otel.Tracer("metrics-aggregator")
		ctx, span := tracer.Start(ctx, r.Method+" "+r.URL.Path)
		defer span.End()

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r.WithContext(ctx))

		span.SetAttributes(
			attribute.String("http.method", r.Method),
			attribute.Int("http.status_code", rec.status),
		)
		if rec.status >= 400 {
			span.SetStatus(codes.Error, http.StatusText(rec.status))
		}
	})
}

func run(ctx context.Context, agg *aggregator.Aggregator, addr string) error {
	cfg := loadHTTPServerConfigFromEnv()
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "ok")
	})
	mux.HandleFunc("/metrics", makeMetricsHandler(agg, cfg))

	handler := requestIDMiddleware(tracingMiddleware(mux))
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: cfg.readHeaderTimeout,
		ReadTimeout:       cfg.readTimeout,
		WriteTimeout:      cfg.writeTimeout,
		IdleTimeout:       cfg.idleTimeout,
		MaxHeaderBytes:    cfg.maxHeaderBytes,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	err := srv.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func main() {
	shutdownTracing, err := tracing.InitTracing("metrics-aggregator")
	if err != nil {
		log.Fatal().Err(err).Msg("tracing setup failed")
	}
	defer func() {
		if err := shutdownTracing(context.Background()); err != nil {
			log.Error().Err(err).Msg("tracing shutdown error")
		}
	}()

	agg, err := aggregator.NewAggregator(os.Getenv("METRICS_ENDPOINTS"))
	if err != nil {
		log.Fatal().Err(err).Msg("setup endpoints failed")
	}

	port := os.Getenv("METRICS_AGGREGATOR_PORT")
	if port == "" {
		port = aggregator.DefaultAggregatorPort
	}
	if _, err := validatePort(port); err != nil {
		log.Fatal().Err(err).Msg("invalid METRICS_AGGREGATOR_PORT")
	}
	bindAddr := os.Getenv("METRICS_BIND_ADDRESS")
	addr := bindAddr + ":" + port

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	log.Info().Str("addr", addr).Msg("HTTP server starting")
	if err := run(ctx, agg, addr); err != nil {
		log.Fatal().Err(err).Msg("HTTP server exited")
	}
	log.Info().Msg("HTTP server stopped gracefully")
}
