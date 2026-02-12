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

package drbdr

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/equality"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdsetup"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

// Reconciler reconciles DRBDResource objects for the current node.
type Reconciler struct {
	cl        client.Client
	nodeName  string
	portCache *PortCache
}

var _ reconcile.TypedReconciler[DRBDReconcileRequest] = (*Reconciler)(nil)

// NewReconciler creates a new Reconciler.
func NewReconciler(cl client.Client, nodeName string, portCache *PortCache) *Reconciler {
	return &Reconciler{
		cl:        cl,
		nodeName:  nodeName,
		portCache: portCache,
	}
}

// Reconcile reconciles a DRBDResource based on the request type.
func (r *Reconciler) Reconcile(
	ctx context.Context,
	req DRBDReconcileRequest,
) (reconcile.Result, error) {
	rf := flow.BeginRootReconcile(ctx)

	// Edge case: scanner found non-prefixed DRBD resource
	if req.ActualNameOnTheNode != "" {
		return r.reconcileByActualName(rf.Ctx(), req.ActualNameOnTheNode).ToCtrl()
	}

	// Main scenario: reconcile by K8S name
	rf.Log().V(1).Info("Reconciling DRBDResource", "name", req.Name)
	drbdr, ok, err := r.getDRBDRByName(rf.Ctx(), req.Name)
	if err != nil {
		return rf.Fail(err).ToCtrl()
	}
	if !ok {
		return rf.Done().ToCtrl() // Deleted
	}

	// Skip if resource belongs to different node (predicates should filter this normally)
	if drbdr.Spec.NodeName != r.nodeName {
		rf.Log().V(1).Info("DRBDResource belongs to different node, skipping", "nodeName", drbdr.Spec.NodeName)
		return rf.Done().ToCtrl()
	}

	return r.reconcileDRBDR(rf.Ctx(), drbdr).ToCtrl()
}

// reconcileByActualName handles requests with ActualNameOnTheNode (non-prefixed DRBD resources).
// Either finds the owning DRBDResource and reconciles it, or tears down orphan.
//
// Reconcile pattern: Pure orchestration
func (r *Reconciler) reconcileByActualName(
	ctx context.Context,
	actualName string,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "by-actual-name", "actualNameOnTheNode", actualName)
	defer rf.OnEnd(&outcome)

	// Find K8S resource that has this ActualNameOnTheNode
	drbdrs, err := r.getDRBDRsOnNode(rf.Ctx())
	if err != nil {
		return rf.Fail(err)
	}

	var matches []v1alpha1.DRBDResource
	for i := range drbdrs {
		if drbdrs[i].Spec.ActualNameOnTheNode == actualName {
			matches = append(matches, drbdrs[i])
		}
	}

	switch len(matches) {
	case 0:
		// Orphan - no K8S object owns this DRBD resource, tear it down
		outcome = r.reconcileOrphanDRBD(rf.Ctx(), actualName)
		return
	case 1:
		outcome = r.reconcileDRBDR(rf.Ctx(), &matches[0])
		return
	default:
		return rf.Fail(fmt.Errorf("multiple DRBDResources (%d) have ActualNameOnTheNode=%q on node %q",
			len(matches), actualName, r.nodeName))
	}
}

