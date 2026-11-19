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
Where: docs/USAGE.md, a new section immediately before "Additional features for applications"
Replaced by: nothing; this section is withheld until the new control plane is released
-->

### Selecting a replication network (new control plane)

With `settings.newControlPlane: true`, select the replication network when creating
a ReplicatedStorageClass using `spec.systemNetworkNames`. The list accepts exactly
one entry:

- `Internal` uses the first `InternalIP` in each Node's `status.addresses`. This
  option does not require the `sdn` module. If the RSC field is omitted, the
  controller fills it with `["Internal"]`.
- A SystemNetwork name uses the matching `SystemNetworkIP` published in
  `NodeNetworkStatus/<node>.status.addresses` by the `sdn` module.

The following example creates a class using Internal networking and three
preconfigured LVMVolumeGroups on different nodes:

```yaml
apiVersion: storage.deckhouse.io/v1alpha1
kind: ReplicatedStorageClass
metadata:
  name: replicated-internal
spec:
  storage:
    type: LVM
    lvmVolumeGroups:
      - name: lvg-1
      - name: lvg-2
      - name: lvg-3
  failuresToTolerate: 1
  guaranteedMinimumDataRedundancy: 1
  topology: Ignored
  volumeAccess: Local
  reclaimPolicy: Delete
  systemNetworkNames: [Internal]
```

To use a dedicated replication network, first enable the `sdn` module and create
a SystemNetwork with `spec.ipam` configured. Its underlay must connect the nodes
that will participate in replication. Wait for SDN to assign addresses to those
nodes. Then create a **separate** class from the example above, changing its name
and network:

```yaml
metadata:
  name: replicated-storage-net
spec:
  systemNetworkNames: [storage-net]
```

This fragment replaces the corresponding fields in the complete example; it is
not a standalone manifest or an update to `replicated-internal`.
The RSC controller creates an RSP with the selected network automatically.
For manually created RSPs, set the same `spec.systemNetworkNames` field at creation.

#### Availability and diagnostics

Admission rejects an RSC referencing a nonexistent SystemNetwork, a network
without `spec.ipam`, or a SystemNetwork while SDN support is unavailable.
After enabling SDN, wait for the controller, agent and webhook workloads to
roll out with SDN support before creating the class.

A candidate node is eligible only if the selected SystemNetwork exists, is not
being deleted, and has a usable matching `SystemNetworkIP` in NodeNetworkStatus.
SystemNetwork readiness alone does not prove that every node has an address.
Inspect the RSP's `status.eligibleNodes`, `status.message` and Ready condition;
partial exclusion is reported in the message. If no candidates have the required
network, the Ready condition is False with reason
`NoNodesWithRequiredSystemNetworks`; the RSC also reports the pool's failure.
If an agent cannot obtain a required address while reconciling a DRBDResource,
its Configured condition is False with reason `SystemNetworkAddressUnavailable`.

#### Current limitations

- Once set, `spec.systemNetworkNames` is immutable in both RSC and RSP.
  Removing the installed RSC field is also rejected. Choose another network by
  creating a separate class for new volumes, not by updating an existing class.
- Automatic migration of existing volumes between networks and automatic
  network failover are not supported. A published address is not a link-health
  check.
- Do not delete a SystemNetwork or disable SDN while volumes use that network.
  This version does not automatically move them to Internal.
- The agent reads current addresses during reconciliation; changes to
  NodeNetworkStatus do not independently trigger its DRBDResource reconciliation.
  Immediate application of an IP change is not guaranteed.

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
- `volumeCleanup` makes the module recreate the StorageClass as well; the new method applies only to volumes created after the change (see the FAQ, "How do I wipe the data of deleted volumes?");
- `configurationRolloutStrategy` and `eligibleNodesConflictResolutionStrategy` control how the changes reach existing volumes.

