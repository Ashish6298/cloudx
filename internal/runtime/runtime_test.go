package runtime

import (
	"context"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
)

func TestNativeRuntime_StartAndInspect(t *testing.T) {
	rt := NewNativeRuntime()
	defer rt.Close()

	var cmd string
	var args []string
	if runtime.GOOS == "windows" {
		cmd = "powershell"
		args = []string{"-NoProfile", "-Command", "Start-Sleep -Milliseconds 300; Write-Output 'hello from worker'"}
	} else {
		cmd = "sh"
		args = []string{"-c", "sleep 0.3; echo 'hello from worker'"}
	}

	taskID := id.NewTaskID()
	status, err := rt.Start(context.Background(), ProcessSpec{
		ID:      taskID,
		Command: cmd,
		Args:    args,
	})
	if err != nil {
		t.Fatalf("failed to start process: %v", err)
	}

	if status.ID != taskID {
		t.Fatalf("expected task id %s, got %s", taskID, status.ID)
	}
	if status.PID <= 0 {
		t.Fatalf("expected valid PID > 0, got %d", status.PID)
	}
	if !status.Running {
		t.Fatalf("expected process to be running initially")
	}

	// Inspect while running
	inspectStatus, err := rt.Inspect(context.Background(), taskID)
	if err != nil {
		t.Fatalf("inspect failed: %v", err)
	}
	if inspectStatus.PID != status.PID {
		t.Fatalf("PID mismatch: expected %d, got %d", status.PID, inspectStatus.PID)
	}

	// Wait for process completion
	var finalStatus *ProcessStatus
	for i := 0; i < 40; i++ {
		time.Sleep(100 * time.Millisecond)
		finalStatus, err = rt.Inspect(context.Background(), taskID)
		if err == nil && !finalStatus.Running {
			break
		}
	}

	if finalStatus == nil || finalStatus.Running {
		t.Fatalf("expected process to be completed")
	}
	if finalStatus.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", finalStatus.ExitCode)
	}

	// Check logs
	logReader, err := rt.Logs(context.Background(), taskID, LogOptions{})
	if err != nil {
		t.Fatalf("failed to get logs: %v", err)
	}
	defer logReader.Close()

	logBytes, err := io.ReadAll(logReader)
	if err != nil {
		t.Fatalf("failed to read logs: %v", err)
	}

	if !strings.Contains(string(logBytes), "hello from worker") {
		t.Fatalf("expected log output to contain 'hello from worker', got: %s", string(logBytes))
	}
}

func TestNativeRuntime_StopAndKill(t *testing.T) {
	rt := NewNativeRuntime()
	defer rt.Close()

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
	status, err := rt.Start(context.Background(), ProcessSpec{
		ID:      taskID,
		Command: cmd,
		Args:    args,
	})
	if err != nil {
		t.Fatalf("failed to start process: %v", err)
	}

	if !status.Running {
		t.Fatalf("expected process to be running")
	}

	// Stop with short timeout
	err = rt.Stop(context.Background(), taskID, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("failed to stop process: %v", err)
	}

	inspectStatus, err := rt.Inspect(context.Background(), taskID)
	if err != nil {
		t.Fatalf("inspect failed: %v", err)
	}
	if inspectStatus.Running {
		t.Fatalf("expected process to be stopped")
	}
}

func TestNativeRuntime_Restart(t *testing.T) {
	rt := NewNativeRuntime()
	defer rt.Close()

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
	status1, err := rt.Start(context.Background(), ProcessSpec{
		ID:      taskID,
		Command: cmd,
		Args:    args,
	})
	if err != nil {
		t.Fatalf("failed to start process: %v", err)
	}

	status2, err := rt.Restart(context.Background(), taskID, 300*time.Millisecond)
	if err != nil {
		t.Fatalf("failed to restart process: %v", err)
	}

	if status2.PID == status1.PID {
		t.Fatalf("expected new PID on restart, got same %d", status2.PID)
	}
	if !status2.Running {
		t.Fatalf("expected restarted process to be running")
	}

	_ = rt.Stop(context.Background(), taskID, 300*time.Millisecond)
}

func TestNativeRuntime_ErrorsAndNotFound(t *testing.T) {
	rt := NewNativeRuntime()
	defer rt.Close()

	// 1. Invalid command
	_, err := rt.Start(context.Background(), ProcessSpec{
		ID:      id.NewTaskID(),
		Command: "",
	})
	if err == nil {
		t.Fatalf("expected error for empty command")
	}

	// 2. Non-existent process inspection
	_, err = rt.Inspect(context.Background(), id.NewTaskID())
	if err != ErrProcessNotFound {
		t.Fatalf("expected ErrProcessNotFound, got %v", err)
	}

	// 3. Signal non-existent
	err = rt.Signal(context.Background(), id.NewTaskID(), os.Interrupt)
	if err != ErrProcessNotFound {
		t.Fatalf("expected ErrProcessNotFound on signal, got %v", err)
	}
}

func TestRuntime_InterfaceCompliance(t *testing.T) {
	// Verify NativeRuntime satisfies Runtime interface at compile time
	var _ Runtime = (*NativeRuntime)(nil)

	rt := NewNativeRuntime()
	if rt.Type() != "native" {
		t.Fatalf("expected runtime type 'native', got '%s'", rt.Type())
	}
}
