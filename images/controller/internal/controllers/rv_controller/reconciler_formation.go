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
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"slices"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/drbd_size"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/idset"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

// Formation plan IDs stored in transition.PlanID.
const (
	formationPlanCreate = "create/v1"
	formationPlanAdopt  = "adopt/v1"
)

// Formation create/v1 step indices. Each step function accesses t.Steps[idx] directly.
const (
	formationStepIdxPreconfigure          = 0
	formationStepIdxEstablishConnectivity = 1
	formationStepIdxBootstrapData         = 2
	formationStepCount                    = 3
)

// formationStepNames maps step index to human-readable name (displayed in status) for create/v1.
var formationStepNames = [formationStepCount]string{
	formationStepIdxPreconfigure:          "Preconfigure",
	formationStepIdxEstablishConnectivity: "Establish connectivity",
	formationStepIdxBootstrapData:         "Bootstrap data",
}

// Formation adopt/v1 step indices.
const (
	adoptStepIdxVerifyPrerequisites       = 0
	adoptStepIdxPopulateAndVerifyDatamesh = 1
	adoptStepIdxExitMaintenance           = 2
	adoptStepCount                        = 3
)

// adoptStepNames maps step index to human-readable name (displayed in status) for adopt/v1.
var adoptStepNames = [adoptStepCount]string{
	adoptStepIdxVerifyPrerequisites:       "Verify prerequisites",
	adoptStepIdxPopulateAndVerifyDatamesh: "Populate and verify datamesh",
	adoptStepIdxExitMaintenance:           "Exit maintenance",
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
	stepIdx int,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "formation")
	defer rf.OnEnd(&outcome)

	// Determine plan: adopt when annotation is present, create otherwise.
	planID := formationPlanCreate
	if _, ok := rv.Annotations[v1alpha1.AdoptRVRAnnotationKey]; ok {
		planID = formationPlanAdopt
	}

	// Find or create the Formation transition. Created on first entry (stepIdx=0).
	t, created := ensureFormationTransition(rv, planID)

	// Dispatch by plan stored in the transition (persisted on first creation).
	switch t.PlanID {
	case formationPlanAdopt:
		switch stepIdx {
		case adoptStepIdxVerifyPrerequisites:
			outcome = r.reconcileAdoptStepVerifyPrerequisites(rf.Ctx(), rv, rvrs, rsp, rsc, t, created)
		case adoptStepIdxPopulateAndVerifyDatamesh:
			outcome = r.reconcileAdoptStepPopulateAndVerifyDatamesh(rf.Ctx(), rv, rvrs, rsp, t)
		case adoptStepIdxExitMaintenance:
			outcome = r.reconcileAdoptStepExitMaintenance(rf.Ctx(), rv, rvrs, t)
		default:
			return rf.Fail(fmt.Errorf("invalid adopt formation step index: %d", stepIdx))
		}

	case formationPlanCreate, "":
		switch stepIdx {
		case formationStepIdxPreconfigure:
			outcome = r.reconcileFormationStepPreconfigure(rf.Ctx(), rv, rvrs, rsp, rsc, t, created)
		case formationStepIdxEstablishConnectivity:
			outcome = r.reconcileFormationStepEstablishConnectivity(rf.Ctx(), rv, rvrs, rsp, rsc, t)
		case formationStepIdxBootstrapData:
			outcome = r.reconcileFormationStepBootstrapData(rf.Ctx(), rv, rvrs, rsp, rsc, t)
		default:
			return rf.Fail(fmt.Errorf("invalid formation step index: %d", stepIdx))
		}

	default:
		return rf.Fail(fmt.Errorf("unknown formation plan: %s", t.PlanID))
	}

	// Set "waiting" conditions on all RVAs — datamesh is not ready yet.
	outcome = outcome.Merge(r.reconcileRVAWaiting(rf.Ctx(), rvas, "Datamesh formation is in progress"))

	return outcome
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: formation-preconfigure
//

