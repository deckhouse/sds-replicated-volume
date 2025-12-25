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

// Package rvrvolume implements the rvr-volume-controller, which manages the lifecycle
// of LVM Logical Volumes (LLV) backing Diskful replicas.
//
// # Controller Responsibilities
//
// The controller manages LVM volumes by:
//   - Creating LLV resources for Diskful replicas
//   - Setting owner references on LLVs pointing to RVRs
//   - Updating rvr.status.lvmLogicalVolumeName when LLV is ready
//   - Deleting LLVs when replica type changes from Diskful
//   - Clearing lvmLogicalVolumeName status after LLV deletion
//
// # Watched Resources
//
// The controller watches:
//   - ReplicatedVolumeReplica: To manage LLV lifecycle
//   - LvmLogicalVolume: To track LLV readiness and status
//
// # LLV Lifecycle Management
//
// Create LLV when:
//   - rvr.spec.type==Diskful
//   - rvr.metadata.deletionTimestamp==nil (not being deleted)
//   - No LLV exists yet for this RVR
//
// Delete LLV when:
//   - rvr.spec.type!=Diskful (type changed to Access or TieBreaker)
//   - rvr.status.actualType==rvr.spec.type (actual type matches desired)
//     * This ensures DRBD has released the volume before deletion
//
// # Reconciliation Flow
//
//  1. Check prerequisites:
//     - RV must have the controller finalizer
//  2. Get the RVR being reconciled
//  3. Check rvr.spec.type:
//
// For Diskful replicas (rvr.spec.type==Diskful AND deletionTimestamp==nil):
//     a. Check if LLV already exists (by owner reference or name)
//     b. If LLV doesn't exist:
//        - Create new LLV resource
//        - Set spec.size from RV
//        - Set spec.lvmVolumeGroupName from storage pool
//        - Set metadata.ownerReferences pointing to RVR
//     c. If LLV exists and is ready:
//        - Update rvr.status.lvmLogicalVolumeName to LLV name
//
// For non-Diskful replicas (rvr.spec.type!=Diskful):
//     a. Check if rvr.status.actualType==rvr.spec.type (type transition complete)
//     b. If types match and LLV exists:
//        - Delete the LLV
//     c. After LLV deletion:
//        - Clear rvr.status.lvmLogicalVolumeName
//
//  4. If rvr.metadata.deletionTimestamp is set:
//     - LLV will be deleted via owner reference cascade (handled by Kubernetes)
//
// # Status Updates
//
// The controller maintains:
//   - rvr.status.lvmLogicalVolumeName - Name of the associated LLV (when ready)
//
// Creates and manages:
//   - LvmLogicalVolume resources with owner references
//
// # Owner References
//
// LLVs have ownerReferences set to point to their RVR:
//   - Enables automatic LLV cleanup when RVR is deleted
//   - Uses controller reference pattern (controller=true, blockOwnerDeletion=true)
//
// # Special Notes
//
// Type Transitions:
//   - When replica type changes (e.g., Diskful→Access for quorum rebalancing)
//   - Must wait for rvr.status.actualType to match rvr.spec.type
//   - This ensures DRBD has released the disk before LVM volume deletion
//   - Prevents data corruption and resource conflicts
//
// LLV Readiness:
//   - Only set lvmLogicalVolumeName when LLV is ready (can be used by DRBD)
//   - This prevents drbd-config-controller from trying to use non-ready volumes
//
// Storage Pool Integration:
//   - LLV is created on the storage pool specified in ReplicatedStorageClass
//   - Node must have the required LVM volume group available
//   - Scheduling controller ensures nodes are selected appropriately
//
// The LLV provides the underlying storage layer for DRBD replication, bridging
// the ReplicatedVolume abstraction with actual LVM-based storage on nodes.
package rvrvolume
