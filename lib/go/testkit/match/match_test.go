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

package match

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// testObj is a minimal client.Object + Conditioned stub for match tests.
type testObjStatus struct {
	Phase      string             `json:"phase,omitempty"`
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

type testObj struct {
	metav1.ObjectMeta `json:"metadata"`
	metav1.TypeMeta   `json:",inline"`
	Status            testObjStatus `json:"status,omitempty"`
}

func (o *testObj) DeepCopyObject() runtime.Object {
	c := *o
	c.Status.Conditions = make([]metav1.Condition, len(o.Status.Conditions))
	copy(c.Status.Conditions, o.Status.Conditions)
	co := o.DeepCopy()
	c.ObjectMeta = *co
	return &c
}

func (o *testObj) GetStatusPhase() string                   { return o.Status.Phase }
func (o *testObj) GetStatusConditions() []metav1.Condition  { return o.Status.Conditions }
func (o *testObj) SetStatusConditions(c []metav1.Condition) { o.Status.Conditions = c }

var _ client.Object = &testObj{}
var _ Phased = &testObj{}
var _ Conditioned = &testObj{}

func obj(phase string) *testObj {
	o := &testObj{}
	o.Status.Phase = phase
	return o
}

func objWithCondition(condType, status, reason, message string) *testObj { //nolint:unparam // condType is parameterized for test readability
	return &testObj{
		Status: testObjStatus{Conditions: []metav1.Condition{{
			Type:               condType,
			Status:             metav1.ConditionStatus(status),
			Reason:             reason,
			Message:            message,
			LastTransitionTime: metav1.NewTime(time.Now()),
		}}},
	}
}

// ---------------------------------------------------------------------------
// GomegaMatcher compliance
// ---------------------------------------------------------------------------

var _ = Describe("GomegaMatcher compliance", func() {
	It("Phase matcher works with Expect().To()", func() {
		Expect(obj("Healthy")).To(Phase("Healthy"))
	})

	It("Phase matcher fails with Expect().To() on mismatch", func() {
		Expect(obj("Pending")).ToNot(Phase("Healthy"))
	})

	It("Gomega And works with our matchers", func() {
		o := obj("Healthy")
		o.SetFinalizers([]string{"test-fin"})
		Expect(o).To(And(Phase("Healthy"), HasFinalizer("test-fin")))
	})

	It("Gomega Or works with our matchers", func() {
		Expect(obj("Pending")).To(Or(Phase("Pending"), Phase("Healthy")))
	})

	It("Gomega Not works with our matchers", func() {
		Expect(obj("Healthy")).To(Not(Phase("Critical")))
	})
})

// ---------------------------------------------------------------------------
// MatchObject
// ---------------------------------------------------------------------------

var _ = Describe("MatchObject", func() {
	It("returns match and detail for our matchers", func() {
		matched, detail := MatchObject(Phase("Healthy"), obj("Healthy"))
		Expect(matched).To(BeTrue())
		Expect(detail).To(ContainSubstring("Healthy"))
	})

	It("returns mismatch and detail for our matchers", func() {
		matched, detail := MatchObject(Phase("Healthy"), obj("Pending"))
		Expect(matched).To(BeFalse())
		Expect(detail).To(ContainSubstring("Pending"))
	})
})

// ---------------------------------------------------------------------------
// Phase matchers
// ---------------------------------------------------------------------------

var _ = Describe("Phase", func() {
	It("matches when phase equals expected", func() {
		Expect(obj("Healthy")).To(Phase("Healthy"))
	})

	It("fails when phase differs", func() {
		Expect(obj("Pending")).ToNot(Phase("Healthy"))
	})

	It("matches empty phase on fresh object", func() {
		Expect(obj("")).To(Phase(""))
	})

	It("works for any type with status.phase-like field", func() {
		Expect(obj("Attached")).To(Phase("Attached"))
	})
})

var _ = Describe("PhaseNot", func() {
	It("matches when phase differs", func() {
		Expect(obj("Healthy")).To(PhaseNot("Critical"))
	})

	It("fails when phase equals excluded", func() {
		Expect(obj("Critical")).ToNot(PhaseNot("Critical"))
	})
})

var _ = Describe("AnyPhase", func() {
	It("matches when phase is in set", func() {
		Expect(obj("Configuring")).To(AnyPhase("Provisioning", "Configuring"))
	})

	It("fails when phase is not in set", func() {
		Expect(obj("Healthy")).ToNot(AnyPhase("Provisioning", "Configuring"))
	})
})

// ---------------------------------------------------------------------------
// Condition matchers
// ---------------------------------------------------------------------------

var _ = Describe("HasCondition", func() {
	It("matches when condition exists", func() {
		Expect(objWithCondition("Ready", "True", "Ready", "")).To(HasCondition("Ready"))
	})

	It("fails when condition absent", func() {
		Expect(&testObj{}).ToNot(HasCondition("Ready"))
	})
})

var _ = Describe("NoCondition", func() {
	It("matches when condition absent", func() {
		Expect(&testObj{}).To(NoCondition("Ready"))
	})

	It("fails when condition exists", func() {
		Expect(objWithCondition("Ready", "True", "Ready", "")).ToNot(NoCondition("Ready"))
	})
})

var _ = Describe("ConditionStatus", func() {
	It("matches correct status", func() {
		Expect(objWithCondition("Ready", "True", "Ready", "")).To(ConditionStatus("Ready", "True"))
	})

	It("fails on wrong status", func() {
		Expect(objWithCondition("Ready", "False", "NotReady", "")).ToNot(ConditionStatus("Ready", "True"))
	})

	It("fails when condition absent", func() {
		Expect(&testObj{}).ToNot(ConditionStatus("Ready", "True"))
	})
})

var _ = Describe("ConditionReason", func() {
	It("matches correct reason", func() {
		Expect(objWithCondition("Ready", "True", "QuorumMet", "")).To(ConditionReason("Ready", "QuorumMet"))
	})

	It("fails on wrong reason", func() {
		Expect(objWithCondition("Ready", "False", "QuorumLost", "")).ToNot(ConditionReason("Ready", "QuorumMet"))
	})
})

var _ = Describe("ConditionMessageContains", func() {
	It("matches substring", func() {
		Expect(objWithCondition("Ready", "True", "Ready", "quorum: 2/3")).To(ConditionMessageContains("Ready", "quorum"))
	})

	It("fails when substring absent", func() {
		Expect(objWithCondition("Ready", "True", "Ready", "all good")).ToNot(ConditionMessageContains("Ready", "quorum"))
	})
})

// ---------------------------------------------------------------------------
// Metadata matchers
// ---------------------------------------------------------------------------

var _ = Describe("HasFinalizer", func() {
	It("matches when finalizer present", func() {
		o := &testObj{}
		o.SetFinalizers([]string{"test-finalizer"})
		Expect(o).To(HasFinalizer("test-finalizer"))
	})

	It("fails when finalizer absent", func() {
		Expect(&testObj{}).ToNot(HasFinalizer("test-finalizer"))
	})
})

var _ = Describe("HasLabel", func() {
	It("matches when label key exists", func() {
		o := &testObj{}
		o.SetLabels(map[string]string{"app": "test"})
		Expect(o).To(HasLabel("app"))
	})

	It("fails when label key absent", func() {
		Expect(&testObj{}).ToNot(HasLabel("app"))
	})
})

var _ = Describe("LabelEquals", func() {
	It("matches when key=value", func() {
		o := &testObj{}
		o.SetLabels(map[string]string{"app": "test"})
		Expect(o).To(LabelEquals("app", "test"))
	})

	It("fails when value differs", func() {
		o := &testObj{}
		o.SetLabels(map[string]string{"app": "other"})
		Expect(o).ToNot(LabelEquals("app", "test"))
	})

	It("fails when key absent", func() {
		Expect(&testObj{}).ToNot(LabelEquals("app", "test"))
	})
})

var _ = Describe("IsDeleting", func() {
	It("matches when deletionTimestamp set", func() {
		o := &testObj{}
		now := metav1.Now()
		o.SetDeletionTimestamp(&now)
		Expect(o).To(IsDeleting())
	})

	It("fails when deletionTimestamp not set", func() {
		Expect(&testObj{}).ToNot(IsDeleting())
	})
})

// ---------------------------------------------------------------------------
// ResourceVersionAtLeast
// ---------------------------------------------------------------------------

var _ = Describe("ResourceVersionAtLeast", func() {
	It("matches when RV equals target", func() {
		o := &testObj{}
		o.SetResourceVersion("100")
		Expect(o).To(ResourceVersionAtLeast("100"))
	})

	It("matches when RV exceeds target", func() {
		o := &testObj{}
		o.SetResourceVersion("200")
		Expect(o).To(ResourceVersionAtLeast("100"))
	})

	It("fails when RV below target", func() {
		o := &testObj{}
		o.SetResourceVersion("50")
		Expect(o).ToNot(ResourceVersionAtLeast("100"))
	})

	It("failure message includes both versions", func() {
		o := &testObj{}
		o.SetResourceVersion("50")
		m := ResourceVersionAtLeast("100")
		matched, _ := m.Match(o)
		Expect(matched).To(BeFalse())
		Expect(m.FailureMessage(o)).To(ContainSubstring("50"))
		Expect(m.FailureMessage(o)).To(ContainSubstring("100"))
	})
})

// ---------------------------------------------------------------------------
// Switch
// ---------------------------------------------------------------------------

var _ = Describe("Switch", func() {
	It("enabled: delegates to inner matcher", func() {
		sw := NewSwitch(Phase("Healthy"))
		Expect(obj("Healthy")).To(sw)
	})

	It("enabled: fails when inner fails", func() {
		sw := NewSwitch(Phase("Healthy"))
		Expect(obj("Pending")).ToNot(sw)
	})

	It("disabled: always passes", func() {
		sw := NewSwitch(Phase("Healthy"))
		sw.Disable()
		Expect(obj("Critical")).To(sw)
	})

	It("re-enabled: delegates again", func() {
		sw := NewSwitch(Phase("Healthy"))
		sw.Disable()
		sw.Enable()
		Expect(obj("Critical")).ToNot(sw)
	})

	It("IsDisabled reports state", func() {
		sw := NewSwitch(Phase("Healthy"))
		Expect(sw.IsDisabled()).To(BeFalse())
		sw.Disable()
		Expect(sw.IsDisabled()).To(BeTrue())
		sw.Enable()
		Expect(sw.IsDisabled()).To(BeFalse())
	})

	It("WithDisabled scope-based", func() {
		sw := NewSwitch(Phase("Healthy"))
		WithDisabled(sw, func() {
			Expect(sw.IsDisabled()).To(BeTrue())
		})
		Expect(sw.IsDisabled()).To(BeFalse())
	})

	It("WithDisabled re-enables on panic", func() {
		sw := NewSwitch(Phase("Healthy"))
		Expect(func() {
			WithDisabled(sw, func() {
				panic("boom")
			})
		}).To(PanicWith("boom"))
		Expect(sw.IsDisabled()).To(BeFalse())
	})

	It("works with Gomega And combinator", func() {
		o := obj("Healthy")
		o.SetFinalizers([]string{"test"})
		sw := NewSwitch(Phase("Healthy"))
		Expect(o).To(And(sw, HasFinalizer("test")))
	})
})
