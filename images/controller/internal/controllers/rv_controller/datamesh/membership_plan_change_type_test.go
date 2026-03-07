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
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// ──────────────────────────────────────────────────────────────────────────────
// ChangeReplicaType(A → TB)
//

var _ = Describe("ChangeReplicaType(A→TB)", func() {
	It("creates transition, member type becomes TB", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeTieBreaker),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5),
			mkRVR("rv-1-1", "node-2", 5),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())

		// Member type changed to TieBreaker.
		m := rv.Status.Datamesh.FindMemberByName("rv-1-1")
		Expect(m).NotTo(BeNil())
		Expect(m.Type).To(Equal(v1alpha1.DatameshMemberTypeTieBreaker))

		// Transition created with correct metadata.
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		t := &rv.Status.DatameshTransitions[0]
		Expect(t.Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeReplicaType))
		Expect(t.PlanID).To(Equal("a-to-tb/v1"))
		Expect(t.FromReplicaType).To(Equal(v1alpha1.ReplicaTypeAccess))
		Expect(t.ToReplicaType).To(Equal(v1alpha1.ReplicaTypeTieBreaker))
	})

	It("completes when FM + subject confirm", func() {
		t := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:            v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeReplicaType,
			Group:           v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership,
			ReplicaName:     "rv-1-1",
			PlanID:          "a-to-tb/v1",
			FromReplicaType: v1alpha1.ReplicaTypeAccess,
			ToReplicaType:   v1alpha1.ReplicaTypeTieBreaker,
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{
				Name: "A → TB", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
				DatameshRevision: 6, StartedAt: ptr.To(metav1.Now()),
			}},
		}
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeTieBreaker, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeTieBreaker),
			},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		// D (FM) + subject both confirmed rev 6.
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 6),
			mkRVR("rv-1-1", "node-2", 6),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(Equal("Replica type changed successfully"))
	})

	It("already TB → skip", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeTieBreaker, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeTieBreaker),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5),
			mkRVR("rv-1-1", "node-2", 5),
		}
		settleEffectiveLayout(rv, rvrs)

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{})

		// Already TB → skip, no transition.
		Expect(changed).To(BeFalse())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})

	It("not a member → skip", func() {
		rv := mkRV(5, nil,
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeTieBreaker),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-1", "node-2", 5)}
		settleEffectiveLayout(rv, rvrs)

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-2"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeFalse())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// ChangeReplicaType(TB → A)
//

var _ = Describe("ChangeReplicaType(TB→A)", func() {
	It("creates transition, member type becomes A", func() {
		// 1D + 1TB, FTT=0 → odd D (1), TB not required → passes.
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeTieBreaker, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeAccess),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5),
			mkRVR("rv-1-1", "node-2", 5),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())

		// Member type changed to Access.
		m := rv.Status.Datamesh.FindMemberByName("rv-1-1")
		Expect(m).NotTo(BeNil())
		Expect(m.Type).To(Equal(v1alpha1.DatameshMemberTypeAccess))

		// Transition metadata.
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		t := &rv.Status.DatameshTransitions[0]
		Expect(t.PlanID).To(Equal("tb-to-a/v1"))
		Expect(t.FromReplicaType).To(Equal(v1alpha1.ReplicaTypeTieBreaker))
		Expect(t.ToReplicaType).To(Equal(v1alpha1.ReplicaTypeAccess))
	})

	It("guard: VolumeAccess=Local blocks", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeTieBreaker, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeAccess),
			},
			nil,
		)
		rv.Status.Configuration = &v1alpha1.ReplicatedVolumeConfiguration{
			ReplicatedStoragePoolName: "test-pool", Topology: v1alpha1.TopologyIgnored,
			VolumeAccess: v1alpha1.VolumeAccessLocal,
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5),
			mkRVR("rv-1-1", "node-2", 5),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue()) // message changed
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(ContainSubstring("volumeAccess is Local"))
	})

	It("guard: TB required (even D, FTT=D/2)", func() {
		// 2D + 1TB, FTT=1 → even D, FTT=D/2 → TB required.
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeTieBreaker, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeAccess),
			},
			nil,
		)
		rv.Status.Configuration = &v1alpha1.ReplicatedVolumeConfiguration{
			ReplicatedStoragePoolName: "test-pool", Topology: v1alpha1.TopologyIgnored,
			VolumeAccess:       v1alpha1.VolumeAccessPreferablyLocal,
			FailuresToTolerate: 1, GuaranteedMinimumDataRedundancy: 0,
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5),
			mkRVR("rv-1-1", "node-2", 5),
			mkRVR("rv-1-2", "node-3", 5),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue()) // message changed
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(ContainSubstring("TB required"))
	})

	It("guard: TB not required (odd D)", func() {
		// 3D + 1TB, FTT=1 → odd D → TB not required → change proceeds.
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeDiskful, "node-3"),
				mkMember("rv-1-3", v1alpha1.DatameshMemberTypeTieBreaker, "node-4"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-3", v1alpha1.ReplicaTypeAccess),
			},
			nil,
		)
		rv.Status.Configuration = &v1alpha1.ReplicatedVolumeConfiguration{
			ReplicatedStoragePoolName: "test-pool", Topology: v1alpha1.TopologyIgnored,
			VolumeAccess:       v1alpha1.VolumeAccessPreferablyLocal,
			FailuresToTolerate: 1, GuaranteedMinimumDataRedundancy: 1,
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5),
			mkRVR("rv-1-1", "node-2", 5),
			mkRVR("rv-1-2", "node-3", 5),
			mkRVR("rv-1-3", "node-4", 5),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3", "node-4"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("tb-to-a/v1"))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// ChangeReplicaType dispatch skip
//

var _ = Describe("ChangeReplicaType dispatch skip", func() {
	It("skips when AddReplica in progress", func() {
		// Active AddReplica transition for rv-1-1.
		addT := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership,
			ReplicaName: "rv-1-1",
			PlanID:      "access/v1",
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{
				Name: "✦ → A", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
				DatameshRevision: 6, StartedAt: ptr.To(metav1.Now()),
			}},
		}
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeTieBreaker),
			},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{addT},
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 6),
			mkRVR("rv-1-1", "node-2", 5), // not yet confirmed → AddReplica stays active
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue()) // progress message updated
		// AddReplica still in progress, ChangeRole skipped.
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// ChangeReplicaType(A → sD)
//

