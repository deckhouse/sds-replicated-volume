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

package rvrcontroller

import (
	"context"
	"fmt"
	"slices"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

// ensureConditionAttached ensures the RVR Attached condition reflects the current DRBDR state.
func ensureConditionAttached(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	drbdr *v1alpha1.DRBDResource,
	datameshMember *v1alpha1.DatameshMember,
	agentReady bool,
	drbdrConfigurationPending bool,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "condition-attached")
	defer ef.OnEnd(&outcome)

	changed := false

	// Compute flags.
	intendedAttached := datameshMember != nil && datameshMember.Attached
	actualAttached := drbdr != nil &&
		drbdr.Status.ActiveConfiguration != nil &&
		drbdr.Status.ActiveConfiguration.Role == v1alpha1.DRBDRolePrimary

	// Guard: Condition not relevant (no DRBDR OR (not intended AND not actual)).
	if drbdr == nil || (!intendedAttached && !actualAttached) {
		changed = applyAttachedCondAbsent(rvr) || changed
		return ef.Ok().ReportChangedIf(changed)
	}

	// From here: drbdr != nil AND (intendedAttached OR actualAttached).

	// Guard: agent not ready.
	if !agentReady {
		changed = applyAttachedCondUnknown(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonAgentNotReady,
			"Agent is not ready") || changed
		return ef.Ok().ReportChangedIf(changed)
	}

	// Guard: configuration is being applied.
	if drbdrConfigurationPending {
		changed = applyAttachedCondUnknown(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonApplyingConfiguration,
			"DRBD configuration is being applied") || changed
		return ef.Ok().ReportChangedIf(changed)
	}

	// Normal path.
	switch {
	case intendedAttached && !actualAttached:
		// Expected attached, but not attached.
		changed = applyAttachedCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonAttachmentFailed,
			"Expected to be attached, but not attached; see DRBDConfigured condition") || changed

	case !intendedAttached && actualAttached:
		// Should be detached, but still attached.
		changed = applyAttachedCondTrue(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonDetachmentFailed,
			"Expected to be detached, but still attached; see DRBDConfigured condition") || changed

	case actualAttached && drbdr.Status.DeviceIOSuspended != nil && *drbdr.Status.DeviceIOSuspended:
		// Attached but I/O is suspended.
		changed = applyAttachedCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonIOSuspended,
			"Attached, but I/O is suspended; see Ready and DRBDConfigured conditions") || changed

	default:
		// intendedAttached AND actualAttached AND I/O not suspended — all good.
		changed = applyAttachedCondTrue(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonAttached,
			"Attached and ready for I/O") || changed
	}

	return ef.Ok().ReportChangedIf(changed)
}

