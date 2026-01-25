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
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"slices"
	"strings"
	"time"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/drbd_size"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

// ──────────────────────────────────────────────────────────────────────────────
// Wiring / construction
//

type Reconciler struct {
	cl     client.Client
	scheme *runtime.Scheme
	log    logr.Logger
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

func NewReconciler(cl client.Client, scheme *runtime.Scheme, log logr.Logger) *Reconciler {
	return &Reconciler{
		cl:     cl,
		scheme: scheme,
		log:    log,
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile
//

// Reconcile pattern: Pure orchestration
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	rf := flow.BeginRootReconcile(ctx)

	rvr, err := r.getRVR(rf.Ctx(), req.Name)
	if err != nil {
		return rf.Fail(err).ToCtrl()
	}

	drbdr, err := r.getDRBDR(rf.Ctx(), req.Name)
	if err != nil {
		return rf.Failf(err, "getting DRBDResource").ToCtrl()
	}

	llvs, err := r.getLLVs(rf.Ctx(), req.Name, drbdr)
	if err != nil {
		return rf.Failf(err, "listing LVMLogicalVolumes").ToCtrl()
	}

	rv, err := r.getRV(rf.Ctx(), rvr)
	if err != nil {
		return rf.Failf(err, "getting ReplicatedVolume %s", rvr.Spec.ReplicatedVolumeName).ToCtrl()
	}

	if rvr != nil {
		outcome := r.reconcileMetadata(rf.Ctx(), rvr, rv, llvs, drbdr)
		if outcome.ShouldReturn() {
			return outcome.ToCtrl()
		}
	}

	var base *v1alpha1.ReplicatedVolumeReplica
	if rvr != nil {
		base = rvr.DeepCopy()
	}

	readyLLVName, outcome := r.reconcileBackingVolume(rf.Ctx(), rvr, &llvs, rv, drbdr)
	if outcome.ShouldReturn() {
		return outcome.ToCtrl()
	}

	// TODO: pass readyLLVName to reconcileDRBDResource when it needs the backing volume name
	_ = readyLLVName

	outcome = outcome.Merge(r.reconcileDRBDResource(rf.Ctx(), rvr, rv, drbdr))
	if outcome.ShouldReturn() {
		return outcome.ToCtrl()
	}

	if outcome.DidChange() && rvr != nil {
		if err := r.patchRVRStatus(rf.Ctx(), rvr, base, outcome.OptimisticLockRequired()); err != nil {
			return rf.Fail(err).ToCtrl()
		}
	}

	return rf.Done().ToCtrl()
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: metadata (finalizers + labels)
//

// reconcileMetadata reconciles the RVR metadata (finalizers and labels).
//
// Reconcile pattern: Target-state driven
func (r *Reconciler) reconcileMetadata(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	rv *v1alpha1.ReplicatedVolume,
	llvs []snc.LVMLogicalVolume,
	drbdr *v1alpha1.DRBDResource,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "metadata")
	defer rf.OnEnd(&outcome)

	// Compute target finalizer state.
	shouldExist := !rvrShouldNotExist(rvr)
	hasLLVs := len(llvs) > 0
	hasDRBDR := drbdr != nil
	// Keep finalizer if RVR should exist or if there are still child resources.
	targetFinalizerPresent := shouldExist || hasLLVs || hasDRBDR

	// Compute actual LVG name from the LLV referenced by DRBDResource, or first LLV.
	var actualLVGName string
	if drbdr != nil && drbdr.Spec.LVMLogicalVolumeName != "" {
		if llv := findLLVByName(llvs, drbdr.Spec.LVMLogicalVolumeName); llv != nil {
			actualLVGName = llv.Spec.LVMVolumeGroupName
		}
	}
	if actualLVGName == "" && len(llvs) > 0 {
		actualLVGName = llvs[0].Spec.LVMVolumeGroupName
	}

	if isRVRMetadataInSync(rvr, rv, targetFinalizerPresent, actualLVGName) {
		return rf.Continue()
	}

	base := rvr.DeepCopy()
	applyRVRMetadata(rvr, rv, targetFinalizerPresent, actualLVGName)

	if err := r.patchRVR(rf.Ctx(), rvr, base, true); err != nil {
		return rf.Fail(err)
	}

	// If finalizer was removed, we're done (object will be deleted).
	if !targetFinalizerPresent {
		return rf.Done()
	}

	return rf.Continue()
}

// isRVRMetadataInSync checks if the RVR metadata (finalizer + labels) is in sync with the target state.
func isRVRMetadataInSync(rvr *v1alpha1.ReplicatedVolumeReplica, rv *v1alpha1.ReplicatedVolume, targetFinalizerPresent bool, targetLVGName string) bool {
	// Check finalizer.
	actualFinalizerPresent := obju.HasFinalizer(rvr, v1alpha1.RVRControllerFinalizer)
	if targetFinalizerPresent != actualFinalizerPresent {
		return false
	}

	// Check replicated-volume label.
	if rvr.Spec.ReplicatedVolumeName != "" {
		if !obju.HasLabelValue(rvr, v1alpha1.ReplicatedVolumeLabelKey, rvr.Spec.ReplicatedVolumeName) {
			return false
		}
	}

	// Check replicated-storage-class label.
	if rv != nil && rv.Spec.ReplicatedStorageClassName != "" {
		if !obju.HasLabelValue(rvr, v1alpha1.ReplicatedStorageClassLabelKey, rv.Spec.ReplicatedStorageClassName) {
			return false
		}
	}

	// Check lvm-volume-group label.
	if targetLVGName != "" {
		if !obju.HasLabelValue(rvr, v1alpha1.LVMVolumeGroupLabelKey, targetLVGName) {
			return false
		}
	}

	return true
}

// applyRVRMetadata applies the target metadata (finalizer + labels) to the RVR.
// Returns true if any metadata was changed.
func applyRVRMetadata(rvr *v1alpha1.ReplicatedVolumeReplica, rv *v1alpha1.ReplicatedVolume, targetFinalizerPresent bool, targetLVGName string) (changed bool) {
	// Apply finalizer.
	if targetFinalizerPresent {
		changed = obju.AddFinalizer(rvr, v1alpha1.RVRControllerFinalizer) || changed
	} else {
		changed = obju.RemoveFinalizer(rvr, v1alpha1.RVRControllerFinalizer) || changed
	}

	// Apply replicated-volume label.
	if rvr.Spec.ReplicatedVolumeName != "" {
		changed = obju.SetLabel(rvr, v1alpha1.ReplicatedVolumeLabelKey, rvr.Spec.ReplicatedVolumeName) || changed
	}

	// Apply replicated-storage-class label.
	// Note: node-name label is managed by rvr_scheduling_controller.
	if rv != nil && rv.Spec.ReplicatedStorageClassName != "" {
		changed = obju.SetLabel(rvr, v1alpha1.ReplicatedStorageClassLabelKey, rv.Spec.ReplicatedStorageClassName) || changed
	}

	// Apply lvm-volume-group label.
	if targetLVGName != "" {
		changed = obju.SetLabel(rvr, v1alpha1.LVMVolumeGroupLabelKey, targetLVGName) || changed
	}

	return changed
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: backing-volume
//

// Reconcile pattern: In-place reconciliation
func (r *Reconciler) reconcileBackingVolume(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	llvs *[]snc.LVMLogicalVolume,
	rv *v1alpha1.ReplicatedVolume,
	drbdr *v1alpha1.DRBDResource,
) (readyLLVName string, outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "backing-volume")
	defer rf.OnEnd(&outcome)

	// 1. Deletion branch: if RVR should not exist, delete all LLVs.
	if rvrShouldNotExist(rvr) {
		message := "Replica is being deleted; all backing volumes have been deleted"
		reason := v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonNotApplicable

		if len(*llvs) > 0 {
			deletingNames, ro := r.reconcileLLVsDeletion(rf.Ctx(), llvs, nil)
			if ro.ShouldReturn() {
				return "", ro
			}
			reason = v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonNotApplicable
			message = fmt.Sprintf("Replica is being deleted; deleting backing volumes: %s", strings.Join(deletingNames, ", "))
		}

		// Set BackingVolumeReady condition on RVR if rvr exists.
		changed := false
		if rvr != nil {
			changed = applyRVRBackingVolumeReadyCondFalse(rvr, reason, message)
		}

		return "", rf.Continue().ReportChangedIf(changed)
	}

	// 2. Compute actual and intended states.
	actual := computeActualBackingVolume(drbdr, *llvs)
	intended := computeIntendedBackingVolume(rvr, rv, actual)

	// 3. If intended == nil, determine why and set appropriate condition.
	if intended == nil {
		reason, message := computeBackingVolumeNotApplicableReason(rvr, rv)

		if len(*llvs) > 0 {
			deletingNames, ro := r.reconcileLLVsDeletion(rf.Ctx(), llvs, nil)
			if ro.ShouldReturn() {
				return "", ro
			}
			message = fmt.Sprintf("%s; deleting backing volumes: %s", message, strings.Join(deletingNames, ", "))
		}

		changed := applyRVRBackingVolumeReadyCondFalse(rvr, reason, message)
		return "", rf.Continue().ReportChangedIf(changed)
	}

	// 4. Find the intended LLV in the list.
	intendedLLV := findLLVByName(*llvs, intended.LLVName)

	// 5. Create if missing.
	if intendedLLV == nil {
		llv, err := newLLV(r.scheme, rvr, rv, intended)
		if err != nil {
			return "", rf.Failf(err, "constructing LLV %s", intended.LLVName)
		}

		if err := r.createLLV(rf.Ctx(), llv); err != nil {
			// Handle validation errors specially: log, set condition and requeue.
			// LVMLogicalVolume is not our API, so we treat validation errors as recoverable
			// for safety reasons (e.g., schema changes in sds-node-configurator).
			if apierrors.IsInvalid(err) {
				rf.Log().Error(err, "Failed to create backing volume", "llvName", intended.LLVName)
				applyRVRBackingVolumeReadyCondFalse(rvr,
					v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonProvisioningFailed,
					fmt.Sprintf("Failed to create backing volume %s: %s", intended.LLVName, computeAPIValidationErrorCauses(err)))
				if actual != nil {
					return actual.LLVName, rf.RequeueAfter(5 * time.Minute).ReportChanged()
				}
				return "", rf.RequeueAfter(5 * time.Minute).ReportChanged()
			}
			return "", rf.Failf(err, "creating LLV %s", intended.LLVName)
		}

		// Add newly created LLV to the slice for further processing.
		*llvs = append(*llvs, *llv)

		// Set condition: Provisioning or Reprovisioning.
		var reason, message string
		if actual != nil {
			reason = v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonReprovisioning
			message = fmt.Sprintf("Creating new backing volume %s to replace %s", intended.LLVName, actual.LLVName)
		} else {
			reason = v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonProvisioning
			message = fmt.Sprintf("Creating backing volume %s", intended.LLVName)
		}
		changed := applyRVRBackingVolumeReadyCondFalse(rvr, reason, message)

		// Return actual LLV name if exists, otherwise empty.
		if actual != nil {
			return actual.LLVName, rf.Continue().ReportChangedIf(changed)
		}
		return "", rf.Continue().ReportChangedIf(changed)
	}

	// 6. Ensure metadata (ownerRef, finalizer, label) on the intended LLV.
	// Note: In rare cases this results in two LLV patches per reconcile (metadata + resize).
	// This only happens during migration or manual interventions, so it's acceptable.
	if !isLLVMetadataInSync(rvr, rv, intendedLLV) {
		base := intendedLLV.DeepCopy()
		if _, err := applyLLVMetadata(r.scheme, rvr, rv, intendedLLV); err != nil {
			return "", rf.Failf(err, "applying LLV %s metadata", intendedLLV.Name)
		}
		if err := r.patchLLV(rf.Ctx(), intendedLLV, base, true); err != nil {
			return "", rf.Failf(err, "patching LLV %s metadata", intendedLLV.Name)
		}
	}

	// 7. Check if LLV is ready.
	if !isLLVReady(intendedLLV) {
		var reason, message string
		switch {
		case actual != nil && actual.LLVName == intended.LLVName:
			// LLV exists and is already used by DRBDResource, just not ready yet.
			reason = v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonNotReady
			message = fmt.Sprintf("Waiting for backing volume %s to become ready", intended.LLVName)
		case actual != nil:
			// Creating new LLV to replace existing one.
			reason = v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonReprovisioning
			message = fmt.Sprintf("Waiting for new backing volume %s to become ready (replacing %s)", intended.LLVName, actual.LLVName)
		default:
			// Creating new LLV from scratch.
			reason = v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonProvisioning
			message = fmt.Sprintf("Waiting for backing volume %s to become ready", intended.LLVName)
		}
		changed := applyRVRBackingVolumeReadyCondFalse(rvr, reason, message)

		if actual != nil {
			return actual.LLVName, rf.Continue().ReportChangedIf(changed)
		}
		return "", rf.Continue().ReportChangedIf(changed)
	}

	// 8. Resize if needed: LLV is ready, but size may need to grow.
	// Note: Status is guaranteed non-nil here because isLLVReady checks it.
	actualSize := intendedLLV.Status.ActualSize
	if actualSize.Cmp(intended.Size) < 0 {
		// Patch only if Spec.Size differs from intended.
		if intendedLLV.Spec.Size != intended.Size.String() {
			base := intendedLLV.DeepCopy()
			intendedLLV.Spec.Size = intended.Size.String()
			if err := r.patchLLV(rf.Ctx(), intendedLLV, base, true); err != nil {
				// Handle validation errors specially: log, set condition and requeue.
				// LVMLogicalVolume is not our API, so we treat validation errors as recoverable
				// for safety reasons (e.g., schema changes in sds-node-configurator).
				if apierrors.IsInvalid(err) {
					rf.Log().Error(err, "Failed to resize backing volume", "llvName", intended.LLVName)
					applyRVRBackingVolumeReadyCondFalse(rvr,
						v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonResizeFailed,
						fmt.Sprintf("Failed to resize backing volume %s: %s", intended.LLVName, computeAPIValidationErrorCauses(err)))
					return intended.LLVName, rf.RequeueAfter(5 * time.Minute).ReportChanged()
				}
				return "", rf.Failf(err, "patching LLV %s size", intendedLLV.Name)
			}
		}

		changed := applyRVRBackingVolumeReadyCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonResizing,
			fmt.Sprintf("Resizing backing volume %s from %s to %s", intended.LLVName, actualSize.String(), intended.Size.String()))

		if actual != nil {
			return actual.LLVName, rf.Continue().ReportChangedIf(changed)
		}
		return "", rf.Continue().ReportChangedIf(changed)
	}

	// 9. Fully ready: delete obsolete LLVs and set condition to Ready.
	// Keep actual LLV if it's different from intended (migration in progress).
	// But at this point intended is ready, so we can delete actual too.
	if len(*llvs) > 0 {
		if _, ro := r.reconcileLLVsDeletion(rf.Ctx(), llvs, []string{intended.LLVName}); ro.ShouldReturn() {
			return "", ro
		}
	}

	changed := applyRVRBackingVolumeReadyCondTrue(rvr,
		v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonReady,
		fmt.Sprintf("Backing volume %s is ready", intended.LLVName))

	return intended.LLVName, rf.Continue().ReportChangedIf(changed)
}

// rvrShouldNotExist returns true if RV is absent or RV is being deleted
// with only our finalizer remaining.
func rvrShouldNotExist(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
	if rvr == nil {
		return true
	}
	if rvr.DeletionTimestamp == nil {
		return false
	}
	// RV is being deleted; check if only our finalizer remains.
	return !obju.HasFinalizersOtherThan(rvr, v1alpha1.RVRControllerFinalizer)
}

// backingVolume represents the intended state of a backing volume for RVR.
// nil means no backing volume is needed (or configuration is incomplete).
type backingVolume struct {
	// LLVName is the intended name of the LVMLogicalVolume.
	LLVName string
	// LVMVolumeGroupName is the LVMVolumeGroup resource name.
	LVMVolumeGroupName string
	// ThinPoolName is the thin pool name (empty for thick volumes).
	ThinPoolName string
	// Size is the intended size of the backing volume.
	Size resource.Quantity
}

// computeActualBackingVolume extracts the actual backing volume state from DRBDResource and LLVs.
// Returns nil if DRBDResource is nil or has no LVMLogicalVolumeName.
func computeActualBackingVolume(drbdr *v1alpha1.DRBDResource, llvs []snc.LVMLogicalVolume) *backingVolume {
	if drbdr == nil {
		return nil
	}
	if drbdr.Spec.LVMLogicalVolumeName == "" {
		return nil
	}

	llvName := drbdr.Spec.LVMLogicalVolumeName

	llv := findLLVByName(llvs, llvName)
	if llv == nil {
		return nil
	}

	bv := &backingVolume{
		LLVName:            llvName,
		LVMVolumeGroupName: llv.Spec.LVMVolumeGroupName,
	}

	// Extract thin pool name if thin volume.
	if llv.Spec.Thin != nil {
		bv.ThinPoolName = llv.Spec.Thin.PoolName
	}

	// Get actual size from status.
	if llv.Status != nil {
		bv.Size = llv.Status.ActualSize
	}

	return bv
}

// computeIntendedBackingVolume computes the intended backing volume state.
// Returns nil if no backing volume is needed or configuration is incomplete.
//
// Algorithm:
// 1. Check if backing volume is needed:
//   - If member in datamesh: type == Diskful AND typeTransition != ToDiskless
//   - If NOT member: type == Diskful AND deletionTimestamp == nil
//
// 2. Get configuration from:
//   - If member in datamesh: use datamesh member's nodeName, lvmVolumeGroupName, lvmVolumeGroupThinPoolName
//   - If NOT member: use RVR spec's nodeName, lvmVolumeGroupName, lvmVolumeGroupThinPoolName
//
// 3. LLV name:
//   - If actual LLV exists on the same LVG/ThinPool: reuse actual.LLVName (migration support)
//   - Otherwise: generate new name as rvrName + "-" + fnv128(lvgName + thinPoolName)
func computeIntendedBackingVolume(rvr *v1alpha1.ReplicatedVolumeReplica, rv *v1alpha1.ReplicatedVolume, actual *backingVolume) *backingVolume {
	if rvr == nil {
		return nil
	}

	if rv == nil {
		// RV not found — can't compute intended, return actual as-is.
		return actual
	}

	// Find datamesh member.
	member := rv.Status.Datamesh.FindMemberByName(rvr.Name)

	// Check if backing volume is needed.
	bv := &backingVolume{}
	if member != nil {
		// Member in datamesh: needs backing volume if type is Diskful and NOT transitioning to diskless.
		//
		// DRBD disk addition/removal order:
		// - Adding disk: first configure all peers (transition from intentional diskless to diskless
		//   with bitmap), then add the local disk.
		// - Removing disk: first remove the local disk, then reconfigure peers.
		//
		// When TypeTransition == ToDiskless, we must remove the backing volume first.
		if member.Type != v1alpha1.ReplicaTypeDiskful ||
			member.TypeTransition == v1alpha1.ReplicatedVolumeDatameshMemberTypeTransitionToDiskless {
			return nil
		}

		// Check if configuration is complete (lvgName are required, nodeName is always set for member).
		if member.LVMVolumeGroupName == "" {
			return nil
		}

		// Use datamesh member configuration.
		bv.LVMVolumeGroupName = member.LVMVolumeGroupName
		bv.ThinPoolName = member.LVMVolumeGroupThinPoolName
	} else {
		// Not a member: needs backing volume if type is Diskful and RVR is not being deleted.
		if rvr.Spec.Type != v1alpha1.ReplicaTypeDiskful || rvrShouldNotExist(rvr) {
			return nil
		}

		// Check if configuration is complete (nodeName and lvgName are required).
		if rvr.Spec.NodeName == "" || rvr.Spec.LVMVolumeGroupName == "" {
			return nil
		}

		// Use RVR spec configuration.
		bv.LVMVolumeGroupName = rvr.Spec.LVMVolumeGroupName
		bv.ThinPoolName = rvr.Spec.LVMVolumeGroupThinPoolName
	}

	// Always use the size from datamesh, not from RV spec, even if we are not a member of datamesh.
	// The datamesh size represents the actual coordinated size across all replicas. Using RV spec size
	// could cause inconsistencies during resize operations when datamesh has not yet propagated the new size.
	bv.Size = drbd_size.LowerVolumeSize(rv.Status.Datamesh.Size)

	// Compute LLV name.
	// For migration: if actual LLV exists on the same LVG/ThinPool, reuse its name
	// (old naming scheme). Otherwise, generate a new deterministic name.
	if actual != nil &&
		actual.LVMVolumeGroupName == bv.LVMVolumeGroupName &&
		actual.ThinPoolName == bv.ThinPoolName {
		bv.LLVName = actual.LLVName
	} else {
		bv.LLVName = computeLLVName(rvr.Name, bv.LVMVolumeGroupName, bv.ThinPoolName)
	}

	return bv
}

// computeBackingVolumeNotApplicableReason determines the reason and message
// when backing volume cannot be computed (intended == nil).
// Returns (reason, message) for the BackingVolumeReady condition.
//
// This function mirrors the logic of computeIntendedBackingVolume to identify
// the specific cause of returning nil.
func computeBackingVolumeNotApplicableReason(rvr *v1alpha1.ReplicatedVolumeReplica, rv *v1alpha1.ReplicatedVolume) (reason, message string) {
	// If RV is nil, we can't determine precise reason.
	if rv == nil {
		return v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonNotApplicable,
			"Backing volume state cannot be determined (ReplicatedVolume not found)"
	}

	// Find datamesh member.
	member := rv.Status.Datamesh.FindMemberByName(rvr.Name)

	if member != nil {
		// Member in datamesh.
		if member.Type != v1alpha1.ReplicaTypeDiskful {
			return v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonNotApplicable,
				"Backing volume is not applicable for diskless replica type"
		}
		if member.TypeTransition == v1alpha1.ReplicatedVolumeDatameshMemberTypeTransitionToDiskless {
			return v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonNotApplicable,
				"Backing volume is being removed (transition to diskless)"
		}
		// Member is Diskful but LVMVolumeGroupName is empty.
		return v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonWaitingForConfiguration,
			"Waiting for storage assignment"
	}

	// Not a member.
	if rvr.Spec.Type != v1alpha1.ReplicaTypeDiskful {
		return v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonNotApplicable,
			"Backing volume is not applicable for diskless replica type"
	}

	if rvr.Spec.NodeName == "" {
		return v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonWaitingForConfiguration,
			"Waiting for node assignment"
	}

	// NodeName is set but LVMVolumeGroupName is empty.
	return v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonWaitingForConfiguration,
		"Waiting for storage assignment"
}

// computeLLVName computes the LVMLogicalVolume name for a given RVR.
// Format: rvrName + "-" + fnv128(lvgName + thinPoolName)
func computeLLVName(rvrName, lvgName, thinPoolName string) string {
	h := fnv.New128a()
	h.Write([]byte(lvgName))
	h.Write([]byte{0}) // separator
	h.Write([]byte(thinPoolName))
	checksum := hex.EncodeToString(h.Sum(nil))
	return rvrName + "-" + checksum
}

// newLLV constructs a new LVMLogicalVolume with ownerRef, finalizer, and labels.
func newLLV(scheme *runtime.Scheme, rvr *v1alpha1.ReplicatedVolumeReplica, rv *v1alpha1.ReplicatedVolume, bv *backingVolume) (*snc.LVMLogicalVolume, error) {
	llv := &snc.LVMLogicalVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name:       bv.LLVName,
			Labels:     map[string]string{},
			Finalizers: []string{v1alpha1.RVRControllerFinalizer},
		},
		Spec: snc.LVMLogicalVolumeSpec{
			ActualLVNameOnTheNode: bv.LLVName,
			LVMVolumeGroupName:    bv.LVMVolumeGroupName,
			Size:                  bv.Size.String(),
		},
	}

	// Add replicated-volume label.
	if rvr.Spec.ReplicatedVolumeName != "" {
		llv.Labels[v1alpha1.ReplicatedVolumeLabelKey] = rvr.Spec.ReplicatedVolumeName
	}

	// Add replicated-storage-class label.
	if rv != nil && rv.Spec.ReplicatedStorageClassName != "" {
		llv.Labels[v1alpha1.ReplicatedStorageClassLabelKey] = rv.Spec.ReplicatedStorageClassName
	}

	if bv.ThinPoolName == "" {
		llv.Spec.Type = "Thick"
	} else {
		llv.Spec.Type = "Thin"
		llv.Spec.Thin = &snc.LVMLogicalVolumeThinSpec{
			PoolName: bv.ThinPoolName,
		}
	}

	if _, err := obju.SetControllerRef(llv, rvr, scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference: %w", err)
	}

	return llv, nil
}

