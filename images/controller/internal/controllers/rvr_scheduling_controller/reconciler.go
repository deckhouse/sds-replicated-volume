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

package rvr_scheduling_controller

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
)

const (
	nodeZoneLabel      = "topology.kubernetes.io/zone"
	topologyIgnored    = "Ignored"
	topologyZonal      = "Zonal"
	topologyTransZonal = "TransZonal"
)

var (
	errSchedulingTopologyConflict = errors.New("scheduling topology conflict")
	errSchedulingNoCandidateNodes = errors.New("scheduling no candidate nodes")
)

type Reconciler struct {
	cl             client.Client
	log            logr.Logger
	scheme         *runtime.Scheme
	extenderClient *SchedulerExtenderClient
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

func NewReconciler(cl client.Client, log logr.Logger, scheme *runtime.Scheme) (*Reconciler, error) {
	extenderClient, err := NewSchedulerHTTPClient()
	if err != nil {
		log.Error(err, "failed to create scheduler-extender client")
		return nil, err // TODO: implement graceful shutdown
	}

	// Initialize reconciler with Kubernetes client, logger, scheme and scheduler-extender client.
	return &Reconciler{
		cl:             cl,
		log:            log,
		scheme:         scheme,
		extenderClient: extenderClient,
	}, nil
}

func (r *Reconciler) Reconcile(
	ctx context.Context,
	req reconcile.Request,
) (reconcile.Result, error) {
	log := r.log.WithName("Reconcile").WithValues("request", req)

	// Load ReplicatedVolume, its ReplicatedStorageClass and all relevant replicas.
	// The helper may also return an early reconcile.Result (e.g. when RV is not ready yet).
	sctx, failReason := r.prepareSchedulingContext(ctx, req, log)
	if failReason != nil {
		// set conditions
		if err := r.setScheduledConditionOnAllRVRs(ctx, sctx.Rv, failReason, log); err != nil {
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, nil

	}

	// Phase 1: place Diskful replicas.
	if err := r.scheduleDiskfulPhase(ctx, sctx); err != nil {
		reason := schedulingErrorToReason(err)
		if setErr := r.setScheduledConditionOnAllRVRs(ctx, sctx.Rv, reason, log); setErr != nil {
			log.Error(setErr, "failed to set Scheduled condition on RVRs after scheduling error")
		}
		return reconcile.Result{}, err
	}

	// Phase 2: place Access replicas.
	if err := r.scheduleAccessPhase(sctx); err != nil {
		reason := schedulingErrorToReason(err)
		if setErr := r.setScheduledConditionOnAllRVRs(ctx, sctx.Rv, reason, log); setErr != nil {
			log.Error(setErr, "failed to set Scheduled condition on RVRs after scheduling error")
		}
		return reconcile.Result{}, err
	}

	// Phase 3: place TieBreaker replicas.
	if err := r.scheduleTieBreakerPhase(sctx); err != nil {
		reason := schedulingErrorToReason(err)
		if setErr := r.setScheduledConditionOnAllRVRs(ctx, sctx.Rv, reason, log); setErr != nil {
			log.Error(setErr, "failed to set Scheduled condition on RVRs after scheduling error")
		}
		return reconcile.Result{}, err
	}

	// Phase 2: place remaining Diskful replicas (respecting topology and capacity).
	// if err := r.scheduleDiskfulPhase(ctx, rv, rsc, replicasForRV, log); err != nil {
	// 	_ = r.updateScheduledConditions(ctx, replicasForRV, err, log)
	// 	return reconcile.Result{}, err
	// }

	// // Phase 3: place Access replicas on publishOn nodes that still lack any replica.
	// if err := r.scheduleAccessPhase(ctx, rv, replicasForRV, log); err != nil {
	// 	_ = r.updateScheduledConditions(ctx, replicasForRV, err, log)
	// 	return reconcile.Result{}, err
	// }

	// // Phase 4: place TieBreaker replicas according to topology and existing placements.
	// if err := r.scheduleTieBreakerPhase(ctx, rsc, replicasForRV, log); err != nil {
	// 	_ = r.updateScheduledConditions(ctx, replicasForRV, err, log)
	// 	return reconcile.Result{}, err
	// }

	// Finally, update Scheduled conditions on all replicas based on their NodeName.
	// if err := r.updateScheduledConditions(ctx, replicasForRV, nil, log); err != nil {
	// 	log.Error(err, "failed to update Scheduled conditions on replicas")
	// 	return reconcile.Result{}, err
	// }

	for _, rvr := range sctx.RVRsToSchedule {
		// Create original state for patch (without NodeName)
		original := rvr.DeepCopy()
		original.Spec.NodeName = ""

		// Apply the patch; ignore NotFound errors because the replica may have been deleted meanwhile.
		if err := r.cl.Patch(ctx, rvr, client.MergeFrom(original)); err != nil {
			if apierrors.IsNotFound(err) {
				continue // Replica may have been deleted
			}
			return reconcile.Result{}, fmt.Errorf("failed to patch RVR %s: %w", rvr.Name, err)
		}

		// Set Scheduled condition to True for successfully scheduled replicas
		if err := r.setScheduledConditionOnRVR(
			ctx,
			rvr,
			metav1.ConditionTrue,
			v1alpha3.ReasonSchedulingReplicaScheduled,
			"",
		); err != nil {
			return reconcile.Result{}, fmt.Errorf("failed to set Scheduled condition on RVR %s: %w", rvr.Name, err)
		}
	}

	return reconcile.Result{}, nil
}

// rvNotReadyReason describes why an RV is not ready for scheduling.
type rvNotReadyReason struct {
	reason  string
	message string
}

// schedulingErrorToReason converts a scheduling error to rvNotReadyReason.
func schedulingErrorToReason(err error) *rvNotReadyReason {
	reason := v1alpha3.ReasonSchedulingFailed
	switch {
	case errors.Is(err, errSchedulingTopologyConflict):
		reason = v1alpha3.ReasonSchedulingTopologyConflict
	case errors.Is(err, errSchedulingNoCandidateNodes):
		reason = v1alpha3.ReasonSchedulingNoCandidateNodes
	}
	return &rvNotReadyReason{
		reason:  reason,
		message: err.Error(),
	}
}

// isRVReadyToSchedule checks if the ReplicatedVolume is ready for scheduling.
// Returns nil if ready, or a reason struct if not ready.
func isRVReadyToSchedule(rv *v1alpha3.ReplicatedVolume) *rvNotReadyReason {
	// ReplicatedVolume is considered ready only when status is present and ConditionReady is true.
	if rv.Status == nil {
		return &rvNotReadyReason{
			reason:  v1alpha3.ReasonSchedulingPending,
			message: "ReplicatedVolume status is not initialized",
		}
	}

	if rv.Finalizers == nil {
		return &rvNotReadyReason{
			reason:  v1alpha3.ReasonSchedulingPending,
			message: "ReplicatedVolume has no finalizers",
		}
	}

	if !slices.Contains(rv.Finalizers, v1alpha3.ControllerAppFinalizer) {
		return &rvNotReadyReason{
			reason:  v1alpha3.ReasonSchedulingPending,
			message: "ReplicatedVolume is missing controller finalizer",
		}
	}

	if rv.Spec.ReplicatedStorageClassName == "" {
		return &rvNotReadyReason{
			reason:  v1alpha3.ReasonSchedulingPending,
			message: "ReplicatedStorageClassName is not specified",
		}
	}

	if rv.Spec.Size.IsZero() {
		return &rvNotReadyReason{
			reason:  v1alpha3.ReasonSchedulingPending,
			message: "ReplicatedVolume size is zero",
		}
	}

	return nil
}

func (r *Reconciler) prepareSchedulingContext(
	ctx context.Context,
	req reconcile.Request,
	log logr.Logger,
) (*SchedulingContext, *rvNotReadyReason) {
	// Fetch the target ReplicatedVolume for this reconcile request.
	rv := &v1alpha3.ReplicatedVolume{}
	if err := r.cl.Get(ctx, req.NamespacedName, rv); err != nil {
		// If the volume no longer exists, exit reconciliation without error.
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		log.Error(err, "unable to get ReplicatedVolume")
		return nil, &rvNotReadyReason{
			reason:  v1alpha3.ReasonSchedulingFailed,
			message: fmt.Sprintf("unable to get ReplicatedVolume: %v", err),
		}
	}

	notReadyReason := isRVReadyToSchedule(rv)
	if notReadyReason != nil {
		return nil, notReadyReason
	}

	// Load the referenced ReplicatedStorageClass.
	rsc := &v1alpha1.ReplicatedStorageClass{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: rv.Spec.ReplicatedStorageClassName}, rsc); err != nil {
		log.Error(err, "unable to get ReplicatedStorageClass")
		return nil, &rvNotReadyReason{
			reason:  v1alpha3.ReasonSchedulingFailed,
			message: fmt.Sprintf("unable to get ReplicatedStorageClass: %v", err),
		}
	}

	// List all ReplicatedVolumeReplica resources in the cluster.
	replicaList := &v1alpha3.ReplicatedVolumeReplicaList{}
	if err := r.cl.List(ctx, replicaList); err != nil {
		log.Error(err, "unable to list ReplicatedVolumeReplica")
		return nil, &rvNotReadyReason{
			reason:  v1alpha3.ReasonSchedulingFailed,
			message: fmt.Sprintf("unable to list ReplicatedVolumeReplica: %v", err),
		}
	}

	// Keep only replicas that belong to this RV and are not being deleted.
	var replicasForRV []*v1alpha3.ReplicatedVolumeReplica
	for _, rvr := range replicaList.Items {
		if rvr.Spec.ReplicatedVolumeName != rv.Name || !rvr.DeletionTimestamp.IsZero() {
			continue
		}
		replicasForRV = append(replicasForRV, &rvr)
	}

	rsp := &v1alpha1.ReplicatedStoragePool{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: rsc.Spec.StoragePool}, rsp); err != nil {
		log.Error(err, "unable to get ReplicatedStoragePool", "name", rsc.Spec.StoragePool)
		return nil, &rvNotReadyReason{
			reason:  v1alpha3.ReasonSchedulingFailed,
			message: fmt.Sprintf("unable to get ReplicatedStoragePool: %v", err),
		}
	}

	rspLvgToNodeInfoMap, err := r.getLVGToNodesByStoragePool(ctx, rsc, log)
	if err != nil {
		return nil, &rvNotReadyReason{
			reason:  v1alpha3.ReasonSchedulingFailed,
			message: fmt.Sprintf("unable to get LVG to nodes mapping: %v", err),
		}
	}

	rspNodes := []string{}
	for _, info := range rspLvgToNodeInfoMap {
		rspNodes = append(rspNodes, info.NodeName)
	}

	nodeNameToZone, err := r.getNodeNameToZoneMap(ctx, log)
	if err != nil {
		return nil, &rvNotReadyReason{
			reason:  v1alpha3.ReasonSchedulingFailed,
			message: fmt.Sprintf("unable to get node to zone mapping: %v", err),
		}
	}

	publishOnList := getPublishOnNodeList(rv)
	nodesWithRVReplica := getNodesWithRVReplicaSet(replicasForRV)
	scheduledDiskfulReplicas, unscheduledDiskfulReplicas := getTypedReplicasLists(replicasForRV, v1alpha3.ReplicaTypeDiskful)
	publishNodesWithoutAnyReplica := publishNodesWithoutAnyReplica(publishOnList, nodesWithRVReplica)

	schedulingCtx := &SchedulingContext{
		Log:                            log,
		Rv:                             rv,
		Rsc:                            rsc,
		Rsp:                            rsp,
		RvrList:                        replicasForRV,
		RvPublishOnNodes:               publishOnList,
		RspLvgToNodeInfoMap:            rspLvgToNodeInfoMap,
		NodesWithAnyReplica:            nodesWithRVReplica,
		UnscheduledDiskfulReplicas:     unscheduledDiskfulReplicas,
		ScheduledDiskfulReplicas:       scheduledDiskfulReplicas,
		PublishOnNodesWithoutRvReplica: publishNodesWithoutAnyReplica,
		RspNodes:                       rspNodes,
		NodeNameToZone:                 nodeNameToZone,
	}

	return schedulingCtx, nil
}

