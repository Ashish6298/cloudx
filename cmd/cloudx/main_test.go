package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/state/models"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
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

	// 2. cluster status / cloudx status
	statusCmd := newRootCmd()
	statusBuf := new(bytes.Buffer)
	statusCmd.SetOut(statusBuf)
	statusCmd.SetErr(statusBuf)

	statusCmd.SetArgs([]string{"--storage-path", tempDir, "status"})
	if err := statusCmd.Execute(); err != nil {
		t.Fatalf("cluster status failed: %v", err)
	}

	statusOut := statusBuf.String()
	if !strings.Contains(statusOut, "CLOUDX CLUSTER") || !strings.Contains(statusOut, "Control Plane:") || !strings.Contains(statusOut, "HEALTH") {
		t.Fatalf("expected cluster status overview, got: %s", statusOut)
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

	// 6. cloudx deployment list
	depListCmd := newRootCmd()
	depListBuf := new(bytes.Buffer)
	depListCmd.SetOut(depListBuf)
	depListCmd.SetErr(depListBuf)
	depListCmd.SetArgs([]string{"--storage-path", tempDir, "deployment", "list"})

	if err := depListCmd.Execute(); err != nil {
		t.Fatalf("deployment list failed: %v", err)
	}

	depListOut := depListBuf.String()
	if !strings.Contains(depListOut, "web-api") || !strings.Contains(depListOut, "DEPLOYMENT ID") {
		t.Fatalf("expected deployment list table, got: %s", depListOut)
	}

	// 7. cloudx service scale web-api 5
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

	// 8. cloudx deploy web-api:v2 (Versioned Deployment CLI test)
	deployV2Cmd := newRootCmd()
	deployV2Buf := new(bytes.Buffer)
	deployV2Cmd.SetOut(deployV2Buf)
	deployV2Cmd.SetErr(deployV2Buf)
	deployV2Cmd.SetArgs([]string{"--storage-path", tempDir, "deploy", "web-api:v2"})

	if err := deployV2Cmd.Execute(); err != nil {
		t.Fatalf("deploy version command failed: %v", err)
	}

	deployV2Out := deployV2Buf.String()
	if !strings.Contains(deployV2Out, "web-api") || !strings.Contains(deployV2Out, "Version:    v2") || !strings.Contains(deployV2Out, "[SUCCESS]") {
		t.Fatalf("expected version deployment success output, got: %s", deployV2Out)
	}

	// 9. Inspect deployments to verify both v1 and v2 exist simultaneously in state
	depListV2Cmd := newRootCmd()
	depListV2Buf := new(bytes.Buffer)
	depListV2Cmd.SetOut(depListV2Buf)
	depListV2Cmd.SetErr(depListV2Buf)
	depListV2Cmd.SetArgs([]string{"--storage-path", tempDir, "deployment", "list"})

	if err := depListV2Cmd.Execute(); err != nil {
		t.Fatalf("deployment list v2 failed: %v", err)
	}

	depListV2Out := depListV2Buf.String()
	if !strings.Contains(depListV2Out, "v1") || !strings.Contains(depListV2Out, "v2") {
		t.Fatalf("expected both v1 and v2 deployments in listing, got: %s", depListV2Out)
	}

	// 10. cloudx rollback web-api (Rollback v2 -> v1)
	rollbackCmd := newRootCmd()
	rollbackBuf := new(bytes.Buffer)
	rollbackCmd.SetOut(rollbackBuf)
	rollbackCmd.SetErr(rollbackBuf)
	rollbackCmd.SetArgs([]string{"--storage-path", tempDir, "rollback", "web-api"})

	if err := rollbackCmd.Execute(); err != nil {
		t.Fatalf("rollback command failed: %v", err)
	}

	rollbackOut := rollbackBuf.String()
	if !strings.Contains(rollbackOut, "rolled back successfully") || !strings.Contains(rollbackOut, "To Version:      v1") {
		t.Fatalf("expected rollback success output to v1, got: %s", rollbackOut)
	}

	// 11. cloudx events (Event CLI test)
	eventsCmd := newRootCmd()
	eventsBuf := new(bytes.Buffer)
	eventsCmd.SetOut(eventsBuf)
	eventsCmd.SetErr(eventsBuf)
	eventsCmd.SetArgs([]string{"--storage-path", tempDir, "events"})

	if err := eventsCmd.Execute(); err != nil {
		t.Fatalf("events command failed: %v", err)
	}

	eventsOut := eventsBuf.String()
	if !strings.Contains(eventsOut, "TIMESTAMP") || !strings.Contains(eventsOut, "SERVICE_CREATED") {
		t.Fatalf("expected chronological events listing, got: %s", eventsOut)
	}

	// 12. cloudx events --service web-api
	eventsSvcCmd := newRootCmd()
	eventsSvcBuf := new(bytes.Buffer)
	eventsSvcCmd.SetOut(eventsSvcBuf)
	eventsSvcCmd.SetErr(eventsSvcBuf)
	eventsSvcCmd.SetArgs([]string{"--storage-path", tempDir, "events", "--service", "web-api"})

	if err := eventsSvcCmd.Execute(); err != nil {
		t.Fatalf("events --service command failed: %v", err)
	}

	eventsSvcOut := eventsSvcBuf.String()
	if !strings.Contains(eventsSvcOut, "SERVICE_CREATED") && !strings.Contains(eventsSvcOut, "SERVICE_ROLLED_BACK") {
		t.Fatalf("expected service events, got: %s", eventsSvcOut)
	}

	// 13. cloudx events --since 1h --json
	eventsSinceCmd := newRootCmd()
	eventsSinceBuf := new(bytes.Buffer)
	eventsSinceCmd.SetOut(eventsSinceBuf)
	eventsSinceCmd.SetErr(eventsSinceBuf)
	eventsSinceCmd.SetArgs([]string{"--storage-path", tempDir, "events", "--since", "1h", "--json"})

	if err := eventsSinceCmd.Execute(); err != nil {
		t.Fatalf("events --since --json command failed: %v", err)
	}

	eventsSinceOut := eventsSinceBuf.String()
	if !strings.Contains(eventsSinceOut, `"type":`) {
		t.Fatalf("expected JSON events output, got: %s", eventsSinceOut)
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

func TestServiceLogsCLI(t *testing.T) {
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

	// 2. Open store and create active worker + service + task
	ctx := context.Background()
	dbPath := filepath.Join(tempDir, "cloudx.db")
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}

	workerID := id.NewWorkerID()
	now := time.Now().UTC()
	_ = store.Workers().Create(ctx, &models.Worker{
		ID:        workerID,
		NodeID:    "local-node",
		Address:   "127.0.0.1:7001",
		Status:    "READY",
		Heartbeat: now,
		CreatedAt: now,
		UpdatedAt: now,
	})

	svcID := id.NewServiceID()
	depID := id.NewDeploymentID()
	_ = store.Services().Create(ctx, &models.Service{
		ID:        svcID,
		Name:      "log-api",
		Replicas:  1,
		Runtime:   "native",
		Command:   "echo hello-world",
		Status:    "RUNNING",
		CreatedAt: now,
		UpdatedAt: now,
	})

	taskID := id.NewTaskID()
	_ = store.Tasks().Create(ctx, &models.Task{
		ID:           taskID,
		ServiceID:    svcID,
		DeploymentID: depID,
		WorkerID:     workerID,
		State:        "RUNNING",
		PID:          1234,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	_ = store.Close()

	// 3. Write mock log line to disk log file for the created task
	logDir := filepath.Join(tempDir, "logs")
	_ = os.MkdirAll(logDir, 0755)

	taskLogFile := filepath.Join(logDir, fmt.Sprintf("%s.log", taskID))
	logContent := fmt.Sprintf("[%s] [stdout] [%s] [%s] Server booted successfully\n[%s] [stdout] [%s] [%s] Ready to receive traffic\n",
		time.Now().UTC().Format(time.RFC3339Nano),
		workerID,
		depID,
		time.Now().UTC().Format(time.RFC3339Nano),
		workerID,
		depID,
	)
	if err := os.WriteFile(taskLogFile, []byte(logContent), 0644); err != nil {
		t.Fatalf("failed to write task log file: %v", err)
	}

	// 4. Test cloudx service logs log-api
	logsCmd := newRootCmd()
	logsBuf := new(bytes.Buffer)
	logsCmd.SetOut(logsBuf)
	logsCmd.SetErr(logsBuf)
	logsCmd.SetArgs([]string{"--storage-path", tempDir, "service", "logs", "log-api"})
	if err := logsCmd.Execute(); err != nil {
		t.Fatalf("service logs failed: %v", err)
	}

	logsOut := logsBuf.String()
	if !strings.Contains(logsOut, "Server booted successfully") || !strings.Contains(logsOut, "Ready to receive traffic") {
		t.Fatalf("expected log lines in output, got: %s", logsOut)
	}

	// 5. Test cloudx service logs log-api --json
	jsonLogsCmd := newRootCmd()
	jsonLogsBuf := new(bytes.Buffer)
	jsonLogsCmd.SetOut(jsonLogsBuf)
	jsonLogsCmd.SetErr(jsonLogsBuf)
	jsonLogsCmd.SetArgs([]string{"--storage-path", tempDir, "service", "logs", "log-api", "--json"})
	if err := jsonLogsCmd.Execute(); err != nil {
		t.Fatalf("service logs --json failed: %v", err)
	}
	if !strings.Contains(jsonLogsBuf.String(), `"message": "Server booted successfully"`) && !strings.Contains(jsonLogsBuf.String(), `"message":"Server booted successfully"`) {
		t.Fatalf("expected JSON log output, got: %s", jsonLogsBuf.String())
	}
}

func TestClusterStatusOverviewCLI(t *testing.T) {
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

	// 2. Open store and seed realistic topology: 2 workers, 2 services, 1 job, 3 tasks (2 healthy, 1 degraded)
	ctx := context.Background()
	dbPath := filepath.Join(tempDir, "cloudx.db")
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}

	now := time.Now().UTC()
	_ = store.Nodes().Create(ctx, &models.Node{
		ID:        "desktop-node",
		Name:      "desktop",
		Address:   "127.0.0.1:7002",
		Status:    "READY",
		CreatedAt: now,
		UpdatedAt: now,
	})

	w1 := id.NewWorkerID()
	w2 := id.NewWorkerID()
	_ = store.Workers().Create(ctx, &models.Worker{
		ID:        w1,
		NodeID:    "local-node",
		Address:   "127.0.0.1:7001",
		Status:    "READY",
		Heartbeat: now,
		CreatedAt: now,
		UpdatedAt: now,
	})
	_ = store.Workers().Create(ctx, &models.Worker{
		ID:        w2,
		NodeID:    "desktop-node",
		Address:   "127.0.0.1:7002",
		Status:    "READY",
		Heartbeat: now,
		CreatedAt: now,
		UpdatedAt: now,
	})

	s1 := id.NewServiceID()
	s2 := id.NewServiceID()
	_ = store.Services().Create(ctx, &models.Service{
		ID:        s1,
		Name:      "api-gateway",
		Replicas:  2,
		Runtime:   "native",
		Command:   "echo api",
		Status:    "RUNNING",
		CreatedAt: now,
		UpdatedAt: now,
	})
	_ = store.Services().Create(ctx, &models.Service{
		ID:        s2,
		Name:      "cache-service",
		Replicas:  1,
		Runtime:   "native",
		Command:   "echo cache",
		Status:    "RUNNING",
		CreatedAt: now,
		UpdatedAt: now,
	})

	j1 := id.NewJobID()
	_ = store.Jobs().Create(ctx, &models.Job{
		ID:        j1,
		Name:      "db-backup",
		Command:   "backup.sh",
		Status:    "COMPLETED",
		CreatedAt: now,
		UpdatedAt: now,
	})

	// Tasks
	_ = store.Tasks().Create(ctx, &models.Task{
		ID:        id.NewTaskID(),
		ServiceID: s1,
		WorkerID:  w1,
		State:     "RUNNING",
		CreatedAt: now,
		UpdatedAt: now,
	})
	_ = store.Tasks().Create(ctx, &models.Task{
		ID:        id.NewTaskID(),
		ServiceID: s1,
		WorkerID:  w2,
		State:     "HEALTHY",
		CreatedAt: now,
		UpdatedAt: now,
	})
	_ = store.Tasks().Create(ctx, &models.Task{
		ID:        id.NewTaskID(),
		ServiceID: s2,
		WorkerID:  w1,
		State:     "UNHEALTHY",
		CreatedAt: now,
		UpdatedAt: now,
	})
	_ = store.Close()

	// 3. Test `cloudx status` terminal output
	statusCmd := newRootCmd()
	statusBuf := new(bytes.Buffer)
	statusCmd.SetOut(statusBuf)
	statusCmd.SetErr(statusBuf)
	statusCmd.SetArgs([]string{"--storage-path", tempDir, "status"})

	if err := statusCmd.Execute(); err != nil {
		t.Fatalf("cloudx status command failed: %v", err)
	}

	outStr := statusBuf.String()
	if !strings.Contains(outStr, "CLOUDX CLUSTER") {
		t.Fatalf("expected 'CLOUDX CLUSTER' heading, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "Workers:       2") || !strings.Contains(outStr, "Services:      2") || !strings.Contains(outStr, "Jobs:          1") {
		t.Fatalf("expected cluster tallies, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "Healthy:       2") || !strings.Contains(outStr, "Degraded:      1") || !strings.Contains(outStr, "Failed:        0") {
		t.Fatalf("expected health breakdown, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "NODE") || !strings.Contains(outStr, "CPU") || !strings.Contains(outStr, "MEMORY") {
		t.Fatalf("expected node summary table, got:\n%s", outStr)
	}

	// 4. Test `cloudx status --json`
	jsonStatusCmd := newRootCmd()
	jsonStatusBuf := new(bytes.Buffer)
	jsonStatusCmd.SetOut(jsonStatusBuf)
	jsonStatusCmd.SetErr(jsonStatusBuf)
	jsonStatusCmd.SetArgs([]string{"--storage-path", tempDir, "status", "--json"})

	if err := jsonStatusCmd.Execute(); err != nil {
		t.Fatalf("cloudx status --json command failed: %v", err)
	}

	jsonStr := jsonStatusBuf.String()
	if !strings.Contains(jsonStr, `"workers": 2`) || !strings.Contains(jsonStr, `"services": 2`) {
		t.Fatalf("expected valid JSON cluster status, got:\n%s", jsonStr)
	}
}

func TestServiceEndpointsCmd(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "cloudx.db")
	ctx := context.Background()

	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite store: %v", err)
	}

	now := time.Now().UTC()
	nodeID := id.NewNodeID()
	_ = store.Nodes().Create(ctx, &models.Node{
		ID:        nodeID,
		Name:      "test-node",
		Address:   "10.0.0.5",
		Status:    "READY",
		CreatedAt: now,
		UpdatedAt: now,
	})

	workerID := id.NewWorkerID()
	_ = store.Workers().Create(ctx, &models.Worker{
		ID:        workerID,
		NodeID:    nodeID,
		Address:   "10.0.0.5:7001",
		Status:    "READY",
		Heartbeat: now,
		CreatedAt: now,
		UpdatedAt: now,
	})

	svcID := id.NewServiceID()
	specJSON := `{"name":"api","command":"server","ports":[{"host_port":8000,"service_port":8000,"protocol":"tcp"}]}`
	_ = store.Services().Create(ctx, &models.Service{
		ID:        svcID,
		Name:      "api",
		Replicas:  1,
		Runtime:   "native",
		Command:   "server",
		Status:    "RUNNING",
		SpecJSON:  specJSON,
		CreatedAt: now,
		UpdatedAt: now,
	})

	_ = store.Tasks().Create(ctx, &models.Task{
		ID:        id.NewTaskID(),
		ServiceID: svcID,
		WorkerID:  workerID,
		State:     string(models.TaskStateRunning),
		PID:       5050,
		CreatedAt: now,
		UpdatedAt: now,
	})
	_ = store.Close()

	// 1. Test `cloudx service endpoints api`
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"--storage-path", tempDir, "service", "endpoints", "api"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("cloudx service endpoints api command failed: %v", err)
	}

	outStr := buf.String()
	if !strings.Contains(outStr, "10.0.0.5:8000") || !strings.Contains(outStr, "api") {
		t.Fatalf("expected endpoint '10.0.0.5:8000' in output, got:\n%s", outStr)
	}

	// 2. Test `cloudx service endpoints --json`
	jsonCmd := newRootCmd()
	jsonBuf := new(bytes.Buffer)
	jsonCmd.SetOut(jsonBuf)
	jsonCmd.SetErr(jsonBuf)
	jsonCmd.SetArgs([]string{"--storage-path", tempDir, "service", "endpoints", "--json"})

	if err := jsonCmd.Execute(); err != nil {
		t.Fatalf("cloudx service endpoints --json command failed: %v", err)
	}

	jsonOut := jsonBuf.String()
	if !strings.Contains(jsonOut, "10.0.0.5:8000") {
		t.Fatalf("expected JSON output to contain endpoint address, got:\n%s", jsonOut)
	}
}

func TestCLI_ClusterInitAndToken(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Test `cloudx cluster init`
	initCmd := newRootCmd()
	initBuf := new(bytes.Buffer)
	initCmd.SetOut(initBuf)
	initCmd.SetErr(initBuf)
	initCmd.SetArgs([]string{"--storage-path", tempDir, "cluster", "init"})

	if err := initCmd.Execute(); err != nil {
		t.Fatalf("cluster init failed: %v", err)
	}

	initOut := initBuf.String()
	if !strings.Contains(initOut, "Bootstrap Token:") || !strings.Contains(initOut, "cloudx worker join") {
		t.Fatalf("expected bootstrap token instructions in cluster init output, got:\n%s", initOut)
	}

	// 2. Test `cloudx cluster token`
	tokenCmd := newRootCmd()
	tokenBuf := new(bytes.Buffer)
	tokenCmd.SetOut(tokenBuf)
	tokenCmd.SetErr(tokenBuf)
	tokenCmd.SetArgs([]string{"--storage-path", tempDir, "cluster", "token"})

	if err := tokenCmd.Execute(); err != nil {
		t.Fatalf("cluster token failed: %v", err)
	}

	tokenOut := strings.TrimSpace(tokenBuf.String())
	if !strings.HasPrefix(tokenOut, "clx-btk-") {
		t.Fatalf("expected bootstrap token starting with 'clx-btk-', got %q", tokenOut)
	}
}

func TestPhase61_CLIStructure(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Verify `cloudx init` (root alias)
	initCmd := newRootCmd()
	initBuf := new(bytes.Buffer)
	initCmd.SetOut(initBuf)
	initCmd.SetErr(initBuf)
	initCmd.SetArgs([]string{"--storage-path", tempDir, "init"})
	if err := initCmd.Execute(); err != nil {
		t.Fatalf("cloudx init root alias failed: %v", err)
	}
	if !strings.Contains(initBuf.String(), "initialized successfully") {
		t.Fatalf("expected initialization success from cloudx init, got: %s", initBuf.String())
	}

	// 2. Verify `cloudx status` (root alias)
	statusCmd := newRootCmd()
	statusBuf := new(bytes.Buffer)
	statusCmd.SetOut(statusBuf)
	statusCmd.SetErr(statusBuf)
	statusCmd.SetArgs([]string{"--storage-path", tempDir, "status"})
	if err := statusCmd.Execute(); err != nil {
		t.Fatalf("cloudx status root alias failed: %v", err)
	}
	if !strings.Contains(statusBuf.String(), "CLOUDX CLUSTER") {
		t.Fatalf("expected cluster status from cloudx status, got: %s", statusBuf.String())
	}

	// 3. Deploy a service and test `cloudx service restart`
	manifestContent := `
version: "v1"
services:
  cache-svc:
    command: python3 -m http.server 9090
    replicas: 1
    runtime: native
`
	manifestPath := filepath.Join(tempDir, "cache.yaml")
	if err := os.WriteFile(manifestPath, []byte(manifestContent), 0644); err != nil {
		t.Fatalf("failed to write manifest: %v", err)
	}

	deployCmd := newRootCmd()
	deployBuf := new(bytes.Buffer)
	deployCmd.SetOut(deployBuf)
	deployCmd.SetErr(deployBuf)
	deployCmd.SetArgs([]string{"--storage-path", tempDir, "deploy", "-f", manifestPath})
	if err := deployCmd.Execute(); err != nil {
		t.Fatalf("deploy failed: %v", err)
	}

	// Test `cloudx service restart cache-svc`
	restartCmd := newRootCmd()
	restartBuf := new(bytes.Buffer)
	restartCmd.SetOut(restartBuf)
	restartCmd.SetErr(restartBuf)
	restartCmd.SetArgs([]string{"--storage-path", tempDir, "service", "restart", "cache-svc"})
	if err := restartCmd.Execute(); err != nil {
		t.Fatalf("service restart failed: %v", err)
	}
	if !strings.Contains(restartBuf.String(), "restarted successfully") {
		t.Fatalf("expected restart success output, got: %s", restartBuf.String())
	}

	// Test `cloudx service restart cache-svc --json`
	restartJSONCmd := newRootCmd()
	restartJSONBuf := new(bytes.Buffer)
	restartJSONCmd.SetOut(restartJSONBuf)
	restartJSONCmd.SetErr(restartJSONBuf)
	restartJSONCmd.SetArgs([]string{"--storage-path", tempDir, "service", "restart", "cache-svc", "--json"})
	if err := restartJSONCmd.Execute(); err != nil {
		t.Fatalf("service restart --json failed: %v", err)
	}
	if !strings.Contains(restartJSONBuf.String(), `"service_name": "cache-svc"`) {
		t.Fatalf("expected json restart result, got: %s", restartJSONBuf.String())
	}
}
