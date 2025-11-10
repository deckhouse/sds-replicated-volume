---
title: "The sds-replicated-volume module"
description: "The sds-replicated-volume module: General Concepts and Principles."
moduleStatus: preview
---

{{< alert level="warning" >}}
The module is only guaranteed to work if the [system requirements](./readme.html#system-requirements-and-recommendations) are met.
As for any other configurations, the module may work, but its smooth operation is not guaranteed.
{{< /alert >}}

This module manages replicated block storage based on `DRBD`. Currently, `LINSTOR` is used as a control-plane/backend (without the possibility of direct user configuration).

The module allows you to create a `Storage Pool` as well as a `StorageClass` by creating [Kubernetes custom resources](./cr.html).

To create a `Storage Pool`, you will need the `LVMVolumeGroup` configured on the cluster nodes. The `LVM` configuration is done by the [sds-node-configurator](/modules/sds-node-configurator/) module.

{{< alert level="warning" >}}
Before enabling the `sds-replicated-volume` module, you must enable the `sds-node-configurator` module.

Data synchronization during volume replication is carried out in synchronous mode only, asynchronous mode is not supported.

If your cluster has only a single node, use `sds-local-volume` instead of `sds-replicated-volume`.
To use `sds-replicated-volume`, a minimum of 3 nodes is required. It is advisable to have 4 or more nodes to mitigate the impact of potential node failures.
{{< /alert >}}

{{< alert level="info" >}}
Available access modes: RWO; RWX — only in DVP;
{{< /alert >}}

After you enable the `sds-replicated-volume` module in the Deckhouse configuration, you will only have to create [ReplicatedStoragePool and ReplicatedStorageClass](./usage.html#configuring-the-linstor-backend).

To ensure the proper functioning of the `sds-replicated-volume` module, follow these steps:

- Enable the [sds-node-configurator](/modules/sds-node-configurator/) module.  
  Ensure that the `sds-node-configurator` module is enabled **before** enabling the `sds-replicated-volume` module.

{{< alert level="warning" >}}
Direct configuration of the LINSTOR backend by the user is prohibited.
{{< /alert >}}

{{< alert level="info" >}}
For working with snapshots, the [snapshot-controller](/modules/snapshot-controller/) module must be connected.
{{< /alert >}}

- Configure LVMVolumeGroup.
  Before creating a StorageClass, create the [LVMVolumeGroup](/modules/sds-node-configurator/cr.html#lvmvolumegroup) resource for the `sds-node-configurator` module on the cluster nodes.

- Create Storage Pools and Corresponding StorageClasses.

  Users are **prohibited** from creating StorageClasses for the `replicated.csi.storage.deckhouse.io` CSI driver.  

  After the `sds-replicated-volume` module is activated in the Deckhouse configuration, the cluster will automatically be set up to work with the LINSTOR backend. You only need to create the [storage pools and StorageClasses](./usage.html#configuring-the-linstor-backend).

The module supports two operating modes: LVM and LVMThin.  
Each mode has its own characteristics, advantages, and limitations. Learn more about the differences [in the FAQ](./faq.html#what-is-difference-between-lvm-and-lvmthin).

## Quickstart guide

Note that all commands must be run on a machine that has administrator access to the Kubernetes API.

### Enabling modules

Enabling the `sds-node-configurator` module:

1. Create a ModuleConfig resource to enable the module:

   ```yaml
   kubectl apply -f - <<EOF
   apiVersion: deckhouse.io/v1alpha1
   kind: ModuleConfig
   metadata:
     name: sds-node-configurator
   spec:
     enabled: true
     version: 1
   EOF
   ```

1. Wait for the `sds-node-configurator` module to reaches the `Ready` state.

   ```shell
   kubectl get module sds-node-configurator -w
   ```

1. Activate the `sds-replicated-volume` module. Before enabling, it is recommended to review the [available settings](./configuration.html).

   The example below launches the module with default settings, which will result in creating service pods for the `sds-replicated-volume` component on all cluster nodes, installing the DRBD kernel module, and registering the CSI driver:

   ```yaml
   kubectl apply -f - <<EOF
   apiVersion: deckhouse.io/v1alpha1
   kind: ModuleConfig
   metadata:
     name: sds-replicated-volume
   spec:
     enabled: true
     version: 1
   EOF
   ```

1. Wait for the `sds-replicated-volume` module to reach the `Ready` state.

   ```shell
   kubectl get module sds-replicated-volume -w
   ```

1. Make sure that all pods in `d8-sds-replicated-volume` and `d8-sds-node-configurator` namespaces are `Running` or `Completed` and are running on all nodes where DRBD resources are intended to be used:
  
   ```shell
   kubectl -n d8-sds-replicated-volume get pod -o wide -w
   kubectl -n d8-sds-node-configurator get pod -o wide -w
   ```

### Configuring storage on nodes

You need to create `LVM` volume groups on the nodes using `LVMVolumeGroup` custom resources. As part of this quickstart guide, we will create a regular `Thick` storage. See [usage examples](./usage.html) to learn more about custom resources.

To configure the storage:

- List all the [BlockDevice](/modules/sds-node-configurator/cr.html#blockdevice) resources available in your cluster:

```shell
kubectl get bd

NAME                                           NODE       CONSUMABLE   SIZE      PATH
dev-0a29d20f9640f3098934bca7325f3080d9b6ef74   worker-0   true         30Gi      /dev/vdd
dev-457ab28d75c6e9c0dfd50febaac785c838f9bf97   worker-0   false        20Gi      /dev/vde
dev-49ff548dfacba65d951d2886c6ffc25d345bb548   worker-1   true         35Gi      /dev/vde
dev-75d455a9c59858cf2b571d196ffd9883f1349d2e   worker-2   true         35Gi      /dev/vdd
dev-ecf886f85638ee6af563e5f848d2878abae1dcfd   worker-0   true         5Gi       /dev/vdb
```


- Create an [LVMVolumeGroup](/modules/sds-node-configurator/cr.html#lvmvolumegroup) resource for `worker-0`:

```yaml
kubectl apply -f - <<EOF
apiVersion: storage.deckhouse.io/v1alpha1
kind: LVMVolumeGroup
metadata:
  name: "vg-1-on-worker-0" # The name can be any fully qualified resource name in Kubernetes. This LVMVolumeGroup resource name will be used to create ReplicatedStoragePool in the future
spec:
  type: Local
  local:
    nodeName: "worker-0"
  blockDeviceSelector:
    matchExpressions:
      - key: kubernetes.io/metadata.name
        operator: In
        values:
          - dev-0a29d20f9640f3098934bca7325f3080d9b6ef74
          - dev-ecf886f85638ee6af563e5f848d2878abae1dcfd
  actualVGNameOnTheNode: "vg-1" # the name of the LVM VG to be created from the above block devices on the node 
EOF
```

- Wait for the created `LVMVolumeGroup` resource to become `Ready`:

```shell
kubectl get lvg vg-1-on-worker-0 -w
```

- The resource becoming `Ready` means that an LVM VG named `vg-1` made up of the `/dev/vdd` and `/dev/vdb` block devices has been created on the `worker-0` node.

  - Next, create an [LVMVolumeGroup](/modules/sds-node-configurator/cr.html#lvmvolumegroup) resource for `worker-1`:

```shell
kubectl apply -f - <<EOF
apiVersion: storage.deckhouse.io/v1alpha1
kind: LVMVolumeGroup
metadata:
  name: "vg-1-on-worker-1"
spec:
  type: Local
  local:
    nodeName: "worker-1"
  blockDeviceSelector:
    matchExpressions:
      - key: kubernetes.io/metadata.name
        operator: In
        values:
          - dev-49ff548dfacba65d951d2886c6ffc25d345bb548
  actualVGNameOnTheNode: "vg-1"
EOF
```

- Wait for the created `LVMVolumeGroup` resource to become `Ready`:

```shell
kubectl get lvg vg-1-on-worker-1 -w
```

- The resource becoming `Ready` means that an LVM VG named `vg-1` made up of the `/dev/vde` block device has been created on the `worker-1` node.

  - Create an [LVMVolumeGroup](/modules/sds-node-configurator/cr.html#lvmvolumegroup) resource for `worker-2`:

```shell
kubectl apply -f - <<EOF
apiVersion: storage.deckhouse.io/v1alpha1
kind: LVMVolumeGroup
metadata:
  name: "vg-1-on-worker-2"
spec:
  type: Local
  local:
    nodeName: "worker-2"
  blockDeviceSelector:
    matchExpressions:
      - key: kubernetes.io/metadata.name
        operator: In
        values:
          - dev-75d455a9c59858cf2b571d196ffd9883f1349d2e
  actualVGNameOnTheNode: "vg-1"
EOF
```

- Wait for the created `LVMVolumeGroup` resource to become `Ready`:

```shell
kubectl get lvg vg-1-on-worker-2 -w
```

- The resource becoming `Ready` means that an LVM VG named `vg-1` made up of the `/dev/vdd` block device has been created on the `worker-2` node.

  - Now that we have all the LVM VGs created on the nodes, create a [ReplicatedStoragePool](./cr.html#replicatedstoragepool) out of those VGs:

```yaml
kubectl apply -f -<<EOF
apiVersion: storage.deckhouse.io/v1alpha1
kind: ReplicatedStoragePool
metadata:
  name: data
spec:
  type: LVM
  lvmVolumeGroups: # Here, specify the names of the LVMVolumeGroup resources you created earlier
    - name: vg-1-on-worker-0
    - name: vg-1-on-worker-1
    - name: vg-1-on-worker-2
EOF

```

- Wait for the created `ReplicatedStoragePool` resource to become `Completed`:

```shell
kubectl get rsp data -w
```

- Confirm that the `data` Storage Pool has been created on nodes `worker-0`, `worker-1` and `worker-2`:

```shell
alias linstor='kubectl -n d8-sds-replicated-volume exec -ti deploy/linstor-controller -- linstor'
linstor sp l

╭─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
┊ StoragePool          ┊ Node     ┊ Driver   ┊ PoolName ┊ FreeCapacity ┊ TotalCapacity ┊ CanSnapshots ┊ State ┊ SharedName                    ┊
╞═════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════╡
┊ DfltDisklessStorPool ┊ worker-0 ┊ DISKLESS ┊          ┊              ┊               ┊ False        ┊ Ok    ┊ worker-0;DfltDisklessStorPool ┊
┊ DfltDisklessStorPool ┊ worker-1 ┊ DISKLESS ┊          ┊              ┊               ┊ False        ┊ Ok    ┊ worker-1;DfltDisklessStorPool ┊
┊ DfltDisklessStorPool ┊ worker-2 ┊ DISKLESS ┊          ┊              ┊               ┊ False        ┊ Ok    ┊ worker-2;DfltDisklessStorPool ┊
┊ data                 ┊ worker-0 ┊ LVM      ┊ vg-1     ┊    35.00 GiB ┊     35.00 GiB ┊ False        ┊ Ok    ┊ worker-0;data                 ┊
┊ data                 ┊ worker-1 ┊ LVM      ┊ vg-1     ┊    35.00 GiB ┊     35.00 GiB ┊ False        ┊ Ok    ┊ worker-1;data                 ┊
┊ data                 ┊ worker-2 ┊ LVM      ┊ vg-1     ┊    35.00 GiB ┊     35.00 GiB ┊ False        ┊ Ok    ┊ worker-2;data                 ┊
╰─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
```

- Create a [ReplicatedStorageClass](./cr.html#replicatedstorageclass) resource for a zone-free cluster (see [use cases](./layouts.html) for details on how zonal ReplicatedStorageClasses work):

```yaml
kubectl apply -f -<<EOF
apiVersion: storage.deckhouse.io/v1alpha1
kind: ReplicatedStorageClass
metadata:
  name: replicated-storage-class
spec:
  storagePool: data # Here, specify the name of the ReplicatedStoragePool you created earlier
  reclaimPolicy: Delete
  topology: Ignored # - note that setting "ignored" means there should be no zones (nodes labeled topology.kubernetes.io/zone) in the cluster
EOF
```

- Wait for the created `ReplicatedStorageClass` resource to become `Created`:

```shell
kubectl get rsc replicated-storage-class -w
```

- Confirm that the corresponding `StorageClass` has been created:

```shell
kubectl get sc replicated-storage-class
```

- If `StorageClass` with the name `replicated-storage-class` is shown, then the configuration of the `sds-replicated-volume` module is complete. Now users can create PVs by specifying `StorageClass` with the name `replicated-storage-class`. Given the above settings, a volume will be created with 3 replicas on different nodes.

## System requirements and recommendations

### Requirements

{{< alert level="info" >}}
Applicable to both single-zone clusters and clusters using multiple availability zones.
{{< /alert >}}

- Use stock kernels provided with [supported distributions](https://deckhouse.io/documentation/v1/supported_versions.html#linux).
  - A network infrastructure with a bandwidth of 10 Gbps or higher is required for network connectivity.
  - To achieve maximum performance, the network latency between nodes should be between 0.5–1 ms. Latencies greater than 5 ms will cause serious performance issues.
  - Do not use another SDS (Software Defined Storage) to provide disks for SDS Deckhouse.

### Recommendations

- Avoid using RAID. The reasons are detailed [in the FAQ](./faq.html#why-is-it-not-recommended-to-use-raid-for-disks-that-are-used-by-the-sds-replicated-volume-module).
  - Use local physical disks. The reasons are detailed [in the FAQ](./faq.html#why-do-you-recommend-using-local-disks-and-not-nas).
  - In order for cluster to be operational, but with performance degradation, network latency should not be higher than 20ms between nodes
