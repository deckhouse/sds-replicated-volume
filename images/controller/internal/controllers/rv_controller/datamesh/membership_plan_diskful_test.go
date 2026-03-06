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

var _ = Describe("AddReplica(D) dispatch", func() {
	// Even voters (2D) + new D request → no q↑.
	It("even voters, no sD, no qmr↑ → diskful/v1", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-2")},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 5), mkRVR("rv-1-2", "node-3", 0),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("diskful/v1"))
	})

	It("even voters, no sD, qmr↑ → diskful-qmr-up/v1", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-2")},
			nil,
		)
		rv.Status.Configuration.GuaranteedMinimumDataRedundancy = 1
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 5), mkRVR("rv-1-2", "node-3", 0),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("diskful-qmr-up/v1"))
	})

	// Odd voters (1D) + new D request → q↑.
	It("odd voters, no sD, no qmr↑ → diskful-q-up/v1", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-1")},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 0),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("diskful-q-up/v1"))
	})

	It("odd voters, no sD, qmr↑ → diskful-q-up-qmr-up/v1", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-1")},
			nil,
		)
		rv.Status.Configuration.GuaranteedMinimumDataRedundancy = 1
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 0),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("diskful-q-up-qmr-up/v1"))
	})

	// sD variants.
	It("even voters, sD, no qmr↑ → diskful-via-sd/v1", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-2")},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 5), mkRVR("rv-1-2", "node-3", 0),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{ShadowDiskful: true})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("diskful-via-sd/v1"))
	})

	It("even voters, sD, qmr↑ → diskful-via-sd-qmr-up/v1", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-2")},
			nil,
		)
		rv.Status.Configuration.GuaranteedMinimumDataRedundancy = 1
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 5), mkRVR("rv-1-2", "node-3", 0),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{ShadowDiskful: true})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("diskful-via-sd-qmr-up/v1"))
	})

	It("odd voters, sD, no qmr↑ → diskful-via-sd-q-up/v1", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-1")},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 0),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{ShadowDiskful: true})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("diskful-via-sd-q-up/v1"))
	})

	It("odd voters, sD, qmr↑ → diskful-via-sd-q-up-qmr-up/v1", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-1")},
			nil,
		)
		rv.Status.Configuration.GuaranteedMinimumDataRedundancy = 1
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 0),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{ShadowDiskful: true})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("diskful-via-sd-q-up-qmr-up/v1"))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// diskful/v1 (even→odd, no sD, no qmr↑) — simplest: 2 steps
//

var _ = Describe("AddReplica(D) diskful/v1", func() {
	It("creates transition, member = D∅ with BV", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-2")},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 5), mkRVR("rv-1-2", "node-3", 0),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())

		// Member created as LiminalDiskful with BV fields.
		Expect(rv.Status.Datamesh.Members).To(HaveLen(3))
		m := rv.Status.Datamesh.FindMemberByName("rv-1-2")
		Expect(m).NotTo(BeNil())
		Expect(m.Type).To(Equal(v1alpha1.DatameshMemberTypeLiminalDiskful))
		Expect(m.LVMVolumeGroupName).To(Equal("test-lvg"))
		Expect(m.LVMVolumeGroupThinPoolName).To(Equal("test-thin"))

		// Transition: 2 steps.
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		t := &rv.Status.DatameshTransitions[0]
		Expect(t.Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica))
		Expect(t.PlanID).To(Equal("diskful/v1"))
		Expect(t.Steps).To(HaveLen(2))
		Expect(t.Steps[0].Name).To(Equal("✦ → D∅"))
		Expect(t.Steps[0].Status).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive))
		Expect(t.Steps[1].Name).To(Equal("D∅ → D"))
		Expect(t.Steps[1].Status).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending))
	})

	It("step 1 confirmed → D∅ → D", func() {
		t := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			ReplicaName: "rv-1-2", PlanID: "diskful/v1",
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "✦ → D∅", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
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
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-2")},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		// All 3 members confirmed rev 6.
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 6), mkRVR("rv-1-1", "node-2", 6), mkRVR("rv-1-2", "node-3", 6),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())

		// Type changed to Diskful (step 2 applied).
		m := rv.Status.Datamesh.FindMemberByName("rv-1-2")
		Expect(m).NotTo(BeNil())
		Expect(m.Type).To(Equal(v1alpha1.DatameshMemberTypeDiskful))
		Expect(m.LVMVolumeGroupName).To(Equal("test-lvg"))

		// Step 1 completed, step 2 active.
		Expect(rv.Status.DatameshTransitions[0].Steps[0].Status).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted))
		Expect(rv.Status.DatameshTransitions[0].Steps[1].Status).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive))
	})

	It("both steps confirmed → completed", func() {
		t := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			ReplicaName: "rv-1-2", PlanID: "diskful/v1",
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "✦ → D∅", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted, DatameshRevision: 6},
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
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-2")},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		// Subject confirmed rev 7 (subjectOnly).
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 6), mkRVR("rv-1-1", "node-2", 6), mkRVR("rv-1-2", "node-3", 7),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(Equal("Joined datamesh successfully"))
	})

	It("step 1 partial: not all confirmed", func() {
		t := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			ReplicaName: "rv-1-2", PlanID: "diskful/v1",
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "✦ → D∅", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
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
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-2")},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		// 2 of 3 confirmed (rv-1-2 not yet).
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 6), mkRVR("rv-1-1", "node-2", 6), mkRVR("rv-1-2", "node-3", 5),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Steps[0].Status).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive))
		Expect(rv.Status.DatameshTransitions[0].Steps[0].Message).To(ContainSubstring("2/3"))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// diskful-q-up/v1 (odd→even, A vestibule, no qmr↑) — 3 steps
