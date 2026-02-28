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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// ──────────────────────────────────────────────────────────────────────────────
// computeRVAAttachedCondition
//

var _ = Describe("computeRVAAttachedConditionFromAttachmentsSummary", func() {
	It("returns Attached when fully attached", func() {
		as := &attachmentState{
			intent:           attachmentIntentAttach,
			member:           &v1alpha1.DatameshMember{Attached: true, Type: v1alpha1.DatameshMemberTypeDiskful},
			conditionReason:  v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttached,
			conditionMessage: "Volume is attached and ready to serve I/O on the node",
		}
		cond := computeRVAAttachedConditionFromAttachmentsSummary(as)
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttached))
	})

	It("returns Attaching when attach transition is active", func() {
		as := &attachmentState{
			intent:                    attachmentIntentAttach,
			member:                    &v1alpha1.DatameshMember{Attached: true},
			hasActiveAttachTransition: true,
			conditionMessage:          "Attaching, 0/1 confirmed",
			conditionReason:           v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttaching,
		}
		cond := computeRVAAttachedConditionFromAttachmentsSummary(as)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttaching))
		Expect(cond.Message).To(ContainSubstring("Attaching"))
	})

	It("returns Detaching when detach transition is active", func() {
		as := &attachmentState{
			intent:                    attachmentIntentDetach,
			member:                    &v1alpha1.DatameshMember{Attached: false},
			hasActiveDetachTransition: true,
			conditionMessage:          "Detaching, 0/1 confirmed",
			conditionReason:           v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonDetaching,
		}
		cond := computeRVAAttachedConditionFromAttachmentsSummary(as)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonDetaching))
	})

	It("returns Pending when slot is full", func() {
		as := &attachmentState{
			intent:           attachmentIntentPending,
			conditionMessage: "Waiting for attachment slot (slots occupied 2/2)",
			conditionReason:  v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonPending,
		}
		cond := computeRVAAttachedConditionFromAttachmentsSummary(as)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonPending))
	})

	It("returns Pending when attach blocked by quorum", func() {
		as := &attachmentState{
			intent:           attachmentIntentPending,
			conditionMessage: "Quorum not satisfied (1/2 replicas with quorum)",
			conditionReason:  v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonPending,
		}
		cond := computeRVAAttachedConditionFromAttachmentsSummary(as)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonPending))
	})

	It("returns VolumeDeleting when attach blocked by deletion", func() {
		as := &attachmentState{
			intent:           attachmentIntentPending,
			conditionMessage: "Volume is being deleted",
			conditionReason:  v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonReplicatedVolumeDeleting,
		}
		cond := computeRVAAttachedConditionFromAttachmentsSummary(as)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonReplicatedVolumeDeleting))
	})

	It("returns NodeNotEligible when node not in RSP", func() {
		as := &attachmentState{
			intent:           attachmentIntentPending,
			conditionMessage: "Node is not eligible for pool pool-1",
			conditionReason:  v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonNodeNotEligible,
		}
		cond := computeRVAAttachedConditionFromAttachmentsSummary(as)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonNodeNotEligible))
	})

	It("returns Pending when node not ready", func() {
		as := &attachmentState{
			intent:           attachmentIntentPending,
			conditionMessage: "Node is not ready",
			conditionReason:  v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonPending,
		}
		cond := computeRVAAttachedConditionFromAttachmentsSummary(as)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonPending))
	})

	It("returns Pending when agent not ready", func() {
		as := &attachmentState{
			intent:           attachmentIntentPending,
			conditionMessage: "Agent is not ready on node",
			conditionReason:  v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonPending,
		}
		cond := computeRVAAttachedConditionFromAttachmentsSummary(as)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonPending))
	})

	It("returns WaitingForReplica when no replica on node", func() {
		as := &attachmentState{
			intent:           attachmentIntentPending,
			conditionMessage: "Waiting for replica on node",
			conditionReason:  v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonWaitingForReplica,
		}
		cond := computeRVAAttachedConditionFromAttachmentsSummary(as)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonWaitingForReplica))
	})

	It("returns WaitingForReplica when replica joining datamesh", func() {
		as := &attachmentState{
			intent:           attachmentIntentPending,
			conditionMessage: "Waiting for replica [#3] to join datamesh",
			conditionReason:  v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonWaitingForReplica,
		}
		cond := computeRVAAttachedConditionFromAttachmentsSummary(as)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonWaitingForReplica))
	})

	It("returns WaitingForReplica when RVR not ready", func() {
		as := &attachmentState{
			intent:           attachmentIntentAttach,
			member:           &v1alpha1.DatameshMember{Attached: false},
			conditionMessage: "Waiting for replica to become Ready",
			conditionReason:  v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonWaitingForReplica,
		}
		cond := computeRVAAttachedConditionFromAttachmentsSummary(as)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonWaitingForReplica))
	})

	It("returns Attaching when waiting for multiattach", func() {
		as := &attachmentState{
			intent:           attachmentIntentAttach,
			member:           &v1alpha1.DatameshMember{Attached: false},
			conditionMessage: "Waiting for multiattach to be enabled",
			conditionReason:  v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttaching,
		}
		cond := computeRVAAttachedConditionFromAttachmentsSummary(as)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttaching))
	})

	It("returns Pending when detach conflict blocks attach", func() {
		as := &attachmentState{
			intent:                    attachmentIntentAttach,
			member:                    &v1alpha1.DatameshMember{Attached: false},
			hasActiveDetachTransition: true,
			conditionMessage:          "Attach pending, waiting for detach to complete first",
			conditionReason:           v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonPending,
		}
		cond := computeRVAAttachedConditionFromAttachmentsSummary(as)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonPending))
	})

	It("returns Detaching when device in use blocks detach", func() {
		as := &attachmentState{
			intent:           attachmentIntentDetach,
			member:           &v1alpha1.DatameshMember{Attached: true},
			conditionMessage: "Device in use, detach blocked",
			conditionReason:  v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonDetaching,
		}
		cond := computeRVAAttachedConditionFromAttachmentsSummary(as)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonDetaching))
	})

	It("returns LocalityNotSatisfied when conditionReason is set by upstream flow", func() {
		as := &attachmentState{
			intent:           attachmentIntentPending,
			conditionMessage: "No Diskful replica on this node (volumeAccess is Local for storage class sc-1)",
			conditionReason:  v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonVolumeAccessLocalityNotSatisfied,
		}
		cond := computeRVAAttachedConditionFromAttachmentsSummary(as)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonVolumeAccessLocalityNotSatisfied))
		Expect(cond.Message).To(ContainSubstring("No Diskful replica"))
		Expect(cond.Message).To(ContainSubstring("Local"))
	})

	It("panics when conditionReason is empty", func() {
		as := &attachmentState{nodeName: "node-1"}
		Expect(func() { computeRVAAttachedConditionFromAttachmentsSummary(as) }).To(Panic())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// computeRVAReplicaReadyCondition
//

var _ = Describe("computeRVAReplicaReadyConditionFromAttachmentsSummary", func() {
	It("returns WaitingForReplica when attachmentState is nil", func() {
		cond := computeRVAReplicaReadyConditionFromAttachmentsSummary(nil)
		Expect(cond.Type).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondReplicaReadyType))
		Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondReplicaReadyReasonWaitingForReplica))
	})

	It("returns WaitingForReplica when RVR is nil", func() {
		as := &attachmentState{}
		cond := computeRVAReplicaReadyConditionFromAttachmentsSummary(as)
		Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondReplicaReadyReasonWaitingForReplica))
	})

	It("returns WaitingForReplica when RVR has no Ready condition", func() {
		as := &attachmentState{
			rvr: &v1alpha1.ReplicatedVolumeReplica{},
		}
		cond := computeRVAReplicaReadyConditionFromAttachmentsSummary(as)
		Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondReplicaReadyReasonWaitingForReplica))
	})

	It("mirrors RVR Ready=True condition", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{}
		rvr.Status.Conditions = []metav1.Condition{
			{
				Type:    v1alpha1.ReplicatedVolumeReplicaCondReadyType,
				Status:  metav1.ConditionTrue,
				Reason:  v1alpha1.ReplicatedVolumeReplicaCondReadyReasonReady,
				Message: "Ready for I/O",
			},
		}
		as := &attachmentState{rvr: rvr}
		cond := computeRVAReplicaReadyConditionFromAttachmentsSummary(as)
		Expect(cond.Type).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondReplicaReadyType))
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondReadyReasonReady))
		Expect(cond.Message).To(Equal("Ready for I/O"))
	})

	It("mirrors RVR Ready=False condition", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{}
		rvr.Status.Conditions = []metav1.Condition{
			{
				Type:    v1alpha1.ReplicatedVolumeReplicaCondReadyType,
				Status:  metav1.ConditionFalse,
				Reason:  v1alpha1.ReplicatedVolumeReplicaCondReadyReasonQuorumLost,
				Message: "Quorum is lost",
			},
		}
		as := &attachmentState{rvr: rvr}
		cond := computeRVAReplicaReadyConditionFromAttachmentsSummary(as)
		Expect(cond.Type).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondReplicaReadyType))
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondReadyReasonQuorumLost))
		Expect(cond.Message).To(Equal("Quorum is lost"))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// computeRVAReadyCondition
