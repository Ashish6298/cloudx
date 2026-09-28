# CloudX Developer & Operations Manual

Welcome to the CloudX developer guide. This manual provides practical, real-world tutorials, operational instructions, complete working examples, and configuration specifications for engineers running CloudX locally, in production, or across distributed development topologies.

---

## Table of Contents
1. [Installation & Prerequisites](#1-installation--prerequisites)
2. [Configuration Management](#2-configuration-management)
3. [Cluster Initialization](#3-cluster-initialization)
4. [Starting Control Plane & Worker Joining](#4-starting-control-plane--worker-joining)
5. [Deployments & Rolling Updates](#5-deployments--rolling-updates)
6. [Dynamic Service Scaling](#6-dynamic-service-scaling)
7. [Finite Batch Workloads (Jobs)](#7-finite-batch-workloads-jobs)
8. [Persistent Storage Volumes](#8-persistent-storage-volumes)
9. [Networking & Service Discovery](#9-networking--service-discovery)
10. [Workload Logs & Aggregation](#10-workload-logs--aggregation)
11. [Cluster Audit Events](#11-cluster-audit-events)
12. [Deployment Rollbacks](#12-deployment-rollbacks)
13. [Diagnostics & Automated Health Checks](#13-diagnostics--automated-health-checks)

---

## 1. Installation & Prerequisites

### Prerequisites
- **Operating System**: Linux (Ubuntu 20.04+, Debian, Fedora, RHEL), macOS (11+), or Windows (10/11 / Server).
- **Go Toolchain**: Go 1.22+ (if compiling from source).
- **Zero CGO**: Pure Go implementation — no GCC, MinGW, or external C compilers required.

### Building from Source
```bash
# Clone the repository
git clone https://github.com/cloudx-org/cloudx.git
cd cloudx

# Build primary CLI and Worker binaries
go build -o bin/cloudx ./cmd/cloudx
go build -o bin/cloudx-worker ./cmd/cloudx-worker

# Add to system PATH
export PATH=$PATH:$(pwd)/bin
```

### Verifying Installation
```bash
cloudx version
```
*Output:*
```text
CloudX version v0.1.0 (commit: abc1234, built: 2026-09-28)
```

---

## 2. Configuration Management

CloudX resolves settings deterministically in the following order of precedence:
1. **CLI Flags** (`--node-id`, `--control-plane-addr`, `--config`, etc.)
2. **Environment Variables** (`CLOUDX_NODE_ID`, `CLOUDX_CONTROL_PLANE_ADDRESS`, etc.)
3. **YAML Configuration File** (`cloudx.yaml` in current working dir or `~/.cloudx/cloudx.yaml`)
4. **Built-in Defaults** (`127.0.0.1:7000`, `native` runtime, `~/.cloudx`)

### Example `cloudx.yaml`
```yaml
node:
  id: "node-master-01"
  name: "Master Control Node"
  labels:
    zone: "us-east-1a"
    role: "control-plane"

control_plane:
  address: "127.0.0.1:7000"

worker:
  address: "127.0.0.1:7001"
  port_range:
    min: 30000
    max: 32767

runtime:
  type: "native"

storage:
  path: "/var/lib/cloudx"

health:
  heartbeat_interval: "2s"
  suspect_timeout: "5s"
  unhealthy_timeout: "15s"
  lost_timeout: "30s"

logging:
  level: "info"
  format: "text"
```

### Inspecting and Validating Configuration
```bash
# Display resolved configuration
cloudx config show

# Validate configuration syntax and schema
cloudx config validate -c cloudx.yaml
```

---

## 3. Cluster Initialization

To initialize a new cluster state store (SQLite database schema, migrations, and default settings):

```bash
# Initialize local storage and state schemas
cloudx init
# or
cloudx cluster init --storage-path ~/.cloudx
```
*Output:*
```text
✓ Initialized CloudX storage directory at ~/.cloudx
✓ Created SQLite state store schema (~/.cloudx/cloudx.db)
✓ Cluster initialized successfully.
```

---

## 4. Starting Control Plane & Worker Joining

### Starting the Control Plane Daemon
```bash
# Run the control plane in foreground
cloudx server --config cloudx.yaml
```

### Starting Worker Daemons (Worker Joining)
Workers automatically join the cluster by registering with the control plane and streaming continuous heartbeats.

```bash
# Start Worker 1 (Default Port)
cloudx-worker --control-plane-addr 127.0.0.1:7000 --worker-addr 127.0.0.1:7001 --node-id worker-01

# Start Worker 2 (Secondary Worker on Port 7002)
cloudx-worker --control-plane-addr 127.0.0.1:7000 --worker-addr 127.0.0.1:7002 --node-id worker-02

# Start Worker 3 (Tertiary Worker on Port 7003)
cloudx-worker --control-plane-addr 127.0.0.1:7000 --worker-addr 127.0.0.1:7003 --node-id worker-03
```

### Inspecting Cluster Nodes
```bash
cloudx worker list
# or
cloudx node list
```
*Output:*
```text
NODE ID      NAME         ADDRESS          STATUS   TASKS   CPU USAGE   MEMORY USAGE
worker-01    worker-01    127.0.0.1:7001   READY    0       0.4%        32.1 MB
worker-02    worker-02    127.0.0.1:7002   READY    0       0.2%        31.8 MB
worker-03    worker-03    127.0.0.1:7003   READY    0       0.3%        32.0 MB
```

---

## 5. Deployments & Rolling Updates

CloudX supports declarative service manifests and progressive rolling deployments.

### Example Service Manifest (`api-service.yaml`)
```yaml
apiVersion: cloudx/v1
kind: Service
metadata:
  name: payment-api
  labels:
    tier: backend
    env: production
spec:
  replicas: 2
  template:
    version: "v1.0.0"
    command: "/usr/local/bin/payment-api"
    args: ["--port", "8080"]
    env:
      DATABASE_URL: "postgres://db.internal:5432/payments"
      LOG_LEVEL: "info"
    resources:
      cpu: "500m"
      memory: "256Mi"
    healthCheck:
      type: "http"
      path: "/healthz"
      port: 8080
      interval: "5s"
      timeout: "2s"
      failureThreshold: 3
    restartPolicy: "always"
```

### Deploying Services
```bash
# Apply deployment manifest
cloudx deploy -f api-service.yaml

# Inspect active services
cloudx service list
```

### Rolling Updates (Deploying v2)
```bash
# Upgrade service image/version with zero downtime
cloudx deploy payment-api:v2.0.0

# Monitor rolling deployment progress
cloudx deployment list
cloudx deployment inspect payment-api
```

---

## 6. Dynamic Service Scaling

Scale services dynamically up or down; the reconciler computes task placement and distributes replicas evenly across workers.

```bash
# Scale payment-api to 5 replicas
cloudx service scale payment-api 5

# Verify task distribution across workers
cloudx task list --service payment-api
```
*Output:*
```text
TASK ID                               NODE ID      SERVICE       STATE     PORT    RESTARTS   AGE
tsk-18d98709-0ceb2a84386a             worker-01    payment-api   RUNNING   30101   0          2m
tsk-18d98709-8b2f0c91aa60             worker-02    payment-api   RUNNING   30101   0          2m
tsk-18d98709-eeb9d1157460             worker-03    payment-api   RUNNING   30101   0          45s
tsk-18d98709-319628a3255e             worker-01    payment-api   RUNNING   30102   0          45s
tsk-18d98709-dac76f8458b7             worker-02    payment-api   RUNNING   30102   0          45s
```

---

## 7. Finite Batch Workloads (Jobs)

Jobs execute batch processes to completion, capture exit codes, support retries with exponential backoff, and terminate automatically upon success.

### Example Job Manifest (`migration-job.yaml`)
```yaml
apiVersion: cloudx/v1
kind: Job
metadata:
  name: db-migrate
spec:
  command: "/usr/bin/migrate"
  args: ["-path", "/migrations", "-database", "$DB_URL", "up"]
  maxRetries: 3
  timeout: "5m"
  backoff: "10s"
```

### Executing and Monitoring Jobs
```bash
# Run batch job from manifest
cloudx job run -f migration-job.yaml

# Run ad-hoc batch job directly via CLI
cloudx job run db-backup --command "/usr/local/bin/backup.sh" --args "--full"

# List batch jobs
cloudx job list

# Inspect job execution details and exit code
cloudx job inspect db-migrate

# Tail job logs
cloudx job logs db-migrate
```

---

## 8. Persistent Storage Volumes

Volumes provide persistent, host-isolated directories for workloads requiring durable storage.

```bash
# Provision a named persistent volume
cloudx volume create database-storage --size 10Gi

# List volumes
cloudx volume list

# Inspect volume details and host mount path
cloudx volume inspect database-storage

# Delete volume
cloudx volume delete database-storage
```

---

## 9. Networking & Service Discovery

CloudX manages dynamic port allocations and local service routing.

```bash
# List service endpoint routes
cloudx network list

# Inspect endpoints for a specific service
cloudx service endpoints payment-api
```
*Output:*
```text
SERVICE       ENDPOINT            NODE ID      STATUS    HEALTH
payment-api   127.0.0.1:30101     worker-01    ACTIVE    HEALTHY
payment-api   127.0.0.1:30101     worker-02    ACTIVE    HEALTHY
payment-api   127.0.0.1:30101     worker-03    ACTIVE    HEALTHY
```

---

## 10. Workload Logs & Aggregation

Streams non-blocking stdout and stderr from tasks across all workers.

```bash
# View recent logs for a service
cloudx service logs payment-api

# Follow/stream live logs in real-time
cloudx service logs payment-api --follow

# Tail logs for a specific task
cloudx task logs tsk-18d98709-0ceb2a84386a --lines 50
```

---

## 11. Cluster Audit Events

Inspect real-time and historical cluster events, lifecycle transitions, scheduler decisions, and failure notifications.

```bash
# List recent cluster events
cloudx events

# Filter events by service
cloudx events --service payment-api --limit 20
```
*Output:*
```text
TIMESTAMP            SEVERITY   REASON                 TARGET                  MESSAGE
2026-09-28 15:50:12  INFO       SERVICE_CREATED        service/payment-api     Service 'payment-api' created with 2 replicas
2026-09-28 15:50:13  INFO       TASK_SCHEDULED         task/tsk-0ceb2a84       Scheduled to node worker-01 on port 30101
2026-09-28 15:50:14  INFO       TASK_HEALTHY           task/tsk-0ceb2a84       Health check HTTP GET /healthz returned 200 OK
2026-09-28 15:52:00  INFO       SERVICE_SCALED         service/payment-api     Desired replicas updated: 2 -> 5
2026-09-28 15:53:10  WARN       TASK_FAILED            task/tsk-319628         Process exited with code 137 (SIGKILL)
2026-09-28 15:53:12  INFO       TASK_RESTARTED         task/tsk-319628         Auto-healing restarted task (Restart #1)
```

---

## 12. Deployment Rollbacks

Instantly roll back a problematic rollout to its previous known-good version.

```bash
# Roll back service to previous stable version
cloudx rollback payment-api

# Roll back to an explicit historical version
cloudx rollback payment-api --version 1
```

---

## 13. Diagnostics & Automated Health Checks

The diagnostic engine checks 9 core cluster subsystems to isolate operational issues.

```bash
# Run comprehensive diagnostic scan
cloudx diagnose
```
*Output:*
```text
[+] 1. Control Plane Reachability ......... PASS (127.0.0.1:7000 responds in 1.2ms)
[+] 2. Worker Node Connectivity ........... PASS (3/3 workers connected)
[+] 3. SQLite Database Integrity .......... PASS (WAL mode enabled, integrity_check OK)
[+] 4. Heartbeat Status ................... PASS (All nodes within 2s heartbeat window)
[+] 5. Scheduler Schedulability ........... PASS (3 nodes in READY state)
[+] 6. Orphaned Task Audit ................ PASS (0 orphaned tasks detected)
[+] 7. Deployment Health .................. PASS (All deployments in STABLE state)
[+] 8. Host Resource Pressure ............. PASS (CPU: 12%, Memory: 24% available)
[+] 9. Configuration Validation ........... PASS (Configuration syntax & permissions valid)

SUMMARY: Cluster is fully healthy. 9 checks passed, 0 warnings, 0 failures.
```

---

*CloudX Developer & Operations Manual — Milestone 20 / Phase 73.*
