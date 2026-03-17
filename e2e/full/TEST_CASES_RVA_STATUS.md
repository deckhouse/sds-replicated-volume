# E2E RVA Phase/Message Test Cases

End-to-end tests for ReplicatedVolumeAttachment phase and message reporting.
Each test creates or mutates RVA/RV/RSC resources and verifies observable
`.status.phase`, `.status.message`, and `.status.replicaReady`.

Non-obvious / non-trivial cases are marked with ⚡.

---

## 1. Pending — RV not found

**Phase=Pending when the referenced ReplicatedVolume does not exist.**

Setup: Create RVA referencing a non-existent RV name.

Assert:
- `phase=Pending`.
- `message` = `ReplicatedVolume not found; waiting for it to appear`.

Cleanup: delete the RVA.

---

## 2. Pending — formation in progress

⚡ **Phase=Pending while datamesh formation is still running.**

Setup: Create RV and RVA simultaneously (RVA created before formation
completes).

Assert (immediately after creation):
- `phase=Pending`.
- `message` mentions `formation is in progress`.

Note: formation may complete within 1–2 seconds on a healthy cluster. To
reliably catch this phase, the RVA must be created in the same shell command
as the RV, with no delay.

---

## 3. Attaching → Attached (healthy)

**Phase transitions from Attaching to Attached after formation completes.**

Prerequisite: RVA from test 2 (or freshly created after RV is Ready).

Assert (during transition):
- `phase=Attaching`.
- `message` matches `Attaching volume: .../... replicas confirmed revision ...`.

Assert (after settled):
- `phase=Attached`.
- `replicaReady=True`.
- `message` = `Volume is attached and ready to serve I/O on the node`.

---

## 4. Pending — slot full (maxAttachments=1)

**Phase=Pending when the attachment slot is already occupied.**

Prerequisite: RV with one RVA already `Attached` (maxAttachments=1 default).

Setup: Create a second RVA on a different node.

Assert:
- First RVA stays `Attached`.
- Second RVA is `Pending`.
- `message` = `Attaching volume is blocked: waiting for attachment slot (1/1 occupied)`.

---

## 5. Detach frees slot — second RVA attaches

**Deleting the attached RVA frees the slot for the pending one.**

Prerequisite: Two RVAs from test 4 — one Attached, one Pending.

Action: Delete the Attached RVA.

Assert:
- Deleted RVA disappears.
- Remaining RVA transitions to `Attached`.

---

## 6. Pending — VolumeAccess=Local, no Diskful on node

⚡ **Phase=Pending when volumeAccess is Local and the target node has no
Diskful replica.**

Prerequisite: RSC with `volumeAccess: Local`. RV with Diskful replicas on
worker nodes only.

Setup: Create RVA on a node that has no Diskful replica (e.g. master node).

Assert:
- `phase=Pending`.
- `message` mentions `waiting for replica` or `no Diskful replica on this node`.

Note: if the target node is also not eligible for the RSP, the message may
say `waiting for replica` rather than the more specific Local-mode message.
The exact wording depends on whether eligibility or Local-mode check fires
first.

Cleanup: delete the RVA, restore `volumeAccess: PreferablyLocal`.

---

## 7. Pending — node not eligible

**Phase=Pending when the target node is not in the RSP eligible nodes list.**

Prerequisite: RV is Ready. No other RVA occupies the slot.

Setup: Create RVA on a node that is not eligible (e.g. a master node without
an LVG in the RSC).

Assert:
- `phase=Pending`.
- `message` mentions `not eligible` or `waiting for replica`.

Note: to test this independently from slot-full, ensure the attachment slot
is free first (delete any existing Attached RVA before creating this one).

Cleanup: delete the RVA.

---

## 8. Pending — volume is terminating

**Phase=Pending when the referenced RV has a deletionTimestamp.**

Prerequisite: RVA exists and is Attached (or Pending).

Action: Delete the RV (`--wait=false`).

Assert:
- `phase=Pending`.
- `message` = `Attaching volume is blocked: volume is terminating`.

Cleanup: delete remaining RVAs, force-delete RV if stuck.

---

## 9. Terminating — RVA deleted while attached

⚡ **Phase=Terminating is transient while detach runs.**

Prerequisite: RVA is `Attached`. No other RVA occupies the slot (to ensure
this RVA actually reaches Attached before deletion).

Action: Delete the RVA.

Assert (during detach):
- `phase=Terminating`.
- `message` matches `Detaching volume: ...`.

Assert (after detach):
- RVA is fully deleted.

To reliably observe Terminating, the RVA must be Attached before deletion.
If it was only Pending, deletion is instant (no detach needed).

---

## 10. Slot contention prevents Attached state for new RVAs

⚡ **A newly created RVA stays Pending when slot is occupied, even if
the target node has a Diskful replica.**

Setup: RV with 3D (FTT=1/GMDR=1). One RVA already Attached on node A.
Create a second RVA on node B (which has a Diskful replica).

Assert:
- Second RVA is `Pending`, not `Attaching`.
- `message` = `waiting for attachment slot (1/1 occupied)`.

Slot check is evaluated before Local/eligibility checks. Even a perfectly
valid node will be blocked if no slot is available.

---

## 11. FTT/GMDR fields work after removing replication default

**RSC with FTT/GMDR (no replication field) creates valid configuration.**

Setup: Create RSC with `failuresToTolerate: 1`,
`guaranteedMinimumDataRedundancy: 1`, no `replication` field.

Assert:
- RSC is `Ready`.
- `status.configuration.failuresToTolerate=1`.
- `status.configuration.guaranteedMinimumDataRedundancy=1`.
- RV created under this RSC gets 3 Diskful replicas.

This test confirms the fix for the CRD default bug: removing the
`+kubebuilder:default` on `replication` allows FTT/GMDR to be used
without triggering the mutual-exclusivity XValidation.

---

## Environment notes

**Nodes:**
- NODE_1: `p-karpov-debian-0` (worker, has LVG)
- NODE_2: `p-karpov-ubuntu-0` (worker, has LVG)
- NODE_3: `p-karpov-worker-0` (worker, has LVG)
- MASTER: `p-karpov-master-0` (no LVG in RSC → not eligible)

**RSC:** `test-rsc` — FTT=1, GMDR=1, topology=Ignored, volumeAccess=PreferablyLocal.

**RV:** `test-rv` — 100Mi, replicatedStorageClassName=test-rsc.

**RVA naming:**
- `test-rva-orphan` (test 1, ephemeral)
- `test-rva-1` (tests 2–5, deleted in test 5)
- `test-rva-2` (tests 4–5, persists)
- `test-rva-local` (test 6, ephemeral)
- `test-rva-noelig` (test 7, ephemeral)
- `test-rva-3` (test 9, ephemeral)
- `test-rva-del` (test 8, ephemeral)

**Volume creation:** Use `ReplicatedVolume` directly (not PVC). The
rsc-controller does not create Kubernetes StorageClass objects.

**Slot contention:** Default `maxAttachments=1`. Most tests must be
sequenced carefully: ensure the slot is free before testing non-slot
scenarios (tests 6, 7, 9).

**Skipped scenarios (require destructive actions):**
- Quorum loss (Attached with degraded ReplicaReady) — requires stopping
  agents or disconnecting DRBD peers.
- Detach blocked by InUse — requires a running pod with a mounted PVC,
  which needs a Kubernetes StorageClass.
