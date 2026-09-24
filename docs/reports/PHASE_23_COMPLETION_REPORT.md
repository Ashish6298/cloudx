# Phase 23 Completion Report: Service Definition

## Executive Summary
Phase 23 begins **Milestone 7 (Services and Reconciliation)** by establishing CloudX's declarative service specification format, parser, unit normalizer, and strict validator. It allows users and orchestrators to define multi-replica services with resource constraints, health check probes, restart policies, port mappings, and volume attachments.

---

## Service Definition Specification (`internal/spec/service_spec.go`)

### 1. Specification Schema
```yaml
version: "v1"
services:
  api:
    command: ./api
    args: ["--port", "8080"]
    environment:
      ENV: "production"
      PORT: "8080"
    working_dir: /app
    replicas: 3
    runtime: native  # "native" or "docker"
    resources:
      cpu: 1         # 1, 0.5, "500m", "2.0"
      memory: 512MB  # "512MB", "1GB", "256MiB", 536870912
    ports:
      - 8080
      - "9090:9090/tcp"
    restart_policy:
      type: always   # "always", "on-failure", "never"
      max_retries: 5
      backoff_period: "5s"
    health_check:
      type: http     # "process", "tcp", "http"
      path: /healthz
      port: 8080
      interval: "10s"
      timeout: "2s"
      failure_threshold: 3
    volumes:
      - "/host/data:/app/data:ro"
      - name: shared-vol
        target: /app/shared
```

---

## Key Deliverables Implemented

### 1. Robust Parser & Normalizers (`internal/spec/service_spec.go`)
- **`ParseConfig(data []byte)` & `ParseConfigFile(path string)`**: Decodes multi-service YAML manifests from in-memory buffers or disk files.
- **Resource Normalizers**:
  - `ParseCPU`: Decodes numeric floats, integers, and Kubernetes-style millicore strings (e.g. `500m` $\rightarrow$ `0.5`, `2` $\rightarrow$ `2.0`).
  - `ParseMemory` / `ParseMemoryString`: Decodes binary (`KiB`, `MiB`, `GiB`), decimal (`KB`, `MB`, `GB`), and raw bytes.
- **PortSpec & VolumeSpec Unmarshalers**: Supports concise integer forms (`8080`), string mappings (`8080:80/tcp`), volume mount strings (`/host:/target:ro`), and structured objects.

### 2. Strict Service Validation (`Validate() (*ParsedResources, error)`)
- Validates name regex (`^[a-zA-Z0-9][-a-zA-Z0-9_.]*$`).
- Enforces mandatory `command`.
- Validates non-negative replicas.
- Validates supported runtimes (`native`, `docker`).
- Validates restart policies (`always`, `on-failure`, `never`).
- Validates health check parameters (interval $\ge$ 100ms, timeout > 0, port ranges).
- Validates port bounds (1..65535) and protocols (`tcp`, `udp`).
- Validates volume target paths and volume names.

---

## Test Verification

Unit tests in [`internal/spec/service_spec_test.go`](../../internal/spec/service_spec_test.go) verified all parsing and validation rules:

| Test Case | Scenario Tested | Result |
|---|---|---|
| `TestParseConfig_FullExampleYAML` | Complete YAML with CPU, Memory, Ports, Volumes, Probes, Restarts | **PASS** |
| `TestParseConfigFile_FromDisk` | Reading and validating manifest files from filesystem | **PASS** |
| `TestServiceConfig_ValidationErrors` | Empty command, negative replicas, invalid runtimes, port ranges, units | **PASS** |
| `TestParseCPU_And_Memory` | Parsing millicores, GiB, MB, KB, bytes, decimal units | **PASS** |

All repository packages passed: **100% PASS** (22 packages).

---

## Readiness for Next Phase
- **Status**: **READY FOR NEXT PHASE** (Phase 24 — Service Deployment: `cloudx deploy`, `cloudx service list`, `cloudx service inspect`).
