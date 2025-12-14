package rvfinalizer

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type RemoveFinalizerIfPossibleHandler struct {
	cl  client.Client
	log *slog.Logger
	rv  *v1alpha3.ReplicatedVolume
}

func rvFinalizerMayNeedToBeRemoved(rv *v1alpha3.ReplicatedVolume) bool {
	return rv.DeletionTimestamp != nil && slices.Contains(rv.Finalizers, v1alpha3.ControllerAppFinalizer)
}

func (h *RemoveFinalizerIfPossibleHandler) Handle(ctx context.Context) error {
	if !rvFinalizerMayNeedToBeRemoved(h.rv) {
		h.log.Debug("finalizer not need to be removed")
		return nil
	}

	rvrList := &v1alpha3.ReplicatedVolumeReplicaList{}
	if err := h.cl.List(ctx, rvrList); err != nil {
		return fmt.Errorf("listing rvrs: %w", err)
	}

	for i := range rvrList.Items {
		if rvrList.Items[i].Spec.ReplicatedVolumeName == h.rv.Name {
			h.log.Debug(
				"found rvr 'rvrName' linked to rv 'rvName', therefore skip removing finalizer from rv",
				"rvrName", rvrList.Items[i].Name,
			)
			return nil
		}
	}

	patch := client.MergeFrom(h.rv.DeepCopy())

	h.rv.Finalizers = slices.DeleteFunc(
		h.rv.Finalizers,
		func(f string) bool { return f == v1alpha3.ControllerAppFinalizer },
	)

	if err := h.cl.Patch(ctx, h.rv, patch); err != nil {
		return fmt.Errorf("patching rv finalizers: %w", err)
	}

	h.log.Info("finalizer removed")

	return nil
}
