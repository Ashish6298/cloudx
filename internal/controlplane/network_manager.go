package controlplane

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/registry"
	"github.com/cloudx-org/cloudx/internal/state/models"
)

// NetworkCreateOptions configures parameters for creating a logical network.
type NetworkCreateOptions struct {
	Name   string `json:"name"`
	Subnet string `json:"subnet,omitempty"`
}

// NetworkCreateResult holds the outcome of creating a logical network.
type NetworkCreateResult struct {
	NetworkID   id.ID     `json:"network_id"`
	NetworkName string    `json:"network_name"`
	Subnet      string    `json:"subnet"`
	CreatedAt   time.Time `json:"created_at"`
}

// CreateNetwork provisions a new logical CloudX network.
func (cp *ControlPlane) CreateNetwork(ctx context.Context, opts NetworkCreateOptions) (*NetworkCreateResult, error) {
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		return nil, fmt.Errorf("network name is required")
	}

	store := cp.StateManager.Store()
	if store == nil {
		return nil, fmt.Errorf("state store is not available")
	}

	// Check for duplicate network name
	existing, _ := store.Networks().GetByName(ctx, name)
	if existing != nil {
		return nil, fmt.Errorf("network '%s' already exists", name)
	}

	subnet := strings.TrimSpace(opts.Subnet)
	if subnet == "" {
		// Assign standard logical network subnet prefix if unspecified
		subnet = "10.244.0.0/16"
	}

	now := time.Now().UTC()
	netID := id.NewNetworkID()

	netRecord := &models.Network{
		ID:        netID,
		Name:      name,
		Subnet:    subnet,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := store.Networks().Create(ctx, netRecord); err != nil {
		return nil, fmt.Errorf("failed to persist network record: %w", err)
	}

	// Append NETWORK_CREATED audit event
	_ = store.Events().Append(ctx, &models.Event{
		ID:        id.NewEventID(),
		Type:      "NETWORK_CREATED",
		Source:    "controlplane",
		EntityID:  netID,
		Payload:   fmt.Sprintf(`{"network_id":"%s","name":"%s","subnet":"%s"}`, netID, name, subnet),
		CreatedAt: now,
	})

	cp.logger.Info("Created logical network '%s' (ID: %s, Subnet: %s)", name, netID, subnet)

	return &NetworkCreateResult{
		NetworkID:   netID,
		NetworkName: name,
		Subnet:      subnet,
		CreatedAt:   now,
	}, nil
}

// ListNetworks returns all logical networks in the cluster.
func (cp *ControlPlane) ListNetworks(ctx context.Context) ([]*models.Network, error) {
	store := cp.StateManager.Store()
	if store == nil {
		return nil, fmt.Errorf("state store is not available")
	}

	return store.Networks().List(ctx)
}

// NetworkInspectResult provides inspection details for a logical network.
type NetworkInspectResult struct {
	Network   *models.Network      `json:"network"`
	Services  []string             `json:"services"`
	Endpoints []*registry.Endpoint `json:"endpoints"`
}

// InspectNetwork retrieves detailed information and active member services/endpoints for a logical network.
func (cp *ControlPlane) InspectNetwork(ctx context.Context, nameOrID string) (*NetworkInspectResult, error) {
	store := cp.StateManager.Store()
	if store == nil {
		return nil, fmt.Errorf("state store is not available")
	}

	netRecord, err := cp.resolveNetwork(ctx, nameOrID)
	if err != nil {
		return nil, err
	}

	// Fetch active healthy endpoints in this logical network
	var eps []*registry.Endpoint
	if cp.RegistryManager != nil {
		_ = cp.RegistryManager.Refresh(ctx)
		eps = cp.RegistryManager.ServiceRegistry().LookupByNetwork(netRecord.Name)
	}

	// Collect unique services belonging to this network
	svcSet := make(map[string]bool)
	for _, ep := range eps {
		svcSet[ep.ServiceName] = true
	}

	// Also scan all registered services in store to find declared network members
	services, _ := store.Services().List(ctx)
	for _, s := range services {
		_, _, nets := resolveDetailsFromSpec(s)
		for _, n := range nets {
			if strings.EqualFold(n, netRecord.Name) {
				svcSet[s.Name] = true
			}
		}
	}

	memberServices := make([]string, 0, len(svcSet))
	for svcName := range svcSet {
		memberServices = append(memberServices, svcName)
	}

	return &NetworkInspectResult{
		Network:   netRecord,
		Services:  memberServices,
		Endpoints: eps,
	}, nil
}

// DeleteNetwork removes a logical network from the cluster.
func (cp *ControlPlane) DeleteNetwork(ctx context.Context, nameOrID string) error {
	store := cp.StateManager.Store()
	if store == nil {
		return fmt.Errorf("state store is not available")
	}

	netRecord, err := cp.resolveNetwork(ctx, nameOrID)
	if err != nil {
		return err
	}

	// Check if any active service references this network
	services, _ := store.Services().List(ctx)
	for _, s := range services {
		if strings.ToUpper(s.Status) == "STOPPED" {
			continue
		}
		_, _, nets := resolveDetailsFromSpec(s)
		for _, n := range nets {
			if strings.EqualFold(n, netRecord.Name) {
				return fmt.Errorf("cannot delete network '%s': active service '%s' is attached to this network", netRecord.Name, s.Name)
			}
		}
	}

	if err := store.Networks().Delete(ctx, netRecord.ID); err != nil {
		return fmt.Errorf("failed to delete network record: %w", err)
	}

	_ = store.Events().Append(ctx, &models.Event{
		ID:        id.NewEventID(),
		Type:      "NETWORK_DELETED",
		Source:    "controlplane",
		EntityID:  netRecord.ID,
		Payload:   fmt.Sprintf(`{"network_id":"%s","name":"%s"}`, netRecord.ID, netRecord.Name),
		CreatedAt: time.Now().UTC(),
	})

	cp.logger.Info("Deleted logical network '%s' (ID: %s)", netRecord.Name, netRecord.ID)
	return nil
}

func (cp *ControlPlane) resolveNetwork(ctx context.Context, nameOrID string) (*models.Network, error) {
	store := cp.StateManager.Store()
	if store == nil {
		return nil, fmt.Errorf("state store is not available")
	}

	trimmed := strings.TrimSpace(nameOrID)
	if trimmed == "" {
		return nil, fmt.Errorf("network name or ID is required")
	}

	// 1. Try by ID
	n, err := store.Networks().Get(ctx, id.ID(trimmed))
	if err == nil && n != nil {
		return n, nil
	}

	// 2. Try by Name
	n, err = store.Networks().GetByName(ctx, trimmed)
	if err == nil && n != nil {
		return n, nil
	}

	return nil, fmt.Errorf("network '%s' not found", trimmed)
}
