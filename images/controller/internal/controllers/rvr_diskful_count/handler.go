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

// filter events for ReplicatedVolumeReplica
func (h *Handler) HandleReplicatedVolumeReplica(ctx context.Context, obj client.Object) (request []Request) {
	log := h.log.WithName("HandleReplicatedVolumeReplica").WithValues("obj", obj)
	rvr := obj.(*v1alpha3.ReplicatedVolumeReplica)
	if rvr == nil {
		log.Info("Can't convert to v1alpha3.ReplicatedVolumeReplica")
		return
	}

	request = append(request, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(rvr)})
	return
}

// filter events for ReplicatedVolume
func (h *Handler) HandleReplicatedVolume(ctx context.Context, obj client.Object) (request []Request) {
	log := h.log.WithName("HandleReplicatedVolume").WithValues("obj", obj)
	rv := obj.(*v1alpha3.ReplicatedVolume)
	if rv == nil {
		log.Info("Can't convert to v1alpha3.ReplicatedVolume")
		return
	}

	request = append(request, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(rv)})
	return
}
