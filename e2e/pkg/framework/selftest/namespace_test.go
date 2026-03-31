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
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	fw "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework"
	. "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

var _ = Describe("Namespace", Label(fw.LabelSmoke), func() {
	Context("per-spec (TestNS)", func() {
		It("creates a namespace with e2e labels", func(ctx SpecContext) {
			tns := f.TestNS()
			tns.Create(ctx)

			ns := tns.Object()
			Expect(ns.Name).To(HavePrefix("e2e-"))
			Expect(ns.Labels).To(HaveKey(fw.LabelE2ERunKey))
			Expect(ns.Labels[fw.LabelE2ERunKey]).To(Equal(f.RunID()))
			Expect(ns.Labels).To(HaveKey(fw.LabelE2EWorkerKey))
			Expect(ns.Labels[fw.LabelE2EWorkerKey]).To(Equal(strconv.Itoa(f.WorkerID)))
		})

		Context("is deleted by DeferCleanup after the spec", Ordered, func() {
			var nsName string

			It("creates a namespace", func(ctx SpecContext) {
				tns := f.TestNS()
				tns.Create(ctx)
				nsName = tns.Name()
			})

			It("verifies the namespace is gone", func(ctx SpecContext) {
				Expect(nsName).NotTo(BeEmpty())
				tns := f.TestNSExact(nsName)
				tns.Get(ctx)
				tns.Await(ctx, Deleted())
			})
		})
	})

	Context("shared (SharedNS)", func() {
		It("is created and accessible via SharedNS", func(ctx SpecContext) {
			tns := f.SharedNS()
			tns.CreateShared(ctx)
			Expect(tns.Name()).To(HavePrefix("e2e-"))
		})

		It("exists on cluster with e2e run label", func(ctx SpecContext) {
			tns := f.SharedNS()
			tns.CreateShared(ctx)
			ns := tns.Object()
			Expect(ns.Labels).To(HaveKey(fw.LabelE2ERunKey))
			Expect(ns.Labels[fw.LabelE2ERunKey]).To(Equal(f.RunID()))
		})

		It("does not have a worker label", func(ctx SpecContext) {
			tns := f.SharedNS()
			tns.CreateShared(ctx)
			ns := tns.Object()
			Expect(ns.Labels).NotTo(HaveKey(fw.LabelE2EWorkerKey))
		})

		It("returns the same cached instance on repeated calls", func(ctx SpecContext) {
			a := f.SharedNS()
			a.CreateShared(ctx)
			b := f.SharedNS()
			b.CreateShared(ctx)
			Expect(a.Name()).To(Equal(b.Name()))
		})
	})
})
