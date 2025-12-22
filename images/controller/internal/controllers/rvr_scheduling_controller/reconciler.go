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
	"github.com/google/uuid"
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
	// Generate unique trace ID for this reconciliation cycle
	traceID := uuid.New().String()[:8] // Use first 8 chars for brevity

	log := r.log.WithName("RVRScheduler").WithValues(
		"traceID", traceID,
		"rv", req.Name,
	)
	log.V(1).Info("starting reconciliation cycle")

	// Load ReplicatedVolume, its ReplicatedStorageClass and all relevant replicas.
	// The helper may also return an early reconcile.Result (e.g. when RV is not ready yet).
	sctx, failReason := r.prepareSchedulingContext(ctx, req, log)
	if failReason != nil {
		log.V(1).Info("RV not ready for scheduling", "reason", failReason.reason, "message", failReason.message)
		if err := r.setFailedScheduledConditionOnNonScheduledRVRs(ctx, sctx.Rv, failReason, log); err != nil {
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, nil
	}
	log.V(1).Info("scheduling context prepared", "rsc", sctx.Rsc.Name, "topology", sctx.Rsc.Spec.Topology, "volumeAccess", sctx.Rsc.Spec.VolumeAccess)

	// Phase 1: place Diskful replicas.
	log.V(1).Info("starting Diskful phase", "unscheduledCount", len(sctx.UnscheduledDiskfulReplicas))
	if err := r.scheduleDiskfulPhase(ctx, sctx); err != nil {
		return reconcile.Result{}, r.handlePhaseError(ctx, sctx, "Diskful", err, log)
	}
	log.V(1).Info("Diskful phase completed", "scheduledCountTotal", len(sctx.RVRsToSchedule))

	// Phase 2: place Access replicas.
	log.V(1).Info("starting Access phase", "unscheduledCount", len(sctx.UnscheduledAccessReplicas))
	if err := r.scheduleAccessPhase(sctx); err != nil {
		return reconcile.Result{}, r.handlePhaseError(ctx, sctx, "Access", err, log)
	}
	log.V(1).Info("Access phase completed", "scheduledCountTotal", len(sctx.RVRsToSchedule))

	// Phase 3: place TieBreaker replicas.
	log.V(1).Info("starting TieBreaker phase", "unscheduledCount", len(sctx.UnscheduledTieBreakerReplicas))
	if err := r.scheduleTieBreakerPhase(sctx); err != nil {
		return reconcile.Result{}, r.handlePhaseError(ctx, sctx, "TieBreaker", err, log)
	}
	log.V(1).Info("TieBreaker phase completed", "scheduledCountTotal", len(sctx.RVRsToSchedule))

	log.V(1).Info("patching scheduled replicas", "countTotal", len(sctx.RVRsToSchedule))
	if err := r.patchScheduledReplicas(ctx, sctx, log); err != nil {
		return reconcile.Result{}, err
	}

	// Ensure all previously scheduled replicas have correct Scheduled condition
	if err := r.ensureScheduledConditionOnExistingReplicas(ctx, sctx, log); err != nil {
		return reconcile.Result{}, err
	}

	log.V(1).Info("reconciliation completed successfully", "totalScheduled", len(sctx.RVRsToSchedule))
	return reconcile.Result{}, nil
}

// rvNotReadyReason describes why an RV is not ready for scheduling.
type rvNotReadyReason struct {
	reason  string
	message string
}