**Moving to a new storage pool.** Classes with the same `storage`, `zones`, `nodeLabelSelector`, `systemNetworkNames` and `eligibleNodesPolicy` share one automatically created storage pool (`status.storagePoolName`). After a change to any of them the class gets a new pool, and its volumes follow `configurationRolloutStrategy`: under `RollingUpdate` they are rolled out to the new pool (if their replicas are already on eligible nodes of the new pool, no data is moved), under `NewVolumesOnly` the existing volumes stay on the previous pool and only new volumes are created in the new one. The previous pool is released once no volume uses it. After a module update that changes how the pool name is built, existing classes stay on their pools; a class moves to the new pool on its first `spec` change of **any** field, including `reclaimPolicy` or `replication`, and its volumes follow `configurationRolloutStrategy` as above. A replica left on a node or LVMVolumeGroup that the new pool does not include is reported as a conflict (`VolumesSatisfyEligibleNodes`); RollingRepair handles it only if the node is absent or has remained not ready beyond the grace period. Conflicts caused only by placement changes on live nodes still require manual resolution (see the limitations below).

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
- `eligibleNodesConflictResolutionStrategy: RollingRepair` (the default) evacuates diskful and tie-breaker members from nodes outside the pool's `eligibleNodes` when they have remained not ready longer than `eligibleNodesPolicy.notReadyGracePeriod` or the Node object is absent. It removes the member first; normal layout convergence creates the replacement. Attached or DRBD-reachable members are not removed, and removal requires a remaining live quorum and an UpToDate diskful copy. Access and ShadowDiskful members, and live nodes excluded only by labels, zones or LVMVolumeGroup changes, are outside its scope; Manual disables evacuation. There is no separate limit on the number of volumes evacuated concurrently; replacement synchronization uses the per-node resync limiter. The volume condition distinguishes `ConflictResolutionInProgress` and `ConflictResolutionBlocked`; the class message in `VolumesSatisfyEligibleNodes` breaks down these groups and other conflicts, whose total is `status.volumes.inConflictWithEligibleNodes`. The configuration rollout `maxParallel` above is a separate limit. LLVs retain foreign finalizers and remain `Terminating` until sds-node-configurator deletes their LVs when the node returns (see [Evacuated replica deletion](../../images/controller/internal/controllers/rvr_controller/README.md#evacuated-replica-deletion)). See [Evacuation](../../images/controller/internal/controllers/rv_controller/README.md#evacuation) for the safety guards and recovery lifecycle.

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

When both ends of a connection are `UpToDate` but residual `out-of-sync` bytes stay frozen, DRBD will not resync them on its own (matching generation UUIDs mean no resync is triggered). Reconnecting the peer makes DRBD compare the current generation UUIDs of both ends again. Only a Primary that writes while it is not connected to the peer creates a new current UUID, so the outcome depends on where the Primary is:

- the Primary is on neither end of this connection (for example, it is a diskless replica on another node), or it did not write while the connection was down: the UUIDs still match, and DRBD clears the out-of-sync bits of this connection without transferring or comparing any data, even if the volume was written meanwhile;
- the Primary at one end wrote while the connection was down: the out-of-sync blocks are resynced from the bitmap together with the blocks written meanwhile. The larger the residual, the longer the resync: a residual covering the whole volume means a full resync, and the other end stays `Inconsistent` until it finishes;
- the UUIDs already differed before the reconnect (a diskless replica shows the peer as `peer-disk:Consistent`, see below): DRBD decides on a resync, or reports a split brain, from the UUID history, whether or not anything was written.

The reconnect does not check the data: if the blocks may really differ, verify them and re-mirror one copy instead (see "If a small residual persists" below).

Reconnect one connection at a time and check every replica of the volume before each reconnect. A reconnect takes its connection down for a moment: if another replica is already `Outdated`, `Inconsistent` or resyncing, the volume can lose quorum and suspend I/O. This is what happens when a node has out-of-sync data toward several peers (one alert per connection) and the connections are reconnected one right after another.

1. Check every replica of the volume. `<volume>` is the PersistentVolume name; the DRBD resource of a replica on its node is `sdsrv-<DRBDResource name>`, that is `sdsrv-<volume>-<N>`:

   ```shell
   d8 k --as=system:sudouser get drbdr -l sds-replicated-volume.deckhouse.io/replicated-volume=<volume>
   ```

   Every diskful replica must be `UpToDate` in `DISKSTATE`, and every replica must have `true` in `QUORUM`. On every node of the list, the status of its DRBD resource (see "Inspect a resource") must show no `Outdated` or `Inconsistent` disk, no resync in progress (`replication:SyncSource` or `replication:SyncTarget`) and no `quorum:no`. If any of this does not hold, do not reconnect anything: wait for the resync to finish, or deal with the failed replica first.

2. Reconnect one connection and check that its out-of-sync data is gone:

   ```shell
   d8 k --as=system:sudouser -n d8-sds-replicated-volume exec -t $POD -c agent -- drbdsetup disconnect <resource> <peer-node-id>
   # out-of-sync should become 0:
   d8 k --as=system:sudouser -n d8-sds-replicated-volume exec -t $POD -c agent -- drbdsetup status <resource> --statistics
   ```

3. Repeat step 1 before reconnecting the next connection. If the reconnect started a resync, wait until it finishes. If a replica stays `Outdated` or `Inconsistent`, or a replica loses quorum, stop and do not reconnect anything else; for a lost quorum, see "Restore a lost quorum" above.

A diskless replica can show a diskful peer as `peer-disk:Consistent` instead of `UpToDate`. DRBD does this when the current data generation of the peer differs from the one the diskless replica works with; here it is a symptom of the same out-of-sync data, not a separate failure.

