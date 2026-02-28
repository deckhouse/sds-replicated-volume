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

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

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
				if err := r.patchDRBDR(rf.Ctx(), drbdr, base); err != nil {
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

	// 2. ReplicatedVolume not found — stop reconciliation and wait for it to appear.
	// Without RV we cannot determine datamesh state, system networks, peers, etc.
	if rv == nil {
		changed := applyDRBDConfiguredCondUnknown(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonWaitingForReplicatedVolume,
			"ReplicatedVolume not found; waiting for it to be present")
		return drbdr, rf.Continue().ReportChangedIf(changed)
	}

	// 3. Datamesh not initialized yet — wait for RV controller to set it up.
	// Normally datamesh is already initialized by the time RVR is created,
	// but we check for non-standard usage scenarios (e.g., RVR created before RV) and general correctness.
	if rv.Status.DatameshRevision == 0 {
		changed := applyDRBDConfiguredCondUnknown(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonWaitingForReplicatedVolume,
			"Datamesh is not initialized yet; waiting for ReplicatedVolume to initialize it")
		return drbdr, rf.Continue().ReportChangedIf(changed)
	}

	datamesh := &rv.Status.Datamesh

	// 4. No system networks in datamesh — DRBD needs networks to communicate.
	// If datamesh is initialized, system networks should already be set,
	// but we check for non-standard scenarios and general correctness.
	if len(datamesh.SystemNetworkNames) == 0 {
		changed := applyDRBDConfiguredCondUnknown(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonWaitingForReplicatedVolume,
			"No system networks in datamesh; waiting for ReplicatedVolume to configure them")
		return drbdr, rf.Continue().ReportChangedIf(changed)
	}

	// 5. Node not assigned yet — cannot configure DRBD without a node.
	// Note: Per API validation, NodeName is immutable once set. We rely on this invariant below.
	if rvr.Spec.NodeName == "" {
		changed := applyDRBDConfiguredCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonPendingScheduling,
			"Waiting for node assignment")
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
			if apierrors.IsAlreadyExists(err) {
				// Concurrent reconciliation created this DRBDResource. Requeue to pick it up from cache.
				rf.Log().Info("DRBDResource already exists, requeueing", "drbdr", newObj.Name)
				return drbdr, rf.DoneAndRequeue()
			}
			return drbdr, rf.Failf(err, "creating DRBDResource")
		}
		drbdr = newObj

		targetDRBDRReconciliationCache = newDRBDRReconciliationCache(rv.Status.DatameshRevision, drbdr.Generation, targetType)
	} else {
		// We need to check DRBDR spec if any of the tracked values changed since last reconciliation:
		// - DRBDResource.Generation (DRBDR was modified externally)
		// - DatameshRevision (datamesh configuration changed)
		// - TargetType (effective member type changed, e.g. BV appeared)
		targetDRBDRReconciliationCache = newDRBDRReconciliationCache(rv.Status.DatameshRevision, drbdr.Generation, targetType)
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
				if err := r.patchDRBDR(rf.Ctx(), drbdr, base); err != nil {
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
		return drbdr, rf.Continue().ReportChangedIf(changed)
	}

	// 9. Check DRBDR configuration status.
	drbdrConfiguredCond := obju.GetStatusCondition(drbdr, v1alpha1.DRBDResourceCondConfiguredType)

	// 9a. Configured condition not set yet — waiting for agent to respond.
	if drbdrConfiguredCond == nil {
		changed = applyDRBDConfiguredCondUnknown(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonApplyingConfiguration,
			"Waiting for agent to respond (Configured condition is not set yet)") || changed
		return drbdr, rf.Continue().ReportChangedIf(changed)
	}

	// 9b. Agent hasn't processed the current generation yet.
	if drbdrConfiguredCond.ObservedGeneration != drbdr.Generation {
		msg := fmt.Sprintf("Waiting for agent to respond (generation: %d, observedGeneration: %d)",
			drbdr.Generation, drbdrConfiguredCond.ObservedGeneration)
		changed = applyDRBDConfiguredCondUnknown(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonApplyingConfiguration, msg) || changed
		return drbdr, rf.Continue().ReportChangedIf(changed)
	}

	// 9c. DRBDR is in maintenance mode.
	if drbdrConfiguredCond.Reason == v1alpha1.DRBDResourceCondConfiguredReasonInMaintenance {
		changed = applyDRBDConfiguredCondUnknown(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonApplyingConfiguration,
			"DRBD is in maintenance mode") || changed
		return drbdr, rf.Continue().ReportChangedIf(changed)
	}

	// 9d. DRBDR configuration failed.
	if drbdrConfiguredCond.Status != metav1.ConditionTrue {
		msg := fmt.Sprintf("DRBD configuration failed (reason: %s)", drbdrConfiguredCond.Reason)
		changed = applyDRBDConfiguredCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonConfigurationFailed, msg) || changed
		return drbdr, rf.Continue().ReportChangedIf(changed)
	}

	// At this point DRBDR is configured (Configured condition is True).

	// 10. DRBDR is configured — check addresses are populated.
	// Note: We check drbdr.Status.Addresses (the source of truth) rather than
	// rvr.Status.Addresses (the mirror). ensureStatusAddressesAndType copies
	// addresses from DRBDR to RVR later in the same reconcile cycle.
	if len(drbdr.Status.Addresses) == 0 {
		changed = applyDRBDConfiguredCondUnknown(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonApplyingConfiguration,
			"Waiting for DRBD addresses") || changed
		return drbdr, rf.Continue().ReportChangedIf(changed)
	}

	// 11. If targetType != intendedType, DRBDR is not yet configured for the
	// intended type (e.g. backing volume is not ready yet). Wait until the
	// preconditions are met and targetType converges to intendedType.
	if targetType != intendedType {
		changed = applyDRBDConfiguredCondFalse(rvr,
			v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonWaitingForBackingVolume,
			fmt.Sprintf("DRBD preconfigured as %s, waiting for backing volume to become ready", targetType)) || changed
		return drbdr, rf.Continue().ReportChangedIf(changed)
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
			return drbdr, rf.Continue().ReportChangedIf(changed)
		}

		// 12b. Check if backing volume needs resize.
		if targetBV.Size.Cmp(intendedBV.Size) < 0 {
			msg := fmt.Sprintf("Backing volume resize pending: current size %s, expected size %s",
				targetBV.Size.String(), intendedBV.Size.String())
			changed = applyDRBDConfiguredCondFalse(rvr,
				v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonWaitingForBackingVolume, msg) || changed
			return drbdr, rf.Continue().ReportChangedIf(changed)
		}
	}

	// 13. If not a datamesh member.
	if member == nil {
		// If previously a datamesh member (DatameshRevision > 0) but now removed from the
		// datamesh (e.g., during replica deletion), reset DatameshRevision to signal that
		// the replica is no longer a datamesh member. The RV controller uses this to detect
		// that the replica has acknowledged its removal from the datamesh.
		if rvr.Status.DatameshRevision != 0 {
			rvr.Status.DatameshRevision = 0
			changed = true
			changed = applyDRBDConfiguredCondTrue(rvr,
				v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonConfigured,
				"DRBD configured (removed from datamesh)") || changed
			return drbdr, rf.Continue().ReportChangedIf(changed)
		}

		if rvr.DeletionTimestamp != nil {
			// Replica is being deleted, DRBD is configured (standalone, no peers).
			changed = applyDRBDConfiguredCondTrue(rvr,
				v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonConfigured,
				"DRBD configured (replica is being deleted)") || changed
		} else {
			// Never was a datamesh member — DRBD is preconfigured, waiting for membership.
			changed = applyDRBDConfiguredCondFalse(rvr,
				v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonPendingDatameshJoin,
				"DRBD preconfigured, waiting for datamesh membership") || changed
		}
		return drbdr, rf.Continue().ReportChangedIf(changed)
	}

	// Invariant: at this point, for members whose DRBD should be configured with a backing
	// volume (HasBackingVolume), targetBV must equal intendedBV.
	//
	// - No BV needed (!HasBackingVolume): both targetBV and intendedBV are nil — invariant trivially holds.
	// - BV fully ready (HasBackingVolume): backing volume exists, matches intended configuration
	//   (LVG, ThinPool, Size), and is referenced in DRBDR — targetBV equals intendedBV.
	//
	// Any in-progress state (creating, resizing, replacing) would have returned earlier.
	if targetType.HasBackingVolume() && !targetBV.Equal(intendedBV) {
		panic(fmt.Sprintf("BUG: targetBV and intendedBV must be equal for Diskful, got targetBV=%+v, intendedBV=%+v", targetBV, intendedBV))
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
		v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonConfigured, "DRBD fully configured") || changed
	return drbdr, rf.Continue().ReportChangedIf(changed)
}

