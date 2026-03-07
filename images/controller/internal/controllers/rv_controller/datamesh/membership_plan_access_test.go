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
// AddReplica(A)
//

var _ = Describe("AddReplica(A)", func() {
	It("creates transition and member", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestAccess("rv-1-1")},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5),
			mkRVR("rv-1-1", "node-2", 0),
		}

		changed, replicas := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())

		// Member added.
		Expect(rv.Status.Datamesh.Members).To(HaveLen(2))
		newMember := rv.Status.Datamesh.FindMemberByName("rv-1-1")
		Expect(newMember).NotTo(BeNil())
		Expect(newMember.Type).To(Equal(v1alpha1.DatameshMemberTypeAccess))
		Expect(newMember.NodeName).To(Equal("node-2"))
		Expect(newMember.Addresses).To(HaveLen(1))
		Expect(newMember.Attached).To(BeFalse())

		// Revision incremented.
		Expect(rv.Status.DatameshRevision).To(Equal(int64(6)))

		// Transition created.
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		t := &rv.Status.DatameshTransitions[0]
		Expect(t.Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica))
		Expect(t.ReplicaName).To(Equal("rv-1-1"))
		Expect(t.PlanID).To(Equal("access/v1"))
		Expect(t.Steps[0].DatameshRevision).To(Equal(int64(6)))

		// Membership message on replica context.
		rc1 := findReplicaContext(replicas, 1)
		Expect(rc1).NotTo(BeNil())
		Expect(rc1.membershipMessage).To(ContainSubstring("Joining datamesh"))

		// Request message written back.
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(ContainSubstring("Joining datamesh"))
	})

	It("guard: RV deleting", func() {
		rv := mkRV(5, nil, []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestAccess("rv-1-1")}, nil)
		rv.DeletionTimestamp = ptr.To(metav1.Now())
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-1", "node-2", 0)}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-2"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue()) // message changed
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(ContainSubstring("being deleted"))
	})

	It("guard: VolumeAccess Local", func() {
		rv := mkRV(5, nil, []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestAccess("rv-1-1")}, nil)
		rv.Status.Configuration = &v1alpha1.ReplicatedVolumeConfiguration{
			ReplicatedStoragePoolName: "test-pool",
			Topology:                  v1alpha1.TopologyIgnored,
			VolumeAccess:              v1alpha1.VolumeAccessLocal,
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-1", "node-2", 0)}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-2"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(ContainSubstring("volumeAccess is Local"))
	})

	It("guard: addresses empty", func() {
		rv := mkRV(5, nil, []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestAccess("rv-1-1")}, nil)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVRBare("rv-1-1", "node-2")}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-2"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(ContainSubstring("addresses"))
	})

	It("guard: RVR nil (no RVR at all)", func() {
		// Join request exists but the RVR has not been created yet.
		// guardAddressesPopulated checks rctx.rvr == nil and blocks.
		rv := mkRV(5, nil, []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestAccess("rv-1-1")}, nil)
		// Only the Diskful RVR — no RVR for rv-1-1.
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-2"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(ContainSubstring("addresses"))
	})

	It("guard: member on same node", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-2")},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestAccess("rv-1-1")},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-2", 5),
			mkRVR("rv-1-1", "node-2", 0),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-2"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(ContainSubstring("Diskful"))
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(ContainSubstring("node-2"))
	})

	It("guard: RSP nil", func() {
		rv := mkRV(5, nil, []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestAccess("rv-1-1")}, nil)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-1", "node-2", 0)}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(ContainSubstring("ReplicatedStoragePool"))
	})

	It("guard: node not eligible", func() {
		rv := mkRV(5, nil, []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestAccess("rv-1-1")}, nil)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-1", "node-2", 0)}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(ContainSubstring("eligible"))
	})

	It("plan selection: already a member", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{mkMember("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-2")},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestAccess("rv-1-1")},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-1", "node-2", 5)}
		settleEffectiveLayout(rv, rvrs)

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-2"), rvrs, nil, FeatureFlags{})

		// planAddReplica returns ("","") → skip, no message written.
		Expect(changed).To(BeFalse())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})

	It("extracts zone from RSP", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestAccess("rv-1-1")},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5),
			mkRVR("rv-1-1", "node-2", 0),
		}
		rsp := mkRSPWithZones("node-1", "zone-a", "node-2", "zone-b")

		ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, FeatureFlags{})

		newMember := rv.Status.Datamesh.FindMemberByName("rv-1-1")
		Expect(newMember).NotTo(BeNil())
		Expect(newMember.Zone).To(Equal("zone-b"))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// RemoveReplica(A)
