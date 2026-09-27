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
│   ├── metrics/          # Metrics model: counters, gauges, histograms (Phase 58)
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
  - [x] **Phase 4**: Identity and Identifier System *([PHASE_4_COMPLETION_REPORT.md](./docs/reports/PHASE_4_COMPLETION_REPORT.md))*
- [x] **Milestone 2: State Engine** (Phases 5–8)
  - [x] **Phase 5**: SQLite State Store *([PHASE_5_COMPLETION_REPORT.md](./docs/reports/PHASE_5_COMPLETION_REPORT.md))*
  - [x] **Phase 6**: Desired State Model *([PHASE_6_COMPLETION_REPORT.md](./docs/reports/PHASE_6_COMPLETION_REPORT.md))*
  - [x] **Phase 7**: Actual State Model *([PHASE_7_COMPLETION_REPORT.md](./docs/reports/PHASE_7_COMPLETION_REPORT.md))*
  - [x] **Phase 8**: State Transitions *([PHASE_8_COMPLETION_REPORT.md](./docs/reports/PHASE_8_COMPLETION_REPORT.md))*
- [x] **Milestone 3: Control Plane** (Phases 9–11)
  - [x] **Phase 9**: Control Plane Core *([PHASE_9_COMPLETION_REPORT.md](./docs/reports/PHASE_9_COMPLETION_REPORT.md))*
  - [x] **Phase 10**: Protobuf Definitions *([PHASE_10_COMPLETION_REPORT.md](./docs/reports/PHASE_10_COMPLETION_REPORT.md))*
  - [x] **Phase 11**: gRPC Control Plane API *([PHASE_11_COMPLETION_REPORT.md](./docs/reports/PHASE_11_COMPLETION_REPORT.md))*
- [x] **Milestone 4: Worker Runtime** (Phases 12–16)
  - [x] **Phase 12**: Worker Daemon *([PHASE_12_COMPLETION_REPORT.md](./docs/reports/PHASE_12_COMPLETION_REPORT.md))*
  - [x] **Phase 13**: Runtime Interface *([PHASE_13_COMPLETION_REPORT.md](./docs/reports/PHASE_13_COMPLETION_REPORT.md))*
  - [x] **Phase 14**: Native Process Runtime *([PHASE_14_COMPLETION_REPORT.md](./docs/reports/PHASE_14_COMPLETION_REPORT.md))*
  - [x] **Phase 15**: Task Manager *([PHASE_15_COMPLETION_REPORT.md](./docs/reports/PHASE_15_COMPLETION_REPORT.md))*
  - [x] **Phase 16**: Resource Monitor *([PHASE_16_COMPLETION_REPORT.md](./docs/reports/PHASE_16_COMPLETION_REPORT.md))*
- [x] **Milestone 5: Worker Registration and Cluster** (Phases 17–19)
  - [x] **Phase 17**: Worker Registration *([PHASE_17_COMPLETION_REPORT.md](./docs/reports/PHASE_17_COMPLETION_REPORT.md))*
  - [x] **Phase 18**: Heartbeat and Failure Detection *([PHASE_18_COMPLETION_REPORT.md](./docs/reports/PHASE_18_COMPLETION_REPORT.md))*
  - [x] **Phase 19**: Cluster Commands *([PHASE_19_COMPLETION_REPORT.md](./docs/reports/PHASE_19_COMPLETION_REPORT.md))*
- [x] **Milestone 6: Scheduler** (Phases 20–22)
  - [x] **Phase 20**: Scheduling Model *([PHASE_20_COMPLETION_REPORT.md](./docs/reports/PHASE_20_COMPLETION_REPORT.md))*
  - [x] **Phase 21**: Basic Scheduler *([PHASE_21_COMPLETION_REPORT.md](./docs/reports/PHASE_21_COMPLETION_REPORT.md))*
  - [x] **Phase 22**: Task Assignment *([PHASE_22_COMPLETION_REPORT.md](./docs/reports/PHASE_22_COMPLETION_REPORT.md))*
