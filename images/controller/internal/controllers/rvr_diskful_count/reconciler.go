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
	"fmt"
	"time"

	utils "github.com/deckhouse/sds-common-lib/utils"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/api"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type Reconciler struct {
	cl  client.Client
	log logr.Logger
}

type Request = reconcile.Request

var _ reconcile.Reconciler = (*Reconciler)(nil)

// NewReconciler is a small helper constructor that is primarily useful for tests.
func NewReconciler(cl client.Client, log logr.Logger) *Reconciler {
	return &Reconciler{
		cl:  cl,
		log: log,
	}
}

func (r *Reconciler) Reconcile(ctx context.Context, req Request) (reconcile.Result, error) {
	// always will come an event on ReplicatedVolume, even if the event happened on ReplicatedVolumeReplica

	log := r.log.WithName("Reconcile").WithValues("req", req)
	log.Info("Reconciling started")
	start := time.Now()
	defer func() {
		log.Info("Reconcile finished", "duration", time.Since(start).String())
	}()

	// Get ReplicatedVolume object
	rv := &v1alpha3.ReplicatedVolume{}
	err := r.cl.Get(ctx, client.ObjectKey{Name: req.Name}, rv)
	if err != nil {
		if client.IgnoreNotFound(err) == nil {
			log.Info("ReplicatedVolume not found, ignoring reconcile request")
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, fmt.Errorf("getting ReplicatedVolume: %w", err)
	}

	if rv.DeletionTimestamp != nil {
		log.Info("ReplicatedVolume is being deleted, ignoring reconcile request")
		return reconcile.Result{}, nil
	}

	// Get diskful replica count
	neededNumberOfReplicas, err := getDiskfulReplicaCount(ctx, r.cl, rv)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("getting diskful replica count: %w", err)
	}
	log.V(4).Info("Calculated diskful replica count", "count", neededNumberOfReplicas)

	// Get all RVRs for this RV
	totalRvrMap, err := getDiskfulReplicatedVolumeReplicas(ctx, r.cl, rv)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("getting ReplicatedVolumeReplicas: %w", err)
	}
	totalNumberOfReplicas := len(totalRvrMap)
	log.V(4).Info("Found ReplicatedVolumeReplicas", "count", totalNumberOfReplicas)

	// If no RVRs found, create one
	if totalNumberOfReplicas == 0 {
		log.Info("No ReplicatedVolumeReplicas found for ReplicatedVolume, creating one")
		err = createReplicatedVolumeReplica(ctx, r.cl, rv, log)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("creating ReplicatedVolumeReplica: %w", err)
		}

		err = setDiskfulReplicaCountReachedCondition(
			ctx, r.cl, log, rv,
			metav1.ConditionFalse,
			"FirstReplicaIsBeingCreated",
			fmt.Sprintf("Created first replica, need %d diskful replicas", neededNumberOfReplicas),
		)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("setting DiskfulReplicaCountReached condition: %w", err)
		}

		return reconcile.Result{}, nil
	}

	deletedRvrMap := getDeletedReplicas(totalRvrMap)
	deletedNumberOfReplicas := len(deletedRvrMap)
	log.V(4).Info("Counted deleting ReplicatedVolumeReplicas", "count", deletedNumberOfReplicas)

	// Count non-deleted ReplicatedVolumeReplicas
	nonDeletedNumberOfReplicas := totalNumberOfReplicas - deletedNumberOfReplicas
	log.V(4).Info("Counted non-deleted ReplicatedVolumeReplicas", "count", nonDeletedNumberOfReplicas)

	if nonDeletedNumberOfReplicas == 0 {
		log.Info("No non-deleted ReplicatedVolumeReplicas found for ReplicatedVolume, creating one")
		err = createReplicatedVolumeReplica(ctx, r.cl, rv, log)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("creating ReplicatedVolumeReplica: %w", err)
		}

		err = setDiskfulReplicaCountReachedCondition(
			ctx, r.cl, log, rv,
			metav1.ConditionFalse,
			"FirstReplicaIsBeingCreated",
			fmt.Sprintf("Created non-deleted replica, need %d diskful replicas", neededNumberOfReplicas),
		)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("setting DiskfulReplicaCountReached condition: %w", err)
		}

		return reconcile.Result{}, nil
	}

	// Need to wait until RVR becomes Ready.
	if nonDeletedNumberOfReplicas == 1 {
		for _, rvr := range deletedRvrMap {
			if !isRvrReady(rvr) {
				log.V(4).Info("RVR is not ready yet, waiting", "rvr", rvr.Name)
				return reconcile.Result{}, nil
			}

			// Ready condition is True, continue with the code
			log.V(4).Info("RVR Ready condition is True, continuing", "rvr", rvr.Name)
		}
	}

	// warning message if more non-deleted RVRs found than needed
	if nonDeletedNumberOfReplicas > neededNumberOfReplicas {
		log.V(1).Info("More non-deleted ReplicatedVolumeReplicas found than needed", "nonDeletedNumberOfReplicas", nonDeletedNumberOfReplicas, "neededNumberOfReplicas", neededNumberOfReplicas)

		// TODO: should we set a condition here that there are more replicas than needed?

		return reconcile.Result{}, nil
	}

	// Calculate number of replicas to create
	creatingNumberOfReplicas := neededNumberOfReplicas - nonDeletedNumberOfReplicas
	log.V(4).Info("Calculated number of replicas to create", "creatingNumberOfReplicas", creatingNumberOfReplicas)

	if creatingNumberOfReplicas > 0 {
		log.Info("Creating replicas", "creatingNumberOfReplicas", creatingNumberOfReplicas)
		for i := 0; i < creatingNumberOfReplicas; i++ {
			log.V(4).Info("Creating replica", "replica", i)
			err = createReplicatedVolumeReplica(ctx, r.cl, rv, log)
			if err != nil {
				return reconcile.Result{}, fmt.Errorf("creating ReplicatedVolumeReplica: %w", err)
			}
		}

		// Set condition that required number of replicas is reached
		err = setDiskfulReplicaCountReachedCondition(
			ctx, r.cl, log, rv,
			metav1.ConditionTrue,
			"CreatedRequiredNumberOfReplicas",
			fmt.Sprintf("Created %d replica(s), required number of diskful replicas is reached: %d", creatingNumberOfReplicas, neededNumberOfReplicas),
		)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("setting DiskfulReplicaCountReached condition: %w", err)
		}
	} else {
		log.Info("No replicas to create")
		// Set condition that required number of replicas is reached
		err = setDiskfulReplicaCountReachedCondition(
			ctx, r.cl, log, rv,
			metav1.ConditionTrue,
			"RequiredNumberOfReplicasIsAvailable",
			fmt.Sprintf("Required number of diskful replicas is reached: %d", neededNumberOfReplicas),
		)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("setting DiskfulReplicaCountReached condition: %w", err)
		}
	}

	return reconcile.Result{}, nil
}

