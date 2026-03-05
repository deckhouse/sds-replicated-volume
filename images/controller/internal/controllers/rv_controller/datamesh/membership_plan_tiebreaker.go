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
		Steps(
			dmte.ReplicaStep("✦ → TB", applyCreateMember(v1alpha1.DatameshMemberTypeTieBreaker), confirmFMPlusSubject).
				DiagnosticConditions(v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType),
		).
		OnComplete(onJoinComplete).
		Build()

	// RemoveReplica(TB): TB → ✕
	removeReplica.Plan("tiebreaker/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership).
		ReplicaType(v1alpha1.ReplicaTypeTieBreaker).
		DisplayName("Leaving datamesh").
		Guards(commonRemoveGuards...).
		Guards(leavingTBGuards...).
		Steps(
			dmte.ReplicaStep("TB → ✕", applyRemoveMember, confirmFMPlusSubjectLeaving).
				DiagnosticConditions(v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType),
		).
		OnComplete(onLeaveComplete).
		Build()
}