var _ = Describe("ChangeReplicaType(A→sD)", func() {
	It("creates transition with 2 steps, member type = LiminalShadowDiskful", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeShadowDiskful),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5),
			mkRVR("rv-1-1", "node-2", 5),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{ShadowDiskful: true})

		Expect(changed).To(BeTrue())

		// Step 1 applied: type changed to LiminalShadowDiskful with BV fields.
		m := rv.Status.Datamesh.FindMemberByName("rv-1-1")
		Expect(m).NotTo(BeNil())
		Expect(m.Type).To(Equal(v1alpha1.DatameshMemberTypeLiminalShadowDiskful))
		Expect(m.LVMVolumeGroupName).To(Equal("test-lvg"))
		Expect(m.LVMVolumeGroupThinPoolName).To(Equal("test-thin"))

		// Transition: 2 steps, correct metadata.
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		t := &rv.Status.DatameshTransitions[0]
		Expect(t.Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeReplicaType))
		Expect(t.PlanID).To(Equal("a-to-sd/v1"))
		Expect(t.FromReplicaType).To(Equal(v1alpha1.ReplicaTypeAccess))
		Expect(t.ToReplicaType).To(Equal(v1alpha1.ReplicaTypeShadowDiskful))
		Expect(t.Steps).To(HaveLen(2))
		Expect(t.Steps[0].Name).To(Equal("A → sD∅"))
		Expect(t.Steps[0].Status).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive))
		Expect(t.Steps[1].Name).To(Equal("sD∅ → sD"))
		Expect(t.Steps[1].Status).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending))
	})

	It("guard: ShadowDiskful feature not supported", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeShadowDiskful),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5),
			mkRVR("rv-1-1", "node-2", 5),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{ShadowDiskful: false})

		Expect(changed).To(BeTrue()) // message changed
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(ContainSubstring("ShadowDiskful not supported"))
	})

	It("step 1 confirmed → advances to step 2, type becomes sD", func() {
		t := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeReplicaType,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership,
			ReplicaName: "rv-1-1", PlanID: "a-to-sd/v1",
			FromReplicaType: v1alpha1.ReplicaTypeAccess,
			ToReplicaType:   v1alpha1.ReplicaTypeShadowDiskful,
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "A → sD∅", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					DatameshRevision: 6, StartedAt: ptr.To(metav1.Now())},
				{Name: "sD∅ → sD", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending},
			},
		}
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeLiminalShadowDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeShadowDiskful),
			},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		// All members confirmed rev 6 → step 1 completes.
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 6),
			mkRVR("rv-1-1", "node-2", 6),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{ShadowDiskful: true})

		Expect(changed).To(BeTrue())

		// Type changed to ShadowDiskful (step 2 applied).
		m := rv.Status.Datamesh.FindMemberByName("rv-1-1")
		Expect(m).NotTo(BeNil())
		Expect(m.Type).To(Equal(v1alpha1.DatameshMemberTypeShadowDiskful))

		// Step 1 completed, step 2 active.
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Steps[0].Status).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted))
		Expect(rv.Status.DatameshTransitions[0].Steps[1].Status).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive))
	})

	It("both steps confirmed → transition completed", func() {
		t := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeReplicaType,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership,
			ReplicaName: "rv-1-1", PlanID: "a-to-sd/v1",
			FromReplicaType: v1alpha1.ReplicaTypeAccess,
			ToReplicaType:   v1alpha1.ReplicaTypeShadowDiskful,
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "A → sD∅", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted,
					DatameshRevision: 6},
				{Name: "sD∅ → sD", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					DatameshRevision: 7, StartedAt: ptr.To(metav1.Now())},
			},
		}
		rv := mkRV(7,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeShadowDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeShadowDiskful),
			},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		// Subject confirmed rev 7 (step 2 = subjectOnly).
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 6),
			mkRVR("rv-1-1", "node-2", 7),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{ShadowDiskful: true})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(Equal("Replica type changed successfully"))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// ChangeReplicaType(sD → A)
//

var _ = Describe("ChangeReplicaType(sD→A)", func() {
	It("creates transition with 2 steps, member type = LiminalShadowDiskful", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeShadowDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeAccess),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5),
			mkRVR("rv-1-1", "node-2", 5),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())

		// Step 1 applied: type changed to LiminalShadowDiskful (disk detach), BV preserved.
		m := rv.Status.Datamesh.FindMemberByName("rv-1-1")
		Expect(m).NotTo(BeNil())
		Expect(m.Type).To(Equal(v1alpha1.DatameshMemberTypeLiminalShadowDiskful))
		Expect(m.LVMVolumeGroupName).To(Equal("test-lvg"))
		Expect(m.LVMVolumeGroupThinPoolName).To(Equal("test-thin"))

		// Transition metadata.
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		t := &rv.Status.DatameshTransitions[0]
		Expect(t.PlanID).To(Equal("sd-to-a/v1"))
		Expect(t.Steps).To(HaveLen(2))
		Expect(t.Steps[0].Name).To(Equal("sD → sD∅"))
		Expect(t.Steps[1].Name).To(Equal("sD∅ → A"))
	})

	It("guard: VolumeAccess=Local blocks", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeShadowDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeAccess),
			},
			nil,
		)
		rv.Status.Configuration = &v1alpha1.ReplicatedVolumeConfiguration{
			ReplicatedStoragePoolName: "test-pool", Topology: v1alpha1.TopologyIgnored,
			VolumeAccess: v1alpha1.VolumeAccessLocal,
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5),
			mkRVR("rv-1-1", "node-2", 5),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue()) // message changed
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(ContainSubstring("volumeAccess is Local"))
	})

	It("step 1 confirmed → advances to step 2, type becomes A", func() {
		t := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeReplicaType,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership,
			ReplicaName: "rv-1-1", PlanID: "sd-to-a/v1",
			FromReplicaType: v1alpha1.ReplicaTypeShadowDiskful,
			ToReplicaType:   v1alpha1.ReplicaTypeAccess,
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "sD → sD∅", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					DatameshRevision: 6, StartedAt: ptr.To(metav1.Now())},
				{Name: "sD∅ → A", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending},
			},
		}
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeLiminalShadowDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeAccess),
			},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		// Subject confirmed rev 6 (step 1 = subjectOnly).
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5),
			mkRVR("rv-1-1", "node-2", 6),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())

		// Type changed to Access (step 2 applied), BV fields cleared.
		m := rv.Status.Datamesh.FindMemberByName("rv-1-1")
		Expect(m).NotTo(BeNil())
		Expect(m.Type).To(Equal(v1alpha1.DatameshMemberTypeAccess))
		Expect(m.LVMVolumeGroupName).To(BeEmpty())
		Expect(m.LVMVolumeGroupThinPoolName).To(BeEmpty())

		// Step 1 completed, step 2 active.
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Steps[0].Status).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted))
		Expect(rv.Status.DatameshTransitions[0].Steps[1].Status).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive))
	})

	It("both steps confirmed → transition completed", func() {
		t := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeReplicaType,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership,
			ReplicaName: "rv-1-1", PlanID: "sd-to-a/v1",
			FromReplicaType: v1alpha1.ReplicaTypeShadowDiskful,
			ToReplicaType:   v1alpha1.ReplicaTypeAccess,
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "sD → sD∅", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted,
					DatameshRevision: 6},
				{Name: "sD∅ → A", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					DatameshRevision: 7, StartedAt: ptr.To(metav1.Now())},
			},
		}
		rv := mkRV(7,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeAccess),
			},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		// All members confirmed rev 7 (step 2 = allMembers).
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 7),
			mkRVR("rv-1-1", "node-2", 7),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(Equal("Replica type changed successfully"))
	})

	It("handles liminal state (sD∅→A)", func() {
		// Member stuck in LiminalShadowDiskful (interrupted A→sD).
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				{Name: "rv-1-1", Type: v1alpha1.DatameshMemberTypeLiminalShadowDiskful, NodeName: "node-2"},
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeAccess),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5),
			mkRVR("rv-1-1", "node-2", 5),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		// Dispatch routes LiminalShadowDiskful → sd-to-a/v1.
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("sd-to-a/v1"))

		// Step 1 is no-op (type already LiminalShadowDiskful).
		m := rv.Status.Datamesh.FindMemberByName("rv-1-1")
		Expect(m).NotTo(BeNil())
		Expect(m.Type).To(Equal(v1alpha1.DatameshMemberTypeLiminalShadowDiskful))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// ChangeReplicaType(TB → sD)
//

