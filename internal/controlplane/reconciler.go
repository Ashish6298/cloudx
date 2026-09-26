package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/scheduler"
	"github.com/cloudx-org/cloudx/internal/spec"
	"github.com/cloudx-org/cloudx/internal/state"
	"github.com/cloudx-org/cloudx/internal/state/models"
)

// ReconcilerConfig configures the background reconciliation loop.
type ReconcilerConfig struct {
	Interval time.Duration
}

// DefaultReconcilerConfig provides production defaults.
func DefaultReconcilerConfig() ReconcilerConfig {
	return ReconcilerConfig{
		Interval: 2 * time.Second,
	}
}

// Reconciler executes the continuous reconciliation loop ensuring that actual running
// replicas match desired service specifications, handling replica shortfalls, surpluses,
// and automatic rescheduling of orphaned tasks on failed/lost worker nodes.
type Reconciler struct {
	mu          sync.RWMutex
	cfg         ReconcilerConfig
	store       state.Store
	scheduler   scheduler.Scheduler
	coordinator *scheduler.AssignmentCoordinator
	dispatcher  scheduler.Dispatcher
	logger      logging.Logger
	cancel      context.CancelFunc
}

// NewReconciler creates a new Reconciler instance.
func NewReconciler(cfg ReconcilerConfig, store state.Store, sched scheduler.Scheduler, dispatcher scheduler.Dispatcher, logger logging.Logger) *Reconciler {
	if logger == nil {
		logger = logging.NewDefaultLogger()
	}
	if sched == nil {
		sched = scheduler.NewBasicScheduler()
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 2 * time.Second
	}

	coordinator := scheduler.NewAssignmentCoordinator(store, sched, dispatcher, logger)

	return &Reconciler{
		cfg:         cfg,
		store:       store,
		scheduler:   sched,
		coordinator: coordinator,
		dispatcher:  dispatcher,
		logger:      logger.With("component", "reconciler"),
	}
}

func (r *Reconciler) Name() string { return "Reconciler" }

// SetDispatcher updates the task dispatcher on the reconciler.
func (r *Reconciler) SetDispatcher(dispatcher scheduler.Dispatcher) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dispatcher = dispatcher
	r.coordinator = scheduler.NewAssignmentCoordinator(r.store, r.scheduler, dispatcher, r.logger)
}

// Start launches the background reconciliation loop.
func (r *Reconciler) Start(ctx context.Context) error {
	r.mu.Lock()
	if r.store == nil {
		r.mu.Unlock()
		return fmt.Errorf("state store is required for reconciler")
	}
	loopCtx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	r.mu.Unlock()

	r.logger.Info("Starting Reconciler loop (interval: %v)...", r.cfg.Interval)
	go r.runLoop(loopCtx)
	return nil
}

// Stop gracefully stops the reconciliation loop.
func (r *Reconciler) Stop(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	r.logger.Info("Reconciler loop stopped")
	return nil
}

func (r *Reconciler) runLoop(ctx context.Context) {
	ticker := time.NewTicker(r.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.ReconcileAll(ctx)
		}
	}
}

// ReconciliationSummary details the actions taken during a reconciliation pass.
type ReconciliationSummary struct {
	EvaluatedServices int `json:"evaluated_services"`
	CreatedTasks      int `json:"created_tasks"`
	RemovedTasks      int `json:"removed_tasks"`
	OrphanedRecovered int `json:"orphaned_recovered"`
}

