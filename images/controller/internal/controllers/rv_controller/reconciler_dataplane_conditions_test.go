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
)

// newDataPlaneTestRV builds a minimal, formed RV at FTT=0, GMDR=0 with a fresh
// effective layout so it reports Ready/Resilient by default; entries mutate it
// toward the specific precedence rule under test.
func newDataPlaneTestRV() *v1alpha1.ReplicatedVolume {
	return &v1alpha1.ReplicatedVolume{
		Status: v1alpha1.ReplicatedVolumeStatus{
			DatameshRevision: 1,
			Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
				FailuresToTolerate:              0,
				GuaranteedMinimumDataRedundancy: 0,
			},
			EffectiveLayout: v1alpha1.ReplicatedVolumeEffectiveLayout{
				FailuresToTolerate:              ptr.To(int8(0)),
				GuaranteedMinimumDataRedundancy: ptr.To(int8(0)),
			},
		},
	}
}

// attachedReadyServing is the AttachedReady report of a volume whose consumers are all fine —
// the default input for the Ready precedence entries that are not about attachments.
func attachedReadyServing() attachedReadyReport {
	return attachedReadyReport{
		present: true,
		status:  metav1.ConditionTrue,
		reason:  v1alpha1.ReplicatedVolumeCondAttachedReadyReasonAttachedReady,
		message: "All 1 attachments are serving IO",
	}
}

// attachedReadyFrozen is the AttachedReady report of a volume with a frozen consumer.
func attachedReadyFrozen() attachedReadyReport {
	return attachedReadyReport{
		present: true,
		status:  metav1.ConditionFalse,
		reason:  v1alpha1.ReplicatedVolumeCondAttachedReadyReasonIOSuspended,
		message: "1/1 attachments are not serving IO: replica rv-r0 on node n0 (IO is suspended on the device)",
	}
}

// addAttachedMember appends an attached datamesh member to the RV.
func addAttachedMember(rv *v1alpha1.ReplicatedVolume, name, nodeName string) {
	rv.Status.Datamesh.Members = append(rv.Status.Datamesh.Members, v1alpha1.DatameshMember{
		Name:     name,
		NodeName: nodeName,
		Attached: true,
	})
}

// newTestRVRWithAttachedReason builds an RVR carrying only the Attached condition, which is the
// single input computeAttachmentVerdict reads.
func newTestRVRWithAttachedReason(name string, status metav1.ConditionStatus, reason string) *v1alpha1.ReplicatedVolumeReplica {
	return &v1alpha1.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: v1alpha1.ReplicatedVolumeReplicaStatus{
			Conditions: []metav1.Condition{{
				Type:   v1alpha1.ReplicatedVolumeReplicaCondAttachedType,
				Status: status,
				Reason: reason,
			}},
		},
	}
}

// newTestRVAOnNode builds a live consumer on the node.
func newTestRVAOnNode(name, nodeName string) *v1alpha1.ReplicatedVolumeAttachment {
	return &v1alpha1.ReplicatedVolumeAttachment{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       v1alpha1.ReplicatedVolumeAttachmentSpec{NodeName: nodeName},
	}
}

