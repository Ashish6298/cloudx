package registry

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/state"
)

// ServiceResolver provides a high-level service discovery interface for applications
// to resolve service names into active, healthy network endpoints without hardcoding worker addresses.
type ServiceResolver interface {
	// ResolveService returns all healthy endpoints for the specified service name or ID.
	ResolveService(ctx context.Context, serviceNameOrID string) ([]*Endpoint, error)
	// ResolveOne returns a single healthy endpoint for the specified service (e.g., for quick client connections).
	ResolveOne(ctx context.Context, serviceNameOrID string) (*Endpoint, error)
	// ResolveServiceInNetwork returns healthy endpoints for a service scoped to a specific logical network.
	ResolveServiceInNetwork(ctx context.Context, serviceNameOrID, networkName string) ([]*Endpoint, error)
	// ResolveNetwork returns all healthy endpoints belonging to a given logical network.
	ResolveNetwork(ctx context.Context, networkName string) ([]*Endpoint, error)
}

// StoreResolver implements ServiceResolver directly against the cluster state store and registry.
type StoreResolver struct {
	registry ServiceRegistry
	store    state.Store
}

// NewStoreResolver creates a new ServiceResolver backed by a ServiceRegistry and state.Store.
func NewStoreResolver(reg ServiceRegistry, store state.Store) *StoreResolver {
	return &StoreResolver{
		registry: reg,
		store:    store,
	}
}

// ResolveService returns all healthy endpoints for a given service name (case-insensitive) or service ID.
func (r *StoreResolver) ResolveService(ctx context.Context, serviceNameOrID string) ([]*Endpoint, error) {
	if strings.TrimSpace(serviceNameOrID) == "" {
		return nil, fmt.Errorf("service name or ID must not be empty")
	}

	// 1. If store is provided, ensure registry has latest snapshot
	if r.store != nil {
		_ = r.registry.Refresh(ctx, r.store)
	}

	// 2. Lookup by service name
	eps := r.registry.Lookup(serviceNameOrID)
	if len(eps) > 0 {
		return eps, nil
	}

	// 3. Lookup by service ID
	eps = r.registry.LookupByID(id.ID(serviceNameOrID))
	if len(eps) > 0 {
		return eps, nil
	}

	// 4. If store is available, verify if service exists at all
	if r.store != nil {
		svc, err := r.store.Services().GetByName(ctx, serviceNameOrID)
		if err == nil && svc != nil {
			// Service exists in cluster, but no healthy endpoints currently available
			return []*Endpoint{}, nil
		}

		svcByID, err := r.store.Services().Get(ctx, id.ID(serviceNameOrID))
		if err == nil && svcByID != nil {
			return []*Endpoint{}, nil
		}

		return nil, fmt.Errorf("service '%s' not found", serviceNameOrID)
	}

	return []*Endpoint{}, nil
}

// ResolveServiceInNetwork returns healthy endpoints for a service scoped to a specific logical network.
func (r *StoreResolver) ResolveServiceInNetwork(ctx context.Context, serviceNameOrID, networkName string) ([]*Endpoint, error) {
	if strings.TrimSpace(serviceNameOrID) == "" {
		return nil, fmt.Errorf("service name or ID must not be empty")
	}
	if strings.TrimSpace(networkName) == "" {
		return r.ResolveService(ctx, serviceNameOrID)
	}

	if r.store != nil {
		_ = r.registry.Refresh(ctx, r.store)
	}

	eps := r.registry.LookupServiceInNetwork(serviceNameOrID, networkName)
	return eps, nil
}

// ResolveNetwork returns all healthy endpoints belonging to a given logical network.
func (r *StoreResolver) ResolveNetwork(ctx context.Context, networkName string) ([]*Endpoint, error) {
	if strings.TrimSpace(networkName) == "" {
		return nil, fmt.Errorf("network name must not be empty")
	}

	if r.store != nil {
		_ = r.registry.Refresh(ctx, r.store)
	}

	eps := r.registry.LookupByNetwork(networkName)
	return eps, nil
}

// ResolveOne returns the first healthy endpoint for a given service, or an error if none are available.
func (r *StoreResolver) ResolveOne(ctx context.Context, serviceNameOrID string) (*Endpoint, error) {
	eps, err := r.ResolveService(ctx, serviceNameOrID)
	if err != nil {
		return nil, err
	}
	if len(eps) == 0 {
		return nil, fmt.Errorf("no healthy endpoints available for service '%s'", serviceNameOrID)
	}
	return eps[0], nil
}
