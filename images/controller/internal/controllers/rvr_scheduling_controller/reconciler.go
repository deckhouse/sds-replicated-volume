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
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

const (
	topologyIgnored    = "Ignored"
	topologyZonal      = "Zonal"
	topologyTransZonal = "TransZonal"

	attachToScoreBonus = 1000
)

var (
	errSchedulingTopologyConflict = errors.New("scheduling topology conflict")
	errSchedulingNoCandidateNodes = errors.New("scheduling no candidate nodes")
	errSchedulingPending          = errors.New("scheduling pending")
)

// --- Wiring / construction

type Reconciler struct {
	cl             client.Client
	extenderClient SchedulerExtenderClient
}

func NewReconciler(cl client.Client) (*Reconciler, error) {
	extenderClient, err := NewSchedulerExtenderClient()
	if err != nil {
		return nil, err
	}

	return &Reconciler{
		cl:             cl,
		extenderClient: extenderClient,
	}, nil
}

func NewReconcilerWithExtender(cl client.Client, extenderClient SchedulerExtenderClient) *Reconciler {
	return &Reconciler{
		cl:             cl,
		extenderClient: extenderClient,
	}
}

// --- Root Reconcile

// Reconcile pattern: Per-RVR orchestration with error resilience.
// Each RVR is reconciled independently; errors on one RVR don't block others.
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	rf := flow.BeginRootReconcile(ctx)

	// Prepare scheduling context.
	sctx, err := r.prepareSchedulingContext(rf.Ctx(), req.Name)
	if err != nil {
		setErr := r.setFailedScheduledConditionOnUnscheduledRVRs(rf.Ctx(), req.Name, computeSchedulingFailureReason(err))
		if setErr != nil {
			return rf.Fail(setErr).ToCtrl()
		}
		return rf.Fail(err).ToCtrl()
	}
	if sctx == nil {
		return rf.Done().ToCtrl()
	}

	var errs []error
	var hasSchedulingFailure bool

	// Phase 1: Reconcile already scheduled RVRs.
	for _, rvr := range sctx.ScheduledRVRs() {
		if err := r.reconcileScheduledRVR(rf.Ctx(), sctx, rvr); err != nil {
			errs = append(errs, fmt.Errorf("RVR %s: %w", rvr.Name, err))
		}
	}

	// Phase 2: Prepare zone candidates for Diskful (compute capacity scores once).
	var diskfulPrepareErr error
	if len(sctx.UnscheduledDiskful) > 0 {
		diskfulPrepareErr = r.prepareScoredCandidatesForDiskful(rf.Ctx(), sctx)
		if diskfulPrepareErr != nil {
			// Mark all unscheduled Diskful as failed
			failureReason := computeSchedulingFailureReason(diskfulPrepareErr)
			for _, rvr := range sctx.UnscheduledDiskful {
				if setErr := r.setScheduledConditionFalseOnRVR(rf.Ctx(), rvr, failureReason); setErr != nil {
					errs = append(errs, fmt.Errorf("RVR %s: %w", rvr.Name, setErr))
				}
			}
			// Preparation failure is a scheduling failure
			hasSchedulingFailure = true
		}
	}

	// Phase 3: Reconcile unscheduled Diskful RVRs (only if preparation succeeded).
	if diskfulPrepareErr == nil {
		for _, rvr := range sctx.UnscheduledDiskful {
			schedulingFailed, err := r.reconcileUnscheduledDiskfulRVR(rf.Ctx(), sctx, rvr)
			if err != nil {
				errs = append(errs, fmt.Errorf("RVR %s: %w", rvr.Name, err))
			}
			if schedulingFailed {
				hasSchedulingFailure = true
			}
		}
	}

	// Phase 4: Reconcile unscheduled TieBreaker RVRs.
	for _, rvr := range sctx.UnscheduledTieBreaker {
		schedulingFailed, err := r.reconcileUnscheduledTieBreakerRVR(rf.Ctx(), sctx, rvr)
		if err != nil {
			errs = append(errs, fmt.Errorf("RVR %s: %w", rvr.Name, err))
		}
		if schedulingFailed {
			hasSchedulingFailure = true
		}
	}

	// Aggregate errors and determine result.
	// Patch errors (API failures) → exponential backoff.
	if len(errs) > 0 {
		return rf.Fail(errors.Join(errs...)).ToCtrl()
	}
	// Scheduling failures (no suitable nodes/capacity) → fixed 30s requeue.
	// This handles capacity changes that don't trigger RSP watch events.
	if hasSchedulingFailure {
		return rf.DoneAndRequeueAfter(30 * time.Second).ToCtrl()
	}

	return rf.Done().ToCtrl()
}

// --- Reconcile: already-scheduled

// reconcileAlreadyScheduled updates conditions on already scheduled RVRs.
//
// Reconcile pattern: In-place reconciliation
func (r *Reconciler) reconcileAlreadyScheduled(ctx context.Context, sctx *SchedulingContext) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "already-scheduled")
	defer rf.OnEnd(&outcome)

	if err := r.updateScheduledConditionOnScheduledRVRs(rf.Ctx(), sctx); err != nil {
		return rf.Fail(err)
	}

	return rf.Continue()
}

// --- Reconcile: diskful

// reconcileDiskful schedules diskful replicas.
//
// Reconcile pattern: In-place reconciliation
func (r *Reconciler) reconcileDiskful(ctx context.Context, sctx *SchedulingContext) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "diskful")
	defer rf.OnEnd(&outcome)

	if len(sctx.UnscheduledDiskful) == 0 {
		return rf.Done()
	}

	candidateNodes := computeEligibleNodeNames(sctx.EligibleNodes, sctx.OccupiedNodes)
	if len(candidateNodes) == 0 {
		return r.failDiskfulScheduling(rf, sctx, fmt.Errorf("%w: no candidate nodes from storage pool", errSchedulingNoCandidateNodes))
	}

	zoneCandidates, err := r.applyTopologyFilter(candidateNodes, true, sctx)
	if err != nil {
		return r.failDiskfulScheduling(rf, sctx, err)
	}

	zoneCandidates, err = r.applyCapacityFilterAndScore(rf.Ctx(), zoneCandidates, sctx)
	if err != nil {
		return r.failDiskfulScheduling(rf, sctx, err)
	}

	applyAttachToBonus(zoneCandidates, sctx.AttachToNodes)

	assignedRVRs, err := r.assignReplicasToNodes(
		zoneCandidates,
		sctx.UnscheduledDiskful,
		sctx,
		v1alpha1.ReplicaTypeDiskful,
		true,
	)
	if err != nil {
		return r.failDiskfulScheduling(rf, sctx, err)
	}

	if err := r.updateScheduledRVRs(rf.Ctx(), assignedRVRs); err != nil {
		return rf.Fail(err)
	}

	updateStateAfterScheduling(sctx, assignedRVRs)

	if len(sctx.UnscheduledDiskful) > 0 {
		return r.failDiskfulScheduling(rf, sctx, fmt.Errorf("%w: not enough candidate nodes to schedule all Diskful replicas", errSchedulingNoCandidateNodes))
	}

	return rf.Done()
}

