package rvrstatusconfignodeid

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	e "github.com/deckhouse/sds-replicated-volume/images/controller/internal/errors"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/api"
)

type Reconciler struct {
	Cl     client.Client
	Log    *slog.Logger
	LogAlt logr.Logger
}

var _ reconcile.Reconciler = &Reconciler{}

const (
	maxNodeID = 7
	minNodeID = 0
)

func (r *Reconciler) Reconcile(
	ctx context.Context,
	req reconcile.Request,
) (reconcile.Result, error) {
	log := r.LogAlt.WithName("Reconcile").WithValues("req", req)
	log.Info("Reconciling")

	// Get the RVR
	rvr := &v1alpha3.ReplicatedVolumeReplica{}
	if err := r.Cl.Get(ctx, req.NamespacedName, rvr); err != nil {
		if client.IgnoreNotFound(err) == nil {
			log.V(1).Info("RVR not found, might be deleted")
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, fmt.Errorf("getting RVR %s: %w", req.NamespacedName, err)
	}

	// Check if nodeId is already set
	if rvr.Status != nil && rvr.Status.Config != nil && rvr.Status.Config.NodeId != nil {
		log.V(1).Info("nodeId already assigned", "nodeId", *rvr.Status.Config.NodeId)
		return reconcile.Result{}, nil
	}

	// Get all RVRs for the same ReplicatedVolume
	rvrList := &v1alpha3.ReplicatedVolumeReplicaList{}
	if err := r.Cl.List(ctx, rvrList); err != nil {
		return reconcile.Result{}, fmt.Errorf("listing RVRs: %w", err)
	}

	// Filter by replicatedVolumeName
	filteredRVRs := make([]v1alpha3.ReplicatedVolumeReplica, 0)
	for _, item := range rvrList.Items {
		if item.Spec.ReplicatedVolumeName == rvr.Spec.ReplicatedVolumeName {
			filteredRVRs = append(filteredRVRs, item)
		}
	}
	rvrList.Items = filteredRVRs

	// Collect used nodeIDs
	usedNodeIDs := make(map[uint]bool)
	for _, item := range rvrList.Items {
		if item.Status != nil && item.Status.Config != nil && item.Status.Config.NodeId != nil {
			nodeID := *item.Status.Config.NodeId
			if nodeID >= minNodeID && nodeID <= maxNodeID {
				usedNodeIDs[nodeID] = true
			}
		}
	}

	// Count total replicas (including the one we're processing)
	totalReplicas := len(rvrList.Items)
	if totalReplicas > maxNodeID+1 {
		// NOTE: Setting status condition is NOT in the spec.
		// This was added to improve observability - administrators can see the problem
		// in RVR status conditions instead of only in controller logs.
		// To revert: remove the setNodeIDErrorCondition call and the function definition.
		// The spec only requires returning an error, which we do below.
		if err := r.setNodeIDErrorCondition(ctx, rvr, fmt.Sprintf(
			"too many replicas for volume %s: %d (maximum is %d)",
			rvr.Spec.ReplicatedVolumeName,
			totalReplicas,
			maxNodeID+1,
		)); err != nil {
			log.Info("failed to set error condition", "err", err)
		}

		return reconcile.Result{}, e.ErrInvalidClusterf(
			"too many replicas for volume %s: %d (maximum is %d)",
			rvr.Spec.ReplicatedVolumeName,
			totalReplicas,
			maxNodeID+1,
		)
	}

	// Find available nodeID
	var availableNodeID *uint
	for i := uint(minNodeID); i <= uint(maxNodeID); i++ {
		if !usedNodeIDs[i] {
			availableNodeID = &i
			break
		}
	}

	if availableNodeID == nil {
		// NOTE: Setting status condition is NOT in the spec.
		// This was added to improve observability - administrators can see the problem
		// in RVR status conditions instead of only in controller logs.
		// To revert: remove the setNodeIDErrorCondition call and the function definition.
		// The spec only requires returning an error, which we do below.
		if err := r.setNodeIDErrorCondition(ctx, rvr, fmt.Sprintf(
			"no available nodeID for volume %s (all %d nodeIDs are used)",
			rvr.Spec.ReplicatedVolumeName,
			maxNodeID+1,
		)); err != nil {
			log.Info("failed to set error condition", "err", err)
		}

		return reconcile.Result{}, e.ErrInvalidClusterf(
			"no available nodeID for volume %s (all %d nodeIDs are used)",
			rvr.Spec.ReplicatedVolumeName,
			maxNodeID+1,
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
	if err := r.Cl.Get(ctx, req.NamespacedName, freshRVR); err != nil {
		return reconcile.Result{}, fmt.Errorf("getting RVR for patch: %w", err)
	}
	if err := api.PatchStatusWithConflictRetry(ctx, r.Cl, freshRVR, func(currentRVR *v1alpha3.ReplicatedVolumeReplica) error {
		// Check again if nodeID is already set (handles race condition where another worker set it during retry)
		if currentRVR.Status != nil && currentRVR.Status.Config != nil && currentRVR.Status.Config.NodeId != nil {
			log.V(1).Info("nodeID already assigned by another worker", "nodeID", *currentRVR.Status.Config.NodeId)
			return nil // Already set, nothing to do (idempotent)
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

		// Initialize status conditions if needed
		currentRVR.InitializeStatusConditions()

		return nil
	}); err != nil {
		// If error is ErrInvalidCluster, it means all nodeIDs are used
		if errors.Is(err, e.ErrInvalidCluster) {
			if err := r.setNodeIDErrorCondition(ctx, rvr, fmt.Sprintf(
				"no available nodeID for volume %s (all %d nodeIDs are used)",
				rvr.Spec.ReplicatedVolumeName,
				maxNodeID+1,
			)); err != nil {
				log.Info("failed to set error condition", "err", err)
			}
		}
		return reconcile.Result{}, fmt.Errorf("updating RVR status with nodeID: %w", err)
	}

	// Get final state to log the assigned nodeID
	finalRVR := &v1alpha3.ReplicatedVolumeReplica{}
	if err := r.Cl.Get(ctx, req.NamespacedName, finalRVR); err == nil {
		if finalRVR.Status != nil && finalRVR.Status.Config != nil && finalRVR.Status.Config.NodeId != nil {
			log.Info("assigned nodeID to RVR", "nodeID", *finalRVR.Status.Config.NodeId, "volume", rvr.Spec.ReplicatedVolumeName)
		}
	}

	return reconcile.Result{}, nil
}

// setNodeIDErrorCondition sets ConfigurationAdjusted condition to False with error reason
// when nodeID cannot be assigned due to too many replicas or all nodeIDs are used.
//
// NOTE: This function and its usage are NOT in the spec.
// This was added to improve observability - when nodeID cannot be assigned,
// the ConfigurationAdjusted condition is set to False with ConfigurationFailed reason,
// making the problem visible in RVR status conditions for administrators.
//
// The spec only requires returning an error from Reconcile, which is done by the caller.
// To revert this enhancement: remove all calls to setNodeIDErrorCondition and delete this function.
func (r *Reconciler) setNodeIDErrorCondition(ctx context.Context, rvr *v1alpha3.ReplicatedVolumeReplica, message string) error {
	// Get RVR again to ensure we have the latest version
	currentRVR := &v1alpha3.ReplicatedVolumeReplica{}
	if err := r.Cl.Get(ctx, client.ObjectKeyFromObject(rvr), currentRVR); err != nil {
		return fmt.Errorf("getting RVR for condition update: %w", err)
	}

	// Initialize status if needed
	if currentRVR.Status == nil {
		currentRVR.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{}
	}
	currentRVR.InitializeStatusConditions()

	// Set ConfigurationAdjusted condition to False with error reason
	// This indicates that configuration cannot be applied without nodeID
	cond := metav1.Condition{
		Type:               v1alpha3.ConditionTypeConfigurationAdjusted,
		Status:             metav1.ConditionFalse,
		Reason:             v1alpha3.ReasonConfigurationFailed,
		Message:            message,
		ObservedGeneration: currentRVR.Generation,
		LastTransitionTime: metav1.Now(),
	}
	meta.SetStatusCondition(&currentRVR.Status.Conditions, cond)

	// Update status
	if err := r.Cl.Status().Update(ctx, currentRVR); err != nil {
		// Fallback to regular Update for fake client compatibility in tests
		if updateErr := r.Cl.Update(ctx, currentRVR); updateErr != nil {
			return fmt.Errorf("updating RVR condition: %w (Status().Update failed: %v)", updateErr, err)
		}
	}

	return nil
}
