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
	"maps"
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
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/syncbudget"
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

	// resyncLimit is the per-node resync-end budget. Zero means the limiter is off — no budget is
	// consulted and no replica ever waits for a resync slot.
	resyncLimit syncbudget.Limit

	// resyncReservations holds the ends of the transfers this process has committed but not yet
	// observed — see resyncReservations. Without it the budget is blind to its own decisions for as
	// long as a placement takes to come back through the watch and the agent takes to report the
	// transfer itself.
	resyncReservations *resyncReservations
}

func NewReconciler(
	cl client.Client,
	log logr.Logger,
	scheme *runtime.Scheme,
	extenderClient schext.Client,
) *Reconciler {
	return &Reconciler{
		cl:                 cl,
		log:                log,
		scheme:             scheme,
		extenderClient:     extenderClient,
		resyncReservations: newResyncReservations(resyncReservationTTL, time.Now),
	}
}

// resyncSlotRequeueInterval is how often a replica waiting for a resync slot re-checks the budget.
//
// Nothing wakes it when a resync elsewhere finishes: fanning out over nodes would enqueue up to
// 500 volumes per node transition, so the periodic re-check is the progress mechanism instead.
const resyncSlotRequeueInterval = 5 * time.Second

// resyncSlotWaitMessage is what a replica waiting for a resync slot reports. It carries no live
// numbers on purpose — those would repatch every waiting replica whenever any resync finished.
const resyncSlotWaitMessage = "Waiting for a resync slot: a node that would send this volume's " +
	"data is already at its resync limit; placement resumes when a resync finishes"

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
			"ReplicatedVolume not found; waiting for it to be present").
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

	rsp, err := r.getRSP(rf.Ctx(), rv.Status.Configuration.ReplicatedStoragePoolName)
	if err != nil {
		return rf.Fail(err).ToCtrl()
	}

	// Guard 3: RSP not found — storage pool doesn't exist yet.
	if rsp == nil {
		return r.reconcileRVRsCondition(rf.Ctx(), rvrs, all,
			metav1.ConditionUnknown,
			v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonWaitingForReplicatedVolume,
			"ReplicatedStoragePool not found; waiting for it to be present").
			Enrichf("setting WaitingForReplicatedVolume: RSP %q not found", rv.Status.Configuration.ReplicatedStoragePoolName).ToCtrl()
	}

	attachToNodes, err := r.getIntendedAttachments(rf.Ctx(), req.Name)
	if err != nil {
		return rf.Fail(err).ToCtrl()
	}

	// Build the scheduling context: eligible nodes, topology, zone layout,
	// reservation parameters, and replica-type sets (All, Access, Diskful,
	// TieBreaker, Scheduled) with OccupiedNodes — all computed in a single
	// pass over rvrs.
	sctx := computeSchedulingContext(rv, rsp, rvrs, attachToNodes)

	// Access replicas (diskless) don't need scheduling — they are created
	// directly on the node where they are needed. Remove the Scheduled
	// condition from them if it was set previously (e.g., the replica
	// type was changed from Diskful/TieBreaker to Access), and exclude
	// them from all subsequent scheduling logic.
	var outcome flow.ReconcileOutcome
	if sctx.Access.Len() > 0 {
		outcome = r.reconcileRVRsConditionAbsent(rf.Ctx(), rvrs, sctx.Access)
		if outcome.ShouldReturn() {
			return outcome.ToCtrl()
		}
	}

	// Mark replicas whose placement is already complete for their type
	// (see isReplicaScheduled) as successfully scheduled.
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

	// Node load for the tie-break below the score. Counted here rather than in
	// computeSchedulingContext because that runs before the guard above, and a
	// reconciliation with nothing left to place has no use for it.
	//
	// Only the eligible nodes are counted: candidates come from that list, so a
	// replica anywhere else can never be compared against.
	//
	// Skipped when every replica left already has a node and is back only for its LVG:
	// such a replica is narrowed to the node it sits on, so no two nodes are compared
	// and the count could not change the outcome.
	//
	// Access is taken out for the same reason the guard above takes it out — this
	// controller does not place those. They arrive with a node already set, so today
	// they are never in Unplaced anyway; the subtraction is here so that the gate does
	// not rest on an invariant owned by whoever creates them.
	if !sctx.Unplaced.Difference(sctx.Access).IsEmpty() {
		nodeLoad, loadOutcome := r.reconcileNodeLoad(rf.Ctx(), sctx.EligibleNodes)
		outcome = outcome.Merge(loadOutcome.Enrichf("counting node load"))
		if outcome.ShouldReturn() {
			return outcome.ToCtrl()
		}
		sctx.NodeLoad = nodeLoad
	}

	// Replicas that still need scheduling. Deleting RVRs are not excluded:
	// the scheduler's job is to place replicas, not to decide whether they
	// are needed. Unnecessary replicas will have their finalizers removed
	// and disappear on their own.
	unscheduled := sctx.All.Difference(sctx.Scheduled)

	// Phase 2: Schedule unscheduled Diskful RVRs (per-RVR pipeline).
	//
	// Each iteration checks ShouldReturn to avoid scheduling more replicas
	// while the informer cache is stale. This reduces (but does not eliminate)
	// the chance of placing two replicas on the same node; the ultimate
	// uniqueness guarantee is enforced by datamesh.
	unscheduledDiskful := unscheduled.Intersect(sctx.Diskful)

	// The resync budget is a property of the volume, not of a replica, so it is decided
	// once per reconciliation and lazily: most volumes never reach this loop at all.
	var targetResyncBudget *resyncBudgetDecision

	for _, rvr := range rvrs {
		if !unscheduledDiskful.Contains(rvr.ID()) {
			continue
		}

		// Only a replica with no node yet has its whole copy arriving over the network. One that
		// already has a node is here for its LVG, and holding that back would leave it partially
		// placed forever.
		unplaced := rvr.Spec.NodeName == ""
		if unplaced {
			if targetResyncBudget == nil {
				target, budgetOutcome := r.reconcileResyncBudget(rf.Ctx(), rv, rvrs)
				outcome = outcome.Merge(budgetOutcome.Enrichf("deciding the resync budget"))
				if outcome.ShouldReturn() {
					return outcome.ToCtrl()
				}
				targetResyncBudget = &target
				sctx.ResyncOccupancy = targetResyncBudget.occupancyAtAdmission
				sctx.ResyncObservedOccupancy = targetResyncBudget.observedOccupancy
				sctx.ResyncSenderNodes = targetResyncBudget.senderNodes
			}

			// The sending side is re-measured per replica: admission asked for room for one end on
			// each sender, while every replica placed below charges those same senders one more.
			sendersFull := len(targetResyncBudget.senderNodes) > 0 && sctx.ResyncOccupancy != nil &&
				!r.resyncLimit.HasRoomOnAll(targetResyncBudget.senderNodes, sctx.ResyncOccupancy)

			if targetResyncBudget.waiting || sendersFull {
				// Unknown, not False: computeActualSchedulingFailureMessages buckets on IsFalse(),
				// and waiting for a slot is not a failure.
				outcome = outcome.Merge(
					r.reconcileRVRCondition(rf.Ctx(), rvr, metav1.ConditionUnknown,
						v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonWaitingForResyncSlot,
						resyncSlotWaitMessage).
						Enrichf("setting WaitingForResyncSlot on rvr %s", rvr.Name),
					rf.ContinueAndRequeueAfter(resyncSlotRequeueInterval),
				)
				if outcome.ShouldReturn() {
					return outcome.ToCtrl()
				}
				continue
			}
		}

		outcome = outcome.Merge(r.reconcileOneRVRScheduling(rf.Ctx(), rvr, sctx).
			Enrichf("scheduling Diskful"))
		if outcome.ShouldReturn() {
			return outcome.ToCtrl()
		}

		// A node on the replica, not the outcome, is what says it was placed: scheduling that found
		// nowhere to land also returns a non-terminal outcome.
		if unplaced && rvr.Spec.NodeName != "" && sctx.ResyncOccupancy != nil {
			for _, node := range targetResyncBudget.senderNodes {
				sctx.ResyncOccupancy[node]++
			}
		}
	}

	// Phase 3: Schedule unscheduled TieBreaker RVRs (per-RVR pipeline, no scoring).
	// Same per-iteration ShouldReturn guard as Phase 2 (see comment above).
	unscheduledTieBreaker := unscheduled.Intersect(sctx.TieBreaker)
	for _, rvr := range rvrs {
		if !unscheduledTieBreaker.Contains(rvr.ID()) {
			continue
		}
		outcome = outcome.Merge(r.reconcileOneRVRScheduling(rf.Ctx(), rvr, sctx).
			Enrichf("scheduling TieBreaker"))
		if outcome.ShouldReturn() {
			return outcome.ToCtrl()
		}
	}

	return outcome.ToCtrl()
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
// Reconcile: node-load
//

