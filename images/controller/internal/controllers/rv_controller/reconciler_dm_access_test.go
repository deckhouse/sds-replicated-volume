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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/idset"
)

// ──────────────────────────────────────────────────────────────────────────────
// Tests: ensureDatameshAddAccessReplica
//

var _ = Describe("ensureDatameshAddAccessReplica", func() {
	mkRV := func(members []v1alpha1.DatameshMember, rev int64) *v1alpha1.ReplicatedVolume { //nolint:unparam
		return &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
					VolumeAccess: v1alpha1.VolumeAccessPreferablyLocal,
				},
				DatameshRevision: rev,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: members,
				},
			},
		}
	}

	mkJoinRequest := func(name string) *v1alpha1.ReplicatedVolumeDatameshReplicaRequest { //nolint:unparam
		return &v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
			Name: name,
			Request: v1alpha1.DatameshMembershipRequest{
				Operation: v1alpha1.DatameshMembershipRequestOperationJoin,
				Type:      v1alpha1.ReplicaTypeAccess,
			},
			FirstObservedAt: metav1.Now(),
		}
	}

	mkRSP := func(nodeNames ...string) *rspView {
		var nodes []v1alpha1.ReplicatedStoragePoolEligibleNode
		for _, n := range nodeNames {
			nodes = append(nodes, v1alpha1.ReplicatedStoragePoolEligibleNode{NodeName: n})
		}
		return &rspView{EligibleNodes: nodes}
	}

	mkRVR := func(name, nodeName string) *v1alpha1.ReplicatedVolumeReplica { //nolint:unparam
		return &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				NodeName: nodeName,
				Type:     v1alpha1.ReplicaTypeAccess,
			},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				Addresses: []v1alpha1.DRBDResourceAddressStatus{{SystemNetworkName: "default"}},
			},
		}
	}

	It("creates AddAccessReplica transition and member", func() {
		rv := mkRV([]v1alpha1.DatameshMember{
			{Name: "rv-1-0", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "node-1"},
		}, 5)
		replicaReq := mkJoinRequest("rv-1-1")
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-1", "node-2")}

		changed := ensureDatameshAddAccessReplica(rv, rvrs, replicaReq, idset.Of(0), mkRSP("node-1", "node-2"))

		Expect(changed).To(BeTrue())

		// Member added.
		Expect(rv.Status.Datamesh.Members).To(HaveLen(2))
		newMember := rv.Status.Datamesh.FindMemberByName("rv-1-1")
		Expect(newMember).NotTo(BeNil())
		Expect(newMember.Type).To(Equal(v1alpha1.DatameshMemberTypeAccess))
		Expect(newMember.NodeName).To(Equal("node-2"))
		Expect(newMember.Attached).To(BeFalse())

		// Revision incremented.
		Expect(rv.Status.DatameshRevision).To(Equal(int64(6)))

		// Transition created with message from progress function.
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		t := rv.Status.DatameshTransitions[0]
		Expect(t.Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddAccessReplica))
		Expect(t.ReplicaName).To(Equal("rv-1-1"))
		Expect(t.DatameshRevision).To(Equal(int64(6)))
		Expect(t.Message).To(ContainSubstring("0/2"))
		Expect(t.Message).To(ContainSubstring("revision 6"))
	})

	It("skips when RV is deleting", func() {
		rv := mkRV(nil, 5)
		rv.DeletionTimestamp = ptr.To(metav1.Now())
		replicaReq := mkJoinRequest("rv-1-1")
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-1", "node-2")}

		changed := ensureDatameshAddAccessReplica(rv, rvrs, replicaReq, 0, nil)

		Expect(changed).To(BeTrue()) // message changed
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(replicaReq.Message).To(ContainSubstring("Will not join datamesh"))
	})

	It("skips when already a datamesh member", func() {
		rv := mkRV([]v1alpha1.DatameshMember{
			{Name: "rv-1-1", Type: v1alpha1.DatameshMemberTypeAccess, NodeName: "node-2"},
		}, 5)
		replicaReq := mkJoinRequest("rv-1-1")
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-1", "node-2")}

		changed := ensureDatameshAddAccessReplica(rv, rvrs, replicaReq, 0, nil)

		Expect(changed).To(BeFalse())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})

	It("skips when any member on same node", func() {
		rv := mkRV([]v1alpha1.DatameshMember{
			{Name: "rv-1-0", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "node-2"},
		}, 5)
		replicaReq := mkJoinRequest("rv-1-1")
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-1", "node-2")}

		changed := ensureDatameshAddAccessReplica(rv, rvrs, replicaReq, idset.Of(0), nil)

		Expect(changed).To(BeTrue()) // message changed
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(replicaReq.Message).To(ContainSubstring("Diskful"))
		Expect(replicaReq.Message).To(ContainSubstring("node-2"))
	})

	It("skips when VolumeAccess is Local", func() {
		rv := mkRV(nil, 5)
		rv.Status.Configuration.VolumeAccess = v1alpha1.VolumeAccessLocal
		replicaReq := mkJoinRequest("rv-1-1")
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-1", "node-2")}

		changed := ensureDatameshAddAccessReplica(rv, rvrs, replicaReq, 0, nil)

		Expect(changed).To(BeTrue()) // message changed
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(replicaReq.Message).To(ContainSubstring("volumeAccess is Local"))
	})

	It("skips silently when RVR not found", func() {
		rv := mkRV(nil, 5)
		replicaReq := mkJoinRequest("rv-1-1")

		changed := ensureDatameshAddAccessReplica(rv, nil, replicaReq, 0, mkRSP("node-2"))

		Expect(changed).To(BeFalse())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})

	It("waits when addresses empty", func() {
		rv := mkRV(nil, 5)
		replicaReq := mkJoinRequest("rv-1-1")
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-1-1"},
			Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-2"},
		}

		changed := ensureDatameshAddAccessReplica(rv, []*v1alpha1.ReplicatedVolumeReplica{rvr}, replicaReq, 0, mkRSP("node-2"))

		Expect(changed).To(BeTrue()) // message changed
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(replicaReq.Message).To(ContainSubstring("addresses"))
	})

	It("waits when RSP is nil", func() {
		rv := mkRV(nil, 5)
		replicaReq := mkJoinRequest("rv-1-1")
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-1", "node-2")}

		changed := ensureDatameshAddAccessReplica(rv, rvrs, replicaReq, 0, nil)

		Expect(changed).To(BeTrue()) // message changed
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(replicaReq.Message).To(ContainSubstring("ReplicatedStoragePool"))
	})

	It("skips when node not in eligible nodes", func() {
		rv := mkRV(nil, 5)
		replicaReq := mkJoinRequest("rv-1-1")
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-1", "node-2")}

		changed := ensureDatameshAddAccessReplica(rv, rvrs, replicaReq, 0, mkRSP("node-1"))

		Expect(changed).To(BeTrue()) // message changed
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(replicaReq.Message).To(ContainSubstring("node-2"))
		Expect(replicaReq.Message).To(ContainSubstring("eligible"))
	})

	It("extracts zone from RSP eligible node", func() {
		rv := mkRV([]v1alpha1.DatameshMember{
			{Name: "rv-1-0", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "node-1"},
		}, 5)
		replicaReq := mkJoinRequest("rv-1-1")
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-1", "node-2")}
		rsp := &rspView{EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1", ZoneName: "zone-a"},
			{NodeName: "node-2", ZoneName: "zone-b"},
		}}

		changed := ensureDatameshAddAccessReplica(rv, rvrs, replicaReq, idset.Of(0), rsp)

		Expect(changed).To(BeTrue())
		newMember := rv.Status.Datamesh.FindMemberByName("rv-1-1")
		Expect(newMember).NotTo(BeNil())
		Expect(newMember.Zone).To(Equal("zone-b"))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Tests: ensureDatameshRemoveAccessReplica
//

var _ = Describe("ensureDatameshRemoveAccessReplica", func() {
	mkRV := func(members []v1alpha1.DatameshMember, rev int64) *v1alpha1.ReplicatedVolume { //nolint:unparam
		return &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshRevision: rev,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: members,
				},
			},
		}
	}

	mkLeaveRequest := func(name string) *v1alpha1.ReplicatedVolumeDatameshReplicaRequest { //nolint:unparam
		return &v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
			Name: name,
			Request: v1alpha1.DatameshMembershipRequest{
				Operation: v1alpha1.DatameshMembershipRequestOperationLeave,
			},
			FirstObservedAt: metav1.Now(),
		}
	}

	It("creates RemoveAccessReplica transition and removes member", func() {
		rv := mkRV([]v1alpha1.DatameshMember{
			{Name: "rv-1-0", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "node-1"},
			{Name: "rv-1-1", Type: v1alpha1.DatameshMemberTypeAccess, NodeName: "node-2", Attached: false},
		}, 5)
		replicaReq := mkLeaveRequest("rv-1-1")

		changed := ensureDatameshRemoveAccessReplica(rv, nil, replicaReq, idset.Of(0))

		Expect(changed).To(BeTrue())

		// Member removed.
		Expect(rv.Status.Datamesh.Members).To(HaveLen(1))
		Expect(rv.Status.Datamesh.FindMemberByName("rv-1-1")).To(BeNil())

		// Revision incremented.
		Expect(rv.Status.DatameshRevision).To(Equal(int64(6)))

		// Transition created with message from progress function.
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		t := rv.Status.DatameshTransitions[0]
		Expect(t.Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveAccessReplica))
		Expect(t.ReplicaName).To(Equal("rv-1-1"))
		Expect(t.DatameshRevision).To(Equal(int64(6)))
		Expect(t.Message).To(ContainSubstring("0/"))
	})

	It("skips when not a datamesh member", func() {
		rv := mkRV(nil, 5)
		replicaReq := mkLeaveRequest("rv-1-1")

		changed := ensureDatameshRemoveAccessReplica(rv, nil, replicaReq, 0)

		Expect(changed).To(BeFalse())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})

	It("skips when member is attached", func() {
		rv := mkRV([]v1alpha1.DatameshMember{
			{Name: "rv-1-1", Type: v1alpha1.DatameshMemberTypeAccess, NodeName: "node-2", Attached: true},
		}, 5)
		replicaReq := mkLeaveRequest("rv-1-1")

		changed := ensureDatameshRemoveAccessReplica(rv, nil, replicaReq, 0)

		Expect(changed).To(BeTrue()) // message changed
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.Datamesh.Members).To(HaveLen(1))
		Expect(replicaReq.Message).To(ContainSubstring("attached"))
	})

	It("works in detach-only mode (RV deleting)", func() {
		rv := mkRV([]v1alpha1.DatameshMember{
			{Name: "rv-1-0", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "node-1"},
			{Name: "rv-1-1", Type: v1alpha1.DatameshMemberTypeAccess, NodeName: "node-2", Attached: false},
		}, 5)
		rv.DeletionTimestamp = ptr.To(metav1.Now())
		replicaReq := mkLeaveRequest("rv-1-1")

		changed := ensureDatameshRemoveAccessReplica(rv, nil, replicaReq, idset.Of(0))

		// Leave still proceeds when RV is deleting (per section 13).
		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveAccessReplica))
	})

	It("skips when member type is not Access (defensive)", func() {
		rv := mkRV([]v1alpha1.DatameshMember{
			{Name: "rv-1-1", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "node-2"},
		}, 5)
		replicaReq := mkLeaveRequest("rv-1-1")

		changed := ensureDatameshRemoveAccessReplica(rv, nil, replicaReq, 0)

		Expect(changed).To(BeFalse())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.Datamesh.Members).To(HaveLen(1))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Tests: ensureDatameshAccessReplicaTransitionProgress
//

var _ = Describe("ensureDatameshAccessReplicaTransitionProgress", func() {
	mkRVR := func(name string, datameshRev int64) *v1alpha1.ReplicatedVolumeReplica {
		return &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshRevision: datameshRev,
			},
		}
	}

	mkReplicaRequest := func(name string, join bool) *v1alpha1.ReplicatedVolumeDatameshReplicaRequest { //nolint:unparam
		req := v1alpha1.DatameshMembershipRequest{}
		if join {
			req.Operation = v1alpha1.DatameshMembershipRequestOperationJoin
			req.Type = v1alpha1.ReplicaTypeAccess
		} else {
			req.Operation = v1alpha1.DatameshMembershipRequestOperationLeave
		}
		return &v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
			Name:    name,
			Request: req,
		}
	}

	It("completes RemoveAccessReplica when all confirmed normally", func() {
		t := &v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:             v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveAccessReplica,
			DatameshRevision: 6, ReplicaName: "rv-1-1", StartedAt: metav1.Now(),
		}
		replicaReq := mkReplicaRequest("rv-1-1", false)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-0", 6), mkRVR("rv-1-1", 6)}

		completed, changed := ensureDatameshAccessReplicaTransitionProgress(rvrs, t, replicaReq, idset.Of(0))

		Expect(completed).To(BeTrue())
		Expect(changed).To(BeTrue())
		Expect(replicaReq.Message).To(Equal("Left datamesh successfully"))
	})

	It("completes RemoveAccessReplica when subject has revision 0", func() {
		t := &v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:             v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveAccessReplica,
			DatameshRevision: 6, ReplicaName: "rv-1-1", StartedAt: metav1.Now(),
		}
		replicaReq := mkReplicaRequest("rv-1-1", false)
		// Diskful confirmed (rev 6), subject reset revision to 0 (left datamesh).
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-0", 6), mkRVR("rv-1-1", 0)}

		completed, changed := ensureDatameshAccessReplicaTransitionProgress(rvrs, t, replicaReq, idset.Of(0))

		Expect(completed).To(BeTrue())
		Expect(changed).To(BeTrue())
		Expect(replicaReq.Message).To(Equal("Left datamesh successfully"))
	})

	It("stays in progress when subject has revision 0 but diskful not confirmed", func() {
		t := &v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:             v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveAccessReplica,
			DatameshRevision: 6, ReplicaName: "rv-1-1", StartedAt: metav1.Now(),
		}
		replicaReq := mkReplicaRequest("rv-1-1", false)
		// Subject reset (rev 0 = confirmed), but diskful still at rev 4.
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-0", 4), mkRVR("rv-1-1", 0)}

		completed, changed := ensureDatameshAccessReplicaTransitionProgress(rvrs, t, replicaReq, idset.Of(0))

		Expect(completed).To(BeFalse())
		Expect(changed).To(BeTrue()) // messages set
		Expect(t.Message).To(ContainSubstring("1/2"))
		Expect(replicaReq.Message).To(ContainSubstring("Leaving datamesh"))
	})

	It("does not show PendingDatameshJoin as error for AddAccessReplica subject", func() {
		t := &v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:             v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddAccessReplica,
			DatameshRevision: 6, ReplicaName: "rv-1-1", StartedAt: metav1.Now(),
		}
		replicaReq := mkReplicaRequest("rv-1-1", true)
		// Diskful confirmed. Subject waiting with DRBDConfigured=False/PendingDatameshJoin.
		subjectRVR := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-1-1", Generation: 1},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshRevision: 5,
				Conditions: []metav1.Condition{
					{Type: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType, Status: metav1.ConditionFalse,
						Reason: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonPendingDatameshJoin, ObservedGeneration: 1},
				},
			},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-0", 6), subjectRVR}

		completed, changed := ensureDatameshAccessReplicaTransitionProgress(rvrs, t, replicaReq, idset.Of(0))

		Expect(completed).To(BeFalse())
		Expect(changed).To(BeTrue())
		// PendingDatameshJoin should NOT appear in errors.
		Expect(t.Message).NotTo(ContainSubstring("Errors"))
		Expect(t.Message).NotTo(ContainSubstring("PendingDatameshJoin"))
		Expect(t.Message).To(ContainSubstring("1/2"))
	})

	It("shows other DRBDConfigured=False reasons as errors for AddAccessReplica subject", func() {
		t := &v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:             v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddAccessReplica,
			DatameshRevision: 6, ReplicaName: "rv-1-1", StartedAt: metav1.Now(),
		}
		replicaReq := mkReplicaRequest("rv-1-1", true)
		// Subject waiting with DRBDConfigured=False/ConfigurationFailed — this IS an error.
		subjectRVR := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-1-1", Generation: 1},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshRevision: 5,
				Conditions: []metav1.Condition{
					{Type: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType, Status: metav1.ConditionFalse,
						Reason: "ConfigurationFailed", ObservedGeneration: 1},
				},
			},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-0", 6), subjectRVR}

		completed, changed := ensureDatameshAccessReplicaTransitionProgress(rvrs, t, replicaReq, idset.Of(0))

		Expect(completed).To(BeFalse())
		Expect(changed).To(BeTrue())
		Expect(t.Message).To(ContainSubstring("Errors"))
		Expect(t.Message).To(ContainSubstring("ConfigurationFailed"))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Tests: ensureDatameshAccessReplicas (integration)
