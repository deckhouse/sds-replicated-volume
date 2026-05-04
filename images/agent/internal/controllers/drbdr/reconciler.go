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
	"os"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdutils"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

// Reconciler reconciles DRBDResource objects for the current node.
type Reconciler struct {
	cl           client.Client
	nodeName     string
	portRegistry *PortRegistry
}

var _ reconcile.TypedReconciler[DRBDReconcileRequest] = (*Reconciler)(nil)

// NewReconciler creates a new Reconciler.
func NewReconciler(cl client.Client, nodeName string, portRegistry *PortRegistry) *Reconciler {
	return &Reconciler{
		cl:           cl,
		nodeName:     nodeName,
		portRegistry: portRegistry,
	}
}

// isInCleanup returns true when the DRBDResource is marked for deletion and
// the agent holds the only remaining finalizer (ready for final cleanup).
func isInCleanup(drbdr *v1alpha1.DRBDResource) bool {
	return drbdr.DeletionTimestamp != nil && !obju.HasFinalizersOtherThan(drbdr, v1alpha1.AgentFinalizer)
}

// isUpAndNotInCleanup returns true when the DRBDResource is NOT being torn
// down — it is neither being deleted nor in spec.state=Down.
func isUpAndNotInCleanup(drbdr *v1alpha1.DRBDResource) bool {
	return !isInCleanup(drbdr) && drbdr.Spec.State != v1alpha1.DRBDResourceStateDown
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
		return r.reconcileOrphanDRBD(rf.Ctx(), req.Name).ToCtrl()
	}

	// Skip if resource belongs to different node (predicates should filter this normally)
	if drbdr.Spec.NodeName != r.nodeName {
		rf.Log().V(1).Info("DRBDResource belongs to different node, skipping", "nodeName", drbdr.Spec.NodeName)
		return rf.Done().ToCtrl()
	}

	return r.reconcileDRBDR(rf.Ctx(), drbdr).ToCtrl()
}

