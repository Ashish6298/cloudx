package controlplane

import (
	"context"
	"fmt"
	"sync"

	"github.com/cloudx-org/cloudx/internal/common/errors"
	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/config"
	"github.com/cloudx-org/cloudx/internal/state"
)

// Component defines the lifecycle interface for all control plane subsystems.
type Component interface {
	Name() string
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

// StateManager manages state store access.
type StateManager struct {
	store state.Store
}

func NewStateManager(store state.Store) *StateManager {
	return &StateManager{store: store}
}

func (c *StateManager) Name() string { return "StateManager" }
func (c *StateManager) Start(ctx context.Context) error {
	if c.store == nil {
		return errors.NewInvalidState("state store dependency is nil", nil)
	}
	return nil
}
func (c *StateManager) Stop(ctx context.Context) error {
	if c.store != nil {
		return c.store.Close()
	}
	return nil
}
func (c *StateManager) Store() state.Store { return c.store }

// Registry manages service discovery endpoints.
type Registry struct{}

func NewRegistry() *Registry                    { return &Registry{} }
func (c *Registry) Name() string                { return "Registry" }
func (c *Registry) Start(ctx context.Context) error { return nil }
func (c *Registry) Stop(ctx context.Context) error  { return nil }

// Scheduler manages task placement and scoring.
type Scheduler struct{}

func NewScheduler() *Scheduler                   { return &Scheduler{} }
func (c *Scheduler) Name() string                { return "Scheduler" }
func (c *Scheduler) Start(ctx context.Context) error { return nil }
func (c *Scheduler) Stop(ctx context.Context) error  { return nil }

// DeploymentManager coordinates rolling updates and rollbacks.
type DeploymentManager struct{}

func NewDeploymentManager() *DeploymentManager       { return &DeploymentManager{} }
func (c *DeploymentManager) Name() string            { return "DeploymentManager" }
func (c *DeploymentManager) Start(ctx context.Context) error { return nil }
func (c *DeploymentManager) Stop(ctx context.Context) error  { return nil }

// HealthManager monitors worker heartbeats and task health probes.
type HealthManager struct{}

func NewHealthManager() *HealthManager           { return &HealthManager{} }
func (c *HealthManager) Name() string            { return "HealthManager" }
func (c *HealthManager) Start(ctx context.Context) error { return nil }
func (c *HealthManager) Stop(ctx context.Context) error  { return nil }

// Reconciler runs the continuous desired-state convergence loop.
type Reconciler struct{}

func NewReconciler() *Reconciler                  { return &Reconciler{} }
func (c *Reconciler) Name() string                { return "Reconciler" }
func (c *Reconciler) Start(ctx context.Context) error { return nil }
func (c *Reconciler) Stop(ctx context.Context) error  { return nil }

// EventManager processes and appends cluster events.
type EventManager struct{}

func NewEventManager() *EventManager                { return &EventManager{} }
func (c *EventManager) Name() string                { return "EventManager" }
func (c *EventManager) Start(ctx context.Context) error { return nil }
func (c *EventManager) Stop(ctx context.Context) error  { return nil }

// Options holds dependencies and configuration for the ControlPlane.
type Options struct {
	Config     *config.Config
	Store      state.Store
	Logger     logging.Logger
	Components []Component // optional custom components
}

// Status represents the operational status of the Control Plane.
type Status string

const (
	StatusInitialized Status = "INITIALIZED"
	StatusStarting    Status = "STARTING"
	StatusRunning     Status = "RUNNING"
	StatusStopping    Status = "STOPPING"
	StatusStopped     Status = "STOPPED"
	StatusFailed      Status = "FAILED"
)

// ControlPlane is the central brain of CloudX.
type ControlPlane struct {
	mu         sync.RWMutex
	cfg        *config.Config
	logger     logging.Logger
	status     Status
	cancel     context.CancelFunc
	components []Component

	// Major Subsystems
	StateManager      *StateManager
	Registry          *Registry
	Scheduler         *Scheduler
	DeploymentManager *DeploymentManager
	HealthManager     *HealthManager
	Reconciler        *Reconciler
	EventManager      *EventManager
}

// New creates and wires a new ControlPlane instance via dependency injection.
func New(opts Options) (*ControlPlane, error) {
	if opts.Config == nil {
		opts.Config = config.NewDefaultConfig()
	}
	if opts.Logger == nil {
		opts.Logger = logging.NewDefaultLogger()
	}

	stateMgr := NewStateManager(opts.Store)
	registry := NewRegistry()
	scheduler := NewScheduler()
	depMgr := NewDeploymentManager()
	healthMgr := NewHealthManager()
	reconciler := NewReconciler()
	eventMgr := NewEventManager()

	coreComponents := []Component{
		stateMgr,
		registry,
		scheduler,
		depMgr,
		healthMgr,
		reconciler,
		eventMgr,
	}

	if len(opts.Components) > 0 {
		coreComponents = append(coreComponents, opts.Components...)
	}

	cp := &ControlPlane{
		cfg:               opts.Config,
		logger:            opts.Logger.WithNode(opts.Config.Node.ID),
		status:            StatusInitialized,
		components:        coreComponents,
		StateManager:      stateMgr,
		Registry:          registry,
		Scheduler:         scheduler,
		DeploymentManager: depMgr,
		HealthManager:     healthMgr,
		Reconciler:        reconciler,
		EventManager:      eventMgr,
	}

	return cp, nil
}

// Status returns the current operational status of the ControlPlane.
func (cp *ControlPlane) Status() Status {
	cp.mu.RLock()
	defer cp.mu.RUnlock()
	return cp.status
}

// Start initializes and boots all control plane components.
func (cp *ControlPlane) Start(ctx context.Context) error {
	cp.mu.Lock()
	if cp.status == StatusRunning || cp.status == StatusStarting {
		cp.mu.Unlock()
		return errors.NewInvalidState("control plane is already running or starting", nil)
	}

	cp.status = StatusStarting
	runCtx, cancel := context.WithCancel(ctx)
	cp.cancel = cancel
	cp.mu.Unlock()

	cp.logger.Info("Starting CloudX Control Plane on %s...", cp.cfg.ControlPlane.Address)

	// Boot components in sequence
	for _, comp := range cp.components {
		select {
		case <-runCtx.Done():
			cp.logger.Warn("Control plane startup aborted by context cancellation")
			_ = cp.Stop(context.Background())
			return runCtx.Err()
		default:
			cp.logger.Debug("Starting component: %s", comp.Name())
			if err := comp.Start(runCtx); err != nil {
				cp.mu.Lock()
				cp.status = StatusFailed
				cp.mu.Unlock()
				cp.logger.Error("Failed to start component %s: %v", comp.Name(), err)
				_ = cp.Stop(context.Background())
				return fmt.Errorf("component %s failed to start: %w", comp.Name(), err)
			}
		}
	}

	cp.mu.Lock()
	cp.status = StatusRunning
	cp.mu.Unlock()

	cp.logger.Info("CloudX Control Plane is READY")
	return nil
}

// Stop gracefully terminates the control plane and all managed components.
func (cp *ControlPlane) Stop(ctx context.Context) error {
	cp.mu.Lock()
	if cp.status == StatusStopped || cp.status == StatusStopping {
		cp.mu.Unlock()
		return nil
	}

	cp.status = StatusStopping
	if cp.cancel != nil {
		cp.cancel()
	}
	cp.mu.Unlock()

	cp.logger.Info("Stopping CloudX Control Plane gracefully...")

	// Stop components in reverse dependency order
	var stopErrs []string
	for i := len(cp.components) - 1; i >= 0; i-- {
		comp := cp.components[i]
		cp.logger.Debug("Stopping component: %s", comp.Name())
		if err := comp.Stop(ctx); err != nil {
			cp.logger.Error("Error stopping component %s: %v", comp.Name(), err)
			stopErrs = append(stopErrs, fmt.Sprintf("%s: %v", comp.Name(), err))
		}
	}

	cp.mu.Lock()
	cp.status = StatusStopped
	cp.mu.Unlock()

	cp.logger.Info("CloudX Control Plane STOPPED")
	if len(stopErrs) > 0 {
		return fmt.Errorf("errors during control plane shutdown: %v", stopErrs)
	}
	return nil
}
