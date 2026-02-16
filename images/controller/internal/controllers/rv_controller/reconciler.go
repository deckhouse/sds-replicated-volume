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
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"slices"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/drbd_size"
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

	// Load RSC.
	rsc, err := r.getRSC(rf.Ctx(), rv.Spec.ReplicatedStorageClassName)
	if err != nil {
		return rf.Failf(err, "getting ReplicatedStorageClass").ToCtrl()
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

	// Preparatory actions.
	eo := flow.MergeEnsures(
		ensureRVConfiguration(rf.Ctx(), rv, rsc),
		ensureDatameshPendingReplicaTransitions(rf.Ctx(), rv, rvrs),
	)
	if eo.Error() != nil {
		return rf.Fail(eo.Error()).ToCtrl()
	}
	outcome = outcome.WithChangeFrom(eo)

	// Perform main processing.
	if rv.Status.Configuration != nil {
		rsp, err := r.getRSP(rf.Ctx(), rv.Status.Configuration.StoragePoolName, rvrs, rvas)
		if err != nil {
			return rf.Failf(err, "getting RSP").ToCtrl()
		}
		if forming, formationPhase := isFormationInProgress(rv); forming {
			outcome = outcome.Merge(r.reconcileFormation(rf.Ctx(), rv, &rvrs, rvas, rsp, rsc, formationPhase))
		} else {
			outcome = outcome.Merge(r.reconcileNormalOperation(rf.Ctx(), rv, &rvrs, rvas, rsp))
		}
		if outcome.ShouldReturn() {
			return outcome.ToCtrl()
		}
	}

	// Ensure conditions.
	eo = flow.MergeEnsures(
		ensureConditionConfigurationReady(rf.Ctx(), rv, rsc),
	)
	if eo.Error() != nil {
		return rf.Fail(eo.Error()).ToCtrl()
	}
	outcome = outcome.WithChangeFrom(eo)

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
// Reconcile: formation
//

// Reconcile pattern: Pure orchestration
func (r *Reconciler) reconcileFormation(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rvrs *[]*v1alpha1.ReplicatedVolumeReplica,
	rvas []*v1alpha1.ReplicatedVolumeAttachment,
	rsp *rspView,
	rsc *v1alpha1.ReplicatedStorageClass,
	phase v1alpha1.ReplicatedVolumeFormationPhase,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "formation")
	defer rf.OnEnd(&outcome)

	switch phase {
	case v1alpha1.ReplicatedVolumeFormationPhasePreconfigure, "":
		outcome = r.reconcileFormationPhasePreconfigure(rf.Ctx(), rv, rvrs, rsp, rsc)
	case v1alpha1.ReplicatedVolumeFormationPhaseEstablishConnectivity:
		outcome = r.reconcileFormationPhaseEstablishConnectivity(rf.Ctx(), rv, rvrs, rsp, rsc)
	case v1alpha1.ReplicatedVolumeFormationPhaseBootstrapData:
		outcome = r.reconcileFormationPhaseBootstrapData(rf.Ctx(), rv, rvrs, rsp, rsc)
	default:
		return rf.Fail(fmt.Errorf("invalid formation phase: %s", phase))
	}

	// Set "waiting" conditions on all RVAs — datamesh is not ready yet.
	outcome = outcome.Merge(r.reconcileRVAWaiting(rf.Ctx(), rvas, "Datamesh formation is in progress"))

	return outcome
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: formation-preconfigure
//

// reconcileFormationPhasePreconfigure handles initial formation: creates replicas, waits for them
// to become preconfigured, and initializes datamesh configuration.
//
// Reconcile pattern: Pure orchestration
func (r *Reconciler) reconcileFormationPhasePreconfigure(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rvrs *[]*v1alpha1.ReplicatedVolumeReplica,
	rsp *rspView,
	rsc *v1alpha1.ReplicatedStorageClass,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "preconfigure")
	defer rf.OnEnd(&outcome)

	changed := applyFormationTransition(rv, v1alpha1.ReplicatedVolumeFormationPhasePreconfigure)
	if changed {
		// Initialize datamesh configuration. These values are set once and not synchronized
		// with external intended state during formation (RSP can't change while
		// status.Configuration is locked).
		//
		// Required for replicas to reach pre-configured state:
		//   - SystemNetworkNames: without it replicas can't report addresses
		//   - Size: without it replicas can't order backing volumes
		//
		rv.Status.DatameshRevision = 1
		rv.Status.Datamesh.SystemNetworkNames = rsp.SystemNetworkNames
		rv.Status.Datamesh.Size = rv.Spec.Size
		

		applyFormationTransitionMessage(rv, "Starting preconfigure phase")
	}

	// Replicas placed on nodes that violate eligible nodes constraints.
	// These need to be replaced with replicas on eligible nodes.
	misplaced := idset.FromWhere(*rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		return rvr.DeletionTimestamp == nil &&
			obju.StatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType).
				IsFalse().
				ObservedGenerationCurrent().
				Eval()
	})

	// Replicas that are still being deleted.
	deleting := idset.FromWhere(*rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		return rvr.DeletionTimestamp != nil
	})

	// Collect diskful replicas.
	diskful := idset.FromWhere(*rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		return rvr.Spec.Type == v1alpha1.ReplicaTypeDiskful
	})

	// Ignore misplaced replicas.
	diskful = diskful.Difference(misplaced)

	// Ignore deleting replicas.
	diskful = diskful.Difference(deleting)

	targetDiskfulCount := computeIntendedDiskfulReplicaCount(rv)

	// Create missing diskful replicas only when there are no replicas being deleted or misplaced.
	// This prevents zombie accumulation: we wait for all cleanup to finish before creating new ones.
	if deleting.IsEmpty() && misplaced.IsEmpty() {
		for diskful.Len() < targetDiskfulCount {
			rvr, err := r.createDiskfulRVR(rf.Ctx(), rv, rvrs)
			if err != nil {
				if apierrors.IsAlreadyExists(err) {
					// Stale cache: RVR was already created by a previous reconciliation. Requeue to pick it up.
					rf.Log().Info("RVR already exists, requeueing")
					return rf.DoneAndRequeue()
				}
				return rf.Failf(err, "creating diskful RVR")
			}
			diskful.Add(rvr.ID())
		}
	}

	// Replicas that have been assigned to a node by the scheduler.
	// ObservedGenerationCurrent ensures the scheduling decision is up-to-date with the current spec.
	scheduled := idset.FromWhere(*rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		return obju.StatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondScheduledType).
			IsTrue().
			ObservedGenerationCurrent().
			Eval()
	})

	// Replicas ready to join the datamesh: DRBD is preconfigured and waiting for membership.
	// These replicas have:
	// - a pending transition with Member=true (signaling readiness to become a datamesh member),
	// - DRBDConfigured condition with reason PendingDatameshJoin (DRBD setup complete, awaiting membership).
	preconfigured := idset.FromWhere(*rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		pt := rvr.Status.DatameshPendingTransition
		return pt != nil && pt.Member != nil && *pt.Member &&
			obju.StatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType).
				ReasonEqual(v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonPendingDatameshJoin).
				ObservedGenerationCurrent().
				Eval()
	})

	// Remove excess diskful replicas (prefer higher ID).
	// Priority: prefer deleting replicas that are less progressed (not scheduled > not preconfigured > any).
	for diskful.Len() > targetDiskfulCount {
		var candidates idset.IDSet
		if ns := diskful.Difference(scheduled); !ns.IsEmpty() {
			candidates = ns
		} else if np := diskful.Difference(preconfigured); !np.IsEmpty() {
			candidates = np
		} else {
			candidates = diskful
		}
		diskful.Remove(candidates.Max())
	}

	// Delete all replicas not in diskful (misplaced, excess, etc.).
	for _, rvr := range *rvrs {
		if !diskful.Contains(rvr.ID()) {
			if err := r.deleteRVRWithForcedFinalizerRemoval(rf.Ctx(), rvr); err != nil {
				return rf.Failf(err, "deleting RVR %s", rvr.Name)
			}
		}
	}

	// Wait for all deleting RVRs to be fully removed before proceeding with formation.
	// Replicas may be deleting for various reasons: leftover from a previous formation cycle,
	// excess replicas removed above, misplaced replicas, or externally created replicas.
	// If deletion takes too long, restart formation.
	deleting = idset.FromWhere(*rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		return rvr.DeletionTimestamp != nil
	})
	if !deleting.IsEmpty() {
		msg := fmt.Sprintf(
			"Waiting for %d deleting replicas [%s] to be fully removed before proceeding with formation",
			deleting.Len(), deleting.String(),
		)
		changed = applyFormationTransitionMessage(rv, msg) || changed
		changed = applyPendingReplicaMessages(
			rv, diskful,
			"Datamesh is forming, waiting for replica cleanup to complete",
		) || changed
		return r.reconcileFormationRestartIfTimeoutPassed(rf.Ctx(), rv, rvrs, rsc, 30*time.Second).
			ReportChangedIf(changed)
	}

	// Replicas waiting to be scheduled.
	waitingScheduling := diskful.Difference(scheduled)

	// Split waitingScheduling: replicas where scheduling explicitly failed vs not yet processed.
	schedulingFailed := idset.FromWhere(*rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		return waitingScheduling.Contains(rvr.ID()) &&
			obju.StatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondScheduledType).
				IsFalse().
				Eval()
	})
	pendingScheduling := waitingScheduling.Difference(schedulingFailed)

	// Replicas scheduled but waiting for preconfiguration.
	waitingPreconfiguration := scheduled.Intersect(diskful).Difference(preconfigured)

	// Wait for replicas to become preconfigured; restart formation if timeout exceeded.
	if !waitingScheduling.IsEmpty() || !waitingPreconfiguration.IsEmpty() {
		msg := computeFormationPreconfigureWaitMessage(*rvrs, targetDiskfulCount,
			pendingScheduling, schedulingFailed, waitingPreconfiguration)
		changed = applyFormationTransitionMessage(rv, msg) || changed
		changed = applyPendingReplicaMessages(
			rv, preconfigured,
			"Datamesh is forming, waiting for other replicas to become preconfigured",
		) || changed
		return r.reconcileFormationRestartIfTimeoutPassed(rf.Ctx(), rv, rvrs, rsc, 30*time.Second).
			ReportChangedIf(changed)
	}

	// Verify all diskful replicas have addresses for all required SystemNetworkNames (safety check).
	requiredNetworks := rv.Status.Datamesh.SystemNetworkNames
	missingAddresses := idset.FromWhere(*rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		if !diskful.Contains(rvr.ID()) {
			return false
		}
		// Check if RVR has addresses for all required networks.
		matchCount := 0
		for _, addr := range rvr.Status.Addresses {
			if slices.Contains(requiredNetworks, addr.SystemNetworkName) {
				matchCount++
			}
		}
		return matchCount != len(requiredNetworks)
	})
	if !missingAddresses.IsEmpty() {
		okReplicas := diskful.Difference(missingAddresses)

		msg := fmt.Sprintf(
			"Address configuration mismatch: replicas [%s] do not have addresses for all required networks %v. "+
				"This should not happen during normal formation. "+
				"If not resolved automatically, formation will be restarted",
			missingAddresses.String(), requiredNetworks,
		)
		changed = applyFormationTransitionMessage(rv, msg) || changed
		changed = applyPendingReplicaMessages(
			rv, okReplicas,
			"Datamesh is forming, waiting for other replicas to report required addresses",
		) || changed
		changed = applyPendingReplicaMessages(
			rv, missingAddresses,
			"Replica addresses do not match required network configuration, blocking datamesh formation",
		) || changed
		return r.reconcileFormationRestartIfTimeoutPassed(rf.Ctx(), rv, rvrs, rsc, 30*time.Second).
			ReportChangedIf(changed)
	}

	// Verify all diskful replicas are on eligible nodes (safety check).
	notEligible := idset.FromWhere(*rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		return diskful.Contains(rvr.ID()) && rsp.FindEligibleNode(rvr.Spec.NodeName) == nil
	})
	if !notEligible.IsEmpty() {
		okReplicas := diskful.Difference(notEligible)

		msg := fmt.Sprintf(
			"Replicas [%s] are placed on nodes not in eligible nodes list. "+
				"This should not happen during normal formation. "+
				"If not resolved automatically, formation will be restarted",
			notEligible.String(),
		)
		changed = applyFormationTransitionMessage(rv, msg) || changed
		changed = applyPendingReplicaMessages(
			rv, okReplicas,
			"Datamesh is forming, waiting for other replicas to be on eligible nodes",
		) || changed
		changed = applyPendingReplicaMessages(
			rv, notEligible,
			"Replica is placed on a node not in the eligible nodes list, blocking datamesh formation",
		) || changed
		return r.reconcileFormationRestartIfTimeoutPassed(rf.Ctx(), rv, rvrs, rsc, 30*time.Second).
			ReportChangedIf(changed)
	}

	// Verify spec matches pending transition (safety check against spec changes during formation).
	specMismatch := idset.FromWhere(*rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		if !diskful.Contains(rvr.ID()) {
			return false
		}
		pt := rvr.Status.DatameshPendingTransition
		if pt == nil {
			return false
		}
		return rvr.Spec.Type != pt.Type ||
			rvr.Spec.LVMVolumeGroupName != pt.LVMVolumeGroupName ||
			rvr.Spec.LVMVolumeGroupThinPoolName != pt.ThinPoolName
	})
	if !specMismatch.IsEmpty() {
		okReplicas := diskful.Difference(specMismatch)

		msg := fmt.Sprintf(
			"Replicas [%s] have spec changes that don't match pending transition. "+
				"This may indicate spec changed during formation. "+
				"If not resolved automatically, formation will be restarted",
			specMismatch.String(),
		)
		changed = applyFormationTransitionMessage(rv, msg) || changed
		changed = applyPendingReplicaMessages(
			rv, okReplicas,
			"Datamesh is forming, waiting for other replicas to resolve spec mismatch",
		) || changed
		changed = applyPendingReplicaMessages(
			rv, specMismatch,
			"Replica spec does not match pending transition, blocking datamesh formation",
		) || changed
		return r.reconcileFormationRestartIfTimeoutPassed(rf.Ctx(), rv, rvrs, rsc, 30*time.Second).
			ReportChangedIf(changed)
	}

	// Verify backing volume size is sufficient for datamesh (safety check).
	insufficientSize := idset.FromWhere(*rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		if !diskful.Contains(rvr.ID()) {
			return false
		}
		if rvr.Status.BackingVolume == nil || rvr.Status.BackingVolume.Size == nil {
			return false // Size not yet known, will be checked later.
		}
		usable := drbd_size.UsableSize(*rvr.Status.BackingVolume.Size)
		return usable.Cmp(rv.Status.Datamesh.Size) < 0
	})
	if !insufficientSize.IsEmpty() {
		okReplicas := diskful.Difference(insufficientSize)

		msg := fmt.Sprintf(
			"Replicas [%s] have insufficient backing volume size for datamesh (required: %s). "+
				"This should not happen during normal formation. "+
				"If not resolved automatically, formation will be restarted",
			insufficientSize.String(), rv.Status.Datamesh.Size.String(),
		)
		changed = applyFormationTransitionMessage(rv, msg) || changed
		changed = applyPendingReplicaMessages(
			rv, okReplicas,
			"Datamesh is forming, waiting for other replicas to resolve backing volume size issue",
		) || changed
		changed = applyPendingReplicaMessages(
			rv, insufficientSize,
			"Replica backing volume size is insufficient for datamesh, blocking formation",
		) || changed
		return r.reconcileFormationRestartIfTimeoutPassed(rf.Ctx(), rv, rvrs, rsc, 30*time.Second).
			ReportChangedIf(changed)
	}

	// Passing rf.Ctx() produces nested logger scope in the callee — intentional for traceability.
	return r.reconcileFormationPhaseEstablishConnectivity(rf.Ctx(), rv, rvrs, rsp, rsc)
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: formation-establish-connectivity
//