// ReconcileAll runs an idempotent convergence pass over all services in the cluster.
func (r *Reconciler) ReconcileAll(ctx context.Context) (*ReconciliationSummary, error) {
	r.mu.RLock()
	store := r.store
	coordinator := r.coordinator
	r.mu.RUnlock()

	if store == nil {
		return nil, fmt.Errorf("store is nil")
	}

	services, err := store.Services().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list services: %w", err)
	}

	workers, err := store.Workers().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list workers: %w", err)
	}

	workerStatusMap := make(map[id.ID]string)
	for _, w := range workers {
		workerStatusMap[w.ID] = w.Status
	}

	summary := &ReconciliationSummary{
		EvaluatedServices: len(services),
	}

	for _, svc := range services {
		// 1. Get all tasks for this service
		tasks, err := store.Tasks().ListByService(ctx, svc.ID)
		if err != nil {
			r.logger.Warn("Failed to list tasks for service %s: %v", svc.Name, err)
			continue
		}

		// 2. Check for orphaned tasks on LOST or UNHEALTHY workers, and unrecoverable task failures
		var activeTasks []*models.Task
		for _, t := range tasks {
			workerStatus := workerStatusMap[t.WorkerID]
			isTerminated := t.State == string(models.TaskStateStopped) ||
				t.State == string(models.TaskStateFailed) ||
				t.State == string(models.TaskStateCrashLoop) ||
				t.State == string(models.TaskStateLost)

			if !isTerminated && (workerStatus == "LOST" || workerStatus == "UNHEALTHY" || workerStatus == "") {
				// Task is orphaned! Transition to LOST
				r.logger.Warn("Task %s is orphaned on %s worker %s. Marking LOST and rescheduling...",
					t.ID, workerStatus, t.WorkerID)
				t.State = string(models.TaskStateLost)
				t.UpdatedAt = time.Now().UTC()
				_ = store.Tasks().Update(ctx, t)
				summary.OrphanedRecovered++

				// Append TASK_RESCHEDULED event
				_ = store.Events().Append(ctx, &models.Event{
					ID:        id.NewEventID(),
					Type:      "TASK_RESCHEDULED",
					Source:    "reconciler",
					EntityID:  t.ID,
					Payload:   fmt.Sprintf(`{"service_id":"%s","worker_id":"%s","worker_status":"%s","reason":"orphaned_worker"}`, svc.ID, t.WorkerID, workerStatus),
					CreatedAt: time.Now().UTC(),
				})
				continue
			}

			// If task is in CRASH_LOOP or unrecoverable FAILED on a worker, mark it for replacement by not counting towards active replicas
			if !isTerminated {
				activeTasks = append(activeTasks, t)
			}
		}

		desiredReplicas := svc.Replicas
		_ = len(activeTasks)

		// Parse service spec
		var svcConfig spec.ServiceConfig
		if svc.SpecJSON != "" {
			_ = json.Unmarshal([]byte(svc.SpecJSON), &svcConfig)
		}
		if svcConfig.Command == "" {
			svcConfig.Command = svc.Command
		}
		if svcConfig.Runtime == "" {
			svcConfig.Runtime = svc.Runtime
		}
		parsedRes, _ := svcConfig.Validate()
		if parsedRes == nil {
			parsedRes = &spec.ParsedResources{CPUCores: 0.5, MemoryBytes: 256 * 1024 * 1024}
		}

		// Extract port and volume requirements for scheduling constraints
		var svcHostPorts []int
		for _, p := range svcConfig.Ports {
			if p.HostPort > 0 {
				svcHostPorts = append(svcHostPorts, p.HostPort)
			}
		}
		var svcVolNames []string
		for _, v := range svcConfig.Volumes {
			if v.VolumeName != "" {
				svcVolNames = append(svcVolNames, v.VolumeName)
			}
		}

		// 3. Identify active/desired deployment for this service
		var targetDeployment *models.Deployment
		deps, _ := store.Deployments().ListByService(ctx, svc.ID)
		for _, d := range deps {
			if d.Status == string(models.DeploymentStatusActive) || d.Status == "RUNNING" || d.Status == string(models.DeploymentStatusInProgress) {
				targetDeployment = d
				break
			}
		}
		if targetDeployment == nil && len(deps) > 0 {
			targetDeployment = deps[0]
		}

		var targetDeploymentID id.ID
		if targetDeployment != nil {
			targetDeploymentID = targetDeployment.ID
		}

		// 4. Partition active tasks into matching (current version) and outdated (previous versions)
		var matchingTasks []*models.Task
		var outdatedTasks []*models.Task
		for _, t := range activeTasks {
			if targetDeploymentID != "" && t.DeploymentID != "" && t.DeploymentID != targetDeploymentID {
				outdatedTasks = append(outdatedTasks, t)
			} else {
				matchingTasks = append(matchingTasks, t)
			}
		}

		// Determine update strategy parameters (default: rolling, maxUnavailable=1)
		currentMatching := len(matchingTasks)
		maxUnavailable := 1
		strategyType := "rolling"
		if svcConfig.UpdateStrategy != nil {
			if svcConfig.UpdateStrategy.Type != "" {
				strategyType = strings.ToLower(svcConfig.UpdateStrategy.Type)
			}
			if svcConfig.UpdateStrategy.MaxUnavailable > 0 {
				maxUnavailable = svcConfig.UpdateStrategy.MaxUnavailable
			}
		}

		// Count healthy matching tasks
		matchingHealthyCount := 0
		for _, t := range matchingTasks {
			if t.State == string(models.TaskStateHealthy) || t.State == string(models.TaskStateRunning) {
				matchingHealthyCount++
			}
		}

		// Check for failure/unhealthy condition in newly deployed tasks
		hasFailedNewTasks := false
		for _, t := range matchingTasks {
			if t.State == string(models.TaskStateFailed) || t.State == string(models.TaskStateCrashLoop) || t.State == string(models.TaskStateUnhealthy) {
				hasFailedNewTasks = true
				break
			}
		}

		if hasFailedNewTasks && len(matchingTasks) > 0 {
			r.logger.Warn("Rollout for service %s halted: new deployment %s has unhealthy/failed tasks", svc.Name, targetDeploymentID)
			// Halt rollout progression
		}

		// 5. Progressive Rolling Replacement logic
		if strategyType == "recreate" {
			// Recreate strategy: stop all outdated tasks first, then create matching
			for _, ot := range outdatedTasks {
				r.logger.Info("Recreate strategy: stopping outdated task %s for service %s...", ot.ID, svc.Name)
				ot.State = string(models.TaskStateStopped)
				ot.UpdatedAt = time.Now().UTC()
				_ = store.Tasks().Update(ctx, ot)
				summary.RemovedTasks++
			}

			if currentMatching < desiredReplicas {
				deficit := desiredReplicas - currentMatching
				for i := 0; i < deficit; i++ {
					taskID := id.NewTaskID()
					_, err := coordinator.Assign(ctx, scheduler.AssignOptions{
						TaskID:       taskID,
						ServiceID:    svc.ID,
						DeploymentID: targetDeploymentID,
						Requirements: &scheduler.TaskRequirements{
							CPU:             parsedRes.CPUCores,
							Memory:          parsedRes.MemoryBytes,
							RequiredRuntime: svc.Runtime,
							RequiredPorts:   svcHostPorts,
							RequiredVolumes: svcVolNames,
						},
						Spec: scheduler.TaskSpec{
							Command:         svcConfig.Command,
							Args:            svcConfig.Args,
							Environment:     svcConfig.Environment,
							WorkingDir:      svcConfig.WorkingDir,
							Runtime:         svc.Runtime,
							RequiredPorts:   svcHostPorts,
							RequiredVolumes: svcVolNames,
							RestartPolicy: models.RestartPolicy{
								Type: func() models.RestartPolicyType {
									if svcConfig.RestartPolicy != nil {
										return models.RestartPolicyType(svcConfig.RestartPolicy.Type)
									}
									return models.RestartPolicyAlways
								}(),
							},
							SpecJSON: svc.SpecJSON,
						},
					})
					if err != nil {
						r.logger.Warn("Reconciler failed to schedule task for %s: %v", svc.Name, err)
						continue
					}
					summary.CreatedTasks++
				}
			}
		} else {
			// Progressive Rolling update:
			// If no outdated tasks exist (standard scale-up), provision full deficit
			// If outdated tasks exist (version rollout), roll out progressively respecting maxUnavailable
			if currentMatching < desiredReplicas && !hasFailedNewTasks {
				step := desiredReplicas - currentMatching
				if len(outdatedTasks) > 0 {
					step = maxUnavailable
					if step <= 0 {
						step = 1
					}
					deficit := desiredReplicas - currentMatching
					if deficit < step {
						step = deficit
					}
				}

				for i := 0; i < step; i++ {
					taskID := id.NewTaskID()
					_, err := coordinator.Assign(ctx, scheduler.AssignOptions{
						TaskID:       taskID,
						ServiceID:    svc.ID,
						DeploymentID: targetDeploymentID,
						Requirements: &scheduler.TaskRequirements{
							CPU:             parsedRes.CPUCores,
							Memory:          parsedRes.MemoryBytes,
							RequiredRuntime: svc.Runtime,
							RequiredPorts:   svcHostPorts,
							RequiredVolumes: svcVolNames,
						},
						Spec: scheduler.TaskSpec{
							Command:         svcConfig.Command,
							Args:            svcConfig.Args,
							Environment:     svcConfig.Environment,
							WorkingDir:      svcConfig.WorkingDir,
							Runtime:         svc.Runtime,
							RequiredPorts:   svcHostPorts,
							RequiredVolumes: svcVolNames,
							RestartPolicy: models.RestartPolicy{
								Type: func() models.RestartPolicyType {
									if svcConfig.RestartPolicy != nil {
										return models.RestartPolicyType(svcConfig.RestartPolicy.Type)
									}
									return models.RestartPolicyAlways
								}(),
							},
							SpecJSON: svc.SpecJSON,
						},
					})
					if err != nil {
						r.logger.Warn("Reconciler failed to schedule rolling replica for %s: %v", svc.Name, err)
						continue
					}
					summary.CreatedTasks++
					matchingHealthyCount++
					currentMatching++
					matchingTasks = append(matchingTasks, &models.Task{
						ID:           taskID,
						ServiceID:    svc.ID,
						DeploymentID: targetDeploymentID,
						State:        string(models.TaskStateRunning),
					})
				}
			}

			// Decommission outdated tasks progressively as matching tasks become available/healthy
			// Ensure we do not decommission more than maxUnavailable per pass, and total active replicas >= (desiredReplicas - maxUnavailable)
			if len(outdatedTasks) > 0 && !hasFailedNewTasks {
				// Target outdated tasks to keep = desiredReplicas - len(matchingTasks)
				// If len(matchingTasks) >= desiredReplicas, target outdated tasks to keep is 0
				targetOutdated := desiredReplicas - len(matchingTasks)
				if targetOutdated < 0 {
					targetOutdated = 0
				}
				neededToStop := len(outdatedTasks) - targetOutdated
				allowedToStop := neededToStop
				if allowedToStop > maxUnavailable {
					allowedToStop = maxUnavailable
				}
				if allowedToStop < 0 {
					allowedToStop = 0
				}

				for i := 0; i < allowedToStop && i < len(outdatedTasks); i++ {
					ot := outdatedTasks[i]
					r.logger.Info("Progressive rolling update: retiring outdated replica %s (deployment %s) for service %s...",
						ot.ID, ot.DeploymentID, svc.Name)
					ot.State = string(models.TaskStateStopped)
					ot.UpdatedAt = time.Now().UTC()
					_ = store.Tasks().Update(ctx, ot)
					summary.RemovedTasks++
				}
			}

			// If current matching exceeds desired replicas, scale down matching
			if currentMatching > desiredReplicas {
				surplus := currentMatching - desiredReplicas
				for i := 0; i < surplus && i < len(matchingTasks); i++ {
					t := matchingTasks[i]
					t.State = string(models.TaskStateStopped)
					t.UpdatedAt = time.Now().UTC()
					_ = store.Tasks().Update(ctx, t)
					summary.RemovedTasks++
				}
			}
		}

		// 5. Update derived service status
		newStatus := "RUNNING"
		if desiredReplicas == 0 {
			newStatus = "STOPPED"
		} else {
			// Re-count active tasks after adjustments
			currentActive, _ := store.Tasks().ListByService(ctx, svc.ID)
			liveCount := 0
			for _, t := range currentActive {
				if t.State != string(models.TaskStateStopped) && t.State != string(models.TaskStateFailed) && t.State != string(models.TaskStateLost) {
					liveCount++
				}
			}
			if liveCount == 0 {
				newStatus = "DEGRADED"
			} else if liveCount < desiredReplicas {
				newStatus = "DEGRADED"
			}
		}

		if svc.Status != newStatus {
			svc.Status = newStatus
			svc.UpdatedAt = time.Now().UTC()
			_ = store.Services().Update(ctx, svc)
		}
	}

	return summary, nil
}
