package models

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
)

// JobState represents the execution lifecycle states of a finite job.
type JobState string

const (
	JobStatePending   JobState = "PENDING"
	JobStateAssigned  JobState = "ASSIGNED"
	JobStateRunning   JobState = "RUNNING"
	JobStateSucceeded JobState = "SUCCEEDED"
	JobStateFailed    JobState = "FAILED"
	JobStateCancelled JobState = "CANCELLED"
)

// ValidJobStates defines all valid job lifecycle states.
var ValidJobStates = map[JobState]bool{
	JobStatePending:   true,
	JobStateAssigned:  true,
	JobStateRunning:   true,
	JobStateSucceeded: true,
	JobStateFailed:    true,
	JobStateCancelled: true,
}

// IsTerminalJobState returns true if the job state cannot transition further.
func IsTerminalJobState(state JobState) bool {
	return state == JobStateSucceeded || state == JobStateFailed || state == JobStateCancelled
}

// JobRetryPolicy defines retry behavior for failed job executions.
type JobRetryPolicy struct {
	MaxRetries    int           `json:"max_retries" yaml:"max_retries"`
	BackoffPeriod time.Duration `json:"backoff_period,omitempty" yaml:"backoff_period,omitempty"`
}

// JobConfig defines the execution specification of a finite batch job workload.
type JobConfig struct {
	Command     string               `json:"command" yaml:"command"`
	Args        []string             `json:"args,omitempty" yaml:"args,omitempty"`
	Environment map[string]string    `json:"environment,omitempty" yaml:"environment,omitempty"`
	WorkingDir  string               `json:"working_dir,omitempty" yaml:"working_dir,omitempty"`
	Runtime     string               `json:"runtime,omitempty" yaml:"runtime,omitempty"`
	Resources   ResourceRequirements `json:"resources" yaml:"resources"`
	RetryPolicy JobRetryPolicy       `json:"retry_policy" yaml:"retry_policy"`
	Timeout     time.Duration        `json:"timeout,omitempty" yaml:"timeout,omitempty"` // e.g. 5m, 1h
}

