package registry

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
	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/spec"
	"github.com/cloudx-org/cloudx/internal/state"
	"github.com/cloudx-org/cloudx/internal/state/models"
)

// Endpoint represents a healthy, reachable network endpoint for a service task.
type Endpoint struct {
	ServiceID   id.ID     `json:"service_id"`
	ServiceName string    `json:"service_name"`
	TaskID      id.ID     `json:"task_id"`
	WorkerID    id.ID     `json:"worker_id"`
	Host        string    `json:"host"`
	Port        int       `json:"port"`
	Address     string    `json:"address"` // Formatted as host:port
	Protocol    string    `json:"protocol"` // e.g. "tcp", "udp", "http"
	Healthy     bool      `json:"healthy"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ServiceRegistry maintains the mapping from service name to healthy task endpoints.
type ServiceRegistry interface {
	// Register adds or updates an endpoint in the registry.
	Register(ep *Endpoint) error
	// Deregister removes a specific task endpoint from the registry.
	Deregister(taskID id.ID) error
	// Lookup returns all currently healthy endpoints for a given service name.
	Lookup(serviceName string) []*Endpoint
	// LookupByID returns all currently healthy endpoints for a given service ID.
	LookupByID(serviceID id.ID) []*Endpoint
	// ListAll returns a snapshot map of all registered service names to their healthy endpoints.
	ListAll() map[string][]*Endpoint
	// Refresh synchronizes the registry against the authoritative cluster state store.
	Refresh(ctx context.Context, store state.Store) error
	// Clear flushes all endpoints from the registry.
	Clear()
}

// InMemoryRegistry is a thread-safe in-memory implementation of ServiceRegistry.
type InMemoryRegistry struct {
	mu        sync.RWMutex
	endpoints map[id.ID]*Endpoint // Keyed by TaskID
	byService map[string]map[id.ID]*Endpoint
	bySvcID   map[id.ID]map[id.ID]*Endpoint
	logger    logging.Logger
}

// NewInMemoryRegistry creates a new initialized InMemoryRegistry.
func NewInMemoryRegistry(logger logging.Logger) *InMemoryRegistry {
	if logger == nil {
		logger = logging.NewDefaultLogger()
	}
	return &InMemoryRegistry{
		endpoints: make(map[id.ID]*Endpoint),
		byService: make(map[string]map[id.ID]*Endpoint),
		bySvcID:   make(map[id.ID]map[id.ID]*Endpoint),
		logger:    logger.With("component", "service_registry"),
	}
}

// Register adds or updates an endpoint. Only healthy endpoints are stored.
func (r *InMemoryRegistry) Register(ep *Endpoint) error {
	if ep == nil {
		return fmt.Errorf("endpoint is nil")
	}
	if ep.TaskID == "" {
		return fmt.Errorf("endpoint TaskID is required")
	}
	if ep.ServiceName == "" {
		return fmt.Errorf("endpoint ServiceName is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	svcKey := strings.ToLower(ep.ServiceName)

	// If marked unhealthy, deregister if present
	if !ep.Healthy {
		r.removeLocked(ep.TaskID)
		return nil
	}

	// Remove old entry if existed under different key
	r.removeLocked(ep.TaskID)

	epCopy := *ep
	if epCopy.Address == "" && epCopy.Host != "" && epCopy.Port > 0 {
		epCopy.Address = net.JoinHostPort(epCopy.Host, strconv.Itoa(epCopy.Port))
	}
	if epCopy.UpdatedAt.IsZero() {
		epCopy.UpdatedAt = time.Now().UTC()
	}

	r.endpoints[ep.TaskID] = &epCopy

	if r.byService[svcKey] == nil {
		r.byService[svcKey] = make(map[id.ID]*Endpoint)
	}
	r.byService[svcKey][ep.TaskID] = &epCopy

	if ep.ServiceID != "" {
		if r.bySvcID[ep.ServiceID] == nil {
			r.bySvcID[ep.ServiceID] = make(map[id.ID]*Endpoint)
		}
		r.bySvcID[ep.ServiceID][ep.TaskID] = &epCopy
	}

	return nil
}

// Deregister removes a task's endpoint.
func (r *InMemoryRegistry) Deregister(taskID id.ID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removeLocked(taskID)
	return nil
}

func (r *InMemoryRegistry) removeLocked(taskID id.ID) {
	existing, ok := r.endpoints[taskID]
	if !ok {
		return
	}

	delete(r.endpoints, taskID)

	svcKey := strings.ToLower(existing.ServiceName)
	if svcMap, ok := r.byService[svcKey]; ok {
		delete(svcMap, taskID)
		if len(svcMap) == 0 {
			delete(r.byService, svcKey)
		}
	}

	if existing.ServiceID != "" {
		if idMap, ok := r.bySvcID[existing.ServiceID]; ok {
			delete(idMap, taskID)
			if len(idMap) == 0 {
				delete(r.bySvcID, existing.ServiceID)
			}
		}
	}
}

// Lookup returns all healthy endpoints for the specified service name (case-insensitive).
func (r *InMemoryRegistry) Lookup(serviceName string) []*Endpoint {
	r.mu.RLock()
	defer r.mu.RUnlock()

	svcKey := strings.ToLower(serviceName)
	svcMap, ok := r.byService[svcKey]
	if !ok || len(svcMap) == 0 {
		return []*Endpoint{}
	}

	result := make([]*Endpoint, 0, len(svcMap))
	for _, ep := range svcMap {
		epCopy := *ep
		result = append(result, &epCopy)
	}
	return result
}

// LookupByID returns all healthy endpoints for the specified service ID.
func (r *InMemoryRegistry) LookupByID(serviceID id.ID) []*Endpoint {
	r.mu.RLock()
	defer r.mu.RUnlock()

	idMap, ok := r.bySvcID[serviceID]
	if !ok || len(idMap) == 0 {
		return []*Endpoint{}
	}

	result := make([]*Endpoint, 0, len(idMap))
	for _, ep := range idMap {
		epCopy := *ep
		result = append(result, &epCopy)
	}
	return result
}

// ListAll returns a snapshot copy of all services and their registered endpoints.
func (r *InMemoryRegistry) ListAll() map[string][]*Endpoint {
	r.mu.RLock()
	defer r.mu.RUnlock()

	res := make(map[string][]*Endpoint, len(r.byService))
	for svcKey, svcMap := range r.byService {
		eps := make([]*Endpoint, 0, len(svcMap))
		for _, ep := range svcMap {
			epCopy := *ep
			eps = append(eps, &epCopy)
		}
		res[svcKey] = eps
	}
	return res
}

// Clear flushes all endpoints from the registry.
func (r *InMemoryRegistry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.endpoints = make(map[id.ID]*Endpoint)
	r.byService = make(map[string]map[id.ID]*Endpoint)
	r.bySvcID = make(map[id.ID]map[id.ID]*Endpoint)
}

// Refresh reconciles the registry against the current authoritative state store.
// Only tasks that are in a healthy state (RUNNING or HEALTHY) on a worker in READY state
// with a valid host address are registered.
func (r *InMemoryRegistry) Refresh(ctx context.Context, store state.Store) error {
	if store == nil {
		return fmt.Errorf("state store is nil")
	}

	services, err := store.Services().List(ctx)
	if err != nil {
		return fmt.Errorf("failed to list services: %w", err)
	}

	workers, err := store.Workers().List(ctx)
	if err != nil {
		return fmt.Errorf("failed to list workers: %w", err)
	}

	tasks, err := store.Tasks().List(ctx)
	if err != nil {
		return fmt.Errorf("failed to list tasks: %w", err)
	}

	// Index services by ID
	svcMap := make(map[id.ID]*models.Service, len(services))
	for _, s := range services {
		svcMap[s.ID] = s
	}

	// Index workers by ID (only READY workers are usable)
	readyWorkers := make(map[id.ID]*models.Worker)
	for _, w := range workers {
		if strings.ToUpper(w.Status) == "READY" {
			readyWorkers[w.ID] = w
		}
	}

	// Build new endpoint set
	newEndpoints := make(map[id.ID]*Endpoint)

	for _, t := range tasks {
		// Only services, not finite jobs
		if t.ServiceID == "" {
			continue
		}

		// Task must be in a healthy usable state (RUNNING or HEALTHY)
		stateUpper := strings.ToUpper(t.State)
		if stateUpper != string(models.TaskStateRunning) && stateUpper != string(models.TaskStateHealthy) {
			continue
		}

		// Worker must be READY
		worker, ok := readyWorkers[t.WorkerID]
		if !ok || worker == nil {
			continue
		}

		svc, ok := svcMap[t.ServiceID]
		if !ok || svc == nil {
			continue
		}

		// Resolve host from worker address
		host := resolveHost(worker.Address)
		if host == "" {
			host = "127.0.0.1"
		}

		// Resolve port and protocol from service spec
		port, proto := resolveServicePort(svc)

		var formattedAddr string
		if port > 0 {
			formattedAddr = net.JoinHostPort(host, strconv.Itoa(port))
		} else {
			formattedAddr = host
		}

		ep := &Endpoint{
			ServiceID:   svc.ID,
			ServiceName: svc.Name,
			TaskID:      t.ID,
			WorkerID:    worker.ID,
			Host:        host,
			Port:        port,
			Address:     formattedAddr,
			Protocol:    proto,
			Healthy:     true,
			UpdatedAt:   time.Now().UTC(),
		}

		newEndpoints[t.ID] = ep
	}

	// Atomically swap into in-memory registry
	r.mu.Lock()
	defer r.mu.Unlock()

	r.endpoints = make(map[id.ID]*Endpoint, len(newEndpoints))
	r.byService = make(map[string]map[id.ID]*Endpoint)
	r.bySvcID = make(map[id.ID]map[id.ID]*Endpoint)

	for taskID, ep := range newEndpoints {
		r.endpoints[taskID] = ep
		svcKey := strings.ToLower(ep.ServiceName)
		if r.byService[svcKey] == nil {
			r.byService[svcKey] = make(map[id.ID]*Endpoint)
		}
		r.byService[svcKey][taskID] = ep

		if ep.ServiceID != "" {
			if r.bySvcID[ep.ServiceID] == nil {
				r.bySvcID[ep.ServiceID] = make(map[id.ID]*Endpoint)
			}
			r.bySvcID[ep.ServiceID][taskID] = ep
		}
	}

	return nil
}

// resolveHost extracts the IP/hostname from a worker address (which may be "host:port").
func resolveHost(addr string) string {
	if addr == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(addr)
	if err == nil && host != "" {
		return host
	}
	// No port separator, return as is
	return addr
}

// resolveServicePort inspects the service's SpecJSON or defaults to find exposed port and protocol.
func resolveServicePort(svc *models.Service) (int, string) {
	if svc == nil {
		return 0, "tcp"
	}

	if svc.SpecJSON != "" {
		var cfg spec.ServiceConfig
		if err := json.Unmarshal([]byte(svc.SpecJSON), &cfg); err == nil {
			if len(cfg.Ports) > 0 {
				p := cfg.Ports[0]
				portVal := p.HostPort
				if portVal <= 0 {
					portVal = p.ServicePort
				}
				proto := p.Protocol
				if proto == "" {
					proto = "tcp"
				}
				return portVal, proto
			}
			// Check HealthCheck port as fallback if specified
			if cfg.HealthCheck != nil && cfg.HealthCheck.Port > 0 {
				return cfg.HealthCheck.Port, "tcp"
			}
		}
	}

	return 0, "tcp"
}
