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
// Dispatcher
//

var _ = Describe("ChangeQuorum dispatch", func() {
	It("skip: q and qmr already correct", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeDiskful, "node-3"),
			},
			nil, nil,
		)
		rv.Status.Configuration.GuaranteedMinimumDataRedundancy = 1
		rv.Status.Datamesh.Quorum = 2
		rv.Status.Datamesh.QuorumMinimumRedundancy = 2
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRUpToDate("rv-1-0", "node-1", 5),
			mkRVRUpToDate("rv-1-1", "node-2", 5),
			mkRVRUpToDate("rv-1-2", "node-3", 5),
		}
		settleEffectiveLayout(rv, rvrs)

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeFalse())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})

	It("skip: voting transition active", func() {
		// Pre-existing VotingMembership transition + wrong qmr.
		addT := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			ReplicaName: "rv-1-2", PlanID: "diskful/v1",
			ReplicaType: v1alpha1.ReplicaTypeDiskful,
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "✦ → D∅", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					DatameshRevision: 5, StartedAt: ptr.To(metav1.Now())},
				{Name: "D∅ → D", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending},
			},
		}
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeLiminalDiskful, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-2")},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{addT},
		)
		rv.Status.Datamesh.QuorumMinimumRedundancy = 5 // wrong, but should not trigger ChangeQuorum
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", 5), mkRVR("rv-1-1", "node-2", 5), mkRVR("rv-1-2", "node-3", 5),
		}

		ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{})

		// No ChangeQuorum — only the existing AddReplica.
		for _, t := range rv.Status.DatameshTransitions {
			Expect(t.Type).NotTo(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeQuorum))
		}
	})

	It("defer: qmr+1 with pending Join(D)", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-2")},
			nil,
		)
		rv.Status.Configuration.GuaranteedMinimumDataRedundancy = 1
		// qmr=1, needs 2 — but Join(D) present → defer to membership.
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRUpToDate("rv-1-0", "node-1", 5),
			mkRVRUpToDate("rv-1-1", "node-2", 5),
			mkRVR("rv-1-2", "node-3", 0),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		// AddReplica dispatched, not ChangeQuorum.
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica))
	})

	It("defer: qmr-1 with pending Leave(D)", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeDiskful, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkLeaveRequest("rv-1-2")},
			nil,
		)
		rv.Status.Datamesh.QuorumMinimumRedundancy = 2 // needs 1 — but Leave(D) present → defer
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRUpToDate("rv-1-0", "node-1", 5),
			mkRVRUpToDate("rv-1-1", "node-2", 5),
			mkRVRUpToDate("rv-1-2", "node-3", 5),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveReplica))
	})

	It("dispatch: qmr+1 no D request (diagonal)", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
			},
			nil, nil,
		)
		rv.Status.Configuration.GuaranteedMinimumDataRedundancy = 1
		// qmr=1, needs 2, no D request → ChangeQuorum.
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRUpToDate("rv-1-0", "node-1", 5),
			mkRVRUpToDate("rv-1-1", "node-2", 5),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeQuorum))
	})

	It("dispatch: qmr diff > 1", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeDiskful, "node-3"),
			},
			nil, nil,
		)
		rv.Status.Datamesh.QuorumMinimumRedundancy = 3 // needs 1, diff=2
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRUpToDate("rv-1-0", "node-1", 5),
			mkRVRUpToDate("rv-1-1", "node-2", 5),
			mkRVRUpToDate("rv-1-2", "node-3", 5),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeQuorum))
	})

	It("dispatch: q wrong", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeDiskful, "node-3"),
			},
			nil, nil,
		)
		rv.Status.Configuration.GuaranteedMinimumDataRedundancy = 1
		rv.Status.Datamesh.Quorum = 5 // wrong
		rv.Status.Datamesh.QuorumMinimumRedundancy = 2
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRUpToDate("rv-1-0", "node-1", 5),
			mkRVRUpToDate("rv-1-1", "node-2", 5),
			mkRVRUpToDate("rv-1-2", "node-3", 5),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeQuorum))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Plan selection
//

var _ = DescribeTable("ChangeQuorum plan selection",
	func(expectedQ, currentQ, expectedQMR, currentQMR byte, expectedPlan string) {
		plan := selectQuorumPlan(expectedQ, currentQ, expectedQMR, currentQMR)
		Expect(string(plan)).To(Equal(expectedPlan))
	},
	Entry("both lower", byte(1), byte(2), byte(1), byte(2), "lower/v1"),
	Entry("both raise", byte(3), byte(2), byte(3), byte(2), "raise/v1"),
	Entry("only qmr↑", byte(2), byte(2), byte(2), byte(1), "raise/v1"),
	Entry("only q↓", byte(1), byte(2), byte(1), byte(1), "lower/v1"),
	Entry("q↓ + qmr↑", byte(1), byte(2), byte(2), byte(1), "lower-q-raise-qmr/v1"),
	Entry("q↑ + qmr↓", byte(3), byte(2), byte(1), byte(2), "raise-q-lower-qmr/v1"),
)

