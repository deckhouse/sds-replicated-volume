## Patches

### 001-requisites.patch

Fix multiple requisites

Sometimes Kubernetes may request multiple requisites in topology in CreateVolume request.
This patch considers just the first one as the requested node.

- https://github.com/piraeusdatastore/linstor-csi/pull/196

### 002-rename-linbit-labels.patch

Rename linbit labels

This patch renames following Linstor-csi-node labels: - linbit.com/hostname -> storage.deckhouse.io/sds-replicated-volume-hostname - linbit.com/sp-> storage.deckhouse.io/sds-replicated-volume-sp-

### 003-new-csi-path.patch

Change csi endpoint

Change csi endpoint from `linstor.csi.linbit.com` to `replicated.csi.storage.deckhouse.io`.

### 004-csi-add-new-topology-logic.patch

Add new topology logic

### 005-add-filesystem-creation-on-mount-failure.patch

Add Filesystem Creation on Mount Failure

This patch introduces a mechanism to automatically create a filesystem on a volume if mounting fails due to a "wrong fs type" error. Specifically, if the filesystem type is `ext4` and the mount operation returns an error indicating a mismatch in filesystem type, the system will attempt to create an `ext4` filesystem on the volume. If the filesystem creation is successful, it will retry mounting the volume. This ensures that volumes are correctly formatted and mounted, reducing manual intervention in case of such errors.

### 006-fix-cve.patch

CVE fix
