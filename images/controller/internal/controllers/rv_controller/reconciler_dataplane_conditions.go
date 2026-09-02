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
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: data-plane conditions
//

// reconcileDataPlaneConditions is the SINGLE writer of the Ready, Resilient and AttachedReady
// conditions in the normal-operation flow. The deletion branch (reconcileDeletion) also writes
// Ready=False/Terminating via the same computeReadyReport + applyReadyCond helpers in
// its own atomic status patch, mirroring the MembershipLayoutConverged pattern.
// It computes all three from the effective layout, configuration, deletion timestamp, formation
// state and the attachment state of the members, and applies them in place. It only reports; it
// reuses the already-refreshed EffectiveLayout (updated inside reconcileNormalOperation) and the
// already-set configuration.
//
// AttachedReady is computed first because Ready folds its verdict in as reason
// AttachmentNotReady, so both conditions always agree within a pass.
//
// Reconcile pattern: In-place reconciliation
func (r *Reconciler) reconcileDataPlaneConditions(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	rvas []*v1alpha1.ReplicatedVolumeAttachment,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "data-plane-conditions")
	defer rf.OnEnd(&outcome)

	attachedReady := computeAttachedReadyReport(rv, rvrs, rvas)
	ready := computeReadyReport(rv, attachedReady)
	resilient := computeResilientReport(rv)

	changed := applyAttachedReadyCond(rv, attachedReady)
	changed = applyReadyCond(rv, ready) || changed
	changed = applyResilientCond(rv, resilient) || changed

	return rf.Continue().ReportChangedIf(changed)
}

// ──────────────────────────────────────────────────────────────────────────────
// Reports
//

// readyReport is the computed Ready condition report: the aggregate availability + lifecycle
// state of the volume, with the cause of unavailability carried in the reason.
type readyReport struct {
	status  metav1.ConditionStatus
	reason  string
	message string
}

// resilientReport is the computed Resilient condition report: whether the effective redundancy
// meets the redundancy intended by the volume's configuration. present=false means the
// condition must not be touched this pass (e.g. the volume is being deleted and the last
// known value is preserved).
type resilientReport struct {
	present bool
	status  metav1.ConditionStatus
	reason  string
	message string
}

// attachedReadyReport is the computed AttachedReady condition report: whether every established
// attachment of the volume is serving I/O on its node. present=false means the condition must
// not be touched this pass (deletion freezes it; a volume without a configuration has never had
// it evaluated).
type attachedReadyReport struct {
	present bool
	status  metav1.ConditionStatus
	reason  string
	message string
}

// isBlocking reports whether the attachment state alone makes the volume unavailable to its
// consumers, which is what Ready folds in as reason AttachmentNotReady. Only a definite False
// blocks: Unknown covers transient attach ramp-up and stale agents, and letting it drive Ready
// would make Ready flap on every pod start.
func (r attachedReadyReport) isBlocking() bool {
	return r.present && r.status == metav1.ConditionFalse
}

// ──────────────────────────────────────────────────────────────────────────────
// Compute: Ready
//

