package worker

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/common/logging"
	run "github.com/cloudx-org/cloudx/internal/runtime"
	"github.com/cloudx-org/cloudx/internal/state/models"
	v1 "github.com/cloudx-org/cloudx/proto/v1"
)

type mockReporter struct {
	mu      sync.Mutex
	reports []*v1.ReportTaskStatusRequest
}

func (m *mockReporter) ReportTaskStatus(ctx context.Context, req *v1.ReportTaskStatusRequest) (*v1.ReportTaskStatusResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reports = append(m.reports, req)
	return &v1.ReportTaskStatusResponse{Acknowledged: true}, nil
}

func (m *mockReporter) getReports() []*v1.ReportTaskStatusRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	copied := make([]*v1.ReportTaskStatusRequest, len(m.reports))
	copy(copied, m.reports)
	return copied
}

func TestTaskManager_FullLifecycle_Success(t *testing.T) {
	reporter := &mockReporter{}
	tm := NewTaskManager(TaskManagerOptions{
		WorkerID: id.NewWorkerID(),
		Runtime:  run.NewNativeRuntime(),
		Reporter: reporter,
		Logger:   logging.NewDefaultLogger(),
	})
	defer tm.Close()

	var cmd string
	var args []string
	if runtime.GOOS == "windows" {
		cmd = "powershell"
		args = []string{"-NoProfile", "-Command", "Start-Sleep -Milliseconds 200; exit 0"}
	} else {
		cmd = "sh"
		args = []string{"-c", "sleep 0.2; exit 0"}
	}

	taskID := id.NewTaskID()
	err := tm.AssignTask(context.Background(), TaskAssignment{
		TaskID:  taskID,
		Command: cmd,
		Args:    args,
	})
	if err != nil {
		t.Fatalf("failed to assign task: %v", err)
	}

	// Poll until task reaches STOPPED
	var snap *TaskStatusSnapshot
	for i := 0; i < 40; i++ {
		time.Sleep(100 * time.Millisecond)
		snap, err = tm.GetTask(taskID)
		if err == nil && snap.State == models.TaskStateStopped {
			break
		}
	}

	if snap == nil || snap.State != models.TaskStateStopped {
		t.Fatalf("expected task state STOPPED, got %v", snap)
	}
	if snap.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", snap.ExitCode)
	}
	if snap.PID <= 0 {
		t.Fatalf("expected recorded PID > 0, got %d", snap.PID)
	}

	// Verify transitions reported: ASSIGNED, STARTING, RUNNING, STOPPED
	reports := reporter.getReports()
	if len(reports) < 3 {
		t.Fatalf("expected at least 3 state reports, got %d", len(reports))
	}
}

func TestTaskManager_FullLifecycle_CrashAndFailure(t *testing.T) {
	reporter := &mockReporter{}
	tm := NewTaskManager(TaskManagerOptions{
		WorkerID: id.NewWorkerID(),
		Runtime:  run.NewNativeRuntime(),
		Reporter: reporter,
		Logger:   logging.NewDefaultLogger(),
	})
	defer tm.Close()

	var cmd string
	var args []string
	if runtime.GOOS == "windows" {
		cmd = "powershell"
		args = []string{"-NoProfile", "-Command", "Start-Sleep -Milliseconds 100; exit 7"}
	} else {
		cmd = "sh"
		args = []string{"-c", "sleep 0.1; exit 7"}
	}

	taskID := id.NewTaskID()
	err := tm.AssignTask(context.Background(), TaskAssignment{
		TaskID:  taskID,
		Command: cmd,
		Args:    args,
	})
	if err != nil {
		t.Fatalf("failed to assign task: %v", err)
	}

	// Poll until task reaches FAILED
	var snap *TaskStatusSnapshot
	for i := 0; i < 40; i++ {
		time.Sleep(100 * time.Millisecond)
		snap, err = tm.GetTask(taskID)
		if err == nil && snap.State == models.TaskStateFailed {
			break
		}
	}

	if snap == nil || snap.State != models.TaskStateFailed {
		t.Fatalf("expected task state FAILED, got %v", snap)
	}
	if snap.ExitCode != 7 {
		t.Fatalf("expected exit code 7, got %d", snap.ExitCode)
	}
}

