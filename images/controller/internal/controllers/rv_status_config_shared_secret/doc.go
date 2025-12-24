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

// Package rvstatusconfigsharedsecret implements the rv-status-config-shared-secret-controller,
// which manages DRBD shared secret and hash algorithm selection for ReplicatedVolumes.
//
// # Controller Responsibilities
//
// The controller manages DRBD authentication by:
//   - Generating initial shared secret for new volumes
//   - Selecting appropriate hash algorithm (sha256, sha1)
//   - Handling algorithm incompatibility errors from replicas
//   - Falling back to alternative algorithms when needed
//
// # Watched Resources
//
// The controller watches:
//   - ReplicatedVolume: To initialize shared secret configuration
//   - ReplicatedVolumeReplica: To detect algorithm incompatibility errors
//
// # Triggers
//
// The controller reconciles when:
//   - CREATE(RV) - Initialize shared secret and algorithm
//   - CREATE/UPDATE(RVR) - Check for algorithm errors and retry with fallback
//
// # Hash Algorithm Selection
//
// Supported algorithms (in preference order):
//  1. sha256 (preferred, more secure)
//  2. sha1 (fallback for older DRBD versions)
//
// # Reconciliation Flow
//
// For new ReplicatedVolumes:
//  1. Check if rv.status.drbd.config.sharedSecret is set
//  2. If not set:
//     a. Generate a new random shared secret
//     b. Set rv.status.drbd.config.sharedSecretAlg = "sha256" (first algorithm)
//     c. Update rv.status.drbd.config.sharedSecret
//
// For existing ReplicatedVolumes with algorithm errors:
//  1. Check all RVRs for rvr.status.drbd.errors.sharedSecretAlgSelectionError
//  2. If any RVR reports unsupported algorithm:
//     a. Extract the failed algorithm from error.unsupportedAlg
//     b. Select the next algorithm from the supported list
//     c. If next algorithm exists:
//        - Generate new shared secret
//        - Update rv.status.drbd.config.sharedSecretAlg
//        - Update rv.status.drbd.config.sharedSecret
//     d. If no more algorithms available:
//        - Set rv.status.conditions[type=SharedSecretAlgorithmSelected].status=False
//        - Set reason=UnableToSelectSharedSecretAlgorithm
//        - Include details in message (node, algorithm)
//
// # Status Updates
//
// The controller maintains:
//   - rv.status.drbd.config.sharedSecret - Randomly generated authentication secret
//   - rv.status.drbd.config.sharedSecretAlg - Selected hash algorithm (sha256 or sha1)
//   - rv.status.conditions[type=SharedSecretAlgorithmSelected] - Algorithm selection status
//
// # Error Handling
//
// When all algorithms have been exhausted without success:
//   - The condition SharedSecretAlgorithmSelected is set to False
//   - The reason indicates inability to select a working algorithm
//   - The volume cannot proceed to Ready state
//
// # Special Notes
//
// The shared secret is used by DRBD for peer authentication. All replicas of a volume
// must use the same secret and hash algorithm. If nodes have different DRBD versions
// with different algorithm support, the controller will try fallback options.
//
// The secret is regenerated each time the algorithm changes to ensure security.
package rvstatusconfigsharedsecret
