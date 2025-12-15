package drbdconfig

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdadm"
	"github.com/spf13/afero"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type DownHandler struct {
	cl  client.Client
	log *slog.Logger
	rvr *v1alpha3.ReplicatedVolumeReplica
	llv *snc.LVMLogicalVolume // will be nil for rvr.spec.type != "Diskful" or for non-initialized RVR
}

func (h *DownHandler) Handle(ctx context.Context) error {
	for _, f := range h.rvr.Finalizers {
		if f != v1alpha3.AgentAppFinalizer {
			h.log.Info("non-agent finalizer found, ignore", "rvrName", h.rvr.Name)
			return nil
		}
	}

	rvName := h.rvr.Spec.ReplicatedVolumeName
	regularFilePath, tmpFilePath := FilePaths(rvName)

	if err := drbdadm.ExecuteDown(ctx, h.rvr.Spec.ReplicatedVolumeName); err != nil {
		h.log.Warn("failed to bring down DRBD resource", "resource", h.rvr.Spec.ReplicatedVolumeName, "error", err)
	} else {
		h.log.Info("successfully brought down DRBD resource", "resource", h.rvr.Spec.ReplicatedVolumeName)
	}

	if err := FS.Remove(regularFilePath); err != nil {
		if !errors.Is(err, afero.ErrFileNotFound) {
			h.log.Warn("failed to remove config file", "path", regularFilePath, "error", err)
		}
	} else {
		h.log.Info("successfully removed config file", "path", regularFilePath)
	}

	if err := FS.Remove(tmpFilePath); err != nil {
		if !errors.Is(err, afero.ErrFileNotFound) {
			h.log.Warn("failed to remove config file", "path", tmpFilePath, "error", err)
		}
	} else {
		h.log.Info("successfully removed config file", "path", tmpFilePath)
	}

	// remove finalizer to unblock deletion
	if err := h.removeFinalizerFromLLV(ctx); err != nil {
		return err
	}
	if err := h.removeFinalizerFromRVR(ctx); err != nil {
		return err
	}
	return nil
}

func (h *DownHandler) removeFinalizerFromRVR(ctx context.Context) error {
	patch := client.MergeFrom(h.rvr.DeepCopy())
	h.rvr.SetFinalizers(nil)
	if err := h.cl.Patch(ctx, h.rvr, patch); err != nil {
		return fmt.Errorf("patching rvr finalizers: %w", err)
	}
	return nil
}

func (h *DownHandler) removeFinalizerFromLLV(ctx context.Context) error {
	patch := client.MergeFrom(h.llv.DeepCopy())
	h.llv.SetFinalizers(nil)
	if err := h.cl.Patch(ctx, h.llv, patch); err != nil {
		return fmt.Errorf("patching llv finalizers: %w", err)
	}
	return nil
}
