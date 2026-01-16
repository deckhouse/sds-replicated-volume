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

func TestConditionEqualByStatus(t *testing.T) {
	a := &metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "A", Message: "a"}
	b := &metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "B", Message: "b"}

	if !objutilv1.ConditionEqualByStatus(a, b) {
		t.Fatalf("expected equal when Type and Status match")
	}

	b.Type = "Other"
	if objutilv1.ConditionEqualByStatus(a, b) {
		t.Fatalf("expected not equal when Type differs")
	}

	b.Type = "Ready"
	b.Status = metav1.ConditionFalse
	if objutilv1.ConditionEqualByStatus(a, b) {
		t.Fatalf("expected not equal when Status differs")
	}

	if !objutilv1.ConditionEqualByStatus((*metav1.Condition)(nil), (*metav1.Condition)(nil)) {
		t.Fatalf("expected nil==nil to be equal")
	}
	if objutilv1.ConditionEqualByStatus(a, (*metav1.Condition)(nil)) {
		t.Fatalf("expected non-nil != nil")
	}
}

func TestAreConditionsSemanticallyEqual_SelectedTypes(t *testing.T) {
	a := &testConditionedObject{}
	b := &testConditionedObject{}

	a.SetGeneration(1)
	b.SetGeneration(1)

	_ = objutilv1.SetStatusCondition(a, metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "OK"})
	_ = objutilv1.SetStatusCondition(b, metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "OK"})

	// Both missing -> equal for that type.
	if !objutilv1.AreConditionsSemanticallyEqual(a, b, "Missing") {
		t.Fatalf("expected equal when selected condition type is missing on both objects")
	}

	// Present on both and semantically equal -> equal.
	if !objutilv1.AreConditionsSemanticallyEqual(a, b, "Ready") {
		t.Fatalf("expected equal for semantically equal condition on both objects")
	}

	// Missing on one -> not equal.
	_ = objutilv1.RemoveStatusCondition(b, "Ready")
	if objutilv1.AreConditionsSemanticallyEqual(a, b, "Ready") {
		t.Fatalf("expected not equal when condition is missing on exactly one object")
	}
}

func TestAreConditionsSemanticallyEqual_AllTypesWhenEmpty(t *testing.T) {
	a := &testConditionedObject{}
	b := &testConditionedObject{}

	a.SetGeneration(1)
	b.SetGeneration(1)

	_ = objutilv1.SetStatusCondition(a, metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "OK"})
	_ = objutilv1.SetStatusCondition(b, metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "OK"})

	_ = objutilv1.SetStatusCondition(a, metav1.Condition{Type: "Online", Status: metav1.ConditionTrue})
	_ = objutilv1.SetStatusCondition(b, metav1.Condition{Type: "Online", Status: metav1.ConditionTrue})

	if !objutilv1.AreConditionsSemanticallyEqual(a, b) {
		t.Fatalf("expected equal when all condition types are semantically equal")
	}

	// Change meaning for one condition type.
	_ = objutilv1.SetStatusCondition(b, metav1.Condition{Type: "Online", Status: metav1.ConditionFalse, Reason: "Down"})
	if objutilv1.AreConditionsSemanticallyEqual(a, b) {
		t.Fatalf("expected not equal when any condition meaning differs")
	}
}

func TestAreConditionsEqualByStatus_SelectedTypes(t *testing.T) {
	a := &testConditionedObject{}
	b := &testConditionedObject{}

	a.SetGeneration(1)
	b.SetGeneration(1)

	_ = objutilv1.SetStatusCondition(a, metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "A"})
	_ = objutilv1.SetStatusCondition(b, metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "B"})

	// StatusEqual ignores Reason/Message/ObservedGeneration differences.
	if !objutilv1.AreConditionsEqualByStatus(a, b, "Ready") {
		t.Fatalf("expected equal when Type and Status match")
	}

	// Both missing -> equal for that type.
	if !objutilv1.AreConditionsEqualByStatus(a, b, "Missing") {
		t.Fatalf("expected equal when selected condition type is missing on both objects")
	}

	// Missing on one -> not equal.
	_ = objutilv1.RemoveStatusCondition(b, "Ready")
	if objutilv1.AreConditionsEqualByStatus(a, b, "Ready") {
		t.Fatalf("expected not equal when condition is missing on exactly one object")
	}
}

func TestAreConditionsEqualByStatus_AllTypesWhenEmpty(t *testing.T) {
	a := &testConditionedObject{}
	b := &testConditionedObject{}

	a.SetGeneration(1)
	b.SetGeneration(1)

	_ = objutilv1.SetStatusCondition(a, metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "A"})
	_ = objutilv1.SetStatusCondition(b, metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "B"})

	_ = objutilv1.SetStatusCondition(a, metav1.Condition{Type: "Online", Status: metav1.ConditionTrue})
	_ = objutilv1.SetStatusCondition(b, metav1.Condition{Type: "Online", Status: metav1.ConditionTrue})

	if !objutilv1.AreConditionsEqualByStatus(a, b) {
		t.Fatalf("expected equal when all condition types have equal Type+Status")
	}

	// Status differs for one condition type -> not equal.
	_ = objutilv1.SetStatusCondition(b, metav1.Condition{Type: "Online", Status: metav1.ConditionFalse, Reason: "Down"})
	if objutilv1.AreConditionsEqualByStatus(a, b) {
		t.Fatalf("expected not equal when any condition Status differs")
	}
}
