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
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/datamesh"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/idset"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

// ──────────────────────────────────────────────────────────────────────────────
// Wiring / construction
//

type Reconciler struct {
	cl     client.Client
	scheme *runtime.Scheme

	// attachedRVAs is metrics-only state: it never affects reconciliation decisions.
	attachedRVAs *attachedRVATracker
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

func NewReconciler(cl client.Client, scheme *runtime.Scheme) *Reconciler {
	return &Reconciler{cl: cl, scheme: scheme, attachedRVAs: newAttachedRVATracker()}
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile
//

// Reconcile pattern: Pure orchestration
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	rf := flow.BeginRootReconcile(ctx)

	// Get the ReplicatedVolume.
	rv, err := r.getRV(rf.Ctx(), req.Name)
	if err != nil {
		return rf.Failf(err, "getting ReplicatedVolume").ToCtrl()
	}
	if rv == nil {
		// Metrics-only state: the volume is gone, so are its attachments.
		r.attachedRVAs.forgetVolume(req.Name)
		return flow.MergeReconciles(
			r.reconcileOrphanedRVAs(rf.Ctx(), req.Name),
			r.reconcileOrphanedRVRs(rf.Ctx(), req.Name),
		).ToCtrl()
	}

	// Load RSC (Auto mode only; Manual mode has no RSC reference).
	var rsc *v1alpha1.ReplicatedStorageClass
	if rv.Spec.ReplicatedStorageClassName != "" {
		rsc, err = r.getRSC(rf.Ctx(), rv.Spec.ReplicatedStorageClassName)
		if err != nil {
			return rf.Failf(err, "getting ReplicatedStorageClass").ToCtrl()
		}
	}

	// Load child resources.
	rvas, err := r.getRVAsSorted(rf.Ctx(), req.Name)
	if err != nil {
		return rf.Failf(err, "listing ReplicatedVolumeAttachments").ToCtrl()
	}
	// Metrics-only state: drop RVAs that left without this controller removing the finalizer.
	r.attachedRVAs.retain(req.Name, rvas)

	rvrs, err := r.getRVRsSorted(rf.Ctx(), req.Name)
	if err != nil {
		return rf.Failf(err, "listing ReplicatedVolumeReplicas").ToCtrl()
	}

	// Maintenance: an operator asked us to leave this volume alone. Placed after the children are
	// loaded and BEFORE the deletion branch on purpose — deletion handling is one of the actions
	// the pause stops, and no evaluation order may let it slip past. The paused path is a
	// reconcile step of its own rather than an early return, because the observational layer it
	// still runs has to be persisted by a status patch of its own.
	if rv.IsUnderMaintenance() {
		return r.reconcileMaintenance(rf.Ctx(), rv, rvrs, rvas).ToCtrl()
	}

	// Handle deletion: force-cleanup children, then remove our finalizer from RV.
	//
	// rvShouldNotExist returns true only when:
	//   - RV has DeletionTimestamp set,
	//   - no finalizers except ours,
	//   - no attached datamesh members,
	//   - no Detach transitions in progress.
	//
	// While the RV is still attached or detaching, rvShouldNotExist returns false
	// and reconciliation continues through the normal path (where reconcileRVAMetadata
	// and the future attach/detach logic handle the graceful detach lifecycle).
	//
	// Once all attachments are fully resolved, we enter this branch and force-delete
	// all remaining child resources (RVRs, datamesh state) via reconcileDeletion.
	if rvShouldNotExist(rv) {
		// Order matters (Go evaluates arguments left to right):
		// 1. reconcileDeletion: set RVA conditions, force-delete RVRs, clear datamesh members.
		// 2. reconcileRVAMetadata: remove finalizer from deleting RVAs (may trigger
		//    Kubernetes finalization = object deletion). Must run after reconcileDeletion,
		//    otherwise reconcileDeletion would try to patch conditions on an already-deleted RVA.
		// 3. reconcileMetadata: remove RV finalizer if no children remain.
		result := flow.MergeReconciles(
			r.reconcileDeletion(rf.Ctx(), rv, rvas, &rvrs),
			r.reconcileRVAMetadata(rf.Ctx(), rv, rvas),
			r.reconcileMetadata(rf.Ctx(), rv, rvrs),
		)
		if result.Error() == nil && !obju.HasFinalizer(rv, v1alpha1.RVControllerFinalizer) {
			observeRVDeletion(rv)
		}
		return result.ToCtrl()
	}

	// Reconcile the RV metadata (finalizers and labels).
	outcome := r.reconcileMetadata(rf.Ctx(), rv, rvrs)
	if outcome.ShouldReturn() {
		return outcome.ToCtrl()
	}

	base := rv.DeepCopy()

	// Remove the UnderMaintenance condition on the first pass after spec.maintenance is cleared.
	// applyUnderMaintenanceCond is the single writer of the condition and is called from both
	// paths; a volume with the field set never reaches this line, so here it only ever removes.
	outcome = outcome.ReportChangedIf(applyUnderMaintenanceCond(rv))

	// Derive rv.Status.Configuration from the appropriate source (RSC or ManualConfiguration).
	// Called here only for initial set (config is nil). During normal operation,
	// reconcileRVConfiguration is called inside reconcileNormalOperation.
	// During create formation, config is frozen (only formation restart calls
	// reconcileRVConfiguration). During adopt formation, config is NOT re-derived;
	// adopt accepts pre-existing replicas as-is regardless of RSC mismatch.
	if rv.Status.Configuration == nil {
		outcome = outcome.Merge(r.reconcileRVConfiguration(rf.Ctx(), rv, rsc))
		if outcome.ShouldReturn() {
			return outcome.ToCtrl()
		}
	}

	// Preparatory actions.
	eo := flow.MergeEnsures(
		ensureDatameshReplicaRequests(rf.Ctx(), rv, rvrs),
		ensureStatusSize(rf.Ctx(), rv, rvrs),
	)
	if eo.Error() != nil {
		return rf.Fail(eo.Error()).ToCtrl()
	}
	outcome = outcome.WithChangeFrom(eo)

	// Perform main processing. The RSP view outlives the block: reconcilePlacementConditions
	// below needs it to judge placement, and it must run for a volume in any state, including
	// one still forming.
	var rsp *rspView
	// Transitions the datamesh engine completed during this pass, for the datamesh metrics.
	var completedTransitions []v1alpha1.ReplicatedVolumeDatameshTransition
	if rv.Status.Configuration != nil {
		var err error
		rsp, err = r.getRSP(rf.Ctx(), rv.Status.Configuration.ReplicatedStoragePoolName, rvrs, rvas)
		if err != nil {
			return rf.Failf(err, "getting RSP").ToCtrl()
		}
		if forming, formationStepIdx := isFormationInProgress(rv); forming {
			outcome = outcome.Merge(r.reconcileFormation(rf.Ctx(), rv, &rvrs, rvas, rsp, rsc, formationStepIdx))
		} else {
			outcome = flow.MergeReconciles(outcome,
				r.reconcileRVConfiguration(rf.Ctx(), rv, rsc),
				r.reconcileNormalOperation(rf.Ctx(), rv, &rvrs, rvas, rsp, &completedTransitions),
				r.reconcileLayoutStatus(rf.Ctx(), rv, rvrs, rvas),
			)
		}
		if outcome.ShouldReturn() {
			return outcome.ToCtrl()
		}
	}

	// Compute and apply data-plane conditions (Ready, Resilient, AttachedReady) from the
	// refreshed effective layout, configuration, formation/deletion state and the attachment
	// state of the members. Runs for every RV that did not enter reconcileDeletion this pass —
	// including deleting-but-still-attached volumes (DeletionTimestamp != nil,
	// rvShouldNotExist == false), where computeReadyReport yields Terminating by design during
	// graceful detach. The force-delete branch (reconcileDeletion) writes Ready separately
	// inside its own atomic status patch.
	outcome = outcome.Merge(r.reconcileDataPlaneConditions(rf.Ctx(), rv, rvrs, rvas))
	if outcome.ShouldReturn() {
		return outcome.ToCtrl()
	}

	// Report configuration convergence (Configured) from the applied datamesh revision of the
	// members. Placed next to the data-plane conditions rather than inside normal operation:
	// a forming volume runs reconcileFormation instead, and a volume without a configuration
	// runs neither, yet both must have the condition reported (a forming volume as
	// ConfigurationUnknown until the datamesh has both a revision and members, then
	// ConfigurationInProgress; a config-less one as absent). The deletion branch does not write
	// it at all — the condition freezes at its last value, like Resilient and AttachedReady.
	outcome = outcome.Merge(r.reconcileConfiguredCondition(rf.Ctx(), rv, rvrs))
	if outcome.ShouldReturn() {
		return outcome.ToCtrl()
	}

	// Report placement (Scheduled, SatisfyEligibleNodes and the violations behind it) from the
	// replica-level verdicts and the storage pool view. Placed next to the other condition
	// reporters, and for the same reason: a forming volume runs reconcileFormation instead of
	// normal operation, yet its scheduling progress is exactly what an operator watches then.
	// The deletion branch does not write them at all — they freeze at their last value, like
	// Resilient, AttachedReady and Configured.
	outcome = outcome.Merge(r.reconcilePlacementConditions(rf.Ctx(), rv, rvrs, rsp))
	if outcome.ShouldReturn() {
		return outcome.ToCtrl()
	}

	// If datamesh just made the RV eligible for deletion (e.g., last member detached),
	// requeue immediately so the next reconcile enters reconcileDeletion.
	if rv.DeletionTimestamp != nil && rvShouldNotExist(rv) {
		outcome = outcome.Merge(rf.ContinueAndRequeue())
	}

	// Reconcile RVA and RVR finalizers.
	outcome = flow.MergeReconciles(
		outcome,
		r.reconcileRVAMetadata(rf.Ctx(), rv, rvas),
		r.reconcileRVRFinalizers(rf.Ctx(), rv, rvrs),
	)
	if outcome.ShouldReturn() {
		return outcome.ToCtrl()
	}

	// Compute pending metric observations before patching, then observe them
	// only after the status state they describe has been committed.
	now := time.Now()
	metricObservations := computeDatameshMetricObservations(now, rv, base.Status.DatameshTransitions, completedTransitions, rvrs)
	metricObservations = append(metricObservations, computeRVInitialFormationMetricObservations(now, base, rv)...)

	if outcome.DidChange() {
		if err := r.patchRVStatus(rf.Ctx(), rv, base); err != nil {
			return rf.Fail(err).ToCtrl()
		}
		metricObservations.observe()
	}

	return outcome.ToCtrl()
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: orphaned RVRs
//

// reconcileOrphanedRVRs handles RVRs that reference a deleted/absent RV.
// Loads RVRs by rvName and delegates to reconcileRVRFinalizers with rv=nil,
// which adds the finalizer to non-deleting RVRs and removes it from deleting ones
// (isRVRMemberOrLeavingDatamesh returns false when rv is nil).
//
// Reconcile pattern: Pure orchestration
func (r *Reconciler) reconcileOrphanedRVRs(
	ctx context.Context,
	rvName string,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "orphaned-rvrs")
	defer rf.OnEnd(&outcome)

	rvrs, err := r.getRVRsSorted(rf.Ctx(), rvName)
	if err != nil {
		return rf.Failf(err, "listing RVRs for deleted RV")
	}
	if len(rvrs) == 0 {
		return rf.Done()
	}

	return r.reconcileRVRFinalizers(rf.Ctx(), nil, rvrs)
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: maintenance
//

// reconcileMaintenance is the whole of what the controller does to a volume whose
// spec.maintenance is set: it reports the pause and republishes the health aggregated from the
// replicas, then stops. Everything else the controller would do — deletion handling and the
// volume's own finalizer, the metadata reconcile, configuration derivation, the preparatory
// datamesh steps, formation, normal operation with its membership, attachment and resize planning
// and every datamesh transition, layout status, the Configured and placement conditions, and the
// replica and attachment finalizer steps — does not run, and the status each of them owns freezes
// at its last published value rather than being cleared (the convention the deletion branch
// already uses for Resilient, AttachedReady and Configured).
//
// The data-plane conditions are the only observational layer, and that is the whole list: their
// inputs are the replicas' own status, which the replica controller and the agent keep
// maintaining — this iteration stops neither. Why the placement conditions and the metrics are
// NOT on that list is in the Maintenance section of README.md.
//
// Reconcile pattern: In-place reconciliation
func (r *Reconciler) reconcileMaintenance(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	rvas []*v1alpha1.ReplicatedVolumeAttachment,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "maintenance")
	defer rf.OnEnd(&outcome)

	base := rv.DeepCopy()

	changed := applyUnderMaintenanceCond(rv)

	// Ready, Resilient and AttachedReady: the health aggregated from the replicas, which is what
	// the operator watches to see whether the volume recovers on its own.
	dataPlaneOutcome := r.reconcileDataPlaneConditions(rf.Ctx(), rv, rvrs, rvas)
	if dataPlaneOutcome.Error() != nil {
		return dataPlaneOutcome
	}
	changed = changed || dataPlaneOutcome.DidChange()

	if changed {
		if err := r.patchRVStatus(rf.Ctx(), rv, base); err != nil {
			return rf.Failf(err, "publishing maintenance status")
		}
	}

	// A terminal outcome is propagated only AFTER the status has been persisted. Returning it
	// first would drop the observational layer along with the condition — the exact early-return
	// hazard this path carries its own status patch to avoid.
	if dataPlaneOutcome.ShouldReturn() {
		return dataPlaneOutcome
	}

	// We're done. Don't continue further reconciliation.
	return rf.Done().ReportChangedIf(changed)
}

// applyUnderMaintenanceCond brings the UnderMaintenance condition in line with spec.maintenance
// and is the SINGLE writer of that condition. It is presence-based: the condition is set while
// the field is set, carrying the mode as its reason, and removed when the field is cleared.
//
// Both the paused path and the normal path call it; the normal-path call is the one that removes
// the condition on the first pass after the field is cleared.
func applyUnderMaintenanceCond(rv *v1alpha1.ReplicatedVolume) bool {
	if !rv.IsUnderMaintenance() {
		return obju.RemoveStatusCondition(rv, v1alpha1.ReplicatedVolumeCondUnderMaintenanceType)
	}
	// The reason names the mode. There is exactly one reason per mode and the CRD enum admits
	// exactly one mode, so a new mode must bring its own reason constant with it.
	return obju.SetStatusCondition(rv, metav1.Condition{
		Type:   v1alpha1.ReplicatedVolumeCondUnderMaintenanceType,
		Status: metav1.ConditionTrue,
		Reason: v1alpha1.ReplicatedVolumeCondUnderMaintenanceReasonNoResourceReconciliation,
		Message: fmt.Sprintf(
			"Every volume controller action is stopped by spec.maintenance=%s; "+
				"the volume will not finish deleting until it is cleared", rv.Spec.Maintenance),
	})
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: normal-operation
//

// reconcileNormalOperation handles the steady-state lifecycle of a formed datamesh
// (attach handling, scaling, etc.).
// The transitions the datamesh engine completed are stored in completedTransitions.
//
// Reconcile pattern: Pure orchestration
func (r *Reconciler) reconcileNormalOperation(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rvrs *[]*v1alpha1.ReplicatedVolumeReplica,
	rvas []*v1alpha1.ReplicatedVolumeAttachment,
	rsp *rspView,
	completedTransitions *[]v1alpha1.ReplicatedVolumeDatameshTransition,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "normal-operation")
	defer rf.OnEnd(&outcome)

	// Create Access RVRs for Active RVAs on nodes without any RVR.
	outcome = r.reconcileCreateAccessReplicas(rf.Ctx(), rv, rvrs, rvas, rsp)
	if outcome.ShouldReturn() {
		return outcome
	}

	// Run datamesh transition engine: membership, quorum, attachment, network transitions.
	changed, dmrctxs, completed := datamesh.ProcessTransitionsWithCompleted(
		rf.Ctx(), rv, rsp, *rvrs, rvas, datamesh.FeatureFlags{})
	*completedTransitions = completed
	if changed {
		outcome = outcome.ReportChanged()
	}

	outcome = outcome.Merge(
		// Update RVA conditions and status fields from datamesh replica contexts.
		r.reconcileRVAConditionsFromDatameshReplicaContext(rf.Ctx(), dmrctxs),

		// Delete unnecessary Access RVRs (redundant or unused).
		r.reconcileDeleteAccessReplicas(rf.Ctx(), rv, rvrs, rvas),
	)
	if outcome.ShouldReturn() {
		return outcome
	}

	// Converge the datamesh layout toward the intended layout (at most one whitelisted
	// action per pass). Runs last so it sees any transition just created by ProcessTransitions
	// and no-ops while a membership transition is in flight.
	return outcome.Merge(r.reconcileLayoutConvergence(rf.Ctx(), rv, rvrs, rvas))
}

// applyDatameshTransitionStepMessage sets the Message field on a datamesh transition step.
// Returns true if the message was changed. No-op if step is nil.
func applyDatameshTransitionStepMessage(step *v1alpha1.ReplicatedVolumeDatameshTransitionStep, msg string) bool {
	if step == nil || step.Message == msg {
		return false
	}
	step.Message = msg
	return true
}

// makeDatameshSingleStepTransition creates a transition with a single step that is immediately Active.
//
// Exception: uses metav1.Now() for StartedAt. This is controller-owned state
// (persisted decision timestamp), acceptable here because the value is set once
// and stabilized across subsequent reconciliations.
func makeDatameshSingleStepTransition(
	typ v1alpha1.ReplicatedVolumeDatameshTransitionType,
	group v1alpha1.ReplicatedVolumeDatameshTransitionGroup,
	replicaName string,
	replicaType v1alpha1.ReplicaType,
	stepName string,
	datameshRevision int64,
) v1alpha1.ReplicatedVolumeDatameshTransition {
	now := metav1.Now()
	return v1alpha1.ReplicatedVolumeDatameshTransition{
		Type:        typ,
		Group:       group,
		ReplicaName: replicaName,
		ReplicaType: replicaType,
		Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
			{
				Name:             stepName,
				Status:           v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
				DatameshRevision: datameshRevision,
				StartedAt:        &now,
			},
		},
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: layout convergence
//

// reconcileLayoutConvergence performs at most one whitelisted layout-convergence action per
// reconcile pass to move the actual datamesh layout toward the intended layout:
//
//   - P1 retype (r3→r2 migration): convert one Diskful replica into a TieBreaker by patching
//     its spec.type together with its (now invalid) backing-volume fields; the existing
//     ChangeRole → DMTE machinery then drives the membership transition (no resync, no data
//     movement).
//   - P2 tie-breaker deficit: create a missing TieBreaker replica when the diskful count is
//     already correct (e.g. a freshly formed r2 volume still at 2D, or a manually deleted TB).
//     The scheduler places it and it joins the datamesh via the standard tiebreaker/v1 plan. The
//     same action replaces a TieBreaker whose RVR is being deleted: it is still a member and
//     still counted by the raw layout, so the replacement deficit is computed separately
//     (computeTargetTieBreakerReplacement) — strict create-first, the datamesh releases the old
//     one only once the replacement is operational.
//   - In-place R2→R3: prepare and promote the existing TB on its current node,
//     preserving its identity and waiting for the third copy before raising qmr.
//   - P3 heal: create a missing Diskful replica when a copy is gone from the membership. The replica is created
//     with no node and no storage; the scheduler places it (under the resync limiter), the
//     replica controller asks to join, the datamesh picks an add-diskful plan and the resync runs
//     itself. Whatever removed the member — a graceful leave, a force-removal, a manual
//     force-leave, the automatic cleanup of an orphan — is immaterial: the trigger is the missing
//     member, not its cause.
//   - P4 tie-breaker surplus (including completion of older add-first upsizes): delete one TieBreaker replica
//     once the copies match the configuration and every one of them is UpToDate. Deleting the RVR
//     is the whole action — the replica controller turns it into a Leave request and the datamesh
//     removes the member under its own guards.
//   - P5 diskful surplus: delete one unattached Diskful replica when the tie-breaker count
//     is sufficient and the datamesh's removal guards permit it under the new configuration.
//
// Safety invariant: convergence removes only surplus members under the datamesh's guards,
// through normal RVR deletion, and waits for a departure before choosing another removal.
// Repeating each whitelisted action must end in the intended layout.
// The decision is a pure compute helper (computeTargetLayoutAction), reused
// by the MembershipLayoutConverged condition writer (reconcileLayoutStatus) so the condition and
// the action stay in agreement; convergence itself never writes the condition (single-writer
// invariant) and therefore discards the report half of that decision.
//
// After performing an action it returns ContinueAndRequeue: the split-client cache may be stale
// relative to our own write, so we requeue rather than rely on the watch (see
// controller-reconciliation.mdc). The outcome is deliberately NON-terminal — the root Reconcile
// must still reach its status patch, otherwise every status change computed in the acting pass
// (including the MembershipLayoutConverged report for exactly this action) is dropped (see
// controller-reconciliation-flow.mdc, Continue* vs Done* with requeue). Mismatches outside the
// whitelist are left untouched and reported honestly by the condition writer.
//
// The target itself encodes the drift check: computeTargetLayoutAction yields layoutActionNone
// exactly when there is nothing to enforce this pass, so no separate is*InSync* step is needed —
// an action kind other than layoutActionNone IS "not in sync".
//
// Reconcile pattern: Target-state driven
func (r *Reconciler) reconcileLayoutConvergence(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rvrs *[]*v1alpha1.ReplicatedVolumeReplica,
	rvas []*v1alpha1.ReplicatedVolumeAttachment,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "layout-convergence")
	defer rf.OnEnd(&outcome)

	// Preconditions: configuration acknowledged (computeTargetLayoutAction dereferences it) and
	// RV not being deleted. The deletion check is also made inside computeTargetLayoutAction,
	// which reports Unknown/VolumeDeleting and never yields an action; it is repeated here so this
	// step provably performs no I/O for a deleting volume. Formation completion is guaranteed by
	// the caller (normal-operation branch); active layout-changing transitions are handled inside
	// computeTargetLayoutAction (it returns no action then).
	if rv.Status.Configuration == nil || rv.DeletionTimestamp != nil {
		return rf.Continue()
	}

	// The report half of the decision belongs to reconcileLayoutStatus (single writer of the
	// MembershipLayoutConverged condition); convergence acts on the target only.
	targetAction, _ := computeTargetLayoutAction(rv, *rvrs, rvas)
	switch targetAction.kind {
	case layoutActionRetypeToDiskful:
		rvr := findRVRByName(*rvrs, targetAction.retypeRVRName)
		if rvr == nil {
			return rf.ContinueAndRequeue()
		}
		base := rvr.DeepCopy()
		// The scheduler assigns storage on this same node; the member remains TB
		// until DMTE's preparation and connectivity guards pass.
		rvr.Spec.Type = v1alpha1.ReplicaTypeDiskful
		if err := r.patchRVR(rf.Ctx(), rvr, base); err != nil {
			return rf.Failf(err, "retyping TieBreaker RVR %s to Diskful", rvr.Name)
		}
		return rf.ContinueAndRequeue()

	case layoutActionRetypeToTieBreaker:
		rvr := findRVRByName(*rvrs, targetAction.retypeRVRName)
		if rvr == nil {
			// Candidate vanished (stale cache); recompute next pass.
			return rf.Continue()
		}
		// Apply the target in the Reconcile method (not in the patch helper); take base
		// immediately before the mutation and patch with the existing optimistic-lock merge.
		base := rvr.DeepCopy()
		applyRVRRetypeToTieBreaker(rvr)
		if err := r.patchRVR(rf.Ctx(), rvr, base); err != nil {
			return rf.Failf(err, "retyping Diskful RVR %s to TieBreaker", rvr.Name)
		}
		rf.Log().Info("Converging layout: retyped Diskful replica to TieBreaker", "rvr", rvr.Name)
		return rf.ContinueAndRequeue()

	// Both creations are one policy applied to two replica types, so they share the arm: an RVR
	// of the target type with an empty nodeName and no backing-volume fields. The scheduler
	// assigns the node, the volume group and the thin pool, exactly as it does for formation and
	// for a replica an operator adds by hand. (The creation stays here rather than in a create*
	// helper: a helper of that category is one Create call and carries no policy — see
	// controller-reconcile-helper-create.mdc — and the AlreadyExists interpretation below is
	// policy.)
	case layoutActionCreateTieBreaker, layoutActionCreateDiskful:
		replicaType := v1alpha1.ReplicaTypeTieBreaker
		if targetAction.kind == layoutActionCreateDiskful {
			replicaType = v1alpha1.ReplicaTypeDiskful
		}
		// The name is chosen deterministically by newRVR (ChooseNewName), so a repeated Create
		// after a stale-cache retry converges via AlreadyExists.
		rvr, err := newRVR(rv, *rvrs, replicaType, "")
		if err != nil {
			return rf.Failf(err, "creating %s RVR", replicaType)
		}
		if _, err := obju.SetControllerRef(rvr, rv, r.scheme); err != nil {
			return rf.Failf(err, "creating %s RVR", replicaType)
		}
		if err := r.createRVR(rf.Ctx(), rvr); err != nil {
			if apierrors.IsAlreadyExists(err) {
				// Expected race: a concurrent reconcile — or, for a diskful copy, an operator
				// creating a replica by hand — already claimed the same deterministic name. Do
				// not Get immediately (the cache may not yet contain it); requeue and let the
				// next pass observe it.
				rf.Log().Info("Converging layout: replica already exists, requeuing", "type", replicaType)
				return rf.ContinueAndRequeue()
			}
			return rf.Failf(err, "creating %s RVR", replicaType)
		}
		*rvrs = insertRVRSorted(*rvrs, rvr)
		rf.Log().Info("Converging layout: created replica", "type", replicaType, "rvr", rvr.Name)
		return rf.ContinueAndRequeue()

	// Deleting the RVR is the whole removal action: the RV
	// controller finalizer holds the object, the replica controller turns the deletion timestamp
	// into a Leave request, the datamesh runs the tiebreaker/v1 removal plan under its guards, and
	// the DRBD resource is torn down after the member has left. deleteRVR stamps the local copy, so
	// the replacement step (which counts leaving tie-breakers) sees the departure on this very pass.
	case layoutActionRemoveTieBreaker:
		rvr := findRVRByName(*rvrs, targetAction.removeRVRName)
		if rvr == nil {
			// Candidate vanished between the decision and the action (stale cache). A race, not a
			// failure — but the report of this pass names a replica that is already gone, so the
			// recomputation is asked for explicitly instead of being left to the watch.
			return rf.ContinueAndRequeue()
		}
		if err := r.deleteRVR(rf.Ctx(), rvr); err != nil {
			if apierrors.IsConflict(err) {
				// The name now belongs to a newer replica than the one in the cache (deleteRVR
				// guards by UID). The same stale-cache race as above, and the object that holds the
				// name — possibly a copy — must not be touched.
				rf.Log().Info("Converging layout: surplus tie-breaker was replaced under the same name, requeuing",
					"rvr", rvr.Name)
				return rf.ContinueAndRequeue()
			}
			return rf.Failf(err, "deleting surplus TieBreaker RVR %s", rvr.Name)
		}
		rf.Log().Info("Converging layout: deleted surplus TieBreaker replica", "rvr", rvr.Name)
		return rf.ContinueAndRequeue()

	case layoutActionRemoveDiskful:
		rvr := findRVRByName(*rvrs, targetAction.removeRVRName)
		if rvr == nil {
			// Candidate vanished from the snapshot; recompute instead of choosing another.
			return rf.ContinueAndRequeue()
		}
		if err := r.deleteRVR(rf.Ctx(), rvr); err != nil {
			if apierrors.IsConflict(err) {
				rf.Log().Info("Converging layout: surplus diskful was replaced under the same name, requeuing",
					"rvr", rvr.Name)
				return rf.ContinueAndRequeue()
			}
			return rf.Failf(err, "deleting surplus Diskful RVR %s", rvr.Name)
		}
		rf.Log().Info("Converging layout: deleted surplus Diskful replica", "rvr", rvr.Name)
		return rf.ContinueAndRequeue()

	default: // layoutActionNone
		return rf.Continue()
	}
}

// membershipLayoutConvergedVolumeDeletingMessage is the MembershipLayoutConverged message published on both deletion
// paths (the normal-operation writer via computeTargetLayoutAction, and the early
// reconcileDeletion branch that an unattached RV reaches directly).
const membershipLayoutConvergedVolumeDeletingMessage = "volume is being deleted; layout convergence suspended"

// layoutActionKind enumerates the whitelisted layout-convergence actions.
type layoutActionKind int

const (
	// layoutActionNone means no convergence action is taken this pass. The report explains why:
	// already converged, a transition/action is already in flight, no admissible candidate, or
	// the mismatch is outside the convergence whitelist.
	layoutActionNone layoutActionKind = iota
	// layoutActionRetypeToTieBreaker converts one Diskful replica into a TieBreaker (P1).
	layoutActionRetypeToTieBreaker
	// layoutActionCreateTieBreaker creates a missing TieBreaker replica (P2).
	layoutActionCreateTieBreaker
	// layoutActionCreateDiskful creates a missing Diskful replica (P3 heal).
	layoutActionCreateDiskful
	// layoutActionRemoveTieBreaker deletes one surplus TieBreaker replica (P4).
	layoutActionRemoveTieBreaker
	// layoutActionRemoveDiskful deletes one surplus Diskful replica (P5).
	layoutActionRemoveDiskful
	// layoutActionRetypeToDiskful reserves the existing TB for in-place R2 -> R3.
	layoutActionRetypeToDiskful
)

// targetLayoutAction is the pure decision of what (if anything) layout convergence should do this
// pass. Computed by computeTargetLayoutAction and consumed by reconcileLayoutConvergence (to act).
//
// It carries the target only: the MembershipLayoutConverged report describing the same decision is a
// separate output (membershipLayoutConvergedReport), so status-shaped report data is never mixed into the
// target artifact (see controller-reconcile-helper-compute.mdc, "Patch-domain separation").
type targetLayoutAction struct {
	kind layoutActionKind
	// retypeRVRName identifies the replica whose spec type changes, including
	// cancellation of an unstarted promotion reservation.
	retypeRVRName string
	// removeRVRName is the RVR chosen for a P4 or P5 removal.
	removeRVRName string
}

// membershipLayoutConvergedReport is the published MembershipLayoutConverged report describing the convergence
// decision. Computed by computeTargetLayoutAction alongside the target and consumed by the
// MembershipLayoutConverged condition writer (reconcileLayoutStatus), so the action and the condition never
// disagree.
type membershipLayoutConvergedReport struct {
	status  metav1.ConditionStatus
	reason  string
	message string
}

// computeTargetLayoutAction decides the single whitelisted convergence action (if any) that moves
// the actual datamesh layout toward the intended layout, and produces the MembershipLayoutConverged report
// as a SEPARATE output (target and report are never mixed into one artifact).
// It is pure (non-I/O, no mutation of inputs) and deterministic.
//
// Decision order (fixed — each earlier step wins over the later ones):
//  1. RV deletion → Unknown/VolumeDeleting, no action.
//  2. An active layout-changing transition → no action (with a named departure report
//     when a counted copy is deleting, otherwise Converging). This precedes the
//     actual/intended comparison on purpose: mid-flight D→TB makes the counted layout equal the
//     intended one for one step (the member is already a TieBreaker while the transition is still
//     running), and reporting Converged there would flip the condition True and back.
//     Before generic pending-work checks, in-place promotion reserves the TB and
//     handles a pre-dispatch target reversal or lost original voter.
//  3. ANY remaining retype requested in an earlier pass (spec flipped, DMTE not dispatched yet) →
//     Converging, in either direction (to a tie-breaker, or to diskful). The step does not look
//     at the deficit: a flipped spec is a layout change in flight even when the intended layout
//     no longer asks for it (the configuration was switched back inside the dispatch window), and
//     Converged there would be a false "nothing is moving". Because the step also precedes the
//     replacement domain, a terminating tie-breaker waits for the pending retype to resolve
//     before a replacement is created — a combination convergence itself never produces (it
//     issues at most one action per pass).
//  4. A tie-breaker replacement deficit (a tie-breaker member whose RVR is being deleted has no
//     replacement yet) → create the replacement (strict create-first). This also precedes the
//     actual/intended comparison: the raw layout still counts the terminating tie-breaker, so the
//     comparison alone would report Converged while the only tie-breaker is leaving.
//  5. A deleting counted copy at matching D counts → CannotConverge (deletion cannot
//     be rolled back); at surplus D counts → Converging, naming the departure and its
//     datamesh message. Promotion, retypes and TB replacement retain priority; a real
//     copy deficit still reaches P3 and its creation-in-flight handling.
//  6. Comparison of actual against intended: equal AND no diskful creation in flight →
//     Converged; otherwise the whitelist below.
//
// Whitelist (in-place promotion/recovery is considered before these general patterns; all
// other mismatches are reported but never acted upon). Repeating each action must
// end in the intended layout:
//   - P1 retype (r3→r2): actualD > intendedD && actualTB < intendedTB — convert one Diskful voter
//     into the missing tie-breaker. A 4D volume at an r2 config becomes 3D+1TB,
//     then P5 trims the remaining surplus copy.
//   - P2 tie-breaker deficit: actualD == intendedD && actualTB < intendedTB — create the missing
//     tie-breaker.
//   - P3 heal (diskful deficit): actualD < intendedD — create the missing diskful copy
//     when the in-place TB promotion does not own the deficit. A surplus TB left
//     over after an add-first growth is handled by P4.
//   - P4 tie-breaker surplus: actualD == intendedD && actualTB > intendedTB —
//     delete one tie-breaker replica, but only once every counted copy is UpToDate
//     (computeTargetSurplusTieBreakerRemoval).
//   - P5 diskful surplus: actualD > intendedD && actualTB >= intendedTB — delete one
//     unattached Diskful replica allowed by the datamesh removal guards. Departures
//     and orphans block a second removal; non-UpToDate copies are preferred.
//
// P3 and P4 complete an add-first upsize when a copy was already placed before
// the TB could be reserved. Healthy 2D+TB → 3D uses the in-place promotion instead.
// The five patterns are mutually exclusive: D deficits belong to P3; equal D counts
// split by TB deficit/surplus into P2/P4; D surpluses split into P1 when TB is missing
// and P5 otherwise. Thus the order of the case arms carries no meaning.
//
// Idempotency across the split-client cache is handled by counting in-flight work: a retype whose
// spec was already flipped (pendingRetypeToTieBreakerMemberNames / pendingRetypeToDiskfulMemberNames,
// step 3), a tie-breaker RVR already created but not yet a member (countPendingTieBreakerCreations)
// or a diskful RVR the layout does not count yet (countPendingDiskfulCreations) is reported without
// issuing a second action. A deleting counted copy similarly owns its departure
// until membership changes (computeDiskfulRemovalPendingReport).
//
// # Diskful replicas in flight
//
// A diskful replica in flight is one the LAYOUT does not count: it has no member, or its member is
// neither Diskful nor LiminalDiskful (see isDiskfulCreationInFlight). Counting by MEMBERSHIP — the
// formula the tie-breaker pattern can afford — leaks: an add-diskful plan that has to raise quorum
// first enters the member as Access (the vestibule), where the replica IS a member while the layout
// still does not count it. The invariant that must hold is that counted copies plus copies in
// flight never exceed the intended copy count through convergence's own doing; the leak breaks it,
// and the cost is an unnecessary resync followed by a P5 removal.
//
// The gate is "at least one in flight", NOT "one per missing copy" as it is for tie-breakers. For a
// tie-breaker the deficit is structurally at most one, so the two formulas are indistinguishable
// there; a diskful deficit can be two (r1→r3, two copies lost out of four), and the per-deficit
// formula would put two replicas in flight before either became a member. There is nothing to win:
// all add-diskful plans sit in one transition group, which the concurrency tracker serialises per
// volume, so the second join waits regardless. There is something to lose: the second replica
// occupies a node (any RVR with a node counts as occupying it), takes a volume-group reservation
// and gets a logical volume, so capacity is allocated up front and sits idle.
//
// The in-flight term in step 6 keeps a pending creation visible when a configuration
// is widened and then reverted. Once a node appears the copy can join and overshoot
// the intended count, requiring a P5 removal; no dispatch guard refuses that join.
// Without a volume group the replica has no logical volume yet — only its identifier.
func computeTargetLayoutAction(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	rvas []*v1alpha1.ReplicatedVolumeAttachment,
) (targetLayoutAction, membershipLayoutConvergedReport) {
	// 1. Deletion: convergence is not evaluated, and no action is ever taken.
	if rv.DeletionTimestamp != nil {
		return targetLayoutAction{kind: layoutActionNone}, membershipLayoutConvergedReport{
			status:  metav1.ConditionUnknown,
			reason:  v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonVolumeDeleting,
			message: membershipLayoutConvergedVolumeDeletingMessage,
		}
	}

	intendedD, intendedTB := rv.Status.Configuration.IntendedLayout()
	actualD, actualTB := computeActualLayout(rv)
	actualLayout := formatLayout(actualD, actualTB)
	intendedLayout := formatLayout(intendedD, intendedTB)

	// The self-heal artifacts, and with them every step P3 owns.
	inFlightDiskful := countPendingDiskfulCreations(rv, rvrs)
	pendingRetypeToDiskful := pendingRetypeToDiskfulMemberNames(rv, rvrs)
	diskfulRemovalReport, diskfulRemovalPending := computeDiskfulRemovalPendingReport(
		rv, rvrs, actualD, intendedD, actualLayout, intendedLayout)

	// 2. A layout-changing transition is already moving the composition → no new action.
	if hasLayoutChangingTransition(rv) {
		if diskfulRemovalPending {
			return targetLayoutAction{kind: layoutActionNone}, diskfulRemovalReport
		}
		return targetLayoutAction{kind: layoutActionNone}, membershipLayoutConvergedReport{
			status:  metav1.ConditionFalse,
			reason:  v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverging,
			message: fmt.Sprintf("layout transition in progress: have %s, want %s", actualLayout, intendedLayout),
		}
	}

	if action, report, ok := computeTargetPendingPromotionRecovery(rv, rvrs); ok {
		return action, report
	}

	if action, report, ok := computeTargetTieBreakerPromotion(rv, rvrs); ok {
		return action, report
	}

	// 3. A retype already requested in a previous pass (spec flipped, DMTE not started yet).
	// ANY pending retype blocks Converged, whether or not the intended layout still shows a
	// tie-breaker deficit: the spec of a member is already flipped and the datamesh may act on
	// it at any moment, so "actual matches intended and nothing is moving" would be a lie.
	if pending := pendingRetypeToTieBreakerMemberNames(rv, rvrs); len(pending) > 0 {
		var message string
		switch {
		case actualTB < intendedTB:
			// The retype fills a genuine tie-breaker deficit — the layouts tell the whole story.
			message = fmt.Sprintf("retype to tie-breaker requested: have %s, want %s",
				actualLayout, intendedLayout)
		case actualD == intendedD && actualTB == intendedTB:
			// The deficit the retype was requested for is gone (typically the configuration was
			// switched back before the datamesh dispatched the transition). "have 3D, want 3D"
			// would say nothing here, so the message names the replica whose spec is flipped;
			// see the rv_controller README for the recovery recipes.
			message = fmt.Sprintf(
				"retype to tie-breaker pending on %s: the layout already matches the intended one (%s); "+
					"likely a reverted configuration change, and the retype is not rolled back automatically",
				strings.Join(pending, ", "), actualLayout)
		default:
			// A mismatch that needs no additional tie-breaker: the layouts are meaningful, but
			// the pending replica still has to be named.
			message = fmt.Sprintf("retype to tie-breaker pending on %s: have %s, want %s",
				strings.Join(pending, ", "), actualLayout, intendedLayout)
		}
		return targetLayoutAction{kind: layoutActionNone}, membershipLayoutConvergedReport{
			status:  metav1.ConditionFalse,
			reason:  v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverging,
			message: message,
		}
	}

	// 3b. The same check in the other direction: a member that is not a diskful voter while its
	// RVR spec.type already asks for Diskful. This is the vestibule of an add-diskful plan that
	// raises quorum (member Access, replica Diskful), or a retype requested by hand. It is
	// formally redundant — such a replica is in flight by the definition above, so no second copy
	// would be created anyway — and it is here for the message and for the symmetry a maintainer
	// looking at step 3 will expect to find.
	if len(pendingRetypeToDiskful) > 0 {
		var message string
		if actualD == intendedD && actualTB == intendedTB {
			// "have 2D+1TB, want 2D+1TB" would say nothing; name the replica instead (mirrors
			// the same branch of step 3).
			message = fmt.Sprintf(
				"retype to diskful pending on %s: the layout already matches the intended one (%s); "+
					"likely a reverted configuration change, and the retype is not rolled back automatically",
				strings.Join(pendingRetypeToDiskful, ", "), actualLayout)
		} else {
			message = fmt.Sprintf("retype to diskful pending on %s: have %s, want %s",
				strings.Join(pendingRetypeToDiskful, ", "), actualLayout, intendedLayout)
		}
		return targetLayoutAction{kind: layoutActionNone}, membershipLayoutConvergedReport{
			status:  metav1.ConditionFalse,
			reason:  v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverging,
			message: message,
		}
	}

	// 4. Tie-breaker replacement (strict create-first): a tie-breaker whose RVR is being deleted
	// still counts in the raw layout, but it is on its way out and must be replaced BEFORE the
	// datamesh releases it.
	if action, report, ok := computeTargetTieBreakerReplacement(
		rv, rvrs, actualD, actualTB, intendedD, intendedTB, actualLayout, intendedLayout,
	); ok {
		return action, report
	}

	// 5. A deleting counted copy is still a member, even when an operator deleted it.
	// Check after promotion/retype and TB replacement so their recovery steps retain priority,
	// but before equality and P4/P5: matching counts cannot mean Converged while a copy leaves,
	// and no second removal may race it. Real deficits fall through to P3 and its in-flight gate.
	if diskfulRemovalPending {
		return targetLayoutAction{kind: layoutActionNone}, diskfulRemovalReport
	}

	// 6. Converged: actual matches intended and no copy is still being created.
	// A pending creation at matching counts still needs its scheduling/progress report.
	if actualD == intendedD && actualTB == intendedTB {
		if inFlightDiskful > 0 {
			return targetLayoutAction{kind: layoutActionNone},
				computeDiskfulCreationPendingReport(rv, rvrs, actualLayout, intendedLayout, true)
		}
		return targetLayoutAction{kind: layoutActionNone}, membershipLayoutConvergedReport{
			status:  metav1.ConditionTrue,
			reason:  v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverged,
			message: fmt.Sprintf("layout converged: %s", actualLayout),
		}
	}

	switch {
	// P1 retype: too many diskful voters and a tie-breaker deficit.
	case actualD > intendedD && actualTB < intendedTB:
		name, noCandidateReason := selectRetypeCandidate(rv, rvrs, rvas)
		if name == "" {
			return targetLayoutAction{kind: layoutActionNone}, membershipLayoutConvergedReport{
				status: metav1.ConditionFalse,
				reason: v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonCannotConverge,
				message: fmt.Sprintf("cannot retype Diskful replica to tie-breaker (have %s, want %s): %s",
					actualLayout, intendedLayout, noCandidateReason),
			}
		}
		return targetLayoutAction{
			kind:          layoutActionRetypeToTieBreaker,
			retypeRVRName: name,
		}, membershipLayoutConvergedReport{
			status: metav1.ConditionFalse,
			reason: v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverging,
			message: fmt.Sprintf("retyping Diskful replica %s to tie-breaker: have %s, want %s",
				name, actualLayout, intendedLayout),
		}

	// P2 tie-breaker deficit: correct diskful count but a tie-breaker missing.
	case actualD == intendedD && actualTB < intendedTB:
		if countPendingTieBreakerCreations(rv, rvrs) >= intendedTB-actualTB {
			// The replica exists but cannot be placed: that is the scheduler's final word for
			// the current spec, not progress. Only a CURRENT Scheduled=False counts (see
			// computeActualPendingTieBreakerSchedulingFailure).
			if failure := computeActualPendingTieBreakerSchedulingFailure(rv, rvrs); failure != "" {
				return targetLayoutAction{kind: layoutActionNone}, membershipLayoutConvergedReport{
					status: metav1.ConditionFalse,
					reason: v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonCannotConverge,
					message: fmt.Sprintf("cannot place tie-breaker replica (have %s, want %s): %s",
						actualLayout, intendedLayout, failure),
				}
			}
			return targetLayoutAction{kind: layoutActionNone}, membershipLayoutConvergedReport{
				status:  metav1.ConditionFalse,
				reason:  v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverging,
				message: fmt.Sprintf("tie-breaker creation pending: have %s, want %s", actualLayout, intendedLayout),
			}
		}
		return targetLayoutAction{kind: layoutActionCreateTieBreaker}, membershipLayoutConvergedReport{
			status:  metav1.ConditionFalse,
			reason:  v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverging,
			message: fmt.Sprintf("creating tie-breaker replica: have %s, want %s", actualLayout, intendedLayout),
		}

	// P3 heal: a diskful copy is missing. The replica is created BLIND — with no node and no
	// storage — and the verdict on whether it can be placed is read back from the scheduler
	// (computeDiskfulCreationPendingReport). Mirroring the scheduler's admissibility check here
	// would mean re-implementing its whole phase pipeline (node readiness, volume-group readiness,
	// occupancy, zoning, extender, capacity), which would drift apart from the original on its
	// first change. That is the opposite choice from P1, which does mirror the DMTE guards —
	// deliberately: those guards are cheap and live in the same package.
	case actualD < intendedD:
		if inFlightDiskful > 0 {
			// The layouts differ here by construction, so they carry the progress themselves.
			return targetLayoutAction{kind: layoutActionNone},
				computeDiskfulCreationPendingReport(rv, rvrs, actualLayout, intendedLayout, false)
		}
		return targetLayoutAction{kind: layoutActionCreateDiskful}, membershipLayoutConvergedReport{
			status:  metav1.ConditionFalse,
			reason:  v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverging,
			message: fmt.Sprintf("creating diskful replica: have %s, want %s", actualLayout, intendedLayout),
		}

	// P4 tie-breaker surplus: the copies match the configuration and a tie-breaker is left over.
	// This removes an obsolete witness after add-first growth and is gated on
	// every counted copy being UpToDate, because until then the volume carries fewer copies of the
	// data than the configuration asks for (see computeTargetSurplusTieBreakerRemoval).
	case actualD == intendedD && actualTB > intendedTB:
		return computeTargetSurplusTieBreakerRemoval(
			rv, rvrs, rvas, intendedD, actualLayout, intendedLayout,
		)

	// P5: a copy surplus with no tie-breaker deficit. P1 owns the complementary case.
	case actualD > intendedD && actualTB >= intendedTB:
		return computeTargetSurplusDiskfulRemoval(rv, rvrs, rvas, actualLayout, intendedLayout)

	// Outside the whitelist: report honestly, take no action.
	default:
		return targetLayoutAction{kind: layoutActionNone}, membershipLayoutConvergedReport{
			status: metav1.ConditionFalse,
			reason: v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonTransitionUnsupported,
			message: fmt.Sprintf(
				"layout mismatch: have %s, want %s; automatic transition is not supported, manual intervention required",
				actualLayout, intendedLayout),
		}
	}
}

// departingVoterMemberNames returns the sorted names of voter members on their way out:
// deleting ones, whose RVR carries a deletion timestamp, and orphans, whose RVR is gone and
// which the datamesh force-removes on its own.
func departingVoterMemberNames(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
) (deleting, orphans []string) {
	for i := range rv.Status.Datamesh.Members {
		m := &rv.Status.Datamesh.Members[i]
		if !m.Type.IsVoter() {
			continue
		}
		switch rvr := findRVRByName(rvrs, m.Name); {
		case rvr == nil:
			orphans = append(orphans, m.Name)
		case rvr.DeletionTimestamp != nil:
			deleting = append(deleting, m.Name)
		}
	}
	slices.Sort(deleting)
	slices.Sort(orphans)
	return deleting, orphans
}

// computeDiskfulRemovalPendingReport owns departures of counted copies (D and D∅),
// including manual deletions. Counts stay raw until membership changes. A real
// deficit is left to P3; a surplus waits, and matching D counts need intervention
// because Kubernetes deletion cannot be cancelled by restoring the configuration.
func computeDiskfulRemovalPendingReport(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	actualD, intendedD int,
	actualLayout, intendedLayout string,
) (membershipLayoutConvergedReport, bool) {
	if actualD < intendedD {
		return membershipLayoutConvergedReport{}, false
	}
	leaving, _ := departingVoterMemberNames(rv, rvrs)
	if len(leaving) == 0 {
		return membershipLayoutConvergedReport{}, false
	}
	for i, name := range leaving {
		for _, request := range rv.Status.DatameshReplicaRequests {
			if request.Name == name && request.Message != "" {
				leaving[i] += ": " + request.Message
				break
			}
		}
	}
	slices.Sort(leaving)
	report := membershipLayoutConvergedReport{
		status: metav1.ConditionFalse,
		reason: v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverging,
		message: fmt.Sprintf("diskful replica %s is terminating: waiting for it to leave the datamesh (have %s, want %s)",
			strings.Join(leaving, "; "), actualLayout, intendedLayout),
	}
	if actualD == intendedD {
		report.reason = v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonCannotConverge
		report.message = fmt.Sprintf("diskful replica %s is terminating while the diskful count already matches the intended one (have %s, want %s); "+
			"likely a reverted configuration change or manual deletion; the deletion is not rolled back automatically",
			strings.Join(leaving, "; "), actualLayout, intendedLayout)
	}
	return report, true
}

// computeTargetSurplusDiskfulRemoval takes away one surplus copy under the new
// configuration. Departures are handled before the whitelist; orphans likewise
// hold P5 back because the datamesh will force-remove them without our help.
func computeTargetSurplusDiskfulRemoval(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	rvas []*v1alpha1.ReplicatedVolumeAttachment,
	actualLayout, intendedLayout string,
) (targetLayoutAction, membershipLayoutConvergedReport) {
	if _, orphans := departingVoterMemberNames(rv, rvrs); len(orphans) > 0 {
		return targetLayoutAction{kind: layoutActionNone}, membershipLayoutConvergedReport{
			status: metav1.ConditionFalse,
			reason: v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverging,
			message: fmt.Sprintf("diskful replica %s has no replica object: waiting for the datamesh to force-remove it (have %s, want %s)",
				strings.Join(orphans, ", "), actualLayout, intendedLayout),
		}
	}
	name, reason := selectSurplusDiskfulCandidate(rv, rvrs, rvas)
	if name == "" {
		return targetLayoutAction{kind: layoutActionNone}, membershipLayoutConvergedReport{
			status: metav1.ConditionFalse,
			reason: v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonCannotConverge,
			message: fmt.Sprintf("cannot remove surplus diskful replica (have %s, want %s): %s",
				actualLayout, intendedLayout, reason),
		}
	}
	return targetLayoutAction{kind: layoutActionRemoveDiskful, removeRVRName: name}, membershipLayoutConvergedReport{
		status: metav1.ConditionFalse,
		reason: v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverging,
		message: fmt.Sprintf("removing surplus diskful replica %s: have %s, want %s",
			name, actualLayout, intendedLayout),
	}
}

// selectSurplusDiskfulCandidate prefers a non-UpToDate copy, then the
// lexicographically last name. Only exact Diskful members with Diskful RVRs are
// eligible. Attachment (including active RVAs) is checked before the datamesh's
// own removal guards; no redundancy/quorum arithmetic is duplicated here.
// Inputs are read-only, and both the choice and refusal diagnostics are stable.
func selectSurplusDiskfulCandidate(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	rvas []*v1alpha1.ReplicatedVolumeAttachment,
) (name, reason string) {
	var upToDate, notUpToDate string
	var refusals []string
	for i := range rv.Status.Datamesh.Members {
		m := &rv.Status.Datamesh.Members[i]
		if m.Type != v1alpha1.DatameshMemberTypeDiskful {
			continue
		}
		rvr := findRVRByName(rvrs, m.Name)
		switch {
		case rvr == nil:
			refusals = append(refusals, m.Name+": replica object is missing")
			continue
		case rvr.Spec.Type != v1alpha1.ReplicaTypeDiskful:
			refusals = append(refusals, m.Name+": replica spec.type is not Diskful")
			continue
		case rvr.DeletionTimestamp != nil:
			refusals = append(refusals, m.Name+": replica is terminating")
			continue
		case isMemberAttached(m, rvas):
			refusals = append(refusals, m.Name+": replica is attached")
			continue
		}
		ready, isUpToDate, message := datamesh.DiskfulRemovalReadiness(rv, rvrs, m.Name)
		if !ready {
			refusals = append(refusals, m.Name+": "+message)
			continue
		}
		if isUpToDate {
			upToDate = max(upToDate, m.Name)
		} else {
			notUpToDate = max(notUpToDate, m.Name)
		}
	}
	if name := cmp.Or(notUpToDate, upToDate); name != "" {
		return name, ""
	}
	if len(refusals) == 0 {
		return "", "no Diskful members to remove"
	}
	slices.Sort(refusals)
	return "", strings.Join(refusals, "; ")
}

// computeTargetPendingPromotionRecovery handles changes to the input layout
// before DMTE starts a promotion. Empty storage intent can be cancelled after
// a target reversal. If an original voter was removed, repair that data copy
// first while the existing TB keeps its role and its prepared storage.
func computeTargetPendingPromotionRecovery(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
) (targetLayoutAction, membershipLayoutConvergedReport, bool) {
	if rv.Status.Configuration.FailuresToTolerate != 1 {
		return targetLayoutAction{}, membershipLayoutConvergedReport{}, false
	}
	var tb *v1alpha1.ReplicatedVolumeReplica
	for _, member := range rv.Status.Datamesh.Members {
		if member.Type != v1alpha1.DatameshMemberTypeTieBreaker {
			continue
		}
		rvr := findRVRByName(rvrs, member.Name)
		if rvr != nil && rvr.DeletionTimestamp == nil && rvr.Spec.Type == v1alpha1.ReplicaTypeDiskful {
			tb = rvr
			break
		}
	}
	if tb == nil {
		return targetLayoutAction{}, membershipLayoutConvergedReport{}, false
	}
	report := membershipLayoutConvergedReport{
		status: metav1.ConditionFalse,
		reason: v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverging,
	}
	if rv.Status.Configuration.GuaranteedMinimumDataRedundancy == 0 {
		// No LVG means the scheduler has not committed any storage or resync
		// reservation. The patch's resourceVersion protects that assumption
		// against a concurrent scheduler write on this same RVR.
		canCancel := tb.Spec.LVMVolumeGroupName == "" && tb.Spec.LVMVolumeGroupThinPoolName == "" && tb.Status.BackingVolume == nil
		for _, request := range rv.Status.DatameshReplicaRequests {
			if request.Name == tb.Name {
				canCancel = false
			}
		}
		if canCancel {
			report.message = fmt.Sprintf("cancelling unstarted tie-breaker promotion on %s after the target returned to R2", tb.Name)
			return targetLayoutAction{kind: layoutActionRetypeToTieBreaker, retypeRVRName: tb.Name}, report, true
		}
		report.reason = v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonCannotConverge
		report.message = fmt.Sprintf("target returned to R2 after storage preparation on %s; prepared promotion is not cancelled automatically", tb.Name)
		return targetLayoutAction{kind: layoutActionNone}, report, true
	}
	actualD, actualTB := computeActualLayout(rv)
	if rv.Status.Configuration.GuaranteedMinimumDataRedundancy != 1 || actualD >= 2 {
		return targetLayoutAction{}, membershipLayoutConvergedReport{}, false
	}
	// The TB's own spec is an in-flight copy, but cannot replace an original
	// voter yet. Exclude that one reservation when checking for a repair.
	others := make([]*v1alpha1.ReplicatedVolumeReplica, 0, len(rvrs)-1)
	for _, rvr := range rvrs {
		if rvr.Name != tb.Name {
			others = append(others, rvr)
		}
	}
	if countPendingDiskfulCreations(rv, others) > 0 {
		return targetLayoutAction{kind: layoutActionNone},
			computeDiskfulCreationPendingReport(rv, others, formatLayout(actualD, actualTB), "3D", false), true
	}
	report.message = "restoring the original diskful copies before continuing tie-breaker promotion"
	return targetLayoutAction{kind: layoutActionCreateDiskful}, report, true
}

// computeTargetTieBreakerPromotion prefers the existing TB for R2 -> R3. A
// pending Diskful spec on that same member is the durable reservation. Another
// diskful copy already in flight is never discarded to make room: before the
// reservation it keeps its existing path, after it the conflict is reported.
func computeTargetTieBreakerPromotion(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
) (targetLayoutAction, membershipLayoutConvergedReport, bool) {
	if !datamesh.IsR2ToR3Promotion(rv) {
		return targetLayoutAction{}, membershipLayoutConvergedReport{}, false
	}
	var tb *v1alpha1.ReplicatedVolumeReplica
	for _, member := range rv.Status.Datamesh.Members {
		if member.Type == v1alpha1.DatameshMemberTypeTieBreaker {
			tb = findRVRByName(rvrs, member.Name)
			break
		}
	}
	if tb == nil || tb.DeletionTimestamp != nil {
		return targetLayoutAction{}, membershipLayoutConvergedReport{}, false
	}
	report := membershipLayoutConvergedReport{
		status: metav1.ConditionFalse,
		reason: v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverging,
	}
	for _, rvr := range rvrs {
		if rvr.Name == tb.Name || !isDiskfulCreationInFlight(rv, rvr) {
			continue
		}
		if tb.Spec.Type != v1alpha1.ReplicaTypeDiskful {
			// Another copy is already on its way in. Let the add-first path
			// own it instead of reserving the TB as well.
			return targetLayoutAction{}, membershipLayoutConvergedReport{}, false
		}
		report.reason = v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonCannotConverge
		report.message = fmt.Sprintf("tie-breaker promotion on %s conflicts with diskful replica %s; neither replica is removed automatically", tb.Name, rvr.Name)
		return targetLayoutAction{kind: layoutActionNone}, report, true
	}
	if tb.Spec.Type == v1alpha1.ReplicaTypeDiskful {
		if condition := obju.GetStatusCondition(tb, v1alpha1.ReplicatedVolumeReplicaCondScheduledType); condition != nil && condition.ObservedGeneration == tb.Generation && condition.Status == metav1.ConditionFalse {
			report.reason = v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonCannotConverge
			report.message = fmt.Sprintf("cannot prepare disk on tie-breaker %s: %s", tb.Name, condition.Message)
		} else if condition != nil && condition.ObservedGeneration == tb.Generation &&
			condition.Reason == v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonWaitingForResyncSlot {
			report.message = fmt.Sprintf("tie-breaker promotion on %s is waiting for a free resync slot: %s", tb.Name, condition.Message)
		} else {
			report.message = fmt.Sprintf("preparing in-place tie-breaker promotion on %s: have 2D+1TB, want 3D", tb.Name)
		}
		return targetLayoutAction{kind: layoutActionNone}, report, true
	}
	if tb.Spec.Type != v1alpha1.ReplicaTypeTieBreaker {
		return targetLayoutAction{}, membershipLayoutConvergedReport{}, false
	}
	if ready, message := datamesh.TieBreakerPromotionReadiness(rv, rvrs, tb.Name); !ready {
		report.message = "waiting to promote tie-breaker: " + message
		return targetLayoutAction{kind: layoutActionNone}, report, true
	}
	report.message = fmt.Sprintf("promoting tie-breaker %s in place: have 2D+1TB, want 3D", tb.Name)
	return targetLayoutAction{kind: layoutActionRetypeToDiskful, retypeRVRName: tb.Name}, report, true
}

// computeTargetTieBreakerReplacement decides what convergence does about tie-breaker members
// that are leaving (their RVR is being deleted), and returns (action, report, true) when the
// tie-breaker replacement domain owns this pass. It returns (_, _, false) when nothing is leaving
// or when the situation is outside its scope, letting the caller fall through to the plain
// actual/intended comparison. Like its caller it keeps the target and the MembershipLayoutConverged report
// as separate outputs.
//
// Strict create-first: the datamesh only releases the old tie-breaker once its replacement is
// operational (see the datamesh guard guardTBSufficient), so the replacement must be created
// while the old one is still a member. The raw layout counts the terminating tie-breaker, so the
// deficit is computed SEPARATELY here, over non-deleting tie-breakers only; computeActualLayout
// stays raw and status.membershipLayout keeps reporting the honest composition (2D+2TB in the replacement
// window).
//
// State table:
//
//	old leaving, no replacement                  → create it (Converging)
//	old leaving, replacement pending             → Converging (or CannotConverge on a current
//	                                               Scheduled=False: the old tie-breaker keeps
//	                                               working, the replacement RVR keeps waiting
//	                                               for a free eligible node)
//	old leaving, replacement joined the datamesh → Converging (the DMTE guard releases the old
//	                                               one once the replacement is operational)
//	old leaving, no replacement needed           → Converging (the departure alone settles the
//	                                               layout, or leaves a surplus for P4 to pick up
//	                                               on the pass after it)
//	old gone                                     → not our business (the caller compares layouts)
//
// Scope: every departure at a correct diskful count belongs here, including one that leaves a
// tie-breaker surplus behind. That is deliberate rather than incidental: owning the whole
// departure window is what keeps P4 from removing a second tie-breaker while the first is still
// leaving, without P4 having to track departures of its own. A wrong diskful count
// (actualD != intendedD) is left to the caller, which heals the copy first.
func computeTargetTieBreakerReplacement(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	actualD, actualTB, intendedD, intendedTB int,
	actualLayout, intendedLayout string,
) (targetLayoutAction, membershipLayoutConvergedReport, bool) {
	leaving := deletingTieBreakerMemberNames(rv, rvrs)
	if len(leaving) == 0 || actualD != intendedD {
		return targetLayoutAction{}, membershipLayoutConvergedReport{}, false
	}

	// Tie-breakers that stay: the supply the intended layout can rely on.
	availableTB := actualTB - len(leaving)

	subject := fmt.Sprintf("tie-breaker %s is terminating", strings.Join(leaving, ", "))
	if len(leaving) > 1 {
		subject = fmt.Sprintf("tie-breakers %s are terminating", strings.Join(leaving, ", "))
	}
	layouts := fmt.Sprintf("have %s, want %s", actualLayout, intendedLayout)

	if availableTB >= intendedTB {
		// The remaining tie-breakers already cover the intended layout — either a replacement
		// joined the datamesh, or none is needed at all, or one too many is left over. Releasing
		// the leaving member is the DMTE's decision (it waits until a replacement is operational),
		// so there is nothing to do here but report progress. Owning the surplus case too is what
		// keeps P4 from removing a SECOND tie-breaker while the first one is still on its way out:
		// the departure already reduces the count, and the next pass sees the result.
		waiting := "waiting for it to leave the datamesh"
		switch {
		case len(leaving) > 1:
			waiting = "waiting for them to leave the datamesh"
		case availableTB == intendedTB && intendedTB > 0:
			waiting = "its replacement joined the datamesh, waiting for it to leave"
		}
		return targetLayoutAction{kind: layoutActionNone}, membershipLayoutConvergedReport{
			status:  metav1.ConditionFalse,
			reason:  v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverging,
			message: fmt.Sprintf("%s: %s (%s)", subject, waiting, layouts),
		}, true
	}

	deficit := intendedTB - availableTB
	if countPendingTieBreakerCreations(rv, rvrs) >= deficit {
		// The replacement RVR exists but has not joined yet. Only a CURRENT Scheduled=False is
		// the scheduler's verdict for this spec (see computeActualPendingTieBreakerSchedulingFailure):
		// with every eligible node occupied the replacement cannot be placed, and strict
		// create-first keeps the terminating tie-breaker working instead of releasing it.
		if failure := computeActualPendingTieBreakerSchedulingFailure(rv, rvrs); failure != "" {
			return targetLayoutAction{kind: layoutActionNone}, membershipLayoutConvergedReport{
				status: metav1.ConditionFalse,
				reason: v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonCannotConverge,
				message: fmt.Sprintf("%s: cannot place a replacement (%s): %s",
					subject, layouts, failure),
			}, true
		}
		return targetLayoutAction{kind: layoutActionNone}, membershipLayoutConvergedReport{
			status:  metav1.ConditionFalse,
			reason:  v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverging,
			message: fmt.Sprintf("%s: replacement tie-breaker creation pending (%s)", subject, layouts),
		}, true
	}

	return targetLayoutAction{kind: layoutActionCreateTieBreaker}, membershipLayoutConvergedReport{
		status:  metav1.ConditionFalse,
		reason:  v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverging,
		message: fmt.Sprintf("%s: creating a replacement (%s)", subject, layouts),
	}, true
}

// deletingTieBreakerMemberNames returns the sorted names of TieBreaker members whose RVR is being
// deleted — the tie-breakers that need a replacement before the datamesh may release them.
//
// A member whose RVR is gone entirely is NOT one of them: that is an orphan, force-removed by the
// datamesh without any tie-breaker guard, and the plain deficit path then heals the layout. Adding
// it here would start a replacement in parallel with the force-removal.
func deletingTieBreakerMemberNames(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
) []string {
	var names []string
	for i := range rv.Status.Datamesh.Members {
		m := &rv.Status.Datamesh.Members[i]
		if m.Type != v1alpha1.DatameshMemberTypeTieBreaker {
			continue
		}
		if rvr := findRVRByName(rvrs, m.Name); rvr != nil && rvr.DeletionTimestamp != nil {
			names = append(names, m.Name)
		}
	}
	slices.Sort(names)
	return names
}

// computeTargetSurplusTieBreakerRemoval decides what convergence does about a tie-breaker surplus
// at a correct diskful count (P4), and is the second half of the upsize r2→r3: P3 brings the copy
// in, the counts level, and this step takes the tie-breaker the configuration no longer asks for
// away. It produces the target and the MembershipLayoutConverged report as separate outputs, like
// its caller.
//
// It is only reached with no tie-breaker leaving (computeTargetTieBreakerReplacement owns the whole
// departure window at a correct diskful count) and with a genuine surplus.
//
// A tie-breaker holds no data; P2 creates one back if the configuration asks for it
// again. Surplus data copies have their own guarded removal path in P5.
//
// Three gates stand before the action, and each of them reports Converging rather than
// CannotConverge, because each is a state that resolves itself:
//
//   - UpToDate. The removal waits until every counted copy is UpToDate
//     (status.effectiveLayout.upToDateVoters, the same count the resync limiter reads). Quorum does
//     not ask for this — the datamesh guard guardTBSufficient releases the tie-breaker as soon as
//     the voter count turns odd, which happens when the new copy JOINS, long before its resync ends
//     — so without this gate the tie-breaker would be dropped from a volume carrying two copies of
//     the data out of three. The gate lives here and not in the datamesh guards on purpose: it
//     protects the automatic path only, and a human deleting a tie-breaker by hand keeps doing so
//     under the quorum arithmetic alone.
//   - An admissible candidate. An orphaned surplus tie-breaker (a member with no replica object)
//     holds the whole pattern back — see selectSurplusTieBreakerCandidate.
//   - Attachment. A tie-breaker the datamesh reports Attached (or whose node carries an active RVA)
//     would have its Leave blocked by guardNotAttached. That is a transient state, so it is
//     reported as Converging and waited out rather than called terminal.
func computeTargetSurplusTieBreakerRemoval(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	rvas []*v1alpha1.ReplicatedVolumeAttachment,
	intendedD int,
	actualLayout, intendedLayout string,
) (targetLayoutAction, membershipLayoutConvergedReport) {
	layouts := fmt.Sprintf("have %s, want %s", actualLayout, intendedLayout)

	if upToDate := int(rv.Status.EffectiveLayout.UpToDateVoters); upToDate < intendedD {
		return targetLayoutAction{kind: layoutActionNone}, membershipLayoutConvergedReport{
			status: metav1.ConditionFalse,
			reason: v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverging,
			message: fmt.Sprintf(
				"waiting for the diskful copies to synchronize before removing a surplus tie-breaker "+
					"(%d of %d UpToDate): %s", upToDate, intendedD, layouts),
		}
	}

	name, waiting := selectSurplusTieBreakerCandidate(rv, rvrs)
	if name == "" {
		return targetLayoutAction{kind: layoutActionNone}, membershipLayoutConvergedReport{
			status:  metav1.ConditionFalse,
			reason:  v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverging,
			message: fmt.Sprintf("%s (%s)", waiting, layouts),
		}
	}

	if m := rv.Status.Datamesh.FindMemberByName(name); m != nil && isMemberAttached(m, rvas) {
		return targetLayoutAction{kind: layoutActionNone}, membershipLayoutConvergedReport{
			status: metav1.ConditionFalse,
			reason: v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverging,
			message: fmt.Sprintf("surplus tie-breaker %s is attached: waiting for it to detach (%s)",
				name, layouts),
		}
	}

	return targetLayoutAction{
		kind:          layoutActionRemoveTieBreaker,
		removeRVRName: name,
	}, membershipLayoutConvergedReport{
		status:  metav1.ConditionFalse,
		reason:  v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverging,
		message: fmt.Sprintf("removing surplus tie-breaker %s: %s", name, layouts),
	}
}

// selectSurplusTieBreakerCandidate deterministically chooses the tie-breaker member P4 removes, or
// returns ("", waiting) when none may be removed this pass (the waiting text feeds the Converging
// message, mirroring selectRetypeCandidate's (name, reason)).
//
// Preference order:
//  1. a tie-breaker that is not operational right now (datamesh.IsTieBreakerOperational — the very
//     criterion guardTBSufficient and formation apply): the volume parts with the worst of them,
//     not with an arbitrary one;
//  2. otherwise the lexicographically last member name, the same arbitrary-but-stable rule
//     selectRetypeCandidate uses.
//
// Determinism matters more than cleverness here: a repeated pass MUST pick the same candidate, or
// convergence would delete tie-breakers in turn. Operationality is a LIVE property (revision,
// DRBDConfigured, peer connections), so the ranking can move between passes while nothing has been
// removed yet — the volume then parts with whichever tie-breaker is the worse one at the moment it
// acts, which is the intent. It cannot cost a second removal: once one is deleted, the replacement
// domain owns every pass until it has left, and by then the surplus is gone.
//
// A member with no replica object at all is an ORPHAN and never a candidate: the datamesh
// force-removes it without any tie-breaker guard, and deleting a working one in parallel would take
// the count below the intended one and hand it straight back to P2. One orphan therefore holds the
// whole pattern back, which is what the first waiting text says.
//
// A tie-breaker whose RVR is already being deleted needs no place in the order either: the caller is
// not reached while one is leaving (the replacement domain owns that window), which is exactly "a
// removal already chosen by hand is the chosen candidate".
func selectSurplusTieBreakerCandidate(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
) (name, waiting string) {
	peers := computeActualTieBreakerPeers(rv, rvrs)

	var orphans []string
	var operational, notOperational string
	for i := range rv.Status.Datamesh.Members {
		m := &rv.Status.Datamesh.Members[i]
		if m.Type != v1alpha1.DatameshMemberTypeTieBreaker {
			continue
		}
		rvr := findRVRByName(rvrs, m.Name)
		if rvr == nil {
			orphans = append(orphans, m.Name)
			continue
		}
		if ok, _ := datamesh.IsTieBreakerOperational(rvr, peers, rv.Status.DatameshRevision); !ok {
			if m.Name > notOperational {
				notOperational = m.Name
			}
			continue
		}
		if m.Name > operational {
			operational = m.Name
		}
	}

	if len(orphans) > 0 {
		slices.Sort(orphans)
		return "", fmt.Sprintf("tie-breaker %s has no replica: waiting for the datamesh to force-remove it",
			strings.Join(orphans, ", "))
	}
	if notOperational != "" {
		return notOperational, ""
	}
	if operational != "" {
		return operational, ""
	}
	// Unreachable from the caller, which is only entered on a genuine tie-breaker surplus: with no
	// orphans left, a surplus means at least one tie-breaker member carries a replica. Reported
	// rather than asserted, so a future caller cannot turn it into a silently idle pass.
	return "", "no tie-breaker replica to remove"
}

// computeActualTieBreakerPeers returns the peers a tie-breaker is expected to be connected to: the
// data-bearing (full-mesh) members, resolved exactly as the datamesh engine resolves them. A member
// whose replica object is missing stays in the list (nil RVR) so that its connection still has to be
// confirmed by the tie-breaker's own fresh report.
//
// Shared by formation (computeActualTieBreakerReadiness) and by the P4 candidate selection, which
// must ask "is this tie-breaker operational?" the way guardTBSufficient asks it.
func computeActualTieBreakerPeers(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
) []datamesh.TieBreakerPeer {
	peers := make([]datamesh.TieBreakerPeer, 0, len(rv.Status.Datamesh.Members))
	for i := range rv.Status.Datamesh.Members {
		m := &rv.Status.Datamesh.Members[i]
		if !m.Type.ConnectsToAllPeers() {
			continue
		}
		peers = append(peers, datamesh.TieBreakerPeer{
			ID:   m.ID(),
			Name: m.Name,
			RVR:  findRVRByName(rvrs, m.Name),
		})
	}
	return peers
}

// selectRetypeCandidate deterministically chooses the Diskful replica to retype into a
// tie-breaker for a P1 convergence, or returns ("", reason) when none is admissible (the reason
// feeds the CannotConverge message).
//
// A Diskful member is an admissible candidate when:
//   - its RVR spec.type is still Diskful (not already requested to change),
//   - it is not attached — neither member.Attached nor an active RVA on its node (retyping an
//     attached replica would disrupt live I/O),
//   - its node passes the tie-breaker placement precondition mirroring the DMTE gain-TB guards
//     (a mismatch would flip the spec to TieBreaker but leave the DMTE unable to run the
//     ChangeRole transition, wedging the volume in a misleading Converging state):
//   - TransZonal (guardTransZonalTBPlacement): the member's zone must hold at most one diskful
//     voter.
//   - Zonal (guardZonalSameZone): the member's zone must be a primary zone — one with the
//     maximum diskful-voter count (ties are all acceptable).
//   - for TransZonal, the retype also passes the DMTE lose-side zone quorum precondition
//     (mirrors guardZoneFTTPreservedForRetypeToTieBreaker — see
//     isRetypeToTieBreakerZoneQuorumSafe). Without this mirror a legitimately
//     non-convergible layout (e.g. two zones holding 2D and 1D) would pick a candidate whose
//     dispatch stays blocked forever, reporting Converging instead of CannotConverge.
//
// Among admissible candidates the lexicographically last RVR name is chosen (arbitrary but stable
// and deterministic).
func selectRetypeCandidate(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	rvas []*v1alpha1.ReplicatedVolumeAttachment,
) (name, reason string) {
	topology := rv.Status.Configuration.Topology
	transZonal := topology == v1alpha1.TopologyTransZonal
	zonal := topology == v1alpha1.TopologyZonal

	// Diskful voters and tie-breakers per zone (mirrors voterCountPerZone / tbCountPerZone used
	// by the DMTE zone guards).
	var (
		votersPerZone = map[string]int{}
		tbPerZone     = map[string]int{}
		voters        int
		totalTB       int
	)
	if transZonal || zonal {
		for i := range rv.Status.Datamesh.Members {
			m := &rv.Status.Datamesh.Members[i]
			switch {
			case m.Type.IsVoter():
				votersPerZone[m.Zone]++
				voters++
			case m.Type == v1alpha1.DatameshMemberTypeTieBreaker:
				tbPerZone[m.Zone]++
				totalTB++
			}
		}
	}
	// For Zonal, a voter gain (including a tie-breaker) must land in a primary zone — one with the
	// maximum voter count (mirrors guardZonalSameZone). Precompute that maximum once.
	maxZoneVoters := 0
	if zonal {
		for _, c := range votersPerZone {
			if c > maxZoneVoters {
				maxZoneVoters = c
			}
		}
	}

	var (
		chosen              string
		sawDiskful          bool
		sawSpecDiskful      bool
		sawUnattachedSpecDF bool
		sawZonePlacementOK  bool
	)
	for i := range rv.Status.Datamesh.Members {
		m := &rv.Status.Datamesh.Members[i]
		if m.Type != v1alpha1.DatameshMemberTypeDiskful {
			continue
		}
		sawDiskful = true

		rvr := findRVRByName(rvrs, m.Name)
		if rvr == nil || rvr.Spec.Type != v1alpha1.ReplicaTypeDiskful {
			continue
		}
		sawSpecDiskful = true

		if isMemberAttached(m, rvas) {
			continue
		}
		sawUnattachedSpecDF = true

		// Gain side: zone placement precondition (mirrors the DMTE gain-TB guards).
		if transZonal && votersPerZone[m.Zone] > 1 {
			continue
		}
		if zonal && maxZoneVoters > 0 && votersPerZone[m.Zone] != maxZoneVoters {
			continue
		}
		sawZonePlacementOK = true

		// Lose side: zone quorum precondition (mirrors the DMTE retype-aware zone FTT guard).
		if transZonal && !isRetypeToTieBreakerZoneQuorumSafe(m.Zone, votersPerZone, tbPerZone, voters, totalTB) {
			continue
		}

		if m.Name > chosen {
			chosen = m.Name
		}
	}

	if chosen != "" {
		return chosen, ""
	}

	// Classify why no candidate is admissible (for the CannotConverge message).
	switch {
	case !sawDiskful:
		return "", "no diskful replicas found"
	case !sawSpecDiskful:
		return "", "all diskful replicas are already being retyped"
	case !sawUnattachedSpecDF:
		return "", "all diskful replicas are attached"
	case !sawZonePlacementOK:
		return "", "no diskful replica can become a tie-breaker without violating zone placement"
	default:
		return "", "no diskful replica can become a tie-breaker: after the retype, losing a zone would lose quorum"
	}
}

// isRetypeToTieBreakerZoneQuorumSafe reports whether retyping the diskful voter in subjectZone
// into a tie-breaker keeps quorum survivable for the loss of any zone. It mirrors the DMTE
// guard guardZoneFTTPreservedForRetypeToTieBreaker so that preselection never hands the
// convergence loop a candidate whose transition the guard would block forever.
//
// The subject does not disappear: it stays a quorum participant as a tie-breaker in its own zone,
// so it counts as a surviving tie-breaker for every zone except its own (that future tie-breaker
// dies together with its zone).
//
// Only meaningful for TransZonal; callers gate on the topology.
func isRetypeToTieBreakerZoneQuorumSafe(
	subjectZone string,
	votersPerZone, tbPerZone map[string]int,
	voters, totalTB int,
) bool {
	if voters == 0 {
		return true
	}
	votersAfter := voters - 1
	qAfter := votersAfter/2 + 1

	for zone, zoneVoters := range votersPerZone {
		adjustedZoneVoters := zoneVoters
		if zone == subjectZone && adjustedZoneVoters > 0 {
			adjustedZoneVoters--
		}
		dSurviving := votersAfter - adjustedZoneVoters
		tbSurviving := totalTB - tbPerZone[zone]
		if zone != subjectZone {
			tbSurviving++
		}

		if dSurviving >= qAfter {
			continue
		}
		if dSurviving == qAfter-1 && tbSurviving > 0 {
			continue
		}
		return false
	}
	return true
}

// computeActualPendingTieBreakerSchedulingFailure reports the scheduler's CURRENT verdict for
// tie-breaker RVRs that exist but have not joined the datamesh yet (see
// computeActualPendingSchedulingFailure for the verdict rule).
func computeActualPendingTieBreakerSchedulingFailure(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
) string {
	return computeActualPendingSchedulingFailure(rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		return isTieBreakerCreationInFlight(rv, rvr)
	})
}

