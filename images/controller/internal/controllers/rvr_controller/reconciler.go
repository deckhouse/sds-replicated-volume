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
	"k8s.io/utils/ptr"
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

	// Get RSP eligibility view for condition checks.
	// Skip if node not assigned or RVR is being deleted.
	var rspView *rspEligibilityView
	if rvr != nil &&
		rvr.Spec.NodeName != "" &&
		!rvrShouldNotExist(rvr) &&
		rv != nil &&
		rv.Status.Configuration != nil &&
		rv.Status.Configuration.StoragePoolName != "" {
		rspView, err = r.getRSPEligibilityView(rf.Ctx(), rv.Status.Configuration.StoragePoolName, rvr.Spec.NodeName)
		if err != nil {
			return rf.Failf(err, "getting RSP eligibility for %s", rv.Status.Configuration.StoragePoolName).ToCtrl()
		}
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
	targetBV, intendedBV, outcome := r.reconcileBackingVolume(rf.Ctx(), rvr, &llvs, rv, drbdr, rspView)
	if outcome.ShouldReturn() {
		return outcome.ToCtrl()
	}

	// Reconcile the DRBD resource.
	drbdr, ro := r.reconcileDRBDResource(rf.Ctx(), rvr, rv, drbdr, targetBV, intendedBV)
	outcome = outcome.Merge(ro)
	if outcome.ShouldReturn() {
		return outcome.ToCtrl()
	}

	if rvr != nil {
		// compute agentReady and drbdrConfigurationPending
		drbdConfiguredCond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType)
		var agentReady, drbdrConfigurationPending bool
		if drbdConfiguredCond != nil {
			agentReady = drbdConfiguredCond.Reason != v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonAgentNotReady
			drbdrConfigurationPending = drbdConfiguredCond.Reason == v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonApplyingConfiguration
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
			// Ensure status fields.
			ensureStatusAddressesAndType(rf.Ctx(), rvr, drbdr),
			ensureStatusAttachment(rf.Ctx(), rvr, drbdr, agentReady, drbdrConfigurationPending),
			ensureStatusPeers(rf.Ctx(), rvr, drbdr),
			ensureStatusBackingVolume(rf.Ctx(), rvr, drbdr, llvs),
			ensureStatusQuorum(rf.Ctx(), rvr, drbdr),

			// Ensure conditions.
			ensureConditionAttached(rf.Ctx(), rvr, drbdr, datameshMember, agentReady, drbdrConfigurationPending),
			ensureConditionFullyConnected(rf.Ctx(), rvr, drbdr, datamesh, agentReady),
			ensureConditionBackingVolumeUpToDate(rf.Ctx(), rvr, drbdr, datameshMember, agentReady, drbdrConfigurationPending),
			ensureConditionReady(rf.Ctx(), rvr, drbdr, agentReady, drbdrConfigurationPending),
			ensureConditionSatisfyEligibleNodes(rf.Ctx(), rvr, rv, rspView),

			// Ensure datamesh pending transition and configured condition.
			ensureStatusDatameshPendingTransitionAndConfiguredCond(rf.Ctx(), rvr, rv, rspView),
		)
		if eo.Error() != nil {
			return rf.Failf(eo.Error(), "ensuring status").ToCtrl()
		}
		outcome = outcome.WithChangeFrom(eo)
	}

	// Patch the RVR status if changed.
	if outcome.DidChange() && rvr != nil {
		if err := r.patchRVRStatus(rf.Ctx(), rvr, base, outcome.OptimisticLockRequired()); err != nil {
			return rf.Fail(err).ToCtrl()
		}
	}

	return outcome.ToCtrl()
}

// ensureConditionAttached ensures the RVR Attached condition reflects the current DRBDR state.
func ensureConditionAttached(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	drbdr *v1alpha1.DRBDResource,
	datameshMember *v1alpha1.ReplicatedVolumeDatameshMember,
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
			"Configuration is being applied") || changed
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

// ensureStatusAttachment ensures the RVR Attachment status field reflects the current DRBDR state.
func ensureStatusAttachment(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	drbdr *v1alpha1.DRBDResource,
	agentReady bool,
	drbdrConfigurationPending bool,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "status-attachment")
	defer ef.OnEnd(&outcome)

	// Guard: agent not ready or configuration pending — keep attachment as is.
	if !agentReady || drbdrConfigurationPending {
		return ef.Ok()
	}

	// Compute actual attached state.
	actualAttached := drbdr != nil &&
		drbdr.Status.ActiveConfiguration != nil &&
		drbdr.Status.ActiveConfiguration.Role == v1alpha1.DRBDRolePrimary

	var attachment *v1alpha1.ReplicatedVolumeReplicaStatusAttachment
	if actualAttached {
		attachment = &v1alpha1.ReplicatedVolumeReplicaStatusAttachment{
			DevicePath:  drbdr.Status.Device,
			IOSuspended: drbdr.Status.DeviceIOSuspended != nil && *drbdr.Status.DeviceIOSuspended,
		}
	}

	changed := applyRVRAttachment(rvr, attachment)
	return ef.Ok().ReportChangedIf(changed)
}

// ensureStatusAddressesAndType ensures the RVR status Addresses and Type fields
// reflect the current DRBDR state.
func ensureStatusAddressesAndType(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	drbdr *v1alpha1.DRBDResource,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "status-addresses-and-type")
	defer ef.OnEnd(&outcome)

	changed := false

	// Guard: no DRBDR -> clear addresses and type.
	if drbdr == nil {
		if len(rvr.Status.Addresses) > 0 {
			rvr.Status.Addresses = nil
			changed = true
		}
		if rvr.Status.Type != "" {
			rvr.Status.Type = ""
			changed = true
		}
		return ef.Ok().ReportChangedIf(changed)
	}

	// Apply addresses from DRBDR status.
	if !slices.Equal(rvr.Status.Addresses, drbdr.Status.Addresses) {
		rvr.Status.Addresses = slices.Clone(drbdr.Status.Addresses)
		changed = true
	}

	// Apply type from DRBDR active configuration.
	// Note: Access and TieBreaker both appear as Diskless here because they cannot be
	// distinguished from the replica's own status.
	var typ v1alpha1.DRBDResourceType
	if drbdr.Status.ActiveConfiguration != nil {
		typ = drbdr.Status.ActiveConfiguration.Type
	}
	if rvr.Status.Type != typ {
		rvr.Status.Type = typ
		changed = true
	}

	return ef.Ok().ReportChangedIf(changed)
}

