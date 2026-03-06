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
)

var _ = Describe("ReplicaContext", func() {
	Describe("dmte.ReplicaCtx interface", func() {
		It("ID returns stored id", func() {
			rc := &ReplicaContext{id: 5}
			Expect(rc.ID()).To(Equal(uint8(5)))
		})

		It("Name returns stored name", func() {
			rc := &ReplicaContext{name: "rv-1-5"}
			Expect(rc.Name()).To(Equal("rv-1-5"))
		})

		It("Exists returns true when RVR is set", func() {
			rc := &ReplicaContext{rvr: &v1alpha1.ReplicatedVolumeReplica{}}
			Expect(rc.Exists()).To(BeTrue())
		})

		It("Exists returns false when RVR is nil", func() {
			rc := &ReplicaContext{}
			Expect(rc.Exists()).To(BeFalse())
		})

		It("Generation returns RVR generation", func() {
			rc := &ReplicaContext{rvr: &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Generation: 7},
			}}
			Expect(rc.Generation()).To(Equal(int64(7)))
		})

		It("Conditions returns RVR status conditions", func() {
			conds := []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue},
			}
			rc := &ReplicaContext{rvr: &v1alpha1.ReplicatedVolumeReplica{
				Status: v1alpha1.ReplicatedVolumeReplicaStatus{Conditions: conds},
			}}
			Expect(rc.Conditions()).To(Equal(conds))
		})
	})

	Describe("output accessors", func() {
		It("MembershipMessage returns stored message", func() {
			rc := &ReplicaContext{membershipMessage: "Joining"}
			Expect(rc.membershipMessage).To(Equal("Joining"))
		})

		It("AttachmentConditionMessage returns stored message", func() {
			rc := &ReplicaContext{attachmentConditionMessage: "Attaching"}
			Expect(rc.AttachmentConditionMessage()).To(Equal("Attaching"))
		})

		It("AttachmentConditionReason returns stored reason", func() {
			rc := &ReplicaContext{attachmentConditionReason: "Pending"}
			Expect(rc.AttachmentConditionReason()).To(Equal("Pending"))
		})
	})
})

