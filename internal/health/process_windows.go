//go:build windows

package health

import (
	"fmt"
	"os"
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32        = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess = kernel32.NewProc("OpenProcess")
	procGetExitCode = kernel32.NewProc("GetExitCodeProcess")
)

const (
	processQueryLimitedInformation = 0x1000
	stillActive                    = 259
)

func checkOSProcessAlive(pid int, start time.Time) ProbeResult {
	if pid <= 0 {
		return ProbeResult{
			Timestamp: start,
			Healthy:   false,
			Latency:   time.Since(start),
			Error:     "invalid process PID <= 0",
		}
	}

	// First verify with os.FindProcess
	_, err := os.FindProcess(pid)
	if err != nil {
		return ProbeResult{
			Timestamp: start,
			Healthy:   false,
			Latency:   time.Since(start),
			Error:     fmt.Sprintf("process %d not found: %v", pid, err),
		}
	}

	// Open process with query permissions on Windows
	h, _, _ := procOpenProcess.Call(uintptr(processQueryLimitedInformation), 0, uintptr(pid))
	if h == 0 {
		return ProbeResult{
			Timestamp: start,
			Healthy:   false,
			Latency:   time.Since(start),
			Error:     fmt.Sprintf("process %d cannot be opened or is not running", pid),
		}
	}
	defer syscall.CloseHandle(syscall.Handle(h))

	var exitCode uint32
	r1, _, _ := procGetExitCode.Call(h, uintptr(unsafe.Pointer(&exitCode)))
	if r1 == 0 {
		return ProbeResult{
			Timestamp: start,
			Healthy:   false,
			Latency:   time.Since(start),
			Error:     fmt.Sprintf("failed to get exit code for process %d", pid),
		}
	}

	if exitCode != stillActive {
		return ProbeResult{
			Timestamp: start,
			Healthy:   false,
			Latency:   time.Since(start),
			Error:     fmt.Sprintf("process %d exited with code %d", pid, exitCode),
		}
	}

	return ProbeResult{
		Timestamp: start,
		Healthy:   true,
		Latency:   time.Since(start),
	}
}
