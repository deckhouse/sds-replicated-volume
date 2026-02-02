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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

// ──────────────────────────────────────────────────────────────────────────────
// Wiring / construction
//

type Reconciler struct {
	cl client.Client
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

func NewReconciler(cl client.Client) *Reconciler {
	return &Reconciler{cl: cl}
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile
//

// Reconcile pattern: Pure orchestration
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	rf := flow.BeginRootReconcile(ctx)

	// Get the ReplicatedVolume.
	rv, err := r.getRV(rf.Ctx(), req.Name)
	if err != nil {
		return rf.Failf(err, "getting ReplicatedVolume").ToCtrl()
	}
	if rv == nil {
		// NotFound: object deleted, nothing to do.
		return rf.Done().ToCtrl()
	}

	// Load RSC.
	rsc, err := r.getRSC(rf.Ctx(), rv.Spec.ReplicatedStorageClassName)
	if err != nil {
		return rf.Failf(err, "getting ReplicatedStorageClass").ToCtrl()
	}

	// Load child resources.
	rvas, err := r.getRVAs(rf.Ctx(), req.Name)
	if err != nil {
		return rf.Failf(err, "listing ReplicatedVolumeAttachments").ToCtrl()
	}

	rvrs, err := r.getRVRs(rf.Ctx(), req.Name)
	if err != nil {
		return rf.Failf(err, "listing ReplicatedVolumeReplicas").ToCtrl()
	}

	// Reconcile the RV metadata (finalizers and labels).
	outcome := r.reconcileMetadata(rf.Ctx(), rv, rvas, rvrs)
	if outcome.ShouldReturn() {
		return outcome.ToCtrl()
	}

	// Handle deletion.
	if rvShouldNotExist(rv) {
		return r.reconcileDeletion(rf.Ctx(), rv, rvas, rvrs).ToCtrl()
	}

	base := rv.DeepCopy()

	// Reconcile RV status: configuration and condition.
	outcome = outcome.WithChangeFrom(ensureRVConfiguration(rf.Ctx(), rv, rsc))
	if outcome.ShouldReturn() {
		return outcome.ToCtrl()
	}

	if outcome.DidChange() {
		if err := r.patchRVStatus(rf.Ctx(), rv, base, outcome.OptimisticLockRequired()); err != nil {
			return rf.Fail(err).ToCtrl()
		}
	}

	return rf.Done().ToCtrl()
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: metadata
//

// Reconcile pattern: Target-state driven
func (r *Reconciler) reconcileMetadata(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rvas []v1alpha1.ReplicatedVolumeAttachment,
	rvrs []v1alpha1.ReplicatedVolumeReplica,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "metadata")
	defer rf.OnEnd(&outcome)

	// Compute target finalizer state.
	// RV should exist if it has no DeletionTimestamp.
	shouldExist := rv.DeletionTimestamp == nil
	hasRVAs := len(rvas) > 0
	hasRVRs := len(rvrs) > 0
	// Keep finalizer if RV should exist or if there are still child resources.
	targetFinalizerPresent := shouldExist || hasRVAs || hasRVRs

	if isRVMetadataInSync(rv, targetFinalizerPresent) {
		return rf.Continue()
	}

	base := rv.DeepCopy()
	applyRVMetadata(rv, targetFinalizerPresent)

	if err := r.patchRV(rf.Ctx(), rv, base, true); err != nil {
		return rf.Fail(err)
	}

	// If finalizer was removed, we're done (object will be deleted).
	if !targetFinalizerPresent {
		return rf.Done()
	}

	return rf.Continue()
}

// isRVMetadataInSync checks if the RV metadata (finalizer + labels) is in sync with the target state.
func isRVMetadataInSync(rv *v1alpha1.ReplicatedVolume, targetFinalizerPresent bool) bool {
	// Check finalizer.
	actualFinalizerPresent := obju.HasFinalizer(rv, v1alpha1.RVControllerFinalizer)
	if actualFinalizerPresent != targetFinalizerPresent {
		return false
	}

	// Check replicated-storage-class label.
	if rv.Spec.ReplicatedStorageClassName != "" {
		if !obju.HasLabelValue(rv, v1alpha1.ReplicatedStorageClassLabelKey, rv.Spec.ReplicatedStorageClassName) {
			return false
		}
	}

	return true
}

// applyRVMetadata applies finalizer and labels to RV.
// Returns true if any metadata was changed.
func applyRVMetadata(rv *v1alpha1.ReplicatedVolume, targetFinalizerPresent bool) (changed bool) {
	// Apply finalizer.
	if targetFinalizerPresent {
		changed = obju.AddFinalizer(rv, v1alpha1.RVControllerFinalizer) || changed
	} else {
		changed = obju.RemoveFinalizer(rv, v1alpha1.RVControllerFinalizer) || changed
	}

	// Apply replicated-storage-class label.
	if rv.Spec.ReplicatedStorageClassName != "" {
		changed = obju.SetLabel(rv, v1alpha1.ReplicatedStorageClassLabelKey, rv.Spec.ReplicatedStorageClassName) || changed
	}

	return changed
}

// ensureRVConfiguration ensures RV configuration is initialized from RSC and sets ConfigurationReady condition.
func ensureRVConfiguration(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rsc *v1alpha1.ReplicatedStorageClass,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "configuration")
	defer ef.OnEnd(&outcome)

	changed := false

	// Guard: RSC not found.
	if rsc == nil {
		changed = applyConfigurationReadyCondFalse(rv,
			v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonWaitingForStorageClass,
			fmt.Sprintf("ReplicatedStorageClass %q not found", rv.Spec.ReplicatedStorageClassName)) || changed
		return ef.Ok().ReportChangedIf(changed)
	}

	// Guard: RSC has no configuration.
	if rsc.Status.Configuration == nil {
		changed = applyConfigurationReadyCondFalse(rv,
			v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonWaitingForStorageClass,
			fmt.Sprintf("ReplicatedStorageClass %q configuration not ready", rsc.Name)) || changed
		return ef.Ok().ReportChangedIf(changed)
	}

	// Initialize configuration if not set.
	if rv.Status.Configuration == nil {
		rv.Status.Configuration = rsc.Status.Configuration.DeepCopy()
		rv.Status.ConfigurationGeneration = rsc.Status.ConfigurationGeneration
		changed = true
	}

	// If ConfigurationGeneration is not set, configuration rollout is in progress.
	if rv.Status.ConfigurationGeneration == 0 {
		changed = applyConfigurationReadyCondFalse(rv,
			v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonConfigurationRolloutInProgress,
			"") || changed
		return ef.Ok().ReportChangedIf(changed)
	}

	// Check if generation matches.
	if rv.Status.ConfigurationGeneration == rsc.Status.ConfigurationGeneration {
		changed = applyConfigurationReadyCondTrue(rv,
			v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonReady,
			"Configuration matches storage class") || changed
	} else {
		changed = applyConfigurationReadyCondFalse(rv,
			v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonStaleConfiguration,
			fmt.Sprintf("Configuration generation %d does not match storage class generation %d",
				rv.Status.ConfigurationGeneration, rsc.Status.ConfigurationGeneration)) || changed
	}

	return ef.Ok().ReportChangedIf(changed)
}

