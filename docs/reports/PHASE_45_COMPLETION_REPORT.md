# Phase 45 — Volume and Scheduling Constraints: Completion Report

**Milestone:** 12 — Persistent Volumes  
**Phase:** 45 — Volume and Scheduling Constraints  
**Status:** ✅ COMPLETE  
**Date:** 2026-09-25  

---

## Objective

Introduce **storage-aware scheduling** so that a workload requiring a local volume
is always placed on the worker that owns that volume.

```
volume "db-data" exists on worker-2
↓
task requiring "db-data" MUST run on worker-2
↓
CloudX never schedules volume-dependent tasks onto incompatible workers
```

---

## Files Added / Modified

| File | Action | Description |
|---|---|---|
| `internal/scheduler/model.go` | **Modified** | Added `RequiredVolumes []string` to `TaskRequirements`; added `VolumeNames []string` to `WorkerCapacity`; added constraint #6 (volume affinity) to `CanFit` |
| `internal/scheduler/assignment.go` | **Modified** | Added `RequiredVolumes []string` to `TaskSpec`; fetch volumes from store in `Assign`, build `workerVolumeNames` map, populate `WorkerCapacity.VolumeNames`; propagate `Spec.RequiredVolumes → Requirements.RequiredVolumes` |
| `internal/scheduler/volume_scheduling_test.go` | **Created** | 12 unit tests for `CanFit` volume affinity and multi-worker scheduling |
| `internal/controlplane/volume_scheduling_integration_test.go` | **Created** | 7 integration tests through `AssignmentCoordinator` with SQLite state store |
| `README.md` | **Modified** | Phase 45 and Milestone 12 marked complete |

---

## Architecture: Storage-Aware Scheduling

### Data Flow

```
CreateVolume(cfg with WorkerID) → volumes table (WorkerID = worker-2)
                                          ↓
AssignmentCoordinator.Assign()
  ├─ store.Volumes().List()        → build workerVolumeNames map
  │     worker-2 → ["db-data"]
  │     worker-1 → []
  ├─ Build WorkerCapacity[]
  │     worker-1.VolumeNames = []
  │     worker-2.VolumeNames = ["db-data"]
  ├─ Propagate RequiredVolumes: Spec → Requirements
  └─ BasicScheduler.Schedule()
        CanFit(worker-1, req{RequiredVolumes:["db-data"]})
          → REJECT: "volume affinity unmet: task requires 'db-data' but worker-1 does not own it"
        CanFit(worker-2, req{RequiredVolumes:["db-data"]})
          → FEASIBLE ✓
        → ScheduleDecision{WorkerID: worker-2}
```

### Volume Ownership Rules

| Scenario | Ownership |
|---|---|
| Volume with `WorkerID = worker-2` | Pinned to `worker-2` only |
| Volume with `WorkerID = ""` (unbound) | Accessible from **all** workers (single-node default) |
| Task with no `RequiredVolumes` | No storage constraint — normal scoring applies |
| Task requiring volume not in any worker's set | **Scheduling fails** with explicit error |

### `CanFit` Constraint #6 — Volume Affinity

```go
if len(req.RequiredVolumes) > 0 {
    workerVolSet := make(map[string]bool, len(worker.VolumeNames))
    for _, vn := range worker.VolumeNames {
        workerVolSet[strings.ToLower(vn)] = true
    }
    for _, reqVol := range req.RequiredVolumes {
        if !workerVolSet[strings.ToLower(reqVol)] {
            reasons = append(reasons, fmt.Sprintf(
                "volume affinity unmet: task requires volume '%s' but worker '%s' does not own it",
                reqVol, worker.WorkerID,
            ))
        }
    }
}
```

Key design decisions:
- **Case-insensitive** matching prevents fragile name comparisons
- **All-or-nothing**: ALL required volumes must be on the same worker
- **Fails explicitly**: if volumes are split across workers, scheduling returns an informative error
- **Zero-cost for non-volume tasks**: `len(req.RequiredVolumes) == 0` short-circuits

### Two RequiredVolumes Entry Points

1. **`TaskRequirements.RequiredVolumes`** — set directly by callers (control plane, job execution)  
2. **`TaskSpec.RequiredVolumes`** — set in the task spec; automatically merged into `Requirements` during `Assign()` if Requirements doesn't already have volumes

