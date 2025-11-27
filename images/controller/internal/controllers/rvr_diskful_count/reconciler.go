package rvrdiskfulcount

import (
	"context"
	"fmt"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
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
	rvrList, err := getReplicatedVolumeReplicas(ctx, r.cl, rv)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("getting ReplicatedVolumeReplicas: %w", err)
	}
	log.V(4).Info("Found ReplicatedVolumeReplicas", "count", len(rvrList.Items))

	if len(rvrList.Items) == 0 {
		log.Info("No ReplicatedVolumeReplicas found for ReplicatedVolume, creating one")
		err = createReplicatedVolumeReplica(ctx, r.cl, rv, log)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("creating ReplicatedVolumeReplica: %w", err)
		}
		log.Info("Created ReplicatedVolumeReplica for ReplicatedVolume")
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
// by the spec.replicatedVolumeName field.
func getReplicatedVolumeReplicas(ctx context.Context, cl client.Client, rv *v1alpha3.ReplicatedVolume) (*v1alpha3.ReplicatedVolumeReplicaList, error) {
	rvrList := &v1alpha3.ReplicatedVolumeReplicaList{}
	err := cl.List(
		ctx,
		rvrList,
		client.MatchingFields{"spec.replicatedVolumeName": rv.Name},
	)
	if err != nil {
		return nil, fmt.Errorf("listing ReplicatedVolumeReplicas for ReplicatedVolume %s: %w", rv.Name, err)
	}
	return rvrList, nil
}

// createReplicatedVolumeReplica creates a ReplicatedVolumeReplica for the given ReplicatedVolume
// with ownerReference to RV. Uses the first node from spec.publishOn for nodeName.
func createReplicatedVolumeReplica(ctx context.Context, cl client.Client, rv *v1alpha3.ReplicatedVolume, log logr.Logger) error {
	// Determine nodeName from PublishOn
	var nodeName string
	if len(rv.Spec.PublishOn) > 0 {
		nodeName = rv.Spec.PublishOn[0]
	} else {
		return fmt.Errorf("cannot create ReplicatedVolumeReplica: ReplicatedVolume has no PublishOn nodes specified")
	}

	// Create ownerReference
	gv := schema.GroupVersion{Group: "storage.deckhouse.io", Version: "v1alpha3"}
	ownerRef := metav1.NewControllerRef(rv, schema.GroupVersionKind{
		Group:   gv.Group,
		Version: gv.Version,
		Kind:    "ReplicatedVolume",
	})

	generateName := fmt.Sprintf("%s%s", rv.Name, "-")
	log.V(4).Info("Creating ReplicatedVolumeReplica", "generateName", generateName)

	// Create ReplicatedVolumeReplica object
	rvr := &v1alpha3.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName:    generateName,
			OwnerReferences: []metav1.OwnerReference{*ownerRef},
		},
		Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
			ReplicatedVolumeName: rv.Name,
			NodeName:             nodeName,
			Diskless:             false,
		},
	}

	err := cl.Create(ctx, rvr)
	if err != nil {
		return fmt.Errorf("creating ReplicatedVolumeReplica with GenerateName %s: %w", generateName, err)
	}

	return nil
}
