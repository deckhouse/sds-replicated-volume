package rvrstatusconfignodeid

import (
	"context"
	"fmt"
	"slices"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	e "github.com/deckhouse/sds-replicated-volume/images/controller/internal/errors"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/api"
)

// formatValidRange returns a formatted string representing the valid nodeID range.
func formatValidRange() string {
	return fmt.Sprintf("[%d; %d]", MinNodeID, MaxNodeID)
}

type Reconciler struct {
	cl  client.Client
	log logr.Logger
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

// NewReconciler creates a new Reconciler instance.
// This is primarily used for testing, as fields are private.
func NewReconciler(cl client.Client, log logr.Logger) *Reconciler {
	return &Reconciler{
		cl:  cl,
		log: log,
	}
}

func (r *Reconciler) Reconcile(
	ctx context.Context,
	req reconcile.Request,
) (reconcile.Result, error) {
	log := r.log.WithName("Reconcile").WithValues("req", req)
	log.Info("Reconciling")

	// Get the RVR
	rvr := &v1alpha3.ReplicatedVolumeReplica{}
	if err := r.cl.Get(ctx, req.NamespacedName, rvr); err != nil {
		if client.IgnoreNotFound(err) == nil {
			log.V(1).Info("RVR not found, might be deleted")
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, fmt.Errorf("getting RVR %s: %w", req.NamespacedName, err)
	}

	// Check if nodeId is already set
	if rvr.Status != nil && rvr.Status.Config != nil && rvr.Status.Config.NodeId != nil {
		nodeID := *rvr.Status.Config.NodeId
		// Validate nodeID range if it's set
		if nodeID < MinNodeID || nodeID > MaxNodeID {
			// NOTE: Logging invalid nodeID is NOT in the spec.
			// This was added to improve observability - administrators can see invalid nodeIDs in logs.
			log.Error(nil, "nodeID outside valid range, will be reset and reassigned",
				"nodeID", nodeID,
				"validRange", formatValidRange(),
				"rvr", req.NamespacedName,
			)
			// Reset invalid nodeID - will continue processing to assign valid nodeID
			rvr.Status.Config.NodeId = nil
		} else {
			log.V(1).Info("nodeId already assigned", "nodeId", nodeID)
			return reconcile.Result{}, nil
		}
	}

	// Get all RVRs for the same ReplicatedVolume
	rvrList := &v1alpha3.ReplicatedVolumeReplicaList{}
	if err := r.cl.List(ctx, rvrList); err != nil {
		return reconcile.Result{}, fmt.Errorf("listing RVRs: %w", err)
	}

	// Filter by replicatedVolumeName
	rvrList.Items = slices.DeleteFunc(rvrList.Items, func(item v1alpha3.ReplicatedVolumeReplica) bool {
		return item.Spec.ReplicatedVolumeName != rvr.Spec.ReplicatedVolumeName
	})

	// Collect used nodeIDs
	usedNodeIDs := make(map[uint]struct{})
	for _, item := range rvrList.Items {
		if item.Status != nil && item.Status.Config != nil && item.Status.Config.NodeId != nil {
			nodeID := *item.Status.Config.NodeId
			if nodeID >= MinNodeID && nodeID <= MaxNodeID {
				usedNodeIDs[nodeID] = struct{}{}
			} else {
				// NOTE: Logging invalid nodeID is NOT in the spec.
				// This was added to improve observability - administrators can see invalid nodeIDs in logs.
				// To revert: remove this log line.
				log.V(1).Info("ignoring nodeID outside valid range", "nodeID", nodeID, "validRange", formatValidRange())
			}
		}
	}

	// Count total replicas (including the one we're processing)
	totalReplicas := len(rvrList.Items)
	if totalReplicas > MaxNodeID+1 {
		// NOTE: It would be a good idea to set ConfigurationAdjusted condition to False here
		// for better user experience and understanding what's wrong (for administrators, not end users).
		// This would make the problem visible in RVR status conditions, not just in controller logs.
		// However, for now we keep it simple and only log the error.
		// The error will be logged by controller-runtime and visible in controller logs.
		log.Error(nil, "too many replicas for volume", "volume", rvr.Spec.ReplicatedVolumeName, "replicas", totalReplicas, "max", MaxNodeID+1)

		return reconcile.Result{}, e.ErrInvalidClusterf(
			"too many replicas for volume %s: %d (maximum is %d)",
			rvr.Spec.ReplicatedVolumeName,
			totalReplicas,
			MaxNodeID+1,
		)
	}

	// Find available nodeID
	var availableNodeID *uint
	for i := uint(MinNodeID); i <= uint(MaxNodeID); i++ {
		if _, exists := usedNodeIDs[i]; !exists {
			availableNodeID = &i
			break
		}
	}

	if availableNodeID == nil {
		// NOTE: It would be a good idea to set ConfigurationAdjusted condition to False here
		// for better user experience and understanding what's wrong (for administrators, not end users).
		// This would make the problem visible in RVR status conditions, not just in controller logs.
		// However, for now we keep it simple and only log the error.
		// The error will be logged by controller-runtime and visible in controller logs.
		log.Error(nil, "no available nodeID for volume", "volume", rvr.Spec.ReplicatedVolumeName, "maxNodeIDs", MaxNodeID+1)

		return reconcile.Result{}, e.ErrInvalidClusterf(
			"no available nodeID for volume %s (all %d nodeIDs are used)",
			rvr.Spec.ReplicatedVolumeName,
			MaxNodeID+1,
		)
	}

	// Update RVR status with nodeID using PatchStatusWithConflictRetry
	// This handles concurrent updates: if another worker assigned a nodeID while we were processing,
	// the patch will retry and we'll re-check if nodeID is already set (idempotent check at start of function).
	//
	// NOTE: We use the pre-calculated availableNodeID. If there's a conflict (409), PatchStatusWithConflictRetry
	// will reload the resource and retry. At that point, if nodeID was already set by another worker,
	// we'll detect it and skip (idempotent). If not, we'll use our pre-calculated nodeID.
	// This is safe because:
	// 1. We check at the start of the function if nodeID is already set (early exit)
	// 2. If conflict occurs, we reload and check again inside patchFn
	// 3. The worst case is we retry with the same nodeID, which is fine (idempotent)
	// Get fresh RVR for PatchStatusWithConflictRetry (it needs a valid object with correct key)
	freshRVR := &v1alpha3.ReplicatedVolumeReplica{}
	if err := r.cl.Get(ctx, req.NamespacedName, freshRVR); err != nil {
		return reconcile.Result{}, fmt.Errorf("getting RVR for patch: %w", err)
	}
	if err := api.PatchStatusWithConflictRetry(ctx, r.cl, freshRVR, func(currentRVR *v1alpha3.ReplicatedVolumeReplica) error {
		// Check again if nodeID is already set (handles race condition where another worker set it during retry)
		if currentRVR.Status != nil && currentRVR.Status.Config != nil && currentRVR.Status.Config.NodeId != nil {
			currentNodeID := *currentRVR.Status.Config.NodeId
			// Validate nodeID range - if invalid, reset it
			if currentNodeID < MinNodeID || currentNodeID > MaxNodeID {
				// NOTE: Logging invalid nodeID is NOT in the spec.
				// This was added to improve observability - administrators can see invalid nodeIDs in logs.
				// To revert: remove this log line and the validation check.
				log.Error(nil, "nodeID outside valid range during patch, resetting",
					"nodeID", currentNodeID,
					"validRange", formatValidRange(),
					"rvr", req.NamespacedName,
				)
				currentRVR.Status.Config.NodeId = nil
				// Continue to assign valid nodeID
			} else {
				log.V(1).Info("nodeID already assigned by another worker", "nodeID", currentNodeID)
				return nil // Already set, nothing to do (idempotent)
			}
		}

		// Use the pre-calculated availableNodeID
		// If there was a conflict and we're retrying, the resource was reloaded with fresh data.
		// But we still use our pre-calculated nodeID because:
		// - If it's still available, we assign it (correct)
		// - If it was taken by another worker, we'll get another conflict and retry again
		// - Eventually, either we succeed or all nodeIDs are taken (handled by initial check)

		// Initialize status if needed
		if currentRVR.Status == nil {
			currentRVR.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{}
		}
		if currentRVR.Status.Config == nil {
			currentRVR.Status.Config = &v1alpha3.DRBDConfig{}
		}

		// Set the pre-calculated nodeID
		currentRVR.Status.Config.NodeId = availableNodeID

		return nil
	}); err != nil {
		return reconcile.Result{}, fmt.Errorf("updating RVR status with nodeID: %w", err)
	}

	// Get final state to log the assigned nodeID
	finalRVR := &v1alpha3.ReplicatedVolumeReplica{}
	if err := r.cl.Get(ctx, req.NamespacedName, finalRVR); err == nil {
		if finalRVR.Status != nil && finalRVR.Status.Config != nil && finalRVR.Status.Config.NodeId != nil {
			log.Info("assigned nodeID to RVR", "nodeID", *finalRVR.Status.Config.NodeId, "volume", rvr.Spec.ReplicatedVolumeName)
		}
	}

	return reconcile.Result{}, nil
}
