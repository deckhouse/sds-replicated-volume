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

// Package rvpublishcontroller implements the rv-publish-controller, which manages
// the promotion and demotion of DRBD replicas to Primary role based on volume
// access requirements.
//
// # Controller Responsibilities
//
// The controller ensures replicas are promoted/demoted correctly by:
//   - Monitoring rv.spec.publishOn for nodes requiring volume access
//   - Setting rvr.status.drbd.config.primary to control replica promotion
//   - Managing allowTwoPrimaries configuration for live migration scenarios
//   - Updating rv.status.publishedOn to reflect actual Primary replicas
//   - Converting TieBreaker replicas to Access replicas when promotion is needed
//   - Validating that Local volume access requirements can be satisfied
//
// # Watched Resources
//
// The controller watches:
//   - ReplicatedVolume: To monitor publishOn requirements
//   - ReplicatedVolumeReplica: To track replica states and roles
//   - ReplicatedStorageClass: To check volumeAccess policy
//
// # Prerequisites
//
// The controller only operates when:
//   - rv.status.conditions[type=Ready].status=True
//
// When RV is being deleted (only module finalizers remain):
//   - All replicas are demoted (primary=false)
//   - No new promotions occur
//
// # Reconciliation Flow
//
//  1. Verify ReplicatedVolume is ready
//  2. Handle deletion case:
//     - If RV has deletionTimestamp and only module finalizers, demote all replicas
//  3. Process each node in rv.spec.publishOn:
//     a. Find or identify replica on that node
//     b. For Local volume access:
//     - Verify replica is Diskful type
//     - Set condition PublishSucceeded=False if not (UnableToProvideLocalVolumeAccess)
//     c. For TieBreaker replicas:
//     - Convert spec.type to Access before promoting
//     d. Set rvr.status.drbd.config.primary=true
//  4. Handle allowTwoPrimaries configuration:
//     - If len(rv.spec.publishOn)==2:
//     * Set rv.status.drbd.config.allowTwoPrimaries=true
//     * Wait for all replicas to report rvr.status.drbd.actual.allowTwoPrimaries=true
//     * Then proceed with promotions
//     - If len(rv.spec.publishOn)<2:
//     * Set rv.status.drbd.config.allowTwoPrimaries=false
//  5. Demote replicas no longer in publishOn:
//     - Set rvr.status.drbd.config.primary=false
//  6. Update rv.status.publishedOn:
//     - List nodes where rvr.status.drbd.status.role==Primary
//
// # Status Updates
//
// The controller maintains:
//   - rvr.status.drbd.config.primary - Desired Primary role for each replica
//   - rv.status.drbd.config.allowTwoPrimaries - Allow multiple Primary replicas (for migration)
//   - rv.status.publishedOn - Nodes where replicas are actually Primary
//   - rv.status.conditions[type=PublishSucceeded] - Publication success/failure status
//
// # Special Notes
//
// Local Volume Access:
//   - When rsc.spec.volumeAccess==Local, only Diskful replicas can be promoted
//   - If no Diskful replica exists on the requested node, publication fails
//
// Two Primaries Support:
//   - Required for live migration of VMs between nodes
//   - DRBD must be configured (allowTwoPrimaries) before promoting the second replica
//   - Configuration must be applied (actual.allowTwoPrimaries) before promotion
//
// TieBreaker Conversion:
//   - TieBreaker replicas cannot be Primary
//   - Automatically converted to Access type when promotion is required
package rvpublishcontroller