- [x] **Milestone 7: Services and Reconciliation** (Phases 23–27)
  - [x] **Phase 23**: Service Definition *([PHASE_23_COMPLETION_REPORT.md](./docs/reports/PHASE_23_COMPLETION_REPORT.md))*
  - [x] **Phase 24**: Service Deployment *([PHASE_24_COMPLETION_REPORT.md](./docs/reports/PHASE_24_COMPLETION_REPORT.md))*
  - [x] **Phase 25**: Reconciliation Engine *([PHASE_25_COMPLETION_REPORT.md](./docs/reports/PHASE_25_COMPLETION_REPORT.md))*
  - [x] **Phase 26**: Replica Scaling *([PHASE_26_COMPLETION_REPORT.md](./docs/reports/PHASE_26_COMPLETION_REPORT.md))*
  - [x] **Phase 27**: Restart Policies *([PHASE_27_COMPLETION_REPORT.md](./docs/reports/PHASE_27_COMPLETION_REPORT.md))*
- [x] **Milestone 8: Health and Failure Recovery** (Phases 28–31)
  - [x] **Phase 28**: Health Checks *([PHASE_28_COMPLETION_REPORT.md](./docs/reports/PHASE_28_COMPLETION_REPORT.md))*
  - [x] **Phase 29**: Automatic Failure Recovery *([PHASE_29_COMPLETION_REPORT.md](./docs/reports/PHASE_29_COMPLETION_REPORT.md))*
  - [x] **Phase 30**: Failure Simulation *([PHASE_30_COMPLETION_REPORT.md](./docs/reports/PHASE_30_COMPLETION_REPORT.md))*
  - [x] **Phase 31**: Reconciliation Reliability *([PHASE_31_COMPLETION_REPORT.md](./docs/reports/PHASE_31_COMPLETION_REPORT.md))*
- [x] **Milestone 9: Deployments and Rollbacks** (Phases 32–35)
  - [x] **Phase 32**: Deployment Model *([PHASE_32_COMPLETION_REPORT.md](./docs/reports/PHASE_32_COMPLETION_REPORT.md))*
  - [x] **Phase 33**: Versioned Deployment *([PHASE_33_COMPLETION_REPORT.md](./docs/reports/PHASE_33_COMPLETION_REPORT.md))*
  - [x] **Phase 34**: Rolling Deployment *([PHASE_34_COMPLETION_REPORT.md](./docs/reports/PHASE_34_COMPLETION_REPORT.md))*
  - [x] **Phase 35**: Rollback Strategy *([PHASE_35_COMPLETION_REPORT.md](./docs/reports/PHASE_35_COMPLETION_REPORT.md))*
- [x] **Milestone 10: Events, Logs and Auditability** (Phases 36–39)
  - [x] **Phase 36**: Event System *([PHASE_36_COMPLETION_REPORT.md](./docs/reports/PHASE_36_COMPLETION_REPORT.md))*
  - [x] **Phase 37**: Event CLI *([PHASE_37_COMPLETION_REPORT.md](./docs/reports/PHASE_37_COMPLETION_REPORT.md))*
  - [x] **Phase 38**: Service Logs *([PHASE_38_COMPLETION_REPORT.md](./docs/reports/PHASE_38_COMPLETION_REPORT.md))*
  - [x] **Phase 39**: Cluster Status *([PHASE_39_COMPLETION_REPORT.md](./docs/reports/PHASE_39_COMPLETION_REPORT.md))*
- [x] **Milestone 11: Job Execution** (Phases 40–42)
  - [x] **Phase 40**: Job Model *([PHASE_40_COMPLETION_REPORT.md](./docs/reports/PHASE_40_COMPLETION_REPORT.md))*
  - [x] **Phase 41**: Job Scheduler Integration *([PHASE_41_COMPLETION_REPORT.md](./docs/reports/PHASE_41_COMPLETION_REPORT.md))*
  - [x] **Phase 42**: Job Lifecycle and Retry *([PHASE_42_COMPLETION_REPORT.md](./docs/reports/PHASE_42_COMPLETION_REPORT.md))*
- [x] **Milestone 12: Persistent Volumes** (Phases 43–45 ✅ complete)
  - [x] **Phase 43**: Volume Model *([PHASE_43_COMPLETION_REPORT.md](./docs/reports/PHASE_43_COMPLETION_REPORT.md))*
  - [x] **Phase 44**: Volume Lifecycle *([PHASE_44_COMPLETION_REPORT.md](./docs/reports/PHASE_44_COMPLETION_REPORT.md))*
  - [x] **Phase 45**: Volume and Scheduling Constraints *([PHASE_45_COMPLETION_REPORT.md](./docs/reports/PHASE_45_COMPLETION_REPORT.md))*