// applyConfigurationReadyCondTrue sets ConfigurationReady condition to True.
func applyConfigurationReadyCondTrue(rv *v1alpha1.ReplicatedVolume, reason, message string) bool {
	return obju.SetStatusCondition(rv, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeCondConfigurationReadyType,
		Status:  metav1.ConditionTrue,
		Reason:  reason,
		Message: message,
	})
}

// applyConfigurationReadyCondFalse sets ConfigurationReady condition to False.
func applyConfigurationReadyCondFalse(rv *v1alpha1.ReplicatedVolume, reason, message string) bool {
	return obju.SetStatusCondition(rv, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeCondConfigurationReadyType,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

// rvShouldNotExist returns true if RV should be deleted:
// DeletionTimestamp is set, no finalizers except ours, and no attached members.
func rvShouldNotExist(rv *v1alpha1.ReplicatedVolume) bool {
	if rv == nil {
		return true
	}

	if rv.DeletionTimestamp == nil {
		return false
	}

	// Check no other finalizers except ours.
	if obju.HasFinalizersOtherThan(rv, v1alpha1.RVControllerFinalizer) {
		return false
	}

	// Check no attached members.
	for i := range rv.Status.Datamesh.Members {
		if rv.Status.Datamesh.Members[i].Attached {
			return false
		}
	}

	return true
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: deletion
//

// Reconcile pattern: In-place reconciliation
func (r *Reconciler) reconcileDeletion(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rvas []v1alpha1.ReplicatedVolumeAttachment,
	rvrs []v1alpha1.ReplicatedVolumeReplica,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "deletion")
	defer rf.OnEnd(&outcome)

	// Step 1: Update all RVA conditions.
	for i := range rvas {
		rva := &rvas[i]
		if isRVADeletionConditionsInSync(rva) {
			continue
		}

		base := rva.DeepCopy()
		applyRVADeletionConditions(rva)

		if err := r.patchRVAStatus(rf.Ctx(), rva, base, false); err != nil {
			return rf.Failf(err, "patching RVA %s status", rva.Name)
		}
	}

	// Step 2: Remove finalizers from RVRs and delete them.
	for i := range rvrs {
		rvr := &rvrs[i]

		// Remove our finalizer if present.
		if obju.HasFinalizer(rvr, v1alpha1.RVControllerFinalizer) {
			base := rvr.DeepCopy()
			obju.RemoveFinalizer(rvr, v1alpha1.RVControllerFinalizer)
			if err := r.patchRVR(rf.Ctx(), rvr, base, false); err != nil {
				return rf.Failf(err, "patching RVR %s", rvr.Name)
			}
		}

		// Delete the RVR.
		if err := r.deleteRVR(rf.Ctx(), rvr); err != nil {
			return rf.Failf(err, "deleting RVR %s", rvr.Name)
		}
	}

	// Step 3: Clear datamesh members.
	if len(rv.Status.Datamesh.Members) > 0 {
		base := rv.DeepCopy()
		rv.Status.Datamesh.Members = nil
		if err := r.patchRVStatus(rf.Ctx(), rv, base, false); err != nil {
			return rf.Failf(err, "clearing datamesh members")
		}
	}

	// We're done. Don't continue further reconciliation.
	return rf.Done()
}

// isRVADeletionConditionsInSync checks if RVA has the expected conditions for RV deletion.
func isRVADeletionConditionsInSync(rva *v1alpha1.ReplicatedVolumeAttachment) bool {
	// Should have exactly 2 conditions: Attached and Ready.
	if len(rva.Status.Conditions) != 2 {
		return false
	}

	attachedCond := obju.GetStatusCondition(rva, v1alpha1.ReplicatedVolumeAttachmentCondAttachedType)
	if attachedCond == nil ||
		attachedCond.Status != metav1.ConditionFalse ||
		attachedCond.Reason != v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonWaitingForReplicatedVolume {
		return false
	}

	readyCond := obju.GetStatusCondition(rva, v1alpha1.ReplicatedVolumeAttachmentCondReadyType)
	if readyCond == nil ||
		readyCond.Status != metav1.ConditionFalse ||
		readyCond.Reason != v1alpha1.ReplicatedVolumeAttachmentCondReadyReasonNotAttached {
		return false
	}

	return true
}

// applyRVADeletionConditions sets RVA conditions for RV deletion:
// Attached=False (WaitingForReplicatedVolume), Ready=False (NotAttached).
// Removes all other conditions.
func applyRVADeletionConditions(rva *v1alpha1.ReplicatedVolumeAttachment) {
	rva.Status.Conditions = []metav1.Condition{
		{
			Type:    v1alpha1.ReplicatedVolumeAttachmentCondAttachedType,
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonWaitingForReplicatedVolume,
			Message: "ReplicatedVolume is being deleted",
		},
		{
			Type:    v1alpha1.ReplicatedVolumeAttachmentCondReadyType,
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha1.ReplicatedVolumeAttachmentCondReadyReasonNotAttached,
			Message: "ReplicatedVolume is being deleted",
		},
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Single-call I/O helpers
//

// --- RV ---

func (r *Reconciler) getRV(ctx context.Context, name string) (*v1alpha1.ReplicatedVolume, error) {
	var rv v1alpha1.ReplicatedVolume
	if err := r.cl.Get(ctx, client.ObjectKey{Name: name}, &rv); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return nil, err
		}
		return nil, nil
	}
	return &rv, nil
}

func (r *Reconciler) patchRV(ctx context.Context, obj, base *v1alpha1.ReplicatedVolume, optimisticLock bool) error {
	var patch client.Patch
	if optimisticLock {
		patch = client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})
	} else {
		patch = client.MergeFrom(base)
	}
	return r.cl.Patch(ctx, obj, patch)
}

