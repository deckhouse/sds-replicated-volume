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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	nodeutil "k8s.io/component-helpers/node/util"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
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
	cl                client.Client
	scheme            *runtime.Scheme
	log               logr.Logger
	agentPodNamespace string
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

func NewReconciler(cl client.Client, scheme *runtime.Scheme, log logr.Logger, agentPodNamespace string) *Reconciler {
	return &Reconciler{
		cl:                cl,
		scheme:            scheme,
		log:               log,
		agentPodNamespace: agentPodNamespace,
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
		// Reconcile the RVR metadata (finalizers and labels).
		outcome := r.reconcileMetadata(rf.Ctx(), rvr, rv, llvs, drbdr)
		if outcome.ShouldReturn() {
			return outcome.ToCtrl()
		}
	}

	var base *v1alpha1.ReplicatedVolumeReplica
	if rvr != nil {
		base = rvr.DeepCopy()
	}

	// Reconcile the backing volume (LVMLogicalVolume).
	targetLLVName, outcome := r.reconcileBackingVolume(rf.Ctx(), rvr, &llvs, rv, drbdr)
	if outcome.ShouldReturn() {
		return outcome.ToCtrl()
	}

	// Reconcile the DRBD resource.
	outcome = outcome.Merge(r.reconcileDRBDResource(rf.Ctx(), rvr, rv, drbdr, targetLLVName))
	if outcome.ShouldReturn() {
		return outcome.ToCtrl()
	}

	if rvr != nil {
		// compute agentReady and drbdrConfigurationPending
		configuredCond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondConfiguredType)
		var agentReady, drbdrConfigurationPending bool
		if configuredCond != nil {
			agentReady = configuredCond.Reason != v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonAgentNotReady
			drbdrConfigurationPending = configuredCond.Reason == v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonApplyingConfiguration
		}

		// Ensure RVR status fields reflect the current DRBDR state.
		eo := flow.MergeEnsures(
			ensureAttachmentStatus(rf.Ctx(), rvr, drbdr, agentReady, drbdrConfigurationPending),
			ensurePeersStatus(rf.Ctx(), rvr, drbdr, agentReady, drbdrConfigurationPending),
			ensureDiskStatus(rf.Ctx(), rvr, drbdr, agentReady, drbdrConfigurationPending),
			ensureQuorumStatus(rf.Ctx(), rvr, drbdr, agentReady, drbdrConfigurationPending),
		)
		if eo.Error() != nil {
			return rf.Failf(eo.Error(), "ensuring status").ToCtrl()
		}
		outcome = outcome.WithChangeFrom(eo)

		// Reconcile the SatisfyEligibleNodes condition.
		outcome = outcome.Merge(r.reconcileSatisfyEligibleNodesCondition(rf.Ctx(), rvr, drbdr))
		if outcome.ShouldReturn() {
			return outcome.ToCtrl()
		}
	}

	// Patch the RVR status if changed.
	if outcome.DidChange() && rvr != nil {
		if err := r.patchRVRStatus(rf.Ctx(), rvr, base, outcome.OptimisticLockRequired()); err != nil {
			return rf.Fail(err).ToCtrl()
		}
	}

	return rf.Done().ToCtrl()
}

// ensureAttachmentStatus ensures the RVR attachment-related status fields reflect the current DRBDR state.
func ensureAttachmentStatus(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	drbdr *v1alpha1.DRBDResource,
	agentReady bool,
	drbdrConfigurationPending bool,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "attachment-status")
	defer ef.OnEnd(&outcome)

	// TODO: implement

	// devicePathOnTheNode !!!

	// attached condition

	_ = rvr
	_ = drbdr
	_ = agentReady
	_ = drbdrConfigurationPending

	return ef.Ok()
}

// ensurePeersStatus ensures the RVR peers-related status fields reflect the current DRBDR state.
func ensurePeersStatus(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	drbdr *v1alpha1.DRBDResource,
	agentReady bool,
	drbdrConfigurationPending bool,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "peers-status")
	defer ef.OnEnd(&outcome)

	// TODO: implement

	// if drbdr == nil: FullyConnected = False, reason = NotApplicable, peers = nil

	// if agent not ready: FullyConnected = Unknown, reason = AgentNotReady, peers = nil

	// if drbdrConfigurationPending: FullyConnected = Unknown, reason = ApplyingConfiguration, peers = nil

	// otherwise ...

	// rvr.status.peers[]
	//   - name: ...
	//     type: Disful / TieBreaker / Access
	//     attached: true/false
	//     established: [systemNetworkName, ...]
	//     connectionState: ...
	//     diskState: ...

	// if len(rvr.status.peers) == 0: FullyConnected = False, reason = NoPeers

	// if all peers have all networks from rv.spec.systemNetworkNames in established: FullyConnected = True, reason = Ready

	// otherwise: FullyConnected = False, reason = NotConnected / PartialyConnected, message = "connected to X of Y fully, and to Z partially"

	_ = rvr
	_ = drbdr
	_ = agentReady
	_ = drbdrConfigurationPending

	return ef.Ok()
}

