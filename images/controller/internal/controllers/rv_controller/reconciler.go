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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
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
		return r.reconcileOrphanedRVAs(rf.Ctx(), req.Name).ToCtrl()
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
	// and reconciliation continues through the normal path (where reconcileRVAFinalizers
	// and the future attach/detach logic handle the graceful detach lifecycle).
	//
	// Once all attachments are fully resolved, we enter this branch and force-delete
	// all remaining child resources (RVRs, datamesh state) via reconcileDeletion.
	if rvShouldNotExist(rv) {
		// Order matters (Go evaluates arguments left to right):
		// 1. reconcileDeletion: set RVA conditions, force-delete RVRs, clear datamesh members.
		// 2. reconcileRVAFinalizers: remove finalizer from deleting RVAs (may trigger
		//    Kubernetes finalization = object deletion). Must run after reconcileDeletion,
		//    otherwise reconcileDeletion would try to patch conditions on an already-deleted RVA.
		// 3. reconcileMetadata: remove RV finalizer if no children remain.
		return flow.MergeReconciles(
			r.reconcileDeletion(rf.Ctx(), rv, rvas, &rvrs),
			r.reconcileRVAFinalizers(rf.Ctx(), rv, rvas),
			r.reconcileMetadata(rf.Ctx(), rv, rvrs),
		).ToCtrl()
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
	// During formation, config is frozen (only formation reset calls reconcileRVConfiguration).
	if rv.Status.Configuration == nil {
		outcome = outcome.Merge(r.reconcileRVConfiguration(rf.Ctx(), rv, rsc))
		if outcome.ShouldReturn() {
			return outcome.ToCtrl()
		}
	}

	// Preparatory actions.
	eo := flow.MergeEnsures(
		ensureDatameshReplicaRequests(rf.Ctx(), rv, rvrs),
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
			)
		}
		if outcome.ShouldReturn() {
			return outcome.ToCtrl()
		}
	}

	// Reconcile RVA and RVR finalizers.
	outcome = flow.MergeReconciles(
		outcome,
		r.reconcileRVAFinalizers(rf.Ctx(), rv, rvas),
		r.reconcileRVRFinalizers(rf.Ctx(), rv, rvrs),
	)
	if outcome.ShouldReturn() {
		return outcome.ToCtrl()
	}

	if outcome.DidChange() {
		if err := r.patchRVStatus(rf.Ctx(), rv, base); err != nil {
			return rf.Fail(err).ToCtrl()
		}
	}

	return outcome.ToCtrl()
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

	var atts *attachmentsSummary
	eo := flow.MergeEnsures(
		// Process datamesh Access replica membership transitions.
		ensureDatameshAccessReplicas(rf.Ctx(), rv, *rvrs, rsp),

		// Process attach/detach transitions.
		ensureDatameshAttachments(rf.Ctx(), rv, *rvrs, rvas, rsp, &atts),
	)
	if eo.Error() != nil {
		return rf.Fail(eo.Error())
	}
	outcome = outcome.WithChangeFrom(eo)

	outcome = outcome.Merge(
		// Update RVA conditions and status fields.
		r.reconcileRVAConditionsFromAttachmentsSummary(rf.Ctx(), atts),

		// Delete unnecessary Access RVRs (redundant or unused).
		r.reconcileDeleteAccessReplicas(rf.Ctx(), rv, rvrs, rvas),
	)

	return outcome
}

// computeDatameshTransitionProgressMessage builds a detailed transition message showing
// confirmation progress and errors from waiting replicas.
//
// skipError (optional) is called for each waiting replica that has a False condition
// from conditionTypes. If it returns true, the replica is not reported as an error.
func computeDatameshTransitionProgressMessage(
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	revision int64,
	mustConfirm, confirmed idset.IDSet,
	skipError func(id uint8, cond *metav1.Condition) bool,
	conditionTypes ...string,
) string {
	waiting := mustConfirm.Difference(confirmed)

	var msg strings.Builder
	fmt.Fprintf(&msg, "%d/%d replicas confirmed revision %d",
		confirmed.Len(), mustConfirm.Len(), revision)

	if waiting.IsEmpty() {
		return msg.String()
	}

	fmt.Fprintf(&msg, ". Waiting: [%s]", waiting)

	var found idset.IDSet
	errorGroups := 0
	for _, rvr := range rvrs {
		id := rvr.ID()
		if !waiting.Contains(id) {
			continue
		}
		found.Add(id)

		replicaHasError := false
		for _, condType := range conditionTypes {
			cond := obju.GetStatusCondition(rvr, condType)
			if cond == nil || cond.Status != metav1.ConditionFalse || cond.ObservedGeneration != rvr.Generation {
				continue
			}
			if skipError != nil && skipError(id, cond) {
				continue
			}

			if !replicaHasError {
				if errorGroups == 0 {
					msg.WriteString(". Errors: ")
				} else {
					msg.WriteString(" | ")
				}
				fmt.Fprintf(&msg, "#%d ", id)
				replicaHasError = true
				errorGroups++
			} else {
				msg.WriteString(", ")
			}
			fmt.Fprintf(&msg, "%s/%s", condType, cond.Reason)
			if cond.Message != "" {
				msg.WriteString(": ")
				msg.WriteString(cond.Message)
			}
		}
	}

	for id := range waiting.Difference(found).All() {
		if errorGroups == 0 {
			msg.WriteString(". Errors: ")
		} else {
			msg.WriteString(" | ")
		}
		fmt.Fprintf(&msg, "#%d Replica not found", id)
		errorGroups++
	}

	return msg.String()
}

