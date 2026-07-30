# E2E Layout Convergence & r3→r2 Migration Test Cases

End-to-end tests for the r3→r2 auto-migration feature: layout comparison
(`MembershipLayoutConverged` condition + `status.membershipLayout`), the narrow convergence whitelist
(P1 retype Diskful→TieBreaker, P2 heal missing tie-breaker), tie-breaker creation
at formation and strict create-first tie-breaker replacement, the RSC aggregate
(`status.volumes`, `ConfigurationRolledOut`), the rollout strategies, and the
conservative RSC-update validation matrix. The migration is also exercised under
the `Local` volume access and the `TransZonal` topology.

The same area covers what happens when the layout diverges and stays diverged:
the manual recovery of a lost diskful replica in both directions (cases BE2E-1
and BE2E-2), the manual shrink of an excess replica (BE2E-3), and the alerting
pipeline that tells an operator to perform them —
`sds_rv_membership_layout_converged` → scrape → the
`D8ReplicatedVolumeLayoutDegraded` rule → a firing `ClusterAlert` (BE2E-4).

The migration trigger is always an **in-place edit of `rsc.spec.replication`** on an
existing ReplicatedStorageClass. `rv.spec.replicatedStorageClassName` is never
changed (that path is out of scope until verdict D-3). Replication modes map to
FTT/GMDR as: `ConsistencyAndAvailability` = FTT1/GMDR1 → **3D**;
`Availability` = FTT1/GMDR0 → **2D+1TB**.

Each case is titled with the text of its Ginkgo `It` verbatim; the line right
below the title carries the case identifier, its position in this document, the
opt-in class mark where it applies (`Disruptive`, `LongHaul`), and the
`Describe` container the spec lives in (a container usually holds more than one
case).

Run against a real cluster (`e2e/full`, Ginkgo). These specs are written and
compiled here; the actual run happens on the stand. Non-obvious cases are
marked with ⚡.

---

## migrates a 3D volume to 2D+1TB (one diskful retyped to tie-breaker)

E2E-1 · case 1 · ⚡ Disruptive · Describe: `Layout: r3->r2 migration by editing rsc.spec.replication`

**Editing `rsc.spec.replication` migrates a 3D volume to 2D+1TB via one in-place retype.**

Covers: decomposition T-2.0.3 (direction r3→r2); verifies blocks 1+2.

Labelled `Disruptive` (it writes to the raw DRBD device) — auto-injects `Serial`
+ lowest priority; skipped unless `E2E_ALLOW_DISRUPTIVE=true` or
`E2E_RUN_ALL=true`.

Given: a dedicated RSC with `replication: ConsistencyAndAvailability`, a 3D volume
(`MembershipLayoutConverged=True/Converged`, `status.membershipLayout=3D`), attached with I/O-safety
invariants active and a raw-device writer running on the attached node
(`Framework.StartIOWorkload`).

When: `rsc.spec.replication` is edited to `Availability`.

Then:
- `MembershipLayoutConverged` moves `False/Converging` → `True/Converged` (no flapping).
- Exactly one diskful is retyped to a tie-breaker; the composition is 2D+1TB
  (`status.membershipLayout=2D+1TB`, 2 Diskful + 1 TieBreaker members, still 3 members).
- ⚡ No `AddReplica` transition ever fires — the migration is a retype, not a
  resync.
- The retyped replica releases its backing LV (`status.backingVolume == nil`).
- On the RSC, `ConfigurationRolledOut=True/RolledOutToAllVolumes` and
  `status.volumes.aligned == 1`.