// ensureStatusPeers ensures the RVR status.peers field reflects the current DRBDR state.
//
// This function updates rvr.Status.Peers in-place based solely on drbdr.Status.Peers:
//   - Each drbdr peer becomes a peer entry (connection state, disk state, etc.)
//   - Type is computed from drbdr peer:
//   - Diskful → Diskful
//   - Diskless + AllowRemoteRead=false → Access
//   - Diskless + AllowRemoteRead=true → TieBreaker
//
// The order of rvr.Status.Peers mirrors drbdr.Status.Peers.
func ensureStatusPeers(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	drbdr *v1alpha1.DRBDResource,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "status-peers")
	defer ef.OnEnd(&outcome)

	changed := false

	// Guard: no DRBDR or no peers → clear rvr.Status.Peers.
	if drbdr == nil || len(drbdr.Status.Peers) == 0 {
		if len(rvr.Status.Peers) > 0 {
			rvr.Status.Peers = nil
			changed = true
		}
		return ef.Ok().ReportChangedIf(changed)
	}

	drbdrPeers := drbdr.Status.Peers
	n := len(drbdrPeers)

	// Ensure rvr.Status.Peers has the right length.
	if cap(rvr.Status.Peers) < n {
		// Need to grow capacity.
		newPeers := make([]v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus, n)
		copy(newPeers, rvr.Status.Peers)
		rvr.Status.Peers = newPeers
	}
	if len(rvr.Status.Peers) != n {
		rvr.Status.Peers = rvr.Status.Peers[:n]
		changed = true
	}

	// Update each position in-place (order mirrors drbdr.Status.Peers).
	for i := range drbdrPeers {
		src := &drbdrPeers[i]
		dst := &rvr.Status.Peers[i]

		// Compute target Type from drbdr peer.
		var targetType v1alpha1.ReplicaType
		switch src.Type {
		case v1alpha1.DRBDResourceTypeDiskful:
			targetType = v1alpha1.ReplicaTypeDiskful
		case v1alpha1.DRBDResourceTypeDiskless:
			if src.AllowRemoteRead {
				targetType = v1alpha1.ReplicaTypeTieBreaker
			} else {
				targetType = v1alpha1.ReplicaTypeAccess
			}
		}

		// Compute target Attached.
		targetAttached := src.Role == v1alpha1.DRBDRolePrimary

		// Update fields, tracking changes.
		if dst.Name != src.Name {
			dst.Name = src.Name
			changed = true
		}
		if dst.Type != targetType {
			dst.Type = targetType
			changed = true
		}
		if dst.Attached != targetAttached {
			dst.Attached = targetAttached
			changed = true
		}

		// Update ConnectionEstablishedOn in-place (order mirrors drbdr paths).
		establishedCount := 0
		for _, path := range src.Paths {
			if path.Established {
				establishedCount++
			}
		}
		if cap(dst.ConnectionEstablishedOn) < establishedCount {
			newConnOn := make([]string, establishedCount)
			copy(newConnOn, dst.ConnectionEstablishedOn)
			dst.ConnectionEstablishedOn = newConnOn
		}
		if len(dst.ConnectionEstablishedOn) != establishedCount {
			dst.ConnectionEstablishedOn = dst.ConnectionEstablishedOn[:establishedCount]
			changed = true
		}
		j := 0
		for _, path := range src.Paths {
			if path.Established {
				if dst.ConnectionEstablishedOn[j] != path.SystemNetworkName {
					dst.ConnectionEstablishedOn[j] = path.SystemNetworkName
					changed = true
				}
				j++
			}
		}
		if dst.ConnectionState != src.ConnectionState {
			dst.ConnectionState = src.ConnectionState
			changed = true
		}
		if dst.BackingVolumeState != src.DiskState {
			dst.BackingVolumeState = src.DiskState
			changed = true
		}
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
		changed = applyFullyConnectedCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedReasonNoPeers,
			"No peers configured") || changed
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

// ensureStatusBackingVolume ensures the RVR backing volume status fields reflect the current DRBDR state.
func ensureStatusBackingVolume(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	drbdr *v1alpha1.DRBDResource,
	llvs []snc.LVMLogicalVolume,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "status-backing-volume")
	defer ef.OnEnd(&outcome)

	changed := false

	// Apply backingVolume status fields.
	// If DRBDR exists and has an active configuration with LVMLogicalVolumeName, we look up
	// the corresponding LLV to populate size, VG name, and thin pool. If the referenced LLV
	// is not found, we fail — this indicates an inconsistent state.
	// If DRBDR is nil, we clear the backingVolume status entirely.
	if drbdr == nil {
		// No DRBDR -> clear BackingVolume entirely.
		if rvr.Status.BackingVolume != nil {
			rvr.Status.BackingVolume = nil
			changed = true
		}
	} else {
		// Find the LLV referenced by DRBDR active configuration.
		var llv *snc.LVMLogicalVolume
		if drbdr.Status.ActiveConfiguration != nil {
			llvName := drbdr.Status.ActiveConfiguration.LVMLogicalVolumeName
			if llvName != "" {
				llv = findLLVByName(llvs, llvName)
				if llv == nil {
					return ef.Errf("active LLV %q not found", llvName)
				}
			}
		}

		if llv == nil {
			// DRBDR exists but no LLV -> clear BackingVolume.
			if rvr.Status.BackingVolume != nil {
				rvr.Status.BackingVolume = nil
				changed = true
			}
		} else {
			// Build the target BackingVolume from LLV + DRBDR.
			target := v1alpha1.ReplicatedVolumeReplicaStatusBackingVolume{
				LVMVolumeGroupName: llv.Spec.LVMVolumeGroupName,
				State:              drbdr.Status.DiskState,
			}
			if llv.Spec.Thin != nil {
				target.LVMVolumeGroupThinPoolName = llv.Spec.Thin.PoolName
			}
			if llv.Status != nil && !llv.Status.ActualSize.IsZero() {
				target.Size = &llv.Status.ActualSize
			}

			// Compare and apply.
			needsUpdate := true
			if rvr.Status.BackingVolume != nil {
				current := rvr.Status.BackingVolume
				sizeEqual := (current.Size == nil && target.Size == nil) ||
					(current.Size != nil && target.Size != nil && current.Size.Cmp(*target.Size) == 0)
				needsUpdate = current.State != target.State ||
					current.LVMVolumeGroupName != target.LVMVolumeGroupName ||
					current.LVMVolumeGroupThinPoolName != target.LVMVolumeGroupThinPoolName ||
					!sizeEqual
			}
			if needsUpdate {
				rvr.Status.BackingVolume = &target
				changed = true
			}
		}
	}

	return ef.Ok().ReportChangedIf(changed).RequireOptimisticLock()
}

// ensureConditionBackingVolumeUpToDate ensures the RVR BackingVolumeUpToDate condition reflects the current DRBDR state.
func ensureConditionBackingVolumeUpToDate(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	drbdr *v1alpha1.DRBDResource,
	datameshMember *v1alpha1.ReplicatedVolumeDatameshMember,
	agentReady bool,
	drbdrConfigurationPending bool,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "condition-backing-volume-up-to-date")
	defer ef.OnEnd(&outcome)

	changed := false

	// Determine if the BackingVolumeUpToDate condition is relevant.
	// The condition is relevant only for datamesh members (if not a member, the peer is not
	// connected and the disk cannot be synchronized by definition), AND when a backing volume
	// either should exist (intendedType == Diskful) or actually exists (rvr.Status.BackingVolume != nil).
	intendedType := computeIntendedType(rvr, datameshMember)
	conditionRelevant := datameshMember != nil &&
		(intendedType == v1alpha1.ReplicaTypeDiskful || rvr.Status.BackingVolume != nil)

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
			"Configuration is being applied") || changed
		return ef.Ok().ReportChangedIf(changed)
	}

	// After passing drbdrConfigurationPending guard, ActiveConfiguration must exist.
	// If it's nil here, the state is inconsistent — likely a bug in the agent.
	if drbdr.Status.ActiveConfiguration == nil {
		return ef.Errf("drbdr.Status.ActiveConfiguration is nil after configuration is no longer pending")
	}

	// servingIO indicates that this replica is attached on the node and serving application I/O
	// (in DRBD this is the Primary role).
	// Used to provide detailed messages about where I/O is being handled.
	servingIO := drbdr.Status.ActiveConfiguration.Role == v1alpha1.DRBDRolePrimary

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
		reason = v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonAttaching
		if servingIO {
			message = "Backing volume is being attached; reading metadata from local device; application I/O forwarded to peers"
		} else {
			message = "Backing volume is being attached; reading metadata from local device"
		}

	case v1alpha1.DiskStateDetaching:
		reason = v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonDetaching
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

	case v1alpha1.DiskStateInconsistent, v1alpha1.DiskStateOutdated,
		v1alpha1.DiskStateNegotiating, v1alpha1.DiskStateConsistent:
		// States requiring synchronization — check peer availability first.
		hasUpToDatePeer := computeHasUpToDatePeer(rvr.Status.Peers)
		hasConnectedAttachedPeer := computeHasConnectedAttachedPeer(rvr.Status.Peers)
		hasAnyAttachedPeer := computeHasAnyAttachedPeer(rvr.Status.Peers)
		ioAvailable := servingIO || hasConnectedAttachedPeer || !hasAnyAttachedPeer

		if !hasUpToDatePeer {
			reason = v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonSynchronizationBlocked
			message = "Synchronization blocked: no peer with up-to-date data available"
			break
		}
		if !ioAvailable {
			reason = v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonSynchronizationBlocked
			message = "Synchronization blocked: awaiting connection to attached peer"
			break
		}

		// Synchronization can proceed — provide state-specific message.
		reason = v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonSynchronizing
		switch drbdr.Status.DiskState {
		case v1alpha1.DiskStateInconsistent:
			if servingIO {
				message = "Backing volume partially synchronized; local reads from synced blocks, others forwarded to peer"
			} else {
				message = "Backing volume partially synchronized; sync in progress"
			}
		case v1alpha1.DiskStateOutdated:
			if servingIO {
				message = "Backing volume data outdated; application I/O forwarded to up-to-date peer during resync"
			} else {
				message = "Backing volume data outdated; resynchronization in progress"
			}
		case v1alpha1.DiskStateNegotiating:
			if servingIO {
				message = "Negotiating sync direction; application I/O forwarded to peers"
			} else {
				message = "Negotiating synchronization direction with peers"
			}
		case v1alpha1.DiskStateConsistent:
			if servingIO {
				message = "Backing volume consistent, determining currency; application I/O forwarded to peers"
			} else {
				message = "Backing volume consistent; awaiting peer negotiation to determine if up-to-date"
			}
		}

	default:
		reason = v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateReasonUnknownState
		message = fmt.Sprintf("Unknown backing volume state: %s", drbdr.Status.DiskState)
	}

	changed = applyBackingVolumeUpToDateCondFalse(rvr, reason, message) || changed
	return ef.Ok().ReportChangedIf(changed)
}

