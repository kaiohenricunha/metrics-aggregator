package main

import (
	"fmt"
	"net/http"
	"os"
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

// metricsHandler serves the aggregated /metrics output.
func metricsHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	log.Info().Msg("/metrics request started")

	w.Header().Set("Content-Type", "text/plain")
	metrics, err := aggregator.AggregateMetrics()
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

func main() {
	if err := aggregator.SetupEndpoints(); err != nil {
		log.Fatal().Err(err).Msg("setup endpoints failed")
	}

	port := os.Getenv("METRICS_AGGREGATOR_PORT")
	if port == "" {
		port = aggregator.DefaultAggregatorPort
	}
	addr := ":" + port

	log.Info().Str("addr", addr).Msg("HTTP server starting")
	http.HandleFunc("/metrics", metricsHandler)

	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatal().Err(err).Msg("HTTP server exited")
	}
}
