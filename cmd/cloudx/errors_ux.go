package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"

	clxerrors "github.com/cloudx-org/cloudx/internal/common/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// DetailedError provides structured, user-friendly context, causes, and remediations.
type DetailedError struct {
	Title          string            `json:"title"`
	Endpoint       string            `json:"endpoint,omitempty"`
	PossibleCauses []string          `json:"possible_causes,omitempty"`
	Remediations   []string          `json:"remediations,omitempty"`
	TechnicalError string            `json:"technical_error,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

// FormatError analyzes an error, extracts root cause semantics, and formats
// a clear, actionable developer message. If verbose is enabled, technical
// stack traces or raw errors are appended.
func FormatError(err error, endpoint string, verbose bool) string {
	if err == nil {
		return ""
	}

	det := AnalyzeError(err, endpoint)
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("%s\n", det.Title))

	if det.Endpoint != "" {
		sb.WriteString(fmt.Sprintf("\nEndpoint:\n%s\n", det.Endpoint))
	}

	if len(det.Metadata) > 0 {
		sb.WriteString("\nContext:\n")
		for k, v := range det.Metadata {
			sb.WriteString(fmt.Sprintf("- %s: %s\n", k, v))
		}
	}

	if len(det.PossibleCauses) > 0 {
		sb.WriteString("\nPossible causes:\n")
		for _, cause := range det.PossibleCauses {
			sb.WriteString(fmt.Sprintf("- %s\n", cause))
		}
	}

	if len(det.Remediations) > 0 {
		sb.WriteString("\nSuggested actions:\n")
		for _, rem := range det.Remediations {
			sb.WriteString(fmt.Sprintf("- %s\n", rem))
		}
	}

	if verbose && det.TechnicalError != "" {
		sb.WriteString(fmt.Sprintf("\nTechnical details (--verbose):\n%s\n", det.TechnicalError))
	} else if !verbose && det.TechnicalError != "" {
		sb.WriteString("\n(Provide technical details under: --verbose)\n")
	}

	return strings.TrimSpace(sb.String())
}

// AnalyzeError inspects error types and error strings to categorize failures.
func AnalyzeError(err error, endpoint string) DetailedError {
	errStr := err.Error()
	techErr := errStr

	// 1. gRPC Status Errors
	if s, ok := status.FromError(err); ok {
		switch s.Code() {
		case codes.Unavailable:
			ep := endpoint
			if ep == "" {
				ep = extractEndpoint(errStr)
			}
			if ep == "" {
				ep = "127.0.0.1:7000"
			}
			return DetailedError{
				Title:    "CloudX control plane is unreachable.",
				Endpoint: ep,
				PossibleCauses: []string{
					"Control plane is stopped.",
					"Incorrect endpoint address.",
					"Network connection unavailable or blocked by firewall.",
				},
				Remediations: []string{
					"Start the control plane with: 'cloudx server'",
					"Check the configured address with: 'cloudx config show'",
					"Pass an explicit endpoint with: '--control-plane-addr <host:port>'",
				},
				TechnicalError: techErr,
			}
		case codes.NotFound:
			return DetailedError{
				Title: "Requested resource was not found.",
				PossibleCauses: []string{
					"The specified service, job, node, or volume name does not exist in cluster state.",
					"The resource was deleted or evicted.",
				},
				Remediations: []string{
					"List existing services with: 'cloudx service list'",
					"List existing jobs with: 'cloudx job list'",
					"Inspect cluster events with: 'cloudx events'",
				},
				TechnicalError: techErr,
			}
		case codes.Unauthenticated, codes.PermissionDenied:
			return DetailedError{
				Title: "Authentication or authorization failure.",
				PossibleCauses: []string{
					"Invalid or expired cluster bootstrap join token.",
					"Mismatched Cluster ID between node and control plane.",
				},
				Remediations: []string{
					"Inspect or generate a new bootstrap token with: 'cloudx cluster token create'",
					"Verify cluster token in cloudx.yaml or pass via CLOUDX_BOOTSTRAP_TOKEN",
				},
				TechnicalError: techErr,
			}
		case codes.ResourceExhausted:
			return DetailedError{
				Title: "Cluster resources exhausted.",
				PossibleCauses: []string{
					"No worker nodes have sufficient CPU or memory available.",
					"All matching workers are drained or unhealthy.",
				},
				Remediations: []string{
					"Inspect node capacity with: 'cloudx node list' or 'cloudx status'",
					"Run diagnostics with: 'cloudx diagnose'",
				},
				TechnicalError: techErr,
			}
		}
	}

	// 2. Net Op Errors / Connection Refused
	var netOpErr *net.OpError
	if errors.As(err, &netOpErr) || strings.Contains(errStr, "connection refused") || strings.Contains(errStr, "connectex") || strings.Contains(errStr, "No connection could be made") {
		ep := endpoint
		if ep == "" && netOpErr != nil && netOpErr.Addr != nil {
			ep = netOpErr.Addr.String()
		}
		if ep == "" {
			ep = extractEndpoint(errStr)
		}
		if ep == "" {
			ep = "127.0.0.1:7000"
		}
		return DetailedError{
			Title:    "CloudX control plane is unreachable.",
			Endpoint: ep,
			PossibleCauses: []string{
				"Control plane is stopped.",
				"Incorrect endpoint address.",
				"Network unavailable or port is occupied.",
			},
			Remediations: []string{
				"Start the control plane with: 'cloudx server'",
				"Verify control plane status with: 'cloudx status' or 'cloudx diagnose'",
				"Check configuration with: 'cloudx config show'",
			},
			TechnicalError: techErr,
		}
	}

	// 3. Context Timeout / Deadline Exceeded
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(errStr, "context deadline exceeded") {
		return DetailedError{
			Title:    "Operation timed out.",
			Endpoint: endpoint,
			PossibleCauses: []string{
				"Target node or control plane is under heavy load and slow to respond.",
				"Network latency or dropped packets.",
				"A long-running task or probe did not complete in the allocated timeout window.",
			},
			Remediations: []string{
				"Run 'cloudx diagnose' to check for resource pressure or degraded workers.",
				"Retry the command with a longer timeout if available.",
			},
			TechnicalError: techErr,
		}
	}

	// 4. File / Directory Not Found
	if errors.Is(err, os.ErrNotExist) || strings.Contains(errStr, "The system cannot find the file specified") || strings.Contains(errStr, "no such file or directory") {
		return DetailedError{
			Title: "Specified file or directory not found.",
			PossibleCauses: []string{
				"The manifest path or configuration file path is incorrect.",
				"The cluster storage path does not exist.",
			},
			Remediations: []string{
				"Verify the file path provided to '--file' / '-f' or '--config' / '-c'.",
				"Initialize local cluster storage with: 'cloudx init'",
			},
			TechnicalError: techErr,
		}
	}

	// 5. CloudX Structured Typed Errors
	if clxerrors.IsCode(err, clxerrors.CodeInvalidConfig) {
		return DetailedError{
			Title: "Configuration is invalid.",
			PossibleCauses: []string{
				"Syntax error in YAML configuration or manifest file.",
				"Missing required configuration fields or invalid values.",
			},
			Remediations: []string{
				"Validate configuration with: 'cloudx config validate'",
				"Inspect active configuration with: 'cloudx config show'",
			},
			TechnicalError: techErr,
		}
	}

	if clxerrors.IsCode(err, clxerrors.CodeResourceUnavailable) || clxerrors.IsCode(err, clxerrors.CodeSchedulingFailure) || strings.Contains(errStr, "no workers registered") || strings.Contains(errStr, "no worker available") {
		return DetailedError{
			Title: "Workload placement failed: no suitable worker available.",
			PossibleCauses: []string{
				"No compute workers are currently joined and in READY status.",
				"Workers lack sufficient CPU or Memory resources to satisfy workload requirements.",
			},
			Remediations: []string{
				"Join a worker using: 'cloudx worker start' or 'cloudx worker join'",
				"Inspect worker statuses with: 'cloudx worker status' or 'cloudx node list'",
				"Run 'cloudx diagnose' to evaluate cluster readiness.",
			},
			TechnicalError: techErr,
		}
	}

	if clxerrors.IsCode(err, clxerrors.CodeStorageFailure) || strings.Contains(errStr, "database is locked") || strings.Contains(errStr, "failed to open cluster database") {
		return DetailedError{
			Title: "Cluster state database error.",
			PossibleCauses: []string{
				"SQLite database file is corrupted or locked by another active process.",
				"Storage path lacks read/write permissions.",
			},
			Remediations: []string{
				"Run database health diagnostics with: 'cloudx diagnose'",
				"Ensure storage directory permissions are accessible.",
			},
			TechnicalError: techErr,
		}
	}

	// 6. Generic Fallback
	return DetailedError{
		Title:          "An error occurred executing the command.",
		Endpoint:       endpoint,
		TechnicalError: techErr,
		Remediations: []string{
			"Run 'cloudx diagnose' to check cluster health and configuration.",
			"Run command with '--verbose' for raw technical diagnostic details.",
		},
	}
}

// PrintError formats and writes the error to the provided writer (typically stderr).
func PrintError(w io.Writer, err error, endpoint string, verbose bool) {
	if err == nil {
		return
	}
	msg := FormatError(err, endpoint, verbose)
	fmt.Fprintln(w, msg)
}

// extractEndpoint tries to parse an IP:PORT or hostname:PORT substring from an error string.
func extractEndpoint(errStr string) string {
	parts := strings.Fields(errStr)
	for _, p := range parts {
		clean := strings.Trim(p, `"'(),:`)
		if strings.Count(clean, ":") == 1 {
			host, port, err := net.SplitHostPort(clean)
			if err == nil && host != "" && port != "" {
				return clean
			}
		}
	}
	return ""
}