// reconcileFormationStepPreconfigure handles initial formation: creates replicas, waits for them
// to become preconfigured, and initializes datamesh configuration.
//
// Reconcile pattern: Pure orchestration
func (r *Reconciler) reconcileFormationStepPreconfigure(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rvrs *[]*v1alpha1.ReplicatedVolumeReplica,
	rsp *rspView,
	rsc *v1alpha1.ReplicatedStorageClass,
	t *v1alpha1.ReplicatedVolumeDatameshTransition,
	created bool,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "preconfigure")
	defer rf.OnEnd(&outcome)

	step := &t.Steps[formationStepIdxPreconfigure]
	changed := created
	if created {
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

		step.DatameshRevision = rv.Status.DatameshRevision
		applyDatameshTransitionStepMessage(step, "Starting preconfigure")
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
		for diskful.Len() < int(targetDiskfulCount) {
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
		req := rvr.Status.DatameshRequest
		return req != nil && req.Operation == v1alpha1.DatameshMembershipRequestOperationJoin &&
			obju.StatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType).
				ReasonEqual(v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonPendingDatameshJoin).
				ObservedGenerationCurrent().
				Eval()
	})

	// Remove excess diskful replicas (prefer higher ID).
	// Priority: prefer deleting replicas that are less progressed (not scheduled > not preconfigured > any).
	for diskful.Len() > int(targetDiskfulCount) {
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
		changed = applyDatameshTransitionStepMessage(step, msg) || changed
		changed = applyDatameshReplicaRequestMessages(
			rv, diskful,
			"Datamesh is forming, waiting for replica cleanup to complete",
		) || changed
		return r.reconcileFormationRestartIfTimeoutPassed(rf.Ctx(), rv, rvrs, rsc, t, 30*time.Second).
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
		changed = applyDatameshTransitionStepMessage(step, msg) || changed
		changed = applyDatameshReplicaRequestMessages(
			rv, preconfigured,
			"Datamesh is forming, waiting for other replicas to become preconfigured",
		) || changed
		return r.reconcileFormationRestartIfTimeoutPassed(rf.Ctx(), rv, rvrs, rsc, t, 30*time.Second).
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
		changed = applyDatameshTransitionStepMessage(step, msg) || changed
		changed = applyDatameshReplicaRequestMessages(
			rv, okReplicas,
			"Datamesh is forming, waiting for other replicas to report required addresses",
		) || changed
		changed = applyDatameshReplicaRequestMessages(
			rv, missingAddresses,
			"Replica addresses do not match required network configuration, blocking datamesh formation",
		) || changed
		return r.reconcileFormationRestartIfTimeoutPassed(rf.Ctx(), rv, rvrs, rsc, t, 30*time.Second).
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
		changed = applyDatameshTransitionStepMessage(step, msg) || changed
		changed = applyDatameshReplicaRequestMessages(
			rv, okReplicas,
			"Datamesh is forming, waiting for other replicas to be on eligible nodes",
		) || changed
		changed = applyDatameshReplicaRequestMessages(
			rv, notEligible,
			"Replica is placed on a node not in the eligible nodes list, blocking datamesh formation",
		) || changed
		return r.reconcileFormationRestartIfTimeoutPassed(rf.Ctx(), rv, rvrs, rsc, t, 30*time.Second).
			ReportChangedIf(changed)
	}

	// Verify spec matches pending transition (safety check against spec changes during formation).
	specMismatch := idset.FromWhere(*rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		if !diskful.Contains(rvr.ID()) {
			return false
		}
		req := rvr.Status.DatameshRequest
		if req == nil {
			return false
		}
		return rvr.Spec.Type != req.Type ||
			rvr.Spec.LVMVolumeGroupName != req.LVMVolumeGroupName ||
			rvr.Spec.LVMVolumeGroupThinPoolName != req.ThinPoolName
	})
	if !specMismatch.IsEmpty() {
		okReplicas := diskful.Difference(specMismatch)

		msg := fmt.Sprintf(
			"Replicas [%s] have spec changes that don't match pending transition. "+
				"This may indicate spec changed during formation. "+
				"If not resolved automatically, formation will be restarted",
			specMismatch.String(),
		)
		changed = applyDatameshTransitionStepMessage(step, msg) || changed
		changed = applyDatameshReplicaRequestMessages(
			rv, okReplicas,
			"Datamesh is forming, waiting for other replicas to resolve spec mismatch",
		) || changed
		changed = applyDatameshReplicaRequestMessages(
			rv, specMismatch,
			"Replica spec does not match pending transition, blocking datamesh formation",
		) || changed
		return r.reconcileFormationRestartIfTimeoutPassed(rf.Ctx(), rv, rvrs, rsc, t, 30*time.Second).
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
		changed = applyDatameshTransitionStepMessage(step, msg) || changed
		changed = applyDatameshReplicaRequestMessages(
			rv, okReplicas,
			"Datamesh is forming, waiting for other replicas to resolve backing volume size issue",
		) || changed
		changed = applyDatameshReplicaRequestMessages(
			rv, insufficientSize,
			"Replica backing volume size is insufficient for datamesh, blocking formation",
		) || changed
		return r.reconcileFormationRestartIfTimeoutPassed(rf.Ctx(), rv, rvrs, rsc, t, 30*time.Second).
			ReportChangedIf(changed)
	}

	// Advance to next step and fall through.
	advanceFormationStep(t, formationStepIdxPreconfigure)
	return r.reconcileFormationStepEstablishConnectivity(rf.Ctx(), rv, rvrs, rsp, rsc, t)
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: formation-establish-connectivity
//