---

## Test Results

### Scheduler Unit Tests (12 new)

```
=== RUN   TestCanFit_VolumeAffinity_NoRequirement               PASS (0.00s)
=== RUN   TestCanFit_VolumeAffinity_WorkerOwnsVolume             PASS (0.00s)
=== RUN   TestCanFit_VolumeAffinity_WorkerDoesNotOwnVolume       PASS (0.00s)
=== RUN   TestCanFit_VolumeAffinity_WorkerHasNoVolumes           PASS (0.00s)
=== RUN   TestCanFit_VolumeAffinity_MultipleVolumesAllPresent    PASS (0.00s)
=== RUN   TestCanFit_VolumeAffinity_MultipleVolumesOnesMissing   PASS (0.00s)
=== RUN   TestCanFit_VolumeAffinity_CaseInsensitive              PASS (0.00s)
=== RUN   TestCanFit_VolumeAffinity_CombinedWithOtherConstraints PASS (0.00s)
=== RUN   TestBasicScheduler_VolumeAffinity_TargetsCorrectWorker PASS (0.00s)
=== RUN   TestBasicScheduler_VolumeAffinity_NoCompatibleWorker   PASS (0.00s)
=== RUN   TestBasicScheduler_VolumeAffinity_ThreeWorkersPinned   PASS (0.00s)
=== RUN   TestBasicScheduler_NoVolumeRequirement_UsesNormalScoring PASS (0.00s)

PASS  internal/scheduler  0.368s
```

### Control Plane Integration Tests (7 new)

```
=== RUN   TestVolumeSchedulingIntegration_PinsToVolumeOwner             PASS (0.03s)
=== RUN   TestVolumeSchedulingIntegration_UnboundVolumeAccessibleToAll  PASS (0.02s)
=== RUN   TestVolumeSchedulingIntegration_NoCompatibleWorkerFails       PASS (0.02s)
=== RUN   TestVolumeSchedulingIntegration_SpecRequiredVolumesPathway    PASS (0.03s)
=== RUN   TestVolumeSchedulingIntegration_VolumeCreatedViaControlPlane  PASS (0.03s)
=== RUN   TestVolumeSchedulingIntegration_MultiVolumeAllOnSameWorker    PASS (0.03s)
=== RUN   TestVolumeSchedulingIntegration_VolumeSplitAcrossWorkersFails PASS (0.03s)

PASS  internal/controlplane  1.059s
```

**Full suite:** All 25 packages — ✅ PASS (0 failures)

---

## Acceptance Criteria: Status

| Criterion | Status |
|---|---|
| Volume-dependent task is always scheduled on the volume's worker | ✅ |
| Task is rejected (not silently misplaced) when no compatible worker exists | ✅ |
| Tasks without volume requirements continue to use normal scheduling | ✅ |
| Multi-volume tasks require ALL volumes on the same worker | ✅ |
| Split-volume scenario (vol-a on w1, vol-b on w2) fails explicitly | ✅ |
| Unbound volumes (no WorkerID) are accessible from all workers | ✅ |
| Full `ControlPlane.CreateVolume` → schedule pathway works | ✅ |
| Existing tests unaffected (regression-free) | ✅ |

---

## Milestone 12 Summary — Persistent Volumes (Complete)

| Phase | Title | Status |
|---|---|---|
| 43 | Volume Model | ✅ |
| 44 | Volume Lifecycle | ✅ |
| 45 | Volume and Scheduling Constraints | ✅ |

**Milestone 12 is fully complete.**

---

## Readiness for Milestone 13 — Service Discovery and Networking

**Phase 45 is ready to advance to Milestone 13 (Phase 46 — Service Registry).**

Milestone 12 delivered:
- Volume abstraction (model, state store, spec)
- Full lifecycle (create/list/inspect/delete) with directory provisioning
- Process injection via `CLOUDX_VOLUME_<NAME>` env vars
- Storage-aware scheduling — volume-dependent tasks are always pinned to the correct worker

Milestone 13 will build on the existing task/worker/registry infrastructure to allow services to communicate using logical identities without hardcoded worker addresses.
