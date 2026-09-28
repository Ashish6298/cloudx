// Package otel provides OpenTelemetry-compatible tracing and metrics interfaces
// and default implementations for CloudX.
//
// Per Phase 59 — OpenTelemetry-Compatible Architecture (Milestone 16: Observability):
//
//   - Tracing/metrics interfaces compatible with OpenTelemetry semantics.
//   - Zero required external infrastructure: CloudX works completely out-of-the-box
//     with no external collector, Jaeger, or Prometheus server required.
//   - In-memory ring buffer trace exporter + no-op tracer + console exporter.
//   - Optional OTLP HTTP JSON exporter activated via CLOUDX_OTEL_ENDPOINT or config.
//   - Metrics bridge that exports internal/metrics Registry snapshots into OTEL-compatible
//     metric batches.
//   - Forward-compatible instrumentation: all components (reconciler, scheduler,
//     worker, state) can be instrumented now; if an external collector is later configured,
//     telemetry flows seamlessly with zero code redesign.
package otel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ──────────────────────────────────────────────────────────────────────────────
// OpenTelemetry Core Types & Semantic Constants
// ──────────────────────────────────────────────────────────────────────────────

// TraceID is a 16-byte (32 hex char) OpenTelemetry trace identifier.
type TraceID [16]byte

func (t TraceID) String() string {
	return hex.EncodeToString(t[:])
}

// IsValid returns true if TraceID is non-zero.
func (t TraceID) IsValid() bool {
	return t != TraceID{}
}

// NewTraceID generates a cryptographically secure random 16-byte TraceID.
func NewTraceID() TraceID {
	var tid TraceID
	if _, err := rand.Read(tid[:]); err != nil {
		// Fallback: timestamp entropy
		nano := time.Now().UnixNano()
		for i := 0; i < 8; i++ {
			tid[i] = byte(nano >> (i * 8))
			tid[i+8] = byte(nano ^ int64(i))
		}
	}
	return tid
}

// TraceIDFromHex parses a 32-hex-character string into a TraceID.
func TraceIDFromHex(s string) (TraceID, error) {
	var tid TraceID
	if len(s) != 32 {
		return tid, fmt.Errorf("invalid TraceID hex length: expected 32, got %d", len(s))
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return tid, fmt.Errorf("invalid TraceID hex: %w", err)
	}
	copy(tid[:], b)
	return tid, nil
}

// SpanID is an 8-byte (16 hex char) OpenTelemetry span identifier.
type SpanID [8]byte

func (s SpanID) String() string {
	return hex.EncodeToString(s[:])
}

// IsValid returns true if SpanID is non-zero.
func (s SpanID) IsValid() bool {
	return s != SpanID{}
}

// NewSpanID generates a cryptographically secure random 8-byte SpanID.
func NewSpanID() SpanID {
	var sid SpanID
	if _, err := rand.Read(sid[:]); err != nil {
		nano := time.Now().UnixNano()
		for i := 0; i < 8; i++ {
			sid[i] = byte(nano >> (i * 8))
		}
	}
	return sid
}

// SpanIDFromHex parses a 16-hex-character string into a SpanID.
func SpanIDFromHex(s string) (SpanID, error) {
	var sid SpanID
	if len(s) != 16 {
		return sid, fmt.Errorf("invalid SpanID hex length: expected 16, got %d", len(s))
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return sid, fmt.Errorf("invalid SpanID hex: %w", err)
	}
	copy(sid[:], b)
	return sid, nil
}

// SpanKind classifies the relationship of the span to the trace.
type SpanKind string

const (
	SpanKindInternal SpanKind = "INTERNAL"
	SpanKindServer   SpanKind = "SERVER"
	SpanKindClient   SpanKind = "CLIENT"
	SpanKindProducer SpanKind = "PRODUCER"
	SpanKindConsumer SpanKind = "CONSUMER"
)

// StatusCode represents the status of a Span according to OpenTelemetry.
type StatusCode string

const (
	StatusUnset StatusCode = "UNSET"
	StatusOK    StatusCode = "OK"
	StatusError StatusCode = "ERROR"
)

// SpanStatus holds the canonical code and human-readable description of a span result.
type SpanStatus struct {
	Code        StatusCode `json:"code"`
	Description string     `json:"description,omitempty"`
}

// Attribute is an immutable key-value pair representing span metadata.
type Attribute struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

// StringAttr returns a string Attribute.
func StringAttr(k, v string) Attribute { return Attribute{Key: k, Value: v} }

