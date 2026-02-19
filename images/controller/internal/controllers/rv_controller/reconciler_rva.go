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
	"k8s.io/utils/ptr"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: RVA conditions
//

// reconcileRVAConditionsFromAttachmentsSummary updates status conditions and attachment fields
// for each RVA based on the attachmentsSummary.
//
// Iterates over attachmentStates; each state contains all RVAs for that node,
// so conditions are computed once per node and applied to all RVAs.
//
// Reconcile pattern: In-place reconciliation
func (r *Reconciler) reconcileRVAConditionsFromAttachmentsSummary(
	ctx context.Context,
	atts *attachmentsSummary,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "rva-conditions")
	defer rf.OnEnd(&outcome)

	// Process RVAs grouped by node via attachmentStates.
	for i := range atts.attachmentStates {
		as := &atts.attachmentStates[i]
		if len(as.rvas) == 0 {
			continue
		}

		// Conditions are identical for all RVAs on the same node.
		attached := computeRVAAttachedConditionFromAttachmentsSummary(as)
		replicaReady := computeRVAReplicaReadyConditionFromAttachmentsSummary(as)

		for _, rva := range as.rvas {
			// Ready differs per RVA: Deleting RVAs are never Ready.
			deleting := rva.DeletionTimestamp != nil
			ready := computeRVAReadyCondition(attached, replicaReady, deleting)

			if obju.ConditionSemanticallyEqual(obju.GetStatusCondition(rva, attached.Type), &attached) &&
				obju.ConditionSemanticallyEqual(obju.GetStatusCondition(rva, replicaReady.Type), &replicaReady) &&
				obju.ConditionSemanticallyEqual(obju.GetStatusCondition(rva, ready.Type), &ready) &&
				isRVAAttachmentFieldsInSync(rva, as.rvr) {
				continue
			}

			base := rva.DeepCopy()

			obju.SetStatusCondition(rva, attached)
			obju.SetStatusCondition(rva, replicaReady)
			obju.SetStatusCondition(rva, ready)

			// Copy attachment fields from RVR if available, clear otherwise.
			applyRVAAttachmentFields(rva, as.rvr)

			if err := r.patchRVAStatus(rf.Ctx(), rva, base); err != nil {
				return rf.Failf(err, "patching RVA %s status", rva.Name)
			}
		}
	}

	return rf.Continue()
}

// ──────────────────────────────────────────────────────────────────────────────
// Helpers: reconcileRVAConditions (non-I/O)
//

// computeRVAAttachedCondition computes the Attached condition for an RVA
// from the pre-indexed attachmentState.
//
// All reasons and messages are set by the upstream attachment flow
// (computeDatameshAttachmentIntents / ensureDatameshAttach*Transitions).
// This function is a pure mapper: conditionReason → condition Status/Reason/Message.
func computeRVAAttachedConditionFromAttachmentsSummary(as *attachmentState) metav1.Condition {
	if as.conditionReason == "" || as.conditionMessage == "" {
		panic(fmt.Sprintf(
			"computeRVAAttachedConditionFromAttachmentsSummary: conditionReason and conditionMessage must be set by upstream flow for node %s (intent=%s, reason=%q, message=%q)",
			as.nodeName, as.intent, as.conditionReason, as.conditionMessage,
		))
	}

	cond := metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeAttachmentCondAttachedType,
		Reason:  as.conditionReason,
		Message: as.conditionMessage,
	}

	if cond.Reason == v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttached {
		cond.Status = metav1.ConditionTrue
	} else {
		cond.Status = metav1.ConditionFalse
	}

	return cond
}

