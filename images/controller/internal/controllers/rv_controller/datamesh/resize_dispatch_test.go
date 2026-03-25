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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/drbd_size"
)

// mkRVRResizeReady creates an RVR that passes all resize guards:
// Ready=True, BackingVolume with size and UpToDate state, peers Established,
// DRBDConfigured=True.
func mkRVRResizeReady(name, nodeName string, bvSize resource.Quantity, peerNames ...string) *v1alpha1.ReplicatedVolumeReplica {
	rvr := mkRVR(name, nodeName, 5)
	rvr.Generation = 1
	rvr.Status.Quorum = ptr.To(true)
	rvr.Status.BackingVolume = &v1alpha1.ReplicatedVolumeReplicaStatusBackingVolume{
		State: v1alpha1.DiskStateUpToDate,
		Size:  &bvSize,
	}
	rvr.Status.Conditions = []metav1.Condition{
		{
			Type:               v1alpha1.ReplicatedVolumeReplicaCondReadyType,
			Status:             metav1.ConditionTrue,
			Reason:             "Ready",
			ObservedGeneration: 1,
		},
		{
			Type:               v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType,
			Status:             metav1.ConditionTrue,
			Reason:             "Configured",
			ObservedGeneration: 1,
		},
	}
	for _, peerName := range peerNames {
		rvr.Status.Peers = append(rvr.Status.Peers, v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{
			Name:             peerName,
			ConnectionState:  v1alpha1.ConnectionStateConnected,
			ReplicationState: v1alpha1.ReplicationStateEstablished,
		})
	}
	return rvr
}

// mkResizeRV creates an RV with spec.Size and datamesh.Size set for resize tests.
func mkResizeRV(
	specSize, datameshSize resource.Quantity,
	revision int64,
	members []v1alpha1.DatameshMember,
	transitions []v1alpha1.ReplicatedVolumeDatameshTransition,
) *v1alpha1.ReplicatedVolume {
	rv := mkRV(revision, members, nil, transitions)
	rv.Spec.Size = specSize
	rv.Status.Datamesh.Size = datameshSize
	return rv
}

// ──────────────────────────────────────────────────────────────────────────────
// Dispatcher
//

var _ = Describe("ResizeVolume dispatch", func() {
	It("skip: datamesh.Size == spec.Size", func() {
		size := resource.MustParse("10Gi")
		bvSize := drbd_size.LowerVolumeSize(size)
		rv := mkResizeRV(size, size, 5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
			}, nil)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRResizeReady("rv-1-0", "node-1", bvSize, "rv-1-1"),
			mkRVRResizeReady("rv-1-1", "node-2", bvSize, "rv-1-0"),
		}
		settleEffectiveLayout(rv, rvrs)

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeFalse())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})

	It("skip: spec.Size < datamesh.Size (shrink not supported)", func() {
		specSize := resource.MustParse("5Gi")
		datameshSize := resource.MustParse("10Gi")
		bvSize := drbd_size.LowerVolumeSize(datameshSize)
		rv := mkResizeRV(specSize, datameshSize, 5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
			}, nil)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRResizeReady("rv-1-0", "node-1", bvSize),
		}
		settleEffectiveLayout(rv, rvrs)

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeFalse())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})

	It("dispatch: datamesh.Size < spec.Size", func() {
		specSize := resource.MustParse("20Gi")
		datameshSize := resource.MustParse("10Gi")
		bvSize := drbd_size.LowerVolumeSize(specSize)
		rv := mkResizeRV(specSize, datameshSize, 5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
			}, nil)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRResizeReady("rv-1-0", "node-1", bvSize, "rv-1-1"),
			mkRVRResizeReady("rv-1-1", "node-2", bvSize, "rv-1-0"),
		}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeResizeVolume))
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("resize/v1"))
		Expect(rv.Status.Datamesh.Size.Equal(specSize)).To(BeTrue())
		Expect(rv.Status.DatameshRevision).To(Equal(int64(6)))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Guards
//

var _ = Describe("ResizeVolume guards", func() {
	It("guard: no ready diskful member", func() {
		specSize := resource.MustParse("20Gi")
		datameshSize := resource.MustParse("10Gi")
		bvSize := drbd_size.LowerVolumeSize(specSize)
		rv := mkResizeRV(specSize, datameshSize, 5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
			}, nil)
		rvr := mkRVR("rv-1-0", "node-1", 5)
		rvr.Status.BackingVolume = &v1alpha1.ReplicatedVolumeReplicaStatusBackingVolume{
			State: v1alpha1.DiskStateUpToDate,
			Size:  &bvSize,
		}
		// Ready=False — guard should block.
		rvr.Status.Conditions = []metav1.Condition{{
			Type:   v1alpha1.ReplicatedVolumeReplicaCondReadyType,
			Status: metav1.ConditionFalse,
			Reason: "QuorumLost",
		}}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}

		ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		// No ResizeVolume transition created (guard blocked).
		for _, t := range rv.Status.DatameshTransitions {
			Expect(t.Type).NotTo(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeResizeVolume))
		}
	})

	It("guard: active resync", func() {
		specSize := resource.MustParse("20Gi")
		datameshSize := resource.MustParse("10Gi")
		bvSize := drbd_size.LowerVolumeSize(specSize)
		rv := mkResizeRV(specSize, datameshSize, 5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
			}, nil)
		rvr0 := mkRVRResizeReady("rv-1-0", "node-1", bvSize, "rv-1-1")
		rvr1 := mkRVRResizeReady("rv-1-1", "node-2", bvSize, "rv-1-0")
		// Override peer replication state to SyncTarget on rvr1.
		rvr1.Status.Peers[0].ReplicationState = v1alpha1.ReplicationStateSyncTarget
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr0, rvr1}

		ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		for _, t := range rv.Status.DatameshTransitions {
			Expect(t.Type).NotTo(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeResizeVolume))
		}
	})

	It("guard: backing volumes not grown", func() {
		specSize := resource.MustParse("20Gi")
		datameshSize := resource.MustParse("10Gi")
		smallBvSize := drbd_size.LowerVolumeSize(datameshSize) // too small for specSize
		rv := mkResizeRV(specSize, datameshSize, 5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
			}, nil)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRResizeReady("rv-1-0", "node-1", smallBvSize),
		}

		ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		for _, t := range rv.Status.DatameshTransitions {
			Expect(t.Type).NotTo(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeResizeVolume))
		}
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Settle (confirm)
//

