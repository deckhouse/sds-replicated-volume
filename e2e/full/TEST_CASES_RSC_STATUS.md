# E2E RSC Status/Phase Test Cases

End-to-end tests for rsc-controller phase and message reporting. Each test
creates or mutates a ReplicatedStorageClass and verifies the resulting
`.status.phase` and `.status.message`.

Non-obvious / non-trivial cases are marked with ⚡.

---

## 1. WaitingForStoragePool — non-existent LVG

**Phase=WaitingForStoragePool when a referenced LVMVolumeGroup does not exist.**

Setup: Create RSC with `replication: None`, `topology: Ignored`, referencing
a non-existent LVG name.

Assert:
- `phase=WaitingForStoragePool`.
- `message` mentions the missing LVG name.
- No StorageClass is created.

Cleanup: delete the RSC.

---

## 2. InsufficientNodes — not enough nodes for TransZonal topology

**Phase=InsufficientNodes when eligible nodes cannot satisfy zone requirements.**

Prerequisite: label 3 worker nodes with `topology.kubernetes.io/zone`
(`zone-a`, `zone-b`, `zone-c`).

Setup: Create RSC with `replication: ConsistencyAndAvailability`,
`topology: TransZonal`, `zones: [zone-a, zone-b, zone-x]`.
Zone `zone-x` has no matching nodes.

Assert:
- `phase=InsufficientNodes`.
- `message` mentions the unresolvable zone or insufficient node count.

Cleanup: delete the RSC, remove zone labels.

---

## 3. InvalidConfiguration — deprecated storagePool referencing missing RSP

**Phase=InvalidConfiguration when spec.storagePool names a non-existent RSP.**

Setup: Create RSC with `storagePool: "non-existent-rsp"`,
`replication: None`, `topology: Ignored`, and a valid LVG.

Assert:
- `phase=InvalidConfiguration`.
- `message` mentions the non-existent RSP.

Cleanup: delete the RSC.

---

## 4. Ready — no volumes

**Phase=Ready immediately after valid RSC is accepted.**

Setup: Create RSC with `replication: ConsistencyAndAvailability`,
`topology: Ignored`, referencing 3 valid LVGs (one per worker node).

Assert:
- `phase=Ready`.
- `status.volumes.total` is 0 (or nil).
- A corresponding StorageClass exists.
- `status.storagePoolName` is set.
- `status.configuration` is populated with resolved FTT/GMDR.

---

## 5. Ready — with volumes

**Phase=Ready persists when volumes are created and fully aligned.**

Prerequisite: RSC from test 4 is Ready.

Setup: Create a PVC with `storageClassName` matching the RSC name.
Wait for the PVC to be Bound and the RV to reach a healthy state.

Assert:
- `phase=Ready`.
- `status.volumes.total >= 1`.
- `status.volumes.aligned >= 1`.
- `status.volumes.staleConfiguration` is 0 (or nil).

---

## 6. RollingOut — replication change triggers volume rollout

⚡ **Phase=RollingOut is transient — must be captured quickly after patch.**

Prerequisite: RSC from test 5 with at least 1 aligned volume.

Action: Patch `replication` from `ConsistencyAndAvailability` to `Availability`.

Assert (immediately after patch):
- `phase=RollingOut` (may be transient).
- `status.configuration.failuresToTolerate` reflects the new value.
- `status.volumes.staleConfiguration >= 1` (volumes not yet updated).

Assert (after rollout completes):
- `phase=Ready`.
- `status.volumes.staleConfiguration` is 0 (or nil).
- `status.volumes.aligned` equals `status.volumes.total`.

---

## 7. PartiallyAligned — config rollout disabled

⚡ **Phase=PartiallyAligned when config divergence exists but rollout is NewVolumesOnly.**

Prerequisite: RSC from test 6 with all volumes aligned to the new config.

Action:
1. Patch `configurationRolloutStrategy` to `{type: NewVolumesOnly, rollingUpdate: null}`.
2. Patch `replication` to `Consistency`.

Assert:
- `phase=PartiallyAligned`.
- `status.volumes.staleConfiguration >= 1` (existing volumes have old config).
- `status.configuration` reflects the new replication setting.
- No automatic rollout occurs (staleConfiguration stays non-zero).

The controller must not roll out config changes when the strategy is
`NewVolumesOnly` — divergence is expected and reported, not auto-fixed.

---

## 8. PartiallyAligned — eligible nodes conflict with Manual resolution

⚡ **Phase=PartiallyAligned when a volume has replicas on non-eligible nodes
and conflict resolution is Manual.**

Prerequisite: RSC from test 7.

Action:
1. Patch `eligibleNodesConflictResolutionStrategy` to `{type: Manual, rollingRepair: null}`.
2. Remove a label (e.g. `test-b`) from 2 of 3 worker nodes.
3. Patch `nodeLabelSelector` to `{matchLabels: {test-b: ""}}`.

Assert:
- `phase=PartiallyAligned`.
- `status.volumes.inConflictWithEligibleNodes >= 1`.
- No automatic repair occurs (replicas stay on non-eligible nodes).

Cleanup: restore removed node labels.

The controller must not move replicas when Manual conflict resolution is set.
The divergence is reported but the user is expected to act.

---

## 9. Terminating — RSC deleted while volumes exist

⚡ **Phase=Terminating is transient while the finalizer holds.**

Prerequisite: RSC with at least 1 volume (PVC still exists).

Action: `kubectl delete rsc <name>`.

Assert (immediately after delete, before finalizer releases):
- `phase=Terminating`.
- RSC still exists (finalizer holds it).

