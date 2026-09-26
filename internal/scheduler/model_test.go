package scheduler

import (
	"testing"

	"github.com/cloudx-org/cloudx/internal/common/id"
)

func TestCanFit_Feasibility_Success(t *testing.T) {
	worker := &WorkerCapacity{
		WorkerID:            id.NewWorkerID(),
		NodeID:              id.NewNodeID(),
		Hostname:            "worker-1",
		Status:              "READY",
		CPUTotal:            8.0,
		CPUAllocated:        2.0, // 6.0 available
		MemoryTotal:         16 * 1024 * 1024 * 1024,
		MemoryAllocated:     4 * 1024 * 1024 * 1024, // 12GB available
		RuntimeCapabilities: []string{"native", "docker"},
		NodeLabels: map[string]string{
			"zone": "us-east-1a",
			"tier": "compute",
		},
	}

	req := &TaskRequirements{
		TaskID:          id.NewTaskID(),
		CPU:             2.0,
		Memory:          4 * 1024 * 1024 * 1024,
		RequiredRuntime: "native",
		NodeConstraints: map[string]string{
			"zone": "us-east-1a",
		},
		Priority: PriorityStandard,
	}

	result := CanFit(worker, req)
	if !result.Feasible {
		t.Fatalf("expected task to fit worker, got reasons: %v", result.Reasons)
	}
}

func TestCanFit_InsufficientCPU(t *testing.T) {
	worker := &WorkerCapacity{
		WorkerID:            id.NewWorkerID(),
		Status:              "READY",
		CPUTotal:            4.0,
		CPUAllocated:        3.5, // only 0.5 available
		MemoryTotal:         16 * 1024 * 1024 * 1024,
		MemoryAllocated:     0,
		RuntimeCapabilities: []string{"native"},
	}

	req := &TaskRequirements{
		TaskID:          id.NewTaskID(),
		CPU:             1.0, // asks for 1.0
		Memory:          512 * 1024 * 1024,
		RequiredRuntime: "native",
	}

	result := CanFit(worker, req)
	if result.Feasible {
		t.Fatalf("expected task fit rejection due to CPU, but was feasible")
	}
	if len(result.Reasons) == 0 {
		t.Fatalf("expected rejection reason, got none")
	}
}

func TestCanFit_InsufficientMemory(t *testing.T) {
	worker := &WorkerCapacity{
		WorkerID:            id.NewWorkerID(),
		Status:              "READY",
		CPUTotal:            8.0,
		CPUAllocated:        0.0,
		MemoryTotal:         8 * 1024 * 1024 * 1024,
		MemoryAllocated:     7 * 1024 * 1024 * 1024, // only 1GB available
		RuntimeCapabilities: []string{"native"},
	}

	req := &TaskRequirements{
		TaskID:          id.NewTaskID(),
		CPU:             1.0,
		Memory:          2 * 1024 * 1024 * 1024, // asks for 2GB
		RequiredRuntime: "native",
	}

	result := CanFit(worker, req)
	if result.Feasible {
		t.Fatalf("expected task fit rejection due to Memory, but was feasible")
	}
}

func TestCanFit_IncompatibleRuntime(t *testing.T) {
	worker := &WorkerCapacity{
		WorkerID:            id.NewWorkerID(),
		Status:              "READY",
		CPUTotal:            8.0,
		MemoryTotal:         16 * 1024 * 1024 * 1024,
		RuntimeCapabilities: []string{"native"}, // Only native supported
	}

	req := &TaskRequirements{
		TaskID:          id.NewTaskID(),
		CPU:             1.0,
		Memory:          512 * 1024 * 1024,
		RequiredRuntime: "docker", // Task requires docker
	}

	result := CanFit(worker, req)
	if result.Feasible {
		t.Fatalf("expected task fit rejection due to incompatible runtime")
	}
}

func TestCanFit_WorkerNotReady(t *testing.T) {
	worker := &WorkerCapacity{
		WorkerID:            id.NewWorkerID(),
		Status:              "UNHEALTHY", // Worker is unhealthy
		CPUTotal:            16.0,
		MemoryTotal:         64 * 1024 * 1024 * 1024,
		RuntimeCapabilities: []string{"native"},
	}

	req := &TaskRequirements{
		TaskID:          id.NewTaskID(),
		CPU:             1.0,
		Memory:          512 * 1024 * 1024,
		RequiredRuntime: "native",
	}

	result := CanFit(worker, req)
	if result.Feasible {
		t.Fatalf("expected task fit rejection due to non-READY status")
	}
}

