package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cloudx-org/cloudx/internal/auth"
	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/state"
	"github.com/cloudx-org/cloudx/internal/state/models"
)

// Standard Event Types for CloudX Cluster Actions.
const (
	// Worker Lifecycle Events
	EventWorkerRegistered = "WORKER_REGISTERED"
	EventWorkerLost       = "WORKER_LOST"
	EventWorkerStatus     = "WORKER_STATUS_CHANGED"

	// Service Lifecycle Events
	EventServiceCreated = "SERVICE_CREATED"
	EventServiceUpdated = "SERVICE_UPDATED"
	EventServiceScaled  = "SERVICE_SCALED"
	EventServiceDeleted = "SERVICE_DELETED"

	// Deployment Lifecycle Events
	EventDeploymentStarted    = "DEPLOYMENT_STARTED"
	EventDeploymentCompleted  = "DEPLOYMENT_COMPLETED"
	EventDeploymentFailed     = "DEPLOYMENT_FAILED"
	EventDeploymentRolledBack = "DEPLOYMENT_ROLLED_BACK"

	// Task & Process Lifecycle Events
	EventTaskAssigned        = "TASK_ASSIGNED"
	EventTaskStarting        = "TASK_STARTING"
	EventProcessStarted      = "PROCESS_STARTED"
	EventProcessStopped      = "PROCESS_STOPPED"
	EventProcessCrashed      = "PROCESS_CRASHED"
	EventHealthCheckHealthy  = "HEALTH_CHECK_HEALTHY"
	EventHealthCheckFailed   = "HEALTH_CHECK_FAILED"
	EventTaskRescheduled     = "TASK_RESCHEDULED"
	EventTaskCrashLoop       = "TASK_CRASH_LOOP"

	// Simulation Events
	EventSimulationTriggered = "SIMULATION_TRIGGERED"
)

// EventFilter specifies search and query criteria for cluster events.
type EventFilter struct {
	Type      string    `json:"type,omitempty"`
	Source    string    `json:"source,omitempty"`
	EntityID  id.ID     `json:"entity_id,omitempty"`
	ServiceID id.ID     `json:"service_id,omitempty"`
	Since     time.Time `json:"since,omitempty"`
	Limit     int       `json:"limit,omitempty"`
}

// EventRecorder provides a high-level API to publish append-only, strongly-typed cluster events.
type EventRecorder interface {
	Record(ctx context.Context, eventType string, source string, entityID id.ID, payload any) (*models.Event, error)
	List(ctx context.Context, filter EventFilter) ([]*models.Event, error)
	ListByEntity(ctx context.Context, entityID id.ID) ([]*models.Event, error)
}

// Recorder implements EventRecorder backed by a state.Store.
type Recorder struct {
	store  state.Store
	logger logging.Logger
}

// NewRecorder constructs a new persistent EventRecorder.
func NewRecorder(store state.Store, logger logging.Logger) *Recorder {
	if logger == nil {
		logger = logging.NewDefaultLogger()
	}
	return &Recorder{
		store:  store,
		logger: logger.With("component", "event_recorder"),
	}
}

// Record persists an append-only event record into the state store.
func (r *Recorder) Record(ctx context.Context, eventType string, source string, entityID id.ID, payload any) (*models.Event, error) {
	if r.store == nil {
		return nil, fmt.Errorf("state store is nil")
	}

	var payloadStr string
	if payload != nil {
		switch p := payload.(type) {
		case string:
			payloadStr = p
		case []byte:
			payloadStr = string(p)
		default:
			bytes, err := json.Marshal(p)
			if err != nil {
				payloadStr = fmt.Sprintf("%v", p)
			} else {
				payloadStr = string(bytes)
			}
		}
	}

	// Mask any sensitive credentials, tokens, or environment secrets in the event payload
	payloadStr = auth.RedactJSONPayload(payloadStr)

	event := &models.Event{
		ID:        id.NewEventID(),
		Type:      eventType,
		Source:    source,
		EntityID:  entityID,
		Payload:   payloadStr,
		CreatedAt: time.Now().UTC(),
	}

	if err := r.store.Events().Append(ctx, event); err != nil {
		r.logger.Warn("Failed to append event %s for entity %s: %v", eventType, entityID, err)
		return nil, fmt.Errorf("failed to append event: %w", err)
	}

	r.logger.Debug("Recorded cluster event: [%s] source=%s entity=%s", eventType, source, entityID)
	return event, nil
}

// List queries events matching the given filter.
func (r *Recorder) List(ctx context.Context, filter EventFilter) ([]*models.Event, error) {
	if r.store == nil {
		return nil, fmt.Errorf("state store is nil")
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}

	events, err := r.store.Events().List(ctx, limit)
	if err != nil {
		return nil, err
	}

	var filtered []*models.Event
	for _, e := range events {
		if filter.Type != "" && e.Type != filter.Type {
			continue
		}
		if filter.Source != "" && e.Source != filter.Source {
			continue
		}
		if filter.EntityID != "" && e.EntityID != filter.EntityID {
			continue
		}
		if !filter.Since.IsZero() && e.CreatedAt.Before(filter.Since) {
			continue
		}
		filtered = append(filtered, e)
	}

	return filtered, nil
}

// ListByEntity retrieves all events relating to a given entity (node, worker, service, deployment, task).
func (r *Recorder) ListByEntity(ctx context.Context, entityID id.ID) ([]*models.Event, error) {
	if r.store == nil {
		return nil, fmt.Errorf("state store is nil")
	}
	return r.store.Events().ListByEntity(ctx, entityID)
}
