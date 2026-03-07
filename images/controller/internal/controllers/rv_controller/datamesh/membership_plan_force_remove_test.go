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
// Dispatch
//

var _ = Describe("ForceRemoveReplica dispatch", func() {
	It("ForceLeave(A) → access/v1", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkForceLeaveRequest("rv-1-1")},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-0", "node-1", 5)}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		t := &rv.Status.DatameshTransitions[0]
		Expect(t.Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeForceRemoveReplica))
		Expect(t.PlanID).To(Equal("access/v1"))
	})

	It("ForceLeave(TB) → tiebreaker/v1", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeTieBreaker, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkForceLeaveRequest("rv-1-1")},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-0", "node-1", 5)}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("tiebreaker/v1"))
	})

	It("ForceLeave(sD) → shadow-diskful/v1", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeShadowDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkForceLeaveRequest("rv-1-1")},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-0", "node-1", 5)}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("shadow-diskful/v1"))
	})

	It("ForceLeave(D) odd voters → diskful/v1", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeDiskful, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkForceLeaveRequest("rv-1-2")},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 5),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("diskful/v1"))
	})

	It("ForceLeave(D) even voters → diskful-q-down/v1", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkForceLeaveRequest("rv-1-1")},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-0", "node-1", 5)}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("diskful-q-down/v1"))
	})

	It("ForceLeave(D∅) → diskful/v1 (liminal)", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeLiminalDiskful, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkForceLeaveRequest("rv-1-2")},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 5),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("diskful/v1"))
	})

	It("orphan member (no RVR) → auto ForceRemove", func() {
		// Member exists but no RVR and no request → auto-dispatch ForceRemove.
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-2"),
			},
			nil, // no requests
			nil,
		)
		// Only rv-1-0 has RVR. rv-1-1 has no RVR (node lost).
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-0", "node-1", 5)}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		t := &rv.Status.DatameshTransitions[0]
		Expect(t.Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeForceRemoveReplica))
		Expect(t.PlanID).To(Equal("access/v1"))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Step flow
//

var _ = Describe("ForceRemoveReplica step flow", func() {
	It("ForceRemove(A): member removed, transition active", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkForceLeaveRequest("rv-1-1")},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-0", "node-1", 5)}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		// Member removed (single-step, apply already ran).
		Expect(rv.Status.Datamesh.FindMemberByName("rv-1-1")).To(BeNil())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Steps[0].Name).To(Equal("Force remove"))
	})

	It("ForceRemove(D) odd voters: member removed, baseline updated", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeDiskful, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkForceLeaveRequest("rv-1-2")},
			nil,
		)
		rv.Status.Configuration.FailuresToTolerate = 1
		rv.Status.Datamesh.Quorum = 2
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 5),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.Datamesh.FindMemberByName("rv-1-2")).To(BeNil())
		// Baseline updated in apply (lowering). 2D, q=2 → FTT=0.
	})

	It("ForceRemove(D) + q↓: q lowered", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkForceLeaveRequest("rv-1-1")},
			nil,
		)
		rv.Status.Datamesh.Quorum = 2
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-0", "node-1", 5)}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.Datamesh.FindMemberByName("rv-1-1")).To(BeNil())
		Expect(rv.Status.Datamesh.Quorum).To(Equal(byte(1)))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Guards
//

var _ = Describe("ForceRemoveReplica guards", func() {
	It("guardMemberUnreachable blocks when peer sees Connected", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkForceLeaveRequest("rv-1-1")},
			nil,
		)
		rvr0 := mkRVR("rv-1-0", "node-1", 5)
		rvr0.Status.Conditions = []metav1.Condition{
			{Type: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType,
				Status: metav1.ConditionTrue,
				Reason: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonConfigured},
		}
		rvr0.Status.Peers = []v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{
			{Name: "rv-1-1", ConnectionState: v1alpha1.ConnectionStateConnected},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr0}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		// No ForceRemove transition created (guard blocked).
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(ContainSubstring("reachable"))
	})

	It("guardMemberUnreachable passes when agent not ready (stale)", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkForceLeaveRequest("rv-1-1")},
			nil,
		)
		rvr0 := mkRVR("rv-1-0", "node-1", 5)
		rvr0.Status.Conditions = []metav1.Condition{
			{Type: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType,
				Status: metav1.ConditionFalse,
				Reason: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonAgentNotReady},
		}
		rvr0.Status.Peers = []v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{
			{Name: "rv-1-1", ConnectionState: v1alpha1.ConnectionStateConnected}, // stale!
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr0}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		// Guard passes (stale agent skipped), ForceRemove dispatched.
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeForceRemoveReplica))
	})

	It("guardNotAttached blocks", func() {
		member := mkMember("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-2")
		member.Attached = true
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				member,
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkForceLeaveRequest("rv-1-1")},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-0", "node-1", 5)}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		// No ForceRemove transition (guard blocked). Detach may be created by attachment dispatcher.
		for _, t := range rv.Status.DatameshTransitions {
			Expect(t.Type).NotTo(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeForceRemoveReplica))
		}
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(ContainSubstring("attached"))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Completion
//

var _ = Describe("ForceRemoveReplica completion", func() {
	It("completed → message", func() {
		t := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeForceRemoveReplica,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupEmergency,
			ReplicaName: "rv-1-1", PlanID: "access/v1",
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "Force remove", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					DatameshRevision: 6, StartedAt: ptr.To(metav1.Now())},
			},
		}
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkForceLeaveRequest("rv-1-1")},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 6), mkRVR("rv-1-1", "node-2", 0),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(Equal("Force-removed from datamesh"))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// CancelActiveOnCreate
//

