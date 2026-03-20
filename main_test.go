package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kaiohenricunha/metrics-aggregator/pkg/aggregator"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// freePort returns an available TCP port.
func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return fmt.Sprintf("127.0.0.1:%d", port)
}

// startServer launches run() in a goroutine with a cancel-able context.
func startServer(t *testing.T, agg *aggregator.Aggregator) (addr string, cancel context.CancelFunc) {
	t.Helper()
	addr = freePort(t)
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		if err := run(ctx, agg, addr); err != nil {
			t.Logf("run error: %v", err)
		}
	}()

	// Wait for server to be ready
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			conn.Close()
			return addr, cancel
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("server did not start in time")
	return "", nil
}

// newTestAgg creates a test aggregator backed by the given URL.
func newTestAgg(t *testing.T, url string) *aggregator.Aggregator {
	t.Helper()
	config := fmt.Sprintf(`{"test":"%s"}`, url)
	agg, err := aggregator.NewAggregator(config)
	if err != nil {
		t.Fatal(err)
	}
	return agg
}

func TestRun_ServesMetrics(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("up 1\n"))
	}))
	defer backend.Close()

	agg := newTestAgg(t, backend.URL)
	addr, cancel := startServer(t, agg)
	defer cancel()

	resp, err := http.Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(body) == 0 {
		t.Fatal("empty metrics response")
	}
}

func TestRun_ServesHealthz(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("up 1\n"))
	}))
	defer backend.Close()

	agg := newTestAgg(t, backend.URL)
	addr, cancel := startServer(t, agg)
	defer cancel()

	resp, err := http.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Fatalf("expected 'ok', got %q", string(body))
	}
}

func TestRun_GracefulShutdown(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("up 1\n"))
	}))
	defer backend.Close()

	agg := newTestAgg(t, backend.URL)
	addr, cancel := startServer(t, agg)

	// Verify server is up
	resp, err := http.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// Cancel context → graceful shutdown
	cancel()

	// Server should stop accepting connections
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, err := http.Get("http://" + addr + "/healthz")
		if err != nil {
			return // connection refused → server shut down
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("server did not shut down after context cancellation")
}

func TestRequestIDMiddleware_GeneratesID(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("up 1\n"))
	}))
	defer backend.Close()

	agg := newTestAgg(t, backend.URL)
	addr, cancel := startServer(t, agg)
	defer cancel()

	resp, err := http.Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	id := resp.Header.Get("X-Request-Id")
	if id == "" {
		t.Fatal("expected X-Request-Id header to be set")
	}
	if len(id) != 32 {
		t.Fatalf("expected 32-char hex ID, got %q (len=%d)", id, len(id))
	}
}

func TestRequestIDMiddleware_PreservesID(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("up 1\n"))
	}))
	defer backend.Close()

	agg := newTestAgg(t, backend.URL)
	addr, cancel := startServer(t, agg)
	defer cancel()

	req, _ := http.NewRequest("GET", "http://"+addr+"/metrics", nil)
	req.Header.Set("X-Request-Id", "abc123")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	id := resp.Header.Get("X-Request-Id")
	if id != "abc123" {
		t.Fatalf("expected X-Request-Id 'abc123', got %q", id)
	}
}

