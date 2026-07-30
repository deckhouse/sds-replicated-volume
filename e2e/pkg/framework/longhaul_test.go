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

var _ = Describe("hasLongHaulLabel", func() {
	It("returns true when Labels contain LongHaul", func() {
		args := []any{Labels{"Slow", LabelLongHaul}}
		Expect(hasLongHaulLabel(args)).To(BeTrue())
	})

	It("returns false when no LongHaul label", func() {
		args := []any{Labels{"Slow", LabelDisruptive}}
		Expect(hasLongHaulLabel(args)).To(BeFalse())
	})

	It("returns false when no Labels at all", func() {
		args := []any{"text", func() {}}
		Expect(hasLongHaulLabel(args)).To(BeFalse())
	})

	It("finds LongHaul among multiple Labels args", func() {
		args := []any{Labels{"Slow"}, Labels{LabelLongHaul}}
		Expect(hasLongHaulLabel(args)).To(BeTrue())
	})

	// The transformer leaves a spec alone when it carries both labels, so the
	// Disruptive placement (Serial + lowest priority) is never contradicted by a
	// second, opposite SpecPriority.
	It("is reported alongside Disruptive so the transformer can defer to it", func() {
		args := []any{Labels{LabelLongHaul, LabelDisruptive}}
		Expect(hasLongHaulLabel(args)).To(BeTrue())
		Expect(hasDisruptiveLabel(args)).To(BeTrue())
	})
})