func (r *Reconciler) scheduleDiskfulPhase(
	ctx context.Context,
	sctx *SchedulingContext,
) error {
	if len(sctx.UnscheduledDiskfulReplicas) == 0 {
		// Nothing to do if all Diskful replicas are already scheduled.
		return nil
	}

	candidateNodes := sctx.RspNodes

	// Apply topology constraints (Any/Zonal/TransZonal) to the publishOn nodes without replicas.
	// Spec «Diskful & Local»: apply topology (Any / Zonal / TransZonal) constraints to publishOn nodes.
	err := r.applyTopologyFilter(candidateNodes, sctx)
	if err != nil {
		// Topology constraints for Diskful & Local phase are violated.
		return fmt.Errorf("%w: %v", errSchedulingTopologyConflict, err)
	}

	if len(sctx.ZonesToNodeCandidatesMap) == 0 {
		return fmt.Errorf("%w: no candidate nodes found after topology filtering", errSchedulingNoCandidateNodes)
	}

	err = r.extenderClient.filterNodesBySchedulerExtender(ctx, sctx)
	if err != nil {
		return fmt.Errorf("%w: %v", errSchedulingNoCandidateNodes, err) // TODO: change error
	}

	if len(sctx.ZonesToNodeCandidatesMap) == 0 {
		return fmt.Errorf("%w: no nodes with sufficient storage space found", errSchedulingNoCandidateNodes)
	}

	// // Count the total number of candidate nodes after filtering
	// numCandidateNodes := 0
	// for _, candidates := range sctx.ZonesToNodeCandidatesMap {
	// 	numCandidateNodes += len(candidates)
	// }

	// if numCandidateNodes != len(sctx.PublishOnNodesWithoutRvReplica) {
	// 	return fmt.Errorf("%w: number of candidate nodes (%d) does not match number of publishOn nodes without replicas (%d)",
	// 		errSchedulingTopologyConflict, numCandidateNodes, len(sctx.PublishOnNodesWithoutRvReplica))
	// }

	// // Spec «Diskful & Local»: if we cannot place a replica on every publishOn node, it's an error.
	// // Check that we have enough unscheduled Diskful replicas for all candidate nodes.
	// if len(sctx.UnscheduledDiskfulReplicas) < numCandidateNodes {
	// 	return fmt.Errorf("%w: not enough unscheduled Diskful replicas (%d) for publishOn nodes (%d)",
	// 		errSchedulingNoCandidateNodes, len(sctx.UnscheduledDiskfulReplicas), numCandidateNodes)
	// }

	// // Take only as many replicas as we have candidate nodes to schedule in this phase.
	// // Remaining replicas will be scheduled in subsequent phases.
	// replicasToSchedule := sctx.UnscheduledDiskfulReplicas[:numCandidateNodes]

	sctx.ApplyPublishOnBonus()
	assignedReplicas, err := r.setNodesToRVReplicas(sctx, sctx.UnscheduledDiskfulReplicas)
	if err != nil {
		return fmt.Errorf("%w: %v", err, err)
	}

	sctx.UpdateAfterScheduling(assignedReplicas)

	return nil
}

// setNodesToRVReplicas assigns nodes to unscheduled replicas based on topology and node scores.
// For Ignored topology: selects best nodes by score.
// For Zonal topology: selects the best zone first (by total score), then best nodes from that zone.
// For TransZonal topology: distributes replicas across zones, picking zones with fewer scheduled replicas first.
// Note: This function returns the list of replicas that were assigned nodes in this call.
func (r *Reconciler) setNodesToRVReplicas(
	sctx *SchedulingContext,
	unscheduledReplicas []*v1alpha3.ReplicatedVolumeReplica,
) ([]*v1alpha3.ReplicatedVolumeReplica, error) {
	if len(unscheduledReplicas) == 0 {
		sctx.Log.Info("no unscheduled replicas to assign", "rv", sctx.Rv.Name)
		return nil, nil
	}

	switch sctx.Rsc.Spec.Topology {
	case "Ignored":
		return r.assignReplicasIgnoredTopology(sctx, unscheduledReplicas)
	case "Zonal":
		return r.assignReplicasZonalTopology(sctx, unscheduledReplicas)
	case "TransZonal":
		return r.assignReplicasTransZonalTopology(sctx, unscheduledReplicas)
	default:
		return nil, fmt.Errorf("unknown topology: %s", sctx.Rsc.Spec.Topology)
	}
}

