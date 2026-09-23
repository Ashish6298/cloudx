package transitions

import (
	"fmt"
	"sync"

	"github.com/cloudx-org/cloudx/internal/state/models"
)

// AllowedTransitions maps each TaskState to its valid destination states.
var AllowedTransitions = map[models.TaskState]map[models.TaskState]bool{
	models.TaskStatePending: {
		models.TaskStateAssigned: true,
		models.TaskStateStopping: true,
		models.TaskStateStopped:  true,
		models.TaskStateFailed:   true,
		models.TaskStateLost:     true,
	},
	models.TaskStateAssigned: {
		models.TaskStateStarting: true,
		models.TaskStateStopping: true,
		models.TaskStateStopped:  true,
		models.TaskStateFailed:   true,
		models.TaskStateLost:     true,
	},
	models.TaskStateStarting: {
		models.TaskStateRunning:  true,
		models.TaskStateStopping: true,
		models.TaskStateStopped:  true,
		models.TaskStateFailed:   true,
		models.TaskStateLost:     true,
	},
	models.TaskStateRunning: {
		models.TaskStateHealthy:   true,
		models.TaskStateUnhealthy: true,
		models.TaskStateStopping:  true,
		models.TaskStateStopped:   true,
		models.TaskStateFailed:    true,
		models.TaskStateLost:      true,
	},
	models.TaskStateHealthy: {
		models.TaskStateUnhealthy: true,
		models.TaskStateRunning:   true,
		models.TaskStateStopping:  true,
		models.TaskStateStopped:   true,
		models.TaskStateFailed:    true,
		models.TaskStateLost:      true,
	},
	models.TaskStateUnhealthy: {
		models.TaskStateHealthy:  true,
		models.TaskStateRunning:  true,
		models.TaskStateStopping: true,
		models.TaskStateStopped:  true,
		models.TaskStateFailed:   true,
		models.TaskStateLost:     true,
	},
	models.TaskStateStopping: {
		models.TaskStateStopped: true,
		models.TaskStateFailed:  true,
		models.TaskStateLost:    true,
	},
	models.TaskStateStopped: {}, // Terminal
	models.TaskStateFailed:  {}, // Terminal
	models.TaskStateLost:    {}, // Terminal
}

// IsTerminal returns true if the state is terminal (no further transitions permitted).
func IsTerminal(state models.TaskState) bool {
	validTargets, exists := AllowedTransitions[state]
	return !exists || len(validTargets) == 0
}

// Validate checks whether transitioning from current to next is allowed.
func Validate(current, next models.TaskState) error {
	if current == next {
		return nil // idempotent no-op transition
	}

	validNext, ok := AllowedTransitions[current]
	if !ok {
		return fmt.Errorf("unknown source task state: '%s'", current)
	}

	if len(validNext) == 0 {
		return fmt.Errorf("invalid transition: state '%s' is terminal and cannot transition to '%s'", current, next)
	}

	if !validNext[next] {
		return fmt.Errorf("invalid state transition: cannot move task from '%s' to '%s'", current, next)
	}

	return nil
}

// StateMachine manages concurrent, deterministic state transitions for a single task.
type StateMachine struct {
	mu      sync.RWMutex
	current models.TaskState
	history []StateChange
}

// StateChange records a transition event.
type StateChange struct {
	From   models.TaskState
	To     models.TaskState
	Reason string
}

// NewStateMachine creates a new StateMachine with an initial state (defaults to PENDING).
func NewStateMachine(initial models.TaskState) *StateMachine {
	if initial == "" {
		initial = models.TaskStatePending
	}
	return &StateMachine{
		current: initial,
		history: make([]StateChange, 0),
	}
}

// Current returns the current state thread-safely.
func (sm *StateMachine) Current() models.TaskState {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.current
}

// IsTerminal returns true if the machine is currently in a terminal state.
func (sm *StateMachine) IsTerminal() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return IsTerminal(sm.current)
}

// Transition attempts to atomically validate and update the state.
func (sm *StateMachine) Transition(next models.TaskState, reason string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if err := Validate(sm.current, next); err != nil {
		return err
	}

	if sm.current != next {
		sm.history = append(sm.history, StateChange{
			From:   sm.current,
			To:     next,
			Reason: reason,
		})
		sm.current = next
	}

	return nil
}

// History returns a copy of the transition audit trail.
func (sm *StateMachine) History() []StateChange {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	out := make([]StateChange, len(sm.history))
	copy(out, sm.history)
	return out
}
