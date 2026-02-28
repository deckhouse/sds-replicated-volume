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

package rvrcontroller

import (
	"context"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

var _ = Describe("computeTargetDatameshRequest", func() {
	// validRV is a minimal ReplicatedVolume that passes all rv/datamesh prerequisites.
	validRV := &v1alpha1.ReplicatedVolume{
		Status: v1alpha1.ReplicatedVolumeStatus{
			DatameshRevision: 1,
			Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
				Topology:           v1alpha1.TopologyIgnored,
				FailuresToTolerate: 1, GuaranteedMinimumDataRedundancy: 1,
				VolumeAccess:              v1alpha1.VolumeAccessPreferablyLocal,
				ReplicatedStoragePoolName: "pool-1",
			},
		},
	}

	It("returns PendingLeave when deleting and is datamesh member", func() {
		now := metav1.Now()
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "rvr-1",
				DeletionTimestamp: &now,
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				NodeName: "node-1",
			},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshRevision: 1, // Is a member.
			},
		}

		target, reason, message := computeTargetDatameshRequest(rvr, nil, nil)

		Expect(target).NotTo(BeNil())
		Expect(target.Operation).To(Equal(v1alpha1.DatameshMembershipRequestOperationLeave))
		Expect(target.Type).To(BeEmpty())
		Expect(reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonPendingLeave))
		Expect(message).To(ContainSubstring("Deletion"))
	})

	It("returns nil reason when deleting and not a datamesh member", func() {
		now := metav1.Now()
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "rvr-1",
				DeletionTimestamp: &now,
			},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshRevision: 0, // Not a member.
			},
		}

		target, reason, _ := computeTargetDatameshRequest(rvr, nil, nil)

		Expect(target).To(BeNil())
		Expect(reason).To(BeEmpty())
	})

	It("returns WaitingForReplicatedVolume when rv is nil", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}

		target, reason, message := computeTargetDatameshRequest(rvr, nil, nil)

		Expect(target).To(BeNil())
		Expect(reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonWaitingForReplicatedVolume))
		Expect(message).To(ContainSubstring("ReplicatedVolume"))
	})

	It("returns WaitingForReplicatedVolume when datamesh is not initialized", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshRevision: 0, // Not initialized.
			},
		}

		target, reason, message := computeTargetDatameshRequest(rvr, rv, nil)

		Expect(target).To(BeNil())
		Expect(reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonWaitingForReplicatedVolume))
		Expect(message).To(ContainSubstring("Datamesh"))
	})

	It("returns WaitingForReplicatedVolume when rv configuration is nil", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshRevision: 1,
				Configuration:    nil, // Not configured.
			},
		}

		target, reason, message := computeTargetDatameshRequest(rvr, rv, nil)

		Expect(target).To(BeNil())
		Expect(reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonWaitingForReplicatedVolume))
		Expect(message).To(ContainSubstring("no configuration yet"))
	})

	It("returns PendingScheduling when NodeName is empty", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				NodeName: "", // Not scheduled.
			},
		}

		target, reason, message := computeTargetDatameshRequest(rvr, validRV, nil)

		Expect(target).To(BeNil())
		Expect(reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonPendingScheduling))
		Expect(message).To(ContainSubstring("node"))
	})

	It("returns PendingScheduling when Diskful has no LVMVolumeGroupName", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				NodeName:           "node-1",
				Type:               v1alpha1.ReplicaTypeDiskful,
				LVMVolumeGroupName: "", // Not assigned.
			},
		}

		target, reason, message := computeTargetDatameshRequest(rvr, validRV, nil)

		Expect(target).To(BeNil())
		Expect(reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonPendingScheduling))
		Expect(message).To(ContainSubstring("storage"))
	})

	It("returns WaitingForReplicatedVolume when rspView is nil", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				NodeName:           "node-1",
				Type:               v1alpha1.ReplicaTypeDiskful,
				LVMVolumeGroupName: "lvg-1",
			},
		}

		target, reason, message := computeTargetDatameshRequest(rvr, validRV, nil)

		Expect(target).To(BeNil())
		Expect(reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonWaitingForReplicatedVolume))
		Expect(message).To(ContainSubstring("ReplicatedStoragePool not found"))
	})

	It("returns NodeNotEligible when node is not in eligible list", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				NodeName:           "node-1",
				Type:               v1alpha1.ReplicaTypeDiskful,
				LVMVolumeGroupName: "lvg-1",
			},
		}
		rspView := &rspEligibilityView{
			EligibleNode: nil, // Node not eligible.
		}

		target, reason, message := computeTargetDatameshRequest(rvr, validRV, rspView)

		Expect(target).To(BeNil())
		Expect(reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonNodeNotEligible))
		Expect(message).To(ContainSubstring("node-1"))
	})

	It("returns StorageNotEligible when LVG is not eligible", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				NodeName:           "node-1",
				Type:               v1alpha1.ReplicaTypeDiskful,
				LVMVolumeGroupName: "lvg-missing",
			},
		}
		rspView := &rspEligibilityView{
			EligibleNode: &v1alpha1.ReplicatedStoragePoolEligibleNode{
				NodeName: "node-1",
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
					{Name: "lvg-1"}, // Different LVG.
				},
			},
		}

		target, reason, _ := computeTargetDatameshRequest(rvr, validRV, rspView)

		Expect(target).To(BeNil())
		Expect(reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonStorageNotEligible))
	})

	It("returns PendingJoin for Diskful non-member", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				NodeName:                   "node-1",
				Type:                       v1alpha1.ReplicaTypeDiskful,
				LVMVolumeGroupName:         "lvg-1",
				LVMVolumeGroupThinPoolName: "tp-1",
			},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshRevision: 0, // Not a member.
			},
		}
		rspView := &rspEligibilityView{
			EligibleNode: &v1alpha1.ReplicatedStoragePoolEligibleNode{
				NodeName: "node-1",
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
					{Name: "lvg-1", ThinPoolName: "tp-1"},
				},
			},
		}

		target, reason, message := computeTargetDatameshRequest(rvr, validRV, rspView)

		Expect(target).NotTo(BeNil())
		Expect(target.Operation).To(Equal(v1alpha1.DatameshMembershipRequestOperationJoin))
		Expect(target.Type).To(Equal(v1alpha1.ReplicaTypeDiskful))
		Expect(target.LVMVolumeGroupName).To(Equal("lvg-1"))
		Expect(target.ThinPoolName).To(Equal("tp-1"))
		Expect(reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonPendingJoin))
		Expect(message).To(ContainSubstring("Diskful"))
	})

	It("returns PendingJoin for Access non-member", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				NodeName: "node-1",
				Type:     v1alpha1.ReplicaTypeAccess,
			},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshRevision: 0, // Not a member.
			},
		}
		rspView := &rspEligibilityView{
			EligibleNode: &v1alpha1.ReplicatedStoragePoolEligibleNode{
				NodeName: "node-1",
			},
		}

		target, reason, _ := computeTargetDatameshRequest(rvr, validRV, rspView)

		Expect(target).NotTo(BeNil())
		Expect(target.Operation).To(Equal(v1alpha1.DatameshMembershipRequestOperationJoin))
		Expect(target.Type).To(Equal(v1alpha1.ReplicaTypeAccess))
		Expect(target.LVMVolumeGroupName).To(BeEmpty())
		Expect(target.ThinPoolName).To(BeEmpty())
		Expect(reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonPendingJoin))
	})

	It("returns PendingRoleChange when type not in sync", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				NodeName:           "node-1",
				Type:               v1alpha1.ReplicaTypeDiskful, // Want Diskful.
				LVMVolumeGroupName: "lvg-1",
			},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshRevision: 1,                                 // Is a member.
				Type:             v1alpha1.DRBDResourceTypeDiskless, // Currently Diskless.
			},
		}
		rspView := &rspEligibilityView{
			EligibleNode: &v1alpha1.ReplicatedStoragePoolEligibleNode{
				NodeName: "node-1",
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
					{Name: "lvg-1"},
				},
			},
		}

		target, reason, message := computeTargetDatameshRequest(rvr, validRV, rspView)

		Expect(target).NotTo(BeNil())
		Expect(target.Operation).To(Equal(v1alpha1.DatameshMembershipRequestOperationChangeRole))
		Expect(target.Type).To(Equal(v1alpha1.ReplicaTypeDiskful))
		Expect(target.LVMVolumeGroupName).To(Equal("lvg-1"))
		Expect(reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonPendingRoleChange))
		Expect(message).To(ContainSubstring("role"))
	})

	It("returns PendingBackingVolumeChange when BV not in sync", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				NodeName:                   "node-1",
				Type:                       v1alpha1.ReplicaTypeDiskful,
				LVMVolumeGroupName:         "lvg-new", // Want new LVG.
				LVMVolumeGroupThinPoolName: "tp-new",
			},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshRevision: 1,
				Type:             v1alpha1.DRBDResourceTypeDiskful, // Type matches.
				BackingVolume: &v1alpha1.ReplicatedVolumeReplicaStatusBackingVolume{
					LVMVolumeGroupName:         "lvg-old", // Old LVG.
					LVMVolumeGroupThinPoolName: "tp-old",
				},
			},
		}
		rspView := &rspEligibilityView{
			EligibleNode: &v1alpha1.ReplicatedStoragePoolEligibleNode{
				NodeName: "node-1",
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
					{Name: "lvg-new", ThinPoolName: "tp-new"},
				},
			},
		}

		target, reason, message := computeTargetDatameshRequest(rvr, validRV, rspView)

		Expect(target).NotTo(BeNil())
		Expect(target.Operation).To(Equal(v1alpha1.DatameshMembershipRequestOperationChangeBackingVolume))
		Expect(target.Type).To(BeEmpty())
		Expect(target.LVMVolumeGroupName).To(Equal("lvg-new"))
		Expect(target.ThinPoolName).To(Equal("tp-new"))
		Expect(reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonPendingBackingVolumeChange))
		Expect(message).To(ContainSubstring("backing volume"))
	})

	It("returns Configured when all in sync", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				NodeName:                   "node-1",
				Type:                       v1alpha1.ReplicaTypeDiskful,
				LVMVolumeGroupName:         "lvg-1",
				LVMVolumeGroupThinPoolName: "tp-1",
			},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshRevision: 1,
				Type:             v1alpha1.DRBDResourceTypeDiskful, // Type matches.
				BackingVolume: &v1alpha1.ReplicatedVolumeReplicaStatusBackingVolume{
					LVMVolumeGroupName:         "lvg-1", // LVG matches.
					LVMVolumeGroupThinPoolName: "tp-1",  // TP matches.
				},
			},
		}
		rspView := &rspEligibilityView{
			EligibleNode: &v1alpha1.ReplicatedStoragePoolEligibleNode{
				NodeName: "node-1",
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
					{Name: "lvg-1", ThinPoolName: "tp-1"},
				},
			},
		}

		target, reason, message := computeTargetDatameshRequest(rvr, validRV, rspView)

		Expect(target).To(BeNil())
		Expect(reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonConfigured))
		Expect(message).To(ContainSubstring("configured"))
	})

	It("returns Configured for Access member with matching type", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				NodeName: "node-1",
				Type:     v1alpha1.ReplicaTypeAccess,
			},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshRevision: 1,
				Type:             v1alpha1.DRBDResourceTypeDiskless, // Access → Diskless.
			},
		}
		rspView := &rspEligibilityView{
			EligibleNode: &v1alpha1.ReplicatedStoragePoolEligibleNode{
				NodeName: "node-1",
			},
		}

		target, reason, _ := computeTargetDatameshRequest(rvr, validRV, rspView)

		Expect(target).To(BeNil())
		Expect(reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonConfigured))
	})
})

