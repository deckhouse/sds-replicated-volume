/*
Copyright 2025 Flant JSC

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

// Package rvfinalizer implements the rv-finalizer-controller, which manages the
// controller finalizer on ReplicatedVolume resources.
//
// # Controller Responsibilities
//
// The controller ensures proper lifecycle management by:
//   - Adding the controller finalizer (sds-replicated-volume.storage.deckhouse.io/controller) to new RVs
//   - Removing the finalizer when deletion is safe (all RVRs are gone)
//
// # Watched Resources
//
// The controller watches:
//   - ReplicatedVolume: To manage finalizers
//   - ReplicatedVolumeReplica: To track when all replicas are deleted
//
// # Reconciliation Flow
//
// When RV is not being deleted (metadata.deletionTimestamp is not set):
//  1. Check if the finalizer sds-replicated-volume.storage.deckhouse.io/controller exists
//  2. If not present, add it to rv.metadata.finalizers
//
// When RV is being deleted (metadata.deletionTimestamp is set):
//  1. List all ReplicatedVolumeReplicas with rvr.spec.replicatedVolumeName matching the RV
//  2. If any RVRs exist, keep the finalizer (deletion is not safe)
//  3. If no RVRs exist, remove the controller finalizer from rv.metadata.finalizers
//
// # Status Updates
//
// This controller does not update status fields; it only manages finalizers.
//
// # Special Notes
//
// The finalizer ensures that a ReplicatedVolume cannot be fully deleted from the cluster
// until all its replicas have been removed, preventing orphaned resources and ensuring
// proper cleanup.
//
// This controller works with rv-delete-propagation-controller, which triggers deletion
// of RVRs when an RV is deleted.
package rvfinalizer