// computeActualPendingSchedulingFailure reports the scheduler's CURRENT verdict for the replicas
// inFlight selects, as a non-empty human-readable summary, or "" when it has not ruled against any
// of them. Shared by the tie-breaker and the diskful patterns, which differ only in what counts as
// in flight.
//
// Only a Scheduled=False whose ObservedGeneration matches the RVR generation is a verdict. A
// missing, Unknown or stale Scheduled condition means the scheduler simply has not (re-)evaluated
// this replica yet, so convergence is still in progress — which is also what keeps a placement the
// resync limiter deferred (published as Unknown) out of CannotConverge.
func computeActualPendingSchedulingFailure(
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	inFlight func(*v1alpha1.ReplicatedVolumeReplica) bool,
) string {
	var msgs []string
	for _, rvr := range rvrs {
		if !inFlight(rvr) {
			continue
		}
		if !obju.StatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondScheduledType).
			IsFalse().ObservedGenerationCurrent().Eval() {
			continue
		}
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondScheduledType)
		detail := cond.Message
		if detail == "" {
			detail = cond.Reason
		}
		msg := fmt.Sprintf("%s: %s", rvr.Name, detail)
		if !slices.Contains(msgs, msg) {
			msgs = append(msgs, msg)
		}
	}
	slices.Sort(msgs)
	return strings.Join(msgs, " | ")
}