//

var _ = Describe("RemoveReplica(A)", func() {
	It("creates transition and removes member", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkLeaveRequest("rv-1-1")},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5),
			mkRVR("rv-1-1", "node-2", 5),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())

		// Member removed.
		Expect(rv.Status.Datamesh.Members).To(HaveLen(1))
		Expect(rv.Status.Datamesh.FindMemberByName("rv-1-1")).To(BeNil())

		// Revision incremented.
		Expect(rv.Status.DatameshRevision).To(Equal(int64(6)))

		// Transition created.
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		t := &rv.Status.DatameshTransitions[0]
		Expect(t.Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveReplica))
		Expect(t.ReplicaName).To(Equal("rv-1-1"))
		Expect(t.PlanID).To(Equal("access/v1"))
	})

	It("guard: member attached", func() {
		m := mkMember("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-2")
		m.Attached = true
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{m},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkLeaveRequest("rv-1-1")},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-1", "node-2", 5)}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		// Outer loop: iteration 1 dispatches Detach (Attached→false). Iteration 2
		// re-dispatches RemoveReplica → guardNotAttached sees active Detach → blocks.
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(ContainSubstring("transition in progress"))
		// Detach still active (awaiting RVR confirmation).
		hasDetach := false
		for _, t := range rv.Status.DatameshTransitions {
			if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach {
				hasDetach = true
			}
		}
		Expect(hasDetach).To(BeTrue())
	})

	It("plan selection: not a member", func() {
		rv := mkRV(5, nil, []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkLeaveRequest("rv-1-1")}, nil)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-1", "node-2", 5)}
		settleEffectiveLayout(rv, rvrs)

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		// planRemoveReplica returns ("","") → skip.
		Expect(changed).To(BeFalse())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})

	It("works when RV deleting", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkLeaveRequest("rv-1-1")},
			nil,
		)
		rv.DeletionTimestamp = ptr.To(metav1.Now())
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5),
			mkRVR("rv-1-1", "node-2", 5),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		// Leave is not blocked by deletion.
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveReplica))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Settle
//