func TestTaskManager_PreventDuplicateExecution(t *testing.T) {
	tm := NewTaskManager(TaskManagerOptions{
		WorkerID: id.NewWorkerID(),
		Runtime:  run.NewNativeRuntime(),
		Logger:   logging.NewDefaultLogger(),
	})
	defer tm.Close()

	var cmd string
	var args []string
	if runtime.GOOS == "windows" {
		cmd = "powershell"
		args = []string{"-NoProfile", "-Command", "Start-Sleep -Seconds 5"}
	} else {
		cmd = "sh"
		args = []string{"-c", "sleep 5"}
	}

	taskID := id.NewTaskID()
	assignment := TaskAssignment{
		TaskID:  taskID,
		Command: cmd,
		Args:    args,
	}

	// First assignment
	err := tm.AssignTask(context.Background(), assignment)
	if err != nil {
		t.Fatalf("first assignment failed: %v", err)
	}

	// Duplicate assignment must be rejected
	err = tm.AssignTask(context.Background(), assignment)
	if err != ErrTaskAlreadyExists {
		t.Fatalf("expected ErrTaskAlreadyExists on duplicate assignment, got %v", err)
	}
}

func TestTaskManager_StopTask(t *testing.T) {
	tm := NewTaskManager(TaskManagerOptions{
		WorkerID: id.NewWorkerID(),
		Runtime:  run.NewNativeRuntime(),
		Logger:   logging.NewDefaultLogger(),
	})
	defer tm.Close()

	var cmd string
	var args []string
	if runtime.GOOS == "windows" {
		cmd = "powershell"
		args = []string{"-NoProfile", "-Command", "Start-Sleep -Seconds 10"}
	} else {
		cmd = "sh"
		args = []string{"-c", "sleep 10"}
	}

	taskID := id.NewTaskID()
	err := tm.AssignTask(context.Background(), TaskAssignment{
		TaskID:  taskID,
		Command: cmd,
		Args:    args,
	})
	if err != nil {
		t.Fatalf("failed to assign task: %v", err)
	}

	// Wait for RUNNING
	for i := 0; i < 40; i++ {
		time.Sleep(50 * time.Millisecond)
		st, _ := tm.GetTask(taskID)
		if st != nil && st.State == models.TaskStateRunning {
			break
		}
	}

	// Stop task
	err = tm.StopTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("failed to stop task: %v", err)
	}

	// Poll until STOPPED
	var finalSnap *TaskStatusSnapshot
	for i := 0; i < 40; i++ {
		time.Sleep(100 * time.Millisecond)
		finalSnap, _ = tm.GetTask(taskID)
		if finalSnap != nil && finalSnap.State == models.TaskStateStopped {
			break
		}
	}

	if finalSnap == nil || finalSnap.State != models.TaskStateStopped {
		t.Fatalf("expected stopped state, got %v", finalSnap)
	}
}

func TestTaskManager_InvalidSpecsAndNotFound(t *testing.T) {
	tm := NewTaskManager(TaskManagerOptions{
		WorkerID: id.NewWorkerID(),
	})
	defer tm.Close()

	// 1. Missing task ID
	err := tm.AssignTask(context.Background(), TaskAssignment{
		Command: "echo",
	})
	if err == nil {
		t.Fatalf("expected error for missing task ID")
	}

	// 2. Missing command
	err = tm.AssignTask(context.Background(), TaskAssignment{
		TaskID: id.NewTaskID(),
	})
	if err == nil {
		t.Fatalf("expected error for missing command")
	}

	// 3. Unknown task get
	_, err = tm.GetTask(id.NewTaskID())
	if err != ErrTaskNotFound {
		t.Fatalf("expected ErrTaskNotFound, got %v", err)
	}

	// 4. Unknown task stop
	err = tm.StopTask(context.Background(), id.NewTaskID())
	if err != ErrTaskNotFound {
		t.Fatalf("expected ErrTaskNotFound on stop, got %v", err)
	}
}

