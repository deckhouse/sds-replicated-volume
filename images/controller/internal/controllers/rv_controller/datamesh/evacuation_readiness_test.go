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
	"maps"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/dmte"
)

var _ = Describe("EvacuationBlock", func() {
	DescribeTable("reports the same guard verdict as dispatch", func(
		ctx SpecContext,
		change func(*v1alpha1.ReplicatedVolume, []*v1alpha1.ReplicatedVolumeReplica, Evacuees),
		want string,
	) {
		rv, rvrs := evacuationLayout(3)
		evacuees := Evacuees{"rv-1-2": {}}
		change(rv, rvrs, evacuees)
		beforeRVRs := make([]*v1alpha1.ReplicatedVolumeReplica, len(rvrs))
		for i, rvr := range rvrs {
			beforeRVRs[i] = rvr.DeepCopy()
		}
		beforeEvacuees := maps.Clone(evacuees)

		_, contexts, _ := ProcessTransitionsWithCompleted(ctx, rv, nil, rvrs, nil, FeatureFlags{}, evacuees)
		beforeRV := rv.DeepCopy()
		name, message := EvacuationBlock(contexts)
		Expect(message).To(Equal(want))
		if want == "" {
			Expect(name).To(BeEmpty())
			Expect(rv.Status.Datamesh.FindMemberByName("rv-1-2")).To(BeNil())
		} else {
			Expect(name).To(Equal("rv-1-2"))
			Expect(rv.Status.Datamesh.FindMemberByName(name)).NotTo(BeNil())
			Expect(rv.Status.DatameshTransitions).NotTo(ContainElement(
				HaveField("Type", v1alpha1.ReplicatedVolumeDatameshTransitionTypeForceRemoveReplica)))
		}
		Expect(rv).To(Equal(beforeRV))
		Expect(rvrs).To(Equal(beforeRVRs))
		Expect(evacuees).To(Equal(beforeEvacuees))
	},
		Entry("ready", func(_ *v1alpha1.ReplicatedVolume, _ []*v1alpha1.ReplicatedVolumeReplica, _ Evacuees) {}, ""),
		Entry("attachment still blocks after dispatch starts Detach", func(rv *v1alpha1.ReplicatedVolume, rvrs []*v1alpha1.ReplicatedVolumeReplica, _ Evacuees) {
			rv.Status.Datamesh.Members[2].Attached = true
			rvrs[0].Status.Peers = []v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{{Name: "rv-1-2", ConnectionState: v1alpha1.ConnectionStateConnected}}
		}, "detach transition in progress"),
		Entry("reachable peers sorted by name", func(_ *v1alpha1.ReplicatedVolume, rvrs []*v1alpha1.ReplicatedVolumeReplica, _ Evacuees) {
			for _, rvr := range rvrs[:2] {
				rvr.Status.Peers = []v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{{Name: "rv-1-2", ConnectionState: v1alpha1.ConnectionStateConnected}}
			}
			slices.Reverse(rvrs)
		}, "member is reachable over DRBD (Connected from 2 replica(s): [rv-1-0, rv-1-1])"),
		Entry("live quorum precedes UpToDate", func(rv *v1alpha1.ReplicatedVolume, rvrs []*v1alpha1.ReplicatedVolumeReplica, evacuees Evacuees) {
			// Keep rv-1-2 first by name: rv-1-3 is also dead and must not count.
			rv.Status.Datamesh.Members[1].Name, rvrs[1].Name = "rv-1-3", "rv-1-3"
			evacuees["rv-1-3"] = struct{}{}
			rvrs[0].Status.BackingVolume.State = v1alpha1.DiskStateOutdated
		}, "evacuation would leave 1 live voter(s), quorum after removal needs 2"),
		Entry("no live UpToDate copy", func(_ *v1alpha1.ReplicatedVolume, rvrs []*v1alpha1.ReplicatedVolumeReplica, _ Evacuees) {
			for _, rvr := range rvrs[:2] {
				rvr.Status.BackingVolume.State = v1alpha1.DiskStateOutdated
			}
		}, "no live UpToDate diskful replica would remain"),
		Entry("pending Detach after Attached was cleared", func(rv *v1alpha1.ReplicatedVolume, rvrs []*v1alpha1.ReplicatedVolumeReplica, _ Evacuees) {
			rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{{
				Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach, PlanID: "detach/v1", ReplicaName: "rv-1-2",
				Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{
					Name: "Detach", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, DatameshRevision: 5,
				}},
			}}
			rvrs[2].Status.DatameshRevision = 4
		}, "detach transition in progress"),
		Entry("manual ForceLeave retains the shared guards' emergency semantics", func(rv *v1alpha1.ReplicatedVolume, rvrs []*v1alpha1.ReplicatedVolumeReplica, _ Evacuees) {
			rv.Status.DatameshReplicaRequests = []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkForceLeaveRequest("rv-1-2")}
			for _, rvr := range rvrs[:2] {
				rvr.Status.BackingVolume.State = v1alpha1.DiskStateOutdated
			}
		}, ""),
	)

	It("matches dispatch in voter-first name order and reselects after removal", func() {
		rv, rvrs := evacuationLayout(5)
		rv.Status.Datamesh.Members[0].Type = v1alpha1.DatameshMemberTypeTieBreaker
		rvrs[0].Spec.Type = v1alpha1.ReplicaTypeTieBreaker
		rv.Status.Datamesh.Members[3].Name, rvrs[3].Name = "rv-1-10", "rv-1-10"
		evacuees := Evacuees{"rv-1-0": {}, "rv-1-2": {}, "rv-1-10": {}}
		for _, want := range []struct {
			name string
			id   uint8
			plan string
		}{
			{"rv-1-10", 10, "diskful-q-down/v1"},
			{"rv-1-2", 2, "diskful/v1"},
			{"rv-1-0", 0, "tiebreaker/v1"},
		} {
			p := buildContexts(rv, nil, rvrs, nil, FeatureFlags{}, evacuees)
			Expect(selectEvacuee(p.Global()).Name()).To(Equal(want.name))
			Expect(slices.Collect(membershipDispatcher()(p))).To(Equal([]dmte.DispatchDecision{
				dmte.DispatchReplica(p.Replica(want.id), v1alpha1.ReplicatedVolumeDatameshTransitionTypeForceRemoveReplica, dmte.PlanID(want.plan)),
			}))
			rv.Status.Datamesh.Members = slices.DeleteFunc(rv.Status.Datamesh.Members, func(m v1alpha1.DatameshMember) bool { return m.Name == want.name })
		}
		p := buildContexts(rv, nil, rvrs, nil, FeatureFlags{}, evacuees)
		Expect(selectEvacuee(p.Global())).To(BeNil())
	})

	It("has no block without evacuees or contexts", func(ctx SpecContext) {
		name, message := EvacuationBlock(nil)
		Expect(name).To(BeEmpty())
		Expect(message).To(BeEmpty())
		rv, rvrs := evacuationLayout(3)
		_, contexts, _ := ProcessTransitionsWithCompleted(ctx, rv, nil, rvrs, nil, FeatureFlags{}, nil)
		name, message = EvacuationBlock(contexts)
		Expect(name).To(BeEmpty())
		Expect(message).To(BeEmpty())
	})

	It("uses attachment state after the engine settles Detach", func(ctx SpecContext) {
		rv, rvrs := evacuationLayout(3)
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{{
			Type:   v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach,
			PlanID: "detach/v1", ReplicaName: "rv-1-2",
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{
				Name: "Detach", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, DatameshRevision: 5,
			}},
		}}
		_, contexts, _ := ProcessTransitionsWithCompleted(ctx, rv, nil, rvrs, nil, FeatureFlags{}, Evacuees{"rv-1-2": {}})
		name, message := EvacuationBlock(contexts)
		Expect(name).To(BeEmpty())
		Expect(message).To(BeEmpty())
		Expect(rv.Status.Datamesh.FindMemberByName("rv-1-2")).To(BeNil())
		Expect(rv.Status.DatameshTransitions).To(ContainElement(
			HaveField("Type", v1alpha1.ReplicatedVolumeDatameshTransitionTypeForceRemoveReplica)))
	})

	It("ignores non-members, orphans and non-evacuated member types", func() {
		rv, rvrs := evacuationLayout(3)
		rv.Status.Datamesh.Members[0].Type = v1alpha1.DatameshMemberTypeAccess
		rv.Status.Datamesh.Members[1].Type = v1alpha1.DatameshMemberTypeShadowDiskful
		p := buildContexts(rv, nil, rvrs[:2], nil, FeatureFlags{}, Evacuees{"rv-1-0": {}, "rv-1-1": {}, "rv-1-2": {}, "rv-1-3": {}})
		name, message := EvacuationBlock(p.Global().allReplicas)
		Expect(name).To(BeEmpty())
		Expect(message).To(BeEmpty())
	})
})