// reconcileFormationStepEstablishConnectivity handles post-preconfiguration formation: validates datamesh
// membership consistency, establishes connectivity, and completes formation.
//
// Reconcile pattern: Pure orchestration
func (r *Reconciler) reconcileFormationStepEstablishConnectivity(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rvrs *[]*v1alpha1.ReplicatedVolumeReplica,
	rsp *rspView,
	rsc *v1alpha1.ReplicatedStorageClass,
	t *v1alpha1.ReplicatedVolumeDatameshTransition,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "establish-connectivity")
	defer rf.OnEnd(&outcome)

	step := &t.Steps[formationStepIdxEstablishConnectivity]
	changed := false

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

			// We could use rv.Status.DatameshReplicaRequests, but that would require
			// searching by name. ensureDatameshReplicaRequests called earlier guarantees
			// these fields match.
			req := rvr.Status.DatameshRequest

			applyDatameshMember(rv, v1alpha1.DatameshMember{
				Name:                       rvr.Name,
				Type:                       v1alpha1.DatameshMemberType(req.Type),
				NodeName:                   rvr.Spec.NodeName,
				Zone:                       zone,
				Addresses:                  slices.Clone(rvr.Status.Addresses),
				LVMVolumeGroupName:         req.LVMVolumeGroupName,
				LVMVolumeGroupThinPoolName: req.ThinPoolName,
			})
		}

		// Set baseline GMDR from the configuration that formation is building towards.
		// Formation creates the exact layout matching configuration, so baseline = config.
		rv.Status.BaselineGuaranteedMinimumDataRedundancy = rv.Status.Configuration.GuaranteedMinimumDataRedundancy

		// Quorum settings control DRBD split-brain prevention based on replica count.
		quorum, quorumMinimumRedundancy := computeTargetQuorum(rv)
		rv.Status.Datamesh.Quorum = quorum
		rv.Status.Datamesh.QuorumMinimumRedundancy = quorumMinimumRedundancy

		rv.Status.DatameshRevision++
		step.DatameshRevision = rv.Status.DatameshRevision
		applyDatameshReplicaRequestMessages(rv, diskful, "Datamesh is forming, waiting for replica to apply new configuration")
		applyDatameshTransitionStepMessage(step, fmt.Sprintf(
			"Replicas [%s] preconfigured and added to datamesh. Waiting for them to establish connections",
			diskful.String(),
		))

		return rf.Continue().ReportChanged()
	}

	// Verify datamesh members match active replicas (safety check).
	dmDiskful := idset.FromWhere(rv.Status.Datamesh.Members, func(m v1alpha1.DatameshMember) bool {
		return m.Type == v1alpha1.DatameshMemberTypeDiskful
	})
	if dmDiskful != diskful {
		msg := fmt.Sprintf(
			"Datamesh members mismatch: datamesh has [%s], but active RVRs are [%s]. "+
				"This may be caused by manual intervention during formation. "+
				"If not resolved automatically, formation will be restarted",
			dmDiskful.String(), diskful.String(),
		)
		changed = applyDatameshTransitionStepMessage(step, msg) || changed
		changed = applyDatameshReplicaRequestMessages(rv, diskful, msg) || changed
		return r.reconcileFormationRestartIfTimeoutPassed(rf.Ctx(), rv, rvrs, rsc, t, 30*time.Second).
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
		changed = applyDatameshTransitionStepMessage(step, fmt.Sprintf(
			"Waiting for replicas [%s] to be fully configured for datamesh revision %d",
			notConfigured.String(), rv.Status.DatameshRevision,
		)) || changed
		changed = applyDatameshReplicaRequestMessages(rv, notConfigured, "Datamesh is forming, waiting for DRBD configuration to continue") || changed
		changed = applyDatameshReplicaRequestMessages(rv, configured, "Datamesh is forming, DRBD configured, waiting for other replicas") || changed
		return r.reconcileFormationRestartIfTimeoutPassed(rf.Ctx(), rv, rvrs, rsc, t, 30*time.Second).
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
		changed = applyDatameshTransitionStepMessage(step, fmt.Sprintf(
			"Waiting for replicas [%s] to establish connections with all peers",
			notConnected.String(),
		)) || changed
		changed = applyDatameshReplicaRequestMessages(rv, diskful, "Datamesh is forming, waiting for all replicas to connect to each other") || changed
		return r.reconcileFormationRestartIfTimeoutPassed(rf.Ctx(), rv, rvrs, rsc, t, 30*time.Second).
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
		changed = applyDatameshTransitionStepMessage(step, fmt.Sprintf(
			"Waiting for replicas [%s] to be ready for data bootstrap "+
				"(backing volume Inconsistent + Established replication with all peers)",
			notReady.String(),
		)) || changed
		changed = applyDatameshReplicaRequestMessages(rv, notReady, "Datamesh is forming, waiting for data bootstrap readiness (requires backing volume Inconsistent and replication Established with all peers)") || changed
		changed = applyDatameshReplicaRequestMessages(rv, readyForDataBootstrap, "Datamesh is forming, ready for data bootstrap, waiting for other replicas") || changed
		return r.reconcileFormationRestartIfTimeoutPassed(rf.Ctx(), rv, rvrs, rsc, t, 30*time.Second).
			ReportChangedIf(changed)
	}

	// Advance to next step and fall through.
	advanceFormationStep(t, formationStepIdxEstablishConnectivity)
	return r.reconcileFormationStepBootstrapData(rf.Ctx(), rv, rvrs, rsp, rsc, t)
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: formation-bootstrap-data
//

