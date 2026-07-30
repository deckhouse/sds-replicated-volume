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

// The table below is the whole contract of the opt-in gate. Both the Disruptive
// and the LongHaul gate call optInEnabled with their own variable and the
// umbrella one, so covering the function covers both classes.
var _ = Describe("optInEnabled", func() {
	DescribeTable("decides from the two values alone",
		func(classVar, runAllVar string, enabled bool) {
			Expect(optInEnabled(classVar, runAllVar)).To(Equal(enabled))
		},

		// Nothing set — every opt-in class is off.
		Entry("both empty", "", "", false),

		// The class variable spelled as a true boolean.
		Entry("class true", "true", "", true),
		Entry("class TRUE", "TRUE", "", true),
		Entry("class True", "True", "", true),
		Entry("class 1", "1", "", true),
		Entry("class t", "t", "", true),

		// The regression this gate exists for: E2E_ALLOW_DISRUPTIVE=false used to
		// switch the class ON, because the old check was `!= ""`.
		Entry("class false", "false", "", false),
		Entry("class 0", "0", "", false),
		Entry("class f", "f", "", false),

		// Values ParseBool cannot read fall back to "off" — a value nobody meant
		// as a yes must not enable a destructive class.
		Entry("class yes", "yes", "", false),
		Entry("class on", "on", "", false),
		Entry("class arbitrary text", "please", "", false),
		Entry("class blank space", " ", "", false),

		// The umbrella variable, parsed by the very same rule.
		Entry("umbrella true, class empty", "", "true", true),
		Entry("umbrella 1, class empty", "", "1", true),
		Entry("umbrella false, class empty", "", "false", false),
		Entry("umbrella garbage, class empty", "", "sure", false),

		// No veto in either direction: either value being true is enough.
		Entry("umbrella false, class true", "true", "false", true),
		Entry("umbrella true, class false", "false", "true", true),
		Entry("both true", "true", "true", true),
	)
})

var _ = Describe("hasLabelInArgs", func() {
	It("finds the label in a Labels arg", func() {
		Expect(hasLabelInArgs([]any{Labels{"Smoke", LabelLongHaul}}, LabelLongHaul)).To(BeTrue())
	})

	It("returns false when another label is present", func() {
		Expect(hasLabelInArgs([]any{Labels{"Smoke"}}, LabelLongHaul)).To(BeFalse())
	})

	It("returns false when there are no Labels args at all", func() {
		Expect(hasLabelInArgs([]any{"text", func() {}}, LabelLongHaul)).To(BeFalse())
	})

	It("searches every Labels arg", func() {
		args := []any{Labels{"Smoke"}, "text", Labels{LabelDisruptive}}
		Expect(hasLabelInArgs(args, LabelDisruptive)).To(BeTrue())
	})
})

var _ = Describe("focusRequested", func() {
	It("is false for an unfocused run", func() {
		Expect(focusRequested(nil, nil)).To(BeFalse())
	})

	It("is true when --focus was given", func() {
		Expect(focusRequested([]string{"Layout"}, nil)).To(BeTrue())
	})

	It("is true when --focus-file was given", func() {
		Expect(focusRequested(nil, []string{"layout_alert_test.go"})).To(BeTrue())
	})
})