// isLLVReady checks if LLV is ready (Status != nil and Phase == "Created").
func isLLVReady(llv *snc.LVMLogicalVolume) bool {
	return llv != nil && llv.Status != nil && llv.Status.Phase == "Created"
}

// findLLVByName finds LLV in slice by name.
func findLLVByName(llvs []snc.LVMLogicalVolume, name string) *snc.LVMLogicalVolume {
	for i := range llvs {
		if llvs[i].Name == name {
			return &llvs[i]
		}
	}
	return nil
}

// computeAPIValidationErrorCauses extracts and formats causes from a Kubernetes validation error.
// Returns a human-readable string like "spec.size: Invalid value, spec.name: Required value".
func computeAPIValidationErrorCauses(err error) string {
	statusErr, ok := err.(*apierrors.StatusError)
	if !ok || statusErr.ErrStatus.Details == nil || len(statusErr.ErrStatus.Details.Causes) == 0 {
		return err.Error()
	}

	causes := statusErr.ErrStatus.Details.Causes
	parts := make([]string, 0, len(causes))
	for _, c := range causes {
		switch {
		case c.Field != "" && c.Message != "":
			parts = append(parts, fmt.Sprintf("%s: %s", c.Field, c.Message))
		case c.Message != "":
			parts = append(parts, c.Message)
		case c.Field != "":
			parts = append(parts, c.Field)
		}
	}

	if len(parts) == 0 {
		return err.Error()
	}
	return strings.Join(parts, "; ")
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: llvs-deletion
//

// reconcileLLVsDeletion deletes all LLVs except those in keep list.
// Returns names of LLVs being deleted (for condition messages) and outcome.
//
// Reconcile pattern: In-place reconciliation
func (r *Reconciler) reconcileLLVsDeletion(
	ctx context.Context,
	llvs *[]snc.LVMLogicalVolume,
	keep []string,
) (deletingNames []string, outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "llvs-deletion")
	defer rf.OnEnd(&outcome)

	// Collect names of LLVs to delete.
	deletingNames = make([]string, 0, len(*llvs))
	for i := range *llvs {
		name := (*llvs)[i].Name
		if !slices.Contains(keep, name) {
			deletingNames = append(deletingNames, name)
		}
	}

	// Delete LLVs in reverse order (to keep indices valid after slice deletion).
	for i := len(*llvs) - 1; i >= 0; i-- {
		llv := &(*llvs)[i]
		if slices.Contains(keep, llv.Name) {
			continue
		}

		// Remove our finalizer if present.
		if obju.HasFinalizer(llv, v1alpha1.RVRControllerFinalizer) {
			base := llv.DeepCopy()
			obju.RemoveFinalizer(llv, v1alpha1.RVRControllerFinalizer)
			if err := r.patchLLV(rf.Ctx(), llv, base, false); err != nil {
				return deletingNames, rf.Failf(err, "patching LLV %s", llv.Name)
			}
		}

		// Delete the LLV and remove from slice.
		if err := r.deleteLLV(rf.Ctx(), llv); err != nil {
			return deletingNames, rf.Failf(err, "deleting LLV %s", llv.Name)
		}
		*llvs = slices.Delete(*llvs, i, i+1)
	}

	return deletingNames, rf.Continue()
}

