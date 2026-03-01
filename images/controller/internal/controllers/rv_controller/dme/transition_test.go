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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

var _ = Describe("createReplicaTransition", func() {
	It("creates a transition with all steps from plan", func() {
		rv := &v1alpha1.ReplicatedVolume{}

		reg := NewRegistry()
		reg.ReplicaTransition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica).
			Plan("access", v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership).
			ReplicaType(v1alpha1.ReplicaTypeAccess).
			ReplicaStep("✦ → A", stubReplicaApply, stubReplicaConfirm).
			MessagePrefix("Joining datamesh").
			Build()

		eng := &Engine{registry: reg, globalCtx: GlobalContext{RV: rv}}
		rctx := &ReplicaContext{
			NodeName:          "node-x",
			RVR:               &v1alpha1.ReplicatedVolumeReplica{ObjectMeta: metav1.ObjectMeta{Name: "rv-1-5"}},
			MembershipRequest: &v1alpha1.ReplicatedVolumeDatameshReplicaRequest{Name: "rv-1-5"},
		}

		t, reason := eng.createReplicaTransition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica, "access", rctx)

		Expect(reason).To(BeEmpty())
		Expect(t).NotTo(BeNil())
		Expect(t.Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica))
		Expect(t.Group).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership))
		Expect(t.PlanID).To(Equal("access"))
		Expect(t.ReplicaName).To(Equal("rv-1-5"))
		Expect(t.ReplicaType).To(Equal(v1alpha1.ReplicaTypeAccess))
		Expect(t.Steps).To(HaveLen(1))
		Expect(t.Steps[0].Name).To(Equal("✦ → A"))
		Expect(t.Steps[0].Status).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive))
		Expect(t.Steps[0].StartedAt).NotTo(BeNil())
		Expect(t.Steps[0].DatameshRevision).To(Equal(int64(1)))
		Expect(t.Steps[0].Message).To(ContainSubstring("0/0 replicas confirmed"))
		Expect(rv.Status.DatameshRevision).To(Equal(int64(1)))
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		// Membership message composed with prefix.
		Expect(rctx.membershipMessage).To(ContainSubstring("Joining datamesh"))
	})

	It("blocks when parallelism check fails", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica,
						Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
						ReplicaName: "rv-1-0",
						Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
							{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, StartedAt: ptr.To(metav1.Now())},
						}},
				},
			},
		}

		reg := NewRegistry()
		reg.ReplicaTransition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica).
			Plan("diskful-odd", v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership).
			ReplicaStep("✦ → D∅", stubReplicaApply, stubReplicaConfirm).
			Build()

		eng := &Engine{registry: reg, globalCtx: GlobalContext{RV: rv}}
		rctx := &ReplicaContext{
			NodeName: "node-x",
			Member:   &v1alpha1.DatameshMember{Name: "rv-1-5"},
		}

		t, reason := eng.createReplicaTransition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica, "diskful-odd", rctx)

		Expect(t).To(BeNil())
		Expect(reason).To(ContainSubstring("voting membership"))
	})
})

var _ = Describe("advanceStep", func() {
	It("completes step 0 and activates step 1", func() {
		now := metav1.Now()
		t := &v1alpha1.ReplicatedVolumeDatameshTransition{
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "✦ → D∅", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, StartedAt: &now, Message: "progress"},
				{Name: "D∅ → D", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending},
			},
		}

		advanceStep(t, 0)

		Expect(t.Steps[0].Status).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted))
		Expect(t.Steps[0].CompletedAt).NotTo(BeNil())
		Expect(t.Steps[0].Message).To(BeEmpty())
		Expect(t.Steps[1].Status).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive))
		Expect(t.Steps[1].StartedAt).NotTo(BeNil())
	})
})

var _ = Describe("cancelTransitionsForReplica", func() {
	It("removes transitions for the given replica", func() {
		now := metav1.Now()
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach, Group: v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment, ReplicaName: "rv-1-0", Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, StartedAt: ptr.To(now)}}},
					{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica, Group: v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership, ReplicaName: "rv-1-3", Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, StartedAt: ptr.To(now)}}},
					{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach, Group: v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment, ReplicaName: "rv-1-0", Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, StartedAt: ptr.To(now)}}},
				},
			},
		}

		eng := &Engine{registry: NewRegistry(), globalCtx: GlobalContext{RV: rv}}
		changed := eng.cancelTransitionsForReplica(rv, 0)
		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].ReplicaName).To(Equal("rv-1-3"))
	})

	It("returns false when no transitions match", func() {
		rv := &v1alpha1.ReplicatedVolume{}
		eng := &Engine{registry: NewRegistry(), globalCtx: GlobalContext{RV: rv}}
		changed := eng.cancelTransitionsForReplica(rv, 5)
		Expect(changed).To(BeFalse())
	})
})
