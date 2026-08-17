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
	"slices"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// drbdrPredicates returns predicates for DRBDResource filtering.
// Only resources belonging to the specified node will trigger reconciliation.
// Reacts to:
//   - Generation changes (spec updates)
//   - DeletionTimestamp changes (start of deletion)
//   - Finalizers changes
//
// Pure status updates are ignored to avoid reconcile loops.
func drbdrPredicates(nodeName string) []predicate.Predicate {
	return []predicate.Predicate{
		predicate.Funcs{
			CreateFunc: func(e event.TypedCreateEvent[client.Object]) bool {
				dr := e.Object.(*v1alpha1.DRBDResource)
				return dr.Spec.NodeName == nodeName
			},
			UpdateFunc: func(e event.TypedUpdateEvent[client.Object]) bool {
				drNew := e.ObjectNew.(*v1alpha1.DRBDResource)
				drOld := e.ObjectOld.(*v1alpha1.DRBDResource)
				if drNew.Spec.NodeName != nodeName {
					return false
				}

				// React to Generation change (spec updates).
				if drNew.Generation != drOld.Generation {
					return true
				}

				// React to DeletionTimestamp change (start of deletion).
				// Deletion is metadata-only and does not bump Generation, but
				// the agent must reconcile to tear the DRBD resource down and
				// drop its finalizer. Without this, a DRBDResource deleted while
				// its DRBD device is quiescent (so the scanner emits no event)
				// would never be reconciled and would leak its finalizer.
				if (drOld.DeletionTimestamp == nil) != (drNew.DeletionTimestamp == nil) {
					return true
				}

				// React to Finalizers change (gates the final teardown decision).
				if !slices.Equal(drOld.Finalizers, drNew.Finalizers) {
					return true
				}

				// Ignore pure status updates to avoid reconcile loops.
				return false
			},
			DeleteFunc: func(e event.TypedDeleteEvent[client.Object]) bool {
				dr := e.Object.(*v1alpha1.DRBDResource)
				return dr.Spec.NodeName == nodeName
			},
			GenericFunc: func(_ event.TypedGenericEvent[client.Object]) bool {
				return false
			},
		},
	}
}

// drbdmPredicates selects the DRBDMapper events that change something this
// controller acts on, for this node only. Everything else is dropped, so drbdm's
// own reconcile traffic costs no DRBDResource reconciles.
//
//   - Delete: the teardown and demote paths hold DRBD up while a DRBDMapper
//     exists, so its disappearance is what releases them — and the delete event is
//     the first moment it is certainly gone.
//   - Update where Configured flips: that is what changes the device this
//     controller publishes to consumers.
//
// Creates are dropped: a mapper starts out with no configured device, so there is
// nothing to publish and nothing waiting on it.
func drbdmPredicates(nodeName string) []predicate.Predicate {
	return []predicate.Predicate{
		predicate.Funcs{
			CreateFunc: func(_ event.TypedCreateEvent[client.Object]) bool {
				return false
			},
			UpdateFunc: func(e event.TypedUpdateEvent[client.Object]) bool {
				updated, ok := e.ObjectNew.(*v1alpha1.DRBDMapper)
				if !ok || updated.Spec.NodeName != nodeName {
					return false
				}
				old, ok := e.ObjectOld.(*v1alpha1.DRBDMapper)
				return ok && configuredChanged(old, updated)
			},
			DeleteFunc: func(e event.TypedDeleteEvent[client.Object]) bool {
				drbdm, ok := e.Object.(*v1alpha1.DRBDMapper)
				return ok && drbdm.Spec.NodeName == nodeName
			},
			GenericFunc: func(_ event.TypedGenericEvent[client.Object]) bool {
				return false
			},
		},
	}
}

// configuredChanged reports whether the Configured condition flipped between two
// versions of a DRBDMapper.
func configuredChanged(old, updated *v1alpha1.DRBDMapper) bool {
	return obju.IsStatusConditionPresentAndTrue(old, v1alpha1.DRBDMapperCondConfiguredType) !=
		obju.IsStatusConditionPresentAndTrue(updated, v1alpha1.DRBDMapperCondConfiguredType)
}
