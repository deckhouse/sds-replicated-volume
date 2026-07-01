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

package drbdr

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

const testPredicateNodeName = "node-a"

func drbdrForPredicate(nodeName string, generation int64, deleting bool, finalizers ...string) *v1alpha1.DRBDResource {
	dr := &v1alpha1.DRBDResource{
		ObjectMeta: metav1.ObjectMeta{
			Generation: generation,
			Finalizers: finalizers,
		},
		Spec: v1alpha1.DRBDResourceSpec{NodeName: nodeName},
	}
	if deleting {
		now := metav1.Now()
		dr.DeletionTimestamp = &now
	}
	return dr
}

func TestDRBDRPredicatesUpdate(t *testing.T) {
	pred := drbdrPredicates(testPredicateNodeName)[0]

	tests := []struct {
		name     string
		oldObj   *v1alpha1.DRBDResource
		newObj   *v1alpha1.DRBDResource
		expected bool
	}{
		{
			name:     "different node is ignored",
			oldObj:   drbdrForPredicate("node-b", 1, false),
			newObj:   drbdrForPredicate("node-b", 2, false),
			expected: false,
		},
		{
			name:     "generation change triggers reconcile",
			oldObj:   drbdrForPredicate(testPredicateNodeName, 1, false),
			newObj:   drbdrForPredicate(testPredicateNodeName, 2, false),
			expected: true,
		},
		{
			name:     "pure status update is ignored",
			oldObj:   drbdrForPredicate(testPredicateNodeName, 3, false, v1alpha1.AgentFinalizer),
			newObj:   drbdrForPredicate(testPredicateNodeName, 3, false, v1alpha1.AgentFinalizer),
			expected: false,
		},
		{
			// Regression guard: the transition into deletion is metadata-only
			// (Generation is not bumped), yet the agent must reconcile to tear
			// the DRBD resource down and drop its finalizer. Dropping this event
			// leaks the finalizer when no scanner (kernel) event follows.
			name:     "deletion start triggers reconcile despite unchanged generation",
			oldObj:   drbdrForPredicate(testPredicateNodeName, 3, false, v1alpha1.AgentFinalizer),
			newObj:   drbdrForPredicate(testPredicateNodeName, 3, true, v1alpha1.AgentFinalizer),
			expected: true,
		},
		{
			name:     "finalizer change triggers reconcile",
			oldObj:   drbdrForPredicate(testPredicateNodeName, 3, true, v1alpha1.AgentFinalizer),
			newObj:   drbdrForPredicate(testPredicateNodeName, 3, true),
			expected: true,
		},
		{
			name:     "status update while deleting is ignored",
			oldObj:   drbdrForPredicate(testPredicateNodeName, 3, true, v1alpha1.AgentFinalizer),
			newObj:   drbdrForPredicate(testPredicateNodeName, 3, true, v1alpha1.AgentFinalizer),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pred.Update(event.TypedUpdateEvent[client.Object]{
				ObjectOld: tt.oldObj,
				ObjectNew: tt.newObj,
			})
			if got != tt.expected {
				t.Errorf("Update() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestDRBDRPredicatesCreateDelete(t *testing.T) {
	pred := drbdrPredicates(testPredicateNodeName)[0]

	if !pred.Create(event.TypedCreateEvent[client.Object]{Object: drbdrForPredicate(testPredicateNodeName, 1, false)}) {
		t.Error("Create() on matching node = false, want true")
	}
	if pred.Create(event.TypedCreateEvent[client.Object]{Object: drbdrForPredicate("node-b", 1, false)}) {
		t.Error("Create() on other node = true, want false")
	}
	if !pred.Delete(event.TypedDeleteEvent[client.Object]{Object: drbdrForPredicate(testPredicateNodeName, 1, true)}) {
		t.Error("Delete() on matching node = false, want true")
	}
	if pred.Delete(event.TypedDeleteEvent[client.Object]{Object: drbdrForPredicate("node-b", 1, true)}) {
		t.Error("Delete() on other node = true, want false")
	}
}
