package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/state/models"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
)

func TestJobCLICommands(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "cloudx.db")
	ctx := context.Background()
	now := time.Now().UTC()

	// 1. Initialize store with worker
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}

	nodeID := id.NewNodeID()
	workerID := id.NewWorkerID()
	_ = store.Nodes().Create(ctx, &models.Node{
		ID:        nodeID,
		Name:      "test-node",
		Status:    "READY",
		CreatedAt: now,
		UpdatedAt: now,
	})
	_ = store.Workers().Create(ctx, &models.Worker{
		ID:        workerID,
		NodeID:    nodeID,
		Status:    "READY",
		Heartbeat: now,
		CreatedAt: now,
		UpdatedAt: now,
	})
	_ = store.Close()

	// 2. Test `cloudx job run migration --command "echo hello"`
	runCmd := newRootCmd()
	runBuf := new(bytes.Buffer)
	runCmd.SetOut(runBuf)
	runCmd.SetErr(runBuf)
	runCmd.SetArgs([]string{
		"--storage-path", tempDir,
		"job", "run", "migration",
		"--command", "echo",
		"--args", "hello,world",
		"--timeout", "5m",
		"--max-retries", "2",
	})

	if err := runCmd.Execute(); err != nil {
		t.Fatalf("job run command failed: %v", err)
	}

	runOut := runBuf.String()
	if !strings.Contains(runOut, "Job 'migration' submitted successfully!") {
		t.Fatalf("expected job submission message, got:\n%s", runOut)
	}
	if !strings.Contains(runOut, "migration") {
		t.Fatalf("expected job name in output table, got:\n%s", runOut)
	}

	// 3. Test `cloudx job list`
	listCmd := newRootCmd()
	listBuf := new(bytes.Buffer)
	listCmd.SetOut(listBuf)
	listCmd.SetErr(listBuf)
	listCmd.SetArgs([]string{"--storage-path", tempDir, "job", "list"})

	if err := listCmd.Execute(); err != nil {
		t.Fatalf("job list command failed: %v", err)
	}

	listOut := listBuf.String()
	if !strings.Contains(listOut, "migration") {
		t.Fatalf("expected job list to contain 'migration', got:\n%s", listOut)
	}

	// 4. Test `cloudx job inspect migration`
	inspectCmd := newRootCmd()
	inspectBuf := new(bytes.Buffer)
	inspectCmd.SetOut(inspectBuf)
	inspectCmd.SetErr(inspectBuf)
	inspectCmd.SetArgs([]string{"--storage-path", tempDir, "job", "inspect", "migration"})

	if err := inspectCmd.Execute(); err != nil {
		t.Fatalf("job inspect command failed: %v", err)
	}

	inspectOut := inspectBuf.String()
	if !strings.Contains(inspectOut, "Job: migration") || !strings.Contains(inspectOut, "Command:") {
		t.Fatalf("expected inspect details, got:\n%s", inspectOut)
	}

	// 5. Test `cloudx job run` with manifest file
	manifestPath := filepath.Join(tempDir, "sample-job.yaml")
	manifestContent := `
jobs:
  db-seed:
    command: "python"
    args: ["seed.py"]
    runtime: "native"
    timeout: "10m"
`
	if err := os.WriteFile(manifestPath, []byte(manifestContent), 0644); err != nil {
		t.Fatalf("failed to write job manifest: %v", err)
	}

	fileRunCmd := newRootCmd()
	fileRunBuf := new(bytes.Buffer)
	fileRunCmd.SetOut(fileRunBuf)
	fileRunCmd.SetErr(fileRunBuf)
	fileRunCmd.SetArgs([]string{
		"--storage-path", tempDir,
		"job", "run", "-f", manifestPath,
	})

	if err := fileRunCmd.Execute(); err != nil {
		t.Fatalf("job run from manifest failed: %v", err)
	}

	fileRunOut := fileRunBuf.String()
	if !strings.Contains(fileRunOut, "Job 'db-seed' submitted successfully!") {
		t.Fatalf("expected 'db-seed' submission, got:\n%s", fileRunOut)
	}
}
