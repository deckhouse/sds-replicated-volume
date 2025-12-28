---
title: "The sds-replicated-volume module: configuration examples"
description: The sds-replicated-volume controller usage and work-flow examples.
---

{{< alert level="warning" >}}
The module is only guaranteed to work if the [system requirements](./readme.html#system-requirements-and-recommendations) are met.
As for any other configurations, the module may work, but its smooth operation is not guaranteed.
{{< /alert >}}

Once the `sds-replicated-volume` module is enabled in the Deckhouse configuration, all that remains is to create the storage pools and StorageClass according to the instructions below.

## Configuring the `sds-replicated-volume` backend

In `Deckhouse`, the `sds-replicated-volume-controller` is configured by creating `ReplicatedStoragePool` and `ReplicatedStorageClass` [custom resources](./cr.html). The `LVM Volume Group` and `LVM Thin pool` configured on the cluster nodes are required to create a `Storage Pool`. The [sds-node-configurator](/modules/sds-node-configurator/usage.html) module handles the configuration of `LVM`.

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

> Please note that all fields of `spec` section of the ReplicatedStorageClass resource are **immutable**.

The `sds-replicated-volume-controller` will automatically keep the `status` field up to date to reflect the results of the ongoing operations.

#### Updating the ReplicatedStorageClass resource

It is currently **not possible** to change configuration parameters of the StorageClass created via the ReplicatedStorageClass resource.

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