// reconcileDRBDR performs the main reconciliation for a DRBDResource.
//
// Reconcile pattern: In-place reconciliation
func (r *Reconciler) reconcileDRBDR(
	ctx context.Context,
	drbdr *v1alpha1.DRBDResource,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "drbdr", "name", drbdr.Name)
	defer rf.OnEnd(&outcome)

	// Phase 0: Handle ActualNameOnTheNode rename if set
	if drbdr.Spec.ActualNameOnTheNode != "" {
		outcome = r.reconcileActualNameOnTheNode(rf.Ctx(), drbdr)
		return
	}

	// Phase 1: Ensure finalizer (adds if needed for up resources)
	if finalizerOutcome := r.reconcileFinalizer(rf.Ctx(), drbdr, true); finalizerOutcome.ShouldReturn() {
		if finalizerOutcome.Error() != nil {
			return rf.Fail(finalizerOutcome.Error())
		}
		return rf.Done()
	}

	// Phase 2: Ensure addresses in status (in-memory only, no patch yet)
	// Error here is non-critical (e.g., port allocation failure) - continue reconciliation
	statusBase := drbdr.DeepCopy()
	node, err := r.getRequiredCurrentNode(rf.Ctx())
	if err != nil {
		return rf.Fail(err)
	}
	addrErr := ensureAddresses(rf.Ctx(), drbdr, node, r.portCache.Allocate).Error()

	// Phase 3: Add finalizer to intended LLV (before DRBD operations)
	var intendedDisk string
	var llvErr error
	if drbdr.Spec.Type == v1alpha1.DRBDResourceTypeDiskful {
		intendedDisk, llvErr = r.reconcileLLVFinalizerAdd(rf.Ctx(), drbdr.Spec.LVMLogicalVolumeName)
	}

	// Phase 4: DRBD convergence
	iState := computeIntendedDRBDState(drbdr, intendedDisk)

	aState, aErr := observeActualDRBDState(rf.Ctx(), DRBDResourceNameOnTheNode(drbdr))
	aErr = ConfiguredReasonError(aErr, v1alpha1.DRBDResourceCondConfiguredReasonStateQueryFailed)

	// Compute and execute DRBD actions
	targetActions := computeTargetDRBDActions(iState, aState)
	maintenanceMode := drbdr.Spec.Maintenance == v1alpha1.MaintenanceModeNoResourceReconciliation
	refreshNeeded, drbdErr := convergeDRBDState(rf.Ctx(), targetActions, maintenanceMode)

	// Refresh actual state if DRBD state was changed
	var aErr2 error
	if refreshNeeded {
		aState, aErr2 = observeActualDRBDState(rf.Ctx(), DRBDResourceNameOnTheNode(drbdr))
		aErr2 = ConfiguredReasonError(aErr2, v1alpha1.DRBDResourceCondConfiguredReasonStateQueryFailed)
	}

	// Remove finalizer from previous LLV (only if DRBD has actually detached from it)
	// Also computes actualLLVName for report state
	actualLLVName, actualLLVErr := r.reconcileLLVFinalizerRemove(rf.Ctx(), aState, drbdr.Status.ActiveConfiguration)
	if actualLLVErr != nil {
		llvErr = errors.Join(llvErr, actualLLVErr)
	}

	// Phase 5: Report and status patch
	reconcileErr := errors.Join(addrErr, llvErr, aErr, aErr2, drbdErr)
	if ensureOutcome := ensureReportState(rf.Ctx(), aState, drbdr, actualLLVName, reconcileErr, maintenanceMode); ensureOutcome.Error() != nil {
		reconcileErr = errors.Join(reconcileErr, ensureOutcome.Error())
	}

	// Patch status if changed
	if !equality.Semantic.DeepEqual(statusBase.Status, drbdr.Status) {
		statusPatchErr := r.patchDRBDRStatus(rf.Ctx(), drbdr, statusBase, false)
		// Ignore "not found" error if object was being deleted
		if statusPatchErr != nil && !(drbdr.DeletionTimestamp != nil && client.IgnoreNotFound(statusPatchErr) == nil) {
			reconcileErr = errors.Join(reconcileErr, statusPatchErr)
		}
	}

	// Phase 6: Finalize (removes finalizer if needed for down/deleted resources)
	if finalizerOutcome := r.reconcileFinalizer(rf.Ctx(), drbdr, false); finalizerOutcome.ShouldReturn() {
		if finalizerOutcome.Error() != nil {
			reconcileErr = errors.Join(reconcileErr, finalizerOutcome.Error())
		}
	}

	if reconcileErr != nil {
		return rf.Fail(reconcileErr)
	}
	return rf.Done()
}

// reconcileOrphanDRBD handles DRBD resources with no matching K8S object.
// actualName is the actual DRBD name on the node to tear down.
//
// Reconcile pattern: In-place reconciliation
func (r *Reconciler) reconcileOrphanDRBD(
	ctx context.Context,
	actualName string,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "orphan-drbd", "actualNameOnTheNode", actualName)
	defer rf.OnEnd(&outcome)

	rf.Log().Info("Cleaning up orphan DRBD resource")

	downAction := DownAction{ResourceName: actualName}
	if err := downAction.Execute(rf.Ctx()); err != nil {
		return rf.Fail(err)
	}
	return rf.Done()
}

