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
	"k8s.io/utils/ptr"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

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
			InUse:       drbdr.Status.DeviceOpen != nil && *drbdr.Status.DeviceOpen,
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
	rvNamePrefix := rvr.Spec.ReplicatedVolumeName + "-"
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

		// Guard: foreign peer (peer not belonging to this ReplicatedVolume).
		// DRBDR spec.peers is derived from RV datamesh members, so foreign peers should never
		// appear. If they do, it indicates a serious misconfiguration. This check also ensures
		// that ID() (which extracts the numeric suffix from peer name) works correctly.
		if !strings.HasPrefix(src.Name, rvNamePrefix) {
			return ef.Errf("foreign peer detected: %s", src.Name)
		}

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
		if dst.ReplicationState != src.ReplicationState {
			dst.ReplicationState = src.ReplicationState
			changed = true
		}
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

	return ef.Ok().ReportChangedIf(changed)
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

	// Count connected Diskful/TieBreaker/UpToDate peers.
	for i := range rvr.Status.Peers {
		p := &rvr.Status.Peers[i]
		if p.ConnectionState != v1alpha1.ConnectionStateConnected {
			continue
		}
		switch p.Type {
		case v1alpha1.ReplicaTypeDiskful:
			summary.ConnectedDiskfulPeers++
		case v1alpha1.ReplicaTypeTieBreaker:
			summary.ConnectedTieBreakerPeers++
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

// ensureStatusDatameshRequestAndConfiguredCond determines what datamesh transition is pending
// (join, leave, role change, or backing volume change) by comparing rvr.Spec to rvr.Status,
// and updates status.datameshPending and the Configured condition accordingly.
func ensureStatusDatameshRequestAndConfiguredCond(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	rv *v1alpha1.ReplicatedVolume,
	rspView *rspEligibilityView,
) (outcome flow.EnsureOutcome) {
	ef := flow.BeginEnsure(ctx, "status-datamesh-pending-and-configured-cond")
	defer ef.OnEnd(&outcome)

	changed := false

	// Compute target (single call for both status fields).
	target, condReason, condMessage := computeTargetDatameshRequest(rvr, rv, rspView)

	// Append RV datamesh pending transition message to condition message if there's a pending transition.
	if target != nil && rv != nil {
		for i := range rv.Status.DatameshReplicaRequests {
			if rv.Status.DatameshReplicaRequests[i].Name == rvr.Name {
				if msg := rv.Status.DatameshReplicaRequests[i].Message; msg != "" {
					condMessage += ": " + msg
				}
				break
			}
		}
	}

	// Apply datameshPending.
	changed = applyDatameshRequest(rvr, target) || changed

	// Apply Configured condition.
	if condReason == "" {
		changed = applyConfiguredCondAbsent(rvr) || changed
	} else {
		switch condReason {
		case v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonConfigured:
			changed = applyConfiguredCondTrue(rvr, condReason, condMessage) || changed
		case v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonWaitingForReplicatedVolume:
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

func computeTargetDatameshRequest(
	rvr *v1alpha1.ReplicatedVolumeReplica,
	rv *v1alpha1.ReplicatedVolume,
	rspView *rspEligibilityView,
) (target *v1alpha1.DatameshMembershipRequest, condReason, condMessage string) {
	// Check if being deleted.
	if rvr.DeletionTimestamp != nil {
		if rvr.Status.DatameshRevision != 0 {
			// Currently a datamesh member - need to leave.
			return &v1alpha1.DatameshMembershipRequest{
					Operation: v1alpha1.DatameshMembershipRequestOperationLeave,
				}, v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonPendingLeave,
				"Deletion in progress, waiting to leave datamesh"
		}
		// Not a member - nothing to do (condition will be removed).
		return nil, "", ""
	}

	// Check rv/datamesh prerequisites (more fundamental than node scheduling).
	if rv == nil {
		return nil, v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonWaitingForReplicatedVolume,
			"ReplicatedVolume not found; waiting for it to be present"
	}
	if rv.Status.DatameshRevision == 0 {
		return nil, v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonWaitingForReplicatedVolume,
			"Datamesh is not initialized yet; waiting for ReplicatedVolume to initialize it"
	}
	if rv.Status.Configuration == nil {
		return nil, v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonWaitingForReplicatedVolume,
			"ReplicatedVolume has no configuration yet; waiting for it to appear"
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
		return nil, v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonWaitingForReplicatedVolume,
			"ReplicatedStoragePool not found; waiting for it to be present"
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
				reason = v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonWaitingForReplicatedVolume
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
		req := &v1alpha1.DatameshMembershipRequest{
			Operation: v1alpha1.DatameshMembershipRequestOperationJoin,
			Type:      rvr.Spec.Type,
		}
		if rvr.Spec.Type == v1alpha1.ReplicaTypeDiskful {
			req.LVMVolumeGroupName = rvr.Spec.LVMVolumeGroupName
			req.ThinPoolName = rvr.Spec.LVMVolumeGroupThinPoolName
		}
		return req, v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonPendingJoin,
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
		req := &v1alpha1.DatameshMembershipRequest{
			Operation: v1alpha1.DatameshMembershipRequestOperationChangeRole,
			Type:      rvr.Spec.Type,
		}
		if rvr.Spec.Type == v1alpha1.ReplicaTypeDiskful {
			req.LVMVolumeGroupName = rvr.Spec.LVMVolumeGroupName
			req.ThinPoolName = rvr.Spec.LVMVolumeGroupThinPoolName
		}
		return req, v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonPendingRoleChange,
			fmt.Sprintf("Waiting to change role to %s", rvr.Spec.Type)
	}

	// Type matches - for Diskful, check backing volume.
	if rvr.Spec.Type == v1alpha1.ReplicaTypeDiskful {
		bvInSync := rvr.Status.BackingVolume != nil &&
			rvr.Status.BackingVolume.LVMVolumeGroupName == rvr.Spec.LVMVolumeGroupName &&
			rvr.Status.BackingVolume.LVMVolumeGroupThinPoolName == rvr.Spec.LVMVolumeGroupThinPoolName
		if !bvInSync {
			// Backing volume differs - need BV change.
			req := &v1alpha1.DatameshMembershipRequest{
				Operation:          v1alpha1.DatameshMembershipRequestOperationChangeBackingVolume,
				LVMVolumeGroupName: rvr.Spec.LVMVolumeGroupName,
				ThinPoolName:       rvr.Spec.LVMVolumeGroupThinPoolName,
			}
			storageDesc := rvr.Spec.LVMVolumeGroupName
			if rvr.Spec.LVMVolumeGroupThinPoolName != "" {
				storageDesc += "/" + rvr.Spec.LVMVolumeGroupThinPoolName
			}
			return req, v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonPendingBackingVolumeChange,
				fmt.Sprintf("Waiting to change backing volume to %s", storageDesc)
		}
	}

	// Everything is in sync - no pending operation.
	return nil, v1alpha1.ReplicatedVolumeReplicaCondConfiguredReasonConfigured,
		"Replica is configured as intended"
}

// applyDatameshRequest applies the target datameshPending to rvr.Status in-place.

func applyDatameshRequest(rvr *v1alpha1.ReplicatedVolumeReplica, target *v1alpha1.DatameshMembershipRequest) bool {
	changed := false
	current := rvr.Status.DatameshRequest

	// Handle target == nil case.
	if target == nil {
		if current != nil {
			rvr.Status.DatameshRequest = nil
			changed = true
		}
		return changed
	}

	// Target is not nil — ensure current exists.
	if current == nil {
		rvr.Status.DatameshRequest = &v1alpha1.DatameshMembershipRequest{}
		current = rvr.Status.DatameshRequest
		changed = true
	}

	// Compare and update each field.
	if current.Operation != target.Operation {
		current.Operation = target.Operation
		changed = true
	}
	if current.Type != target.Type {
		current.Type = target.Type
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

func applyRVRAttachment(rvr *v1alpha1.ReplicatedVolumeReplica, attachment *v1alpha1.ReplicatedVolumeReplicaStatusAttachment) bool {
	if ptr.Equal(rvr.Status.Attachment, attachment) {
		return false
	}
	rvr.Status.Attachment = attachment
	return true
}