var _ = Describe("buildContexts", func() {
	It("collects IDs from Members", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: minimalConfig,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-0", NodeName: "node-a", Type: v1alpha1.DatameshMemberTypeDiskful},
						{Name: "rv-1-3", NodeName: "node-b", Type: v1alpha1.DatameshMemberTypeAccess},
					},
				},
			},
		}

		p := buildContexts(rv, nil, nil, nil, FeatureFlags{})

		rc0 := p.Replica(0)
		Expect(rc0).NotTo(BeNil())
		Expect(rc0.ID()).To(Equal(uint8(0)))
		Expect(rc0.Name()).To(Equal("rv-1-0"))
		Expect(rc0.nodeName).To(Equal("node-a"))
		Expect(rc0.member).NotTo(BeNil())
		Expect(rc0.member.Type).To(Equal(v1alpha1.DatameshMemberTypeDiskful))

		rc3 := p.Replica(3)
		Expect(rc3).NotTo(BeNil())
		Expect(rc3.ID()).To(Equal(uint8(3)))
		Expect(rc3.nodeName).To(Equal("node-b"))

		Expect(p.Replica(5)).To(BeNil())
	})

	It("collects IDs from RVRs", func() {
		rv := &v1alpha1.ReplicatedVolume{Status: v1alpha1.ReplicatedVolumeStatus{Configuration: minimalConfig}}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			{ObjectMeta: metav1.ObjectMeta{Name: "rv-1-2"}, Spec: v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-c"}},
		}

		p := buildContexts(rv, nil, rvrs, nil, FeatureFlags{})

		rc2 := p.Replica(2)
		Expect(rc2).NotTo(BeNil())
		Expect(rc2.ID()).To(Equal(uint8(2)))
		Expect(rc2.Name()).To(Equal("rv-1-2"))
		Expect(rc2.nodeName).To(Equal("node-c"))
		Expect(rc2.rvr).To(Equal(rvrs[0]))
		Expect(rc2.Exists()).To(BeTrue())
	})

	It("collects IDs from Requests", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: minimalConfig,
				DatameshReplicaRequests: []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
					{Name: "rv-1-7"},
				},
			},
		}

		p := buildContexts(rv, nil, nil, nil, FeatureFlags{})

		rc7 := p.Replica(7)
		Expect(rc7).NotTo(BeNil())
		Expect(rc7.ID()).To(Equal(uint8(7)))
		Expect(rc7.Name()).To(Equal("rv-1-7"))
		Expect(rc7.membershipRequest).NotTo(BeNil())
	})

	It("collects IDs from Transitions", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: minimalConfig,
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					{ReplicaName: "rv-1-4", Type: "AddReplica", Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
						{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, StartedAt: ptr.To(metav1.Now())},
					}},
				},
			},
		}

		p := buildContexts(rv, nil, nil, nil, FeatureFlags{})

		rc4 := p.Replica(4)
		Expect(rc4).NotTo(BeNil())
		Expect(rc4.ID()).To(Equal(uint8(4)))
	})

	It("skips global transitions when collecting IDs", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: minimalConfig,
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					{Type: "EnableMultiattach", Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
						{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, StartedAt: ptr.To(metav1.Now())},
					}},
				},
			},
		}

		p := buildContexts(rv, nil, nil, nil, FeatureFlags{})

		// No replica contexts should be created from global transitions.
		for id := range uint8(32) {
			Expect(p.Replica(id)).To(BeNil())
		}
	})

	It("populates RVR, Member, and Request on same replica", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: minimalConfig,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-5", NodeName: "node-x", Type: v1alpha1.DatameshMemberTypeAccess},
					},
				},
				DatameshReplicaRequests: []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
					{Name: "rv-1-5"},
				},
			},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			{ObjectMeta: metav1.ObjectMeta{Name: "rv-1-5"}, Spec: v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-x"}},
		}

		p := buildContexts(rv, nil, rvrs, nil, FeatureFlags{})

		rc5 := p.Replica(5)
		Expect(rc5).NotTo(BeNil())
		Expect(rc5.rvr).NotTo(BeNil())
		Expect(rc5.member).NotTo(BeNil())
		Expect(rc5.membershipRequest).NotTo(BeNil())
	})

	It("NodeName from RVR takes priority over Member", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: minimalConfig,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-0", NodeName: "member-node"},
					},
				},
			},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			{ObjectMeta: metav1.ObjectMeta{Name: "rv-1-0"}, Spec: v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "rvr-node"}},
		}

		p := buildContexts(rv, nil, rvrs, nil, FeatureFlags{})

		rc0 := p.Replica(0)
		Expect(rc0.nodeName).To(Equal("rvr-node"))
	})

	It("Name priority: RVR > Member > Request", func() {
		// Only Member and Request (no RVR) — Member wins.
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: minimalConfig,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-2", NodeName: "node-a"},
					},
				},
				DatameshReplicaRequests: []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
					{Name: "rv-1-2"},
				},
			},
		}

		p := buildContexts(rv, nil, nil, nil, FeatureFlags{})
		rc2 := p.Replica(2)
		Expect(rc2.Name()).To(Equal("rv-1-2"))
	})

	It("assigns RVAs by NodeName", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: minimalConfig,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-0", NodeName: "node-a"},
					},
				},
			},
		}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{
			{Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{NodeName: "node-a"}},
		}

		p := buildContexts(rv, nil, nil, rvas, FeatureFlags{})

		rc0 := p.Replica(0)
		Expect(rc0.rvas).To(HaveLen(1))
	})

	It("appends orphan RVA nodes", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: minimalConfig,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-0", NodeName: "node-a"},
					},
				},
			},
		}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{
			{Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{NodeName: "node-orphan"}},
		}

		p := buildContexts(rv, nil, nil, rvas, FeatureFlags{})

		gctx := p.Global()

		// Orphan is in allReplicas but not in ID index.
		Expect(gctx.allReplicas).To(HaveLen(2))
		found := false
		for i := range gctx.allReplicas {
			if gctx.allReplicas[i].nodeName == "node-orphan" {
				Expect(gctx.allReplicas[i].rvas).To(HaveLen(1))
				found = true
			}
		}
		Expect(found).To(BeTrue())

		// Known replica still accessible by ID.
		Expect(p.Replica(0)).NotTo(BeNil())
		Expect(p.Replica(0).nodeName).To(Equal("node-a"))
	})

	It("transitions are NOT populated on ReplicaContexts", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: minimalConfig,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-0", NodeName: "node-a"},
					},
				},
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					{
						ReplicaName: "rv-1-0",
						Type:        "AddReplica",
						Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership,
						Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
							{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, StartedAt: ptr.To(metav1.Now())},
						},
					},
				},
			},
		}

		p := buildContexts(rv, nil, nil, nil, FeatureFlags{})

		rc0 := p.Replica(0)
		Expect(rc0.membershipTransition).To(BeNil())
		Expect(rc0.attachmentTransition).To(BeNil())
	})

	It("GlobalContext is populated", func() {
		rv := &v1alpha1.ReplicatedVolume{Status: v1alpha1.ReplicatedVolumeStatus{Configuration: minimalConfig}}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{{ObjectMeta: metav1.ObjectMeta{Name: "rv-1-0"}, Spec: v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "n"}}}

		p := buildContexts(rv, nil, rvrs, nil, FeatureFlags{ShadowDiskful: true})

		gctx := p.Global()
		Expect(gctx).NotTo(BeNil())
		Expect(gctx.features.ShadowDiskful).To(BeTrue())
	})

	It("handles empty state", func() {
		rv := &v1alpha1.ReplicatedVolume{Status: v1alpha1.ReplicatedVolumeStatus{Configuration: minimalConfig}}
		p := buildContexts(rv, nil, nil, nil, FeatureFlags{})

		gctx := p.Global()
		Expect(gctx).NotTo(BeNil())
		Expect(gctx.allReplicas).To(BeEmpty())
		for id := range uint8(32) {
			Expect(p.Replica(id)).To(BeNil())
		}
	})

	It("Members point into rv.Status.Datamesh.Members slice", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: minimalConfig,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-0", NodeName: "node-a", Type: v1alpha1.DatameshMemberTypeDiskful},
						{Name: "rv-1-3", NodeName: "node-b", Type: v1alpha1.DatameshMemberTypeAccess},
					},
				},
			},
		}

		p := buildContexts(rv, nil, nil, nil, FeatureFlags{})

		rc0 := p.Replica(0)
		rc3 := p.Replica(3)

		// Member pointers point into the original slice (stable during engine processing).
		Expect(rc0.member).To(BeIdenticalTo(&rv.Status.Datamesh.Members[0]))
		Expect(rc3.member).To(BeIdenticalTo(&rv.Status.Datamesh.Members[1]))
	})

	It("rebuilds ID index when orphan RVA causes backing array reallocation", func() {
		// Two known IDs fill the initial capacity; two orphan nodes force reallocation.
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: minimalConfig,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-0", NodeName: "node-a"},
						{Name: "rv-1-1", NodeName: "node-b"},
					},
				},
			},
		}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{
			{Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{NodeName: "node-orphan-1"}},
			{Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{NodeName: "node-orphan-2"}},
		}

		p := buildContexts(rv, nil, nil, rvas, FeatureFlags{})

		// Known replicas still accessible by ID after reallocation.
		rc0 := p.Replica(0)
		Expect(rc0).NotTo(BeNil())
		Expect(rc0.nodeName).To(Equal("node-a"))

		rc1 := p.Replica(1)
		Expect(rc1).NotTo(BeNil())
		Expect(rc1.nodeName).To(Equal("node-b"))

		// Orphans are in allReplicas.
		gctx := p.Global()
		Expect(gctx.allReplicas).To(HaveLen(4))
	})

	It("Requests point into rv.Status.DatameshReplicaRequests slice", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: minimalConfig,
				DatameshReplicaRequests: []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
					{Name: "rv-1-2"},
				},
			},
		}

		p := buildContexts(rv, nil, nil, nil, FeatureFlags{})

		rc2 := p.Replica(2)
		Expect(rc2.membershipRequest).To(BeIdenticalTo(&rv.Status.DatameshReplicaRequests[0]))
	})

	It("sets gctx on all ReplicaContexts including orphans", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: minimalConfig,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-0", NodeName: "node-a"},
					},
				},
			},
		}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{
			{Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{NodeName: "node-orphan"}},
		}

		p := buildContexts(rv, nil, nil, rvas, FeatureFlags{})

		gctx := p.Global()
		for i := range gctx.allReplicas {
			Expect(gctx.allReplicas[i].gctx).To(BeIdenticalTo(gctx))
		}
	})
})

