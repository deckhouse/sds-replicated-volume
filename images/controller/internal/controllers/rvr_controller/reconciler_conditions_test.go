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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

var _ = Describe("findLVGInEligibleNodeByName", func() {
	It("returns nil when LVG not found", func() {
		node := &v1alpha1.ReplicatedStoragePoolEligibleNode{
			NodeName: "node-1",
			LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
				{Name: "lvg-other"},
			},
		}

		Expect(findLVGInEligibleNodeByName(node, "lvg-1")).To(BeNil())
	})

	It("returns nil for empty LVMVolumeGroups", func() {
		node := &v1alpha1.ReplicatedStoragePoolEligibleNode{
			NodeName:        "node-1",
			LVMVolumeGroups: nil,
		}

		Expect(findLVGInEligibleNodeByName(node, "lvg-1")).To(BeNil())
	})

	It("returns pointer to found LVG by name", func() {
		node := &v1alpha1.ReplicatedStoragePoolEligibleNode{
			NodeName: "node-1",
			LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
				{Name: "lvg-1", ThinPoolName: "tp-1"},
				{Name: "lvg-2", ThinPoolName: "tp-2"},
			},
		}

		result := findLVGInEligibleNodeByName(node, "lvg-1")

		Expect(result).NotTo(BeNil())
		Expect(result.Name).To(Equal("lvg-1"))
		Expect(result.ThinPoolName).To(Equal("tp-1"))
	})

	It("returns pointer to slice element (same memory)", func() {
		node := &v1alpha1.ReplicatedStoragePoolEligibleNode{
			NodeName: "node-1",
			LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
				{Name: "lvg-1"},
			},
		}

		result := findLVGInEligibleNodeByName(node, "lvg-1")

		Expect(result).To(BeIdenticalTo(&node.LVMVolumeGroups[0]))
	})
})

var _ = Describe("findLVGInEligibleNode", func() {
	It("returns nil when LVG not found", func() {
		node := &v1alpha1.ReplicatedStoragePoolEligibleNode{
			NodeName: "node-1",
			LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
				{Name: "lvg-other", ThinPoolName: "tp-1"},
			},
		}

		Expect(findLVGInEligibleNode(node, "lvg-1", "tp-1")).To(BeNil())
	})

	It("returns nil when name matches but thinPoolName does not", func() {
		node := &v1alpha1.ReplicatedStoragePoolEligibleNode{
			NodeName: "node-1",
			LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
				{Name: "lvg-1", ThinPoolName: "tp-other"},
			},
		}

		Expect(findLVGInEligibleNode(node, "lvg-1", "tp-1")).To(BeNil())
	})

	It("returns nil for empty LVMVolumeGroups", func() {
		node := &v1alpha1.ReplicatedStoragePoolEligibleNode{
			NodeName:        "node-1",
			LVMVolumeGroups: nil,
		}

		Expect(findLVGInEligibleNode(node, "lvg-1", "tp-1")).To(BeNil())
	})

	It("returns pointer to found LVG when both name and thinPoolName match", func() {
		node := &v1alpha1.ReplicatedStoragePoolEligibleNode{
			NodeName: "node-1",
			LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
				{Name: "lvg-1", ThinPoolName: "tp-1"},
				{Name: "lvg-1", ThinPoolName: "tp-2"},
				{Name: "lvg-2", ThinPoolName: "tp-1"},
			},
		}

		result := findLVGInEligibleNode(node, "lvg-1", "tp-2")

		Expect(result).NotTo(BeNil())
		Expect(result.Name).To(Equal("lvg-1"))
		Expect(result.ThinPoolName).To(Equal("tp-2"))
	})

	It("returns pointer to slice element (same memory)", func() {
		node := &v1alpha1.ReplicatedStoragePoolEligibleNode{
			NodeName: "node-1",
			LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
				{Name: "lvg-1", ThinPoolName: "tp-1"},
			},
		}

		result := findLVGInEligibleNode(node, "lvg-1", "tp-1")

		Expect(result).To(BeIdenticalTo(&node.LVMVolumeGroups[0]))
	})
})

var _ = Describe("computeEligibilityWarnings", func() {
	It("returns empty string when no warnings", func() {
		node := &v1alpha1.ReplicatedStoragePoolEligibleNode{
			NodeName:      "node-1",
			Unschedulable: false,
			NodeReady:     true,
			AgentReady:    true,
		}

		result := computeEligibilityWarnings(node, nil)

		Expect(result).To(BeEmpty())
	})

	It("returns single warning without 'and'", func() {
		node := &v1alpha1.ReplicatedStoragePoolEligibleNode{
			NodeName:      "node-1",
			Unschedulable: true,
			NodeReady:     true,
			AgentReady:    true,
		}

		result := computeEligibilityWarnings(node, nil)

		Expect(result).To(Equal("node is unschedulable"))
	})

	It("returns two warnings joined with 'and'", func() {
		node := &v1alpha1.ReplicatedStoragePoolEligibleNode{
			NodeName:      "node-1",
			Unschedulable: true,
			NodeReady:     false,
			AgentReady:    true,
		}

		result := computeEligibilityWarnings(node, nil)

		Expect(result).To(Equal("node is unschedulable and node is not ready"))
	})

	It("returns 3+ warnings with serial comma", func() {
		node := &v1alpha1.ReplicatedStoragePoolEligibleNode{
			NodeName:      "node-1",
			Unschedulable: true,
			NodeReady:     false,
			AgentReady:    false,
		}

		result := computeEligibilityWarnings(node, nil)

		Expect(result).To(Equal("node is unschedulable, node is not ready, and agent is not ready"))
	})

	It("collects all node warnings", func() {
		node := &v1alpha1.ReplicatedStoragePoolEligibleNode{
			NodeName:      "node-1",
			Unschedulable: true,
			NodeReady:     false,
			AgentReady:    false,
		}

		result := computeEligibilityWarnings(node, nil)

		Expect(result).To(ContainSubstring("node is unschedulable"))
		Expect(result).To(ContainSubstring("node is not ready"))
		Expect(result).To(ContainSubstring("agent is not ready"))
	})

	It("collects LVG warnings", func() {
		node := &v1alpha1.ReplicatedStoragePoolEligibleNode{
			NodeName:      "node-1",
			Unschedulable: false,
			NodeReady:     true,
			AgentReady:    true,
		}
		lvg := &v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
			Name:          "lvg-1",
			Unschedulable: true,
			Ready:         false,
		}

		result := computeEligibilityWarnings(node, lvg)

		Expect(result).To(Equal("LVMVolumeGroup is unschedulable and LVMVolumeGroup is not ready"))
	})

	It("collects both node and LVG warnings", func() {
		node := &v1alpha1.ReplicatedStoragePoolEligibleNode{
			NodeName:      "node-1",
			Unschedulable: true,
			NodeReady:     true,
			AgentReady:    true,
		}
		lvg := &v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
			Name:          "lvg-1",
			Unschedulable: false,
			Ready:         false,
		}

		result := computeEligibilityWarnings(node, lvg)

		Expect(result).To(Equal("node is unschedulable and LVMVolumeGroup is not ready"))
	})

	It("handles nil LVG (only node warnings)", func() {
		node := &v1alpha1.ReplicatedStoragePoolEligibleNode{
			NodeName:      "node-1",
			Unschedulable: true,
			NodeReady:     true,
			AgentReady:    true,
		}

		result := computeEligibilityWarnings(node, nil)

		Expect(result).To(Equal("node is unschedulable"))
		Expect(result).NotTo(ContainSubstring("LVMVolumeGroup"))
	})
})

