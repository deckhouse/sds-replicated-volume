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

// ──────────────────────────────────────────────────────────────────────────────
// Plan registration
//

// registerNetworkPlans registers RepairNetworkAddresses plans.
func registerNetworkPlans(reg *dmte.Registry[*globalContext, *ReplicaContext]) {
	repair := reg.GlobalTransition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeRepairNetworkAddresses)

	// RepairNetworkAddresses: sync member addresses from RVR.
	//
	// When this triggers, DRBD is already listening on the new IP/port —
	// connections on the old address are already broken and IO is already
	// frozen. So we act aggressively: replace member addresses immediately
	// to restore connectivity as fast as possible.
	//
	// Guarded: all replicas' RVR addresses must match the datamesh target
	// network list before repair can proceed (ensures stable network set).
	// Confirms when all expected peer connections are verified (at least
	// one side with a ready agent reports Connected).
	repair.Plan("repair/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupNetwork).
		DisplayName("Repairing connectivity").
		Guards(guardReplicasMatchTargetNetworks).
		Steps(
			ngStep("Repair",
				repairAddresses,
				confirmAllMembersConnected,
			),
		).
		Build()
}