// applyRVRRetypeToTieBreaker applies the P1 retype target to the RVR main patch domain: it flips
// spec.type to TieBreaker and clears the backing-volume fields that only a Diskful replica may
// carry.
//
// Both writes belong to the SAME patch: the API rejects backing-volume fields on a non-Diskful
// replica ("lvmVolumeGroupName can only be set for Diskful type", see
// ReplicatedVolumeReplicaSpec), so a patch that only flips the type never lands and the migration
// retries forever. This matches the datamesh plan, which clears the backing volume on the D∅ → TB
// step. The LLV survives the clearing: for a datamesh member the intended backing volume is
// derived from the member record, not from the RVR spec, so it is kept until the member actually
// leaves (see computeIntendedBackingVolume).
func applyRVRRetypeToTieBreaker(rvr *v1alpha1.ReplicatedVolumeReplica) {
	rvr.Spec.Type = v1alpha1.ReplicaTypeTieBreaker
	rvr.Spec.LVMVolumeGroupName = ""
	rvr.Spec.LVMVolumeGroupThinPoolName = ""
}

// isMemberAttached reports whether the given member is currently attached: either the datamesh
// member is marked Attached (published / DRBD Primary) or an active (non-deleting) RVA targets its
// node. Attached replicas are excluded as retype and diskful-removal candidates to protect live I/O; an
// attached tie-breaker has its removal deferred (the datamesh guard guardNotAttached would block
// the Leave anyway).
func isMemberAttached(m *v1alpha1.DatameshMember, rvas []*v1alpha1.ReplicatedVolumeAttachment) bool {
	if m.Attached {
		return true
	}
	for _, rva := range rvas {
		if rva.DeletionTimestamp == nil && rva.Spec.NodeName == m.NodeName {
			return true
		}
	}
	return false
}

