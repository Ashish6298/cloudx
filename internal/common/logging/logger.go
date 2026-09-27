package logging

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cloudx-org/cloudx/internal/auth"
)

// RedactSecrets scans a text string and masks any detected credentials, tokens, or private keys.
func RedactSecrets(input string) string {
	return auth.RedactString(input)
}

// RedactField returns a redacted representation if the key implies secret/credential content.
func RedactField(key string, value any) any {
	return auth.RedactValue(key, value)
}

// Level defines logging severity.
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// ParseLevel parses a case-insensitive string into a Level.
func ParseLevel(lvl string) Level {
	switch strings.ToLower(strings.TrimSpace(lvl)) {
	case "debug":
		return LevelDebug
	case "info":
		return LevelInfo
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	default:
		return LevelInfo
	}
}

// Format defines output format (text vs json).
type Format string

const (
	FormatText Format = "text"
	FormatJSON Format = "json"
)

// Logger is the structured logger interface for CloudX.
type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)

	With(key string, value any) Logger
	WithFields(fields map[string]any) Logger

	// Strongly-typed CloudX subsystem identity helpers
	WithNode(id string) Logger
	WithService(id string) Logger
	WithDeployment(id string) Logger
	WithTask(id string) Logger
	WithWorker(id string) Logger
	WithJob(id string) Logger
	WithEvent(id string) Logger

	SetLevel(level Level)
	SetOutput(w io.Writer)
	SetFormat(format Format)
}

// LogEntry is the internal representation of a log event.
type LogEntry struct {
	Timestamp string         `json:"timestamp"`
	Level     string         `json:"level"`
	Message   string         `json:"message"`
	Fields    map[string]any `json:"fields,omitempty"`
}

type defaultLogger struct {
	mu     sync.Mutex
	out    io.Writer
	level  Level
	format Format
	fields map[string]any
}

// New creates a new Logger with standard defaults.
func New(lvl Level, format Format) Logger {
	return &defaultLogger{
		out:    os.Stdout,
		level:  lvl,
		format: format,
		fields: make(map[string]any),
	}
}

// NewDefaultLogger returns a text logger at INFO level.
func NewDefaultLogger() Logger {
	return New(LevelInfo, FormatText)
}

func (l *defaultLogger) clone() *defaultLogger {
	l.mu.Lock()
	defer l.mu.Unlock()

	newFields := make(map[string]any, len(l.fields))
	for k, v := range l.fields {
		newFields[k] = v
	}

	return &defaultLogger{
		out:    l.out,
		level:  l.level,
		format: l.format,
		fields: newFields,
	}
}

func (l *defaultLogger) With(key string, value any) Logger {
	cp := l.clone()
	cp.fields[key] = RedactField(key, value)
	return cp
}

func (l *defaultLogger) WithFields(fields map[string]any) Logger {
	cp := l.clone()
	for k, v := range fields {
		cp.fields[k] = RedactField(k, v)
	}
	return cp
}

func (l *defaultLogger) WithNode(id string) Logger {
	return l.With("node_id", id)
}

func (l *defaultLogger) WithService(id string) Logger {
	return l.With("service_id", id)
}

func (l *defaultLogger) WithDeployment(id string) Logger {
	return l.With("deployment_id", id)
}

func (l *defaultLogger) WithTask(id string) Logger {
	return l.With("task_id", id)
}

func (l *defaultLogger) WithWorker(id string) Logger {
	return l.With("worker_id", id)
}

func (l *defaultLogger) WithJob(id string) Logger {
	return l.With("job_id", id)
}

func (l *defaultLogger) WithEvent(id string) Logger {
	return l.With("event_id", id)
}

func (l *defaultLogger) SetLevel(level Level) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

func (l *defaultLogger) SetOutput(w io.Writer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.out = w
}

func (l *defaultLogger) SetFormat(format Format) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.format = format
}

func (l *defaultLogger) log(level Level, msg string, args ...any) {
	if level < l.level {
		return
	}

	formattedMsg := msg
	if len(args) > 0 {
		formattedMsg = fmt.Sprintf(msg, args...)
	}

	// Mask any sensitive tokens/credentials in the message
	formattedMsg = RedactSecrets(formattedMsg)

	entry := LogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Level:     level.String(),
		Message:   formattedMsg,
		Fields:    l.fields,
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.format == FormatJSON {
		_ = json.NewEncoder(l.out).Encode(entry)
		return
	}

	// Text format: [TIMESTAMP] [LEVEL] Message key=value key2=value2
	var fieldStr string
	if len(l.fields) > 0 {
		var pairs []string
		for k, v := range l.fields {
			pairs = append(pairs, fmt.Sprintf("%s=%v", k, v))
		}
		fieldStr = " " + strings.Join(pairs, " ")
	}

	fmt.Fprintf(l.out, "[%s] [%-5s] %s%s\n", entry.Timestamp[:19], entry.Level, entry.Message, fieldStr)
}

func (l *defaultLogger) Debug(msg string, args ...any) {
	l.log(LevelDebug, msg, args...)
}

func (l *defaultLogger) Info(msg string, args ...any) {
	l.log(LevelInfo, msg, args...)
}

func (l *defaultLogger) Warn(msg string, args ...any) {
	l.log(LevelWarn, msg, args...)
}

func (l *defaultLogger) Error(msg string, args ...any) {
	l.log(LevelError, msg, args...)
}
