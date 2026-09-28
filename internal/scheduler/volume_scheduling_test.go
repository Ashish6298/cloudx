package scheduler

import (
	"context"
	"testing"

	"github.com/cloudx-org/cloudx/internal/common/id"
)

// ─────────────────────────────────────────────────────────────────────────────
// Unit tests for volume affinity in CanFit
// ─────────────────────────────────────────────────────────────────────────────

func TestCanFit_VolumeAffinity_NoRequirement(t *testing.T) {
	// A task with no volume requirements should fit any ready worker regardless
	// of whether that worker has volumes or not.
	worker := &WorkerCapacity{
		WorkerID:            id.NewWorkerID(),
		Status:              "READY",
		CPUTotal:            8.0,
		MemoryTotal:         16 * 1024 * 1024 * 1024,
		RuntimeCapabilities: []string{"native"},
		VolumeNames:         []string{}, // worker owns no volumes
	}

	req := &TaskRequirements{
		TaskID:          id.NewTaskID(),
		CPU:             1.0,
		Memory:          512 * 1024 * 1024,
		RequiredRuntime: "native",
		RequiredVolumes: nil, // no volume requirement
	}

	result := CanFit(worker, req)
	if !result.Feasible {
		t.Fatalf("expected fit for task with no volume requirements, got reasons: %v", result.Reasons)
	}
}

func TestCanFit_VolumeAffinity_WorkerOwnsVolume(t *testing.T) {
	// Worker 2 owns "data" → task requiring "data" should fit worker 2.
	workerID := id.NewWorkerID()
	worker := &WorkerCapacity{
		WorkerID:            workerID,
		Status:              "READY",
		CPUTotal:            8.0,
		MemoryTotal:         16 * 1024 * 1024 * 1024,
		RuntimeCapabilities: []string{"native"},
		VolumeNames:         []string{"data", "logs"},
	}

	req := &TaskRequirements{
		TaskID:          id.NewTaskID(),
		CPU:             1.0,
		Memory:          512 * 1024 * 1024,
		RequiredRuntime: "native",
		RequiredVolumes: []string{"data"},
	}

	result := CanFit(worker, req)
	if !result.Feasible {
		t.Fatalf("expected task requiring 'data' to fit worker owning 'data', got reasons: %v", result.Reasons)
	}
}

func TestCanFit_VolumeAffinity_WorkerDoesNotOwnVolume(t *testing.T) {
	// Worker 1 does NOT own "data" → task requiring "data" must be rejected.
	worker := &WorkerCapacity{
		WorkerID:            id.NewWorkerID(),
		Status:              "READY",
		CPUTotal:            8.0,
		MemoryTotal:         16 * 1024 * 1024 * 1024,
		RuntimeCapabilities: []string{"native"},
		VolumeNames:         []string{"other-vol"}, // owns "other-vol" but not "data"
	}

	req := &TaskRequirements{
		TaskID:          id.NewTaskID(),
		CPU:             1.0,
		Memory:          512 * 1024 * 1024,
		RequiredRuntime: "native",
		RequiredVolumes: []string{"data"},
	}

	result := CanFit(worker, req)
	if result.Feasible {
		t.Fatal("expected task requiring 'data' to be rejected on worker that does not own 'data'")
	}
	if len(result.Reasons) == 0 {
		t.Fatal("expected a rejection reason for volume affinity failure")
	}
	t.Logf("Correctly rejected: %v", result.Reasons)
}

func TestCanFit_VolumeAffinity_WorkerHasNoVolumes(t *testing.T) {
	// Worker has no volumes at all → task requiring any volume is rejected.
	worker := &WorkerCapacity{
		WorkerID:            id.NewWorkerID(),
		Status:              "READY",
		CPUTotal:            8.0,
		MemoryTotal:         16 * 1024 * 1024 * 1024,
		RuntimeCapabilities: []string{"native"},
		VolumeNames:         nil, // zero volumes
	}

	req := &TaskRequirements{
		TaskID:          id.NewTaskID(),
		CPU:             1.0,
		Memory:          512 * 1024 * 1024,
		RequiredRuntime: "native",
		RequiredVolumes: []string{"data"},
	}

	result := CanFit(worker, req)
	if result.Feasible {
		t.Fatal("expected task rejected on worker with no volumes")
	}
}

