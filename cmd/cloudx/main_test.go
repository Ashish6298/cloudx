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

func TestClusterCommands(t *testing.T) {
	tempDir := t.TempDir()

	// 1. cluster init
	initCmd := newRootCmd()
	initBuf := new(bytes.Buffer)
	initCmd.SetOut(initBuf)
	initCmd.SetErr(initBuf)

	initCmd.SetArgs([]string{"--storage-path", tempDir, "cluster", "init"})
	if err := initCmd.Execute(); err != nil {
		t.Fatalf("cluster init failed: %v", err)
	}

	initOut := initBuf.String()
	if !strings.Contains(initOut, "initialized successfully") {
		t.Fatalf("expected initialization success, got: %s", initOut)
	}

	// 2. cluster status
	statusCmd := newRootCmd()
	statusBuf := new(bytes.Buffer)
	statusCmd.SetOut(statusBuf)
	statusCmd.SetErr(statusBuf)

	statusCmd.SetArgs([]string{"--storage-path", tempDir, "cluster", "status"})
	if err := statusCmd.Execute(); err != nil {
		t.Fatalf("cluster status failed: %v", err)
	}

	statusOut := statusBuf.String()
	if !strings.Contains(statusOut, "Registered Nodes:") && !strings.Contains(statusOut, "Cluster Status:") {
		t.Fatalf("expected cluster status output, got: %s", statusOut)
	}

	// 3. cluster nodes
	nodesCmd := newRootCmd()
	nodesBuf := new(bytes.Buffer)
	nodesCmd.SetOut(nodesBuf)
	nodesCmd.SetErr(nodesBuf)

	nodesCmd.SetArgs([]string{"--storage-path", tempDir, "cluster", "nodes"})
	if err := nodesCmd.Execute(); err != nil {
		t.Fatalf("cluster nodes failed: %v", err)
	}

	nodesOut := nodesBuf.String()
	if !strings.Contains(nodesOut, "NODE") || !strings.Contains(nodesOut, "STATUS") {
		t.Fatalf("expected table headers in cluster nodes, got: %s", nodesOut)
	}
}

func TestWorkerCommands(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	cmd.SetArgs([]string{"worker", "--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("worker help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "start") || !strings.Contains(out, "join") || !strings.Contains(out, "status") {
		t.Fatalf("expected start, join, status subcommands, got: %s", out)
	}
}