// pendingRetypeToTieBreakerMemberNames returns the sorted names of diskful members whose RVR
// spec.type has already been flipped to TieBreaker (a retype requested in a previous pass, not
// yet reflected in the member type). Used to avoid issuing a second retype while the first is in
// flight, and to name the replica in the Converging message.
func pendingRetypeToTieBreakerMemberNames(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
) []string {
	var names []string
	for i := range rv.Status.Datamesh.Members {
		m := &rv.Status.Datamesh.Members[i]
		if !isDiskfulLayoutMemberType(m.Type) {
			continue
		}
		if rvr := findRVRByName(rvrs, m.Name); rvr != nil && rvr.Spec.Type == v1alpha1.ReplicaTypeTieBreaker {
			names = append(names, m.Name)
		}
	}
	slices.Sort(names)
	return names
}

// countPendingTieBreakerCreations counts TieBreaker RVRs that are not yet datamesh members
// (created in a previous pass, still being scheduled / joining). Used to avoid creating a second
// tie-breaker while the first is in flight.
func countPendingTieBreakerCreations(rv *v1alpha1.ReplicatedVolume, rvrs []*v1alpha1.ReplicatedVolumeReplica) int {
	count := 0
	for _, rvr := range rvrs {
		if isTieBreakerCreationInFlight(rv, rvr) {
			count++
		}
	}
	return count
}