// reconcileNodeLoad counts, for each eligible node, the replicas placed on it across all
// volumes. The result is the tie-break SelectBest applies below the capacity score.
//
// Only the eligible nodes are counted: candidates come from that list, so the load of a
// node outside the pool is never compared against anything.
//
// One indexed read per node rather than one read of the whole collection: the count is
// all that is wanted, and a list of every replica in the cluster materialises each of
// them into the list's backing array. Per node only that node's replicas are live at a
// time, which is what keeps this path's peak flat as the cluster grows.
//
// Every replica on a node counts, whatever its type: a TieBreaker and an Access replica
// carry no data but hold a DRBD resource there just as a Diskful one does. Replicas
// being deleted count too — their resource is up until teardown completes.
//
// Reconcile pattern: Target-state driven. This method owns the actual half only: it
// observes the load and returns it; the tie-break in reconcileOneRVRScheduling is what
// acts on it.
func (r *Reconciler) reconcileNodeLoad(
	ctx context.Context,
	nodes []v1alpha1.ReplicatedStoragePoolEligibleNode,
) (_ map[string]int, outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "node-load")
	defer rf.OnEnd(&outcome)

	load := make(map[string]int, len(nodes))
	for i := range nodes {
		node := nodes[i].NodeName
		count, err := r.getReplicaCountOnNode(rf.Ctx(), node)
		if err != nil {
			return nil, rf.Failf(err, "counting replicas on node %s", node)
		}
		// Idle nodes stay out of the map, so its keys are exactly the loaded nodes.
		// A missing key reads as zero where the tie-break looks it up.
		if count > 0 {
			load[node] = count
		}
	}

	return load, rf.Continue()
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: resync-budget
//

