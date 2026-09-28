package logs

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
)

// LogEntry captures an individual structured log line associated with a workload.
type LogEntry struct {
	Timestamp    time.Time `json:"timestamp"`
	ServiceID    id.ID     `json:"service_id,omitempty"`
	ServiceName  string    `json:"service_name,omitempty"`
	JobID        id.ID     `json:"job_id,omitempty"`
	JobName      string    `json:"job_name,omitempty"`
	DeploymentID id.ID     `json:"deployment_id,omitempty"`
	TaskID       id.ID     `json:"task_id,omitempty"`
	WorkerID     id.ID     `json:"worker_id,omitempty"`
	Stream       string    `json:"stream"` // "stdout" or "stderr"
	Message      string    `json:"message"`
}

// LogFilter specifies criteria for reading and streaming workload logs.
type LogFilter struct {
	ServiceID    id.ID     `json:"service_id,omitempty"`
	ServiceName  string    `json:"service_name,omitempty"`
	JobID        id.ID     `json:"job_id,omitempty"`
	JobName      string    `json:"job_name,omitempty"`
	DeploymentID id.ID     `json:"deployment_id,omitempty"`
	TaskID       id.ID     `json:"task_id,omitempty"`
	WorkerID     id.ID     `json:"worker_id,omitempty"`
	Since        time.Time `json:"since,omitempty"`
	TailLines    int       `json:"tail_lines,omitempty"`
	Follow       bool      `json:"follow,omitempty"`
}

// WorkloadLogger manages ring-buffered & file-persisted log capture for tasks.
// Ring buffer prevents unlimited memory consumption while disk persistence provides historical inspection.
type WorkloadLogger struct {
	baseDir  string
	maxLines int
	mu       sync.RWMutex
	buffers  map[id.ID]*RingBuffer
	metaMap  map[id.ID]LogEntry
	subMu    sync.RWMutex
	subs     map[id.ID][]chan LogEntry
	allSubs  []chan LogEntry
}

// RingBuffer implements a thread-safe, fixed-capacity circular buffer of log entries to avoid unlimited memory buffering.
type RingBuffer struct {
	mu       sync.RWMutex
	capacity int
	entries  []LogEntry
	head     int
	full     bool
}

// NewRingBuffer initializes a RingBuffer with fixed capacity.
func NewRingBuffer(capacity int) *RingBuffer {
	if capacity <= 0 {
		capacity = 1000 // Default bounded capacity per task
	}
	return &RingBuffer{
		capacity: capacity,
		entries:  make([]LogEntry, capacity),
	}
}

// Append writes a log entry into the circular ring buffer.
func (rb *RingBuffer) Append(entry LogEntry) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	rb.entries[rb.head] = entry
	rb.head = (rb.head + 1) % rb.capacity
	if rb.head == 0 {
		rb.full = true
	}
}

// Entries returns a copy of all active entries in chronological order.
func (rb *RingBuffer) Entries() []LogEntry {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if !rb.full {
		res := make([]LogEntry, rb.head)
		copy(res, rb.entries[:rb.head])
		return res
	}

	res := make([]LogEntry, rb.capacity)
	n := copy(res, rb.entries[rb.head:])
	copy(res[n:], rb.entries[:rb.head])
	return res
}

// Global logger instance for shared in-process access
var (
	defaultLogger *WorkloadLogger
	defaultOnce   sync.Once
)

// DefaultWorkloadLogger returns a process-wide singleton WorkloadLogger.
func DefaultWorkloadLogger() *WorkloadLogger {
	defaultOnce.Do(func() {
		defaultLogger = NewWorkloadLogger("", 1000)
	})
	return defaultLogger
}

// NewWorkloadLogger initializes a WorkloadLogger.
func NewWorkloadLogger(baseDir string, maxLines int) *WorkloadLogger {
	if maxLines <= 0 {
		maxLines = 1000
	}
	if baseDir != "" {
		_ = os.MkdirAll(baseDir, 0755)
	}
	return &WorkloadLogger{
		baseDir:  baseDir,
		maxLines: maxLines,
		buffers:  make(map[id.ID]*RingBuffer),
		metaMap:  make(map[id.ID]LogEntry),
		subs:     make(map[id.ID][]chan LogEntry),
	}
}

// SetBaseDir updates or sets the disk log directory.
func (wl *WorkloadLogger) SetBaseDir(baseDir string) {
	wl.mu.Lock()
	defer wl.mu.Unlock()
	wl.baseDir = baseDir
	if baseDir != "" {
		_ = os.MkdirAll(baseDir, 0755)
	}
}

// LogWriter returns an io.Writer that captures process stdout/stderr into disk files, ring buffer, and subscribers.
func (wl *WorkloadLogger) LogWriter(entryMeta LogEntry) io.Writer {
	wl.mu.Lock()
	wl.metaMap[entryMeta.TaskID] = entryMeta
	wl.mu.Unlock()

	return &taskLogWriter{
		wl:        wl,
		entryMeta: entryMeta,
	}
}

