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

func TestConditionSemanticallyEqual(t *testing.T) {
	base := &metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "OK",
		Message:            "all good",
		ObservedGeneration: 1,
		LastTransitionTime: metav1.NewTime(time.Unix(100, 0).UTC()),
	}

	// Identical conditions are equal.
	same := &metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "OK",
		Message:            "all good",
		ObservedGeneration: 1,
		LastTransitionTime: metav1.NewTime(time.Unix(999, 0).UTC()), // Different LTT
	}
	if !objutilv1.ConditionSemanticallyEqual(base, same) {
		t.Fatalf("expected equal when only LastTransitionTime differs")
	}

	// Different Type -> not equal.
	diffType := &metav1.Condition{Type: "Other", Status: metav1.ConditionTrue, Reason: "OK", Message: "all good", ObservedGeneration: 1}
	if objutilv1.ConditionSemanticallyEqual(base, diffType) {
		t.Fatalf("expected not equal when Type differs")
	}

	// Different Status -> not equal.
	diffStatus := &metav1.Condition{Type: "Ready", Status: metav1.ConditionFalse, Reason: "OK", Message: "all good", ObservedGeneration: 1}
	if objutilv1.ConditionSemanticallyEqual(base, diffStatus) {
		t.Fatalf("expected not equal when Status differs")
	}

	// Different Reason -> not equal.
	diffReason := &metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Other", Message: "all good", ObservedGeneration: 1}
	if objutilv1.ConditionSemanticallyEqual(base, diffReason) {
		t.Fatalf("expected not equal when Reason differs")
	}

	// Different Message -> not equal.
	diffMessage := &metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "OK", Message: "different", ObservedGeneration: 1}
	if objutilv1.ConditionSemanticallyEqual(base, diffMessage) {
		t.Fatalf("expected not equal when Message differs")
	}

	// Different ObservedGeneration -> not equal.
	diffOG := &metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "OK", Message: "all good", ObservedGeneration: 2}
	if objutilv1.ConditionSemanticallyEqual(base, diffOG) {
		t.Fatalf("expected not equal when ObservedGeneration differs")
	}

	// nil handling.
	if !objutilv1.ConditionSemanticallyEqual(nil, nil) {
		t.Fatalf("expected nil==nil to be equal")
	}
	if objutilv1.ConditionSemanticallyEqual(base, nil) {
		t.Fatalf("expected non-nil != nil")
	}
	if objutilv1.ConditionSemanticallyEqual(nil, base) {
		t.Fatalf("expected nil != non-nil")
	}
}

func TestIsStatusConditionPresentAndTrue(t *testing.T) {
	obj := &testConditionedObject{}
	obj.SetGeneration(1)

	// Condition not present -> false.
	if objutilv1.IsStatusConditionPresentAndTrue(obj, "Ready") {
		t.Fatalf("expected false when condition not present")
	}

	// Condition present and True -> true.
	_ = objutilv1.SetStatusCondition(obj, metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "OK"})
	if !objutilv1.IsStatusConditionPresentAndTrue(obj, "Ready") {
		t.Fatalf("expected true when condition is True")
	}

	// Condition present but False -> false.
	_ = objutilv1.SetStatusCondition(obj, metav1.Condition{Type: "Ready", Status: metav1.ConditionFalse, Reason: "NotOK"})
	if objutilv1.IsStatusConditionPresentAndTrue(obj, "Ready") {
		t.Fatalf("expected false when condition is False")
	}

	// Condition present but Unknown -> false.
	_ = objutilv1.SetStatusCondition(obj, metav1.Condition{Type: "Ready", Status: metav1.ConditionUnknown, Reason: "Pending"})
	if objutilv1.IsStatusConditionPresentAndTrue(obj, "Ready") {
		t.Fatalf("expected false when condition is Unknown")
	}
}

