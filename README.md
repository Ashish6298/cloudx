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

---

### 4. CLI Command Interface & Maturity (Phase 61 & Phase 62)

CloudX features a professional developer interface with human-friendly terminal formatting and machine-readable JSON output for CI/CD, automation scripts, and tooling integrations.

#### Standard Command Surface

| Domain | Command | Description |
|--------|---------|-------------|
| **Core** | `cloudx init` / `cloudx status` | Initialize storage/database and inspect overall cluster health |
| **Cluster** | `cloudx cluster init` / `status` / `nodes` / `token` | Cluster bootstrap token management, health topology, member nodes |
| **Worker** | `cloudx worker start` / `join` / `status` | Start local worker daemon, join remote cluster, inspect worker list |
| **Deployment**| `cloudx deploy -f <manifest>` / `cloudx rollback <svc> [ver]` | Declarative service manifests and atomic zero-cost version rollbacks |
| **Service** | `cloudx service list` / `inspect` / `scale` / `restart` / `endpoints` / `logs` | Service lifecycle, live logs streaming, replica scaling, service discovery |
| **Job** | `cloudx job run` / `list` / `inspect` / `logs` / `cancel` / `retry` | Batch job execution, failure retries, execution logging, status inspection |
| **Node** | `cloudx node list` / `drain` | Node topology and zero-downtime worker task evacuation |
| **Volume** | `cloudx volume create` / `list` / `inspect` / `delete` | Local and host persistent storage mounts for stateful workloads |
| **Network** | `cloudx network create` / `list` / `inspect` / `delete` | Isolated virtual overlay networking and DNS service discovery |
| **Explain** | `cloudx task explain <task-id>` | Deterministic scheduler decision rationale and scoring breakdown |
| **Diagnostics**| `cloudx diagnose` (aliases: `doctor`, `diag`) | 9-vector operational health and sanity inspection |

#### Machine-Readable JSON Output (`--output json` / `-o json`)

All major commands support the standard `--output json` (or `-o json` / `--json`) flag for frictionless integration with CI/CD pipelines, jq filters, automation scripts, and future web dashboards:

```bash
# Core status & topology
cloudx status --output json
cloudx cluster nodes -o json
cloudx worker status -o json
cloudx node list --output json

# Workloads & Inspections
cloudx deploy -f service.yaml --output json
cloudx service list -o json
cloudx service inspect api --output json
cloudx service endpoints --output json
cloudx job list -o json
cloudx job inspect migration --output json

# Observability, Events & Diagnostics
cloudx events --service api --output json
cloudx diagnose --output json
cloudx metrics show --output json
cloudx otel status --output json
cloudx otel spans --output json
cloudx otel metrics --output json
```

---

### 5. Developer Error UX & Actionable Remediation (Phase 63)

CloudX avoids raw, confusing RPC errors in favor of clear, developer-centric explanations with contextual remediation and suggested commands.

#### Example: Unreachable Control Plane

**Bad (Raw technical error):**
```text
rpc error: code = Unavailable desc = connection refused
```

**CloudX (Standard Developer UX):**
```text
CloudX control plane is unreachable.

Endpoint:
127.0.0.1:7000

Possible causes:
- Control plane is stopped.
- Incorrect endpoint address.
- Network connection unavailable or blocked by firewall.

Suggested actions:
- Start the control plane with: 'cloudx server'
- Check the configured address with: 'cloudx config show'
- Pass an explicit endpoint with: '--control-plane-addr <host:port>'

(Provide technical details under: --verbose)
```

#### Detailed Technical Mode (`--verbose` / `-v`)

Developers debugging deep RPC connectivity or subsystem failures can pass `--verbose` / `-v` to inspect raw root-cause error chains without obscuring clarity:

```bash
cloudx status --verbose
cloudx deploy -f service.yaml -v
```

---

### 6. RPC Security, TLS / mTLS & Secret Protection (Phase 64)

