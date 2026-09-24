//go:build !windows

package monitor

import (
	"context"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// PosixCollector collects resource metrics on Linux/Unix/macOS systems.
type PosixCollector struct {
	lastIdle  uint64
	lastTotal uint64
	lastTime  time.Time
}

// NewPlatformCollector returns a POSIX Collector instance.
func NewPlatformCollector() Collector {
	return &PosixCollector{}
}

func (c *PosixCollector) Platform() string {
	return runtime.GOOS
}

func (c *PosixCollector) Collect(ctx context.Context) (*ResourceMetrics, error) {
	metrics := &ResourceMetrics{
		Timestamp:    time.Now().UTC(),
		Platform:     runtime.GOOS,
		SystemLoad1:  -1.0,
		SystemLoad5:  -1.0,
		SystemLoad15: -1.0,
	}

	// 1. Read Linux /proc/meminfo if available
	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		lines := strings.Split(string(data), "\n")
		var totalKb, availKb int64
		for _, line := range lines {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				switch fields[0] {
				case "MemTotal:":
					totalKb, _ = strconv.ParseInt(fields[1], 10, 64)
				case "MemAvailable:":
					availKb, _ = strconv.ParseInt(fields[1], 10, 64)
				}
			}
		}
		if totalKb > 0 {
			metrics.TotalMemoryBytes = totalKb * 1024
			metrics.MemoryAvailBytes = availKb * 1024
			metrics.MemoryUsedBytes = (totalKb - availKb) * 1024
		}
	} else {
		// Fallback memory stats via runtime.MemStats
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		metrics.MemoryUsedBytes = int64(m.Alloc)
		metrics.TotalMemoryBytes = int64(m.Sys)
		metrics.MemoryAvailBytes = int64(m.Sys - m.Alloc)
	}

	// 2. Read Linux /proc/loadavg if available
	if data, err := os.ReadFile("/proc/loadavg"); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) >= 3 {
			l1, _ := strconv.ParseFloat(fields[0], 64)
			l5, _ := strconv.ParseFloat(fields[1], 64)
			l15, _ := strconv.ParseFloat(fields[2], 64)
			metrics.SystemLoad1 = l1
			metrics.SystemLoad5 = l5
			metrics.SystemLoad15 = l15
		}
	}

	// 3. Read Linux /proc/stat for CPU
	if data, err := os.ReadFile("/proc/stat"); err == nil {
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "cpu ") {
				fields := strings.Fields(line)
				if len(fields) >= 5 {
					user, _ := strconv.ParseUint(fields[1], 10, 64)
					nice, _ := strconv.ParseUint(fields[2], 10, 64)
					system, _ := strconv.ParseUint(fields[3], 10, 64)
					idle, _ := strconv.ParseUint(fields[4], 10, 64)
					total := user + nice + system + idle

					if c.lastTime.IsZero() {
						c.lastIdle = idle
						c.lastTotal = total
						c.lastTime = time.Now()
						metrics.CPUUsagePercent = 0.0
					} else {
						deltaIdle := idle - c.lastIdle
						deltaTotal := total - c.lastTotal
						if deltaTotal > 0 && deltaTotal >= deltaIdle {
							metrics.CPUUsagePercent = float64(deltaTotal-deltaIdle) * 100.0 / float64(deltaTotal)
						}
						c.lastIdle = idle
						c.lastTotal = total
						c.lastTime = time.Now()
					}
				}
				break
			}
		}
	}

	// 4. Process count via /proc
	if entries, err := os.ReadDir("/proc"); err == nil {
		procCount := 0
		for _, entry := range entries {
			if entry.IsDir() {
				if _, err := strconv.Atoi(entry.Name()); err == nil {
					procCount++
				}
			}
		}
		metrics.ProcessCount = procCount
	}

	return metrics, nil
}