// isTieBreakerCreationInFlight reports whether rvr is a tie-breaker on its way INTO the datamesh:
// its spec asks for TieBreaker, it is not being deleted, and it is not a member yet.
//
// Defined over MEMBERSHIP, which the diskful counterpart deliberately is not — see
// isDiskfulCreationInFlight for why the two formulas differ and must not be unified. A tie-breaker
// RVR being deleted is not in flight at all: it is on its way out and will never satisfy the
// deficit, so it must not hold back a new creation.
func isTieBreakerCreationInFlight(
	rv *v1alpha1.ReplicatedVolume,
	rvr *v1alpha1.ReplicatedVolumeReplica,
) bool {
	return rvr.Spec.Type == v1alpha1.ReplicaTypeTieBreaker &&
		rvr.DeletionTimestamp == nil &&
		rv.Status.Datamesh.FindMemberByName(rvr.Name) == nil
}

// pendingRetypeToDiskfulMemberNames returns the sorted names of members that are not diskful
// voters while their RVR spec.type already asks for Diskful — the mirror image of
// pendingRetypeToTieBreakerMemberNames.
//
// Two states land here: the vestibule of an add-diskful plan that raises quorum (the member enters
// as Access before it becomes D∅), and a retype requested by hand. Convergence itself never
// requests one, so this only ever reports.
func pendingRetypeToDiskfulMemberNames(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
) []string {
	var names []string
	for i := range rv.Status.Datamesh.Members {
		m := &rv.Status.Datamesh.Members[i]
		if isDiskfulLayoutMemberType(m.Type) {
			continue
		}
		if rvr := findRVRByName(rvrs, m.Name); rvr != nil && rvr.Spec.Type == v1alpha1.ReplicaTypeDiskful {
			names = append(names, m.Name)
		}
	}
	slices.Sort(names)
	return names
}

// countPendingDiskfulCreations counts the Diskful RVRs the layout does not count yet — the copies
// in flight (see isDiskfulCreationInFlight). Used to keep convergence from issuing a second
// creation for a deficit it is already working on.
func countPendingDiskfulCreations(rv *v1alpha1.ReplicatedVolume, rvrs []*v1alpha1.ReplicatedVolumeReplica) int {
	count := 0
	for _, rvr := range rvrs {
		if isDiskfulCreationInFlight(rv, rvr) {
			count++
		}
	}
	return count
}

// isDiskfulCreationInFlight reports whether rvr is a diskful copy on its way INTO the layout: its
// spec asks for Diskful, it is not being deleted, and the layout does not count it — either it has
// no member at all, or its member type is neither Diskful nor LiminalDiskful.
//
// Defined over the LAYOUT, not over membership. "Not a member" — the formula the tie-breaker
// pattern uses — is wrong here: the vestibule of a quorum-raising add-diskful plan IS a member
// (as Access) while the layout still does not count it, and reading it as idle costs the volume a
// copy too many. A replica being deleted is not in flight at all: it will never satisfy the
// deficit, so it must not hold back the heal either.
func isDiskfulCreationInFlight(
	rv *v1alpha1.ReplicatedVolume,
	rvr *v1alpha1.ReplicatedVolumeReplica,
) bool {
	if rvr.Spec.Type != v1alpha1.ReplicaTypeDiskful || rvr.DeletionTimestamp != nil {
		return false
	}
	m := rv.Status.Datamesh.FindMemberByName(rvr.Name)
	return m == nil || !isDiskfulLayoutMemberType(m.Type)
}

// computeDiskfulCreationPendingReport reports a diskful copy that is already in flight, and is the
// single place that classifies what the volume is waiting for:
//
//	the scheduler cannot place it (a CURRENT Scheduled=False) → CannotConverge, a human is needed
//	the resync limiter deferred it (Scheduled=Unknown/WaitingForResyncSlot) → Converging, and the
//	  message says so: the reason is right (a queued slot IS progress) but "creation pending" alone
//	  would read as a scheduler that cannot place the copy
//	anything else (no condition, stale, Scheduled=True, waiting to join) → Converging
//
// layoutMatchesIntended selects the message for the reverted-configuration case, where the copy is
// in flight while the counts already match: "have 2D+1TB, want 2D+1TB" says nothing, so the
// replica is named instead (mirrors the same branch of step 3 of the decision order).
func computeDiskfulCreationPendingReport(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	actualLayout, intendedLayout string,
	layoutMatchesIntended bool,
) membershipLayoutConvergedReport {
	if failure := computeActualPendingDiskfulSchedulingFailure(rv, rvrs); failure != "" {
		return membershipLayoutConvergedReport{
			status: metav1.ConditionFalse,
			reason: v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonCannotConverge,
			message: fmt.Sprintf("cannot place diskful replica (have %s, want %s): %s",
				actualLayout, intendedLayout, failure),
		}
	}
	if waiting := computeActualPendingDiskfulResyncSlotWait(rv, rvrs); waiting != "" {
		return membershipLayoutConvergedReport{
			status: metav1.ConditionFalse,
			reason: v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverging,
			message: fmt.Sprintf("diskful replica %s is waiting for a free resync slot: have %s, want %s",
				waiting, actualLayout, intendedLayout),
		}
	}
	if layoutMatchesIntended {
		return membershipLayoutConvergedReport{
			status: metav1.ConditionFalse,
			reason: v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverging,
			message: fmt.Sprintf(
				"diskful replica creation pending on %s: the layout already matches the intended one (%s); "+
					"likely a reverted configuration change, and the replica is not removed automatically",
				pendingDiskfulCreationNames(rv, rvrs), actualLayout),
		}
	}
	return membershipLayoutConvergedReport{
		status:  metav1.ConditionFalse,
		reason:  v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverging,
		message: fmt.Sprintf("diskful replica creation pending: have %s, want %s", actualLayout, intendedLayout),
	}
}

// pendingDiskfulCreationNames returns the sorted names of the diskful copies in flight, joined for
// a condition message. Only used where the layout arithmetic carries no information.
func pendingDiskfulCreationNames(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
) string {
	var names []string
	for _, rvr := range rvrs {
		if isDiskfulCreationInFlight(rv, rvr) {
			names = append(names, rvr.Name)
		}
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}

// computeActualPendingDiskfulSchedulingFailure reports the scheduler's CURRENT verdict for diskful
// copies in flight (see computeActualPendingSchedulingFailure for the verdict rule).
func computeActualPendingDiskfulSchedulingFailure(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
) string {
	return computeActualPendingSchedulingFailure(rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		return isDiskfulCreationInFlight(rv, rvr)
	})
}

// computeActualPendingDiskfulResyncSlotWait returns the names of the diskful copies in flight whose
// placement the resync limiter has deferred, or "" when none is waiting. The limiter publishes the
// wait as Scheduled=Unknown/WaitingForResyncSlot, which is deliberately not a verdict, so the
// condition reason stays Converging and only the message changes. Because the pattern is gated on
// the limiter being on, this is the state every early user meets first.
func computeActualPendingDiskfulResyncSlotWait(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
) string {
	var names []string
	for _, rvr := range rvrs {
		if !isDiskfulCreationInFlight(rv, rvr) {
			continue
		}
		if !obju.StatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondScheduledType).
			StatusEqual(metav1.ConditionUnknown).
			ReasonEqual(v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonWaitingForResyncSlot).
			ObservedGenerationCurrent().Eval() {
			continue
		}
		names = append(names, rvr.Name)
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}

// isDiskfulLayoutMemberType reports whether a member of this type is counted as a diskful voter by
// the layout (see computeActualLayout): a data-bearing voter, or one whose disk is not attached yet.
func isDiskfulLayoutMemberType(t v1alpha1.DatameshMemberType) bool {
	return t == v1alpha1.DatameshMemberTypeDiskful || t == v1alpha1.DatameshMemberTypeLiminalDiskful
}

// findRVRByName returns the RVR with the given name, or nil if absent.
func findRVRByName(rvrs []*v1alpha1.ReplicatedVolumeReplica, name string) *v1alpha1.ReplicatedVolumeReplica {
	for _, rvr := range rvrs {
		if rvr.Name == name {
			return rvr
		}
	}
	return nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: metadata
//

// reconcileMetadata reconciles the RV main-domain metadata (finalizer and labels).
//
// Reconcile pattern: Target-state driven
func (r *Reconciler) reconcileMetadata(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "metadata")
	defer rf.OnEnd(&outcome)

	// Compute target finalizer state.
	// RV should exist if it has no DeletionTimestamp.
	shouldExist := rv.DeletionTimestamp == nil
	hasRVRs := len(rvrs) > 0
	// Keep finalizer if RV should exist or if there are still RVRs (datamesh children).
	// RVAs do not block RV deletion — they are independent intent objects.
	targetFinalizerPresent := shouldExist || hasRVRs

	if isRVMetadataInSync(rv, targetFinalizerPresent) {
		return rf.Continue()
	}

	base := rv.DeepCopy()
	applyRVMetadata(rv, targetFinalizerPresent)

	if err := r.patchRV(rf.Ctx(), rv, base); err != nil {
		return rf.Fail(err)
	}

	// The first finalizer add on a live RV is the moment the controller takes the volume over.
	if rv.DeletionTimestamp == nil &&
		!obju.HasFinalizer(base, v1alpha1.RVControllerFinalizer) &&
		obju.HasFinalizer(rv, v1alpha1.RVControllerFinalizer) {
		observeRVCreated(rv)
	}

	// If finalizer was removed, we're done (object will be deleted).
	if !targetFinalizerPresent {
		return rf.Done()
	}

	return rf.Continue()
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: RVR finalizers
//

// reconcileRVRFinalizers adds RVControllerFinalizer to non-deleting RVRs (including user-created)
// and removes it from deleting RVRs when safe (not a datamesh member and no RemoveReplica
// transition in progress).
//
// Reconcile pattern: Target-state driven
func (r *Reconciler) reconcileRVRFinalizers(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "rvr-finalizers")
	defer rf.OnEnd(&outcome)

	for _, rvr := range rvrs {
		if rvr.DeletionTimestamp == nil {
			// Non-deleting: add finalizer if missing.

			// Skip if finalizer is already present.
			if obju.HasFinalizer(rvr, v1alpha1.RVControllerFinalizer) {
				continue
			}

			// Add finalizer to ensure datamesh cleanup completes before RVR is deleted.
			base := rvr.DeepCopy()
			obju.AddFinalizer(rvr, v1alpha1.RVControllerFinalizer)
			if err := r.patchRVR(rf.Ctx(), rvr, base); err != nil {
				return rf.Failf(err, "adding finalizer to RVR %s", rvr.Name)
			}
		} else {
			// Deleting: remove finalizer if safe.

			// Skip if finalizer is already absent.
			if !obju.HasFinalizer(rvr, v1alpha1.RVControllerFinalizer) {
				continue
			}

			// Not safe to remove if the RVR is still a datamesh member or leaving datamesh
			// (RemoveReplica transition in progress).
			if isRVRMemberOrLeavingDatamesh(rv, rvr.Name) {
				continue
			}

			// Remove finalizer — RVR can be finalized.
			base := rvr.DeepCopy()
			obju.RemoveFinalizer(rvr, v1alpha1.RVControllerFinalizer)
			if err := r.patchRVR(rf.Ctx(), rvr, base); err != nil {
				return rf.Failf(err, "removing finalizer from RVR %s", rvr.Name)
			}
		}
	}

	return rf.Continue()
}