// ensureConditionFullyConnected ensures the RVR FullyConnected condition reflects the current peer connectivity.
func ensureConditionFullyConnected(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	drbdr *v1alpha1.DRBDResource,
	datamesh *v1alpha1.ReplicatedVolumeDatamesh,
	agentReady bool,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "condition-fully-connected")
	defer ef.OnEnd(&outcome)

	changed := false

	// Guard: Condition not relevant.
	// - no DRBDR exists, OR
	// - not a datamesh member AND no drbdr peers, OR
	// - no addresses configured (no system networks to check against)
	isMember := datamesh != nil && datamesh.FindMemberByName(rvr.Name) != nil
	if drbdr == nil || (!isMember && len(drbdr.Status.Peers) == 0) || len(rvr.Status.Addresses) == 0 {
		changed = applyFullyConnectedCondAbsent(rvr) || changed
		return ef.Ok().ReportChangedIf(changed)
	}

	// Guard: agent not ready.
	if !agentReady {
		changed = applyFullyConnectedCondUnknown(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedReasonAgentNotReady,
			"Agent is not ready") || changed
		return ef.Ok().ReportChangedIf(changed)
	}

	// Guard: no peers.
	if len(rvr.Status.Peers) == 0 {
		if isMember && len(datamesh.Members) == 1 {
			// Sole datamesh member — no peers expected, trivially fully connected.
			changed = applyFullyConnectedCondTrue(rvr,
				v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedReasonSoleMember,
				"Sole datamesh member, no peers expected") || changed
		} else {
			changed = applyFullyConnectedCondFalse(rvr,
				v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedReasonNoPeers,
				"No peers configured") || changed
		}
		return ef.Ok().ReportChangedIf(changed)
	}

	// Count connection states from merged peers.
	// Empty ConnectionState means peer is pending (not in drbdr) → count as not connected.
	var fullyConnected, partiallyConnected, notConnected int
	for i := range rvr.Status.Peers {
		p := &rvr.Status.Peers[i]
		if p.ConnectionState == v1alpha1.ConnectionStateConnected {
			// Check if all system networks are established.
			// Fully connected = all addresses present in ConnectionEstablishedOn.
			allEstablished := len(rvr.Status.Addresses) > 0
			for _, addr := range rvr.Status.Addresses {
				if !slices.Contains(p.ConnectionEstablishedOn, addr.SystemNetworkName) {
					allEstablished = false
					break
				}
			}
			if allEstablished {
				fullyConnected++
			} else {
				partiallyConnected++
			}
		} else {
			// Empty ConnectionState (pending) or any other state → not connected.
			notConnected++
		}
	}

	totalPeers := len(rvr.Status.Peers)
	connectedPeers := fullyConnected + partiallyConnected

	// Special case: not a datamesh member but has connections.
	if !isMember && connectedPeers > 0 {
		changed = applyFullyConnectedCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedReasonPartiallyConnected,
			fmt.Sprintf("Connected to %d peers, but should not be connected to any (not a datamesh member)", connectedPeers)) || changed
		return ef.Ok().ReportChangedIf(changed)
	}

	switch {
	case fullyConnected == totalPeers:
		// All peers fully connected on all paths.
		changed = applyFullyConnectedCondTrue(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedReasonFullyConnected,
			"Fully connected to all peers on all paths") || changed
	case notConnected == totalPeers:
		// Not connected to any peer.
		changed = applyFullyConnectedCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedReasonNotConnected,
			"Not connected to any peer") || changed
	case notConnected == 0:
		// All peers connected but not all paths established.
		changed = applyFullyConnectedCondTrue(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedReasonConnectedToAllPeers,
			fmt.Sprintf("Connected to all %d peers, but not all paths are established", totalPeers)) || changed
	default:
		// Partially connected.
		changed = applyFullyConnectedCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedReasonPartiallyConnected,
			fmt.Sprintf("Connected to %d of %d peers (%d fully, %d partially, %d not connected)",
				connectedPeers, totalPeers, fullyConnected, partiallyConnected, notConnected)) || changed
	}

	return ef.Ok().ReportChangedIf(changed)
}

