package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestWorkerCmd(t *testing.T) {
	cmd := newWorkerCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	cmd.SetArgs([]string{"version"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("worker version command failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "cloudx-worker") {
		t.Errorf("expected worker version output to contain 'cloudx-worker', got %q", out)
	}
}
