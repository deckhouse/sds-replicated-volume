package rvrdiskfulcount

import (
	"context"
	"log/slog"
	"slices"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	e "github.com/deckhouse/sds-replicated-volume/images/controller/internal/errors"
	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type Reconciler struct {
	cl  client.Client
	rdr client.Reader
	sch *runtime.Scheme
	log logr.Logger
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

func (r *Reconciler) Reconcile(
	ctx context.Context,
	req reconcile.Request,
) (reconcile.Result, error) {
	log := r.log.WithValues("request", req.NamespacedName).WithName("Reconcile")
	var rv v1alpha3.ReplicatedVolume
	if err := r.cl.Get(ctx, req.NamespacedName, &rv); err != nil {
		log.Error(err, "unable to fetch ReplicatedVolume")
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	conditions.IsTrue(rv.Status, DiskfulReplicaCountReached)
	//DiskfulReplicaCountReached==True
	//AllReplicasReady==True
	//SharedSecretAlgorithmSelected==True

	var rvrList v1alpha3.ReplicatedVolumeReplicaList
	if err := r.cl.List(ctx, &rvrList); err != nil {
		log.Error(err, "unable to fetch ReplicatedVolumeReplicaList")
		return reconcile.Result{}, err
	}

	rvrList.Items = slices.DeleteFunc(rvrList.Items, func(rvr v1alpha3.ReplicatedVolumeReplica) bool {
		return !metav1.IsControlledBy(&rvr, &rv)
	})
	from := client.MergeFrom(rv.DeepCopy()) // DeepCopy ?
	// какое то изменение rv
	// кром QuorumConfigured==True
	if err := r.cl.Patch(ctx, &rv, from); err != nil {
		log.Error(err, "unable to fetch ReplicatedVolume")
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
}
