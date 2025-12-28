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

// Package rvraccesscount implements the rvr-access-count-controller, which manages
// Access-type replicas to provide volume access on nodes without Diskful replicas.
//
// # Controller Responsibilities
//
// The controller manages Access replicas by:
//   - Creating Access replicas for nodes in rv.status.desiredAttachTo without other replica types
//   - Deleting Access replicas when they are no longer needed
//   - Ensuring enough replicas exist for requested access points
//
// # Watched Resources
//
// The controller watches:
//   - ReplicatedVolume: To monitor attachTo requirements
//   - ReplicatedVolumeReplica: To track existing replicas
//   - ReplicatedStorageClass: To check volumeAccess policy
//
// # Access Replica Requirements
//
// Access replicas are needed when:
//   - rsc.spec.volumeAccess != Local (Remote or Any access modes)
//   - A node is in rv.status.desiredAttachTo
//   - No Diskful or TieBreaker replica exists on that node
//
// Access replicas should be removed when:
//   - The node is no longer in rv.status.desiredAttachTo
//   - The node is not in rv.status.actuallyAttachedTo (not actively using the volume)
//
// # Reconciliation Flow
//
//  1. Check prerequisites:
//     - RV must have the controller finalizer
//     - rv.status.condition[type=IOReady].status must be True
//  2. If RV is being deleted (only module finalizers remain):
//     - Skip creation of new Access replicas
//  3. For each node in rv.status.desiredAttachTo:
//     a. Check if a replica already exists on that node
//     b. If no replica exists and rsc.spec.volumeAccess != Local:
//     - Create new RVR with spec.type=Access
//  4. For each Access replica:
//     a. If node not in rv.status.desiredAttachTo AND not in rv.status.actuallyAttachedTo:
//     - Delete the Access replica
//
// # Status Updates
//
// This controller creates, updates, and deletes ReplicatedVolumeReplica resources
// with spec.type=Access. It does not directly update status fields.
//
// # Special Notes
//
// Local Volume Access:
//   - When rsc.spec.volumeAccess==Local, Access replicas are not created
//   - Only Diskful replicas can provide Local access
//
// TieBreaker Conversion:
//   - TieBreaker replicas can be converted to Access replicas by rv-attach-controller
//     when promotion to Primary is required
//
// The controller only processes resources when the RV has the controller finalizer
// and IOReady condition is True, ensuring the volume is in a stable state.
package rvraccesscount
