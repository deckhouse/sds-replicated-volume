---
title: "The sds-replicated-volume module: configuration examples"
description: The sds-replicated-volume controller usage and work-flow examples.
---

{{< alert level="warning" >}}
The module is only guaranteed to work if the [system requirements](./readme.html#system-requirements-and-recommendations) are met.
As for any other configurations, the module may work, but its smooth operation is not guaranteed.
{{< /alert >}}

Once the `sds-replicated-volume` module is enabled in the Deckhouse configuration, all that remains is to create the storage pools and StorageClass according to the instructions below.

## Configuring the module

The configuration is performed by the `sds-replicated-volume-controller` using the custom resources [ReplicatedStoragePool](/modules/sds-replicated-volume/cr.html#replicatedstoragepool) and [ReplicatedStorageClass](/modules/sds-replicated-volume/cr.html#replicatedstorageclass). To create a Storage Pool, the [LVMVolumeGroup](/modules/sds-node-configurator/cr.html#lvmvolumegroup) and an LVM Thin Pool must be preconfigured on the cluster nodes. LVM configuration is provided by the [`sds-node-configurator`](/modules/sds-node-configurator/) module.

### Setting up LVM

Configuration examples can be found in the [sds-node-configurator](/modules/sds-node-configurator/usage.html) module documentation. The configuration will result in [LVMVolumeGroup](/modules/sds-node-configurator/cr.html#lvmvolumegroup) resources to be created in the cluster (the latter are required for further configuration).

### Using ReplicatedStoragePool resources

#### Creating a ReplicatedStoragePool resource

- To create a `Storage Pool` the user has to create a [ReplicatedStoragePool](./cr.html#replicatedstoragepool) resource and fill in the `spec` field, specifying the pool type as well as the [LVMVolumeGroup](/modules/sds-node-configurator/cr.html#lvmvolumegroup) resources used.

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

An example of a resource for classic Thin LVM volumes:

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

Before working with the backend the controller will validate the provided configuration. If an error is detected, it will report the cause of the error.

For all LVMVolumeGroup resources in the `spec` of the ReplicatedStoragePool resource the following rules must be met:

- They must reside on different nodes. You may not refer to multiple LVMVolumeGroup resources located on the same node.
- All nodes should be of type other than `CloudEphemeral` (see [Node types](https://deckhouse.io/products/kubernetes-platform/documentation/v1/modules/040-node-manager/#node-types))

Information about the controller's progress and results is available in the `status` field of the created ReplicatedStoragePool resource.

The `sds-replicated-volume-controller` will then process the `ReplicatedStoragePool` resource defined by the user and create the corresponding `Storage Pool` in the backend. The name of the `Storage Pool` being created will match the name of the created `ReplicatedStoragePool` resource. The `Storage Pool` will be created on the nodes defined in the LVMVolumeGroup resources.

#### Updating the ReplicatedStoragePool resource

You can add new LVMVolumeGroups to the `spec.lvmVolumeGroups` list (effectively adding new nodes to the Storage Pool).

The `sds-replicated-volume-controller` will then validate the new configuration. If it is valid, the controller will update the `Storage Pool` in the backend. The results of this operation will also be reflected in the `status` field of the `ReplicatedStoragePool` resource.

> Note that the `spec.type` field of the ReplicatedStoragePool resource is **immutable**.
>
> The controller does not respond to changes made by the user in the `status` field of the resource.

#### Deleting the ReplicatedStoragePool resource

Currently, the `sds-replicated-volume-controller` does not handle the deletion of ReplicatedStoragePool resources in any way.

> Deleting a resource does not affect the `Storage Pool` created for it in the backend.
If the user recreates the deleted resource with the same name and configuration, the controller will detect that the corresponding `Storage Pools` are already created, so no changes will be made.

The `status.phase` field of the created resource will be set to `Created`.

### Using ReplicatedStorageClass resources

#### Creating a ReplicatedStorageClass resource

To create a StorageClass in Kubernetes, you have to create a [ReplicatedStorageClass](./cr.html#replicatedstorageclass) resource and fill in the `spec` field with the required parameters. (Note that you cannot manually create a StorageClass for the replicated.csi.storage.deckhouse.io CSI driver).

Below is an example of a resource for creating a StorageClass based on local volumes only (i.e., no data can be accessed over the network) and with a high data redundancy in a cluster consisting of three zones:

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

The `replication` parameter is omitted since it is set to `ConsistencyAndAvailability` by default, which is consistent with high redundancy requirements.

Below is an example of a resource for creating a StorageClass with allowed access to data over the network and no redundancy in a cluster where there are no zones (e.g., it is a good fit for testing environments):

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

More examples with different usage scenarios and layouts [can be found here](./layouts.html)

> Before creating the StorageClass, the configuration user provides will be validated.
> If errors are found, the StorageClass will not be created, and the information about the error will be saved to the `status` field of the ReplicatedStorageClass resource.

The `sds-replicated-volume-controller` will then analyze the user's ReplicatedStorageClass resource and create the corresponding Storage Class in Kubernetes.

> Please note that most fields of the `spec` section of the ReplicatedStorageClass resource are **immutable** after creation. Only the replication settings (`replication`, `failuresToTolerate`, `guaranteedMinimumDataRedundancy`), `configurationRolloutStrategy`, `eligibleNodesConflictResolutionStrategy` and `reclaimPolicy` (the StorageClass is recreated with the new policy) can be changed on an existing resource; changing any other field (`storage`, `topology`, `zones`, `volumeAccess`, `nodeLabelSelector`, etc.) is rejected on update.

The `sds-replicated-volume-controller` will automatically keep the `status` field up to date to reflect the results of the ongoing operations.

#### Updating the ReplicatedStorageClass resource

Most `spec` fields are immutable after creation, and any attempt to change one is rejected with an error naming the field; changing such a field (for example `storage`, `topology`, `zones`, `volumeAccess`, `nodeLabelSelector`) requires recreating the resource. The replication settings and `reclaimPolicy` are mutable: editing `replication` enables the r3→r2 migration below, and editing `reclaimPolicy` makes the module recreate the StorageClass with the new policy.

##### Migrating volumes from three replicas (r3) to two replicas + tie-breaker (r2)

Editing `spec.replication` of an existing ReplicatedStorageClass changes the intended layout of **all** volumes of that class at once. To migrate from `ConsistencyAndAvailability` (three data replicas, layout `3D`) to `Availability` (two data replicas plus a diskless tie-breaker, layout `2D+1TB`):

1. Edit the class:

   ```shell
   kubectl patch replicatedstorageclass <RSC_NAME> --type=merge -p '{"spec":{"replication":"Availability"}}'
   ```

2. The controller migrates each volume in place: one diskful replica is retyped into a tie-breaker (no full resync, no data movement) and its logical volume is released. Watch progress per volume via the `MembershipLayoutConverged` condition and the `MembershipLayout` print column:

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

**Limitations.**

- There is no automatic reverse path: editing `replication` back toward more replicas (r2→r3) is reported on each volume as `MembershipLayoutConverged=False/TransitionUnsupported` and performs no action — it requires manual intervention.
- Reverting the edit while a volume is still migrating does not cancel a retype that is already in flight, and such a volume never reports `Converged` again on its own. Depending on the timing the volume ends up either at `2D+1TB` against the intended `3D` (`MembershipLayoutConverged=False/TransitionUnsupported`), or with a replica whose `spec.type` is stuck at `TieBreaker` while the layout still reads `3D` (`MembershipLayoutConverged=False/Converging`, with the affected replica named in the condition message). No data is lost in either case. In the second case, restore the replica with a single patch that sets `spec.type` back to `Diskful` and re-adds its backing-volume fields (`spec.lvmVolumeGroupName`, and `spec.lvmVolumeGroupThinPoolName` for a thin pool) with the values from `status.datamesh.members` of the volume.
- Under `RollingUpdate` the edit applies to every volume of the class at once: throttling the rollout (`configurationRolloutStrategy.rollingUpdate.maxParallel`) is not yet implemented.

#### Deleting the ReplicatedStorageClass resource

You can delete the StorageClass in Kubernetes by removing its`ReplicatedStorageClass resource.
The`sds-replicated-volume-controller` will detect that the resource has been deleted and carry out all necessary operations to properly delete its associated StorageClass.

> The `sds-replicated-volume-controller` will only delete the StorageClass associated with the resource if the `status.phase` field of the ReplicatedStorageClass resource is set to `Created`. Otherwise, the controller will only delete the ReplicatedStorageClass resource while its associated StorageClass will not be affected.

## Additional features for applications

### Hosting an application "closer" to the data (data locality)

In a hyperconverged infrastructure, you may want your pods to run on the same nodes as their data volumes, as this will help maximize storage performance.

The module provides a custom scheduler for such tasks. It takes into account where exactly the data is stored and tries to schedule pods first on those nodes where the data is available locally.
Any pod that uses sds-replicated-volume volumes will be automatically configured to use this scheduler.

Data locality is determined by the `volumeAccess` parameter when the ReplicatedStorageClass resource is being created.