// reconcileFormationPhaseEstablishConnectivity handles post-preconfiguration formation: validates datamesh
// membership consistency, establishes connectivity, and completes formation.
//
// Reconcile pattern: Pure orchestration
func (r *Reconciler) reconcileFormationPhaseEstablishConnectivity(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rvrs *[]*v1alpha1.ReplicatedVolumeReplica,
	rsp *rspView,
	rsc *v1alpha1.ReplicatedStorageClass,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "establish-connectivity")
	defer rf.OnEnd(&outcome)

	changed := applyFormationTransition(rv, v1alpha1.ReplicatedVolumeFormationPhaseEstablishConnectivity)
	if changed {
		applyFormationTransitionMessage(rv, "Starting establish connectivity phase")
	}

	// Collect active diskful replicas (not being deleted).
	diskful := idset.FromWhere(*rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		return rvr.Spec.Type == v1alpha1.ReplicaTypeDiskful && rvr.DeletionTimestamp == nil
	})

	if len(rv.Status.Datamesh.Members) == 0 {
		// Replicas need shared secret to establish peer connections.
		secret, err := generateSharedSecret()
		if err != nil {
			return rf.Fail(err)
		}
		rv.Status.Datamesh.SharedSecretAlg = v1alpha1.SharedSecretAlgSHA256
		rv.Status.Datamesh.SharedSecret = secret

		// Add diskful replicas as datamesh members.
		for _, rvr := range *rvrs {
			if !diskful.Contains(rvr.ID()) {
				continue
			}

			// Find zone from rsp.EligibleNodes.
			var zone string
			if en := rsp.FindEligibleNode(rvr.Spec.NodeName); en != nil {
				zone = en.ZoneName
			}

			// We could use rv.Status.DatameshPendingReplicaTransitions, but that would require
			// searching by name. ensureDatameshPendingReplicaTransitions called earlier guarantees
			// these fields match.
			pt := rvr.Status.DatameshPendingTransition

			applyDatameshMember(rv, v1alpha1.ReplicatedVolumeDatameshMember{
				Name:                       rvr.Name,
				Type:                       pt.Type,
				NodeName:                   rvr.Spec.NodeName,
				Zone:                       zone,
				Addresses:                  slices.Clone(rvr.Status.Addresses),
				LVMVolumeGroupName:         pt.LVMVolumeGroupName,
				LVMVolumeGroupThinPoolName: pt.ThinPoolName,
			})
		}

		// Quorum settings control DRBD split-brain prevention based on replica count.
		quorum, quorumMinimumRedundancy := computeTargetQuorum(rv)
		rv.Status.Datamesh.Quorum = quorum
		rv.Status.Datamesh.QuorumMinimumRedundancy = quorumMinimumRedundancy

		rv.Status.DatameshRevision++
		applyPendingReplicaMessages(rv, diskful, "Datamesh is forming, waiting for replica to apply new configuration")
		applyFormationTransitionMessage(rv, fmt.Sprintf(
			"Replicas [%s] preconfigured and added to datamesh. Waiting for them to establish connections",
			diskful.String(),
		))

		return rf.Continue().ReportChanged()
	}

	// Verify datamesh members match active replicas (safety check).
	dmDiskful := idset.FromWhere(rv.Status.Datamesh.Members, func(m v1alpha1.ReplicatedVolumeDatameshMember) bool {
		return m.Type == v1alpha1.ReplicaTypeDiskful
	})
	if dmDiskful != diskful {
		msg := fmt.Sprintf(
			"Datamesh members mismatch: datamesh has [%s], but active RVRs are [%s]. "+
				"This may be caused by manual intervention during formation. "+
				"If not resolved automatically, formation will be restarted",
			dmDiskful.String(), diskful.String(),
		)
		changed = applyFormationTransitionMessage(rv, msg) || changed
		changed = applyPendingReplicaMessages(rv, diskful, msg) || changed
		return r.reconcileFormationRestartIfTimeoutPassed(rf.Ctx(), rv, rvrs, rsc, 30*time.Second).
			ReportChangedIf(changed)
	}

	// Verify all diskful replicas are fully configured for the current datamesh revision.
	configured := idset.FromWhere(*rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		return diskful.Contains(rvr.ID()) &&
			rvr.Status.DatameshRevision == rv.Status.DatameshRevision &&
			obju.StatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType).
				IsTrue().
				ObservedGenerationCurrent().
				Eval()
	})
	if configured != diskful {
		notConfigured := diskful.Difference(configured)
		changed = applyFormationTransitionMessage(rv, fmt.Sprintf(
			"Waiting for replicas [%s] to be fully configured for datamesh revision %d",
			notConfigured.String(), rv.Status.DatameshRevision,
		)) || changed
		changed = applyPendingReplicaMessages(rv, notConfigured, "Datamesh is forming, waiting for DRBD configuration to continue") || changed
		changed = applyPendingReplicaMessages(rv, configured, "Datamesh is forming, DRBD configured, waiting for other replicas") || changed
		return r.reconcileFormationRestartIfTimeoutPassed(rf.Ctx(), rv, rvrs, rsc, 30*time.Second).
			ReportChangedIf(changed)
	}

	// Verify all diskful replicas are connected to each other.
	connected := idset.FromWhere(*rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		if !diskful.Contains(rvr.ID()) {
			return false
		}
		// Expected peers: all other diskful replicas.
		expectedPeers := diskful.Difference(idset.Of(rvr.ID()))
		// Actual peers: only diskful with ConnectionStateConnected.
		actualPeers := idset.FromWhere(rvr.Status.Peers, func(p v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus) bool {
			return p.Type == v1alpha1.ReplicaTypeDiskful &&
				p.ConnectionState == v1alpha1.ConnectionStateConnected
		})
		return actualPeers == expectedPeers
	})
	if connected != diskful {
		notConnected := diskful.Difference(connected)
		changed = applyFormationTransitionMessage(rv, fmt.Sprintf(
			"Waiting for replicas [%s] to establish connections with all peers",
			notConnected.String(),
		)) || changed
		changed = applyPendingReplicaMessages(rv, diskful, "Datamesh is forming, waiting for all replicas to connect to each other") || changed
		return r.reconcileFormationRestartIfTimeoutPassed(rf.Ctx(), rv, rvrs, rsc, 30*time.Second).
			ReportChangedIf(changed)
	}

	// Verify all diskful replicas are ready for data bootstrap:
	// - backing volume in Inconsistent state (normal for freshly created volume before initial sync), and
	// - Established replication with all diskful peers.
	// This is the special case during formation: all peers have Inconsistent backing volumes
	// with UUID_JUST_CREATED flag. DRBD establishes the connection between such peers
	// before any resync is triggered.
	readyForDataBootstrap := idset.FromWhere(*rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		if !diskful.Contains(rvr.ID()) {
			return false
		}
		// Backing volume must be Inconsistent.
		if rvr.Status.BackingVolume == nil || rvr.Status.BackingVolume.State != v1alpha1.DiskStateInconsistent {
			return false
		}
		// All diskful peers must have ReplicationState == Established.
		expectedPeers := diskful.Difference(idset.Of(rvr.ID()))
		replicationEstablishedPeers := idset.FromWhere(rvr.Status.Peers, func(p v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus) bool {
			return p.Type == v1alpha1.ReplicaTypeDiskful &&
				p.ReplicationState == v1alpha1.ReplicationStateEstablished
		})
		return replicationEstablishedPeers == expectedPeers
	})
	if readyForDataBootstrap != diskful {
		notReady := diskful.Difference(readyForDataBootstrap)
		changed = applyFormationTransitionMessage(rv, fmt.Sprintf(
			"Waiting for replicas [%s] to be ready for data bootstrap "+
				"(backing volume Inconsistent + Established replication with all peers)",
			notReady.String(),
		)) || changed
		changed = applyPendingReplicaMessages(rv, notReady, "Datamesh is forming, waiting for data bootstrap readiness (requires backing volume Inconsistent and replication Established with all peers)") || changed
		changed = applyPendingReplicaMessages(rv, readyForDataBootstrap, "Datamesh is forming, ready for data bootstrap, waiting for other replicas") || changed
		return r.reconcileFormationRestartIfTimeoutPassed(rf.Ctx(), rv, rvrs, rsc, 30*time.Second).
			ReportChangedIf(changed)
	}

	// Passing rf.Ctx() produces nested logger scope in the callee — intentional for traceability.
	return r.reconcileFormationPhaseBootstrapData(rf.Ctx(), rv, rvrs, rsp, rsc)
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: formation-bootstrap-data
//