// ensureDiskStatus ensures the RVR disk-related status fields reflect the current DRBDR state.
func ensureDiskStatus(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	drbdr *v1alpha1.DRBDResource,
	agentReady bool,
	drbdrConfigurationPending bool,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "disk-status")
	defer ef.OnEnd(&outcome)

	// TODO: implement

	// if drbdr == nil: DiskInSync = False, reason = NotApplicable, diskState = nil

	// if agent not ready: DiskInSync = Unknown, reason = AgentNotReady, diskState = nil

	// if drbdrConfigurationPending: DiskInSync = Unknown, reason = ApplyingConfiguration, diskState = nil

	// diskState = drbdr.status.diskState

	// TODO: think through the logic with effectiveType and what should be written in condition reasons

	_ = rvr
	_ = drbdr
	_ = agentReady
	_ = drbdrConfigurationPending

	return ef.Ok()
}

// ensureQuorumStatus ensures the RVR quorum-related status fields reflect the current DRBDR state.
func ensureQuorumStatus(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	drbdr *v1alpha1.DRBDResource,
	agentReady bool,
	drbdrConfigurationPending bool,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "quorum-status")
	defer ef.OnEnd(&outcome)

	// TODO: implement

	// if drbdr == nil: Ready = False, reason = NotApplicable, quorum = nil, quorumSummary = nil

	// if agent not ready: Ready = Unknown, reason = AgentNotReady, quorum = nil, quorumSummary = nil

	// if drbdrConfigurationPending: Ready = Unknown, reason = ApplyingConfiguration, quorum = nil, quorumSummary = nil

	// if drbdr.status.quorum == false: Ready = False, reason = QuorumLost, quorum = false, quorumSummary = {fill}

	// otherwise: Ready = True, reason = Ready, quorum = true, quorumSummary = {fill}

	// message should show quorum: M/N, data quorum: J/K
	//   M — number of TB/DISKFUL nodes with established connection
	//   N — quorum from drbdr.spec.quorum
	//   J — number of TB/DISKFUL nodes with established connection and disk in sync
	//   K — quorumMinimumRedundancy from drbdr.spec.quorumMinimumRedundancy

	// rvr.status.quorumSummary
	//   connectedVotingPeers: M
	//   quorum: N
	//   connectedUpToDatePeers: J
	//   quorumMinimumRedundancy: K

	_ = rvr
	_ = drbdr
	_ = agentReady
	_ = drbdrConfigurationPending

	return ef.Ok()
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
) (targetLLVName string, outcome flow.ReconcileOutcome) {
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

		// Set BackingVolumeReady condition on RVR if rvr exists and clear size.
		changed := false
		if rvr != nil {
			changed = applyRVRBackingVolumeReadyCondFalse(rvr, reason, message)
			changed = applyRVRBackingVolumeSize(rvr, resource.Quantity{}) || changed
		}

		return "", rf.Continue().ReportChangedIf(changed)
	}

	// 2. Compute actual state.
	actual := computeActualBackingVolume(drbdr, *llvs)

	// 3. ReplicatedVolume not found — stop reconciliation and wait for it to appear.
	// Without RV we cannot determine datamesh state.
	if rv == nil {
		changed := applyRVRBackingVolumeReadyCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonWaitingForReplicatedVolume,
			"ReplicatedVolume not found")
		return actual.LLVNameOrEmpty(), rf.Continue().ReportChangedIf(changed)
	}

	// 4. Datamesh not initialized yet — wait for RV controller to set it up.
	// Normally datamesh is already initialized by the time RVR is created,
	// but we check for non-standard usage scenarios (e.g., RVR created before RV) and general correctness.
	if rv.Status.DatameshRevision == 0 {
		changed := applyRVRBackingVolumeReadyCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonWaitingForReplicatedVolume,
			"Datamesh is not initialized yet")
		return actual.LLVNameOrEmpty(), rf.Continue().ReportChangedIf(changed)
	}

	// 5. Compute intended state.
	intended, reason, message := computeIntendedBackingVolume(rvr, rv, actual)

	// 6. If intended == nil, delete LLVs, clear size and set appropriate condition.
	if intended == nil {
		if len(*llvs) > 0 {
			deletingNames, ro := r.reconcileLLVsDeletion(rf.Ctx(), llvs, nil)
			if ro.ShouldReturn() {
				return "", ro
			}
			message = fmt.Sprintf("%s; deleting backing volumes: %s", message, strings.Join(deletingNames, ", "))
		}

		changed := applyRVRBackingVolumeReadyCondFalse(rvr, reason, message)
		changed = applyRVRBackingVolumeSize(rvr, resource.Quantity{}) || changed
		return "", rf.Continue().ReportChangedIf(changed)
	}

	// 7. Find the intended LLV in the list.
	intendedLLV := findLLVByName(*llvs, intended.LLVName)

	// 8. Create if missing.
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
				return actual.LLVNameOrEmpty(), rf.RequeueAfter(5 * time.Minute).ReportChanged()
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
		return actual.LLVNameOrEmpty(), rf.Continue().ReportChangedIf(changed)
	}

	// 9. Ensure metadata (ownerRef, finalizer, label) on the intended LLV.
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

	// 10. Check if LLV is ready.
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
		return actual.LLVNameOrEmpty(), rf.Continue().ReportChangedIf(changed)
	}

	// 11. Resize if needed: LLV is ready, but size may need to grow.
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

		return actual.LLVNameOrEmpty(), rf.Continue().ReportChangedIf(changed)
	}

	// 12. Fully ready: delete obsolete LLVs and set condition to Ready.
	// Keep actual LLV if it's different from intended (migration in progress).
	// But at this point intended is ready, so we can delete actual too.
	message = fmt.Sprintf("Backing volume %s is ready", intended.LLVName)
	if len(*llvs) > 0 {
		var ro flow.ReconcileOutcome
		deletingNames, ro := r.reconcileLLVsDeletion(rf.Ctx(), llvs, []string{intended.LLVName})
		if ro.ShouldReturn() {
			return "", ro
		}
		message = fmt.Sprintf("%s; deleting obsolete: %s", message, strings.Join(deletingNames, ", "))
	}
	changed := applyRVRBackingVolumeReadyCondTrue(rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonReady, message)
	changed = applyRVRBackingVolumeSize(rvr, intendedLLV.Status.ActualSize) || changed

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