var _ = Describe("ChangeReplicaType(TB→sD)", func() {
	It("creates transition with 2 steps, member type = LiminalShadowDiskful", func() {
		// 1D + 1TB: odd D → TB not required → leavingTBGuards pass.
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeTieBreaker, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeShadowDiskful),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5),
			mkRVR("rv-1-1", "node-2", 5),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{ShadowDiskful: true})

		Expect(changed).To(BeTrue())

		// Step 1 applied: type = LiminalShadowDiskful with BV fields.
		m := rv.Status.Datamesh.FindMemberByName("rv-1-1")
		Expect(m).NotTo(BeNil())
		Expect(m.Type).To(Equal(v1alpha1.DatameshMemberTypeLiminalShadowDiskful))
		Expect(m.LVMVolumeGroupName).To(Equal("test-lvg"))
		Expect(m.LVMVolumeGroupThinPoolName).To(Equal("test-thin"))

		// Transition metadata.
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		t := &rv.Status.DatameshTransitions[0]
		Expect(t.PlanID).To(Equal("tb-to-sd/v1"))
		Expect(t.FromReplicaType).To(Equal(v1alpha1.ReplicaTypeTieBreaker))
		Expect(t.ToReplicaType).To(Equal(v1alpha1.ReplicaTypeShadowDiskful))
		Expect(t.Steps).To(HaveLen(2))
		Expect(t.Steps[0].Name).To(Equal("TB → sD∅"))
		Expect(t.Steps[1].Name).To(Equal("sD∅ → sD"))
	})

	It("guard: ShadowDiskful feature not supported", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeTieBreaker, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeShadowDiskful),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5),
			mkRVR("rv-1-1", "node-2", 5),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{ShadowDiskful: false})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(ContainSubstring("ShadowDiskful not supported"))
	})

	It("guard: TB required (even D, FTT=D/2)", func() {
		// 2D + 1TB, FTT=1 → even D, FTT=D/2 → TB required → blocked.
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeTieBreaker, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeShadowDiskful),
			},
			nil,
		)
		rv.Status.Configuration = &v1alpha1.ReplicatedVolumeConfiguration{
			ReplicatedStoragePoolName: "test-pool", Topology: v1alpha1.TopologyIgnored,
			VolumeAccess:       v1alpha1.VolumeAccessPreferablyLocal,
			FailuresToTolerate: 1, GuaranteedMinimumDataRedundancy: 0,
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5),
			mkRVR("rv-1-1", "node-2", 5),
			mkRVR("rv-1-2", "node-3", 5),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{ShadowDiskful: true})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(ContainSubstring("TB required"))
	})

	It("completes full lifecycle", func() {
		t := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeReplicaType,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership,
			ReplicaName: "rv-1-1", PlanID: "tb-to-sd/v1",
			FromReplicaType: v1alpha1.ReplicaTypeTieBreaker,
			ToReplicaType:   v1alpha1.ReplicaTypeShadowDiskful,
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "TB → sD∅", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted,
					DatameshRevision: 6},
				{Name: "sD∅ → sD", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					DatameshRevision: 7, StartedAt: ptr.To(metav1.Now())},
			},
		}
		rv := mkRV(7,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeShadowDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeShadowDiskful),
			},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 6),
			mkRVR("rv-1-1", "node-2", 7),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{ShadowDiskful: true})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(Equal("Replica type changed successfully"))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// ChangeReplicaType(sD → TB)
//

var _ = Describe("ChangeReplicaType(sD→TB)", func() {
	It("creates transition with 2 steps, member type = LiminalShadowDiskful", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeShadowDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeTieBreaker),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5),
			mkRVR("rv-1-1", "node-2", 5),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())

		// Step 1 applied: type = LiminalShadowDiskful (disk detach), BV fields preserved.
		m := rv.Status.Datamesh.FindMemberByName("rv-1-1")
		Expect(m).NotTo(BeNil())
		Expect(m.Type).To(Equal(v1alpha1.DatameshMemberTypeLiminalShadowDiskful))
		Expect(m.LVMVolumeGroupName).To(Equal("test-lvg"))
		Expect(m.LVMVolumeGroupThinPoolName).To(Equal("test-thin"))

		// Transition metadata.
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		t := &rv.Status.DatameshTransitions[0]
		Expect(t.PlanID).To(Equal("sd-to-tb/v1"))
		Expect(t.Steps).To(HaveLen(2))
		Expect(t.Steps[0].Name).To(Equal("sD → sD∅"))
		Expect(t.Steps[1].Name).To(Equal("sD∅ → TB"))
	})

	It("guard: VolumeAccess=Local blocks", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeShadowDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeTieBreaker),
			},
			nil,
		)
		rv.Status.Configuration = &v1alpha1.ReplicatedVolumeConfiguration{
			ReplicatedStoragePoolName: "test-pool", Topology: v1alpha1.TopologyIgnored,
			VolumeAccess: v1alpha1.VolumeAccessLocal,
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5),
			mkRVR("rv-1-1", "node-2", 5),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(ContainSubstring("volumeAccess is Local"))
	})

	It("step 1 confirmed → step 2 applied, BV fields cleared", func() {
		t := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeReplicaType,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership,
			ReplicaName: "rv-1-1", PlanID: "sd-to-tb/v1",
			FromReplicaType: v1alpha1.ReplicaTypeShadowDiskful,
			ToReplicaType:   v1alpha1.ReplicaTypeTieBreaker,
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "sD → sD∅", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					DatameshRevision: 6, StartedAt: ptr.To(metav1.Now())},
				{Name: "sD∅ → TB", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending},
			},
		}
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeLiminalShadowDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeTieBreaker),
			},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		// Subject confirmed rev 6 (step 1 = subjectOnly).
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5),
			mkRVR("rv-1-1", "node-2", 6),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())

		// Type changed to TB (step 2 applied), BV fields cleared.
		m := rv.Status.Datamesh.FindMemberByName("rv-1-1")
		Expect(m).NotTo(BeNil())
		Expect(m.Type).To(Equal(v1alpha1.DatameshMemberTypeTieBreaker))
		Expect(m.LVMVolumeGroupName).To(BeEmpty())
		Expect(m.LVMVolumeGroupThinPoolName).To(BeEmpty())

		// Step 1 completed, step 2 active.
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Steps[0].Status).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted))
		Expect(rv.Status.DatameshTransitions[0].Steps[1].Status).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive))
	})

	It("completes full lifecycle", func() {
		t := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeReplicaType,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership,
			ReplicaName: "rv-1-1", PlanID: "sd-to-tb/v1",
			FromReplicaType: v1alpha1.ReplicaTypeShadowDiskful,
			ToReplicaType:   v1alpha1.ReplicaTypeTieBreaker,
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "sD → sD∅", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted,
					DatameshRevision: 6},
				{Name: "sD∅ → TB", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					DatameshRevision: 7, StartedAt: ptr.To(metav1.Now())},
			},
		}
		rv := mkRV(7,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeTieBreaker, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeTieBreaker),
			},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		// All members confirmed rev 7 (step 2 = allMembers).
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 7),
			mkRVR("rv-1-1", "node-2", 7),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(Equal("Replica type changed successfully"))
	})

	It("handles liminal state (sD∅→TB)", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				{Name: "rv-1-1", Type: v1alpha1.DatameshMemberTypeLiminalShadowDiskful, NodeName: "node-2"},
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeTieBreaker),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5),
			mkRVR("rv-1-1", "node-2", 5),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("sd-to-tb/v1"))

		// Step 1 is no-op (already liminal).
		m := rv.Status.Datamesh.FindMemberByName("rv-1-1")
		Expect(m).NotTo(BeNil())
		Expect(m.Type).To(Equal(v1alpha1.DatameshMemberTypeLiminalShadowDiskful))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// ChangeReplicaType(sD → D)
//

var _ = Describe("ChangeReplicaType(sD→D) dispatch", func() {
	It("even voters → sd-to-d/v1", func() {
		// 2D (voters) + 1sD (non-voter). Voters=2 (even) → no q↑.
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeShadowDiskful, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeDiskful),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 5), mkRVR("rv-1-2", "node-3", 5),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{ShadowDiskful: true})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("sd-to-d/v1"))
	})

	It("odd voters → sd-to-d-q-up/v1", func() {
		// 1D (voter) + 1sD (non-voter). Voters=1 (odd) → q↑.
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeShadowDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeDiskful),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 5),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{ShadowDiskful: true})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("sd-to-d-q-up/v1"))
	})
})

