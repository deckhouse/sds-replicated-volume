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

package rvr_status_config_peers

import (
	"context"
	"errors"
	"maps"
	"slices"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
)

type Reconciler struct {
	cl  client.Client
	log logr.Logger
}

type Request = reconcile.Request

var _ reconcile.Reconciler = (*Reconciler)(nil)
var (
	ErrMultiplePeersOnSameNode = errors.New("multiple peers on the same node")
)

func NewReconciler(cl client.Client, log logr.Logger) *Reconciler {
	return &Reconciler{
		cl:  cl,
		log: log,
	}
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
			log.V(4).Info("Not controlled by this ReplicatedVolume")
			return true
		}

		log := log.WithValues("rvr", rvr)

		if rvr.Spec.NodeName == "" {
			log.V(2).Info("No node name. Skipping")
			return true
		}

		if rvr.Status == nil || rvr.Status.DRBD == nil || rvr.Status.DRBD.Config == nil {
			log.V(2).Info("No status.drbd.config. Skipping")
			return true
		}

		if rvr.Status.DRBD.Config.NodeId == nil {
			log.V(2).Info("No status.drbd.config.nodId. Skipping")
			return true
		}

		if rvr.Status.DRBD.Config.Address == nil {
			log.V(2).Info("No status.drbd.config.address. Skipping")
			return true
		}

		return false
	})

	peers := make(map[string]v1alpha3.Peer, len(list.Items))
	for _, rvr := range list.Items {
		if _, exist := peers[rvr.Spec.NodeName]; exist {
			log.Error(ErrMultiplePeersOnSameNode, "Can't build peers map")
			return reconcile.Result{}, ErrMultiplePeersOnSameNode
		}
		peers[rvr.Spec.NodeName] = v1alpha3.Peer{
			NodeId:   *rvr.Status.DRBD.Config.NodeId,
			Address:  *rvr.Status.DRBD.Config.Address,
			Diskless: rvr.Spec.IsDiskless(),
		}
	}

	log.Info("Filtered peers", "peers", peers)

	for _, rvr := range list.Items {
		log := log.WithValues("rvr", rvr)

		peersWithoutSelf := maps.Clone(peers)
		delete(peersWithoutSelf, rvr.Spec.NodeName)

		peersChanged := !maps.Equal(peersWithoutSelf, rvr.Status.DRBD.Config.Peers)
		if !peersChanged && rvr.Status.DRBD.Config.PeersInitialized {
			log.V(1).Info("not changed")
			continue
		}

		from := client.MergeFrom(&rvr)
		changedRvr := rvr.DeepCopy()

		changedRvr.Status.DRBD.Config.Peers = peersWithoutSelf
		// After first initialization, even if there are no peers, set peersInitialized=true
		changedRvr.Status.DRBD.Config.PeersInitialized = true
		if err := r.cl.Status().Patch(ctx, changedRvr, from); err != nil {
			log.Error(err, "Patching ReplicatedVolumeReplica")
			return reconcile.Result{}, client.IgnoreNotFound(err)
		}
		log.Info("Patched with new peers", "peers", peersWithoutSelf, "peersInitialized", changedRvr.Status.DRBD.Config.PeersInitialized)
	}

	return reconcile.Result{}, nil
}
