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
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/idset"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes"
	rvrllvname "github.com/deckhouse/sds-replicated-volume/images/controller/internal/rvr_llv_name"
	schext "github.com/deckhouse/sds-replicated-volume/images/controller/internal/scheduler_extender"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

// ──────────────────────────────────────────────────────────────────────────────
// Wiring / construction
//

type Reconciler struct {
	cl             client.Client
	log            logr.Logger
	scheme         *runtime.Scheme
	extenderClient schext.Client
}

func NewReconciler(cl client.Client, log logr.Logger, scheme *runtime.Scheme, extenderClient schext.Client) *Reconciler {
	return &Reconciler{
		cl:             cl,
		log:            log,
		scheme:         scheme,
		extenderClient: extenderClient,
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile
//

// Reconcile pattern: Pure orchestration.
// Each RVR is reconciled independently; errors on one RVR don't block others.
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	rf := flow.BeginRootReconcile(ctx)

	rv, err := r.getRV(rf.Ctx(), req.Name)
	if err != nil {
		return rf.Fail(err).ToCtrl()
	}

	rvrs, err := r.getRVRsByRVName(rf.Ctx(), req.Name)
	if err != nil {
		return rf.Fail(err).ToCtrl()
	}

	all := idset.FromAll(rvrs)

	// Guard 1: RV not found — nothing to schedule.
	if rv == nil {
		return r.reconcileRVRsCondition(rf.Ctx(), rvrs, all,
			metav1.ConditionUnknown,
			v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonWaitingForReplicatedVolume,
			"ReplicatedVolume not found; waiting for it be present").
			Enrichf("setting WaitingForReplicatedVolume: RV not found").ToCtrl()
	}

	// Guard 2: RV has no configuration yet — RV controller hasn't reconciled it.
	if rv.Status.Configuration == nil {
		return r.reconcileRVRsCondition(rf.Ctx(), rvrs, all,
			metav1.ConditionUnknown,
			v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonWaitingForReplicatedVolume,
			"ReplicatedVolume has no configuration yet; waiting for it to appear").
			Enrichf("setting WaitingForReplicatedVolume: RV has no configuration").ToCtrl()
	}

	rsp, err := r.getRSP(rf.Ctx(), rv.Status.Configuration.StoragePoolName)
	if err != nil {
		return rf.Fail(err).ToCtrl()
	}

	// Guard 3: RSP not found — storage pool doesn't exist yet.
	if rsp == nil {
		return r.reconcileRVRsCondition(rf.Ctx(), rvrs, all,
			metav1.ConditionUnknown,
			v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonWaitingForReplicatedVolume,
			"ReplicatedStoragePool not found; waiting for it to be present").
			Enrichf("setting WaitingForReplicatedVolume: RSP %q not found", rv.Status.Configuration.StoragePoolName).ToCtrl()
	}

	// Build the scheduling context: eligible nodes, topology, zone layout,
	// reservation parameters, and replica-type sets (All, Access, Diskful,
	// TieBreaker, Deleting, Scheduled) with OccupiedNodes — all computed in
	// a single pass over rvrs.
	sctx := computeSchedulingContext(rv, rsp, rvrs)

	// Access replicas (diskless) don't need scheduling — they are created
	// directly on the node where they are needed. Remove the Scheduled
	// condition from them if it was set previously (e.g., the replica
	// type was changed from Diskful/TieBreaker to Access), and exclude
	// them from all subsequent scheduling logic.
	var outcome flow.ReconcileOutcome
	if sctx.Access.Len() > 0 {
		outcome := r.reconcileRVRsConditionAbsent(rf.Ctx(), rvrs, sctx.Access)
		if outcome.ShouldReturn() {
			return outcome.ToCtrl()
		}
	}

	// Mark replicas that already have a node assigned and passed eligible-node
	// validation as successfully scheduled.
	if sctx.Scheduled.Len() > 0 {
		outcome = outcome.Merge(r.reconcileRVRsCondition(rf.Ctx(), rvrs, sctx.Scheduled,
			metav1.ConditionTrue,
			v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonScheduled,
			"Replica scheduled successfully"))
		if outcome.ShouldReturn() {
			return outcome.ToCtrl()
		}
	}

	// All relevant replicas are already scheduled — nothing more to do.
	// Guard: All != 0 protects against RVRs with non-standard names whose
	// IDs fall outside the 0-31 range and silently map to 0 in IDSet.
	scheduledNonAccess := sctx.Scheduled.Difference(sctx.Access)
	allNonAccess := sctx.All.Difference(sctx.Access)
	if scheduledNonAccess == allNonAccess {
		return rf.Done().ToCtrl()
	}

	// Replicas that still need scheduling. Deleting RVRs (DeletionTimestamp
	// set) are excluded — they are being removed and should not be placed.
	unscheduled := sctx.All.Difference(sctx.Scheduled).Difference(sctx.Deleting)

	// Phase 2: Schedule unscheduled Diskful RVRs (per-RVR pipeline).
	unscheduledDiskful := unscheduled.Intersect(sctx.Diskful)
	for _, rvr := range rvrs {
		if !unscheduledDiskful.Contains(rvr.ID()) {
			continue
		}
		outcome = outcome.Merge(r.reconcileOneRVRScheduling(rf.Ctx(), rvr, sctx).
			Enrichf("scheduling Diskful"))
	}
	if outcome.ShouldReturn() {
		return outcome.ToCtrl()
	}

	// Phase 3: Schedule unscheduled TieBreaker RVRs (per-RVR pipeline, no scoring).
	unscheduledTieBreaker := unscheduled.Intersect(sctx.TieBreaker)
	for _, rvr := range rvrs {
		if !unscheduledTieBreaker.Contains(rvr.ID()) {
			continue
		}
		outcome = outcome.Merge(r.reconcileOneRVRScheduling(rf.Ctx(), rvr, sctx).
			Enrichf("scheduling TieBreaker"))
	}

	return rf.Done().ToCtrl()
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: rvrs-condition-absent
//

// reconcileRVRsConditionAbsent removes the Scheduled condition from every RVR
// whose ID is in rvrIDs. RVRs that don't have the condition are skipped.
// NotFound errors on individual patches are silently ignored.
//
// Reconcile pattern: In-place reconciliation
func (r *Reconciler) reconcileRVRsConditionAbsent(
	ctx context.Context,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	rvrIDs idset.IDSet,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "rvrs-condition-absent", "rvrIDs", rvrIDs.String())
	defer rf.OnEnd(&outcome)

	for _, rvr := range rvrs {
		if !rvrIDs.Contains(rvr.ID()) {
			continue
		}
		if !obju.HasStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondScheduledType) {
			continue
		}
		base := rvr.DeepCopy()
		obju.RemoveStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondScheduledType)
		if err := r.patchRVRStatus(rf.Ctx(), rvr, base); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return rf.Fail(err)
		}
	}

	return rf.Continue()
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: rvrs-condition
//

