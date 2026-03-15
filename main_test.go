package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
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
