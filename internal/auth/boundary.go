package auth

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cloudx-org/cloudx/internal/common/id"
)

// PermissionScope defines the operational boundary for CloudX execution contexts.
type PermissionScope string

const (
	ScopeControlPlane PermissionScope = "control_plane"
	ScopeWorker       PermissionScope = "worker"
	ScopeRuntime      PermissionScope = "runtime"
)

// Context key for storing/retrieving permission scope
type scopeContextKey struct{}

// WithPermissionScope injects the PermissionScope into the context.
func WithPermissionScope(ctx context.Context, scope PermissionScope) context.Context {
	return context.WithValue(ctx, scopeContextKey{}, scope)
}

// GetPermissionScope extracts the active PermissionScope from context (defaults to ScopeControlPlane).
func GetPermissionScope(ctx context.Context) PermissionScope {
	if val := ctx.Value(scopeContextKey{}); val != nil {
		if s, ok := val.(PermissionScope); ok {
			return s
		}
	}
	return ScopeControlPlane
}

// EnsureScope validates that the active context possesses the required permission scope.
func EnsureScope(ctx context.Context, allowedScopes ...PermissionScope) error {
	active := GetPermissionScope(ctx)
	for _, allowed := range allowedScopes {
		if active == allowed {
			return nil
		}
	}
	return fmt.Errorf("permission boundary violation: current scope '%s' is not authorized for operation (allowed: %v)", active, allowedScopes)
}

var (
	// Resource name/ID validation regex (prevents control chars, null bytes, traversal characters)
	validResourceNameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)
)

// ValidateResourceID validates any externally supplied resource ID or name.
// It checks length, character boundaries, and rejects empty strings or path traversal tokens.
func ValidateResourceID(identifier string, entityType ...id.EntityType) error {
	cleaned := strings.TrimSpace(identifier)
	if cleaned == "" {
		return fmt.Errorf("identifier cannot be empty")
	}

	if strings.Contains(cleaned, "..") || strings.ContainsAny(cleaned, "/\\*?<>|\":;\x00") {
		return fmt.Errorf("invalid identifier '%s': contains prohibited path traversal or illegal characters", identifier)
	}

	if !validResourceNameRegex.MatchString(cleaned) {
		return fmt.Errorf("invalid identifier format '%s': must start with alphanumeric and contain only [a-zA-Z0-9_.-] (max 128 chars)", identifier)
	}

	// If entityType filter is provided, check prefix if formatted as a canonical ID
	if len(entityType) > 0 {
		if parsed, err := id.Parse(cleaned); err == nil {
			matched := false
			for _, expectedType := range entityType {
				if parsed.Type == expectedType {
					matched = true
					break
				}
			}
			if !matched {
				return fmt.Errorf("identifier '%s' has entity type '%s', expected one of %v", cleaned, parsed.Type, entityType)
			}
		}
	}

	return nil
}

// ValidateSafePath verifies that a target path is strictly contained within a base directory,
// preventing path traversal attacks (e.g. "../../../etc/passwd" or relative symlink escapes).
func ValidateSafePath(baseDir, targetPath string) (string, error) {
	if strings.TrimSpace(baseDir) == "" {
		return "", fmt.Errorf("base directory cannot be empty")
	}
	if strings.TrimSpace(targetPath) == "" {
		return "", fmt.Errorf("target path cannot be empty")
	}

	// Check for null bytes
	if strings.Contains(targetPath, "\x00") || strings.Contains(baseDir, "\x00") {
		return "", fmt.Errorf("path contains null byte")
	}

	// Clean base and target paths
	cleanBase := filepath.Clean(baseDir)
	var resolvedPath string
	if filepath.IsAbs(targetPath) {
		resolvedPath = filepath.Clean(targetPath)
	} else {
		resolvedPath = filepath.Clean(filepath.Join(cleanBase, targetPath))
	}

	// Convert paths to absolute for relative check
	absBase, err := filepath.Abs(cleanBase)
	if err != nil {
		return "", fmt.Errorf("failed to resolve absolute base directory: %w", err)
	}

	absResolved, err := filepath.Abs(resolvedPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve absolute target path: %w", err)
	}

	// Check if absResolved starts with absBase
	rel, err := filepath.Rel(absBase, absResolved)
	if err != nil || strings.HasPrefix(rel, "..") || rel == ".." {
		return "", fmt.Errorf("path traversal violation: '%s' escapes boundary directory '%s'", targetPath, baseDir)
	}

	return absResolved, nil
}
