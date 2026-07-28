---
title: "The sds-replicated-volume module"
description: "The sds-replicated-volume module: General Concepts and Principles."
moduleStatus: preview
---

This module manages replicated block storage based on `DRBD`. `LINSTOR` is used as the control plane (direct backend configuration by the user is prohibited).

The module lets you create a Storage Pool and a StorageClass by creating [Kubernetes custom resources](./cr.html).

To create a Storage Pool, configure [LVMVolumeGroup](/modules/sds-node-configurator/cr.html#lvmvolumegroup) resources on the cluster nodes. LVM is configured by the [sds-node-configurator](/modules/sds-node-configurator/) module.

Supported access modes: `RWO`; `RWX` — only in DVP. Data synchronization during volume replication runs in synchronous mode only; asynchronous mode is not supported.

The module supports the `LVM` and `LVMThin` modes. Learn more about the differences [in the FAQ](./faq.html#what-is-difference-between-lvm-and-lvmthin).

After you enable the module, create [ReplicatedStoragePool and ReplicatedStorageClass](./usage.html#configuring-the-linstor-backend).

## Quickstart guide

Run all commands on a machine that has administrator access to the Kubernetes API.

### Enabling modules

1. Create a `ModuleConfig` resource to enable the [sds-node-configurator](/modules/sds-node-configurator/) module:

   ```yaml
   d8 k apply -f - <<EOF
   apiVersion: deckhouse.io/v1alpha1
   kind: ModuleConfig
   metadata:
     name: sds-node-configurator
   spec:
     enabled: true
     version: 1
   EOF
   ```

1. Wait for the `sds-node-configurator` module to reach the `Ready` state:

   ```shell
   d8 k get module sds-node-configurator -w
   ```

1. Enable the `sds-replicated-volume` module. Before enabling, review the [available settings](./configuration.html).

   The example below starts the module with default settings: service pods are created on all cluster nodes, the DRBD kernel module is installed, and the CSI driver is registered:

   ```yaml
   d8 k apply -f - <<EOF
   apiVersion: deckhouse.io/v1alpha1
   kind: ModuleConfig
   metadata:
     name: sds-replicated-volume
   spec:
     enabled: true
     version: 2
   EOF
   ```

1. Wait for the `sds-replicated-volume` module to reach the `Ready` state:

   ```shell
   d8 k get module sds-replicated-volume -w
   ```

1. Make sure that all pods in the `d8-sds-replicated-volume` and `d8-sds-node-configurator` namespaces are in the `Running` or `Completed` status and run on all nodes where you plan to use DRBD resources:

   ```shell
   d8 k -n d8-sds-replicated-volume get pod -o wide -w
   d8 k -n d8-sds-node-configurator get pod -o wide -w
   ```

### Selecting data nodes

Specify the [settings.dataNodes.nodeSelector](./configuration.html#parameters-datanodes-nodeselector) parameter when enabling the module.

Labels `storage.deckhouse.io/sds-replicated-volume-*` that were already added are not removed automatically: the current control plane has no automatic data eviction from nodes.

To remove module resources from a node without removing the node from the cluster:

1. On any master node, run the [data eviction script](./faq.html#example-of-removing-resources-from-a-node-without-removing-the-node-itself) `/opt/deckhouse/sbin/evict.sh` with the `--delete-resources-only` parameter.
1. After eviction, remove the module labels from the node and remove the node from LINSTOR:

   ```shell
   export NODE_NAME=<node-name>
   d8 k get node $NODE_NAME -o jsonpath='{.metadata.labels}' | jq -r 'keys[] | select(startswith("storage.deckhouse.io/sds-replicated-volume-"))' | while read label; do
     d8 k label node $NODE_NAME "$label"-
   done
   d8 k -n d8-sds-replicated-volume exec -ti deploy/linstor-controller -- linstor node lost $NODE_NAME
   ```

### Configuring storage on nodes

Create LVM volume groups using [LVMVolumeGroup](/modules/sds-node-configurator/cr.html#lvmvolumegroup) resources. This quickstart creates Thick storage. For details, see [usage examples](./usage.html).

1. List all [BlockDevice](/modules/sds-node-configurator/cr.html#blockdevice) resources available in the cluster:

   ```shell
   d8 k get bd

   NAME                                           NODE       CONSUMABLE   SIZE      PATH
   dev-0a29d20f9640f3098934bca7325f3080d9b6ef74   worker-0   true         30Gi      /dev/vdd
   dev-457ab28d75c6e9c0dfd50febaac785c838f9bf97   worker-0   false        20Gi      /dev/vde
   dev-49ff548dfacba65d951d2886c6ffc25d345bb548   worker-1   true         35Gi      /dev/vde
   dev-75d455a9c59858cf2b571d196ffd9883f1349d2e   worker-2   true         35Gi      /dev/vdd
   dev-ecf886f85638ee6af563e5f848d2878abae1dcfd   worker-0   true         5Gi       /dev/vdb
   ```

1. Create an [LVMVolumeGroup](/modules/sds-node-configurator/cr.html#lvmvolumegroup) resource for the `worker-0` node:

   ```shell
   d8 k apply -f - <<EOF
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

1. Wait for the `LVMVolumeGroup` resource to become `Ready`:

   ```shell
   d8 k get lvg vg-1-on-worker-0 -w
   ```

   When the resource is `Ready`, an LVM VG named `vg-1` made up of `/dev/vdd` and `/dev/vdb` has been created on `worker-0`.

1. Create an [LVMVolumeGroup](/modules/sds-node-configurator/cr.html#lvmvolumegroup) resource for the `worker-1` node:

   ```shell
   d8 k apply -f - <<EOF
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

1. Wait for the `LVMVolumeGroup` resource to become `Ready`:

   ```shell
   d8 k get lvg vg-1-on-worker-1 -w
   ```

   When the resource is `Ready`, an LVM VG named `vg-1` made up of `/dev/vde` has been created on `worker-1`.

1. Create an [LVMVolumeGroup](/modules/sds-node-configurator/cr.html#lvmvolumegroup) resource for the `worker-2` node:

   ```shell
   d8 k apply -f - <<EOF
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

1. Wait for the `LVMVolumeGroup` resource to become `Ready`:

   ```shell
   d8 k get lvg vg-1-on-worker-2 -w
   ```

   When the resource is `Ready`, an LVM VG named `vg-1` made up of `/dev/vdd` has been created on `worker-2`.

1. Create a [ReplicatedStoragePool](./cr.html#replicatedstoragepool) from the LVM VGs:

   ```shell
   d8 k apply -f -<<EOF
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

1. Wait for the `ReplicatedStoragePool` resource to become `Completed`:

   ```shell
   d8 k get rsp data -w
   ```

1. Confirm that the `data` Storage Pool has been created on nodes `worker-0`, `worker-1`, and `worker-2`:

   ```shell
   alias linstor='d8 k -n d8-sds-replicated-volume exec -ti deploy/linstor-controller -- linstor'
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

1. Create a [ReplicatedStorageClass](./cr.html#replicatedstorageclass) resource for a zone-free cluster (for zonal scenarios, see [use cases](./layouts.html)):

   ```shell
   d8 k apply -f -<<EOF
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

1. Wait for the `ReplicatedStorageClass` resource to become `Created`:

   ```shell
   d8 k get rsc replicated-storage-class -w
   ```

1. Confirm that the corresponding StorageClass has been created:

   ```shell
   d8 k get sc replicated-storage-class
   ```

   If a StorageClass named `replicated-storage-class` is shown, the module configuration is complete. Users can create PVs by specifying this StorageClass. With the settings above, a volume is created with three replicas on different nodes.

## System requirements and recommendations

The module is only guaranteed to work if the requirements below are met. For other configurations, the module may work, but smooth operation is not guaranteed.

### Requirements

The cluster must meet the following requirements (for both single-zone and multi-zone clusters):

- Before enabling `sds-replicated-volume`, enable the [sds-node-configurator](/modules/sds-node-configurator/) module.
- Connect the [snapshot-controller](/modules/snapshot-controller/) module.
- Use at least 3 nodes. Prefer 4 or more to mitigate node failures. If the cluster has a single node, use [sds-local-volume](/modules/sds-local-volume/) instead of `sds-replicated-volume`.
- Do not configure the LINSTOR backend directly.
- Do not manually create a StorageClass for the `replicated.csi.storage.deckhouse.io` CSI driver.
- Use stock kernels provided with [supported distributions](/products/kubernetes-platform/documentation/v1/supported_versions.html#linux).
- Use network infrastructure with a bandwidth of 10 Gbps or higher.
- For maximum performance, keep network latency between nodes within 0.5–1 ms. Latencies greater than 5 ms cause serious performance issues.
- Do not use another SDS (Software Defined Storage) to provide disks for SDS Deckhouse.
- For DRBD replication to work, allow communication between nodes on ports `7000–7999` using the UDP protocol. For details, see the table ["Traffic Between Nodes"](/products/kubernetes-platform/documentation/v1/reference/network_interaction.html#traffic-between-nodes). If needed, override the port range with the [`drbdPortRange` setting](./configuration.html#parameters-drbdportrange) by specifying `minPort` and `maxPort`.

  After changing `drbdPortRange`, restart the LINSTOR controller for the new settings to take effect. Existing DRBD resources keep their assigned ports.

### Recommendations

Follow these recommendations when planning storage:

- Avoid using RAID. Details are [in the FAQ](./faq.html#why-is-it-not-recommended-to-use-raid-for-disks-that-are-used-by-the-sds-replicated-volume-module).
- Use local physical disks. Details are [in the FAQ](./faq.html#why-do-you-recommend-using-local-disks-and-not-nas).
- For the cluster to stay operational with degraded performance, network latency between nodes must not exceed 10 ms.
- For guaranteed data consistency, use [ReplicatedStorageClass](./cr.html#replicatedstorageclass) with the `ConsistencyAndAvailability` replication mode ([`spec.replication`](./cr.html#replicatedstorageclass-v1alpha1-spec-replication)) — this mode is used by default.

  **Caution.** Changing the mode to `Availability` may lead to a split brain and data loss if network connectivity fails.
