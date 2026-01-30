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
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	nodeutil "k8s.io/component-helpers/node/util"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/drbd_size"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

// ──────────────────────────────────────────────────────────────────────────────
// Wiring / construction
//

type Reconciler struct {
	cl                client.Client
	scheme            *runtime.Scheme
	log               logr.Logger
	agentPodNamespace string
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

func NewReconciler(cl client.Client, scheme *runtime.Scheme, log logr.Logger, agentPodNamespace string) *Reconciler {
	return &Reconciler{
		cl:                cl,
		scheme:            scheme,
		log:               log,
		agentPodNamespace: agentPodNamespace,
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile
//

// Reconcile pattern: Pure orchestration
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	rf := flow.BeginRootReconcile(ctx)

	rvr, err := r.getRVR(rf.Ctx(), req.Name)
	if err != nil {
		return rf.Fail(err).ToCtrl()
	}

	drbdr, err := r.getDRBDR(rf.Ctx(), req.Name)
	if err != nil {
		return rf.Failf(err, "getting DRBDResource").ToCtrl()
	}

	llvs, err := r.getLLVs(rf.Ctx(), req.Name, drbdr)
	if err != nil {
		return rf.Failf(err, "listing LVMLogicalVolumes").ToCtrl()
	}

	rv, err := r.getRV(rf.Ctx(), rvr)
	if err != nil {
		return rf.Failf(err, "getting ReplicatedVolume %s", rvr.Spec.ReplicatedVolumeName).ToCtrl()
	}

	if rvr != nil {
		// Reconcile the RVR metadata (finalizers and labels).
		outcome := r.reconcileMetadata(rf.Ctx(), rvr, rv, llvs, drbdr)
		if outcome.ShouldReturn() {
			return outcome.ToCtrl()
		}
	}

	var base *v1alpha1.ReplicatedVolumeReplica
	if rvr != nil {
		base = rvr.DeepCopy()
	}

	// Reconcile the backing volume (LVMLogicalVolume).
	targetLLVName, outcome := r.reconcileBackingVolume(rf.Ctx(), rvr, &llvs, rv, drbdr)
	if outcome.ShouldReturn() {
		return outcome.ToCtrl()
	}

	// Reconcile the DRBD resource.
	drbdr, ro := r.reconcileDRBDResource(rf.Ctx(), rvr, rv, drbdr, targetLLVName)
	outcome = outcome.Merge(ro)
	if outcome.ShouldReturn() {
		return outcome.ToCtrl()
	}

	if rvr != nil {
		// compute agentReady and drbdrConfigurationPending
		configuredCond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondConfiguredType)
		var agentReady, drbdrConfigurationPending bool
		if configuredCond != nil {
			agentReady = configuredCond.Reason != v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonAgentNotReady
			drbdrConfigurationPending = configuredCond.Reason == v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonApplyingConfiguration
		}

		// Find datamesh and datamesh member for this RVR.
		var datamesh *v1alpha1.ReplicatedVolumeDatamesh
		var datameshMember *v1alpha1.ReplicatedVolumeDatameshMember
		if rv != nil {
			datamesh = &rv.Status.Datamesh
			datameshMember = datamesh.FindMemberByName(rvr.Name)
		}

		// Ensure RVR status fields reflect the current DRBDR state.
		eo := flow.MergeEnsures(
			ensureAttachmentStatus(rf.Ctx(), rvr, drbdr, datameshMember, agentReady, drbdrConfigurationPending),
			ensurePeersStatus(rf.Ctx(), rvr, drbdr, datamesh, agentReady),
			ensureBackingVolumeStatus(rf.Ctx(), rvr, drbdr, datameshMember, agentReady, drbdrConfigurationPending),
			ensureQuorumStatus(rf.Ctx(), rvr, drbdr, agentReady, drbdrConfigurationPending),
		)
		if eo.Error() != nil {
			return rf.Failf(eo.Error(), "ensuring status").ToCtrl()
		}
		outcome = outcome.WithChangeFrom(eo)

		// Reconcile the SatisfyEligibleNodes condition.
		outcome = outcome.Merge(r.reconcileSatisfyEligibleNodesCondition(rf.Ctx(), rvr, rv))
		if outcome.ShouldReturn() {
			return outcome.ToCtrl()
		}
	}

	// Patch the RVR status if changed.
	if outcome.DidChange() && rvr != nil {
		if err := r.patchRVRStatus(rf.Ctx(), rvr, base, outcome.OptimisticLockRequired()); err != nil {
			return rf.Fail(err).ToCtrl()
		}
	}

	return outcome.ToCtrl()
}

// ensureAttachmentStatus ensures the RVR attachment-related status fields reflect the current DRBDR state.
func ensureAttachmentStatus(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	drbdr *v1alpha1.DRBDResource,
	datameshMember *v1alpha1.ReplicatedVolumeDatameshMember,
	agentReady bool,
	drbdrConfigurationPending bool,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "attachment-status")
	defer ef.OnEnd(&outcome)

	changed := false

	// Compute flags.
	intendedAttached := datameshMember != nil && datameshMember.Role == v1alpha1.DRBDRolePrimary
	actualAttached := drbdr != nil &&
		drbdr.Status.ActiveConfiguration != nil &&
		drbdr.Status.ActiveConfiguration.Role == v1alpha1.DRBDRolePrimary

	// Guard: Condition not relevant (no DRBDR OR (not intended AND not actual)).
	if drbdr == nil || (!intendedAttached && !actualAttached) {
		changed = applyRVRAttachedCondAbsent(rvr) || changed
		changed = applyRVRDevicePath(rvr, "") || changed
		changed = applyRVRDeviceIOSuspended(rvr, nil) || changed
		return ef.Ok().ReportChangedIf(changed)
	}

	// From here: drbdr != nil AND (intendedAttached OR actualAttached).

	// Guard: agent not ready.
	if !agentReady {
		changed = applyRVRAttachedCondUnknown(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonAgentNotReady,
			"Agent is not ready") || changed
		// DevicePath/DeviceIOSuspended not changed.
		return ef.Ok().ReportChangedIf(changed)
	}

	// Guard: configuration is being applied.
	if drbdrConfigurationPending {
		changed = applyRVRAttachedCondUnknown(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonApplyingConfiguration,
			"Configuration is being applied") || changed
		// DevicePath/DeviceIOSuspended not changed.
		return ef.Ok().ReportChangedIf(changed)
	}

	// Normal path.
	switch {
	case intendedAttached && !actualAttached:
		// Expected attached, but not attached.
		changed = applyRVRAttachedCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonAttachmentFailed,
			"Expected to be attached, but not attached; see Configured condition") || changed
		changed = applyRVRDevicePath(rvr, "") || changed
		changed = applyRVRDeviceIOSuspended(rvr, nil) || changed

	case !intendedAttached && actualAttached:
		// Should be detached, but still attached.
		changed = applyRVRAttachedCondTrue(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonDetachmentFailed,
			"Expected to be detached, but still attached; see Configured condition") || changed
		changed = applyRVRDevicePath(rvr, drbdr.Status.Device) || changed
		changed = applyRVRDeviceIOSuspended(rvr, drbdr.Status.DeviceIOSuspended) || changed

	case actualAttached && drbdr.Status.DeviceIOSuspended != nil && *drbdr.Status.DeviceIOSuspended:
		// Attached but I/O is suspended.
		changed = applyRVRAttachedCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonIOSuspended,
			"Attached, but I/O is suspended; see Ready and Configured conditions") || changed
		changed = applyRVRDevicePath(rvr, drbdr.Status.Device) || changed
		changed = applyRVRDeviceIOSuspended(rvr, drbdr.Status.DeviceIOSuspended) || changed

	default:
		// intendedAttached AND actualAttached AND I/O not suspended — all good.
		changed = applyRVRAttachedCondTrue(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonAttached,
			"Attached and ready for I/O") || changed
		changed = applyRVRDevicePath(rvr, drbdr.Status.Device) || changed
		changed = applyRVRDeviceIOSuspended(rvr, drbdr.Status.DeviceIOSuspended) || changed
	}

	return ef.Ok().ReportChangedIf(changed)
}

// ensurePeersStatus ensures the RVR peers-related status fields reflect the current DRBDR state.
func ensurePeersStatus(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	drbdr *v1alpha1.DRBDResource,
	datamesh *v1alpha1.ReplicatedVolumeDatamesh,
	agentReady bool,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "peers-status")
	defer ef.OnEnd(&outcome)

	changed := false

	// Guard: Condition not relevant.
	// - no DRBDR exists, OR
	// - not a datamesh member AND no drbdr peers
	isMember := datamesh != nil && datamesh.FindMemberByName(rvr.Name) != nil
	if drbdr == nil || (!isMember && len(drbdr.Status.Peers) == 0) {
		changed = applyRVRFullyConnectedCondAbsent(rvr) || changed
		changed = applyRVRStatusPeers(rvr, nil) || changed
		return ef.Ok().ReportChangedIf(changed)
	}

	// Guard: agent not ready.
	if !agentReady {
		changed = applyRVRFullyConnectedCondUnknown(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedReasonAgentNotReady,
			"Agent is not ready") || changed
		changed = applyRVRStatusPeers(rvr, nil) || changed
		return ef.Ok().ReportChangedIf(changed)
	}

	// Ensure peers status by merging datamesh.Members and drbdr.Status.Peers.
	changed = ensureRVRStatusPeers(rvr, drbdr.Status.Peers, datamesh, rvr.Name) || changed

	// Evaluate FullyConnected condition based on merged peers.
	// Note: rvr.Status.Peers now contains the merged list from datamesh + drbdr.
	// Peers with empty ConnectionState are pending (not yet in drbdr).
	rvrPeers := rvr.Status.Peers
	if len(rvrPeers) == 0 {
		changed = applyRVRFullyConnectedCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedReasonNoPeers,
			"No peers configured") || changed
		return ef.Ok().ReportChangedIf(changed)
	}

	// Get expected system network names from datamesh.
	var systemNetworkNames []string
	if datamesh != nil {
		systemNetworkNames = datamesh.SystemNetworkNames
	}

	// Count connection states from merged peers.
	// Empty ConnectionState means peer is pending (not in drbdr) → count as not connected.
	var fullyConnected, partiallyConnected, notConnected int
	for i := range rvrPeers {
		p := &rvrPeers[i]
		if p.ConnectionState == v1alpha1.ConnectionStateConnected {
			// Check if all system networks are established.
			// Fully connected = all systemNetworkNames present in ConnectionEstablishedOn.
			allEstablished := len(systemNetworkNames) > 0
			for _, sn := range systemNetworkNames {
				if !slices.Contains(p.ConnectionEstablishedOn, sn) {
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

	totalPeers := len(rvrPeers)
	connectedPeers := fullyConnected + partiallyConnected

	// Special case: not a datamesh member but has connections.
	if !isMember && connectedPeers > 0 {
		changed = applyRVRFullyConnectedCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedReasonPartiallyConnected,
			fmt.Sprintf("Connected to %d peers, but should not be connected to any (not a datamesh member)", connectedPeers)) || changed
		return ef.Ok().ReportChangedIf(changed)
	}

	switch {
	case fullyConnected == totalPeers:
		// All peers fully connected on all paths.
		changed = applyRVRFullyConnectedCondTrue(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedReasonFullyConnected,
			"Fully connected to all peers on all paths") || changed
	case notConnected == totalPeers:
		// Not connected to any peer.
		changed = applyRVRFullyConnectedCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedReasonNotConnected,
			"Not connected to any peer") || changed
	case notConnected == 0:
		// All peers connected but not all paths established.
		changed = applyRVRFullyConnectedCondTrue(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedReasonConnectedToAllPeers,
			fmt.Sprintf("Connected to all %d peers, but not all paths are established", totalPeers)) || changed
	default:
		// Partially connected.
		changed = applyRVRFullyConnectedCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedReasonPartiallyConnected,
			fmt.Sprintf("Connected to %d of %d peers (%d fully, %d partially, %d not connected)",
				connectedPeers, totalPeers, fullyConnected, partiallyConnected, notConnected)) || changed
	}

	return ef.Ok().ReportChangedIf(changed)
}