// reconcileFormationStepBootstrapData handles the data bootstrap phase of formation:
// creates a DRBDResourceOperation (new-current-uuid) to trigger initial data synchronization,
// waits for the operation to complete, and finalizes formation.
//
// Reconcile pattern: Pure orchestration
func (r *Reconciler) reconcileFormationStepBootstrapData(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rvrs *[]*v1alpha1.ReplicatedVolumeReplica,
	rsp *rspView,
	rsc *v1alpha1.ReplicatedStorageClass,
	t *v1alpha1.ReplicatedVolumeDatameshTransition,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "bootstrap-data")
	defer rf.OnEnd(&outcome)

	step := &t.Steps[formationStepIdxBootstrapData]
	changed := false

	dmDiskful := idset.FromWhere(rv.Status.Datamesh.Members, func(m v1alpha1.DatameshMember) bool {
		return m.Type == v1alpha1.DatameshMemberTypeDiskful
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
		formationStartedAt := t.StartedAt().Time
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
			changed = applyDatameshTransitionStepMessage(step, "Existing DRBDResourceOperation has unexpected parameters, restarting formation") || changed
			changed = applyDatameshReplicaRequestMessages(rv, dmDiskful, "Datamesh is forming, restarting due to data bootstrap operation parameter mismatch") || changed

			return r.reconcileFormationRestartIfTimeoutPassed(rf.Ctx(), rv, rvrs, rsc, t, 30*time.Second).
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

		changed = applyDatameshTransitionStepMessage(step, "Data bootstrap operation failed, restarting formation") || changed
		changed = applyDatameshReplicaRequestMessages(rv, dmDiskful, "Datamesh is forming, restarting due to failed data bootstrap operation") || changed

		return r.reconcileFormationRestartIfTimeoutPassed(rf.Ctx(), rv, rvrs, rsc, t, 30*time.Second).
			ReportChangedIf(changed)
	}

	// Verify the DRBDResourceOperation has completed successfully.
	// If it is still running or pending — update messages and wait.
	if drbdrOp.Status.Phase != v1alpha1.DRBDOperationPhaseSucceeded {
		changed = applyDatameshTransitionStepMessage(step, "Data bootstrap initiated, waiting for operation to complete. "+dataBootstrapModeMsg) || changed
		changed = applyDatameshReplicaRequestMessages(rv, dmDiskful, "Datamesh is forming, waiting for data bootstrap to complete") || changed

		return r.reconcileFormationRestartIfTimeoutPassed(rf.Ctx(), rv, rvrs, rsc, t, dataBootstrapTimeout).
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
		changed = applyDatameshTransitionStepMessage(step, fmt.Sprintf(
			"Data bootstrap in progress, waiting for replicas [%s] to reach UpToDate state. %s",
			notUpToDate.String(), dataBootstrapModeMsg,
		)) || changed
		changed = applyDatameshReplicaRequestMessages(rv, notUpToDate, "Datamesh is forming, data bootstrap in progress, waiting for backing volume to become UpToDate") || changed
		changed = applyDatameshReplicaRequestMessages(rv, upToDate, "Datamesh is forming, data bootstrap in progress, replica is UpToDate, waiting for remaining replicas") || changed

		return r.reconcileFormationRestartIfTimeoutPassed(rf.Ctx(), rv, rvrs, rsc, t, dataBootstrapTimeout).
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
	t *v1alpha1.ReplicatedVolumeDatameshTransition,
	timeout time.Duration,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "restart")
	defer rf.OnEnd(&outcome)

	formationStartedAt := t.StartedAt().Time

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

	// Reset configuration and datamesh state, then immediately re-derive
	// configuration so the ConfigurationReady condition does not flicker.
	rv.Status.Configuration = nil
	rv.Status.ConfigurationGeneration = 0
	rv.Status.ConfigurationObservedGeneration = 0
	rv.Status.DatameshRevision = 0
	rv.Status.Datamesh = v1alpha1.ReplicatedVolumeDatamesh{}
	rv.Status.BaselineGuaranteedMinimumDataRedundancy = 0
	rv.Status.DatameshTransitions = nil
	rv.Status.DatameshReplicaRequests = nil

	// Re-derive configuration from source (Configuration is nil after reset).
	outcome = r.reconcileRVConfiguration(rf.Ctx(), rv, rsc)
	if outcome.ShouldReturn() {
		return outcome
	}

	return rf.ContinueAndRequeue().ReportChanged()
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: adopt/v1 — VerifyPrerequisites
//

// reconcileAdoptStepVerifyPrerequisites verifies that pre-existing replicas satisfy all
// prerequisites for adoption.
// Unlike create/v1, this step never creates or deletes RVRs.
//
// Reconcile pattern: Pure orchestration
func (r *Reconciler) reconcileAdoptStepVerifyPrerequisites(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rvrs *[]*v1alpha1.ReplicatedVolumeReplica,
	rsp *rspView,
	rsc *v1alpha1.ReplicatedStorageClass,
	t *v1alpha1.ReplicatedVolumeDatameshTransition,
	created bool,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "adopt-verify-prerequisites")
	defer rf.OnEnd(&outcome)

	step := &t.Steps[adoptStepIdxVerifyPrerequisites]
	changed := created
	if created {
		rv.Status.DatameshRevision = 1
		rv.Status.Datamesh.SystemNetworkNames = rsp.SystemNetworkNames
		rv.Status.Datamesh.Size = rv.Spec.Size

		step.DatameshRevision = rv.Status.DatameshRevision
		applyDatameshTransitionStepMessage(step, "Starting adopt: waiting for pre-existing replicas")
	}

	// Collect non-deleting replicas by type.
	diskful := idset.FromWhere(*rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		return rvr.Spec.Type == v1alpha1.ReplicaTypeDiskful && rvr.DeletionTimestamp == nil
	})
	tiebreakers := idset.FromWhere(*rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		return rvr.Spec.Type == v1alpha1.ReplicaTypeTieBreaker && rvr.DeletionTimestamp == nil
	})
	access := idset.FromWhere(*rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		return rvr.Spec.Type == v1alpha1.ReplicaTypeAccess && rvr.DeletionTimestamp == nil
	})
	all := diskful.Union(tiebreakers).Union(access)

	// Gate: diskful replicas exist.
	if diskful.IsEmpty() {
		changed = applyDatameshTransitionStepMessage(step, "No diskful replicas found, waiting for pre-existing RVRs to appear") || changed
		return rf.ContinueAndRequeue().ReportChangedIf(changed)
	}

	// Gate: no deleting RVRs remain.
	deleting := idset.FromWhere(*rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		return rvr.DeletionTimestamp != nil
	})
	if !deleting.IsEmpty() {
		msg := fmt.Sprintf("Waiting for %d deleting replicas [%s] to be fully removed", deleting.Len(), deleting.String())
		changed = applyDatameshTransitionStepMessage(step, msg) || changed
		return rf.ContinueAndRequeue().ReportChangedIf(changed)
	}

	// Replicas that have been assigned to a node by the scheduler.
	// Access replicas do not go through the scheduling pipeline; they are
	// considered scheduled when they have a non-empty spec.nodeName.
	scheduled := idset.FromWhere(*rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		if rvr.Spec.Type == v1alpha1.ReplicaTypeAccess {
			return rvr.Spec.NodeName != ""
		}
		return obju.StatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondScheduledType).
			IsTrue().
			ObservedGenerationCurrent().
			Eval()
	})

	// Gate: all replicas scheduled.
	waitingScheduling := all.Difference(scheduled)
	if !waitingScheduling.IsEmpty() {
		schedulingFailed := idset.FromWhere(*rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
			return waitingScheduling.Contains(rvr.ID()) &&
				obju.StatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondScheduledType).
					IsFalse().
					Eval()
		})
		pendingScheduling := waitingScheduling.Difference(schedulingFailed)

		var parts []string
		if !pendingScheduling.IsEmpty() {
			parts = append(parts, fmt.Sprintf("pending scheduling [%s]", pendingScheduling))
		}
		if !schedulingFailed.IsEmpty() {
			part := fmt.Sprintf("scheduling failed [%s]", schedulingFailed)
			if msgs := computeActualSchedulingFailureMessages(*rvrs, schedulingFailed); len(msgs) > 0 {
				part += " (" + strings.Join(msgs, " | ") + ")"
			}
			parts = append(parts, part)
		}
		msg := fmt.Sprintf("Waiting for %d/%d replicas to be scheduled: %s",
			waitingScheduling.Len(), all.Len(), strings.Join(parts, ", "))
		changed = applyDatameshTransitionStepMessage(step, msg) || changed
		changed = applyDatameshReplicaRequestMessages(
			rv, scheduled.Intersect(all),
			"Datamesh is forming (adopt), waiting for other replicas to be scheduled",
		) || changed
		return rf.ContinueAndRequeue().ReportChangedIf(changed)
	}

	// Gate: all replicas in maintenance mode with DatameshRequest Join.
	inMaintenance := idset.FromWhere(*rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		req := rvr.Status.DatameshRequest
		return req != nil && req.Operation == v1alpha1.DatameshMembershipRequestOperationJoin &&
			obju.StatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType).
				ReasonEqual(v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonInMaintenance).
				ObservedGenerationCurrent().
				Eval()
	})
	waitingMaintenance := all.Difference(inMaintenance)
	if !waitingMaintenance.IsEmpty() {
		msg := fmt.Sprintf("Waiting for %d/%d replicas to enter maintenance mode with DatameshRequest Join: [%s]",
			waitingMaintenance.Len(), all.Len(), waitingMaintenance)
		changed = applyDatameshTransitionStepMessage(step, msg) || changed
		changed = applyDatameshReplicaRequestMessages(
			rv, inMaintenance,
			"Datamesh is forming (adopt), waiting for other replicas to enter maintenance mode",
		) || changed
		return rf.ContinueAndRequeue().ReportChangedIf(changed)
	}

	// Gate: all diskful replicas have UpToDate backing volume.
	bvReady := idset.FromWhere(*rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		return diskful.Contains(rvr.ID()) &&
			rvr.Status.BackingVolume != nil &&
			rvr.Status.BackingVolume.State == v1alpha1.DiskStateUpToDate
	})
	waitingBV := diskful.Difference(bvReady)
	if !waitingBV.IsEmpty() {
		msg := fmt.Sprintf("Waiting for %d/%d replicas to have UpToDate backing volume: [%s]",
			waitingBV.Len(), diskful.Len(), waitingBV)
		changed = applyDatameshTransitionStepMessage(step, msg) || changed
		changed = applyDatameshReplicaRequestMessages(
			rv, bvReady,
			"Datamesh is forming (adopt), waiting for other replicas to have UpToDate backing volume",
		) || changed
		return rf.ContinueAndRequeue().ReportChangedIf(changed)
	}

	// Gate: diskful replica count matches intended configuration.
	targetDiskfulCount := computeIntendedDiskfulReplicaCount(rv)
	if diskful.Len() != int(targetDiskfulCount) {
		msg := fmt.Sprintf(
			"Diskful replica count mismatch: found %d, expected %d (FTT=%d + GMDR=%d + 1). "+
				"Pre-existing replicas do not match the intended configuration",
			diskful.Len(), targetDiskfulCount,
			rv.Status.Configuration.FailuresToTolerate,
			rv.Status.Configuration.GuaranteedMinimumDataRedundancy,
		)
		changed = applyDatameshTransitionStepMessage(step, msg) || changed
		changed = applyDatameshReplicaRequestMessages(
			rv, diskful,
			fmt.Sprintf("Datamesh formation blocked: found %d diskful replicas, expected %d (FTT=%d + GMDR=%d + 1)",
				diskful.Len(), targetDiskfulCount,
				rv.Status.Configuration.FailuresToTolerate,
				rv.Status.Configuration.GuaranteedMinimumDataRedundancy),
		) || changed
		return rf.ContinueAndRequeue().ReportChangedIf(changed)
	}

	// Gate: tiebreaker replica count matches intended configuration.
	// TB = 1 if D is even AND FTT == D/2, else 0.
	var targetTBCount int
	if targetDiskfulCount%2 == 0 && targetDiskfulCount > 0 &&
		rv.Status.Configuration.FailuresToTolerate == targetDiskfulCount/2 {
		targetTBCount = 1
	}
	if tiebreakers.Len() != targetTBCount {
		msg := fmt.Sprintf(
			"TieBreaker replica count mismatch: found %d, expected %d (D=%d, FTT=%d). "+
				"Pre-existing replicas do not match the intended configuration",
			tiebreakers.Len(), targetTBCount, targetDiskfulCount,
			rv.Status.Configuration.FailuresToTolerate,
		)
		changed = applyDatameshTransitionStepMessage(step, msg) || changed
		changed = applyDatameshReplicaRequestMessages(
			rv, all,
			fmt.Sprintf("Datamesh formation blocked: found %d tiebreaker replicas, expected %d",
				tiebreakers.Len(), targetTBCount),
		) || changed
		return rf.ContinueAndRequeue().ReportChangedIf(changed)
	}

	// Gate: all replicas have addresses for required networks.
	requiredNetworks := rv.Status.Datamesh.SystemNetworkNames
	missingAddresses := idset.FromWhere(*rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		if !all.Contains(rvr.ID()) {
			return false
		}
		matchCount := 0
		for _, addr := range rvr.Status.Addresses {
			if slices.Contains(requiredNetworks, addr.SystemNetworkName) {
				matchCount++
			}
		}
		return matchCount != len(requiredNetworks)
	})
	if !missingAddresses.IsEmpty() {
		okReplicas := all.Difference(missingAddresses)
		msg := fmt.Sprintf(
			"Address configuration mismatch: replicas [%s] do not have addresses for all required networks %v",
			missingAddresses.String(), requiredNetworks,
		)
		changed = applyDatameshTransitionStepMessage(step, msg) || changed
		changed = applyDatameshReplicaRequestMessages(
			rv, okReplicas,
			"Datamesh is forming (adopt), waiting for other replicas to report required addresses",
		) || changed
		changed = applyDatameshReplicaRequestMessages(
			rv, missingAddresses,
			"Replica addresses do not match required network configuration, blocking datamesh formation",
		) || changed
		return rf.ContinueAndRequeue().ReportChangedIf(changed)
	}

	// Gate: all replicas are on eligible nodes.
	notEligible := idset.FromWhere(*rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		return all.Contains(rvr.ID()) && rsp.FindEligibleNode(rvr.Spec.NodeName) == nil
	})
	if !notEligible.IsEmpty() {
		okReplicas := all.Difference(notEligible)
		msg := fmt.Sprintf("Replicas [%s] are placed on nodes not in eligible nodes list", notEligible.String())
		changed = applyDatameshTransitionStepMessage(step, msg) || changed
		changed = applyDatameshReplicaRequestMessages(
			rv, okReplicas,
			"Datamesh is forming (adopt), waiting for other replicas to be on eligible nodes",
		) || changed
		changed = applyDatameshReplicaRequestMessages(
			rv, notEligible,
			"Replica is placed on a node not in the eligible nodes list, blocking datamesh formation",
		) || changed
		return rf.ContinueAndRequeue().ReportChangedIf(changed)
	}

	// Gate: replica spec matches pending transition.
	specMismatch := idset.FromWhere(*rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		if !all.Contains(rvr.ID()) {
			return false
		}
		req := rvr.Status.DatameshRequest
		if req == nil {
			return false
		}
		return rvr.Spec.Type != req.Type ||
			rvr.Spec.LVMVolumeGroupName != req.LVMVolumeGroupName ||
			rvr.Spec.LVMVolumeGroupThinPoolName != req.ThinPoolName
	})
	if !specMismatch.IsEmpty() {
		okReplicas := all.Difference(specMismatch)
		msg := fmt.Sprintf("Replicas [%s] have spec changes that don't match pending transition", specMismatch.String())
		changed = applyDatameshTransitionStepMessage(step, msg) || changed
		changed = applyDatameshReplicaRequestMessages(
			rv, okReplicas,
			"Datamesh is forming (adopt), waiting for other replicas to resolve spec mismatch",
		) || changed
		changed = applyDatameshReplicaRequestMessages(
			rv, specMismatch,
			"Replica spec does not match pending transition, blocking datamesh formation",
		) || changed
		return rf.ContinueAndRequeue().ReportChangedIf(changed)
	}

	// Gate: backing volume size is sufficient.
	insufficientSize := idset.FromWhere(*rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		if !diskful.Contains(rvr.ID()) {
			return false
		}
		if rvr.Status.BackingVolume == nil || rvr.Status.BackingVolume.Size == nil {
			return false
		}
		usable := drbd_size.UsableSize(*rvr.Status.BackingVolume.Size)
		return usable.Cmp(rv.Status.Datamesh.Size) < 0
	})
	if !insufficientSize.IsEmpty() {
		okReplicas := diskful.Difference(insufficientSize)
		msg := fmt.Sprintf(
			"Replicas [%s] have insufficient backing volume size for datamesh (required: %s)",
			insufficientSize.String(), rv.Status.Datamesh.Size.String(),
		)
		changed = applyDatameshTransitionStepMessage(step, msg) || changed
		changed = applyDatameshReplicaRequestMessages(
			rv, okReplicas,
			"Datamesh is forming (adopt), waiting for other replicas to resolve backing volume size issue",
		) || changed
		changed = applyDatameshReplicaRequestMessages(
			rv, insufficientSize,
			"Replica backing volume size is insufficient for datamesh, blocking formation",
		) || changed
		return rf.ContinueAndRequeue().ReportChangedIf(changed)
	}

	// All prerequisites verified — advance to PopulateAndVerifyDatamesh.
	advanceFormationStep(t, adoptStepIdxVerifyPrerequisites)
	return r.reconcileAdoptStepPopulateAndVerifyDatamesh(rf.Ctx(), rv, rvrs, rsp, t)
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: adopt/v1 — PopulateAndVerifyDatamesh
//

