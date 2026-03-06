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