// reconcileRVRsCondition sets the Scheduled condition (with the given status,
// reason and message) on every RVR whose ID is in rvrIDs.
// Pass idset.All to affect all RVRs unconditionally.
// NotFound errors on individual patches are silently ignored (the RVR may have
// been deleted concurrently).
//
// Reconcile pattern: In-place reconciliation
func (r *Reconciler) reconcileRVRsCondition(
	ctx context.Context,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	rvrIDs idset.IDSet,
	status metav1.ConditionStatus,
	reason,
	message string,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "rvrs-condition", "rvrIDs", rvrIDs.String())
	defer rf.OnEnd(&outcome)

	for _, rvr := range rvrs {
		if !rvrIDs.Contains(rvr.ID()) {
			continue
		}
		outcome = outcome.Merge(r.reconcileRVRCondition(rf.Ctx(), rvr, status, reason, message).
			Enrichf("setting condition on rvr %s", rvr.Name))
		if outcome.ShouldReturn() {
			return outcome
		}
	}

	return rf.Continue()
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: schedule-rvr
//

// reconcileOneRVRScheduling builds a fresh candidate pipeline for a single RVR,
// selects the best candidate, and places the RVR.
// After successful placement, mutates occupied and ReplicasByZone so subsequent
// calls see up-to-date state.
//
// Reconcile pattern: In-place reconciliation.
func (r *Reconciler) reconcileOneRVRScheduling(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	sctx *schedulingContext,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "schedule-rvr", "rvr", rvr.Name)
	defer rf.OnEnd(&outcome)

	logger := rf.Log()

	var p SchedulingPipeline

	// Build per-RVR pipeline from scratch.
	//
	// RSP.Status.EligibleNodes already contains only the nodes / LVGs /
	// thin-pools that satisfy the RSC constraints, including the
	// user-configured zone(s). Therefore we do NOT filter by zone
	// membership here — that is already handled by the RSP controller.
	// The zone predicates below (Zonal / TransZonal) only choose which
	// of the eligible zones to prefer for replica placement.
	isDiskful := rvr.Spec.Type == v1alpha1.ReplicaTypeDiskful
	if isDiskful {
		p = TakeLVGsFromEligibleNodes(sctx.EligibleNodes, logger)
	} else {
		p = TakeOnlyNodesFromEligibleNodes(sctx.EligibleNodes, logger)
	}

	// Exclude nodes that are unschedulable, not ready, or whose agent is down.
	p = WithPredicate(p, "node not ready", logger, func(e *CandidateEntry) *CandidateEntry {
		if e.Node.Unschedulable || !e.Node.NodeReady || !e.Node.AgentReady {
			return nil
		}
		return e
	})

	// Exclude LVGs that are unschedulable or not ready (Diskful only).
	if isDiskful {
		p = WithPredicate(p, "LVG not ready", logger, func(e *CandidateEntry) *CandidateEntry {
			if e.LVG.Unschedulable || !e.LVG.Ready {
				return nil
			}
			return e
		})
	}

	// Fully unscheduled RVR (no NodeName): exclude nodes already occupied
	// by other replicas so each replica lands on a separate node.
	//
	// Partially scheduled RVR (NodeName set, but LVG missing): narrow
	// the pipeline to only the assigned node — we just need to pick an
	// LVG on it. The "node occupied" filter is intentionally skipped
	// because the RVR's own node is in OccupiedNodes and would block
	// itself.
	if rvr.Spec.NodeName == "" {
		occupied := sctx.OccupiedNodes
		p = WithPredicate(p, "node occupied", logger, func(e *CandidateEntry) *CandidateEntry {
			if _, ok := occupied[e.Node.NodeName]; ok {
				return nil
			}
			return e
		})
	} else {
		nodeName := rvr.Spec.NodeName
		p = WithPredicate(p, "node mismatch (assigned)", logger, func(e *CandidateEntry) *CandidateEntry {
			if e.Node.NodeName != nodeName {
				return nil
			}
			return e
		})
	}

	// Zonal topology: all replicas (Diskful and TieBreaker) should land in
	// the same zone. Pick the zone(s) that already hold the majority of
	// Diskful replicas so that new replicas (of any type) follow them.
	// If replicas are already spread across multiple zones (user error or
	// migration), prefer the zone with the most Diskful to minimize
	// cross-zone divergence.
	if sctx.Topology == v1alpha1.TopologyZonal {
		zones := computePreferredZones(sctx.ReplicasByZone, func(a, b idset.IDSet) int {
			return cmp.Compare(
				a.Intersect(sctx.Diskful).Len(),
				b.Intersect(sctx.Diskful).Len(),
			)
		})

		if len(zones) > 1 {
			logger.Info("zonal topology: Diskful replicas are spread across multiple zones; this is an inconsistent state — preferring zones with the most Diskful",
				"preferredZones", zones)
		}

		if len(zones) < len(sctx.ReplicasByZone) {
			p = WithPredicate(p, "zone (zonal majority)", logger, func(e *CandidateEntry) *CandidateEntry {
				if !slices.Contains(zones, e.Node.ZoneName) {
					return nil
				}
				return e
			})
		}
	}

	// TransZonal topology: replicas should be evenly spread across zones.
	if sctx.Topology == v1alpha1.TopologyTransZonal {
		var compare func(a, b idset.IDSet) int
		if isDiskful {
			// Diskful replicas participate in the quorum, so they must be spread
			// evenly across zones for availability. The rv_controller is
			// responsible for creating the right number of replicas; this
			// scheduler only decides where to place them.
			// Preferred zone: fewest Diskful replicas.
			compare = func(a, b idset.IDSet) int {
				return -1 * cmp.Compare(
					a.Intersect(sctx.Diskful).Len(),
					b.Intersect(sctx.Diskful).Len(),
				)
			}
		} else {
			// TieBreaker replicas do not participate in the main quorum, but in
			// some configurations they form a separate tie-breaker quorum used to
			// sustain the main one — so they must be spread evenly as well.
			// Preferred zone: fewest total replicas (Diskful + TieBreaker), and
			// among those — fewest TieBreaker replicas. This ensures TieBreakers
			// land in zones with less overall load first; when total counts are
			// equal, they fill zones that have more Diskful relative to TieBreakers,
			// keeping the tie-breaker quorum balanced independently.
			allReplicas := sctx.Diskful.Union(sctx.TieBreaker)
			compare = func(a, b idset.IDSet) int {
				totalA := a.Intersect(allReplicas).Len()
				totalB := b.Intersect(allReplicas).Len()
				if c := cmp.Compare(totalA, totalB); c != 0 {
					return -c
				}
				return -cmp.Compare(
					a.Intersect(sctx.TieBreaker).Len(),
					b.Intersect(sctx.TieBreaker).Len(),
				)
			}
		}
		zones := computePreferredZones(sctx.ReplicasByZone, compare)
		if len(zones) < len(sctx.ReplicasByZone) {
			p = WithPredicate(p, "zone (transZonal min replicas)", logger, func(e *CandidateEntry) *CandidateEntry {
				if !slices.Contains(zones, e.Node.ZoneName) {
					return nil
				}
				return e
			})
		}
	}

	// reservationID identifies the capacity reservation in the scheduler-extender.
	// It is either namespace/pvc (set by CSI via annotation on the RV) or the
	// deterministic LVMLogicalVolume name that will be created for this RVR.
	reservationID := sctx.ReservationID
	if reservationID == "" {
		reservationID = rvrllvname.ComputeLLVName(
			rvr.Name, rvr.Spec.LVMVolumeGroupName, rvr.Spec.LVMVolumeGroupThinPoolName)
	}

	// Scoring (Diskful only): eagerly query the extender and merge-join scores.
	if isDiskful {
		var err error
		p, err = FilteredAndScoredBySchedulerExtender(rf.Ctx(), p, r.extenderClient, reservationID, 2*time.Second, sctx.Size, logger)
		if err != nil {
			return r.reconcileRVRCondition(rf.Ctx(), rvr, metav1.ConditionFalse,
				v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonExtenderUnavailable,
				"Scheduling is temporarily unavailable: capacity scoring service is unreachable").
				Merge(rf.Fail(err))
		}

		// Attach-to bonus (applied after scoring).
		// attachToScoreBonus is added to the scheduling score of nodes that are
		// already listed in RV's attachToNodes, so they are strongly preferred
		// when placing new replicas.
		attachToNodes := sctx.AttachToNodes
		if len(attachToNodes) > 0 {
			attachSet := make(map[string]struct{}, len(attachToNodes))
			for _, n := range attachToNodes {
				attachSet[n] = struct{}{}
			}
			p = WithPredicate(p, "attach-to bonus", logger, func(e *CandidateEntry) *CandidateEntry {
				if _, ok := attachSet[e.Node.NodeName]; ok {
					e.Score += 1000 // Attach to bonus
				}
				return e
			})
		}
	}

	// When volumeAccess != Any, prefer nodes with more than one LVG.
	// If a disk fails, having a second LVG on the same node allows
	// migrating the volume locally without violating volumeAccess
	// constraints (the pod can keep running on the same node).
	if isDiskful && sctx.VolumeAccess != v1alpha1.VolumeAccessAny {
		p = WithNodeScore(p, "multi-LVG node bonus", logger,
			func(_ string, group []*CandidateEntry) int {
				if len(group) > 1 {
					return 2
				}
				return 0
			})
	}

	// Zone capacity penalty (Diskful + Zonal only): penalize zones that do
	// not have enough free nodes to host the required number of Diskful
	// replicas. For TransZonal this is unnecessary — replicas are already
	// spread across zones by the zone filtering predicate above.
	//
	// The required Diskful count depends on the replication mode:
	//   None                       → 1
	//   Availability/Consistency   → 2
	//   ConsistencyAndAvailability → 3
	//
	// We subtract already-scheduled Diskful to get the remaining demand.
	// By this point the "node occupied" predicate has already filtered
	// out occupied nodes, so every candidate in the pipeline is on a
	// free node. We just count unique nodes per zone.
	// Zones where free nodes < remaining demand receive a -800 penalty,
	// making them strongly disfavored (the extender returns scores in
	// the 0–10 range, so -800 pushes these zones to the bottom and
	// they will only be chosen as a last resort).
	if isDiskful && sctx.Topology == v1alpha1.TopologyZonal {
		requiredDiskful := 1
		switch sctx.Replication {
		case v1alpha1.ReplicationAvailability, v1alpha1.ReplicationConsistency:
			requiredDiskful = 2
		case v1alpha1.ReplicationConsistencyAndAvailability:
			requiredDiskful = 3
		}
		scheduledDiskful := sctx.Scheduled.Intersect(sctx.Diskful).Len()
		remainingDemand := requiredDiskful - scheduledDiskful
		if remainingDemand < 0 {
			remainingDemand = 0
		}

		if remainingDemand > 0 {
			p = WithZoneScore(p, "zone capacity penalty", logger,
				func(_ string, group []*CandidateEntry) int {
					// Count unique free nodes (occupied already filtered out).
					seen := make(map[string]struct{}, len(group))
					for _, e := range group {
						seen[e.Node.NodeName] = struct{}{}
					}
					if len(seen) < remainingDemand {
						return -800
					}
					return 0
				})
		}
	}

	best, summary := SelectBest(p, func(a, b *CandidateEntry) bool {
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if a.Node.NodeName != b.Node.NodeName {
			return a.Node.NodeName < b.Node.NodeName
		}
		return a.LVGName() < b.LVGName()
	})

	if best == nil {
		return r.reconcileRVRCondition(rf.Ctx(), rvr, metav1.ConditionFalse,
			v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonSchedulingFailed, summary)
	}

	if isDiskful {
		if err := r.extenderClient.NarrowReservation(rf.Ctx(), reservationID, 60*time.Second, schext.LVMVolumeGroup{
			LVGName:      best.LVGName(),
			ThinPoolName: best.ThinPoolName(),
		}); err != nil {
			return r.reconcileRVRCondition(rf.Ctx(), rvr, metav1.ConditionFalse,
				v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonExtenderUnavailable,
				"Scheduling is temporarily unavailable: failed to confirm capacity reservation").
				Merge(rf.Fail(err))
		}
	}

	outcome = r.reconcileRVRPlacement(rf.Ctx(), rvr, best)
	if outcome.ShouldReturn() {
		return outcome
	}

	// Placement succeeded — update scheduling context for subsequent RVRs.
	sctx.Update(rvr, best)

	return rf.Continue()
}