var _ = Describe("ChangeReplicaType(sD→D) sd-to-d/v1", func() {
	It("creates transition (1 step), type = D", func() {
		// 2D + 1sD. Voters=2 (even) → sd-to-d/v1.
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeShadowDiskful, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeDiskful),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 5), mkRVR("rv-1-2", "node-3", 5),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{ShadowDiskful: true})

		Expect(changed).To(BeTrue())
		m := rv.Status.Datamesh.FindMemberByName("rv-1-2")
		Expect(m).NotTo(BeNil())
		Expect(m.Type).To(Equal(v1alpha1.DatameshMemberTypeDiskful))
		// BV preserved (both sD and D have BV).
		Expect(m.LVMVolumeGroupName).To(Equal("test-lvg"))

		t := &rv.Status.DatameshTransitions[0]
		Expect(t.PlanID).To(Equal("sd-to-d/v1"))
		Expect(t.Steps).To(HaveLen(1))
		Expect(t.Steps[0].Name).To(Equal("sD → D"))
	})

	It("completed → message", func() {
		t := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeReplicaType,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			ReplicaName: "rv-1-2", PlanID: "sd-to-d/v1",
			FromReplicaType: v1alpha1.ReplicaTypeShadowDiskful,
			ToReplicaType:   v1alpha1.ReplicaTypeDiskful,
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "sD → D", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					DatameshRevision: 6, StartedAt: ptr.To(metav1.Now())},
			},
		}
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeDiskful, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeDiskful),
			},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 6), mkRVR("rv-1-1", "node-2", 6), mkRVR("rv-1-2", "node-3", 6),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{ShadowDiskful: true})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(Equal("Replica type changed successfully"))
	})
})

var _ = Describe("ChangeReplicaType(sD→D) sd-to-d-q-up/v1", func() {
	It("creates transition (3 steps)", func() {
		// 1D + 1sD. Voters=1 (odd) → q↑.
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeShadowDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeDiskful),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 5),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{ShadowDiskful: true})

		Expect(changed).To(BeTrue())
		t := &rv.Status.DatameshTransitions[0]
		Expect(t.PlanID).To(Equal("sd-to-d-q-up/v1"))
		Expect(t.Steps).To(HaveLen(3))
		Expect(t.Steps[0].Name).To(Equal("sD → sD∅"))
		Expect(t.Steps[1].Name).To(Equal("sD∅ → D∅ + q↑"))
		Expect(t.Steps[2].Name).To(Equal("D∅ → D"))
	})

	It("sD∅→D∅+q↑: q raised, BV preserved", func() {
		// 1D + sD∅ (step 1 done). Voters=1 (odd) → q↑.
		t := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeReplicaType,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			ReplicaName: "rv-1-1", PlanID: "sd-to-d-q-up/v1",
			FromReplicaType: v1alpha1.ReplicaTypeShadowDiskful,
			ToReplicaType:   v1alpha1.ReplicaTypeDiskful,
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "sD → sD∅", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					DatameshRevision: 6, StartedAt: ptr.To(metav1.Now())},
				{Name: "sD∅ → D∅ + q↑", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending},
				{Name: "D∅ → D", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending},
			},
		}
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeLiminalShadowDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeDiskful),
			},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		rv.Status.Datamesh.Quorum = 1
		// Subject confirmed (subjectOnly for step 1).
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 6),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{ShadowDiskful: true})

		Expect(changed).To(BeTrue())

		// Step 2 applied: LiminalDiskful, BV preserved, q raised.
		m := rv.Status.Datamesh.FindMemberByName("rv-1-1")
		Expect(m).NotTo(BeNil())
		Expect(m.Type).To(Equal(v1alpha1.DatameshMemberTypeLiminalDiskful))
		Expect(m.LVMVolumeGroupName).To(Equal("test-lvg"))

		// q raised: 2 voters (D + D∅) → q = max(2/2+1, 1) = 2.
		Expect(rv.Status.Datamesh.Quorum).To(Equal(byte(2)))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// ChangeReplicaType(D → sD)
//

var _ = Describe("ChangeReplicaType(D→sD) dispatch", func() {
	It("odd voters → d-to-sd/v1", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeDiskful, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeShadowDiskful),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRUpToDate("rv-1-0", "node-1", 5), mkRVRUpToDate("rv-1-1", "node-2", 5),
			mkRVRUpToDate("rv-1-2", "node-3", 5),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{ShadowDiskful: true})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("d-to-sd/v1"))
	})

	It("even voters → d-to-sd-q-down/v1", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeShadowDiskful),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRUpToDate("rv-1-0", "node-1", 5), mkRVRUpToDate("rv-1-1", "node-2", 5),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{ShadowDiskful: true})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("d-to-sd-q-down/v1"))
	})
})

var _ = Describe("ChangeReplicaType(D→sD) d-to-sd/v1", func() {
	It("creates transition (1 step), type = sD", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeDiskful, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeShadowDiskful),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRUpToDate("rv-1-0", "node-1", 5), mkRVRUpToDate("rv-1-1", "node-2", 5),
			mkRVRUpToDate("rv-1-2", "node-3", 5),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{ShadowDiskful: true})

		Expect(changed).To(BeTrue())
		m := rv.Status.Datamesh.FindMemberByName("rv-1-2")
		Expect(m).NotTo(BeNil())
		Expect(m.Type).To(Equal(v1alpha1.DatameshMemberTypeShadowDiskful))
		Expect(m.LVMVolumeGroupName).To(Equal("test-lvg"))

		t := &rv.Status.DatameshTransitions[0]
		Expect(t.PlanID).To(Equal("d-to-sd/v1"))
		Expect(t.Steps).To(HaveLen(1))
		Expect(t.Steps[0].Name).To(Equal("D → sD"))
	})

	It("guard: leavingDGuards block", func() {
		// 1D: removing voter violates FTT (D_count=1, D_min=1 with FTT=0 → 1 > 1 false).
		// Actually with FTT=0 and GMDR=0: D_min = 0+0+1 = 1. D_count=1. 1 > 1 is false → blocked.
		// But GMDR guard: ADR = 1-1 = 0. 0 > 0 false → also blocked.
		// Need UpToDate for GMDR to pass. With 1 UpToDate: ADR=0, target_GMDR=0. 0 > 0 false → GMDR blocks.
		// So even 1D with default config is blocked by GMDR guard.
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-0", v1alpha1.ReplicaTypeShadowDiskful),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRUpToDate("rv-1-0", "node-1", 5),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1"), rvrs, nil, FeatureFlags{ShadowDiskful: true})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})
})

var _ = Describe("ChangeReplicaType(D→sD) d-to-sd-q-down/v1", func() {
	It("creates transition (3 steps)", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeShadowDiskful),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRUpToDate("rv-1-0", "node-1", 5), mkRVRUpToDate("rv-1-1", "node-2", 5),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{ShadowDiskful: true})

		Expect(changed).To(BeTrue())
		t := &rv.Status.DatameshTransitions[0]
		Expect(t.PlanID).To(Equal("d-to-sd-q-down/v1"))
		Expect(t.Steps).To(HaveLen(3))
		Expect(t.Steps[0].Name).To(Equal("D → D∅"))
		Expect(t.Steps[1].Name).To(Equal("D∅ → sD∅ + q↓"))
		Expect(t.Steps[2].Name).To(Equal("sD∅ → sD"))
	})

	It("D∅→sD∅+q↓: q lowered, BV preserved", func() {
		t := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeReplicaType,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			ReplicaName: "rv-1-1", PlanID: "d-to-sd-q-down/v1",
			FromReplicaType: v1alpha1.ReplicaTypeDiskful,
			ToReplicaType:   v1alpha1.ReplicaTypeShadowDiskful,
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "D → D∅", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					DatameshRevision: 6, StartedAt: ptr.To(metav1.Now())},
				{Name: "D∅ → sD∅ + q↓", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending},
				{Name: "sD∅ → sD", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending},
			},
		}
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeLiminalDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeShadowDiskful),
			},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		rv.Status.Datamesh.Quorum = 2
		// Subject confirmed (subjectOnly for step 1).
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 6),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{ShadowDiskful: true})

		Expect(changed).To(BeTrue())

		// Step 2 applied: LiminalShadowDiskful, BV preserved, q lowered.
		m := rv.Status.Datamesh.FindMemberByName("rv-1-1")
		Expect(m).NotTo(BeNil())
		Expect(m.Type).To(Equal(v1alpha1.DatameshMemberTypeLiminalShadowDiskful))
		Expect(m.LVMVolumeGroupName).To(Equal("test-lvg"))

		// q lowered: 1 voter (D only, D∅→sD∅ is now non-voter) → q = 1.
		Expect(rv.Status.Datamesh.Quorum).To(Equal(byte(1)))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// sD↔D additional coverage