var _ = Describe("applyDatameshRequest", func() {
	It("clears datameshPending when target is nil and current exists", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshRequest: &v1alpha1.DatameshMembershipRequest{
					Type: v1alpha1.ReplicaTypeDiskful,
				},
			},
		}

		changed := applyDatameshRequest(rvr, nil)

		Expect(changed).To(BeTrue())
		Expect(rvr.Status.DatameshRequest).To(BeNil())
	})

	It("returns false when both target and current are nil", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshRequest: nil,
			},
		}

		changed := applyDatameshRequest(rvr, nil)

		Expect(changed).To(BeFalse())
		Expect(rvr.Status.DatameshRequest).To(BeNil())
	})

	It("creates datameshPending when target exists and current is nil", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshRequest: nil,
			},
		}
		target := &v1alpha1.DatameshMembershipRequest{
			Operation: v1alpha1.DatameshMembershipRequestOperationJoin,
			Type:      v1alpha1.ReplicaTypeDiskful,
		}

		changed := applyDatameshRequest(rvr, target)

		Expect(changed).To(BeTrue())
		Expect(rvr.Status.DatameshRequest).NotTo(BeNil())
		Expect(rvr.Status.DatameshRequest.Operation).To(Equal(v1alpha1.DatameshMembershipRequestOperationJoin))
		Expect(rvr.Status.DatameshRequest.Type).To(Equal(v1alpha1.ReplicaTypeDiskful))
	})

	It("returns false when current matches target (idempotent)", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshRequest: &v1alpha1.DatameshMembershipRequest{
					Operation:          v1alpha1.DatameshMembershipRequestOperationJoin,
					Type:               v1alpha1.ReplicaTypeDiskful,
					LVMVolumeGroupName: "lvg-1",
					ThinPoolName:       "tp-1",
				},
			},
		}
		target := &v1alpha1.DatameshMembershipRequest{
			Operation:          v1alpha1.DatameshMembershipRequestOperationJoin,
			Type:               v1alpha1.ReplicaTypeDiskful,
			LVMVolumeGroupName: "lvg-1",
			ThinPoolName:       "tp-1",
		}

		changed := applyDatameshRequest(rvr, target)

		Expect(changed).To(BeFalse())
	})

	It("updates Member field when it differs", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshRequest: &v1alpha1.DatameshMembershipRequest{
					Operation: v1alpha1.DatameshMembershipRequestOperationJoin,
				},
			},
		}
		target := &v1alpha1.DatameshMembershipRequest{
			Operation: v1alpha1.DatameshMembershipRequestOperationLeave,
		}

		changed := applyDatameshRequest(rvr, target)

		Expect(changed).To(BeTrue())
		Expect(rvr.Status.DatameshRequest.Operation).To(Equal(v1alpha1.DatameshMembershipRequestOperationLeave))
	})

	It("updates Type field when it differs", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshRequest: &v1alpha1.DatameshMembershipRequest{
					Type: v1alpha1.ReplicaTypeAccess,
				},
			},
		}
		target := &v1alpha1.DatameshMembershipRequest{
			Type: v1alpha1.ReplicaTypeDiskful,
		}

		changed := applyDatameshRequest(rvr, target)

		Expect(changed).To(BeTrue())
		Expect(rvr.Status.DatameshRequest.Type).To(Equal(v1alpha1.ReplicaTypeDiskful))
	})

	It("updates LVMVolumeGroupName field when it differs", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshRequest: &v1alpha1.DatameshMembershipRequest{
					LVMVolumeGroupName: "lvg-old",
				},
			},
		}
		target := &v1alpha1.DatameshMembershipRequest{
			LVMVolumeGroupName: "lvg-new",
		}

		changed := applyDatameshRequest(rvr, target)

		Expect(changed).To(BeTrue())
		Expect(rvr.Status.DatameshRequest.LVMVolumeGroupName).To(Equal("lvg-new"))
	})

	It("updates ThinPoolName field when it differs", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshRequest: &v1alpha1.DatameshMembershipRequest{
					ThinPoolName: "tp-old",
				},
			},
		}
		target := &v1alpha1.DatameshMembershipRequest{
			ThinPoolName: "tp-new",
		}

		changed := applyDatameshRequest(rvr, target)

		Expect(changed).To(BeTrue())
		Expect(rvr.Status.DatameshRequest.ThinPoolName).To(Equal("tp-new"))
	})
})

