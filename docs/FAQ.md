---
title: "The sds-replicated-volume module: FAQ"
description: LINSTOR Troubleshooting. What is difference between LVM and LVMThin? LINSTOR performance and reliability notes, comparison to Ceph. How to add existing LINSTOR LVM or LVMThin pool. How to configure Prometheus to use LINSTOR for storing data. Controller's work-flow questions.
---

{{< alert level="warning" >}}
The module is only guaranteed to work if [requirements](./readme.html#system-requirements-and-recommendations) are met.
As for any other configurations, the module may work, but its smooth operation is not guaranteed.
{{< /alert >}}

## What is difference between LVM and LVMThin?

- LVM is simpler and has high performance that is similar to that of native disk drives, but it does not support snapshots;
- LVMThin allows for snapshots and overprovisioning; however, it is slower than LVM.

{{< alert level="warning" >}}
Overprovisioning in LVMThin should be used with caution, monitoring the availability of free space in the pool (The cluster monitoring system generates separate events when the free space in the pool reaches 20%, 10%, 5%, and 1%).

In case of no free space in the pool, degradation in the module's operation as a whole will be observed, and there is a real possibility of data loss!
{{< /alert >}}

## How do I get info about the space used?

There are two options:

1. Using the Grafana dashboard:

* Navigate to **Dashboards --> Storage --> LINSTOR/DRBD**  
  In the upper right corner, you'll see the amount of space used in the cluster.

  > **Caution!** *Raw* space usage in the cluster is displayed. Suppose you create a volume with two replicas. In this case, these values must be divided by two to see how many such volumes can be in your cluster.

2. Using the LINSTOR command line:

  ```shell
  kubectl exec -n d8-sds-replicated-volume deploy/linstor-controller -- linstor storage-pool list
  ```

  > **Caution!** *Raw* space usage for each node in the cluster is displayed. Suppose you create a volume with two replicas. In this case, those two replicas must fully fit on two nodes in your cluster.

## How do I set the default StorageClass?

Set the `spec.IsDefault` field to `true` in the corresponding [ReplicatedStorageClass](./cr.html#replicatedstorageclass) custom resource.

## How do I add the existing LVM Volume Group or LVMThin pool?

1. Manually add the `storage.deckhouse.io/enabled=true` LVM tag to the Volume Group:

   ```shell
   vgchange myvg-0 --add-tag storage.deckhouse.io/enabled=true
   ```

   This VG will be automatically discovered and a corresponding `LVMVolumeGroup` resource will be created in the cluster for it.

2. Specify this resource in the [ReplicatedStoragePool](./cr.html#replicatedstoragepool) parameters in the `spec.lvmVolumeGroups[].name` field (note that for the LVMThin pool, you must additionally specify its name in `spec.lvmVolumeGroups[].thinPoolName`).

## How to increase the limit on the number of DRBD devices / change the ports through which DRBD clusters communicate with each other?

To increase the limit on the number of DRBD devices / change the ports through which DRBD clusters communicate with each other, you can use the drbdPortRange setting. By default, DRBD resources use TCP ports 7000-7999. These values can be redefined using minPort and maxPort.

{{< alert level="warning" >}}
Changing the drbdPortRange minPort/maxPort will not affect existing DRBD resources; they will continue to operate on their original ports.

After changing the drbdPortRange values, the linstor controller needs to be restarted.
{{< /alert >}}

## How to properly reboot a node with DRBD resources

{{< alert level="warning" >}}
For greater stability of the module, it is not recommended to reboot multiple nodes simultaneously.
{{< /alert >}}

1. Drain the node.

  ```shell
  kubectl drain test-node-1 --ignore-daemonsets --delete-emptydir-data
  ```

2. Check that there are no problematic resources in DRBD / resources in SyncTarget. If there are any, wait for synchronization / take measures to restore normal operation.

  ```shell
  # kubectl -n d8-sds-replicated-volume exec -t deploy/linstor-controller -- linstor r l --faulty
  Defaulted container "linstor-controller" out of: linstor-controller, kube-rbac-proxy
  +----------------------------------------------------------------+
  | ResourceName | Node | Port | Usage | Conns | State | CreatedOn |
  |================================================================|
  +----------------------------------------------------------------+
  ```

3. Reboot the node and wait for the synchronization of all DRBD resources. Then uncordon the node. If another node needs to be rebooted, repeat the algorithm.

  ```shell
  # kubectl uncordon test-node-1
  node/test-node-1 uncordoned
  ```

## How do I free some space on storage pool by moving resources to another

1. Check the LINSTOR storage pool: `kubectl exec -n d8-sds-replicated-volume deploy/linstor-controller -- linstor storage-pool list -n OLD_NODE`

2. Check the LINSTOR volumes: `kubectl exec -n d8-sds-replicated-volume deploy/linstor-controller -- linstor volume list -n OLD_NODE`

3. Search for replicas you want to move `kubectl exec -n d8-sds-replicated-volume deploy/linstor-controller -- linstor resource list-volumes`

4. Move this replicas to other nodes (only 1-2 replicas sync simultaneously):
``` shell
kubectl exec -n d8-sds-replicated-volume deploy/linstor-controller -- linstor --yes-i-am-sane-and-i-understand-what-i-am-doing resource create NEW_NODE RESOURCE_NAME
kubectl exec -n d8-sds-replicated-volume deploy/linstor-controller -- linstor resource-definition wait-sync RESOURCE_NAME
kubectl exec -n d8-sds-replicated-volume deploy/linstor-controller -- linstor --yes-i-am-sane-and-i-understand-what-i-am-doing resource delete OLD_NODE RESOURCE_NAME
```

## How do I evict DRBD resources from a node?

1. Check existence of `evict.sh` script on any master node:

  ```shell
   ls -l /opt/deckhouse/sbin/evict.sh
  ```

2. Fix all faulty LINSTOR resources in the cluster. Run the following command to filter them:

  ```shell
  kubectl -n d8-sds-replicated-volume exec -ti deploy/linstor-controller -- linstor resource list --faulty
  ```

3. Check that all the pods in the `d8-sds-replicated-volume` namespace are in the Running state:

  ```shell
  kubectl -n d8-sds-replicated-volume get pods | grep -v Running
  ```

## How to remove DRBD resources from a node, including removal from LINSTOR and Kubernetes?

Run the `evict.sh` script in interactive mode by specifying the delete mode `--delete-node`:

```shell
/opt/deckhouse/sbin/evict.sh --delete-node
```

To run the `evict.sh` script in non-interactive mode, you need to add the `--non-interactive` flag when invoking it, as well as the name of the node from which you want to evict the resources. In this mode, the script will execute all actions without asking for user confirmation. Example invocation:

```shell
/opt/deckhouse/sbin/evict.sh --non-interactive --delete-node --node-name "worker-1"
```

## How do I evict DRBD resources from a node without deleting it from LINSTOR and Kubernetes

1. Run the `evict.sh` script in interactive mode (`--delete-resources-only`):

```shell
/opt/deckhouse/sbin/evict.sh --delete-resources-only
```

To run the `evict.sh` script in non-interactive mode, add the `--non-interactive` flag followed by the name of the node to evict the resources from. In this mode, the script will perform all the necessary actions automatically (no user confirmation is required). Example:

```shell
/opt/deckhouse/sbin/evict.sh --non-interactive --delete-resources-only --node-name "worker-1"
```

> **Caution!** After the script finishes its job, the node will still be in the Kubernetes cluster albeit in *SchedulingDisabled* status. In LINSTOR, the *AutoplaceTarget=false* property will be set for this node, preventing the LINSTOR scheduler from creating resources on this node.

2. Run the following command to allow DRBD resources and pods to be scheduled on the node again:

```shell
alias linstor='kubectl -n d8-sds-replicated-volume exec -ti deploy/linstor-controller -- linstor'
linstor node set-property "worker-1" AutoplaceTarget
kubectl uncordon "worker-1"
```

3. Run the following command to check the *AutoplaceTarget* property for all nodes (the AutoplaceTarget field will be empty for nodes that are allowed to host LINSTOR resources):

```shell
alias linstor='kubectl -n d8-sds-replicated-volume exec -ti deploy/linstor-controller -- linstor'
linstor node list -s AutoplaceTarget
```

## Troubleshooting

Problems can occur at different levels of component operation.
This cheat sheet will help you quickly navigate through the diagnosis of various problems with the LINSTOR-created volumes:

![LINSTOR cheatsheet](./images/linstor-debug-cheatsheet.svg)
<!--- Source: https://docs.google.com/drawings/d/19hn3nRj6jx4N_haJE0OydbGKgd-m8AUSr0IqfHfT6YA/edit --->

Some common problems are described below.

### linstor-node fail to start because the drbd module cannot be loaded

1. Check the status of the `linstor-node` pods:

```shell
kubectl get pod -n d8-sds-replicated-volume -l app=linstor-node
```

2. If some of those pods got stuck in `Init` state, check the DRBD version as well as the bashible logs on the node:

```shell
cat /proc/drbd
journalctl -fu bashible
```

The most likely reasons why bashible is unable to load the kernel module:

- You have the in-tree version of the DRBDv8 module preloaded, whereas LINSTOR requires DRBDv9.
  Verify the preloaded module version using the following command: `cat /proc/drbd`. If the file is missing, then the module is not preloaded and this is not your case.

- You have Secure Boot enabled.
  Since the DRBD module is compiled dynamically for your kernel (similar to dkms), it is not digitally signed.
  Currently, running the DRBD module with a Secure Boot enabled is not supported.

### The Pod cannot start due to the `FailedMount` error

#### **The Pod is stuck at the `ContainerCreating` phase**

If the Pod is stuck at the `ContainerCreating` phase, and if the errors like those shown below are displayed when the `kubectl describe pod` command is invoked, then it means that the device is mounted on one of the nodes.

```text
rpc error: code = Internal desc = NodePublishVolume failed for pvc-b3e51b8a-9733-4d9a-bf34-84e0fee3168d: checking
for exclusive open failed: wrong medium type, check device health
```

Use the command below to see if this is the case:

```shell
alias linstor='kubectl -n d8-sds-replicated-volume exec -ti deploy/linstor-controller -- linstor'
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

## I have deleted the ReplicatedStoragePool resource, yet its associated Storage Pool in the LINSTOR backend is still there. Is it supposed to be like this?

Yes, this is the expected behavior. Currently, the `sds-replicated-volume` module does not process operations when deleting the `ReplicatedStoragePool` resource.

## I am unable to update the fields in the ReplicatedStorageClass resource spec. Is this the expected behavior?  

Yes, this is the expected behavior. Only the `isDefault` field is editable in the `spec`. All the other fields in the resource `spec` are made immutable.

## When you delete a ReplicatedStorageClass resource, its child StorageClass in Kubernetes is not deleted. What can I do in this case?

The child StorageClass is only deleted if the status of the ReplicatedStorageClass resource is `Created`. Otherwise, you will need to either restore the ReplicatedStorageClass resource to a working state or delete the StorageClass yourself.

## I noticed that an error occurred when trying to create a Storage Pool / Storage Class, but in the end the necessary entity was successfully created. Is this behavior acceptable?

This is the expected behavior. The module will automatically retry the unsuccessful operation if the error was caused by circumstances beyond the module's control (for example, a momentary disruption in the Kubernetes API).

## When running commands in the LINSTOR CLI, I get the "You're not allowed to change state of linstor cluster manually. Please contact tech support" error. What to do?

In the `sds-replicated-volume` module, we have restricted the list of commands that are allowed to be run in LINSTOR, because we plan to automate all manual operations. Some of them are already automated, e.g., creating a Tie-Breaker in cases when LINSTOR doesn't create them for resources with 2 replicas. Use the command below to see the list of allowed commands:

```shell
alias linstor='kubectl -n d8-sds-replicated-volume exec -ti deploy/linstor-controller -- linstor'
linstor --help
```

## How do I restore LINSTOR DB from backup?

The backups of LINSTOR resources are stored in secrets as CRD YAML files and have a segmented format. Backup occurs automatically on a schedule.

An example of a correctly formatted backup looks like this:

```shell
linstor-20240425074718-backup-0              Opaque                           1      28s     sds-replicated-volume.deckhouse.io/linstor-db-backup=20240425074718
linstor-20240425074718-backup-1              Opaque                           1      28s     sds-replicated-volume.deckhouse.io/linstor-db-backup=20240425074718
linstor-20240425074718-backup-2              Opaque                           1      28s     sds-replicated-volume.deckhouse.io/linstor-db-backup=20240425074718
linstor-20240425074718-backup-completed      Opaque                           0      28s     <none>
```

The backup is stored in encoded segments in secrets of the form linstor-%date_time%-backup-{0..2}, where the secret of the form linstor-%date_time%-backup-completed contains no data and serves as a marker for a successfully completed backup process.

### Restoration Process

Set the environment variables:

```shell
NAMESPACE="d8-sds-replicated-volume"
BACKUP_NAME="linstor_db_backup"
```

Check for the presence of backup copies:

```shell
kubectl -n $NAMESPACE get secrets --show-labels
```

Example command output:

```shell
linstor-20240425072413-backup-0              Opaque                           1      33m     sds-replicated-volume.deckhouse.io/linstor-db-backup=20240425072413
linstor-20240425072413-backup-1              Opaque                           1      33m     sds-replicated-volume.deckhouse.io/linstor-db-backup=20240425072413
linstor-20240425072413-backup-2              Opaque                           1      33m     sds-replicated-volume.deckhouse.io/linstor-db-backup=20240425072413
linstor-20240425072413-backup-completed      Opaque                           0      33m     <none>
linstor-20240425072510-backup-0              Opaque                           1      32m     sds-replicated-volume.deckhouse.io/linstor-db-backup=20240425072510
linstor-20240425072510-backup-1              Opaque                           1      32m     sds-replicated-volume.deckhouse.io/linstor-db-backup=20240425072510
linstor-20240425072510-backup-2              Opaque                           1      32m     sds-replicated-volume.deckhouse.io/linstor-db-backup=20240425072510
linstor-20240425072510-backup-completed      Opaque                           0      32m     <none>
linstor-20240425072634-backup-0              Opaque                           1      31m     sds-replicated-volume.deckhouse.io/linstor-db-backup=20240425072634
linstor-20240425072634-backup-1              Opaque                           1      31m     sds-replicated-volume.deckhouse.io/linstor-db-backup=20240425072634
linstor-20240425072634-backup-2              Opaque                           1      31m     sds-replicated-volume.deckhouse.io/linstor-db-backup=20240425072634
linstor-20240425072634-backup-completed      Opaque                           0      31m     <none>
linstor-20240425072918-backup-0              Opaque                           1      28m     sds-replicated-volume.deckhouse.io/linstor-db-backup=20240425072918
linstor-20240425072918-backup-1              Opaque                           1      28m     sds-replicated-volume.deckhouse.io/linstor-db-backup=20240425072918
linstor-20240425072918-backup-2              Opaque                           1      28m     sds-replicated-volume.deckhouse.io/linstor-db-backup=20240425072918
linstor-20240425072918-backup-completed      Opaque                           0      28m     <none>
linstor-20240425074718-backup-0              Opaque                           1      10m     sds-replicated-volume.deckhouse.io/linstor-db-backup=20240425074718
linstor-20240425074718-backup-1              Opaque                           1      10m     sds-replicated-volume.deckhouse.io/linstor-db-backup=20240425074718
linstor-20240425074718-backup-2              Opaque                           1      10m     sds-replicated-volume.deckhouse.io/linstor-db-backup=20240425074718
linstor-20240425074718-backup-completed      Opaque                           0      10m     <none>

```

Each backup has its own label with the creation time. Choose the desired one and copy its label into an environment variable.
For example, let's take the label of the most recent copy from the output above:

```shell
LABEL_SELECTOR="sds-replicated-volume.deckhouse.io/linstor-db-backup=20240425074718"
```

Create a temporary directory to store archive parts:
```shell
TMPDIR=$(mktemp -d)
echo "Временный каталог: $TMPDIR"
```

Next, create an empty archive and combine the secret data into one file:
```shell
COMBINED="${BACKUP_NAME}_combined.tar"
> "$COMBINED"
```

Then, retrieve the list of secrets by label, decrypt the data, and place the backup data into the archive:
```shell
MOBJECTS=$(kubectl get rsmb -l "$LABEL_SELECTOR" --sort-by=.metadata.name -o jsonpath="{.items[*].metadata.name}")

for MOBJECT in $MOBJECTS; do
  echo "Process: $MOBJECT"
  kubectl get rsmb "$MOBJECT" -o jsonpath="{.data}" | base64 --decode >> "$COMBINED"
done
```

Unpack the combined tar file to obtain the backup resources:
```shell
mkdir -p "./backup"
tar -xf "$COMBINED" -C "./backup --strip-components=2
```
Check the contents of the backup:
```shell
ls ./backup
```
```shell
ebsremotes.yaml                    layerdrbdvolumedefinitions.yaml        layerwritecachevolumes.yaml  propscontainers.yaml      satellitescapacity.yaml  secidrolemap.yaml         trackingdate.yaml
files.yaml                         layerdrbdvolumes.yaml                  linstorremotes.yaml          resourceconnections.yaml  schedules.yaml           secobjectprotection.yaml  volumeconnections.yaml
keyvaluestore.yaml                 layerluksvolumes.yaml                  linstorversion.yaml          resourcedefinitions.yaml  secaccesstypes.yaml      secroles.yaml             volumedefinitions.yaml
layerbcachevolumes.yaml            layeropenflexresourcedefinitions.yaml  nodeconnections.yaml         resourcegroups.yaml       secaclmap.yaml           sectyperules.yaml         volumegroups.yaml
layercachevolumes.yaml             layeropenflexvolumes.yaml              nodenetinterfaces.yaml       resources.yaml            secconfiguration.yaml    sectypes.yaml             volumes.yaml
layerdrbdresourcedefinitions.yaml  layerresourceids.yaml                  nodes.yaml                   rollback.yaml             secdfltroles.yaml        spacehistory.yaml
layerdrbdresources.yaml            layerstoragevolumes.yaml               nodestorpool.yaml            s3remotes.yaml            secidentities.yaml       storpooldefinitions.yaml
```
If everything is fine, restore the desired entity by applying the YAML file:
```shell
kubectl apply -f %something%.yaml
```
Or apply bulk-apply if full restoration is needed:
```shell
kubectl apply -f ./backup/
```

## Service pods of sds-replicated-volume components fail to be created on the node I need

Most likely this is due to node labels.

- Check [dataNodes.nodeSelector](./configuration.html#parameters-datanodes-nodeselector) in the module settings:

```shell
kubectl get mc sds-replicated-volume -o=jsonpath={.spec.settings.dataNodes.nodeSelector}
```

- Check the selectors that `sds-replicated-volume-controller` uses:

```shell
kubectl -n d8-sds-replicated-volume get secret d8-sds-replicated-volume-controller-config  -o jsonpath='{.data.config}' | base64 --decode

```

- The `d8-sds-replicated-volume-controller-config` secret should contain the selectors that are specified in the module settings, as well as the `kubernetes.io/os: linux` selector.

- Make sure that the target node has all the labels specified in the `d8-sds-replicated-volume-controller-config` secret:

```shell
kubectl get node worker-0 --show-labels
```

- If there are no labels, add them to the `NodeGroup` or to the node via templates.

- If there are labels, check if the target node has the `storage.deckhouse.io/sds-replicated-volume-node=` label attached. If there is no label, check if the sds-replicated-volume-controller is running and if it is running, examine its logs:

```shell
kubectl -n d8-sds-replicated-volume get po -l app=sds-replicated-volume-controller
kubectl -n d8-sds-replicated-volume logs -l app=sds-replicated-volume-controller
```

## I have not found an answer to my question and am having trouble getting the module to work. What do I do?

Information about the reasons for the failure is saved to the `Status.Reason` field of the `ReplicatedStoragePool` and `ReplicatedStorageClass` resources.
If the information provided is not enough to identify the problem, refer to the sds-replicated-volume-controller logs.

## Migrating from the Deckhouse Kubernetes Platform [linstor](https://deckhouse.io/documentation/v1.57/modules/041-linstor/)  built-in module to sds-replicated-volume

Note that the `LINSTOR` control-plane and its CSI will be unavailable during the migration process. This will make it impossible to create/expand/delete PVs and create/delete pods using the `LINSTOR` PV during the migration.

> **Please note!** User data will not be affected by the migration. Basically, the migration to a new namespace will take place. Also, new components will be added (in the future, they will take over all `LINSTOR` volume management functionality).

### Migration steps

1. Make sure there are no faulty `LINSTOR` resources in the cluster. The command below should return an empty list:

```shell
alias linstor='kubectl -n d8-linstor exec -ti deploy/linstor-controller -- linstor'
linstor resource list --faulty
```

> **Caution!** You should fix all `LINSTOR` resources before migrating.

2. Disable the `linstor` module:

```shell
kubectl patch moduleconfig linstor --type=merge -p '{"spec": {"enabled": false}}'
```

3. Wait for the `d8-linstor` namespace to be deleted.

```shell
kubectl get namespace d8-linstor
```

4. Create a `ModuleConfig` resource for `sds-node-configurator`.

```shell
kubectl apply -f -<<EOF
apiVersion: deckhouse.io/v1alpha1
kind: ModuleConfig
metadata:
  name: sds-node-configurator
spec:
  enabled: true
  version: 1
EOF
```

5. Wait for the `sds-node-configurator` module to become `Ready`.

```shell
kubectl get moduleconfig sds-node-configurator
```

6. Create a `ModuleConfig` resource for `sds-replicated-volume`.

> **Caution!** Failing to specify the `settings.dataNodes.nodeSelector` parameter in the `sds-replicated-volume` module settings would result in the value for this parameter to be derived from the `linstor` module when installing the `sds-replicated-volume` module. If this parameter is not defined there as well, it will remain empty and all the nodes in the cluster will be treated as storage nodes.

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

7. Wait for the `sds-replicated-volume` module to become `Ready`.

```shell
kubectl get moduleconfig sds-replicated-volume
```

8. Check the `sds-replicated-volume` module settings.

```shell
kubectl get moduleconfig sds-replicated-volume -oyaml
```

9. Wait for all pods in the `d8-sds-replicated-volume` and `d8-sds-node-configurator` namespaces to become `Ready` or `Completed`.

```shell
kubectl get po -n d8-sds-node-configurator
kubectl get po -n d8-sds-replicated-volume
```

10. Override the `linstor` command alias and check the `LINSTOR` resources:

```shell
alias linstor='kubectl -n d8-sds-replicated-volume exec -ti deploy/linstor-controller -- linstor'
linstor resource list --faulty
```

If there are no faulty resources, then the migration was successful.

### Migrating to ReplicatedStorageClass

Note that StorageClasses in this module are managed via the `ReplicatedStorageClass` resource. StorageClasses should not be created manually.

When migrating from the linstor module, delete old StorageClasses and create new ones via the `ReplicatedStorageClass` resource (refer to the table below).

Note that in the old StorageClasses, you should pick up the option from the parameter section of the StorageClass itself, while for the new StorageClass, you should specify the corresponding option in `ReplicatedStorageClass`. 

| StorageClass parameter                     | ReplicatedStorageClass      | Default parameter | Notes                                                     |
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
- If you need to use `volumeBindingMode: Immediate`, set the volumeAccess parameter of the `ReplicatedStorageClass` to `Any`.

You can read more about working with `ReplicatedStorageClass` resources [here](./usage.html).

### Migrating to ReplicatedStoragePool

The `ReplicatedStoragePool` resource allows you to create a `Storage Pool` in `LINSTOR`. It is recommended to create this resource for the `Storage Pools` that already exist in LINSTOR and specify the existing `LVMVolumeGroups` in this resource. In this case, the controller will see that the corresponding `Storage Pool` has been created and leave it unchanged, while the `status.phase` field of the created resource will be set to `Created`. Refer to the [sds-node-configurator](../../sds-node-configurator/stable/usage.html) documentation to learn more about `LVMVolumeGroup` resources. To learn more about working with `ReplicatedStoragePool` resources, click [here](./usage.html).

## Migrating from sds-drbd module to sds-replicated-volume

Note that the module control-plane and its CSI will be unavailable during the migration process. This will make it impossible to create/expand/delete PVs and create/delete pods using `DRBD` PV during the migration.

> **Please note!** User data will not be affected by the migration. Basically, the migration to a new namespace will take place. Also, new components will be added (in the future, they will take over all module volume management functionality).

### Migration steps

1. Make sure there are no faulty `DRBD` resources in the cluster. The command below should return an empty list:

```shell
alias linstor='kubectl -n d8-sds-drbd exec -ti deploy/linstor-controller -- linstor'
linstor resource list --faulty
```

> **Caution!** You should fix all `DRBD` resources before migrating.

2. Disable the `sds-drbd` module:

```shell
kubectl patch moduleconfig sds-drbd --type=merge -p '{"spec": {"enabled": false}}'
```

3. Wait for the `d8-sds-drbd` namespace to be deleted.

```shell
kubectl get namespace d8-sds-drbd
```

4. Create a `ModuleConfig` resource for `sds-replicated-volume`.

> **Caution!** Failing to specify the `settings.dataNodes.nodeSelector` parameter in the `sds-replicated-volume` module settings would result in the value for this parameter to be derived from the `sds-drbd` module when installing the `sds-replicated-volume` module. If this parameter is not defined there as well, it will remain empty and all the nodes in the cluster will be treated as storage nodes.

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

5. Wait for the `sds-replicated-volume` module to become `Ready`.

```shell
kubectl get moduleconfig sds-replicated-volume
```

6. Check the `sds-replicated-volume` module settings.

```shell
kubectl get moduleconfig sds-replicated-volume -oyaml
```

7. Wait for all pods in the `d8-sds-replicated-volume` namespaces to become `Ready` or `Completed`.

```shell
kubectl get po -n d8-sds-replicated-volume
```

8. Override the `linstor` command alias and check the `DRBD` resources:

```shell
alias linstor='kubectl -n d8-sds-replicated-volume exec -ti deploy/linstor-controller -- linstor'
linstor resource list --faulty
```

If there are no faulty resources, then the migration was successful.

> **Caution!** The resources DRBDStoragePool and DRBDStorageClass will be automatically migrated to ReplicatedStoragePool and ReplicatedStorageClass during the process, no user intervention is required for this. The functionality of these resources will not change. However, it is worth checking if there are any DRBDStoragePool or DRBDStorageClass left in cluster. If they exist after the migration, please inform our support team.

## Why is it not recommended to use RAID for disks that are used by the `sds-replicated-volume` module?

DRBD with a replica count greater than 1 provides de facto network RAID. Using RAID locally may be inefficient because:

- Redundant RAID dramatically increases the overhead in terms of space utilization. Here is an example: Suppose, a `DBRDStorageClass` is used with `replication` set to `ConsistencyAndAvailability`. With this setting, DRBD will store data in three replicas (one replica per three different hosts). If RAID1 is used on these hosts, a total of 6 GB of disk space will be required to store 1 GB of data. Redundant RAID is worth using for easier server maintenance when the storage costs are irrelevant. RAID1 in this case will allow you to change disks on servers without having to move data replicas from the "problem" disk.

- As for RAID0, the performance gain will be unnoticeable, since data replication will be performed over the network and the network is likely to be the bottleneck. On top of that, decreased storage reliability on the host will potentially lead to data unavailability given that in DRBD, switching from a faulty replica to a healthy one is not instantaneous.

## Why do you recommend using local disks (and not NAS)?

DRBD uses the network for data replication. When using NAS, network load will increase significantly because nodes will synchronize data not only with NAS but also between each other. Similarly, read/write latency will also increase. NAS typically involves using RAID on its side, which also adds overhead.
