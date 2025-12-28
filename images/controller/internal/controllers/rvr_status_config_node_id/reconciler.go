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

	u "github.com/deckhouse/sds-common-lib/utils"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
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
	var rv v1alpha1.ReplicatedVolume
	if err := r.cl.Get(ctx, req.NamespacedName, &rv); err != nil {
		if client.IgnoreNotFound(err) == nil {
			log.V(1).Info("ReplicatedVolume not found, probably deleted")
			return reconcile.Result{}, nil
		}
		log.Error(err, "Getting ReplicatedVolume")
		return reconcile.Result{}, err
	}

	// List all RVRs and filter by replicatedVolumeName
	// Note: We list all RVRs and filter in memory instead of using owner reference index
	// to avoid requiring a custom index field setup in the manager.
	rvrList := &v1alpha1.ReplicatedVolumeReplicaList{}
	if err := r.cl.List(ctx, rvrList); err != nil {
		log.Error(err, "listing RVRs")
		return reconcile.Result{}, err
	}

	// Filter by replicatedVolumeName (required field, always present)
	rvrList.Items = slices.DeleteFunc(rvrList.Items, func(item v1alpha1.ReplicatedVolumeReplica) bool {
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
	var rvrsNeedingNodeID []v1alpha1.ReplicatedVolumeReplica

	for _, item := range rvrList.Items {
		// Check if Config exists and has valid nodeID
		if item.Status != nil && item.Status.DRBD != nil && item.Status.DRBD.Config != nil && item.Status.DRBD.Config.NodeId != nil {
			nodeID := *item.Status.DRBD.Config.NodeId
			usedNodeIDs[nodeID] = struct{}{}
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

	// Assign nodeIDs to RVRs that need them sequentially
	// Note: We use ResourceVersion from List. Since we reconcile RV (not RVR) and process RVRs sequentially
	// for each RV, no one can edit the same RVR simultaneously within our controller. This makes the code
	// simple and solid, though not the fastest (no parallel processing of RVRs).
	// If we run out of available nodeIDs, we stop assigning, fail the reconcile, and let the next reconcile handle remaining RVRs once some replicas are removed.

	rvPrefix := rv.Name + "-"
	for i := range rvrsNeedingNodeID {
		rvr := &rvrsNeedingNodeID[i]

		nodeID, ok := rvr.NodeIdFromName(rvPrefix)
		if !ok {
			log.V(1).Info(fmt.Sprintf("failed parsing node id from rvr name: %s", rvr.Name))
			continue
		}

		// Prepare patch: initialize status fields if needed and set nodeID
		orig := client.MergeFrom(rvr.DeepCopy())
		if rvr.Status == nil {
			rvr.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{}
		}
		if rvr.Status.DRBD == nil {
			rvr.Status.DRBD = &v1alpha1.DRBD{}
		}
		if rvr.Status.DRBD.Config == nil {
			rvr.Status.DRBD.Config = &v1alpha1.DRBDConfig{}
		}
		rvr.Status.DRBD.Config.NodeId = u.Ptr(uint(nodeID))

		// Patch RVR status with assigned nodeID
		if err := r.cl.Status().Patch(ctx, rvr, orig); err != nil {
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
