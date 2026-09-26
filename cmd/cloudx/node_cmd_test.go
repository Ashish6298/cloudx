package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/state/models"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
)

func TestNodeCLICommands(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "cloudx.db")
	ctx := context.Background()
	now := time.Now().UTC()

	// 1. Initialize SQLite store with nodes, workers, and tasks
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}

	node1ID := id.NewNodeID()
	worker1ID := id.NewWorkerID()
	_ = store.Nodes().Create(ctx, &models.Node{
		ID:        node1ID,
		Name:      "worker-1",
		Address:   "192.168.1.101:7001",
		Status:    "READY",
		CreatedAt: now,
		UpdatedAt: now,
	})
	_ = store.Workers().Create(ctx, &models.Worker{
		ID:        worker1ID,
		NodeID:    node1ID,
		Address:   "192.168.1.101:7001",
		Status:    "READY",
		Heartbeat: now,
		CreatedAt: now,
		UpdatedAt: now,
	})

	node2ID := id.NewNodeID()
	worker2ID := id.NewWorkerID()
	_ = store.Nodes().Create(ctx, &models.Node{
		ID:        node2ID,
		Name:      "worker-2",
		Address:   "192.168.1.102:7001",
		Status:    "READY",
		CreatedAt: now,
		UpdatedAt: now,
	})
	_ = store.Workers().Create(ctx, &models.Worker{
		ID:        worker2ID,
		NodeID:    node2ID,
		Address:   "192.168.1.102:7001",
		Status:    "READY",
		Heartbeat: now,
		CreatedAt: now,
		UpdatedAt: now,
	})

	// Add an active task to worker-2
	task1ID := id.NewTaskID()
	serviceID := id.NewServiceID()
	_ = store.Tasks().Create(ctx, &models.Task{
		ID:        task1ID,
		ServiceID: serviceID,
		WorkerID:  worker2ID,
		State:     "RUNNING",
		CreatedAt: now,
		UpdatedAt: now,
	})
	_ = store.Close()

	// 2. Test `cloudx node list`
	listCmd := newRootCmd()
	listBuf := new(bytes.Buffer)
	listCmd.SetOut(listBuf)
	listCmd.SetErr(listBuf)
	listCmd.SetArgs([]string{
		"--storage-path", tempDir,
		"node", "list",
	})
	if err := listCmd.Execute(); err != nil {
		t.Fatalf("node list failed: %v", err)
	}

	outStr := listBuf.String()
	if !strings.Contains(outStr, "worker-1") || !strings.Contains(outStr, "worker-2") {
		t.Errorf("Expected node list output to contain worker-1 and worker-2, got:\n%s", outStr)
	}

	// 3. Test `cloudx node drain worker-2`
	drainCmd := newRootCmd()
	drainBuf := new(bytes.Buffer)
	drainCmd.SetOut(drainBuf)
	drainCmd.SetErr(drainBuf)
	drainCmd.SetArgs([]string{
		"--storage-path", tempDir,
		"node", "drain", "worker-2",
	})
	if err := drainCmd.Execute(); err != nil {
		t.Fatalf("node drain failed: %v", err)
	}

	drainOut := drainBuf.String()
	if !strings.Contains(drainOut, "DRAINING") {
		t.Errorf("Expected drain output to mention DRAINING, got:\n%s", drainOut)
	}

	// 4. Verify worker-2 status updated to DRAINING in database
	checkStore, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to re-open store: %v", err)
	}
	defer checkStore.Close()

	w2, err := checkStore.Workers().Get(ctx, worker2ID)
	if err != nil {
		t.Fatalf("failed to query worker-2: %v", err)
	}
	if w2.Status != "DRAINING" {
		t.Errorf("Expected worker-2 status to be DRAINING, got %s", w2.Status)
	}
}
