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
	"iter"
	"slices"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/dmte"
)

// networkDispatcher returns a dmte.DispatchFunc that handles two network
// transition types:
//
//  1. RepairNetworkAddresses — detects address mismatches (missing, stale,
//     wrong IP/port) and dispatches repair. Takes priority.
//  2. ChangeSystemNetworks — detects diff between datamesh and RSP system
//     network names and dispatches the appropriate plan (add/remove/update/migrate).
//
// Both are in the Network group (serialized). Repair is dispatched first;
// CSN is skipped when repair is needed (tracker would block anyway).
func networkDispatcher() dmte.DispatchFunc[provider] {
	return func(cp provider) iter.Seq[dmte.DispatchDecision] {
		return func(yield func(dmte.DispatchDecision) bool) {
			gctx := cp.Global()

			// 1. RepairNetworkAddresses — address-level repair takes priority.
			if needsAddressRepair(gctx) {
				yield(dmte.DispatchGlobal(
					v1alpha1.ReplicatedVolumeDatameshTransitionTypeRepairNetworkAddresses,
					"repair/v1",
				))
				return // Network group is serialized; repair takes priority.
			}

			// 2. ChangeSystemNetworks — system network set changes.
			if gctx.rsp == nil {
				return
			}
			current := gctx.datamesh.systemNetworkNames
			target := gctx.rsp.GetSystemNetworkNames()

			// Compute diff.
			var hasAdded, hasRemoved, hasRemaining bool
			for _, net := range target {
				if !slices.Contains(current, net) {
					hasAdded = true
					break
				}
			}
			for _, net := range current {
				if slices.Contains(target, net) {
					hasRemaining = true
				} else {
					hasRemoved = true
				}
			}

			// Nothing to do.
			if !hasAdded && !hasRemoved {
				return
			}

			// Select plan.
			var planID dmte.PlanID
			switch {
			case hasAdded && hasRemoved && !hasRemaining:
				planID = "migrate/v1"
			case hasAdded && hasRemoved:
				planID = "update/v1"
			case hasAdded:
				planID = "add/v1"
			default:
				planID = "remove/v1"
			}

			yield(dmte.DispatchGlobal(
				v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeSystemNetworks,
				planID,
			))
		}
	}
}
