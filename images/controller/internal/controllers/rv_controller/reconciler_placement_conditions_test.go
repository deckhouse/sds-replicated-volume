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

package rvcontroller

import (
	"cmp"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// ──────────────────────────────────────────────────────────────────────────────
// Fixtures
//

// newPlacementTestRV builds a formed RV with the given topology and members, so the entries only
// have to describe what the replicas and the storage pool report back.
func newPlacementTestRV(
	topology v1alpha1.ReplicatedStorageClassTopology,
	members ...v1alpha1.DatameshMember,
) *v1alpha1.ReplicatedVolume {
	return &v1alpha1.ReplicatedVolume{
		Status: v1alpha1.ReplicatedVolumeStatus{
			DatameshRevision: 7,
			Configuration:    &v1alpha1.ReplicatedVolumeConfiguration{Topology: topology},
			Datamesh:         v1alpha1.ReplicatedVolumeDatamesh{Members: members},
		},
	}
}

// placementTestMember builds a datamesh member placed on the given node.
func placementTestMember(name string, typ v1alpha1.DatameshMemberType, nodeName string) v1alpha1.DatameshMember {
	return v1alpha1.DatameshMember{Name: name, Type: typ, NodeName: nodeName}
}

// withFormationTransition marks the volume as still forming. Formation acts on the datamesh as a
// whole and carries no replica name.
func withFormationTransition(rv *v1alpha1.ReplicatedVolume) *v1alpha1.ReplicatedVolume {
	return withReplicaTransition(rv, v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation, "")
}

// withReplicaTransition adds a datamesh transition acting on the named replica.
func withReplicaTransition(
	rv *v1alpha1.ReplicatedVolume,
	typ v1alpha1.ReplicatedVolumeDatameshTransitionType,
	replicaName string,
) *v1alpha1.ReplicatedVolume {
	rv.Status.DatameshTransitions = append(rv.Status.DatameshTransitions,
		v1alpha1.ReplicatedVolumeDatameshTransition{Type: typ, ReplicaName: replicaName})
	return rv
}

// withDeletionTimestamp marks the volume as being deleted.
func withDeletionTimestamp(rv *v1alpha1.ReplicatedVolume) *v1alpha1.ReplicatedVolume {
	rv.DeletionTimestamp = ptr.To(metav1.Now())
	return rv
}

// placementTestRSP builds a storage pool view. Eligible nodes are sorted by name because
// rspView.FindEligibleNode binary-searches them.
//
// zones is the pool's whole zone span, passed explicitly rather than derived from nodes: the
// view narrows EligibleNodes to one volume's nodes, so the two differ in exactly the cases the
// collapse rule cares about.
func placementTestRSP(zones []string, nodes ...v1alpha1.ReplicatedStoragePoolEligibleNode) *rspView {
	slices.SortFunc(nodes, func(a, b v1alpha1.ReplicatedStoragePoolEligibleNode) int {
		return cmp.Compare(a.NodeName, b.NodeName)
	})
	return &rspView{EligibleZones: zones, EligibleNodes: nodes}
}

// placementTestNode builds an eligible node in the given zone.
func placementTestNode(nodeName, zone string) v1alpha1.ReplicatedStoragePoolEligibleNode {
	return v1alpha1.ReplicatedStoragePoolEligibleNode{NodeName: nodeName, ZoneName: zone}
}

// placementTestRVR builds a replica of the given type with no conditions yet.
func placementTestRVR(name string, typ v1alpha1.ReplicaType) *v1alpha1.ReplicatedVolumeReplica {
	return &v1alpha1.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{Type: typ},
	}
}

// withScheduledCond records a Scheduled condition on the replica. obju.SetStatusCondition stamps
// ObservedGeneration from the object generation, so a replica built here reports a current verdict
// until withBumpedGeneration says otherwise.
func withScheduledCond(
	rvr *v1alpha1.ReplicatedVolumeReplica,
	status metav1.ConditionStatus,
	reason string,
) *v1alpha1.ReplicatedVolumeReplica {
	obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondScheduledType,
		Status:  status,
		Reason:  reason,
		Message: reason + " detail",
	})
	return rvr
}

// withEligibleCond records a SatisfyEligibleNodes condition on the replica.
func withEligibleCond(
	rvr *v1alpha1.ReplicatedVolumeReplica,
	status metav1.ConditionStatus,
	reason string,
) *v1alpha1.ReplicatedVolumeReplica {
	obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType,
		Status:  status,
		Reason:  reason,
		Message: reason + " detail",
	})
	return rvr
}

// withBumpedGeneration advances the replica generation past the conditions already recorded on it,
// which is exactly the state between the scheduler patching the spec and writing its verdict.
func withBumpedGeneration(rvr *v1alpha1.ReplicatedVolumeReplica) *v1alpha1.ReplicatedVolumeReplica {
	rvr.Generation++
	return rvr
}

// withDeletedRVR marks the replica as being deleted.
func withDeletedRVR(rvr *v1alpha1.ReplicatedVolumeReplica) *v1alpha1.ReplicatedVolumeReplica {
	rvr.DeletionTimestamp = ptr.To(metav1.Now())
	return rvr
}

// scheduledRVR is the common case: a diskful replica the scheduler has placed.
func scheduledRVR(name string) *v1alpha1.ReplicatedVolumeReplica {
	return withScheduledCond(
		placementTestRVR(name, v1alpha1.ReplicaTypeDiskful),
		metav1.ConditionTrue,
		v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonScheduled)
}

// satisfiedRVR is the common case: a diskful replica on a node the storage pool allows.
func satisfiedRVR(name string) *v1alpha1.ReplicatedVolumeReplica {
	return withEligibleCond(
		placementTestRVR(name, v1alpha1.ReplicaTypeDiskful),
		metav1.ConditionTrue,
		v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonSatisfied)
}

// ──────────────────────────────────────────────────────────────────────────────
// Scheduled
//