Assert (after cleanup completes):
- RSC is fully deleted.

Cleanup: delete PVC, wait for PV cleanup.

---

## 10. InsufficientNodes via nodeLabelSelector narrowing eligible nodes

⚡ **Phase=InsufficientNodes (not PartiallyAligned) when nodeLabelSelector reduces
eligible nodes below the minimum required by FTT/GMDR.**

Setup: RSC with `replication: Availability` (FTT=1, GMDR=0, needs 3 nodes),
`topology: Ignored`, 3 LVGs on 3 nodes. RSC is Ready with 2 volumes.

Action:
1. Patch `eligibleNodesConflictResolutionStrategy` to `{type: Manual, rollingRepair: null}`.
2. Label only 2 of 3 nodes with `exclude-one-node=true`.
3. Patch `nodeLabelSelector` to `{matchLabels: {exclude-one-node: "true"}}`.

Assert:
- `phase=InsufficientNodes` (NOT PartiallyAligned).
- `message` says "FTT=1, GMDR=0 requires at least 3 nodes, have 2".

This is a subtle edge case: even though the intent was to test PartiallyAligned
(node conflict), reducing eligible nodes below the replication minimum makes the
pool unviable, which is a stronger error. The controller correctly prioritizes
InsufficientNodes over PartiallyAligned.

To test PartiallyAligned (nodes manual) properly, use a replication mode that
needs fewer nodes (e.g. `Consistency` needing 2 nodes) so that excluding 1 of 3
creates a conflict without making the pool insufficient.

Cleanup: remove `exclude-one-node` labels.

---

## 11. Volumes always report "aligned" — config divergence not yet detected

⚡ **RollingOut and PartiallyAligned (config) are not observable when RV does not
yet implement configuration sync tracking.**

Setup: RSC with `ConsistencyAndAvailability`, 2 RVs, Ready.

Action: Patch `replication` to `Consistency`.

Observed:
- `phase=Ready` immediately — no RollingOut phase.
- `status.volumes.aligned = 2` even though RV config hasn't been updated.

Root cause: The RSC controller counts volumes as aligned/stale based on
`rv.status.rscConfigurationGeneration` vs `rsc.status.configurationGeneration`.
If the RV controller does not yet stamp this field, all volumes appear aligned.

This test documents the current behavior. Once RV config sync is implemented,
tests 6 and 7 should observe RollingOut and PartiallyAligned respectively.

---

## 12. StorageClass not created by rsc-controller

**RSC controller does not create Kubernetes StorageClass objects.**

Setup: Create a valid RSC. Wait for Ready.

Assert:
- `status.storagePoolName` is set (RSP created).
- No Kubernetes StorageClass with name matching the RSC exists.
- No SC with label `storage.deckhouse.io/replicatedStorageClassName=<rsc-name>`.

Note: StorageClass creation is handled by a separate controller (old controller).
To create volumes for testing, use `ReplicatedVolume` directly (not PVC).

---

## 13. Re-enable RollingUpdate after NewVolumesOnly

**Phase returns to Ready when RollingUpdate is re-enabled and no divergence exists.**

Prerequisite: RSC with `configurationRolloutStrategy: NewVolumesOnly`.
All volumes happen to already match the current configuration (no actual stale volumes).

Action: Patch `configurationRolloutStrategy` to
`{type: RollingUpdate, rollingUpdate: {maxParallel: 5}}`.

Assert:
- `phase=Ready` (no divergence to roll out).
- `ConfigurationRolledOut=True`.

If volumes were actually stale, expect transient `phase=RollingOut` before Ready.

---

## 14. Terminating — finalizer holds until RVs are deleted

⚡ **RSC stays in Terminating phase until all RVs referencing it are deleted.**

Setup: RSC with 2 RVs, Ready.

Action: `kubectl delete rsc <name> --wait=false`.

Assert:
- `phase=Terminating` persists while RVs exist.
- RSC has `deletionTimestamp` but is not removed.

Action: Delete both RVs.

Assert:
- RSC disappears (finalizer released).

---

## Environment setup / teardown

**Zone labels** (needed for test 2):
- `p-karpov-debian-0` → `topology.kubernetes.io/zone=zone-a`
- `p-karpov-ubuntu-0` → `topology.kubernetes.io/zone=zone-b`
- `p-karpov-worker-0` → `topology.kubernetes.io/zone=zone-c`

Remove all zone labels after test 2 (or at global teardown).

**Node labels** (needed for tests 8, 10):
- For test 8: temporarily remove `test-b` from 2 worker nodes, restore after.
- For test 10: add `exclude-one-node=true` to 2 of 3 worker nodes, remove after.

**Volume creation**:
- RSC controller does not create Kubernetes StorageClass (see test 12).
- Use `ReplicatedVolume` directly (not PVC) to create volumes for testing.

**RSC naming convention**:
- Tests 1–3: separate RSCs (`test-rsc-bad-lvg`, `test-rsc-insufficient`, `test-rsc-invalid`), each created and deleted within the test.
- Tests 4–14: single RSC (`test-rsc`) that evolves through phases.

**Merge patch gotcha**:
- When changing `configurationRolloutStrategy.type` to `NewVolumesOnly`,
  must null out `rollingUpdate`: `{type: NewVolumesOnly, rollingUpdate: null}`.
- When changing `eligibleNodesConflictResolutionStrategy.type` to `Manual`,
  must null out `rollingRepair`: `{type: Manual, rollingRepair: null}`.
- Without explicit nulls, CRD XValidation rejects the patch.