// LLVNameOrEmpty returns the LLV name or empty string if bv is nil.
func (bv *backingVolume) LLVNameOrEmpty() string {
	if bv == nil {
		return ""
	}
	return bv.LLVName
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
func computeIntendedBackingVolume(rvr *v1alpha1.ReplicatedVolumeReplica, rv *v1alpha1.ReplicatedVolume, actual *backingVolume) (intended *backingVolume, reason, message string) {
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
		// Therefore member in datamesh:
		// - Needs backing volume if:
		//   - Type is Diskful, OR
		//   - TypeTransition is ToDiskful (preparing disk before becoming Diskful)
		// - Does NOT need backing volume if:
		//   - TypeTransition is ToDiskless (removing disk)
		//   - Type is diskless (Access/TieBreaker) and no transition to Diskful

		// Check if backing volume is needed.
		isDiskful := member.Type == v1alpha1.ReplicaTypeDiskful
		transitioningToDiskful := member.TypeTransition == v1alpha1.ReplicatedVolumeDatameshMemberTypeTransitionToDiskful
		transitioningToDiskless := member.TypeTransition == v1alpha1.ReplicatedVolumeDatameshMemberTypeTransitionToDiskless
		if !isDiskful && !transitioningToDiskful {
			return nil, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonNotApplicable,
				"Backing volume is not applicable for diskless replica type"
		}
		if transitioningToDiskless {
			return nil, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonNotApplicable,
				"Backing volume is not applicable (transition to diskless)"
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
		bv.LLVName = computeLLVName(rvr.Name, bv.LVMVolumeGroupName, bv.ThinPoolName)
	}

	return bv, "", ""
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
func (r *Reconciler) reconcileDRBDResource(ctx context.Context, rvr *v1alpha1.ReplicatedVolumeReplica, rv *v1alpha1.ReplicatedVolume, drbdr *v1alpha1.DRBDResource, targetLLVName string) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "drbd-resource")
	defer rf.OnEnd(&outcome)

	// 1. Deletion branch: if RVR should not exist, remove finalizer from DRBDR and delete it.
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

	// 2. Node not assigned yet — cannot configure DRBD without a node.
	// Note: Per API validation, NodeName is immutable once set. We rely on this invariant below.
	if rvr.Spec.NodeName == "" {
		changed := applyRVRConfiguredCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonPendingScheduling,
			"Waiting for node assignment")
		return rf.Continue().ReportChangedIf(changed)
	}

	// 3. ReplicatedVolume not found — stop reconciliation and wait for it to appear.
	// Without RV we cannot determine datamesh state, system networks, peers, etc.
	if rv == nil {
		changed := applyRVRConfiguredCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonWaitingForReplicatedVolume,
			"ReplicatedVolume not found")
		return rf.Continue().ReportChangedIf(changed)
	}

	// 4. Datamesh not initialized yet — wait for RV controller to set it up.
	// Normally datamesh is already initialized by the time RVR is created,
	// but we check for non-standard usage scenarios (e.g., RVR created before RV) and general correctness.
	if rv.Status.DatameshRevision == 0 {
		changed := applyRVRConfiguredCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonWaitingForReplicatedVolume,
			"Datamesh is not initialized yet")
		return rf.Continue().ReportChangedIf(changed)
	}

	datamesh := &rv.Status.Datamesh
	datameshRevision := rv.Status.DatameshRevision

	// 5. No system networks in datamesh — DRBD needs networks to communicate.
	// If datamesh is initialized, system networks should already be set,
	// but we check for non-standard scenarios and general correctness.
	if len(datamesh.SystemNetworkNames) == 0 {
		changed := applyRVRConfiguredCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonWaitingForReplicatedVolume,
			"No system networks in datamesh")
		return rf.Continue().ReportChangedIf(changed)
	}

	member := datamesh.FindMemberByName(rvr.Name)
	intendedEffectiveType := computeIntendedEffectiveType(rvr, member)
	targetEffectiveType := computeTargetEffectiveType(intendedEffectiveType, targetLLVName)

	// 6. Create or update DRBDR.
	if drbdr == nil {
		// Compute target DRBDR spec.
		targetSpec := computeTargetDRBDRSpec(rvr, drbdr, datamesh, member, targetLLVName, targetEffectiveType)

		// Create new DRBDResource.
		newObj, err := newDRBDR(r.scheme, rvr, targetSpec)
		if err != nil {
			return rf.Failf(err, "constructing DRBDResource")
		}
		if err := r.createDRBDR(rf.Ctx(), newObj); err != nil {
			return rf.Failf(err, "creating DRBDResource")
		}
		drbdr = newObj
	} else {
		// We need to check DRBDR spec if any of the tracked values changed since last reconciliation:
		// - DRBDResource.Generation (DRBDR was modified externally)
		// - DatameshRevision (datamesh configuration changed)
		// - EffectiveType (replica type changed, e.g. diskful -> tiebreaker due to missing disk)
		specMayNeedUpdate := drbdr.Generation != rvr.Status.DRBDResourceGeneration ||
			rvr.Status.DatameshRevision != datameshRevision ||
			rvr.Status.EffectiveType != targetEffectiveType

		if specMayNeedUpdate {
			// Compute target DRBDR spec.
			targetSpec := computeTargetDRBDRSpec(rvr, drbdr, datamesh, member, targetLLVName, targetEffectiveType)

			// Compare and potentially apply changes.
			// DeepEqual handles resource.Quantity and nested structs correctly.
			// Performance: not a hot path, reflection overhead is negligible here.
			if !equality.Semantic.DeepEqual(drbdr.Spec, targetSpec) {
				base := drbdr.DeepCopy()
				drbdr.Spec = targetSpec
				if err := r.patchDRBDR(rf.Ctx(), drbdr, base, true); err != nil {
					return rf.Failf(err, "patching DRBDResource")
				}
			}
		}
	}

	// 7. Update rvr.status fields. Further we require optimistic lock for this step.
	changed := applyRVRDatameshRevision(rvr, rv.Status.DatameshRevision)
	changed = applyRVRDRBDResourceGeneration(rvr, drbdr.Generation) || changed
	changed = applyRVREffectiveType(rvr, targetEffectiveType) || changed

	// =====================================================

	// 8. Check if the agent is ready on the node.
	nodeName := rvr.Spec.NodeName
	agentReady, err := r.getAgentReady(rf.Ctx(), nodeName)
	if err != nil {
		return rf.Failf(err, "getting agent readiness for %s", nodeName)
	}
	if !agentReady {
		// Check node readiness only when agent is not ready — to provide diagnostic info.
		nodeReady, err := r.getNodeReady(rf.Ctx(), nodeName)
		if err != nil {
			return rf.Failf(err, "getting node readiness for %s", nodeName)
		}
		var nodeState string
		if nodeReady {
			nodeState = "Ready"
		} else {
			nodeState = "NotReady"
		}
		msg := fmt.Sprintf("Agent is not ready on node %s (node status: %s)", nodeName, nodeState)
		changed = applyRVRConfiguredCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonAgentNotReady, msg) || changed
		return rf.Continue().ReportChangedIf(changed).RequireOptimisticLock()
	}

	// 9. Check DRBDR configuration status.
	state, msg := computeActualDRBDRConfigured(drbdr)
	if state == DRBDRConfiguredStatePending {
		changed = applyRVRConfiguredCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonApplyingConfiguration, msg) || changed
		return rf.Continue().ReportChangedIf(changed).RequireOptimisticLock()
	}
	if state == DRBDRConfiguredStateFalse {
		changed = applyRVRConfiguredCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonConfigurationFailed, msg) || changed
		return rf.Continue().ReportChangedIf(changed).RequireOptimisticLock()
	}

	// At this point targetEffectiveType must equal intendedEffectiveType.
	// If not, our reconciliation logic has a bug.
	if targetEffectiveType != intendedEffectiveType {
		panic(fmt.Sprintf("targetEffectiveType (%s) != intendedEffectiveType (%s)", targetEffectiveType, intendedEffectiveType))
	}

	// 10. DRBDR is configured — copy addresses to RVR status.
	if len(rvr.Status.Addresses) == 0 {
		changed = applyRVRConfiguredCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonApplyingConfiguration,
			"Waiting for DRBD addresses") || changed
		return rf.Continue().ReportChangedIf(changed).RequireOptimisticLock()
	}
	changed = applyRVRAddresses(rvr, drbdr.Status.Addresses) || changed

	// 11. If not a datamesh member — DRBD is preconfigured, waiting for membership.
	if member == nil {
		changed = applyRVRConfiguredCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonPendingDatameshJoin,
			"DRBD preconfigured, waiting for datamesh membership") || changed
		return rf.Continue().ReportChangedIf(changed).RequireOptimisticLock()
	}

	// 12. We are a datamesh member — fully configured.
	changed = applyRVRConfiguredCondTrue(rvr,
		v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonConfigured, "Configured") || changed
	return rf.Continue().ReportChangedIf(changed).RequireOptimisticLock()
}