// findRVRByID returns the RVR with the given ID, or nil if not found.
// rvrs must be sorted by ID (as returned by getRVRsSorted).
func findRVRByID(rvrs []*v1alpha1.ReplicatedVolumeReplica, id uint8) *v1alpha1.ReplicatedVolumeReplica {
	idx, found := slices.BinarySearchFunc(rvrs, id, func(rvr *v1alpha1.ReplicatedVolumeReplica, target uint8) int {
		return cmp.Compare(rvr.ID(), target)
	})
	if !found {
		return nil
	}
	return rvrs[idx]
}

// removeDatameshMembers removes members whose ID is in the given set.
// Returns true if any member was removed.
func removeDatameshMembers(rv *v1alpha1.ReplicatedVolume, ids idset.IDSet) bool {
	n := 0
	for i := range rv.Status.Datamesh.Members {
		if !ids.Contains(rv.Status.Datamesh.Members[i].ID()) {
			rv.Status.Datamesh.Members[n] = rv.Status.Datamesh.Members[i]
			n++
		}
	}
	if n == len(rv.Status.Datamesh.Members) {
		return false
	}
	rv.Status.Datamesh.Members = rv.Status.Datamesh.Members[:n]
	return true
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

// applyDatameshReplicaRequestMessage sets the Message field on the given pending replica
// transition. Returns true if the message was changed.
func applyDatameshReplicaRequestMessage(p *v1alpha1.ReplicatedVolumeDatameshReplicaRequest, msg string) bool {
	if p == nil || p.Message == msg {
		return false
	}
	p.Message = msg
	return true
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

// applyDatameshMemberAbsent removes members whose ID is NOT in the given set.
// Returns true if any member was removed.
func applyDatameshMemberAbsent(rv *v1alpha1.ReplicatedVolume, repIDs idset.IDSet) bool {
	n := 0
	for i := range rv.Status.Datamesh.Members {
		if repIDs.Contains(rv.Status.Datamesh.Members[i].ID()) {
			rv.Status.Datamesh.Members[n] = rv.Status.Datamesh.Members[i]
			n++
		}
	}
	if n == len(rv.Status.Datamesh.Members) {
		return false
	}
	rv.Status.Datamesh.Members = rv.Status.Datamesh.Members[:n]
	return true
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
	targetDiskfulCount byte,
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
		waitingCount, targetDiskfulCount, strings.Join(waitReasons, ", "))
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

// computeIntendedDiskfulReplicaCount returns the intended number of diskful replicas.
// D = FTT + GMDR + 1
func computeIntendedDiskfulReplicaCount(rv *v1alpha1.ReplicatedVolume) byte {
	cfg := rv.Status.Configuration
	return cfg.FailuresToTolerate + cfg.GuaranteedMinimumDataRedundancy + 1
}

// computeTargetQuorum computes Quorum and QuorumMinimumRedundancy from the
// effective layout (not from Configuration — those are the target, not reality).
//
//	qmr = effective_GMDR + 1
//	q   = floor(voters / 2) + 1, but at least floor(minD / 2) + 1
//	minD = effective_FTT + effective_GMDR + 1
func computeTargetQuorum(rv *v1alpha1.ReplicatedVolume) (q, qmr byte) {
	el := rv.Status.EffectiveLayout
	minD := el.FailuresToTolerate + el.GuaranteedMinimumDataRedundancy + 1

	minQ := minD/2 + 1
	voters := idset.FromWhere(rv.Status.Datamesh.Members, func(m v1alpha1.DatameshMember) bool {
		return m.Type.IsVoter()
	})
	quorum := byte(voters.Len()/2 + 1)
	q = max(quorum, minQ)

	qmr = el.GuaranteedMinimumDataRedundancy + 1

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

// computeActualQuorum checks whether at least one voting replica has quorum and a ready agent.
// Returns (true, "") if quorum is satisfied, or (false, diagnostic) with a diagnostic detail otherwise.
func computeActualQuorum(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
) (satisfied bool, diagnostic string) {
	voters := idset.FromWhere(rv.Status.Datamesh.Members, func(m v1alpha1.DatameshMember) bool {
		return m.Type.IsVoter()
	})
	agentNotReady := idset.FromWhere(rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		return obju.StatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondReadyType).
			ReasonEqual(v1alpha1.ReplicatedVolumeReplicaCondReadyReasonAgentNotReady).
			Eval()
	})
	withQuorum := idset.FromWhere(rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		return rvr.Status.Quorum != nil && *rvr.Status.Quorum
	}).Intersect(voters).Difference(agentNotReady)

	if !withQuorum.IsEmpty() {
		return true, ""
	}

	// Diagnostic: which voter replicas exist and why they don't count.
	allRVRIDs := idset.FromAll(rvrs)
	voterAgentNotReady := voters.Intersect(agentNotReady)
	voterNoQuorum := voters.Intersect(allRVRIDs).Difference(agentNotReady)

	switch {
	case !voterNoQuorum.IsEmpty() && !voterAgentNotReady.IsEmpty():
		diagnostic = fmt.Sprintf("no quorum on [%s]; agent not ready on [%s]",
			voterNoQuorum, voterAgentNotReady)
	case !voterNoQuorum.IsEmpty():
		diagnostic = fmt.Sprintf("no quorum on [%s]", voterNoQuorum)
	case !voterAgentNotReady.IsEmpty():
		diagnostic = fmt.Sprintf("agent not ready on [%s]", voterAgentNotReady)
	default:
		diagnostic = "no voter replicas available"
	}
	return false, diagnostic
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
//   - Formation reset: after clearing Configuration to nil (re-derive)
//
// During formation, callers do NOT call this function (config is frozen).
//
// Generation semantics by mode:
//   - Auto mode: ConfigurationGeneration = RSC's Status.ConfigurationGeneration
//   - Manual mode: ConfigurationGeneration = rv.Generation (any spec field bumps it;
//     deep-compare prevents false-positive config updates)
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
		intended = rsc.Status.Configuration
		intendedGeneration = rsc.Status.ConfigurationGeneration
	}

	// Fast-path: config content matches intended → update generation tracking, skip the rest.
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