// computeRVAReplicaReadyCondition computes the ReplicaReady condition for an RVA
// by mirroring the RVR Ready condition for the replica on this node.
func computeRVAReplicaReadyConditionFromAttachmentsSummary(as *attachmentState) metav1.Condition {
	cond := metav1.Condition{
		Type: v1alpha1.ReplicatedVolumeAttachmentCondReplicaReadyType,
	}

	// No attachment state or no RVR.
	if as == nil || as.rvr == nil {
		cond.Status = metav1.ConditionUnknown
		cond.Reason = v1alpha1.ReplicatedVolumeAttachmentCondReplicaReadyReasonWaitingForReplica
		cond.Message = "Replica not found on node"
		return cond
	}

	// Mirror the RVR Ready condition.
	rvrReady := obju.GetStatusCondition(as.rvr, v1alpha1.ReplicatedVolumeReplicaCondReadyType)
	if rvrReady == nil {
		cond.Status = metav1.ConditionUnknown
		cond.Reason = v1alpha1.ReplicatedVolumeAttachmentCondReplicaReadyReasonWaitingForReplica
		cond.Message = "Replica Ready condition not yet available"
		return cond
	}

	// Copy Status, Reason, Message from RVR Ready condition.
	cond.Status = rvrReady.Status
	cond.Reason = rvrReady.Reason
	cond.Message = rvrReady.Message
	return cond
}

// computeRVAReadyCondition computes the aggregate Ready condition:
// Ready=True iff Attached=True AND ReplicaReady=True AND not deleting.
func computeRVAReadyCondition(attached, replicaReady metav1.Condition, deleting bool) metav1.Condition {
	cond := metav1.Condition{
		Type: v1alpha1.ReplicatedVolumeAttachmentCondReadyType,
	}

	if deleting {
		cond.Status = metav1.ConditionFalse
		cond.Reason = v1alpha1.ReplicatedVolumeAttachmentCondReadyReasonDeleting
		cond.Message = "Attachment is being deleted"
		return cond
	}

	if replicaReady.Status == metav1.ConditionFalse {
		cond.Status = metav1.ConditionFalse
		cond.Reason = v1alpha1.ReplicatedVolumeAttachmentCondReadyReasonReplicaNotReady
		cond.Message = "See ReplicaReady condition"
		return cond
	}

	if replicaReady.Status == metav1.ConditionUnknown {
		cond.Status = metav1.ConditionUnknown
		cond.Reason = v1alpha1.ReplicatedVolumeAttachmentCondReadyReasonReplicaNotReady
		cond.Message = "See ReplicaReady condition"
		return cond
	}

	if attached.Status != metav1.ConditionTrue {
		cond.Status = metav1.ConditionFalse
		cond.Reason = v1alpha1.ReplicatedVolumeAttachmentCondReadyReasonNotAttached
		cond.Message = "See Attached condition"
		return cond
	}

	cond.Status = metav1.ConditionTrue
	cond.Reason = v1alpha1.ReplicatedVolumeAttachmentCondReadyReasonReady
	cond.Message = "Attached and replica is ready"
	return cond
}

// applyRVAAttachmentFields fills devicePath, ioSuspended, inUse from the RVR attachment.
// Clears the fields if rvr or its attachment is nil.
func applyRVAAttachmentFields(rva *v1alpha1.ReplicatedVolumeAttachment, rvr *v1alpha1.ReplicatedVolumeReplica) {
	if rvr == nil || rvr.Status.Attachment == nil {
		clearRVAAttachmentFields(rva)
		return
	}
	att := rvr.Status.Attachment
	rva.Status.DevicePath = att.DevicePath
	rva.Status.IOSuspended = ptr.To(att.IOSuspended)
	rva.Status.InUse = ptr.To(att.InUse)
}

// clearRVAAttachmentFields clears devicePath, ioSuspended, inUse.
func clearRVAAttachmentFields(rva *v1alpha1.ReplicatedVolumeAttachment) {
	rva.Status.DevicePath = ""
	rva.Status.IOSuspended = nil
	rva.Status.InUse = nil
}