var _ = Describe("thinPoolMismatchMessage", func() {
	It("returns LVM thick message when ThinPool specified but RSP type is LVM", func() {
		msg := thinPoolMismatchMessage(v1alpha1.ReplicatedStoragePoolTypeLVM, "lvg-1", "tp-1")

		Expect(msg).To(ContainSubstring("tp-1"))
		Expect(msg).To(ContainSubstring("LVM (thick)"))
	})

	It("returns LVMThin message when ThinPool not specified but RSP type is LVMThin", func() {
		msg := thinPoolMismatchMessage(v1alpha1.ReplicatedStoragePoolTypeLVMThin, "lvg-1", "")

		Expect(msg).To(ContainSubstring("ThinPool is not specified"))
		Expect(msg).To(ContainSubstring("LVMThin"))
	})

	It("returns not-in-list message when ThinPool specified for LVMThin but not in list", func() {
		msg := thinPoolMismatchMessage(v1alpha1.ReplicatedStoragePoolTypeLVMThin, "lvg-1", "tp-missing")

		Expect(msg).To(ContainSubstring("tp-missing"))
		Expect(msg).To(ContainSubstring("not in the allowed list"))
		Expect(msg).To(ContainSubstring("lvg-1"))
	})

	It("returns unexpected state message for default case", func() {
		msg := thinPoolMismatchMessage(v1alpha1.ReplicatedStoragePoolTypeLVM, "lvg-1", "")

		Expect(msg).To(ContainSubstring("Unexpected state"))
		Expect(msg).To(ContainSubstring("lvg-1"))
	})
})

var _ = Describe("applySatisfyEligibleNodesCondAbsent", func() {
	It("removes condition when present", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}
		// First set the condition.
		applySatisfyEligibleNodesCondTrue(rvr, "Satisfied", "Test")

		changed := applySatisfyEligibleNodesCondAbsent(rvr)

		Expect(changed).To(BeTrue())
		Expect(obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType)).To(BeNil())
	})

	It("returns false when condition already absent", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}

		changed := applySatisfyEligibleNodesCondAbsent(rvr)

		Expect(changed).To(BeFalse())
	})
})

var _ = Describe("applySatisfyEligibleNodesCondUnknown", func() {
	It("sets condition to Unknown", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}

		changed := applySatisfyEligibleNodesCondUnknown(rvr, "WaitingForReplicatedVolume", "Test message")

		Expect(changed).To(BeTrue())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonWaitingForReplicatedVolume))
		Expect(cond.Message).To(Equal("Test message"))
	})

	It("returns false when condition already set to same value", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}
		applySatisfyEligibleNodesCondUnknown(rvr, "WaitingForReplicatedVolume", "Test message")

		changed := applySatisfyEligibleNodesCondUnknown(rvr, "WaitingForReplicatedVolume", "Test message")

		Expect(changed).To(BeFalse())
	})
})

var _ = Describe("applySatisfyEligibleNodesCondFalse", func() {
	It("sets condition to False", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}

		changed := applySatisfyEligibleNodesCondFalse(rvr, "NodeMismatch", "Test message")

		Expect(changed).To(BeTrue())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal("NodeMismatch"))
		Expect(cond.Message).To(Equal("Test message"))
	})

	It("returns false when condition already set to same value", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}
		applySatisfyEligibleNodesCondFalse(rvr, "NodeMismatch", "Test message")

		changed := applySatisfyEligibleNodesCondFalse(rvr, "NodeMismatch", "Test message")

		Expect(changed).To(BeFalse())
	})
})

var _ = Describe("applySatisfyEligibleNodesCondTrue", func() {
	It("sets condition to True", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}

		changed := applySatisfyEligibleNodesCondTrue(rvr, "Satisfied", "Test message")

		Expect(changed).To(BeTrue())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonSatisfied))
		Expect(cond.Message).To(Equal("Test message"))
	})

	It("returns false when condition already set to same value", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}
		applySatisfyEligibleNodesCondTrue(rvr, "Satisfied", "Test message")

		changed := applySatisfyEligibleNodesCondTrue(rvr, "Satisfied", "Test message")

		Expect(changed).To(BeFalse())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Get helpers tests
//

