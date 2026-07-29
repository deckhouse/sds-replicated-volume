/*
Copyright 2026 Flant JSC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

const AgentFinalizer = "sds-replicated-volume.deckhouse.io/agent"

const RSCControllerFinalizer = "sds-replicated-volume.deckhouse.io/rsc-controller"

const RSCControllerFinalizerLegacy = "replicatedstorageclass.storage.deckhouse.io"

const RVControllerFinalizer = "sds-replicated-volume.deckhouse.io/rv-controller"

// RVCloneSourceFinalizer is placed on a ReplicatedVolume that currently serves
// as a data source (spec.dataSource) for another ReplicatedVolume being cloned
// from it. The finalizer prevents the source RV from being deleted while clone
// bootstrap (LVM thin-clone on the primary + DRBD initial sync on peers) is in
// progress; it is removed once the target RV finishes formation or starts
// deleting itself.
const RVCloneSourceFinalizer = "sds-replicated-volume.deckhouse.io/rv-clone-source"

const RVRControllerFinalizer = "sds-replicated-volume.deckhouse.io/rvr-controller"

// CSIControllerFinalizer is set on ReplicatedVolume and ReplicatedVolumeAttachment
// by the CSI controller when creating or ensuring those resources.
const CSIControllerFinalizer = "sds-replicated-volume.deckhouse.io/csi-controller"

const RVSControllerFinalizer = "sds-replicated-volume.deckhouse.io/rvs-controller"

// RVSRestoreSourceFinalizer is placed on a ReplicatedVolumeSnapshot that
// currently serves as a data source (spec.dataSource) for a ReplicatedVolume
// being restored from it. It is the snapshot-side counterpart of
// [RVCloneSourceFinalizer]: while restore is in progress the target volume still
// reads from the per-replica snapshots, so the snapshot must neither disappear
// from the API nor have its children garbage-collected. Removed once the target
// RV finishes formation or starts deleting itself.
const RVSRestoreSourceFinalizer = "sds-replicated-volume.deckhouse.io/rvs-restore-source"

const RVRSControllerFinalizer = "sds-replicated-volume.deckhouse.io/rvrs-controller"

// StorageClassFinalizer is set on Kubernetes StorageClass objects managed by
// the RSC controller. Uses the legacy storage.deckhouse.io prefix for
// backward compatibility with the old controller.
const StorageClassFinalizer = "storage.deckhouse.io/sds-replicated-volume"
