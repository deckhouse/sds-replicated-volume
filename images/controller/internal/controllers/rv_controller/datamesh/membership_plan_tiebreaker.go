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

// registerTieBreakerPlans registers AddReplica(TB) and RemoveReplica(TB) plans.
func registerTieBreakerPlans(
	addReplica, removeReplica *dmte.RegisteredTransition[*globalContext, *ReplicaContext],
) {
	// AddReplica(TB): ✦ → TB
	// No TB-specific guards — adding a TB is always safe.
	addReplica.Plan("tiebreaker/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership).
		ReplicaType(v1alpha1.ReplicaTypeTieBreaker).
		DisplayName("Joining datamesh").
		Guards(commonAddGuards...).
		Guards(gainTBGuards...).
		Steps(
			mrStep("✦ → TB",
				createMember(v1alpha1.DatameshMemberTypeTieBreaker),
				confirmFMPlusSubject,
			),
		).
		OnComplete(onJoinComplete).
		Build()

	// RemoveReplica(TB): TB → ✕
	removeReplica.Plan("tiebreaker/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership).
		ReplicaType(v1alpha1.ReplicaTypeTieBreaker).
		DisplayName("Leaving datamesh").
		Guards(commonRemoveGuards...).
		Guards(loseTBGuards...).
		Steps(
			mrStep("TB → ✕",
				removeMember,
				confirmFMPlusSubjectLeaving,
			),
		).
		OnComplete(onLeaveComplete).
		Build()
}