// assignReplicasIgnoredTopology assigns replicas to best nodes by score (ignoring zones).
// It modifies rvr.Spec.NodeName and adds replicas to sctx.RVRsToSchedule for later patching.
// Returns the list of replicas that were assigned nodes.
func (r *Reconciler) assignReplicasIgnoredTopology(
	sctx *SchedulingContext,
	unscheduledReplicas []*v1alpha3.ReplicatedVolumeReplica,
) ([]*v1alpha3.ReplicatedVolumeReplica, error) {
	// Collect all candidates from all zones and sort by score descending
	var allCandidates []NodeCandidate
	for _, candidates := range sctx.ZonesToNodeCandidatesMap {
		allCandidates = append(allCandidates, candidates...)
	}

	// Sort by score descending (higher score = better)
	slices.SortFunc(allCandidates, func(a, b NodeCandidate) int {
		return b.Score - a.Score // descending
	})

	// Assign nodes to replicas
	var assignedReplicas []*v1alpha3.ReplicatedVolumeReplica
	usedNodes := make(map[string]struct{})
	for _, rvr := range unscheduledReplicas {
		var selectedNode string
		for _, candidate := range allCandidates {
			if _, used := usedNodes[candidate.Name]; !used {
				selectedNode = candidate.Name
				usedNodes[selectedNode] = struct{}{}
				break
			}
		}

		if selectedNode == "" {
			return assignedReplicas, fmt.Errorf("%w: not enough candidate nodes for all replicas", errSchedulingNoCandidateNodes)
		}

		// Mark replica for scheduling
		rvr.Spec.NodeName = selectedNode
		assignedReplicas = append(assignedReplicas, rvr)
	}

	return assignedReplicas, nil
}

// assignReplicasZonalTopology selects the best zone first, then assigns replicas to best nodes in that zone.
// Returns the list of replicas that were assigned nodes.
func (r *Reconciler) assignReplicasZonalTopology(
	sctx *SchedulingContext,
	unscheduledReplicas []*v1alpha3.ReplicatedVolumeReplica,
) ([]*v1alpha3.ReplicatedVolumeReplica, error) {
	// Find the best zone by combined metric: totalScore * len(candidates)
	// This ensures zones with more nodes are preferred when scores are comparable
	var bestZone string
	bestZoneScore := -1

	for zone, candidates := range sctx.ZonesToNodeCandidatesMap {
		totalScore := 0
		for _, c := range candidates {
			totalScore += c.Score
		}
		// Combined metric: zones with more nodes and good scores are preferred
		zoneScore := totalScore * len(candidates)
		if zoneScore > bestZoneScore {
			bestZoneScore = zoneScore
			bestZone = zone
		}
	}

	if bestZone == "" {
		return nil, fmt.Errorf("%w: no zones with candidates available", errSchedulingNoCandidateNodes)
	}

	// Get candidates from the best zone and sort by score descending
	candidates := slices.Clone(sctx.ZonesToNodeCandidatesMap[bestZone])
	slices.SortFunc(candidates, func(a, b NodeCandidate) int {
		return b.Score - a.Score // descending
	})

	// Assign nodes to replicas
	var assignedReplicas []*v1alpha3.ReplicatedVolumeReplica
	usedNodes := make(map[string]struct{})
	for _, rvr := range unscheduledReplicas {
		var selectedNode string
		for _, candidate := range candidates {
			if _, used := usedNodes[candidate.Name]; !used {
				selectedNode = candidate.Name
				usedNodes[selectedNode] = struct{}{}
				break
			}
		}

		if selectedNode == "" {
			return assignedReplicas, fmt.Errorf("%w: not enough candidate nodes in zone %s for all replicas", errSchedulingNoCandidateNodes, bestZone)
		}

		// Mark replica for scheduling
		rvr.Spec.NodeName = selectedNode
		assignedReplicas = append(assignedReplicas, rvr)
	}

	return assignedReplicas, nil
}

// assignReplicasTransZonalTopology distributes replicas across zones, preferring zones with fewer scheduled replicas of the same type.
// It modifies rvr.Spec.NodeName and adds replicas to sctx.RVRsToSchedule for later patching.
// Returns the list of replicas that were assigned nodes.
func (r *Reconciler) assignReplicasTransZonalTopology(
	sctx *SchedulingContext,
	unscheduledReplicas []*v1alpha3.ReplicatedVolumeReplica,
) ([]*v1alpha3.ReplicatedVolumeReplica, error) {
	if len(unscheduledReplicas) == 0 {
		return nil, nil
	}

	// Determine the type of replicas being scheduled
	replicaType := unscheduledReplicas[0].Spec.Type

	// Count already scheduled replicas of the same type per zone
	zoneReplicaCount := countReplicasByZone(sctx.RvrList, replicaType, sctx.NodeNameToZone)

	// Track used nodes in this scheduling round (copy from sctx to allow modifications)
	usedNodes := make(map[string]struct{}, len(sctx.NodesWithAnyReplica))
	for nodeName := range sctx.NodesWithAnyReplica {
		usedNodes[nodeName] = struct{}{}
	}

	// For each unscheduled replica, pick the zone with fewest replicas, then best node
	var assignedReplicas []*v1alpha3.ReplicatedVolumeReplica
	for _, rvr := range unscheduledReplicas {
		// Find zone with minimum replica count that has available candidates
		var selectedZone string
		minCount := -1

		for zone, candidates := range sctx.ZonesToNodeCandidatesMap {
			// Check if zone has any unused candidates
			hasAvailable := false
			for _, c := range candidates {
				if _, used := usedNodes[c.Name]; !used {
					hasAvailable = true
					break
				}
			}
			if !hasAvailable {
				continue
			}

			count := zoneReplicaCount[zone]
			if minCount == -1 || count < minCount {
				minCount = count
				selectedZone = zone
			}
		}

		if selectedZone == "" {
			// No more zones with available candidates - stop scheduling remaining replicas
			// This is not an error for some replica types (e.g., Access, TieBreaker)
			return assignedReplicas, nil
		}

		// Get candidates from selected zone and sort by score descending
		candidates := sctx.ZonesToNodeCandidatesMap[selectedZone]
		slices.SortFunc(candidates, func(a, b NodeCandidate) int {
			return b.Score - a.Score // descending
		})

		// Find best unused node in this zone
		var selectedNode string
		for _, candidate := range candidates {
			if _, used := usedNodes[candidate.Name]; !used {
				selectedNode = candidate.Name
				break
			}
		}

		if selectedNode == "" {
			// No available node in this zone - stop scheduling remaining replicas
			return assignedReplicas, nil
		}

		// Mark replica for scheduling
		rvr.Spec.NodeName = selectedNode
		assignedReplicas = append(assignedReplicas, rvr)

		// Update tracking
		usedNodes[selectedNode] = struct{}{}
		zoneReplicaCount[selectedZone]++
	}

	return assignedReplicas, nil
}

// func (r *Reconciler) scheduleDiskfulPhase(
// 	ctx context.Context,
// 	rv *v1alpha3.ReplicatedVolume,
// 	rsc *v1alpha1.ReplicatedStorageClass,
// 	replicasForRV []v1alpha3.ReplicatedVolumeReplica,
// 	log logr.Logger,
// ) error {
// 	// Non-Local Diskful scheduling is skipped when access mode is Local.
// 	// Spec «Diskful (non-Local)»: skip this phase when volumeAccess is Local.
// 	if rsc.Spec.VolumeAccess == "Local" {
// 		return nil
// 	}

// 	// Collect all Diskful replicas that are not bound to any node.
// 	// Spec «Diskful (non-Local)»: consider only Diskful replicas that are not yet scheduled.
// 	// _, unscheduledDiskfulReplicas := getTypedReplicasLists(replicasForRV, v1alpha3.ReplicaTypeDiskful)
// 	// if len(unscheduledDiskfulReplicas) == 0 {
// 	// 	return nil
// 	// }

