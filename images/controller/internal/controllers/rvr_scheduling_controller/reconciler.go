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

package rvrschedulingcontroller

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
	errSchedulingPending          = errors.New("scheduling pending")
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
	log := r.log.WithName("RVRScheduler").WithValues(
		"rv", req.Name,
	)
	log.V(1).Info("starting reconciliation cycle")

	// Load ReplicatedVolume, its ReplicatedStorageClass and all relevant replicas.
	sctx, err := r.prepareSchedulingContext(ctx, req, log)
	if err != nil {
		return reconcile.Result{}, r.handlePhaseError(ctx, req.Name, "prepare", err, log)
	}
	if sctx == nil {
		// ReplicatedVolume not found, skip reconciliation
		return reconcile.Result{}, nil
	}
	log.V(1).Info("scheduling context prepared", "rsc", sctx.Rsc.Name, "topology", sctx.Rsc.Spec.Topology, "volumeAccess", sctx.Rsc.Spec.VolumeAccess)

	// Ensure all previously scheduled replicas have correct Scheduled condition
	// This is done early so that even if phases fail, existing replicas have correct conditions
	if err := r.ensureScheduledConditionOnExistingReplicas(ctx, sctx, log); err != nil {
		return reconcile.Result{}, err
	}

	// Phase 1: place Diskful replicas.
	log.V(1).Info("starting Diskful phase", "unscheduledCount", len(sctx.UnscheduledDiskfulReplicas))
	if err := r.scheduleDiskfulPhase(ctx, sctx); err != nil {
		return reconcile.Result{}, r.handlePhaseError(ctx, req.Name, string(v1alpha1.ReplicaTypeDiskful), err, log)
	}
	log.V(1).Info("Diskful phase completed", "scheduledCountTotal", len(sctx.RVRsToSchedule))

	// Phase 2: place Access replicas.
	log.V(1).Info("starting Access phase", "unscheduledCount", len(sctx.UnscheduledAccessReplicas))
	if err := r.scheduleAccessPhase(sctx); err != nil {
		return reconcile.Result{}, r.handlePhaseError(ctx, req.Name, string(v1alpha1.ReplicaTypeAccess), err, log)
	}
	log.V(1).Info("Access phase completed", "scheduledCountTotal", len(sctx.RVRsToSchedule))

	// Phase 3: place TieBreaker replicas.
	log.V(1).Info("starting TieBreaker phase", "unscheduledCount", len(sctx.UnscheduledTieBreakerReplicas))
	if err := r.scheduleTieBreakerPhase(ctx, sctx); err != nil {
		return reconcile.Result{}, r.handlePhaseError(ctx, req.Name, string(v1alpha1.ReplicaTypeTieBreaker), err, log)
	}
	log.V(1).Info("TieBreaker phase completed", "scheduledCountTotal", len(sctx.RVRsToSchedule))

	log.V(1).Info("patching scheduled replicas", "countTotal", len(sctx.RVRsToSchedule))
	if err := r.patchScheduledReplicas(ctx, sctx, log); err != nil {
		return reconcile.Result{}, err
	}

	log.V(1).Info("reconciliation completed successfully", "totalScheduled", len(sctx.RVRsToSchedule))
	return reconcile.Result{}, nil
}

// rvrNotReadyReason describes why an RVR is not ready for scheduling.
type rvrNotReadyReason struct {
	reason  string
	message string
}

// handlePhaseError handles errors that occur during scheduling phases.
// It logs the error, sets failed condition on RVRs, and returns the error.
func (r *Reconciler) handlePhaseError(
	ctx context.Context,
	rvName string,
	phaseName string,
	err error,
	log logr.Logger,
) error {
	log.Error(err, phaseName+" phase failed")
	reason := schedulingErrorToReason(err)
	if setErr := r.setFailedScheduledConditionOnNonScheduledRVRs(ctx, rvName, reason, log); setErr != nil {
		log.Error(setErr, "failed to set Scheduled condition on RVRs after scheduling error")
	}
	return err
}

// schedulingErrorToReason converts a scheduling error to rvrNotReadyReason.
func schedulingErrorToReason(err error) *rvrNotReadyReason {
	reason := v1alpha1.ReasonSchedulingFailed
	switch {
	case errors.Is(err, errSchedulingTopologyConflict):
		reason = v1alpha1.ReasonSchedulingTopologyConflict
	case errors.Is(err, errSchedulingNoCandidateNodes):
		reason = v1alpha1.ReasonSchedulingNoCandidateNodes
	case errors.Is(err, errSchedulingPending):
		reason = v1alpha1.ReasonSchedulingPending
	}
	return &rvrNotReadyReason{
		reason:  reason,
		message: err.Error(),
	}
}

// patchScheduledReplicas patches all scheduled replicas with their assigned node names
// and sets the Scheduled condition to True.
func (r *Reconciler) patchScheduledReplicas(
	ctx context.Context,
	sctx *SchedulingContext,
	log logr.Logger,
) error {
	if len(sctx.RVRsToSchedule) == 0 {
		log.V(1).Info("no scheduled replicas to patch")
		return nil
	}

	for _, rvr := range sctx.RVRsToSchedule {
		log.V(2).Info("patching replica", "rvr", rvr.Name, "nodeName", rvr.Spec.NodeName, "type", rvr.Spec.Type)
		// Create original state for patch (without NodeName and node-name label)
		original := rvr.DeepCopy()
		original.Spec.NodeName = ""

		// Set node-name label together with NodeName.
		// Note: if label is removed manually, it won't be restored until next condition check
		// in ensureScheduledConditionOnExistingReplicas (which runs on each reconcile).
		rvr.Labels, _ = v1alpha1.EnsureLabel(rvr.Labels, v1alpha1.LabelNodeName, rvr.Spec.NodeName)

		// Apply the patch; ignore NotFound errors because the replica may have been deleted meanwhile.
		if err := r.cl.Patch(ctx, rvr, client.MergeFrom(original)); err != nil {
			if apierrors.IsNotFound(err) {
				log.V(1).Info("replica not found during patch, skipping", "rvr", rvr.Name)
				continue // Replica may have been deleted
			}
			return fmt.Errorf("failed to patch RVR %s: %w", rvr.Name, err)
		}

		// Set Scheduled condition to True for successfully scheduled replicas
		if err := r.setScheduledConditionOnRVR(
			ctx,
			rvr,
			metav1.ConditionTrue,
			v1alpha1.ReasonSchedulingReplicaScheduled,
			"",
		); err != nil {
			return fmt.Errorf("failed to set Scheduled condition on RVR %s: %w", rvr.Name, err)
		}
	}
	return nil
}

