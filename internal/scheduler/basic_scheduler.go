package scheduler

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/cloudx-org/cloudx/internal/common/id"
)

// Scheduler defines the orchestration interface for selecting worker nodes for tasks.
type Scheduler interface {
	// Schedule evaluates candidate workers and selects the optimal worker for the task.
	Schedule(ctx context.Context, req *TaskRequirements, workers []*WorkerCapacity) (*ScheduleDecision, error)
}

// ScheduleDecision captures the placement decision made by the scheduler.
type ScheduleDecision struct {
	TaskID          id.ID             `json:"task_id"`
	WorkerID        id.ID             `json:"worker_id"`
	Hostname        string            `json:"hostname"`
	Score           float64           `json:"score"`
	EvaluatedNodes  int               `json:"evaluated_nodes"`
	FeasibleNodes   int               `json:"feasible_nodes"`
	RejectedReasons map[string]string `json:"rejected_reasons,omitempty"` // workerID -> reason
}

// ScoredWorker wraps a feasible worker with its calculated scheduling score.
type ScoredWorker struct {
	Worker *WorkerCapacity
	Score  float64
}

// BasicScheduler implements deterministic rule-based scoring and placement.
type BasicScheduler struct{}

// NewBasicScheduler instantiates a new BasicScheduler.
func NewBasicScheduler() *BasicScheduler {
	return &BasicScheduler{}
}

// Schedule performs deterministic 6-step scheduling:
// 1. Filter unavailable workers (handled by CanFit)
// 2. Filter insufficient resources (handled by CanFit)
// 3. Filter incompatible runtime (handled by CanFit)
// 4. Calculate score for feasible workers
// 5. Select highest-scoring worker
// 6. Break ties deterministically (lexicographically by WorkerID)
func (s *BasicScheduler) Schedule(ctx context.Context, req *TaskRequirements, workers []*WorkerCapacity) (*ScheduleDecision, error) {
	if req == nil {
		return nil, fmt.Errorf("task requirements cannot be nil")
	}

	if len(workers) == 0 {
		return nil, fmt.Errorf("no workers available in cluster")
	}

	rejectedReasons := make(map[string]string)
	var feasible []*WorkerCapacity

	// 1-3. Filter candidate workers
	for _, w := range workers {
		if w == nil {
			continue
		}
		fit := CanFit(w, req)
		if fit.Feasible {
			feasible = append(feasible, w)
		} else {
			rejectedReasons[w.WorkerID.String()] = strings.Join(fit.Reasons, "; ")
		}
	}

	if len(feasible) == 0 {
		return nil, fmt.Errorf("no feasible workers found for task %s (evaluated %d workers)", req.TaskID, len(workers))
	}

	// 4. Calculate score for each feasible worker
	scored := make([]ScoredWorker, len(feasible))
	for i, w := range feasible {
		score := s.ScoreWorker(w, req)
		scored[i] = ScoredWorker{
			Worker: w,
			Score:  score,
		}
	}

	// 5-6. Sort workers: highest score first, then deterministically by WorkerID string
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].Score != scored[j].Score {
			return scored[i].Score > scored[j].Score
		}
		return scored[i].Worker.WorkerID.String() < scored[j].Worker.WorkerID.String()
	})

	best := scored[0]
	return &ScheduleDecision{
		TaskID:          req.TaskID,
		WorkerID:        best.Worker.WorkerID,
		Hostname:        best.Worker.Hostname,
		Score:           best.Score,
		EvaluatedNodes:  len(workers),
		FeasibleNodes:   len(feasible),
		RejectedReasons: rejectedReasons,
	}, nil
}

// ScoreWorker computes a deterministic placement score for a feasible candidate.
// Higher score indicates a more suitable worker.
// Scoring weights:
// - Available CPU (up to 40 pts)
// - Available Memory (up to 40 pts)
// - Low Resource Pressure bonus (up to 10 pts)
// - Low Task Count bonus (up to 10 pts)
func (s *BasicScheduler) ScoreWorker(w *WorkerCapacity, req *TaskRequirements) float64 {
	if w == nil {
		return 0
	}

	// 1. Available CPU Score (normalized, 0 to 40 points)
	// Ratio of unallocated CPU after placing the task
	cpuAvail := w.CPUAvailable()
	var cpuScore float64
	if w.CPUTotal > 0 {
		remRatio := (cpuAvail - req.CPU) / w.CPUTotal
		if remRatio < 0 {
			remRatio = 0
		}
		cpuScore = remRatio * 40.0
	}

	// 2. Available Memory Score (normalized, 0 to 40 points)
	memAvail := w.MemoryAvailable()
	var memScore float64
	if w.MemoryTotal > 0 {
		remRatio := float64(memAvail-req.Memory) / float64(w.MemoryTotal)
		if remRatio < 0 {
			remRatio = 0
		}
		memScore = remRatio * 40.0
	}

	// 3. Resource Pressure Inversion (0 to 10 points)
	// Lower pressure yields higher score
	cpuPressure := w.CPUPressure()
	memPressure := w.MemoryPressure()
	avgPressure := (cpuPressure + memPressure) / 2.0
	if avgPressure > 1.0 {
		avgPressure = 1.0
	} else if avgPressure < 0.0 {
		avgPressure = 0.0
	}
	pressureScore := (1.0 - avgPressure) * 10.0

	// 4. Existing Workload / Task Count Penalty (0 to 10 points)
	// Prefer nodes with fewer active workloads to spread load
	taskCountScore := 10.0 / (1.0 + float64(w.TaskCount))

	// 5. Service Replica Anti-Affinity Spread (0 to 50 points)
	// Heavily prefer nodes that do NOT currently run replicas of this same service
	var serviceSpreadScore float64 = 50.0
	if req.ServiceID != "" && len(w.ServiceTaskCounts) > 0 {
		sameServiceReplicas := w.ServiceTaskCounts[req.ServiceID]
		serviceSpreadScore = 50.0 / (1.0 + float64(sameServiceReplicas*5))
	}

	totalScore := cpuScore + memScore + pressureScore + taskCountScore + serviceSpreadScore
	return totalScore
}
