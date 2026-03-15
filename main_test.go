package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kaiohenricunha/metrics-aggregator/pkg/aggregator"
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
