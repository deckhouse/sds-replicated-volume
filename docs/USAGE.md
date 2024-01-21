---
title: "The SDS-DRDB module: configuration examples"
description: The SDS-DRDB controller usage and work-flow examples.
---

{{< alert level="warning" >}}
The module is guaranteed to work only in the following cases:
- if stock kernels shipped with the [supported distributions](https://deckhouse.io/documentation/v1/supported_versions.html#linux) are used;
- if a 10 Gbps network is used.

As for any other configurations, the module may work, but its smooth operation is not guaranteed.
{{< /alert >}}

Once the `SDS-DRBD` module is enabled in the Deckhouse configuration, your cluster will be automatically configured to use the `LINSTOR` backend. All that remains is to create the storage pools and StorageClass according to the instructions below.

## Configuring the LINSTOR backend

In `Deckhouse`, the `sds-drbd-controller` handles the configuration of `LINSTOR` storage. For this, `DRBDStoragePool` and `DRBDStorageClass` [custom resources](./cr.html) are created. The `LVM Volume Group` and `LVM Thin pool` configured on the cluster nodes are required to create a `Storage Pool`. The [SDS-Node-Configurator](../../sds-node-configurator/stable/) module handles the configuration of `LVM`.

> **Caution!** The user may not configure the `LINSTOR` backend directly.

### Setting up LVM

Configuration examples can be found in the [SDS-Node-Configurator](../../sds-node-configurator/stable/usage.html) module documentation. The configuration will result in [LVMVolumeGroup](./../../sds-node-configurator/stable/cr.html#lvmvolumegroup) resources to be created in the cluster (the latter are required for further configuration).

### Using `DRBDStoragePool` resources

#### Creating a `DRBDStoragePool` resource

- To create a `Storage Pool` on specific nodes in `LINSTOR`, the user has to create a [DRBDStoragePool](./cr.html#drbdstoragepool) resource and fill in the `spec` field, specifying the pool type as well as the [LVMVolumeGroup](../../sds-node-configurator/stable/cr.html#lvmvolumegroup) resources used.

- An example of a resource for classic LVM volumes (Thick):

```yaml
apiVersion: storage.deckhouse.io/v1alpha1
kind: DRBDStoragePool
metadata:
  name: data
spec:
  type: LVM
  lvmvolumegroups:
    - name: lvg-1
    - name: lvg-2
```

- An example of a resource for classic Thin LVM volumes:

```yaml
apiVersion: storage.deckhouse.io/v1alpha1
kind: DRBDStoragePool
metadata:
  name: thin-data
spec:
  type: LVMThin
  lvmvolumegroups:
    - name: lvg-3
      thinpoolname: thin-pool
    - name: lvg-4
      thinpoolname: thin-pool
```

> **Caution!** All `LVMVolumeGroup` resources in the `spec` of the `DRBDStoragePool` resource must reside on different nodes. (You may not refer to multiple `LVMVolumeGroup` resources located on the same node).

The `sds-drbd-controller` will then process the `DRBDStoragePool` resource defined by the user and create the corresponding `Storage Pool` in the `Linstor` backend.

> The name of the `Storage Pool` being created will match the name of the created `DRBDStoragePool` resource.
>
> The `Storage Pool` will be created on the nodes defined in the LVMVolumeGroup resources.

Information about the controller's progress and results is available in the `status` field of the created `DRBDStoragePool` resource.

> Before working with `LINSTOR`, the controller will validate the provided configuration. If an error is detected, it will report the cause of the error. 
>
> Invalid `Storage Pools` will not be created in `LINSTOR`.

#### Updating the `DRBDStoragePool` resource

You can add new `LVMVolumeGroups` to the `spec.lvmVolumeGroups` list (effectively adding new nodes to the Storage Pool).

The `sds-drbd-controller` will then validate the new configuration. If it is valid, the controller will update the `Storage Pool` in the `Linstor` backend. The results of this operation will also be reflected in the `status` field of the `DRBDStoragePool` resource.

> Note that the `spec.type` field of the `DRBDStoragePool` resource is **immutable**.
>
> The controller does not respond to changes made by the user in the `status` field of the resource.

#### Deleting the `DRBDStoragePool` resource

Currently, the `sds-drbd-controller` does not handle the deletion of `DRBDStoragePool` resources in any way.

> Deleting a resource does not affect the `Storage Pool` created for it in the `Linstor` backend.
If the user recreates the deleted resource with the same name and configuration, the controller will detect that the corresponding `Storage Pools` are already created, so no changes will be made.
The `status.phase` field of the created resource will be set to `Created`.

2. ### Using `DRBDStorageClass` resources

#### Creating a `DRBDStorageClass` resource

- To create a `StorageClass` in `Kubernetes`, you have to create a [DRBDStorageClass](./cr.html#drbdstorageclass) resource and fill in the `spec` field with the required parameters. (Note that you cannot manually create a StorageClass for the drbd.csi.storage.deckhouse.io CSI driver).

- Below is an example of a resource for creating a `StorageClass` based on local volumes only (i.e., no data can be accessed over the network) and with a high data redundancy in a cluster consisting of three zones:

```yaml
apiVersion: storage.deckhouse.io/v1alpha1
kind: DRBDStorageClass
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

- Below is an example of a resource for creating a `StorageClass` with allowed access to data over the network and no redundancy in a cluster where there are no zones (e.g., it is a good fit for testing environments):

```yaml
apiVersion: storage.deckhouse.io/v1alpha1
kind: DRBDStorageClass
metadata:
  name: testclass
spec:
  replication: None
  storagePool: storage-pool-name
  reclaimPolicy: Delete
  topology: Ignored
```

- More examples with different usage scenarios and layouts [can be found here](./layouts.html)

> Before creating the `StorageClass`, the configuration user provides will be validated.
> If errors are found, the `StorageClass` will not be created, and the information about the error will be saved to the `status` field of the `DRBDStorageClass` resource.

The `sds-drbd-controller` will then analyze the user's `DRBDStorageClass` resource and create the corresponding `Storage Class` in `Kubernetes`.

> Please note that all fields of `spec` section of the `DRBDStorageClass` resource are **immutable** except for the `spec.isDefault` field.

The `sds-drbd-controller` will automatically keep the `status` field up to date to reflect the results of the ongoing operations.

#### Updating the `DRBDStorageClass` resource

Currently, the `sds-drbd-controller` only supports changing the `isDefault` field. It is **not possible** to change other configuration parameters of the `StorageClass` created via the `DRBDStorageClass` resource.

#### Deleting the `DRBDStorageClass` resource

You can delete the `StorageClass` in `Kubernetes` by removing its `DRBDStorageClass` resource. 
The `sds-drbd-controller` will detect that the resource has been deleted and carry out all necessary operations to properly delete its associated `StorageClass`.

> The `sds-drbd-controller` will only delete the `StorageClass` associated with the resource if the `status.phase` field of the `DRBDStorageClass` resource is set to `Created`. Otherwise, the controller will only delete the `DRBDStorageClass` resource while its associated `StorageClass` will not be affected.

## Additional features for applications

### Hosting an application "closer" to the data (data locality)

In a hyperconverged infrastructure, you may want your pods to run on the same nodes as their data volumes, as this will help maximize storage performance.

The module provides a custom scheduler for such tasks. It takes into account where exactly the data is stored and tries to schedule pods first on those nodes where the data is available locally.
Any pod that uses sds-drbd volumes will be automatically configured to use this scheduler.

Data locality is determined by the `volumeAccess` parameter when the `DRDBStorageClass` resource is being created.
