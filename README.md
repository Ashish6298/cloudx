# CloudX — Local-First Private Cloud Runtime

[![Build & Test](https://img.shields.io/badge/status-phase%202%20complete-brightgreen)](#)
[![Go Version](https://img.shields.io/badge/go-1.22%2B-blue)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-green)](./LICENSE)

**CloudX** is a local-first private cloud runtime that allows developers to deploy, schedule, execute, monitor, recover, scale, and manage services and jobs across one or more machines.

---

## Architecture Overview

CloudX operates on a continuous **Desired-State Reconciliation Loop**:

```text
DESIRED STATE
      ↓
CONTROL PLANE
      ↓
SCHEDULER
      ↓
WORKER
      ↓
RUNTIME (Native OS Processes)
      ↓
ACTUAL STATE
      ↓
REPORT BACK
      ↓
RECONCILIATION
      ↓
DESIRED STATE
```

---

## Configuration Hierarchy

CloudX configuration is explicit and deterministic, resolved in the following strict order of precedence:

1. **Built-in Defaults** (e.g. `127.0.0.1:7000`, `native` runtime, `~/.cloudx`)
2. **Configuration File** (`cloudx.yaml` in current working dir, explicit `-c/--config` flag, or `~/.cloudx/cloudx.yaml`)
3. **Environment Variables** (`CLOUDX_NODE_ID`, `CLOUDX_CONTROL_PLANE_ADDRESS`, `CLOUDX_LOGGING_LEVEL`, etc.)
4. **CLI Flags** (`--node-id`, `--control-plane-addr`, `--log-level`, etc.)

---

## Repository Structure

```text
cloudx/
├── cmd/
│   ├── cloudx/           # Primary CLI entrypoint (Cobra)
│   └── cloudx-worker/    # Worker daemon entrypoint
├── internal/
│   ├── api/              # API interfaces and protocol contracts
│   ├── config/           # YAML, env & CLI configuration engine + validator
│   ├── controlplane/     # Central control plane logic & reconciler
│   ├── events/           # Audit trail and event engine
│   ├── health/           # Heartbeat and health check probes
│   ├── registry/         # Service discovery & endpoint registry
│   ├── runtime/          # Process runtime abstractions
│   ├── scheduler/        # Deterministic scoring & placement engine
│   ├── state/            # SQLite persistent repository layer
│   ├── worker/           # Worker daemon orchestration
│   └── common/           # Shared identifiers and build versioning
├── proto/                # Protocol Buffer RPC definitions
├── configs/              # Reference configurations (cloudx.yaml)
├── scripts/              # Automation and setup scripts
├── test/                 # Integration and E2E fixtures
├── docs/                 # Architecture, design & phase reports
├── go.mod                # Go module definition
├── go.sum                # Go checksums
├── Makefile              # Build automation
├── LICENSE               # MIT License
├── phase.txt             # 88-Phase Master Execution Roadmap
└── project.txt           # Master System Specification
```

---

## Getting Started

### Prerequisites
- Go 1.22 or newer

### Building Binaries
```bash
# Build both CLI and Worker binaries
go build -o bin/cloudx ./cmd/cloudx
go build -o bin/cloudx-worker ./cmd/cloudx-worker
```

### Running Tests
```bash
go test -v ./...
```

### CLI Usage & Configuration Inspection
```bash
# Version check
./bin/cloudx version
./bin/cloudx version --json

# View resolved configuration
./bin/cloudx config show
./bin/cloudx config show --json

# Validate configuration
./bin/cloudx config validate

# CLI overrides
./bin/cloudx --node-id custom-node --log-level debug config show
```

---

## Roadmap & Implementation Status

Implementation follows the [88-Phase Roadmap](./phase.txt):

- [x] **Milestone 1: Project Foundation**
  - [x] **Phase 1**: Repository & Go Project Initialization *([PHASE_1_COMPLETION_REPORT.md](./docs/reports/PHASE_1_COMPLETION_REPORT.md))*
  - [x] **Phase 2**: Configuration System *([PHASE_2_COMPLETION_REPORT.md](./docs/reports/PHASE_2_COMPLETION_REPORT.md))*
  - [x] **Phase 3**: Logging and Error Infrastructure *([PHASE_3_COMPLETION_REPORT.md](./docs/reports/PHASE_3_COMPLETION_REPORT.md))*
  - [ ] **Phase 4**: Identity and Identifier System
- [ ] **Milestone 2: State Engine** (Phases 5–8)
- [ ] **Milestone 3: Control Plane** (Phases 9–11)
- [ ] **Milestone 4: Worker Runtime** (Phases 12–15)
- [ ] **Milestones 5–88**: Full orchestration, scheduling, reconciliation, failover, rolling updates, volumes, and release audit.
