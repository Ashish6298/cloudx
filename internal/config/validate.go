package config

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

var validLogLevels = map[string]bool{
	"debug": true,
	"info":  true,
	"warn":  true,
	"error": true,
}

var validRuntimeTypes = map[string]bool{
	"native": true,
	"docker": true,
}

// Validate performs strict deterministic semantic validation on Config.
func (c *Config) Validate() error {
	var errs ValidationErrors

	// Node
	if strings.TrimSpace(c.Node.ID) == "" {
		errs = append(errs, ValidationError{Field: "node.id", Message: "node id must not be empty"})
	}
	if strings.TrimSpace(c.Node.Name) == "" {
		errs = append(errs, ValidationError{Field: "node.name", Message: "node name must not be empty"})
	}

	// Control Plane Address
	if strings.TrimSpace(c.ControlPlane.Address) == "" {
		errs = append(errs, ValidationError{Field: "control_plane.address", Message: "control plane address must not be empty"})
	} else if err := validateHostPort(c.ControlPlane.Address); err != nil {
		errs = append(errs, ValidationError{Field: "control_plane.address", Message: fmt.Sprintf("invalid address format (%s)", err.Error())})
	}

	// Worker Address
	if strings.TrimSpace(c.Worker.Address) == "" {
		errs = append(errs, ValidationError{Field: "worker.address", Message: "worker address must not be empty"})
	} else if err := validateHostPort(c.Worker.Address); err != nil {
		errs = append(errs, ValidationError{Field: "worker.address", Message: fmt.Sprintf("invalid address format (%s)", err.Error())})
	}

	// Runtime
	if !validRuntimeTypes[strings.ToLower(c.Runtime.Type)] {
		errs = append(errs, ValidationError{
			Field:   "runtime.type",
			Message: fmt.Sprintf("invalid runtime type '%s', must be 'native' or 'docker'", c.Runtime.Type),
		})
	}

	// Storage Path
	if strings.TrimSpace(c.Storage.Path) == "" {
		errs = append(errs, ValidationError{Field: "storage.path", Message: "storage path must not be empty"})
	}

	// Health Interval
	if c.Health.HeartbeatInterval <= 0 {
		errs = append(errs, ValidationError{
			Field:   "health.heartbeat_interval",
			Message: "heartbeat interval must be a positive duration (e.g. 5s, 10s)",
		})
	} else if c.Health.HeartbeatInterval < 100*time.Millisecond {
		errs = append(errs, ValidationError{
			Field:   "health.heartbeat_interval",
			Message: "heartbeat interval cannot be less than 100ms",
		})
	}

	// Logging Level
	if !validLogLevels[strings.ToLower(c.Logging.Level)] {
		errs = append(errs, ValidationError{
			Field:   "logging.level",
			Message: fmt.Sprintf("invalid log level '%s', must be one of: debug, info, warn, error", c.Logging.Level),
		})
	}

	if errs.HasErrors() {
		return errs
	}
	return nil
}

func validateHostPort(addr string) error {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("must be formatted as host:port, got '%s'", addr)
	}
	if host == "" {
		return fmt.Errorf("host cannot be empty")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("port must be an integer between 1 and 65535, got '%s'", portStr)
	}
	return nil
}