// DRBDRConfiguredState represents the configuration state of a DRBDResource.
type DRBDRConfiguredState int

const (
	// DRBDRConfiguredStateTrue means the DRBDResource is fully configured
	// (Configured condition is True and ObservedGeneration matches Generation).
	DRBDRConfiguredStateTrue DRBDRConfiguredState = iota
	// DRBDRConfiguredStateFalse means the DRBDResource configuration failed
	// (Configured condition is False and ObservedGeneration matches Generation).
	DRBDRConfiguredStateFalse
	// DRBDRConfiguredStatePending means the agent hasn't processed the current generation yet
	// (no Configured condition or ObservedGeneration doesn't match Generation).
	DRBDRConfiguredStatePending
)

// computeActualDRBDRConfigured returns the configuration state of a DRBDResource and a message:
// - DRBDRConfiguredStateTrue: configured successfully
// - DRBDRConfiguredStateFalse: configuration failed (message contains error from condition)
// - DRBDRConfiguredStatePending: waiting for agent to process or resource is in maintenance mode
func computeActualDRBDRConfigured(drbdr *v1alpha1.DRBDResource) (DRBDRConfiguredState, string) {
	if drbdr == nil {
		panic("computeActualDRBDRConfigured: drbdr is nil")
	}
	cond := obju.GetStatusCondition(drbdr, v1alpha1.DRBDResourceCondConfiguredType)

	// DRBDResource was just created and hasn't been processed by the agent yet.
	if cond == nil {
		return DRBDRConfiguredStatePending, "Waiting for agent to respond (Configured condition is not set yet)"
	}

	// We just made changes to the DRBDResourceand the agent hasn't processed them yet.
	if cond.ObservedGeneration != drbdr.Generation {
		return DRBDRConfiguredStatePending, fmt.Sprintf(
			"Waiting for agent to respond (generation: %d, observedGeneration: %d)",
			drbdr.Generation, cond.ObservedGeneration)
	}

	// DRBDResource is in maintenance mode.
	if cond.Reason == v1alpha1.DRBDResourceCondConfiguredReasonInMaintenance {
		return DRBDRConfiguredStatePending, "DRBD is in maintenance mode"
	}

	// DRBDResource is successfully configured.
	if cond.Status == metav1.ConditionTrue {
		return DRBDRConfiguredStateTrue, ""
	}

	// DRBDResource configuration failed.
	return DRBDRConfiguredStateFalse, fmt.Sprintf("DRBD configuration failed (reason: %s)", cond.Reason)
}