// computeReadyReport computes the Ready condition from the effective layout, the configuration,
// the deletion timestamp, the formation state and the AttachedReady report. Ordered precedence,
// first match wins:
//  1. DeletionTimestamp set                 → False / Terminating
//  2. Formation in progress or never formed  → False / Forming
//  3. Effective FTT or GMDR nil              → Unknown / StatusUnknown
//  4. FTT < 0                                → False / QuorumLost
//  5. AttachedReady is False                 → False / AttachmentNotReady
//  6. otherwise                              → True / Ready
//
// Rule 5 sits below the mesh-level rules on purpose: when the mesh itself lost quorum, a frozen
// consumer is a symptom of it, and the operator needs the mesh-level root cause in the reason.
// Above rule 6 it closes the opposite gap — the mesh holds quorum on the majority side of a
// partition while the consumer is frozen on the isolated one.
//
// It is a pure read-only computation: it MUST NOT mutate rv.
func computeReadyReport(rv *v1alpha1.ReplicatedVolume, attachedReady attachedReadyReport) readyReport {
	// 1. Terminating
	if rv.DeletionTimestamp != nil {
		return readyReport{
			status:  metav1.ConditionFalse,
			reason:  v1alpha1.ReplicatedVolumeCondReadyReasonTerminating,
			message: "Volume is being deleted",
		}
	}

	// 2. Forming (active formation transition or volume never reached IO-serving state)
	if forming, _ := isFormationInProgress(rv); forming {
		return readyReport{
			status:  metav1.ConditionFalse,
			reason:  v1alpha1.ReplicatedVolumeCondReadyReasonForming,
			message: "Volume formation in progress",
		}
	}

	// 3. Effective layout undetermined
	el := &rv.Status.EffectiveLayout
	if el.FailuresToTolerate == nil || el.GuaranteedMinimumDataRedundancy == nil {
		return readyReport{
			status:  metav1.ConditionUnknown,
			reason:  v1alpha1.ReplicatedVolumeCondReadyReasonStatusUnknown,
			message: effectiveLayoutUndeterminedMessage(el),
		}
	}

	ftt := *el.FailuresToTolerate
	gmdr := *el.GuaranteedMinimumDataRedundancy

	// 4. Quorum lost (FTT < 0)
	if ftt < 0 {
		return readyReport{
			status: metav1.ConditionFalse,
			reason: v1alpha1.ReplicatedVolumeCondReadyReasonQuorumLost,
			message: fmt.Sprintf("Quorum lost: FTT=%d (reachable %d+%dTB-%dq, UpToDate %d-%dqmr)",
				ftt, el.ReachableVoters, el.ReachableTieBreakers, rv.Status.Datamesh.Quorum,
				el.UpToDateVoters, rv.Status.Datamesh.QuorumMinimumRedundancy),
		}
	}

	// 5. Mesh-level availability holds, but a consumer is not being served.
	if attachedReady.isBlocking() {
		return readyReport{
			status:  metav1.ConditionFalse,
			reason:  v1alpha1.ReplicatedVolumeCondReadyReasonAttachmentNotReady,
			message: attachedReady.message,
		}
	}

	// 6. Ready
	return readyReport{
		status:  metav1.ConditionTrue,
		reason:  v1alpha1.ReplicatedVolumeCondReadyReasonReady,
		message: fmt.Sprintf("Serving IO with guaranteed minimum redundancy (FTT=%d, GMDR=%d)", ftt, gmdr),
	}
}

// effectiveLayoutUndeterminedMessage builds the StatusUnknown message from the effective layout
// stale-agent count. Pure.
func effectiveLayoutUndeterminedMessage(el *v1alpha1.ReplicatedVolumeEffectiveLayout) string {
	if el.StaleAgents > 0 {
		return fmt.Sprintf("Effective layout undetermined: %d stale agents", el.StaleAgents)
	}
	return "Effective layout undetermined"
}

// ──────────────────────────────────────────────────────────────────────────────
// Compute: Resilient
//