var _ = Describe("applyConfiguredCondAbsent", func() {
	It("removes Configured condition when it exists", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}
		// Pre-set condition.
		obju.SetStatusCondition(rvr, metav1.Condition{
			Type:    v1alpha1.ReplicatedVolumeReplicaCondConfiguredType,
			Status:  metav1.ConditionTrue,
			Reason:  v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonConfigured,
			Message: "Test",
		})

		changed := applyConfiguredCondAbsent(rvr)

		Expect(changed).To(BeTrue())
		Expect(obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondConfiguredType)).To(BeNil())
	})

	It("returns false when condition already absent (idempotent)", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}

		changed := applyConfiguredCondAbsent(rvr)

		Expect(changed).To(BeFalse())
	})
})

var _ = Describe("applyConfiguredCondFalse", func() {
	It("sets Configured condition to False", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}

		changed := applyConfiguredCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonPendingJoin,
			"Waiting to join")

		Expect(changed).To(BeTrue())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondConfiguredType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonPendingJoin))
		Expect(cond.Message).To(Equal("Waiting to join"))
	})

	It("returns false when condition already matches (idempotent)", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}
		obju.SetStatusCondition(rvr, metav1.Condition{
			Type:    v1alpha1.ReplicatedVolumeReplicaCondConfiguredType,
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonPendingJoin,
			Message: "Waiting to join",
		})

		changed := applyConfiguredCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonPendingJoin,
			"Waiting to join")

		Expect(changed).To(BeFalse())
	})
})

var _ = Describe("applyConfiguredCondTrue", func() {
	It("sets Configured condition to True", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}

		changed := applyConfiguredCondTrue(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonConfigured,
			"Replica is configured")

		Expect(changed).To(BeTrue())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondConfiguredType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonConfigured))
		Expect(cond.Message).To(Equal("Replica is configured"))
	})

	It("returns false when condition already matches (idempotent)", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}
		obju.SetStatusCondition(rvr, metav1.Condition{
			Type:    v1alpha1.ReplicatedVolumeReplicaCondConfiguredType,
			Status:  metav1.ConditionTrue,
			Reason:  v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonConfigured,
			Message: "Replica is configured",
		})

		changed := applyConfiguredCondTrue(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonConfigured,
			"Replica is configured")

		Expect(changed).To(BeFalse())
	})
})

var _ = Describe("applyConfiguredCondUnknown", func() {
	It("sets condition to Unknown", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}

		changed := applyConfiguredCondUnknown(rvr, "TestReason", "Test message")

		Expect(changed).To(BeTrue())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondConfiguredType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
		Expect(cond.Reason).To(Equal("TestReason"))
		Expect(cond.Message).To(Equal("Test message"))
	})

	It("returns false when condition already matches (idempotent)", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}
		applyConfiguredCondUnknown(rvr, "TestReason", "Test message")

		changed := applyConfiguredCondUnknown(rvr, "TestReason", "Test message")

		Expect(changed).To(BeFalse())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Construction functions tests
//

var _ = Describe("ensureStatusDatameshRequestAndConfiguredCond", func() {
	var ctx context.Context

	// validRV is a minimal ReplicatedVolume that passes all rv/datamesh prerequisites.
	validRV := &v1alpha1.ReplicatedVolume{
		Status: v1alpha1.ReplicatedVolumeStatus{
			DatameshRevision: 1,
			Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
				Topology:           v1alpha1.TopologyIgnored,
				FailuresToTolerate: 1, GuaranteedMinimumDataRedundancy: 1,
				VolumeAccess:              v1alpha1.VolumeAccessPreferablyLocal,
				ReplicatedStoragePoolName: "pool-1",
			},
		},
	}

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("sets PendingJoin for non-member replica", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				NodeName:           "node-1",
				Type:               v1alpha1.ReplicaTypeDiskful,
				LVMVolumeGroupName: "lvg-1",
			},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshRevision: 0, // Not a member.
			},
		}
		rspView := &rspEligibilityView{
			EligibleNode: &v1alpha1.ReplicatedStoragePoolEligibleNode{
				NodeName: "node-1",
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
					{Name: "lvg-1"},
				},
			},
		}

		outcome := ensureStatusDatameshRequestAndConfiguredCond(ctx, rvr, validRV, rspView)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeTrue())

		// Check datameshPending.
		Expect(rvr.Status.DatameshRequest).NotTo(BeNil())
		Expect(rvr.Status.DatameshRequest.Operation).To(Equal(v1alpha1.DatameshMembershipRequestOperationJoin))
		Expect(rvr.Status.DatameshRequest.Type).To(Equal(v1alpha1.ReplicaTypeDiskful))

		// Check Configured condition.
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondConfiguredType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonPendingJoin))
	})

	It("sets PendingLeave for deleting member", func() {
		now := metav1.Now()
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "rvr-1",
				DeletionTimestamp: &now,
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				NodeName: "node-1",
			},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshRevision: 1, // Is a member.
			},
		}

		outcome := ensureStatusDatameshRequestAndConfiguredCond(ctx, rvr, nil, nil)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeTrue())

		// Check datameshPending.
		Expect(rvr.Status.DatameshRequest).NotTo(BeNil())
		Expect(rvr.Status.DatameshRequest.Operation).To(Equal(v1alpha1.DatameshMembershipRequestOperationLeave))

		// Check Configured condition.
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondConfiguredType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonPendingLeave))
	})

	It("sets Configured=True and clears datameshPending when all in sync", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				NodeName:           "node-1",
				Type:               v1alpha1.ReplicaTypeDiskful,
				LVMVolumeGroupName: "lvg-1",
			},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshRevision: 1,
				Type:             v1alpha1.DRBDResourceTypeDiskful, // Type matches.
				BackingVolume: &v1alpha1.ReplicatedVolumeReplicaStatusBackingVolume{
					LVMVolumeGroupName: "lvg-1", // BV matches.
				},
				DatameshRequest: &v1alpha1.DatameshMembershipRequest{
					Type: v1alpha1.ReplicaTypeDiskful, // Stale pending.
				},
			},
		}
		rspView := &rspEligibilityView{
			EligibleNode: &v1alpha1.ReplicatedStoragePoolEligibleNode{
				NodeName: "node-1",
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
					{Name: "lvg-1"},
				},
			},
		}

		outcome := ensureStatusDatameshRequestAndConfiguredCond(ctx, rvr, validRV, rspView)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeTrue())

		// Check datameshPending is cleared.
		Expect(rvr.Status.DatameshRequest).To(BeNil())

		// Check Configured condition.
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondConfiguredType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonConfigured))
	})

	It("sets PendingScheduling when node not assigned", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				NodeName: "", // Not scheduled.
			},
		}

		outcome := ensureStatusDatameshRequestAndConfiguredCond(ctx, rvr, validRV, nil)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeTrue())

		// Check datameshPending is nil.
		Expect(rvr.Status.DatameshRequest).To(BeNil())

		// Check Configured condition.
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondConfiguredType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonPendingScheduling))
	})

	It("returns changed=false when already in desired state (idempotent)", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				NodeName:           "node-1",
				Type:               v1alpha1.ReplicaTypeDiskful,
				LVMVolumeGroupName: "lvg-1",
			},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshRevision: 1,
				Type:             v1alpha1.DRBDResourceTypeDiskful,
				BackingVolume: &v1alpha1.ReplicatedVolumeReplicaStatusBackingVolume{
					LVMVolumeGroupName: "lvg-1",
				},
				DatameshRequest: nil, // Already cleared.
			},
		}
		// Pre-set condition to match expected.
		obju.SetStatusCondition(rvr, metav1.Condition{
			Type:    v1alpha1.ReplicatedVolumeReplicaCondConfiguredType,
			Status:  metav1.ConditionTrue,
			Reason:  v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonConfigured,
			Message: "Replica is configured as intended",
		})
		rspView := &rspEligibilityView{
			EligibleNode: &v1alpha1.ReplicatedStoragePoolEligibleNode{
				NodeName: "node-1",
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
					{Name: "lvg-1"},
				},
			},
		}

		outcome := ensureStatusDatameshRequestAndConfiguredCond(ctx, rvr, validRV, rspView)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeFalse())
	})

	It("appends RV datamesh replica request message to condition", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				NodeName:           "node-1",
				Type:               v1alpha1.ReplicaTypeDiskful,
				LVMVolumeGroupName: "lvg-1",
			},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshRevision: 0, // Not a member.
			},
		}
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshRevision: 1,
				Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
					Topology:           v1alpha1.TopologyIgnored,
					FailuresToTolerate: 1, GuaranteedMinimumDataRedundancy: 1,
					VolumeAccess:              v1alpha1.VolumeAccessPreferablyLocal,
					ReplicatedStoragePoolName: "pool-1",
				},
				DatameshReplicaRequests: []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
					{
						Name:    "rvr-1",
						Message: "Waiting for DRBD resource creation",
					},
					{
						Name:    "rvr-other",
						Message: "Other replica message",
					},
				},
			},
		}
		rspView := &rspEligibilityView{
			EligibleNode: &v1alpha1.ReplicatedStoragePoolEligibleNode{
				NodeName: "node-1",
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
					{Name: "lvg-1"},
				},
			},
		}

		outcome := ensureStatusDatameshRequestAndConfiguredCond(ctx, rvr, rv, rspView)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeTrue())

		// Check Configured condition message includes RV message.
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondConfiguredType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Message).To(ContainSubstring(": Waiting for DRBD resource creation"))
		Expect(cond.Message).NotTo(ContainSubstring("Other replica message"))
	})

	It("does not append RV message when no datamesh request", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				NodeName:           "node-1",
				Type:               v1alpha1.ReplicaTypeDiskful,
				LVMVolumeGroupName: "lvg-1",
			},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshRevision: 1,
				Type:             v1alpha1.DRBDResourceTypeDiskful,
				BackingVolume: &v1alpha1.ReplicatedVolumeReplicaStatusBackingVolume{
					LVMVolumeGroupName: "lvg-1",
				},
			},
		}
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshRevision: 1,
				Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
					Topology:           v1alpha1.TopologyIgnored,
					FailuresToTolerate: 1, GuaranteedMinimumDataRedundancy: 1,
					VolumeAccess:              v1alpha1.VolumeAccessPreferablyLocal,
					ReplicatedStoragePoolName: "pool-1",
				},
				DatameshReplicaRequests: []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
					{
						Name:    "rvr-1",
						Message: "Should not appear",
					},
				},
			},
		}
		rspView := &rspEligibilityView{
			EligibleNode: &v1alpha1.ReplicatedStoragePoolEligibleNode{
				NodeName: "node-1",
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
					{Name: "lvg-1"},
				},
			},
		}

		outcome := ensureStatusDatameshRequestAndConfiguredCond(ctx, rvr, rv, rspView)

		Expect(outcome.Error()).NotTo(HaveOccurred())

		// Check Configured condition message does NOT include RV message (no pending transition).
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondConfiguredType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Message).NotTo(ContainSubstring("Should not appear"))
	})
})

