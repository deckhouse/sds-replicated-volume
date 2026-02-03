/*
Copyright 2025 Flant JSC

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

package drbd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/indexes"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

// Reconciler reconciles DRBDResource objects for the current node.
type Reconciler struct {
	cl        client.Client
	nodeName  string
	portCache *PortCache
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

// NewReconciler creates a new Reconciler.
func NewReconciler(cl client.Client, nodeName string, portCache *PortCache) *Reconciler {
	return &Reconciler{
		cl:        cl,
		nodeName:  nodeName,
		portCache: portCache,
	}
}

// Reconcile reconciles a DRBDResource.
func (r *Reconciler) Reconcile(
	ctx context.Context,
	req reconcile.Request,
) (reconcile.Result, error) {
	rf := flow.BeginRootReconcile(ctx)
	rf.Log().V(1).Info("Reconciling DRBDResource", "name", req.Name)

	// Get DRBDResource
	drbdr, ok, err := r.getCurrentNodeDRBDR(rf.Ctx(), req)
	if err != nil {
		return rf.Fail(err).ToCtrl()
	}
	if !ok {
		// K8S object not found by Name - check for orphan/rename scenario
		if outcome := r.reconcileOrphanDRBD(rf.Ctx(), req.Name); outcome.ShouldReturn() {
			return outcome.ToCtrl()
		}
		return rf.Done().ToCtrl()
	}

	// Phase 1: Ensure finalizer (adds if needed for up resources)
	if outcome := r.reconcileFinalizer(rf.Ctx(), drbdr, true); outcome.ShouldReturn() {
		return outcome.ToCtrl()
	}

	// Phase 2: Ensure addresses in status (in-memory only, no patch yet)
	// Error here is non-critical (e.g., port allocation failure) - continue reconciliation
	node, err := r.getRequiredCurrentNode(rf.Ctx())
	if err != nil {
		return rf.Fail(err).ToCtrl()
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
	statusBase := drbdr.DeepCopy()
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
		return rf.Fail(reconcileErr).ToCtrl()
	}
	return rf.Done().ToCtrl()
}

// getCurrentNodeDRBDR gets the DRBDResource if it belongs to the current node.
func (r *Reconciler) getCurrentNodeDRBDR(
	ctx context.Context,
	req reconcile.Request,
) (*v1alpha1.DRBDResource, bool, error) {
	sf := flow.BeginStep(ctx, "get-drbdr")
	log := sf.Log()

	dr := &v1alpha1.DRBDResource{}
	if err := r.cl.Get(ctx, req.NamespacedName, dr); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return nil, false, flow.Wrapf(err, "getting DRBDResource")
		}
		log.V(1).Info("DRBDResource not found, skipping")
		return nil, false, nil
	}

	if dr.Spec.NodeName != r.nodeName {
		log.V(1).Info("DRBDResource belongs to different node, skipping", "nodeName", dr.Spec.NodeName)
		return nil, false, nil
	}

	return dr, true, nil
}

// getRequiredCurrentNode gets the Node object by name.
// Returns error if the Node is not found (required resource).
func (r *Reconciler) getRequiredCurrentNode(ctx context.Context) (*corev1.Node, error) {
	node := &corev1.Node{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: r.nodeName}, node); err != nil {
		return nil, flow.Wrapf(err, "getting Node %q", r.nodeName)
	}
	return node, nil
}

// getLVMLogicalVolume gets the LVMLogicalVolume by name.
// Returns (nil, nil) if not found.
func (r *Reconciler) getLVMLogicalVolume(ctx context.Context, name string) (*snc.LVMLogicalVolume, error) {
	if name == "" {
		return nil, nil
	}
	llv := &snc.LVMLogicalVolume{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: name}, llv); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return nil, nil
		}
		return nil, flow.Wrapf(err, "getting LVMLogicalVolume %q", name)
	}
	return llv, nil
}

// getLVMVolumeGroup gets the LVMVolumeGroup by name.
// Returns (nil, nil) if not found.
func (r *Reconciler) getLVMVolumeGroup(ctx context.Context, name string) (*snc.LVMVolumeGroup, error) {
	if name == "" {
		return nil, nil
	}
	lvg := &snc.LVMVolumeGroup{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: name}, lvg); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return nil, nil
		}
		return nil, flow.Wrapf(err, "getting LVMVolumeGroup %q", name)
	}
	return lvg, nil
}

// getLVGsOnNode lists LVMVolumeGroup objects on the current node using the index.
// Returns empty slice (not error) if none found.
func (r *Reconciler) getLVGsOnNode(ctx context.Context) ([]snc.LVMVolumeGroup, error) {
	list := &snc.LVMVolumeGroupList{}
	if err := r.cl.List(ctx, list, client.MatchingFields{
		indexes.IndexFieldLVGByNodeName: r.nodeName,
	}); err != nil {
		return nil, flow.Wrapf(err, "listing LVMVolumeGroups on node")
	}
	return list.Items, nil
}

// getLLVsForLVG lists LVMLogicalVolume objects for a specific LVG using the index.
// Returns empty slice (not error) if none found.
func (r *Reconciler) getLLVsForLVG(ctx context.Context, lvgName string) ([]snc.LVMLogicalVolume, error) {
	list := &snc.LVMLogicalVolumeList{}
	if err := r.cl.List(ctx, list, client.MatchingFields{
		indexes.IndexFieldLLVByLVGName: lvgName,
	}); err != nil {
		return nil, flow.Wrapf(err, "listing LVMLogicalVolumes for LVG %q", lvgName)
	}
	return list.Items, nil
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
		if err := r.patchDRBDR(rf.Ctx(), drbdr, base, ensureOutcome.OptimisticLockRequired()); err != nil {
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

// getDRBDRsOnNode lists all DRBDResource objects on this node using the field index.
// Returns empty slice if none found. Order is unspecified.
func (r *Reconciler) getDRBDRsOnNode(ctx context.Context) ([]v1alpha1.DRBDResource, error) {
	list := &v1alpha1.DRBDResourceList{}
	if err := r.cl.List(ctx, list, client.MatchingFields{
		indexes.IndexFieldDRBDRByNodeName: r.nodeName,
	}); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// reconcileOrphanDRBD handles DRBD resources that have no matching K8S object by Name.
// It searches by ActualNameOnTheNode and either renames or tears down the DRBD resource.
func (r *Reconciler) reconcileOrphanDRBD(ctx context.Context, k8sName string) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "reconcile-orphan-drbd")
	defer rf.OnEnd(&outcome)

	// Derive DRBD name from K8S name using helper
	drbdName := DRBDNameFromK8SName(k8sName)

	// List all DRBDRs on this node (using index)
	drbdrs, err := r.getDRBDRsOnNode(rf.Ctx())
	if err != nil {
		return rf.Fail(flow.Wrapf(err, "listing DRBDResources on node"))
	}

	// Find matches by ActualNameOnTheNode
	var matches []v1alpha1.DRBDResource
	for _, dr := range drbdrs {
		if dr.Spec.ActualNameOnTheNode == drbdName {
			matches = append(matches, dr)
		}
	}

	switch len(matches) {
	case 0:
		// Orphan: no K8S object owns this DRBD resource - tear it down
		rf.Log().Info("Cleaning up orphan DRBD resource", "drbdName", drbdName)
		downAction := DownAction{ResourceName: drbdName}
		if err := downAction.Execute(rf.Ctx()); err != nil {
			return rf.Fail(err)
		}
		return rf.Done()

	case 1:
		// Rename: K8S object exists with ActualNameOnTheNode matching this DRBD name
		dr := &matches[0]
		newName := DRBDNameFromK8SName(dr.Name)
		rf.Log().Info("Renaming DRBD resource to standard name", "from", drbdName, "to", newName)

		// 1. Rename DRBD resource
		renameAction := RenameAction{OldName: drbdName, NewName: newName}
		if err := renameAction.Execute(rf.Ctx()); err != nil {
			return rf.Fail(err)
		}

		// 2. Clear ActualNameOnTheNode
		base := dr.DeepCopy()
		dr.Spec.ActualNameOnTheNode = ""
		if err := r.patchDRBDR(rf.Ctx(), dr, base, false); err != nil {
			return rf.Fail(err)
		}

		return rf.Done()

	default:
		// Multiple K8S objects claim this DRBD name - configuration error
		return rf.Fail(fmt.Errorf("multiple DRBDResources (%d) have ActualNameOnTheNode=%q on node %q",
			len(matches), drbdName, r.nodeName))
	}
}