// computeIntendedEffectiveType returns the intended effective replica type.
// If member is nil, returns rvr.Spec.Type.
// During transitions, uses TieBreaker as intermediate type:
// - ToDiskful but not yet Diskful → TieBreaker (disk is being prepared)
// - Diskful with ToDiskless → TieBreaker (data is being moved away)
func computeIntendedEffectiveType(rvr *v1alpha1.ReplicatedVolumeReplica, member *v1alpha1.ReplicatedVolumeDatameshMember) v1alpha1.ReplicaType {
	if member == nil {
		return rvr.Spec.Type
	}

	// During transitions, use TieBreaker as intermediate type.
	if member.Type == v1alpha1.ReplicaTypeDiskful &&
		member.TypeTransition == v1alpha1.ReplicatedVolumeDatameshMemberTypeTransitionToDiskless {
		return v1alpha1.ReplicaTypeTieBreaker
	}

	return member.Type
}

// computeTargetEffectiveType returns the target effective replica type.
// If intended is Diskful but there's no backing volume, returns TieBreaker.
// Otherwise returns the intended type.
func computeTargetEffectiveType(intendedEffectiveType v1alpha1.ReplicaType, targetLLVName string) v1alpha1.ReplicaType {
	if intendedEffectiveType == v1alpha1.ReplicaTypeDiskful && targetLLVName == "" {
		return v1alpha1.ReplicaTypeTieBreaker
	}
	return intendedEffectiveType
}

