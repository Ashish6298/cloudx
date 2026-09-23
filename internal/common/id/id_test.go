package id

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestIDGeneration(t *testing.T) {
	entityTypes := []struct {
		gen    func() ID
		prefix string
	}{
		{NewNodeID, "node"},
		{NewWorkerID, "wrk"},
		{NewServiceID, "srv"},
		{NewDeploymentID, "dep"},
		{NewTaskID, "tsk"},
		{NewJobID, "job"},
		{NewVolumeID, "vol"},
		{NewNetworkID, "net"},
		{NewEventID, "evt"},
	}

	for _, tc := range entityTypes {
		id := tc.gen()
		str := id.String()
		if !strings.HasPrefix(str, tc.prefix+"-") {
			t.Errorf("expected ID prefix %q, got: %s", tc.prefix, str)
		}

		if err := Validate(str); err != nil {
			t.Errorf("generated ID failed validation: %v", err)
		}
	}
}

func TestUniquenessAndCollisionResistance(t *testing.T) {
	const count = 10000
	ids := make(map[string]bool, count)
	var mu sync.Mutex

	var wg sync.WaitGroup
	workers := 10
	perWorker := count / workers

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			localIDs := make([]string, perWorker)
			for j := 0; j < perWorker; j++ {
				localIDs[j] = string(NewServiceID())
			}

			mu.Lock()
			for _, idStr := range localIDs {
				if ids[idStr] {
					t.Errorf("duplicate ID collision detected: %s", idStr)
				}
				ids[idStr] = true
			}
			mu.Unlock()
		}()
	}

	wg.Wait()

	if len(ids) != count {
		t.Errorf("expected %d unique IDs, got %d", count, len(ids))
	}
}

func TestParsingAndValidation(t *testing.T) {
	now := time.Now().UTC()
	id := NewTaskID()

	parsed, err := Parse(string(id))
	if err != nil {
		t.Fatalf("failed to parse valid ID: %v", err)
	}

	if parsed.Type != EntityTask {
		t.Errorf("expected type %s, got %s", EntityTask, parsed.Type)
	}

	diff := parsed.Timestamp.Sub(now)
	if diff < -2*time.Second || diff > 2*time.Second {
		t.Errorf("timestamp parsing inaccurate: diff=%v", diff)
	}

	// Invalid ID test cases
	invalidIDs := []string{
		"",
		"invalid",
		"srv-123",
		"srv-invalidtimestamp-abcdef123456",
		"unknown-18f45a2b0000-abcdef123456",
		"srv-18f45a2b0000-short",
	}

	for _, inv := range invalidIDs {
		if err := Validate(inv); err == nil {
			t.Errorf("expected validation failure for invalid ID %q, got nil", inv)
		}
	}
}

func TestSerialization(t *testing.T) {
	meta := NewEntityMeta(EntityService, "api-gateway")

	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("failed to marshal EntityMeta: %v", err)
	}

	var unmarshaled struct {
		ID        string    `json:"id"`
		Name      string    `json:"name"`
		CreatedAt time.Time `json:"created_at"`
		UpdatedAt time.Time `json:"updated_at"`
	}

	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}

	if unmarshaled.ID != meta.ID.String() {
		t.Errorf("expected ID %q, got %q", meta.ID.String(), unmarshaled.ID)
	}
	if unmarshaled.Name != "api-gateway" {
		t.Errorf("expected name 'api-gateway', got %q", unmarshaled.Name)
	}
	if unmarshaled.CreatedAt.IsZero() {
		t.Errorf("expected non-zero CreatedAt")
	}
}
