package rvrownerreferencecontroller

import (
	"context"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
)

type Reconciler struct {
	cl     client.Client
	log    logr.Logger
	scheme *runtime.Scheme
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

func NewReconciler(cl client.Client, log logr.Logger, scheme *runtime.Scheme) *Reconciler {
	return &Reconciler{
		cl:     cl,
		log:    log,
		scheme: scheme,
	}
}

func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := r.log.WithName("Reconcile").WithValues("req", req)

	rvr := &v1alpha3.ReplicatedVolumeReplica{}
	if err := r.cl.Get(ctx, req.NamespacedName, rvr); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	if !rvr.DeletionTimestamp.IsZero() {
		return reconcile.Result{}, nil
	}

	if rvr.Spec.ReplicatedVolumeName == "" {
		return reconcile.Result{}, nil
	}

	rv := &v1alpha3.ReplicatedVolume{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: rvr.Spec.ReplicatedVolumeName}, rv); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	originalRVR := rvr.DeepCopy()

	if err := controllerutil.SetControllerReference(rv, rvr, r.scheme); err != nil {
		log.Error(err, "unable to set controller reference")
		return reconcile.Result{}, err
	}

	if err := r.cl.Patch(ctx, rvr, client.MergeFrom(originalRVR)); err != nil {
		log.Error(err, "unable to patch ReplicatedVolumeReplica ownerReference", "rvr", rvr.Name)
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}