var _ = Describe("data-plane conditions", func() {
	Describe("computeReadyReport", func() {
		DescribeTable("follows the precedence order",
			func(mutate func(*v1alpha1.ReplicatedVolume), attached attachedReadyReport, wantStatus metav1.ConditionStatus, wantReason string) {
				rv := newDataPlaneTestRV()
				mutate(rv)
				report := computeReadyReport(rv, attached)
				Expect(report.status).To(Equal(wantStatus))
				Expect(report.reason).To(Equal(wantReason))
			},
			Entry("deletion timestamp set",
				func(rv *v1alpha1.ReplicatedVolume) { rv.DeletionTimestamp = ptr.To(metav1.Now()) },
				attachedReadyServing(),
				metav1.ConditionFalse, v1alpha1.ReplicatedVolumeCondReadyReasonTerminating),
			Entry("formation in progress",
				func(rv *v1alpha1.ReplicatedVolume) { rv.Status.DatameshRevision = 0 },
				attachedReadyServing(),
				metav1.ConditionFalse, v1alpha1.ReplicatedVolumeCondReadyReasonForming),
			Entry("effective FTT nil",
				func(rv *v1alpha1.ReplicatedVolume) { rv.Status.EffectiveLayout.FailuresToTolerate = nil },
				attachedReadyServing(),
				metav1.ConditionUnknown, v1alpha1.ReplicatedVolumeCondReadyReasonStatusUnknown),
			Entry("FTT < 0",
				func(rv *v1alpha1.ReplicatedVolume) {
					rv.Status.EffectiveLayout.FailuresToTolerate = ptr.To(int8(-1))
				},
				attachedReadyServing(),
				metav1.ConditionFalse, v1alpha1.ReplicatedVolumeCondReadyReasonQuorumLost),
			Entry("mesh is fine but the consumer is frozen",
				func(*v1alpha1.ReplicatedVolume) {},
				attachedReadyFrozen(),
				metav1.ConditionFalse, v1alpha1.ReplicatedVolumeCondReadyReasonAttachmentNotReady),
			Entry("FTT >= 0 and GMDR >= 0",
				func(*v1alpha1.ReplicatedVolume) {},
				attachedReadyServing(),
				metav1.ConditionTrue, v1alpha1.ReplicatedVolumeCondReadyReasonReady),

			// AttachedReady only drives Ready on a definite False. Unknown covers attach
			// ramp-up and stale agents, where flipping Ready would flap on every pod start.
			Entry("AttachedReady Unknown does not lower Ready",
				func(*v1alpha1.ReplicatedVolume) {},
				attachedReadyReport{
					present: true,
					status:  metav1.ConditionUnknown,
					reason:  v1alpha1.ReplicatedVolumeCondAttachedReadyReasonStatusUnknown,
				},
				metav1.ConditionTrue, v1alpha1.ReplicatedVolumeCondReadyReasonReady),
			Entry("AttachedReady not evaluated does not lower Ready",
				func(*v1alpha1.ReplicatedVolume) {},
				attachedReadyReport{present: false},
				metav1.ConditionTrue, v1alpha1.ReplicatedVolumeCondReadyReasonReady),
			Entry("no established attachment does not lower Ready",
				func(*v1alpha1.ReplicatedVolume) {},
				attachedReadyReport{
					present: true,
					status:  metav1.ConditionTrue,
					reason:  v1alpha1.ReplicatedVolumeCondAttachedReadyReasonNoAttachments,
				},
				metav1.ConditionTrue, v1alpha1.ReplicatedVolumeCondReadyReasonReady),

			// Combined entries verify precedence: a higher-precedence rule must
			// win even when a lower one also matches.
			Entry("deletion + FTT<0 → Terminating wins",
				func(rv *v1alpha1.ReplicatedVolume) {
					rv.DeletionTimestamp = ptr.To(metav1.Now())
					rv.Status.EffectiveLayout.FailuresToTolerate = ptr.To(int8(-1))
				},
				attachedReadyServing(),
				metav1.ConditionFalse, v1alpha1.ReplicatedVolumeCondReadyReasonTerminating),
			Entry("formation + frozen consumer → Forming wins",
				func(rv *v1alpha1.ReplicatedVolume) { rv.Status.DatameshRevision = 0 },
				attachedReadyFrozen(),
				metav1.ConditionFalse, v1alpha1.ReplicatedVolumeCondReadyReasonForming),
			Entry("FTT<0 + frozen consumer → QuorumLost wins",
				func(rv *v1alpha1.ReplicatedVolume) {
					rv.Status.EffectiveLayout.FailuresToTolerate = ptr.To(int8(-1))
				},
				attachedReadyFrozen(),
				metav1.ConditionFalse, v1alpha1.ReplicatedVolumeCondReadyReasonQuorumLost),
			Entry("layout undetermined + frozen consumer → StatusUnknown wins",
				func(rv *v1alpha1.ReplicatedVolume) {
					rv.Status.EffectiveLayout.FailuresToTolerate = nil
				},
				attachedReadyFrozen(),
				metav1.ConditionUnknown, v1alpha1.ReplicatedVolumeCondReadyReasonStatusUnknown),
			Entry("FTT set + GMDR nil → StatusUnknown (second || operand)",
				func(rv *v1alpha1.ReplicatedVolume) {
					rv.Status.EffectiveLayout.FailuresToTolerate = ptr.To(int8(0))
					rv.Status.EffectiveLayout.GuaranteedMinimumDataRedundancy = nil
				},
				attachedReadyServing(),
				metav1.ConditionUnknown, v1alpha1.ReplicatedVolumeCondReadyReasonStatusUnknown),
		)

		It("carries the AttachedReady message so the cause is not lost", func() {
			rv := newDataPlaneTestRV()
			frozen := attachedReadyFrozen()
			report := computeReadyReport(rv, frozen)
			Expect(report.message).To(Equal(frozen.message))
		})
	})

	Describe("computeResilientReport", func() {
		DescribeTable("computes the Resilient report",
			func(mutate func(*v1alpha1.ReplicatedVolume), wantPresent bool, wantStatus metav1.ConditionStatus, wantReason string) {
				rv := newDataPlaneTestRV()
				mutate(rv)
				report := computeResilientReport(rv)
				Expect(report.present).To(Equal(wantPresent))
				Expect(report.status).To(Equal(wantStatus))
				Expect(report.reason).To(Equal(wantReason))
			},
			Entry("deletion timestamp set",
				func(rv *v1alpha1.ReplicatedVolume) { rv.DeletionTimestamp = ptr.To(metav1.Now()) },
				false, metav1.ConditionStatus(""), ""),
			Entry("configuration nil",
				func(rv *v1alpha1.ReplicatedVolume) { rv.Status.Configuration = nil },
				true, metav1.ConditionFalse, v1alpha1.ReplicatedVolumeCondResilientReasonForming),
			Entry("formation in progress",
				func(rv *v1alpha1.ReplicatedVolume) { rv.Status.DatameshRevision = 0 },
				true, metav1.ConditionFalse, v1alpha1.ReplicatedVolumeCondResilientReasonForming),
			Entry("effective FTT nil",
				func(rv *v1alpha1.ReplicatedVolume) { rv.Status.EffectiveLayout.FailuresToTolerate = nil },
				true, metav1.ConditionUnknown, v1alpha1.ReplicatedVolumeCondResilientReasonStatusUnknown),
			Entry("effective meets intent",
				func(*v1alpha1.ReplicatedVolume) {},
				true, metav1.ConditionTrue, v1alpha1.ReplicatedVolumeCondResilientReasonResilient),
			Entry("effective below intent",
				func(rv *v1alpha1.ReplicatedVolume) { rv.Status.Configuration.FailuresToTolerate = 1 },
				true, metav1.ConditionFalse, v1alpha1.ReplicatedVolumeCondResilientReasonDegraded),
		)
	})

	Describe("computeAttachedReadyReport", func() {
		// The default fixture is one attached member on n0 with a live consumer and a serving
		// replica; each entry mutates exactly the input its rule is about.
		type inputs struct {
			rv   *v1alpha1.ReplicatedVolume
			rvrs []*v1alpha1.ReplicatedVolumeReplica
			rvas []*v1alpha1.ReplicatedVolumeAttachment
		}

		newInputs := func() *inputs {
			rv := newDataPlaneTestRV()
			addAttachedMember(rv, "rv-r0", "n0")
			return &inputs{
				rv: rv,
				rvrs: []*v1alpha1.ReplicatedVolumeReplica{
					newTestRVRWithAttachedReason("rv-r0", metav1.ConditionTrue,
						v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonAttached),
				},
				rvas: []*v1alpha1.ReplicatedVolumeAttachment{newTestRVAOnNode("rva-n0", "n0")},
			}
		}

		DescribeTable("computes the AttachedReady report",
			func(mutate func(*inputs), wantPresent bool, wantStatus metav1.ConditionStatus, wantReason string) {
				in := newInputs()
				mutate(in)
				report := computeAttachedReadyReport(in.rv, in.rvrs, in.rvas)
				Expect(report.present).To(Equal(wantPresent))
				Expect(report.status).To(Equal(wantStatus))
				Expect(report.reason).To(Equal(wantReason))
			},

			// Not evaluated.
			Entry("deletion timestamp set freezes the condition",
				func(in *inputs) { in.rv.DeletionTimestamp = ptr.To(metav1.Now()) },
				false, metav1.ConditionStatus(""), ""),
			Entry("configuration nil leaves the condition absent",
				func(in *inputs) { in.rv.Status.Configuration = nil },
				false, metav1.ConditionStatus(""), ""),

			// Nothing established.
			Entry("no members at all",
				func(in *inputs) { in.rv.Status.Datamesh.Members = nil },
				true, metav1.ConditionTrue, v1alpha1.ReplicatedVolumeCondAttachedReadyReasonNoAttachments),
			Entry("member is not attached",
				func(in *inputs) { in.rv.Status.Datamesh.Members[0].Attached = false },
				true, metav1.ConditionTrue, v1alpha1.ReplicatedVolumeCondAttachedReadyReasonNoAttachments),
			Entry("attached member without a consumer on its node",
				func(in *inputs) { in.rvas = nil },
				true, metav1.ConditionTrue, v1alpha1.ReplicatedVolumeCondAttachedReadyReasonNoAttachments),
			Entry("the only consumer is being deleted",
				func(in *inputs) { in.rvas[0].DeletionTimestamp = ptr.To(metav1.Now()) },
				true, metav1.ConditionTrue, v1alpha1.ReplicatedVolumeCondAttachedReadyReasonNoAttachments),
			Entry("a Detach transition is in flight for the member",
				func(in *inputs) {
					in.rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{{
						Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach,
						ReplicaName: "rv-r0",
					}}
				},
				true, metav1.ConditionTrue, v1alpha1.ReplicatedVolumeCondAttachedReadyReasonNoAttachments),
			Entry("a ForceDetach transition is in flight for the member",
				func(in *inputs) {
					in.rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{{
						Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeForceDetach,
						ReplicaName: "rv-r0",
					}}
				},
				true, metav1.ConditionTrue, v1alpha1.ReplicatedVolumeCondAttachedReadyReasonNoAttachments),
			Entry("a Detach transition for another replica does not skip this member",
				func(in *inputs) {
					in.rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{{
						Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach,
						ReplicaName: "rv-r1",
					}}
				},
				true, metav1.ConditionTrue, v1alpha1.ReplicatedVolumeCondAttachedReadyReasonAttachedReady),

			// Single established attachment, per-verdict mapping.
			Entry("replica is attached and serving",
				func(*inputs) {},
				true, metav1.ConditionTrue, v1alpha1.ReplicatedVolumeCondAttachedReadyReasonAttachedReady),
			Entry("replica reports IOSuspended",
				func(in *inputs) {
					in.rvrs[0] = newTestRVRWithAttachedReason("rv-r0", metav1.ConditionFalse,
						v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonIOSuspended)
				},
				true, metav1.ConditionFalse, v1alpha1.ReplicatedVolumeCondAttachedReadyReasonIOSuspended),
			Entry("replica reports AttachmentFailed",
				func(in *inputs) {
					in.rvrs[0] = newTestRVRWithAttachedReason("rv-r0", metav1.ConditionFalse,
						v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonAttachmentFailed)
				},
				true, metav1.ConditionFalse, v1alpha1.ReplicatedVolumeCondAttachedReadyReasonAttachmentFailed),
			Entry("replica reports AgentNotReady",
				func(in *inputs) {
					in.rvrs[0] = newTestRVRWithAttachedReason("rv-r0", metav1.ConditionFalse,
						v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonAgentNotReady)
				},
				true, metav1.ConditionUnknown, v1alpha1.ReplicatedVolumeCondAttachedReadyReasonStatusUnknown),
			Entry("replica reports ApplyingConfiguration",
				func(in *inputs) {
					in.rvrs[0] = newTestRVRWithAttachedReason("rv-r0", metav1.ConditionFalse,
						v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonApplyingConfiguration)
				},
				true, metav1.ConditionUnknown, v1alpha1.ReplicatedVolumeCondAttachedReadyReasonStatusUnknown),
			Entry("replica reports DetachmentFailed",
				func(in *inputs) {
					in.rvrs[0] = newTestRVRWithAttachedReason("rv-r0", metav1.ConditionFalse,
						v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonDetachmentFailed)
				},
				true, metav1.ConditionUnknown, v1alpha1.ReplicatedVolumeCondAttachedReadyReasonStatusUnknown),
			Entry("replica is missing",
				func(in *inputs) { in.rvrs = nil },
				true, metav1.ConditionUnknown, v1alpha1.ReplicatedVolumeCondAttachedReadyReasonStatusUnknown),
			Entry("replica has no Attached condition yet",
				func(in *inputs) {
					in.rvrs[0] = &v1alpha1.ReplicatedVolumeReplica{
						ObjectMeta: metav1.ObjectMeta{Name: "rv-r0"},
					}
				},
				true, metav1.ConditionUnknown, v1alpha1.ReplicatedVolumeCondAttachedReadyReasonStatusUnknown),

			// Multiattach: the worst verdict wins.
			Entry("one serving, one frozen → IOSuspended",
				func(in *inputs) {
					addAttachedMember(in.rv, "rv-r1", "n1")
					in.rvrs = append(in.rvrs, newTestRVRWithAttachedReason("rv-r1", metav1.ConditionFalse,
						v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonIOSuspended))
					in.rvas = append(in.rvas, newTestRVAOnNode("rva-n1", "n1"))
				},
				true, metav1.ConditionFalse, v1alpha1.ReplicatedVolumeCondAttachedReadyReasonIOSuspended),
			Entry("one failed, one frozen → IOSuspended wins",
				func(in *inputs) {
					in.rvrs[0] = newTestRVRWithAttachedReason("rv-r0", metav1.ConditionFalse,
						v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonAttachmentFailed)
					addAttachedMember(in.rv, "rv-r1", "n1")
					in.rvrs = append(in.rvrs, newTestRVRWithAttachedReason("rv-r1", metav1.ConditionFalse,
						v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonIOSuspended))
					in.rvas = append(in.rvas, newTestRVAOnNode("rva-n1", "n1"))
				},
				true, metav1.ConditionFalse, v1alpha1.ReplicatedVolumeCondAttachedReadyReasonIOSuspended),
			Entry("one unknown, one failed → AttachmentFailed wins",
				func(in *inputs) {
					in.rvrs[0] = newTestRVRWithAttachedReason("rv-r0", metav1.ConditionFalse,
						v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonAgentNotReady)
					addAttachedMember(in.rv, "rv-r1", "n1")
					in.rvrs = append(in.rvrs, newTestRVRWithAttachedReason("rv-r1", metav1.ConditionFalse,
						v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonAttachmentFailed))
					in.rvas = append(in.rvas, newTestRVAOnNode("rva-n1", "n1"))
				},
				true, metav1.ConditionFalse, v1alpha1.ReplicatedVolumeCondAttachedReadyReasonAttachmentFailed),
			Entry("both serving",
				func(in *inputs) {
					addAttachedMember(in.rv, "rv-r1", "n1")
					in.rvrs = append(in.rvrs, newTestRVRWithAttachedReason("rv-r1", metav1.ConditionTrue,
						v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonAttached))
					in.rvas = append(in.rvas, newTestRVAOnNode("rva-n1", "n1"))
				},
				true, metav1.ConditionTrue, v1alpha1.ReplicatedVolumeCondAttachedReadyReasonAttachedReady),
		)

		It("names the offending replica and counts the established attachments", func() {
			in := newInputs()
			addAttachedMember(in.rv, "rv-r1", "n1")
			in.rvrs = append(in.rvrs, newTestRVRWithAttachedReason("rv-r1", metav1.ConditionFalse,
				v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonIOSuspended))
			in.rvas = append(in.rvas, newTestRVAOnNode("rva-n1", "n1"))

			report := computeAttachedReadyReport(in.rv, in.rvrs, in.rvas)
			Expect(report.message).To(Equal(
				"1/2 attachments are not serving IO: replica rv-r1 on node n1 (IO is suspended on the device)"))
		})

		// An Unknown verdict is the absence of a report, not an established fact, so the
		// message must not claim the attachment is not serving.
		It("says serving could not be confirmed when the worst verdict is Unknown", func() {
			in := newInputs()
			in.rvrs[0] = newTestRVRWithAttachedReason("rv-r0", metav1.ConditionFalse,
				v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonAgentNotReady)

			report := computeAttachedReadyReport(in.rv, in.rvrs, in.rvas)
			Expect(report.message).To(Equal(
				"1/1 attachments are not confirmed serving IO: replica rv-r0 on node n0 " +
					"(replica Attached=False/AgentNotReady)"))
		})

		// Mixed mesh: the count under a definite worst must speak for the definite offenders
		// only. Counting the unreported member here would claim 2/2 are not serving IO while
		// only one of them is known to be frozen.
		It("counts only definite offenders when a definite verdict and an Unknown one coexist", func() {
			in := newInputs()
			in.rvrs[0] = newTestRVRWithAttachedReason("rv-r0", metav1.ConditionFalse,
				v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonIOSuspended)
			addAttachedMember(in.rv, "rv-r1", "n1")
			in.rvrs = append(in.rvrs, newTestRVRWithAttachedReason("rv-r1", metav1.ConditionFalse,
				v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonAgentNotReady))
			in.rvas = append(in.rvas, newTestRVAOnNode("rva-n1", "n1"))

			report := computeAttachedReadyReport(in.rv, in.rvrs, in.rvas)
			Expect(report.status).To(Equal(metav1.ConditionFalse))
			Expect(report.reason).To(Equal(v1alpha1.ReplicatedVolumeCondAttachedReadyReasonIOSuspended))
			Expect(report.message).To(Equal(
				"1/2 attachments are not serving IO: replica rv-r0 on node n0 (IO is suspended on the device)"))
		})
	})
})
