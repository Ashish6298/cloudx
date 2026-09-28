//go:build windows

package monitor

import (
	"context"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procGlobalMemoryStatus = kernel32.NewProc("GlobalMemoryStatusEx")
	procGetSystemTimes     = kernel32.NewProc("GetSystemTimes")
	procCreateToolhelp32   = kernel32.NewProc("CreateToolhelp32Snapshot")
	procProcess32First     = kernel32.NewProc("Process32FirstW")
	procProcess32Next      = kernel32.NewProc("Process32NextW")
)

type memorystatusex struct {
	cbSize                  uint32
	dwMemoryLoad            uint32
	ullTotalPhys            uint64
	ullAvailPhys            uint64
	ullTotalPageFile        uint64
	ullAvailPageFile        uint64
	ullTotalVirtual         uint64
	ullAvailVirtual         uint64
	ullAvailExtendedVirtual uint64
}

type processEntry32W struct {
	dwSize              uint32
	cntUsage            uint32
	th32ProcessID       uint32
	th32DefaultHeapID   uintptr
	th32ModuleID        uint32
	cntThreads          uint32
	th32ParentProcessID uint32
	pcPriClassBase      int32
	dwFlags             uint32
	szExeFile           [260]uint16
}

type filetime struct {
	dwLowDateTime  uint32
	dwHighDateTime uint32
}

func filetimeToUint64(ft filetime) uint64 {
	return (uint64(ft.dwHighDateTime) << 32) | uint64(ft.dwLowDateTime)
}

// WindowsCollector collects native Windows telemetry metrics without external CLI dependencies.
type WindowsCollector struct {
	lastIdle   uint64
	lastKernel uint64
	lastUser   uint64
	lastTime   time.Time
}

// NewPlatformCollector returns a new Windows-native Collector instance.
func NewPlatformCollector() Collector {
	return &WindowsCollector{}
}

func (c *WindowsCollector) Platform() string {
	return "windows"
}

func (c *WindowsCollector) Collect(ctx context.Context) (*ResourceMetrics, error) {
	metrics := &ResourceMetrics{
		Timestamp: time.Now().UTC(),
		Platform:  runtime.GOOS,
	}

	// 1. Memory via GlobalMemoryStatusEx
	var mem memorystatusex
	mem.cbSize = uint32(unsafe.Sizeof(mem))
	r1, _, _ := procGlobalMemoryStatus.Call(uintptr(unsafe.Pointer(&mem)))
	if r1 != 0 {
		metrics.TotalMemoryBytes = int64(mem.ullTotalPhys)
		metrics.MemoryAvailBytes = int64(mem.ullAvailPhys)
		metrics.MemoryUsedBytes = int64(mem.ullTotalPhys - mem.ullAvailPhys)
	}

	// 2. CPU Usage via GetSystemTimes
	var idleTime, kernelTime, userTime filetime
	r1, _, _ = procGetSystemTimes.Call(
		uintptr(unsafe.Pointer(&idleTime)),
		uintptr(unsafe.Pointer(&kernelTime)),
		uintptr(unsafe.Pointer(&userTime)),
	)
	if r1 != 0 {
		currIdle := filetimeToUint64(idleTime)
		currKernel := filetimeToUint64(kernelTime)
		currUser := filetimeToUint64(userTime)

		if c.lastTime.IsZero() {
			c.lastIdle = currIdle
			c.lastKernel = currKernel
			c.lastUser = currUser
			c.lastTime = time.Now()
			metrics.CPUUsagePercent = 0.0
		} else {
			deltaIdle := currIdle - c.lastIdle
			deltaKernel := currKernel - c.lastKernel
			deltaUser := currUser - c.lastUser
			totalSys := deltaKernel + deltaUser

			if totalSys > 0 && totalSys >= deltaIdle {
				cpuPercent := float64(totalSys-deltaIdle) * 100.0 / float64(totalSys)
				if cpuPercent < 0 {
					cpuPercent = 0.0
				}
				if cpuPercent > 100.0 {
					cpuPercent = 100.0
				}
				metrics.CPUUsagePercent = cpuPercent
			}

			c.lastIdle = currIdle
			c.lastKernel = currKernel
			c.lastUser = currUser
			c.lastTime = time.Now()
		}
	}

	// 3. Process count via Toolhelp32Snapshot
	const th32csSnapProcess = 0x00000002
	handle, _, _ := procCreateToolhelp32.Call(uintptr(th32csSnapProcess), 0)
	if handle != uintptr(syscall.InvalidHandle) {
		defer syscall.CloseHandle(syscall.Handle(handle))

		var pe processEntry32W
		pe.dwSize = uint32(unsafe.Sizeof(pe))

		count := 0
		r1, _, _ := procProcess32First.Call(handle, uintptr(unsafe.Pointer(&pe)))
		for r1 != 0 {
			count++
			r1, _, _ = procProcess32Next.Call(handle, uintptr(unsafe.Pointer(&pe)))
		}
		metrics.ProcessCount = count
	}

	// 4. System load is unsupported natively on Windows -> 0.0 (per spec)
	metrics.SystemLoad1 = -1.0
	metrics.SystemLoad5 = -1.0
	metrics.SystemLoad15 = -1.0

	return metrics, nil
}
