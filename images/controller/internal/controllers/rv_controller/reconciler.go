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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
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

// Reconcile pattern: Orchestration
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	ctx, _ = flow.Begin(ctx)

	// Get the ReplicatedVolume
	rv := &v1alpha1.ReplicatedVolume{}
	if err := r.cl.Get(ctx, req.NamespacedName, rv); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return flow.Failf(err, "getting ReplicatedVolume").ToCtrl()
		}
	}

	// Reconcile main
	outcome := r.reconcileMain(ctx, rv)
	if outcome.ShouldReturn() {
		return outcome.ToCtrl()
	}

	// Reconcile status subresource
	outcome = r.reconcileStatus(ctx, req.Name, rv)
	if outcome.ShouldReturn() {
		return outcome.ToCtrl()
	}

	return flow.Done().ToCtrl()
}

// Reconcile pattern: Conditional desired evaluation
func (r *Reconciler) reconcileMain(ctx context.Context, rv *v1alpha1.ReplicatedVolume) (outcome flow.Outcome) {
	ctx, _ = flow.BeginPhase(ctx, "main")
	defer flow.EndPhase(ctx, &outcome)

	if rv == nil {
		return flow.Continue()
	}

	if obju.HasLabelValue(rv, v1alpha1.ReplicatedStorageClassLabelKey, rv.Spec.ReplicatedStorageClassName) {
		return flow.Continue()
	}

	base := rv.DeepCopy()

	obju.SetLabel(rv, v1alpha1.ReplicatedStorageClassLabelKey, rv.Spec.ReplicatedStorageClassName)

	if err := r.cl.Patch(ctx, rv, client.MergeFrom(base)); err != nil {
		return flow.Fail(err).Enrichf("patching ReplicatedVolume")
	}

	return flow.Continue()
}

// Reconcile pattern: Desired-state driven
func (r *Reconciler) reconcileStatus(ctx context.Context, rvName string, rv *v1alpha1.ReplicatedVolume) (outcome flow.Outcome) {
	ctx, _ = flow.BeginPhase(ctx, "status")
	defer flow.EndPhase(ctx, &outcome)

	// Allocate device minor and compute target condition
	outcome, targetDM, targetDMCond := r.allocateDM(ctx, rv, rvName)
	if rv == nil {
		return outcome
	}

	// If status is in sync, return
	if isDMInSync(rv, targetDM, targetDMCond) {
		return outcome
	}

	base := rv.DeepCopy()

	// Apply target values to status
	applyDM(rv, targetDM, targetDMCond)

	// Patch status with optimistic lock
	if err := r.cl.Status().Patch(ctx, rv, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil {
		return outcome.Merge(
			flow.Fail(err).Enrichf("patching ReplicatedVolume"),
		)
	}

	return outcome
}

func isDMInSync(rv *v1alpha1.ReplicatedVolume, targetdDM *v1alpha1.DeviceMinor, targetDMCond metav1.Condition) bool {
	return ptr.Equal(rv.Status.DeviceMinor, targetdDM) &&
		obju.IsStatusConditionPresentAndSemanticallyEqual(rv, targetDMCond)
}

func applyDM(rv *v1alpha1.ReplicatedVolume, targetdDM *v1alpha1.DeviceMinor, targetDMCond metav1.Condition) {
	rv.Status.DeviceMinor = targetdDM
	obju.SetStatusCondition(rv, targetDMCond)
}

func (r *Reconciler) allocateDM(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rvName string,
) (outcome flow.Outcome, targetDM *v1alpha1.DeviceMinor, targetDMCond metav1.Condition) {
	ctx, log := flow.BeginPhase(ctx, "deviceMinor")
	defer flow.EndPhase(ctx, &outcome)

	// Wait for pool to be ready (blocks until initialized after leader election).
	pool, err := r.deviceMinorPoolSource.DeviceMinorPool(ctx)
	if err != nil {
		return flow.Failf(err, "getting device minor idpool"), nil, metav1.Condition{}
	}

	if rv == nil {
		// Release device minor from pool only when object is NotFound.
		log.Info("ReplicatedVolume deleted, releasing device minor from pool")
		pool.Release(rvName)

		return flow.Continue(), nil, metav1.Condition{}
	}

	// Allocate device minor and compute condition
	targetDM, dmErr := pool.EnsureAllocated(rv.Name, rv.Status.DeviceMinor)
	targetDMCond = newRVDeviceMinorAssignedCondition(dmErr)

	// If there is an error, the phase should fail, but only after patching status.
	if dmErr != nil {
		if idpool.IsOutOfRange(dmErr) {
			// Device minor is invalid, it's safe to return nil (which will unset status.deviceMinor in RV) because
			// even if RV has replicas with this device minor, they will fail to start.
			targetDM = nil
		} else {
			// IMPORTANT: on pool allocation and pool validation errors we do NOT change rv.Status.DeviceMinor.
			// If it was previously assigned, it must remain as-is to avoid creating conflicts.
			// We assume resolving such conflicts is the user's responsibility.
			targetDM = rv.Status.DeviceMinor
		}

		return flow.Fail(dmErr).Enrichf("allocating device minor"), targetDM, targetDMCond
	}

	return flow.Continue(), targetDM, targetDMCond
}

// newRVDeviceMinorAssignedCondition computes the condition value for
// ReplicatedVolumeCondDeviceMinorAssignedType based on the allocation/validation error (if any).
//
// - If err is nil: Status=True, Reason=Assigned.
// - If err is a DuplicateIDError: Status=False, Reason=Duplicate, Message=err.Error().
// - Otherwise: Status=False, Reason=AssignmentFailed, Message=err.Error().
func newRVDeviceMinorAssignedCondition(err error) metav1.Condition {
	cond := metav1.Condition{
		Type: v1alpha1.ReplicatedVolumeCondDeviceMinorAssignedType,
	}

	if err != nil {
		cond.Status = metav1.ConditionFalse
		if idpool.IsDuplicateID(err) {
			cond.Reason = v1alpha1.ReplicatedVolumeCondDeviceMinorAssignedReasonDuplicate
		} else {
			cond.Reason = v1alpha1.ReplicatedVolumeCondDeviceMinorAssignedReasonAssignmentFailed
		}
		cond.Message = err.Error()

		return cond
	}

	cond.Status = metav1.ConditionTrue
	cond.Reason = v1alpha1.ReplicatedVolumeCondDeviceMinorAssignedReasonAssigned
	return cond
}
