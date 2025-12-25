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

// Package rvrstatusconfigpeers implements the rvr-status-config-peers-controller,
// which maintains the peer list for each ReplicatedVolumeReplica, enabling DRBD
// replication connections.
//
// # Controller Responsibilities
//
// The controller manages peer relationships by:
//   - Populating rvr.status.drbd.config.peers with ready peer replicas
//   - Including only replicas that are ready for DRBD connections
//   - Excluding the replica itself from its peer list
//   - Marking the peer list as initialized
//
// # Watched Resources
//
// The controller watches:
//   - ReplicatedVolumeReplica: To maintain peer lists across all replicas
//
// # Ready Replica Definition
//
// A replica is considered ready to be a peer when ALL of the following are set:
//   - rvr.spec.nodeName != "" (scheduled to a node)
//   - rvr.status.drbd.config.nodeId != nil (DRBD node ID assigned)
//   - rvr.status.drbd.config.address != nil (network address configured)
//
// # Reconciliation Flow
//
//  1. Check prerequisites:
//     - RV must have the controller finalizer
//  2. Get the RVR being reconciled
//  3. Get the ReplicatedVolume using rvr.spec.replicatedVolumeName
//  4. List all RVRs belonging to this RV
//  5. For each RVR in the volume:
//     a. Collect ready peers (meeting Ready Replica criteria)
//     b. Exclude the current replica from its own peer list
//     c. Build peer entries with:
//     - nodeId: rvr.status.drbd.config.nodeId
//     - address: rvr.status.drbd.config.address
//     - Any other relevant peer information
//  6. Update rvr.status.drbd.config.peers with the peer list
//  7. Set rvr.status.drbd.config.peersInitialized = true
//     (even if peer list is empty - first replica case)
//
// # Peer List Structure
//
// Each peer entry contains:
//   - Node ID: DRBD node identifier
//   - Address: Network address (IPv4 and port) for DRBD communication
//
// # Status Updates
//
// The controller maintains:
//   - rvr.status.drbd.config.peers - List of peer replicas
//   - rvr.status.drbd.config.peersInitialized - Initialization flag
//
// # Special Notes
//
// Initialization Flag:
//   - Set to true after first peer list update
//   - Remains true even if peer list becomes empty (e.g., during replica scaling)
//   - Used by drbd-config-controller to determine if it can proceed with configuration
//
// First Replica Case:
//   - The first replica will have an empty peer list initially
//   - peersInitialized is still set to true to allow DRBD configuration
//   - As more replicas become ready, they are added to peer lists
//
// Dynamic Peer Updates:
//   - Peer lists are updated as replicas are added, removed, or change state
//   - All replicas get updated peer lists when any replica's readiness changes
//   - DRBD configuration is adjusted on nodes to reflect new peer topology
//
// The peer list enables DRBD to establish replication connections between nodes,
// forming the mesh network necessary for distributed storage.
package rvrstatusconfigpeers
