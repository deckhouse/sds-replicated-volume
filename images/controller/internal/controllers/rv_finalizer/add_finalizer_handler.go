package rvfinalizer

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type AddFinalizerHandler struct {
	cl  client.Client
	log *slog.Logger
	rv  *v1alpha3.ReplicatedVolume
}

func rvNeedsFinalizer(rv *v1alpha3.ReplicatedVolume) bool {
	return rv.DeletionTimestamp == nil && !slices.Contains(rv.Finalizers, v1alpha3.ControllerAppFinalizer)
}

func (h *AddFinalizerHandler) Handle(ctx context.Context) error {
	if !rvNeedsFinalizer(h.rv) {
		h.log.Debug("finalizer not needed")
		return nil
	}

	patch := client.MergeFrom(h.rv.DeepCopy())

	h.rv.Finalizers = append(h.rv.Finalizers, v1alpha3.ControllerAppFinalizer)
	if err := h.cl.Patch(ctx, h.rv, patch); err != nil {
		return fmt.Errorf("patching rv finalizers: %w", err)
	}

	h.log.Info("finalizer added to rv")

	return nil
}