var _ = Describe("Scheduled condition", func() {
	Describe("computeScheduledReport", func() {
		DescribeTable("leaves the condition untouched",
			func(rv *v1alpha1.ReplicatedVolume, rvrs []*v1alpha1.ReplicatedVolumeReplica) {
				// The zero value, not merely present=false: a report that must not be applied
				// must not carry a status or a reason either.
				Expect(computeScheduledReport(rv, rvrs)).To(Equal(scheduledReport{}))
			},

			// Rule 1: deletion freezes the condition, like Resilient and Configured.
			Entry("deletion timestamp set",
				withDeletionTimestamp(newPlacementTestRV(v1alpha1.TopologyIgnored)),
				[]*v1alpha1.ReplicatedVolumeReplica{scheduledRVR("rv-0")}),

			// Rule 2: absent = not yet evaluated.
			Entry("no configuration yet",
				&v1alpha1.ReplicatedVolume{},
				[]*v1alpha1.ReplicatedVolumeReplica{scheduledRVR("rv-0")}),
		)

		DescribeTable("follows the rule order",
			func(rv *v1alpha1.ReplicatedVolume, rvrs []*v1alpha1.ReplicatedVolumeReplica, wantReason string) {
				report := computeScheduledReport(rv, rvrs)
				Expect(report.present).To(BeTrue())
				Expect(report.reason).To(Equal(wantReason))
				// Scheduled never reports Unknown: "not placed yet" is a known answer, so every
				// non-True verdict is a False (see the rule 5 rationale).
				if wantReason == v1alpha1.ReplicatedVolumeCondScheduledReasonAllReplicasScheduled {
					Expect(report.status).To(Equal(metav1.ConditionTrue))
				} else {
					Expect(report.status).To(Equal(metav1.ConditionFalse))
				}
			},

			// Rule 3.
			Entry("no replicas at all",
				newPlacementTestRV(v1alpha1.TopologyIgnored),
				nil,
				v1alpha1.ReplicatedVolumeCondScheduledReasonSchedulingInProgress),

			// Rule 3: Access replicas are not placed by the scheduler, so a volume holding only
			// Access replicas has nothing to report on.
			Entry("only Access replicas",
				newPlacementTestRV(v1alpha1.TopologyIgnored),
				[]*v1alpha1.ReplicatedVolumeReplica{
					placementTestRVR("rv-0", v1alpha1.ReplicaTypeAccess),
				},
				v1alpha1.ReplicatedVolumeCondScheduledReasonSchedulingInProgress),

			// Rule 4.
			Entry("a replica the scheduler could not place",
				newPlacementTestRV(v1alpha1.TopologyIgnored),
				[]*v1alpha1.ReplicatedVolumeReplica{
					scheduledRVR("rv-0"),
					withScheduledCond(placementTestRVR("rv-1", v1alpha1.ReplicaTypeDiskful),
						metav1.ConditionFalse,
						v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonSchedulingFailed),
				},
				v1alpha1.ReplicatedVolumeCondScheduledReasonReplicasNotScheduled),

			// Rule 4: an unavailable extender is a failure the operator can act on.
			Entry("scheduler extender unavailable",
				newPlacementTestRV(v1alpha1.TopologyIgnored),
				[]*v1alpha1.ReplicatedVolumeReplica{
					withScheduledCond(placementTestRVR("rv-0", v1alpha1.ReplicaTypeDiskful),
						metav1.ConditionFalse,
						v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonExtenderUnavailable),
				},
				v1alpha1.ReplicatedVolumeCondScheduledReasonReplicasNotScheduled),

			// Rule 4 outranks rule 5: a definite failure is the actionable cause.
			Entry("one replica failed, another still pending",
				newPlacementTestRV(v1alpha1.TopologyIgnored),
				[]*v1alpha1.ReplicatedVolumeReplica{
					placementTestRVR("rv-0", v1alpha1.ReplicaTypeDiskful),
					withScheduledCond(placementTestRVR("rv-1", v1alpha1.ReplicaTypeDiskful),
						metav1.ConditionFalse,
						v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonSchedulingFailed),
				},
				v1alpha1.ReplicatedVolumeCondScheduledReasonReplicasNotScheduled),

			// Rule 4 outranks formation: a formation blocked on placement must say so rather than
			// report "in progress" forever.
			Entry("failure during formation",
				withFormationTransition(newPlacementTestRV(v1alpha1.TopologyIgnored)),
				[]*v1alpha1.ReplicatedVolumeReplica{
					withScheduledCond(placementTestRVR("rv-0", v1alpha1.ReplicaTypeDiskful),
						metav1.ConditionFalse,
						v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonSchedulingFailed),
				},
				v1alpha1.ReplicatedVolumeCondScheduledReasonReplicasNotScheduled),

			// Rule 5: no verdict yet.
			Entry("replica has no Scheduled condition",
				newPlacementTestRV(v1alpha1.TopologyIgnored),
				[]*v1alpha1.ReplicatedVolumeReplica{
					placementTestRVR("rv-0", v1alpha1.ReplicaTypeDiskful),
				},
				v1alpha1.ReplicatedVolumeCondScheduledReasonSchedulingInProgress),

			// Rule 5.
			Entry("replica reports Unknown",
				newPlacementTestRV(v1alpha1.TopologyIgnored),
				[]*v1alpha1.ReplicatedVolumeReplica{
					withScheduledCond(placementTestRVR("rv-0", v1alpha1.ReplicaTypeDiskful),
						metav1.ConditionUnknown,
						v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonWaitingForReplicatedVolume),
				},
				v1alpha1.ReplicatedVolumeCondScheduledReasonSchedulingInProgress),

			// Rule 5: a stale success is not a success. The scheduler patches the spec before it
			// writes the verdict, so the stored condition still describes the previous placement.
			Entry("stale success verdict",
				newPlacementTestRV(v1alpha1.TopologyIgnored),
				[]*v1alpha1.ReplicatedVolumeReplica{
					withBumpedGeneration(scheduledRVR("rv-0")),
				},
				v1alpha1.ReplicatedVolumeCondScheduledReasonSchedulingInProgress),

			// Rule 5: a deliberate deferral is not a failure — nothing for an operator to fix.
			Entry("placement deferred by the resync limiter",
				newPlacementTestRV(v1alpha1.TopologyIgnored),
				[]*v1alpha1.ReplicatedVolumeReplica{
					withScheduledCond(placementTestRVR("rv-0", v1alpha1.ReplicaTypeDiskful),
						metav1.ConditionFalse,
						v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonWaitingForResyncSlot),
				},
				v1alpha1.ReplicatedVolumeCondScheduledReasonSchedulingInProgress),

			// Rule 5: an ordinary prerequisite is not a failure either.
			Entry("waiting for the volume",
				newPlacementTestRV(v1alpha1.TopologyIgnored),
				[]*v1alpha1.ReplicatedVolumeReplica{
					withScheduledCond(placementTestRVR("rv-0", v1alpha1.ReplicaTypeDiskful),
						metav1.ConditionFalse,
						v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonWaitingForReplicatedVolume),
				},
				v1alpha1.ReplicatedVolumeCondScheduledReasonSchedulingInProgress),

			// Rule 5: placement is settled but formation has not finished, so more replicas may
			// still be created.
			Entry("all placed, formation still running",
				withFormationTransition(newPlacementTestRV(v1alpha1.TopologyIgnored)),
				[]*v1alpha1.ReplicatedVolumeReplica{scheduledRVR("rv-0")},
				v1alpha1.ReplicatedVolumeCondScheduledReasonSchedulingInProgress),

			// Rule 6.
			Entry("every replica placed",
				newPlacementTestRV(v1alpha1.TopologyIgnored),
				[]*v1alpha1.ReplicatedVolumeReplica{
					scheduledRVR("rv-0"),
					withScheduledCond(placementTestRVR("rv-1", v1alpha1.ReplicaTypeTieBreaker),
						metav1.ConditionTrue,
						v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonScheduled),
				},
				v1alpha1.ReplicatedVolumeCondScheduledReasonAllReplicasScheduled),

			// Rule 6: an unplaced Access replica is outside the aggregation set.
			Entry("Access replica without a verdict is ignored",
				newPlacementTestRV(v1alpha1.TopologyIgnored),
				[]*v1alpha1.ReplicatedVolumeReplica{
					scheduledRVR("rv-0"),
					placementTestRVR("rv-1", v1alpha1.ReplicaTypeAccess),
				},
				v1alpha1.ReplicatedVolumeCondScheduledReasonAllReplicasScheduled),

			// Rule 6: a replica on its way out is not waiting for a placement.
			Entry("deleting replica without a verdict is ignored",
				newPlacementTestRV(v1alpha1.TopologyIgnored),
				[]*v1alpha1.ReplicatedVolumeReplica{
					scheduledRVR("rv-0"),
					withDeletedRVR(placementTestRVR("rv-1", v1alpha1.ReplicaTypeDiskful)),
				},
				v1alpha1.ReplicatedVolumeCondScheduledReasonAllReplicasScheduled),

			// A replica outside the datamesh still counts: the P2 heal tie-breaker is
			// unschedulable precisely while it has not joined.
			Entry("tie-breaker outside the datamesh cannot be placed",
				newPlacementTestRV(v1alpha1.TopologyIgnored,
					placementTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful, "n0")),
				[]*v1alpha1.ReplicatedVolumeReplica{
					scheduledRVR("rv-0"),
					withScheduledCond(placementTestRVR("rv-1", v1alpha1.ReplicaTypeTieBreaker),
						metav1.ConditionFalse,
						v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonSchedulingFailed),
				},
				v1alpha1.ReplicatedVolumeCondScheduledReasonReplicasNotScheduled),
		)

		It("says what a fully placed volume is still waiting for", func() {
			// N/N next to a False status reads like a broken condition unless the message
			// names what holds it down. Every replica that exists is placed here; formation
			// is the only thing left.
			report := computeScheduledReport(
				withFormationTransition(newPlacementTestRV(v1alpha1.TopologyIgnored)),
				[]*v1alpha1.ReplicatedVolumeReplica{scheduledRVR("rv-0"), scheduledRVR("rv-1")})

			Expect(report.message).To(Equal(
				"2/2 replicas scheduled: waiting for formation, which may still add replicas"))
		})

		It("names the unplaced replica even while formation runs", func() {
			// A named replica is the more specific answer and takes precedence over the
			// formation wording.
			report := computeScheduledReport(
				withFormationTransition(newPlacementTestRV(v1alpha1.TopologyIgnored)),
				[]*v1alpha1.ReplicatedVolumeReplica{
					scheduledRVR("rv-0"),
					placementTestRVR("rv-1", v1alpha1.ReplicaTypeDiskful),
				})

			Expect(report.message).To(Equal(
				"1/2 replicas scheduled: waiting for rv-1 (replica has no Scheduled condition yet)"))
		})

		It("names the same offender whatever order the replicas arrive in", func() {
			// The caller sorts by node name first, so rescheduling one replica reorders the
			// slice. Without the name tie-break the message would then move onto a different
			// replica that has not changed at all, costing a status patch for nothing.
			failed := func(name string) *v1alpha1.ReplicatedVolumeReplica {
				return withScheduledCond(placementTestRVR(name, v1alpha1.ReplicaTypeDiskful),
					metav1.ConditionFalse,
					v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonSchedulingFailed)
			}

			rv := newPlacementTestRV(v1alpha1.TopologyIgnored)
			forward := computeScheduledReport(rv,
				[]*v1alpha1.ReplicatedVolumeReplica{failed("rv-0"), failed("rv-1")})
			reversed := computeScheduledReport(rv,
				[]*v1alpha1.ReplicatedVolumeReplica{failed("rv-1"), failed("rv-0")})

			Expect(reversed).To(Equal(forward))
			Expect(forward.message).To(ContainSubstring("rv-0"))
		})

		It("counts placed replicas and names the offender", func() {
			report := computeScheduledReport(
				newPlacementTestRV(v1alpha1.TopologyIgnored),
				[]*v1alpha1.ReplicatedVolumeReplica{
					scheduledRVR("rv-0"),
					scheduledRVR("rv-1"),
					withScheduledCond(placementTestRVR("rv-2", v1alpha1.ReplicaTypeDiskful),
						metav1.ConditionFalse,
						v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonSchedulingFailed),
				})

			Expect(report.message).To(Equal("2/3 replicas scheduled: rv-2 (SchedulingFailed detail)"))
		})
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// SatisfyEligibleNodes
//

var _ = Describe("SatisfyEligibleNodes condition", func() {
	Describe("computeSatisfyEligibleNodesReport", func() {
		DescribeTable("leaves the condition and the violations untouched",
			func(rv *v1alpha1.ReplicatedVolume, rsp *rspView) {
				Expect(computeSatisfyEligibleNodesReport(rv, nil, rsp)).
					To(Equal(satisfyEligibleNodesReport{}))
			},

			// Rule 1: deletion freezes both, so the list never outlives the condition explaining it.
			Entry("deletion timestamp set",
				withDeletionTimestamp(newPlacementTestRV(v1alpha1.TopologyIgnored)),
				placementTestRSP(nil)),

			// Rule 2.
			Entry("no configuration yet",
				&v1alpha1.ReplicatedVolume{},
				placementTestRSP(nil)),

			// Rule 3: eligibility is undecidable without the pool. Reporting Unknown instead would
			// make rsc_controller count the volume as a placement conflict.
			Entry("no storage pool view",
				newPlacementTestRV(v1alpha1.TopologyIgnored),
				nil),
		)

		DescribeTable("follows the rule order",
			func(
				rv *v1alpha1.ReplicatedVolume,
				rvrs []*v1alpha1.ReplicatedVolumeReplica,
				rsp *rspView,
				wantStatus metav1.ConditionStatus,
				wantReason string,
			) {
				report := computeSatisfyEligibleNodesReport(rv, rvrs, rsp)
				Expect(report.present).To(BeTrue())
				Expect(report.status).To(Equal(wantStatus))
				Expect(report.reason).To(Equal(wantReason))
			},

			// Rule 4.
			Entry("every member where it belongs",
				newPlacementTestRV(v1alpha1.TopologyTransZonal,
					placementTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful, "n0"),
					placementTestMember("rv-1", v1alpha1.DatameshMemberTypeDiskful, "n1")),
				[]*v1alpha1.ReplicatedVolumeReplica{satisfiedRVR("rv-0"), satisfiedRVR("rv-1")},
				placementTestRSP([]string{"zone-a", "zone-b"},
					placementTestNode("n0", "zone-a"), placementTestNode("n1", "zone-b")),
				metav1.ConditionTrue,
				v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonSatisfyEligibleNodes),

			// Rule 4: a volume with no members has nothing misplaced.
			Entry("no members yet",
				newPlacementTestRV(v1alpha1.TopologyTransZonal),
				nil,
				placementTestRSP([]string{"zone-a"}),
				metav1.ConditionTrue,
				v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonSatisfyEligibleNodes),

			// Rule 6.
			Entry("replica on a node the pool does not allow",
				newPlacementTestRV(v1alpha1.TopologyIgnored,
					placementTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful, "n0")),
				[]*v1alpha1.ReplicatedVolumeReplica{
					withEligibleCond(placementTestRVR("rv-0", v1alpha1.ReplicaTypeDiskful),
						metav1.ConditionFalse,
						v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonNodeMismatch),
				},
				placementTestRSP(nil),
				metav1.ConditionFalse,
				v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonInConflictWithEligibleNodes),

			// Rule 6.
			Entry("replica on a volume group the pool does not allow",
				newPlacementTestRV(v1alpha1.TopologyIgnored,
					placementTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful, "n0")),
				[]*v1alpha1.ReplicatedVolumeReplica{
					withEligibleCond(placementTestRVR("rv-0", v1alpha1.ReplicaTypeDiskful),
						metav1.ConditionFalse,
						v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonLVMVolumeGroupMismatch),
				},
				placementTestRSP(nil, placementTestNode("n0", "")),
				metav1.ConditionFalse,
				v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonInConflictWithEligibleNodes),

			// Rule 6.
			Entry("replica on a thin pool the pool does not allow",
				newPlacementTestRV(v1alpha1.TopologyIgnored,
					placementTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful, "n0")),
				[]*v1alpha1.ReplicatedVolumeReplica{
					withEligibleCond(placementTestRVR("rv-0", v1alpha1.ReplicaTypeDiskful),
						metav1.ConditionFalse,
						v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonThinPoolMismatch),
				},
				placementTestRSP(nil, placementTestNode("n0", "")),
				metav1.ConditionFalse,
				v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonInConflictWithEligibleNodes),

			// A replica that has not been evaluated is not a violation: the replica controller
			// simply has not judged this placement yet.
			Entry("replica waiting for the volume is not a violation",
				newPlacementTestRV(v1alpha1.TopologyIgnored,
					placementTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful, "n0")),
				[]*v1alpha1.ReplicatedVolumeReplica{
					withEligibleCond(placementTestRVR("rv-0", v1alpha1.ReplicaTypeDiskful),
						metav1.ConditionUnknown,
						v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonWaitingForReplicatedVolume),
				},
				placementTestRSP(nil, placementTestNode("n0", "")),
				metav1.ConditionTrue,
				v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonSatisfyEligibleNodes),

			Entry("member without a replica is not a violation",
				newPlacementTestRV(v1alpha1.TopologyIgnored,
					placementTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful, "n0")),
				nil,
				placementTestRSP(nil, placementTestNode("n0", "")),
				metav1.ConditionTrue,
				v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonSatisfyEligibleNodes),

			// An Access member sits with its consumer, not where the scheduler put it, so its
			// node leaving the pool is a conflict nobody can act on: it cannot be moved, no
			// transition removes it, and the cleanup will not touch it while it is attached.
			// Reporting it would hold the storage class out of Ready for the life of the
			// workload.
			Entry("Access member off the pool is not a violation",
				newPlacementTestRV(v1alpha1.TopologyIgnored,
					placementTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful, "n0"),
					placementTestMember("rv-1", v1alpha1.DatameshMemberTypeAccess, "n1")),
				[]*v1alpha1.ReplicatedVolumeReplica{
					satisfiedRVR("rv-0"),
					withEligibleCond(placementTestRVR("rv-1", v1alpha1.ReplicaTypeAccess),
						metav1.ConditionFalse,
						v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonNodeMismatch),
				},
				placementTestRSP(nil, placementTestNode("n0", "")),
				metav1.ConditionTrue,
				v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonSatisfyEligibleNodes),

			// A stale verdict describes the previous placement, so it is not a verdict.
			Entry("stale eligibility verdict is not a violation",
				newPlacementTestRV(v1alpha1.TopologyIgnored,
					placementTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful, "n0")),
				[]*v1alpha1.ReplicatedVolumeReplica{
					withBumpedGeneration(
						withEligibleCond(placementTestRVR("rv-0", v1alpha1.ReplicaTypeDiskful),
							metav1.ConditionFalse,
							v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonNodeMismatch)),
				},
				placementTestRSP(nil, placementTestNode("n0", "")),
				metav1.ConditionTrue,
				v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonSatisfyEligibleNodes),

			// Rule 5: the offending member itself is being taken out.
			Entry("offender being removed by RemoveReplica",
				withReplicaTransition(
					newPlacementTestRV(v1alpha1.TopologyIgnored,
						placementTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful, "n0")),
					v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveReplica, "rv-0"),
				[]*v1alpha1.ReplicatedVolumeReplica{
					withEligibleCond(placementTestRVR("rv-0", v1alpha1.ReplicaTypeDiskful),
						metav1.ConditionFalse,
						v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonNodeMismatch),
				},
				placementTestRSP(nil),
				metav1.ConditionFalse,
				v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonConflictResolutionInProgress),

			// Rule 5: the emergency path counts too.
			Entry("offender being removed by ForceRemoveReplica",
				withReplicaTransition(
					newPlacementTestRV(v1alpha1.TopologyIgnored,
						placementTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful, "n0")),
					v1alpha1.ReplicatedVolumeDatameshTransitionTypeForceRemoveReplica, "rv-0"),
				[]*v1alpha1.ReplicatedVolumeReplica{
					withEligibleCond(placementTestRVR("rv-0", v1alpha1.ReplicaTypeDiskful),
						metav1.ConditionFalse,
						v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonNodeMismatch),
				},
				placementTestRSP(nil),
				metav1.ConditionFalse,
				v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonConflictResolutionInProgress),

			// Membership churn as such is not a repair: this addition moves no misplaced
			// member, and calling it one would tell the operator a volume is being fixed while
			// nothing is fixing it.
			Entry("addition of an unrelated replica is not a repair",
				withReplicaTransition(
					newPlacementTestRV(v1alpha1.TopologyIgnored,
						placementTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful, "n0")),
					v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica, "rv-9"),
				[]*v1alpha1.ReplicatedVolumeReplica{
					withEligibleCond(placementTestRVR("rv-0", v1alpha1.ReplicaTypeDiskful),
						metav1.ConditionFalse,
						v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonNodeMismatch),
				},
				placementTestRSP(nil),
				metav1.ConditionFalse,
				v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonInConflictWithEligibleNodes),

			// A removal of the right kind, but of somebody else.
			Entry("removal of a replica that is not the offender is not a repair",
				withReplicaTransition(
					newPlacementTestRV(v1alpha1.TopologyIgnored,
						placementTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful, "n0")),
					v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveReplica, "rv-9"),
				[]*v1alpha1.ReplicatedVolumeReplica{
					withEligibleCond(placementTestRVR("rv-0", v1alpha1.ReplicaTypeDiskful),
						metav1.ConditionFalse,
						v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonNodeMismatch),
				},
				placementTestRSP(nil),
				metav1.ConditionFalse,
				v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonInConflictWithEligibleNodes),

			// An unrelated transition type does not count as a repair either.
			Entry("conflict with an unrelated transition in flight",
				withReplicaTransition(
					newPlacementTestRV(v1alpha1.TopologyIgnored,
						placementTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful, "n0")),
					v1alpha1.ReplicatedVolumeDatameshTransitionTypeResizeVolume, "rv-0"),
				[]*v1alpha1.ReplicatedVolumeReplica{
					withEligibleCond(placementTestRVR("rv-0", v1alpha1.ReplicaTypeDiskful),
						metav1.ConditionFalse,
						v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonNodeMismatch),
				},
				placementTestRSP(nil),
				metav1.ConditionFalse,
				v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonInConflictWithEligibleNodes),
		)

		DescribeTable("judges topology across the member set",
			func(
				rv *v1alpha1.ReplicatedVolume,
				rvrs []*v1alpha1.ReplicatedVolumeReplica,
				rsp *rspView,
				wantOffenders []string,
			) {
				report := computeSatisfyEligibleNodesReport(rv, rvrs, rsp)

				names := make([]string, 0, len(report.violations))
				for _, v := range report.violations {
					Expect(v.Reason).To(Equal(
						v1alpha1.ReplicatedVolumeEligibleNodesViolationReasonNodeTopologyMismatch))
					names = append(names, v.ReplicaName)
				}
				Expect(names).To(Equal(wantOffenders))
			},

			// Two voters in one zone: each node is eligible on its own, and only the volume can
			// see that the pair breaks TransZonal.
			// The composite mode is legal and must stay silent: this is exactly the layout the
			// RSC validator admits for FTT=2/GMDR=2 on three zones, and the one the scheduler's
			// round-robin produces there. A voters-per-zone rule would flag four of these five
			// members on a healthy volume.
			Entry("TransZonal composite 2D|2D|1D is not a violation",
				newPlacementTestRV(v1alpha1.TopologyTransZonal,
					placementTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful, "n0"),
					placementTestMember("rv-1", v1alpha1.DatameshMemberTypeDiskful, "n1"),
					placementTestMember("rv-2", v1alpha1.DatameshMemberTypeDiskful, "n2"),
					placementTestMember("rv-3", v1alpha1.DatameshMemberTypeDiskful, "n3"),
					placementTestMember("rv-4", v1alpha1.DatameshMemberTypeDiskful, "n4")),
				nil,
				placementTestRSP([]string{"zone-a", "zone-b", "zone-c"},
					placementTestNode("n0", "zone-a"),
					placementTestNode("n1", "zone-a"),
					placementTestNode("n2", "zone-b"),
					placementTestNode("n3", "zone-b"),
					placementTestNode("n4", "zone-c")),
				[]string{}),

			// The one distribution both the guard and the validator forbid. Every voter is
			// reported: the fault is in the set, no single member is the misplaced one.
			Entry("TransZonal with every voter collapsed into one zone",
				newPlacementTestRV(v1alpha1.TopologyTransZonal,
					placementTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful, "n0"),
					placementTestMember("rv-1", v1alpha1.DatameshMemberTypeDiskful, "n1"),
					placementTestMember("rv-2", v1alpha1.DatameshMemberTypeDiskful, "n2")),
				nil,
				placementTestRSP([]string{"zone-a", "zone-b", "zone-c"},
					placementTestNode("n0", "zone-a"),
					placementTestNode("n1", "zone-a"),
					placementTestNode("n2", "zone-a")),
				[]string{"rv-0", "rv-1", "rv-2"}),

			// A lone voter is trivially "all in one zone" -- true of a volume mid-formation or
			// down to its last replica, neither of which is misplaced.
			Entry("TransZonal with a single voter is not a collapse",
				newPlacementTestRV(v1alpha1.TopologyTransZonal,
					placementTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful, "n0"),
					placementTestMember("rv-1", v1alpha1.DatameshMemberTypeTieBreaker, "n1")),
				nil,
				placementTestRSP([]string{"zone-a", "zone-b"},
					placementTestNode("n0", "zone-a"),
					placementTestNode("n1", "zone-a")),
				[]string{}),

			// A pool with a single zone left the volume nowhere to spread to.
			Entry("TransZonal collapse in a single-zone pool is not a violation",
				newPlacementTestRV(v1alpha1.TopologyTransZonal,
					placementTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful, "n0"),
					placementTestMember("rv-1", v1alpha1.DatameshMemberTypeDiskful, "n1")),
				nil,
				placementTestRSP([]string{"zone-a"},
					placementTestNode("n0", "zone-a"),
					placementTestNode("n1", "zone-a")),
				[]string{}),

			// A zone the pool does not declare is outside the intended spread.
			Entry("TransZonal with a voter in an undeclared zone",
				newPlacementTestRV(v1alpha1.TopologyTransZonal,
					placementTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful, "n0"),
					placementTestMember("rv-1", v1alpha1.DatameshMemberTypeDiskful, "n1")),
				[]*v1alpha1.ReplicatedVolumeReplica{satisfiedRVR("rv-0"), satisfiedRVR("rv-1")},
				placementTestRSP([]string{"zone-a", "zone-b"},
					placementTestNode("n0", "zone-a"),
					placementTestNode("n1", "zone-c")),
				[]string{"rv-1"}),

			// A node carrying no zone cannot satisfy a zonal spread.
			Entry("TransZonal with an unzoned node",
				newPlacementTestRV(v1alpha1.TopologyTransZonal,
					placementTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful, "n0"),
					placementTestMember("rv-1", v1alpha1.DatameshMemberTypeDiskful, "n1")),
				[]*v1alpha1.ReplicatedVolumeReplica{satisfiedRVR("rv-0"), satisfiedRVR("rv-1")},
				placementTestRSP([]string{"zone-a"},
					placementTestNode("n0", "zone-a"),
					placementTestNode("n1", "")),
				[]string{"rv-1"}),

			// A pool with no zoned nodes at all offered the volume no shape to take, and the
			// storage class already reports it: its validation groups eligible nodes by zone
			// and refuses TransZonal below two. Listing every voter here would point the
			// operator at the volume when the fault is in the pool.
			Entry("TransZonal on a pool with no zoned nodes is not a violation",
				newPlacementTestRV(v1alpha1.TopologyTransZonal,
					placementTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful, "n0"),
					placementTestMember("rv-1", v1alpha1.DatameshMemberTypeDiskful, "n1")),
				nil,
				placementTestRSP(nil,
					placementTestNode("n0", ""),
					placementTestNode("n1", "")),
				[]string{}),

			// Tie-breakers carry no data, so spreading them is the transition guards' business.
			Entry("TransZonal tie-breaker sharing a zone with a voter",
				newPlacementTestRV(v1alpha1.TopologyTransZonal,
					placementTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful, "n0"),
					placementTestMember("rv-1", v1alpha1.DatameshMemberTypeDiskful, "n1"),
					placementTestMember("rv-2", v1alpha1.DatameshMemberTypeTieBreaker, "n2")),
				[]*v1alpha1.ReplicatedVolumeReplica{
					satisfiedRVR("rv-0"), satisfiedRVR("rv-1"), satisfiedRVR("rv-2"),
				},
				placementTestRSP([]string{"zone-a", "zone-b"},
					placementTestNode("n0", "zone-a"),
					placementTestNode("n1", "zone-b"),
					placementTestNode("n2", "zone-a")),
				[]string{}),

			// Zonal: the primary zone is the one holding the most voters.
			Entry("Zonal with a voter outside the home zone",
				newPlacementTestRV(v1alpha1.TopologyZonal,
					placementTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful, "n0"),
					placementTestMember("rv-1", v1alpha1.DatameshMemberTypeDiskful, "n1"),
					placementTestMember("rv-2", v1alpha1.DatameshMemberTypeDiskful, "n2")),
				[]*v1alpha1.ReplicatedVolumeReplica{
					satisfiedRVR("rv-0"), satisfiedRVR("rv-1"), satisfiedRVR("rv-2"),
				},
				placementTestRSP([]string{"zone-a", "zone-b"},
					placementTestNode("n0", "zone-a"),
					placementTestNode("n1", "zone-a"),
					placementTestNode("n2", "zone-b")),
				[]string{"rv-2"}),

			// The shape a two-replica Zonal volume takes when one of its two nodes is relabelled
			// into another zone — the likeliest Zonal violation there is, and the one a
			// most-voters rule cannot see, because with one voter per zone every zone ties for
			// the maximum.
			Entry("Zonal split evenly between two zones",
				newPlacementTestRV(v1alpha1.TopologyZonal,
					placementTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful, "n0"),
					placementTestMember("rv-1", v1alpha1.DatameshMemberTypeDiskful, "n1")),
				nil,
				placementTestRSP([]string{"zone-a", "zone-b"},
					placementTestNode("n0", "zone-a"),
					placementTestNode("n1", "zone-b")),
				[]string{"rv-1"}),

			// The same blind spot with even counts above one.
			Entry("Zonal split evenly two against two",
				newPlacementTestRV(v1alpha1.TopologyZonal,
					placementTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful, "n0"),
					placementTestMember("rv-1", v1alpha1.DatameshMemberTypeDiskful, "n1"),
					placementTestMember("rv-2", v1alpha1.DatameshMemberTypeDiskful, "n2"),
					placementTestMember("rv-3", v1alpha1.DatameshMemberTypeDiskful, "n3")),
				nil,
				placementTestRSP([]string{"zone-a", "zone-b"},
					placementTestNode("n0", "zone-a"),
					placementTestNode("n1", "zone-a"),
					placementTestNode("n2", "zone-b"),
					placementTestNode("n3", "zone-b")),
				[]string{"rv-2", "rv-3"}),

			// An unzoned node is the outlier, not the home: consolidating onto nodes that carry
			// no zone satisfies nothing, and the "" bucket would otherwise win the tie against
			// a real zone outright ("" sorts first).
			Entry("Zonal split between a real zone and an unzoned node",
				newPlacementTestRV(v1alpha1.TopologyZonal,
					placementTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful, "n0"),
					placementTestMember("rv-1", v1alpha1.DatameshMemberTypeDiskful, "n1")),
				nil,
				placementTestRSP([]string{"zone-a"},
					placementTestNode("n0", "zone-a"),
					placementTestNode("n1", "")),
				[]string{"rv-1"}),

			// The home is the largest REAL zone, not the largest one: reporting the single
			// zoned voter would point at the only destination that fixes nothing.
			Entry("Zonal with the unzoned bucket larger than the real zone",
				newPlacementTestRV(v1alpha1.TopologyZonal,
					placementTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful, "n0"),
					placementTestMember("rv-1", v1alpha1.DatameshMemberTypeDiskful, "n1"),
					placementTestMember("rv-2", v1alpha1.DatameshMemberTypeDiskful, "n2")),
				nil,
				placementTestRSP([]string{"zone-a"},
					placementTestNode("n0", "zone-a"),
					placementTestNode("n1", ""),
					placementTestNode("n2", "")),
				[]string{"rv-1", "rv-2"}),

			// All voters in one zone is the contract satisfied, whichever zone it is.
			Entry("Zonal with every voter in one zone is not a violation",
				newPlacementTestRV(v1alpha1.TopologyZonal,
					placementTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful, "n0"),
					placementTestMember("rv-1", v1alpha1.DatameshMemberTypeDiskful, "n1")),
				nil,
				placementTestRSP([]string{"zone-a", "zone-b"},
					placementTestNode("n0", "zone-b"),
					placementTestNode("n1", "zone-b")),
				[]string{}),

			// Ignored imposes no placement constraint, so the same skew is not a violation.
			Entry("Ignored topology never reports a zone violation",
				newPlacementTestRV(v1alpha1.TopologyIgnored,
					placementTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful, "n0"),
					placementTestMember("rv-1", v1alpha1.DatameshMemberTypeDiskful, "n1")),
				[]*v1alpha1.ReplicatedVolumeReplica{satisfiedRVR("rv-0"), satisfiedRVR("rv-1")},
				placementTestRSP([]string{"zone-a"},
					placementTestNode("n0", "zone-a"),
					placementTestNode("n1", "zone-a")),
				[]string{}),

			// A node outside the pool view falls back to the zone recorded at join time, which
			// is what lets the collapse be seen at all here.
			Entry("TransZonal falls back to the member zone for an uncovered node",
				newPlacementTestRV(v1alpha1.TopologyTransZonal,
					v1alpha1.DatameshMember{
						Name: "rv-0", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "n0", Zone: "zone-a",
					},
					v1alpha1.DatameshMember{
						Name: "rv-1", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "n1", Zone: "zone-a",
					}),
				nil,
				placementTestRSP([]string{"zone-a", "zone-b"}),
				[]string{"rv-0", "rv-1"}),
		)

		It("reports every voter outside the home zone of a split", func() {
			// Three occupied zones, the bulk tied between two of them. Everything outside the
			// home zone is reported, not just the odd one out: the volume has to end up in a
			// single zone, so the two voters in zone-b are as misplaced as the one in zone-c.
			report := computeSatisfyEligibleNodesReport(
				newPlacementTestRV(v1alpha1.TopologyZonal,
					placementTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful, "n0"),
					placementTestMember("rv-1", v1alpha1.DatameshMemberTypeDiskful, "n1"),
					placementTestMember("rv-2", v1alpha1.DatameshMemberTypeDiskful, "n2"),
					placementTestMember("rv-3", v1alpha1.DatameshMemberTypeDiskful, "n3"),
					placementTestMember("rv-4", v1alpha1.DatameshMemberTypeDiskful, "n4")),
				nil,
				placementTestRSP([]string{"zone-a", "zone-b", "zone-c"},
					placementTestNode("n0", "zone-a"),
					placementTestNode("n1", "zone-a"),
					placementTestNode("n2", "zone-b"),
					placementTestNode("n3", "zone-b"),
					placementTestNode("n4", "zone-c")))

			names := make([]string, 0, len(report.violations))
			for _, v := range report.violations {
				names = append(names, v.ReplicaName)
			}
			Expect(names).To(Equal([]string{"rv-2", "rv-3", "rv-4"}))
			Expect(report.message).To(Equal(
				`3/5 members are not on eligible nodes: replica rv-2 on node n2 ` +
					`(voters are split across 3 zones: this one is in "zone-b", 2 of 5 are in "zone-a")`))
		})

		It("points at the real zone, not at the unzoned nodes", func() {
			// Without skipping the "" bucket the message would read `1 of 2 are in ""` and send
			// the operator to consolidate onto nodes that carry no zone at all.
			report := computeSatisfyEligibleNodesReport(
				newPlacementTestRV(v1alpha1.TopologyZonal,
					placementTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful, "n0"),
					placementTestMember("rv-1", v1alpha1.DatameshMemberTypeDiskful, "n1")),
				nil,
				placementTestRSP([]string{"zone-a"},
					placementTestNode("n0", "zone-a"),
					placementTestNode("n1", "")))

			Expect(report.message).To(Equal(
				`1/2 members are not on eligible nodes: replica rv-1 on node n1 ` +
					`(voters are split across 2 zones: this one is in "", 1 of 2 are in "zone-a")`))
		})

		It("names where the bulk of the voters sits", func() {
			report := computeSatisfyEligibleNodesReport(
				newPlacementTestRV(v1alpha1.TopologyZonal,
					placementTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful, "n0"),
					placementTestMember("rv-1", v1alpha1.DatameshMemberTypeDiskful, "n1"),
					placementTestMember("rv-2", v1alpha1.DatameshMemberTypeDiskful, "n2")),
				nil,
				placementTestRSP([]string{"zone-a", "zone-b"},
					placementTestNode("n0", "zone-a"),
					placementTestNode("n1", "zone-a"),
					placementTestNode("n2", "zone-b")))

			Expect(report.message).To(Equal(
				`1/3 members are not on eligible nodes: replica rv-2 on node n2 ` +
					`(voters are split across 2 zones: this one is in "zone-b", 2 of 3 are in "zone-a")`))
		})

		It("counts only the members it judged", func() {
			// The Access member is skipped, so it must not appear in the denominator either --
			// "1/2" would count a member whose placement was never examined.
			report := computeSatisfyEligibleNodesReport(
				newPlacementTestRV(v1alpha1.TopologyIgnored,
					placementTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful, "n0"),
					placementTestMember("rv-1", v1alpha1.DatameshMemberTypeAccess, "n1")),
				[]*v1alpha1.ReplicatedVolumeReplica{
					withEligibleCond(placementTestRVR("rv-0", v1alpha1.ReplicaTypeDiskful),
						metav1.ConditionFalse,
						v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonNodeMismatch),
					satisfiedRVR("rv-1"),
				},
				placementTestRSP(nil))

			Expect(report.message).To(HavePrefix("1/1 members are not on eligible nodes:"))
		})

		It("says so when every member is an Access one", func() {
			report := computeSatisfyEligibleNodesReport(
				newPlacementTestRV(v1alpha1.TopologyIgnored,
					placementTestMember("rv-0", v1alpha1.DatameshMemberTypeAccess, "n0")),
				nil,
				placementTestRSP(nil))

			Expect(report.status).To(Equal(metav1.ConditionTrue))
			Expect(report.message).To(Equal(
				"Datamesh holds only Access members, whose placement is not judged"))
		})

		It("does not claim all zero members are placed correctly", func() {
			// "All 0 datamesh members are on eligible nodes" is true and useless; a volume
			// whose datamesh is still empty should say that instead.
			report := computeSatisfyEligibleNodesReport(
				newPlacementTestRV(v1alpha1.TopologyTransZonal), nil, placementTestRSP([]string{"zone-a"}))

			Expect(report.status).To(Equal(metav1.ConditionTrue))
			Expect(report.message).To(Equal("Datamesh has no members yet"))
		})

		It("prefers node eligibility over topology for the same member", func() {
			// Both members sit in a zone the pool does not declare, so both qualify for a
			// topology violation; rv-1 is ineligible on top of that. The eligibility verdict is
			// the more actionable one and wins, and the member is reported once, not twice.
			report := computeSatisfyEligibleNodesReport(
				newPlacementTestRV(v1alpha1.TopologyTransZonal,
					placementTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful, "n0"),
					placementTestMember("rv-1", v1alpha1.DatameshMemberTypeDiskful, "n1")),
				[]*v1alpha1.ReplicatedVolumeReplica{
					satisfiedRVR("rv-0"),
					withEligibleCond(placementTestRVR("rv-1", v1alpha1.ReplicaTypeDiskful),
						metav1.ConditionFalse,
						v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonNodeMismatch),
				},
				placementTestRSP([]string{"zone-a", "zone-b"},
					placementTestNode("n0", "zone-c"),
					placementTestNode("n1", "zone-c")))

			Expect(report.violations).To(Equal([]v1alpha1.ReplicatedVolumeEligibleNodesViolation{
				{
					NodeName:    "n0",
					ReplicaName: "rv-0",
					Reason:      v1alpha1.ReplicatedVolumeEligibleNodesViolationReasonNodeTopologyMismatch,
				},
				{
					NodeName:    "n1",
					ReplicaName: "rv-1",
					Reason:      v1alpha1.ReplicatedVolumeEligibleNodesViolationReasonOutOfEligibleNodes,
				},
			}))
		})

		It("sorts violations by replica name and names the first one", func() {
			// Members are listed out of order on purpose: the published list and the message must
			// not depend on where a member happens to sit in status.datamesh.members.
			report := computeSatisfyEligibleNodesReport(
				newPlacementTestRV(v1alpha1.TopologyIgnored,
					placementTestMember("rv-2", v1alpha1.DatameshMemberTypeDiskful, "n2"),
					placementTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful, "n0"),
					placementTestMember("rv-1", v1alpha1.DatameshMemberTypeDiskful, "n1")),
				[]*v1alpha1.ReplicatedVolumeReplica{
					withEligibleCond(placementTestRVR("rv-2", v1alpha1.ReplicaTypeDiskful),
						metav1.ConditionFalse,
						v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonNodeMismatch),
					satisfiedRVR("rv-0"),
					withEligibleCond(placementTestRVR("rv-1", v1alpha1.ReplicaTypeDiskful),
						metav1.ConditionFalse,
						v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonNodeMismatch),
				},
				placementTestRSP(nil))

			names := make([]string, 0, len(report.violations))
			for _, v := range report.violations {
				names = append(names, v.ReplicaName)
			}
			Expect(names).To(Equal([]string{"rv-1", "rv-2"}))
			Expect(report.message).To(Equal(
				"2/3 members are not on eligible nodes: replica rv-1 on node n1 (NodeMismatch detail)"))
		})
	})

	Describe("applyScheduledCond", func() {
		It("leaves the condition alone when the report must not be applied", func() {
			rv := newPlacementTestRV(v1alpha1.TopologyIgnored)
			applyScheduledCond(rv, scheduledReport{
				present: true,
				status:  metav1.ConditionTrue,
				reason:  v1alpha1.ReplicatedVolumeCondScheduledReasonAllReplicasScheduled,
				message: "All 1 replicas are scheduled",
			})
			before := rv.DeepCopy()

			Expect(applyScheduledCond(rv, scheduledReport{})).To(BeFalse())
			Expect(rv.Status.Conditions).To(Equal(before.Status.Conditions))
		})
	})

	Describe("applySatisfyEligibleNodesCond", func() {
		It("leaves the condition alone when the report must not be applied", func() {
			rv := newPlacementTestRV(v1alpha1.TopologyIgnored)
			applySatisfyEligibleNodesCond(rv, satisfyEligibleNodesReport{
				present: true,
				status:  metav1.ConditionTrue,
				reason:  v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonSatisfyEligibleNodes,
				message: "All 1 datamesh members are on eligible nodes",
			})
			before := rv.DeepCopy()

			Expect(applySatisfyEligibleNodesCond(rv, satisfyEligibleNodesReport{})).To(BeFalse())
			Expect(rv.Status.Conditions).To(Equal(before.Status.Conditions))
		})
	})

	Describe("applyEligibleNodesViolations", func() {
		It("publishes the computed violations", func() {
			rv := newPlacementTestRV(v1alpha1.TopologyIgnored)
			violations := []v1alpha1.ReplicatedVolumeEligibleNodesViolation{
				{
					NodeName:    "n0",
					ReplicaName: "rv-0",
					Reason:      v1alpha1.ReplicatedVolumeEligibleNodesViolationReasonOutOfEligibleNodes,
				},
				{
					NodeName:    "n1",
					ReplicaName: "rv-1",
					Reason:      v1alpha1.ReplicatedVolumeEligibleNodesViolationReasonNodeTopologyMismatch,
				},
			}

			Expect(applyEligibleNodesViolations(rv,
				satisfyEligibleNodesReport{present: true, violations: violations})).To(BeTrue())
			Expect(rv.Status.EligibleNodesViolations).To(Equal(violations))
		})

		It("reports no change when the list is unchanged", func() {
			// The equality guard is what keeps a volume with a standing conflict from
			// reporting a status change on every pass: without it reconcilePlacementConditions
			// would return changed forever and the reconciler would patch the status each
			// time, for every conflicting volume in the fleet.
			rv := newPlacementTestRV(v1alpha1.TopologyIgnored)
			report := satisfyEligibleNodesReport{
				present: true,
				violations: []v1alpha1.ReplicatedVolumeEligibleNodesViolation{{
					NodeName:    "n0",
					ReplicaName: "rv-0",
					Reason:      v1alpha1.ReplicatedVolumeEligibleNodesViolationReasonOutOfEligibleNodes,
				}},
			}

			Expect(applyEligibleNodesViolations(rv, report)).To(BeTrue())
			Expect(applyEligibleNodesViolations(rv, report)).To(BeFalse())
			Expect(rv.Status.EligibleNodesViolations).To(Equal(report.violations))
		})

		It("clears the list once the volume is back within its constraints", func() {
			rv := newPlacementTestRV(v1alpha1.TopologyIgnored)
			rv.Status.EligibleNodesViolations = []v1alpha1.ReplicatedVolumeEligibleNodesViolation{{
				NodeName:    "n0",
				ReplicaName: "rv-0",
				Reason:      v1alpha1.ReplicatedVolumeEligibleNodesViolationReasonOutOfEligibleNodes,
			}}

			Expect(applyEligibleNodesViolations(rv, satisfyEligibleNodesReport{present: true})).To(BeTrue())
			Expect(rv.Status.EligibleNodesViolations).To(BeEmpty())
		})

		It("leaves the list alone when the report must not be applied", func() {
			rv := newPlacementTestRV(v1alpha1.TopologyIgnored)
			existing := []v1alpha1.ReplicatedVolumeEligibleNodesViolation{{
				NodeName:    "n0",
				ReplicaName: "rv-0",
				Reason:      v1alpha1.ReplicatedVolumeEligibleNodesViolationReasonOutOfEligibleNodes,
			}}
			rv.Status.EligibleNodesViolations = existing

			Expect(applyEligibleNodesViolations(rv, satisfyEligibleNodesReport{})).To(BeFalse())
			Expect(rv.Status.EligibleNodesViolations).To(Equal(existing))
		})

		It("does not alias the report", func() {
			rv := newPlacementTestRV(v1alpha1.TopologyIgnored)
			report := satisfyEligibleNodesReport{
				present: true,
				violations: []v1alpha1.ReplicatedVolumeEligibleNodesViolation{{
					NodeName:    "n0",
					ReplicaName: "rv-0",
					Reason:      v1alpha1.ReplicatedVolumeEligibleNodesViolationReasonOutOfEligibleNodes,
				}},
			}

			Expect(applyEligibleNodesViolations(rv, report)).To(BeTrue())
			rv.Status.EligibleNodesViolations[0].NodeName = "mutated"
			Expect(report.violations[0].NodeName).To(Equal("n0"))
		})
	})
})

