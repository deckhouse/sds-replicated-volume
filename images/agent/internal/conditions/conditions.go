package conditions

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func IsTrue(conditions []metav1.Condition, conditionType string) bool {
	for _, condition := range conditions {
		if condition.Type == conditionType && condition.Status == metav1.ConditionTrue {
			return true
		}
	}
	return false
}

// Set adds the provided condition to the slice if it is not present,
// or updates the existing condition with the same Type. It returns the new slice.
func Set(conditionsSlice []metav1.Condition, newCondition metav1.Condition) []metav1.Condition {
	for i := range conditionsSlice {
		if conditionsSlice[i].Type == newCondition.Type {
			conditionsSlice[i] = newCondition
			return conditionsSlice
		}
	}
	return append(conditionsSlice, newCondition)
}
