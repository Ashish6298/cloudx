package health

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
	"unsafe"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/common/logging"
)

func unsafePointer(p any) unsafe.Pointer {
	return unsafe.Pointer(&p)
}

// HealthState represents the health status of a monitored task/service.
type HealthState string

const (
	HealthStateUnknown   HealthState = "UNKNOWN"
	HealthStateHealthy   HealthState = "HEALTHY"
	HealthStateUnhealthy HealthState = "UNHEALTHY"
)

// CheckType defines the type of health probe.
type CheckType string

const (
	CheckTypeProcess CheckType = "process"
	CheckTypeTCP     CheckType = "tcp"
	CheckTypeHTTP    CheckType = "http"
)

// ProbeConfig specifies the parameters and thresholds for health probing.
type ProbeConfig struct {
	Type             CheckType     `json:"type" yaml:"type"`                         // process, tcp, http
	PID              int           `json:"pid,omitempty" yaml:"pid,omitempty"`       // for process check
	Port             int           `json:"port,omitempty" yaml:"port,omitempty"`     // for tcp / http check
	Path             string        `json:"path,omitempty" yaml:"path,omitempty"`     // for http check (e.g. /healthz)
	Host             string        `json:"host,omitempty" yaml:"host,omitempty"`     // defaults to 127.0.0.1
	Interval         time.Duration `json:"interval" yaml:"interval"`                 // probe period (e.g. 500ms, 1s)
	Timeout          time.Duration `json:"timeout" yaml:"timeout"`                   // per-probe timeout
	FailureThreshold int           `json:"failure_threshold" yaml:"failure_threshold"` // consecutive failures to declare UNHEALTHY
	SuccessThreshold int           `json:"success_threshold" yaml:"success_threshold"` // consecutive successes to declare HEALTHY
}

// DefaultProbeConfig returns standard default probe settings.
func DefaultProbeConfig() ProbeConfig {
	return ProbeConfig{
		Type:             CheckTypeProcess,
		Host:             "127.0.0.1",
		Interval:         1 * time.Second,
		Timeout:          500 * time.Millisecond,
		FailureThreshold: 3,
		SuccessThreshold: 1,
	}
}

// ProbeResult records the outcome of an individual health probe execution.
type ProbeResult struct {
	Timestamp time.Time     `json:"timestamp"`
	Healthy   bool          `json:"healthy"`
	Latency   time.Duration `json:"latency"`
	Error     string        `json:"error,omitempty"`
}

// TaskHealthStatus holds the continuous health observation state of a task.
type TaskHealthStatus struct {
	TaskID              id.ID        `json:"task_id"`
	State               HealthState  `json:"state"`
	ConsecutiveSuccess  int          `json:"consecutive_success"`
	ConsecutiveFailures int          `json:"consecutive_failures"`
	TotalChecks         int          `json:"total_checks"`
	LastCheckTime       time.Time    `json:"last_check_time"`
	LastResult          *ProbeResult `json:"last_result,omitempty"`
}

// StateChangeCallback is invoked when a task transitions health states (e.g. UNKNOWN -> HEALTHY or HEALTHY -> UNHEALTHY).
type StateChangeCallback func(taskID id.ID, prev, current HealthState, details string)

// Prober executes individual checks (process, tcp, http) against a target.
type Prober interface {
	Check(ctx context.Context, cfg ProbeConfig) ProbeResult
}

// DefaultProber implements standard Process, TCP, and HTTP health checks.
type DefaultProber struct {
	httpClient *http.Client
}