var _ = Describe("ensureStatusAttachment", func() {
	var (
		ctx context.Context
		rvr *v1alpha1.ReplicatedVolumeReplica
	)

	BeforeEach(func() {
		ctx = logr.NewContext(context.Background(), logr.Discard())
		rvr = &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}
	})

	It("clears attachment when drbdr is nil", func() {
		rvr.Status.Attachment = &v1alpha1.ReplicatedVolumeReplicaStatusAttachment{
			DevicePath:  "/dev/drbd1000",
			IOSuspended: false,
		}

		outcome := ensureStatusAttachment(ctx, rvr, nil, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rvr.Status.Attachment).To(BeNil())
	})

	It("clears attachment when not actual attached", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				ActiveConfiguration: &v1alpha1.DRBDResourceActiveConfiguration{
					Role: v1alpha1.DRBDRoleSecondary,
				},
			},
		}
		rvr.Status.Attachment = &v1alpha1.ReplicatedVolumeReplicaStatusAttachment{
			DevicePath:  "/dev/drbd1000",
			IOSuspended: false,
		}

		outcome := ensureStatusAttachment(ctx, rvr, drbdr, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rvr.Status.Attachment).To(BeNil())
	})

	It("keeps attachment unchanged when agent not ready", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Device:            "/dev/drbd1000",
				DeviceIOSuspended: boolPtr(false),
				ActiveConfiguration: &v1alpha1.DRBDResourceActiveConfiguration{
					Role: v1alpha1.DRBDRolePrimary,
				},
			},
		}
		rvr.Status.Attachment = &v1alpha1.ReplicatedVolumeReplicaStatusAttachment{
			DevicePath:  "/dev/drbd999",
			IOSuspended: true,
		}

		outcome := ensureStatusAttachment(ctx, rvr, drbdr, false, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeFalse())
		Expect(rvr.Status.Attachment.DevicePath).To(Equal("/dev/drbd999"))
		Expect(rvr.Status.Attachment.IOSuspended).To(BeTrue())
	})

	It("keeps attachment unchanged when configuration pending", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Device:            "/dev/drbd1000",
				DeviceIOSuspended: boolPtr(false),
				ActiveConfiguration: &v1alpha1.DRBDResourceActiveConfiguration{
					Role: v1alpha1.DRBDRolePrimary,
				},
			},
		}
		rvr.Status.Attachment = &v1alpha1.ReplicatedVolumeReplicaStatusAttachment{
			DevicePath:  "/dev/drbd999",
			IOSuspended: true,
		}

		outcome := ensureStatusAttachment(ctx, rvr, drbdr, true, true)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeFalse())
		Expect(rvr.Status.Attachment.DevicePath).To(Equal("/dev/drbd999"))
	})

	It("sets attachment when actual attached", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Device:            "/dev/drbd1000",
				DeviceIOSuspended: boolPtr(false),
				ActiveConfiguration: &v1alpha1.DRBDResourceActiveConfiguration{
					Role: v1alpha1.DRBDRolePrimary,
				},
			},
		}

		outcome := ensureStatusAttachment(ctx, rvr, drbdr, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rvr.Status.Attachment).NotTo(BeNil())
		Expect(rvr.Status.Attachment.DevicePath).To(Equal("/dev/drbd1000"))
		Expect(rvr.Status.Attachment.IOSuspended).To(BeFalse())
	})

	It("sets IOSuspended=true when I/O is suspended", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Device:            "/dev/drbd1000",
				DeviceIOSuspended: boolPtr(true),
				ActiveConfiguration: &v1alpha1.DRBDResourceActiveConfiguration{
					Role: v1alpha1.DRBDRolePrimary,
				},
			},
		}

		outcome := ensureStatusAttachment(ctx, rvr, drbdr, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(rvr.Status.Attachment).NotTo(BeNil())
		Expect(rvr.Status.Attachment.DevicePath).To(Equal("/dev/drbd1000"))
		Expect(rvr.Status.Attachment.IOSuspended).To(BeTrue())
	})

	It("is idempotent", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Device:            "/dev/drbd1000",
				DeviceIOSuspended: boolPtr(false),
				ActiveConfiguration: &v1alpha1.DRBDResourceActiveConfiguration{
					Role: v1alpha1.DRBDRolePrimary,
				},
			},
		}

		outcome1 := ensureStatusAttachment(ctx, rvr, drbdr, true, false)
		Expect(outcome1.Error()).NotTo(HaveOccurred())
		Expect(outcome1.DidChange()).To(BeTrue())

		outcome2 := ensureStatusAttachment(ctx, rvr, drbdr, true, false)
		Expect(outcome2.Error()).NotTo(HaveOccurred())
		Expect(outcome2.DidChange()).To(BeFalse())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// ensureStatusAddressesAndType tests
//

