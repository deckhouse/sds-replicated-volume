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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	fw "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/match"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/require"
	tkmatch "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

var _ = Describe("RVS restore", func() {
	DescribeTable("restores RV from snapshot for layout",
		func(ctx SpecContext, layout fw.TestLayout) {
			srcRV := f.SetupLayout(ctx, layout)
			srcRVS := f.SetupRVS(ctx, srcRV)

			srcSize := srcRV.Object().Status.Datamesh.Size.DeepCopy()

			restored := f.TestRV().
				FTT(layout.FTT).
				GMDR(layout.GMDR).
				DataSourceRVS(srcRVS.Name())
			restored.Create(ctx)

			restored.Await(ctx, match.RV.FormationComplete())
			restored.Await(ctx, match.RV.NoActiveTransitions())

			for _, trvr := range restored.TestRVRs() {
				trvr.Await(ctx, tkmatch.Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy)))
			}

			restored.Await(ctx, match.RV.DatameshSizeGE(srcSize))
			restored.Await(ctx, match.RV.Members(layout.ExpectedReplicas()))
		},

		Entry("1D",
			Label(fw.LabelSlow), SpecTimeout(4*time.Minute), require.MinNodes(1),
			fw.TestLayout{FTT: 0, GMDR: 0}),

		Entry("2D",
			Label(fw.LabelSlow), SpecTimeout(5*time.Minute), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 1}),

		Entry("3D",
			Label(fw.LabelSlow), SpecTimeout(6*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 1}),
	)
})

var _ = Describe("RVS restore rejected", func() {
	It("does not reach Formation when source RVS does not exist",
		Label(fw.LabelSlow), SpecTimeout(2*time.Minute), require.MinNodes(1),
		func(ctx SpecContext) {
			restored := f.TestRV().
				FTT(0).
				GMDR(0).
				DataSourceRVS("e2e-nonexistent-rvs-12345")
			restored.Create(ctx)

			Consistently(ctx, func() bool {
				rv := restored.Object()
				if rv == nil {
					return false
				}
				for i := range rv.Status.DatameshTransitions {
					t := &rv.Status.DatameshTransitions[i]
					if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation && t.IsCompleted() {
						return true
					}
				}
				return false
			}, "30s", "2s").Should(BeFalse(),
				"restored RV must not reach FormationComplete when source RVS is missing")
		})
})

var _ = Describe("RVS restore parallel from same snapshot", func() {
	It("two RVs restored in parallel from one snapshot",
		Label(fw.LabelSlow), SpecTimeout(7*time.Minute), require.MinNodes(1),
		func(ctx SpecContext) {
			srcRV := f.SetupLayout(ctx, fw.TestLayout{FTT: 0, GMDR: 0})
			srcRVS := f.SetupRVS(ctx, srcRV)

			restoredA := f.TestRV().
				FTT(0).
				GMDR(0).
				DataSourceRVS(srcRVS.Name())
			restoredB := f.TestRV().
				FTT(0).
				GMDR(0).
				DataSourceRVS(srcRVS.Name())

			restoredA.Create(ctx)
			restoredB.Create(ctx)

			restoredA.Await(ctx, match.RV.FormationComplete())
			restoredB.Await(ctx, match.RV.FormationComplete())

			for _, trvr := range restoredA.TestRVRs() {
				trvr.Await(ctx, tkmatch.Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy)))
			}
			for _, trvr := range restoredB.TestRVRs() {
				trvr.Await(ctx, tkmatch.Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy)))
			}
		})
})
