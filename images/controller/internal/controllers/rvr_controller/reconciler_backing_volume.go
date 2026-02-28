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
	"fmt"
	"slices"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/drbd_size"
	rvrllvname "github.com/deckhouse/sds-replicated-volume/images/controller/internal/rvr_llv_name"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

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
	rspView *rspEligibilityView,
) (targetBV, intendedBV *backingVolume, outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "backing-volume")
	defer rf.OnEnd(&outcome)

	// 1. Deletion branch: if RVR should not exist, delete all LLVs.
	if rvrShouldNotExist(rvr) {
		if len(*llvs) > 0 {
			deletingNames, ro := r.reconcileLLVsDeletion(rf.Ctx(), llvs, nil)
			if ro.ShouldReturn() {
				return nil, nil, ro
			}
			// rvr can be nil here if it was already deleted; nothing to update in that case.
			if rvr == nil {
				return nil, nil, rf.Continue()
			}
			// Still deleting — set condition False.
			changed := applyBackingVolumeReadyCondFalse(rvr,
				v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonNotApplicable,
				fmt.Sprintf("Replica is being deleted; deleting backing volumes: %s", strings.Join(deletingNames, ", ")))
			return nil, nil, rf.Continue().ReportChangedIf(changed)
		}

		// All LLVs deleted.
		// If rvr is nil (already deleted), just return.
		if rvr == nil {
			return nil, nil, rf.Continue()
		}
		// Remove condition entirely.
		changed := applyBackingVolumeReadyCondAbsent(rvr)
		return nil, nil, rf.Continue().ReportChangedIf(changed)
	}

	// 2. Compute actual state.
	actual := computeActualBackingVolume(drbdr, *llvs)

	// 3. ReplicatedVolume not found — stop reconciliation and wait for it to appear.
	// Without RV we cannot determine datamesh state.
	if rv == nil {
		changed := applyBackingVolumeReadyCondUnknown(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonWaitingForReplicatedVolume,
			"ReplicatedVolume not found; waiting for it to be present")
		return actual, nil, rf.Continue().ReportChangedIf(changed)
	}

	// 4. Datamesh not initialized yet — wait for RV controller to set it up.
	// Normally datamesh is already initialized by the time RVR is created,
	// but we check for non-standard usage scenarios (e.g., RVR created before RV) and general correctness.
	if rv.Status.DatameshRevision == 0 {
		changed := applyBackingVolumeReadyCondUnknown(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonWaitingForReplicatedVolume,
			"Datamesh is not initialized yet; waiting for ReplicatedVolume to initialize it")
		return actual, nil, rf.Continue().ReportChangedIf(changed)
	}

	// 5. Compute intended state.
	intended, reason, message := computeIntendedBackingVolume(rvr, rv, actual, rspView)

	// 6. If intended == nil, delete LLVs and set/remove condition based on reason.
	if intended == nil {
		// Use Unknown when waiting for external prerequisites (RV/RSP),
		// False for all other reasons (PendingScheduling, NotApplicable, etc.).
		applyBVReadyCond := applyBackingVolumeReadyCondFalse
		if reason == v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonWaitingForReplicatedVolume {
			applyBVReadyCond = applyBackingVolumeReadyCondUnknown
		}

		if len(*llvs) > 0 {
			deletingNames, ro := r.reconcileLLVsDeletion(rf.Ctx(), llvs, nil)
			if ro.ShouldReturn() {
				return nil, nil, ro
			}
			// Still deleting — set condition.
			changed := applyBVReadyCond(rvr, reason,
				fmt.Sprintf("%s; deleting backing volumes: %s", message, strings.Join(deletingNames, ", ")))
			return nil, nil, rf.Continue().ReportChangedIf(changed)
		}

		// No LLVs left. If backing volume is genuinely not applicable, remove condition entirely.
		// Otherwise keep the condition with the reason (e.g. PendingScheduling).
		if reason == v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonNotApplicable {
			changed := applyBackingVolumeReadyCondAbsent(rvr)
			return nil, nil, rf.Continue().ReportChangedIf(changed)
		}
		changed := applyBVReadyCond(rvr, reason, message)
		return nil, nil, rf.Continue().ReportChangedIf(changed)
	}

	// 7. Find the intended LLV in the list.
	intendedLLV := findLLVByName(*llvs, intended.LLVName)

	// 8. If the intended LLV exists but is being deleted, wait for deletion to complete.
	// This happens when an RVR is recreated while the old LLV from the previous incarnation
	// is still being deleted (e.g., held by sds-node-configurator's finalizer).
	// We cannot create a new LLV with the same name until the old one is fully removed.
	//
	// IMPORTANT: This guard applies ONLY to RVRs that are not yet datamesh members.
	// For active datamesh members, a deleting LLV is an abnormal situation (e.g., accidental
	// manual deletion) and we must NOT help delete it by removing our finalizer — the existing
	// steps 9–12 will handle it (report NotReady, etc.).
	if intendedLLV != nil && intendedLLV.DeletionTimestamp != nil &&
		rv.Status.Datamesh.FindMemberByName(rvr.Name) == nil {
		// Remove our finalizer from the deleting LLV if still present
		// (help speed up deletion in case it was missed by the previous incarnation).
		if obju.HasFinalizer(intendedLLV, v1alpha1.RVRControllerFinalizer) {
			base := intendedLLV.DeepCopy()
			obju.RemoveFinalizer(intendedLLV, v1alpha1.RVRControllerFinalizer)
			if err := r.patchLLV(rf.Ctx(), intendedLLV, base); err != nil {
				return nil, nil, rf.Failf(err, "removing finalizer from deleting LLV %s", intendedLLV.Name)
			}
		}

		changed := applyBackingVolumeReadyCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonProvisioning,
			fmt.Sprintf("Waiting for old backing volume %s to be fully deleted", intended.LLVName))
		return actual, intended, rf.Continue().ReportChangedIf(changed)
	}

	// 9. Create if missing.
	if intendedLLV == nil {
		llv, err := newLLV(r.scheme, rvr, rv, intended)
		if err != nil {
			return nil, nil, rf.Failf(err, "constructing LLV %s", intended.LLVName)
		}

		if err := r.createLLV(rf.Ctx(), llv); err != nil {
			if apierrors.IsAlreadyExists(err) {
				// Concurrent reconciliation created this LLV. Requeue to pick it up from cache.
				rf.Log().Info("LLV already exists, requeueing", "llvName", intended.LLVName)
				return nil, nil, rf.DoneAndRequeue()
			}
			// Handle validation errors specially: log, set condition and requeue.
			// LVMLogicalVolume is not our API, so we treat validation errors as recoverable
			// for safety reasons (e.g., schema changes in sds-node-configurator).
			if apierrors.IsInvalid(err) {
				rf.Log().Error(err, "Failed to create backing volume", "llvName", intended.LLVName)
				applyBackingVolumeReadyCondFalse(rvr,
					v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonProvisioningFailed,
					fmt.Sprintf("Failed to create backing volume %s: %s", intended.LLVName, computeAPIValidationErrorCauses(err)))
				return actual, intended, rf.ContinueAndRequeueAfter(5 * time.Minute).ReportChanged()
			}
			return nil, nil, rf.Failf(err, "creating LLV %s", intended.LLVName)
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
		changed := applyBackingVolumeReadyCondFalse(rvr, reason, message)

		// Return actual BV as target.
		return actual, intended, rf.Continue().ReportChangedIf(changed)
	}

	// 10. Ensure metadata (ownerRef, finalizer, label) on the intended LLV.
	// Note: In rare cases this results in two LLV patches per reconcile (metadata + resize).
	// This only happens during migration or manual interventions, so it's acceptable.
	if !isLLVMetadataInSync(rvr, rv, intendedLLV) {
		base := intendedLLV.DeepCopy()
		if _, err := applyLLVMetadata(r.scheme, rvr, rv, intendedLLV); err != nil {
			return nil, nil, rf.Failf(err, "applying LLV %s metadata", intendedLLV.Name)
		}
		if err := r.patchLLV(rf.Ctx(), intendedLLV, base); err != nil {
			return nil, nil, rf.Failf(err, "patching LLV %s metadata", intendedLLV.Name)
		}
	}

	// 11. Check if LLV is ready.
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
		changed := applyBackingVolumeReadyCondFalse(rvr, reason, message)
		return actual, intended, rf.Continue().ReportChangedIf(changed)
	}

	// 12. Resize if needed: LLV is ready, but size may need to grow.
	// Note: Status is guaranteed non-nil here because isLLVReady checks it.
	actualSize := intendedLLV.Status.ActualSize
	if actualSize.Cmp(intended.Size) < 0 {
		// Patch only if Spec.Size differs from intended.
		if intendedLLV.Spec.Size != intended.Size.String() {
			base := intendedLLV.DeepCopy()
			intendedLLV.Spec.Size = intended.Size.String()
			if err := r.patchLLV(rf.Ctx(), intendedLLV, base); err != nil {
				// Handle validation errors specially: log, set condition and requeue.
				// LVMLogicalVolume is not our API, so we treat validation errors as recoverable
				// for safety reasons (e.g., schema changes in sds-node-configurator).
				if apierrors.IsInvalid(err) {
					rf.Log().Error(err, "Failed to resize backing volume", "llvName", intended.LLVName)
					applyBackingVolumeReadyCondFalse(rvr,
						v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonResizeFailed,
						fmt.Sprintf("Failed to resize backing volume %s: %s", intended.LLVName, computeAPIValidationErrorCauses(err)))
					return actual, intended, rf.ContinueAndRequeueAfter(5 * time.Minute).ReportChanged()
				}
				return nil, nil, rf.Failf(err, "patching LLV %s size", intendedLLV.Name)
			}
		}

		changed := applyBackingVolumeReadyCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonResizing,
			fmt.Sprintf("Resizing backing volume %s from %s to %s", intended.LLVName, actualSize.String(), intended.Size.String()))

		return actual, intended, rf.Continue().ReportChangedIf(changed)
	}

	// 13. Fully ready: delete obsolete LLVs and set condition to Ready.
	// Keep actual LLV if it's different from intended (migration in progress).
	// But at this point intended is ready, so we can delete actual too.
	message = fmt.Sprintf("Backing volume %s is ready", intended.LLVName)
	if len(*llvs) > 0 {
		var ro flow.ReconcileOutcome
		deletingNames, ro := r.reconcileLLVsDeletion(rf.Ctx(), llvs, []string{intended.LLVName})
		if ro.ShouldReturn() {
			return nil, nil, ro
		}
		if len(deletingNames) > 0 {
			message = fmt.Sprintf("%s; deleting obsolete: %s", message, strings.Join(deletingNames, ", "))
		}
	}
	changed := applyBackingVolumeReadyCondTrue(rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonReady, message)

	return intended, intended, rf.Continue().ReportChangedIf(changed)
}

