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

package rvr

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdadm"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/api"
)

type resourceDeleteRequestHandler struct {
	ctx      context.Context
	log      *slog.Logger
	cl       client.Client
	nodeName string
	rvr      *v1alpha2.ReplicatedVolumeReplica
}

func (h *resourceDeleteRequestHandler) Handle() error {
	if err := drbdadm.ExecuteDown(h.ctx, h.rvr.Spec.ReplicatedVolumeName); err != nil {
		h.log.Warn("failed to bring down DRBD resource", "resource", h.rvr.Spec.ReplicatedVolumeName, "error", err)
	} else {
		h.log.Info("successfully brought down DRBD resource", "resource", h.rvr.Spec.ReplicatedVolumeName)
	}

	configPath := filepath.Join(resourcesDir, h.rvr.Spec.ReplicatedVolumeName+".res")
	if err := os.Remove(configPath); err != nil {
		if !os.IsNotExist(err) {
			h.log.Warn("failed to remove config file", "path", configPath, "error", err)
		}
	} else {
		h.log.Info("successfully removed config file", "path", configPath)
	}

	// remove finalizer to unblock deletion
	if err := api.PatchWithConflictRetry(
		h.ctx, h.cl, h.rvr,
		func(obj *v1alpha2.ReplicatedVolumeReplica) error {
			obj.Finalizers = slices.DeleteFunc(
				obj.Finalizers,
				func(f string) bool { return f == rvrFinalizerName },
			)
			return nil
		},
	); err != nil {
		return fmt.Errorf("removing finalizer: %w", err)
	}

	return nil
}