// --- apply ---

// applyPlacementFromEntry sets node, LVG and thin pool on RVR spec from a
// CandidateEntry. Returns true if any field changed.
func applyPlacementFromEntry(rvr *v1alpha1.ReplicatedVolumeReplica, entry *CandidateEntry) bool {
	changed := false

	if rvr.Spec.NodeName != entry.Node.NodeName {
		rvr.Spec.NodeName = entry.Node.NodeName
		changed = true
	}

	if rvr.Spec.LVMVolumeGroupName != entry.LVGName() {
		rvr.Spec.LVMVolumeGroupName = entry.LVGName()
		changed = true
	}

	if rvr.Spec.LVMVolumeGroupThinPoolName != entry.ThinPoolName() {
		rvr.Spec.LVMVolumeGroupThinPoolName = entry.ThinPoolName()
		changed = true
	}

	return changed
}

// --- compute ---

// schedulingContext holds mutable scheduling state shared across per-RVR
// pipeline runs within a single Reconcile invocation.
type schedulingContext struct {
	// ReservationID is either the CSI-provided namespace/pvc annotation or
	// the deterministic LVMLogicalVolume name used as a scheduler-extender
	// reservation key.
	ReservationID string

	// Size is the requested volume size in bytes.
	Size int64

	// EligibleNodes are the candidate nodes from the RSP, sorted by NodeName.
	// OccupiedNodes tracks nodes already assigned to an RVR in this Reconcile.
	// AttachToNodes lists nodes where the volume must be attached (from RV).
	EligibleNodes []v1alpha1.ReplicatedStoragePoolEligibleNode
	OccupiedNodes map[string]struct{}
	AttachToNodes []string

	// Topology and zone layout.
	//
	// ReplicasByZone maps zone name → IDSet of replicas in that zone.
	// Keys are derived from EligibleNodes (unique ZoneName values); values
	// may be empty (no replicas yet).
	//
	// TransZonal: RSP always lists explicit non-empty zones, and the RSP
	// controller guarantees EligibleNodes contains only nodes from those
	// zones — so ReplicasByZone keys are exactly the RSP zones.
	//
	// Zonal / Ignored: RSP MAY have no zones. Nodes may or may not have
	// a ZoneName set (mixed). ReplicasByZone is built from whatever
	// ZoneName each node carries, so "" is a valid key ("zone unknown").
	Topology       v1alpha1.ReplicatedStorageClassTopology
	ReplicasByZone map[string]idset.IDSet

	// Replication mode and volume access from the resolved RSC configuration.
	Replication  v1alpha1.ReplicatedStorageClassReplication
	VolumeAccess v1alpha1.ReplicatedStorageClassVolumeAccess

	// Replica type sets (computed in one pass over rvrs).
	All        idset.IDSet
	Access     idset.IDSet
	Diskful    idset.IDSet
	TieBreaker idset.IDSet
	Deleting   idset.IDSet
	Scheduled  idset.IDSet
}

