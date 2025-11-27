package rvrdiskfulcount

import (
	"context"

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

	return reconcile.Result{}, nil
}