//

var _ = Describe("AddReplica(D) diskful-q-up/v1", func() {
	It("creates transition, member = A (no BV)", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-1")},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 0),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())

		// Step 1: Access vestibule, no BV.
		m := rv.Status.Datamesh.FindMemberByName("rv-1-1")
		Expect(m).NotTo(BeNil())
		Expect(m.Type).To(Equal(v1alpha1.DatameshMemberTypeAccess))
		Expect(m.LVMVolumeGroupName).To(BeEmpty())
		Expect(m.LVMVolumeGroupThinPoolName).To(BeEmpty())

		// 3 steps.
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		t := &rv.Status.DatameshTransitions[0]
		Expect(t.PlanID).To(Equal("diskful-q-up/v1"))
		Expect(t.Steps).To(HaveLen(3))
		Expect(t.Steps[0].Name).To(Equal("✦ → A"))
		Expect(t.Steps[1].Name).To(Equal("A → D∅ + q↑"))
		Expect(t.Steps[2].Name).To(Equal("D∅ → D"))
	})

	It("step 1 confirmed → A → D∅ + q↑: type, BV, quorum", func() {
		t := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			ReplicaName: "rv-1-1", PlanID: "diskful-q-up/v1",
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "✦ → A", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					DatameshRevision: 6, StartedAt: ptr.To(metav1.Now())},
				{Name: "A → D∅ + q↑", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending},
				{Name: "D∅ → D", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending},
			},
		}
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-1")},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		rv.Status.Datamesh.Quorum = 1
		// FM (D) + subject (A) confirmed → step 1 completes.
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 6), mkRVR("rv-1-1", "node-2", 6),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())

		// Step 2 applied: type = LiminalDiskful, BV set.
		m := rv.Status.Datamesh.FindMemberByName("rv-1-1")
		Expect(m).NotTo(BeNil())
		Expect(m.Type).To(Equal(v1alpha1.DatameshMemberTypeLiminalDiskful))
		Expect(m.LVMVolumeGroupName).To(Equal("test-lvg"))
		Expect(m.LVMVolumeGroupThinPoolName).To(Equal("test-thin"))

		// q raised: 2 voters (D + D∅) → q = max(2/2+1, 1) = 2.
		Expect(rv.Status.Datamesh.Quorum).To(Equal(byte(2)))

		// Step flow.
		Expect(rv.Status.DatameshTransitions[0].Steps[0].Status).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted))
		Expect(rv.Status.DatameshTransitions[0].Steps[1].Status).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive))
	})

	It("step 2 confirmed → D∅ → D", func() {
		t := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			ReplicaName: "rv-1-1", PlanID: "diskful-q-up/v1",
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "✦ → A", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted, DatameshRevision: 6},
				{Name: "A → D∅ + q↑", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					DatameshRevision: 7, StartedAt: ptr.To(metav1.Now())},
				{Name: "D∅ → D", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending},
			},
		}
		rv := mkRV(7,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeLiminalDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-1")},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		// All members confirmed (confirmAllMembers for step 2).
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 7), mkRVR("rv-1-1", "node-2", 7),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		m := rv.Status.Datamesh.FindMemberByName("rv-1-1")
		Expect(m.Type).To(Equal(v1alpha1.DatameshMemberTypeDiskful))

		Expect(rv.Status.DatameshTransitions[0].Steps[1].Status).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted))
		Expect(rv.Status.DatameshTransitions[0].Steps[2].Status).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive))
	})

	It("all steps confirmed → completed", func() {
		t := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			ReplicaName: "rv-1-1", PlanID: "diskful-q-up/v1",
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "✦ → A", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted, DatameshRevision: 6},
				{Name: "A → D∅ + q↑", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted, DatameshRevision: 7},
				{Name: "D∅ → D", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					DatameshRevision: 8, StartedAt: ptr.To(metav1.Now())},
			},
		}
		rv := mkRV(8,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-1")},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 7), mkRVR("rv-1-1", "node-2", 8),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(Equal("Joined datamesh successfully"))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// diskful-qmr-up/v1 (even→odd, no sD, qmr↑) — 3 steps
//

var _ = Describe("AddReplica(D) diskful-qmr-up/v1", func() {
	It("creates transition (3 steps)", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-2")},
			nil,
		)
		rv.Status.Configuration.GuaranteedMinimumDataRedundancy = 1
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 5), mkRVR("rv-1-2", "node-3", 0),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		t := &rv.Status.DatameshTransitions[0]
		Expect(t.PlanID).To(Equal("diskful-qmr-up/v1"))
		Expect(t.Steps).To(HaveLen(3))
		Expect(t.Steps[0].Name).To(Equal("✦ → D∅"))
		Expect(t.Steps[1].Name).To(Equal("D∅ → D"))
		Expect(t.Steps[2].Name).To(Equal("qmr↑"))
	})

	It("D∅→D confirmed → qmr↑ applied", func() {
		t := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			ReplicaName: "rv-1-2", PlanID: "diskful-qmr-up/v1",
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "✦ → D∅", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted, DatameshRevision: 6},
				{Name: "D∅ → D", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					DatameshRevision: 7, StartedAt: ptr.To(metav1.Now())},
				{Name: "qmr↑", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending},
			},
		}
		rv := mkRV(7,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeDiskful, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-2")},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		rv.Status.Configuration.GuaranteedMinimumDataRedundancy = 1
		// Subject confirmed (subjectOnly for step 2).
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 6), mkRVR("rv-1-1", "node-2", 6), mkRVR("rv-1-2", "node-3", 7),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())

		// qmr raised: config.GMDR + 1 = 2.
		Expect(rv.Status.Datamesh.QuorumMinimumRedundancy).To(Equal(byte(2)))

		// Step 2 completed, step 3 (qmr↑) active.
		Expect(rv.Status.DatameshTransitions[0].Steps[1].Status).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted))
		Expect(rv.Status.DatameshTransitions[0].Steps[2].Status).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive))
	})

	It("qmr↑ confirmed → completed, baseline updated", func() {
		t := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			ReplicaName: "rv-1-2", PlanID: "diskful-qmr-up/v1",
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "✦ → D∅", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted, DatameshRevision: 6},
				{Name: "D∅ → D", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted, DatameshRevision: 7},
				{Name: "qmr↑", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					DatameshRevision: 8, StartedAt: ptr.To(metav1.Now())},
			},
		}
		rv := mkRV(8,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeDiskful, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-2")},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		rv.Status.Configuration.GuaranteedMinimumDataRedundancy = 1
		rv.Status.Datamesh.Quorum = 2
		rv.Status.Datamesh.QuorumMinimumRedundancy = 2 // set by previous step
		// All 3 members confirmed (confirmAllMembers for qmr↑).
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 8), mkRVR("rv-1-1", "node-2", 8), mkRVR("rv-1-2", "node-3", 8),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(Equal("Joined datamesh successfully"))

		// Baseline layout updated by OnComplete.
		Expect(rv.Status.BaselineLayout.GuaranteedMinimumDataRedundancy).To(Equal(byte(1)))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// diskful-q-up-qmr-up/v1 (odd→even, A vestibule, q↑ + qmr↑) — 4 steps
