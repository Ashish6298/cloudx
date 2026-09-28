// Package otel provides exporters for OpenTelemetry spans.
package otel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

// ──────────────────────────────────────────────────────────────────────────────
// Console / Stream Span Exporter (For local debugging & diagnostics)
// ──────────────────────────────────────────────────────────────────────────────

// ConsoleExporter writes human-readable or JSON-formatted spans to an io.Writer (default os.Stdout).
type ConsoleExporter struct {
	mu     sync.Mutex
	writer io.Writer
	json   bool
}

// NewConsoleExporter creates a new ConsoleExporter.
func NewConsoleExporter(w io.Writer, jsonOutput bool) *ConsoleExporter {
	if w == nil {
		w = os.Stdout
	}
	return &ConsoleExporter{
		writer: w,
		json:   jsonOutput,
	}
}

func (c *ConsoleExporter) ExportSpans(ctx context.Context, spans []ReadOnlySpan) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, s := range spans {
		if c.json {
			data, err := json.Marshal(s)
			if err != nil {
				return err
			}
			fmt.Fprintln(c.writer, string(data))
		} else {
			fmt.Fprintf(c.writer, "[SPAN] %s [%s] %s trace_id=%s span_id=%s dur=%.2fms status=%s\n",
				s.StartTime.Format("15:04:05.000"),
				s.SpanKind,
				s.Name,
				s.SpanContext.TraceID.String()[:8]+"...",
				s.SpanContext.SpanID.String()[:8],
				s.DurationMs,
				s.Status.Code,
			)
		}
	}
	return nil
}

func (c *ConsoleExporter) Shutdown(ctx context.Context) error {
	return nil
}

// ──────────────────────────────────────────────────────────────────────────────
// OTLP HTTP / JSON Exporter (Standard OpenTelemetry Collector Compatibility)
// ──────────────────────────────────────────────────────────────────────────────

// OTLPHTTPExporter sends finished spans via standard OTLP/HTTP JSON to an OpenTelemetry collector.
// Enabled only when an endpoint is configured (e.g. http://localhost:4318/v1/traces).
type OTLPHTTPExporter struct {
	endpoint   string
	client     *http.Client
	resource   map[string]string
	exportURL  string
	maxRetries int
}

// NewOTLPHTTPExporter creates an exporter targeting an OTLP HTTP receiver.
// endpoint can be e.g. "http://localhost:4318" or full "http://localhost:4318/v1/traces".
func NewOTLPHTTPExporter(endpoint string, resource map[string]string) *OTLPHTTPExporter {
	exportURL := endpoint
	if !bytes.Contains([]byte(exportURL), []byte("/v1/traces")) {
		if exportURL[len(exportURL)-1] == '/' {
			exportURL = exportURL + "v1/traces"
		} else {
			exportURL = exportURL + "/v1/traces"
		}
	}

	return &OTLPHTTPExporter{
		endpoint: endpoint,
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
		resource:   resource,
		exportURL:  exportURL,
		maxRetries: 2,
	}
}

// OTLP Span JSON wire format types
type otlpExportTraceServiceRequest struct {
	ResourceSpans []otlpResourceSpans `json:"resourceSpans"`
}

type otlpResourceSpans struct {
	Resource   otlpResource     `json:"resource"`
	ScopeSpans []otlpScopeSpans `json:"scopeSpans"`
}

type otlpResource struct {
	Attributes []otlpKeyValue `json:"attributes"`
}

type otlpScopeSpans struct {
	Scope otlpScope  `json:"scope"`
	Spans []otlpSpan `json:"spans"`
}

type otlpScope struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type otlpSpan struct {
	TraceID           string         `json:"traceId"`
	SpanID            string         `json:"spanId"`
	ParentSpanID      string         `json:"parentSpanId,omitempty"`
	Name              string         `json:"name"`
	Kind              int            `json:"kind"`
	StartTimeUnixNano string         `json:"startTimeUnixNano"`
	EndTimeUnixNano   string         `json:"endTimeUnixNano"`
	Attributes        []otlpKeyValue `json:"attributes,omitempty"`
	Status            otlpStatus     `json:"status"`
}

