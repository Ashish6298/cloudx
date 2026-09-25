package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
)

// nativeProcess holds state for an actively managed or completed native process.
type nativeProcess struct {
	spec      ProcessSpec
	cmd       *exec.Cmd
	pid       int
	running   bool
	exitCode  int
	startTime time.Time
	stopTime  time.Time
	err       error
	logBuf    *bytes.Buffer
	mu        sync.RWMutex
	done      chan struct{}
}

// NativeRuntime implements Runtime using Go's standard os/exec process subsystem.
type NativeRuntime struct {
	mu        sync.RWMutex
	processes map[id.ID]*nativeProcess
	closed    bool
}

// NewNativeRuntime constructs a new NativeRuntime instance.
func NewNativeRuntime() *NativeRuntime {
	return &NativeRuntime{
		processes: make(map[id.ID]*nativeProcess),
	}
}

// Type returns "native".
func (r *NativeRuntime) Type() string {
	return "native"
}

// Start spawns a native OS process in the background, continuously capturing stdout & stderr.
func (r *NativeRuntime) Start(ctx context.Context, spec ProcessSpec) (*ProcessStatus, error) {
	if spec.Command == "" {
		return nil, fmt.Errorf("%w: command cannot be empty", ErrInvalidProcessSpec)
	}
	if spec.ID == "" {
		spec.ID = id.NewTaskID()
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return nil, errors.New("runtime is closed")
	}

	if existing, ok := r.processes[spec.ID]; ok {
		existing.mu.RLock()
		isRunning := existing.running
		existing.mu.RUnlock()
		if isRunning {
			return nil, ErrProcessAlreadyRuns
		}
	}

	cmd := exec.Command(spec.Command, spec.Args...)
	if spec.WorkingDir != "" {
		cmd.Dir = spec.WorkingDir
	}

	// Environment variables
	if len(spec.Environment) > 0 {
		env := os.Environ()
		for k, v := range spec.Environment {
			env = append(env, fmt.Sprintf("%s=%s", k, v))
		}
		cmd.Env = env
	}

	logBuf := &bytes.Buffer{}
	if spec.Stdout != nil {
		cmd.Stdout = io.MultiWriter(logBuf, spec.Stdout)
	} else {
		cmd.Stdout = logBuf
	}

	if spec.Stderr != nil {
		cmd.Stderr = io.MultiWriter(logBuf, spec.Stderr)
	} else {
		cmd.Stderr = logBuf
	}

	proc := &nativeProcess{
		spec:      spec,
		cmd:       cmd,
		running:   true,
		startTime: time.Now().UTC(),
		logBuf:    logBuf,
		done:      make(chan struct{}),
	}

	if err := cmd.Start(); err != nil {
		proc.running = false
		proc.err = err
		proc.exitCode = -1
		return nil, fmt.Errorf("failed to start process: %w", err)
	}

	proc.pid = cmd.Process.Pid
	r.processes[spec.ID] = proc

	// Supervise process completion asynchronously in goroutine
	go func() {
		waitErr := cmd.Wait()
		proc.mu.Lock()
		defer proc.mu.Unlock()

		proc.running = false
		proc.stopTime = time.Now().UTC()
		if waitErr != nil {
			if exitErr, ok := waitErr.(*exec.ExitError); ok {
				proc.exitCode = exitErr.ExitCode()
			} else {
				proc.exitCode = -1
			}
			proc.err = waitErr
		} else {
			proc.exitCode = 0
		}
		close(proc.done)
	}()

	return &ProcessStatus{
		ID:        spec.ID,
		PID:       proc.pid,
		Running:   true,
		StartTime: proc.startTime,
	}, nil
}