var _ = Describe("ensureStatusAddressesAndType", func() {
	var (
		ctx context.Context
		rvr *v1alpha1.ReplicatedVolumeReplica
	)

	BeforeEach(func() {
		ctx = logr.NewContext(context.Background(), logr.Discard())
		rvr = &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}
	})

	It("clears addresses and type when drbdr is nil", func() {
		rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			{SystemNetworkName: "net-1", Address: v1alpha1.DRBDAddress{IPv4: "10.0.0.1", Port: 7000}},
		}
		rvr.Status.Type = v1alpha1.DRBDResourceTypeDiskful

		outcome := ensureStatusAddressesAndType(ctx, rvr, nil)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rvr.Status.Addresses).To(BeNil())
		Expect(rvr.Status.Type).To(BeEmpty())
	})

	It("returns no change when drbdr is nil and addresses/type already empty", func() {
		outcome := ensureStatusAddressesAndType(ctx, rvr, nil)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeFalse())
	})

	It("copies addresses from drbdr", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Addresses: []v1alpha1.DRBDResourceAddressStatus{
					{SystemNetworkName: "net-1", Address: v1alpha1.DRBDAddress{IPv4: "10.0.0.1", Port: 7000}},
					{SystemNetworkName: "net-2", Address: v1alpha1.DRBDAddress{IPv4: "10.0.0.2", Port: 7001}},
				},
			},
		}

		outcome := ensureStatusAddressesAndType(ctx, rvr, drbdr)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rvr.Status.Addresses).To(HaveLen(2))
		Expect(rvr.Status.Addresses[0].SystemNetworkName).To(Equal("net-1"))
		Expect(rvr.Status.Addresses[1].SystemNetworkName).To(Equal("net-2"))
	})

	It("sets type from ActiveConfiguration", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				ActiveConfiguration: &v1alpha1.DRBDResourceActiveConfiguration{
					Type: v1alpha1.DRBDResourceTypeDiskful,
				},
			},
		}

		outcome := ensureStatusAddressesAndType(ctx, rvr, drbdr)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rvr.Status.Type).To(Equal(v1alpha1.DRBDResourceTypeDiskful))
	})

	It("sets type to empty when ActiveConfiguration is nil", func() {
		rvr.Status.Type = v1alpha1.DRBDResourceTypeDiskful
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				ActiveConfiguration: nil,
			},
		}

		outcome := ensureStatusAddressesAndType(ctx, rvr, drbdr)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rvr.Status.Type).To(BeEmpty())
	})

	It("is idempotent", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Addresses: []v1alpha1.DRBDResourceAddressStatus{
					{SystemNetworkName: "net-1", Address: v1alpha1.DRBDAddress{IPv4: "10.0.0.1", Port: 7000}},
				},
				ActiveConfiguration: &v1alpha1.DRBDResourceActiveConfiguration{
					Type: v1alpha1.DRBDResourceTypeDiskful,
				},
			},
		}

		outcome1 := ensureStatusAddressesAndType(ctx, rvr, drbdr)
		Expect(outcome1.Error()).NotTo(HaveOccurred())
		Expect(outcome1.DidChange()).To(BeTrue())

		outcome2 := ensureStatusAddressesAndType(ctx, rvr, drbdr)
		Expect(outcome2.Error()).NotTo(HaveOccurred())
		Expect(outcome2.DidChange()).To(BeFalse())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// ensureStatusPeers + ensureConditionFullyConnected tests
//

