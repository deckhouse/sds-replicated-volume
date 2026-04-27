# E2E Controller Test Cases

End-to-end tests for rv-controller + rvr-controller running against a real
cluster. Each test operates on ReplicatedVolume / ReplicatedVolumeReplica /
ReplicatedVolumeAttachment resources and verifies observable state (conditions,
status fields, resource existence).

Non-obvious / non-trivial cases are marked with ⚡.

---

## 1. AddReplica vestibule: backing volume preserved

⚡ **LLV is NOT deleted during the Access stage of diskful-q-up.**

Setup: RV (thin-r1, Auto mode) → 1D created automatically.
Create a second Diskful RVR → triggers AddReplica plan `diskful-q-up/v1`
(✦→A→D∅+q↑→D).

Assert during transition:
- The LLV for the new RVR is created once (Provisioning → Ready).
- `BackingVolumeReady` never shows `NotApplicable`.
- No LLV deletion event for the new RVR's LLV.
- The LLV has no `deletionTimestamp` at any point during the transition.

Assert after transition:
- Both RVRs are `Ready=True`, `Configured=True`, `BackingVolumeReady=True`.
- `BackingVolumeUpToDate=True` (sync completed).
- RV `effectiveLayout` reflects 2 voters.

During vestibule transitions the datamesh member is temporarily diskless
(Access) while the spec already targets Diskful. The controller must keep the
backing volume based on the target type, not the current member type.

---

## 2. SatisfyEligibleNodes for deleting RVRs

⚡ **SatisfyEligibleNodes reports True/Satisfied while RVR is deleting.**

Setup: RV with 2D (both Ready).
Delete one RVR → triggers RemoveReplica.

Assert during deletion (RVR has deletionTimestamp, Configured=PendingLeave):
- `SatisfyEligibleNodes` is `True/Satisfied` throughout the deletion.
- The condition message says "Replica satisfies eligible nodes requirements",
  not "ReplicatedStoragePool not found".

Assert after deletion:
- RVR is fully deleted.
- Remaining RVR still has `SatisfyEligibleNodes=True/Satisfied`.

The RSP eligibility lookup must not be skipped for deleting RVRs — the
condition should reflect actual eligibility status regardless of deletion state.

---

## 3. Multiattach disabled when maxAttachments decreases to 1

⚡ **DisableMultiattach dispatched after maxAttachments decrease + Detach.**

Setup: RV with 2D (both Ready). `maxAttachments=2`.
Create 2 RVAs (one per Diskful node) → EnableMultiattach → both attached.
Wait until `multiattach=true` and both RVAs are `Attached=True`.

Action:
1. Patch `maxAttachments=1`.
2. Create a third RVA on a different node.
   This RVA should be blocked: "Waiting for attachment slot".
3. Delete one of the two attached RVAs → Detach fires.

Assert after Detach completes:
- `multiattach=false` — DisableMultiattach was dispatched and completed.
- One RVA is `Attached=True`, two are not.

Assert that DisableMultiattach happens promptly after the Detach completes.

The multiattach toggle decision must consider `maxAttachments`, not only the
number of intended attachments. When `maxAttachments=1`, multiattach is never
needed regardless of how many RVAs exist.

---

## 4. Multiattach not disabled while potentiallyAttached > 1

⚡ **DisableMultiattach deferred while 2 members are still potentially Primary.**

Setup: same as test 3 up to the point where `maxAttachments=1` and
`multiattach=true` with 2 attached members.

Action:
1. Patch `maxAttachments=1` (do NOT delete any RVA yet).
2. Create a third RVA on a different node.

Assert immediately after maxAttachments=1:
- `multiattach` stays `true` — DisableMultiattach is NOT dispatched because
  both members are still potentially Primary.
- No DisableMultiattach transition in `datameshTransitions`.
- Third RVA is `Attached=False` (blocked by slot).

Disabling dual-Primary mode while two members may still be Primary would risk
data corruption. The dispatcher must defer Disable until at most one member is
potentially attached.

---

## 5. Multiattach enable skipped when maxAttachments=1

**EnableMultiattach not dispatched despite multiple RVAs when maxAttachments=1.**

Setup: RV with 2D, `maxAttachments=1`, `multiattach=false`.
Create 2 RVAs.

Assert:
- `multiattach` stays `false`.
- No EnableMultiattach transition in `datameshTransitions`.
- Only one RVA is `Attached=True`.
- The second RVA shows "Waiting for attachment slot".

With `maxAttachments=1` only one slot is available, so enabling dual-Primary
mode serves no purpose. The dispatcher must skip Enable entirely.

---

## 6. Orphaned RVR deletion when parent RV does not exist

**RVR with RVControllerFinalizer is deleted when RV is absent.**

Setup: Create a standalone RVR with `spec.replicatedVolumeName` pointing to a
non-existent RV.

Assert after creation:
- `RVControllerFinalizer` is present on the RVR (added by `reconcileOrphanedRVRs`).

Action: Delete the RVR.

Assert after deletion:
- RVR is fully deleted (not stuck with orphaned finalizer).

When the parent RV does not exist, the RV controller must still remove its
finalizer from deleting RVRs via the orphaned-RVR handler. Without this,
the RVR would hang in deletion forever.