// 	// Discover LVG-backed nodes for the storage pool defined in the RSC.
// 	// Spec «Diskful (non-Local)»: intersect candidate nodes with LVG nodes from rsp.spec.lvmVolumeGroups (storagePool).
// 	// rspLvgsToNodesMap, err := r.getLVGToNodesByStoragePool(ctx, rsc, log)
// 	// if err != nil {
// 	// 	log.Error(err, "failed to determine diskful candidate nodes")
// 	// 	return err
// 	// }

// 	// // Optionally narrow down LVG nodes by capacity via scheduler-extender.
// 	// // Spec «Diskful (non-Local)»: account for capacity via scheduler-extender.
// 	// capacityFilteredDiskfulNodes, err := r.filterLVGNodesByCapacity(ctx, rv, rspLvgsToNodesMap)
// 	// if err != nil {
// 	// 	return err
// 	// }

// 	// // Track nodes that already host any replica of this RV.
// 	// // Spec «Diskful (non-Local)»: exclude nodes that already host any replica of this RV (any type).
// 	// nodesWithAnyReplica := getNodesWithRVReplicaList(replicasForRV)

// 	// // Build a map of node -> zone for all cluster nodes.
// 	// // Spec «Diskful (non-Local)»: build node->zone map for future topology checks.
// 	// rawNodeNameToZone, err := r.getNodeNameToZoneMap(ctx, log)
// 	// if err != nil {
// 	// 	return err
// 	// }

// 	// // Filter nodes according to RSC topology type and allowed zones.
// 	// // Spec «Diskful (non-Local)»: filter nodes according to rsc.spec.topology and rsc.spec.zones.
// 	// nodeNameToRSCZone, err := filterNodesByRSCTopology(rawNodeNameToZone, rsc)
// 	// if err != nil {
// 	// 	return err
// 	// }

// 	// // Start from all publishOn nodes.
// 	// // Spec «Diskful (non-Local)»: try to honor rv.spec.publishOn by preferring these nodes when possible.
// 	// allPublishNodes := getPublishOnNodeList(rv)
// 	// // Intersect publishOn nodes with capacity-allowed LVG nodes if extender filtering was applied.
// 	// nodesToPublishOn := allPublishNodes
// 	// if capacityFilteredDiskfulNodes != nil {
// 	// 	filtered := make(map[string]struct{}, len(allPublishNodes))
// 	// 	for nodeName := range allPublishNodes {
// 	// 		if _, ok := capacityFilteredDiskfulNodes[nodeName]; ok {
// 	// 			filtered[nodeName] = struct{}{}
// 	// 		}
// 	// 	}
// 	// 	nodesToPublishOn = filtered
// 	// }

// 	// Place replicas according to topology (Any / Zonal / TransZonal).
// 	// Spec «Diskful (non-Local)»: place replicas according to topology (Any / Zonal / TransZonal).
// 	// switch rsc.Spec.Topology {
// 	// case "Ignored":
// 	// 	return r.scheduleDiskfulAnyTopology(ctx, unscheduledDiskfulReplicas, nodesWithAnyReplica, nodesToPublishOn, log)
// 	// case "Zonal":
// 	// 	return r.scheduleDiskfulZonalTopology(ctx, unscheduledDiskfulReplicas, replicasForRV, nodesWithAnyReplica, nodesToPublishOn, nodeNameToRSCZone, log)
// 	// case "TransZonal":
// 	// 	return r.scheduleDiskfulTransZonalTopology(ctx, unscheduledDiskfulReplicas, replicasForRV, nodesWithAnyReplica, nodesToPublishOn, nodeNameToRSCZone, log)
// 	// default:
// 	// 	return nil
// 	// }

// 	// TODO: When implementing this phase, call sctx.ApplyPublishOnBonus() after filterNodesBySchedulerExtender
// 	// and before setNodesToRVReplicas to prefer publishOn nodes for Diskful replica placement.

// 	return nil // TODO remove
// }

func (r *Reconciler) scheduleAccessPhase(
	sctx *SchedulingContext,
) error {
	// Spec «Access»: phase works only when:
	// - rv.spec.publishOn is set AND not all publishOn nodes have replicas
	// - rsc.spec.volumeAccess != Local
	if len(sctx.RvPublishOnNodes) == 0 {
		return nil
	}

	if sctx.Rsc.Spec.VolumeAccess == "Local" {
		return nil
	}

	// Get unscheduled Access replicas
	_, unscheduledAccessReplicas := getTypedReplicasLists(sctx.RvrList, v1alpha3.ReplicaTypeAccess)
	if len(unscheduledAccessReplicas) == 0 {
		// All Access replicas are already scheduled; nothing to do.
		return nil
	}

	// Spec «Access»: exclude nodes that already host any replica of this RV (any type)
	// Use PublishOnNodesWithoutRvReplica which already contains publishOn nodes without any replica
	candidateNodes := sctx.PublishOnNodesWithoutRvReplica
	if len(candidateNodes) == 0 {
		// All publishOn nodes already have replicas; nothing to do.
		// Spec «Access»: it is allowed to have replicas that could not be scheduled
		return nil
	}

	// We are not required to place all Access replicas or to cover all publishOn nodes.
	// Spec «Access»: it is allowed to have nodes in rv.spec.publishOn without enough replicas
	// Spec «Access»: it is allowed to have replicas that could not be scheduled
	nodesToFill := len(candidateNodes)
	if len(unscheduledAccessReplicas) < nodesToFill {
		nodesToFill = len(unscheduledAccessReplicas)
	}

	var assignedReplicas []*v1alpha3.ReplicatedVolumeReplica
	for i := 0; i < nodesToFill; i++ {
		nodeName := candidateNodes[i]
		rvr := unscheduledAccessReplicas[i]

		rvr.Spec.NodeName = nodeName
		assignedReplicas = append(assignedReplicas, rvr)
	}

	// Update context after scheduling
	sctx.UpdateAfterScheduling(assignedReplicas)

	return nil
}

func (r *Reconciler) scheduleTieBreakerPhase(
	sctx *SchedulingContext,
) error {
	// Get unscheduled TieBreaker replicas
	_, unscheduledTieBreakerReplicas := getTypedReplicasLists(sctx.RvrList, v1alpha3.ReplicaTypeTieBreaker)
	if len(unscheduledTieBreakerReplicas) == 0 {
		// All TieBreaker replicas are already scheduled; nothing to do.
		return nil
	}

	// Spec «TieBreaker»: exclude nodes that already have replicas of this RV (any type)
	// NodesWithAnyReplica is already maintained in sctx

	// Choose a planning strategy based on topology type.
	var assignedReplicas []*v1alpha3.ReplicatedVolumeReplica
	var err error

	switch sctx.Rsc.Spec.Topology {
	case "TransZonal":
		assignedReplicas, err = r.scheduleTieBreakerTransZonal(sctx, unscheduledTieBreakerReplicas)
	case "Zonal":
		assignedReplicas, err = r.scheduleTieBreakerZonal(sctx, unscheduledTieBreakerReplicas)
	default:
		assignedReplicas, err = r.scheduleTieBreakerIgnored(sctx, unscheduledTieBreakerReplicas)
	}

	if err != nil {
		return err
	}

	// Update context after scheduling
	sctx.UpdateAfterScheduling(assignedReplicas)

	return nil
}