// isLLVMetadataInSync checks if ownerRef, finalizer, and labels are set on LLV.
func isLLVMetadataInSync(rvr *v1alpha1.ReplicatedVolumeReplica, rv *v1alpha1.ReplicatedVolume, llv *snc.LVMLogicalVolume) bool {
	if !obju.HasFinalizer(llv, v1alpha1.RVRControllerFinalizer) {
		return false
	}
	if !obju.HasControllerRef(llv, rvr) {
		return false
	}

	// Check replicated-volume label.
	if rvr.Spec.ReplicatedVolumeName != "" {
		if !obju.HasLabelValue(llv, v1alpha1.ReplicatedVolumeLabelKey, rvr.Spec.ReplicatedVolumeName) {
			return false
		}
	}

	// Check replicated-storage-class label.
	if rv != nil && rv.Spec.ReplicatedStorageClassName != "" {
		if !obju.HasLabelValue(llv, v1alpha1.ReplicatedStorageClassLabelKey, rv.Spec.ReplicatedStorageClassName) {
			return false
		}
	}

	return true
}

// applyLLVMetadata applies ownerRef, finalizer, and labels to LLV.
// Returns true if any changes were made.
func applyLLVMetadata(scheme *runtime.Scheme, rvr *v1alpha1.ReplicatedVolumeReplica, rv *v1alpha1.ReplicatedVolume, llv *snc.LVMLogicalVolume) (bool, error) {
	changed := false

	// Ensure finalizer.
	if obju.AddFinalizer(llv, v1alpha1.RVRControllerFinalizer) {
		changed = true
	}

	// Ensure replicated-volume label.
	if rvr.Spec.ReplicatedVolumeName != "" {
		if obju.SetLabel(llv, v1alpha1.ReplicatedVolumeLabelKey, rvr.Spec.ReplicatedVolumeName) {
			changed = true
		}
	}

	// Ensure replicated-storage-class label.
	if rv != nil && rv.Spec.ReplicatedStorageClassName != "" {
		if obju.SetLabel(llv, v1alpha1.ReplicatedStorageClassLabelKey, rv.Spec.ReplicatedStorageClassName) {
			changed = true
		}
	}

	// Ensure ownerRef.
	if ownerRefChanged, err := obju.SetControllerRef(llv, rvr, scheme); err != nil {
		return changed, fmt.Errorf("setting controller reference: %w", err)
	} else if ownerRefChanged {
		changed = true
	}

	return changed, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: drbd-resource
//

// Reconcile pattern: In-place reconciliation
func (r *Reconciler) reconcileDRBDResource(ctx context.Context, rvr *v1alpha1.ReplicatedVolumeReplica, rv *v1alpha1.ReplicatedVolume, drbdr *v1alpha1.DRBDResource) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "drbd-resource")
	defer rf.OnEnd(&outcome)

	if rvrShouldNotExist(rvr) {
		if drbdr != nil {
			// Remove our finalizer if present.
			if obju.HasFinalizer(drbdr, v1alpha1.RVRControllerFinalizer) {
				base := drbdr.DeepCopy()
				obju.RemoveFinalizer(drbdr, v1alpha1.RVRControllerFinalizer)
				if err := r.patchDRBDR(rf.Ctx(), drbdr, base, false); err != nil {
					return rf.Failf(err, "patching DRBDResource %s", drbdr.Name)
				}
			}

			// Delete the DRBDResource.
			if err := r.deleteDRBDR(rf.Ctx(), drbdr); err != nil {
				return rf.Failf(err, "deleting DRBDResource %s", drbdr.Name)
			}
		}

		// Set Configured condition on RVR.
		changed := false
		if rvr != nil {
			var message string
			if drbdr == nil {
				message = "Replica is being deleted; DRBD resource has been deleted"
			} else {
				message = fmt.Sprintf("Replica is being deleted; waiting for DRBD resource %s to be deleted", drbdr.Name)
			}
			changed = applyRVRConfiguredCondFalse(rvr,
				v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonNotApplicable, message)
		}

		return rf.Continue().ReportChangedIf(changed)
	}

	// TODO: implement DRBD resource reconciliation

	// rvr.status.effectiveType — надо заполнять
	// conditions[type=Configured]

	_ = rv

	return rf.Continue()
}