var _ = Describe("ensureStatusPeers + ensureConditionFullyConnected", func() {
	var (
		ctx context.Context
		rvr *v1alpha1.ReplicatedVolumeReplica
	)

	BeforeEach(func() {
		ctx = logr.NewContext(context.Background(), logr.Discard())
		rvr = &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "peer",
			},
		}
	})

	It("removes condition and clears peers when drbdr is nil", func() {
		// Set up existing condition and peers
		obju.SetStatusCondition(rvr, metav1.Condition{
			Type:   v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedType,
			Status: metav1.ConditionTrue,
			Reason: "PreviousReason",
		})
		rvr.Status.Peers = []v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{{Name: "peer-1"}}

		outcome := flow.MergeEnsures(
			ensureStatusPeers(ctx, rvr, nil),
			ensureConditionFullyConnected(ctx, rvr, nil, nil, true),
		)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeTrue())
		Expect(obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedType)).To(BeNil())
		Expect(rvr.Status.Peers).To(BeEmpty())
	})

	It("removes condition when not a datamesh member and no drbdr peers", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Peers: []v1alpha1.DRBDResourcePeerStatus{}, // no peers
			},
		}
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{
			Members: []v1alpha1.DatameshMember{
				{Name: "other-rvr"}, // rvr-1 is not a member
			},
		}

		outcome := flow.MergeEnsures(
			ensureStatusPeers(ctx, rvr, drbdr),
			ensureConditionFullyConnected(ctx, rvr, drbdr, datamesh, true),
		)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedType)).To(BeNil())
	})

	It("sets Unknown when agent not ready", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Peers: []v1alpha1.DRBDResourcePeerStatus{
					{Name: "peer-1", Type: v1alpha1.DRBDResourceTypeDiskful, ConnectionState: v1alpha1.ConnectionStateConnected},
				},
			},
		}
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{
			Members: []v1alpha1.DatameshMember{
				{Name: "peer-1"},
				{Name: "rvr-1"}, // self is member
			},
		}
		rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			{SystemNetworkName: "net-1"},
		}

		outcome := flow.MergeEnsures(
			ensureStatusPeers(ctx, rvr, drbdr),
			ensureConditionFullyConnected(ctx, rvr, drbdr, datamesh, false), // agent not ready
		)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedReasonAgentNotReady))
		// ensureStatusPeers now populates peers even when agent is not ready
		Expect(rvr.Status.Peers).To(HaveLen(1))
	})

	It("sets NoPeersExpected (True) when sole datamesh member has no peers", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Peers: []v1alpha1.DRBDResourcePeerStatus{},
			},
		}
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{
			Members: []v1alpha1.DatameshMember{
				{Name: "rvr-1"}, // only self, no peers
			},
		}
		rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			{SystemNetworkName: "net-1"},
		}

		outcome := flow.MergeEnsures(
			ensureStatusPeers(ctx, rvr, drbdr),
			ensureConditionFullyConnected(ctx, rvr, drbdr, datamesh, true),
		)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedReasonSoleMember))
	})

	It("sets NoPeers (False) when multi-member datamesh has no peers yet", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Peers: []v1alpha1.DRBDResourcePeerStatus{},
			},
		}
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{
			Members: []v1alpha1.DatameshMember{
				{Name: "rvr-1"},
				{Name: "rvr-2"},
			},
		}
		rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			{SystemNetworkName: "net-1"},
		}

		outcome := flow.MergeEnsures(
			ensureStatusPeers(ctx, rvr, drbdr),
			ensureConditionFullyConnected(ctx, rvr, drbdr, datamesh, true),
		)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedReasonNoPeers))
	})

	It("sets NotConnected when all peers not connected", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Peers: []v1alpha1.DRBDResourcePeerStatus{
					{Name: "peer-1", Type: v1alpha1.DRBDResourceTypeDiskful, ConnectionState: v1alpha1.ConnectionStateConnecting},
					{Name: "peer-2", Type: v1alpha1.DRBDResourceTypeDiskful, ConnectionState: ""},
				},
			},
		}
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{
			Members: []v1alpha1.DatameshMember{
				{Name: "peer-1", Type: v1alpha1.DatameshMemberTypeDiskful},
				{Name: "peer-2", Type: v1alpha1.DatameshMemberTypeDiskful},
				{Name: "rvr-1"},
			},
			SystemNetworkNames: []string{"net-1"},
		}
		rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			{SystemNetworkName: "net-1"},
		}

		outcome := flow.MergeEnsures(
			ensureStatusPeers(ctx, rvr, drbdr),
			ensureConditionFullyConnected(ctx, rvr, drbdr, datamesh, true),
		)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedReasonNotConnected))
	})

	It("sets FullyConnected when all peers fully connected on all paths", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Peers: []v1alpha1.DRBDResourcePeerStatus{
					{
						Name:            "peer-1",
						Type:            v1alpha1.DRBDResourceTypeDiskful,
						ConnectionState: v1alpha1.ConnectionStateConnected,
						Paths: []v1alpha1.DRBDResourcePathStatus{
							{SystemNetworkName: "net-1", Established: true},
							{SystemNetworkName: "net-2", Established: true},
						},
					},
				},
			},
		}
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{
			Members: []v1alpha1.DatameshMember{
				{Name: "peer-1", Type: v1alpha1.DatameshMemberTypeDiskful},
				{Name: "rvr-1"},
			},
			SystemNetworkNames: []string{"net-1", "net-2"},
		}
		rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			{SystemNetworkName: "net-1"},
			{SystemNetworkName: "net-2"},
		}

		outcome := flow.MergeEnsures(
			ensureStatusPeers(ctx, rvr, drbdr),
			ensureConditionFullyConnected(ctx, rvr, drbdr, datamesh, true),
		)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedReasonFullyConnected))
	})

	It("sets ConnectedToAllPeers when connected but not all paths established", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Peers: []v1alpha1.DRBDResourcePeerStatus{
					{
						Name:            "peer-1",
						Type:            v1alpha1.DRBDResourceTypeDiskful,
						ConnectionState: v1alpha1.ConnectionStateConnected,
						Paths: []v1alpha1.DRBDResourcePathStatus{
							{SystemNetworkName: "net-1", Established: true},
							{SystemNetworkName: "net-2", Established: false}, // not all established
						},
					},
				},
			},
		}
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{
			Members: []v1alpha1.DatameshMember{
				{Name: "peer-1", Type: v1alpha1.DatameshMemberTypeDiskful},
				{Name: "rvr-1"},
			},
			SystemNetworkNames: []string{"net-1", "net-2"},
		}
		rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			{SystemNetworkName: "net-1"},
			{SystemNetworkName: "net-2"},
		}

		outcome := flow.MergeEnsures(
			ensureStatusPeers(ctx, rvr, drbdr),
			ensureConditionFullyConnected(ctx, rvr, drbdr, datamesh, true),
		)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedReasonConnectedToAllPeers))
	})

	It("sets PartiallyConnected with detailed message for mixed state", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Peers: []v1alpha1.DRBDResourcePeerStatus{
					{
						Name:            "peer-1",
						Type:            v1alpha1.DRBDResourceTypeDiskful,
						ConnectionState: v1alpha1.ConnectionStateConnected,
						Paths: []v1alpha1.DRBDResourcePathStatus{
							{SystemNetworkName: "net-1", Established: true},
						},
					},
					{
						Name:            "peer-2",
						Type:            v1alpha1.DRBDResourceTypeDiskful,
						ConnectionState: v1alpha1.ConnectionStateConnecting,
					},
				},
			},
		}
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{
			Members: []v1alpha1.DatameshMember{
				{Name: "peer-1", Type: v1alpha1.DatameshMemberTypeDiskful},
				{Name: "peer-2", Type: v1alpha1.DatameshMemberTypeDiskful},
				{Name: "rvr-1"},
			},
			SystemNetworkNames: []string{"net-1"},
		}
		rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			{SystemNetworkName: "net-1"},
		}

		outcome := flow.MergeEnsures(
			ensureStatusPeers(ctx, rvr, drbdr),
			ensureConditionFullyConnected(ctx, rvr, drbdr, datamesh, true),
		)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedReasonPartiallyConnected))
		Expect(cond.Message).To(ContainSubstring("1 of 2"))
	})

	It("sets PartiallyConnected when not a member but has connections", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Peers: []v1alpha1.DRBDResourcePeerStatus{
					{
						Name:            "peer-1",
						Type:            v1alpha1.DRBDResourceTypeDiskful,
						ConnectionState: v1alpha1.ConnectionStateConnected,
						Paths: []v1alpha1.DRBDResourcePathStatus{
							{SystemNetworkName: "net-1", Established: true},
						},
					},
				},
			},
		}
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{
			Members: []v1alpha1.DatameshMember{
				{Name: "peer-1", Type: v1alpha1.DatameshMemberTypeDiskful},
				// rvr-1 is NOT a member
			},
			SystemNetworkNames: []string{"net-1"},
		}
		rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			{SystemNetworkName: "net-1"},
		}

		outcome := flow.MergeEnsures(
			ensureStatusPeers(ctx, rvr, drbdr),
			ensureConditionFullyConnected(ctx, rvr, drbdr, datamesh, true),
		)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedReasonPartiallyConnected))
		Expect(cond.Message).To(ContainSubstring("not a datamesh member"))
	})

	It("returns no change when already in sync", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Peers: []v1alpha1.DRBDResourcePeerStatus{
					{
						Name:            "peer-1",
						Type:            v1alpha1.DRBDResourceTypeDiskful,
						ConnectionState: v1alpha1.ConnectionStateConnected,
						Paths: []v1alpha1.DRBDResourcePathStatus{
							{SystemNetworkName: "net-1", Established: true},
						},
					},
				},
			},
		}
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{
			Members: []v1alpha1.DatameshMember{
				{Name: "peer-1", Type: v1alpha1.DatameshMemberTypeDiskful},
				{Name: "rvr-1"},
			},
			SystemNetworkNames: []string{"net-1"},
		}

		// First call
		outcome1 := flow.MergeEnsures(
			ensureStatusPeers(ctx, rvr, drbdr),
			ensureConditionFullyConnected(ctx, rvr, drbdr, datamesh, true),
		)
		Expect(outcome1.Error()).NotTo(HaveOccurred())
		Expect(outcome1.DidChange()).To(BeTrue())

		// Second call should report no change
		outcome2 := flow.MergeEnsures(
			ensureStatusPeers(ctx, rvr, drbdr),
			ensureConditionFullyConnected(ctx, rvr, drbdr, datamesh, true),
		)
		Expect(outcome2.Error()).NotTo(HaveOccurred())
		Expect(outcome2.DidChange()).To(BeFalse())
	})

	It("populates peers from drbdr with computed types", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Peers: []v1alpha1.DRBDResourcePeerStatus{
					{
						Name:            "peer-1",
						Type:            v1alpha1.DRBDResourceTypeDiskful,
						ConnectionState: v1alpha1.ConnectionStateConnected,
						DiskState:       v1alpha1.DiskStateUpToDate,
						Paths: []v1alpha1.DRBDResourcePathStatus{
							{SystemNetworkName: "net-1", Established: true},
						},
					},
					{
						Name:            "peer-2",
						Type:            v1alpha1.DRBDResourceTypeDiskless,
						AllowRemoteRead: true, // TieBreaker
					},
				},
			},
		}
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{
			Members: []v1alpha1.DatameshMember{
				{Name: "peer-1", Type: v1alpha1.DatameshMemberTypeDiskful},
				{Name: "peer-2", Type: v1alpha1.DatameshMemberTypeTieBreaker},
				{Name: "rvr-1"},
			},
			SystemNetworkNames: []string{"net-1"},
		}

		outcome := flow.MergeEnsures(
			ensureStatusPeers(ctx, rvr, drbdr),
			ensureConditionFullyConnected(ctx, rvr, drbdr, datamesh, true),
		)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(rvr.Status.Peers).To(HaveLen(2))

		// peer-1: Diskful
		peer1 := findPeerByName(rvr.Status.Peers, "peer-1")
		Expect(peer1).NotTo(BeNil())
		Expect(peer1.Type).To(Equal(v1alpha1.ReplicaTypeDiskful))
		Expect(peer1.ConnectionState).To(Equal(v1alpha1.ConnectionStateConnected))
		Expect(peer1.BackingVolumeState).To(Equal(v1alpha1.DiskStateUpToDate))

		// peer-2: TieBreaker (Diskless + AllowRemoteRead=true)
		peer2 := findPeerByName(rvr.Status.Peers, "peer-2")
		Expect(peer2).NotTo(BeNil())
		Expect(peer2.Type).To(Equal(v1alpha1.ReplicaTypeTieBreaker))
	})
})