//

var _ = Describe("computeRVAReadyCondition", func() {
	It("returns Ready when both Attached and ReplicaReady are True and not deleting", func() {
		attached := metav1.Condition{Status: metav1.ConditionTrue}
		replicaReady := metav1.Condition{Status: metav1.ConditionTrue}
		cond := computeRVAReadyCondition(attached, replicaReady, false)
		Expect(cond.Type).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondReadyType))
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondReadyReasonReady))
	})

	It("returns Deleting when RVA is deleting", func() {
		attached := metav1.Condition{Status: metav1.ConditionTrue}
		replicaReady := metav1.Condition{Status: metav1.ConditionTrue}
		cond := computeRVAReadyCondition(attached, replicaReady, true)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondReadyReasonDeleting))
	})

	It("returns NotAttached when Attached is False", func() {
		attached := metav1.Condition{Status: metav1.ConditionFalse}
		replicaReady := metav1.Condition{Status: metav1.ConditionTrue}
		cond := computeRVAReadyCondition(attached, replicaReady, false)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondReadyReasonNotAttached))
		Expect(cond.Message).To(Equal("See Attached condition"))
	})

	It("returns ReplicaNotReady when Attached=True but ReplicaReady is False", func() {
		attached := metav1.Condition{Status: metav1.ConditionTrue}
		replicaReady := metav1.Condition{Status: metav1.ConditionFalse}
		cond := computeRVAReadyCondition(attached, replicaReady, false)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondReadyReasonReplicaNotReady))
		Expect(cond.Message).To(Equal("See ReplicaReady condition"))
	})

	It("returns ReplicaNotReady when both are not True (ReplicaReady checked first)", func() {
		attached := metav1.Condition{Status: metav1.ConditionFalse}
		replicaReady := metav1.Condition{Status: metav1.ConditionFalse}
		cond := computeRVAReadyCondition(attached, replicaReady, false)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondReadyReasonReplicaNotReady))
	})

	It("returns Unknown when Attached=True but ReplicaReady is Unknown", func() {
		attached := metav1.Condition{Status: metav1.ConditionTrue}
		replicaReady := metav1.Condition{Status: metav1.ConditionUnknown}
		cond := computeRVAReadyCondition(attached, replicaReady, false)
		Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondReadyReasonReplicaNotReady))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// RVA finalizer helpers tests (moved from reconciler_test.go)
