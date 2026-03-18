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

var _ = Describe("Node Exec", Label(fw.LabelSmoke), func() {
	It("Drbdsetup returns zero exit code for a valid command", func(ctx SpecContext) {
		result := f.Drbdsetup(ctx, f.Discovery.AnyNode(), "show")
		Expect(result.ExitCode).To(Equal(0))
	})

	It("LVM returns zero exit code and non-empty output", func(ctx SpecContext) {
		result := f.LVM(ctx, f.Discovery.AnyNode(), "vgs")
		Expect(result.ExitCode).To(Equal(0))
		Expect(result.Stdout).NotTo(BeEmpty())
	})

	It("returns non-zero exit code without failing the test", func(ctx SpecContext) {
		result := f.Drbdsetup(ctx, f.Discovery.AnyNode(), "status", "nonexistent-resource-name-e2e-selftest")
		Expect(result.ExitCode).NotTo(Equal(0))
	})
})