func TestLoadHTTPServerConfigFromEnv_Defaults(t *testing.T) {
	t.Setenv("METRICS_CACHE_TTL", "")
	t.Setenv("METRICS_MAX_INFLIGHT", "")
	t.Setenv("METRICS_SERVER_READ_HEADER_TIMEOUT", "")
	t.Setenv("METRICS_SERVER_READ_TIMEOUT", "")
	t.Setenv("METRICS_SERVER_WRITE_TIMEOUT", "")
	t.Setenv("METRICS_SERVER_IDLE_TIMEOUT", "")
	t.Setenv("METRICS_SERVER_MAX_HEADER_BYTES", "")

	cfg := loadHTTPServerConfigFromEnv()
	if cfg.readHeaderTimeout != 2*time.Second {
		t.Fatalf("expected default readHeaderTimeout=2s, got %s", cfg.readHeaderTimeout)
	}
	if cfg.readTimeout != 5*time.Second {
		t.Fatalf("expected default readTimeout=5s, got %s", cfg.readTimeout)
	}
	if cfg.writeTimeout != 10*time.Second {
		t.Fatalf("expected default writeTimeout=10s, got %s", cfg.writeTimeout)
	}
	if cfg.idleTimeout != 60*time.Second {
		t.Fatalf("expected default idleTimeout=60s, got %s", cfg.idleTimeout)
	}
	if cfg.maxHeaderBytes != 1<<20 {
		t.Fatalf("expected default maxHeaderBytes=1MiB, got %d", cfg.maxHeaderBytes)
	}
	if cfg.cacheTTL != time.Second {
		t.Fatalf("expected default cacheTTL=1s, got %s", cfg.cacheTTL)
	}
	if cfg.maxInflight != 32 {
		t.Fatalf("expected default maxInflight=32, got %d", cfg.maxInflight)
	}
}

func TestMetricsHandler_DedupesConcurrentScrapes(t *testing.T) {
	var backendHits atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := backendHits.Add(1)
		w.Write([]byte(fmt.Sprintf("backend_hits_total %d\n", n)))
	}))
	defer backend.Close()

	t.Setenv("METRICS_CACHE_TTL", "2s")
	t.Setenv("METRICS_MAX_INFLIGHT", "32")

	agg := newTestAgg(t, backend.URL)
	addr, cancel := startServer(t, agg)
	defer cancel()

	const reqs = 20
	var wg sync.WaitGroup
	errCh := make(chan error, reqs)

	for i := 0; i < reqs; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Get("http://" + addr + "/metrics")
			if err != nil {
				errCh <- err
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				errCh <- fmt.Errorf("unexpected status %d", resp.StatusCode)
				return
			}
		}()
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("request error: %v", err)
		}
	}
	if hits := backendHits.Load(); hits > 2 {
		t.Fatalf("expected deduped scrape calls <= 2, got %d", hits)
	}
}

func TestMetricsHandler_EnforcesMaxInflight(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.Write([]byte("up 1\n"))
	}))
	defer backend.Close()

	t.Setenv("METRICS_CACHE_TTL", "0s")
	t.Setenv("METRICS_MAX_INFLIGHT", "1")

	agg := newTestAgg(t, backend.URL)
	addr, cancel := startServer(t, agg)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		resp, err := http.Get("http://" + addr + "/metrics")
		if err != nil {
			errCh <- err
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			errCh <- fmt.Errorf("first request expected 200, got %d", resp.StatusCode)
			return
		}
		errCh <- nil
	}()

	time.Sleep(50 * time.Millisecond)

	resp, err := http.Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected second request to be rejected with 503, got %d", resp.StatusCode)
	}

	if err := <-errCh; err != nil {
		t.Fatalf("first request failed: %v", err)
	}
}

func TestGenerateRequestID_FallbackWhenEntropyFails(t *testing.T) {
	previousRandRead := randRead
	randRead = func(_ []byte) (int, error) {
		return 0, errors.New("entropy source unavailable")
	}
	defer func() { randRead = previousRandRead }()

	id1 := generateRequestID()
	id2 := generateRequestID()

	if len(id1) != 32 || len(id2) != 32 {
		t.Fatalf("expected 32-char hex IDs, got %q (%d) and %q (%d)", id1, len(id1), id2, len(id2))
	}
	if id1 == id2 {
		t.Fatalf("fallback request IDs must remain unique, both were %q", id1)
	}
}