func (r *Reconciler) failDiskfulScheduling(rf flow.ReconcileFlow, sctx *SchedulingContext, err error) flow.ReconcileOutcome {
	failureReason := computeSchedulingFailureReason(err)
	if setErr := r.setScheduledConditionFalseOnRVRs(rf.Ctx(), sctx.UnscheduledDiskful, failureReason); setErr != nil {
		return rf.Fail(setErr)
	}
	return rf.DoneAndRequeueAfter(30 * time.Second)
}

// --- Reconcile: tiebreaker

// reconcileTieBreaker schedules tiebreaker replicas.
//
// Reconcile pattern: In-place reconciliation
func (r *Reconciler) reconcileTieBreaker(ctx context.Context, sctx *SchedulingContext) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "tiebreaker")
	defer rf.OnEnd(&outcome)

	if len(sctx.UnscheduledTieBreaker) == 0 {
		return rf.Done()
	}

	candidateNodes := computeEligibleNodeNames(sctx.EligibleNodes, sctx.OccupiedNodes)
	if len(candidateNodes) == 0 {
		return r.failTieBreakerScheduling(rf, sctx, fmt.Errorf("%w: no candidate nodes for TieBreaker", errSchedulingNoCandidateNodes))
	}

	zoneCandidates, err := r.applyTopologyFilter(candidateNodes, false, sctx)
	if err != nil {
		return r.failTieBreakerScheduling(rf, sctx, err)
	}

	assignedRVRs, err := r.assignReplicasToNodes(
		zoneCandidates,
		sctx.UnscheduledTieBreaker,
		sctx,
		v1alpha1.ReplicaType(""),
		true,
	)
	if err != nil {
		return r.failTieBreakerScheduling(rf, sctx, err)
	}

	if err := r.updateScheduledRVRs(rf.Ctx(), assignedRVRs); err != nil {
		return rf.Fail(err)
	}

	updateStateAfterScheduling(sctx, assignedRVRs)

	if len(sctx.UnscheduledTieBreaker) > 0 {
		return r.failTieBreakerScheduling(rf, sctx, fmt.Errorf("%w: not enough candidate nodes to schedule all TieBreaker replicas", errSchedulingNoCandidateNodes))
	}

	return rf.Done()
}

func (r *Reconciler) failTieBreakerScheduling(rf flow.ReconcileFlow, sctx *SchedulingContext, err error) flow.ReconcileOutcome {
	failureReason := computeSchedulingFailureReason(err)
	if setErr := r.setScheduledConditionFalseOnRVRs(rf.Ctx(), sctx.UnscheduledTieBreaker, failureReason); setErr != nil {
		return rf.Fail(setErr)
	}
	return rf.DoneAndRequeueAfter(30 * time.Second)
}

// --- Per-RVR Reconcile functions

// reconcileScheduledRVR updates the Scheduled condition on an already scheduled RVR.
// Uses canonical apply pattern: base -> apply -> patch (only if changed).
func (r *Reconciler) reconcileScheduledRVR(ctx context.Context, _ *SchedulingContext, rvr *v1alpha1.ReplicatedVolumeReplica) error {
	// Main domain: ensure node name label exists
	base := rvr.DeepCopy()
	mainChanged := applyNodeNameLabelIfMissing(rvr)
	if mainChanged {
		if err := r.patchRVR(ctx, rvr, base, true); err != nil {
			return err
		}
	}

	// Status domain: ensure Scheduled=True condition
	statusBase := rvr.DeepCopy()
	statusChanged := applyScheduledConditionTrue(rvr)
	if statusChanged {
		if err := r.patchRVRStatus(ctx, rvr, statusBase); err != nil {
			return err
		}
	}

	return nil
}

// reconcileUnscheduledDiskfulRVR schedules a single Diskful RVR.
// Uses canonical apply pattern: base -> apply -> patch (only if changed).
// On successful patch, updates sctx to reflect the scheduling.
// On failed patch, sctx remains unchanged (node stays available for next RVR).
// Returns (schedulingFailed, err): schedulingFailed=true means no suitable node found but status was updated.
func (r *Reconciler) reconcileUnscheduledDiskfulRVR(ctx context.Context, sctx *SchedulingContext, rvr *v1alpha1.ReplicatedVolumeReplica) (schedulingFailed bool, err error) {
	// Select best candidate based on topology
	candidate, err := r.selectBestCandidate(sctx, true)
	if err != nil {
		// Set Scheduled=False condition and signal scheduling failure
		if setErr := r.setScheduledConditionFalseOnRVR(ctx, rvr, computeSchedulingFailureReason(err)); setErr != nil {
			return false, setErr
		}
		return true, nil
	}

	// Main domain: base -> apply -> patch
	base := rvr.DeepCopy()
	mainChanged := applyPlacement(rvr, candidate)
	if mainChanged {
		if err := r.patchRVR(ctx, rvr, base, true); err != nil {
			// Patch failed - don't update sctx, node remains available
			return false, err
		}
	}

	// Patch succeeded - update scheduling context
	sctx.MarkNodeOccupied(candidate.Name)
	sctx.RemoveCandidate(candidate.Name)
	sctx.ScheduledDiskful = append(sctx.ScheduledDiskful, rvr)
	if sctx.RSC.Spec.Topology == topologyTransZonal && candidate.Zone != "" {
		sctx.IncrementZoneReplicaCount(candidate.Zone)
	}

	// Status domain: set Scheduled=True
	statusBase := rvr.DeepCopy()
	statusChanged := applyScheduledConditionTrue(rvr)
	if statusChanged {
		if err := r.patchRVRStatus(ctx, rvr, statusBase); err != nil {
			return false, err
		}
	}

	return false, nil
}