// handlePhaseError handles errors that occur during scheduling phases.
// It logs the error, sets failed condition on RVRs, and returns the error.
func (r *Reconciler) handlePhaseError(
	ctx context.Context,
	sctx *SchedulingContext,
	phaseName string,
	err error,
	log logr.Logger,
) error {
	log.Error(err, phaseName+" phase failed")
	reason := schedulingErrorToReason(err)
	if setErr := r.setFailedScheduledConditionOnNonScheduledRVRs(ctx, sctx.Rv, reason, log); setErr != nil {
		log.Error(setErr, "failed to set Scheduled condition on RVRs after scheduling error")
	}
	return err
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
		// Create original state for patch (without NodeName)
		original := rvr.DeepCopy()
		original.Spec.NodeName = ""

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
			v1alpha3.ReasonSchedulingReplicaScheduled,
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
	alreadyScheduledReplicas := make([]*v1alpha3.ReplicatedVolumeReplica, 0)
	alreadyScheduledReplicas = append(alreadyScheduledReplicas, sctx.ScheduledDiskfulReplicas...)

	// Also check for scheduled Access and TieBreaker replicas from RvrList
	for _, rvr := range sctx.RvrList {
		if rvr.Spec.NodeName == "" {
			continue // Skip unscheduled
		}
		// Skip if it was scheduled in this cycle
		alreadyScheduled := true
		for _, newlyScheduled := range sctx.RVRsToSchedule {
			if rvr.Name == newlyScheduled.Name {
				alreadyScheduled = false
				break
			}
		}
		if !alreadyScheduled {
			continue
		}
		// Skip Diskful as they are already in ScheduledDiskfulReplicas
		if rvr.Spec.Type == v1alpha3.ReplicaTypeDiskful {
			continue
		}
		alreadyScheduledReplicas = append(alreadyScheduledReplicas, rvr)
	}

	for _, rvr := range alreadyScheduledReplicas {
		// Check if condition is already correct
		var cond *metav1.Condition
		if rvr.Status != nil {
			cond = meta.FindStatusCondition(rvr.Status.Conditions, v1alpha3.ConditionTypeScheduled)
		}
		if cond != nil && cond.Status == metav1.ConditionTrue && cond.Reason == v1alpha3.ReasonSchedulingReplicaScheduled {
			continue // Already correct
		}

		log.V(2).Info("fixing Scheduled condition on existing replica", "rvr", rvr.Name)
		if err := r.setScheduledConditionOnRVR(
			ctx,
			rvr,
			metav1.ConditionTrue,
			v1alpha3.ReasonSchedulingReplicaScheduled,
			"",
		); err != nil {
			return fmt.Errorf("failed to set Scheduled condition on existing RVR %s: %w", rvr.Name, err)
		}
	}

	return nil
}