// applyRVRConfiguredCondFalse sets the Configured condition to False on RVR.
func applyRVRConfiguredCondFalse(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondConfiguredType,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

// applyRVRBackingVolumeReadyCondFalse sets the BackingVolumeReady condition to False on RVR.
func applyRVRBackingVolumeReadyCondFalse(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyType,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

// applyRVRBackingVolumeReadyCondTrue sets the BackingVolumeReady condition to True on RVR.
func applyRVRBackingVolumeReadyCondTrue(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyType,
		Status:  metav1.ConditionTrue,
		Reason:  reason,
		Message: message,
	})
}

// getRVR fetches the ReplicatedVolumeReplica by name.
// Returns (nil, nil) if not found.
func (r *Reconciler) getRVR(ctx context.Context, name string) (*v1alpha1.ReplicatedVolumeReplica, error) {
	var rvr v1alpha1.ReplicatedVolumeReplica
	if err := r.cl.Get(ctx, client.ObjectKey{Name: name}, &rvr); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &rvr, nil
}

// getLLVs returns LVMLogicalVolumes owned by the given RVR name.
// Also includes the LLV referenced by DRBDResource.Spec if present, because:
//   - the volume may be in the process of disk replacement (e.g., LVG or thin pool change),
//   - for migration we support the "old" naming scheme, so we also fetch the old
//     LLV name from DRBDResource just in case.
func (r *Reconciler) getLLVs(ctx context.Context, rvrName string, drbdr *v1alpha1.DRBDResource) ([]snc.LVMLogicalVolume, error) {
	var list snc.LVMLogicalVolumeList
	if err := r.cl.List(ctx, &list,
		client.MatchingFields{indexes.IndexFieldLLVByRVROwner: rvrName},
	); err != nil {
		return nil, err
	}

	// If DRBDResource references an LLV, ensure it's in the list.
	if drbdr != nil && drbdr.Spec.LVMLogicalVolumeName != "" {
		llvName := drbdr.Spec.LVMLogicalVolumeName

		// If not already in list, fetch and append.
		if !slices.ContainsFunc(list.Items, func(llv snc.LVMLogicalVolume) bool {
			return llv.Name == llvName
		}) {
			var llv snc.LVMLogicalVolume
			if err := r.cl.Get(ctx, client.ObjectKey{Name: llvName}, &llv); err != nil {
				if !apierrors.IsNotFound(err) {
					return nil, err
				}
				// LLV not found — that's ok, it may have been deleted.
			} else {
				list.Items = append(list.Items, llv)
			}
		}
	}

	return list.Items, nil
}

