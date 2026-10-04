<!--
How to read this file.

The new control plane of sds-replicated-volume (its own controller, without LINSTOR) has not been
released yet, but its documentation was already written into the module docs. Until the release,
that documentation is kept here: docs/newcontrolplane/ is not rendered on the documentation site,
the same way crds/newcontrolplane/ keeps the new control plane CRDs out of it.

The file collects fragments cut out of every document in docs/, grouped by the source document
(one level-2 heading per document). Every fragment is preceded by a comment that names:
  - Where: the section of the source document the fragment belongs to;
  - Replaced by: what the published document has in its place now (if anything) -- the text
    describing the released (LINSTOR-based) control plane.

When the new control plane is released, put every fragment back where its comment says,
replacing the text named in "Replaced by", and delete this file together with its _RU pair.
New documentation of the new control plane written before the release goes here as well, in
the same format, and always into both files: SUMMARY.md and SUMMARY_RU.md mirror each other.
Fragments keep the heading levels they have in the source document.
-->

## USAGE.md

<!--
Where: docs/USAGE.md, "Creating a ReplicatedStorageClass resource", the note after the paragraph on the controller creating the StorageClass
Replaced by: the note saying that the spec fields cannot be changed after creation
-->

> Please note that most fields of the `spec` section of the ReplicatedStorageClass resource are **immutable** after creation. Only the replication settings (`replication`, `failuresToTolerate`, `guaranteedMinimumDataRedundancy`), `configurationRolloutStrategy`, `eligibleNodesConflictResolutionStrategy` and `reclaimPolicy` (the StorageClass is recreated with the new policy) can be changed on an existing resource; changing any other field (`storage`, `topology`, `zones`, `volumeAccess`, `nodeLabelSelector`, etc.) is rejected on update.

<!--
Where: docs/USAGE.md, the body of "Updating the ReplicatedStorageClass resource", including its subsections
Replaced by: the paragraph saying that the StorageClass parameters cannot be changed
-->

Most `spec` fields are immutable after creation, and any attempt to change one is rejected with an error naming the field; changing such a field (for example `storage`, `topology`, `zones`, `volumeAccess`, `nodeLabelSelector`) requires recreating the resource. The replication settings and `reclaimPolicy` are mutable: editing `replication` enables the r3→r2 migration below, and editing `reclaimPolicy` makes the module recreate the StorageClass with the new policy.

##### Migrating volumes from three replicas (r3) to two replicas + tie-breaker (r2)

Editing `spec.replication` of an existing ReplicatedStorageClass changes the intended layout of **all** volumes of that class at once. To migrate from `ConsistencyAndAvailability` (three data replicas, layout `3D`) to `Availability` (two data replicas plus a diskless tie-breaker, layout `2D+1TB`):

1. Edit the class:

   ```shell
   kubectl patch replicatedstorageclass <RSC_NAME> --type=merge -p '{"spec":{"replication":"Availability"}}'
   ```

2. The controller migrates each volume in place: one diskful replica is retyped into a tie-breaker (no full resync, no data movement) and its logical volume is released. Watch progress per volume via the `LAYOUT` and `CONVERGED` print columns (`CONVERGED` needs `-o wide`):

   ```shell
   kubectl get replicatedvolume -o wide
   kubectl get replicatedvolume <RV_NAME> -o jsonpath='{.status.membershipLayout} {range .status.conditions[?(@.type=="MembershipLayoutConverged")]}{.status}/{.reason}{end}{"\n"}'
   ```

   A volume is migrated when `MembershipLayoutConverged` is `True/Converged` and `status.membershipLayout` is `2D+1TB`.

3. Watch the class-wide rollout via the `ConfigurationRolledOut` condition and the `status.volumes` counters:

   ```shell
   kubectl get replicatedstorageclass <RSC_NAME> -o jsonpath='{.status.volumes}{"\n"}'
   ```

   The rollout is complete when `ConfigurationRolledOut` is `True`, which happens exactly when `status.volumes.pendingObservation` and `status.volumes.staleConfiguration` are both `0`.

   Do not wait for `aligned` to reach `total`. Only volumes that take their configuration from the class participate in the rollout; a volume switched to `spec.configurationMode: Manual` carries its own configuration, so the class neither rolls anything out to it nor waits for it. Such volumes are still counted in `total`, and the class therefore completes the rollout with `aligned` below `total`.

**Requirements.** The `2D+1TB` layout needs a node for the tie-breaker in addition to the two diskful nodes: at least 3 nodes for `Ignored` topology, at least 3 zones for `TransZonal`, or at least 3 nodes in the volume's zone for `Zonal`. These match the `3D` requirements, so r3→r2 does not raise them.

