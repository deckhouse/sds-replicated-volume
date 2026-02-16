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

package rvcontroller

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/idset"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

// ──────────────────────────────────────────────────────────────────────────────
// Types
//

// attachmentsSummary is the pre-indexed summary of all datamesh attachment operations.
// Built and populated by ensureDatameshAttachments.
// Returned to downstream consumers (e.g., RVA condition reconciliation).
type attachmentsSummary struct {
	// attachmentStates sorted by NodeName. Contains an attachment state for every node
	// that has a datamesh member or at least one RVA.
	// Binary search via findAttachmentStateByNodeName().
	attachmentStates []attachmentState

	// attachmentStateByReplicaID maps memberID → *attachmentState for O(1) lookups.
	// Filled during buildAttachmentsSummary. Entry is nil if no attachment state for that member ID.
	attachmentStateByReplicaID [32]*attachmentState

	// attachBlocked: all attach operations globally blocked.
	attachBlocked                 bool
	attachBlockedConditionMessage string
	attachBlockedConditionReason  string

	// Global transition flags.
	hasActiveEnableMultiattachTransition  bool
	hasActiveDisableMultiattachTransition bool

	// intendedAttachments is the set of member IDs with intent=Attach.
	// May exceed rv.Spec.MaxAttachments if already-attached nodes are over limit.
	intendedAttachments idset.IDSet

	// potentiallyAttached is the set of member IDs that are or may still be attached.
	// Includes three cases:
	//   1. Attached == true, no active Attach transition — fully attached (confirmed).
	//   2. Attached == true, hasActiveAttachTransition — attaching (not yet confirmed by replica).
	//   3. Attached == false, hasActiveDetachTransition — detaching (not yet confirmed by replica,
	//      node may still be Primary).
	// Mutable: Add on Attach creation. NOT removed on Detach creation
	// (member stays "potentially attached" until Detach is confirmed).
	potentiallyAttached idset.IDSet
}

// findAttachmentStateByNodeName returns a pointer to the attachment state for the given node, or nil.
// Uses binary search on the sorted attachmentStates slice.
func (s *attachmentsSummary) findAttachmentStateByNodeName(nodeName string) *attachmentState {
	idx, found := slices.BinarySearchFunc(s.attachmentStates, nodeName, func(as attachmentState, target string) int {
		return cmp.Compare(as.nodeName, target)
	})
	if !found {
		return nil
	}
	return &s.attachmentStates[idx]
}

// attachmentIntent indicates the intended attachment operation for a node.
type attachmentIntent string

const (
	attachmentIntentAttach  attachmentIntent = "Attach"  // should be attached
	attachmentIntentPending attachmentIntent = "Pending" // active RVA exists, attach not yet possible
	attachmentIntentDetach  attachmentIntent = "Detach"  // should be detached
	// "" = no intent (node in list for indexing only, not processed)
)

// attachmentState contains all pre-indexed data for one node's attachment.
// No additional lookups needed during guard checks.
type attachmentState struct {
	nodeName string

	// intent indicates the intended attachment operation for this node.
	intent attachmentIntent

	// rvas contains all RVAs (active and deleting) for this node.
	rvas []*v1alpha1.ReplicatedVolumeAttachment

	// Pointer into rv.Status.Datamesh.Members. nil = no member on this node.
	// Mutations (member.Attached) go through this pointer.
	member *v1alpha1.ReplicatedVolumeDatameshMember

	// Pointer into rvrs slice. nil = no RVR found for this node's member.
	// Read-only — any RVR field available without extra lookups.
	rvr *v1alpha1.ReplicatedVolumeReplica

	// Per-node transition flags.
	hasActiveAttachTransition    bool
	hasActiveDetachTransition    bool
	hasActiveAddAccessTransition bool

	// conditionMessage is the human-readable message for the RVA Attached condition.
	// conditionReason is the machine-readable reason (maps to ReplicatedVolumeAttachmentCondAttachedReason* constants).
	// Both are always set together by ensureDatameshAttachments for every node that has RVAs.
	conditionMessage string
	conditionReason  string
}

// hasActiveRVA returns true if this node has at least one non-deleting RVA.
func (as *attachmentState) hasActiveRVA() bool {
	for _, rva := range as.rvas {
		if rva.DeletionTimestamp == nil {
			return true
		}
	}
	return false
}