var _ = Describe("ResizeVolume settle", func() {
	It("partial confirm: some members not confirmed", func() {
		specSize := resource.MustParse("20Gi")
		datameshSize := resource.MustParse("10Gi")
		bvSize := drbd_size.LowerVolumeSize(specSize)
		rv := mkResizeRV(specSize, datameshSize, 5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
			}, nil)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRResizeReady("rv-1-0", "node-1", bvSize, "rv-1-1"),
			mkRVRResizeReady("rv-1-1", "node-2", bvSize, "rv-1-0"),
		}

		// First call: dispatch.
		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})
		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))

		// Simulate: only rv-1-0 bumps revision, rv-1-1 stays at old.
		rvrs[0].Status.DatameshRevision = rv.Status.DatameshRevision
		// rvrs[1] stays at 5

		// Second call: settle — partial.
		_, _ = ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		// Transition still active (not all confirmed).
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeResizeVolume))
	})

	It("all confirmed: transition completed", func() {
		specSize := resource.MustParse("20Gi")
		datameshSize := resource.MustParse("10Gi")
		bvSize := drbd_size.LowerVolumeSize(specSize)
		rv := mkResizeRV(specSize, datameshSize, 5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
			}, nil)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRResizeReady("rv-1-0", "node-1", bvSize, "rv-1-1"),
			mkRVRResizeReady("rv-1-1", "node-2", bvSize, "rv-1-0"),
		}

		// Dispatch.
		ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))

		// Simulate: all bump revision.
		for _, rvr := range rvrs {
			rvr.Status.DatameshRevision = rv.Status.DatameshRevision
		}

		// Settle: all confirmed.
		ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.Datamesh.Size.Equal(specSize)).To(BeTrue())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// E2E settle loop
//

var _ = Describe("ResizeVolume e2e", func() {
	It("full lifecycle: dispatch → simulate → complete", func() {
		specSize := resource.MustParse("20Gi")
		datameshSize := resource.MustParse("10Gi")
		bvSize := drbd_size.LowerVolumeSize(specSize)
		rv := mkResizeRV(specSize, datameshSize, 5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
			}, nil)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRResizeReady("rv-1-0", "node-1", bvSize, "rv-1-1"),
			mkRVRResizeReady("rv-1-1", "node-2", bvSize, "rv-1-0"),
		}

		runSettleLoop(rv, nil, rvrs, nil, FeatureFlags{}, nil, nil)

		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.Datamesh.Size.Equal(specSize)).To(BeTrue())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Concurrency
//

var _ = Describe("ResizeVolume concurrency", func() {
	It("resize blocked by active AddReplica(D)", func() {
		specSize := resource.MustParse("20Gi")
		datameshSize := resource.MustParse("10Gi")
		bvSize := drbd_size.LowerVolumeSize(specSize)

		// Pre-existing AddReplica(D) transition.
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
		rv := mkResizeRV(specSize, datameshSize, 5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeLiminalDiskful, "node-3"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{addT},
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRResizeReady("rv-1-0", "node-1", bvSize, "rv-1-1"),
			mkRVRResizeReady("rv-1-1", "node-2", bvSize, "rv-1-0"),
			mkRVR("rv-1-2", "node-3", 5),
		}

		ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{})

		// Only the AddReplica transition; no ResizeVolume.
		for _, t := range rv.Status.DatameshTransitions {
			Expect(t.Type).NotTo(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeResizeVolume))
		}
	})

	It("AddReplica(D) blocked by active resize", func() {
		specSize := resource.MustParse("20Gi")
		datameshSize := resource.MustParse("10Gi")
		bvSize := drbd_size.LowerVolumeSize(specSize)

		// Pre-existing ResizeVolume transition.
		resizeT := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:   v1alpha1.ReplicatedVolumeDatameshTransitionTypeResizeVolume,
			Group:  v1alpha1.ReplicatedVolumeDatameshTransitionGroupResize,
			PlanID: "resize/v1",
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "resize", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					DatameshRevision: 6, StartedAt: ptr.To(metav1.Now())},
			},
		}
		rv := mkResizeRV(specSize, datameshSize, 6,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{resizeT},
		)
		rv.Status.DatameshReplicaRequests = []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
			mkJoinRequestD("rv-1-2"),
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRResizeReady("rv-1-0", "node-1", bvSize, "rv-1-1"),
			mkRVRResizeReady("rv-1-1", "node-2", bvSize, "rv-1-0"),
			mkRVR("rv-1-2", "node-3", 0),
		}

		ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{})

		// No AddReplica dispatched — only the existing ResizeVolume.
		for _, t := range rv.Status.DatameshTransitions {
			Expect(t.Type).NotTo(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica))
		}
	})
})