var _ = Describe("writebackMembersFromContexts", func() {
	It("fast path: no changes when set and pointers match", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: minimalConfig,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-0", NodeName: "node-a", Type: v1alpha1.DatameshMemberTypeDiskful},
					},
				},
			},
		}
		p := buildContexts(rv, nil, nil, nil, FeatureFlags{})

		writebackMembersFromContexts(rv, p.Global())

		Expect(rv.Status.Datamesh.Members).To(HaveLen(1))
		Expect(rv.Status.Datamesh.Members[0].Name).To(Equal("rv-1-0"))
	})

	It("member added: new heap-allocated member appended", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: minimalConfig,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-0", NodeName: "node-a", Type: v1alpha1.DatameshMemberTypeDiskful},
					},
				},
			},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			{ObjectMeta: metav1.ObjectMeta{Name: "rv-1-0"}, Spec: v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-a"}},
			{ObjectMeta: metav1.ObjectMeta{Name: "rv-1-3"}, Spec: v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-b"}},
		}
		p := buildContexts(rv, nil, rvrs, nil, FeatureFlags{})

		// Simulate apply: add member for replica 3.
		rc3 := p.Replica(3)
		rc3.member = &v1alpha1.DatameshMember{Name: "rv-1-3", NodeName: "node-b", Type: v1alpha1.DatameshMemberTypeAccess}

		writebackMembersFromContexts(rv, p.Global())

		Expect(rv.Status.Datamesh.Members).To(HaveLen(2))
		Expect(rv.Status.Datamesh.Members[0].Name).To(Equal("rv-1-0"))
		Expect(rv.Status.Datamesh.Members[1].Name).To(Equal("rv-1-3"))
	})

	It("member removed: member set to nil", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: minimalConfig,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-0", NodeName: "node-a", Type: v1alpha1.DatameshMemberTypeDiskful},
						{Name: "rv-1-3", NodeName: "node-b", Type: v1alpha1.DatameshMemberTypeAccess},
					},
				},
			},
		}
		p := buildContexts(rv, nil, nil, nil, FeatureFlags{})

		// Simulate apply: remove member for replica 3.
		rc3 := p.Replica(3)
		rc3.member = nil

		writebackMembersFromContexts(rv, p.Global())

		Expect(rv.Status.Datamesh.Members).To(HaveLen(1))
		Expect(rv.Status.Datamesh.Members[0].Name).To(Equal("rv-1-0"))
	})

	It("member replaced: pointer changed to new heap object", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: minimalConfig,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-0", NodeName: "node-a", Type: v1alpha1.DatameshMemberTypeAccess},
					},
				},
			},
		}
		p := buildContexts(rv, nil, nil, nil, FeatureFlags{})

		// Simulate apply: replace member 0 with a new object (e.g., type change).
		rc0 := p.Replica(0)
		rc0.member = &v1alpha1.DatameshMember{Name: "rv-1-0", NodeName: "node-a", Type: v1alpha1.DatameshMemberTypeDiskful}

		writebackMembersFromContexts(rv, p.Global())

		Expect(rv.Status.Datamesh.Members).To(HaveLen(1))
		Expect(rv.Status.Datamesh.Members[0].Type).To(Equal(v1alpha1.DatameshMemberTypeDiskful))
	})

	It("mixed: one surviving, one removed, one added", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: minimalConfig,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-0", NodeName: "node-a", Type: v1alpha1.DatameshMemberTypeDiskful},
						{Name: "rv-1-1", NodeName: "node-b", Type: v1alpha1.DatameshMemberTypeAccess},
					},
				},
			},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			{ObjectMeta: metav1.ObjectMeta{Name: "rv-1-0"}, Spec: v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-a"}},
			{ObjectMeta: metav1.ObjectMeta{Name: "rv-1-1"}, Spec: v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-b"}},
			{ObjectMeta: metav1.ObjectMeta{Name: "rv-1-5"}, Spec: v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-c"}},
		}
		p := buildContexts(rv, nil, rvrs, nil, FeatureFlags{})

		// Remove replica 1, add replica 5, keep replica 0.
		p.Replica(1).member = nil
		p.Replica(5).member = &v1alpha1.DatameshMember{Name: "rv-1-5", NodeName: "node-c", Type: v1alpha1.DatameshMemberTypeAccess}

		writebackMembersFromContexts(rv, p.Global())

		Expect(rv.Status.Datamesh.Members).To(HaveLen(2))
		Expect(rv.Status.Datamesh.Members[0].Name).To(Equal("rv-1-0"))
		Expect(rv.Status.Datamesh.Members[1].Name).To(Equal("rv-1-5"))
	})
})