// computeTargetDRBDRType converts ReplicaType to DRBDResourceType.
// Diskful → Diskful, Access/TieBreaker → Diskless.
func computeDRBDRType(replicaType v1alpha1.ReplicaType) v1alpha1.DRBDResourceType {
	switch replicaType {
	case v1alpha1.ReplicaTypeDiskful:
		return v1alpha1.DRBDResourceTypeDiskful
	case v1alpha1.ReplicaTypeAccess, v1alpha1.ReplicaTypeTieBreaker:
		return v1alpha1.DRBDResourceTypeDiskless
	default:
		return v1alpha1.DRBDResourceTypeDiskless
	}
}

// newDRBDR constructs a new DRBDResource with ownerRef and finalizer.
// Spec must already have all fields set (including NodeName and NodeID).
func newDRBDR(
	scheme *runtime.Scheme,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	spec v1alpha1.DRBDResourceSpec,
) (*v1alpha1.DRBDResource, error) {
	drbdr := &v1alpha1.DRBDResource{
		ObjectMeta: metav1.ObjectMeta{
			Name:       rvr.Name,
			Finalizers: []string{v1alpha1.RVRControllerFinalizer},
		},
		Spec: spec,
	}
	if err := controllerutil.SetControllerReference(rvr, drbdr, scheme); err != nil {
		return nil, err
	}
	return drbdr, nil
}

// computeTargetDRBDRSpec computes the target DRBDR spec based on rvr, existing drbdr,
// datamesh and member configuration.
// If drbdr exists, starts from a copy of its spec (preserving immutable and user-controlled fields).
// Otherwise creates a new spec with NodeName/NodeID from rvr.
//
// Exception: This helper uses DeepCopy which normally violates ComputeReconcileHelper rules.
// This is intentional: we copy the existing spec to preserve immutable and user-controlled fields
// (NodeName, NodeID, Maintenance) that we don't want to manually track. The DeepCopy overhead
// is acceptable since this is not a hot path.
func computeTargetDRBDRSpec(
	rvr *v1alpha1.ReplicatedVolumeReplica,
	drbdr *v1alpha1.DRBDResource,
	datamesh *v1alpha1.ReplicatedVolumeDatamesh,
	member *v1alpha1.ReplicatedVolumeDatameshMember,
	targetLLVName string,
	targetEffectiveType v1alpha1.ReplicaType,
) v1alpha1.DRBDResourceSpec {
	// Start from existing spec (preserves immutable and user-controlled fields) or create new.
	var spec v1alpha1.DRBDResourceSpec
	if drbdr != nil {
		spec = *drbdr.Spec.DeepCopy()
	} else {
		nodeID, ok := rvr.NodeID()
		if !ok {
			panic("invalid RVR name: cannot extract NodeID from " + rvr.Name)
		}
		spec.NodeName = rvr.Spec.NodeName
		spec.NodeID = nodeID
	}

	// Fill mutable fields.
	spec.Type = computeDRBDRType(targetEffectiveType)
	spec.SystemNetworks = slices.Clone(datamesh.SystemNetworkNames)
	spec.State = v1alpha1.DRBDResourceStateUp

	// LLV name and Size: non-empty only for Diskful.
	if spec.Type == v1alpha1.DRBDResourceTypeDiskful {
		spec.LVMLogicalVolumeName = targetLLVName
		spec.Size = &datamesh.Size
	} else {
		spec.LVMLogicalVolumeName = ""
		spec.Size = nil
	}

	// Membership-dependent configuration.
	if member == nil {
		// Cannot become Primary while not in datamesh.
		spec.Role = v1alpha1.DRBDRoleSecondary
		spec.AllowTwoPrimaries = false

		// Cannot participate in quorum while not in datamesh.
		spec.Quorum = 32
		spec.QuorumMinimumRedundancy = 32

		// Does not connect to any peers while not in datamesh.
		spec.Peers = nil
	} else {
		// Datamesh determines the role for each member and whether multiple primaries are allowed.
		spec.Role = member.Role
		spec.AllowTwoPrimaries = datamesh.AllowTwoPrimaries

		// Quorum: diskless node quorum depends on connection to enough UpToDate diskful nodes that have quorum.
		if spec.Type == v1alpha1.DRBDResourceTypeDiskless {
			spec.Quorum = 32
			spec.QuorumMinimumRedundancy = datamesh.QuorumMinimumRedundancy
		} else {
			spec.Quorum = datamesh.Quorum
			spec.QuorumMinimumRedundancy = datamesh.QuorumMinimumRedundancy
		}

		// Compute peers based on self type.
		spec.Peers = computeTargetDRBDRPeers(datamesh, member)
	}

	return spec
}