// IntAttr returns an int64 Attribute.
func IntAttr(k string, v int64) Attribute { return Attribute{Key: k, Value: v} }

// FloatAttr returns a float64 Attribute.
func FloatAttr(k string, v float64) Attribute { return Attribute{Key: k, Value: v} }

// BoolAttr returns a bool Attribute.
func BoolAttr(k string, v bool) Attribute { return Attribute{Key: k, Value: v} }

// Event represents an annotated point-in-time notification on a span.
type Event struct {
	Name       string      `json:"name"`
	Timestamp  time.Time   `json:"timestamp"`
	Attributes []Attribute `json:"attributes,omitempty"`
}

// SpanContext carries span identifiers and trace options across process boundaries.
type SpanContext struct {
	TraceID    TraceID `json:"trace_id"`
	SpanID     SpanID  `json:"span_id"`
	TraceFlags byte    `json:"trace_flags"`
}

// IsValid returns true if both TraceID and SpanID are valid.
func (sc SpanContext) IsValid() bool {
	return sc.TraceID.IsValid() && sc.SpanID.IsValid()
}

// ──────────────────────────────────────────────────────────────────────────────
// ReadOnlySpan & Span Snapshot Data
// ──────────────────────────────────────────────────────────────────────────────

// ReadOnlySpan represents an immutable or finished span snapshot ready for export or inspection.
type ReadOnlySpan struct {
	Name         string            `json:"name"`
	SpanContext  SpanContext       `json:"span_context"`
	ParentSpanID SpanID            `json:"parent_span_id,omitempty"`
	SpanKind     SpanKind          `json:"span_kind"`
	StartTime    time.Time         `json:"start_time"`
	EndTime      time.Time         `json:"end_time"`
	DurationMs   float64           `json:"duration_ms"`
	Status       SpanStatus        `json:"status"`
	Attributes   map[string]any    `json:"attributes,omitempty"`
	Events       []Event           `json:"events,omitempty"`
	Resource     map[string]string `json:"resource,omitempty"`
}

// ──────────────────────────────────────────────────────────────────────────────
// Tracing Interfaces
// ──────────────────────────────────────────────────────────────────────────────

// Span represents an active, mutable OpenTelemetry-compatible span.
type Span interface {
	// SpanContext returns the SpanContext for this span.
	SpanContext() SpanContext
	// IsRecording returns true if this span is recording telemetry.
	IsRecording() bool
	// SetStatus sets the span status code and description.
	SetStatus(code StatusCode, description string)
	// SetAttributes adds or updates attributes on this span.
	SetAttributes(attrs ...Attribute)
	// AddEvent records a point-in-time event on this span.
	AddEvent(name string, attrs ...Attribute)
	// RecordError records an error event and marks the span Status to StatusError.
	RecordError(err error)
	// End completes the span with the current timestamp.
	End()
}

// Tracer creates Spans. It is goroutine-safe.
type Tracer interface {
	// Start starts a new span, returning a context carrying the span and the Span itself.
	Start(ctx context.Context, spanName string, opts ...SpanOption) (context.Context, Span)
}

// TracerProvider manages access to named Tracer instances.
type TracerProvider interface {
	// Tracer returns a Tracer with the given instrumentation scope name.
	Tracer(name string) Tracer
}

// SpanOption configures span creation.
type SpanOption func(*spanConfig)

type spanConfig struct {
	kind       SpanKind
	attributes []Attribute
	startTime  time.Time
}

// WithSpanKind sets the SpanKind on span creation.
func WithSpanKind(kind SpanKind) SpanOption {
	return func(c *spanConfig) { c.kind = kind }
}

// WithAttributes adds initial attributes on span creation.
func WithAttributes(attrs ...Attribute) SpanOption {
	return func(c *spanConfig) { c.attributes = append(c.attributes, attrs...) }
}

// WithStartTime sets an explicit start time.
func WithStartTime(t time.Time) SpanOption {
	return func(c *spanConfig) { c.startTime = t }
}

// ──────────────────────────────────────────────────────────────────────────────
// Context Key for Trace Propagation
// ──────────────────────────────────────────────────────────────────────────────

type contextKey struct{}

var activeSpanKey = contextKey{}

// ContextWithSpan returns a new context carrying the given span.
func ContextWithSpan(ctx context.Context, span Span) context.Context {
	return context.WithValue(ctx, activeSpanKey, span)
}

