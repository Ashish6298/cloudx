package id

import (
	"encoding/json"
	"time"
)

// EntityMeta provides the baseline identity and audit fields for every CloudX entity.
type EntityMeta struct {
	ID        ID        `json:"id" yaml:"id"`
	Name      string    `json:"name,omitempty" yaml:"name,omitempty"`
	CreatedAt time.Time `json:"created_at" yaml:"created_at"`
	UpdatedAt time.Time `json:"updated_at" yaml:"updated_at"`
}

// NewEntityMeta creates a new initialized EntityMeta for a given entity type and name.
func NewEntityMeta(entityType EntityType, name string) EntityMeta {
	now := time.Now().UTC()
	return EntityMeta{
		ID:        New(entityType),
		Name:      name,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// Touch updates the UpdatedAt timestamp.
func (m *EntityMeta) Touch() {
	m.UpdatedAt = time.Now().UTC()
}

// MarshalJSON provides standard JSON serialization.
func (m EntityMeta) MarshalJSON() ([]byte, error) {
	type Alias EntityMeta
	return json.Marshal(&struct {
		ID string `json:"id"`
		Alias
	}{
		ID:    m.ID.String(),
		Alias: (Alias)(m),
	})
}