// getDRBDR fetches the DRBDResource by name.
// Returns (nil, nil) if not found.
func (r *Reconciler) getDRBDR(ctx context.Context, name string) (*v1alpha1.DRBDResource, error) {
	var drbdr v1alpha1.DRBDResource
	if err := r.cl.Get(ctx, client.ObjectKey{Name: name}, &drbdr); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &drbdr, nil
}

// getRV fetches the ReplicatedVolume by name from RVR spec.
// Returns (nil, nil) if not found.
func (r *Reconciler) getRV(ctx context.Context, rvr *v1alpha1.ReplicatedVolumeReplica) (*v1alpha1.ReplicatedVolume, error) {
	if rvr == nil || rvr.Spec.ReplicatedVolumeName == "" {
		return nil, nil
	}

	var rv v1alpha1.ReplicatedVolume
	if err := r.cl.Get(ctx, client.ObjectKey{Name: rvr.Spec.ReplicatedVolumeName}, &rv); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &rv, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Single-call I/O helper categories
//

// --- ReplicatedVolume (RV) ---

func (r *Reconciler) patchRVRStatus(ctx context.Context, obj, base *v1alpha1.ReplicatedVolumeReplica, optimisticLock bool) error {
	var patch client.Patch
	if optimisticLock {
		patch = client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})
	} else {
		patch = client.MergeFrom(base)
	}
	return r.cl.Status().Patch(ctx, obj, patch)
}

