package state

import (
	"context"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/state/models"
)

// Store defines the central persistence interface for CloudX state.
type Store interface {
	Nodes() NodeRepository
	Workers() WorkerRepository
	Services() ServiceRepository
	Deployments() DeploymentRepository
	Tasks() TaskRepository
	Jobs() JobRepository
	Volumes() VolumeRepository
	Networks() NetworkRepository
	Events() EventRepository
	DesiredState() DesiredStateStore

	// Transaction executes a function within a database transaction.
	Transaction(ctx context.Context, fn func(tx Store) error) error

	// Close shuts down the state store cleanly.
	Close() error
}

// DesiredStateStore manages persistence of ServiceDesiredState.
type DesiredStateStore interface {
	Create(ctx context.Context, state *models.ServiceDesiredState) error
	Get(ctx context.Context, id id.ID) (*models.ServiceDesiredState, error)
	GetByName(ctx context.Context, name string) (*models.ServiceDesiredState, error)
	List(ctx context.Context) ([]*models.ServiceDesiredState, error)
	Update(ctx context.Context, state *models.ServiceDesiredState) error
	Delete(ctx context.Context, id id.ID) error
}

// NodeRepository handles Node persistence.
type NodeRepository interface {
	Create(ctx context.Context, n *models.Node) error
	Get(ctx context.Context, id id.ID) (*models.Node, error)
	List(ctx context.Context) ([]*models.Node, error)
	Update(ctx context.Context, n *models.Node) error
	Delete(ctx context.Context, id id.ID) error
}

// WorkerRepository handles Worker persistence.
type WorkerRepository interface {
	Create(ctx context.Context, w *models.Worker) error
	Get(ctx context.Context, id id.ID) (*models.Worker, error)
	List(ctx context.Context) ([]*models.Worker, error)
	Update(ctx context.Context, w *models.Worker) error
	Delete(ctx context.Context, id id.ID) error
}

// ServiceRepository handles Service persistence.
type ServiceRepository interface {
	Create(ctx context.Context, s *models.Service) error
	Get(ctx context.Context, id id.ID) (*models.Service, error)
	GetByName(ctx context.Context, name string) (*models.Service, error)
	List(ctx context.Context) ([]*models.Service, error)
	Update(ctx context.Context, s *models.Service) error
	Delete(ctx context.Context, id id.ID) error
}

// DeploymentRepository handles Deployment persistence.
type DeploymentRepository interface {
	Create(ctx context.Context, d *models.Deployment) error
	Get(ctx context.Context, id id.ID) (*models.Deployment, error)
	ListByService(ctx context.Context, serviceID id.ID) ([]*models.Deployment, error)
	Update(ctx context.Context, d *models.Deployment) error
	Delete(ctx context.Context, id id.ID) error
}

// TaskRepository handles Task persistence.
type TaskRepository interface {
	Create(ctx context.Context, t *models.Task) error
	Get(ctx context.Context, id id.ID) (*models.Task, error)
	List(ctx context.Context) ([]*models.Task, error)
	ListByService(ctx context.Context, serviceID id.ID) ([]*models.Task, error)
	ListByWorker(ctx context.Context, workerID id.ID) ([]*models.Task, error)
	Update(ctx context.Context, t *models.Task) error
	Delete(ctx context.Context, id id.ID) error
}

// JobRepository handles Job persistence.
type JobRepository interface {
	Create(ctx context.Context, j *models.Job) error
	Get(ctx context.Context, id id.ID) (*models.Job, error)
	List(ctx context.Context) ([]*models.Job, error)
	Update(ctx context.Context, j *models.Job) error
	Delete(ctx context.Context, id id.ID) error
}

// VolumeRepository handles Volume persistence.
type VolumeRepository interface {
	Create(ctx context.Context, v *models.Volume) error
	Get(ctx context.Context, id id.ID) (*models.Volume, error)
	List(ctx context.Context) ([]*models.Volume, error)
	Update(ctx context.Context, v *models.Volume) error
	Delete(ctx context.Context, id id.ID) error
}

// NetworkRepository handles Network persistence.
type NetworkRepository interface {
	Create(ctx context.Context, n *models.Network) error
	Get(ctx context.Context, id id.ID) (*models.Network, error)
	List(ctx context.Context) ([]*models.Network, error)
	Update(ctx context.Context, n *models.Network) error
	Delete(ctx context.Context, id id.ID) error
}

// EventRepository handles Event append and query.
type EventRepository interface {
	Append(ctx context.Context, e *models.Event) error
	Get(ctx context.Context, id id.ID) (*models.Event, error)
	List(ctx context.Context, limit int) ([]*models.Event, error)
	ListByEntity(ctx context.Context, entityID id.ID) ([]*models.Event, error)
}
