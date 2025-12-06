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

package rvrstatusconfignodeid

import (
	"context"
	"fmt"
	"slices"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
)

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

	// Get the ReplicatedVolume (parent resource)
	var rv v1alpha3.ReplicatedVolume
	if err := r.cl.Get(ctx, req.NamespacedName, &rv); err != nil {
		log.Error(err, "Getting ReplicatedVolume")
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	// List all RVRs and filter by replicatedVolumeName
	// Note: We list all RVRs and filter in memory instead of using owner reference index
	// to avoid requiring a custom index field setup in the manager.
	rvrList := &v1alpha3.ReplicatedVolumeReplicaList{}
	if err := r.cl.List(ctx, rvrList); err != nil {
		log.Error(err, "listing RVRs")
		return reconcile.Result{}, err
	}

	// Filter by replicatedVolumeName (required field, always present)
	rvrList.Items = slices.DeleteFunc(rvrList.Items, func(item v1alpha3.ReplicatedVolumeReplica) bool {
		return item.Spec.ReplicatedVolumeName != rv.Name
	})

	// Early exit if no RVRs for this volume
	if len(rvrList.Items) == 0 {
		log.V(1).Info("no RVRs for volume")
		return reconcile.Result{}, nil
	}

	// Collect used nodeIDs and find RVRs that need nodeID assignment
	// - RVRs with valid nodeID: add to usedNodeIDs map
	// - RVRs without nodeID: add to rvrsNeedingNodeID list
	// - RVRs with invalid nodeID: log and ignore. TODO: Revisit this in spec
	usedNodeIDs := make(map[uint]struct{})
	var rvrsNeedingNodeID []v1alpha3.ReplicatedVolumeReplica

	for _, item := range rvrList.Items {
		// Check if Config exists and has valid nodeID
		if item.Status != nil && item.Status.DRBD != nil && item.Status.DRBD.Config != nil && item.Status.DRBD.Config.NodeId != nil {
			nodeID := *item.Status.DRBD.Config.NodeId
			if v1alpha3.IsValidNodeID(nodeID) {
				usedNodeIDs[nodeID] = struct{}{}
				continue
			}
			// NOTE: Logging invalid nodeID is NOT in the spec.
			// This was added to improve observability - administrators can see invalid nodeIDs in logs.
			// To revert: remove this log line.
			log.V(1).Info("ignoring nodeID outside valid range", "nodeID", nodeID, "validRange", v1alpha3.FormatValidNodeIDRange(), "rvr", item.Name, "volume", rv.Name)
			continue
		}
		// RVR needs nodeID assignment
		rvrsNeedingNodeID = append(rvrsNeedingNodeID, item)
	}

	// Early exit if all RVRs already have valid nodeIDs
	if len(rvrsNeedingNodeID) == 0 {
		log.V(1).Info("all RVRs already have valid nodeIDs")
		return reconcile.Result{}, nil
	}

	// Find available nodeIDs (not in usedNodeIDs map)
	availableNodeIDs := make([]uint, 0, int(v1alpha3.RVRMaxNodeID)+1)
	for i := v1alpha3.RVRMinNodeID; i <= v1alpha3.RVRMaxNodeID; i++ {
		if _, exists := usedNodeIDs[i]; !exists {
			availableNodeIDs = append(availableNodeIDs, i)
		}
	}

	// Warn if we don't have enough available nodeIDs, but continue assigning what we have
	// Remaining RVRs will get nodeIDs in the next reconcile when more become available
	if len(availableNodeIDs) < len(rvrsNeedingNodeID) {
		totalReplicas := len(rvrList.Items)
		log.Info(
			"not enough available nodeIDs to assign all replicas; will assign to as many as possible and fail reconcile",
			"needed", len(rvrsNeedingNodeID),
			"available", len(availableNodeIDs),
			"replicas", totalReplicas,
			"max", int(v1alpha3.RVRMaxNodeID)+1,
			"volume", rv.Name,
		)
	}

	// Assign nodeIDs to RVRs that need them sequentially
	// Note: We use ResourceVersion from List. Since we reconcile RV (not RVR) and process RVRs sequentially
	// for each RV, no one can edit the same RVR simultaneously within our controller. This makes the code
	// simple and solid, though not the fastest (no parallel processing of RVRs).
	// If we run out of available nodeIDs, we stop assigning, fail the reconcile, and let the next reconcile handle remaining RVRs once some replicas are removed.
	for i := range rvrsNeedingNodeID {
		rvr := &rvrsNeedingNodeID[i]

		// Get next available nodeID from the list
		// If no more available, stop assigning (remaining RVRs will be handled in next reconcile)
		if i >= len(availableNodeIDs) {
			// We will fail reconcile and let the next reconcile handle remaining RVRs
			err := fmt.Errorf(
				"%s for volume %s: remaining RVRs without nodeID=%d, usedNodeIDs=%d, maxNodeIDs=%d",
				ErrNotEnoughAvailableNodeIDsPrefix,
				rv.Name,
				len(rvrsNeedingNodeID)-i,
				len(usedNodeIDs),
				int(v1alpha3.RVRMaxNodeID)+1,
			)
			log.Error(err, "no more available nodeIDs, remaining RVRs will be assigned only after some replicas are removed")
			return reconcile.Result{}, err
		}
		nodeID := availableNodeIDs[i]

		// Prepare patch: initialize status fields if needed and set nodeID
		from := client.MergeFrom(rvr)
		changedRVR := rvr.DeepCopy()
		if changedRVR.Status == nil {
			changedRVR.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{}
		}
		if changedRVR.Status.DRBD == nil {
			changedRVR.Status.DRBD = &v1alpha3.DRBD{}
		}
		if changedRVR.Status.DRBD.Config == nil {
			changedRVR.Status.DRBD.Config = &v1alpha3.DRBDConfig{}
		}
		changedRVR.Status.DRBD.Config.NodeId = &nodeID

		// Patch RVR status with assigned nodeID
		if err := r.cl.Status().Patch(ctx, changedRVR, from); err != nil {
			if client.IgnoreNotFound(err) == nil {
				// RVR was deleted, skip
				continue
			}
			log.Error(err, "Patching ReplicatedVolumeReplica status with nodeID", "rvr", rvr.Name, "nodeID", nodeID)
			return reconcile.Result{}, err
		}
		log.Info("assigned nodeID to RVR", "nodeID", nodeID, "rvr", rvr.Name, "volume", rv.Name)
	}

	return reconcile.Result{}, nil
}
