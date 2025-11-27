package rvrdiskfulcount

import (
	"context"
	"fmt"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"github.com/go-logr/logr"
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

	// Получаем объект ReplicatedVolume
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

	// Получаем количество diskful реплик
	diskfulCount, err := getDiskfulReplicaCount(ctx, r.cl, rv)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("getting diskful replica count: %w", err)
	}
	log.V(4).Info("Calculated diskful replica count", "count", diskfulCount)

	// Получаем все RVR для данного RV
	rvrList, err := getReplicatedVolumeReplicas(ctx, r.cl, rv)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("getting ReplicatedVolumeReplicas: %w", err)
	}
	log.V(4).Info("Found ReplicatedVolumeReplicas", "count", len(rvrList.Items))

	return reconcile.Result{}, nil
}

// getDiskfulReplicaCount получает количество diskful реплик на основе ReplicatedStorageClass.
//
// Если replication = None, возвращает 1; если replication = Availability, возвращает 2;
// если replication = ConsistencyAndAvailability, возвращает 3.
func getDiskfulReplicaCount(ctx context.Context, cl client.Client, rv *v1alpha3.ReplicatedVolume) (int, error) {
	// Получаем имя ReplicatedStorageClass из ReplicatedVolume
	rscName := rv.Spec.ReplicatedStorageClassName
	if rscName == "" {
		return 0, fmt.Errorf("ReplicatedVolume has empty ReplicatedStorageClassName")
	}

	// Получаем объект ReplicatedStorageClass
	rsc := &v1alpha1.ReplicatedStorageClass{}
	err := cl.Get(ctx, client.ObjectKey{Name: rscName}, rsc)
	if err != nil {
		return 0, fmt.Errorf("getting ReplicatedStorageClass %s: %w", rscName, err)
	}

	// Определяем количество diskful реплик на основе replication
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

// getReplicatedVolumeReplicas получает все ReplicatedVolumeReplica для данного ReplicatedVolume
// по полю spec.replicatedVolumeName.
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
