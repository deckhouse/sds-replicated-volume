# Formation Concurrency Control

## Problem

When many ReplicatedVolume (RV) resources are created simultaneously (e.g., 500+
during bulk provisioning or load testing), all of them enter formation at the same
time. Formation is an I/O-heavy process: it creates ReplicatedVolumeReplica (RVR)
objects, triggers DRBD resource scheduling, disk allocation, and data
synchronization.

Without concurrency control, this causes a **conflict storm**:

1. **API server overload.** Each of the N formations creates/updates RVRs.
   The `rvr-scheduling-controller` concurrently updates the same RVRs.
   With 500+ RVs × 2 replicas each, the API server receives thousands of
   concurrent writes, many of which fail with `409 Conflict`.

2. **Formation timeout cascade.** Conflicts delay progress. Formations exceed
   their timeout (`defaultFormationRestartTimeout`) and restart — deleting all
   RVRs and starting over. This amplifies the write load on the API server.

3. **Disk I/O starvation.** Multiple formations on the same node compete for
   disk bandwidth during DRBD data bootstrap. Each volume gets near-zero
   throughput, causing further timeouts and restarts.

The result is a positive feedback loop where increasing load causes increasing
waste, and no formations complete.

## Solution

### Formation Semaphore

The `rv-controller` Reconciler maintains an in-process atomic counter
(`activeFormations`) that limits the number of concurrent formation reconciles.
Before entering `reconcileFormation`, each reconcile attempts to acquire a slot.
If all slots are occupied, the reconcile returns `RequeueAfter` with a jittered
delay (5–15 seconds) to avoid thundering herd effects.

```
reconcile(RV):
  if isFormationInProgress(rv):
    if activeFormations >= MaxConcurrentFormations:
      return RequeueAfter(jitter 5-15s)
    activeFormations++
    defer activeFormations--
    reconcileFormation(rv)
  else:
    reconcileNormalOperation(rv)
```

### Constants (controlleroptions)

| Constant                    | Value | Description                                           |
|-----------------------------|-------|-------------------------------------------------------|
| `MaxConcurrentReconciles`   | 20    | Total parallel reconcile goroutines per controller    |
| `MaxConcurrentFormations`   | 4     | Max parallel formations in rv-controller              |

The ratio (4 / 20 = 20%) ensures that bulk RV creation does not starve normal
operations (attach, detach, replica moves, quorum changes).

### Formation Restart Timeouts

Formation uses `reconcileFormationRestartIfTimeoutPassed` to detect stalls. If a
formation has been running longer than the timeout (measured from the transition's
`StartedAt`), it deletes all RVRs and restarts from scratch.

| Timeout                           | Value      | When                            |
|-----------------------------------|------------|---------------------------------|
| `defaultFormationRestartTimeout`  | 300s       | Preconfigure, connectivity, and most bootstrap stages |
| `dataBootstrapTimeout`            | 300s + size-based | Data synchronization stage (adds volume_size / 12.5 MB/s for thick LVM) |

The `dataBootstrapTimeout` uses `defaultFormationRestartTimeout` as its base
because the timeout check measures elapsed time from **formation start**, not
from the current step start. Without this, a formation that spent 4 minutes in
preconfigure would immediately timeout upon entering data bootstrap (since
`elapsed > 1 min`).

## Design Decisions

### Why in-process semaphore (not API-based)?

- **Zero cost.** No additional API calls. The counter is a single `atomic.Int32`.
- **Good enough.** The rv-controller runs as a single pod (leader election).
  There is no multi-instance coordination needed.
- **Survives restarts gracefully.** If the pod restarts, `activeFormations`
  resets to 0. Formations in progress will be re-queued by the workqueue
  and compete for slots again — no stale state.

### Why not per-node limits?

Per-node limits would be more precise (each node has independent disk I/O
bandwidth), but:

- At formation entry, target nodes are **not yet known** — scheduling happens
  inside formation.
- Would require either a cache-based List or a more complex tracking structure.
- The global semaphore is sufficient for the current scale (5–20 nodes,
  up to 500 RVs).

Per-node limits may be added as a future optimization if needed.

### Why 4 slots?

- With 5 nodes: 4 formations can run on different nodes without disk I/O
  contention.
- With 20 nodes: 4 is conservative but safe. Can be increased later.
- Throughput: at ~30–60s per formation, 4 slots ≈ 4–8 RVs/min ≈ 500 RVs
  in ~1–2 hours. Acceptable for bulk provisioning.

### Why 20 MaxConcurrentReconciles?

- Previous value (10) was insufficient for clusters with 500+ RVs.
- 20 goroutines provide enough parallelism for normal operations while
  keeping API server load manageable.
- Go goroutines are cheap (~8 KB stack each). 20 goroutines = ~160 KB.
- The real bottleneck is API server latency, not local concurrency.

## Files

| File | Change |
|------|--------|
| `controlleroptions/options.go` | `MaxConcurrentReconciles`, `MaxConcurrentFormations` constants |
| `rv_controller/reconciler.go` | `activeFormations` field, `tryAcquireFormationSlot`/`releaseFormationSlot` methods, gate before `reconcileFormation` |
| `rv_controller/controller.go` | Uses `controlleroptions.MaxConcurrentReconciles` |
| `rvr_scheduling_controller/controller.go` | Uses `controlleroptions.MaxConcurrentReconciles` |