var _ = Describe("Settle Access transitions", func() {
	// mkActiveTransition builds an active single-step transition for settle tests.
	mkActiveTransition := func(
		tt v1alpha1.ReplicatedVolumeDatameshTransitionType,
		replicaName string, //nolint:unparam
		revision int64, //nolint:unparam
	) v1alpha1.ReplicatedVolumeDatameshTransition {
		return v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        tt,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership,
			ReplicaName: replicaName,
			PlanID:      "access/v1",
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{
					Name:             "✦ → A",
					Status:           v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					DatameshRevision: revision,
					StartedAt:        ptr.To(metav1.Now()),
				},
			},
		}
	}

	It("completes AddReplica when all confirmed", func() {
		t := mkActiveTransition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica, "rv-1-1", 6)
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestAccess("rv-1-1")},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-0", "node-1", 6), mkRVR("rv-1-1", "node-2", 6)}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(Equal("Joined datamesh successfully"))
	})

	It("completes RemoveReplica when subject rev=0", func() {
		t := mkActiveTransition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveReplica, "rv-1-1", 6)
		t.Steps[0].Name = "A → ✕"
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkLeaveRequest("rv-1-1")},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		// Diskful confirmed (rev 6), subject reset revision to 0 (left datamesh).
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-0", "node-1", 6), mkRVR("rv-1-1", "node-2", 0)}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(Equal("Left datamesh successfully"))
	})

	It("progress message when partial confirmation", func() {
		t := mkActiveTransition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica, "rv-1-1", 6)
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestAccess("rv-1-1")},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		// Diskful confirmed, subject not yet.
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-0", "node-1", 6), mkRVR("rv-1-1", "node-2", 5)}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Steps[0].Message).To(ContainSubstring("1/2"))
		Expect(rv.Status.DatameshTransitions[0].Steps[0].Message).To(ContainSubstring("revision 6"))
	})

	It("PendingDatameshJoin not shown as error", func() {
		t := mkActiveTransition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica, "rv-1-1", 6)
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestAccess("rv-1-1")},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		subjectRVR := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-1-1", Generation: 1},
			Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-2"},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshRevision: 5,
				Addresses:        []v1alpha1.DRBDResourceAddressStatus{{SystemNetworkName: "default"}},
				Conditions: []metav1.Condition{{
					Type:               v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType,
					Status:             metav1.ConditionFalse,
					Reason:             v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonPendingDatameshJoin,
					ObservedGeneration: 1,
				}},
			},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-0", "node-1", 6), subjectRVR}

		ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(rv.Status.DatameshTransitions[0].Steps[0].Message).NotTo(ContainSubstring("Errors"))
		Expect(rv.Status.DatameshTransitions[0].Steps[0].Message).NotTo(ContainSubstring("PendingDatameshJoin"))
	})

	It("other DRBDConfigured errors surfaced", func() {
		t := mkActiveTransition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica, "rv-1-1", 6)
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestAccess("rv-1-1")},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		subjectRVR := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-1-1", Generation: 1},
			Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-2"},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshRevision: 5,
				Addresses:        []v1alpha1.DRBDResourceAddressStatus{{SystemNetworkName: "default"}},
				Conditions: []metav1.Condition{{
					Type:               v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType,
					Status:             metav1.ConditionFalse,
					Reason:             "ConfigurationFailed",
					ObservedGeneration: 1,
				}},
			},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-0", "node-1", 6), subjectRVR}

		ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(rv.Status.DatameshTransitions[0].Steps[0].Message).To(ContainSubstring("Errors"))
		Expect(rv.Status.DatameshTransitions[0].Steps[0].Message).To(ContainSubstring("ConfigurationFailed"))
	})

	It("ShadowDiskful in mustConfirm set", func() {
		t := mkActiveTransition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica, "rv-1-1", 6)
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeShadowDiskful, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestAccess("rv-1-1")},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 6),
			mkRVR("rv-1-1", "node-2", 6),
			mkRVR("rv-1-2", "node-3", 5), // sD not confirmed
		}

		ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		// Transition NOT completed because ShadowDiskful(#2) has not confirmed.
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Steps[0].Message).To(ContainSubstring("#2"))
	})

	It("RemoveReplica partial progress", func() {
		t := mkActiveTransition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveReplica, "rv-1-1", 6)
		t.Steps[0].Name = "A → ✕"
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkLeaveRequest("rv-1-1")},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		// Subject at rev 4 (not confirmed, not reset to 0). Diskful also not confirmed.
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-0", "node-1", 4), mkRVR("rv-1-1", "node-2", 4)}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Steps[0].Message).To(ContainSubstring("0/2"))
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(ContainSubstring("Leaving datamesh"))
	})

	It("subject RVR disappears during active transition", func() {
		// Active AddReplica for rv-1-1. Diskful confirmed, but subject RVR is gone.
		// confirmFMPlusSubject: mustConfirm={#0,#1}, confirmed={#0} (subject missing).
		t := mkActiveTransition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica, "rv-1-1", 6)
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestAccess("rv-1-1")},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		// Only Diskful RVR — subject RVR is completely gone.
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-0", "node-1", 6)}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		// Transition NOT completed — subject not confirmed.
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Steps[0].Message).To(ContainSubstring("1/2"))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Integration
//

