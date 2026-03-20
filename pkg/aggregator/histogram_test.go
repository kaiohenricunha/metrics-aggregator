package aggregator

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// bucketVal acquires h.mu and returns h.counts[i] (safe for tests in package aggregator).
func bucketVal(h *Histogram, i int) int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.counts[i]
}

func TestHistogram_Observe_SingleValue(t *testing.T) {
	h := NewHistogram(DefaultBuckets())
	h.Observe(0.5)

	if h.Count() != 1 {
		t.Fatalf("expected count 1, got %d", h.Count())
	}
	if h.Sum() != 0.5 {
		t.Fatalf("expected sum 0.5, got %f", h.Sum())
	}
	// 0.5 <= 0.5, 1, 2.5, 5, 10, +Inf — so buckets at those boundaries should be 1
	// 0.5 > 0.005, 0.01, 0.025, 0.05, 0.1, 0.25 — those should be 0
	for i, bound := range h.bounds {
		val := bucketVal(h, i)
		if bound >= 0.5 {
			if val != 1 {
				t.Errorf("bucket le=%.3f: expected 1, got %d", bound, val)
			}
		} else {
			if val != 0 {
				t.Errorf("bucket le=%.3f: expected 0, got %d", bound, val)
			}
		}
	}
}

func TestHistogram_Observe_MultipleValues(t *testing.T) {
	h := NewHistogram([]float64{1, 5, 10})
	h.Observe(0.5) // <= 1, 5, 10, +Inf
	h.Observe(3)   // <= 5, 10, +Inf
	h.Observe(7)   // <= 10, +Inf
	h.Observe(15)  // <= +Inf only

	if h.Count() != 4 {
		t.Fatalf("expected count 4, got %d", h.Count())
	}
	expectedSum := 0.5 + 3 + 7 + 15.0
	if h.Sum() != expectedSum {
		t.Fatalf("expected sum %f, got %f", expectedSum, h.Sum())
	}

	// Cumulative: le=1 → 1, le=5 → 2, le=10 → 3, le=+Inf → 4
	expected := []int64{1, 2, 3, 4}
	for i, exp := range expected {
		got := bucketVal(h, i)
		if got != exp {
			t.Errorf("bucket[%d] le=%v: expected %d, got %d", i, h.bounds[i], exp, got)
		}
	}
}

func TestHistogram_ConcurrentObserve(t *testing.T) {
	h := NewHistogram(DefaultBuckets())
	var wg sync.WaitGroup
	for g := 0; g < 100; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				h.Observe(0.1)
			}
		}()
	}
	wg.Wait()

	if h.Count() != 10000 {
		t.Fatalf("expected count 10000, got %d", h.Count())
	}
}

func TestHistogram_DefaultBuckets(t *testing.T) {
	expected := []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}
	got := DefaultBuckets()
	if len(got) != len(expected) {
		t.Fatalf("expected %d default buckets, got %d", len(expected), len(got))
	}
	for i, v := range expected {
		if got[i] != v {
			t.Errorf("bucket[%d]: expected %f, got %f", i, v, got[i])
		}
	}
}

func TestHistogram_CustomBuckets(t *testing.T) {
	h := NewHistogram([]float64{1, 5, 10})
	// Should have 4 bounds: 1, 5, 10, +Inf
	if len(h.bounds) != 4 {
		t.Fatalf("expected 4 bounds (3 custom + Inf), got %d", len(h.bounds))
	}
	if !math.IsInf(h.bounds[3], 1) {
		t.Fatalf("last bound should be +Inf, got %f", h.bounds[3])
	}
}

func TestHistogram_Render_Empty(t *testing.T) {
	h := NewHistogram([]float64{1, 5})
	header := RenderHeader("test_metric", "A test metric.")
	samples := RenderSamples("test_metric", "", h)
	output := header + "\n" + samples

	if !strings.Contains(output, "# HELP test_metric A test metric.") {
		t.Error("missing HELP line")
	}
	if !strings.Contains(output, "# TYPE test_metric histogram") {
		t.Error("missing TYPE line")
	}
	if !strings.Contains(output, `test_metric_bucket{le="1"} 0`) {
		t.Error("missing bucket le=1")
	}
	if !strings.Contains(output, `test_metric_bucket{le="5"} 0`) {
		t.Error("missing bucket le=5")
	}
	if !strings.Contains(output, `test_metric_bucket{le="+Inf"} 0`) {
		t.Error("missing +Inf bucket")
	}
	if !strings.Contains(output, "test_metric_sum 0") {
		t.Error("missing _sum 0")
	}
	if !strings.Contains(output, "test_metric_count 0") {
		t.Error("missing _count 0")
	}
}