**Limiting the rollout to new volumes.** By default (`configurationRolloutStrategy.type: RollingUpdate`) a configuration edit applies to every volume of the class. Set the strategy to `NewVolumesOnly` to apply it to newly created volumes only:

```shell
kubectl patch replicatedstorageclass <RSC_NAME> --type=merge -p '{"spec":{"configurationRolloutStrategy":{"type":"NewVolumesOnly","rollingUpdate":null}}}'
```

Volumes that already have a configuration then keep it. Such a volume observes the new configuration (so the class is not stuck waiting for it) but does not apply it, and reports:

```shell
kubectl get replicatedvolume <RV_NAME> -o jsonpath='{range .status.conditions[?(@.type=="ConfigurationReady")]}{.status}/{.reason}: {.message}{end}{"\n"}'
# False/NewerConfigurationHeld: ... has a newer configuration (generation N); the volume keeps its configuration (generation M) ...
```

Held volumes are counted in `status.volumes.staleConfiguration` of the class, and `ConfigurationRolledOut` becomes `False/ConfigurationRolloutDisabled`. The hold is deliberate and persists even if the held configuration later stops matching the cluster: to release a volume, switch the strategy back to `RollingUpdate` (all held volumes roll out through the normal path) or recreate the volume. Switching from `RollingUpdate` to `NewVolumesOnly` never rolls anything back — configuration already applied stays applied.

**Throttling the rollout.** Under `RollingUpdate`, `configurationRolloutStrategy.rollingUpdate.maxParallel` (default `5`) caps how many volumes of the class migrate at the same time:

```shell
kubectl patch replicatedstorageclass <RSC_NAME> --type=merge -p '{"spec":{"configurationRolloutStrategy":{"type":"RollingUpdate","rollingUpdate":{"maxParallel":2}}}}'
```

The volumes that still need the new configuration are ordered by name, and the leading ones fill the free slots — all of them at once, so with `maxParallel: 2` the first two migrate in parallel. A slot frees up when its volume reports `MembershipLayoutConverged=True/Converged`, and the next name in the order takes it. The ones still waiting keep their own configuration and report:

```shell
kubectl get replicatedvolume <RV_NAME> -o jsonpath='{range .status.conditions[?(@.type=="ConfigurationReady")]}{.status}/{.reason}: {.message}{end}{"\n"}'
# False/ConfigurationRolloutInProgress: ... rolls its configuration (generation N) out to at most 2 volume(s) at a time ...
```

Waiting volumes are counted in `status.volumes.staleConfiguration` of the class, so `ConfigurationRolledOut` stays `False/ConfigurationRolloutInProgress` until the whole class has migrated. Lowering `maxParallel` does not stop volumes that are already migrating — it only keeps new ones from joining. A volume that can never converge on the new configuration (see the limitations below) holds its slot indefinitely, and that is the point of the parameter: it bounds how many volumes a bad edit reaches, not only how fast a good one spreads.

**Limitations.**

- There is no automatic reverse path: editing `replication` back toward more replicas (r2→r3) is reported on each volume as `MembershipLayoutConverged=False/TransitionUnsupported` and performs no action until you intervene. The blocker is the *removal*, not the addition: r2 keeps a tie-breaker that r3 does not ask for, and the control plane never removes a replica. **Delete the tie-breaker replica** — with an odd diskful count no tie-breaker is required, so the datamesh releases it — and the missing third copy is then added automatically. Doing it the other way round (adding the copy first) also works but keeps the volume in `TransitionUnsupported` for the whole migration. (A volume that merely *lost* a diskful replica is a different case and needs no manual step at all: see below.)
- Reverting the edit while a volume is still migrating does not cancel a retype that is already in flight, and such a volume never reports `Converged` again on its own. Depending on the timing the volume ends up either at `2D+1TB` against the intended `3D` (`MembershipLayoutConverged=False/TransitionUnsupported`), or with a replica whose `spec.type` is stuck at `TieBreaker` while the layout still reads `3D` (`MembershipLayoutConverged=False/Converging`, with the affected replica named in the condition message). No data is lost in either case. In the second case, restore the replica with a single patch that sets `spec.type` back to `Diskful` and re-adds its backing-volume fields (`spec.lvmVolumeGroupName`, and `spec.lvmVolumeGroupThinPoolName` for a thin pool) with the values from `status.datamesh.members` of the volume.
- `eligibleNodesConflictResolutionStrategy.rollingRepair.maxParallel` is accepted but not implemented: repairing volumes that ended up on non-eligible nodes is not throttled. This is a different parameter from the configuration rollout `maxParallel` above, which is enforced.

