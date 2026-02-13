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

package drbdrop

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

// OperationReconciler reconciles DRBDResourceOperation objects (single-resource operations).
type OperationReconciler struct {
	cl       client.Client
	nodeName string
}

// NewOperationReconciler creates a new OperationReconciler.
func NewOperationReconciler(cl client.Client, nodeName string) *OperationReconciler {
	return &OperationReconciler{
		cl:       cl,
		nodeName: nodeName,
	}
}

// Reconcile reconciles a DRBDResourceOperation.
// Reconcile pattern: Pure orchestration — dispatch by operation type to reconcile* helpers.
func (r *OperationReconciler) Reconcile(
	ctx context.Context,
	req reconcile.Request,
) (reconcile.Result, error) {
	rf := flow.BeginRootReconcile(ctx)
	rf.Log().V(1).Info("Reconciling DRBDResourceOperation", "name", req.Name)

	op, err := r.getOperation(rf.Ctx(), req.NamespacedName)
	if err != nil {
		return rf.Fail(err).ToCtrl()
	}
	if op == nil {
		return rf.Done().ToCtrl()
	}

	switch op.Status.Phase {
	case v1alpha1.DRBDOperationPhaseSucceeded, v1alpha1.DRBDOperationPhaseFailed:
		rf.Log().V(1).Info("Operation in terminal state, skipping", "phase", op.Status.Phase)
		return rf.Done().ToCtrl()
	}

	switch op.Spec.Type {
	case v1alpha1.DRBDResourceOperationCreateNewUUID:
		return r.reconcileCreateNewUUID(rf.Ctx(), op).ToCtrl()
	default:
		return r.reconcileUnsupported(rf.Ctx(), op, op.Spec.Type).ToCtrl()
	}
}

// reconcileCreateNewUUID runs the CreateNewUUID operation for the DRBDResource on this node.
// Reconcile pattern: Get required objects → ensure status Running + patch → execute → ensure terminal status + patch.
func (r *OperationReconciler) reconcileCreateNewUUID(ctx context.Context, op *v1alpha1.DRBDResourceOperation) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "CreateNewUUID")
	defer rf.OnEnd(&outcome)

	drbdr, err := r.getDRBDResourceForOperation(ctx, op)
	if err != nil {
		return rf.Fail(err)
	}
	if drbdr == nil {
		return rf.Done()
	}

	base := op.DeepCopy()
	runOutcome := ensureOperationStatusRunning(ctx, op, metav1.NewTime(time.Now()))
	if runOutcome.Error() != nil {
		return rf.Fail(runOutcome.Error())
	}
	if runOutcome.DidChange() {
		if err := r.patchOperationStatus(ctx, op, base, false); err != nil {
			return rf.Fail(err)
		}
	}

	opErr := r.executeCreateNewUUID(ctx, op, drbdr)
	if opErr != nil {
		if err := r.failOperationAndPatch(ctx, op, opErr.Error()); err != nil {
			return rf.Fail(err)
		}
		return rf.Done()
	}

	if err := r.succeedOperationAndPatch(ctx, op); err != nil {
		return rf.Fail(err)
	}
	return rf.Done()
}

// reconcileUnsupported marks the operation as Failed and returns Done.
func (r *OperationReconciler) reconcileUnsupported(ctx context.Context, op *v1alpha1.DRBDResourceOperation, opType v1alpha1.DRBDResourceOperationType) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "Unsupported")
	defer rf.OnEnd(&outcome)

	if err := r.failOperationAndPatch(ctx, op, fmt.Sprintf("operation type %s not implemented", opType)); err != nil {
		return rf.Fail(err)
	}
	return rf.Done()
}

// getOperation fetches the DRBDResourceOperation by key. Returns (nil, nil) if not found.
func (r *OperationReconciler) getOperation(ctx context.Context, key client.ObjectKey) (*v1alpha1.DRBDResourceOperation, error) {
	op := &v1alpha1.DRBDResourceOperation{}
	if err := r.cl.Get(ctx, key, op); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return nil, nil
		}
		return nil, err
	}
	return op, nil
}

