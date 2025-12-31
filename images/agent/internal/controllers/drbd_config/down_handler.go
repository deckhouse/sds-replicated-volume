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

package drbdconfig

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/spf13/afero"
	"sigs.k8s.io/controller-runtime/pkg/client"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdadm"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdsetup"
)

type DownHandler struct {
	cl  client.Client
	log *slog.Logger
	rvr *v1alpha1.ReplicatedVolumeReplica
	llv *snc.LVMLogicalVolume // will be nil for non-diskful or non-initialized replicas
}

func (h *DownHandler) Handle(ctx context.Context) error {
	for _, f := range h.rvr.Finalizers {
		if f != v1alpha1.AgentAppFinalizer {
			h.log.Info("non-agent finalizer found, ignore", "rvrName", h.rvr.Name)
			return nil
		}
	}

	rvName := h.rvr.Spec.ReplicatedVolumeName
	regularFilePath, tmpFilePath := FilePaths(h.rvr.Name)

	// Try drbdadm first (uses config file)
	if err := drbdadm.ExecuteDown(ctx, rvName); err != nil {
		h.log.Warn("drbdadm down failed, trying drbdsetup down", "resource", rvName, "error", err)
		// Fallback to drbdsetup (doesn't need config file)
		if err := drbdsetup.ExecuteDown(ctx, rvName); err != nil {
			return fmt.Errorf("failed to bring down DRBD resource %s: %w", rvName, err)
		}
		h.log.Info("successfully brought down DRBD resource via drbdsetup", "resource", rvName)
	} else {
		h.log.Info("successfully brought down DRBD resource", "resource", rvName)
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
	if !slices.Contains(h.rvr.Finalizers, v1alpha1.AgentAppFinalizer) {
		return nil
	}
	patch := client.MergeFrom(h.rvr.DeepCopy())
	h.rvr.Finalizers = slices.DeleteFunc(h.rvr.Finalizers, func(f string) bool {
		return f == v1alpha1.AgentAppFinalizer
	})
	if err := h.cl.Patch(ctx, h.rvr, patch); err != nil {
		return fmt.Errorf("patching rvr finalizers: %w", err)
	}
	return nil
}

func (h *DownHandler) removeFinalizerFromLLV(ctx context.Context) error {
	if h.llv == nil {
		return nil
	}
	if !slices.Contains(h.llv.Finalizers, v1alpha1.AgentAppFinalizer) {
		return nil
	}
	patch := client.MergeFrom(h.llv.DeepCopy())
	h.llv.Finalizers = slices.DeleteFunc(h.llv.Finalizers, func(f string) bool {
		return f == v1alpha1.AgentAppFinalizer
	})
	if err := h.cl.Patch(ctx, h.llv, patch); err != nil {
		return fmt.Errorf("patching llv finalizers: %w", err)
	}
	return nil
}
