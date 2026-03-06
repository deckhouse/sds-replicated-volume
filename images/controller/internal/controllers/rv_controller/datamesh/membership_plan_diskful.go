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
	addReplica *dmte.RegisteredTransition[*globalContext, *ReplicaContext],
) {
	// ════════════════════════════════════════════════════════════════════════
	// Without sD (A vestibule for odd→even)
	// ════════════════════════════════════════════════════════════════════════

	// AddReplica(D): ✦ → D∅ → D (even→odd voters, no qmr↑)
	addReplica.Plan("diskful/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership).
		ReplicaType(v1alpha1.ReplicaTypeDiskful).
		DisplayName("Adding diskful replica").
		Guards(commonAddGuards...).
		Steps(
			mrStep("✦ → D∅",
				composeApply(
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
		ReplicaType(v1alpha1.ReplicaTypeDiskful).
		DisplayName("Adding diskful replica").
		Guards(commonAddGuards...).
		Steps(
			mrStep("✦ → D∅",
				composeApply(
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
			),
		).
		OnComplete(onJoinCompleteWithBaselineLayoutUpdate).
		Build()

	// AddReplica(D) + q↑: ✦ → A → D∅ + q↑ → D (odd→even, no qmr↑)
	addReplica.Plan("diskful-q-up/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership).
		ReplicaType(v1alpha1.ReplicaTypeDiskful).
		DisplayName("Adding diskful replica").
		Guards(commonAddGuards...).
		Steps(
			mrStep("✦ → A",
				createMember(v1alpha1.DatameshMemberTypeAccess),
				confirmFMPlusSubject,
			),
			mrStep("A → D∅ + q↑",
				composeApply(
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
		ReplicaType(v1alpha1.ReplicaTypeDiskful).
		DisplayName("Adding diskful replica").
		Guards(commonAddGuards...).
		Steps(
			mrStep("✦ → A",
				createMember(v1alpha1.DatameshMemberTypeAccess),
				confirmFMPlusSubject,
			),
			mrStep("A → D∅ + q↑",
				composeApply(
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
			),
		).
		OnComplete(onJoinCompleteWithBaselineLayoutUpdate).
		Build()

	// ════════════════════════════════════════════════════════════════════════
	// With sD (sD pre-sync, requires Flant DRBD)
	// ════════════════════════════════════════════════════════════════════════

	// AddReplica(D) via sD: ✦ → sD∅ → sD → D (even→odd, no qmr↑)
	addReplica.Plan("diskful-via-sd/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership).
		ReplicaType(v1alpha1.ReplicaTypeDiskful).
		DisplayName("Adding diskful replica").
		Guards(commonAddGuards...).
		Guards(guardShadowDiskfulSupported).
		Steps(
			mrStep("✦ → sD∅",
				composeApply(
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
		).
		OnComplete(onJoinComplete).
		Build()

	// AddReplica(D) via sD + qmr↑: ✦ → sD∅ → sD → D → qmr↑ (even→odd, qmr↑)
	addReplica.Plan("diskful-via-sd-qmr-up/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership).
		ReplicaType(v1alpha1.ReplicaTypeDiskful).
		DisplayName("Adding diskful replica").
		Guards(commonAddGuards...).
		Guards(guardShadowDiskfulSupported).
		Steps(
			mrStep("✦ → sD∅",
				composeApply(
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
			),
		).
		OnComplete(onJoinCompleteWithBaselineLayoutUpdate).
		Build()

	// AddReplica(D) via sD + q↑: ✦ → sD∅ → sD → sD∅ → D∅ + q↑ → D (odd→even, no qmr↑)
	addReplica.Plan("diskful-via-sd-q-up/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership).
		ReplicaType(v1alpha1.ReplicaTypeDiskful).
		DisplayName("Adding diskful replica").
		Guards(commonAddGuards...).
		Guards(guardShadowDiskfulSupported).
		Steps(
			mrStep("✦ → sD∅",
				composeApply(
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
				composeApply(
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
	addReplica.Plan("diskful-via-sd-q-up-qmr-up/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership).
		ReplicaType(v1alpha1.ReplicaTypeDiskful).
		DisplayName("Adding diskful replica").
		Guards(commonAddGuards...).
		Guards(guardShadowDiskfulSupported).
		Steps(
			mrStep("✦ → sD∅",
				composeApply(
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
				composeApply(
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
			),
		).
		OnComplete(onJoinCompleteWithBaselineLayoutUpdate).
		Build()
}
