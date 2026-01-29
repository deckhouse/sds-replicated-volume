/*
Copyright 2025 Flant JSC

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

package rvrschedulingcontroller

import (
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func RVRPredicates() []predicate.Predicate {
	return []predicate.Predicate{
		predicate.Funcs{
			CreateFunc:  func(_ event.TypedCreateEvent[client.Object]) bool { return true },
			UpdateFunc:  func(_ event.TypedUpdateEvent[client.Object]) bool { return false },
			DeleteFunc:  func(_ event.TypedDeleteEvent[client.Object]) bool { return false },
			GenericFunc: func(_ event.TypedGenericEvent[client.Object]) bool { return false },
		},
	}
}

// RSPPredicates returns predicates for ReplicatedStoragePool events.
// Reacts to:
//   - Create: always (new RSP may have eligibleNodes)
//   - Update: only if eligibleNodes or usedBy.replicatedStorageClassNames changed
//   - Delete: always (RSP removed)
//   - Generic: always (external triggers)
func RSPPredicates() []predicate.Predicate {
	return []predicate.Predicate{
		predicate.TypedFuncs[client.Object]{
			UpdateFunc: func(e event.TypedUpdateEvent[client.Object]) bool {
				oldRSP, okOld := e.ObjectOld.(*v1alpha1.ReplicatedStoragePool)
				newRSP, okNew := e.ObjectNew.(*v1alpha1.ReplicatedStoragePool)
				if !okOld || !okNew || oldRSP == nil || newRSP == nil {
					return true
				}
				// React if eligibleNodes changed.
				if !v1alpha1.EligibleNodesEqual(oldRSP.Status.EligibleNodes, newRSP.Status.EligibleNodes) {
					return true
				}
				// React if usedBy.replicatedStorageClassNames changed.
				if !sets.NewString(oldRSP.Status.UsedBy.ReplicatedStorageClassNames...).
					Equal(sets.NewString(newRSP.Status.UsedBy.ReplicatedStorageClassNames...)) {
					return true
				}
				return false
			},
		},
	}
}