// ensureBackingVolumeStatus ensures the RVR backing volume-related status fields reflect the current DRBDR state.
func ensureBackingVolumeStatus(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	drbdr *v1alpha1.DRBDResource,
	datameshMember *v1alpha1.ReplicatedVolumeDatameshMember,
	agentReady bool,
	drbdrConfigurationPending bool,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "disk-status")
	defer ef.OnEnd(&outcome)

	changed := false

	// Determine if condition is relevant.
	// Condition is relevant only for Diskful replicas that are members of the datamesh.
	intendedEffectiveType := computeIntendedEffectiveType(rvr, datameshMember)
	conditionRelevant := datameshMember != nil && intendedEffectiveType == v1alpha1.ReplicaTypeDiskful

	// Guard: no DRBDR or condition not relevant — remove it.
	if drbdr == nil || !conditionRelevant {
		changed = applyRVRBackingVolumeInSyncCondAbsent(rvr) || changed
		changed = applyRVRBackingVolumeState(rvr, "") || changed
		return ef.Ok().ReportChangedIf(changed)
	}

	// Guard: agent not ready.
	if !agentReady {
		changed = applyRVRBackingVolumeInSyncCondUnknown(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeInSyncReasonAgentNotReady,
			"Agent is not ready") || changed
		changed = applyRVRBackingVolumeState(rvr, "") || changed
		return ef.Ok().ReportChangedIf(changed)
	}

	// Guard: configuration is being applied.
	if drbdrConfigurationPending {
		changed = applyRVRBackingVolumeInSyncCondUnknown(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeInSyncReasonApplyingConfiguration,
			"Configuration is being applied") || changed
		changed = applyRVRBackingVolumeState(rvr, "") || changed
		return ef.Ok().ReportChangedIf(changed)
	}

	// Normal path: evaluate disk state.
	diskState := drbdr.Status.DiskState
	changed = applyRVRBackingVolumeState(rvr, diskState) || changed

	// Compute peer and attachment state for detailed messages.
	weAttached := drbdr.Status.ActiveConfiguration != nil && drbdr.Status.ActiveConfiguration.Role == v1alpha1.DRBDRolePrimary
	hasUpToDatePeer := computeHasUpToDatePeer(rvr.Status.Peers)
	hasConnectedAttachedPeer := computeHasConnectedAttachedPeer(rvr.Status.Peers)
	hasAnyAttachedPeer := computeHasAnyAttachedPeer(rvr.Status.Peers)
	ioAvailable := weAttached || hasConnectedAttachedPeer || !hasAnyAttachedPeer

	switch diskState {
	case v1alpha1.DiskStateUpToDate:
		if weAttached {
			changed = applyRVRBackingVolumeInSyncCondTrue(rvr,
				v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeInSyncReasonInSync,
				"Disk is fully up-to-date; application I/O served locally") || changed
		} else {
			changed = applyRVRBackingVolumeInSyncCondTrue(rvr,
				v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeInSyncReasonInSync,
				"Disk is fully up-to-date") || changed
		}

	case v1alpha1.DiskStateDiskless:
		if weAttached {
			changed = applyRVRBackingVolumeInSyncCondFalse(rvr,
				v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeInSyncReasonNoDisk,
				"No local disk; application I/O forwarded to peers") || changed
		} else {
			changed = applyRVRBackingVolumeInSyncCondFalse(rvr,
				v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeInSyncReasonNoDisk,
				"No local disk attached") || changed
		}

	case v1alpha1.DiskStateAttaching:
		if weAttached {
			changed = applyRVRBackingVolumeInSyncCondFalse(rvr,
				v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeInSyncReasonAttaching,
				"Disk is being attached; reading metadata from local device; application I/O forwarded to peers") || changed
		} else {
			changed = applyRVRBackingVolumeInSyncCondFalse(rvr,
				v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeInSyncReasonAttaching,
				"Disk is being attached; reading metadata from local device") || changed
		}

	case v1alpha1.DiskStateDetaching:
		if weAttached {
			changed = applyRVRBackingVolumeInSyncCondFalse(rvr,
				v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeInSyncReasonDetaching,
				"Disk is being detached; application I/O transitioning to peers") || changed
		} else {
			changed = applyRVRBackingVolumeInSyncCondFalse(rvr,
				v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeInSyncReasonDetaching,
				"Disk is being detached") || changed
		}

	case v1alpha1.DiskStateFailed:
		if weAttached {
			changed = applyRVRBackingVolumeInSyncCondFalse(rvr,
				v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeInSyncReasonDiskFailed,
				"Disk failed due to I/O errors; application I/O transitioning to peers") || changed
		} else {
			changed = applyRVRBackingVolumeInSyncCondFalse(rvr,
				v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeInSyncReasonDiskFailed,
				"Disk failed due to I/O errors") || changed
		}

	case v1alpha1.DiskStateInconsistent, v1alpha1.DiskStateOutdated,
		v1alpha1.DiskStateNegotiating, v1alpha1.DiskStateConsistent:
		// States requiring synchronization — check peer availability.
		switch {
		case !hasUpToDatePeer:
			changed = applyRVRBackingVolumeInSyncCondFalse(rvr,
				v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeInSyncReasonSynchronizationBlocked,
				"Synchronization blocked: no peer with up-to-date data available") || changed
		case !ioAvailable:
			changed = applyRVRBackingVolumeInSyncCondFalse(rvr,
				v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeInSyncReasonSynchronizationBlocked,
				"Synchronization blocked: awaiting connection to attached peer") || changed
		default:
			// Synchronization can proceed — provide detailed message.
			changed = applyBackingVolumeSyncMessage(rvr, diskState, weAttached) || changed
		}

	default:
		changed = applyRVRBackingVolumeInSyncCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeInSyncReasonUnknownState,
			fmt.Sprintf("Unknown disk state: %s", diskState)) || changed
	}

	return ef.Ok().ReportChangedIf(changed)
}

// applyBackingVolumeSyncMessage sets the BackingVolumeInSync condition with detailed sync message.
func applyBackingVolumeSyncMessage(rvr *v1alpha1.ReplicatedVolumeReplica, diskState v1alpha1.DiskState, weAttached bool) bool {
	var message string
	switch diskState {
	case v1alpha1.DiskStateInconsistent:
		if weAttached {
			message = "Disk partially synchronized; local reads from synced blocks, others forwarded to peer"
		} else {
			message = "Disk partially synchronized; sync in progress"
		}
	case v1alpha1.DiskStateOutdated:
		if weAttached {
			message = "Disk data outdated; application I/O forwarded to up-to-date peer during resync"
		} else {
			message = "Disk data outdated; resynchronization in progress"
		}
	case v1alpha1.DiskStateNegotiating:
		if weAttached {
			message = "Negotiating sync direction; application I/O forwarded to peers"
		} else {
			message = "Negotiating synchronization direction with peers"
		}
	case v1alpha1.DiskStateConsistent:
		if weAttached {
			message = "Disk consistent, determining currency; application I/O forwarded to peers"
		} else {
			message = "Disk consistent; awaiting peer negotiation to determine if up-to-date"
		}
	default:
		message = fmt.Sprintf("Disk synchronizing (%s)", diskState)
	}
	return applyRVRBackingVolumeInSyncCondFalse(rvr,
		v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeInSyncReasonSynchronizing,
		message)
}

// computeHasUpToDatePeer returns true if any peer has UpToDate disk.
// Note: if peer is not connected, its BackingVolumeState won't be UpToDate.
func computeHasUpToDatePeer(peers []v1alpha1.PeerStatus) bool {
	for i := range peers {
		if peers[i].BackingVolumeState == v1alpha1.DiskStateUpToDate {
			return true
		}
	}
	return false
}

// computeHasConnectedAttachedPeer returns true if any peer is attached and connected.
func computeHasConnectedAttachedPeer(peers []v1alpha1.PeerStatus) bool {
	for i := range peers {
		if peers[i].Attached && len(peers[i].ConnectionEstablishedOn) > 0 {
			return true
		}
	}
	return false
}

// computeHasAnyAttachedPeer returns true if any peer is attached.
func computeHasAnyAttachedPeer(peers []v1alpha1.PeerStatus) bool {
	for i := range peers {
		if peers[i].Attached {
			return true
		}
	}
	return false
}

// ensureQuorumStatus ensures the RVR quorum-related status fields reflect the current DRBDR state.
func ensureQuorumStatus(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	drbdr *v1alpha1.DRBDResource,
	agentReady bool,
	drbdrConfigurationPending bool,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "quorum-status")
	defer ef.OnEnd(&outcome)

	changed := false

	// Guard: RVR is being deleted (drbdr == nil implies deletion).
	if rvrShouldNotExist(rvr) || drbdr == nil {
		changed = applyRVRReadyCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondReadyReasonDeleting,
			"Replica is being deleted") || changed
		changed = applyRVRStatusQuorum(rvr, nil) || changed
		changed = ensureRVRStatusQuorumSummary(rvr, nil) || changed // nil clears optional fields (quorum, quorumMinimumRedundancy)
		return ef.Ok().ReportChangedIf(changed)
	}

	// Guard: agent not ready.
	if !agentReady {
		changed = applyRVRReadyCondUnknown(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondReadyReasonAgentNotReady,
			"Agent is not ready") || changed
		changed = applyRVRStatusQuorum(rvr, nil) || changed
		changed = ensureRVRStatusQuorumSummary(rvr, nil) || changed // nil clears optional fields (quorum, quorumMinimumRedundancy)
		return ef.Ok().ReportChangedIf(changed)
	}

	// Guard: configuration is being applied.
	if drbdrConfigurationPending {
		changed = applyRVRReadyCondUnknown(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondReadyReasonApplyingConfiguration,
			"Configuration is being applied") || changed
		changed = applyRVRStatusQuorum(rvr, nil) || changed
		changed = ensureRVRStatusQuorumSummary(rvr, nil) || changed // nil clears optional fields (quorum, quorumMinimumRedundancy)
		return ef.Ok().ReportChangedIf(changed)
	}

	// Fill quorumSummary from rvr.Status.Peers and drbdr.Status.ActiveConfiguration.
	changed = ensureRVRStatusQuorumSummary(rvr, drbdr) || changed

	// Copy quorum from drbdr.Status.Quorum.
	changed = applyRVRStatusQuorum(rvr, drbdr.Status.Quorum) || changed

	// Build message with quorum info: "quorum: M/N, data quorum: J/K"
	// M = connectedVotingPeers, N = quorum threshold (or "unknown")
	// J = connectedUpToDatePeers, K = quorumMinimumRedundancy (or "unknown")
	msg := buildQuorumMessage(rvr.Status.QuorumSummary)

	// Set Ready condition based on drbdr.Status.Quorum.
	if drbdr.Status.Quorum == nil || !*drbdr.Status.Quorum {
		changed = applyRVRReadyCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondReadyReasonQuorumLost,
			msg) || changed
	} else {
		changed = applyRVRReadyCondTrue(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondReadyReasonReady,
			msg) || changed
	}

	return ef.Ok().ReportChangedIf(changed)
}

// buildQuorumMessage builds a human-readable quorum status message.
func buildQuorumMessage(qs *v1alpha1.QuorumSummary) string {
	if qs == nil {
		return "quorum: unknown"
	}

	quorumStr := "unknown"
	if qs.Quorum != nil {
		quorumStr = strconv.Itoa(*qs.Quorum)
	}

	qmrStr := "unknown"
	if qs.QuorumMinimumRedundancy != nil {
		qmrStr = strconv.Itoa(*qs.QuorumMinimumRedundancy)
	}

	return fmt.Sprintf("quorum: %d/%s, data quorum: %d/%s",
		qs.ConnectedVotingPeers, quorumStr,
		qs.ConnectedUpToDatePeers, qmrStr)
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: metadata (finalizers + labels)
//

// reconcileMetadata reconciles the RVR metadata (finalizers and labels).
//
// Reconcile pattern: Target-state driven
func (r *Reconciler) reconcileMetadata(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	rv *v1alpha1.ReplicatedVolume,
	llvs []snc.LVMLogicalVolume,
	drbdr *v1alpha1.DRBDResource,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "metadata")
	defer rf.OnEnd(&outcome)

	// Compute target finalizer state.
	shouldExist := !rvrShouldNotExist(rvr)
	hasLLVs := len(llvs) > 0
	hasDRBDR := drbdr != nil
	// Keep finalizer if RVR should exist or if there are still child resources.
	targetFinalizerPresent := shouldExist || hasLLVs || hasDRBDR

	// Compute actual LVG name from the LLV referenced by DRBDResource, or first LLV.
	var actualLVGName string
	if drbdr != nil && drbdr.Spec.LVMLogicalVolumeName != "" {
		if llv := findLLVByName(llvs, drbdr.Spec.LVMLogicalVolumeName); llv != nil {
			actualLVGName = llv.Spec.LVMVolumeGroupName
		}
	}
	if actualLVGName == "" && len(llvs) > 0 {
		actualLVGName = llvs[0].Spec.LVMVolumeGroupName
	}

	if isRVRMetadataInSync(rvr, rv, targetFinalizerPresent, actualLVGName) {
		return rf.Continue()
	}

	base := rvr.DeepCopy()
	applyRVRMetadata(rvr, rv, targetFinalizerPresent, actualLVGName)

	if err := r.patchRVR(rf.Ctx(), rvr, base, true); err != nil {
		return rf.Fail(err)
	}

	// If finalizer was removed, we're done (object will be deleted).
	if !targetFinalizerPresent {
		return rf.Done()
	}

	return rf.Continue()
}