//

var _ = Describe("isNodeAttachedOrDetaching", func() {
	It("returns false when rv is nil", func() {
		Expect(isNodeAttachedOrDetaching(nil, "node-1")).To(BeFalse())
	})

	It("returns false when no members", func() {
		rv := &v1alpha1.ReplicatedVolume{}
		Expect(isNodeAttachedOrDetaching(rv, "node-1")).To(BeFalse())
	})

	It("returns true when member is attached on node", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-0", NodeName: "node-1", Attached: true},
					},
				},
			},
		}
		Expect(isNodeAttachedOrDetaching(rv, "node-1")).To(BeTrue())
	})

	It("returns false when member is not attached", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-0", NodeName: "node-1", Attached: false},
					},
				},
			},
		}
		Expect(isNodeAttachedOrDetaching(rv, "node-1")).To(BeFalse())
	})

	It("returns false when attached member on different node", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-0", NodeName: "node-2", Attached: true},
					},
				},
			},
		}
		Expect(isNodeAttachedOrDetaching(rv, "node-1")).To(BeFalse())
	})

	It("returns true when detach transition targets this node", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-0", NodeName: "node-1", Attached: false},
					},
				},
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach, ReplicaName: "rv-1-0", Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, StartedAt: ptr.To(metav1.Now())}}},
				},
			},
		}
		Expect(isNodeAttachedOrDetaching(rv, "node-1")).To(BeTrue())
	})

	It("returns false when attach transition (not detach)", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-0", NodeName: "node-1", Attached: false},
					},
				},
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach, ReplicaName: "rv-1-0", Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, StartedAt: ptr.To(metav1.Now())}}},
				},
			},
		}
		Expect(isNodeAttachedOrDetaching(rv, "node-1")).To(BeFalse())
	})

	It("returns false when detach transition targets different node", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-0", NodeName: "node-2", Attached: false},
					},
				},
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach, ReplicaName: "rv-1-0", Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, StartedAt: ptr.To(metav1.Now())}}},
				},
			},
		}
		Expect(isNodeAttachedOrDetaching(rv, "node-1")).To(BeFalse())
	})
})

