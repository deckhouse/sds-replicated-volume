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
	"fmt"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/idpool"
)

type Reconciler struct {
	cl                    client.Client
	log                   logr.Logger
	deviceMinorPoolSource DeviceMinorPoolSource
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

func NewReconciler(cl client.Client, log logr.Logger, poolSource DeviceMinorPoolSource) *Reconciler {
	return &Reconciler{cl: cl, log: log, deviceMinorPoolSource: poolSource}
}

func Wrap(err error, format string, args ...any) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf(format+": %w", append(args, err)...)
}

func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := r.log.WithValues("req", req)

	// Wait for pool to be ready (blocks until initialized after leader election).
	pool, err := r.deviceMinorPoolSource.DeviceMinorPool(ctx)
	if err != nil {
		return reconcile.Result{}, Wrap(err, "failed to get device minor idpool")
	}

	// Get the ReplicatedVolume
	rv := &v1alpha1.ReplicatedVolume{}
	if err := r.cl.Get(ctx, req.NamespacedName, rv); err != nil {
		if client.IgnoreNotFound(err) == nil {
			// Release device minor from pool only when object is NotFound.
			pool.Release(req.Name)
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, Wrap(err, "failed to get ReplicatedVolume %s", req.Name)
	}

	if err := r.reconcileRV(ctx, log, rv); err != nil {
		return reconcile.Result{}, Wrap(err, "failed to reconcile ReplicatedVolume %s", req.Name)
	}

	if err := r.reconcileRVStatus(ctx, log, rv, pool); err != nil {
		return reconcile.Result{}, Wrap(err, "failed to reconcile ReplicatedVolume %s status", req.Name)
	}

	return reconcile.Result{}, nil
}

func (r *Reconciler) reconcileRV(ctx context.Context, _ logr.Logger, rv *v1alpha1.ReplicatedVolume) error {
	if rv.IsStorageClassLabelInSync() {
		return nil
	}

	original := rv.DeepCopy()

	rv.EnsureStorageClassLabel()

	if err := r.cl.Patch(ctx, rv, client.MergeFrom(original)); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return nil
		}
		return err
	}

	return nil
}

func (r *Reconciler) reconcileRVStatus(ctx context.Context, _ logr.Logger, rv *v1alpha1.ReplicatedVolume, pool *idpool.IDPool[v1alpha1.DeviceMinor]) error {
	desiredDeviceMinor, desiredDeviceMinorComputeErr := computeDeviceMinor(rv, pool)
	desiredDeviceMinorAssignedCondition := computeDeviceMinorAssignedCondition(desiredDeviceMinorComputeErr)

	if rv.Status.DeviceMinorEquals(desiredDeviceMinor) && v1alpha1.IsConditionPresentAndSpecAgnosticEqual(rv.Status.Conditions, desiredDeviceMinorAssignedCondition) {
		return desiredDeviceMinorComputeErr
	}

	original := rv.DeepCopy()

	rv.Status.SetDeviceMinorPtr(desiredDeviceMinor)
	meta.SetStatusCondition(&rv.Status.Conditions, desiredDeviceMinorAssignedCondition)

	if err := r.cl.Status().Patch(ctx, rv, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})); err != nil {
		return err
	}

	// Release the device minor back to the pool if it wasn't assigned.
	// Safe to do here because the status has already been successfully patched in the Kubernetes API.
	if !rv.Status.HasDeviceMinor() {
		pool.Release(rv.Name)
	}

	// if !original.Status.DeviceMinorEquals(rv.Status.DeviceMinor) {
	//	// TODO: log INFO about
	// }

	return desiredDeviceMinorComputeErr
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
		// Device minor is invalid, it's safe to return nil (wich will unset status.deviceMinor in RV) because
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