// isRVRMemberOrLeavingDatamesh returns true if the RVR is a datamesh member or has an active
// RemoveReplica transition (still leaving datamesh). Returns false when rv is nil.
func isRVRMemberOrLeavingDatamesh(rv *v1alpha1.ReplicatedVolume, rvrName string) bool {
	if rv == nil {
		return false
	}

	// Check if the RVR is a datamesh member.
	if rv.Status.Datamesh.FindMemberByName(rvrName) != nil {
		return true
	}

	// Check for active RemoveReplica transition for this replica.
	for i := range rv.Status.DatameshTransitions {
		t := &rv.Status.DatameshTransitions[i]
		if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveReplica && t.ReplicaName == rvrName {
			return true
		}
	}

	return false
}

// applyDatameshMember adds or updates a member in the datamesh.
// Returns true if the member was added or any field was changed.
func applyDatameshMember(rv *v1alpha1.ReplicatedVolume, member v1alpha1.DatameshMember) bool {
	for i := range rv.Status.Datamesh.Members {
		if rv.Status.Datamesh.Members[i].ID() == member.ID() {
			m := &rv.Status.Datamesh.Members[i]
			changed := false
			if m.Type != member.Type {
				m.Type = member.Type
				changed = true
			}
			if m.NodeName != member.NodeName {
				m.NodeName = member.NodeName
				changed = true
			}
			if m.Zone != member.Zone {
				m.Zone = member.Zone
				changed = true
			}
			if !slices.Equal(m.Addresses, member.Addresses) {
				m.Addresses = member.Addresses
				changed = true
			}
			if m.LVMVolumeGroupName != member.LVMVolumeGroupName {
				m.LVMVolumeGroupName = member.LVMVolumeGroupName
				changed = true
			}
			if m.LVMVolumeGroupThinPoolName != member.LVMVolumeGroupThinPoolName {
				m.LVMVolumeGroupThinPoolName = member.LVMVolumeGroupThinPoolName
				changed = true
			}
			if m.Attached != member.Attached {
				m.Attached = member.Attached
				changed = true
			}
			return changed
		}
	}
	rv.Status.Datamesh.Members = append(rv.Status.Datamesh.Members, member)
	return true
}

// ensureDatameshMemberAddresses syncs datamesh member addresses from current
// RVR statuses. If any address changed, updates the member and increments
// DatameshRevision so that agents re-converge. Returns true if anything changed.
func ensureDatameshMemberAddresses(rv *v1alpha1.ReplicatedVolume, rvrs []*v1alpha1.ReplicatedVolumeReplica) bool {
	addressChanged := false
	for i := range rv.Status.Datamesh.Members {
		m := &rv.Status.Datamesh.Members[i]
		for _, rvr := range rvrs {
			if rvr.Name != m.Name {
				continue
			}
			if len(rvr.Status.Addresses) == 0 {
				break
			}
			if !slices.Equal(m.Addresses, rvr.Status.Addresses) {
				m.Addresses = slices.Clone(rvr.Status.Addresses)
				addressChanged = true
			}
			break
		}
	}
	if addressChanged {
		rv.Status.DatameshRevision++
	}
	return addressChanged
}

// applyDatameshReplicaRequestMessages updates the Message field for pending replica transitions
// whose ID is in the given set. Returns true if any message was changed.
func applyDatameshReplicaRequestMessages(rv *v1alpha1.ReplicatedVolume, repIDs idset.IDSet, message string) bool {
	changed := false
	for i := range rv.Status.DatameshReplicaRequests {
		t := &rv.Status.DatameshReplicaRequests[i]
		if repIDs.Contains(t.ID()) && t.Message != message {
			t.Message = message
			changed = true
		}
	}
	return changed
}

// computeFormationPreconfigureWaitMessage builds a human-readable formation transition
// message for the preconfigure phase, showing only non-empty wait reasons
// (pending scheduling, scheduling failed with inline error details, preconfiguring).
func computeFormationPreconfigureWaitMessage(
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	targetReplicaCount byte,
	pendingScheduling, schedulingFailed, waitingPreconfiguration idset.IDSet,
) string {
	var waitReasons []string
	if !pendingScheduling.IsEmpty() {
		waitReasons = append(waitReasons, fmt.Sprintf("pending scheduling [%s]", pendingScheduling))
	}
	if !schedulingFailed.IsEmpty() {
		part := fmt.Sprintf("scheduling failed [%s]", schedulingFailed)
		if msgs := computeActualSchedulingFailureMessages(rvrs, schedulingFailed); len(msgs) > 0 {
			part += " (" + strings.Join(msgs, " | ") + ")"
		}
		waitReasons = append(waitReasons, part)
	}
	if !waitingPreconfiguration.IsEmpty() {
		waitReasons = append(waitReasons, fmt.Sprintf("preconfiguring [%s]", waitingPreconfiguration))
	}
	waitingCount := pendingScheduling.Len() + schedulingFailed.Len() + waitingPreconfiguration.Len()
	return fmt.Sprintf("Waiting for %d/%d replicas: %s",
		waitingCount, targetReplicaCount, strings.Join(waitReasons, ", "))
}

// computeActualSchedulingFailureMessages collects deduplicated, sorted messages from RVRs
// whose Scheduled condition is present and False. Only RVRs whose ID is in the given set
// are considered. Returns nil if no such messages exist.
func computeActualSchedulingFailureMessages(rvrs []*v1alpha1.ReplicatedVolumeReplica, ids idset.IDSet) []string {
	var msgs []string
	for _, rvr := range rvrs {
		if !ids.Contains(rvr.ID()) {
			continue
		}
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondScheduledType)
		if cond == nil || cond.Status != metav1.ConditionFalse || cond.Message == "" {
			continue
		}
		if !slices.Contains(msgs, cond.Message) {
			msgs = append(msgs, cond.Message)
		}
	}
	slices.Sort(msgs)
	return msgs
}

// computeTargetQuorum computes Quorum and QuorumMinimumRedundancy from the
// configuration. Used during formation to set initial q/qmr values.
//
//	qmr = config.GMDR + 1
//	q   = floor(voters / 2) + 1, but at least floor(minD / 2) + 1
//	minD = intended diskful count (v1alpha1.ReplicatedVolumeConfiguration.IntendedLayout)
func computeTargetQuorum(rv *v1alpha1.ReplicatedVolume) (q, qmr byte) {
	cfg := rv.Status.Configuration
	// minD is the intended diskful count. Derived from the single source of truth
	// (IntendedLayout) rather than re-deriving D = FTT+GMDR+1 here.
	intendedD, _ := cfg.IntendedLayout()
	minD := byte(intendedD)

	minQ := minD/2 + 1
	voters := idset.FromWhere(rv.Status.Datamesh.Members, func(m v1alpha1.DatameshMember) bool {
		return m.Type.IsVoter()
	})
	quorum := byte(voters.Len()/2 + 1)
	q = max(quorum, minQ)

	qmr = cfg.QuorumMinimumRedundancy()

	return q, qmr
}

// transZonalValidZoneCounts returns the zone counts a TransZonal layout with the given
// FTT/GMDR combination supports. It is the single Go source of the "TransZonal zone
// requirements" table of the datamesh README and MUST match the RSC-level CEL zone
// validation (new control plane rules in api/v1alpha1/rsc_types.go); the cross-check
// test pinning the two together is reconciler_zone_cel_consistency_test.go.
// Admission caps FTT/GMDR at 1 (see the field markers in rsc_types.go), so the higher
// datamesh layouts (4D, 4D+1TB, 5D) have no entries: they cannot be configured.
func transZonalValidZoneCounts(ftt, gmdr byte) []int {
	switch {
	case ftt == 0 && gmdr == 1: // 2D: pure only.
		return []int{2}
	case ftt == 1 && gmdr == 0: // 2D+1TB: pure only.
		return []int{3}
	case ftt == 1 && gmdr == 1: // 3D: pure only.
		return []int{3}
	default:
		// Layouts above FTT=1/GMDR=1 are not admitted; unknown combos are invalid.
		return nil
	}
}

// isTransZonalZoneCountValid checks whether the given zone count is valid for a TransZonal
// layout with the specified FTT/GMDR combination, i.e. whether the count is one of
// transZonalValidZoneCounts(ftt, gmdr).
func isTransZonalZoneCountValid(ftt, gmdr byte, zoneCount int) bool {
	return slices.Contains(transZonalValidZoneCounts(ftt, gmdr), zoneCount)
}

// joinZoneCounts renders a valid zone count list for a condition message: "2",
// "3 or 5", "3 or 4 or 5". An empty list renders as "" (the caller reports
// unsupported combinations with its own message).
func joinZoneCounts(counts []int) string {
	strs := make([]string, 0, len(counts))
	for _, c := range counts {
		strs = append(strs, fmt.Sprintf("%d", c))
	}
	return strings.Join(strs, " or ")
}

// isRVMetadataInSync checks if the RV metadata (finalizer + labels) is in sync with the target state.
func isRVMetadataInSync(rv *v1alpha1.ReplicatedVolume, targetFinalizerPresent bool) bool {
	// Check finalizer.
	actualFinalizerPresent := obju.HasFinalizer(rv, v1alpha1.RVControllerFinalizer)
	if actualFinalizerPresent != targetFinalizerPresent {
		return false
	}

	// Check replicated-storage-class label.
	if rv.Spec.ReplicatedStorageClassName != "" {
		if !obju.HasLabelValue(rv, v1alpha1.ReplicatedStorageClassLabelKey, rv.Spec.ReplicatedStorageClassName) {
			return false
		}
	} else {
		// Manual mode or no RSC: label must not exist.
		if obju.HasLabel(rv, v1alpha1.ReplicatedStorageClassLabelKey) {
			return false
		}
	}

	return true
}

// applyRVMetadata applies finalizer and labels to RV.
// Returns true if any metadata was changed.
func applyRVMetadata(rv *v1alpha1.ReplicatedVolume, targetFinalizerPresent bool) (changed bool) {
	// Apply finalizer.
	if targetFinalizerPresent {
		changed = obju.AddFinalizer(rv, v1alpha1.RVControllerFinalizer) || changed
	} else {
		changed = obju.RemoveFinalizer(rv, v1alpha1.RVControllerFinalizer) || changed
	}

	// Apply replicated-storage-class label (set in Auto mode, remove in Manual mode).
	if rv.Spec.ReplicatedStorageClassName != "" {
		changed = obju.SetLabel(rv, v1alpha1.ReplicatedStorageClassLabelKey, rv.Spec.ReplicatedStorageClassName) || changed
	} else {
		changed = obju.RemoveLabel(rv, v1alpha1.ReplicatedStorageClassLabelKey) || changed
	}

	return changed
}

// reconcileRVConfiguration derives rv.Status.Configuration from the appropriate source
// (RSC in Auto mode, ManualConfiguration in Manual mode), validates TransZonal zone
// count via RSP, and sets the ConfigurationReady condition.
//
// Callers control when this function is called:
//   - Root Reconcile: when Configuration is nil (initial set)
//   - reconcileNormalOperation: always (check for config updates)
//   - Formation reset (create/v1): after clearing Configuration to nil (re-derive)
//
// During formation (both create and adopt), callers do NOT call this function
// (config is frozen). Any pending config change is picked up by normal operation
// after formation completes.
//
// Generation semantics by mode:
//   - Auto mode: ConfigurationGeneration = RSC's Status.ConfigurationGeneration
//   - Manual mode: both generations stay 0 — the configuration comes from the volume spec, so
//     there is no storage class rollout to track. Content equality (not generations) decides
//     whether the stored configuration needs an update.
//
// In Auto mode the RSC configuration is read only when the class has published it for its
// current spec generation, and it is applied only when the class rollout strategy allows it:
// NewVolumesOnly holds it back from every existing volume (see that branch), RollingUpdate lets
// it through in batches of maxParallel (see the rollout budget below and reconciler_rollout.go).
// ConfigurationGeneration always names the generation the stored content came from;
// ConfigurationObservedGeneration names the newest generation the volume has seen. The two differ
// exactly while a newer configuration is held back or waiting for a rollout slot.
//
// Reconcile pattern: In-place reconciliation
func (r *Reconciler) reconcileRVConfiguration(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rsc *v1alpha1.ReplicatedStorageClass,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "configuration")
	defer rf.OnEnd(&outcome)

	changed := false

	// Compute intended configuration and generation from the appropriate source.
	// intended is a read-only pointer (no DeepCopy); clone only when writing to status.
	var intended *v1alpha1.ReplicatedVolumeConfiguration
	var intendedGeneration int64
	// holdNewerConfiguration reports that the storage class rolls its configuration out to new
	// volumes only, so a volume that already has one must keep it. It is set in the Auto branch
	// only, and therefore implies rsc != nil.
	holdNewerConfiguration := false
	// throttleRollout reports that the intended configuration comes from a storage class, so the
	// class-wide rollout budget applies to it. It is set in the Auto branch only, and therefore
	// implies rsc != nil.
	throttleRollout := false

	switch rv.Spec.ConfigurationMode {
	case v1alpha1.ReplicatedVolumeConfigurationModeManual:
		// CEL validation guarantees ManualConfiguration is present in Manual mode.
		// intendedGeneration stays 0: no RSC rollout tracking for Manual mode.
		intended = rv.Spec.ManualConfiguration
	default: // Auto (or empty — default is Auto).
		if rsc == nil {
			changed = applyConfigurationReadyCondFalse(rv,
				v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonWaitingForStorageClass,
				fmt.Sprintf("ReplicatedStorageClass %q not found", rv.Spec.ReplicatedStorageClassName))
			return rf.Continue().ReportChangedIf(changed)
		}
		if rsc.Status.Configuration == nil {
			changed = applyConfigurationReadyCondFalse(rv,
				v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonWaitingForStorageClass,
				fmt.Sprintf("ReplicatedStorageClass %q configuration not ready", rsc.Name))
			return rf.Continue().ReportChangedIf(changed)
		}
		// The published configuration must describe the storage class as it is now. Until the
		// class controller accepts the latest spec edit, status still carries the previous
		// generation: applying it would hand a volume a configuration the user has already
		// replaced — and under NewVolumesOnly the volume would then hold that superseded
		// configuration forever, because it stops being "new" the moment it gets one.
		// Waiting is safe: the RV watches RSC status changes, so the next publish wakes us up.
		if rsc.Status.ConfigurationGeneration != rsc.Generation {
			changed = applyConfigurationReadyCondFalse(rv,
				v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonWaitingForStorageClass,
				fmt.Sprintf("ReplicatedStorageClass %q has not published a configuration for generation %d yet (published generation: %d)",
					rsc.Name, rsc.Generation, rsc.Status.ConfigurationGeneration))
			return rf.Continue().ReportChangedIf(changed)
		}
		intended = rsc.Status.Configuration
		intendedGeneration = rsc.Status.ConfigurationGeneration
		holdNewerConfiguration = rsc.Spec.ConfigurationRolloutStrategy.GetType() == v1alpha1.ConfigurationRolloutNewVolumesOnly
		throttleRollout = true
	}

	// Fast-path: config content matches intended → update generation tracking, skip the rest.
	// This runs before the NewVolumesOnly hold on purpose: equal content means the volume is
	// already aligned with the new generation, so there is nothing to hold back.
	if rv.Status.Configuration != nil && *rv.Status.Configuration == *intended {
		if rv.Status.ConfigurationGeneration != intendedGeneration {
			rv.Status.ConfigurationGeneration = intendedGeneration
			changed = true
		}

		if rv.Status.ConfigurationObservedGeneration != intendedGeneration {
			rv.Status.ConfigurationObservedGeneration = intendedGeneration
			changed = true
		}

		changed = applyConfigurationReadyCondTrue(rv,
			v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonReady,
			"Configuration is ready") || changed
		return rf.Continue().ReportChangedIf(changed)
	}

	// NewVolumesOnly: observe the new configuration but do not apply it.
	//
	// The volume keeps both its configuration content and the generation that content came
	// from (they are always consistent), while ConfigurationObservedGeneration advances so the
	// storage class aggregate does not hang in "pending observation" forever. The held state is
	// reported honestly as ConfigurationReady=False: the condition means "configuration matches
	// the storage class", and here it deliberately does not. Nothing gates on this condition —
	// the volume keeps operating on its own configuration.
	//
	// The hold is deliberate and applies even when the held configuration later becomes
	// unsatisfiable: the escape is to switch the strategy to RollingUpdate or recreate the
	// volume, never a silent replacement.
	if holdNewerConfiguration && rv.Status.Configuration != nil {
		if rv.Status.ConfigurationObservedGeneration != intendedGeneration {
			rv.Status.ConfigurationObservedGeneration = intendedGeneration
			changed = true
		}

		changed = applyConfigurationReadyCondFalse(rv,
			v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonNewerConfigurationHeld,
			fmt.Sprintf("ReplicatedStorageClass %q has a newer configuration (generation %d); "+
				"the volume keeps its configuration (generation %d) because the rollout strategy is %s",
				rsc.Name, intendedGeneration, rv.Status.ConfigurationGeneration,
				v1alpha1.ConfigurationRolloutNewVolumesOnly)) || changed
		return rf.Continue().ReportChangedIf(changed)
	}

	// Validate TransZonal zone count.
	if intended.Topology == v1alpha1.TopologyTransZonal {
		rspZoneCount, err := r.getRSPZoneCount(rf.Ctx(), intended.ReplicatedStoragePoolName)
		if err != nil {
			if apierrors.IsNotFound(err) {
				changed = applyConfigurationReadyCondFalse(rv,
					v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonInvalidConfiguration,
					fmt.Sprintf("ReplicatedStoragePool %q not found", intended.ReplicatedStoragePoolName))
				return rf.Continue().ReportChangedIf(changed)
			}
			return rf.Failf(err, "getting RSP zone count for %s", intended.ReplicatedStoragePoolName)
		}

		if !isTransZonalZoneCountValid(intended.FailuresToTolerate, intended.GuaranteedMinimumDataRedundancy, rspZoneCount) {
			// Name the allowed counts so the operator can tell a spec problem (wrong zone
			// count for the layout) from an infrastructure one (a zone lost its nodes).
			validCounts := transZonalValidZoneCounts(intended.FailuresToTolerate, intended.GuaranteedMinimumDataRedundancy)
			msg := fmt.Sprintf("TransZonal with FTT=%d, GMDR=%d requires exactly %s zones, RSP has %d zones",
				intended.FailuresToTolerate, intended.GuaranteedMinimumDataRedundancy,
				joinZoneCounts(validCounts), rspZoneCount)
			if len(validCounts) == 0 {
				// Unreachable with admitted specs (CEL rules out unsupported combos); kept
				// honest for hand-edited or pre-existing objects.
				msg = fmt.Sprintf("TransZonal with FTT=%d, GMDR=%d is not a supported combination, RSP has %d zones",
					intended.FailuresToTolerate, intended.GuaranteedMinimumDataRedundancy, rspZoneCount)
			}
			changed = applyConfigurationReadyCondFalse(rv,
				v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonInvalidConfiguration, msg)
			return rf.Continue().ReportChangedIf(changed)
		}
	}

	// Throttle the rollout across the class: RollingUpdate.maxParallel caps how many volumes may
	// be migrating to the new configuration at once, so an unsatisfiable configuration damages a
	// bounded number of them (see reconciler_rollout.go). Only a volume that already carries a
	// configuration is throttled: a new one is not migrating anything.
	//
	// The gate sits behind the validation on purpose — an invalid configuration must be reported
	// as such rather than queued, and a volume waiting for a slot must not be told the queue is
	// what stops it when the configuration would be rejected anyway.
	if throttleRollout && rv.Status.Configuration != nil {
		classRVs, err := r.getRVsByRSC(rf.Ctx(), rsc.Name)
		if err != nil {
			return rf.Failf(err, "listing ReplicatedVolumes of ReplicatedStorageClass %s", rsc.Name)
		}

		maxParallel := computeIntendedRolloutMaxParallel(rsc)
		cohort := computeActualRolloutCohort(rv, classRVs, intended, intendedGeneration)
		if !computeTargetRolloutAdmission(rv, cohort, maxParallel) {
			// Keep the applied content and the generation it came from (they are always
			// consistent), and only record that the newer generation has been observed, so the
			// class aggregate does not hang in "pending observation" while the volume queues.
			if rv.Status.ConfigurationObservedGeneration != intendedGeneration {
				rv.Status.ConfigurationObservedGeneration = intendedGeneration
				changed = true
			}

			// The message deliberately carries no live counters: it is rewritten into the object
			// on every change, and a class-wide progress number would repatch every waiting
			// volume each time any other volume converges.
			changed = applyConfigurationReadyCondFalse(rv,
				v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonConfigurationRolloutInProgress,
				fmt.Sprintf("ReplicatedStorageClass %q rolls its configuration (generation %d) out to at most %d volume(s) at a time; "+
					"the volume keeps its configuration (generation %d) until a slot is free",
					rsc.Name, intendedGeneration, maxParallel, rv.Status.ConfigurationGeneration)) || changed

			return rf.ContinueAndRequeueAfter(configurationRolloutRequeueInterval).ReportChangedIf(changed)
		}
	}

	// Set or update configuration.
	// Content differs from intended (fast-path above ruled out content-equal case).
	// DeepCopy to avoid aliasing with the RSC cache object or ManualConfiguration.
	rv.Status.Configuration = intended.DeepCopy()
	rv.Status.ConfigurationGeneration = intendedGeneration
	rv.Status.ConfigurationObservedGeneration = intendedGeneration
	changed = true

	// Configuration is set and valid.
	changed = applyConfigurationReadyCondTrue(rv,
		v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonReady,
		"Configuration is ready") || changed

	return rf.Continue().ReportChangedIf(changed)
}

