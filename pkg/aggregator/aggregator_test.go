package aggregator

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// TestNewAggregator tests the Aggregator constructor with various inputs.
func TestNewAggregator(t *testing.T) {
	t.Run("valid JSON map", func(t *testing.T) {
		agg, err := NewAggregator(`{"svc1":"http://a/metrics","svc2":"http://b/metrics"}`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		eps := agg.getEndpoints()
		if len(eps) != 2 {
			t.Fatalf("expected 2 endpoints, got %d", len(eps))
		}
		names := map[string]bool{}
		for _, ep := range eps {
			names[ep.Name] = true
		}
		if !names["svc1"] || !names["svc2"] {
			t.Fatalf("endpoint names wrong: %+v", eps)
		}
	})

	t.Run("valid CSV", func(t *testing.T) {
		agg, err := NewAggregator("http://a/metrics,http://b/metrics")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		eps := agg.getEndpoints()
		if len(eps) != 2 {
			t.Fatalf("expected 2 endpoints, got %d", len(eps))
		}
		if eps[0].Name != "endpoint1" || eps[1].Name != "endpoint2" {
			t.Fatalf("expected auto-names, got %+v", eps)
		}
	})

	t.Run("invalid names rejected", func(t *testing.T) {
		_, err := NewAggregator(`{"svc bad":"http://a/metrics"}`)
		if err == nil {
			t.Fatal("expected error for invalid name")
		}
	})

	t.Run("empty input rejected", func(t *testing.T) {
		_, err := NewAggregator("")
		if err == nil {
			t.Fatal("expected error for empty input")
		}
	})

	t.Run("name with quotes rejected", func(t *testing.T) {
		_, err := NewAggregator(`{"svc\"bad":"http://a/metrics"}`)
		if err == nil {
			t.Fatal("expected error for invalid name")
		}
	})

	t.Run("name with newline rejected", func(t *testing.T) {
		_, err := NewAggregator("{\"svc\\nbad\":\"http://a/metrics\"}")
		if err == nil {
			t.Fatal("expected error for invalid name")
		}
	})

	t.Run("valid hyphens underscores digits", func(t *testing.T) {
		_, err := NewAggregator(`{"svc-1_test":"http://a/metrics"}`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("only commas no URLs", func(t *testing.T) {
		_, err := NewAggregator(",")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("URLs with spaces and empty entries", func(t *testing.T) {
		agg, err := NewAggregator("http://a:9090/metrics, ,http://b:9090/metrics")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		eps := agg.getEndpoints()
		if len(eps) != 2 {
			t.Fatalf("expected 2 endpoints, got %d", len(eps))
		}
		if eps[0].Name != "endpoint1" || eps[1].Name != "endpoint3" {
			t.Fatalf("expected endpoint1,endpoint3, got %+v", eps)
		}
	})

	t.Run("single URL", func(t *testing.T) {
		agg, err := NewAggregator("http://a:9090/metrics")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		eps := agg.getEndpoints()
		if len(eps) != 1 {
			t.Fatalf("expected 1 endpoint, got %d", len(eps))
		}
		if eps[0].Name != "endpoint1" {
			t.Fatalf("expected endpoint1, got %s", eps[0].Name)
		}
	})
}

// newTestAggregator creates an Aggregator from endpoints directly (for tests).
func newTestAggregator(eps []Endpoint) *Aggregator {
	scrapeDurationHist := make(map[string]*Histogram, len(eps))
	for _, ep := range eps {
		scrapeDurationHist[ep.Name] = NewHistogram(DefaultBuckets())
	}
	return &Aggregator{
		endpoints:          eps,
		client:             &http.Client{},
		logger:             log.Logger,
		scrapeErrors:       make([]atomic.Int64, len(eps)),
		scrapeDurationHist: scrapeDurationHist,
	}
}

// TestAggregator_AggregateMetrics tests struct-based aggregation.
func TestAggregator_AggregateMetrics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("# HELP m A metric\n# TYPE m counter\nm{l=\"v\"} 1"))
	}))
	defer server.Close()

	config := fmt.Sprintf(`{"test":"%s"}`, server.URL)
	agg, err := NewAggregator(config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	res, err := agg.AggregateMetrics(context.Background())
	if err != nil {
		t.Fatalf("aggregate error: %v", err)
	}
	want := `m{origin_container="test",l="v"} 1`
	if !strings.Contains(res, want) {
		t.Fatalf("want %q in output, got %q", want, res)
	}
}

// TestAggregator_Isolation verifies two Aggregator instances don't interfere.
func TestAggregator_Isolation(t *testing.T) {
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("metric_a 1\n"))
	}))
	defer srv1.Close()
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("metric_b 2\n"))
	}))
	defer srv2.Close()

	agg1, err := NewAggregator(fmt.Sprintf(`{"svc1":"%s"}`, srv1.URL))
	if err != nil {
		t.Fatal(err)
	}
	agg2, err := NewAggregator(fmt.Sprintf(`{"svc2":"%s"}`, srv2.URL))
	if err != nil {
		t.Fatal(err)
	}

	res1, err := agg1.AggregateMetrics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	res2, err := agg2.AggregateMetrics(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(res1, `origin_container="svc1"`) {
		t.Fatalf("agg1 should contain svc1, got: %s", res1)
	}
	if strings.Contains(res1, "svc2") {
		t.Fatalf("agg1 should not contain svc2, got: %s", res1)
	}
	if !strings.Contains(res2, `origin_container="svc2"`) {
		t.Fatalf("agg2 should contain svc2, got: %s", res2)
	}
	if strings.Contains(res2, "svc1") {
		t.Fatalf("agg2 should not contain svc1, got: %s", res2)
	}
}

