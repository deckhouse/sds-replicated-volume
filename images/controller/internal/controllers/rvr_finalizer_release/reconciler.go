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

package rvrfinalizerrelease

import (
	"context"
	"slices"
	"time"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes"
)

const requeueAfterSec = 10

type Reconciler struct {
	cl     client.Client
	log    logr.Logger
	scheme *runtime.Scheme
}

func NewReconciler(cl client.Client, log logr.Logger, scheme *runtime.Scheme) *Reconciler {
	return &Reconciler{
		cl:     cl,
		log:    log,
		scheme: scheme,
	}
}

var _ reconcile.Reconciler = &Reconciler{}

func (r *Reconciler) Reconcile(
	ctx context.Context,
	req reconcile.Request,
) (reconcile.Result, error) {
	log := r.log.WithName("Reconcile").WithValues("request", req)

	rvr := &v1alpha1.ReplicatedVolumeReplica{}
	if err := r.cl.Get(ctx, req.NamespacedName, rvr); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("ReplicatedVolumeReplica not found, probably already deleted")
			return reconcile.Result{}, nil
		}
		log.Error(err, "Can't get ReplicatedVolumeReplica")
		return reconcile.Result{}, err
	}

	if rvr.DeletionTimestamp.IsZero() {
		log.Info("ReplicatedVolumeReplica is not being deleted, skipping")
		return reconcile.Result{}, nil
	}

	rv, rsc, replicasForRV, err := r.loadGCContext(ctx, rvr.Spec.ReplicatedVolumeName, log)
	if err != nil {
		return reconcile.Result{}, err
	}

	if rv.DeletionTimestamp == nil {
		if !isThisReplicaCountEnoughForQuorum(rv, replicasForRV, rvr.Name) {
			log.Info("cluster is not ready for RVR GC: quorum condition is not satisfied. Requeue after", "seconds", requeueAfterSec)
			return reconcile.Result{
				RequeueAfter: requeueAfterSec * time.Second,
			}, nil
		}

		if !hasEnoughDiskfulReplicasForReplication(rsc, replicasForRV, rvr.Name) {
			log.Info("cluster is not ready for RVR GC: replication condition is not satisfied. Requeue after", "seconds", requeueAfterSec)
			return reconcile.Result{
				RequeueAfter: requeueAfterSec * time.Second,
			}, nil
		}

		if isDeletingReplicaAttached(rv, rvr.Spec.NodeName) {
			log.Info("cluster is not ready for RVR GC: deleting replica is attached. Requeue after", "seconds", requeueAfterSec)
			return reconcile.Result{
				RequeueAfter: requeueAfterSec * time.Second,
			}, nil
		}
	} else {
		for i := range replicasForRV {
			if isDeletingReplicaAttached(rv, replicasForRV[i].Spec.NodeName) {
				log.Info("cluster is not ready for RVR GC: one replica is still attached. Requeue after",
					"seconds", requeueAfterSec,
					"replicaName", replicasForRV[i].Name)
				return reconcile.Result{
					RequeueAfter: requeueAfterSec * time.Second,
				}, nil
			}
		}
	}

	if err := r.removeControllerFinalizer(ctx, rvr, log); err != nil {
		return reconcile.Result{}, err
	}

	// If this RVR is the last one for the RV, remove controller finalizer from RV as well.
	// This allows RV to be deleted / managed without being blocked by an orphaned finalizer.
	if isLastReplicaForRV(replicasForRV, rvr.Name) {
		if err := removeRVControllerFinalizer(ctx, r.cl, rv); err != nil {
			if apierrors.IsConflict(err) {
				return reconcile.Result{Requeue: true}, nil
			}
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

func (r *Reconciler) loadGCContext(
	ctx context.Context,
	rvName string,
	log logr.Logger,
) (*v1alpha1.ReplicatedVolume, *v1alpha1.ReplicatedStorageClass, []v1alpha1.ReplicatedVolumeReplica, error) {
	rv := &v1alpha1.ReplicatedVolume{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: rvName}, rv); err != nil {
		log.Error(err, "Can't get ReplicatedVolume")
		return nil, nil, nil, err
	}

	rsc := &v1alpha1.ReplicatedStorageClass{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: rv.Spec.ReplicatedStorageClassName}, rsc); err != nil {
		log.Error(err, "Can't get ReplicatedStorageClass")
		return nil, nil, nil, err
	}

	rvrList := &v1alpha1.ReplicatedVolumeReplicaList{}
	if err := r.cl.List(ctx, rvrList, client.MatchingFields{
		indexes.IndexFieldRVRByReplicatedVolumeName: rv.Name,
	}); err != nil {
		log.Error(err, "Can't list ReplicatedVolumeReplica")
		return nil, nil, nil, err
	}

	return rv, rsc, rvrList.Items, nil
}

