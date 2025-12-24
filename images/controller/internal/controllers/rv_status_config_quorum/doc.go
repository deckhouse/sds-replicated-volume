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

// Package rvstatusconfigquorum implements the rv-status-config-quorum-controller,
// which calculates and maintains DRBD quorum configuration for ReplicatedVolumes.
//
// # Controller Responsibilities
//
// The controller manages quorum settings by:
//   - Calculating appropriate quorum values based on replica count
//   - Setting quorumMinimumRedundancy based on Diskful replica count
//   - Ensuring cluster stability before raising quorum
//   - Managing finalizers on replicas to prevent unsafe quorum reduction
//
// # Watched Resources
//
// The controller watches:
//   - ReplicatedVolume: To calculate and update quorum configuration
//   - ReplicatedVolumeReplica: To count replicas and manage finalizers
//
// # Triggers
//
// The controller reconciles when:
//   - CREATE/UPDATE(RV) where rv.status.conditions[type=Ready].status==True
//
// # Quorum Calculation
//
// Given:
//   - N = total number of replicas (all types)
//   - M = number of Diskful replicas
//
// The quorum is calculated as:
//
//	if M > 1 {
//	    quorum = max(2, N/2 + 1)
//	    quorumMinimumRedundancy = max(2, M/2 + 1)
//	} else {
//	    quorum = 0
//	    quorumMinimumRedundancy = 0
//	}
//
// # Reconciliation Flow
//
//  1. Verify the volume is ready (all Ready conditions except QuorumConfigured are True)
//  2. Count total replicas (N) and Diskful replicas (M)
//  3. Calculate quorum and quorumMinimumRedundancy values
//  4. Before increasing quorum:
//     - Add finalizer to each RVR to prevent accidental deletion during quorum change
//  5. Update rv.status.drbd.config.quorum and rv.status.drbd.config.quorumMinimumRedundancy
//  6. Handle replica deletion:
//     - When rvr.metadata.deletionTimestamp is set, only remove finalizer after
//       quorum has been safely reduced
//  7. Update rv.status.conditions[type=QuorumConfigured]:
//     - status=True when quorum is properly configured
//     - status=False if configuration failed
//
// # Status Updates
//
// The controller maintains:
//   - rv.status.drbd.config.quorum - Minimum number of replicas for consensus
//   - rv.status.drbd.config.quorumMinimumRedundancy - Minimum Diskful replicas for quorum
//   - rv.status.conditions[type=QuorumConfigured] - Quorum configuration status
//
// # Special Notes
//
// Quorum ensures data safety:
//   - Prevents split-brain scenarios in distributed storage
//   - Ensures writes succeed only when enough replicas acknowledge
//   - Protects against data loss when nodes fail
//
// The controller carefully manages quorum changes to avoid data unavailability or
// split-brain conditions during replica scaling operations.
package rvstatusconfigquorum