// isRVAAttachmentFieldsInSync checks if attachment fields match the RVR.
func isRVAAttachmentFieldsInSync(rva *v1alpha1.ReplicatedVolumeAttachment, rvr *v1alpha1.ReplicatedVolumeReplica) bool {
	if rvr == nil || rvr.Status.Attachment == nil {
		return rva.Status.DevicePath == "" && rva.Status.IOSuspended == nil && rva.Status.InUse == nil
	}
	att := rvr.Status.Attachment
	return rva.Status.DevicePath == att.DevicePath &&
		ptr.Equal(rva.Status.IOSuspended, ptr.To(att.IOSuspended)) &&
		ptr.Equal(rva.Status.InUse, ptr.To(att.InUse))
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: RVA finalizers
//

// reconcileRVAFinalizers adds RVControllerFinalizer to non-deleting RVAs
// and removes it from deleting RVAs when safe (node not attached and no detach in progress).
//
// Reconcile pattern: Target-state driven
func (r *Reconciler) reconcileRVAFinalizers(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rvas []*v1alpha1.ReplicatedVolumeAttachment,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "rva-finalizers")
	defer rf.OnEnd(&outcome)

	for _, rva := range rvas {
		if rva.DeletionTimestamp == nil {
			// Non-deleting: add finalizer if missing.

			// Skip if finalizer is already present.
			if obju.HasFinalizer(rva, v1alpha1.RVControllerFinalizer) {
				continue
			}

			// Add finalizer to ensure detach completes before RVA is deleted.
			base := rva.DeepCopy()
			obju.AddFinalizer(rva, v1alpha1.RVControllerFinalizer)
			if err := r.patchRVA(rf.Ctx(), rva, base); err != nil {
				return rf.Failf(err, "adding finalizer to RVA %s", rva.Name)
			}
		} else {
			// Deleting: remove finalizer if safe.

			// Skip if finalizer is already absent.
			if !obju.HasFinalizer(rva, v1alpha1.RVControllerFinalizer) {
				continue
			}

			// Safe to remove if another non-deleting RVA exists on the same node
			// (duplicate — the other RVA will maintain the attach).
			isDuplicate := hasOtherNonDeletingRVAOnNode(rvas, rva.Spec.NodeName, rva.Name)

			// Also safe to remove if the node is not attached and no detach is in progress
			// (nothing to wait for — detach already completed or was never started).
			if !isDuplicate && isNodeAttachedOrDetaching(rv, rva.Spec.NodeName) {
				continue
			}

			// Remove finalizer — RVA can be finalized.
			base := rva.DeepCopy()
			obju.RemoveFinalizer(rva, v1alpha1.RVControllerFinalizer)
			if err := r.patchRVA(rf.Ctx(), rva, base); err != nil {
				return rf.Failf(err, "removing finalizer from RVA %s", rva.Name)
			}
		}
	}

	return rf.Continue()
}

// isNodeAttachedOrDetaching returns true if the given node has an attached datamesh member
// or an active Detach transition. Returns false when rv is nil (no datamesh state).
func isNodeAttachedOrDetaching(rv *v1alpha1.ReplicatedVolume, nodeName string) bool {
	if rv == nil {
		return false
	}

	// Check for attached member on this node.
	for i := range rv.Status.Datamesh.Members {
		m := &rv.Status.Datamesh.Members[i]
		if m.NodeName == nodeName && m.Attached {
			return true
		}
	}

	// Check for active Detach transition targeting a replica on this node.
	for i := range rv.Status.DatameshTransitions {
		t := &rv.Status.DatameshTransitions[i]

		if t.Type != v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach {
			continue
		}

		// Look up the member by replicaName to get its nodeName.
		member := rv.Status.Datamesh.FindMemberByName(t.ReplicaName)
		if member != nil && member.NodeName == nodeName {
			return true
		}
	}

	return false
}