// ensureScheduledConditionOnExistingReplicas ensures that all already-scheduled replicas
// (those that had NodeName set before this reconcile) have the correct Scheduled condition.
// This handles cases where condition was missing or incorrect.
func (r *Reconciler) ensureScheduledConditionOnExistingReplicas(
	ctx context.Context,
	sctx *SchedulingContext,
	log logr.Logger,
) error {
	// Collect all scheduled replicas that were NOT scheduled in this cycle
	alreadyScheduledReplicas := make([]*v1alpha1.ReplicatedVolumeReplica, 0)

	// Also check for scheduled Access and TieBreaker replicas from RvrList
	for _, rvr := range sctx.RvrList {
		if rvr.Spec.NodeName == "" {
			continue // Skip unscheduled
		}
		alreadyScheduledReplicas = append(alreadyScheduledReplicas, rvr)
	}

	for _, rvr := range alreadyScheduledReplicas {
		log.V(2).Info("fixing Scheduled condition on existing replica", "rvr", rvr.Name)

		// Ensure node-name label is set (restores label if manually removed)
		if err := r.ensureNodeNameLabel(ctx, log, rvr); err != nil {
			return fmt.Errorf("failed to ensure node-name label on RVR %s: %w", rvr.Name, err)
		}

		if err := r.setScheduledConditionOnRVR(
			ctx,
			rvr,
			metav1.ConditionTrue,
			v1alpha1.ReasonSchedulingReplicaScheduled,
			"",
		); err != nil {
			return fmt.Errorf("failed to set Scheduled condition on existing RVR %s: %w", rvr.Name, err)
		}
	}

	return nil
}

// isRVReadyToSchedule checks if the ReplicatedVolume is ready for scheduling.
// Returns nil if ready, or an error wrapped with errSchedulingPending if not ready.
func isRVReadyToSchedule(rv *v1alpha1.ReplicatedVolume) error {
	if rv.Status == nil {
		return fmt.Errorf("%w: ReplicatedVolume status is not initialized", errSchedulingPending)
	}

	if rv.Finalizers == nil {
		return fmt.Errorf("%w: ReplicatedVolume has no finalizers", errSchedulingPending)
	}

	if !slices.Contains(rv.Finalizers, v1alpha1.ControllerAppFinalizer) {
		return fmt.Errorf("%w: ReplicatedVolume is missing controller finalizer", errSchedulingPending)
	}

	if rv.Spec.ReplicatedStorageClassName == "" {
		return fmt.Errorf("%w: ReplicatedStorageClassName is not specified in ReplicatedVolume spec", errSchedulingPending)
	}

	if rv.Spec.Size.IsZero() {
		return fmt.Errorf("%w: ReplicatedVolume size is zero in ReplicatedVolume spec", errSchedulingPending)
	}

	return nil
}

func (r *Reconciler) prepareSchedulingContext(
	ctx context.Context,
	req reconcile.Request,
	log logr.Logger,
) (*SchedulingContext, error) {
	// Fetch the target ReplicatedVolume for this reconcile request.
	rv := &v1alpha1.ReplicatedVolume{}
	if err := r.cl.Get(ctx, req.NamespacedName, rv); err != nil {
		// If the volume no longer exists, exit reconciliation without error.
		if apierrors.IsNotFound(err) {
			log.V(1).Info("ReplicatedVolume not found, skipping reconciliation")
			return nil, nil
		}
		return nil, fmt.Errorf("unable to get ReplicatedVolume: %w", err)
	}

	if err := isRVReadyToSchedule(rv); err != nil {
		return nil, err
	}

	// Load the referenced ReplicatedStorageClass.
	rsc := &v1alpha1.ReplicatedStorageClass{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: rv.Spec.ReplicatedStorageClassName}, rsc); err != nil {
		return nil, fmt.Errorf("unable to get ReplicatedStorageClass: %w", err)
	}

	// List all ReplicatedVolumeReplica resources in the cluster.
	replicaList := &v1alpha1.ReplicatedVolumeReplicaList{}
	if err := r.cl.List(ctx, replicaList); err != nil {
		return nil, fmt.Errorf("unable to list ReplicatedVolumeReplica: %w", err)
	}

	// Collect replicas for this RV:
	// - replicasForRV: non-deleting replicas
	// - nodesWithRVReplica: all occupied nodes (including nodes with deleting replicas)
	replicasForRV, nodesWithRVReplica := collectReplicasAndOccupiedNodes(replicaList.Items, rv.Name)

	rsp := &v1alpha1.ReplicatedStoragePool{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: rsc.Spec.StoragePool}, rsp); err != nil {
		return nil, fmt.Errorf("unable to get ReplicatedStoragePool %s: %w", rsc.Spec.StoragePool, err)
	}

	rspLvgToNodeInfoMap, err := r.getLVGToNodesByStoragePool(ctx, rsp, log)
	if err != nil {
		return nil, fmt.Errorf("unable to get LVG to nodes mapping: %w", err)
	}

	// Build list of RSP nodes WITHOUT replicas - exclude nodes that already have replicas.
	rspNodesWithoutReplica := []string{}
	for _, info := range rspLvgToNodeInfoMap {
		if _, hasReplica := nodesWithRVReplica[info.NodeName]; !hasReplica {
			rspNodesWithoutReplica = append(rspNodesWithoutReplica, info.NodeName)
		}
	}

	nodeNameToZone, err := r.getNodeNameToZoneMap(ctx, log)
	if err != nil {
		return nil, fmt.Errorf("unable to get node to zone mapping: %w", err)
	}

	attachToList := getAttachToNodeList(rv)
	scheduledDiskfulReplicas, unscheduledDiskfulReplicas := getTypedReplicasLists(replicasForRV, v1alpha1.ReplicaTypeDiskful)
	_, unscheduledAccessReplicas := getTypedReplicasLists(replicasForRV, v1alpha1.ReplicaTypeAccess)
	_, unscheduledTieBreakerReplicas := getTypedReplicasLists(replicasForRV, v1alpha1.ReplicaTypeTieBreaker)
	attachToNodesWithoutAnyReplica := getAttachToNodesWithoutAnyReplica(attachToList, nodesWithRVReplica)

	schedulingCtx := &SchedulingContext{
		Log:                           log,
		Rv:                            rv,
		Rsc:                           rsc,
		Rsp:                           rsp,
		RvrList:                       replicasForRV,
		AttachToNodes:                 attachToList,
		AttachToNodesWithoutRvReplica: attachToNodesWithoutAnyReplica,
		RspLvgToNodeInfoMap:           rspLvgToNodeInfoMap,
		NodesWithAnyReplica:           nodesWithRVReplica,
		UnscheduledDiskfulReplicas:    unscheduledDiskfulReplicas,
		ScheduledDiskfulReplicas:      scheduledDiskfulReplicas,
		UnscheduledAccessReplicas:     unscheduledAccessReplicas,
		UnscheduledTieBreakerReplicas: unscheduledTieBreakerReplicas,
		RspNodesWithoutReplica:        rspNodesWithoutReplica,
		NodeNameToZone:                nodeNameToZone,
	}

	return schedulingCtx, nil
}