var _ = Describe("hasOtherNonDeletingRVAOnNode", func() {
	It("returns false when no other RVAs", func() {
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{
			{ObjectMeta: metav1.ObjectMeta{Name: "rva-1"}, Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{NodeName: "node-1"}},
		}
		Expect(hasOtherNonDeletingRVAOnNode(rvas, "node-1", "rva-1")).To(BeFalse())
	})

	It("returns true when another active RVA exists on same node", func() {
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{
			{ObjectMeta: metav1.ObjectMeta{Name: "rva-1"}, Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{NodeName: "node-1"}},
			{ObjectMeta: metav1.ObjectMeta{Name: "rva-2"}, Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{NodeName: "node-1"}},
		}
		Expect(hasOtherNonDeletingRVAOnNode(rvas, "node-1", "rva-1")).To(BeTrue())
	})

	It("returns false when other RVA is deleting", func() {
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{
			{ObjectMeta: metav1.ObjectMeta{Name: "rva-1"}, Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{NodeName: "node-1"}},
			{ObjectMeta: metav1.ObjectMeta{Name: "rva-2", DeletionTimestamp: ptr.To(metav1.Now())}, Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{NodeName: "node-1"}},
		}
		Expect(hasOtherNonDeletingRVAOnNode(rvas, "node-1", "rva-1")).To(BeFalse())
	})

	It("returns false when other RVA is on different node", func() {
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{
			{ObjectMeta: metav1.ObjectMeta{Name: "rva-1"}, Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{NodeName: "node-1"}},
			{ObjectMeta: metav1.ObjectMeta{Name: "rva-2"}, Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{NodeName: "node-2"}},
		}
		Expect(hasOtherNonDeletingRVAOnNode(rvas, "node-1", "rva-1")).To(BeFalse())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// reconcileRVAFinalizers (moved from reconciler_test.go)
//

var _ = Describe("reconcileRVAFinalizers", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	})

	makeRVA := func(name, rvName, node string) *v1alpha1.ReplicatedVolumeAttachment {
		return &v1alpha1.ReplicatedVolumeAttachment{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
				ReplicatedVolumeName: rvName,
				NodeName:             node,
			},
		}
	}

	makeDeletingRVA := func(name, rvName, node string) *v1alpha1.ReplicatedVolumeAttachment {
		rva := makeRVA(name, rvName, node)
		rva.Finalizers = []string{v1alpha1.RVControllerFinalizer}
		rva.DeletionTimestamp = ptr.To(metav1.Now())
		return rva
	}

	It("adds finalizer to non-deleting RVA", func(ctx SpecContext) {
		rva := makeRVA("rva-1", "rv-1", "node-1")
		cl := newClientBuilder(scheme).WithObjects(rva).Build()
		rec := NewReconciler(cl, scheme)

		rvas := []*v1alpha1.ReplicatedVolumeAttachment{rva}
		outcome := rec.reconcileRVAFinalizers(ctx, &v1alpha1.ReplicatedVolume{}, rvas)
		Expect(outcome.Error()).NotTo(HaveOccurred())

		var updated v1alpha1.ReplicatedVolumeAttachment
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rva), &updated)).To(Succeed())
		Expect(updated.Finalizers).To(ContainElement(v1alpha1.RVControllerFinalizer))
	})

	It("skips non-deleting RVA that already has finalizer", func(ctx SpecContext) {
		rva := makeRVA("rva-1", "rv-1", "node-1")
		rva.Finalizers = []string{v1alpha1.RVControllerFinalizer}
		cl := newClientBuilder(scheme).WithObjects(rva).Build()
		rec := NewReconciler(cl, scheme)

		rvas := []*v1alpha1.ReplicatedVolumeAttachment{rva}
		outcome := rec.reconcileRVAFinalizers(ctx, &v1alpha1.ReplicatedVolume{}, rvas)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeFalse())
	})

	It("removes finalizer from deleting RVA when rv is nil", func(ctx SpecContext) {
		rva := makeDeletingRVA("rva-1", "rv-1", "node-1")
		cl := newClientBuilder(scheme).WithObjects(rva).Build()
		rec := NewReconciler(cl, scheme)

		rvas := []*v1alpha1.ReplicatedVolumeAttachment{rva}
		outcome := rec.reconcileRVAFinalizers(ctx, nil, rvas)
		Expect(outcome.Error()).NotTo(HaveOccurred())

		// After removing the last finalizer, the fake client finalizes the object (deletes it).
		var updated v1alpha1.ReplicatedVolumeAttachment
		err := cl.Get(ctx, client.ObjectKeyFromObject(rva), &updated)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	It("keeps finalizer on deleting RVA when node is attached", func(ctx SpecContext) {
		rva := makeDeletingRVA("rva-1", "rv-1", "node-1")
		cl := newClientBuilder(scheme).WithObjects(rva).Build()
		rec := NewReconciler(cl, scheme)

		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-0", NodeName: "node-1", Attached: true},
					},
				},
			},
		}

		rvas := []*v1alpha1.ReplicatedVolumeAttachment{rva}
		outcome := rec.reconcileRVAFinalizers(ctx, rv, rvas)
		Expect(outcome.Error()).NotTo(HaveOccurred())

		var updated v1alpha1.ReplicatedVolumeAttachment
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rva), &updated)).To(Succeed())
		Expect(updated.Finalizers).To(ContainElement(v1alpha1.RVControllerFinalizer))
	})

	It("removes finalizer from deleting RVA when node is attached but duplicate active RVA exists", func(ctx SpecContext) {
		rvaDeleting := makeDeletingRVA("rva-1", "rv-1", "node-1")
		rvaActive := makeRVA("rva-2", "rv-1", "node-1")
		cl := newClientBuilder(scheme).WithObjects(rvaDeleting, rvaActive).Build()
		rec := NewReconciler(cl, scheme)

		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-0", NodeName: "node-1", Attached: true},
					},
				},
			},
		}

		rvas := []*v1alpha1.ReplicatedVolumeAttachment{rvaDeleting, rvaActive}
		outcome := rec.reconcileRVAFinalizers(ctx, rv, rvas)
		Expect(outcome.Error()).NotTo(HaveOccurred())

		// Deleting RVA finalized (last finalizer removed → object deleted by fake client).
		var updated v1alpha1.ReplicatedVolumeAttachment
		err := cl.Get(ctx, client.ObjectKeyFromObject(rvaDeleting), &updated)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())

		// Active RVA got finalizer added.
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvaActive), &updated)).To(Succeed())
		Expect(updated.Finalizers).To(ContainElement(v1alpha1.RVControllerFinalizer))
	})

	It("keeps finalizer on deleting RVA when detach transition in progress", func(ctx SpecContext) {
		rva := makeDeletingRVA("rva-1", "rv-1", "node-1")
		cl := newClientBuilder(scheme).WithObjects(rva).Build()
		rec := NewReconciler(cl, scheme)

		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-0", NodeName: "node-1", Attached: false},
					},
				},
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach, ReplicaName: "rv-1-0", Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, StartedAt: ptr.To(metav1.Now())}}},
				},
			},
		}

		rvas := []*v1alpha1.ReplicatedVolumeAttachment{rva}
		outcome := rec.reconcileRVAFinalizers(ctx, rv, rvas)
		Expect(outcome.Error()).NotTo(HaveOccurred())

		var updated v1alpha1.ReplicatedVolumeAttachment
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rva), &updated)).To(Succeed())
		Expect(updated.Finalizers).To(ContainElement(v1alpha1.RVControllerFinalizer))
	})

	It("removes finalizer from deleting RVA when node not attached and no detach transition", func(ctx SpecContext) {
		rva := makeDeletingRVA("rva-1", "rv-1", "node-1")
		cl := newClientBuilder(scheme).WithObjects(rva).Build()
		rec := NewReconciler(cl, scheme)

		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-0", NodeName: "node-1", Attached: false},
					},
				},
			},
		}

		rvas := []*v1alpha1.ReplicatedVolumeAttachment{rva}
		outcome := rec.reconcileRVAFinalizers(ctx, rv, rvas)
		Expect(outcome.Error()).NotTo(HaveOccurred())

		// Deleting RVA finalized (last finalizer removed → object deleted by fake client).
		var updated v1alpha1.ReplicatedVolumeAttachment
		err := cl.Get(ctx, client.ObjectKeyFromObject(rva), &updated)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// reconcileRVAWaiting