// reconcileFormationPhaseBootstrapData handles the data bootstrap phase of formation:
// creates a DRBDResourceOperation (new-current-uuid) to trigger initial data synchronization,
// waits for the operation to complete, and finalizes formation.
//
// Reconcile pattern: Pure orchestration
func (r *Reconciler) reconcileFormationPhaseBootstrapData(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rvrs *[]*v1alpha1.ReplicatedVolumeReplica,
	rsp *rspView,
	rsc *v1alpha1.ReplicatedStorageClass,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "bootstrap-data")
	defer rf.OnEnd(&outcome)

	changed := applyFormationTransition(rv, v1alpha1.ReplicatedVolumeFormationPhaseBootstrapData)
	if changed {
		applyFormationTransitionMessage(rv, "Starting data bootstrap")
	}

	dmDiskful := idset.FromWhere(rv.Status.Datamesh.Members, func(m v1alpha1.ReplicatedVolumeDatameshMember) bool {
		return m.Type == v1alpha1.ReplicaTypeDiskful
	})

	// Name: rv.Name + "-formation" (rv.Name <= 120, suffix 10 chars, total <= 130 < 253 limit).
	drbdrOpName := rv.Name + "-formation"

	drbdrOp, err := r.getDRBDROp(rf.Ctx(), drbdrOpName)
	if err != nil {
		return rf.Failf(err, "getting DRBDResourceOperation %s", drbdrOpName)
	}

	// If the operation exists but was created before the current formation transition
	// started, it is stale (leftover from a previous attempt) and must be deleted.
	if drbdrOp != nil {
		idx := slices.IndexFunc(rv.Status.DatameshTransitions, func(t v1alpha1.ReplicatedVolumeDatameshTransition) bool {
			return t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation
		})
		if idx < 0 {
			panic("reconcileFormationPhaseBootstrapData: no Formation transition found")
		}
		formationStartedAt := rv.Status.DatameshTransitions[idx].StartedAt.Time
		if drbdrOp.CreationTimestamp.Time.Before(formationStartedAt) {
			if err := r.deleteDRBDROp(rf.Ctx(), drbdrOp); err != nil {
				return rf.Failf(err, "deleting stale DRBDResourceOperation %s", drbdrOpName)
			}
			drbdrOp = nil
		}
	}

	// Single replica: no peers to synchronize with, always use clear-bitmap.
	// Multiple replicas with thin provisioning: clear-bitmap (thin volumes don't need full resync).
	// Multiple replicas with thick provisioning: force-resync (thick volumes require full data synchronization).
	singleReplica := dmDiskful.Len() == 1
	drbdrOpFormationParams := &v1alpha1.CreateNewUUIDParams{
		ClearBitmap: singleReplica || rsp.Type == v1alpha1.ReplicatedStoragePoolTypeLVMThin,
		ForceResync: !singleReplica && rsp.Type == v1alpha1.ReplicatedStoragePoolTypeLVM,
	}

	// Create DRBDResourceOperation for data bootstrap if it doesn't exist yet.
	// If it already exists, verify that its parameters match the expected ones;
	// if not, the configuration has changed and formation must restart.
	if drbdrOp == nil {
		drbdResourceName := v1alpha1.FormatReplicatedVolumeReplicaName(rv.Name, dmDiskful.Min())
		drbdrOpSpec := v1alpha1.DRBDResourceOperationSpec{
			DRBDResourceName: drbdResourceName,
			Type:             v1alpha1.DRBDResourceOperationCreateNewUUID,
			CreateNewUUID:    drbdrOpFormationParams,
		}
		drbdrOp, err = r.createDRBDROp(rf.Ctx(), rv, drbdrOpName, drbdrOpSpec)
		if err != nil {
			if apierrors.IsAlreadyExists(err) {
				// Concurrent reconciliation created this DRBDResourceOperation. Requeue to pick it up from cache.
				rf.Log().Info("DRBDResourceOperation already exists, requeueing", "drbdrOp", drbdrOpName)
				return rf.DoneAndRequeue()
			}
			return rf.Failf(err, "creating DRBDResourceOperation %s", drbdrOpName)
		}
	} else {
		// Verify that DRBDResourceName targets one of the diskful replicas.
		drbdrOpTargetsDiskful := false
		for id := range dmDiskful.All() {
			if drbdrOp.Spec.DRBDResourceName == v1alpha1.FormatReplicatedVolumeReplicaName(rv.Name, id) {
				drbdrOpTargetsDiskful = true
				break
			}
		}
		if drbdrOp.Spec.Type != v1alpha1.DRBDResourceOperationCreateNewUUID ||
			!drbdrOpTargetsDiskful ||
			drbdrOp.Spec.CreateNewUUID == nil ||
			drbdrOp.Spec.CreateNewUUID.ClearBitmap != drbdrOpFormationParams.ClearBitmap ||
			drbdrOp.Spec.CreateNewUUID.ForceResync != drbdrOpFormationParams.ForceResync {
			changed = applyFormationTransitionMessage(rv, "Existing DRBDResourceOperation has unexpected parameters, restarting formation") || changed
			changed = applyPendingReplicaMessages(rv, dmDiskful, "Datamesh is forming, restarting due to data bootstrap operation parameter mismatch") || changed

			return r.reconcileFormationRestartIfTimeoutPassed(rf.Ctx(), rv, rvrs, rsc, 30*time.Second).
				ReportChangedIf(changed)
		}
	}

	// Timeout: 1 min base. For multi-replica thick provisioning (force-resync) add volume
	// size / 100 Mbit/s (≈12.5 MB/s) to account for full data synchronization. Single
	// replica and thin provisioning (clear-bitmap) skip full resync, so no size-based
	// addition is needed.
	//
	// We expect sds-replicated-volume to run on 10 Gbit/s networks (or at least 1 Gbit/s).
	// In the worst case, when many volumes are being created simultaneously, each volume
	// should still get at least 100 Mbit/s of bandwidth. If even that is not enough,
	// something has gone completely wrong and there is no point in waiting further.
	dataBootstrapTimeout := 1 * time.Minute
	if !singleReplica && rsp.Type == v1alpha1.ReplicatedStoragePoolTypeLVM {
		const worstCaseBytesPerSec = 100 * 1000 * 1000 / 8 // 100 Mbit/s in bytes
		sizeBytes := rv.Status.Datamesh.Size.Value()
		dataBootstrapTimeout += time.Duration(sizeBytes/worstCaseBytesPerSec) * time.Second
	}

	// Build a human-readable description of the data bootstrap mode.
	var dataBootstrapModeMsg string
	if !singleReplica && rsp.Type == v1alpha1.ReplicatedStoragePoolTypeLVM {
		dataBootstrapModeMsg = fmt.Sprintf(
			"Full data synchronization from source replica %s. Timeout: %s",
			drbdrOp.Spec.DRBDResourceName, dataBootstrapTimeout)
	} else {
		dataBootstrapModeMsg = "Fast mode: data is assumed identical, no synchronization required"
	}

	// Verify the DRBDResourceOperation has not failed.
	// If it failed — restart formation with a 30-second timeout.
	if drbdrOp.Status.Phase == v1alpha1.DRBDOperationPhaseFailed {
		rf.Log().Error(fmt.Errorf("DRBDResourceOperation %s failed: %s", drbdrOpName, drbdrOp.Status.Message), "data bootstrap operation failed, restarting formation")

		changed = applyFormationTransitionMessage(rv, "Data bootstrap operation failed, restarting formation") || changed
		changed = applyPendingReplicaMessages(rv, dmDiskful, "Datamesh is forming, restarting due to failed data bootstrap operation") || changed

		return r.reconcileFormationRestartIfTimeoutPassed(rf.Ctx(), rv, rvrs, rsc, 30*time.Second).
			ReportChangedIf(changed)
	}

	// Verify the DRBDResourceOperation has completed successfully.
	// If it is still running or pending — update messages and wait.
	if drbdrOp.Status.Phase != v1alpha1.DRBDOperationPhaseSucceeded {
		changed = applyFormationTransitionMessage(rv, "Data bootstrap initiated, waiting for operation to complete. "+dataBootstrapModeMsg) || changed
		changed = applyPendingReplicaMessages(rv, dmDiskful, "Datamesh is forming, waiting for data bootstrap to complete") || changed

		return r.reconcileFormationRestartIfTimeoutPassed(rf.Ctx(), rv, rvrs, rsc, dataBootstrapTimeout).
			ReportChangedIf(changed)
	}

	// Verify all diskful replicas have backing volume UpToDate — this is the actual
	// completion signal for data bootstrap (the DRBDResourceOperation only initiates it).
	upToDate := idset.FromWhere(*rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		return dmDiskful.Contains(rvr.ID()) &&
			rvr.Status.BackingVolume != nil &&
			rvr.Status.BackingVolume.State == v1alpha1.DiskStateUpToDate
	})
	if upToDate != dmDiskful {
		// TODO: In the future, include synchronization progress (%) in the messages.
		// DRBD tracks sync progress per-peer (done/total), but DRBDResource status
		// does not currently expose this data. Once DRBDResource reports per-peer
		// sync progress, we can show something like "Data bootstrap: 42% (3.2 GiB / 7.6 GiB)"
		// in both the transition message and per-replica messages.
		notUpToDate := dmDiskful.Difference(upToDate)
		changed = applyFormationTransitionMessage(rv, fmt.Sprintf(
			"Data bootstrap in progress, waiting for replicas [%s] to reach UpToDate state. %s",
			notUpToDate.String(), dataBootstrapModeMsg,
		)) || changed
		changed = applyPendingReplicaMessages(rv, notUpToDate, "Datamesh is forming, data bootstrap in progress, waiting for backing volume to become UpToDate") || changed
		changed = applyPendingReplicaMessages(rv, upToDate, "Datamesh is forming, data bootstrap in progress, replica is UpToDate, waiting for remaining replicas") || changed

		return r.reconcileFormationRestartIfTimeoutPassed(rf.Ctx(), rv, rvrs, rsc, dataBootstrapTimeout).
			ReportChangedIf(changed)
	}

	// All replicas are UpToDate — data bootstrap is complete, formation is finished!
	// The datamesh is born. Remove the Formation transition so that the main reconcile
	// loop can proceed with normal operation (e.g., attach handling, scaling).
	changed = applyFormationTransitionAbsent(rv) || changed

	// Requeue is required so the next reconciliation enters the normal-operation path.
	return rf.ContinueAndRequeue().ReportChangedIf(changed)
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: formation-restart
//