// reconcileByActualName handles requests with ActualNameOnTheNode (non-prefixed DRBD resources).
// Either finds the owning DRBDResource and reconciles it, or skips (not ours).
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
		rf.Log().V(1).Info("Non-managed DRBD resource, skipping")
		return rf.Done()
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

	maintenanceMode := drbdr.Spec.Maintenance == v1alpha1.MaintenanceModeNoResourceReconciliation

	// Phase 0: Handle ActualNameOnTheNode rename if set.
	// Maintenance mode skips the rename but falls through to Phase 4+
	// where DRBDResourceNameOnTheNode returns the old name correctly.
	if drbdr.Spec.ActualNameOnTheNode != "" {
		if !maintenanceMode {
			outcome = r.reconcileActualNameOnTheNode(rf.Ctx(), drbdr)
			return
		}
		rf.Log().Info("Skipping ActualNameOnTheNode rename due to maintenance mode",
			"actualName", drbdr.Spec.ActualNameOnTheNode)
	}

	// Phase 1: Ensure finalizer (adds if needed for up resources)
	if finalizerOutcome := r.reconcileFinalizer(rf.Ctx(), drbdr, true, isUpAndNotInCleanup(drbdr)); finalizerOutcome.ShouldReturn() {
		if finalizerOutcome.Error() != nil {
			return rf.Fail(finalizerOutcome.Error())
		}
		return rf.Done()
	}

	// Snapshot deletion state after Phase 1. Phase 1 may have patched the
	// object, refreshing its metadata from the API server. This snapshot is
	// used by all subsequent phases to ensure consistent behavior even if
	// the status patch further mutates drbdr metadata.
	upAndNotInCleanup := isUpAndNotInCleanup(drbdr)

	// Observe DRBD state early — the result is used both by Phase 2
	// (existing port adoption) and Phase 4 (convergence).
	drbdResName := DRBDResourceNameOnTheNode(drbdr)
	aState, aErr := observeActualDRBDState(rf.Ctx(), drbdResName)
	aErr = ConfiguredReasonError(aErr, v1alpha1.DRBDResourceCondConfiguredReasonStateQueryFailed)

	// Phase 2: Ensure addresses are persisted (patch with optimistic lock).
	// Must happen before DRBD convergence so that stale-cache races cause
	// a conflict error here, before any destructive kernel actions execute.
	existingPorts := existingPortsFromState(aState)
	if addrOutcome := r.reconcileAddresses(rf.Ctx(), drbdr, existingPorts); addrOutcome.ShouldReturn() {
		if addrOutcome.Error() != nil {
			return rf.Fail(addrOutcome.Error())
		}
		return rf.Done()
	}

	// Snapshot status after addresses are persisted. The addresses patch may
	// have updated the object's resourceVersion; taking the snapshot here
	// ensures the final status patch uses the correct base.
	statusBase := drbdr.DeepCopy()

	// Collect the pending-release list from status.
	var pendingRelease []string
	if drbdr.Status.ActiveConfiguration != nil {
		pendingRelease = drbdr.Status.ActiveConfiguration.LLVFinalizersToRelease
	}

	// Recovery: if DRBD has a disk whose LLV is not yet tracked in the
	// pending-release list, add it and requeue. This handles partial
	// reconciliation where a previous cycle added a finalizer but failed
	// the status patch.
	actualDiskLLV := r.computeActualLLVName(rf.Ctx(), aState)
	if actualDiskLLV != "" && !slices.Contains(pendingRelease, actualDiskLLV) {
		if recoveryOutcome := r.reconcileLLVRecovery(rf.Ctx(), drbdr, statusBase, actualDiskLLV); recoveryOutcome.ShouldReturn() {
			return recoveryOutcome
		}
	}

	// Step 1: Add finalizer to intended LLV (before DRBD operations).
	// Skip in maintenance (no attach will happen) and when not Up+Diskful.
	// Record the LLV in the pending-release list (written to status in Step 4)
	// so we never lose track of it.
	var intendedDisk string
	var llvErr error
	if !maintenanceMode && drbdr.Spec.Type == v1alpha1.DRBDResourceTypeDiskful && upAndNotInCleanup {
		llvName := drbdr.Spec.LVMLogicalVolumeName
		if llvName != "" && !slices.Contains(pendingRelease, llvName) {
			pendingRelease = append(pendingRelease, llvName)
		}
		intendedDisk, llvErr = r.reconcileLLVFinalizerAdd(rf.Ctx(), llvName)
	}

	// Step 2: DRBD convergence.
	if aErr == nil {
		if diskErr := observeActualDiskState(rf.Ctx(), aState, intendedDisk, drbdr.Status.DeviceUUID); diskErr != nil {
			aErr = ConfiguredReasonError(diskErr, v1alpha1.DRBDResourceCondConfiguredReasonStateQueryFailed)
		}
	}

	iState := computeIntendedDRBDState(drbdr, intendedDisk, upAndNotInCleanup)

	targetActions := computeTargetDRBDActions(iState, aState)

	refreshNeeded, drbdErr := convergeDRBDState(rf.Ctx(), targetActions, maintenanceMode)

	drbdErr = ensureLocalPortConflictResolved(rf.Ctx(), drbdr, drbdErr, r.portRegistry.Allocate)

	var aErr2 error
	if refreshNeeded {
		aState, aErr2 = observeActualDRBDState(rf.Ctx(), drbdResName)
		if aErr2 == nil {
			aErr2 = observeActualDiskState(rf.Ctx(), aState, intendedDisk, drbdr.Status.DeviceUUID)
		}
		aErr2 = ConfiguredReasonError(aErr2, v1alpha1.DRBDResourceCondConfiguredReasonStateQueryFailed)
	}

	// Step 3: Release finalizers from LLVs we no longer need (after detach).
	// Skip in maintenance (no detach happened).
	if !maintenanceMode {
		keepLLV := ""
		if upAndNotInCleanup {
			keepLLV = drbdr.Spec.LVMLogicalVolumeName
		}
		for _, llv := range pendingRelease {
			if llv == keepLLV {
				continue
			}
			if releaseErr := r.releaseLLVFinalizer(rf.Ctx(), llv); releaseErr != nil {
				llvErr = errors.Join(llvErr, releaseErr)
				continue
			}
			pendingRelease = slices.DeleteFunc(pendingRelease, func(s string) bool { return s == llv })
		}
	}

	// Step 4: Report and status patch.
	// lvmLogicalVolumeName always reflects actual DRBD state.
	newStatusLLV := r.computeActualLLVName(rf.Ctx(), aState)

	reconcileErr := errors.Join(llvErr, aErr, aErr2, drbdErr)
	if ensureOutcome := ensureReportState(rf.Ctx(), aState, drbdr, newStatusLLV, reconcileErr, maintenanceMode); ensureOutcome.Error() != nil {
		reconcileErr = errors.Join(reconcileErr, ensureOutcome.Error())
	}

	// Write the pending-release list after ensureReportState (which may
	// initialize ActiveConfiguration via aState.Report).
	ensureActiveConfiguration(drbdr).LLVFinalizersToRelease = pendingRelease

	// Patch status if changed
	if !equality.Semantic.DeepEqual(statusBase.Status, drbdr.Status) {
		statusPatchErr := r.patchDRBDRStatus(rf.Ctx(), drbdr, statusBase, true)
		// Ignore "not found" error if object was being deleted
		if statusPatchErr != nil && (drbdr.DeletionTimestamp == nil || client.IgnoreNotFound(statusPatchErr) != nil) {
			reconcileErr = errors.Join(reconcileErr, statusPatchErr)
		}
	}

	// Phase 6: Finalize (removes finalizer if needed for down/deleted resources)
	if finalizerOutcome := r.reconcileFinalizer(rf.Ctx(), drbdr, false, upAndNotInCleanup); finalizerOutcome.ShouldReturn() {
		if finalizerOutcome.Error() != nil {
			reconcileErr = errors.Join(reconcileErr, finalizerOutcome.Error())
		}
	}

	if reconcileErr != nil {
		return rf.Fail(reconcileErr)
	}
	return rf.Done()
}

