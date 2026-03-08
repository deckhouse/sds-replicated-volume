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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/dmte"
)

// ──────────────────────────────────────────────────────────────────────────────
// Plan registration
//

// registerAccessPlans registers AddReplica(A) and RemoveReplica(A) plans.
func registerAccessPlans(
	addReplica, removeReplica *dmte.RegisteredTransition[*globalContext, *ReplicaContext],
) {
	// AddReplica(A): ✦ → A
	addReplica.Plan("access/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership).
		Init(setReplicaType(v1alpha1.ReplicaTypeAccess)).
		DisplayName("Joining datamesh").
		Guards(commonAddGuards...).
		Guards(guardVolumeAccessNotLocal).
		Steps(
			mrStep("✦ → A",
				createMember(v1alpha1.DatameshMemberTypeAccess),
				confirmFMPlusSubject,
			).
				DiagnosticSkipError(skipPendingDatameshJoin),
		).
		OnComplete(onJoinComplete).
		Build()

	// RemoveReplica(A): A → ✕
	removeReplica.Plan("access/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership).
		Init(setReplicaType(v1alpha1.ReplicaTypeAccess)).
		DisplayName("Leaving datamesh").
		Guards(commonRemoveGuards...).
		Steps(
			mrStep("A → ✕",
				removeMember,
				confirmFMPlusSubjectLeaving,
			),
		).
		OnComplete(onLeaveComplete).
		Build()
}

// ──────────────────────────────────────────────────────────────────────────────
// Access-specific guards
//

// guardVolumeAccessNotLocal blocks if VolumeAccess is Local (Access replicas
// are not allowed in Local mode).
func guardVolumeAccessNotLocal(gctx *globalContext, _ *ReplicaContext) dmte.GuardResult {
	if gctx.configuration.VolumeAccess == v1alpha1.VolumeAccessLocal {
		return dmte.GuardResult{
			Blocked: true,
			Message: "Will not join datamesh: volumeAccess is Local",
		}
	}
	return dmte.GuardResult{}
}

// ──────────────────────────────────────────────────────────────────────────────
// Access-specific helpers
//

// skipPendingDatameshJoin allows ignoring DRBDConfigured=False with reason
// PendingDatameshJoin on the subject replica during AddReplica transitions.
// The subject replica has not joined the datamesh yet — this is expected.
func skipPendingDatameshJoin(rctx *ReplicaContext, id uint8, cond *metav1.Condition) bool {
	return id == rctx.ID() && cond.Reason == v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonPendingDatameshJoin
}