// hasOtherNonDeletingRVAOnNode returns true if there is another non-deleting RVA on the same node,
// excluding the RVA identified by excludeName.
func hasOtherNonDeletingRVAOnNode(rvas []*v1alpha1.ReplicatedVolumeAttachment, nodeName string, excludeName string) bool {
	for _, rva := range rvas {
		if rva.Name == excludeName {
			continue
		}

		if rva.DeletionTimestamp != nil {
			continue
		}

		if rva.Spec.NodeName == nodeName {
			return true
		}
	}
	return false
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: RVA waiting (RV unavailable / not ready)
//

// reconcileRVAWaiting sets "waiting" conditions on all RVAs when RV is unavailable or not ready:
// Attached=False/WaitingForReplicatedVolume, Ready=False/NotAttached, ReplicaReady removed,
// attachment fields cleared. The provided message distinguishes the reason.
//
// Used when:
//   - RV is not found (orphaned RVAs)
//   - RV is being deleted
//   - Datamesh formation is in progress
//
// Reconcile pattern: In-place reconciliation
func (r *Reconciler) reconcileRVAWaiting(
	ctx context.Context,
	rvas []*v1alpha1.ReplicatedVolumeAttachment,
	message string,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "rva-waiting")
	defer rf.OnEnd(&outcome)

	for _, rva := range rvas {
		// Check if conditions are already in sync.
		attachedInSync := obju.StatusCondition(rva, v1alpha1.ReplicatedVolumeAttachmentCondAttachedType).
			IsFalse().
			ReasonEqual(v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonWaitingForReplicatedVolume).
			MessageEqual(message).
			Eval()
		readyInSync := obju.StatusCondition(rva, v1alpha1.ReplicatedVolumeAttachmentCondReadyType).
			IsFalse().
			ReasonEqual(v1alpha1.ReplicatedVolumeAttachmentCondReadyReasonNotAttached).
			MessageEqual(message).
			Eval()
		replicaReadyAbsent := obju.StatusCondition(rva, v1alpha1.ReplicatedVolumeAttachmentCondReplicaReadyType).Absent().Eval()
		fieldsClear := isRVAAttachmentFieldsInSync(rva, nil)

		if attachedInSync && readyInSync && replicaReadyAbsent && fieldsClear {
			continue
		}

		base := rva.DeepCopy()

		obju.SetStatusCondition(rva, metav1.Condition{
			Type:    v1alpha1.ReplicatedVolumeAttachmentCondAttachedType,
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonWaitingForReplicatedVolume,
			Message: message,
		})
		obju.SetStatusCondition(rva, metav1.Condition{
			Type:    v1alpha1.ReplicatedVolumeAttachmentCondReadyType,
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha1.ReplicatedVolumeAttachmentCondReadyReasonNotAttached,
			Message: message,
		})
		obju.RemoveStatusCondition(rva, v1alpha1.ReplicatedVolumeAttachmentCondReplicaReadyType)
		clearRVAAttachmentFields(rva)

		if err := r.patchRVAStatus(rf.Ctx(), rva, base); err != nil {
			return rf.Failf(err, "patching RVA %s status", rva.Name)
		}
	}

	return rf.Continue()
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: orphaned RVAs (RV already deleted)
//

// reconcileOrphanedRVAs handles RVAs that reference a deleted RV.
// Loads RVAs by rvName, sets waiting conditions and removes RVControllerFinalizer from deleting RVAs.
//
// Reconcile pattern: Pure orchestration
func (r *Reconciler) reconcileOrphanedRVAs(
	ctx context.Context,
	rvName string,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "orphaned-rvas")
	defer rf.OnEnd(&outcome)

	rvas, err := r.getRVAsSorted(rf.Ctx(), rvName)
	if err != nil {
		return rf.Failf(err, "listing RVAs for deleted RV")
	}
	if len(rvas) == 0 {
		return rf.Done()
	}

	return flow.MergeReconciles(
		r.reconcileRVAWaiting(rf.Ctx(), rvas, "ReplicatedVolume not found; waiting for it to appear"),
		r.reconcileRVAFinalizers(rf.Ctx(), nil, rvas),
	)
}