// computeTargetDRBDRPeers computes the target peers from datamesh members (excluding self).
//
// Peer connectivity rules:
//   - Diskful replica (or TieBreaker transitioning ToDiskful) connects to ALL peers,
//     but sets AllowRemoteRead=false for Access peers (so they are not considered in the tie-breaker mechanism).
//   - Access/TieBreaker replica connects only to Diskful peers and TieBreaker peers transitioning ToDiskful.
func computeTargetDRBDRPeers(datamesh *v1alpha1.ReplicatedVolumeDatamesh, self *v1alpha1.ReplicatedVolumeDatameshMember) []v1alpha1.DRBDResourcePeer {
	if len(datamesh.Members) <= 1 {
		return nil
	}

	// Diskful or TieBreaker transitioning ToDiskful is treated as Diskful for peer connectivity.
	selfTreatedAsDiskful := self.Type == v1alpha1.ReplicaTypeDiskful ||
		(self.Type == v1alpha1.ReplicaTypeTieBreaker && self.TypeTransition == v1alpha1.ReplicatedVolumeDatameshMemberTypeTransitionToDiskful)

	peers := make([]v1alpha1.DRBDResourcePeer, 0, len(datamesh.Members)-1)
	for i := range datamesh.Members {
		m := &datamesh.Members[i]
		if m.Name == self.Name {
			continue
		}

		// Diskful or TieBreaker transitioning ToDiskful is treated as Diskful for peer connectivity.
		peerTreatedAsDiskful := m.Type == v1alpha1.ReplicaTypeDiskful ||
			(m.Type == v1alpha1.ReplicaTypeTieBreaker && m.TypeTransition == v1alpha1.ReplicatedVolumeDatameshMemberTypeTransitionToDiskful)

		if !selfTreatedAsDiskful && !peerTreatedAsDiskful {
			// Access/TieBreaker: only connect to peers treated as Diskful.
			continue
		}

		// AllowRemoteRead=false for Access peers (so they are not considered in the tie-breaker mechanism).
		peerIsAccess := m.Type == v1alpha1.ReplicaTypeAccess
		allowRemoteRead := !peerIsAccess

		// Extract NodeID from the member name. This should never panic because
		// member name format is validated by API.
		nodeID, ok := m.NodeID()
		if !ok {
			panic("m.NodeID() failed for member " + m.Name)
		}

		// Peer type: if treated as Diskful, use Diskful; otherwise Diskless.
		peerType := v1alpha1.DRBDResourceTypeDiskless
		if peerTreatedAsDiskful {
			peerType = v1alpha1.DRBDResourceTypeDiskful
		}

		peers = append(peers, v1alpha1.DRBDResourcePeer{
			Name:            m.Name,
			Type:            peerType,
			AllowRemoteRead: allowRemoteRead,
			NodeID:          nodeID,
			Protocol:        v1alpha1.DRBDProtocolC,
			SharedSecret:    datamesh.SharedSecret,
			SharedSecretAlg: datamesh.SharedSecretAlg,
			Paths:           buildDRBDRPeerPaths(m.Addresses),
		})
	}
	return peers
}