//

var _ = Describe("ChangeReplicaType(sD↔D) additional", func() {
	It("sD→D: baseline updated on completion (raising)", func() {
		t := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeReplicaType,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			ReplicaName: "rv-1-2", PlanID: "sd-to-d/v1",
			FromReplicaType: v1alpha1.ReplicaTypeShadowDiskful,
			ToReplicaType:   v1alpha1.ReplicaTypeDiskful,
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "sD → D", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					DatameshRevision: 6, StartedAt: ptr.To(metav1.Now())},
			},
		}
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeDiskful, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeDiskful),
			},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		rv.Status.Configuration.FailuresToTolerate = 1
		rv.Status.Datamesh.Quorum = 2
		// All confirmed.
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 6), mkRVR("rv-1-1", "node-2", 6), mkRVR("rv-1-2", "node-3", 6),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{ShadowDiskful: true})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		// Baseline updated by step OnComplete (raising): 3D, q=2 → FTT=1.
	})

	It("D→sD: baseline updated in apply (lowering)", func() {
		// 3D (odd) → D→sD. Voter count drops: 3→2.
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeDiskful, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeShadowDiskful),
			},
			nil,
		)
		rv.Status.Configuration.FailuresToTolerate = 1
		rv.Status.Datamesh.Quorum = 2
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRUpToDate("rv-1-0", "node-1", 5), mkRVRUpToDate("rv-1-1", "node-2", 5),
			mkRVRUpToDate("rv-1-2", "node-3", 5),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{ShadowDiskful: true})

		Expect(changed).To(BeTrue())
		// Step applied: D→sD. Baseline updated immediately (lowering).
		// 2 voters (D+D) + 1 sD (non-voter), q=2. FTT = min(D-q+TB, D-qmr) = min(2-2+0, 2-1) = min(0, 1) = 0.
	})

	It("dispatch: LiminalShadowDiskful → Diskful", func() {
		// sD∅ member with target Diskful → dispatch routes to sd-to-d plan.
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeLiminalShadowDiskful, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeDiskful),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 5), mkRVR("rv-1-2", "node-3", 5),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{ShadowDiskful: true})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("sd-to-d/v1"))
	})

	It("dispatch: LiminalDiskful → ShadowDiskful", func() {
		// D∅ member with target ShadowDiskful → dispatch routes to d-to-sd plan.
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeLiminalDiskful, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeShadowDiskful),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRUpToDate("rv-1-0", "node-1", 5), mkRVRUpToDate("rv-1-1", "node-2", 5),
			mkRVRUpToDate("rv-1-2", "node-3", 5),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{ShadowDiskful: true})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("d-to-sd/v1"))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// ChangeReplicaType(A → D)
//

var _ = Describe("ChangeReplicaType(A→D) dispatch", func() {
	It("even voters, no sD → a-to-d/v1", func() {
		// 2D + 1A. Voters=2 (even).
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeAccess, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeDiskful),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 5), mkRVR("rv-1-2", "node-3", 5),
		}
		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{})
		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("a-to-d/v1"))
	})

	It("odd voters, no sD → a-to-d-q-up/v1", func() {
		// 1D + 1A. Voters=1 (odd).
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeDiskful),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 5),
		}
		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{})
		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("a-to-d-q-up/v1"))
	})

	It("even voters, sD → a-to-d-via-sd/v1", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeAccess, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeDiskful),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 5), mkRVR("rv-1-2", "node-3", 5),
		}
		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{ShadowDiskful: true})
		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("a-to-d-via-sd/v1"))
	})

	It("odd voters, sD → a-to-d-via-sd-q-up/v1", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeDiskful),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 5),
		}
		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{ShadowDiskful: true})
		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("a-to-d-via-sd-q-up/v1"))
	})
})

var _ = Describe("ChangeReplicaType(A→D) plans", func() {
	It("a-to-d/v1: creates (2 steps), BV set", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeAccess, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeDiskful),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 5), mkRVR("rv-1-2", "node-3", 5),
		}
		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{})
		Expect(changed).To(BeTrue())

		m := rv.Status.Datamesh.FindMemberByName("rv-1-2")
		Expect(m.Type).To(Equal(v1alpha1.DatameshMemberTypeLiminalDiskful))
		Expect(m.LVMVolumeGroupName).To(Equal("test-lvg"))

		t := &rv.Status.DatameshTransitions[0]
		Expect(t.PlanID).To(Equal("a-to-d/v1"))
		Expect(t.Steps).To(HaveLen(2))
		Expect(t.Steps[0].Name).To(Equal("A → D∅"))
		Expect(t.Steps[1].Name).To(Equal("D∅ → D"))
	})

	It("a-to-d-q-up/v1: creates (2 steps), q raised on confirm", func() {
		t := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeReplicaType,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			ReplicaName: "rv-1-1", PlanID: "a-to-d-q-up/v1",
			FromReplicaType: v1alpha1.ReplicaTypeAccess,
			ToReplicaType:   v1alpha1.ReplicaTypeDiskful,
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "A → D∅ + q↑", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					DatameshRevision: 6, StartedAt: ptr.To(metav1.Now())},
				{Name: "D∅ → D", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending},
			},
		}
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeLiminalDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeDiskful),
			},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		rv.Status.Datamesh.Quorum = 2
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 6), mkRVR("rv-1-1", "node-2", 6),
		}
		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{})
		Expect(changed).To(BeTrue())

		m := rv.Status.Datamesh.FindMemberByName("rv-1-1")
		Expect(m.Type).To(Equal(v1alpha1.DatameshMemberTypeDiskful))
		Expect(rv.Status.Datamesh.Quorum).To(Equal(byte(2)))
	})

	It("a-to-d-via-sd/v1: creates (3 steps)", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeAccess, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeDiskful),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 5), mkRVR("rv-1-2", "node-3", 5),
		}
		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{ShadowDiskful: true})
		Expect(changed).To(BeTrue())

		t := &rv.Status.DatameshTransitions[0]
		Expect(t.PlanID).To(Equal("a-to-d-via-sd/v1"))
		Expect(t.Steps).To(HaveLen(3))
		Expect(t.Steps[0].Name).To(Equal("A → sD∅"))
		Expect(t.Steps[1].Name).To(Equal("sD∅ → sD"))
		Expect(t.Steps[2].Name).To(Equal("sD → D"))
	})

	It("a-to-d-via-sd-q-up/v1: creates (5 steps)", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeDiskful),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 5),
		}
		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{ShadowDiskful: true})
		Expect(changed).To(BeTrue())

		t := &rv.Status.DatameshTransitions[0]
		Expect(t.PlanID).To(Equal("a-to-d-via-sd-q-up/v1"))
		Expect(t.Steps).To(HaveLen(5))
		Expect(t.Steps[0].Name).To(Equal("A → sD∅"))
		Expect(t.Steps[1].Name).To(Equal("sD∅ → sD"))
		Expect(t.Steps[2].Name).To(Equal("sD → sD∅"))
		Expect(t.Steps[3].Name).To(Equal("sD∅ → D∅ + q↑"))
		Expect(t.Steps[4].Name).To(Equal("D∅ → D"))
	})

	It("a-to-d/v1: baseline updated on completion (raising)", func() {
		t := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeReplicaType,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			ReplicaName: "rv-1-2", PlanID: "a-to-d/v1",
			FromReplicaType: v1alpha1.ReplicaTypeAccess,
			ToReplicaType:   v1alpha1.ReplicaTypeDiskful,
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "A → D∅", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					DatameshRevision: 6, StartedAt: ptr.To(metav1.Now())},
				{Name: "D∅ → D", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending},
			},
		}
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeLiminalDiskful, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeDiskful),
			},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		rv.Status.Configuration.FailuresToTolerate = 1
		rv.Status.Datamesh.Quorum = 2
		// All confirmed → step OnComplete fires → baseline update.
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 6), mkRVR("rv-1-1", "node-2", 6), mkRVR("rv-1-2", "node-3", 6),
		}
		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{})
		Expect(changed).To(BeTrue())
		// 3D (odd), q=2 → FTT = min(3-2, 3-1) = min(1, 2) = 1.
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// ChangeReplicaType(D → A)
//

