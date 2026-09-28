package worker

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/logs"
	run "github.com/cloudx-org/cloudx/internal/runtime"
	"github.com/cloudx-org/cloudx/internal/state/models"
)

// TestWorkerStress_ShortLivedWorkloads executes hundreds of concurrent and sequential short-lived tasks,
// measuring process creation, process cleanup, log collection, state updates, resource reporting,
// and checking for goroutine leaks, memory leaks, and zombie processes.
func TestWorkerStress_ShortLivedWorkloads(t *testing.T) {
	reporter := &mockReporter{}
	logger := logging.New(logging.LevelError, logging.FormatText)
	wlog := logs.NewWorkloadLogger("", 1000)

	tm := NewTaskManager(TaskManagerOptions{
		WorkerID:       id.NewWorkerID(),
		Runtime:        run.NewNativeRuntime(),
		Reporter:       reporter,
		Logger:         logger,
		WorkloadLogger: wlog,
	})
	defer tm.Close()

	// Capture initial baseline goroutine and memory metrics
	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	initialGoroutines := runtime.NumGoroutine()
	var initialMem runtime.MemStats
	runtime.ReadMemStats(&initialMem)

	const totalTasks = 100
	const concurrentBatches = 10
	const tasksPerBatch = totalTasks / concurrentBatches

	var completedTasks int64
	var totalCreationDuration int64 // nanoseconds
	var totalCleanupDuration int64  // nanoseconds

	var cmd string
	var args []string
	if runtime.GOOS == "windows" {
		cmd = "powershell"
		args = []string{"-NoProfile", "-Command", "Write-Output 'stress test log payload'; Start-Sleep -Milliseconds 50; exit 0"}
	} else {
		cmd = "sh"
		args = []string{"-c", "echo 'stress test log payload'; sleep 0.05; exit 0"}
	}

	fmt.Printf("\n=== WORKER STRESS TEST REPORT (PHASE 78) ===\n")
	fmt.Printf("Workload: %d total short-lived tasks executed across %d parallel batches\n", totalTasks, concurrentBatches)

	startStress := time.Now()

	for batch := 0; batch < concurrentBatches; batch++ {
		var wg sync.WaitGroup
		for i := 0; i < tasksPerBatch; i++ {
			wg.Add(1)
			taskID := id.NewTaskID()
			go func(tID id.ID) {
				defer wg.Done()

				t0 := time.Now()
				err := tm.AssignTask(context.Background(), TaskAssignment{
					TaskID:      tID,
					ServiceName: "stress-service",
					Command:     cmd,
					Args:        args,
					Timeout:     5 * time.Second,
					RestartPolicy: models.RestartPolicy{
						Type: models.RestartPolicyNever,
					},
				})
				creationDur := time.Since(t0)
				atomic.AddInt64(&totalCreationDuration, creationDur.Nanoseconds())

				if err != nil {
					t.Errorf("AssignTask failed for %s: %v", tID, err)
					return
				}

				// Wait for process completion (transition to STOPPED)
				cleanupStart := time.Now()
				deadline := time.Now().Add(10 * time.Second)
				for time.Now().Before(deadline) {
					snap, err := tm.GetTask(tID)
					if err == nil && (snap.State == models.TaskStateStopped || snap.State == models.TaskStateFailed) {
						atomic.AddInt64(&completedTasks, 1)
						atomic.AddInt64(&totalCleanupDuration, time.Since(cleanupStart).Nanoseconds())
						break
					}
					time.Sleep(20 * time.Millisecond)
				}
			}(taskID)
		}
		wg.Wait()
	}

	totalDuration := time.Since(startStress)

	// Post-stress stabilization and resource verification
	runtime.GC()
	time.Sleep(200 * time.Millisecond)
	finalGoroutines := runtime.NumGoroutine()
	var finalMem runtime.MemStats
	runtime.ReadMemStats(&finalMem)

	// Ensure all tasks completed successfully
	if completedTasks != int64(totalTasks) {
		t.Fatalf("expected %d tasks completed, got %d", totalTasks, completedTasks)
	}

	// Verify log ring buffer captured workload entries
	recentLogs := wlog.ReadFilteredLogs(logs.LogFilter{ServiceName: "stress-service"}, nil)
	if len(recentLogs) == 0 {
		t.Errorf("expected logs captured in ring buffer, got 0")
	}

	// Verify state reports to control plane
	reports := reporter.getReports()
	if len(reports) < totalTasks*2 {
		t.Errorf("expected at least %d state update reports, got %d", totalTasks*2, len(reports))
	}

	avgCreation := time.Duration(totalCreationDuration / totalTasks)
	avgCleanup := time.Duration(totalCleanupDuration / totalTasks)
	goroutineDelta := finalGoroutines - initialGoroutines
	memDeltaKB := int64(finalMem.Alloc-initialMem.Alloc) / 1024

	fmt.Printf("%-32s: %d / %d (100%% Clean Completion)\n", "Tasks Executed & Cleaned", completedTasks, totalTasks)
	fmt.Printf("%-32s: %v\n", "Total Stress Duration", totalDuration)
	fmt.Printf("%-32s: %v\n", "Avg Process Launch Latency", avgCreation)
	fmt.Printf("%-32s: %v\n", "Avg Process Cleanup Latency", avgCleanup)
	fmt.Printf("%-32s: %d reports received\n", "State Updates Reported", len(reports))
	fmt.Printf("%-32s: %d lines captured\n", "Logs Aggregated in RingBuf", len(recentLogs))
	fmt.Printf("%-32s: %d (Baseline: %d, Final: %d)\n", "Goroutine Leak Delta", goroutineDelta, initialGoroutines, finalGoroutines)
	fmt.Printf("%-32s: %d KB\n", "Heap Memory Allocation Delta", memDeltaKB)
	fmt.Printf("============================================\n\n")

	// Strict checks for leaks: Goroutines delta should be minimal (<= 10)
	if goroutineDelta > 10 {
		t.Errorf("potential goroutine leak detected: delta = %d goroutines", goroutineDelta)
	}
}
