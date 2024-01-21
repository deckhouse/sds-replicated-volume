---
title: "The SDS-DRBD module: FAQ"
description: LINSTOR Troubleshooting. What is difference between LVM and LVMThin? LINSTOR performance and reliability notes, comparison to Ceph. How to add existing LINSTOR LVM or LVMThin pool. How to configure Prometheus to use LINSTOR for storing data. Controller's work-flow questions.
---

{{< alert level="warning" >}}
The module is guaranteed to work only in the following cases:
- if stock kernels shipped with the [supported distributions](https://deckhouse.io/documentation/v1/supported_versions.html#linux) are used;
- if a 10 Gbps network is used.

As for any other configurations, the module may work, but its smooth operation is not guaranteed.
{{< /alert >}}

## What is difference between LVM and LVMThin?

In a nutshell::

- LVM is simpler and has performance comparable to that of native disk drives;
- LVMThin allows for snapshots and overprovisioning; however, it is twice as slow.

## How do I get info about the space used?

There are two options:

- Using the Grafana dashboard: navigate to **Dashboards --> Storage --> LINSTOR/DRBD**  
  In the upper right corner, you'll see the amount of space used in the cluster.

  > **Caution!** *Raw* space usage in the cluster is displayed.
  > Suppose you create a volume with two replicas. In this case, these values must be divided by two to see how many such volumes can be in your cluster.

- Using the LINSTOR command line:

  ```shell
  kubectl exec -n d8-sds-drbd deploy/linstor-controller -- linstor storage-pool list
  ```

  > **Caution!** *Raw* space usage for each node in the cluster is displayed.
  > Suppose you create a volume with two replicas. In this case, those two replicas must fully fit on two nodes in your cluster.

## How do I set the default StorageClass?

Set the `spec.IsDefault` field to `true` in the corresponding [DRBDStorageClass](./cr.html#drbdstorageclass) custom resource.

## How do I add the existing LVM or LVMThin pool?

1. Manually add the `storage.deckhouse.io/enabled=true` tag to the Volume Group:
```
vgchange myvg-0 --add-tag storage.deckhouse.io/enabled=true
```
2. This VG will be automatically discovered and a corresponding `LVMVolumeGroup` resource will be created in the cluster for it.
3. You can specify this resource in the [DRBDStoragePool](./cr.html#drbdstoragepool) parameters in the `spec.lvmVolumeGroups[].name` field (note that for the LVMThin pool, you must additionally specify its name in `spec.lvmVolumeGroups[].thinPoolName`).

## How do I evict resources from a node?

* Download the `evict.sh` script on the host that has administrative access to the Kubernetes API server (for the script to work, you need to have `kubectl` and `jq` installed):

  * Download the latest script version from GitHub:

    ```shell
    curl -fsSL -o evict.sh https://raw.githubusercontent.com/deckhouse/deckhouse/main/modules/041-linstor/tools/evict.sh
    chmod 700 evict.sh
    ```

  * Alternatively, download the script from the `deckhouse` pod:

    ```shell
    kubectl -n d8-system cp -c deckhouse $(kubectl -n d8-system get po -l app=deckhouse -o jsonpath='{.items[0].metadata.name}'):/deckhouse/modules/041-linstor/tools/evict.sh ./evict.sh
    chmod 700 evict.sh
    ```

* Fix all faulty LINSTOR resources in the cluster. Run the following command to detect them:

  ```shell
  kubectl -n d8-sds-drbd exec -ti deploy/linstor-controller -- linstor resource list --faulty
  ```

* Check that all the pods in the `d8-sds-drbd` namespace are in the Running state:

  ```shell
  kubectl -n d8-sds-drbd get pods | grep -v Running
  ```

### How do I evict resources from a node without deleting it from LINSTOR and Kubernetes

Run the `evict.sh` script in interactive mode (`--delete-resources-only`):

```shell
./evict.sh --delete-resources-only
```

To run the `evict.sh` script in non-interactive mode, add the `--non-interactive` flag followed by the name of the node to evict the resources from. In this mode, the script will perform all the necessary actions automatically (no user confirmation is required). Example:

```shell
./evict.sh --non-interactive --delete-resources-only --node-name "worker-1"
```

> **Caution!** After the script finishes its job, the node will still be in the Kubernetes cluster albeit in *SchedulingDisabled* status. In LINSTOR, the *AutoplaceTarget=false* property will be set for this node, preventing the LINSTOR scheduler from creating resources on this node.

Run the following command to allow resources and pods to be scheduled on the node again:

```shell
alias linstor='kubectl -n d8-sds-drbd exec -ti deploy/linstor-controller -- linstor'
linstor node set-property "worker-1" AutoplaceTarget
kubectl uncordon "worker-1"
```

Run the following command to check the *AutoplaceTarget* property for all nodes (the AutoplaceTarget field will be empty for nodes that are allowed to host LINSTOR resources):

```shell
alias linstor='kubectl -n d8-sds-drbd exec -ti deploy/linstor-controller -- linstor'
linstor node list -s AutoplaceTarget
```

## Troubleshooting

Problems can occur at different levels of component operation.
This simple cheat sheet will help you quickly navigate through the diagnosis of various problems with the LINSTOR-created volumes:

![LINSTOR cheatsheet](../../images/041-linstor/linstor-debug-cheatsheet.svg)
<!--- Source: https://docs.google.com/drawings/d/19hn3nRj6jx4N_haJE0OydbGKgd-m8AUSr0IqfHfT6YA/edit --->

Some common problems are described below.

### linstor-node fail to start because the drbd module cannot be loaded

Check the status of the `linstor-node` Pods:

```shell
kubectl get pod -n d8-sds-drbd -l app=linstor-node
```

If you see that some of them got stuck in `Init` state, check the DRBD version aw well as the bashible logs on the node:

```shell
cat /proc/drbd
journalctl -fu bashible
```

The most likely reasons why loading the kernel module fails:

- You have the in-tree version of the DRBDv8 module preloaded, whereas LINSTOR requires DRBDv9.
  Check the preloaded module version: `cat /proc/drbd`. If the file is missing, then the module is not preloaded and this is not your case.

- You have Secure Boot enabled.
  Since the DRBD module we provide is compiled dynamically for your kernel (similar to dkms), it is not digitally signed.
  We do not currently support running the DRBD module with a Secure Boot enabled.

### The Pod cannot start due to the `FailedMount` error

#### **The Pod is stuck at the `ContainerCreating` phase**

If the Pod is stuck at the `ContainerCreating` phase, and the following errors are displayed when the `kubectl describe pod` command is invoked:

```text
rpc error: code = Internal desc = NodePublishVolume failed for pvc-b3e51b8a-9733-4d9a-bf34-84e0fee3168d: checking
for exclusive open failed: wrong medium type, check device health
```

... it means that the device is still mounted on one ot the nodes.

Use the command below to see if this is the case:

```shell
alias linstor='kubectl -n d8-sds-drbd exec -ti deploy/linstor-controller -- linstor'
linstor resource list -r pvc-b3e51b8a-9733-4d9a-bf34-84e0fee3168d
```

The `InUse` flag will point to the node on which the device is being used. You will need to manually unmount the disk on that node.

#### **Errors of the `Input/output error` type**

These errors usually occur during the file system (mkfs) creation.

Check `dmesg` on the node where the pod is being run:

```shell
dmesg | grep 'Remote failed to finish a request within'
```

If the command output is not empty (the `dmesg` output contains lines like *"Remote failed to finish a request within ... "*), most likely your disk subsystem is too slow for DRBD to run properly.

## I have deleted the DRBDStoragePool resource, yet its associated Storage Pool in the LINSTOR backend is still there. Is it supposed to be like this?
Yes, this is the expected behavior. Currently, the `SDS-DRBD` module does not process operations when deleting the `DRBDStoragePool` resource.

## I am unable to update the fields in the DRBDStorageClass resource spec. Is this the expected behavior?  
Yes, this is the expected behavior. Only the `isDefault` field is editable in the `spec`. All the other fields in the resource `spec` are made immutable.

## When you delete a DRBDStorageClass resource, its child StorageClass in Kubernetes is not deleted. What can I do in this case?
The child StorageClass is only deleted if the status of the DRBDStorageClass resource is `Created`. Otherwise, you will need to either restore the DRBDStorageClass resource to a working state or delete the StorageClass yourself.

## I noticed that when creating a Storage Pool / Storage Class in the corresponding resource, an error was displayed, but then everything was fine, and the desired entity was created. Is this the expected behavior?
Yes, this is the expected behavior. The module will automatically retry the unsuccessful operation if the error was caused by circumstances beyond the module's control (for example, a momentary disruption in the Kubernetes API).

## I have not found an answer to my question and am having trouble getting the module to work. What do I do?
Information about the reasons for the failure is saved to the `Status.Reason` field of the `DRBDStoragePool` and `DRBDStorageClass` resources. 
If the information provided is not enough to identify the problem, refer to the sds-drbd-controller logs.

## Migrating to DRBDStorageClass

In this module, StorageClasses are managed via the DRBDStorageClass resource. StorageClasses are not supposed to be created manually.

When migrating off the Linstor module, you must delete the old StorageClasses and create new ones via the DRBDStorageClass resource according to the table below.

Note that for the old StorageClass, you have to use the option from the parameter section of the StorageClass itself, whereas when creating a new one, you have to specify the corresponding option in the DRBDStorageClass.

| StorageClass parameter                     | DRBDStorageClass      | Default parameter | Notes                                                     |
|-------------------------------------------|-----------------------|-|----------------------------------------------------------------|
| linstor.csi.linbit.com/placementCount: "1" | replication: "None"   | | A single volume replica with data will be created                  |
| linstor.csi.linbit.com/placementCount: "2" | replication: "Availability" | | Two volume replicas with data will be created                  |
| linstor.csi.linbit.com/placementCount: "3" | replication: "ConsistencyAndAvailability" | Yes | Three volume replicas with data will be created                   |
| linstor.csi.linbit.com/storagePool: "name" | storagePool: "name"   | | Name of the storage pool to use for storage               |
| linstor.csi.linbit.com/allowRemoteVolumeAccess: "false" | volumeAccess: "Local" | | Pods are not allowed to access data volumes remotely (only local access to the disk within the Node is allowed) |

On top of these, the following parameters are available:

- reclaimPolicy (Delete, Retain) - corresponds to the reclaimPolicy parameter of the old StorageClass
- zones - list of zones to be used for hosting resources ( the actual names of the zones in the cloud). Please note that remote access to a volume with data is only possible within a single zone!
- volumeAccess can be "Local" (access is strictly within the node), "EventuallyLocal" (the data replica will be synchronized on the node with the running pod a while after the start), "PreferablyLocal" (remote access to the volume with data is allowed, volumeBindingMode: WaitForFirstConsumer), "Any" (remote access to the volume with data is allowed, volumeBindingMode: Immediate).
- If you need to use `volumeBindingMode: Immediate`, set the volumeAccess parameter of the DRBDStorageClass to `Any`.
