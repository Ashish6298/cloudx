package worker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudx-org/cloudx/internal/common/id"
)

// IdentityManager ensures a worker's ID remains stable across restarts.
type IdentityManager struct {
	storagePath string
}

func NewIdentityManager(storagePath string) *IdentityManager {
	return &IdentityManager{storagePath: storagePath}
}

// GetOrCreateIdentity returns existing worker ID from storage or generates a new one.
func (m *IdentityManager) GetOrCreateIdentity(explicitID string) (id.ID, error) {
	if explicitID != "" {
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
	if err := os.WriteFile(idFilePath, []byte(newID.String()), 0644); err != nil {
		return "", fmt.Errorf("failed to persist worker ID: %w", err)
	}

	return newID, nil
}