// isRVRMetadataInSync checks if the RVR metadata (finalizer + labels) is in sync with the target state.
func isRVRMetadataInSync(rvr *v1alpha1.ReplicatedVolumeReplica, rv *v1alpha1.ReplicatedVolume, targetFinalizerPresent bool, targetLVGName string) bool {
	// Check finalizer.
	actualFinalizerPresent := obju.HasFinalizer(rvr, v1alpha1.RVRControllerFinalizer)
	if targetFinalizerPresent != actualFinalizerPresent {
		return false
	}

	// Check replicated-volume label.
	if rvr.Spec.ReplicatedVolumeName != "" {
		if !obju.HasLabelValue(rvr, v1alpha1.ReplicatedVolumeLabelKey, rvr.Spec.ReplicatedVolumeName) {
			return false
		}
	}

	// Check replicated-storage-class label.
	if rv != nil && rv.Spec.ReplicatedStorageClassName != "" {
		if !obju.HasLabelValue(rvr, v1alpha1.ReplicatedStorageClassLabelKey, rv.Spec.ReplicatedStorageClassName) {
			return false
		}
	}

	// Check lvm-volume-group label.
	if targetLVGName != "" {
		if !obju.HasLabelValue(rvr, v1alpha1.LVMVolumeGroupLabelKey, targetLVGName) {
			return false
		}
	}

	return true
}

// applyRVRMetadata applies the target metadata (finalizer + labels) to the RVR.
// Returns true if any metadata was changed.
func applyRVRMetadata(rvr *v1alpha1.ReplicatedVolumeReplica, rv *v1alpha1.ReplicatedVolume, targetFinalizerPresent bool, targetLVGName string) (changed bool) {
	// Apply finalizer.
	if targetFinalizerPresent {
		changed = obju.AddFinalizer(rvr, v1alpha1.RVRControllerFinalizer) || changed
	} else {
		changed = obju.RemoveFinalizer(rvr, v1alpha1.RVRControllerFinalizer) || changed
	}

	// Apply replicated-volume label.
	if rvr.Spec.ReplicatedVolumeName != "" {
		changed = obju.SetLabel(rvr, v1alpha1.ReplicatedVolumeLabelKey, rvr.Spec.ReplicatedVolumeName) || changed
	}

	// Apply replicated-storage-class label.
	// Note: node-name label is managed by rvr_scheduling_controller.
	if rv != nil && rv.Spec.ReplicatedStorageClassName != "" {
		changed = obju.SetLabel(rvr, v1alpha1.ReplicatedStorageClassLabelKey, rv.Spec.ReplicatedStorageClassName) || changed
	}

	// Apply lvm-volume-group label.
	if targetLVGName != "" {
		changed = obju.SetLabel(rvr, v1alpha1.LVMVolumeGroupLabelKey, targetLVGName) || changed
	}

	return changed
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: backing-volume
//

// Reconcile pattern: In-place reconciliation
func (r *Reconciler) reconcileBackingVolume(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	llvs *[]snc.LVMLogicalVolume,
	rv *v1alpha1.ReplicatedVolume,
	drbdr *v1alpha1.DRBDResource,
) (targetLLVName string, outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "backing-volume")
	defer rf.OnEnd(&outcome)

	// 1. Deletion branch: if RVR should not exist, delete all LLVs.
	if rvrShouldNotExist(rvr) {
		if len(*llvs) > 0 {
			deletingNames, ro := r.reconcileLLVsDeletion(rf.Ctx(), llvs, nil)
			if ro.ShouldReturn() {
				return "", ro
			}
			// rvr can be nil here if it was already deleted; nothing to update in that case.
			if rvr == nil {
				return "", rf.Continue()
			}
			// Still deleting — set condition False.
			changed := applyRVRBackingVolumeReadyCondFalse(rvr,
				v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonNotApplicable,
				fmt.Sprintf("Replica is being deleted; deleting backing volumes: %s", strings.Join(deletingNames, ", ")))
			changed = applyRVRBackingVolumeSize(rvr, resource.Quantity{}) || changed
			return "", rf.Continue().ReportChangedIf(changed)
		}

		// All LLVs deleted.
		// If rvr is nil (already deleted), just return.
		if rvr == nil {
			return "", rf.Continue()
		}
		// Remove condition entirely.
		changed := applyRVRBackingVolumeReadyCondAbsent(rvr)
		changed = applyRVRBackingVolumeSize(rvr, resource.Quantity{}) || changed
		return "", rf.Continue().ReportChangedIf(changed)
	}

	// 2. Compute actual state.
	actual := computeActualBackingVolume(drbdr, *llvs)

	// 3. ReplicatedVolume not found — stop reconciliation and wait for it to appear.
	// Without RV we cannot determine datamesh state.
	if rv == nil {
		changed := applyRVRBackingVolumeReadyCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonWaitingForReplicatedVolume,
			"ReplicatedVolume not found")
		return actual.LLVNameOrEmpty(), rf.Continue().ReportChangedIf(changed)
	}

	// 4. Datamesh not initialized yet — wait for RV controller to set it up.
	// Normally datamesh is already initialized by the time RVR is created,
	// but we check for non-standard usage scenarios (e.g., RVR created before RV) and general correctness.
	if rv.Status.DatameshRevision == 0 {
		changed := applyRVRBackingVolumeReadyCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonWaitingForReplicatedVolume,
			"Datamesh is not initialized yet")
		return actual.LLVNameOrEmpty(), rf.Continue().ReportChangedIf(changed)
	}

	// 5. Compute intended state.
	intended, reason, message := computeIntendedBackingVolume(rvr, rv, actual)

	// 6. If intended == nil, delete LLVs, clear size and remove condition when done.
	if intended == nil {
		if len(*llvs) > 0 {
			deletingNames, ro := r.reconcileLLVsDeletion(rf.Ctx(), llvs, nil)
			if ro.ShouldReturn() {
				return "", ro
			}
			// Still deleting — set condition False.
			changed := applyRVRBackingVolumeReadyCondFalse(rvr, reason,
				fmt.Sprintf("%s; deleting backing volumes: %s", message, strings.Join(deletingNames, ", ")))
			changed = applyRVRBackingVolumeSize(rvr, resource.Quantity{}) || changed
			return "", rf.Continue().ReportChangedIf(changed)
		}

		// All LLVs deleted — remove condition entirely.
		changed := applyRVRBackingVolumeReadyCondAbsent(rvr)
		changed = applyRVRBackingVolumeSize(rvr, resource.Quantity{}) || changed
		return "", rf.Continue().ReportChangedIf(changed)
	}

	// 7. Find the intended LLV in the list.
	intendedLLV := findLLVByName(*llvs, intended.LLVName)

	// 8. Create if missing.
	if intendedLLV == nil {
		llv, err := newLLV(r.scheme, rvr, rv, intended)
		if err != nil {
			return "", rf.Failf(err, "constructing LLV %s", intended.LLVName)
		}

		if err := r.createLLV(rf.Ctx(), llv); err != nil {
			// Handle validation errors specially: log, set condition and requeue.
			// LVMLogicalVolume is not our API, so we treat validation errors as recoverable
			// for safety reasons (e.g., schema changes in sds-node-configurator).
			if apierrors.IsInvalid(err) {
				rf.Log().Error(err, "Failed to create backing volume", "llvName", intended.LLVName)
				applyRVRBackingVolumeReadyCondFalse(rvr,
					v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonProvisioningFailed,
					fmt.Sprintf("Failed to create backing volume %s: %s", intended.LLVName, computeAPIValidationErrorCauses(err)))
				return actual.LLVNameOrEmpty(), rf.ContinueAndRequeueAfter(5 * time.Minute).ReportChanged()
			}
			return "", rf.Failf(err, "creating LLV %s", intended.LLVName)
		}

		// Add newly created LLV to the slice for further processing.
		*llvs = append(*llvs, *llv)

		// Set condition: Provisioning or Reprovisioning.
		var reason, message string
		if actual != nil {
			reason = v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonReprovisioning
			message = fmt.Sprintf("Creating new backing volume %s to replace %s", intended.LLVName, actual.LLVName)
		} else {
			reason = v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonProvisioning
			message = fmt.Sprintf("Creating backing volume %s", intended.LLVName)
		}
		changed := applyRVRBackingVolumeReadyCondFalse(rvr, reason, message)

		// Return actual LLV name if exists, otherwise empty.
		return actual.LLVNameOrEmpty(), rf.Continue().ReportChangedIf(changed)
	}

	// 9. Ensure metadata (ownerRef, finalizer, label) on the intended LLV.
	// Note: In rare cases this results in two LLV patches per reconcile (metadata + resize).
	// This only happens during migration or manual interventions, so it's acceptable.
	if !isLLVMetadataInSync(rvr, rv, intendedLLV) {
		base := intendedLLV.DeepCopy()
		if _, err := applyLLVMetadata(r.scheme, rvr, rv, intendedLLV); err != nil {
			return "", rf.Failf(err, "applying LLV %s metadata", intendedLLV.Name)
		}
		if err := r.patchLLV(rf.Ctx(), intendedLLV, base, true); err != nil {
			return "", rf.Failf(err, "patching LLV %s metadata", intendedLLV.Name)
		}
	}

	// 10. Check if LLV is ready.
	if !isLLVReady(intendedLLV) {
		var reason, message string
		switch {
		case actual != nil && actual.LLVName == intended.LLVName:
			// LLV exists and is already used by DRBDResource, just not ready yet.
			reason = v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonNotReady
			message = fmt.Sprintf("Waiting for backing volume %s to become ready", intended.LLVName)
		case actual != nil:
			// Creating new LLV to replace existing one.
			reason = v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonReprovisioning
			message = fmt.Sprintf("Waiting for new backing volume %s to become ready (replacing %s)", intended.LLVName, actual.LLVName)
		default:
			// Creating new LLV from scratch.
			reason = v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonProvisioning
			message = fmt.Sprintf("Waiting for backing volume %s to become ready", intended.LLVName)
		}
		changed := applyRVRBackingVolumeReadyCondFalse(rvr, reason, message)
		return actual.LLVNameOrEmpty(), rf.Continue().ReportChangedIf(changed)
	}

	// 11. Resize if needed: LLV is ready, but size may need to grow.
	// Note: Status is guaranteed non-nil here because isLLVReady checks it.
	actualSize := intendedLLV.Status.ActualSize
	if actualSize.Cmp(intended.Size) < 0 {
		// Patch only if Spec.Size differs from intended.
		if intendedLLV.Spec.Size != intended.Size.String() {
			base := intendedLLV.DeepCopy()
			intendedLLV.Spec.Size = intended.Size.String()
			if err := r.patchLLV(rf.Ctx(), intendedLLV, base, true); err != nil {
				// Handle validation errors specially: log, set condition and requeue.
				// LVMLogicalVolume is not our API, so we treat validation errors as recoverable
				// for safety reasons (e.g., schema changes in sds-node-configurator).
				if apierrors.IsInvalid(err) {
					rf.Log().Error(err, "Failed to resize backing volume", "llvName", intended.LLVName)
					applyRVRBackingVolumeReadyCondFalse(rvr,
						v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonResizeFailed,
						fmt.Sprintf("Failed to resize backing volume %s: %s", intended.LLVName, computeAPIValidationErrorCauses(err)))
					return intended.LLVName, rf.ContinueAndRequeueAfter(5 * time.Minute).ReportChanged()
				}
				return "", rf.Failf(err, "patching LLV %s size", intendedLLV.Name)
			}
		}

		changed := applyRVRBackingVolumeReadyCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonResizing,
			fmt.Sprintf("Resizing backing volume %s from %s to %s", intended.LLVName, actualSize.String(), intended.Size.String()))

		return actual.LLVNameOrEmpty(), rf.Continue().ReportChangedIf(changed)
	}

	// 12. Fully ready: delete obsolete LLVs and set condition to Ready.
	// Keep actual LLV if it's different from intended (migration in progress).
	// But at this point intended is ready, so we can delete actual too.
	message = fmt.Sprintf("Backing volume %s is ready", intended.LLVName)
	if len(*llvs) > 0 {
		var ro flow.ReconcileOutcome
		deletingNames, ro := r.reconcileLLVsDeletion(rf.Ctx(), llvs, []string{intended.LLVName})
		if ro.ShouldReturn() {
			return "", ro
		}
		message = fmt.Sprintf("%s; deleting obsolete: %s", message, strings.Join(deletingNames, ", "))
	}
	changed := applyRVRBackingVolumeReadyCondTrue(rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonReady, message)
	changed = applyRVRBackingVolumeSize(rvr, intendedLLV.Status.ActualSize) || changed

	return intended.LLVName, rf.Continue().ReportChangedIf(changed)
}