func findPeerByName(peers []v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus, name string) *v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus {
	for i := range peers {
		if peers[i].Name == name {
			return &peers[i]
		}
	}
	return nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Apply condition helpers tests
//

var _ = Describe("applyRVRAttachment", func() {
	var rvr *v1alpha1.ReplicatedVolumeReplica

	BeforeEach(func() {
		rvr = &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}
	})

	It("sets attachment from nil and returns true", func() {
		changed := applyRVRAttachment(rvr, &v1alpha1.ReplicatedVolumeReplicaStatusAttachment{
			DevicePath:  "/dev/drbd1000",
			IOSuspended: false,
		})
		Expect(changed).To(BeTrue())
		Expect(rvr.Status.Attachment).NotTo(BeNil())
		Expect(rvr.Status.Attachment.DevicePath).To(Equal("/dev/drbd1000"))
		Expect(rvr.Status.Attachment.IOSuspended).To(BeFalse())
	})

	It("clears attachment when nil", func() {
		rvr.Status.Attachment = &v1alpha1.ReplicatedVolumeReplicaStatusAttachment{
			DevicePath:  "/dev/drbd1000",
			IOSuspended: false,
		}
		changed := applyRVRAttachment(rvr, nil)
		Expect(changed).To(BeTrue())
		Expect(rvr.Status.Attachment).To(BeNil())
	})

	It("is idempotent for nil", func() {
		changed := applyRVRAttachment(rvr, nil)
		Expect(changed).To(BeFalse())
	})

	It("is idempotent for same value", func() {
		applyRVRAttachment(rvr, &v1alpha1.ReplicatedVolumeReplicaStatusAttachment{
			DevicePath:  "/dev/drbd1000",
			IOSuspended: false,
		})
		changed := applyRVRAttachment(rvr, &v1alpha1.ReplicatedVolumeReplicaStatusAttachment{
			DevicePath:  "/dev/drbd1000",
			IOSuspended: false,
		})
		Expect(changed).To(BeFalse())
	})

	It("updates DevicePath", func() {
		rvr.Status.Attachment = &v1alpha1.ReplicatedVolumeReplicaStatusAttachment{
			DevicePath:  "/dev/drbd1000",
			IOSuspended: false,
		}
		changed := applyRVRAttachment(rvr, &v1alpha1.ReplicatedVolumeReplicaStatusAttachment{
			DevicePath:  "/dev/drbd2000",
			IOSuspended: false,
		})
		Expect(changed).To(BeTrue())
		Expect(rvr.Status.Attachment.DevicePath).To(Equal("/dev/drbd2000"))
	})

	It("updates IOSuspended", func() {
		rvr.Status.Attachment = &v1alpha1.ReplicatedVolumeReplicaStatusAttachment{
			DevicePath:  "/dev/drbd1000",
			IOSuspended: false,
		}
		changed := applyRVRAttachment(rvr, &v1alpha1.ReplicatedVolumeReplicaStatusAttachment{
			DevicePath:  "/dev/drbd1000",
			IOSuspended: true,
		})
		Expect(changed).To(BeTrue())
		Expect(rvr.Status.Attachment.IOSuspended).To(BeTrue())
	})

	It("updates InUse", func() {
		rvr.Status.Attachment = &v1alpha1.ReplicatedVolumeReplicaStatusAttachment{
			DevicePath:  "/dev/drbd1000",
			IOSuspended: false,
			InUse:       false,
		}
		changed := applyRVRAttachment(rvr, &v1alpha1.ReplicatedVolumeReplicaStatusAttachment{
			DevicePath:  "/dev/drbd1000",
			IOSuspended: false,
			InUse:       true,
		})
		Expect(changed).To(BeTrue())
		Expect(rvr.Status.Attachment.InUse).To(BeTrue())
	})
})

var _ = Describe("ensureStatusPeers (logic)", func() {
	var (
		ctx context.Context
		rvr *v1alpha1.ReplicatedVolumeReplica
	)

	BeforeEach(func() {
		ctx = logr.NewContext(context.Background(), logr.Discard())
		rvr = &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "peer",
			},
		}
	})

	It("computes Type from drbdr peer fields", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Peers: []v1alpha1.DRBDResourcePeerStatus{
					{Name: "peer-1", Type: v1alpha1.DRBDResourceTypeDiskful, ConnectionState: v1alpha1.ConnectionStateConnected, DiskState: v1alpha1.DiskStateUpToDate, ReplicationState: v1alpha1.ReplicationStateEstablished},
					{Name: "peer-2", Type: v1alpha1.DRBDResourceTypeDiskless, AllowRemoteRead: true},  // TieBreaker
					{Name: "peer-3", Type: v1alpha1.DRBDResourceTypeDiskless, AllowRemoteRead: false}, // Access
				},
			},
		}

		outcome := ensureStatusPeers(ctx, rvr, drbdr)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rvr.Status.Peers).To(HaveLen(3))

		// peer-1: Diskful
		peer1 := findPeerByName(rvr.Status.Peers, "peer-1")
		Expect(peer1).NotTo(BeNil())
		Expect(peer1.Type).To(Equal(v1alpha1.ReplicaTypeDiskful))
		Expect(peer1.ConnectionState).To(Equal(v1alpha1.ConnectionStateConnected))
		Expect(peer1.BackingVolumeState).To(Equal(v1alpha1.DiskStateUpToDate))
		Expect(peer1.ReplicationState).To(Equal(v1alpha1.ReplicationStateEstablished))

		// peer-2: TieBreaker (Diskless + AllowRemoteRead=true)
		peer2 := findPeerByName(rvr.Status.Peers, "peer-2")
		Expect(peer2).NotTo(BeNil())
		Expect(peer2.Type).To(Equal(v1alpha1.ReplicaTypeTieBreaker))

		// peer-3: Access (Diskless + AllowRemoteRead=false)
		peer3 := findPeerByName(rvr.Status.Peers, "peer-3")
		Expect(peer3).NotTo(BeNil())
		Expect(peer3.Type).To(Equal(v1alpha1.ReplicaTypeAccess))
	})

	It("mirrors drbdr.Status.Peers order", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Peers: []v1alpha1.DRBDResourcePeerStatus{
					{Name: "peer-3", Type: v1alpha1.DRBDResourceTypeDiskful},
					{Name: "peer-1", Type: v1alpha1.DRBDResourceTypeDiskful},
					{Name: "peer-2", Type: v1alpha1.DRBDResourceTypeDiskful},
				},
			},
		}

		outcome := ensureStatusPeers(ctx, rvr, drbdr)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rvr.Status.Peers).To(HaveLen(3))
		// Order mirrors drbdr.Status.Peers
		Expect(rvr.Status.Peers[0].Name).To(Equal("peer-3"))
		Expect(rvr.Status.Peers[1].Name).To(Equal("peer-1"))
		Expect(rvr.Status.Peers[2].Name).To(Equal("peer-2"))
	})

	It("returns no change when unchanged (idempotent)", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Peers: []v1alpha1.DRBDResourcePeerStatus{
					{Name: "peer-1", Type: v1alpha1.DRBDResourceTypeDiskful, ConnectionState: v1alpha1.ConnectionStateConnected, ReplicationState: v1alpha1.ReplicationStateEstablished},
				},
			},
		}

		outcome1 := ensureStatusPeers(ctx, rvr, drbdr)
		Expect(outcome1.DidChange()).To(BeTrue())

		outcome2 := ensureStatusPeers(ctx, rvr, drbdr)
		Expect(outcome2.Error()).NotTo(HaveOccurred())
		Expect(outcome2.DidChange()).To(BeFalse())
	})

	It("trims excess peers from previous state", func() {
		// Start with 2 peers
		rvr.Status.Peers = []v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{
			{Name: "peer-1"},
			{Name: "peer-2"},
		}

		// Now only 1 peer
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Peers: []v1alpha1.DRBDResourcePeerStatus{
					{Name: "peer-1", Type: v1alpha1.DRBDResourceTypeDiskful},
				},
			},
		}

		outcome := ensureStatusPeers(ctx, rvr, drbdr)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rvr.Status.Peers).To(HaveLen(1))
	})

	It("clears peers when drbdr has no peers", func() {
		rvr.Status.Peers = []v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{{Name: "peer-1"}}
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Peers: []v1alpha1.DRBDResourcePeerStatus{},
			},
		}

		outcome := ensureStatusPeers(ctx, rvr, drbdr)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rvr.Status.Peers).To(BeEmpty())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// ensureStatusQuorum tests