// SpanFromContext returns the current active Span from context, or a NoopSpan if none.
func SpanFromContext(ctx context.Context) Span {
	if ctx == nil {
		return noopSpanInstance
	}
	if v, ok := ctx.Value(activeSpanKey).(Span); ok && v != nil {
		return v
	}
	return noopSpanInstance
}

// ──────────────────────────────────────────────────────────────────────────────
// SpanExporter Interface
// ──────────────────────────────────────────────────────────────────────────────

// SpanExporter handles exporting batches of finished spans.
type SpanExporter interface {
	// ExportSpans receives a batch of ReadOnlySpans and exports them.
	ExportSpans(ctx context.Context, spans []ReadOnlySpan) error
	// Shutdown flushes pending spans and releases resources.
	Shutdown(ctx context.Context) error
}

// ──────────────────────────────────────────────────────────────────────────────
// Default Implementation: RecordableSpan & Tracer
// ──────────────────────────────────────────────────────────────────────────────

type recordableSpan struct {
	mu           sync.RWMutex
	name         string
	spanContext  SpanContext
	parentSpanID SpanID
	kind         SpanKind
	startTime    time.Time
	endTime      time.Time
	status       SpanStatus
	attributes   map[string]any
	events       []Event
	ended        bool
	tracer       *standardTracer
}

func (s *recordableSpan) SpanContext() SpanContext { return s.spanContext }

func (s *recordableSpan) IsRecording() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return !s.ended
}

func (s *recordableSpan) SetStatus(code StatusCode, description string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	s.status = SpanStatus{Code: code, Description: description}
}

func (s *recordableSpan) SetAttributes(attrs ...Attribute) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	if s.attributes == nil {
		s.attributes = make(map[string]any)
	}
	for _, a := range attrs {
		s.attributes[a.Key] = a.Value
	}
}

func (s *recordableSpan) AddEvent(name string, attrs ...Attribute) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	s.events = append(s.events, Event{
		Name:       name,
		Timestamp:  time.Now().UTC(),
		Attributes: attrs,
	})
}

func (s *recordableSpan) RecordError(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	s.status = SpanStatus{Code: StatusError, Description: err.Error()}
	s.events = append(s.events, Event{
		Name:      "exception",
		Timestamp: time.Now().UTC(),
		Attributes: []Attribute{
			StringAttr("exception.message", err.Error()),
		},
	})
}

func (s *recordableSpan) End() {
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	s.ended = true
	s.endTime = time.Now().UTC()
	if s.status.Code == "" {
		s.status.Code = StatusOK
	}

	attrsCopy := make(map[string]any, len(s.attributes))
	for k, v := range s.attributes {
		attrsCopy[k] = v
	}
	eventsCopy := make([]Event, len(s.events))
	copy(eventsCopy, s.events)

	durMs := float64(s.endTime.Sub(s.startTime).Microseconds()) / 1000.0

	ro := ReadOnlySpan{
		Name:         s.name,
		SpanContext:  s.spanContext,
		ParentSpanID: s.parentSpanID,
		SpanKind:     s.kind,
		StartTime:    s.startTime,
		EndTime:      s.endTime,
		DurationMs:   durMs,
		Status:       s.status,
		Attributes:   attrsCopy,
		Events:       eventsCopy,
	}
	tracer := s.tracer
	s.mu.Unlock()

	if tracer != nil {
		tracer.onSpanEnd(ro)
	}
}

// standardTracer implements Tracer.
type standardTracer struct {
	name     string
	provider *StandardProvider
}

