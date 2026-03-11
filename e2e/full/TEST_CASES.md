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
