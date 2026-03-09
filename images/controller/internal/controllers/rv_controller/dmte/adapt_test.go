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

	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/idset"
)

// ──────────────────────────────────────────────────────────────────────────────
// Scope adapters
//

var _ = Describe("AdaptGlobalApply", func() {
	It("wraps global apply, ignores rctx", func() {
		called := false
		global := func(_ *testGCtx) bool { called = true; return true }
		adapted := AdaptGlobalApply[*testGCtx, *testReplicaCtx](global)

		result := adapted(&testGCtx{}, &testReplicaCtx{})
		Expect(called).To(BeTrue())
		Expect(result).To(BeTrue())
	})
})

var _ = Describe("AdaptGlobalConfirm", func() {
	It("wraps global confirm, ignores rctx", func() {
		global := func(_ *testGCtx, _ int64) ConfirmResult {
			return ConfirmResult{MustConfirm: idset.Of(0), Confirmed: idset.Of(0)}
		}
		adapted := AdaptGlobalConfirm[*testGCtx, *testReplicaCtx](global)

		result := adapted(&testGCtx{}, &testReplicaCtx{}, 10)
		Expect(result.MustConfirm).To(Equal(idset.Of(0)))
		Expect(result.Confirmed).To(Equal(idset.Of(0)))
	})
})

var _ = Describe("AdaptGlobalOnComplete", func() {
	It("wraps global onComplete, ignores rctx", func() {
		called := false
		global := func(_ *testGCtx) { called = true }
		adapted := AdaptGlobalOnComplete[*testGCtx, *testReplicaCtx](global)

		adapted(&testGCtx{}, &testReplicaCtx{})
		Expect(called).To(BeTrue())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Cross-category adapter
//

var _ = Describe("AdaptApplyToOnComplete", func() {
	It("discards bool return", func() {
		called := false
		apply := func(_ *testGCtx) bool { called = true; return true }
		adapted := AdaptApplyToOnComplete(apply)

		adapted(&testGCtx{}) // returns nothing
		Expect(called).To(BeTrue())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Combinators
//

var _ = Describe("ComposeGlobalApply", func() {
	It("OR of results, calls all", func() {
		calls := 0
		a := func(*testGCtx) bool { calls++; return false }
		b := func(*testGCtx) bool { calls++; return true }
		c := func(*testGCtx) bool { calls++; return false }

		result := ComposeGlobalApply(a, b, c)(&testGCtx{})
		Expect(result).To(BeTrue())
		Expect(calls).To(Equal(3))
	})

	It("all false → false", func() {
		a := func(*testGCtx) bool { return false }
		b := func(*testGCtx) bool { return false }

		Expect(ComposeGlobalApply(a, b)(&testGCtx{})).To(BeFalse())
	})
})

var _ = Describe("ComposeReplicaApply", func() {
	It("OR of results, calls all", func() {
		calls := 0
		a := func(*testGCtx, *testReplicaCtx) bool { calls++; return false }
		b := func(*testGCtx, *testReplicaCtx) bool { calls++; return true }

		result := ComposeReplicaApply(a, b)(&testGCtx{}, &testReplicaCtx{})
		Expect(result).To(BeTrue())
		Expect(calls).To(Equal(2))
	})

	It("all false → false", func() {
		a := func(*testGCtx, *testReplicaCtx) bool { return false }
		Expect(ComposeReplicaApply(a)(&testGCtx{}, &testReplicaCtx{})).To(BeFalse())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// SetChanged
//

var _ = Describe("SetChanged", func() {
	It("returns true and sets value when different", func() {
		v := 1
		Expect(SetChanged(&v, 2)).To(BeTrue())
		Expect(v).To(Equal(2))
	})

	It("returns false when same", func() {
		v := 1
		Expect(SetChanged(&v, 1)).To(BeFalse())
		Expect(v).To(Equal(1))
	})

	It("works with strings", func() {
		v := "a"
		Expect(SetChanged(&v, "b")).To(BeTrue())
		Expect(v).To(Equal("b"))
	})

	It("works with bools", func() {
		v := false
		Expect(SetChanged(&v, true)).To(BeTrue())
		Expect(v).To(BeTrue())
	})
})