// computeSchedulingContext builds a schedulingContext from RV, RSP and RVRs.
// All replica-type sets (All, Access, Diskful, TieBreaker, Deleting, Scheduled)
// and OccupiedNodes are computed in a single pass over rvrs.
// EligibleNodes are sorted by NodeName.
func computeSchedulingContext(
	rv *v1alpha1.ReplicatedVolume,
	rsp *v1alpha1.ReplicatedStoragePool,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
) *schedulingContext {
	eligibleNodes := rsp.Status.EligibleNodes

	// Safety sort: eligible nodes should arrive sorted from the API, but ensure it.
	slices.SortFunc(eligibleNodes, func(a, b v1alpha1.ReplicatedStoragePoolEligibleNode) int {
		return cmp.Compare(a.NodeName, b.NodeName)
	})

	sctx := &schedulingContext{
		EligibleNodes: eligibleNodes,
		Topology:      rv.Status.Configuration.Topology,
		Replication:   rv.Status.Configuration.Replication,
		VolumeAccess:  rv.Status.Configuration.VolumeAccess,
		AttachToNodes: rv.Status.DesiredAttachTo,
		ReservationID: rv.GetAnnotations()[v1alpha1.SchedulingReservationIDAnnotationKey],
		Size:          rv.Spec.Size.Value(),
		OccupiedNodes: make(map[string]struct{}, len(rvrs)),
	}

	poolType := rsp.Spec.Type

	// Single pass: classify each RVR by type, scheduling state, and deletion.
	for _, rvr := range rvrs {
		id := rvr.ID()
		sctx.All.Add(id)

		switch rvr.Spec.Type {
		case v1alpha1.ReplicaTypeAccess:
			sctx.Access.Add(id)
		case v1alpha1.ReplicaTypeDiskful:
			sctx.Diskful.Add(id)
		case v1alpha1.ReplicaTypeTieBreaker:
			sctx.TieBreaker.Add(id)
		}

		if rvr.DeletionTimestamp != nil {
			sctx.Deleting.Add(id)
		}

		if rvr.Spec.NodeName != "" {
			sctx.OccupiedNodes[rvr.Spec.NodeName] = struct{}{}

			if rvr.Spec.LVMVolumeGroupName != "" {
				scheduled := false
				switch poolType {
				case v1alpha1.ReplicatedStoragePoolTypeLVMThin:
					scheduled = rvr.Spec.LVMVolumeGroupThinPoolName != ""
				case v1alpha1.ReplicatedStoragePoolTypeLVM:
					scheduled = rvr.Spec.LVMVolumeGroupThinPoolName == ""
				}
				if scheduled {
					sctx.Scheduled.Add(id)
				}
			}
		}
	}

	sctx.ReplicasByZone = computeReplicasByZone(eligibleNodes, rvrs)

	return sctx
}

