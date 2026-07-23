# E2E Layout Convergence & r3→r2 Migration Test Cases

End-to-end tests for the r3→r2 auto-migration feature: layout comparison
(`LayoutConverged` condition + `status.layout`), the narrow convergence whitelist
(P1 retype Diskful→TieBreaker, P2 heal missing tie-breaker), tie-breaker creation
at formation, the RSC aggregate (`status.volumes`, `ConfigurationRolledOut`), and
the conservative RSC-update validation matrix.

The migration trigger is always an **in-place edit of `rsc.spec.replication`** on an
existing ReplicatedStorageClass. `rv.spec.replicatedStorageClassName` is never
changed (that path is out of scope until verdict D-3). Replication modes map to
FTT/GMDR as: `ConsistencyAndAvailability` = FTT1/GMDR1 → **3D**;
`Availability` = FTT1/GMDR0 → **2D+1TB**.

Run against a real cluster (`e2e/full`, Ginkgo). These specs are written and
compiled here; the actual run happens on the stand. Non-obvious cases are
marked with ⚡.

---

## 1. r3→r2 migration of a single volume (E2E-1)

**Editing `rsc.spec.replication` migrates a 3D volume to 2D+1TB via one in-place retype.**

Covers: decomposition T-2.0.3 (direction r3→r2); verifies blocks 1+2.
Spec: `Layout: r3->r2 migration by editing rsc.spec.replication` →
`migrates a 3D volume to 2D+1TB (one diskful retyped to tie-breaker)`.

Given: a dedicated RSC with `replication: ConsistencyAndAvailability`, a 3D volume
(`LayoutConverged=True/Converged`, `status.layout=3D`), attached with I/O-safety
invariants active.

When: `rsc.spec.replication` is edited to `Availability`.

Then:
- `LayoutConverged` moves `False/Converging` → `True/Converged` (no flapping).
- Exactly one diskful is retyped to a tie-breaker; the composition is 2D+1TB
  (`status.layout=2D+1TB`, 2 Diskful + 1 TieBreaker members, still 3 members).
- ⚡ No `AddReplica` transition ever fires — the migration is a retype, not a
  resync.
- The retyped replica releases its backing LV (`status.backingVolume == nil`).
- On the RSC, `ConfigurationRolledOut=True/RolledOutToAllVolumes` and
  `status.volumes.aligned == 1`.
- I/O-safety invariants (quorum correct, never I/O-suspended) hold throughout.

---

## 2. New r2 volume forms directly as 2D+1TB (E2E-2)

**A fresh r2 volume includes the tie-breaker at formation, not as a later DMTE step.**

Covers: decomposition T-1.1.2 (e2e part) and T-1.1.3 (auto-2D+1TB part); verifies block 3.
Spec: `Layout: tie-breaker at formation and healing` →
`forms a new r2 volume directly as 2D+1TB (no post-formation doctoring)`.

Given: an r2 storage class (FTT1/GMDR0 = `Availability`); at least 3 nodes so a
tie-breaker can be placed.

When: a volume is created.

Then:
- By `FormationComplete` the composition is already 2D+1TB (2 Diskful + 1
  TieBreaker).
- ⚡ Neither `AddReplica` nor `ChangeReplicaType` transitions appear — the
  tie-breaker is part of the formation membership, not a post-formation doctoring
  step (so DMTE and bitmap-bug B-1 are never entered).
- `LayoutConverged=True/Converged`; the volume serves I/O.

---

## 3. Deleted tie-breaker is healed (E2E-3)

**Convergence recreates a manually deleted tie-breaker (P2 add-TB pattern).**

Covers: decomposition T-2.1.2 (P2 path); verifies block 2.
Spec: `Layout: tie-breaker at formation and healing` →
`heals a deleted tie-breaker via the P2 add-TB pattern`.

Given: a healthy 2D+1TB volume.

When: the tie-breaker RVR is deleted (`kubectl delete rvr`).

Then:
- Convergence recreates a tie-breaker via P2; the composition returns to 2D+1TB
  (1 TieBreaker member, 3 members total).
- `LayoutConverged` returns to `True/Converged`; data and I/O are untouched.

---