func (t *standardTracer) Start(ctx context.Context, spanName string, opts ...SpanOption) (context.Context, Span) {
	cfg := spanConfig{
		kind:      SpanKindInternal,
		startTime: time.Now().UTC(),
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	// Trace context propagation
	var traceID TraceID
	var parentSpanID SpanID

	parentSpan := SpanFromContext(ctx)
	if parentSpan != nil && parentSpan.SpanContext().IsValid() {
		traceID = parentSpan.SpanContext().TraceID
		parentSpanID = parentSpan.SpanContext().SpanID
	} else {
		traceID = NewTraceID()
	}

	spanID := NewSpanID()

	attrs := make(map[string]any)
	for _, a := range cfg.attributes {
		attrs[a.Key] = a.Value
	}

	s := &recordableSpan{
		name: spanName,
		spanContext: SpanContext{
			TraceID:    traceID,
			SpanID:     spanID,
			TraceFlags: 0x01, // sampled
		},
		parentSpanID: parentSpanID,
		kind:         cfg.kind,
		startTime:    cfg.startTime,
		attributes:   attrs,
		status:       SpanStatus{Code: StatusUnset},
		tracer:       t,
	}

	return ContextWithSpan(ctx, s), s
}

func (t *standardTracer) onSpanEnd(span ReadOnlySpan) {
	if t.provider != nil {
		t.provider.recordSpan(span)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// In-Memory Ring Buffer Exporter (Default — Zero External Dependency)
// ──────────────────────────────────────────────────────────────────────────────

// RingBufferExporter stores the last N finished spans in an in-memory ring buffer.
// Completely self-contained, thread-safe, and zero external infrastructure required.
type RingBufferExporter struct {
	mu       sync.RWMutex
	capacity int
	spans    []ReadOnlySpan
	total    int64
}

// NewRingBufferExporter creates a ring buffer exporter with capacity N spans (default 256).
func NewRingBufferExporter(capacity int) *RingBufferExporter {
	if capacity <= 0 {
		capacity = 256
	}
	return &RingBufferExporter{
		capacity: capacity,
		spans:    make([]ReadOnlySpan, 0, capacity),
	}
}

func (r *RingBufferExporter) ExportSpans(ctx context.Context, spans []ReadOnlySpan) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, s := range spans {
		atomic.AddInt64(&r.total, 1)
		if len(r.spans) < r.capacity {
			r.spans = append(r.spans, s)
		} else {
			// Evict oldest (FIFO)
			copy(r.spans[0:], r.spans[1:])
			r.spans[len(r.spans)-1] = s
		}
	}
	return nil
}

func (r *RingBufferExporter) Shutdown(ctx context.Context) error {
	return nil
}

// Spans returns a copy of all retained spans in chronological order.
func (r *RingBufferExporter) Spans() []ReadOnlySpan {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ReadOnlySpan, len(r.spans))
	copy(out, r.spans)
	return out
}

// TotalExported returns total number of spans exported since creation.
func (r *RingBufferExporter) TotalExported() int64 {
	return atomic.LoadInt64(&r.total)
}

// Clear flushes all stored spans.
func (r *RingBufferExporter) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.spans = r.spans[:0]
}

// ──────────────────────────────────────────────────────────────────────────────
// No-op Implementations (Zero Cost when telemetry is disabled)
// ──────────────────────────────────────────────────────────────────────────────

type noopSpan struct{}

var noopSpanInstance = &noopSpan{}

func (n *noopSpan) SpanContext() SpanContext                 { return SpanContext{} }
func (n *noopSpan) IsRecording() bool                        { return false }
func (n *noopSpan) SetStatus(code StatusCode, desc string)   {}
func (n *noopSpan) SetAttributes(attrs ...Attribute)         {}
func (n *noopSpan) AddEvent(name string, attrs ...Attribute) {}
func (n *noopSpan) RecordError(err error)                    {}
func (n *noopSpan) End()                                     {}

// NoopTracer returns no-op spans with zero overhead.
type NoopTracer struct{}

func (n *NoopTracer) Start(ctx context.Context, name string, opts ...SpanOption) (context.Context, Span) {
	return ctx, noopSpanInstance
}

// NoopTracerProvider returns a NoopTracer.
type NoopTracerProvider struct{}

func (n *NoopTracerProvider) Tracer(name string) Tracer { return &NoopTracer{} }

// ──────────────────────────────────────────────────────────────────────────────
// Provider: Central OpenTelemetry Coordinator for CloudX
// ──────────────────────────────────────────────────────────────────────────────

// ProviderConfig configures telemetry provider settings.
type ProviderConfig struct {
	ServiceName    string        // e.g. "cloudx-controlplane" or "cloudx-worker"
	ServiceVersion string        // e.g. "0.1.0"
	NodeID         string        // e.g. "worker-1"
	OTLPEndpoint   string        // optional OTLP HTTP endpoint (e.g. "http://localhost:4318")
	BufferCapacity int           // in-memory buffer capacity (default 512)
	BatchInterval  time.Duration // batch export interval
	Disabled       bool          // if true, use no-op tracer
}

// StandardProvider is CloudX's primary OpenTelemetry-compatible telemetry provider.
type StandardProvider struct {
	mu             sync.RWMutex
	cfg            ProviderConfig
	tracers        map[string]*standardTracer
	exporters      []SpanExporter
	ringBuffer     *RingBufferExporter
	resourceAttrs  map[string]string
	spanQueue      chan ReadOnlySpan
	stopCh         chan struct{}
	wg             sync.WaitGroup
	running        bool
	spansCollected int64
}

