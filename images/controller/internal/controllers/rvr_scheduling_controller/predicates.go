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

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// RVRPredicates returns predicates for ReplicatedVolumeReplica events.
// Reacts to:
//   - Create: always (new RVR needs scheduling)
//   - Update: only if Diskful RVR needs LVG scheduling (LVG/ThinPool cleared or became Diskful)
//   - Delete: never (scheduling controller doesn't handle deletion)
//   - Generic: never
func RVRPredicates() []predicate.Predicate {
	return []predicate.Predicate{
		predicate.Funcs{
			CreateFunc:  func(_ event.TypedCreateEvent[client.Object]) bool { return true },
			UpdateFunc:  rvrNeedsLVGScheduling,
			DeleteFunc:  func(_ event.TypedDeleteEvent[client.Object]) bool { return false },
			GenericFunc: func(_ event.TypedGenericEvent[client.Object]) bool { return false },
		},
	}
}

// rvrNeedsLVGScheduling returns true if a Diskful RVR needs LVG scheduling.
// This happens when:
//   - Case 1: LVG or ThinPool was cleared on an existing Diskful RVR
//   - Case 2: RVR transitioned from Diskless to Diskful but missing LVG/ThinPool
func rvrNeedsLVGScheduling(e event.TypedUpdateEvent[client.Object]) bool {
	oldRVR, okOld := e.ObjectOld.(*v1alpha1.ReplicatedVolumeReplica)
	newRVR, okNew := e.ObjectNew.(*v1alpha1.ReplicatedVolumeReplica)
	if !okOld || !okNew || oldRVR == nil || newRVR == nil {
		return false
	}

	// Case 1: LVG/ThinPool was cleared on Diskful
	if newRVR.Spec.Type == v1alpha1.ReplicaTypeDiskful {
		// LVG cleared (had LVG -> no LVG)
		if oldRVR.Spec.LVMVolumeGroupName != "" && newRVR.Spec.LVMVolumeGroupName == "" {
			return true
		}
		// ThinPool cleared (for LVMThin)
		if oldRVR.Spec.LVMVolumeGroupThinPoolName != "" && newRVR.Spec.LVMVolumeGroupThinPoolName == "" {
			return true
		}
	}

	// Case 2: Became Diskful but needs LVG/ThinPool scheduling
	// (transition from Diskless types like TieBreaker/Access)
	if oldRVR.Spec.Type != v1alpha1.ReplicaTypeDiskful &&
		newRVR.Spec.Type == v1alpha1.ReplicaTypeDiskful &&
		newRVR.Spec.NodeName != "" &&
		(newRVR.Spec.LVMVolumeGroupName == "" || newRVR.Spec.LVMVolumeGroupThinPoolName == "") {
		return true
	}

	return false
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

				if oldRSP.Status.EligibleNodesRevision != newRSP.Status.EligibleNodesRevision {
					return true
				}

				return false
			},
		},
	}
}
