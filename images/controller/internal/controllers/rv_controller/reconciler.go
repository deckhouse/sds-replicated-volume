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
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

type Reconciler struct {
	cl                    client.Client
	deviceMinorPoolSource DeviceMinorPoolSource
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

func NewReconciler(cl client.Client, poolSource DeviceMinorPoolSource) *Reconciler {
	return &Reconciler{cl: cl, deviceMinorPoolSource: poolSource}
}

// Reconcile pattern: Pure orchestration
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	rf := flow.BeginRootReconcile(ctx)

	// Get the ReplicatedVolume
	rv := &v1alpha1.ReplicatedVolume{}
	if err := r.cl.Get(rf.Ctx(), req.NamespacedName, rv); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return rf.Failf(err, "getting ReplicatedVolume").ToCtrl()
		}

		// NotFound: treat object as deleted so that reconciliation can run cleanup (e.g. release device minor).
		rv = nil
	}

	// Reconcile main resource
	outcome := r.reconcileMain(rf.Ctx(), rv)
	if outcome.ShouldReturn() {
		return outcome.ToCtrl()
	}

	// Reconcile status subresource
	outcome = r.reconcileStatus(rf.Ctx(), req.Name, rv)
	if outcome.ShouldReturn() {
		return outcome.ToCtrl()
	}

	return rf.Done().ToCtrl()
}

// Reconcile pattern: Conditional desired evaluation
func (r *Reconciler) reconcileMain(ctx context.Context, rv *v1alpha1.ReplicatedVolume) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "main")
	defer rf.OnEnd(&outcome)

	if rv == nil {
		return rf.Continue()
	}

	if obju.HasLabelValue(rv, v1alpha1.ReplicatedStorageClassLabelKey, rv.Spec.ReplicatedStorageClassName) {
		return rf.Continue()
	}

	base := rv.DeepCopy()

	obju.SetLabel(rv, v1alpha1.ReplicatedStorageClassLabelKey, rv.Spec.ReplicatedStorageClassName)

	if err := r.cl.Patch(rf.Ctx(), rv, client.MergeFrom(base)); err != nil {
		return rf.Fail(err).Enrichf("patching ReplicatedVolume")
	}

	return rf.Continue()
}

// Reconcile pattern: Target-state driven
func (r *Reconciler) reconcileStatus(ctx context.Context, rvName string, rv *v1alpha1.ReplicatedVolume) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "status")
	defer rf.OnEnd(&outcome)

	// Allocate device minor and compute target condition.
	//
	// Best-effort: we intentionally skip outcome.ShouldReturn() check here because we want to
	// persist the error condition to status even when allocation fails. The error is still
	// propagated via outcome after the patch (or returned as-is if already in sync).
	outcome, targetDM, targetDMCond := r.allocateDM(rf.Ctx(), rv, rvName)
	if rv == nil {
		return outcome
	}

	// If status is in sync, return (preserving any error from allocateDM)
	if isDMInSync(rv, targetDM, targetDMCond) {
		return outcome
	}

	base := rv.DeepCopy()

	// Apply target values to status
	applyDM(rv, targetDM, targetDMCond)

	// Patch status with optimistic lock
	if err := r.cl.Status().Patch(rf.Ctx(), rv, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil {
		return rf.Merge(
			outcome,
			rf.Fail(err).Enrichf("patching ReplicatedVolume"),
		)
	}

	return outcome
}

func isDMInSync(rv *v1alpha1.ReplicatedVolume, targetDM *v1alpha1.DeviceMinor, targetDMCond metav1.Condition) bool {
	return ptr.Equal(rv.Status.DeviceMinor, targetDM) &&
		obju.IsStatusConditionPresentAndSemanticallyEqual(rv, targetDMCond)
}

func applyDM(rv *v1alpha1.ReplicatedVolume, targetDM *v1alpha1.DeviceMinor, targetDMCond metav1.Condition) {
	rv.Status.DeviceMinor = targetDM
	obju.SetStatusCondition(rv, targetDMCond)
}

func (r *Reconciler) allocateDM(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rvName string,
) (outcome flow.ReconcileOutcome, targetDM *v1alpha1.DeviceMinor, targetDMCond metav1.Condition) {
	rf := flow.BeginReconcile(ctx, "device-minor")
	defer rf.OnEnd(&outcome)

	// Wait for pool to be ready (blocks until initialized after leader election).
	pool, err := r.deviceMinorPoolSource.DeviceMinorPool(rf.Ctx())
	if err != nil {
		// IMPORTANT: if pool is unavailable we do NOT change rv.Status.DeviceMinor.
		// If it was previously assigned, it must remain as-is to avoid creating conflicts.
		// We still want to expose the failure via a proper status condition.
		if rv != nil {
			targetDM = rv.Status.DeviceMinor
		}
		targetDMCond = newDeviceMinorAssignedCondition(err)
		return rf.Failf(err, "getting device minor idpool"), targetDM, targetDMCond
	}

	if rv == nil {
		// Release device minor from pool only when object is NotFound.
		rf.Log().Info("ReplicatedVolume deleted, releasing device minor from pool")
		pool.Release(rvName)

		return rf.Continue(), nil, metav1.Condition{}
	}

	// Allocate device minor and compute condition
	targetDM, dmErr := pool.EnsureAllocated(rv.Name, rv.Status.DeviceMinor)
	targetDMCond = newDeviceMinorAssignedCondition(dmErr)

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

		return rf.Fail(dmErr).Enrichf("allocating device minor"), targetDM, targetDMCond
	}

	return rf.Continue(), targetDM, targetDMCond
}

// newDeviceMinorAssignedCondition computes the condition value for
// ReplicatedVolumeCondDeviceMinorAssignedType based on the allocation/validation error (if any).
//
// - If err is nil: Status=True, Reason=Assigned.
// - If err is a DuplicateIDError: Status=False, Reason=Duplicate, Message=err.Error().
// - Otherwise: Status=False, Reason=AssignmentFailed, Message=err.Error().
func newDeviceMinorAssignedCondition(err error) metav1.Condition {
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
