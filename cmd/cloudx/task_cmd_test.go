package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/state/models"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
)

func TestTaskExplainCmd(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "cloudx.db")
	ctx := context.Background()

	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite store: %v", err)
	}
	defer store.Close()

	// 1. Setup Node and Worker
	nodeID := id.NewNodeID()
	workerID := id.NewWorkerID()
	now := time.Now().UTC()

	err = store.Nodes().Create(ctx, &models.Node{
		ID:        nodeID,
		Name:      "worker-2",
		Address:   "10.0.0.2:9090",
		Status:    "READY",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to create node: %v", err)
	}

	err = store.Workers().Create(ctx, &models.Worker{
		ID:        workerID,
		NodeID:    nodeID,
		Address:   "10.0.0.2:9090",
		Status:    "READY",
		Heartbeat: now,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to create worker: %v", err)
	}

	// 2. Setup Service, Deployment, and Task
	serviceID := id.NewServiceID()
	deploymentID := id.NewDeploymentID()
	taskID := id.NewTaskID()

	err = store.Services().Create(ctx, &models.Service{
		ID:        serviceID,
		Name:      "api-service",
		Status:    "ACTIVE",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}

	depConfig := models.DeploymentConfig{
		Command:  "node index.js",
		Runtime:  "native",
		Replicas: 1,
		Resources: models.ResourceRequirements{
			CPU:    0.5,
			Memory: 512 * 1024 * 1024,
		},
		Volumes: []models.VolumeMount{
			{VolumeName: "app-data", MountPath: "/data"},
		},
	}
	immDep := &models.ImmutableDeployment{
		ID:          deploymentID,
		ServiceID:   serviceID,
		ServiceName: "api-service",
		Version:     "v1",
		ConfigHash:  depConfig.ComputeHash(),
		Status:      models.DeploymentStatusActive,
		Config:      depConfig,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	depSpecJSON, _ := immDep.ToSpecJSON()

	err = store.Deployments().Create(ctx, &models.Deployment{
		ID:        deploymentID,
		ServiceID: serviceID,
		Version:   "v1",
		Status:    "ACTIVE",
		SpecJSON:  depSpecJSON,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("failed to create deployment: %v", err)
	}

	err = store.Tasks().Create(ctx, &models.Task{
		ID:           taskID,
		ServiceID:    serviceID,
		DeploymentID: deploymentID,
		WorkerID:     workerID,
		State:        "RUNNING",
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	// Append TASK_ASSIGNED event
	_ = store.Events().Append(ctx, &models.Event{
		ID:        id.NewEventID(),
		Type:      "TASK_ASSIGNED",
		Source:    "assignment_coordinator",
		EntityID:  taskID,
		Payload:   fmt.Sprintf(`{"worker_id":"%s","service_id":"%s","deployment_id":"%s","score":95.50}`, workerID, serviceID, deploymentID),
		CreatedAt: now,
	})

	// Close store to release file lock for CLI commands
	_ = store.Close()

	// 3. Test Human-Readable output
	rootCmd := newRootCmd()
	var outBuf bytes.Buffer
	rootCmd.SetOut(&outBuf)
	rootCmd.SetErr(&outBuf)
	rootCmd.SetArgs([]string{"--storage-path", tempDir, "task", "explain", taskID.String()})

	err = rootCmd.Execute()
	if err != nil {
		t.Fatalf("rootCmd.Execute failed: %v", err)
	}

	outStr := outBuf.String()
	t.Logf("CLI Output:\n%s", outStr)

	if !strings.Contains(outStr, "Selected worker: worker-2") {
		t.Errorf("expected output to contain 'Selected worker: worker-2', got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "CPU available: sufficient") {
		t.Errorf("expected output to contain 'CPU available: sufficient', got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "Memory available: sufficient") {
		t.Errorf("expected output to contain 'Memory available: sufficient', got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "Worker healthy") {
		t.Errorf("expected output to contain 'Worker healthy', got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "Runtime supported") {
		t.Errorf("expected output to contain 'Runtime supported', got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "Volume available") {
		t.Errorf("expected output to contain 'Volume available', got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "Highest scheduler score") {
		t.Errorf("expected output to contain 'Highest scheduler score', got:\n%s", outStr)
	}

	// 4. Test JSON output
	var jsonBuf bytes.Buffer
	rootCmdJSON := newRootCmd()
	rootCmdJSON.SetOut(&jsonBuf)
	rootCmdJSON.SetErr(&jsonBuf)
	rootCmdJSON.SetArgs([]string{"--storage-path", tempDir, "task", "explain", taskID.String(), "--json"})

	err = rootCmdJSON.Execute()
	if err != nil {
		t.Fatalf("rootCmdJSON.Execute failed: %v", err)
	}

	var explanation TaskExplanation
	err = json.Unmarshal(jsonBuf.Bytes(), &explanation)
	if err != nil {
		t.Fatalf("failed to unmarshal json explanation: %v\nOutput: %s", err, jsonBuf.String())
	}

	if explanation.SelectedWorker != "worker-2" {
		t.Errorf("expected SelectedWorker 'worker-2', got '%s'", explanation.SelectedWorker)
	}
	if explanation.Score != 95.50 {
		t.Errorf("expected Score 95.50, got %.2f", explanation.Score)
	}
	if len(explanation.Reasons) != 6 {
		t.Errorf("expected 6 reasons, got %d: %v", len(explanation.Reasons), explanation.Reasons)
	}
}

func TestTaskExplainJob(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "cloudx.db")
	ctx := context.Background()

	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite store: %v", err)
	}

	nodeID := id.NewNodeID()
	workerID := id.NewWorkerID()
	jobID := id.NewJobID()
	taskID := id.NewTaskID()
	now := time.Now().UTC()

	_ = store.Nodes().Create(ctx, &models.Node{
		ID:        nodeID,
		Name:      "worker-1",
		Address:   "10.0.0.1:9090",
		Status:    "READY",
		CreatedAt: now,
		UpdatedAt: now,
	})

	_ = store.Workers().Create(ctx, &models.Worker{
		ID:        workerID,
		NodeID:    nodeID,
		Address:   "10.0.0.1:9090",
		Status:    "READY",
		Heartbeat: now,
		CreatedAt: now,
		UpdatedAt: now,
	})

	jobRec := &models.JobRecord{
		ID:    jobID,
		Name:  "db-migrate",
		State: models.JobStateRunning,
		Config: models.JobConfig{
			Command: "migrate up",
			Runtime: "native",
			Resources: models.ResourceRequirements{
				CPU:    1.0,
				Memory: 1024 * 1024 * 1024,
			},
		},
		TaskID:    taskID,
		CreatedAt: now,
		UpdatedAt: now,
	}
	specJSON, _ := jobRec.ToSpecJSON()

	_ = store.Jobs().Create(ctx, &models.Job{
		ID:        jobID,
		Name:      "db-migrate",
		Command:   "migrate up",
		Status:    "RUNNING",
		SpecJSON:  specJSON,
		CreatedAt: now,
		UpdatedAt: now,
	})

	_ = store.Tasks().Create(ctx, &models.Task{
		ID:        taskID,
		JobID:     jobID,
		WorkerID:  workerID,
		State:     "RUNNING",
		CreatedAt: now,
		UpdatedAt: now,
	})

	_ = store.Events().Append(ctx, &models.Event{
		ID:        id.NewEventID(),
		Type:      "TASK_ASSIGNED",
		Source:    "assignment_coordinator",
		EntityID:  taskID,
		Payload:   fmt.Sprintf(`{"worker_id":"%s","job_id":"%s","score":88.20}`, workerID, jobID),
		CreatedAt: now,
	})

	_ = store.Close()

	rootCmd := newRootCmd()
	var outBuf bytes.Buffer
	rootCmd.SetOut(&outBuf)
	rootCmd.SetErr(&outBuf)
	rootCmd.SetArgs([]string{"--storage-path", tempDir, "task", "explain", taskID.String()})

	err = rootCmd.Execute()
	if err != nil {
		t.Fatalf("rootCmd.Execute failed for job task: %v", err)
	}

	outStr := outBuf.String()
	if !strings.Contains(outStr, "Selected worker: worker-1") {
		t.Errorf("expected output to contain 'Selected worker: worker-1', got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "CPU available: sufficient") {
		t.Errorf("expected output to contain 'CPU available: sufficient', got:\n%s", outStr)
	}
}

func TestTaskExplainNotFound(t *testing.T) {
	tempDir := t.TempDir()
	rootCmd := newRootCmd()
	var outBuf bytes.Buffer
	rootCmd.SetOut(&outBuf)
	rootCmd.SetErr(&outBuf)
	rootCmd.SetArgs([]string{"--storage-path", tempDir, "task", "explain", "non-existent-task"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatalf("expected error for non-existent task, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %v", err)
	}
}