var _ = Describe("writebackDatameshFromContext", func() {
	It("writes scalar parameters back to rv", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: minimalConfig,
			},
		}
		p := buildContexts(rv, nil, nil, nil, FeatureFlags{})

		gctx := p.Global()
		gctx.datamesh.Quorum = 3
		gctx.datamesh.QuorumMinimumRedundancy = 2
		gctx.datamesh.Multiattach = true

		writebackDatameshFromContext(rv, gctx)

		Expect(rv.Status.Datamesh.Quorum).To(Equal(byte(3)))
		Expect(rv.Status.Datamesh.QuorumMinimumRedundancy).To(Equal(byte(2)))
		Expect(rv.Status.Datamesh.Multiattach).To(BeTrue())
	})
})

var _ = Describe("writebackRequestMessagesFromContexts", func() {
	It("returns false when no requests exist", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{Configuration: minimalConfig},
		}
		p := buildContexts(rv, nil, nil, nil, FeatureFlags{})

		Expect(writebackRequestMessagesFromContexts(p.Global())).To(BeFalse())
	})

	It("returns false when message is unchanged", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: minimalConfig,
				DatameshReplicaRequests: []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
					{Name: "rv-1-2", Message: "existing"},
				},
			},
		}
		p := buildContexts(rv, nil, nil, nil, FeatureFlags{})

		// Set same message via slot output field.
		p.Replica(2).membershipMessage = "existing"

		Expect(writebackRequestMessagesFromContexts(p.Global())).To(BeFalse())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(Equal("existing"))
	})

	It("returns true and updates when message changed", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: minimalConfig,
				DatameshReplicaRequests: []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
					{Name: "rv-1-2", Message: "old"},
				},
			},
		}
		p := buildContexts(rv, nil, nil, nil, FeatureFlags{})

		p.Replica(2).membershipMessage = "Joining datamesh: 2/4 replicas confirmed revision 7"

		Expect(writebackRequestMessagesFromContexts(p.Global())).To(BeTrue())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(Equal("Joining datamesh: 2/4 replicas confirmed revision 7"))
	})

	It("handles mixed: some changed, some unchanged", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: minimalConfig,
				DatameshReplicaRequests: []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
					{Name: "rv-1-0", Message: "stable"},
					{Name: "rv-1-5", Message: "old"},
				},
			},
		}
		p := buildContexts(rv, nil, nil, nil, FeatureFlags{})

		p.Replica(0).membershipMessage = "stable"
		p.Replica(5).membershipMessage = "updated"

		Expect(writebackRequestMessagesFromContexts(p.Global())).To(BeTrue())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(Equal("stable"))
		Expect(rv.Status.DatameshReplicaRequests[1].Message).To(Equal("updated"))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// isQuorumSatisfied / computeQuorumCheck
