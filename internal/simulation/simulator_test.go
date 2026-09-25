package simulation

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/health"
	run "github.com/cloudx-org/cloudx/internal/runtime"
	"github.com/cloudx-org/cloudx/internal/state/models"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
	"github.com/cloudx-org/cloudx/internal/worker"
)

func TestSimulator_KillProcess(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to open sqlite store: %v", err)
	}
	defer store.Close()

	logger := logging.NewDefaultLogger()
	workerID := id.NewWorkerID()
	tm := worker.NewTaskManager(worker.TaskManagerOptions{
		WorkerID: workerID,
		Runtime:  run.NewNativeRuntime(),
		Logger:   logger,
	})
	defer tm.Close()

	simulator := NewSimulator(store, tm, nil, logger)

	var cmd string
	var args []string
	if runtime.GOOS == "windows" {
		cmd = "powershell"
		args = []string{"-NoProfile", "-Command", "Start-Sleep -Seconds 10"}
	} else {
		cmd = "sh"
		args = []string{"-c", "sleep 10"}
	}

	taskID := id.NewTaskID()
	err = tm.AssignTask(ctx, worker.TaskAssignment{
		TaskID:  taskID,
		Command: cmd,
		Args:    args,
	})
	if err != nil {
		t.Fatalf("failed to assign task: %v", err)
	}

	// Wait until task is running
	for i := 0; i < 40; i++ {
		time.Sleep(50 * time.Millisecond)
		snap, _ := tm.GetTask(taskID)
		if snap != nil && snap.State == models.TaskStateRunning {
			break
		}
	}

	// 1. Execute failure simulation: Kill Process
	res, err := simulator.KillProcess(ctx, taskID)
	if err != nil {
		t.Fatalf("failed to kill task process: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected successful kill process result")
	}

	// 2. Poll until task transitions to FAILED or STOPPED
	var finalSnap *worker.TaskStatusSnapshot
	for i := 0; i < 40; i++ {
		time.Sleep(50 * time.Millisecond)
		finalSnap, _ = tm.GetTask(taskID)
		if finalSnap != nil && (finalSnap.State == models.TaskStateFailed || finalSnap.State == models.TaskStateStopped) {
			break
		}
	}

	if finalSnap == nil || (finalSnap.State != models.TaskStateFailed && finalSnap.State != models.TaskStateStopped) {
		t.Fatalf("expected task state FAILED or STOPPED after kill simulation, got: %v", finalSnap)
	}
}

func TestSimulator_BreakAndRestoreHealthEndpoint(t *testing.T) {
	ctx := context.Background()
	store, _ := sqlite.Open(ctx, ":memory:")
	defer store.Close()

	logger := logging.NewDefaultLogger()
	prober := NewSimulatedProber(health.NewDefaultProber())
	simulator := NewSimulator(store, nil, prober, logger)

	taskID := id.NewTaskID()
	cfg := health.ProbeConfig{
		Type: health.CheckTypeProcess,
		PID:  1, // dummy pid
	}

	// 1. Inject failure
	res, err := simulator.BreakHealthEndpoint(ctx, taskID, "injected test outage")
	if err != nil {
		t.Fatalf("failed to break health endpoint: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected break health success")
	}

	// 2. Probe should fail
	probeRes := prober.Check(ctx, cfg)
	if probeRes.Healthy {
		t.Fatalf("expected probe to fail after break health injection")
	}

	// 3. Restore health
	resRestore, err := simulator.RestoreHealthEndpoint(ctx, taskID)
	if err != nil || !resRestore.Success {
		t.Fatalf("failed to restore health endpoint: %v", err)
	}
}

func TestSimulator_DelayHeartbeat(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to open sqlite store: %v", err)
	}
	defer store.Close()

	logger := logging.NewDefaultLogger()
	simulator := NewSimulator(store, nil, nil, logger)

	now := time.Now().UTC()
	workerID := id.NewWorkerID()
	nodeID := id.NewNodeID()

	_ = store.Nodes().Create(ctx, &models.Node{
		ID:        nodeID,
		Name:      "test-node",
		Status:    "READY",
		CreatedAt: now,
		UpdatedAt: now,
	})

	_ = store.Workers().Create(ctx, &models.Worker{
		ID:        workerID,
		NodeID:    nodeID,
		Status:    "READY",
		Heartbeat: now,
		CreatedAt: now,
		UpdatedAt: now,
	})

	// Delay heartbeat by 90 seconds
	res, err := simulator.DelayHeartbeat(ctx, workerID, 90*time.Second)
	if err != nil {
		t.Fatalf("failed to delay heartbeat: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected delay heartbeat success")
	}

	w, _ := store.Workers().Get(ctx, workerID)
	if w.Heartbeat.After(now.Add(-60 * time.Second)) {
		t.Fatalf("expected heartbeat to be backdated by >60s, got %s", w.Heartbeat)
	}
}

func TestSimulator_ExhaustResources(t *testing.T) {
	ctx := context.Background()
	store, _ := sqlite.Open(ctx, ":memory:")
	defer store.Close()

	logger := logging.NewDefaultLogger()
	simulator := NewSimulator(store, nil, nil, logger)

	res, err := simulator.ExhaustResources(ctx, id.NewTaskID(), 32)
	if err != nil {
		t.Fatalf("failed to run resource exhaustion simulation: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success")
	}
}