// resyncBudgetDecision is what the resync limiter decided about one volume in one reconciliation.
//
// It is a target in the sense of controller-terminology.mdc, not a report: nothing about it is
// published and nothing about it is stored.
type resyncBudgetDecision struct {
	// waiting is true when the volume must not start filling replicas yet.
	waiting bool

	// occupancyAtAdmission is the "node → resync ends" map this volume's admission was decided
	// against — see syncbudget.Pass.SubjectOccupancy. It is nil when the decision did not need it:
	// no claim, limiter off, or the volume is waiting.
	occupancyAtAdmission map[string]int

	// senderNodes are the nodes this volume reads its data from, sorted. They are the nodes the
	// admitted claim charged, so every replica placed against this decision loads exactly them.
	// Empty in the same cases occupancyAtAdmission is nil.
	senderNodes []string

	// observedOccupancy is the "node → resync ends" map as the cluster reported it, before this
	// process's own pending reservations were merged in and before the queue charged anything.
	//
	// It is what resyncReservations.reserve has to be measured against: occupancyAtAdmission
	// already carries the reservations that existed when the decision was taken, and re-adding
	// them at reservation time would count them twice. Nil whenever occupancyAtAdmission is.
	observedOccupancy map[string]int
}

// reconcileResyncBudget decides whether rv may start filling replicas over the network now, and
// against which occupancy the receiving node must then be chosen.
//
// The whole ordered queue is walked, not just this volume's own sender nodes: a volume ahead of
// this one may want the same nodes, and only the walk knows what it holds there. Walking it whole
// is not the same as reading the cluster whole, though: both inputs are selected by index, and the
// selections are complete for what they feed — see syncbudget.MayClaim and syncbudget.OccupiesEnds.
// Reading every volume and every replica instead costs, on a cluster of thousands, more memory per
// worker than the controller is allowed in total.
//
// The two selections are separate on purpose: a volume still forming its datamesh is left out of
// the queue while its replicas keep occupying the nodes they sit on.
//
// Every read here skips DeepCopy and aliases the informer cache; the objects go to syncbudget,
// which never mutates them, and MUST NOT be handed to anything that patches. Nor are they held:
// each candidate's replicas become a claim and are let go before the next candidate is read, so
// what the walk costs is the queue, not the volumes behind it.
//
// Reconcile pattern: Target-state driven. This method owns the target half only; the root
// Reconcile turns a "wait" into the WaitingForResyncSlot condition on the individual replicas.
func (r *Reconciler) reconcileResyncBudget(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
) (_ resyncBudgetDecision, outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "resync-budget")
	defer rf.OnEnd(&outcome)

	if r.resyncLimit <= 0 {
		return resyncBudgetDecision{}, rf.Continue()
	}

	// No own ends: this call decides only whether rv claims at all and what it needs, both of
	// which are read below. The claim admission is judged on is the queue's, built with the ends
	// the occupancy map itself reports.
	actualClaim, claiming := syncbudget.ComputeActualClaim(rv, rvrs, nil)
	if !claiming {
		return resyncBudgetDecision{}, rf.Continue()
	}

	occupyingRVRs, err := r.getRVRsOccupyingResyncEnds(rf.Ctx())
	if err != nil {
		return resyncBudgetDecision{}, rf.Failf(err, "listing ReplicatedVolumeReplicas occupying resync ends")
	}

	candidateRVs, err := r.getRVsClaimingResyncSlots(rf.Ctx())
	if err != nil {
		return resyncBudgetDecision{}, rf.Failf(err, "listing ReplicatedVolumes claiming resync slots")
	}

	ownEndsByRV := syncbudget.ComputeActualOccupancyByRV(occupyingRVRs)

	// The subject's claim is the one computed above, from the object this reconciliation holds,
	// with the own ends the occupancy reading reports for it. Never the listing's copy of the
	// volume: it answers from an index that may not have caught up, and a subject missing from
	// the queue reads as admitted.
	actualClaim.OwnEnds = ownEndsByRV[rv.Name]

	// The candidates come pre-filtered by the index, but their sender nodes cannot: whether a
	// member holds usable data is recorded on the replica, while the member entry the volume
	// carries says only where it is. So each candidate's replicas are read one volume at a time,
	// which is a read per volume short of a copy — not per volume in the cluster.
	//
	// Each read is turned into a claim before the next one is made, and the replicas are let go:
	// the walk needs the claims, and holding every candidate's replicas until the whole queue is
	// built is what used to put the cluster back in memory a piece at a time.
	//
	// Senders are all these replicas are read for; what each claimant already occupies comes from
	// the occupancy listing instead, so that the two cannot disagree.
	queue := make([]syncbudget.Claim, 0, len(candidateRVs)+1)
	queue = append(queue, actualClaim)

	for _, candidate := range candidateRVs {
		if candidate.Name == rv.Name {
			// Already in the queue, from the object in hand. Volumes are cluster-scoped, so at
			// most one candidate can match — and a second copy of the subject's claim would
			// charge its own sender nodes twice and block it behind itself.
			continue
		}

		candidateRVRs, err := r.getUnsafeRVRsByRVName(rf.Ctx(), candidate.Name)
		if err != nil {
			return resyncBudgetDecision{}, rf.Failf(err,
				"listing ReplicatedVolumeReplicas of rv %s", candidate.Name)
		}

		claim, ok := syncbudget.ComputeActualClaim(candidate, candidateRVRs, ownEndsByRV[candidate.Name])
		if !ok {
			// MayClaim accepts a superset of the queue: this one turned out not to be short of
			// anything it can read, now that its replicas have been seen.
			continue
		}
		queue = append(queue, claim)
	}

	syncbudget.SortQueue(queue)

	// Every reservation gives up the half the listing now accounts for by itself: the receiving end
	// as soon as the placement is visible, the sending end only once the transfer is reported, and
	// both as soon as the replica stops owing data at all. Kept on past that, a reservation would
	// charge its node a second time — or, for a replica gone from the listing for good, hold a node
	// against nothing until the TTL ran out.
	r.resyncReservations.observe(occupyingRVRs)

	// The ends this process has already handed out but not yet seen are added to what the cluster
	// reported, so that a node with a placement still in flight reads as occupied to the walk and
	// to the receiving-node filter fed from it.
	observedOccupancy := syncbudget.ComputeActualOccupancy(occupyingRVRs)

	reportPass := syncbudget.RunPassForSubject(
		queue,
		r.resyncReservations.mergedInto(observedOccupancy),
		r.resyncLimit,
		rv.Name,
	)
	if reportPass.IsWaiting(rv.Name) {
		rf.Log().V(2).Info("waiting for a resync slot: a needed node is full or held by a volume ahead in the queue",
			"claimClass", actualClaim.Class.String(), "neededEnds", actualClaim.NeededEnds,
			"waiters", reportPass.Waiters)
		return resyncBudgetDecision{waiting: true}, rf.Continue()
	}

	// SubjectOccupancy, not the raw occupancy the pass started from: the receiving-node filter must
	// see the holds this pass created, or it would pick a node a waiting volume is holding.
	//
	// The sender nodes come from the claim rather than from a second ComputeActualSenderNodes call,
	// so that what is charged per placement is exactly what the pass charged for this volume.
	return resyncBudgetDecision{
		occupancyAtAdmission: reportPass.SubjectOccupancy,
		senderNodes:          slices.Sorted(maps.Keys(actualClaim.NeededEnds)),
		observedOccupancy:    observedOccupancy,
	}, rf.Continue()
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

	// Whether this replica is arriving without a node at all, as opposed to being
	// back only for its LVG. Read before anything places it, since placement fills
	// the field in.
	unplaced := rvr.Spec.NodeName == ""

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
	if unplaced {
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
	//
	// The scheduler uses greedy round-robin (fewest replicas → preferred zone)
	// and does NOT enforce the max-D-per-zone constraint (D − qmr) or the
	// required zone count directly. It relies on RSC-level CEL validation to
	// restrict TransZonal to valid zone counts for each FTT/GMDR combination
	// (e.g., FTT=2,GMDR=2 allows only 3 or 5 zones). Given valid zone counts,
	// the round-robin naturally satisfies the max-D-per-zone constraint.
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

	// Exclude nodes with no free resync end. The sending side of the budget
	// is settled before this pipeline runs; the receiving node is chosen here.
	//
	// The gate repeats the one in Reconcile: a tie-breaker receives no data, and a partially placed
	// replica would be judged against its own owed end on the only node left to it.
	//
	// The stage sits last among the filters, so everything it saw had already survived the others:
	// when it admitted none of them, the budget alone is what left the replica nowhere to land.
	resyncSeen, resyncAdmitted := 0, 0
	if isDiskful && unplaced && sctx.ResyncOccupancy != nil {
		resyncOccupancy := sctx.ResyncOccupancy
		resyncLimit := r.resyncLimit
		p = WithPredicate(p, "no free resync end", logger, func(e *CandidateEntry) *CandidateEntry {
			resyncSeen++
			if !resyncLimit.HasRoom(resyncOccupancy[e.Node.NodeName]) {
				return nil
			}
			resyncAdmitted++
			return e
		})
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
	// The required Diskful count (D = FTT + GMDR + 1) comes from v1alpha1.IntendedDiskfulCount.
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
		requiredDiskful := v1alpha1.IntendedDiskfulCount(sctx.FailuresToTolerate, sctx.GuaranteedMinimumDataRedundancy)
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

	// Ordering: score first, then how loaded the node already is, then names.
	//
	// The load only ever decides a tie, so capacity still outranks it for Diskful and
	// the attach-to bonus still outranks both. For a TieBreaker it decides everything:
	// nothing above scores a diskless candidate, so without it every tie-breaker in the
	// cluster lands on whichever eligible node sorts first by name.
	//
	// The name comparison stays as the last key. It is what makes the choice repeatable
	// across reconciliations of the same unchanged state.
	best, summary := SelectBest(p, func(a, b *CandidateEntry) bool {
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if la, lb := sctx.NodeLoad[a.Node.NodeName], sctx.NodeLoad[b.Node.NodeName]; la != lb {
			return la < lb
		}
		if a.Node.NodeName != b.Node.NodeName {
			return a.Node.NodeName < b.Node.NodeName
		}
		return a.LVGName() < b.LVGName()
	})

	if best == nil {
		// Nowhere to land only because the receiving side of the budget is full: a wait, not a
		// failure, reported the way the sending-side gate reports one.
		if resyncSeen > 0 && resyncAdmitted == 0 {
			logger.V(2).Info("no candidate left after the resync budget filter; waiting for a resync slot",
				"summary", summary)
			return r.reconcileRVRCondition(rf.Ctx(), rvr, metav1.ConditionUnknown,
				v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonWaitingForResyncSlot,
				resyncSlotWaitMessage).
				Merge(rf.ContinueAndRequeueAfter(resyncSlotRequeueInterval))
		}

		return r.reconcileRVRCondition(rf.Ctx(), rvr, metav1.ConditionFalse,
			v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonSchedulingFailed, summary)
	}

	// Both ends of the transfer this placement is about to start, claimed together before anything
	// is written or narrowed. Neither gate above can settle them: the sending side was admitted
	// against an occupancy snapshot taken when this reconciliation began, and the receiving node was
	// picked from the same snapshot, so every node another worker has committed a transfer on since
	// is missing from both. This is the point where that is settled, once, for the nodes actually
	// used — and the only point at which the sending and the receiving node are judged together.
	//
	// Losing the race is a wait, not a failure, and it is reported as the filter reports one: the
	// re-check comes round again on the requeue, by which time the winner's placement is visible.
	reservedReceivingEnd := false
	if isDiskful && unplaced && sctx.ResyncOccupancy != nil {
		if !r.resyncReservations.reserve(
			rvr.Name, best.Node.NodeName, sctx.ResyncSenderNodes, r.resyncLimit, sctx.ResyncObservedOccupancy,
		) {
			logger.V(2).Info("a node of this transfer lost its last resync end while the replica was being scheduled",
				"receivingNode", best.Node.NodeName, "senderNodes", sctx.ResyncSenderNodes)
			return r.reconcileRVRCondition(rf.Ctx(), rvr, metav1.ConditionUnknown,
				v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonWaitingForResyncSlot,
				resyncSlotWaitMessage).
				Merge(rf.ContinueAndRequeueAfter(resyncSlotRequeueInterval))
		}
		reservedReceivingEnd = true
	}

	if isDiskful {
		if err := r.extenderClient.NarrowReservation(rf.Ctx(), reservationID, 60*time.Second, schext.LVMVolumeGroup{
			LVGName:      best.LVGName(),
			ThinPoolName: best.ThinPoolName(),
		}); err != nil {
			if reservedReceivingEnd {
				r.resyncReservations.release(rvr.Name)
			}
			return r.reconcileRVRCondition(rf.Ctx(), rvr, metav1.ConditionFalse,
				v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonExtenderUnavailable,
				"Scheduling is temporarily unavailable: failed to confirm capacity reservation").
				Merge(rf.Fail(err))
		}
	}

	outcome = r.reconcileRVRPlacement(rf.Ctx(), rvr, best)
	if outcome.ShouldReturn() {
		// A node on the replica is what says it was placed, here as in Reconcile. Without one
		// nothing took the end, and holding it would idle a node until the TTL ran out.
		if reservedReceivingEnd && rvr.Spec.NodeName == "" {
			r.resyncReservations.release(rvr.Name)
		}
		return outcome
	}

	// The only record of why this node was picked. The Scheduled condition says that
	// the replica was placed, never against what; the pipeline summary is built for
	// the failure paths and is discarded here.
	//
	// The replica type is worth a field of its own: a TieBreaker is scored by nobody,
	// so "score 0" on it is the normal case rather than the anomaly it looks like.
	placed := []any{
		"type", rvr.Spec.Type,
		"node", best.Node.NodeName,
		"lvg", best.LVGName(),
		"score", best.Score,
	}
	// Left out rather than logged as zero when the count was skipped: on that path the
	// node was never compared against another, and a zero would read as an idle node.
	if sctx.NodeLoad != nil {
		placed = append(placed, "nodeLoad", sctx.NodeLoad[best.Node.NodeName])
	}
	logger.V(2).Info("replica placed", placed...)

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

	// EligibleNodes are the candidate nodes from the RSP (a clone owned by this
	// context, down to each node's LVMVolumeGroups), sorted by NodeName.
	// OccupiedNodes tracks nodes already assigned to an RVR in this Reconcile.
	// AttachToNodes lists nodes where the volume must be attached (from RVA
	// objects, via getIntendedAttachments; a clone owned by this context).
	EligibleNodes []v1alpha1.ReplicatedStoragePoolEligibleNode
	OccupiedNodes map[string]struct{}
	AttachToNodes []string

	// ResyncOccupancy is the "node → resync ends" map the resync budget was decided against — see
	// resyncBudgetDecision.occupancyAtAdmission — or nil when the budget was not consulted.
	// Reconcile fills it in once the decision is taken, since it is derived from the whole cluster
	// while this context is built from one volume, and charges the volume's sender nodes in it as
	// it places replicas, so that later replicas of the same volume see the load the earlier ones
	// created. The map is owned by this context.
	ResyncOccupancy map[string]int

	// ResyncObservedOccupancy is the same map as the cluster reported it — see
	// resyncBudgetDecision.observedOccupancy. Only the reservation of a transfer's ends reads it,
	// and it is nil exactly when ResyncOccupancy is. Not charged as replicas are placed: a placement
	// becomes a reservation instead, which is what the reservation is measured against.
	ResyncObservedOccupancy map[string]int

	// ResyncSenderNodes are the nodes a replica of this volume reads its data from — see
	// resyncBudgetDecision.senderNodes. Every placement loads them alongside the node it lands on,
	// so the reservation has to take them together. Empty exactly when ResyncOccupancy is nil.
	ResyncSenderNodes []string

	// NodeLoad maps an eligible node to the replicas already placed on it, counted
	// across all volumes. It breaks ties between candidates of equal score, so that
	// the least loaded node wins instead of the alphabetically first one.
	//
	// A snapshot: Update does not charge placements to it — see Update. Nil unless
	// some replica of the volume has no node at all, and nil for every reconciliation
	// that returns at the "all scheduled" guard.
	NodeLoad map[string]int

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

	// FTT/GMDR and volume access from the resolved configuration.
	FailuresToTolerate              byte
	GuaranteedMinimumDataRedundancy byte
	VolumeAccess                    v1alpha1.ReplicatedStorageClassVolumeAccess

	// Replica type sets (computed in one pass over rvrs).
	//
	// Scheduled holds the replicas whose placement is already complete for their
	// type (see isReplicaScheduled); Access replicas are never in it.
	//
	// Unplaced holds the replicas with no node at all, as opposed to the ones back
	// only for their LVG; it gates the node-load count, since a replica that already
	// has a node is narrowed to it and no two nodes are ever compared for it. It is
	// the state the reconciliation started from — Update does not remove a replica it
	// places, the way it adds one to Scheduled.
	All        idset.IDSet
	Access     idset.IDSet
	Diskful    idset.IDSet
	TieBreaker idset.IDSet
	Scheduled  idset.IDSet
	Unplaced   idset.IDSet
}

// computeSchedulingContext builds a schedulingContext from RV, RSP, RVRs, and
// the desired attach-to node list (from RVA objects).
// All replica-type sets (All, Access, Diskful, TieBreaker, Scheduled)
// and OccupiedNodes are computed in a single pass over rvrs; membership in
// Scheduled is decided per replica type by isReplicaScheduled.
// The returned context owns every slice it exposes: EligibleNodes is a clone of the RSP list
// (each node's LVMVolumeGroups cloned as well), sorted by NodeName, and AttachToNodes is a clone
// of the caller's list — consumers may mutate them without reaching back into the read-only
// inputs.
func computeSchedulingContext(
	rv *v1alpha1.ReplicatedVolume,
	rsp *v1alpha1.ReplicatedStoragePool,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	attachToNodes []string,
) *schedulingContext {
	// Safety sort: eligible nodes should arrive sorted from the API, but ensure it. The RSP is a
	// read-only input, so sort a clone — sorting the slice in place would reorder the caller's
	// (cached) object through the alias. Cloning the outer slice is not enough: every node carries
	// its own LVMVolumeGroups slice, which would still share its backing array with the RSP, so
	// clone those per node as well (their elements are plain values — no deeper level to copy).
	eligibleNodes := slices.Clone(rsp.Status.EligibleNodes)
	for i := range eligibleNodes {
		eligibleNodes[i].LVMVolumeGroups = slices.Clone(eligibleNodes[i].LVMVolumeGroups)
	}
	slices.SortFunc(eligibleNodes, func(a, b v1alpha1.ReplicatedStoragePoolEligibleNode) int {
		return cmp.Compare(a.NodeName, b.NodeName)
	})

	sctx := &schedulingContext{
		EligibleNodes:                   eligibleNodes,
		Topology:                        rv.Status.Configuration.Topology,
		FailuresToTolerate:              rv.Status.Configuration.FailuresToTolerate,
		GuaranteedMinimumDataRedundancy: rv.Status.Configuration.GuaranteedMinimumDataRedundancy,
		VolumeAccess:                    rv.Status.Configuration.VolumeAccess,
		AttachToNodes:                   slices.Clone(attachToNodes),
		ReservationID:                   rv.GetAnnotations()[v1alpha1.SchedulingReservationIDAnnotationKey],
		Size:                            rv.Spec.Size.Value(),
		OccupiedNodes:                   make(map[string]struct{}, len(rvrs)),
	}

	poolType := rsp.Spec.Type

	// Single pass: classify each RVR by type and scheduling state.
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

		if rvr.Spec.NodeName != "" {
			sctx.OccupiedNodes[rvr.Spec.NodeName] = struct{}{}

			if isReplicaScheduled(rvr, poolType) {
				sctx.Scheduled.Add(id)
			}
		} else {
			sctx.Unplaced.Add(id)
		}
	}

	sctx.ReplicasByZone = computeReplicasByZone(eligibleNodes, rvrs)

	return sctx
}