// ensureConditionBackingVolumeUpToDate ensures the RVR BackingVolumeUpToDate condition reflects the current DRBDR state.
func ensureConditionBackingVolumeUpToDate(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	drbdr *v1alpha1.DRBDResource,
	datameshMember *v1alpha1.DatameshMember,
	agentReady bool,
	drbdrConfigurationPending bool,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "condition-backing-volume-up-to-date")
	defer ef.OnEnd(&outcome)

	changed := false

	// Determine if the BackingVolumeUpToDate condition is relevant.
	// The condition is relevant only for datamesh members (if not a member, the peer is not
	// connected and the disk cannot be synchronized by definition), AND when a backing volume
	// either should exist (NeedsBackingVolume) or actually exists.
	intendedType := computeIntendedType(rvr, datameshMember)
	conditionRelevant := datameshMember != nil &&
		(intendedType.NeedsBackingVolume() ||
			rvr.Status.BackingVolume != nil)

	// Guard: no DRBDR or condition not relevant — remove it.
	if drbdr == nil || !conditionRelevant {
		changed = applyBackingVolumeUpToDateCondAbsent(rvr) || changed
		return ef.Ok().ReportChangedIf(changed)
	}

	// Guard: agent not ready.
	if !agentReady {
		changed = applyBackingVolumeUpToDateCondUnknown(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonAgentNotReady,
			"Agent is not ready") || changed
		return ef.Ok().ReportChangedIf(changed)
	}

	// Guard: configuration is being applied.
	if drbdrConfigurationPending {
		changed = applyBackingVolumeUpToDateCondUnknown(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonApplyingConfiguration,
			"DRBD configuration is being applied") || changed
		return ef.Ok().ReportChangedIf(changed)
	}

	// After passing drbdrConfigurationPending guard, ActiveConfiguration must exist.
	// If it's nil here, the state is inconsistent — likely a bug in the agent.
	if drbdr.Status.ActiveConfiguration == nil {
		return ef.Errf("drbdr.Status.ActiveConfiguration is nil after configuration is no longer pending")
	}

	// servingIO indicates that this replica is actively serving application I/O:
	// the DRBD device is Primary (attached and accepting I/O) and device I/O is not suspended.
	// Used to provide detailed messages about where I/O is being handled.
	servingIO := drbdr.Status.ActiveConfiguration.Role == v1alpha1.DRBDRolePrimary && drbdr.Status.DeviceIOSuspended != nil && !*drbdr.Status.DeviceIOSuspended

	// UpToDate is the only state with True condition — handle it separately.
	if drbdr.Status.DiskState == v1alpha1.DiskStateUpToDate {
		var message string
		if servingIO {
			message = "Backing volume is fully up-to-date; application I/O served locally"
		} else {
			message = "Backing volume is fully up-to-date"
		}
		changed = applyBackingVolumeUpToDateCondTrue(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonUpToDate,
			message) || changed
		return ef.Ok().ReportChangedIf(changed)
	}

	// All other states result in False condition.
	var reason string
	var message string

	switch drbdr.Status.DiskState {
	case v1alpha1.DiskStateDiskless:
		reason = v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonAbsent
		if servingIO {
			message = "No backing volume; application I/O forwarded to peers"
		} else {
			message = "No backing volume attached"
		}

	case v1alpha1.DiskStateAttaching:
		reason = v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonAbsent
		if servingIO {
			message = "Backing volume is being attached; reading metadata from local device; application I/O forwarded to peers"
		} else {
			message = "Backing volume is being attached; reading metadata from local device"
		}

	case v1alpha1.DiskStateDetaching:
		reason = v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonAbsent
		if servingIO {
			message = "Backing volume is being detached; application I/O transitioning to peers"
		} else {
			message = "Backing volume is being detached"
		}

	case v1alpha1.DiskStateFailed:
		reason = v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonFailed
		if servingIO {
			message = "Backing volume failed due to I/O errors; application I/O transitioning to peers"
		} else {
			message = "Backing volume failed due to I/O errors"
		}

	case v1alpha1.DiskStateNegotiating:
		reason = v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonUnknown
		if servingIO {
			message = "Backing volume state is not yet determined; negotiating with peers after attach; application I/O forwarded to peers"
		} else {
			message = "Backing volume state is not yet determined; negotiating with peers after attach"
		}

	case v1alpha1.DiskStateOutdated:
		reason = v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonRequiresSynchronization
		if servingIO {
			message = "Backing volume data is outdated; awaiting resynchronization from up-to-date peer; application I/O forwarded to peers"
		} else {
			message = "Backing volume data is outdated; awaiting resynchronization from up-to-date peer"
		}

	case v1alpha1.DiskStateConsistent:
		reason = v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonUnknown
		message = "Backing volume data was consistent; peer connection required to confirm up-to-date status"
		if servingIO {
			ef.Log().Error(nil, "unexpected: servingIO is true while disk state is Consistent; this should not happen")
		}

	case v1alpha1.DiskStateInconsistent:
		syncPeerIdx := slices.IndexFunc(rvr.Status.Peers, func(p v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus) bool {
			return p.ReplicationState == v1alpha1.ReplicationStateSyncTarget
		})
		if syncPeerIdx >= 0 {
			reason = v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonSynchronizing
			peerName := rvr.Status.Peers[syncPeerIdx].Name
			if servingIO {
				message = fmt.Sprintf("Backing volume is synchronizing from peer %s; application I/O forwarded to peers", peerName)
			} else {
				message = fmt.Sprintf("Backing volume is synchronizing from peer %s", peerName)
			}
			break
		}

		establishedPeerIdx := slices.IndexFunc(rvr.Status.Peers, func(p v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus) bool {
			return p.BackingVolumeState == v1alpha1.DiskStateUpToDate && p.ReplicationState == v1alpha1.ReplicationStateEstablished
		})
		if establishedPeerIdx >= 0 {
			// Synchronization completed with this peer, but there is no connection
			// to an attached peer — DRBD considers this "unstable" replication.
			// The local disk will not transition to UpToDate until a connection
			// with an attached up-to-date peer is established.
			reason = v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonSynchronizing
			peerName := rvr.Status.Peers[establishedPeerIdx].Name
			message = fmt.Sprintf("Backing volume synchronized via intermediate peer %s, but no direct connection to an attached peer; cannot transition to UpToDate until a direct peer connection is established", peerName)
			if servingIO {
				ef.Log().Error(nil, "unexpected: servingIO is true while synchronized via intermediate peer; this should not happen")
			}
			break
		}

		hasUpToDatePeer := slices.ContainsFunc(rvr.Status.Peers, func(p v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus) bool {
			return p.BackingVolumeState == v1alpha1.DiskStateUpToDate
		})
		if !hasUpToDatePeer {
			reason = v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonRequiresSynchronization
			message = "Backing volume requires synchronization, but no up-to-date peers are available"
			if servingIO {
				ef.Log().Error(nil, "unexpected: servingIO is true while no up-to-date peers are available; this should not happen")
			}
			break
		}

		reason = v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonRequiresSynchronization
		if servingIO {
			message = "Backing volume requires synchronization from an up-to-date peer; application I/O forwarded to peers"
		} else {
			message = "Backing volume requires synchronization from an up-to-date peer"
		}

	default:
		reason = v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonUnknown
		if servingIO {
			message = fmt.Sprintf("Unknown backing volume state: %s; application I/O forwarded to peers", drbdr.Status.DiskState)
		} else {
			message = fmt.Sprintf("Unknown backing volume state: %s", drbdr.Status.DiskState)
		}
	}

	changed = applyBackingVolumeUpToDateCondFalse(rvr, reason, message) || changed
	return ef.Ok().ReportChangedIf(changed)
}

