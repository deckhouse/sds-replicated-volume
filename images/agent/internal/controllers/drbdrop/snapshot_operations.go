package drbdrop

import (
	"context"
	"fmt"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/controllers/drbdr"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdutils"
)

func (r *OperationReconciler) executeTrackBitmap(
	ctx context.Context,
	op *v1alpha1.DRBDResourceOperation,
	dr *v1alpha1.DRBDResource,
) error {
	peerNodeID, err := peerNodeIDFromOperation(op)
	if err != nil {
		return err
	}
	drbdResName := drbdr.DRBDResourceNameOnTheNode(dr)
	if err := drbdutils.ExecuteTrackBitmap(ctx, drbdResName, peerNodeID, true); err != nil {
		return fmt.Errorf("executing track-bitmap: %w", err)
	}
	return nil
}

func (r *OperationReconciler) executeUntrackBitmap(
	ctx context.Context,
	op *v1alpha1.DRBDResourceOperation,
	dr *v1alpha1.DRBDResource,
) error {
	peerNodeID, err := peerNodeIDFromOperation(op)
	if err != nil {
		return err
	}
	drbdResName := drbdr.DRBDResourceNameOnTheNode(dr)
	if err := drbdutils.ExecuteTrackBitmap(ctx, drbdResName, peerNodeID, false); err != nil {
		return fmt.Errorf("executing untrack-bitmap: %w", err)
	}
	return nil
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
	if err := drbdutils.ExecuteSuspendIO(ctx, minor); err != nil {
		return fmt.Errorf("executing suspend-io: %w", err)
	}
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
	if err := drbdutils.ExecuteResumeIO(ctx, minor); err != nil {
		return fmt.Errorf("executing resume-io: %w", err)
	}
	return nil
}

func peerNodeIDFromOperation(op *v1alpha1.DRBDResourceOperation) (uint8, error) {
	if op.Spec.PeerNodeID == nil {
		return 0, fmt.Errorf("peerNodeID is required")
	}
	return *op.Spec.PeerNodeID, nil
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