func TestCanFit_NodeConstraintMismatch(t *testing.T) {
	worker := &WorkerCapacity{
		WorkerID:            id.NewWorkerID(),
		Status:              "READY",
		CPUTotal:            8.0,
		MemoryTotal:         16 * 1024 * 1024 * 1024,
		RuntimeCapabilities: []string{"native"},
		NodeLabels: map[string]string{
			"zone": "us-west-1",
		},
	}

	req := &TaskRequirements{
		TaskID:          id.NewTaskID(),
		CPU:             1.0,
		Memory:          512 * 1024 * 1024,
		RequiredRuntime: "native",
		NodeConstraints: map[string]string{
			"zone": "us-east-1", // Requires us-east-1
		},
	}

	result := CanFit(worker, req)
	if result.Feasible {
		t.Fatalf("expected rejection due to node constraint mismatch")
	}
}

func TestWorkerCapacity_AvailableCalculation(t *testing.T) {
	w := &WorkerCapacity{
		CPUTotal:        4.0,
		CPUAllocated:    3.0,
		MemoryTotal:     8000,
		MemoryAllocated: 5000,
	}

	if w.CPUAvailable() != 1.0 {
		t.Fatalf("expected 1.0 CPU available, got %f", w.CPUAvailable())
	}
	if w.MemoryAvailable() != 3000 {
		t.Fatalf("expected 3000 memory available, got %d", w.MemoryAvailable())
	}

	// Over-allocated case returns 0, not negative
	w.CPUAllocated = 5.0
	w.MemoryAllocated = 9000
	if w.CPUAvailable() != 0 {
		t.Fatalf("expected 0 CPU available on over-allocation, got %f", w.CPUAvailable())
	}
	if w.MemoryAvailable() != 0 {
		t.Fatalf("expected 0 Memory available on over-allocation, got %d", w.MemoryAvailable())
	}
}

func TestCanFit_StorageAffinity(t *testing.T) {
	worker := &WorkerCapacity{
		WorkerID:            id.NewWorkerID(),
		Status:              "READY",
		CPUTotal:            8.0,
		MemoryTotal:         16 * 1024 * 1024 * 1024,
		RuntimeCapabilities: []string{"native"},
		VolumeNames:         []string{"app-data", "db-storage"},
	}

	// Case 1: Requirement for matching volume fits
	reqMatching := &TaskRequirements{
		TaskID:          id.NewTaskID(),
		CPU:             1.0,
		Memory:          512 * 1024 * 1024,
		RequiredRuntime: "native",
		RequiredVolumes: []string{"app-data"},
	}
	res := CanFit(worker, reqMatching)
	if !res.Feasible {
		t.Fatalf("expected matching volume to be feasible, got: %v", res.Reasons)
	}

	// Case 2: Requirement for missing volume is rejected
	reqMissing := &TaskRequirements{
		TaskID:          id.NewTaskID(),
		CPU:             1.0,
		Memory:          512 * 1024 * 1024,
		RequiredRuntime: "native",
		RequiredVolumes: []string{"missing-vol"},
	}
	resMissing := CanFit(worker, reqMissing)
	if resMissing.Feasible {
		t.Fatalf("expected missing volume to be rejected")
	}
}

func TestCanFit_PortConflict(t *testing.T) {
	worker := &WorkerCapacity{
		WorkerID:            id.NewWorkerID(),
		Status:              "READY",
		CPUTotal:            8.0,
		MemoryTotal:         16 * 1024 * 1024 * 1024,
		RuntimeCapabilities: []string{"native"},
		AllocatedPorts:      []int{8080, 9090},
	}

	// Case 1: Non-conflicting port requirement succeeds
	reqFree := &TaskRequirements{
		TaskID:          id.NewTaskID(),
		CPU:             1.0,
		Memory:          512 * 1024 * 1024,
		RequiredRuntime: "native",
		RequiredPorts:   []int{8000, 3000},
	}
	resFree := CanFit(worker, reqFree)
	if !resFree.Feasible {
		t.Fatalf("expected non-conflicting port to fit, got reasons: %v", resFree.Reasons)
	}

	// Case 2: Conflicting host port requirement is rejected
	reqConflict := &TaskRequirements{
		TaskID:          id.NewTaskID(),
		CPU:             1.0,
		Memory:          512 * 1024 * 1024,
		RequiredRuntime: "native",
		RequiredPorts:   []int{8080},
	}
	resConflict := CanFit(worker, reqConflict)
	if resConflict.Feasible {
		t.Fatalf("expected conflicting port to be rejected")
	}
	if len(resConflict.Reasons) == 0 || !containsSubstring(resConflict.Reasons[0], "port conflict: host port 8080 is already in use") {
		t.Fatalf("expected port conflict reason, got: %v", resConflict.Reasons)
	}
}

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && len(substr) > 0 && (s[:len(substr)] == substr || findSub(s, substr)))
}

func findSub(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

