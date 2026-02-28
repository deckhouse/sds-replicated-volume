# rvr_controller Performance Analysis

**Commit**: `ad52772e1ec8d071d48db46b95273bdf4fa53bae`
**Analysis Date**: 2026-02-01

This document provides a detailed complexity and memory allocation analysis of the `rvr_controller` reconciliation loop.

## Executive Summary

| Scenario | Cache Reads | API Writes | DeepCopies | Heap Allocs | Notes |
|----------|-------------|------------|------------|-------------|-------|
| **Steady-state (hot path)** | 4-6 | 0-1 | 1-2 | ~20-30 | Best case when everything is in sync |
| **Status-only update** | 4-6 | 1 | 2 | ~25-35 | Conditions/status fields changed |
| **Initial creation** | 4-5 | 2-3 | 2-4 | ~40-60 | Create LLV + DRBDR |
| **DRBDR update** | 4-6 | 1-2 | 3-5 | ~40-50 | Datamesh revision changed |
| **Deletion** | 4-6 | 2-4 | 3-6 | ~30-50 | Cleanup children, remove finalizer |

## Analysis Parameters

The analysis uses these parameters for a typical 3-5 replica datamesh:

| Parameter | Symbol | Typical Value | Description |
|-----------|--------|---------------|-------------|
| Peers | N | 2-4 | Number of peer replicas |
| System Networks | K | 1-2 | Number of DRBD networks |
| Conditions | C | 8-9 | Number of conditions on RVR |
| LLVs | L | 1 | Number of LVMLogicalVolumes per RVR |
| Paths per peer | P | 1-2 | Network paths per peer (= K) |

## I/O Operations

### Cache Reads (with DeepCopy unless noted)

| Operation | Function | Line | DeepCopy | Conditional |
|-----------|----------|------|----------|-------------|
| Get RVR | `getRVR` | 80 | Yes (~1KB) | Always |
| Get DRBDResource | `getDRBDR` | 85 | Yes (~2-3KB) | Always |
| List LLVs | `getLLVs` | 90 | Yes (~0.5KB/LLV) | Always |
| Get RV | `getRV` | 95 | Yes (~1-2KB) | If rvr != nil |
| Get RSP | `getRSPEligibilityView` | 109 | **No** (UnsafeDisableDeepCopy) | If node assigned |
| List Pods | `getAgentReady` | 2094 | **No** (UnsafeDisableDeepCopy) | In reconcileDRBDResource |
| Get Node | `getNodeReady` | 2100 | **No** (UnsafeDisableDeepCopy) | Only if agent not ready |

**Typical cache read cost**: 4-6 reads, ~5-8KB DeepCopy total

### API Writes

| Operation | Function | Line | When |
|-----------|----------|------|------|
| Patch RVR (main) | `patchRVR` | 1261 | Metadata out of sync |
| Patch RVR Status | `patchRVRStatus` | 185 | Status/conditions changed |
| Create LLV | `createLLV` | 1431 | LLV doesn't exist |
| Patch LLV (metadata) | `patchLLV` | 1471 | LLV metadata out of sync |
| Patch LLV (resize) | `patchLLV` | 1505 | LLV needs resize |
| Patch LLV (finalizer) | `patchLLV` | 1882 | Before deletion |
| Delete LLV | `deleteLLV` | 1888 | Cleanup |
| Create DRBDResource | `createDRBDR` | 2057 | DRBDR doesn't exist |
| Patch DRBDResource | `patchDRBDR` | 1973, 2081 | Finalizer removal or spec update |
| Delete DRBDResource | `deleteDRBDR` | 1979 | Cleanup |

## DeepCopy Analysis

### Implicit DeepCopy (via client.Get/List)

Objects fetched without `UnsafeDisableDeepCopy` are automatically deep-copied by controller-runtime:

| Object | Estimated Size | Fields Contributing to Size |
|--------|---------------|----------------------------|
| RVR | ~1KB | Status.Conditions (C × ~150B), Status.Peers (N × ~100B) |
| DRBDResource | ~2-3KB | Spec.Peers (N × ~200B), Status.Peers (N × ~150B), Spec.Paths |
| RV | ~1-2KB | Status.Datamesh.Members (M × ~200B) |
| LLV | ~0.5KB | Spec, Status |

