//go:build !windows

package health

import (
	"fmt"
	"os"
	"syscall"
	"time"
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

	proc, err := os.FindProcess(pid)
	if err != nil {
		return ProbeResult{
			Timestamp: start,
			Healthy:   false,
			Latency:   time.Since(start),
			Error:     fmt.Sprintf("process %d not found: %v", pid, err),
		}
	}

	// Signal 0 checks if process exists without sending an actual signal
	err = proc.Signal(syscall.Signal(0))
	if err != nil {
		return ProbeResult{
			Timestamp: start,
			Healthy:   false,
			Latency:   time.Since(start),
			Error:     fmt.Sprintf("process %d is not alive: %v", pid, err),
		}
	}

	return ProbeResult{
		Timestamp: start,
		Healthy:   true,
		Latency:   time.Since(start),
	}
}
