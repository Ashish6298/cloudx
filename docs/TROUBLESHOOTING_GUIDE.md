# CloudX Troubleshooting & Operations Guide

Welcome to the CloudX Troubleshooting and Diagnostics Guide. This document provides systematic diagnosis workflows, root-cause analyses, error code explanations, and actionable remediation steps for the 10 most common cluster failures and operational anomalies.

---

## Table of Contents
1. [General Diagnostic Workflow](#1-general-diagnostic-workflow)
2. [Failure 1: Control Plane Unavailable / Unreachable](#2-failure-1-control-plane-unavailable--unreachable)
3. [Failure 2: Worker Cannot Join Cluster](#3-failure-2-worker-cannot-join-cluster)
4. [Failure 3: Worker Node Marked LOST](#4-failure-3-worker-node-marked-lost)
5. [Failure 4: Service / Workload Crash (CrashLoopBackOff)](#5-failure-4-service--workload-crash-crashloopbackoff)
6. [Failure 5: Health Check Probe Failures](#6-failure-5-health-check-probe-failures)
7. [Failure 6: Deployment Stuck / Rolling Update Halted](#7-failure-6-deployment-stuck--rolling-update-halted)
8. [Failure 7: Rollback Failure](#8-failure-7-rollback-failure)
9. [Failure 8: Volume Mount & Path Conflicts](#9-failure-8-volume-mount--path-conflicts)
10. [Failure 9: Port Allocation Conflicts](#10-failure-9-port-allocation-conflicts)
11. [Failure 10: Insufficient Cluster Resources](#11-failure-10-insufficient-cluster-resources)
12. [Summary Quick Reference Table](#12-summary-quick-reference-table)

---

## 1. General Diagnostic Workflow

Before diving into specific component failures, execute the automated **9-Vector Diagnostic Engine** to pinpoint cluster health issues:

```bash
# Run comprehensive automated cluster diagnostics
cloudx diagnose

# Run with verbose error details
cloudx diagnose --verbose

# Output machine-readable JSON for scripts
cloudx diagnose --output json
```

### Diagnostic Vectors Evaluated:
1. **Control plane reachability & listener socket**
2. **Worker daemon connectivity & TCP handshakes**
3. **SQLite database integrity (`PRAGMA integrity_check`) & WAL status**
4. **Heartbeat timeouts & node state progression**
5. **Scheduler capacity & ready nodes**
6. **Orphaned tasks audit**
7. **Deployment rollout progress & canary health**
8. **Host CPU/memory resource pressure**
9. **Configuration syntax & path permissions**

---

## 2. Failure 1: Control Plane Unavailable / Unreachable

### Symptoms:
- CLI returns `CloudX control plane is unreachable` or `connection refused`.
- Workers fail to report heartbeats.

### Diagnostic Steps:
```bash
# 1. Check if control plane process is running
ps aux | grep "cloudx server"  # Linux/macOS
Get-Process -Name cloudx       # PowerShell

# 2. Verify configured control plane endpoint
cloudx config show

# 3. Test TCP port reachability
nc -zv 127.0.0.1 7000
```

### Root Causes & Remediation:
1. **Control plane daemon is stopped**:
   - *Fix*: Start the control plane: `cloudx server --config ~/.cloudx/cloudx.yaml`.
2. **Incorrect endpoint in client configuration**:
   - *Fix*: Override endpoint via CLI flag `--control-plane-addr 127.0.0.1:7000` or update `CLOUDX_CONTROL_PLANE_ADDRESS` environment variable.
3. **Port 7000 is occupied by another process**:
   - *Fix*: Identify occupying process with `lsof -i :7000` or `netstat -ano | findstr :7000` and kill it or rebind CloudX to another port (`--control-plane-addr 127.0.0.1:7100`).

---

## 3. Failure 2: Worker Cannot Join Cluster

### Symptoms:
- `cloudx-worker` exits immediately or logs `Registration failed: rpc error: code = Unavailable`.
- Worker does not appear in `cloudx worker list`.

### Diagnostic Steps:
```bash
# 1. Inspect worker daemon logs
tail -n 50 ~/.cloudx/logs/worker.log

# 2. Check TLS certificate validity (if mTLS enabled)
cloudx config validate
```

### Root Causes & Remediation:
1. **Node ID collision**:
   - *Cause*: A worker is attempting to register with a `node-id` already registered by another active daemon.
   - *Fix*: Pass a unique node ID: `cloudx-worker --node-id worker-node-$(uuidgen | cut -d'-' -f1)`.
2. **Control plane port blocked by firewall**:
   - *Fix*: Ensure firewall allows inbound TCP traffic on port 7000.
3. **TLS/mTLS certificate mismatch**:
   - *Fix*: Verify CA certificate path or re-generate worker certs using `internal/auth/tls.go` PKI utilities.

---

## 4. Failure 3: Worker Node Marked LOST

### Symptoms:
- Node status in `cloudx node list` transitions: `READY` $\rightarrow$ `SUSPECTED` (5s) $\rightarrow$ `UNHEALTHY` (15s) $\rightarrow$ `LOST` (30s).
- All tasks hosted on the worker are evicted.

### Diagnostic Steps:
```bash
# 1. Inspect cluster events for node health transitions
cloudx events --limit 20

# 2. Verify worker process status on the remote machine
cloudx node inspect <node-id>
```

### Root Causes & Remediation:
1. **Worker daemon crashed or host rebooted**:
   - *Fix*: Re-launch `cloudx-worker`. Upon reconnecting, the worker re-registers and resumes heartbeat streaming; the reconciler will automatically restore scheduling eligibility.
2. **Heavy network latency or packet loss**:
   - *Fix*: If network links between worker and control plane are slow, adjust heartbeat grace periods in `cloudx.yaml`:
     ```yaml
     health:
       heartbeat_interval: "5s"
       suspect_timeout: "15s"
       lost_timeout: "60s"
     ```

---

## 5. Failure 4: Service / Workload Crash (CrashLoopBackOff)

### Symptoms:
- Task state alternates between `RUNNING` $\rightarrow$ `FAILED` $\rightarrow$ `RESTARTING`.
- Task restart counter increments continuously.

### Diagnostic Steps:
```bash
# 1. Inspect workload stdout/stderr logs
cloudx service logs <service-name>

# 2. Inspect task exit code and failure reason
cloudx task inspect <task-id>
```

### Root Causes & Remediation:
1. **Application runtime error / missing binary**:
   - *Cause*: Command binary path does not exist or missing executable permissions (`chmod +x`).
   - *Fix*: Ensure binary exists on the worker host filesystem or specify absolute path in manifest.
2. **Missing required environment variables or secrets**:
   - *Fix*: Verify `env` mappings in service YAML manifest.
3. **Process exited with code 137 (OOM / SIGKILL)**:
   - *Fix*: The process exceeded memory limits. Increase memory allocation in `resources.memory` (e.g. `512Mi` $\rightarrow$ `1Gi`).

---

## 6. Failure 5: Health Check Probe Failures

### Symptoms:
- Tasks start and run for a few seconds, then transition to `UNHEALTHY` and get terminated.
- Reconciler logs `Health check failed: timeout or non-200 HTTP status`.

### Diagnostic Steps:
```bash
# 1. Inspect recent health events
cloudx events --service <service-name>

# 2. Test the health check endpoint directly from the worker host
curl -v http://127.0.0.1:<allocated-port>/healthz
```

### Root Causes & Remediation:
1. **Slow application startup time (probe fires before app is ready)**:
   - *Fix*: Increase `initialDelaySeconds` in service health check spec (e.g., set to `10s`).
2. **Incorrect probe port or endpoint path**:
   - *Fix*: Ensure `spec.template.healthCheck.path` matches the actual application route (e.g., `/health` vs `/healthz`).
3. **Application deadlock on database connection pool**:
   - *Fix*: Inspect application logs (`cloudx service logs <service>`) to resolve external dependency blocking.

---

## 7. Failure 6: Deployment Stuck / Rolling Update Halted

### Symptoms:
- `cloudx deploy api:v2` is initiated, but new replicas never transition to `READY`.
- `cloudx deployment list` shows status `IN_PROGRESS` or `FAILED`.

### Diagnostic Steps:
```bash
# 1. Inspect active deployment state
cloudx deployment inspect <service-name>

# 2. Check cluster events for deployment halts
cloudx events
```

### Root Causes & Remediation:
1. **New version fails health checks**:
   - *Cause*: Deployment controller detects failed canary probes and halts rollout to protect production traffic.
   - *Fix*: Fix application bugs in v2 or run `cloudx rollback <service-name>` to immediately restore v1.
2. **Insufficient cluster capacity for surge replicas**:
   - *Fix*: Add an additional worker node or configure deployment strategy with non-surging rolling replacement.

---

## 8. Failure 7: Rollback Failure

### Symptoms:
- `cloudx rollback <service-name>` returns `no previous revision found` or fails to converge.

### Diagnostic Steps:
```bash
# 1. List deployment history revisions
cloudx deployment history <service-name>
```

### Root Causes & Remediation:
1. **No prior deployment revisions exist**:
   - *Cause*: Service was deployed for the first time without prior versions.
   - *Fix*: Explicitly deploy target version: `cloudx deploy <service-name>:<version>`.
2. **Target rollback version contains obsolete configuration**:
   - *Fix*: Deploy a new patch manifest directly using `cloudx deploy -f service.yaml`.

---

## 9. Failure 8: Volume Mount & Path Conflicts

### Symptoms:
- Task fails to start with error: `failed to mount volume: directory outside storage root (path traversal detected)` or `volume in use by another task`.

### Diagnostic Steps:
```bash
# 1. Inspect volume metadata
cloudx volume inspect <volume-name>

# 2. Check volume attachments
cloudx volume list
```

### Root Causes & Remediation:
1. **Path traversal attack protection triggered**:
   - *Cause*: Volume mount specification contains `../` or unauthorized root directory references.
   - *Fix*: Use safe, relative paths or authorized storage directories under `~/.cloudx/volumes/`.
2. **Exclusive volume lock conflict**:
   - *Cause*: Two tasks requesting exclusive read-write access to the same local directory.
   - *Fix*: Ensure single replica per exclusive volume or use shared read-only drivers.

---

## 10. Failure 9: Port Allocation Conflicts

### Symptoms:
- Task stays in `PENDING` with scheduler rejection: `node rejected: host port 8080 already allocated`.

### Diagnostic Steps:
```bash
# 1. Inspect port allocations on the worker
cloudx network list

# 2. Check active task port mappings
cloudx task list
```

### Root Causes & Remediation:
1. **Static port collisions**:
   - *Cause*: Multiple service tasks explicitly requesting identical static host port bindings on the same node.
   - *Fix*: Use dynamic port allocation (`port: 0` or omit static host port mapping) to let CloudX assign an available port from the pool (`30000–32767`).
2. **Port pool exhaustion**:
   - *Fix*: Expand port range in `cloudx.yaml` (`worker.port_range.min: 20000`, `worker.port_range.max: 40000`).

---

## 11. Failure 10: Insufficient Cluster Resources

### Symptoms:
- Reconciler logs `0/3 nodes feasible for task scheduling: insufficient CPU/Memory`.
- Tasks remain in `PENDING` state indefinitely.

### Diagnostic Steps:
```bash
# 1. Check worker capacities and allocated loads
cloudx worker list

# 2. Inspect scheduler rejection reasons for task
cloudx task inspect <task-id>
```

### Root Causes & Remediation:
1. **Cluster capacity exhausted**:
   - *Fix*: Scale down unneeded workloads (`cloudx service scale <service> 0`) or join additional worker daemons (`cloudx-worker`).
2. **Task resource requests are unrealistically high**:
   - *Fix*: Lower `resources.cpu` and `resources.memory` values in service manifest to fit within worker node limits.

---

## 12. Summary Quick Reference Table

| Failure Scenario | Primary Diagnostic Tool | Common Root Cause | Quick Remediation |
| :--- | :--- | :--- | :--- |
| **Control Plane Unreachable** | `cloudx diagnose`, `nc -zv` | Daemon stopped or port 7000 busy | Run `cloudx server` / kill port occupant |
| **Worker Cannot Join** | `tail -n 50 worker.log` | Node ID collision / Firewall block | Assign unique `--node-id` |
| **Worker Node LOST** | `cloudx events`, `cloudx node list` | Heartbeat timeout (>30s) / Network partition | Restart worker / adjust heartbeat timeout |
| **Workload CrashLoop** | `cloudx service logs <service>` | Application exit, missing binary, OOM | Check logs, fix path, increase RAM |
| **Health Check Failure** | `curl -v http://127.0.0.1:<port>` | Missing initial delay / wrong probe path | Adjust `initialDelaySeconds` & probe URL |
| **Deployment Stuck** | `cloudx deployment inspect` | Canary health probe failure | Fix v2 bug or run `cloudx rollback` |
| **Rollback Failure** | `cloudx deployment history` | No revision history | Deploy explicit version manifest |
| **Volume Path Conflict** | `cloudx volume inspect` | Path traversal (`../`) / Lock contention | Sanitize mount path |
| **Port Conflict** | `cloudx network list` | Static port collision on same host | Use dynamic port allocation pool |
| **Insufficient Resources** | `cloudx worker list` | CPU / Memory request exceeds capacity | Add worker node / lower task requests |

---

*CloudX Troubleshooting & Operations Guide — Milestone 20 / Phase 75.*