// reconcileOrphanDRBD handles owned DRBD resources with no matching K8S object.
// k8sName is the K8S resource name; the actual DRBD name is derived from it.
// If the DRBD resource does not exist on the node, this is a no-op.
//
// Reconcile pattern: In-place reconciliation
func (r *Reconciler) reconcileOrphanDRBD(
	ctx context.Context,
	k8sName string,
) (outcome flow.ReconcileOutcome) {
	drbdName := DRBDNameFromK8SName(k8sName)
	rf := flow.BeginReconcile(ctx, "orphan-drbd", "drbdResourceName", drbdName)
	defer rf.OnEnd(&outcome)

	status, err := drbdutils.ExecuteStatus(rf.Ctx(), drbdName)
	if err != nil {
		return rf.Fail(err)
	}
	if len(status) == 0 {
		return rf.Done()
	}

	rf.Log().Info("Cleaning up orphan DRBD resource")

	_ = os.Remove(DeviceSymlinkPath(k8sName))

	downAction := DownAction{ResourceName: drbdName}
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

	err := drbdutils.ExecuteRename(rf.Ctx(), oldName, newName)

	switch {
	case err == nil:
		// Rename succeeded - clear ActualNameOnTheNode
		outcome = r.reconcileClearActualName(rf.Ctx(), drbdr)
		return

	case errors.Is(err, drbdutils.ErrRenameUnknownResource):
		// Old name doesn't exist - check if new name exists (rename might have succeeded before)
		status, statusErr := drbdutils.ExecuteStatus(rf.Ctx(), newName)
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

	case errors.Is(err, drbdutils.ErrRenameAlreadyExists):
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
			fmt.Errorf("LVMLogicalVolume %q not found", llvName),
			v1alpha1.DRBDResourceCondConfiguredReasonStateQueryFailed,
		)
	}

	// Do not add finalizer to a deleting LLV — treat it as unavailable.
	if llv.DeletionTimestamp != nil {
		return "", ConfiguredReasonError(
			fmt.Errorf("LVMLogicalVolume %q is being deleted", llvName),
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
			fmt.Errorf("LVMVolumeGroup %q not found", llv.Spec.LVMVolumeGroupName),
			v1alpha1.DRBDResourceCondConfiguredReasonStateQueryFailed,
		)
	}

	return formatLVMDevicePath(lvg.Spec.ActualVGNameOnTheNode, llv.Spec.ActualLVNameOnTheNode), nil
}

