package models

import (
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
)

// Node represents a machine in the CloudX cluster.
type Node struct {
	ID        id.ID     `json:"id"`
	Name      string    `json:"name"`
	Address   string    `json:"address"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Worker represents a compute worker daemon.
type Worker struct {
	ID        id.ID     `json:"id"`
	NodeID    id.ID     `json:"node_id"`
	Address   string    `json:"address"`
	Status    string    `json:"status"`
	Heartbeat time.Time `json:"heartbeat"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Service represents a long-running replicated workload.
type Service struct {
	ID        id.ID     `json:"id"`
	Name      string    `json:"name"`
	Replicas  int       `json:"replicas"`
	Runtime   string    `json:"runtime"`
	Command   string    `json:"command"`
	Status    string    `json:"status"`
	SpecJSON  string    `json:"spec_json"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Deployment represents an immutable version rollout of a service.
type Deployment struct {
	ID        id.ID     `json:"id"`
	ServiceID id.ID     `json:"service_id"`
	Version   string    `json:"version"`
	Status    string    `json:"status"`
	SpecJSON  string    `json:"spec_json"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Task represents a running unit of execution assigned to a worker.
type Task struct {
	ID           id.ID     `json:"id"`
	ServiceID    id.ID     `json:"service_id,omitempty"`
	JobID        id.ID     `json:"job_id,omitempty"`
	DeploymentID id.ID     `json:"deployment_id,omitempty"`
	WorkerID     id.ID     `json:"worker_id"`
	State        string    `json:"state"`
	PID          int       `json:"pid"`
	ExitCode     int       `json:"exit_code"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Job represents a finite batch workload.
type Job struct {
	ID        id.ID     `json:"id"`
	Name      string    `json:"name"`
	Command   string    `json:"command"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Volume represents persistent storage attached to workloads.
type Volume struct {
	ID        id.ID     `json:"id"`
	Name      string    `json:"name"`
	WorkerID  id.ID     `json:"worker_id"`
	Path      string    `json:"path"`
	Driver    string    `json:"driver"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Network represents a logical network segment.
type Network struct {
	ID        id.ID     `json:"id"`
	Name      string    `json:"name"`
	Subnet    string    `json:"subnet"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Event represents an immutable cluster audit trail entry.
type Event struct {
	ID        id.ID     `json:"id"`
	Type      string    `json:"type"`
	Source    string    `json:"source"`
	EntityID  id.ID     `json:"entity_id"`
	Payload   string    `json:"payload"`
	CreatedAt time.Time `json:"created_at"`
}