func (r *Reconciler) scheduleDiskfulPhase(
	ctx context.Context,
	sctx *SchedulingContext,
) error {
	if len(sctx.UnscheduledDiskfulReplicas) == 0 {
		sctx.Log.V(1).Info("no unscheduled Diskful replicas. Skipping Diskful phase.")
		return nil
	}

	candidateNodes := sctx.RspNodesWithoutReplica
	sctx.Log.V(1).Info("Diskful phase: initial candidate nodes", "count", len(candidateNodes), "nodes", candidateNodes)

	// Try to schedule replicas, collect failure reason if any step fails
	failureReason := r.tryScheduleDiskfulReplicas(ctx, sctx, candidateNodes)

	// Set Scheduled=False condition on remaining unscheduled Diskful replicas
	if len(sctx.UnscheduledDiskfulReplicas) > 0 && failureReason != nil {
		sctx.Log.V(1).Info("setting Scheduled=False on unscheduled Diskful replicas",
			"count", len(sctx.UnscheduledDiskfulReplicas),
			"reason", failureReason.reason)
		return r.setScheduledConditionOnRVRs(
			ctx,
			sctx.UnscheduledDiskfulReplicas,
			metav1.ConditionFalse,
			failureReason.reason,
			failureReason.message,
			sctx.Log,
		)
	}

	return nil
}

// tryScheduleDiskfulReplicas attempts to schedule Diskful replicas and returns failure reason if not all could be scheduled.
func (r *Reconciler) tryScheduleDiskfulReplicas(
	ctx context.Context,
	sctx *SchedulingContext,
	candidateNodes []string,
) *rvrNotReadyReason {
	// Apply topology constraints (also checks for empty candidates)
	if err := r.applyTopologyFilter(candidateNodes, true, sctx); err != nil {
		sctx.Log.V(1).Info("topology filter failed", "error", err)
		return schedulingErrorToReason(err)
	}

	// Apply capacity filtering
	if err := r.applyCapacityFilterAndScoreCandidates(ctx, sctx); err != nil {
		sctx.Log.V(1).Info("capacity filter failed", "error", err)
		return schedulingErrorToReason(err)
	}
	sctx.Log.V(1).Info("capacity filter applied and candidates scored", "zonesCount", len(sctx.ZonesToNodeCandidatesMap))

	sctx.ApplyAttachToBonus()
	sctx.Log.V(1).Info("attachTo bonus applied")

	// Assign replicas in best-effort mode
	assignedReplicas, err := r.assignReplicasToNodes(sctx, sctx.UnscheduledDiskfulReplicas, v1alpha1.ReplicaTypeDiskful, true)
	if err != nil {
		sctx.Log.Error(err, "unexpected error during replica assignment")
		return schedulingErrorToReason(err)
	}
	sctx.Log.V(1).Info("Diskful replicas assigned", "count", len(assignedReplicas))

	sctx.UpdateAfterScheduling(assignedReplicas)

	// Return failure reason if not all replicas were scheduled
	if len(sctx.UnscheduledDiskfulReplicas) > 0 {
		return schedulingErrorToReason(fmt.Errorf("%w: not enough candidate nodes to schedule all Diskful replicas", errSchedulingNoCandidateNodes))
	}

	return nil
}

// assignReplicasToNodes assigns nodes to unscheduled replicas based on topology and node scores.
// For Ignored topology: selects best nodes by score.
// For Zonal topology: selects the best zone first (by total score), then best nodes from that zone.
// For TransZonal topology: distributes replicas across zones, picking zones with fewer scheduled replicas first.
// replicaTypeFilter: for TransZonal, which replica types to count for zone balancing (empty = all types).
// bestEffort: if true, don't return error when not enough nodes.
// Note: This function returns the list of replicas that were assigned nodes in this call.
func (r *Reconciler) assignReplicasToNodes(
	sctx *SchedulingContext,
	unscheduledReplicas []*v1alpha1.ReplicatedVolumeReplica,
	replicaTypeFilter v1alpha1.ReplicaType,
	bestEffort bool,
) ([]*v1alpha1.ReplicatedVolumeReplica, error) {
	if len(unscheduledReplicas) == 0 {
		sctx.Log.Info("no unscheduled replicas to assign", "rv", sctx.Rv.Name)
		return nil, nil
	}

	switch sctx.Rsc.Spec.Topology {
	case topologyIgnored:
		return r.assignReplicasIgnoredTopology(sctx, unscheduledReplicas, bestEffort)
	case topologyZonal:
		return r.assignReplicasZonalTopology(sctx, unscheduledReplicas, bestEffort)
	case topologyTransZonal:
		return r.assignReplicasTransZonalTopology(sctx, unscheduledReplicas, replicaTypeFilter, bestEffort)
	default:
		return nil, fmt.Errorf("unknown topology: %s", sctx.Rsc.Spec.Topology)
	}
}

// assignReplicasIgnoredTopology assigns replicas to best nodes by score (ignoring zones).
// If bestEffort=true, assigns as many as possible without error.
// Returns the list of replicas that were assigned nodes.
func (r *Reconciler) assignReplicasIgnoredTopology(
	sctx *SchedulingContext,
	unscheduledReplicas []*v1alpha1.ReplicatedVolumeReplica,
	bestEffort bool,
) ([]*v1alpha1.ReplicatedVolumeReplica, error) {
	sctx.Log.V(1).Info("assigning replicas with Ignored topology", "replicasCount", len(unscheduledReplicas), "bestEffort", bestEffort)
	// Collect all candidates from all zones
	var allCandidates []NodeCandidate
	for _, candidates := range sctx.ZonesToNodeCandidatesMap {
		allCandidates = append(allCandidates, candidates...)
	}
	sctx.Log.V(2).Info("collected candidates", "count", len(allCandidates))

	// Assign nodes to replicas
	var assignedReplicas []*v1alpha1.ReplicatedVolumeReplica
	for _, rvr := range unscheduledReplicas {
		selectedNode, remaining := SelectAndRemoveBestNode(allCandidates)
		if selectedNode == "" {
			sctx.Log.V(1).Info("not enough candidate nodes for all replicas", "assigned", len(assignedReplicas), "total", len(unscheduledReplicas))
			if bestEffort {
				break // Best-effort: return what we have
			}
			return assignedReplicas, fmt.Errorf("%w: not enough candidate nodes for all replicas", errSchedulingNoCandidateNodes)
		}
		allCandidates = remaining

		// Mark replica for scheduling
		sctx.Log.V(2).Info("assigned replica to node", "rvr", rvr.Name, "node", selectedNode)
		rvr.Spec.NodeName = selectedNode
		assignedReplicas = append(assignedReplicas, rvr)
	}

	return assignedReplicas, nil
}