// scheduleTieBreakerTransZonal schedules TieBreaker replicas for TransZonal topology.
// Spec «TieBreaker»: for TransZonal - schedule each rvr to the zone with the smallest number of replicas.
// If there are multiple zones with the smallest number - choose any of them.
// If there are no free nodes in zones with the smallest number of replicas - return scheduling error.
func (r *Reconciler) scheduleTieBreakerTransZonal(
	sctx *SchedulingContext,
	unscheduledReplicas []*v1alpha3.ReplicatedVolumeReplica,
) ([]*v1alpha3.ReplicatedVolumeReplica, error) {
	// Count how many replicas (any type) already exist in each zone.
	zoneReplicaCount := countAllReplicasByZone(sctx.RvrList, sctx.NodeNameToZone)

	// Build zone -> available nodes map (nodes without any replica)
	zoneToAvailableNodes := make(map[string][]string)
	for nodeName, zone := range sctx.NodeNameToZone {
		if zone == "" {
			continue
		}
		// Check if zone is allowed by RSC
		if len(sctx.Rsc.Spec.Zones) > 0 && !slices.Contains(sctx.Rsc.Spec.Zones, zone) {
			continue
		}
		// Exclude nodes that already have a replica
		if _, hasReplica := sctx.NodesWithAnyReplica[nodeName]; hasReplica {
			continue
		}
		zoneToAvailableNodes[zone] = append(zoneToAvailableNodes[zone], nodeName)
	}

	// Track used nodes during this scheduling round
	usedNodes := make(map[string]struct{})

	var assignedReplicas []*v1alpha3.ReplicatedVolumeReplica
	for _, rvr := range unscheduledReplicas {
		// Find zone with minimum replica count that has available nodes
		var selectedZone string
		minCount := -1

		for zone, availableNodes := range zoneToAvailableNodes {
			// Check if zone has any unused nodes
			hasUnused := false
			for _, nodeName := range availableNodes {
				if _, used := usedNodes[nodeName]; !used {
					hasUnused = true
					break
				}
			}
			if !hasUnused {
				continue
			}

			count := zoneReplicaCount[zone]
			if minCount == -1 || count < minCount {
				minCount = count
				selectedZone = zone
			}
		}

		if selectedZone == "" {
			// Spec «TieBreaker»: if there are no free nodes in zones with minimal replica count - return error
			return nil, fmt.Errorf(
				"%w: cannot schedule TieBreaker: no free node in zones with minimal replica count",
				errSchedulingNoCandidateNodes,
			)
		}

		// Find first available node in selected zone
		var selectedNode string
		for _, nodeName := range zoneToAvailableNodes[selectedZone] {
			if _, used := usedNodes[nodeName]; !used {
				selectedNode = nodeName
				break
			}
		}

		// Assign node to replica
		rvr.Spec.NodeName = selectedNode
		assignedReplicas = append(assignedReplicas, rvr)

		// Update tracking
		usedNodes[selectedNode] = struct{}{}
		zoneReplicaCount[selectedZone]++
	}

	return assignedReplicas, nil
}

// scheduleTieBreakerZonal schedules TieBreaker replicas for Zonal topology.
// Spec «TieBreaker»: for Zonal - exclude nodes from other zones.
func (r *Reconciler) scheduleTieBreakerZonal(
	sctx *SchedulingContext,
	unscheduledReplicas []*v1alpha3.ReplicatedVolumeReplica,
) ([]*v1alpha3.ReplicatedVolumeReplica, error) {
	// Determine the target zone from already placed replicas
	var targetZone string
	for _, rvr := range sctx.RvrList {
		if rvr.Spec.NodeName == "" {
			continue
		}
		zone, ok := sctx.NodeNameToZone[rvr.Spec.NodeName]
		if !ok || zone == "" {
			continue
		}
		if targetZone == "" {
			targetZone = zone
		} else if targetZone != zone {
			return nil, fmt.Errorf(
				"%w: cannot satisfy Zonal topology: replicas already exist in multiple zones (%s, %s)",
				errSchedulingTopologyConflict,
				targetZone,
				zone,
			)
		}
	}

	// If there is no existing replica, pick any zone allowed by the topology
	if targetZone == "" {
		if len(sctx.Rsc.Spec.Zones) > 0 {
			targetZone = sctx.Rsc.Spec.Zones[0]
		} else {
			// Pick first available zone from nodes
			for _, zone := range sctx.NodeNameToZone {
				if zone != "" {
					targetZone = zone
					break
				}
			}
		}
	}

	if targetZone == "" {
		return nil, fmt.Errorf(
			"%w: cannot determine target zone for Zonal topology",
			errSchedulingTopologyConflict,
		)
	}

	// Collect available nodes in the target zone (excluding nodes with replicas)
	var candidateNodes []string
	for nodeName, zone := range sctx.NodeNameToZone {
		if zone != targetZone {
			continue
		}
		if _, hasReplica := sctx.NodesWithAnyReplica[nodeName]; hasReplica {
			continue
		}
		candidateNodes = append(candidateNodes, nodeName)
	}

	// Schedule as many replicas as we have candidate nodes
	limit := len(candidateNodes)
	if len(unscheduledReplicas) < limit {
		limit = len(unscheduledReplicas)
	}

	var assignedReplicas []*v1alpha3.ReplicatedVolumeReplica
	for i := 0; i < limit; i++ {
		nodeName := candidateNodes[i]
		rvr := unscheduledReplicas[i]

		rvr.Spec.NodeName = nodeName
		assignedReplicas = append(assignedReplicas, rvr)
	}

	return assignedReplicas, nil
}

// scheduleTieBreakerIgnored schedules TieBreaker replicas for Ignored topology.
// Spec «TieBreaker»: for Any/Ignored - zones are not considered.
func (r *Reconciler) scheduleTieBreakerIgnored(
	sctx *SchedulingContext,
	unscheduledReplicas []*v1alpha3.ReplicatedVolumeReplica,
) ([]*v1alpha3.ReplicatedVolumeReplica, error) {
	// Collect all nodes that do not yet host any replica of this RV
	var candidateNodes []string
	for nodeName := range sctx.NodeNameToZone {
		if _, hasReplica := sctx.NodesWithAnyReplica[nodeName]; hasReplica {
			continue
		}
		// If zones are specified in RSC, filter by them
		if len(sctx.Rsc.Spec.Zones) > 0 {
			zone := sctx.NodeNameToZone[nodeName]
			if !slices.Contains(sctx.Rsc.Spec.Zones, zone) {
				continue
			}
		}
		candidateNodes = append(candidateNodes, nodeName)
	}

	// Schedule as many replicas as we have candidate nodes
	limit := len(candidateNodes)
	if len(unscheduledReplicas) < limit {
		limit = len(unscheduledReplicas)
	}

	var assignedReplicas []*v1alpha3.ReplicatedVolumeReplica
	for i := 0; i < limit; i++ {
		nodeName := candidateNodes[i]
		rvr := unscheduledReplicas[i]

		rvr.Spec.NodeName = nodeName
		assignedReplicas = append(assignedReplicas, rvr)
	}

	return assignedReplicas, nil
}

func getPublishOnNodeList(rv *v1alpha3.ReplicatedVolume) []string {
	// Convert publishOn slice to a set for fast membership checks.
	publishNodeSet := make([]string, len(rv.Spec.PublishOn))
	for i, nodeName := range rv.Spec.PublishOn {
		publishNodeSet[i] = nodeName
	}

	return publishNodeSet
}

func getNodesWithRVReplicaSet(
	replicasForRV []*v1alpha3.ReplicatedVolumeReplica,
) map[string]struct{} {
	// Build a set of nodes that already host at least one replica of this RV.
	nodesWithAnyReplica := make(map[string]struct{})

	for _, rvr := range replicasForRV {
		if rvr.Spec.NodeName != "" {
			nodesWithAnyReplica[rvr.Spec.NodeName] = struct{}{}
		}
	}

	return nodesWithAnyReplica
}

func getTypedReplicasLists(
	replicasForRV []*v1alpha3.ReplicatedVolumeReplica,
	replicaType string,
) (scheduled, unscheduled []*v1alpha3.ReplicatedVolumeReplica) {
	// Collect replicas of the given type, separating them by NodeName assignment.
	for _, rvr := range replicasForRV {
		if rvr.Spec.Type != replicaType {
			continue
		}
		if rvr.Spec.NodeName != "" {
			scheduled = append(scheduled, rvr)
		} else {
			unscheduled = append(unscheduled, rvr)
		}
	}

	return scheduled, unscheduled
}

