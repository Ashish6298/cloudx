package id

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// EntityType represents a CloudX entity classification.
type EntityType string

const (
	EntityNode       EntityType = "node"
	EntityWorker     EntityType = "wrk"
	EntityService    EntityType = "srv"
	EntityDeployment EntityType = "dep"
	EntityTask       EntityType = "tsk"
	EntityJob        EntityType = "job"
	EntityVolume     EntityType = "vol"
	EntityNetwork    EntityType = "net"
	EntityEvent      EntityType = "evt"
)

var validEntityTypes = map[EntityType]bool{
	EntityNode:       true,
	EntityWorker:     true,
	EntityService:    true,
	EntityDeployment: true,
	EntityTask:       true,
	EntityJob:        true,
	EntityVolume:     true,
	EntityNetwork:    true,
	EntityEvent:      true,
}

// ID represents a collision-resistant, inspectable CloudX identifier.
// Format: <prefix>-<timestamp_hex>-<random_hex>
// Example: srv-18f45a2b-8a7f9b2c
type ID string

func (id ID) String() string {
	return string(id)
}

// Type returns the EntityType of the ID.
func (id ID) Type() (EntityType, error) {
	parsed, err := Parse(string(id))
	if err != nil {
		return "", err
	}
	return parsed.Type, nil
}

// ParsedID holds the constituent components of a CloudX ID.
type ParsedID struct {
	Type      EntityType
	Timestamp time.Time
	Entropy   string
	Raw       string
}

// New generates a new collision-resistant ID for the specified EntityType.
func New(entityType EntityType) ID {
	if !validEntityTypes[entityType] {
		// Fallback for custom or untyped entities
		entityType = EntityType(strings.ToLower(string(entityType)))
	}

	ts := uint64(time.Now().UTC().UnixNano())
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		// fallback to pseudo-random if crypto/rand fails
		ts = ts ^ uint64(time.Now().UnixMicro())
	}

	raw := fmt.Sprintf("%s-%012x-%s", entityType, ts, hex.EncodeToString(b))
	return ID(raw)
}

// Parse validates and extracts metadata from an ID string.
func Parse(idStr string) (*ParsedID, error) {
	parts := strings.Split(idStr, "-")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid ID format: expected <type>-<timestamp>-<entropy>, got '%s'", idStr)
	}

	prefix := EntityType(parts[0])
	if !validEntityTypes[prefix] {
		return nil, fmt.Errorf("invalid entity prefix '%s'", prefix)
	}

	var tsNano uint64
	_, err := fmt.Sscanf(parts[1], "%x", &tsNano)
	if err != nil {
		return nil, fmt.Errorf("invalid timestamp component in ID: %w", err)
	}

	if len(parts[2]) != 12 {
		return nil, fmt.Errorf("invalid entropy component length: expected 12 hex chars, got %d", len(parts[2]))
	}

	return &ParsedID{
		Type:      prefix,
		Timestamp: time.Unix(0, int64(tsNano)).UTC(),
		Entropy:   parts[2],
		Raw:       idStr,
	}, nil
}

// Validate checks whether an ID string is structurally valid.
func Validate(idStr string) error {
	_, err := Parse(idStr)
	return err
}

// Helper generators for core entities
func NewNodeID() ID       { return New(EntityNode) }
func NewWorkerID() ID     { return New(EntityWorker) }
func NewServiceID() ID    { return New(EntityService) }
func NewDeploymentID() ID { return New(EntityDeployment) }
func NewTaskID() ID       { return New(EntityTask) }
func NewJobID() ID        { return New(EntityJob) }
func NewVolumeID() ID     { return New(EntityVolume) }
func NewNetworkID() ID    { return New(EntityNetwork) }
func NewEventID() ID      { return New(EntityEvent) }