// isRVReadyToSchedule checks if the ReplicatedVolume is ready for scheduling.
// Returns nil if ready, or a reason struct if not ready.
func isRVReadyToSchedule(rv *v1alpha3.ReplicatedVolume) *rvNotReadyReason {
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
			message: "ReplicatedStorageClassName is not specified in ReplicatedVolume spec",
		}
	}

	if rv.Spec.Size.IsZero() {
		return &rvNotReadyReason{
			reason:  v1alpha3.ReasonSchedulingPending,
			message: "ReplicatedVolume size is zero in ReplicatedVolume spec",
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
			log.V(1).Info("ReplicatedVolume not found, skipping reconciliation")
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

	rspLvgToNodeInfoMap, err := r.getLVGToNodesByStoragePool(ctx, rsp, log)
	if err != nil {
		return nil, &rvNotReadyReason{
			reason:  v1alpha3.ReasonSchedulingFailed,
			message: fmt.Sprintf("unable to get LVG to nodes mapping: %v", err),
		}
	}

	// Get nodes that already have replicas of this RV.
	nodesWithRVReplica := getNodesWithRVReplicaSet(replicasForRV)

	// Build list of RSP nodes WITHOUT replicas - exclude nodes that already have replicas.
	rspNodesWithoutReplica := []string{}
	for _, info := range rspLvgToNodeInfoMap {
		if _, hasReplica := nodesWithRVReplica[info.NodeName]; !hasReplica {
			rspNodesWithoutReplica = append(rspNodesWithoutReplica, info.NodeName)
		}
	}

	nodeNameToZone, err := r.getNodeNameToZoneMap(ctx, log)
	if err != nil {
		return nil, &rvNotReadyReason{
			reason:  v1alpha3.ReasonSchedulingFailed,
			message: fmt.Sprintf("unable to get node to zone mapping: %v", err),
		}
	}

	publishOnList := getPublishOnNodeList(rv)
	scheduledDiskfulReplicas, unscheduledDiskfulReplicas := getTypedReplicasLists(replicasForRV, v1alpha3.ReplicaTypeDiskful)
	_, unscheduledAccessReplicas := getTypedReplicasLists(replicasForRV, v1alpha3.ReplicaTypeAccess)
	_, unscheduledTieBreakerReplicas := getTypedReplicasLists(replicasForRV, v1alpha3.ReplicaTypeTieBreaker)
	publishNodesWithoutAnyReplica := getPublishNodesWithoutAnyReplica(publishOnList, nodesWithRVReplica)

	schedulingCtx := &SchedulingContext{
		Log:                            log,
		Rv:                             rv,
		Rsc:                            rsc,
		Rsp:                            rsp,
		RvrList:                        replicasForRV,
		PublishOnNodes:                 publishOnList,
		PublishOnNodesWithoutRvReplica: publishNodesWithoutAnyReplica,
		RspLvgToNodeInfoMap:            rspLvgToNodeInfoMap,
		NodesWithAnyReplica:            nodesWithRVReplica,
		UnscheduledDiskfulReplicas:     unscheduledDiskfulReplicas,
		ScheduledDiskfulReplicas:       scheduledDiskfulReplicas,
		UnscheduledAccessReplicas:      unscheduledAccessReplicas,
		UnscheduledTieBreakerReplicas:  unscheduledTieBreakerReplicas,
		RspNodesWithoutReplica:         rspNodesWithoutReplica,
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
		sctx.Log.V(1).Info("no unscheduled Diskful replicas. Skipping Diskful phase.")
		return nil
	}

	candidateNodes := sctx.RspNodesWithoutReplica
	sctx.Log.V(1).Info("Diskful phase: initial candidate nodes", "count", len(candidateNodes), "nodes", candidateNodes)

	// Apply topology constraints (Ignored/Zonal/TransZonal) to the nodes without replicas.
	err := r.applyTopologyFilter(candidateNodes, true, sctx) // isDiskfulPhase=true
	if err != nil {
		// Topology constraints for Diskful & Local phase are violated.
		return fmt.Errorf("%w: %v", errSchedulingTopologyConflict, err)
	}

	if len(sctx.ZonesToNodeCandidatesMap) == 0 {
		return fmt.Errorf("%w: no candidate nodes found after topology filtering", errSchedulingNoCandidateNodes)
	}
	sctx.Log.V(1).Info("topology filter applied", "zonesCount", len(sctx.ZonesToNodeCandidatesMap))

	// Apply capacity filtering using scheduler extender
	err = r.applyCapacityFilterAndScoreCandidates(ctx, sctx)
	if err != nil {
		return err
	}
	sctx.Log.V(1).Info("capacity filter applied and candidates scored", "zonesCount", len(sctx.ZonesToNodeCandidatesMap))

	sctx.ApplyPublishOnBonus()
	sctx.Log.V(1).Info("publishOn bonus applied")

	// Assign replicas: for Diskful count only Diskful replicas for zone balancing, strict mode (must place all)
	assignedReplicas, err := r.assignReplicasToNodes(sctx, sctx.UnscheduledDiskfulReplicas, v1alpha3.ReplicaTypeDiskful, false)
	if err != nil {
		return err
	}
	sctx.Log.V(1).Info("Diskful replicas assigned", "count", len(assignedReplicas))

	sctx.UpdateAfterScheduling(assignedReplicas)

	return nil
}

// assignReplicasToNodes assigns nodes to unscheduled replicas based on topology and node scores.
// For Ignored topology: selects best nodes by score.
// For Zonal topology: selects the best zone first (by total score), then best nodes from that zone.
// For TransZonal topology: distributes replicas across zones, picking zones with fewer scheduled replicas first.
// replicaTypeFilter: for TransZonal, which replica types to count for zone balancing (empty = all types).
// bestEffort: if true, don't return error when not enough nodes (used for TieBreaker).
// Note: This function returns the list of replicas that were assigned nodes in this call.
func (r *Reconciler) assignReplicasToNodes(
	sctx *SchedulingContext,
	unscheduledReplicas []*v1alpha3.ReplicatedVolumeReplica,
	replicaTypeFilter string,
	bestEffort bool,
) ([]*v1alpha3.ReplicatedVolumeReplica, error) {
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
		return r.assignReplicasTransZonalTopology(sctx, unscheduledReplicas, replicaTypeFilter)
	default:
		return nil, fmt.Errorf("unknown topology: %s", sctx.Rsc.Spec.Topology)
	}
}

