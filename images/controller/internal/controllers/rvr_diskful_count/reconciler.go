package rvrdiskfulcount

import (
	"context"

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
	log := r.log.WithName("Reconcile").WithValues("req", req)
	log.Info("Reconciling")

	// Determine object type: try to get both types and see which one exists
	rvr := &v1alpha3.ReplicatedVolumeReplica{}
	errRVR := r.cl.Get(ctx, req.NamespacedName, rvr)
	if errRVR == nil {
		// This is ReplicatedVolumeReplica
		log.Info("Reconciling ReplicatedVolumeReplica", "name", req.Name)
		return r.reconcileRVR(ctx, rvr)
	}

	rv := &v1alpha3.ReplicatedVolume{}
	errRV := r.cl.Get(ctx, req.NamespacedName, rv)
	if errRV == nil {
		// This is ReplicatedVolume
		log.Info("Reconciling ReplicatedVolume", "name", req.Name)
		return r.reconcileRV(ctx, rv)
	}

	// If both objects are not found, they might have been deleted
	if client.IgnoreNotFound(errRVR) == nil && client.IgnoreNotFound(errRV) == nil {
		log.Info("Object not found, might be deleted", "name", req.Name)
		return reconcile.Result{}, nil
	}

	// If there was another error, return it (prioritize the first error if it's not NotFound)
	if client.IgnoreNotFound(errRVR) != nil {
		return reconcile.Result{}, errRVR
	}
	return reconcile.Result{}, errRV
}

func (r *Reconciler) reconcileRVR(ctx context.Context, rvr *v1alpha3.ReplicatedVolumeReplica) (reconcile.Result, error) {
	// TODO: implement logic for ReplicatedVolumeReplica
	return reconcile.Result{}, nil
}

func (r *Reconciler) reconcileRV(ctx context.Context, rv *v1alpha3.ReplicatedVolume) (reconcile.Result, error) {
	// TODO: implement logic for ReplicatedVolume
	return reconcile.Result{}, nil
}