// earliestActiveRVATimestamp returns the CreationTimestamp of the earliest active (non-deleting) RVA.
// Returns zero time if no active RVA exists.
// Relies on rvas being sorted by CreationTimestamp within each node (guaranteed by getRVAs sort order).
func (as *attachmentState) earliestActiveRVATimestamp() metav1.Time {
	for _, rva := range as.rvas {
		if rva.DeletionTimestamp == nil {
			return rva.CreationTimestamp
		}
	}
	return metav1.Time{}
}

// ──────────────────────────────────────────────────────────────────────────────
// buildAttachmentsSummary
//

// buildAttachmentsSummary builds the pre-indexed attachments summary from rv, rvrs, and rvas.
// Populates attachmentStates (sorted by NodeName) with member/rvr/rvas pointers and the
// attachmentStateByReplicaID index. Does NOT fill intent, transition flags, or counts.
//
// Uses sorted merge of members and rvas (both sorted by NodeName) to avoid intermediate maps.
// rvas MUST be sorted by NodeName (primary), CreationTimestamp (secondary) — as returned by getRVAsSorted.
func buildAttachmentsSummary(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	rvas []*v1alpha1.ReplicatedVolumeAttachment,
) *attachmentsSummary {
	// Sort members by NodeName for merge. Stack-allocated array avoids heap allocation.
	var sortedMembers [32]*v1alpha1.ReplicatedVolumeDatameshMember
	numMembers := min(len(rv.Status.Datamesh.Members), 32)
	for i := range numMembers {
		sortedMembers[i] = &rv.Status.Datamesh.Members[i]
	}
	slices.SortFunc(sortedMembers[:numMembers], func(a, b *v1alpha1.ReplicatedVolumeDatameshMember) int {
		return cmp.Compare(a.NodeName, b.NodeName)
	})

	// Sorted merge: members (sorted by NodeName) + rvas (sorted by NodeName from getRVAsSorted).
	// Produces attachmentStates sorted by NodeName with zero intermediate maps.
	// Capacity is an upper bound — actual count may be smaller when members and RVAs share nodes.
	s := &attachmentsSummary{
		attachmentStates: make([]attachmentState, 0, numMembers+len(rvas)),
	}
	memberIdx, rvaIdx := 0, 0
	for memberIdx < numMembers || rvaIdx < len(rvas) {
		// Determine the current node name (smallest of the two heads).
		var nodeName string
		switch {
		case memberIdx >= numMembers:
			nodeName = rvas[rvaIdx].Spec.NodeName
		case rvaIdx >= len(rvas):
			nodeName = sortedMembers[memberIdx].NodeName
		default:
			mNN := sortedMembers[memberIdx].NodeName
			rvaNN := rvas[rvaIdx].Spec.NodeName
			if mNN <= rvaNN {
				nodeName = mNN
			} else {
				nodeName = rvaNN
			}
		}

		// Append new attachmentState for this node.
		s.attachmentStates = append(s.attachmentStates, attachmentState{nodeName: nodeName})
		as := &s.attachmentStates[len(s.attachmentStates)-1]

		// Consume member if it matches this node.
		if memberIdx < numMembers && sortedMembers[memberIdx].NodeName == nodeName {
			as.member = sortedMembers[memberIdx]
			as.rvr = findRVRByID(rvrs, as.member.ID())
			s.attachmentStateByReplicaID[as.member.ID()] = as
			memberIdx++
		} else {
			// No member on this node — find RVR by NodeName (linear search).
			for _, rvr := range rvrs {
				if rvr.Spec.NodeName == nodeName {
					as.rvr = rvr
					break
				}
			}
		}

		// Consume consecutive RVAs for this node (sub-slice of input, zero alloc).
		rvaStart := rvaIdx
		for rvaIdx < len(rvas) && rvas[rvaIdx].Spec.NodeName == nodeName {
			rvaIdx++
		}
		if rvaIdx > rvaStart {
			as.rvas = rvas[rvaStart:rvaIdx]
		}
	}

	return s
}

// ──────────────────────────────────────────────────────────────────────────────
// computeDatameshAttachBlocked
//