// --- ReplicatedVolumeReplica (RVR) ---

func (r *Reconciler) patchRVR(ctx context.Context, obj, base *v1alpha1.ReplicatedVolumeReplica, optimisticLock bool) error {
	var patch client.Patch
	if optimisticLock {
		patch = client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})
	} else {
		patch = client.MergeFrom(base)
	}
	return r.cl.Patch(ctx, obj, patch)
}

// --- LVMLogicalVolume (LLV) ---

func (r *Reconciler) patchLLV(ctx context.Context, obj, base *snc.LVMLogicalVolume, optimisticLock bool) error {
	var patch client.Patch
	if optimisticLock {
		patch = client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})
	} else {
		patch = client.MergeFrom(base)
	}
	return r.cl.Patch(ctx, obj, patch)
}

func (r *Reconciler) deleteLLV(ctx context.Context, llv *snc.LVMLogicalVolume) error {
	if llv.DeletionTimestamp != nil {
		return nil
	}
	return client.IgnoreNotFound(r.cl.Delete(ctx, llv))
}

func (r *Reconciler) createLLV(ctx context.Context, llv *snc.LVMLogicalVolume) error {
	return r.cl.Create(ctx, llv)
}

// --- DRBDResource (DRBDR) ---

func (r *Reconciler) patchDRBDR(ctx context.Context, obj, base *v1alpha1.DRBDResource, optimisticLock bool) error {
	var patch client.Patch
	if optimisticLock {
		patch = client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})
	} else {
		patch = client.MergeFrom(base)
	}
	return r.cl.Patch(ctx, obj, patch)
}

func (r *Reconciler) deleteDRBDR(ctx context.Context, drbdr *v1alpha1.DRBDResource) error {
	if drbdr.DeletionTimestamp != nil {
		return nil
	}
	return client.IgnoreNotFound(r.cl.Delete(ctx, drbdr))
}
