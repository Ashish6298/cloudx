package transitions

import (
	"fmt"
	"sync"
	"testing"

	"github.com/cloudx-org/cloudx/internal/state/models"
)

func TestValidTransitions(t *testing.T) {
	validSequences := [][]models.TaskState{
		// Happy path 1: PENDING -> ASSIGNED -> STARTING -> RUNNING -> HEALTHY -> STOPPING -> STOPPED
		{
			models.TaskStatePending,
			models.TaskStateAssigned,
			models.TaskStateStarting,
			models.TaskStateRunning,
			models.TaskStateHealthy,
			models.TaskStateStopping,
			models.TaskStateStopped,
		},
		// Unhealthy recovery path: RUNNING -> UNHEALTHY -> HEALTHY -> RUNNING
		{
			models.TaskStatePending,
			models.TaskStateAssigned,
			models.TaskStateStarting,
			models.TaskStateRunning,
			models.TaskStateUnhealthy,
			models.TaskStateHealthy,
			models.TaskStateRunning,
		},
		// Failure path: PENDING -> ASSIGNED -> LOST
		{
			models.TaskStatePending,
			models.TaskStateAssigned,
			models.TaskStateLost,
		},
		// Runtime failure: RUNNING -> FAILED
		{
			models.TaskStatePending,
			models.TaskStateAssigned,
			models.TaskStateStarting,
			models.TaskStateRunning,
			models.TaskStateFailed,
		},
	}

	for i, seq := range validSequences {
		sm := NewStateMachine(seq[0])
		for j := 1; j < len(seq); j++ {
			if err := sm.Transition(seq[j], fmt.Sprintf("step %d", j)); err != nil {
				t.Fatalf("sequence [%d] step [%d] (%s -> %s) failed: %v", i, j, seq[j-1], seq[j], err)
			}
			if sm.Current() != seq[j] {
				t.Errorf("expected state %s, got %s", seq[j], sm.Current())
			}
		}
	}
}

func TestInvalidTransitions(t *testing.T) {
	invalidTransitions := []struct {
		from models.TaskState
		to   models.TaskState
	}{
		{models.TaskStatePending, models.TaskStateRunning},    // Cannot skip ASSIGNED/STARTING
		{models.TaskStatePending, models.TaskStateHealthy},    // Cannot jump to HEALTHY directly
		{models.TaskStateAssigned, models.TaskStateHealthy},   // Must be STARTING/RUNNING first
		{models.TaskStateStarting, models.TaskStateHealthy},   // Must reach RUNNING before HEALTHY
		{models.TaskStateStopping, models.TaskStateRunning},   // Cannot go backwards from STOPPING to RUNNING
		{models.TaskStateStopping, models.TaskStateHealthy},   // Cannot go backwards from STOPPING to HEALTHY
	}

	for _, tc := range invalidTransitions {
		err := Validate(tc.from, tc.to)
		if err == nil {
			t.Errorf("expected error for invalid transition %s -> %s, got nil", tc.from, tc.to)
		}
	}
}

func TestTerminalStates(t *testing.T) {
	terminalStates := []models.TaskState{
		models.TaskStateStopped,
		models.TaskStateLost,
	}

	for _, ts := range terminalStates {
		if !IsTerminal(ts) {
			t.Errorf("expected %s to be recognized as terminal", ts)
		}

		// Attempting any transition from a terminal state must be rejected
		targets := []models.TaskState{
			models.TaskStatePending,
			models.TaskStateAssigned,
			models.TaskStateStarting,
			models.TaskStateRunning,
			models.TaskStateHealthy,
			models.TaskStateUnhealthy,
			models.TaskStateStopping,
		}

		for _, target := range targets {
			err := Validate(ts, target)
			if err == nil {
				t.Errorf("expected terminal state %s to reject transition to %s, got nil", ts, target)
			}
		}
	}
}

func TestConcurrentTransitionAttempts(t *testing.T) {
	sm := NewStateMachine(models.TaskStateRunning)
	const goroutines = 50

	var wg sync.WaitGroup
	var successCount int
	var mu sync.Mutex

	// Concurrent race to transition from RUNNING to STOPPING vs HEALTHY
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			var target models.TaskState
			if idx%2 == 0 {
				target = models.TaskStateHealthy
			} else {
				target = models.TaskStateStopping
			}

			err := sm.Transition(target, fmt.Sprintf("goroutine %d", idx))
			if err == nil {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}(i)
	}

	wg.Wait()

	curr := sm.Current()
	if curr != models.TaskStateHealthy && curr != models.TaskStateStopping {
		t.Errorf("unexpected terminal state after concurrency: %s", curr)
	}

	history := sm.History()
	if len(history) == 0 {
		t.Errorf("expected non-empty transition history")
	}
}
