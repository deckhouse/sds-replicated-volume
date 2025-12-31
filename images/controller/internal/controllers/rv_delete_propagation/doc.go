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

// Package rvdeletepropagation implements the rv-delete-propagation-controller,
// which propagates deletion from ReplicatedVolume to all its ReplicatedVolumeReplicas.
//
// # Controller Responsibilities
//
// The controller ensures proper cleanup by:
//   - Detecting when a ReplicatedVolume has metadata.deletionTimestamp set
//   - Triggering deletion of all associated ReplicatedVolumeReplicas
//
// # Watched Resources
//
// The controller watches:
//   - ReplicatedVolume: To detect deletion events
//   - ReplicatedVolumeReplica: To identify replicas belonging to deleted volumes
//
// # Reconciliation Flow
//
//  1. Check if ReplicatedVolume has metadata.deletionTimestamp set
//  2. List all ReplicatedVolumeReplicas with rvr.spec.replicatedVolumeName matching the RV
//  3. For each RVR without deletionTimestamp:
//     - Trigger deletion by calling Delete on the RVR
//
// # Status Updates
//
// This controller does not update any status fields; it only triggers RVR deletions.
//
// # Special Notes
//
// This controller works in conjunction with rv-finalizer-controller, which manages
// the RV finalizer and ensures the RV is not fully deleted until all RVRs are removed.
package rvdeletepropagation
