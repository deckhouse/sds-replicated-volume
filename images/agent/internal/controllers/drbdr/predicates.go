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

package drbdr

import (
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// drbdrPredicates returns predicates for DRBDResource filtering.
// Only resources belonging to the specified node will trigger reconciliation.
func drbdrPredicates(nodeName string) []predicate.Predicate {
	return []predicate.Predicate{
		predicate.Funcs{
			CreateFunc: func(e event.TypedCreateEvent[client.Object]) bool {
				dr, ok := e.Object.(*v1alpha1.DRBDResource)
				return ok && dr.Spec.NodeName == nodeName
			},
			UpdateFunc: func(e event.TypedUpdateEvent[client.Object]) bool {
				dr, ok := e.ObjectNew.(*v1alpha1.DRBDResource)
				return ok && dr.Spec.NodeName == nodeName
			},
			DeleteFunc: func(e event.TypedDeleteEvent[client.Object]) bool {
				dr, ok := e.Object.(*v1alpha1.DRBDResource)
				return ok && dr.Spec.NodeName == nodeName
			},
			GenericFunc: func(_ event.TypedGenericEvent[client.Object]) bool {
				return false
			},
		},
	}
}