// TestAggregator_ErrorPaths tests best-effort behavior (T1).
func TestAggregator_ErrorPaths(t *testing.T) {
	t.Run("no endpoints", func(t *testing.T) {
		agg := newTestAggregator(nil)
		_, err := agg.AggregateMetrics(context.Background())
		if err == nil || !strings.Contains(err.Error(), "no endpoints configured") {
			t.Fatalf("expected 'no endpoints configured', got %v", err)
		}
	})

	t.Run("single endpoint 500", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()
		agg := newTestAggregator([]Endpoint{{Name: "bad", URL: srv.URL}})
		_, err := agg.AggregateMetrics(context.Background())
		if err == nil {
			t.Fatal("expected error when only endpoint returns 500")
		}
	})

	t.Run("unreachable endpoint", func(t *testing.T) {
		agg := newTestAggregator([]Endpoint{{Name: "down", URL: "http://127.0.0.1:1"}})
		_, err := agg.AggregateMetrics(context.Background())
		if err == nil {
			t.Fatal("expected error for unreachable endpoint")
		}
	})

	t.Run("both endpoints fail", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer srv.Close()
		agg := newTestAggregator([]Endpoint{
			{Name: "bad1", URL: srv.URL},
			{Name: "bad2", URL: "http://127.0.0.1:1"},
		})
		_, err := agg.AggregateMetrics(context.Background())
		if err == nil {
			t.Fatal("expected error when all endpoints fail")
		}
	})

	t.Run("partial success", func(t *testing.T) {
		good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte("up 1\n"))
		}))
		defer good.Close()
		bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer bad.Close()

		agg := newTestAggregator([]Endpoint{
			{Name: "healthy", URL: good.URL},
			{Name: "broken", URL: bad.URL},
		})
		res, err := agg.AggregateMetrics(context.Background())
		if err != nil {
			t.Fatalf("partial success should not error: %v", err)
		}
		if !strings.Contains(res, `origin_container="healthy"`) {
			t.Fatalf("missing healthy endpoint data in: %s", res)
		}
		if !strings.Contains(res, `metrics_aggregator_scrape_success{endpoint="healthy"} 1`) {
			t.Fatal("missing scrape_success=1 for healthy endpoint")
		}
		if !strings.Contains(res, `metrics_aggregator_scrape_success{endpoint="broken"} 0`) {
			t.Fatal("missing scrape_success=0 for broken endpoint")
		}
	})
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

