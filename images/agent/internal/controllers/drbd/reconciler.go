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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
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
		return rf.Done().ToCtrl()
	}

	// Get Node (required)
	node, err := r.getRequiredCurrentNode(rf.Ctx())
	if err != nil {
		return rf.Fail(err).ToCtrl()
	}

	base := drbdr.DeepCopy()

	// Check maintenance mode
	maintenanceMode := drbdr.Spec.Maintenance == v1alpha1.MaintenanceModeNoResourceReconciliation

	// Phase 1: Ensure finalizer (adds if needed for up resources)
	if outcome := r.reconcileFinalizer(rf.Ctx(), drbdr, base, true); outcome.ShouldReturn() {
		return outcome.ToCtrl()
	}

	// Phase 2: Ensure addresses in status (in-memory only, no patch yet)
	// Error here is non-critical (e.g., port allocation failure) - continue reconciliation
	addrErr := ensureAddresses(rf.Ctx(), drbdr, node, r.portCache.Allocate).Error()

	// Phase 3: DRBD convergence
	var llvErr, lvgErr, aErr, aErr2, drbdErr error

	// Get backing disk path for diskful resources
	var backingDisk string
	if drbdr.Spec.Type == v1alpha1.DRBDResourceTypeDiskful {
		var llv *snc.LVMLogicalVolume
		llv, llvErr = r.getLVMLogicalVolume(rf.Ctx(), drbdr.Spec.LVMLogicalVolumeName)
		llvErr = ConfiguredReasonError(llvErr, v1alpha1.DRBDResourceCondConfiguredReasonStateQueryFailed)

		if llv != nil {
			var lvg *snc.LVMVolumeGroup
			lvg, lvgErr = r.getLVMVolumeGroup(rf.Ctx(), llv.Spec.LVMVolumeGroupName)
			lvgErr = ConfiguredReasonError(lvgErr, v1alpha1.DRBDResourceCondConfiguredReasonStateQueryFailed)

			if lvg != nil {
				backingDisk = v1alpha1.SprintDRBDDisk(lvg.Spec.ActualVGNameOnTheNode, llv.Spec.ActualLVNameOnTheNode)
			}
		}
	}

	iState := computeIntendedDRBDState(drbdr, backingDisk)

	aState, aErr := observeActualDRBDState(rf.Ctx(), DRBDResourceNameOnTheNode(drbdr))
	aErr = ConfiguredReasonError(aErr, v1alpha1.DRBDResourceCondConfiguredReasonStateQueryFailed)

	// Compute and execute DRBD actions
	targetActions := computeTargetDRBDActions(iState, aState)
	refreshNeeded, drbdErr := convergeDRBDState(rf.Ctx(), targetActions, maintenanceMode)

	// Refresh actual state if DRBD state was changed
	if refreshNeeded {
		aState, aErr2 = observeActualDRBDState(rf.Ctx(), DRBDResourceNameOnTheNode(drbdr))
		aErr2 = ConfiguredReasonError(aErr2, v1alpha1.DRBDResourceCondConfiguredReasonStateQueryFailed)
	}

	// Phase 4: Report and status patch
	reconcileErr := errors.Join(addrErr, llvErr, lvgErr, aErr, aErr2, drbdErr)
	if ensureOutcome := ensureReportState(rf.Ctx(), aState, drbdr, reconcileErr, maintenanceMode); ensureOutcome.Error() != nil {
		reconcileErr = errors.Join(reconcileErr, ensureOutcome.Error())
	}

	// Patch status if changed
	if !equality.Semantic.DeepEqual(base.Status, drbdr.Status) {
		statusPatchErr := r.patchDRBDRStatus(rf.Ctx(), drbdr, base, false)
		// Ignore "not found" error if object was being deleted
		if statusPatchErr != nil && !(drbdr.DeletionTimestamp != nil && client.IgnoreNotFound(statusPatchErr) == nil) {
			reconcileErr = errors.Join(reconcileErr, statusPatchErr)
		}
		base = drbdr.DeepCopy()
	}

	// Phase 5: Finalize (removes finalizer if needed for down/deleted resources)
	if finalizerOutcome := r.reconcileFinalizer(rf.Ctx(), drbdr, base, false); finalizerOutcome.ShouldReturn() {
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

// reconcileFinalizer adds or removes the agent finalizer based on resource state.
// When adding=true, it adds the finalizer if the resource is up and not in cleanup.
// When adding=false, it removes the finalizer if the resource is down or being deleted.
func (r *Reconciler) reconcileFinalizer(
	ctx context.Context,
	drbdr *v1alpha1.DRBDResource,
	base *v1alpha1.DRBDResource,
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