//

var _ = Describe("AddReplica(D) diskful-q-up-qmr-up/v1", func() {
	It("creates transition (4 steps)", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-1")},
			nil,
		)
		rv.Status.Configuration.GuaranteedMinimumDataRedundancy = 1
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 0),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		t := &rv.Status.DatameshTransitions[0]
		Expect(t.PlanID).To(Equal("diskful-q-up-qmr-up/v1"))
		Expect(t.Steps).To(HaveLen(4))
		Expect(t.Steps[0].Name).To(Equal("✦ → A"))
		Expect(t.Steps[1].Name).To(Equal("A → D∅ + q↑"))
		Expect(t.Steps[2].Name).To(Equal("D∅ → D"))
		Expect(t.Steps[3].Name).To(Equal("qmr↑"))
	})

	It("full lifecycle → q + qmr raised, baseline updated", func() {
		// Start at the last step: qmr↑ active, all previous completed.
		t := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			ReplicaName: "rv-1-1", PlanID: "diskful-q-up-qmr-up/v1",
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "✦ → A", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted, DatameshRevision: 6},
				{Name: "A → D∅ + q↑", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted, DatameshRevision: 7},
				{Name: "D∅ → D", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted, DatameshRevision: 8},
				{Name: "qmr↑", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					DatameshRevision: 9, StartedAt: ptr.To(metav1.Now())},
			},
		}
		rv := mkRV(9,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-1")},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		rv.Status.Configuration.GuaranteedMinimumDataRedundancy = 1
		rv.Status.Datamesh.Quorum = 2
		rv.Status.Datamesh.QuorumMinimumRedundancy = 2
		// All confirmed.
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 9), mkRVR("rv-1-1", "node-2", 9),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(Equal("Joined datamesh successfully"))
		Expect(rv.Status.Datamesh.Quorum).To(Equal(byte(2)))
		Expect(rv.Status.Datamesh.QuorumMinimumRedundancy).To(Equal(byte(2)))
		Expect(rv.Status.BaselineLayout.GuaranteedMinimumDataRedundancy).To(Equal(byte(1)))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// diskful-via-sd/v1 (even→odd, sD pre-sync, no qmr↑) — 3 steps