CloudX implements robust transport layer security, cryptographic identity verification, and secret log redaction across the gRPC communication fabric.

#### Transport Layer Security (TLS & mTLS)
- **Zero-Config Self-Signed Certificates**: CloudX can generate in-memory or on-disk X.509 RSA certificates for encrypted development environments with zero external dependencies.
- **Mutual TLS (mTLS)**: Enforces bidirectional cryptographic identity verification between worker daemons and the control plane.
- **Configuration**:
  ```yaml
  tls:
    enabled: true
    cert_file: "/etc/cloudx/tls/server.crt"
    key_file: "/etc/cloudx/tls/server.key"
    ca_file: "/etc/cloudx/tls/ca.crt"
    client_auth: true # Enforce mTLS client certificate verification
  ```

#### Secret & Token Log Redaction
CloudX automatically detects and redacts secrets before writing logs to stdout or persistent storage:
- Bootstrap tokens (`clx-btk-*` $\rightarrow$ `clx-btk-[REDACTED]`)
- Authorization headers (`Bearer [REDACTED]`)
- PEM private keys (`-----BEGIN ... PRIVATE KEY-----` $\rightarrow$ `[REDACTED_PRIVATE_KEY]`)
- Structured sensitive fields (`password`, `token`, `secret`, `private_key`)

---

### 7. Universal Secret Redaction & Protection (Phase 65)

CloudX implements universal, multi-layer secret protection across the entire system to prevent accidental credential leakage in logs, persistent events, CLI output, and error messages.

#### Redaction Domains
- **Application Logs (`internal/common/logging`)**: All text messages and structured log fields undergo automatic credential sanitization.
- **Cluster Events (`internal/events`)**: Event payloads with sensitive keys, database URLs, and API tokens are sanitized before SQLite persistence and during `cloudx events` CLI display.
- **Resource Inspection (`cloudx service inspect`, `cloudx job inspect`)**: Sensitive command-line flags (`--password`, `--token`, `--api-key`) and environment variables (`DATABASE_URL`, `DB_PASSWORD`, `API_KEY`, etc.) are masked as `[REDACTED]` in terminal outputs.
- **Error Messages (`errors_ux`)**: Formatted developer error messages automatically mask credentials, URLs, and private keys embedded in exception strings or technical stack traces.

---

### 8. Permission Boundaries & Input Validation (Phase 66)

CloudX strictly demarcates execution boundaries and sanitizes all incoming identifiers and file paths:

- **Permission Scopes (`auth.PermissionScope`)**: Distinguishes Control Plane, Worker, and Runtime execution contexts via `auth.EnsureScope`.
- **Resource ID Sanitization (`auth.ValidateResourceID`)**: Enforces alphanumeric naming rules and rejects illegal characters, null bytes, and traversal tokens (`..`, `/`, `\`, `*`, `?`).
- **Path Traversal Prevention (`auth.ValidateSafePath`)**: Ensures volume locations and runtime paths cannot escape root storage boundaries.

---

### 9. Unit Test Completion & Reliability Verification (Phase 67)

CloudX features high meaningful automated test coverage across all subsystems, verifying production readiness and system invariants:

- **Metrics & Observability (`internal/metrics`, `internal/otel`)**: Counter, Gauge, and Histogram metrics (>91% coverage).
- **Health & Failure Detectors (`internal/health`, `internal/state/transitions`)**: Failure detectors, probe execution, heartbeat monitoring, and deterministic state transitions (>88% coverage).
- **Control Plane & Service Discovery (`internal/controlplane`, `internal/registry`)**: Multi-version rolling updates, instant rollbacks, multi-factor placement scheduler, and service discovery (>82% coverage).
- **Storage & State Machine (`internal/state/sqlite`)**: Transactional ACID state persistence, relational foreign-key integrity, and repository CRUD (>83% coverage).
- **Runtime Execution (`internal/runtime`, `internal/worker`)**: Native process lifecycle supervision, resource limits, and worker daemon management (>80% coverage).

---

### 10. Automated Cluster Integration Test Harness (Phase 68)

CloudX includes a hermetic, local-first **Integration Test Harness** (`test/integration/harness.go`) designed for end-to-end multi-node cluster verification without any external cloud service or third-party infrastructure dependencies.

#### Verification Cycle
The harness tests the complete cluster lifecycle in an automated test suite:
$$\text{Deploy v1} \longrightarrow \text{Scale Up} \longrightarrow \text{Crash Task/Worker} \longrightarrow \text{Auto-Recover} \longrightarrow \text{Deploy v2} \longrightarrow \text{Rollback to v1}$$

#### Running Integration Tests
```bash
# Execute the full integration test suite
go test -v ./test/integration/...
```

---

### 11. Deterministic Failure Testing (Phase 69)

CloudX features a comprehensive automated failure test suite (`test/integration/failure_scenarios_test.go`) validating deterministic fault tolerance, self-healing, and state recovery across 10 critical failure modes:

1. **Worker Crash**: Orphaned tasks on abruptly lost workers are automatically detected by the Reconciler and migrated to surviving healthy nodes.
2. **Process Crash**: Terminated workloads are caught via process supervision, transitioning tasks to `FAILED` and auto-spawning replacements.
3. **Control-Plane Restart**: Full state, deployment histories, and task mappings survive abrupt control-plane shutdown and recovery against persistent SQLite storage.
4. **SQLite Interruption**: Interrupted transactions rollback completely without partial or corrupted state persistence.
5. **RPC Timeout**: Expired client deadlines fail cleanly with standard gRPC `DeadlineExceeded` without server hangs or goroutine leaks.
6. **Duplicate Messages**: Worker re-registration and task assignments execute idempotently without throwing `AlreadyExists` or duplicating state.
7. **Delayed Messages**: Unresponsive workers trigger progressive degradation (`READY` $\rightarrow$ `SUSPECTED` $\rightarrow$ `UNHEALTHY` $\rightarrow$ `LOST`).
8. **Health Failure**: Faulty probes correctly transition workloads and self-heal upon probe recovery.
9. **Resource Exhaustion**: Safe in-process resource limits protect cluster stability during memory pressure.
10. **Worker Reconnection**: Disconnected worker daemons seamlessly reconnect and restore `READY` status using persisted `worker.id`.

```bash
# Run all failure scenario tests
go test -v -run TestFailure_ ./test/integration/...
```

---

### 12. Race & Concurrency Hardening (Phase 70)

CloudX implements strict synchronization and mutual exclusion guarantees across all core layers, verified via high-concurrency automated race testing (`test/integration/race_concurrency_test.go`):

- **State Repository Layer**: SQLite WAL mode and serialized single-connection pooling prevent data races and database lock contention under high-throughput parallel reads and writes.
- **Worker Task Manager**: Thread-safe task maps protected by `sync.RWMutex` combined with per-task state mutexes eliminate torn reads during simultaneous assignment, supervision, and stop requests.
- **Scheduler Scoring**: Stateless, read-only multi-node capacity evaluations support unbounded concurrent scheduling passes.
- **Atomic Reconciliation**: The Control Plane Reconciler uses a dedicated `reconcileMu` mutex to serialize cluster convergence passes, preventing race conditions and replica over-provisioning during simultaneous triggers.
- **Event Engine**: Thread-safe transactional append-only recording with in-line payload secret masking under heavy concurrent publishing.

```bash
# Run race and concurrency verification tests
go test -v -run TestRace_ ./test/integration/...
```

---

### 13. CloudX Golden-Path End-to-End Test Suite (Phase 71)

CloudX includes a fully automated 17-step **Golden-Path End-to-End Test Suite** (`test/integration/e2e_golden_path_test.go`) validating the entire operational lifecycle from initialization to self-healing, rolling upgrade, rollback, and inspection:

1. **Initialize cluster**: SQLite database and schemas setup.
2. **Start control plane**: Core subsystems (Reconciler, Scheduler, Failure Detector, Event Engine) launched.
3. **Start 3 workers**: Worker daemons register via gRPC and establish heartbeat streams.
4. **Deploy API**: `cloudx-api:v1` deployed and scheduled.
5. **Scale API to 3**: Dynamic scale-up to 3 replicas across 3 workers.
6. **Verify health**: Active probe and heartbeat validation.
7. **Kill one process**: SIGKILL simulation on task.
8. **Verify restart**: Reconciler self-heals task deficit.
9. **Kill worker node**: Simulates abrupt node termination.
10. **Verify rescheduling**: Orphaned tasks automatically rescheduled onto healthy nodes.
11. **Deploy v2**: Rolling upgrade to `cloudx-api:v2`.
12. **Verify rollout**: Progressive replica cutover to v2.
13. **Trigger failure**: Canary probe fault injection.
14. **Rollback**: Instantaneous rollback to stable `v1` version.
15. **Verify v1**: Verification that active deployment points to `v1`.
16. **Inspect events**: Complete audit trail verification (`SERVICE_CREATED`, `SERVICE_SCALED`, `DEPLOYMENT_STARTED`, `SERVICE_ROLLED_BACK`).
17. **Inspect logs**: Workload log capture and aggregation verification.

```bash
# Run the complete Golden-Path End-to-End test suite
go test -v -run TestE2E_GoldenPathScenario ./test/integration/...
```

---

### 14. Architecture Documentation (Phase 72)

For deep technical insights into CloudX internals, consult the [Comprehensive Architecture Guide](docs/ARCHITECTURE.md), covering:
- **System Overview & Principles**: Local-first runtime, zero CGO, declarative state loops.
- **Topological Architecture**: Multi-worker & control plane system diagram.
- **Subsystem Breakdown**: Reconciler, Scheduler, Native Process Runtime, Volume Manager, and Job Engine.
- **State Model & Storage**: SQLite WAL transactional database schema and ER diagrams.
- **Continuous Reconciliation**: Desired-state convergence loop mechanics.
- **Networking & Discovery**: Dynamic port allocator (`30000–32767`), conflict avoidance, and service registry.
- **Fault Recovery**: Multi-tier failure detector state machine, node evictions, and rolling deployment auto-rollbacks.
- **Security & Boundaries**: mTLS PKI, permission isolation, path-traversal guards, and secret redaction.
- **Observability**: Prometheus metrics collectors, OpenTelemetry tracing spans, and diagnostic probes.

---

### 15. Developer & Operations Guide (Phase 73)

For day-to-day operations, manifest templates, and CLI guides, consult the [Developer & Operations Guide](docs/DEVELOPER_GUIDE.md), featuring:
- **Installation & Pre-requisites**: Pure-Go zero-CGO compilation.
- **Configuration Precedence**: Precedence hierarchy and YAML reference.
- **Cluster Initialization & Worker Joining**: Multi-worker local cluster setup.
- **Deployments & Scaling**: Declarative YAML service templates and dynamic replica scaling.
- **Batch Jobs**: Finite workload manifests, retries, backoff, and timeouts.
- **Volumes & Networking**: Storage provisioning and dynamic port allocations.
- **Logs, Events & Rollbacks**: Live streaming logs, audit trails, and instant rollbacks.
- **Diagnostics**: Automated 9-vector health checks via `cloudx diagnose`.

---

### 16. Design Decisions & Technical Rationale (Phase 74)

For system design interviews and architectural deep-dives, consult the [Design Decisions Guide](docs/DESIGN_DECISIONS.md), addressing:
- **Why Native OS Process Runtime First?**: Sub-millisecond cold starts (< 5ms), zero container daemon dependencies, and portable process isolation.
- **Why Not Kubernetes?**: Eliminating the heavy operational tax (etcd quorums, multi-component control planes, 500MB+ RAM) in favor of lightweight local-first orchestration (< 35MB RAM).
- **Why SQLite WAL?**: Embedded ACID persistence, zero-CGO compilation, and single-file portability.
- **Why gRPC & Protobuf?**: Strongly typed contracts, binary efficiency, and HTTP/2 bidirectional multiplexing.
- **Why Desired vs. Actual State?**: Inherent self-healing, network partition tolerance, and declarative idempotency.
- **Why Deterministic Rule-Based Scheduling?**: Full placement explainability (`ScoreBreakdown`), anti-affinity spreading, and resource packing.
- **Why Level-Triggered Reconciliation?**: Resilient state convergence with zero event loss.

---

### 17. Troubleshooting & Diagnostics Guide (Phase 75)

For rapid operational issue resolution, consult the [Troubleshooting & Diagnostics Guide](docs/TROUBLESHOOTING_GUIDE.md), featuring resolution paths for 10 common failure scenarios:
- **Control Plane Unreachable**: Socket checks, port binding conflicts, and daemon restart procedures.
- **Worker Join Failures**: Node ID collisions, firewall blocks, and mTLS certificate verification.
- **Worker Lost Transitions**: Heartbeat timeout progression (`READY` $\rightarrow$ `SUSPECTED` $\rightarrow$ `UNHEALTHY` $\rightarrow$ `LOST`), task evictions, and reconnection healing.
- **Workload CrashLoopBackOff**: Process exit codes (e.g. exit code 137 OOM), missing binaries, and RAM limits.
- **Health Check Failures**: Tuning probe delays, route validation, and timeout thresholds.
- **Stuck Deployments**: Canary probe failure diagnosis and automated rollbacks.
- **Rollback Failures**: History revision lookups and declarative patch manifests.
- **Volume & Path Traversal Conflicts**: Path traversal attack prevention (`../`) and exclusive lock contention.
- **Port Allocation Collisions**: Static port conflicts and dynamic port pool (`30000–32767`) expansion.
- **Resource Exhaustion**: Capacity bottleneck analysis and worker scaling.

---

### 18. Scheduler Performance & Scale Benchmarks (Phase 76)

CloudX features a high-throughput deterministic rule-based scheduler with sub-millisecond placement latency across single and multi-hundred worker cluster sizes:

| Scale (Worker Nodes) | Avg Latency | P95 Latency | Throughput | Alloc Memory / Op |
| :--- | :--- | :--- | :--- | :--- |
| **10 Workers** | **3.09 µs** | < 10 µs | **~323,000 ops/sec** | 5.5 KB |
| **50 Workers** | **21.9 µs** | < 50 µs | **~45,500 ops/sec** | 23.8 KB |
| **100 Workers** | **47.6 µs** | ~520 µs | **~21,000 ops/sec** | 48.2 KB |
| **500 Workers** | **371.8 µs** | ~1.28 ms | **~2,700 ops/sec** | 366.7 KB |

```bash
# Run scheduler benchmark suite
go test -bench=BenchmarkScheduler_Scale -benchmem ./internal/scheduler/...
```

---

### 19. Reconciliation Performance & Scale Benchmarks (Phase 77)

CloudX implements a fast, level-triggered desired-state reconciliation loop capable of evaluating hundreds of services and up to 1,000 tasks in tens of milliseconds:

| Scale Scenario | Services Evaluated | Tasks Evaluated | Workers Evaluated | Sweep Duration | Events Generated |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **10 Services / 20 Tasks** | 10 | 20 | 3 | **2.66 ms** | 0 (Steady State) |
| **100 Services / 200 Tasks** | 100 | 200 | 10 | **17.77 ms** | 0 (Steady State) |
| **1000 Tasks / 250 Services** | 250 | 1,000 | 20 | **49.90 ms** | 0 (Steady State) |

```bash
# Run reconciliation scale benchmark suite
go test -v -run TestReconciliation_ScaleBenchmark ./internal/controlplane/...
go test -bench=BenchmarkReconciliation_Sweep -benchmem ./internal/controlplane/...
```

---

### 20. Worker Stress Testing & Resource Stability (Phase 78)

CloudX worker daemons and task managers are verified under high-throughput concurrent workloads with short-lived native processes:

- **100% Process Cleanup**: 100/100 tasks launched, supervised, and cleanly stopped without zombie PIDs.
- **Zero Goroutine Leaks**: `Goroutine Leak Delta = 0` (baseline: 2, post-stress: 2).
- **Log Ring-Buffer Capture**: 100/100 workload stdout/stderr streams captured into memory-bounded circular buffers.
- **State Reporting Under Concurrency**: 500 state update transitions verified across lifecycle stages (`PENDING` $\rightarrow$ `ASSIGNED` $\rightarrow$ `STARTING` $\rightarrow$ `RUNNING` $\rightarrow$ `STOPPED`).

```bash
# Run worker stress test suite
go test -v -run TestWorkerStress_ShortLivedWorkloads ./internal/worker/...
```

---

### 21. SQLite Database Hardening & Persistence Verification (Phase 79)

CloudX features a hardened, transactional embedded SQLite state store (`internal/state/sqlite/`) utilizing pure-Go `modernc.org/sqlite` in Write-Ahead Logging (`WAL`) mode:

- **ACID Transactions**: Multi-statement transactional rollback guarantees verified on error injection.
- **Concurrent Read/Write Isolation**: Zero lock contentions or torn reads under 40 parallel reader/writer goroutines.
- **Persistence Recovery**: 100% data recovery verified across clean restarts and sudden process termination.
- **Corruption Resilience**: Invalid/corrupt file headers cleanly detected and rejected during initialization.
- **Migration Idempotency**: Multi-pass schema execution verified with `PRAGMA integrity_check = ok`.
- **High Transactional Throughput**: **~10,600 atomic multi-statement writes/second** (**94.4 µs/op**).

```bash
# Run database hardening test suite
go test -v -run TestDatabaseHardening_ ./internal/state/sqlite/...
go test -bench=BenchmarkDatabase_TransactionalWrites -benchmem ./internal/state/sqlite/...
```

---

### 22. Cross-Platform Builds & Target Architectures (Phase 80)

CloudX is built with pure Go (`CGO_ENABLED=0`) and natively cross-compiles for tier-1 developer platforms:

| OS Target | Architecture | CLI Binary (`cloudx`) | Worker Binary (`cloudx-worker`) |
| :--- | :--- | :--- | :--- |
| **Windows** | `amd64` (x86_64) | `bin/dist/windows_amd64/cloudx.exe` | `bin/dist/windows_amd64/cloudx-worker.exe` |
| **Windows** | `arm64` | `bin/dist/windows_arm64/cloudx.exe` | `bin/dist/windows_arm64/cloudx-worker.exe` |
| **Linux** | `amd64` (x86_64) | `bin/dist/linux_amd64/cloudx` | `bin/dist/linux_amd64/cloudx-worker` |
| **Linux** | `arm64` (aarch64) | `bin/dist/linux_arm64/cloudx` | `bin/dist/linux_arm64/cloudx-worker` |
| **macOS** | `amd64` (Intel) | `bin/dist/darwin_amd64/cloudx` | `bin/dist/darwin_amd64/cloudx-worker` |
| **macOS** | `arm64` (Apple Silicon) | `bin/dist/darwin_arm64/cloudx` | `bin/dist/darwin_arm64/cloudx-worker` |

```bash
# Build all cross-platform targets
go run scripts/cross_build.go
```














