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

package dmte

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("ReplicaStepBuilder", func() {
	It("sets name, scope, apply, confirm", func() {
		b := ReplicaStep("✦ → A", stubReplicaApply, stubReplicaConfirm)
		s := b.build()
		Expect(s.name).To(Equal("✦ → A"))
		Expect(s.scope).To(Equal(ReplicaScope))
		Expect(s.replicaApply).NotTo(BeNil())
		Expect(s.replicaConfirm).NotTo(BeNil())
		Expect(s.globalApply).To(BeNil())
		Expect(s.globalConfirm).To(BeNil())
	})

	It("panics on empty name", func() {
		Expect(func() { ReplicaStep("", stubReplicaApply, stubReplicaConfirm) }).To(Panic())
	})

	It("panics on nil apply", func() {
		Expect(func() { ReplicaStep[*testGCtx, *testReplicaCtx]("step", nil, stubReplicaConfirm) }).To(Panic())
	})

	It("panics on nil confirm", func() {
		Expect(func() { ReplicaStep("step", stubReplicaApply, nil) }).To(Panic())
	})

	It("Details sets opaque details", func() {
		b := ReplicaStep("step", stubReplicaApply, stubReplicaConfirm).
			Details("Attaching")
		Expect(b.build().details).To(Equal("Attaching"))
	})

	It("DiagnosticConditions sets condition types", func() {
		b := ReplicaStep("step", stubReplicaApply, stubReplicaConfirm).
			DiagnosticConditions("DRBDConfigured", "Ready")
		Expect(b.build().diagnosticConditionTypes).To(Equal([]string{"DRBDConfigured", "Ready"}))
	})

	It("DiagnosticSkipError sets skip function", func() {
		skip := func(*testReplicaCtx, uint8, *metav1.Condition) bool { return true }
		b := ReplicaStep("step", stubReplicaApply, stubReplicaConfirm).
			DiagnosticSkipError(skip)
		Expect(b.build().diagnosticSkipError).NotTo(BeNil())
	})

	It("fluent chaining returns same builder", func() {
		b := ReplicaStep("step", stubReplicaApply, stubReplicaConfirm)
		b2 := b.Details("X").DiagnosticConditions("Y")
		Expect(b2).To(BeIdenticalTo(b))
	})
})

var _ = Describe("GlobalStepBuilder", func() {
	It("sets name, scope, apply, confirm", func() {
		b := GlobalStep("qmr↑", stubGlobalApply, stubGlobalConfirm)
		s := buildGlobalStep[*testGCtx, *testReplicaCtx](b)
		Expect(s.name).To(Equal("qmr↑"))
		Expect(s.scope).To(Equal(GlobalScope))
		Expect(s.globalApply).NotTo(BeNil())
		Expect(s.globalConfirm).NotTo(BeNil())
		Expect(s.replicaApply).To(BeNil())
		Expect(s.replicaConfirm).To(BeNil())
	})

	It("panics on empty name", func() {
		Expect(func() { GlobalStep("", stubGlobalApply, stubGlobalConfirm) }).To(Panic())
	})

	It("panics on nil apply", func() {
		Expect(func() { GlobalStep[*testGCtx]("step", nil, stubGlobalConfirm) }).To(Panic())
	})

	It("panics on nil confirm", func() {
		Expect(func() { GlobalStep("step", stubGlobalApply, nil) }).To(Panic())
	})

	It("Details sets opaque details", func() {
		b := GlobalStep("step", stubGlobalApply, stubGlobalConfirm).
			Details("Enabling")
		s := buildGlobalStep[*testGCtx, *testReplicaCtx](b)
		Expect(s.details).To(Equal("Enabling"))
	})

	It("DiagnosticConditions sets condition types", func() {
		b := GlobalStep("step", stubGlobalApply, stubGlobalConfirm).
			DiagnosticConditions("DRBDConfigured")
		s := buildGlobalStep[*testGCtx, *testReplicaCtx](b)
		Expect(s.diagnosticConditionTypes).To(Equal([]string{"DRBDConfigured"}))
	})

	It("fluent chaining returns same builder", func() {
		b := GlobalStep("step", stubGlobalApply, stubGlobalConfirm)
		b2 := b.Details("X").DiagnosticConditions("Y")
		Expect(b2).To(BeIdenticalTo(b))
	})
})

var _ = Describe("step dispatch", func() {
	It("apply dispatches to replica callback", func() {
		called := false
		apply := ReplicaApplyFunc[*testGCtx, *testReplicaCtx](func(*testGCtx, *testReplicaCtx) { called = true })
		s := ReplicaStep("step", apply, stubReplicaConfirm).build()
		s.apply(&testGCtx{}, &testReplicaCtx{})
		Expect(called).To(BeTrue())
	})

	It("apply dispatches to global callback", func() {
		called := false
		apply := GlobalApplyFunc[*testGCtx](func(*testGCtx) { called = true })
		s := buildGlobalStep[*testGCtx, *testReplicaCtx](GlobalStep("step", apply, stubGlobalConfirm))
		s.apply(&testGCtx{}, &testReplicaCtx{})
		Expect(called).To(BeTrue())
	})

	It("confirm dispatches to replica callback", func() {
		confirm := ReplicaConfirmFunc[*testGCtx, *testReplicaCtx](func(*testGCtx, *testReplicaCtx, int64) ConfirmResult {
			return ConfirmResult{MustConfirm: 0b111}
		})
		s := ReplicaStep("step", stubReplicaApply, confirm).build()
		cr := s.confirm(&testGCtx{}, &testReplicaCtx{}, 5)
		Expect(cr.MustConfirm.Len()).To(Equal(3))
	})

	It("confirm dispatches to global callback", func() {
		confirm := GlobalConfirmFunc[*testGCtx](func(*testGCtx, int64) ConfirmResult {
			return ConfirmResult{MustConfirm: 0b11}
		})
		s := buildGlobalStep[*testGCtx, *testReplicaCtx](GlobalStep("step", stubGlobalApply, confirm))
		cr := s.confirm(&testGCtx{}, &testReplicaCtx{}, 5)
		Expect(cr.MustConfirm.Len()).To(Equal(2))
	})

	It("apply panics on invalid scope", func() {
		s := step[*testGCtx, *testReplicaCtx]{name: "bad", scope: Scope(99)}
		Expect(func() { s.apply(&testGCtx{}, &testReplicaCtx{}) }).To(Panic())
	})

	It("confirm panics on invalid scope", func() {
		s := step[*testGCtx, *testReplicaCtx]{name: "bad", scope: Scope(99)}
		Expect(func() { s.confirm(&testGCtx{}, &testReplicaCtx{}, 1) }).To(Panic())
	})
})
