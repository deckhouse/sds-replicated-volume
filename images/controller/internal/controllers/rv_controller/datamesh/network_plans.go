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

// registerNetworkPlans registers RepairNetworkAddresses and ChangeSystemNetworks plans.
func registerNetworkPlans(reg *dmte.Registry[*globalContext, *ReplicaContext]) {
	repairNetworkAddresses := reg.GlobalTransition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeRepairNetworkAddresses)
	changeSystemNetworks := reg.GlobalTransition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeSystemNetworks)

	// RepairNetworkAddresses: sync member addresses from RVR.
	//
	// When this triggers, DRBD is already listening on the new IP/port —
	// connections on the old address are already broken and IO is already
	// frozen. We act aggressively to restore connectivity ASAP.
	repairNetworkAddresses.Plan("repair/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupNetwork).
		DisplayName("Repairing connectivity").
		Guards(guardReplicasMatchTargetNetworks).
		Steps(
			ngStep("Repair",
				repairAddresses,
				confirmAllConnected,
			),
		).
		Build()

	// ChangeSystemNetworks(add): introduce new system networks.
	//
	// Adding networks is always safe for existing connections.
	changeSystemNetworks.Plan("add/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupNetwork).
		Init(setSystemNetworks).
		DisplayName("Adding system networks").
		Guards(guardNodesHaveAddedNetworks).
		Steps(
			ngStep("Listen",
				addNewNetworks,
				confirmAddedAddressesAvailable,
			),
			ngStep("Connect",
				addNewAddresses,
				confirmAllConnectedOnAddedNetworks,
			),
		).
		Build()

	// ChangeSystemNetworks(remove): decommission system networks.
	//
	// Guarded: remaining networks must have full connectivity.
	changeSystemNetworks.Plan("remove/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupNetwork).
		Init(setSystemNetworks).
		DisplayName("Removing system networks").
		Guards(guardRemainingNetworksConnected).
		Steps(
			ngStep("Disconnect",
				removeOldNetworksAndAddresses,
				confirmAllMembers,
			),
		).
		Build()

	// ChangeSystemNetworks(update): add new and remove old simultaneously.
	//
	// Remaining networks (intersection) carry traffic during the transition.
	changeSystemNetworks.Plan("update/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupNetwork).
		Init(setSystemNetworks).
		DisplayName("Updating system networks").
		Guards(guardRemainingNetworksConnected, guardNodesHaveAddedNetworks).
		Steps(
			ngStep("Listen new & Disconnect old",
				composeGlobalApply(
					removeOldNetworksAndAddresses,
					addNewNetworks,
				),
				confirmAddedAddressesAvailable,
			),
			ngStep("Connect",
				addNewAddresses,
				confirmAllConnectedOnAddedNetworks,
			),
		).
		Build()

	// ChangeSystemNetworks(migrate): full replacement (no remaining networks).
	//
	// No connectivity guard — intersection is empty, old network may already
	// be broken. Establishes new connectivity first, then removes old.
	changeSystemNetworks.Plan("migrate/v1").
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupNetwork).
		Init(setSystemNetworks).
		DisplayName("Migrating system networks").
		Guards(guardNodesHaveAddedNetworks).
		Steps(
			ngStep("Listen new",
				addNewNetworks,
				confirmAddedAddressesAvailable,
			),
			ngStep("Connect new",
				addNewAddresses,
				confirmAllConnectedOnAddedNetworks,
			),
			ngStep("Disconnect old",
				removeOldNetworksAndAddresses,
				confirmAllMembers,
			),
		).
		Build()
}
