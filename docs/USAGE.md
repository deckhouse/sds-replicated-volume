---
title: "The sds-replicated-volume module: configuration examples"
description: The sds-replicated-volume controller usage and work-flow examples.
---

{{< alert level="warning" >}}
The module is only guaranteed to work if the [system requirements](./readme.html#system-requirements-and-recommendations) are met.
As for any other configurations, the module may work, but its smooth operation is not guaranteed.
{{< /alert >}}

Once the `sds-replicated-volume` module is enabled in the Deckhouse configuration, create a [ReplicatedStoragePool](#creating-a-replicatedstoragepool-resource) and a [ReplicatedStorageClass](#creating-a-replicatedstorageclass-resource) according to the instructions below.

## Configuring the module

The configuration is performed by the `sds-replicated-volume-controller` using the custom resources [ReplicatedStoragePool](./cr.html#replicatedstoragepool) and [ReplicatedStorageClass](./cr.html#replicatedstorageclass). To create a Storage Pool, first configure [LVMVolumeGroup](/modules/sds-node-configurator/cr.html#lvmvolumegroup) and an LVM Thin Pool on the cluster nodes. LVM configuration is provided by the [`sds-node-configurator`](/modules/sds-node-configurator/) module.

### Setting up LVM

Configuration examples can be found in the [sds-node-configurator](/modules/sds-node-configurator/usage.html) module documentation. As a result of the configuration, [LVMVolumeGroup](/modules/sds-node-configurator/cr.html#lvmvolumegroup) resources appear in the cluster; they are required for further configuration.

### Using ReplicatedStoragePool resources

#### Creating a ReplicatedStoragePool resource

1. Create a [ReplicatedStoragePool](./cr.html#replicatedstoragepool) resource and fill in the [`spec`](./cr.html#replicatedstoragepool-v1alpha1-spec) field, specifying the pool type and the [LVMVolumeGroup](/modules/sds-node-configurator/cr.html#lvmvolumegroup) resources to use.

   An example of a resource for classic LVM volumes (Thick):

   ```yaml
   apiVersion: storage.deckhouse.io/v1alpha1
   kind: ReplicatedStoragePool
   metadata:
     name: data
   spec:
     type: LVM
     lvmVolumeGroups:
     - name: lvg-1
     - name: lvg-2
   ```

   An example of a resource for Thin LVM volumes:

   ```yaml
   apiVersion: storage.deckhouse.io/v1alpha1
   kind: ReplicatedStoragePool
   metadata:
     name: thin-data
   spec:
     type: LVMThin
     lvmVolumeGroups:
       - name: lvg-3
         thinPoolName: thin-pool
       - name: lvg-4
         thinPoolName: thin-pool
   ```

1. Before working with the backend, wait for the controller to validate the provided configuration. If an error is detected, check the cause in the [`status`](./cr.html#replicatedstoragepool-v1alpha1-status) field.

   For all [LVMVolumeGroup](/modules/sds-node-configurator/cr.html#lvmvolumegroup) resources specified in the [`spec`](./cr.html#replicatedstoragepool-v1alpha1-spec) of the [ReplicatedStoragePool](./cr.html#replicatedstoragepool) resource, the following rules must be met:

   - They must reside on different nodes. Do not specify multiple LVMVolumeGroup resources located on the same node.
   - All nodes must be of a type other than `CloudEphemeral` (["Node types"](/products/kubernetes-platform/documentation/v1/modules/040-node-manager/#node-types)).

1. Check the controller progress and results in the [`status`](./cr.html#replicatedstoragepool-v1alpha1-status) field of the created [ReplicatedStoragePool](./cr.html#replicatedstoragepool) resource.

The `sds-replicated-volume-controller` processes the [ReplicatedStoragePool](./cr.html#replicatedstoragepool) resource and creates the corresponding Storage Pool in the backend. The name of the created Storage Pool matches the name of the [ReplicatedStoragePool](./cr.html#replicatedstoragepool) resource. The Storage Pool is created on the nodes defined in the [LVMVolumeGroup](/modules/sds-node-configurator/cr.html#lvmvolumegroup) resources.

#### Updating the ReplicatedStoragePool resource

1. Add new [LVMVolumeGroup](/modules/sds-node-configurator/cr.html#lvmvolumegroup) resources to the [`spec.lvmVolumeGroups`](./cr.html#replicatedstoragepool-v1alpha1-spec-lvmvolumegroups) list (this adds new nodes to the Storage Pool).

1. Wait for the `sds-replicated-volume-controller` to validate the new configuration. If it is valid, the controller updates the Storage Pool in the backend.

1. Check the results of the operation in the [`status`](./cr.html#replicatedstoragepool-v1alpha1-status) field of the [ReplicatedStoragePool](./cr.html#replicatedstoragepool) resource.

{{< alert level="warning" >}}
The `spec.type` field of the ReplicatedStoragePool resource is **immutable**.
The controller does not respond to changes made by the user in the `status` field of the resource.
{{< /alert >}}

#### Deleting the ReplicatedStoragePool resource

Delete the [ReplicatedStoragePool](./cr.html#replicatedstoragepool) resource if needed.

{{< alert level="warning" >}}
Currently, the `sds-replicated-volume-controller` does not handle the deletion of [ReplicatedStoragePool](./cr.html#replicatedstoragepool) resources. Deleting a resource does not affect the Storage Pool created for it in the backend. If you recreate the deleted resource with the same name and configuration, the controller detects that the corresponding Storage Pools are already created and leaves them unchanged. The [`status.phase`](./cr.html#replicatedstoragepool-v1alpha1-status-phase) field of the created resource is set to `Created`.
{{< /alert >}}

### Using ReplicatedStorageClass resources

#### Creating a ReplicatedStorageClass resource

1. Create a [ReplicatedStorageClass](./cr.html#replicatedstorageclass) resource and fill in the [`spec`](./cr.html#replicatedstorageclass-v1alpha1-spec) field with the required parameters. Do not manually create a StorageClass for the `replicated.csi.storage.deckhouse.io` CSI driver.

   Below is an example of a resource for creating a StorageClass based on local volumes only (no data access over the network) and with high data redundancy in a cluster consisting of three zones:

   ```yaml
   apiVersion: storage.deckhouse.io/v1alpha1
   kind: ReplicatedStorageClass
   metadata:
     name: haclass
   spec:
     storagePool: storage-pool-name
     volumeAccess: Local
     reclaimPolicy: Delete
     topology: TransZonal
     zones:
     - zone-a
     - zone-b
     - zone-c
   ```

   The [`replication`](./cr.html#replicatedstorageclass-v1alpha1-spec-replication) parameter is omitted since it defaults to `ConsistencyAndAvailability`, which matches high redundancy requirements.

   Below is an example of a resource for creating a StorageClass with allowed access to data over the network and no redundancy in a cluster where there are no zones (for example, a good fit for testing environments):

   ```yaml
   apiVersion: storage.deckhouse.io/v1alpha1
   kind: ReplicatedStorageClass
   metadata:
     name: testclass
   spec:
     replication: None
     storagePool: storage-pool-name
     reclaimPolicy: Delete
     topology: Ignored
   ```

   More examples with different usage scenarios and layouts are described in the [documentation](./layouts.html).

{{< alert level="info" >}}
Before a StorageClass is created, the provided configuration is validated.
If errors are found, the StorageClass is not created, and error details appear in the `status` field of the ReplicatedStorageClass resource.
{{< /alert >}}

Processing the ReplicatedStorageClass resource results in creating the required StorageClass in Kubernetes.

{{< alert level="warning" >}}
Most fields of the `spec` of the ReplicatedStorageClass resource are **immutable** after creation. Only the replication settings (`replication`, `failuresToTolerate`, `guaranteedMinimumDataRedundancy`), `configurationRolloutStrategy`, `eligibleNodesConflictResolutionStrategy`, and `reclaimPolicy` (the StorageClass is recreated with the new policy) can be changed on an existing resource; changing any other field (`storage`, `topology`, `zones`, `volumeAccess`, `nodeLabelSelector`, and similar) is rejected on update.
{{< /alert >}}

The `status` field is updated by the `sds-replicated-volume-controller` to show the results of the operations.

#### Updating the ReplicatedStorageClass resource


Most `spec` fields are immutable after creation, and any attempt to change one is rejected with an error naming the field; changing such a field (for example `storage`, `topology`, `zones`, `volumeAccess`, `nodeLabelSelector`) requires recreating the resource. The replication settings and `reclaimPolicy` are mutable: editing `replication` enables the r3→r2 migration below, and editing `reclaimPolicy` makes the module recreate the StorageClass with the new policy.

##### Migrating volumes from three replicas (r3) to two replicas + tie-breaker (r2)

Editing `spec.replication` of an existing ReplicatedStorageClass changes the intended layout of **all** volumes of that class at once. To migrate from `ConsistencyAndAvailability` (three data replicas, layout `3D`) to `Availability` (two data replicas plus a diskless tie-breaker, layout `2D+1TB`):

1. Edit the class:

   ```shell
   d8 k patch replicatedstorageclass <RSC_NAME> --type=merge -p '{"spec":{"replication":"Availability"}}'
   ```

   `<RSC_NAME>` — name of the ReplicatedStorageClass resource.

2. The controller migrates each volume in place: one diskful replica is retyped into a tie-breaker (no full resync, no data movement) and its logical volume is released. Watch progress per volume via the `MembershipLayoutConverged` condition and the `MembershipLayout` print column:

   ```shell
   d8 k get replicatedvolume -o wide
   d8 k get replicatedvolume <RV_NAME> -o jsonpath='{.status.membershipLayout} {range .status.conditions[?(@.type=="MembershipLayoutConverged")]}{.status}/{.reason}{end}{"\n"}'
   ```

   A volume is migrated when `MembershipLayoutConverged` is `True/Converged` and `status.membershipLayout` is `2D+1TB`.

3. Watch the class-wide rollout via the `ConfigurationRolledOut` condition and the `status.volumes` counters:

   ```shell
   d8 k get replicatedstorageclass <RSC_NAME> -o jsonpath='{.status.volumes}{"\n"}'
   ```

   The rollout is complete when `ConfigurationRolledOut` is `True`, which happens exactly when `status.volumes.pendingObservation` and `status.volumes.staleConfiguration` are both `0`.

   Do not wait for `aligned` to reach `total`. Only volumes that take their configuration from the class participate in the rollout; a volume switched to `spec.configurationMode: Manual` carries its own configuration, so the class neither rolls anything out to it nor waits for it. Such volumes are still counted in `total`, and the class therefore completes the rollout with `aligned` below `total`.

**Requirements.** The `2D+1TB` layout needs a node for the tie-breaker in addition to the two diskful nodes: at least 3 nodes for `Ignored` topology, at least 3 zones for `TransZonal`, or at least 3 nodes in the volume's zone for `Zonal`. These match the `3D` requirements, so r3→r2 does not raise them.

**Limiting the rollout to new volumes.** By default (`configurationRolloutStrategy.type: RollingUpdate`) a configuration edit applies to every volume of the class. Set the strategy to `NewVolumesOnly` to apply it to newly created volumes only:

```shell
d8 k patch replicatedstorageclass <RSC_NAME> --type=merge -p '{"spec":{"configurationRolloutStrategy":{"type":"NewVolumesOnly","rollingUpdate":null}}}'
```

Volumes that already have a configuration then keep it. Such a volume observes the new configuration (so the class is not stuck waiting for it) but does not apply it, and reports:

```shell
d8 k get replicatedvolume <RV_NAME> -o jsonpath='{range .status.conditions[?(@.type=="ConfigurationReady")]}{.status}/{.reason}: {.message}{end}{"\n"}'
# False/NewerConfigurationHeld: ... has a newer configuration (generation N); the volume keeps its configuration (generation M) ...
```

Held volumes are counted in `status.volumes.staleConfiguration` of the class, and `ConfigurationRolledOut` becomes `False/ConfigurationRolloutDisabled`. The hold is deliberate and persists even if the held configuration later stops matching the cluster: to release a volume, switch the strategy back to `RollingUpdate` (all held volumes roll out through the normal path) or recreate the volume. Switching from `RollingUpdate` to `NewVolumesOnly` never rolls anything back — configuration already applied stays applied.

**Throttling the rollout.** Under `RollingUpdate`, `configurationRolloutStrategy.rollingUpdate.maxParallel` (default `5`) caps how many volumes of the class migrate at the same time:

```shell
d8 k patch replicatedstorageclass <RSC_NAME> --type=merge -p '{"spec":{"configurationRolloutStrategy":{"type":"RollingUpdate","rollingUpdate":{"maxParallel":2}}}}'
```

The volumes that still need the new configuration are ordered by name, and the leading ones fill the free slots — all of them at once, so with `maxParallel: 2` the first two migrate in parallel. A slot frees up when its volume reports `MembershipLayoutConverged=True/Converged`, and the next name in the order takes it. The ones still waiting keep their own configuration and report:

```shell
d8 k get replicatedvolume <RV_NAME> -o jsonpath='{range .status.conditions[?(@.type=="ConfigurationReady")]}{.status}/{.reason}: {.message}{end}{"\n"}'
# False/ConfigurationRolloutInProgress: ... rolls its configuration (generation N) out to at most 2 volume(s) at a time ...
```

Waiting volumes are counted in `status.volumes.staleConfiguration` of the class, so `ConfigurationRolledOut` stays `False/ConfigurationRolloutInProgress` until the whole class has migrated. Lowering `maxParallel` does not stop volumes that are already migrating — it only keeps new ones from joining. A volume that can never converge on the new configuration (see the limitations below) holds its slot indefinitely, and that is the point of the parameter: it bounds how many volumes a bad edit reaches, not only how fast a good one spreads.

**Limitations.**

- There is no automatic reverse path: editing `replication` back toward more replicas (r2→r3) is reported on each volume as `MembershipLayoutConverged=False/TransitionUnsupported` and performs no action — it requires manual intervention.
- Reverting the edit while a volume is still migrating does not cancel a retype that is already in flight, and such a volume never reports `Converged` again on its own. Depending on the timing the volume ends up either at `2D+1TB` against the intended `3D` (`MembershipLayoutConverged=False/TransitionUnsupported`), or with a replica whose `spec.type` is stuck at `TieBreaker` while the layout still reads `3D` (`MembershipLayoutConverged=False/Converging`, with the affected replica named in the condition message). No data is lost in either case. In the second case, restore the replica with a single patch that sets `spec.type` back to `Diskful` and re-adds its backing-volume fields (`spec.lvmVolumeGroupName`, and `spec.lvmVolumeGroupThinPoolName` for a thin pool) with the values from `status.datamesh.members` of the volume.
- `eligibleNodesConflictResolutionStrategy.rollingRepair.maxParallel` is accepted but not implemented: repairing volumes that ended up on non-eligible nodes is not throttled. This is a different parameter from the configuration rollout `maxParallel` above, which is enforced.

#### Deleting the ReplicatedStorageClass resource

1. Delete the [ReplicatedStorageClass](./cr.html#replicatedstorageclass) resource to remove the associated StorageClass in Kubernetes.

1. Wait for the `sds-replicated-volume-controller` to detect the deletion and perform all necessary operations to properly delete the child StorageClass.

{{< alert level="warning" >}}
The `sds-replicated-volume-controller` deletes the child StorageClass only if the `status.phase` field of the ReplicatedStorageClass resource is set to `Created`. Otherwise, only the ReplicatedStorageClass resource is deleted, and the child StorageClass is not affected.
{{< /alert >}}

## Additional features for applications

### Hosting an application "closer" to the data (data locality)

In a hyperconverged infrastructure, you may need to preferentially place an application pod on nodes where its required storage data is available locally. This maximizes storage performance.

To solve this, the module provides a custom scheduler that takes data placement into account and tries to schedule the pod first on nodes where the data is available locally. This scheduler is assigned automatically to any pod that uses `sds-replicated-volume` volumes.

Data locality is configured with the [`volumeAccess`](./cr.html#replicatedstorageclass-v1alpha1-spec-volumeaccess) parameter when creating a [ReplicatedStorageClass](./cr.html#replicatedstorageclass) resource.
