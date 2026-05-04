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

package full

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	fw "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/require"
)

var _ = Describe("Migration: RV adopt/v1 with excess replicas", Label(fw.LabelUpgrade, fw.LabelSlow), func() {
	DescribeTable("adopts when source has more replicas than adopt RSC expects",
		func(ctx SpecContext, sourceLayout, adoptLayout fw.TestLayout) {
			res := setupAdoptPreexisting(ctx, sourceLayout)

			fmt.Fprintf(GinkgoWriter, "[adopt-excess] creating RV %s with adopt RSC (FTT=%d, GMDR=%d), source has %d replicas\n",
				res.RVName, adoptLayout.FTT, adoptLayout.GMDR, len(res.Replicas))

			trv := f.TestRVExact(res.RVName).Adopt().AdoptSharedSecret(res.AdoptSecret).FTT(adoptLayout.FTT).GMDR(adoptLayout.GMDR)
			if sourceLayout.Attached > 0 {
				trv = trv.MaxAttachments(byte(sourceLayout.Attached))
			}
			trv.Create(ctx)

			for _, r := range res.Replicas {
				if r.Role == v1alpha1.DRBDRolePrimary {
					trv.Attach(ctx, r.NodeName)
				}
			}

			finishAdopt(ctx, trv, res)
		},

		// ── Source 2D (FTT=0, GMDR=1) — adopt RSC(0,0): expects 1D, has 2D ──

		Entry("2D, adopt RSC(0,0)",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 1}, fw.TestLayout{FTT: 0, GMDR: 0}),
		Entry("2D (1att), adopt RSC(0,0)",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 1, Attached: 1}, fw.TestLayout{FTT: 0, GMDR: 0}),
		Entry("2D (2att), adopt RSC(0,0)",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 1, Attached: 2}, fw.TestLayout{FTT: 0, GMDR: 0}),
		Entry("2D+1A (1att), adopt RSC(0,0)",
			SpecTimeout(2*time.Minute), require.MinNodes(2, 1),
			fw.TestLayout{FTT: 0, GMDR: 1, Access: 1, Attached: 1}, fw.TestLayout{FTT: 0, GMDR: 0}),
		Entry("2D+1A (2att), adopt RSC(0,0)",
			SpecTimeout(2*time.Minute), require.MinNodes(2, 1),
			fw.TestLayout{FTT: 0, GMDR: 1, Access: 1, Attached: 2}, fw.TestLayout{FTT: 0, GMDR: 0}),
		Entry("2D+2A (2att), adopt RSC(0,0)",
			SpecTimeout(2*time.Minute), require.MinNodes(2, 2),
			fw.TestLayout{FTT: 0, GMDR: 1, Access: 2, Attached: 2}, fw.TestLayout{FTT: 0, GMDR: 0}),

		// ── Source 2D+1TB (FTT=1, GMDR=0) — adopt RSC(0,0): expects 1D+0TB, has 2D+1TB ──

		Entry("2D+1TB, adopt RSC(0,0)",
			SpecTimeout(3*time.Minute), require.MinNodes(2, 1),
			fw.TestLayout{FTT: 1, GMDR: 0}, fw.TestLayout{FTT: 0, GMDR: 0}),
		Entry("2D+1TB (1att), adopt RSC(0,0)",
			SpecTimeout(3*time.Minute), require.MinNodes(2, 1),
			fw.TestLayout{FTT: 1, GMDR: 0, Attached: 1}, fw.TestLayout{FTT: 0, GMDR: 0}),
		Entry("2D+1TB (2att), adopt RSC(0,0)",
			Label("Bug:MultiattachDRBDLock"),
			SpecTimeout(3*time.Minute), require.MinNodes(2, 1),
			fw.TestLayout{FTT: 1, GMDR: 0, Attached: 2}, fw.TestLayout{FTT: 0, GMDR: 0}),
		Entry("2D+1TB+1A (1att), adopt RSC(0,0)",
			SpecTimeout(3*time.Minute), require.MinNodes(2, 2),
			fw.TestLayout{FTT: 1, GMDR: 0, Access: 1, Attached: 1}, fw.TestLayout{FTT: 0, GMDR: 0}),
		Entry("2D+1TB+1A (2att), adopt RSC(0,0)",
			Label("Bug:MultiattachDRBDLock"),
			SpecTimeout(3*time.Minute), require.MinNodes(2, 2),
			fw.TestLayout{FTT: 1, GMDR: 0, Access: 1, Attached: 2}, fw.TestLayout{FTT: 0, GMDR: 0}),
		Entry("2D+1TB+2A (2att), adopt RSC(0,0)",
			Label("Bug:MultiattachDRBDLock"),
			SpecTimeout(3*time.Minute), require.MinNodes(2, 3),
			fw.TestLayout{FTT: 1, GMDR: 0, Access: 2, Attached: 2}, fw.TestLayout{FTT: 0, GMDR: 0}),

		// ── Source 2D+1TB (FTT=1, GMDR=0) — adopt RSC(0,1): expects 2D+0TB, has 2D+1TB ──

		Entry("2D+1TB, adopt RSC(0,1)",
			SpecTimeout(3*time.Minute), require.MinNodes(2, 1),
			fw.TestLayout{FTT: 1, GMDR: 0}, fw.TestLayout{FTT: 0, GMDR: 1}),
		Entry("2D+1TB (1att), adopt RSC(0,1)",
			SpecTimeout(3*time.Minute), require.MinNodes(2, 1),
			fw.TestLayout{FTT: 1, GMDR: 0, Attached: 1}, fw.TestLayout{FTT: 0, GMDR: 1}),
		Entry("2D+1TB (2att), adopt RSC(0,1)",
			Label("Bug:MultiattachDRBDLock"),
			SpecTimeout(3*time.Minute), require.MinNodes(2, 1),
			fw.TestLayout{FTT: 1, GMDR: 0, Attached: 2}, fw.TestLayout{FTT: 0, GMDR: 1}),
		Entry("2D+1TB+1A (1att), adopt RSC(0,1)",
			SpecTimeout(3*time.Minute), require.MinNodes(2, 2),
			fw.TestLayout{FTT: 1, GMDR: 0, Access: 1, Attached: 1}, fw.TestLayout{FTT: 0, GMDR: 1}),
		Entry("2D+1TB+1A (2att), adopt RSC(0,1)",
			Label("Bug:MultiattachDRBDLock"),
			SpecTimeout(3*time.Minute), require.MinNodes(2, 2),
			fw.TestLayout{FTT: 1, GMDR: 0, Access: 1, Attached: 2}, fw.TestLayout{FTT: 0, GMDR: 1}),
		Entry("2D+1TB+2A (2att), adopt RSC(0,1)",
			Label("Bug:MultiattachDRBDLock"),
			SpecTimeout(3*time.Minute), require.MinNodes(2, 3),
			fw.TestLayout{FTT: 1, GMDR: 0, Access: 2, Attached: 2}, fw.TestLayout{FTT: 0, GMDR: 1}),

		// ── Source 3D (FTT=1, GMDR=1) — adopt RSC(0,0): expects 1D, has 3D ──

		Entry("3D, adopt RSC(0,0)",
			SpecTimeout(3*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 1}, fw.TestLayout{FTT: 0, GMDR: 0}),
		Entry("3D (1att), adopt RSC(0,0)",
			SpecTimeout(3*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 1, Attached: 1}, fw.TestLayout{FTT: 0, GMDR: 0}),
		Entry("3D (2att), adopt RSC(0,0)",
			SpecTimeout(3*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 1, Attached: 2}, fw.TestLayout{FTT: 0, GMDR: 0}),
		Entry("3D+1A (1att), adopt RSC(0,0)",
			SpecTimeout(3*time.Minute), require.MinNodes(3, 1),
			fw.TestLayout{FTT: 1, GMDR: 1, Access: 1, Attached: 1}, fw.TestLayout{FTT: 0, GMDR: 0}),
		Entry("3D+1A (2att), adopt RSC(0,0)",
			SpecTimeout(3*time.Minute), require.MinNodes(3, 1),
			fw.TestLayout{FTT: 1, GMDR: 1, Access: 1, Attached: 2}, fw.TestLayout{FTT: 0, GMDR: 0}),
		Entry("3D+2A (2att), adopt RSC(0,0)",
			SpecTimeout(3*time.Minute), require.MinNodes(3, 2),
			fw.TestLayout{FTT: 1, GMDR: 1, Access: 2, Attached: 2}, fw.TestLayout{FTT: 0, GMDR: 0}),

		// ── Source 3D (FTT=1, GMDR=1) — adopt RSC(0,1): expects 2D, has 3D ──

		Entry("3D, adopt RSC(0,1)",
			SpecTimeout(3*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 1}, fw.TestLayout{FTT: 0, GMDR: 1}),
		Entry("3D (1att), adopt RSC(0,1)",
			SpecTimeout(3*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 1, Attached: 1}, fw.TestLayout{FTT: 0, GMDR: 1}),
		Entry("3D (2att), adopt RSC(0,1)",
			SpecTimeout(3*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 1, Attached: 2}, fw.TestLayout{FTT: 0, GMDR: 1}),
		Entry("3D+1A (1att), adopt RSC(0,1)",
			SpecTimeout(3*time.Minute), require.MinNodes(3, 1),
			fw.TestLayout{FTT: 1, GMDR: 1, Access: 1, Attached: 1}, fw.TestLayout{FTT: 0, GMDR: 1}),
		Entry("3D+1A (2att), adopt RSC(0,1)",
			SpecTimeout(3*time.Minute), require.MinNodes(3, 1),
			fw.TestLayout{FTT: 1, GMDR: 1, Access: 1, Attached: 2}, fw.TestLayout{FTT: 0, GMDR: 1}),
		Entry("3D+2A (2att), adopt RSC(0,1)",
			SpecTimeout(3*time.Minute), require.MinNodes(3, 2),
			fw.TestLayout{FTT: 1, GMDR: 1, Access: 2, Attached: 2}, fw.TestLayout{FTT: 0, GMDR: 1}),

		// ── Source 4D+1TB (FTT=2, GMDR=1) — adopt RSC(1,0): expects 2D+1TB, has 4D+1TB ──

		Entry("4D+1TB, adopt RSC(1,0)",
			SpecTimeout(4*time.Minute), require.MinNodes(4, 1),
			fw.TestLayout{FTT: 2, GMDR: 1}, fw.TestLayout{FTT: 1, GMDR: 0}),
		Entry("4D+1TB (1att), adopt RSC(1,0)",
			SpecTimeout(4*time.Minute), require.MinNodes(4, 1),
			fw.TestLayout{FTT: 2, GMDR: 1, Attached: 1}, fw.TestLayout{FTT: 1, GMDR: 0}),
		Entry("4D+1TB (2att), adopt RSC(1,0)",
			Label("Bug:MultiattachDRBDLock"),
			SpecTimeout(4*time.Minute), require.MinNodes(4, 1),
			fw.TestLayout{FTT: 2, GMDR: 1, Attached: 2}, fw.TestLayout{FTT: 1, GMDR: 0}),
		Entry("4D+1TB+1A (1att), adopt RSC(1,0)",
			SpecTimeout(4*time.Minute), require.MinNodes(4, 2),
			fw.TestLayout{FTT: 2, GMDR: 1, Access: 1, Attached: 1}, fw.TestLayout{FTT: 1, GMDR: 0}),
		Entry("4D+1TB+1A (2att), adopt RSC(1,0)",
			Label("Bug:MultiattachDRBDLock"),
			SpecTimeout(4*time.Minute), require.MinNodes(4, 2),
			fw.TestLayout{FTT: 2, GMDR: 1, Access: 1, Attached: 2}, fw.TestLayout{FTT: 1, GMDR: 0}),
		Entry("4D+1TB+2A (2att), adopt RSC(1,0)",
			Label("Bug:MultiattachDRBDLock"),
			SpecTimeout(4*time.Minute), require.MinNodes(4, 3),
			fw.TestLayout{FTT: 2, GMDR: 1, Access: 2, Attached: 2}, fw.TestLayout{FTT: 1, GMDR: 0}),
	)
})