// assignReplicasIgnoredTopology assigns replicas to best nodes by score (ignoring zones).
// If bestEffort=true, assigns as many as possible without error.
// Returns the list of replicas that were assigned nodes.
func (r *Reconciler) assignReplicasIgnoredTopology(
	sctx *SchedulingContext,
	unscheduledReplicas []*v1alpha3.ReplicatedVolumeReplica,
	bestEffort bool,
) ([]*v1alpha3.ReplicatedVolumeReplica, error) {
	sctx.Log.V(1).Info("assigning replicas with Ignored topology", "replicasCount", len(unscheduledReplicas), "bestEffort", bestEffort)
	// Collect all candidates from all zones
	var allCandidates []NodeCandidate
	for _, candidates := range sctx.ZonesToNodeCandidatesMap {
		allCandidates = append(allCandidates, candidates...)
	}
	sctx.Log.V(2).Info("collected candidates", "count", len(allCandidates))

	// Assign nodes to replicas
	var assignedReplicas []*v1alpha3.ReplicatedVolumeReplica
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
	unscheduledReplicas []*v1alpha3.ReplicatedVolumeReplica,
	bestEffort bool,
) ([]*v1alpha3.ReplicatedVolumeReplica, error) {
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
	var assignedReplicas []*v1alpha3.ReplicatedVolumeReplica
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
// Returns the list of replicas that were assigned nodes.
func (r *Reconciler) assignReplicasTransZonalTopology(
	sctx *SchedulingContext,
	unscheduledReplicas []*v1alpha3.ReplicatedVolumeReplica,
	replicaTypeFilter string,
) ([]*v1alpha3.ReplicatedVolumeReplica, error) {
	if len(unscheduledReplicas) == 0 {
		return nil, nil
	}

	sctx.Log.V(1).Info("assigning replicas with TransZonal topology", "replicasCount", len(unscheduledReplicas), "replicaTypeFilter", replicaTypeFilter)

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
	var assignedReplicas []*v1alpha3.ReplicatedVolumeReplica
	for i, rvr := range unscheduledReplicas {
		sctx.Log.V(2).Info("scheduling replica", "index", i, "rvr", rvr.Name)

		// Find zone with minimum replica count among ALL allowed zones
		globalMinZone, globalMinCount := findZoneWithMinReplicaCount(allowedZones, zoneReplicaCount)

		// Find zone with minimum replica count that has available candidates
		selectedZone, availableMinCount := findZoneWithMinReplicaCount(availableZones, zoneReplicaCount)

		if selectedZone == "" {
			// No more zones with available candidates
			sctx.Log.V(1).Info("no more zones with available candidates", "assigned", len(assignedReplicas), "total", len(unscheduledReplicas))
			return nil, fmt.Errorf(
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
			return nil, fmt.Errorf(
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
	// - rv.spec.publishOn is set AND not all publishOn nodes have replicas
	// - rsc.spec.volumeAccess != Local
	if len(sctx.PublishOnNodes) == 0 {
		sctx.Log.V(1).Info("skipping Access phase: no publishOn nodes")
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
	// Use PublishOnNodesWithoutRvReplica which already contains publishOn nodes without any replica
	candidateNodes := sctx.PublishOnNodesWithoutRvReplica
	if len(candidateNodes) == 0 {
		// All publishOn nodes already have replicas; nothing to do.
		// Spec «Access»: it is allowed to have replicas that could not be scheduled
		sctx.Log.V(1).Info("Access phase: all publishOn nodes already have replicas")
		return nil
	}
	sctx.Log.V(1).Info("Access phase: candidate nodes", "count", len(candidateNodes), "nodes", candidateNodes)

	// We are not required to place all Access replicas or to cover all publishOn nodes.
	// Spec «Access»: it is allowed to have nodes in rv.spec.publishOn without enough replicas
	// Spec «Access»: it is allowed to have replicas that could not be scheduled
	nodesToFill := min(len(candidateNodes), len(sctx.UnscheduledAccessReplicas))
	sctx.Log.V(1).Info("Access phase: scheduling replicas", "nodesToFill", nodesToFill)

	var assignedReplicas []*v1alpha3.ReplicatedVolumeReplica
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

	// Apply topology filter (isDiskfulPhase=false)
	if err := r.applyTopologyFilter(candidateNodes, false, sctx); err != nil {
		return err
	}

	// Assign replicas: count ALL replica types for zone balancing, strict mode (must place all)
	assignedReplicas, err := r.assignReplicasToNodes(sctx, sctx.UnscheduledTieBreakerReplicas, "", false)
	if err != nil {
		return err
	}

	// Update context after scheduling
	sctx.UpdateAfterScheduling(assignedReplicas)
	sctx.Log.V(1).Info("TieBreaker phase: completed", "assigned", len(assignedReplicas))

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

func getPublishOnNodeList(rv *v1alpha3.ReplicatedVolume) []string {
	return slices.Clone(rv.Spec.PublishOn)
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

// setFailedScheduledConditionOnNonScheduledRVRs sets the Scheduled condition to False on all RVRs
// belonging to the given RV when the RV is not ready for scheduling.
func (r *Reconciler) setFailedScheduledConditionOnNonScheduledRVRs(
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
			log.Error(err, "failed to set Scheduled condition", "rvr", rvr.Name, "reason", notReadyReason.reason, "message", notReadyReason.message)
			return err
		}
	}

	return nil
}

func getPublishNodesWithoutAnyReplica(
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

// applyTopologyFilter groups candidate nodes by zones based on RSC topology.
// isDiskfulPhase affects only Zonal topology:
//   - true: falls back to publishOn or any allowed zone if no ScheduledDiskfulReplicas
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
		return nil

	case topologyZonal:
		sctx.Log.V(1).Info("topology filter: Zonal - grouping candidates by zone")
		return r.applyZonalTopologyFilter(candidateNodes, isDiskfulPhase, sctx)

	case topologyTransZonal:
		// Same for both phases: group by allowed zones
		sctx.Log.V(1).Info("topology filter: TransZonal - distributing across zones")
		allowedZones := getAllowedZones(nil, sctx.Rsc.Spec.Zones, sctx.NodeNameToZone)
		sctx.ZonesToNodeCandidatesMap = r.groupCandidateNodesByZone(candidateNodes, allowedZones, sctx)
		sctx.Log.V(1).Info("topology filter applied", "zonesCount", len(sctx.ZonesToNodeCandidatesMap))
		return nil

	default:
		return fmt.Errorf("unknown RSC topology: %s", sctx.Rsc.Spec.Topology)
	}
}

// applyZonalTopologyFilter handles Zonal topology logic.
// For isDiskfulPhase=true: ScheduledDiskfulReplicas -> publishOn -> any allowed zone
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
			return fmt.Errorf("scheduled diskful replica %s is on node %s without zone label for Zonal topology", rvr.Name, rvr.Spec.NodeName)
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
		// Diskful phase: fallback to publishOn zones
		for _, nodeName := range sctx.PublishOnNodes {
			zone, ok := sctx.NodeNameToZone[nodeName]
			if !ok || zone == "" {
				return fmt.Errorf("publishOn node %s has no zone label", nodeName)
			}
			if !slices.Contains(targetZones, zone) {
				targetZones = append(targetZones, zone)
			}
		}
		sctx.Log.V(2).Info("applyZonalTopologyFilter: publishOn zones", "zones", targetZones)
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
	replicas []*v1alpha3.ReplicatedVolumeReplica,
	replicaType string,
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