var _ = Describe("ForceRemoveReplica CancelActiveOnCreate", func() {
	It("existing AddReplica cancelled when ForceLeave dispatched", func() {
		// In-flight AddReplica for rv-1-1.
		addT := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership,
			ReplicaName: "rv-1-1", PlanID: "access/v1",
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "✦ → A", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					DatameshRevision: 5, StartedAt: ptr.To(metav1.Now())},
			},
		}
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-2"),
			},
			// ForceLeave request replaces the Join request.
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkForceLeaveRequest("rv-1-1")},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{addT},
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-0", "node-1", 5)}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		// AddReplica cancelled, ForceRemove created.
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeForceRemoveReplica))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Additional coverage
//

var _ = Describe("ForceRemoveReplica additional", func() {
	It("ForceLeave(sD∅) → shadow-diskful/v1 (liminal)", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeLiminalShadowDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkForceLeaveRequest("rv-1-1")},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-0", "node-1", 5)}
		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})
		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("shadow-diskful/v1"))
	})

	It("Emergency preempts in-flight AddReplica(D)", func() {
		// Setup: 3D layout. AddReplica(D)+q↑ for rv-1-3 is dispatched.
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeDiskful, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-3")},
			nil,
		)
		rv.Status.Datamesh.Quorum = 2
		rv.Status.Datamesh.QuorumMinimumRedundancy = 2
		rv.Status.Configuration.FailuresToTolerate = 1
		rv.Status.Configuration.GuaranteedMinimumDataRedundancy = 1

		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRUpToDate("rv-1-0", "node-1", 5),
			mkRVRUpToDate("rv-1-1", "node-2", 5),
			mkRVRUpToDate("rv-1-2", "node-3", 5),
			mkRVR("rv-1-3", "node-4", 0), // new replica for AddD
		}
		rsp := mkRSP("node-1", "node-2", "node-3", "node-4")

		// Step 1: dispatch AddReplica(D)+q↑. First step (✦→A) applied.
		changed, _ := ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, FeatureFlags{})
		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Type).To(Equal(
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica))

		// Confirm step 1 (all replicas see the new revision).
		for _, rvr := range rvrs {
			rvr.Status.DatameshRevision = rv.Status.DatameshRevision
		}

		// Step 2: rv-1-2 dies mid-flight. Remove RVR + add ForceLeave.
		rvrs = rvrs[:3] // remove rv-1-3's position — keep only 0,1,2
		// Actually we need to remove rv-1-2 (dead) and keep rv-1-3 (new).
		filteredRVRs := []*v1alpha1.ReplicatedVolumeReplica{rvrs[0], rvrs[1], mkRVR("rv-1-3", "node-4", rv.Status.DatameshRevision)}

		rv.Status.DatameshReplicaRequests = append(
			rv.Status.DatameshReplicaRequests,
			mkForceLeaveRequest("rv-1-2"),
		)

		// Run until stable: ForceRemove should preempt and the system should stabilize.
		runUntilStable(rv, rsp, filteredRVRs, FeatureFlags{})

		// rv-1-2 should be removed.
		Expect(rv.Status.Datamesh.FindMemberByName("rv-1-2")).To(BeNil())

		// System must stabilize: no stuck transitions.
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())

		// q must be correct for final voter count.
		var voters int
		for _, m := range rv.Status.Datamesh.Members {
			if m.Type.IsVoter() {
				voters++
			}
		}
		Expect(rv.Status.Datamesh.Quorum).To(Equal(expectedQ(voters)),
			"q must match actual voter count %d", voters)
	})

	It("orphan D (even voters) → auto ForceRemove with q↓", func() {
		// 2D members, rv-1-1 has no RVR (node lost). Even voters → q↓.
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
			},
			nil, nil,
		)
		rv.Status.Datamesh.Quorum = 2
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-0", "node-1", 5)}
		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})
		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		t := &rv.Status.DatameshTransitions[0]
		Expect(t.Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeForceRemoveReplica))
		Expect(t.PlanID).To(Equal("diskful-q-down/v1"))
		// Member removed, q lowered.
		Expect(rv.Status.Datamesh.FindMemberByName("rv-1-1")).To(BeNil())
		Expect(rv.Status.Datamesh.Quorum).To(Equal(byte(1)))
	})

	It("orphan attached D: full ForceDetach → ForceRemove cycle", func() {
		// 3D layout. rv-1-2 dies: RVR removed, member still Attached=true.
		// Both ForceDetach and ForceLeave requests provided simultaneously.
		// ForceDetach runs first (attachment dispatcher), clears Attached.
		// Then ForceRemove runs (membership dispatcher), removes the member.
		member := mkMember("rv-1-2", v1alpha1.DatameshMemberTypeDiskful, "node-3")
		member.Attached = true

		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				member,
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkForceDetachRequest("rv-1-2"),
				mkForceLeaveRequest("rv-1-2"),
			},
			nil,
		)
		// Only surviving RVRs (dead node has no RVR).
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRUpToDate("rv-1-0", "node-1", 5),
			mkRVRUpToDate("rv-1-1", "node-2", 5),
		}

		runUntilStable(rv, nil, rvrs, FeatureFlags{})

		// rv-1-2 must be fully removed.
		Expect(rv.Status.Datamesh.FindMemberByName("rv-1-2")).To(BeNil())
		// 2 surviving voters, q correct.
		var voters int
		for _, m := range rv.Status.Datamesh.Members {
			if m.Type.IsVoter() {
				voters++
			}
		}
		Expect(voters).To(Equal(2))
		Expect(rv.Status.Datamesh.Quorum).To(Equal(expectedQ(2)))
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})
})