func (r *Reconciler) updateScheduledConditions(
	ctx context.Context,
	replicasForRV []v1alpha3.ReplicatedVolumeReplica,
	schedulingErr error,
	log logr.Logger,
) error {
	for _, rvr := range replicasForRV {
		patch := client.MergeFrom(rvr.DeepCopy())

		if rvr.Status == nil {
			rvr.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{}
		}

		status := metav1.ConditionFalse
		reason := v1alpha3.ReasonSchedulingWaitingForAnotherReplica
		message := ""

		if rvr.Spec.NodeName != "" {
			status = metav1.ConditionTrue
			reason = v1alpha3.ReasonSchedulingReplicaScheduled
		} else if schedulingErr != nil {
			switch {
			case errors.Is(schedulingErr, errSchedulingTopologyConflict):
				reason = v1alpha3.ReasonSchedulingTopologyConflict
				message = schedulingErr.Error()
			case errors.Is(schedulingErr, errSchedulingNoCandidateNodes):
				reason = v1alpha3.ReasonSchedulingNoCandidateNodes
				message = schedulingErr.Error()
			default:
				reason = v1alpha3.ReasonSchedulingFailed
				message = schedulingErr.Error()
			}
		}

		meta.SetStatusCondition(
			&rvr.Status.Conditions,
			metav1.Condition{
				Type:               v1alpha3.ConditionTypeScheduled,
				Status:             status,
				Reason:             reason,
				Message:            message,
				ObservedGeneration: rvr.Generation,
			},
		)

		if err := r.cl.Status().Patch(ctx, &rvr, patch); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			log.Error(err, "failed to patch Scheduled condition on ReplicatedVolumeReplica", "rvr", rvr.Name)
			return err
		}
	}

	return nil
}

// setScheduledConditionOnRVR sets the Scheduled condition on a single RVR.
func (r *Reconciler) setScheduledConditionOnRVR(
	ctx context.Context,
	rvr *v1alpha3.ReplicatedVolumeReplica,
	status metav1.ConditionStatus,
	reason string,
	message string,
) error {
	patch := client.MergeFrom(rvr.DeepCopy())

	if rvr.Status == nil {
		rvr.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{}
	}

	changed := meta.SetStatusCondition(
		&rvr.Status.Conditions,
		metav1.Condition{
			Type:               v1alpha3.ConditionTypeScheduled,
			Status:             status,
			Reason:             reason,
			Message:            message,
			ObservedGeneration: rvr.Generation,
		},
	)

	if !changed {
		return nil
	}

	err := r.cl.Status().Patch(ctx, rvr, patch)
	if apierrors.IsNotFound(err) {
		return nil
	}

	return err
}

// setScheduledConditionOnAllRVRs sets the Scheduled condition to False on all RVRs
// belonging to the given RV when the RV is not ready for scheduling.
func (r *Reconciler) setScheduledConditionOnAllRVRs(
	ctx context.Context,
	rv *v1alpha3.ReplicatedVolume,
	notReadyReason *rvNotReadyReason,
	log logr.Logger,
) error {
	// List all ReplicatedVolumeReplica resources in the cluster.
	replicaList := &v1alpha3.ReplicatedVolumeReplicaList{}
	if err := r.cl.List(ctx, replicaList); err != nil {
		log.Error(err, "unable to list ReplicatedVolumeReplica")
		return err
	}

	// Update Scheduled condition on all RVRs belonging to this RV.
	for _, rvr := range replicaList.Items {
		// TODO: fix checking for deletion
		if rvr.Spec.ReplicatedVolumeName != rv.Name || !rvr.DeletionTimestamp.IsZero() {
			continue
		}

		// Skip if the replica is already scheduled (has NodeName assigned).
		if rvr.Spec.NodeName != "" {
			continue
		}

		if err := r.setScheduledConditionOnRVR(
			ctx,
			&rvr,
			metav1.ConditionFalse,
			notReadyReason.reason,
			notReadyReason.message,
		); err != nil {
			log.Error(err, "failed to set Scheduled condition on ReplicatedVolumeReplica", "rvr", rvr.Name)
			return err
		}
	}

	return nil
}

func publishNodesWithoutAnyReplica(
	publishOnList []string,
	nodesWithRVReplica map[string]struct{},
) []string {
	publishNodesWithoutAnyReplica := make([]string, 0, len(publishOnList))

	for _, node := range publishOnList {
		if _, hasReplica := nodesWithRVReplica[node]; !hasReplica {
			publishNodesWithoutAnyReplica = append(publishNodesWithoutAnyReplica, node)
		}
	}
	return publishNodesWithoutAnyReplica
}

func nodesFromLVGMap(
	lvgToNodes map[string][]string,
) map[string]struct{} {
	nodesWithLVG := make(map[string]struct{})
	for _, nodes := range lvgToNodes {
		for _, n := range nodes {
			nodesWithLVG[n] = struct{}{}
		}
	}
	return nodesWithLVG
}

// func (r *Reconciler) filterLVGNodesByCapacity(
// 	ctx context.Context,
// 	rv *v1alpha3.ReplicatedVolume,
// 	lvgToNodeNamesMap map[string][]string,
// ) (map[string]struct{}, error) {
// 	if lvgToNodeNamesMap == nil {
// 		// No LVG restriction — all publishOn nodes are allowed from capacity perspective.
// 		return nil, nil
// 	}

// 	if len(lvgToNodeNamesMap) == 0 {
// 		// No LVG-backed nodes found; treat as "no additional restriction".
// 		return nil, nil
// 	}

// 	return r.extenderClient.filterNodesBySchedulerExtender(ctx, rv, lvgToNodeNamesMap, r.log)
// }

// func buildDiskfulLocalCandidates(
// 	rv *v1alpha3.ReplicatedVolume,
// 	replicasForRV []v1alpha3.ReplicatedVolumeReplica,
// 	allowedDiskfulNodes map[string]struct{},
// ) ([]*v1alpha3.ReplicatedVolumeReplica, []string) {
// 	publishNodeSet := getPublishOnNodeList(rv)

// 	filtered := make(map[string]struct{}, len(publishNodeSet))
// 	for nodeName := range publishNodeSet {
// 		if _, ok := allowedDiskfulNodes[nodeName]; ok {
// 			filtered[nodeName] = struct{}{}
// 		}
// 	}
// 	publishNodeSet = filtered

// 	nodesWithAnyReplica := getNodesWithRVReplicaList(replicasForRV)
// 	_, unscheduledDiskfulReplicas := getTypedReplicasLists(replicasForRV, v1alpha3.ReplicaTypeDiskful)
// 	publishNodesWithoutAnyReplica := publishNodesWithoutAnyReplica(publishNodeSet, nodesWithAnyReplica)

// 	return unscheduledDiskfulReplicas, publishNodesWithoutAnyReplica
// }

func (r *Reconciler) applyTopologyFilter(
	candidateNodes []string,
	sctx *SchedulingContext,
) error {

	switch sctx.Rsc.Spec.Topology {
	case topologyIgnored:
		sctx.Log.V(1).Info("skipping topology filter for Ignored topology", "topology", sctx.Rsc.Spec.Topology)
		// Create a fake zone "ignored" with all candidate nodes
		nodeCandidates := make([]NodeCandidate, 0, len(candidateNodes))
		for _, nodeName := range candidateNodes {
			nodeCandidates = append(nodeCandidates, NodeCandidate{
				Name:  nodeName,
				Score: 0, // All nodes have equal score when topology is ignored
			})
		}
		sctx.ZonesToNodeCandidatesMap = map[string][]NodeCandidate{
			"ignored": nodeCandidates,
		}
		return nil
	case topologyZonal:
		// Create a map of zones to node candidates
		err := r.makeZonalNodeCandidates(candidateNodes, sctx)
		if err != nil {
			return err
		}
	case topologyTransZonal:
		err := r.makeTransZonalNodeCandidates(candidateNodes, sctx)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown RSC topology: %s", sctx.Rsc.Spec.Topology)
	}
	return nil
}

// countReplicasByZone counts how many replicas of a specific type are scheduled in each zone.
func countReplicasByZone(
	replicas []*v1alpha3.ReplicatedVolumeReplica,
	replicaType string,
	nodeNameToZone map[string]string,
) map[string]int {
	zoneReplicaCount := make(map[string]int)
	for _, rvr := range replicas {
		if rvr.Spec.Type != replicaType || rvr.Spec.NodeName == "" {
			continue
		}
		if zone, ok := nodeNameToZone[rvr.Spec.NodeName]; ok {
			zoneReplicaCount[zone]++
		}
	}
	return zoneReplicaCount
}