// isReplicaScheduled reports whether an RVR that already has a node assigned carries everything
// its type needs to be considered placed — the criterion is type-aware because the placement
// pipelines are:
//
//   - TieBreaker: diskless, placed by the node-only pipeline
//     (TakeOnlyNodesFromEligibleNodes) and never assigned an LVG — the node IS its whole
//     placement. Requiring an LVG here would keep every tie-breaker (in particular one retyped
//     from Diskful by the r3→r2 migration) permanently unscheduled and re-run the pipeline for
//     it on every reconcile, even though spec.nodeName is immutable and there is nothing left
//     to decide.
//   - Access: not scheduled by this controller at all — Access replicas are created directly on
//     the node where they are needed and their Scheduled condition is removed.
//   - Diskful / ShadowDiskful: backed by a volume, so the storage assignment must match the pool
//     type (thin pools require a thin-pool name, thick pools require its absence).
func isReplicaScheduled(rvr *v1alpha1.ReplicatedVolumeReplica, poolType v1alpha1.ReplicatedStoragePoolType) bool {
	switch rvr.Spec.Type {
	case v1alpha1.ReplicaTypeTieBreaker:
		return true
	case v1alpha1.ReplicaTypeAccess:
		return false
	default:
		if rvr.Spec.LVMVolumeGroupName == "" {
			return false
		}
		switch poolType {
		case v1alpha1.ReplicatedStoragePoolTypeLVMThin:
			return rvr.Spec.LVMVolumeGroupThinPoolName != ""
		case v1alpha1.ReplicatedStoragePoolTypeLVM:
			return rvr.Spec.LVMVolumeGroupThinPoolName == ""
		default:
			return false
		}
	}
}