// reconcileUnscheduledTieBreakerRVR schedules a single TieBreaker RVR.
// Uses canonical apply pattern: base -> apply -> patch (only if changed).
// On successful patch, updates sctx to reflect the scheduling.
// On failed patch, sctx remains unchanged (node stays available for next RVR).
// Returns (schedulingFailed, err): schedulingFailed=true means no suitable node found but status was updated.
func (r *Reconciler) reconcileUnscheduledTieBreakerRVR(ctx context.Context, sctx *SchedulingContext, rvr *v1alpha1.ReplicatedVolumeReplica) (schedulingFailed bool, err error) {
	// Prepare candidates for TieBreaker (no capacity scoring needed)
	candidateNodes := computeEligibleNodeNames(sctx.EligibleNodes, sctx.OccupiedNodes)
	if len(candidateNodes) == 0 {
		if setErr := r.setScheduledConditionFalseOnRVR(ctx, rvr, computeSchedulingFailureReason(
			fmt.Errorf("%w: no candidate nodes for TieBreaker", errSchedulingNoCandidateNodes))); setErr != nil {
			return false, setErr
		}
		return true, nil
	}

	// Apply topology filter for TieBreaker
	zoneCandidates, err := r.applyTopologyFilter(candidateNodes, false, sctx)
	if err != nil {
		if setErr := r.setScheduledConditionFalseOnRVR(ctx, rvr, computeSchedulingFailureReason(err)); setErr != nil {
			return false, setErr
		}
		return true, nil
	}

	// For TransZonal TieBreaker, we need to count ALL replicas (not just Diskful).
	// Recompute if this is the first TieBreaker (ZoneReplicaCounts may have been set for Diskful phase).
	if sctx.RSC.Spec.Topology == topologyTransZonal {
		// Recompute counts including newly scheduled replicas
		sctx.ZoneReplicaCounts = computeReplicasByZone(sctx.AllRVRs, "", sctx.NodeToZone)
	}

	// Select best candidate
	candidate, err := selectBestCandidateFromZones(zoneCandidates, sctx)
	if err != nil {
		if setErr := r.setScheduledConditionFalseOnRVR(ctx, rvr, computeSchedulingFailureReason(err)); setErr != nil {
			return false, setErr
		}
		return true, nil
	}

	// Main domain: base -> apply -> patch
	// TieBreaker doesn't have LVG info, just node
	base := rvr.DeepCopy()
	tbCandidate := NodeCandidate{Name: candidate.Name, Zone: candidate.Zone}
	mainChanged := applyPlacement(rvr, tbCandidate)
	if mainChanged {
		if err := r.patchRVR(ctx, rvr, base, true); err != nil {
			// Patch failed - don't update sctx, node remains available
			return false, err
		}
	}

	// Patch succeeded - update scheduling context
	sctx.MarkNodeOccupied(candidate.Name)
	if sctx.RSC.Spec.Topology == topologyTransZonal && candidate.Zone != "" {
		sctx.IncrementZoneReplicaCount(candidate.Zone)
	}

	// Status domain: set Scheduled=True
	statusBase := rvr.DeepCopy()
	statusChanged := applyScheduledConditionTrue(rvr)
	if statusChanged {
		if err := r.patchRVRStatus(ctx, rvr, statusBase); err != nil {
			return false, err
		}
	}

	return false, nil
}

// setScheduledConditionFalseOnRVR sets Scheduled=False on a single RVR.
func (r *Reconciler) setScheduledConditionFalseOnRVR(ctx context.Context, rvr *v1alpha1.ReplicatedVolumeReplica, reason *schedulingFailureReason) error {
	base := rvr.DeepCopy()
	changed := applyScheduledConditionFalse(rvr, reason.reason, reason.message)
	if !changed {
		return nil
	}
	return r.patchRVRStatus(ctx, rvr, base)
}

// selectBestCandidate selects the best candidate from sctx.ScoredCandidates based on topology.
// For Zonal topology, it also sets sctx.SelectedZone on first call.
func (r *Reconciler) selectBestCandidate(sctx *SchedulingContext, isDiskful bool) (NodeCandidate, error) {
	if len(sctx.ScoredCandidates) == 0 {
		return NodeCandidate{}, fmt.Errorf("%w: no zone candidates available", errSchedulingNoCandidateNodes)
	}

	switch sctx.RSC.Spec.Topology {
	case topologyIgnored:
		return selectBestCandidateIgnored(sctx)
	case topologyZonal:
		return selectBestCandidateZonal(sctx, isDiskful)
	case topologyTransZonal:
		return selectBestCandidateTransZonal(sctx)
	default:
		return NodeCandidate{}, fmt.Errorf("unknown topology: %s", sctx.RSC.Spec.Topology)
	}
}

// selectBestCandidateIgnored selects the best candidate across all zones (no zone constraints).
func selectBestCandidateIgnored(sctx *SchedulingContext) (NodeCandidate, error) {
	var allCandidates []NodeCandidate
	for _, candidates := range sctx.ScoredCandidates {
		allCandidates = append(allCandidates, candidates...)
	}

	if len(allCandidates) == 0 {
		return NodeCandidate{}, fmt.Errorf("%w: no candidates available", errSchedulingNoCandidateNodes)
	}

	best, _ := computeBestNode(allCandidates)
	if best.Name == "" {
		return NodeCandidate{}, fmt.Errorf("%w: no candidates available", errSchedulingNoCandidateNodes)
	}

	return best, nil
}

// selectBestCandidateZonal selects the best candidate for Zonal topology.
// On first call, determines and stores the best zone in sctx.SelectedZone.
func selectBestCandidateZonal(sctx *SchedulingContext, isDiskful bool) (NodeCandidate, error) {
	// Determine zone if not yet selected
	if sctx.SelectedZone == "" {
		if !isDiskful {
			return NodeCandidate{}, fmt.Errorf("%w: cannot schedule TieBreaker before Diskful in Zonal topology", errSchedulingNoCandidateNodes)
		}

		// Select best zone based on capacity scores
		var bestZone string
		bestZoneScore := -1
		for zone, candidates := range sctx.ScoredCandidates {
			if len(candidates) == 0 {
				continue
			}
			totalScore := 0
			for _, c := range candidates {
				totalScore += c.BestScore
			}
			zoneScore := totalScore * len(candidates)
			if zoneScore > bestZoneScore {
				bestZoneScore = zoneScore
				bestZone = zone
			}
		}

		if bestZone == "" {
			return NodeCandidate{}, fmt.Errorf("%w: no zones with candidates for Zonal topology", errSchedulingNoCandidateNodes)
		}

		sctx.SelectedZone = bestZone
	}

	// Select best candidate from the selected zone
	candidates := sctx.ScoredCandidates[sctx.SelectedZone]
	if len(candidates) == 0 {
		return NodeCandidate{}, fmt.Errorf("%w: no candidates left in zone %s", errSchedulingNoCandidateNodes, sctx.SelectedZone)
	}

	best, _ := computeBestNode(candidates)
	if best.Name == "" {
		return NodeCandidate{}, fmt.Errorf("%w: no candidates left in zone %s", errSchedulingNoCandidateNodes, sctx.SelectedZone)
	}

	return best, nil
}

