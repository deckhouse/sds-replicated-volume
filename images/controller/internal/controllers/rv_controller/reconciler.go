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
		return flow.Failf(err, "getting device minor idpool").ToCtrl()
	}

	// Get the ReplicatedVolume
	rv := &v1alpha1.ReplicatedVolume{}
	if err := r.cl.Get(ctx, req.NamespacedName, rv); err != nil {
		if client.IgnoreNotFound(err) == nil {
			// Release device minor from pool only when object is NotFound.
			pool.Release(req.Name)
			return flow.Done().ToCtrl()
		}
		return flow.Failf(err, "getting ReplicatedVolume").ToCtrl()
	}

	outcome := r.reconcileMain(ctx, rv)
	if outcome.ShouldReturn() {
		return outcome.ToCtrl()
	}

	outcome = r.reconcileStatus(ctx, rv, pool)
	if outcome.ShouldReturn() {
		return outcome.ToCtrl()
	}

	return outcome.ToCtrl()
}

// Reconcile pattern: Conditional desired evaluation
func (r *Reconciler) reconcileMain(ctx context.Context, rv *v1alpha1.ReplicatedVolume) (outcome flow.Outcome) {
	ctx, _ = flow.BeginPhase(ctx, "main")
	defer flow.EndPhase(ctx, &outcome)

	expectedRSC := rv.Spec.ReplicatedStorageClassName
	if expectedRSC == "" {
		if !obju.HasLabel(rv, v1alpha1.ReplicatedStorageClassLabelKey) {
			return flow.Continue()
		}
	} else {
		if obju.HasLabelValue(rv, v1alpha1.ReplicatedStorageClassLabelKey, expectedRSC) {
			return flow.Continue()
		}
	}

	base := rv.DeepCopy()

	if expectedRSC != "" {
		_ = obju.SetLabel(rv, v1alpha1.ReplicatedStorageClassLabelKey, expectedRSC)
	} else {
		_ = obju.RemoveLabel(rv, v1alpha1.ReplicatedStorageClassLabelKey)
	}

	outcome = r.patchRV(ctx, rv, base, false)
	if outcome.Error() != nil {
		if client.IgnoreNotFound(outcome.Error()) == nil {
			return flow.Continue()
		}
		return outcome.Enrichf("patching ReplicatedVolume main")
	}

	return flow.Continue()
}

// Reconcile pattern: Desired-state driven
func (r *Reconciler) reconcileStatus(ctx context.Context, rv *v1alpha1.ReplicatedVolume, pool *idpool.IDPool[v1alpha1.DeviceMinor]) (outcome flow.Outcome) {
	ctx, _ = flow.BeginPhase(ctx, "status")
	defer flow.EndPhase(ctx, &outcome)

	desiredDeviceMinor, desiredDeviceMinorComputeErr := computeDesiredDeviceMinor(rv, pool)
	desiredDeviceMinorAssignedCondition := computeDesiredDeviceMinorAssignedCondition(desiredDeviceMinorComputeErr, rv.Generation)

	if isStatusDeviceMinorUpToDate(rv, desiredDeviceMinor, desiredDeviceMinorAssignedCondition) {
		if desiredDeviceMinorComputeErr != nil {
			return flow.Fail(desiredDeviceMinorComputeErr)
		}
		return flow.Continue()
	}

	base := rv.DeepCopy()

	applyStatusDeviceMinor(rv, desiredDeviceMinor, desiredDeviceMinorAssignedCondition)

	outcome = r.patchRVStatus(ctx, rv, base, true)
	if outcome.Error() != nil {
		if client.IgnoreNotFound(outcome.Error()) == nil {
			// RV disappeared between Get and Status().Patch: release any reserved ID.
			pool.Release(rv.Name)
			if desiredDeviceMinorComputeErr != nil {
				return flow.Fail(desiredDeviceMinorComputeErr)
			}
			return flow.Continue()
		}

		// Preserve compute error visibility alongside patch errors.
		return flow.Fail(
			errors.Join(outcome.Error(), desiredDeviceMinorComputeErr),
		).Enrichf("patching ReplicatedVolume status")
	}

	// Release the device minor back to the pool if it wasn't assigned.
	// Safe to do here because the status has already been successfully patched in the Kubernetes API.
	if !rv.Status.HasDeviceMinor() {
		pool.Release(rv.Name)
	}

	if desiredDeviceMinorComputeErr != nil {
		return flow.Fail(desiredDeviceMinorComputeErr)
	}
	return flow.Continue()
}

// computeDesiredDeviceMinor computes the desired value for rv.status.deviceMinor.
//
// Note: this helper mutates the in-memory ID pool (a deterministic, reconciler-owned state) by
// reserving the ID for this RV when possible.
func computeDesiredDeviceMinor(rv *v1alpha1.ReplicatedVolume, pool *idpool.IDPool[v1alpha1.DeviceMinor]) (*v1alpha1.DeviceMinor, error) {
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

func computeDesiredDeviceMinorAssignedCondition(err error, observedGeneration int64) metav1.Condition {
	cond := metav1.Condition{
		Type:               v1alpha1.ReplicatedVolumeCondDeviceMinorAssignedType,
		ObservedGeneration: observedGeneration,
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

func isStatusDeviceMinorUpToDate(
	rv *v1alpha1.ReplicatedVolume,
	desiredDeviceMinor *v1alpha1.DeviceMinor,
	desiredDeviceMinorAssignedCondition metav1.Condition,
) bool {
	return rv.Status.DeviceMinorEquals(desiredDeviceMinor) &&
		obju.IsStatusConditionPresentAndSemanticallyEqual(rv, desiredDeviceMinorAssignedCondition)
}

func applyStatusDeviceMinor(
	rv *v1alpha1.ReplicatedVolume,
	desiredDeviceMinor *v1alpha1.DeviceMinor,
	desiredDeviceMinorAssignedCondition metav1.Condition,
) {
	rv.Status.SetDeviceMinorPtr(desiredDeviceMinor)
	_ = obju.SetStatusCondition(rv, desiredDeviceMinorAssignedCondition)
}

func (r *Reconciler) patchRV(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	base *v1alpha1.ReplicatedVolume,
	optimisticLock bool,
) flow.Outcome {
	if optimisticLock {
		if err := r.cl.Patch(ctx, rv, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil {
			return flow.Fail(err)
		}
		return flow.Continue()
	}

	if err := r.cl.Patch(ctx, rv, client.MergeFrom(base)); err != nil {
		return flow.Fail(err)
	}
	return flow.Continue()
}

func (r *Reconciler) patchRVStatus(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	base *v1alpha1.ReplicatedVolume,
	optimisticLock bool,
) flow.Outcome {
	if optimisticLock {
		if err := r.cl.Status().Patch(ctx, rv, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil {
			return flow.Fail(err)
		}
		return flow.Continue()
	}

	if err := r.cl.Status().Patch(ctx, rv, client.MergeFrom(base)); err != nil {
		return flow.Fail(err)
	}
	return flow.Continue()
}