func TestTaskManager_RestartPolicy_Never(t *testing.T) {
	tm := NewTaskManager(TaskManagerOptions{
		WorkerID: id.NewWorkerID(),
		Runtime:  run.NewNativeRuntime(),
		Logger:   logging.NewDefaultLogger(),
	})
	defer tm.Close()

	var cmd string
	var args []string
	if runtime.GOOS == "windows" {
		cmd = "powershell"
		args = []string{"-NoProfile", "-Command", "exit 1"}
	} else {
		cmd = "sh"
		args = []string{"-c", "exit 1"}
	}

	taskID := id.NewTaskID()
	err := tm.AssignTask(context.Background(), TaskAssignment{
		TaskID:  taskID,
		Command: cmd,
		Args:    args,
		RestartPolicy: models.RestartPolicy{
			Type: models.RestartPolicyNever,
		},
	})
	if err != nil {
		t.Fatalf("assign failed: %v", err)
	}

	time.Sleep(300 * time.Millisecond)
	st, err := tm.GetTask(taskID)
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}

	// Never restart -> stays FAILED
	if st.State != models.TaskStateFailed {
		t.Fatalf("expected state FAILED for policy 'never', got %s", st.State)
	}
	if st.RestartCount != 0 {
		t.Fatalf("expected 0 restarts for policy 'never', got %d", st.RestartCount)
	}
}

func TestTaskManager_RestartPolicy_OnFailure_And_CrashLoop(t *testing.T) {
	tm := NewTaskManager(TaskManagerOptions{
		WorkerID: id.NewWorkerID(),
		Runtime:  run.NewNativeRuntime(),
		Logger:   logging.NewDefaultLogger(),
	})
	defer tm.Close()

	var cmd string
	var args []string
	if runtime.GOOS == "windows" {
		cmd = "powershell"
		args = []string{"-NoProfile", "-Command", "exit 1"}
	} else {
		cmd = "sh"
		args = []string{"-c", "exit 1"}
	}

	taskID := id.NewTaskID()
	err := tm.AssignTask(context.Background(), TaskAssignment{
		TaskID:  taskID,
		Command: cmd,
		Args:    args,
		RestartPolicy: models.RestartPolicy{
			Type:          models.RestartPolicyOnFailure,
			MaxRetries:    2,
			BackoffPeriod: 50 * time.Millisecond,
		},
	})
	if err != nil {
		t.Fatalf("assign failed: %v", err)
	}

	// Poll until CRASH_LOOP entered after exceeding max retries (2)
	var finalState models.TaskState
	for i := 0; i < 40; i++ {
		time.Sleep(100 * time.Millisecond)
		st, _ := tm.GetTask(taskID)
		if st != nil {
			finalState = st.State
			if st.State == models.TaskStateCrashLoop {
				break
			}
		}
	}

	if finalState != models.TaskStateCrashLoop {
		t.Fatalf("expected state CRASH_LOOP after repeated failures, got %s", finalState)
	}
}

func TestTaskManager_RestartPolicy_Always_CleanExit(t *testing.T) {
	tm := NewTaskManager(TaskManagerOptions{
		WorkerID: id.NewWorkerID(),
		Runtime:  run.NewNativeRuntime(),
		Logger:   logging.NewDefaultLogger(),
	})
	defer tm.Close()

	var cmd string
	var args []string
	if runtime.GOOS == "windows" {
		cmd = "powershell"
		args = []string{"-NoProfile", "-Command", "exit 0"}
	} else {
		cmd = "sh"
		args = []string{"-c", "exit 0"}
	}

	taskID := id.NewTaskID()
	err := tm.AssignTask(context.Background(), TaskAssignment{
		TaskID:  taskID,
		Command: cmd,
		Args:    args,
		RestartPolicy: models.RestartPolicy{
			Type:          models.RestartPolicyAlways,
			MaxRetries:    3,
			BackoffPeriod: 50 * time.Millisecond,
		},
	})
	if err != nil {
		t.Fatalf("assign failed: %v", err)
	}

	// Clean exits with always restart policy trigger restarts
	time.Sleep(250 * time.Millisecond)
	st, err := tm.GetTask(taskID)
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}
	if st.RestartCount < 1 {
		t.Fatalf("expected at least 1 restart for 'always' restart policy, got %d", st.RestartCount)
	}
}