// computeHasUpToDatePeer returns true if any peer has UpToDate disk.
// Note: if peer is not connected, its BackingVolumeState won't be UpToDate.
func computeHasUpToDatePeer(peers []v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus) bool {
	for i := range peers {
		if peers[i].BackingVolumeState == v1alpha1.DiskStateUpToDate {
			return true
		}
	}
	return false
}

// computeHasConnectedAttachedPeer returns true if any peer is attached and connected.
func computeHasConnectedAttachedPeer(peers []v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus) bool {
	for i := range peers {
		if peers[i].Attached && len(peers[i].ConnectionEstablishedOn) > 0 {
			return true
		}
	}
	return false
}

// computeHasAnyAttachedPeer returns true if any peer is attached.
func computeHasAnyAttachedPeer(peers []v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus) bool {
	for i := range peers {
		if peers[i].Attached {
			return true
		}
	}
	return false
}

// ensureStatusQuorum ensures the RVR quorum-related status fields reflect the current DRBDR state.
func ensureStatusQuorum(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	drbdr *v1alpha1.DRBDResource,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "status-quorum")
	defer ef.OnEnd(&outcome)

	changed := false

	// When drbdr is nil, clear both fields.
	if drbdr == nil {
		if rvr.Status.QuorumSummary != nil {
			rvr.Status.QuorumSummary = nil
			changed = true
		}
		if rvr.Status.Quorum != nil {
			rvr.Status.Quorum = nil
			changed = true
		}
		return ef.Ok().ReportChangedIf(changed)
	}

	summary := &v1alpha1.ReplicatedVolumeReplicaStatusQuorumSummary{}

	// Count connected voting/UpToDate peers.
	for i := range rvr.Status.Peers {
		p := &rvr.Status.Peers[i]
		if p.ConnectionState != v1alpha1.ConnectionStateConnected {
			continue
		}
		if p.Type == v1alpha1.ReplicaTypeDiskful || p.Type == v1alpha1.ReplicaTypeTieBreaker {
			summary.ConnectedVotingPeers++
		}
		if p.BackingVolumeState == v1alpha1.DiskStateUpToDate {
			summary.ConnectedUpToDatePeers++
		}
	}

	// Copy quorum thresholds from drbdr.status.activeConfiguration.
	if ac := drbdr.Status.ActiveConfiguration; ac != nil {
		if ac.Quorum != nil {
			summary.Quorum = ptr.To(int(*ac.Quorum))
		}
		if ac.QuorumMinimumRedundancy != nil {
			summary.QuorumMinimumRedundancy = ptr.To(int(*ac.QuorumMinimumRedundancy))
		}
	}

	// Update QuorumSummary if it has changed.
	if rvr.Status.QuorumSummary == nil || *rvr.Status.QuorumSummary != *summary {
		rvr.Status.QuorumSummary = summary
		changed = true
	}

	// Update Quorum if it has changed.
	if !ptr.Equal(rvr.Status.Quorum, drbdr.Status.Quorum) {
		rvr.Status.Quorum = drbdr.Status.Quorum
		changed = true
	}

	return ef.Ok().ReportChangedIf(changed)
}

// ensureConditionReady ensures the RVR Ready condition reflects the current DRBDR quorum state.
func ensureConditionReady(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	drbdr *v1alpha1.DRBDResource,
	agentReady bool,
	drbdrConfigurationPending bool,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "condition-ready")
	defer ef.OnEnd(&outcome)

	changed := false

	// Guard: RVR is being deleted (drbdr == nil implies deletion).
	if rvrShouldNotExist(rvr) || drbdr == nil {
		changed = applyReadyCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondReadyReasonDeleting,
			"Replica is being deleted") || changed
		return ef.Ok().ReportChangedIf(changed)
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
			"Configuration is being applied") || changed
		return ef.Ok().ReportChangedIf(changed)
	}

	// Build message with quorum info: "quorum: M/N, data quorum: J/K"
	// M = connectedVotingPeers, N = quorum threshold (or "unknown")
	// J = connectedUpToDatePeers, K = quorumMinimumRedundancy (or "unknown")
	msg := "quorum: unknown"
	if qs := rvr.Status.QuorumSummary; qs != nil {
		quorumStr := "unknown"
		if qs.Quorum != nil {
			quorumStr = strconv.Itoa(*qs.Quorum)
		}
		qmrStr := "unknown"
		if qs.QuorumMinimumRedundancy != nil {
			qmrStr = strconv.Itoa(*qs.QuorumMinimumRedundancy)
		}
		msg = fmt.Sprintf("quorum: %d/%s, data quorum: %d/%s",
			qs.ConnectedVotingPeers, quorumStr,
			qs.ConnectedUpToDatePeers, qmrStr)
	}

	// Set Ready condition based on drbdr.Status.Quorum.
	if drbdr.Status.Quorum == nil || !*drbdr.Status.Quorum {
		changed = applyReadyCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondReadyReasonQuorumLost,
			msg) || changed
	} else {
		changed = applyReadyCondTrue(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondReadyReasonReady,
			msg) || changed
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

	// Guard: no RV or no Configuration — condition Unknown.
	if rv == nil || rv.Status.Configuration == nil || rv.Status.Configuration.StoragePoolName == "" {
		changed = applySatisfyEligibleNodesCondUnknown(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonPendingConfiguration,
			"Configuration not yet available") || changed
		return ef.Ok().ReportChangedIf(changed)
	}

	// Guard: RSP not found — condition Unknown.
	if rspView == nil {
		changed = applySatisfyEligibleNodesCondUnknown(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonPendingConfiguration,
			"ReplicatedStoragePool not found") || changed
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

		// Then, check ThinPool if specified.
		if rvr.Spec.LVMVolumeGroupThinPoolName != "" {
			lvg = findLVGInEligibleNode(rspView.EligibleNode, rvr.Spec.LVMVolumeGroupName, rvr.Spec.LVMVolumeGroupThinPoolName)
			if lvg == nil {
				changed = applySatisfyEligibleNodesCondFalse(rvr,
					v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonThinPoolMismatch,
					"Node and LVMVolumeGroup are eligible, but ThinPool is not in the allowed list for this LVMVolumeGroup according to ReplicatedStoragePool") || changed
				return ef.Ok().ReportChangedIf(changed)
			}
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

// ensureStatusDatameshPendingTransitionAndConfiguredCond determines what datamesh transition is pending
// (join, leave, role change, or backing volume change) by comparing rvr.Spec to rvr.Status,
// and updates status.datameshPending and the Configured condition accordingly.
func ensureStatusDatameshPendingTransitionAndConfiguredCond(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	rv *v1alpha1.ReplicatedVolume,
	rspView *rspEligibilityView,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "status-datamesh-pending-and-configured-cond")
	defer ef.OnEnd(&outcome)

	changed := false

	// Compute target (single call for both status fields).
	target, condReason, condMessage := computeTargetDatameshPendingTransition(rvr, rspView)

	// Append RV datamesh pending transition message to condition message if there's a pending transition.
	if target != nil && rv != nil {
		for i := range rv.Status.DatameshPendingReplicaTransitions {
			if rv.Status.DatameshPendingReplicaTransitions[i].Name == rvr.Name {
				if msg := rv.Status.DatameshPendingReplicaTransitions[i].Message; msg != "" {
					condMessage += "; " + msg
				}
				break
			}
		}
	}

	// Apply datameshPending.
	changed = applyDatameshPendingTransition(rvr, target) || changed

	// Apply Configured condition.
	if condReason == "" {
		changed = applyConfiguredCondAbsent(rvr) || changed
	} else {
		switch condReason {
		case v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonConfigured:
			changed = applyConfiguredCondTrue(rvr, condReason, condMessage) || changed
		case v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonPendingConfiguration:
			changed = applyConfiguredCondUnknown(rvr, condReason, condMessage) || changed
		default:
			changed = applyConfiguredCondFalse(rvr, condReason, condMessage) || changed
		}
	}

	return ef.Ok().ReportChangedIf(changed)
}

// applyConfiguredCondAbsent removes the Configured condition.
func applyConfiguredCondAbsent(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
	return obju.RemoveStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondConfiguredType)
}

// applyConfiguredCondUnknown sets the Configured condition to Unknown.
func applyConfiguredCondUnknown(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondConfiguredType,
		Status:  metav1.ConditionUnknown,
		Reason:  reason,
		Message: message,
	})
}

