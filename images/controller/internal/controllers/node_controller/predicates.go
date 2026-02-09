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

package nodecontroller

import (
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// nodePredicates returns predicates for Node events.
// Reacts to:
//   - Create: always
//   - Update: only if AgentNodeLabelKey presence/absence changed
//   - Delete: never
func nodePredicates() []predicate.Predicate {
	return []predicate.Predicate{
		predicate.TypedFuncs[client.Object]{
			UpdateFunc: func(e event.TypedUpdateEvent[client.Object]) bool {
				// React if AgentNodeLabelKey presence, absence, or value changed.
				oldVal, oldHas := e.ObjectOld.GetLabels()[v1alpha1.AgentNodeLabelKey]
				newVal, newHas := e.ObjectNew.GetLabels()[v1alpha1.AgentNodeLabelKey]

				return oldHas != newHas || oldVal != newVal
			},
			DeleteFunc: func(_ event.TypedDeleteEvent[client.Object]) bool {
				// Node deletions are not interesting for this controller.
				return false
			},
		},
	}
}

// rspPredicates returns predicates for ReplicatedStoragePool events.
// Reacts to:
//   - Create: always (new RSP may have eligibleNodes)
//   - Update: only if eligibleNodes changed
//   - Delete: always (RSP removed, nodes may need label removed)
func rspPredicates() []predicate.Predicate {
	return []predicate.Predicate{
		predicate.TypedFuncs[client.Object]{
			UpdateFunc: func(e event.TypedUpdateEvent[client.Object]) bool {
				oldRSP, okOld := e.ObjectOld.(*v1alpha1.ReplicatedStoragePool)
				newRSP, okNew := e.ObjectNew.(*v1alpha1.ReplicatedStoragePool)
				if !okOld || !okNew || oldRSP == nil || newRSP == nil {
					return true
				}

				// React only if eligibleNodes changed.
				return !eligibleNodesEqual(oldRSP.Status.EligibleNodes, newRSP.Status.EligibleNodes)
			},
		},
	}
}

// eligibleNodesEqual compares two eligibleNodes slices by node names only.
//
// Uses EligibleNodesSortedIndex to handle potentially unsorted input safely.
// In the common case (already sorted), this adds only O(n) overhead with zero allocations.
func eligibleNodesEqual(a, b []v1alpha1.ReplicatedStoragePoolEligibleNode) bool {
	if len(a) != len(b) {
		return false
	}

	// Build sorted indices — O(n) if already sorted, O(n log n) otherwise.
	aIdx := v1alpha1.NewEligibleNodesSortedIndex(a)
	bIdx := v1alpha1.NewEligibleNodesSortedIndex(b)

	for i := range aIdx.Len() {
		if aIdx.NodeName(i) != bIdx.NodeName(i) {
			return false
		}
	}
	return true
}

// drbdResourcePredicates returns predicates for DRBDResource events.
// Reacts to:
//   - Create: always (new resource appeared on a node)
//   - Update: never (nodeName is immutable, other fields don't affect decision)
//   - Delete: always (resource removed from a node)
func drbdResourcePredicates() []predicate.Predicate {
	return []predicate.Predicate{
		predicate.TypedFuncs[client.Object]{
			UpdateFunc: func(_ event.TypedUpdateEvent[client.Object]) bool {
				// nodeName is immutable, other fields don't affect label decisions.
				return false
			},
		},
	}
}
