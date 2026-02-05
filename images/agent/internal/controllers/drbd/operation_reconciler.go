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
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

// OperationReconciler reconciles DRBDResourceOperation objects.
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
func (r *OperationReconciler) Reconcile(
	ctx context.Context,
	req reconcile.Request,
) (reconcile.Result, error) {
	rf := flow.BeginRootReconcile(ctx)
	rf.Log().V(1).Info("Reconciling DRBDResourceOperation", "name", req.Name)

	// Get DRBDResourceOperation
	op := &v1alpha1.DRBDResourceOperation{}
	if err := r.cl.Get(rf.Ctx(), req.NamespacedName, op); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return rf.Done().ToCtrl()
		}
		return rf.Fail(err).ToCtrl()
	}

	// Skip terminal states
	if op.Status != nil {
		switch op.Status.Phase {
		case v1alpha1.DRBDOperationPhaseSucceeded, v1alpha1.DRBDOperationPhaseFailed:
			rf.Log().V(1).Info("Operation in terminal state, skipping", "phase", op.Status.Phase)
			return rf.Done().ToCtrl()
		}
	}

	// Find target DRBDResource
	drbdr, err := r.getDRBDResourceForOperation(rf.Ctx(), op)
	if err != nil {
		return r.failOperation(rf.Ctx(), op, err)
	}
	if drbdr == nil {
		// Not on this node or not found
		return rf.Done().ToCtrl()
	}

	// Initialize status if needed
	if op.Status == nil {
		op.Status = &v1alpha1.DRBDResourceOperationStatus{}
	}

	// Set Running phase if not already
	if op.Status.Phase != v1alpha1.DRBDOperationPhaseRunning {
		if err := r.setRunningPhase(rf.Ctx(), op); err != nil {
			return rf.Fail(err).ToCtrl()
		}
	}

	// Dispatch operation
	var opErr error
	switch op.Spec.Type {
	case v1alpha1.DRBDResourceOperationCreateNewUUID:
		opErr = r.executeCreateNewUUID(rf.Ctx(), op, drbdr)
	case v1alpha1.DRBDResourceOperationForcePrimary:
		opErr = fmt.Errorf("'ForcePrimary' operation not implemented")
	case v1alpha1.DRBDResourceOperationInvalidate:
		opErr = fmt.Errorf("'Invalidate' operation not implemented")
	case v1alpha1.DRBDResourceOperationOutdate:
		opErr = fmt.Errorf("'Outdate' operation not implemented")
	case v1alpha1.DRBDResourceOperationVerify:
		opErr = fmt.Errorf("'Verify' operation not implemented")
	case v1alpha1.DRBDResourceOperationCreateSnapshot:
		opErr = fmt.Errorf("'CreateSnapshot' operation not implemented")
	default:
		opErr = fmt.Errorf("unknown operation type: %s", op.Spec.Type)
	}

	if opErr != nil {
		return r.failOperation(rf.Ctx(), op, opErr)
	}

	// Success
	return r.succeedOperation(rf.Ctx(), op)
}

// getDRBDResourceForOperation finds the target DRBDResource and validates it belongs to this node.
// Returns (nil, nil) if the resource exists but belongs to a different node.
func (r *OperationReconciler) getDRBDResourceForOperation(
	ctx context.Context,
	op *v1alpha1.DRBDResourceOperation,
) (*v1alpha1.DRBDResource, error) {
	drbdr := &v1alpha1.DRBDResource{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: op.Spec.DRBDResourceName}, drbdr); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return nil, fmt.Errorf("DRBDResource %q not found", op.Spec.DRBDResourceName)
		}
		return nil, flow.Wrapf(err, "getting DRBDResource %q", op.Spec.DRBDResourceName)
	}

	// Check if resource belongs to this node
	if drbdr.Spec.NodeName != r.nodeName {
		return nil, nil
	}

	return drbdr, nil
}

// setRunningPhase updates the operation status to Running.
func (r *OperationReconciler) setRunningPhase(ctx context.Context, op *v1alpha1.DRBDResourceOperation) error {
	base := op.DeepCopy()
	now := metav1.NewTime(time.Now())
	op.Status.Phase = v1alpha1.DRBDOperationPhaseRunning
	op.Status.StartedAt = &now
	op.Status.Message = ""
	return r.cl.Status().Patch(ctx, op, client.MergeFrom(base))
}

// failOperation updates the operation status to Failed.
func (r *OperationReconciler) failOperation(
	ctx context.Context,
	op *v1alpha1.DRBDResourceOperation,
	opErr error,
) (reconcile.Result, error) {
	base := op.DeepCopy()
	now := metav1.NewTime(time.Now())
	if op.Status == nil {
		op.Status = &v1alpha1.DRBDResourceOperationStatus{}
	}
	op.Status.Phase = v1alpha1.DRBDOperationPhaseFailed
	op.Status.CompletedAt = &now
	op.Status.Message = opErr.Error()

	if err := r.cl.Status().Patch(ctx, op, client.MergeFrom(base)); err != nil {
		return reconcile.Result{}, fmt.Errorf("patching status to Failed: %w", err)
	}
	return reconcile.Result{}, nil
}

// succeedOperation updates the operation status to Succeeded.
func (r *OperationReconciler) succeedOperation(
	ctx context.Context,
	op *v1alpha1.DRBDResourceOperation,
) (reconcile.Result, error) {
	base := op.DeepCopy()
	now := metav1.NewTime(time.Now())
	op.Status.Phase = v1alpha1.DRBDOperationPhaseSucceeded
	op.Status.CompletedAt = &now
	op.Status.Message = ""

	if err := r.cl.Status().Patch(ctx, op, client.MergeFrom(base)); err != nil {
		return reconcile.Result{}, fmt.Errorf("patching status to Succeeded: %w", err)
	}
	return reconcile.Result{}, nil
}
