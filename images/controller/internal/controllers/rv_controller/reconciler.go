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
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

func NewReconciler(cl client.Client, scheme *runtime.Scheme) *Reconciler {
	return &Reconciler{cl: cl, scheme: scheme}
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

	rvrs, err := r.getRVRsSorted(rf.Ctx(), req.Name)
	if err != nil {
		return rf.Failf(err, "listing ReplicatedVolumeReplicas").ToCtrl()
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

	// Perform main processing.
	if rv.Status.Configuration != nil {
		rsp, err := r.getRSP(rf.Ctx(), rv.Status.Configuration.ReplicatedStoragePoolName, rvrs, rvas)
		if err != nil {
			return rf.Failf(err, "getting RSP").ToCtrl()
		}
		if forming, formationStepIdx := isFormationInProgress(rv); forming {
			outcome = outcome.Merge(r.reconcileFormation(rf.Ctx(), rv, &rvrs, rvas, rsp, rsc, formationStepIdx))
		} else {
			outcome = flow.MergeReconciles(outcome,
				r.reconcileRVConfiguration(rf.Ctx(), rv, rsc),
				r.reconcileNormalOperation(rf.Ctx(), rv, &rvrs, rvas, rsp),
				r.reconcileLayoutStatus(rf.Ctx(), rv, rvrs, rvas),
			)
		}
		if outcome.ShouldReturn() {
			return outcome.ToCtrl()
		}
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
	metricObservations := computeDatameshMetricObservations(now, rv, base.Status.DatameshTransitions, rvrs)
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
// Reconcile: normal-operation
//

// reconcileNormalOperation handles the steady-state lifecycle of a formed datamesh
// (attach handling, scaling, etc.).
//
// Reconcile pattern: Pure orchestration
func (r *Reconciler) reconcileNormalOperation(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rvrs *[]*v1alpha1.ReplicatedVolumeReplica,
	rvas []*v1alpha1.ReplicatedVolumeAttachment,
	rsp *rspView,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "normal-operation")
	defer rf.OnEnd(&outcome)

	// Create Access RVRs for Active RVAs on nodes without any RVR.
	outcome = r.reconcileCreateAccessReplicas(rf.Ctx(), rv, rvrs, rvas, rsp)
	if outcome.ShouldReturn() {
		return outcome
	}

	// Run datamesh transition engine: membership, quorum, attachment, network transitions.
	changed, dmrctxs := datamesh.ProcessTransitions(
		rf.Ctx(), rv, rsp, *rvrs, rvas, datamesh.FeatureFlags{})
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
//   - P2 heal: create a missing TieBreaker replica when the diskful count is already correct
//     (e.g. a freshly formed r2 volume still at 2D, or a manually deleted TB). The scheduler
//     places it and it joins the datamesh via the standard tiebreaker/v1 plan. The same action
//     replaces a TieBreaker whose RVR is being deleted: it is still a member and still counted
//     by the raw layout, so the replacement deficit is computed separately
//     (computeTargetTieBreakerReplacement) — strict create-first, the datamesh releases the old
//     one only once the replacement is operational.
//
// Safety invariant: convergence NEVER creates a Diskful replica and NEVER deletes a replica or
// its data. The decision is a pure compute helper (computeTargetLayoutAction), reused by the
// LayoutConverged condition writer (reconcileLayoutStatus) so the condition and the action stay
// in agreement; convergence itself never writes the condition (single-writer invariant) and
// therefore discards the report half of that decision.
//
// After performing an action it returns ContinueAndRequeue: the split-client cache may be stale
// relative to our own write, so we requeue rather than rely on the watch (see
// controller-reconciliation.mdc). The outcome is deliberately NON-terminal — the root Reconcile
// must still reach its status patch, otherwise every status change computed in the acting pass
// (including the LayoutConverged report for exactly this action) is dropped (see
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
	// LayoutConverged condition); convergence acts on the target only.
	targetAction, _ := computeTargetLayoutAction(rv, *rvrs, rvas)
	switch targetAction.kind {
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

	case layoutActionCreateTieBreaker:
		// The name is chosen deterministically by newRVR (ChooseNewName), so a repeated Create
		// after a stale-cache retry converges via AlreadyExists.
		rvr, err := newRVR(rv, *rvrs, v1alpha1.ReplicaTypeTieBreaker, "")
		if err != nil {
			return rf.Failf(err, "creating tie-breaker RVR")
		}
		if _, err := obju.SetControllerRef(rvr, rv, r.scheme); err != nil {
			return rf.Failf(err, "creating tie-breaker RVR")
		}
		if err := r.createRVR(rf.Ctx(), rvr); err != nil {
			if apierrors.IsAlreadyExists(err) {
				// Expected race: a concurrent reconcile already created the TB under the same
				// deterministic name. Do not Get immediately (the cache may not yet contain it);
				// requeue and let the next pass observe it.
				rf.Log().Info("Converging layout: tie-breaker replica already exists, requeuing")
				return rf.ContinueAndRequeue()
			}
			return rf.Failf(err, "creating tie-breaker RVR")
		}
		*rvrs = insertRVRSorted(*rvrs, rvr)
		rf.Log().Info("Converging layout: created tie-breaker replica")
		return rf.ContinueAndRequeue()

	default: // layoutActionNone
		return rf.Continue()
	}
}

// layoutConvergedVolumeDeletingMessage is the LayoutConverged message published on both deletion
// paths (the normal-operation writer via computeTargetLayoutAction, and the early
// reconcileDeletion branch that an unattached RV reaches directly).
const layoutConvergedVolumeDeletingMessage = "volume is being deleted; layout convergence suspended"

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
)

// targetLayoutAction is the pure decision of what (if anything) layout convergence should do this
// pass. Computed by computeTargetLayoutAction and consumed by reconcileLayoutConvergence (to act).
//
// It carries the target only: the LayoutConverged report describing the same decision is a
// separate output (layoutConvergedReport), so status-shaped report data is never mixed into the
// target artifact (see controller-reconcile-helper-compute.mdc, "Patch-domain separation").
type targetLayoutAction struct {
	kind layoutActionKind
	// retypeRVRName is the RVR chosen for a P1 retype (set only for layoutActionRetypeToTieBreaker).
	retypeRVRName string
}

// layoutConvergedReport is the published LayoutConverged report describing the convergence
// decision. Computed by computeTargetLayoutAction alongside the target and consumed by the
// LayoutConverged condition writer (reconcileLayoutStatus), so the action and the condition never
// disagree.
type layoutConvergedReport struct {
	status  metav1.ConditionStatus
	reason  string
	message string
}

// computeTargetLayoutAction decides the single whitelisted convergence action (if any) that moves
// the actual datamesh layout toward the intended layout, and produces the LayoutConverged report
// as a SEPARATE output (target and report are never mixed into one artifact).
// It is pure (non-I/O, no mutation of inputs) and deterministic.
//
// Decision order (fixed — each earlier step wins over the later ones):
//  1. RV deletion → Unknown/VolumeDeleting, no action.
//  2. An active layout-changing transition → Converging, no action. This precedes the
//     actual/intended comparison on purpose: mid-flight D→TB makes the counted layout equal the
//     intended one for one step (the member is already a TieBreaker while the transition is still
//     running), and reporting Converged there would flip the condition True and back.
//  3. A retype requested in an earlier pass (spec flipped, DMTE not dispatched yet) → Converging.
//  4. A tie-breaker replacement deficit (a tie-breaker member whose RVR is being deleted has no
//     replacement yet) → create the replacement (strict create-first). This also precedes the
//     actual/intended comparison: the raw layout still counts the terminating tie-breaker, so the
//     comparison alone would report Converged while the only tie-breaker is leaving.
//  5. Comparison of actual against intended: equal → Converged; otherwise the whitelist below.
//
// Whitelist (only these two mismatches ever trigger an action; everything else is reported but
// never acted upon):
//   - P1 retype (r3→r2): actualD > intendedD && actualTB < intendedTB — convert one Diskful voter
//     into the missing tie-breaker. It never removes the extra diskful voters, so a 4D volume at
//     an r2 config becomes 3D+1TB after one retype and is then reported TransitionUnsupported.
//   - P2 heal: actualD == intendedD && actualTB < intendedTB — create the missing tie-breaker.
//
// Idempotency across the split-client cache is handled by counting in-flight work: a retype whose
// spec was already flipped (countPendingRetypesToTieBreaker, step 3) or a tie-breaker RVR already
// created but not yet a member (countPendingTieBreakerCreations) is reported without issuing a
// second action.
func computeTargetLayoutAction(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	rvas []*v1alpha1.ReplicatedVolumeAttachment,
) (targetLayoutAction, layoutConvergedReport) {
	// 1. Deletion: convergence is not evaluated, and no action is ever taken.
	if rv.DeletionTimestamp != nil {
		return targetLayoutAction{kind: layoutActionNone}, layoutConvergedReport{
			status:  metav1.ConditionUnknown,
			reason:  v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonVolumeDeleting,
			message: layoutConvergedVolumeDeletingMessage,
		}
	}

	intendedD, intendedTB := rv.Status.Configuration.IntendedLayout()
	actualD, actualTB := computeActualLayout(rv)
	actualLayout := formatLayout(actualD, actualTB)
	intendedLayout := formatLayout(intendedD, intendedTB)

	// 2. A layout-changing transition is already moving the composition → Converging, no new action.
	if hasLayoutChangingTransition(rv) {
		return targetLayoutAction{kind: layoutActionNone}, layoutConvergedReport{
			status:  metav1.ConditionFalse,
			reason:  v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverging,
			message: fmt.Sprintf("layout transition in progress: have %s, want %s", actualLayout, intendedLayout),
		}
	}

	// 3. A retype already requested in a previous pass (spec flipped, DMTE not started yet).
	if actualTB < intendedTB && countPendingRetypesToTieBreaker(rv, rvrs) >= intendedTB-actualTB {
		return targetLayoutAction{kind: layoutActionNone}, layoutConvergedReport{
			status:  metav1.ConditionFalse,
			reason:  v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverging,
			message: fmt.Sprintf("retype to tie-breaker requested: have %s, want %s", actualLayout, intendedLayout),
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

	// 5. Converged: actual matches intended.
	if actualD == intendedD && actualTB == intendedTB {
		return targetLayoutAction{kind: layoutActionNone}, layoutConvergedReport{
			status:  metav1.ConditionTrue,
			reason:  v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverged,
			message: fmt.Sprintf("layout converged: %s", actualLayout),
		}
	}

	switch {
	// P1 retype: too many diskful voters and a tie-breaker deficit.
	case actualD > intendedD && actualTB < intendedTB:
		name, noCandidateReason := selectRetypeCandidate(rv, rvrs, rvas)
		if name == "" {
			return targetLayoutAction{kind: layoutActionNone}, layoutConvergedReport{
				status: metav1.ConditionFalse,
				reason: v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonCannotConverge,
				message: fmt.Sprintf("cannot retype Diskful replica to tie-breaker (have %s, want %s): %s",
					actualLayout, intendedLayout, noCandidateReason),
			}
		}
		return targetLayoutAction{
				kind:          layoutActionRetypeToTieBreaker,
				retypeRVRName: name,
			}, layoutConvergedReport{
				status: metav1.ConditionFalse,
				reason: v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverging,
				message: fmt.Sprintf("retyping Diskful replica %s to tie-breaker: have %s, want %s",
					name, actualLayout, intendedLayout),
			}

	// P2 heal: correct diskful count but a tie-breaker deficit.
	case actualD == intendedD && actualTB < intendedTB:
		if countPendingTieBreakerCreations(rv, rvrs) >= intendedTB-actualTB {
			// The replica exists but cannot be placed: that is the scheduler's final word for
			// the current spec, not progress. Only a CURRENT Scheduled=False counts (see
			// computeActualPendingTieBreakerSchedulingFailure).
			if failure := computeActualPendingTieBreakerSchedulingFailure(rv, rvrs); failure != "" {
				return targetLayoutAction{kind: layoutActionNone}, layoutConvergedReport{
					status: metav1.ConditionFalse,
					reason: v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonCannotConverge,
					message: fmt.Sprintf("cannot place tie-breaker replica (have %s, want %s): %s",
						actualLayout, intendedLayout, failure),
				}
			}
			return targetLayoutAction{kind: layoutActionNone}, layoutConvergedReport{
				status:  metav1.ConditionFalse,
				reason:  v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverging,
				message: fmt.Sprintf("tie-breaker creation pending: have %s, want %s", actualLayout, intendedLayout),
			}
		}
		return targetLayoutAction{kind: layoutActionCreateTieBreaker}, layoutConvergedReport{
			status:  metav1.ConditionFalse,
			reason:  v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverging,
			message: fmt.Sprintf("creating tie-breaker replica: have %s, want %s", actualLayout, intendedLayout),
		}

	// Outside the whitelist: report honestly, take no action.
	default:
		return targetLayoutAction{kind: layoutActionNone}, layoutConvergedReport{
			status: metav1.ConditionFalse,
			reason: v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonTransitionUnsupported,
			message: fmt.Sprintf(
				"layout mismatch: have %s, want %s; automatic transition is not supported, manual intervention required",
				actualLayout, intendedLayout),
		}
	}
}

// computeTargetTieBreakerReplacement decides what convergence does about tie-breaker members
// that are leaving (their RVR is being deleted), and returns (action, report, true) when the
// tie-breaker replacement domain owns this pass. It returns (_, _, false) when nothing is leaving
// or when the situation is outside its scope, letting the caller fall through to the plain
// actual/intended comparison. Like its caller it keeps the target and the LayoutConverged report
// as separate outputs.
//
// Strict create-first: the datamesh only releases the old tie-breaker once its replacement is
// operational (see the datamesh guard guardTBSufficient), so the replacement must be created
// while the old one is still a member. The raw layout counts the terminating tie-breaker, so the
// deficit is computed SEPARATELY here, over non-deleting tie-breakers only; computeActualLayout
// stays raw and status.layout keeps reporting the honest composition (2D+2TB in the replacement
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
//	old gone                                     → not our business (the caller compares layouts)
//
// Scope: only the tie-breaker deficit created by the departure is handled. A wrong diskful count
// (actualD != intendedD) or a genuine tie-breaker surplus is left to the caller, which reports it
// honestly instead of piling a replacement on top of an unsupported layout.
func computeTargetTieBreakerReplacement(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	actualD, actualTB, intendedD, intendedTB int,
	actualLayout, intendedLayout string,
) (targetLayoutAction, layoutConvergedReport, bool) {
	leaving := deletingTieBreakerMemberNames(rv, rvrs)
	if len(leaving) == 0 || actualD != intendedD {
		return targetLayoutAction{}, layoutConvergedReport{}, false
	}

	// Tie-breakers that stay: the supply the intended layout can rely on.
	availableTB := actualTB - len(leaving)
	if availableTB > intendedTB {
		// A surplus beyond the departure — outside this domain, report it honestly.
		return targetLayoutAction{}, layoutConvergedReport{}, false
	}

	subject := fmt.Sprintf("tie-breaker %s is terminating", strings.Join(leaving, ", "))
	if len(leaving) > 1 {
		subject = fmt.Sprintf("tie-breakers %s are terminating", strings.Join(leaving, ", "))
	}
	layouts := fmt.Sprintf("have %s, want %s", actualLayout, intendedLayout)

	if availableTB == intendedTB {
		// The remaining tie-breakers already cover the intended layout — either a replacement
		// joined the datamesh, or none is needed at all. Releasing the leaving member is the
		// DMTE's decision (it waits until a replacement is operational), so there is nothing to
		// do here but report progress.
		waiting := "waiting for it to leave the datamesh"
		switch {
		case len(leaving) > 1:
			waiting = "waiting for them to leave the datamesh"
		case intendedTB > 0:
			waiting = "its replacement joined the datamesh, waiting for it to leave"
		}
		return targetLayoutAction{kind: layoutActionNone}, layoutConvergedReport{
			status:  metav1.ConditionFalse,
			reason:  v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverging,
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
			return targetLayoutAction{kind: layoutActionNone}, layoutConvergedReport{
				status: metav1.ConditionFalse,
				reason: v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonCannotConverge,
				message: fmt.Sprintf("%s: cannot place a replacement (%s): %s",
					subject, layouts, failure),
			}, true
		}
		return targetLayoutAction{kind: layoutActionNone}, layoutConvergedReport{
			status:  metav1.ConditionFalse,
			reason:  v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverging,
			message: fmt.Sprintf("%s: replacement tie-breaker creation pending (%s)", subject, layouts),
		}, true
	}

	return targetLayoutAction{kind: layoutActionCreateTieBreaker}, layoutConvergedReport{
		status:  metav1.ConditionFalse,
		reason:  v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverging,
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
// tie-breaker RVRs that exist but have not joined the datamesh yet. It returns a non-empty,
// human-readable summary only when at least one of them carries a Scheduled=False condition whose
// ObservedGeneration matches the RVR generation.
//
// A missing, Unknown or stale Scheduled condition is not a verdict — the scheduler simply has not
// (re-)evaluated this replica yet, so convergence is still in progress. A tie-breaker RVR that is
// being deleted is not a pending creation at all and is skipped (mirrors
// countPendingTieBreakerCreations).
func computeActualPendingTieBreakerSchedulingFailure(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
) string {
	var msgs []string
	for _, rvr := range rvrs {
		if rvr.Spec.Type != v1alpha1.ReplicaTypeTieBreaker || rvr.DeletionTimestamp != nil {
			continue
		}
		if rv.Status.Datamesh.FindMemberByName(rvr.Name) != nil {
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

// isMemberAttached reports whether the given diskful member is currently attached: either the
// datamesh member is marked Attached (published / DRBD Primary) or an active (non-deleting) RVA
// targets its node. Attached replicas are excluded as retype candidates to avoid disrupting
// live I/O.
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

// countPendingRetypesToTieBreaker counts diskful members whose RVR spec.type has already been
// flipped to TieBreaker (a retype requested in a previous pass, not yet reflected in the member
// type). Used to avoid issuing a second retype while the first is in flight.
func countPendingRetypesToTieBreaker(rv *v1alpha1.ReplicatedVolume, rvrs []*v1alpha1.ReplicatedVolumeReplica) int {
	count := 0
	for i := range rv.Status.Datamesh.Members {
		m := &rv.Status.Datamesh.Members[i]
		if m.Type != v1alpha1.DatameshMemberTypeDiskful && m.Type != v1alpha1.DatameshMemberTypeLiminalDiskful {
			continue
		}
		if rvr := findRVRByName(rvrs, m.Name); rvr != nil && rvr.Spec.Type == v1alpha1.ReplicaTypeTieBreaker {
			count++
		}
	}
	return count
}

// countPendingTieBreakerCreations counts TieBreaker RVRs that are not yet datamesh members
// (created in a previous pass, still being scheduled / joining). Used to avoid creating a second
// tie-breaker while the first is in flight.
//
// A tie-breaker RVR that is itself being deleted is not in flight towards membership — it is on
// its way out and will never satisfy the deficit, so it does not hold back a new creation.
func countPendingTieBreakerCreations(rv *v1alpha1.ReplicatedVolume, rvrs []*v1alpha1.ReplicatedVolumeReplica) int {
	count := 0
	for _, rvr := range rvrs {
		if rvr.Spec.Type != v1alpha1.ReplicaTypeTieBreaker || rvr.DeletionTimestamp != nil {
			continue
		}
		if rv.Status.Datamesh.FindMemberByName(rvr.Name) == nil {
			count++
		}
	}
	return count
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

	qmr = cfg.GuaranteedMinimumDataRedundancy + 1

	return q, qmr
}

// isTransZonalZoneCountValid checks whether the given zone count is valid for a TransZonal
// layout with the specified FTT/GMDR combination. Valid zone counts match the RSC-level
// CEL zone validation.
func isTransZonalZoneCountValid(ftt, gmdr byte, zoneCount int) bool {
	switch {
	case ftt == 0 && gmdr == 1:
		return zoneCount == 2
	case ftt == 1 && gmdr == 0:
		return zoneCount == 3
	case ftt == 1 && gmdr == 1:
		return zoneCount == 3
	case ftt == 1 && gmdr == 2:
		return zoneCount == 3 || zoneCount == 5
	case ftt == 2 && gmdr == 1:
		return zoneCount == 4
	case ftt == 2 && gmdr == 2:
		return zoneCount == 3 || zoneCount == 5
	default:
		return false // FTT=0,GMDR=0 is not TransZonal; unknown combos are invalid.
	}
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
// current spec generation, and it is applied only when the class rollout strategy allows it
// (see the NewVolumesOnly branch). ConfigurationGeneration always names the generation the
// stored content came from; ConfigurationObservedGeneration names the newest generation the
// volume has seen. The two differ exactly while a newer configuration is held back.
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
			changed = applyConfigurationReadyCondFalse(rv,
				v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonInvalidConfiguration,
				fmt.Sprintf("TransZonal with FTT=%d, GMDR=%d requires a valid zone count, RSP has %d zones",
					intended.FailuresToTolerate, intended.GuaranteedMinimumDataRedundancy, rspZoneCount))
			return rf.Continue().ReportChangedIf(changed)
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

// reconcileLayoutStatus is the SINGLE writer of the LayoutConverged condition and
// status.layout. It compares the actual datamesh layout (diskful voters + tie-breakers)
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

	changed := applyLayout(rv, report.layout)
	switch report.converged.status {
	case metav1.ConditionTrue:
		changed = applyLayoutConvergedCondTrue(rv, report.converged.reason, report.converged.message) || changed
	case metav1.ConditionUnknown:
		// Deletion: we no longer evaluate convergence (see computeTargetLayoutAction).
		changed = applyLayoutConvergedCondUnknown(rv, report.converged.reason, report.converged.message) || changed
	default:
		changed = applyLayoutConvergedCondFalse(rv, report.converged.reason, report.converged.message) || changed
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
		switch rv.Status.Datamesh.Members[i].Type {
		case v1alpha1.DatameshMemberTypeDiskful, v1alpha1.DatameshMemberTypeLiminalDiskful:
			diskful++
		case v1alpha1.DatameshMemberTypeTieBreaker:
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
// convergence progress (otherwise the LayoutConverged condition would flap on unrelated
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

// layoutReport is the computed report driving status.layout and the LayoutConverged condition.
type layoutReport struct {
	// layout mirrors the optional status.layout field: nil means "unset" (no layout to
	// publish), never the empty string. computeLayoutReport always fills it, because it only
	// runs post-formation, where the member composition is known; the pointer keeps the
	// "unset" state representable end-to-end so it is never conflated with "".
	layout *string
	// converged is the LayoutConverged report produced by the convergence decision.
	converged layoutConvergedReport
}

// computeLayoutReport produces the status.layout string and the LayoutConverged report by
// reusing the convergence decision (computeTargetLayoutAction), so the reported reason always
// matches what reconcileLayoutConvergence does this pass:
//   - actual == intended                                   → True / Converged
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
		layout:    ptr.To(formatLayout(actualD, actualTB)),
		converged: convergedReport,
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

// applyLayout sets status.layout (the actual datamesh layout string).
//
// status.layout is an optional scalar: nil (absent) means "not computed yet" and is NOT the
// same as the empty string, which is never a valid layout value. A nil layout therefore
// clears the field instead of publishing "".
func applyLayout(rv *v1alpha1.ReplicatedVolume, layout *string) bool {
	if ptr.Equal(rv.Status.Layout, layout) {
		return false
	}
	if layout == nil {
		rv.Status.Layout = nil
	} else {
		// Copy the value instead of sharing the report's pointer (read-only input contract).
		rv.Status.Layout = ptr.To(*layout)
	}
	return true
}

// applyLayoutConvergedCondTrue sets the LayoutConverged condition to True.
func applyLayoutConvergedCondTrue(rv *v1alpha1.ReplicatedVolume, reason, message string) bool {
	return obju.SetStatusCondition(rv, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeCondLayoutConvergedType,
		Status:  metav1.ConditionTrue,
		Reason:  reason,
		Message: message,
	})
}

// applyLayoutConvergedCondFalse sets the LayoutConverged condition to False.
func applyLayoutConvergedCondFalse(rv *v1alpha1.ReplicatedVolume, reason, message string) bool {
	return obju.SetStatusCondition(rv, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeCondLayoutConvergedType,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

// applyLayoutConvergedCondUnknown sets the LayoutConverged condition to Unknown.
func applyLayoutConvergedCondUnknown(rv *v1alpha1.ReplicatedVolume, reason, message string) bool {
	return obju.SetStatusCondition(rv, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeCondLayoutConvergedType,
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
	changed := applyLayoutConvergedCondUnknown(rv,
		v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonVolumeDeleting,
		layoutConvergedVolumeDeletingMessage)
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
	// Zones is the list of zones from RSP spec.
	Zones []string
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
	eligibleNodes := make([]v1alpha1.ReplicatedStoragePoolEligibleNode, 0, len(nodeNames))
	for i := range unsafeRSP.Status.EligibleNodes {
		node := &unsafeRSP.Status.EligibleNodes[i]
		_, found := slices.BinarySearch(nodeNames, node.NodeName)
		if found {
			// DeepCopy to avoid aliasing with cache (LVMVolumeGroups is a slice).
			eligibleNodes = append(eligibleNodes, *node.DeepCopy())
		}
	}

	// Safety sort: RSP eligible nodes are sorted by NodeName in practice (rsp_controller
	// maintains sorted order), but we sort here defensively to guarantee the invariant
	// that rspView.FindEligibleNode relies on (binary search by NodeName).
	slices.SortFunc(eligibleNodes, func(a, b v1alpha1.ReplicatedStoragePoolEligibleNode) int {
		return cmp.Compare(a.NodeName, b.NodeName)
	})

	return &rspView{
		Type:               unsafeRSP.Spec.Type,
		Zones:              slices.Clone(unsafeRSP.Spec.Zones),
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

// getRVRsSorted lists ReplicatedVolumeReplicas for the given RV name,
// sorted by ID (deterministic, ascending).
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

func (r *Reconciler) deleteRVR(ctx context.Context, obj *v1alpha1.ReplicatedVolumeReplica) error {
	if obj.DeletionTimestamp != nil {
		return nil
	}
	if err := client.IgnoreNotFound(r.cl.Delete(ctx, obj)); err != nil {
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