// TestAddCustomLabel_EdgeCases covers real-world metric formats (T3).
func TestAddCustomLabel_EdgeCases(t *testing.T) {
	tests := []struct {
		name   string
		metric string
		epName string
		want   string
	}{
		{
			name:   "metric with timestamp",
			metric: `metric{l="v"} 1 1625000000`,
			epName: "svc",
			want:   `metric{origin_container="svc",l="v"} 1 1625000000`,
		},
		{
			name:   "histogram sum no labels",
			metric: `request_duration_sum 53423`,
			epName: "svc",
			want:   `request_duration_sum{origin_container="svc"} 53423`,
		},
		{
			name:   "malformed no space",
			metric: `metric_name`,
			epName: "svc",
			want:   `metric_name`,
		},
		{
			name:   "pre-existing origin_container skipped (C3)",
			metric: `metric{origin_container="old"} 1`,
			epName: "svc",
			want:   `metric{origin_container="old"} 1`,
		},
		{
			name:   "origin_container in label value should not prevent injection",
			metric: `metric{note="origin_container=x"} 1`,
			epName: "svc",
			want:   `metric{origin_container="svc",note="origin_container=x"} 1`,
		},
		{
			name:   "histogram bucket",
			metric: `http_duration_bucket{le="0.5"} 100`,
			epName: "web",
			want:   `http_duration_bucket{origin_container="web",le="0.5"} 100`,
		},
		{
			name:   "metric value with scientific notation",
			metric: `process_cpu_seconds_total 1.23e+04`,
			epName: "app",
			want:   `process_cpu_seconds_total{origin_container="app"} 1.23e+04`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := addCustomLabel(tc.metric, tc.epName)
			if got != tc.want {
				t.Fatalf("want %q, got %q", tc.want, got)
			}
		})
	}
}

// TestAggregator_DuplicateMetricFamilies verifies metadata stripping (C1, T4).
func TestAggregator_DuplicateMetricFamilies(t *testing.T) {
	metricsBody := "# TYPE go_goroutines gauge\n# HELP go_goroutines Number of goroutines.\ngo_goroutines 42\n"

	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(metricsBody))
	}))
	defer srv1.Close()
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(metricsBody))
	}))
	defer srv2.Close()

	agg := newTestAggregator([]Endpoint{
		{Name: "svc1", URL: srv1.URL},
		{Name: "svc2", URL: srv2.URL},
	})
	res, err := agg.AggregateMetrics(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, line := range strings.Split(res, "\n") {
		if strings.HasPrefix(line, "# TYPE go_goroutines") || strings.HasPrefix(line, "# HELP go_goroutines") {
			t.Fatalf("scraped metadata should be stripped, found: %s", line)
		}
	}

	count := strings.Count(res, "go_goroutines{origin_container=")
	if count != 2 {
		t.Fatalf("expected 2 go_goroutines metric lines, got %d in:\n%s", count, res)
	}
}

// TestAggregator_ValidOutput verifies all output lines match Prometheus format (T4).
func TestAggregator_ValidOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("# TYPE up gauge\nup 1\nhttp_requests_total{method=\"GET\"} 100\n"))
	}))
	defer srv.Close()

	agg := newTestAggregator([]Endpoint{{Name: "svc", URL: srv.URL}})
	res, err := agg.AggregateMetrics(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	metricLine := regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*(\{[^}]*\})?\s+\S+(\s+\S+)?$`)
	commentLine := regexp.MustCompile(`^#\s`)

	for _, line := range strings.Split(res, "\n") {
		if line == "" {
			continue
		}
		if commentLine.MatchString(line) {
			continue
		}
		if !metricLine.MatchString(line) {
			t.Errorf("invalid Prometheus line: %q", line)
		}
	}
}

