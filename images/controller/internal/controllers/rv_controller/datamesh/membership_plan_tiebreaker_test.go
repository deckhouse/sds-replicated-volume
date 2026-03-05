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
// AddReplica(TB)
//

var _ = Describe("AddReplica(TB)", func() {
	It("creates transition and TB member", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestTB("rv-1-1")},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5),
			mkRVR("rv-1-1", "node-2", 0),
		}

		changed, replicas := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())

		// Member added with TieBreaker type.
		Expect(rv.Status.Datamesh.Members).To(HaveLen(2))
		newMember := rv.Status.Datamesh.FindMemberByName("rv-1-1")
		Expect(newMember).NotTo(BeNil())
		Expect(newMember.Type).To(Equal(v1alpha1.DatameshMemberTypeTieBreaker))
		Expect(newMember.NodeName).To(Equal("node-2"))

		// Revision incremented.
		Expect(rv.Status.DatameshRevision).To(Equal(int64(6)))

		// Transition created with correct plan.
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		t := &rv.Status.DatameshTransitions[0]
		Expect(t.Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica))
		Expect(t.PlanID).To(Equal("tiebreaker/v1"))
		Expect(t.ReplicaName).To(Equal("rv-1-1"))

		// Membership message.
		rc1 := findReplicaContext(replicas, 1)
		Expect(rc1).NotTo(BeNil())
		Expect(rc1.membershipMessage).To(ContainSubstring("Joining datamesh"))
	})

	It("VolumeAccess Local does NOT block TB", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestTB("rv-1-1")},
			nil,
		)
		rv.Status.Configuration = &v1alpha1.ReplicatedVolumeConfiguration{
			ReplicatedStoragePoolName: "test-pool", Topology: v1alpha1.TopologyIgnored,
			VolumeAccess: v1alpha1.VolumeAccessLocal,
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5),
			mkRVR("rv-1-1", "node-2", 0),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		// TB join proceeds despite VolumeAccess=Local (no guardVolumeAccessNotLocal on TB plan).
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("tiebreaker/v1"))
	})

	It("plan selection: already a member", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{mkMember("rv-1-1", v1alpha1.DatameshMemberTypeTieBreaker, "node-2")},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestTB("rv-1-1")},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-1", "node-2", 5)}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-2"), rvrs, nil, FeatureFlags{})

		// Already a member → planAddReplica returns skip.
		Expect(changed).To(BeFalse())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// RemoveReplica(TB)
//

var _ = Describe("RemoveReplica(TB)", func() {
	It("creates transition and removes member", func() {
		// 3D+1TB (FTT=1, GMDR=1): odd D → TB_min=0 → removal allowed.
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeDiskful, "node-3"),
				mkMember("rv-1-3", v1alpha1.DatameshMemberTypeTieBreaker, "node-4"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkLeaveRequest("rv-1-3")},
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

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())

		// Member removed.
		Expect(rv.Status.Datamesh.Members).To(HaveLen(3))
		Expect(rv.Status.Datamesh.FindMemberByName("rv-1-3")).To(BeNil())

		// Transition created.
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		t := &rv.Status.DatameshTransitions[0]
		Expect(t.Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveReplica))
		Expect(t.PlanID).To(Equal("tiebreaker/v1"))
		Expect(t.ReplicaName).To(Equal("rv-1-3"))
	})

	It("guard: TB required (even D, FTT=D/2)", func() {
		// Layout 2D+1TB (FTT=1, GMDR=0): voters=2 even, FTT=1=D/2 → TB_min=1.
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeTieBreaker, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkLeaveRequest("rv-1-2")},
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

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue()) // message changed
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(ContainSubstring("TB required"))
	})

	It("guard: TB not required (odd D)", func() {
		// Layout 3D+1TB (FTT=1, GMDR=1): voters=3 odd → TB_min=0 → removal allowed.
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeDiskful, "node-3"),
				mkMember("rv-1-3", v1alpha1.DatameshMemberTypeTieBreaker, "node-4"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkLeaveRequest("rv-1-3")},
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

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		// Removal allowed — transition created.
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveReplica))
	})

	It("plan selection: not a member", func() {
		rv := mkRV(5, nil, []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkLeaveRequest("rv-1-1")}, nil)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-1", "node-2", 5)}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		// Not a member → planRemoveReplica returns skip.
		Expect(changed).To(BeFalse())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Settle TB transitions
//

var _ = Describe("Settle TB transitions", func() {
	mkAddTBTransition := func(replicaName string, revision int64) v1alpha1.ReplicatedVolumeDatameshTransition {
		return v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership,
			ReplicaName: replicaName,
			PlanID:      "tiebreaker/v1",
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{
				Name: "✦ → TB", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
				DatameshRevision: revision, StartedAt: ptr.To(metav1.Now()),
			}},
		}
	}

	It("completes AddReplica(TB) when all confirmed", func() {
		t := mkAddTBTransition("rv-1-1", 6)
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeTieBreaker, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestTB("rv-1-1")},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-0", "node-1", 6), mkRVR("rv-1-1", "node-2", 6)}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(Equal("Joined datamesh successfully"))
	})

	It("completes RemoveReplica(TB) when subject rev=0", func() {
		removeT := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveReplica,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership,
			ReplicaName: "rv-1-1",
			PlanID:      "tiebreaker/v1",
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{
				Name: "TB → ✕", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
				DatameshRevision: 6, StartedAt: ptr.To(metav1.Now()),
			}},
		}
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkLeaveRequest("rv-1-1")},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{removeT},
		)
		// Diskful confirmed (rev 6), subject reset to 0 (left datamesh).
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-0", "node-1", 6), mkRVR("rv-1-1", "node-2", 0)}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(Equal("Left datamesh successfully"))
	})
})
