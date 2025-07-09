package aggregator

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// TestSetupEndpoints verifies correct initialisation from env-var and fallback.
func TestSetupEndpoints(t *testing.T) {
	// Case 1: default endpoints
	endpoints = nil
	os.Unsetenv("METRICS_ENDPOINTS")
	SetupEndpoints()
	if len(endpoints) != 2 {
		t.Fatalf("expected 2 default endpoints, got %d", len(endpoints))
	}

	// Case 2: custom env-var
	endpoints = nil
	env := `{"my-release":"http://localhost:8080/metrics","sidecar":"http://localhost:8082/metrics"}`
	os.Setenv("METRICS_ENDPOINTS", env)
	SetupEndpoints()
	if len(endpoints) != 2 {
		t.Fatalf("expected 2 env endpoints, got %d", len(endpoints))
	}
	if endpoints[0].Name != "my-release" || endpoints[1].Name != "sidecar" {
		t.Fatalf("unexpected endpoints: %+v", endpoints)
	}
}

// TestAggregateMetrics checks that metrics are fetched and relabelled.
func TestAggregateMetrics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(`# HELP test_metric help
# TYPE test_metric counter
test_metric{label="value"} 1`)); err != nil {
			t.Fatalf("write failed: %v", err)
		}
	}))
	defer server.Close()

	endpoints = []Endpoint{{Name: "test-service", URL: server.URL}}

	metrics, err := AggregateMetrics()
	if err != nil {
		t.Fatalf("aggregate error: %v", err)
	}
	want := `test_metric{origin_container="test-service",label="value"} 1`
	if !strings.Contains(metrics, want) {
		t.Fatalf("expected %q, got %q", want, metrics)
	}
}

// TestAddCustomLabel ensures label injection works with and without existing labels.
func TestAddCustomLabel(t *testing.T) {
	got := addCustomLabel(`metric{a="b"} 1`, "svc")
	want := `metric{origin_container="svc",a="b"} 1`
	if got != want {
		t.Fatalf("want %q, got %q", want, got)
	}

	got = addCustomLabel(`metric 1`, "svc")
	want = `metric{origin_container="svc"} 1`
	if got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
}
