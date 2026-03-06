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

package datamesh

import (
	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/dmte"
)

// registerShadowDiskfulPlans registers AddReplica(sD) and RemoveReplica(sD) plans.
func registerShadowDiskfulPlans(
	addReplica, removeReplica *dmte.RegisteredTransition[*globalContext, *ReplicaContext],
) {
	// AddReplica(sD): ✦ → sD∅ → sD
	//
	// Two steps because DRBD bitmap ordering matters:
	//
	// Step 1 (✦ → sD∅): creates a liminal member. Peers enable bitmaps
	// (and add full-mesh connections) for this member BEFORE it attaches
	// its disk. DRBD will refuse to attach a disk if peers do not have
	// bitmaps allocated.
	//
	// Step 2 (sD∅ → sD): attaches the disk. Bitmaps are already in place.
	addReplica.Plan("shadow-diskful/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership).
		ReplicaType(v1alpha1.ReplicaTypeShadowDiskful).
		DisplayName("Joining datamesh").
		Guards(commonAddGuards...).
		Guards(guardShadowDiskfulSupported).
		Steps(
			dmte.ReplicaStep("✦ → sD∅",
				applyCreateMember(v1alpha1.DatameshMemberTypeLiminalShadowDiskful),
				asReplicaConfirm(confirmAllMembers),
			).DiagnosticConditions(v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType),
			dmte.ReplicaStep("sD∅ → sD",
				applySetType(v1alpha1.DatameshMemberTypeShadowDiskful),
				confirmSubjectOnly,
			).DiagnosticConditions(v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType),
		).
		OnComplete(onJoinComplete).
		Build()

	// RemoveReplica(sD): sD → ✕ (single step)
	//
	// Unlike AddReplica, removal does not need an intermediate liminal state.
	// The member is removed from the datamesh in one step; peers drop connections
	// and bitmaps, and the leaving member detaches its disk — order does not
	// matter because DRBD handles disconnection gracefully.
	// Also handles removal from sD∅ state (member already liminal).
	removeReplica.Plan("shadow-diskful/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership).
		ReplicaType(v1alpha1.ReplicaTypeShadowDiskful).
		DisplayName("Leaving datamesh").
		Guards(commonRemoveGuards...).
		Steps(
			dmte.ReplicaStep("sD → ✕",
				applyRemoveMember,
				confirmAllMembersLeaving,
			).DiagnosticConditions(v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType),
		).
		OnComplete(onLeaveComplete).
		Build()
}
