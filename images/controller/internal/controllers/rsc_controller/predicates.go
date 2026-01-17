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

package rsccontroller

import (
	corev1 "k8s.io/api/core/v1"
	nodeutil "k8s.io/component-helpers/node/util"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// NodePredicates returns predicates for Node events.
// Filters to only react to:
//   - Zone label changes (topology.kubernetes.io/zone)
//   - Ready condition changes
//   - spec.unschedulable changes
func NodePredicates() []predicate.Predicate {
	return []predicate.Predicate{
		predicate.Funcs{
			UpdateFunc: func(e event.TypedUpdateEvent[client.Object]) bool {
				oldNode, okOld := e.ObjectOld.(*corev1.Node)
				newNode, okNew := e.ObjectNew.(*corev1.Node)
				if !okOld || !okNew || oldNode == nil || newNode == nil {
					return true
				}

				// Zone label change (via client.Object getter).
				if e.ObjectOld.GetLabels()[corev1.LabelTopologyZone] != e.ObjectNew.GetLabels()[corev1.LabelTopologyZone] {
					return true
				}

				// Ready condition change.
				_, oldReady := nodeutil.GetNodeCondition(&oldNode.Status, corev1.NodeReady)
				_, newReady := nodeutil.GetNodeCondition(&newNode.Status, corev1.NodeReady)
				if (oldReady == nil) != (newReady == nil) ||
					(oldReady != nil && newReady != nil && oldReady.Status != newReady.Status) {
					return true
				}

				// spec.unschedulable change.
				if oldNode.Spec.Unschedulable != newNode.Spec.Unschedulable {
					return true
				}

				return false
			},
		},
	}
}

// RSPPredicates returns predicates for ReplicatedStoragePool events.
// Filters to only react to generation changes (spec updates).
func RSPPredicates() []predicate.Predicate {
	return []predicate.Predicate{predicate.GenerationChangedPredicate{}}
}

// LVGPredicates returns predicates for LVMVolumeGroup events.
// Filters to only react to generation changes (spec updates).
func LVGPredicates() []predicate.Predicate {
	return []predicate.Predicate{predicate.GenerationChangedPredicate{}}
}

// RVPredicates returns predicates for ReplicatedVolume events.
// Filters to only react to changes in:
//   - spec.replicatedStorageClassName (storage class reference)
//   - StorageClassConfigurationAligned condition
//   - StorageClassEligibleNodesAligned condition
func RVPredicates() []predicate.Predicate {
	return []predicate.Predicate{
		predicate.Funcs{
			GenericFunc: func(event.TypedGenericEvent[client.Object]) bool { return false },
			UpdateFunc: func(e event.TypedUpdateEvent[client.Object]) bool {
				oldRV, okOld := e.ObjectOld.(*v1alpha1.ReplicatedVolume)
				newRV, okNew := e.ObjectNew.(*v1alpha1.ReplicatedVolume)
				if !okOld || !okNew || oldRV == nil || newRV == nil {
					return true
				}

				// Storage class reference change.
				if oldRV.Spec.ReplicatedStorageClassName != newRV.Spec.ReplicatedStorageClassName {
					return true
				}

				return !obju.AreConditionsSemanticallyEqual(
					oldRV, newRV,
					v1alpha1.ReplicatedVolumeCondStorageClassConfigurationAlignedType,
					v1alpha1.ReplicatedVolumeCondStorageClassEligibleNodesAlignedType,
				)
			},
		},
	}
}
