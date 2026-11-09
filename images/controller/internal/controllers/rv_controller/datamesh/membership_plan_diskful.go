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
// Recorded exceptions (apply changed, version kept; the reason is at the
// callback):
//   - raiseQMR, the qmr↑ step of the *-qmr-up/v1 plans.
//
// To introduce a new version: keep the old version registered (settle-only),
// register the new version, update the dispatcher to yield the new PlanID.

package datamesh

import (
	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/dmte"
)

// registerDiskfulPlans registers all AddReplica(D) plan variants.
//
// 8 plans covering all combinations of:
//   - Voter parity: even→odd (no q↑) vs odd→even (q↑ needed)
//   - ShadowDiskful: via A vestibule vs via sD pre-sync
//   - qmr↑: baseline GMDR < target GMDR
//
// All D plans start by creating the member as liminal (D∅ or sD∅):
// peers must enable bitmaps BEFORE disk attach — DRBD will refuse to
// attach a disk if peers do not have bitmaps allocated. For odd→even
// voter plans (q↑ variants): an A or sD vestibule is created first,
// connections are established while quorum is unaffected, then converted
// to D∅ + q↑ in a single config push. sD variants pre-sync data
// invisibly before promotion, reducing the D∅ resync from hours to a
// delta resync (seconds).
func registerDiskfulPlans(
	addReplica, removeReplica *dmte.RegisteredTransition[*globalContext, *ReplicaContext],
) {
	// ════════════════════════════════════════════════════════════════════════
	// Without sD (A vestibule for odd→even)
	// ════════════════════════════════════════════════════════════════════════

	// AddReplica(D): ✦ → D∅ → D (even→odd voters, no qmr↑)
	addReplica.Plan("diskful/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership).
		Init(setReplicaType(v1alpha1.ReplicaTypeDiskful)).
		DisplayName("Adding diskful replica").
		Guards(commonAddGuards...).
		Guards(gainVoterGuards...).
		Guards(guardVotersEven, guardMaxDiskMembers).
		Steps(
			mrStep("✦ → D∅",
				dmte.ComposeReplicaApply(
					createMember(v1alpha1.DatameshMemberTypeLiminalDiskful),
					setBackingVolumeFromRequest,
				),
				asReplicaConfirm(confirmAllMembers),
			),
			mrStep("D∅ → D",
				setType(v1alpha1.DatameshMemberTypeDiskful),
				confirmSubjectOnly,
			),
		).
		OnComplete(onJoinComplete).
		Build()

	// AddReplica(D) + qmr↑: ✦ → D∅ → D → qmr↑ (even→odd, qmr↑)
	addReplica.Plan("diskful-qmr-up/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership).
		Init(setReplicaType(v1alpha1.ReplicaTypeDiskful)).
		DisplayName("Adding diskful replica").
		Guards(commonAddGuards...).
		Guards(gainVoterGuards...).
		Guards(guardVotersEven, guardQMRRaiseNeeded, guardMaxDiskMembers).
		Steps(
			mrStep("✦ → D∅",
				dmte.ComposeReplicaApply(
					createMember(v1alpha1.DatameshMemberTypeLiminalDiskful),
					setBackingVolumeFromRequest,
				),
				asReplicaConfirm(confirmAllMembers),
			),
			mrStep("D∅ → D",
				setType(v1alpha1.DatameshMemberTypeDiskful),
				confirmSubjectOnly,
			),
			mgStep("qmr↑",
				raiseQMR,
				confirmAllMembers,
			).OnComplete(dmte.AdaptApplyToOnComplete(updateBaselineGMDR)),
		).
		OnComplete(onJoinComplete).
		Build()

	// AddReplica(D) + q↑: ✦ → A → D∅ + q↑ → D (odd→even, no qmr↑)
	addReplica.Plan("diskful-q-up/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership).
		Init(setReplicaType(v1alpha1.ReplicaTypeDiskful)).
		DisplayName("Adding diskful replica").
		Guards(commonAddGuards...).
		Guards(gainVoterGuards...).
		Guards(guardVotersOdd, guardMaxDiskMembers).
		Steps(
			mrStep("✦ → A",
				createMember(v1alpha1.DatameshMemberTypeAccess),
				confirmFMPlusSubjectConnected,
			),
			mrStep("A → D∅ + q↑",
				dmte.ComposeReplicaApply(
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
		OnComplete(onJoinComplete).
		Build()

	// AddReplica(D) + q↑ + qmr↑: ✦ → A → D∅ + q↑ → D → qmr↑ (odd→even, qmr↑)
	addReplica.Plan("diskful-q-up-qmr-up/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership).
		Init(setReplicaType(v1alpha1.ReplicaTypeDiskful)).
		DisplayName("Adding diskful replica").
		Guards(commonAddGuards...).
		Guards(gainVoterGuards...).
		Guards(guardVotersOdd, guardQMRRaiseNeeded, guardMaxDiskMembers).
		Steps(
			mrStep("✦ → A",
				createMember(v1alpha1.DatameshMemberTypeAccess),
				confirmFMPlusSubjectConnected,
			),
			mrStep("A → D∅ + q↑",
				dmte.ComposeReplicaApply(
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
			mgStep("qmr↑",
				raiseQMR,
				confirmAllMembers,
			).OnComplete(dmte.AdaptApplyToOnComplete(updateBaselineGMDR)),
		).
		OnComplete(onJoinComplete).
		Build()

	// ════════════════════════════════════════════════════════════════════════
	// With sD (sD pre-sync, requires Flant DRBD)
	// ════════════════════════════════════════════════════════════════════════

	// AddReplica(D) via sD: ✦ → sD∅ → sD → D (even→odd, no qmr↑)
	addReplica.Plan("diskful-via-sd/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership).
		Init(setReplicaType(v1alpha1.ReplicaTypeDiskful)).
		DisplayName("Adding diskful replica").
		Guards(commonAddGuards...).
		Guards(gainVoterGuards...).
		Guards(guardVotersEven, guardShadowDiskfulSupported, guardMaxDiskMembers).
		Steps(
			mrStep("✦ → sD∅",
				dmte.ComposeReplicaApply(
					createMember(v1alpha1.DatameshMemberTypeLiminalShadowDiskful),
					setBackingVolumeFromRequest,
				),
				asReplicaConfirm(confirmAllMembers),
			),
			mrStep("sD∅ → sD",
				setType(v1alpha1.DatameshMemberTypeShadowDiskful),
				confirmSubjectOnly,
			),
			// sD → D: non-voter becomes voter. All peers must update
			// connection config (arr, voting) — unlike disk attach (D∅→D),
			// this is not automatic. No qmr change → no baseline update.
			mrStep("sD → D",
				setType(v1alpha1.DatameshMemberTypeDiskful),
				asReplicaConfirm(confirmAllMembers),
			),
		).
		OnComplete(onJoinComplete).
		Build()

	// AddReplica(D) via sD + qmr↑: ✦ → sD∅ → sD → D → qmr↑ (even→odd, qmr↑)
	addReplica.Plan("diskful-via-sd-qmr-up/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership).
		Init(setReplicaType(v1alpha1.ReplicaTypeDiskful)).
		DisplayName("Adding diskful replica").
		Guards(commonAddGuards...).
		Guards(gainVoterGuards...).
		Guards(guardVotersEven, guardQMRRaiseNeeded, guardShadowDiskfulSupported, guardMaxDiskMembers).
		Steps(
			mrStep("✦ → sD∅",
				dmte.ComposeReplicaApply(
					createMember(v1alpha1.DatameshMemberTypeLiminalShadowDiskful),
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
			mgStep("qmr↑",
				raiseQMR,
				confirmAllMembers,
			).OnComplete(dmte.AdaptApplyToOnComplete(updateBaselineGMDR)),
		).
		OnComplete(onJoinComplete).
		Build()

	// AddReplica(D) via sD + q↑: ✦ → sD∅ → sD → sD∅ → D∅ + q↑ → D (odd→even, no qmr↑)
	//
	// sD pre-syncs data invisibly (steps 1-2). Then the critical sequence:
	//
	// sD→D direct promotion CANNOT be used here. Adding a voter to odd voters
	// makes them even, and with even voters the old (low) q allows a symmetric
	// partition — split-brain. q MUST be raised together with the voter addition.
	//
	// But datamesh is a target configuration that asynchronous agents apply in
	// unpredictable order. Any change we publish must be safe regardless of
	// which replicas apply it first. Publishing sD→D + q↑ in one revision is
	// unsafe: a peer might see the new voter before raising q, creating a
	// window with even voters and old q.
	//
	// Solution: detach disk (step 3: sD→sD∅), then sD∅→D∅+q↑ (step 4).
	// Both sD∅ and D∅ are diskless — the change is a pure config update
	// (voter status + q) that is safe in any application order. Then D∅→D
	// (step 5) re-attaches with a delta resync (seconds, not hours — data
	// already synced during the sD phase).
	addReplica.Plan("diskful-via-sd-q-up/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership).
		Init(setReplicaType(v1alpha1.ReplicaTypeDiskful)).
		DisplayName("Adding diskful replica").
		Guards(commonAddGuards...).
		Guards(gainVoterGuards...).
		Guards(guardVotersOdd, guardShadowDiskfulSupported, guardMaxDiskMembers).
		Steps(
			mrStep("✦ → sD∅",
				dmte.ComposeReplicaApply(
					createMember(v1alpha1.DatameshMemberTypeLiminalShadowDiskful),
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
				dmte.ComposeReplicaApply(
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
		OnComplete(onJoinComplete).
		Build()

	// AddReplica(D) via sD + q↑ + qmr↑: ✦ → sD∅ → sD → sD∅ → D∅ + q↑ → D → qmr↑ (odd→even, qmr↑)
	// Same sD detach-before-promote sequence as diskful-via-sd-q-up — see comment above.
	addReplica.Plan("diskful-via-sd-q-up-qmr-up/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership).
		Init(setReplicaType(v1alpha1.ReplicaTypeDiskful)).
		DisplayName("Adding diskful replica").
		Guards(commonAddGuards...).
		Guards(gainVoterGuards...).
		Guards(guardVotersOdd, guardQMRRaiseNeeded, guardShadowDiskfulSupported, guardMaxDiskMembers).
		Steps(
			mrStep("✦ → sD∅",
				dmte.ComposeReplicaApply(
					createMember(v1alpha1.DatameshMemberTypeLiminalShadowDiskful),
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
				dmte.ComposeReplicaApply(
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
			mgStep("qmr↑",
				raiseQMR,
				confirmAllMembers,
			).OnComplete(dmte.AdaptApplyToOnComplete(updateBaselineGMDR)),
		).
		OnComplete(onJoinComplete).
		Build()

	// ════════════════════════════════════════════════════════════════════════
	// RemoveReplica(D)
	// ════════════════════════════════════════════════════════════════════════
	//
	// D → D∅ detaches disk (subject-only confirm). For even→odd voter plans
	// (q↓ variants): D∅ → A + q↓ uses the A vestibule — the mirror of the
	// add path (A → D∅ + q↑). D∅ (voter, full-mesh) becomes A (non-voter,
	// star) with q lowered in one revision. A is invisible to quorum, so
	// the change is safe regardless of which agents apply it first. Then
	// A → ✕ is a simple star member removal. BV fields are cleared on D∅→A.
	//
	// qmr↓ is always the FIRST step — must relax quorum constraint before
	// removing the voter.

	// RemoveReplica(D): D → D∅ → ✕ (odd→even voters, no qmr↓)
	removeReplica.Plan("remove-diskful/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership).
		Init(setReplicaType(v1alpha1.ReplicaTypeDiskful)).
		DisplayName("Removing diskful replica").
		Guards(removeDiskfulGuards...).
		Guards(guardVotersOdd).
		Steps(
			mrStep("D → D∅",
				setType(v1alpha1.DatameshMemberTypeLiminalDiskful),
				confirmSubjectOnly,
			),
			mrStep("D∅ → ✕",
				removeMember,
				confirmAllMembersLeaving,
			),
		).
		OnComplete(onLeaveComplete).
		Build()

	// qmr↓ + RemoveReplica(D): qmr↓ → D → D∅ → ✕ (odd→even, qmr↓)
	removeReplica.Plan("remove-diskful-qmr-down/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership).
		Init(setReplicaType(v1alpha1.ReplicaTypeDiskful)).
		DisplayName("Removing diskful replica").
		Guards(removeDiskfulGuards...).
		Guards(guardVotersOdd, guardQMRLowerNeeded).
		Steps(
			mgStep("qmr↓",
				dmte.ComposeGlobalApply(
					lowerQMR,
					updateBaselineGMDR,
				),
				confirmAllMembers,
			),
			mrStep("D → D∅",
				setType(v1alpha1.DatameshMemberTypeLiminalDiskful),
				confirmSubjectOnly,
			),
			mrStep("D∅ → ✕",
				removeMember,
				confirmAllMembersLeaving,
			),
		).
		OnComplete(onLeaveComplete).
		Build()

	// RemoveReplica(D) + q↓: D → D∅ → A + q↓ → ✕ (even→odd, no qmr↓)
	removeReplica.Plan("remove-diskful-q-down/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership).
		Init(setReplicaType(v1alpha1.ReplicaTypeDiskful)).
		DisplayName("Removing diskful replica").
		Guards(removeDiskfulGuards...).
		Guards(guardVotersEven).
		Steps(
			mrStep("D → D∅",
				setType(v1alpha1.DatameshMemberTypeLiminalDiskful),
				confirmSubjectOnly,
			),
			mrStep("D∅ → A + q↓",
				dmte.ComposeReplicaApply(
					setType(v1alpha1.DatameshMemberTypeAccess),
					clearBackingVolume,
					asReplicaApply(lowerQ),
				),
				asReplicaConfirm(confirmAllMembers),
			),
			mrStep("A → ✕",
				removeMember,
				confirmFMPlusSubjectLeaving,
			),
		).
		OnComplete(onLeaveComplete).
		Build()

	// qmr↓ + RemoveReplica(D) + q↓: qmr↓ → D → D∅ → A + q↓ → ✕ (even→odd, qmr↓)
	removeReplica.Plan("remove-diskful-qmr-down-q-down/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership).
		Init(setReplicaType(v1alpha1.ReplicaTypeDiskful)).
		DisplayName("Removing diskful replica").
		Guards(removeDiskfulGuards...).
		Guards(guardVotersEven, guardQMRLowerNeeded).
		Steps(
			mgStep("qmr↓",
				dmte.ComposeGlobalApply(
					lowerQMR,
					updateBaselineGMDR,
				),
				confirmAllMembers,
			),
			mrStep("D → D∅",
				setType(v1alpha1.DatameshMemberTypeLiminalDiskful),
				confirmSubjectOnly,
			),
			mrStep("D∅ → A + q↓",
				dmte.ComposeReplicaApply(
					setType(v1alpha1.DatameshMemberTypeAccess),
					clearBackingVolume,
					asReplicaApply(lowerQ),
				),
				asReplicaConfirm(confirmAllMembers),
			),
			mrStep("A → ✕",
				removeMember,
				confirmFMPlusSubjectLeaving,
			),
		).
		OnComplete(onLeaveComplete).
		Build()
}
