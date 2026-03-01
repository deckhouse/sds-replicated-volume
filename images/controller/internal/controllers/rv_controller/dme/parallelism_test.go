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

func mkActiveTx(typ v1alpha1.ReplicatedVolumeDatameshTransitionType, group v1alpha1.ReplicatedVolumeDatameshTransitionGroup, replicaName string) v1alpha1.ReplicatedVolumeDatameshTransition {
	return v1alpha1.ReplicatedVolumeDatameshTransition{
		Type:        typ,
		Group:       group,
		ReplicaName: replicaName,
		Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
			{Name: "step", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, StartedAt: ptr.To(metav1.Now())},
		},
	}
}

var _ = Describe("parallelismCache", func() {
	It("allows NonVotingMembership when no active transitions", func() {
		cache := &parallelismCache{}
		allowed, _ := cache.check(nil, v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership, 1)
		Expect(allowed).To(BeTrue())
	})

	It("blocks per-member membership when same replica has active membership transition", func() {
		transitions := []v1alpha1.ReplicatedVolumeDatameshTransition{
			mkActiveTx(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica, v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership, "rv-1-3"),
		}
		cache := &parallelismCache{}
		allowed, reason := cache.check(transitions, v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership, 3)
		Expect(allowed).To(BeFalse())
		Expect(reason).To(ContainSubstring("Membership transition already"))
	})

	It("allows attachment alongside membership on same member", func() {
		transitions := []v1alpha1.ReplicatedVolumeDatameshTransition{
			mkActiveTx(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica, v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership, "rv-1-3"),
		}
		cache := &parallelismCache{}
		allowed, _ := cache.check(transitions, v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment, 3)
		Expect(allowed).To(BeTrue())
	})

	It("blocks VotingMembership when another VotingMembership is active", func() {
		transitions := []v1alpha1.ReplicatedVolumeDatameshTransition{
			mkActiveTx(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica, v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership, "rv-1-0"),
		}
		cache := &parallelismCache{}
		allowed, reason := cache.check(transitions, v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership, 5)
		Expect(allowed).To(BeFalse())
		Expect(reason).To(ContainSubstring("voting membership"))
	})

	It("allows NonVotingMembership alongside VotingMembership on different member", func() {
		transitions := []v1alpha1.ReplicatedVolumeDatameshTransition{
			mkActiveTx(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica, v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership, "rv-1-0"),
		}
		cache := &parallelismCache{}
		allowed, _ := cache.check(transitions, v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership, 5)
		Expect(allowed).To(BeTrue())
	})

	It("blocks everything when Quorum is active", func() {
		transitions := []v1alpha1.ReplicatedVolumeDatameshTransition{
			mkActiveTx(v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeQuorum, v1alpha1.ReplicatedVolumeDatameshTransitionGroupQuorum, ""),
		}
		cache := &parallelismCache{}
		allowed, reason := cache.check(transitions, v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership, 5)
		Expect(allowed).To(BeFalse())
		Expect(reason).To(ContainSubstring("ChangeQuorum"))
	})

	It("blocks Quorum when any membership transition is active", func() {
		transitions := []v1alpha1.ReplicatedVolumeDatameshTransition{
			mkActiveTx(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica, v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership, "rv-1-0"),
		}
		cache := &parallelismCache{}
		allowed, reason := cache.check(transitions, v1alpha1.ReplicatedVolumeDatameshTransitionGroupQuorum, noReplicaID)
		Expect(allowed).To(BeFalse())
		Expect(reason).To(ContainSubstring("Cannot start ChangeQuorum"))
	})

	It("allows Emergency even when Voter is active", func() {
		transitions := []v1alpha1.ReplicatedVolumeDatameshTransition{
			mkActiveTx(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica, v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership, "rv-1-0"),
		}
		cache := &parallelismCache{}
		allowed, _ := cache.check(transitions, v1alpha1.ReplicatedVolumeDatameshTransitionGroupEmergency, 5)
		Expect(allowed).To(BeTrue())
	})

	It("blocks Attachment when Multiattach is active", func() {
		transitions := []v1alpha1.ReplicatedVolumeDatameshTransition{
			mkActiveTx(v1alpha1.ReplicatedVolumeDatameshTransitionTypeEnableMultiattach, v1alpha1.ReplicatedVolumeDatameshTransitionGroupMultiattach, ""),
		}
		cache := &parallelismCache{}
		allowed, reason := cache.check(transitions, v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment, 5)
		Expect(allowed).To(BeFalse())
		Expect(reason).To(ContainSubstring("Multiattach"))
	})

	It("blocks everything when Formation is active", func() {
		transitions := []v1alpha1.ReplicatedVolumeDatameshTransition{
			mkActiveTx(v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation, v1alpha1.ReplicatedVolumeDatameshTransitionGroupFormation, ""),
		}
		cache := &parallelismCache{}
		allowed, reason := cache.check(transitions, v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership, 5)
		Expect(allowed).To(BeFalse())
		Expect(reason).To(ContainSubstring("Formation"))
	})
})
