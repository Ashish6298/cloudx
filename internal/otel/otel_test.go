package otel_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/metrics"
	"github.com/cloudx-org/cloudx/internal/otel"
)

func TestTraceIDAndSpanID(t *testing.T) {
	tid := otel.NewTraceID()
	if !tid.IsValid() {
		t.Fatal("expected valid TraceID")
	}
	if len(tid.String()) != 32 {
		t.Fatalf("expected 32 hex chars, got %d (%s)", len(tid.String()), tid.String())
	}

	sid := otel.NewSpanID()
	if !sid.IsValid() {
		t.Fatal("expected valid SpanID")
	}
	if len(sid.String()) != 16 {
		t.Fatalf("expected 16 hex chars, got %d (%s)", len(sid.String()), sid.String())
	}

	// Round-trip parse
	parsedTid, err := otel.TraceIDFromHex(tid.String())
	if err != nil {
		t.Fatalf("failed to parse valid TraceID hex: %v", err)
	}
	if parsedTid != tid {
		t.Fatalf("expected %s, got %s", tid.String(), parsedTid.String())
	}

	parsedSid, err := otel.SpanIDFromHex(sid.String())
	if err != nil {
		t.Fatalf("failed to parse valid SpanID hex: %v", err)
	}
	if parsedSid != sid {
		t.Fatalf("expected %s, got %s", sid.String(), parsedSid.String())
	}
}

func TestW3CTraceparent(t *testing.T) {
	tid := otel.NewTraceID()
	sid := otel.NewSpanID()
	sc := otel.SpanContext{
		TraceID:    tid,
		SpanID:     sid,
		TraceFlags: 0x01,
	}

	header := otel.InjectTraceparent(sc)
	if header == "" {
		t.Fatal("expected non-empty traceparent header")
	}

	extracted, err := otel.ExtractTraceparent(header)
	if err != nil {
		t.Fatalf("failed to extract traceparent: %v", err)
	}
	if extracted.TraceID != tid {
		t.Fatalf("expected TraceID %s, got %s", tid.String(), extracted.TraceID.String())
	}
	if extracted.SpanID != sid {
		t.Fatalf("expected SpanID %s, got %s", sid.String(), extracted.SpanID.String())
	}
	if extracted.TraceFlags != 0x01 {
		t.Fatalf("expected TraceFlags 0x01, got %02x", extracted.TraceFlags)
	}

	// Test invalid traceparent formats
	if _, err := otel.ExtractTraceparent("invalid"); err == nil {
		t.Fatal("expected error on malformed traceparent")
	}
	if _, err := otel.ExtractTraceparent("01-invalid-invalid-01"); err == nil {
		t.Fatal("expected error on non-00 version")
	}
}

func TestTracerAndSpanLifecycle(t *testing.T) {
	provider := otel.NewProvider(otel.ProviderConfig{
		ServiceName:    "test-service",
		ServiceVersion: "1.0.0",
		NodeID:         "node-test",
		BufferCapacity: 100,
	})
	defer provider.Shutdown(context.Background())

	tracer := provider.Tracer("test-tracer")

	ctx, span := tracer.Start(context.Background(), "reconcile.pass",
		otel.WithSpanKind(otel.SpanKindInternal),
		otel.WithAttributes(
			otel.StringAttr("cloudx.cluster_id", "cluster-1"),
			otel.IntAttr("replicas.evaluated", 3),
		),
	)

	if !span.IsRecording() {
		t.Fatal("expected span to be recording")
	}

	span.SetAttributes(otel.BoolAttr("success", true))
	span.AddEvent("evaluating_services", otel.IntAttr("count", 3))
	span.SetStatus(otel.StatusOK, "reconciliation complete")
	span.End()

	if span.IsRecording() {
		t.Fatal("expected span to not be recording after End()")
	}

	// Wait briefly for background export loop
	time.Sleep(50 * time.Millisecond)

	spans := provider.RingBuffer().Spans()
	if len(spans) == 0 {
		t.Fatal("expected at least 1 exported span in ring buffer")
	}

	last := spans[len(spans)-1]
	if last.Name != "reconcile.pass" {
		t.Fatalf("expected span name 'reconcile.pass', got '%s'", last.Name)
	}
	if last.SpanKind != otel.SpanKindInternal {
		t.Fatalf("expected INTERNAL kind, got %s", last.SpanKind)
	}
	if last.Status.Code != otel.StatusOK {
		t.Fatalf("expected OK status, got %s", last.Status.Code)
	}
	if last.Attributes["cloudx.cluster_id"] != "cluster-1" {
		t.Fatalf("expected cluster-1 attribute, got %v", last.Attributes["cloudx.cluster_id"])
	}
	if len(last.Events) != 1 || last.Events[0].Name != "evaluating_services" {
		t.Fatalf("expected event 'evaluating_services', got %v", last.Events)
	}
	if last.Resource["service.name"] != "test-service" {
		t.Fatalf("expected resource service.name 'test-service', got %v", last.Resource["service.name"])
	}

	_ = ctx
}