// reconcileActualNameOnTheNode handles renaming DRBD resource from ActualNameOnTheNode to standard name.
//
// Reconcile pattern: In-place reconciliation
func (r *Reconciler) reconcileActualNameOnTheNode(
	ctx context.Context,
	drbdr *v1alpha1.DRBDResource,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "actual-name-on-the-node")
	defer rf.OnEnd(&outcome)

	oldName := drbdr.Spec.ActualNameOnTheNode
	newName := DRBDNameFromK8SName(drbdr.Name)

	rf.Log().Info("Renaming DRBD resource", "from", oldName, "to", newName)

	err := drbdsetup.ExecuteRename(rf.Ctx(), oldName, newName)

	switch {
	case err == nil:
		// Rename succeeded - clear ActualNameOnTheNode
		outcome = r.reconcileClearActualName(rf.Ctx(), drbdr)
		return

	case errors.Is(err, drbdsetup.ErrRenameUnknownResource):
		// Old name doesn't exist - check if new name exists (rename might have succeeded before)
		status, statusErr := drbdsetup.ExecuteStatus(rf.Ctx(), newName)
		if statusErr != nil {
			return rf.Fail(statusErr)
		}
		if len(status) == 0 {
			// Neither old nor new name exists - error
			return rf.Failf(err, "DRBD resource not found: oldName=%s, newName=%s", oldName, newName)
		}
		// New name exists - rename succeeded previously, clear ActualNameOnTheNode
		rf.Log().Info("DRBD resource already renamed", "newName", newName)
		outcome = r.reconcileClearActualName(rf.Ctx(), drbdr)
		return

	case errors.Is(err, drbdsetup.ErrRenameAlreadyExists):
		// Both names exist - configuration error
		return rf.Failf(err, "both DRBD resource names exist: oldName=%s, newName=%s", oldName, newName)

	default:
		return rf.Fail(err)
	}
}

// reconcileClearActualName patches the spec to clear ActualNameOnTheNode.
//
// Reconcile pattern: In-place reconciliation
func (r *Reconciler) reconcileClearActualName(
	ctx context.Context,
	drbdr *v1alpha1.DRBDResource,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "clear-actual-name")
	defer rf.OnEnd(&outcome)

	base := drbdr.DeepCopy()
	drbdr.Spec.ActualNameOnTheNode = ""
	if err := r.patchDRBDR(rf.Ctx(), drbdr, base, false); err != nil {
		return rf.Fail(err)
	}
	return rf.Done()
}

// patchLLV patches an LVMLogicalVolume (main patch domain).
func (r *Reconciler) patchLLV(ctx context.Context, obj, base *snc.LVMLogicalVolume) error {
	return r.cl.Patch(ctx, obj, client.MergeFrom(base))
}

// reconcileLLVFinalizerAdd adds AgentFinalizer to an LLV and returns its disk path.
func (r *Reconciler) reconcileLLVFinalizerAdd(ctx context.Context, llvName string) (diskPath string, err error) {
	if llvName == "" {
		return "", nil
	}

	llv, err := r.getLVMLogicalVolume(ctx, llvName)
	if err != nil {
		return "", ConfiguredReasonError(err, v1alpha1.DRBDResourceCondConfiguredReasonStateQueryFailed)
	}
	if llv == nil {
		return "", ConfiguredReasonError(
			flow.Wrapf(nil, "LVMLogicalVolume %q not found", llvName),
			v1alpha1.DRBDResourceCondConfiguredReasonStateQueryFailed,
		)
	}

	// Add finalizer if not present
	if !obju.HasFinalizer(llv, v1alpha1.AgentFinalizer) {
		llvBase := llv.DeepCopy()
		obju.AddFinalizer(llv, v1alpha1.AgentFinalizer)
		if err := r.patchLLV(ctx, llv, llvBase); err != nil {
			return "", flow.Wrapf(err, "adding finalizer on LLV %q", llv.Name)
		}
	}

	// Compute disk path
	lvg, err := r.getLVMVolumeGroup(ctx, llv.Spec.LVMVolumeGroupName)
	if err != nil {
		return "", ConfiguredReasonError(err, v1alpha1.DRBDResourceCondConfiguredReasonStateQueryFailed)
	}
	if lvg == nil {
		return "", ConfiguredReasonError(
			flow.Wrapf(nil, "LVMVolumeGroup %q not found", llv.Spec.LVMVolumeGroupName),
			v1alpha1.DRBDResourceCondConfiguredReasonStateQueryFailed,
		)
	}

	return formatLVMDevicePath(lvg.Spec.ActualVGNameOnTheNode, llv.Spec.ActualLVNameOnTheNode), nil
}