//

var _ = Describe("reconcileRVAWaiting", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	})

	const msg = "test message"

	It("patches RVA with wrong conditions", func(ctx SpecContext) {
		rva := &v1alpha1.ReplicatedVolumeAttachment{
			ObjectMeta: metav1.ObjectMeta{Name: "rva-1"},
			Status: v1alpha1.ReplicatedVolumeAttachmentStatus{
				Conditions: []metav1.Condition{
					{Type: v1alpha1.ReplicatedVolumeAttachmentCondAttachedType, Status: metav1.ConditionTrue, Reason: "Attached"},
					{Type: v1alpha1.ReplicatedVolumeAttachmentCondReadyType, Status: metav1.ConditionTrue, Reason: "Ready"},
					{Type: v1alpha1.ReplicatedVolumeAttachmentCondReplicaReadyType, Status: metav1.ConditionTrue, Reason: "Ready"},
				},
			},
		}
		cl := newClientBuilder(scheme).WithObjects(rva).WithStatusSubresource(rva).Build()
		rec := NewReconciler(cl, scheme)

		rvas := []*v1alpha1.ReplicatedVolumeAttachment{rva}
		outcome := rec.reconcileRVAWaiting(ctx, rvas, msg)
		Expect(outcome.Error()).NotTo(HaveOccurred())

		var updated v1alpha1.ReplicatedVolumeAttachment
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rva), &updated)).To(Succeed())

		Expect(obju.GetStatusCondition(&updated, v1alpha1.ReplicatedVolumeAttachmentCondReplicaReadyType)).To(BeNil())

		attached := obju.GetStatusCondition(&updated, v1alpha1.ReplicatedVolumeAttachmentCondAttachedType)
		Expect(attached).NotTo(BeNil())
		Expect(attached.Status).To(Equal(metav1.ConditionFalse))
		Expect(attached.Reason).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonWaitingForReplicatedVolume))
		Expect(attached.Message).To(Equal(msg))

		ready := obju.GetStatusCondition(&updated, v1alpha1.ReplicatedVolumeAttachmentCondReadyType)
		Expect(ready).NotTo(BeNil())
		Expect(ready.Status).To(Equal(metav1.ConditionFalse))
		Expect(ready.Reason).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondReadyReasonNotAttached))
		Expect(ready.Message).To(Equal(msg))
	})

	It("skips RVA already in sync", func(ctx SpecContext) {
		rva := &v1alpha1.ReplicatedVolumeAttachment{
			ObjectMeta: metav1.ObjectMeta{Name: "rva-1"},
			Status: v1alpha1.ReplicatedVolumeAttachmentStatus{
				Conditions: []metav1.Condition{
					{
						Type:    v1alpha1.ReplicatedVolumeAttachmentCondAttachedType,
						Status:  metav1.ConditionFalse,
						Reason:  v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonWaitingForReplicatedVolume,
						Message: msg,
					},
					{
						Type:    v1alpha1.ReplicatedVolumeAttachmentCondReadyType,
						Status:  metav1.ConditionFalse,
						Reason:  v1alpha1.ReplicatedVolumeAttachmentCondReadyReasonNotAttached,
						Message: msg,
					},
				},
			},
		}
		cl := newClientBuilder(scheme).WithObjects(rva).WithStatusSubresource(rva).Build()
		rec := NewReconciler(cl, scheme)

		rvas := []*v1alpha1.ReplicatedVolumeAttachment{rva}
		outcome := rec.reconcileRVAWaiting(ctx, rvas, msg)
		Expect(outcome.Error()).NotTo(HaveOccurred())

		// Verify resourceVersion did not change (no patch was issued).
		var updated v1alpha1.ReplicatedVolumeAttachment
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rva), &updated)).To(Succeed())
		Expect(updated.ResourceVersion).To(Equal(rva.ResourceVersion))
	})

	It("patches RVA when message differs", func(ctx SpecContext) {
		rva := &v1alpha1.ReplicatedVolumeAttachment{
			ObjectMeta: metav1.ObjectMeta{Name: "rva-1"},
			Status: v1alpha1.ReplicatedVolumeAttachmentStatus{
				Conditions: []metav1.Condition{
					{
						Type:    v1alpha1.ReplicatedVolumeAttachmentCondAttachedType,
						Status:  metav1.ConditionFalse,
						Reason:  v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonWaitingForReplicatedVolume,
						Message: "old message",
					},
					{
						Type:    v1alpha1.ReplicatedVolumeAttachmentCondReadyType,
						Status:  metav1.ConditionFalse,
						Reason:  v1alpha1.ReplicatedVolumeAttachmentCondReadyReasonNotAttached,
						Message: "old message",
					},
				},
			},
		}
		cl := newClientBuilder(scheme).WithObjects(rva).WithStatusSubresource(rva).Build()
		rec := NewReconciler(cl, scheme)

		rvas := []*v1alpha1.ReplicatedVolumeAttachment{rva}
		outcome := rec.reconcileRVAWaiting(ctx, rvas, msg)
		Expect(outcome.Error()).NotTo(HaveOccurred())

		var updated v1alpha1.ReplicatedVolumeAttachment
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rva), &updated)).To(Succeed())

		attached := obju.GetStatusCondition(&updated, v1alpha1.ReplicatedVolumeAttachmentCondAttachedType)
		Expect(attached).NotTo(BeNil())
		Expect(attached.Message).To(Equal(msg))
	})

	It("patches RVA when ReplicaReady condition is still present", func(ctx SpecContext) {
		rva := &v1alpha1.ReplicatedVolumeAttachment{
			ObjectMeta: metav1.ObjectMeta{Name: "rva-1"},
			Status: v1alpha1.ReplicatedVolumeAttachmentStatus{
				Conditions: []metav1.Condition{
					{
						Type:    v1alpha1.ReplicatedVolumeAttachmentCondAttachedType,
						Status:  metav1.ConditionFalse,
						Reason:  v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonWaitingForReplicatedVolume,
						Message: msg,
					},
					{
						Type:    v1alpha1.ReplicatedVolumeAttachmentCondReadyType,
						Status:  metav1.ConditionFalse,
						Reason:  v1alpha1.ReplicatedVolumeAttachmentCondReadyReasonNotAttached,
						Message: msg,
					},
					{
						Type:   v1alpha1.ReplicatedVolumeAttachmentCondReplicaReadyType,
						Status: metav1.ConditionUnknown,
						Reason: v1alpha1.ReplicatedVolumeAttachmentCondReplicaReadyReasonWaitingForReplica,
					},
				},
			},
		}
		cl := newClientBuilder(scheme).WithObjects(rva).WithStatusSubresource(rva).Build()
		rec := NewReconciler(cl, scheme)

		rvas := []*v1alpha1.ReplicatedVolumeAttachment{rva}
		outcome := rec.reconcileRVAWaiting(ctx, rvas, msg)
		Expect(outcome.Error()).NotTo(HaveOccurred())

		var updated v1alpha1.ReplicatedVolumeAttachment
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rva), &updated)).To(Succeed())
		Expect(obju.GetStatusCondition(&updated, v1alpha1.ReplicatedVolumeAttachmentCondReplicaReadyType)).To(BeNil())
	})

	It("clears attachment fields", func(ctx SpecContext) {
		rva := &v1alpha1.ReplicatedVolumeAttachment{
			ObjectMeta: metav1.ObjectMeta{Name: "rva-1"},
			Status: v1alpha1.ReplicatedVolumeAttachmentStatus{
				DevicePath:  "/dev/drbd1000",
				IOSuspended: ptr.To(false),
				InUse:       ptr.To(true),
			},
		}
		cl := newClientBuilder(scheme).WithObjects(rva).WithStatusSubresource(rva).Build()
		rec := NewReconciler(cl, scheme)

		rvas := []*v1alpha1.ReplicatedVolumeAttachment{rva}
		outcome := rec.reconcileRVAWaiting(ctx, rvas, msg)
		Expect(outcome.Error()).NotTo(HaveOccurred())

		var updated v1alpha1.ReplicatedVolumeAttachment
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rva), &updated)).To(Succeed())
		Expect(updated.Status.DevicePath).To(BeEmpty())
		Expect(updated.Status.IOSuspended).To(BeNil())
		Expect(updated.Status.InUse).To(BeNil())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// reconcileOrphanedRVAs