// ensureConditionReady ensures the RVR Ready condition reflects the current DRBDR quorum state.
func ensureConditionReady(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	rv *v1alpha1.ReplicatedVolume,
	drbdr *v1alpha1.DRBDResource,
	datameshMember *v1alpha1.DatameshMember,
	agentReady bool,
	drbdrConfigurationPending bool,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "condition-ready")
	defer ef.OnEnd(&outcome)

	changed := false

	// Guard: RVR is being deleted.
	if rvrShouldNotExist(rvr) {
		changed = applyReadyCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondReadyReasonDeleting,
			"Replica is being deleted") || changed
		return ef.Ok().ReportChangedIf(changed)
	}

	// Guard: RV missing.
	if rv == nil {
		changed = applyReadyCondUnknown(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondReadyReasonWaitingForReplicatedVolume,
			"ReplicatedVolume not found; waiting for it to be present") || changed
		return ef.Ok().ReportChangedIf(changed)
	}

	// Guard: datamesh not initialized (revision 0).
	if rv.Status.DatameshRevision == 0 {
		changed = applyReadyCondUnknown(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondReadyReasonWaitingForReplicatedVolume,
			"Datamesh is not initialized yet; waiting for ReplicatedVolume to initialize it") || changed
		return ef.Ok().ReportChangedIf(changed)
	}

	// Guard: no system networks in datamesh.
	if len(rv.Status.Datamesh.SystemNetworkNames) == 0 {
		changed = applyReadyCondUnknown(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondReadyReasonWaitingForReplicatedVolume,
			"No system networks in datamesh; waiting for ReplicatedVolume to configure them") || changed
		return ef.Ok().ReportChangedIf(changed)
	}

	// Guard: node not assigned yet.
	if rvr.Spec.NodeName == "" {
		changed = applyReadyCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondReadyReasonPendingScheduling,
			"Waiting for node assignment") || changed
		return ef.Ok().ReportChangedIf(changed)
	}

	// Invariant: drbdr must be non-nil at this point. The only scenarios where
	// reconcileDRBDResource does not create a DRBDR are: (1) RVR is being deleted
	// (caught by the rvrShouldNotExist guard above), (2) RV/datamesh not ready,
	// or (3) node not assigned — (2) and (3) are handled above.
	if drbdr == nil {
		panic(fmt.Sprintf(
			"ensureConditionReady: drbdr is nil for RVR %s but all known prerequisites are met "+
				"(rv=%v, datameshRevision=%d, networks=%d, node=%q)",
			rvr.Name, rv != nil, rv.Status.DatameshRevision,
			len(rv.Status.Datamesh.SystemNetworkNames), rvr.Spec.NodeName,
		))
	}

	// Guard: agent not ready.
	if !agentReady {
		changed = applyReadyCondUnknown(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondReadyReasonAgentNotReady,
			"Agent is not ready") || changed
		return ef.Ok().ReportChangedIf(changed)
	}

	// Guard: configuration is being applied.
	if drbdrConfigurationPending {
		changed = applyReadyCondUnknown(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondReadyReasonApplyingConfiguration,
			"DRBD configuration is being applied") || changed
		return ef.Ok().ReportChangedIf(changed)
	}

	// Ready depends on quorum. How quorum works differs by replica role:
	//
	//   - Not a datamesh member yet: cannot serve I/O, waiting to join.
	//   - Diskless member (TieBreaker/Access): does not vote itself,
	//     gets quorum from connected UpToDate diskful peers.
	//   - Diskful member: votes directly, needs enough peers for majority.
	qs := rvr.Status.QuorumSummary
	numOrUnknown := func(v *int) string {
		if v == nil {
			return "unknown"
		}
		return fmt.Sprint(*v)
	}
	switch {
	case datameshMember == nil:
		if rvr.DeletionTimestamp != nil {
			// Replica is being deleted, not joining.
			changed = applyReadyCondFalse(rvr,
				v1alpha1.ReplicatedVolumeReplicaCondReadyReasonDeleting,
				"Replica is being deleted") || changed
		} else {
			// Not a datamesh member yet — cannot participate in quorum.
			changed = applyReadyCondFalse(rvr,
				v1alpha1.ReplicatedVolumeReplicaCondReadyReasonPendingDatameshJoin,
				"Waiting to join datamesh") || changed
		}

	case rvr.Status.Type == v1alpha1.DRBDResourceTypeDiskless:
		// Diskless member: quorum provided by connected peers.
		msg := fmt.Sprintf("Diskless replica; quorum via connected peers (data quorum: %d/%s)",
			qs.ConnectedUpToDatePeers, numOrUnknown(qs.QuorumMinimumRedundancy))
		if drbdr.Status.Quorum == nil || !*drbdr.Status.Quorum {
			changed = applyReadyCondFalse(rvr,
				v1alpha1.ReplicatedVolumeReplicaCondReadyReasonQuorumViaPeers,
				msg) || changed
		} else {
			changed = applyReadyCondTrue(rvr,
				v1alpha1.ReplicatedVolumeReplicaCondReadyReasonQuorumViaPeers,
				msg) || changed
		}

	default:
		// Diskful member: normal quorum voting.
		// Self counts as a diskful vote when the disk is actually attached
		// (any state except Diskless and Attaching), and as an UpToDate vote
		// only when the disk is UpToDate.
		// "Quorum: diskful M/N + tie-breakers T, data quorum: J/K"
		diskfulVotes := qs.ConnectedDiskfulPeers
		upToDateVotes := qs.ConnectedUpToDatePeers
		if bv := rvr.Status.BackingVolume; bv != nil {
			if bv.State != v1alpha1.DiskStateDiskless &&
				bv.State != v1alpha1.DiskStateAttaching {
				diskfulVotes++ // self has an attached disk
			}
			if bv.State == v1alpha1.DiskStateUpToDate {
				upToDateVotes++ // self is UpToDate
			}
		}
		msg := fmt.Sprintf("Quorum: diskful %d/%s", diskfulVotes, numOrUnknown(qs.Quorum))
		if qs.ConnectedTieBreakerPeers > 0 {
			msg += fmt.Sprintf(" + tie-breakers %d", qs.ConnectedTieBreakerPeers)
		}
		msg += fmt.Sprintf(", data quorum: %d/%s", upToDateVotes, numOrUnknown(qs.QuorumMinimumRedundancy))
		if drbdr.Status.Quorum == nil || !*drbdr.Status.Quorum {
			changed = applyReadyCondFalse(rvr,
				v1alpha1.ReplicatedVolumeReplicaCondReadyReasonQuorumLost,
				msg) || changed
		} else {
			changed = applyReadyCondTrue(rvr,
				v1alpha1.ReplicatedVolumeReplicaCondReadyReasonReady,
				msg) || changed
		}
	}

	return ef.Ok().ReportChangedIf(changed)
}

