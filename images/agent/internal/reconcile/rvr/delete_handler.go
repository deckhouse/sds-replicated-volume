package rvr

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdadm"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type resourceDeleteRequestHandler struct {
	ctx                  context.Context
	log                  *slog.Logger
	cl                   client.Client
	nodeName             string
	replicatedVolumeName string
}

func (h *resourceDeleteRequestHandler) Handle() error {
	if err := drbdadm.ExecuteDown(h.ctx, h.replicatedVolumeName); err != nil {
		h.log.Warn("failed to bring down DRBD resource", "resource", h.replicatedVolumeName, "error", err)
	} else {
		h.log.Info("successfully brought down DRBD resource", "resource", h.replicatedVolumeName)
	}

	configPath := filepath.Join(resourcesDir, h.replicatedVolumeName+".res")
	if err := os.Remove(configPath); err != nil {
		if !os.IsNotExist(err) {
			h.log.Warn("failed to remove config file", "path", configPath, "error", err)
		}
	} else {
		h.log.Info("successfully removed config file", "path", configPath)
	}

	return nil
}
