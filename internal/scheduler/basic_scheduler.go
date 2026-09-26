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

// ScoreBreakdown provides an explainable and reproducible decomposition of a node's placement score.
type ScoreBreakdown struct {
	CPUScore           float64 `json:"cpu_score"`             // 0 to 40 pts (Remaining unallocated CPU ratio)
	MemoryScore        float64 `json:"memory_score"`          // 0 to 40 pts (Remaining unallocated RAM ratio)
	PressureScore      float64 `json:"pressure_score"`        // 0 to 10 pts (Average CPU/Memory pressure inversion)
	TaskCountScore     float64 `json:"task_count_score"`      // 0 to 10 pts (Workload spread factor)
	ServiceSpreadScore float64 `json:"service_spread_score"`  // 0 to 50 pts (Replica anti-affinity spread)
	AffinityScore      float64 `json:"affinity_score"`        // 0 to 20 pts (Tag and label matching bonus)
	NodeStateScore     float64 `json:"node_state_score"`      // 0 to 10 pts (Node readiness and health stability)
	TotalScore         float64 `json:"total_score"`           // Aggregate score
}

// Explanation provides a human-readable explanation of why a worker was selected or scored.
func (b ScoreBreakdown) Explanation() string {
	return fmt.Sprintf("CPU=%.2f, Mem=%.2f, Pressure=%.2f, Tasks=%.2f, ServiceSpread=%.2f, Affinity=%.2f, NodeState=%.2f (Total=%.2f)",
		b.CPUScore, b.MemoryScore, b.PressureScore, b.TaskCountScore, b.ServiceSpreadScore, b.AffinityScore, b.NodeStateScore, b.TotalScore)
}

// NodeEvaluation captures scoring and evaluation details for each candidate worker node.
type NodeEvaluation struct {
	WorkerID  id.ID          `json:"worker_id"`
	Hostname  string         `json:"hostname"`
	Score     float64        `json:"score"`
	Breakdown ScoreBreakdown `json:"breakdown"`
	Feasible  bool           `json:"feasible"`
	Reason    string         `json:"reason,omitempty"`
}

// ScheduleDecision captures the placement decision made by the scheduler with full explainability.
type ScheduleDecision struct {
	TaskID          id.ID                     `json:"task_id"`
	WorkerID        id.ID                     `json:"worker_id"`
	Hostname        string                    `json:"hostname"`
	Score           float64                   `json:"score"`
	Breakdown       ScoreBreakdown            `json:"breakdown"`
	EvaluatedNodes  int                       `json:"evaluated_nodes"`
	FeasibleNodes   int                       `json:"feasible_nodes"`
	RejectedReasons map[string]string         `json:"rejected_reasons,omitempty"` // workerID -> reason
	Evaluations     map[string]NodeEvaluation `json:"evaluations,omitempty"`      // workerID -> evaluation
}

// ScoredWorker wraps a feasible worker with its calculated scheduling score and breakdown.
type ScoredWorker struct {
	Worker    *WorkerCapacity
	Score     float64
	Breakdown ScoreBreakdown
}

// BasicScheduler implements deterministic rule-based scoring and placement.
type BasicScheduler struct{}

// NewBasicScheduler instantiates a new BasicScheduler.
func NewBasicScheduler() *BasicScheduler {
	return &BasicScheduler{}
}

// Schedule performs deterministic multi-factor scheduling:
// 1. Filter unavailable workers (handled by CanFit)
// 2. Filter insufficient resources (handled by CanFit)
// 3. Filter incompatible runtime (handled by CanFit)
// 4. Calculate score for feasible workers with full breakdown
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
	evaluations := make(map[string]NodeEvaluation)
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
			reasonStr := strings.Join(fit.Reasons, "; ")
			rejectedReasons[w.WorkerID.String()] = reasonStr
			evaluations[w.WorkerID.String()] = NodeEvaluation{
				WorkerID: w.WorkerID,
				Hostname: w.Hostname,
				Feasible: false,
				Reason:   reasonStr,
			}
		}
	}

	if len(feasible) == 0 {
		return nil, fmt.Errorf("no feasible workers found for task %s (evaluated %d workers)", req.TaskID, len(workers))
	}

	// 4. Calculate score and breakdown for each feasible worker
	scored := make([]ScoredWorker, len(feasible))
	for i, w := range feasible {
		score, breakdown := s.ScoreWorkerWithBreakdown(w, req)
		scored[i] = ScoredWorker{
			Worker:    w,
			Score:     score,
			Breakdown: breakdown,
		}
		evaluations[w.WorkerID.String()] = NodeEvaluation{
			WorkerID:  w.WorkerID,
			Hostname:  w.Hostname,
			Score:     score,
			Breakdown: breakdown,
			Feasible:  true,
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
		Breakdown:       best.Breakdown,
		EvaluatedNodes:  len(workers),
		FeasibleNodes:   len(feasible),
		RejectedReasons: rejectedReasons,
		Evaluations:     evaluations,
	}, nil
}

