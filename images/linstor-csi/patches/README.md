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
