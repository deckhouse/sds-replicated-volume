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
)

var _ = Describe("SetupLayout", Label(fw.LabelSlow), func() {
	DescribeTable("creates a fully formed RV with the correct layout",
		func(ctx SpecContext, l fw.TestLayout) {
			trv := f.SetupLayout(ctx, l)

			rv := trv.Object()
			members := rv.Status.Datamesh.Members
			Expect(members).To(HaveLen(l.ExpectedReplicas()))

			D := int(l.FTT) + int(l.GMDR) + 1
			wantTB := 0
			if D%2 == 0 && D > 0 && int(l.FTT) == D/2 {
				wantTB = 1
			}

			var gotD, gotTB, gotA, gotAttached int
			for _, m := range members {
				switch m.Type {
				case v1alpha1.DatameshMemberTypeDiskful:
					gotD++
				case v1alpha1.DatameshMemberTypeTieBreaker:
					gotTB++
				case v1alpha1.DatameshMemberTypeAccess:
					gotA++
				}
				if m.Attached {
					gotAttached++
				}
			}
			Expect(gotD).To(Equal(D), "diskful member count")
			Expect(gotTB).To(Equal(wantTB), "tiebreaker member count")
			Expect(gotA).To(Equal(l.Access), "access member count")
			Expect(gotAttached).To(BeNumerically(">=", l.Attached), "attached member count")

			Expect(trv.RVRCount()).To(Equal(l.ExpectedReplicas()), "RVR count")

			for _, trvr := range trv.TestRVRs() {
				obj := trvr.Object()
				if obj.Spec.Type == v1alpha1.ReplicaTypeDiskful {
					trvr.DRBDR().Await(ctx, DRBDR.DiskState(v1alpha1.DiskStateUpToDate))
				}
			}

			trv.Await(ctx, RV.FormationComplete())
		},

		Entry("1D",
			fw.TestLayout{FTT: 0, GMDR: 0}),
		Entry("2D",
			require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 1}),
		Entry("2D+1TB",
			require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 0}),
		Entry("3D",
			require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 1}),

		Entry("1D+1A",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 0, Access: 1, Attached: 1}),
		Entry("2D+1A",
			SpecTimeout(2*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 0, GMDR: 1, Access: 1, Attached: 1}),
		Entry("1D+2A",
			SpecTimeout(2*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 0, GMDR: 0, Access: 2, Attached: 2}),

		Entry("1D (1att)",
			fw.TestLayout{FTT: 0, GMDR: 0, Attached: 1}),
		Entry("2D (2att)",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 1, Attached: 2}),
		Entry("2D+1TB (1att)",
			SpecTimeout(2*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 0, Attached: 1}),

		Entry("1D+1A (1att)",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 0, Access: 1, Attached: 1}),
	)
})
