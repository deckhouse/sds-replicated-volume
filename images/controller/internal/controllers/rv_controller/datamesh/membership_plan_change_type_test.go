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