// ensureConditionSatisfyEligibleNodes ensures the RVR SatisfyEligibleNodes condition
// reflects whether the replica placement satisfies RSP eligible nodes requirements.
func ensureConditionSatisfyEligibleNodes(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	rv *v1alpha1.ReplicatedVolume,
	rspView *rspEligibilityView,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "condition-satisfy-eligible-nodes")
	defer ef.OnEnd(&outcome)

	changed := false

	// Guard: node not selected — remove condition (not applicable).
	if rvr.Spec.NodeName == "" {
		changed = applySatisfyEligibleNodesCondAbsent(rvr) || changed
		return ef.Ok().ReportChangedIf(changed)
	}

	// Guard: RV missing — condition Unknown.
	if rv == nil {
		changed = applySatisfyEligibleNodesCondUnknown(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonWaitingForReplicatedVolume,
			"ReplicatedVolume not found; waiting for it to be present") || changed
		return ef.Ok().ReportChangedIf(changed)
	}

	// Guard: no Configuration — condition Unknown.
	if rv.Status.Configuration == nil {
		changed = applySatisfyEligibleNodesCondUnknown(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonWaitingForReplicatedVolume,
			"ReplicatedVolume has no configuration yet; waiting for it to appear") || changed
		return ef.Ok().ReportChangedIf(changed)
	}

	// Guard: RSP not found — condition Unknown.
	if rspView == nil {
		changed = applySatisfyEligibleNodesCondUnknown(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonWaitingForReplicatedVolume,
			"ReplicatedStoragePool not found; waiting for it to be present") || changed
		return ef.Ok().ReportChangedIf(changed)
	}

	// Node not in eligible nodes list.
	if rspView.EligibleNode == nil {
		changed = applySatisfyEligibleNodesCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonNodeMismatch,
			"Node is not in the eligible nodes list according to ReplicatedStoragePool") || changed
		return ef.Ok().ReportChangedIf(changed)
	}

	// Check LVMVolumeGroup and ThinPool (only for Diskful).
	var lvg *v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup
	if rvr.Spec.Type == v1alpha1.ReplicaTypeDiskful && rvr.Spec.LVMVolumeGroupName != "" {
		// First, check if LVMVolumeGroup exists in eligible node (by name only).
		lvg = findLVGInEligibleNodeByName(rspView.EligibleNode, rvr.Spec.LVMVolumeGroupName)
		if lvg == nil {
			changed = applySatisfyEligibleNodesCondFalse(rvr,
				v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonLVMVolumeGroupMismatch,
				"Node is eligible, but LVMVolumeGroup is not in the allowed list for this node according to ReplicatedStoragePool") || changed
			return ef.Ok().ReportChangedIf(changed)
		}

		// Then, check ThinPool (even if empty — must match RSP type).
		lvg = findLVGInEligibleNode(rspView.EligibleNode, rvr.Spec.LVMVolumeGroupName, rvr.Spec.LVMVolumeGroupThinPoolName)
		if lvg == nil {
			msg := thinPoolMismatchMessage(rspView.Type, rvr.Spec.LVMVolumeGroupName, rvr.Spec.LVMVolumeGroupThinPoolName)
			changed = applySatisfyEligibleNodesCondFalse(rvr,
				v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonThinPoolMismatch,
				msg) || changed
			return ef.Ok().ReportChangedIf(changed)
		}
	}

	// Collect warnings.
	warnings := computeEligibilityWarnings(rspView.EligibleNode, lvg)

	// All checks passed — condition True.
	message := "Replica satisfies eligible nodes requirements"
	if warnings != "" {
		message += ", however note that currently " + warnings
	}
	changed = applySatisfyEligibleNodesCondTrue(rvr,
		v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonSatisfied,
		message) || changed

	return ef.Ok().ReportChangedIf(changed)
}

