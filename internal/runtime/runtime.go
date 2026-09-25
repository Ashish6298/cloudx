package runtime

import (
	"context"
	"errors"
	"io"
	"os"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
)

var (
	ErrProcessNotFound    = errors.New("runtime: process not found")
	ErrProcessAlreadyRuns = errors.New("runtime: process is already running")
	ErrUnsupportedSignal  = errors.New("runtime: unsupported signal")
	ErrInvalidProcessSpec = errors.New("runtime: invalid process spec")
)

// ProcessSpec defines the specification required to launch a workload.
type ProcessSpec struct {
	ID          id.ID             `json:"id" yaml:"id"`
	Command     string            `json:"command" yaml:"command"`
	Args        []string          `json:"args,omitempty" yaml:"args,omitempty"`
	Environment map[string]string `json:"environment,omitempty" yaml:"environment,omitempty"`
	WorkingDir  string            `json:"working_dir,omitempty" yaml:"working_dir,omitempty"`
	Stdout      io.Writer         `json:"-" yaml:"-"`
	Stderr      io.Writer         `json:"-" yaml:"-"`
}

// ProcessStatus represents the runtime status of a process.
type ProcessStatus struct {
	ID        id.ID     `json:"id" yaml:"id"`
	PID       int       `json:"pid" yaml:"pid"`
	Running   bool      `json:"running" yaml:"running"`
	ExitCode  int       `json:"exit_code" yaml:"exit_code"`
	StartTime time.Time `json:"start_time" yaml:"start_time"`
	Duration  time.Duration `json:"duration" yaml:"duration"`
	Error     string    `json:"error,omitempty" yaml:"error,omitempty"`
}

// LogOptions configures log streaming / reading.
type LogOptions struct {
	Follow     bool `json:"follow"`
	TailLines  int  `json:"tail_lines"`
	ShowStdout bool `json:"show_stdout"`
	ShowStderr bool `json:"show_stderr"`
}

// Runtime provides an abstraction over workload execution engines (Native, Docker, etc.).
// The CloudX control plane and worker orchestration rely exclusively on this interface
// and never invoke os/exec or platform APIs directly.
type Runtime interface {
	// Type returns the runtime provider classification (e.g. "native", "docker").
	Type() string

	// Start launches a new process described by spec and returns its initial status.
	Start(ctx context.Context, spec ProcessSpec) (*ProcessStatus, error)

	// Stop terminates the process identified by id gracefully, waiting up to timeout before killing it.
	Stop(ctx context.Context, id id.ID, timeout time.Duration) error

	// Restart stops (if running) and starts the process again with its existing specification.
	Restart(ctx context.Context, id id.ID, timeout time.Duration) (*ProcessStatus, error)

	// Inspect retrieves current runtime metadata and state for a process.
	Inspect(ctx context.Context, id id.ID) (*ProcessStatus, error)

	// Logs retrieves stdout/stderr logs for a process.
	Logs(ctx context.Context, id id.ID, opts LogOptions) (io.ReadCloser, error)

	// Signal sends an OS signal (e.g. SIGTERM, SIGKILL, SIGHUP) to a running process.
	Signal(ctx context.Context, id id.ID, sig os.Signal) error

	// Close cleans up all processes and resources managed by this runtime.
	Close() error
}