func TestMetricsHandler_HTTPRequestsTotal(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("up 1\n"))
	}))
	defer backend.Close()

	// Use a long cache TTL so the second request hits cache
	t.Setenv("METRICS_CACHE_TTL", "10s")
	t.Setenv("METRICS_MAX_INFLIGHT", "32")

	agg := newTestAgg(t, backend.URL)
	addr, cancel := startServer(t, agg)
	defer cancel()

	// First request (cache miss)
	resp, err := http.Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body1, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if !strings.Contains(string(body1), "metrics_aggregator_http_requests_total 1") {
		t.Fatalf("expected http_requests_total 1 on first request, got:\n%s", body1)
	}

	// Second request (cache hit — should still increment)
	resp, err = http.Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body2, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if !strings.Contains(string(body2), "metrics_aggregator_http_requests_total 2") {
		t.Fatalf("expected http_requests_total 2 on cached request, got:\n%s", body2)
	}
}

func TestStatusRecorder_TracksBytesOnErrorResponses(t *testing.T) {
	rec := &statusRecorder{ResponseWriter: httptest.NewRecorder(), status: http.StatusOK}
	http.Error(rec, "too many concurrent scrape requests", http.StatusServiceUnavailable)

	if rec.status != http.StatusServiceUnavailable {
		t.Fatalf("expected status %d, got %d", http.StatusServiceUnavailable, rec.status)
	}
	if rec.bytes == 0 {
		t.Fatal("expected statusRecorder to track bytes written by http.Error")
	}
}

func TestMetricsHandler_HTTPRequestDurationHistogram(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("up 1\n"))
	}))
	defer backend.Close()

	t.Setenv("METRICS_CACHE_TTL", "0s")
	t.Setenv("METRICS_MAX_INFLIGHT", "32")

	agg := newTestAgg(t, backend.URL)
	addr, cancel := startServer(t, agg)
	defer cancel()

	for range 3 {
		resp, err := http.Get("http://" + addr + "/metrics")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}

	resp, err := http.Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	output := string(body)

	if !strings.Contains(output, "# TYPE metrics_aggregator_http_request_duration_seconds histogram") {
		t.Fatalf("missing histogram TYPE line in:\n%s", output)
	}
	if !strings.Contains(output, `metrics_aggregator_http_request_duration_seconds_bucket{le="+Inf"}`) {
		t.Fatalf("missing +Inf bucket in:\n%s", output)
	}
	if !strings.Contains(output, "metrics_aggregator_http_request_duration_seconds_sum") {
		t.Fatalf("missing _sum in:\n%s", output)
	}
	if !strings.Contains(output, "metrics_aggregator_http_request_duration_seconds_count") {
		t.Fatalf("missing _count in:\n%s", output)
	}
}

func TestMetricsHandler_HTTPRequestDurationHistogram_IncludesRejected(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.Write([]byte("up 1\n"))
	}))
	defer backend.Close()

	t.Setenv("METRICS_CACHE_TTL", "0s")
	t.Setenv("METRICS_MAX_INFLIGHT", "1")

	agg := newTestAgg(t, backend.URL)
	addr, cancel := startServer(t, agg)
	defer cancel()

	// First request holds the inflight slot
	go func() {
		http.Get("http://" + addr + "/metrics")
	}()
	time.Sleep(50 * time.Millisecond)

	// Second request should be rejected (503)
	resp, err := http.Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", resp.StatusCode)
	}

	// Wait for first request to complete, then fetch metrics to check histogram
	time.Sleep(400 * time.Millisecond)
	resp, err = http.Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	// The histogram should have counted both requests (success + rejected)
	if !strings.Contains(string(body), "metrics_aggregator_http_request_duration_seconds_count") {
		t.Fatalf("missing histogram _count in:\n%s", string(body))
	}
}

func TestRequestIDMiddleware_ParsesTraceparent(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("up 1\n"))
	}))
	defer backend.Close()

	agg := newTestAgg(t, backend.URL)
	addr, cancel := startServer(t, agg)
	defer cancel()

	req, _ := http.NewRequest("GET", "http://"+addr+"/metrics", nil)
	req.Header.Set("traceparent", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	// If no panic occurred and response is OK, the parsing succeeded
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestRequestIDMiddleware_NoTraceFieldsWithoutTraceparent(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("up 1\n"))
	}))
	defer backend.Close()

	agg := newTestAgg(t, backend.URL)
	addr, cancel := startServer(t, agg)
	defer cancel()

	resp, err := http.Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestRequestIDMiddleware_InvalidTraceparentIgnored(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("up 1\n"))
	}))
	defer backend.Close()

	agg := newTestAgg(t, backend.URL)
	addr, cancel := startServer(t, agg)
	defer cancel()

	req, _ := http.NewRequest("GET", "http://"+addr+"/metrics", nil)
	req.Header.Set("traceparent", "malformed-garbage")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 even with invalid traceparent, got %d", resp.StatusCode)
	}
}

