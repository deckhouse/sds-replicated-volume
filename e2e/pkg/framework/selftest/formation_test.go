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

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	fw "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework"
	. "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/match"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/require"
	. "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

var _ = Describe("Formation", Label(fw.LabelSmoke), func() {
	Context("1D", func() {
		It("creates a single-replica volume and passes safety invariants", func(ctx SpecContext) {
			trv := f.TestRV().Size("2Mi")
			trv.Create(ctx)
			trv.Await(ctx, RV.FormationComplete())

			Expect(trv.RVRCount()).To(Equal(1))

			rv := trv.Object()
			Expect(rv.Spec.ConfigurationMode).To(
				Equal(v1alpha1.ReplicatedVolumeConfigurationModeAuto))
			Expect(rv.Spec.Size.String()).To(Equal("2Mi"))
			Expect(rv.Spec.ReplicatedStorageClassName).NotTo(BeEmpty())
			Expect(rv.Name).To(HavePrefix("e2e-"))
			Expect(rv.Status.DatameshRevision).To(BeNumerically(">", 0))

			trvr := trv.TestRVR(0)
			Expect(trvr.Object().Spec.Type).To(Equal(v1alpha1.ReplicaTypeDiskful))
			Expect(trvr.Object().Spec.ReplicatedVolumeName).To(Equal(trv.Name()))

			trv.ActivateSafetyInvariants()
			time.Sleep(3 * time.Second)
			Expect(trv.RVRCount()).To(BeNumerically(">=", 1))
		})
	})

	Context("3D", require.MinNodes(3), func() {
		It("forms a three-replica volume with correct quorum", func(ctx SpecContext) {
			trv := f.TestRV().FTT(1).GMDR(1)
			trv.Create(ctx)
			trv.Await(ctx, RV.FormationComplete())

			Expect(trv.RVRCount()).To(Equal(3))

			for i := 0; i < 3; i++ {
				trvr := trv.TestRVR(i)
				Expect(trvr.Object()).To(
					Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy)))
			}

			rv := trv.Object()
			Expect(rv.Status.Datamesh.Quorum).To(Equal(byte(2)),
				"3D quorum should be 2 (voters/2+1 = 3/2+1)")
			Expect(rv.Status.Datamesh.QuorumMinimumRedundancy).To(Equal(byte(2)))
			Expect(rv.Status.Datamesh.Members).To(HaveLen(3))

			for _, m := range rv.Status.Datamesh.Members {
				Expect(m.Type).To(Equal(v1alpha1.DatameshMemberTypeDiskful))
			}
		})
	})
})
