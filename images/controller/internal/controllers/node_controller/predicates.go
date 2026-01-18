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
	"slices"

	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// NodePredicates returns predicates for Node events.
// Reacts to:
//   - Create: always
//   - Update: only if AgentNodeLabelKey presence/absence changed
//   - Delete: never
func NodePredicates() []predicate.Predicate {
	return []predicate.Predicate{
		predicate.Funcs{
			UpdateFunc: func(e event.TypedUpdateEvent[client.Object]) bool {
				// Only react if AgentNodeLabelKey presence/absence changed.
				_, oldHas := e.ObjectOld.GetLabels()[v1alpha1.AgentNodeLabelKey]
				_, newHas := e.ObjectNew.GetLabels()[v1alpha1.AgentNodeLabelKey]

				return oldHas != newHas
			},
			DeleteFunc: func(_ event.TypedDeleteEvent[client.Object]) bool {
				// Node deletions are not interesting for this controller.
				return false
			},
		},
	}
}

// RSCPredicates returns predicates for ReplicatedStorageClass events.
// Reacts to:
//   - Create: always
//   - Update: only if nodeLabelSelector or zones changed
//   - Delete: always
func RSCPredicates() []predicate.Predicate {
	return []predicate.Predicate{
		predicate.Funcs{
			UpdateFunc: func(e event.TypedUpdateEvent[client.Object]) bool {
				oldRSC, okOld := e.ObjectOld.(*v1alpha1.ReplicatedStorageClass)
				newRSC, okNew := e.ObjectNew.(*v1alpha1.ReplicatedStorageClass)
				if !okOld || !okNew || oldRSC == nil || newRSC == nil {
					return true
				}

				// React if nodeLabelSelector changed.
				if !apiequality.Semantic.DeepEqual(
					oldRSC.Spec.NodeLabelSelector,
					newRSC.Spec.NodeLabelSelector,
				) {
					return true
				}

				// React if zones changed.
				if !slices.Equal(oldRSC.Spec.Zones, newRSC.Spec.Zones) {
					return true
				}

				return false
			},
		},
	}
}

// DRBDResourcePredicates returns predicates for DRBDResource events.
// Reacts to:
//   - Create: always (new resource appeared on a node)
//   - Update: never (nodeName is immutable, other fields don't affect decision)
//   - Delete: always (resource removed from a node)
func DRBDResourcePredicates() []predicate.Predicate {
	return []predicate.Predicate{
		predicate.Funcs{
			UpdateFunc: func(_ event.TypedUpdateEvent[client.Object]) bool {
				// nodeName is immutable, other fields don't affect label decisions.
				return false
			},
		},
	}
}