<!--
Where: docs/USAGE.md, a section of its own between "Updating the ReplicatedStorageClass resource" and "Deleting the ReplicatedStorageClass resource"
Replaced by: nothing
-->

#### Recovering a lost replica

A volume that lost a diskful replica — the node was removed, the replica was force-removed, or the member left the datamesh for any other reason — repairs itself. The control plane creates a replacement `ReplicatedVolumeReplica`, the scheduler places it, the datamesh joins it and the data is resynchronized. No manual step is involved, and the sequence is the same whatever removed the member.

While the repair runs, the volume reports its progress in `MembershipLayoutConverged`, for example `False/Converging: creating diskful replica: have 2D, want 3D`. The `D8ReplicatedVolumeLayoutDegraded` alert stays silent throughout: it fires on `TransitionUnsupported` and `CannotConverge` only.

```shell
kubectl get replicatedvolume <RV_NAME> -o jsonpath='{.status.membershipLayout} {range .status.conditions[?(@.type=="MembershipLayoutConverged")]}{.status}/{.reason}: {.message}{end}{"\n"}'
```

Two things to expect:

- **One copy at a time.** A volume that lost two replicas is repaired over two rounds: the second replacement is created only once the first has joined. Capacity is not reserved in advance.
- **Nowhere to place it.** If no eligible node can host the replica, the volume reports `False/CannotConverge` with the scheduler's own summary and the alert does fire — this is the case that needs a human. The replica already exists and is placed as soon as a node or capacity appears; nothing has to be created by hand.

What is **not** repaired automatically, because the control plane never removes a replica or its data: a volume with more replicas than its configuration asks for, and a replica whose member is still in the datamesh but whose disk is gone (that is a replacement, not an addition).

## FAQ.md

<!--
Where: docs/FAQ.md, the last subsection of "Troubleshooting"
Replaced by: nothing
-->

### Diagnosing DRBD out-of-sync data or lost quorum (new control plane)

This section applies to clusters running the new control plane of `sds-replicated-volume` (its own controller, without LINSTOR). In this mode there is no LINSTOR client: DRBD resources are managed directly with `drbdsetup`, and the per-node agent runs in the `agent` DaemonSet (label `app=agent`). The `agent` container is distroless — it has no shell, so the `drbd*` binaries are invoked directly (`exec ... -- drbdsetup ...`, not `-- bash`).

The DRBD alerts (`D8DrbdPeerDeviceIsOutOfSync`, `D8DrbdDeviceHasNoQuorum`, `D8DrbdDeviceIsNotConnected`) carry two useful labels:

- `name` — the DRBD resource name; use it as the `<resource>` argument below;
- `conn_name` — the affected peer connection, shown as `peer-N`, where `N` is the peer node-id used by `drbdsetup` (confirm it in `drbdsetup status --verbose`, which prints `peer-N node-id:X`).

All commands below use `d8 k` as the kubectl entry point.

#### Inspect a resource

Find the `agent` Pod on the node and print the resource status (replace `<node>` and `<resource>`):

```shell
POD=$(d8 k -n d8-sds-replicated-volume get pod --field-selector=spec.nodeName=<node> -l app=agent -o name)
d8 k --as=system:sudouser -n d8-sds-replicated-volume exec -t $POD -c agent -- drbdsetup status <resource> --verbose --statistics
```

Note: `drbdadm status <resource>` does not work here (the new control plane does not write `/etc/drbd.d/*.res` files), always use `drbdsetup`.

#### Restore a lost quorum (`D8DrbdDeviceHasNoQuorum`)

A diskless Primary suspends its I/O when it loses quorum. A common cause is a diskful replica stuck resyncing near completion (`replication:SyncTarget` with `out-of-sync`/`received` not moving, sometimes a corrupt `done:` value far above 100%), so too few replicas are `UpToDate`.

Reconnect the stalled peer connection on the stalled (`Inconsistent`) node to restart the resync. `<peer-node-id>` is the node-id of the up-to-date source:

```shell
d8 k --as=system:sudouser -n d8-sds-replicated-volume exec -t $POD -c agent -- drbdsetup disconnect <resource> <peer-node-id>
```

The connection re-establishes automatically. When a second replica reaches `UpToDate`, quorum is restored and the Primary resumes I/O (`drbdsetup status <resource>` shows `quorum:yes`, `blocked:no`).

This is safe: the up-to-date source is untouched, an `Inconsistent` replica does not count toward quorum, and the Primary I/O is already suspended, so reconnecting introduces no new disruption and cannot lose data.