// TestAggregator_SelfInstrumentation verifies scrape_success and scrape_duration metrics (M1).
func TestAggregator_SelfInstrumentation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("up 1\n"))
	}))
	defer srv.Close()

	agg := newTestAggregator([]Endpoint{{Name: "mysvc", URL: srv.URL}})
	res, err := agg.AggregateMetrics(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	checks := []string{
		"# TYPE metrics_aggregator_scrape_success gauge",
		`metrics_aggregator_scrape_success{endpoint="mysvc"} 1`,
		"# TYPE metrics_aggregator_scrape_duration_seconds histogram",
		`metrics_aggregator_scrape_duration_seconds_count{endpoint="mysvc"}`,
		`metrics_aggregator_scrape_duration_seconds_sum{endpoint="mysvc"}`,
		`metrics_aggregator_scrape_duration_seconds_bucket{endpoint="mysvc",le="+Inf"}`,
		"# TYPE metrics_aggregator_scrape_errors_total counter",
		`metrics_aggregator_scrape_errors_total{endpoint="mysvc"} 0`,
		"# TYPE metrics_aggregator_scrape_invalid_samples gauge",
		`metrics_aggregator_scrape_invalid_samples{endpoint="mysvc"} 0`,
		"# TYPE metrics_aggregator_requests_total counter",
		"metrics_aggregator_requests_total 1",
		"# TYPE metrics_aggregator_errors_total counter",
		"metrics_aggregator_errors_total 0",
	}
	for _, c := range checks {
		if !strings.Contains(res, c) {
			t.Errorf("missing %q in output:\n%s", c, res)
		}
	}
}

// TestAggregator_SelfInstrumentation_Extended verifies request/error counters.
func TestAggregator_SelfInstrumentation_Extended(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("up 1\n"))
	}))
	defer srv.Close()

	agg := newTestAggregator([]Endpoint{{Name: "svc", URL: srv.URL}})

	// First call
	res1, err := agg.AggregateMetrics(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res1, "metrics_aggregator_requests_total 1") {
		t.Fatalf("expected requests_total 1, got:\n%s", res1)
	}

	// Second call
	res2, err := agg.AggregateMetrics(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res2, "metrics_aggregator_requests_total 2") {
		t.Fatalf("expected requests_total 2, got:\n%s", res2)
	}
	if !strings.Contains(res2, "metrics_aggregator_errors_total 0") {
		t.Fatalf("expected errors_total 0, got:\n%s", res2)
	}
}

// TestAggregator_ErrorCounter verifies errors_total increments on failure.
func TestAggregator_ErrorCounter(t *testing.T) {
	agg := newTestAggregator([]Endpoint{{Name: "dead", URL: "http://127.0.0.1:1"}})

	_, err := agg.AggregateMetrics(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}

	// Call again with a working endpoint to see the counter
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("up 1\n"))
	}))
	defer srv.Close()
	agg.endpoints = []Endpoint{{Name: "ok", URL: srv.URL}}
	agg.scrapeErrors = make([]atomic.Int64, 1)
	agg.scrapeDurationHist = map[string]*Histogram{"ok": NewHistogram(DefaultBuckets())}

	res, err := agg.AggregateMetrics(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// errors_total should be 1 (from the failed call)
	if !strings.Contains(res, "metrics_aggregator_errors_total 1") {
		t.Fatalf("expected errors_total 1, got:\n%s", res)
	}
	if !strings.Contains(res, "metrics_aggregator_requests_total 2") {
		t.Fatalf("expected requests_total 2, got:\n%s", res)
	}
}

// TestAggregator_ContextLogger verifies that request-scoped logger fields appear in log output.
func TestAggregator_ContextLogger(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf).With().Str("request_id", "test-req-42").Logger()
	ctx := logger.WithContext(context.Background())

	// Use a dead endpoint to trigger an error log line
	agg := newTestAggregator([]Endpoint{{Name: "dead", URL: "http://127.0.0.1:1"}})
	agg.AggregateMetrics(ctx)

	output := buf.String()
	if !strings.Contains(output, "test-req-42") {
		t.Fatalf("expected request_id in log output, got: %s", output)
	}
	if !strings.Contains(output, "HTTP GET failed") {
		t.Fatalf("expected 'HTTP GET failed' in log output, got: %s", output)
	}
}

