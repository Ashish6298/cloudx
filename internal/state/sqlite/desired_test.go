package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/state/models"
)

func TestDesiredStateCRUD(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	// 1. Create desired service
	desired := &models.ServiceDesiredState{
		ID:       id.NewServiceID(),
		Name:     "web-api",
		Version:  "v1.0.0",
		Replicas: 3,
		Runtime:  "native",
		Command:  "./server --port 8080",
		Environment: map[string]string{
			"ENV":  "production",
			"PORT": "8080",
		},
		Ports: []models.PortMapping{
			{HostPort: 8080, ServicePort: 8080, Protocol: "tcp"},
		},
		Resources: models.ResourceRequirements{
			CPU:    1.5,
			Memory: 536870912, // 512MB
		},
		RestartPolicy: models.RestartPolicy{
			Type: models.RestartPolicyAlways,
		},
		HealthCheck: &models.HealthCheckSpec{
			Type:             models.HealthCheckHTTP,
			Path:             "/healthz",
			Port:             8080,
			Interval:         5 * time.Second,
			Timeout:          2 * time.Second,
			FailureThreshold: 3,
			SuccessThreshold: 1,
		},
		Volumes: []models.VolumeMount{
			{VolumeName: "api-storage", MountPath: "/data", ReadOnly: false},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := store.DesiredState().Create(ctx, desired); err != nil {
		t.Fatalf("failed to create desired state: %v", err)
	}

	// 2. Read & verify
	loaded, err := store.DesiredState().Get(ctx, desired.ID)
	if err != nil {
		t.Fatalf("failed to load desired state: %v", err)
	}

	if loaded.Name != "web-api" {
		t.Errorf("expected name 'web-api', got %q", loaded.Name)
	}
	if loaded.Version != "v1.0.0" {
		t.Errorf("expected version 'v1.0.0', got %q", loaded.Version)
	}
	if loaded.Replicas != 3 {
		t.Errorf("expected 3 replicas, got %d", loaded.Replicas)
	}
	if loaded.Environment["ENV"] != "production" {
		t.Errorf("expected ENV=production, got %q", loaded.Environment["ENV"])
	}
	if loaded.HealthCheck == nil || loaded.HealthCheck.Path != "/healthz" {
		t.Errorf("expected health check path /healthz, got %v", loaded.HealthCheck)
	}

	// 3. Update desired replicas
	loaded.Replicas = 5
	if err := store.DesiredState().Update(ctx, loaded); err != nil {
		t.Fatalf("failed to update desired replicas: %v", err)
	}

	updated, err := store.DesiredState().GetByName(ctx, "web-api")
	if err != nil || updated.Replicas != 5 {
		t.Fatalf("expected 5 replicas after update, got %d (err: %v)", updated.Replicas, err)
	}

	// 4. Change version (e.g. rollout)
	updated.Version = "v2.0.0"
	if err := store.DesiredState().Update(ctx, updated); err != nil {
		t.Fatalf("failed to update version: %v", err)
	}

	v2, err := store.DesiredState().Get(ctx, desired.ID)
	if err != nil || v2.Version != "v2.0.0" {
		t.Fatalf("expected version 'v2.0.0', got %q", v2.Version)
	}

	// 5. Delete desired state
	if err := store.DesiredState().Delete(ctx, desired.ID); err != nil {
		t.Fatalf("failed to delete desired state: %v", err)
	}

	_, err = store.DesiredState().Get(ctx, desired.ID)
	if err == nil {
		t.Fatalf("expected error getting deleted desired state, got nil")
	}
}

func TestDesiredStatePersistAndReload(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "persist_desired.db")
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	srvID := id.NewServiceID()

	// Store desired state on disk
	{
		store, err := Open(ctx, dbPath)
		if err != nil {
			t.Fatalf("failed to open store: %v", err)
		}

		desired := &models.ServiceDesiredState{
			ID:        srvID,
			Name:      "persistent-service",
			Version:   "v1.2.3",
			Replicas:  4,
			Runtime:   "native",
			Command:   "run-app",
			CreatedAt: now,
			UpdatedAt: now,
		}

		if err := store.DesiredState().Create(ctx, desired); err != nil {
			t.Fatalf("failed to create desired state: %v", err)
		}
		_ = store.Close()
	}

	// Reload from disk in a fresh store instance
	{
		reloadedStore, err := Open(ctx, dbPath)
		if err != nil {
			t.Fatalf("failed to reload store: %v", err)
		}
		defer reloadedStore.Close()

		reloaded, err := reloadedStore.DesiredState().Get(ctx, srvID)
		if err != nil {
			t.Fatalf("failed to fetch reloaded state: %v", err)
		}

		if reloaded.Name != "persistent-service" || reloaded.Replicas != 4 || reloaded.Version != "v1.2.3" {
			t.Errorf("reloaded state mismatch: %+v", reloaded)
		}
	}
}

func TestInvalidDesiredState(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	invalidStates := []*models.ServiceDesiredState{
		{Name: "", Version: "v1", Command: "app"},                           // empty name
		{Name: "app", Version: "", Command: "app"},                          // empty version
		{Name: "app", Version: "v1", Command: ""},                           // empty command
		{Name: "app", Version: "v1", Command: "app", Replicas: -1},          // negative replicas
		{Name: "app", Version: "v1", Command: "app", Runtime: "kubernetes"}, // invalid runtime
		{Name: "app", Version: "v1", Command: "app", RestartPolicy: models.RestartPolicy{Type: "invalid-policy"}},
		{
			Name: "app", Version: "v1", Command: "app",
			Ports: []models.PortMapping{{HostPort: 99999, ServicePort: 80}}, // invalid port
		},
		{
			Name: "app", Version: "v1", Command: "app",
			HealthCheck: &models.HealthCheckSpec{Type: "invalid-probe", Interval: 10 * time.Millisecond},
		},
	}

	for i, inv := range invalidStates {
		inv.ID = id.NewServiceID()
		err := store.DesiredState().Create(ctx, inv)
		if err == nil {
			t.Errorf("expected validation failure for invalid case [%d], got nil", i)
		}
		if !strings.Contains(err.Error(), "invalid desired state") {
			t.Errorf("expected 'invalid desired state' error message, got: %v", err)
		}
	}
}