func TestHistogram_Render_WithObservations(t *testing.T) {
	h := NewHistogram([]float64{1, 5, 10})
	h.Observe(0.5)
	h.Observe(3)
	h.Observe(7)

	samples := RenderSamples("dur", "", h)

	// Cumulative: le=1 → 1, le=5 → 2, le=10 → 3, le=+Inf → 3
	if !strings.Contains(samples, `dur_bucket{le="1"} 1`) {
		t.Errorf("expected le=1 → 1, got:\n%s", samples)
	}
	if !strings.Contains(samples, `dur_bucket{le="5"} 2`) {
		t.Errorf("expected le=5 → 2, got:\n%s", samples)
	}
	if !strings.Contains(samples, `dur_bucket{le="10"} 3`) {
		t.Errorf("expected le=10 → 3, got:\n%s", samples)
	}
	if !strings.Contains(samples, `dur_bucket{le="+Inf"} 3`) {
		t.Errorf("expected le=+Inf → 3, got:\n%s", samples)
	}
	if !strings.Contains(samples, "dur_sum 10.5") {
		t.Errorf("expected sum 10.5, got:\n%s", samples)
	}
	if !strings.Contains(samples, "dur_count 3") {
		t.Errorf("expected count 3, got:\n%s", samples)
	}
}

func TestHistogram_Render_PlusInfBucket(t *testing.T) {
	h := NewHistogram([]float64{1})
	h.Observe(100) // way beyond bucket
	h.Observe(0.5) // within bucket

	samples := RenderSamples("m", "", h)
	if !strings.Contains(samples, `m_bucket{le="+Inf"} 2`) {
		t.Errorf("expected +Inf bucket to equal total count 2, got:\n%s", samples)
	}
}

func TestHistogram_Render_ValidPrometheusFormat(t *testing.T) {
	h := NewHistogram(DefaultBuckets())
	h.Observe(0.042)
	h.Observe(1.5)

	header := RenderHeader("test_hist", "A histogram.")
	samples := RenderSamples("test_hist", "", h)
	output := header + "\n" + samples

	for _, line := range strings.Split(output, "\n") {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		if !sampleLine.MatchString(line) {
			t.Errorf("line does not match Prometheus sample format: %q", line)
		}
	}
}

func TestHistogram_Render_WithLabels(t *testing.T) {
	h := NewHistogram([]float64{1, 5})
	h.Observe(0.5)

	samples := RenderSamples("scrape_dur", `endpoint="svc"`, h)
	if !strings.Contains(samples, `scrape_dur_bucket{endpoint="svc",le="1"} 1`) {
		t.Errorf("expected labeled bucket, got:\n%s", samples)
	}
	if !strings.Contains(samples, `scrape_dur_sum{endpoint="svc"}`) {
		t.Errorf("expected labeled sum, got:\n%s", samples)
	}
	if !strings.Contains(samples, `scrape_dur_count{endpoint="svc"} 1`) {
		t.Errorf("expected labeled count, got:\n%s", samples)
	}
}

func TestHistogram_Render_AllLinesValidFormat(t *testing.T) {
	// Verify that labeled histogram output also passes sampleLine regex
	h := NewHistogram(DefaultBuckets())
	h.Observe(0.123)

	samples := RenderSamples("my_metric", `endpoint="a"`, h)
	// Updated pattern to handle quoted strings in label values (like le="+Inf")
	metricLine := regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*(\{(?:[^"{}\\]|"(?:[^"\\]|\\.)*")*\})?\s+\S+(\s+\S+)?$`)

	for _, line := range strings.Split(samples, "\n") {
		if line == "" {
			continue
		}
		if !metricLine.MatchString(line) {
			t.Errorf("labeled histogram line does not match Prometheus format: %q", line)
		}
	}
}

func TestHistogram_ConcurrentObserveAndRender(t *testing.T) {
	h := NewHistogram(DefaultBuckets())
	const goroutines = 100
	const perGoroutine = 1000

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				h.Observe(1.0)
			}
		}()
	}

	// Reader goroutine: continuously render while writers are active
	done := make(chan struct{})
	go func() {
		defer close(done)
		wg.Wait()
	}()

	renderCount := 0
	for {
		select {
		case <-done:
			goto doneRendering
		default:
			snap := RenderSamples("test_hist", "", h)
			renderCount++
			// Verify bucket counts in any snapshot are monotonically non-decreasing
			var prevCount int64 = -1
			for _, line := range strings.Split(snap, "\n") {
				if !strings.Contains(line, "_bucket{le=") {
					continue
				}
				parts := strings.Fields(line)
				if len(parts) < 2 {
					continue
				}
				v, err := strconv.ParseInt(parts[len(parts)-1], 10, 64)
				if err != nil {
					continue
				}
				if v < prevCount {
					t.Errorf("bucket counts not monotonically non-decreasing: prev=%d, cur=%d in line %q", prevCount, v, line)
				}
				prevCount = v
			}
		}
	}
doneRendering:

	if got := h.Count(); got != goroutines*perGoroutine {
		t.Fatalf("expected count %d, got %d", goroutines*perGoroutine, got)
	}
	if got := h.Sum(); got != float64(goroutines*perGoroutine) {
		t.Fatalf("expected sum %g, got %g", float64(goroutines*perGoroutine), got)
	}
	_ = renderCount
}

func BenchmarkHistogramObserve(b *testing.B) {
	h := NewHistogram(DefaultBuckets())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.Observe(0.5)
	}
}