// reconcileLLVFinalizerRemove removes AgentFinalizer from an LLV if it's no longer in use.
// It computes actualLLVName from the DRBD actual state and only removes the finalizer if
// previousLLVName != actualLLVName (meaning DRBD has detached from it).
// Returns actualLLVName for use in report state.
func (r *Reconciler) reconcileLLVFinalizerRemove(
	ctx context.Context,
	aState ActualDRBDState,
	statusActiveConfig *v1alpha1.DRBDResourceActiveConfiguration,
) (actualLLVName string, err error) {
	// Compute actualLLVName from actualDisk
	if !aState.IsZero() && len(aState.Volumes()) > 0 {
		if actualDisk := aState.Volumes()[0].BackingDisk(); actualDisk != "" {
			lvgsOnNode, err := r.getLVGsOnNode(ctx)
			if err != nil {
				return "", ConfiguredReasonError(err, v1alpha1.DRBDResourceCondConfiguredReasonStateQueryFailed)
			}

			var allLLVs []snc.LVMLogicalVolume
			for i := range lvgsOnNode {
				llvs, err := r.getLLVsForLVG(ctx, lvgsOnNode[i].Name)
				if err != nil {
					return "", ConfiguredReasonError(err, v1alpha1.DRBDResourceCondConfiguredReasonStateQueryFailed)
				}
				allLLVs = append(allLLVs, llvs...)
			}

			actualLLVName = computeLLVNameFromDiskPath(actualDisk, lvgsOnNode, allLLVs)
		}
	}

	// Only remove finalizer if previous LLV differs from actual
	if statusActiveConfig == nil ||
		statusActiveConfig.LVMLogicalVolumeName == "" ||
		statusActiveConfig.LVMLogicalVolumeName == actualLLVName {
		return actualLLVName, nil
	}

	llv, err := r.getLVMLogicalVolume(ctx, statusActiveConfig.LVMLogicalVolumeName)
	if err != nil {
		return actualLLVName, flow.Wrapf(err, "getting previous LLV %q", statusActiveConfig.LVMLogicalVolumeName)
	}
	if llv == nil {
		return actualLLVName, nil
	}

	if obju.HasFinalizer(llv, v1alpha1.AgentFinalizer) {
		llvBase := llv.DeepCopy()
		obju.RemoveFinalizer(llv, v1alpha1.AgentFinalizer)
		if err := r.patchLLV(ctx, llv, llvBase); err != nil {
			return actualLLVName, flow.Wrapf(err, "removing finalizer on LLV %q", llv.Name)
		}
	}

	return actualLLVName, nil
}

// reconcileFinalizer adds or removes the agent finalizer based on resource state.
// When adding=true, it adds the finalizer if the resource is up and not in cleanup.
// When adding=false, it removes the finalizer if the resource is down or being deleted.
func (r *Reconciler) reconcileFinalizer(
	ctx context.Context,
	drbdr *v1alpha1.DRBDResource,
	adding bool,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "reconcile-finalizer")
	defer rf.OnEnd(&outcome)

	isUpAndNotInCleanup := true
	if drbdr.DeletionTimestamp != nil && !obju.HasFinalizersOtherThan(drbdr, v1alpha1.AgentFinalizer) {
		isUpAndNotInCleanup = false
	} else if drbdr.Spec.State == v1alpha1.DRBDResourceStateDown {
		isUpAndNotInCleanup = false
	}

	base := drbdr.DeepCopy()
	ensureOutcome := ensureFinalizer(rf.Ctx(), drbdr, adding, isUpAndNotInCleanup)
	if ensureOutcome.Error() != nil {
		return rf.Fail(ensureOutcome.Error())
	}

	// Patch main object if finalizers changed
	if ensureOutcome.DidChange() {
		if err := r.patchDRBDR(rf.Ctx(), drbdr, base, true); err != nil {
			return rf.Fail(err)
		}
	}

	return rf.Continue()
}