//

var _ = Describe("AddReplica(D) diskful-via-sd/v1", func() {
	It("creates transition, member = sD∅ with BV", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-2")},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 5), mkRVR("rv-1-2", "node-3", 0),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{ShadowDiskful: true})

		Expect(changed).To(BeTrue())

		// Step 1: LiminalShadowDiskful with BV.
		m := rv.Status.Datamesh.FindMemberByName("rv-1-2")
		Expect(m).NotTo(BeNil())
		Expect(m.Type).To(Equal(v1alpha1.DatameshMemberTypeLiminalShadowDiskful))
		Expect(m.LVMVolumeGroupName).To(Equal("test-lvg"))

		t := &rv.Status.DatameshTransitions[0]
		Expect(t.PlanID).To(Equal("diskful-via-sd/v1"))
		Expect(t.Steps).To(HaveLen(3))
		Expect(t.Steps[0].Name).To(Equal("✦ → sD∅"))
		Expect(t.Steps[1].Name).To(Equal("sD∅ → sD"))
		Expect(t.Steps[2].Name).To(Equal("sD → D"))
	})

	It("sD∅→sD confirmed → sD→D applied", func() {
		t := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			ReplicaName: "rv-1-2", PlanID: "diskful-via-sd/v1",
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "✦ → sD∅", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted, DatameshRevision: 6},
				{Name: "sD∅ → sD", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					DatameshRevision: 7, StartedAt: ptr.To(metav1.Now())},
				{Name: "sD → D", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending},
			},
		}
		rv := mkRV(7,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeShadowDiskful, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-2")},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		// Subject confirmed (subjectOnly for step 2).
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 6), mkRVR("rv-1-1", "node-2", 6), mkRVR("rv-1-2", "node-3", 7),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{ShadowDiskful: true})

		Expect(changed).To(BeTrue())

		// sD → D: type changed to Diskful (step 3 applied).
		m := rv.Status.Datamesh.FindMemberByName("rv-1-2")
		Expect(m.Type).To(Equal(v1alpha1.DatameshMemberTypeDiskful))

		Expect(rv.Status.DatameshTransitions[0].Steps[1].Status).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted))
		Expect(rv.Status.DatameshTransitions[0].Steps[2].Status).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive))
	})

	It("all confirmed → completed", func() {
		t := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			ReplicaName: "rv-1-2", PlanID: "diskful-via-sd/v1",
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "✦ → sD∅", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted, DatameshRevision: 6},
				{Name: "sD∅ → sD", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted, DatameshRevision: 7},
				{Name: "sD → D", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					DatameshRevision: 8, StartedAt: ptr.To(metav1.Now())},
			},
		}
		rv := mkRV(8,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeDiskful, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-2")},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		// All 3 confirmed (confirmAllMembers for sD→D step).
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 8), mkRVR("rv-1-1", "node-2", 8), mkRVR("rv-1-2", "node-3", 8),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{ShadowDiskful: true})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(Equal("Joined datamesh successfully"))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// diskful-via-sd-q-up/v1 (odd→even, sD pre-sync + q↑) — 5 steps
//