// rvrShouldNotExist returns true if RV is absent or RV is being deleted
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

// LLVNameOrEmpty returns the LLV name or empty string if bv is nil.
func (bv *backingVolume) LLVNameOrEmpty() string {
	if bv == nil {
		return ""
	}
	return bv.LLVName
}

// Equal returns true if bv and other are equal (both nil, or all fields match).
func (bv *backingVolume) Equal(other *backingVolume) bool {
	if bv == nil && other == nil {
		return true
	}
	if bv == nil || other == nil {
		return false
	}
	return bv.LLVName == other.LLVName &&
		bv.LVMVolumeGroupName == other.LVMVolumeGroupName &&
		bv.ThinPoolName == other.ThinPoolName &&
		bv.Size.Cmp(other.Size) == 0
}

// computeActualBackingVolume extracts the actual backing volume state from DRBDResource and LLVs.
// Returns nil if DRBDResource is nil, has no LVMLogicalVolumeName, or the referenced LLV is not found.
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
//
// Returns (intended, reason, message):
//   - intended != nil: backing volume is needed, reason and message are empty.
//   - intended == nil: backing volume is not applicable, reason and message explain why.
//
// Algorithm:
// 1. Check if backing volume is needed:
//   - If member in datamesh: NeedsBackingVolume()
//   - If NOT member: type == Diskful AND deletionTimestamp == nil
//
// 2. Get configuration from:
//   - If member in datamesh: use datamesh member's nodeName, lvmVolumeGroupName, lvmVolumeGroupThinPoolName
//   - If NOT member: use RVR spec's nodeName, lvmVolumeGroupName, lvmVolumeGroupThinPoolName
//
// 3. LLV name:
//   - If actual LLV exists on the same LVG/ThinPool: reuse actual.LLVName (migration support)
//   - Otherwise: generate new name as rvrName + "-" + fnv128(lvgName + thinPoolName)
func computeIntendedBackingVolume(rvr *v1alpha1.ReplicatedVolumeReplica, rv *v1alpha1.ReplicatedVolume, actual *backingVolume, rspView *rspEligibilityView) (intended *backingVolume, reason, message string) {
	if rvr == nil {
		panic("computeIntendedBackingVolume: rvr is nil")
	}
	if rv == nil {
		panic("computeIntendedBackingVolume: rv is nil")
	}

	// Find datamesh member.
	member := rv.Status.Datamesh.FindMemberByName(rvr.Name)

	// Check if backing volume is needed.
	bv := &backingVolume{}
	if member != nil {
		// DRBD disk addition/removal order for member:
		// - Adding disk: first configure all peers (transition from intentional diskless to diskless
		//   with bitmap), then add the local disk.
		// - Removing disk: first remove the local disk, then reconfigure peers.
		//
		// The liminal stage exists precisely for this: the member is already Diskful (peers see it,
		// quorum counts it) but DRBD is configured as diskless — enabling the peer setup phase
		// before/after the actual disk attach/detach.
		//
		// Backing volume is needed for all Diskful members, including liminal ones.
		// Liminal members maintain their backing volume; it is just not referenced in the DRBDR spec.
		if !member.Type.NeedsBackingVolume() {
			return nil, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonNotApplicable,
				"Backing volume is not applicable for diskless replica type"
		}

		// Check if configuration is complete (lvgName are required, nodeName is always set for member).
		if member.LVMVolumeGroupName == "" {
			return nil, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonPendingScheduling,
				"Waiting for storage assignment"
		}

		// Use datamesh member configuration.
		bv.LVMVolumeGroupName = member.LVMVolumeGroupName
		bv.ThinPoolName = member.LVMVolumeGroupThinPoolName
	} else {
		// Not a member: needs backing volume if type is Diskful.
		if rvr.Spec.Type != v1alpha1.ReplicaTypeDiskful {
			return nil, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonNotApplicable,
				"Backing volume is not applicable for diskless replica type"
		}

		// Check if configuration is complete (nodeName and lvgName are required).
		if rvr.Spec.NodeName == "" {
			return nil, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonPendingScheduling,
				"Waiting for node assignment"
		}
		if rvr.Spec.LVMVolumeGroupName == "" {
			return nil, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonPendingScheduling,
				"Waiting for storage assignment"
		}

		// Validate RSP eligibility and storage assignment.
		code, msg := rspView.isStorageEligible(rvr.Spec.LVMVolumeGroupName, rvr.Spec.LVMVolumeGroupThinPoolName)
		if code != storageEligibilityOK {
			var reason string
			switch code {
			case storageEligibilityRSPNotAvailable:
				reason = v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonWaitingForReplicatedVolume
			default:
				reason = v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonPendingScheduling
			}
			return nil, reason, msg
		}

		// Use RVR spec configuration.
		bv.LVMVolumeGroupName = rvr.Spec.LVMVolumeGroupName
		bv.ThinPoolName = rvr.Spec.LVMVolumeGroupThinPoolName
	}

	// Use the larger of RV spec size and datamesh size.
	// During resize, datamesh will lag behind spec until all backing volumes are ready.
	// We could just use spec.size, but we take the max for correctness in case of non-standard interventions.
	size := rv.Status.Datamesh.Size
	if rv.Spec.Size.Cmp(size) > 0 {
		size = rv.Spec.Size
	}
	bv.Size = drbd_size.LowerVolumeSize(size)

	// Compute LLV name.
	// For migration: if actual LLV exists on the same LVG/ThinPool, reuse its name
	// (old naming scheme). Otherwise, generate a new deterministic name.
	if actual != nil &&
		actual.LVMVolumeGroupName == bv.LVMVolumeGroupName &&
		actual.ThinPoolName == bv.ThinPoolName {
		bv.LLVName = actual.LLVName
	} else {
		bv.LLVName = rvrllvname.ComputeLLVName(rvr.Name, bv.LVMVolumeGroupName, bv.ThinPoolName)
	}

	return bv, "", ""
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
			if err := r.patchLLV(rf.Ctx(), llv, base); err != nil {
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
	changed := obju.AddFinalizer(llv, v1alpha1.RVRControllerFinalizer)

	// Ensure finalizer.

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

// applyBackingVolumeReadyCondFalse sets the BackingVolumeReady condition to False on RVR.
func applyBackingVolumeReadyCondFalse(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyType,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

// applyBackingVolumeReadyCondUnknown sets the BackingVolumeReady condition to Unknown on RVR.
func applyBackingVolumeReadyCondUnknown(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyType,
		Status:  metav1.ConditionUnknown,
		Reason:  reason,
		Message: message,
	})
}

// applyBackingVolumeReadyCondTrue sets the BackingVolumeReady condition to True on RVR.
func applyBackingVolumeReadyCondTrue(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyType,
		Status:  metav1.ConditionTrue,
		Reason:  reason,
		Message: message,
	})
}

// applyBackingVolumeReadyCondAbsent removes the BackingVolumeReady condition from RVR.
func applyBackingVolumeReadyCondAbsent(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
	return obju.RemoveStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyType)
}
