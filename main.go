// Package main runs the metrics-aggregator HTTP server.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kaiohenricunha/metrics-aggregator/pkg/aggregator"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
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
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.status = code
	sr.ResponseWriter.WriteHeader(code)
}

// generateRequestID returns a 32-char hex string from crypto/rand.
func generateRequestID() string {
	b := make([]byte, 16)
	rand.Read(b)
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

func makeMetricsHandler(agg *aggregator.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		logger := zerolog.Ctx(r.Context())

		w.Header().Set("Content-Type", "text/plain")
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		metrics, err := agg.AggregateMetrics()
		if err != nil {
			logger.Error().Err(err).Msg("aggregation failure")
			http.Error(rec, "failed to aggregate metrics", http.StatusInternalServerError)
			rec.status = http.StatusInternalServerError
			logger.Info().
				Str("method", r.Method).
				Str("path", r.URL.Path).
				Int("status", rec.status).
				Dur("duration", time.Since(start)).
				Int("bytes", 0).
				Msg("request completed")
			return
		}

		n, _ := fmt.Fprint(rec, metrics)
		logger.Info().
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Int("status", rec.status).
			Dur("duration", time.Since(start)).
			Int("bytes", n).
			Msg("request completed")
	}
}

func run(ctx context.Context, agg *aggregator.Aggregator, addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "ok")
	})
	mux.HandleFunc("/metrics", makeMetricsHandler(agg))

	handler := requestIDMiddleware(mux)
	srv := &http.Server{Addr: addr, Handler: handler}

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