// computeResilientReport computes the Resilient condition: whether the effective redundancy
// (FTT/GMDR) meets the redundancy intended by the volume's configuration.
//
// Deletion: present=false (the condition is not recomputed; the last known value is preserved).
// Formation / configuration not set / never formed: False / Forming.
// Effective layout undetermined: Unknown / StatusUnknown.
// Effective FTT and GMDR both >= intended: True / Resilient.
// Otherwise: False / Degraded.
//
// It is a pure read-only computation: it MUST NOT mutate rv.
func computeResilientReport(rv *v1alpha1.ReplicatedVolume) resilientReport {
	// Deletion: preserve the last known value.
	if rv.DeletionTimestamp != nil {
		return resilientReport{present: false}
	}

	// Formation / no configuration / never reached IO-serving state.
	forming, _ := isFormationInProgress(rv)
	if rv.Status.Configuration == nil || forming {
		return resilientReport{
			present: true,
			status:  metav1.ConditionFalse,
			reason:  v1alpha1.ReplicatedVolumeCondResilientReasonForming,
			message: "Volume formation in progress",
		}
	}

	// Effective layout undetermined.
	el := &rv.Status.EffectiveLayout
	if el.FailuresToTolerate == nil || el.GuaranteedMinimumDataRedundancy == nil {
		return resilientReport{
			present: true,
			status:  metav1.ConditionUnknown,
			reason:  v1alpha1.ReplicatedVolumeCondResilientReasonStatusUnknown,
			message: effectiveLayoutUndeterminedMessage(el),
		}
	}

	ftt := *el.FailuresToTolerate
	gmdr := *el.GuaranteedMinimumDataRedundancy
	intendedFTT := int8(rv.Status.Configuration.FailuresToTolerate)
	intendedGMDR := int8(rv.Status.Configuration.GuaranteedMinimumDataRedundancy)

	if ftt >= intendedFTT && gmdr >= intendedGMDR {
		return resilientReport{
			present: true,
			status:  metav1.ConditionTrue,
			reason:  v1alpha1.ReplicatedVolumeCondResilientReasonResilient,
			message: fmt.Sprintf("Effective redundancy meets intent: FTT=%d/%d, GMDR=%d/%d",
				ftt, intendedFTT, gmdr, intendedGMDR),
		}
	}
	return resilientReport{
		present: true,
		status:  metav1.ConditionFalse,
		reason:  v1alpha1.ReplicatedVolumeCondResilientReasonDegraded,
		message: fmt.Sprintf("Redundancy below intent: FTT=%d (intended %d), GMDR=%d (intended %d)",
			ftt, intendedFTT, gmdr, intendedGMDR),
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Compute: AttachedReady
//

// attachmentVerdict is the per-attachment verdict, ordered by severity so the volume-level
// report can be the worst verdict across all established attachments.
type attachmentVerdict int

const (
	// attachmentVerdictServing means the attachment is attached and serving I/O.
	attachmentVerdictServing attachmentVerdict = iota
	// attachmentVerdictUnknown means the attachment is in a transient or unreported state.
	attachmentVerdictUnknown
	// attachmentVerdictAttachmentFailed means the replica should be attached but is not.
	attachmentVerdictAttachmentFailed
	// attachmentVerdictIOSuspended means the device is attached but its I/O is frozen.
	attachmentVerdictIOSuspended
)

// computeAttachedReadyReport computes the AttachedReady condition: whether every established
// attachment of the volume is serving I/O on its node.
//
// An attachment is established when the datamesh member is marked Attached, a non-deleting RVA
// exists on its node (the consumer), and no Detach/ForceDetach transition is in flight for it.
// Members without a consumer and members mid-detach are therefore skipped.
//
// The volume-level verdict is the worst per-attachment verdict; the message names the first
// member holding it (members are traversed in status order, which is stable across passes).
//
// # Why evaluating the start-of-pass snapshot is safe during an attach ramp-up
//
// The report is computed once per pass, from the member list as it stands at the start of it. The
// member filter above closes only the window BEFORE the attach is committed: while m.Attached is
// still false the member is skipped. It does NOT close the window right after — m.Attached is
// already true in the API, while DRBD has not promoted the device yet, and that is exactly the
// shape of the AttachmentFailed verdict (intendedAttached && !actualAttached in
// rvr_controller/reconciler_conditions.go). Were it reported, a routine pod start would flip
// AttachedReady to False and Ready to AttachmentNotReady for a pass or two.
//
// What keeps that from happening is NOT anything in this file: the replica controller checks
// drbdrConfigurationPending BEFORE the intended/actual comparison, so a replica whose attach is
// still being applied reports Unknown/ApplyingConfiguration, which maps to
// attachmentVerdictUnknown and leaves Ready alone. The property is thus a cross-controller
// agreement, not a local invariant — reordering those guards in the replica controller, or
// removing the pending check, would silently make Ready flap on every attach.
//
// The e2e guard against that regression is the continuous Ready=True invariant armed around the
// attach in e2e/full/layout_reporting_test.go; the unit tests here are single-pass and cannot
// observe a transient.
//
// Deletion: present=false (frozen at the last value; Ready already reports Terminating).
// No configuration: present=false (absent = not yet evaluated).
//
// It is a pure read-only computation: it MUST NOT mutate rv, rvrs or rvas.
func computeAttachedReadyReport(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	rvas []*v1alpha1.ReplicatedVolumeAttachment,
) attachedReadyReport {
	// Deletion: preserve the last known value.
	if rv.DeletionTimestamp != nil {
		return attachedReadyReport{present: false}
	}

	// No configuration yet: nothing has been evaluated.
	if rv.Status.Configuration == nil {
		return attachedReadyReport{present: false}
	}

	established := 0
	// notServing and unconfirmed are counted apart so the message never overstates what is
	// known: a definite verdict (frozen, failed) is a fact, an Unknown one is only the absence
	// of a report, and a mesh can hold both at once.
	notServing := 0
	unconfirmed := 0
	worst := attachmentVerdictServing
	worstDetail := ""
	worstMember := ""
	worstNode := ""

	for i := range rv.Status.Datamesh.Members {
		m := &rv.Status.Datamesh.Members[i]
		if !m.Attached {
			continue
		}
		if !hasNonDeletingRVAOnNode(rvas, m.NodeName) {
			continue
		}
		if hasActiveDetachTransition(rv, m.Name) {
			continue
		}
		established++

		verdict, detail := computeAttachmentVerdict(findRVRByName(rvrs, m.Name))
		if verdict == attachmentVerdictServing {
			continue
		}
		if verdict == attachmentVerdictUnknown {
			unconfirmed++
		} else {
			notServing++
		}
		if verdict > worst {
			worst = verdict
			worstDetail = detail
			worstMember = m.Name
			worstNode = m.NodeName
		}
	}

	if established == 0 {
		return attachedReadyReport{
			present: true,
			status:  metav1.ConditionTrue,
			reason:  v1alpha1.ReplicatedVolumeCondAttachedReadyReasonNoAttachments,
			message: "No established attachment: the volume is not in use",
		}
	}

	if worst == attachmentVerdictServing {
		return attachedReadyReport{
			present: true,
			status:  metav1.ConditionTrue,
			reason:  v1alpha1.ReplicatedVolumeCondAttachedReadyReasonAttachedReady,
			message: fmt.Sprintf("All %d attachments are serving IO", established),
		}
	}

	// The verb follows the worst verdict, not the count: "are not serving IO" states a fact and
	// belongs to a definite verdict only. When the worst is Unknown there is no definite verdict
	// anywhere in the mesh, so the honest claim is that serving could not be confirmed.
	// The count follows the same split — under a definite worst it counts definite offenders
	// only, so it never speaks for the members that merely failed to report.
	msg := func(affected int, verb string) string {
		return fmt.Sprintf("%d/%d attachments %s: replica %s on node %s (%s)",
			affected, established, verb, worstMember, worstNode, worstDetail)
	}

	switch worst {
	case attachmentVerdictIOSuspended:
		return attachedReadyReport{
			present: true,
			status:  metav1.ConditionFalse,
			reason:  v1alpha1.ReplicatedVolumeCondAttachedReadyReasonIOSuspended,
			message: msg(notServing, "are not serving IO"),
		}
	case attachmentVerdictAttachmentFailed:
		return attachedReadyReport{
			present: true,
			status:  metav1.ConditionFalse,
			reason:  v1alpha1.ReplicatedVolumeCondAttachedReadyReasonAttachmentFailed,
			message: msg(notServing, "are not serving IO"),
		}
	default:
		// attachmentVerdictUnknown. Serving is returned above, so it is the only verdict
		// left today; a verdict added later lands here too and is reported as unconfirmed
		// rather than as a definite failure — the conservative direction, since only a
		// definite False lowers Ready.
		return attachedReadyReport{
			present: true,
			status:  metav1.ConditionUnknown,
			reason:  v1alpha1.ReplicatedVolumeCondAttachedReadyReasonStatusUnknown,
			message: msg(unconfirmed, "are not confirmed serving IO"),
		}
	}
}

// computeAttachmentVerdict maps the replica's Attached condition to a per-attachment verdict and
// a human-readable detail. It mirrors a condition instead of reading status.attachment directly:
// the replica controller is the single writer of that condition, so the verdict does not depend
// on the order in which this controller writes its own RVA status within the same pass.
//
// Reasons outside the definite ones (AgentNotReady, ApplyingConfiguration, DetachmentFailed, and
// anything added later) map to Unknown: they are transient or unreported states, and Ready must
// not flip on them.
//
// It switches on the reason alone, deliberately. The status cannot substitute for it:
// DetachmentFailed is reported with Status=True, so a status-driven mapping would read a member
// that should be detached but is not as serving. The reverse coupling — that the definite reasons
// (IOSuspended, AttachmentFailed) always arrive with Status=False — is a property of the single
// writer rather than something checked here; it is pinned by the status/reason pairs asserted in
// rvr_controller/reconciler_conditions_test.go. A writer that ever emitted a definite reason with
// another status would still be reported as a definite failure.
func computeAttachmentVerdict(rvr *v1alpha1.ReplicatedVolumeReplica) (attachmentVerdict, string) {
	if rvr == nil {
		return attachmentVerdictUnknown, "replica not found"
	}

	cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondAttachedType)
	if cond == nil {
		return attachmentVerdictUnknown, "replica has no Attached condition yet"
	}

	switch cond.Reason {
	case v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonAttached:
		return attachmentVerdictServing, ""
	case v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonIOSuspended:
		return attachmentVerdictIOSuspended, "IO is suspended on the device"
	case v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonAttachmentFailed:
		return attachmentVerdictAttachmentFailed, "expected to be attached, but is not"
	default:
		return attachmentVerdictUnknown, fmt.Sprintf("replica Attached=%s/%s", cond.Status, cond.Reason)
	}
}

// hasNonDeletingRVAOnNode reports whether a live consumer (a non-deleting RVA) exists on the node.
// The empty excludeName excludes nothing: RVA names are never empty.
func hasNonDeletingRVAOnNode(rvas []*v1alpha1.ReplicatedVolumeAttachment, nodeName string) bool {
	return hasOtherNonDeletingRVAOnNode(rvas, nodeName, "")
}

// hasActiveDetachTransition reports whether a Detach or ForceDetach transition is in flight for
// the given replica. Such a member is on its way out of the attached state, so its attachment is
// not a consumer whose freeze should be reported.
func hasActiveDetachTransition(rv *v1alpha1.ReplicatedVolume, replicaName string) bool {
	for i := range rv.Status.DatameshTransitions {
		t := &rv.Status.DatameshTransitions[i]
		if t.ReplicaName != replicaName {
			continue
		}
		if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach ||
			t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeForceDetach {
			return true
		}
	}
	return false
}

// ──────────────────────────────────────────────────────────────────────────────
// Apply
//

// applyReadyCond sets the Ready condition from a computed readyReport.
func applyReadyCond(rv *v1alpha1.ReplicatedVolume, report readyReport) bool {
	return obju.SetStatusCondition(rv, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeCondReadyType,
		Status:  report.status,
		Reason:  report.reason,
		Message: report.message,
	})
}

// applyResilientCond sets the Resilient condition from a computed resilientReport.
// When report.present is false the condition is left untouched (the last known value
// is preserved, e.g. during deletion).
func applyResilientCond(rv *v1alpha1.ReplicatedVolume, report resilientReport) bool {
	if !report.present {
		return false
	}
	return obju.SetStatusCondition(rv, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeCondResilientType,
		Status:  report.status,
		Reason:  report.reason,
		Message: report.message,
	})
}

// applyAttachedReadyCond sets the AttachedReady condition from a computed attachedReadyReport.
// When report.present is false the condition is left untouched (the last known value is
// preserved, e.g. during deletion).
func applyAttachedReadyCond(rv *v1alpha1.ReplicatedVolume, report attachedReadyReport) bool {
	if !report.present {
		return false
	}
	return obju.SetStatusCondition(rv, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeCondAttachedReadyType,
		Status:  report.status,
		Reason:  report.reason,
		Message: report.message,
	})
}