// Update records a successful placement: marks the node as occupied and adds
// the RVR to its zone in ReplicasByZone.
func (sctx *schedulingContext) Update(rvr *v1alpha1.ReplicatedVolumeReplica, entry *CandidateEntry) {
	id := rvr.ID()
	sctx.OccupiedNodes[entry.Node.NodeName] = struct{}{}
	sctx.Scheduled.Add(id)
	s := sctx.ReplicasByZone[entry.Node.ZoneName]
	s.Add(id)
	sctx.ReplicasByZone[entry.Node.ZoneName] = s
}

// computeReplicasByZone builds a map of zone → IDSet of all replicas in
// that zone. Keys are derived from eligibleNodes (not from RSP.Spec.Zones),
// so it works correctly even when RSP has no explicit zones and nodes carry
// mixed or empty ZoneNames. Uses binary search on the sorted eligibleNodes
// slice to resolve node → zone.
func computeReplicasByZone(
	eligibleNodes []v1alpha1.ReplicatedStoragePoolEligibleNode,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
) map[string]idset.IDSet {
	// Collect unique zones from eligibleNodes.
	replicasByZone := make(map[string]idset.IDSet)
	for i := range eligibleNodes {
		if _, ok := replicasByZone[eligibleNodes[i].ZoneName]; !ok {
			replicasByZone[eligibleNodes[i].ZoneName] = 0
		}
	}

	// Populate with existing replicas (all types).
	for _, rvr := range rvrs {
		// Skip unscheduled replicas — they have no node yet.
		if rvr.Spec.NodeName == "" {
			continue
		}

		// Resolve node → zone via binary search on sorted eligibleNodes.
		idx, found := slices.BinarySearchFunc(eligibleNodes, rvr.Spec.NodeName, func(n v1alpha1.ReplicatedStoragePoolEligibleNode, name string) int {
			return cmp.Compare(n.NodeName, name)
		})
		if !found {
			continue
		}
		zone := eligibleNodes[idx].ZoneName

		if s, ok := replicasByZone[zone]; ok {
			s.Add(rvr.ID())
			replicasByZone[zone] = s
		}
	}

	return replicasByZone
}