// ensureFinalizer adds or removes the finalizer based on resource state.
func ensureFinalizer(
	ctx context.Context,
	drbdr *v1alpha1.DRBDResource,
	adding bool,
	isUpAndNotInCleanup bool,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "ensure-finalizer")
	defer ef.OnEnd(&outcome)

	var changed bool
	if adding && isUpAndNotInCleanup {
		changed = obju.AddFinalizer(drbdr, v1alpha1.AgentFinalizer)
	} else if !adding && !isUpAndNotInCleanup {
		changed = obju.RemoveFinalizer(drbdr, v1alpha1.AgentFinalizer)
	}
	return ef.Ok().ReportChangedIf(changed)
}

// patchDRBDR patches the main object (metadata/spec).
func (r *Reconciler) patchDRBDR(
	ctx context.Context,
	obj, base *v1alpha1.DRBDResource,
	optimisticLock bool,
) error {
	var patch client.Patch
	if optimisticLock {
		patch = client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})
	} else {
		patch = client.MergeFrom(base)
	}
	return r.cl.Patch(ctx, obj, patch)
}

// patchDRBDRStatus patches the status subresource.
func (r *Reconciler) patchDRBDRStatus(
	ctx context.Context,
	obj, base *v1alpha1.DRBDResource,
	optimisticLock bool,
) error {
	var patch client.Patch
	if optimisticLock {
		patch = client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})
	} else {
		patch = client.MergeFrom(base)
	}
	return r.cl.Status().Patch(ctx, obj, patch)
}

// convergeDRBDState executes DRBD actions to converge to the target state.
// Returns whether DRBD state was changed (requiring a refresh) and any error.
// When maintenanceMode is true, actions are logged but not executed.
func convergeDRBDState(ctx context.Context, actions DRBDActions, maintenanceMode bool) (refreshNeeded bool, err error) {
	log := log.FromContext(ctx)
	for _, action := range actions {
		log.Info("DRBD action", "action", action.String(), "maintenanceMode", maintenanceMode)
		if maintenanceMode {
			continue
		}
		if err := action.Execute(ctx); err != nil {
			return refreshNeeded, err
		}
		refreshNeeded = true
	}
	return refreshNeeded, nil
}

// formatLVMDevicePath formats the path to an LVM logical volume device.
func formatLVMDevicePath(vgName, lvName string) string {
	return "/dev/" + vgName + "/" + lvName
}

// computeLLVNameFromDiskPath returns the LLV K8s name for a disk path.
// Disk path format: /dev/{actualVGNameOnTheNode}/{actualLVNameOnTheNode}
// Returns empty string if no matching LLV found.
//
// This is a pure function - all data must be pre-loaded.
func computeLLVNameFromDiskPath(
	diskPath string,
	lvgs []snc.LVMVolumeGroup,
	llvs []snc.LVMLogicalVolume,
) string {
	if diskPath == "" {
		return ""
	}

	// Parse path: /dev/{vgName}/{lvName}
	parts := strings.Split(diskPath, "/")
	if len(parts) != 4 || parts[0] != "" || parts[1] != "dev" {
		return ""
	}
	vgName, lvName := parts[2], parts[3]

	// Find LVG by actualVGNameOnTheNode
	var matchingLVG *snc.LVMVolumeGroup
	for i := range lvgs {
		if lvgs[i].Spec.ActualVGNameOnTheNode == vgName {
			matchingLVG = &lvgs[i]
			break
		}
	}
	if matchingLVG == nil {
		return ""
	}

	// Find LLV by lvmVolumeGroupName and actualLVNameOnTheNode
	for i := range llvs {
		llv := &llvs[i]
		if llv.Spec.LVMVolumeGroupName == matchingLVG.Name &&
			llv.Spec.ActualLVNameOnTheNode == lvName {
			return llv.Name
		}
	}

	return ""
}