func TestNewAggregator_SecurityValidation_StrictByDefault(t *testing.T) {
	tests := []struct {
		name   string
		config string
	}{
		{
			name:   "reject URL userinfo",
			config: `{"svc":"http://user:pass@localhost:9090/metrics"}`,
		},
		{
			name:   "reject non metrics path",
			config: `{"svc":"http://localhost:9090/secret"}`,
		},
		{
			name:   "reject unsupported scheme",
			config: `{"svc":"file:///etc/passwd"}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("METRICS_SECURITY_MODE", "")
			_, err := NewAggregator(tc.config)
			if err == nil {
				t.Fatalf("expected security validation error for %s", tc.config)
			}
		})
	}
}

func TestNewAggregator_SecurityValidation_LegacyMode(t *testing.T) {
	t.Setenv("METRICS_SECURITY_MODE", "legacy")
	_, err := NewAggregator(`{"svc":"http://user:pass@localhost:9090/secret"}`)
	if err != nil {
		t.Fatalf("legacy mode should keep previous parsing behavior, got: %v", err)
	}
}

func TestNewAggregator_DoesNotLogSensitiveEndpointValues(t *testing.T) {
	secret := "user:Pa55w0rd"
	cfg := `{"svc":"http://` + secret + `@127.0.0.1:9090/metrics",`

	previousLogger := log.Logger
	defer func() { log.Logger = previousLogger }()

	var buf bytes.Buffer
	log.Logger = zerolog.New(&buf).With().Timestamp().Logger()

	_, _ = NewAggregator(cfg)

	output := buf.String()
	if strings.Contains(output, secret) {
		t.Fatalf("log output leaked endpoint credentials: %s", output)
	}
	if strings.Contains(output, `"env"`) {
		t.Fatalf("log output should not include raw env/config payload: %s", output)
	}
}

func TestAggregator_BlocksRedirectToSensitivePath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/metrics":
			http.Redirect(w, r, "/secret", http.StatusFound)
		case "/secret":
			_, _ = w.Write([]byte("aws_secret_access_key SUPERSECRET123\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	agg := newTestAggregator([]Endpoint{{Name: "redir", URL: srv.URL + "/metrics"}})
	metrics, err := agg.AggregateMetrics(context.Background())
	if err == nil {
		t.Fatalf("expected redirected scrape to be blocked, got output:\n%s", metrics)
	}
	if strings.Contains(metrics, "SUPERSECRET123") {
		t.Fatalf("redirected secret must not appear in output: %s", metrics)
	}
}

func TestAggregator_RejectsInvalidSampleValues(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("http_requests_total oops\n"))
	}))
	defer srv.Close()

	agg := newTestAggregator([]Endpoint{{Name: "bad", URL: srv.URL}})
	_, err := agg.AggregateMetrics(context.Background())
	if err == nil {
		t.Fatal("expected invalid sample payload to be rejected")
	}
}

func TestAggregator_InvalidSamplesMetricUsesNonTotalGaugeName(t *testing.T) {
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("up 1\n"))
	}))
	defer good.Close()

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("http_requests_total oops\n"))
	}))
	defer bad.Close()

	agg := newTestAggregator([]Endpoint{
		{Name: "good", URL: good.URL},
		{Name: "bad", URL: bad.URL},
	})

	res, err := agg.AggregateMetrics(context.Background())
	if err != nil {
		t.Fatalf("expected partial success, got error: %v", err)
	}

	if !strings.Contains(res, "# TYPE metrics_aggregator_scrape_invalid_samples gauge") {
		t.Fatalf("expected invalid samples gauge type line, got:\n%s", res)
	}
	if strings.Contains(res, "metrics_aggregator_scrape_invalid_samples_total") {
		t.Fatalf("invalid samples gauge metric must not use _total suffix, got:\n%s", res)
	}
	if !strings.Contains(res, `metrics_aggregator_scrape_invalid_samples{endpoint="bad"} 1`) {
		t.Fatalf("expected invalid sample count for bad endpoint, got:\n%s", res)
	}
}

func TestAggregator_OversizedResponseIsRejectedWithoutPartialIngestion(t *testing.T) {
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("up 1\n"))
	}))
	defer good.Close()

	line := "oversized_metric 1\n"
	repeat := (maxBodySize / len(line)) + 128
	oversizedPayload := strings.Repeat(line, repeat)
	oversized := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(oversizedPayload))
	}))
	defer oversized.Close()

	agg := newTestAggregator([]Endpoint{
		{Name: "good", URL: good.URL},
		{Name: "big", URL: oversized.URL},
	})

	res, err := agg.AggregateMetrics(context.Background())
	if err != nil {
		t.Fatalf("expected partial success when one endpoint is oversized, got: %v", err)
	}

	if !strings.Contains(res, `metrics_aggregator_scrape_success{endpoint="big"} 0`) {
		t.Fatalf("expected oversized endpoint to be marked unsuccessful, got:\n%s", res)
	}
	if strings.Contains(res, `oversized_metric{origin_container="big"}`) {
		t.Fatalf("oversized endpoint data must not be partially ingested, got:\n%s", res)
	}
	if !strings.Contains(res, `up{origin_container="good"} 1`) {
		t.Fatalf("expected healthy endpoint metrics to remain, got:\n%s", res)
	}
}

func TestAggregator_PerEndpointScrapeErrors(t *testing.T) {
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("up 1\n"))
	}))
	defer good.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()

	agg := newTestAggregator([]Endpoint{
		{Name: "healthy", URL: good.URL},
		{Name: "broken", URL: bad.URL},
	})
	res, err := agg.AggregateMetrics(context.Background())
	if err != nil {
		t.Fatalf("expected partial success, got error: %v", err)
	}

	if !strings.Contains(res, "# TYPE metrics_aggregator_scrape_errors_total counter") {
		t.Fatalf("missing scrape_errors_total TYPE line in:\n%s", res)
	}
	if !strings.Contains(res, `metrics_aggregator_scrape_errors_total{endpoint="broken"} 1`) {
		t.Fatalf("expected scrape_errors_total for broken=1 in:\n%s", res)
	}
	if !strings.Contains(res, `metrics_aggregator_scrape_errors_total{endpoint="healthy"} 0`) {
		t.Fatalf("expected scrape_errors_total for healthy=0 in:\n%s", res)
	}
}

func TestAggregator_PerEndpointScrapeErrors_Accumulate(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("up 1\n"))
	}))
	defer good.Close()

	agg := newTestAggregator([]Endpoint{
		{Name: "ok", URL: good.URL},
		{Name: "fail", URL: bad.URL},
	})

	agg.AggregateMetrics(context.Background())
	res, _ := agg.AggregateMetrics(context.Background())

	if !strings.Contains(res, `metrics_aggregator_scrape_errors_total{endpoint="fail"} 2`) {
		t.Fatalf("expected scrape_errors_total for fail=2 after 2 calls, got:\n%s", res)
	}
}

func TestAggregator_ScrapeDurationHistogram(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("up 1\n"))
	}))
	defer srv.Close()

	agg := newTestAggregator([]Endpoint{{Name: "svc", URL: srv.URL}})
	res, err := agg.AggregateMetrics(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(res, "# TYPE metrics_aggregator_scrape_duration_seconds histogram") {
		t.Fatalf("expected histogram TYPE for scrape_duration, got:\n%s", res)
	}
	if !strings.Contains(res, `metrics_aggregator_scrape_duration_seconds_bucket{endpoint="svc",le="+Inf"}`) {
		t.Fatalf("expected +Inf bucket, got:\n%s", res)
	}
	if !strings.Contains(res, `metrics_aggregator_scrape_duration_seconds_sum{endpoint="svc"}`) {
		t.Fatalf("expected _sum, got:\n%s", res)
	}
	if !strings.Contains(res, `metrics_aggregator_scrape_duration_seconds_count{endpoint="svc"} 1`) {
		t.Fatalf("expected _count=1, got:\n%s", res)
	}
}

func TestAggregator_ScrapeDurationHistogram_Accumulates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("up 1\n"))
	}))
	defer srv.Close()

	agg := newTestAggregator([]Endpoint{{Name: "svc", URL: srv.URL}})
	for range 3 {
		agg.AggregateMetrics(context.Background())
	}
	res, _ := agg.AggregateMetrics(context.Background())

	// 4 calls total (3 + the one that produces res), but histogram accumulates across all calls
	if !strings.Contains(res, `metrics_aggregator_scrape_duration_seconds_count{endpoint="svc"} 4`) {
		t.Fatalf("expected _count=4 after 4 calls, got:\n%s", res)
	}
}

func TestAggregator_ForwardsRequestIDHeader(t *testing.T) {
	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Request-Id")
		w.Write([]byte("up 1\n"))
	}))
	defer srv.Close()

	agg := newTestAggregator([]Endpoint{{Name: "svc", URL: srv.URL}})
	ctx := context.WithValue(context.Background(), ContextKeyRequestID, "test-req-123")
	agg.AggregateMetrics(ctx)

	if gotHeader != "test-req-123" {
		t.Fatalf("expected X-Request-Id 'test-req-123', got %q", gotHeader)
	}
}

func TestAggregator_ForwardsTraceparentHeader(t *testing.T) {
	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("traceparent")
		w.Write([]byte("up 1\n"))
	}))
	defer srv.Close()

	tp := "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"
	agg := newTestAggregator([]Endpoint{{Name: "svc", URL: srv.URL}})
	ctx := context.WithValue(context.Background(), ContextKeyTraceparent, tp)
	agg.AggregateMetrics(ctx)

	if gotHeader != tp {
		t.Fatalf("expected traceparent %q, got %q", tp, gotHeader)
	}
}

func TestAggregator_NoHeadersWhenContextEmpty(t *testing.T) {
	var gotRequestID, gotTraceparent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRequestID = r.Header.Get("X-Request-Id")
		gotTraceparent = r.Header.Get("traceparent")
		w.Write([]byte("up 1\n"))
	}))
	defer srv.Close()

	agg := newTestAggregator([]Endpoint{{Name: "svc", URL: srv.URL}})
	agg.AggregateMetrics(context.Background())

	if gotRequestID != "" {
		t.Fatalf("expected no X-Request-Id header, got %q", gotRequestID)
	}
	if gotTraceparent != "" {
		t.Fatalf("expected no traceparent header, got %q", gotTraceparent)
	}
}

func setupTestTracing(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	original := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		tp.Shutdown(context.Background())
		otel.SetTracerProvider(original)
	})
	return exporter
}

func TestAggregator_CreatesPerEndpointSpans(t *testing.T) {
	exporter := setupTestTracing(t)

	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("up 1\n"))
	}))
	defer srv1.Close()
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("up 1\n"))
	}))
	defer srv2.Close()

	agg := newTestAggregator([]Endpoint{
		{Name: "svc1", URL: srv1.URL},
		{Name: "svc2", URL: srv2.URL},
	})
	agg.AggregateMetrics(context.Background())

	spans := exporter.GetSpans()
	names := map[string]bool{}
	for _, s := range spans {
		names[s.Name] = true
	}
	if !names["scrape svc1"] || !names["scrape svc2"] {
		t.Fatalf("expected 'scrape svc1' and 'scrape svc2' spans, got: %v", names)
	}
}

func TestAggregator_ScrapeSpanRecordsError(t *testing.T) {
	exporter := setupTestTracing(t)

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("up 1\n"))
	}))
	defer good.Close()

	agg := newTestAggregator([]Endpoint{
		{Name: "good", URL: good.URL},
		{Name: "bad", URL: bad.URL},
	})
	agg.AggregateMetrics(context.Background())

	spans := exporter.GetSpans()
	for _, s := range spans {
		if s.Name == "scrape bad" {
			if len(s.Events) == 0 {
				t.Fatal("expected error event on bad endpoint span")
			}
			return
		}
	}
	t.Fatal("span 'scrape bad' not found")
}

func TestAggregator_NoSpansWhenNoTracer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("up 1\n"))
	}))
	defer srv.Close()

	// Use default no-op global tracer (don't set up test tracing)
	agg := newTestAggregator([]Endpoint{{Name: "svc", URL: srv.URL}})
	_, err := agg.AggregateMetrics(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestIsValidPrometheusSampleLine_EscapedBrace(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		valid bool
	}{
		{"escaped brace in label value", `http_requests_total{status="5xx\}escaped"} 42`, true},
		{"embedded quote escape in value", `metric{a="b\"}c"} 1`, true},
		{"normal label", `metric{a="b"} 1`, true},
		{"no labels", `metric 1`, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isValidPrometheusSampleLine(tc.line)
			if got != tc.valid {
				t.Errorf("isValidPrometheusSampleLine(%q) = %v, want %v", tc.line, got, tc.valid)
			}
		})
	}
}
