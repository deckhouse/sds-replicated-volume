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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// RVRPredicates returns predicates for ReplicatedVolumeReplica events.
// Reacts to:
//   - Create: always (new RVR needs scheduling)
//   - Update: spec changed (generation bump) or SatisfyEligibleNodes is False
//   - Delete: never (scheduling controller doesn't handle deletion)
//   - Generic: never
func RVRPredicates() []predicate.Predicate {
	return []predicate.Predicate{
		predicate.Funcs{
			UpdateFunc: rvrNeedsLVGScheduling,
			DeleteFunc: func(_ event.TypedDeleteEvent[client.Object]) bool { return false },
		},
	}
}

// rvrNeedsLVGScheduling returns true if:
//   - RVR spec changed (generation bumped), or
//   - SatisfyEligibleNodes condition is present and False (needs re-scheduling).
func rvrNeedsLVGScheduling(e event.TypedUpdateEvent[client.Object]) bool {
	oldRVR, okOld := e.ObjectOld.(*v1alpha1.ReplicatedVolumeReplica)
	newRVR, okNew := e.ObjectNew.(*v1alpha1.ReplicatedVolumeReplica)
	if !okOld || !okNew || oldRVR == nil || newRVR == nil {
		return true
	}

	if newRVR.Generation != oldRVR.Generation {
		return true
	}

	if obju.StatusCondition(newRVR, v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType).IsFalse().Eval() {
		return true
	}

	return false
}

// RVPredicates returns predicates for ReplicatedVolume events.
// Reacts to:
//   - Update: status.configurationGeneration changed
//   - Create, Delete, Generic: never
func RVPredicates() []predicate.Predicate {
	return []predicate.Predicate{
		predicate.Funcs{
			UpdateFunc: rvConfigurationGenerationChanged,
		},
	}
}

// rvConfigurationGenerationChanged returns true if status.configurationGeneration changed.
func rvConfigurationGenerationChanged(e event.TypedUpdateEvent[client.Object]) bool {
	oldRV, okOld := e.ObjectOld.(*v1alpha1.ReplicatedVolume)
	newRV, okNew := e.ObjectNew.(*v1alpha1.ReplicatedVolume)
	if !okOld || !okNew || oldRV == nil || newRV == nil {
		return true
	}
	return newRV.Status.ConfigurationGeneration != oldRV.Status.ConfigurationGeneration
}

// RSPPredicates returns predicates for ReplicatedStoragePool events.
// Reacts to:
//   - Create: always (new RSP may affect scheduling)
//   - Update: status.eligibleNodesRevision changed
//   - Delete, Generic: never
func RSPPredicates() []predicate.Predicate {
	return []predicate.Predicate{
		predicate.Funcs{
			UpdateFunc: rspEligibleNodesRevisionChanged,
		},
	}
}

// rspEligibleNodesRevisionChanged returns true if status.eligibleNodesRevision changed.
func rspEligibleNodesRevisionChanged(e event.TypedUpdateEvent[client.Object]) bool {
	oldRSP, okOld := e.ObjectOld.(*v1alpha1.ReplicatedStoragePool)
	newRSP, okNew := e.ObjectNew.(*v1alpha1.ReplicatedStoragePool)
	if !okOld || !okNew || oldRSP == nil || newRSP == nil {
		return true
	}
	return newRSP.Status.EligibleNodesRevision != oldRSP.Status.EligibleNodesRevision
}