// Update records a successful placement: marks the node as occupied and adds the RVR
// to its zone in ReplicasByZone.
//
// NodeLoad is deliberately left alone. Charging the node here could not change a later
// decision: this same call puts it in OccupiedNodes, and the "node occupied" predicate
// then keeps it out of the candidates of every replica of this volume that still needs
// one. What keeps the replicas of one volume apart is that predicate, not the count.
//
// It would not be free either: the placement log reads NodeLoad for the chosen node,
// and were the increment to happen before it, the line would report a load that already
// includes the replica being reported.
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
		StatusEqual(status).ReasonEqual(reason).MessageEqual(message).ObservedGenerationCurrent().Eval() {
		return rf.Continue()
	}

	base := rvr.DeepCopy()

	obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondScheduledType,
		Status:  status,
		Reason:  reason,
		Message: message,
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

// getRVRsOccupyingResyncEnds lists the ReplicatedVolumeReplicas that carry resync ends on their
// node, which is every replica the occupancy map is built from and no other.
//
// The index behind it selects by syncbudget.OccupiesEnds, the same predicate the map's own sweep
// skips replicas by, so the narrowed result yields the same map as every replica in the cluster
// would — bounded by how much resync is in flight rather than by how many replicas exist.
//
// It reads only a few fields, so the copies the cache would otherwise make are pure
// overhead — hence UnsafeDisableDeepCopy. The returned replicas MUST be treated as
// read-only: they alias the informer cache.
//
// No ordering is imposed; the consumer does not depend on the order it walks them in.
func (r *Reconciler) getRVRsOccupyingResyncEnds(ctx context.Context) ([]*v1alpha1.ReplicatedVolumeReplica, error) {
	list := &v1alpha1.ReplicatedVolumeReplicaList{}
	if err := r.cl.List(ctx, list, client.UnsafeDisableDeepCopy, client.MatchingFields{
		indexes.IndexFieldRVRByResyncEndsOccupancy: indexes.IndexValueResyncEndsOccupant,
	}); err != nil {
		return nil, err
	}

	// Same []T-to-[]*T copy loop as getRVRsByRVName below; see the HACK note there.
	result := make([]*v1alpha1.ReplicatedVolumeReplica, len(list.Items))
	for i := range list.Items {
		result[i] = &list.Items[i]
	}
	return result, nil
}

