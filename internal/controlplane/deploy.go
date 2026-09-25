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

	// 3. Create Immutable Deployment Record
	version := svcConfig.Version
	if strings.TrimSpace(version) == "" {
		version = "v1"
	}

	deploymentID := id.NewDeploymentID()

	// Convert ports and volumes to models
	var ports []models.PortMapping
	for _, p := range svcConfig.Ports {
		ports = append(ports, models.PortMapping{
			HostPort:    p.HostPort,
			ServicePort: p.ServicePort,
			Protocol:    p.Protocol,
		})
	}

	var vols []models.VolumeMount
	for _, v := range svcConfig.Volumes {
		vols = append(vols, models.VolumeMount{
			VolumeName: v.VolumeName,
			MountPath:  v.Target,
			ReadOnly:   v.ReadOnly,
		})
	}

	var hcSpec *models.HealthCheckSpec
	if svcConfig.HealthCheck != nil {
		hcSpec = &models.HealthCheckSpec{
			Type:             models.HealthCheckType(svcConfig.HealthCheck.Type),
			Path:             svcConfig.HealthCheck.Path,
			Port:             svcConfig.HealthCheck.Port,
			FailureThreshold: svcConfig.HealthCheck.FailureThreshold,
			SuccessThreshold: svcConfig.HealthCheck.SuccessThreshold,
		}
		if svcConfig.HealthCheck.Interval != "" {
			d, _ := time.ParseDuration(svcConfig.HealthCheck.Interval)
			hcSpec.Interval = d
		}
		if svcConfig.HealthCheck.Timeout != "" {
			d, _ := time.ParseDuration(svcConfig.HealthCheck.Timeout)
			hcSpec.Timeout = d
		}
	}

	depConfig := models.DeploymentConfig{
		Command:     svcConfig.Command,
		Args:        svcConfig.Args,
		Environment: svcConfig.Environment,
		WorkingDir:  svcConfig.WorkingDir,
		Artifact:    svcConfig.Artifact,
		Runtime:     svcConfig.Runtime,
		Replicas:    replicas,
		Resources: models.ResourceRequirements{
			CPU:    parsedRes.CPUCores,
			Memory: parsedRes.MemoryBytes,
		},
		RestartPolicy: models.RestartPolicy{
			Type: func() models.RestartPolicyType {
				if svcConfig.RestartPolicy != nil {
					return models.RestartPolicyType(svcConfig.RestartPolicy.Type)
				}
				return models.RestartPolicyAlways
			}(),
		},
		HealthCheck: hcSpec,
		Ports:       ports,
		Volumes:     vols,
	}

	immDeployment := &models.ImmutableDeployment{
		ID:          deploymentID,
		ServiceID:   serviceID,
		ServiceName: svcConfig.Name,
		Version:     version,
		ConfigHash:  depConfig.ComputeHash(),
		Status:      models.DeploymentStatusInProgress,
		Config:      depConfig,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	depSpecJSON, err := immDeployment.ToSpecJSON()
	if err != nil {
		depSpecJSON = specJSON
	}

	// Supercede previous active deployments for this service
	prevDeployments, _ := store.Deployments().ListByService(ctx, serviceID)
	for _, prevDep := range prevDeployments {
		if prevDep.ID != deploymentID && prevDep.Status != string(models.DeploymentStatusSuperceded) && prevDep.Status != string(models.DeploymentStatusRolledBack) {
			prevDep.Status = string(models.DeploymentStatusSuperceded)
			prevDep.UpdatedAt = now
			_ = store.Deployments().Update(ctx, prevDep)
		}
	}

	deploymentRecord := &models.Deployment{
		ID:        deploymentID,
		ServiceID: serviceID,
		Version:   version,
		Status:    string(models.DeploymentStatusActive),
		SpecJSON:  depSpecJSON,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.Deployments().Create(ctx, deploymentRecord); err != nil {
		return nil, fmt.Errorf("failed to create deployment record: %w", err)
	}

	// 4. Schedule & Assign Replicas via Reconciler
	reconciler := cp.Reconciler
	if dispatcher != nil {
		reconciler.SetDispatcher(dispatcher)
	}

	summary, err := reconciler.ReconcileAll(ctx)
	if err != nil {
		cp.logger.Warn("Initial reconciliation pass encountered warning: %v", err)
	}
	_ = summary

	// Fetch current active tasks assigned to this deployment
	allTasks, _ := store.Tasks().ListByService(ctx, serviceID)
	var activeTasks []*scheduler.AssignmentResult
	for _, t := range allTasks {
		if t.DeploymentID == deploymentID && t.State != string(models.TaskStateStopped) && t.State != string(models.TaskStateFailed) && t.State != string(models.TaskStateLost) {
			activeTasks = append(activeTasks, &scheduler.AssignmentResult{
				TaskID:    t.ID,
				WorkerID:  t.WorkerID,
				State:     models.TaskState(t.State),
				Timestamp: t.UpdatedAt,
			})
		}
	}

	// 5. Update Status
	svcStatus := "RUNNING"
	if len(activeTasks) < replicas {
		if len(activeTasks) == 0 && replicas > 0 {
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
		Payload:   fmt.Sprintf(`{"service":"%s","version":"%s","deployment_id":"%s","replicas":%d,"assigned":%d,"status":"%s"}`, svcConfig.Name, version, deploymentID, replicas, len(activeTasks), svcStatus),
		CreatedAt: time.Now().UTC(),
	})

	return &DeployResult{
		ServiceID:    serviceID,
		DeploymentID: deploymentID,
		ServiceName:  svcConfig.Name,
		Replicas:     replicas,
		Status:       svcStatus,
		Tasks:        activeTasks,
		CreatedAt:    now,
	}, nil
}

// DeployVersion activates an existing or requested immutable deployment version for a service (e.g. "api:v2").
// It updates the desired service state to match that version's specification and runs reconciliation.
func (cp *ControlPlane) DeployVersion(ctx context.Context, serviceNameOrID string, targetVersion string, dispatcher scheduler.Dispatcher) (*DeployResult, error) {
	if strings.TrimSpace(serviceNameOrID) == "" {
		return nil, fmt.Errorf("service name or ID is required")
	}
	if strings.TrimSpace(targetVersion) == "" {
		return nil, fmt.Errorf("target version is required (e.g. 'v2')")
	}

	store := cp.StateManager.Store()
	if store == nil {
		return nil, fmt.Errorf("state store is not available")
	}

	inspectRes, err := cp.InspectService(ctx, serviceNameOrID)
	if err != nil {
		return nil, err
	}

	svc := inspectRes.Service

	// Locate the target deployment record for this service matching the version
	var targetDep *models.Deployment
	for _, d := range inspectRes.Deployments {
		if strings.EqualFold(d.Version, targetVersion) || d.ID.String() == targetVersion {
			targetDep = d
			break
		}
	}

	now := time.Now().UTC()

	if targetDep == nil {
		// If deployment version doesn't exist yet, construct from current service spec with new version
		return cp.deployNewVersionFromCurrentSpec(ctx, svc, targetVersion, dispatcher)
	}

	// Found existing deployment version! Point service desired state to this deployment
	immDep, err := models.DeploymentFromModel(targetDep)
	if err != nil {
		return nil, fmt.Errorf("failed to parse deployment model: %w", err)
	}

	// Update service desired state from target deployment config
	svc.SpecJSON = targetDep.SpecJSON
	svc.Runtime = immDep.Config.Runtime
	svc.Command = immDep.Config.Command
	if immDep.Config.Replicas > 0 {
		svc.Replicas = immDep.Config.Replicas
	}
	svc.Status = "UPDATING"
	svc.UpdatedAt = now

	if err := store.Services().Update(ctx, svc); err != nil {
		return nil, fmt.Errorf("failed to update service desired state: %w", err)
	}

	// Mark all other deployments for this service as SUPERCEDED, mark target as ACTIVE
	deps, _ := store.Deployments().ListByService(ctx, svc.ID)
	for _, d := range deps {
		if d.ID == targetDep.ID {
			d.Status = string(models.DeploymentStatusActive)
			d.UpdatedAt = now
			_ = store.Deployments().Update(ctx, d)
		} else if d.Status != string(models.DeploymentStatusSuperceded) && d.Status != string(models.DeploymentStatusRolledBack) {
			d.Status = string(models.DeploymentStatusSuperceded)
			d.UpdatedAt = now
			_ = store.Deployments().Update(ctx, d)
		}
	}

	// Trigger reconciliation loop to transition workloads from older version tasks to this deployment
	reconciler := cp.Reconciler
	if dispatcher != nil {
		reconciler.SetDispatcher(dispatcher)
	}

	_, err = reconciler.ReconcileAll(ctx)
	if err != nil {
		cp.logger.Warn("Reconciliation during version deploy warning: %v", err)
	}

	// Fetch active tasks on this deployment
	allTasks, _ := store.Tasks().ListByService(ctx, svc.ID)
	var activeTasks []*scheduler.AssignmentResult
	for _, t := range allTasks {
		if t.DeploymentID == targetDep.ID && t.State != string(models.TaskStateStopped) && t.State != string(models.TaskStateFailed) && t.State != string(models.TaskStateLost) {
			activeTasks = append(activeTasks, &scheduler.AssignmentResult{
				TaskID:    t.ID,
				WorkerID:  t.WorkerID,
				State:     models.TaskState(t.State),
				Timestamp: t.UpdatedAt,
			})
		}
	}

	svcStatus := "RUNNING"
	if len(activeTasks) < svc.Replicas {
		if len(activeTasks) == 0 && svc.Replicas > 0 {
			svcStatus = "FAILED"
		} else {
			svcStatus = "DEGRADED"
		}
	}
	if svc.Replicas == 0 {
		svcStatus = "STOPPED"
	}

	svc.Status = svcStatus
	svc.UpdatedAt = time.Now().UTC()
	_ = store.Services().Update(ctx, svc)

	// Append audit event
	_ = store.Events().Append(ctx, &models.Event{
		ID:        id.NewEventID(),
		Type:      "VERSION_DEPLOYED",
		Source:    "controlplane",
		EntityID:  svc.ID,
		Payload:   fmt.Sprintf(`{"service":"%s","version":"%s","deployment_id":"%s","replicas":%d,"assigned":%d,"status":"%s"}`, svc.Name, targetVersion, targetDep.ID, svc.Replicas, len(activeTasks), svcStatus),
		CreatedAt: time.Now().UTC(),
	})

	return &DeployResult{
		ServiceID:    svc.ID,
		DeploymentID: targetDep.ID,
		ServiceName:  svc.Name,
		Replicas:     svc.Replicas,
		Status:       svcStatus,
		Tasks:        activeTasks,
		CreatedAt:    targetDep.CreatedAt,
	}, nil
}

func (cp *ControlPlane) deployNewVersionFromCurrentSpec(ctx context.Context, svc *models.Service, newVersion string, dispatcher scheduler.Dispatcher) (*DeployResult, error) {
	var svcConfig spec.ServiceConfig
	if svc.SpecJSON != "" {
		_ = json.Unmarshal([]byte(svc.SpecJSON), &svcConfig)
	}
	svcConfig.Name = svc.Name
	svcConfig.Version = newVersion
	if svcConfig.Command == "" {
		svcConfig.Command = svc.Command
	}
	if svcConfig.Runtime == "" {
		svcConfig.Runtime = svc.Runtime
	}
	replicas := svc.Replicas
	svcConfig.Replicas = &replicas

	return cp.DeployService(ctx, &svcConfig, dispatcher)
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

