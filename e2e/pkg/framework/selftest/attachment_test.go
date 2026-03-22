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

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	fw "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework"
	. "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/match"
)

var _ = Describe("Attachment", Label(fw.LabelSmoke), func() {
	It("attaches a volume and observes an attached member", func(ctx SpecContext) {
		trv := f.TestRV()
		trv.Create(ctx)
		trv.Await(ctx, RV.FormationComplete())

		trv.Attach(ctx, f.Discovery.AnyNode())

		trv.Await(ctx, RV.Custom("has attached member", func(rv *v1alpha1.ReplicatedVolume) bool {
			for _, m := range rv.Status.Datamesh.Members {
				if m.Attached {
					return true
				}
			}
			return false
		}))

		rv := trv.Object()
		attachedCount := 0
		for _, m := range rv.Status.Datamesh.Members {
			if m.Attached {
				attachedCount++
			}
		}
		Expect(attachedCount).To(BeNumerically(">=", 1))
	})
})
