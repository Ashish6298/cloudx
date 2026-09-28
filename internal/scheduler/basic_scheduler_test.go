package scheduler

import (
	"context"
	"testing"

	"github.com/cloudx-org/cloudx/internal/common/id"
)

func TestBasicScheduler_NoWorkers(t *testing.T) {
	sched := NewBasicScheduler()
	req := &TaskRequirements{
		TaskID: id.NewTaskID(),
		CPU:    1.0,
		Memory: 1024 * 1024 * 1024,
	}

	decision, err := sched.Schedule(context.Background(), req, []*WorkerCapacity{})
	if err == nil {
		t.Fatalf("expected error when scheduling with no workers, got decision: %+v", decision)
	}
}

func TestBasicScheduler_NoCapacity(t *testing.T) {
	sched := NewBasicScheduler()
	req := &TaskRequirements{
		TaskID: id.NewTaskID(),
		CPU:    8.0,
		Memory: 16 * 1024 * 1024 * 1024,
	}

	worker := &WorkerCapacity{
		WorkerID:        id.NewWorkerID(),
		Hostname:        "node-small",
		Status:          "READY",
		CPUTotal:        4.0,
		CPUAllocated:    0.0,
		MemoryTotal:     8 * 1024 * 1024 * 1024,
		MemoryAllocated: 0,
	}

	decision, err := sched.Schedule(context.Background(), req, []*WorkerCapacity{worker})
	if err == nil {
		t.Fatalf("expected error when no worker has sufficient capacity, got: %+v", decision)
	}
}

func TestBasicScheduler_OneWorker(t *testing.T) {
	sched := NewBasicScheduler()
	req := &TaskRequirements{
		TaskID: id.NewTaskID(),
		CPU:    1.0,
		Memory: 1024 * 1024 * 1024,
	}

	workerID := id.NewWorkerID()
	worker := &WorkerCapacity{
		WorkerID:        workerID,
		Hostname:        "node-1",
		Status:          "READY",
		CPUTotal:        4.0,
		CPUAllocated:    1.0,
		MemoryTotal:     8 * 1024 * 1024 * 1024,
		MemoryAllocated: 2 * 1024 * 1024 * 1024,
	}

	decision, err := sched.Schedule(context.Background(), req, []*WorkerCapacity{worker})
	if err != nil {
		t.Fatalf("unexpected error scheduling onto single feasible worker: %v", err)
	}

	if decision.WorkerID != workerID {
		t.Fatalf("expected worker %s, got %s", workerID, decision.WorkerID)
	}
	if decision.FeasibleNodes != 1 || decision.EvaluatedNodes != 1 {
		t.Fatalf("expected 1 evaluated and feasible node, got evaluated=%d, feasible=%d",
			decision.EvaluatedNodes, decision.FeasibleNodes)
	}
}

func TestBasicScheduler_MultipleWorkers_PicksHighestScoring(t *testing.T) {
	sched := NewBasicScheduler()
	req := &TaskRequirements{
		TaskID: id.NewTaskID(),
		CPU:    1.0,
		Memory: 1024 * 1024 * 1024,
	}

	// Busy worker with 80% allocation
	busyWorker := &WorkerCapacity{
		WorkerID:        id.NewWorkerID(),
		Hostname:        "node-busy",
		Status:          "READY",
		CPUTotal:        4.0,
		CPUAllocated:    3.0,
		MemoryTotal:     8 * 1024 * 1024 * 1024,
		MemoryAllocated: 6 * 1024 * 1024 * 1024,
		TaskCount:       5,
	}

	// Idle worker with low allocation
	idleWorkerID := id.NewWorkerID()
	idleWorker := &WorkerCapacity{
		WorkerID:        idleWorkerID,
		Hostname:        "node-idle",
		Status:          "READY",
		CPUTotal:        16.0,
		CPUAllocated:    2.0,
		MemoryTotal:     32 * 1024 * 1024 * 1024,
		MemoryAllocated: 4 * 1024 * 1024 * 1024,
		TaskCount:       1,
	}

	decision, err := sched.Schedule(context.Background(), req, []*WorkerCapacity{busyWorker, idleWorker})
	if err != nil {
		t.Fatalf("unexpected schedule error: %v", err)
	}

	if decision.WorkerID != idleWorkerID {
		t.Fatalf("expected scheduler to pick idle worker %s, got %s", idleWorkerID, decision.WorkerID)
	}
}