// selectBestCandidateTransZonal selects the best candidate for TransZonal topology.
// Places replica in the zone with minimum replica count.
func selectBestCandidateTransZonal(sctx *SchedulingContext) (NodeCandidate, error) {
	// Find zone with minimum replica count that has available candidates
	var selectedZone string
	minCount := -1

	for zone, candidates := range sctx.ScoredCandidates {
		if len(candidates) == 0 {
			continue
		}
		count := sctx.ZoneReplicaCounts[zone]
		if minCount < 0 || count < minCount {
			minCount = count
			selectedZone = zone
		}
	}

	if selectedZone == "" {
		return NodeCandidate{}, fmt.Errorf("%w: no zones with candidates for TransZonal topology", errSchedulingNoCandidateNodes)
	}

	// Select best candidate from the selected zone
	candidates := sctx.ScoredCandidates[selectedZone]
	best, _ := computeBestNode(candidates)
	if best.Name == "" {
		return NodeCandidate{}, fmt.Errorf("%w: no candidates left in zone %s", errSchedulingNoCandidateNodes, selectedZone)
	}

	best.Zone = selectedZone
	return best, nil
}

// selectBestCandidateFromZones selects the best candidate from zone candidates (for TieBreaker).
func selectBestCandidateFromZones(zoneCandidates map[string][]NodeCandidate, sctx *SchedulingContext) (NodeCandidate, error) {
	switch sctx.RSC.Spec.Topology {
	case topologyIgnored:
		var allCandidates []NodeCandidate
		for _, candidates := range zoneCandidates {
			allCandidates = append(allCandidates, candidates...)
		}
		if len(allCandidates) == 0 {
			return NodeCandidate{}, fmt.Errorf("%w: no candidates for TieBreaker", errSchedulingNoCandidateNodes)
		}
		// TieBreaker doesn't use capacity scores, just pick first available
		return allCandidates[0], nil

	case topologyZonal:
		if sctx.SelectedZone == "" {
			return NodeCandidate{}, fmt.Errorf("%w: no zone selected for TieBreaker in Zonal topology", errSchedulingNoCandidateNodes)
		}
		candidates := zoneCandidates[sctx.SelectedZone]
		if len(candidates) == 0 {
			return NodeCandidate{}, fmt.Errorf("%w: no candidates in zone %s for TieBreaker", errSchedulingNoCandidateNodes, sctx.SelectedZone)
		}
		return candidates[0], nil

	case topologyTransZonal:
		// Find zone with minimum replica count
		var selectedZone string
		minCount := -1
		for zone, candidates := range zoneCandidates {
			if len(candidates) == 0 {
				continue
			}
			count := sctx.ZoneReplicaCounts[zone]
			if minCount < 0 || count < minCount {
				minCount = count
				selectedZone = zone
			}
		}
		if selectedZone == "" {
			return NodeCandidate{}, fmt.Errorf("%w: no zones with candidates for TieBreaker", errSchedulingNoCandidateNodes)
		}
		candidate := zoneCandidates[selectedZone][0]
		candidate.Zone = selectedZone
		return candidate, nil

	default:
		return NodeCandidate{}, fmt.Errorf("unknown topology: %s", sctx.RSC.Spec.Topology)
	}
}

// prepareScoredCandidatesForDiskful computes candidates with capacity scores.
// Called once before processing unscheduled Diskful RVRs.
// Candidates are grouped by zone (or by "Ignored" key for Ignored topology).
func (r *Reconciler) prepareScoredCandidatesForDiskful(ctx context.Context, sctx *SchedulingContext) error {
	candidateNodes := computeEligibleNodeNames(sctx.EligibleNodes, sctx.OccupiedNodes)
	if len(candidateNodes) == 0 {
		return fmt.Errorf("%w: no candidate nodes from storage pool", errSchedulingNoCandidateNodes)
	}

	zoneCandidates, err := r.applyTopologyFilter(candidateNodes, true, sctx)
	if err != nil {
		return err
	}

	zoneCandidates, err = r.applyCapacityFilterAndScore(ctx, zoneCandidates, sctx)
	if err != nil {
		return err
	}

	applyAttachToBonus(zoneCandidates, sctx.AttachToNodes)

	sctx.ScoredCandidates = zoneCandidates

	// Initialize ZoneReplicaCounts for TransZonal topology
	if sctx.RSC.Spec.Topology == topologyTransZonal {
		sctx.ZoneReplicaCounts = computeReplicasByZone(sctx.AllRVRs, v1alpha1.ReplicaTypeDiskful, sctx.NodeToZone)
	}

	return nil
}

// --- Helpers: scheduling (apply)

func (r *Reconciler) applyTopologyFilter(
	candidateNodes []string,
	isDiskfulPhase bool,
	sctx *SchedulingContext,
) (map[string][]NodeCandidate, error) {
	switch sctx.RSC.Spec.Topology {
	case topologyIgnored:
		candidates := make([]NodeCandidate, 0, len(candidateNodes))
		for _, nodeName := range candidateNodes {
			candidates = append(candidates, NodeCandidate{Name: nodeName})
		}
		return map[string][]NodeCandidate{topologyIgnored: candidates}, nil

	case topologyZonal:
		return r.applyZonalTopologyFilter(candidateNodes, isDiskfulPhase, sctx)

	case topologyTransZonal:
		allowedZones := computeAllowedZones(nil, sctx.RSC.Spec.Zones, sctx.NodeToZone)
		return groupCandidateNodesByZone(candidateNodes, allowedZones, sctx.NodeToZone), nil

	default:
		return nil, fmt.Errorf("unknown RSC topology: %s", sctx.RSC.Spec.Topology)
	}
}

func (r *Reconciler) applyZonalTopologyFilter(
	candidateNodes []string,
	isDiskfulPhase bool,
	sctx *SchedulingContext,
) (map[string][]NodeCandidate, error) {
	var zonesWithScheduledDiskful []string
	for _, rvr := range sctx.ScheduledDiskful {
		zone, ok := sctx.NodeToZone[rvr.Spec.NodeName]
		if !ok || zone == "" {
			return nil, fmt.Errorf("%w: scheduled diskful replica %s is on node %s without zone label for Zonal topology",
				errSchedulingTopologyConflict, rvr.Name, rvr.Spec.NodeName)
		}
		if !slices.Contains(zonesWithScheduledDiskful, zone) {
			zonesWithScheduledDiskful = append(zonesWithScheduledDiskful, zone)
		}
	}

	if len(zonesWithScheduledDiskful) > 1 {
		return nil, fmt.Errorf("%w: scheduled diskful replicas are in multiple zones %v for Zonal topology",
			errSchedulingTopologyConflict, zonesWithScheduledDiskful)
	}

	var targetZones []string
	switch {
	case len(zonesWithScheduledDiskful) > 0:
		targetZones = zonesWithScheduledDiskful
	case !isDiskfulPhase:
		return nil, fmt.Errorf("%w: cannot schedule TieBreaker for Zonal topology: no Diskful replicas scheduled",
			errSchedulingNoCandidateNodes)
	default:
		for _, nodeName := range sctx.AttachToNodes {
			zone, ok := sctx.NodeToZone[nodeName]
			if !ok || zone == "" {
				return nil, fmt.Errorf("%w: attachTo node %s has no zone label for Zonal topology",
					errSchedulingTopologyConflict, nodeName)
			}
			if !slices.Contains(targetZones, zone) {
				targetZones = append(targetZones, zone)
			}
		}
	}

	allowedZones := computeAllowedZones(targetZones, sctx.RSC.Spec.Zones, sctx.NodeToZone)
	result := groupCandidateNodesByZone(candidateNodes, allowedZones, sctx.NodeToZone)

	if len(result) == 0 {
		return nil, fmt.Errorf("%w: no candidate nodes found after topology filtering", errSchedulingNoCandidateNodes)
	}

	return result, nil
}

