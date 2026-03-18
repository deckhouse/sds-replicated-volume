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

package match

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func minAddr() []v1alpha1.DRBDResourceAddressStatus {
	return []v1alpha1.DRBDResourceAddressStatus{
		{SystemNetworkName: "default"},
	}
}

var _ = Describe("RV.Quorum", func() {
	It("matches correct quorum", func() {
		rv := &v1alpha1.ReplicatedVolume{}
		rv.Status.Datamesh.Quorum = 2
		Expect(rv).To(RV.Quorum(2))
	})

	It("fails on wrong quorum", func() {
		rv := &v1alpha1.ReplicatedVolume{}
		rv.Status.Datamesh.Quorum = 1
		Expect(rv).ToNot(RV.Quorum(2))
	})
})

var _ = Describe("RV.Members", func() {
	It("matches zero members", func() {
		rv := &v1alpha1.ReplicatedVolume{}
		Expect(rv).To(RV.Members(0))
	})

	It("matches 3 members", func() {
		rv := &v1alpha1.ReplicatedVolume{}
		rv.Status.Datamesh.Members = []v1alpha1.DatameshMember{
			{Name: "rv-0", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "n1", Addresses: minAddr()},
			{Name: "rv-1", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "n2", Addresses: minAddr()},
			{Name: "rv-2", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "n3", Addresses: minAddr()},
		}
		Expect(rv).To(RV.Members(3))
	})

	It("fails on wrong count", func() {
		rv := &v1alpha1.ReplicatedVolume{}
		rv.Status.Datamesh.Members = []v1alpha1.DatameshMember{
			{Name: "rv-0", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "n1", Addresses: minAddr()},
		}
		Expect(rv).ToNot(RV.Members(3))
	})
})

var _ = Describe("RV.Multiattach", func() {
	It("matches true", func() {
		rv := &v1alpha1.ReplicatedVolume{}
		rv.Status.Datamesh.Multiattach = true
		Expect(rv).To(RV.Multiattach(true))
	})

	It("matches false", func() {
		rv := &v1alpha1.ReplicatedVolume{}
		Expect(rv).To(RV.Multiattach(false))
	})

	It("fails on mismatch", func() {
		rv := &v1alpha1.ReplicatedVolume{}
		rv.Status.Datamesh.Multiattach = true
		Expect(rv).ToNot(RV.Multiattach(false))
	})
})

var _ = Describe("RV.HasActiveTransition", func() {
	It("matches when active transition exists", func() {
		rv := &v1alpha1.ReplicatedVolume{}
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{
			{
				Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation,
				Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
					{Name: "step1", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive},
				},
			},
		}
		Expect(rv).To(RV.HasActiveTransition(string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation)))
	})

	It("fails when transition is completed", func() {
		rv := &v1alpha1.ReplicatedVolume{}
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{
			{
				Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation,
				Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
					{Name: "step1", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted},
				},
			},
		}
		Expect(rv).ToNot(RV.HasActiveTransition(string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation)))
	})

	It("fails when no transitions", func() {
		rv := &v1alpha1.ReplicatedVolume{}
		Expect(rv).ToNot(RV.HasActiveTransition(string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation)))
	})
})

var _ = Describe("RV.FormationComplete", func() {
	It("matches when formation transition completed", func() {
		rv := &v1alpha1.ReplicatedVolume{}
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{
			{
				Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation,
				Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
					{Name: "s1", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted},
					{Name: "s2", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted},
				},
			},
		}
		Expect(rv).To(RV.FormationComplete())
	})

	It("fails when formation transition active", func() {
		rv := &v1alpha1.ReplicatedVolume{}
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{
			{
				Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation,
				Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
					{Name: "s1", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive},
				},
			},
		}
		Expect(rv).ToNot(RV.FormationComplete())
	})

	It("matches when no formation transition but members present", func() {
		rv := &v1alpha1.ReplicatedVolume{}
		rv.Status.Datamesh.Members = []v1alpha1.DatameshMember{
			{Name: "rv-0", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "n1", Addresses: minAddr()},
		}
		Expect(rv).To(RV.FormationComplete())
	})

	It("fails when no formation transition and no members", func() {
		rv := &v1alpha1.ReplicatedVolume{}
		Expect(rv).ToNot(RV.FormationComplete())
	})
})