#### Clear out-of-sync data (`D8DrbdPeerDeviceIsOutOfSync`)

When both ends of a connection are `UpToDate` but residual `out-of-sync` bytes stay frozen, DRBD will not resync them on its own (matching generation UUIDs mean no resync is triggered). Reconnecting the peer forces a bitmap-based resync of exactly those extents:

```shell
d8 k --as=system:sudouser -n d8-sds-replicated-volume exec -t $POD -c agent -- drbdsetup disconnect <resource> <peer-node-id>
# out-of-sync should become 0:
d8 k --as=system:sudouser -n d8-sds-replicated-volume exec -t $POD -c agent -- drbdsetup status <resource> --statistics
```

If a small residual persists:

1. Run an online verification of that connection (reads both disks and reconciles matching/differing blocks):

   ```shell
   d8 k --as=system:sudouser -n d8-sds-replicated-volume exec -t $POD -c agent -- drbdsetup verify <resource> <peer-node-id> 0
   ```

2. If it still persists and the peer disk is `UpToDate`, fully re-mirror the peer copy. On the peer node (the one named by `conn_name`), invalidate its local copy — it becomes `Inconsistent` and does a full resync from an up-to-date replica. Quorum is held by the remaining up-to-date replicas during the resync:

   ```shell
   PEER_POD=$(d8 k -n d8-sds-replicated-volume get pod --field-selector=spec.nodeName=<peer-node> -l app=agent -o name)
   MIN=$(d8 k --as=system:sudouser -n d8-sds-replicated-volume exec -t $PEER_POD -c agent -- drbdsetup status <peer-resource> --verbose | grep -oE 'minor:[0-9]+' | head -1 | cut -d: -f2)
   d8 k --as=system:sudouser -n d8-sds-replicated-volume exec -t $PEER_POD -c agent -- drbdsetup invalidate $MIN
   ```

**Phantom counter.** If, after a full re-mirror, a Primary still reports a tiny `out-of-sync` toward a peer whose data was just re-read bit-for-bit, this is a stale accounting artifact on the Primary side, not a real divergence — the data on all replicas is identical. Reconnect, verify and re-mirror will not clear it, it resets only on a disruptive action (a failover or a reboot of the Primary node). For a live volume it is safe to leave it until the next maintenance window.

<!--
Where: docs/FAQ.md, the answer to "I am unable to update the fields in the ReplicatedStorageClass resource spec. Is this the expected behavior?"
Replaced by: the answer saying that changing the spec is not supported
-->

Partly. Most `spec` fields are immutable after creation, and updates to them are rejected with an error that names the field. The replication settings (`replication`, `failuresToTolerate`, `guaranteedMinimumDataRedundancy`), the `configurationRolloutStrategy`/`eligibleNodesConflictResolutionStrategy` fields and `reclaimPolicy` (the StorageClass is recreated with the new policy) **can** be changed on an existing resource. Editing `replication` is how volumes are migrated between layouts — for example from `ConsistencyAndAvailability` (`3D`) to `Availability` (`2D+1TB`); see the "Updating the ReplicatedStorageClass resource" section of the usage guide. All other fields (`storage`, `topology`, `zones`, `volumeAccess`, `nodeLabelSelector`, and so on) remain immutable — to change them, recreate the resource.

<!--
Where: docs/FAQ.md, the body of "How much of a volume is reserved for the superuser?"
Replaced by: the same section rewritten for the released control plane (annotation set on creation only)
-->

Nothing, by default: a volume of this module is formatted with `-m 0`, so the whole volume is available to the workload.

Where the classic 5% ext4 reserve is wanted — for example to keep a filesystem writable for a privileged process after a workload has filled it — annotate the `ReplicatedStorageClass` with the percentage to reserve:

```shell
d8 k annotate replicatedstorageclass <name> storage.deckhouse.io/ext4-reserved-percent=5
```

The value is a whole number of percent between 0 and 50; an invalid one is rejected by the validating webhook.

The reserve applies only to volumes created after the annotation was set — filesystems that already exist keep the reserve they were created with. Only the ext filesystems keep blocks for the superuser, so the annotation is ignored for XFS volumes.

Changing the annotation makes the controller recreate the StorageClass, because `parameters` of an existing StorageClass are immutable in Kubernetes. Existing volumes and PVCs are not affected.

On the legacy LINSTOR control plane the same annotation is honoured through the `FileSystem/MkfsParameters` property of the LINSTOR resource definition. The one exception there is the rescue path that formats a volume when LINSTOR did not: it always uses the module default of 0%, because the percentage does not reach it.
