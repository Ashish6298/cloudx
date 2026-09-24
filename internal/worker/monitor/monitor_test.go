package monitor

import (
	"context"
	"errors"
	"testing"
	"time"
)

type failingCollector struct{}

func (f *failingCollector) Platform() string {
	return "failing-mock"
}

func (f *failingCollector) Collect(ctx context.Context) (*ResourceMetrics, error) {
	return nil, errors.New("simulated sensor failure")
}

func TestResourceCollection_PlatformCollector(t *testing.T) {
	collector := NewPlatformCollector()
	if collector == nil {
		t.Fatalf("expected non-nil platform collector")
	}

	if collector.Platform() == "" {
		t.Fatalf("expected non-empty platform string")
	}

	metrics, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("failed to collect resource metrics: %v", err)
	}

	if metrics.Platform == "" {
		t.Fatalf("expected non-empty platform in metrics")
	}

	if metrics.TotalMemoryBytes <= 0 {
		t.Fatalf("expected positive total memory bytes, got %d", metrics.TotalMemoryBytes)
	}

	if metrics.MemoryAvailBytes <= 0 {
		t.Fatalf("expected positive available memory bytes, got %d", metrics.MemoryAvailBytes)
	}

	if metrics.MemoryUsedBytes < 0 {
		t.Fatalf("expected non-negative used memory bytes, got %d", metrics.MemoryUsedBytes)
	}

	if metrics.ProcessCount <= 0 {
		t.Fatalf("expected positive process count, got %d", metrics.ProcessCount)
	}

	if metrics.CPUUsagePercent < 0.0 || metrics.CPUUsagePercent > 100.0 {
		t.Fatalf("expected CPU percent between 0 and 100, got %f", metrics.CPUUsagePercent)
	}

	// Sampling consecutive reads should succeed and calculate delta CPU
	time.Sleep(50 * time.Millisecond)
	metrics2, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("failed second metric collection: %v", err)
	}
	if metrics2.CPUUsagePercent < 0.0 || metrics2.CPUUsagePercent > 100.0 {
		t.Fatalf("expected valid CPU percent on second sample, got %f", metrics2.CPUUsagePercent)
	}
}

func TestResourceMetrics_Serialization(t *testing.T) {
	original := &ResourceMetrics{
		Timestamp:        time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC),
		CPUUsagePercent:  24.5,
		MemoryUsedBytes:  4 * 1024 * 1024 * 1024,
		MemoryAvailBytes: 12 * 1024 * 1024 * 1024,
		TotalMemoryBytes: 16 * 1024 * 1024 * 1024,
		ProcessCount:     142,
		SystemLoad1:      1.5,
		SystemLoad5:      1.2,
		SystemLoad15:     0.9,
		Platform:         "linux",
	}

	data, err := original.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON failed: %v", err)
	}

	restored, err := FromJSON(data)
	if err != nil {
		t.Fatalf("FromJSON failed: %v", err)
	}

	if restored.Platform != original.Platform {
		t.Fatalf("platform mismatch: expected %s, got %s", original.Platform, restored.Platform)
	}
	if restored.CPUUsagePercent != original.CPUUsagePercent {
		t.Fatalf("CPU mismatch: expected %f, got %f", original.CPUUsagePercent, restored.CPUUsagePercent)
	}
	if restored.TotalMemoryBytes != original.TotalMemoryBytes {
		t.Fatalf("TotalMemory mismatch: expected %d, got %d", original.TotalMemoryBytes, restored.TotalMemoryBytes)
	}
	if restored.ProcessCount != original.ProcessCount {
		t.Fatalf("ProcessCount mismatch: expected %d, got %d", original.ProcessCount, restored.ProcessCount)
	}
	if restored.SystemLoad1 != original.SystemLoad1 {
		t.Fatalf("SystemLoad1 mismatch: expected %f, got %f", original.SystemLoad1, restored.SystemLoad1)
	}

	// Test invalid json
	_, err = FromJSON([]byte("{invalid-json"))
	if err == nil {
		t.Fatalf("expected error for invalid JSON, got nil")
	}
}

func TestResourceCollection_SamplingFailure(t *testing.T) {
	f := &failingCollector{}
	_, err := f.Collect(context.Background())
	if err == nil {
		t.Fatalf("expected sampling failure error, got nil")
	}
	if err.Error() != "simulated sensor failure" {
		t.Fatalf("expected 'simulated sensor failure', got %v", err)
	}
}