// NewDefaultProber creates a new DefaultProber.
func NewDefaultProber() *DefaultProber {
	return &DefaultProber{
		httpClient: &http.Client{
			// Transport timeouts handled via per-request context
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// Check evaluates the health of the target according to ProbeConfig.
func (p *DefaultProber) Check(ctx context.Context, cfg ProbeConfig) ProbeResult {
	start := time.Now()
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 1 * time.Second
	}

	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	host := cfg.Host
	if host == "" {
		host = "127.0.0.1"
	}

	switch cfg.Type {
	case CheckTypeTCP:
		return p.checkTCP(checkCtx, host, cfg.Port, start)
	case CheckTypeHTTP:
		return p.checkHTTP(checkCtx, host, cfg.Port, cfg.Path, start)
	case CheckTypeProcess:
		fallthrough
	default:
		return p.checkProcess(cfg.PID, start)
	}
}

// checkProcess checks if the operating system process with the given PID is alive and running.
func (p *DefaultProber) checkProcess(pid int, start time.Time) ProbeResult {
	return checkOSProcessAlive(pid, start)
}

// checkTCP attempts a TCP connection to host:port.
func (p *DefaultProber) checkTCP(ctx context.Context, host string, port int, start time.Time) ProbeResult {
	if port <= 0 || port > 65535 {
		return ProbeResult{
			Timestamp: start,
			Healthy:   false,
			Latency:   time.Since(start),
			Error:     fmt.Sprintf("invalid port %d", port),
		}
	}

	addr := fmt.Sprintf("%s:%d", host, port)
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return ProbeResult{
			Timestamp: start,
			Healthy:   false,
			Latency:   time.Since(start),
			Error:     fmt.Sprintf("tcp dial to %s failed: %v", addr, err),
		}
	}
	_ = conn.Close()

	return ProbeResult{
		Timestamp: start,
		Healthy:   true,
		Latency:   time.Since(start),
	}
}

// checkHTTP performs an HTTP GET request to http://host:port/path and expects status in [200, 399].
func (p *DefaultProber) checkHTTP(ctx context.Context, host string, port int, path string, start time.Time) ProbeResult {
	if port <= 0 || port > 65535 {
		return ProbeResult{
			Timestamp: start,
			Healthy:   false,
			Latency:   time.Since(start),
			Error:     fmt.Sprintf("invalid port %d", port),
		}
	}

	if path == "" {
		path = "/"
	} else if path[0] != '/' {
		path = "/" + path
	}

	url := fmt.Sprintf("http://%s:%d%s", host, port, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ProbeResult{
			Timestamp: start,
			Healthy:   false,
			Latency:   time.Since(start),
			Error:     fmt.Sprintf("failed to create http request: %v", err),
		}
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return ProbeResult{
			Timestamp: start,
			Healthy:   false,
			Latency:   time.Since(start),
			Error:     fmt.Sprintf("http get %s failed: %v", url, err),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return ProbeResult{
			Timestamp: start,
			Healthy:   false,
			Latency:   time.Since(start),
			Error:     fmt.Sprintf("http probe %s returned status %d", url, resp.StatusCode),
		}
	}

	return ProbeResult{
		Timestamp: start,
		Healthy:   true,
		Latency:   time.Since(start),
	}
}

// monitoredTask wraps an active probe target.
type monitoredTask struct {
	taskID id.ID
	cfg    ProbeConfig
	status TaskHealthStatus
	cancel context.CancelFunc
}

// TaskHealthMonitor runs periodic probes for registered tasks and tracks transitions between
// UNKNOWN, HEALTHY, and UNHEALTHY based on configurable failure/success thresholds.
type TaskHealthMonitor struct {
	mu         sync.RWMutex
	prober     Prober
	logger     logging.Logger
	onStatus   StateChangeCallback
	monitored  map[id.ID]*monitoredTask
	closed     bool
}

// NewTaskHealthMonitor constructs a new TaskHealthMonitor.
func NewTaskHealthMonitor(prober Prober, logger logging.Logger, onStatus StateChangeCallback) *TaskHealthMonitor {
	if prober == nil {
		prober = NewDefaultProber()
	}
	if logger == nil {
		logger = logging.NewDefaultLogger()
	}

	return &TaskHealthMonitor{
		prober:    prober,
		logger:    logger.With("component", "task_health_monitor"),
		onStatus:  onStatus,
		monitored: make(map[id.ID]*monitoredTask),
	}
}

// RegisterTask registers or updates a task probe configuration and launches the probe loop.
func (m *TaskHealthMonitor) RegisterTask(ctx context.Context, taskID id.ID, cfg ProbeConfig) error {
	if taskID == "" {
		return fmt.Errorf("task_id is required")
	}

	// Normalize defaults
	if cfg.Interval <= 0 {
		cfg.Interval = 1 * time.Second
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 500 * time.Millisecond
	}
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 3
	}
	if cfg.SuccessThreshold <= 0 {
		cfg.SuccessThreshold = 1
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return fmt.Errorf("health monitor is closed")
	}

	// If already registered, cancel previous probe loop
	if existing, ok := m.monitored[taskID]; ok {
		if existing.cancel != nil {
			existing.cancel()
		}
	}

	probeCtx, cancel := context.WithCancel(ctx)
	mt := &monitoredTask{
		taskID: taskID,
		cfg:    cfg,
		status: TaskHealthStatus{
			TaskID: taskID,
			State:  HealthStateUnknown,
		},
		cancel: cancel,
	}
	m.monitored[taskID] = mt
	m.mu.Unlock()

	m.logger.Info("Registered health probe for task %s (type: %s, interval: %v, timeout: %v, fail_thresh: %d, succ_thresh: %d)",
		taskID, cfg.Type, cfg.Interval, cfg.Timeout, cfg.FailureThreshold, cfg.SuccessThreshold)

	// Launch probe worker goroutine
	go m.runProbeLoop(probeCtx, mt)

	return nil
}

// UpdatePID updates the PID of an actively monitored task (e.g. after process start/restart).
func (m *TaskHealthMonitor) UpdatePID(taskID id.ID, pid int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if mt, ok := m.monitored[taskID]; ok {
		mt.cfg.PID = pid
	}
}

// UnregisterTask stops probing the specified task and removes it from the monitor.
func (m *TaskHealthMonitor) UnregisterTask(taskID id.ID) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if mt, ok := m.monitored[taskID]; ok {
		if mt.cancel != nil {
			mt.cancel()
		}
		delete(m.monitored, taskID)
		m.logger.Debug("Unregistered health probe for task %s", taskID)
	}
}