var _ = Describe("AddReplica(D) diskful-via-sd-q-up/v1", func() {
	It("creates transition (5 steps)", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-1")},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 0),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{ShadowDiskful: true})

		Expect(changed).To(BeTrue())
		t := &rv.Status.DatameshTransitions[0]
		Expect(t.PlanID).To(Equal("diskful-via-sd-q-up/v1"))
		Expect(t.Steps).To(HaveLen(5))
		Expect(t.Steps[0].Name).To(Equal("✦ → sD∅"))
		Expect(t.Steps[1].Name).To(Equal("sD∅ → sD"))
		Expect(t.Steps[2].Name).To(Equal("sD → sD∅"))
		Expect(t.Steps[3].Name).To(Equal("sD∅ → D∅ + q↑"))
		Expect(t.Steps[4].Name).To(Equal("D∅ → D"))
	})

	It("sD→sD∅ confirmed → sD∅→D∅+q↑: BV preserved, q raised", func() {
		// Step 3 (sD → sD∅) active, subject confirmed.
		t := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			ReplicaName: "rv-1-1", PlanID: "diskful-via-sd-q-up/v1",
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "✦ → sD∅", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted, DatameshRevision: 6},
				{Name: "sD∅ → sD", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted, DatameshRevision: 7},
				{Name: "sD → sD∅", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					DatameshRevision: 8, StartedAt: ptr.To(metav1.Now())},
				{Name: "sD∅ → D∅ + q↑", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending},
				{Name: "D∅ → D", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending},
			},
		}
		rv := mkRV(8,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeLiminalShadowDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-1")},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		rv.Status.Datamesh.Quorum = 1
		// Subject confirmed (subjectOnly for step 3).
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 7), mkRVR("rv-1-1", "node-2", 8),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{ShadowDiskful: true})

		Expect(changed).To(BeTrue())

		// Step 4 applied: LiminalDiskful, BV preserved, q raised.
		m := rv.Status.Datamesh.FindMemberByName("rv-1-1")
		Expect(m.Type).To(Equal(v1alpha1.DatameshMemberTypeLiminalDiskful))
		Expect(m.LVMVolumeGroupName).To(Equal("test-lvg"))
		Expect(m.LVMVolumeGroupThinPoolName).To(Equal("test-thin"))

		// q raised: 2 voters (D + D∅) → q = 2.
		Expect(rv.Status.Datamesh.Quorum).To(Equal(byte(2)))

		Expect(rv.Status.DatameshTransitions[0].Steps[2].Status).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted))
		Expect(rv.Status.DatameshTransitions[0].Steps[3].Status).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive))
	})

	It("full lifecycle → completed", func() {
		// Last step (D∅→D) active, subject confirmed.
		t := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			ReplicaName: "rv-1-1", PlanID: "diskful-via-sd-q-up/v1",
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "✦ → sD∅", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted, DatameshRevision: 6},
				{Name: "sD∅ → sD", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted, DatameshRevision: 7},
				{Name: "sD → sD∅", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted, DatameshRevision: 8},
				{Name: "sD∅ → D∅ + q↑", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted, DatameshRevision: 9},
				{Name: "D∅ → D", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					DatameshRevision: 10, StartedAt: ptr.To(metav1.Now())},
			},
		}
		rv := mkRV(10,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-1")},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		rv.Status.Datamesh.Quorum = 2
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 9), mkRVR("rv-1-1", "node-2", 10),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{ShadowDiskful: true})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(Equal("Joined datamesh successfully"))
		Expect(rv.Status.Datamesh.Quorum).To(Equal(byte(2)))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Remaining sD+qmr plans