// computeDatameshAttachBlocked determines if attach is globally blocked and sets
// atts.attachBlocked, atts.attachBlockedConditionReason, and atts.attachBlockedConditionMessage accordingly.
func computeDatameshAttachBlocked(
	atts *attachmentsSummary,
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
) {
	if rv.DeletionTimestamp != nil {
		atts.attachBlocked = true
		atts.attachBlockedConditionReason = v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonReplicatedVolumeDeleting
		atts.attachBlockedConditionMessage = "Volume is being deleted"
		return
	}

	if quorumOK, diagnostic := computeActualQuorum(rv, rvrs); !quorumOK {
		atts.attachBlocked = true
		atts.attachBlockedConditionReason = v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonPending
		atts.attachBlockedConditionMessage = "Quorum not satisfied: " + diagnostic
		return
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// computeDatameshAttachmentIntents
//

// computeDatameshAttachmentIntents computes the intent for each node in the attachments summary.
// Must be called AFTER computeDatameshPotentiallyAttached (needs atts.potentiallyAttached).
//
// Algorithm:
//  1. PotentiallyAttached nodes with active RVA → intent=Attach (keep their slot).
//     PotentiallyAttached nodes without active RVA → intent=Detach.
//  2. If attach globally blocked → all remaining active-RVA nodes → Pending (early return).
//  3. If no slots available → all remaining active-RVA nodes → Pending (early return).
//  4. Remaining active-RVA nodes are checked for eligibility (RSP) and member presence.
//     Ineligible or memberless nodes → Pending with specific message.
//  5. Eligible candidates compete for available slots in FIFO order → Attach or Pending.
//
// Already-attached nodes NEVER lose their slot when maxAttachments decreases.
func computeDatameshAttachmentIntents(atts *attachmentsSummary, rv *v1alpha1.ReplicatedVolume, rsp *rspView) {
	maxAttachments := rv.Spec.MaxAttachments

	// Step 1: assign Attach/Detach for potentiallyAttached nodes (pre-computed).
	// potentiallyAttached nodes ALWAYS occupy a slot — even detaching ones,
	// because they may still be Primary until detach is confirmed by the replica.
	occupiedSlots := atts.potentiallyAttached.Len()
	for i := range atts.attachmentStates {
		as := &atts.attachmentStates[i]
		if as.member == nil || !atts.potentiallyAttached.Contains(as.member.ID()) {
			continue
		}
		if as.hasActiveRVA() {
			as.intent = attachmentIntentAttach
			atts.intendedAttachments.Add(as.member.ID())
		} else {
			as.intent = attachmentIntentDetach
		}
	}

	// If attach is globally blocked — unassigned active-RVA nodes are Pending;
	// nodes with only deleting RVAs are Detached.
	if atts.attachBlocked {
		for i := range atts.attachmentStates {
			as := &atts.attachmentStates[i]
			if as.intent != "" {
				continue
			}
			if as.hasActiveRVA() {
				as.intent = attachmentIntentPending
				as.conditionReason = atts.attachBlockedConditionReason
				as.conditionMessage = atts.attachBlockedConditionMessage
			} else if len(as.rvas) > 0 {
				// Only deleting RVAs remain — node is already detached or was never attached.
				as.intent = attachmentIntentDetach
				as.conditionReason = v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonDetached
				as.conditionMessage = "Volume has been detached from the node"
			}
		}
		return
	}

	// Step 2: assign slots to unassigned active-RVA nodes.
	// Only nodes with active RVA + eligible node + datamesh member compete for slots.
	// Others are queued immediately with a specific message explaining the blocker.

	// occupiedSlots may exceed maxAttachments if rv.Spec.MaxAttachments was decreased
	// while nodes were already attached. In that case availableNewSlots is 0.
	availableNewSlots := max(0, int(maxAttachments)-occupiedSlots)

	// No slots available — all unassigned active-RVA nodes are Pending.
	if availableNewSlots == 0 {
		for i := range atts.attachmentStates {
			as := &atts.attachmentStates[i]
			if as.intent == "" && as.hasActiveRVA() {
				as.intent = attachmentIntentPending
				as.conditionReason = v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonPending
				as.conditionMessage = fmt.Sprintf("Waiting for attachment slot (slots occupied %d/%d)", occupiedSlots, maxAttachments)
			}
		}
		return
	}

	if rsp == nil {
		panic("computeDatameshAttachmentIntents: rsp must not be nil")
	}

	// Collect candidates (eligible + has member). Others are queued with specific messages.
	var candidates []*attachmentState
	for i := range atts.attachmentStates {
		as := &atts.attachmentStates[i]
		if as.intent != "" {
			continue // already assigned in step 1
		}
		if !as.hasActiveRVA() {
			continue // no active RVA — no intent
		}

		// Check node eligibility via RSP.
		en := rsp.FindEligibleNode(as.nodeName)
		switch {
		case en == nil:
			as.intent = attachmentIntentPending
			as.conditionReason = v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonNodeNotEligible
			as.conditionMessage = fmt.Sprintf("Node is not eligible for storage class %s (pool %s)",
				rv.Spec.ReplicatedStorageClassName, rv.Status.Configuration.StoragePoolName)
			continue
		case !en.NodeReady:
			as.intent = attachmentIntentPending
			as.conditionReason = v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonPending
			as.conditionMessage = "Node is not ready"
			continue
		case !en.AgentReady:
			as.intent = attachmentIntentPending
			as.conditionReason = v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonPending
			as.conditionMessage = "Agent is not ready on node"
			continue
		}

		if as.member == nil {
			// Active RVA but no datamesh member — can't attach yet.
			as.intent = attachmentIntentPending

			// VolumeAccess=Local: no Access replica will be created on this node,
			// so if there is no Diskful RVR, attachment is permanently impossible.
			if rv.Status.Configuration.VolumeAccess == v1alpha1.VolumeAccessLocal &&
				(as.rvr == nil || as.rvr.Spec.Type != v1alpha1.ReplicaTypeDiskful) {
				as.conditionReason = v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonVolumeAccessLocalityNotSatisfied
				as.conditionMessage = fmt.Sprintf(
					"No Diskful replica on this node (volumeAccess is Local for storage class %s)",
					rv.Spec.ReplicatedStorageClassName)
				continue
			}

			as.conditionReason = v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonWaitingForReplica
			if as.rvr == nil {
				as.conditionMessage = "Waiting for replica on node"
			} else {
				rvrID := as.rvr.ID()
				prtIdx := slices.IndexFunc(rv.Status.DatameshPendingReplicaTransitions, func(prt v1alpha1.ReplicatedVolumeDatameshPendingReplicaTransition) bool {
					return prt.Name == as.rvr.Name && prt.Transition.Member != nil && *prt.Transition.Member
				})
				readyCond := obju.GetStatusCondition(as.rvr, v1alpha1.ReplicatedVolumeReplicaCondReadyType)
				switch {
				case prtIdx >= 0:
					as.conditionMessage = fmt.Sprintf("Waiting for replica [#%d] to join datamesh: %s",
						rvrID, rv.Status.DatameshPendingReplicaTransitions[prtIdx].Message)
				case readyCond != nil && readyCond.Message != "":
					as.conditionMessage = fmt.Sprintf("Waiting for replica [#%d] to join datamesh: %s — %s",
						rvrID, readyCond.Reason, readyCond.Message)
				default:
					as.conditionMessage = fmt.Sprintf("Waiting for replica [#%d] to join datamesh", rvrID)
				}
			}
			continue
		}
		candidates = append(candidates, as)
	}

	// Sort candidates FIFO: earliest active RVA timestamp, then NodeName tie-breaker.
	slices.SortFunc(candidates, func(a, b *attachmentState) int {
		if c := a.earliestActiveRVATimestamp().Time.Compare(b.earliestActiveRVATimestamp().Time); c != 0 {
			return c
		}
		return cmp.Compare(a.nodeName, b.nodeName)
	})

	// Assign: first availableNewSlots → Attach, rest → Pending.
	for j, as := range candidates {
		if j < availableNewSlots {
			as.intent = attachmentIntentAttach
			atts.intendedAttachments.Add(as.member.ID())
		} else {
			as.intent = attachmentIntentPending
			as.conditionReason = v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonPending
			as.conditionMessage = fmt.Sprintf("Waiting for attachment slot (slots occupied %d/%d)", occupiedSlots, maxAttachments)
		}
	}

	// Nodes with only deleting RVAs (no active RVAs) that were never attached
	// or already fully detached — mark as Detached.
	for i := range atts.attachmentStates {
		as := &atts.attachmentStates[i]
		if as.intent == "" && len(as.rvas) > 0 {
			as.intent = attachmentIntentDetach
			as.conditionReason = v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonDetached
			as.conditionMessage = "Volume has been detached from the node"
		}
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// ensureDatameshAttachments
//

// ensureDatameshAttachments coordinates datamesh attach/detach transitions.
// Writes the fully populated attachmentsSummary to outAtts for downstream consumers
// (e.g., RVA condition reconciliation).
//
// Exception: sub-functions use metav1.Now() for StartedAt when creating new transitions.
// This is controller-owned state (persisted decision timestamp), acceptable here
// because the value is set once and stabilized across subsequent reconciliations.
func ensureDatameshAttachments(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	rvas []*v1alpha1.ReplicatedVolumeAttachment,
	rsp *rspView,
	outAtts **attachmentsSummary,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "datamesh-attachments")
	defer ef.OnEnd(&outcome)

	// Build per-node attachment states (members, rvrs, rvas — no intent yet).
	atts := buildAttachmentsSummary(rv, rvrs, rvas)
	*outAtts = atts

	// Determine if attach is globally blocked.
	computeDatameshAttachBlocked(atts, rv, rvrs)

	// Complete Attach/Detach transitions; fill per-node flags (including AddAccess index).
	changed := ensureDatameshAttachDetachTransitionProgress(rv, rvrs, atts)

	// Identify potentially-attached nodes (needs hasActiveDetachTransition from above).
	computeDatameshPotentiallyAttached(atts)

	// Complete Enable/Disable Multiattach transitions (needs potentiallyAttached).
	changed = ensureDatameshMultiattachTransitionProgress(rv, rvrs, atts) || changed

	// Compute intent for each node (slot priority to already-attached, FIFO for new).
	computeDatameshAttachmentIntents(atts, rv, rsp)

	// Enable/disable multiattach based on actual intent count.
	multiattachChanged := ensureDatameshMultiattachToggle(rv, atts)
	if multiattachChanged {
		// Fill progress message on the newly created transition.
		ensureDatameshMultiattachTransitionProgress(rv, rvrs, atts)
	}
	changed = multiattachChanged || changed

	// Create Detach transitions where needed.
	changed = ensureDatameshDetachTransitions(rv, rvrs, atts) || changed

	// Create Attach transitions where needed.
	changed = ensureDatameshAttachTransitions(rv, rvrs, atts) || changed

	return ef.Ok().ReportChangedIf(changed)
}

// ──────────────────────────────────────────────────────────────────────────────
// ensureDatameshAttachDetachTransitionProgress
//

// ensureDatameshAttachDetachTransitionProgress completes finished Attach/Detach transitions
// and fills per-node transition flags (hasActiveAttachTransition, hasActiveDetachTransition,
// hasActiveAddAccessTransition) and global counters on the attachments summary.
// Backward pass over rv.Status.DatameshTransitions.
// Returns true if rv was changed.
func ensureDatameshAttachDetachTransitionProgress(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	atts *attachmentsSummary,
) bool {
	changed := false

	for i := len(rv.Status.DatameshTransitions) - 1; i >= 0; i-- {
		t := &rv.Status.DatameshTransitions[i]

		switch t.Type {
		case v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach,
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach:
			replicaID := t.ReplicaID()
			as := atts.attachmentStateByReplicaID[replicaID]

			// Attach completion: replica confirmed the revision.
			if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach {
				if as != nil && as.rvr != nil && as.rvr.Status.DatameshRevision >= t.DatameshRevision {
					rv.Status.DatameshTransitions = slices.Delete(rv.Status.DatameshTransitions, i, i+1)
					changed = true
					continue
				}
			}

			// Detach completion: replica confirmed the revision, is no longer a datamesh member, or doesn't exist.
			if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach {
				if as == nil || as.rvr == nil || as.rvr.Status.DatameshRevision == 0 || as.rvr.Status.DatameshRevision >= t.DatameshRevision {
					rv.Status.DatameshTransitions = slices.Delete(rv.Status.DatameshTransitions, i, i+1)
					changed = true
					continue
				}
			}

			// Not completed — fill flags (as != nil is defensive: transition should always have a matching node).
			if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach {
				if as != nil {
					as.hasActiveAttachTransition = true
				}
			} else {
				if as != nil {
					as.hasActiveDetachTransition = true
				}
			}

			// Update transition and attachmentState progress messages.
			progressMsg := computeDatameshTransitionProgressMessage(rvrs, t.DatameshRevision, idset.Of(replicaID), idset.IDSet(0), nil,
				v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType)
			changed = applyTransitionMessage(t, progressMsg) || changed

			if as != nil {
				switch t.Type {
				case v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach:
					as.conditionReason = v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttaching
					as.conditionMessage = "Attaching, " + progressMsg
				case v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach:
					as.conditionReason = v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonDetaching
					as.conditionMessage = "Detaching, " + progressMsg
				}
			}

		case v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddAccessReplica:
			// Not completed here (handled by ensureDatameshAccessReplicas), only index flag.
			replicaID := t.ReplicaID()
			if as := atts.attachmentStateByReplicaID[replicaID]; as != nil {
				as.hasActiveAddAccessTransition = true
			}

		default:
			// Other transition types are not handled here.
		}
	}

	return changed
}

// ──────────────────────────────────────────────────────────────────────────────
// computeDatameshPotentiallyAttached
//

// computeDatameshPotentiallyAttached fills atts.potentiallyAttached — the set of member IDs
// that are or may still be attached (member.Attached || hasActiveDetachTransition).
// Must be called AFTER ensureDatameshAttachDetachTransitionProgress (needs hasActiveDetachTransition).
func computeDatameshPotentiallyAttached(atts *attachmentsSummary) {
	for i := range atts.attachmentStates {
		as := &atts.attachmentStates[i]
		if as.member != nil && (as.member.Attached || as.hasActiveDetachTransition) {
			atts.potentiallyAttached.Add(as.member.ID())
		}
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// ensureDatameshMultiattachTransitionProgress
//

// ensureDatameshMultiattachTransitionProgress completes finished EnableMultiattach/DisableMultiattach
// transitions and fills global flags (hasActiveEnableMultiattachTransition,
// hasActiveDisableMultiattachTransition) on the attachments summary.
// Backward pass over rv.Status.DatameshTransitions.
// Must be called AFTER computeDatameshPotentiallyAttached (needs atts.potentiallyAttached for mustConfirm).
// Returns true if rv was changed.
func ensureDatameshMultiattachTransitionProgress(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	atts *attachmentsSummary,
) bool {
	changed := false

	for i := len(rv.Status.DatameshTransitions) - 1; i >= 0; i-- {
		t := &rv.Status.DatameshTransitions[i]

		// Skip non-multiattach transitions (handled by ensureDatameshAttachDetachTransitionProgress).
		if t.Type != v1alpha1.ReplicatedVolumeDatameshTransitionTypeEnableMultiattach &&
			t.Type != v1alpha1.ReplicatedVolumeDatameshTransitionTypeDisableMultiattach {
			continue
		}

		// Must be confirmed by all Diskful members + any potentially-attached member.
		mustConfirm := idset.FromWhere(rv.Status.Datamesh.Members, func(m v1alpha1.ReplicatedVolumeDatameshMember) bool {
			return m.Type == v1alpha1.ReplicaTypeDiskful || m.Attached
		}).Union(atts.potentiallyAttached)

		// Completion: all required members confirmed revision.
		confirmed := idset.FromWhere(rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
			return rvr.Status.DatameshRevision >= t.DatameshRevision
		}).Intersect(mustConfirm)

		if confirmed == mustConfirm {
			rv.Status.DatameshTransitions = slices.Delete(rv.Status.DatameshTransitions, i, i+1)
			changed = true
			continue
		}

		// Not completed — fill flags.
		if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeEnableMultiattach {
			atts.hasActiveEnableMultiattachTransition = true
		} else {
			atts.hasActiveDisableMultiattachTransition = true
		}

		// Update transition progress message.
		changed = applyTransitionMessage(t,
			computeDatameshTransitionProgressMessage(rvrs, t.DatameshRevision, mustConfirm, confirmed, nil,
				v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType),
		) || changed
	}

	return changed
}

// ──────────────────────────────────────────────────────────────────────────────
// ensureDatameshMultiattachToggle
//

// ensureDatameshMultiattachToggle enables or disables multiattach based on the actual intent count.
// Returns true if rv was changed.
func ensureDatameshMultiattachToggle(
	rv *v1alpha1.ReplicatedVolume,
	atts *attachmentsSummary,
) bool {
	changed := false

	// intendedAttachments already respects rv.Spec.MaxAttachments for slot allocation.
	// It may exceed MaxAttachments if the limit was decreased after nodes were already attached —
	// that is a normal transient state (no forced detach).
	needMultiattach := atts.intendedAttachments.Len() > 1

	if needMultiattach && !rv.Status.Datamesh.Multiattach && !atts.hasActiveEnableMultiattachTransition {
		rv.Status.Datamesh.Multiattach = true
		rv.Status.DatameshRevision++
		rv.Status.DatameshTransitions = append(rv.Status.DatameshTransitions,
			v1alpha1.ReplicatedVolumeDatameshTransition{
				Type:             v1alpha1.ReplicatedVolumeDatameshTransitionTypeEnableMultiattach,
				DatameshRevision: rv.Status.DatameshRevision,
				StartedAt:        metav1.Now(),
			},
		)
		atts.hasActiveEnableMultiattachTransition = true
		changed = true
	}

	if !needMultiattach && rv.Status.Datamesh.Multiattach &&
		atts.potentiallyAttached.Len() <= 1 &&
		!atts.hasActiveDisableMultiattachTransition &&
		!atts.hasActiveEnableMultiattachTransition {
		rv.Status.Datamesh.Multiattach = false
		rv.Status.DatameshRevision++
		rv.Status.DatameshTransitions = append(rv.Status.DatameshTransitions,
			v1alpha1.ReplicatedVolumeDatameshTransition{
				Type:             v1alpha1.ReplicatedVolumeDatameshTransitionTypeDisableMultiattach,
				DatameshRevision: rv.Status.DatameshRevision,
				StartedAt:        metav1.Now(),
			},
		)
		atts.hasActiveDisableMultiattachTransition = true
		changed = true
	}

	return changed
}

// ──────────────────────────────────────────────────────────────────────────────
// ensureDatameshDetachTransitions
//

// ensureDatameshDetachTransitions creates Detach transitions for nodes with intent=Detach,
// respecting guard rules. Sets messages on blocked nodes. Returns true if rv was changed.
func ensureDatameshDetachTransitions(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	atts *attachmentsSummary,
) bool {
	changed := false

	for i := range atts.attachmentStates {
		as := &atts.attachmentStates[i]
		if as.intent != attachmentIntentDetach {
			continue
		}

		// Already fully detached (no pending transition) — settled.
		if as.member != nil && !as.member.Attached && !as.hasActiveDetachTransition {
			as.conditionReason = v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonDetached
			as.conditionMessage = "Volume has been detached from the node"
			continue
		}

		// Already has an active Detach transition — skip.
		if as.hasActiveDetachTransition {
			// conditionReason/conditionMessage already set by ensureDatameshAttachDetachTransitionProgress.
			continue
		}

		// Guard: conflict — Attach on the same replica.
		if as.hasActiveAttachTransition {
			as.conditionMessage = "Detach pending, waiting for attach to complete first. " + as.conditionMessage
			// conditionReason stays from the attach transition (Attaching).
			continue
		}

		// Note: we do NOT check RVR existence or Ready condition for detach.
		// Detach proceeds regardless — if the RVR is missing, no one will confirm the revision,
		// but that is specifically handled by ensureDatameshAttachDetachTransitionProgress

		// Guard: not in use.
		if as.rvr != nil && as.rvr.Status.Attachment != nil && as.rvr.Status.Attachment.InUse {
			as.conditionReason = v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonDetaching
			as.conditionMessage = "Device in use, detach blocked"
			continue
		}

		// All guards passed — create Detach transition.
		as.member.Attached = false
		rv.Status.DatameshRevision++
		msg := computeDatameshTransitionProgressMessage(rvrs, rv.Status.DatameshRevision, idset.Of(as.member.ID()), idset.IDSet(0), nil,
			v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType)
		rv.Status.DatameshTransitions = append(rv.Status.DatameshTransitions,
			v1alpha1.ReplicatedVolumeDatameshTransition{
				Type:             v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach,
				DatameshRevision: rv.Status.DatameshRevision,
				ReplicaName:      as.member.Name,
				StartedAt:        metav1.Now(),
				Message:          msg,
			},
		)
		changed = true
		as.hasActiveDetachTransition = true
		as.conditionReason = v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonDetaching
		as.conditionMessage = "Detaching, " + msg
	}

	return changed
}

// ──────────────────────────────────────────────────────────────────────────────
// ensureDatameshAttachTransitions
//

// ensureDatameshAttachTransitions creates Attach transitions for nodes with intent=Attach,
// respecting guard rules and multiattach constraints. Sets messages on blocked nodes.
// Returns true if rv was changed.
func ensureDatameshAttachTransitions(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	atts *attachmentsSummary,
) bool {
	changed := false

	for i := range atts.attachmentStates {
		as := &atts.attachmentStates[i]
		if as.intent != attachmentIntentAttach {
			continue
		}

		// Already fully attached (no pending transition) — settled.
		if as.member != nil && as.member.Attached && !as.hasActiveAttachTransition {
			as.conditionReason = v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttached
			as.conditionMessage = "Volume is attached and ready to serve I/O on the node"
			if rv.DeletionTimestamp != nil {
				as.conditionMessage += " (volume is pending deletion and will be deleted after the last active attachment is removed)"
			}
			continue
		}

		// Already has an active Attach transition — skip.
		if as.hasActiveAttachTransition {
			// conditionReason/conditionMessage already set by ensureDatameshAttachDetachTransitionProgress.
			continue
		}

		// Guard: conflict — Detach on the same replica.
		if as.hasActiveDetachTransition {
			as.conditionReason = v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonPending
			as.conditionMessage = "Attach pending, waiting for detach to complete first. " + as.conditionMessage
			continue
		}

		// Guard: AddAccessReplica in progress for same replica.
		if as.hasActiveAddAccessTransition {
			as.conditionReason = v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonWaitingForReplica
			as.conditionMessage = "Waiting for replica to join datamesh"
			continue
		}

		// Defensive: should not happen — computeDatameshAttachmentIntents sets intent=Attach
		// only for nodes with a datamesh member (potentiallyAttached or candidate).
		if as.member == nil {
			as.conditionReason = v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonWaitingForReplica
			as.conditionMessage = "Waiting for datamesh member"
			continue
		}

		// Defensive: should not happen — RVR is protected by a finalizer while the member
		// is part of datamesh, so it cannot be deleted before RemoveAccessReplica completes.
		if as.rvr == nil {
			as.conditionReason = v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonWaitingForReplica
			as.conditionMessage = "Waiting for replica"
			continue
		}

		// Guard: RVR Ready.
		if !obju.StatusCondition(as.rvr, v1alpha1.ReplicatedVolumeReplicaCondReadyType).IsTrue().Eval() {
			as.conditionReason = v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonWaitingForReplica
			as.conditionMessage = "Waiting for replica to become Ready"
			continue
		}

		// Guard: VolumeAccess=Local requires a Diskful member on this node.
		if rv.Status.Configuration.VolumeAccess == v1alpha1.VolumeAccessLocal &&
			as.member.Type != v1alpha1.ReplicaTypeDiskful {
			as.conditionReason = v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonVolumeAccessLocalityNotSatisfied
			as.conditionMessage = fmt.Sprintf(
				"No Diskful replica on this node (volumeAccess is Local for storage class %s)",
				rv.Spec.ReplicatedStorageClassName)
			continue
		}

		// Multiattach guards.
		if !atts.potentiallyAttached.IsEmpty() {
			if !rv.Status.Datamesh.Multiattach || atts.hasActiveEnableMultiattachTransition {
				as.conditionReason = v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttaching
				as.conditionMessage = "Waiting for multiattach to be enabled"
				for i := range rv.Status.DatameshTransitions {
					if rv.Status.DatameshTransitions[i].Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeEnableMultiattach {
						if m := rv.Status.DatameshTransitions[i].Message; m != "" {
							as.conditionMessage += ". " + m
						}
						break
					}
				}
				break
			}

			// Defensive: unclear when this can happen (EnableMultiattach and DisableMultiattach
			// are mutually exclusive, and we just checked EnableMultiattach above).
			if atts.hasActiveDisableMultiattachTransition {
				as.conditionReason = v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttaching
				as.conditionMessage = "Waiting for multiattach to be enabled, but disable is in progress and must complete first"
				for i := range rv.Status.DatameshTransitions {
					if rv.Status.DatameshTransitions[i].Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeDisableMultiattach {
						if m := rv.Status.DatameshTransitions[i].Message; m != "" {
							as.conditionMessage += ". " + m
						}
						break
					}
				}
				break
			}
		}

		// Defensive: should not happen — when attachBlocked, computeDatameshAttachmentIntents
		// sets intent=Pending for all non-potentiallyAttached nodes, and potentiallyAttached
		// nodes are caught by the settled/transition checks above.
		if atts.attachBlocked {
			as.conditionReason = atts.attachBlockedConditionReason
			as.conditionMessage = atts.attachBlockedConditionMessage
			continue
		}

		// All guards passed — create Attach transition.
		as.member.Attached = true
		rv.Status.DatameshRevision++
		msg := computeDatameshTransitionProgressMessage(rvrs, rv.Status.DatameshRevision, idset.Of(as.member.ID()), idset.IDSet(0), nil,
			v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType)
		rv.Status.DatameshTransitions = append(rv.Status.DatameshTransitions,
			v1alpha1.ReplicatedVolumeDatameshTransition{
				Type:             v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach,
				DatameshRevision: rv.Status.DatameshRevision,
				ReplicaName:      as.member.Name,
				StartedAt:        metav1.Now(),
				Message:          msg,
			},
		)
		changed = true
		as.hasActiveAttachTransition = true
		atts.potentiallyAttached.Add(as.member.ID())
		as.conditionReason = v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttaching
		as.conditionMessage = "Attaching, " + msg
	}

	return changed
}