If a small residual persists:

1. Run an online verification of that connection. It reads both disks and marks the blocks that differ as out-of-sync; it does not resync them and does not clear the bits of the blocks that are equal, so the counter does not go down:

   ```shell
   d8 k --as=system:sudouser -n d8-sds-replicated-volume exec -t $POD -c agent -- drbdsetup verify <resource> <peer-node-id> 0
   ```

   When the connection is back to `replication:Established`, read the result in the kernel log of the node (`dmesg` on the node): the `Out of sync: start=..., size=...` lines list the blocks that verify found different. The final `Online verify found N blocks out of sync` line counts the whole bitmap of the connection, the old residual included, so it does not tell them apart. No `Out of sync:` lines means that both copies are identical and the residual is stale accounting, not a divergence. Do not reconnect after a verify that found differing blocks: with matching UUIDs the reconnect clears the bits verify set, without a resync.

2. If verify found differing blocks, fully re-mirror one copy. Never invalidate the disk of the Primary: invalidate the end of the connection that is not Primary. If neither end is Primary, nothing tells which copy is right: stop and contact tech support. Before invalidating, check every replica of the volume as in step 1 of the reconnect above: every other diskful replica must be `UpToDate` and connected. The invalidated copy becomes `Inconsistent` and does a full resync from an up-to-date replica. Quorum is held by the remaining up-to-date replicas during the resync; with two diskful replicas (`2D+1TB`) the volume has no second up-to-date copy until the resync ends. On the node of the copy to invalidate:

   ```shell
   TARGET_POD=$(d8 k -n d8-sds-replicated-volume get pod --field-selector=spec.nodeName=<target-node> -l app=agent -o name)
   MIN=$(d8 k --as=system:sudouser -n d8-sds-replicated-volume exec -t $TARGET_POD -c agent -- drbdsetup status <target-resource> --verbose | grep -oE 'minor:[0-9]+' | head -1 | cut -d: -f2)
   d8 k --as=system:sudouser -n d8-sds-replicated-volume exec -t $TARGET_POD -c agent -- drbdsetup invalidate $MIN
   ```

**Phantom counter.** If, after a full re-mirror, a Primary still reports a tiny `out-of-sync` toward a peer whose data was just re-read bit-for-bit, this is a stale accounting artifact on the Primary side, not a real divergence — the data on all replicas is identical. Reconnect, verify and re-mirror will not clear it, it resets only on a disruptive action (a failover or a reboot of the Primary node). For a live volume it is safe to leave it until the next maintenance window.

<!--
Where: docs/FAQ.md, the answer to "I am unable to update the fields in the ReplicatedStorageClass resource spec. Is this the expected behavior?"
Replaced by: the answer saying that changing the spec is not supported
-->

Only for a few fields. `topology`, `volumeAccess` and, once set, `systemNetworkNames` are immutable, and updates to them are rejected with an error that names the field — to change them, recreate the resource. All other fields **can** be changed on an existing resource. Editing `replication` (or `failuresToTolerate`/`guaranteedMinimumDataRedundancy`) is how volumes are migrated between layouts — for example from `ConsistencyAndAvailability` (`3D`) to `Availability` (`2D+1TB`); editing `storage`, `zones`, `nodeLabelSelector` or `eligibleNodesPolicy` moves the class to a new storage pool; editing `reclaimPolicy` recreates the StorageClass with the new policy; editing `volumeCleanup` recreates the StorageClass too and applies only to volumes created after the change. See the "Updating the ReplicatedStorageClass resource" section of the usage guide.

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

<!--
Where: docs/FAQ.md, the body of "How do I get info about the space used?"
Replaced by: the answer pointing at the "LINSTOR/DRBD" dashboard and `linstor storage-pool list`
-->

Open "Dashboards" → "Storage" → "SDS Replicated Volume" in Grafana and expand the "Capacity by ReplicatedStorageClass" row. Pick the classes in the "Storage Class" selector at the top of the dashboard; every panel of the row shows each selected class separately:

- "Raw Capacity", "Raw Free" and "Raw Used" show the capacity of the storage pool of the class before replication. A thick (`LVM`) pool counts the size and the free space of its Volume Groups. A thin (`LVMThin`) pool counts the allocation limit of its thin pools, that is the thin pool size multiplied by `allocationLimit` of the LVMVolumeGroup (150% by default), and its free space is that limit minus the space already allocated to thin volumes.
- "Usable Capacity" and "Usable Free" show the same values divided by the number of replicas of the class ("Replicas"): `failuresToTolerate + guaranteedMinimumDataRedundancy + 1`, which is 1 for `None`, 2 for `Availability` and `Consistency` and 3 for `ConsistencyAndAvailability`. This is how much volume data the class can hold. It is an upper bound: the replicas of a volume are placed on different nodes and each of them fits into one Volume Group, so with unevenly filled nodes less can actually be placed.
- "Thin Pools: Allocation and Data by Storage Class" shows, for thin pools, the allocated space against the allocation limit and the data actually written against the physical size of the thin pools. Written data close to the physical size means the thin pools are about to run out of space, whatever the allocation limit allows.
- "Capacity by Node / LVMVolumeGroup" lists every LVMVolumeGroup (and thin pool) of the storage pools of the selected classes with its node, capacity, free space and the share used.