- [x] **Milestone 13: Service Discovery and Networking** (Phases 46–49 ✅ complete)
  - [x] **Phase 46**: Service Registry *([PHASE_46_COMPLETION_REPORT.md](./docs/reports/PHASE_46_COMPLETION_REPORT.md))*
  - [x] **Phase 47**: Service Discovery API *([PHASE_47_COMPLETION_REPORT.md](./docs/reports/PHASE_47_COMPLETION_REPORT.md))*
  - [x] **Phase 48**: Port Mapping *([PHASE_48_COMPLETION_REPORT.md](./docs/reports/PHASE_48_COMPLETION_REPORT.md))*
  - [x] **Phase 49**: Logical CloudX Network *([PHASE_49_COMPLETION_REPORT.md](./docs/reports/PHASE_49_COMPLETION_REPORT.md))*
- [x] **Milestone 14: Multi-Node Private Cloud** (Phases 50–54 ✅ complete)
  - [x] **Phase 50**: Remote Worker Join *([PHASE_50_COMPLETION_REPORT.md](./docs/reports/PHASE_50_COMPLETION_REPORT.md))*
  - [x] **Phase 51**: Cluster Token and Authentication *([PHASE_51_COMPLETION_REPORT.md](./docs/reports/PHASE_51_COMPLETION_REPORT.md))*
  - [x] **Phase 52**: Multi-Node Scheduling *([PHASE_52_COMPLETION_REPORT.md](./docs/reports/PHASE_52_COMPLETION_REPORT.md))*
  - [x] **Phase 53**: Node Drain *([PHASE_53_COMPLETION_REPORT.md](./docs/reports/PHASE_53_COMPLETION_REPORT.md))*
  - [x] **Phase 54**: Cluster Recovery *([PHASE_54_COMPLETION_REPORT.md](./docs/reports/PHASE_54_COMPLETION_REPORT.md))*
- [x] **Milestone 15: Resource-Aware Orchestration** (Phases 55–57 ✅ complete)
  - [x] **Phase 56**: Improved Scheduling Score *([PHASE_56_COMPLETION_REPORT.md](./docs/reports/PHASE_56_COMPLETION_REPORT.md))*
  - [x] **Phase 57**: Scheduling Explanation *([PHASE_57_COMPLETION_REPORT.md](./docs/reports/PHASE_57_COMPLETION_REPORT.md))*
- [x] **Milestone 16: Observability** (Phases 58–60 ✅ complete)
  - [x] **Phase 58**: Metrics Model *([PHASE_58_COMPLETION_REPORT.md](./docs/reports/PHASE_58_COMPLETION_REPORT.md))*
  - [x] **Phase 59**: OpenTelemetry-Compatible Architecture *([PHASE_59_COMPLETION_REPORT.md](./docs/reports/PHASE_59_COMPLETION_REPORT.md))*
  - [x] **Phase 60**: Diagnostics *([PHASE_60_COMPLETION_REPORT.md](./docs/reports/PHASE_60_COMPLETION_REPORT.md))*
- [x] **Milestone 17: CLI Maturity** (Phases 61–63)
  - [x] **Phase 61**: CLI Command Structure *([PHASE_61_COMPLETION_REPORT.md](./docs/reports/PHASE_61_COMPLETION_REPORT.md))*
- [ ] **Milestones 18–88**: CLI machine output & ergonomics, DNS resolution, load balancing, security, ingress, multi-node mesh, and release audit.

---

## Developer CLI Command Matrix

The `cloudx` CLI provides an intuitive, consistent command surface across all core CloudX lifecycle primitives:

```bash
# Cluster & Workers
cloudx init                     # Quick cluster initialization
cloudx status                   # Quick cluster status & node summary
cloudx cluster init/status/nodes# Comprehensive cluster control plane operations
cloudx worker start/join/status # Worker lifecycle management

# Services & Deployments
cloudx deploy [manifest.yaml]   # Deploy service or batch manifest
cloudx rollback <service>       # Rollback service to previous revision
cloudx service list             # List registered services
cloudx service inspect <id>     # Detailed JSON/tabular inspection
cloudx service scale <id> <n>   # Dynamically scale replica count
cloudx service restart <id>     # Restart active service instances
cloudx service logs <id>        # View/stream task logs
cloudx service endpoints        # List exposed service ports and VIPs

# Jobs
cloudx job run [job.yaml]       # Run batch or one-off job
cloudx job list                 # List batch job executions
cloudx job inspect <id>         # Inspect job execution details
cloudx job logs <id>            # Fetch job execution output

# Nodes, Volumes & Networks
cloudx node list                # List registered nodes
cloudx node drain <id>          # Drain node and migrate tasks
cloudx volume create/list/inspect/delete  # Persistent storage management
cloudx network create/list      # Virtual overlay networking

# Observability & Diagnostics
cloudx events                   # Stream cluster lifecycle audit events
cloudx diagnose                 # Comprehensive diagnostic health checks
cloudx metrics show             # In-process metrics inspection
cloudx otel status/spans/metrics# OpenTelemetry tracing and metric bridging
```

