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

	var rvr v1alpha3.ReplicatedVolumeReplica
	if err := r.cl.Get(ctx, req.NamespacedName, &rvr); err != nil {
		log.Error(err, "Can't get ReplicatedVolumeReplica")
		return reconcile.Result{}, err
	}

	if rvr.Spec.ReplicatedVolumeName == "" {
		log.Info("ReplicatedVolumeName is empty. Skipping")
		return reconcile.Result{}, nil
	}

	if !rvr.IsReady() {
		log.Info("ReplicatedVolumeName is not ready. Skipping")
		return reconcile.Result{}, nil
	}

	log.V(1).Info("Listing other replicas")
	var list v1alpha3.ReplicatedVolumeReplicaList
	if err := r.cl.List(ctx, &list); err != nil {
		log.Error(err, "Listing ReplicatedVolumeReplica")
		return reconcile.Result{}, err
	}

	peers := make(map[string]v1alpha3.Peer, 8)
	for _, item := range list.Items {
		log := log.WithValues("item", item)
		if item.Spec.ReplicatedVolumeName == "" {
			log.V(2).Info("ReplicatedVolumeName is empty. Skipping")
			continue
		}

		if item.Spec.ReplicatedVolumeName != rvr.Spec.ReplicatedVolumeName {
			log.V(2).Info("ReplicatedVolumeName is not the same. Skipping")
			continue
		}

		if !item.IsReady() {
			log.V(2).Info("ReplicatedVolumeName is not ready. Skipping")
			continue
		}

		if item.Spec.NodeName == rvr.Spec.NodeName {
			log.V(2).Info("It's the target replica. Skipping")
			continue
		}

		if _, exist := peers[item.Spec.NodeName]; exist {
			log.Info("Peer on this node already found. Skipping")
			continue
		}

		peers[item.Spec.NodeName] = v1alpha3.Peer{
			NodeId:   *item.Status.Config.NodeId,
			Address:  *item.Status.Config.Address,
			Diskless: item.Spec.Diskless,
		}
	}

	log.Info("Patching with new peers", "peers", peers)
	from := client.MergeFrom(&rvr)
	rvr.Status.Config.Peers = peers
	if err := r.cl.Patch(ctx, &rvr, from); err != nil {
		log.Error(err, "Patching ReplicatedVolumeReplica")
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}
