package auth_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudx-org/cloudx/internal/auth"
	"github.com/cloudx-org/cloudx/internal/common/id"
)

func TestPermissionScope_Boundaries(t *testing.T) {
	// 1. Default context is ScopeControlPlane
	defaultCtx := context.Background()
	if err := auth.EnsureScope(defaultCtx, auth.ScopeControlPlane); err != nil {
		t.Fatalf("expected default context to have ScopeControlPlane: %v", err)
	}
	if err := auth.EnsureScope(defaultCtx, auth.ScopeWorker); err == nil {
		t.Errorf("expected ScopeWorker check to fail on default context")
	}

	// 2. Explicit Worker Scope
	workerCtx := auth.WithPermissionScope(context.Background(), auth.ScopeWorker)
	if err := auth.EnsureScope(workerCtx, auth.ScopeWorker); err != nil {
		t.Fatalf("expected worker scope to succeed: %v", err)
	}
	if err := auth.EnsureScope(workerCtx, auth.ScopeControlPlane); err == nil {
		t.Errorf("expected control plane scope check to fail on worker context")
	}

	// 3. Multi-scope allowed check
	if err := auth.EnsureScope(workerCtx, auth.ScopeControlPlane, auth.ScopeWorker); err != nil {
		t.Errorf("expected multi-scope match to succeed: %v", err)
	}
}

func TestValidateResourceID_ValidAndInvalid(t *testing.T) {
	validIDs := []string{
		"my-service",
		"payment_api_v1",
		"web-app-01.service",
		string(id.NewServiceID()),
		string(id.NewWorkerID()),
		string(id.NewTaskID()),
	}

	for _, vid := range validIDs {
		if err := auth.ValidateResourceID(vid); err != nil {
			t.Errorf("expected valid ID '%s' to pass validation, got: %v", vid, err)
		}
	}

	invalidIDs := []struct {
		id  string
		msg string
	}{
		{"", "empty ID"},
		{"../etc/passwd", "path traversal"},
		{"service/with/slashes", "slashes prohibited"},
		{"svc\\with\\backslashes", "backslashes prohibited"},
		{"bad*character", "wildcard prohibited"},
		{"null\x00byte", "null byte prohibited"},
		{"-starts-with-hyphen", "must start with alphanumeric"},
		{"_starts_with_underscore", "must start with alphanumeric"},
	}

	for _, tc := range invalidIDs {
		if err := auth.ValidateResourceID(tc.id); err == nil {
			t.Errorf("expected invalid ID '%s' (%s) to fail validation, but it passed", tc.id, tc.msg)
		}
	}
}

func TestValidateSafePath_PathTraversalPrevention(t *testing.T) {
	baseDir := filepath.Join(t.TempDir(), "storage_root")
	_ = os.MkdirAll(baseDir, 0755)

	// Valid subpaths
	validPaths := []string{
		"volumes/vol-1",
		"logs/tasks/tsk-1.log",
		filepath.Join(baseDir, "volumes/data"),
	}

	for _, p := range validPaths {
		resolved, err := auth.ValidateSafePath(baseDir, p)
		if err != nil {
			t.Errorf("expected safe path for '%s', got error: %v", p, err)
		}
		if !filepath.IsAbs(resolved) {
			t.Errorf("expected absolute resolved path, got: %s", resolved)
		}
	}

	// Path traversal attempts
	traversalAttempts := []string{
		"../escaped_dir",
		"../../etc/passwd",
		"../../../Windows/System32",
		"volumes/../../secret",
		filepath.Join(baseDir, "../outside"),
	}

	for _, p := range traversalAttempts {
		_, err := auth.ValidateSafePath(baseDir, p)
		if err == nil {
			t.Errorf("expected path traversal violation for '%s', but got no error", p)
		}
	}
}
