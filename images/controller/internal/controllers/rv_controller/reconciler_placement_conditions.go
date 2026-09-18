/*
Copyright 2026 Flant JSC

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

package rvcontroller

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: placement conditions
//

// reconcilePlacementConditions is the SINGLE writer of the Scheduled and SatisfyEligibleNodes
// conditions and of status.eligibleNodesViolations. Both conditions answer placement questions —
// "did every replica get a node?" and "is every replica on a node it is allowed to be on?" — and
// share the same inputs, so they are computed together.
//
// Placement is orthogonal to availability: a volume with a misplaced replica keeps serving I/O,
// so neither condition enters Ready. They are ticket-worthy, not page-worthy.
//
// It only reports; the repair itself is driven elsewhere (the scheduler assigns nodes, the
// datamesh transition engine moves members).
//
// Reconcile pattern: In-place reconciliation
func (r *Reconciler) reconcilePlacementConditions(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	rsp *rspView,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "placement-conditions")
	defer rf.OnEnd(&outcome)

	scheduled := computeScheduledReport(rv, rvrs)
	satisfyEligibleNodes := computeSatisfyEligibleNodesReport(rv, rvrs, rsp)

	changed := applyScheduledCond(rv, scheduled)
	changed = applySatisfyEligibleNodesCond(rv, satisfyEligibleNodes) || changed
	changed = applyEligibleNodesViolations(rv, satisfyEligibleNodes) || changed

	return rf.Continue().ReportChangedIf(changed)
}

// ──────────────────────────────────────────────────────────────────────────────
// Reports
//

// scheduledReport is the computed Scheduled condition report: whether every replica that needs a
// placement has one. present=false means the condition must not be touched this pass (deletion
// freezes it; a volume without a configuration has never had it evaluated).
type scheduledReport struct {
	present bool
	status  metav1.ConditionStatus
	reason  string
	message string
}

// satisfyEligibleNodesReport is the computed SatisfyEligibleNodes condition report together with
// the per-replica violations that back it. Both land in the status patch domain and are always
// written as a pair: the condition is the verdict, violations is the inventory behind it.
// present=false means neither must be touched this pass.
type satisfyEligibleNodesReport struct {
	present    bool
	status     metav1.ConditionStatus
	reason     string
	message    string
	violations []v1alpha1.ReplicatedVolumeEligibleNodesViolation
}

// placementOffender pairs a published violation with the explanation behind it. The explanation
// is not published — status.eligibleNodesViolations carries a machine-readable reason, and the
// prose belongs in the condition message — but the volume-level message names one offender, and
// that one needs its own words rather than a generic rendering of the reason.
type placementOffender struct {
	violation v1alpha1.ReplicatedVolumeEligibleNodesViolation
	detail    string
}

// schedulingVerdict is the per-replica scheduling verdict derived from the replica's Scheduled
// condition. Ordered by severity: a definite failure outranks a missing verdict, so the aggregate
// reports the actionable cause when a volume holds both.
type schedulingVerdict uint8

const (
	// schedulingVerdictScheduled means the replica has a node and, where applicable, storage.
	schedulingVerdictScheduled schedulingVerdict = iota
	// schedulingVerdictPending means the scheduler has not given a verdict yet, or has
	// deliberately deferred the placement.
	schedulingVerdictPending
	// schedulingVerdictFailed means the scheduler tried and could not place the replica.
	schedulingVerdictFailed
)

// ──────────────────────────────────────────────────────────────────────────────
// Compute: Scheduled
//

// computeScheduledReport computes the Scheduled condition by aggregating the replica-level
// Scheduled conditions. Ordered, first match wins:
//
//  1. DeletionTimestamp set  → present=false (frozen at the last value)
//  2. Configuration nil      → present=false (absent = not yet evaluated)
//  3. No replicas to place   → False / SchedulingInProgress
//  4. Any replica failed     → False / ReplicasNotScheduled
//  5. Any replica pending,
//     or formation active    → False / SchedulingInProgress
//  6. otherwise              → True / AllReplicasScheduled
//
// The aggregation set is every non-deleting replica whose type is not Access, NOT the datamesh
// members: a replica is scheduled long before it joins the mesh, and the two cases where that
// matters are exactly the ones an operator needs to see. During formation there are no members
// yet, and a tie-breaker created by layout convergence (the P2 heal) is unschedulable precisely
// while it is still outside the mesh. Access replicas are excluded because they are not placed by
// the scheduler at all: they are created on the node that already runs the consumer.
//
// Rule 4 outranks formation on purpose. A volume whose formation cannot finish because the
// scheduler found no candidate must say so, not hide behind "in progress" forever.
//
// Rule 5 reports False rather than Unknown. Unknown means "evaluated but undeterminable"; a
// replica waiting for its first scheduling pass is neither — the answer is known and it is "not
// yet". This matches Configured=False/ConfigurationInProgress for the same shape of state.
//
// It is a pure read-only computation: it MUST NOT mutate rv or rvrs.
func computeScheduledReport(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
) scheduledReport {
	// 1. Deletion: preserve the last known value.
	if rv.DeletionTimestamp != nil {
		return scheduledReport{present: false}
	}

	// 2. No configuration yet: nothing has been evaluated.
	if rv.Status.Configuration == nil {
		return scheduledReport{present: false}
	}

	// Ties on the worst verdict are broken by replica name, so the offender named in the message
	// does not depend on the order the caller happens to hand the replicas in. Relying on that
	// order would make the message churn on an unrelated event: getRVRsSorted sorts by node name
	// first, so rescheduling one replica reorders the slice and could move the name onto a
	// different replica that has not changed at all. It also mirrors the violations of
	// computeSatisfyEligibleNodesReport, which are sorted by replica name for the same reason.
	total := 0
	scheduled := 0
	worst := schedulingVerdictScheduled
	worstName := ""
	worstDetail := ""

	for _, rvr := range rvrs {
		if rvr.DeletionTimestamp != nil || rvr.Spec.Type == v1alpha1.ReplicaTypeAccess {
			continue
		}
		total++

		verdict, detail := computeSchedulingVerdict(rvr)
		if verdict == schedulingVerdictScheduled {
			scheduled++
			continue
		}
		// A replica whose verdict is Scheduled never reaches here, so the name comparison is
		// never made against the empty initial worstName.
		if verdict > worst || (verdict == worst && rvr.Name < worstName) {
			worst = verdict
			worstName = rvr.Name
			worstDetail = detail
		}
	}

	// 3. Nothing to place yet: the replicas themselves have not been created.
	if total == 0 {
		return scheduledReport{
			present: true,
			status:  metav1.ConditionFalse,
			reason:  v1alpha1.ReplicatedVolumeCondScheduledReasonSchedulingInProgress,
			message: "No replicas to schedule yet",
		}
	}

	// 4. A definite scheduling failure.
	if worst == schedulingVerdictFailed {
		return scheduledReport{
			present: true,
			status:  metav1.ConditionFalse,
			reason:  v1alpha1.ReplicatedVolumeCondScheduledReasonReplicasNotScheduled,
			message: fmt.Sprintf("%d/%d replicas scheduled: %s (%s)",
				scheduled, total, worstName, worstDetail),
		}
	}

	// 5. Still in flight: an unplaced replica, or a formation that has not reached placement yet.
	if forming, _ := isFormationInProgress(rv); worst == schedulingVerdictPending || forming {
		// The count alone would contradict the status while formation is what holds the
		// condition down: every replica that exists is placed, so the message reads N/N and
		// has to say what is still being waited for. A named replica takes precedence — it
		// is the more specific answer, and a formation can be waiting on one.
		message := fmt.Sprintf("%d/%d replicas scheduled", scheduled, total)
		switch {
		case worstName != "":
			message += fmt.Sprintf(": waiting for %s (%s)", worstName, worstDetail)
		case forming:
			message += ": waiting for formation, which may still add replicas"
		}
		return scheduledReport{
			present: true,
			status:  metav1.ConditionFalse,
			reason:  v1alpha1.ReplicatedVolumeCondScheduledReasonSchedulingInProgress,
			message: message,
		}
	}

	// 6. Every replica is placed.
	return scheduledReport{
		present: true,
		status:  metav1.ConditionTrue,
		reason:  v1alpha1.ReplicatedVolumeCondScheduledReasonAllReplicasScheduled,
		message: fmt.Sprintf("All %d replicas are scheduled", total),
	}
}

// computeSchedulingVerdict maps a replica's Scheduled condition to a per-replica verdict and a
// human-readable detail.
//
// A verdict whose ObservedGeneration lags the replica generation is not a verdict: the scheduler
// patches spec.nodeName (and the storage fields) before it writes the condition, so between those
// two writes the stored condition still describes the previous placement. Treating it as pending
// keeps the aggregate from reporting a stale success — and from reporting a stale failure for a
// replica that has since been placed.
//
// Only SchedulingFailed and ExtenderUnavailable count as failures. WaitingForResyncSlot is a
// deliberate deferral (a node that would send this replica its data is at its resync limit) and
// WaitingForReplicatedVolume is an ordinary prerequisite — neither is something an operator can
// act on, so both stay pending, as does any reason added later.
func computeSchedulingVerdict(rvr *v1alpha1.ReplicatedVolumeReplica) (schedulingVerdict, string) {
	cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondScheduledType)
	if cond == nil {
		return schedulingVerdictPending, "replica has no Scheduled condition yet"
	}
	if cond.ObservedGeneration != rvr.Generation {
		return schedulingVerdictPending, "scheduling verdict is not current"
	}
	if cond.Status == metav1.ConditionTrue {
		return schedulingVerdictScheduled, ""
	}

	detail := cond.Message
	if detail == "" {
		detail = cond.Reason
	}

	switch cond.Reason {
	case v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonSchedulingFailed,
		v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonExtenderUnavailable:
		return schedulingVerdictFailed, detail
	default:
		return schedulingVerdictPending, detail
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Compute: SatisfyEligibleNodes
//

// computeSatisfyEligibleNodesReport computes the SatisfyEligibleNodes condition and the
// status.eligibleNodesViolations list that backs it. Ordered, first match wins:
//
//  1. DeletionTimestamp set       → present=false (frozen at the last value)
//  2. Configuration nil           → present=false (absent = not yet evaluated)
//  3. RSP view unavailable        → present=false (absent = not yet evaluated)
//  4. No violations               → True / SatisfyEligibleNodes
//  5. An offender is being
//     removed                     → False / ConflictResolutionInProgress
//  6. Violations, no repair       → False / InConflictWithEligibleNodes
//
// Rules 1-3 report absent rather than Unknown, deliberately. The condition has no Unknown reason,
// and rsc_controller counts every volume whose condition is present and not True as a placement
// conflict (computeActualVolumesSummary): an Unknown would inflate the class-wide
// InConflictWithEligibleNodes counter and put a healthy storage class into RollingOut.
//
// Two independent checks produce violations, and they cannot both live at the replica level:
//
//   - node eligibility (is this node, LVG and thin pool allowed at all?) is a per-replica
//     predicate, already decided by the replica controller — this function only mirrors its
//     verdict, it does not re-derive it;
//   - topology (TransZonal, Zonal) is a property of the member set, not of a member: three
//     replicas can each sit on a perfectly eligible node while the volume still keeps two of its
//     three voters in one zone. Only the volume sees all the zones at once, which is why the
//     reason NodeTopologyMismatch exists at this level and has no replica-level counterpart.
//
// It is a pure read-only computation: it MUST NOT mutate rv, rvrs or rsp.
func computeSatisfyEligibleNodesReport(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	rsp *rspView,
) satisfyEligibleNodesReport {
	// 1. Deletion: preserve the last known value.
	if rv.DeletionTimestamp != nil {
		return satisfyEligibleNodesReport{present: false}
	}

	// 2. No configuration yet: nothing has been evaluated.
	if rv.Status.Configuration == nil {
		return satisfyEligibleNodesReport{present: false}
	}

	// 3. No RSP view: eligibility is undecidable, and reporting a conflict would be a false alarm.
	if rsp == nil {
		return satisfyEligibleNodesReport{present: false}
	}

	topologyViolations := computeTopologyViolations(rv, rsp)

	judged := 0
	var offenders []placementOffender

	for i := range rv.Status.Datamesh.Members {
		m := &rv.Status.Datamesh.Members[i]

		// Access members have no placement of their own to get wrong: they are created on the
		// node that already runs a consumer, not chosen by the scheduler, and being diskless
		// they put no data anywhere. Their node can still leave the pool afterwards, and
		// reporting that would produce a conflict nobody can act on -- the member cannot be
		// moved (it must sit with its consumer), no transition removes it (the cleanup calls
		// deleteRVR directly, so the report could not even reach ConflictResolutionInProgress),
		// and the cleanup refuses to touch it while it is attached. The volume would then hold
		// its storage class out of Ready for as long as the workload runs. That is the same
		// standing, unfixable conflict this file refuses to create for voters-per-zone above.
		//
		// It also aligns the three checks in this file: computeScheduledReport excludes Access,
		// computeTopologyViolations judges voters only, and this one used to take everything.
		if m.Type == v1alpha1.DatameshMemberTypeAccess {
			continue
		}
		judged++

		// Node eligibility first: a member on a node the pool does not allow is misplaced
		// regardless of which zone that node is in, and that is the more actionable reason.
		if detail, ok := computeNodeEligibilityViolation(findRVRByName(rvrs, m.Name)); ok {
			offenders = append(offenders, newPlacementOffender(m,
				v1alpha1.ReplicatedVolumeEligibleNodesViolationReasonOutOfEligibleNodes, detail))
			continue
		}

		if detail, ok := topologyViolations[m.Name]; ok {
			offenders = append(offenders, newPlacementOffender(m,
				v1alpha1.ReplicatedVolumeEligibleNodesViolationReasonNodeTopologyMismatch, detail))
		}
	}

	// 4. Everything is where it is allowed to be. The count is of the members actually judged,
	// so the claim stays true on a volume whose Access members were skipped.
	if len(offenders) == 0 {
		message := fmt.Sprintf("All %d datamesh members are on eligible nodes", judged)
		switch {
		case len(rv.Status.Datamesh.Members) == 0:
			message = "Datamesh has no members yet"
		case judged == 0:
			message = "Datamesh holds only Access members, whose placement is not judged"
		}
		return satisfyEligibleNodesReport{
			present: true,
			status:  metav1.ConditionTrue,
			reason:  v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonSatisfyEligibleNodes,
			message: message,
		}
	}

	// Sorted by replica name so the published list and the offender named in the message are
	// stable across passes, whatever order the members happen to sit in.
	slices.SortFunc(offenders, func(a, b placementOffender) int {
		return cmp.Compare(a.violation.ReplicaName, b.violation.ReplicaName)
	})

	violations := make([]v1alpha1.ReplicatedVolumeEligibleNodesViolation, 0, len(offenders))
	for _, o := range offenders {
		violations = append(violations, o.violation)
	}

	first := offenders[0]
	message := fmt.Sprintf("%d/%d members are not on eligible nodes: replica %s on node %s (%s)",
		len(offenders), judged, first.violation.ReplicaName, first.violation.NodeName, first.detail)

	// 5. One of the offending members is already being taken out.
	if hasActiveConflictResolutionTransition(rv, offenders) {
		return satisfyEligibleNodesReport{
			present:    true,
			status:     metav1.ConditionFalse,
			reason:     v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonConflictResolutionInProgress,
			message:    message,
			violations: violations,
		}
	}

	// 6. Conflict with nothing resolving it.
	return satisfyEligibleNodesReport{
		present:    true,
		status:     metav1.ConditionFalse,
		reason:     v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonInConflictWithEligibleNodes,
		message:    message,
		violations: violations,
	}
}

// computeNodeEligibilityViolation mirrors the replica's SatisfyEligibleNodes condition into a
// volume-level verdict: whether the replica sits on a node, LVM volume group or thin pool the
// storage pool does not allow. It returns the replica's own explanation so the volume message can
// carry it verbatim.
//
// It mirrors a condition rather than re-deriving eligibility from the RSP view: the replica
// controller is the single writer of that verdict, and re-deriving it here would create a second
// source of truth that can disagree with the replica's own status.
//
// A missing replica, a missing condition, an Unknown one, or a verdict whose ObservedGeneration
// lags the replica generation is not a verdict and is not a violation — the replica controller has
// not (re-)evaluated this placement yet, and reporting a conflict on that basis would be a guess.
func computeNodeEligibilityViolation(rvr *v1alpha1.ReplicatedVolumeReplica) (string, bool) {
	if rvr == nil {
		return "", false
	}

	if !obju.StatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType).
		IsFalse().ObservedGenerationCurrent().Eval() {
		return "", false
	}

	cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType)
	switch cond.Reason {
	case v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonNodeMismatch,
		v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonLVMVolumeGroupMismatch,
		v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonThinPoolMismatch:
		detail := cond.Message
		if detail == "" {
			detail = cond.Reason
		}
		return detail, true
	default:
		// WaitingForReplicatedVolume and anything added later: not an eligibility verdict.
		return "", false
	}
}

// computeTopologyViolations reports which voter members violate the volume's topology, keyed by
// member name, with a human-readable detail as the value. It returns nil when the topology
// imposes no placement constraint (Ignored) or when there is nothing to judge.
//
// Only voters (Diskful and LiminalDiskful) are judged. Tie-breakers and Access members carry no
// data, so spreading them is a quorum-survivability question that the datamesh transition guards
// already own; reporting them here would turn a legitimate tie-breaker placement into a standing
// violation.
//
// Zones come from the RSP view — the node's zone as it is now, not the member's Zone field, which
// is a snapshot taken when the member joined. The snapshot is only a fallback for a node the view
// does not cover, and such a member is reported as OutOfEligibleNodes anyway.
//
// Under TransZonal the report deliberately does NOT judge how the voters are distributed across
// zones. That distribution has two supported modes -- pure (one voter per zone) and composite
// (several voters in a zone, which is what lets FTT=2/GMDR=2 run on three zones as 2D|2D|1D, see
// computeTransZonalMinZones in rsc_controller) -- and the two places that define the legal shape
// do not fully agree on the boundary between them: the RSC validator admits composite layouts
// that guardTransZonalVoterPlacement, a per-addition quorum-survivability check, would block.
// Encoding a voters-per-zone number here would mean picking a side in that disagreement and
// standing a healthy, scheduler-produced volume up as a permanent conflict.
//
// What is reported is only what BOTH of them forbid:
//   - a zone the storage pool has no eligible nodes in, or a node with no zone at all;
//   - every voter collapsed into a single zone, which the guard always blocks (losing that zone
//     leaves zero surviving voters, below any qmr) and which the validator rules out by
//     requiring at least two zones with disks for every FTT/GMDR combination.
//
// The zone span comes from rspView.EligibleZones — the zones of the pool's eligible nodes, not
// spec.zones, which is only a node selector and is empty on a pool that never restricted them.
//
// Zonal asks a question of the same shape but with the opposite answer: the voters must occupy
// exactly ONE zone, so more than one occupied zone is a split whatever the counts. This does
// diverge from guardZonalSameZone, deliberately. That guard decides whether one incoming member
// may join and admits any zone holding the most voters, which on an even split is every zone --
// it has nothing to break the tie with and no reason to. This report sees the whole set, and a
// set already spread over two zones is already wrong, so the tie is not an ambiguity to tolerate.
func computeTopologyViolations(
	rv *v1alpha1.ReplicatedVolume,
	rsp *rspView,
) map[string]string {
	topology := rv.Status.Configuration.Topology
	if topology != v1alpha1.TopologyTransZonal && topology != v1alpha1.TopologyZonal {
		return nil
	}

	zoneByMember := make(map[string]string)
	votersPerZone := make(map[string]int)
	for i := range rv.Status.Datamesh.Members {
		m := &rv.Status.Datamesh.Members[i]
		if !m.Type.IsVoter() {
			continue
		}
		zone := memberZone(m, rsp)
		zoneByMember[m.Name] = zone
		votersPerZone[zone]++
	}
	if len(zoneByMember) == 0 {
		return nil
	}

	violations := make(map[string]string)

	if topology == v1alpha1.TopologyTransZonal {
		// Every branch below is gated on the pool having somewhere to spread to, because a
		// volume is not misplaced for a shape its pool never offered. A pool with no zoned
		// nodes at all is not even a silent failure: the storage class validation groups
		// eligible nodes by zone and refuses TransZonal below two zones, so such a pool already
		// reports ConfigurationReady=False/InvalidConfiguration. Repeating that here, as a list
		// of voters to move with nowhere to move them, would point the operator at the volume
		// when the fault is in the pool.
		//
		// A collapse is additionally a property of the set, so there is no one member to blame:
		// every voter is in the wrong place relative to the others, and all of them are
		// reported. Its extra precondition only narrows it — a single voter is trivially "all
		// in one zone", true of a volume mid-formation or down to its last replica, neither of
		// which is misplaced.
		poolHasZones := len(rsp.EligibleZones) > 0
		collapsed := len(zoneByMember) > 1 && len(votersPerZone) == 1 && len(rsp.EligibleZones) > 1

		for name, zone := range zoneByMember {
			switch {
			// The pool is zoned and this member's node is not: a genuine outlier, and a
			// fixable one — label the node or move the replica to a zoned one.
			case zone == "" && poolHasZones:
				violations[name] = "node has no zone, but the volume is TransZonal"
			case poolHasZones && !slices.Contains(rsp.EligibleZones, zone):
				violations[name] = fmt.Sprintf(
					"zone %q holds no eligible nodes of the storage pool", zone)
			case collapsed:
				violations[name] = fmt.Sprintf(
					"all %d diskful voters are in zone %q, but the storage pool spans %d zones",
					len(zoneByMember), zone, len(rsp.EligibleZones))
			}
		}
		return violations
	}

	// Zonal: the voters occupy exactly one zone or they do not. A single occupied zone is the
	// whole contract satisfied, whichever zone it happens to be — including the case where none
	// of the nodes carry a zone at all, where "" is that one zone and there is nothing to fix.
	//
	// No pool-shape guard is needed here, unlike the TransZonal collapse: a pool spanning one
	// zone puts every node in it, so a split cannot arise in the first place.
	if len(votersPerZone) <= 1 {
		return violations
	}

	// The home zone is the real zone holding the most voters — the cheapest place to consolidate
	// — with ties broken lexicographically so the answer to "which ones do I move" is the same on
	// every pass. Under a tie the choice is arbitrary in substance: any occupied zone is an
	// equally valid destination, and the message says where the bulk sits so an operator can
	// decide otherwise.
	//
	// The "" bucket is not a destination and is skipped: consolidating onto nodes that carry no
	// zone satisfies the Zonal contract in no sense, and this file already calls such a node an
	// outlier under TransZonal. Skipping it cannot empty the candidate set — the split guard
	// above means at least two distinct zones are occupied, so dropping "" leaves a real one.
	// The consequence is deliberate: with the voters at, say, 3 unzoned against 1 in zone-a, the
	// home is zone-a and the three unzoned ones are reported, because the shorter list would
	// point at the only destination that fixes nothing.
	homeZone := ""
	homeVoters := -1
	for zone, count := range votersPerZone {
		if zone == "" {
			continue
		}
		if count > homeVoters || (count == homeVoters && zone < homeZone) {
			homeZone = zone
			homeVoters = count
		}
	}

	for name, zone := range zoneByMember {
		if zone == homeZone {
			continue
		}
		violations[name] = fmt.Sprintf(
			"voters are split across %d zones: this one is in %q, %d of %d are in %q",
			len(votersPerZone), zone, homeVoters, len(zoneByMember), homeZone)
	}
	return violations
}

// memberZone returns the zone of the member's node as the storage pool sees it now, falling back
// to the zone recorded on the member when the node is outside the RSP view.
func memberZone(m *v1alpha1.DatameshMember, rsp *rspView) string {
	if node := rsp.FindEligibleNode(m.NodeName); node != nil {
		return node.ZoneName
	}
	return m.Zone
}

// hasActiveConflictResolutionTransition reports whether one of the offending members is being
// taken out of the datamesh right now. A conflict with a repair already under way is a different
// state from one nothing is acting on: the first clears itself, the second needs an operator.
//
// The match is narrow on purpose — the transition must both be a removal AND name an offender.
// Membership churn as such proves nothing: adding an Access replica for a new consumer, or
// healing a tie-breaker, moves no misplaced member, and reporting those as a repair tells the
// operator a volume is being fixed when nothing is fixing it.
//
// A removal is the only transition that can be recognized as a repair from the outside. The
// replacement half of a create-first repair names the NEW replica, not the offender, so it is
// indistinguishable from any other addition — and while it runs the offender is still in place
// and the conflict is still real, which is what the report then says. Nothing resolves these
// conflicts automatically today (the storage class RollingRepair strategy is a stub), so a
// repair in flight means an operator is performing it, and an operator removes the offender.
func hasActiveConflictResolutionTransition(rv *v1alpha1.ReplicatedVolume, offenders []placementOffender) bool {
	for i := range rv.Status.DatameshTransitions {
		t := &rv.Status.DatameshTransitions[i]

		switch t.Type {
		case v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveReplica,
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeForceRemoveReplica:
		default:
			continue
		}

		if slices.ContainsFunc(offenders, func(o placementOffender) bool {
			return o.violation.ReplicaName == t.ReplicaName
		}) {
			return true
		}
	}
	return false
}

// ──────────────────────────────────────────────────────────────────────────────
// Apply
//

// applyScheduledCond sets the Scheduled condition from a computed scheduledReport.
// When report.present is false the condition is left untouched (the last known value is
// preserved, e.g. during deletion).
func applyScheduledCond(rv *v1alpha1.ReplicatedVolume, report scheduledReport) bool {
	if !report.present {
		return false
	}
	return obju.SetStatusCondition(rv, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeCondScheduledType,
		Status:  report.status,
		Reason:  report.reason,
		Message: report.message,
	})
}

// applySatisfyEligibleNodesCond sets the SatisfyEligibleNodes condition from a computed
// satisfyEligibleNodesReport. When report.present is false the condition is left untouched.
func applySatisfyEligibleNodesCond(rv *v1alpha1.ReplicatedVolume, report satisfyEligibleNodesReport) bool {
	if !report.present {
		return false
	}
	return obju.SetStatusCondition(rv, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesType,
		Status:  report.status,
		Reason:  report.reason,
		Message: report.message,
	})
}

// applyEligibleNodesViolations publishes the per-replica placement violations that back the
// SatisfyEligibleNodes condition. When report.present is false the list is left untouched, so it
// stays in step with the condition it explains; a volume back within its constraints has the list
// cleared.
func applyEligibleNodesViolations(rv *v1alpha1.ReplicatedVolume, report satisfyEligibleNodesReport) bool {
	if !report.present {
		return false
	}
	if slices.Equal(rv.Status.EligibleNodesViolations, report.violations) {
		return false
	}
	// Cloned so the published status does not alias the report.
	rv.Status.EligibleNodesViolations = slices.Clone(report.violations)
	return true
}

// ──────────────────────────────────────────────────────────────────────────────
// Construction
//

// newPlacementOffender builds a placementOffender for a datamesh member.
func newPlacementOffender(
	m *v1alpha1.DatameshMember,
	reason v1alpha1.ReplicatedVolumeEligibleNodesViolationReason,
	detail string,
) placementOffender {
	return placementOffender{
		violation: v1alpha1.ReplicatedVolumeEligibleNodesViolation{
			NodeName:    m.NodeName,
			ReplicaName: m.Name,
			Reason:      reason,
		},
		detail: detail,
	}
}
