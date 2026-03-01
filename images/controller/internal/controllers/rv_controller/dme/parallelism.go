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

package dme

import (
	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/idset"
)

// noReplicaID is the sentinel value for transitions without a specific replica (global scope).
// Must be outside the valid replica ID range (0-31).
const noReplicaID uint8 = 255

// parallelismCache tracks active transitions per group for O(1) parallelism checks.
// Initialized on-demand from rv.Status.DatameshTransitions on first check() call.
// Updated incrementally when transitions are created, completed, or cancelled.
type parallelismCache struct {
	initialized bool

	// Global scope groups (bool — at most one active).
	hasFormationTransition   bool
	hasQuorumTransition      bool
	hasMultiattachTransition bool

	// Member scope groups (idset — which replicas have active transitions).
	votingMembershipTransitions    idset.IDSet
	nonVotingMembershipTransitions idset.IDSet
	attachmentTransitions          idset.IDSet
	emergencyTransitions           idset.IDSet
}

// ensureInitialized builds the cache from rv.Status.DatameshTransitions if not yet initialized.
func (c *parallelismCache) ensureInitialized(transitions []v1alpha1.ReplicatedVolumeDatameshTransition) {
	if c.initialized {
		return
	}
	for i := range transitions {
		c.add(&transitions[i])
	}
	c.initialized = true
}

// add registers an active transition in the cache.
func (c *parallelismCache) add(t *v1alpha1.ReplicatedVolumeDatameshTransition) {
	switch t.Group {
	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupFormation:
		c.hasFormationTransition = true
	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupQuorum:
		c.hasQuorumTransition = true
	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupMultiattach:
		c.hasMultiattachTransition = true
	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership:
		c.votingMembershipTransitions.Add(t.ReplicaID())
	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership:
		c.nonVotingMembershipTransitions.Add(t.ReplicaID())
	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment:
		c.attachmentTransitions.Add(t.ReplicaID())
	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupEmergency:
		c.emergencyTransitions.Add(t.ReplicaID())
	}
}

// remove unregisters a transition from the cache.
// Must be called BEFORE the transition is removed from the slice (pointer must be valid).
func (c *parallelismCache) remove(t *v1alpha1.ReplicatedVolumeDatameshTransition) {
	switch t.Group {
	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupFormation:
		c.hasFormationTransition = false
	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupQuorum:
		c.hasQuorumTransition = false
	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupMultiattach:
		c.hasMultiattachTransition = false
	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership:
		c.votingMembershipTransitions.Remove(t.ReplicaID())
	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership:
		c.nonVotingMembershipTransitions.Remove(t.ReplicaID())
	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment:
		c.attachmentTransitions.Remove(t.ReplicaID())
	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupEmergency:
		c.emergencyTransitions.Remove(t.ReplicaID())
	}
}

// check returns (true, "") if the proposed transition is allowed, (false, reason) if blocked.
//
// Universal parallelism rules:
//   - Formation blocks everything.
//   - Per-member: at most one transition per replica (except Emergency which cancels conflicts first).
//   - VotingMembership: serialized — only one active at a time.
//   - Quorum: exclusive — blocks all others, blocked by all others.
//   - Attachment: blocked by Quorum and Multiattach.
//   - Multiattach: blocked by Quorum and Attachment; serialized.
//   - Emergency: always allowed (per-member conflicts are cancelled before creation).
func (c *parallelismCache) check(
	transitions []v1alpha1.ReplicatedVolumeDatameshTransition,
	proposedGroup v1alpha1.ReplicatedVolumeDatameshTransitionGroup,
	proposedReplicaID uint8,
) (allowed bool, reason string) {
	c.ensureInitialized(transitions)

	// Formation blocks everything.
	if c.hasFormationTransition {
		return false, "Blocked by active Formation"
	}

	// Emergency always allowed (caller cancels per-member conflicts before creation).
	if proposedGroup == v1alpha1.ReplicatedVolumeDatameshTransitionGroupEmergency {
		return true, ""
	}

	// Per-member: at most one membership transition and at most one attachment transition per replica.
	if proposedReplicaID != noReplicaID {
		isMembership := proposedGroup == v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership ||
			proposedGroup == v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership
		isAttachment := proposedGroup == v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment

		if isMembership &&
			(c.votingMembershipTransitions.Contains(proposedReplicaID) ||
				c.nonVotingMembershipTransitions.Contains(proposedReplicaID)) {
			return false, "Membership transition already in progress for this replica"
		}
		if isAttachment && c.attachmentTransitions.Contains(proposedReplicaID) {
			return false, "Attachment transition already in progress for this replica"
		}
		// Emergency per-member check: blocked if emergency already active for this replica.
		if proposedGroup == v1alpha1.ReplicatedVolumeDatameshTransitionGroupEmergency &&
			c.emergencyTransitions.Contains(proposedReplicaID) {
			return false, "Emergency transition already in progress for this replica"
		}
	}

	// Active Quorum blocks everything.
	if c.hasQuorumTransition {
		return false, "Blocked by active ChangeQuorum transition"
	}

	switch proposedGroup {
	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership:
		if c.votingMembershipTransitions.Len() > 0 {
			return false, "Another voting membership transition is already in progress"
		}

	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership:
		// Can overlap with everything (per-member check above handles same-replica).

	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupQuorum:
		// Quorum is exclusive — blocked if any other transition is active.
		if c.votingMembershipTransitions.Len() > 0 ||
			c.nonVotingMembershipTransitions.Len() > 0 ||
			c.attachmentTransitions.Len() > 0 ||
			c.hasMultiattachTransition {
			return false, "Cannot start ChangeQuorum: other transitions are active"
		}

	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment:
		if c.hasMultiattachTransition {
			return false, "Blocked by active Multiattach transition"
		}

	case v1alpha1.ReplicatedVolumeDatameshTransitionGroupMultiattach:
		if c.attachmentTransitions.Len() > 0 {
			return false, "Blocked by active Attachment transitions"
		}
		if c.hasMultiattachTransition {
			return false, "Another Multiattach transition is already in progress"
		}
	}

	return true, ""
}