// reconcileFormationRestartIfTimeoutPassed resets formation state and deletes all replicas.
// This should be called when formation needs to restart (e.g., due to spec/constraint changes).
// To avoid thrashing, the restart is delayed: if less than timeout has passed since
// formation started, returns requeue to execute at the timeout mark.
//
// Reconcile pattern: Pure orchestration
func (r *Reconciler) reconcileFormationRestartIfTimeoutPassed(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rvrs *[]*v1alpha1.ReplicatedVolumeReplica,
	rsc *v1alpha1.ReplicatedStorageClass,
	timeout time.Duration,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "restart")
	defer rf.OnEnd(&outcome)

	// Find Formation transition to get start time.
	idx := slices.IndexFunc(rv.Status.DatameshTransitions, func(t v1alpha1.ReplicatedVolumeDatameshTransition) bool {
		return t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation
	})
	if idx < 0 {
		panic("reconcileFormationRestartIfTimeoutPassed called without active Formation transition")
	}
	formationStartedAt := rv.Status.DatameshTransitions[idx].StartedAt.Time

	elapsed := time.Since(formationStartedAt)
	if elapsed < timeout {
		// Wait until timeout has passed since formation started.
		return rf.ContinueAndRequeueAfter(timeout - elapsed)
	}

	rf.Log().Error(fmt.Errorf("formation timed out after %s, restarting", elapsed.Truncate(time.Second)), "restarting formation")

	// Delete formation DRBDResourceOperation if it exists.
	drbdrOpName := rv.Name + "-formation"
	drbdrOp, err := r.getDRBDROp(rf.Ctx(), drbdrOpName)
	if err != nil {
		return rf.Failf(err, "getting DRBDResourceOperation %s", drbdrOpName)
	}
	if drbdrOp != nil {
		if err := r.deleteDRBDROp(rf.Ctx(), drbdrOp); err != nil {
			return rf.Failf(err, "deleting DRBDResourceOperation %s", drbdrOpName)
		}
	}

	// Delete all replicas (with finalizer removal to avoid blocking on normal cleanup).
	for _, rvr := range *rvrs {
		if err := r.deleteRVRWithForcedFinalizerRemoval(rf.Ctx(), rvr); err != nil {
			return rf.Failf(err, "deleting RVR %s", rvr.Name)
		}
	}

	// Reset configuration and datamesh state, then immediately re-initialize
	// configuration from RSC so the intermediate nil state is never persisted
	// (avoids unnecessary RSC reconciliation / pendingObservation churn).
	rv.Status.Configuration = nil
	rv.Status.ConfigurationGeneration = 0
	rv.Status.ConfigurationObservedGeneration = 0
	rv.Status.DatameshRevision = 0
	rv.Status.Datamesh = v1alpha1.ReplicatedVolumeDatamesh{}
	rv.Status.DatameshTransitions = nil
	rv.Status.DatameshPendingReplicaTransitions = nil

	// Re-initialize configuration from RSC.
	eo := ensureRVConfiguration(rf.Ctx(), rv, rsc)
	if eo.Error() != nil {
		return rf.Fail(eo.Error())
	}

	return rf.ContinueAndRequeue().ReportChanged()
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