// applyConfiguredCondFalse sets the Configured condition to False.
func applyConfiguredCondFalse(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondConfiguredType,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

// applyConfiguredCondTrue sets the Configured condition to True.
func applyConfiguredCondTrue(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondConfiguredType,
		Status:  metav1.ConditionTrue,
		Reason:  reason,
		Message: message,
	})
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
	rspView *rspEligibilityView,
) (targetBV, intendedBV *backingVolume, outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "backing-volume")
	defer rf.OnEnd(&outcome)

	// 1. Deletion branch: if RVR should not exist, delete all LLVs.
	if rvrShouldNotExist(rvr) {
		if len(*llvs) > 0 {
			deletingNames, ro := r.reconcileLLVsDeletion(rf.Ctx(), llvs, nil)
			if ro.ShouldReturn() {
				return nil, nil, ro
			}
			// rvr can be nil here if it was already deleted; nothing to update in that case.
			if rvr == nil {
				return nil, nil, rf.Continue()
			}
			// Still deleting — set condition False.
			changed := applyBackingVolumeReadyCondFalse(rvr,
				v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonNotApplicable,
				fmt.Sprintf("Replica is being deleted; deleting backing volumes: %s", strings.Join(deletingNames, ", ")))
			return nil, nil, rf.Continue().ReportChangedIf(changed)
		}

		// All LLVs deleted.
		// If rvr is nil (already deleted), just return.
		if rvr == nil {
			return nil, nil, rf.Continue()
		}
		// Remove condition entirely.
		changed := applyBackingVolumeReadyCondAbsent(rvr)
		return nil, nil, rf.Continue().ReportChangedIf(changed)
	}

	// 2. Compute actual state.
	actual := computeActualBackingVolume(drbdr, *llvs)

	// 3. ReplicatedVolume not found — stop reconciliation and wait for it to appear.
	// Without RV we cannot determine datamesh state.
	if rv == nil {
		changed := applyBackingVolumeReadyCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonWaitingForReplicatedVolume,
			"ReplicatedVolume not found")
		return actual, nil, rf.Continue().ReportChangedIf(changed)
	}

	// 4. Datamesh not initialized yet — wait for RV controller to set it up.
	// Normally datamesh is already initialized by the time RVR is created,
	// but we check for non-standard usage scenarios (e.g., RVR created before RV) and general correctness.
	if rv.Status.DatameshRevision == 0 {
		changed := applyBackingVolumeReadyCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonWaitingForReplicatedVolume,
			"Datamesh is not initialized yet")
		return actual, nil, rf.Continue().ReportChangedIf(changed)
	}

	// 5. Compute intended state.
	intended, reason, message := computeIntendedBackingVolume(rvr, rv, actual, rspView)

	// 6. If intended == nil, delete LLVs, clear size and remove condition when done.
	if intended == nil {
		if len(*llvs) > 0 {
			deletingNames, ro := r.reconcileLLVsDeletion(rf.Ctx(), llvs, nil)
			if ro.ShouldReturn() {
				return nil, nil, ro
			}
			// Still deleting — set condition False.
			changed := applyBackingVolumeReadyCondFalse(rvr, reason,
				fmt.Sprintf("%s; deleting backing volumes: %s", message, strings.Join(deletingNames, ", ")))
			return nil, nil, rf.Continue().ReportChangedIf(changed)
		}

		// All LLVs deleted — remove condition entirely.
		changed := applyBackingVolumeReadyCondAbsent(rvr)
		return nil, nil, rf.Continue().ReportChangedIf(changed)
	}

	// 7. Find the intended LLV in the list.
	intendedLLV := findLLVByName(*llvs, intended.LLVName)

	// 8. Create if missing.
	if intendedLLV == nil {
		llv, err := newLLV(r.scheme, rvr, rv, intended)
		if err != nil {
			return nil, nil, rf.Failf(err, "constructing LLV %s", intended.LLVName)
		}

		if err := r.createLLV(rf.Ctx(), llv); err != nil {
			// Handle validation errors specially: log, set condition and requeue.
			// LVMLogicalVolume is not our API, so we treat validation errors as recoverable
			// for safety reasons (e.g., schema changes in sds-node-configurator).
			if apierrors.IsInvalid(err) {
				rf.Log().Error(err, "Failed to create backing volume", "llvName", intended.LLVName)
				applyBackingVolumeReadyCondFalse(rvr,
					v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonProvisioningFailed,
					fmt.Sprintf("Failed to create backing volume %s: %s", intended.LLVName, computeAPIValidationErrorCauses(err)))
				return actual, intended, rf.ContinueAndRequeueAfter(5 * time.Minute).ReportChanged()
			}
			return nil, nil, rf.Failf(err, "creating LLV %s", intended.LLVName)
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
		changed := applyBackingVolumeReadyCondFalse(rvr, reason, message)

		// Return actual BV as target.
		return actual, intended, rf.Continue().ReportChangedIf(changed)
	}

	// 9. Ensure metadata (ownerRef, finalizer, label) on the intended LLV.
	// Note: In rare cases this results in two LLV patches per reconcile (metadata + resize).
	// This only happens during migration or manual interventions, so it's acceptable.
	if !isLLVMetadataInSync(rvr, rv, intendedLLV) {
		base := intendedLLV.DeepCopy()
		if _, err := applyLLVMetadata(r.scheme, rvr, rv, intendedLLV); err != nil {
			return nil, nil, rf.Failf(err, "applying LLV %s metadata", intendedLLV.Name)
		}
		if err := r.patchLLV(rf.Ctx(), intendedLLV, base, true); err != nil {
			return nil, nil, rf.Failf(err, "patching LLV %s metadata", intendedLLV.Name)
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
		changed := applyBackingVolumeReadyCondFalse(rvr, reason, message)
		return actual, intended, rf.Continue().ReportChangedIf(changed)
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
					applyBackingVolumeReadyCondFalse(rvr,
						v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonResizeFailed,
						fmt.Sprintf("Failed to resize backing volume %s: %s", intended.LLVName, computeAPIValidationErrorCauses(err)))
					return actual, intended, rf.ContinueAndRequeueAfter(5 * time.Minute).ReportChanged()
				}
				return nil, nil, rf.Failf(err, "patching LLV %s size", intendedLLV.Name)
			}
		}

		changed := applyBackingVolumeReadyCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonResizing,
			fmt.Sprintf("Resizing backing volume %s from %s to %s", intended.LLVName, actualSize.String(), intended.Size.String()))

		return actual, intended, rf.Continue().ReportChangedIf(changed)
	}

	// 12. Fully ready: delete obsolete LLVs and set condition to Ready.
	// Keep actual LLV if it's different from intended (migration in progress).
	// But at this point intended is ready, so we can delete actual too.
	message = fmt.Sprintf("Backing volume %s is ready", intended.LLVName)
	if len(*llvs) > 0 {
		var ro flow.ReconcileOutcome
		deletingNames, ro := r.reconcileLLVsDeletion(rf.Ctx(), llvs, []string{intended.LLVName})
		if ro.ShouldReturn() {
			return nil, nil, ro
		}
		message = fmt.Sprintf("%s; deleting obsolete: %s", message, strings.Join(deletingNames, ", "))
	}
	changed := applyBackingVolumeReadyCondTrue(rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonReady, message)

	return intended, intended, rf.Continue().ReportChangedIf(changed)
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

