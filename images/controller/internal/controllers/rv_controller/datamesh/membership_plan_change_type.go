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

// registerChangeTypePlans registers ChangeReplicaType plans for non-voter type transitions.
func registerChangeTypePlans(
	changeReplicaType *dmte.RegisteredTransition[*globalContext, *ReplicaContext],
) {
	// ChangeReplicaType(A → TB)
	// Star-to-star role change: FM peers update quorum role, no connection changes.
	// No guards — adding TB role is always safe, and both A and TB are diskless
	// star members, so the transition is safe while attached.
	changeReplicaType.Plan("a-to-tb/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership).
		FromReplicaType(v1alpha1.ReplicaTypeAccess).
		ToReplicaType(v1alpha1.ReplicaTypeTieBreaker).
		DisplayName("Changing replica type").
		Steps(
			mrStep("A → TB",
				setType(v1alpha1.DatameshMemberTypeTieBreaker),
				confirmFMPlusSubject,
			),
		).
		OnComplete(onChangeTypeComplete).
		Build()

	// ChangeReplicaType(TB → A)
	// Star-to-star role change. Guarded: VolumeAccess=Local blocks A replicas,
	// and leaving-TB guards ensure TB coverage is maintained.
	changeReplicaType.Plan("tb-to-a/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership).
		FromReplicaType(v1alpha1.ReplicaTypeTieBreaker).
		ToReplicaType(v1alpha1.ReplicaTypeAccess).
		DisplayName("Changing replica type").
		Guards(guardVolumeAccessNotLocal).
		Guards(leavingTBGuards...).
		Steps(
			mrStep("TB → A",
				setType(v1alpha1.DatameshMemberTypeAccess),
				confirmFMPlusSubject,
			),
		).
		OnComplete(onChangeTypeComplete).
		Build()

	// ChangeReplicaType(A → sD): A → sD∅ → sD
	//
	// Two steps because DRBD bitmap ordering matters (same as AddReplica(sD)):
	//
	// Step 1 (A → sD∅): peers enable bitmaps for this member (and add
	// full-mesh connections). Must happen BEFORE disk attach — DRBD will
	// refuse to attach a disk if peers do not have bitmaps allocated.
	//
	// Step 2 (sD∅ → sD): attaches the disk. Bitmaps are already in place.
	changeReplicaType.Plan("a-to-sd/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership).
		FromReplicaType(v1alpha1.ReplicaTypeAccess).
		ToReplicaType(v1alpha1.ReplicaTypeShadowDiskful).
		DisplayName("Changing replica type").
		Guards(guardShadowDiskfulSupported).
		Steps(
			mrStep("A → sD∅",
				composeApply(
					setType(v1alpha1.DatameshMemberTypeLiminalShadowDiskful),
					setBackingVolumeFromRequest,
				),
				asReplicaConfirm(confirmAllMembers),
			),
			mrStep("sD∅ → sD",
				setType(v1alpha1.DatameshMemberTypeShadowDiskful),
				confirmSubjectOnly,
			),
		).
		OnComplete(onChangeTypeComplete).
		Build()

	// ChangeReplicaType(sD → A): sD → sD∅ → A
	//
	// Two steps — reverse of A → sD:
	//
	// Step 1 (sD → sD∅): detach disk locally. Must happen BEFORE peers
	// disable bitmaps — DRBD will refuse to disable bitmaps for a peer
	// that still has a disk attached.
	//
	// Step 2 (sD∅ → A): peers disable bitmaps (and switch to star
	// connections). Disk is already detached so this succeeds.
	//
	// Also handles transition from sD∅ state (interrupted A→sD): step 1 is a
	// no-op if already liminal.
	changeReplicaType.Plan("sd-to-a/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership).
		FromReplicaType(v1alpha1.ReplicaTypeShadowDiskful).
		ToReplicaType(v1alpha1.ReplicaTypeAccess).
		DisplayName("Changing replica type").
		Guards(guardVolumeAccessNotLocal).
		Steps(
			mrStep("sD → sD∅",
				setType(v1alpha1.DatameshMemberTypeLiminalShadowDiskful),
				confirmSubjectOnly,
			),
			mrStep("sD∅ → A",
				composeApply(
					setType(v1alpha1.DatameshMemberTypeAccess),
					clearBackingVolume,
				),
				asReplicaConfirm(confirmAllMembers),
			),
		).
		OnComplete(onChangeTypeComplete).
		Build()

	// ChangeReplicaType(TB → sD): TB → sD∅ → sD
	//
	// Same bitmap ordering as A → sD:
	// Step 1 (TB → sD∅): peers enable bitmaps. Must happen before disk attach.
	// Step 2 (sD∅ → sD): disk attach.
	//
	// Guarded: feature flag (sD requires Flant DRBD) + leaving-TB guards.
	changeReplicaType.Plan("tb-to-sd/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership).
		FromReplicaType(v1alpha1.ReplicaTypeTieBreaker).
		ToReplicaType(v1alpha1.ReplicaTypeShadowDiskful).
		DisplayName("Changing replica type").
		Guards(guardShadowDiskfulSupported).
		Guards(leavingTBGuards...).
		Steps(
			mrStep("TB → sD∅",
				composeApply(
					setType(v1alpha1.DatameshMemberTypeLiminalShadowDiskful),
					setBackingVolumeFromRequest,
				),
				asReplicaConfirm(confirmAllMembers),
			),
			mrStep("sD∅ → sD",
				setType(v1alpha1.DatameshMemberTypeShadowDiskful),
				confirmSubjectOnly,
			),
		).
		OnComplete(onChangeTypeComplete).
		Build()

	// ChangeReplicaType(sD → TB): sD → sD∅ → TB
	//
	// Same bitmap ordering as sD → A:
	// Step 1 (sD → sD∅): disk detach. Must happen before peers disable bitmaps.
	// Step 2 (sD∅ → TB): peers disable bitmaps.
	//
	// Guarded: VolumeAccess=Local blocks TB (TB cannot serve IO locally).
	// Also handles sD∅ → TB (liminal state, step 1 is no-op).
	changeReplicaType.Plan("sd-to-tb/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership).
		FromReplicaType(v1alpha1.ReplicaTypeShadowDiskful).
		ToReplicaType(v1alpha1.ReplicaTypeTieBreaker).
		DisplayName("Changing replica type").
		Guards(guardVolumeAccessNotLocal).
		Steps(
			mrStep("sD → sD∅",
				setType(v1alpha1.DatameshMemberTypeLiminalShadowDiskful),
				confirmSubjectOnly,
			),
			mrStep("sD∅ → TB",
				composeApply(
					setType(v1alpha1.DatameshMemberTypeTieBreaker),
					clearBackingVolume,
				),
				asReplicaConfirm(confirmAllMembers),
			),
		).
		OnComplete(onChangeTypeComplete).
		Build()
}
