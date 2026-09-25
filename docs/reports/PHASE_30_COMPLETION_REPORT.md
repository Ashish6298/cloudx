# Phase 30 Completion Report: Failure Simulation

## Executive Summary
Phase 30 establishes deterministic, safe, and workload-targeted failure-testing tools for the CloudX orchestrator. The `cloudx fail` suite enables operators and automated validation pipelines to induce controlled faults across workloads and cluster nodes without performing dangerous host-level disruptions. All failure scenarios trigger state changes, audit events, and automatic reconciliations that demonstrate repeatable recovery.

---

## Implemented Failure Simulation Scenarios

| Scenario | CLI Command | Implementation Details | Recovery Mechanism Tested |
| :--- | :--- | :--- | :--- |
| **1. Kill Process** | `cloudx fail kill-process --task <id>` | Sends terminate signal to the process managed by TaskManager. | Task manager detects process exit, marks state `FAILED`, triggers restart policy or triggers Reconciler replica replacement if in `CRASH_LOOP`. |
| **2. Stop Worker** | `cloudx fail stop-worker --worker <id>` | Gracefully terminates a running worker process. | Control plane failure detector marks worker `SUSPECTED` -> `UNHEALTHY` -> `LOST`. Reconciler detects orphaned tasks and schedules new replicas on healthy workers. |
| **3. Break Health** | `cloudx fail break-health --task <id> [--reason <msg>]` | Injects synthetic failure response into `SimulatedProber` for the target task. | Health probe registers consecutive failures exceeding threshold, transitions task to `UNHEALTHY`. Task manager stops task and initiates restart/reconciliation. |
| **4. Delay Heartbeat** | `cloudx fail delay-heartbeat --worker <id> [--delay <duration>]` | Backdates worker's `LastHeartbeatAt` in SQLite state storage. | Failure detector detects missed heartbeat deadline, transitions worker state to `SUSPECTED` / `UNHEALTHY` / `LOST`. |
| **5. Exhaust Resources**| `cloudx fail exhaust-resources --task <id> [--memory-mb <mb>] [--duration <dur>]` | Allocates a bounded in-memory buffer (capped safely at 512MB max) inside a controlled goroutine. | Resource monitor captures elevated RAM utilization in periodic metrics; verifies cgroup/quota enforcement triggers without crashing host OS. |

---

## CLI Integration & Usage

### 1. `cloudx fail kill-process`
```bash
# Kill a specific managed task process
cloudx fail kill-process --task tsk-18d89ba2566deacc-df22bd817e42
```

### 2. `cloudx fail stop-worker`
```bash
# Stop a worker node
cloudx fail stop-worker --worker wrk-18d89ba1ed919788-d713ee33dd3b
```

### 3. `cloudx fail break-health`
```bash
# Force task health probes to fail
cloudx fail break-health --task tsk-18d89ba2dd9e7778-ae1b26c25102 --reason "simulated 500 error"
```

### 4. `cloudx fail delay-heartbeat`
```bash
# Backdate worker heartbeat to test failure detection
cloudx fail delay-heartbeat --worker wrk-18d89ba1ed919788-d713ee33dd3b --delay 45s
```

### 5. `cloudx fail exhaust-resources`
```bash
# Allocate temporary memory buffer for a workload
cloudx fail exhaust-resources --task tsk-18d89ba2566deacc-df22bd817e42 --memory-mb 64 --duration 10s
```

---

## Verification & Test Results

### 1. Unit & Scenario Tests (`internal/simulation`)
- `TestSimulator_KillProcess`: Verified process kill, health probe teardown, and task transition to `FAILED`.
- `TestSimulator_BreakAndRestoreHealthEndpoint`: Verified injection and clearing of mock probe failure states.
- `TestSimulator_DelayHeartbeat`: Verified backdating of worker heartbeats in SQLite and subsequent failure detection.
- `TestSimulator_ExhaustResources`: Verified safe, bounded memory buffer allocation and subsequent release.

### 2. CLI Integration Tests (`cmd/cloudx`)
- `TestFailSimulationCLI`: Verified all 5 subcommands correctly parse flags, interact with the state store, log audit events, and produce structured output.

### 3. Full Test Suite Validation
```
ok  github.com/cloudx-org/cloudx/cmd/cloudx           1.459s
ok  github.com/cloudx-org/cloudx/cmd/cloudx-worker    0.237s
ok  github.com/cloudx-org/cloudx/internal/api         0.303s
ok  github.com/cloudx-org/cloudx/internal/common/errors 0.671s
ok  github.com/cloudx-org/cloudx/internal/common/id     0.676s
ok  github.com/cloudx-org/cloudx/internal/common/logging 0.672s
ok  github.com/cloudx-org/cloudx/internal/common/version 0.631s
ok  github.com/cloudx-org/cloudx/internal/config      0.773s
ok  github.com/cloudx-org/cloudx/internal/controlplane 0.342s
ok  github.com/cloudx-org/cloudx/internal/health      0.915s
ok  github.com/cloudx-org/cloudx/internal/runtime     3.358s
ok  github.com/cloudx-org/cloudx/internal/scheduler   0.418s
ok  github.com/cloudx-org/cloudx/internal/simulation  2.489s
ok  github.com/cloudx-org/cloudx/internal/spec       0.535s
ok  github.com/cloudx-org/cloudx/internal/state/models 0.499s
ok  github.com/cloudx-org/cloudx/internal/state/sqlite 0.981s
ok  github.com/cloudx-org/cloudx/internal/state/transitions 0.453s
ok  github.com/cloudx-org/cloudx/internal/worker      8.676s
ok  github.com/cloudx-org/cloudx/internal/worker/monitor 0.553s
ok  github.com/cloudx-org/cloudx/proto/v1             0.162s
```
**Total Passing Packages**: 20 / 20 tested packages passed with zero failures.

---

## Safety Guarantees
1. **Scoped Faults**: Process termination and resource allocation are restricted strictly to tasks tracked by CloudX TaskManager.
2. **Capped Resource Consumption**: Resource exhaustion enforces a hard ceiling of 512 MB to ensure host system stability.
3. **Audit Logging**: Every simulated failure generates a persistent record in the `events` table for cluster observability.

---

## Milestone 8 Status & Readiness for Next Milestone
- **Milestone 8 (Health and Failure Recovery)** is now **100% COMPLETE**:
  - Phase 28: Health Checks (Process, TCP, HTTP) ✅
  - Phase 29: Automatic Failure Recovery (Worker lost, Task crashed, Reconciler self-healing) ✅
  - Phase 30: Failure Simulation (`kill-process`, `stop-worker`, `break-health`, `delay-heartbeat`, `exhaust-resources`) ✅
- **Readiness**: **READY FOR MILESTONE 9** (Networking and Service Discovery / Routing).