//

var _ = Describe("AddReplica(D) diskful-via-sd-qmr-up/v1", func() {
	It("creates transition (4 steps)", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-2")},
			nil,
		)
		rv.Status.Configuration.GuaranteedMinimumDataRedundancy = 1
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 5), mkRVR("rv-1-2", "node-3", 0),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{ShadowDiskful: true})

		Expect(changed).To(BeTrue())
		t := &rv.Status.DatameshTransitions[0]
		Expect(t.PlanID).To(Equal("diskful-via-sd-qmr-up/v1"))
		Expect(t.Steps).To(HaveLen(4))
		Expect(t.Steps[0].Name).To(Equal("✦ → sD∅"))
		Expect(t.Steps[1].Name).To(Equal("sD∅ → sD"))
		Expect(t.Steps[2].Name).To(Equal("sD → D"))
		Expect(t.Steps[3].Name).To(Equal("qmr↑"))
	})
})

var _ = Describe("AddReplica(D) diskful-via-sd-q-up-qmr-up/v1", func() {
	It("creates transition (6 steps)", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-1")},
			nil,
		)
		rv.Status.Configuration.GuaranteedMinimumDataRedundancy = 1
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 0),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{ShadowDiskful: true})

		Expect(changed).To(BeTrue())
		t := &rv.Status.DatameshTransitions[0]
		Expect(t.PlanID).To(Equal("diskful-via-sd-q-up-qmr-up/v1"))
		Expect(t.Steps).To(HaveLen(6))
		Expect(t.Steps[0].Name).To(Equal("✦ → sD∅"))
		Expect(t.Steps[1].Name).To(Equal("sD∅ → sD"))
		Expect(t.Steps[2].Name).To(Equal("sD → sD∅"))
		Expect(t.Steps[3].Name).To(Equal("sD∅ → D∅ + q↑"))
		Expect(t.Steps[4].Name).To(Equal("D∅ → D"))
		Expect(t.Steps[5].Name).To(Equal("qmr↑"))
	})

	It("full lifecycle → q + qmr raised, baseline updated", func() {
		// Last step (qmr↑) active, all confirmed.
		t := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			ReplicaName: "rv-1-1", PlanID: "diskful-via-sd-q-up-qmr-up/v1",
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "✦ → sD∅", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted, DatameshRevision: 6},
				{Name: "sD∅ → sD", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted, DatameshRevision: 7},
				{Name: "sD → sD∅", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted, DatameshRevision: 8},
				{Name: "sD∅ → D∅ + q↑", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted, DatameshRevision: 9},
				{Name: "D∅ → D", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted, DatameshRevision: 10},
				{Name: "qmr↑", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					DatameshRevision: 11, StartedAt: ptr.To(metav1.Now())},
			},
		}
		rv := mkRV(11,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-1")},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		rv.Status.Configuration.GuaranteedMinimumDataRedundancy = 1
		rv.Status.Datamesh.Quorum = 2
		rv.Status.Datamesh.QuorumMinimumRedundancy = 2
		// All confirmed.
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 11), mkRVR("rv-1-1", "node-2", 11),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{ShadowDiskful: true})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(Equal("Joined datamesh successfully"))
		Expect(rv.Status.Datamesh.Quorum).To(Equal(byte(2)))
		Expect(rv.Status.Datamesh.QuorumMinimumRedundancy).To(Equal(byte(2)))
		Expect(rv.Status.BaselineLayout.GuaranteedMinimumDataRedundancy).To(Equal(byte(1)))
	})
})