---

## 7. RVS creation: Prepare → Sync → Ready across layouts

**Full RVS lifecycle on top of formed ReplicatedVolume layouts.**

Setup: RV formed via `SetupLayout` for each of: 1D, 2D, 3D, 2D+1TB, 2D multiattach.

Action: Create `ReplicatedVolumeSnapshot` pointing at the RV.

Assert during transitions:
- RVS goes through `Pending → Preparing → Synchronizing → Ready` without
  being kicked back to earlier phases.
- `prepare-mesh` completes (`match.RVS.PrepareComplete`).
- `sync-mesh` completes (`match.RVS.SyncComplete`).

Assert after completion:
- RVS is `ReadyToUse=true`, `NoActiveTransitions`, member count matches the
  number of diskful members of the source RV (`Type.HasBackingVolume()`).
- Every owned `ReplicatedVolumeReplicaSnapshot` is `ReadyToUse=true` and has
  a non-empty `status.snapshotHandle`.
- No orphan temporary `DRBDResource` / `DRBDResourceOperation` objects named
  after the RVS remain (`expectNoOrphanSyncResources`).

The snapshot flow is the primary DMTE-driven user-facing feature. All three
phases plus cleanup of temporary sync resources must succeed deterministically
across the supported RV layouts.

---

## 8. RVS deletion cascade at every lifecycle phase

⚡ **RVS and all children are garbage-collected regardless of deletion timing.**

Setup: 2D RV via `SetupLayout`.

Variants (each deletes RVS at a different phase):
1. During `Preparing` — delete immediately after `Create`, before prepare completes.
2. During `Synchronizing` — delete after `PrepareComplete` but before `SyncComplete`.
3. After `Ready` — delete once RVS is `ReadyToUse=true`.

Assert in all variants:
- The RVS object itself disappears from the API (`tkmatch.Deleted`).
- Every child `ReplicatedVolumeReplicaSnapshot` is removed.
- Every temporary `DRBDResource` created for `sync-mesh` with a matching name
  prefix is removed.
- Every temporary `DRBDResourceOperation` with a matching name prefix is removed.
- Parent RV/RVR objects are untouched (RVS deletion must never cascade upward).

Premature deletion used to leave orphan `DRBDResource`/`DRBDResourceOperation`
objects stuck with finalizers, blocking the next sync attempt. The cascade
invariant must hold for every phase, including the middle of DMTE transitions.

---

## 9. RVS sync resilience: recover from disappearing temporary DRBDResources

⚡ **`syncNeedsReset` watchdog recovers the snapshot after sync-mesh loses
  its DRBDResource objects.**

Setup: 3D RV via `SetupLayout` (to make `sync-mesh` long enough to race with).

Action:
1. Create RVS and wait for `Synchronizing` phase.
2. Wait until temporary `DRBDResource` objects exist for the sync step.
3. Force-delete them: strip finalizers, then `Delete` — simulating the
   agent-side regression where sync DRBDResources vanish mid-flight.

Assert:
- The RVS does NOT get stuck in `Synchronizing` forever.
- The controller's `syncNeedsReset` watchdog (2-minute threshold) fires,
  the sync step is retried and RVS eventually reaches `ReadyToUse=true`
  with `NoActiveTransitions`.

Regression guard for the observed incident where DRBDResources disappeared
mid-sync and the snapshot hung for > 10 minutes. The watchdog must detect the
discrepancy between expected and actual sync resources and reset the step
instead of waiting indefinitely.

---

## 10. RVS restore: new RV built from a ReplicatedVolumeSnapshot

**`spec.dataSource { kind: ReplicatedVolumeSnapshot }` forms a ready RV.**

Setup: For each of 1D / 2D / 3D — formed source RV via `SetupLayout`, then
`SetupRVS` on it so there is a `ReadyToUse` RVS to restore from.

Action: Create a new RV with `spec.dataSource.kind=ReplicatedVolumeSnapshot`
and `name=<rvs>` (`TestRV.DataSourceRVS`).

Assert:
- Restored RV reaches `FormationComplete` and `NoActiveTransitions`.
- All RVRs of the restored RV are `Healthy`.
- `status.datamesh.size` of the restored RV is ≥ source RV size.
- Member count matches the target layout.

Snapshot-restore is the main "bring data back" flow. The new RV must go
through normal Formation on the same RSC, pulling data from RVS, and land in
a fully healthy state — not a partial or "needs manual intervention" state.

---

## 11. RVS clone: new RV built from an existing ReplicatedVolume

**`spec.dataSource { kind: ReplicatedVolume }` forms a ready cloned RV.**

Setup: For each of 1D / 2D / 3D — formed source RV via `SetupLayout`.

Action: Create a new RV with `spec.dataSource.kind=ReplicatedVolume` and
`name=<rv>` (`TestRV.DataSourceRV`).

Assert:
- Cloned RV reaches `FormationComplete` and `NoActiveTransitions`.
- All RVRs of the cloned RV are `Healthy`.
- `status.datamesh.size` of the cloned RV is ≥ source RV size.
- Member count matches the target layout.

Volume cloning is implemented as an internal snapshot + restore pipeline
inside the controller. The resulting RV must behave identically to a
snapshot-restored RV from the user's perspective.
