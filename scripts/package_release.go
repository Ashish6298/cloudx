package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Target struct {
	OS      string
	Arch    string
	Ext     string
	Archive string // "zip" for windows, "tar.gz" for linux/darwin
}

func main() {
	version := "v1.0.0"
	if envVer := os.Getenv("CLOUDX_VERSION"); envVer != "" {
		version = envVer
	}

	// 1. Resolve Git Commit
	gitCommit := "dev"
	if out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output(); err == nil {
		gitCommit = strings.TrimSpace(string(out))
	}

	buildDate := time.Now().UTC().Format(time.RFC3339)

	targets := []Target{
		{"windows", "amd64", ".exe", "zip"},
		{"windows", "arm64", ".exe", "zip"},
		{"linux", "amd64", "", "tar.gz"},
		{"linux", "arm64", "", "tar.gz"},
		{"darwin", "amd64", "", "tar.gz"},
		{"darwin", "arm64", "", "tar.gz"},
	}

	releaseDir := filepath.Join("bin", "release")
	_ = os.RemoveAll(releaseDir)
	if err := os.MkdirAll(releaseDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "failed to create release directory: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("=== CLOUDX RELEASE PACKAGING PIPELINE (PHASE 81) ===\n")
	fmt.Printf("Release Version: %s\n", version)
	fmt.Printf("Git Commit:      %s\n", gitCommit)
	fmt.Printf("Build Timestamp: %s\n\n", buildDate)
	fmt.Printf("%-10s %-10s %-38s %-12s %-12s\n", "OS", "ARCH", "ARCHIVE PACKAGE", "SIZE", "SHA256 CHECKSUM")

	var checksumLines []string

	for _, t := range targets {
		stageDir := filepath.Join(releaseDir, fmt.Sprintf("cloudx_%s_%s_%s", version, t.OS, t.Arch))
		_ = os.MkdirAll(stageDir, 0755)

		cliName := fmt.Sprintf("cloudx%s", t.Ext)
		workerName := fmt.Sprintf("cloudx-worker%s", t.Ext)

		cliPath := filepath.Join(stageDir, cliName)
		workerPath := filepath.Join(stageDir, workerName)

		ldflags := fmt.Sprintf("-s -w -X github.com/cloudx-org/cloudx/internal/common/version.Version=%s -X github.com/cloudx-org/cloudx/internal/common/version.GitCommit=%s -X github.com/cloudx-org/cloudx/internal/common/version.BuildDate=%s",
			version, gitCommit, buildDate)

		// 1. Build CLI
		cmdCLI := exec.Command("go", "build", "-trimpath", "-ldflags", ldflags, "-o", cliPath, "./cmd/cloudx")
		cmdCLI.Env = append(os.Environ(), "GOOS="+t.OS, "GOARCH="+t.Arch, "CGO_ENABLED=0")
		if out, err := cmdCLI.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to build CLI %s/%s: %v (%s)\n", t.OS, t.Arch, err, string(out))
			os.Exit(1)
		}

		// 2. Build Worker
		cmdWorker := exec.Command("go", "build", "-trimpath", "-ldflags", ldflags, "-o", workerPath, "./cmd/cloudx-worker")
		cmdWorker.Env = append(os.Environ(), "GOOS="+t.OS, "GOARCH="+t.Arch, "CGO_ENABLED=0")
		if out, err := cmdWorker.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to build Worker %s/%s: %v (%s)\n", t.OS, t.Arch, err, string(out))
			os.Exit(1)
		}

		// Include README / License / sample config in release archive
		_ = copyFile("LICENSE", filepath.Join(stageDir, "LICENSE"))
		_ = copyFile("README.md", filepath.Join(stageDir, "README.md"))

		archiveName := fmt.Sprintf("cloudx_%s_%s_%s.%s", version, t.OS, t.Arch, t.Archive)
		archivePath := filepath.Join(releaseDir, archiveName)

		if t.Archive == "zip" {
			if err := createZip(stageDir, archivePath); err != nil {
				fmt.Fprintf(os.Stderr, "failed to create zip: %v\n", err)
				os.Exit(1)
			}
		} else {
			if err := createTarGz(stageDir, archivePath); err != nil {
				fmt.Fprintf(os.Stderr, "failed to create tar.gz: %v\n", err)
				os.Exit(1)
			}
		}

		// Cleanup stage folder
		_ = os.RemoveAll(stageDir)

		// Calculate SHA-256 Checksum
		sum, size, err := computeSHA256(archivePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to compute checksum: %v\n", err)
			os.Exit(1)
		}

		checksumLines = append(checksumLines, fmt.Sprintf("%s  %s", sum, archiveName))
		sizeMB := float64(size) / (1024 * 1024)

		fmt.Printf("%-10s %-10s %-38s %-12s %-12s\n",
			t.OS, t.Arch, archiveName, fmt.Sprintf("%.1f MB", sizeMB), sum[:12]+"...")
	}

	// Write CHECKSUMS.txt
	checksumsFile := filepath.Join(releaseDir, "CHECKSUMS.txt")
	_ = os.WriteFile(checksumsFile, []byte(strings.Join(checksumLines, "\n")+"\n"), 0644)

	fmt.Printf("\n✓ Generated SHA256 checksums file: %s\n", checksumsFile)
	fmt.Printf("====================================================\n")
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func computeSHA256(filePath string) (string, int64, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()

	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), size, nil
}

func createZip(srcDir, zipPath string) error {
	zipFile, err := os.Create(zipPath)
	if err != nil {
		return err
	}
	defer zipFile.Close()

	w := zip.NewWriter(zipFile)
	defer w.Close()

	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		f, err := w.Create(relPath)
		if err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		_, err = io.Copy(f, in)
		return err
	})
}

func createTarGz(srcDir, tarPath string) error {
	tarFile, err := os.Create(tarPath)
	if err != nil {
		return err
	}
	defer tarFile.Close()

	gw := gzip.NewWriter(tarFile)
	defer gw.Close()

	tw := tar.NewWriter(gw)
	defer tw.Close()

	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(info, info.Name())
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relPath)
		header.Mode = 0755
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		_, err = io.Copy(tw, in)
		return err
	})
}