//

var _ = Describe("ensureDatameshAccessReplicas", func() {
	mkRVR := func(name string, datameshRev int64) *v1alpha1.ReplicatedVolumeReplica {
		return &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshRevision: datameshRev,
			},
		}
	}

	It("completes transition and removes it", func(ctx SpecContext) {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-0", Type: v1alpha1.DatameshMemberTypeDiskful},
						{Name: "rv-1-1", Type: v1alpha1.DatameshMemberTypeAccess},
					},
				},
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddAccessReplica,
						DatameshRevision: 6, ReplicaName: "rv-1-1", StartedAt: metav1.Now()},
				},
				DatameshReplicaRequests: []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
					{Name: "rv-1-1", Request: v1alpha1.DatameshMembershipRequest{
						Operation: v1alpha1.DatameshMembershipRequestOperationJoin, Type: v1alpha1.ReplicaTypeAccess,
					}},
				},
			},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-0", 6), mkRVR("rv-1-1", 6)}

		outcome := ensureDatameshAccessReplicas(ctx, rv, rvrs, nil)

		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(ContainSubstring("Joined datamesh successfully"))
	})

	It("updates progress when transition not complete", func(ctx SpecContext) {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-0", Type: v1alpha1.DatameshMemberTypeDiskful},
						{Name: "rv-1-1", Type: v1alpha1.DatameshMemberTypeAccess},
					},
				},
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddAccessReplica,
						DatameshRevision: 6, ReplicaName: "rv-1-1", StartedAt: metav1.Now()},
				},
				DatameshReplicaRequests: []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
					{Name: "rv-1-1", Request: v1alpha1.DatameshMembershipRequest{
						Operation: v1alpha1.DatameshMembershipRequestOperationJoin, Type: v1alpha1.ReplicaTypeAccess,
					}},
				},
			},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-0", 6), mkRVR("rv-1-1", 5)}

		outcome := ensureDatameshAccessReplicas(ctx, rv, rvrs, nil)

		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Message).To(ContainSubstring("1/2"))
		Expect(rv.Status.DatameshTransitions[0].Message).To(ContainSubstring("#1"))
	})

	It("surfaces errors from DRBDConfigured condition", func(ctx SpecContext) {
		failingRVR := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-1-0", Generation: 1},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshRevision: 5,
				Conditions: []metav1.Condition{
					{Type: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType, Status: metav1.ConditionFalse,
						Reason: "ConfigurationFailed", ObservedGeneration: 1},
				},
			},
		}
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-0", Type: v1alpha1.DatameshMemberTypeDiskful},
						{Name: "rv-1-1", Type: v1alpha1.DatameshMemberTypeAccess},
					},
				},
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddAccessReplica,
						DatameshRevision: 6, ReplicaName: "rv-1-1", StartedAt: metav1.Now()},
				},
				DatameshReplicaRequests: []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
					{Name: "rv-1-1", Request: v1alpha1.DatameshMembershipRequest{
						Operation: v1alpha1.DatameshMembershipRequestOperationJoin, Type: v1alpha1.ReplicaTypeAccess,
					}},
				},
			},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{failingRVR, mkRVR("rv-1-1", 6)}

		outcome := ensureDatameshAccessReplicas(ctx, rv, rvrs, nil)

		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rv.Status.DatameshTransitions[0].Message).To(ContainSubstring("Errors"))
		Expect(rv.Status.DatameshTransitions[0].Message).To(ContainSubstring("ConfigurationFailed"))
	})

	It("handles missing RVR in progress message", func(ctx SpecContext) {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-0", Type: v1alpha1.DatameshMemberTypeDiskful},
					},
				},
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddAccessReplica,
						DatameshRevision: 6, ReplicaName: "rv-1-1", StartedAt: metav1.Now()},
				},
				DatameshReplicaRequests: []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
					{Name: "rv-1-1", Request: v1alpha1.DatameshMembershipRequest{
						Operation: v1alpha1.DatameshMembershipRequestOperationJoin, Type: v1alpha1.ReplicaTypeAccess,
					}},
				},
			},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-0", 6)}

		outcome := ensureDatameshAccessReplicas(ctx, rv, rvrs, nil)

		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rv.Status.DatameshTransitions[0].Message).To(ContainSubstring("#1 Replica not found"))
	})

	It("completes transition and then processes new join", func(ctx SpecContext) {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration:    &v1alpha1.ReplicatedVolumeConfiguration{VolumeAccess: v1alpha1.VolumeAccessPreferablyLocal},
				DatameshRevision: 6,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-0", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "node-1"},
						{Name: "rv-1-1", Type: v1alpha1.DatameshMemberTypeAccess, NodeName: "node-2"},
					},
				},
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddAccessReplica,
						DatameshRevision: 6, ReplicaName: "rv-1-1", StartedAt: metav1.Now()},
				},
				DatameshReplicaRequests: []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
					{Name: "rv-1-1", Request: v1alpha1.DatameshMembershipRequest{
						Operation: v1alpha1.DatameshMembershipRequestOperationJoin, Type: v1alpha1.ReplicaTypeAccess,
					}},
					{Name: "rv-1-2", Request: v1alpha1.DatameshMembershipRequest{
						Operation: v1alpha1.DatameshMembershipRequestOperationJoin, Type: v1alpha1.ReplicaTypeAccess,
					}},
				},
			},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			{ObjectMeta: metav1.ObjectMeta{Name: "rv-1-0"}, Status: v1alpha1.ReplicatedVolumeReplicaStatus{DatameshRevision: 6}},
			{ObjectMeta: metav1.ObjectMeta{Name: "rv-1-1"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-2", Type: v1alpha1.ReplicaTypeAccess},
				Status: v1alpha1.ReplicatedVolumeReplicaStatus{DatameshRevision: 6,
					Addresses: []v1alpha1.DRBDResourceAddressStatus{{SystemNetworkName: "default"}}}},
			{ObjectMeta: metav1.ObjectMeta{Name: "rv-1-2"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-3", Type: v1alpha1.ReplicaTypeAccess},
				Status: v1alpha1.ReplicatedVolumeReplicaStatus{
					Addresses: []v1alpha1.DRBDResourceAddressStatus{{SystemNetworkName: "default"}}}},
		}

		rsp := &rspView{EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1"}, {NodeName: "node-2"}, {NodeName: "node-3"},
		}}
		outcome := ensureDatameshAccessReplicas(ctx, rv, rvrs, rsp)

		Expect(outcome.DidChange()).To(BeTrue())
		Expect(outcome.Error()).NotTo(HaveOccurred())

		// Old transition completed and removed. New transition for rv-1-2.
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].ReplicaName).To(Equal("rv-1-2"))

		// rv-1-2 added as member.
		Expect(rv.Status.Datamesh.Members).To(HaveLen(3))
		Expect(rv.Status.Datamesh.FindMemberByName("rv-1-2")).NotTo(BeNil())

		// Revision: was 6, completion doesn't increment, join increments to 7.
		Expect(rv.Status.DatameshRevision).To(Equal(int64(7)))
	})

	It("does not create join when RemoveAccessReplica is in progress (loop structure)", func(ctx SpecContext) {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshRevision: 6,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-0", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "node-1"},
					},
				},
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveAccessReplica,
						DatameshRevision: 5, ReplicaName: "rv-1-1", StartedAt: metav1.Now()},
				},
				DatameshReplicaRequests: []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
					{Name: "rv-1-1", Request: v1alpha1.DatameshMembershipRequest{
						Operation: v1alpha1.DatameshMembershipRequestOperationJoin, Type: v1alpha1.ReplicaTypeAccess,
					}},
				},
			},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			{ObjectMeta: metav1.ObjectMeta{Name: "rv-1-0"}, Status: v1alpha1.ReplicatedVolumeReplicaStatus{DatameshRevision: 4}},
			{ObjectMeta: metav1.ObjectMeta{Name: "rv-1-1"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-2", Type: v1alpha1.ReplicaTypeAccess},
				Status: v1alpha1.ReplicatedVolumeReplicaStatus{DatameshRevision: 4,
					Addresses: []v1alpha1.DRBDResourceAddressStatus{{SystemNetworkName: "default"}}}},
		}

		outcome := ensureDatameshAccessReplicas(ctx, rv, rvrs, nil)

		Expect(outcome.DidChange()).To(BeTrue()) // progress message updated
		// RemoveAccessReplica still in progress — not completed.
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveAccessReplica))
		// No AddAccessReplica created — loop structure prevents it.
		Expect(rv.Status.Datamesh.Members).To(HaveLen(1))
	})

	It("processes multiple pendings in one call", func(ctx SpecContext) {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration:    &v1alpha1.ReplicatedVolumeConfiguration{VolumeAccess: v1alpha1.VolumeAccessPreferablyLocal},
				DatameshRevision: 5,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-0", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "node-1"},
					},
				},
				DatameshReplicaRequests: []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
					{Name: "rv-1-1", Request: v1alpha1.DatameshMembershipRequest{
						Operation: v1alpha1.DatameshMembershipRequestOperationJoin, Type: v1alpha1.ReplicaTypeAccess,
					}},
					{Name: "rv-1-2", Request: v1alpha1.DatameshMembershipRequest{
						Operation: v1alpha1.DatameshMembershipRequestOperationJoin, Type: v1alpha1.ReplicaTypeAccess,
					}},
				},
			},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			{ObjectMeta: metav1.ObjectMeta{Name: "rv-1-1"},
				Spec:   v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-2", Type: v1alpha1.ReplicaTypeAccess},
				Status: v1alpha1.ReplicatedVolumeReplicaStatus{Addresses: []v1alpha1.DRBDResourceAddressStatus{{SystemNetworkName: "default"}}}},
			{ObjectMeta: metav1.ObjectMeta{Name: "rv-1-2"},
				Spec:   v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-3", Type: v1alpha1.ReplicaTypeAccess},
				Status: v1alpha1.ReplicatedVolumeReplicaStatus{Addresses: []v1alpha1.DRBDResourceAddressStatus{{SystemNetworkName: "default"}}}},
		}

		rsp := &rspView{EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1"}, {NodeName: "node-2"}, {NodeName: "node-3"},
		}}
		outcome := ensureDatameshAccessReplicas(ctx, rv, rvrs, rsp)

		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rv.Status.Datamesh.Members).To(HaveLen(3))
		Expect(rv.Status.DatameshRevision).To(Equal(int64(7)))
		Expect(rv.Status.DatameshTransitions).To(HaveLen(2))
		// Each has its own revision.
		revs := []int64{rv.Status.DatameshTransitions[0].DatameshRevision, rv.Status.DatameshTransitions[1].DatameshRevision}
		Expect(revs).To(ConsistOf(int64(6), int64(7)))
	})

	It("completes RemoveAccessReplica when subject has revision 0", func(ctx SpecContext) {
		// After RemoveAccessReplica transition is created, the member is already removed from
		// datamesh. The pending leave for the (now non-member) replica is not indexed by the
		// parent function (filtered as non-Access member), so replicaReq is nil when checking progress.
		// The transition still completes based on confirmed revisions.
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-0", Type: v1alpha1.DatameshMemberTypeDiskful},
					},
				},
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveAccessReplica,
						DatameshRevision: 6, ReplicaName: "rv-1-1", StartedAt: metav1.Now()},
				},
			},
		}
		// Diskful confirmed (rev 6), subject reset revision to 0.
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-0", 6), mkRVR("rv-1-1", 0)}

		outcome := ensureDatameshAccessReplicas(ctx, rv, rvrs, nil)

		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})

	It("blocks join when VolumeAccess is Local", func(ctx SpecContext) {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration:    &v1alpha1.ReplicatedVolumeConfiguration{VolumeAccess: v1alpha1.VolumeAccessLocal},
				DatameshRevision: 5,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-0", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "node-1"},
					},
				},
				DatameshReplicaRequests: []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
					{Name: "rv-1-1", Request: v1alpha1.DatameshMembershipRequest{
						Operation: v1alpha1.DatameshMembershipRequestOperationJoin, Type: v1alpha1.ReplicaTypeAccess,
					}},
				},
			},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			{ObjectMeta: metav1.ObjectMeta{Name: "rv-1-1"},
				Spec:   v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-2", Type: v1alpha1.ReplicaTypeAccess},
				Status: v1alpha1.ReplicatedVolumeReplicaStatus{Addresses: []v1alpha1.DRBDResourceAddressStatus{{SystemNetworkName: "default"}}}},
		}

		rsp := &rspView{EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1"}, {NodeName: "node-2"},
		}}
		outcome := ensureDatameshAccessReplicas(ctx, rv, rvrs, rsp)

		Expect(outcome.DidChange()).To(BeTrue()) // message changed
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.Datamesh.Members).To(HaveLen(1))
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(ContainSubstring("volumeAccess is Local"))
	})

	It("includes ShadowDiskful in mustConfirm set for AddAccessReplica transition", func(ctx SpecContext) {
		// ShadowDiskful has ConnectsToAllPeers()=true, so it must confirm transitions.
		// With Diskful(#0) at rev 6 and ShadowDiskful(#2) at rev 5,
		// the transition should NOT complete because ShadowDiskful has not confirmed.
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-0", Type: v1alpha1.DatameshMemberTypeDiskful},
						{Name: "rv-1-1", Type: v1alpha1.DatameshMemberTypeAccess},
						{Name: "rv-1-2", Type: v1alpha1.DatameshMemberTypeShadowDiskful},
					},
				},
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddAccessReplica,
						DatameshRevision: 6, ReplicaName: "rv-1-1", StartedAt: metav1.Now()},
				},
				DatameshReplicaRequests: []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
					{Name: "rv-1-1", Request: v1alpha1.DatameshMembershipRequest{
						Operation: v1alpha1.DatameshMembershipRequestOperationJoin, Type: v1alpha1.ReplicaTypeAccess,
					}},
				},
			},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			{ObjectMeta: metav1.ObjectMeta{Name: "rv-1-0"}, Status: v1alpha1.ReplicatedVolumeReplicaStatus{DatameshRevision: 6}},
			{ObjectMeta: metav1.ObjectMeta{Name: "rv-1-1"}, Status: v1alpha1.ReplicatedVolumeReplicaStatus{DatameshRevision: 6}},
			{ObjectMeta: metav1.ObjectMeta{Name: "rv-1-2"}, Status: v1alpha1.ReplicatedVolumeReplicaStatus{DatameshRevision: 5}}, // not confirmed
		}

		outcome := ensureDatameshAccessReplicas(ctx, rv, rvrs, nil)

		Expect(outcome.DidChange()).To(BeTrue())
		// Transition should NOT be completed because ShadowDiskful(#2) has not confirmed.
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Message).To(ContainSubstring("#2"))
	})
})
