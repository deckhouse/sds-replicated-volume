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

package worldcontroller

import (
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const agentPodLabelApp = "agent"

// agentPodPredicates returns typed predicates for agent Pod objects.
// Only triggers for pods with label app=agent.
// Triggers on:
// - Create and Delete events
// - Ready condition status changes
func agentPodPredicates() []predicate.TypedPredicate[*corev1.Pod] {
	return []predicate.TypedPredicate[*corev1.Pod]{
		predicate.TypedFuncs[*corev1.Pod]{
			CreateFunc: func(e event.TypedCreateEvent[*corev1.Pod]) bool {
				return isAgentPod(e.Object)
			},
			DeleteFunc: func(e event.TypedDeleteEvent[*corev1.Pod]) bool {
				return isAgentPod(e.Object)
			},
			UpdateFunc: func(e event.TypedUpdateEvent[*corev1.Pod]) bool {
				oldPod := e.ObjectOld
				newPod := e.ObjectNew
				if oldPod == nil || newPod == nil {
					return true
				}

				// Only process agent pods
				if !isAgentPod(newPod) {
					return false
				}

				// Ready condition status changes
				if getPodReadyStatus(oldPod) != getPodReadyStatus(newPod) {
					return true
				}

				return false
			},
			GenericFunc: func(e event.TypedGenericEvent[*corev1.Pod]) bool {
				return isAgentPod(e.Object)
			},
		},
	}
}

// isAgentPod checks if a pod is an agent pod by label.
func isAgentPod(pod *corev1.Pod) bool {
	if pod == nil {
		return false
	}
	labels := pod.GetLabels()
	return labels != nil && labels["app"] == agentPodLabelApp
}

// getPodReadyStatus returns the status of the Ready condition for a Pod.
func getPodReadyStatus(pod *corev1.Pod) corev1.ConditionStatus {
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady {
			return cond.Status
		}
	}
	return corev1.ConditionUnknown
}