var _ = Describe("Access integration", func() {
	It("settle + dispatch in same call", func() {
		// Existing AddReplica(rv-1-1) transition at rev 6, all confirmed → will complete.
		// Pending join for rv-1-2 → new transition will be created.
		t := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership,
			ReplicaName: "rv-1-1",
			PlanID:      "access/v1",
			ReplicaType: v1alpha1.ReplicaTypeAccess,
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{
				Name:             "✦ → A",
				Status:           v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
				DatameshRevision: 6,
				StartedAt:        ptr.To(metav1.Now()),
			}},
		}
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkJoinRequestAccess("rv-1-1"),
				mkJoinRequestAccess("rv-1-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 6),
			mkRVR("rv-1-1", "node-2", 6),
			mkRVR("rv-1-2", "node-3", 0),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())

		// Old transition completed, new one created for rv-1-2.
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].ReplicaName).To(Equal("rv-1-2"))

		// rv-1-2 added as member.
		Expect(rv.Status.Datamesh.FindMemberByName("rv-1-2")).NotTo(BeNil())

		// Revision: was 6, completion doesn't increment, new join increments to 7.
		Expect(rv.Status.DatameshRevision).To(Equal(int64(7)))
	})

	It("multiple parallel NonVoting joins", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkJoinRequestAccess("rv-1-1"),
				mkJoinRequestAccess("rv-1-2"),
			},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5),
			mkRVR("rv-1-1", "node-2", 0),
			mkRVR("rv-1-2", "node-3", 0),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.Datamesh.Members).To(HaveLen(3))
		Expect(rv.Status.DatameshTransitions).To(HaveLen(2))
		Expect(rv.Status.DatameshRevision).To(Equal(int64(7)))

		// Each transition has its own revision.
		revs := []int64{
			rv.Status.DatameshTransitions[0].Steps[0].DatameshRevision,
			rv.Status.DatameshTransitions[1].Steps[0].DatameshRevision,
		}
		Expect(revs).To(ConsistOf(int64(6), int64(7)))
	})

	It("active same-type transition skips dispatch", func() {
		// AddReplica active for rv-1-1 (not yet confirmed).
		// Join request for rv-1-1 still present.
		// Dispatcher should skip (same type active) — no duplicate transition.
		t := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership,
			ReplicaName: "rv-1-1",
			PlanID:      "access/v1",
			ReplicaType: v1alpha1.ReplicaTypeAccess,
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{
				Name:             "✦ → A",
				Status:           v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
				DatameshRevision: 6,
				StartedAt:        ptr.To(metav1.Now()),
			}},
		}
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestAccess("rv-1-1")},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 5)}

		ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{})

		// Still exactly one transition (no duplicate created).
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].ReplicaName).To(Equal("rv-1-1"))
	})

	It("RemoveReplica in-progress blocks AddReplica for same replica", func() {
		// Active RemoveReplica for rv-1-1 (not yet confirmed).
		// Join request for rv-1-1 (re-join after leave).
		// Engine slot conflict: RemoveReplica occupies the membership slot,
		// so AddReplica for the same replica is blocked.
		removeT := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveReplica,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership,
			ReplicaName: "rv-1-1",
			PlanID:      "access/v1",
			ReplicaType: v1alpha1.ReplicaTypeAccess,
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{
				Name:             "A → ✕",
				Status:           v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
				DatameshRevision: 6,
				StartedAt:        ptr.To(metav1.Now()),
			}},
		}
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				// rv-1-1 already removed from members (RemoveReplica apply removes it)
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestAccess("rv-1-1")},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{removeT},
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 4), // not yet confirmed
			mkRVR("rv-1-1", "node-2", 4), // not yet confirmed
		}

		ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{})

		// Only RemoveReplica present — no AddReplica created.
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveReplica))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
//

// findReplicaContext finds a ReplicaContext by ID in the slice.
func findReplicaContext(replicas []ReplicaContext, id uint8) *ReplicaContext {
	for i := range replicas {
		if replicas[i].id == id {
			return &replicas[i]
		}
	}
	return nil
}
