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

package rvrcontroller

import (
	"slices"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// rvrPredicates returns predicates for ReplicatedVolumeReplica events.
// Reacts to Generation changes (Spec) and Finalizers changes.
// Skips pure Status updates and Labels/Annotations changes.
func rvrPredicates() []predicate.Predicate {
	return []predicate.Predicate{
		predicate.TypedFuncs[client.Object]{
			UpdateFunc: func(e event.TypedUpdateEvent[client.Object]) bool {
				// React to Generation change (Spec changes).
				if e.ObjectNew.GetGeneration() != e.ObjectOld.GetGeneration() {
					return true
				}
				// React to Finalizers change.
				if !slices.Equal(e.ObjectNew.GetFinalizers(), e.ObjectOld.GetFinalizers()) {
					return true
				}
				return false
			},
		},
	}
}

// llvPredicates returns predicates for LVMLogicalVolume events.
// Intentionally empty: we need to react to all LLV fields (Status, Spec, Labels, Finalizers, OwnerReferences).
// Filtering only Annotations would add complexity with negligible benefit.
func llvPredicates() []predicate.Predicate {
	return nil
}

// drbdrPredicates returns predicates for DRBDResource events.
// Intentionally empty: we need to react to all DRBDResource fields
func drbdrPredicates() []predicate.Predicate {
	return nil
}

// rvPredicates returns predicates for ReplicatedVolume events.
// Reacts to:
// - DatameshRevision changes (Status.Datamesh.Size, membership, etc.)
// - Spec.ReplicatedStorageClassName changes (for labels)
// - DatameshReplicaRequests message changes (for condition message enrichment)
func rvPredicates() []predicate.Predicate {
	return []predicate.Predicate{
		predicate.TypedFuncs[client.Object]{
			CreateFunc:  func(event.TypedCreateEvent[client.Object]) bool { return false },
			DeleteFunc:  func(event.TypedDeleteEvent[client.Object]) bool { return false },
			GenericFunc: func(event.TypedGenericEvent[client.Object]) bool { return false },
			UpdateFunc: func(e event.TypedUpdateEvent[client.Object]) bool {
				oldRV, okOld := e.ObjectOld.(*v1alpha1.ReplicatedVolume)
				newRV, okNew := e.ObjectNew.(*v1alpha1.ReplicatedVolume)
				if !okOld || !okNew || oldRV == nil || newRV == nil {
					return true
				}

				// React to DatameshRevision change (covers Size, membership changes, etc.).
				if oldRV.Status.DatameshRevision != newRV.Status.DatameshRevision {
					return true
				}

				// React to Spec.ReplicatedStorageClassName change (for labels).
				if oldRV.Spec.ReplicatedStorageClassName != newRV.Spec.ReplicatedStorageClassName {
					return true
				}

				// React to DatameshReplicaRequests message changes (for condition message enrichment).
				if !slices.EqualFunc(
					oldRV.Status.DatameshReplicaRequests,
					newRV.Status.DatameshReplicaRequests,
					func(a, b v1alpha1.ReplicatedVolumeDatameshReplicaRequest) bool {
						return a.Name == b.Name && a.Message == b.Message
					},
				) {
					return true
				}

				return false
			},
		},
	}
}

// agentPodPredicates returns predicates for agent Pod events.
// Filters to only react to:
//   - Pods in the specified namespace with label app=agent
//   - Ready condition changes
//   - Create/Delete events
func agentPodPredicates(podNamespace string) []predicate.Predicate {
	return []predicate.Predicate{
		predicate.TypedFuncs[client.Object]{
			CreateFunc: func(e event.TypedCreateEvent[client.Object]) bool {
				pod, ok := e.Object.(*corev1.Pod)
				if !ok || pod == nil {
					return true // Be conservative on type assertion failure.
				}
				return pod.Namespace == podNamespace && pod.Labels["app"] == "agent"
			},
			UpdateFunc: func(e event.TypedUpdateEvent[client.Object]) bool {
				oldPod, okOld := e.ObjectOld.(*corev1.Pod)
				newPod, okNew := e.ObjectNew.(*corev1.Pod)
				if !okOld || !okNew || oldPod == nil || newPod == nil {
					return true // Be conservative on type assertion failure.
				}

				// Only care about agent pods in the target namespace.
				if newPod.Namespace != podNamespace || newPod.Labels["app"] != "agent" {
					return false
				}

				// React to Ready condition changes.
				oldReady := isPodReady(oldPod)
				newReady := isPodReady(newPod)
				return oldReady != newReady
			},
			DeleteFunc: func(e event.TypedDeleteEvent[client.Object]) bool {
				pod, ok := e.Object.(*corev1.Pod)
				if !ok || pod == nil {
					return true // Be conservative on type assertion failure.
				}
				return pod.Namespace == podNamespace && pod.Labels["app"] == "agent"
			},
		},
	}
}

// isPodReady checks if a pod has the Ready condition set to True.
func isPodReady(pod *corev1.Pod) bool {
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}

// rspPredicates returns predicates for ReplicatedStoragePool events.
// Filters to react only to eligibleNodes changes (using EligibleNodesRevision).
// The actual per-node change detection is done in the EventHandler.
func rspPredicates() []predicate.Predicate {
	return []predicate.Predicate{
		predicate.TypedFuncs[client.Object]{
			CreateFunc: func(event.TypedCreateEvent[client.Object]) bool { return true },
			DeleteFunc: func(event.TypedDeleteEvent[client.Object]) bool { return true },
			UpdateFunc: func(e event.TypedUpdateEvent[client.Object]) bool {
				oldRSP, okOld := e.ObjectOld.(*v1alpha1.ReplicatedStoragePool)
				newRSP, okNew := e.ObjectNew.(*v1alpha1.ReplicatedStoragePool)
				if !okOld || !okNew || oldRSP == nil || newRSP == nil {
					return true
				}
				// React only if EligibleNodesRevision changed.
				return oldRSP.Status.EligibleNodesRevision != newRSP.Status.EligibleNodesRevision
			},
		},
	}
}
