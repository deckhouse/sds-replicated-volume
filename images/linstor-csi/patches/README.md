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

The driver reads our own CRDs for this, so the patch adds two module requirements:
`github.com/deckhouse/sds-node-configurator/api` and
`github.com/deckhouse/sds-replicated-volume/api`, each pinned to the commit
that last touched its `api/` directory. They used to be the `github.com/deckhouse/...`
copies, which stopped receiving our changes when those repositories were archived.

Two consequences worth knowing before regenerating this patch:

- The `sds-replicated-volume/api` module itself requires `k8s.io/apimachinery v0.35.2`,
  `k8s.io/api v0.35.1` and `sigs.k8s.io/controller-runtime v0.23.3`, so minimal version
  selection pulls the whole Kubernetes set of the driver forward from `v0.32.x`, together
  with `structured-merge-diff` v4 -> v6 and the `go` directive to 1.26.6. That is inherent
  to consuming the module; the alternative is a dedicated api tag with a conservative
  dependency set.
- `ReplicatedStorageClass.Spec.VolumeAccess` and `.Topology` are named string types in the
  fox api, not plain strings, so `pkg/driver/driver.go` converts them explicitly where it
  fills `volume.AccessibleTopologiesParams`.

The image therefore builds with the private-module credential, like every other Go image
in this repository.

### 005-add-filesystem-creation-on-mount-failure.patch

Add Filesystem Creation on Mount Failure

This patch introduces a mechanism to automatically create a filesystem on a volume if mounting fails due to a "wrong fs type" error. Specifically, if the filesystem type is `ext4` and the mount operation returns an error indicating a mismatch in filesystem type, the system will attempt to create an `ext4` filesystem on the volume. If the filesystem creation is successful, it will retry mounting the volume. This ensures that volumes are correctly formatted and mounted, reducing manual intervention in case of such errors.

### 007-fix-cve.patch

CVE fix

Bumps `google.golang.org/grpc` to v1.82.1 and `google.golang.org/protobuf` to v1.36.11,
pulling `golang.org/x/oauth2` and `genproto/googleapis/rpc` forward with them.

`006-fix-cve.patch` used to sit here and raised `golang.org/x/oauth2` to v0.27.0. It was
dropped: the Kubernetes set that `004` now brings in already resolves `oauth2` well past
that version, so the patch no longer had a line to change and would only fail to apply.

Bump golang.org/x/net, golang.org/x/text, golang.org/x/sys and google.golang.org/grpc to versions without known vulnerabilities.
