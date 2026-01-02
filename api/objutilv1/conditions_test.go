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

package objutilv1_test

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/sds-replicated-volume/api/objutilv1"
)

type testConditionedObject struct {
	metav1.PartialObjectMetadata
	conds []metav1.Condition
}

func (o *testConditionedObject) GetStatusConditions() []metav1.Condition {
	return o.conds
}

func (o *testConditionedObject) SetStatusConditions(conditions []metav1.Condition) {
	o.conds = conditions
}

func TestSetStatusCondition_ObservedGenerationAndLastTransitionTime(t *testing.T) {
	obj := &testConditionedObject{}
	obj.SetGeneration(1)

	in := metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "OK", Message: "ok"}

	if changed := objutilv1.SetStatusCondition(obj, in); !changed {
		t.Fatalf("expected changed=true on first set")
	}

	got := objutilv1.GetStatusCondition(obj, "Ready")
	if got == nil {
		t.Fatalf("expected condition to be present")
	}

	if got.ObservedGeneration != 1 {
		t.Fatalf("expected ObservedGeneration=1, got %d", got.ObservedGeneration)
	}
	if got.LastTransitionTime.IsZero() {
		t.Fatalf("expected LastTransitionTime to be set")
	}
	ltt1 := got.LastTransitionTime

	// Same input, same generation -> no change.
	if changed := objutilv1.SetStatusCondition(obj, in); changed {
		t.Fatalf("expected changed=false on idempotent set")
	}

	got = objutilv1.GetStatusCondition(obj, "Ready")
	if got == nil {
		t.Fatalf("expected condition to be present")
	}
	if got.LastTransitionTime != ltt1 {
		t.Fatalf("expected LastTransitionTime to be preserved on idempotent set")
	}

	// Only generation changes -> ObservedGeneration changes, but LastTransitionTime is preserved.
	obj.SetGeneration(2)
	if changed := objutilv1.SetStatusCondition(obj, in); !changed {
		t.Fatalf("expected changed=true when only ObservedGeneration changes")
	}

	got = objutilv1.GetStatusCondition(obj, "Ready")
	if got == nil {
		t.Fatalf("expected condition to be present")
	}
	if got.ObservedGeneration != 2 {
		t.Fatalf("expected ObservedGeneration=2, got %d", got.ObservedGeneration)
	}
	if got.LastTransitionTime != ltt1 {
		t.Fatalf("expected LastTransitionTime to be preserved when only ObservedGeneration changes")
	}

	// Message changes -> LastTransitionTime is preserved.
	obj.conds[0].LastTransitionTime = metav1.NewTime(time.Unix(2, 0).UTC())
	ltt2 := obj.conds[0].LastTransitionTime

	in.Message = "new-message"
	obj.SetGeneration(3)
	if changed := objutilv1.SetStatusCondition(obj, in); !changed {
		t.Fatalf("expected changed=true when message changes")
	}
	got = objutilv1.GetStatusCondition(obj, "Ready")
	if got == nil {
		t.Fatalf("expected condition to be present")
	}
	if got.LastTransitionTime != ltt2 {
		t.Fatalf("expected LastTransitionTime to be preserved when only message changes")
	}

	// Reason changes -> LastTransitionTime is preserved.
	obj.conds[0].LastTransitionTime = metav1.NewTime(time.Unix(3, 0).UTC())
	ltt3 := obj.conds[0].LastTransitionTime

	in.Reason = "Other"
	obj.SetGeneration(4)
	if changed := objutilv1.SetStatusCondition(obj, in); !changed {
		t.Fatalf("expected changed=true when reason changes")
	}
	got = objutilv1.GetStatusCondition(obj, "Ready")
	if got == nil {
		t.Fatalf("expected condition to be present")
	}
	if got.LastTransitionTime != ltt3 {
		t.Fatalf("expected LastTransitionTime to be preserved when only reason changes")
	}

	// Actual transition -> LastTransitionTime updated.
	// Make old LTT distinguishable.
	obj.conds[0].LastTransitionTime = metav1.NewTime(time.Unix(1, 0).UTC())
	oldLTT := obj.conds[0].LastTransitionTime

	in.Status = metav1.ConditionFalse
	obj.SetGeneration(5)
	if changed := objutilv1.SetStatusCondition(obj, in); !changed {
		t.Fatalf("expected changed=true when meaning changes")
	}

	got = objutilv1.GetStatusCondition(obj, "Ready")
	if got == nil {
		t.Fatalf("expected condition to be present")
	}
	if got.LastTransitionTime == oldLTT {
		t.Fatalf("expected LastTransitionTime to change when meaning changes")
	}
}

func TestRemoveStatusCondition(t *testing.T) {
	obj := &testConditionedObject{}

	if changed := objutilv1.RemoveStatusCondition(obj, "Ready"); changed {
		t.Fatalf("expected changed=false when condition not present")
	}

	obj.SetGeneration(1)
	_ = objutilv1.SetStatusCondition(obj, metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue})

	if changed := objutilv1.RemoveStatusCondition(obj, "Ready"); !changed {
		t.Fatalf("expected changed=true when condition present")
	}
	if objutilv1.HasStatusCondition(obj, "Ready") {
		t.Fatalf("expected condition to be removed")
	}
}
