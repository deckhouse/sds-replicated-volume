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

package datamesh

import (
	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/dmte"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/idset"
)

// newConcurrencyTracker creates a new concurrency tracker implementing dmte.Tracker.
//
// The tracker operates on globalContext fields — all transition tracking state
// (IDSets, bools, potentiallyAttached) lives on gctx. The tracker is a stateless
// behavior layer that mutates and reads gctx fields.
//
// Initializes gctx.potentiallyAttached from current member state (Attached == true).
// The engine populates the tracker via Add() calls for existing transitions
// during NewEngine().
func newConcurrencyTracker(gctx *globalContext) *concurrencyTracker {
	// Init potentiallyAttached from current member state: members with
	// Attached == true are already potentially attached before any
	// transitions are loaded.
	for i := range gctx.allReplicas {
		rc := &gctx.allReplicas[i]
		if rc.member != nil && rc.member.Attached {
			gctx.potentiallyAttached.Add(rc.id)
		}
	}
	return &concurrencyTracker{gctx: gctx}
}

// concurrencyTracker implements dmte.Tracker as a stateless behavior layer
// on top of globalContext. All tracking state (IDSets, bools, potentiallyAttached)
// lives on gctx — the tracker reads and mutates those fields.
//
// Updated incrementally when transitions are created (Add), completed (Remove),
// or cancelled (Remove).
type concurrencyTracker struct {
	// gctx is a reference to the global context that owns the tracking state.
	gctx *globalContext
}

// Add registers a newly created or existing transition in the tracker.
func (c *concurrencyTracker) Add(t *dmte.Transition) {
	switch t.Group {
	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupFormation:
		c.gctx.hasFormationTransition = true
	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupQuorum:
		c.gctx.hasQuorumTransition = true
	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupMultiattach:
		c.gctx.hasMultiattachTransition = true
	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupNetwork:
		c.gctx.hasNetworkTransition = true
		if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeSystemNetworks {
			c.gctx.targetSystemNetworkNames = &t.TargetSystemNetworkNames
		}
	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership:
		c.gctx.votingMembershipTransitions.Add(t.ReplicaID())
	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership:
		c.gctx.nonVotingMembershipTransitions.Add(t.ReplicaID())
	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment:
		c.gctx.attachmentTransitions.Add(t.ReplicaID())
		c.gctx.potentiallyAttached.Add(t.ReplicaID()) // attaching or detaching — still possibly Primary
	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupEmergency:
		c.gctx.emergencyTransitions.Add(t.ReplicaID())
	}
}

// Remove unregisters a completed or cancelled transition from the tracker.
// Must be called BEFORE the transition is removed from storage.
func (c *concurrencyTracker) Remove(t *dmte.Transition) {
	switch t.Group {
	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupFormation:
		c.gctx.hasFormationTransition = false
	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupQuorum:
		c.gctx.hasQuorumTransition = false
	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupMultiattach:
		c.gctx.hasMultiattachTransition = false
	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupNetwork:
		c.gctx.hasNetworkTransition = false
		c.gctx.targetSystemNetworkNames = nil
	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership:
		c.gctx.votingMembershipTransitions.Remove(t.ReplicaID())
	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership:
		c.gctx.nonVotingMembershipTransitions.Remove(t.ReplicaID())
	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment:
		c.gctx.attachmentTransitions.Remove(t.ReplicaID())
		if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach {
			c.gctx.potentiallyAttached.Remove(t.ReplicaID()) // detach confirmed — no longer Primary
		}
	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupEmergency:
		c.gctx.emergencyTransitions.Remove(t.ReplicaID())
	}
}

