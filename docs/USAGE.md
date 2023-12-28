---
title: "The SDS-DRDB module: configuration examples"
description: The SDS-DRDB controller usage and work-flow examples.
---
{% alert level="warning" %}
The module is guaranteed to work only in the following cases:
- if stock kernels shipped with the [supported distributions](../../supported_versions.html#linux) are used;
- if a 10 Gbps network is used.

As for any other configurations, the module may work, but its smooth operation is not guaranteed.
{% endalert %}

Once the module is enabled, the cluster is automatically configured to use LINSTOR. You only need to configure the storage.

## Configuring LINSTOR storage
In `Deckhouse`, the `sds-drbd-controller` handles the configuration of `LINSTOR` storage. For this, `DRBDStoragePool` and `DRBDStorageClass` [custom resources](link to resources) are created.

- To create a `Storage Pool` in `Linstor`, the user has to manually [create](#creating-drbdstoragepool-resource) the `DRBDStoragePool` resource and populate the `Spec` field by entering the pool type as well as the [LVMVolumeGroup](link to resource) resources used.

- To create a `Storage Class` in `Kubernetes`, the user has to manually [create](#creating-drbdstorageclass-resource) the `DRBDStorageClass` and populate its `Spec` field with all the necessary parameters as well as the `DRBDStoragePool` resources used.

## How the `sds-drbd-controller` works

The controller's operations can be divided into 2 types:

1. ### [DRBDStoragePool](resource) resources
#### Creating a `DRBDStoragePool` resource

The DRBDStoragePool resource allows you to create a Storage Pool in LINSTOR based on the [LVMVolumeGroup](link to the resource) resources you use.
For this, the user has to manually create the resource and populate the `Spec` field (that is, to describe the desired state of the `Storage Pool` in `Linstor`).

> **Caution!** All LVMVolumeGroup resources in the Spec of the DRBDStoragePool resource must reside on different nodes. (You may not refer to multiple LVMVolumeGroup resources located on the same node).

The `sds-drbd-controller` will then process the `DRBDStoragePool` resource defined bby the user and create the corresponding `Storage Pool` in `Linstor`.

> The name of the created `Storage Pool` will match the name of the created `DRBDStoragePool` resource.

Information about the controller's progress and results is available in the Status field of the created DRBDStoragePool resource.

> Before working with LINSTOR, the controller will validate the provided configuration. If an error is detected, it will report the cause of the error. This will prevent Storage Pools with errors from being created in LINSTOR.
>
> If an error occurs when creating a Storage Pool in LINSTOR, the controller will delete the invalid Storage Pool in LINSTOR.

#### Updating the `DRBDStoragePool` resource

You can update the existing configuration of a `Storage Pool` in `Linstor`. To do so, make the desired changes to the `Spec` field of the corresponding resource.

The `sds-drbd-controller` will then validate the new configuration. If it is valid, the controller will update the `Storage Pool` in `Linstor`. The results of this operation will also be reflected in the `Status` field of the `DRBDStoragePool` resource.

> Note that the `Spec.Type` field of the `DRBDStoragePool` resource is **immutable**.
>
> The controller does not respond to changes made by the user in the `Spec.Status` resource field.

#### Deleting the `DRBDStoragePool` resource

Currently, the `sds-drbd-controller` does not handle the deletion of `DRBDStoragePool` resources in any way.

> Deleting a resource does not affect the `Storage Pool` created for it in `Linstor`. 
If the user recreates the deleted resource with the same name and configuration, the controller will detect that the corresponding `Storage Pools` are already created, so no changes will be made.
The `Status.Phase` field of the created resource will be set to `Created`.

2. ### [DRBDStorageClass](resource) resources

#### Creating a `DRBDStorageClass` resource

The `DRBDStorageClass` resource allows you to create the `Storage Class` in `Kubernetes` for the `Storage Pool` in `Linstor` based on the specified `DRBDStoragePool` resources. For this, you have to manually create the resource and populate the `Spec` field to reflect the desired state of the `Storage Class` in `Kubernetes`.

> Before creating the `Storage Class`, the configuration user provides will be validated. If errors are found, the `Storage Class` will not be created, and information about the error will be saved to the `Status` field of the `DRBDStorageClass` resource.

The `sds-drbd-controller` will then analyze the user's `DRBDStorageClass` resource and create the corresponding `Storage Class` in `Kubernetes`.

> Please note that all fields of `spec` section of the `DRBDStorageClass` resource are immutable except for the `spec.isDefault` field.

The `sds-drbd-controller` will automatically keep the Status field up to date to reflect the results of the ongoing operations.

#### Updating the `DRBDStorageClass` resource

Currently, the `sds-drbd-controller` only supports only changing the `isDefault` field. It is **not possible** to change other configuration parameters of the created `Storage Class` by modifying the `DRBDStorageClass` resource.

#### Deleting the `DRBDStorageClass` resource

You can delete the `Storage Class` in `Kubernetes` by removing its `DRBDStorageClass` resource. The `sds-drbd-controller` will detect that the resource has been deleted and carry out all necessary operations to properly delete its associated `Storage Class`.

> The `sds-drbd-controller` will only delete the `StorageClass` associated with the resource if the `status.Phase` field of the `DRBDStorageClass` field of the `DRBDStorageClass` resource is set to `Created`. Otherwise, the controller will not delete the `StorageClass`. The resource will **not be** deleted either.

## Additional features for applications that use LINSTOR storage

In a hyperconverged infrastructure, you may want your Pods to run on the same nodes as their data volumes, as this will help maximize storage performance.

The linstor module provides a custom `linstor` kube-scheduler for such tasks. It takes into account where exactly the data is stored and tries to schedule Pod first on those nodes where data is available locally.

Any Pod that uses linstor volumes will be automatically configured to use the `linstor` scheduler.

Data locality is determined by the `volumeAccess` parameter in a `DRDBStorageClass` resource.