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

package rvrdiskfulcount

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

type Reconciler struct {
	cl     client.Client
	log    logr.Logger
	scheme *runtime.Scheme
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

var ErrEmptyReplicatedStorageClassName = errors.New("ReplicatedVolume has empty ReplicatedStorageClassName")

// NewReconciler is a small helper constructor that is primarily useful for tests.
func NewReconciler(cl client.Client, log logr.Logger, scheme *runtime.Scheme) *Reconciler {
	return &Reconciler{
		cl:     cl,
		log:    log,
		scheme: scheme,
	}
}

func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	// always will come an event on ReplicatedVolume, even if the event happened on ReplicatedVolumeReplica

	log := r.log.WithName("Reconcile").WithValues("req", req)
	log.Info("Reconciling started")
	start := time.Now()
	defer func() {
		log.Info("Reconcile finished", "duration", time.Since(start).String())
	}()

	// Get ReplicatedVolume object
	rv := &v1alpha1.ReplicatedVolume{}
	err := r.cl.Get(ctx, req.NamespacedName, rv)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("ReplicatedVolume not found, ignoring reconcile request")
			return reconcile.Result{}, nil
		}
		log.Error(err, "getting ReplicatedVolume")
		return reconcile.Result{}, err
	}

	if !v1alpha1.HasControllerFinalizer(rv) {
		log.Info("ReplicatedVolume does not have controller finalizer, ignoring reconcile request")
		return reconcile.Result{}, nil
	}

	if rv.DeletionTimestamp != nil && !v1alpha1.HasExternalFinalizers(rv) {
		log.Info("ReplicatedVolume is being deleted, ignoring reconcile request")
		return reconcile.Result{}, nil
	}

	// Get ReplicatedStorageClass object
	rscName := rv.Spec.ReplicatedStorageClassName
	if rscName == "" {
		log.Error(ErrEmptyReplicatedStorageClassName, "ReplicatedVolume has empty ReplicatedStorageClassName")
		return reconcile.Result{}, ErrEmptyReplicatedStorageClassName
	}

	rsc := &v1alpha1.ReplicatedStorageClass{}
	err = r.cl.Get(ctx, client.ObjectKey{Name: rscName}, rsc)
	if err != nil {
		log.Error(err, "getting ReplicatedStorageClass", "name", rscName)
		return reconcile.Result{}, err
	}

	// Get diskful replica count
	neededNumberOfReplicas, err := getDiskfulReplicaCountFromReplicatedStorageClass(rsc)
	if err != nil {
		log.Error(err, "getting diskful replica count")
		return reconcile.Result{}, err
	}
	log.V(4).Info("Calculated diskful replica count", "count", neededNumberOfReplicas)

	// Get all RVRs for this RV
	rvrList := &v1alpha1.ReplicatedVolumeReplicaList{}
	if err = r.cl.List(ctx, rvrList); err != nil {
		log.Error(err, "listing all ReplicatedVolumeReplicas")
		return reconcile.Result{}, err
	}
	rvrList.Items = slices.DeleteFunc(
		rvrList.Items,
		func(rvr v1alpha1.ReplicatedVolumeReplica) bool { return rvr.Spec.ReplicatedVolumeName != rv.Name },
	)

	totalRvrMap, err := getDiskfulReplicatedVolumeReplicas(ctx, r.cl, rv, log, rvrList.Items)
	if err != nil {
		return reconcile.Result{}, err
	}

	deletedRvrMap, nonDeletedRvrMap := splitReplicasByDeletionStatus(totalRvrMap)

	log.V(4).Info("Counted RVRs", "total", len(totalRvrMap), "deleted", len(deletedRvrMap), "nonDeleted", len(nonDeletedRvrMap))

	switch {
	case len(nonDeletedRvrMap) == 0:
		log.Info("No non-deleted ReplicatedVolumeReplicas found for ReplicatedVolume, creating one")
		err = createReplicatedVolumeReplica(ctx, r.cl, r.scheme, rv, log, &rvrList.Items)
		if err != nil {
			log.Error(err, "creating ReplicatedVolumeReplica")
			return reconcile.Result{}, err
		}

		return reconcile.Result{}, nil

	case len(nonDeletedRvrMap) == 1:
		// Need to wait until RVR becomes Ready.
		for _, rvr := range nonDeletedRvrMap {
			// Do nothing until the only non-deleted replica is ready
			if !isRvrReady(rvr) {
				log.V(4).Info("RVR is not ready yet, waiting", "rvr", rvr.Name)
				return reconcile.Result{}, nil
			}

			// Ready condition is True, continue with the code
			log.V(4).Info("RVR Ready condition is True, continuing", "rvr", rvr.Name)
		}

	case len(nonDeletedRvrMap) > neededNumberOfReplicas:
		// Warning message if more non-deleted diskful RVRs found than needed.
		// Processing such a situation is not the responsibility of this controller.
		log.V(1).Info("More non-deleted diskful ReplicatedVolumeReplicas found than needed", "nonDeletedNumberOfReplicas", len(nonDeletedRvrMap), "neededNumberOfReplicas", neededNumberOfReplicas)
		return reconcile.Result{}, nil
	}

	// Calculate number of replicas to create
	creatingNumberOfReplicas := neededNumberOfReplicas - len(nonDeletedRvrMap)
	log.V(4).Info("Calculated number of replicas to create", "creatingNumberOfReplicas", creatingNumberOfReplicas)

	if creatingNumberOfReplicas > 0 {
		log.Info("Creating replicas", "creatingNumberOfReplicas", creatingNumberOfReplicas)
		for i := 0; i < creatingNumberOfReplicas; i++ {
			log.V(4).Info("Creating replica", "replica", i)
			err = createReplicatedVolumeReplica(ctx, r.cl, r.scheme, rv, log, &rvrList.Items)
			if err != nil {
				log.Error(err, "creating ReplicatedVolumeReplica")
				return reconcile.Result{}, err
			}
		}
	} else {
		log.Info("No replicas to create")
	}

	return reconcile.Result{}, nil
}

