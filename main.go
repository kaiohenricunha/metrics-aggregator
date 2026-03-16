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
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
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

// requestIDMiddleware injects a request ID into the response and logger context.
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			id = generateRequestID()
		}
		w.Header().Set("X-Request-Id", id)

		logger := log.With().Str("request_id", id).Logger()
		ctx := logger.WithContext(r.Context())
		next.ServeHTTP(w, r.WithContext(ctx))
	})
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

func run(ctx context.Context, agg *aggregator.Aggregator, addr string) error {
	cfg := loadHTTPServerConfigFromEnv()
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "ok")
	})
	mux.HandleFunc("/metrics", makeMetricsHandler(agg, cfg))

	handler := requestIDMiddleware(mux)
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
	agg, err := aggregator.NewAggregator(os.Getenv("METRICS_ENDPOINTS"))
	if err != nil {
		log.Fatal().Err(err).Msg("setup endpoints failed")
	}

	port := os.Getenv("METRICS_AGGREGATOR_PORT")
	if port == "" {
		port = aggregator.DefaultAggregatorPort
	}
	addr := ":" + port

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	log.Info().Str("addr", addr).Msg("HTTP server starting")
	if err := run(ctx, agg, addr); err != nil {
		log.Fatal().Err(err).Msg("HTTP server exited")
	}
	log.Info().Msg("HTTP server stopped gracefully")
}
