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

// Package rvstatusconditions implements the rv-status-conditions-controller,
// which aggregates various status conditions to determine the overall Ready status
// of a ReplicatedVolume.
//
// # Controller Responsibilities
//
// The controller evaluates readiness by:
//   - Checking all required Ready conditions
//   - Computing the overall Ready condition based on sub-conditions
//   - Determining the phase (Terminating, Synchronizing, Ready)
//
// # Watched Resources
//
// The controller watches:
//   - ReplicatedVolume: To evaluate and update status conditions
//
// # Ready Conditions
//
// A ReplicatedVolume is considered Ready when ALL of the following conditions are True:
//   - QuorumConfigured - Quorum settings are properly configured
//   - DiskfulReplicaCountReached - Required number of Diskful replicas exists
//   - AllReplicasReady - All replicas report Ready status
//   - SharedSecretAlgorithmSelected - Shared secret algorithm is selected and valid
//
// # Phase Determination
//
// The controller sets rv.status.phase based on the current state:
//   - Terminating: metadata.deletionTimestamp is set
//   - Synchronizing: Not all replicas are synchronized or ready
//   - Ready: All Ready conditions are satisfied
//
// # Reconciliation Flow
//
//  1. Evaluate each sub-condition from rv.status.conditions
//  2. Check if all required conditions have status=True
//  3. Set rv.status.conditions[type=Ready]:
//     - status=True if all conditions met
//     - status=False with appropriate reason if any condition fails
//  4. Set rv.status.phase based on current state
//
// # Status Updates
//
// The controller maintains:
//   - rv.status.conditions[type=Ready] - Overall readiness status
//   - rv.status.phase - Current lifecycle phase
package rvstatusconditions