// countAllReplicasByZone counts how many replicas (any type) are scheduled in each zone.
func countAllReplicasByZone(
	replicas []*v1alpha3.ReplicatedVolumeReplica,
	nodeNameToZone map[string]string,
) map[string]int {
	zoneReplicaCount := make(map[string]int)
	for _, rvr := range replicas {
		if rvr.Spec.NodeName == "" {
			continue
		}
		zone, ok := nodeNameToZone[rvr.Spec.NodeName]
		if !ok || zone == "" {
			continue
		}
		zoneReplicaCount[zone]++
	}
	return zoneReplicaCount
}

// groupCandidateNodesByZone groups candidate nodes by their zones, filtering by allowed zones
// and excluding nodes that already have replicas.
func (r *Reconciler) groupCandidateNodesByZone(
	candidateNodes []string,
	allowedZones map[string]struct{},
	sctx *SchedulingContext,
) map[string][]NodeCandidate {
	zonesToCandidates := make(map[string][]NodeCandidate)

	for _, nodeName := range candidateNodes {
		// Skip nodes that already have a replica
		if _, ok := sctx.NodesWithAnyReplica[nodeName]; ok {
			continue
		}

		zone, ok := sctx.NodeNameToZone[nodeName]
		if !ok || zone == "" {
			continue // Skip nodes without zone label
		}

		if _, ok := allowedZones[zone]; !ok {
			continue // Skip nodes not in allowed zones
		}

		zonesToCandidates[zone] = append(zonesToCandidates[zone], NodeCandidate{
			Name:  nodeName,
			Score: 0,
		})
	}

	return zonesToCandidates
}

// makeZonalNodeCandidates creates ZonesToNodeCandidatesMap for Zonal topology.
// It determines the target zone based on:
// 1. Already scheduled diskful replicas (if any exist, use their zone)
// 2. publishOn nodes (if specified, all must be in the same zone)
// 3. rsc.spec.zones (if neither 1 nor 2, use allowed zones)
// 4. All cluster zones (if rsc.spec.zones is empty)
func (r *Reconciler) makeZonalNodeCandidates(
	candidateNodes []string,
	sctx *SchedulingContext,
) error {
	// Find zones of already scheduled diskful replicas
	var zonesWithScheduledDiskfulReplicas []string
	for _, rvr := range sctx.ScheduledDiskfulReplicas {
		zone, ok := sctx.NodeNameToZone[rvr.Spec.NodeName]
		if !ok || zone == "" {
			return fmt.Errorf("scheduled diskful replica %s is on node %s without zone label for Zonal topology", rvr.Name, rvr.Spec.NodeName)
		}
		if !slices.Contains(zonesWithScheduledDiskfulReplicas, zone) {
			zonesWithScheduledDiskfulReplicas = append(zonesWithScheduledDiskfulReplicas, zone)
		}
	}

	// For Zonal topology, all scheduled diskful replicas must be in the same zone
	if len(zonesWithScheduledDiskfulReplicas) > 1 {
		return fmt.Errorf("%w: scheduled diskful replicas are in multiple zones %v for Zonal topology",
			errSchedulingTopologyConflict, zonesWithScheduledDiskfulReplicas)
	}

	// Find zones of publishOn nodes
	var publishOnZones []string
	for _, nodeName := range sctx.RvPublishOnNodes {
		zone, ok := sctx.NodeNameToZone[nodeName]
		if !ok || zone == "" {
			return fmt.Errorf("publishOn node %s has no zone label", nodeName)
		}
		if !slices.Contains(publishOnZones, zone) {
			publishOnZones = append(publishOnZones, zone)
		}
	}

	// // For Zonal topology, all publishOn nodes must be in the same zone
	// if len(publishOnZones) > 1 {
	// 	return fmt.Errorf("%w: publishOn nodes are in multiple zones %v for Zonal topology",
	// 		errSchedulingTopologyConflict, publishOnZones)
	// }

	// Determine the target zones
	var targetZones []string

	if len(zonesWithScheduledDiskfulReplicas) > 0 {
		targetZones = zonesWithScheduledDiskfulReplicas
	} else if len(publishOnZones) > 0 {
		targetZones = publishOnZones
	}

	// Build candidate nodes map
	// If we have target zones, only include nodes from those zones
	// Otherwise, include nodes from all allowed zones (rsc.spec.zones or all cluster zones)
	allowedZones := make(map[string]struct{})
	if len(targetZones) > 0 {
		// We have determined target zones
		for _, zone := range targetZones {
			allowedZones[zone] = struct{}{}
		}
	} else if len(sctx.Rsc.Spec.Zones) > 0 {
		// Use zones from RSC spec
		for _, zone := range sctx.Rsc.Spec.Zones {
			allowedZones[zone] = struct{}{}
		}
	} else {
		// Use all zones from cluster (collect unique zones)
		for _, zone := range sctx.NodeNameToZone {
			if zone != "" {
				allowedZones[zone] = struct{}{}
			}
		}
	}

	// Group candidate nodes by zone
	sctx.ZonesToNodeCandidatesMap = r.groupCandidateNodesByZone(candidateNodes, allowedZones, sctx)
	return nil
}

// makeTransZonalNodeCandidates creates ZonesToNodeCandidatesMap for TransZonal topology.
// Unlike Zonal, it distributes replicas across different zones.
// Rules:
// - Does not consider already scheduled replicas
// - Verifies publishOn nodes are in allowed zones
// - Allowed zones come from rsc.spec.zones or all cluster zones if not specified
func (r *Reconciler) makeTransZonalNodeCandidates(
	candidateNodes []string,
	sctx *SchedulingContext,
) error {
	// Determine allowed zones from RSC spec or all cluster zones
	allowedZones := make(map[string]struct{})
	if len(sctx.Rsc.Spec.Zones) > 0 {
		for _, zone := range sctx.Rsc.Spec.Zones {
			allowedZones[zone] = struct{}{}
		}
	} else {
		// Use all zones from cluster
		for _, zone := range sctx.NodeNameToZone {
			if zone != "" {
				allowedZones[zone] = struct{}{}
			}
		}
	}

	// Verify publishOn nodes are in allowed zones
	// publishOnZones := make(map[string]string)
	// for _, nodeName := range sctx.RvPublishOnNodes {
	// 	zone, ok := sctx.NodeNameToZone[nodeName]
	// 	if !ok || zone == "" {
	// 		return fmt.Errorf("publishOn node %s has no zone label", nodeName)
	// 	}
	// 	if _, ok := allowedZones[zone]; !ok {
	// 		return fmt.Errorf("%w: publishOn node %s is in zone %s which is not in allowed zones %v",
	// 			errSchedulingTopologyConflict, nodeName, zone, sctx.Rsc.Spec.Zones)
	// 	}
	// 	publishOnZones[nodeName] = zone
	// }

	// if sctx.Rsc.Spec.VolumeAccess == "Local" && len(sctx.RvPublishOnNodes) > 1 {
	// 	zonesSet := make(map[string]struct{})
	// 	for _, zone := range publishOnZones {
	// 		zonesSet[zone] = struct{}{}
	// 	}
	// 	if len(zonesSet) != len(sctx.RvPublishOnNodes) {
	// 		return fmt.Errorf("%w: TransZonal topology with Local volumeAccess requires publishOn nodes to be in different zones",
	// 			errSchedulingTopologyConflict)
	// 	}
	// }

	// Group candidate nodes by zone
	sctx.ZonesToNodeCandidatesMap = r.groupCandidateNodesByZone(candidateNodes, allowedZones, sctx)
	return nil
}

// func (r *Reconciler) filterDiskfulLocalPublishNodesByTopology(
// 	ctx context.Context,
// 	rsc *v1alpha1.ReplicatedStorageClass,
// 	publishNodesWithoutAnyReplica []string,
// 	log logr.Logger,
// ) ([]string, map[string]string, error) {
// 	rawNodeNameToZone, err := r.getNodeNameToZoneMap(ctx, log)
// 	if err != nil {
// 		log.Error(err, "unable to list Nodes while applying topology in DiskfulLocal phase")
// 		return nil, nil, err
// 	}

