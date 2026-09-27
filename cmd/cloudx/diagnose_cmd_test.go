package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudx-org/cloudx/internal/diagnostics"
)

func TestDiagnoseCmd(t *testing.T) {
	cmd := newRootCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"diagnose"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("failed to execute diagnose: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "CloudX Cluster Diagnostics") {
		t.Errorf("expected diagnostics header in output, got: %s", out)
	}
	if !strings.Contains(out, "Overall Status:") {
		t.Errorf("expected Overall Status in output, got: %s", out)
	}
	if !strings.Contains(out, "Database Integrity") {
		t.Errorf("expected Database Integrity check in output, got: %s", out)
	}
	if !strings.Contains(out, "Scheduler Status") {
		t.Errorf("expected Scheduler Status check in output, got: %s", out)
	}
}

func TestDiagnoseCmdJSON(t *testing.T) {
	cmd := newRootCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"diagnose", "--json"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("failed to execute diagnose --json: %v", err)
	}

	var report diagnostics.DiagnosticReport
	if err := json.Unmarshal(buf.Bytes(), &report); err != nil {
		t.Fatalf("failed to parse JSON diagnostic report: %v, raw output: %s", err, buf.String())
	}

	if len(report.Checks) != 9 {
		t.Errorf("expected 9 diagnostic checks in JSON report, got %d", len(report.Checks))
	}
	if report.Overall == "" {
		t.Error("expected non-empty overall status")
	}
}