// thinPoolMismatchMessage returns a human-readable message describing why the
// ThinPool check failed. It differentiates between type mismatches (ThinPool
// specified for LVM thick, or not specified for LVMThin) and name mismatches
// (ThinPool specified but not in the eligible list).
func thinPoolMismatchMessage(rspType v1alpha1.ReplicatedStoragePoolType, lvgName, thinPool string) string {
	switch {
	case thinPool != "" && rspType == v1alpha1.ReplicatedStoragePoolTypeLVM:
		return fmt.Sprintf(
			"ThinPool %q is specified, but ReplicatedStoragePool type is LVM (thick), which does not use ThinPools",
			thinPool)
	case thinPool == "" && rspType == v1alpha1.ReplicatedStoragePoolTypeLVMThin:
		return "ThinPool is not specified, but ReplicatedStoragePool type is LVMThin, which requires a ThinPool"
	case thinPool != "":
		return fmt.Sprintf(
			"ThinPool %q is not in the allowed list for LVMVolumeGroup %q according to ReplicatedStoragePool",
			thinPool, lvgName)
	default:
		// Should not happen: LVM (thick) RSP entries have no ThinPool,
		// LVMThin RSP entries always have one. If we reach here, something
		// is inconsistent in the RSP eligible nodes data.
		return fmt.Sprintf(
			"Unexpected state: LVMVolumeGroup %q exists in eligible nodes but no entry matches the expected ThinPool configuration for this ReplicatedStoragePool type",
			lvgName)
	}
}