// applyTransitionMessage sets the Message field on a datamesh transition.
// Returns true if the message was changed.
func applyTransitionMessage(t *v1alpha1.ReplicatedVolumeDatameshTransition, msg string) bool {
	if t.Message == msg {
		return false
	}
	t.Message = msg
	return true
}

// applyPendingReplicaTransitionMessage sets the Message field on the given pending replica
// transition. Returns true if the message was changed.
func applyPendingReplicaTransitionMessage(p *v1alpha1.ReplicatedVolumeDatameshPendingReplicaTransition, msg string) bool {
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
// and removes it from deleting RVRs when safe (not a datamesh member and no RemoveAccessReplica
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
			// (RemoveAccessReplica transition in progress).
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
// RemoveAccessReplica transition (still leaving datamesh). Returns false when rv is nil.
func isRVRMemberOrLeavingDatamesh(rv *v1alpha1.ReplicatedVolume, rvrName string) bool {
	if rv == nil {
		return false
	}

	// Check if the RVR is a datamesh member.
	if rv.Status.Datamesh.FindMemberByName(rvrName) != nil {
		return true
	}

	// Check for active RemoveAccessReplica transition for this replica.
	for i := range rv.Status.DatameshTransitions {
		t := &rv.Status.DatameshTransitions[i]
		if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveAccessReplica && t.ReplicaName == rvrName {
			return true
		}
	}

	return false
}

// generateSharedSecret generates a random DRBD shared secret.
// DRBD shared-secret supports up to 64 characters.
// Generates 32 random bytes and encodes as base64 (~43 chars).
func generateSharedSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate shared secret: %w", err)
	}
	// Use RawStdEncoding to avoid padding; ensure <=64 chars.
	return base64.RawStdEncoding.EncodeToString(buf), nil
}

