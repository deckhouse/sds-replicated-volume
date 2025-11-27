package rvrdiskfulcount

import (
	"context"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type Handler struct {
	cl  client.Client
	log logr.Logger
}

func (h *Handler) HandleReplicatedVolumeReplica(ctx context.Context, obj client.Object) (request []Request) {
	log := h.log.WithName("HandleReplicatedVolumeReplica").WithValues("obj", obj)
	rvr := obj.(*v1alpha3.ReplicatedVolumeReplica)
	if rvr == nil {
		log.Info("Can't convert to v1alpha3.ReplicatedVolumeReplica")
		return
	}

	if rvr.Spec.ReplicatedVolumeName == "" {
		log.Info("ReplicatedVolumeName is empty")
		return
	}

	if rvr.IsReady() {
		log.Info("ReplicatedVolumeName is ready. Scheduling")
		request = append(request, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(rvr)})
	}

	log.V(1).Info("Listing other replicas")
	var list v1alpha3.ReplicatedVolumeReplicaList
	if err := h.cl.List(ctx, &list); err != nil {
		log.Error(err, "Listing ReplicatedVolumeReplica")
		return
	}

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

		log.V(1).Info("Scheduling")
		request = append(request, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&item)})
	}
	return
}