// applyConfigurationReadyCondTrue sets ConfigurationReady condition to True.
func applyConfigurationReadyCondTrue(rv *v1alpha1.ReplicatedVolume, reason, message string) bool {
	return obju.SetStatusCondition(rv, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeCondConfigurationReadyType,
		Status:  metav1.ConditionTrue,
		Reason:  reason,
		Message: message,
	})
}

// applyConfigurationReadyCondFalse sets ConfigurationReady condition to False.
func applyConfigurationReadyCondFalse(rv *v1alpha1.ReplicatedVolume, reason, message string) bool {
	return obju.SetStatusCondition(rv, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeCondConfigurationReadyType,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: layout status
//

// reconcileLayoutStatus is the SINGLE writer of the MembershipLayoutConverged condition and
// status.membershipLayout. It compares the actual datamesh layout (diskful voters + tie-breakers)
// against the layout intended by the volume's configuration and reports whether they
// have converged.
//
// It is called only post-formation (the root Reconcile invokes it in the normal-operation
// branch, never during formation) and after the configuration has been acknowledged
// (status.configuration is set). It only reports: the convergence actions live in the
// separate reconcileLayoutConvergence step, whose decision (computeTargetLayoutAction) is
// reused here so the reported Converging/CannotConverge/TransitionUnsupported reason always
// agrees with what convergence did — keeping this the only writer of the condition.
//
// Reconcile pattern: In-place reconciliation
func (r *Reconciler) reconcileLayoutStatus(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	rvas []*v1alpha1.ReplicatedVolumeAttachment,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "layout-status")
	defer rf.OnEnd(&outcome)

	// Precondition: configuration must be acknowledged (guaranteed by the caller, which
	// invokes this only inside the "configuration exists" normal-operation branch).
	if rv.Status.Configuration == nil {
		return rf.Continue()
	}

	report := computeLayoutReport(rv, rvrs, rvas)

	changed := applyMembershipLayout(rv, report.membershipLayout)
	switch report.converged.status {
	case metav1.ConditionTrue:
		changed = applyMembershipLayoutConvergedCondTrue(rv, report.converged.reason, report.converged.message) || changed
	case metav1.ConditionUnknown:
		// Deletion: we no longer evaluate convergence (see computeTargetLayoutAction).
		changed = applyMembershipLayoutConvergedCondUnknown(rv, report.converged.reason, report.converged.message) || changed
	default:
		changed = applyMembershipLayoutConvergedCondFalse(rv, report.converged.reason, report.converged.message) || changed
	}

	return rf.Continue().ReportChangedIf(changed)
}

// computeActualLayout counts the actual datamesh layout from members:
//   - diskful     = Diskful + LiminalDiskful members (quorum voters holding data)
//   - tiebreakers = TieBreaker members
//
// Access and ShadowDiskful (and their liminal variant) members are not part of the layout.
func computeActualLayout(rv *v1alpha1.ReplicatedVolume) (diskful, tiebreakers int) {
	for i := range rv.Status.Datamesh.Members {
		switch t := rv.Status.Datamesh.Members[i].Type; {
		case isDiskfulLayoutMemberType(t):
			diskful++
		case t == v1alpha1.DatameshMemberTypeTieBreaker:
			tiebreakers++
		}
	}
	return diskful, tiebreakers
}

// hasLayoutChangingTransition reports whether an active datamesh transition changes the layout
// composition (diskful voters / tie-breakers).
//
// Only membership transitions can do so; other types (Attach/Detach/ForceDetach, ResizeVolume,
// ChangeQuorum, ChangeSystemNetworks, Enable/DisableMultiattach, RepairNetworkAddresses) leave
// the layout unchanged. Membership types mirror the dispatch in
// datamesh/membership_dispatch.go.
//
// A membership transition is not enough by itself: Access and ShadowDiskful members are outside
// the layout, so Add/Remove/ChangeReplicaType involving only those types must not be treated as
// convergence progress (otherwise the MembershipLayoutConverged condition would flap on unrelated
// membership activity). The classification therefore goes by the REPLICA TYPES recorded in the
// transition, which are always populated (see ReplicatedVolumeDatameshTransition).
//
// It deliberately does NOT filter by Group: ForceRemoveReplica lives in the Emergency group, so
// a Group == VotingMembership filter would silently drop it.
func hasLayoutChangingTransition(rv *v1alpha1.ReplicatedVolume) bool {
	for i := range rv.Status.DatameshTransitions {
		t := &rv.Status.DatameshTransitions[i]
		switch t.Type {
		case v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica,
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveReplica,
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeForceRemoveReplica:
			if isLayoutReplicaType(t.ReplicaType) {
				return true
			}
		case v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeReplicaType:
			if isLayoutReplicaType(t.FromReplicaType) || isLayoutReplicaType(t.ToReplicaType) {
				return true
			}
		}
	}
	return false
}

// isLayoutReplicaType reports whether a replica of this type is counted by the layout
// (see computeActualLayout: diskful voters and tie-breakers).
func isLayoutReplicaType(t v1alpha1.ReplicaType) bool {
	return t == v1alpha1.ReplicaTypeDiskful || t == v1alpha1.ReplicaTypeTieBreaker
}

// layoutReport is the computed report driving status.membershipLayout and the MembershipLayoutConverged condition.
type layoutReport struct {
	// membershipLayout mirrors the optional status.membershipLayout field: nil means "unset" (no layout to
	// publish), never the empty string. computeLayoutReport always fills it, because it only
	// runs post-formation, where the member composition is known; the pointer keeps the
	// "unset" state representable end-to-end so it is never conflated with "".
	membershipLayout *string
	// converged is the MembershipLayoutConverged report produced by the convergence decision.
	converged membershipLayoutConvergedReport
}

// computeLayoutReport produces the status.membershipLayout string and the MembershipLayoutConverged report by
// reusing the convergence decision (computeTargetLayoutAction), so the reported reason always
// matches what reconcileLayoutConvergence does this pass:
//   - actual == intended, with no pending layout work       → True / Converged
//   - a membership transition or a queued action is moving  → False / Converging
//     the layout (retype requested / TB creation pending)
//   - a whitelist pattern applies but no admissible candidate → False / CannotConverge
//   - mismatch outside the whitelist                        → False / TransitionUnsupported
//
// A matching layout is reported Converged regardless of unrelated active transitions
// (e.g. attach/resize), so the condition does not flap while the layout is already correct.
func computeLayoutReport(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	rvas []*v1alpha1.ReplicatedVolumeAttachment,
) layoutReport {
	actualD, actualTB := computeActualLayout(rv)
	_, convergedReport := computeTargetLayoutAction(rv, rvrs, rvas)
	return layoutReport{
		membershipLayout: ptr.To(formatLayout(actualD, actualTB)),
		converged:        convergedReport,
	}
}

// formatLayout renders a datamesh layout as a short, deterministic string:
// "3D" for 3 diskful and no tie-breaker, "2D+1TB" for 2 diskful and 1 tie-breaker.
// The "+NTB" suffix is omitted when there are no tie-breakers.
func formatLayout(diskful, tiebreakers int) string {
	if tiebreakers == 0 {
		return fmt.Sprintf("%dD", diskful)
	}
	return fmt.Sprintf("%dD+%dTB", diskful, tiebreakers)
}

// applyMembershipLayout sets status.membershipLayout (the actual datamesh layout string).
//
// status.membershipLayout is an optional scalar: nil (absent) means "not computed yet" and is NOT the
// same as the empty string, which is never a valid layout value. A nil layout therefore
// clears the field instead of publishing "".
func applyMembershipLayout(rv *v1alpha1.ReplicatedVolume, layout *string) bool {
	if ptr.Equal(rv.Status.MembershipLayout, layout) {
		return false
	}
	if layout == nil {
		rv.Status.MembershipLayout = nil
	} else {
		// Copy the value instead of sharing the report's pointer (read-only input contract).
		rv.Status.MembershipLayout = ptr.To(*layout)
	}
	return true
}

// applyMembershipLayoutConvergedCondTrue sets the MembershipLayoutConverged condition to True.
func applyMembershipLayoutConvergedCondTrue(rv *v1alpha1.ReplicatedVolume, reason, message string) bool {
	return obju.SetStatusCondition(rv, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType,
		Status:  metav1.ConditionTrue,
		Reason:  reason,
		Message: message,
	})
}

// applyMembershipLayoutConvergedCondFalse sets the MembershipLayoutConverged condition to False.
func applyMembershipLayoutConvergedCondFalse(rv *v1alpha1.ReplicatedVolume, reason, message string) bool {
	return obju.SetStatusCondition(rv, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

// applyMembershipLayoutConvergedCondUnknown sets the MembershipLayoutConverged condition to Unknown.
func applyMembershipLayoutConvergedCondUnknown(rv *v1alpha1.ReplicatedVolume, reason, message string) bool {
	return obju.SetStatusCondition(rv, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType,
		Status:  metav1.ConditionUnknown,
		Reason:  reason,
		Message: message,
	})
}

// ensureDatameshReplicaRequests synchronizes rv.Status.DatameshReplicaRequests
// with the current DatameshRequest from each RVR.
// Both lists are kept sorted by name for determinism.
// Uses sorted merge-in-place algorithm (no map allocation).
func ensureDatameshReplicaRequests(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "datamesh-pending-replica-transitions")
	defer ef.OnEnd(&outcome)

	changed := false
	existing := rv.Status.DatameshReplicaRequests

	// Ensure existing entries are sorted by name for the merge algorithm below.
	// Note: sorting does not mark changed=true intentionally. Order is semantically
	// irrelevant for the API, so a mere reorder is not a reason to patch. If a real
	// content change occurs, the patch will persist the correctly sorted value.
	slices.SortFunc(existing, func(a, b v1alpha1.ReplicatedVolumeDatameshReplicaRequest) int {
		return cmp.Compare(a.ID(), b.ID())
	})

	// Merge-in-place with two pointers.
	// rvrs are already sorted by caller (getRVRsSorted).
	result := make([]v1alpha1.ReplicatedVolumeDatameshReplicaRequest, 0, len(existing)+len(rvrs))
	i, j := 0, 0

	for i < len(existing) && j < len(rvrs) {
		// Skip rvrs with nil transition.
		if rvrs[j].Status.DatameshRequest == nil {
			j++
			continue
		}

		existingName := existing[i].Name
		rvrName := rvrs[j].Name

		switch cmp.Compare(existingName, rvrName) {
		case -1: // existingName < rvrName: entry removed
			changed = true
			i++
		case 1: // existingName > rvrName: new entry
			changed = true
			result = append(result, v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				Name: rvrName,
				// DeepCopy to avoid aliasing: rvrs is a read-only input,
				// and the cloned value will live inside rv.Status (mutation target).
				Request:         *rvrs[j].Status.DatameshRequest.DeepCopy(),
				FirstObservedAt: metav1.Now(),
			})
			j++
		case 0: // equal names
			if existing[i].Request.Equals(rvrs[j].Status.DatameshRequest) {
				// Keep as-is.
				result = append(result, existing[i])
			} else {
				// Update: copy request, clear Message, set new FirstObservedAt.
				changed = true
				result = append(result, v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
					Name: rvrName,
					// DeepCopy to avoid aliasing: rvrs is a read-only input,
					// and the cloned value will live inside rv.Status (mutation target).
					Request:         *rvrs[j].Status.DatameshRequest.DeepCopy(),
					FirstObservedAt: metav1.Now(),
				})
			}
			i++
			j++
		}
	}

	// Drain remaining rv entries (removed).
	if i < len(existing) {
		changed = true
	}

	// Drain remaining rvrs with non-nil transition (added).
	for j < len(rvrs) {
		if rvrs[j].Status.DatameshRequest != nil {
			changed = true
			result = append(result, v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				Name: rvrs[j].Name,
				// DeepCopy to avoid aliasing: rvrs is a read-only input,
				// and the cloned value will live inside rv.Status (mutation target).
				Request:         *rvrs[j].Status.DatameshRequest.DeepCopy(),
				FirstObservedAt: metav1.Now(),
			})
		}
		j++
	}

	// Assign result only if changed.
	if changed {
		rv.Status.DatameshReplicaRequests = result
	}

	return ef.Ok().ReportChangedIf(changed)
}

// ensureStatusSize ensures rv.Status.Size reflects the minimum usable DRBD
// capacity across all diskful datamesh member replicas. Nil when no diskful
// members have reported their size yet.
func ensureStatusSize(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "status-size")
	defer ef.OnEnd(&outcome)

	// Build an IDSet of diskful datamesh members.
	diskfulMembers := idset.FromWhere(rv.Status.Datamesh.Members, func(m v1alpha1.DatameshMember) bool {
		return m.Type.HasBackingVolume()
	})

	// Find the minimum non-nil size across diskful datamesh member RVRs.
	var minSize *resource.Quantity
	for _, rvr := range rvrs {
		if !diskfulMembers.Contains(rvr.ID()) {
			continue
		}
		if rvr.Status.Size == nil {
			continue
		}
		if minSize == nil || rvr.Status.Size.Cmp(*minSize) < 0 {
			q := rvr.Status.Size.DeepCopy()
			minSize = &q
		}
	}

	// Compare and apply.
	changed := false
	currentNil := rv.Status.Size == nil
	targetNil := minSize == nil
	if currentNil != targetNil || (!currentNil && !rv.Status.Size.Equal(*minSize)) {
		rv.Status.Size = minSize
		changed = true
	}

	return ef.Ok().ReportChangedIf(changed)
}

