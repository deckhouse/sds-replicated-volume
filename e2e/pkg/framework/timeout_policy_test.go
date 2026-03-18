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

package framework

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("parseTimeoutMultiplier", func() {
	It("valid float sets multiplier", func() {
		Expect(parseTimeoutMultiplier("2.5")).To(Equal(2.5))
	})

	It("invalid string defaults to 1.0", func() {
		Expect(parseTimeoutMultiplier("not-a-number")).To(Equal(1.0))
	})

	It("negative value defaults to 1.0", func() {
		Expect(parseTimeoutMultiplier("-3.0")).To(Equal(1.0))
	})

	It("zero defaults to 1.0", func() {
		Expect(parseTimeoutMultiplier("0")).To(Equal(1.0))
	})

	It("empty string defaults to 1.0", func() {
		Expect(parseTimeoutMultiplier("")).To(Equal(1.0))
	})

	It("valid integer is accepted", func() {
		Expect(parseTimeoutMultiplier("3")).To(Equal(3.0))
	})
})

var _ = Describe("hasContextBody", func() {
	It("returns true for func(SpecContext)", func() {
		Expect(hasContextBody([]any{func(_ SpecContext) {}})).To(BeTrue())
	})

	It("returns false for bare func()", func() {
		Expect(hasContextBody([]any{func() {}})).To(BeFalse())
	})

	It("returns false when no function in args", func() {
		Expect(hasContextBody([]any{"text", 42})).To(BeFalse())
	})

	It("finds context body among other args", func() {
		Expect(hasContextBody([]any{
			"description",
			Label("Smoke"),
			func(_ SpecContext) {},
		})).To(BeTrue())
	})
})
