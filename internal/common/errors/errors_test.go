package errors

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestErrorCreationAndFormatting(t *testing.T) {
	err := New(CodeInvalidState, "cannot start task in stopped state").
		WithOp("TaskRunner.Start").
		WithField("task_id", "task-123")

	if err.Code != CodeInvalidState {
		t.Errorf("expected code %s, got %s", CodeInvalidState, err.Code)
	}

	formatted := err.Error()
	if !strings.Contains(formatted, "[INVALID_STATE]") || !strings.Contains(formatted, "TaskRunner.Start") {
		t.Errorf("expected formatted error to contain code and op, got: %s", formatted)
	}
}

func TestErrorWrappingAndRootCause(t *testing.T) {
	rootErr := fmt.Errorf("connection refused on port 7000")
	wrapped := Wrap(CodeRPCFailure, "failed to connect to control plane", rootErr).
		WithOp("WorkerClient.Connect").
		WithField("control_plane_addr", "127.0.0.1:7000")

	if !errors.Is(wrapped, rootErr) {
		t.Errorf("expected errors.Is to match root error")
	}

	if wrapped.Unwrap() != rootErr {
		t.Errorf("expected Unwrap to return rootErr")
	}

	if !strings.Contains(wrapped.Error(), "connection refused on port 7000") {
		t.Errorf("expected error message to preserve root cause, got: %s", wrapped.Error())
	}
}

func TestErrorClassification(t *testing.T) {
	tests := []struct {
		err  *Error
		code ErrorCode
	}{
		{NewInvalidConfig("bad yaml", nil), CodeInvalidConfig},
		{NewResourceUnavailable("out of memory", nil), CodeResourceUnavailable},
		{NewNotFound("service api not found", nil), CodeNotFound},
		{NewConflict("worker already registered", nil), CodeConflict},
		{NewInvalidState("task stopping", nil), CodeInvalidState},
		{NewRuntimeFailure("process exited with status 1", nil), CodeRuntimeFailure},
		{NewRPCFailure("gRPC timeout", nil), CodeRPCFailure},
		{NewStorageFailure("database locked", nil), CodeStorageFailure},
		{NewSchedulingFailure("no worker matches capacity", nil), CodeSchedulingFailure},
	}

	for _, tc := range tests {
		if !IsCode(tc.err, tc.code) {
			t.Errorf("expected IsCode to return true for code %s", tc.code)
		}
		if GetCode(tc.err) != tc.code {
			t.Errorf("expected GetCode to return %s, got %s", tc.code, GetCode(tc.err))
		}
	}
}

func TestErrorJSONSerialization(t *testing.T) {
	rootErr := fmt.Errorf("disk I/O error")
	appErr := Wrap(CodeStorageFailure, "cannot persist service state", rootErr).
		WithOp("ServiceRepo.Save").
		WithField("service_id", "srv-api-1")

	data, err := json.Marshal(appErr)
	if err != nil {
		t.Fatalf("failed to marshal Error to JSON: %v", err)
	}

	jsonStr := string(data)
	if !strings.Contains(jsonStr, `"code":"STORAGE_FAILURE"`) {
		t.Errorf("expected JSON to contain code, got: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"root_cause":"disk I/O error"`) {
		t.Errorf("expected JSON to contain root_cause, got: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"service_id":"srv-api-1"`) {
		t.Errorf("expected JSON to contain fields, got: %s", jsonStr)
	}
}