// rvShouldNotExist returns true if RV should be deleted:
// DeletionTimestamp is set, no finalizers except ours, no attached members,
// and no Detach transitions in progress.
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

	// Step 3: Clear datamesh members.
	if len(rv.Status.Datamesh.Members) > 0 {
		base := rv.DeepCopy()
		rv.Status.Datamesh.Members = nil
		if err := r.patchRVStatus(rf.Ctx(), rv, base); err != nil {
			return rf.Failf(err, "clearing datamesh members")
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
		return cmp.Compare(a.ID(), b.ID())
	})
	return result, nil
}

// createDiskfulRVR creates a Diskful RVR (nodeName is left empty for the scheduler to assign).
func (r *Reconciler) createDiskfulRVR(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rvrs *[]*v1alpha1.ReplicatedVolumeReplica,
) (*v1alpha1.ReplicatedVolumeReplica, error) {
	return r.createRVR(ctx, rv, rvrs, v1alpha1.ReplicaTypeDiskful, "")
}

// createAccessRVR creates an Access RVR on the specified node.
func (r *Reconciler) createAccessRVR(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rvrs *[]*v1alpha1.ReplicatedVolumeReplica,
	nodeName string,
) (*v1alpha1.ReplicatedVolumeReplica, error) {
	return r.createRVR(ctx, rv, rvrs, v1alpha1.ReplicaTypeAccess, nodeName)
}

// createRVR constructs a new ReplicatedVolumeReplica (choosing a free ID name,
// adding the RV controller finalizer, setting controller owner ref), creates it via the API,
// and inserts it into rvrs in sorted order.
//
// Exception: this is a composite create helper — it performs object construction, finalizer
// setup, and caller-slice mutation beyond a simple single-call Create. This is intentional
// to keep the formation loop readable; all policy decisions (when to create, how many)
// remain in the calling Reconcile method.
//
// Prefer using typed wrappers: createDiskfulRVR, createAccessRVR.
func (r *Reconciler) createRVR(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rvrs *[]*v1alpha1.ReplicatedVolumeReplica,
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
	if !rvr.ChooseNewName(*rvrs) {
		return nil, fmt.Errorf("no available ID for new RVR")
	}
	obju.AddFinalizer(rvr, v1alpha1.RVControllerFinalizer)
	if _, err := obju.SetControllerRef(rvr, rv, r.scheme); err != nil {
		return nil, err
	}
	if err := r.cl.Create(ctx, rvr); err != nil {
		return nil, err
	}
	// Insert in sorted order by ID.
	rvrID := rvr.ID()
	idx, _ := slices.BinarySearchFunc(*rvrs, rvrID, func(r *v1alpha1.ReplicatedVolumeReplica, id uint8) int {
		return cmp.Compare(r.ID(), id)
	})
	*rvrs = slices.Insert(*rvrs, idx, rvr)
	return rvr, nil
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