### Explicit DeepCopy Calls

| Location | Object | Line | Scenario | Size |
|----------|--------|------|----------|------|
| Reconcile | `rvr.DeepCopy()` | 125 | Base for status patch | ~1KB |
| reconcileMetadata | `rvr.DeepCopy()` | 1258 | Base for main patch | ~1KB |
| reconcileBackingVolume | `intendedLLV.DeepCopy()` | 1467, 1503 | Base for LLV patch | ~0.5KB |
| reconcileLLVsDeletion | `llv.DeepCopy()` | 1880 | Base for finalizer removal | ~0.5KB |
| reconcileDRBDResource | `drbdr.DeepCopy()` | 1971, 2079 | Base for DRBDR patch | ~2-3KB |
| computeTargetDRBDRSpec | `drbdr.Spec.DeepCopy()` | 2518 | Copy existing spec | ~1.5KB |
| getRSPEligibilityView | `EligibleNode.DeepCopy()` | 3041 | Single node entry | ~0.3KB |

**Note**: DeepCopy at line 125 (`base = rvr.DeepCopy()`) is always executed if `rvr != nil`, even if no status changes occur.

## Heap Allocation Inventory

### Per-Reconcile Allocations (Always)

| Category | Source | Count | Est. Size |
|----------|--------|-------|-----------|
| flow.ReconcileFlow | `BeginRootReconcile` | 1 | 40B |
| flow.ReconcileFlow | `BeginReconcile` (metadata, backing-volume, drbd-resource) | 3 | 120B |
| flow.EnsureFlow | `BeginEnsure` (11 ensure functions) | 11 | 440B |
| context.WithValue | `storePhaseContext` per phase | 15 | ~1.5KB |
| Logger (logr.Logger) | `buildPhaseLogger` per phase | 15 | ~2KB |
| phaseContextValue | Stored in context | 15 | ~0.6KB |

**Subtotal (flow infrastructure)**: ~5KB per reconcile

### Conditional Allocations

#### Status Updates (ensureStatus* functions)

| Allocation | Location | Condition | Size |
|------------|----------|-----------|------|
| `[]ReplicatedVolumeReplicaStatusPeerStatus` | line 381 | Peers capacity changed | N × ~100B |
| `[]string` (ConnectionEstablishedOn) | line 433 | Per peer, paths changed | N × K × 16B |
| `ReplicatedVolumeReplicaStatusQuorumSummary` | line 861 | Always in ensureStatusQuorum | ~40B |
| `slices.Clone(addresses)` | line 327 | Addresses changed | K × ~80B |
| `ReplicatedVolumeReplicaStatusAttachment` | line 290 | If attached | ~40B |
| `ReplicatedVolumeReplicaStatusBackingVolume` | line 634 | If DRBDR has active config | ~100B |

#### DRBDR Spec Computation

| Allocation | Location | Condition | Size |
|------------|----------|-----------|------|
| `[]DRBDResourcePeer` | line 2594 | Computing target spec | N × ~200B |
| `[]DRBDResourcePath` | line 2649 | Per peer | N × K × ~50B |
| `slices.Clone(systemNetworks)` | line 2530 | Always in computeTargetDRBDRSpec | K × 16B |

#### DatameshRequest

| Allocation | Location | Condition | Size |
|------------|----------|-----------|------|
| `ReplicatedVolumeReplicaStatusDatameshRequest` | lines 2303, 2358, 2382, 2400, 2435 | Membership request exists | ~60B |

#### LLV/DRBDR Creation

| Allocation | Location | Condition | Size |
|------------|----------|-----------|------|
| `LVMLogicalVolume` (new) | `newLLV` | Creating LLV | ~500B |
| `DRBDResource` (new) | `newDRBDR` line 2485 | Creating DRBDR | ~2KB |

#### String Allocations (fmt.Sprintf)

The reconciler contains **35 `fmt.Sprintf` calls** for condition messages and error formatting. Most are in error paths or condition updates, contributing ~50-200B each when executed.

