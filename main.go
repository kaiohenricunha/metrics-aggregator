package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/kaiohenricunha/metrics-aggregator/pkg/aggregator"
)

// metricsHandler handles requests to the /metrics endpoint.
// It aggregates metrics from all configured endpoints and returns them as a single response.
func metricsHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("Received request for /metrics")
	w.Header().Set("Content-Type", "text/plain")
	metrics, err := aggregator.AggregateMetrics()
	if err != nil {
		log.Printf("Error aggregating metrics: %v\n", err)
		http.Error(w, "Failed to aggregate metrics", http.StatusInternalServerError)
		return
	}
	fmt.Fprint(w, metrics)
	log.Println("Successfully responded to /metrics request")
}

// main initializes the metrics aggregator and starts the HTTP server.
// It sets up the endpoints based on the environment variable METRICS_ENDPOINTS or defaults to predefined endpoints.
// The server listens on the port specified by the METRICS_AGGREGATOR_PORT environment variable, defaulting to 9090 if not set.
func main() {
	aggregator.SetupEndpoints()
	port := os.Getenv("METRICS_AGGREGATOR_PORT")
	if port == "" {
		port = "9090"
	}
	addr := ":" + port
	log.Printf("Starting server on %s\n", addr)
	http.HandleFunc("/metrics", metricsHandler)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}