// rvrShouldNotExist returns true if RV is absent or RV is being deleted
// with only our finalizer remaining.
func rvrShouldNotExist(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
	if rvr == nil {
		return true
	}
	if rvr.DeletionTimestamp == nil {
		return false
	}
	// RV is being deleted; check if only our finalizer remains.
	return !obju.HasFinalizersOtherThan(rvr, v1alpha1.RVRControllerFinalizer)
}

// backingVolume represents the intended state of a backing volume for RVR.
// nil means no backing volume is needed (or configuration is incomplete).
type backingVolume struct {
	// LLVName is the intended name of the LVMLogicalVolume.
	LLVName string
	// LVMVolumeGroupName is the LVMVolumeGroup resource name.
	LVMVolumeGroupName string
	// ThinPoolName is the thin pool name (empty for thick volumes).
	ThinPoolName string
	// Size is the intended size of the backing volume.
	Size resource.Quantity
}

// LLVNameOrEmpty returns the LLV name or empty string if bv is nil.
func (bv *backingVolume) LLVNameOrEmpty() string {
	if bv == nil {
		return ""
	}
	return bv.LLVName
}

// computeActualBackingVolume extracts the actual backing volume state from DRBDResource and LLVs.
// Returns nil if DRBDResource is nil, has no LVMLogicalVolumeName, or the referenced LLV is not found.
func computeActualBackingVolume(drbdr *v1alpha1.DRBDResource, llvs []snc.LVMLogicalVolume) *backingVolume {
	if drbdr == nil {
		return nil
	}
	if drbdr.Spec.LVMLogicalVolumeName == "" {
		return nil
	}

	llvName := drbdr.Spec.LVMLogicalVolumeName

	llv := findLLVByName(llvs, llvName)
	if llv == nil {
		return nil
	}

	bv := &backingVolume{
		LLVName:            llvName,
		LVMVolumeGroupName: llv.Spec.LVMVolumeGroupName,
	}

	// Extract thin pool name if thin volume.
	if llv.Spec.Thin != nil {
		bv.ThinPoolName = llv.Spec.Thin.PoolName
	}

	// Get actual size from status.
	if llv.Status != nil {
		bv.Size = llv.Status.ActualSize
	}

	return bv
}

// computeIntendedBackingVolume computes the intended backing volume state.
//
// Returns (intended, reason, message):
//   - intended != nil: backing volume is needed, reason and message are empty.
//   - intended == nil: backing volume is not applicable, reason and message explain why.
//
// Algorithm:
// 1. Check if backing volume is needed:
//   - If member in datamesh: type == Diskful AND typeTransition != ToDiskless
//   - If NOT member: type == Diskful AND deletionTimestamp == nil
//
// 2. Get configuration from:
//   - If member in datamesh: use datamesh member's nodeName, lvmVolumeGroupName, lvmVolumeGroupThinPoolName
//   - If NOT member: use RVR spec's nodeName, lvmVolumeGroupName, lvmVolumeGroupThinPoolName
//
// 3. LLV name:
//   - If actual LLV exists on the same LVG/ThinPool: reuse actual.LLVName (migration support)
//   - Otherwise: generate new name as rvrName + "-" + fnv128(lvgName + thinPoolName)
func computeIntendedBackingVolume(rvr *v1alpha1.ReplicatedVolumeReplica, rv *v1alpha1.ReplicatedVolume, actual *backingVolume) (intended *backingVolume, reason, message string) {
	if rvr == nil {
		panic("computeIntendedBackingVolume: rvr is nil")
	}
	if rv == nil {
		panic("computeIntendedBackingVolume: rv is nil")
	}

	// Find datamesh member.
	member := rv.Status.Datamesh.FindMemberByName(rvr.Name)

	// Check if backing volume is needed.
	bv := &backingVolume{}
	if member != nil {
		// DRBD disk addition/removal order for member:
		// - Adding disk: first configure all peers (transition from intentional diskless to diskless
		//   with bitmap), then add the local disk.
		// - Removing disk: first remove the local disk, then reconfigure peers.
		//
		// Therefore member in datamesh:
		// - Needs backing volume if:
		//   - Type is Diskful, OR
		//   - TypeTransition is ToDiskful (preparing disk before becoming Diskful)
		// - Does NOT need backing volume if:
		//   - TypeTransition is ToDiskless (removing disk)
		//   - Type is diskless (Access/TieBreaker) and no transition to Diskful

		// Check if backing volume is needed.
		isDiskful := member.Type == v1alpha1.ReplicaTypeDiskful
		transitioningToDiskful := member.TypeTransition == v1alpha1.ReplicatedVolumeDatameshMemberTypeTransitionToDiskful
		transitioningToDiskless := member.TypeTransition == v1alpha1.ReplicatedVolumeDatameshMemberTypeTransitionToDiskless
		if !isDiskful && !transitioningToDiskful {
			return nil, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonNotApplicable,
				"Backing volume is not applicable for diskless replica type"
		}
		if transitioningToDiskless {
			return nil, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonNotApplicable,
				"Backing volume is not applicable (transition to diskless)"
		}

		// Check if configuration is complete (lvgName are required, nodeName is always set for member).
		if member.LVMVolumeGroupName == "" {
			return nil, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonPendingScheduling,
				"Waiting for storage assignment"
		}

		// Use datamesh member configuration.
		bv.LVMVolumeGroupName = member.LVMVolumeGroupName
		bv.ThinPoolName = member.LVMVolumeGroupThinPoolName
	} else {
		// Not a member: needs backing volume if type is Diskful.
		if rvr.Spec.Type != v1alpha1.ReplicaTypeDiskful {
			return nil, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonNotApplicable,
				"Backing volume is not applicable for diskless replica type"
		}

		// Check if configuration is complete (nodeName and lvgName are required).
		if rvr.Spec.NodeName == "" {
			return nil, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonPendingScheduling,
				"Waiting for node assignment"
		}
		if rvr.Spec.LVMVolumeGroupName == "" {
			return nil, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonPendingScheduling,
				"Waiting for storage assignment"
		}

		// Use RVR spec configuration.
		bv.LVMVolumeGroupName = rvr.Spec.LVMVolumeGroupName
		bv.ThinPoolName = rvr.Spec.LVMVolumeGroupThinPoolName
	}

	// Use the larger of RV spec size and datamesh size.
	// During resize, datamesh will lag behind spec until all backing volumes are ready.
	// We could just use spec.size, but we take the max for correctness in case of non-standard interventions.
	size := rv.Status.Datamesh.Size
	if rv.Spec.Size.Cmp(size) > 0 {
		size = rv.Spec.Size
	}
	bv.Size = drbd_size.LowerVolumeSize(size)

	// Compute LLV name.
	// For migration: if actual LLV exists on the same LVG/ThinPool, reuse its name
	// (old naming scheme). Otherwise, generate a new deterministic name.
	if actual != nil &&
		actual.LVMVolumeGroupName == bv.LVMVolumeGroupName &&
		actual.ThinPoolName == bv.ThinPoolName {
		bv.LLVName = actual.LLVName
	} else {
		bv.LLVName = computeLLVName(rvr.Name, bv.LVMVolumeGroupName, bv.ThinPoolName)
	}

	return bv, "", ""
}

// computeLLVName computes the LVMLogicalVolume name for a given RVR.
// Format: rvrName + "-" + fnv128(lvgName + thinPoolName)
func computeLLVName(rvrName, lvgName, thinPoolName string) string {
	h := fnv.New128a()
	h.Write([]byte(lvgName))
	h.Write([]byte{0}) // separator
	h.Write([]byte(thinPoolName))
	checksum := hex.EncodeToString(h.Sum(nil))
	return rvrName + "-" + checksum
}

// newLLV constructs a new LVMLogicalVolume with ownerRef, finalizer, and labels.
func newLLV(scheme *runtime.Scheme, rvr *v1alpha1.ReplicatedVolumeReplica, rv *v1alpha1.ReplicatedVolume, bv *backingVolume) (*snc.LVMLogicalVolume, error) {
	llv := &snc.LVMLogicalVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name:       bv.LLVName,
			Labels:     map[string]string{},
			Finalizers: []string{v1alpha1.RVRControllerFinalizer},
		},
		Spec: snc.LVMLogicalVolumeSpec{
			ActualLVNameOnTheNode: bv.LLVName,
			LVMVolumeGroupName:    bv.LVMVolumeGroupName,
			Size:                  bv.Size.String(),
		},
	}

	// Add replicated-volume label.
	if rvr.Spec.ReplicatedVolumeName != "" {
		llv.Labels[v1alpha1.ReplicatedVolumeLabelKey] = rvr.Spec.ReplicatedVolumeName
	}

	// Add replicated-storage-class label.
	if rv != nil && rv.Spec.ReplicatedStorageClassName != "" {
		llv.Labels[v1alpha1.ReplicatedStorageClassLabelKey] = rv.Spec.ReplicatedStorageClassName
	}

	if bv.ThinPoolName == "" {
		llv.Spec.Type = "Thick"
	} else {
		llv.Spec.Type = "Thin"
		llv.Spec.Thin = &snc.LVMLogicalVolumeThinSpec{
			PoolName: bv.ThinPoolName,
		}
	}

	if _, err := obju.SetControllerRef(llv, rvr, scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference: %w", err)
	}

	return llv, nil
}

// isLLVReady checks if LLV is ready (Status != nil and Phase == "Created").
func isLLVReady(llv *snc.LVMLogicalVolume) bool {
	return llv != nil && llv.Status != nil && llv.Status.Phase == "Created"
}

// findLLVByName finds LLV in slice by name.
func findLLVByName(llvs []snc.LVMLogicalVolume, name string) *snc.LVMLogicalVolume {
	for i := range llvs {
		if llvs[i].Name == name {
			return &llvs[i]
		}
	}
	return nil
}

// computeAPIValidationErrorCauses extracts and formats causes from a Kubernetes validation error.
// Returns a human-readable string like "spec.size: Invalid value, spec.name: Required value".
func computeAPIValidationErrorCauses(err error) string {
	statusErr, ok := err.(*apierrors.StatusError)
	if !ok || statusErr.ErrStatus.Details == nil || len(statusErr.ErrStatus.Details.Causes) == 0 {
		return err.Error()
	}

	causes := statusErr.ErrStatus.Details.Causes
	parts := make([]string, 0, len(causes))
	for _, c := range causes {
		switch {
		case c.Field != "" && c.Message != "":
			parts = append(parts, fmt.Sprintf("%s: %s", c.Field, c.Message))
		case c.Message != "":
			parts = append(parts, c.Message)
		case c.Field != "":
			parts = append(parts, c.Field)
		}
	}

	if len(parts) == 0 {
		return err.Error()
	}
	return strings.Join(parts, "; ")
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: llvs-deletion
//

// reconcileLLVsDeletion deletes all LLVs except those in keep list.
// Returns names of LLVs being deleted (for condition messages) and outcome.
//
// Reconcile pattern: In-place reconciliation
func (r *Reconciler) reconcileLLVsDeletion(
	ctx context.Context,
	llvs *[]snc.LVMLogicalVolume,
	keep []string,
) (deletingNames []string, outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "llvs-deletion")
	defer rf.OnEnd(&outcome)

	// Collect names of LLVs to delete.
	deletingNames = make([]string, 0, len(*llvs))
	for i := range *llvs {
		name := (*llvs)[i].Name
		if !slices.Contains(keep, name) {
			deletingNames = append(deletingNames, name)
		}
	}

	// Delete LLVs in reverse order (to keep indices valid after slice deletion).
	for i := len(*llvs) - 1; i >= 0; i-- {
		llv := &(*llvs)[i]
		if slices.Contains(keep, llv.Name) {
			continue
		}

		// Remove our finalizer if present.
		if obju.HasFinalizer(llv, v1alpha1.RVRControllerFinalizer) {
			base := llv.DeepCopy()
			obju.RemoveFinalizer(llv, v1alpha1.RVRControllerFinalizer)
			if err := r.patchLLV(rf.Ctx(), llv, base, false); err != nil {
				return deletingNames, rf.Failf(err, "patching LLV %s", llv.Name)
			}
		}

		// Delete the LLV and remove from slice.
		if err := r.deleteLLV(rf.Ctx(), llv); err != nil {
			return deletingNames, rf.Failf(err, "deleting LLV %s", llv.Name)
		}
		*llvs = slices.Delete(*llvs, i, i+1)
	}

	return deletingNames, rf.Continue()
}