// Equal returns true if bv and other are equal (both nil, or all fields match).
func (bv *backingVolume) Equal(other *backingVolume) bool {
	if bv == nil && other == nil {
		return true
	}
	if bv == nil || other == nil {
		return false
	}
	return bv.LLVName == other.LLVName &&
		bv.LVMVolumeGroupName == other.LVMVolumeGroupName &&
		bv.ThinPoolName == other.ThinPoolName &&
		bv.Size.Cmp(other.Size) == 0
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
func computeIntendedBackingVolume(rvr *v1alpha1.ReplicatedVolumeReplica, rv *v1alpha1.ReplicatedVolume, actual *backingVolume, rspView *rspEligibilityView) (intended *backingVolume, reason, message string) {
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

		// Validate RSP eligibility and storage assignment.
		code, msg := rspView.isStorageEligible(rvr.Spec.LVMVolumeGroupName, rvr.Spec.LVMVolumeGroupThinPoolName)
		if code != storageEligibilityOK {
			var reason string
			switch code {
			case storageEligibilityRSPNotAvailable:
				reason = v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonWaitingForReplicatedVolume
			default:
				reason = v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonPendingScheduling
			}
			return nil, reason, msg
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
func (r *Reconciler) reconcileDRBDResource(ctx context.Context, rvr *v1alpha1.ReplicatedVolumeReplica, rv *v1alpha1.ReplicatedVolume, drbdr *v1alpha1.DRBDResource, targetBV, intendedBV *backingVolume) (_ *v1alpha1.DRBDResource, outcome flow.ReconcileOutcome) {
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

		// Set DRBDConfigured condition on RVR.
		changed := false
		if rvr != nil {
			var message string
			if drbdr == nil {
				message = "Replica is being deleted; DRBD resource has been deleted"
			} else {
				message = fmt.Sprintf("Replica is being deleted; waiting for DRBD resource %s to be deleted", drbdr.Name)
			}
			changed = applyDRBDConfiguredCondFalse(rvr,
				v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonNotApplicable, message)
		}

		// DRBDR may still exist in the API (e.g., blocked by finalizers),
		// but for reconciliation purposes we consider it deleted.
		return nil, rf.Continue().ReportChangedIf(changed)
	}

	// 2. Node not assigned yet — cannot configure DRBD without a node.
	// Note: Per API validation, NodeName is immutable once set. We rely on this invariant below.
	if rvr.Spec.NodeName == "" {
		changed := applyDRBDConfiguredCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonPendingScheduling,
			"Waiting for node assignment")
		return drbdr, rf.Continue().ReportChangedIf(changed)
	}

	// 3. ReplicatedVolume not found — stop reconciliation and wait for it to appear.
	// Without RV we cannot determine datamesh state, system networks, peers, etc.
	if rv == nil {
		changed := applyDRBDConfiguredCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonWaitingForReplicatedVolume,
			"ReplicatedVolume not found")
		return drbdr, rf.Continue().ReportChangedIf(changed)
	}

	// 4. Datamesh not initialized yet — wait for RV controller to set it up.
	// Normally datamesh is already initialized by the time RVR is created,
	// but we check for non-standard usage scenarios (e.g., RVR created before RV) and general correctness.
	if rv.Status.DatameshRevision == 0 {
		changed := applyDRBDConfiguredCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonWaitingForReplicatedVolume,
			"Datamesh is not initialized yet")
		return drbdr, rf.Continue().ReportChangedIf(changed)
	}

	datamesh := &rv.Status.Datamesh

	// 5. No system networks in datamesh — DRBD needs networks to communicate.
	// If datamesh is initialized, system networks should already be set,
	// but we check for non-standard scenarios and general correctness.
	if len(datamesh.SystemNetworkNames) == 0 {
		changed := applyDRBDConfiguredCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonWaitingForReplicatedVolume,
			"No system networks in datamesh")
		return drbdr, rf.Continue().ReportChangedIf(changed)
	}

	member := datamesh.FindMemberByName(rvr.Name)
	intendedType := computeIntendedType(rvr, member)
	targetType := computeTargetType(intendedType, targetBV.LLVNameOrEmpty())

	// 6. Create or update DRBDR.
	var targetDRBDRReconciliationCache v1alpha1.ReplicatedVolumeReplicaStatusDRBDRReconciliationCache
	if drbdr == nil {
		// Compute target DRBDR spec.
		targetSpec := computeTargetDRBDRSpec(rvr, drbdr, datamesh, member, targetBV.LLVNameOrEmpty(), targetType)

		// Create new DRBDResource.
		newObj, err := newDRBDR(r.scheme, rvr, targetSpec)
		if err != nil {
			return drbdr, rf.Failf(err, "constructing DRBDResource")
		}
		if err := r.createDRBDR(rf.Ctx(), newObj); err != nil {
			return drbdr, rf.Failf(err, "creating DRBDResource")
		}
		drbdr = newObj

		targetDRBDRReconciliationCache = computeTargetDRBDRReconciliationCache(rv.Status.DatameshRevision, drbdr.Generation, targetType)
	} else {
		// We need to check DRBDR spec if any of the tracked values changed since last reconciliation:
		// - DRBDResource.Generation (DRBDR was modified externally)
		// - DatameshRevision (datamesh configuration changed)
		// - RVRType (replica type changed, e.g. diskful -> tiebreaker due to missing disk)
		targetDRBDRReconciliationCache := computeTargetDRBDRReconciliationCache(rv.Status.DatameshRevision, drbdr.Generation, targetType)
		specMayNeedUpdate := rvr.Status.DRBDRReconciliationCache != targetDRBDRReconciliationCache

		if specMayNeedUpdate {
			// Compute target DRBDR spec.
			targetSpec := computeTargetDRBDRSpec(rvr, drbdr, datamesh, member, targetBV.LLVNameOrEmpty(), targetType)

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

	// 7. Cache target configuration to skip redundant spec comparisons on next reconcile.
	//    Further we always require optimistic lock.
	changed := applyRVRDRBDRReconciliationCache(rvr, targetDRBDRReconciliationCache)

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
		changed = applyDRBDConfiguredCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonAgentNotReady, msg) || changed
		return drbdr, rf.Continue().ReportChangedIf(changed).RequireOptimisticLock()
	}

	// 9. Check DRBDR configuration status.
	drbdrConfiguredCond := obju.GetStatusCondition(drbdr, v1alpha1.DRBDResourceCondConfiguredType)

	// 9a. Configured condition not set yet — waiting for agent to respond.
	if drbdrConfiguredCond == nil {
		changed = applyDRBDConfiguredCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonApplyingConfiguration,
			"Waiting for agent to respond (Configured condition is not set yet)") || changed
		return drbdr, rf.Continue().ReportChangedIf(changed).RequireOptimisticLock()
	}

	// 9b. Agent hasn't processed the current generation yet.
	if drbdrConfiguredCond.ObservedGeneration != drbdr.Generation {
		msg := fmt.Sprintf("Waiting for agent to respond (generation: %d, observedGeneration: %d)",
			drbdr.Generation, drbdrConfiguredCond.ObservedGeneration)
		changed = applyDRBDConfiguredCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonApplyingConfiguration, msg) || changed
		return drbdr, rf.Continue().ReportChangedIf(changed).RequireOptimisticLock()
	}

	// 9c. DRBDR is in maintenance mode.
	if drbdrConfiguredCond.Reason == v1alpha1.DRBDResourceCondConfiguredReasonInMaintenance {
		changed = applyDRBDConfiguredCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonApplyingConfiguration,
			"DRBD is in maintenance mode") || changed
		return drbdr, rf.Continue().ReportChangedIf(changed).RequireOptimisticLock()
	}

	// 9d. DRBDR configuration failed.
	if drbdrConfiguredCond.Status != metav1.ConditionTrue {
		msg := fmt.Sprintf("DRBD configuration failed (reason: %s)", drbdrConfiguredCond.Reason)
		changed = applyDRBDConfiguredCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonConfigurationFailed, msg) || changed
		return drbdr, rf.Continue().ReportChangedIf(changed).RequireOptimisticLock()
	}

	// At this point DRBDR is configured (Configured condition is True).

	// 10. DRBDR is configured — check addresses are populated.
	if len(rvr.Status.Addresses) == 0 {
		changed = applyDRBDConfiguredCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonApplyingConfiguration,
			"Waiting for DRBD addresses") || changed
		return drbdr, rf.Continue().ReportChangedIf(changed).RequireOptimisticLock()
	}

	// 11. If targetType != intendedType, DRBDR is configured as TieBreaker
	// but we want Diskful — waiting for backing volume to become ready.
	// This happens when LLV is not yet ready and we preconfigured DRBDR as TieBreaker.
	// Once LLV becomes ready, targetLLVName will be set and targetType will match intendedType.
	if targetType != intendedType {
		changed = applyDRBDConfiguredCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonWaitingForBackingVolume,
			"DRBD preconfigured as TieBreaker, waiting for backing volume to become ready") || changed
		return drbdr, rf.Continue().ReportChangedIf(changed).RequireOptimisticLock()
	}

	// 12. Check if backing volume matches intended configuration.
	if intendedBV != nil && targetBV != nil {
		// 12a. Check if LVG or ThinPool changed — wait for backing volume replacement.
		// This can happen when LVG or ThinPool was changed in RVR spec (before datamesh membership)
		// or in datamesh (after membership).
		if targetBV.LVMVolumeGroupName != intendedBV.LVMVolumeGroupName ||
			targetBV.ThinPoolName != intendedBV.ThinPoolName {
			formatBV := func(bv *backingVolume) string {
				if bv.ThinPoolName == "" {
					return fmt.Sprintf("LVG %q without thinpool", bv.LVMVolumeGroupName)
				}
				return fmt.Sprintf("LVG %q with thinpool %q", bv.LVMVolumeGroupName, bv.ThinPoolName)
			}
			msg := fmt.Sprintf("Backing volume replacement pending: current %s, expected %s",
				formatBV(targetBV), formatBV(intendedBV))
			changed = applyDRBDConfiguredCondFalse(rvr,
				v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonWaitingForBackingVolume, msg) || changed
			return drbdr, rf.Continue().ReportChangedIf(changed).RequireOptimisticLock()
		}

		// 12b. Check if backing volume needs resize.
		if targetBV.Size.Cmp(intendedBV.Size) < 0 {
			msg := fmt.Sprintf("Backing volume resize pending: current size %s, expected size %s",
				targetBV.Size.String(), intendedBV.Size.String())
			changed = applyDRBDConfiguredCondFalse(rvr,
				v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonWaitingForBackingVolume, msg) || changed
			return drbdr, rf.Continue().ReportChangedIf(changed).RequireOptimisticLock()
		}
	}

	// 13. If not a datamesh member — DRBD is preconfigured, waiting for membership.
	if member == nil {
		changed = applyDRBDConfiguredCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonPendingDatameshJoin,
			"DRBD preconfigured, waiting for datamesh membership") || changed
		return drbdr, rf.Continue().ReportChangedIf(changed).RequireOptimisticLock()
	}

	// Invariant: at this point backing volume must be either not needed or fully ready.
	//
	// - Not needed (diskless replica): both targetBV and intendedBV are nil.
	// - Fully ready: backing volume exists, matches intended configuration (LVG, ThinPool, Size),
	//   and is attached to DRBDR — targetBV equals intendedBV.
	//
	// Any in-progress state (creating, resizing, replacing) would have returned earlier.
	if !targetBV.Equal(intendedBV) {
		panic(fmt.Sprintf("BUG: targetBV and intendedBV must be equal, got targetBV=%+v, intendedBV=%+v", targetBV, intendedBV))
	}

	// 14. Replica is fully configured to the current datamesh revision — record DatameshRevision.
	//
	// At this point, the replica is fully configured for the current datamesh revision:
	//   - DRBD was configured to match the intended state derived from this datamesh revision.
	//   - Backing volume (if Diskful) was configured: exists and matches intended LVG/ThinPool/Size.
	//   - Backing volume (if Diskful) is ready: reported ready and actual size >= intended size.
	//   - Agent confirmed successful configuration.
	//
	// Note: "configured" does NOT mean:
	//   - DRBD connections are established (happens asynchronously after configuration).
	//   - Backing volume is synchronized (resync happens asynchronously if the volume was newly added).
	if rvr.Status.DatameshRevision != rv.Status.DatameshRevision {
		rvr.Status.DatameshRevision = rv.Status.DatameshRevision
		changed = true
	}
	changed = applyDRBDConfiguredCondTrue(rvr,
		v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonConfigured, "Configured") || changed
	return drbdr, rf.Continue().ReportChangedIf(changed).RequireOptimisticLock()
}

