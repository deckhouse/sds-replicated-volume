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

// Evaluated while this package's var initializers run — the phase in which
// Ginkgo builds every TOP-LEVEL node, before the spec tree exists. Reading the
// container chain there must answer "nothing inherited" instead of panicking:
// the LongHaul transformer runs on containers too, and every spec file in
// e2e/full opens with a top-level Describe, so a panic here takes the whole
// suite down at init.
var (
	topLevelLongHaulApplies    = longHaulPriorityApplies([]any{Labels{LabelLongHaul}})
	topLevelDisruptiveDefers   = longHaulPriorityApplies([]any{Labels{LabelLongHaul, LabelDisruptive}})
	topLevelCollectedNodeLabel = collectNodeLabels([]any{Labels{LabelLongHaul}})
)

// longHaulPriorityApplies may only run during tree construction (it reads the
// container chain of the node being built), so every case is decided in a
// container body and the verdict is asserted afterwards from the spec.
var _ = Describe("longHaulPriorityApplies", func() {
	It("injects the priority for a top-level LongHaul container", func() {
		Expect(topLevelLongHaulApplies).To(BeTrue())
		Expect(topLevelCollectedNodeLabel).To(ConsistOf(LabelLongHaul))
	})

	It("defers on a top-level container carrying both labels", func() {
		Expect(topLevelDisruptiveDefers).To(BeFalse())
	})

	appliesToLongHaulNode := longHaulPriorityApplies([]any{Labels{LabelLongHaul}})
	appliesToPlainNode := longHaulPriorityApplies([]any{Labels{LabelSlow}})
	appliesToNodeWithBothLabels := longHaulPriorityApplies(
		[]any{Labels{LabelLongHaul, LabelDisruptive}},
	)

	It("injects the priority for a LongHaul node under a plain container", func() {
		Expect(appliesToLongHaulNode).To(BeTrue())
	})

	It("leaves a node without the LongHaul label alone", func() {
		Expect(appliesToPlainNode).To(BeFalse())
	})

	It("defers to Disruptive written on the same node", func() {
		Expect(appliesToNodeWithBothLabels).To(BeFalse())
	})

	// Regression pin: Disruptive on a container plus LongHaul on the It is how
	// the two classes actually meet in this suite (Disruptive is written on the
	// Describe across the whole suite). Ginkgo resolves a spec's priority from
	// the innermost node that sets one, so injecting MaxInt on the It here would
	// beat the container's MinInt and hoist a destructive spec to the FRONT of
	// the serial phase — the Disruptive placement must win instead.
	Describe("under a Disruptive container", Label(LabelDisruptive), func() {
		appliesUnderDisruptiveContainer := longHaulPriorityApplies(
			[]any{Labels{LabelLongHaul}},
		)

		It("defers to Disruptive inherited from the container", func() {
			Expect(appliesUnderDisruptiveContainer).To(BeFalse())
		})
	})
})
