package rvrdiskfulcount

import (
	"context"
	"fmt"

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

func (r *Reconciler) Reconcile(ctx context.Context, req Request) (reconcile.Result, error) {
	// always will come an event on ReplicatedVolume, even if the event happened on ReplicatedVolumeReplica

	log := r.log.WithName("Reconcile").WithValues("req", req)
	log.Info("Reconciling")

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
	diskfulCount, err := getDiskfulReplicaCount(ctx, r.cl, rv)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("getting diskful replica count: %w", err)
	}
	log.V(4).Info("Calculated diskful replica count", "count", diskfulCount)

	// Get all RVRs for this RV
	rvrMap, err := getReplicatedVolumeReplicas(ctx, r.cl, rv)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("getting ReplicatedVolumeReplicas: %w", err)
	}
	log.V(4).Info("Found ReplicatedVolumeReplicas", "count", len(rvrMap))

	if len(rvrMap) == 0 {
		log.Info("No ReplicatedVolumeReplicas found for ReplicatedVolume, creating one")
		err = createReplicatedVolumeReplica(ctx, r.cl, rv, log)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("creating ReplicatedVolumeReplica: %w", err)
		}
		log.Info("Created ReplicatedVolumeReplica for ReplicatedVolume")

		// Update condition after creating replica
		err = setDiskfulReplicaCountReachedCondition(
			ctx, r.cl, log, rv,
			metav1.ConditionFalse,
			"ReplicasBeingCreated",
			fmt.Sprintf("Created first replica, need %d diskful replicas", diskfulCount),
		)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("setting DiskfulReplicaCountReached condition: %w", err)
		}

		return reconcile.Result{}, nil
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

// getReplicatedVolumeReplicas gets all ReplicatedVolumeReplica objects for the given ReplicatedVolume
// by the spec.replicatedVolumeName field. Returns a map with RVR name as key and RVR object as value.
// Returns empty map if no RVRs are found.
func getReplicatedVolumeReplicas(ctx context.Context, cl client.Client, rv *v1alpha3.ReplicatedVolume) (map[string]*v1alpha3.ReplicatedVolumeReplica, error) {
	allRvrList := &v1alpha3.ReplicatedVolumeReplicaList{}
	err := cl.List(ctx, allRvrList)
	if err != nil {
		return nil, fmt.Errorf("listing all ReplicatedVolumeReplicas: %w", err)
	}

	// Filter by spec.replicatedVolumeName and build map
	rvrMap := make(map[string]*v1alpha3.ReplicatedVolumeReplica)

	for i := range allRvrList.Items {
		if allRvrList.Items[i].Spec.ReplicatedVolumeName == rv.Name {
			rvrMap[allRvrList.Items[i].Name] = &allRvrList.Items[i]
		}
	}

	return rvrMap, nil
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
	log.V(4).Info("Creating ReplicatedVolumeReplica", "generateName", generateName)

	rvr := &v1alpha3.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName:    generateName,
			OwnerReferences: []metav1.OwnerReference{ownerRef},
		},
		Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
			ReplicatedVolumeName: rv.Name,
			Diskless:             false,
		},
	}

	err := cl.Create(ctx, rvr)
	if err != nil {
		return fmt.Errorf("creating ReplicatedVolumeReplica with GenerateName %s: %w", generateName, err)
	}

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
