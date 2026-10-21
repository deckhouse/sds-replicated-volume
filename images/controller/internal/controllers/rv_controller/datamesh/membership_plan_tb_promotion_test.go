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

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// A real R2 source: two current data copies and the same connected TB that
// will acquire the third disk. No fourth member or node is available.
func promotionFixture() (*v1alpha1.ReplicatedVolume, []*v1alpha1.ReplicatedVolumeReplica) {
	rv := mkRV(10, []v1alpha1.DatameshMember{
		mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-0"),
		mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
		mkMember("rv-1-2", v1alpha1.DatameshMemberTypeTieBreaker, "node-2"),
	}, []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
		mkChangeRoleRequest("rv-1-2", v1alpha1.ReplicaTypeDiskful),
	}, nil)
	rv.Status.Configuration.FailuresToTolerate = 1
	rv.Status.Configuration.GuaranteedMinimumDataRedundancy = 1
	rvrs := []*v1alpha1.ReplicatedVolumeReplica{
		mkRVRUpToDate("rv-1-0", "node-0", 10),
		mkRVRUpToDate("rv-1-1", "node-1", 10),
		mkRVR("rv-1-2", "node-2", 10),
	}
	for _, rvr := range rvrs {
		rvr.Generation = 1
		rvr.Spec.Type = v1alpha1.ReplicaTypeDiskful
		rvr.Spec.LVMVolumeGroupName = "test-lvg"
		rvr.Spec.LVMVolumeGroupThinPoolName = "test-thin"
		rvr.Status.Conditions = []metav1.Condition{
			{Type: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType, Status: metav1.ConditionTrue, ObservedGeneration: 1},
			{Type: v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyType, Status: metav1.ConditionTrue, ObservedGeneration: 1},
		}
	}
	simulateWithPeers(rv, rvrs)
	return rv, rvrs
}

