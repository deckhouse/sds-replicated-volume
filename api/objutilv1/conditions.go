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
	"slices"

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

// ConditionEqualByStatus compares conditions by Type and Status only.
func ConditionEqualByStatus(a, b *metav1.Condition) bool {
	if a == nil || b == nil {
		return a == b
	}

	return a.Type == b.Type &&
		a.Status == b.Status
}

func areConditionsEqual(a, b StatusConditionObject, condTypes []string, cmp func(a, b *metav1.Condition) bool) bool {
	if a == nil || b == nil {
		return a == b
	}

	aConds := a.GetStatusConditions()
	bConds := b.GetStatusConditions()

	var types []string
	if len(condTypes) > 0 {
		// Keep caller order; ignore duplicates without sorting.
		types = make([]string, 0, len(condTypes))
		for _, t := range condTypes {
			if slices.Contains(types, t) {
				continue
			}
			types = append(types, t)
		}
	} else {
		types = make([]string, 0, len(aConds)+len(bConds))
		for i := range aConds {
			types = append(types, aConds[i].Type)
		}
		for i := range bConds {
			types = append(types, bConds[i].Type)
		}

		// Deduplicate for the "all types" mode; order doesn't matter here.
		slices.Sort(types)
		types = slices.Compact(types)
	}

	for i := range types {
		condType := types[i]
		ac := meta.FindStatusCondition(aConds, condType)
		bc := meta.FindStatusCondition(bConds, condType)
		if ac == nil || bc == nil {
			if ac == bc {
				continue
			}
			return false
		}
		if !cmp(ac, bc) {
			return false
		}
	}

	return true
}

// AreConditionsSemanticallyEqual compares `.status.conditions` between two objects.
//
// If condTypes are provided, it compares only those condition types (duplicates are ignored).
// If condTypes is empty, it compares all condition types present in either object.
//
// Missing conditions:
// - if a condition type is missing on both objects, it is considered equal;
// - if it is missing on exactly one object, it is not equal.
//
// Semantic equality ignores LastTransitionTime (see ConditionSemanticallyEqual).
func AreConditionsSemanticallyEqual(a, b StatusConditionObject, condTypes ...string) bool {
	return areConditionsEqual(a, b, condTypes, ConditionSemanticallyEqual)
}

// AreConditionsEqualByStatus compares `.status.conditions` between two objects by Type and Status only.
//
// If condTypes are provided, it compares only those condition types (duplicates are ignored).
// If condTypes is empty, it compares all condition types present in either object.
//
// Missing conditions:
// - if a condition type is missing on both objects, it is considered equal;
// - if it is missing on exactly one object, it is not equal.
func AreConditionsEqualByStatus(a, b StatusConditionObject, condTypes ...string) bool {
	return areConditionsEqual(a, b, condTypes, ConditionEqualByStatus)
}

// IsStatusConditionPresentAndEqual reports whether `.status.conditions` contains the condition type with the given status.
func IsStatusConditionPresentAndEqual(obj StatusConditionObject, condType string, condStatus metav1.ConditionStatus) bool {
	actual := meta.FindStatusCondition(obj.GetStatusConditions(), condType)
	return actual != nil && actual.Status == condStatus
}

// IsStatusConditionPresentAndTrue is a convenience wrapper for IsStatusConditionPresentAndEqual(..., ConditionTrue).
func IsStatusConditionPresentAndTrue(obj StatusConditionObject, condType string) bool {
	return IsStatusConditionPresentAndEqual(obj, condType, metav1.ConditionTrue)
}

// IsStatusConditionPresentAndFalse is a convenience wrapper for IsStatusConditionPresentAndEqual(..., ConditionFalse).
func IsStatusConditionPresentAndFalse(obj StatusConditionObject, condType string) bool {
	return IsStatusConditionPresentAndEqual(obj, condType, metav1.ConditionFalse)
}

// IsStatusConditionPresentAndSemanticallyEqual reports whether the condition with the same Type is present and semantically equal.
func IsStatusConditionPresentAndSemanticallyEqual(obj StatusConditionObject, expected metav1.Condition) bool {
	actual := meta.FindStatusCondition(obj.GetStatusConditions(), expected.Type)
	return actual != nil && ConditionSemanticallyEqual(actual, &expected)
}

// HasStatusCondition reports whether `.status.conditions` contains the given condition type.
func HasStatusCondition(obj StatusConditionObject, condType string) bool {
	return meta.FindStatusCondition(obj.GetStatusConditions(), condType) != nil
}

// GetStatusCondition returns the condition with the given type from `.status.conditions`, or nil if it is not present.
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

// RemoveStatusCondition removes the condition with the given type from `.status.conditions`.
// It returns whether the stored conditions changed.
func RemoveStatusCondition(obj StatusConditionObject, condType string) (changed bool) {
	conds := obj.GetStatusConditions()
	changed = meta.RemoveStatusCondition(&conds, condType)
	if changed {
		obj.SetStatusConditions(conds)
	}
	return changed
}
