package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/scheduler"
	"github.com/cloudx-org/cloudx/internal/spec"
	"github.com/cloudx-org/cloudx/internal/state/models"
)

// DeployResult captures the outcome of deploying a service.
type DeployResult struct {
	ServiceID    id.ID                         `json:"service_id"`
	DeploymentID id.ID                         `json:"deployment_id"`
	ServiceName  string                        `json:"service_name"`
	Replicas     int                           `json:"replicas"`
	Status       string                        `json:"status"`
	Tasks        []*scheduler.AssignmentResult `json:"tasks,omitempty"`
	CreatedAt    time.Time                     `json:"created_at"`
}

// DeployService validates, persists desired service state, rolls out a deployment, and schedules replicas.
func (cp *ControlPlane) DeployService(ctx context.Context, svcConfig *spec.ServiceConfig, dispatcher scheduler.Dispatcher) (*DeployResult, error) {
	if svcConfig == nil {
		return nil, fmt.Errorf("service configuration is nil")
	}

	// 1. Validate Service Configuration
	parsedRes, err := svcConfig.Validate()
	if err != nil {
		return nil, fmt.Errorf("invalid service configuration: %w", err)
	}

	store := cp.StateManager.Store()
	if store == nil {
		return nil, fmt.Errorf("state store is not available")
	}

	now := time.Now().UTC()
	replicas := 1
	if svcConfig.Replicas != nil {
		replicas = *svcConfig.Replicas
	}

	// Convert config to JSON for spec_json persistence
	specBytes, err := json.Marshal(svcConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize spec: %w", err)
	}
	specJSON := string(specBytes)

	// 2. Persist Desired State in Services repository
	var serviceID id.ID
	existingServices, err := store.Services().List(ctx)
	if err == nil {
		for _, s := range existingServices {
			if s.Name == svcConfig.Name {
				serviceID = s.ID
				break
			}
		}
	}

	if serviceID == "" {
		serviceID = id.NewServiceID()
		svcRecord := &models.Service{
			ID:        serviceID,
			Name:      svcConfig.Name,
			Replicas:  replicas,
			Runtime:   svcConfig.Runtime,
			Command:   svcConfig.Command,
			Status:    "PENDING",
			SpecJSON:  specJSON,
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := store.Services().Create(ctx, svcRecord); err != nil {
			return nil, fmt.Errorf("failed to create service record: %w", err)
		}
	} else {
		svcRecord, err := store.Services().Get(ctx, serviceID)
		if err != nil {
			return nil, fmt.Errorf("failed to get existing service: %w", err)
		}
		svcRecord.Replicas = replicas
		svcRecord.Runtime = svcConfig.Runtime
		svcRecord.Command = svcConfig.Command
		svcRecord.SpecJSON = specJSON
		svcRecord.Status = "UPDATING"
		svcRecord.UpdatedAt = now
		if err := store.Services().Update(ctx, svcRecord); err != nil {
			return nil, fmt.Errorf("failed to update service record: %w", err)
		}
	}

	// 3. Create Deployment Record
	deploymentID := id.NewDeploymentID()
	deploymentRecord := &models.Deployment{
		ID:        deploymentID,
		ServiceID: serviceID,
		Version:   fmt.Sprintf("v-%d", now.Unix()),
		Status:    "IN_PROGRESS",
		SpecJSON:  specJSON,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.Deployments().Create(ctx, deploymentRecord); err != nil {
		return nil, fmt.Errorf("failed to create deployment record: %w", err)
	}

	// 4. Schedule & Assign Replicas
	var assignedTasks []*scheduler.AssignmentResult
	if replicas > 0 {
		sched := scheduler.NewBasicScheduler()
		coordinator := scheduler.NewAssignmentCoordinator(store, sched, dispatcher, cp.logger)

		for i := 0; i < replicas; i++ {
			taskID := id.NewTaskID()
			assignRes, err := coordinator.Assign(ctx, scheduler.AssignOptions{
				TaskID:       taskID,
				ServiceID:    serviceID,
				DeploymentID: deploymentID,
				Requirements: &scheduler.TaskRequirements{
					CPU:             parsedRes.CPUCores,
					Memory:          parsedRes.MemoryBytes,
					RequiredRuntime: svcConfig.Runtime,
				},
				Spec: scheduler.TaskSpec{
					Command:       svcConfig.Command,
					Args:          svcConfig.Args,
					Environment:   svcConfig.Environment,
					WorkingDir:    svcConfig.WorkingDir,
					Runtime:       svcConfig.Runtime,
					RestartPolicy: models.RestartPolicy{
						Type: func() models.RestartPolicyType {
							if svcConfig.RestartPolicy != nil {
								return models.RestartPolicyType(svcConfig.RestartPolicy.Type)
							}
							return models.RestartPolicyAlways
						}(),
					},
					SpecJSON: specJSON,
				},
			})
			if err != nil {
				cp.logger.Warn("Failed to schedule replica %d/%d for service %s: %v", i+1, replicas, svcConfig.Name, err)
				continue
			}
			assignedTasks = append(assignedTasks, assignRes)
		}
	}

	// 5. Update Status
	svcStatus := "RUNNING"
	if len(assignedTasks) < replicas {
		if len(assignedTasks) == 0 && replicas > 0 {
			svcStatus = "FAILED"
		} else {
			svcStatus = "DEGRADED"
		}
	}
	if replicas == 0 {
		svcStatus = "STOPPED"
	}

	svcRecord, err := store.Services().Get(ctx, serviceID)
	if err == nil && svcRecord != nil {
		svcRecord.Status = svcStatus
		svcRecord.UpdatedAt = time.Now().UTC()
		_ = store.Services().Update(ctx, svcRecord)
	}

	deploymentRecord.Status = svcStatus
	deploymentRecord.UpdatedAt = time.Now().UTC()
	_ = store.Deployments().Update(ctx, deploymentRecord)

	// Append deployment audit event
	_ = store.Events().Append(ctx, &models.Event{
		ID:        id.NewEventID(),
		Type:      "SERVICE_DEPLOYED",
		Source:    "controlplane",
		EntityID:  serviceID,
		Payload:   fmt.Sprintf(`{"service":"%s","replicas":%d,"assigned":%d,"status":"%s"}`, svcConfig.Name, replicas, len(assignedTasks), svcStatus),
		CreatedAt: time.Now().UTC(),
	})

	return &DeployResult{
		ServiceID:    serviceID,
		DeploymentID: deploymentID,
		ServiceName:  svcConfig.Name,
		Replicas:     replicas,
		Status:       svcStatus,
		Tasks:        assignedTasks,
		CreatedAt:    now,
	}, nil
}

// ServiceInspectResult holds full inspection details for a service.
type ServiceInspectResult struct {
	Service     *models.Service      `json:"service"`
	Deployments []*models.Deployment `json:"deployments"`
	Tasks       []*models.Task       `json:"tasks"`
}

// InspectService retrieves full service details including its deployment history and running tasks.
func (cp *ControlPlane) InspectService(ctx context.Context, nameOrID string) (*ServiceInspectResult, error) {
	store := cp.StateManager.Store()
	if store == nil {
		return nil, fmt.Errorf("state store is not available")
	}

	var targetSvc *models.Service
	// Try lookup by ID
	svc, err := store.Services().Get(ctx, id.ID(nameOrID))
	if err == nil && svc != nil {
		targetSvc = svc
	} else {
		// Lookup by Name
		services, err := store.Services().List(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list services: %w", err)
		}
		for _, s := range services {
			if strings.EqualFold(s.Name, nameOrID) {
				targetSvc = s
				break
			}
		}
	}

	if targetSvc == nil {
		return nil, fmt.Errorf("service '%s' not found", nameOrID)
	}

	// Fetch deployments
	svcDeployments, _ := store.Deployments().ListByService(ctx, targetSvc.ID)

	// Fetch tasks
	svcTasks, _ := store.Tasks().ListByService(ctx, targetSvc.ID)

	return &ServiceInspectResult{
		Service:     targetSvc,
		Deployments: svcDeployments,
		Tasks:       svcTasks,
	}, nil
}

// ScaleResult details the outcome of a service scaling operation.
type ScaleResult struct {
	ServiceID       id.ID                  `json:"service_id"`
	ServiceName     string                 `json:"service_name"`
	PreviousReplicas int                   `json:"previous_replicas"`
	DesiredReplicas  int                   `json:"desired_replicas"`
	Summary          ReconciliationSummary `json:"summary"`
	Status           string                `json:"status"`
	UpdatedAt        time.Time             `json:"updated_at"`
}

// ScaleService updates the desired replica count for a service and runs reconciliation to converge.
func (cp *ControlPlane) ScaleService(ctx context.Context, nameOrID string, replicas int, dispatcher scheduler.Dispatcher) (*ScaleResult, error) {
	if replicas < 0 {
		return nil, fmt.Errorf("replicas cannot be negative (got %d)", replicas)
	}

	store := cp.StateManager.Store()
	if store == nil {
		return nil, fmt.Errorf("state store is not available")
	}

	inspectRes, err := cp.InspectService(ctx, nameOrID)
	if err != nil {
		return nil, err
	}

	svc := inspectRes.Service
	prevReplicas := svc.Replicas
	now := time.Now().UTC()

	svc.Replicas = replicas
	svc.UpdatedAt = now
	if err := store.Services().Update(ctx, svc); err != nil {
		return nil, fmt.Errorf("failed to update desired replicas: %w", err)
	}

	// Run Reconciler to converge actual tasks to new desired replicas
	reconciler := cp.Reconciler
	if dispatcher != nil {
		reconciler.SetDispatcher(dispatcher)
	}

	summary, err := reconciler.ReconcileAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("reconciliation failed after scale update: %w", err)
	}

	// Fetch updated status
	updatedSvc, _ := store.Services().Get(ctx, svc.ID)
	status := "RUNNING"
	if updatedSvc != nil {
		status = updatedSvc.Status
	}

	// Append scaling audit event
	_ = store.Events().Append(ctx, &models.Event{
		ID:        id.NewEventID(),
		Type:      "SERVICE_SCALED",
		Source:    "controlplane",
		EntityID:  svc.ID,
		Payload:   fmt.Sprintf(`{"service":"%s","prev_replicas":%d,"desired_replicas":%d,"created":%d,"removed":%d}`, svc.Name, prevReplicas, replicas, summary.CreatedTasks, summary.RemovedTasks),
		CreatedAt: now,
	})

	return &ScaleResult{
		ServiceID:        svc.ID,
		ServiceName:      svc.Name,
		PreviousReplicas: prevReplicas,
		DesiredReplicas:  replicas,
		Summary:          *summary,
		Status:           status,
		UpdatedAt:        now,
	}, nil
}