---


## Observability & Telemetry

CloudX provides rich in-process metrics, OpenTelemetry-compatible tracing, and cluster diagnostics with **zero required external infrastructure**.

### 1. In-Process Metrics (`cloudx metrics show`)

Phase 58 introduces a zero-dependency in-process metrics model:

```bash
# Show all cluster metrics
./bin/cloudx metrics show

# Filter by domain
./bin/cloudx metrics show --domain worker
./bin/cloudx metrics show --domain service
./bin/cloudx metrics show --domain controlplane
./bin/cloudx metrics show --domain job

# Machine-readable JSON output
./bin/cloudx metrics show --json
```

| Domain | Metrics |
|--------|--------|
| **Control Plane** | reconciliation cycles/failures/duration, scheduling latency/decisions/failures, RPC failures, state CRUD counters |
| **Worker** | CPU usage %, memory used/avail/total bytes, active task count, process restarts, heartbeat counters |
| **Service** | replicas desired, replicas running, health failures, restart count |
| **Job** | execution duration histogram, succeeded/failed/cancelled/retry counters |

---

### 2. OpenTelemetry Tracing & Export (`cloudx otel`)

Phase 59 introduces OpenTelemetry-compatible tracing and metric bridging:

```bash
# Inspect OpenTelemetry provider status and configuration
./bin/cloudx otel status
./bin/cloudx otel status --json

# Inspect in-memory trace spans recorded by CloudX
./bin/cloudx otel spans
./bin/cloudx otel spans --limit 10
./bin/cloudx otel spans --json

# Inspect bridged OpenTelemetry ResourceMetrics
./bin/cloudx otel metrics
./bin/cloudx otel metrics --json
```

#### Standalone vs. Hybrid Mode
- **Standalone Mode (Default):** Spans and metrics are captured in-memory using an efficient FIFO ring buffer. No external server (Collector/Jaeger/Tempo) is required.
- **Hybrid / Export Mode:** Set `CLOUDX_OTEL_ENDPOINT=http://localhost:4318` (or configure `telemetry.otlp_endpoint` in `cloudx.yaml`) to automatically stream OTLP HTTP/JSON trace spans to any OpenTelemetry collector while preserving local standalone functionality.

---

### 3. Cluster Diagnostics (`cloudx diagnose`)

Phase 60 introduces a single diagnostic tool to identify common CloudX problems across 9 core vectors:

```bash
# Run comprehensive diagnostic checks across cluster
./bin/cloudx diagnose

# Aliases
./bin/cloudx doctor
./bin/cloudx diag

# Output machine-readable JSON report
./bin/cloudx diagnose --json
```

#### Diagnostic Vectors Evaluated

1. **Configuration:** Validates semantic structure, storage paths, and filesystem write permissions.
2. **Database Integrity:** Runs SQLite `PRAGMA integrity_check` and `PRAGMA foreign_key_check` on `cloudx.db`.
3. **Control Plane Health:** Tests TCP listener reachability and control plane daemon state.
4. **Worker Connectivity:** Inspects registered worker daemons and verifies TCP network reachability.
5. **Heartbeat Status:** Evaluates heartbeat timestamps and detects `SUSPECTED`, `UNHEALTHY`, or `LOST` workers.
6. **Scheduler Status:** Checks active schedulable workers in `READY` state.
7. **Orphaned Tasks:** Identifies stranded tasks on dead/lost workers and crashlooping workloads.
8. **Failed Deployments:** Detects degraded services and stalled/halted deployment revisions.
9. **Resource Pressure:** Evaluates host CPU utilization, memory pressure, and worker capacity exhaustion.











