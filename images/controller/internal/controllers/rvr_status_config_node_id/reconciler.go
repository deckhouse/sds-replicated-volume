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
)

// formatValidRange returns a formatted string representing the valid nodeID range.
func formatValidRange() string {
	return fmt.Sprintf("[%d; %d]", MinNodeID, MaxNodeID)
}

// isValidNodeID checks if nodeID is within valid range [MinNodeID; MaxNodeID].
func isValidNodeID(nodeID uint) bool {
	return nodeID >= MinNodeID && nodeID <= MaxNodeID
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
	var rvr v1alpha3.ReplicatedVolumeReplica
	if err := r.cl.Get(ctx, req.NamespacedName, &rvr); err != nil {
		log.Error(err, "Getting ReplicatedVolumeReplica")
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	// Check if nodeId is already set
	if rvr.Status != nil && rvr.Status.DRBD != nil && rvr.Status.DRBD.Config != nil && rvr.Status.DRBD.Config.NodeId != nil {
		nodeID := *rvr.Status.DRBD.Config.NodeId
		if isValidNodeID(nodeID) {
			log.V(1).Info("nodeId already assigned", "nodeId", nodeID)
			return reconcile.Result{}, nil
		}
		// NOTE: Logging invalid nodeID is NOT in the spec.
		// This was added to improve observability - administrators can see invalid nodeIDs in logs.
		log.V(0).Info("nodeID outside valid range, will be reset and reassigned",
			"nodeID", nodeID,
			"validRange", formatValidRange(),
			"rvr", req.NamespacedName,
		)
		// Reset invalid nodeID - will continue processing to assign valid nodeID
		rvr.Status.DRBD.Config.NodeId = nil
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
		if item.Status != nil && item.Status.DRBD != nil && item.Status.DRBD.Config != nil && item.Status.DRBD.Config.NodeId != nil {
			nodeID := *item.Status.DRBD.Config.NodeId
			if isValidNodeID(nodeID) {
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

	// Update RVR status with nodeID using simple Patch
	// If there's a conflict (409), error will be returned and next reconciliation will retry
	from := client.MergeFrom(&rvr)
	changedRVR := (&rvr).DeepCopy()
	if changedRVR.Status == nil {
		changedRVR.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{}
	}
	if changedRVR.Status.DRBD == nil {
		changedRVR.Status.DRBD = &v1alpha3.DRBD{}
	}
	if changedRVR.Status.DRBD.Config == nil {
		changedRVR.Status.DRBD.Config = &v1alpha3.DRBDConfig{}
	}
	changedRVR.Status.DRBD.Config.NodeId = availableNodeID

	if err := r.cl.Status().Patch(ctx, changedRVR, from); err != nil {
		log.Error(err, "Patching ReplicatedVolumeReplica status with nodeID")
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	log.Info("assigned nodeID to RVR", "nodeID", *availableNodeID, "volume", rvr.Spec.ReplicatedVolumeName)

	return reconcile.Result{}, nil
}
