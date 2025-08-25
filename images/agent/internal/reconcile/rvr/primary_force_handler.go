package rvr

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha2"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdadm"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type resourcePrimaryForceRequestHandler struct {
	ctx      context.Context
	log      *slog.Logger
	cl       client.Client
	nodeName string
	rvr      *v1alpha2.ReplicatedVolumeReplica
}

func (h *resourcePrimaryForceRequestHandler) Handle() error {
	if h.rvr.Spec.NodeName != h.nodeName {
		return fmt.Errorf("expected spec.nodeName to be %s, got %s", h.nodeName, h.rvr.Spec.NodeName)
	}

	ann := h.rvr.GetAnnotations()
	if ann[v1alpha2.AnnotationKeyPrimaryForce] == "" {
		h.log.Warn("primary-force annotation no longer present; skipping", "name", h.rvr.Name)
		return nil
	}

	if err := drbdadm.ExecutePrimaryForce(h.ctx, h.rvr.Spec.ReplicatedVolumeName); err != nil {
		h.log.Error("failed to force promote to primary", "error", err)
		return fmt.Errorf("drbdadm primary --force: %w", err)
	}

	// demote back to secondary unless desired primary in spec
	if !h.rvr.Spec.Primary {
		if err := drbdadm.ExecuteSecondary(h.ctx, h.rvr.Spec.ReplicatedVolumeName); err != nil {
			h.log.Error("failed to demote to secondary after forced promotion", "error", err)
			return fmt.Errorf("drbdadm secondary: %w", err)
		}
	}

	// remove the annotation to mark completion
	patch := client.MergeFrom(h.rvr.DeepCopy())
	ann = h.rvr.GetAnnotations()
	delete(ann, v1alpha2.AnnotationKeyPrimaryForce)
	h.rvr.SetAnnotations(ann)
	if err := h.cl.Patch(h.ctx, h.rvr, patch); err != nil {
		h.log.Error("failed to remove primary-force annotation", "name", h.rvr.Name, "error", err)
		return fmt.Errorf("removing primary-force annotation: %w", err)
	}

	h.log.Info("successfully handled primary-force request")
	return nil
}