### Flow Package Allocations

Each `flow.Begin*` call allocates:
- `ReconcileFlow`/`EnsureFlow` struct: 24-32B
- `phaseContextValue` struct: ~40B
- `context.WithValue` overhead: ~100B
- Logger with name: ~100-200B
- `[]any` for kv arguments: 0-48B

**Per-phase overhead**: ~300-400B

## Scenario Breakdown

### Scenario 1: Steady-State (Hot Path)

When RVR exists and everything is in sync:

```
Reconcile
├── getRVR                    [Cache read + DeepCopy ~1KB]
├── getDRBDR                  [Cache read + DeepCopy ~2KB]
├── getLLVs                   [Cache read + DeepCopy ~0.5KB]
├── getRV                     [Cache read + DeepCopy ~1.5KB]
├── getRSPEligibilityView     [Cache read, UnsafeDisableDeepCopy + manual ~0.3KB]
├── reconcileMetadata         [Skip: isRVRMetadataInSync returns true]
├── rvr.DeepCopy()            [Explicit DeepCopy ~1KB]
├── reconcileBackingVolume    [Pure computation, no I/O]
├── reconcileDRBDResource     [Pure computation, no I/O]
│   ├── getAgentReady         [Cache read, UnsafeDisableDeepCopy]
│   └── (skip getNodeReady if agent ready)
├── 11× ensureStatus*         [Pure in-memory mutations]
└── patchRVRStatus            [API write if changed, else skip]
```

**Totals**:
- Cache reads: 5-6
- DeepCopy: ~6-7KB
- API writes: 0-1 (only if status changed)
- Heap allocations: ~20-30 (mostly flow infrastructure)

### Scenario 2: Initial Creation

When RVR is new and LLV + DRBDR need to be created:

```
Reconcile
├── getRVR                    [Cache read + DeepCopy]
├── getDRBDR                  [Cache read, returns nil]
├── getLLVs                   [Cache read, returns empty]
├── getRV                     [Cache read + DeepCopy]
├── getRSPEligibilityView     [Cache read]
├── reconcileMetadata
│   ├── rvr.DeepCopy()        [Explicit DeepCopy]
│   └── patchRVR              [API write: add finalizer]
├── rvr.DeepCopy()            [Explicit DeepCopy]
├── reconcileBackingVolume
│   ├── computeIntendedBackingVolume
│   ├── newLLV                [Allocate new LLV struct]
│   └── createLLV             [API write: create LLV]
├── reconcileDRBDResource
│   ├── computeTargetDRBDRSpec
│   │   └── make([]DRBDResourcePeer) [N × ~200B]
│   ├── newDRBDR              [Allocate new DRBDR struct]
│   ├── createDRBDR           [API write: create DRBDR]
│   └── getAgentReady         [Cache read]
├── 11× ensureStatus*         [In-memory mutations]
└── patchRVRStatus            [API write: update status]
```

**Totals**:
- Cache reads: 5
- DeepCopy: ~5KB
- API writes: 4 (patch RVR, create LLV, create DRBDR, patch status)
- Heap allocations: ~40-60 (includes new object structs)

### Scenario 3: Datamesh Revision Changed

When RV datamesh updates and DRBDR spec needs patching:

```
Reconcile
├── (standard loads)          [4 cache reads + DeepCopy]
├── reconcileMetadata         [Skip if in sync]
├── rvr.DeepCopy()            [Explicit DeepCopy]
├── reconcileBackingVolume    [Pure computation]
├── reconcileDRBDResource
│   ├── specMayNeedUpdate     [true: datamesh revision differs]
│   ├── computeTargetDRBDRSpec
│   │   ├── drbdr.Spec.DeepCopy() [Explicit DeepCopy ~1.5KB]
│   │   └── make([]DRBDResourcePeer) [N × ~200B]
│   ├── equality.Semantic.DeepEqual [Reflection comparison]
│   ├── drbdr.DeepCopy()      [Explicit DeepCopy for base]
│   └── patchDRBDR            [API write]
├── 11× ensureStatus*
└── patchRVRStatus            [API write]
```

