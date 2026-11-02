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

> Please note that `topology`, `volumeAccess` and, once set, `systemNetworkNames` of the ReplicatedStorageClass resource are **immutable**: changing them is rejected on update. All other `spec` fields can be changed on an existing resource. A change to `storage`, `zones`, `nodeLabelSelector` or `eligibleNodesPolicy` moves the class to a new storage pool, and a change to `reclaimPolicy` makes the module recreate the StorageClass with the new policy (see "Updating the ReplicatedStorageClass resource").

<!--
Where: docs/USAGE.md, "Creating a ReplicatedStorageClass resource", the note after the example on validating the configuration before the StorageClass is created
Replaced by: the note saying that the StorageClass is not created if errors are found, and the error is saved to the status field
-->

> The configuration is validated when the resource is created or updated: an invalid one is rejected with an error. The module creates the StorageClass right away; whether the volumes of the class can already be created is reported in the `status` field of the ReplicatedStorageClass resource (see "Checking the state of a ReplicatedStorageClass resource").

<!--
Where: docs/USAGE.md, a section of its own between "Creating a ReplicatedStorageClass resource" and "Updating the ReplicatedStorageClass resource"
Replaced by: the sentence saying that the controller keeps the status field up to date
-->

#### Checking the state of a ReplicatedStorageClass resource

`kubectl get replicatedstorageclass` shows the phase of each class together with its FTT/GMDR and the number of volumes; `-o wide` adds the topology, the storage pool and a message explaining the phase. The conditions give the details:

```shell
kubectl get replicatedstorageclass <RSC_NAME> -o jsonpath='{.status.phase}: {.status.message}{"\n"}{range .status.conditions[*]}{.type}={.status}/{.reason}: {.message}{"\n"}{end}'
```

The class publishes the configuration its volumes are built from (`status.configuration`). The configuration of the current `spec` is **accepted** when `status.configurationGeneration` equals `metadata.generation`. Until then new volumes of the class wait for it, and existing volumes keep the previous configuration. While a change is not accepted, the `Ready` message starts with `Pending:` and names what is pending.

| Phase | `Ready` | What it means | What to do |
|---|---|---|---|
| `Ready` | `True/Ready` | The configuration is accepted and all volumes are aligned with it | Nothing |
| `RollingOut` | `True/Ready` | Volumes are being aligned with the configuration (a rollout, or volumes that have not observed it yet) | Wait; follow `ConfigurationRolledOut` and `status.volumes` |
| `PartiallyAligned` | `True/Ready` | Some volumes are not aligned, and every automatic fix for them is disabled (`NewVolumesOnly`, `Manual`) | Enable the rollout, or fix the volumes manually |
| `WaitingForStoragePool` | `False/WaitingForStoragePool` | The configuration of the current `spec` is not accepted: the storage pool is not ready. New volumes wait | Check `StoragePoolReady` and the ReplicatedStoragePool: missing or invalid LVMVolumeGroups, an invalid node selector |
| `InsufficientNodes` | `False/InsufficientEligibleNodes` | The configuration of the current `spec` is not accepted: the eligible nodes do not meet the FTT/GMDR/topology requirements. New volumes wait | Add nodes, or relax `nodeLabelSelector`/`zones`/the redundancy settings |
| `Degraded` | `False/Degraded` | The storage pool is not ready or its eligible nodes no longer meet the requirements since the configuration was accepted. The accepted configuration stays in effect: new volumes get it and existing ones keep it, but replicas can be placed only on the nodes the storage pool still offers, so a new volume may not be formed | Restore the LVMVolumeGroups or nodes of the pool; the cause is in `StoragePoolReady` or in the message. If a new volume is not formed, see its `Scheduled` and `MembershipLayoutConverged` conditions |
| `InvalidConfiguration` | `False/InvalidConfiguration` | The `spec` is invalid, for example `storage` is missing | Fix the `spec` |
| `InvalidConfiguration` | `False/StorageClassConflict` | A StorageClass of another provisioner already has the name of the class. The module leaves that StorageClass alone and does not create its own | Delete the other StorageClass, or recreate the class under another name |
| `Terminating` | — | The class is being deleted; the message names the number of volumes it still waits for | Delete the remaining volumes |

Conditions of the class:

- `Ready` — the summary above;
- `StoragePoolReady` — the `Ready` condition of the storage pool the class is moving to or is on (`status.storagePoolName`), with its reason and message;
- `ConfigurationRolledOut` — whether all volumes that take their configuration from the class are aligned with it (see "Updating the ReplicatedStorageClass resource");
- `VolumesSatisfyEligibleNodes` — whether all volumes have their replicas on eligible nodes; the message names the number of volumes that do not.