func TestIsStatusConditionPresentAndFalse(t *testing.T) {
	obj := &testConditionedObject{}
	obj.SetGeneration(1)

	// Condition not present -> false.
	if objutilv1.IsStatusConditionPresentAndFalse(obj, "Ready") {
		t.Fatalf("expected false when condition not present")
	}

	// Condition present and False -> true.
	_ = objutilv1.SetStatusCondition(obj, metav1.Condition{Type: "Ready", Status: metav1.ConditionFalse, Reason: "NotOK"})
	if !objutilv1.IsStatusConditionPresentAndFalse(obj, "Ready") {
		t.Fatalf("expected true when condition is False")
	}

	// Condition present but True -> false.
	_ = objutilv1.SetStatusCondition(obj, metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "OK"})
	if objutilv1.IsStatusConditionPresentAndFalse(obj, "Ready") {
		t.Fatalf("expected false when condition is True")
	}

	// Condition present but Unknown -> false.
	_ = objutilv1.SetStatusCondition(obj, metav1.Condition{Type: "Ready", Status: metav1.ConditionUnknown, Reason: "Pending"})
	if objutilv1.IsStatusConditionPresentAndFalse(obj, "Ready") {
		t.Fatalf("expected false when condition is Unknown")
	}
}

func TestHasStatusCondition(t *testing.T) {
	obj := &testConditionedObject{}
	obj.SetGeneration(1)

	if objutilv1.HasStatusCondition(obj, "Ready") {
		t.Fatalf("expected false when condition not present")
	}

	_ = objutilv1.SetStatusCondition(obj, metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "OK"})
	if !objutilv1.HasStatusCondition(obj, "Ready") {
		t.Fatalf("expected true when condition present")
	}

	if objutilv1.HasStatusCondition(obj, "Other") {
		t.Fatalf("expected false for different condition type")
	}
}

func TestGetStatusCondition(t *testing.T) {
	obj := &testConditionedObject{}
	obj.SetGeneration(1)

	if objutilv1.GetStatusCondition(obj, "Ready") != nil {
		t.Fatalf("expected nil when condition not present")
	}

	_ = objutilv1.SetStatusCondition(obj, metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "OK", Message: "all good"})
	cond := objutilv1.GetStatusCondition(obj, "Ready")
	if cond == nil {
		t.Fatalf("expected condition to be returned")
	}
	if cond.Type != "Ready" || cond.Status != metav1.ConditionTrue || cond.Reason != "OK" || cond.Message != "all good" {
		t.Fatalf("unexpected condition values: %+v", cond)
	}
}

// ----------------------------------------------------------------------------
// Fluent API tests
// ----------------------------------------------------------------------------

func TestStatusConditionChecker_Present(t *testing.T) {
	obj := &testConditionedObject{}
	obj.SetGeneration(1)

	// Condition not present -> Eval returns false.
	if objutilv1.StatusCondition(obj, "Ready").Present().Eval() {
		t.Fatalf("expected false when condition not present")
	}

	_ = objutilv1.SetStatusCondition(obj, metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "OK"})
	if !objutilv1.StatusCondition(obj, "Ready").Present().Eval() {
		t.Fatalf("expected true when condition present")
	}
}

func TestStatusConditionChecker_Absent(t *testing.T) {
	obj := &testConditionedObject{}
	obj.SetGeneration(1)

	// Condition not present -> Absent returns true.
	if !objutilv1.StatusCondition(obj, "Ready").Absent().Eval() {
		t.Fatalf("expected true when condition not present")
	}

	// Condition present -> Absent returns false.
	_ = objutilv1.SetStatusCondition(obj, metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "OK"})
	if objutilv1.StatusCondition(obj, "Ready").Absent().Eval() {
		t.Fatalf("expected false when condition present")
	}

	// Absent on a different type that doesn't exist -> true.
	if !objutilv1.StatusCondition(obj, "Other").Absent().Eval() {
		t.Fatalf("expected true for absent condition type")
	}
}