// CanAdmit returns (true, "", nil) if the proposed transition is allowed,
// (false, message, nil) if blocked.
//
// The engine passes a temporary Transition with Type, Group, and ReplicaName set.
// Global transitions have ReplicaName == "".
//
// Universal parallelism rules:
//   - Formation blocks everything except Network and Emergency.
//   - Per-member: at most one membership and one attachment transition per replica
//     (except Emergency which cancels conflicts first).
//   - VotingMembership: serialized — only one active at a time.
//     VotingMembership and Quorum are mutually exclusive (voter set must be stable).
//     Blocked by active Network (addresses must be stable before voter changes).
//   - Quorum: blocked by VotingMembership (voter set must be stable).
//     Blocked by active Network (addresses must be stable before quorum changes).
//   - Network: serialized (at most one active). Blocks VotingMembership and Quorum
//     (semi-emergency: not blocked by Formation, VotingMembership, or Quorum).
//   - Attachment: multiattach readiness checked via potentiallyAttached state
//     (second+ attach requires multiattach enabled and confirmed).
//   - Multiattach: serialized (no double-toggle).
//   - Emergency: always allowed (per-member conflicts are cancelled before creation).
func (c *concurrencyTracker) CanAdmit(t *dmte.Transition) (bool, string, any) {
	gctx := c.gctx
	proposedGroup := t.Group
	isGlobal := t.ReplicaName == ""
	isAttachment := proposedGroup == v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment

	// attachmentDetails returns reason as details for attachment-group proposals,
	// nil for all others. Only the attachment slot consumes details (as the RVA
	// Attached condition reason); other slots ignore them.
	attachmentDetails := func(reason string) any {
		if isAttachment {
			return reason
		}
		return nil
	}

	// Formation blocks everything except Network (semi-emergency) and Emergency.
	if gctx.hasFormationTransition &&
		proposedGroup != v1alpha1.ReplicatedVolumeDatameshTransitionGroupNetwork {
		return false, "Blocked by active Formation",
			attachmentDetails(v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonPending)
	}

	// Emergency always allowed (caller cancels per-member conflicts before creation).
	if proposedGroup == v1alpha1.ReplicatedVolumeDatameshTransitionGroupEmergency {
		return true, "", nil
	}

	// Per-member checks (replica-scoped only).
	if !isGlobal {
		replicaID := t.ReplicaID()

		isMembership := proposedGroup == v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership ||
			proposedGroup == v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership

		if isMembership &&
			(gctx.votingMembershipTransitions.Contains(replicaID) ||
				gctx.nonVotingMembershipTransitions.Contains(replicaID)) {
			return false, "Membership transition already in progress for this replica", nil
		}
		if isAttachment && gctx.attachmentTransitions.Contains(replicaID) {
			return false, "Attachment transition already in progress for this replica",
				v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonPending
		}
	}

	switch proposedGroup {
	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership:
		if gctx.votingMembershipTransitions.Len() > 0 {
			return false, "Another voting membership transition is already in progress", nil
		}
		if gctx.hasQuorumTransition {
			return false, "Blocked by active ChangeQuorum transition", nil
		}
		if gctx.hasNetworkTransition {
			return false, "Blocked by active Network transition", nil
		}

	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership:
		// Can overlap with everything (per-member check above handles same-replica).

	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupQuorum:
		// Serialized: only one ChangeQuorum at a time.
		if gctx.hasQuorumTransition {
			return false, "Another ChangeQuorum transition is already in progress", nil
		}
		// Quorum is blocked by active voting membership transitions (they change
		// the voter set that quorum depends on). Non-voting membership, attachment,
		// and multiattach transitions do not affect quorum and can run in parallel.
		if gctx.votingMembershipTransitions.Len() > 0 {
			return false, "Cannot start ChangeQuorum: voting membership transition is active", nil
		}
		if gctx.hasNetworkTransition {
			return false, "Blocked by active Network transition", nil
		}

	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment:
		// Multiattach readiness: if anyone ELSE is potentially attached,
		// multiattach must be enabled AND confirmed (no active toggle).
		// Exclude the proposed replica from the check: a Detach of the only
		// attached member must not be blocked by the member's own presence
		// in potentiallyAttached.
		others := gctx.potentiallyAttached
		if !isGlobal {
			others = others.Difference(idset.Of(t.ReplicaID()))
		}
		if !others.IsEmpty() {
			if !gctx.datamesh.multiattach || gctx.hasMultiattachTransition {
				return false, "Waiting for multiattach to be enabled",
					v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttaching
			}
		}

	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupMultiattach:
		// Serialized only — dispatcher decides when to toggle.
		if gctx.hasMultiattachTransition {
			return false, "Another Multiattach transition is already in progress", nil
		}

	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupNetwork:
		// Serialized: at most one Network transition at a time.
		if gctx.hasNetworkTransition {
			return false, "Another Network transition is already in progress", nil
		}
	}

	return true, "", nil
}
