package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// ResourceMetrics contains the system resource telemetry of a worker machine.
type ResourceMetrics struct {
	Timestamp        time.Time `json:"timestamp"`
	CPUUsagePercent  float64   `json:"cpu_usage_percent"`
	MemoryUsedBytes  int64     `json:"memory_used_bytes"`
	MemoryAvailBytes int64     `json:"memory_avail_bytes"`
	TotalMemoryBytes int64     `json:"total_memory_bytes"`
	ProcessCount     int       `json:"process_count"`
	SystemLoad1      float64   `json:"system_load_1"`
	SystemLoad5      float64   `json:"system_load_5"`
	SystemLoad15     float64   `json:"system_load_15"`
	Platform         string    `json:"platform"`
}

// ToJSON serializes the ResourceMetrics into a JSON byte array.
func (m *ResourceMetrics) ToJSON() ([]byte, error) {
	return json.Marshal(m)
}

// FromJSON deserializes JSON into a ResourceMetrics struct.
func FromJSON(data []byte) (*ResourceMetrics, error) {
	var m ResourceMetrics
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("failed to parse resource metrics JSON: %w", err)
	}
	return &m, nil
}

// Collector defines the interface for platform-independent resource telemetry collection.
type Collector interface {
	// Collect gathers instantaneous system resource usage.
	Collect(ctx context.Context) (*ResourceMetrics, error)
	// Platform returns the operating system platform identifier (e.g. "windows", "linux", "darwin").
	Platform() string
}
