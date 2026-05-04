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

var _ = Describe("Name", func() {
	It("constructs prefixed name", func() {
		f := &Framework{prefix: "e2e-abc123"}
		Expect(f.Name("test")).To(Equal("e2e-abc123-test"))
	})

	It("uses different prefix for different runID", func() {
		f := &Framework{prefix: "e2e-ff0011"}
		Expect(f.Name("vol")).To(Equal("e2e-ff0011-vol"))
	})

	It("handles empty suffix", func() {
		f := &Framework{prefix: "e2e-abc123"}
		Expect(f.Name("")).To(Equal("e2e-abc123-"))
	})
})

var _ = Describe("generateRunID", func() {
	It("returns 6-char hex string", func() {
		id := generateRunID()
		Expect(id).To(HaveLen(6))
		Expect(id).To(MatchRegexp(`^[0-9a-f]{6}$`))
	})

	It("generates different IDs", func() {
		ids := make(map[string]bool)
		for i := 0; i < 100; i++ {
			ids[generateRunID()] = true
		}
		Expect(len(ids)).To(BeNumerically(">", 90))
	})
})

var _ = Describe("RunID", func() {
	It("returns stored runID", func() {
		f := &Framework{runID: "abc123"}
		Expect(f.RunID()).To(Equal("abc123"))
	})
})
