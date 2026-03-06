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

// IMPORTANT: PlanID versioning
//
// PlanIDs are persisted in rv.Status.DatameshTransitions. Changing a plan
// in a way that breaks in-flight transitions requires a NEW version:
//   - Step composition changed (added, removed, reordered)
//   - Step apply semantics changed (different mutations)
//
// Safe changes (no new version needed):
//   - Guards, confirm, DisplayName, diagnostics, OnComplete
//
// To introduce a new version: keep the old version registered (settle-only),
// register the new version, update the dispatcher to yield the new PlanID.

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
		Guards(guardShadowDiskfulSupported, guardMaxDiskMembers).
		Steps(
			mrStep("✦ → sD∅",
				composeReplicaApply(
					createMember(v1alpha1.DatameshMemberTypeLiminalShadowDiskful),
					setBackingVolumeFromRequest,
				),
				asReplicaConfirm(confirmAllMembers),
			),
			mrStep("sD∅ → sD",
				setType(v1alpha1.DatameshMemberTypeShadowDiskful),
				confirmSubjectOnly,
			),
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
			mrStep("sD → ✕",
				removeMember,
				confirmAllMembersLeaving,
			),
		).
		OnComplete(onLeaveComplete).
		Build()
}