// computeIntendedType returns the intended replica type.
// If member is nil, returns rvr.Spec.Type.
// During transitions, uses TieBreaker as intermediate type:
// - ToDiskful but not yet Diskful → TieBreaker (disk is being prepared)
// - Diskful with ToDiskless → TieBreaker (data is being moved away)
func computeIntendedType(rvr *v1alpha1.ReplicatedVolumeReplica, member *v1alpha1.ReplicatedVolumeDatameshMember) v1alpha1.ReplicaType {
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

// computeTargetType returns the target replica type.
// If intended is Diskful but there's no backing volume, returns TieBreaker.
// Otherwise returns the intended type.
func computeTargetType(intendedType v1alpha1.ReplicaType, targetLLVName string) v1alpha1.ReplicaType {
	if intendedType == v1alpha1.ReplicaTypeDiskful && targetLLVName == "" {
		return v1alpha1.ReplicaTypeTieBreaker
	}
	return intendedType
}

// computeTargetDRBDRReconciliationCache returns the target reconciliation cache
// for the current reconciliation iteration.
func computeTargetDRBDRReconciliationCache(
	datameshRevision int64,
	drbdrGeneration int64,
	rvrType v1alpha1.ReplicaType,
) v1alpha1.ReplicatedVolumeReplicaStatusDRBDRReconciliationCache {
	return v1alpha1.ReplicatedVolumeReplicaStatusDRBDRReconciliationCache{
		DatameshRevision: datameshRevision,
		DRBDRGeneration:  drbdrGeneration,
		RVRType:          rvrType,
	}
}

// computeTargetDatameshPendingTransition computes the target datameshPending field based on
// rvr.Spec (intended state), rvr.Status (actual state), and eligibility in RSP.
//
// Returns (target, condReason, condMessage) where:
//   - target is the target datameshPending value (nil means no pending operation)
//   - condReason is the Configured condition reason
//   - condMessage is the Configured condition message
//
// The condReason/condMessage are always set: when target is nil, they indicate
// why there's no pending operation (either configured or blocked).
func computeTargetDatameshPendingTransition(
	rvr *v1alpha1.ReplicatedVolumeReplica,
	rspView *rspEligibilityView,
) (target *v1alpha1.ReplicatedVolumeReplicaStatusDatameshPendingTransition, condReason, condMessage string) {
	// Check if being deleted.
	if rvr.DeletionTimestamp != nil {
		if rvr.Status.DatameshRevision != 0 {
			// Currently a datamesh member - need to leave.
			return &v1alpha1.ReplicatedVolumeReplicaStatusDatameshPendingTransition{
					Member: ptr.To(false),
				}, v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonPendingLeave,
				"Deletion in progress, waiting to leave datamesh"
		}
		// Not a member - nothing to do (condition will be removed).
		return nil, "", ""
	}

	// Check if scheduled.
	if rvr.Spec.NodeName == "" {
		return nil, v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonPendingScheduling,
			"Waiting for node assignment"
	}

	// Check if Diskful needs LVG.
	if rvr.Spec.Type == v1alpha1.ReplicaTypeDiskful && rvr.Spec.LVMVolumeGroupName == "" {
		return nil, v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonPendingScheduling,
			"Waiting for storage assignment"
	}

	// Check RSP availability.
	if rspView == nil {
		return nil, v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonPendingConfiguration,
			"Waiting for storage pool configuration"
	}

	// Check node eligibility.
	if rspView.EligibleNode == nil {
		return nil, v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonNodeNotEligible,
			fmt.Sprintf("Node %q is not eligible in storage pool", rvr.Spec.NodeName)
	}

	// Check storage eligibility for Diskful.
	if rvr.Spec.Type == v1alpha1.ReplicaTypeDiskful {
		code, msg := rspView.isStorageEligible(rvr.Spec.LVMVolumeGroupName, rvr.Spec.LVMVolumeGroupThinPoolName)
		if code != storageEligibilityOK {
			var reason string
			switch code {
			case storageEligibilityRSPNotAvailable:
				reason = v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonPendingConfiguration
			case storageEligibilityNodeNotEligible:
				reason = v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonNodeNotEligible
			default:
				reason = v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonStorageNotEligible
			}
			return nil, reason, msg
		}
	}

	// Check if already a datamesh member.
	isMember := rvr.Status.DatameshRevision != 0

	if !isMember {
		// Not a member - need to join.
		pending := &v1alpha1.ReplicatedVolumeReplicaStatusDatameshPendingTransition{
			Member: ptr.To(true),
			Role:   rvr.Spec.Type,
		}
		if rvr.Spec.Type == v1alpha1.ReplicaTypeDiskful {
			pending.LVMVolumeGroupName = rvr.Spec.LVMVolumeGroupName
			pending.ThinPoolName = rvr.Spec.LVMVolumeGroupThinPoolName
		}
		return pending, v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonPendingJoin,
			fmt.Sprintf("Waiting to join datamesh as %s", rvr.Spec.Type)
	}

	// Already a member - check if state is in sync.
	// Compare types: Spec.Type (Diskful/Access/TieBreaker) vs Status.Type (Diskful/Diskless).
	var typeInSync bool
	switch rvr.Spec.Type {
	case v1alpha1.ReplicaTypeDiskful:
		typeInSync = rvr.Status.Type == v1alpha1.DRBDResourceTypeDiskful
	case v1alpha1.ReplicaTypeAccess, v1alpha1.ReplicaTypeTieBreaker:
		typeInSync = rvr.Status.Type == v1alpha1.DRBDResourceTypeDiskless
	}

	if !typeInSync {
		// Type differs - need role change.
		pending := &v1alpha1.ReplicatedVolumeReplicaStatusDatameshPendingTransition{
			Role: rvr.Spec.Type,
		}
		if rvr.Spec.Type == v1alpha1.ReplicaTypeDiskful {
			pending.LVMVolumeGroupName = rvr.Spec.LVMVolumeGroupName
			pending.ThinPoolName = rvr.Spec.LVMVolumeGroupThinPoolName
		}
		return pending, v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonPendingRoleChange,
			fmt.Sprintf("Waiting to change role to %s", rvr.Spec.Type)
	}

	// Type matches - for Diskful, check backing volume.
	if rvr.Spec.Type == v1alpha1.ReplicaTypeDiskful {
		bvInSync := rvr.Status.BackingVolume != nil &&
			rvr.Status.BackingVolume.LVMVolumeGroupName == rvr.Spec.LVMVolumeGroupName &&
			rvr.Status.BackingVolume.LVMVolumeGroupThinPoolName == rvr.Spec.LVMVolumeGroupThinPoolName
		if !bvInSync {
			// Backing volume differs - need BV change.
			pending := &v1alpha1.ReplicatedVolumeReplicaStatusDatameshPendingTransition{
				LVMVolumeGroupName: rvr.Spec.LVMVolumeGroupName,
				ThinPoolName:       rvr.Spec.LVMVolumeGroupThinPoolName,
			}
			storageDesc := rvr.Spec.LVMVolumeGroupName
			if rvr.Spec.LVMVolumeGroupThinPoolName != "" {
				storageDesc += "/" + rvr.Spec.LVMVolumeGroupThinPoolName
			}
			return pending, v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonPendingBackingVolumeChange,
				fmt.Sprintf("Waiting to change backing volume to %s", storageDesc)
		}
	}

	// Everything is in sync - no pending operation.
	return nil, v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonConfigured,
		"Replica is configured as intended"
}

// applyDatameshPendingTransition applies the target datameshPending to rvr.Status in-place.
// Returns true if the field was changed.
func applyDatameshPendingTransition(rvr *v1alpha1.ReplicatedVolumeReplica, target *v1alpha1.ReplicatedVolumeReplicaStatusDatameshPendingTransition) bool {
	changed := false
	current := rvr.Status.DatameshPendingTransition

	// Handle target == nil case.
	if target == nil {
		if current != nil {
			rvr.Status.DatameshPendingTransition = nil
			changed = true
		}
		return changed
	}

	// Target is not nil — ensure current exists.
	if current == nil {
		rvr.Status.DatameshPendingTransition = &v1alpha1.ReplicatedVolumeReplicaStatusDatameshPendingTransition{}
		current = rvr.Status.DatameshPendingTransition
		changed = true
	}

	// Compare and update each field.
	if !ptr.Equal(current.Member, target.Member) {
		if target.Member != nil {
			current.Member = ptr.To(*target.Member)
		} else {
			current.Member = nil
		}
		changed = true
	}
	if current.Role != target.Role {
		current.Role = target.Role
		changed = true
	}
	if current.LVMVolumeGroupName != target.LVMVolumeGroupName {
		current.LVMVolumeGroupName = target.LVMVolumeGroupName
		changed = true
	}
	if current.ThinPoolName != target.ThinPoolName {
		current.ThinPoolName = target.ThinPoolName
		changed = true
	}

	return changed
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
	targetType v1alpha1.ReplicaType,
) v1alpha1.DRBDResourceSpec {
	// Start from existing spec (preserves immutable and user-controlled fields) or create new.
	var spec v1alpha1.DRBDResourceSpec
	if drbdr != nil {
		spec = *drbdr.Spec.DeepCopy()
	} else {
		spec.NodeName = rvr.Spec.NodeName
		spec.NodeID = rvr.NodeID()
	}

	// Fill mutable fields.
	spec.Type = computeDRBDRType(targetType)
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
		if member.Attached {
			spec.Role = v1alpha1.DRBDRolePrimary
		} else {
			spec.Role = v1alpha1.DRBDRoleSecondary
		}
		spec.AllowTwoPrimaries = datamesh.AllowMultiattach

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

		// Extract NodeID from the member name (validated by API).
		nodeID := m.NodeID()

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

	// Sort peers by Name for deterministic output (in case if datamesh.Members is not ordered by Name).
	slices.SortFunc(peers, func(a, b v1alpha1.DRBDResourcePeer) int {
		return strings.Compare(a.Name, b.Name)
	})

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

// applyDRBDConfiguredCondFalse sets the DRBDConfigured condition to False on RVR.
func applyDRBDConfiguredCondFalse(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

// applyDRBDConfiguredCondTrue sets the DRBDConfigured condition to True on RVR.
func applyDRBDConfiguredCondTrue(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType,
		Status:  metav1.ConditionTrue,
		Reason:  reason,
		Message: message,
	})
}

// applyBackingVolumeReadyCondFalse sets the BackingVolumeReady condition to False on RVR.
func applyBackingVolumeReadyCondFalse(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyType,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

// applyBackingVolumeReadyCondTrue sets the BackingVolumeReady condition to True on RVR.
func applyBackingVolumeReadyCondTrue(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyType,
		Status:  metav1.ConditionTrue,
		Reason:  reason,
		Message: message,
	})
}