// isFormationInProgress returns true if the datamesh is still forming:
// either has never been formed (DatameshRevision == 0), or has an active Formation transition.
// When true, also returns the current formation phase
// (empty if the datamesh has never been formed or no Formation transition exists).
func isFormationInProgress(rv *v1alpha1.ReplicatedVolume) (bool, v1alpha1.ReplicatedVolumeFormationPhase) {
	if rv.Status.DatameshRevision == 0 {
		return true, ""
	}

	for i := range rv.Status.DatameshTransitions {
		if rv.Status.DatameshTransitions[i].Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation {
			if rv.Status.DatameshTransitions[i].Formation == nil {
				panic("isFormationInProgress: Formation transition exists but Formation field is nil")
			}
			return true, rv.Status.DatameshTransitions[i].Formation.Phase
		}
	}
	return false, ""
}

// applyFormationTransition ensures a Formation transition exists with the given phase.
// Formation transitions never carry a DatameshRevision (it is always zero/absent).
// If absent, creates it with StartedAt set to now.
// If present but phase differs, updates it (StartedAt is preserved).
// Returns true if the object was changed.
//
// Exception: uses metav1.Now() for StartedAt when creating a new transition.
// This is controller-owned state (persisted decision timestamp), acceptable here
// because the value is set once and stabilized across subsequent reconciliations.
func applyFormationTransition(rv *v1alpha1.ReplicatedVolume, phase v1alpha1.ReplicatedVolumeFormationPhase) bool {
	for i := range rv.Status.DatameshTransitions {
		if rv.Status.DatameshTransitions[i].Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation {
			if rv.Status.DatameshTransitions[i].Formation == nil {
				panic("applyFormationTransition: Formation transition exists but Formation field is nil")
			}

			if rv.Status.DatameshTransitions[i].Formation.Phase != phase {
				rv.Status.DatameshTransitions[i].Formation.Phase = phase
				return true
			}
			return false
		}
	}

	// Formation transition not found — create it.
	rv.Status.DatameshTransitions = append(rv.Status.DatameshTransitions, v1alpha1.ReplicatedVolumeDatameshTransition{
		Type:      v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation,
		StartedAt: metav1.Now(),
		Formation: &v1alpha1.ReplicatedVolumeDatameshTransitionFormation{Phase: phase},
	})
	return true
}

