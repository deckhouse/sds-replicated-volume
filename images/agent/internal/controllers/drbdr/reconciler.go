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
	"time"

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

	// Phase 1: Rename the on-node resource to its canonical name. Maintenance
	// skips the rename; later phases still resolve the old name via
	// DRBDResourceNameOnTheNode.
	if drbdr.Spec.ActualNameOnTheNode != "" {
		if !maintenanceMode {
			outcome = r.reconcileActualNameOnTheNode(rf.Ctx(), drbdr)
			return
		}
		rf.Log().Info("Skipping ActualNameOnTheNode rename due to maintenance mode",
			"actualName", drbdr.Spec.ActualNameOnTheNode)
	}

	// Phase 2: Add our finalizer (Up, non-deleting resources only).
	if finalizerOutcome := r.reconcileFinalizer(rf.Ctx(), drbdr, true, isUpAndNotInCleanup(drbdr)); finalizerOutcome.ShouldReturn() {
		if finalizerOutcome.Error() != nil {
			return rf.Fail(finalizerOutcome.Error())
		}
		return rf.Done()
	}
	// Re-evaluate after Phase 2's patch may have refreshed metadata.
	upAndNotInCleanup := isUpAndNotInCleanup(drbdr)

	// Observe the on-node DRBD state; reused by Phases 3 and 6.
	drbdResName := DRBDResourceNameOnTheNode(drbdr)
	aState, aErr := observeActualDRBDState(rf.Ctx(), drbdResName)
	aErr = ConfiguredReasonError(aErr, v1alpha1.DRBDResourceCondConfiguredReasonStateQueryFailed)

	// Phase 3: Persist allocated addresses before any destructive DRBD action,
	// so a stale-cache race fails here on the optimistic lock rather than after
	// a kernel change.
	existingPorts := existingPortsFromState(aState)
	if addrOutcome := r.reconcileAddresses(rf.Ctx(), drbdr, existingPorts); addrOutcome.ShouldReturn() {
		if addrOutcome.Error() != nil {
			return rf.Fail(addrOutcome.Error())
		}
		return rf.Done()
	}

	// Patch base for the final status patch (Phase 8), taken after the
	// addresses patch settled the resourceVersion.
	statusBase := drbdr.DeepCopy()

	// Phase 4: Write-ahead the LLV finalizer-release list. Recording which LLV
	// we (will) hold a finalizer on BEFORE adding the finalizer (Phase 5) or
	// attaching a disk (Phase 6) is what makes the finalizer impossible to leak:
	// Phase 9 will not drop our DRBDResource finalizer while this list is
	// non-empty.
	intendedLLVFinalizersToRelease, llvListOutcome := r.reconcileLLVFinalizersToRelease(
		rf.Ctx(), drbdr, upAndNotInCleanup, maintenanceMode)
	if llvListOutcome.ShouldReturn() {
		return llvListOutcome
	}

	// Phase 5: Add our finalizer to the LLV we are about to attach (Up +
	// Diskful, outside maintenance). It is already tracked by Phase 4. The
	// currently attached disk is passed so a deleting LLV we already hold
	// attached stays the intended disk (no spurious detach); teardown is driven
	// by spec, observed here as upAndNotInCleanup=false or Type=Diskless.
	var intendedDisk string
	var llvErr error
	if !maintenanceMode && drbdr.Spec.Type == v1alpha1.DRBDResourceTypeDiskful && upAndNotInCleanup {
		intendedDisk, llvErr = r.reconcileLLVFinalizerAdd(rf.Ctx(), drbdr.Spec.LVMLogicalVolumeName, actualBackingDisk(aState))
	}

	// Phase 6: Converge the on-node DRBD state to the intended state, then
	// re-observe so the status report and finalizer decisions below reflect the
	// result of our actions.
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

	// LLV backing the disk after convergence: empty once detached, non-empty if
	// a detach failed or was skipped (maintenance). Drives both the release
	// below and the reported lvmLogicalVolumeName.
	currentDiskLLV := r.computeActualLLVName(rf.Ctx(), aState)

	// Phase 7: Release finalizers from LLVs no longer backing the disk. This is
	// a Kubernetes operation, so it is not gated on maintenance — only on the
	// actual disk state: keep the finalizer while the LLV is the spec disk we
	// intend to keep (Up) or is still attached (releasing an attached LLV would
	// orphan it). What remains in the list is what we still owe a release.
	kept := make([]string, 0, len(intendedLLVFinalizersToRelease))
	for _, llv := range intendedLLVFinalizersToRelease {
		keep := (upAndNotInCleanup && llv == drbdr.Spec.LVMLogicalVolumeName) || llv == currentDiskLLV
		if !keep {
			if releaseErr := r.releaseLLVFinalizer(rf.Ctx(), llv); releaseErr != nil {
				llvErr = errors.Join(llvErr, releaseErr)
				keep = true // release failed; retry next reconcile
			}
		}
		if keep {
			kept = append(kept, llv)
		}
	}
	intendedLLVFinalizersToRelease = kept

	// Phase 8: Report status (lvmLogicalVolumeName mirrors the actual disk) and
	// patch it. The pending-release list is written after ensureReportState,
	// which may initialize ActiveConfiguration.
	reconcileErr := errors.Join(llvErr, aErr, aErr2, drbdErr)
	if ensureOutcome := ensureReportState(rf.Ctx(), aState, drbdr, currentDiskLLV, reconcileErr, maintenanceMode); ensureOutcome.Error() != nil {
		reconcileErr = errors.Join(reconcileErr, ensureOutcome.Error())
	}
	ensureActiveConfiguration(drbdr).LLVFinalizersToRelease = intendedLLVFinalizersToRelease

	// Compute pending metric observations before patching, then observe them
	// only after the status state they describe has been committed.
	metricObservations := computeDRBDRMetricObservations(time.Now(), drbdr, statusBase)

	statusChanged := !equality.Semantic.DeepEqual(statusBase.Status, drbdr.Status)
	if statusChanged {
		statusPatchErr := r.patchDRBDRStatus(rf.Ctx(), drbdr, statusBase, true)
		// Tolerate not-found while the object is being deleted.
		if statusPatchErr != nil && (drbdr.DeletionTimestamp == nil || client.IgnoreNotFound(statusPatchErr) != nil) {
			reconcileErr = errors.Join(reconcileErr, statusPatchErr)
		}
		if statusPatchErr == nil {
			metricObservations.observe()
		}
	} else {
		metricObservations.observe()
	}

	// Phase 9: Drop our DRBDResource finalizer only once fully torn down: every
	// owed LLV finalizer released (else the LLV finalizer leaks) AND the on-node
	// resource gone (else we orphan it). Maintenance is not special-cased — a
	// pending detach/down simply keeps the actual state from reaching "torn
	// down", so we hold the finalizer until it runs. A nil aState means the
	// status query failed; reconcileErr is set and we requeue rather than assume
	// teardown.
	drbdResourceGone := !aState.IsZero() && !aState.ResourceExists()
	if len(intendedLLVFinalizersToRelease) == 0 && drbdResourceGone {
		if finalizerOutcome := r.reconcileFinalizer(rf.Ctx(), drbdr, false, upAndNotInCleanup); finalizerOutcome.ShouldReturn() {
			if finalizerOutcome.Error() != nil {
				reconcileErr = errors.Join(reconcileErr, finalizerOutcome.Error())
			}
			// If finalizer was removed during cleanup, observe deletion metrics.
			if finalizerOutcome.Error() == nil && isInCleanup(drbdr) && !obju.HasFinalizer(drbdr, v1alpha1.AgentFinalizer) {
				observeDRBDRDeletion(drbdr)
			}
		}
	} else if !upAndNotInCleanup {
		rf.Log().Info("Deferring DRBDResource finalizer removal until DRBD teardown completes",
			"pendingLLVFinalizersToRelease", intendedLLVFinalizersToRelease,
			"drbdResourceGone", drbdResourceGone)
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
//
// attachedDisk is the backing disk currently attached to DRBD (empty when none).
// It matters only for the deleting-LLV case: while we still intend to be Diskful
// with this LLV (Up + Diskful spec), a deletionTimestamp on the LLV object must
// not, by itself, trigger a detach. If the disk is already attached we keep it
// as the intended disk (so the diff engine leaves it in place) and return no
// error, so Configured stays True and the controller can proceed to command the
// real teardown via spec (Type=Diskless / State=Down / DRBDResource deletion).
// Only when nothing is attached yet do we return an error, since a deleting LLV
// cannot be attached.
func (r *Reconciler) reconcileLLVFinalizerAdd(ctx context.Context, llvName string, attachedDisk string) (diskPath string, err error) {
	if llvName == "" {
		return "", nil
	}

	llv, err := r.getRequiredLLV(ctx, llvName)
	if err != nil {
		return "", ConfiguredReasonError(err, v1alpha1.DRBDResourceCondConfiguredReasonBackingVolumeUnavailable)
	}

	// The LLV object is being deleted. We do not add our finalizer to a
	// deleting object, and we must not conflate "LLV object deleting" with
	// "backing device gone".
	if llv.DeletionTimestamp != nil {
		// Disk already attached: keep it as the intended disk (no detach) and
		// report success. Configured must stay True so the controller can drive
		// the real teardown via spec; surfacing an error here would wedge the
		// resource at Configured=False and deadlock cleanup. The finalizer we
		// added while the LLV was live is still present and is released by the
		// normal teardown path (Phase 7).
		if attachedDisk != "" {
			return attachedDisk, nil
		}
		// Nothing attached to keep and the LLV is already deleting: it cannot be
		// attached. Surface an error so we keep reconciling until teardown via
		// spec resolves it.
		return "", ConfiguredReasonError(
			fmt.Errorf("LVMLogicalVolume %q is being deleted", llvName),
			v1alpha1.DRBDResourceCondConfiguredReasonBackingVolumeDeleting,
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
	lvg, err := r.getRequiredLVG(ctx, llv.Spec.LVMVolumeGroupName)
	if err != nil {
		return "", ConfiguredReasonError(err, v1alpha1.DRBDResourceCondConfiguredReasonBackingVolumeUnavailable)
	}

	return formatLVMDevicePath(lvg.Spec.ActualVGNameOnTheNode, llv.Spec.ActualLVNameOnTheNode), nil
}

// reconcileLLVFinalizersToRelease maintains the write-ahead set of LVMLogicalVolume
// names whose agent finalizer this DRBDResource owes a release for, stored in
// status.ActiveConfiguration.LLVFinalizersToRelease. It records the LLV we are
// about to attach (and add a finalizer to in Phase 5) BEFORE doing either, so a
// crash can never strand a finalizer: Phase 9 will not drop our DRBDResource
// finalizer while this list is non-empty. Entries are removed only in Phase 7,
// after the finalizer is released.
//
// If the set grew it is persisted and the reconcile requeues, so the remaining
// phases proceed from durable state.
func (r *Reconciler) reconcileLLVFinalizersToRelease(
	ctx context.Context,
	drbdr *v1alpha1.DRBDResource,
	upAndNotInCleanup bool,
	maintenanceMode bool,
) (list []string, outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "llv-finalizers-to-release")
	defer rf.OnEnd(&outcome)

	if drbdr.Status.ActiveConfiguration != nil {
		list = slices.Clone(drbdr.Status.ActiveConfiguration.LLVFinalizersToRelease)
	}
	originalLen := len(list)

	if !maintenanceMode && upAndNotInCleanup &&
		drbdr.Spec.Type == v1alpha1.DRBDResourceTypeDiskful &&
		drbdr.Spec.LVMLogicalVolumeName != "" &&
		!slices.Contains(list, drbdr.Spec.LVMLogicalVolumeName) {
		list = append(list, drbdr.Spec.LVMLogicalVolumeName)
	}

	if len(list) == originalLen {
		return list, rf.Continue()
	}

	// Commit the grown list before any finalizer add / DRBD action, then
	// requeue so subsequent phases proceed from durable state.
	base := drbdr.DeepCopy()
	ensureActiveConfiguration(drbdr).LLVFinalizersToRelease = list
	if err := r.patchDRBDRStatus(rf.Ctx(), drbdr, base, true); err != nil {
		return list, rf.Fail(err)
	}
	return list, rf.DoneAndRequeue()
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
			// Maintenance pauses DRBD operations only: we do not execute the
			// action, but we log the one we would have run so the skipped work
			// stays observable.
			log.Info("Skipping DRBD action due to maintenance mode", "action", action.String())
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
