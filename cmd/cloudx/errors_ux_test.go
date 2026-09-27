package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	clxerrors "github.com/cloudx-org/cloudx/internal/common/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestErrorUX_GRPCUnavailable(t *testing.T) {
	grpcErr := status.Error(codes.Unavailable, "connection refused")
	formatted := FormatError(grpcErr, "127.0.0.1:7000", false)

	// Check core Phase 63 requirements
	if !strings.Contains(formatted, "CloudX control plane is unreachable.") {
		t.Errorf("expected unreachable title, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "Endpoint:\n127.0.0.1:7000") {
		t.Errorf("expected endpoint 127.0.0.1:7000, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "Possible causes:") {
		t.Errorf("expected 'Possible causes:', got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "- Control plane is stopped.") {
		t.Errorf("expected 'Control plane is stopped.', got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "--verbose") {
		t.Errorf("expected '--verbose' mention, got:\n%s", formatted)
	}

	// Test verbose details
	verboseFormatted := FormatError(grpcErr, "127.0.0.1:7000", true)
	if !strings.Contains(verboseFormatted, "Technical details (--verbose):") {
		t.Errorf("expected technical details header with --verbose, got:\n%s", verboseFormatted)
	}
	if !strings.Contains(verboseFormatted, "connection refused") {
		t.Errorf("expected raw error string in verbose output, got:\n%s", verboseFormatted)
	}
}

func TestErrorUX_NetConnectionRefused(t *testing.T) {
	netErr := &net.OpError{
		Op:   "dial",
		Net:  "tcp",
		Addr: &net.TCPAddr{IP: net.ParseIP("192.168.1.50"), Port: 7000},
		Err:  errors.New("connectex: No connection could be made because the target machine actively refused it"),
	}

	formatted := FormatError(netErr, "", false)
	if !strings.Contains(formatted, "CloudX control plane is unreachable.") {
		t.Errorf("expected unreachable title, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "192.168.1.50:7000") {
		t.Errorf("expected endpoint extracted from net.OpError, got:\n%s", formatted)
	}
}

func TestErrorUX_ContextTimeout(t *testing.T) {
	timeoutErr := fmt.Errorf("operation failed: %w", context.DeadlineExceeded)
	formatted := FormatError(timeoutErr, "127.0.0.1:7000", false)

	if !strings.Contains(formatted, "Operation timed out.") {
		t.Errorf("expected timeout title, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "heavy load") {
		t.Errorf("expected heavy load in causes, got:\n%s", formatted)
	}
}

func TestErrorUX_NotFound(t *testing.T) {
	notFoundErr := status.Error(codes.NotFound, "service 'non-existent' not found")
	formatted := FormatError(notFoundErr, "", false)

	if !strings.Contains(formatted, "Requested resource was not found.") {
		t.Errorf("expected not found title, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "cloudx service list") {
		t.Errorf("expected service list in suggestions, got:\n%s", formatted)
	}
}

func TestErrorUX_ResourceUnavailable(t *testing.T) {
	schedErr := clxerrors.NewResourceUnavailable("no suitable worker node found", nil)
	formatted := FormatError(schedErr, "", false)

	if !strings.Contains(formatted, "Workload placement failed: no suitable worker available.") {
		t.Errorf("expected placement failure title, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "cloudx worker start") {
		t.Errorf("expected worker start in suggestions, got:\n%s", formatted)
	}
}

func TestErrorUX_InvalidConfig(t *testing.T) {
	cfgErr := clxerrors.NewInvalidConfig("yaml unmarshal error on line 12", nil)
	formatted := FormatError(cfgErr, "", false)

	if !strings.Contains(formatted, "Configuration is invalid.") {
		t.Errorf("expected config invalid title, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "cloudx config validate") {
		t.Errorf("expected config validate in suggestions, got:\n%s", formatted)
	}
}