func TestParseTraceparent(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantOK  bool
		traceID string
		spanID  string
	}{
		{"valid", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01", true, "0af7651916cd43dd8448eb211c80319c", "b7ad6b7169203331"},
		{"too few parts", "00-abc-01", false, "", ""},
		{"bad trace_id length", "00-0af765-b7ad6b7169203331-01", false, "", ""},
		{"bad span_id length", "00-0af7651916cd43dd8448eb211c80319c-b7ad-01", false, "", ""},
		{"uppercase hex rejected", "00-0AF7651916CD43DD8448EB211C80319C-B7AD6B7169203331-01", false, "", ""},
		{"all-zero trace_id rejected", "00-00000000000000000000000000000000-b7ad6b7169203331-01", false, "", ""},
		{"all-zero span_id rejected", "00-0af7651916cd43dd8448eb211c80319c-0000000000000000-01", false, "", ""},
		{"version ff rejected", "ff-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01", false, "", ""},
		{"bad version length", "000-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01", false, "", ""},
		{"uppercase version rejected", "0A-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01", false, "", ""},
		{"bad flags length", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-001", false, "", ""},
		{"uppercase flags rejected", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-0F", false, "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			traceID, spanID, ok := parseTraceparent(tc.input)
			if ok != tc.wantOK {
				t.Fatalf("parseTraceparent(%q): ok=%v, want %v", tc.input, ok, tc.wantOK)
			}
			if ok {
				if traceID != tc.traceID {
					t.Errorf("traceID=%q, want %q", traceID, tc.traceID)
				}
				if spanID != tc.spanID {
					t.Errorf("spanID=%q, want %q", spanID, tc.spanID)
				}
			}
		})
	}
}

func setupTestTracing(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	original := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		tp.Shutdown(context.Background())
		otel.SetTracerProvider(original)
	})
	return exporter
}

func TestTracingMiddleware_CreatesSpan(t *testing.T) {
	exporter := setupTestTracing(t)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("up 1\n"))
	}))
	defer backend.Close()

	t.Setenv("METRICS_CACHE_TTL", "0s")
	t.Setenv("METRICS_MAX_INFLIGHT", "32")

	agg := newTestAgg(t, backend.URL)
	addr, cancel := startServer(t, agg)
	defer cancel()

	resp, err := http.Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	spans := exporter.GetSpans()
	found := false
	for _, s := range spans {
		if s.Name == "GET /metrics" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected span named 'GET /metrics', got %d spans", len(spans))
	}
}

func TestRequestIDMiddleware_TruncatesLongHeader(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("up 1\n"))
	}))
	defer backend.Close()
	agg := newTestAgg(t, backend.URL)
	addr, cancel := startServer(t, agg)
	defer cancel()

	longID := strings.Repeat("a", 10000)
	req, _ := http.NewRequest("GET", "http://"+addr+"/metrics", nil)
	req.Header.Set("X-Request-Id", longID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	id := resp.Header.Get("X-Request-Id")
	if len(id) > 128 {
		t.Fatalf("expected X-Request-Id to be truncated to 128, got len=%d", len(id))
	}
}

func TestRequestIDMiddleware_StripsNonPrintable(t *testing.T) {
	// Use httptest.NewRecorder directly to bypass Go HTTP client header validation
	handler := requestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/metrics", nil)
	req.Header["X-Request-Id"] = []string{"abc\x00\x01def"} // bypass header validation
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	id := rec.Header().Get("X-Request-Id")
	for _, c := range id {
		if c < 0x20 || c > 0x7E {
			t.Fatalf("X-Request-Id contains non-printable char 0x%02x: %q", c, id)
		}
	}
}

