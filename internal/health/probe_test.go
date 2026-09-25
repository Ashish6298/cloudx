package health

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/common/logging"
)

func TestProber_ProcessAlive(t *testing.T) {
	prober := NewDefaultProber()
	pid := os.Getpid()

	res := prober.Check(context.Background(), ProbeConfig{
		Type: CheckTypeProcess,
		PID:  pid,
	})
	if !res.Healthy {
		t.Fatalf("expected current process %d to be healthy, got: %v", pid, res.Error)
	}

	// Non-existent invalid PID
	resInvalid := prober.Check(context.Background(), ProbeConfig{
		Type: CheckTypeProcess,
		PID:  -999,
	})
	if resInvalid.Healthy {
		t.Fatalf("expected invalid pid -999 to be unhealthy")
	}
}

func TestProber_TCPPort(t *testing.T) {
	// Start TCP listener
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen tcp: %v", err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port
	prober := NewDefaultProber()

	// 1. Healthy open port
	res := prober.Check(context.Background(), ProbeConfig{
		Type:    CheckTypeTCP,
		Host:    "127.0.0.1",
		Port:    port,
		Timeout: 500 * time.Millisecond,
	})
	if !res.Healthy {
		t.Fatalf("expected open TCP port %d to be healthy, got error: %v", port, res.Error)
	}

	// 2. Closed port
	resClosed := prober.Check(context.Background(), ProbeConfig{
		Type:    CheckTypeTCP,
		Host:    "127.0.0.1",
		Port:    65530, // Assuming closed
		Timeout: 200 * time.Millisecond,
	})
	if resClosed.Healthy {
		t.Fatalf("expected closed port to be unhealthy")
	}
}

func TestProber_HTTPEndpoint(t *testing.T) {
	var statusCode int = http.StatusOK
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		code := statusCode
		mu.Unlock()

		if r.URL.Path == "/healthz" {
			w.WriteHeader(code)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	port := server.Listener.Addr().(*net.TCPAddr).Port
	prober := NewDefaultProber()

	// 1. Success HTTP 200
	res := prober.Check(context.Background(), ProbeConfig{
		Type:    CheckTypeHTTP,
		Host:    "127.0.0.1",
		Port:    port,
		Path:    "/healthz",
		Timeout: 500 * time.Millisecond,
	})
	if !res.Healthy {
		t.Fatalf("expected HTTP 200 to be healthy, got: %v", res.Error)
	}

	// 2. Failure HTTP 503
	mu.Lock()
	statusCode = http.StatusServiceUnavailable
	mu.Unlock()

	res503 := prober.Check(context.Background(), ProbeConfig{
		Type:    CheckTypeHTTP,
		Host:    "127.0.0.1",
		Port:    port,
		Path:    "/healthz",
		Timeout: 500 * time.Millisecond,
	})
	if res503.Healthy {
		t.Fatalf("expected HTTP 503 to be unhealthy")
	}

	// 3. 404 Path
	res404 := prober.Check(context.Background(), ProbeConfig{
		Type:    CheckTypeHTTP,
		Host:    "127.0.0.1",
		Port:    port,
		Path:    "/nonexistent",
		Timeout: 500 * time.Millisecond,
	})
	if res404.Healthy {
		t.Fatalf("expected 404 to be unhealthy")
	}
}

// MockProber allows deterministic control over probe success and failures.
type MockProber struct {
	mu      sync.Mutex
	healthy bool
	err     string
}

func (m *MockProber) SetHealthy(healthy bool, err string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.healthy = healthy
	m.err = err
}

func (m *MockProber) Check(ctx context.Context, cfg ProbeConfig) ProbeResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	return ProbeResult{
		Timestamp: time.Now().UTC(),
		Healthy:   m.healthy,
		Latency:   10 * time.Millisecond,
		Error:     m.err,
	}
}

