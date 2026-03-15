// Package main runs the metrics-aggregator HTTP server.
package main

import (
	"context"
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

func makeMetricsHandler(agg *aggregator.Aggregator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		log.Info().Msg("/metrics request started")

		w.Header().Set("Content-Type", "text/plain")
		metrics, err := agg.AggregateMetrics()
		if err != nil {
			log.Error().Err(err).Msg("aggregation failure")
			http.Error(w, "failed to aggregate metrics", http.StatusInternalServerError)
			return
		}

		fmt.Fprint(w, metrics)
		log.Info().
			Dur("duration", time.Since(start)).
			Int("bytes", len(metrics)).
			Msg("/metrics request completed")
	}
}

func run(ctx context.Context, agg *aggregator.Aggregator, addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "ok")
	})
	mux.HandleFunc("/metrics", makeMetricsHandler(agg))

	srv := &http.Server{Addr: addr, Handler: mux}

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