// reconcileAdoptStepPopulateAndVerifyDatamesh populates the datamesh (shared
// secret, members, quorum) from pre-existing replicas and then verifies that
// the auto-generated configuration (RV → RVR → DRBDR) is consistent with the
// actual DRBD state. All checks run while DRBDR is in maintenance mode, so DRBD
// does NOT react to configuration changes; consistency is checked indirectly via
// RVR status, because the DRBDR controller reports actual DRBD state even in
// maintenance.
//
// Reconcile pattern: Pure orchestration
func (r *Reconciler) reconcileAdoptStepPopulateAndVerifyDatamesh(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rvrs *[]*v1alpha1.ReplicatedVolumeReplica,
	rsp *rspView,
	t *v1alpha1.ReplicatedVolumeDatameshTransition,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "adopt-populate-and-verify-datamesh")
	defer rf.OnEnd(&outcome)

	step := &t.Steps[adoptStepIdxPopulateAndVerifyDatamesh]
	changed := false

	diskful := idset.FromWhere(*rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		return rvr.Spec.Type == v1alpha1.ReplicaTypeDiskful && rvr.DeletionTimestamp == nil
	})
	tiebreakers := idset.FromWhere(*rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		return rvr.Spec.Type == v1alpha1.ReplicaTypeTieBreaker && rvr.DeletionTimestamp == nil
	})
	access := idset.FromWhere(*rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		return rvr.Spec.Type == v1alpha1.ReplicaTypeAccess && rvr.DeletionTimestamp == nil
	})
	all := diskful.Union(tiebreakers).Union(access)

	// Populate datamesh: generate shared secret and add all replicas as members.
	if len(rv.Status.Datamesh.Members) == 0 {
		if rv.Status.Datamesh.SharedSecret == "" {
			secret, err := generateSharedSecret()
			if err != nil {
				return rf.Fail(err)
			}
			rv.Status.Datamesh.SharedSecretAlg = v1alpha1.SharedSecretAlgSHA256
			rv.Status.Datamesh.SharedSecret = secret
		}

		attachedCount := 0
		for _, rvr := range *rvrs {
			if !all.Contains(rvr.ID()) {
				continue
			}
			var zone string
			if en := rsp.FindEligibleNode(rvr.Spec.NodeName); en != nil {
				zone = en.ZoneName
			}
			attached := rvr.Status.Attachment != nil
			if attached {
				attachedCount++
			}
			req := rvr.Status.DatameshRequest
			applyDatameshMember(rv, v1alpha1.DatameshMember{
				Name:                       rvr.Name,
				Type:                       v1alpha1.DatameshMemberType(req.Type),
				NodeName:                   rvr.Spec.NodeName,
				Zone:                       zone,
				Addresses:                  slices.Clone(rvr.Status.Addresses),
				LVMVolumeGroupName:         req.LVMVolumeGroupName,
				LVMVolumeGroupThinPoolName: req.ThinPoolName,
				Attached:                   attached,
			})
		}

		rv.Status.Datamesh.Multiattach = attachedCount > 1
		rv.Status.BaselineGuaranteedMinimumDataRedundancy = rv.Status.Configuration.GuaranteedMinimumDataRedundancy

		quorum, quorumMinimumRedundancy := computeTargetQuorum(rv)
		rv.Status.Datamesh.Quorum = quorum
		rv.Status.Datamesh.QuorumMinimumRedundancy = quorumMinimumRedundancy

		rv.Status.DatameshRevision++
		step.DatameshRevision = rv.Status.DatameshRevision
		applyDatameshReplicaRequestMessages(rv, all, "Datamesh is forming (adopt), waiting for replica to apply new configuration")
		applyDatameshTransitionStepMessage(step, fmt.Sprintf(
			"Replicas [%s] added to datamesh. Verifying configuration consistency",
			all.String(),
		))

		return rf.Continue().ReportChanged()
	}

	// Gate: all replicas have observed the datamesh revision (agent processed the DRBDR spec).
	// This uses DatameshRevisionObservedByAgent (not DatameshRevision) because the
	// agent may be in maintenance mode and unable to fully apply the configuration.
	// We only need the agent to have *seen* the spec — the subsequent gates (connections,
	// UpToDate) verify that the actual DRBD state is correct.
	observed := idset.FromWhere(*rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		return all.Contains(rvr.ID()) &&
			rvr.Status.DatameshRevisionObservedByAgent >= rv.Status.DatameshRevision
	})
	if observed != all {
		notObserved := all.Difference(observed)
		changed = applyDatameshTransitionStepMessage(step, fmt.Sprintf(
			"Waiting for replicas [%s] to observe datamesh revision %d",
			notObserved.String(), rv.Status.DatameshRevision,
		)) || changed
		changed = applyDatameshReplicaRequestMessages(
			rv, notObserved,
			"Datamesh is forming (adopt), waiting for agent to process new configuration",
		) || changed
		return rf.ContinueAndRequeue().ReportChangedIf(changed)
	}

	// Gate: datamesh members match active replicas.
	dmAll := idset.FromWhere(rv.Status.Datamesh.Members, func(m v1alpha1.DatameshMember) bool {
		return m.Type == v1alpha1.DatameshMemberTypeDiskful ||
			m.Type == v1alpha1.DatameshMemberTypeTieBreaker ||
			m.Type == v1alpha1.DatameshMemberTypeAccess
	})
	if dmAll != all {
		msg := fmt.Sprintf(
			"Datamesh members mismatch: datamesh has [%s], but active RVRs are [%s]",
			dmAll.String(), all.String(),
		)
		changed = applyDatameshTransitionStepMessage(step, msg) || changed
		changed = applyDatameshReplicaRequestMessages(
			rv, all,
			"Datamesh members mismatch, blocking formation",
		) || changed
		return rf.ContinueAndRequeue().ReportChangedIf(changed)
	}

	// Gate: all replicas report connected to expected peers.
	// Star topology: D (FM) connects to all others; TB/A (star) connect only to D.
	connected := idset.FromWhere(*rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		if !all.Contains(rvr.ID()) {
			return false
		}
		var expectedPeers idset.IDSet
		if rvr.Spec.Type == v1alpha1.ReplicaTypeDiskful {
			expectedPeers = all.Difference(idset.Of(rvr.ID()))
		} else {
			expectedPeers = diskful
		}
		actualPeers := idset.FromWhere(rvr.Status.Peers, func(p v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus) bool {
			return p.ConnectionState == v1alpha1.ConnectionStateConnected
		})
		return expectedPeers.Difference(actualPeers).IsEmpty()
	})
	if connected != all {
		notConnected := all.Difference(connected)
		changed = applyDatameshTransitionStepMessage(step, fmt.Sprintf(
			"Waiting for replicas [%s] to establish connections with all peers",
			notConnected.String(),
		)) || changed
		changed = applyDatameshReplicaRequestMessages(
			rv, notConnected,
			"Datamesh is forming (adopt), replica not connected to all expected peers",
		) || changed
		changed = applyDatameshReplicaRequestMessages(
			rv, connected,
			"Datamesh is forming (adopt), waiting for other replicas to establish connections",
		) || changed
		return rf.ContinueAndRequeue().ReportChangedIf(changed)
	}

	// Gate: all diskful replicas report UpToDate backing volume.
	dmDiskful := idset.FromWhere(rv.Status.Datamesh.Members, func(m v1alpha1.DatameshMember) bool {
		return m.Type == v1alpha1.DatameshMemberTypeDiskful
	})
	upToDate := idset.FromWhere(*rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		return dmDiskful.Contains(rvr.ID()) &&
			rvr.Status.BackingVolume != nil &&
			rvr.Status.BackingVolume.State == v1alpha1.DiskStateUpToDate
	})
	if upToDate != dmDiskful {
		notUpToDate := dmDiskful.Difference(upToDate)
		changed = applyDatameshTransitionStepMessage(step, fmt.Sprintf(
			"Waiting for replicas [%s] to reach UpToDate state",
			notUpToDate.String(),
		)) || changed
		changed = applyDatameshReplicaRequestMessages(rv, notUpToDate, "Datamesh is forming (adopt), waiting for backing volume to become UpToDate") || changed
		changed = applyDatameshReplicaRequestMessages(rv, upToDate, "Datamesh is forming (adopt), replica is UpToDate, waiting for remaining replicas") || changed
		return rf.ContinueAndRequeue().ReportChangedIf(changed)
	}

	// Gate: quorum minimum redundancy does not exceed diskful count.
	if int(rv.Status.Datamesh.QuorumMinimumRedundancy) > dmDiskful.Len() {
		msg := fmt.Sprintf(
			"Quorum minimum redundancy (%d) exceeds diskful replica count (%d); "+
				"lifting maintenance would cause quorum loss",
			rv.Status.Datamesh.QuorumMinimumRedundancy, dmDiskful.Len(),
		)
		changed = applyDatameshTransitionStepMessage(step, msg) || changed
		changed = applyDatameshReplicaRequestMessages(
			rv, all,
			"Quorum minimum redundancy exceeds diskful count, blocking formation",
		) || changed
		return rf.ContinueAndRequeue().ReportChangedIf(changed)
	}

	// Datamesh populated and verified — advance to ExitMaintenance.
	advanceFormationStep(t, adoptStepIdxPopulateAndVerifyDatamesh)
	return r.reconcileAdoptStepExitMaintenance(rf.Ctx(), rv, rvrs, t)
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: adopt/v1 — ExitMaintenance
//

