package scheduler

import (
	"fmt"
	"strings"

	"github.com/cloudx-org/cloudx/internal/common/id"
)

// Priority represents task scheduling importance.
type Priority int

const (
	PriorityLow      Priority = 10
	PriorityStandard Priority = 50
	PriorityHigh     Priority = 100
	PriorityCritical Priority = 1000
)

// TaskRequirements defines the placement and resource criteria required by a workload.
type TaskRequirements struct {
	TaskID          id.ID             `json:"task_id"`
	CPU             float64           `json:"cpu"`              // requested CPU cores (e.g. 0.5, 1.0, 2.0)
	Memory          int64             `json:"memory"`           // requested Memory in bytes (e.g. 512*1024*1024)
	RequiredRuntime string            `json:"required_runtime"` // "native", "docker", etc.
	NodeConstraints map[string]string `json:"node_constraints,omitempty"`
	AffinityTags    []string          `json:"affinity_tags,omitempty"`
	Priority        Priority          `json:"priority"`
}

// WorkerCapacity represents the hardware and runtime profile of a candidate worker.
type WorkerCapacity struct {
	WorkerID            id.ID             `json:"worker_id"`
	NodeID              id.ID             `json:"node_id"`
	Hostname            string            `json:"hostname"`
	Status              string            `json:"status"` // "READY", "SUSPECTED", "UNHEALTHY", "LOST"
	CPUTotal            float64           `json:"cpu_total"`
	CPUAllocated        float64           `json:"cpu_allocated"`
	MemoryTotal         int64             `json:"memory_total"`
	MemoryAllocated     int64             `json:"memory_allocated"`
	RuntimeCapabilities []string          `json:"runtime_capabilities"`
	NodeLabels          map[string]string `json:"node_labels,omitempty"`
	Tags                []string          `json:"tags,omitempty"`
	TaskCount           int               `json:"task_count"`
}

// CPUAvailable returns the unallocated CPU cores on this worker.
func (w *WorkerCapacity) CPUAvailable() float64 {
	avail := w.CPUTotal - w.CPUAllocated
	if avail < 0 {
		return 0
	}
	return avail
}

// MemoryAvailable returns the unallocated RAM in bytes on this worker.
func (w *WorkerCapacity) MemoryAvailable() int64 {
	avail := w.MemoryTotal - w.MemoryAllocated
	if avail < 0 {
		return 0
	}
	return avail
}

// CPUPressure returns the CPU allocation ratio (0.0 to 1.0+).
func (w *WorkerCapacity) CPUPressure() float64 {
	if w.CPUTotal <= 0 {
		return 1.0
	}
	return w.CPUAllocated / w.CPUTotal
}

// MemoryPressure returns the Memory allocation ratio (0.0 to 1.0+).
func (w *WorkerCapacity) MemoryPressure() float64 {
	if w.MemoryTotal <= 0 {
		return 1.0
	}
	return float64(w.MemoryAllocated) / float64(w.MemoryTotal)
}


// FitResult details whether a worker can run a task and the reason if rejected.
type FitResult struct {
	Feasible bool     `json:"feasible"`
	Reasons  []string `json:"reasons,omitempty"`
}

// CanFit evaluates whether a candidate worker satisfies all hard requirements of a task.
func CanFit(worker *WorkerCapacity, req *TaskRequirements) FitResult {
	var reasons []string

	if worker == nil {
		return FitResult{Feasible: false, Reasons: []string{"worker is nil"}}
	}
	if req == nil {
		return FitResult{Feasible: true}
	}

	// 1. Worker Status check (Must be READY)
	if strings.ToUpper(worker.Status) != "READY" {
		reasons = append(reasons, fmt.Sprintf("worker status is %s (must be READY)", worker.Status))
	}

	// 2. CPU Capacity check
	if req.CPU > 0 && worker.CPUAvailable() < req.CPU {
		reasons = append(reasons, fmt.Sprintf("insufficient CPU: required %.2f cores, available %.2f cores (total: %.2f, allocated: %.2f)",
			req.CPU, worker.CPUAvailable(), worker.CPUTotal, worker.CPUAllocated))
	}

	// 3. Memory Capacity check
	if req.Memory > 0 && worker.MemoryAvailable() < req.Memory {
		reasons = append(reasons, fmt.Sprintf("insufficient memory: required %d bytes, available %d bytes (total: %d, allocated: %d)",
			req.Memory, worker.MemoryAvailable(), worker.MemoryTotal, worker.MemoryAllocated))
	}

	// 4. Runtime Capabilities check
	if req.RequiredRuntime != "" {
		runtimeFound := false
		for _, r := range worker.RuntimeCapabilities {
			if strings.EqualFold(r, req.RequiredRuntime) {
				runtimeFound = true
				break
			}
		}
		if !runtimeFound {
			reasons = append(reasons, fmt.Sprintf("incompatible runtime: task requires '%s', worker supports %v",
				req.RequiredRuntime, worker.RuntimeCapabilities))
		}
	}

	// 5. Node Constraints check
	if len(req.NodeConstraints) > 0 {
		for k, v := range req.NodeConstraints {
			workerVal, exists := worker.NodeLabels[k]
			if !exists || workerVal != v {
				reasons = append(reasons, fmt.Sprintf("node constraint unmet: '%s=%s' (worker has '%s')", k, v, workerVal))
			}
		}
	}

	return FitResult{
		Feasible: len(reasons) == 0,
		Reasons:  reasons,
	}
}
