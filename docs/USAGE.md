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
All fields in the `spec` of the ReplicatedStorageClass resource are **immutable**.
{{< /alert >}}

The `status` field is updated by the `sds-replicated-volume-controller` to show the results of the operations.

#### Updating the ReplicatedStorageClass resource

It is currently **not possible** to change the parameters of a StorageClass created via the [ReplicatedStorageClass](./cr.html#replicatedstorageclass) resource.

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
