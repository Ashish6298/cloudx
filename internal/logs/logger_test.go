package logs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
)

func TestRingBuffer_BoundedCapacity(t *testing.T) {
	capacity := 5
	rb := NewRingBuffer(capacity)

	for i := 1; i <= 10; i++ {
		rb.Append(LogEntry{
			Message:   fmt.Sprintf("line %d", i),
			Timestamp: time.Now().UTC(),
		})
	}

	entries := rb.Entries()
	if len(entries) != capacity {
		t.Fatalf("expected ring buffer entries length %d, got %d", capacity, len(entries))
	}

	// Should contain lines 6 through 10
	if entries[0].Message != "line 6" {
		t.Errorf("expected first entry to be 'line 6', got '%s'", entries[0].Message)
	}
	if entries[4].Message != "line 10" {
		t.Errorf("expected last entry to be 'line 10', got '%s'", entries[4].Message)
	}
}

func TestWorkloadLogger_WriteAndRead(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cloudx-logs-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	wl := NewWorkloadLogger(tmpDir, 100)

	svcID := id.NewServiceID()
	depID := id.NewDeploymentID()
	taskID := id.NewTaskID()
	workerID := id.NewWorkerID()

	writer := wl.LogWriter(LogEntry{
		ServiceID:    svcID,
		ServiceName:  "web-service",
		DeploymentID: depID,
		TaskID:       taskID,
		WorkerID:     workerID,
		Stream:       "stdout",
	})

	_, err = writer.Write([]byte("Server starting...\nListening on port 8080\n"))
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}

	// 1. Read Task Logs
	entries := wl.ReadTaskLogs(taskID, 10)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Message != "Server starting..." {
		t.Errorf("unexpected entry 0: %s", entries[0].Message)
	}
	if entries[1].Message != "Listening on port 8080" {
		t.Errorf("unexpected entry 1: %s", entries[1].Message)
	}

	// Verify metadata association
	if entries[0].ServiceID != svcID || entries[0].ServiceName != "web-service" || entries[0].DeploymentID != depID || entries[0].WorkerID != workerID {
		t.Errorf("metadata missing or mismatched in entry: %+v", entries[0])
	}

	// 2. Read filtered logs
	filtered := wl.ReadFilteredLogs(LogFilter{
		ServiceName: "web-service",
	}, nil)
	if len(filtered) != 2 {
		t.Fatalf("expected 2 filtered entries, got %d", len(filtered))
	}

	// 3. Verify disk file creation
	logFile := filepath.Join(tmpDir, fmt.Sprintf("%s.log", taskID))
	if _, err := os.Stat(logFile); os.IsNotExist(err) {
		t.Fatalf("expected log file on disk at %s", logFile)
	}
}

func TestWorkloadLogger_LiveSubscription(t *testing.T) {
	wl := NewWorkloadLogger("", 100)

	taskID := id.NewTaskID()
	writer := wl.LogWriter(LogEntry{
		TaskID: taskID,
		Stream: "stdout",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	liveCh := wl.SubscribeFilter(ctx, LogFilter{TaskID: taskID}, []id.ID{taskID})

	// Write asynchronously
	go func() {
		time.Sleep(50 * time.Millisecond)
		_, _ = writer.Write([]byte("Live log message 1\n"))
		time.Sleep(50 * time.Millisecond)
		_, _ = writer.Write([]byte("Live log message 2\n"))
	}()

	var received []string
	for len(received) < 2 {
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for live log lines: got %d lines", len(received))
		case entry, ok := <-liveCh:
			if !ok {
				t.Fatalf("live channel closed unexpectedly")
			}
			received = append(received, entry.Message)
		}
	}

	if received[0] != "Live log message 1" || received[1] != "Live log message 2" {
		t.Errorf("unexpected received messages: %v", received)
	}
}
