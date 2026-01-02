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

package v1alpha1

import (
	"slices"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ConditionSpecAgnosticEqual compares only meaning of a condition,
// ignoring ObservedGeneration and LastTransitionTime.
func ConditionSpecAgnosticEqual(a, b *metav1.Condition) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Type == b.Type &&
		a.Status == b.Status &&
		a.Reason == b.Reason &&
		a.Message == b.Message
}

// ConditionSpecAwareEqual compares meaning of a condition and also
// requires ObservedGeneration to match. It still ignores LastTransitionTime.
func ConditionSpecAwareEqual(a, b *metav1.Condition) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Type == b.Type &&
		a.Status == b.Status &&
		a.Reason == b.Reason &&
		a.Message == b.Message &&
		a.ObservedGeneration == b.ObservedGeneration
}

// IsConditionPresentAndSpecAgnosticEqual checks that a condition with the same Type as expected exists in conditions
// and is equal to expected ignoring ObservedGeneration and LastTransitionTime.
func IsConditionPresentAndSpecAgnosticEqual(conditions []metav1.Condition, expected metav1.Condition) bool {
	actual := meta.FindStatusCondition(conditions, expected.Type)
	return actual != nil && ConditionSpecAgnosticEqual(actual, &expected)
}

// IsConditionPresentAndSpecAwareEqual checks that a condition with the same Type as expected exists in conditions
// and is equal to expected requiring ObservedGeneration to match, but ignoring LastTransitionTime.
func IsConditionPresentAndSpecAwareEqual(conditions []metav1.Condition, expected metav1.Condition) bool {
	actual := meta.FindStatusCondition(conditions, expected.Type)
	return actual != nil && ConditionSpecAwareEqual(actual, &expected)
}

// EnsureLabel sets a label on the given labels map if it's not already set to the expected value.
// Returns the updated labels map and a boolean indicating if a change was made.
// This function is used across controllers for idempotent label updates.
func EnsureLabel(labels map[string]string, key, value string) (map[string]string, bool) {
	if labels == nil {
		labels = make(map[string]string)
	}
	if labels[key] == value {
		return labels, false // no change needed
	}
	labels[key] = value
	return labels, true
}

func isExternalFinalizer(f string) bool {
	return f != ControllerFinalizer && f != AgentFinalizer
}

func HasExternalFinalizers(obj metav1.Object) bool {
	return slices.ContainsFunc(obj.GetFinalizers(), isExternalFinalizer)
}

func HasControllerFinalizer(obj metav1.Object) bool {
	return slices.Contains(obj.GetFinalizers(), ControllerFinalizer)
}

func HasAgentFinalizer(obj metav1.Object) bool {
	return slices.Contains(obj.GetFinalizers(), AgentFinalizer)
}