// reconcileAdoptStepExitMaintenance waits for all datamesh member replicas to
// exit maintenance mode and verifies they become healthy (Ready). Currently the
// step only waits; in the future the controller will actively lift maintenance.
//
// Reconcile pattern: Pure orchestration
func (r *Reconciler) reconcileAdoptStepExitMaintenance(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rvrs *[]*v1alpha1.ReplicatedVolumeReplica,
	t *v1alpha1.ReplicatedVolumeDatameshTransition,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "adopt-exit-maintenance")
	defer rf.OnEnd(&outcome)

	step := &t.Steps[adoptStepIdxExitMaintenance]
	changed := false

	dmAll := idset.FromWhere(rv.Status.Datamesh.Members, func(m v1alpha1.DatameshMember) bool {
		return m.Type == v1alpha1.DatameshMemberTypeDiskful ||
			m.Type == v1alpha1.DatameshMemberTypeTieBreaker ||
			m.Type == v1alpha1.DatameshMemberTypeAccess
	})

	// Gate: all replicas exited maintenance mode.
	exitedMaintenance := idset.FromWhere(*rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		return dmAll.Contains(rvr.ID()) &&
			obju.StatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType).
				IsTrue().
				ReasonNotEqual(v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonInMaintenance).
				ObservedGenerationCurrent().
				Eval()
	})
	if exitedMaintenance != dmAll {
		stillInMaintenance := dmAll.Difference(exitedMaintenance)
		changed = applyDatameshTransitionStepMessage(step, fmt.Sprintf(
			"Waiting for replicas [%s] to exit maintenance mode",
			stillInMaintenance.String(),
		)) || changed
		return rf.ContinueAndRequeue().ReportChangedIf(changed)
	}

	// Gate: all replicas are Ready.
	ready := idset.FromWhere(*rvrs, func(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
		return dmAll.Contains(rvr.ID()) &&
			obju.StatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondReadyType).
				IsTrue().
				ObservedGenerationCurrent().
				Eval()
	})
	if ready != dmAll {
		notReady := dmAll.Difference(ready)
		changed = applyDatameshTransitionStepMessage(step, fmt.Sprintf(
			"Waiting for replicas [%s] to become Ready",
			notReady.String(),
		)) || changed
		return rf.ContinueAndRequeue().ReportChangedIf(changed)
	}

	// All replicas healthy — formation complete.
	changed = applyFormationTransitionAbsent(rv) || changed
	return rf.ContinueAndRequeue().ReportChangedIf(changed)
}