// isLLVMetadataInSync checks if ownerRef, finalizer, and labels are set on LLV.
func isLLVMetadataInSync(rvr *v1alpha1.ReplicatedVolumeReplica, rv *v1alpha1.ReplicatedVolume, llv *snc.LVMLogicalVolume) bool {
	if !obju.HasFinalizer(llv, v1alpha1.RVRControllerFinalizer) {
		return false
	}
	if !obju.HasControllerRef(llv, rvr) {
		return false
	}

	// Check replicated-volume label.
	if rvr.Spec.ReplicatedVolumeName != "" {
		if !obju.HasLabelValue(llv, v1alpha1.ReplicatedVolumeLabelKey, rvr.Spec.ReplicatedVolumeName) {
			return false
		}
	}

	// Check replicated-storage-class label.
	if rv != nil && rv.Spec.ReplicatedStorageClassName != "" {
		if !obju.HasLabelValue(llv, v1alpha1.ReplicatedStorageClassLabelKey, rv.Spec.ReplicatedStorageClassName) {
			return false
		}
	}

	return true
}

// applyLLVMetadata applies ownerRef, finalizer, and labels to LLV.
// Returns true if any changes were made.
func applyLLVMetadata(scheme *runtime.Scheme, rvr *v1alpha1.ReplicatedVolumeReplica, rv *v1alpha1.ReplicatedVolume, llv *snc.LVMLogicalVolume) (bool, error) {
	changed := false

	// Ensure finalizer.
	if obju.AddFinalizer(llv, v1alpha1.RVRControllerFinalizer) {
		changed = true
	}

	// Ensure replicated-volume label.
	if rvr.Spec.ReplicatedVolumeName != "" {
		if obju.SetLabel(llv, v1alpha1.ReplicatedVolumeLabelKey, rvr.Spec.ReplicatedVolumeName) {
			changed = true
		}
	}

	// Ensure replicated-storage-class label.
	if rv != nil && rv.Spec.ReplicatedStorageClassName != "" {
		if obju.SetLabel(llv, v1alpha1.ReplicatedStorageClassLabelKey, rv.Spec.ReplicatedStorageClassName) {
			changed = true
		}
	}

	// Ensure ownerRef.
	if ownerRefChanged, err := obju.SetControllerRef(llv, rvr, scheme); err != nil {
		return changed, fmt.Errorf("setting controller reference: %w", err)
	} else if ownerRefChanged {
		changed = true
	}

	return changed, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: drbd-resource
//

// Reconcile pattern: In-place reconciliation
func (r *Reconciler) reconcileDRBDResource(ctx context.Context, rvr *v1alpha1.ReplicatedVolumeReplica, rv *v1alpha1.ReplicatedVolume, drbdr *v1alpha1.DRBDResource, targetLLVName string) (_ *v1alpha1.DRBDResource, outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "drbd-resource")
	defer rf.OnEnd(&outcome)

	// 1. Deletion branch: if RVR should not exist, remove finalizer from DRBDR and delete it.
	if rvrShouldNotExist(rvr) {
		if drbdr != nil {
			// Remove our finalizer if present.
			if obju.HasFinalizer(drbdr, v1alpha1.RVRControllerFinalizer) {
				base := drbdr.DeepCopy()
				obju.RemoveFinalizer(drbdr, v1alpha1.RVRControllerFinalizer)
				if err := r.patchDRBDR(rf.Ctx(), drbdr, base, false); err != nil {
					return drbdr, rf.Failf(err, "patching DRBDResource %s", drbdr.Name)
				}
			}

			// Delete the DRBDResource.
			if err := r.deleteDRBDR(rf.Ctx(), drbdr); err != nil {
				return drbdr, rf.Failf(err, "deleting DRBDResource %s", drbdr.Name)
			}
		}

		// Set Configured condition on RVR.
		changed := false
		if rvr != nil {
			var message string
			if drbdr == nil {
				message = "Replica is being deleted; DRBD resource has been deleted"
			} else {
				message = fmt.Sprintf("Replica is being deleted; waiting for DRBD resource %s to be deleted", drbdr.Name)
			}
			changed = applyRVRConfiguredCondFalse(rvr,
				v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonNotApplicable, message)
		}

		// DRBDR may still exist in the API (e.g., blocked by finalizers),
		// but for reconciliation purposes we consider it deleted.
		return nil, rf.Continue().ReportChangedIf(changed)
	}

	// 2. Node not assigned yet — cannot configure DRBD without a node.
	// Note: Per API validation, NodeName is immutable once set. We rely on this invariant below.
	if rvr.Spec.NodeName == "" {
		changed := applyRVRConfiguredCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonPendingScheduling,
			"Waiting for node assignment")
		return drbdr, rf.Continue().ReportChangedIf(changed)
	}

	// 3. ReplicatedVolume not found — stop reconciliation and wait for it to appear.
	// Without RV we cannot determine datamesh state, system networks, peers, etc.
	if rv == nil {
		changed := applyRVRConfiguredCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonWaitingForReplicatedVolume,
			"ReplicatedVolume not found")
		return drbdr, rf.Continue().ReportChangedIf(changed)
	}

	// 4. Datamesh not initialized yet — wait for RV controller to set it up.
	// Normally datamesh is already initialized by the time RVR is created,
	// but we check for non-standard usage scenarios (e.g., RVR created before RV) and general correctness.
	if rv.Status.DatameshRevision == 0 {
		changed := applyRVRConfiguredCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonWaitingForReplicatedVolume,
			"Datamesh is not initialized yet")
		return drbdr, rf.Continue().ReportChangedIf(changed)
	}

	datamesh := &rv.Status.Datamesh
	datameshRevision := rv.Status.DatameshRevision

	// 5. No system networks in datamesh — DRBD needs networks to communicate.
	// If datamesh is initialized, system networks should already be set,
	// but we check for non-standard scenarios and general correctness.
	if len(datamesh.SystemNetworkNames) == 0 {
		changed := applyRVRConfiguredCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonWaitingForReplicatedVolume,
			"No system networks in datamesh")
		return drbdr, rf.Continue().ReportChangedIf(changed)
	}

	member := datamesh.FindMemberByName(rvr.Name)
	intendedEffectiveType := computeIntendedEffectiveType(rvr, member)
	targetEffectiveType := computeTargetEffectiveType(intendedEffectiveType, targetLLVName)

	// 6. Create or update DRBDR.
	if drbdr == nil {
		// Compute target DRBDR spec.
		targetSpec := computeTargetDRBDRSpec(rvr, drbdr, datamesh, member, targetLLVName, targetEffectiveType)

		// Create new DRBDResource.
		newObj, err := newDRBDR(r.scheme, rvr, targetSpec)
		if err != nil {
			return drbdr, rf.Failf(err, "constructing DRBDResource")
		}
		if err := r.createDRBDR(rf.Ctx(), newObj); err != nil {
			return drbdr, rf.Failf(err, "creating DRBDResource")
		}
		drbdr = newObj
	} else {
		// We need to check DRBDR spec if any of the tracked values changed since last reconciliation:
		// - DRBDResource.Generation (DRBDR was modified externally)
		// - DatameshRevision (datamesh configuration changed)
		// - EffectiveType (replica type changed, e.g. diskful -> tiebreaker due to missing disk)
		specMayNeedUpdate := drbdr.Generation != rvr.Status.DRBDResourceGeneration ||
			rvr.Status.DatameshRevision != datameshRevision ||
			rvr.Status.EffectiveType != targetEffectiveType

		if specMayNeedUpdate {
			// Compute target DRBDR spec.
			targetSpec := computeTargetDRBDRSpec(rvr, drbdr, datamesh, member, targetLLVName, targetEffectiveType)

			// Compare and potentially apply changes.
			// DeepEqual handles resource.Quantity and nested structs correctly.
			// Performance: not a hot path, reflection overhead is negligible here.
			if !equality.Semantic.DeepEqual(drbdr.Spec, targetSpec) {
				base := drbdr.DeepCopy()
				drbdr.Spec = targetSpec
				if err := r.patchDRBDR(rf.Ctx(), drbdr, base, true); err != nil {
					return drbdr, rf.Failf(err, "patching DRBDResource")
				}
			}
		}
	}

	// 7. Update rvr.status fields. Further we require optimistic lock for this step.
	changed := applyRVRDatameshRevision(rvr, rv.Status.DatameshRevision)
	changed = applyRVRDRBDResourceGeneration(rvr, drbdr.Generation) || changed
	changed = applyRVREffectiveType(rvr, targetEffectiveType) || changed

	// =====================================================

	// 8. Check if the agent is ready on the node.
	nodeName := rvr.Spec.NodeName
	agentReady, err := r.getAgentReady(rf.Ctx(), nodeName)
	if err != nil {
		return drbdr, rf.Failf(err, "getting agent readiness for %s", nodeName)
	}
	if !agentReady {
		// Check node readiness only when agent is not ready — to provide diagnostic info.
		nodeReady, err := r.getNodeReady(rf.Ctx(), nodeName)
		if err != nil {
			return drbdr, rf.Failf(err, "getting node readiness for %s", nodeName)
		}
		var nodeState string
		if nodeReady {
			nodeState = "Ready"
		} else {
			nodeState = "NotReady"
		}
		msg := fmt.Sprintf("Agent is not ready on node %s (node status: %s)", nodeName, nodeState)
		changed = applyRVRConfiguredCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonAgentNotReady, msg) || changed
		return drbdr, rf.Continue().ReportChangedIf(changed).RequireOptimisticLock()
	}

	// 9. Check DRBDR configuration status.
	state, msg := computeActualDRBDRConfigured(drbdr)
	if state == DRBDRConfiguredStatePending {
		changed = applyRVRConfiguredCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonApplyingConfiguration, msg) || changed
		return drbdr, rf.Continue().ReportChangedIf(changed).RequireOptimisticLock()
	}
	if state == DRBDRConfiguredStateFalse {
		changed = applyRVRConfiguredCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonConfigurationFailed, msg) || changed
		return drbdr, rf.Continue().ReportChangedIf(changed).RequireOptimisticLock()
	}

	// At this point targetEffectiveType must equal intendedEffectiveType.
	// If not, our reconciliation logic has a bug.
	if targetEffectiveType != intendedEffectiveType {
		panic(fmt.Sprintf("targetEffectiveType (%s) != intendedEffectiveType (%s)", targetEffectiveType, intendedEffectiveType))
	}

	// 10. DRBDR is configured — copy addresses to RVR status.
	if len(rvr.Status.Addresses) == 0 {
		changed = applyRVRConfiguredCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonApplyingConfiguration,
			"Waiting for DRBD addresses") || changed
		return drbdr, rf.Continue().ReportChangedIf(changed).RequireOptimisticLock()
	}
	changed = applyRVRAddresses(rvr, drbdr.Status.Addresses) || changed

	// 11. If not a datamesh member — DRBD is preconfigured, waiting for membership.
	if member == nil {
		changed = applyRVRConfiguredCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonPendingDatameshJoin,
			"DRBD preconfigured, waiting for datamesh membership") || changed
		return drbdr, rf.Continue().ReportChangedIf(changed).RequireOptimisticLock()
	}

	// 12. We are a datamesh member — fully configured.
	changed = applyRVRConfiguredCondTrue(rvr,
		v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonConfigured, "Configured") || changed
	return drbdr, rf.Continue().ReportChangedIf(changed).RequireOptimisticLock()
}

// DRBDRConfiguredState represents the configuration state of a DRBDResource.
type DRBDRConfiguredState int

const (
	// DRBDRConfiguredStateTrue means the DRBDResource is fully configured
	// (Configured condition is True and ObservedGeneration matches Generation).
	DRBDRConfiguredStateTrue DRBDRConfiguredState = iota
	// DRBDRConfiguredStateFalse means the DRBDResource configuration failed
	// (Configured condition is False and ObservedGeneration matches Generation).
	DRBDRConfiguredStateFalse
	// DRBDRConfiguredStatePending means the agent hasn't processed the current generation yet
	// (no Configured condition or ObservedGeneration doesn't match Generation).
	DRBDRConfiguredStatePending
)