<!--
Where: docs/USAGE.md, the body of "Updating the ReplicatedStorageClass resource", including its subsections
Replaced by: the paragraph saying that the StorageClass parameters cannot be changed
-->

`topology`, `volumeAccess` and, once set, `systemNetworkNames` are immutable: an attempt to change one is rejected with an error naming the field, and changing it requires recreating the resource. Every other `spec` field is mutable:

- the replication settings (`replication`, `failuresToTolerate`, `guaranteedMinimumDataRedundancy`) change the intended layout of the volumes — for example the r3→r2 migration below;
- `storage`, `zones`, `nodeLabelSelector` and `eligibleNodesPolicy` make up the identity of the storage pool of the class, so a change moves the class to a new storage pool;
- `reclaimPolicy` makes the module recreate the StorageClass with the new policy;
- `configurationRolloutStrategy` and `eligibleNodesConflictResolutionStrategy` control how the changes reach existing volumes.

**Moving to a new storage pool.** Classes with the same `storage`, `zones`, `nodeLabelSelector`, `systemNetworkNames` and `eligibleNodesPolicy` share one automatically created storage pool (`status.storagePoolName`). After a change to any of them the class gets a new pool, and its volumes follow `configurationRolloutStrategy`: under `RollingUpdate` they are rolled out to the new pool (if their replicas are already on eligible nodes of the new pool, no data is moved), under `NewVolumesOnly` the existing volumes stay on the previous pool and only new volumes are created in the new one. The previous pool is released once no volume uses it. After a module update that changes how the pool name is built, existing classes stay on their pools; a class moves to the new pool on its first `spec` change of **any** field, including `reclaimPolicy` or `replication`, and its volumes follow `configurationRolloutStrategy` as above. A replica left on a node or LVMVolumeGroup that the new pool does not include is reported as a conflict (`VolumesSatisfyEligibleNodes`); the automatic repair of such conflicts is not implemented yet (see the limitations below), so move the replica manually.

While the new pool is not ready, or its nodes do not meet the FTT/GMDR/topology requirements, the change is not accepted: new volumes of the class wait for it, and the existing ones keep the previous configuration (see "Checking the state of a ReplicatedStorageClass resource").

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

**Requirements.** The `2D+1TB` layout needs a node for the tie-breaker in addition to the two diskful nodes: at least 3 nodes for `Ignored` topology, exactly 3 zones for `TransZonal` (see the zone count table in the `zones` field description of the ReplicatedStorageClass CRD), or at least 3 nodes in the volume's zone for `Zonal`. These match the `3D` requirements, so r3→r2 does not raise them.

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

**Growing a class (r2→r3).** Editing `replication` from `Availability` to `ConsistencyAndAvailability` needs no manual step either. Each volume of the class walks `2D+1TB → 3D+1TB → 3D` on its own: the control plane creates the third copy, waits for it to synchronize, and only then removes the tie-breaker that `r3` does not ask for — in that order, so the volume never stands on two copies of the data without a witness. `MembershipLayoutConverged` names the step it is on throughout (`creating diskful replica`, `waiting for a free resync slot`, `waiting for the diskful copies to synchronize before removing a surplus tie-breaker`, `tie-breaker ... is terminating`), and the `D8ReplicatedVolumeLayoutDegraded` alert stays silent: it fires on `TransitionUnsupported` and `CannotConverge` only. Until the third copy is synchronized, though, the volume is below its new intended redundancy (`Resilient=False/Degraded`), so a growth that takes longer than 30 minutes — a large volume, or a long wait for a free resync slot — fires the `D8ReplicatedVolumeNotResilient` ticket for that volume. It resolves on its own once the copy is synchronized, and at most `maxParallel` volumes of the class are in this state at once: the ones still waiting for a rollout slot keep their old configuration and stay `Resilient`.

The volume holds its rollout slot for the whole of it — including the resync — so `maxParallel` bounds how many volumes of the class are mid-growth at once. If the third copy cannot be placed, the volume reports `False/CannotConverge` with the scheduler's own summary and keeps its tie-breaker: a placement that fails never costs the volume its fault tolerance.

**Limitations.**