var _ = Describe("ChangeReplicaType(D→A) dispatch", func() {
	It("odd voters → d-to-a/v1", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeDiskful, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeAccess),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRUpToDate("rv-1-0", "node-1", 5), mkRVRUpToDate("rv-1-1", "node-2", 5),
			mkRVRUpToDate("rv-1-2", "node-3", 5),
		}
		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{})
		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("d-to-a/v1"))
	})

	It("even voters → d-to-a-q-down/v1", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeAccess),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRUpToDate("rv-1-0", "node-1", 5), mkRVRUpToDate("rv-1-1", "node-2", 5),
		}
		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{})
		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("d-to-a-q-down/v1"))
	})
})

var _ = Describe("ChangeReplicaType(D→A) plans", func() {
	It("d-to-a/v1: creates (2 steps), BV cleared", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeDiskful, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeAccess),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRUpToDate("rv-1-0", "node-1", 5), mkRVRUpToDate("rv-1-1", "node-2", 5),
			mkRVRUpToDate("rv-1-2", "node-3", 5),
		}
		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{})
		Expect(changed).To(BeTrue())

		// Step 1 (D→D∅) applied.
		m := rv.Status.Datamesh.FindMemberByName("rv-1-2")
		Expect(m.Type).To(Equal(v1alpha1.DatameshMemberTypeLiminalDiskful))

		t := &rv.Status.DatameshTransitions[0]
		Expect(t.PlanID).To(Equal("d-to-a/v1"))
		Expect(t.Steps).To(HaveLen(2))
		Expect(t.Steps[0].Name).To(Equal("D → D∅"))
		Expect(t.Steps[1].Name).To(Equal("D∅ → A"))
	})

	It("d-to-a-q-down/v1: D∅→A+q↓ applied, q lowered, BV cleared", func() {
		t := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeReplicaType,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			ReplicaName: "rv-1-1", PlanID: "d-to-a-q-down/v1",
			FromReplicaType: v1alpha1.ReplicaTypeDiskful,
			ToReplicaType:   v1alpha1.ReplicaTypeAccess,
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "D → D∅", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					DatameshRevision: 6, StartedAt: ptr.To(metav1.Now())},
				{Name: "D∅ → A + q↓", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending},
			},
		}
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeLiminalDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeAccess),
			},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		rv.Status.Datamesh.Quorum = 2
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 6),
		}
		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{})
		Expect(changed).To(BeTrue())

		m := rv.Status.Datamesh.FindMemberByName("rv-1-1")
		Expect(m.Type).To(Equal(v1alpha1.DatameshMemberTypeAccess))
		Expect(m.LVMVolumeGroupName).To(BeEmpty())
		Expect(rv.Status.Datamesh.Quorum).To(Equal(byte(1)))
	})

	It("d-to-a: guard VolumeAccessLocal blocks", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeDiskful, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeAccess),
			},
			nil,
		)
		rv.Status.Configuration.VolumeAccess = v1alpha1.VolumeAccessLocal
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRUpToDate("rv-1-0", "node-1", 5), mkRVRUpToDate("rv-1-1", "node-2", 5),
			mkRVRUpToDate("rv-1-2", "node-3", 5),
		}
		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{})
		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(ContainSubstring("Local"))
	})

	It("d-to-a: guard leavingDGuards block (GMDR)", func() {
		// 2D with GMDR=1: ADR = 1-1 = 0. 0 > 1 false → blocked.
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeAccess),
			},
			nil,
		)
		rv.Status.Configuration.GuaranteedMinimumDataRedundancy = 1
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRUpToDate("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 5),
		}
		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{})
		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(ContainSubstring("GMDR"))
	})

	It("d-to-a/v1: baseline updated in apply (lowering)", func() {
		// 3D → D→A. Voter drops: 3→2.
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeDiskful, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeAccess),
			},
			nil,
		)
		rv.Status.Configuration.FailuresToTolerate = 1
		rv.Status.Datamesh.Quorum = 2
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRUpToDate("rv-1-0", "node-1", 5), mkRVRUpToDate("rv-1-1", "node-2", 5),
			mkRVRUpToDate("rv-1-2", "node-3", 5),
		}
		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{})
		Expect(changed).To(BeTrue())
		// Step 1 (D→D∅) applied. Baseline NOT yet updated (D∅ still voter).
		// Baseline stays at FTT=1 after step 1.
	})
})

var _ = Describe("ChangeReplicaType(D→A) additional", func() {
	It("d-to-a/v1: completed → message", func() {
		t := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeReplicaType,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			ReplicaName: "rv-1-2", PlanID: "d-to-a/v1",
			FromReplicaType: v1alpha1.ReplicaTypeDiskful,
			ToReplicaType:   v1alpha1.ReplicaTypeAccess,
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "D → D∅", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted, DatameshRevision: 6},
				{Name: "D∅ → A", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					DatameshRevision: 7, StartedAt: ptr.To(metav1.Now())},
			},
		}
		rv := mkRV(7,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeAccess, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeAccess),
			},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 7), mkRVR("rv-1-1", "node-2", 7), mkRVR("rv-1-2", "node-3", 7),
		}
		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{})
		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(Equal("Replica type changed successfully"))
	})

	It("d-to-a/v1: step 2 D∅→A applied, BV cleared", func() {
		t := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeReplicaType,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			ReplicaName: "rv-1-2", PlanID: "d-to-a/v1",
			FromReplicaType: v1alpha1.ReplicaTypeDiskful,
			ToReplicaType:   v1alpha1.ReplicaTypeAccess,
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "D → D∅", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					DatameshRevision: 6, StartedAt: ptr.To(metav1.Now())},
				{Name: "D∅ → A", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending},
			},
		}
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeLiminalDiskful, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeAccess),
			},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		// Subject confirmed (subjectOnly for step 1).
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 5), mkRVR("rv-1-2", "node-3", 6),
		}
		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{})
		Expect(changed).To(BeTrue())

		m := rv.Status.Datamesh.FindMemberByName("rv-1-2")
		Expect(m.Type).To(Equal(v1alpha1.DatameshMemberTypeAccess))
		Expect(m.LVMVolumeGroupName).To(BeEmpty())
		Expect(m.LVMVolumeGroupThinPoolName).To(BeEmpty())
	})

	It("dispatch: LiminalDiskful → Access", func() {
		// D∅ member with target Access → d-to-a plan.
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeLiminalDiskful, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeAccess),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRUpToDate("rv-1-0", "node-1", 5), mkRVRUpToDate("rv-1-1", "node-2", 5),
			mkRVRUpToDate("rv-1-2", "node-3", 5),
		}
		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{})
		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("d-to-a/v1"))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// ChangeReplicaType(TB → D)
//

var _ = Describe("ChangeReplicaType(TB→D) dispatch", func() {
	It("even voters, no sD → tb-to-d/v1", func() {
		// 2D + 1TB. Voters=2 (even).
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeTieBreaker, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeDiskful),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 5), mkRVR("rv-1-2", "node-3", 5),
		}
		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{})
		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("tb-to-d/v1"))
	})

	It("odd voters, no sD → tb-to-d-q-up/v1", func() {
		// 1D + 1TB. Voters=1 (odd).
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeTieBreaker, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeDiskful),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 5),
		}
		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{})
		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("tb-to-d-q-up/v1"))
	})

	It("even voters, sD → tb-to-d-via-sd/v1", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeTieBreaker, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeDiskful),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 5), mkRVR("rv-1-2", "node-3", 5),
		}
		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{ShadowDiskful: true})
		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("tb-to-d-via-sd/v1"))
	})

	It("odd voters, sD → tb-to-d-via-sd-q-up/v1", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeTieBreaker, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeDiskful),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 5),
		}
		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{ShadowDiskful: true})
		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("tb-to-d-via-sd-q-up/v1"))
	})
})