**Totals**:
- Cache reads: 5-6
- DeepCopy: ~8-10KB (extra for DRBDR patch base)
- API writes: 2 (patch DRBDR, patch status)
- Heap allocations: ~40-50

### Scenario 4: Deletion

When RVR has DeletionTimestamp and only our finalizer remains:

```
Reconcile
├── getRVR                    [Cache read + DeepCopy]
├── getDRBDR                  [Cache read + DeepCopy]
├── getLLVs                   [Cache read + DeepCopy]
├── getRV                     [Cache read + DeepCopy]
├── (skip getRSPEligibilityView: rvrShouldNotExist=true)
├── reconcileMetadata         [Compute targetFinalizerPresent=false]
├── reconcileBackingVolume
│   ├── reconcileLLVsDeletion
│   │   ├── llv.DeepCopy()    [Per LLV]
│   │   ├── patchLLV          [API write: remove finalizer]
│   │   └── deleteLLV         [API write: delete]
│   └── return (no status updates if rvr=nil)
├── reconcileDRBDResource
│   ├── drbdr.DeepCopy()      [Explicit DeepCopy]
│   ├── patchDRBDR            [API write: remove finalizer]
│   └── deleteDRBDR           [API write: delete]
└── (continue to finalize RVR removal)
```

**Totals**:
- Cache reads: 4
- DeepCopy: ~5-7KB
- API writes: 3-5 (patch/delete LLV, patch/delete DRBDR, remove RVR finalizer)
- Heap allocations: ~30-50

## Complexity Analysis

### Time Complexity

| Operation | Complexity | Notes |
|-----------|-----------|-------|
| `ensureStatusPeers` | O(N × K) | Iterates peers and paths |
| `ensureConditionFullyConnected` | O(N × K) | Checks connection per peer per network |
| `computeTargetDRBDRPeers` | O(N log N) | Sorts peers by name |
| `findLLVByName` | O(L) | Linear search in LLV slice |
| `computeHasUpToDatePeer` | O(N) | Linear scan of peers |
| Condition helpers | O(C) | Linear scan of conditions array |

### Space Complexity

| Component | Complexity | Notes |
|-----------|-----------|-------|
| RVR Status.Peers | O(N) | One entry per peer |
| DRBDResource Spec.Peers | O(N × K) | Peer entries with paths |
| Flow context stack | O(depth) | ~15 phases max depth |

## Optimizations Applied

The controller already implements several optimizations:

1. **UnsafeDisableDeepCopy for read-only access**:
   - `getRSPEligibilityView` uses `UnsafeDisableDeepCopy` then manually copies only the needed `EligibleNode`
   - `getAgentReady` uses `UnsafeDisableDeepCopy` (only reads Pod.Status.Conditions)
   - `getNodeReady` uses `UnsafeDisableDeepCopy` (only reads Node.Status.Conditions)

2. **Reconciliation cache (`drbdrReconciliationCache`)**:
   - Stores `(DatameshRevision, DRBDRGeneration, RVRType)`
   - Skips expensive `computeTargetDRBDRSpec` + `DeepEqual` when cache matches

3. **In-place slice updates**:
   - `ensureStatusPeers` reuses existing slice capacity when possible
   - Only reallocates when capacity is insufficient

4. **Early returns**:
   - Guards in ensure functions return early when condition is not applicable
   - `isRVRMetadataInSync` / `isLLVMetadataInSync` check before copying

## Execution Time Estimates

### Latency Components

| Component | Typical Latency | Notes |
|-----------|-----------------|-------|
| Cache read (with DeepCopy) | 1-10 μs | In-memory, dominated by DeepCopy |
| Cache read (UnsafeDisableDeepCopy) | 0.1-1 μs | Near-zero, just pointer access |
| API write (Patch/Create/Delete) | 5-50 ms | Network RTT to API server |
| Pure computation | < 1 ms | CPU-bound, negligible |
| Flow infrastructure | ~0.1 ms | Context/logger setup |
| `equality.Semantic.DeepEqual` | 10-100 μs | Reflection-based comparison |

### Per-Scenario Execution Time