func (r *Reconciler) applyCapacityFilterAndScore(
	ctx context.Context,
	zoneCandidates map[string][]NodeCandidate,
	sctx *SchedulingContext,
) (map[string][]NodeCandidate, error) {
	candidateNodeSet := make(map[string]struct{})
	for _, candidates := range zoneCandidates {
		for _, c := range candidates {
			candidateNodeSet[c.Name] = struct{}{}
		}
	}

	var lvgQueries []LVGQuery
	for lvgName, info := range sctx.LVGToNode {
		if _, ok := candidateNodeSet[info.NodeName]; !ok {
			continue
		}
		lvgQueries = append(lvgQueries, LVGQuery{
			Name:         lvgName,
			ThinPoolName: info.ThinPoolName,
		})
	}

	if len(lvgQueries) == 0 {
		return nil, fmt.Errorf("%w: no candidate nodes have LVGs from storage pool %s", errSchedulingNoCandidateNodes, sctx.RSC.Status.StoragePoolName)
	}

	var volType string
	switch sctx.StoragePoolType {
	case "LVMThin":
		volType = "thin"
	case "LVM":
		volType = "thick"
	default:
		return nil, fmt.Errorf("storage pool type is not supported: %s", sctx.StoragePoolType)
	}

	lvgScores, err := r.extenderClient.QueryLVGScores(ctx, lvgQueries, VolumeInfo{
		Name: sctx.RV.Name,
		Size: sctx.RV.Spec.Size.Value(),
		Type: volType,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errSchedulingNoCandidateNodes, err)
	}

	// Aggregate LVG scores per node: find best LVG, count suitable LVGs, sum scores.
	type nodeAggregatedLVG struct {
		BestLVGName      string
		BestThinPoolName string
		BestScore        int
		LVGCount         int
		SumScore         int
	}
	nodeAggregated := make(map[string]*nodeAggregatedLVG)

	for lvgName, info := range sctx.LVGToNode {
		score, ok := lvgScores[lvgName]
		if !ok {
			continue
		}
		agg, exists := nodeAggregated[info.NodeName]
		if !exists {
			nodeAggregated[info.NodeName] = &nodeAggregatedLVG{
				BestLVGName:      lvgName,
				BestThinPoolName: info.ThinPoolName,
				BestScore:        score,
				LVGCount:         1,
				SumScore:         score,
			}
			continue
		}
		agg.LVGCount++
		agg.SumScore += score
		if score > agg.BestScore {
			agg.BestScore = score
			agg.BestLVGName = lvgName
			agg.BestThinPoolName = info.ThinPoolName
		}
	}

	result := make(map[string][]NodeCandidate)
	for zone, candidates := range zoneCandidates {
		var filtered []NodeCandidate
		for _, c := range candidates {
			if agg, ok := nodeAggregated[c.Name]; ok {
				filtered = append(filtered, NodeCandidate{
					Name:         c.Name,
					Zone:         zone,
					BestScore:    agg.BestScore,
					LVGCount:     agg.LVGCount,
					SumScore:     agg.SumScore,
					LVGName:      agg.BestLVGName,
					ThinPoolName: agg.BestThinPoolName,
				})
			}
		}
		if len(filtered) > 0 {
			result[zone] = filtered
		}
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("%w: no nodes with sufficient storage space found", errSchedulingNoCandidateNodes)
	}

	return result, nil
}

func applyAttachToBonus(zoneCandidates map[string][]NodeCandidate, attachToNodes []string) {
	if len(attachToNodes) == 0 {
		return
	}

	attachToSet := make(map[string]struct{}, len(attachToNodes))
	for _, node := range attachToNodes {
		attachToSet[node] = struct{}{}
	}

	for zone, candidates := range zoneCandidates {
		for i := range candidates {
			if _, isAttachTo := attachToSet[candidates[i].Name]; isAttachTo {
				candidates[i].BestScore += attachToScoreBonus
			}
		}
		zoneCandidates[zone] = candidates
	}
}

func (r *Reconciler) assignReplicasToNodes(
	zoneCandidates map[string][]NodeCandidate,
	unscheduledRVRs []*v1alpha1.ReplicatedVolumeReplica,
	sctx *SchedulingContext,
	replicaTypeFilter v1alpha1.ReplicaType,
	bestEffort bool,
) ([]*v1alpha1.ReplicatedVolumeReplica, error) {
	if len(unscheduledRVRs) == 0 {
		return nil, nil
	}

	switch sctx.RSC.Spec.Topology {
	case topologyIgnored:
		return assignReplicasIgnoredTopology(zoneCandidates, unscheduledRVRs, bestEffort)
	case topologyZonal:
		return assignReplicasZonalTopology(zoneCandidates, unscheduledRVRs, bestEffort)
	case topologyTransZonal:
		return assignReplicasTransZonalTopology(zoneCandidates, unscheduledRVRs, sctx, replicaTypeFilter, bestEffort)
	default:
		return nil, fmt.Errorf("unknown topology: %s", sctx.RSC.Spec.Topology)
	}
}

func assignReplicasIgnoredTopology(
	zoneCandidates map[string][]NodeCandidate,
	unscheduledRVRs []*v1alpha1.ReplicatedVolumeReplica,
	bestEffort bool,
) ([]*v1alpha1.ReplicatedVolumeReplica, error) {
	var allCandidates []NodeCandidate
	for _, candidates := range zoneCandidates {
		allCandidates = append(allCandidates, candidates...)
	}

	var assignedRVRs []*v1alpha1.ReplicatedVolumeReplica
	for _, rvr := range unscheduledRVRs {
		selected, remaining := computeBestNode(allCandidates)
		if selected.Name == "" {
			if bestEffort {
				break
			}
			return assignedRVRs, fmt.Errorf("%w: not enough candidate nodes for all replicas", errSchedulingNoCandidateNodes)
		}
		allCandidates = remaining

		applyPlacement(rvr, selected)
		assignedRVRs = append(assignedRVRs, rvr)
	}

	return assignedRVRs, nil
}