var _ = Describe("RV.QuorumCorrect", func() {
	It("passes with correct quorum (3 voters, q=2)", func() {
		rv := &v1alpha1.ReplicatedVolume{}
		rv.Status.Datamesh.Quorum = 2
		rv.Status.Datamesh.Members = []v1alpha1.DatameshMember{
			{Name: "rv-0", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "n1", Addresses: minAddr()},
			{Name: "rv-1", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "n2", Addresses: minAddr()},
			{Name: "rv-2", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "n3", Addresses: minAddr()},
		}
		Expect(rv).To(RV.QuorumCorrect())
	})

	It("fails with wrong quorum", func() {
		rv := &v1alpha1.ReplicatedVolume{}
		rv.Status.Datamesh.Quorum = 5
		rv.Status.Datamesh.Members = []v1alpha1.DatameshMember{
			{Name: "rv-0", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "n1", Addresses: minAddr()},
			{Name: "rv-1", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "n2", Addresses: minAddr()},
		}
		Expect(rv).ToNot(RV.QuorumCorrect())
	})

	It("passes with no members", func() {
		rv := &v1alpha1.ReplicatedVolume{}
		Expect(rv).To(RV.QuorumCorrect())
	})

	It("passes with only non-voter members", func() {
		rv := &v1alpha1.ReplicatedVolume{}
		rv.Status.Datamesh.Members = []v1alpha1.DatameshMember{
			{Name: "rv-0", Type: v1alpha1.DatameshMemberTypeTieBreaker, NodeName: "n1", Addresses: minAddr()},
			{Name: "rv-1", Type: v1alpha1.DatameshMemberTypeAccess, NodeName: "n2", Addresses: minAddr()},
		}
		Expect(rv).To(RV.QuorumCorrect())
	})

	It("counts LiminalDiskful as voter", func() {
		rv := &v1alpha1.ReplicatedVolume{}
		rv.Status.Datamesh.Quorum = 2
		rv.Status.Datamesh.Members = []v1alpha1.DatameshMember{
			{Name: "rv-0", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "n1", Addresses: minAddr()},
			{Name: "rv-1", Type: v1alpha1.DatameshMemberTypeLiminalDiskful, NodeName: "n2", Addresses: minAddr()},
			{Name: "rv-2", Type: v1alpha1.DatameshMemberTypeTieBreaker, NodeName: "n3", Addresses: minAddr()},
		}
		Expect(rv).To(RV.QuorumCorrect())
	})

	It("ignores ShadowDiskful in voter count", func() {
		rv := &v1alpha1.ReplicatedVolume{}
		rv.Status.Datamesh.Quorum = 2
		rv.Status.Datamesh.Members = []v1alpha1.DatameshMember{
			{Name: "rv-0", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "n1", Addresses: minAddr()},
			{Name: "rv-1", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "n2", Addresses: minAddr()},
			{Name: "rv-2", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "n3", Addresses: minAddr()},
			{Name: "rv-3", Type: v1alpha1.DatameshMemberTypeShadowDiskful, NodeName: "n4", Addresses: minAddr()},
		}
		Expect(rv).To(RV.QuorumCorrect())
	})
})

var _ = Describe("RV.SafetyChecks", func() {
	It("returns 1 matcher", func() {
		Expect(RV.SafetyChecks()).To(HaveLen(1))
	})
})

var _ = Describe("RV.Custom", func() {
	It("receives typed object", func() {
		rv := &v1alpha1.ReplicatedVolume{}
		rv.Name = "test-rv"
		Expect(rv).To(RV.Custom("name check", func(r *v1alpha1.ReplicatedVolume) bool {
			return r.Name == "test-rv"
		}))
	})

	It("fails when function returns false", func() {
		rv := &v1alpha1.ReplicatedVolume{}
		Expect(rv).ToNot(RV.Custom("always false", func(_ *v1alpha1.ReplicatedVolume) bool {
			return false
		}))
	})
})
