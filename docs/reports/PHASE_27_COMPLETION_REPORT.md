# Phase 27 Completion Report: Restart Policies

**Status:** Completed  
**Milestone:** 7 — Services and Reconciliation  
**Component:** Worker Task Manager & State Transitions  
**Date:** September 2026  

---

## 1. Executive Summary

Phase 27 implements process lifecycle restart policies for CloudX tasks running on worker nodes. The engine supports `never`, `on-failure`, and `always` restart policies, backed by an exponential backoff algorithm with jitter-less determinism and configurable limits to protect worker nodes against rapid restart thrashing (fork bombs / crash loops).

When tasks experience sustained failures exceeding policy thresholds, the task lifecycle explicitly enters the `CRASH_LOOP` state, enabling alerting, visibility, and control-plane triage.

---

## 2. Key Features Implemented

### 2.1 Restart Policies
- **`never` (`RestartPolicyNever`)**:
  - Tasks that terminate (cleanly exit `0` or fail non-zero) are never restarted.
  - Final state moves to `STOPPED` (if exit code 0) or `FAILED` (if non-zero).
- **`on-failure` (`RestartPolicyOnFailure`)**:
  - Tasks that exit cleanly (`exit code 0`) transition to `STOPPED` and are **not** restarted.
  - Tasks that crash or exit with non-zero codes enter the restart backoff loop.
- **`always` (`RestartPolicyAlways`)**:
  - Tasks that terminate for any reason (clean exit or crash) are restarted up to max retry attempts unless explicitly stopped by a user/control-plane signal.

### 2.2 Exponential Backoff & Rapid Restart Prevention
- **Calculation Formula**:
  $$\text{Backoff} = \min(\text{BaseBackoff} \times 2^{\text{RestartCount} - 1},\, \text{MaxBackoff})$$
  - Default Base: `500ms`
  - Default Multiplier: `2.0`
  - Default Max Backoff: `30s`
  - Default Max Retries: `5` attempts
- **State Flow**:
  $$\text{RUNNING} \rightarrow \text{FAILED} \rightarrow \text{BACKOFF} \rightarrow \text{RESTARTING} \rightarrow \text{STARTING} \rightarrow \text{RUNNING}$$
- **Asynchronous Execution Delay**:
  - The restart routine waits `time.After(backoff)` in an asynchronous goroutine, checking for cancellation / task stop requests prior to relaunching the process.

### 2.3 Crash Loop Detection (`CRASH_LOOP`)
- Once `RestartCount > MaxRetries`, task restarts are halted.
- The task transitions:
  $$\text{FAILED} \rightarrow \text{CRASH\_LOOP}$$
- State persists in task state history and emits structured warning logs for operator intervention.

---

## 3. State Transition Matrix Updates

| Source State | Destination State | Valid Under Phase 27 |
|:---|:---|:---:|
| `FAILED` | `BACKOFF` | Yes (Trigger restart delay) |
| `FAILED` | `CRASH_LOOP` | Yes (Exceeded max retries) |
| `BACKOFF` | `RESTARTING` | Yes (Backoff timer expired) |
| `BACKOFF` | `STOPPING` / `STOPPED` | Yes (User cancelled task during backoff) |
| `RESTARTING` | `STARTING` | Yes (Relaunching process) |
| `CRASH_LOOP` | `RESTARTING` / `STOPPING` | Yes (Manual restart or teardown) |

---

## 4. Verification & Testing

All unit tests across the entire CloudX workspace passed with `0` errors.

### Dedicated Test Coverage
- **`TestTaskManager_RestartPolicy_Never`**:
  - Confirmed exit 1 under policy `never` marks task `FAILED` without triggering backoff or relaunch.
- **`TestTaskManager_RestartPolicy_OnFailure_And_CrashLoop`**:
  - Confirmed successive crashes increment `RestartCount`, double backoff duration, advance states `RUNNING` $\rightarrow$ `FAILED` $\rightarrow$ `BACKOFF` $\rightarrow$ `RESTARTING` $\rightarrow$ `STARTING` $\rightarrow$ `RUNNING`, and finally transition to `CRASH_LOOP` when max retries are exceeded.
- **`TestTaskManager_RestartPolicy_Always_CleanExit`**:
  - Confirmed clean exit (code 0) under policy `always` triggers restart attempt and backoff.

### Global Test Suite Run
```bash
go test -count=1 ./...
ok      github.com/cloudx-org/cloudx/cmd/cloudx                 1.670s
ok      github.com/cloudx-org/cloudx/cmd/cloudx-worker          0.329s
ok      github.com/cloudx-org/cloudx/internal/api               0.387s
ok      github.com/cloudx-org/cloudx/internal/common/errors     0.859s
ok      github.com/cloudx-org/cloudx/internal/common/id         0.918s
ok      github.com/cloudx-org/cloudx/internal/common/logging    1.044s
ok      github.com/cloudx-org/cloudx/internal/common/version    0.810s
ok      github.com/cloudx-org/cloudx/internal/config            0.998s
ok      github.com/cloudx-org/cloudx/internal/controlplane      0.419s
ok      github.com/cloudx-org/cloudx/internal/health            2.213s
ok      github.com/cloudx-org/cloudx/internal/runtime           3.998s
ok      github.com/cloudx-org/cloudx/internal/scheduler         0.560s
ok      github.com/cloudx-org/cloudx/internal/spec              0.815s
ok      github.com/cloudx-org/cloudx/internal/state/models      0.772s
ok      github.com/cloudx-org/cloudx/internal/state/sqlite      1.399s
ok      github.com/cloudx-org/cloudx/internal/state/transitions 0.628s
ok      github.com/cloudx-org/cloudx/internal/worker            6.611s
ok      github.com/cloudx-org/cloudx/internal/worker/monitor    0.745s
ok      github.com/cloudx-org/cloudx/proto/v1                   0.227s
```

---

## 5. Next Phase Readiness

- **Milestone 7 (Services and Reconciliation)** is **100% complete** (Phases 23–27).
- The system is fully ready for **Milestone 8 — Health and Failure Recovery (Phase 28: Health Probes)**.
