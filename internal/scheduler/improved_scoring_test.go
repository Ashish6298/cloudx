package scheduler_test

import (
	"context"
	"testing"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/scheduler"
)

// TestImprovedScoring_AllFactors_ExplainableAndReproducible verifies all 7 factors of Phase 56:
// 1. Available CPU
// 2. Available Memory
// 3. Resource Pressure Inversion
// 4. Existing Task Count
// 5. Service Replica Anti-Affinity Spread
// 6. Affinity Tags / Node Labels Match
// 7. Node State
func TestImprovedScoring_AllFactors_ExplainableAndReproducible(t *testing.T) {
	sched := scheduler.NewBasicScheduler()
	serviceID := id.NewServiceID()

	w1ID := id.NewWorkerID()
	w2ID := id.NewWorkerID()

	// Worker 1: High CPU/Mem, 0 tasks, matching affinity tags, READY status
	w1 := &scheduler.WorkerCapacity{
		WorkerID:            w1ID,
		Hostname:            "node-fast-gpu",
		Status:              "READY",
		CPUTotal:            32.0,
		CPUAllocated:        2.0,
		MemoryTotal:         64 * 1024 * 1024 * 1024,
		MemoryAllocated:     4 * 1024 * 1024 * 1024,
		TaskCount:           0,
		Tags:                []string{"gpu", "high-mem"},
		NodeLabels:          map[string]string{"zone": "us-east", "tier": "compute"},
		RuntimeCapabilities: []string{"native", "docker"},
		ServiceTaskCounts:   make(map[id.ID]int),
	}

	// Worker 2: High allocation pressure, 5 tasks, already running 1 replica of same service, no affinity tags
	w2 := &scheduler.WorkerCapacity{
		WorkerID:            w2ID,
		Hostname:            "node-standard",
		Status:              "READY",
		CPUTotal:            8.0,
		CPUAllocated:        6.0,
		MemoryTotal:         16 * 1024 * 1024 * 1024,
		MemoryAllocated:     12 * 1024 * 1024 * 1024,
		TaskCount:           5,
		Tags:                []string{"standard"},
		NodeLabels:          map[string]string{"zone": "us-west"},
		RuntimeCapabilities: []string{"native"},
		ServiceTaskCounts: map[id.ID]int{
			serviceID: 1,
		},
	}

	req := &scheduler.TaskRequirements{
		TaskID:          id.NewTaskID(),
		ServiceID:       serviceID,
		CPU:             2.0,
		Memory:          4 * 1024 * 1024 * 1024,
		RequiredRuntime: "native",
		AffinityTags:    []string{"gpu", "zone=us-east"},
	}

	decision, err := sched.Schedule(context.Background(), req, []*scheduler.WorkerCapacity{w1, w2})
	if err != nil {
		t.Fatalf("Schedule failed: %v", err)
	}

	// 1. Check optimal selection
	if decision.WorkerID != w1ID {
		t.Fatalf("Expected scheduler to select w1 (%s), got %s", w1ID, decision.WorkerID)
	}

	// 2. Validate Explainable Score Breakdown
	bd := decision.Breakdown
	t.Logf("Selected Node Score: %s", bd.Explanation())

	if bd.AffinityScore != 20.0 {
		t.Errorf("Expected full 20.0 affinity points on w1, got %.2f", bd.AffinityScore)
	}
	if bd.ServiceSpreadScore != 50.0 {
		t.Errorf("Expected 50.0 service spread points on w1 with 0 replicas, got %.2f", bd.ServiceSpreadScore)
	}
	if bd.NodeStateScore != 10.0 {
		t.Errorf("Expected 10.0 node state points for READY worker, got %.2f", bd.NodeStateScore)
	}

	// 3. Verify Breakdown exists for all evaluated candidates
	if len(decision.Evaluations) != 2 {
		t.Fatalf("Expected 2 node evaluations, got %d", len(decision.Evaluations))
	}
	evalW2, ok := decision.Evaluations[w2ID.String()]
	if !ok {
		t.Fatalf("Missing evaluation for w2")
	}
	if evalW2.Breakdown.ServiceSpreadScore >= bd.ServiceSpreadScore {
		t.Errorf("Expected w2 service spread score (%.2f) to be lower than w1 (%.2f)",
			evalW2.Breakdown.ServiceSpreadScore, bd.ServiceSpreadScore)
	}
	if evalW2.Breakdown.AffinityScore != 0 {
		t.Errorf("Expected 0 affinity points on w2, got %.2f", evalW2.Breakdown.AffinityScore)
	}

	// 4. Test Determinism & Reproducibility (repeat 100 times)
	for i := 0; i < 100; i++ {
		repeatDecision, err := sched.Schedule(context.Background(), req, []*scheduler.WorkerCapacity{w2, w1})
		if err != nil {
			t.Fatalf("Repeat %d failed: %v", i, err)
		}
		if repeatDecision.WorkerID != w1ID {
			t.Fatalf("Non-deterministic placement at iteration %d: got %s, expected %s",
				i, repeatDecision.WorkerID, w1ID)
		}
		if repeatDecision.Score != decision.Score {
			t.Fatalf("Non-reproducible score at iteration %d: got %.4f, expected %.4f",
				i, repeatDecision.Score, decision.Score)
		}
	}
}

// TestImprovedScoring_AffinityBonus verifies tag and label matching scoring
func TestImprovedScoring_AffinityBonus(t *testing.T) {
	sched := scheduler.NewBasicScheduler()

	w1 := &scheduler.WorkerCapacity{
		WorkerID:            id.NewWorkerID(),
		Hostname:            "node-tagged",
		Status:              "READY",
		CPUTotal:            8.0,
		MemoryTotal:         16 * 1024 * 1024 * 1024,
		Tags:                []string{"ssd", "nvme"},
		NodeLabels:          map[string]string{"env": "prod"},
		RuntimeCapabilities: []string{"native"},
	}

	w2 := &scheduler.WorkerCapacity{
		WorkerID:            id.NewWorkerID(),
		Hostname:            "node-untagged",
		Status:              "READY",
		CPUTotal:            8.0,
		MemoryTotal:         16 * 1024 * 1024 * 1024,
		Tags:                []string{"hdd"},
		NodeLabels:          map[string]string{"env": "dev"},
		RuntimeCapabilities: []string{"native"},
	}

	req := &scheduler.TaskRequirements{
		TaskID:          id.NewTaskID(),
		CPU:             1.0,
		Memory:          1024 * 1024 * 1024,
		RequiredRuntime: "native",
		AffinityTags:    []string{"ssd", "env=prod"},
	}

	score1, bd1 := sched.ScoreWorkerWithBreakdown(w1, req)
	score2, bd2 := sched.ScoreWorkerWithBreakdown(w2, req)

	if bd1.AffinityScore != 20.0 {
		t.Errorf("Expected 20.0 affinity points on w1, got %.2f", bd1.AffinityScore)
	}
	if bd2.AffinityScore != 0.0 {
		t.Errorf("Expected 0.0 affinity points on w2, got %.2f", bd2.AffinityScore)
	}
	if score1 <= score2 {
		t.Errorf("Expected tagged node score (%.2f) to exceed untagged node score (%.2f)", score1, score2)
	}
}