- ⚡ I/O continuity is proven on the data path, not only through conditions:
  verified device writes advance before, during and after the retype (the
  writer's sequence must move, without a stall or an early exit).
- I/O-safety invariants (quorum correct, never I/O-suspended) hold throughout.

---

## forms a new r2 volume directly as 2D+1TB (no post-formation doctoring)

E2E-2 · case 2 · Describe: `Layout: tie-breaker at formation and healing`

**A fresh r2 volume includes the tie-breaker at formation, not as a later DMTE step.**

Covers: decomposition T-1.1.2 (e2e part) and T-1.1.3 (auto-2D+1TB part); verifies block 3.

Given: an r2 storage class (FTT1/GMDR0 = `Availability`); at least 3 nodes so a
tie-breaker can be placed.

When: a volume is created.

Then:
- By `FormationComplete` the member composition is already 2 Diskful + 1
  TieBreaker. The `status.membershipLayout` string is read later, on the converged
  snapshot: no layout is published while formation runs.
- ⚡ Neither `AddReplica` nor `ChangeReplicaType` transitions appear — the
  tie-breaker is part of the formation membership, not a post-formation doctoring
  step (so DMTE and bitmap-bug B-1 are never entered).
- `MembershipLayoutConverged=True/Converged`; the volume serves I/O.

---

## heals a deleted tie-breaker via the P2 add-TB pattern

E2E-3 · case 3 · ⚡ Disruptive · Describe: `Layout: tie-breaker at formation and healing`

**Convergence recreates a manually deleted tie-breaker (P2 add-TB pattern).**

Covers: decomposition T-2.1.2 (P2 path); verifies block 2.

Labelled `Disruptive` because of the raw-device writer.

Given: a healthy 2D+1TB volume, attached on a diskful node with a raw-device
writer running from before the deletion.

When: the tie-breaker RVR is deleted (`kubectl delete rvr`).

Then:
- Convergence recreates a tie-breaker via P2; the composition returns to 2D+1TB
  (1 TieBreaker member, 3 members total), and the tie-breaker is a different one
  than the deleted replica — which is also what proves the observation is not the
  pre-deletion state.
- `MembershipLayoutConverged` returns to `True/Converged`.
- ⚡ Verified device writes keep advancing through the healing; the io-workload's
  historical gap check (every progress wait + the whole journal at cleanup) turns
  the writer into a continuous availability claim for the entire spec, not a pair
  of point probes.

---

## reports TransitionUnsupported for an r2->r3 upsize and leaves the layout intact

E2E-4 · case 4 · ⚡ Disruptive · Describe: `Layout: unsupported divergence is reported, not acted upon`

⚡ **An r2→r3 upsize is reported as `TransitionUnsupported` with exact arithmetic and triggers no action.**

Covers: decomposition T-2.2.1 (e2e part); negative case for future US-2.4; verifies block 1.

Labelled `Disruptive` (it writes to the raw DRBD device) — auto-injects `Serial`
+ lowest priority; skipped unless `E2E_ALLOW_DISRUPTIVE=true` or
`E2E_RUN_ALL=true`.

Given: an r2 storage class (`replication: Availability`), a 2D+1TB volume,
`MembershipLayoutConverged=True/Converged`, attached on one of its diskful nodes with a
raw-device writer running there (`Framework.StartIOWorkload`) — the writer is
started **before** the unsupported edit, so one process spans the whole mismatch
window.

When: `rsc.spec.replication` is edited to `ConsistencyAndAvailability` (upsize —
outside the convergence whitelist).

Then:
- `MembershipLayoutConverged=False/TransitionUnsupported`; the message contains the exact
  arithmetic `have 2D+1TB, want 3D`.
- The replica composition is untouched: no new RVR, still 2D+1TB (2 Diskful + 1
  TieBreaker), RVR count unchanged.
- ⚡ The volume keeps serving on the data path, not only in its conditions: the
  attachment stays `Ready=True/Ready` (the condition the CSI driver gates
  publishing on), the attached diskful replica stays `Ready=True/Ready`, and
  verified device writes advance while the mismatch is reported, without a stall
  or an early exit.
- The RSC aggregate is honestly not rolled out (`ConfigurationRolledOut=False`,
  `status.volumes.aligned == 0`).
- Reverting `replication` to `Availability` returns `MembershipLayoutConverged` to
  `True/Converged` — no stale latch, no flapping.
- Verified device writes advance again after the revert, from the same writer.

---

## migrates all volumes of a class with a single rsc.spec.replication edit

E2E-5 · case 5 · Describe: `Layout: r3->r2 migration by editing rsc.spec.replication`

**One `rsc.spec.replication` edit migrates every volume of the class.**

Covers: decomposition T-2.0.3 ("mass migration" part); verifies blocks 1+2.

Given: an r3 storage class with N volumes (N=3), one volume attached.

When: `rsc.spec.replication` is edited to `Availability` once.

Then:
- Every volume converges to 2D+1TB (`MembershipLayoutConverged=True/Converged`, 1 tie-breaker
  each); no `AddReplica`/resync on any volume.
- The RSC aggregate reaches `status.volumes.aligned == N` and
  `staleConfiguration == 0` without stalling; `ConfigurationRolledOut=True`.

---

## keeps I/O on quorum 2/3 while a diskful node reboots, then recovers

E2E-6 · case 6 · ⚡ Disruptive · Describe: `Layout: r2 volume survives a diskful node outage`

⚡ **A 2D+1TB volume keeps quorum (2/3) when a diskful node is rebooted, and recovers.**

Covers: decomposition T-1.1.3 ("disruptive" part) and the Epic 1 criterion; verifies block 3.

Labelled `Disruptive` — auto-injects `Serial` + lowest priority; skipped unless
`E2E_ALLOW_DISRUPTIVE=true` or `E2E_RUN_ALL=true`. Node reboot is performed via
`Framework.RebootNode` (`systemctl reboot` through the sds-node-configurator pod's
`nsenter`).

Given: a healthy 2D+1TB volume with a raw-device writer running on a surviving
diskful node (not the one to be rebooted). Quorum-survival invariants are armed
per replica on the surviving diskful and the tie-breaker — `NeverLoseQuorum`,
`NeverCritical`, `NeverIOSuspended` — plus `QuorumThresholdCorrect` on the RV.
The victim replica is deliberately NOT armed: it legitimately dips while its
node is down and briefly reports Critical while it rejoins.

When: the other diskful node is rebooted.

Then:
- ⚡ I/O is proven to advance AFTER DRBD declares the dead peer (the survivor
  reports `FullyConnected=False/PartiallyConnected`), not merely after the
  kubelet notices the reboot: quorum is only re-evaluated at the DRBD
  declaration, and a volume that freezes at that moment must fail here.
- I/O keeps flowing on quorum 2/3 (surviving diskful + tie-breaker): the
  attachment stays `Ready=True/Ready` (which subsumes `Attached=True`) and the
  surviving diskful replica stays `Ready=True/Ready`.
- ⚡ Verified device writes keep advancing while the node is down and after it
  returns; the writer tolerates a longer heartbeat gap (90s) around the outage
  but must never stall or exit — a stall longer than that anywhere in the run
  fails the spec even if writes resumed (historical gap check, enforced by the
  io-workload framework on every progress wait and over the whole journal at
  cleanup).
- After the node returns, its replica rejoins and reaches `Healthy`; the
  surviving replicas return to `Healthy` with no invariant violation recorded
  (the closing Awaits surface violations from snapshots no assertion looked
  at); the layout is intact (2D+1TB, `MembershipLayoutConverged=True/Converged`).

---

## rejects storage/topology changes and accepts a replication edit

E2E-7 · case 7 · Describe: `Layout: incompatible ReplicatedStorageClass updates are rejected`

**Storage/topology changes are rejected with a field-naming error; replication edits pass.**

Covers: decomposition T-2.0.1 (e2e negative) and the RSC part of US-2.3; verifies block 4.

Given: an r2 storage class with an existing 2D+1TB volume.

When / Then:
- Changing `spec.storage.type` (LVMThin→LVM with thinPoolName still set) is
  rejected by the CEL consistency guard: error contains `thinPoolName must not
  be specified when type is LVM`. CRD validation (CEL) runs before validating
  webhooks, so this probe never reaches the webhook — which is why the
  immutability guard gets its own probe below.
- Changing `spec.storage` composition (an entry's thinPoolName, schema-valid
  and consistent) is rejected by the update webhook: error contains
  `spec.storage is immutable once set`.
- Changing `spec.topology` is rejected: error contains
  `spec.topology is immutable` (CEL transition rule).
- After the rejections the RSC spec is unchanged (topology `Ignored`, storage
  type `LVMThin`, no bogus thinPoolName) and the volume layout is untouched
  (2D+1TB).
- A subsequent legitimate `spec.replication` edit is accepted.

---

## retypes a non-attached replica and keeps the attached node diskful

E2E-LOCAL · case 8 · ⚡ Disruptive · Describe: `Layout: r3->r2 migration with volumeAccess=Local`

⚡ **With `volumeAccess: Local` the retype must never demote the node the workload runs on.**

Covers: the `Local` guard of the retype candidate selection; verifies block 2.

Given: a dedicated RSC with `volumeAccess: Local` and `replication:
ConsistencyAndAvailability`, a 3D volume attached on one of its diskful nodes,
with a raw-device writer running there.

When: `rsc.spec.replication` is edited to `Availability`.

Then:
- The migration completes: 2D+1TB, `MembershipLayoutConverged=True/Converged`, retype in
  place (same RVR set, no `AddReplica`).
- ⚡ The attached node is still a **Diskful** member — asserted as an `Always`
  invariant for the whole migration window, not only at the end. A `Local` volume
  whose local replica became a tie-breaker would be cut off from its own data.
- The tie-breaker landed on some other node and released its backing LV.
- Verified device writes advance before, during and after the retype; the
  attachment stays `Attached`.

---

## migrates 3D in three zones to 2D+1TB with the tie-breaker in the third zone

E2E-TZ · case 9 · ⚡ Disruptive · Describe: `Layout: r3->r2 migration with TransZonal topology`

⚡ **A 3D volume spread over three zones migrates to 2D+1TB with the tie-breaker holding the third zone.**

Covers: verdict №19 (TransZonal retype); verifies blocks 1+2 under a zonal
topology.

Given: three usable diskful nodes, each labelled into its own **synthetic zone**
(`topology.kubernetes.io/zone`, unique per run). The spec is `Disruptive` and
`Serial` because it mutates node labels; a `DeferCleanup` registered **before the
first label write** restores the exact previous state of every touched node,
including deleting the label on nodes that had none. A TransZonal RSC over those
three zones holds a 3D volume — the actual zone spread is asserted **before** the
migration (one diskful per synthetic zone). The volume is attached and a
raw-device writer runs on the attached node.

When: `rsc.spec.replication` is edited to `Availability`.

Then:
- ⚡ A `ChangeReplicaType` transition is actually observed — the spec does not
  accept a volume that merely sits in `Converging` forever.
- The volume reaches 2D+1TB with `MembershipLayoutConverged=True/Converged`, retype in
  place (same RVR set).
- ⚡ Zone coverage is preserved: the two diskful members occupy two distinct
  zones, the tie-breaker holds the remaining third zone (and did not move
  between zones).
- Tie-break is intact at the DRBD level: `rv.status.datamesh.quorum == 2`, both
  diskful nodes report `quorum` in `drbdsetup status` and are connected to the
  tie-breaker peer.
- Verified device writes advance before, during and after the retype.

---

## replaces a deleted tie-breaker create-first when a free node exists

E2E-TB1 · case 10 · ⚡ Disruptive · Describe: `Layout: tie-breaker replacement`

**Deleting a tie-breaker starts a strict create-first replacement: the new one joins before the old one leaves.**

Covers: verdict №4 (strict create-first tie-breaker replacement); verifies block 2.

Labelled `Disruptive` because of the raw-device writer.

Requires **≥4 eligible nodes** (`require.MinNodes(2, 2)` — only two of them need
storage): three are occupied by the volume, the fourth hosts the replacement. On
smaller stands the spec skips.

Given: a healthy 2D+1TB volume; both diskful nodes have the tie-breaker in their
DRBD configuration (`drbdsetup show`) and report quorum; the volume is attached
on a diskful node with a raw-device writer running from before the deletion.

When: the tie-breaker RVR is deleted.

Then:
- ⚡ The **create-first window is actually observed**: at some point the datamesh
  holds 4 members with 2 tie-breakers (`status.membershipLayout=2D+2TB`) while the deleted
  one is still a member. The volume never drops to a single tie-breaker in
  between.
- The replacement is a different object — identified by **UID**, not name — and
  lands on a node other than the old tie-breaker's.
- ⚡ The replacement becomes operational on the data path: both diskful nodes are
  polled until DRBD reports it as a connected peer. The departure of the old
  tie-breaker is the deadline — finding it gone triggers one last fresh read of
  both nodes, and only "gone while the replacement is still not connected" fails
  the spec. (A departure is not a verdict by itself: membership comes from the
  informer and the peer state from an exec, so on a fast run the release can
  legitimately complete between two polls.)
- Only then does the old tie-breaker leave: its RVR (that UID) disappears and it
  is dropped from `status.datamesh.members`.
- The volume returns to a converged 2D+1TB whose tie-breaker is the replacement;
  on both diskful nodes DRBD now shows exactly the other diskful and the new
  tie-breaker as peers, mirrored by `rvr.status.peers`.
- ⚡ Tiebreak protection is never lost: an `Always` invariant requires every
  snapshot to hold two diskful members **and at least one tie-breaker**, so a
  bare 2D — where losing either diskful node freezes I/O — cannot occur even for
  one observation. The quorum value stays 2 for the whole window, including
  2D+2TB: tie-breakers do not vote, so the two diskful members are the only
  voters. That value is not restated by the case's own matcher — the framework's
  `QuorumThresholdCorrect` invariant checks the published quorum against the current
  voter count on every snapshot, which together with the invariant above is
  exactly "quorum is 2 throughout". DRBD-level quorum is re-verified at both
  ends.
- ⚡ Verified device writes keep advancing from before the deletion, once the
  replacement is operational, and at the end of the cycle; the io-workload's
  historical gap check (every progress wait + the whole journal at cleanup)
  makes the entire replacement window a continuous availability claim.

---

## keeps a terminating tie-breaker working when no node can host a replacement

E2E-TB2 · case 11 · ⚡ Disruptive · Describe: `Layout: tie-breaker replacement`

⚡ **With every eligible node occupied, the deleted tie-breaker keeps serving quorum and the volume says `CannotConverge`; the documented manual escape ends the deadlock.**

Covers: verdict №4 (the no-free-node branch) and validates the operator recipe in
`debug_and_problem_solving.md`; verifies blocks 2+6.

Runs on **any stand with ≥3 nodes** — it does not skip; instead it *creates* the
no-free-node situation.

Given: exactly three usable diskful nodes are carved out with a unique
`e2e.deckhouse.io/node-scope` label (`DeferCleanup` registered before the first
write, exact restore afterwards). A dedicated RSC pins both its storage (LVGs of
those nodes) and its `nodeLabelSelector` to that scope — an LVG list alone would
not be enough, since a tie-breaker needs no storage and could be placed anywhere.
⚡ The eligible set of the generated RSP is asserted to be **exactly** those three
nodes before anything is deleted: the spec fails rather than silently degrading
into the free-node case. A 2D+1TB volume fills the whole set, is attached, and a
raw-device writer runs on the attached node.

When: the tie-breaker RVR is deleted.

Then (phase 1 — the honest deadlock):
- The volume reports `MembershipLayoutConverged=False/CannotConverge` with the scheduler's
  own reason in the message (`cannot place a replacement`).
- A replacement RVR **is** created (create-first is strict) but stays unplaced,
  with `Scheduled=False/SchedulingFailed` and an empty `spec.nodeName`.
- ⚡ The old tie-breaker is `terminating but operational`: still a datamesh
  member, still present in the DRBD configuration of **both** diskful nodes, so
  quorum is still 3-way. Verified device writes keep advancing.
- ⚡ This state is **stable, not slowly converging**: an `Always` invariant holds
  it (`CannotConverge` + the old member present) across a long stretch of
  sustained I/O.

Then (phase 2 — the documented manual escape):
- Pre-conditions from the recipe are asserted first: both diskful replicas
  `Healthy` and `UpToDate`, `rvr.status.quorum == true` on both, the D↔D
  connection confirmed at the DRBD level, and I/O alive.
- The finalizer is removed from the terminating tie-breaker by hand (⚡ the one
  place in the suite where this is allowed — see `RUNNING.md`).
- The member becomes an orphan and is force-removed **once the peers stop seeing
  it**; the spec waits for that with a generous timeout instead of demanding it
  be instantaneous.
- The old tie-breaker is gone from `status.datamesh.members` and from the DRBD
  configuration of both diskful nodes.
- P2 places the pending replacement on the freed node; the volume returns to
  2D+1TB with `MembershipLayoutConverged=True/Converged`, and DRBD on both diskful nodes
  shows the other diskful plus the new tie-breaker.
- `rv.status.datamesh.quorum == 2` is an `Always` invariant of the whole spec,
  cross-checked at the DRBD level at both ends; the writer never stalls.

---

## holds the old volume at 3D, creates new ones as 2D+1TB, and releases the hold on RollingUpdate

E2E-NVO · case 12 · Describe: `Layout: NewVolumesOnly holds existing volumes`

**`configurationRolloutStrategy: NewVolumesOnly` holds existing volumes at their layout and says so; switching to `RollingUpdate` releases them.**

Covers: verdict №2 (the strategy used to be inert); verifies block 5.

Given: an r3 RSC with `configurationRolloutStrategy.type: NewVolumesOnly` and one
3D volume created before the edit.

When: `rsc.spec.replication` is edited to `Availability`, a second volume is
created, and only afterwards the strategy is switched to `RollingUpdate`.

Then:
- The old volume is **held, not silently stale**:
  `ConfigurationReady=False/NewerConfigurationHeld` and `status.membershipLayout=3D`. ⚡ The
  hold is asserted as an `Always` invariant across the formation of the second
  volume, not just sampled once.
- The RSC is honest about it: `ConfigurationRolledOut=False/ConfigurationRolloutDisabled`
  and `status.volumes.staleConfiguration == 1`.
- The volume created **after** the edit gets the new configuration: 2D+1TB with
  `ConfigurationReady=True/Ready`.
- After the switch to `RollingUpdate` the held volume migrates: 2D+1TB,
  `ConfigurationReady=True/Ready`, and the RSC reports
  `ConfigurationRolledOut=True/RolledOutToAllVolumes` with
  `status.volumes.aligned == 2`.

---

## restores an r2 volume to 2D+1TB by creating a diskful replica by hand

BE2E-1 · case 13 · ⚡ Disruptive · Describe: `Layout: manual recovery of a lost diskful replica`

⚡ **After an r2 volume loses a diskful replica, creating a `ReplicatedVolumeReplica`
by hand brings it back to 2D+1TB — the join goes through `diskful-q-up/v1`, the
exact edge bug B-1 used to break.**

Covers: the e2e part of the B-1 fix (the agent restores `--bitmap=yes` when a
peer leaves the diskless stage) and the r2 half of the r2/r3 parity claim.

Labelled `Disruptive` because of the raw-device writer — auto-injects `Serial` +
lowest priority; skipped unless `E2E_ALLOW_DISRUPTIVE=true` or `E2E_RUN_ALL=true`.
The replica loss itself is **not** disruptive: no finalizer is stripped and
nothing outside the volume's own `spec` is touched (see below).

Given: a converged 2D+1TB volume, attached on one of its two diskful nodes with a
raw-device writer running there. The victim is the OTHER diskful node — the
datamesh refuses to demote an attached voter.

When: the victim replica is lost through the legal simulation — the volume
configuration is temporarily switched to Manual `FTT=0/GMDR=0` (`topology:
Ignored`, the API rejects that pair under any other topology) so
`guardFTTPreserved` stops blocking a voter's departure, the RVR is deleted
through the ordinary API path, and the original Auto configuration is restored.
Then a diskful RVR is created by hand, with a free replica id and no placement.

Then:
- ⚡ The volume really shrinks first: `Deleted()` on the victim RVR (the
  controller released its own finalizer) and
  `MembershipLayoutConverged=False/TransitionUnsupported` with the exact
  arithmetic `have 1D+1TB, want 2D+1TB`. The arithmetic is what makes the
  assertion non-vacuous — inside the downgrade window the volume reports the very
  same reason for the *excess*.
- ⚡ Nothing is removed by convergence itself: an `Always` invariant pins the
  member composition for the whole downgrade window, so a future change that
  starts trimming volumes automatically cannot be mistaken for this spec's own
  delete.
- The kernel agrees: on the surviving diskful node the departed peer is gone from
  the DRBD configuration, only the tie-breaker peer is left, quorum is still held
  and the enforced threshold matches the published one (now 1).
- The layout metric reads `0` with `reason=TransitionUnsupported` on every
  controller pod — the series `D8ReplicatedVolumeLayoutDegraded` selects.
- ⚡ Verified device writes keep advancing while the volume runs on its single
  remaining copy.
- ⚡ The join runs `diskful-q-up/v1` (or its `-qmr-up` variant): odd→even voters,
  the `✦ → A → D∅+q↑ → D` path with the Access vestibule. Asserted on the
  transition's `planID`, which is set at dispatch and lives for the whole join —
  unlike a single step name. **Under B-1 the spec fails here**: the peer never
  regains its bitmap, the kernel refuses the connection and the replica never
  reaches `Healthy`.
- The new replica reaches `Healthy` with `backingVolume.state=UpToDate`; the
  volume is 2D+1TB again, `MembershipLayoutConverged=True/Converged`, quorum
  raised back to 2 and enforced at the DRBD level, with the new peer visible on
  the surviving node.
- The metric returns to `1`/`Converged`, so the alert can no longer fire.
- Verified device writes advance again after the recovery.

---

## restores an r3 volume to 3D by creating a diskful replica by hand

BE2E-2 · case 14 · ⚡ Disruptive · Describe: `Layout: manual recovery of a lost diskful replica`

**The r3 half of the parity claim: losing one of three diskful replicas is
repaired by hand through `diskful/v1`, which has no Access stage at all.**

Covers: the r3 recovery path, and guards it against collateral damage from the
B-1 fix (the fix changes agent behaviour for every volume, so the path that
always worked has to be pinned too).

Labelled `Disruptive` because of the raw-device writer; skipped unless
`E2E_ALLOW_DISRUPTIVE=true` or `E2E_RUN_ALL=true`.

Given: a converged 3D volume (no tie-breaker), attached on one diskful node with
a raw-device writer; the victim is a third, unattached diskful node.

When: the same legal loss simulation as BE2E-1, then a diskful RVR is created by
hand.

Then:
- The volume shrinks to 2D with `have 2D, want 3D`; the quorum threshold stays 2
  — with two voters left the volume now survives no further failure at all, which
  is exactly the situation the alert exists for.
- The metric reads `0`/`TransitionUnsupported`; device writes keep advancing.
- ⚡ The join runs `diskful/v1` (or `diskful-qmr-up/v1`): even→odd voters,
  `✦ → D∅ → D`, **no** Access vestibule and no quorum raise.
- The replica reaches `Healthy`/`UpToDate`, the volume is 3D and
  `True/Converged`, DRBD sees the recovered peer, the metric is back to `1`.

---

## reports an excess diskful without removing it and converges after a manual delete

BE2E-3 · case 15 · ⚡ Disruptive · Describe: `Layout: an excess replica is reported and removed by hand`

⚡ **A volume with more replicas than its configuration asks for is reported
honestly and is never trimmed automatically; deleting the excess RVR by hand is
the whole shrink procedure.**

Covers: the negative half of the convergence contract ("convergence never
removes a replica") and the operator's shrink recipe; pins the layout metric on
live data.

Labelled `Disruptive` because of the raw-device writer; skipped unless
`E2E_ALLOW_DISRUPTIVE=true` or `E2E_RUN_ALL=true`.

Given: a converged 2D+1TB volume, attached, with a raw-device writer running; the
layout metric reads `1`/`Converged`.

When: a third diskful RVR is created by hand (composition → 3D+1TB), and later
deleted by hand.

Then (phase 1 — the honest report):
- `MembershipLayoutConverged=False/TransitionUnsupported` with the exact
  arithmetic `have 3D+1TB, want 2D+1TB`.
- ⚡ Convergence takes no action: two `Always` invariants — the member
  composition is unchanged, and no `RemoveReplica` transition ever appears — hold
  across a long stretch of sustained verified I/O, so "nothing happened" is an
  observation over real elapsed time rather than a single sample.
- The layout metric drops to `0` with `reason=TransitionUnsupported` on every
  controller pod: exactly the series and label pair the alert rule selects.

Then (phase 2 — the manual shrink):
- The excess RVR is deleted with a plain delete. ⚡ **No configuration downgrade
  is needed here**, unlike BE2E-1/2: with 3 voters against `dMin=2` the FTT/GMDR
  guards allow the departure, which is why the recipe is "just delete it".
- The volume returns to 2D+1TB, `True/Converged`, quorum 2 cross-checked at the
  DRBD level; the metric returns to `1`/`Converged` and the writer never stalled.

---

## raises a firing D8ReplicatedVolumeLayoutDegraded for every volume that lost a diskful replica

BE2E-4 · case 16 · ⚡ LongHaul · Describe: `Layout: a degraded layout raises a ClusterAlert`

⚡ **The alerting pipeline end to end: collector → ServiceMonitor scrape → the
`D8ReplicatedVolumeLayoutDegraded` rule → Alertmanager → a firing `ClusterAlert`
naming the volume.**

Covers: `monitoring/prometheus-rules/replicated-volume-layout.yaml` on a live
cluster — the only part of the pipeline no unit test can reach.

**Two independent gates, both explicit `Skip`s with an instruction:**
1. `LongHaul` — the spec is skipped unless `E2E_ALLOW_LONG_HAUL=true` or
   `E2E_RUN_ALL=true` (a focused run bypasses it). The label raises the default
   `SpecTimeout` to 30min and gives the spec the highest priority.
2. `clusteralerts.deckhouse.io` absent — a stand without Deckhouse observability
   (Prometheus + alerts-receiver) never materializes an alert as an object, so
   the spec skips with that stated.

⚡ The spec is deliberately **not** `Disruptive`: that label would inject `Serial`
+ lowest priority and push the 15-minute wait to the end of the run, where it
overlaps nothing. It earns that by stripping no finalizer, touching no node label
and writing to no raw device — the loss simulation only edits the volume's own
`spec` (see BE2E-1).

Given: a converged 3D volume and a converged 2D+1TB volume; no attachments.

When: each loses one diskful replica through the same legal simulation. ⚡ Both
losses happen **before** either alert is awaited, so the two `for: 15m` windows
run at the same time and the spec costs one wait, not two.

Then:
- Both volumes report `False/TransitionUnsupported` with the exact arithmetic
  (`have 2D, want 3D` and `have 1D+1TB, want 2D+1TB`).
- ⚡ Precondition before the long wait: the metric reads `0` with
  `reason=TransitionUnsupported` for both volumes. It is what makes a failure
  diagnosable — if the series is present and no alert arrives, the defect is in
  the scrape, the rule or the receiver, never in the controller.
- For each volume a `ClusterAlert` eventually exists with
  `alert.name=D8ReplicatedVolumeLayoutDegraded`, `alert.labels.name=<volume>`,
  `alert.labels.reason=TransitionUnsupported`, `alert.severityLevel="6"` and
  `status.alertStatus=firing`. ⚡ The label match is what makes the assertion
  specific: "some layout alert is firing" would also pass on an alert about a
  volume this spec never touched.
- Resolution after the repair is **not** asserted: the retention and the
  resolve behaviour of the alerts-receiver are unverified. Check it by eye on the
  first real run and record the result here.
