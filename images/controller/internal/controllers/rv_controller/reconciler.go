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

package rvcontroller

import (
	"context"
	"errors"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/idpool"
	"github.com/deckhouse/sds-replicated-volume/internal/reconciliation/flow"
)

type Reconciler struct {
	cl                    client.Client
	deviceMinorPoolSource DeviceMinorPoolSource
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

func NewReconciler(cl client.Client, poolSource DeviceMinorPoolSource) *Reconciler {
	return &Reconciler{cl: cl, deviceMinorPoolSource: poolSource}
}

// Reconcile pattern: In-place reconciliation
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	ctx, _ = flow.Begin(ctx)

	// Wait for pool to be ready (blocks until initialized after leader election).
	pool, err := r.deviceMinorPoolSource.DeviceMinorPool(ctx)
	if err != nil {
		return flow.Failf(err, "failed to get device minor idpool").ToCtrl()
	}

	// Get the ReplicatedVolume
	rv := &v1alpha1.ReplicatedVolume{}
	if err := r.cl.Get(ctx, req.NamespacedName, rv); err != nil {
		if client.IgnoreNotFound(err) == nil {
			// Release device minor from pool only when object is NotFound.
			pool.Release(req.Name)
			return flow.Done().ToCtrl()
		}
		return flow.Failf(err, "failed to get ReplicatedVolume %s", req.Name).ToCtrl()
	}

	out := flow.Merge(
		r.reconcileMain(ctx, rv),
		r.reconcileStatus(ctx, rv, pool),
	)

	return out.ToCtrl()
}

func (r *Reconciler) reconcileMain(ctx context.Context, rv *v1alpha1.ReplicatedVolume) flow.Outcome {
	ctx, _ = flow.BeginPhase(ctx, "main", "replicatedVolume", rv.Name)

	if rv.IsStorageClassLabelInSync() {
		return flow.Continue()
	}

	base := rv.DeepCopy()

	rv.EnsureStorageClassLabel()

	if err := r.cl.Patch(ctx, rv, client.MergeFrom(base)); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return flow.Continue()
		}
		return flow.ContinueErrf(err, "failed to patch ReplicatedVolume %s main resource", rv.Name)
	}

	return flow.Continue()
}

func (r *Reconciler) reconcileStatus(ctx context.Context, rv *v1alpha1.ReplicatedVolume, pool *idpool.IDPool[v1alpha1.DeviceMinor]) flow.Outcome {
	ctx, _ = flow.BeginPhase(ctx, "status", "replicatedVolume", rv.Name)

	desiredDeviceMinor, desiredDeviceMinorComputeErr := computeDeviceMinor(rv, pool)
	desiredDeviceMinorAssignedCondition := computeDeviceMinorAssignedCondition(desiredDeviceMinorComputeErr)
	desiredDeviceMinorAssignedCondition.ObservedGeneration = rv.Generation

	if rv.Status.DeviceMinorEquals(desiredDeviceMinor) && obju.IsStatusConditionPresentAndSemanticallyEqual(rv, desiredDeviceMinorAssignedCondition) {
		return flow.ContinueErr(desiredDeviceMinorComputeErr)
	}

	base := rv.DeepCopy()

	rv.Status.SetDeviceMinorPtr(desiredDeviceMinor)
	_ = obju.SetStatusCondition(rv, desiredDeviceMinorAssignedCondition)

	if err := r.cl.Status().Patch(ctx, rv, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil {
		if client.IgnoreNotFound(err) == nil {
			// RV disappeared between Get and Status().Patch: release any reserved ID.
			pool.Release(rv.Name)
			return flow.ContinueErr(desiredDeviceMinorComputeErr)
		}
		return flow.ContinueErr(errors.Join(
			flow.Wrapf(err, "failed to patch ReplicatedVolume %s status subresource", rv.Name),
			desiredDeviceMinorComputeErr,
		))
	}

	// Release the device minor back to the pool if it wasn't assigned.
	// Safe to do here because the status has already been successfully patched in the Kubernetes API.
	if !rv.Status.HasDeviceMinor() {
		pool.Release(rv.Name)
	}

	// if !original.Status.DeviceMinorEquals(rv.Status.DeviceMinor) {
	//	// TODO: log INFO about
	// }

	return flow.ContinueErr(desiredDeviceMinorComputeErr)
}

func computeDeviceMinor(rv *v1alpha1.ReplicatedVolume, pool *idpool.IDPool[v1alpha1.DeviceMinor]) (*v1alpha1.DeviceMinor, error) {
	dm, has := rv.Status.GetDeviceMinor()

	// Assign a new device minor
	if !has {
		dm, err := pool.GetOrCreate(rv.Name)
		if err != nil {
			// Failed to assign a new device minor, return nil
			return nil, err
		}

		// Successfully assigned a new device minor, return it
		return &dm, nil
	}

	// Validate previously assigned device minor
	if err := dm.Validate(); err != nil {
		// Device minor is invalid, it's safe to return nil (which will unset status.deviceMinor in RV) because
		// even if RV has replicas with this device minor, they will fail to start.
		return nil, err
	}

	// Check if the device minor belongs to our RV
	if err := pool.GetOrCreateWithID(rv.Name, dm); err != nil {
		return &dm, err
	}

	// Successfully assigned the device minor, return it
	return &dm, nil
}

func computeDeviceMinorAssignedCondition(err error) metav1.Condition {
	cond := metav1.Condition{
		Type: v1alpha1.ReplicatedVolumeCondDeviceMinorAssignedType,
	}

	if err == nil {
		cond.Status = metav1.ConditionTrue
		cond.Reason = v1alpha1.ReplicatedVolumeCondDeviceMinorAssignedReasonAssigned
		return cond
	}

	cond.Status = metav1.ConditionFalse
	if idpool.IsDuplicateID(err) {
		cond.Reason = v1alpha1.ReplicatedVolumeCondDeviceMinorAssignedReasonDuplicate
	} else {
		cond.Reason = v1alpha1.ReplicatedVolumeCondDeviceMinorAssignedReasonAssignmentFailed
	}
	cond.Message = err.Error()

	return cond
}