// ──────────────────────────────────────────────────────────────────────────────
// Formation helpers
//

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
// When true, also returns the current step index (0 = Preconfigure, the starting point).
// stepIdx=0 is returned both for "never formed" and "Preconfigure in progress" — both
// lead to reconcileFormationStepPreconfigure which creates the transition if absent.
func isFormationInProgress(rv *v1alpha1.ReplicatedVolume) (bool, int) {
	if rv.Status.DatameshRevision == 0 {
		return true, 0
	}

	for i := range rv.Status.DatameshTransitions {
		if rv.Status.DatameshTransitions[i].Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation {
			// Find the first non-completed step index.
			for j := range rv.Status.DatameshTransitions[i].Steps {
				if rv.Status.DatameshTransitions[i].Steps[j].Status != v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted {
					return true, j
				}
			}
			// All steps completed — should not happen (transition is removed on completion).
			return true, 0
		}
	}
	return false, 0
}

// ensureFormationTransition finds the existing Formation transition or creates a new one.
// planID selects the step layout (create/v1 or adopt/v1); it is stored in the transition
// on creation and ignored on subsequent calls (the persisted PlanID is authoritative).
// Returns a pointer to the transition and whether it was just created.
//
// Exception: uses metav1.Now() for StartedAt when creating a new transition.
// This is controller-owned state (persisted decision timestamp), acceptable here
// because the value is set once and stabilized across subsequent reconciliations.
func ensureFormationTransition(rv *v1alpha1.ReplicatedVolume, planID string) (*v1alpha1.ReplicatedVolumeDatameshTransition, bool) {
	for i := range rv.Status.DatameshTransitions {
		if rv.Status.DatameshTransitions[i].Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation {
			return &rv.Status.DatameshTransitions[i], false
		}
	}

	// Select step names and count based on planID.
	var stepCount int
	var stepNames []string
	switch planID {
	case formationPlanAdopt:
		stepCount = adoptStepCount
		stepNames = adoptStepNames[:]
	default:
		stepCount = formationStepCount
		stepNames = formationStepNames[:]
	}

	// Create with all steps pre-declared. First step is Active, rest are Pending.
	now := metav1.Now()
	steps := make([]v1alpha1.ReplicatedVolumeDatameshTransitionStep, stepCount)
	for i := range steps {
		steps[i].Name = stepNames[i]
		steps[i].Status = v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending
	}
	steps[0].Status = v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive
	steps[0].StartedAt = &now

	rv.Status.DatameshTransitions = append(rv.Status.DatameshTransitions, v1alpha1.ReplicatedVolumeDatameshTransition{
		Type:   v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation,
		Group:  v1alpha1.ReplicatedVolumeDatameshTransitionGroupFormation,
		PlanID: planID,
		Steps:  steps,
	})
	return &rv.Status.DatameshTransitions[len(rv.Status.DatameshTransitions)-1], true
}

// advanceFormationStep completes the step at fromIdx and activates the next step.
// Clears the message on the completed step (no leftover "waiting for..." text).
//
// Exception: uses metav1.Now() for timestamps (controller-owned state).
func advanceFormationStep(t *v1alpha1.ReplicatedVolumeDatameshTransition, fromIdx int) {
	now := metav1.Now()
	t.Steps[fromIdx].Status = v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted
	t.Steps[fromIdx].CompletedAt = &now
	t.Steps[fromIdx].Message = ""
	t.Steps[fromIdx+1].Status = v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive
	t.Steps[fromIdx+1].StartedAt = &now
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
