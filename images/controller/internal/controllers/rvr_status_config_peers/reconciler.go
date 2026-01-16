/*
Copyright 2026 Flant JSC

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

package rvrstatusconfigpeers

import (
	"context"
	"errors"
	"maps"
	"slices"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes"
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

	var rv v1alpha1.ReplicatedVolume
	if err := r.cl.Get(ctx, req.NamespacedName, &rv); err != nil {
		if client.IgnoreNotFound(err) == nil {
			log.V(1).Info("ReplicatedVolume not found, probably deleted")
			return reconcile.Result{}, nil
		}
		log.Error(err, "Can't get ReplicatedVolume")
		return reconcile.Result{}, err
	}

	if !obju.HasFinalizer(&rv, v1alpha1.ControllerFinalizer) {
		log.Info("ReplicatedVolume does not have controller finalizer, skipping")
		return reconcile.Result{}, nil
	}

	log.V(1).Info("Listing replicas")
	var list v1alpha1.ReplicatedVolumeReplicaList
	if err := r.cl.List(ctx, &list, client.MatchingFields{
		indexes.IndexFieldRVRByReplicatedVolumeName: rv.Name,
	}); err != nil {
		log.Error(err, "Listing ReplicatedVolumeReplica")
		return reconcile.Result{}, err
	}

	log.V(2).Info("Removing items without required status fields")
	list.Items = slices.DeleteFunc(list.Items, func(rvr v1alpha1.ReplicatedVolumeReplica) bool {
		log := log.WithValues("rvr", rvr)

		if rvr.Spec.NodeName == "" {
			log.V(2).Info("No node name. Skipping")
			return true
		}

		if rvr.Status.DRBD == nil || rvr.Status.DRBD.Config == nil {
			log.V(2).Info("No status.drbd.config. Skipping")
			return true
		}

		if rvr.Status.DRBD.Config.Address == nil {
			log.V(2).Info("No status.drbd.config.address. Skipping")
			return true
		}

		return false
	})

	peers := make(map[string]v1alpha1.Peer, len(list.Items))
	for _, rvr := range list.Items {
		if _, exist := peers[rvr.Spec.NodeName]; exist {
			log.Error(ErrMultiplePeersOnSameNode, "Can't build peers map")
			return reconcile.Result{}, ErrMultiplePeersOnSameNode
		}
		nodeID, _ := rvr.NodeID()
		peers[rvr.Spec.NodeName] = v1alpha1.Peer{
			NodeId:   nodeID,
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