var _ = Describe("reconcilePlacementConditions", func() {
	// The helper performs no I/O, so it needs no client.
	var r *Reconciler

	BeforeEach(func() {
		r = &Reconciler{}
	})

	It("writes both conditions and reports the change", func(ctx SpecContext) {
		// The positive direction is what pins the wiring. Each apply call sits on the LEFT of
		// its `|| changed`, so none of them is skipped once an earlier one reported a change;
		// swapping the operands would short-circuit the later calls and silently drop a
		// condition, which only a pass that expects every write can catch.
		rv := newPlacementTestRV(v1alpha1.TopologyTransZonal,
			placementTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful, "n0"),
			placementTestMember("rv-1", v1alpha1.DatameshMemberTypeDiskful, "n1"))
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			withEligibleCond(scheduledRVR("rv-0"), metav1.ConditionTrue,
				v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonSatisfied),
			withEligibleCond(scheduledRVR("rv-1"), metav1.ConditionTrue,
				v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonSatisfied),
		}
		rsp := placementTestRSP([]string{"zone-a", "zone-b"},
			placementTestNode("n0", "zone-a"), placementTestNode("n1", "zone-b"))

		outcome := r.reconcilePlacementConditions(ctx, rv, rvrs, rsp)

		Expect(outcome.Error()).To(BeNil())
		Expect(outcome.DidChange()).To(BeTrue())
		Expect(obju.StatusCondition(rv, v1alpha1.ReplicatedVolumeCondScheduledType).
			IsTrue().ReasonEqual(v1alpha1.ReplicatedVolumeCondScheduledReasonAllReplicasScheduled).
			Eval()).To(BeTrue())
		Expect(obju.StatusCondition(rv, v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesType).
			IsTrue().ReasonEqual(v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonSatisfyEligibleNodes).
			Eval()).To(BeTrue())
		Expect(rv.Status.EligibleNodesViolations).To(BeEmpty())
	})

	It("touches nothing and reports no change for a deleting volume", func(ctx SpecContext) {
		// Deletion freezes all three at once, and the pass must stay silent: reporting a change
		// here would make the reconciler patch the status of every deleting volume forever.
		rv := withDeletionTimestamp(newPlacementTestRV(v1alpha1.TopologyIgnored,
			placementTestMember("rv-0", v1alpha1.DatameshMemberTypeDiskful, "n0")))
		applyScheduledCond(rv, scheduledReport{
			present: true,
			status:  metav1.ConditionFalse,
			reason:  v1alpha1.ReplicatedVolumeCondScheduledReasonReplicasNotScheduled,
			message: "0/1 replicas scheduled",
		})
		applySatisfyEligibleNodesCond(rv, satisfyEligibleNodesReport{
			present: true,
			status:  metav1.ConditionFalse,
			reason:  v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonInConflictWithEligibleNodes,
			message: "1/1 members are not on eligible nodes",
		})
		rv.Status.EligibleNodesViolations = []v1alpha1.ReplicatedVolumeEligibleNodesViolation{{
			NodeName:    "n0",
			ReplicaName: "rv-0",
			Reason:      v1alpha1.ReplicatedVolumeEligibleNodesViolationReasonOutOfEligibleNodes,
		}}
		before := rv.DeepCopy()

		outcome := r.reconcilePlacementConditions(ctx, rv, nil, placementTestRSP(nil))

		Expect(outcome.Error()).To(BeNil())
		Expect(outcome.DidChange()).To(BeFalse())
		Expect(rv.Status).To(Equal(before.Status))
	})
})
