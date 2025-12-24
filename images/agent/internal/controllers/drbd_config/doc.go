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

// Package drbdconfig implements the drbd-config-controller, which synchronizes desired
// configuration from ReplicatedVolume and ReplicatedVolumeReplica resources with actual
// DRBD configuration on the node.
//
// # Controller Responsibilities
//
// The controller ensures that DRBD resources are properly configured and synchronized on the
// local node by:
//   - Writing and validating DRBD resource configuration files
//   - Creating DRBD metadata for Diskful replicas
//   - Performing initial synchronization for new Diskful replicas
//   - Executing DRBD commands (up, adjust) to apply configuration
//   - Managing finalizers for proper cleanup during resource deletion
//   - Tracking configuration errors in RVR status
//
// # Watched Resources
//
// The controller watches:
//   - ReplicatedVolume: Primary resource containing shared DRBD configuration
//   - ReplicatedVolumeReplica: Replica-specific configuration for the local node
//
// Only replicas where rvr.spec.nodeName matches the controller's NODE_NAME are processed.
//
// # Required Fields
//
// Before proceeding with configuration, the following fields must be initialized:
//   - rv.metadata.name
//   - rv.status.drbd.config.sharedSecret
//   - rv.status.drbd.config.sharedSecretAlg
//   - rv.status.drbd.config.deviceMinor
//   - rvr.status.drbd.config.nodeId
//   - rvr.status.drbd.config.address
//   - rvr.status.drbd.config.peers (with peersInitialized flag)
//   - rvr.status.lvmLogicalVolumeName (only for Diskful replicas)
//
// # Reconciliation Flow
//
// When the replica is not being deleted (rvr.metadata.deletionTimestamp is not set):
//  1. Add finalizers to RVR:
//     - sds-replicated-volume.storage.deckhouse.io/agent
//     - sds-replicated-volume.storage.deckhouse.io/controller
//  2. Write configuration to temporary file and validate with `drbdadm sh-nop`
//  3. If valid, move configuration to main file; otherwise report error and stop
//  4. For Diskful replicas:
//     - Check for metadata existence with `drbdadm dump-md`
//     - Create metadata if missing with `drbdadm create-md`
//     - Perform initial sync if needed (first replica with no peers):
//       * Execute `drbdadm primary --force`
//       * Execute `drbdadm secondary`
//     - Set rvr.status.drbd.actual.initialSyncCompleted=true
//  5. For non-Diskful replicas:
//     - Set rvr.status.drbd.actual.initialSyncCompleted=true immediately
//  6. Check if resource is up with `drbdadm status`
//  7. If not up, execute `drbdadm up`
//  8. Execute `drbdadm adjust` to apply configuration changes
//
// When the replica is being deleted (rvr.metadata.deletionTimestamp is set):
//  1. If other finalizers exist besides agent finalizer, stop reconciliation
//  2. Execute `drbdadm down` to stop DRBD resource
//  3. Remove configuration files (main and temporary)
//  4. Remove agent finalizer (last one to be removed)
//
// # Status Updates
//
// The controller maintains the following status fields:
//   - rvr.status.drbd.errors.* - Validation and command execution errors
//   - rvr.status.drbd.actual.disk - Path to the LVM logical volume (Diskful only)
//   - rvr.status.drbd.actual.allowTwoPrimaries - Applied from RV config
//   - rvr.status.drbd.actual.initialSyncCompleted - Initial sync completion flag
//
// # Special Handling
//
// TieBreaker replicas require special DRBD parameters to avoid metadata synchronization
// to the node (no local disk storage).
//
// The controller only processes resources when the RV has the controller finalizer
// (sds-replicated-volume.storage.deckhouse.io/controller) set, ensuring proper
// initialization order.
//
// Resources marked for deletion (metadata.deletionTimestamp set) are only considered
// deleted if they don't have non-module finalizers (those not starting with
// sds-replicated-volume.storage.deckhouse.io/).
package drbdconfig