func isThisReplicaCountEnoughForQuorum(
	rv *v1alpha1.ReplicatedVolume,
	replicasForRV []v1alpha1.ReplicatedVolumeReplica,
	deletingRVRName string,
) bool {
	quorum := 0
	if rv.Status.DRBD != nil && rv.Status.DRBD.Config != nil {
		quorum = int(rv.Status.DRBD.Config.Quorum)
	}
	if quorum == 0 {
		return true
	}

	onlineReplicaCount := 0
	for _, rvr := range replicasForRV {
		if rvr.Name == deletingRVRName {
			continue
		}
		if meta.IsStatusConditionTrue(rvr.Status.Conditions, v1alpha1.ReplicatedVolumeReplicaCondOnlineType) {
			onlineReplicaCount++
		}
	}

	return onlineReplicaCount >= quorum
}

func isDeletingReplicaAttached(
	rv *v1alpha1.ReplicatedVolume,
	deletingRVRNodeName string,
) bool {
	if deletingRVRNodeName == "" {
		return false
	}

	return slices.Contains(rv.Status.ActuallyAttachedTo, deletingRVRNodeName)
}

func hasEnoughDiskfulReplicasForReplication(
	rsc *v1alpha1.ReplicatedStorageClass,
	replicasForRV []v1alpha1.ReplicatedVolumeReplica,
	deletingRVRName string,
) bool {
	var requiredDiskful int
	switch rsc.Spec.Replication {
	case "ConsistencyAndAvailability":
		requiredDiskful = 3
	case "Availability":
		requiredDiskful = 2
	default:
		requiredDiskful = 1
	}

	ioReadyDiskfullCount := 0
	for _, rvr := range replicasForRV {
		if rvr.Name == deletingRVRName {
			continue
		}
		if !rvr.DeletionTimestamp.IsZero() {
			continue
		}
		if rvr.Spec.Type != v1alpha1.ReplicaTypeDiskful {
			continue
		}
		if rvr.Status.ActualType != v1alpha1.ReplicaTypeDiskful {
			continue
		}

		if !meta.IsStatusConditionTrue(rvr.Status.Conditions, v1alpha1.ReplicatedVolumeReplicaCondIOReadyType) {
			continue
		}

		ioReadyDiskfullCount++
	}

	return ioReadyDiskfullCount >= requiredDiskful
}

func (r *Reconciler) removeControllerFinalizer(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	log logr.Logger,
) error {
	current := &v1alpha1.ReplicatedVolumeReplica{}
	if err := r.cl.Get(ctx, client.ObjectKeyFromObject(rvr), current); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		log.Error(err, "failed to reload ReplicatedVolumeReplica before removing controller finalizer", "rvr", rvr.Name)
		return err
	}

	if len(current.Finalizers) == 0 {
		return nil
	}

	oldFinalizersLen := len(current.Finalizers)
	current.Finalizers = slices.DeleteFunc(current.Finalizers, func(f string) bool { return f == v1alpha1.ControllerFinalizer })

	if oldFinalizersLen == len(current.Finalizers) {
		return nil
	}

	if err := r.cl.Update(ctx, current); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		log.Error(err, "failed to update ReplicatedVolumeReplica while removing controller finalizer", "rvr", rvr.Name)
		return err
	}

	return nil
}

func isLastReplicaForRV(replicasForRV []v1alpha1.ReplicatedVolumeReplica, deletingRVRName string) bool {
	for i := range replicasForRV {
		if replicasForRV[i].Name != deletingRVRName {
			return false
		}
	}
	return true
}

func removeRVControllerFinalizer(ctx context.Context, cl client.Client, rv *v1alpha1.ReplicatedVolume) error {
	if rv == nil {
		panic("removeRVControllerFinalizer: nil rv (programmer error)")
	}
	if !v1alpha1.HasControllerFinalizer(rv) {
		return nil
	}

	original := rv.DeepCopy()
	rv.Finalizers = slices.DeleteFunc(rv.Finalizers, func(f string) bool { return f == v1alpha1.ControllerFinalizer })
	return cl.Patch(ctx, rv, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{}))
}