// findLVGInEligibleNodeByName finds an LVMVolumeGroup by name only in the eligible node.
func findLVGInEligibleNodeByName(node *v1alpha1.ReplicatedStoragePoolEligibleNode, lvgName string) *v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup {
	for i := range node.LVMVolumeGroups {
		if node.LVMVolumeGroups[i].Name == lvgName {
			return &node.LVMVolumeGroups[i]
		}
	}
	return nil
}

// findLVGInEligibleNode finds an LVMVolumeGroup by name and thin pool name in the eligible node.
func findLVGInEligibleNode(node *v1alpha1.ReplicatedStoragePoolEligibleNode, lvgName, thinPoolName string) *v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup {
	for i := range node.LVMVolumeGroups {
		if node.LVMVolumeGroups[i].Name == lvgName && node.LVMVolumeGroups[i].ThinPoolName == thinPoolName {
			return &node.LVMVolumeGroups[i]
		}
	}
	return nil
}

// computeEligibilityWarnings collects warnings about node/LVG readiness.
func computeEligibilityWarnings(node *v1alpha1.ReplicatedStoragePoolEligibleNode, lvg *v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup) string {
	var warnings []string

	if node.Unschedulable {
		warnings = append(warnings, "node is unschedulable")
	}
	if !node.NodeReady {
		warnings = append(warnings, "node is not ready")
	}
	if !node.AgentReady {
		warnings = append(warnings, "agent is not ready")
	}

	if lvg != nil {
		if lvg.Unschedulable {
			warnings = append(warnings, "LVMVolumeGroup is unschedulable")
		}
		if !lvg.Ready {
			warnings = append(warnings, "LVMVolumeGroup is not ready")
		}
	}

	// Join warnings with commas and "and" before the last element (serial comma per CMOS).
	switch len(warnings) {
	case 0:
		return ""
	case 1:
		return warnings[0]
	case 2:
		return warnings[0] + " and " + warnings[1]
	default:
		return strings.Join(warnings[:len(warnings)-1], ", ") + ", and " + warnings[len(warnings)-1]
	}
}