// assignReplicasZonalTopology selects the best zone first, then assigns replicas to best nodes in that zone.
// If bestEffort=true, assigns as many as possible without error.
// Returns the list of replicas that were assigned nodes.
func (r *Reconciler) assignReplicasZonalTopology(
	sctx *SchedulingContext,
	unscheduledReplicas []*v1alpha1.ReplicatedVolumeReplica,
	bestEffort bool,
) ([]*v1alpha1.ReplicatedVolumeReplica, error) {
	sctx.Log.V(1).Info("assigning replicas with Zonal topology", "replicasCount", len(unscheduledReplicas), "bestEffort", bestEffort)
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
		sctx.Log.V(2).Info("evaluating zone", "zone", zone, "candidatesCount", len(candidates), "totalScore", totalScore, "zoneScore", zoneScore)
		if zoneScore > bestZoneScore {
			bestZoneScore = zoneScore
			bestZone = zone
		}
	}

	if bestZone == "" {
		sctx.Log.V(1).Info("no zones with candidates available")
		if bestEffort {
			return nil, nil // Best-effort: no candidates, no error
		}
		return nil, fmt.Errorf("%w: no zones with candidates available", errSchedulingNoCandidateNodes)
	}
	sctx.Log.V(1).Info("selected best zone", "zone", bestZone, "score", bestZoneScore)

	// Assign nodes to replicas
	var assignedReplicas []*v1alpha1.ReplicatedVolumeReplica
	for _, rvr := range unscheduledReplicas {
		selectedNode, remaining := SelectAndRemoveBestNode(sctx.ZonesToNodeCandidatesMap[bestZone])
		if selectedNode == "" {
			sctx.Log.V(1).Info("not enough candidate nodes in zone", "zone", bestZone, "assigned", len(assignedReplicas), "total", len(unscheduledReplicas))
			if bestEffort {
				break // Best-effort: return what we have
			}
			return assignedReplicas, fmt.Errorf("%w: not enough candidate nodes in zone %s for all replicas", errSchedulingNoCandidateNodes, bestZone)
		}
		sctx.ZonesToNodeCandidatesMap[bestZone] = remaining

		// Mark replica for scheduling
		sctx.Log.V(2).Info("assigned replica to node in zone", "rvr", rvr.Name, "node", selectedNode, "zone", bestZone)
		rvr.Spec.NodeName = selectedNode
		assignedReplicas = append(assignedReplicas, rvr)
	}

	return assignedReplicas, nil
}

// assignReplicasTransZonalTopology distributes replicas across zones, preferring zones with fewer scheduled replicas of the same type.
// It modifies rvr.Spec.NodeName and adds replicas to sctx.RVRsToSchedule for later patching.
// If bestEffort=true, assigns as many as possible without error when distribution constraints can't be met.
// Returns the list of replicas that were assigned nodes.
func (r *Reconciler) assignReplicasTransZonalTopology(
	sctx *SchedulingContext,
	unscheduledReplicas []*v1alpha1.ReplicatedVolumeReplica,
	replicaTypeFilter v1alpha1.ReplicaType,
	bestEffort bool,
) ([]*v1alpha1.ReplicatedVolumeReplica, error) {
	if len(unscheduledReplicas) == 0 {
		return nil, nil
	}

	sctx.Log.V(1).Info("assigning replicas with TransZonal topology", "replicasCount", len(unscheduledReplicas), "replicaTypeFilter", replicaTypeFilter, "bestEffort", bestEffort)

	// Count already scheduled replicas per zone (filtered by type if specified)
	zoneReplicaCount := countReplicasByZone(sctx.RvrList, replicaTypeFilter, sctx.NodeNameToZone)
	sctx.Log.V(2).Info("current zone replica distribution", "zoneReplicaCount", zoneReplicaCount)

	// Get all allowed zones for TransZonal topology
	allowedZones := getAllowedZones(nil, sctx.Rsc.Spec.Zones, sctx.NodeNameToZone)
	sctx.Log.V(2).Info("allowed zones for TransZonal", "zones", allowedZones)

	// Build set of zones that have available candidates
	availableZones := make(map[string]struct{})
	for zone, candidates := range sctx.ZonesToNodeCandidatesMap {
		if len(candidates) > 0 {
			availableZones[zone] = struct{}{}
		}
	}

	// For each unscheduled replica, pick the zone with fewest replicas, then best node
	var assignedReplicas []*v1alpha1.ReplicatedVolumeReplica
	for i, rvr := range unscheduledReplicas {
		sctx.Log.V(2).Info("scheduling replica", "index", i, "rvr", rvr.Name)

		// Find zone with minimum replica count among ALL allowed zones
		globalMinZone, globalMinCount := findZoneWithMinReplicaCount(allowedZones, zoneReplicaCount)

		// Find zone with minimum replica count that has available candidates
		selectedZone, availableMinCount := findZoneWithMinReplicaCount(availableZones, zoneReplicaCount)

		if selectedZone == "" {
			// No more zones with available candidates
			sctx.Log.V(1).Info("no more zones with available candidates", "assigned", len(assignedReplicas), "total", len(unscheduledReplicas))
			if bestEffort {
				break // Best-effort: return what we have
			}
			return assignedReplicas, fmt.Errorf(
				"%w: no zones with available nodes to place replica",
				errSchedulingNoCandidateNodes,
			)
		}

		// Check if we can guarantee even distribution:
		// If the global minimum (across all allowed zones) is less than the minimum among available zones,
		// it means there's a zone that should have replicas but has no available nodes.
		if globalMinCount < availableMinCount {
			sctx.Log.V(1).Info("cannot guarantee even distribution: zone with fewer replicas has no available nodes",
				"unavailableZone", globalMinZone, "replicasInZone", globalMinCount, "minReplicasInAvailableZones", availableMinCount)
			if bestEffort {
				break // Best-effort: return what we have, can't maintain even distribution
			}
			return assignedReplicas, fmt.Errorf(
				"%w: zone %q has %d replicas but no available nodes; replica should be placed there to maintain even distribution across zones",
				errSchedulingNoCandidateNodes,
				globalMinZone,
				globalMinCount,
			)
		}

		sctx.Log.V(2).Info("selected zone for replica", "zone", selectedZone, "replicaCount", availableMinCount)

		// Select best node from zone and remove it from candidates
		selectedNode, remaining := SelectAndRemoveBestNode(sctx.ZonesToNodeCandidatesMap[selectedZone])
		if selectedNode == "" {
			// No available node in this zone - stop scheduling remaining replicas
			sctx.Log.V(1).Info("no available node in selected zone", "zone", selectedZone)
			return assignedReplicas, nil
		}
		sctx.ZonesToNodeCandidatesMap[selectedZone] = remaining

		// Update availableZones if zone has no more candidates
		if len(remaining) == 0 {
			delete(availableZones, selectedZone)
		}

		// Update replica node name
		sctx.Log.V(2).Info("assigned replica to node", "rvr", rvr.Name, "node", selectedNode, "zone", selectedZone)
		rvr.Spec.NodeName = selectedNode
		assignedReplicas = append(assignedReplicas, rvr)

		// Update zone replica count
		zoneReplicaCount[selectedZone]++
	}

	sctx.Log.V(1).Info("TransZonal assignment completed", "assigned", len(assignedReplicas))
	return assignedReplicas, nil
}