var _ = Describe("ensureConditionSatisfyEligibleNodes", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("removes condition when node not selected", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				NodeName: "", // Not selected.
			},
		}
		// Pre-set condition.
		applySatisfyEligibleNodesCondTrue(rvr, "Satisfied", "Test")

		outcome := ensureConditionSatisfyEligibleNodes(ctx, rvr, nil, nil)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeTrue())
		Expect(obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType)).To(BeNil())
	})

	It("sets condition to Unknown when RV is nil", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				NodeName: "node-1",
			},
		}

		outcome := ensureConditionSatisfyEligibleNodes(ctx, rvr, nil, nil)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeTrue())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonWaitingForReplicatedVolume))
	})

	It("sets condition to Unknown when RV has no Configuration", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				NodeName: "node-1",
			},
		}
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: nil,
			},
		}

		outcome := ensureConditionSatisfyEligibleNodes(ctx, rvr, rv, nil)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType)
		Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonWaitingForReplicatedVolume))
	})

	It("sets condition to Unknown when RSP not found (nil rspView)", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				NodeName: "node-1",
			},
		}
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
					Topology:           v1alpha1.TopologyIgnored,
					FailuresToTolerate: 1, GuaranteedMinimumDataRedundancy: 1,
					VolumeAccess:              v1alpha1.VolumeAccessPreferablyLocal,
					ReplicatedStoragePoolName: "rsp-missing",
				},
			},
		}

		outcome := ensureConditionSatisfyEligibleNodes(ctx, rvr, rv, nil)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType)
		Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonWaitingForReplicatedVolume))
		Expect(cond.Message).To(ContainSubstring("ReplicatedStoragePool not found"))
	})

	It("sets condition to False when node not in eligibleNodes", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				NodeName: "node-1",
			},
		}
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
					Topology:           v1alpha1.TopologyIgnored,
					FailuresToTolerate: 1, GuaranteedMinimumDataRedundancy: 1,
					VolumeAccess:              v1alpha1.VolumeAccessPreferablyLocal,
					ReplicatedStoragePoolName: "rsp-1",
				},
			},
		}
		// rspView with nil EligibleNode means node not found in RSP.
		rspView := &rspEligibilityView{
			Type:         v1alpha1.ReplicatedStoragePoolTypeLVM,
			EligibleNode: nil,
		}

		outcome := ensureConditionSatisfyEligibleNodes(ctx, rvr, rv, rspView)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonNodeMismatch))
	})

	It("sets condition to False when Diskful LVMVolumeGroup not found", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				NodeName:           "node-1",
				Type:               v1alpha1.ReplicaTypeDiskful,
				LVMVolumeGroupName: "lvg-1",
			},
		}
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
					Topology:           v1alpha1.TopologyIgnored,
					FailuresToTolerate: 1, GuaranteedMinimumDataRedundancy: 1,
					VolumeAccess:              v1alpha1.VolumeAccessPreferablyLocal,
					ReplicatedStoragePoolName: "rsp-1",
				},
			},
		}
		rspView := &rspEligibilityView{
			Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
			EligibleNode: &v1alpha1.ReplicatedStoragePoolEligibleNode{
				NodeName:   "node-1",
				NodeReady:  true,
				AgentReady: true,
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
					{Name: "lvg-other", Ready: true},
				},
			},
		}

		outcome := ensureConditionSatisfyEligibleNodes(ctx, rvr, rv, rspView)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonLVMVolumeGroupMismatch))
	})

	It("sets condition to False when ThinPool not found", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				NodeName:                   "node-1",
				Type:                       v1alpha1.ReplicaTypeDiskful,
				LVMVolumeGroupName:         "lvg-1",
				LVMVolumeGroupThinPoolName: "tp-1",
			},
		}
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
					Topology:           v1alpha1.TopologyIgnored,
					FailuresToTolerate: 1, GuaranteedMinimumDataRedundancy: 1,
					VolumeAccess:              v1alpha1.VolumeAccessPreferablyLocal,
					ReplicatedStoragePoolName: "rsp-1",
				},
			},
		}
		rspView := &rspEligibilityView{
			Type: v1alpha1.ReplicatedStoragePoolTypeLVMThin,
			EligibleNode: &v1alpha1.ReplicatedStoragePoolEligibleNode{
				NodeName:   "node-1",
				NodeReady:  true,
				AgentReady: true,
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
					{Name: "lvg-1", ThinPoolName: "tp-other", Ready: true},
				},
			},
		}

		outcome := ensureConditionSatisfyEligibleNodes(ctx, rvr, rv, rspView)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonThinPoolMismatch))
	})

	It("sets condition to True when all checks pass (Diskless)", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				NodeName: "node-1",
				Type:     v1alpha1.ReplicaTypeAccess,
			},
		}
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
					Topology:           v1alpha1.TopologyIgnored,
					FailuresToTolerate: 1, GuaranteedMinimumDataRedundancy: 1,
					VolumeAccess:              v1alpha1.VolumeAccessPreferablyLocal,
					ReplicatedStoragePoolName: "rsp-1",
				},
			},
		}
		rspView := &rspEligibilityView{
			Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
			EligibleNode: &v1alpha1.ReplicatedStoragePoolEligibleNode{
				NodeName:   "node-1",
				NodeReady:  true,
				AgentReady: true,
			},
		}

		outcome := ensureConditionSatisfyEligibleNodes(ctx, rvr, rv, rspView)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType)
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonSatisfied))
		Expect(cond.Message).To(Equal("Replica satisfies eligible nodes requirements"))
	})

	It("sets condition to True when all checks pass (Diskful with ThinPool)", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				NodeName:                   "node-1",
				Type:                       v1alpha1.ReplicaTypeDiskful,
				LVMVolumeGroupName:         "lvg-1",
				LVMVolumeGroupThinPoolName: "tp-1",
			},
		}
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
					Topology:           v1alpha1.TopologyIgnored,
					FailuresToTolerate: 1, GuaranteedMinimumDataRedundancy: 1,
					VolumeAccess:              v1alpha1.VolumeAccessPreferablyLocal,
					ReplicatedStoragePoolName: "rsp-1",
				},
			},
		}
		rspView := &rspEligibilityView{
			Type: v1alpha1.ReplicatedStoragePoolTypeLVMThin,
			EligibleNode: &v1alpha1.ReplicatedStoragePoolEligibleNode{
				NodeName:   "node-1",
				NodeReady:  true,
				AgentReady: true,
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
					{Name: "lvg-1", ThinPoolName: "tp-1", Ready: true},
				},
			},
		}

		outcome := ensureConditionSatisfyEligibleNodes(ctx, rvr, rv, rspView)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType)
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonSatisfied))
	})

	It("sets condition to True with warnings when node is unschedulable", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				NodeName: "node-1",
				Type:     v1alpha1.ReplicaTypeAccess,
			},
		}
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
					Topology:           v1alpha1.TopologyIgnored,
					FailuresToTolerate: 1, GuaranteedMinimumDataRedundancy: 1,
					VolumeAccess:              v1alpha1.VolumeAccessPreferablyLocal,
					ReplicatedStoragePoolName: "rsp-1",
				},
			},
		}
		rspView := &rspEligibilityView{
			Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
			EligibleNode: &v1alpha1.ReplicatedStoragePoolEligibleNode{
				NodeName:      "node-1",
				NodeReady:     true,
				AgentReady:    true,
				Unschedulable: true, // Warning.
			},
		}

		outcome := ensureConditionSatisfyEligibleNodes(ctx, rvr, rv, rspView)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType)
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonSatisfied))
		Expect(cond.Message).To(ContainSubstring("however note that currently"))
		Expect(cond.Message).To(ContainSubstring("node is unschedulable"))
	})

	It("sets condition to True with warnings when LVMVolumeGroup is not ready", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				NodeName:           "node-1",
				Type:               v1alpha1.ReplicaTypeDiskful,
				LVMVolumeGroupName: "lvg-1",
			},
		}
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
					Topology:           v1alpha1.TopologyIgnored,
					FailuresToTolerate: 1, GuaranteedMinimumDataRedundancy: 1,
					VolumeAccess:              v1alpha1.VolumeAccessPreferablyLocal,
					ReplicatedStoragePoolName: "rsp-1",
				},
			},
		}
		rspView := &rspEligibilityView{
			Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
			EligibleNode: &v1alpha1.ReplicatedStoragePoolEligibleNode{
				NodeName:   "node-1",
				NodeReady:  true,
				AgentReady: true,
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
					{Name: "lvg-1", Ready: false}, // Warning.
				},
			},
		}

		outcome := ensureConditionSatisfyEligibleNodes(ctx, rvr, rv, rspView)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType)
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Message).To(ContainSubstring("LVMVolumeGroup is not ready"))
	})
})