## 4. Unsupported divergence is reported, not acted upon (E2E-4)

⚡ **An r2→r3 upsize is reported as `TransitionUnsupported` with exact arithmetic and triggers no action.**

Covers: decomposition T-2.2.1 (e2e part); negative case for future US-2.4; verifies block 1.
Spec: `Layout: unsupported divergence is reported, not acted upon` →
`reports TransitionUnsupported for an r2->r3 upsize and leaves the layout intact`.

Given: an r2 storage class (`replication: Availability`), a 2D+1TB volume,
`LayoutConverged=True/Converged`.

When: `rsc.spec.replication` is edited to `ConsistencyAndAvailability` (upsize —
outside the convergence whitelist).

Then:
- `LayoutConverged=False/TransitionUnsupported`; the message contains the exact
  arithmetic `have 2D+1TB, want 3D`.
- The replica composition is untouched: no new RVR, still 2D+1TB (2 Diskful + 1
  TieBreaker), RVR count unchanged.
- The RSC aggregate is honestly not rolled out (`ConfigurationRolledOut=False`,
  `status.volumes.aligned == 0`).
- Reverting `replication` to `Availability` returns `LayoutConverged` to
  `True/Converged` — no stale latch, no flapping.

---

## 5. Mass migration of a whole class (E2E-5)

**One `rsc.spec.replication` edit migrates every volume of the class.**

Covers: decomposition T-2.0.3 ("mass migration" part); verifies blocks 1+2.
Spec: `Layout: r3->r2 migration by editing rsc.spec.replication` →
`migrates all volumes of a class with a single rsc.spec.replication edit`.

Given: an r3 storage class with N volumes (N=3), I/O on a subset.

When: `rsc.spec.replication` is edited to `Availability` once.

Then:
- Every volume converges to 2D+1TB (`LayoutConverged=True/Converged`, 1 tie-breaker
  each); no `AddReplica`/resync on any volume.
- The RSC aggregate reaches `status.volumes.aligned == N` and
  `staleConfiguration == 0` without stalling; `ConfigurationRolledOut=True`.

---

## 6. r2 volume survives a diskful node outage (E2E-6, ⚡ Disruptive)

⚡ **A 2D+1TB volume keeps quorum (2/3) when a diskful node is rebooted, and recovers.**

Covers: decomposition T-1.1.3 ("disruptive" part) and the Epic 1 criterion; verifies block 3.
Spec: `Layout: r2 volume survives a diskful node outage` →
`keeps I/O on quorum 2/3 while a diskful node reboots, then recovers`.
Labelled `Disruptive` — auto-injects `Serial` + lowest priority; skipped unless
`E2E_ALLOW_DISRUPTIVE=true`. Node reboot is performed via
`Framework.RebootNode` (`systemctl reboot` through the sds-node-configurator pod's
`nsenter`).

Given: a healthy 2D+1TB volume with active I/O published on a surviving diskful
node (not the one to be rebooted).

When: the other diskful node is rebooted.

Then:
- I/O keeps flowing on quorum 2/3 (surviving diskful + tie-breaker): the RV stays
  `IOReady=True` and the attachment stays `Attached`.
- After the node returns, its replica rejoins and reaches `Healthy`; the layout is
  intact (2D+1TB, `LayoutConverged=True/Converged`).

---

## 7. Incompatible RSC updates are rejected (E2E-7)

**Storage/topology changes are rejected with a field-naming error; replication edits pass.**

Covers: decomposition T-2.0.1 (e2e negative) and the RSC part of US-2.3; verifies block 4.
Spec: `Layout: incompatible ReplicatedStorageClass updates are rejected` →
`rejects storage/topology changes and accepts a replication edit`.

Given: an r2 storage class with an existing 2D+1TB volume.

When / Then:
- Changing `spec.storage` is rejected: error contains
  `spec.storage is immutable once set` (webhook guard).
- Changing `spec.topology` is rejected: error contains
  `spec.topology is immutable` (CEL transition rule).
- After both rejections the RSC spec is unchanged (topology `Ignored`, storage
  type `LVMThin`) and the volume layout is untouched (2D+1TB).
- A subsequent legitimate `spec.replication` edit is accepted.
