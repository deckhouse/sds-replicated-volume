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

package selftest

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	fw "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework"
)

var _ = Describe("Framework Initialization", Label(fw.LabelSmoke), func() {
	It("discovers cluster nodes", func() {
		nodes := f.Discovery.EligibleNodes()
		Expect(nodes).NotTo(BeEmpty(), "no eligible nodes discovered")
		Expect(len(nodes)).To(BeNumerically(">=", 1))
	})

	It("sets worker ID and prefix", func() {
		Expect(f.WorkerID).To(BeNumerically(">=", 0))
		Expect(f.Name("test")).To(HavePrefix("e2e-"))
	})

	It("has a functional client and cache", func() {
		Expect(f.Client).NotTo(BeNil())
		Expect(f.Cache).NotTo(BeNil())
		Expect(f.Scheme).NotTo(BeNil())
		Expect(f.Debugger).NotTo(BeNil())
	})
})