var _ = Describe("ensureConditionAttached", func() {
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

	It("removes condition when drbdr is nil", func() {
		obju.SetStatusCondition(rvr, metav1.Condition{
			Type:   v1alpha1.ReplicatedVolumeReplicaCondAttachedType,
			Status: metav1.ConditionTrue,
			Reason: "PreviousReason",
		})

		outcome := ensureConditionAttached(ctx, rvr, nil, nil, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeTrue())
		Expect(obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondAttachedType)).To(BeNil())
	})

	It("removes condition when neither intended nor actual attached", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				ActiveConfiguration: &v1alpha1.DRBDResourceActiveConfiguration{
					Role: v1alpha1.DRBDRoleSecondary,
				},
			},
		}
		member := &v1alpha1.DatameshMember{
			Attached: false,
		}

		outcome := ensureConditionAttached(ctx, rvr, drbdr, member, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondAttachedType)).To(BeNil())
	})

	It("sets Unknown when agent not ready", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				ActiveConfiguration: &v1alpha1.DRBDResourceActiveConfiguration{
					Role: v1alpha1.DRBDRoleSecondary,
				},
			},
		}
		member := &v1alpha1.DatameshMember{
			Attached: true,
		}

		outcome := ensureConditionAttached(ctx, rvr, drbdr, member, false, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondAttachedType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonAgentNotReady))
	})

	It("sets Unknown when configuration pending", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				ActiveConfiguration: &v1alpha1.DRBDResourceActiveConfiguration{
					Role: v1alpha1.DRBDRolePrimary,
				},
			},
		}
		member := &v1alpha1.DatameshMember{
			Attached: true,
		}

		outcome := ensureConditionAttached(ctx, rvr, drbdr, member, true, true)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondAttachedType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonApplyingConfiguration))
	})

	It("sets False with AttachmentFailed when intended but not actual", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				ActiveConfiguration: &v1alpha1.DRBDResourceActiveConfiguration{
					Role: v1alpha1.DRBDRoleSecondary,
				},
			},
		}
		member := &v1alpha1.DatameshMember{
			Attached: true,
		}

		outcome := ensureConditionAttached(ctx, rvr, drbdr, member, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondAttachedType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonAttachmentFailed))
	})

	It("sets True with DetachmentFailed when not intended but actual", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Device:            "/dev/drbd1000",
				DeviceIOSuspended: boolPtr(false),
				ActiveConfiguration: &v1alpha1.DRBDResourceActiveConfiguration{
					Role: v1alpha1.DRBDRolePrimary,
				},
			},
		}
		member := &v1alpha1.DatameshMember{
			Attached: false,
		}

		outcome := ensureConditionAttached(ctx, rvr, drbdr, member, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondAttachedType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonDetachmentFailed))
	})

	It("sets False with IOSuspended when attached but I/O suspended", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Device:            "/dev/drbd1000",
				DeviceIOSuspended: boolPtr(true),
				ActiveConfiguration: &v1alpha1.DRBDResourceActiveConfiguration{
					Role: v1alpha1.DRBDRolePrimary,
				},
			},
		}
		member := &v1alpha1.DatameshMember{
			Attached: true,
		}

		outcome := ensureConditionAttached(ctx, rvr, drbdr, member, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondAttachedType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonIOSuspended))
	})

	It("sets True with Attached when both intended and actual", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Device:            "/dev/drbd1000",
				DeviceIOSuspended: boolPtr(false),
				ActiveConfiguration: &v1alpha1.DRBDResourceActiveConfiguration{
					Role: v1alpha1.DRBDRolePrimary,
				},
			},
		}
		member := &v1alpha1.DatameshMember{
			Attached: true,
		}

		outcome := ensureConditionAttached(ctx, rvr, drbdr, member, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondAttachedType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonAttached))
		Expect(cond.Message).To(ContainSubstring("ready for I/O"))
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
		member := &v1alpha1.DatameshMember{
			Attached: true,
		}

		outcome1 := ensureConditionAttached(ctx, rvr, drbdr, member, true, false)
		Expect(outcome1.Error()).NotTo(HaveOccurred())
		Expect(outcome1.DidChange()).To(BeTrue())

		outcome2 := ensureConditionAttached(ctx, rvr, drbdr, member, true, false)
		Expect(outcome2.Error()).NotTo(HaveOccurred())
		Expect(outcome2.DidChange()).To(BeFalse())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// ensureStatusAttachment tests
//

var _ = Describe("Attached condition apply helpers", func() {
	var rvr *v1alpha1.ReplicatedVolumeReplica

	BeforeEach(func() {
		rvr = &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}
	})

	Describe("applyAttachedCondFalse", func() {
		It("sets condition to False", func() {
			changed := applyAttachedCondFalse(rvr, "TestReason", "Test message")
			Expect(changed).To(BeTrue())
			cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondAttachedType)
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal("TestReason"))
		})

		It("is idempotent", func() {
			applyAttachedCondFalse(rvr, "TestReason", "Test message")
			changed := applyAttachedCondFalse(rvr, "TestReason", "Test message")
			Expect(changed).To(BeFalse())
		})
	})

	Describe("applyAttachedCondUnknown", func() {
		It("sets condition to Unknown", func() {
			changed := applyAttachedCondUnknown(rvr, "TestReason", "Test message")
			Expect(changed).To(BeTrue())
			cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondAttachedType)
			Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
		})

		It("is idempotent", func() {
			applyAttachedCondUnknown(rvr, "TestReason", "Test message")
			changed := applyAttachedCondUnknown(rvr, "TestReason", "Test message")
			Expect(changed).To(BeFalse())
		})
	})

	Describe("applyAttachedCondTrue", func() {
		It("sets condition to True", func() {
			changed := applyAttachedCondTrue(rvr, "TestReason", "Test message")
			Expect(changed).To(BeTrue())
			cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondAttachedType)
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		})

		It("is idempotent", func() {
			applyAttachedCondTrue(rvr, "TestReason", "Test message")
			changed := applyAttachedCondTrue(rvr, "TestReason", "Test message")
			Expect(changed).To(BeFalse())
		})
	})

	Describe("applyAttachedCondAbsent", func() {
		It("removes the condition", func() {
			applyAttachedCondTrue(rvr, "TestReason", "Test message")
			changed := applyAttachedCondAbsent(rvr)
			Expect(changed).To(BeTrue())
			cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondAttachedType)
			Expect(cond).To(BeNil())
		})

		It("is idempotent when condition absent", func() {
			changed := applyAttachedCondAbsent(rvr)
			Expect(changed).To(BeFalse())
		})
	})
})

var _ = Describe("BackingVolumeUpToDate condition apply helpers", func() {
	var rvr *v1alpha1.ReplicatedVolumeReplica

	BeforeEach(func() {
		rvr = &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}
	})

	Describe("applyBackingVolumeUpToDateCondAbsent", func() {
		It("removes the condition", func() {
			applyBackingVolumeUpToDateCondTrue(rvr, "TestReason", "Test")
			changed := applyBackingVolumeUpToDateCondAbsent(rvr)
			Expect(changed).To(BeTrue())
			Expect(obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateType)).To(BeNil())
		})
	})

	Describe("applyBackingVolumeUpToDateCondTrue", func() {
		It("sets condition to True", func() {
			changed := applyBackingVolumeUpToDateCondTrue(rvr, "TestReason", "Test")
			Expect(changed).To(BeTrue())
			cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateType)
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		})
	})

	Describe("applyBackingVolumeUpToDateCondFalse", func() {
		It("sets condition to False", func() {
			changed := applyBackingVolumeUpToDateCondFalse(rvr, "TestReason", "Test")
			Expect(changed).To(BeTrue())
			cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateType)
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		})
	})

	Describe("applyBackingVolumeUpToDateCondUnknown", func() {
		It("sets condition to Unknown", func() {
			changed := applyBackingVolumeUpToDateCondUnknown(rvr, "TestReason", "Test")
			Expect(changed).To(BeTrue())
			cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateType)
			Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
		})
	})
})

