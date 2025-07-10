package aggregator

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// TestSetupEndpoints error when env-var missing and success when set.
func TestSetupEndpoints(t *testing.T) {
	// Case 1: env not set → expect error
	endpoints = nil
	os.Unsetenv(metricsEnvVariableName)
	if err := SetupEndpoints(); err == nil {
		t.Fatalf("expected error when %s is unset", metricsEnvVariableName)
	}

	// Case 2: valid JSON map
	endpoints = nil
	env := `{"svc1":"http://localhost:9090/metrics","svc2":"http://localhost:8082/metrics"}`
	os.Setenv(metricsEnvVariableName, env)
	if err := SetupEndpoints(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(endpoints) != 2 {
		t.Fatalf("expected 2 endpoints, got %d", len(endpoints))
	}
	if endpoints[0].Name != "svc1" || endpoints[1].Name != "svc2" {
		t.Fatalf("endpoint names wrong: %+v", endpoints)
	}
}

// TestAggregateMetrics ensures scrape + relabel works.
func TestAggregateMetrics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`# HELP m A metric
# TYPE m counter
m{l="v"} 1`))
	}))
	defer server.Close()

	endpoints = []Endpoint{{Name: "test", URL: server.URL}}

	res, err := AggregateMetrics()
	if err != nil {
		t.Fatalf("aggregate error: %v", err)
	}
	want := `m{origin_container="test",l="v"} 1`
	if !strings.Contains(res, want) {
		t.Fatalf("want %q in output, got %q", want, res)
	}
}

// TestAddCustomLabel checks label injection.
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
