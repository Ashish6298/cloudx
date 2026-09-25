package models

import (
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
)

func TestJobRecord_Validate(t *testing.T) {
	job := &JobRecord{
		ID:    id.NewJobID(),
		Name:  "batch-cleanup",
		State: JobStatePending,
		Config: JobConfig{
			Command: "rm -rf /tmp/cache",
			Args:    []string{"--dry-run"},
			Environment: map[string]string{
				"ENV": "production",
			},
			Runtime: "native",
			Resources: ResourceRequirements{
				CPU:    1.0,
				Memory: 512 * 1024 * 1024,
			},
			RetryPolicy: JobRetryPolicy{
				MaxRetries:    2,
				BackoffPeriod: 10 * time.Second,
			},
			Timeout: 15 * time.Minute,
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	if err := job.Validate(); err != nil {
		t.Fatalf("expected valid job, got: %v", err)
	}

	hash := job.Config.ComputeHash()
	if hash == "" {
		t.Fatalf("expected non-empty config hash")
	}

	// Determinism check
	hash2 := job.Config.ComputeHash()
	if hash != hash2 {
		t.Fatalf("expected identical config hash for identical configuration")
	}
}

func TestJobRecord_Transitions(t *testing.T) {
	job := &JobRecord{
		ID:    id.NewJobID(),
		Name:  "etl-job",
		State: JobStatePending,
		Config: JobConfig{
			Command: "python run.py",
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	// 1. PENDING -> ASSIGNED
	if err := job.Transition(JobStateAssigned); err != nil {
		t.Fatalf("unexpected transition error: %v", err)
	}
	if job.State != JobStateAssigned {
		t.Fatalf("expected ASSIGNED, got %s", job.State)
	}

	// 2. ASSIGNED -> RUNNING
	if err := job.Transition(JobStateRunning); err != nil {
		t.Fatalf("unexpected transition error: %v", err)
	}
	if job.State != JobStateRunning || job.StartedAt.IsZero() {
		t.Fatalf("expected RUNNING with non-zero StartedAt")
	}

	// 3. RUNNING -> SUCCEEDED (Terminal)
	if err := job.Transition(JobStateSucceeded); err != nil {
		t.Fatalf("unexpected transition error: %v", err)
	}
	if job.State != JobStateSucceeded || job.CompletedAt.IsZero() {
		t.Fatalf("expected SUCCEEDED with non-zero CompletedAt")
	}

	// 4. SUCCEEDED -> RUNNING (Invalid terminal transition)
	if err := job.Transition(JobStateRunning); err == nil {
		t.Fatalf("expected error transitioning from SUCCEEDED terminal state")
	}
}

func TestJobRecord_RetryTransitions(t *testing.T) {
	job := &JobRecord{
		ID:    id.NewJobID(),
		Name:  "flaky-job",
		State: JobStateRunning,
		Config: JobConfig{
			Command: "curl https://example.com/api",
		},
	}

	// RUNNING -> FAILED
	if err := job.Transition(JobStateFailed); err != nil {
		t.Fatalf("unexpected transition error: %v", err)
	}

	// FAILED -> PENDING (Retry attempt)
	if err := job.Transition(JobStatePending); err != nil {
		t.Fatalf("unexpected retry transition error: %v", err)
	}
	if job.State != JobStatePending {
		t.Fatalf("expected PENDING, got %s", job.State)
	}
}

func TestJobRecord_Serialization(t *testing.T) {
	job := &JobRecord{
		ID:    id.NewJobID(),
		Name:  "report-generator",
		State: JobStatePending,
		Config: JobConfig{
			Command: "report-gen",
			Timeout: 5 * time.Minute,
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	specJSON, err := job.ToSpecJSON()
	if err != nil {
		t.Fatalf("failed to serialize job: %v", err)
	}

	genericModel := &Job{
		ID:        job.ID,
		Name:      job.Name,
		Command:   job.Config.Command,
		Status:    string(job.State),
		SpecJSON:  specJSON,
		CreatedAt: job.CreatedAt,
		UpdatedAt: job.UpdatedAt,
	}

	deserialized, err := JobFromModel(genericModel)
	if err != nil {
		t.Fatalf("failed to deserialize job: %v", err)
	}

	if deserialized.Name != job.Name || deserialized.Config.Timeout != 5*time.Minute {
		t.Fatalf("deserialized job does not match original: %+v", deserialized)
	}
}
