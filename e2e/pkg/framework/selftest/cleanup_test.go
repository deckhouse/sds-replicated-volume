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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	fw "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework"
	. "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/match"
)

var _ = Describe("Cleanup", Label(fw.LabelSmoke), func() {
	Context("verifies cleanup completeness", Ordered, func() {
		var rvName string

		It("creates resources that will be cleaned up", func(ctx SpecContext) {
			trv := f.TestRV()
			trv.Create(ctx)
			trv.Await(ctx, RV.FormationComplete())
			trv.Attach(ctx, f.Discovery.AnyNode())
			rvName = trv.Name()

			rv := &v1alpha1.ReplicatedVolume{}
			Expect(f.Cache.Get(ctx, types.NamespacedName{Name: rvName}, rv)).To(Succeed())
		})

		It("verifies all resources are gone after cleanup", func(ctx SpecContext) {
			Expect(rvName).NotTo(BeEmpty(), "rvName should be set by previous It")

			Eventually(func(g Gomega) {
				rv := &v1alpha1.ReplicatedVolume{}
				err := f.Cache.Get(ctx, types.NamespacedName{Name: rvName}, rv)
				g.Expect(err).To(HaveOccurred(), "RV should be deleted")
			}, 10*time.Second, 500*time.Millisecond).Should(Succeed())

			Eventually(func(g Gomega) {
				rvrList := &v1alpha1.ReplicatedVolumeReplicaList{}
				g.Expect(f.Cache.List(ctx, rvrList)).To(Succeed())
				for _, rvr := range rvrList.Items {
					g.Expect(rvr.Spec.ReplicatedVolumeName).NotTo(Equal(rvName),
						"no RVRs should reference the cleaned-up RV")
				}
			}, 1*time.Second, 200*time.Millisecond).Should(Succeed())

			Eventually(func(g Gomega) {
				rvaList := &v1alpha1.ReplicatedVolumeAttachmentList{}
				g.Expect(f.Cache.List(ctx, rvaList)).To(Succeed())
				for _, rva := range rvaList.Items {
					g.Expect(rva.Spec.ReplicatedVolumeName).NotTo(Equal(rvName),
						"no RVAs should reference the cleaned-up RV")
				}
			}, 1*time.Second, 200*time.Millisecond).Should(Succeed())
		})
	})
})
