package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
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

		// 3. Identify active/desired deployment for this service
		var targetDeployment *models.Deployment
		deps, _ := store.Deployments().ListByService(ctx, svc.ID)
		for i := len(deps) - 1; i >= 0; i-- {
			d := deps[i]
			if d.Status == string(models.DeploymentStatusActive) || d.Status == "RUNNING" || d.Status == string(models.DeploymentStatusInProgress) {
				targetDeployment = d
				break
			}
		}
		if targetDeployment == nil && len(deps) > 0 {
			targetDeployment = deps[len(deps)-1]
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

		currentMatching := len(matchingTasks)

		// 5. Case A: Matching < Desired -> Scale UP (create tasks on target deployment)
		if currentMatching < desiredReplicas {
			deficit := desiredReplicas - currentMatching
			r.logger.Info("Service %s has deficit of %d replicas on deployment %s (desired: %d, current: %d). Creating tasks...",
				svc.Name, deficit, targetDeploymentID, desiredReplicas, currentMatching)

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
					},
					Spec: scheduler.TaskSpec{
						Command:       svcConfig.Command,
						Args:          svcConfig.Args,
						Environment:   svcConfig.Environment,
						WorkingDir:    svcConfig.WorkingDir,
						Runtime:       svc.Runtime,
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
					r.logger.Warn("Reconciler failed to schedule new replica for %s: %v", svc.Name, err)
					continue
				}
				summary.CreatedTasks++
			}
		} else if currentMatching > desiredReplicas {
			// 6. Case B: Matching > Desired -> Scale DOWN surplus matching tasks
			surplus := currentMatching - desiredReplicas
			r.logger.Info("Service %s has surplus of %d replicas (desired: %d, matching: %d). Stopping tasks...",
				svc.Name, surplus, desiredReplicas, currentMatching)

			for i := 0; i < surplus && i < len(matchingTasks); i++ {
				t := matchingTasks[i]
				t.State = string(models.TaskStateStopped)
				t.UpdatedAt = time.Now().UTC()
				_ = store.Tasks().Update(ctx, t)
				summary.RemovedTasks++
			}
		}

		// 7. Transition/Decommission outdated tasks from previous versions
		for _, ot := range outdatedTasks {
			r.logger.Info("Decommissioning outdated task %s (deployment: %s) for service %s...",
				ot.ID, ot.DeploymentID, svc.Name)
			ot.State = string(models.TaskStateStopped)
			ot.UpdatedAt = time.Now().UTC()
			_ = store.Tasks().Update(ctx, ot)
			summary.RemovedTasks++
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