// reconcileLLVRecovery adds an untracked LLV to the pending-release list
// and requeues. This handles the case where a previous cycle added a
// finalizer but failed the status patch.
func (r *Reconciler) reconcileLLVRecovery(
	ctx context.Context,
	drbdr *v1alpha1.DRBDResource,
	statusBase *v1alpha1.DRBDResource,
	llvName string,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "llv-recovery")
	defer rf.OnEnd(&outcome)

	ac := ensureActiveConfiguration(drbdr)
	ac.LLVFinalizersToRelease = append(ac.LLVFinalizersToRelease, llvName)
	if err := r.patchDRBDRStatus(rf.Ctx(), drbdr, statusBase, true); err != nil {
		return rf.Fail(err)
	}
	return rf.DoneAndRequeue()
}

func ensureActiveConfiguration(drbdr *v1alpha1.DRBDResource) *v1alpha1.DRBDResourceActiveConfiguration {
	if drbdr.Status.ActiveConfiguration == nil {
		drbdr.Status.ActiveConfiguration = &v1alpha1.DRBDResourceActiveConfiguration{}
	}
	return drbdr.Status.ActiveConfiguration
}

// computeActualLLVName reverse-computes the LLV name from the DRBD actual
// backing disk path. Returns empty if DRBD has no disk attached.
func (r *Reconciler) computeActualLLVName(ctx context.Context, aState ActualDRBDState) string {
	if aState.IsZero() || len(aState.Volumes()) == 0 {
		return ""
	}
	actualDisk := aState.Volumes()[0].BackingDisk()
	if actualDisk == "" {
		return ""
	}

	lvgsOnNode, err := r.getLVGsOnNode(ctx)
	if err != nil {
		return ""
	}

	var allLLVs []snc.LVMLogicalVolume
	for i := range lvgsOnNode {
		llvs, err := r.getLLVsForLVG(ctx, lvgsOnNode[i].Name)
		if err != nil {
			return ""
		}
		allLLVs = append(allLLVs, llvs...)
	}

	return computeLLVNameFromDiskPath(actualDisk, lvgsOnNode, allLLVs)
}

// computeLLVNameFromDiskPath returns the LLV K8s name for a disk path.
// Disk path format: /dev/{actualVGNameOnTheNode}/{actualLVNameOnTheNode}
func computeLLVNameFromDiskPath(
	diskPath string,
	lvgs []snc.LVMVolumeGroup,
	llvs []snc.LVMLogicalVolume,
) string {
	parts := strings.Split(diskPath, "/")
	if len(parts) != 4 || parts[0] != "" || parts[1] != "dev" {
		return ""
	}
	vgName, lvName := parts[2], parts[3]

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

	for i := range llvs {
		if llvs[i].Spec.LVMVolumeGroupName == matchingLVG.Name &&
			llvs[i].Spec.ActualLVNameOnTheNode == lvName {
			return llvs[i].Name
		}
	}
	return ""
}

func (r *Reconciler) releaseLLVFinalizer(ctx context.Context, llvName string) error {
	llv, err := r.getLVMLogicalVolume(ctx, llvName)
	if err != nil {
		return flow.Wrapf(err, "getting LLV %q for finalizer release", llvName)
	}
	if llv == nil {
		return nil
	}

	if obju.HasFinalizer(llv, v1alpha1.AgentFinalizer) {
		llvBase := llv.DeepCopy()
		obju.RemoveFinalizer(llv, v1alpha1.AgentFinalizer)
		if err := r.patchLLV(ctx, llv, llvBase); err != nil {
			if client.IgnoreNotFound(err) == nil {
				return nil
			}
			return flow.Wrapf(err, "releasing finalizer on LLV %q", llvName)
		}
	}

	return nil
}

