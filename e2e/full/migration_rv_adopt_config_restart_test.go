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
	. "github.com/onsi/gomega"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	fw "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework"
	. "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/match"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/require"
)

var _ = Describe("Migration: RV adopt/v1 config restart", Label(fw.LabelUpgrade, fw.LabelSlow), func() {
	DescribeTable("restarts adopt when RSC configuration does not match and user fixes it",
		func(ctx SpecContext, sourceLayout, wrongLayout fw.TestLayout) {
			res := setupAdoptPreexisting(ctx, sourceLayout)

			// Create RV with WRONG RSC configuration.
			fmt.Fprintf(GinkgoWriter, "[adopt-config-restart] creating RV %s with wrong RSC (FTT=%d, GMDR=%d), expecting stuck\n",
				res.RVName, wrongLayout.FTT, wrongLayout.GMDR)

			trv := f.TestRVExact(res.RVName).Adopt().AdoptSharedSecret(res.AdoptSecret).FTT(wrongLayout.FTT).GMDR(wrongLayout.GMDR)
			if sourceLayout.Attached > 0 {
				trv = trv.MaxAttachments(byte(sourceLayout.Attached))
			}
			trv.Create(ctx)

			for _, r := range res.Replicas {
				if r.Role == v1alpha1.DRBDRolePrimary {
					trv.Attach(ctx, r.NodeName)
				}
			}

			// Await step 0 stuck on replica count mismatch (diskful or tiebreaker).
			formationType := v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation
			trv.Await(ctx, Or(
				RV.TransitionCurrentStepMessageContains(formationType, "Diskful replica count mismatch"),
				RV.TransitionCurrentStepMessageContains(formationType, "TieBreaker replica count mismatch"),
			))

			// Fix — switch RV to the correct RSC.
			fmt.Fprintf(GinkgoWriter, "[adopt-config-restart] switching RV %s to correct RSC %s\n",
				res.RVName, res.RSCName)

			trv.Update(ctx, func(rv *v1alpha1.ReplicatedVolume) {
				rv.Spec.ReplicatedStorageClassName = res.RSCName
			})

			// Await formation completion (same as normal adopt).
			finishAdopt(ctx, trv, res)
		},

		// ── Group 1: Source 1D (FTT=0, GMDR=0) — wrong RSCs: (0,1), (1,0), (1,1) ──
		// All hit diskful count mismatch (expects 2D or 3D, has 1D).

		Entry("1D, wrong RSC(0,1)",
			SpecTimeout(2*time.Minute),
			fw.TestLayout{FTT: 0, GMDR: 0}, fw.TestLayout{FTT: 0, GMDR: 1}),
		Entry("1D, wrong RSC(1,0)",
			SpecTimeout(2*time.Minute),
			fw.TestLayout{FTT: 0, GMDR: 0}, fw.TestLayout{FTT: 1, GMDR: 0}),
		Entry("1D, wrong RSC(1,1)",
			SpecTimeout(2*time.Minute),
			fw.TestLayout{FTT: 0, GMDR: 0}, fw.TestLayout{FTT: 1, GMDR: 1}),

		Entry("1D (1att), wrong RSC(0,1)",
			SpecTimeout(2*time.Minute),
			fw.TestLayout{FTT: 0, GMDR: 0, Attached: 1}, fw.TestLayout{FTT: 0, GMDR: 1}),
		Entry("1D (1att), wrong RSC(1,0)",
			SpecTimeout(2*time.Minute),
			fw.TestLayout{FTT: 0, GMDR: 0, Attached: 1}, fw.TestLayout{FTT: 1, GMDR: 0}),
		Entry("1D (1att), wrong RSC(1,1)",
			SpecTimeout(2*time.Minute),
			fw.TestLayout{FTT: 0, GMDR: 0, Attached: 1}, fw.TestLayout{FTT: 1, GMDR: 1}),

		Entry("1D+1A (1att), wrong RSC(0,1)",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 0, Access: 1, Attached: 1}, fw.TestLayout{FTT: 0, GMDR: 1}),
		Entry("1D+1A (1att), wrong RSC(1,0)",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 0, Access: 1, Attached: 1}, fw.TestLayout{FTT: 1, GMDR: 0}),
		Entry("1D+1A (1att), wrong RSC(1,1)",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 0, Access: 1, Attached: 1}, fw.TestLayout{FTT: 1, GMDR: 1}),

		Entry("1D+1A (2att), wrong RSC(0,1)",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 0, Access: 1, Attached: 2}, fw.TestLayout{FTT: 0, GMDR: 1}),
		Entry("1D+1A (2att), wrong RSC(1,0)",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 0, Access: 1, Attached: 2}, fw.TestLayout{FTT: 1, GMDR: 0}),
		Entry("1D+1A (2att), wrong RSC(1,1)",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 0, Access: 1, Attached: 2}, fw.TestLayout{FTT: 1, GMDR: 1}),

		Entry("1D+2A (2att), wrong RSC(0,1)",
			SpecTimeout(2*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 0, GMDR: 0, Access: 2, Attached: 2}, fw.TestLayout{FTT: 0, GMDR: 1}),
		Entry("1D+2A (2att), wrong RSC(1,0)",
			SpecTimeout(2*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 0, GMDR: 0, Access: 2, Attached: 2}, fw.TestLayout{FTT: 1, GMDR: 0}),
		Entry("1D+2A (2att), wrong RSC(1,1)",
			SpecTimeout(2*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 0, GMDR: 0, Access: 2, Attached: 2}, fw.TestLayout{FTT: 1, GMDR: 1}),

		// ── Group 2: Source 2D (FTT=0, GMDR=1) ──
		// Wrong RSC(1,1): hits diskful mismatch (expects 3D, has 2D).
		// Wrong RSC(1,0): hits tiebreaker mismatch (D=2 matches, expects 1TB, has 0TB).

		Entry("2D, wrong RSC(1,1)",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 1}, fw.TestLayout{FTT: 1, GMDR: 1}),
		Entry("2D (1att), wrong RSC(1,1)",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 1, Attached: 1}, fw.TestLayout{FTT: 1, GMDR: 1}),
		Entry("2D (2att), wrong RSC(1,1)",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 1, Attached: 2}, fw.TestLayout{FTT: 1, GMDR: 1}),
		Entry("2D+1A (1att), wrong RSC(1,1)",
			SpecTimeout(2*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 0, GMDR: 1, Access: 1, Attached: 1}, fw.TestLayout{FTT: 1, GMDR: 1}),
		Entry("2D+1A (2att), wrong RSC(1,1)",
			SpecTimeout(2*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 0, GMDR: 1, Access: 1, Attached: 2}, fw.TestLayout{FTT: 1, GMDR: 1}),
		Entry("2D+2A (2att), wrong RSC(1,1)",
			SpecTimeout(2*time.Minute), require.MinNodes(4),
			fw.TestLayout{FTT: 0, GMDR: 1, Access: 2, Attached: 2}, fw.TestLayout{FTT: 1, GMDR: 1}),

		Entry("2D, wrong RSC(1,0)",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 1}, fw.TestLayout{FTT: 1, GMDR: 0}),
		Entry("2D (1att), wrong RSC(1,0)",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 1, Attached: 1}, fw.TestLayout{FTT: 1, GMDR: 0}),
		Entry("2D (2att), wrong RSC(1,0)",
			SpecTimeout(2*time.Minute), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 1, Attached: 2}, fw.TestLayout{FTT: 1, GMDR: 0}),
		Entry("2D+1A (1att), wrong RSC(1,0)",
			SpecTimeout(2*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 0, GMDR: 1, Access: 1, Attached: 1}, fw.TestLayout{FTT: 1, GMDR: 0}),
		Entry("2D+1A (2att), wrong RSC(1,0)",
			SpecTimeout(2*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 0, GMDR: 1, Access: 1, Attached: 2}, fw.TestLayout{FTT: 1, GMDR: 0}),
		Entry("2D+2A (2att), wrong RSC(1,0)",
			SpecTimeout(2*time.Minute), require.MinNodes(4),
			fw.TestLayout{FTT: 0, GMDR: 1, Access: 2, Attached: 2}, fw.TestLayout{FTT: 1, GMDR: 0}),

		// ── Group 3: Source 2D+1TB (FTT=1, GMDR=0) ──
		// Wrong RSC(1,1): hits diskful mismatch (expects 3D, has 2D).
		// Note: RSC(0,1) would NOT get stuck (2D>=2D, 1TB>=0TB with >= semantics).

		Entry("2D+1TB, wrong RSC(1,1)",
			SpecTimeout(3*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 0}, fw.TestLayout{FTT: 1, GMDR: 1}),
		Entry("2D+1TB (1att), wrong RSC(1,1)",
			SpecTimeout(3*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 0, Attached: 1}, fw.TestLayout{FTT: 1, GMDR: 1}),
		Entry("2D+1TB (2att), wrong RSC(1,1)",
			Label("Bug:MultiattachDRBDLock"),
			SpecTimeout(3*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 0, Attached: 2}, fw.TestLayout{FTT: 1, GMDR: 1}),
		Entry("2D+1TB+1A (1att), wrong RSC(1,1)",
			Label("Bug:MultiattachDRBDLock"),
			SpecTimeout(3*time.Minute), require.MinNodes(4),
			fw.TestLayout{FTT: 1, GMDR: 0, Access: 1, Attached: 1}, fw.TestLayout{FTT: 1, GMDR: 1}),
		Entry("2D+1TB+1A (2att), wrong RSC(1,1)",
			Label("Bug:MultiattachDRBDLock"),
			SpecTimeout(3*time.Minute), require.MinNodes(4),
			fw.TestLayout{FTT: 1, GMDR: 0, Access: 1, Attached: 2}, fw.TestLayout{FTT: 1, GMDR: 1}),
		Entry("2D+1TB+2A (2att), wrong RSC(1,1)",
			Label("Bug:MultiattachDRBDLock"),
			SpecTimeout(3*time.Minute), require.MinNodes(5),
			fw.TestLayout{FTT: 1, GMDR: 0, Access: 2, Attached: 2}, fw.TestLayout{FTT: 1, GMDR: 1}),
	)
})