// applySatisfyEligibleNodesCondAbsent removes the SatisfyEligibleNodes condition.
func applySatisfyEligibleNodesCondAbsent(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
	return obju.RemoveStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType)
}

// applySatisfyEligibleNodesCondUnknown sets the SatisfyEligibleNodes condition to Unknown.
//
//nolint:unparam // reason kept for API consistency with other applyCond* helpers.
func applySatisfyEligibleNodesCondUnknown(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType,
		Status:  metav1.ConditionUnknown,
		Reason:  reason,
		Message: message,
	})
}

// applySatisfyEligibleNodesCondFalse sets the SatisfyEligibleNodes condition to False.
func applySatisfyEligibleNodesCondFalse(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

// applySatisfyEligibleNodesCondTrue sets the SatisfyEligibleNodes condition to True.
//
//nolint:unparam // reason kept for API consistency with other applyCond* helpers.
func applySatisfyEligibleNodesCondTrue(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType,
		Status:  metav1.ConditionTrue,
		Reason:  reason,
		Message: message,
	})
}

// applyAttachedCondFalse sets the Attached condition to False on RVR.
func applyAttachedCondFalse(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondAttachedType,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

// applyAttachedCondUnknown sets the Attached condition to Unknown on RVR.
func applyAttachedCondUnknown(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondAttachedType,
		Status:  metav1.ConditionUnknown,
		Reason:  reason,
		Message: message,
	})
}

// applyAttachedCondTrue sets the Attached condition to True on RVR.
func applyAttachedCondTrue(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondAttachedType,
		Status:  metav1.ConditionTrue,
		Reason:  reason,
		Message: message,
	})
}

// applyAttachedCondAbsent removes the Attached condition from RVR.
func applyAttachedCondAbsent(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
	return obju.RemoveStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondAttachedType)
}

// applyBackingVolumeUpToDateCondFalse sets the BackingVolumeUpToDate condition to False on RVR.
func applyBackingVolumeUpToDateCondAbsent(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
	return obju.RemoveStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateType)
}

func applyBackingVolumeUpToDateCondTrue(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateType,
		Status:  metav1.ConditionTrue,
		Reason:  reason,
		Message: message,
	})
}

func applyBackingVolumeUpToDateCondFalse(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateType,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

// applyBackingVolumeUpToDateCondUnknown sets the BackingVolumeUpToDate condition to Unknown on RVR.
func applyBackingVolumeUpToDateCondUnknown(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateType,
		Status:  metav1.ConditionUnknown,
		Reason:  reason,
		Message: message,
	})
}

// applyFullyConnectedCondFalse sets the FullyConnected condition to False on RVR.
func applyFullyConnectedCondFalse(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedType,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

// applyFullyConnectedCondTrue sets the FullyConnected condition to True on RVR.
func applyFullyConnectedCondTrue(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedType,
		Status:  metav1.ConditionTrue,
		Reason:  reason,
		Message: message,
	})
}

// applyFullyConnectedCondUnknown sets the FullyConnected condition to Unknown on RVR.
func applyFullyConnectedCondUnknown(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedType,
		Status:  metav1.ConditionUnknown,
		Reason:  reason,
		Message: message,
	})
}

// applyFullyConnectedCondAbsent removes the FullyConnected condition from RVR.
func applyFullyConnectedCondAbsent(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
	return obju.RemoveStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedType)
}

// applyReadyCondFalse sets the Ready condition to False on RVR.
func applyReadyCondTrue(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondReadyType,
		Status:  metav1.ConditionTrue,
		Reason:  reason,
		Message: message,
	})
}

func applyReadyCondFalse(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondReadyType,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

// applyReadyCondUnknown sets the Ready condition to Unknown on RVR.
func applyReadyCondUnknown(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondReadyType,
		Status:  metav1.ConditionUnknown,
		Reason:  reason,
		Message: message,
	})
}