func TestBasicScheduler_DeterministicSelection(t *testing.T) {
	sched := NewBasicScheduler()
	req := &TaskRequirements{
		TaskID: id.NewTaskID(),
		CPU:    1.0,
		Memory: 1024 * 1024 * 1024,
	}

	// Two identical workers with deterministic fixed IDs
	w1 := &WorkerCapacity{
		WorkerID:    id.ID("worker-aaaa"),
		Hostname:    "node-a",
		Status:      "READY",
		CPUTotal:    4.0,
		MemoryTotal: 8 * 1024 * 1024 * 1024,
		TaskCount:   0,
	}
	w2 := &WorkerCapacity{
		WorkerID:    id.ID("worker-zzzz"),
		Hostname:    "node-z",
		Status:      "READY",
		CPUTotal:    4.0,
		MemoryTotal: 8 * 1024 * 1024 * 1024,
		TaskCount:   0,
	}

	// Run 20 iterations in different input slices to verify order independence & deterministic selection
	for i := 0; i < 20; i++ {
		var workers []*WorkerCapacity
		if i%2 == 0 {
			workers = []*WorkerCapacity{w1, w2}
		} else {
			workers = []*WorkerCapacity{w2, w1}
		}

		decision, err := sched.Schedule(context.Background(), req, workers)
		if err != nil {
			t.Fatalf("schedule failed at iteration %d: %v", i, err)
		}

		// Should always deterministically tie-break to worker-aaaa
		if decision.WorkerID != id.ID("worker-aaaa") {
			t.Fatalf("expected deterministic choice 'worker-aaaa', got %s at iteration %d", decision.WorkerID, i)
		}
	}
}

func TestBasicScheduler_WorkerFailure_FiltersOutNonReady(t *testing.T) {
	sched := NewBasicScheduler()
	req := &TaskRequirements{
		TaskID: id.NewTaskID(),
		CPU:    1.0,
		Memory: 1024 * 1024 * 1024,
	}

	// Large but LOST worker
	lostWorker := &WorkerCapacity{
		WorkerID:    id.NewWorkerID(),
		Hostname:    "node-lost",
		Status:      "LOST",
		CPUTotal:    32.0,
		MemoryTotal: 64 * 1024 * 1024 * 1024,
	}

	// Unhealthy worker
	unhealthyWorker := &WorkerCapacity{
		WorkerID:    id.NewWorkerID(),
		Hostname:    "node-unhealthy",
		Status:      "UNHEALTHY",
		CPUTotal:    16.0,
		MemoryTotal: 32 * 1024 * 1024 * 1024,
	}

	// Smaller but READY worker
	readyWorkerID := id.NewWorkerID()
	readyWorker := &WorkerCapacity{
		WorkerID:    readyWorkerID,
		Hostname:    "node-ready",
		Status:      "READY",
		CPUTotal:    4.0,
		MemoryTotal: 8 * 1024 * 1024 * 1024,
	}

	decision, err := sched.Schedule(context.Background(), req, []*WorkerCapacity{lostWorker, unhealthyWorker, readyWorker})
	if err != nil {
		t.Fatalf("expected successful scheduling to ready worker, got error: %v", err)
	}

	if decision.WorkerID != readyWorkerID {
		t.Fatalf("expected worker %s, got %s", readyWorkerID, decision.WorkerID)
	}
	if decision.FeasibleNodes != 1 {
		t.Fatalf("expected 1 feasible node, got %d", decision.FeasibleNodes)
	}
}
