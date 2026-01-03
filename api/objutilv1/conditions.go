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

package objutilv1

import (
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ConditionSemanticallyEqual compares conditions ignoring LastTransitionTime.
//
// This is used to avoid bumping LastTransitionTime when only ObservedGeneration changes.
func ConditionSemanticallyEqual(a, b *metav1.Condition) bool {
	if a == nil || b == nil {
		return a == b
	}

	return a.Type == b.Type &&
		a.Status == b.Status &&
		a.Reason == b.Reason &&
		a.Message == b.Message &&
		a.ObservedGeneration == b.ObservedGeneration
}

func IsStatusConditionPresentAndEqual(obj StatusConditionObject, condType string, condStatus metav1.ConditionStatus) bool {
	actual := meta.FindStatusCondition(obj.GetStatusConditions(), condType)
	return actual != nil && actual.Status == condStatus
}

func IsStatusConditionPresentAndTrue(obj StatusConditionObject, condType string) bool {
	return IsStatusConditionPresentAndEqual(obj, condType, metav1.ConditionTrue)
}

func IsStatusConditionPresentAndFalse(obj StatusConditionObject, condType string) bool {
	return IsStatusConditionPresentAndEqual(obj, condType, metav1.ConditionFalse)
}

func IsStatusConditionPresentAndSemanticallyEqual(obj StatusConditionObject, expected metav1.Condition) bool {
	actual := meta.FindStatusCondition(obj.GetStatusConditions(), expected.Type)
	return actual != nil && ConditionSemanticallyEqual(actual, &expected)
}

func HasStatusCondition(obj StatusConditionObject, condType string) bool {
	return meta.FindStatusCondition(obj.GetStatusConditions(), condType) != nil
}

func GetStatusCondition(obj StatusConditionObject, condType string) *metav1.Condition {
	return meta.FindStatusCondition(obj.GetStatusConditions(), condType)
}

// SetStatusCondition upserts a condition into `.status.conditions`.
//
// It always sets ObservedGeneration to obj.Generation and returns whether the
// stored conditions have changed.
//
// LastTransitionTime behavior:
// - MUST be updated when the condition's Status changes
// - SHOULD NOT be updated when only Reason or Message changes
// - for ObservedGeneration-only changes, it preserves the previous LastTransitionTime
func SetStatusCondition(obj StatusConditionObject, cond metav1.Condition) (changed bool) {
	cond.ObservedGeneration = obj.GetGeneration()

	conds := obj.GetStatusConditions()
	old := meta.FindStatusCondition(conds, cond.Type)

	// Per Kubernetes conditions guidance:
	// - MUST bump LastTransitionTime on Status changes
	// - SHOULD NOT bump it on Reason/Message-only changes
	//
	// meta.SetStatusCondition implements the same semantics, but:
	// - for a new condition, it sets LastTransitionTime to now() only if it's zero
	// - for status changes, it uses the provided LastTransitionTime if non-zero
	//
	// We explicitly set LastTransitionTime for new conditions and status changes,
	// and leave it zero for non-status updates so meta keeps the existing value.
	if old == nil || old.Status != cond.Status {
		cond.LastTransitionTime = metav1.Now()
	} else {
		cond.LastTransitionTime = metav1.Time{}
	}

	changed = meta.SetStatusCondition(&conds, cond)
	if changed {
		obj.SetStatusConditions(conds)
	}
	return changed
}

func RemoveStatusCondition(obj StatusConditionObject, condType string) (changed bool) {
	conds := obj.GetStatusConditions()
	changed = meta.RemoveStatusCondition(&conds, condType)
	if changed {
		obj.SetStatusConditions(conds)
	}
	return changed
}
