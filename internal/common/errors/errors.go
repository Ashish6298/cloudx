package errors

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrorCode represents an internal typed error classification.
type ErrorCode string

const (
	CodeInvalidConfig       ErrorCode = "INVALID_CONFIGURATION"
	CodeResourceUnavailable ErrorCode = "RESOURCE_UNAVAILABLE"
	CodeNotFound            ErrorCode = "NOT_FOUND"
	CodeConflict            ErrorCode = "CONFLICT"
	CodeInvalidState        ErrorCode = "INVALID_STATE"
	CodeRuntimeFailure      ErrorCode = "RUNTIME_FAILURE"
	CodeRPCFailure          ErrorCode = "RPC_FAILURE"
	CodeStorageFailure      ErrorCode = "STORAGE_FAILURE"
	CodeSchedulingFailure   ErrorCode = "SCHEDULING_FAILURE"
	CodeInternal            ErrorCode = "INTERNAL"
)

// Error represents a structured, classified CloudX error preserving root causes.
type Error struct {
	Code    ErrorCode      `json:"code"`
	Message string         `json:"message"`
	Op      string         `json:"op,omitempty"`
	Fields  map[string]any `json:"fields,omitempty"`
	Err     error          `json:"-"` // underlying wrapped error
}

// Error formats the error message.
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Err != nil {
		if e.Op != "" {
			return fmt.Sprintf("[%s] %s (%s): %v", e.Code, e.Message, e.Op, e.Err)
		}
		return fmt.Sprintf("[%s] %s: %v", e.Code, e.Message, e.Err)
	}
	if e.Op != "" {
		return fmt.Sprintf("[%s] %s (%s)", e.Code, e.Message, e.Op)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

// Unwrap returns the underlying wrapped error.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// MarshalJSON provides custom serialization preserving underlying error string.
func (e *Error) MarshalJSON() ([]byte, error) {
	type Alias Error
	aux := struct {
		*Alias
		RootCause string `json:"root_cause,omitempty"`
	}{
		Alias: (*Alias)(e),
	}
	if e.Err != nil {
		aux.RootCause = e.Err.Error()
	}
	return json.Marshal(aux)
}

// New creates a new classified Error.
func New(code ErrorCode, message string) *Error {
	return &Error{
		Code:    code,
		Message: message,
		Fields:  make(map[string]any),
	}
}

// Wrap wraps an existing error with a classified Code and context message.
func Wrap(code ErrorCode, message string, err error) *Error {
	return &Error{
		Code:    code,
		Message: message,
		Err:     err,
		Fields:  make(map[string]any),
	}
}

// WithOp attaches an operation name to the error.
func (e *Error) WithOp(op string) *Error {
	e.Op = op
	return e
}

// WithField attaches a structured metadata field to the error.
func (e *Error) WithField(key string, value any) *Error {
	if e.Fields == nil {
		e.Fields = make(map[string]any)
	}
	e.Fields[key] = value
	return e
}

// WithFields attaches multiple fields to the error.
func (e *Error) WithFields(fields map[string]any) *Error {
	if e.Fields == nil {
		e.Fields = make(map[string]any)
	}
	for k, v := range fields {
		e.Fields[k] = v
	}
	return e
}

// Helper constructors for standard error types
func NewInvalidConfig(msg string, err error) *Error {
	return Wrap(CodeInvalidConfig, msg, err)
}

func NewResourceUnavailable(msg string, err error) *Error {
	return Wrap(CodeResourceUnavailable, msg, err)
}

func NewNotFound(msg string, err error) *Error {
	return Wrap(CodeNotFound, msg, err)
}

func NewConflict(msg string, err error) *Error {
	return Wrap(CodeConflict, msg, err)
}

func NewInvalidState(msg string, err error) *Error {
	return Wrap(CodeInvalidState, msg, err)
}

func NewRuntimeFailure(msg string, err error) *Error {
	return Wrap(CodeRuntimeFailure, msg, err)
}

func NewRPCFailure(msg string, err error) *Error {
	return Wrap(CodeRPCFailure, msg, err)
}

func NewStorageFailure(msg string, err error) *Error {
	return Wrap(CodeStorageFailure, msg, err)
}

func NewSchedulingFailure(msg string, err error) *Error {
	return Wrap(CodeSchedulingFailure, msg, err)
}

// IsCode checks whether an error (or any error in its unwrap chain) matches a specific ErrorCode.
func IsCode(err error, code ErrorCode) bool {
	if err == nil {
		return false
	}
	var ce *Error
	if errors.As(err, &ce) {
		return ce.Code == code
	}
	return false
}

// GetCode extracts the ErrorCode from an error if available.
func GetCode(err error) ErrorCode {
	if err == nil {
		return ""
	}
	var ce *Error
	if errors.As(err, &ce) {
		return ce.Code
	}
	return CodeInternal
}