var _ = Describe("FullyConnected condition apply helpers", func() {
	var rvr *v1alpha1.ReplicatedVolumeReplica

	BeforeEach(func() {
		rvr = &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}
	})

	Describe("applyFullyConnectedCondFalse", func() {
		It("sets condition to False", func() {
			changed := applyFullyConnectedCondFalse(rvr, "TestReason", "Test")
			Expect(changed).To(BeTrue())
			cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedType)
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		})
	})

	Describe("applyFullyConnectedCondTrue", func() {
		It("sets condition to True", func() {
			changed := applyFullyConnectedCondTrue(rvr, "TestReason", "Test")
			Expect(changed).To(BeTrue())
			cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedType)
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		})
	})

	Describe("applyFullyConnectedCondUnknown", func() {
		It("sets condition to Unknown", func() {
			changed := applyFullyConnectedCondUnknown(rvr, "TestReason", "Test")
			Expect(changed).To(BeTrue())
			cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedType)
			Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
		})
	})

	Describe("applyFullyConnectedCondAbsent", func() {
		It("removes the condition", func() {
			applyFullyConnectedCondTrue(rvr, "TestReason", "Test")
			changed := applyFullyConnectedCondAbsent(rvr)
			Expect(changed).To(BeTrue())
			Expect(obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedType)).To(BeNil())
		})
	})
})

