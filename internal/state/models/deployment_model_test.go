package models

import (
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
)

func TestImmutableDeployment_HashAndIdentity(t *testing.T) {
	now := time.Now().UTC()
	srvID := id.NewServiceID()
	depID := id.NewDeploymentID()

	cfg1 := DeploymentConfig{
		Command:  "./server --port 8080",
		Artifact: "ghcr.io/cloudx/api:v1.0.0",
		Runtime:  "native",
		Replicas: 3,
		Environment: map[string]string{
			"PORT": "8080",
			"ENV":  "production",
		},
		Resources: ResourceRequirements{
			CPU:    1.5,
			Memory: 512 * 1024 * 1024,
		},
		RestartPolicy: RestartPolicy{
			Type: RestartPolicyAlways,
		},
		Ports: []PortMapping{
			{HostPort: 8080, ServicePort: 8080, Protocol: "tcp"},
		},
	}

	dep1 := &ImmutableDeployment{
		ID:          depID,
		ServiceID:   srvID,
		ServiceName: "api",
		Version:     "v1",
		ConfigHash:  cfg1.ComputeHash(),
		Status:      DeploymentStatusActive,
		Config:      cfg1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := dep1.Validate(); err != nil {
		t.Fatalf("expected dep1 to be valid, got: %v", err)
	}

	hash1 := dep1.ConfigHash
	if hash1 == "" {
		t.Fatalf("expected non-empty config hash")
	}

	// Recompute hash with map keys inserted in different order
	cfg2 := DeploymentConfig{
		Command:  "./server --port 8080",
		Artifact: "ghcr.io/cloudx/api:v1.0.0",
		Runtime:  "native",
		Replicas: 3,
		Environment: map[string]string{
			"ENV":  "production",
			"PORT": "8080",
		},
		Resources: ResourceRequirements{
			CPU:    1.5,
			Memory: 512 * 1024 * 1024,
		},
		RestartPolicy: RestartPolicy{
			Type: RestartPolicyAlways,
		},
		Ports: []PortMapping{
			{HostPort: 8080, ServicePort: 8080, Protocol: "tcp"},
		},
	}

	hash2 := cfg2.ComputeHash()
	if hash1 != hash2 {
		t.Errorf("expected deterministic hash across map iterations, got %s vs %s", hash1, hash2)
	}

	// Change configuration (e.g. replicas or env) -> verify hash changes
	cfg3 := cfg2
	cfg3.Replicas = 5
	hash3 := cfg3.ComputeHash()
	if hash1 == hash3 {
		t.Errorf("expected different config hash when replicas changed from 3 to 5")
	}

	// Verify fingerprint
	fp := dep1.Fingerprint()
	if fp == "" || !testing.Short() && len(fp) < 10 {
		t.Errorf("unexpected fingerprint: %s", fp)
	}
}

func TestImmutableDeployment_Serialization(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	srvID := id.NewServiceID()
	depID := id.NewDeploymentID()

	dep := &ImmutableDeployment{
		ID:          depID,
		ServiceID:   srvID,
		ServiceName: "worker-proc",
		Version:     "v2",
		ConfigHash:  "abcd1234efgh5678",
		Status:      DeploymentStatusActive,
		Config: DeploymentConfig{
			Command:  "./worker",
			Artifact: "bin/worker",
			Runtime:  "native",
			Replicas: 2,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	specJSON, err := dep.ToSpecJSON()
	if err != nil {
		t.Fatalf("failed to serialize deployment: %v", err)
	}

	model := &Deployment{
		ID:        dep.ID,
		ServiceID: dep.ServiceID,
		Version:   dep.Version,
		Status:    string(dep.Status),
		SpecJSON:  specJSON,
		CreatedAt: dep.CreatedAt,
		UpdatedAt: dep.UpdatedAt,
	}

	restored, err := DeploymentFromModel(model)
	if err != nil {
		t.Fatalf("failed to restore deployment from model: %v", err)
	}

	if restored.ID != dep.ID {
		t.Errorf("expected ID %s, got %s", dep.ID, restored.ID)
	}
	if restored.ServiceName != dep.ServiceName {
		t.Errorf("expected service name %s, got %s", dep.ServiceName, restored.ServiceName)
	}
	if restored.Version != "v2" {
		t.Errorf("expected version v2, got %s", restored.Version)
	}
	if restored.ConfigHash != dep.ConfigHash {
		t.Errorf("expected config hash %s, got %s", dep.ConfigHash, restored.ConfigHash)
	}
	if restored.Config.Artifact != "bin/worker" {
		t.Errorf("expected artifact 'bin/worker', got %s", restored.Config.Artifact)
	}
}
