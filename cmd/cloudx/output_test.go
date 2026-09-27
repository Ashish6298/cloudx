package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/state/models"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
)

func TestMachineReadableOutput_Version(t *testing.T) {
	// Test --output json
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"version", "--output", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("version --output json failed: %v", err)
	}

	var ver map[string]any
	if err := json.Unmarshal(buf.Bytes(), &ver); err != nil {
		t.Fatalf("failed to unmarshal JSON version output: %v, raw: %s", err, buf.String())
	}
	if ver["version"] == nil || ver["version"] == "" {
		t.Fatalf("expected version field in JSON output, got: %v", ver)
	}

	// Test -o json
	cmd2 := newRootCmd()
	buf2 := new(bytes.Buffer)
	cmd2.SetOut(buf2)
	cmd2.SetErr(buf2)
	cmd2.SetArgs([]string{"version", "-o", "json"})
	if err := cmd2.Execute(); err != nil {
		t.Fatalf("version -o json failed: %v", err)
	}
	var ver2 map[string]any
	if err := json.Unmarshal(buf2.Bytes(), &ver2); err != nil {
		t.Fatalf("failed to unmarshal JSON version output (-o json): %v, raw: %s", err, buf2.String())
	}
}

func TestMachineReadableOutput_StatusAndNodes(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cloudx-out-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ctx := context.Background()
	dbPath := filepath.Join(tempDir, "cloudx.db")
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite store: %v", err)
	}

	nodeID := id.NewNodeID()
	_ = store.Nodes().Create(ctx, &models.Node{
		ID:        nodeID,
		Name:      "test-worker-1",
		Status:    "READY",
		Address:   "127.0.0.1:7001",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	})

	workerID := id.NewWorkerID()
	_ = store.Workers().Create(ctx, &models.Worker{
		ID:        workerID,
		NodeID:    nodeID,
		Address:   "127.0.0.1:7001",
		Status:    "READY",
		Heartbeat: time.Now().UTC(),
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	})
	store.Close()

	// 1. Test `cloudx status --output json`
	statusCmd := newRootCmd()
	statusBuf := new(bytes.Buffer)
	statusCmd.SetOut(statusBuf)
	statusCmd.SetErr(statusBuf)
	statusCmd.SetArgs([]string{"--storage-path", tempDir, "status", "--output", "json"})
	if err := statusCmd.Execute(); err != nil {
		t.Fatalf("status --output json failed: %v", err)
	}
	var statusMap map[string]any
	if err := json.Unmarshal(statusBuf.Bytes(), &statusMap); err != nil {
		t.Fatalf("failed to parse status JSON: %v, raw: %s", err, statusBuf.String())
	}
	if statusMap["workers"] == nil {
		t.Fatalf("expected 'workers' in status JSON, got: %v", statusMap)
	}

	// 2. Test `cloudx cluster nodes -o json`
	nodesCmd := newRootCmd()
	nodesBuf := new(bytes.Buffer)
	nodesCmd.SetOut(nodesBuf)
	nodesCmd.SetErr(nodesBuf)
	nodesCmd.SetArgs([]string{"--storage-path", tempDir, "cluster", "nodes", "-o", "json"})
	if err := nodesCmd.Execute(); err != nil {
		t.Fatalf("cluster nodes -o json failed: %v", err)
	}
	var nodesList []map[string]any
	if err := json.Unmarshal(nodesBuf.Bytes(), &nodesList); err != nil {
		t.Fatalf("failed to parse nodes JSON: %v, raw: %s", err, nodesBuf.String())
	}
	if len(nodesList) != 1 || nodesList[0]["node"] != "test-worker-1" {
		t.Fatalf("expected 1 node with name test-worker-1, got: %v", nodesList)
	}

	// 3. Test `cloudx node list --output json`
	nodeListCmd := newRootCmd()
	nodeListBuf := new(bytes.Buffer)
	nodeListCmd.SetOut(nodeListBuf)
	nodeListCmd.SetErr(nodeListBuf)
	nodeListCmd.SetArgs([]string{"--storage-path", tempDir, "node", "list", "--output", "json"})
	if err := nodeListCmd.Execute(); err != nil {
		t.Fatalf("node list --output json failed: %v", err)
	}
	var nodeRows []map[string]any
	if err := json.Unmarshal(nodeListBuf.Bytes(), &nodeRows); err != nil {
		t.Fatalf("failed to parse node list JSON: %v, raw: %s", err, nodeListBuf.String())
	}
	if len(nodeRows) != 1 {
		t.Fatalf("expected 1 node row, got %d: %v", len(nodeRows), nodeRows)
	}
}

