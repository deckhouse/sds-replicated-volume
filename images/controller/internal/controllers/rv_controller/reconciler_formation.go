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
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/drbd_size"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/idset"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

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
		changed = applyFormationTransitionMessage(rv, msg) || changed
		changed = applyDatameshReplicaRequestMessages(
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
		changed = applyDatameshReplicaRequestMessages(
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
		changed = applyDatameshReplicaRequestMessages(
			rv, okReplicas,
			"Datamesh is forming, waiting for other replicas to report required addresses",
		) || changed
		changed = applyDatameshReplicaRequestMessages(
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
		changed = applyDatameshReplicaRequestMessages(
			rv, okReplicas,
			"Datamesh is forming, waiting for other replicas to be on eligible nodes",
		) || changed
		changed = applyDatameshReplicaRequestMessages(
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
		changed = applyFormationTransitionMessage(rv, msg) || changed
		changed = applyDatameshReplicaRequestMessages(
			rv, okReplicas,
			"Datamesh is forming, waiting for other replicas to resolve spec mismatch",
		) || changed
		changed = applyDatameshReplicaRequestMessages(
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
		changed = applyDatameshReplicaRequestMessages(
			rv, okReplicas,
			"Datamesh is forming, waiting for other replicas to resolve backing volume size issue",
		) || changed
		changed = applyDatameshReplicaRequestMessages(
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

		// Set effective layout from the configuration that formation is building towards.
		// Must be set before computeTargetQuorum, which derives q/qmr from it.
		rv.Status.EffectiveLayout = v1alpha1.ReplicatedVolumeEffectiveLayout{
			FailuresToTolerate:              rv.Status.Configuration.FailuresToTolerate,
			GuaranteedMinimumDataRedundancy: rv.Status.Configuration.GuaranteedMinimumDataRedundancy,
		}

		// Quorum settings control DRBD split-brain prevention based on replica count.
		quorum, quorumMinimumRedundancy := computeTargetQuorum(rv)
		rv.Status.Datamesh.Quorum = quorum
		rv.Status.Datamesh.QuorumMinimumRedundancy = quorumMinimumRedundancy

		rv.Status.DatameshRevision++
		applyDatameshReplicaRequestMessages(rv, diskful, "Datamesh is forming, waiting for replica to apply new configuration")
		applyFormationTransitionMessage(rv, fmt.Sprintf(
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
		changed = applyFormationTransitionMessage(rv, msg) || changed
		changed = applyDatameshReplicaRequestMessages(rv, diskful, msg) || changed
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
		changed = applyDatameshReplicaRequestMessages(rv, notConfigured, "Datamesh is forming, waiting for DRBD configuration to continue") || changed
		changed = applyDatameshReplicaRequestMessages(rv, configured, "Datamesh is forming, DRBD configured, waiting for other replicas") || changed
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
		changed = applyDatameshReplicaRequestMessages(rv, diskful, "Datamesh is forming, waiting for all replicas to connect to each other") || changed
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
		changed = applyDatameshReplicaRequestMessages(rv, notReady, "Datamesh is forming, waiting for data bootstrap readiness (requires backing volume Inconsistent and replication Established with all peers)") || changed
		changed = applyDatameshReplicaRequestMessages(rv, readyForDataBootstrap, "Datamesh is forming, ready for data bootstrap, waiting for other replicas") || changed
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
			changed = applyDatameshReplicaRequestMessages(rv, dmDiskful, "Datamesh is forming, restarting due to data bootstrap operation parameter mismatch") || changed

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
		changed = applyDatameshReplicaRequestMessages(rv, dmDiskful, "Datamesh is forming, restarting due to failed data bootstrap operation") || changed

		return r.reconcileFormationRestartIfTimeoutPassed(rf.Ctx(), rv, rvrs, rsc, 30*time.Second).
			ReportChangedIf(changed)
	}

	// Verify the DRBDResourceOperation has completed successfully.
	// If it is still running or pending — update messages and wait.
	if drbdrOp.Status.Phase != v1alpha1.DRBDOperationPhaseSucceeded {
		changed = applyFormationTransitionMessage(rv, "Data bootstrap initiated, waiting for operation to complete. "+dataBootstrapModeMsg) || changed
		changed = applyDatameshReplicaRequestMessages(rv, dmDiskful, "Datamesh is forming, waiting for data bootstrap to complete") || changed

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
		changed = applyDatameshReplicaRequestMessages(rv, notUpToDate, "Datamesh is forming, data bootstrap in progress, waiting for backing volume to become UpToDate") || changed
		changed = applyDatameshReplicaRequestMessages(rv, upToDate, "Datamesh is forming, data bootstrap in progress, replica is UpToDate, waiting for remaining replicas") || changed

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

	// Reset configuration and datamesh state, then immediately re-derive
	// configuration so the ConfigurationReady condition does not flicker.
	rv.Status.Configuration = nil
	rv.Status.ConfigurationGeneration = 0
	rv.Status.ConfigurationObservedGeneration = 0
	rv.Status.DatameshRevision = 0
	rv.Status.Datamesh = v1alpha1.ReplicatedVolumeDatamesh{}
	rv.Status.EffectiveLayout = v1alpha1.ReplicatedVolumeEffectiveLayout{}
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