//

var _ = Describe("ensureStatusQuorum", func() {
	var (
		ctx context.Context
		rvr *v1alpha1.ReplicatedVolumeReplica
	)

	BeforeEach(func() {
		ctx = logr.NewContext(context.Background(), logr.Discard())
		rvr = &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}
	})

	It("clears quorum status when drbdr is nil", func() {
		rvr.Status.Quorum = boolPtr(true)
		rvr.Status.QuorumSummary = &v1alpha1.ReplicatedVolumeReplicaStatusQuorumSummary{
			ConnectedDiskfulPeers:  1,
			ConnectedUpToDatePeers: 1,
		}

		outcome := ensureStatusQuorum(ctx, rvr, nil)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(rvr.Status.Quorum).To(BeNil())
		Expect(rvr.Status.QuorumSummary).To(BeNil())
	})

	It("copies quorum from drbdr.Status.Quorum", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Quorum: boolPtr(true),
			},
		}

		outcome := ensureStatusQuorum(ctx, rvr, drbdr)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(rvr.Status.Quorum).NotTo(BeNil())
		Expect(*rvr.Status.Quorum).To(BeTrue())
	})

	It("fills ReplicatedVolumeReplicaStatusQuorumSummary correctly from peers and drbdr", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Quorum: boolPtr(true),
				ActiveConfiguration: &v1alpha1.DRBDResourceActiveConfiguration{
					Quorum:                  bytePtr(2),
					QuorumMinimumRedundancy: bytePtr(1),
				},
			},
		}
		// Set up peers with different states
		rvr.Status.Peers = []v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{
			{
				Name:               "peer-1",
				Type:               v1alpha1.ReplicaTypeDiskful,
				ConnectionState:    v1alpha1.ConnectionStateConnected,
				BackingVolumeState: v1alpha1.DiskStateUpToDate,
			},
			{
				Name:            "peer-2",
				Type:            v1alpha1.ReplicaTypeTieBreaker,
				ConnectionState: v1alpha1.ConnectionStateConnected,
			},
		}

		outcome := ensureStatusQuorum(ctx, rvr, drbdr)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(rvr.Status.QuorumSummary).NotTo(BeNil())
		// peer-1 (Diskful) connected = 1 diskful peer
		Expect(rvr.Status.QuorumSummary.ConnectedDiskfulPeers).To(Equal(1))
		// peer-2 (TieBreaker) connected = 1 tie-breaker peer
		Expect(rvr.Status.QuorumSummary.ConnectedTieBreakerPeers).To(Equal(1))
		// peer-1 has UpToDate = 1 up-to-date peer
		Expect(rvr.Status.QuorumSummary.ConnectedUpToDatePeers).To(Equal(1))
		Expect(rvr.Status.QuorumSummary.Quorum).NotTo(BeNil())
		Expect(*rvr.Status.QuorumSummary.Quorum).To(Equal(2))
		Expect(rvr.Status.QuorumSummary.QuorumMinimumRedundancy).NotTo(BeNil())
		Expect(*rvr.Status.QuorumSummary.QuorumMinimumRedundancy).To(Equal(1))
	})

	It("returns no change when already in sync", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Quorum: boolPtr(true),
			},
		}

		// First call
		outcome1 := ensureStatusQuorum(ctx, rvr, drbdr)
		Expect(outcome1.Error()).NotTo(HaveOccurred())
		Expect(outcome1.DidChange()).To(BeTrue())

		// Second call should report no change
		outcome2 := ensureStatusQuorum(ctx, rvr, drbdr)
		Expect(outcome2.Error()).NotTo(HaveOccurred())
		Expect(outcome2.DidChange()).To(BeFalse())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// ensureConditionReady tests
//

var _ = Describe("ensureStatusBackingVolume", func() {
	var (
		ctx  context.Context
		rvr  *v1alpha1.ReplicatedVolumeReplica
		llvs []snc.LVMLogicalVolume
	)

	BeforeEach(func() {
		ctx = logr.NewContext(context.Background(), logr.Discard())
		rvr = &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				Type: v1alpha1.ReplicaTypeDiskful,
			},
		}
		// Default LLV for tests that reach normal path.
		llvs = []snc.LVMLogicalVolume{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "llv-1"},
				Spec: snc.LVMLogicalVolumeSpec{
					LVMVolumeGroupName: "vg-1",
				},
				Status: &snc.LVMLogicalVolumeStatus{
					ActualSize: resource.MustParse("10Gi"),
				},
			},
		}
	})

	It("clears backingVolume when drbdr is nil", func() {
		rvr.Status.BackingVolume = &v1alpha1.ReplicatedVolumeReplicaStatusBackingVolume{
			State: v1alpha1.DiskStateUpToDate,
		}

		outcome := ensureStatusBackingVolume(ctx, rvr, nil, nil)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rvr.Status.BackingVolume).To(BeNil())
	})

	It("populates backingVolume fields from drbdr and llv", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				DiskState: v1alpha1.DiskStateUpToDate,
				ActiveConfiguration: &v1alpha1.DRBDResourceActiveConfiguration{
					Role:                 v1alpha1.DRBDRolePrimary,
					LVMLogicalVolumeName: "llv-1",
				},
			},
		}

		outcome := ensureStatusBackingVolume(ctx, rvr, drbdr, llvs)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(rvr.Status.BackingVolume).NotTo(BeNil())
		Expect(rvr.Status.BackingVolume.State).To(Equal(v1alpha1.DiskStateUpToDate))
		Expect(rvr.Status.BackingVolume.LVMVolumeGroupName).To(Equal("vg-1"))
		Expect(rvr.Status.BackingVolume.Size.String()).To(Equal("10Gi"))
	})

	It("clears backingVolume when LVMLogicalVolumeName is empty", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				DiskState: v1alpha1.DiskStateUpToDate,
				ActiveConfiguration: &v1alpha1.DRBDResourceActiveConfiguration{
					Role:                 v1alpha1.DRBDRoleSecondary,
					LVMLogicalVolumeName: "", // empty — no backing volume configured
				},
			},
		}

		outcome := ensureStatusBackingVolume(ctx, rvr, drbdr, llvs)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(rvr.Status.BackingVolume).To(BeNil())
	})

	It("returns error when active LLV not found", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				DiskState: v1alpha1.DiskStateUpToDate,
				ActiveConfiguration: &v1alpha1.DRBDResourceActiveConfiguration{
					Role:                 v1alpha1.DRBDRoleSecondary,
					LVMLogicalVolumeName: "non-existent-llv",
				},
			},
		}

		outcome := ensureStatusBackingVolume(ctx, rvr, drbdr, llvs)

		Expect(outcome.Error()).To(HaveOccurred())
		Expect(outcome.Error().Error()).To(ContainSubstring("not found"))
	})

	It("returns no change when already up to date", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				DiskState: v1alpha1.DiskStateUpToDate,
				ActiveConfiguration: &v1alpha1.DRBDResourceActiveConfiguration{
					Role:                 v1alpha1.DRBDRoleSecondary,
					LVMLogicalVolumeName: "llv-1",
				},
			},
		}

		// First call
		outcome1 := ensureStatusBackingVolume(ctx, rvr, drbdr, llvs)
		Expect(outcome1.Error()).NotTo(HaveOccurred())
		Expect(outcome1.DidChange()).To(BeTrue())

		// Second call should report no change
		outcome2 := ensureStatusBackingVolume(ctx, rvr, drbdr, llvs)
		Expect(outcome2.Error()).NotTo(HaveOccurred())
		Expect(outcome2.DidChange()).To(BeFalse())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// ensureConditionBackingVolumeUpToDate tests
//
