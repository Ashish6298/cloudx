package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootCmd(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	cmd.SetArgs([]string{"version"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("version command failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "CloudX") {
		t.Errorf("expected version output to contain 'CloudX', got %q", out)
	}
}

func TestRootCmdJSON(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	cmd.SetArgs([]string{"version", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("version --json command failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, `"version":`) {
		t.Errorf("expected JSON version output to contain '\"version\":', got %q", out)
	}
}

func TestConfigShowCmd(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	cmd.SetArgs([]string{"config", "show"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("config show command failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Node ID:") {
		t.Errorf("expected config show to display Node ID, got: %q", out)
	}
}

func TestConfigValidateCmd(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	cmd.SetArgs([]string{"config", "validate"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("config validate command failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Configuration is valid") {
		t.Errorf("expected success message, got: %q", out)
	}
}
