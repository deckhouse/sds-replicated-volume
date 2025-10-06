---
title: "The sds-replicated-volume module: FAQ"
description: LINSTOR Troubleshooting. What is difference between LVM and LVMThin? Performance and reliability notes, comparison to Ceph. How to add existing LVM or LVMThin pool. How to configure Prometheus to storing data. Controller's work-flow questions.
---

{{< alert level="warning" >}}
The module is only guaranteed to work if the [system requirements](./readme.html#system-requirements-and-recommendations) are met.
As for any other configurations, the module may work, but its smooth operation is not guaranteed.
{{< /alert >}}

## What is difference between LVM and LVMThin?

- LVM is simpler and has high performance that is similar to that of native disk drives, but it does not support snapshots;
- LVMThin allows for snapshots and overprovisioning; however, it is slower than LVM.

{{< alert level="warning" >}}
Overprovisioning in LVMThin should be used with caution, monitoring the availability of free space in the pool (The cluster monitoring system generates separate events when the free space in the pool reaches 20%, 10%, 5%, and 1%).

In case of no free space in the pool, degradation in the module's operation as a whole will be observed, and there is a real possibility of data loss!
{{< /alert >}}

## Which Replication Modes to Use and When?

There are three replication modes in total:

- **None** – A single data replica. Equivalent to a regular PV resource, with no fault tolerance or availability.  
- **Availability** – Two data replicas + one diskless replica (contains no data; when used, it accesses any available "full" replica over the network). Provides a certain level of availability if one replica fails but does not guarantee data consistency (integrity).  
- **ConsistencyAndAvailability** – Three data replicas, full fault tolerance with guaranteed data preservation in case of failures.  

By default, the `ConsistencyAndAvailability` mode is used when creating a `ReplicatedStorageClass`. It can be specified as follows:  

```shell
apiVersion: storage.deckhouse.io/v1alpha1
kind: ReplicatedStorageClass
metadata:
  generation: 2
  name: test-rsc
spec:
  reclaimPolicy: Delete
  replication: ConsistencyAndAvailability
  storagePool: sample
  topology: Ignored
  volumeAccess: Local
```

### When and Which Modes to Use?  

- **None** – Suitable for test environments or clustered applications (e.g., if you deploy a multi-node cluster of RabbitMQ/MongoDB/MySQL/etc.).  
- **Availability** – A compromise mode that ensures availability but, in case of network connectivity issues (one of the quorum replicas is diskless, and accessing data through it happens over the network), may lead to desynchronization and, optionally, data loss.  
  Best suited for non-critical data and applications that require some level of high availability (e.g., when nodes periodically go into maintenance) but do not have strict reliability or data integrity requirements.  
- **ConsistencyAndAvailability** – The most reliable replication mode, recommended for mission-critical applications, vital data, and deploying virtual machines in a DVP environment.  


## How do I get info about the space used?

There are two options:

1. Through the Grafana dashboard:

   Navigate to "Dashboards" → "Storage" → "LINSTOR/DRBD" in the Grafana interface. The current space usage in the cluster is displayed in the top-right corner of the dashboard.

   > **Note.** This information reflects the total available space in the cluster. If volumes need to be created in two replicas, divide these values by two to understand how many such volumes can be accommodated in the cluster.

2. Using the command line:

   ```shell
   kubectl exec -n d8-sds-replicated-volume deploy/linstor-controller -- linstor storage-pool list
   ```

   >****Note.** This information reflects the total available space in the cluster. When creating volumes with two replicas, these two replicas must fit entirely across two nodes of your cluster.

## How do I set the default StorageClass?

Add corresponding StorageClass name to `spec.settings.defaultClusterStorageClass` of `ModuleConfig/global` config.

```shell
   apiVersion: deckhouse.io/v1alpha1
   kind: ModuleConfig
   metadata:
      name: global
   spec:
      version: 2
      settings:
         defaultClusterStorageClass: 'default-fast'
```

## How do I add the existing LVM Volume Group or LVMThin pool?

1. Manually add the `storage.deckhouse.io/enabled=true` LVM tag to the Volume Group:

   ```shell
   vgchange myvg-0 --add-tag storage.deckhouse.io/enabled=true
   ```

   This VG will be automatically discovered and a corresponding LVMVolumeGroup resource will be created in the cluster for it.

2. Specify this resource in the [ReplicatedStoragePool](./cr.html#replicatedstoragepool) parameters in the `spec.lvmVolumeGroups[].name` field (note that for the LVMThin pool, you must additionally specify its name in `spec.lvmVolumeGroups[].thinPoolName`).

## How to expand ReplicatedStoragePool to new cluster node?

To expand an existing ReplicatedStoragePool use new LVM Volume Group, follow these steps:

1. Create new LVMVolumeGroup with [sds-node-configurator](/modules/sds-node-configurator/usage.html#creating-an-lvmvolumegroup-resource)

1. Add the new Volume Group to the existing ReplicatedStoragePool by editing the resource:

   ```shell
   kubectl edit replicatedstoragepool your-pool-name
   ```

   Add the new Volume Group to the `spec.lvmVolumeGroups` section:

   ```yaml
   spec:
     lvmVolumeGroups:
     - name: existing-vg-name
     - name: new-vg-name  # Add this line
   ```

1. For LVMThin pools, additionally specify the thin pool name:

   ```yaml
   spec:
     lvmVolumeGroups:
     - name: existing-vg-name
       thinPoolName: existing-thin-pool
     - name: new-vg-name
       thinPoolName: new-thin-pool  # Add this line
   ```

1. Save the changes. The controller will automatically create a Storage Pool in LINSTOR for the new Volume Group and add it to the existing ReplicatedStoragePool.

1. Check the expansion status:

   ```shell
   kubectl get replicatedstoragepool your-pool-name -o yaml
   ```

   Information about the new Volume Group should be displayed in the status.

## How to increase the limit on the number of DRBD devices / change the ports through which DRBD clusters communicate with each other?

To increase the limit on the number of DRBD devices / change the ports through which DRBD clusters communicate with each other, you can use the drbdPortRange setting. By default, DRBD resources use TCP ports 7000-7999. These values can be redefined using minPort and maxPort.

{{< alert level="warning" >}}
Changing the drbdPortRange minPort/maxPort will not affect existing DRBD resources; they will continue to operate on their original ports.

After changing the drbdPortRange values, the linstor-controller needs to be restarted.
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

   ```console
   $ kubectl -n d8-sds-replicated-volume exec -t deploy/linstor-controller -- linstor r l --faulty
   Defaulted container "linstor-controller" out of: linstor-controller, kube-rbac-proxy
   +----------------------------------------------------------------+
   | ResourceName | Node | Port | Usage | Conns | State | CreatedOn |
   |================================================================|
   +----------------------------------------------------------------+
   ```

3. Reboot the node and wait for the synchronization of all DRBD resources. Then uncordon the node. If another node needs to be rebooted, repeat the algorithm.

   ```shell
   kubectl uncordon test-node-1
   node/test-node-1 uncordoned
   ```

## How do I free some space on storage pool by moving resources to another

1. Check the storage pool: `kubectl exec -n d8-sds-replicated-volume deploy/linstor-controller -- linstor storage-pool list -n OLD_NODE`

2. Check the volumes: `kubectl exec -n d8-sds-replicated-volume deploy/linstor-controller -- linstor volume list -n OLD_NODE`

3. Identify the resources you want to move:

   ```shell
   kubectl exec -n d8-sds-replicated-volume deploy/linstor-controller -- linstor resource list-volumes
   ```

4. Move resources to another node (no more than 1-2 resources at a time):

   ```shell
   kubectl exec -n d8-sds-replicated-volume deploy/linstor-controller -- linstor --yes-i-am-sane-and-i-understand-what-i-am-doing resource create NEW_NODE RESOURCE_NAME
   ```

   ```shell
   kubectl exec -n d8-sds-replicated-volume deploy/linstor-controller -- linstor resource-definition wait-sync RESOURCE_NAME
   ```

   ```shell
   kubectl exec -n d8-sds-replicated-volume deploy/linstor-controller -- linstor --yes-i-am-sane-and-i-understand-what-i-am-doing resource delete OLD_NODE RESOURCE_NAME
   ```

### Can I automate the management of replicas and monitoring of LINSTOR state?
Replica management and state monitoring are automated in the `replicas_manager.sh` script. 
It checks the availability of the LINSTOR controller, identifies faulty or corrupted resources, creates database backups, and manages disk replicas, including configuring TieBreaker for quorum.

To check the existence of the `replicas_manager.sh` script, run the following command on any master node:

   ```shell
   ls -l /opt/deckhouse/sbin/replicas_manager.sh
   ```

Upon execution, the script performs the following actions:
- Verifies the availability of the controller and connectivity to satellites
- Identifies faulty or corrupted resources
- Creates a backup of the database
- Manages the number of disk replicas, adding new ones as needed
- Configures TieBreaker for resources with two replicas
- Logs all actions to a file named linstor_replicas_manager_<date_time>.log
- Provides recommendations for resolving issues, such as stuck replicas

Configuration variables for `replicas_manager.sh`:
- NON_INTERACTIVE — enables non-interactive mode
- TIMEOUT_SEC — timeout between attempts, in seconds (default: 10)
- EXCLUDED_RESOURCES_FROM_CHECK — regular expression to exclude resources from checks
- CHUNK_SIZE — chunk size for processing resources (default: 10)
- NODE_FOR_EVICT — name of the node excluded from creating replicas
- LINSTOR_NAMESPACE — Kubernetes namespace (default: d8-sds-replicated-volume)
- DISKLESS_STORAGE_POOL — pool for diskless replicas (default: DfltDisklessStorPool)

### How to evict DRBD resources from a node?

Eviction of DRBD resources from a node is performed using the `evict.sh` script. It can operate in two modes:

- **Node Deletion Mode** – in this mode, the following actions are executed:
  - Additional replicas are created for each resource hosted on the specified node;
  - The node is removed from `LINSTOR`;
  - The node is removed from `Kubernetes`;

- **Resource Deletion Mode** – in this mode, the following actions are executed:
  - Additional replicas are created for each resource hosted on the specified node;
  - The resources hosted on the specified node are removed from `LINSTOR`;

Before proceeding with the eviction, the following steps must be performed:

1. Check existence of `evict.sh` script on any master node:

   ```shell
   ls -l /opt/deckhouse/sbin/evict.sh
   ```

2. Fix all faulty resources in the cluster. Run the following command to filter them:

   ```shell
   kubectl -n d8-sds-replicated-volume exec -ti deploy/linstor-controller -- linstor resource list --faulty
   ```

1. Check that all the pods in the `d8-sds-replicated-volume` namespace are in the Running state:

   ```shell
   kubectl -n d8-sds-replicated-volume get pods | grep -v Running
   ```

### Example of removing a node from LINSTOR and Kubernetes.

Run the `evict.sh` script on any master node in interactive mode by specifying the delete mode `--delete-node`:

```shell
/opt/deckhouse/sbin/evict.sh --delete-node
```

To run the `evict.sh` script in non-interactive mode, you need to add the `--non-interactive` flag when invoking it, as well as the name of the node from which you want to evict the resources. In this mode, the script will execute all actions without asking for user confirmation.

Example invocation:

```shell
/opt/deckhouse/sbin/evict.sh --non-interactive --delete-node --node-name "worker-1"
```

### Example of removing resources from a node without removing the node itself.

1. Run the `evict.sh` script on any master node in interactive mode (`--delete-resources-only`):

   ```shell
   /opt/deckhouse/sbin/evict.sh --delete-resources-only
   ```

To run the `evict.sh` script in non-interactive mode, add the `--non-interactive` flag followed by the name of the node to evict the resources from. In this mode, the script will perform all the necessary actions automatically (no user confirmation is required).

Example:

```shell
/opt/deckhouse/sbin/evict.sh --non-interactive --delete-resources-only --node-name "worker-1"
```

> **Caution!** After the script finishes its job, the node will still be in the Kubernetes cluster albeit in *SchedulingDisabled* status. In LINSTOR, the *AutoplaceTarget=false* property will be set for this node, preventing the its scheduler from creating resources on this node.

2. Run the following command to allow DRBD resources and pods to be scheduled on the node again:

   ```shell
   alias linstor='kubectl -n d8-sds-replicated-volume exec -ti deploy/linstor-controller -- linstor'
   linstor node set-property "worker-1" AutoplaceTarget
   kubectl uncordon "worker-1"
   ```

3. Run the following command to check the `AutoplaceTarget` property for all nodes (the AutoplaceTarget field will be empty for nodes that are allowed to host LINSTOR resources):

   ```shell
   alias linstor='kubectl -n d8-sds-replicated-volume exec -ti deploy/linstor-controller -- linstor'
   linstor node list -s AutoplaceTarget
   ```

### Description of the `evict.sh` script parameters

- `--delete-node` — Removes the node from LINSTOR and Kubernetes after first creating additional replicas for all resources hosted on that node.
- `--delete-resources-only` — Removes the resources from the node without deleting the node from LINSTOR and Kubernetes, after first creating additional replicas for all resources hosted on that node.
- `--non-interactive` — Runs the script in non-interactive mode.
- `--node-name` — Specifies the name of the node from which resources should be evicted. This parameter is mandatory when using non-interactive mode.
- `--skip-db-backup` — Skips creating a backup of the LINSTOR database before executing the operations.
- `--ignore-advise` — Proceeds with the operations despite warnings from the `linstor advise resource` command. Use if the script was interrupted and the number of replicas for some resources does not match the value specified in the `ReplicatedStorageClass`.
- `--exclude-resources-from-check` — Excludes from checks the resources listed using the `|` (vertical bar) as a separator.


## Troubleshooting

Problems can occur at different levels of component operation.
This cheat sheet will help you quickly navigate through the diagnosis of various problems with the LINSTOR-created volumes:

![cheatsheet](./images/linstor-debug-cheatsheet.svg)
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

- You have the in-tree version of the DRBDv8 module preloaded, whereas DRBDv9 is required.
  Verify the preloaded module version using the following command: `cat /proc/drbd`. If the file is missing, then the module is not preloaded and this is not your case.

- You have Secure Boot enabled.
  Since the DRBD module is compiled dynamically for your kernel (similar to dkms), it is not digitally signed.
  Currently, running the DRBD module with a Secure Boot enabled is not supported.

### The Pod cannot start due to the FailedMount error

#### The Pod is stuck at the ContainerCreating phase

If the Pod is stuck at the `ContainerCreating` phase, and if the errors like those shown below are displayed when the `kubectl describe pod` command is invoked, then it means that the device is mounted on one of the nodes.

```console
rpc error: code = Internal desc = NodePublishVolume failed for pvc-b3e51b8a-9733-4d9a-bf34-84e0fee3168d: checking
for exclusive open failed: wrong medium type, check device health
```

Use the command below to see if this is the case:

```shell
alias linstor='kubectl -n d8-sds-replicated-volume exec -ti deploy/linstor-controller -- linstor'
linstor resource list -r pvc-b3e51b8a-9733-4d9a-bf34-84e0fee3168d
```

The `InUse` flag will point to the node on which the device is being used. You will need to manually unmount the disk on that node.

#### Errors of the Input/output error type

These errors usually occur during the file system (mkfs) creation.

Check `dmesg` on the node where the pod is being run:

```shell
dmesg | grep 'Remote failed to finish a request within'
```

If the command output is not empty (the `dmesg` output contains lines like *"Remote failed to finish a request within ... "*), most likely your disk subsystem is too slow for DRBD to run properly.

## I have deleted the ReplicatedStoragePool resource, yet its associated Storage Pool in the backend is still there. Is it supposed to be like this?

Yes, this is the expected behavior. Currently, the `sds-replicated-volume` module does not process operations when deleting the ReplicatedStoragePool resource.

## I am unable to update the fields in the ReplicatedStorageClass resource spec. Is this the expected behavior?  

Yes, this is the expected behavior. All `spec` fields of the resource made immutable.

## When you delete a ReplicatedStorageClass resource, its child StorageClass in Kubernetes is not deleted. What can I do in this case?

The child StorageClass is only deleted if the status of the ReplicatedStorageClass resource is `Created`. Otherwise, you will need to either restore the ReplicatedStorageClass resource to a working state or delete the StorageClass yourself.

## I noticed that an error occurred when trying to create a Storage Pool / Storage Class, but in the end the necessary entity was successfully created. Is this behavior acceptable?

This is the expected behavior. The module will automatically retry the unsuccessful operation if the error was caused by circumstances beyond the module's control (for example, a momentary disruption in the Kubernetes API).

## When running commands in the CLI, I get the "You're not allowed to change state of linstor cluster manually. Please contact tech support" error. What to do?

In the `sds-replicated-volume` module, we have restricted the list of commands that are allowed to be run in LINSTOR, because we plan to automate all manual operations. Some of them are already automated, e.g., creating a Tie-Breaker in cases when it doesn't create them for resources with 2 replicas. Use the command below to see the list of allowed commands:

```shell
alias linstor='kubectl -n d8-sds-replicated-volume exec -ti deploy/linstor-controller -- linstor'
linstor --help
```

## How do I restore DB from backup?

The backups of LINSTOR resources are stored in Custom Resources `replicatedstoragemetadatabackups.storage.deckhouse.io` and have a segmented format. Backup occurs automatically on a schedule.

An example of a correctly formatted backup looks like this:

```shell
sds-replicated-volume-daily-backup-20241112130501-backup-0    97m     completed=true,sds-replicated-volume.deckhouse.io/sds-replicated-volume-db-backup=20241112130501
sds-replicated-volume-daily-backup-20241112130501-backup-1    97m     completed=true,sds-replicated-volume.deckhouse.io/sds-replicated-volume-db-backup=20241112130501
sds-replicated-volume-daily-backup-20241112140501-backup-0    37m     completed=true,sds-replicated-volume.deckhouse.io/sds-replicated-volume-db-backup=20241112140501
sds-replicated-volume-daily-backup-20241112140501-backup-1    37m     completed=true,sds-replicated-volume.deckhouse.io/sds-replicated-volume-db-backup=20241112140501
sds-replicated-volume-weekly-backup-20241112130400-backup-0   98m     completed=true,sds-replicated-volume.deckhouse.io/sds-replicated-volume-db-backup=20241112130400
sds-replicated-volume-weekly-backup-20241112130400-backup-1   98m     completed=true,sds-replicated-volume.deckhouse.io/sds-replicated-volume-db-backup=20241112130400
```

The backup is stored in encoded segments in Custom Resources `replicatedstoragemetadatabackups.storage.deckhouse.io` of the form `sds-replicated-volume-{daily|weekly}-backup-%date_time%-backup-{0..2}`.

### Restoration process

Set the environment variables:

```shell
NAMESPACE="d8-sds-replicated-volume"
BACKUP_NAME="linstor_db_backup"
```

Check for the presence of backup copies:

```shell
kubectl get rsmb --show-labels
```

Example output:

```shell
sds-replicated-volume-daily-backup-20241112130501-backup-0    97m     completed=true,sds-replicated-volume.deckhouse.io/sds-replicated-volume-db-backup=20241112130501
sds-replicated-volume-daily-backup-20241112130501-backup-1    97m     completed=true,sds-replicated-volume.deckhouse.io/sds-replicated-volume-db-backup=20241112130501
sds-replicated-volume-daily-backup-20241112140501-backup-0    37m     completed=true,sds-replicated-volume.deckhouse.io/sds-replicated-volume-db-backup=20241112140501
sds-replicated-volume-daily-backup-20241112140501-backup-1    37m     completed=true,sds-replicated-volume.deckhouse.io/sds-replicated-volume-db-backup=20241112140501
sds-replicated-volume-weekly-backup-20241112130400-backup-0   98m     completed=true,sds-replicated-volume.deckhouse.io/sds-replicated-volume-db-backup=20241112130400
sds-replicated-volume-weekly-backup-20241112130400-backup-1   98m     completed=true,sds-replicated-volume.deckhouse.io/sds-replicated-volume-db-backup=20241112130400
```

Each backup has its own label with the creation time. Choose the desired one and copy its label into an environment variable.
For example, let's take the label of the most recent copy from the output above:

```shell
LABEL_SELECTOR="sds-replicated-volume.deckhouse.io/linstor-db-backup=20240425074718"
```

Create a temporary directory to store archive parts:

```shell
TMPDIR=$(mktemp -d)
echo "Temporary directory: $TMPDIR"
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
  kubectl get rsmb "$MOBJECT" -o jsonpath="{.spec.data}" | base64 --decode >> "$COMBINED"
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

- If there are no labels, add them to the NodeGroup or to the node via templates.

- If there are labels, check if the target node has the `storage.deckhouse.io/sds-replicated-volume-node=` label attached. If there is no label, check if the sds-replicated-volume-controller is running and if it is running, examine its logs:

  ```shell
  kubectl -n d8-sds-replicated-volume get po -l app=sds-replicated-volume-controller
  kubectl -n d8-sds-replicated-volume logs -l app=sds-replicated-volume-controller
  ```

## I have not found an answer to my question and am having trouble getting the module to work. What do I do?

Information about the reasons for the failure is saved to the `Status.Reason` field of the ReplicatedStoragePool and ReplicatedStorageClass resources.
If the information provided is not enough to identify the problem, refer to the sds-replicated-volume-controller logs.

## Migrating from the Deckhouse Kubernetes Platform [linstor](https://deckhouse.io/documentation/v1.57/modules/041-linstor/)  built-in module to sds-replicated-volume

Note that the `LINSTOR` control-plane and its CSI will be unavailable during the migration process. This will make it impossible to create/expand/delete PVs and create/delete pods using its PV during the migration.

> **Please note!** User data will not be affected by the migration. Basically, the migration to a new namespace will take place. Also, new components will be added (in the future, they will take over all volume management functionality).

### Migration steps

1. Make sure there are no faulty resources in the module's backend. The command below should return an empty list:

```shell
alias linstor='kubectl -n d8-linstor exec -ti deploy/linstor-controller -- linstor'
linstor resource list --faulty
```

> **Caution!** You should fix all resources before migrating.

   > **Caution.** You should fix all LINSTOR resources before migrating.

2. Disable the `linstor` module:

   ```shell
   kubectl patch moduleconfig linstor --type=merge -p '{"spec": {"enabled": false}}'
   ```

3. Wait for the `d8-linstor` namespace to be deleted:

   ```shell
   kubectl get namespace d8-linstor
   ```

4. Create a ModuleConfig resource for `sds-node-configurator`:

   ```yaml
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

5. Wait for the `sds-node-configurator` module to become `Ready`:

   ```shell
   kubectl get moduleconfig sds-node-configurator
   ```

6. Create a ModuleConfig resource for `sds-replicated-volume`.

   > **Caution.** Failing to specify the `settings.dataNodes.nodeSelector` parameter in the `sds-replicated-volume` module settings would result in the value for this parameter to be derived from the `linstor` module when installing the `sds-replicated-volume` module. If this parameter is not defined there as well, it will remain empty and all the nodes in the cluster will be treated as storage nodes.

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

7. Wait for the `sds-replicated-volume` module to become `Ready`:

   ```shell
   kubectl get moduleconfig sds-replicated-volume
   ```

8. Check the `sds-replicated-volume` module settings:

   ```shell
   kubectl get moduleconfig sds-replicated-volume -oyaml
   ```

9. Wait for all pods in the `d8-sds-replicated-volume` and `d8-sds-node-configurator` namespaces to become `Ready` or `Completed`:

   ```shell
   kubectl get po -n d8-sds-node-configurator
   kubectl get po -n d8-sds-replicated-volume
   ```

10. Override the `linstor` command alias and check the resources:

   ```shell
   alias linstor='kubectl -n d8-sds-replicated-volume exec -ti deploy/linstor-controller -- linstor'
   linstor resource list --faulty
   ```

If there are no faulty resources, then the migration was successful.

### Migrating to ReplicatedStorageClass

Note that StorageClasses in this module are managed via the ReplicatedStorageClass resource. StorageClasses should not be created manually.

When migrating from the linstor module, delete old StorageClasses and create new ones via the ReplicatedStorageClass resource (refer to the table below).

Note that in the old StorageClasses, you should pick up the option from the parameter section of the StorageClass itself, while for the new StorageClass, you should specify the corresponding option in ReplicatedStorageClass.

| StorageClass parameter                     | ReplicatedStorageClass      | Default parameter | Notes                                                     |
|-------------------------------------------|-----------------------|-|----------------------------------------------------------------|
| linstor.csi.linbit.com/placementCount: "1" | replication: "None"   | | A single volume replica with data will be created                  |
| linstor.csi.linbit.com/placementCount: "2" | replication: "Availability" | | Two volume replicas with data will be created                  |
| linstor.csi.linbit.com/placementCount: "3" | replication: "ConsistencyAndAvailability" | Yes | Three volume replicas with data will be created                   |
| linstor.csi.linbit.com/storagePool: "name" | storagePool: "name"   | | Name of the storage pool to use for storage               |
| linstor.csi.linbit.com/allowRemoteVolumeAccess: "false" | volumeAccess: "Local" | | Pods are not allowed to access data volumes remotely (only local access to the disk within the Node is allowed) |

On top of these, the following parameters are available:

- `reclaimPolicy` (Delete, Retain) — corresponds to the reclaimPolicy parameter of the old StorageClass.
- `zones` — list of zones to be used for hosting resources ( the actual names of the zones in the cloud). Please note that remote access to a volume with data is only possible within a single zone!
- `volumeAccess` can be `Local` (access is strictly within the node), `EventuallyLocal` (the data replica will be synchronized on the node with the running pod a while after the start), `PreferablyLocal` (remote access to the volume with data is allowed, volumeBindingMode: WaitForFirstConsumer), `Any` (remote access to the volume with data is allowed, volumeBindingMode: Immediate).
- If you need to use `volumeBindingMode: Immediate`, set the volumeAccess parameter of the ReplicatedStorageClass to `Any`.

You can read more about working with ReplicatedStorageClass resources [in the documentation](./usage.html).

### Migrating to ReplicatedStoragePool

The `ReplicatedStoragePool` resource allows you to create a `Storage Pool` in the modules's backend. It is recommended to create this resource for the `Storage Pools` that already exist and specify the existing `LVMVolumeGroups` in this resource. In this case, the controller will see that the corresponding `Storage Pool` has been created and leave it unchanged, while the `status.phase` field of the created resource will be set to `Created`. Refer to the [sds-node-configurator](/modules/sds-node-configurator/usage.html) documentation to learn more about `LVMVolumeGroup` resources. To learn more about working with `ReplicatedStoragePool` resources, click [here](./usage.html).

## Migrating from sds-drbd module to sds-replicated-volume

Note that the module control-plane and its CSI will be unavailable during the migration process. This will make it impossible to create/expand/delete PVs and create/delete pods using DRBD PV during the migration.

> **Note.** User data will not be affected by the migration. Basically, the migration to a new namespace will take place. Also, new components will be added (in the future, they will take over all module volume management functionality).

### Procedure for migration

1. Make sure there are no faulty DRBD resources in the cluster. The command below should return an empty list:

   ```shell
   alias linstor='kubectl -n d8-sds-drbd exec -ti deploy/linstor-controller -- linstor'
   linstor resource list --faulty
   ```

   > **Caution.** You should fix all DRBD resources before migrating.

2. Disable the `sds-drbd` module:

   ```shell
   kubectl patch moduleconfig sds-drbd --type=merge -p '{"spec": {"enabled": false}}'
   ```

3. Wait for the `d8-sds-drbd` namespace to be deleted.

   ```shell
   kubectl get namespace d8-sds-drbd
   ```

4. Create a ModuleConfig resource for `sds-replicated-volume`.

   > **Caution.** Failing to specify the `settings.dataNodes.nodeSelector` parameter in the `sds-replicated-volume` module settings would result in the value for this parameter to be derived from the `sds-drbd` module when installing the `sds-replicated-volume` module. If this parameter is not defined there as well, it will remain empty and all the nodes in the cluster will be treated as storage nodes.

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

5. Wait for the `sds-replicated-volume` module to become `Ready`.

   ```shell
   kubectl get moduleconfig sds-replicated-volume
   ``

6. Check the `sds-replicated-volume` module settings.

   ```shell
   kubectl get moduleconfig sds-replicated-volume -oyaml
   ```

7. Wait for all pods in the `d8-sds-replicated-volume` namespaces to become `Ready` or `Completed`.

   ```shell
   kubectl get po -n d8-sds-replicated-volume
   ```

8. Override the `linstor` command alias and check the DRBD resources:

   ```shell
   alias linstor='kubectl -n d8-sds-replicated-volume exec -ti deploy/linstor-controller -- linstor'
   linstor resource list --faulty
   ```

If there are no faulty resources, then the migration was successful.

> **Caution.** The resources DRBDStoragePool and DRBDStorageClass will be automatically migrated to ReplicatedStoragePool and ReplicatedStorageClass during the process, no user intervention is required for this. The functionality of these resources will not change. However, it is worth checking if there are any DRBDStoragePool or DRBDStorageClass left in cluster. If they exist after the migration, please inform our support team.

## Why is it not recommended to use RAID for disks that are used by the sds-replicated-volume module?

DRBD with a replica count greater than 1 provides de facto network RAID. Using RAID locally may be inefficient because:

- Redundant RAID dramatically increases the overhead in terms of space utilization.
  Here is an example: Suppose, a ReplicatedStorageClass is used with `replication` set to `ConsistencyAndAvailability`. With this setting, DRBD will store data in three replicas (one replica per three different hosts). If RAID1 is used on these hosts, a total of 6 GB of disk space will be required to store 1 GB of data. Redundant RAID is worth using for easier server maintenance when the storage costs are irrelevant. RAID1 in this case will allow you to change disks on servers without having to move data replicas from the "problem" disk.

- As for RAID0, the performance gain will be unnoticeable, since data replication will be performed over the network and the network is likely to be the bottleneck. On top of that, decreased storage reliability on the host will potentially lead to data unavailability given that in DRBD, switching from a faulty replica to a healthy one is not instantaneous.

## Why do you recommend using local disks (and not NAS)?

DRBD uses the network for data replication. When using NAS, network load will increase significantly because nodes will synchronize data not only with NAS but also between each other. Similarly, read/write latency will also increase. NAS typically involves using RAID on its side, which also adds overhead.


## How to manually trigger the certificate renewal process?

Although the certificate renewal process is automated, manual renewal might still be necessary because it can be performed during a convenient maintenance window when it is acceptable to restart the module's objects. The automated renewal does not restart any objects.

To manually trigger the certificate renewal process, create a `ConfigMap` named `manualcertrenewal-trigger`:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: manualcertrenewal-trigger
  namespace: d8-sds-replicated-volume
```

The system will stop all necessary module objects, update the certificates, and then restart them.

You can check the operation status using the following command:

```shell
kubectl -n d8-sds-replicated-volume get cm manualcertrenewal-trigger -ojsonpath='{.data.step}'
```
  - `Prepared` — health checks have passed successfully, and the downtime window has started.
  - `TurnedOffAndRenewedCerts` — the system has been stopped and certificates have been renewed.
  - `TurnedOn` — the system has been restarted.
  - `Done` — the operation is complete and ready to be repeated.

Certificates are issued for a period of one year and are marked as expiring 30 days before their expiration date. The monitoring system alerts about expiring certificates (see the `D8LinstorCertificateExpiringIn30d` alert).

To repeat the operation, simply remove the label from the trigger using the following command:

```shell
kubectl -n d8-sds-replicated-volume label cm manualcertrenewal-trigger storage.deckhouse.io/sds-replicated-volume-manualcertrenewal-completed-
```