func assignReplicasZonalTopology(
	zoneCandidates map[string][]NodeCandidate,
	unscheduledRVRs []*v1alpha1.ReplicatedVolumeReplica,
	bestEffort bool,
) ([]*v1alpha1.ReplicatedVolumeReplica, error) {
	var bestZone string
	bestZoneScore := -1

	for zone, candidates := range zoneCandidates {
		totalScore := 0
		for _, c := range candidates {
			totalScore += c.BestScore
		}
		zoneScore := totalScore * len(candidates)
		if zoneScore > bestZoneScore {
			bestZoneScore = zoneScore
			bestZone = zone
		}
	}

	if bestZone == "" {
		if bestEffort {
			return nil, nil
		}
		return nil, fmt.Errorf("%w: no zones with candidates available", errSchedulingNoCandidateNodes)
	}

	var assignedRVRs []*v1alpha1.ReplicatedVolumeReplica
	candidates := zoneCandidates[bestZone]
	for _, rvr := range unscheduledRVRs {
		selected, remaining := computeBestNode(candidates)
		if selected.Name == "" {
			if bestEffort {
				break
			}
			return assignedRVRs, fmt.Errorf("%w: not enough candidate nodes in zone %s for all replicas", errSchedulingNoCandidateNodes, bestZone)
		}
		candidates = remaining

		applyPlacement(rvr, selected)
		assignedRVRs = append(assignedRVRs, rvr)
	}
	zoneCandidates[bestZone] = candidates

	return assignedRVRs, nil
}

func assignReplicasTransZonalTopology(
	zoneCandidates map[string][]NodeCandidate,
	unscheduledRVRs []*v1alpha1.ReplicatedVolumeReplica,
	sctx *SchedulingContext,
	replicaTypeFilter v1alpha1.ReplicaType,
	bestEffort bool,
) ([]*v1alpha1.ReplicatedVolumeReplica, error) {
	if len(unscheduledRVRs) == 0 {
		return nil, nil
	}

	zoneReplicaCount := computeReplicasByZone(sctx.AllRVRs, replicaTypeFilter, sctx.NodeToZone)
	allowedZones := computeAllowedZones(nil, sctx.RSC.Spec.Zones, sctx.NodeToZone)

	availableZones := make(map[string]struct{})
	for zone, candidates := range zoneCandidates {
		if len(candidates) > 0 {
			availableZones[zone] = struct{}{}
		}
	}

	var assignedRVRs []*v1alpha1.ReplicatedVolumeReplica
	for _, rvr := range unscheduledRVRs {
		globalMinZone, globalMinCount := computeZoneWithMinReplicaCount(allowedZones, zoneReplicaCount)
		selectedZone, availableMinCount := computeZoneWithMinReplicaCount(availableZones, zoneReplicaCount)

		if selectedZone == "" {
			if bestEffort {
				break
			}
			return assignedRVRs, fmt.Errorf("%w: no zones with available nodes to place replica", errSchedulingNoCandidateNodes)
		}

		if globalMinCount < availableMinCount {
			if bestEffort {
				break
			}
			return assignedRVRs, fmt.Errorf("%w: zone %q has %d replicas but no available nodes; replica should be placed there to maintain even distribution across zones",
				errSchedulingNoCandidateNodes, globalMinZone, globalMinCount)
		}

		selected, remaining := computeBestNode(zoneCandidates[selectedZone])
		if selected.Name == "" {
			return assignedRVRs, nil
		}
		zoneCandidates[selectedZone] = remaining

		if len(remaining) == 0 {
			delete(availableZones, selectedZone)
		}

		applyPlacement(rvr, selected)
		assignedRVRs = append(assignedRVRs, rvr)
		zoneReplicaCount[selectedZone]++
	}

	return assignedRVRs, nil
}

// --- Helpers: scheduling (compute)

func computeEligibleNodeNames(eligible []v1alpha1.ReplicatedStoragePoolEligibleNode, occupied map[string]struct{}) []string {
	var result []string
	for _, node := range eligible {
		if node.Unschedulable || !node.NodeReady || !node.AgentReady {
			continue
		}
		if _, ok := occupied[node.NodeName]; ok {
			continue
		}
		result = append(result, node.NodeName)
	}
	return result
}

func computeAttachToNodes(rv *v1alpha1.ReplicatedVolume) []string {
	if rv == nil {
		return nil
	}
	return slices.Clone(rv.Status.DesiredAttachTo)
}

func computeReplicasByZone(
	replicas []*v1alpha1.ReplicatedVolumeReplica,
	replicaType v1alpha1.ReplicaType,
	nodeToZone map[string]string,
) map[string]int {
	result := make(map[string]int)
	for _, rvr := range replicas {
		if replicaType != "" && rvr.Spec.Type != replicaType {
			continue
		}
		if rvr.Spec.NodeName == "" {
			continue
		}
		zone, ok := nodeToZone[rvr.Spec.NodeName]
		if !ok || zone == "" {
			continue
		}
		result[zone]++
	}
	return result
}

func computeAllowedZones(
	targetZones []string,
	rscZones []string,
	nodeToZone map[string]string,
) map[string]struct{} {
	result := make(map[string]struct{})

	switch {
	case len(targetZones) > 0:
		for _, zone := range targetZones {
			result[zone] = struct{}{}
		}
	case len(rscZones) > 0:
		for _, zone := range rscZones {
			result[zone] = struct{}{}
		}
	default:
		for _, zone := range nodeToZone {
			if zone != "" {
				result[zone] = struct{}{}
			}
		}
	}

	return result
}

func computeZoneWithMinReplicaCount(
	zones map[string]struct{},
	zoneReplicaCount map[string]int,
) (string, int) {
	var minZone string
	minCount := -1
	for zone := range zones {
		count := zoneReplicaCount[zone]
		if minCount == -1 || count < minCount {
			minCount = count
			minZone = zone
		}
	}
	return minZone, minCount
}

func computeBestNode(candidates []NodeCandidate) (NodeCandidate, []NodeCandidate) {
	if len(candidates) == 0 {
		return NodeCandidate{}, candidates
	}

	slices.SortFunc(candidates, func(a, b NodeCandidate) int {
		// Primary: BestScore descending
		if a.BestScore != b.BestScore {
			return b.BestScore - a.BestScore
		}
		// Secondary: LVGCount descending (more LVG options = better)
		if a.LVGCount != b.LVGCount {
			return b.LVGCount - a.LVGCount
		}
		// Tertiary: SumScore descending
		return b.SumScore - a.SumScore
	})

	return candidates[0], candidates[1:]
}