func TestTaskHealthMonitor_ThresholdStateProgression(t *testing.T) {
	mock := &MockProber{healthy: false, err: "service starting up"}
	taskID := id.NewTaskID()

	var stateHistory []struct {
		prev HealthState
		curr HealthState
	}
	var mu sync.Mutex

	monitor := NewTaskHealthMonitor(mock, logging.NewDefaultLogger(), func(tid id.ID, prev, curr HealthState, details string) {
		mu.Lock()
		defer mu.Unlock()
		stateHistory = append(stateHistory, struct {
			prev HealthState
			curr HealthState
		}{prev: prev, curr: curr})
	})
	defer monitor.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := ProbeConfig{
		Type:             CheckTypeHTTP,
		Port:             8080,
		Interval:         50 * time.Millisecond,
		Timeout:          50 * time.Millisecond,
		SuccessThreshold: 2,
		FailureThreshold: 3,
	}

	// 1. Register task: Initial state UNKNOWN
	if err := monitor.RegisterTask(ctx, taskID, cfg); err != nil {
		t.Fatalf("failed to register task: %v", err)
	}

	snap, _ := monitor.GetStatus(taskID)
	if snap.State != HealthStateUnknown {
		// After initial immediate check with 1 failure, state remains UNKNOWN because FailureThreshold is 3
		t.Fatalf("expected initial state UNKNOWN before failure threshold reached, got %s", snap.State)
	}

	// 2. Allow 3 failures to accrue -> should transition UNKNOWN -> UNHEALTHY
	time.Sleep(200 * time.Millisecond)

	snap, _ = monitor.GetStatus(taskID)
	if snap.State != HealthStateUnhealthy {
		t.Fatalf("expected state UNHEALTHY after 3 consecutive failures, got %s", snap.State)
	}

	// 3. Transition to Healthy: require 2 consecutive successes
	mock.SetHealthy(true, "")
	time.Sleep(150 * time.Millisecond)

	snap, _ = monitor.GetStatus(taskID)
	if snap.State != HealthStateHealthy {
		t.Fatalf("expected state HEALTHY after 2 consecutive successes, got %s", snap.State)
	}

	// 4. Single transient failure: does not immediately mark UNHEALTHY (since threshold = 3)
	mock.SetHealthy(false, "transient error")
	time.Sleep(60 * time.Millisecond)

	snap, _ = monitor.GetStatus(taskID)
	// Should remain HEALTHY while failures < 3
	if snap.ConsecutiveFailures < 1 {
		t.Fatalf("expected failure count >= 1")
	}

	// 5. Reach failure threshold
	time.Sleep(150 * time.Millisecond)
	snap, _ = monitor.GetStatus(taskID)
	if snap.State != HealthStateUnhealthy {
		t.Fatalf("expected state UNHEALTHY after sustained failures, got %s", snap.State)
	}

	// Verify transitions recorded
	mu.Lock()
	defer mu.Unlock()
	if len(stateHistory) < 3 {
		t.Fatalf("expected at least 3 state transitions, got %d (%v)", len(stateHistory), stateHistory)
	}
}

func TestTaskHealthMonitor_UpdatePIDAndUnregister(t *testing.T) {
	mock := &MockProber{healthy: true}
	taskID := id.NewTaskID()

	monitor := NewTaskHealthMonitor(mock, logging.NewDefaultLogger(), nil)
	defer monitor.Close()

	cfg := ProbeConfig{
		Type:             CheckTypeProcess,
		PID:              1000,
		Interval:         50 * time.Millisecond,
		SuccessThreshold: 1,
	}

	_ = monitor.RegisterTask(context.Background(), taskID, cfg)
	monitor.UpdatePID(taskID, 2000)

	mt, ok := monitor.monitored[taskID]
	if !ok || mt.cfg.PID != 2000 {
		t.Fatalf("expected PID updated to 2000, got %+v", mt)
	}

	monitor.UnregisterTask(taskID)
	_, exists := monitor.GetStatus(taskID)
	if exists {
		t.Fatalf("expected task %s to be removed from monitor", taskID)
	}
}
