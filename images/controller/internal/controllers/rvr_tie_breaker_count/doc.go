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

// Package rvrtiebreakerccount implements the rvr-tie-breaker-count-controller,
// which manages TieBreaker replicas to maintain odd replica counts and prevent
// quorum ties in failure scenarios.
//
// # Controller Responsibilities
//
// The controller manages TieBreaker replicas by:
//   - Creating TieBreaker replicas to ensure odd total replica count
//   - Balancing replica distribution across failure domains
//   - Deleting unnecessary TieBreaker replicas
//   - Ensuring failure of any single failure domain doesn't cause quorum loss
//   - Preventing majority failure domain loss from leaving a quorum
//
// # Watched Resources
//
// The controller watches:
//   - ReplicatedVolume: To determine replica requirements
//   - ReplicatedVolumeReplica: To count existing replicas
//   - ReplicatedStorageClass: To get topology mode
//
// # Failure Domain Definition
//
// Failure Domain (FD) depends on topology:
//   - When rsc.spec.topology==TransZonal: FD is the zone (availability zone)
//   - Otherwise: FD is the node
//
// # TieBreaker Requirements
//
// The controller ensures:
//  1. Single FD failure must NOT cause quorum loss
//  2. Majority FD failure MUST cause quorum loss
//  3. Total replica count is odd
//  4. Replica difference between FDs is at most 1
//
// To achieve this, TieBreaker replicas are added to balance FDs to the minimum
// count where these conditions are satisfied.
//
// # Reconciliation Flow
//
//  1. Check prerequisites:
//     - RV must have the controller finalizer
//  2. If RV is being deleted (only module finalizers remain):
//     - Do not create new replicas
//  3. Get ReplicatedStorageClass to determine topology
//  4. Determine failure domains:
//     - TransZonal: Count replicas per zone
//     - Other: Count replicas per node
//  5. Count existing replicas in each FD (Diskful, Access, TieBreaker)
//  6. Calculate target replica distribution:
//     a. Determine minimum replica count per FD to satisfy requirements
//     b. Ensure total count is odd
//     c. Ensure FD counts differ by at most 1
//  7. For FDs with fewer than target count:
//     - Create TieBreaker replicas to reach target
//  8. For FDs with more than target count:
//     - Delete excess TieBreaker replicas (only TieBreaker type)
//  9. Set rvr.metadata.deletionTimestamp for replicas to be deleted
//
// # Status Updates
//
// This controller creates and deletes ReplicatedVolumeReplica resources with
// spec.type=TieBreaker. It does not directly update status fields.
//
// # Special Notes
//
// Quorum Safety:
//   - TieBreaker replicas participate in quorum but don't store data
//   - They prevent split-brain in even-replica configurations
//   - Example: With 2 Diskful replicas, add 1 TieBreaker for 3 total (quorum=2)
//
// TransZonal Topology:
//   - Replicas are distributed to maintain zone balance
//   - Zone failure should not cause quorum loss
//   - Majority zone failure should cause quorum loss (prevents split-brain)
//
// Dynamic Adjustment:
//   - As Diskful and Access replicas are added/removed, TieBreaker count adjusts
//   - Maintains odd count and balanced distribution automatically
//
// Conversion to Access:
//   - rv-publish-controller may convert TieBreaker to Access when needed for publishing
//   - This controller will create new TieBreaker replicas if balance is disrupted
//
// The TieBreaker mechanism is crucial for maintaining data consistency and
// availability in distributed replicated storage systems.
package rvrtiebreakerccount