type otlpKeyValue struct {
	Key   string    `json:"key"`
	Value otlpValue `json:"value"`
}

type otlpValue struct {
	StringValue *string  `json:"stringValue,omitempty"`
	IntValue    *string  `json:"intValue,omitempty"`
	DoubleValue *float64 `json:"doubleValue,omitempty"`
	BoolValue   *bool    `json:"boolValue,omitempty"`
}

type otlpStatus struct {
	Code    int    `json:"code"` // 0=UNSET, 1=OK, 2=ERROR
	Message string `json:"message,omitempty"`
}

func (e *OTLPHTTPExporter) ExportSpans(ctx context.Context, spans []ReadOnlySpan) error {
	if len(spans) == 0 {
		return nil
	}

	// Build OTLP JSON payload
	var resourceAttrs []otlpKeyValue
	for k, v := range e.resource {
		vStr := v
		resourceAttrs = append(resourceAttrs, otlpKeyValue{
			Key:   k,
			Value: otlpValue{StringValue: &vStr},
		})
	}

	var otlpSpansList []otlpSpan
	for _, s := range spans {
		startNano := fmt.Sprintf("%d", s.StartTime.UnixNano())
		endNano := fmt.Sprintf("%d", s.EndTime.UnixNano())

		statusCode := 0
		switch s.Status.Code {
		case StatusOK:
			statusCode = 1
		case StatusError:
			statusCode = 2
		}

		spanKind := 1 // INTERNAL
		switch s.SpanKind {
		case SpanKindServer:
			spanKind = 2
		case SpanKindClient:
			spanKind = 3
		case SpanKindProducer:
			spanKind = 4
		case SpanKindConsumer:
			spanKind = 5
		}

		var attrs []otlpKeyValue
		for k, v := range s.Attributes {
			attrs = append(attrs, convertAttributeToOTLP(k, v))
		}

		otlpSpansList = append(otlpSpansList, otlpSpan{
			TraceID:           s.SpanContext.TraceID.String(),
			SpanID:            s.SpanContext.SpanID.String(),
			ParentSpanID:      s.ParentSpanID.String(),
			Name:              s.Name,
			Kind:              spanKind,
			StartTimeUnixNano: startNano,
			EndTimeUnixNano:   endNano,
			Attributes:        attrs,
			Status: otlpStatus{
				Code:    statusCode,
				Message: s.Status.Description,
			},
		})
	}

	reqBody := otlpExportTraceServiceRequest{
		ResourceSpans: []otlpResourceSpans{
			{
				Resource: otlpResource{Attributes: resourceAttrs},
				ScopeSpans: []otlpScopeSpans{
					{
						Scope: otlpScope{Name: "cloudx"},
						Spans: otlpSpansList,
					},
				},
			},
		},
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal OTLP trace request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.exportURL, bytes.NewReader(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create OTLP HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		// Non-blocking error: CloudX remains functional even if external collector is unreachable
		return fmt.Errorf("OTLP collector export error (%s): %w", e.exportURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("OTLP collector returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

func (e *OTLPHTTPExporter) Shutdown(ctx context.Context) error {
	return nil
}

func convertAttributeToOTLP(k string, v any) otlpKeyValue {
	switch val := v.(type) {
	case string:
		return otlpKeyValue{Key: k, Value: otlpValue{StringValue: &val}}
	case int:
		s := fmt.Sprintf("%d", val)
		return otlpKeyValue{Key: k, Value: otlpValue{IntValue: &s}}
	case int64:
		s := fmt.Sprintf("%d", val)
		return otlpKeyValue{Key: k, Value: otlpValue{IntValue: &s}}
	case float64:
		return otlpKeyValue{Key: k, Value: otlpValue{DoubleValue: &val}}
	case bool:
		return otlpKeyValue{Key: k, Value: otlpValue{BoolValue: &val}}
	default:
		s := fmt.Sprintf("%v", val)
		return otlpKeyValue{Key: k, Value: otlpValue{StringValue: &s}}
	}
}