// getDiskfulReplicaCount gets the diskful replica count based on ReplicatedStorageClass.
//
// If replication = None, returns 1; if replication = Availability, returns 2;
// if replication = ConsistencyAndAvailability, returns 3.
func getDiskfulReplicaCount(ctx context.Context, cl client.Client, rv *v1alpha3.ReplicatedVolume) (int, error) {
	// Get ReplicatedStorageClass name from ReplicatedVolume
	rscName := rv.Spec.ReplicatedStorageClassName
	if rscName == "" {
		return 0, fmt.Errorf("ReplicatedVolume has empty ReplicatedStorageClassName")
	}

	// Get ReplicatedStorageClass object
	rsc := &v1alpha1.ReplicatedStorageClass{}
	err := cl.Get(ctx, client.ObjectKey{Name: rscName}, rsc)
	if err != nil {
		return 0, fmt.Errorf("getting ReplicatedStorageClass %s: %w", rscName, err)
	}

	// Determine diskful replica count based on replication
	switch rsc.Spec.Replication {
	case "None":
		return 1, nil
	case "Availability":
		return 2, nil
	case "ConsistencyAndAvailability":
		return 3, nil
	default:
		return 0, fmt.Errorf("unknown replication value: %s", rsc.Spec.Replication)
	}
}