func TestNestedSpansAndTracePropagation(t *testing.T) {
	provider := otel.NewProvider(otel.ProviderConfig{
		ServiceName:    "scheduler-test",
		BufferCapacity: 50,
	})
	defer provider.Shutdown(context.Background())

	tracer := provider.Tracer("scheduler")

	// Parent span
	ctx, parent := tracer.Start(context.Background(), "schedule.task")
	parentTID := parent.SpanContext().TraceID
	parentSID := parent.SpanContext().SpanID

	// Child span inheriting context
	childCtx, child := tracer.Start(ctx, "score.nodes")
	if child.SpanContext().TraceID != parentTID {
		t.Fatalf("child must inherit parent TraceID (%s vs %s)",
			parentTID.String(), child.SpanContext().TraceID.String())
	}
	child.SetStatus(otel.StatusOK, "scored 3 nodes")
	child.End()

	parent.SetStatus(otel.StatusOK, "scheduled on node-1")
	parent.End()

	time.Sleep(50 * time.Millisecond)

	spans := provider.RingBuffer().Spans()
	if len(spans) < 2 {
		t.Fatalf("expected at least 2 spans, got %d", len(spans))
	}

	// Verify child has ParentSpanID == parentSID
	var foundChild bool
	for _, s := range spans {
		if s.Name == "score.nodes" {
			foundChild = true
			if s.ParentSpanID != parentSID {
				t.Fatalf("expected child ParentSpanID %s, got %s", parentSID.String(), s.ParentSpanID.String())
			}
		}
	}
	if !foundChild {
		t.Fatal("score.nodes span not found in buffer")
	}

	_ = childCtx
}

func TestRecordErrorOnSpan(t *testing.T) {
	provider := otel.NewProvider(otel.ProviderConfig{BufferCapacity: 10})
	defer provider.Shutdown(context.Background())

	tracer := provider.Tracer("error-tracer")
	_, span := tracer.Start(context.Background(), "failing.operation")
	testErr := errors.New("connection refused to worker-2")
	span.RecordError(testErr)
	span.End()

	time.Sleep(50 * time.Millisecond)

	spans := provider.RingBuffer().Spans()
	if len(spans) == 0 {
		t.Fatal("expected span")
	}
	s := spans[len(spans)-1]
	if s.Status.Code != otel.StatusError {
		t.Fatalf("expected ERROR status code, got %s", s.Status.Code)
	}
	if s.Status.Description != "connection refused to worker-2" {
		t.Fatalf("unexpected description: %s", s.Status.Description)
	}
	if len(s.Events) != 1 || s.Events[0].Name != "exception" {
		t.Fatalf("expected exception event, got %v", s.Events)
	}
}

func TestNoopTracerZeroCost(t *testing.T) {
	noopTracer := &otel.NoopTracer{}
	ctx, span := noopTracer.Start(context.Background(), "noop.span")
	if span.IsRecording() {
		t.Fatal("noop span must not be recording")
	}
	// All operations should be no-ops and not panic
	span.SetStatus(otel.StatusOK, "ok")
	span.SetAttributes(otel.StringAttr("k", "v"))
	span.AddEvent("test")
	span.RecordError(errors.New("err"))
	span.End()

	if span.SpanContext().IsValid() {
		t.Fatal("noop span context must not be valid")
	}
	_ = ctx
}

func TestRingBufferEviction(t *testing.T) {
	rb := otel.NewRingBufferExporter(3)
	ctx := context.Background()

	for i := 1; i <= 5; i++ {
		_ = rb.ExportSpans(ctx, []otel.ReadOnlySpan{
			{Name: string(rune('A' + i - 1))},
		})
	}

	if rb.TotalExported() != 5 {
		t.Fatalf("expected total 5, got %d", rb.TotalExported())
	}
	spans := rb.Spans()
	if len(spans) != 3 {
		t.Fatalf("expected 3 retained spans, got %d", len(spans))
	}
	// FIFO eviction: spans should be C, D, E
	if spans[0].Name != "C" || spans[1].Name != "D" || spans[2].Name != "E" {
		t.Fatalf("expected C, D, E; got %s, %s, %s", spans[0].Name, spans[1].Name, spans[2].Name)
	}
}

