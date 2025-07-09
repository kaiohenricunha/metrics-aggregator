package aggregator

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Endpoint represents a Prometheus /metrics endpoint.
type Endpoint struct {
	Name string
	URL  string
}

var endpoints []Endpoint

// SetupEndpoints initializes the endpoints slice from the METRICS_ENDPOINTS
// environment variable. If the variable is empty, two demo endpoints are used.
func SetupEndpoints() {
	zerolog.TimeFieldFormat = time.RFC3339
	env := os.Getenv("METRICS_ENDPOINTS")

	if env == "" {
		endpoints = []Endpoint{
			{Name: "service1", URL: "http://service1:8080/metrics"},
			{Name: "service2", URL: "http://service2:8081/metrics"},
		}
	} else {
		var endpointMap map[string]string
		if err := json.Unmarshal([]byte(env), &endpointMap); err != nil {
			log.Fatal().Err(err).Msg("failed to parse METRICS_ENDPOINTS")
		}
		for name, url := range endpointMap {
			endpoints = append(endpoints, Endpoint{Name: name, URL: url})
		}
	}

	log.Info().Msg("aggregating metrics from configured endpoints")
	for _, ep := range endpoints {
		log.Info().
			Str("name", ep.Name).
			Str("url", ep.URL).
			Msg("endpoint registered")
	}
}

// AggregateMetrics fetches metrics from all endpoints, adds the
// origin_container label, and returns the merged result.
func AggregateMetrics() (string, error) {
	var merged []string

	for _, ep := range endpoints {
		resp, err := http.Get(ep.URL)
		if err != nil {
			log.Error().
				Err(err).
				Str("url", ep.URL).
				Msg("HTTP GET failed")
			continue
		}
		if resp.StatusCode != http.StatusOK {
			log.Warn().
				Int("status_code", resp.StatusCode).
				Str("url", ep.URL).
				Msg("non-200 response")
			resp.Body.Close()
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			log.Error().
				Err(err).
				Str("url", ep.URL).
				Msg("failed to read body")
			continue
		}

		metrics := strings.Split(string(body), "\n")
		for i, metric := range metrics {
			if strings.HasPrefix(metric, "#") || metric == "" {
				continue
			}
			metrics[i] = addCustomLabel(metric, ep.Name)
		}
		merged = append(merged, strings.Join(metrics, "\n"))
	}

	if len(merged) == 0 {
		return "", fmt.Errorf("no metrics collected")
	}
	return strings.Join(merged, "\n"), nil
}

// addCustomLabel appends or injects the origin_container label.
func addCustomLabel(metric, name string) string {
	parts := strings.SplitN(metric, " ", 2)
	if len(parts) != 2 {
		return metric
	}
	metricNameAndLabels := parts[0]
	value := parts[1]

	if strings.Contains(metricNameAndLabels, "{") {
		metricNameAndLabels = strings.Replace(
			metricNameAndLabels,
			"{",
			fmt.Sprintf("{origin_container=\"%s\",", name),
			1,
		)
	} else {
		metricNameAndLabels = fmt.Sprintf(
			"%s{origin_container=\"%s\"}",
			metricNameAndLabels,
			name,
		)
	}

	return fmt.Sprintf("%s %s", metricNameAndLabels, value)
}
