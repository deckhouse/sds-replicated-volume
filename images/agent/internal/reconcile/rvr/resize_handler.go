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

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdadm"
)

type resourceResizeRequestHandler struct {
	ctx      context.Context
	log      *slog.Logger
	cl       client.Client
	nodeName string
	rvr      *v1alpha2.ReplicatedVolumeReplica
}

func (h *resourceResizeRequestHandler) Handle() error {
	ann := h.rvr.GetAnnotations()
	if ann[v1alpha2.AnnotationKeyNeedResize] == "" {
		h.log.Warn("need-resize annotation no longer present; skipping", "name", h.rvr.Name)
		return nil
	}

	if err := drbdadm.ExecuteResize(h.ctx, h.rvr.Spec.ReplicatedVolumeName); err != nil {
		h.log.Error("failed to resize DRBD resource", "error", err)
		return fmt.Errorf("drbdadm resize: %w", err)
	}

	// remove the annotation to mark completion
	patch := client.MergeFrom(h.rvr.DeepCopy())
	delete(ann, v1alpha2.AnnotationKeyNeedResize)
	h.rvr.SetAnnotations(ann)
	if err := h.cl.Patch(h.ctx, h.rvr, patch); err != nil {
		h.log.Error("failed to remove need-resize annotation", "name", h.rvr.Name, "error", err)
		return fmt.Errorf("removing need-resize annotation: %w", err)
	}

	h.log.Info("successfully resized DRBD resource")
	return nil
}
