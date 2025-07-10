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

// centralised port definitions
const (
	DefaultAggregatorPort  = "9090"
	DefaultPrometheusPort  = "9090"
	metricsEnvVariableName = "METRICS_ENDPOINTS"
)

// Endpoint represents a Prometheus /metrics endpoint.
type Endpoint struct {
	Name string
	URL  string
}

var endpoints []Endpoint

// SetupEndpoints populates the global endpoints slice.
// It returns an error when METRICS_ENDPOINTS is unset or malformed.
func SetupEndpoints() error {
	zerolog.TimeFieldFormat = time.RFC3339
	endpoints = nil // clear previous state

	env := os.Getenv(metricsEnvVariableName)
	if strings.TrimSpace(env) == "" {
		return fmt.Errorf("%s not defined", metricsEnvVariableName)
	}

	// 1) try to parse as JSON map
	var endpointMap map[string]string
	if err := json.Unmarshal([]byte(env), &endpointMap); err == nil {
		for name, url := range endpointMap {
			endpoints = append(endpoints, Endpoint{Name: name, URL: url})
		}
	} else {
		// 2) fallback: comma-separated URLs
		log.Warn().
			Err(err).
			Str("env", env).
			Msg("failed JSON parse, trying comma-separated list")

		for i, url := range strings.Split(env, ",") {
			url = strings.TrimSpace(url)
			if url != "" {
				endpoints = append(endpoints, Endpoint{
					Name: fmt.Sprintf("endpoint%d", i+1),
					URL:  url,
				})
			}
		}
	}

	if len(endpoints) == 0 {
		return fmt.Errorf("no valid endpoints found in %s", metricsEnvVariableName)
	}

	log.Info().Msg("aggregating metrics from configured endpoints")
	for _, ep := range endpoints {
		log.Info().Str("name", ep.Name).Str("url", ep.URL).Msg("endpoint registered")
	}
	return nil
}

// AggregateMetrics fetches metrics, injects origin_container labels and merges them.
func AggregateMetrics() (string, error) {
	if len(endpoints) == 0 {
		return "", fmt.Errorf("no endpoints configured")
	}

	var merged []string
	for _, ep := range endpoints {
		resp, err := http.Get(ep.URL)
		if err != nil {
			log.Error().Err(err).Str("url", ep.URL).Msg("HTTP GET failed")
			continue
		}
		if resp.StatusCode != http.StatusOK {
			log.Warn().Int("status_code", resp.StatusCode).Str("url", ep.URL).Msg("non-200 response")
			resp.Body.Close()
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			log.Error().Err(err).Str("url", ep.URL).Msg("read body failed")
			continue
		}

		metrics := strings.Split(string(body), "\n")
		for i, m := range metrics {
			if strings.HasPrefix(m, "#") || m == "" {
				continue
			}
			metrics[i] = addCustomLabel(m, ep.Name)
		}
		merged = append(merged, strings.Join(metrics, "\n"))
	}

	if len(merged) == 0 {
		return "", fmt.Errorf("no metrics collected")
	}
	return strings.Join(merged, "\n"), nil
}

// addCustomLabel injects origin_container into a metric line.
func addCustomLabel(metric, name string) string {
	parts := strings.SplitN(metric, " ", 2)
	if len(parts) != 2 {
		return metric
	}
	lbls, val := parts[0], parts[1]

	if strings.Contains(lbls, "{") {
		lbls = strings.Replace(lbls, "{", fmt.Sprintf("{origin_container=\"%s\",", name), 1)
	} else {
		lbls = fmt.Sprintf("%s{origin_container=\"%s\"}", lbls, name)
	}
	return fmt.Sprintf("%s %s", lbls, val)
}
