package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

type Target struct {
	OS   string
	Arch string
	Ext  string
}

func main() {
	targets := []Target{
		{"windows", "amd64", ".exe"},
		{"windows", "arm64", ".exe"},
		{"linux", "amd64", ""},
		{"linux", "arm64", ""},
		{"darwin", "amd64", ""},
		{"darwin", "arm64", ""},
	}

	distDir := filepath.Join("bin", "dist")
	_ = os.RemoveAll(distDir)

	fmt.Printf("=== CLOUDX CROSS-PLATFORM BUILD HARNESS (PHASE 80) ===\n")
	fmt.Printf("Host Platform: %s/%s | Go Version: %s\n\n", runtime.GOOS, runtime.GOARCH, runtime.Version())
	fmt.Printf("%-12s %-12s %-25s %-25s %-12s\n", "OS", "ARCH", "CLOUDX CLI", "CLOUDX WORKER", "STATUS")

	startTime := time.Now()

	for _, t := range targets {
		targetDir := filepath.Join(distDir, fmt.Sprintf("%s_%s", t.OS, t.Arch))
		if err := os.MkdirAll(targetDir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "failed to create dist dir: %v\n", err)
			os.Exit(1)
		}

		cliBin := fmt.Sprintf("cloudx%s", t.Ext)
		workerBin := fmt.Sprintf("cloudx-worker%s", t.Ext)

		cliPath := filepath.Join(targetDir, cliBin)
		workerPath := filepath.Join(targetDir, workerBin)

		// 1. Build CLI
		cmdCLI := exec.Command("go", "build", "-trimpath", "-ldflags", "-s -w", "-o", cliPath, "./cmd/cloudx")
		cmdCLI.Env = append(os.Environ(), "GOOS="+t.OS, "GOARCH="+t.Arch, "CGO_ENABLED=0")
		if out, err := cmdCLI.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to build CLI for %s/%s: %v\nOutput: %s\n", t.OS, t.Arch, err, string(out))
			os.Exit(1)
		}

		// 2. Build Worker
		cmdWorker := exec.Command("go", "build", "-trimpath", "-ldflags", "-s -w", "-o", workerPath, "./cmd/cloudx-worker")
		cmdWorker.Env = append(os.Environ(), "GOOS="+t.OS, "GOARCH="+t.Arch, "CGO_ENABLED=0")
		if out, err := cmdWorker.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to build Worker for %s/%s: %v\nOutput: %s\n", t.OS, t.Arch, err, string(out))
			os.Exit(1)
		}

		// Inspect binary sizes
		cliStat, _ := os.Stat(cliPath)
		workerStat, _ := os.Stat(workerPath)

		cliSizeMB := float64(cliStat.Size()) / (1024 * 1024)
		workerSizeMB := float64(workerStat.Size()) / (1024 * 1024)

		fmt.Printf("%-12s %-12s %-25s %-25s %-12s\n",
			t.OS, t.Arch,
			fmt.Sprintf("%s (%.1f MB)", cliBin, cliSizeMB),
			fmt.Sprintf("%s (%.1f MB)", workerBin, workerSizeMB),
			"✓ SUCCESS",
		)
	}

	fmt.Printf("\nBuild completed in %v. All 12 binaries generated cleanly.\n", time.Since(startTime).Round(time.Millisecond))
	fmt.Printf("=======================================================\n")
}