// computeActualDRBDRConfigured returns the configuration state of a DRBDResource and a message:
// - DRBDRConfiguredStateTrue: configured successfully
// - DRBDRConfiguredStateFalse: configuration failed (message contains error from condition)
// - DRBDRConfiguredStatePending: waiting for agent to process or resource is in maintenance mode
func computeActualDRBDRConfigured(drbdr *v1alpha1.DRBDResource) (DRBDRConfiguredState, string) {
	if drbdr == nil {
		panic("computeActualDRBDRConfigured: drbdr is nil")
	}
	cond := obju.GetStatusCondition(drbdr, v1alpha1.DRBDResourceCondConfiguredType)

	// DRBDResource was just created and hasn't been processed by the agent yet.
	if cond == nil {
		return DRBDRConfiguredStatePending, "Waiting for agent to respond (Configured condition is not set yet)"
	}

	// We just made changes to the DRBDResourceand the agent hasn't processed them yet.
	if cond.ObservedGeneration != drbdr.Generation {
		return DRBDRConfiguredStatePending, fmt.Sprintf(
			"Waiting for agent to respond (generation: %d, observedGeneration: %d)",
			drbdr.Generation, cond.ObservedGeneration)
	}

	// DRBDResource is in maintenance mode.
	if cond.Reason == v1alpha1.DRBDResourceCondConfiguredReasonInMaintenance {
		return DRBDRConfiguredStatePending, "DRBD is in maintenance mode"
	}

	// DRBDResource is successfully configured.
	if cond.Status == metav1.ConditionTrue {
		return DRBDRConfiguredStateTrue, ""
	}

	// DRBDResource configuration failed.
	return DRBDRConfiguredStateFalse, fmt.Sprintf("DRBD configuration failed (reason: %s)", cond.Reason)
}

// computeIntendedEffectiveType returns the intended effective replica type.
// If member is nil, returns rvr.Spec.Type.
// During transitions, uses TieBreaker as intermediate type:
// - ToDiskful but not yet Diskful → TieBreaker (disk is being prepared)
// - Diskful with ToDiskless → TieBreaker (data is being moved away)
func computeIntendedEffectiveType(rvr *v1alpha1.ReplicatedVolumeReplica, member *v1alpha1.ReplicatedVolumeDatameshMember) v1alpha1.ReplicaType {
	if member == nil {
		return rvr.Spec.Type
	}

	// During transitions, use TieBreaker as intermediate type.
	if member.Type == v1alpha1.ReplicaTypeDiskful &&
		member.TypeTransition == v1alpha1.ReplicatedVolumeDatameshMemberTypeTransitionToDiskless {
		return v1alpha1.ReplicaTypeTieBreaker
	}

	return member.Type
}

// computeTargetEffectiveType returns the target effective replica type.
// If intended is Diskful but there's no backing volume, returns TieBreaker.
// Otherwise returns the intended type.
func computeTargetEffectiveType(intendedEffectiveType v1alpha1.ReplicaType, targetLLVName string) v1alpha1.ReplicaType {
	if intendedEffectiveType == v1alpha1.ReplicaTypeDiskful && targetLLVName == "" {
		return v1alpha1.ReplicaTypeTieBreaker
	}
	return intendedEffectiveType
}

// computeTargetDRBDRType converts ReplicaType to DRBDResourceType.
// Diskful → Diskful, Access/TieBreaker → Diskless.
func computeDRBDRType(replicaType v1alpha1.ReplicaType) v1alpha1.DRBDResourceType {
	switch replicaType {
	case v1alpha1.ReplicaTypeDiskful:
		return v1alpha1.DRBDResourceTypeDiskful
	case v1alpha1.ReplicaTypeAccess, v1alpha1.ReplicaTypeTieBreaker:
		return v1alpha1.DRBDResourceTypeDiskless
	default:
		return v1alpha1.DRBDResourceTypeDiskless
	}
}

// newDRBDR constructs a new DRBDResource with ownerRef and finalizer.
// Spec must already have all fields set (including NodeName and NodeID).
func newDRBDR(
	scheme *runtime.Scheme,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	spec v1alpha1.DRBDResourceSpec,
) (*v1alpha1.DRBDResource, error) {
	drbdr := &v1alpha1.DRBDResource{
		ObjectMeta: metav1.ObjectMeta{
			Name:       rvr.Name,
			Finalizers: []string{v1alpha1.RVRControllerFinalizer},
		},
		Spec: spec,
	}
	if err := controllerutil.SetControllerReference(rvr, drbdr, scheme); err != nil {
		return nil, err
	}
	return drbdr, nil
}

// computeTargetDRBDRSpec computes the target DRBDR spec based on rvr, existing drbdr,
// datamesh and member configuration.
// If drbdr exists, starts from a copy of its spec (preserving immutable and user-controlled fields).
// Otherwise creates a new spec with NodeName/NodeID from rvr.
//
// Exception: This helper uses DeepCopy which normally violates ComputeReconcileHelper rules.
// This is intentional: we copy the existing spec to preserve immutable and user-controlled fields
// (NodeName, NodeID, Maintenance) that we don't want to manually track. The DeepCopy overhead
// is acceptable since this is not a hot path.
func computeTargetDRBDRSpec(
	rvr *v1alpha1.ReplicatedVolumeReplica,
	drbdr *v1alpha1.DRBDResource,
	datamesh *v1alpha1.ReplicatedVolumeDatamesh,
	member *v1alpha1.ReplicatedVolumeDatameshMember,
	targetLLVName string,
	targetEffectiveType v1alpha1.ReplicaType,
) v1alpha1.DRBDResourceSpec {
	// Start from existing spec (preserves immutable and user-controlled fields) or create new.
	var spec v1alpha1.DRBDResourceSpec
	if drbdr != nil {
		spec = *drbdr.Spec.DeepCopy()
	} else {
		nodeID, ok := rvr.NodeID()
		if !ok {
			panic("invalid RVR name: cannot extract NodeID from " + rvr.Name)
		}
		spec.NodeName = rvr.Spec.NodeName
		spec.NodeID = nodeID
	}

	// Fill mutable fields.
	spec.Type = computeDRBDRType(targetEffectiveType)
	spec.SystemNetworks = slices.Clone(datamesh.SystemNetworkNames)
	spec.State = v1alpha1.DRBDResourceStateUp

	// LLV name and Size: non-empty only for Diskful.
	if spec.Type == v1alpha1.DRBDResourceTypeDiskful {
		spec.LVMLogicalVolumeName = targetLLVName
		spec.Size = &datamesh.Size
	} else {
		spec.LVMLogicalVolumeName = ""
		spec.Size = nil
	}

	// Membership-dependent configuration.
	if member == nil {
		// Cannot become Primary while not in datamesh.
		spec.Role = v1alpha1.DRBDRoleSecondary
		spec.AllowTwoPrimaries = false

		// Cannot participate in quorum while not in datamesh.
		spec.Quorum = 32
		spec.QuorumMinimumRedundancy = 32

		// Does not connect to any peers while not in datamesh.
		spec.Peers = nil
	} else {
		// Datamesh determines the role for each member and whether multiple primaries are allowed.
		spec.Role = member.Role
		spec.AllowTwoPrimaries = datamesh.AllowTwoPrimaries

		// Quorum: diskless node quorum depends on connection to enough UpToDate diskful nodes that have quorum.
		if spec.Type == v1alpha1.DRBDResourceTypeDiskless {
			spec.Quorum = 32
			spec.QuorumMinimumRedundancy = datamesh.QuorumMinimumRedundancy
		} else {
			spec.Quorum = datamesh.Quorum
			spec.QuorumMinimumRedundancy = datamesh.QuorumMinimumRedundancy
		}

		// Compute peers based on self type.
		spec.Peers = computeTargetDRBDRPeers(datamesh, member)
	}

	return spec
}

// computeTargetDRBDRPeers computes the target peers from datamesh members (excluding self).
//
// Peer connectivity rules:
//   - Diskful replica (or TieBreaker transitioning ToDiskful) connects to ALL peers,
//     but sets AllowRemoteRead=false for Access peers (so they are not considered in the tie-breaker mechanism).
//   - Access/TieBreaker replica connects only to Diskful peers and TieBreaker peers transitioning ToDiskful.
func computeTargetDRBDRPeers(datamesh *v1alpha1.ReplicatedVolumeDatamesh, self *v1alpha1.ReplicatedVolumeDatameshMember) []v1alpha1.DRBDResourcePeer {
	if len(datamesh.Members) <= 1 {
		return nil
	}

	// Diskful or TieBreaker transitioning ToDiskful is treated as Diskful for peer connectivity.
	selfTreatedAsDiskful := self.Type == v1alpha1.ReplicaTypeDiskful ||
		(self.Type == v1alpha1.ReplicaTypeTieBreaker && self.TypeTransition == v1alpha1.ReplicatedVolumeDatameshMemberTypeTransitionToDiskful)

	peers := make([]v1alpha1.DRBDResourcePeer, 0, len(datamesh.Members)-1)
	for i := range datamesh.Members {
		m := &datamesh.Members[i]
		if m.Name == self.Name {
			continue
		}

		// Diskful or TieBreaker transitioning ToDiskful is treated as Diskful for peer connectivity.
		peerTreatedAsDiskful := m.Type == v1alpha1.ReplicaTypeDiskful ||
			(m.Type == v1alpha1.ReplicaTypeTieBreaker && m.TypeTransition == v1alpha1.ReplicatedVolumeDatameshMemberTypeTransitionToDiskful)

		if !selfTreatedAsDiskful && !peerTreatedAsDiskful {
			// Access/TieBreaker: only connect to peers treated as Diskful.
			continue
		}

		// AllowRemoteRead=false for Access peers (so they are not considered in the tie-breaker mechanism).
		peerIsAccess := m.Type == v1alpha1.ReplicaTypeAccess
		allowRemoteRead := !peerIsAccess

		// Extract NodeID from the member name. This should never panic because
		// member name format is validated by API.
		nodeID, ok := m.NodeID()
		if !ok {
			panic("m.NodeID() failed for member " + m.Name)
		}

		// Peer type: if treated as Diskful, use Diskful; otherwise Diskless.
		peerType := v1alpha1.DRBDResourceTypeDiskless
		if peerTreatedAsDiskful {
			peerType = v1alpha1.DRBDResourceTypeDiskful
		}

		peers = append(peers, v1alpha1.DRBDResourcePeer{
			Name:            m.Name,
			Type:            peerType,
			AllowRemoteRead: allowRemoteRead,
			NodeID:          nodeID,
			Protocol:        v1alpha1.DRBDProtocolC,
			SharedSecret:    datamesh.SharedSecret,
			SharedSecretAlg: datamesh.SharedSecretAlg,
			Paths:           buildDRBDRPeerPaths(m.Addresses),
		})
	}
	return peers
}

// buildDRBDRPeerPaths builds paths from member addresses.
func buildDRBDRPeerPaths(addresses []v1alpha1.DRBDResourceAddressStatus) []v1alpha1.DRBDResourcePath {
	paths := make([]v1alpha1.DRBDResourcePath, len(addresses))
	for i := range addresses {
		paths[i] = v1alpha1.DRBDResourcePath{
			SystemNetworkName: addresses[i].SystemNetworkName,
			Address:           addresses[i].Address,
		}
	}
	return paths
}

// applyRVRConfiguredCondFalse sets the Configured condition to False on RVR.
func applyRVRConfiguredCondFalse(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondConfiguredType,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

// applyRVRConfiguredCondTrue sets the Configured condition to True on RVR.
func applyRVRConfiguredCondTrue(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondConfiguredType,
		Status:  metav1.ConditionTrue,
		Reason:  reason,
		Message: message,
	})
}

// applyRVRBackingVolumeReadyCondFalse sets the BackingVolumeReady condition to False on RVR.
func applyRVRBackingVolumeReadyCondFalse(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyType,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

// applyRVRBackingVolumeReadyCondTrue sets the BackingVolumeReady condition to True on RVR.
func applyRVRBackingVolumeReadyCondTrue(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyType,
		Status:  metav1.ConditionTrue,
		Reason:  reason,
		Message: message,
	})
}

// applyRVRBackingVolumeReadyCondAbsent removes the BackingVolumeReady condition from RVR.
func applyRVRBackingVolumeReadyCondAbsent(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
	return obju.RemoveStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyType)
}

// applyRVRAttachedCondFalse sets the Attached condition to False on RVR.
func applyRVRAttachedCondFalse(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondAttachedType,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

// applyRVRAttachedCondUnknown sets the Attached condition to Unknown on RVR.
func applyRVRAttachedCondUnknown(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondAttachedType,
		Status:  metav1.ConditionUnknown,
		Reason:  reason,
		Message: message,
	})
}