// applyFormationTransitionMessage updates only the message of an existing Formation transition.
// Panics if no Formation transition exists.
// Returns true if the message was changed.
func applyFormationTransitionMessage(rv *v1alpha1.ReplicatedVolume, message string) bool {
	for i := range rv.Status.DatameshTransitions {
		if rv.Status.DatameshTransitions[i].Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation {
			if rv.Status.DatameshTransitions[i].Message != message {
				rv.Status.DatameshTransitions[i].Message = message
				return true
			}
			return false
		}
	}
	panic("applyFormationTransitionMessage: Formation transition does not exist")
}

// applyFormationTransitionAbsent removes the Formation transition if present.
// Returns true if the object was changed.
func applyFormationTransitionAbsent(rv *v1alpha1.ReplicatedVolume) bool {
	for i := range rv.Status.DatameshTransitions {
		if rv.Status.DatameshTransitions[i].Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation {
			rv.Status.DatameshTransitions = slices.Delete(rv.Status.DatameshTransitions, i, i+1)
			return true
		}
	}
	return false
}

// applyDatameshMember adds or updates a member in the datamesh.
// Returns true if the member was added or any field was changed.
func applyDatameshMember(rv *v1alpha1.ReplicatedVolume, member v1alpha1.ReplicatedVolumeDatameshMember) bool {
	for i := range rv.Status.Datamesh.Members {
		if rv.Status.Datamesh.Members[i].ID() == member.ID() {
			m := &rv.Status.Datamesh.Members[i]
			changed := false
			if m.Type != member.Type {
				m.Type = member.Type
				changed = true
			}
			if m.TypeTransition != member.TypeTransition {
				m.TypeTransition = member.TypeTransition
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

// applyPendingReplicaMessages updates the Message field for pending replica transitions
// whose ID is in the given set. Returns true if any message was changed.
func applyPendingReplicaMessages(rv *v1alpha1.ReplicatedVolume, repIDs idset.IDSet, message string) bool {
	changed := false
	for i := range rv.Status.DatameshPendingReplicaTransitions {
		t := &rv.Status.DatameshPendingReplicaTransitions[i]
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
	targetDiskfulCount int,
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

// computeIntendedDiskfulReplicaCount returns the intended number of diskful replicas
// based on the replication mode from rv.Status.Configuration.
func computeIntendedDiskfulReplicaCount(rv *v1alpha1.ReplicatedVolume) int {
	switch rv.Status.Configuration.Replication {
	case v1alpha1.ReplicationNone:
		return 1
	case v1alpha1.ReplicationConsistencyAndAvailability:
		return 3
	default:
		return 2
	}
}

// computeTargetQuorum computes Quorum and QuorumMinimumRedundancy based on intended diskful members and replication mode.
func computeTargetQuorum(rv *v1alpha1.ReplicatedVolume) (q, qmr byte) {
	intendedDiskful := idset.FromWhere(rv.Status.Datamesh.Members, func(m v1alpha1.ReplicatedVolumeDatameshMember) bool {
		return m.Type == v1alpha1.ReplicaTypeDiskful || m.TypeTransition == v1alpha1.ReplicatedVolumeDatameshMemberTypeTransitionToDiskful
	})

	var minQ, minQMR byte
	switch rv.Status.Configuration.Replication {
	case v1alpha1.ReplicationNone:
		minQ, minQMR = 1, 1
	case v1alpha1.ReplicationAvailability:
		minQ, minQMR = 2, 1
	case v1alpha1.ReplicationConsistency, v1alpha1.ReplicationConsistencyAndAvailability:
		minQ, minQMR = 2, 2
	default:
		minQ, minQMR = 2, 2
	}

	quorum := byte(intendedDiskful.Len()/2 + 1)

	q = max(quorum, minQ)
	qmr = max(quorum, minQMR)

	return q, qmr
}

// computeActualQuorum checks whether at least one diskful replica has quorum and a ready agent.
// Returns (true, "") if quorum is satisfied, or (false, diagnostic) with a diagnostic detail otherwise.
func computeActualQuorum(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
) (satisfied bool, diagnostic string) {
	diskfulMembers := idset.FromWhere(rv.Status.Datamesh.Members, func(m v1alpha1.ReplicatedVolumeDatameshMember) bool {
		return m.Type == v1alpha1.ReplicaTypeDiskful
	})
	agentNotReady := idset.FromWhere(rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		return obju.StatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondReadyType).
			ReasonEqual(v1alpha1.ReplicatedVolumeReplicaCondReadyReasonAgentNotReady).
			Eval()
	})
	withQuorum := idset.FromWhere(rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		return rvr.Status.Quorum != nil && *rvr.Status.Quorum
	}).Intersect(diskfulMembers).Difference(agentNotReady)

	if !withQuorum.IsEmpty() {
		return true, ""
	}

	// Diagnostic: which diskful replicas exist and why they don't count.
	allRVRIDs := idset.FromAll(rvrs)
	diskfulAgentNotReady := diskfulMembers.Intersect(agentNotReady)
	diskfulNoQuorum := diskfulMembers.Intersect(allRVRIDs).Difference(agentNotReady)

	switch {
	case !diskfulNoQuorum.IsEmpty() && !diskfulAgentNotReady.IsEmpty():
		diagnostic = fmt.Sprintf("no quorum on [%s]; agent not ready on [%s]",
			diskfulNoQuorum, diskfulAgentNotReady)
	case !diskfulNoQuorum.IsEmpty():
		diagnostic = fmt.Sprintf("no quorum on [%s]", diskfulNoQuorum)
	case !diskfulAgentNotReady.IsEmpty():
		diagnostic = fmt.Sprintf("agent not ready on [%s]", diskfulAgentNotReady)
	default:
		diagnostic = "no diskful replicas available"
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

	// Apply replicated-storage-class label.
	if rv.Spec.ReplicatedStorageClassName != "" {
		changed = obju.SetLabel(rv, v1alpha1.ReplicatedStorageClassLabelKey, rv.Spec.ReplicatedStorageClassName) || changed
	}

	return changed
}

// ensureRVConfiguration initializes rv.Status.Configuration from RSC.
func ensureRVConfiguration(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rsc *v1alpha1.ReplicatedStorageClass,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "configuration")
	defer ef.OnEnd(&outcome)

	changed := false

	// Guard: RSC not found or has no configuration.
	if rsc == nil || rsc.Status.Configuration == nil {
		return ef.Ok()
	}

	// Initialize configuration if not set.
	if rv.Status.Configuration == nil {
		// DeepCopy to avoid aliasing with the RSC cache object.
		rv.Status.Configuration = rsc.Status.Configuration.DeepCopy()
		rv.Status.ConfigurationGeneration = rsc.Status.ConfigurationGeneration
		rv.Status.ConfigurationObservedGeneration = rsc.Status.ConfigurationGeneration
		changed = true
	}

	return ef.Ok().ReportChangedIf(changed)
}

// ensureConditionConfigurationReady sets the ConfigurationReady condition.
func ensureConditionConfigurationReady(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rsc *v1alpha1.ReplicatedStorageClass,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "cond-configuration-ready")
	defer ef.OnEnd(&outcome)

	changed := false

	// RSC not found.
	if rsc == nil {
		changed = applyConfigurationReadyCondFalse(rv,
			v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonWaitingForStorageClass,
			fmt.Sprintf("ReplicatedStorageClass %q not found", rv.Spec.ReplicatedStorageClassName))
		return ef.Ok().ReportChangedIf(changed)
	}

	// RSC has no configuration.
	if rsc.Status.Configuration == nil {
		changed = applyConfigurationReadyCondFalse(rv,
			v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonWaitingForStorageClass,
			fmt.Sprintf("ReplicatedStorageClass %q configuration not ready", rsc.Name))
		return ef.Ok().ReportChangedIf(changed)
	}

	// ConfigurationGeneration not set = rollout in progress.
	if rv.Status.ConfigurationGeneration == 0 {
		changed = applyConfigurationReadyCondFalse(rv,
			v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonConfigurationRolloutInProgress,
			"")
		return ef.Ok().ReportChangedIf(changed)
	}

	// Check if generation matches.
	if rv.Status.ConfigurationGeneration == rsc.Status.ConfigurationGeneration {
		changed = applyConfigurationReadyCondTrue(rv,
			v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonReady,
			"Configuration matches storage class")
	} else {
		changed = applyConfigurationReadyCondFalse(rv,
			v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonStaleConfiguration,
			fmt.Sprintf("Configuration generation %d does not match storage class generation %d",
				rv.Status.ConfigurationGeneration, rsc.Status.ConfigurationGeneration))
	}

	return ef.Ok().ReportChangedIf(changed)
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

// ensureDatameshPendingReplicaTransitions synchronizes rv.Status.DatameshPendingReplicaTransitions
// with the current DatameshPendingTransition from each RVR.
// Both lists are kept sorted by name for determinism.
// Uses sorted merge-in-place algorithm (no map allocation).
func ensureDatameshPendingReplicaTransitions(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "datamesh-pending-replica-transitions")
	defer ef.OnEnd(&outcome)

	changed := false
	existing := rv.Status.DatameshPendingReplicaTransitions

	// Ensure existing entries are sorted by name for the merge algorithm below.
	// Note: sorting does not mark changed=true intentionally. Order is semantically
	// irrelevant for the API, so a mere reorder is not a reason to patch. If a real
	// content change occurs, the patch will persist the correctly sorted value.
	slices.SortFunc(existing, func(a, b v1alpha1.ReplicatedVolumeDatameshPendingReplicaTransition) int {
		return cmp.Compare(a.ID(), b.ID())
	})

	// Merge-in-place with two pointers.
	// rvrs are already sorted by caller (getRVRsSorted).
	result := make([]v1alpha1.ReplicatedVolumeDatameshPendingReplicaTransition, 0, len(existing)+len(rvrs))
	i, j := 0, 0

	for i < len(existing) && j < len(rvrs) {
		// Skip rvrs with nil transition.
		if rvrs[j].Status.DatameshPendingTransition == nil {
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
			result = append(result, v1alpha1.ReplicatedVolumeDatameshPendingReplicaTransition{
				Name: rvrName,
				// DeepCopy to avoid aliasing: rvrs is a read-only input,
				// and the cloned value will live inside rv.Status (mutation target).
				Transition:      *rvrs[j].Status.DatameshPendingTransition.DeepCopy(),
				FirstObservedAt: metav1.Now(),
			})
			j++
		case 0: // equal names
			if existing[i].Transition.Equals(rvrs[j].Status.DatameshPendingTransition) {
				// Keep as-is.
				result = append(result, existing[i])
			} else {
				// Update: copy transition, clear Message, set new FirstObservedAt.
				changed = true
				result = append(result, v1alpha1.ReplicatedVolumeDatameshPendingReplicaTransition{
					Name: rvrName,
					// DeepCopy to avoid aliasing: rvrs is a read-only input,
					// and the cloned value will live inside rv.Status (mutation target).
					Transition:      *rvrs[j].Status.DatameshPendingTransition.DeepCopy(),
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
		if rvrs[j].Status.DatameshPendingTransition != nil {
			changed = true
			result = append(result, v1alpha1.ReplicatedVolumeDatameshPendingReplicaTransition{
				Name: rvrs[j].Name,
				// DeepCopy to avoid aliasing: rvrs is a read-only input,
				// and the cloned value will live inside rv.Status (mutation target).
				Transition:      *rvrs[j].Status.DatameshPendingTransition.DeepCopy(),
				FirstObservedAt: metav1.Now(),
			})
		}
		j++
	}

	// Assign result only if changed.
	if changed {
		rv.Status.DatameshPendingReplicaTransitions = result
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
		if c := a.CreationTimestamp.Time.Compare(b.CreationTimestamp.Time); c != 0 {
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
	return r.cl.Status().Patch(ctx, obj, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
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