// applyBackingVolumeReadyCondAbsent removes the BackingVolumeReady condition from RVR.
func applyBackingVolumeReadyCondAbsent(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
	return obju.RemoveStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyType)
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

// applyRVRAttachment sets Attachment on RVR status.
// If attachment is nil, clears the Attachment field.
func applyRVRAttachment(rvr *v1alpha1.ReplicatedVolumeReplica, attachment *v1alpha1.ReplicatedVolumeReplicaStatusAttachment) bool {
	if ptr.Equal(rvr.Status.Attachment, attachment) {
		return false
	}
	rvr.Status.Attachment = attachment
	return true
}

// applyRVRDRBDRReconciliationCache updates the DRBDResource reconciliation cache
// with the target values computed during this reconciliation.
func applyRVRDRBDRReconciliationCache(
	rvr *v1alpha1.ReplicatedVolumeReplica,
	target v1alpha1.ReplicatedVolumeReplicaStatusDRBDRReconciliationCache,
) bool {
	if rvr.Status.DRBDRReconciliationCache == target {
		return false
	}
	rvr.Status.DRBDRReconciliationCache = target
	return true
}

// ──────────────────────────────────────────────────────────────────────────────
// View types
//

// rspEligibilityView contains pre-fetched RSP data for eligibility checks.
// Used to avoid I/O in ensure helpers.
type rspEligibilityView struct {
	// EligibleNode is a copy of the eligible node entry for the RVR's node.
	// nil if the node is not in the eligible nodes list or RSP was not found.
	EligibleNode *v1alpha1.ReplicatedStoragePoolEligibleNode
	// Type is the RSP type (LVM or LVMThin).
	// Empty if RSP was not found.
	Type v1alpha1.ReplicatedStoragePoolType
}