// applyRVRAttachedCondTrue sets the Attached condition to True on RVR.
func applyRVRAttachedCondTrue(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondAttachedType,
		Status:  metav1.ConditionTrue,
		Reason:  reason,
		Message: message,
	})
}

// applyRVRAttachedCondAbsent removes the Attached condition from RVR.
func applyRVRAttachedCondAbsent(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
	return obju.RemoveStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondAttachedType)
}

// applyRVRBackingVolumeInSyncCondFalse sets the BackingVolumeInSync condition to False on RVR.
func applyRVRBackingVolumeInSyncCondAbsent(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
	return obju.RemoveStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeInSyncType)
}

func applyRVRBackingVolumeInSyncCondTrue(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeInSyncType,
		Status:  metav1.ConditionTrue,
		Reason:  reason,
		Message: message,
	})
}

func applyRVRBackingVolumeInSyncCondFalse(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeInSyncType,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

// applyRVRBackingVolumeInSyncCondUnknown sets the BackingVolumeInSync condition to Unknown on RVR.
func applyRVRBackingVolumeInSyncCondUnknown(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeInSyncType,
		Status:  metav1.ConditionUnknown,
		Reason:  reason,
		Message: message,
	})
}

// applyRVRFullyConnectedCondFalse sets the FullyConnected condition to False on RVR.
func applyRVRFullyConnectedCondFalse(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedType,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

// applyRVRFullyConnectedCondTrue sets the FullyConnected condition to True on RVR.
func applyRVRFullyConnectedCondTrue(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedType,
		Status:  metav1.ConditionTrue,
		Reason:  reason,
		Message: message,
	})
}

// applyRVRFullyConnectedCondUnknown sets the FullyConnected condition to Unknown on RVR.
func applyRVRFullyConnectedCondUnknown(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedType,
		Status:  metav1.ConditionUnknown,
		Reason:  reason,
		Message: message,
	})
}

// applyRVRFullyConnectedCondAbsent removes the FullyConnected condition from RVR.
func applyRVRFullyConnectedCondAbsent(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
	return obju.RemoveStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedType)
}

// applyRVRReadyCondFalse sets the Ready condition to False on RVR.
func applyRVRReadyCondTrue(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondReadyType,
		Status:  metav1.ConditionTrue,
		Reason:  reason,
		Message: message,
	})
}

func applyRVRReadyCondFalse(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondReadyType,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

// applyRVRReadyCondUnknown sets the Ready condition to Unknown on RVR.
func applyRVRReadyCondUnknown(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondReadyType,
		Status:  metav1.ConditionUnknown,
		Reason:  reason,
		Message: message,
	})
}

// applyRVRDevicePath sets DevicePath on RVR status.
func applyRVRDevicePath(rvr *v1alpha1.ReplicatedVolumeReplica, path string) bool {
	if rvr.Status.DevicePath == path {
		return false
	}
	rvr.Status.DevicePath = path
	return true
}

// applyRVRDeviceIOSuspended sets DeviceIOSuspended on RVR status.
func applyRVRDeviceIOSuspended(rvr *v1alpha1.ReplicatedVolumeReplica, suspended *bool) bool {
	if (rvr.Status.DeviceIOSuspended == nil) == (suspended == nil) {
		if suspended == nil || *rvr.Status.DeviceIOSuspended == *suspended {
			return false
		}
	}
	rvr.Status.DeviceIOSuspended = suspended
	return true
}

// applyRVRBackingVolumeState sets BackingVolumeState on RVR status.
func applyRVRBackingVolumeState(rvr *v1alpha1.ReplicatedVolumeReplica, state v1alpha1.DiskState) bool {
	if rvr.Status.BackingVolumeState == state {
		return false
	}
	rvr.Status.BackingVolumeState = state
	return true
}

// applyRVRStatusQuorum sets Quorum on RVR status.
func applyRVRStatusQuorum(rvr *v1alpha1.ReplicatedVolumeReplica, quorum *bool) bool {
	if (rvr.Status.Quorum == nil) == (quorum == nil) {
		if quorum == nil || *rvr.Status.Quorum == *quorum {
			return false
		}
	}
	rvr.Status.Quorum = quorum
	return true
}

// ensureRVRStatusQuorumSummary computes and sets QuorumSummary on RVR status based on rvr.status.peers and drbdr.status.activeConfiguration.
//
// Exception: This helper intentionally omits a phase scope and returns bool instead of flow.EnsureOutcome
// for simplicity — the computation is straightforward and phase logging would add noise without value.
func ensureRVRStatusQuorumSummary(rvr *v1alpha1.ReplicatedVolumeReplica, drbdr *v1alpha1.DRBDResource) bool {
	// Compute connectedVotingPeers and connectedUpToDatePeers from rvr.Status.Peers.
	var connectedVotingPeers, connectedUpToDatePeers int
	for i := range rvr.Status.Peers {
		p := &rvr.Status.Peers[i]
		if p.ConnectionState != v1alpha1.ConnectionStateConnected {
			continue
		}
		// Voting peers: Diskful or TieBreaker with established connection.
		if p.Type == v1alpha1.ReplicaTypeDiskful || p.Type == v1alpha1.ReplicaTypeTieBreaker {
			connectedVotingPeers++
		}
		// UpToDate peers: connected and UpToDate disk.
		if p.BackingVolumeState == v1alpha1.DiskStateUpToDate {
			connectedUpToDatePeers++
		}
	}

	// Get quorum and quorumMinimumRedundancy from drbdr.status.activeConfiguration.
	var quorum, quorumMinimumRedundancy *int
	if drbdr != nil && drbdr.Status.ActiveConfiguration != nil {
		if drbdr.Status.ActiveConfiguration.Quorum != nil {
			v := int(*drbdr.Status.ActiveConfiguration.Quorum)
			quorum = &v
		}
		if drbdr.Status.ActiveConfiguration.QuorumMinimumRedundancy != nil {
			v := int(*drbdr.Status.ActiveConfiguration.QuorumMinimumRedundancy)
			quorumMinimumRedundancy = &v
		}
	}

	summary := &v1alpha1.QuorumSummary{
		ConnectedVotingPeers:    connectedVotingPeers,
		Quorum:                  quorum,
		ConnectedUpToDatePeers:  connectedUpToDatePeers,
		QuorumMinimumRedundancy: quorumMinimumRedundancy,
	}

	// Compare with current value.
	if rvr.Status.QuorumSummary != nil && *rvr.Status.QuorumSummary == *summary {
		return false
	}
	rvr.Status.QuorumSummary = summary
	return true
}

// applyRVRStatusPeers sets Peers on RVR status.
func applyRVRStatusPeers(rvr *v1alpha1.ReplicatedVolumeReplica, peers []v1alpha1.PeerStatus) bool {
	if len(rvr.Status.Peers) == 0 && len(peers) == 0 {
		return false
	}
	// Use DeepEqual for slice of structs with nested slices.
	if reflect.DeepEqual(rvr.Status.Peers, peers) {
		return false
	}
	rvr.Status.Peers = peers
	return true
}

// ensureRVRStatusPeers performs O(m+n) merge of datamesh.Members and drbdr.Status.Peers
// into rvr.Status.Peers. It reuses the backing array when possible to minimize allocations.
//
// Semantics of empty fields:
//   - Type empty: peer not in datamesh (orphan, being removed)
//   - ConnectionState empty: peer not in drbdr.Status.Peers (pending connection)
//
// Panics if either source is not sorted by Name (invariant).
//
// Exception: This helper intentionally omits a phase scope and returns bool instead of flow.EnsureOutcome
// for simplicity — the computation is straightforward and phase logging would add noise without value.
func ensureRVRStatusPeers(
	rvr *v1alpha1.ReplicatedVolumeReplica,
	drbdrPeers []v1alpha1.DRBDResourcePeerStatus,
	datamesh *v1alpha1.ReplicatedVolumeDatamesh,
	selfName string,
) (changed bool) {
	// Get members from datamesh (nil-safe).
	var members []v1alpha1.ReplicatedVolumeDatameshMember
	if datamesh != nil {
		members = datamesh.Members
	}

	// Assert sorted invariants (panic if violated).
	for i := 1; i < len(members); i++ {
		if members[i-1].Name >= members[i].Name {
			panic("datamesh.Members not sorted by Name")
		}
	}
	for i := 1; i < len(drbdrPeers); i++ {
		if drbdrPeers[i-1].Name >= drbdrPeers[i].Name {
			panic("drbdr.Status.Peers not sorted by Name")
		}
	}

	// O(m+n) merge with lazy capacity growth.
	dst := &rvr.Status.Peers
	i, j, k := 0, 0, 0

	for {
		// Skip self in members.
		for i < len(members) && members[i].Name == selfName {
			i++
		}
		if i >= len(members) && j >= len(drbdrPeers) {
			break
		}

		// Ensure capacity (append grows backing array as needed).
		if k >= len(*dst) {
			*dst = append(*dst, v1alpha1.PeerStatus{})
			changed = true
		}
		p := &(*dst)[k]
		k++

		// Pick next from merge.
		var name string
		var member *v1alpha1.ReplicatedVolumeDatameshMember
		var peer *v1alpha1.DRBDResourcePeerStatus

		switch {
		case i >= len(members):
			name, peer = drbdrPeers[j].Name, &drbdrPeers[j]
			j++
		case j >= len(drbdrPeers):
			name, member = members[i].Name, &members[i]
			i++
		case members[i].Name < drbdrPeers[j].Name:
			name, member = members[i].Name, &members[i]
			i++
		case members[i].Name > drbdrPeers[j].Name:
			name, peer = drbdrPeers[j].Name, &drbdrPeers[j]
			j++
		default:
			name, member, peer = members[i].Name, &members[i], &drbdrPeers[j]
			i++
			j++
		}

		// Update Name.
		if p.Name != name {
			p.Name = name
			changed = true
		}

		// Update Type (from datamesh; empty if orphan).
		var targetType v1alpha1.ReplicaType
		if member != nil {
			targetType = member.Type
		}
		if p.Type != targetType {
			p.Type = targetType
			changed = true
		}

		// Update DRBD-sourced fields (empty if pending connection).
		if peer != nil {
			if targetAttached := peer.Role == v1alpha1.DRBDRolePrimary; p.Attached != targetAttached {
				p.Attached = targetAttached
				changed = true
			}
			connOn := p.ConnectionEstablishedOn[:0]
			for x := range peer.Paths {
				if peer.Paths[x].Established {
					connOn = append(connOn, peer.Paths[x].SystemNetworkName)
				}
			}
			slices.Sort(connOn)
			if !slices.Equal(p.ConnectionEstablishedOn, connOn) {
				p.ConnectionEstablishedOn = connOn
				changed = true
			}
			if p.ConnectionState != peer.ConnectionState {
				p.ConnectionState = peer.ConnectionState
				changed = true
			}
			if p.BackingVolumeState != peer.DiskState {
				p.BackingVolumeState = peer.DiskState
				changed = true
			}
		} else {
			if p.Attached {
				p.Attached = false
				changed = true
			}
			if len(p.ConnectionEstablishedOn) > 0 {
				p.ConnectionEstablishedOn = p.ConnectionEstablishedOn[:0]
				changed = true
			}
			if p.ConnectionState != "" {
				p.ConnectionState = ""
				changed = true
			}
			if p.BackingVolumeState != "" {
				p.BackingVolumeState = ""
				changed = true
			}
		}
	}

	// Trim excess.
	if len(*dst) > k {
		*dst = (*dst)[:k]
		changed = true
	}
	return changed
}

// applyRVRBackingVolumeSize sets BackingVolumeSize on RVR status.
// Pass zero quantity to clear the size.
func applyRVRBackingVolumeSize(rvr *v1alpha1.ReplicatedVolumeReplica, size resource.Quantity) bool {
	if rvr.Status.BackingVolumeSize.Cmp(size) == 0 {
		return false
	}
	rvr.Status.BackingVolumeSize = size
	return true
}

// applyRVREffectiveType sets EffectiveType on RVR status.
func applyRVREffectiveType(rvr *v1alpha1.ReplicatedVolumeReplica, effectiveType v1alpha1.ReplicaType) bool {
	if rvr.Status.EffectiveType == effectiveType {
		return false
	}
	rvr.Status.EffectiveType = effectiveType
	return true
}

// applyRVRDatameshRevision sets DatameshRevision on RVR status.
func applyRVRDatameshRevision(rvr *v1alpha1.ReplicatedVolumeReplica, revision int64) bool {
	if rvr.Status.DatameshRevision == revision {
		return false
	}
	rvr.Status.DatameshRevision = revision
	return true
}