func TestMachineReadableOutput_WorkloadsAndEvents(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cloudx-out-workload-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// 1. Initialize cluster
	initCmd := newRootCmd()
	initBuf := new(bytes.Buffer)
	initCmd.SetOut(initBuf)
	initCmd.SetErr(initBuf)
	initCmd.SetArgs([]string{"--storage-path", tempDir, "init"})
	if err := initCmd.Execute(); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	// 2. Deploy a service
	manifestContent := `
version: "v1"
services:
  web-api:
    command: python3 -m http.server 8080
    replicas: 2
    runtime: native
`
	manifestPath := filepath.Join(tempDir, "web.yaml")
	if err := os.WriteFile(manifestPath, []byte(manifestContent), 0644); err != nil {
		t.Fatalf("failed to write manifest: %v", err)
	}

	deployCmd := newRootCmd()
	deployBuf := new(bytes.Buffer)
	deployCmd.SetOut(deployBuf)
	deployCmd.SetErr(deployBuf)
	deployCmd.SetArgs([]string{"--storage-path", tempDir, "deploy", "-f", manifestPath, "--output", "json"})
	if err := deployCmd.Execute(); err != nil {
		t.Fatalf("deploy with --output json failed: %v", err)
	}
	var deployResults []map[string]any
	if err := json.Unmarshal(deployBuf.Bytes(), &deployResults); err != nil {
		t.Fatalf("failed to parse deploy results JSON: %v, raw: %s", err, deployBuf.String())
	}
	if len(deployResults) != 1 || deployResults[0]["service_name"] != "web-api" {
		t.Fatalf("expected web-api deploy result, got: %v", deployResults)
	}

	// 3. Test `cloudx service list -o json`
	svcListCmd := newRootCmd()
	svcListBuf := new(bytes.Buffer)
	svcListCmd.SetOut(svcListBuf)
	svcListCmd.SetErr(svcListBuf)
	svcListCmd.SetArgs([]string{"--storage-path", tempDir, "service", "list", "-o", "json"})
	if err := svcListCmd.Execute(); err != nil {
		t.Fatalf("service list -o json failed: %v", err)
	}
	var svcs []map[string]any
	if err := json.Unmarshal(svcListBuf.Bytes(), &svcs); err != nil {
		t.Fatalf("failed to parse service list JSON: %v, raw: %s", err, svcListBuf.String())
	}
	if len(svcs) != 1 || svcs[0]["name"] != "web-api" {
		t.Fatalf("expected 1 service named web-api, got: %v", svcs)
	}

	// 4. Test `cloudx service inspect web-api --output json`
	svcInspectCmd := newRootCmd()
	svcInspectBuf := new(bytes.Buffer)
	svcInspectCmd.SetOut(svcInspectBuf)
	svcInspectCmd.SetErr(svcInspectBuf)
	svcInspectCmd.SetArgs([]string{"--storage-path", tempDir, "service", "inspect", "web-api", "--output", "json"})
	if err := svcInspectCmd.Execute(); err != nil {
		t.Fatalf("service inspect --output json failed: %v", err)
	}
	var inspectData map[string]any
	if err := json.Unmarshal(svcInspectBuf.Bytes(), &inspectData); err != nil {
		t.Fatalf("failed to parse service inspect JSON: %v, raw: %s", err, svcInspectBuf.String())
	}
	if inspectData["service"] == nil {
		t.Fatalf("expected 'service' key in inspect JSON, got: %v", inspectData)
	}

	// 5. Test `cloudx events --output json`
	eventsCmd := newRootCmd()
	eventsBuf := new(bytes.Buffer)
	eventsCmd.SetOut(eventsBuf)
	eventsCmd.SetErr(eventsBuf)
	eventsCmd.SetArgs([]string{"--storage-path", tempDir, "events", "--output", "json"})
	if err := eventsCmd.Execute(); err != nil {
		t.Fatalf("events --output json failed: %v", err)
	}
	var eventsList []map[string]any
	if err := json.Unmarshal(eventsBuf.Bytes(), &eventsList); err != nil {
		t.Fatalf("failed to parse events JSON: %v, raw: %s", err, eventsBuf.String())
	}
	if len(eventsList) == 0 {
		t.Fatalf("expected at least 1 event from deployment, got 0")
	}

	// 6. Test `cloudx diagnose --output json`
	diagCmd := newRootCmd()
	diagBuf := new(bytes.Buffer)
	diagCmd.SetOut(diagBuf)
	diagCmd.SetErr(diagBuf)
	diagCmd.SetArgs([]string{"--storage-path", tempDir, "diagnose", "--output", "json"})
	if err := diagCmd.Execute(); err != nil {
		t.Fatalf("diagnose --output json failed: %v", err)
	}
	var diagReport map[string]any
	if err := json.Unmarshal(diagBuf.Bytes(), &diagReport); err != nil {
		t.Fatalf("failed to parse diagnose JSON: %v, raw: %s", err, diagBuf.String())
	}
	if diagReport["overall_status"] == nil || diagReport["checks"] == nil {
		t.Fatalf("expected overall_status and checks in diagnose report, got: %v", diagReport)
	}
}