// NewProvider creates a new OpenTelemetry Provider with built-in in-memory ring buffer.
func NewProvider(cfg ProviderConfig) *StandardProvider {
	if cfg.ServiceName == "" {
		cfg.ServiceName = "cloudx"
	}
	if cfg.BufferCapacity <= 0 {
		cfg.BufferCapacity = 512
	}
	if cfg.BatchInterval <= 0 {
		cfg.BatchInterval = 500 * time.Millisecond
	}

	ring := NewRingBufferExporter(cfg.BufferCapacity)

	res := map[string]string{
		"service.name":    cfg.ServiceName,
		"service.version": cfg.ServiceVersion,
	}
	if cfg.NodeID != "" {
		res["host.name"] = cfg.NodeID
		res["cloudx.node.id"] = cfg.NodeID
	}

	p := &StandardProvider{
		cfg:           cfg,
		tracers:       make(map[string]*standardTracer),
		exporters:     []SpanExporter{},
		ringBuffer:    ring,
		resourceAttrs: res,
		spanQueue:     make(chan ReadOnlySpan, cfg.BufferCapacity*2),
		stopCh:        make(chan struct{}),
	}

	// If OTLP endpoint configured, add OTLP exporter
	if cfg.OTLPEndpoint != "" {
		p.exporters = append(p.exporters, NewOTLPHTTPExporter(cfg.OTLPEndpoint, res))
	}

	p.start()
	return p
}

func (p *StandardProvider) start() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.running {
		return
	}
	p.running = true
	p.wg.Add(1)
	go p.exportLoop()
}