// getDiskfulReplicaCountFromReplicatedStorageClass gets the diskful replica count based on ReplicatedStorageClass.
//
// If replication = None, returns 1; if replication = Availability, returns 2;
// if replication = ConsistencyAndAvailability, returns 3.
func getDiskfulReplicaCountFromReplicatedStorageClass(rsc *v1alpha1.ReplicatedStorageClass) (int, error) {
	// Determine diskful replica count based on replication
	switch rsc.Spec.Replication {
	case v1alpha1.ReplicationNone:
		return 1, nil
	case v1alpha1.ReplicationAvailability:
		return 2, nil
	case v1alpha1.ReplicationConsistencyAndAvailability:
		return 3, nil
	default:
		return 0, fmt.Errorf("unknown replication value: %s", rsc.Spec.Replication)
	}
}

// getDiskfulReplicatedVolumeReplicas gets all Diskful ReplicatedVolumeReplica objects for the given ReplicatedVolume
// by the spec.replicatedVolumeName and spec.type fields. Returns a map with RVR name as key and RVR object as value.
// Returns empty map if no RVRs are found.
func getDiskfulReplicatedVolumeReplicas(
	ctx context.Context,
	cl client.Client,
	rv *v1alpha1.ReplicatedVolume,
	log logr.Logger,
	rvRVRs []v1alpha1.ReplicatedVolumeReplica,
) (map[string]*v1alpha1.ReplicatedVolumeReplica, error) {
	// Filter by spec.replicatedVolumeName and build map
	rvrMap := make(map[string]*v1alpha1.ReplicatedVolumeReplica)

	for i := range rvRVRs {
		if rvRVRs[i].Spec.ReplicatedVolumeName == rv.Name && rvRVRs[i].Spec.Type == v1alpha1.ReplicaTypeDiskful {
			rvrMap[rvRVRs[i].Name] = &rvRVRs[i]
		}
	}

	return rvrMap, nil
}

// splitReplicasByDeletionStatus splits replicas into two maps: one with replicas that have DeletionTimestamp,
// and another with replicas that don't have DeletionTimestamp.
// Returns two maps with RVR name as key and RVR object as value. Returns empty maps if no RVRs are found.
func splitReplicasByDeletionStatus(totalRvrMap map[string]*v1alpha1.ReplicatedVolumeReplica) (deletedRvrMap, nonDeletedRvrMap map[string]*v1alpha1.ReplicatedVolumeReplica) {
	deletedRvrMap = make(map[string]*v1alpha1.ReplicatedVolumeReplica, len(totalRvrMap))
	nonDeletedRvrMap = make(map[string]*v1alpha1.ReplicatedVolumeReplica, len(totalRvrMap))
	for _, rvr := range totalRvrMap {
		if !rvr.DeletionTimestamp.IsZero() {
			deletedRvrMap[rvr.Name] = rvr
		} else {
			nonDeletedRvrMap[rvr.Name] = rvr
		}
	}
	return deletedRvrMap, nonDeletedRvrMap
}

// isRvrReady checks if the ReplicatedVolumeReplica has DataInitialized condition set to True.
// Returns false if Status is nil, Conditions is nil, DataInitialized condition is not found, or DataInitialized condition status is not True.
func isRvrReady(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
	if rvr.Status == nil || rvr.Status.Conditions == nil {
		return false
	}
	return meta.IsStatusConditionTrue(rvr.Status.Conditions, v1alpha1.ConditionTypeDataInitialized)
}

// createReplicatedVolumeReplica creates a ReplicatedVolumeReplica for the given ReplicatedVolume with ownerReference to RV.
func createReplicatedVolumeReplica(
	ctx context.Context,
	cl client.Client,
	scheme *runtime.Scheme,
	rv *v1alpha1.ReplicatedVolume,
	log logr.Logger,
	otherRVRs *[]v1alpha1.ReplicatedVolumeReplica,
) error {
	rvr := &v1alpha1.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{
			Finalizers: []string{v1alpha1.ControllerAppFinalizer},
		},
		Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
			ReplicatedVolumeName: rv.Name,
			Type:                 v1alpha1.ReplicaTypeDiskful,
		},
	}

	if !rvr.ChooseNewName(*otherRVRs) {
		return fmt.Errorf("unable to create new rvr: too many existing replicas for rv %s", rv.Name)
	}

	if err := controllerutil.SetControllerReference(rv, rvr, scheme); err != nil {
		log.Error(err, "setting controller reference")
		return err
	}

	err := cl.Create(ctx, rvr)
	if err != nil {
		log.Error(err, "creating ReplicatedVolumeReplica")
		return err
	}

	*otherRVRs = append((*otherRVRs), *rvr)

	log.Info("Created ReplicatedVolumeReplica", "name", rvr.Name)

	return nil
}
