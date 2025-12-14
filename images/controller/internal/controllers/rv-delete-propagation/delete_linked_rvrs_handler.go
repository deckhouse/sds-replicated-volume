package rvdeletepropagation

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type DeleteLinkedRVRsHandler struct {
	cl     client.Client
	log    *slog.Logger
	rvName string
}

func linkedRVRsNeedToBeDeleted(rv *v1alpha3.ReplicatedVolume) bool {
	return rv.DeletionTimestamp == nil
}

func (h *DeleteLinkedRVRsHandler) Handle(ctx context.Context) error {
	rvrList := &v1alpha3.ReplicatedVolumeReplicaList{}
	if err := h.cl.List(ctx, rvrList); err != nil {
		return fmt.Errorf("listing rvrs: %w", err)
	}

	for i := range rvrList.Items {
		rvr := &rvrList.Items[i]
		if rvr.Spec.ReplicatedVolumeName == h.rvName && rvr.DeletionTimestamp == nil {

			if err := h.cl.Delete(ctx, rvr); err != nil {
				return fmt.Errorf("deleting rvr: %w", err)
			}

			h.log.Info("deleted rvr", "rvrName", rvr.Name)
		}
	}

	h.log.Info("finished rvr deletion")

	return nil
}