//nolint:unparam // error is always nil by design - Access phase never fails
func (r *Reconciler) scheduleAccessPhase(
	sctx *SchedulingContext,
) error {
	// Spec «Access»: phase works only when:
	// - rv.spec.attachTo is set AND not all attachTo nodes have replicas
	// - rsc.spec.volumeAccess != Local
	if len(sctx.AttachToNodes) == 0 {
		sctx.Log.V(1).Info("skipping Access phase: no attachTo nodes")
		return nil
	}

	if sctx.Rsc.Spec.VolumeAccess == "Local" {
		sctx.Log.V(1).Info("skipping Access phase: volumeAccess is Local")
		return nil
	}

	if len(sctx.UnscheduledAccessReplicas) == 0 {
		sctx.Log.V(1).Info("no unscheduled Access replicas")
		return nil
	}
	sctx.Log.V(1).Info("Access phase: processing replicas", "unscheduledCount", len(sctx.UnscheduledAccessReplicas))

	// Spec «Access»: exclude nodes that already host any replica of this RV (any type)
	// Use AttachToNodesWithoutRvReplica which already contains attachTo nodes without any replica
	candidateNodes := sctx.AttachToNodesWithoutRvReplica
	if len(candidateNodes) == 0 {
		// All attachTo nodes already have replicas; nothing to do.
		// Spec «Access»: it is allowed to have replicas that could not be scheduled
		sctx.Log.V(1).Info("Access phase: all attachTo nodes already have replicas")
		return nil
	}
	sctx.Log.V(1).Info("Access phase: candidate nodes", "count", len(candidateNodes), "nodes", candidateNodes)

	// We are not required to place all Access replicas or to cover all attachTo nodes.
	// Spec «Access»: it is allowed to have nodes in rv.spec.attachTo without enough replicas
	// Spec «Access»: it is allowed to have replicas that could not be scheduled
	nodesToFill := min(len(candidateNodes), len(sctx.UnscheduledAccessReplicas))
	sctx.Log.V(1).Info("Access phase: scheduling replicas", "nodesToFill", nodesToFill)

	var assignedReplicas []*v1alpha1.ReplicatedVolumeReplica
	for i := range nodesToFill {
		nodeName := candidateNodes[i]
		rvr := sctx.UnscheduledAccessReplicas[i]

		sctx.Log.V(2).Info("Access phase: assigning replica", "rvr", rvr.Name, "node", nodeName)
		rvr.Spec.NodeName = nodeName
		assignedReplicas = append(assignedReplicas, rvr)
	}

	// Update context after scheduling
	sctx.UpdateAfterScheduling(assignedReplicas)
	sctx.Log.V(1).Info("Access phase: completed", "assigned", len(assignedReplicas))

	return nil
}

func (r *Reconciler) scheduleTieBreakerPhase(
	ctx context.Context,
	sctx *SchedulingContext,
) error {
	if len(sctx.UnscheduledTieBreakerReplicas) == 0 {
		sctx.Log.V(1).Info("no unscheduled TieBreaker replicas")
		return nil
	}
	sctx.Log.V(1).Info("TieBreaker phase: processing replicas", "unscheduledCount", len(sctx.UnscheduledTieBreakerReplicas), "topology", sctx.Rsc.Spec.Topology)

	// Build candidate nodes (nodes without any replica of this RV)
	candidateNodes := r.getTieBreakerCandidateNodes(sctx)
	sctx.Log.V(2).Info("TieBreaker phase: candidate nodes", "count", len(candidateNodes))

	failureReason := r.tryScheduleTieBreakerReplicas(sctx, candidateNodes)

	// Set Scheduled=False condition on remaining unscheduled TieBreaker replicas
	if len(sctx.UnscheduledTieBreakerReplicas) > 0 && failureReason != nil {
		if err := r.setScheduledConditionOnRVRs(
			ctx,
			sctx.UnscheduledTieBreakerReplicas,
			metav1.ConditionFalse,
			failureReason.reason,
			failureReason.message,
			sctx.Log,
		); err != nil {
			return err
		}
	}

	return nil
}

// tryScheduleTieBreakerReplicas attempts to schedule TieBreaker replicas and returns failure reason if not all could be scheduled.
func (r *Reconciler) tryScheduleTieBreakerReplicas(
	sctx *SchedulingContext,
	candidateNodes []string,
) *rvrNotReadyReason {
	// Apply topology filter (isDiskfulPhase=false)
	if err := r.applyTopologyFilter(candidateNodes, false, sctx); err != nil {
		sctx.Log.V(1).Info("topology filter failed", "error", err)
		return schedulingErrorToReason(err)
	}

	// Assign replicas: count ALL replica types for zone balancing, best-effort mode
	assignedReplicas, err := r.assignReplicasToNodes(sctx, sctx.UnscheduledTieBreakerReplicas, v1alpha1.ReplicaType(""), true)
	if err != nil {
		sctx.Log.Error(err, "unexpected error during TieBreaker replica assignment")
		return schedulingErrorToReason(err)
	}

	// Update context after scheduling
	sctx.UpdateAfterScheduling(assignedReplicas)
	sctx.Log.V(1).Info("TieBreaker phase: completed", "assigned", len(assignedReplicas))

	// Return failure reason if not all replicas were scheduled
	if len(sctx.UnscheduledTieBreakerReplicas) > 0 {
		return schedulingErrorToReason(fmt.Errorf("%w: not enough candidate nodes to schedule all TieBreaker replicas", errSchedulingNoCandidateNodes))
	}

	return nil
}

