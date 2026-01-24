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

// Package rvattachcontroller implements the rv-attach-controller.
//
// The controller reconciles desired/actual attachment state for a ReplicatedVolume (RV)
// using ReplicatedVolumeAttachment (RVA) objects as the user-facing "intent", and
// ReplicatedVolumeReplica (RVR) objects as the per-node DRBD execution target.
//
// # Main responsibilities
//
//   - Derive rv.status.desiredAttachTo from the active RVA set (FIFO, unique nodes, max 2),
//     while also using the existing rv.status.desiredAttachTo as a preference/"memory".
//   - Compute rv.status.actuallyAttachedTo from replicas whose rvr.status.drbd.status.role=="Primary".
//   - Drive replica role changes by patching rvr.status.drbd.config.primary (promotion/demotion).
//   - Manage rv.status.drbd.config.allowTwoPrimaries for 2-node attachment (live migration),
//     and wait until rvr.status.drbd.actual.allowTwoPrimaries is applied before requesting
//     the second Primary.
//   - Maintain RVA status (phase + Ready condition) as the externally observable attach progress/result.
//   - Convert TieBreaker replicas to Access replicas when attachment requires promotion.
//
// # Watched resources (conceptually)
//
//   - ReplicatedVolume (RV)
//   - ReplicatedVolumeAttachment (RVA)
//   - ReplicatedVolumeReplica (RVR)
//   - ReplicatedStorageClass (RSC)
//
// # Attach enablement / detach-only mode
//
// The controller may run in "detach-only" mode where it does not add new nodes into
// desiredAttachTo (but still performs demotions and keeps RVA status/finalizers consistent).
//
// Attaching is enabled only when:
//   - RV exists and is not deleting
//   - RV has the module controller finalizer
//   - rv.status is initialized and rv.status.conditions[type=RVIOReady] is True
//   - referenced RSC is available
//
// # desiredAttachTo derivation
//
// High-level rules:
//   - Start from current rv.status.desiredAttachTo (may be empty/nil).
//   - Drop nodes that no longer have an active (non-deleting) RVA.
//   - If attaching is enabled, fill remaining slots from the active RVA set (FIFO) up to 2 nodes.
//   - For Local access, only *new* attachments are allowed on nodes with a Diskful replica,
//     confirmed by rvr.status.actualType==Diskful (agent must initialize status first).
//   - New attachments are not allowed on nodes whose replica is marked for deletion.
//
// # RVA status model
//
// The controller sets RVA.Status.Phase and a Ready condition (type=Ready) with a reason:
//   - Attached (Ready=True, Reason=Attached) when the node is in actuallyAttachedTo.
//   - Detaching (Ready=True, Reason=Attached) when RVA is deleting but the node is still attached.
//   - Pending (Ready=False) when attachment cannot progress:
//     WaitingForReplicatedVolume, WaitingForReplicatedVolumeReady, WaitingForActiveAttachmentsToDetach,
//     LocalityNotSatisfied.
//   - Attaching (Ready=False) while progressing:
//     WaitingForReplica, ConvertingTieBreakerToAccess, SettingPrimary.
//
// # Notes
//
// Local volume access:
//   - Locality constraints are reported via RVA status.
//   - Existing desired nodes may be kept even if Locality becomes violated later.
package rvattachcontroller