// getDRBDResourceForOperation finds the target DRBDResource and validates it belongs to this node.
// Returns (nil, nil) if the resource exists but belongs to a different node. Returns (nil, err) if not found or get failed.
func (r *OperationReconciler) getDRBDResourceForOperation(
	ctx context.Context,
	op *v1alpha1.DRBDResourceOperation,
) (*v1alpha1.DRBDResource, error) {
	drbdr, err := r.getDRBDR(ctx, op.Spec.DRBDResourceName)
	if err != nil {
		return nil, err
	}
	if drbdr == nil {
		return nil, fmt.Errorf("DRBDResource not found")
	}
	if drbdr.Spec.NodeName != r.nodeName {
		return nil, nil
	}
	return drbdr, nil
}

// getDRBDR fetches a DRBDResource by name. Returns (nil, nil) if not found. Single Get only.
func (r *OperationReconciler) getDRBDR(ctx context.Context, name string) (*v1alpha1.DRBDResource, error) {
	drbdr := &v1alpha1.DRBDResource{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: name}, drbdr); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return nil, nil
		}
		return nil, err
	}
	return drbdr, nil
}

// ensureOperationStatusRunning sets Phase=Running, StartedAt, Message="". Caller passes startedAt (no I/O in ensure).
func ensureOperationStatusRunning(ctx context.Context, op *v1alpha1.DRBDResourceOperation, startedAt metav1.Time) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "OperationStatusRunning")
	defer ef.OnEnd(&outcome)
	changed := op.Status.Phase != v1alpha1.DRBDOperationPhaseRunning
	op.Status.Phase = v1alpha1.DRBDOperationPhaseRunning
	op.Status.StartedAt = &startedAt
	op.Status.Message = ""
	return ef.Ok().ReportChangedIf(changed)
}

// ensureOperationStatusSucceeded sets Phase=Succeeded, CompletedAt, Message="". Caller passes completedAt.
func ensureOperationStatusSucceeded(ctx context.Context, op *v1alpha1.DRBDResourceOperation, completedAt metav1.Time) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "OperationStatusSucceeded")
	defer ef.OnEnd(&outcome)
	changed := op.Status.Phase != v1alpha1.DRBDOperationPhaseSucceeded
	op.Status.Phase = v1alpha1.DRBDOperationPhaseSucceeded
	op.Status.CompletedAt = &completedAt
	op.Status.Message = ""
	return ef.Ok().ReportChangedIf(changed)
}

// ensureOperationStatusFailed sets Phase=Failed, CompletedAt, Message. Caller passes completedAt.
func ensureOperationStatusFailed(ctx context.Context, op *v1alpha1.DRBDResourceOperation, message string, completedAt metav1.Time) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "OperationStatusFailed")
	defer ef.OnEnd(&outcome)
	changed := op.Status.Phase != v1alpha1.DRBDOperationPhaseFailed || op.Status.Message != message
	op.Status.Phase = v1alpha1.DRBDOperationPhaseFailed
	op.Status.CompletedAt = &completedAt
	op.Status.Message = message
	return ef.Ok().ReportChangedIf(changed)
}

// failOperationAndPatch sets operation status to Failed with message and patches. Returns ensure error or patch error.
func (r *OperationReconciler) failOperationAndPatch(ctx context.Context, op *v1alpha1.DRBDResourceOperation, message string) error {
	base := op.DeepCopy()
	outcome := ensureOperationStatusFailed(ctx, op, message, metav1.NewTime(time.Now()))
	if outcome.Error() != nil {
		return outcome.Error()
	}
	return r.patchOperationStatus(ctx, op, base, false)
}

// succeedOperationAndPatch sets operation status to Succeeded and patches. Returns ensure error or patch error.
func (r *OperationReconciler) succeedOperationAndPatch(ctx context.Context, op *v1alpha1.DRBDResourceOperation) error {
	base := op.DeepCopy()
	outcome := ensureOperationStatusSucceeded(ctx, op, metav1.NewTime(time.Now()))
	if outcome.Error() != nil {
		return outcome.Error()
	}
	return r.patchOperationStatus(ctx, op, base, false)
}

// patchOperationStatus patches the operation status subresource.
func (r *OperationReconciler) patchOperationStatus(
	ctx context.Context,
	obj, base *v1alpha1.DRBDResourceOperation,
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