// 	nodeNameToRSCZone, err := filterNodesByRSCTopology(rawNodeNameToZone, rsc)
// 	if err != nil {
// 		return nil, nil, err
// 	}

// 	var topoFiltered []string
// 	for _, nodeName := range publishNodesWithoutAnyReplica {
// 		if _, ok := nodeNameToRSCZone[nodeName]; !ok {
// 			return nil, nil, fmt.Errorf(
// 				"cannot schedule Diskful Local replica on publishOn node %s due to topology constraints (topology=%s, zones=%v)",
// 				nodeName, rsc.Spec.Topology, rsc.Spec.Zones,
// 			)
// 		}
// 		topoFiltered = append(topoFiltered, nodeName)
// 	}

// 	return topoFiltered, nodeNameToRSCZone, nil
// }

// func validateDiskfulLocalZonesForZonalAndTransZonal(
// 	rsc *v1alpha1.ReplicatedStorageClass,
// 	replicasForRV []v1alpha3.ReplicatedVolumeReplica,
// 	topoFiltered []string,
// 	nodeNameToRSCZone map[string]string,
// ) error {
// 	// Track which zones already contain a Diskful replica of this RV.
// 	zoneHasReplicaMap := make(map[string]bool)

// 	// First, validate existing Diskful replicas against topology constraints.
// 	for _, rvr := range replicasForRV {
// 		if rvr.Spec.Type != v1alpha3.ReplicaTypeDiskful || rvr.Spec.NodeName == "" {
// 			continue
// 		}
// 		zone, ok := nodeNameToRSCZone[rvr.Spec.NodeName]
// 		if !ok {
// 			return fmt.Errorf(
// 				"existing Diskful replica %s on node %s does not match topology constraints (topology=%s, zones=%v)",
// 				rvr.Name, rvr.Spec.NodeName, rsc.Spec.Topology, rsc.Spec.Zones,
// 			)
// 		}
// 		// For TransZonal we must not have more than one Diskful replica per zone.
// 		if rsc.Spec.Topology == "TransZonal" && zoneHasReplicaMap[zone] {
// 			return fmt.Errorf(
// 				"cannot satisfy TransZonal topology: multiple Diskful replicas already exist in zone %s",
// 				zone,
// 			)
// 		}
// 		zoneHasReplicaMap[zone] = true
// 	}

// 	// Then, validate candidate publishOn nodes that passed the basic topology filter.
// 	for _, nodeName := range topoFiltered {
// 		zone := nodeNameToRSCZone[nodeName]
// 		switch rsc.Spec.Topology {
// 		case "Zonal":
// 			// For Zonal, all Diskful replicas (existing and new) must be in the same zone.
// 			if len(zoneHasReplicaMap) > 0 {
// 				for existingZone := range zoneHasReplicaMap {
// 					if zone != existingZone {
// 						return fmt.Errorf(
// 							"cannot satisfy Zonal topology: publishOn node %s is in zone %s while existing Diskful replicas are in zone %s",
// 							nodeName, zone, existingZone,
// 						)
// 					}
// 					break
// 				}
// 			}
// 			zoneHasReplicaMap[zone] = true
// 		case "TransZonal":
// 			// For TransZonal, new replicas must go only to zones that do not yet contain any Diskful replica.
// 			if zoneHasReplicaMap[zone] {
// 				return fmt.Errorf(
// 					"cannot satisfy TransZonal topology: publishOn node %s is in zone %s which already has a Diskful replica",
// 					nodeName, zone,
// 				)
// 			}
// 			zoneHasReplicaMap[zone] = true
// 		}
// 	}

// 	return nil
// }

func (r *Reconciler) getLVGToNodesByStoragePool(
	ctx context.Context,
	rsc *v1alpha1.ReplicatedStorageClass,
	log logr.Logger,
) (map[string]LvgNodeInfo, error) {
	// If no storage pool is specified, do not restrict nodes by LVG.
	if rsc.Spec.StoragePool == "" {
		return nil, nil
	}

	// Load the referenced ReplicatedStoragePool.
	rsp := &v1alpha1.ReplicatedStoragePool{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: rsc.Spec.StoragePool}, rsp); err != nil {
		// If the storage pool does not exist, do not add extra restrictions.
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		log.Error(err, "unable to get ReplicatedStoragePool", "name", rsc.Spec.StoragePool)
		return nil, err
	}

	// If the pool does not define any LVGs, there is no LVG-based restriction.
	if len(rsp.Spec.LVMVolumeGroups) == 0 {
		return nil, fmt.Errorf("storage pool %s does not define any LVGs", rsc.Spec.StoragePool)
	}

	// Build a map of LVG names to their ThinPoolName from the storage pool spec.
	rspLvgInfo := make(map[string]string, len(rsp.Spec.LVMVolumeGroups))
	for _, g := range rsp.Spec.LVMVolumeGroups {
		rspLvgInfo[g.Name] = g.ThinPoolName
	}

	// List all LVMVolumeGroup objects managed by node-configurator.
	lvgList := &snc.LVMVolumeGroupList{}
	if err := r.cl.List(ctx, lvgList); err != nil {
		log.Error(err, "unable to list LVMVolumeGroup")
		return nil, err
	}

	// Build a map LVG name -> LvgNodeInfo with node name and ThinPoolName.
	lvgToNodeInfoMap := make(map[string]LvgNodeInfo)
	for _, lvg := range lvgList.Items {
		thinPoolName, ok := rspLvgInfo[lvg.Name]
		if !ok {
			continue
		}
		if len(lvg.Status.Nodes) == 0 {
			continue
		}
		lvgToNodeInfoMap[lvg.Name] = LvgNodeInfo{
			NodeName:     lvg.Status.Nodes[0].Name,
			ThinPoolName: thinPoolName,
		}
	}

	if len(lvgToNodeInfoMap) == 0 {
		// No LVG-backed nodes found; fall back to "no restriction".
		return nil, nil
	}

	return lvgToNodeInfoMap, nil
}

func (r *Reconciler) getNodeNameToZoneMap(
	ctx context.Context,
	log logr.Logger,
) (map[string]string, error) {
	// List all Kubernetes Nodes to inspect their zone labels.
	nodes := &corev1.NodeList{}
	if err := r.cl.List(ctx, nodes); err != nil {
		log.Error(err, "unable to list Nodes")
		return nil, err
	}

	// Build a map from node name to its zone (may be empty if label is missing).
	nodeNameToZone := make(map[string]string, len(nodes.Items))

	for _, node := range nodes.Items {
		zone := node.Labels[nodeZoneLabel]
		nodeNameToZone[node.Name] = zone
	}

	return nodeNameToZone, nil
}

func filterNodesByRSCTopology(
	nodeNameToZone map[string]string,
	rsc *v1alpha1.ReplicatedStorageClass,
) (map[string]string, error) {
	// Result collects nodes that satisfy the RSC topology and zone constraints.
	result := make(map[string]string, len(nodeNameToZone))
	rscHasZones := len(rsc.Spec.Zones) > 0

	for nodeName, zone := range nodeNameToZone {
		nodeHasZone := zone != ""

		switch rsc.Spec.Topology {
		case "TransZonal", "Zonal":
			// For Zonal/TransZonal, every node must have a zone label and (optionally) be in one of RSC zones.
			if !nodeHasZone {
				return nil, fmt.Errorf("node %s has no zone label %s", nodeName, nodeZoneLabel)
			}
			if rscHasZones && !slices.Contains(rsc.Spec.Zones, zone) {
				continue
			}
		case "Any":
			// For Any, we only filter by zones if RSC explicitly lists them.
			if rscHasZones {
				if !nodeHasZone || !slices.Contains(rsc.Spec.Zones, zone) {
					continue
				}
			}
		default:
			// Unknown topology type is considered configuration error.
			return nil, fmt.Errorf("rsc %s has unknown topology type %s", rsc.Name, rsc.Spec.Topology)
		}

		result[nodeName] = zone
	}

	return result, nil
}
