/*
Copyright 2026 Flant JSC

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

// IMPORTANT: PlanID versioning
//
// PlanIDs are persisted in rv.Status.DatameshTransitions. Changing a plan
// in a way that breaks in-flight transitions requires a NEW version:
//   - Step composition changed (added, removed, reordered)
//   - Step apply semantics changed (different mutations)
//
// Safe changes (no new version needed):
//   - Guards, confirm, DisplayName, diagnostics, OnComplete

package datamesh

import (
	"fmt"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/dmte"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/idset"
)

// ──────────────────────────────────────────────────────────────────────────────
// Plan registration
//

// registerAttachmentPlans registers Attach, Detach, EnableMultiattach, and
// DisableMultiattach transition plans in the registry.
func registerAttachmentPlans(reg *dmte.Registry[*globalContext, *ReplicaContext]) {
	attach := reg.ReplicaTransition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach, attachmentSlot)
	detach := reg.ReplicaTransition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach, attachmentSlot)
	enableMultiattach := reg.GlobalTransition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeEnableMultiattach)
	disableMultiattach := reg.GlobalTransition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeDisableMultiattach)

	// Attach: set member.Attached = true, wait for subject to confirm.
	attach.Plan("attach/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment).
		DisplayName("Attaching volume").
		Guards(
			guardAttachNotDeleting,
			guardQuorumSatisfied,
			guardSlotAvailable,
			guardVolumeAccessLocalForAttach,
			guardMemberExists,
			guardNodeOperational,
			guardRVRReady,
			guardNoActiveMembershipTransition,
		).
		Steps(
			dmte.ReplicaStep("Attach", applyAttach, confirmSubjectOnly).
				Details(v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttaching).
				DiagnosticConditions(v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType),
		).
		Build()

	// Detach: set member.Attached = false, wait for subject to confirm (or leave).
	detach.Plan("detach/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment).
		DisplayName("Detaching volume").
		Guards(
			guardDeviceNotInUse,
		).
		Steps(
			dmte.ReplicaStep("Detach", applyDetach, confirmSubjectOnlyLeavingOrGone).
				Details(v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonDetaching).
				DiagnosticConditions(v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType),
		).
		Build()

	// EnableMultiattach: set datamesh.Multiattach = true, wait for all relevant members.
	enableMultiattach.Plan("enable-multiattach/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupMultiattach).
		DisplayName("Enabling multiattach").
		Guards(guardMaxAttachmentsAllowsMultiattach).
		Steps(
			dmte.GlobalStep("Enable multiattach", applyEnableMultiattach, confirmMultiattach).
				DiagnosticConditions(v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType),
		).
		Build()

	// DisableMultiattach: set datamesh.Multiattach = false, wait for all relevant members.
	disableMultiattach.Plan("disable-multiattach/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupMultiattach).
		DisplayName("Disabling multiattach").
		Guards(guardCanDisableMultiattach).
		Steps(
			dmte.GlobalStep("Disable multiattach", applyDisableMultiattach, confirmMultiattach).
				DiagnosticConditions(v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType),
		).
		Build()
}

// ──────────────────────────────────────────────────────────────────────────────
// Guards
//

// guardAttachNotDeleting blocks attachment transitions if the ReplicatedVolume is being deleted.
func guardAttachNotDeleting(gctx *globalContext, _ *ReplicaContext) dmte.GuardResult {
	if gctx.deletionTimestamp != nil {
		return dmte.GuardResult{
			Blocked: true,
			Message: "Volume is being deleted",
			Details: v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonReplicatedVolumeDeleting,
		}
	}
	return dmte.GuardResult{}
}

// guardSlotAvailable blocks if all attachment slots are occupied.
// Uses potentiallyAttached on globalContext for accurate counting
// (includes detaching nodes that may still be Primary).
func guardSlotAvailable(gctx *globalContext, _ *ReplicaContext) dmte.GuardResult {
	occupied := gctx.potentiallyAttached.Len()
	if occupied >= int(gctx.maxAttachments) {
		return dmte.GuardResult{
			Blocked: true,
			Message: fmt.Sprintf("Waiting for attachment slot (%d/%d occupied)", occupied, gctx.maxAttachments),
			Details: v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonPending,
		}
	}
	return dmte.GuardResult{}
}

// guardMaxAttachmentsAllowsMultiattach blocks EnableMultiattach if maxAttachments <= 1
// (multiattach is not needed when at most one node can be attached).
func guardMaxAttachmentsAllowsMultiattach(gctx *globalContext) dmte.GuardResult {
	if gctx.maxAttachments <= 1 {
		return dmte.GuardResult{
			Blocked: true,
			Message: "Multiattach not needed: maxAttachments is 1",
		}
	}
	return dmte.GuardResult{}
}

// guardCanDisableMultiattach blocks DisableMultiattach if more than one node
// is potentially attached (still possibly Primary).
func guardCanDisableMultiattach(gctx *globalContext) dmte.GuardResult {
	if gctx.potentiallyAttached.Len() > 1 {
		return dmte.GuardResult{
			Blocked: true,
			Message: fmt.Sprintf("Cannot disable multiattach: %d nodes potentially attached", gctx.potentiallyAttached.Len()),
		}
	}
	return dmte.GuardResult{}
}

// guardQuorumSatisfied blocks if no voter member has quorum.
// Uses a lazy-computed cached value on globalContext.
func guardQuorumSatisfied(gctx *globalContext, _ *ReplicaContext) dmte.GuardResult {
	if !gctx.isQuorumSatisfied() {
		return dmte.GuardResult{
			Blocked: true,
			Message: "Quorum not satisfied: " + gctx.quorumDiagnostic,
			Details: v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonPending,
		}
	}
	return dmte.GuardResult{}
}

// guardNodeOperational blocks if the replica's node is not eligible
// (not in RSP, node not ready, or agent not ready).
// Uses lazy-cached eligibleNode on ReplicaContext.
func guardNodeOperational(_ *globalContext, rctx *ReplicaContext) dmte.GuardResult {
	if rctx.gctx.rsp == nil {
		return dmte.GuardResult{
			Blocked: true,
			Message: "Waiting for ReplicatedStoragePool to be available",
			Details: v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonPending,
		}
	}
	en := rctx.getEligibleNode()
	if en == nil {
		if rctx.gctx.replicatedStorageClassName != "" {
			return dmte.GuardResult{
				Blocked: true,
				Message: fmt.Sprintf("Node %s is not eligible for storage class %s (pool %s)",
					rctx.nodeName, rctx.gctx.replicatedStorageClassName, rctx.gctx.configuration.ReplicatedStoragePoolName),
				Details: v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonNodeNotEligible,
			}
		}
		return dmte.GuardResult{
			Blocked: true,
			Message: fmt.Sprintf("Node %s is not eligible for pool %s",
				rctx.nodeName, rctx.gctx.configuration.ReplicatedStoragePoolName),
			Details: v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonNodeNotEligible,
		}
	}
	if !en.NodeReady {
		return dmte.GuardResult{
			Blocked: true,
			Message: "Node is not ready",
			Details: v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonPending,
		}
	}
	if !en.AgentReady {
		return dmte.GuardResult{
			Blocked: true,
			Message: "Agent is not ready on node",
			Details: v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonPending,
		}
	}
	return dmte.GuardResult{}
}

// guardRVRReady blocks if the replica's RVR does not exist or is not Ready.
func guardRVRReady(_ *globalContext, rctx *ReplicaContext) dmte.GuardResult {
	if rctx.rvr == nil {
		return dmte.GuardResult{
			Blocked: true,
			Message: "Waiting for replica",
			Details: v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonWaitingForReplica,
		}
	}
	if !obju.StatusCondition(rctx.rvr, v1alpha1.ReplicatedVolumeReplicaCondReadyType).IsTrue().Eval() {
		cond := obju.GetStatusCondition(rctx.rvr, v1alpha1.ReplicatedVolumeReplicaCondReadyType)
		msg := "Waiting for replica to become Ready"
		if cond != nil && cond.Message != "" {
			msg += ": " + cond.Reason + " — " + cond.Message
		}
		return dmte.GuardResult{
			Blocked: true,
			Message: msg,
			Details: v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonWaitingForReplica,
		}
	}
	return dmte.GuardResult{}
}

// guardVolumeAccessLocalForAttach blocks if VolumeAccess is Local and no Diskful
// replica exists (or will exist) on this node. Handles both cases:
//   - Member exists but is not Diskful (e.g., Access) → blocked.
//   - No member, but Diskful RVR exists → pass (member will appear, guardMemberExists handles waiting).
//   - No member, no Diskful RVR → blocked (permanently impossible).
func guardVolumeAccessLocalForAttach(gctx *globalContext, rctx *ReplicaContext) dmte.GuardResult {
	if gctx.configuration.VolumeAccess != v1alpha1.VolumeAccessLocal {
		return dmte.GuardResult{}
	}
	// Diskful member → can attach locally.
	if rctx.member != nil && rctx.member.Type.HasBackingVolume() {
		return dmte.GuardResult{}
	}
	// No member yet, but Diskful RVR exists → will become Diskful member.
	if rctx.member == nil && rctx.rvr != nil && rctx.rvr.Spec.Type == v1alpha1.ReplicaTypeDiskful {
		return dmte.GuardResult{}
	}
	// No Diskful on this node.
	if gctx.replicatedStorageClassName != "" {
		return dmte.GuardResult{
			Blocked: true,
			Message: fmt.Sprintf("No Diskful replica on this node (volumeAccess is Local for storage class %s)",
				gctx.replicatedStorageClassName),
			Details: v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonVolumeAccessLocalityNotSatisfied,
		}
	}
	return dmte.GuardResult{
		Blocked: true,
		Message: "No Diskful replica on this node (volumeAccess is Local)",
		Details: v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonVolumeAccessLocalityNotSatisfied,
	}
}

// guardMemberExists blocks if the replica has no datamesh member.
// Produces detailed diagnostic messages about why the member is not yet available.
func guardMemberExists(_ *globalContext, rctx *ReplicaContext) dmte.GuardResult {
	if rctx.member != nil {
		return dmte.GuardResult{}
	}
	reason := v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonWaitingForReplica

	if rctx.rvr == nil {
		return dmte.GuardResult{Blocked: true, Message: "Waiting for replica on node", Details: reason}
	}

	// RVR exists — check membership request progress.
	if rctx.membershipRequest != nil &&
		rctx.membershipRequest.Request.Operation == v1alpha1.DatameshMembershipRequestOperationJoin {
		return dmte.GuardResult{Blocked: true, Message: fmt.Sprintf(
			"Waiting for replica [#%d] to join datamesh: %s",
			rctx.id, rctx.membershipRequest.Message), Details: reason}
	}

	// Check Ready condition for diagnostic info.
	readyCond := obju.GetStatusCondition(rctx.rvr, v1alpha1.ReplicatedVolumeReplicaCondReadyType)
	if readyCond != nil && readyCond.Message != "" {
		return dmte.GuardResult{Blocked: true, Message: fmt.Sprintf(
			"Waiting for replica [#%d] to join datamesh: %s — %s",
			rctx.id, readyCond.Reason, readyCond.Message), Details: reason}
	}

	return dmte.GuardResult{Blocked: true, Message: fmt.Sprintf(
		"Waiting for replica [#%d] to join datamesh", rctx.id), Details: reason}
}

// guardNoActiveMembershipTransition blocks if the replica has an active
// membership transition (e.g., AddAccess in progress — must complete first).
func guardNoActiveMembershipTransition(_ *globalContext, rctx *ReplicaContext) dmte.GuardResult {
	if rctx.membershipTransition != nil {
		return dmte.GuardResult{
			Blocked: true,
			Message: "Waiting for membership transition to complete",
			Details: v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonWaitingForReplica,
		}
	}
	return dmte.GuardResult{}
}

// guardDeviceNotInUse blocks detach if the replica's block device is currently in use.
func guardDeviceNotInUse(_ *globalContext, rctx *ReplicaContext) dmte.GuardResult {
	if rctx.rvr != nil && rctx.rvr.Status.Attachment != nil && rctx.rvr.Status.Attachment.InUse {
		return dmte.GuardResult{
			Blocked: true,
			Message: "Device in use, detach blocked",
			Details: v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonDetaching,
		}
	}
	return dmte.GuardResult{}
}

// ──────────────────────────────────────────────────────────────────────────────
// Apply callbacks
//

// applyAttach sets the member as attached.
// The engine bumps DatameshRevision after this callback.
func applyAttach(_ *globalContext, rctx *ReplicaContext) {
	rctx.member.Attached = true
}

// applyDetach sets the member as not attached.
// The engine bumps DatameshRevision after this callback.
func applyDetach(_ *globalContext, rctx *ReplicaContext) {
	rctx.member.Attached = false
}

// applyEnableMultiattach enables multiattach on the datamesh.
// The engine bumps DatameshRevision after this callback.
func applyEnableMultiattach(gctx *globalContext) {
	gctx.datamesh.Multiattach = true
}

// applyDisableMultiattach disables multiattach on the datamesh.
// The engine bumps DatameshRevision after this callback.
func applyDisableMultiattach(gctx *globalContext) {
	gctx.datamesh.Multiattach = false
}

// ──────────────────────────────────────────────────────────────────────────────
// Confirm callbacks
//

// confirmMultiattach checks confirmation for EnableMultiattach/DisableMultiattach steps.
// MustConfirm = all members with HasBackingVolume() OR Attached.
// Confirmed = RVRs with DatameshRevision >= stepRevision, intersected with MustConfirm.
//
// Shared between Enable and Disable — the wait set is the same: all members that
// have a backing volume (D, sD) or are attached must apply the new multiattach setting.
func confirmMultiattach(gctx *globalContext, stepRevision int64) dmte.ConfirmResult {
	var mustConfirm idset.IDSet
	for i := range gctx.allReplicas {
		rc := &gctx.allReplicas[i]
		if rc.member != nil && (rc.member.Type.HasBackingVolume() || rc.member.Attached) {
			mustConfirm.Add(rc.id)
		}
	}

	confirmed := confirmedReplicas(gctx, stepRevision).Intersect(mustConfirm)

	return dmte.ConfirmResult{MustConfirm: mustConfirm, Confirmed: confirmed}
}