// computePreferredZones returns the zone names whose replica set is the "best"
// according to compare(repIDsA, repIDsB), which follows cmp.Compare semantics.
// The function finds the maximum: the zone(s) for which compare returns the
// highest value relative to others.
func computePreferredZones(
	replicasByZone map[string]idset.IDSet,
	compare func(repIDsA, repIDsB idset.IDSet) int,
) []string {
	if len(replicasByZone) == 0 {
		return nil
	}

	// Find the "best" replica set (maximum by compare).
	var best idset.IDSet
	first := true
	for _, replicas := range replicasByZone {
		if first || compare(replicas, best) > 0 {
			best = replicas
			first = false
		}
	}

	// Collect all zones equivalent to the best (compare == 0).
	var result []string
	for zone, replicas := range replicasByZone {
		if compare(replicas, best) == 0 {
			result = append(result, zone)
		}
	}
	return result
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: place-rvr
//

// reconcileRVRPlacement applies placement from a CandidateEntry to an RVR and
// patches both the main (spec) and status domains.
//
// Reconcile pattern: In-place reconciliation
func (r *Reconciler) reconcileRVRPlacement(ctx context.Context, rvr *v1alpha1.ReplicatedVolumeReplica, entry *CandidateEntry) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "place-rvr")
	defer rf.OnEnd(&outcome)

	// Main domain: apply placement → patch.
	base := rvr.DeepCopy()
	if applyPlacementFromEntry(rvr, entry) {
		if err := r.patchRVR(rf.Ctx(), rvr, base); err != nil {
			return rf.Fail(err)
		}
	}

	// Status domain: set Scheduled=True.
	return r.reconcileRVRCondition(rf.Ctx(), rvr, metav1.ConditionTrue,
		v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonScheduled, "Replica scheduled successfully")
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: rvr-condition
//

