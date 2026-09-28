package worker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudx-org/cloudx/internal/common/id"
)

// BootstrapIdentity stores the secure bootstrap identity of the worker node.
type BootstrapIdentity struct {
	WorkerID       id.ID  `json:"worker_id"`
	NodeID         id.ID  `json:"node_id"`
	BootstrapToken string `json:"bootstrap_token,omitempty"`
}

// IdentityManager ensures a worker's ID and node identity remain stable across restarts.
type IdentityManager struct {
	storagePath string
}

func NewIdentityManager(storagePath string) *IdentityManager {
	return &IdentityManager{storagePath: storagePath}
}

// GetOrCreateIdentity returns existing worker ID from storage or generates a new one.
func (m *IdentityManager) GetOrCreateIdentity(explicitID string) (id.ID, error) {
	if explicitID != "" {
		_ = os.MkdirAll(m.storagePath, 0755)
		idFilePath := filepath.Join(m.storagePath, "worker.id")
		_ = os.WriteFile(idFilePath, []byte(strings.TrimSpace(explicitID)), 0600)
		return id.ID(explicitID), nil
	}

	if err := os.MkdirAll(m.storagePath, 0755); err != nil {
		return "", fmt.Errorf("failed to create worker storage directory: %w", err)
	}

	idFilePath := filepath.Join(m.storagePath, "worker.id")
	data, err := os.ReadFile(idFilePath)
	if err == nil {
		existingID := strings.TrimSpace(string(data))
		if existingID != "" {
			return id.ID(existingID), nil
		}
	}

	// Generate and persist new stable ID
	newID := id.NewWorkerID()
	if err := os.WriteFile(idFilePath, []byte(newID.String()), 0600); err != nil {
		return "", fmt.Errorf("failed to persist worker ID: %w", err)
	}

	return newID, nil
}

// GetOrCreateNodeIdentity returns existing node ID from storage or generates a new one.
func (m *IdentityManager) GetOrCreateNodeIdentity(explicitNodeID string) (id.ID, error) {
	if explicitNodeID != "" {
		return id.ID(explicitNodeID), nil
	}

	if err := os.MkdirAll(m.storagePath, 0755); err != nil {
		return "", fmt.Errorf("failed to create worker storage directory: %w", err)
	}

	nodeFilePath := filepath.Join(m.storagePath, "node.id")
	data, err := os.ReadFile(nodeFilePath)
	if err == nil {
		existingID := strings.TrimSpace(string(data))
		if existingID != "" {
			return id.ID(existingID), nil
		}
	}

	newID := id.NewNodeID()
	if err := os.WriteFile(nodeFilePath, []byte(newID.String()), 0600); err != nil {
		return "", fmt.Errorf("failed to persist node ID: %w", err)
	}

	return newID, nil
}

// SaveBootstrapToken stores the join token securely in the local worker storage.
func (m *IdentityManager) SaveBootstrapToken(token string) error {
	if token == "" {
		return nil
	}
	if err := os.MkdirAll(m.storagePath, 0755); err != nil {
		return fmt.Errorf("failed to create storage directory: %w", err)
	}
	tokenFile := filepath.Join(m.storagePath, "bootstrap.token")
	return os.WriteFile(tokenFile, []byte(strings.TrimSpace(token)), 0600)
}

// GetBootstrapToken loads the persisted bootstrap token if present.
func (m *IdentityManager) GetBootstrapToken() string {
	tokenFile := filepath.Join(m.storagePath, "bootstrap.token")
	data, err := os.ReadFile(tokenFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
