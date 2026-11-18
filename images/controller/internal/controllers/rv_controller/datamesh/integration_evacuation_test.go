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
	"fmt"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func evacuationLayout(voters int) (*v1alpha1.ReplicatedVolume, []*v1alpha1.ReplicatedVolumeReplica) {
	var members []v1alpha1.DatameshMember
	var rvrs []*v1alpha1.ReplicatedVolumeReplica
	for i := range voters {
		name, node := fmt.Sprintf("rv-1-%d", i), fmt.Sprintf("node-%d", i)
		members = append(members, mkMember(name, v1alpha1.DatameshMemberTypeDiskful, node))
		rvrs = append(rvrs, mkRVRUpToDate(name, node, 5))
	}
	return mkRV(5, members, nil, nil), rvrs
}

func evacuationTieBreakerLayout() (*v1alpha1.ReplicatedVolume, []*v1alpha1.ReplicatedVolumeReplica) {
	rv, rvrs := evacuationLayout(3)
	rv.Status.Datamesh.Members[2].Type = v1alpha1.DatameshMemberTypeTieBreaker
	rvrs[2].Spec.Type = v1alpha1.ReplicaTypeTieBreaker
	rvrs[2].Status.BackingVolume = nil
	return rv, rvrs
}

var _ = Describe("automatic evacuation", func() {
	It("removes one evacuee from 3D and completes with only live confirmations", func(ctx SpecContext) {
		rv, rvrs := evacuationLayout(3)
		evacuees := Evacuees{"rv-1-2": {}}
		_, _, completed := ProcessTransitionsWithCompleted(ctx, rv, nil, rvrs, nil, FeatureFlags{}, evacuees)
		Expect(completed).To(BeEmpty())
		Expect(rv.Status.Datamesh.FindMemberByName("rv-1-2")).To(BeNil())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("diskful/v1"))
		simulateRevisionBump(rv, rvrs[:2])
		_, _, completed = ProcessTransitionsWithCompleted(ctx, rv, nil, rvrs, nil, FeatureFlags{}, evacuees)
		Expect(completed).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rvrs[2].Status.DatameshRevision).To(Equal(int64(5)))
	})

	It("blocks two evacuees in 3D with 1 live voter below quorum 2", func(ctx SpecContext) {
		rv, rvrs := evacuationLayout(3)
		_, contexts, _ := ProcessTransitionsWithCompleted(ctx, rv, nil, rvrs, nil, FeatureFlags{}, Evacuees{"rv-1-1": {}, "rv-1-2": {}})
		Expect(rv.Status.Datamesh.Members).To(HaveLen(3))
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(contexts[1].membershipMessage).To(ContainSubstring("evacuation would leave 1 live voter(s), quorum after removal needs 2"))
		Expect(contexts[2].membershipMessage).To(BeEmpty())
	})

	It("removes two evacuees in 4D in name order using the reduced voter count", func(ctx SpecContext) {
		rv, rvrs := evacuationLayout(4)
		// Lexical order differs from replica ID order: 10 must precede 2.
		rv.Status.Datamesh.Members[3].Name = "rv-1-10"
		rvrs[3].Name = "rv-1-10"
		evacuees := Evacuees{"rv-1-2": {}, "rv-1-10": {}}
		_, _, completed := ProcessTransitionsWithCompleted(ctx, rv, nil, rvrs, nil, FeatureFlags{}, evacuees)
		Expect(completed).To(BeEmpty())
		Expect(rv.Status.Datamesh.Members).To(HaveLen(2))
		Expect(rv.Status.DatameshTransitions).To(HaveLen(2))
		first, second := rv.Status.DatameshTransitions[0], rv.Status.DatameshTransitions[1]
		Expect(first.ReplicaName).To(Equal("rv-1-10"))
		Expect(first.PlanID).To(Equal("diskful-q-down/v1"))
		Expect(second.ReplicaName).To(Equal("rv-1-2"))
		Expect(second.PlanID).To(Equal("diskful/v1")) // Its guards saw V=3, not V=4.
		Expect(first.Steps[0].DatameshRevision).To(BeNumerically("<", second.Steps[0].DatameshRevision))
		Expect(rv.Status.Datamesh.Quorum).To(Equal(byte(2)))
		simulateRevisionBump(rv, rvrs[:2])
		_, _, completed = ProcessTransitionsWithCompleted(ctx, rv, nil, rvrs, nil, FeatureFlags{}, evacuees)
		Expect(completed).To(HaveLen(2))
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})

	DescribeTable("removes either D or TB from 2D+1TB", func(ctx SpecContext, subject int, plan string, quorum byte) {
		rv, rvrs := evacuationTieBreakerLayout()
		evacuees := Evacuees{rvrs[subject].Name: {}}
		_, _, completed := ProcessTransitionsWithCompleted(ctx, rv, nil, rvrs, nil, FeatureFlags{}, evacuees)
		Expect(completed).To(BeEmpty())
		Expect(rv.Status.Datamesh.FindMemberByName(rvrs[subject].Name)).To(BeNil())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal(plan))
		Expect(rv.Status.Datamesh.Quorum).To(Equal(quorum))
		for i, rvr := range rvrs {
			if i != subject {
				rvr.Status.DatameshRevision = rv.Status.DatameshRevision
			}
		}
		_, _, completed = ProcessTransitionsWithCompleted(ctx, rv, nil, rvrs, nil, FeatureFlags{}, evacuees)
		Expect(completed).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	},
		Entry("TB keeps V'=2 and q'=2", 2, "tiebreaker/v1", byte(2)),
		Entry("D leaves V'=1 and q'=1", 1, "diskful-q-down/v1", byte(1)),
	)

	DescribeTable("evacuates voters before tie-breakers and guards the current configuration", func(ctx SpecContext, tbFirstByName bool) {
		rv, rvrs := evacuationTieBreakerLayout()
		if tbFirstByName {
			// Lexical order (10 before 3) differs from the replica ID/context order.
			rv.Status.Datamesh.Members[1].Name, rvrs[1].Name = "rv-1-3", "rv-1-3"
			rv.Status.Datamesh.Members[2].Name, rvrs[2].Name = "rv-1-10", "rv-1-10"
		}
		evacuees := Evacuees{rvrs[1].Name: {}, rvrs[2].Name: {}}
		for _, rvr := range rvrs[1:] {
			obju.SetStatusCondition(rvr, metav1.Condition{
				Type: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType, Status: metav1.ConditionFalse,
				Reason: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonAgentNotReady,
			})
		}
		_, _, completed := ProcessTransitionsWithCompleted(ctx, rv, nil, rvrs, nil, FeatureFlags{}, evacuees)
		Expect(completed).To(BeEmpty())
		Expect(rv.Status.Datamesh.Members).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions).To(HaveLen(2))
		first, second := rv.Status.DatameshTransitions[0], rv.Status.DatameshTransitions[1]
		Expect(first.ReplicaName).To(Equal(rvrs[1].Name))
		Expect(first.PlanID).To(Equal("diskful-q-down/v1"))
		Expect(second.ReplicaName).To(Equal(rvrs[2].Name))
		Expect(second.PlanID).To(Equal("tiebreaker/v1"))
		Expect(first.Steps[0].DatameshRevision).To(BeNumerically("<", second.Steps[0].DatameshRevision))
		Expect(rv.Status.Datamesh.Quorum).To(Equal(byte(1)))
		simulateRevisionBump(rv, rvrs[:1])
		_, _, completed = ProcessTransitionsWithCompleted(ctx, rv, nil, rvrs, nil, FeatureFlags{}, evacuees)
		Expect(completed).To(HaveLen(2))
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	}, Entry("D first by name: both leave with q'=1", false), Entry("TB first by name: D leaves first, then TB with q'=1", true))

	DescribeTable("applies the evacuation guards to a TB subject", func(ctx SpecContext, noUpToDate bool) {
		rv, rvrs := evacuationTieBreakerLayout()
		message := "1 live voter(s), quorum after removal needs 2"
		if noUpToDate {
			for _, rvr := range rvrs[:2] {
				rvr.Status.BackingVolume.State = v1alpha1.DiskStateOutdated
			}
			message = "no live UpToDate diskful replica would remain"
		} else {
			obju.SetStatusCondition(rvrs[1], metav1.Condition{
				Type: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType, Status: metav1.ConditionFalse,
				Reason: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonAgentNotReady,
			})
		}
		_, contexts, _ := ProcessTransitionsWithCompleted(ctx, rv, nil, rvrs, nil, FeatureFlags{}, Evacuees{rvrs[2].Name: {}})
		Expect(rv.Status.Datamesh.Members).To(HaveLen(3))
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(contexts[2].membershipMessage).To(ContainSubstring(message))
	}, Entry("TB is never subtracted from voterCount", false), Entry("a live UpToDate diskful copy must remain", true))

	It("blocks when the live remaining voters have no UpToDate diskful copy", func(ctx SpecContext) {
		rv, rvrs := evacuationLayout(3)
		rvrs[0].Status.BackingVolume.State = v1alpha1.DiskStateOutdated
		rvrs[1].Status.BackingVolume.State = v1alpha1.DiskStateOutdated
		_, contexts, _ := ProcessTransitionsWithCompleted(ctx, rv, nil, rvrs, nil, FeatureFlags{}, Evacuees{"rv-1-2": {}})
		Expect(rv.Status.Datamesh.Members).To(HaveLen(3))
		Expect(contexts[2].membershipMessage).To(ContainSubstring("no live UpToDate diskful replica would remain"))
	})

	DescribeTable("does not count a tie-breaker toward the evacuation majority", func(ctx SpecContext, evacuateTB bool) {
		rv, rvrs := evacuationLayout(3)
		rv.Status.Datamesh.Members = append(rv.Status.Datamesh.Members,
			mkMember("rv-1-3", v1alpha1.DatameshMemberTypeTieBreaker, "node-3"))
		rvrs = append(rvrs, mkRVRUpToDate("rv-1-3", "node-3", 5))
		evacuees := Evacuees{"rv-1-1": {}, "rv-1-2": {}}
		if evacuateTB {
			evacuees["rv-1-3"] = struct{}{}
		}
		_, contexts, _ := ProcessTransitionsWithCompleted(ctx, rv, nil, rvrs, nil, FeatureFlags{}, evacuees)
		Expect(rv.Status.Datamesh.Members).To(HaveLen(4))
		Expect(contexts[1].membershipMessage).To(ContainSubstring("1 live voter(s), quorum after removal needs 2"))
	}, Entry("live TB", false), Entry("evacuee TB", true))

	It("does not count a remaining voter whose DRBDConfigured reports AgentNotReady", func(ctx SpecContext) {
		rv, rvrs := evacuationLayout(3)
		obju.SetStatusCondition(rvrs[1], metav1.Condition{
			Type: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType, Status: metav1.ConditionFalse,
			Reason: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonAgentNotReady,
		})
		_, contexts, _ := ProcessTransitionsWithCompleted(ctx, rv, nil, rvrs, nil, FeatureFlags{}, Evacuees{"rv-1-2": {}})
		Expect(rv.Status.Datamesh.Members).To(HaveLen(3))
		Expect(contexts[2].membershipMessage).To(ContainSubstring("1 live voter(s), quorum after removal needs 2"))
	})

	It("rechecks live quorum when another agent dies between passes", func(ctx SpecContext) {
		rv, rvrs := evacuationLayout(3)
		// First pass waits on reachability, while quorum would allow the removal.
		rvrs[0].Status.Peers = []v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{{Name: "rv-1-2", ConnectionState: v1alpha1.ConnectionStateConnected}}
		evacuees := Evacuees{"rv-1-2": {}}
		_, contexts, _ := ProcessTransitionsWithCompleted(ctx, rv, nil, rvrs, nil, FeatureFlags{}, evacuees)
		Expect(contexts[2].membershipMessage).To(ContainSubstring("reachable"))
		rvrs[0].Status.Peers = nil
		obju.SetStatusCondition(rvrs[1], metav1.Condition{
			Type: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType, Status: metav1.ConditionFalse,
			Reason: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonAgentNotReady,
		})
		_, contexts, _ = ProcessTransitionsWithCompleted(ctx, rv, nil, rvrs, nil, FeatureFlags{}, evacuees)
		Expect(rv.Status.Datamesh.Members).To(HaveLen(3))
		Expect(contexts[2].membershipMessage).To(ContainSubstring("1 live voter(s), quorum after removal needs 2"))
	})

	It("keeps orphan removal unrestricted even if their names are passed as evacuees", func(ctx SpecContext) {
		rv, rvrs := evacuationLayout(3)
		rvrs[0].Status.BackingVolume.State = v1alpha1.DiskStateOutdated
		ProcessTransitionsWithCompleted(ctx, rv, nil, rvrs[:1], nil, FeatureFlags{}, Evacuees{"rv-1-1": {}, "rv-1-2": {}})
		Expect(rv.Status.Datamesh.Members).To(HaveLen(1))
		Expect(rv.Status.Datamesh.Quorum).To(Equal(byte(1)))
	})

	It("keeps explicit ForceLeave unrestricted on an evacuee", func(ctx SpecContext) {
		rv, rvrs := evacuationLayout(3)
		rvrs[0].Status.BackingVolume.State = v1alpha1.DiskStateOutdated
		rv.Status.DatameshReplicaRequests = []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkForceLeaveRequest("rv-1-2")}
		ProcessTransitionsWithCompleted(ctx, rv, nil, rvrs, nil, FeatureFlags{}, Evacuees{"rv-1-1": {}, "rv-1-2": {}})
		Expect(rv.Status.Datamesh.FindMemberByName("rv-1-2")).To(BeNil())
		Expect(rv.Status.Datamesh.FindMemberByName("rv-1-1")).NotTo(BeNil())
	})

	DescribeTable("takes precedence over ordinary membership requests", func(ctx SpecContext, request v1alpha1.ReplicatedVolumeDatameshReplicaRequest) {
		rv, rvrs := evacuationLayout(3)
		rv.Status.DatameshReplicaRequests = []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{request}
		ProcessTransitionsWithCompleted(ctx, rv, nil, rvrs, nil, FeatureFlags{}, Evacuees{"rv-1-2": {}})
		Expect(rv.Status.Datamesh.FindMemberByName("rv-1-2")).To(BeNil())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeForceRemoveReplica))
	},
		Entry("Join", mkJoinRequestD("rv-1-2")),
		Entry("Leave", mkLeaveRequest("rv-1-2")),
		Entry("ChangeRole", mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeAccess)),
	)

	It("cancels an active Leave and removes the evacuee", func(ctx SpecContext) {
		rv, rvrs := evacuationLayout(3)
		rvrs[2].Status.DatameshRevision = 0
		rv.Status.DatameshReplicaRequests = []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkLeaveRequest("rv-1-2")}
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveReplica,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			ReplicaName: "rv-1-2", PlanID: "remove-diskful/v1", ReplicaType: v1alpha1.ReplicaTypeDiskful,
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{
				Name: "D → D∅", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
				DatameshRevision: 5, StartedAt: new(metav1.Now()),
			}},
		}}
		rv.Status.Datamesh.Members[2].Type = v1alpha1.DatameshMemberTypeLiminalDiskful
		ProcessTransitionsWithCompleted(ctx, rv, nil, rvrs, nil, FeatureFlags{}, Evacuees{"rv-1-2": {}})
		Expect(rv.Status.Datamesh.FindMemberByName("rv-1-2")).To(BeNil())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeForceRemoveReplica))
	})

	DescribeTable("preserves an active membership transition while evacuation is blocked", func(ctx SpecContext, changingType bool) {
		voters := 3
		transitionType := v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveReplica
		plan, step := "remove-diskful/v1", "D → D∅"
		request := mkLeaveRequest("rv-1-1")
		evacuees := Evacuees{"rv-1-1": {}, "rv-1-2": {}}
		if changingType {
			voters = 4
			transitionType = v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeReplicaType
			plan, step = "a-to-d-q-up/v1", "A → D∅ + q↑"
			request = mkChangeRoleRequest("rv-1-1", v1alpha1.ReplicaTypeDiskful)
			evacuees["rv-1-3"] = struct{}{}
		}
		rv, rvrs := evacuationLayout(voters)
		rv.Status.Datamesh.Quorum = byte(voters/2 + 1)
		rv.Status.Datamesh.Members[1].Type = v1alpha1.DatameshMemberTypeLiminalDiskful
		rvrs[1].Status.DatameshRevision = 0
		rv.Status.DatameshReplicaRequests = []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{request}
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{{
			Type: transitionType, Group: v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			ReplicaName: "rv-1-1", PlanID: plan, ReplicaType: v1alpha1.ReplicaTypeDiskful,
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{
				Name: step, Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
				DatameshRevision: 5, StartedAt: new(metav1.Now()),
			}},
		}}
		if changingType {
			rv.Status.DatameshTransitions[0].ReplicaType = ""
			rv.Status.DatameshTransitions[0].FromReplicaType = v1alpha1.ReplicaTypeAccess
			rv.Status.DatameshTransitions[0].ToReplicaType = v1alpha1.ReplicaTypeDiskful
		}
		// Capture an already-settled progress report, as persisted by the previous pass.
		ProcessTransitionsWithCompleted(ctx, rv, nil, rvrs, nil, FeatureFlags{}, nil)
		before := rv.DeepCopy()
		for range 3 {
			_, contexts, completed := ProcessTransitionsWithCompleted(ctx, rv, nil, rvrs, nil, FeatureFlags{}, evacuees)
			Expect(rv.Status.DatameshTransitions).To(Equal(before.Status.DatameshTransitions))
			Expect(rv.Status.Datamesh).To(Equal(before.Status.Datamesh))
			Expect(rv.Status.DatameshRevision).To(Equal(before.Status.DatameshRevision))
			Expect(completed).To(BeEmpty())
			Expect(contexts[1].membershipMessage).To(ContainSubstring("evacuation would leave 1 live voter(s), quorum after removal needs 2"))
		}

		// The other nodes recover, but the subject still cannot confirm its old step.
		ProcessTransitionsWithCompleted(ctx, rv, nil, rvrs, nil, FeatureFlags{}, Evacuees{"rv-1-1": {}})
		Expect(rv.Status.Datamesh.FindMemberByName("rv-1-1")).To(BeNil())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeForceRemoveReplica))
		Expect(rv.Status.Datamesh.Quorum).To(Equal(byte(2)))
	}, Entry("Leave in 3D", false), Entry("ChangeRole after raising quorum in step 0", true))

	DescribeTable("keeps emergency cancellation before a blocking guard", func(ctx SpecContext, orphan bool) {
		rv, rvrs := evacuationLayout(3)
		rv.Status.Datamesh.Members[1].Type = v1alpha1.DatameshMemberTypeLiminalDiskful
		rvrs[1].Status.DatameshRevision = 0
		rvrs[0].Status.Peers = []v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{{
			Name: "rv-1-1", ConnectionState: v1alpha1.ConnectionStateConnected,
		}}
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveReplica,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			ReplicaName: "rv-1-1", PlanID: "remove-diskful/v1", ReplicaType: v1alpha1.ReplicaTypeDiskful,
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{
				Name: "D → D∅", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, DatameshRevision: 5,
			}},
		}}
		if orphan {
			rvrs = slices.Delete(rvrs, 1, 2)
		} else {
			rv.Status.DatameshReplicaRequests = []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkForceLeaveRequest("rv-1-1")}
		}
		_, contexts, completed := ProcessTransitionsWithCompleted(ctx, rv, nil, rvrs, nil, FeatureFlags{}, Evacuees{"rv-1-1": {}, "rv-1-2": {}})
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(completed).To(BeEmpty())
		Expect(rv.Status.Datamesh.FindMemberByName("rv-1-1")).NotTo(BeNil())
		Expect(contexts[1].membershipMessage).To(ContainSubstring("member is reachable over DRBD"))
	}, Entry("manual ForceLeave on an evacuee", false), Entry("orphan", true))

	DescribeTable("ignores Access and shadow members", func(ctx SpecContext, memberType v1alpha1.DatameshMemberType) {
		rv, rvrs := evacuationLayout(3)
		rv.Status.Datamesh.Members[2].Type = memberType
		rv.Status.Datamesh.Quorum = 2
		ProcessTransitionsWithCompleted(ctx, rv, nil, rvrs, nil, FeatureFlags{}, Evacuees{"rv-1-2": {}})
		Expect(rv.Status.Datamesh.FindMemberByName("rv-1-2")).NotTo(BeNil())
		Expect(slices.ContainsFunc(rv.Status.DatameshTransitions, func(t v1alpha1.ReplicatedVolumeDatameshTransition) bool {
			return t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeForceRemoveReplica
		})).To(BeFalse())
	},
		Entry("Access", v1alpha1.DatameshMemberTypeAccess),
		Entry("ShadowDiskful", v1alpha1.DatameshMemberTypeShadowDiskful),
		Entry("LiminalShadowDiskful", v1alpha1.DatameshMemberTypeLiminalShadowDiskful),
	)
})
