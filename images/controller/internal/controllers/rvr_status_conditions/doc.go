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

// Package rvrstatusconditions implements the rvr-status-conditions-controller,
// which aggregates various status conditions to determine the overall Ready status
// of a ReplicatedVolumeReplica.
//
// # Controller Responsibilities
//
// The controller evaluates replica readiness by:
//   - Checking all required Ready conditions
//   - Computing the overall Ready condition based on sub-conditions
//   - Determining appropriate reasons for non-ready states
//
// # Watched Resources
//
// The controller watches:
//   - ReplicatedVolumeReplica: To evaluate and update status conditions
//
// # Ready Conditions
//
// A ReplicatedVolumeReplica is considered Ready when ALL of the following conditions are True:
//   - InitialSync==True - Initial synchronization completed
//   - DevicesReady==True - DRBD devices are ready
//   - ConfigurationAdjusted==True - DRBD configuration is applied
//   - Quorum==True - Quorum requirements are met
//   - DiskIOSuspended==False - Disk I/O is not suspended
//   - AddressConfigured==True - Network address is configured
//
// # Condition Reasons
//
// The Ready condition can have various reasons indicating the specific issue:
//   - WaitingForInitialSync: Initial sync not yet complete
//   - DevicesAreNotReady: DRBD devices not ready
//   - AdjustmentFailed: DRBD configuration adjustment failed
//   - NoQuorum: Quorum not achieved
//   - DiskIOSuspended: Disk I/O is suspended
//   - Ready: All conditions satisfied
//
// # Reconciliation Flow
//
//  1. Check prerequisites:
//     - RV must have the controller finalizer
//  2. Evaluate each sub-condition from rvr.status.conditions
//  3. Determine if all Ready conditions are satisfied
//  4. Set rvr.status.conditions[type=Ready]:
//     - status=True with reason=Ready if all conditions met
//     - status=False with specific reason indicating first failing condition
//  5. Update the condition with appropriate message for user visibility
//
// # Status Updates
//
// The controller maintains:
//   - rvr.status.conditions[type=Ready] - Overall readiness status
//
// # Special Notes
//
// The Ready condition serves as a high-level indicator that applications and other
// controllers can depend on to determine if a replica is fully operational and can
// serve I/O requests.
//
// The controller uses a priority order when multiple conditions are False to report
// the most critical or blocking issue first.
package rvrstatusconditions