// ScoreWorker computes a deterministic placement score for a feasible candidate.
func (s *BasicScheduler) ScoreWorker(w *WorkerCapacity, req *TaskRequirements) float64 {
	score, _ := s.ScoreWorkerWithBreakdown(w, req)
	return score
}

// ScoreWorkerWithBreakdown computes a deterministic multi-factor placement score and returns
// both the aggregate score and the component breakdown.
//
// Factors Considered (Phase 56):
// 1. Available CPU (0 to 40 pts): Ratio of remaining CPU capacity after task allocation.
// 2. Available Memory (0 to 40 pts): Ratio of remaining RAM capacity after task allocation.
// 3. Resource Pressure Inversion (0 to 10 pts): Inverted average allocation pressure on node.
// 4. Existing Task Count (0 to 10 pts): Load spread factor favoring nodes with fewer workloads.
// 5. Service Replica Anti-Affinity Spread (0 to 50 pts): Heavily penalizes co-locating replicas of same service.
// 6. Affinity Tags & Node Labels (0 to 20 pts): Matches requested affinity tags against worker tags & labels.
// 7. Node State & Health Stability (0 to 10 pts): Bonus for READY state.
func (s *BasicScheduler) ScoreWorkerWithBreakdown(w *WorkerCapacity, req *TaskRequirements) (float64, ScoreBreakdown) {
	if w == nil {
		return 0, ScoreBreakdown{}
	}

	// 1. Available CPU Score (normalized, 0 to 40 points)
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
	taskCountScore := 10.0 / (1.0 + float64(w.TaskCount))

	// 5. Service Replica Anti-Affinity Spread (0 to 50 points)
	var serviceSpreadScore float64 = 50.0
	if req != nil && req.ServiceID != "" && len(w.ServiceTaskCounts) > 0 {
		sameServiceReplicas := w.ServiceTaskCounts[req.ServiceID]
		serviceSpreadScore = 50.0 / (1.0 + float64(sameServiceReplicas*5))
	}

	// 6. Affinity Tags & Node Labels Match (0 to 20 points)
	var affinityScore float64
	if req != nil && len(req.AffinityTags) > 0 {
		matchedTags := 0
		workerTagSet := make(map[string]bool, len(w.Tags)+len(w.NodeLabels))
		for _, tag := range w.Tags {
			workerTagSet[strings.ToLower(tag)] = true
		}
		for k, v := range w.NodeLabels {
			workerTagSet[strings.ToLower(k)] = true
			workerTagSet[strings.ToLower(fmt.Sprintf("%s=%s", k, v))] = true
		}

		for _, tag := range req.AffinityTags {
			if workerTagSet[strings.ToLower(tag)] {
				matchedTags++
			}
		}

		if matchedTags > 0 {
			affinityScore = (float64(matchedTags) / float64(len(req.AffinityTags))) * 20.0
		}
	}

	// 7. Node State & Health Stability (0 to 10 points)
	var nodeStateScore float64
	if strings.ToUpper(w.Status) == "READY" {
		nodeStateScore = 10.0
	}

	totalScore := cpuScore + memScore + pressureScore + taskCountScore + serviceSpreadScore + affinityScore + nodeStateScore

	breakdown := ScoreBreakdown{
		CPUScore:           cpuScore,
		MemoryScore:        memScore,
		PressureScore:      pressureScore,
		TaskCountScore:     taskCountScore,
		ServiceSpreadScore: serviceSpreadScore,
		AffinityScore:      affinityScore,
		NodeStateScore:     nodeStateScore,
		TotalScore:         totalScore,
	}

	return totalScore, breakdown
}