var _ = Describe("Ready condition apply helpers", func() {
	var rvr *v1alpha1.ReplicatedVolumeReplica

	BeforeEach(func() {
		rvr = &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}
	})

	Describe("applyReadyCondTrue", func() {
		It("sets condition to True", func() {
			changed := applyReadyCondTrue(rvr, "TestReason", "Test")
			Expect(changed).To(BeTrue())
			cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondReadyType)
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		})
	})

	Describe("applyReadyCondFalse", func() {
		It("sets condition to False", func() {
			changed := applyReadyCondFalse(rvr, "TestReason", "Test")
			Expect(changed).To(BeTrue())
			cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondReadyType)
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		})
	})

	Describe("applyReadyCondUnknown", func() {
		It("sets condition to Unknown", func() {
			changed := applyReadyCondUnknown(rvr, "TestReason", "Test")
			Expect(changed).To(BeTrue())
			cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondReadyType)
			Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
		})
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Status field apply helpers tests
//

var _ = Describe("ensureConditionReady", func() {
	var (
		ctx context.Context
		rvr *v1alpha1.ReplicatedVolumeReplica
	)

	// validRV is a minimal ReplicatedVolume that passes all rv/datamesh prerequisites.
	validRV := &v1alpha1.ReplicatedVolume{
		Status: v1alpha1.ReplicatedVolumeStatus{
			DatameshRevision: 1,
			Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
				SystemNetworkNames: []string{"net-1"},
			},
		},
	}

	BeforeEach(func() {
		ctx = logr.NewContext(context.Background(), logr.Discard())
		rvr = &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				NodeName: "node-1",
			},
		}
	})

	It("sets Ready=False Deleting when RVR should not exist", func() {
		now := metav1.Now()
		rvr.DeletionTimestamp = &now
		rvr.Finalizers = []string{v1alpha1.RVRControllerFinalizer}

		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Quorum: boolPtr(true),
			},
		}

		outcome := ensureConditionReady(ctx, rvr, nil, drbdr, nil, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondReadyType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondReadyReasonDeleting))
	})

	It("sets Ready=Unknown WaitingForReplicatedVolume when drbdr is nil and rv is nil", func() {
		outcome := ensureConditionReady(ctx, rvr, nil, nil, nil, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondReadyType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondReadyReasonWaitingForReplicatedVolume))
	})

	It("sets Ready=Unknown WaitingForReplicatedVolume when drbdr is nil and datamesh revision is 0", func() {
		rv := &v1alpha1.ReplicatedVolume{}

		outcome := ensureConditionReady(ctx, rvr, rv, nil, nil, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondReadyType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondReadyReasonWaitingForReplicatedVolume))
	})

	It("sets Ready=False PendingScheduling when drbdr is nil and node not assigned", func() {
		rvr.Spec.NodeName = "" // Override BeforeEach default.

		outcome := ensureConditionReady(ctx, rvr, validRV, nil, nil, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondReadyType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondReadyReasonPendingScheduling))
	})

	It("sets Ready=Unknown AgentNotReady when agent not ready", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Quorum: boolPtr(true),
			},
		}

		outcome := ensureConditionReady(ctx, rvr, validRV, drbdr, nil, false, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondReadyType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondReadyReasonAgentNotReady))
	})

	It("sets Ready=Unknown ApplyingConfiguration when config pending", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Quorum: boolPtr(true),
			},
		}

		outcome := ensureConditionReady(ctx, rvr, validRV, drbdr, nil, true, true)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondReadyType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondReadyReasonApplyingConfiguration))
	})

	It("sets Ready=False QuorumLost when quorum is nil", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Quorum: nil,
			},
		}
		member := &v1alpha1.DatameshMember{Name: "rvr-1"}
		rvr.Status.QuorumSummary = &v1alpha1.ReplicatedVolumeReplicaStatusQuorumSummary{
			Quorum: intPtr(1),
		}

		outcome := ensureConditionReady(ctx, rvr, validRV, drbdr, member, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondReadyType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondReadyReasonQuorumLost))
	})

	It("sets Ready=False QuorumLost when quorum is false", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Quorum: boolPtr(false),
			},
		}
		member := &v1alpha1.DatameshMember{Name: "rvr-1"}
		rvr.Status.QuorumSummary = &v1alpha1.ReplicatedVolumeReplicaStatusQuorumSummary{
			Quorum: intPtr(1),
		}

		outcome := ensureConditionReady(ctx, rvr, validRV, drbdr, member, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondReadyType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondReadyReasonQuorumLost))
	})

	It("sets Ready=True Ready when quorum is true", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Quorum: boolPtr(true),
			},
		}
		member := &v1alpha1.DatameshMember{Name: "rvr-1"}
		rvr.Status.QuorumSummary = &v1alpha1.ReplicatedVolumeReplicaStatusQuorumSummary{
			Quorum: intPtr(1),
		}

		outcome := ensureConditionReady(ctx, rvr, validRV, drbdr, member, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondReadyType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondReadyReasonReady))
	})

	It("returns no change when already in sync", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Quorum: boolPtr(true),
			},
		}
		member := &v1alpha1.DatameshMember{Name: "rvr-1"}
		rvr.Status.QuorumSummary = &v1alpha1.ReplicatedVolumeReplicaStatusQuorumSummary{
			Quorum: intPtr(1),
		}

		// First call
		outcome1 := ensureConditionReady(ctx, rvr, validRV, drbdr, member, true, false)
		Expect(outcome1.Error()).NotTo(HaveOccurred())
		Expect(outcome1.DidChange()).To(BeTrue())

		// Second call should report no change
		outcome2 := ensureConditionReady(ctx, rvr, validRV, drbdr, member, true, false)
		Expect(outcome2.Error()).NotTo(HaveOccurred())
		Expect(outcome2.DidChange()).To(BeFalse())
	})

	It("sets Ready=False PendingDatameshJoin when quorum sentinel (non-member)", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Quorum: boolPtr(false),
			},
		}
		rvr.Status.QuorumSummary = &v1alpha1.ReplicatedVolumeReplicaStatusQuorumSummary{
			Quorum:                   intPtr(32),
			QuorumMinimumRedundancy:  intPtr(32),
			ConnectedDiskfulPeers:    0,
			ConnectedTieBreakerPeers: 0,
			ConnectedUpToDatePeers:   0,
		}

		outcome := ensureConditionReady(ctx, rvr, validRV, drbdr, nil, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondReadyType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondReadyReasonPendingDatameshJoin))
		Expect(cond.Message).To(ContainSubstring("Waiting to join datamesh"))
	})

	It("sets Ready=False Deleting when non-member and DeletionTimestamp set", func() {
		now := metav1.Now()
		rvr.DeletionTimestamp = &now
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Quorum: boolPtr(false),
			},
		}
		rvr.Status.QuorumSummary = &v1alpha1.ReplicatedVolumeReplicaStatusQuorumSummary{
			Quorum:                  intPtr(32),
			QuorumMinimumRedundancy: intPtr(32),
		}

		outcome := ensureConditionReady(ctx, rvr, validRV, drbdr, nil, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondReadyType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondReadyReasonDeleting))
		Expect(cond.Message).To(ContainSubstring("being deleted"))
	})

	It("sets Ready=True QuorumViaPeers when diskless member has quorum", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Quorum: boolPtr(true),
			},
		}
		rvr.Status.Type = v1alpha1.DRBDResourceTypeDiskless
		rvr.Status.QuorumSummary = &v1alpha1.ReplicatedVolumeReplicaStatusQuorumSummary{
			Quorum:                   intPtr(32),
			QuorumMinimumRedundancy:  intPtr(1),
			ConnectedDiskfulPeers:    2,
			ConnectedTieBreakerPeers: 0,
			ConnectedUpToDatePeers:   2,
		}
		member := &v1alpha1.DatameshMember{Name: "rvr-1"}

		outcome := ensureConditionReady(ctx, rvr, validRV, drbdr, member, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondReadyType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondReadyReasonQuorumViaPeers))
		Expect(cond.Message).To(ContainSubstring("data quorum: 2/1"))
	})

	It("sets Ready=False QuorumViaPeers when diskless member lost quorum", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Quorum: boolPtr(false),
			},
		}
		rvr.Status.Type = v1alpha1.DRBDResourceTypeDiskless
		rvr.Status.QuorumSummary = &v1alpha1.ReplicatedVolumeReplicaStatusQuorumSummary{
			Quorum:                   intPtr(32),
			QuorumMinimumRedundancy:  intPtr(2),
			ConnectedDiskfulPeers:    1,
			ConnectedTieBreakerPeers: 0,
			ConnectedUpToDatePeers:   0,
		}
		member := &v1alpha1.DatameshMember{Name: "rvr-1"}

		outcome := ensureConditionReady(ctx, rvr, validRV, drbdr, member, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondReadyType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondReadyReasonQuorumViaPeers))
		Expect(cond.Message).To(ContainSubstring("data quorum: 0/2"))
	})

	It("sets Ready=Unknown WaitingForReplicatedVolume when no system networks", func() {
		rvNoNetworks := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshRevision: 1,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					SystemNetworkNames: nil, // no system networks
				},
			},
		}
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Quorum: boolPtr(true),
			},
		}

		outcome := ensureConditionReady(ctx, rvr, rvNoNetworks, drbdr, nil, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondReadyType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondReadyReasonWaitingForReplicatedVolume))
		Expect(cond.Message).To(ContainSubstring("system networks"))
	})

	It("counts self as diskful vote and UpToDate vote in quorum message", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Quorum: boolPtr(true),
			},
		}
		member := &v1alpha1.DatameshMember{Name: "rvr-1"}
		// Diskful member with UpToDate backing volume — self counts as both diskful and UpToDate vote.
		rvr.Status.Type = v1alpha1.DRBDResourceTypeDiskful
		rvr.Status.BackingVolume = &v1alpha1.ReplicatedVolumeReplicaStatusBackingVolume{
			State: v1alpha1.DiskStateUpToDate,
		}
		rvr.Status.QuorumSummary = &v1alpha1.ReplicatedVolumeReplicaStatusQuorumSummary{
			Quorum:                  intPtr(2),
			QuorumMinimumRedundancy: intPtr(1),
			ConnectedDiskfulPeers:   1,
			ConnectedUpToDatePeers:  1,
		}

		outcome := ensureConditionReady(ctx, rvr, validRV, drbdr, member, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondReadyType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		// 1 connected peer + 1 self = 2 diskful, 1 connected UpToDate peer + 1 self = 2 UpToDate
		Expect(cond.Message).To(ContainSubstring("diskful 2/2"))
		Expect(cond.Message).To(ContainSubstring("data quorum: 2/1"))
	})

	It("includes tie-breaker peers in quorum message", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				Quorum: boolPtr(true),
			},
		}
		member := &v1alpha1.DatameshMember{Name: "rvr-1"}
		rvr.Status.Type = v1alpha1.DRBDResourceTypeDiskful
		rvr.Status.BackingVolume = &v1alpha1.ReplicatedVolumeReplicaStatusBackingVolume{
			State: v1alpha1.DiskStateUpToDate,
		}
		rvr.Status.QuorumSummary = &v1alpha1.ReplicatedVolumeReplicaStatusQuorumSummary{
			Quorum:                   intPtr(2),
			QuorumMinimumRedundancy:  intPtr(1),
			ConnectedDiskfulPeers:    0,
			ConnectedTieBreakerPeers: 1, // tie-breaker peer
			ConnectedUpToDatePeers:   0,
		}

		outcome := ensureConditionReady(ctx, rvr, validRV, drbdr, member, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondReadyType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Message).To(ContainSubstring("+ tie-breakers 1"))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// ensureStatusBackingVolume tests
//

