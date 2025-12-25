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

// Package drbdprimary implements the drbd-primary-controller, which manages the DRBD
// resource role (Primary/Secondary) on the local node.
//
// # Controller Responsibilities
//
// The controller ensures that the actual DRBD resource role matches the desired role by:
//   - Executing `drbdadm primary` when promotion to Primary is needed
//   - Executing `drbdadm secondary` when demotion to Secondary is needed
//   - Reporting DRBD command errors in RVR status
//
// # Watched Resources
//
// The controller watches:
//   - ReplicatedVolumeReplica: To monitor desired and actual role configuration
//
// Only replicas where rvr.spec.nodeName matches the controller's NODE_NAME are processed.
//
// # Preconditions
//
// The controller only executes role changes when ALL of the following conditions are met:
//   - rv.status.conditions[type=Ready].status=True
//   - rvr.status.drbd.initialSyncCompleted=true
//     Either:
//   - Promotion needed: rvr.status.drbd.config.primary==true AND rvr.status.drbd.status.role!=Primary
//   - Demotion needed: rvr.status.drbd.config.primary==false AND rvr.status.drbd.status.role==Primary
//
// # Reconciliation Flow
//
//  1. Check that the ReplicatedVolume is ready (all Ready conditions satisfied)
//  2. Verify initial synchronization is complete
//  3. Compare desired role (rvr.status.drbd.config.primary) with actual role (rvr.status.drbd.status.role)
//  4. If promotion is needed:
//     - Execute `drbdadm primary <resource-name>`
//  5. If demotion is needed:
//     - Execute `drbdadm secondary <resource-name>`
//  6. Report any command errors to rvr.status.drbd.errors.*
//
// # Status Updates
//
// The controller maintains:
//   - rvr.status.drbd.errors.* - DRBD command execution errors
//
// # Special Notes
//
// The controller only processes resources when the RV has the controller finalizer
// (sds-replicated-volume.storage.deckhouse.io/controller) set.
//
// Resources marked for deletion (metadata.deletionTimestamp set) are only considered
// deleted if they don't have non-module finalizers (those not starting with
// sds-replicated-volume.storage.deckhouse.io/).
package drbdprimary
