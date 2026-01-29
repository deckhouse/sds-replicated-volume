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
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

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

	// Get Node
	node, err := r.getCurrentNode(rf.Ctx(), drbdr.Spec.NodeName)
	if err != nil {
		return rf.Fail(err).ToCtrl()
	}

	base := drbdr.DeepCopy()

	// Phase 1: Ensure finalizer (adds if needed for up resources)
	if outcome := r.reconcileFinalizer(rf.Ctx(), drbdr, base, true); outcome.ShouldReturn() {
		return outcome.ToCtrl()
	}

	// Phase 2: Ensure addresses in status (in-memory only, no patch yet)
	// Error here is non-critical (e.g., port allocation failure) - continue reconciliation
	addrErr := ensureAddresses(rf.Ctx(), drbdr, node, r.portCache.Allocate).Error()

	// Phase 3: DRBD convergence
	var iErr, aErr, aErr2, drbdErr error

	iState, iErr := computeIntendedDRBDState(rf.Ctx(), r.cl, drbdr)
	iErr = ConfiguredReasonError(iErr, v1alpha1.DRBDResourceCondConfiguredReasonStateQueryFailed)

	aState, aErr := getActualDRBDState(rf.Ctx(), drbdr.DRBDResourceNameOnTheNode())
	aErr = ConfiguredReasonError(aErr, v1alpha1.DRBDResourceCondConfiguredReasonStateQueryFailed)

	// Compute and execute DRBD actions
	targetActions := computeTargetDRBDActions(iState, aState)
	refreshNeeded, drbdErr := r.convergeDRBDState(rf.Ctx(), targetActions)

	// Refresh actual state if DRBD state was changed
	if refreshNeeded {
		aState, aErr2 = getActualDRBDState(rf.Ctx(), drbdr.DRBDResourceNameOnTheNode())
		aErr2 = ConfiguredReasonError(aErr2, v1alpha1.DRBDResourceCondConfiguredReasonStateQueryFailed)
	}

	// Phase 4: Report and status patch
	reconcileErr := errors.Join(addrErr, iErr, aErr, aErr2, drbdErr)
	if ensureOutcome := ensureReportState(rf.Ctx(), aState, drbdr, reconcileErr); ensureOutcome.Error() != nil {
		reconcileErr = errors.Join(reconcileErr, ensureOutcome.Error())
	}

	// Patch status if changed
	if !equality.Semantic.DeepEqual(base.Status, drbdr.Status) {
		statusPatchErr := r.patchDRBDRStatus(rf.Ctx(), drbdr, base)
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

// getCurrentNode gets the Node object by name.
func (r *Reconciler) getCurrentNode(ctx context.Context, nodeName string) (*corev1.Node, error) {
	node := &corev1.Node{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: nodeName}, node); err != nil {
		return nil, flow.Wrapf(err, "getting Node %q", nodeName)
	}
	return node, nil
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
	drbdr *v1alpha1.DRBDResource,
	base *v1alpha1.DRBDResource,
) error {
	return r.cl.Status().Patch(ctx, drbdr, client.MergeFrom(base))
}

// convergeDRBDState executes DRBD actions to converge to the target state.
// Returns whether DRBD state was changed (requiring a refresh) and any error.
func (r *Reconciler) convergeDRBDState(ctx context.Context, actions DRBDActions) (refreshNeeded bool, err error) {
	for _, action := range actions {
		if err := action.Execute(ctx); err != nil {
			return refreshNeeded, err
		}
		refreshNeeded = true
	}
	return refreshNeeded, nil
}