// storageEligibilityCode represents the result of storage eligibility check.
type storageEligibilityCode int

const (
	storageEligibilityOK                  storageEligibilityCode = iota
	storageEligibilityRSPNotAvailable                            // rspView == nil
	storageEligibilityNodeNotEligible                            // rspView.EligibleNode == nil
	storageEligibilityTypeMismatch                               // ThinPool specified but RSP is LVM (or vice versa)
	storageEligibilityLVGNotEligible                             // LVG not in eligible node's list
	storageEligibilityThinPoolNotEligible                        // LVG found but ThinPool not in list
)

// isStorageEligible checks if the given LVG+ThinPool combination is eligible.
// Returns (code, message) where code != storageEligibilityOK means not eligible.
// Safe to call on nil receiver.
func (v *rspEligibilityView) isStorageEligible(lvgName, thinPool string) (storageEligibilityCode, string) {
	// Check RSP eligibility view availability.
	if v == nil {
		return storageEligibilityRSPNotAvailable, "Waiting for RSP eligibility information"
	}

	// Check if node is eligible in RSP.
	if v.EligibleNode == nil {
		return storageEligibilityNodeNotEligible, "Node is not eligible in RSP"
	}

	// Validate ThinPool presence/absence matches RSP type.
	switch v.Type {
	case v1alpha1.ReplicatedStoragePoolTypeLVM:
		if thinPool != "" {
			return storageEligibilityTypeMismatch,
				fmt.Sprintf("ThinPool %q specified but RSP type is LVM (thick); waiting for correct storage assignment", thinPool)
		}
	case v1alpha1.ReplicatedStoragePoolTypeLVMThin:
		if thinPool == "" {
			return storageEligibilityTypeMismatch,
				"ThinPool not specified but RSP type is LVMThin; waiting for correct storage assignment"
		}
	}

	// Check if storage (LVG, or LVG+ThinPool for LVMThin) is in eligible node's list.
	for _, lvg := range v.EligibleNode.LVMVolumeGroups {
		if lvg.Name == lvgName {
			// For LVM (thick): just check LVG name match.
			// For LVMThin: also check ThinPool name match.
			if v.Type == v1alpha1.ReplicatedStoragePoolTypeLVM {
				return storageEligibilityOK, ""
			}
			if lvg.ThinPoolName == thinPool {
				return storageEligibilityOK, ""
			}
			// LVG found but ThinPool doesn't match.
			return storageEligibilityThinPoolNotEligible,
				fmt.Sprintf("LVG %q found but ThinPool %q is not eligible on node", lvgName, thinPool)
		}
	}

	// LVG not found.
	if v.Type == v1alpha1.ReplicatedStoragePoolTypeLVMThin {
		return storageEligibilityLVGNotEligible,
			fmt.Sprintf("LVG %q with ThinPool %q is not eligible on node", lvgName, thinPool)
	}
	return storageEligibilityLVGNotEligible,
		fmt.Sprintf("LVG %q is not eligible on node", lvgName)
}

// ──────────────────────────────────────────────────────────────────────────────
// Single-call I/O helper categories
//

// --- ReplicatedVolumeReplica (RVR) ---

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

func (r *Reconciler) patchRVR(ctx context.Context, obj, base *v1alpha1.ReplicatedVolumeReplica, optimisticLock bool) error {
	var patch client.Patch
	if optimisticLock {
		patch = client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})
	} else {
		patch = client.MergeFrom(base)
	}
	return r.cl.Patch(ctx, obj, patch)
}

func (r *Reconciler) patchRVRStatus(ctx context.Context, obj, base *v1alpha1.ReplicatedVolumeReplica, optimisticLock bool) error {
	var patch client.Patch
	if optimisticLock {
		patch = client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})
	} else {
		patch = client.MergeFrom(base)
	}
	return r.cl.Status().Patch(ctx, obj, patch)
}

// --- DRBDResource (DRBDR) ---

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

func (r *Reconciler) createDRBDR(ctx context.Context, drbdr *v1alpha1.DRBDResource) error {
	return r.cl.Create(ctx, drbdr)
}

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
	if err := client.IgnoreNotFound(r.cl.Delete(ctx, drbdr)); err != nil {
		return err
	}
	drbdr.DeletionTimestamp = ptr.To(metav1.Now())
	return nil
}

// --- ReplicatedStoragePool (RSP) ---

// getRSPEligibilityView fetches RSP and extracts eligibility data for the given node.
// Returns nil view (not error) if RSP is not found.
// Returns view with nil EligibleNode if node is not in eligible nodes list.
func (r *Reconciler) getRSPEligibilityView(
	ctx context.Context,
	rspName string,
	nodeName string,
) (*rspEligibilityView, error) {
	var unsafeRSP v1alpha1.ReplicatedStoragePool
	if err := r.cl.Get(ctx, client.ObjectKey{Name: rspName}, &unsafeRSP, client.UnsafeDisableDeepCopy); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}

	view := &rspEligibilityView{
		Type: unsafeRSP.Spec.Type,
	}

	// Find and copy the eligible node entry.
	for i := range unsafeRSP.Status.EligibleNodes {
		if unsafeRSP.Status.EligibleNodes[i].NodeName == nodeName {
			// DeepCopy to avoid aliasing with cache (LVMVolumeGroups is a slice).
			view.EligibleNode = unsafeRSP.Status.EligibleNodes[i].DeepCopy()
			break
		}
	}

	return view, nil
}

// --- ReplicatedVolume (RV) ---

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

// --- LVMLogicalVolume (LLV) ---

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

func (r *Reconciler) createLLV(ctx context.Context, llv *snc.LVMLogicalVolume) error {
	return r.cl.Create(ctx, llv)
}

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
	if err := client.IgnoreNotFound(r.cl.Delete(ctx, llv)); err != nil {
		return err
	}
	llv.DeletionTimestamp = ptr.To(metav1.Now())
	return nil
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