// getDiskfulReplicatedVolumeReplicas gets all Diskful ReplicatedVolumeReplica objects for the given ReplicatedVolume
// by the spec.replicatedVolumeName and spec.type fields. Returns a map with RVR name as key and RVR object as value.
// Returns empty map if no RVRs are found.
func getDiskfulReplicatedVolumeReplicas(ctx context.Context, cl client.Client, rv *v1alpha3.ReplicatedVolume) (map[string]*v1alpha3.ReplicatedVolumeReplica, error) {
	allRvrList := &v1alpha3.ReplicatedVolumeReplicaList{}
	err := cl.List(ctx, allRvrList)
	if err != nil {
		return nil, fmt.Errorf("listing all ReplicatedVolumeReplicas: %w", err)
	}

	// Filter by spec.replicatedVolumeName and build map
	rvrMap := make(map[string]*v1alpha3.ReplicatedVolumeReplica)

	for i := range allRvrList.Items {
		if allRvrList.Items[i].Spec.ReplicatedVolumeName == rv.Name && allRvrList.Items[i].Spec.Type == "Diskful" {
			rvrMap[allRvrList.Items[i].Name] = &allRvrList.Items[i]
		}
	}

	return rvrMap, nil
}

// Returns a map with RVR name as key and RVR object as value. Returns empty map if no deleted RVRs are found.
func getDeletedReplicas(totalRvrMap map[string]*v1alpha3.ReplicatedVolumeReplica) map[string]*v1alpha3.ReplicatedVolumeReplica {
	rvrMap := make(map[string]*v1alpha3.ReplicatedVolumeReplica)
	for _, rvr := range totalRvrMap {
		if rvr.DeletionTimestamp != nil {
			rvrMap[rvr.Name] = rvr
		}
	}
	return rvrMap
}

// isRvrReady checks if the ReplicatedVolumeReplica has Ready condition set to True.
// Returns false if Status is nil, Conditions is nil, Ready condition is not found, or Ready condition status is not True.
func isRvrReady(rvr *v1alpha3.ReplicatedVolumeReplica) bool {
	if rvr.Status == nil || rvr.Status.Conditions == nil {
		return false
	}

	readyCondition := meta.FindStatusCondition(rvr.Status.Conditions, v1alpha3.ConditionTypeReady)
	if readyCondition == nil {
		return false
	}

	return readyCondition.Status == metav1.ConditionTrue
}

// createReplicatedVolumeReplica creates a ReplicatedVolumeReplica for the given ReplicatedVolume
// with ownerReference to RV. Uses the first node from spec.publishOn for nodeName.
func createReplicatedVolumeReplica(ctx context.Context, cl client.Client, rv *v1alpha3.ReplicatedVolume, log logr.Logger) error {
	ownerRef := metav1.OwnerReference{
		APIVersion:         "storage.deckhouse.io/v1alpha3",
		Kind:               "ReplicatedVolume",
		Name:               rv.Name,
		UID:                rv.UID,
		Controller:         utils.Ptr(true),
		BlockOwnerDeletion: utils.Ptr(true),
	}

	generateName := fmt.Sprintf("%s%s", rv.Name, "-")

	rvr := &v1alpha3.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName:    generateName,
			OwnerReferences: []metav1.OwnerReference{ownerRef},
		},
		Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
			ReplicatedVolumeName: rv.Name,
			Type:                 "Diskful",
		},
	}

	err := cl.Create(ctx, rvr)
	if err != nil {
		return fmt.Errorf("creating ReplicatedVolumeReplica with GenerateName %s: %w", generateName, err)
	}

	log.Info(fmt.Sprintf("Created ReplicatedVolumeReplica %s for ReplicatedVolume", rvr.Name))

	return nil
}

// setDiskfulReplicaCountReachedCondition sets or updates the DiskfulReplicaCountReached condition
// on the ReplicatedVolume status with the provided status, reason, and message.
func setDiskfulReplicaCountReachedCondition(
	ctx context.Context,
	cl client.Client,
	log logr.Logger,
	rv *v1alpha3.ReplicatedVolume,
	status metav1.ConditionStatus,
	reason string,
	message string,
) error {
	log.V(4).Info(fmt.Sprintf("Setting %s condition", v1alpha3.ConditionTypeDiskfulReplicaCountReached), "status", status, "reason", reason, "message", message)
	return api.PatchStatusWithConflictRetry(ctx, cl, rv, func(rv *v1alpha3.ReplicatedVolume) error {
		if rv.Status == nil {
			rv.Status = &v1alpha3.ReplicatedVolumeStatus{}
		}
		meta.SetStatusCondition(
			&rv.Status.Conditions,
			metav1.Condition{
				Type:               v1alpha3.ConditionTypeDiskfulReplicaCountReached,
				Status:             status,
				Reason:             reason,
				Message:            message,
				ObservedGeneration: rv.Generation,
			},
		)
		return nil
	})
}
