package scheduler

import (
	"context"
	"fmt"
	"testing"

	"github.com/cloudx-org/cloudx/internal/common/id"
)

// TestMultiNodeScheduling_ThreeMachines_ReplicasDistributed verifies that when
// scheduling 3 replicas of a service across Machine A, Machine B, Machine C,
// the scheduler uses service anti-affinity spread to assign exactly 1 replica to each node.
func TestMultiNodeScheduling_ThreeMachines_ReplicasDistributed(t *testing.T) {
	sched := NewBasicScheduler()
	serviceID := id.NewServiceID()

	workers := []*WorkerCapacity{
		{
			WorkerID:            id.NewWorkerID(),
			Hostname:            "Machine-A",
			Status:              "READY",
			CPUTotal:            8.0,
			CPUAllocated:        0.0,
			MemoryTotal:         16 * 1024 * 1024 * 1024,
			MemoryAllocated:     0,
			RuntimeCapabilities: []string{"native", "docker"},
			ServiceTaskCounts:   make(map[id.ID]int),
		},
		{
			WorkerID:            id.NewWorkerID(),
			Hostname:            "Machine-B",
			Status:              "READY",
			CPUTotal:            8.0,
			CPUAllocated:        0.0,
			MemoryTotal:         16 * 1024 * 1024 * 1024,
			MemoryAllocated:     0,
			RuntimeCapabilities: []string{"native", "docker"},
			ServiceTaskCounts:   make(map[id.ID]int),
		},
		{
			WorkerID:            id.NewWorkerID(),
			Hostname:            "Machine-C",
			Status:              "READY",
			CPUTotal:            8.0,
			CPUAllocated:        0.0,
			MemoryTotal:         16 * 1024 * 1024 * 1024,
			MemoryAllocated:     0,
			RuntimeCapabilities: []string{"native", "docker"},
			ServiceTaskCounts:   make(map[id.ID]int),
		},
	}

	taskReq := TaskRequirements{
		ServiceID:       serviceID,
		CPU:             1.0,
		Memory:          1024 * 1024 * 1024,
		RequiredRuntime: "native",
	}

	// Schedule Replica 1
	task1 := taskReq
	task1.TaskID = id.NewTaskID()
	dec1, err := sched.Schedule(context.Background(), &task1, workers)
	if err != nil {
		t.Fatalf("Failed to schedule replica 1: %v", err)
	}

	// Update the chosen worker's capacity to reflect replica 1 placement
	for _, w := range workers {
		if w.WorkerID == dec1.WorkerID {
			w.CPUAllocated += task1.CPU
			w.MemoryAllocated += task1.Memory
			w.TaskCount++
			w.ServiceTaskCounts[serviceID]++
		}
	}

	// Schedule Replica 2
	task2 := taskReq
	task2.TaskID = id.NewTaskID()
	dec2, err := sched.Schedule(context.Background(), &task2, workers)
	if err != nil {
		t.Fatalf("Failed to schedule replica 2: %v", err)
	}
	if dec2.WorkerID == dec1.WorkerID {
		t.Fatalf("Replica 2 placed on same worker as Replica 1 (%s), expected spread", dec1.WorkerID)
	}

	// Update chosen worker for replica 2
	for _, w := range workers {
		if w.WorkerID == dec2.WorkerID {
			w.CPUAllocated += task2.CPU
			w.MemoryAllocated += task2.Memory
			w.TaskCount++
			w.ServiceTaskCounts[serviceID]++
		}
	}

	// Schedule Replica 3
	task3 := taskReq
	task3.TaskID = id.NewTaskID()
	dec3, err := sched.Schedule(context.Background(), &task3, workers)
	if err != nil {
		t.Fatalf("Failed to schedule replica 3: %v", err)
	}
	if dec3.WorkerID == dec1.WorkerID || dec3.WorkerID == dec2.WorkerID {
		t.Fatalf("Replica 3 placed on duplicate worker (%s), expected third node", dec3.WorkerID)
	}

	// Update chosen worker for replica 3
	for _, w := range workers {
		if w.WorkerID == dec3.WorkerID {
			w.CPUAllocated += task3.CPU
			w.MemoryAllocated += task3.Memory
			w.TaskCount++
			w.ServiceTaskCounts[serviceID]++
		}
	}

	// Verify all 3 distinct machines have exactly 1 task
	for _, w := range workers {
		count := w.ServiceTaskCounts[serviceID]
		if count != 1 {
			t.Errorf("Worker %s (%s) expected 1 replica, got %d", w.Hostname, w.WorkerID, count)
		}
	}
}

