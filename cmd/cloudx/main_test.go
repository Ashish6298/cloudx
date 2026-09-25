package main

import (
	"bytes"
	"os"
	"path/filepath"
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

func TestDeployAndServiceCommands(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Initialize cluster
	initCmd := newRootCmd()
	initCmd.SetArgs([]string{"--storage-path", tempDir, "cluster", "init"})
	if err := initCmd.Execute(); err != nil {
		t.Fatalf("cluster init failed: %v", err)
	}

	// 2. Create service manifest file
	manifestContent := `
version: "v1"
services:
  web-api:
    command: python3 -m http.server 8080
    replicas: 0
    runtime: native
    resources:
      cpu: 1
      memory: 256MB
`
	manifestPath := filepath.Join(tempDir, "service.yaml")
	if err := os.WriteFile(manifestPath, []byte(manifestContent), 0644); err != nil {
		t.Fatalf("failed to write manifest: %v", err)
	}

	// 3. cloudx deploy -f service.yaml
	deployCmd := newRootCmd()
	deployBuf := new(bytes.Buffer)
	deployCmd.SetOut(deployBuf)
	deployCmd.SetErr(deployBuf)
	deployCmd.SetArgs([]string{"--storage-path", tempDir, "deploy", "-f", manifestPath})

	if err := deployCmd.Execute(); err != nil {
		t.Fatalf("deploy command failed: %v", err)
	}

	deployOut := deployBuf.String()
	if !strings.Contains(deployOut, "web-api") || !strings.Contains(deployOut, "[SUCCESS]") {
		t.Fatalf("expected deploy output to confirm success, got: %s", deployOut)
	}

	// 4. cloudx service list
	listCmd := newRootCmd()
	listBuf := new(bytes.Buffer)
	listCmd.SetOut(listBuf)
	listCmd.SetErr(listBuf)
	listCmd.SetArgs([]string{"--storage-path", tempDir, "service", "list"})

	if err := listCmd.Execute(); err != nil {
		t.Fatalf("service list failed: %v", err)
	}

	listOut := listBuf.String()
	if !strings.Contains(listOut, "web-api") || !strings.Contains(listOut, "SERVICE ID") {
		t.Fatalf("expected service list table, got: %s", listOut)
	}

	// 5. cloudx service inspect web-api
	inspectCmd := newRootCmd()
	inspectBuf := new(bytes.Buffer)
	inspectCmd.SetOut(inspectBuf)
	inspectCmd.SetErr(inspectBuf)
	inspectCmd.SetArgs([]string{"--storage-path", tempDir, "service", "inspect", "web-api"})

	if err := inspectCmd.Execute(); err != nil {
		t.Fatalf("service inspect failed: %v", err)
	}

	inspectOut := inspectBuf.String()
	if !strings.Contains(inspectOut, "SERVICE: web-api") || !strings.Contains(inspectOut, "DEPLOYMENTS:") {
		t.Fatalf("expected detailed service inspection, got: %s", inspectOut)
	}

	// 6. cloudx service scale web-api 5
	scaleCmd := newRootCmd()
	scaleBuf := new(bytes.Buffer)
	scaleCmd.SetOut(scaleBuf)
	scaleCmd.SetErr(scaleBuf)
	scaleCmd.SetArgs([]string{"--storage-path", tempDir, "service", "scale", "web-api", "5"})

	if err := scaleCmd.Execute(); err != nil {
		t.Fatalf("service scale failed: %v", err)
	}

	scaleOut := scaleBuf.String()
	if !strings.Contains(scaleOut, "scaled successfully") || !strings.Contains(scaleOut, "Desired Replicas:  5") {
		t.Fatalf("expected scale output confirmation, got: %s", scaleOut)
	}
}

func TestFailSimulationCLI(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Initialize cluster
	initCmd := newRootCmd()
	initBuf := new(bytes.Buffer)
	initCmd.SetOut(initBuf)
	initCmd.SetErr(initBuf)
	initCmd.SetArgs([]string{"--storage-path", tempDir, "cluster", "init"})
	if err := initCmd.Execute(); err != nil {
		t.Fatalf("cluster init failed: %v", err)
	}

	// 2. Test fail delay-heartbeat
	delayCmd := newRootCmd()
	delayBuf := new(bytes.Buffer)
	delayCmd.SetOut(delayBuf)
	delayCmd.SetErr(delayBuf)
	delayCmd.SetArgs([]string{"--storage-path", tempDir, "fail", "delay-heartbeat", "wrk-123", "--delay", "30s"})
	// wrk-123 doesn't exist -> expected error
	_ = delayCmd.Execute()

	// 3. Test fail break-health
	breakCmd := newRootCmd()
	breakBuf := new(bytes.Buffer)
	breakCmd.SetOut(breakBuf)
	breakCmd.SetErr(breakBuf)
	breakCmd.SetArgs([]string{"--storage-path", tempDir, "fail", "break-health", "tsk-123", "--reason", "outage"})
	if err := breakCmd.Execute(); err != nil {
		t.Fatalf("fail break-health failed: %v", err)
	}
	if !strings.Contains(breakBuf.String(), "SIMULATION SUCCESS") {
		t.Fatalf("expected simulation success output, got: %s", breakBuf.String())
	}

	// 4. Test fail exhaust-resources
	resCmd := newRootCmd()
	resBuf := new(bytes.Buffer)
	resCmd.SetOut(resBuf)
	resCmd.SetErr(resBuf)
	resCmd.SetArgs([]string{"--storage-path", tempDir, "fail", "exhaust-resources", "tsk-123", "-m", "16"})
	if err := resCmd.Execute(); err != nil {
		t.Fatalf("fail exhaust-resources failed: %v", err)
	}
	if !strings.Contains(resBuf.String(), "SIMULATION SUCCESS") {
		t.Fatalf("expected resource exhaustion output, got: %s", resBuf.String())
	}
}