// reconcileFinalizer adds or removes the agent finalizer based on resource state.
// When adding=true, it adds the finalizer if the resource is up and not in cleanup.
// When adding=false, it removes the finalizer if the resource is down or being deleted.
func (r *Reconciler) reconcileFinalizer(
	ctx context.Context,
	drbdr *v1alpha1.DRBDResource,
	adding bool,
	isUpAndNotInCleanup bool,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "reconcile-finalizer")
	defer rf.OnEnd(&outcome)

	base := drbdr.DeepCopy()
	ensureOutcome := ensureFinalizer(rf.Ctx(), drbdr, adding, isUpAndNotInCleanup)
	if ensureOutcome.Error() != nil {
		return rf.Fail(ensureOutcome.Error())
	}

	// Patch main object if finalizers changed
	if ensureOutcome.DidChange() {
		if err := r.patchDRBDR(rf.Ctx(), drbdr, base, true); err != nil {
			if apierrors.IsNotFound(err) {
				return rf.Done()
			}
			return rf.Fail(err)
		}
	}

	return rf.Continue()
}

// reconcileAddresses ensures addresses (IPs + ports) are persisted in status
// before DRBD convergence. This prevents stale-cache races: if the informer
// cache is behind, the optimistic-lock patch fails and the reconcile retries
// before any destructive DRBD kernel actions execute.
//
// existingPorts provides ports from a pre-existing DRBD resource on the node.
// When status.addresses has no port for an IP, the existing port is adopted
// before falling back to portAllocator.
func (r *Reconciler) reconcileAddresses(
	ctx context.Context,
	drbdr *v1alpha1.DRBDResource,
	existingPorts ExistingPorts,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "reconcile-addresses")
	defer rf.OnEnd(&outcome)

	node, err := r.getRequiredCurrentNode(rf.Ctx())
	if err != nil {
		return rf.Fail(err)
	}

	base := drbdr.DeepCopy()
	if addrErr := ensureAddresses(rf.Ctx(), drbdr, node, existingPorts, r.portRegistry.Allocate).Error(); addrErr != nil {
		return rf.Fail(addrErr)
	}

	if !equality.Semantic.DeepEqual(base.Status.Addresses, drbdr.Status.Addresses) {
		if err := r.patchDRBDRStatus(rf.Ctx(), drbdr, base, true); err != nil {
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
	if adding && isUpAndNotInCleanup && drbdr.DeletionTimestamp == nil {
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
		if maintenanceMode {
			continue
		}
		log.Info("DRBD action", "action", action.String(), "maintenanceMode", maintenanceMode)
		if err := action.Execute(ctx); err != nil {
			return refreshNeeded, err
		}
		refreshNeeded = true
	}
	return refreshNeeded, nil
}

// ensureLocalPortConflictResolved checks if drbdErr contains a local port
// conflict from new-path and immediately allocates a replacement port for the
// conflicting IP in drbdr.Status.Addresses.
//
// Returns the original drbdErr annotated with re-allocation info on success,
// or with allocation failure details on error. Returns drbdErr unchanged when
// no local port conflict is detected.
func ensureLocalPortConflictResolved(
	ctx context.Context,
	drbdr *v1alpha1.DRBDResource,
	drbdErr error,
	portAllocator PortAllocator,
) error {
	var conflict *localPortConflictError
	if !errors.As(drbdErr, &conflict) {
		return drbdErr
	}

	for i := range drbdr.Status.Addresses {
		addr := &drbdr.Status.Addresses[i]
		if addr.Address.IPv4 != conflict.ip {
			continue
		}
		oldPort := addr.Address.Port
		newPort, allocErr := portAllocator(ctx, conflict.ip)
		if allocErr != nil {
			return fmt.Errorf("%w (port re-allocation failed: %v)", drbdErr, allocErr)
		}
		log.FromContext(ctx).Info("Re-allocated conflicting local port",
			"ip", conflict.ip, "oldPort", oldPort, "newPort", newPort)
		addr.Address.Port = newPort
		return fmt.Errorf("%w (port re-allocated: %d -> %d)", drbdErr, oldPort, newPort)
	}

	return drbdErr
}

// formatLVMDevicePath formats the path to an LVM logical volume device.
func formatLVMDevicePath(vgName, lvName string) string {
	return "/dev/" + vgName + "/" + lvName
}
