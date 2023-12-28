---
title: "The SDS-DRBD module: FAQ"
description: LINSTOR Troubleshooting. What is difference between LVM and LVMThin? LINSTOR performance and reliability notes, comparison to Ceph. How to add existing LINSTOR LVM or LVMThin pool. How to configure Prometheus to use LINSTOR for storing data. Controller's work-flow questions.
---
{% alert level="warning" %}
The module is guaranteed to work only in the following cases:
- if stock kernels shipped with the [supported distributions](../../supported_versions.html#linux) are used;
- if a 10 Gbps network is used.

As for any other configurations, the module may work, but its smooth operation is not guaranteed.
{% endalert %}

## What is difference between LVM and LVMThin?

In a nutshell::

- LVM is simpler and has performance comparable to that of native drives;
- LVMThin allows snapshots and overprovisioning, but it is twice as slow.

## LINSTOR performance and reliability; comparing it to Ceph

{% alert %}
Recommended reading: ["Comparing Ceph, LINSTOR, Mayastor, and Vitastor storage performance in Kubernetes"](https://www.reddit.com/r/kubernetes/comments/v3tzze/comparing_ceph_linstor_mayastor_and_vitastor/).
{% endalert %}

We approach this issue from a practical point of view. In practice, a difference of a few tens of percent never matters. What matters is a difference of a factor of two or more.

Comparison criteria:

- Sequential read and write are irrelevant, because on any technology they are always up against the network (10 Gbps vs. 1 Gbps). From a practical standpoint, this metric can be completely ignored;
- Random read and write (1 Gbps vs. 10 Gbps):
    - DRBD + LVM is 5 times better than Ceph RBD (latency is 5 times less while IOPS is 5 times higher);
    - DRBD + LVM is 2 times better than DRBD + LVMThin.
- If one of the replicas is local, the read speed will be about the same as the speed of the storage device;
- If there are no replicas on local storage, the write speed will be roughly limited to half the network bandwidth for two replicas, or ⅓ network bandwidth for three replicas;
- With a large number of clients (more than 10, at iodepth 64), Ceph lag tends to increase (up to 10 times); it consumes significantly more CPU resources.

In practice, there are only three significant factors and it does not matter which parameters you change:

- **Read locality** — if all reads are local, they run at the speed (throughput, IOPS, latency) of the local disk (the difference is nearly unnoticeable);
- **1 network hop when writing** — in DRBD, the replication is performed by the *client*, and in Ceph, by the *server*. Thus, Ceph's latency for writes is always at least twice that of DRBD;
- **Code complexity** — latency of calculations on the datapath (CPU instructions are run for each I/O operation). Here, DRBD + LVM is simpler than DRBD + LVMThin, and much simpler than Ceph RBD.

## Which implementation to use in which situation?

By default, the module uses two replicas (the third `diskless` replica is created automatically; it is used for quorum). This approach ensures protection against split-brain and sufficient storage reliability. However, keep in mind the following peculiarities:

- When one of the replicas (replica A) is unavailable, data is written to a single replica (replica B) only. THis means that:
    - If at this moment the second replica (replica B) becomes unavailable, writing and reading will fail;
    - If at the same time the second replica (replica B) is irretrievably lost, the data will be partially lost (since there is only the outdated replica A);
    - If the old replica (replica A) is also irretrievably lost, the data will be lost completely.
- If the second replica is turned off, both replicas must be available to turn it back on (without operator intervention). This is necessary to correctly handle the split-brain situation;
- Enabling a third replica addresses both problems (at least two copies of data are available at any given time), but increases the overhead (network, disk).

It is strongly recommended to have one local replica. This doubles the possible write bandwidth (with two replicas) and significantly increases the read speed. But even if there is no local replica, things will still work fine, except that reads will be done over the network and there will be double network utilization on writes.

Depending on the task, you may choose one of the following:

- DRBD + LVM — faster (x2) and more reliable (LVM is simpler);
- DRBD + LVMThin — support for snapshots and overprovisioning.

## How do I get info about the space used?

There are two ways:

- Using the Grafana dashboard: navigate to **Dashboards --> Storage --> LINSTOR/DRBD**  
  In the upper right corner, you'll see the space used in the cluster.

  > **Attention!** *Raw* space usage in the cluster is displayed.
  > Suppose you create a volume with two replicas. In this case, these values must be divided by two to see how many such volumes can be in your cluster.

- Using the LINSTOR command line:

  ```shell
  kubectl exec -n d8-sds-drbd deploy/linstor-controller -- linstor storage-pool list
  ```

  > **Attention!** *Raw* space usage for each node in the cluster is displayed.
  > Suppose you create a volume with two replicas. In this case, those two replicas must fully fit on two nodes in your cluster.

## How do I set the default StorageClass?

Set the corresponding [DRBDStorageClass](link to a resource) CR field `spec.IsDefault` to `true`.

## How do I add an existing LVM or LVMThin-pool?

Create a new CR [DRBDStoragePool](resource). In the resource `spec.Lvmvolumegroups.Name`, select the corresponding [LVMVolumeGroup](link to a resource) with the `LVMThin-pool` name in `spec.Lvmvolumegroups.Thinpoolname` if nessesary.

## How do I configure Prometheus to use LINSTOR to store data?

Steps to configure Prometheus to use LINSTOR for storing data are as follows:

- [Configure](configuration.html#linstor-storage-configuration) the storage-pools and StorageClass;
- Specify the [longtermStorageClass](../300-prometheus/configuration.html#parameters-longtermstorageclass) and [storageClass](../300-prometheus/configuration.html#parameters-storageclass) parameters in the [prometheus](../300-prometheus/) module config.

  Example:

  ```yaml
  apiVersion: deckhouse.io/v1alpha1
  kind: ModuleConfig
  metadata:
    name: prometheus
  spec:
    version: 2
    enabled: true
    settings:
      longtermStorageClass: linstor-data-r2
      storageClass: linstor-data-r2
  ```

- Wait for the Prometheus Pods to restart.

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

### How do I evict Resources from a Node without deleting It from LINSTOR and Kubernetes

Run the `evict.sh` script in interactive `--delete-resources-only` mode:

```shell
./evict.sh --delete-resources-only
```

To run the `evict.sh` script in non-interactive mode, add the `--non-interactive` flag followed by the name of the node to evict the resources from. In this mode, the script will perform all the necessary actions automatically (no user confirmation is required). Example:

```shell
./evict.sh --non-interactive --delete-resources-only --node-name "worker-1"
```

> **Note!** After the script finishes its job, the node will still be in the Kubernetes cluster albeit in *SchedulingDisabled* status. In LINSTOR, the *AutoplaceTarget=false* property will be set for this node, preventing the LINSTOR scheduler from creating resources on this node.

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

### Evict Resources from a Node and remove it from LINSTOR and Kubernetes

Run the `evict.sh` script in interactive mode with the `--delete-node` flag and enter the node to be deleted:

```shell
./evict.sh --delete-node
```

To run the `evict.sh` script in non-interactive mode, add the `--non-interactive` flag when invoking it, followed by the name of the node to delete. In this mode, the script will run automatically without waiting for the user confirmation. Example:

```shell
./evict.sh --non-interactive --delete-node --node-name "worker-1"
```

  > **Caution!** The script will delete the node from both Kubernetes and LINSTOR.

In this `--delete-node` mode, resources are not physically removed from the node. To clean up the node, log in to it and do the following:

  > **Caution!** These actions will destroy all your data on the node.

* Retrieve and remove all the volume groups on the node that were used for the LINSTOR LVM storage pools:

  ```shell
  vgs -o+tags | awk 'NR==1;$NF~/linstor-/'
  vgremove -y <vg names from previous command>
  ```

* Retrieve and remove all the logical volumes on the node that were used for the LINSTOR LVM_THIN storage pools:

  ```shell
  lvs -o+tags | awk 'NR==1;$NF~/linstor-/'
  lvremove -y /dev/<vg name from previous command>/<lv name from previous command>
  ```

* To continue cleaning, follow [the instructions](../040-node-manager/faq.html#how-to-clean-up-a-node-for-adding-to-the-cluster) (starting from step two).

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

If you see that some of them get stuck in `Init` state, check the DRBD version and the bashible logs on the node:

```shell
cat /proc/drbd
journalctl -fu bashible
```

The most likely reasons why loading the kernel module fails:

- There may be an in-tree kernel version of the DRBDv8 module pre-loaded. However, LINSTOR requires DRBDv9.
  Check the pre-loaded module version: `cat /proc/drbd`. If the file is missing, then the module is not loaded and this is not your case.

- You have Secure Boot enabled.
  Since the DRBD module we provide is compiled dynamically for your kernel (similar to dkms), it is not digitally signed.
  We do not currently support running the DRBD module with a Secure Boot enabled.

### Pod cannot start with the `FailedMount` error

#### **Pod is stuck in the `ContainerCreating` phase**

If the Pod is stuck in the `ContainerCreating` phase, and the following errors are displayed when the `kubectl describe pod` command is invoked:

```text
rpc error: code = Internal desc = NodePublishVolume failed for pvc-b3e51b8a-9733-4d9a-bf34-84e0fee3168d: checking
for exclusive open failed: wrong medium type, check device health
```

... it means that device is still mounted to some other node.

To check it, use the following command:

```shell
linstor resource list -r pvc-b3e51b8a-9733-4d9a-bf34-84e0fee3168d
```

The `InUse` flag indicates which node the device is being used on.

#### **Pod cannot start due to missing CSI driver**

Here is an example of the error when running `kubectl describe pod`:

```text
kubernetes.io/csi: attachment for pvc-be5f1991-e0f8-49e1-80c5-ad1174d10023 failed: CSINode b-node0 does not
contain driver linstor.csi.linbit.com
```

Check the status of the `linstor-csi-node` Pods:

```shell
kubectl get pod -n d8-sds-drbd -l app.kubernetes.io/component=csi-node
```

Most likely, they are stuck in the `Init` state, waiting for the node to change its status to `Online` in LINSTOR. Run the following command to see the node list:

```shell
linstor node list
```

If you see any nodes in the `EVICTED` state, then they have been unavailable for 2 hours. Run the following command to join them to the cluster:

```shell
linstor node rst <name>
```

#### **`Input/output`` errors**

Such errors usually occur while the file system (mkfs) is being created.

Check `dmesg` on the node where your Pod is running:

```shell
dmesg | grep 'Remote failed to finish a request within'
```

If you get any output (there are "Remote failed to finish a request within ..." lines in the `dmesg` output), it is likely that your disk subsystem is too slow for DRBD to function properly.

## Does the SDS-DRBD module automatically create its custom resources based on the existing Storage Pools in Linstor or Storage Classes in Kubernetes?
No, the `SDS-DRBD` module does not automatically create or delete the `DRBDStoragePool` and `DRBDStorageClass`. Only the user can create them. The controller merely reflects the current state of the respective operation in the Status field.

## There are "zones" in the spec field of the DRBDStorageClass resource. What are these?
Zones are a custom abstraction to define which nodes to create volumes on. A node’s membership in a particular zone is displayed in the node's labels under the `topology.kubernetes.io/zone` key.

## I deleted the DRBDStoragePool resource, but the corresponding Storage Pool in Linstor is still there. Is that the expected behavior?
Yes. Currently, the `SDS-DRBD` module does not handle operations when deleting the `DRBDStoragePool` resource.

## I cannot update the spec field of the DRBDStorageClass resource. Is this expected behavior?
Yes, this is expected behavior. The `isDefault` field is the only one that can be modified. All other `spec` fields are immutable.

## I cannot delete the DRBDStorageClass resource along with the associated Storage Class in Kubernetes. What should I do?
Please check the `Status.Phase` field of the resource you are trying to delete. The `SDS-DRBD` controller will perform the necessary deletion operations only if the this field has the `Created` state.

> The `Status.Reason`` field reflects the reasons for the different status, if any.

## Do I need to manually clear the invalid Storage Pools in Linstor in case the creation via the DRDBStoragePool resource fails?
No, our module takes care of this. We are aware that `Linstor` can create invalid `Storage Pools`. Therefore, in case of a failed creation, the controller will automatically remove the incorrectly created `Storage Pool` in `Linstor`.

> If the resource fails to be validated by the controller, no creation of the Storage Pool will occur.

## I noticed that when creating a Storage Pool / Storage Class in the corresponding resource, an error was displayed, but then everything was fine, and the desired entity was created. Is this expected behavior?
Yes, this is expected behavior. The module will automatically retry the unsuccessful operation if the error was caused by circumstances beyond the module's control (for example, a momentary disruption in the Kubernetes API).

## I have not found an answer to my question and am having trouble getting the module to work. What do I do?
Information about the reasons for the failure should be displayed in the `Status.Reason` field of the `DRBDStoragePool` and `DRBDStorageClass` resources. 
If the information provided is not enough to identify the problem, refer to the controller logs.
