package aggregator

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// TestSetupEndpoints tests the SetupEndpoints function to ensure it initializes the endpoints correctly
func TestSetupEndpoints(t *testing.T) {
	// Clear the endpoints slice
	endpoints = []Endpoint{}

	// Test with no environment variable set
	os.Unsetenv("METRICS_ENDPOINTS")
	SetupEndpoints()
	if len(endpoints) != 2 {
		t.Errorf("Expected 2 endpoints, got %d", len(endpoints))
	}

	// Clear the endpoints slice
	endpoints = []Endpoint{}

	// Test with environment variable set
	env := `{"my-release":"http://localhost:8080/metrics","sidecar-same-image":"http://localhost:8082/metrics"}`
	os.Setenv("METRICS_ENDPOINTS", env)
	SetupEndpoints()
	if len(endpoints) != 2 {
		t.Errorf("Expected 2 endpoints, got %d", len(endpoints))
	}
	if endpoints[0].Name != "my-release" || endpoints[0].URL != "http://localhost:8080/metrics" {
		t.Errorf("Unexpected endpoint: %+v", endpoints[0])
	}
	if endpoints[1].Name != "sidecar-same-image" || endpoints[1].URL != "http://localhost:8082/metrics" {
		t.Errorf("Unexpected endpoint: %+v", endpoints[1])
	}
}

// TestAggregateMetrics tests the AggregateMetrics function to ensure it correctly aggregates metrics from endpoints
func TestAggregateMetrics(t *testing.T) {
	// Mock HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`# HELP test_metric A test metric
# TYPE test_metric counter
test_metric{label="value"} 1.0
`))
	}))
	defer server.Close()

	// Set up endpoints to use the mock server
	endpoints = []Endpoint{
		{Name: "test-service", URL: server.URL},
	}

	metrics, err := AggregateMetrics()
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	expected := `test_metric{origin_container="test-service",label="value"} 1.0`
	if !strings.Contains(metrics, expected) {
		t.Errorf("Expected metrics to contain %q, got %q", expected, metrics)
	}
}

// TestAddCustomLabel tests the addCustomLabel function to ensure it correctly adds a custom label to metrics
// It checks both cases: when the metric has existing labels and when it does not.
// It also ensures that the custom label is added correctly.
func TestAddCustomLabel(t *testing.T) {
	metric := `test_metric{label="value"} 1.0`
	name := "test-service"
	expected := `test_metric{origin_container="test-service",label="value"} 1.0`
	result := addCustomLabel(metric, name)
	if result != expected {
		t.Errorf("Expected %q, got %q", expected, result)
	}

	metric = `test_metric 1.0`
	expected = `test_metric{origin_container="test-service"} 1.0`
	result = addCustomLabel(metric, name)
	if result != expected {
		t.Errorf("Expected %q, got %q", expected, result)
	}
}
