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

package testkit

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// testObj is a minimal client.Object implementation for unit tests.
// It satisfies client.Object (via embedded ObjectMeta+TypeMeta)
// and the match.Conditioned interface (via GetStatusConditions).
type testObjStatus struct {
	Phase      string             `json:"phase,omitempty"`
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

type testObj struct {
	metav1.ObjectMeta `json:"metadata"`
	metav1.TypeMeta   `json:",inline"`
	Status            testObjStatus `json:"status,omitempty"`
}

// Alias for brevity in tests.
func (o *testObj) GetStatusPhase() string                   { return o.Status.Phase }
func (o *testObj) GetStatusConditions() []metav1.Condition  { return o.Status.Conditions }
func (o *testObj) SetStatusConditions(c []metav1.Condition) { o.Status.Conditions = c }

func (o *testObj) DeepCopyObject() runtime.Object {
	c := *o
	c.Status.Conditions = make([]metav1.Condition, len(o.Status.Conditions))
	copy(c.Status.Conditions, o.Status.Conditions)
	co := o.DeepCopy()
	c.ObjectMeta = *co
	return &c
}

func makeTestObj(name, phase string) *testObj {
	o := &testObj{}
	o.Name = name
	o.Status.Phase = phase
	return o
}

func makeTestObjWithCondition(name, condType, status, reason, message string) *testObj {
	o := &testObj{}
	o.Name = name
	o.Status.Conditions = []metav1.Condition{
		{Type: condType, Status: metav1.ConditionStatus(status), Reason: reason, Message: message},
	}
	return o
}

func makeTestObjWithFinalizer(name, phase, finalizer string) *testObj { //nolint:unparam // name is parameterized for test readability
	o := makeTestObj(name, phase)
	o.SetFinalizers([]string{finalizer})
	return o
}