func TestStatusConditionChecker_StatusEqual(t *testing.T) {
	obj := &testConditionedObject{}
	obj.SetGeneration(1)
	_ = objutilv1.SetStatusCondition(obj, metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "OK"})

	if !objutilv1.StatusCondition(obj, "Ready").StatusEqual(metav1.ConditionTrue).Eval() {
		t.Fatalf("expected true when status matches")
	}
	if objutilv1.StatusCondition(obj, "Ready").StatusEqual(metav1.ConditionFalse).Eval() {
		t.Fatalf("expected false when status differs")
	}
	if objutilv1.StatusCondition(obj, "Ready").StatusEqual(metav1.ConditionUnknown).Eval() {
		t.Fatalf("expected false when status differs")
	}

	// Condition not present -> always false.
	if objutilv1.StatusCondition(obj, "Missing").StatusEqual(metav1.ConditionTrue).Eval() {
		t.Fatalf("expected false when condition not present")
	}
}

func TestStatusConditionChecker_IsTrueIsFalse(t *testing.T) {
	obj := &testConditionedObject{}
	obj.SetGeneration(1)
	_ = objutilv1.SetStatusCondition(obj, metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "OK"})

	if !objutilv1.StatusCondition(obj, "Ready").IsTrue().Eval() {
		t.Fatalf("expected IsTrue to return true")
	}
	if objutilv1.StatusCondition(obj, "Ready").IsFalse().Eval() {
		t.Fatalf("expected IsFalse to return false")
	}

	_ = objutilv1.SetStatusCondition(obj, metav1.Condition{Type: "Ready", Status: metav1.ConditionFalse, Reason: "NotOK"})
	if objutilv1.StatusCondition(obj, "Ready").IsTrue().Eval() {
		t.Fatalf("expected IsTrue to return false")
	}
	if !objutilv1.StatusCondition(obj, "Ready").IsFalse().Eval() {
		t.Fatalf("expected IsFalse to return true")
	}
}

func TestStatusConditionChecker_ReasonEqual(t *testing.T) {
	obj := &testConditionedObject{}
	obj.SetGeneration(1)
	_ = objutilv1.SetStatusCondition(obj, metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "OK"})

	if !objutilv1.StatusCondition(obj, "Ready").ReasonEqual("OK").Eval() {
		t.Fatalf("expected true when reason matches")
	}
	if objutilv1.StatusCondition(obj, "Ready").ReasonEqual("Other").Eval() {
		t.Fatalf("expected false when reason differs")
	}

	// Condition not present -> always false.
	if objutilv1.StatusCondition(obj, "Missing").ReasonEqual("OK").Eval() {
		t.Fatalf("expected false when condition not present")
	}
}

func TestStatusConditionChecker_ReasonNotEqual(t *testing.T) {
	obj := &testConditionedObject{}
	obj.SetGeneration(1)
	_ = objutilv1.SetStatusCondition(obj, metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "OK"})

	if !objutilv1.StatusCondition(obj, "Ready").ReasonNotEqual("Other").Eval() {
		t.Fatalf("expected true when reason differs")
	}
	if objutilv1.StatusCondition(obj, "Ready").ReasonNotEqual("OK").Eval() {
		t.Fatalf("expected false when reason matches")
	}

	// Condition not present -> always false.
	if objutilv1.StatusCondition(obj, "Missing").ReasonNotEqual("OK").Eval() {
		t.Fatalf("expected false when condition not present")
	}
}

func TestStatusConditionChecker_MessageEqual(t *testing.T) {
	obj := &testConditionedObject{}
	obj.SetGeneration(1)
	_ = objutilv1.SetStatusCondition(obj, metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "OK", Message: "all good"})

	if !objutilv1.StatusCondition(obj, "Ready").MessageEqual("all good").Eval() {
		t.Fatalf("expected true when message matches")
	}
	if objutilv1.StatusCondition(obj, "Ready").MessageEqual("different").Eval() {
		t.Fatalf("expected false when message differs")
	}
	if objutilv1.StatusCondition(obj, "Ready").MessageEqual("").Eval() {
		t.Fatalf("expected false when message is empty but condition has message")
	}

	// Condition not present -> always false.
	if objutilv1.StatusCondition(obj, "Missing").MessageEqual("all good").Eval() {
		t.Fatalf("expected false when condition not present")
	}
}

