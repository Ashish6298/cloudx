// Package metrics provides a lightweight, in-process metrics model for CloudX.
//
// It defines counters, gauges, and histograms for the four instrumentation domains
// required by Phase 58 — Metrics Model (Milestone 16: Observability):
//
//   - Control Plane: reconciliation cycles, scheduling latency, RPC failures, state operations
//   - Worker:        CPU usage, memory usage, task count, process restarts
//   - Service:       replicas desired/running, health failures, restart count
//   - Job:           execution duration, success/failure counts
//
// No external observability server is required; CloudX remains usable without
// external infrastructure (see Phase 59 for OpenTelemetry wiring).
package metrics

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ──────────────────────────────────────────────────────────────────────────────
// Primitive types
// ──────────────────────────────────────────────────────────────────────────────

// Counter is a monotonically-increasing int64 value (e.g. total reconciliation cycles).
type Counter struct {
	name   string
	labels map[string]string
	v      int64
}

// Inc increments the counter by 1.
func (c *Counter) Inc() { atomic.AddInt64(&c.v, 1) }

// Add increments the counter by delta (must be ≥ 0).
func (c *Counter) Add(delta int64) {
	if delta > 0 {
		atomic.AddInt64(&c.v, delta)
	}
}

// Value returns the current counter value.
func (c *Counter) Value() int64 { return atomic.LoadInt64(&c.v) }

// Name returns the metric name.
func (c *Counter) Name() string { return c.name }

// Labels returns the label map.
func (c *Counter) Labels() map[string]string { return c.labels }

// ──────────────────────────────────────────────────────────────────────────────

// Gauge is a float64 value that can go up or down (e.g. CPU %, active tasks).
type Gauge struct {
	mu     sync.RWMutex
	name   string
	labels map[string]string
	v      float64
}

// Set sets the gauge to an absolute value.
func (g *Gauge) Set(v float64) {
	g.mu.Lock()
	g.v = v
	g.mu.Unlock()
}

// Add adds delta to the current gauge value.
func (g *Gauge) Add(delta float64) {
	g.mu.Lock()
	g.v += delta
	g.mu.Unlock()
}

// Value returns the current gauge value.
func (g *Gauge) Value() float64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.v
}

// Name returns the metric name.
func (g *Gauge) Name() string { return g.name }

// Labels returns the label map.
func (g *Gauge) Labels() map[string]string { return g.labels }

// ──────────────────────────────────────────────────────────────────────────────

// Histogram records durations and computes quantiles (e.g. scheduling latency).
type Histogram struct {
	mu       sync.Mutex
	name     string
	labels   map[string]string
	count    int64
	sum      float64   // sum of all observations in seconds
	samples  []float64 // raw observations kept for quantile computation
	maxSamples int     // cap to avoid unbounded growth
}

// Observe records a duration observation.
func (h *Histogram) Observe(d time.Duration) {
	secs := d.Seconds()
	h.mu.Lock()
	defer h.mu.Unlock()
	h.count++
	h.sum += secs
	if len(h.samples) < h.maxSamples {
		h.samples = append(h.samples, secs)
	}
	// When at capacity, replace a random-ish slot (reservoir sampling by position).
	// Simple strategy: overwrite oldest entry cyclically.
	if len(h.samples) == h.maxSamples {
		idx := int(h.count) % h.maxSamples
		h.samples[idx] = secs
	}
}

// Count returns the total number of observations.
func (h *Histogram) Count() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.count
}

// Sum returns the sum of all observed durations in seconds.
func (h *Histogram) Sum() float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sum
}

// Mean returns the arithmetic mean of observed durations in seconds, or 0 if no observations.
func (h *Histogram) Mean() float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.count == 0 {
		return 0
	}
	return h.sum / float64(h.count)
}

// Quantile returns the p-th quantile (0.0–1.0) of observed durations in seconds.
// Returns math.NaN() if there are no samples.
func (h *Histogram) Quantile(p float64) float64 {
	h.mu.Lock()
	cp := make([]float64, len(h.samples))
	copy(cp, h.samples)
	h.mu.Unlock()

	if len(cp) == 0 {
		return math.NaN()
	}
	sort.Float64s(cp)
	idx := p * float64(len(cp)-1)
	lo := int(math.Floor(idx))
	hi := int(math.Ceil(idx))
	if lo == hi {
		return cp[lo]
	}
	frac := idx - float64(lo)
	return cp[lo]*(1-frac) + cp[hi]*frac
}

// Name returns the metric name.
func (h *Histogram) Name() string { return h.name }

// Labels returns the label map.
func (h *Histogram) Labels() map[string]string { return h.labels }

// ──────────────────────────────────────────────────────────────────────────────
// Registry
// ──────────────────────────────────────────────────────────────────────────────

// Registry is the central, thread-safe store for all metrics.
// All metric instances are created through the registry to ensure uniqueness.
type Registry struct {
	mu         sync.RWMutex
	counters   map[string]*Counter
	gauges     map[string]*Gauge
	histograms map[string]*Histogram
}