// TestMultiNodeScheduling_ScaleUpBeyondNodeCount verifies balanced distribution
// when replicas (e.g. 6) exceed number of physical machines (3).
func TestMultiNodeScheduling_ScaleUpBeyondNodeCount(t *testing.T) {
	sched := NewBasicScheduler()
	serviceID := id.NewServiceID()

	workers := []*WorkerCapacity{
		{
			WorkerID:            id.NewWorkerID(),
			Hostname:            "Machine-A",
			Status:              "READY",
			CPUTotal:            16.0,
			MemoryTotal:         32 * 1024 * 1024 * 1024,
			RuntimeCapabilities: []string{"native"},
			ServiceTaskCounts:   make(map[id.ID]int),
		},
		{
			WorkerID:            id.NewWorkerID(),
			Hostname:            "Machine-B",
			Status:              "READY",
			CPUTotal:            16.0,
			MemoryTotal:         32 * 1024 * 1024 * 1024,
			RuntimeCapabilities: []string{"native"},
			ServiceTaskCounts:   make(map[id.ID]int),
		},
		{
			WorkerID:            id.NewWorkerID(),
			Hostname:            "Machine-C",
			Status:              "READY",
			CPUTotal:            16.0,
			MemoryTotal:         32 * 1024 * 1024 * 1024,
			RuntimeCapabilities: []string{"native"},
			ServiceTaskCounts:   make(map[id.ID]int),
		},
	}

	numReplicas := 6
	for r := 1; r <= numReplicas; r++ {
		taskReq := TaskRequirements{
			TaskID:          id.NewTaskID(),
			ServiceID:       serviceID,
			CPU:             1.0,
			Memory:          1024 * 1024 * 1024,
			RequiredRuntime: "native",
		}

		dec, err := sched.Schedule(context.Background(), &taskReq, workers)
		if err != nil {
			t.Fatalf("Failed to schedule replica %d: %v", r, err)
		}

		for _, w := range workers {
			if w.WorkerID == dec.WorkerID {
				w.CPUAllocated += taskReq.CPU
				w.MemoryAllocated += taskReq.Memory
				w.TaskCount++
				w.ServiceTaskCounts[serviceID]++
			}
		}
	}

	// Each worker should have exactly 2 replicas
	for _, w := range workers {
		count := w.ServiceTaskCounts[serviceID]
		if count != 2 {
			t.Errorf("Worker %s (%s) expected 2 replicas, got %d", w.Hostname, w.WorkerID, count)
		}
	}
}

// TestMultiNodeScheduling_HeterogeneousCapacity verifies scheduling respects node resources
// when one machine fills up and remaining replicas overflow to other machines.
func TestMultiNodeScheduling_HeterogeneousCapacity(t *testing.T) {
	sched := NewBasicScheduler()
	serviceID := id.NewServiceID()

	workers := []*WorkerCapacity{
		{
			WorkerID:            id.NewWorkerID(),
			Hostname:            "Machine-A-Small",
			Status:              "READY",
			CPUTotal:            1.0, // Can fit only 1 task
			MemoryTotal:         2 * 1024 * 1024 * 1024,
			RuntimeCapabilities: []string{"native"},
			ServiceTaskCounts:   make(map[id.ID]int),
		},
		{
			WorkerID:            id.NewWorkerID(),
			Hostname:            "Machine-B-Large",
			Status:              "READY",
			CPUTotal:            8.0,
			MemoryTotal:         16 * 1024 * 1024 * 1024,
			RuntimeCapabilities: []string{"native"},
			ServiceTaskCounts:   make(map[id.ID]int),
		},
		{
			WorkerID:            id.NewWorkerID(),
			Hostname:            "Machine-C-Large",
			Status:              "READY",
			CPUTotal:            8.0,
			MemoryTotal:         16 * 1024 * 1024 * 1024,
			RuntimeCapabilities: []string{"native"},
			ServiceTaskCounts:   make(map[id.ID]int),
		},
	}

	// Schedule 5 replicas requiring 1.0 CPU each
	for r := 1; r <= 5; r++ {
		taskReq := TaskRequirements{
			TaskID:          id.NewTaskID(),
			ServiceID:       serviceID,
			CPU:             1.0,
			Memory:          1024 * 1024 * 1024,
			RequiredRuntime: "native",
		}

		dec, err := sched.Schedule(context.Background(), &taskReq, workers)
		if err != nil {
			t.Fatalf("Failed to schedule replica %d: %v", r, err)
		}

		for _, w := range workers {
			if w.WorkerID == dec.WorkerID {
				w.CPUAllocated += taskReq.CPU
				w.MemoryAllocated += taskReq.Memory
				w.TaskCount++
				w.ServiceTaskCounts[serviceID]++
			}
		}
	}

	// Machine A Small should have 1 task (100% full)
	if workers[0].ServiceTaskCounts[serviceID] != 1 {
		t.Errorf("Machine A Small expected 1 replica, got %d", workers[0].ServiceTaskCounts[serviceID])
	}
	// Total tasks placed must be 5
	totalTasks := workers[0].TaskCount + workers[1].TaskCount + workers[2].TaskCount
	if totalTasks != 5 {
		t.Errorf("Expected 5 total tasks placed, got %d", totalTasks)
	}
	fmt.Printf("Distribution across nodes: Machine A=%d, Machine B=%d, Machine C=%d\n",
		workers[0].TaskCount, workers[1].TaskCount, workers[2].TaskCount)
}