func (r *Reconciler) patchRVStatus(ctx context.Context, obj, base *v1alpha1.ReplicatedVolume, optimisticLock bool) error {
	var patch client.Patch
	if optimisticLock {
		patch = client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})
	} else {
		patch = client.MergeFrom(base)
	}
	return r.cl.Status().Patch(ctx, obj, patch)
}

// --- RSC ---

func (r *Reconciler) getRSC(ctx context.Context, name string) (*v1alpha1.ReplicatedStorageClass, error) {
	var rsc v1alpha1.ReplicatedStorageClass
	if err := r.cl.Get(ctx, client.ObjectKey{Name: name}, &rsc); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return nil, err
		}
		return nil, nil
	}
	return &rsc, nil
}

// --- RVA ---

func (r *Reconciler) getRVAs(ctx context.Context, rvName string) ([]v1alpha1.ReplicatedVolumeAttachment, error) {
	var list v1alpha1.ReplicatedVolumeAttachmentList
	if err := r.cl.List(ctx, &list,
		client.MatchingFields{indexes.IndexFieldRVAByReplicatedVolumeName: rvName},
	); err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (r *Reconciler) patchRVAStatus(ctx context.Context, obj, base *v1alpha1.ReplicatedVolumeAttachment, optimisticLock bool) error {
	var patch client.Patch
	if optimisticLock {
		patch = client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})
	} else {
		patch = client.MergeFrom(base)
	}
	return r.cl.Status().Patch(ctx, obj, patch)
}

// --- RVR ---

func (r *Reconciler) getRVRs(ctx context.Context, rvName string) ([]v1alpha1.ReplicatedVolumeReplica, error) {
	var list v1alpha1.ReplicatedVolumeReplicaList
	if err := r.cl.List(ctx, &list,
		client.MatchingFields{indexes.IndexFieldRVRByReplicatedVolumeName: rvName},
	); err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (r *Reconciler) patchRVR(ctx context.Context, obj, base *v1alpha1.ReplicatedVolumeReplica, optimisticLock bool) error {
	var patch client.Patch
	if optimisticLock {
		patch = client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})
	} else {
		patch = client.MergeFrom(base)
	}
	return r.cl.Patch(ctx, obj, patch)
}

func (r *Reconciler) deleteRVR(ctx context.Context, obj *v1alpha1.ReplicatedVolumeReplica) error {
	if obj.DeletionTimestamp != nil {
		return nil
	}
	if err := client.IgnoreNotFound(r.cl.Delete(ctx, obj)); err != nil {
		return err
	}
	now := metav1.Now()
	obj.DeletionTimestamp = &now
	return nil
}