// NewRegistry creates a new empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		counters:   make(map[string]*Counter),
		gauges:     make(map[string]*Gauge),
		histograms: make(map[string]*Histogram),
	}
}

// Default is the process-wide default metrics registry.
var Default = NewRegistry()

// labelKey converts a name + labels into a canonical map key.
func labelKey(name string, labels map[string]string) string {
	if len(labels) == 0 {
		return name
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteString(name)
	sb.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(labels[k])
	}
	sb.WriteByte('}')
	return sb.String()
}

// Counter returns an existing counter or creates a new one.
func (r *Registry) Counter(name string, labels map[string]string) *Counter {
	key := labelKey(name, labels)
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.counters[key]; ok {
		return c
	}
	c := &Counter{name: name, labels: labels}
	r.counters[key] = c
	return c
}

// Gauge returns an existing gauge or creates a new one.
func (r *Registry) Gauge(name string, labels map[string]string) *Gauge {
	key := labelKey(name, labels)
	r.mu.Lock()
	defer r.mu.Unlock()
	if g, ok := r.gauges[key]; ok {
		return g
	}
	g := &Gauge{name: name, labels: labels}
	r.gauges[key] = g
	return g
}

// Histogram returns an existing histogram or creates a new one.
func (r *Registry) Histogram(name string, labels map[string]string) *Histogram {
	key := labelKey(name, labels)
	r.mu.Lock()
	defer r.mu.Unlock()
	if h, ok := r.histograms[key]; ok {
		return h
	}
	h := &Histogram{name: name, labels: labels, maxSamples: 1024}
	r.histograms[key] = h
	return h
}

// ──────────────────────────────────────────────────────────────────────────────
// Snapshot / export
// ──────────────────────────────────────────────────────────────────────────────

// MetricValue is a single exported metric sample suitable for serialisation.
type MetricValue struct {
	Name   string            `json:"name"`
	Type   string            `json:"type"` // "counter", "gauge", "histogram"
	Labels map[string]string `json:"labels,omitempty"`
	Value  float64           `json:"value"`
	// Histogram extras (only set for histograms)
	Count  int64    `json:"count,omitempty"`
	Sum    float64  `json:"sum_seconds,omitempty"`
	P50    *float64 `json:"p50_seconds,omitempty"`
	P90    *float64 `json:"p90_seconds,omitempty"`
	P99    *float64 `json:"p99_seconds,omitempty"`
}

// Snapshot returns a point-in-time copy of all metrics values.
func (r *Registry) Snapshot() []MetricValue {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var out []MetricValue

	for _, c := range r.counters {
		out = append(out, MetricValue{
			Name:   c.name,
			Type:   "counter",
			Labels: c.labels,
			Value:  float64(c.Value()),
		})
	}
	for _, g := range r.gauges {
		out = append(out, MetricValue{
			Name:   g.name,
			Type:   "gauge",
			Labels: g.labels,
			Value:  g.Value(),
		})
	}
	for _, h := range r.histograms {
		mv := MetricValue{
			Name:   h.name,
			Type:   "histogram",
			Labels: h.labels,
			Count:  h.Count(),
			Sum:    h.Sum(),
		}
		if p50 := h.Quantile(0.50); !math.IsNaN(p50) {
			v := p50
			mv.P50 = &v
		}
		if p90 := h.Quantile(0.90); !math.IsNaN(p90) {
			v := p90
			mv.P90 = &v
		}
		if p99 := h.Quantile(0.99); !math.IsNaN(p99) {
			v := p99
			mv.P99 = &v
		}
		out = append(out, mv)
	}

	// Deterministic output order
	sort.Slice(out, func(i, j int) bool {
		ki := labelKey(out[i].Name, out[i].Labels)
		kj := labelKey(out[j].Name, out[j].Labels)
		return ki < kj
	})
	return out
}

// Format renders all metrics to a human-readable text format (similar to Prometheus exposition).
func (r *Registry) Format() string {
	snap := r.Snapshot()
	var sb strings.Builder
	for _, mv := range snap {
		switch mv.Type {
		case "counter", "gauge":
			sb.WriteString(fmt.Sprintf("%-60s %g\n", labelKey(mv.Name, mv.Labels), mv.Value))
		case "histogram":
			key := labelKey(mv.Name, mv.Labels)
			sb.WriteString(fmt.Sprintf("%-60s count=%d sum=%.6fs p50=%s p90=%s p99=%s\n",
				key, mv.Count, mv.Sum,
				derefQuantile(mv.P50), derefQuantile(mv.P90), derefQuantile(mv.P99)))
		}
	}
	return sb.String()
}

// derefQuantile converts a *float64 quantile to its string form, or "NaN" if nil.
func derefQuantile(p *float64) string {
	if p == nil {
		return "NaN"
	}
	return fmt.Sprintf("%.6fs", *p)
}