var _ = Describe("ensureConditionBackingVolumeUpToDate", func() {
	var (
		ctx context.Context
		rvr *v1alpha1.ReplicatedVolumeReplica
	)

	BeforeEach(func() {
		ctx = logr.NewContext(context.Background(), logr.Discard())
		rvr = &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				Type: v1alpha1.ReplicaTypeDiskful,
			},
		}
	})

	It("removes condition when drbdr is nil", func() {
		obju.SetStatusCondition(rvr, metav1.Condition{
			Type:   v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateType,
			Status: metav1.ConditionTrue,
			Reason: "PreviousReason",
		})

		member := &v1alpha1.DatameshMember{
			Name: "rvr-1",
			Type: v1alpha1.DatameshMemberTypeDiskful,
		}

		outcome := ensureConditionBackingVolumeUpToDate(ctx, rvr, nil, member, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeTrue())
		Expect(obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateType)).To(BeNil())
	})

	It("removes condition when not a datamesh member", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				DiskState: v1alpha1.DiskStateUpToDate,
			},
		}

		outcome := ensureConditionBackingVolumeUpToDate(ctx, rvr, drbdr, nil, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateType)).To(BeNil())
	})

	It("removes condition when effectiveType is not Diskful", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				DiskState: v1alpha1.DiskStateUpToDate,
			},
		}
		member := &v1alpha1.DatameshMember{
			Name: "rvr-1",
			Type: v1alpha1.DatameshMemberTypeTieBreaker, // not Diskful
		}

		outcome := ensureConditionBackingVolumeUpToDate(ctx, rvr, drbdr, member, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateType)).To(BeNil())
	})

	It("sets Unknown when agent not ready", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				DiskState: v1alpha1.DiskStateUpToDate,
			},
		}
		member := &v1alpha1.DatameshMember{
			Name: "rvr-1",
			Type: v1alpha1.DatameshMemberTypeDiskful,
		}

		outcome := ensureConditionBackingVolumeUpToDate(ctx, rvr, drbdr, member, false, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonAgentNotReady))
	})

	It("sets Unknown when configuration pending", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				DiskState: v1alpha1.DiskStateUpToDate,
			},
		}
		member := &v1alpha1.DatameshMember{
			Name: "rvr-1",
			Type: v1alpha1.DatameshMemberTypeDiskful,
		}

		outcome := ensureConditionBackingVolumeUpToDate(ctx, rvr, drbdr, member, true, true)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonApplyingConfiguration))
	})

	It("sets True with InSync for DiskStateUpToDate when attached", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				DiskState:         v1alpha1.DiskStateUpToDate,
				DeviceIOSuspended: boolPtr(false),
				ActiveConfiguration: &v1alpha1.DRBDResourceActiveConfiguration{
					Role: v1alpha1.DRBDRolePrimary, // attached
				},
			},
		}
		member := &v1alpha1.DatameshMember{
			Name: "rvr-1",
			Type: v1alpha1.DatameshMemberTypeDiskful,
		}

		outcome := ensureConditionBackingVolumeUpToDate(ctx, rvr, drbdr, member, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonUpToDate))
		Expect(cond.Message).To(ContainSubstring("served locally"))
	})

	It("sets True with InSync for DiskStateUpToDate when not attached", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				DiskState: v1alpha1.DiskStateUpToDate,
				ActiveConfiguration: &v1alpha1.DRBDResourceActiveConfiguration{
					Role: v1alpha1.DRBDRoleSecondary,
				},
			},
		}
		member := &v1alpha1.DatameshMember{
			Name: "rvr-1",
			Type: v1alpha1.DatameshMemberTypeDiskful,
		}

		outcome := ensureConditionBackingVolumeUpToDate(ctx, rvr, drbdr, member, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateType)
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonUpToDate))
		Expect(cond.Message).NotTo(ContainSubstring("served locally"))
	})

	It("sets False with NoDisk for DiskStateDiskless when attached", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				DiskState:         v1alpha1.DiskStateDiskless,
				DeviceIOSuspended: boolPtr(false),
				ActiveConfiguration: &v1alpha1.DRBDResourceActiveConfiguration{
					Role: v1alpha1.DRBDRolePrimary,
				},
			},
		}
		member := &v1alpha1.DatameshMember{
			Name: "rvr-1",
			Type: v1alpha1.DatameshMemberTypeDiskful,
		}

		outcome := ensureConditionBackingVolumeUpToDate(ctx, rvr, drbdr, member, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateType)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonAbsent))
		Expect(cond.Message).To(ContainSubstring("forwarded to peers"))
	})

	It("sets False with Attaching for DiskStateAttaching", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				DiskState: v1alpha1.DiskStateAttaching,
				ActiveConfiguration: &v1alpha1.DRBDResourceActiveConfiguration{
					Role: v1alpha1.DRBDRoleSecondary,
				},
			},
		}
		member := &v1alpha1.DatameshMember{
			Name: "rvr-1",
			Type: v1alpha1.DatameshMemberTypeDiskful,
		}

		outcome := ensureConditionBackingVolumeUpToDate(ctx, rvr, drbdr, member, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateType)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonAbsent))
	})

	It("sets False with Detaching for DiskStateDetaching", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				DiskState: v1alpha1.DiskStateDetaching,
				ActiveConfiguration: &v1alpha1.DRBDResourceActiveConfiguration{
					Role: v1alpha1.DRBDRolePrimary,
				},
			},
		}
		member := &v1alpha1.DatameshMember{
			Name: "rvr-1",
			Type: v1alpha1.DatameshMemberTypeDiskful,
		}

		outcome := ensureConditionBackingVolumeUpToDate(ctx, rvr, drbdr, member, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateType)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonAbsent))
	})

	It("sets False with DiskFailed for DiskStateFailed", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				DiskState: v1alpha1.DiskStateFailed,
				ActiveConfiguration: &v1alpha1.DRBDResourceActiveConfiguration{
					Role: v1alpha1.DRBDRoleSecondary,
				},
			},
		}
		member := &v1alpha1.DatameshMember{
			Name: "rvr-1",
			Type: v1alpha1.DatameshMemberTypeDiskful,
		}

		outcome := ensureConditionBackingVolumeUpToDate(ctx, rvr, drbdr, member, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateType)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonFailed))
	})

	It("sets SynchronizationBlocked when no peer with up-to-date data", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				DiskState: v1alpha1.DiskStateInconsistent,
				ActiveConfiguration: &v1alpha1.DRBDResourceActiveConfiguration{
					Role: v1alpha1.DRBDRoleSecondary,
				},
			},
		}
		member := &v1alpha1.DatameshMember{
			Name: "rvr-1",
			Type: v1alpha1.DatameshMemberTypeDiskful,
		}
		// No peers with UpToDate disk
		rvr.Status.Peers = []v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{
			{Name: "peer-1", BackingVolumeState: v1alpha1.DiskStateInconsistent},
		}

		outcome := ensureConditionBackingVolumeUpToDate(ctx, rvr, drbdr, member, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateType)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonRequiresSynchronization))
		Expect(cond.Message).To(ContainSubstring("no up-to-date peers are available"))
	})

	It("sets RequiresSynchronization for DiskStateOutdated", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				DiskState: v1alpha1.DiskStateOutdated,
				ActiveConfiguration: &v1alpha1.DRBDResourceActiveConfiguration{
					Role: v1alpha1.DRBDRoleSecondary,
				},
			},
		}
		member := &v1alpha1.DatameshMember{
			Name: "rvr-1",
			Type: v1alpha1.DatameshMemberTypeDiskful,
		}

		outcome := ensureConditionBackingVolumeUpToDate(ctx, rvr, drbdr, member, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateType)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonRequiresSynchronization))
		Expect(cond.Message).To(ContainSubstring("outdated"))
	})

	It("sets sync message for Inconsistent with connected up-to-date peer", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				DiskState: v1alpha1.DiskStateInconsistent,
				ActiveConfiguration: &v1alpha1.DRBDResourceActiveConfiguration{
					Role: v1alpha1.DRBDRoleSecondary,
				},
			},
		}
		member := &v1alpha1.DatameshMember{
			Name: "rvr-1",
			Type: v1alpha1.DatameshMemberTypeDiskful,
		}
		rvr.Status.Peers = []v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{
			{
				Name:               "peer-1",
				BackingVolumeState: v1alpha1.DiskStateUpToDate,
				ConnectionState:    v1alpha1.ConnectionStateConnected,
			},
		}

		outcome := ensureConditionBackingVolumeUpToDate(ctx, rvr, drbdr, member, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateType)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonRequiresSynchronization))
		Expect(cond.Message).To(ContainSubstring("requires synchronization from an up-to-date peer"))
	})

	It("sets Unknown disk state message for unrecognized state", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				DiskState: "SomeUnknownState",
				ActiveConfiguration: &v1alpha1.DRBDResourceActiveConfiguration{
					Role: v1alpha1.DRBDRoleSecondary,
				},
			},
		}
		member := &v1alpha1.DatameshMember{
			Name: "rvr-1",
			Type: v1alpha1.DatameshMemberTypeDiskful,
		}

		outcome := ensureConditionBackingVolumeUpToDate(ctx, rvr, drbdr, member, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateType)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonUnknown))
		Expect(cond.Message).To(ContainSubstring("SomeUnknownState"))
	})

	It("returns no change when already in sync", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				DiskState: v1alpha1.DiskStateUpToDate,
				ActiveConfiguration: &v1alpha1.DRBDResourceActiveConfiguration{
					Role: v1alpha1.DRBDRoleSecondary,
				},
			},
		}
		member := &v1alpha1.DatameshMember{
			Name: "rvr-1",
			Type: v1alpha1.DatameshMemberTypeDiskful,
		}

		// First call
		outcome1 := ensureConditionBackingVolumeUpToDate(ctx, rvr, drbdr, member, true, false)
		Expect(outcome1.Error()).NotTo(HaveOccurred())
		Expect(outcome1.DidChange()).To(BeTrue())

		// Second call should report no change
		outcome2 := ensureConditionBackingVolumeUpToDate(ctx, rvr, drbdr, member, true, false)
		Expect(outcome2.Error()).NotTo(HaveOccurred())
		Expect(outcome2.DidChange()).To(BeFalse())
	})

	It("returns error when ActiveConfiguration is nil after configuration is no longer pending", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				DiskState:           v1alpha1.DiskStateUpToDate,
				ActiveConfiguration: nil,
			},
		}
		member := &v1alpha1.DatameshMember{
			Name: "rvr-1",
			Type: v1alpha1.DatameshMemberTypeDiskful,
		}

		outcome := ensureConditionBackingVolumeUpToDate(ctx, rvr, drbdr, member, true, false)

		Expect(outcome.Error()).To(HaveOccurred())
		Expect(outcome.Error().Error()).To(ContainSubstring("ActiveConfiguration is nil"))
	})

	It("sets Synchronizing when Inconsistent with SyncTarget peer", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				DiskState: v1alpha1.DiskStateInconsistent,
				ActiveConfiguration: &v1alpha1.DRBDResourceActiveConfiguration{
					Role: v1alpha1.DRBDRoleSecondary,
				},
			},
		}
		member := &v1alpha1.DatameshMember{
			Name: "rvr-1",
			Type: v1alpha1.DatameshMemberTypeDiskful,
		}
		rvr.Status.Peers = []v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{
			{
				Name:             "peer-sync",
				ReplicationState: v1alpha1.ReplicationStateSyncTarget,
			},
		}

		outcome := ensureConditionBackingVolumeUpToDate(ctx, rvr, drbdr, member, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateType)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonSynchronizing))
		Expect(cond.Message).To(ContainSubstring("peer-sync"))
	})

	It("sets Synchronizing via intermediate peer when Inconsistent with Established UpToDate peer", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				DiskState: v1alpha1.DiskStateInconsistent,
				ActiveConfiguration: &v1alpha1.DRBDResourceActiveConfiguration{
					Role: v1alpha1.DRBDRoleSecondary,
				},
			},
		}
		member := &v1alpha1.DatameshMember{
			Name: "rvr-1",
			Type: v1alpha1.DatameshMemberTypeDiskful,
		}
		rvr.Status.Peers = []v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{
			{
				Name:               "peer-intermediate",
				BackingVolumeState: v1alpha1.DiskStateUpToDate,
				ReplicationState:   v1alpha1.ReplicationStateEstablished,
			},
		}

		outcome := ensureConditionBackingVolumeUpToDate(ctx, rvr, drbdr, member, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateType)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonSynchronizing))
		Expect(cond.Message).To(ContainSubstring("intermediate peer"))
		Expect(cond.Message).To(ContainSubstring("peer-intermediate"))
	})

	It("sets Unknown for Negotiating disk state", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				DiskState: v1alpha1.DiskStateNegotiating,
				ActiveConfiguration: &v1alpha1.DRBDResourceActiveConfiguration{
					Role: v1alpha1.DRBDRoleSecondary,
				},
			},
		}
		member := &v1alpha1.DatameshMember{
			Name: "rvr-1",
			Type: v1alpha1.DatameshMemberTypeDiskful,
		}

		outcome := ensureConditionBackingVolumeUpToDate(ctx, rvr, drbdr, member, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateType)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonUnknown))
		Expect(cond.Message).To(ContainSubstring("negotiating"))
	})

	It("sets Unknown for Consistent disk state", func() {
		drbdr := &v1alpha1.DRBDResource{
			Status: v1alpha1.DRBDResourceStatus{
				DiskState: v1alpha1.DiskStateConsistent,
				ActiveConfiguration: &v1alpha1.DRBDResourceActiveConfiguration{
					Role: v1alpha1.DRBDRoleSecondary,
				},
			},
		}
		member := &v1alpha1.DatameshMember{
			Name: "rvr-1",
			Type: v1alpha1.DatameshMemberTypeDiskful,
		}

		outcome := ensureConditionBackingVolumeUpToDate(ctx, rvr, drbdr, member, true, false)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateType)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonUnknown))
		Expect(cond.Message).To(ContainSubstring("peer connection required"))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Test helpers
//

func boolPtr(b bool) *bool {
	return &b
}

func intPtr(i int) *int {
	return &i
}

func bytePtr(b byte) *byte {
	return &b
}

type testError struct {
	message string
}

func (e *testError) Error() string {
	return e.message
}

func newTestError(msg string) error {
	return &testError{message: msg}
}
