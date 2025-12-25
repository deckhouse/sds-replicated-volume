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

// Package rvrownerreference implements the rvr-owner-reference-controller, which
// maintains the owner reference relationship between ReplicatedVolumeReplicas and
// their parent ReplicatedVolume.
//
// # Controller Responsibilities
//
// The controller ensures proper ownership by:
//   - Setting metadata.ownerReferences on each RVR to point to its parent RV
//   - Using the controller reference pattern for proper cascading deletion
//   - Updating owner references if they become missing or incorrect
//
// # Watched Resources
//
// The controller watches:
//   - ReplicatedVolumeReplica: To maintain owner references
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
// # Reconciliation Flow
//
//  1. Check prerequisites:
//     - RV must have the controller finalizer
//  2. Get the RVR being reconciled
//  3. Fetch the parent ReplicatedVolume using rvr.spec.replicatedVolumeName
//  4. Check if owner reference is correctly set:
//     - Reference exists in rvr.metadata.ownerReferences
//     - Reference points to correct RV (name and UID match)
//     - controller=true and blockOwnerDeletion=true are set
//  5. If owner reference is missing or incorrect:
//     - Call controllerutil.SetControllerReference(rv, rvr, scheme)
//     - Update the RVR
//
// # Status Updates
//
// This controller does not update status fields; it only manages metadata.ownerReferences.
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
// This controller complements rv-finalizer-controller and rv-delete-propagation-controller
// to provide robust lifecycle management.
package rvrownerreference
