package drbdconfig

import (
	"context"
	"log/slog"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type DownHandler struct {
	cl  client.Client
	log *slog.Logger
	rvr *v1alpha3.ReplicatedVolumeReplica
}

func (h *DownHandler) Handle(ctx context.Context) error {

	return nil
}
