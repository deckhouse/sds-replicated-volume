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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/dmte"
)

// mkTransition builds a minimal active transition for tracker tests.
func mkTransition(
	typ v1alpha1.ReplicatedVolumeDatameshTransitionType,
	group v1alpha1.ReplicatedVolumeDatameshTransitionGroup,
	replicaName string,
) *dmte.Transition {
	return &dmte.Transition{
		Type:        typ,
		Group:       group,
		ReplicaName: replicaName,
		Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
			{Name: "step", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, StartedAt: ptr.To(metav1.Now())},
		},
	}
}

// mkProposal builds a temporary transition for CanAdmit (same shape as engine creates).
func mkProposal(
	group v1alpha1.ReplicatedVolumeDatameshTransitionGroup,
	replicaName string,
) *dmte.Transition {
	return &dmte.Transition{
		Group:       group,
		ReplicaName: replicaName,
	}
}

var _ = Describe("concurrencyTracker", func() {
	It("allows NonVotingMembership when no active transitions", func() {
		tracker := newConcurrencyTracker(&globalContext{})
		allowed, _, _ := tracker.CanAdmit(mkProposal(v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership, "rv-1-1"))
		Expect(allowed).To(BeTrue())
	})

	It("blocks per-member membership when same replica has active membership transition", func() {
		tracker := newConcurrencyTracker(&globalContext{})
		tracker.Add(mkTransition(
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica,
			v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership,
			"rv-1-3",
		))

		allowed, reason, _ := tracker.CanAdmit(mkProposal(v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership, "rv-1-3"))
		Expect(allowed).To(BeFalse())
		Expect(reason).To(ContainSubstring("Membership transition already"))
	})

	It("allows attachment alongside membership on same member", func() {
		tracker := newConcurrencyTracker(&globalContext{})
		tracker.Add(mkTransition(
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica,
			v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership,
			"rv-1-3",
		))

		allowed, _, _ := tracker.CanAdmit(mkProposal(v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment, "rv-1-3"))
		Expect(allowed).To(BeTrue())
	})

	It("blocks VotingMembership when another VotingMembership is active", func() {
		tracker := newConcurrencyTracker(&globalContext{})
		tracker.Add(mkTransition(
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica,
			v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			"rv-1-0",
		))

		allowed, reason, _ := tracker.CanAdmit(mkProposal(v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership, "rv-1-5"))
		Expect(allowed).To(BeFalse())
		Expect(reason).To(ContainSubstring("voting membership"))
	})

	It("allows NonVotingMembership alongside VotingMembership on different member", func() {
		tracker := newConcurrencyTracker(&globalContext{})
		tracker.Add(mkTransition(
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica,
			v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			"rv-1-0",
		))

		allowed, _, _ := tracker.CanAdmit(mkProposal(v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership, "rv-1-5"))
		Expect(allowed).To(BeTrue())
	})

	It("allows NonVotingMembership when Quorum is active", func() {
		tracker := newConcurrencyTracker(&globalContext{})
		tracker.Add(mkTransition(
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeQuorum,
			v1alpha1.ReplicatedVolumeDatameshTransitionGroupQuorum,
			"",
		))

		allowed, _, _ := tracker.CanAdmit(mkProposal(v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership, "rv-1-5"))
		Expect(allowed).To(BeTrue())
	})

	It("blocks Quorum when voting membership transition is active", func() {
		tracker := newConcurrencyTracker(&globalContext{})
		tracker.Add(mkTransition(
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica,
			v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			"rv-1-0",
		))

		allowed, reason, _ := tracker.CanAdmit(mkProposal(v1alpha1.ReplicatedVolumeDatameshTransitionGroupQuorum, ""))
		Expect(allowed).To(BeFalse())
		Expect(reason).To(ContainSubstring("Cannot start ChangeQuorum"))
	})

	It("allows Quorum when only non-voting membership is active", func() {
		tracker := newConcurrencyTracker(&globalContext{})
		tracker.Add(mkTransition(
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica,
			v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership,
			"rv-1-0",
		))

		allowed, _, _ := tracker.CanAdmit(mkProposal(v1alpha1.ReplicatedVolumeDatameshTransitionGroupQuorum, ""))
		Expect(allowed).To(BeTrue())
	})

	It("allows Emergency even when Voter is active", func() {
		tracker := newConcurrencyTracker(&globalContext{})
		tracker.Add(mkTransition(
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica,
			v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			"rv-1-0",
		))

		allowed, _, _ := tracker.CanAdmit(mkProposal(v1alpha1.ReplicatedVolumeDatameshTransitionGroupEmergency, "rv-1-5"))
		Expect(allowed).To(BeTrue())
	})

	It("blocks everything when Formation is active", func() {
		tracker := newConcurrencyTracker(&globalContext{})
		tracker.Add(mkTransition(
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation,
			v1alpha1.ReplicatedVolumeDatameshTransitionGroupFormation,
			"",
		))

		allowed, reason, _ := tracker.CanAdmit(mkProposal(v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership, "rv-1-5"))
		Expect(allowed).To(BeFalse())
		Expect(reason).To(ContainSubstring("Formation"))
	})

	It("blocks duplicate multiattach", func() {
		tracker := newConcurrencyTracker(&globalContext{})
		tracker.Add(mkTransition(
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeEnableMultiattach,
			v1alpha1.ReplicatedVolumeDatameshTransitionGroupMultiattach,
			"",
		))

		allowed, reason, _ := tracker.CanAdmit(mkProposal(v1alpha1.ReplicatedVolumeDatameshTransitionGroupMultiattach, ""))
		Expect(allowed).To(BeFalse())
		Expect(reason).To(ContainSubstring("Multiattach transition is already"))
	})

	It("blocks per-member attachment when same replica has active attachment", func() {
		tracker := newConcurrencyTracker(&globalContext{})
		tracker.Add(mkTransition(
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach,
			v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment,
			"rv-1-3",
		))

		allowed, reason, _ := tracker.CanAdmit(mkProposal(v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment, "rv-1-3"))
		Expect(allowed).To(BeFalse())
		Expect(reason).To(ContainSubstring("Attachment transition already"))
	})

	It("Remove unblocks subsequent CanAdmit", func() {
		tracker := newConcurrencyTracker(&globalContext{})
		t := mkTransition(
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica,
			v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			"rv-1-0",
		)
		tracker.Add(t)

		// Blocked before Remove.
		allowed, _, _ := tracker.CanAdmit(mkProposal(v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership, "rv-1-5"))
		Expect(allowed).To(BeFalse())

		tracker.Remove(t)

		// Allowed after Remove.
		allowed, _, _ = tracker.CanAdmit(mkProposal(v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership, "rv-1-5"))
		Expect(allowed).To(BeTrue())
	})

	It("blocks Emergency when Formation is active", func() {
		tracker := newConcurrencyTracker(&globalContext{})
		tracker.Add(mkTransition(
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation,
			v1alpha1.ReplicatedVolumeDatameshTransitionGroupFormation,
			"",
		))

		allowed, reason, _ := tracker.CanAdmit(mkProposal(v1alpha1.ReplicatedVolumeDatameshTransitionGroupEmergency, "rv-1-5"))
		Expect(allowed).To(BeFalse())
		Expect(reason).To(ContainSubstring("Formation"))
	})

	It("blocks VotingMembership when Quorum is active", func() {
		tracker := newConcurrencyTracker(&globalContext{})
		tracker.Add(mkTransition(
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeQuorum,
			v1alpha1.ReplicatedVolumeDatameshTransitionGroupQuorum,
			"",
		))

		allowed, reason, _ := tracker.CanAdmit(mkProposal(v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership, "rv-1-0"))
		Expect(allowed).To(BeFalse())
		Expect(reason).To(ContainSubstring("ChangeQuorum"))
	})

	It("allows Attachment when Quorum is active", func() {
		tracker := newConcurrencyTracker(&globalContext{})
		tracker.Add(mkTransition(
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeQuorum,
			v1alpha1.ReplicatedVolumeDatameshTransitionGroupQuorum,
			"",
		))

		allowed, _, _ := tracker.CanAdmit(mkProposal(v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment, "rv-1-0"))
		Expect(allowed).To(BeTrue())
	})

	It("allows Attachment on different replicas in parallel when multiattach enabled", func() {
		gctx := &globalContext{datamesh: datameshContext{multiattach: true}}
		tracker := newConcurrencyTracker(gctx)
		tracker.Add(mkTransition(
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach,
			v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment,
			"rv-1-0",
		))

		allowed, _, _ := tracker.CanAdmit(mkProposal(v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment, "rv-1-5"))
		Expect(allowed).To(BeTrue())
	})

	It("allows NonVotingMembership on different replicas in parallel", func() {
		tracker := newConcurrencyTracker(&globalContext{})
		tracker.Add(mkTransition(
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica,
			v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership,
			"rv-1-0",
		))

		allowed, _, _ := tracker.CanAdmit(mkProposal(v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership, "rv-1-5"))
		Expect(allowed).To(BeTrue())
	})

	It("allows first Attach when potentiallyAttached is empty", func() {
		tracker := newConcurrencyTracker(&globalContext{})

		allowed, _, _ := tracker.CanAdmit(mkProposal(v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment, "rv-1-0"))
		Expect(allowed).To(BeTrue())
	})

	It("blocks second Attach when multiattach not enabled", func() {
		gctx := &globalContext{datamesh: datameshContext{multiattach: false}}
		tracker := newConcurrencyTracker(gctx)
		// First attach adds to potentiallyAttached.
		tracker.Add(mkTransition(
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach,
			v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment,
			"rv-1-0",
		))

		allowed, reason, _ := tracker.CanAdmit(mkProposal(v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment, "rv-1-5"))
		Expect(allowed).To(BeFalse())
		Expect(reason).To(ContainSubstring("multiattach"))
	})

	It("blocks Attach when EnableMultiattach is active", func() {
		gctx := &globalContext{datamesh: datameshContext{multiattach: true}}
		// One member already attached (from init).
		gctx.allReplicas = []ReplicaContext{{id: 0, member: &v1alpha1.DatameshMember{Name: "rv-1-0", Attached: true}}}
		tracker := newConcurrencyTracker(gctx)
		// EnableMultiattach in progress.
		tracker.Add(mkTransition(
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeEnableMultiattach,
			v1alpha1.ReplicatedVolumeDatameshTransitionGroupMultiattach,
			"",
		))

		allowed, reason, _ := tracker.CanAdmit(mkProposal(v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment, "rv-1-5"))
		Expect(allowed).To(BeFalse())
		Expect(reason).To(ContainSubstring("multiattach"))
	})

	It("allows second Attach when multiattach enabled and confirmed", func() {
		gctx := &globalContext{datamesh: datameshContext{multiattach: true}}
		gctx.allReplicas = []ReplicaContext{{id: 0, member: &v1alpha1.DatameshMember{Name: "rv-1-0", Attached: true}}}
		tracker := newConcurrencyTracker(gctx)

		// No active multiattach transition — multiattach is confirmed.
		allowed, _, _ := tracker.CanAdmit(mkProposal(v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment, "rv-1-5"))
		Expect(allowed).To(BeTrue())
	})

	It("allows multiattach when Attachment is active", func() {
		gctx := &globalContext{}
		tracker := newConcurrencyTracker(gctx)
		tracker.Add(mkTransition(
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach,
			v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment,
			"rv-1-0",
		))

		allowed, _, _ := tracker.CanAdmit(mkProposal(v1alpha1.ReplicatedVolumeDatameshTransitionGroupMultiattach, ""))
		Expect(allowed).To(BeTrue())
	})

	It("Remove Detach clears potentiallyAttached", func() {
		gctx := &globalContext{datamesh: datameshContext{multiattach: false}}
		gctx.allReplicas = []ReplicaContext{
			{id: 0, member: &v1alpha1.DatameshMember{Name: "rv-1-0", Attached: true}},
		}
		tracker := newConcurrencyTracker(gctx)

		// potentiallyAttached has rv-1-0 from init.
		// Second Attach should be blocked.
		allowed, _, _ := tracker.CanAdmit(mkProposal(v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment, "rv-1-5"))
		Expect(allowed).To(BeFalse())

		// Complete a Detach for rv-1-0.
		detachT := mkTransition(
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach,
			v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment,
			"rv-1-0",
		)
		tracker.Add(detachT)
		tracker.Remove(detachT)

		// Now potentiallyAttached is empty — second Attach should be allowed.
		allowed, _, _ = tracker.CanAdmit(mkProposal(v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment, "rv-1-5"))
		Expect(allowed).To(BeTrue())
	})

	It("CanAdmit returns nil details for non-attachment proposals", func() {
		tracker := newConcurrencyTracker(&globalContext{})
		tracker.Add(mkTransition(
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation,
			v1alpha1.ReplicatedVolumeDatameshTransitionGroupFormation,
			"",
		))

		_, _, details := tracker.CanAdmit(mkProposal(v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership, "rv-1-0"))
		Expect(details).To(BeNil())
	})

	It("CanAdmit returns condition reason as details for attachment proposals", func() {
		tracker := newConcurrencyTracker(&globalContext{})
		tracker.Add(mkTransition(
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation,
			v1alpha1.ReplicatedVolumeDatameshTransitionGroupFormation,
			"",
		))

		_, _, details := tracker.CanAdmit(mkProposal(v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment, "rv-1-0"))
		Expect(details).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonPending))
	})
})
