package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/registry"
	"github.com/cloudx-org/cloudx/internal/spec"
	"github.com/cloudx-org/cloudx/internal/state"
	"github.com/cloudx-org/cloudx/internal/state/models"
)

// RegistryManager coordinates service endpoint registration and discovery within the control plane.
type RegistryManager struct {
	mu       sync.RWMutex
	store    state.Store
	registry *registry.InMemoryRegistry
	cancel   context.CancelFunc
}

// NewRegistryManager constructs a new RegistryManager component.
func NewRegistryManager(store state.Store) *RegistryManager {
	return &RegistryManager{
		store:    store,
		registry: registry.NewInMemoryRegistry(nil),
	}
}

func (rm *RegistryManager) Name() string { return "RegistryManager" }

func (rm *RegistryManager) Start(ctx context.Context) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	// Initial population from state store
	if rm.store != nil {
		_ = rm.registry.Refresh(ctx, rm.store)
	}
	return nil
}

func (rm *RegistryManager) Stop(ctx context.Context) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if rm.cancel != nil {
		rm.cancel()
		rm.cancel = nil
	}
	return nil
}

// ServiceRegistry returns the underlying ServiceRegistry interface.
func (rm *RegistryManager) ServiceRegistry() registry.ServiceRegistry {
	return rm.registry
}

// Refresh triggers an explicit synchronization pass against cluster state.
func (rm *RegistryManager) Refresh(ctx context.Context) error {
	rm.mu.RLock()
	store := rm.store
	rm.mu.RUnlock()

	if store == nil {
		return fmt.Errorf("state store is nil")
	}
	return rm.registry.Refresh(ctx, store)
}

// OnTaskStateChange updates service endpoints dynamically when a task changes state.
func (rm *RegistryManager) OnTaskStateChange(ctx context.Context, task *models.Task) error {
	if task == nil || task.ServiceID == "" {
		return nil
	}

	stateUpper := strings.ToUpper(task.State)
	isUsable := stateUpper == string(models.TaskStateRunning) || stateUpper == string(models.TaskStateHealthy)

	if !isUsable {
		// Task is stopped, failed, lost, restarting, etc. -> deregister endpoint
		return rm.registry.Deregister(task.ID)
	}

	// Fetch worker and service to construct endpoint
	rm.mu.RLock()
	store := rm.store
	rm.mu.RUnlock()

	if store == nil {
		return fmt.Errorf("state store is nil")
	}

	worker, err := store.Workers().Get(ctx, task.WorkerID)
	if err != nil || worker == nil || strings.ToUpper(worker.Status) != "READY" {
		// Worker is not ready or doesn't exist -> remove endpoint
		return rm.registry.Deregister(task.ID)
	}

	svc, err := store.Services().Get(ctx, task.ServiceID)
	if err != nil || svc == nil {
		return rm.registry.Deregister(task.ID)
	}

	host := resolveHostFromAddr(worker.Address)
	if host == "" {
		host = "127.0.0.1"
	}

	port, proto, networks := resolveDetailsFromSpec(svc)
	var formattedAddr string
	if port > 0 {
		formattedAddr = net.JoinHostPort(host, strconv.Itoa(port))
	} else {
		formattedAddr = host
	}

	ep := &registry.Endpoint{
		ServiceID:   svc.ID,
		ServiceName: svc.Name,
		TaskID:      task.ID,
		WorkerID:    worker.ID,
		Host:        host,
		Port:        port,
		Address:     formattedAddr,
		Protocol:    proto,
		Networks:    networks,
		Healthy:     true,
		UpdatedAt:   time.Now().UTC(),
	}

	return rm.registry.Register(ep)
}

// OnWorkerStatusChange updates service endpoints when a worker changes health status.
func (rm *RegistryManager) OnWorkerStatusChange(ctx context.Context, workerID id.ID, newStatus string) error {
	if strings.ToUpper(newStatus) == "READY" {
		// Worker recovered or registered -> full refresh
		return rm.Refresh(ctx)
	}

	// Worker is SUSPECTED, UNHEALTHY, or LOST -> remove all endpoints on this worker
	rm.mu.RLock()
	store := rm.store
	rm.mu.RUnlock()

	if store != nil {
		tasks, err := store.Tasks().ListByWorker(ctx, workerID)
		if err == nil {
			for _, t := range tasks {
				_ = rm.registry.Deregister(t.ID)
			}
		}
	}
	return nil
}

// GetServiceEndpoints returns all healthy endpoints for a service name or ID.
func (cp *ControlPlane) GetServiceEndpoints(ctx context.Context, nameOrID string) ([]*registry.Endpoint, error) {
	return cp.ResolveService(ctx, nameOrID)
}

