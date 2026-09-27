// Package otel provides bridges between CloudX metrics and OpenTelemetry data models.
package otel

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cloudx-org/cloudx/internal/metrics"
)

// ──────────────────────────────────────────────────────────────────────────────
// OpenTelemetry Metrics Data Model Types (OTLP Compatible)
// ──────────────────────────────────────────────────────────────────────────────

// MetricType classifies the OTEL metric instrument.
type MetricType string

const (
	MetricTypeSum       MetricType = "SUM"       // Counter / monotonic sum
	MetricTypeGauge     MetricType = "GAUGE"     // Non-monotonic gauge
	MetricTypeHistogram MetricType = "HISTOGRAM" // Explicit bucket histogram
)

// DataPoint represents an individual timestamped measurement.
type DataPoint struct {
	Timestamp  time.Time         `json:"timestamp"`
	Value      float64           `json:"value"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

// HistogramDataPoint represents a histogram distribution measurement.
type HistogramDataPoint struct {
	Timestamp  time.Time         `json:"timestamp"`
	Count      int64             `json:"count"`
	Sum        float64           `json:"sum"`
	P50        *float64          `json:"p50,omitempty"`
	P90        *float64          `json:"p90,omitempty"`
	P99        *float64          `json:"p99,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

// Metric represents an OpenTelemetry-compatible metric descriptor with data points.
type Metric struct {
	Name        string               `json:"name"`
	Description string               `json:"description,omitempty"`
	Unit        string               `json:"unit,omitempty"`
	Type        MetricType           `json:"type"`
	DataPoints  []DataPoint          `json:"data_points,omitempty"`
	HistPoints  []HistogramDataPoint `json:"histogram_data_points,omitempty"`
}

// ScopeMetrics groups metrics produced by an instrumentation scope.
type ScopeMetrics struct {
	ScopeName    string   `json:"scope_name"`
	ScopeVersion string   `json:"scope_version,omitempty"`
	Metrics      []Metric `json:"metrics"`
}

// ResourceMetrics groups scope metrics with resource metadata.
type ResourceMetrics struct {
	Resource     map[string]string `json:"resource"`
	ScopeMetrics []ScopeMetrics    `json:"scope_metrics"`
}

// ──────────────────────────────────────────────────────────────────────────────
// Metrics Bridge: Registry to OpenTelemetry
// ──────────────────────────────────────────────────────────────────────────────

// MetricsBridge translates CloudX in-process metrics from metrics.Registry
// into OpenTelemetry-compatible ResourceMetrics data structures.
type MetricsBridge struct {
	mu            sync.RWMutex
	registry      *metrics.Registry
	resourceAttrs map[string]string
	scopeName     string
	scopeVersion  string
}

// NewMetricsBridge creates a bridge between a CloudX metrics Registry and OTEL models.
func NewMetricsBridge(reg *metrics.Registry, resourceAttrs map[string]string, scopeName, scopeVersion string) *MetricsBridge {
	if scopeName == "" {
		scopeName = "cloudx"
	}
	res := make(map[string]string, len(resourceAttrs))
	for k, v := range resourceAttrs {
		res[k] = v
	}
	return &MetricsBridge{
		registry:      reg,
		resourceAttrs: res,
		scopeName:     scopeName,
		scopeVersion:  scopeVersion,
	}
}

// Collect reads the registry snapshot and converts it to OTEL ResourceMetrics.
func (b *MetricsBridge) Collect(ctx context.Context) (*ResourceMetrics, error) {
	b.mu.RLock()
	reg := b.registry
	resAttrs := b.resourceAttrs
	scopeName := b.scopeName
	scopeVersion := b.scopeVersion
	b.mu.RUnlock()

	if reg == nil {
		return &ResourceMetrics{
			Resource: resAttrs,
			ScopeMetrics: []ScopeMetrics{
				{ScopeName: scopeName, ScopeVersion: scopeVersion},
			},
		}, nil
	}

	snap := reg.Snapshot()
	now := time.Now().UTC()

	var otelMetrics []Metric

	for _, mv := range snap {
		switch mv.Type {
		case "counter":
			m := Metric{
				Name:        mv.Name,
				Description: describeMetric(mv.Name),
				Unit:        inferUnit(mv.Name),
				Type:        MetricTypeSum,
				DataPoints: []DataPoint{
					{
						Timestamp:  now,
						Value:      mv.Value,
						Attributes: mv.Labels,
					},
				},
			}
			otelMetrics = append(otelMetrics, m)

		case "gauge":
			m := Metric{
				Name:        mv.Name,
				Description: describeMetric(mv.Name),
				Unit:        inferUnit(mv.Name),
				Type:        MetricTypeGauge,
				DataPoints: []DataPoint{
					{
						Timestamp:  now,
						Value:      mv.Value,
						Attributes: mv.Labels,
					},
				},
			}
			otelMetrics = append(otelMetrics, m)

		case "histogram":
			m := Metric{
				Name:        mv.Name,
				Description: describeMetric(mv.Name),
				Unit:        inferUnit(mv.Name),
				Type:        MetricTypeHistogram,
				HistPoints: []HistogramDataPoint{
					{
						Timestamp:  now,
						Count:      mv.Count,
						Sum:        mv.Sum,
						P50:        mv.P50,
						P90:        mv.P90,
						P99:        mv.P99,
						Attributes: mv.Labels,
					},
				},
			}
			otelMetrics = append(otelMetrics, m)
		}
	}

	return &ResourceMetrics{
		Resource: resAttrs,
		ScopeMetrics: []ScopeMetrics{
			{
				ScopeName:    scopeName,
				ScopeVersion: scopeVersion,
				Metrics:      otelMetrics,
			},
		},
	}, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// OpenTelemetry Meter / MeterProvider Interfaces
// ──────────────────────────────────────────────────────────────────────────────

// MeterProvider creates named Meter instances.
type MeterProvider interface {
	// Meter returns a Meter for the given instrumentation scope name.
	Meter(name string) Meter
}

// Meter is the interface for recording application metrics.
type Meter interface {
	// Int64Counter creates a new monotonic integer counter.
	Int64Counter(name string, opts ...MetricOption) (Int64Counter, error)
	// Float64Gauge creates a new variable float64 gauge.
	Float64Gauge(name string, opts ...MetricOption) (Float64Gauge, error)
	// Float64Histogram creates a new float64 histogram.
	Float64Histogram(name string, opts ...MetricOption) (Float64Histogram, error)
}

// Int64Counter records monotonic counter increments.
type Int64Counter interface {
	Add(ctx context.Context, delta int64, attrs ...Attribute)
}

// Float64Gauge records floating-point gauge values.
type Float64Gauge interface {
	Record(ctx context.Context, val float64, attrs ...Attribute)
}

// Float64Histogram records duration/size observations.
type Float64Histogram interface {
	Record(ctx context.Context, val float64, attrs ...Attribute)
}

// MetricOption configures metric creation.
type MetricOption func(*metricConfig)

type metricConfig struct {
	description string
	unit        string
}

// WithDescription sets a human-readable metric description.
func WithDescription(d string) MetricOption {
	return func(c *metricConfig) { c.description = d }
}

// WithUnit sets the metric unit (e.g. "s", "By", "1").
func WithUnit(u string) MetricOption {
	return func(c *metricConfig) { c.unit = u }
}

// StandardMeterProvider implements MeterProvider backed by metrics.Registry.
type StandardMeterProvider struct {
	registry *metrics.Registry
}

// NewStandardMeterProvider creates a MeterProvider backed by the given Registry.
func NewStandardMeterProvider(reg *metrics.Registry) *StandardMeterProvider {
	if reg == nil {
		reg = metrics.NewRegistry()
	}
	return &StandardMeterProvider{registry: reg}
}

func (p *StandardMeterProvider) Meter(name string) Meter {
	return &standardMeter{registry: p.registry, scope: name}
}

type standardMeter struct {
	registry *metrics.Registry
	scope    string
}

func (m *standardMeter) Int64Counter(name string, opts ...MetricOption) (Int64Counter, error) {
	return &boundCounter{registry: m.registry, name: name}, nil
}

func (m *standardMeter) Float64Gauge(name string, opts ...MetricOption) (Float64Gauge, error) {
	return &boundGauge{registry: m.registry, name: name}, nil
}

func (m *standardMeter) Float64Histogram(name string, opts ...MetricOption) (Float64Histogram, error) {
	return &boundHistogram{registry: m.registry, name: name}, nil
}

type boundCounter struct {
	registry *metrics.Registry
	name     string
}

func (b *boundCounter) Add(ctx context.Context, delta int64, attrs ...Attribute) {
	lbls := toLabelsMap(attrs)
	c := b.registry.Counter(b.name, lbls)
	c.Add(delta)
}

type boundGauge struct {
	registry *metrics.Registry
	name     string
}

func (b *boundGauge) Record(ctx context.Context, val float64, attrs ...Attribute) {
	lbls := toLabelsMap(attrs)
	g := b.registry.Gauge(b.name, lbls)
	g.Set(val)
}

type boundHistogram struct {
	registry *metrics.Registry
	name     string
}

func (b *boundHistogram) Record(ctx context.Context, val float64, attrs ...Attribute) {
	lbls := toLabelsMap(attrs)
	h := b.registry.Histogram(b.name, lbls)
	h.Observe(time.Duration(val * float64(time.Second)))
}

func toLabelsMap(attrs []Attribute) map[string]string {
	if len(attrs) == 0 {
		return nil
	}
	m := make(map[string]string, len(attrs))
	for _, a := range attrs {
		m[a.Key] = fmt.Sprintf("%v", a.Value)
	}
	return m
}

// ──────────────────────────────────────────────────────────────────────────────
// Helpers for semantic descriptors
// ──────────────────────────────────────────────────────────────────────────────

func describeMetric(name string) string {
	switch name {
	case "cloudx_controlplane_reconciliation_cycles_total":
		return "Total number of control plane reconciliation loops executed"
	case "cloudx_controlplane_reconciliation_failures_total":
		return "Total number of failed reconciliation passes"
	case "cloudx_controlplane_reconciliation_duration_seconds":
		return "Reconciliation cycle duration in seconds"
	case "cloudx_controlplane_scheduling_decisions_total":
		return "Total number of scheduling placement decisions made"
	case "cloudx_controlplane_scheduling_latency_seconds":
		return "Scheduling evaluation latency in seconds"
	case "cloudx_worker_cpu_usage_percent":
		return "Current CPU utilization percentage on the worker"
	case "cloudx_worker_memory_used_bytes":
		return "Current memory used on the worker in bytes"
	case "cloudx_worker_active_task_count":
		return "Current active tasks scheduled and running on this worker"
	case "cloudx_service_replicas_desired":
		return "Desired replica count for the service"
	case "cloudx_service_replicas_running":
		return "Actual running replica count for the service"
	case "cloudx_job_execution_duration_seconds":
		return "Job execution duration in seconds"
	case "cloudx_job_succeeded_total":
		return "Total successful job runs"
	case "cloudx_job_failed_total":
		return "Total failed job runs"
	default:
		return ""
	}
}

func inferUnit(name string) string {
	switch {
	case containsSuffix(name, "_seconds"):
		return "s"
	case containsSuffix(name, "_bytes"):
		return "By"
	case containsSuffix(name, "_percent"):
		return "%"
	case containsSuffix(name, "_total"):
		return "1"
	default:
		return "1"
	}
}

func containsSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}
