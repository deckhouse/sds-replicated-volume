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
		Guards(guardShadowDiskfulSupported, guardMaxDiskMembers).
		Steps(
			mrStep("A → sD∅",
				composeReplicaApply(
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
				composeReplicaApply(
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
		Guards(guardShadowDiskfulSupported, guardMaxDiskMembers).
		Guards(leavingTBGuards...).
		Steps(
			mrStep("TB → sD∅",
				composeReplicaApply(
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
				composeReplicaApply(
					setType(v1alpha1.DatameshMemberTypeTieBreaker),
					clearBackingVolume,
				),
				asReplicaConfirm(confirmAllMembers),
			),
		).
		OnComplete(onChangeTypeComplete).
		Build()

	// ════════════════════════════════════════════════════════════════════════
	// sD ↔ D (voter promotion/demotion, requires Flant DRBD)
	// ════════════════════════════════════════════════════════════════════════

	// ChangeReplicaType(sD → D): sD → D (even→odd, hot promotion)
	//
	// Non-voter becomes voter. All peers update arr/voting config.
	// No BV changes (both sD and D have backing volume).
	// No q change (even→odd voters).
	changeReplicaType.Plan("sd-to-d/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership).
		FromReplicaType(v1alpha1.ReplicaTypeShadowDiskful).
		ToReplicaType(v1alpha1.ReplicaTypeDiskful).
		DisplayName("Changing replica type").
		Guards(guardShadowDiskfulSupported, guardVotersEven).
		Steps(
			mrStep("sD → D",
				setType(v1alpha1.DatameshMemberTypeDiskful),
				asReplicaConfirm(confirmAllMembers),
			),
		).
		OnComplete(onChangeTypeComplete).
		Build()

	// ChangeReplicaType(sD → D) + q↑: sD → sD∅ → D∅ + q↑ → D (odd→even)
	//
	// Same detach-before-promote as AddReplica(D) via sD + q↑ — see
	// membership_plan_diskful.go for rationale (async apply safety).
	// No BV changes — BV already present from sD throughout.
	changeReplicaType.Plan("sd-to-d-q-up/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership).
		FromReplicaType(v1alpha1.ReplicaTypeShadowDiskful).
		ToReplicaType(v1alpha1.ReplicaTypeDiskful).
		DisplayName("Changing replica type").
		Guards(guardShadowDiskfulSupported, guardVotersOdd).
		Steps(
			mrStep("sD → sD∅",
				setType(v1alpha1.DatameshMemberTypeLiminalShadowDiskful),
				confirmSubjectOnly,
			),
			mrStep("sD∅ → D∅ + q↑",
				composeReplicaApply(
					setType(v1alpha1.DatameshMemberTypeLiminalDiskful),
					asReplicaApply(raiseQ),
				),
				asReplicaConfirm(confirmAllMembers),
			),
			mrStep("D∅ → D",
				setType(v1alpha1.DatameshMemberTypeDiskful),
				confirmSubjectOnly,
			),
		).
		OnComplete(onChangeTypeComplete).
		Build()

	// ChangeReplicaType(D → sD): D → sD (odd→even, hot demotion)
	//
	// Voter becomes non-voter. All peers update arr/voting config.
	// No BV changes (both D and sD have backing volume).
	// No q change (odd→even voters).
	changeReplicaType.Plan("d-to-sd/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership).
		FromReplicaType(v1alpha1.ReplicaTypeDiskful).
		ToReplicaType(v1alpha1.ReplicaTypeShadowDiskful).
		DisplayName("Changing replica type").
		Guards(leavingDGuards...).
		Guards(guardShadowDiskfulSupported, guardVotersOdd).
		Steps(
			mrStep("D → sD",
				setType(v1alpha1.DatameshMemberTypeShadowDiskful),
				asReplicaConfirm(confirmAllMembers),
			),
		).
		OnComplete(onChangeTypeComplete).
		Build()

	// ChangeReplicaType(D → sD) + q↓: D → D∅ → sD∅ + q↓ → sD (even→odd)
	//
	// Mirror of sD→D+q↑: D detaches, converts to sD∅+q↓ (voter→non-voter
	// + q lowered atomically), re-attaches as sD with delta resync.
	// No BV changes — BV present throughout.
	// No qmr change → no baseline update needed.
	changeReplicaType.Plan("d-to-sd-q-down/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership).
		FromReplicaType(v1alpha1.ReplicaTypeDiskful).
		ToReplicaType(v1alpha1.ReplicaTypeShadowDiskful).
		DisplayName("Changing replica type").
		Guards(leavingDGuards...).
		Guards(guardShadowDiskfulSupported, guardVotersEven).
		Steps(
			mrStep("D → D∅",
				setType(v1alpha1.DatameshMemberTypeLiminalDiskful),
				confirmSubjectOnly,
			),
			mrStep("D∅ → sD∅ + q↓",
				composeReplicaApply(
					setType(v1alpha1.DatameshMemberTypeLiminalShadowDiskful),
					asReplicaApply(lowerQ),
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

	// ════════════════════════════════════════════════════════════════════════
	// A ↔ D (voter promotion/demotion from/to Access)
	// ════════════════════════════════════════════════════════════════════════

	// ChangeReplicaType(A → D): A → D∅ → D (even→odd, no sD)
	//
	// A (star, non-voter) becomes D∅ (full-mesh, voter) with BV set,
	// then disk attaches. No qmr change → no baseline update.
	changeReplicaType.Plan("a-to-d/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership).
		FromReplicaType(v1alpha1.ReplicaTypeAccess).
		ToReplicaType(v1alpha1.ReplicaTypeDiskful).
		DisplayName("Changing replica type").
		Guards(guardVotersEven, guardMaxDiskMembers).
		Steps(
			mrStep("A → D∅",
				composeReplicaApply(
					setType(v1alpha1.DatameshMemberTypeLiminalDiskful),
					setBackingVolumeFromRequest,
				),
				asReplicaConfirm(confirmAllMembers),
			),
			mrStep("D∅ → D",
				setType(v1alpha1.DatameshMemberTypeDiskful),
				confirmSubjectOnly,
			),
		).
		OnComplete(onChangeTypeComplete).
		Build()

	// ChangeReplicaType(A → D) + q↑: A → D∅ + q↑ → D (odd→even, no sD)
	changeReplicaType.Plan("a-to-d-q-up/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership).
		FromReplicaType(v1alpha1.ReplicaTypeAccess).
		ToReplicaType(v1alpha1.ReplicaTypeDiskful).
		DisplayName("Changing replica type").
		Guards(guardVotersOdd, guardMaxDiskMembers).
		Steps(
			mrStep("A → D∅ + q↑",
				composeReplicaApply(
					setType(v1alpha1.DatameshMemberTypeLiminalDiskful),
					setBackingVolumeFromRequest,
					asReplicaApply(raiseQ),
				),
				asReplicaConfirm(confirmAllMembers),
			),
			mrStep("D∅ → D",
				setType(v1alpha1.DatameshMemberTypeDiskful),
				confirmSubjectOnly,
			),
		).
		OnComplete(onChangeTypeComplete).
		Build()

	// ChangeReplicaType(A → D) via sD: A → sD∅ → sD → D (even→odd, sD)
	//
	// sD pre-syncs data invisibly before voter promotion.
	// No BV changes after A→sD∅ (BV set once, preserved throughout).
	changeReplicaType.Plan("a-to-d-via-sd/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership).
		FromReplicaType(v1alpha1.ReplicaTypeAccess).
		ToReplicaType(v1alpha1.ReplicaTypeDiskful).
		DisplayName("Changing replica type").
		Guards(guardVotersEven, guardShadowDiskfulSupported, guardMaxDiskMembers).
		Steps(
			mrStep("A → sD∅",
				composeReplicaApply(
					setType(v1alpha1.DatameshMemberTypeLiminalShadowDiskful),
					setBackingVolumeFromRequest,
				),
				asReplicaConfirm(confirmAllMembers),
			),
			mrStep("sD∅ → sD",
				setType(v1alpha1.DatameshMemberTypeShadowDiskful),
				confirmSubjectOnly,
			),
			// sD → D: non-voter becomes voter. All peers update config.
			mrStep("sD → D",
				setType(v1alpha1.DatameshMemberTypeDiskful),
				asReplicaConfirm(confirmAllMembers),
			),
		).
		OnComplete(onChangeTypeComplete).
		Build()

	// ChangeReplicaType(A → D) via sD + q↑: A → sD∅ → sD → sD∅ → D∅ + q↑ → D (odd→even, sD)
	//
	// Same detach-before-promote as diskful-via-sd-q-up — see
	// membership_plan_diskful.go for rationale (async apply safety).
	// No BV changes after A→sD∅ — BV present throughout.
	changeReplicaType.Plan("a-to-d-via-sd-q-up/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership).
		FromReplicaType(v1alpha1.ReplicaTypeAccess).
		ToReplicaType(v1alpha1.ReplicaTypeDiskful).
		DisplayName("Changing replica type").
		Guards(guardVotersOdd, guardShadowDiskfulSupported, guardMaxDiskMembers).
		Steps(
			mrStep("A → sD∅",
				composeReplicaApply(
					setType(v1alpha1.DatameshMemberTypeLiminalShadowDiskful),
					setBackingVolumeFromRequest,
				),
				asReplicaConfirm(confirmAllMembers),
			),
			mrStep("sD∅ → sD",
				setType(v1alpha1.DatameshMemberTypeShadowDiskful),
				confirmSubjectOnly,
			),
			mrStep("sD → sD∅",
				setType(v1alpha1.DatameshMemberTypeLiminalShadowDiskful),
				confirmSubjectOnly,
			),
			mrStep("sD∅ → D∅ + q↑",
				composeReplicaApply(
					setType(v1alpha1.DatameshMemberTypeLiminalDiskful),
					asReplicaApply(raiseQ),
				),
				asReplicaConfirm(confirmAllMembers),
			),
			mrStep("D∅ → D",
				setType(v1alpha1.DatameshMemberTypeDiskful),
				confirmSubjectOnly,
			),
		).
		OnComplete(onChangeTypeComplete).
		Build()

	// ChangeReplicaType(D → A): D → D∅ → A (odd→even, no q change)
	//
	// Voter becomes non-voter (Access). BV cleared on D∅→A.
	// No qmr change → no baseline update needed.
	// Guarded: VolumeAccess=Local blocks (A not allowed in Local mode).
	changeReplicaType.Plan("d-to-a/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership).
		FromReplicaType(v1alpha1.ReplicaTypeDiskful).
		ToReplicaType(v1alpha1.ReplicaTypeAccess).
		DisplayName("Changing replica type").
		Guards(leavingDGuards...).
		Guards(guardVolumeAccessNotLocal, guardVotersOdd).
		Steps(
			mrStep("D → D∅",
				setType(v1alpha1.DatameshMemberTypeLiminalDiskful),
				confirmSubjectOnly,
			),
			mrStep("D∅ → A",
				composeReplicaApply(
					setType(v1alpha1.DatameshMemberTypeAccess),
					clearBackingVolume,
				),
				asReplicaConfirm(confirmAllMembers),
			),
		).
		OnComplete(onChangeTypeComplete).
		Build()

	// ChangeReplicaType(D → A) + q↓: D → D∅ → A + q↓ (even→odd)
	//
	// Same vestibule pattern as RemoveReplica(D)+q↓, but ends at A.
	changeReplicaType.Plan("d-to-a-q-down/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership).
		FromReplicaType(v1alpha1.ReplicaTypeDiskful).
		ToReplicaType(v1alpha1.ReplicaTypeAccess).
		DisplayName("Changing replica type").
		Guards(leavingDGuards...).
		Guards(guardVolumeAccessNotLocal, guardVotersEven).
		Steps(
			mrStep("D → D∅",
				setType(v1alpha1.DatameshMemberTypeLiminalDiskful),
				confirmSubjectOnly,
			),
			mrStep("D∅ → A + q↓",
				composeReplicaApply(
					setType(v1alpha1.DatameshMemberTypeAccess),
					clearBackingVolume,
					asReplicaApply(lowerQ),
				),
				asReplicaConfirm(confirmAllMembers),
			),
		).
		OnComplete(onChangeTypeComplete).
		Build()

	// ════════════════════════════════════════════════════════════════════════
	// TB ↔ D (voter promotion/demotion from/to TieBreaker)
	// ════════════════════════════════════════════════════════════════════════
	//
	// Structurally identical to A↔D. Key difference: TB→D has leavingTBGuards
	// (TB leaving its role — if TB required for tiebreaker, guard blocks;
	// user adds another TB first, then converts).

	// ChangeReplicaType(TB → D): TB → D∅ → D (even→odd, no sD)
	changeReplicaType.Plan("tb-to-d/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership).
		FromReplicaType(v1alpha1.ReplicaTypeTieBreaker).
		ToReplicaType(v1alpha1.ReplicaTypeDiskful).
		DisplayName("Changing replica type").
		Guards(leavingTBGuards...).
		Guards(guardVotersEven, guardMaxDiskMembers).
		Steps(
			mrStep("TB → D∅",
				composeReplicaApply(
					setType(v1alpha1.DatameshMemberTypeLiminalDiskful),
					setBackingVolumeFromRequest,
				),
				asReplicaConfirm(confirmAllMembers),
			),
			mrStep("D∅ → D",
				setType(v1alpha1.DatameshMemberTypeDiskful),
				confirmSubjectOnly,
			),
		).
		OnComplete(onChangeTypeComplete).
		Build()

	// ChangeReplicaType(TB → D) + q↑: TB → D∅ + q↑ → D (odd→even, no sD)
	changeReplicaType.Plan("tb-to-d-q-up/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership).
		FromReplicaType(v1alpha1.ReplicaTypeTieBreaker).
		ToReplicaType(v1alpha1.ReplicaTypeDiskful).
		DisplayName("Changing replica type").
		Guards(leavingTBGuards...).
		Guards(guardVotersOdd, guardMaxDiskMembers).
		Steps(
			mrStep("TB → D∅ + q↑",
				composeReplicaApply(
					setType(v1alpha1.DatameshMemberTypeLiminalDiskful),
					setBackingVolumeFromRequest,
					asReplicaApply(raiseQ),
				),
				asReplicaConfirm(confirmAllMembers),
			),
			mrStep("D∅ → D",
				setType(v1alpha1.DatameshMemberTypeDiskful),
				confirmSubjectOnly,
			),
		).
		OnComplete(onChangeTypeComplete).
		Build()

	// ChangeReplicaType(TB → D) via sD: TB → sD∅ → sD → D (even→odd, sD)
	changeReplicaType.Plan("tb-to-d-via-sd/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership).
		FromReplicaType(v1alpha1.ReplicaTypeTieBreaker).
		ToReplicaType(v1alpha1.ReplicaTypeDiskful).
		DisplayName("Changing replica type").
		Guards(leavingTBGuards...).
		Guards(guardVotersEven, guardShadowDiskfulSupported, guardMaxDiskMembers).
		Steps(
			mrStep("TB → sD∅",
				composeReplicaApply(
					setType(v1alpha1.DatameshMemberTypeLiminalShadowDiskful),
					setBackingVolumeFromRequest,
				),
				asReplicaConfirm(confirmAllMembers),
			),
			mrStep("sD∅ → sD",
				setType(v1alpha1.DatameshMemberTypeShadowDiskful),
				confirmSubjectOnly,
			),
			mrStep("sD → D",
				setType(v1alpha1.DatameshMemberTypeDiskful),
				asReplicaConfirm(confirmAllMembers),
			),
		).
		OnComplete(onChangeTypeComplete).
		Build()

	// ChangeReplicaType(TB → D) via sD + q↑: TB → sD∅ → sD → sD∅ → D∅ + q↑ → D (odd→even, sD)
	// Same detach-before-promote — see membership_plan_diskful.go.
	changeReplicaType.Plan("tb-to-d-via-sd-q-up/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership).
		FromReplicaType(v1alpha1.ReplicaTypeTieBreaker).
		ToReplicaType(v1alpha1.ReplicaTypeDiskful).
		DisplayName("Changing replica type").
		Guards(leavingTBGuards...).
		Guards(guardVotersOdd, guardShadowDiskfulSupported, guardMaxDiskMembers).
		Steps(
			mrStep("TB → sD∅",
				composeReplicaApply(
					setType(v1alpha1.DatameshMemberTypeLiminalShadowDiskful),
					setBackingVolumeFromRequest,
				),
				asReplicaConfirm(confirmAllMembers),
			),
			mrStep("sD∅ → sD",
				setType(v1alpha1.DatameshMemberTypeShadowDiskful),
				confirmSubjectOnly,
			),
			mrStep("sD → sD∅",
				setType(v1alpha1.DatameshMemberTypeLiminalShadowDiskful),
				confirmSubjectOnly,
			),
			mrStep("sD∅ → D∅ + q↑",
				composeReplicaApply(
					setType(v1alpha1.DatameshMemberTypeLiminalDiskful),
					asReplicaApply(raiseQ),
				),
				asReplicaConfirm(confirmAllMembers),
			),
			mrStep("D∅ → D",
				setType(v1alpha1.DatameshMemberTypeDiskful),
				confirmSubjectOnly,
			),
		).
		OnComplete(onChangeTypeComplete).
		Build()

	// ChangeReplicaType(D → TB): D → D∅ → TB (odd→even, no q change)
	changeReplicaType.Plan("d-to-tb/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership).
		FromReplicaType(v1alpha1.ReplicaTypeDiskful).
		ToReplicaType(v1alpha1.ReplicaTypeTieBreaker).
		DisplayName("Changing replica type").
		Guards(leavingDGuards...).
		Guards(guardVolumeAccessNotLocal, guardVotersOdd).
		Steps(
			mrStep("D → D∅",
				setType(v1alpha1.DatameshMemberTypeLiminalDiskful),
				confirmSubjectOnly,
			),
			mrStep("D∅ → TB",
				composeReplicaApply(
					setType(v1alpha1.DatameshMemberTypeTieBreaker),
					clearBackingVolume,
				),
				asReplicaConfirm(confirmAllMembers),
			),
		).
		OnComplete(onChangeTypeComplete).
		Build()

	// ChangeReplicaType(D → TB) + q↓: D → D∅ → TB + q↓ (even→odd)
	changeReplicaType.Plan("d-to-tb-q-down/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership).
		FromReplicaType(v1alpha1.ReplicaTypeDiskful).
		ToReplicaType(v1alpha1.ReplicaTypeTieBreaker).
		DisplayName("Changing replica type").
		Guards(leavingDGuards...).
		Guards(guardVolumeAccessNotLocal, guardVotersEven).
		Steps(
			mrStep("D → D∅",
				setType(v1alpha1.DatameshMemberTypeLiminalDiskful),
				confirmSubjectOnly,
			),
			mrStep("D∅ → TB + q↓",
				composeReplicaApply(
					setType(v1alpha1.DatameshMemberTypeTieBreaker),
					clearBackingVolume,
					asReplicaApply(lowerQ),
				),
				asReplicaConfirm(confirmAllMembers),
			),
		).
		OnComplete(onChangeTypeComplete).
		Build()
}
