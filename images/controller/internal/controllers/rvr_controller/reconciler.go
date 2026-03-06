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

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	nodeutil "k8s.io/component-helpers/node/util"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
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

	// Get RSP eligibility view for condition checks.
	// Skip if node not assigned or RVR is being deleted.
	var rspView *rspEligibilityView
	if rvr != nil &&
		rvr.Spec.NodeName != "" &&
		!rvrShouldNotExist(rvr) &&
		rv != nil &&
		rv.Status.Configuration != nil {
		rspView, err = r.getRSPEligibilityView(rf.Ctx(), rv.Status.Configuration.ReplicatedStoragePoolName, rvr.Spec.NodeName)
		if err != nil {
			return rf.Failf(err, "getting RSP eligibility for %s", rv.Status.Configuration.ReplicatedStoragePoolName).ToCtrl()
		}
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
	targetBV, intendedBV, outcome := r.reconcileBackingVolume(rf.Ctx(), rvr, &llvs, rv, drbdr, rspView)
	if outcome.ShouldReturn() {
		return outcome.ToCtrl()
	}

	// Reconcile the DRBD resource.
	drbdr, ro := r.reconcileDRBDResource(rf.Ctx(), rvr, rv, drbdr, targetBV, intendedBV)
	outcome = outcome.Merge(ro)
	if outcome.ShouldReturn() {
		return outcome.ToCtrl()
	}

	if rvr != nil {
		// compute agentReady and drbdrConfigurationPending
		drbdConfiguredCond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType)
		var agentReady, drbdrConfigurationPending bool
		if drbdConfiguredCond != nil {
			agentReady = drbdConfiguredCond.Reason != v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonAgentNotReady
			drbdrConfigurationPending = drbdConfiguredCond.Reason == v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonApplyingConfiguration
		}

		// Find datamesh and datamesh member for this RVR.
		var datamesh *v1alpha1.ReplicatedVolumeDatamesh
		var datameshMember *v1alpha1.DatameshMember
		if rv != nil {
			datamesh = &rv.Status.Datamesh
			datameshMember = datamesh.FindMemberByName(rvr.Name)
		}

		// Ensure RVR status fields reflect the current DRBDR state.
		eo := flow.MergeEnsures(
			// Ensure status fields.
			ensureStatusAddressesAndType(rf.Ctx(), rvr, drbdr),
			ensureStatusAttachment(rf.Ctx(), rvr, drbdr, agentReady, drbdrConfigurationPending),
			ensureStatusPeers(rf.Ctx(), rvr, drbdr),
			ensureStatusBackingVolume(rf.Ctx(), rvr, drbdr, llvs),
			ensureStatusQuorum(rf.Ctx(), rvr, drbdr),

			// Ensure conditions.
			ensureConditionAttached(rf.Ctx(), rvr, drbdr, datameshMember, agentReady, drbdrConfigurationPending),
			ensureConditionFullyConnected(rf.Ctx(), rvr, drbdr, datamesh, agentReady),
			ensureConditionBackingVolumeUpToDate(rf.Ctx(), rvr, drbdr, datameshMember, agentReady, drbdrConfigurationPending),
			ensureConditionReady(rf.Ctx(), rvr, rv, drbdr, datameshMember, agentReady, drbdrConfigurationPending),
			ensureConditionSatisfyEligibleNodes(rf.Ctx(), rvr, rv, rspView),

			// Ensure datamesh pending transition and configured condition.
			ensureStatusDatameshRequestAndConfiguredCond(rf.Ctx(), rvr, rv, rspView),
		)
		if eo.Error() != nil {
			return rf.Failf(eo.Error(), "ensuring status").ToCtrl()
		}
		outcome = outcome.WithChangeFrom(eo)
	}

	// Patch the RVR status if changed.
	if outcome.DidChange() && rvr != nil {
		if err := r.patchRVRStatus(rf.Ctx(), rvr, base); err != nil {
			return rf.Fail(err).ToCtrl()
		}
	}

	return outcome.ToCtrl()
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

	// Kubernetes rejects adding new finalizers to a deleting object.
	// If our finalizer is not already present and the object is deleting,
	// we must not attempt to add it — force target to false.
	if targetFinalizerPresent && rvr.DeletionTimestamp != nil &&
		!obju.HasFinalizer(rvr, v1alpha1.RVRControllerFinalizer) {
		targetFinalizerPresent = false
	}

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

	if err := r.patchRVR(rf.Ctx(), rvr, base); err != nil {
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

// ──────────────────────────────────────────────────────────────────────────────
// View types
//

// rspEligibilityView contains pre-fetched RSP data for eligibility checks.
// Used to avoid I/O in ensure helpers.
type rspEligibilityView struct {
	// EligibleNode is a copy of the eligible node entry for the RVR's node.
	// nil if the node is not in the eligible nodes list or RSP was not found.
	EligibleNode *v1alpha1.ReplicatedStoragePoolEligibleNode
	// Type is the RSP type (LVM or LVMThin).
	// Empty if RSP was not found.
	Type v1alpha1.ReplicatedStoragePoolType
}

// storageEligibilityCode represents the result of storage eligibility check.
type storageEligibilityCode int

const (
	storageEligibilityOK                  storageEligibilityCode = iota
	storageEligibilityRSPNotAvailable                            // rspView == nil
	storageEligibilityNodeNotEligible                            // rspView.EligibleNode == nil
	storageEligibilityTypeMismatch                               // ThinPool specified but RSP is LVM (or vice versa)
	storageEligibilityLVGNotEligible                             // LVG not in eligible node's list
	storageEligibilityThinPoolNotEligible                        // LVG found but ThinPool not in list
)

// isStorageEligible checks if the given LVG+ThinPool combination is eligible.
// Returns (code, message) where code != storageEligibilityOK means not eligible.
// Safe to call on nil receiver.
func (v *rspEligibilityView) isStorageEligible(lvgName, thinPool string) (storageEligibilityCode, string) {
	// Check RSP eligibility view availability.
	if v == nil {
		return storageEligibilityRSPNotAvailable, "ReplicatedStoragePool not found; waiting for it to be present"
	}

	// Check if node is eligible in RSP.
	if v.EligibleNode == nil {
		return storageEligibilityNodeNotEligible, "Node is not eligible in RSP"
	}

	// Validate ThinPool presence/absence matches RSP type.
	switch v.Type {
	case v1alpha1.ReplicatedStoragePoolTypeLVM:
		if thinPool != "" {
			return storageEligibilityTypeMismatch,
				fmt.Sprintf("ThinPool %q specified but RSP type is LVM (thick); waiting for correct storage assignment", thinPool)
		}
	case v1alpha1.ReplicatedStoragePoolTypeLVMThin:
		if thinPool == "" {
			return storageEligibilityTypeMismatch,
				"ThinPool not specified but RSP type is LVMThin; waiting for correct storage assignment"
		}
	}

	// Check if storage (LVG, or LVG+ThinPool for LVMThin) is in eligible node's list.
	for _, lvg := range v.EligibleNode.LVMVolumeGroups {
		if lvg.Name == lvgName {
			// For LVM (thick): just check LVG name match.
			// For LVMThin: also check ThinPool name match.
			if v.Type == v1alpha1.ReplicatedStoragePoolTypeLVM {
				return storageEligibilityOK, ""
			}
			if lvg.ThinPoolName == thinPool {
				return storageEligibilityOK, ""
			}
			// LVG found but ThinPool doesn't match.
			return storageEligibilityThinPoolNotEligible,
				fmt.Sprintf("LVG %q found but ThinPool %q is not eligible on node", lvgName, thinPool)
		}
	}

	// LVG not found.
	if v.Type == v1alpha1.ReplicatedStoragePoolTypeLVMThin {
		return storageEligibilityLVGNotEligible,
			fmt.Sprintf("LVG %q with ThinPool %q is not eligible on node", lvgName, thinPool)
	}
	return storageEligibilityLVGNotEligible,
		fmt.Sprintf("LVG %q is not eligible on node", lvgName)
}

// ──────────────────────────────────────────────────────────────────────────────
// Single-call I/O helper categories
//

// --- ReplicatedVolumeReplica (RVR) ---

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

func (r *Reconciler) patchRVR(ctx context.Context, obj, base *v1alpha1.ReplicatedVolumeReplica) error {
	return r.cl.Patch(ctx, obj, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
}

func (r *Reconciler) patchRVRStatus(ctx context.Context, obj, base *v1alpha1.ReplicatedVolumeReplica) error {
	return r.cl.Status().Patch(ctx, obj, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
}

// --- DRBDResource (DRBDR) ---

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

func (r *Reconciler) createDRBDR(ctx context.Context, drbdr *v1alpha1.DRBDResource) error {
	return r.cl.Create(ctx, drbdr)
}

func (r *Reconciler) patchDRBDR(ctx context.Context, obj, base *v1alpha1.DRBDResource) error {
	return r.cl.Patch(ctx, obj, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
}

func (r *Reconciler) deleteDRBDR(ctx context.Context, drbdr *v1alpha1.DRBDResource) error {
	if drbdr.DeletionTimestamp != nil {
		return nil
	}
	if err := client.IgnoreNotFound(r.cl.Delete(ctx, drbdr)); err != nil {
		return err
	}
	drbdr.DeletionTimestamp = ptr.To(metav1.Now())
	return nil
}

// --- ReplicatedStoragePool (RSP) ---

// getRSPEligibilityView fetches RSP and extracts eligibility data for the given node.
// Returns nil view (not error) if RSP is not found.
// Returns view with nil EligibleNode if node is not in eligible nodes list.
func (r *Reconciler) getRSPEligibilityView(
	ctx context.Context,
	rspName string,
	nodeName string,
) (*rspEligibilityView, error) {
	var unsafeRSP v1alpha1.ReplicatedStoragePool
	if err := r.cl.Get(ctx, client.ObjectKey{Name: rspName}, &unsafeRSP, client.UnsafeDisableDeepCopy); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}

	view := &rspEligibilityView{
		Type: unsafeRSP.Spec.Type,
	}

	// Find and copy the eligible node entry.
	for i := range unsafeRSP.Status.EligibleNodes {
		if unsafeRSP.Status.EligibleNodes[i].NodeName == nodeName {
			// DeepCopy to avoid aliasing with cache (LVMVolumeGroups is a slice).
			view.EligibleNode = unsafeRSP.Status.EligibleNodes[i].DeepCopy()
			break
		}
	}

	return view, nil
}

// --- ReplicatedVolume (RV) ---

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

// --- LVMLogicalVolume (LLV) ---

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

func (r *Reconciler) createLLV(ctx context.Context, llv *snc.LVMLogicalVolume) error {
	return r.cl.Create(ctx, llv)
}

func (r *Reconciler) patchLLV(ctx context.Context, obj, base *snc.LVMLogicalVolume) error {
	return r.cl.Patch(ctx, obj, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
}

func (r *Reconciler) deleteLLV(ctx context.Context, llv *snc.LVMLogicalVolume) error {
	if llv.DeletionTimestamp != nil {
		return nil
	}
	if err := client.IgnoreNotFound(r.cl.Delete(ctx, llv)); err != nil {
		return err
	}
	llv.DeletionTimestamp = ptr.To(metav1.Now())
	return nil
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