// getReplicaCountOnNode reports how many ReplicatedVolumeReplicas sit on one node.
//
// Only the count leaves this function, so nothing keeps the listed replicas alive past
// the call and the caller can walk the nodes one at a time.
func (r *Reconciler) getReplicaCountOnNode(ctx context.Context, nodeName string) (int, error) {
	list := &v1alpha1.ReplicatedVolumeReplicaList{}
	if err := r.cl.List(ctx, list,
		client.MatchingFields{indexes.IndexFieldRVRByNodeName: nodeName},
		client.UnsafeDisableDeepCopy,
	); err != nil {
		return 0, err
	}
	return len(list.Items), nil
}

// getUnsafeRVRsByRVName lists the replicas of one volume, aliasing the informer cache instead of
// copying it (UnsafeDisableDeepCopy). The returned replicas MUST be treated as read-only.
//
// It exists beside getRVRsByRVName because the resync budget reads one volume after another: the
// copies the cache would make are the whole cost of that walk, and nothing in the budget patches
// what it reads. Callers that go on to patch a replica MUST use getRVRsByRVName instead.
//
// No ordering is imposed; the budget derives its sender nodes from the volume's datamesh, which
// carries its own order.
func (r *Reconciler) getUnsafeRVRsByRVName(ctx context.Context, rvName string) ([]*v1alpha1.ReplicatedVolumeReplica, error) {
	list := &v1alpha1.ReplicatedVolumeReplicaList{}
	if err := r.cl.List(ctx, list, client.UnsafeDisableDeepCopy, client.MatchingFields{
		indexes.IndexFieldRVRByReplicatedVolumeName: rvName,
	}); err != nil {
		return nil, err
	}

	// Same []T-to-[]*T copy loop as getRVRsByRVName below; see the HACK note there.
	result := make([]*v1alpha1.ReplicatedVolumeReplica, len(list.Items))
	for i := range list.Items {
		result[i] = &list.Items[i]
	}
	return result, nil
}

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
		// Non-deleting replicas first, deleting last.
		aDeleting := a.DeletionTimestamp != nil
		bDeleting := b.DeletionTimestamp != nil
		if aDeleting != bDeleting {
			if aDeleting {
				return 1
			}
			return -1
		}
		// Within the same group: oldest first (by CreationTimestamp),
		// tie-break by name for determinism.
		if !a.CreationTimestamp.Equal(&b.CreationTimestamp) {
			if a.CreationTimestamp.Before(&b.CreationTimestamp) {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.Name, b.Name)
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

// getRVsClaimingResyncSlots lists the ReplicatedVolumes that could claim a resync slot, through
// the informer cache (UnsafeDisableDeepCopy), so that the resync queue can be built.
//
// The index behind it selects by syncbudget.MayClaim, which judges a volume without its replicas
// and so accepts a superset of the queue: every volume that ends up claiming is here, and a few
// that do not are filtered out when their replicas are read. Erring that way is deliberate — see
// MayClaim.
//
// The returned volumes MUST be treated as read-only: they alias the informer cache. No ordering is
// imposed; the claims built from them are ordered by syncbudget.SortQueue.
func (r *Reconciler) getRVsClaimingResyncSlots(ctx context.Context) ([]*v1alpha1.ReplicatedVolume, error) {
	list := &v1alpha1.ReplicatedVolumeList{}
	if err := r.cl.List(ctx, list, client.UnsafeDisableDeepCopy, client.MatchingFields{
		indexes.IndexFieldRVByResyncClaimCandidacy: indexes.IndexValueResyncClaimCandidate,
	}); err != nil {
		return nil, err
	}

	result := make([]*v1alpha1.ReplicatedVolume, len(list.Items))
	for i := range list.Items {
		result[i] = &list.Items[i]
	}
	return result, nil
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

// --- RVA ---

// getIntendedAttachments lists ReplicatedVolumeAttachment objects for the given
// RV name using the informer cache (UnsafeDisableDeepCopy) and returns a sorted
// slice of unique node names. No map allocations are made.
func (r *Reconciler) getIntendedAttachments(ctx context.Context, rvName string) ([]string, error) {
	var list v1alpha1.ReplicatedVolumeAttachmentList
	if err := r.cl.List(ctx, &list,
		client.MatchingFields{indexes.IndexFieldRVAByReplicatedVolumeName: rvName},
		client.UnsafeDisableDeepCopy,
	); err != nil {
		return nil, err
	}

	if len(list.Items) == 0 {
		return nil, nil
	}

	nodes := make([]string, len(list.Items))
	for i := range list.Items {
		nodes[i] = list.Items[i].Spec.NodeName
	}
	slices.Sort(nodes)
	return slices.Compact(nodes), nil
}
