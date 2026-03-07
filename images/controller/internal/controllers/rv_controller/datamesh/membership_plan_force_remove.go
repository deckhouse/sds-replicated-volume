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

// registerForceRemovePlans registers all ForceRemoveReplica plan variants.
//
// Emergency single-step plans. The dead member is removed immediately without
// intermediate states. CancelActiveOnCreate cancels any in-flight transition
// for the dead member. After removeMember, the dead member is nil → excluded
// from confirmAllMembers MustConfirm automatically.
//
// Guards: guardNotAttached (must ForceDetach first) + guardMemberUnreachable
// (prevents accidental force-removal of a reachable member).
// No leavingDGuards — emergency bypasses preconditions.
func registerForceRemovePlans(
	forceRemove *dmte.RegisteredTransition[*globalContext, *ReplicaContext],
) {
	// ════════════════════════════════════════════════════════════════════════
	// Non-voter force removal
	// ════════════════════════════════════════════════════════════════════════

	// ForceRemoveReplica(A): A → ✕
	forceRemove.Plan("access/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupEmergency).
		ReplicaType(v1alpha1.ReplicaTypeAccess).
		DisplayName("Force-removing replica").
		CancelActiveOnCreate(true).
		Guards(forceRemoveGuards...).
		Steps(
			mrStep("Force remove",
				removeMember,
				asReplicaConfirm(confirmAllMembers),
			),
		).
		OnComplete(onForceRemoveComplete).
		Build()

	// ForceRemoveReplica(TB): TB → ✕
	forceRemove.Plan("tiebreaker/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupEmergency).
		ReplicaType(v1alpha1.ReplicaTypeTieBreaker).
		DisplayName("Force-removing replica").
		CancelActiveOnCreate(true).
		Guards(forceRemoveGuards...).
		Steps(
			mrStep("Force remove",
				removeMember,
				asReplicaConfirm(confirmAllMembers),
			),
		).
		OnComplete(onForceRemoveComplete).
		Build()

	// ForceRemoveReplica(sD): sD → ✕ (also handles sD∅)
	forceRemove.Plan("shadow-diskful/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupEmergency).
		ReplicaType(v1alpha1.ReplicaTypeShadowDiskful).
		DisplayName("Force-removing replica").
		CancelActiveOnCreate(true).
		Guards(forceRemoveGuards...).
		Steps(
			mrStep("Force remove",
				removeMember,
				asReplicaConfirm(confirmAllMembers),
			),
		).
		OnComplete(onForceRemoveComplete).
		Build()

	// ════════════════════════════════════════════════════════════════════════
	// Voter force removal
	// ════════════════════════════════════════════════════════════════════════

	// ForceRemoveReplica(D): D → ✕ (odd→even voters, no q↓)
	// Also handles D∅. No qmr change → no baseline update needed.
	forceRemove.Plan("diskful/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupEmergency).
		ReplicaType(v1alpha1.ReplicaTypeDiskful).
		DisplayName("Force-removing replica").
		CancelActiveOnCreate(true).
		Guards(forceRemoveGuards...).
		Guards(guardVotersOdd).
		Steps(
			mrStep("Force remove",
				removeMember,
				asReplicaConfirm(confirmAllMembers),
			),
		).
		OnComplete(onForceRemoveComplete).
		Build()

	// ForceRemoveReplica(D) + q↓: D → ✕ + q↓ (even→odd voters)
	// Also handles D∅. No qmr change → no baseline update needed.
	forceRemove.Plan("diskful-q-down/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupEmergency).
		ReplicaType(v1alpha1.ReplicaTypeDiskful).
		DisplayName("Force-removing replica").
		CancelActiveOnCreate(true).
		Guards(forceRemoveGuards...).
		Guards(guardVotersEven).
		Steps(
			mrStep("Force remove",
				composeReplicaApply(
					removeMember,
					asReplicaApply(lowerQ),
				),
				asReplicaConfirm(confirmAllMembers),
			),
		).
		OnComplete(onForceRemoveComplete).
		Build()
}