// reconcileRVRCondition sets the Scheduled condition on a single RVR if it
// differs from the current state. NotFound errors are silently ignored.
//
// Reconcile pattern: In-place reconciliation
func (r *Reconciler) reconcileRVRCondition(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	status metav1.ConditionStatus,
	reason,
	message string,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "rvr-condition")
	defer rf.OnEnd(&outcome)

	if obju.StatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondScheduledType).
		StatusEqual(status).ReasonEqual(reason).MessageEqual(message).Eval() {
		return rf.Continue()
	}

	base := rvr.DeepCopy()

	obju.SetStatusCondition(rvr, metav1.Condition{
		Type:               v1alpha1.ReplicatedVolumeReplicaCondScheduledType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: rvr.Generation,
	})

	if err := r.patchRVRStatus(rf.Ctx(), rvr, base); err != nil {
		if apierrors.IsNotFound(err) {
			return rf.Continue()
		}
		return rf.Fail(err)
	}

	return rf.Continue()
}

// ──────────────────────────────────────────────────────────────────────────────
// Single-call I/O helpers
//

// --- RVR ---

func (r *Reconciler) getRVRsByRVName(ctx context.Context, rvName string) ([]*v1alpha1.ReplicatedVolumeReplica, error) {
	list := &v1alpha1.ReplicatedVolumeReplicaList{}
	if err := r.cl.List(ctx, list, client.MatchingFields{
		indexes.IndexFieldRVRByReplicatedVolumeName: rvName,
	}); err != nil {
		return nil, err
	}

	// HACK: Build a slice of pointers from list.Items (which is []T, not []*T).
	//
	// Ideally, ReplicatedVolumeReplicaList.Items should be []*ReplicatedVolumeReplica
	// so that client.List returns pointers directly and we avoid this copy loop.
	// However, changing the API type now would require refactoring many other controllers
	// that use ReplicatedVolumeReplicaList, so we defer that change.
	//
	// TODO: Change ReplicatedVolumeReplicaList.Items to []*ReplicatedVolumeReplica,
	// refactor all dependent code, and remove this workaround.
	result := make([]*v1alpha1.ReplicatedVolumeReplica, len(list.Items))
	for i := range list.Items {
		result[i] = &list.Items[i]
	}

	slices.SortFunc(result, func(a, b *v1alpha1.ReplicatedVolumeReplica) int {
		return cmp.Compare(a.ID(), b.ID())
	})

	return result, nil
}

func (r *Reconciler) patchRVR(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	base *v1alpha1.ReplicatedVolumeReplica,
) error {
	patch := client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})
	if err := r.cl.Patch(ctx, rvr, patch); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	return nil
}

func (r *Reconciler) patchRVRStatus(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	base *v1alpha1.ReplicatedVolumeReplica,
) error {
	patch := client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})
	if err := r.cl.Status().Patch(ctx, rvr, patch); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	return nil
}

// --- RV ---

func (r *Reconciler) getRV(ctx context.Context, name string) (*v1alpha1.ReplicatedVolume, error) {
	rv := &v1alpha1.ReplicatedVolume{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: name}, rv); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return rv, nil
}

// --- RSP ---

func (r *Reconciler) getRSP(ctx context.Context, name string) (*v1alpha1.ReplicatedStoragePool, error) {
	rsp := &v1alpha1.ReplicatedStoragePool{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: name}, rsp); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return rsp, nil
}
