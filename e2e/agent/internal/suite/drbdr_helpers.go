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

package suite

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/e2e/agent/pkg/envtesting"
)

// isDRBDRTerminal returns true when the Configured condition has reached a
// terminal state (Status=True or Status=False) for the current generation.
func isDRBDRTerminal(drbdr *v1alpha1.DRBDResource) bool {
	for _, cond := range drbdr.Status.Conditions {
		if cond.Type != v1alpha1.DRBDResourceCondConfiguredType {
			continue
		}
		if cond.ObservedGeneration < drbdr.Generation {
			return false
		}
		return cond.Status == metav1.ConditionTrue || cond.Status == metav1.ConditionFalse
	}
	return false
}

// isDRBDRCondition returns a predicate that matches when the given condition
// has the specified status and reason for the current generation.
func isDRBDRCondition(condType string, status metav1.ConditionStatus, reason string) func(*v1alpha1.DRBDResource) bool {
	return func(drbdr *v1alpha1.DRBDResource) bool {
		for _, cond := range drbdr.Status.Conditions {
			if cond.Type != condType {
				continue
			}
			if cond.ObservedGeneration < drbdr.Generation {
				return false
			}
			return cond.Status == status && cond.Reason == reason
		}
		return false
	}
}

func assertDRBDRConfigured(e envtesting.E, drbdr *v1alpha1.DRBDResource) {
	e.Helper()
	for _, cond := range drbdr.Status.Conditions {
		if cond.Type != v1alpha1.DRBDResourceCondConfiguredType {
			continue
		}
		if cond.Status != metav1.ConditionTrue {
			e.Fatalf("DRBDResource %q Configured condition is %s (reason: %s, message: %s)",
				drbdr.Name, cond.Status, cond.Reason, cond.Message)
		}
		return
	}
	e.Fatalf("DRBDResource %q has no Configured condition", drbdr.Name)
}

func assertDRBDRCondition(e envtesting.E, drbdr *v1alpha1.DRBDResource, condType string, status metav1.ConditionStatus, reason string) {
	e.Helper()
	for _, cond := range drbdr.Status.Conditions {
		if cond.Type != condType {
			continue
		}
		if cond.Status != status {
			e.Fatalf("DRBDResource %q condition %s: status is %s, want %s (reason: %s, message: %s)",
				drbdr.Name, condType, cond.Status, status, cond.Reason, cond.Message)
		}
		if cond.Reason != reason {
			e.Fatalf("DRBDResource %q condition %s: reason is %s, want %s",
				drbdr.Name, condType, cond.Reason, reason)
		}
		return
	}
	e.Fatalf("DRBDResource %q has no %s condition", drbdr.Name, condType)
}

func assertDRBDRRole(e envtesting.E, drbdr *v1alpha1.DRBDResource, expected v1alpha1.DRBDRole) {
	e.Helper()
	if drbdr.Status.ActiveConfiguration == nil {
		e.Fatalf("DRBDResource %q has no activeConfiguration", drbdr.Name)
	}
	if drbdr.Status.ActiveConfiguration.Role != expected {
		e.Fatalf("DRBDResource %q activeConfiguration.role is %s, want %s",
			drbdr.Name, drbdr.Status.ActiveConfiguration.Role, expected)
	}
}