// Stop terminates the process. First attempts graceful SIGTERM / interrupt, then force kills after timeout.
func (r *NativeRuntime) Stop(ctx context.Context, id id.ID, timeout time.Duration) error {
	r.mu.RLock()
	proc, ok := r.processes[id]
	r.mu.RUnlock()

	if !ok {
		return ErrProcessNotFound
	}

	proc.mu.RLock()
	if !proc.running {
		proc.mu.RUnlock()
		return nil // already stopped
	}
	cmdProcess := proc.cmd.Process
	doneChan := proc.done
	proc.mu.RUnlock()

	if cmdProcess == nil {
		return nil
	}

	// 1. Attempt graceful stop
	_ = cmdProcess.Signal(os.Interrupt)

	// Wait with timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}

	select {
	case <-doneChan:
		return nil
	case <-time.After(timeout):
		// Force kill
		_ = cmdProcess.Kill()
	case <-ctx.Done():
		_ = cmdProcess.Kill()
		return ctx.Err()
	}

	select {
	case <-doneChan:
		return nil
	case <-time.After(1 * time.Second):
		return fmt.Errorf("timed out waiting for process %d to terminate", cmdProcess.Pid)
	}
}

// Restart stops a process and restarts it with its original spec.
func (r *NativeRuntime) Restart(ctx context.Context, id id.ID, timeout time.Duration) (*ProcessStatus, error) {
	r.mu.RLock()
	proc, ok := r.processes[id]
	r.mu.RUnlock()

	if !ok {
		return nil, ErrProcessNotFound
	}

	spec := proc.spec
	if err := r.Stop(ctx, id, timeout); err != nil {
		return nil, fmt.Errorf("failed to stop process during restart: %w", err)
	}

	return r.Start(ctx, spec)
}

// Inspect returns the current status and metadata of a process.
func (r *NativeRuntime) Inspect(ctx context.Context, id id.ID) (*ProcessStatus, error) {
	r.mu.RLock()
	proc, ok := r.processes[id]
	r.mu.RUnlock()

	if !ok {
		return nil, ErrProcessNotFound
	}

	proc.mu.RLock()
	defer proc.mu.RUnlock()

	duration := time.Since(proc.startTime)
	if !proc.running && !proc.stopTime.IsZero() {
		duration = proc.stopTime.Sub(proc.startTime)
	}

	errMsg := ""
	if proc.err != nil {
		errMsg = proc.err.Error()
	}

	return &ProcessStatus{
		ID:        proc.spec.ID,
		PID:       proc.pid,
		Running:   proc.running,
		ExitCode:  proc.exitCode,
		StartTime: proc.startTime,
		Duration:  duration,
		Error:     errMsg,
	}, nil
}

// Logs returns a stream of captured stdout and stderr.
func (r *NativeRuntime) Logs(ctx context.Context, id id.ID, opts LogOptions) (io.ReadCloser, error) {
	r.mu.RLock()
	proc, ok := r.processes[id]
	r.mu.RUnlock()

	if !ok {
		return nil, ErrProcessNotFound
	}

	proc.mu.RLock()
	data := proc.logBuf.Bytes()
	proc.mu.RUnlock()

	// Return a copy of logs in a ReadCloser
	return io.NopCloser(bytes.NewReader(data)), nil
}

// Signal dispatches an OS signal to the target process.
func (r *NativeRuntime) Signal(ctx context.Context, id id.ID, sig os.Signal) error {
	r.mu.RLock()
	proc, ok := r.processes[id]
	r.mu.RUnlock()

	if !ok {
		return ErrProcessNotFound
	}

	proc.mu.RLock()
	defer proc.mu.RUnlock()

	if !proc.running || proc.cmd.Process == nil {
		return errors.New("process is not running")
	}

	if sysSig, ok := sig.(syscall.Signal); ok {
		return proc.cmd.Process.Signal(sysSig)
	}
	return proc.cmd.Process.Signal(sig)
}

// Close stops all running processes and cleans up resources.
func (r *NativeRuntime) Close() error {
	r.mu.Lock()
	r.closed = true
	procs := make([]*nativeProcess, 0, len(r.processes))
	for _, p := range r.processes {
		procs = append(procs, p)
	}
	r.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, p := range procs {
		p.mu.RLock()
		running := p.running
		pid := p.spec.ID
		p.mu.RUnlock()

		if running {
			_ = r.Stop(ctx, pid, 1*time.Second)
		}
	}

	return nil
}