type taskLogWriter struct {
	wl        *WorkloadLogger
	entryMeta LogEntry
	file      *os.File
	fileMu    sync.Mutex
}

func (w *taskLogWriter) Write(p []byte) (n int, err error) {
	now := time.Now().UTC()
	lines := splitLines(p)

	w.wl.mu.Lock()
	rb, ok := w.wl.buffers[w.entryMeta.TaskID]
	if !ok {
		rb = NewRingBuffer(w.wl.maxLines)
		w.wl.buffers[w.entryMeta.TaskID] = rb
	}
	w.wl.mu.Unlock()

	// Append to disk log file if baseDir configured
	if w.wl.baseDir != "" {
		w.fileMu.Lock()
		logPath := filepath.Join(w.wl.baseDir, fmt.Sprintf("%s.log", w.entryMeta.TaskID))
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err == nil {
			for _, line := range lines {
				_, _ = fmt.Fprintf(f, "[%s] [%s] [%s] [%s] %s\n",
					now.Format(time.RFC3339Nano),
					w.entryMeta.Stream,
					w.entryMeta.WorkerID,
					w.entryMeta.DeploymentID,
					line,
				)
			}
			_ = f.Close()
		}
		w.fileMu.Unlock()
	}

	for _, line := range lines {
		entry := LogEntry{
			Timestamp:    now,
			ServiceID:    w.entryMeta.ServiceID,
			ServiceName:  w.entryMeta.ServiceName,
			DeploymentID: w.entryMeta.DeploymentID,
			TaskID:       w.entryMeta.TaskID,
			WorkerID:     w.entryMeta.WorkerID,
			Stream:       w.entryMeta.Stream,
			Message:      line,
		}
		rb.Append(entry)

		// Broadcast to live streaming subscribers
		w.wl.broadcast(entry)
	}

	return len(p), nil
}

func (w *taskLogWriter) Close() error {
	w.fileMu.Lock()
	defer w.fileMu.Unlock()
	if w.file != nil {
		err := w.file.Close()
		w.file = nil
		return err
	}
	return nil
}

func (wl *WorkloadLogger) broadcast(entry LogEntry) {
	wl.subMu.RLock()
	defer wl.subMu.RUnlock()

	// 1. Task-specific subscribers
	if chs, ok := wl.subs[entry.TaskID]; ok {
		for _, ch := range chs {
			select {
			case ch <- entry:
			default:
			}
		}
	}

	// 2. Global subscribers (all tasks / services)
	for _, ch := range wl.allSubs {
		select {
		case ch <- entry:
		default:
		}
	}
}

// ReadTaskLogs retrieves captured log lines for a single task.
func (wl *WorkloadLogger) ReadTaskLogs(taskID id.ID, limit int) []LogEntry {
	wl.mu.RLock()
	rb, ok := wl.buffers[taskID]
	meta := wl.metaMap[taskID]
	baseDir := wl.baseDir
	wl.mu.RUnlock()

	var entries []LogEntry
	if ok && rb != nil {
		entries = rb.Entries()
	} else if baseDir != "" {
		entries = wl.readDiskLogs(taskID, meta)
	}

	if limit > 0 && len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	return entries
}

// ReadFilteredLogs queries and filters logs across one or more tasks.
func (wl *WorkloadLogger) ReadFilteredLogs(filter LogFilter, taskIDs []id.ID) []LogEntry {
	var allEntries []LogEntry

	// If specific TaskID provided in filter, only query that task
	if filter.TaskID != "" {
		taskIDs = []id.ID{filter.TaskID}
	} else if len(taskIDs) == 0 {
		// Collect all known task IDs in buffer & disk
		wl.mu.RLock()
		for tid := range wl.buffers {
			taskIDs = append(taskIDs, tid)
		}
		wl.mu.RUnlock()
	}

	for _, tid := range taskIDs {
		entries := wl.ReadTaskLogs(tid, 0)
		for _, e := range entries {
			if filter.ServiceID != "" && e.ServiceID != "" && e.ServiceID != filter.ServiceID {
				continue
			}
			if filter.ServiceName != "" && e.ServiceName != "" && e.ServiceName != filter.ServiceName {
				continue
			}
			if filter.DeploymentID != "" && e.DeploymentID != "" && e.DeploymentID != filter.DeploymentID {
				continue
			}
			if filter.WorkerID != "" && e.WorkerID != "" && e.WorkerID != filter.WorkerID {
				continue
			}
			if !filter.Since.IsZero() && e.Timestamp.Before(filter.Since) {
				continue
			}
			allEntries = append(allEntries, e)
		}
	}

	// Sort chronologically by timestamp
	sort.Slice(allEntries, func(i, j int) bool {
		return allEntries[i].Timestamp.Before(allEntries[j].Timestamp)
	})

	if filter.TailLines > 0 && len(allEntries) > filter.TailLines {
		allEntries = allEntries[len(allEntries)-filter.TailLines:]
	}

	return allEntries
}