// ComputeHash calculates a deterministic SHA-256 fingerprint for the job configuration.
func (c *JobConfig) ComputeHash() string {
	keys := make([]string, 0, len(c.Environment))
	for k := range c.Environment {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	type sortedEnvPair struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	sortedEnv := make([]sortedEnvPair, 0, len(keys))
	for _, k := range keys {
		sortedEnv = append(sortedEnv, sortedEnvPair{Key: k, Value: c.Environment[k]})
	}

	raw := struct {
		Command     string               `json:"command"`
		Args        []string             `json:"args,omitempty"`
		Environment []sortedEnvPair      `json:"environment,omitempty"`
		WorkingDir  string               `json:"working_dir,omitempty"`
		Runtime     string               `json:"runtime,omitempty"`
		Resources   ResourceRequirements `json:"resources"`
		RetryPolicy JobRetryPolicy       `json:"retry_policy"`
		Timeout     time.Duration        `json:"timeout,omitempty"`
	}{
		Command:     c.Command,
		Args:        c.Args,
		Environment: sortedEnv,
		WorkingDir:  c.WorkingDir,
		Runtime:     c.Runtime,
		Resources:   c.Resources,
		RetryPolicy: c.RetryPolicy,
		Timeout:     c.Timeout,
	}

	b, _ := json.Marshal(raw)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// JobRecord represents the full model for a finite workload in CloudX.
type JobRecord struct {
	ID          id.ID     `json:"id"`
	Name        string    `json:"name"`
	State       JobState  `json:"state"`
	Config      JobConfig `json:"config"`
	ConfigHash  string    `json:"config_hash"`
	AssignedTo  id.ID     `json:"assigned_to,omitempty"` // Worker ID when assigned
	TaskID      id.ID     `json:"task_id,omitempty"`     // Associated execution task ID
	RetryCount  int       `json:"retry_count"`
	ExitCode    int       `json:"exit_code"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Validate ensures all required job fields are provided and valid.
func (j *JobRecord) Validate() error {
	var errs []string

	if strings.TrimSpace(j.ID.String()) == "" {
		errs = append(errs, "job id is required")
	}
	if strings.TrimSpace(j.Name) == "" {
		errs = append(errs, "job name is required")
	}
	if strings.TrimSpace(j.Config.Command) == "" {
		errs = append(errs, "job command is required")
	}
	if !ValidJobStates[j.State] {
		errs = append(errs, fmt.Sprintf("invalid job state '%s'", j.State))
	}
	if j.Config.RetryPolicy.MaxRetries < 0 {
		errs = append(errs, "max_retries cannot be negative")
	}
	if j.Config.RetryPolicy.BackoffPeriod < 0 {
		errs = append(errs, "retry backoff_period cannot be negative")
	}
	if j.Config.Timeout < 0 {
		errs = append(errs, "job timeout cannot be negative")
	}

	rt := strings.ToLower(j.Config.Runtime)
	if rt != "" && rt != "native" && rt != "docker" {
		errs = append(errs, fmt.Sprintf("invalid runtime '%s', must be 'native' or 'docker'", j.Config.Runtime))
	}

	if len(errs) > 0 {
		return fmt.Errorf("invalid job: %s", strings.Join(errs, "; "))
	}
	return nil
}

// AllowedJobTransitions defines valid state transitions for finite jobs.
var AllowedJobTransitions = map[JobState]map[JobState]bool{
	JobStatePending: {
		JobStateAssigned:  true,
		JobStateCancelled: true,
		JobStateFailed:    true,
	},
	JobStateAssigned: {
		JobStateRunning:   true,
		JobStateCancelled: true,
		JobStateFailed:    true,
	},
	JobStateRunning: {
		JobStateSucceeded: true,
		JobStateFailed:    true,
		JobStateCancelled: true,
	},
	JobStateSucceeded: {}, // Terminal
	JobStateFailed: {
		JobStatePending: true, // When retrying
	},
	JobStateCancelled: {}, // Terminal
}

// ValidateJobTransition verifies if transitioning from current to next job state is legal.
func ValidateJobTransition(current, next JobState) error {
	if current == next {
		return nil
	}
	validTargets, exists := AllowedJobTransitions[current]
	if !exists {
		return fmt.Errorf("unknown source job state: '%s'", current)
	}
	if len(validTargets) == 0 {
		return fmt.Errorf("invalid transition: state '%s' is terminal and cannot transition to '%s'", current, next)
	}
	if !validTargets[next] {
		return fmt.Errorf("invalid job state transition: cannot move job from '%s' to '%s'", current, next)
	}
	return nil
}

// Transition moves the job to the next state if valid.
func (j *JobRecord) Transition(next JobState) error {
	if err := ValidateJobTransition(j.State, next); err != nil {
		return err
	}
	j.State = next
	j.UpdatedAt = time.Now().UTC()
	if next == JobStateRunning && j.StartedAt.IsZero() {
		j.StartedAt = j.UpdatedAt
	}
	if IsTerminalJobState(next) && j.CompletedAt.IsZero() {
		j.CompletedAt = j.UpdatedAt
	}
	return nil
}

// ToSpecJSON marshals JobConfig to JSON for DB storage.
func (j *JobRecord) ToSpecJSON() (string, error) {
	b, err := json.Marshal(j)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// JobFromModel converts a generic models.Job into a JobRecord.
func JobFromModel(m *Job) (*JobRecord, error) {
	if m == nil {
		return nil, fmt.Errorf("nil job model")
	}

	var rec JobRecord
	if m.SpecJSON != "" {
		if err := json.Unmarshal([]byte(m.SpecJSON), &rec); err == nil && rec.Name != "" {
			rec.ID = m.ID
			if rec.State == "" {
				rec.State = JobState(m.Status)
			}
			if rec.CreatedAt.IsZero() {
				rec.CreatedAt = m.CreatedAt
			}
			rec.UpdatedAt = m.UpdatedAt
			return &rec, nil
		}
	}

	// Fallback from raw Job fields
	rec.ID = m.ID
	rec.Name = m.Name
	rec.State = JobState(m.Status)
	rec.Config = JobConfig{
		Command: m.Command,
	}
	rec.ConfigHash = rec.Config.ComputeHash()
	rec.CreatedAt = m.CreatedAt
	rec.UpdatedAt = m.UpdatedAt
	return &rec, nil
}
