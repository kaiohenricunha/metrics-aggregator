package aggregator

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Histogram is a thread-safe Prometheus histogram that emits exposition format.
type Histogram struct {
	bounds []float64      // sorted bucket boundaries (always ends with +Inf)
	counts []atomic.Int64 // cumulative bucket counters, aligned with bounds
	count  atomic.Int64
	mu     sync.Mutex
	sum    float64
}

// DefaultBuckets returns the default Prometheus histogram bucket boundaries.
func DefaultBuckets() []float64 {
	return []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}
}

// NewHistogram creates a Histogram with the given bucket boundaries.
// Boundaries are sorted and deduplicated; +Inf is always appended.
func NewHistogram(buckets []float64) *Histogram {
	// Copy, sort, dedupe
	seen := make(map[float64]bool, len(buckets))
	var bounds []float64
	for _, b := range buckets {
		if math.IsInf(b, 1) || seen[b] {
			continue
		}
		seen[b] = true
		bounds = append(bounds, b)
	}
	sort.Float64s(bounds)
	bounds = append(bounds, math.Inf(1))

	return &Histogram{
		bounds: bounds,
		counts: make([]atomic.Int64, len(bounds)),
	}
}

// Observe records a value in the histogram.
func (h *Histogram) Observe(v float64) {
	for i, b := range h.bounds {
		if v <= b {
			h.counts[i].Add(1)
		}
	}
	h.count.Add(1)
	h.mu.Lock()
	h.sum += v
	h.mu.Unlock()
}

// Count returns the total number of observations.
func (h *Histogram) Count() int64 {
	return h.count.Load()
}

// Sum returns the sum of all observed values.
func (h *Histogram) Sum() float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sum
}

// RenderHeader emits the # HELP and # TYPE lines for a histogram metric family.
func RenderHeader(name, help string) string {
	return fmt.Sprintf("# HELP %s %s\n# TYPE %s histogram", name, help, name)
}

// RenderSamples emits _bucket, _sum, and _count lines for a single label set.
// labels is the inner label content (e.g. `endpoint="svc"`) without braces.
func RenderSamples(name string, labels string, h *Histogram) string {
	var b strings.Builder
	h.mu.Lock()
	sum := h.sum
	h.mu.Unlock()
	count := h.count.Load()

	for i, bound := range h.bounds {
		le := formatLE(bound)
		if labels == "" {
			fmt.Fprintf(&b, "%s_bucket{le=%q} %d\n", name, le, h.counts[i].Load())
		} else {
			fmt.Fprintf(&b, "%s_bucket{%s,le=%q} %d\n", name, labels, le, h.counts[i].Load())
		}
	}
	if labels == "" {
		fmt.Fprintf(&b, "%s_sum %s\n", name, formatFloat(sum))
		fmt.Fprintf(&b, "%s_count %d", name, count)
	} else {
		fmt.Fprintf(&b, "%s_sum{%s} %s\n", name, labels, formatFloat(sum))
		fmt.Fprintf(&b, "%s_count{%s} %d", name, labels, count)
	}
	return b.String()
}

func formatLE(v float64) string {
	if math.IsInf(v, 1) {
		return "+Inf"
	}
	return formatFloat(v)
}

func formatFloat(v float64) string {
	s := fmt.Sprintf("%g", v)
	// Ensure there's a decimal point for non-integer values that %g might format as int
	return s
}