func (wl *WorkloadLogger) readDiskLogs(taskID id.ID, meta LogEntry) []LogEntry {
	logPath := filepath.Join(wl.baseDir, fmt.Sprintf("%s.log", taskID))
	f, err := os.Open(logPath)
	if err != nil {
		return nil
	}
	defer f.Close()

	var entries []LogEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		entry := LogEntry{
			Timestamp:    time.Now().UTC(),
			ServiceID:    meta.ServiceID,
			ServiceName:  meta.ServiceName,
			DeploymentID: meta.DeploymentID,
			TaskID:       taskID,
			WorkerID:     meta.WorkerID,
			Stream:       "stdout",
			Message:      line,
		}

		// Try parsing formatted line: "[2026-09-25T12:00:00Z] [stdout] [worker-1] [dep-1] msg"
		if strings.HasPrefix(line, "[") {
			parts := strings.Split(line, "] ")
			if len(parts) >= 5 {
				tsStr := trimBrackets(parts[0])
				stream := trimBrackets(parts[1])
				worker := trimBrackets(parts[2])
				dep := trimBrackets(parts[3])
				msg := strings.Join(parts[4:], "] ")

				if t, err := time.Parse(time.RFC3339Nano, tsStr); err == nil {
					entry.Timestamp = t
				} else if t, err := time.Parse(time.RFC3339, tsStr); err == nil {
					entry.Timestamp = t
				}
				entry.Stream = stream
				entry.Message = msg
				if worker != "" {
					entry.WorkerID = id.ID(worker)
				}
				if dep != "" {
					entry.DeploymentID = id.ID(dep)
				}
			} else if len(parts) >= 3 {
				tsStr := trimBrackets(parts[0])
				stream := trimBrackets(parts[1])
				msg := strings.Join(parts[2:], "] ")

				if t, err := time.Parse(time.RFC3339Nano, tsStr); err == nil {
					entry.Timestamp = t
				} else if t, err := time.Parse(time.RFC3339, tsStr); err == nil {
					entry.Timestamp = t
				}
				entry.Stream = stream
				entry.Message = msg
			}
		}

		entries = append(entries, entry)
	}

	return entries
}

func trimBrackets(s string) string {
	s = bytes.NewBufferString(s).String()
	if len(s) >= 2 && s[0] == '[' && s[len(s)-1] == ']' {
		return s[1 : len(s)-1]
	}
	return s
}

// Subscribe streams live log lines for a task.
func (wl *WorkloadLogger) Subscribe(taskID id.ID) (chan LogEntry, func()) {
	ch := make(chan LogEntry, 200)

	wl.subMu.Lock()
	if taskID == "" {
		wl.allSubs = append(wl.allSubs, ch)
	} else {
		wl.subs[taskID] = append(wl.subs[taskID], ch)
	}
	wl.subMu.Unlock()

	cancel := func() {
		wl.subMu.Lock()
		defer wl.subMu.Unlock()

		if taskID == "" {
			for i, c := range wl.allSubs {
				if c == ch {
					wl.allSubs = append(wl.allSubs[:i], wl.allSubs[i+1:]...)
					close(ch)
					break
				}
			}
		} else {
			chs := wl.subs[taskID]
			for i, c := range chs {
				if c == ch {
					wl.subs[taskID] = append(chs[:i], chs[i+1:]...)
					close(ch)
					break
				}
			}
		}
	}

	return ch, cancel
}

// SubscribeFilter streams live log lines matching filter criteria.
func (wl *WorkloadLogger) SubscribeFilter(ctx context.Context, filter LogFilter, taskIDs []id.ID) <-chan LogEntry {
	outCh := make(chan LogEntry, 100)

	taskSet := make(map[id.ID]bool)
	for _, tid := range taskIDs {
		taskSet[tid] = true
	}

	rawCh, cancel := wl.Subscribe("")

	go func() {
		defer cancel()
		defer close(outCh)

		for {
			select {
			case <-ctx.Done():
				return
			case entry, ok := <-rawCh:
				if !ok {
					return
				}
				if len(taskSet) > 0 && !taskSet[entry.TaskID] {
					continue
				}
				if filter.TaskID != "" && entry.TaskID != filter.TaskID {
					continue
				}
				if filter.ServiceID != "" && entry.ServiceID != "" && entry.ServiceID != filter.ServiceID {
					continue
				}
				if filter.ServiceName != "" && entry.ServiceName != "" && entry.ServiceName != filter.ServiceName {
					continue
				}
				if filter.DeploymentID != "" && entry.DeploymentID != "" && entry.DeploymentID != filter.DeploymentID {
					continue
				}
				if filter.JobID != "" && entry.JobID != "" && entry.JobID != filter.JobID {
					continue
				}
				if filter.JobName != "" && entry.JobName != "" && entry.JobName != filter.JobName {
					continue
				}
				if filter.WorkerID != "" && entry.WorkerID != "" && entry.WorkerID != filter.WorkerID {
					continue
				}

				select {
				case outCh <- entry:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return outCh
}

func splitLines(p []byte) []string {
	var lines []string
	scanner := bufio.NewScanner(bytes.NewReader(p))
	for scanner.Scan() {
		text := scanner.Text()
		if text != "" {
			lines = append(lines, text)
		}
	}
	return lines
}
