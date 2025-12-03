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
	e "github.com/deckhouse/sds-replicated-volume/images/controller/internal/errors"
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

	// Get the RV
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
			log.V(1).Info("ignoring nodeID outside valid range, will reassign", "nodeID", nodeID, "validRange", v1alpha3.FormatValidNodeIDRange(), "rvr", item.Name)
		}
		// RVR needs nodeID assignment (either nil Status/DRBD/Config or invalid nodeID)
		rvrsNeedingNodeID = append(rvrsNeedingNodeID, item)
	}

	// Early exit if all RVRs already have valid nodeIDs
	if len(rvrsNeedingNodeID) == 0 {
		log.V(1).Info("all RVRs already have valid nodeIDs")
		return reconcile.Result{}, nil
	}

	// Count total replicas
	totalReplicas := len(rvrList.Items)
	if totalReplicas > int(v1alpha3.RVRMaxNodeID)+1 {
		err := e.ErrInvalidClusterf(
			"too many replicas for volume %s: %d (maximum is %d)",
			rv.Name,
			totalReplicas,
			int(v1alpha3.RVRMaxNodeID)+1,
		)
		log.Error(err, "too many replicas for volume", "replicas", totalReplicas, "max", int(v1alpha3.RVRMaxNodeID)+1)
		return reconcile.Result{}, err
	}

	// Check if we have enough available nodeIDs
	availableNodeIDs := make([]uint, 0, int(v1alpha3.RVRMaxNodeID)+1)
	for i := v1alpha3.RVRMinNodeID; i <= v1alpha3.RVRMaxNodeID; i++ {
		if _, exists := usedNodeIDs[i]; !exists {
			availableNodeIDs = append(availableNodeIDs, i)
		}
	}

	if len(availableNodeIDs) < len(rvrsNeedingNodeID) {
		err := e.ErrInvalidClusterf(
			"no available nodeID for volume %s: need %d, available %d (all %d nodeIDs are used)",
			rv.Name,
			len(rvrsNeedingNodeID),
			len(availableNodeIDs),
			int(v1alpha3.RVRMaxNodeID)+1,
		)
		log.Error(err, "no available nodeID for volume", "needed", len(rvrsNeedingNodeID), "available", len(availableNodeIDs), "maxNodeIDs", int(v1alpha3.RVRMaxNodeID)+1)
		return reconcile.Result{}, err
	}

	// Assign nodeIDs to RVRs that need them sequentially
	// Note: We use ResourceVersion from List. Since we reconcile RV (not RVR) and process RVRs sequentially
	// for each RV, no one can edit the same RVR simultaneously within our controller. This makes the code
	// simple and solid, though not the fastest (no parallel processing of RVRs).
	nodeIDIndex := 0
	for i := range rvrsNeedingNodeID {
		rvr := &rvrsNeedingNodeID[i]

		// Get next available nodeID
		if nodeIDIndex >= len(availableNodeIDs) {
			return reconcile.Result{}, fmt.Errorf("no more available nodeIDs")
		}
		nodeID := availableNodeIDs[nodeIDIndex]
		nodeIDIndex++

		// Prepare patch using ResourceVersion from List
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

		// Patch RVR status
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
