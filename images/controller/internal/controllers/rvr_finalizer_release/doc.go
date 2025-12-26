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

// Package rvrfinalizerrelease implements the rvr-finalizer-release-controller,
// which safely releases the controller finalizer from ReplicatedVolumeReplicas
// when deletion is safe for the cluster.
//
// # Controller Responsibilities
//
// The controller ensures safe replica deletion by:
//   - Verifying cluster stability before allowing replica removal
//   - Checking quorum requirements are maintained
//   - Ensuring sufficient Diskful replicas remain
//   - Confirming replicas are not published (not Primary)
//   - Removing the controller finalizer when conditions are met
//
// # Background
//
// The agent sets two finalizers on each RVR:
//   - sds-replicated-volume.storage.deckhouse.io/agent (F/agent)
//   - sds-replicated-volume.storage.deckhouse.io/controller (F/controller)
//
// The agent will not remove DRBD resources or remove its finalizer while F/controller
// remains. This controller's job is to release F/controller only when safe to do so.
//
// # Watched Resources
//
// The controller watches:
//   - ReplicatedVolumeReplica: To detect deletion requests
//   - ReplicatedVolume: To check cluster state and requirements
//   - ReplicatedStorageClass: To determine required Diskful replica count
//
// # Safety Conditions
//
// The controller removes F/controller from a deleting RVR when ALL conditions are met:
//
// Always required:
//   - Replica is not published: node not in rv.status.publishedOn
//   - For RV deletion (rv.metadata.deletionTimestamp set):
//   - All replicas must be unpublished (len(rv.status.publishedOn)==0)
//
// When RV is NOT being deleted (rv.metadata.deletionTimestamp==nil):
//   - Remaining online replicas >= quorum:
//   - Count rvr.status.conditions[type=Online].status==True
//   - Exclude the replica being deleted
//   - Count must be >= rv.status.drbd.config.quorum
//   - Sufficient Diskful replicas remain:
//   - Count rvr.spec.Type==Diskful AND rvr.status.actualType==Diskful
//   - Count rvr.status.conditions[type=IOReady].status==True
//   - Exclude replicas being deleted (rvr.metadata.deletionTimestamp!=nil)
//   - Count must meet rsc.spec.replication requirements
//
// # Reconciliation Flow
//
//  1. Check if RVR has metadata.deletionTimestamp set
//  2. If not deleting, skip reconciliation
//  3. Get the associated ReplicatedVolume
//  4. Check if RV is being deleted:
//     a. If yes, verify len(rv.status.publishedOn)==0
//     b. If condition met, remove F/controller and exit
//  5. For non-deleted RV:
//     a. Count online replicas (excluding current RVR)
//     b. Verify count >= rv.status.drbd.config.quorum
//     c. Get ReplicatedStorageClass and determine required Diskful count
//     d. Count ready Diskful replicas (excluding those being deleted)
//     e. Verify count meets replication requirements
//     f. Verify current RVR node not in rv.status.publishedOn
//  6. If all conditions met:
//     - Remove sds-replicated-volume.storage.deckhouse.io/controller from finalizers
//
// # Status Updates
//
// This controller does not update status fields; it only manages finalizers.
//
// # Special Notes
//
// This controller replaces the older rvr-quorum-and-publish-constrained-release-controller
// with enhanced safety checks including the Online condition.
//
// The IOReady condition is checked instead of just Ready to ensure the replica can
// actually perform I/O operations before being counted toward stability requirements.
package rvrfinalizerrelease