func computeSchedulingFailureReason(err error) *schedulingFailureReason {
	reason := v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonSchedulingFailed
	switch {
	case errors.Is(err, errSchedulingTopologyConflict):
		reason = v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonTopologyConstraintsFailed
	case errors.Is(err, errSchedulingNoCandidateNodes):
		reason = v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonNoAvailableNodes
	case errors.Is(err, errSchedulingPending):
		reason = v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonSchedulingPending
	}
	return &schedulingFailureReason{
		reason:  reason,
		message: err.Error(),
	}
}

func computeNodeToZoneFromEligible(eligible []v1alpha1.ReplicatedStoragePoolEligibleNode) map[string]string {
	result := make(map[string]string, len(eligible))
	for _, node := range eligible {
		result[node.NodeName] = node.ZoneName
	}
	return result
}

func computeLVGToNodeFromEligible(eligible []v1alpha1.ReplicatedStoragePoolEligibleNode) map[string]LVGInfo {
	result := make(map[string]LVGInfo)
	for _, node := range eligible {
		for _, lvg := range node.LVMVolumeGroups {
			if !lvg.Ready || lvg.Unschedulable {
				continue
			}
			result[lvg.Name] = LVGInfo{
				NodeName:     node.NodeName,
				ThinPoolName: lvg.ThinPoolName,
			}
		}
	}
	return result
}

func computeStoragePoolType(eligible []v1alpha1.ReplicatedStoragePoolEligibleNode) string {
	for _, node := range eligible {
		for _, lvg := range node.LVMVolumeGroups {
			if lvg.ThinPoolName != "" {
				return "LVMThin"
			}
		}
	}
	return "LVM"
}

// --- Helpers: scheduling (other supporting)

func groupCandidateNodesByZone(
	candidateNodes []string,
	allowedZones map[string]struct{},
	nodeToZone map[string]string,
) map[string][]NodeCandidate {
	result := make(map[string][]NodeCandidate)
	for _, nodeName := range candidateNodes {
		zone, ok := nodeToZone[nodeName]
		if !ok || zone == "" {
			continue
		}
		if _, allowed := allowedZones[zone]; !allowed {
			continue
		}
		result[zone] = append(result[zone], NodeCandidate{Name: nodeName, Zone: zone})
	}
	return result
}

func updateStateAfterScheduling(sctx *SchedulingContext, assignedRVRs []*v1alpha1.ReplicatedVolumeReplica) {
	if len(assignedRVRs) == 0 {
		return
	}

	assignedNames := make(map[string]struct{}, len(assignedRVRs))
	for _, rvr := range assignedRVRs {
		assignedNames[rvr.Name] = struct{}{}
		sctx.OccupiedNodes[rvr.Spec.NodeName] = struct{}{}
		if rvr.Spec.Type == v1alpha1.ReplicaTypeDiskful {
			sctx.ScheduledDiskful = append(sctx.ScheduledDiskful, rvr)
		}
	}

	sctx.UnscheduledDiskful = filterOutAssigned(sctx.UnscheduledDiskful, assignedNames)
	sctx.UnscheduledTieBreaker = filterOutAssigned(sctx.UnscheduledTieBreaker, assignedNames)
}

func filterOutAssigned(rvrs []*v1alpha1.ReplicatedVolumeReplica, assigned map[string]struct{}) []*v1alpha1.ReplicatedVolumeReplica {
	var result []*v1alpha1.ReplicatedVolumeReplica
	for _, rvr := range rvrs {
		if _, ok := assigned[rvr.Name]; !ok {
			result = append(result, rvr)
		}
	}
	return result
}

func isRVReadyToSchedule(rv *v1alpha1.ReplicatedVolume) error {
	if rv.Finalizers == nil {
		return fmt.Errorf("%w: ReplicatedVolume has no finalizers", errSchedulingPending)
	}

	if !slices.Contains(rv.Finalizers, v1alpha1.ControllerFinalizer) {
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

// applyPlacement sets node and LVG on RVR spec. Returns true if any field changed.
func applyPlacement(rvr *v1alpha1.ReplicatedVolumeReplica, candidate NodeCandidate) bool {
	changed := false
	if rvr.Spec.NodeName != candidate.Name {
		rvr.Spec.NodeName = candidate.Name
		changed = true
	}
	if rvr.Spec.LVMVolumeGroupName != candidate.LVGName {
		rvr.Spec.LVMVolumeGroupName = candidate.LVGName
		changed = true
	}
	if rvr.Spec.LVMVolumeGroupThinPoolName != candidate.ThinPoolName {
		rvr.Spec.LVMVolumeGroupThinPoolName = candidate.ThinPoolName
		changed = true
	}
	if obju.SetLabel(rvr, v1alpha1.NodeNameLabelKey, candidate.Name) {
		changed = true
	}
	return changed
}

func applyScheduledConditionTrue(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:               v1alpha1.ReplicatedVolumeReplicaCondScheduledType,
		Status:             metav1.ConditionTrue,
		Reason:             v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonReplicaScheduled,
		ObservedGeneration: rvr.Generation,
	})
}

func applyScheduledConditionFalse(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:               v1alpha1.ReplicatedVolumeReplicaCondScheduledType,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: rvr.Generation,
	})
}

func applyNodeNameLabelIfMissing(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
	if rvr.Spec.NodeName == "" {
		return false
	}
	return obju.SetLabel(rvr, v1alpha1.NodeNameLabelKey, rvr.Spec.NodeName)
}

type schedulingFailureReason struct {
	reason  string
	message string
}

// --- Multi-I/O helpers