func (p *StandardProvider) exportLoop() {
	defer p.wg.Done()
	ticker := time.NewTicker(p.cfg.BatchInterval)
	defer ticker.Stop()

	var batch []ReadOnlySpan

	flush := func() {
		if len(batch) == 0 {
			return
		}
		p.mu.RLock()
		exporters := make([]SpanExporter, len(p.exporters))
		copy(exporters, p.exporters)
		p.mu.RUnlock()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		for _, exp := range exporters {
			_ = exp.ExportSpans(ctx, batch)
		}
		cancel()
		batch = batch[:0]
	}

	for {
		select {
		case <-p.stopCh:
			// Drain queue
			for {
				select {
				case s := <-p.spanQueue:
					batch = append(batch, s)
				default:
					flush()
					return
				}
			}
		case s := <-p.spanQueue:
			batch = append(batch, s)
			if len(batch) >= 64 {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// Tracer returns a named Tracer instance.
func (p *StandardProvider) Tracer(name string) Tracer {
	if p.cfg.Disabled {
		return &NoopTracer{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if t, exists := p.tracers[name]; exists {
		return t
	}
	t := &standardTracer{name: name, provider: p}
	p.tracers[name] = t
	return t
}

func (p *StandardProvider) recordSpan(span ReadOnlySpan) {
	atomic.AddInt64(&p.spansCollected, 1)
	// Inject resource attributes
	span.Resource = p.resourceAttrs

	// Always immediately write to internal ring buffer for zero-latency local inspection
	if p.ringBuffer != nil {
		_ = p.ringBuffer.ExportSpans(context.Background(), []ReadOnlySpan{span})
	}

	// Also send to asynchronous batch queue for remote exporters (e.g. OTLP)
	select {
	case p.spanQueue <- span:
	default:
	}
}

// AddExporter registers an additional custom SpanExporter.
func (p *StandardProvider) AddExporter(exp SpanExporter) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.exporters = append(p.exporters, exp)
}

// RingBuffer returns the default in-memory ring buffer exporter.
func (p *StandardProvider) RingBuffer() *RingBufferExporter {
	return p.ringBuffer
}

// SpansCollected returns total number of spans collected by provider.
func (p *StandardProvider) SpansCollected() int64 {
	return atomic.LoadInt64(&p.spansCollected)
}

// StatusSummary returns diagnostic information about provider status.
type StatusSummary struct {
	ServiceName     string            `json:"service_name"`
	ServiceVersion  string            `json:"service_version"`
	OTLPEndpoint    string            `json:"otlp_endpoint,omitempty"`
	OTLPConfigured  bool              `json:"otlp_configured"`
	ExporterCount   int               `json:"exporter_count"`
	BufferCapacity  int               `json:"buffer_capacity"`
	StoredSpans     int               `json:"stored_spans"`
	TotalCollected  int64             `json:"total_collected"`
	ResourceSummary map[string]string `json:"resource"`
}

// Status returns a point-in-time status summary of the provider.
func (p *StandardProvider) Status() StatusSummary {
	p.mu.RLock()
	defer p.mu.RUnlock()

	stored := 0
	if p.ringBuffer != nil {
		stored = len(p.ringBuffer.Spans())
	}

	return StatusSummary{
		ServiceName:     p.cfg.ServiceName,
		ServiceVersion:  p.cfg.ServiceVersion,
		OTLPEndpoint:    p.cfg.OTLPEndpoint,
		OTLPConfigured:  p.cfg.OTLPEndpoint != "",
		ExporterCount:   len(p.exporters),
		BufferCapacity:  p.cfg.BufferCapacity,
		StoredSpans:     stored,
		TotalCollected:  p.SpansCollected(),
		ResourceSummary: p.resourceAttrs,
	}
}

// Shutdown cleanly stops the provider and flushes all pending spans.
func (p *StandardProvider) Shutdown(ctx context.Context) error {
	p.mu.Lock()
	if !p.running {
		p.mu.Unlock()
		return nil
	}
	p.running = false
	close(p.stopCh)
	p.mu.Unlock()

	p.wg.Wait()

	p.mu.RLock()
	exporters := p.exporters
	p.mu.RUnlock()

	var firstErr error
	for _, exp := range exporters {
		if err := exp.Shutdown(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// ──────────────────────────────────────────────────────────────────────────────
// Global Provider Singleton
// ──────────────────────────────────────────────────────────────────────────────

var (
	globalProviderMu sync.RWMutex
	globalProvider   *StandardProvider
)

// SetGlobalProvider sets the system-wide OpenTelemetry Provider.
func SetGlobalProvider(p *StandardProvider) {
	globalProviderMu.Lock()
	defer globalProviderMu.Unlock()
	globalProvider = p
}

// GetGlobalProvider returns the current system-wide Provider (creates default if nil).
func GetGlobalProvider() *StandardProvider {
	globalProviderMu.Lock()
	defer globalProviderMu.Unlock()
	if globalProvider == nil {
		globalProvider = NewProvider(ProviderConfig{
			ServiceName: "cloudx",
		})
	}
	return globalProvider
}

// GetTracer is a convenience function to get a named Tracer from the global provider.
func GetTracer(name string) Tracer {
	return GetGlobalProvider().Tracer(name)
}

// ──────────────────────────────────────────────────────────────────────────────
// W3C TraceContext Propagation (traceparent header)
// ──────────────────────────────────────────────────────────────────────────────

// InjectTraceparent encodes a SpanContext into a W3C traceparent header string.
// Format: 00-{trace_id}-{span_id}-{trace_flags}
// Example: 00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01
func InjectTraceparent(sc SpanContext) string {
	if !sc.IsValid() {
		return ""
	}
	flags := sc.TraceFlags
	if flags == 0 {
		flags = 0x01 // default sampled
	}
	return fmt.Sprintf("00-%s-%s-%02x", sc.TraceID.String(), sc.SpanID.String(), flags)
}

// ExtractTraceparent parses a W3C traceparent header string into a SpanContext.
func ExtractTraceparent(header string) (SpanContext, error) {
	header = strings.TrimSpace(header)
	parts := strings.Split(header, "-")
	if len(parts) != 4 {
		return SpanContext{}, fmt.Errorf("invalid traceparent format: expected 4 segments, got %d", len(parts))
	}
	if parts[0] != "00" {
		return SpanContext{}, fmt.Errorf("unsupported traceparent version: %s", parts[0])
	}

	tid, err := TraceIDFromHex(parts[1])
	if err != nil {
		return SpanContext{}, fmt.Errorf("invalid trace_id in traceparent: %w", err)
	}

	sid, err := SpanIDFromHex(parts[2])
	if err != nil {
		return SpanContext{}, fmt.Errorf("invalid span_id in traceparent: %w", err)
	}

	var flags byte
	if _, err := fmt.Sscanf(parts[3], "%02x", &flags); err != nil {
		flags = 0x01
	}

	return SpanContext{
		TraceID:    tid,
		SpanID:     sid,
		TraceFlags: flags,
	}, nil
}