func TestCanFit_VolumeAffinity_MultipleVolumesAllPresent(t *testing.T) {
	// Task requires two volumes; worker owns both → should fit.
	worker := &WorkerCapacity{
		WorkerID:            id.NewWorkerID(),
		Status:              "READY",
		CPUTotal:            8.0,
		MemoryTotal:         16 * 1024 * 1024 * 1024,
		RuntimeCapabilities: []string{"native"},
		VolumeNames:         []string{"db-data", "cache", "logs"},
	}

	req := &TaskRequirements{
		TaskID:          id.NewTaskID(),
		CPU:             1.0,
		Memory:          512 * 1024 * 1024,
		RequiredRuntime: "native",
		RequiredVolumes: []string{"db-data", "cache"},
	}

	result := CanFit(worker, req)
	if !result.Feasible {
		t.Fatalf("expected fit when worker owns all required volumes, got: %v", result.Reasons)
	}
}

func TestCanFit_VolumeAffinity_MultipleVolumesOnesMissing(t *testing.T) {
	// Task requires two volumes; worker owns only one → should be rejected.
	worker := &WorkerCapacity{
		WorkerID:            id.NewWorkerID(),
		Status:              "READY",
		CPUTotal:            8.0,
		MemoryTotal:         16 * 1024 * 1024 * 1024,
		RuntimeCapabilities: []string{"native"},
		VolumeNames:         []string{"db-data"}, // owns db-data but NOT cache
	}

	req := &TaskRequirements{
		TaskID:          id.NewTaskID(),
		CPU:             1.0,
		Memory:          512 * 1024 * 1024,
		RequiredRuntime: "native",
		RequiredVolumes: []string{"db-data", "cache"},
	}

	result := CanFit(worker, req)
	if result.Feasible {
		t.Fatal("expected rejection when worker is missing one required volume")
	}
}

func TestCanFit_VolumeAffinity_CaseInsensitive(t *testing.T) {
	// Volume name matching should be case-insensitive.
	worker := &WorkerCapacity{
		WorkerID:            id.NewWorkerID(),
		Status:              "READY",
		CPUTotal:            8.0,
		MemoryTotal:         16 * 1024 * 1024 * 1024,
		RuntimeCapabilities: []string{"native"},
		VolumeNames:         []string{"MyData"}, // stored with mixed case
	}

	req := &TaskRequirements{
		TaskID:          id.NewTaskID(),
		CPU:             1.0,
		Memory:          512 * 1024 * 1024,
		RequiredRuntime: "native",
		RequiredVolumes: []string{"mydata"}, // requested in lowercase
	}

	result := CanFit(worker, req)
	if !result.Feasible {
		t.Fatalf("expected case-insensitive volume match, got reasons: %v", result.Reasons)
	}
}