// computeIntendedType returns the intended type for DRBDR configuration.
// For datamesh members, the type is authoritative from the datamesh.
// For non-members, it is derived from rvr.Spec.Type and represents the goal state;
// DRBDR is configured only partially in this case (e.g. local disk mode without peer connections).
func computeIntendedType(rvr *v1alpha1.ReplicatedVolumeReplica, member *v1alpha1.DatameshMember) v1alpha1.DatameshMemberType {
	if member == nil {
		return v1alpha1.DatameshMemberType(rvr.Spec.Type)
	}

	return member.Type
}

// computeTargetType returns the effective type for DRBDR spec computation.
// It may differ from intendedType when preconditions are not met — currently,
// Diskful without a ready backing volume is downgraded to LiminalDiskful.
func computeTargetType(intendedType v1alpha1.DatameshMemberType, targetLLVName string) v1alpha1.DatameshMemberType {
	if intendedType.HasBackingVolume() && targetLLVName == "" {
		return intendedType.ToLiminal()
	}
	return intendedType
}

// newDRBDRReconciliationCache constructs a reconciliation cache for the current iteration.
func newDRBDRReconciliationCache(
	datameshRevision int64,
	drbdrGeneration int64,
	targetType v1alpha1.DatameshMemberType,
) v1alpha1.ReplicatedVolumeReplicaStatusDRBDRReconciliationCache {
	return v1alpha1.ReplicatedVolumeReplicaStatusDRBDRReconciliationCache{
		DatameshRevision: datameshRevision,
		DRBDRGeneration:  drbdrGeneration,
		TargetType:       targetType,
	}
}