// GetStatus returns a snapshot of the task's health status.
func (m *TaskHealthMonitor) GetStatus(taskID id.ID) (TaskHealthStatus, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if mt, ok := m.monitored[taskID]; ok {
		return mt.status, true
	}
	return TaskHealthStatus{State: HealthStateUnknown}, false
}

// ListStatuses returns all tracked task health snapshots.
func (m *TaskHealthMonitor) ListStatuses() []TaskHealthStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	res := make([]TaskHealthStatus, 0, len(m.monitored))
	for _, mt := range m.monitored {
		res = append(res, mt.status)
	}
	return res
}

// runProbeLoop runs the periodic probe check for a single task until canceled.
func (m *TaskHealthMonitor) runProbeLoop(ctx context.Context, mt *monitoredTask) {
	ticker := time.NewTicker(mt.cfg.Interval)
	defer ticker.Stop()

	// Perform initial check immediately
	m.evaluateProbe(ctx, mt)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.evaluateProbe(ctx, mt)
		}
	}
}

// evaluateProbe executes a single probe check and applies threshold state transition logic.
func (m *TaskHealthMonitor) evaluateProbe(ctx context.Context, mt *monitoredTask) {
	m.mu.RLock()
	cfg := mt.cfg
	m.mu.RUnlock()

	result := m.prober.Check(ctx, cfg)

	m.mu.Lock()
	mt.status.TotalChecks++
	mt.status.LastCheckTime = result.Timestamp
	mt.status.LastResult = &result

	prevState := mt.status.State
	var transitioned bool
	var details string

	if result.Healthy {
		mt.status.ConsecutiveSuccess++
		mt.status.ConsecutiveFailures = 0

		if mt.status.ConsecutiveSuccess >= cfg.SuccessThreshold && prevState != HealthStateHealthy {
			mt.status.State = HealthStateHealthy
			transitioned = true
			details = fmt.Sprintf("Passed %d consecutive health checks (latency: %v)", mt.status.ConsecutiveSuccess, result.Latency)
		}
	} else {
		mt.status.ConsecutiveFailures++
		mt.status.ConsecutiveSuccess = 0

		if mt.status.ConsecutiveFailures >= cfg.FailureThreshold && prevState != HealthStateUnhealthy {
			mt.status.State = HealthStateUnhealthy
			transitioned = true
			details = fmt.Sprintf("Failed %d consecutive health checks: %s", mt.status.ConsecutiveFailures, result.Error)
		}
	}

	currState := mt.status.State
	callback := m.onStatus
	m.mu.Unlock()

	if transitioned {
		m.logger.Info("Task %s health state transitioned: %s -> %s (%s)", mt.taskID, prevState, currState, details)
		if callback != nil {
			callback(mt.taskID, prevState, currState, details)
		}
	}
}

// Close terminates all active probe loops.
func (m *TaskHealthMonitor) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.closed = true
	for _, mt := range m.monitored {
		if mt.cancel != nil {
			mt.cancel()
		}
	}
	m.monitored = make(map[id.ID]*monitoredTask)
	return nil
}
