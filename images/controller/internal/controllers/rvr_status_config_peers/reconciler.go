package rvrdiskfulcount

import (
	"context"
	"maps"
	"slices"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"github.com/go-logr/logr"
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

func shouldBeListedInPeers(rvr *ReplicatedVolumeReplica) bool {
	return rvr.Spec.NodeName != "" &&
		rvr.Status != nil && rvr.Status.Config != nil &&
		rvr.Status.Config.NodeId != nil && rvr.Status.Config.Address != nil
}

func (r *Reconciler) Reconcile(ctx context.Context, req Request) (reconcile.Result, error) {
	log := r.log.WithName("Reconcile").WithValues("req", req)
	log.Info("Reconciling")

	var rv v1alpha3.ReplicatedVolume
	if err := r.cl.Get(ctx, req.NamespacedName, &rv); err != nil {
		log.Error(err, "Can't get ReplicatedVolume")
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	log.V(1).Info("Listing replicas")
	var list v1alpha3.ReplicatedVolumeReplicaList
	if err := r.cl.List(ctx, &list, &client.ListOptions{}); err != nil {
		log.Error(err, "Listing ReplicatedVolumeReplica")
		return reconcile.Result{}, err
	}

	log.V(2).Info("Removing unrelated items")
	list.Items = slices.DeleteFunc(list.Items, func(rvr v1alpha3.ReplicatedVolumeReplica) bool {
		if !metav1.IsControlledBy(&rvr, &rv) {
			return true
		}

		log := log.WithValues("rvr", rvr)

		if rvr.Spec.NodeName == "" {
			log.V(2).Info("No node name. Skipping")
			return true
		}

		if rvr.Status == nil || rvr.Status.Config == nil {
			log.V(2).Info("No status.config. Skipping")
			return true
		}

		if rvr.Status.Config.NodeId == nil {
			log.V(2).Info("No status.config.nodId. Skipping")
			return true
		}

		if rvr.Status.Config.Address == nil {
			log.V(2).Info("No status.config.address. Skipping")
			return true
		}

		if _, exist := peers[rvr.Spec.NodeName]; exist {
			log.Info("Peer on this node already found. Skipping")
			return true
		}
		return false
	})

	peers := make(map[string]v1alpha3.Peer, len(list.Items))
	for _, rvr := range list.Items {
		peers[rvr.Spec.NodeName] = v1alpha3.Peer{
			NodeId:   *rvr.Status.Config.NodeId,
			Address:  *rvr.Status.Config.Address,
			Diskless: rvr.Spec.Diskless,
		}
	}

	log.Info("Filtered peers", "peers", peers)

	for _, rvr := range list.Items {
		log := log.WithValues("rvr", rvr)

		peersWithoutSelf := maps.Clone(peers)
		delete(peersWithoutSelf, rvr.Spec.NodeName)

		if maps.Equal(peersWithoutSelf, rvr.Status.Config.Peers) {
			log.V(1).Info("not changed")
			continue
		}

		from := client.MergeFrom(rvr.DeepCopy())
		rvr.Status.Config.Peers = peers
		if err := r.cl.Patch(ctx, &rv, from); err != nil {
			log.Error(err, "Patching ReplicatedVolume")
			return reconcile.Result{}, client.IgnoreNotFound(err)
		}
		log.Info("Patched with new peers", "peers", peers)
	}

	return reconcile.Result{}, nil
}