| Scenario | Best Case | Typical | Worst Case | Bottleneck |
|----------|-----------|---------|------------|------------|
| **Steady-state (no changes)** | ~0.5 ms | ~1 ms | ~3 ms | Cache reads + DeepCopy |
| **Status-only update** | ~6 ms | ~15 ms | ~50 ms | 1 API write |
| **Initial creation** | ~30 ms | ~60 ms | ~200 ms | 3-4 API writes |
| **DRBDR update** | ~15 ms | ~30 ms | ~100 ms | 1-2 API writes |
| **Deletion** | ~25 ms | ~80 ms | ~250 ms | 3-5 API writes |

### Time Breakdown: Steady-State (Hot Path)

```
Total: ~1 ms (typical)
├── getRVR              ~3 μs   (cache read + DeepCopy ~1KB)
├── getDRBDR            ~5 μs   (cache read + DeepCopy ~2KB)
├── getLLVs             ~2 μs   (cache read + DeepCopy ~0.5KB)
├── getRV               ~4 μs   (cache read + DeepCopy ~1.5KB)
├── getRSPEligibilityView ~1 μs (UnsafeDisableDeepCopy + manual copy ~0.3KB)
├── getAgentReady       ~0.5 μs (UnsafeDisableDeepCopy)
├── rvr.DeepCopy()      ~3 μs   (explicit, ~1KB)
├── reconcileMetadata   ~1 μs   (isInSync check, skip)
├── reconcileBackingVolume ~50 μs (compute*, no I/O)
├── reconcileDRBDResource  ~100 μs (compute*, no I/O, skip spec compare via cache)
├── 11× ensureStatus*   ~200 μs (condition checks + mutations)
├── flow overhead       ~100 μs (15 phases × context/logger)
└── patchRVRStatus      SKIP    (no changes detected)
```

### Time Breakdown: Initial Creation

```
Total: ~60 ms (typical)
├── Cache reads         ~15 μs
├── reconcileMetadata
│   ├── rvr.DeepCopy()  ~3 μs
│   └── patchRVR        ~15 ms  (add finalizer)
├── reconcileBackingVolume
│   ├── computeIntended ~20 μs
│   ├── newLLV          ~5 μs
│   └── createLLV       ~15 ms
├── reconcileDRBDResource
│   ├── computeTarget   ~50 μs
│   ├── newDRBDR        ~10 μs
│   └── createDRBDR     ~15 ms
├── 11× ensureStatus*   ~200 μs
└── patchRVRStatus      ~15 ms
```

### Factors Affecting Execution Time

1. **API Server Load**: Writes can vary 5-200ms depending on API server queue depth
2. **Object Size**: Larger DRBDR (many peers) increases DeepCopy time linearly
3. **Network Latency**: Controller-to-API-server RTT directly impacts write latency
4. **etcd Performance**: Under high load, write latency increases significantly
5. **Cache Staleness**: No impact on read latency (reads always from in-memory cache)

### Throughput Estimate

For a single worker (default):

| Scenario | Reconciles/sec | Notes |
|----------|----------------|-------|
| Steady-state only | ~500-1000 | Cache-bound, no API writes |
| Mixed workload | ~30-50 | Dominated by API write latency |
| Burst creation | ~15-20 | Multiple API writes per reconcile |

With multiple workers (`--max-concurrent-reconciles`), throughput scales linearly for independent RVRs until API server becomes bottleneck.

## Potential Optimization Opportunities

1. **Avoid `rvr.DeepCopy()` when no status changes expected**:
   - Line 125 always copies even in steady-state
   - Could defer until first mutation detected

2. **Pool flow context allocations**:
   - `phaseContextValue` and logger allocations could be pooled
   - ~5KB per reconcile from flow infrastructure

3. **Batch condition updates**:
   - 11 ensure functions each do `obju.SetStatusCondition` (linear scan)
   - Could collect all changes and apply once

4. **Pre-size slices more accurately**:
   - `computeTargetDRBDRPeers` pre-sizes to `len(datamesh.Members)-1`
   - Peers slice in RVR status could be similarly pre-sized