// ResolveService provides the core discovery interface: ResolveService("api") -> healthy endpoints.
func (cp *ControlPlane) ResolveService(ctx context.Context, serviceNameOrID string) ([]*registry.Endpoint, error) {
	if cp.RegistryManager == nil {
		return nil, fmt.Errorf("registry manager is not initialized")
	}

	// Ensure registry is synchronized with state store
	_ = cp.RegistryManager.Refresh(ctx)

	// First try lookup by name
	eps := cp.RegistryManager.ServiceRegistry().Lookup(serviceNameOrID)
	if len(eps) > 0 {
		return eps, nil
	}

	// Then try lookup by ID
	eps = cp.RegistryManager.ServiceRegistry().LookupByID(id.ID(serviceNameOrID))
	if len(eps) > 0 {
		return eps, nil
	}

	// Check if service exists in cluster
	inspectRes, err := cp.InspectService(ctx, serviceNameOrID)
	if err != nil {
		return nil, err
	}

	// Service exists but has no active healthy endpoints
	eps = cp.RegistryManager.ServiceRegistry().Lookup(inspectRes.Service.Name)
	return eps, nil
}

// ResolveServiceOne returns a single healthy endpoint for a service.
func (cp *ControlPlane) ResolveServiceOne(ctx context.Context, serviceNameOrID string) (*registry.Endpoint, error) {
	eps, err := cp.ResolveService(ctx, serviceNameOrID)
	if err != nil {
		return nil, err
	}
	if len(eps) == 0 {
		return nil, fmt.Errorf("no healthy endpoints available for service '%s'", serviceNameOrID)
	}
	return eps[0], nil
}

// ResolveServiceInNetwork returns healthy endpoints for a service filtered within a logical network.
func (cp *ControlPlane) ResolveServiceInNetwork(ctx context.Context, serviceNameOrID, networkName string) ([]*registry.Endpoint, error) {
	if cp.RegistryManager == nil {
		return nil, fmt.Errorf("registry manager is not initialized")
	}

	_ = cp.RegistryManager.Refresh(ctx)
	return cp.RegistryManager.ServiceRegistry().LookupServiceInNetwork(serviceNameOrID, networkName), nil
}

// ResolveNetwork returns all healthy endpoints belonging to a given logical network.
func (cp *ControlPlane) ResolveNetwork(ctx context.Context, networkName string) ([]*registry.Endpoint, error) {
	if cp.RegistryManager == nil {
		return nil, fmt.Errorf("registry manager is not initialized")
	}

	_ = cp.RegistryManager.Refresh(ctx)
	return cp.RegistryManager.ServiceRegistry().LookupByNetwork(networkName), nil
}

// ServiceResolver returns a ServiceResolver instance backed by the ControlPlane.
func (cp *ControlPlane) ServiceResolver() registry.ServiceResolver {
	if cp.RegistryManager == nil {
		return nil
	}
	return registry.NewStoreResolver(cp.RegistryManager.ServiceRegistry(), cp.StateManager.Store())
}

// ListAllEndpoints returns a map of service names to healthy endpoints.
func (cp *ControlPlane) ListAllEndpoints(ctx context.Context) (map[string][]*registry.Endpoint, error) {
	if cp.RegistryManager == nil {
		return nil, fmt.Errorf("registry manager is not initialized")
	}

	_ = cp.RegistryManager.Refresh(ctx)
	return cp.RegistryManager.ServiceRegistry().ListAll(), nil
}

// RefreshServiceRegistry triggers synchronization of the service registry.
func (cp *ControlPlane) RefreshServiceRegistry(ctx context.Context) error {
	if cp.RegistryManager == nil {
		return fmt.Errorf("registry manager is not initialized")
	}
	return cp.RegistryManager.Refresh(ctx)
}

func resolveHostFromAddr(addr string) string {
	if addr == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(addr)
	if err == nil && host != "" {
		return host
	}
	return addr
}

func resolveDetailsFromSpec(svc *models.Service) (int, string, []string) {
	if svc == nil {
		return 0, "tcp", nil
	}
	if svc.SpecJSON != "" {
		var cfg spec.ServiceConfig
		if err := json.Unmarshal([]byte(svc.SpecJSON), &cfg); err == nil {
			portVal := 0
			proto := "tcp"
			if len(cfg.Ports) > 0 {
				p := cfg.Ports[0]
				portVal = p.HostPort
				if portVal <= 0 {
					portVal = p.ServicePort
				}
				if p.Protocol != "" {
					proto = p.Protocol
				}
			} else if cfg.HealthCheck != nil && cfg.HealthCheck.Port > 0 {
				portVal = cfg.HealthCheck.Port
			}
			return portVal, proto, cfg.Networks
		}
	}
	return 0, "tcp", nil
}