// rvShouldNotExist returns true if RV should be deleted:
// DeletionTimestamp is set, no finalizers except ours, and either formation
// is still in progress (incomplete datamesh — skip attach/detach checks) or
// no attached members and no Detach transitions in progress.
func rvShouldNotExist(rv *v1alpha1.ReplicatedVolume) bool {
	if rv == nil {
		return true
	}

	if rv.DeletionTimestamp == nil {
		return false
	}

	// Check no other finalizers except ours.
	if obju.HasFinalizersOtherThan(rv, v1alpha1.RVControllerFinalizer) {
		return false
	}

	// During formation the datamesh is not yet fully established, so attached
	// members and detach transitions are not meaningful blockers — skip them
	// to avoid a deadlock where formation is stuck on deleting RVRs while
	// deletion is stuck waiting for detach that will never come.
	if forming, _ := isFormationInProgress(rv); forming {
		return true
	}

	// Check no attached members.
	for i := range rv.Status.Datamesh.Members {
		if rv.Status.Datamesh.Members[i].Attached {
			return false
		}
	}

	// Check no Detach transitions in progress (agent may still be demoting DRBD).
	for i := range rv.Status.DatameshTransitions {
		if rv.Status.DatameshTransitions[i].Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach {
			return false
		}
	}

	return true
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: deletion
//

// reconcileDeletion handles RV deletion: updates RVA conditions, removes RVR finalizers
// and deletes RVRs, and clears datamesh members.
//
// Reconcile pattern: In-place reconciliation
func (r *Reconciler) reconcileDeletion(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rvas []*v1alpha1.ReplicatedVolumeAttachment,
	rvrs *[]*v1alpha1.ReplicatedVolumeReplica,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "deletion")
	defer rf.OnEnd(&outcome)

	// Step 1: Update all RVA conditions.
	outcome = r.reconcileRVAWaiting(rf.Ctx(), rvas, "ReplicatedVolume is being deleted")
	if outcome.ShouldReturn() {
		return outcome
	}

	// Step 2: Remove finalizers from RVRs and delete them.
	for _, rvr := range *rvrs {
		if err := r.deleteRVRWithForcedFinalizerRemoval(rf.Ctx(), rvr); err != nil {
			return rf.Failf(err, "deleting RVR %s", rvr.Name)
		}
	}

	// Step 3: Publish the layout report for a deleting volume and clear datamesh members,
	// atomically in one status patch.
	//
	// An unattached RV reaches this branch directly, never going through normal operation, so
	// reconcileLayoutStatus never runs for it. Without this write the condition would keep the
	// last Converging/CannotConverge message — promising an action the deletion path will never
	// perform. Unknown/VolumeDeleting states honestly that convergence is no longer evaluated.
	base := rv.DeepCopy()
	changed := applyMembershipLayoutConvergedCondUnknown(rv,
		v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonVolumeDeleting,
		membershipLayoutConvergedVolumeDeletingMessage)
	// Ready=False/Terminating: the volume is being deleted. Resilient and AttachedReady are
	// left untouched (the last known value is preserved), so the empty attachedReadyReport is
	// only a placeholder here — rule 1 of computeReadyReport wins before it is consulted.
	changed = applyReadyCond(rv, computeReadyReport(rv, attachedReadyReport{})) || changed
	if len(rv.Status.Datamesh.Members) > 0 {
		rv.Status.Datamesh.Members = nil
		changed = true
	}
	if changed {
		if err := r.patchRVStatus(rf.Ctx(), rv, base); err != nil {
			return rf.Failf(err, "publishing deletion layout status")
		}
	}

	// We're done. Don't continue further reconciliation.
	return rf.Done()
}

// ──────────────────────────────────────────────────────────────────────────────
// View types
//

// rspView contains pre-fetched RSP data for RV reconciliation.
// Used to avoid I/O in compute/ensure helpers.
type rspView struct {
	// Type is the RSP type (LVM or LVMThin).
	Type v1alpha1.ReplicatedStoragePoolType
	// EligibleZones is the zone span of the whole pool: the distinct, non-empty zones of its
	// eligible nodes, sorted. Unlike EligibleNodes it is NOT narrowed to this volume, because a
	// placement question like "did the voters collapse into one zone?" is asked against the
	// zones the volume could have used, not against the ones it did.
	//
	// It comes from status rather than from spec.zones: spec.zones is a node selector (an empty
	// one admits every zone), so a pool can legitimately span zones it never declared, and the
	// storage class validates TransZonal against the zones of the eligible nodes for exactly
	// that reason. A zoneless node contributes nothing — it is not a zone.
	EligibleZones []string
	// SystemNetworkNames is the list of system network names from RSP spec.
	SystemNetworkNames []string
	// EligibleNodes contains only nodes present in RVRs/RVAs, sorted by NodeName.
	EligibleNodes []v1alpha1.ReplicatedStoragePoolEligibleNode
}

// GetSystemNetworkNames returns the intended system network names from the RSP spec.
func (v *rspView) GetSystemNetworkNames() []string {
	return v.SystemNetworkNames
}

// FindEligibleNode returns a pointer to the eligible node with the given name, or nil if not found.
// Uses binary search (EligibleNodes is sorted by NodeName).
func (v *rspView) FindEligibleNode(nodeName string) *v1alpha1.ReplicatedStoragePoolEligibleNode {
	idx, found := slices.BinarySearchFunc(v.EligibleNodes, nodeName, func(en v1alpha1.ReplicatedStoragePoolEligibleNode, target string) int {
		return cmp.Compare(en.NodeName, target)
	})
	if !found {
		return nil
	}
	return &v.EligibleNodes[idx]
}

// ──────────────────────────────────────────────────────────────────────────────
// Shared non-I/O helpers
//

// newRVR constructs a new in-memory ReplicatedVolumeReplica for rv: it picks the first free ID
// name (deterministic, so a repeated Create after a stale-cache retry converges via
// AlreadyExists) and adds the RV controller finalizer.
//
// It is a pure constructor: no receiver, no I/O, no mutation of its inputs. The controller owner
// reference (which needs the runtime scheme) and the Create call belong to the Reconcile method
// that owns the creation policy; the caller also inserts the created object into its local sorted
// slice (see insertRVRSorted).
func newRVR(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	typ v1alpha1.ReplicaType,
	nodeName string,
) (*v1alpha1.ReplicatedVolumeReplica, error) {
	rvr := &v1alpha1.ReplicatedVolumeReplica{
		Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
			ReplicatedVolumeName: rv.Name,
			Type:                 typ,
			NodeName:             nodeName,
		},
	}
	if !rvr.ChooseNewName(rvrs) {
		return nil, fmt.Errorf("no available ID for new RVR")
	}
	obju.AddFinalizer(rvr, v1alpha1.RVControllerFinalizer)
	return rvr, nil
}

// insertRVRSorted inserts rvr into rvrs keeping the slice ordered by ID, and returns the
// resulting slice. Reconcile methods use it to keep their local RVR slice consistent with the
// API after a create (see controller-reconciliation.mdc, "Local slices after Create/Patch").
func insertRVRSorted(
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	rvr *v1alpha1.ReplicatedVolumeReplica,
) []*v1alpha1.ReplicatedVolumeReplica {
	idx, _ := slices.BinarySearchFunc(rvrs, rvr.ID(), func(r *v1alpha1.ReplicatedVolumeReplica, id uint8) int {
		return cmp.Compare(r.ID(), id)
	})
	return slices.Insert(rvrs, idx, rvr)
}

// ──────────────────────────────────────────────────────────────────────────────
// Single-call I/O helpers
//

// --- RV ---

// getRV fetches a ReplicatedVolume by name. Returns (nil, nil) if not found.
func (r *Reconciler) getRV(ctx context.Context, name string) (*v1alpha1.ReplicatedVolume, error) {
	var rv v1alpha1.ReplicatedVolume
	if err := r.cl.Get(ctx, client.ObjectKey{Name: name}, &rv); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return nil, err
		}
		return nil, nil
	}
	return &rv, nil
}

// getRVsByRSC lists the ReplicatedVolumes that reference the given storage class, through the
// spec.replicatedStorageClassName index. The returned slice is unordered — callers that need a
// deterministic order must sort it themselves.
//
// The volumes come straight from the informer cache without a deep copy, because a rollout
// decision reads a handful of fields from every volume of the class and copying them all would
// be wasteful. Callers MUST treat them as strictly read-only: mutating one corrupts the cache
// for every other reader.
func (r *Reconciler) getRVsByRSC(ctx context.Context, rscName string) ([]*v1alpha1.ReplicatedVolume, error) {
	var list v1alpha1.ReplicatedVolumeList
	if err := r.cl.List(ctx, &list,
		client.MatchingFields{indexes.IndexFieldRVByReplicatedStorageClassName: rscName},
		client.UnsafeDisableDeepCopy,
	); err != nil {
		return nil, err
	}

	rvs := make([]*v1alpha1.ReplicatedVolume, len(list.Items))
	for i := range list.Items {
		rvs[i] = &list.Items[i]
	}
	return rvs, nil
}

func (r *Reconciler) patchRV(ctx context.Context, obj, base *v1alpha1.ReplicatedVolume) error {
	return r.cl.Patch(ctx, obj, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
}

func (r *Reconciler) patchRVStatus(ctx context.Context, obj, base *v1alpha1.ReplicatedVolume) error {
	return r.cl.Status().Patch(ctx, obj, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
}

// --- DRBDROp ---

// getDRBDROp fetches a DRBDResourceOperation by name. Returns (nil, nil) if not found.
func (r *Reconciler) getDRBDROp(ctx context.Context, name string) (*v1alpha1.DRBDResourceOperation, error) {
	var drbdrOp v1alpha1.DRBDResourceOperation
	if err := r.cl.Get(ctx, client.ObjectKey{Name: name}, &drbdrOp); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return nil, err
		}
		return nil, nil
	}
	return &drbdrOp, nil
}

// createDRBDROp constructs a DRBDResourceOperation with the given name and spec,
// sets rv as the controller owner, and creates it via the API.
// Returns the created object with server-assigned fields.
func (r *Reconciler) createDRBDROp(ctx context.Context, rv *v1alpha1.ReplicatedVolume, name string, spec v1alpha1.DRBDResourceOperationSpec) (*v1alpha1.DRBDResourceOperation, error) {
	obj := &v1alpha1.DRBDResourceOperation{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: spec,
	}
	if _, err := obju.SetControllerRef(obj, rv, r.scheme); err != nil {
		return nil, err
	}
	if err := r.cl.Create(ctx, obj); err != nil {
		return nil, err
	}
	return obj, nil
}

func (r *Reconciler) deleteDRBDROp(ctx context.Context, obj *v1alpha1.DRBDResourceOperation) error {
	if obj.DeletionTimestamp != nil {
		return nil
	}
	if err := client.IgnoreNotFound(r.cl.Delete(ctx, obj)); err != nil {
		return err
	}
	obj.DeletionTimestamp = ptr.To(metav1.Now())
	return nil
}

// --- RSP ---

// getRSPZoneCount fetches an RSP by name and returns the number of zones in its spec.
// Returns (0, nil) if RSP is not found (lightweight read for zone validation).
func (r *Reconciler) getRSPZoneCount(ctx context.Context, name string) (int, error) {
	var rsp v1alpha1.ReplicatedStoragePool
	if err := r.cl.Get(ctx, client.ObjectKey{Name: name}, &rsp, client.UnsafeDisableDeepCopy); err != nil {
		return 0, err
	}
	return len(rsp.Spec.Zones), nil
}

// getRSP fetches the RSP and returns a view containing only the eligible nodes
// that are present in the provided RVRs or active (non-deleting) RVAs.
// Uses UnsafeDisableDeepCopy for performance, manually copying needed fields.
func (r *Reconciler) getRSP(
	ctx context.Context,
	rspName string,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	rvas []*v1alpha1.ReplicatedVolumeAttachment,
) (*rspView, error) {
	var unsafeRSP v1alpha1.ReplicatedStoragePool
	if err := r.cl.Get(ctx, client.ObjectKey{Name: rspName}, &unsafeRSP, client.UnsafeDisableDeepCopy); err != nil {
		return nil, err
	}

	// Build sorted, deduplicated list of node names from RVRs + active RVAs for binary search.
	// RVA nodes are included so that Access RVR creation can check eligibility
	// for nodes that don't have an RVR yet.
	nodeNames := make([]string, 0, len(rvrs)+len(rvas))
	for _, rvr := range rvrs {
		if rvr.Spec.NodeName != "" {
			nodeNames = append(nodeNames, rvr.Spec.NodeName)
		}
	}
	for _, rva := range rvas {
		if rva.DeletionTimestamp == nil {
			nodeNames = append(nodeNames, rva.Spec.NodeName)
		}
	}
	slices.Sort(nodeNames)
	nodeNames = slices.Compact(nodeNames)

	// Filter eligible nodes using binary search, then sort by NodeName for rspView lookups.
	// The zone span is collected in the same pass but BEFORE the filter applies, so it describes
	// the pool rather than this volume's corner of it (see rspView.EligibleZones).
	eligibleNodes := make([]v1alpha1.ReplicatedStoragePoolEligibleNode, 0, len(nodeNames))
	eligibleZones := make([]string, 0, len(unsafeRSP.Status.EligibleNodes))
	for i := range unsafeRSP.Status.EligibleNodes {
		node := &unsafeRSP.Status.EligibleNodes[i]
		if node.ZoneName != "" {
			eligibleZones = append(eligibleZones, node.ZoneName)
		}
		_, found := slices.BinarySearch(nodeNames, node.NodeName)
		if found {
			// DeepCopy to avoid aliasing with cache (LVMVolumeGroups is a slice).
			eligibleNodes = append(eligibleNodes, *node.DeepCopy())
		}
	}
	slices.Sort(eligibleZones)
	eligibleZones = slices.Compact(eligibleZones)

	// Safety sort: RSP eligible nodes are sorted by NodeName in practice (rsp_controller
	// maintains sorted order), but we sort here defensively to guarantee the invariant
	// that rspView.FindEligibleNode relies on (binary search by NodeName).
	slices.SortFunc(eligibleNodes, func(a, b v1alpha1.ReplicatedStoragePoolEligibleNode) int {
		return cmp.Compare(a.NodeName, b.NodeName)
	})

	return &rspView{
		Type:               unsafeRSP.Spec.Type,
		EligibleZones:      eligibleZones,
		SystemNetworkNames: slices.Clone(unsafeRSP.Spec.SystemNetworkNames),
		EligibleNodes:      eligibleNodes,
	}, nil
}

// --- RSC ---

// getRSC fetches a ReplicatedStorageClass by name. Returns (nil, nil) if not found.
func (r *Reconciler) getRSC(ctx context.Context, name string) (*v1alpha1.ReplicatedStorageClass, error) {
	var rsc v1alpha1.ReplicatedStorageClass
	if err := r.cl.Get(ctx, client.ObjectKey{Name: name}, &rsc); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return nil, err
		}
		return nil, nil
	}
	return &rsc, nil
}

// --- RVA ---

// getRVAs lists ReplicatedVolumeAttachments for the given RV name,
// sorted by NodeName (primary), CreationTimestamp (secondary), Name (tertiary).
func (r *Reconciler) getRVAsSorted(ctx context.Context, rvName string) ([]*v1alpha1.ReplicatedVolumeAttachment, error) {
	var list v1alpha1.ReplicatedVolumeAttachmentList
	if err := r.cl.List(ctx, &list,
		client.MatchingFields{indexes.IndexFieldRVAByReplicatedVolumeName: rvName},
	); err != nil {
		return nil, err
	}

	// HACK: See comment in getRVRsSorted for rationale.
	result := make([]*v1alpha1.ReplicatedVolumeAttachment, len(list.Items))
	for i := range list.Items {
		result[i] = &list.Items[i]
	}

	slices.SortFunc(result, func(a, b *v1alpha1.ReplicatedVolumeAttachment) int {
		if c := cmp.Compare(a.Spec.NodeName, b.Spec.NodeName); c != 0 {
			return c
		}
		if c := a.CreationTimestamp.Compare(b.CreationTimestamp.Time); c != 0 {
			return c
		}
		return cmp.Compare(a.Name, b.Name)
	})
	return result, nil
}

func (r *Reconciler) patchRVA(ctx context.Context, obj, base *v1alpha1.ReplicatedVolumeAttachment) error {
	return r.cl.Patch(ctx, obj, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
}

func (r *Reconciler) patchRVAStatus(ctx context.Context, obj, base *v1alpha1.ReplicatedVolumeAttachment) error {
	return client.IgnoreNotFound(r.cl.Status().Patch(ctx, obj, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})))
}

// --- RVR ---

// getRVRsSorted lists ReplicatedVolumeReplicas from the cache for the given RV name,
// sorted by node name and ID (deterministic, ascending).
func (r *Reconciler) getRVRsSorted(ctx context.Context, rvName string) ([]*v1alpha1.ReplicatedVolumeReplica, error) {
	var list v1alpha1.ReplicatedVolumeReplicaList
	if err := r.cl.List(ctx, &list,
		client.MatchingFields{indexes.IndexFieldRVRByReplicatedVolumeName: rvName},
	); err != nil {
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
		if c := cmp.Compare(a.Spec.NodeName, b.Spec.NodeName); c != 0 {
			return c
		}
		return cmp.Compare(a.ID(), b.ID())
	})
	return result, nil
}

// createRVR creates the given ReplicatedVolumeReplica via the API. On success obj carries the
// server-assigned fields (uid, resourceVersion, defaults).
//
// The object is built by newRVR and its controller owner reference is set by the calling
// Reconcile method, which also decides what to do with the result (including inserting it into
// its local sorted slice via insertRVRSorted).
func (r *Reconciler) createRVR(ctx context.Context, obj *v1alpha1.ReplicatedVolumeReplica) error {
	return r.cl.Create(ctx, obj)
}

func (r *Reconciler) patchRVR(ctx context.Context, obj, base *v1alpha1.ReplicatedVolumeReplica) error {
	return r.cl.Patch(ctx, obj, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
}

// deleteRVR deletes the given ReplicatedVolumeReplica, guarded by its UID.
//
// An RVR name is the volume name plus the lowest free replica ID, so a name is reused as soon as
// its replica is gone. The UID precondition makes the delete hit the very object the caller decided
// about: a newer replica that took the name while the cache still shows the old one is answered
// with Conflict instead of being deleted. Patches are protected the same way by the optimistic lock
// (patchRVR); this is the delete-side counterpart. The resourceVersion is deliberately not part of
// the precondition: replica status changes constantly, and a stale status says nothing about which
// object a name refers to.
func (r *Reconciler) deleteRVR(ctx context.Context, obj *v1alpha1.ReplicatedVolumeReplica) error {
	if obj.DeletionTimestamp != nil {
		return nil
	}
	if err := client.IgnoreNotFound(r.cl.Delete(ctx, obj, client.Preconditions{UID: &obj.UID})); err != nil {
		return err
	}
	obj.DeletionTimestamp = ptr.To(metav1.Now())
	return nil
}

// deleteRVRWithForcedFinalizerRemoval forcibly removes the RV controller finalizer and deletes the RVR.
//
// WARNING: This bypasses normal datamesh cleanup — the RVR's finalizer is removed without
// checking whether it is still a datamesh member or has pending transitions. Use ONLY in
// "tear everything down" flows (formation restart, RV deletion) where the entire datamesh
// is being reset or destroyed. For normal RVR deletion, use deleteRVR and let
// reconcileRVRFinalizers remove the finalizer when datamesh cleanup completes.
//
// Exception: this is a composite helper (patch + delete = two API calls). It intentionally
// combines finalizer removal and deletion into one step for readability at call sites.
func (r *Reconciler) deleteRVRWithForcedFinalizerRemoval(ctx context.Context, obj *v1alpha1.ReplicatedVolumeReplica) error {
	// Remove finalizer if present.
	if obju.HasFinalizer(obj, v1alpha1.RVControllerFinalizer) {
		base := obj.DeepCopy()
		obju.RemoveFinalizer(obj, v1alpha1.RVControllerFinalizer)
		if err := r.patchRVR(ctx, obj, base); err != nil {
			if apierrors.IsNotFound(err) {
				// Object already deleted (stale cache). Mark as deleting and return.
				obj.DeletionTimestamp = ptr.To(metav1.Now())
				return nil
			}
			return flow.Wrapf(err, "removing finalizer")
		}
	}

	return r.deleteRVR(ctx, obj)
}