// computeTargetDatameshRequest computes the target datameshPending field based on
// rvr.Spec (intended state), rvr.Status (actual state), rv readiness, and eligibility in RSP.
//
// Returns (target, condReason, condMessage) where:
//   - target is the target datameshPending value (nil means no pending operation)
//   - condReason is the Configured condition reason
//   - condMessage is the Configured condition message
//
// The condReason/condMessage are always set: when target is nil, they indicate

// computeDRBDRType converts DatameshMemberType to DRBDResourceType.
// Diskful → Diskful; LiminalDiskful/TieBreaker/Access → Diskless.
func computeDRBDRType(memberType v1alpha1.DatameshMemberType) v1alpha1.DRBDResourceType {
	if memberType.HasBackingVolume() {
		return v1alpha1.DRBDResourceTypeDiskful
	}
	return v1alpha1.DRBDResourceTypeDiskless
}

// newDRBDR constructs a new DRBDResource with ownerRef and finalizer.
// Spec must already have all fields set (including NodeName and NodeID for DRBDR).
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
// Otherwise creates a new spec with NodeName and NodeID (from rvr.ID()) for DRBDR.
//
// Exception: This helper uses DeepCopy which normally violates ComputeReconcileHelper rules.
// This is intentional: we copy the existing spec to preserve immutable and user-controlled fields
// (NodeName, NodeID (DRBDR field), Maintenance) that we don't want to manually track. The DeepCopy overhead
// is acceptable since this is not a hot path.
func computeTargetDRBDRSpec(
	rvr *v1alpha1.ReplicatedVolumeReplica,
	drbdr *v1alpha1.DRBDResource,
	datamesh *v1alpha1.ReplicatedVolumeDatamesh,
	member *v1alpha1.DatameshMember,
	targetLLVName string,
	targetType v1alpha1.DatameshMemberType,
) v1alpha1.DRBDResourceSpec {
	// Start from existing spec (preserves immutable and user-controlled fields) or create new.
	var spec v1alpha1.DRBDResourceSpec
	if drbdr != nil {
		spec = *drbdr.Spec.DeepCopy()
	} else {
		spec.NodeName = rvr.Spec.NodeName
		spec.NodeID = rvr.ID()
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

	// NonVoting: DRBD non-voting option can only be set on diskful replicas; it
	// makes the replica not count itself in its own quorum calculation (peers use
	// allow-remote-read=false for the other side). Two member types have DRBD
	// configured with a disk: Diskful and ShadowDiskful. Of those, only
	// ShadowDiskful needs non-voting.
	spec.NonVoting = targetType == v1alpha1.DatameshMemberTypeShadowDiskful

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
		spec.AllowTwoPrimaries = datamesh.Multiattach

		// Quorum: non-voters (Access, TieBreaker, ShadowDiskful and their liminal variants)
		// use q=32 (impossibly high) so they can never satisfy the main quorum condition
		// on their own; their quorum is determined entirely by the quorate_peers path.
		// Voters use q from datamesh.
		if !targetType.IsVoter() {
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
//   - Full-mesh members (Diskful, LiminalDiskful, ShadowDiskful, LiminalShadowDiskful)
//     connect to ALL peers. AllowRemoteRead is set based on the peer's role:
//     true for voters and tiebreakers, false for Access and ShadowDiskful peers.
//   - Non-full-mesh members (Access, TieBreaker) connect only to full-mesh peers.
func computeTargetDRBDRPeers(datamesh *v1alpha1.ReplicatedVolumeDatamesh, self *v1alpha1.DatameshMember) []v1alpha1.DRBDResourcePeer {
	if len(datamesh.Members) <= 1 {
		return nil
	}

	selfConnectsToAll := self.Type.ConnectsToAllPeers()

	peers := make([]v1alpha1.DRBDResourcePeer, 0, len(datamesh.Members)-1)
	for i := range datamesh.Members {
		m := &datamesh.Members[i]
		if m.Name == self.Name {
			continue
		}

		peerConnectsToAll := m.Type.ConnectsToAllPeers()

		if !selfConnectsToAll && !peerConnectsToAll {
			// Access/TieBreaker: only connect to full-mesh peers.
			continue
		}

		// AllowRemoteRead is enabled only for voters and tiebreakers (so other peers
		// are not considered in the quorum and tie-breaker mechanisms).
		allowRemoteRead := m.Type.IsVoter() || m.Type == v1alpha1.DatameshMemberTypeTieBreaker

		// Extract ID from the member name (validated by API).
		id := m.ID()

		// Peer type: full-mesh peers get DRBDResourceTypeDiskful; others get Diskless.
		peerType := v1alpha1.DRBDResourceTypeDiskless
		if peerConnectsToAll {
			peerType = v1alpha1.DRBDResourceTypeDiskful
		}

		peers = append(peers, v1alpha1.DRBDResourcePeer{
			Name:            m.Name,
			Type:            peerType,
			AllowRemoteRead: allowRemoteRead,
			NodeID:          id,
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

// applyDRBDConfiguredCondUnknown sets the DRBDConfigured condition to Unknown on RVR.
func applyDRBDConfiguredCondUnknown(rvr *v1alpha1.ReplicatedVolumeReplica, reason, message string) bool {
	return obju.SetStatusCondition(rvr, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType,
		Status:  metav1.ConditionUnknown,
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