func TestStatusConditionChecker_MessageContains(t *testing.T) {
	obj := &testConditionedObject{}
	obj.SetGeneration(1)
	_ = objutilv1.SetStatusCondition(obj, metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "OK", Message: "all good stuff"})

	if !objutilv1.StatusCondition(obj, "Ready").MessageContains("good").Eval() {
		t.Fatalf("expected true when message contains substring")
	}
	if !objutilv1.StatusCondition(obj, "Ready").MessageContains("all good stuff").Eval() {
		t.Fatalf("expected true when message equals substring")
	}
	if objutilv1.StatusCondition(obj, "Ready").MessageContains("bad").Eval() {
		t.Fatalf("expected false when message does not contain substring")
	}

	// Condition not present -> always false.
	if objutilv1.StatusCondition(obj, "Missing").MessageContains("good").Eval() {
		t.Fatalf("expected false when condition not present")
	}
}

func TestStatusConditionChecker_ObservedGenerationCurrent(t *testing.T) {
	obj := &testConditionedObject{}
	obj.SetGeneration(1)
	_ = objutilv1.SetStatusCondition(obj, metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "OK"})

	if !objutilv1.StatusCondition(obj, "Ready").ObservedGenerationCurrent().Eval() {
		t.Fatalf("expected true when ObservedGeneration matches Generation")
	}

	// Bump generation without updating condition.
	obj.SetGeneration(2)
	if objutilv1.StatusCondition(obj, "Ready").ObservedGenerationCurrent().Eval() {
		t.Fatalf("expected false when ObservedGeneration is stale")
	}

	// Update condition to match new generation.
	_ = objutilv1.SetStatusCondition(obj, metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "OK"})
	if !objutilv1.StatusCondition(obj, "Ready").ObservedGenerationCurrent().Eval() {
		t.Fatalf("expected true after updating condition")
	}

	// Condition not present -> always false.
	if objutilv1.StatusCondition(obj, "Missing").ObservedGenerationCurrent().Eval() {
		t.Fatalf("expected false when condition not present")
	}
}

func TestStatusConditionChecker_ChainedChecks(t *testing.T) {
	obj := &testConditionedObject{}
	obj.SetGeneration(1)
	_ = objutilv1.SetStatusCondition(obj, metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "OK", Message: "all good"})

	// All checks pass.
	if !objutilv1.StatusCondition(obj, "Ready").
		Present().
		IsTrue().
		ReasonEqual("OK").
		MessageEqual("all good").
		ObservedGenerationCurrent().
		Eval() {
		t.Fatalf("expected true when all checks pass")
	}

	// One check fails in the middle.
	if objutilv1.StatusCondition(obj, "Ready").
		Present().
		IsTrue().
		ReasonEqual("Wrong"). // fails here
		MessageEqual("all good").
		ObservedGenerationCurrent().
		Eval() {
		t.Fatalf("expected false when one check fails")
	}

	// First check fails.
	if objutilv1.StatusCondition(obj, "Missing").
		Present(). // fails here
		IsTrue().
		ReasonEqual("OK").
		Eval() {
		t.Fatalf("expected false when first check fails")
	}
}

func TestAreConditions_NilObjects(t *testing.T) {
	obj := &testConditionedObject{}
	obj.SetGeneration(1)
	_ = objutilv1.SetStatusCondition(obj, metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "OK"})

	// nil == nil.
	if !objutilv1.AreConditionsSemanticallyEqual(nil, nil) {
		t.Fatalf("expected nil==nil to be equal")
	}
	if !objutilv1.AreConditionsEqualByStatus(nil, nil) {
		t.Fatalf("expected nil==nil to be equal")
	}

	// non-nil != nil.
	if objutilv1.AreConditionsSemanticallyEqual(obj, nil) {
		t.Fatalf("expected non-nil != nil")
	}
	if objutilv1.AreConditionsSemanticallyEqual(nil, obj) {
		t.Fatalf("expected nil != non-nil")
	}
	if objutilv1.AreConditionsEqualByStatus(obj, nil) {
		t.Fatalf("expected non-nil != nil")
	}
	if objutilv1.AreConditionsEqualByStatus(nil, obj) {
		t.Fatalf("expected nil != non-nil")
	}
}