func (r *Reconciler) prepareSchedulingContext(
	ctx context.Context,
	rvName string,
) (*SchedulingContext, error) {
	rv, err := r.getRV(ctx, rvName)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}

	if err := isRVReadyToSchedule(rv); err != nil {
		return nil, err
	}

	rsc, err := r.getRSC(ctx, rv.Spec.ReplicatedStorageClassName)
	if err != nil {
		return nil, err
	}

	if rsc.Status.StoragePoolName == "" {
		return nil, fmt.Errorf("%w: RSC %s has no storage pool configured yet", errSchedulingPending, rsc.Name)
	}

	rsp, err := r.getRSP(ctx, rsc.Status.StoragePoolName)
	if err != nil {
		return nil, err
	}

	allRVRs, err := r.listRVRsByRV(ctx, rv.Name)
	if err != nil {
		return nil, err
	}

	eligible := rsp.Status.EligibleNodes
	sctx := &SchedulingContext{
		RV:              rv,
		RSC:             rsc,
		RSP:             rsp,
		EligibleNodes:   eligible,
		AttachToNodes:   computeAttachToNodes(rv),
		NodeToZone:      computeNodeToZoneFromEligible(eligible),
		LVGToNode:       computeLVGToNodeFromEligible(eligible),
		StoragePoolType: computeStoragePoolType(eligible),
		OccupiedNodes:   make(map[string]struct{}),
	}

	for i := range allRVRs {
		rvr := &allRVRs[i]
		if rvr.Spec.NodeName != "" {
			sctx.OccupiedNodes[rvr.Spec.NodeName] = struct{}{}
		}
		if !rvr.DeletionTimestamp.IsZero() {
			continue
		}
		sctx.AllRVRs = append(sctx.AllRVRs, rvr)

		scheduled := rvr.Spec.NodeName != ""
		switch rvr.Spec.Type {
		case v1alpha1.ReplicaTypeDiskful:
			if scheduled {
				sctx.ScheduledDiskful = append(sctx.ScheduledDiskful, rvr)
			} else {
				sctx.UnscheduledDiskful = append(sctx.UnscheduledDiskful, rvr)
			}
		case v1alpha1.ReplicaTypeTieBreaker:
			if scheduled {
				sctx.ScheduledTieBreaker = append(sctx.ScheduledTieBreaker, rvr)
			} else {
				sctx.UnscheduledTieBreaker = append(sctx.UnscheduledTieBreaker, rvr)
			}
		}
	}

	return sctx, nil
}

func (r *Reconciler) updateScheduledRVRs(ctx context.Context, rvrs []*v1alpha1.ReplicatedVolumeReplica) error {
	for _, rvr := range rvrs {
		base := rvr.DeepCopy()
		base.Spec.NodeName = ""
		base.Spec.LVMVolumeGroupName = ""
		base.Spec.LVMVolumeGroupThinPoolName = ""
		delete(base.Labels, v1alpha1.NodeNameLabelKey)

		// Use optimistic locking to prevent overwriting concurrent modifications.
		if err := r.patchRVR(ctx, rvr, base, true); err != nil {
			return err
		}

		statusBase := rvr.DeepCopy()
		applyScheduledConditionTrue(rvr)
		if err := r.patchRVRStatus(ctx, rvr, statusBase); err != nil {
			return err
		}
	}
	return nil
}

func (r *Reconciler) updateScheduledConditionOnScheduledRVRs(ctx context.Context, sctx *SchedulingContext) error {
	for _, rvr := range sctx.AllRVRs {
		if rvr.Spec.NodeName == "" {
			continue
		}

		base := rvr.DeepCopy()
		labelChanged := applyNodeNameLabelIfMissing(rvr)
		if labelChanged {
			if err := r.patchRVR(ctx, rvr, base, false); err != nil {
				return err
			}
		}

		statusBase := rvr.DeepCopy()
		condChanged := applyScheduledConditionTrue(rvr)
		if condChanged {
			if err := r.patchRVRStatus(ctx, rvr, statusBase); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *Reconciler) setScheduledConditionFalseOnRVRs(
	ctx context.Context,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	reason *schedulingFailureReason,
) error {
	for _, rvr := range rvrs {
		base := rvr.DeepCopy()
		changed := applyScheduledConditionFalse(rvr, reason.reason, reason.message)
		if !changed {
			continue
		}
		if err := r.patchRVRStatus(ctx, rvr, base); err != nil {
			return err
		}
	}
	return nil
}

func (r *Reconciler) setFailedScheduledConditionOnUnscheduledRVRs(
	ctx context.Context,
	rvName string,
	reason *schedulingFailureReason,
) error {
	allRVRs, err := r.listRVRsByRV(ctx, rvName)
	if err != nil {
		return err
	}

	for i := range allRVRs {
		rvr := &allRVRs[i]
		if !rvr.DeletionTimestamp.IsZero() {
			continue
		}
		if rvr.Spec.NodeName != "" {
			continue
		}

		base := rvr.DeepCopy()
		changed := applyScheduledConditionFalse(rvr, reason.reason, reason.message)
		if !changed {
			continue
		}
		if err := r.patchRVRStatus(ctx, rvr, base); err != nil {
			return err
		}
	}

	return nil
}

// --- Single-call I/O helpers: RV

func (r *Reconciler) getRV(ctx context.Context, name string) (*v1alpha1.ReplicatedVolume, error) {
	rv := &v1alpha1.ReplicatedVolume{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: name}, rv); err != nil {
		return nil, fmt.Errorf("unable to get ReplicatedVolume %s: %w", name, err)
	}
	return rv, nil
}

// --- Single-call I/O helpers: RSC

func (r *Reconciler) getRSC(ctx context.Context, name string) (*v1alpha1.ReplicatedStorageClass, error) {
	rsc := &v1alpha1.ReplicatedStorageClass{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: name}, rsc); err != nil {
		return nil, fmt.Errorf("unable to get ReplicatedStorageClass %s: %w", name, err)
	}
	return rsc, nil
}

// --- Single-call I/O helpers: RSP

func (r *Reconciler) getRSP(ctx context.Context, name string) (*v1alpha1.ReplicatedStoragePool, error) {
	rsp := &v1alpha1.ReplicatedStoragePool{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: name}, rsp); err != nil {
		return nil, fmt.Errorf("unable to get ReplicatedStoragePool %s: %w", name, err)
	}
	return rsp, nil
}

// --- Single-call I/O helpers: RVR

func (r *Reconciler) listRVRsByRV(ctx context.Context, rvName string) ([]v1alpha1.ReplicatedVolumeReplica, error) {
	list := &v1alpha1.ReplicatedVolumeReplicaList{}
	if err := r.cl.List(ctx, list, client.MatchingFields{
		indexes.IndexFieldRVRByReplicatedVolumeName: rvName,
	}); err != nil {
		return nil, fmt.Errorf("unable to list ReplicatedVolumeReplicas for RV %s: %w", rvName, err)
	}
	return list.Items, nil
}

func (r *Reconciler) patchRVR(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	base *v1alpha1.ReplicatedVolumeReplica,
	optimisticLock bool,
) error {
	var patch client.Patch
	if optimisticLock {
		patch = client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})
	} else {
		patch = client.MergeFrom(base)
	}
	if err := r.cl.Patch(ctx, rvr, patch); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to patch RVR %s: %w", rvr.Name, err)
	}
	return nil
}

func (r *Reconciler) patchRVRStatus(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	base *v1alpha1.ReplicatedVolumeReplica,
) error {
	patch := client.MergeFrom(base)
	if err := r.cl.Status().Patch(ctx, rvr, patch); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to patch RVR %s status: %w", rvr.Name, err)
	}
	return nil
}
