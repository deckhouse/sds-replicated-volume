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

// Package rvrstatusconfignodeid implements the rvr-status-config-node-id-controller,
// which assigns unique DRBD node IDs to replicas within a ReplicatedVolume.
//
// # Controller Responsibilities
//
// The controller ensures unique node ID assignment by:
//   - Allocating node IDs in the range [0, 7]
//   - Ensuring uniqueness among all replicas of the same ReplicatedVolume
//   - Persisting the assignment in rvr.status.drbd.config.nodeId
//
// # Watched Resources
//
// The controller watches:
//   - ReplicatedVolumeReplica: To detect replicas needing node ID assignment
//
// # Triggers
//
// The controller reconciles when:
//   - CREATE(RVR) where status.drbd.config.nodeId is nil
//
// # Node ID Allocation
//
// DRBD node IDs must be:
//   - In the range [0, 7] (DRBD supports maximum 8 nodes)
//   - Unique within each ReplicatedVolume
//   - Stable once assigned (never changed)
//
// Allocation algorithm:
//  1. List all RVRs for this ReplicatedVolume (via rvr.spec.replicatedVolumeName)
//  2. Collect all assigned node IDs
//  3. Find the smallest available ID in range [0, 7]
//  4. Assign it to rvr.status.drbd.config.nodeId
//
// # Reconciliation Flow
//
//  1. Check prerequisites:
//     - RV must have the controller finalizer
//  2. Check if rvr.status.drbd.config.nodeId is already set
//  3. If not set:
//     a. Get the ReplicatedVolume using rvr.spec.replicatedVolumeName
//     b. List all RVRs for this RV
//     c. Build a set of used node IDs (0-7)
//     d. Find smallest available ID
//     e. If all IDs are used (>8 replicas):
//        - Log error and retry (DRBD limitation)
//     f. Update rvr.status.drbd.config.nodeId
//
// # Status Updates
//
// The controller maintains:
//   - rvr.status.drbd.config.nodeId - Unique DRBD node ID within the volume
//
// # Error Handling
//
// If more than 8 replicas are requested (all IDs 0-7 used):
//   - The reconciliation fails and retries
//   - This should be prevented by validation, but is handled gracefully
//
// # Special Notes
//
// DRBD Limitation:
//   - DRBD protocol supports maximum 8 nodes (IDs 0-7)
//   - This limits total replicas (Diskful + Access + TieBreaker) to 8 per volume
//
// Node IDs are permanent for the lifetime of a replica. They are used in:
//   - DRBD configuration files
//   - Peer connection establishment
//   - Replication protocol communication
package rvrstatusconfignodeid