Classes with identical storage settings use one storage pool (`status.storagePoolName`) and share its capacity: their raw values are the same, and space taken by one class is no longer available to the others.

The capacity of LVMVolumeGroups comes from the metrics of the `sds-node-configurator` module (`sds_node_configurator_lvg_*`). The controller of `sds-replicated-volume` exports which LVMVolumeGroups belong to a storage pool and how many replicas a class has:

- `sds_rsp_lvg_info{storage_pool, lvg_name, thin_pool}`: value 1 for every LVMVolumeGroup (and thin pool) listed in `spec.lvmVolumeGroups` of a ReplicatedStoragePool; `thin_pool` is empty for `LVM` pools.
- `sds_rsc_replicas{storage_class, storage_pool}`: the number of diskful replicas of the volumes of a class and the storage pool they are placed in, both taken from the accepted configuration of the class (`status.configuration`).

For example, the usable free space of classes with thick pools:

```text
sum by (storage_pool) (
  max by (lvg_name) (sds_node_configurator_lvg_vg_free_bytes)
  * on (lvg_name) group_right max by (storage_pool, lvg_name) (sds_rsp_lvg_info{thin_pool=""})
)
/ on (storage_pool) group_right max by (storage_class, storage_pool) (sds_rsc_replicas)
```

<!--
Where: docs/FAQ.md, a section of its own after "How much of a volume is reserved for the superuser?"
Replaced by: nothing
-->

## How do I wipe the data of deleted volumes?

By default the data of a deleted volume is not wiped: the space of its logical volumes returns to the volume group (or thin pool) as it is, and nothing guarantees that a volume created there later cannot read it. To have the data overwritten before the space is released, set the method in `spec.volumeCleanup` of the `ReplicatedStorageClass`:

```yaml
apiVersion: storage.deckhouse.io/v1alpha1
kind: ReplicatedStorageClass
metadata:
  name: <name>
spec:
  volumeCleanup: RandomFillSinglePass
  # ...
```

The methods are those of [sds-node-configurator](https://deckhouse.io/modules/sds-node-configurator/), which performs the wipe:

- `RandomFillSinglePass` — the logical volume is overwritten with random data once;
- `RandomFillThreePass` — the logical volume is overwritten with random data three times;
- `Discard` — all blocks of the logical volume are marked as free with the `discard` system call. Only for thick volumes (`storage.type: LVM`) on solid-state drives: the class is rejected with `Discard` on thin storage.

Both `RandomFill` methods wear solid-state drives and are not recommended for them. For thin volumes they overwrite only the space in use at the moment of removal.

What to take into account:

- **Editions.** The wipe is available only in the Enterprise Edition and in the Certified Security Edition Pro. In the other editions a `ReplicatedStorageClass` with `volumeCleanup` is rejected by the validating webhook.
- **The method is fixed when the volume is created.** It is recorded in `spec.volumeCleanup` of the `ReplicatedVolume` and cannot be changed there. Changing the field of the class makes the module recreate the StorageClass and affects only volumes created after the change; existing volumes keep their method. Volumes created before the field was set are not wiped. To check what a volume uses:

  ```shell
  d8 k get replicatedvolume <pv-name> -o jsonpath='{.spec.volumeCleanup}{"\n"}'
  ```

- **Every copy is wiped, not only on deletion.** The method applies to the logical volumes of all replicas of the volume, including replicas created later, and they are wiped whenever they are removed: when the volume is deleted, and also when a replica moves to another node or storage, becomes diskless or is removed. The wipe never starts while DRBD still uses the logical volume.
- **Time.** Overwriting takes time proportional to the size of the volume and is repeated for every replica (three times over for `RandomFillThreePass`). A deleted volume and its PersistentVolume are gone only after all its logical volumes are wiped; while a wipe runs, the `LVMLogicalVolume` is in the `Cleaning` phase:

  ```shell
  d8 k get lvmlogicalvolume -l sds-replicated-volume.deckhouse.io/replicated-volume=<pv-name>
  ```

  The space of a volume is released only after its wipe, so free space in the pool returns later than without a wipe.
