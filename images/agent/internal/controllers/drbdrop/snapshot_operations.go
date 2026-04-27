package drbdrop

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/log"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/controllers/drbdr"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdutils"
)

func (r *OperationReconciler) executeBitmapTracking(
	ctx context.Context,
	op *v1alpha1.DRBDResourceOperation,
	dr *v1alpha1.DRBDResource,
) error {
	start := op.Spec.Type == v1alpha1.DRBDResourceOperationTrackBitmap
	action := "untrack-bitmap"
	if start {
		action = "track-bitmap"
	}
	drbdResName := drbdr.DRBDResourceNameOnTheNode(dr)
	l := log.FromContext(ctx)

	if op.Spec.PeerNodeID != nil {
		peerNodeID := *op.Spec.PeerNodeID
		l.Info("drbdrop: executing "+action, "resource", drbdResName, "peerNodeID", peerNodeID)
		if err := drbdutils.ExecuteTrackBitmap(ctx, drbdResName, peerNodeID, 0, start); err != nil {
			return fmt.Errorf("executing %s: %w", action, err)
		}
		return nil
	}

	peerIDs := uniquePeerNodeIDs(dr)
	if len(peerIDs) == 0 {
		l.Info("drbdrop: "+action+" has no peers, nothing to do", "resource", drbdResName)
		return nil
	}

	l.Info("drbdrop: executing "+action+" (all peers)", "resource", drbdResName, "peerNodeIDs", peerIDs)
	for _, peerNodeID := range peerIDs {
		if err := drbdutils.ExecuteTrackBitmap(ctx, drbdResName, peerNodeID, 0, start); err != nil {
			return fmt.Errorf("executing %s for peerNodeID=%d: %w", action, peerNodeID, err)
		}
	}
	return nil
}

func uniquePeerNodeIDs(dr *v1alpha1.DRBDResource) []uint8 {
	seen := make(map[uint8]struct{}, len(dr.Spec.Peers))
	out := make([]uint8, 0, len(dr.Spec.Peers))
	for i := range dr.Spec.Peers {
		p := &dr.Spec.Peers[i]
		if p.Type != v1alpha1.DRBDResourceTypeDiskful && p.Type != "" {
			continue
		}
		if _, ok := seen[p.NodeID]; ok {
			continue
		}
		seen[p.NodeID] = struct{}{}
		out = append(out, p.NodeID)
	}
	return out
}

func (r *OperationReconciler) executeFlushBitmap(
	ctx context.Context,
	_ *v1alpha1.DRBDResourceOperation,
	dr *v1alpha1.DRBDResource,
) error {
	minor, err := drbdMinor(ctx, dr)
	if err != nil {
		return err
	}
	log.FromContext(ctx).Info("drbdrop: executing flush-bitmap", "resource", drbdr.DRBDResourceNameOnTheNode(dr), "minor", minor)
	if err := drbdutils.ExecuteFlushBitmap(ctx, minor); err != nil {
		return fmt.Errorf("executing flush-bitmap: %w", err)
	}
	return nil
}

func (r *OperationReconciler) executeSuspendIO(
	ctx context.Context,
	_ *v1alpha1.DRBDResourceOperation,
	dr *v1alpha1.DRBDResource,
) error {
	minor, err := drbdMinor(ctx, dr)
	if err != nil {
		return err
	}
	log.FromContext(ctx).Info("drbdrop: executing suspend-io", "resource", drbdr.DRBDResourceNameOnTheNode(dr), "minor", minor)
	if err := drbdutils.ExecuteSuspendIO(ctx, minor); err != nil {
		return fmt.Errorf("executing suspend-io: %w", err)
	}
	log.FromContext(ctx).Info("drbdrop: suspend-io succeeded", "resource", drbdr.DRBDResourceNameOnTheNode(dr), "minor", minor)
	return nil
}

func (r *OperationReconciler) executeResumeIO(
	ctx context.Context,
	_ *v1alpha1.DRBDResourceOperation,
	dr *v1alpha1.DRBDResource,
) error {
	minor, err := drbdMinor(ctx, dr)
	if err != nil {
		return err
	}
	log.FromContext(ctx).Info("drbdrop: executing resume-io", "resource", drbdr.DRBDResourceNameOnTheNode(dr), "minor", minor)
	if err := drbdutils.ExecuteResumeIO(ctx, minor); err != nil {
		return fmt.Errorf("executing resume-io: %w", err)
	}
	log.FromContext(ctx).Info("drbdrop: resume-io succeeded", "resource", drbdr.DRBDResourceNameOnTheNode(dr), "minor", minor)
	return nil
}

func drbdMinor(ctx context.Context, dr *v1alpha1.DRBDResource) (uint, error) {
	drbdResName := drbdr.DRBDResourceNameOnTheNode(dr)
	statusResult, err := drbdutils.ExecuteStatus(ctx, drbdResName)
	if err != nil {
		return 0, fmt.Errorf("querying DRBD status: %w", err)
	}
	if len(statusResult) == 0 {
		return 0, fmt.Errorf("DRBD resource %q does not exist", drbdResName)
	}

	devices := statusResult[0].Devices
	if len(devices) == 0 {
		return 0, fmt.Errorf("DRBD resource %q has no volumes", drbdResName)
	}

	return uint(devices[0].Minor), nil
}