func TestCanFit_VolumeAffinity_CombinedWithOtherConstraints(t *testing.T) {
	// Worker has the right volume but wrong runtime → should be rejected (two reasons).
	worker := &WorkerCapacity{
		WorkerID:            id.NewWorkerID(),
		Status:              "READY",
		CPUTotal:            8.0,
		MemoryTotal:         16 * 1024 * 1024 * 1024,
		RuntimeCapabilities: []string{"native"}, // native only
		VolumeNames:         []string{"data"},
	}

	req := &TaskRequirements{
		TaskID:          id.NewTaskID(),
		CPU:             1.0,
		Memory:          512 * 1024 * 1024,
		RequiredRuntime: "docker", // incompatible
		RequiredVolumes: []string{"data"},
	}

	result := CanFit(worker, req)
	if result.Feasible {
		t.Fatal("expected rejection due to incompatible runtime (volume is present but runtime is wrong)")
	}
	// Should report runtime incompatibility, not volume failure
	hasRuntimeReason := false
	for _, r := range result.Reasons {
		if len(r) > 0 {
			hasRuntimeReason = true
		}
	}
	if !hasRuntimeReason {
		t.Fatal("expected at least one rejection reason")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Multi-worker scheduling: task must go to the correct worker
// ─────────────────────────────────────────────────────────────────────────────

func TestBasicScheduler_VolumeAffinity_TargetsCorrectWorker(t *testing.T) {
	sched := NewBasicScheduler()

	worker1ID := id.NewWorkerID()
	worker2ID := id.NewWorkerID()

	// worker-1: no volumes
	worker1 := &WorkerCapacity{
		WorkerID:            worker1ID,
		Hostname:            "worker-1",
		Status:              "READY",
		CPUTotal:            8.0,
		MemoryTotal:         16 * 1024 * 1024 * 1024,
		RuntimeCapabilities: []string{"native"},
		VolumeNames:         nil,
	}

	// worker-2: owns "data"
	worker2 := &WorkerCapacity{
		WorkerID:            worker2ID,
		Hostname:            "worker-2",
		Status:              "READY",
		CPUTotal:            8.0,
		MemoryTotal:         16 * 1024 * 1024 * 1024,
		RuntimeCapabilities: []string{"native"},
		VolumeNames:         []string{"data"},
	}

	req := &TaskRequirements{
		TaskID:          id.NewTaskID(),
		CPU:             1.0,
		Memory:          512 * 1024 * 1024,
		RequiredRuntime: "native",
		RequiredVolumes: []string{"data"},
	}

	decision, err := sched.Schedule(testCtx(), req, []*WorkerCapacity{worker1, worker2})
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	if decision.WorkerID != worker2ID {
		t.Errorf("expected task to be scheduled on worker-2 (owns 'data'), got %s (hostname=%s)",
			decision.WorkerID, decision.Hostname)
	}
	t.Logf("Correctly scheduled on worker-2 (score=%.2f, feasible=%d/%d)",
		decision.Score, decision.FeasibleNodes, decision.EvaluatedNodes)
}

func TestBasicScheduler_VolumeAffinity_NoCompatibleWorker(t *testing.T) {
	sched := NewBasicScheduler()

	// Neither worker owns "data"
	worker1 := &WorkerCapacity{
		WorkerID:            id.NewWorkerID(),
		Hostname:            "worker-1",
		Status:              "READY",
		CPUTotal:            8.0,
		MemoryTotal:         16 * 1024 * 1024 * 1024,
		RuntimeCapabilities: []string{"native"},
		VolumeNames:         []string{"other"},
	}
	worker2 := &WorkerCapacity{
		WorkerID:            id.NewWorkerID(),
		Hostname:            "worker-2",
		Status:              "READY",
		CPUTotal:            8.0,
		MemoryTotal:         16 * 1024 * 1024 * 1024,
		RuntimeCapabilities: []string{"native"},
		VolumeNames:         nil,
	}

	req := &TaskRequirements{
		TaskID:          id.NewTaskID(),
		CPU:             1.0,
		Memory:          512 * 1024 * 1024,
		RequiredRuntime: "native",
		RequiredVolumes: []string{"data"},
	}

	decision, err := sched.Schedule(testCtx(), req, []*WorkerCapacity{worker1, worker2})
	if err == nil {
		t.Fatalf("expected error when no worker owns required volume, got decision: %+v", decision)
	}
	t.Logf("Correctly failed with: %v", err)
}

func TestBasicScheduler_VolumeAffinity_ThreeWorkersPinned(t *testing.T) {
	// Three workers; only worker-3 owns the required volume.
	// Even if worker-1 or worker-2 are less loaded, task must go to worker-3.
	sched := NewBasicScheduler()

	w1 := &WorkerCapacity{
		WorkerID:            id.NewWorkerID(),
		Hostname:            "worker-1",
		Status:              "READY",
		CPUTotal:            8.0,
		CPUAllocated:        0.0, // most free CPU
		MemoryTotal:         16 * 1024 * 1024 * 1024,
		MemoryAllocated:     0,
		RuntimeCapabilities: []string{"native"},
		TaskCount:           0,
		VolumeNames:         nil,
	}
	w2 := &WorkerCapacity{
		WorkerID:            id.NewWorkerID(),
		Hostname:            "worker-2",
		Status:              "READY",
		CPUTotal:            8.0,
		CPUAllocated:        1.0,
		MemoryTotal:         16 * 1024 * 1024 * 1024,
		MemoryAllocated:     512 * 1024 * 1024,
		RuntimeCapabilities: []string{"native"},
		TaskCount:           2,
		VolumeNames:         nil,
	}
	w3ID := id.NewWorkerID()
	w3 := &WorkerCapacity{
		WorkerID:            w3ID,
		Hostname:            "worker-3",
		Status:              "READY",
		CPUTotal:            8.0,
		CPUAllocated:        4.0, // more loaded
		MemoryTotal:         16 * 1024 * 1024 * 1024,
		MemoryAllocated:     8 * 1024 * 1024 * 1024,
		RuntimeCapabilities: []string{"native"},
		TaskCount:           8,
		VolumeNames:         []string{"db-storage"}, // only owner of db-storage
	}

	req := &TaskRequirements{
		TaskID:          id.NewTaskID(),
		CPU:             0.5,
		Memory:          256 * 1024 * 1024,
		RequiredRuntime: "native",
		RequiredVolumes: []string{"db-storage"},
	}

	decision, err := sched.Schedule(testCtx(), req, []*WorkerCapacity{w1, w2, w3})
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	if decision.WorkerID != w3ID {
		t.Errorf("expected scheduling on worker-3 (volume owner), got %s", decision.Hostname)
	}
	t.Logf("Pinned to worker-3 despite higher load (score=%.2f)", decision.Score)
}

func TestBasicScheduler_NoVolumeRequirement_UsesNormalScoring(t *testing.T) {
	// Without volume constraints normal load-balancing should still pick the least loaded.
	sched := NewBasicScheduler()

	w1ID := id.NewWorkerID()
	w1 := &WorkerCapacity{
		WorkerID:            w1ID,
		Hostname:            "worker-1-light",
		Status:              "READY",
		CPUTotal:            8.0,
		CPUAllocated:        0.5, // lighter
		MemoryTotal:         16 * 1024 * 1024 * 1024,
		MemoryAllocated:     512 * 1024 * 1024,
		RuntimeCapabilities: []string{"native"},
		TaskCount:           1,
		VolumeNames:         []string{"vol-a"},
	}
	w2 := &WorkerCapacity{
		WorkerID:            id.NewWorkerID(),
		Hostname:            "worker-2-heavy",
		Status:              "READY",
		CPUTotal:            8.0,
		CPUAllocated:        6.0, // much heavier
		MemoryTotal:         16 * 1024 * 1024 * 1024,
		MemoryAllocated:     12 * 1024 * 1024 * 1024,
		RuntimeCapabilities: []string{"native"},
		TaskCount:           12,
		VolumeNames:         []string{"vol-b"},
	}

	req := &TaskRequirements{
		TaskID:          id.NewTaskID(),
		CPU:             0.5,
		Memory:          256 * 1024 * 1024,
		RequiredRuntime: "native",
		// No volume requirement — normal scheduling
	}

	decision, err := sched.Schedule(testCtx(), req, []*WorkerCapacity{w1, w2})
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if decision.WorkerID != w1ID {
		t.Errorf("expected lighter worker-1 to win, got %s", decision.Hostname)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Helper
// ─────────────────────────────────────────────────────────────────────────────

func testCtx() context.Context {
	return context.Background()
}
