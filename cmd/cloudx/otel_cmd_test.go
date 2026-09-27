package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/otel"
)

func TestOtelStatusCmd(t *testing.T) {
	cmd := newRootCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"otel", "status"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("failed to execute otel status: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "OpenTelemetry Architecture Status") {
		t.Errorf("expected header in output, got: %s", out)
	}
	if !strings.Contains(out, "STANDALONE") && !strings.Contains(out, "HYBRID") {
		t.Errorf("expected Telemetry Mode in output, got: %s", out)
	}
}

func TestOtelStatusCmdJSON(t *testing.T) {
	cmd := newRootCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"otel", "status", "--json"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("failed to execute otel status --json: %v", err)
	}

	var status otel.StatusSummary
	if err := json.Unmarshal(buf.Bytes(), &status); err != nil {
		t.Fatalf("failed to parse JSON status: %v, raw output: %s", err, buf.String())
	}

	if status.ServiceName == "" {
		t.Error("expected non-empty ServiceName")
	}
}

func TestOtelSpansCmd(t *testing.T) {
	// Generate a test span in global provider
	tracer := otel.GetTracer("cli-test")
	_, span := tracer.Start(context.Background(), "cli.test.span",
		otel.WithAttributes(otel.StringAttr("key", "value")),
	)
	span.SetStatus(otel.StatusOK, "test ok")
	span.End()

	time.Sleep(20 * time.Millisecond)

	cmd := newRootCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"otel", "spans"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("failed to execute otel spans: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Recent OpenTelemetry Spans") {
		t.Errorf("expected spans header in output, got: %s", out)
	}
	if !strings.Contains(out, "cli.test.span") {
		t.Errorf("expected span name in output, got: %s", out)
	}
}