func TestMetricsBridge(t *testing.T) {
	reg := metrics.NewRegistry()
	c := reg.Counter("cloudx_controlplane_reconciliation_cycles_total", nil)
	c.Add(42)

	g := reg.Gauge("cloudx_worker_cpu_usage_percent", map[string]string{"worker_id": "w1"})
	g.Set(18.5)

	h := reg.Histogram("cloudx_controlplane_scheduling_latency_seconds", nil)
	h.Observe(25 * time.Millisecond)

	bridge := otel.NewMetricsBridge(reg, map[string]string{
		"service.name": "cloudx-test",
	}, "cloudx.controlplane", "0.1.0")

	rm, err := bridge.Collect(context.Background())
	if err != nil {
		t.Fatalf("failed to collect bridged metrics: %v", err)
	}

	if rm.Resource["service.name"] != "cloudx-test" {
		t.Fatalf("expected resource service.name 'cloudx-test', got %v", rm.Resource["service.name"])
	}

	if len(rm.ScopeMetrics) == 0 {
		t.Fatal("expected ScopeMetrics")
	}

	mList := rm.ScopeMetrics[0].Metrics
	if len(mList) != 3 {
		t.Fatalf("expected 3 metrics, got %d", len(mList))
	}

	// Verify counter, gauge, histogram types
	var foundCounter, foundGauge, foundHist bool
	for _, m := range mList {
		switch m.Type {
		case otel.MetricTypeSum:
			foundCounter = true
			if len(m.DataPoints) == 0 || m.DataPoints[0].Value != 42 {
				t.Fatalf("expected counter value 42, got %v", m.DataPoints)
			}
		case otel.MetricTypeGauge:
			foundGauge = true
			if len(m.DataPoints) == 0 || m.DataPoints[0].Value != 18.5 {
				t.Fatalf("expected gauge value 18.5, got %v", m.DataPoints)
			}
		case otel.MetricTypeHistogram:
			foundHist = true
			if len(m.HistPoints) == 0 || m.HistPoints[0].Count != 1 {
				t.Fatalf("expected histogram count 1, got %v", m.HistPoints)
			}
		}
	}

	if !foundCounter || !foundGauge || !foundHist {
		t.Fatalf("expected counter, gauge, hist; got c=%v g=%v h=%v", foundCounter, foundGauge, foundHist)
	}
}

func TestStandardMeterProvider(t *testing.T) {
	reg := metrics.NewRegistry()
	mp := otel.NewStandardMeterProvider(reg)
	meter := mp.Meter("test.scope")

	counter, err := meter.Int64Counter("cloudx_job_succeeded_total")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	counter.Add(context.Background(), 5, otel.StringAttr("job_id", "job-123"))

	c := reg.Counter("cloudx_job_succeeded_total", map[string]string{"job_id": "job-123"})
	if c.Value() != 5 {
		t.Fatalf("expected counter value 5, got %d", c.Value())
	}

	gauge, err := meter.Float64Gauge("cloudx_service_replicas_running")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gauge.Record(context.Background(), 3.0, otel.StringAttr("service_id", "srv-1"))

	g := reg.Gauge("cloudx_service_replicas_running", map[string]string{"service_id": "srv-1"})
	if g.Value() != 3.0 {
		t.Fatalf("expected gauge value 3.0, got %f", g.Value())
	}
}

func TestProviderStatus(t *testing.T) {
	provider := otel.NewProvider(otel.ProviderConfig{
		ServiceName:    "cloudx-cp",
		ServiceVersion: "0.1.0",
		NodeID:         "node-alpha",
		OTLPEndpoint:   "http://localhost:4318",
		BufferCapacity: 64,
	})
	defer provider.Shutdown(context.Background())

	st := provider.Status()
	if st.ServiceName != "cloudx-cp" {
		t.Fatalf("expected cloudx-cp, got %s", st.ServiceName)
	}
	if !st.OTLPConfigured {
		t.Fatal("expected OTLPConfigured true")
	}
	if st.OTLPEndpoint != "http://localhost:4318" {
		t.Fatalf("expected OTLPEndpoint, got %s", st.OTLPEndpoint)
	}
	if st.BufferCapacity != 64 {
		t.Fatalf("expected buffer capacity 64, got %d", st.BufferCapacity)
	}
}
