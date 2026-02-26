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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// rspPredicates returns predicates for ReplicatedStoragePool events.
// Filters to only react to:
//   - Generation changes (spec updates)
//   - Ready condition changes (status)
//   - EligibleNodesRevision changes (status)
func rspPredicates() []predicate.Predicate {
	return []predicate.Predicate{
		predicate.TypedFuncs[client.Object]{
			UpdateFunc: func(e event.TypedUpdateEvent[client.Object]) bool {
				// Be conservative if objects are nil.
				if e.ObjectOld == nil || e.ObjectNew == nil {
					return true
				}

				// Generation change (spec updates).
				if e.ObjectNew.GetGeneration() != e.ObjectOld.GetGeneration() {
					return true
				}

				oldRSP, okOld := e.ObjectOld.(*v1alpha1.ReplicatedStoragePool)
				newRSP, okNew := e.ObjectNew.(*v1alpha1.ReplicatedStoragePool)
				if !okOld || !okNew || oldRSP == nil || newRSP == nil {
					return true
				}

				// EligibleNodesRevision change.
				if oldRSP.Status.EligibleNodesRevision != newRSP.Status.EligibleNodesRevision {
					return true
				}

				// Ready condition change.
				return !obju.AreConditionsSemanticallyEqual(
					oldRSP, newRSP,
					v1alpha1.ReplicatedStoragePoolCondReadyType,
				)
			},
		},
	}
}

// rvPredicates returns predicates for ReplicatedVolume events.
// Filters to only react to changes in:
//   - spec.replicatedStorageClassName (storage class reference)
//   - status.configurationObservedGeneration (observed RSC state for acknowledgment tracking)
//   - ConfigurationReady condition
//   - SatisfyEligibleNodes condition
func rvPredicates() []predicate.Predicate {
	return []predicate.Predicate{
		predicate.TypedFuncs[client.Object]{
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

				// Configuration observation state change.
				if oldRV.Status.ConfigurationObservedGeneration != newRV.Status.ConfigurationObservedGeneration {
					return true
				}

				return !obju.AreConditionsSemanticallyEqual(
					oldRV, newRV,
					v1alpha1.ReplicatedVolumeCondConfigurationReadyType,
					v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesType,
				)
			},
		},
	}
}