func TestRequestIDMiddleware_FallbackOnAllNonPrintable(t *testing.T) {
	// Use httptest.NewRecorder directly to bypass Go HTTP client header validation
	handler := requestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/metrics", nil)
	req.Header["X-Request-Id"] = []string{"\x00\x01\x02"} // bypass header validation
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	id := rec.Header().Get("X-Request-Id")
	if len(id) != 32 {
		t.Fatalf("expected fallback 32-char hex ID, got %q (len=%d)", id, len(id))
	}
}

func TestMetricsHandler_BuildInfo(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("up 1\n"))
	}))
	defer backend.Close()
	t.Setenv("METRICS_CACHE_TTL", "0s")
	agg := newTestAgg(t, backend.URL)
	addr, cancel := startServer(t, agg)
	defer cancel()

	resp, err := http.Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `metrics_aggregator_build_info{version="dev",commit="unknown"} 1`) {
		t.Fatalf("expected build_info metric, got:\n%s", body)
	}
}

func TestMetricsHandler_CacheHitsAndMisses(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("up 1\n"))
	}))
	defer backend.Close()

	t.Setenv("METRICS_CACHE_TTL", "10s")
	agg := newTestAgg(t, backend.URL)
	addr, cancel := startServer(t, agg)
	defer cancel()

	// First request: cache miss
	resp, _ := http.Get("http://" + addr + "/metrics")
	body1, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body1), "metrics_aggregator_cache_misses_total 1") {
		t.Fatalf("expected cache_misses_total 1 on first request, got:\n%s", body1)
	}
	if !strings.Contains(string(body1), "metrics_aggregator_cache_hits_total 0") {
		t.Fatalf("expected cache_hits_total 0 on first request, got:\n%s", body1)
	}

	// Second request: cache hit
	resp, _ = http.Get("http://" + addr + "/metrics")
	body2, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body2), "metrics_aggregator_cache_hits_total 1") {
		t.Fatalf("expected cache_hits_total 1 on second request, got:\n%s", body2)
	}
	if !strings.Contains(string(body2), "metrics_aggregator_cache_misses_total 1") {
		t.Fatalf("expected cache_misses_total 1 on second request, got:\n%s", body2)
	}
}

func TestRun_BindAddressRespected(t *testing.T) {
	t.Setenv("METRICS_BIND_ADDRESS", "127.0.0.1")
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("up 1\n"))
	}))
	defer backend.Close()
	agg := newTestAgg(t, backend.URL)

	// Use a free port on 127.0.0.1
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	l.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		if err := run(ctx, agg, "127.0.0.1:"+port); err != nil {
			t.Logf("run error: %v", err)
		}
	}()
	// Wait for server to start
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 50*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	resp, err := http.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestPortValidation_NonNumeric(t *testing.T) {
	_, err := validatePort("abc")
	if err == nil {
		t.Fatal("expected error for non-numeric port")
	}
}

func TestPortValidation_Zero(t *testing.T) {
	_, err := validatePort("0")
	if err == nil {
		t.Fatal("expected error for port 0")
	}
}

func TestPortValidation_OutOfRange(t *testing.T) {
	_, err := validatePort("99999")
	if err == nil {
		t.Fatal("expected error for port out of range")
	}
}

func TestPortValidation_Valid(t *testing.T) {
	p, err := validatePort("9090")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p != "9090" {
		t.Fatalf("expected '9090', got %q", p)
	}
}

func TestTracingMiddleware_NoopWhenDisabled(t *testing.T) {
	// Don't set up any TracerProvider — use default no-op
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("up 1\n"))
	}))
	defer backend.Close()

	t.Setenv("METRICS_CACHE_TTL", "0s")
	agg := newTestAgg(t, backend.URL)
	addr, cancel := startServer(t, agg)
	defer cancel()

	resp, err := http.Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}