// getTieBreakerCandidateNodes returns nodes that can host TieBreaker replicas:
// - Nodes without any replica of this RV
// Zone filtering is done later in applyTopologyFilter which considers scheduled Diskful replicas
func (r *Reconciler) getTieBreakerCandidateNodes(sctx *SchedulingContext) []string {
	var candidateNodes []string
	for nodeName := range sctx.NodeNameToZone {
		if _, hasReplica := sctx.NodesWithAnyReplica[nodeName]; hasReplica {
			continue
		}
		candidateNodes = append(candidateNodes, nodeName)
	}
	return candidateNodes
}

func getAttachToNodeList(rv *v1alpha1.ReplicatedVolume) []string {
	return slices.Clone(rv.Spec.AttachTo)
}

// collectReplicasAndOccupiedNodes filters replicas for a given RV and returns:
// - activeReplicas: non-deleting replicas (both scheduled and unscheduled)
// - occupiedNodes: all nodes with replicas (including deleting ones) to prevent scheduling collisions
func collectReplicasAndOccupiedNodes(
	allReplicas []v1alpha1.ReplicatedVolumeReplica,
	rvName string,
) (activeReplicas []*v1alpha1.ReplicatedVolumeReplica, occupiedNodes map[string]struct{}) {
	occupiedNodes = make(map[string]struct{})

	for i := range allReplicas {
		rvr := &allReplicas[i]
		if rvr.Spec.ReplicatedVolumeName != rvName {
			continue
		}
		// Track nodes from ALL replicas (including deleting ones) for occupancy
		// This prevents scheduling new replicas on nodes where replicas are being deleted
		if rvr.Spec.NodeName != "" {
			occupiedNodes[rvr.Spec.NodeName] = struct{}{}
		}
		// Only include non-deleting replicas (active replicas)
		if rvr.DeletionTimestamp.IsZero() {
			activeReplicas = append(activeReplicas, rvr)
		}
	}
	return activeReplicas, occupiedNodes
}

