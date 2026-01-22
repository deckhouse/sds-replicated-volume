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

package rspcontroller

import (
	"maps"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	nodeutil "k8s.io/component-helpers/node/util"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// RSPPredicates returns predicates for ReplicatedStoragePool events.
// Filters to only react to generation changes (spec updates).
func RSPPredicates() []predicate.Predicate {
	return []predicate.Predicate{predicate.GenerationChangedPredicate{}}
}

// NodePredicates returns predicates for Node events.
// Filters to only react to:
//   - Label changes (for zone and node matching)
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

				// Any label change (for zone and node matching).
				if !maps.Equal(e.ObjectOld.GetLabels(), e.ObjectNew.GetLabels()) {
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

// LVGPredicates returns predicates for LVMVolumeGroup events.
// Filters to only react to:
//   - Generation changes (spec updates, including spec.local.nodeName)
//   - Unschedulable annotation changes
//   - Ready condition status changes
//   - ThinPools[].Ready status changes
func LVGPredicates() []predicate.Predicate {
	return []predicate.Predicate{
		predicate.Funcs{
			UpdateFunc: func(e event.TypedUpdateEvent[client.Object]) bool {
				// Generation change (spec updates).
				if e.ObjectNew.GetGeneration() != e.ObjectOld.GetGeneration() {
					return true
				}

				oldLVG, okOld := e.ObjectOld.(*snc.LVMVolumeGroup)
				newLVG, okNew := e.ObjectNew.(*snc.LVMVolumeGroup)
				if !okOld || !okNew || oldLVG == nil || newLVG == nil {
					return true
				}

				// Unschedulable annotation change.
				_, oldUnschedulable := oldLVG.Annotations[v1alpha1.LVMVolumeGroupUnschedulableAnnotationKey]
				_, newUnschedulable := newLVG.Annotations[v1alpha1.LVMVolumeGroupUnschedulableAnnotationKey]
				if oldUnschedulable != newUnschedulable {
					return true
				}

				// Ready condition status change.
				if lvgReadyConditionStatus(oldLVG) != lvgReadyConditionStatus(newLVG) {
					return true
				}

				// ThinPools[].Ready status change.
				if !areThinPoolsReadyEqual(oldLVG.Status.ThinPools, newLVG.Status.ThinPools) {
					return true
				}

				return false
			},
		},
	}
}

// lvgReadyConditionStatus returns the status of the Ready condition on an LVG.
func lvgReadyConditionStatus(lvg *snc.LVMVolumeGroup) metav1.ConditionStatus {
	if cond := meta.FindStatusCondition(lvg.Status.Conditions, "Ready"); cond != nil {
		return cond.Status
	}
	return metav1.ConditionUnknown
}

// areThinPoolsReadyEqual compares only the Ready field of thin pools by name.
func areThinPoolsReadyEqual(old, new []snc.LVMVolumeGroupThinPoolStatus) bool {
	// Build map of name -> ready for old thin pools.
	oldReady := make(map[string]bool, len(old))
	for _, tp := range old {
		oldReady[tp.Name] = tp.Ready
	}

	// Check new thin pools against old.
	if len(old) != len(new) {
		return false
	}
	for _, tp := range new {
		if oldReady[tp.Name] != tp.Ready {
			return false
		}
	}
	return true
}

// AgentPodPredicates returns predicates for agent Pod events.
// Filters to only react to:
//   - Pods in the specified namespace with label app=agent
//   - Ready condition changes
//   - Create/Delete events
func AgentPodPredicates(podNamespace string) []predicate.Predicate {
	return []predicate.Predicate{
		predicate.Funcs{
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
