package logging

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestLogLevels(t *testing.T) {
	buf := new(bytes.Buffer)
	logger := New(LevelWarn, FormatText)
	logger.SetOutput(buf)

	logger.Debug("debug message")
	logger.Info("info message")
	if buf.Len() > 0 {
		t.Errorf("expected no output for debug/info when level is WARN, got: %q", buf.String())
	}

	logger.Warn("warning message")
	if !strings.Contains(buf.String(), "[WARN ] warning message") {
		t.Errorf("expected warning in output, got: %q", buf.String())
	}

	logger.Error("error message")
	if !strings.Contains(buf.String(), "[ERROR] error message") {
		t.Errorf("expected error in output, got: %q", buf.String())
	}
}

func TestStructuredFieldsText(t *testing.T) {
	buf := new(bytes.Buffer)
	logger := New(LevelInfo, FormatText)
	logger.SetOutput(buf)

	subLogger := logger.
		WithNode("node-alpha").
		WithService("svc-web").
		WithTask("task-99").
		WithWorker("worker-1").
		WithDeployment("dep-v1").
		WithJob("job-migration").
		WithEvent("evt-001")

	subLogger.Info("reconciliation triggered")

	out := buf.String()
	expectedSubstrings := []string{
		"node_id=node-alpha",
		"service_id=svc-web",
		"task_id=task-99",
		"worker_id=worker-1",
		"deployment_id=dep-v1",
		"job_id=job-migration",
		"event_id=evt-001",
		"reconciliation triggered",
	}

	for _, sub := range expectedSubstrings {
		if !strings.Contains(out, sub) {
			t.Errorf("expected text log to contain %q, got: %q", sub, out)
		}
	}
}

func TestStructuredFieldsJSON(t *testing.T) {
	buf := new(bytes.Buffer)
	logger := New(LevelInfo, FormatJSON)
	logger.SetOutput(buf)

	logger.
		WithService("api").
		WithTask("api-1").
		With("custom_metric", 42).
		Info("task started successfully")

	var entry LogEntry
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("failed to parse JSON log entry: %v", err)
	}

	if entry.Level != "INFO" {
		t.Errorf("expected level 'INFO', got %q", entry.Level)
	}
	if entry.Message != "task started successfully" {
		t.Errorf("expected message 'task started successfully', got %q", entry.Message)
	}
	if entry.Fields["service_id"] != "api" {
		t.Errorf("expected field service_id='api', got %v", entry.Fields["service_id"])
	}
	if entry.Fields["task_id"] != "api-1" {
		t.Errorf("expected field task_id='api-1', got %v", entry.Fields["task_id"])
	}
}

func TestParseLevel(t *testing.T) {
	if ParseLevel("DEBUG") != LevelDebug {
		t.Errorf("expected LevelDebug")
	}
	if ParseLevel("info") != LevelInfo {
		t.Errorf("expected LevelInfo")
	}
	if ParseLevel("warn") != LevelWarn {
		t.Errorf("expected LevelWarn")
	}
	if ParseLevel("error") != LevelError {
		t.Errorf("expected LevelError")
	}
	if ParseLevel("invalid") != LevelInfo {
		t.Errorf("expected fallback to LevelInfo")
	}
}
