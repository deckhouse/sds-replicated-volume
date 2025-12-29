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

// Package rvrmetadata implements the rvr-metadata-controller, which manages
// metadata (owner references and labels) on ReplicatedVolumeReplica resources.
//
// # Controller Responsibilities
//
// The controller ensures proper ownership and metadata by:
//   - Setting metadata.ownerReferences on each RVR to point to its parent RV
//   - Using the controller reference pattern for proper cascading deletion
//   - Updating owner references if they become missing or incorrect
//   - Setting replicated-storage-class label from the parent RV
//   - Setting replicated-volume label from rvr.spec.replicatedVolumeName
//   - Setting kubernetes.io/hostname label from rvr.spec.nodeName (after scheduling)
//
// # Watched Resources
//
// The controller watches:
//   - ReplicatedVolumeReplica: To maintain owner references and labels
//
// # Owner Reference Configuration
//
// The controller uses controllerutil.SetControllerReference() to set:
//   - apiVersion: storage.deckhouse.io/v1alpha1
//   - kind: ReplicatedVolume
//   - name: From rvr.spec.replicatedVolumeName
//   - uid: From the actual RV resource
//   - controller: true
//   - blockOwnerDeletion: true
//
// # Labels Managed
//
//   - sds-replicated-volume.storage.deckhouse.io/replicated-storage-class: Name of the ReplicatedStorageClass (from RV)
//   - sds-replicated-volume.storage.deckhouse.io/replicated-volume: Name of the ReplicatedVolume
//   - kubernetes.io/hostname: Node hostname where replica is scheduled (from rvr.spec.nodeName)
//
// # Reconciliation Flow
//
//  1. Get the RVR being reconciled
//  2. Fetch the parent ReplicatedVolume using rvr.spec.replicatedVolumeName
//  3. Set owner reference using controllerutil.SetControllerReference()
//  4. Ensure replicated-storage-class label is set from rv.spec.replicatedStorageClassName
//  5. Ensure replicated-volume label is set from rvr.spec.replicatedVolumeName
//  6. Ensure kubernetes.io/hostname label is set from rvr.spec.nodeName (if scheduled)
//  7. Patch RVR if any changes were made
//
// # Special Notes
//
// Owner references enable Kubernetes garbage collection:
//   - When a ReplicatedVolume is deleted, all its RVRs are automatically marked for deletion
//   - blockOwnerDeletion=true prevents RV deletion if RVRs still exist (works with finalizers)
//
// The controller reference pattern ensures only one controller owns each RVR,
// preventing conflicts in lifecycle management.
//
// This controller complements rv-metadata-controller and rv-delete-propagation-controller
// to provide robust lifecycle management.
package rvrmetadata
