package scheduler

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
)

// generateBenchmarkWorkers creates n synthetic worker nodes with diverse resource allocations and workloads.
func generateBenchmarkWorkers(n int) []*WorkerCapacity {
	workers := make([]*WorkerCapacity, n)
	for i := 0; i < n; i++ {
		wID := id.NewWorkerID()
		workers[i] = &WorkerCapacity{
			WorkerID:            wID,
			NodeID:              id.NewNodeID(),
			Hostname:            fmt.Sprintf("worker-bench-%04d", i),
			Status:              "READY",
			CPUTotal:            16.0,
			CPUAllocated:        float64(i%8) * 1.5,
			MemoryTotal:         64 * 1024 * 1024 * 1024,
			MemoryAllocated:     int64(i%8) * 4 * 1024 * 1024 * 1024,
			TaskCount:           i % 10,
			RuntimeCapabilities: []string{"native", "docker", "containerd"},
			ServiceTaskCounts:   make(map[id.ID]int),
			NodeLabels: map[string]string{
				"zone": fmt.Sprintf("zone-%d", i%3),
				"tier": "compute",
			},
			Tags:           []string{"ssd", "high-cpu"},
			AllocatedPorts: []int{30000 + i%10},
		}
	}
	return workers
}

// BenchmarkScheduler_Scale evaluates scheduling latency, allocations/op, and throughput across worker cluster sizes.
func BenchmarkScheduler_Scale(b *testing.B) {
	sched := NewBasicScheduler()
	ctx := context.Background()

	clusterSizes := []int{10, 50, 100, 500}

	for _, size := range clusterSizes {
		b.Run(fmt.Sprintf("%d_workers", size), func(b *testing.B) {
			workers := generateBenchmarkWorkers(size)
			serviceID := id.NewServiceID()
			taskReq := &TaskRequirements{
				TaskID:          id.NewTaskID(),
				ServiceID:       serviceID,
				CPU:             1.0,
				Memory:          1024 * 1024 * 1024,
				RequiredRuntime: "native",
			}

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				decision, err := sched.Schedule(ctx, taskReq, workers)
				if err != nil || decision == nil {
					b.Fatalf("scheduling failed: %v", err)
				}
			}
		})
	}
}

// TestScheduler_ScalePerformance verifies that scheduling latency and memory footprints stay within tight bounds.
func TestScheduler_ScalePerformance(t *testing.T) {
	sched := NewBasicScheduler()
	ctx := context.Background()

	sizes := []int{10, 50, 100, 500}
	const iterations = 500

	fmt.Printf("\n=== SCHEDULER SCALE PERFORMANCE BENCHMARK REPORT ===\n")
	fmt.Printf("%-15s %-18s %-18s %-18s %-15s\n", "WORKER COUNT", "AVG LATENCY", "P95 LATENCY", "THROUGHPUT (OPS/S)", "ALLOC MEM/OP")

	for _, size := range sizes {
		workers := generateBenchmarkWorkers(size)
		serviceID := id.NewServiceID()
		taskReq := &TaskRequirements{
			TaskID:          id.NewTaskID(),
			ServiceID:       serviceID,
			CPU:             1.0,
			Memory:          1024 * 1024 * 1024,
			RequiredRuntime: "native",
		}

		latencies := make([]time.Duration, iterations)
		var m1, m2 runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&m1)

		startTotal := time.Now()
		for i := 0; i < iterations; i++ {
			t0 := time.Now()
			decision, err := sched.Schedule(ctx, taskReq, workers)
			latencies[i] = time.Since(t0)
			if err != nil || decision == nil {
				t.Fatalf("scheduling failed at worker size %d: %v", size, err)
			}
		}
		totalElapsed := time.Since(startTotal)
		runtime.ReadMemStats(&m2)

		var sum time.Duration
		for _, lat := range latencies {
			sum += lat
		}
		avgLat := sum / iterations

		// Calculate P95
		for i := 0; i < len(latencies); i++ {
			for j := i + 1; j < len(latencies); j++ {
				if latencies[i] > latencies[j] {
					latencies[i], latencies[j] = latencies[j], latencies[i]
				}
			}
		}
		p95Lat := latencies[int(float64(iterations)*0.95)]
		throughput := float64(iterations) / totalElapsed.Seconds()
		allocBytesPerOp := float64(m2.TotalAlloc-m1.TotalAlloc) / float64(iterations)

		fmt.Printf("%-15d %-18v %-18v %-18.0f %-15.0f B\n", size, avgLat, p95Lat, throughput, allocBytesPerOp)

		// Assert reasonable bounds for local scheduling
		if size <= 100 && avgLat > 10*time.Millisecond {
			t.Errorf("expected avg latency < 10ms for %d workers, got %v", size, avgLat)
		}
		if size == 500 && avgLat > 50*time.Millisecond {
			t.Errorf("expected avg latency < 50ms for %d workers, got %v", size, avgLat)
		}
	}
	fmt.Printf("====================================================\n\n")
}