//

var _ = Describe("reconcileOrphanedRVAs", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	})

	It("sets waiting conditions on orphaned RVA", func(ctx SpecContext) {
		rva := &v1alpha1.ReplicatedVolumeAttachment{
			ObjectMeta: metav1.ObjectMeta{Name: "rva-1"},
			Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
				ReplicatedVolumeName: "rv-1",
				NodeName:             "node-1",
			},
		}

		cl := newClientBuilder(scheme).
			WithObjects(rva).
			WithStatusSubresource(rva).
			Build()
		rec := NewReconciler(cl, scheme)

		outcome := rec.reconcileOrphanedRVAs(ctx, "rv-1")
		Expect(outcome.Error()).NotTo(HaveOccurred())

		var updated v1alpha1.ReplicatedVolumeAttachment
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rva), &updated)).To(Succeed())

		cond := obju.GetStatusCondition(&updated, v1alpha1.ReplicatedVolumeAttachmentCondAttachedType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonWaitingForReplicatedVolume))
		Expect(cond.Message).To(ContainSubstring("not found"))
	})

	It("removes finalizer from deleting orphaned RVA", func(ctx SpecContext) {
		rva := &v1alpha1.ReplicatedVolumeAttachment{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "rva-1",
				DeletionTimestamp: ptr.To(metav1.Now()),
				Finalizers:        []string{v1alpha1.RVControllerFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
				ReplicatedVolumeName: "rv-1",
				NodeName:             "node-1",
			},
		}

		cl := newClientBuilder(scheme).
			WithObjects(rva).
			WithStatusSubresource(rva).
			Build()
		rec := NewReconciler(cl, scheme)

		outcome := rec.reconcileOrphanedRVAs(ctx, "rv-1")
		Expect(outcome.Error()).NotTo(HaveOccurred())

		// RVA should be finalized (finalizer removed → object deleted by fake client).
		var updated v1alpha1.ReplicatedVolumeAttachment
		err := cl.Get(ctx, client.ObjectKeyFromObject(rva), &updated)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	It("returns Done when no RVAs exist for the RV", func(ctx SpecContext) {
		cl := newClientBuilder(scheme).Build()
		rec := NewReconciler(cl, scheme)

		outcome := rec.reconcileOrphanedRVAs(ctx, "rv-1")
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.ShouldReturn()).To(BeTrue()) // Done is terminal.
	})

	It("sets conditions and removes finalizer in one call", func(ctx SpecContext) {
		// Deleting RVA with stale conditions — both conditions and finalizer should be handled.
		rva := &v1alpha1.ReplicatedVolumeAttachment{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "rva-1",
				DeletionTimestamp: ptr.To(metav1.Now()),
				Finalizers:        []string{v1alpha1.RVControllerFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
				ReplicatedVolumeName: "rv-1",
				NodeName:             "node-1",
			},
			Status: v1alpha1.ReplicatedVolumeAttachmentStatus{
				Conditions: []metav1.Condition{
					{Type: v1alpha1.ReplicatedVolumeAttachmentCondAttachedType, Status: metav1.ConditionTrue, Reason: "Attached"},
				},
			},
		}

		cl := newClientBuilder(scheme).
			WithObjects(rva).
			WithStatusSubresource(rva).
			Build()
		rec := NewReconciler(cl, scheme)

		outcome := rec.reconcileOrphanedRVAs(ctx, "rv-1")
		Expect(outcome.Error()).NotTo(HaveOccurred())

		// RVA should be finalized (conditions set + finalizer removed → object deleted).
		var updated v1alpha1.ReplicatedVolumeAttachment
		err := cl.Get(ctx, client.ObjectKeyFromObject(rva), &updated)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})
})