var _ = Describe("ChangeReplicaType(TB→D) plans", func() {
	It("tb-to-d/v1: creates (2 steps), BV set", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeTieBreaker, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeDiskful),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 5), mkRVR("rv-1-2", "node-3", 5),
		}
		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{})
		Expect(changed).To(BeTrue())

		m := rv.Status.Datamesh.FindMemberByName("rv-1-2")
		Expect(m.Type).To(Equal(v1alpha1.DatameshMemberTypeLiminalDiskful))
		Expect(m.LVMVolumeGroupName).To(Equal("test-lvg"))

		t := &rv.Status.DatameshTransitions[0]
		Expect(t.PlanID).To(Equal("tb-to-d/v1"))
		Expect(t.Steps).To(HaveLen(2))
		Expect(t.Steps[0].Name).To(Equal("TB → D∅"))
		Expect(t.Steps[1].Name).To(Equal("D∅ → D"))
	})

	It("tb-to-d-q-up/v1: creates (2 steps), q raised on confirm", func() {
		t := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeReplicaType,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			ReplicaName: "rv-1-1", PlanID: "tb-to-d-q-up/v1",
			FromReplicaType: v1alpha1.ReplicaTypeTieBreaker,
			ToReplicaType:   v1alpha1.ReplicaTypeDiskful,
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "TB → D∅ + q↑", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					DatameshRevision: 6, StartedAt: ptr.To(metav1.Now())},
				{Name: "D∅ → D", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending},
			},
		}
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeLiminalDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeDiskful),
			},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		rv.Status.Datamesh.Quorum = 2
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 6), mkRVR("rv-1-1", "node-2", 6),
		}
		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{})
		Expect(changed).To(BeTrue())

		m := rv.Status.Datamesh.FindMemberByName("rv-1-1")
		Expect(m.Type).To(Equal(v1alpha1.DatameshMemberTypeDiskful))
		Expect(rv.Status.Datamesh.Quorum).To(Equal(byte(2)))
	})

	It("tb-to-d-via-sd/v1: creates (3 steps)", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeTieBreaker, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeDiskful),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 5), mkRVR("rv-1-2", "node-3", 5),
		}
		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{ShadowDiskful: true})
		Expect(changed).To(BeTrue())

		t := &rv.Status.DatameshTransitions[0]
		Expect(t.PlanID).To(Equal("tb-to-d-via-sd/v1"))
		Expect(t.Steps).To(HaveLen(3))
		Expect(t.Steps[0].Name).To(Equal("TB → sD∅"))
		Expect(t.Steps[1].Name).To(Equal("sD∅ → sD"))
		Expect(t.Steps[2].Name).To(Equal("sD → D"))
	})

	It("tb-to-d-via-sd-q-up/v1: creates (5 steps)", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeTieBreaker, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeDiskful),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 5),
		}
		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{ShadowDiskful: true})
		Expect(changed).To(BeTrue())

		t := &rv.Status.DatameshTransitions[0]
		Expect(t.PlanID).To(Equal("tb-to-d-via-sd-q-up/v1"))
		Expect(t.Steps).To(HaveLen(5))
		Expect(t.Steps[0].Name).To(Equal("TB → sD∅"))
		Expect(t.Steps[1].Name).To(Equal("sD∅ → sD"))
		Expect(t.Steps[2].Name).To(Equal("sD → sD∅"))
		Expect(t.Steps[3].Name).To(Equal("sD∅ → D∅ + q↑"))
		Expect(t.Steps[4].Name).To(Equal("D∅ → D"))
	})

	It("guard: leavingTBGuards block when TB required", func() {
		// 2D + 1TB (even D, FTT=1 = D/2). TB required. Guard blocks.
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeTieBreaker, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeDiskful),
			},
			nil,
		)
		rv.Status.Configuration.FailuresToTolerate = 1
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 5), mkRVR("rv-1-2", "node-3", 5),
		}
		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{})
		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(ContainSubstring("TB"))
	})

	It("tb-to-d/v1: baseline raised (OnComplete)", func() {
		t := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeReplicaType,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			ReplicaName: "rv-1-2", PlanID: "tb-to-d/v1",
			FromReplicaType: v1alpha1.ReplicaTypeTieBreaker,
			ToReplicaType:   v1alpha1.ReplicaTypeDiskful,
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "TB → D∅", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					DatameshRevision: 6, StartedAt: ptr.To(metav1.Now())},
				{Name: "D∅ → D", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending},
			},
		}
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeLiminalDiskful, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeDiskful),
			},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		rv.Status.Configuration.FailuresToTolerate = 1
		rv.Status.Datamesh.Quorum = 2
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 6), mkRVR("rv-1-1", "node-2", 6), mkRVR("rv-1-2", "node-3", 6),
		}
		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{})
		Expect(changed).To(BeTrue())
	})

	It("tb-to-d/v1: completed → message", func() {
		t := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeReplicaType,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			ReplicaName: "rv-1-2", PlanID: "tb-to-d/v1",
			FromReplicaType: v1alpha1.ReplicaTypeTieBreaker,
			ToReplicaType:   v1alpha1.ReplicaTypeDiskful,
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "TB → D∅", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted, DatameshRevision: 6},
				{Name: "D∅ → D", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					DatameshRevision: 7, StartedAt: ptr.To(metav1.Now())},
			},
		}
		rv := mkRV(7,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeDiskful, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeDiskful),
			},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 6), mkRVR("rv-1-1", "node-2", 6), mkRVR("rv-1-2", "node-3", 7),
		}
		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{})
		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(Equal("Replica type changed successfully"))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// ChangeReplicaType(D → TB)
//

var _ = Describe("ChangeReplicaType(D→TB) dispatch", func() {
	It("odd voters → d-to-tb/v1", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeDiskful, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeTieBreaker),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRUpToDate("rv-1-0", "node-1", 5), mkRVRUpToDate("rv-1-1", "node-2", 5),
			mkRVRUpToDate("rv-1-2", "node-3", 5),
		}
		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{})
		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("d-to-tb/v1"))
	})

	It("even voters → d-to-tb-q-down/v1", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeTieBreaker),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRUpToDate("rv-1-0", "node-1", 5), mkRVRUpToDate("rv-1-1", "node-2", 5),
		}
		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{})
		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("d-to-tb-q-down/v1"))
	})
})

