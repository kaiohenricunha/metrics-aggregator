package aggregator

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
)

type Endpoint struct {
	Name string
	URL  string
}

var endpoints []Endpoint

// SetupEndpoints initializes the endpoints from the environment variable METRICS_ENDPOINTS
// If the variable is not set, it defaults to a predefined list of endpoints.
func SetupEndpoints() {
	env := os.Getenv("METRICS_ENDPOINTS")
	if env == "" {
		endpoints = []Endpoint{
			{Name: "service1", URL: "http://service1:8080/metrics"},
			{Name: "service2", URL: "http://service2:8081/metrics"},
		}
	} else {
		var endpointMap map[string]string
		err := json.Unmarshal([]byte(env), &endpointMap)
		if err != nil {
			log.Fatalf("Error parsing METRICS_ENDPOINTS: %v", err)
		}
		for name, url := range endpointMap {
			endpoints = append(endpoints, Endpoint{Name: name, URL: url})
		}
	}

	log.Println("Aggregating metrics from the following endpoints:")
	for _, endpoint := range endpoints {
		log.Printf("Name: %s, URL: %s\n", endpoint.Name, endpoint.URL)
	}
}

// AggregateMetrics fetches metrics from all configured endpoints,
// adds a custom label to each metric, and merges them into a single string.
// It returns the aggregated metrics or an error if no metrics were collected.
func AggregateMetrics() (string, error) {
	var merged []string
	for _, ep := range endpoints {
		resp, err := http.Get(ep.URL)
		if err != nil {
			fmt.Printf("Error fetching from %s: %v\n", ep.URL, err)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			fmt.Printf("Non-OK HTTP status from %s: %s\n", ep.URL, resp.Status)
			resp.Body.Close()
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			fmt.Printf("Error reading response body from %s: %v\n", ep.URL, err)
			continue
		}
		// Add custom label to each metric
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

// addCustomLabel adds a custom label to the metric string.
// It expects the metric to be in the format "metric_name{labels} value".
// If the metric does not have labels, it adds the label "origin_container" with the given name.
func addCustomLabel(metric, name string) string {
	parts := strings.SplitN(metric, " ", 2)
	if len(parts) != 2 {
		return metric
	}
	metricNameAndLabels := parts[0]
	value := parts[1]

	if strings.Contains(metricNameAndLabels, "{") {
		metricNameAndLabels = strings.Replace(metricNameAndLabels, "{", fmt.Sprintf("{origin_container=\"%s\",", name), 1)
	} else {
		metricNameAndLabels = fmt.Sprintf("%s{origin_container=\"%s\"}", metricNameAndLabels, name)
	}

	return fmt.Sprintf("%s %s", metricNameAndLabels, value)
}