var _ = Describe("in-place TB promotion", func() {
	It("restores a lost voter before dispatching a reserved TransZonal promotion", func() {
		rv, rvrs := promotionFixture()
		rv.Status.Configuration.Topology = v1alpha1.TopologyTransZonal
		rv.Status.Datamesh.Members = rv.Status.Datamesh.Members[1:]
		rv.Status.Datamesh.Quorum = 1
		rvrs = rvrs[1:]
		simulateWithPeers(rv, rvrs)
		rsp := mkRSPWithZones("node-0", "zone-0", "node-1", "zone-1", "node-2", "zone-2")
		ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, FeatureFlags{})
		Expect(rv.Status.DatameshTransitions).To(BeEmpty(), "do not dispatch the old odd-voter promotion")
		Expect(rv.Status.Datamesh.FindMemberByName("rv-1-2").Type).To(Equal(v1alpha1.DatameshMemberTypeTieBreaker))
		repair := mkRVR("rv-1-0", "node-0", 0)
		repair.Spec.Type = v1alpha1.ReplicaTypeDiskful
		rvrs = append(rvrs, repair)
		rv.Status.DatameshReplicaRequests = append(rv.Status.DatameshReplicaRequests, mkJoinRequestD(repair.Name))
		runSettleLoop(rv, rsp, rvrs, nil, FeatureFlags{}, simulateWithPeersAndResync, nil)
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.Datamesh.Members).To(HaveLen(3))
		Expect(rv.Status.Datamesh.FindMemberByName("rv-1-2").Type).To(Equal(v1alpha1.DatameshMemberTypeDiskful))
		Expect(rv.Status.Datamesh.QuorumMinimumRedundancy).To(Equal(byte(2)))
	})

	It("holds qmr from R3 intent, before a ChangeRole request exists", func() {
		rv, rvrs := promotionFixture()
		rv.Status.DatameshReplicaRequests = nil
		rvrs[2].Spec.Type = v1alpha1.ReplicaTypeTieBreaker
		ProcessTransitions(context.Background(), rv, mkRSP("node-0", "node-1", "node-2"), rvrs, nil, FeatureFlags{})
		Expect(rv.Status.Datamesh.QuorumMinimumRedundancy).To(Equal(byte(1)))
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})

	It("waits for bitmap acknowledgements, connectivity and full resync before committing qmr", func() {
		rv, rvrs := promotionFixture()
		rsp := mkRSP("node-0", "node-1", "node-2")
		process := func() { ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, FeatureFlags{}) }
		process()
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("tb-to-d/v2"))
		Expect(rv.Status.Datamesh.Members[2].Type).To(Equal(v1alpha1.DatameshMemberTypeLiminalDiskful))
		Expect(rv.Status.Datamesh.QuorumMinimumRedundancy).To(Equal(byte(1)))

		// A configured revision without connections cannot authorize attach.
		simulateRevisionBump(rv, rvrs)
		for _, rvr := range rvrs {
			rvr.Status.Peers = nil
		}
		process()
		Expect(rv.Status.Datamesh.Members[2].Type).To(Equal(v1alpha1.DatameshMemberTypeLiminalDiskful))
		simulateWithPeers(rv, rvrs)
		process()
		Expect(rv.Status.Datamesh.Members[2].Type).To(Equal(v1alpha1.DatameshMemberTypeDiskful))

		simulateWithPeers(rv, rvrs)
		rvrs[2].Status.BackingVolume = &v1alpha1.ReplicatedVolumeReplicaStatusBackingVolume{State: v1alpha1.DiskStateInconsistent}
		process()
		Expect(rv.Status.Datamesh.QuorumMinimumRedundancy).To(Equal(byte(1)))
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.BaselineGuaranteedMinimumDataRedundancy).To(BeZero())

		rvrs[2].Status.BackingVolume.State = v1alpha1.DiskStateUpToDate
		process()
		Expect(rv.Status.Datamesh.QuorumMinimumRedundancy).To(Equal(byte(2)))
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.BaselineGuaranteedMinimumDataRedundancy).To(BeZero())
		simulateWithPeers(rv, rvrs)
		process()
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.BaselineGuaranteedMinimumDataRedundancy).To(Equal(byte(1)))
		Expect(rv.Status.Datamesh.Members).To(HaveLen(3))
	})

	It("keeps the TB operational while its backing volume is not ready", func() {
		rv, rvrs := promotionFixture()
		rvrs[2].Status.Conditions[1].Status = metav1.ConditionFalse
		ProcessTransitions(context.Background(), rv, mkRSP("node-0", "node-1", "node-2"), rvrs, nil, FeatureFlags{})
		Expect(rv.Status.Datamesh.Members[2].Type).To(Equal(v1alpha1.DatameshMemberTypeTieBreaker))
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.Datamesh.QuorumMinimumRedundancy).To(Equal(byte(1)))
	})

	It("does not promote while an extra Diskful replica is pending", func() {
		rv, rvrs := promotionFixture()
		pending := mkRVR("rv-1-3", "", 0)
		pending.Spec.Type = v1alpha1.ReplicaTypeDiskful
		rvrs = append(rvrs, pending)
		ProcessTransitions(context.Background(), rv, mkRSP("node-0", "node-1", "node-2"), rvrs, nil, FeatureFlags{})
		Expect(rv.Status.Datamesh.Members[2].Type).To(Equal(v1alpha1.DatameshMemberTypeTieBreaker))
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})

	It("recovers an already committed qmr=2 without lowering it", func() {
		rv, rvrs := promotionFixture()
		rv.Status.Datamesh.QuorumMinimumRedundancy = 2
		rv.Status.BaselineGuaranteedMinimumDataRedundancy = 1
		runSettleLoop(rv, mkRSP("node-0", "node-1", "node-2"), rvrs, nil, FeatureFlags{}, simulateWithPeersAndResync,
			func(rv *v1alpha1.ReplicatedVolume, _ int) {
				Expect(rv.Status.Datamesh.QuorumMinimumRedundancy).To(Equal(byte(2)))
			})
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.Datamesh.Members[2].Type).To(Equal(v1alpha1.DatameshMemberTypeDiskful))
	})

	It("blocks both a concurrent placed JoinD and the reserved promotion", func() {
		rv, rvrs := promotionFixture()
		extra := mkRVR("rv-1-3", "node-3", 0)
		extra.Spec.Type = v1alpha1.ReplicaTypeDiskful
		extra.Spec.LVMVolumeGroupName = "test-lvg"
		rvrs = append(rvrs, extra)
		rv.Status.DatameshReplicaRequests = append(rv.Status.DatameshReplicaRequests, mkJoinRequestD(extra.Name))
		for range 3 {
			ProcessTransitions(context.Background(), rv, mkRSP("node-0", "node-1", "node-2", "node-3"), rvrs, nil, FeatureFlags{})
			simulateWithPeers(rv, rvrs)
			Expect(rv.Status.Datamesh.Members).To(HaveLen(3))
			Expect(rv.Status.Datamesh.Members[2].Type).To(Equal(v1alpha1.DatameshMemberTypeTieBreaker))
			Expect(rv.Status.DatameshTransitions).To(BeEmpty())
			Expect(rv.Status.Datamesh.QuorumMinimumRedundancy).To(Equal(byte(1)))
		}
	})

	It("rejects a stale or disconnected data holder before changing the TB", func() {
		rv, rvrs := promotionFixture()
		rvrs[0].Status.Conditions[0].ObservedGeneration = 0
		ProcessTransitions(context.Background(), rv, mkRSP("node-0", "node-1", "node-2"), rvrs, nil, FeatureFlags{})
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		rvrs[0].Status.Conditions[0].ObservedGeneration = rvrs[0].Generation
		rvrs[0].Status.Peers = nil
		ProcessTransitions(context.Background(), rv, mkRSP("node-0", "node-1", "node-2"), rvrs, nil, FeatureFlags{})
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.Datamesh.Members[2].Type).To(Equal(v1alpha1.DatameshMemberTypeTieBreaker))
	})

	It("requires three distinct zones for the TransZonal promotion", func() {
		rv, rvrs := promotionFixture()
		rv.Status.Configuration.Topology = v1alpha1.TopologyTransZonal
		rsp := mkRSPWithZones("node-0", "zone-0", "node-1", "zone-1", "node-2", "zone-1")
		ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, FeatureFlags{})
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		rsp.nodes[2].ZoneName = "zone-2"
		ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, FeatureFlags{})
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].PlanID).To(Equal("tb-to-d/v2"))
	})

	It("resumes a persisted resync step and honors a target reverted to R2", func() {
		rv, rvrs := promotionFixture()
		rsp := mkRSP("node-0", "node-1", "node-2")
		ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, FeatureFlags{})
		simulateWithPeers(rv, rvrs)
		ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, FeatureFlags{})
		// No process-local context is retained by a restarted controller.
		rv = rv.DeepCopy()
		rv.Status.Configuration.GuaranteedMinimumDataRedundancy = 0
		simulateWithPeersAndResync(rv, rvrs)
		runSettleLoop(rv, rsp, rvrs, nil, FeatureFlags{}, simulateWithPeersAndResync, nil)
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.Datamesh.QuorumMinimumRedundancy).To(Equal(byte(1)))
		Expect(rv.Status.BaselineGuaranteedMinimumDataRedundancy).To(BeZero())
	})

	It("releases the voting slot after emergency removal and holds qmr through the replacement resync", func() {
		rv, rvrs := promotionFixture()
		rsp := mkRSP("node-0", "node-1", "node-2", "node-3")
		ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, FeatureFlags{})
		simulateWithPeers(rv, rvrs)
		ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, FeatureFlags{})
		removeRVR(&rvrs, "rv-1-0")
		rv.Status.DatameshReplicaRequests = []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkForceLeaveRequest("rv-1-0")}
		simulateWithPeersAndResync(rv, rvrs)
		runSettleLoop(rv, rsp, rvrs, nil, FeatureFlags{}, simulateWithPeersAndResync,
			func(rv *v1alpha1.ReplicatedVolume, _ int) {
				Expect(rv.Status.Datamesh.QuorumMinimumRedundancy).To(Equal(byte(1)))
			})
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.Datamesh.Members).To(HaveLen(2))

		replacement := mkRVR("rv-1-3", "node-3", 0)
		replacement.Spec.Type = v1alpha1.ReplicaTypeDiskful
		rvrs = append(rvrs, replacement)
		rv.Status.DatameshReplicaRequests = []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD(replacement.Name)}
		runSettleLoop(rv, rsp, rvrs, nil, FeatureFlags{}, simulateWithPeers,
			func(rv *v1alpha1.ReplicatedVolume, _ int) {
				Expect(rv.Status.Datamesh.QuorumMinimumRedundancy).To(Equal(byte(1)))
			})
		Expect(rv.Status.Datamesh.Members).To(HaveLen(3))
		Expect(rv.Status.Datamesh.QuorumMinimumRedundancy).To(Equal(byte(1)))
		simulateWithPeersAndResync(rv, rvrs)
		runSettleLoop(rv, rsp, rvrs, nil, FeatureFlags{}, simulateWithPeersAndResync, nil)
		Expect(rv.Status.Datamesh.QuorumMinimumRedundancy).To(Equal(byte(2)))
	})
})
