---
title: "The sds-replicated-volume module"
description: "The sds-replicated-volume module: General Concepts and Principles."
moduleStatus: preview
---

{{< alert level="warning" >}}
The module is only guaranteed to work if [requirements](./readme.html#system-requirements-and-recommendations) are met.
As for any other configurations, the module may work, but its smooth operation is not guaranteed.
{{< /alert >}}

This module manages replicated block storage based on `DRBD`. Currently, `LINSTOR` is used as a control-plane. The module allows you to create a `Storage Pool` in `LINSTOR` as well as a `StorageClass` in `Kubernetes` by creating [Kubernetes custom resources](./cr.html). 
To create a `Storage Pool`, you will need the `LVMVolumeGroup` configured on the cluster nodes. The `LVM` configuration is done by the [sds-node-configurator](../../sds-node-configurator/stable/) module.
> **Caution!** Before enabling the `sds-replicated-volume` module, you must enable the `sds-node-configurator` module.
> 
> **Caution!** The user is not allowed to configure the `LINSTOR` backend directly.
>
> **Caution!** Data synchronization during volume replication is carried out in synchronous mode only, asynchronous mode is not supported.

After you enable the `sds-replicated-volume` module in the Deckhouse configuration, your cluster will be automatically set to use the `LINSTOR` backend. You will only have to create [storage pools and StorageClasses](./usage.html#configuring-the-linstor-backend).

> **Caution!** The user is not allowed to create a `StorageClass` for the replicated.csi.storage.deckhouse.io CSI driver.

Two modes are supported: LVM and LVMThin.
Each mode has its advantages and disadvantages. Read [FAQ](./faq.html#what-is-difference-between-lvm-and-lvmthin) to learn more and compare them.

## Quickstart guide

Note that all commands must be run on a machine that has administrator access to the Kubernetes API.

### Enabling modules

- Enable the sds-node-configurator module

```shell
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

- Wait for it to become `Ready`. At this stage, you do NOT need to check the pods in the `d8-sds-node-configurator` namespace.

```shell
kubectl get module sds-node-configurator -w
```

- Enable the `sds-replicated-volume` module. Refer to the [configuration](./configuration.html) to learn more about module settings. In the example below, the module is launched with the default settings. This will result in the following actions across all cluster nodes:
  - installation of the `DRBD` kernel module;
  - registration of the CSI driver;
  - launch of service pods for the `sds-replicated-volume` components.

```shell
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

- Wait for the module to become `Ready`.

```shell
kubectl get module sds-replicated-volume -w
```

- Make sure that all pods in `d8-sds-replicated-volume` and `d8-sds-node-configurator` namespaces are `Running` or `Completed` and are running on all nodes where `DRBD` resources are intended to be used.
  
```shell
kubectl -n d8-sds-replicated-volume get pod -owide -w
kubectl -n d8-sds-node-configurator get pod -o wide -w
```

### Configuring storage on nodes

You need to create `LVM` volume groups on the nodes using `LVMVolumeGroup` custom resources. As part of this quickstart guide, we will create a regular `Thick` storage. See [usage examples](./usage.html) to learn more about custom resources.

To configure the storage:

- List all the [BlockDevice](../../sds-node-configurator/stable/cr.html#blockdevice) resources available in your cluster:

```shell
kubectl get bd

NAME                                           NODE       CONSUMABLE   SIZE      PATH
dev-0a29d20f9640f3098934bca7325f3080d9b6ef74   worker-0   true         30Gi      /dev/vdd
dev-457ab28d75c6e9c0dfd50febaac785c838f9bf97   worker-0   false        20Gi      /dev/vde
dev-49ff548dfacba65d951d2886c6ffc25d345bb548   worker-1   true         35Gi      /dev/vde
dev-75d455a9c59858cf2b571d196ffd9883f1349d2e   worker-2   true         35Gi      /dev/vdd
dev-ecf886f85638ee6af563e5f848d2878abae1dcfd   worker-0   true         5Gi       /dev/vdb
```


- Create an [LVMVolumeGroup](../../sds-node-configurator/stable/cr.html#lvmvolumegroup) resource for `worker-0`:

```yaml
kubectl apply -f - <<EOF
apiVersion: storage.deckhouse.io/v1alpha1
kind: LvmVolumeGroup
metadata:
  name: "vg-1-on-worker-0" # The name can be any fully qualified resource name in Kubernetes. This LvmVolumeGroup resource name will be used to create ReplicatedStoragePool in the future
spec:
  type: Local
  blockDeviceNames:  # specify the names of the BlockDevice resources that are located on the target node and whose CONSUMABLE is set to true. Note that the node name is not specified anywhere since it is derived from BlockDevice resources.
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

- Next, create an [LVMVolumeGroup](../../sds-node-configurator/stable/cr.html#lvmvolumegroup) resource for `worker-1`:

```shell
kubectl apply -f - <<EOF
apiVersion: storage.deckhouse.io/v1alpha1
kind: LvmVolumeGroup
metadata:
  name: "vg-1-on-worker-1"
spec:
  type: Local
  blockDeviceNames:
    - dev-49ff548dfacba65d951d2886c6ffc25d345bb548
  actualVGNameOnTheNode: "vg-1"
EOF
```

- Wait for the created `LVMVolumeGroup` resource to become `Ready`:

```shell
kubectl get lvg vg-1-on-worker-1 -w
```

- The resource becoming `Ready` means that an LVM VG named `vg-1` made up of the `/dev/vde` block device has been created on the `worker-1` node.

- Create an [LVMVolumeGroup](../../sds-node-configurator/stable/cr.html#lvmvolumegroup) resource for `worker-2`:

```shell
kubectl apply -f - <<EOF
apiVersion: storage.deckhouse.io/v1alpha1
kind: LvmVolumeGroup
metadata:
  name: "vg-1-on-worker-2"
spec:
  type: Local
  blockDeviceNames:
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
  lvmVolumeGroups: # Here, specify the names of the LvmVolumeGroup resources you created earlier
    - name: vg-1-on-worker-0
    - name: vg-1-on-worker-1
    - name: vg-1-on-worker-2
EOF

```

- Wait for the created `ReplicatedStoragePool` resource to become `Completed`:

```shell
kubectl get rsp data -w
```

- Confirm that the `data` Storage Pool has been created on nodes `worker-0`, `worker-1` and `worker-2` in LINSTOR:

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
- Stock kernels shipped with the [supported distributions](https://deckhouse.io/documentation/v1/supported_versions.html#linux).
- High-speed 10Gbps network.
- Do not use another SDS (Software defined storage) to provide disks to our SDS.

### Recommendations

- Avoid using RAID. The reasons are detailed in the [FAQ](./faq.html#why-is-it-not-recommended-to-use-raid-for-disks-that-are-used-by-the-sds-replicated-volume-module).

- Use local physical disks. The reasons are detailed in the [FAQ](./faq.html#why-do-you-recommend-using-local-disks-and-not-nas).