// applyRVRAddresses sets Addresses on RVR status.
func applyRVRAddresses(rvr *v1alpha1.ReplicatedVolumeReplica, addresses []v1alpha1.DRBDResourceAddressStatus) bool {
	if slices.Equal(rvr.Status.Addresses, addresses) {
		return false
	}
	rvr.Status.Addresses = slices.Clone(addresses)
	return true
}

// applyRVRDRBDResourceGeneration sets DRBDResourceGeneration on RVR status.
func applyRVRDRBDResourceGeneration(rvr *v1alpha1.ReplicatedVolumeReplica, gen int64) bool {
	if rvr.Status.DRBDResourceGeneration == gen {
		return false
	}
	rvr.Status.DRBDResourceGeneration = gen
	return true
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: satisfy-eligible-nodes
//

// reconcileSatisfyEligibleNodesCondition reconciles the RVR SatisfyEligibleNodes condition.
//
// Reconcile pattern: In-place reconciliation
func (r *Reconciler) reconcileSatisfyEligibleNodesCondition(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	rv *v1alpha1.ReplicatedVolume,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "satisfy-eligible-nodes")
	defer rf.OnEnd(&outcome)

	changed := false

	// Guard: node not selected — remove condition (not applicable).
	if rvr.Spec.NodeName == "" {
		changed = applySatisfyEligibleNodesCondAbsent(rvr) || changed
		return rf.Continue().ReportChangedIf(changed)
	}

	// Guard: no RV or no Configuration — condition Unknown.
	if rv == nil || rv.Status.Configuration == nil || rv.Status.Configuration.StoragePoolName == "" {
		changed = applySatisfyEligibleNodesCondUnknown(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonPendingConfiguration,
			"Configuration not yet available") || changed
		return rf.Continue().ReportChangedIf(changed)
	}

	// Get eligible node entry from RSP.
	eligibleNode, err := r.getNodeEligibility(rf.Ctx(), rv.Status.Configuration.StoragePoolName, rvr.Spec.NodeName)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// RSP not found — cannot verify eligibility.
			changed = applySatisfyEligibleNodesCondUnknown(rvr,
				v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonPendingConfiguration,
				"ReplicatedStoragePool not found") || changed
			return rf.Continue().ReportChangedIf(changed)
		}
		return rf.Fail(err)
	}

	// Node not in eligible nodes list.
	if eligibleNode == nil {
		changed = applySatisfyEligibleNodesCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonNodeMismatch,
			"Node is not in the eligible nodes list according to ReplicatedStoragePool") || changed
		return rf.Continue().ReportChangedIf(changed)
	}

	// Check LVMVolumeGroup and ThinPool (only for Diskful).
	var lvg *v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup
	if rvr.Spec.Type == v1alpha1.ReplicaTypeDiskful && rvr.Spec.LVMVolumeGroupName != "" {
		// First, check if LVMVolumeGroup exists in eligible node (by name only).
		lvg = findLVGInEligibleNodeByName(eligibleNode, rvr.Spec.LVMVolumeGroupName)
		if lvg == nil {
			changed = applySatisfyEligibleNodesCondFalse(rvr,
				v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonLVMVolumeGroupMismatch,
				"Node is eligible, but LVMVolumeGroup is not in the allowed list for this node according to ReplicatedStoragePool") || changed
			return rf.Continue().ReportChangedIf(changed)
		}

		// Then, check ThinPool if specified.
		if rvr.Spec.LVMVolumeGroupThinPoolName != "" {
			lvg = findLVGInEligibleNode(eligibleNode, rvr.Spec.LVMVolumeGroupName, rvr.Spec.LVMVolumeGroupThinPoolName)
			if lvg == nil {
				changed = applySatisfyEligibleNodesCondFalse(rvr,
					v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonThinPoolMismatch,
					"Node and LVMVolumeGroup are eligible, but ThinPool is not in the allowed list for this LVMVolumeGroup according to ReplicatedStoragePool") || changed
				return rf.Continue().ReportChangedIf(changed)
			}
		}
	}

	// Collect warnings.
	warnings := collectEligibilityWarnings(eligibleNode, lvg)

	// All checks passed — condition True.
	message := "Replica satisfies eligible nodes requirements"
	if warnings != "" {
		message += ", however note that currently " + warnings
	}
	changed = applySatisfyEligibleNodesCondTrue(rvr,
		v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonSatisfied,
		message) || changed

	return rf.Continue().ReportChangedIf(changed)
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

// collectEligibilityWarnings collects warnings about node/LVG readiness.
func collectEligibilityWarnings(node *v1alpha1.ReplicatedStoragePoolEligibleNode, lvg *v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup) string {
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

// ──────────────────────────────────────────────────────────────────────────────
// Single-call I/O helper categories: GetReconcileHelper
//

// getRVR fetches the ReplicatedVolumeReplica by name.
// Returns (nil, nil) if not found.
func (r *Reconciler) getRVR(ctx context.Context, name string) (*v1alpha1.ReplicatedVolumeReplica, error) {
	var rvr v1alpha1.ReplicatedVolumeReplica
	if err := r.cl.Get(ctx, client.ObjectKey{Name: name}, &rvr); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &rvr, nil
}

// getLLVs returns LVMLogicalVolumes owned by the given RVR name.
// Also includes the LLV referenced by DRBDResource.Spec if present, because:
//   - the volume may be in the process of disk replacement (e.g., LVG or thin pool change),
//   - for migration we support the "old" naming scheme, so we also fetch the old
//     LLV name from DRBDResource just in case.
//
// Exception: This helper performs two reads (List + conditional Get) which violates
// the single-read GetReconcileHelper contract. This is intentional: we need to combine
// owner-based lookup with fallback for legacy/migration LLV names in a single atomic operation.
func (r *Reconciler) getLLVs(ctx context.Context, rvrName string, drbdr *v1alpha1.DRBDResource) ([]snc.LVMLogicalVolume, error) {
	var list snc.LVMLogicalVolumeList
	if err := r.cl.List(ctx, &list,
		client.MatchingFields{indexes.IndexFieldLLVByRVROwner: rvrName},
	); err != nil {
		return nil, err
	}

	// If DRBDResource references an LLV, ensure it's in the list.
	if drbdr != nil && drbdr.Spec.LVMLogicalVolumeName != "" {
		llvName := drbdr.Spec.LVMLogicalVolumeName

		// If not already in list, fetch and append.
		if !slices.ContainsFunc(list.Items, func(llv snc.LVMLogicalVolume) bool {
			return llv.Name == llvName
		}) {
			var llv snc.LVMLogicalVolume
			if err := r.cl.Get(ctx, client.ObjectKey{Name: llvName}, &llv); err != nil {
				if !apierrors.IsNotFound(err) {
					return nil, err
				}
				// LLV not found — that's ok, it may have been deleted.
			} else {
				list.Items = append(list.Items, llv)
			}
		}
	}

	return list.Items, nil
}

// getDRBDR fetches the DRBDResource by name.
// Returns (nil, nil) if not found.
func (r *Reconciler) getDRBDR(ctx context.Context, name string) (*v1alpha1.DRBDResource, error) {
	var drbdr v1alpha1.DRBDResource
	if err := r.cl.Get(ctx, client.ObjectKey{Name: name}, &drbdr); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &drbdr, nil
}

// getRV fetches the ReplicatedVolume by name from RVR spec.
// Returns (nil, nil) if not found.
func (r *Reconciler) getRV(ctx context.Context, rvr *v1alpha1.ReplicatedVolumeReplica) (*v1alpha1.ReplicatedVolume, error) {
	if rvr == nil || rvr.Spec.ReplicatedVolumeName == "" {
		return nil, nil
	}

	var rv v1alpha1.ReplicatedVolume
	if err := r.cl.Get(ctx, client.ObjectKey{Name: rvr.Spec.ReplicatedVolumeName}, &rv); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &rv, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Single-call I/O helper categories
//

// --- ReplicatedVolume (RV) ---

func (r *Reconciler) patchRVRStatus(ctx context.Context, obj, base *v1alpha1.ReplicatedVolumeReplica, optimisticLock bool) error {
	var patch client.Patch
	if optimisticLock {
		patch = client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})
	} else {
		patch = client.MergeFrom(base)
	}
	return r.cl.Status().Patch(ctx, obj, patch)
}

// --- ReplicatedVolumeReplica (RVR) ---

func (r *Reconciler) patchRVR(ctx context.Context, obj, base *v1alpha1.ReplicatedVolumeReplica, optimisticLock bool) error {
	var patch client.Patch
	if optimisticLock {
		patch = client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})
	} else {
		patch = client.MergeFrom(base)
	}
	return r.cl.Patch(ctx, obj, patch)
}

// --- LVMLogicalVolume (LLV) ---

func (r *Reconciler) patchLLV(ctx context.Context, obj, base *snc.LVMLogicalVolume, optimisticLock bool) error {
	var patch client.Patch
	if optimisticLock {
		patch = client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})
	} else {
		patch = client.MergeFrom(base)
	}
	return r.cl.Patch(ctx, obj, patch)
}

func (r *Reconciler) deleteLLV(ctx context.Context, llv *snc.LVMLogicalVolume) error {
	if llv.DeletionTimestamp != nil {
		return nil
	}
	return client.IgnoreNotFound(r.cl.Delete(ctx, llv))
}

func (r *Reconciler) createLLV(ctx context.Context, llv *snc.LVMLogicalVolume) error {
	return r.cl.Create(ctx, llv)
}

// --- DRBDResource (DRBDR) ---

func (r *Reconciler) patchDRBDR(ctx context.Context, obj, base *v1alpha1.DRBDResource, optimisticLock bool) error {
	var patch client.Patch
	if optimisticLock {
		patch = client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})
	} else {
		patch = client.MergeFrom(base)
	}
	return r.cl.Patch(ctx, obj, patch)
}

func (r *Reconciler) deleteDRBDR(ctx context.Context, drbdr *v1alpha1.DRBDResource) error {
	if drbdr.DeletionTimestamp != nil {
		return nil
	}
	return client.IgnoreNotFound(r.cl.Delete(ctx, drbdr))
}

func (r *Reconciler) createDRBDR(ctx context.Context, drbdr *v1alpha1.DRBDResource) error {
	return r.cl.Create(ctx, drbdr)
}

// --- ReplicatedStoragePool (RSP) ---

// getNodeEligibility fetches the eligible node entry for the given node from RSP.
// Returns (nil, nil) if node not in eligibleNodes or eligibleNodes is empty.
func (r *Reconciler) getNodeEligibility(
	ctx context.Context,
	rspName string,
	nodeName string,
) (*v1alpha1.ReplicatedStoragePoolEligibleNode, error) {
	var unsafeRSP v1alpha1.ReplicatedStoragePool
	if err := r.cl.Get(ctx, client.ObjectKey{Name: rspName}, &unsafeRSP, client.UnsafeDisableDeepCopy); err != nil {
		return nil, err
	}

	for i := range unsafeRSP.Status.EligibleNodes {
		if unsafeRSP.Status.EligibleNodes[i].NodeName == nodeName {
			// Return a copy to avoid aliasing with cache.
			result := unsafeRSP.Status.EligibleNodes[i]
			return &result, nil
		}
	}

	return nil, nil
}

// --- Node ---

// getNodeReady checks if a node is ready.
func (r *Reconciler) getNodeReady(ctx context.Context, nodeName string) (bool, error) {
	var unsafeNode corev1.Node
	if err := r.cl.Get(ctx, client.ObjectKey{Name: nodeName}, &unsafeNode, client.UnsafeDisableDeepCopy); err != nil {
		return false, err
	}

	_, readyCond := nodeutil.GetNodeCondition(&unsafeNode.Status, corev1.NodeReady)
	return readyCond != nil && readyCond.Status == corev1.ConditionTrue, nil
}

// --- Pod ---

// getAgentReady checks if the agent pod is ready on a given node.
func (r *Reconciler) getAgentReady(ctx context.Context, nodeName string) (bool, error) {
	var unsafeList corev1.PodList
	if err := r.cl.List(ctx, &unsafeList,
		client.InNamespace(r.agentPodNamespace),
		client.MatchingLabels{"app": "agent"},
		client.MatchingFields{indexes.IndexFieldPodByNodeName: nodeName},
		client.UnsafeDisableDeepCopy,
	); err != nil {
		return false, err
	}

	// There may be completed/terminated pods that haven't been cleaned up yet,
	// so we iterate to find a ready one.
	for i := range unsafeList.Items {
		unsafePod := &unsafeList.Items[i]
		for _, cond := range unsafePod.Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				return true, nil
			}
		}
	}

	return false, nil
}