//

var _ = Describe("isQuorumSatisfied", func() {
	It("satisfied when voter has quorum", func() {
		gctx := &globalContext{
			allReplicas: []ReplicaContext{
				{id: 0, member: &v1alpha1.DatameshMember{Name: "rv-1-0", Type: v1alpha1.DatameshMemberTypeDiskful},
					rvr: &v1alpha1.ReplicatedVolumeReplica{Status: v1alpha1.ReplicatedVolumeReplicaStatus{Quorum: ptr.To(true)}}},
			},
		}

		Expect(gctx.isQuorumSatisfied()).To(BeTrue())
	})

	It("not satisfied when voter has no quorum", func() {
		gctx := &globalContext{
			allReplicas: []ReplicaContext{
				{id: 0, member: &v1alpha1.DatameshMember{Name: "rv-1-0", Type: v1alpha1.DatameshMemberTypeDiskful},
					rvr: &v1alpha1.ReplicatedVolumeReplica{Status: v1alpha1.ReplicatedVolumeReplicaStatus{Quorum: ptr.To(false)}}},
			},
		}

		Expect(gctx.isQuorumSatisfied()).To(BeFalse())
		Expect(gctx.quorumDiagnostic).To(ContainSubstring("no quorum"))
	})

	It("not satisfied when no voter members", func() {
		gctx := &globalContext{
			allReplicas: []ReplicaContext{
				{id: 0, member: &v1alpha1.DatameshMember{Name: "rv-1-0", Type: v1alpha1.DatameshMemberTypeAccess}},
			},
		}

		Expect(gctx.isQuorumSatisfied()).To(BeFalse())
		Expect(gctx.quorumDiagnostic).To(Equal("no voter members"))
	})

	It("skips agent-not-ready voters", func() {
		gctx := &globalContext{
			allReplicas: []ReplicaContext{
				{id: 0, member: &v1alpha1.DatameshMember{Name: "rv-1-0", Type: v1alpha1.DatameshMemberTypeDiskful},
					rvr: &v1alpha1.ReplicatedVolumeReplica{
						Status: v1alpha1.ReplicatedVolumeReplicaStatus{
							Quorum: ptr.To(true),
							Conditions: []metav1.Condition{{
								Type:   v1alpha1.ReplicatedVolumeReplicaCondReadyType,
								Reason: v1alpha1.ReplicatedVolumeReplicaCondReadyReasonAgentNotReady,
							}},
						},
					}},
			},
		}

		Expect(gctx.isQuorumSatisfied()).To(BeFalse())
		Expect(gctx.quorumDiagnostic).To(ContainSubstring("agent not ready"))
	})

	It("mixed: one voter no quorum, one agent-not-ready", func() {
		gctx := &globalContext{
			allReplicas: []ReplicaContext{
				{id: 0, member: &v1alpha1.DatameshMember{Name: "rv-1-0", Type: v1alpha1.DatameshMemberTypeDiskful},
					rvr: &v1alpha1.ReplicatedVolumeReplica{Status: v1alpha1.ReplicatedVolumeReplicaStatus{Quorum: ptr.To(false)}}},
				{id: 1, member: &v1alpha1.DatameshMember{Name: "rv-1-1", Type: v1alpha1.DatameshMemberTypeDiskful},
					rvr: &v1alpha1.ReplicatedVolumeReplica{
						Status: v1alpha1.ReplicatedVolumeReplicaStatus{
							Conditions: []metav1.Condition{{
								Type:   v1alpha1.ReplicatedVolumeReplicaCondReadyType,
								Reason: v1alpha1.ReplicatedVolumeReplicaCondReadyReasonAgentNotReady,
							}},
						},
					}},
			},
		}

		Expect(gctx.isQuorumSatisfied()).To(BeFalse())
		Expect(gctx.quorumDiagnostic).To(ContainSubstring("no quorum"))
		Expect(gctx.quorumDiagnostic).To(ContainSubstring("agent not ready"))
	})

	It("caches result", func() {
		gctx := &globalContext{
			allReplicas: []ReplicaContext{
				{id: 0, member: &v1alpha1.DatameshMember{Name: "rv-1-0", Type: v1alpha1.DatameshMemberTypeDiskful},
					rvr: &v1alpha1.ReplicatedVolumeReplica{Status: v1alpha1.ReplicatedVolumeReplicaStatus{Quorum: ptr.To(true)}}},
			},
		}

		Expect(gctx.isQuorumSatisfied()).To(BeTrue())
		Expect(gctx.quorumSatisfied).NotTo(BeNil())

		// Mutate allReplicas to break quorum — cached value should still be true.
		gctx.allReplicas[0].rvr.Status.Quorum = ptr.To(false)
		Expect(gctx.isQuorumSatisfied()).To(BeTrue())
	})

	It("voter without RVR is skipped", func() {
		gctx := &globalContext{
			allReplicas: []ReplicaContext{
				{id: 0, member: &v1alpha1.DatameshMember{Name: "rv-1-0", Type: v1alpha1.DatameshMemberTypeDiskful}},
			},
		}

		Expect(gctx.isQuorumSatisfied()).To(BeFalse())
		Expect(gctx.quorumDiagnostic).To(Equal("no voter members"))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// getEligibleNode
//

var _ = Describe("getEligibleNode", func() {
	It("returns eligible node when found", func() {
		gctx := &globalContext{rsp: mkRSP("node-a", "node-b")}
		rc := &ReplicaContext{gctx: gctx, nodeName: "node-a"}

		en := rc.getEligibleNode()
		Expect(en).NotTo(BeNil())
		Expect(en.NodeName).To(Equal("node-a"))
	})

	It("returns nil when node not in RSP", func() {
		gctx := &globalContext{rsp: mkRSP("node-a")}
		rc := &ReplicaContext{gctx: gctx, nodeName: "node-x"}

		Expect(rc.getEligibleNode()).To(BeNil())
	})

	It("returns nil when RSP is nil", func() {
		gctx := &globalContext{rsp: nil}
		rc := &ReplicaContext{gctx: gctx, nodeName: "node-a"}

		Expect(rc.getEligibleNode()).To(BeNil())
	})

	It("caches result", func() {
		gctx := &globalContext{rsp: mkRSP("node-a")}
		rc := &ReplicaContext{gctx: gctx, nodeName: "node-a"}

		en1 := rc.getEligibleNode()
		en2 := rc.getEligibleNode()
		Expect(en1).To(BeIdenticalTo(en2))
		Expect(rc.eligibleNodeChecked).To(BeTrue())
	})
})