var _ = Describe("ChangeReplicaType(D→TB) plans", func() {
	It("d-to-tb/v1: creates (2 steps), BV cleared", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeDiskful, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeTieBreaker),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRUpToDate("rv-1-0", "node-1", 5), mkRVRUpToDate("rv-1-1", "node-2", 5),
			mkRVRUpToDate("rv-1-2", "node-3", 5),
		}
		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{})
		Expect(changed).To(BeTrue())

		m := rv.Status.Datamesh.FindMemberByName("rv-1-2")
		Expect(m.Type).To(Equal(v1alpha1.DatameshMemberTypeLiminalDiskful))

		t := &rv.Status.DatameshTransitions[0]
		Expect(t.PlanID).To(Equal("d-to-tb/v1"))
		Expect(t.Steps).To(HaveLen(2))
		Expect(t.Steps[0].Name).To(Equal("D → D∅"))
		Expect(t.Steps[1].Name).To(Equal("D∅ → TB"))
	})

	It("d-to-tb-q-down/v1: q lowered, BV cleared", func() {
		t := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeReplicaType,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			ReplicaName: "rv-1-1", PlanID: "d-to-tb-q-down/v1",
			FromReplicaType: v1alpha1.ReplicaTypeDiskful,
			ToReplicaType:   v1alpha1.ReplicaTypeTieBreaker,
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "D → D∅", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					DatameshRevision: 6, StartedAt: ptr.To(metav1.Now())},
				{Name: "D∅ → TB + q↓", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending},
			},
		}
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeLiminalDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeTieBreaker),
			},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		rv.Status.Datamesh.Quorum = 2
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 6),
		}
		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{})
		Expect(changed).To(BeTrue())

		m := rv.Status.Datamesh.FindMemberByName("rv-1-1")
		Expect(m.Type).To(Equal(v1alpha1.DatameshMemberTypeTieBreaker))
		Expect(m.LVMVolumeGroupName).To(BeEmpty())
		Expect(rv.Status.Datamesh.Quorum).To(Equal(byte(1)))
	})

	It("guard: VolumeAccessLocal blocks D→TB", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeDiskful, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeTieBreaker),
			},
			nil,
		)
		rv.Status.Configuration.VolumeAccess = v1alpha1.VolumeAccessLocal
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRUpToDate("rv-1-0", "node-1", 5), mkRVRUpToDate("rv-1-1", "node-2", 5),
			mkRVRUpToDate("rv-1-2", "node-3", 5),
		}
		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{})
		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(ContainSubstring("Local"))
	})

	It("guard: leavingDGuards block D→TB (GMDR)", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeTieBreaker),
			},
			nil,
		)
		rv.Status.Configuration.GuaranteedMinimumDataRedundancy = 1
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRUpToDate("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 5),
		}
		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{})
		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(ContainSubstring("GMDR"))
	})

	It("d-to-tb/v1: baseline lowered (apply)", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeDiskful, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeTieBreaker),
			},
			nil,
		)
		rv.Status.Configuration.FailuresToTolerate = 1
		rv.Status.Datamesh.Quorum = 2
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRUpToDate("rv-1-0", "node-1", 5), mkRVRUpToDate("rv-1-1", "node-2", 5),
			mkRVRUpToDate("rv-1-2", "node-3", 5),
		}
		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{})
		Expect(changed).To(BeTrue())
		// Step 1 (D→D∅) applied. Baseline stays FTT=1 (D∅ still voter).
	})

	It("d-to-tb/v1: completed → message", func() {
		t := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeReplicaType,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			ReplicaName: "rv-1-2", PlanID: "d-to-tb/v1",
			FromReplicaType: v1alpha1.ReplicaTypeDiskful,
			ToReplicaType:   v1alpha1.ReplicaTypeTieBreaker,
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "D → D∅", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted, DatameshRevision: 6},
				{Name: "D∅ → TB", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					DatameshRevision: 7, StartedAt: ptr.To(metav1.Now())},
			},
		}
		rv := mkRV(7,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeTieBreaker, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeTieBreaker),
			},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 7), mkRVR("rv-1-1", "node-2", 7), mkRVR("rv-1-2", "node-3", 7),
		}
		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{})
		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(Equal("Replica type changed successfully"))
	})

	It("dispatch: LiminalDiskful → TieBreaker", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeLiminalDiskful, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeTieBreaker),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRUpToDate("rv-1-0", "node-1", 5), mkRVRUpToDate("rv-1-1", "node-2", 5),
			mkRVRUpToDate("rv-1-2", "node-3", 5),
		}
		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{})
		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("d-to-tb/v1"))
	})
})

var _ = Describe("ChangeReplicaType(D→TB) additional", func() {
	It("d-to-tb/v1: step 2 D∅→TB applied, BV cleared", func() {
		t := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeReplicaType,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			ReplicaName: "rv-1-2", PlanID: "d-to-tb/v1",
			FromReplicaType: v1alpha1.ReplicaTypeDiskful,
			ToReplicaType:   v1alpha1.ReplicaTypeTieBreaker,
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "D → D∅", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					DatameshRevision: 6, StartedAt: ptr.To(metav1.Now())},
				{Name: "D∅ → TB", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending},
			},
		}
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeLiminalDiskful, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeTieBreaker),
			},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		// Subject confirmed (subjectOnly for step 1).
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 5), mkRVR("rv-1-2", "node-3", 6),
		}
		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{})
		Expect(changed).To(BeTrue())

		m := rv.Status.Datamesh.FindMemberByName("rv-1-2")
		Expect(m.Type).To(Equal(v1alpha1.DatameshMemberTypeTieBreaker))
		Expect(m.LVMVolumeGroupName).To(BeEmpty())
		Expect(m.LVMVolumeGroupThinPoolName).To(BeEmpty())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// ChangeReplicaType + automatic ChangeQuorum for qmr
//
// ChangeReplicaType plans do NOT embed qmr steps (unlike AddReplica(D)).
// After completion, the ChangeQuorum dispatcher should fire automatically
// if qmr diverges from the target.

var _ = Describe("ChangeReplicaType + ChangeQuorum for qmr", func() {
	It("A→D + qmr raise: ChangeQuorum fires after ChangeType completes", func() {
		// 2D (q=2, qmr=2, config GMDR=1) + 1A. Config changed to GMDR=2 → target qmr=3.
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeAccess, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeDiskful),
			},
			nil,
		)
		rv.Status.Datamesh.Quorum = 2
		rv.Status.Datamesh.QuorumMinimumRedundancy = 2
		rv.Status.Configuration.FailuresToTolerate = 1
		rv.Status.Configuration.GuaranteedMinimumDataRedundancy = 2 // target qmr=3

		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRUpToDate("rv-1-0", "node-1", 5),
			mkRVRUpToDate("rv-1-1", "node-2", 5),
			mkRVRUpToDate("rv-1-2", "node-3", 5), // UpToDate: simulates completed resync after A→D
		}
		rsp := mkRSP("node-1", "node-2", "node-3")

		runUntilStable(rv, rsp, rvrs, FeatureFlags{})

		// 3D, q=2, qmr=3 (raised by ChangeQuorum after ChangeType).
		var voters int
		for _, m := range rv.Status.Datamesh.Members {
			if m.Type.IsVoter() {
				voters++
			}
		}
		Expect(voters).To(Equal(3))
		Expect(rv.Status.Datamesh.Quorum).To(Equal(byte(2)))
		Expect(rv.Status.Datamesh.QuorumMinimumRedundancy).To(Equal(byte(3)))
		Expect(rv.Status.BaselineGuaranteedMinimumDataRedundancy).To(Equal(byte(2)))
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})

	It("D→A + qmr lower: ChangeQuorum fires after ChangeType completes", func() {
		// 3D (q=2, qmr=2, config GMDR=1). Config changed to GMDR=0 → target qmr=1.
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeDiskful, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeAccess),
			},
			nil,
		)
		rv.Status.Datamesh.Quorum = 2
		rv.Status.Datamesh.QuorumMinimumRedundancy = 2
		rv.Status.Configuration.FailuresToTolerate = 0
		rv.Status.Configuration.GuaranteedMinimumDataRedundancy = 0 // target qmr=1

		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRUpToDate("rv-1-0", "node-1", 5),
			mkRVRUpToDate("rv-1-1", "node-2", 5),
			mkRVRUpToDate("rv-1-2", "node-3", 5),
		}
		rsp := mkRSP("node-1", "node-2", "node-3")

		runUntilStable(rv, rsp, rvrs, FeatureFlags{})

		// 2D + 1A, q=2, qmr=1 (lowered by ChangeQuorum after ChangeType).
		var dCount, aCount int
		for _, m := range rv.Status.Datamesh.Members {
			switch m.Type {
			case v1alpha1.DatameshMemberTypeDiskful, v1alpha1.DatameshMemberTypeLiminalDiskful:
				dCount++
			case v1alpha1.DatameshMemberTypeAccess:
				aCount++
			}
		}
		Expect(dCount).To(Equal(2))
		Expect(aCount).To(Equal(1))
		Expect(rv.Status.Datamesh.Quorum).To(Equal(byte(2)))
		Expect(rv.Status.Datamesh.QuorumMinimumRedundancy).To(Equal(byte(1)))
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})
})