// buildDRBDRPeerPaths builds paths from member addresses.
func buildDRBDRPeerPaths(addresses []v1alpha1.DRBDResourceAddressStatus) []v1alpha1.DRBDResourcePath {
	paths := make([]v1alpha1.DRBDResourcePath, len(addresses))
	for i := range addresses {
		paths[i] = v1alpha1.DRBDResourcePath{
			SystemNetworkName: addresses[i].SystemNetworkName,
			Address:           addresses[i].Address,
		}
	}
	return paths
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

// applyRVRConfiguredCondTrue sets the Configured condition to True on RVR.
func applyRVRConfiguredCondTrue(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondConfiguredType,
		Status:  metav1.ConditionTrue,
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

// applyRVRBackingVolumeSize sets BackingVolumeSize on RVR status.
// Pass zero quantity to clear the size.
func applyRVRBackingVolumeSize(rvr *v1alpha1.ReplicatedVolumeReplica, size resource.Quantity) bool {
	if rvr.Status.BackingVolumeSize.Cmp(size) == 0 {
		return false
	}
	rvr.Status.BackingVolumeSize = size
	return true
}

// applyRVREffectiveType sets EffectiveType on RVR status.
func applyRVREffectiveType(rvr *v1alpha1.ReplicatedVolumeReplica, effectiveType v1alpha1.ReplicaType) bool {
	if rvr.Status.EffectiveType == effectiveType {
		return false
	}
	rvr.Status.EffectiveType = effectiveType
	return true
}

// applyRVRDatameshRevision sets DatameshRevision on RVR status.
func applyRVRDatameshRevision(rvr *v1alpha1.ReplicatedVolumeReplica, revision int64) bool {
	if rvr.Status.DatameshRevision == revision {
		return false
	}
	rvr.Status.DatameshRevision = revision
	return true
}

// applyRVRAddresses sets Addresses on RVR status.
func applyRVRAddresses(rvr *v1alpha1.ReplicatedVolumeReplica, addresses []v1alpha1.DRBDResourceAddressStatus) bool {
	if slices.Equal(rvr.Status.Addresses, addresses) {
		return false
	}
	rvr.Status.Addresses = slices.Clone(addresses)
	return true
}

// applyRVRDRBDResourceGeneration sets DRBDResourceGeneration on RVR status.
func applyRVRDRBDResourceGeneration(rvr *v1alpha1.ReplicatedVolumeReplica, gen int64) bool {
	if rvr.Status.DRBDResourceGeneration == gen {
		return false
	}
	rvr.Status.DRBDResourceGeneration = gen
	return true
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: satisfy-eligible-nodes
//

// reconcileSatisfyEligibleNodesCondition reconciles the RVR SatisfyEligibleNodes condition.
//
// Reconcile pattern: TODO
func (r *Reconciler) reconcileSatisfyEligibleNodesCondition(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	drbdr *v1alpha1.DRBDResource,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "satisfy-eligible-nodes")
	defer rf.OnEnd(&outcome)

	// TODO: implement

	_ = rvr
	_ = drbdr

	return rf.Continue()
}

// ──────────────────────────────────────────────────────────────────────────────
// Single-call I/O helper categories: GetReconcileHelper
//

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
//
// Exception: This helper performs two reads (List + conditional Get) which violates
// the single-read GetReconcileHelper contract. This is intentional: we need to combine
// owner-based lookup with fallback for legacy/migration LLV names in a single atomic operation.
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

func (r *Reconciler) createDRBDR(ctx context.Context, drbdr *v1alpha1.DRBDResource) error {
	return r.cl.Create(ctx, drbdr)
}

// --- Node ---

// getNodeReady checks if a node is ready.
func (r *Reconciler) getNodeReady(ctx context.Context, nodeName string) (bool, error) {
	var unsafeNode corev1.Node
	if err := r.cl.Get(ctx, client.ObjectKey{Name: nodeName}, &unsafeNode, client.UnsafeDisableDeepCopy); err != nil {
		return false, err
	}

	_, readyCond := nodeutil.GetNodeCondition(&unsafeNode.Status, corev1.NodeReady)
	return readyCond != nil && readyCond.Status == corev1.ConditionTrue, nil
}

// --- Pod ---

// getAgentReady checks if the agent pod is ready on a given node.
func (r *Reconciler) getAgentReady(ctx context.Context, nodeName string) (bool, error) {
	var unsafeList corev1.PodList
	if err := r.cl.List(ctx, &unsafeList,
		client.InNamespace(r.agentPodNamespace),
		client.MatchingLabels{"app": "agent"},
		client.MatchingFields{indexes.IndexFieldPodByNodeName: nodeName},
		client.UnsafeDisableDeepCopy,
	); err != nil {
		return false, err
	}

	// There may be completed/terminated pods that haven't been cleaned up yet,
	// so we iterate to find a ready one.
	for i := range unsafeList.Items {
		unsafePod := &unsafeList.Items[i]
		for _, cond := range unsafePod.Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				return true, nil
			}
		}
	}

	return false, nil
}