func getTypedReplicasLists(
	replicasForRV []*v1alpha1.ReplicatedVolumeReplica,
	replicaType v1alpha1.ReplicaType,
) (scheduled, unscheduled []*v1alpha1.ReplicatedVolumeReplica) {
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

// setScheduledConditionOnRVRs sets the Scheduled condition on a list of RVRs.
func (r *Reconciler) setScheduledConditionOnRVRs(
	ctx context.Context,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	status metav1.ConditionStatus,
	reason string,
	message string,
	log logr.Logger,
) error {
	for _, rvr := range rvrs {
		if err := r.setScheduledConditionOnRVR(ctx, rvr, status, reason, message); err != nil {
			log.Error(err, "failed to set Scheduled condition", "rvr", rvr.Name)
			return err
		}
	}
	return nil
}

// setScheduledConditionOnRVR sets the Scheduled condition on a single RVR.
func (r *Reconciler) setScheduledConditionOnRVR(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	status metav1.ConditionStatus,
	reason string,
	message string,
) error {
	patch := client.MergeFrom(rvr.DeepCopy())

	if rvr.Status == nil {
		rvr.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{}
	}

	changed := meta.SetStatusCondition(
		&rvr.Status.Conditions,
		metav1.Condition{
			Type:               v1alpha1.ConditionTypeScheduled,
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

// ensureNodeNameLabel ensures the node-name label is set on RVR matching its NodeName.
// This restores label if manually removed.
func (r *Reconciler) ensureNodeNameLabel(
	ctx context.Context,
	log logr.Logger,
	rvr *v1alpha1.ReplicatedVolumeReplica,
) error {
	if rvr.Spec.NodeName == "" {
		return nil
	}

	labels, changed := v1alpha1.EnsureLabel(rvr.Labels, v1alpha1.LabelNodeName, rvr.Spec.NodeName)
	if !changed {
		return nil
	}

	log.V(2).Info("restoring node-name label on RVR", "rvr", rvr.Name, "node", rvr.Spec.NodeName)

	patch := client.MergeFrom(rvr.DeepCopy())
	rvr.Labels = labels
	if err := r.cl.Patch(ctx, rvr, patch); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	return nil
}

// setFailedScheduledConditionOnNonScheduledRVRs sets the Scheduled condition to False on all RVRs
// belonging to the given RV when the RV is not ready for scheduling.
func (r *Reconciler) setFailedScheduledConditionOnNonScheduledRVRs(
	ctx context.Context,
	rvName string,
	notReadyReason *rvrNotReadyReason,
	log logr.Logger,
) error {
	// List all ReplicatedVolumeReplica resources in the cluster.
	replicaList := &v1alpha1.ReplicatedVolumeReplicaList{}
	if err := r.cl.List(ctx, replicaList); err != nil {
		log.Error(err, "unable to list ReplicatedVolumeReplica")
		return err
	}

	// Update Scheduled condition on all RVRs belonging to this RV.
	for _, rvr := range replicaList.Items {
		// TODO: fix checking for deletion
		if rvr.Spec.ReplicatedVolumeName != rvName || !rvr.DeletionTimestamp.IsZero() {
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
			log.Error(err, "failed to set Scheduled condition", "rvr", rvr.Name, "reason", notReadyReason.reason, "message", notReadyReason.message)
			return err
		}
	}

	return nil
}

func getAttachToNodesWithoutAnyReplica(
	attachToList []string,
	nodesWithRVReplica map[string]struct{},
) []string {
	attachToNodesWithoutAnyReplica := make([]string, 0, len(attachToList))

	for _, node := range attachToList {
		if _, hasReplica := nodesWithRVReplica[node]; !hasReplica {
			attachToNodesWithoutAnyReplica = append(attachToNodesWithoutAnyReplica, node)
		}
	}
	return attachToNodesWithoutAnyReplica
}

// applyTopologyFilter groups candidate nodes by zones based on RSC topology.
// isDiskfulPhase affects only Zonal topology:
//   - true: falls back to attachTo or any allowed zone if no ScheduledDiskfulReplicas
//   - false: returns error if no ScheduledDiskfulReplicas (TieBreaker needs Diskful zone)
//
// For Ignored and TransZonal, logic is the same for both phases.
func (r *Reconciler) applyTopologyFilter(
	candidateNodes []string,
	isDiskfulPhase bool,
	sctx *SchedulingContext,
) error {
	sctx.Log.V(1).Info("applying topology filter", "topology", sctx.Rsc.Spec.Topology, "candidatesCount", len(candidateNodes), "isDiskfulPhase", isDiskfulPhase)

	switch sctx.Rsc.Spec.Topology {
	case topologyIgnored:
		// Same for both phases: all candidates in single "zone"
		sctx.Log.V(1).Info("topology filter: Ignored - creating single zone with all candidates")
		nodeCandidates := make([]NodeCandidate, 0, len(candidateNodes))
		for _, nodeName := range candidateNodes {
			nodeCandidates = append(nodeCandidates, NodeCandidate{
				Name:  nodeName,
				Score: 0,
			})
		}
		sctx.ZonesToNodeCandidatesMap = map[string][]NodeCandidate{
			topologyIgnored: nodeCandidates,
		}

	case topologyZonal:
		sctx.Log.V(1).Info("topology filter: Zonal - grouping candidates by zone")
		if err := r.applyZonalTopologyFilter(candidateNodes, isDiskfulPhase, sctx); err != nil {
			return err
		}

	case topologyTransZonal:
		// Same for both phases: group by allowed zones
		sctx.Log.V(1).Info("topology filter: TransZonal - distributing across zones")
		allowedZones := getAllowedZones(nil, sctx.Rsc.Spec.Zones, sctx.NodeNameToZone)
		sctx.ZonesToNodeCandidatesMap = r.groupCandidateNodesByZone(candidateNodes, allowedZones, sctx)

	default:
		return fmt.Errorf("unknown RSC topology: %s", sctx.Rsc.Spec.Topology)
	}

	// Check for empty candidates after topology filtering
	if len(sctx.ZonesToNodeCandidatesMap) == 0 {
		return fmt.Errorf("%w: no candidate nodes found after topology filtering", errSchedulingNoCandidateNodes)
	}
	sctx.Log.V(1).Info("topology filter applied", "zonesCount", len(sctx.ZonesToNodeCandidatesMap))
	return nil
}

// applyZonalTopologyFilter handles Zonal topology logic.
// For isDiskfulPhase=true: ScheduledDiskfulReplicas -> attachTo -> any allowed zone
// For isDiskfulPhase=false: ScheduledDiskfulReplicas -> ERROR (TieBreaker needs Diskful zone)
func (r *Reconciler) applyZonalTopologyFilter(
	candidateNodes []string,
	isDiskfulPhase bool,
	sctx *SchedulingContext,
) error {
	sctx.Log.V(1).Info("applyZonalTopologyFilter: starting", "candidatesCount", len(candidateNodes), "isDiskfulPhase", isDiskfulPhase)

	// Find zones of already scheduled diskful replicas
	var zonesWithScheduledDiskfulReplicas []string
	for _, rvr := range sctx.ScheduledDiskfulReplicas {
		zone, ok := sctx.NodeNameToZone[rvr.Spec.NodeName]
		if !ok || zone == "" {
			return fmt.Errorf("%w: scheduled diskful replica %s is on node %s without zone label for Zonal topology",
				errSchedulingTopologyConflict, rvr.Name, rvr.Spec.NodeName)
		}
		if !slices.Contains(zonesWithScheduledDiskfulReplicas, zone) {
			zonesWithScheduledDiskfulReplicas = append(zonesWithScheduledDiskfulReplicas, zone)
		}
	}
	sctx.Log.V(2).Info("applyZonalTopologyFilter: zones with scheduled diskful replicas", "zones", zonesWithScheduledDiskfulReplicas)

	// For Zonal topology, all scheduled diskful replicas must be in the same zone
	if len(zonesWithScheduledDiskfulReplicas) > 1 {
		return fmt.Errorf("%w: scheduled diskful replicas are in multiple zones %v for Zonal topology",
			errSchedulingTopologyConflict, zonesWithScheduledDiskfulReplicas)
	}

	// Determine target zones based on phase
	var targetZones []string

	switch {
	case len(zonesWithScheduledDiskfulReplicas) > 0:
		// Use zone of scheduled Diskful replicas
		targetZones = zonesWithScheduledDiskfulReplicas
	case !isDiskfulPhase:
		// TieBreaker phase: no ScheduledDiskfulReplicas is an error
		return fmt.Errorf("%w: cannot schedule TieBreaker for Zonal topology: no Diskful replicas scheduled",
			errSchedulingNoCandidateNodes)
	default:
		// Diskful phase: fallback to attachTo zones
		for _, nodeName := range sctx.AttachToNodes {
			zone, ok := sctx.NodeNameToZone[nodeName]
			if !ok || zone == "" {
				return fmt.Errorf("%w: attachTo node %s has no zone label for Zonal topology",
					errSchedulingTopologyConflict, nodeName)
			}
			if !slices.Contains(targetZones, zone) {
				targetZones = append(targetZones, zone)
			}
		}
		sctx.Log.V(2).Info("applyZonalTopologyFilter: attachTo zones", "zones", targetZones)
		// If still empty, getAllowedZones will use rsc.spec.zones or all cluster zones
	}

	sctx.Log.V(2).Info("applyZonalTopologyFilter: target zones", "zones", targetZones)

	// Build candidate nodes map
	allowedZones := getAllowedZones(targetZones, sctx.Rsc.Spec.Zones, sctx.NodeNameToZone)
	sctx.Log.V(2).Info("applyZonalTopologyFilter: allowed zones", "zones", allowedZones)

	// Group candidate nodes by zone
	sctx.ZonesToNodeCandidatesMap = r.groupCandidateNodesByZone(candidateNodes, allowedZones, sctx)
	sctx.Log.V(1).Info("applyZonalTopologyFilter: completed", "zonesCount", len(sctx.ZonesToNodeCandidatesMap))
	return nil
}

// applyCapacityFilterAndScoreCandidates filters nodes by available storage capacity using the scheduler extender.
// It converts nodes to LVGs, queries the extender for capacity scores, and updates ZonesToNodeCandidatesMap.
func (r *Reconciler) applyCapacityFilterAndScoreCandidates(
	ctx context.Context,
	sctx *SchedulingContext,
) error {
	// Collect all candidate nodes from ZonesToNodeCandidatesMap
	candidateNodeSet := make(map[string]struct{})
	for _, candidates := range sctx.ZonesToNodeCandidatesMap {
		for _, candidate := range candidates {
			candidateNodeSet[candidate.Name] = struct{}{}
		}
	}

	// Build LVG list from RspLvgToNodeInfoMap, but only for nodes in candidateNodeSet
	reqLVGs := make([]schedulerExtenderLVG, 0, len(sctx.RspLvgToNodeInfoMap))
	for lvgName, info := range sctx.RspLvgToNodeInfoMap {
		// Skip LVGs whose nodes are not in the candidate list
		if _, ok := candidateNodeSet[info.NodeName]; !ok {
			continue
		}
		reqLVGs = append(reqLVGs, schedulerExtenderLVG{
			Name:         lvgName,
			ThinPoolName: info.ThinPoolName,
		})
	}

	if len(reqLVGs) == 0 {
		// No LVGs to check — no candidate nodes have LVGs from the storage pool
		sctx.Log.V(1).Info("no candidate nodes have LVGs from storage pool", "storagePool", sctx.Rsc.Spec.StoragePool)
		return fmt.Errorf("%w: no candidate nodes have LVGs from storage pool %s", errSchedulingNoCandidateNodes, sctx.Rsc.Spec.StoragePool)
	}

	// Convert RSP volume type to scheduler extender volume type
	var volType string
	switch sctx.Rsp.Spec.Type {
	case "LVMThin":
		volType = "thin"
	case "LVM":
		volType = "thick"
	default:
		return fmt.Errorf("RSP volume type is not supported: %s", sctx.Rsp.Spec.Type)
	}
	size := sctx.Rv.Spec.Size.Value()

	// Query scheduler extender for LVG scores
	volumeInfo := VolumeInfo{
		Name: sctx.Rv.Name,
		Size: size,
		Type: volType,
	}
	lvgScores, err := r.extenderClient.queryLVGScores(ctx, reqLVGs, volumeInfo)
	if err != nil {
		sctx.Log.Error(err, "scheduler extender query failed")
		return fmt.Errorf("%w: %v", errSchedulingNoCandidateNodes, err)
	}

	// Build map of node -> score based on LVG scores
	// Node gets the score of its LVG (if LVG is in the response)
	nodeScores := make(map[string]int)
	for lvgName, info := range sctx.RspLvgToNodeInfoMap {
		if score, ok := lvgScores[lvgName]; ok {
			nodeScores[info.NodeName] = score
		}
	}

	// Filter ZonesToNodeCandidatesMap: keep only nodes that have score (i.e., their LVG was returned)
	// and update their scores
	for zone, candidates := range sctx.ZonesToNodeCandidatesMap {
		filteredCandidates := make([]NodeCandidate, 0, len(candidates))
		for _, candidate := range candidates {
			if score, ok := nodeScores[candidate.Name]; ok {
				filteredCandidates = append(filteredCandidates, NodeCandidate{
					Name:  candidate.Name,
					Score: score,
				})
			}
			// Node not in response — skip (no capacity)
		}
		if len(filteredCandidates) > 0 {
			sctx.ZonesToNodeCandidatesMap[zone] = filteredCandidates
		} else {
			delete(sctx.ZonesToNodeCandidatesMap, zone)
		}
	}

	if len(sctx.ZonesToNodeCandidatesMap) == 0 {
		sctx.Log.V(1).Info("no nodes with sufficient storage space found after capacity filtering")
		return fmt.Errorf("%w: no nodes with sufficient storage space found", errSchedulingNoCandidateNodes)
	}

	return nil
}

// countReplicasByZone counts how many replicas are scheduled in each zone.
// If replicaType is not empty, only replicas of that type are counted.
// If replicaType is empty, all replica types are counted.
func countReplicasByZone(
	replicas []*v1alpha1.ReplicatedVolumeReplica,
	replicaType v1alpha1.ReplicaType,
	nodeNameToZone map[string]string,
) map[string]int {
	zoneReplicaCount := make(map[string]int)
	for _, rvr := range replicas {
		if replicaType != "" && rvr.Spec.Type != replicaType {
			continue
		}
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
func (r *Reconciler) groupCandidateNodesByZone(
	candidateNodes []string,
	allowedZones map[string]struct{},
	sctx *SchedulingContext,
) map[string][]NodeCandidate {
	zonesToCandidates := make(map[string][]NodeCandidate)

	for _, nodeName := range candidateNodes {
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

// getAllowedZones determines which zones should be used for replica placement.
// Priority order:
// 1. If targetZones is provided and not empty, use those zones
// 2. If RSC spec defines zones, use those
// 3. Otherwise, use all zones from the cluster (from NodeNameToZone map)
func getAllowedZones(targetZones []string, rscZones []string, nodeNameToZone map[string]string) map[string]struct{} {
	allowedZones := make(map[string]struct{})

	switch {
	case len(targetZones) > 0:
		for _, zone := range targetZones {
			allowedZones[zone] = struct{}{}
		}
	case len(rscZones) > 0:
		for _, zone := range rscZones {
			allowedZones[zone] = struct{}{}
		}
	default:
		for _, zone := range nodeNameToZone {
			if zone != "" {
				allowedZones[zone] = struct{}{}
			}
		}
	}

	return allowedZones
}

func (r *Reconciler) getLVGToNodesByStoragePool(
	ctx context.Context,
	rsp *v1alpha1.ReplicatedStoragePool,
	log logr.Logger,
) (map[string]LvgInfo, error) {
	if rsp == nil || len(rsp.Spec.LVMVolumeGroups) == 0 {
		return nil, fmt.Errorf("storage pool does not define any LVGs")
	}

	lvgList := &snc.LVMVolumeGroupList{}
	if err := r.cl.List(ctx, lvgList); err != nil {
		log.Error(err, "unable to list LVMVolumeGroup")
		return nil, err
	}

	// Build lookup map: LVG name -> LVG object
	lvgByName := make(map[string]*snc.LVMVolumeGroup, len(lvgList.Items))
	for i := range lvgList.Items {
		lvgByName[lvgList.Items[i].Name] = &lvgList.Items[i]
	}

	// Build result map from RSP's LVGs
	result := make(map[string]LvgInfo, len(rsp.Spec.LVMVolumeGroups))
	for _, rspLvg := range rsp.Spec.LVMVolumeGroups {
		lvg, ok := lvgByName[rspLvg.Name]
		if !ok || len(lvg.Status.Nodes) == 0 {
			continue
		}
		result[rspLvg.Name] = LvgInfo{
			NodeName:     lvg.Status.Nodes[0].Name,
			ThinPoolName: rspLvg.ThinPoolName,
		}
	}

	return result, nil
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