- Reverting the edit while a volume is still migrating does not cancel a retype that is already in flight. Depending on the timing the volume ends up either at `2D+1TB` against the intended `3D` — which is simply the growth described above and converges on its own — or with a replica whose `spec.type` is stuck at `TieBreaker` while the layout still reads `3D` (`MembershipLayoutConverged=False/Converging`, with the affected replica named in the condition message). That second one never reports `Converged` again by itself; no data is lost, and the replica is restored with a single patch that sets `spec.type` back to `Diskful` and re-adds its backing-volume fields (`spec.lvmVolumeGroupName`, and `spec.lvmVolumeGroupThinPoolName` for a thin pool) with the values from `status.datamesh.members` of the volume.
- `eligibleNodesConflictResolutionStrategy: RollingRepair` (the default) is accepted but not implemented: nothing moves replicas off nodes that are no longer eligible, and its `rollingRepair.maxParallel` is ignored. Such conflicts are reported in the `VolumesSatisfyEligibleNodes` condition and `status.volumes.inConflictWithEligibleNodes`, and the replicas have to be moved manually. The configuration rollout `maxParallel` above is a different parameter and is enforced.

<!--
Where: docs/USAGE.md, a section of its own between "Updating the ReplicatedStorageClass resource" and "Deleting the ReplicatedStorageClass resource"
Replaced by: nothing
-->

#### Recovering a lost replica

A volume that lost a diskful replica — the node was removed, the replica was force-removed, or the member left the datamesh for any other reason — repairs itself. The control plane creates a replacement `ReplicatedVolumeReplica`, the scheduler places it, the datamesh joins it and the data is resynchronized. No manual step is involved, and the sequence is the same whatever removed the member.

While the repair runs, the volume reports its progress in `MembershipLayoutConverged`, for example `False/Converging: creating diskful replica: have 2D, want 3D`. The `D8ReplicatedVolumeLayoutDegraded` alert stays silent throughout: it fires on `TransitionUnsupported` and `CannotConverge` only. `D8ReplicatedVolumeNotResilient` does fire if the repair takes longer than 30 minutes: until the new copy is synchronized the volume would not survive the next failure.

```shell
kubectl get replicatedvolume <RV_NAME> -o jsonpath='{.status.membershipLayout} {range .status.conditions[?(@.type=="MembershipLayoutConverged")]}{.status}/{.reason}: {.message}{end}{"\n"}'
```

Two things to expect:

- **One copy at a time.** A volume that lost two replicas is repaired over two rounds: the second replacement is created only once the first has joined. Capacity is not reserved in advance.
- **Nowhere to place it.** If no eligible node can host the replica, the volume reports `False/CannotConverge` with the scheduler's own summary and the alert does fire — this is the case that needs a human. The replica already exists and is placed as soon as a node or capacity appears; nothing has to be created by hand.

What is **not** repaired automatically, because the control plane never removes a *copy* or its data: a volume with more copies than its configuration asks for, and a replica whose member is still in the datamesh but whose disk is gone (that is a replacement, not an addition). A surplus *tie-breaker* is different — it carries no data, and the control plane removes it on its own once the copies match the configuration and all of them are synchronized.

## FAQ.md

<!--
Where: docs/FAQ.md, the last subsection of "Troubleshooting"
Replaced by: nothing
-->

### Diagnosing DRBD out-of-sync data or lost quorum (new control plane)

This section applies to clusters running the new control plane of `sds-replicated-volume` (its own controller, without LINSTOR). In this mode there is no LINSTOR client: DRBD resources are managed directly with `drbdsetup`, and the per-node agent runs in the `agent` DaemonSet (label `app=agent`). The `agent` container is distroless — it has no shell, so the `drbd*` binaries are invoked directly (`exec ... -- drbdsetup ...`, not `-- bash`).

The DRBD alerts (`D8DrbdPeerDeviceIsOutOfSync`, `D8DrbdDeviceHasNoQuorum`, `D8DrbdResourceIOSuspended`, `D8DrbdDeviceIsNotConnected`) carry two useful labels:

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

Only for a few fields. `topology`, `volumeAccess` and, once set, `systemNetworkNames` are immutable, and updates to them are rejected with an error that names the field — to change them, recreate the resource. All other fields **can** be changed on an existing resource. Editing `replication` (or `failuresToTolerate`/`guaranteedMinimumDataRedundancy`) is how volumes are migrated between layouts — for example from `ConsistencyAndAvailability` (`3D`) to `Availability` (`2D+1TB`); editing `storage`, `zones`, `nodeLabelSelector` or `eligibleNodesPolicy` moves the class to a new storage pool; editing `reclaimPolicy` recreates the StorageClass with the new policy. See the "Updating the ReplicatedStorageClass resource" section of the usage guide.

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