// ──────────────────────────────────────────────────────────────────────────────
// Step flow + corruption recovery
//

var _ = Describe("ChangeQuorum step flow", func() {
	It("corruption: q=18, qmr=5 → fixed", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeDiskful, "node-3"),
			},
			nil, nil,
		)
		rv.Status.Datamesh.Quorum = 18
		rv.Status.Datamesh.QuorumMinimumRedundancy = 5
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRUpToDate("rv-1-0", "node-1", 5),
			mkRVRUpToDate("rv-1-1", "node-2", 5),
			mkRVRUpToDate("rv-1-2", "node-3", 5),
		}

		runUntilStableUnchecked(rv, mkRSP("node-1", "node-2", "node-3"), rvrs, FeatureFlags{})

		Expect(rv.Status.Datamesh.Quorum).To(Equal(byte(2)))
		Expect(rv.Status.Datamesh.QuorumMinimumRedundancy).To(Equal(byte(1)))
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})

	It("corruption: q=0, qmr=0 → fixed", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeDiskful, "node-3"),
			},
			nil, nil,
		)
		rv.Status.Datamesh.Quorum = 0
		rv.Status.Datamesh.QuorumMinimumRedundancy = 0
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRUpToDate("rv-1-0", "node-1", 5),
			mkRVRUpToDate("rv-1-1", "node-2", 5),
			mkRVRUpToDate("rv-1-2", "node-3", 5),
		}

		runUntilStableUnchecked(rv, mkRSP("node-1", "node-2", "node-3"), rvrs, FeatureFlags{})

		Expect(rv.Status.Datamesh.Quorum).To(Equal(byte(2)))
		Expect(rv.Status.Datamesh.QuorumMinimumRedundancy).To(Equal(byte(1)))
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})

	It("diagonal up: 2D, qmr 1→2", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
			},
			nil, nil,
		)
		rv.Status.Configuration.GuaranteedMinimumDataRedundancy = 1
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRUpToDate("rv-1-0", "node-1", 5),
			mkRVRUpToDate("rv-1-1", "node-2", 5),
		}

		runUntilStable(rv, nil, rvrs, FeatureFlags{})

		Expect(rv.Status.Datamesh.Quorum).To(Equal(byte(2)))
		Expect(rv.Status.Datamesh.QuorumMinimumRedundancy).To(Equal(byte(2)))
		Expect(rv.Status.BaselineGuaranteedMinimumDataRedundancy).To(Equal(byte(1)))
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})

	It("diagonal down: 2D, qmr 2→1", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
			},
			nil, nil,
		)
		rv.Status.Datamesh.QuorumMinimumRedundancy = 2
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRUpToDate("rv-1-0", "node-1", 5),
			mkRVRUpToDate("rv-1-1", "node-2", 5),
		}

		runUntilStable(rv, nil, rvrs, FeatureFlags{})

		Expect(rv.Status.Datamesh.Quorum).To(Equal(byte(2)))
		Expect(rv.Status.Datamesh.QuorumMinimumRedundancy).To(Equal(byte(1)))
		Expect(rv.Status.BaselineGuaranteedMinimumDataRedundancy).To(Equal(byte(0)))
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})

	It("lower/v1: qmr lowered, baseline updated", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeDiskful, "node-3"),
			},
			nil, nil,
		)
		rv.Status.Datamesh.QuorumMinimumRedundancy = 3 // needs 1
		rv.Status.BaselineGuaranteedMinimumDataRedundancy = 2
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRUpToDate("rv-1-0", "node-1", 5),
			mkRVRUpToDate("rv-1-1", "node-2", 5),
			mkRVRUpToDate("rv-1-2", "node-3", 5),
		}

		runUntilStable(rv, nil, rvrs, FeatureFlags{})

		Expect(rv.Status.Datamesh.QuorumMinimumRedundancy).To(Equal(byte(1)))
		Expect(rv.Status.BaselineGuaranteedMinimumDataRedundancy).To(Equal(byte(0)))
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})

	It("raise/v1: q and qmr raised, baseline updated", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
			},
			nil, nil,
		)
		rv.Status.Configuration.GuaranteedMinimumDataRedundancy = 1
		rv.Status.Datamesh.Quorum = 1 // wrong, needs 2
		// qmr=1, needs 2
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRUpToDate("rv-1-0", "node-1", 5),
			mkRVRUpToDate("rv-1-1", "node-2", 5),
		}

		runUntilStable(rv, nil, rvrs, FeatureFlags{})

		Expect(rv.Status.Datamesh.Quorum).To(Equal(byte(2)))
		Expect(rv.Status.Datamesh.QuorumMinimumRedundancy).To(Equal(byte(2)))
		Expect(rv.Status.BaselineGuaranteedMinimumDataRedundancy).To(Equal(byte(1)))
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})
})
