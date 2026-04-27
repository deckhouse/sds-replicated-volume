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

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	fw "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/match"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/require"
	tkmatch "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

var _ = Describe("RVS clone", func() {
	DescribeTable("clones RV from existing RV for layout",
		func(ctx SpecContext, layout fw.TestLayout) {
			srcRV := f.SetupLayout(ctx, layout)
			srcSize := srcRV.Object().Status.Datamesh.Size.DeepCopy()

			clone := f.TestRV().
				FTT(layout.FTT).
				GMDR(layout.GMDR).
				DataSourceRV(srcRV.Name())
			clone.Create(ctx)

			clone.Await(ctx, match.RV.FormationComplete())
			clone.Await(ctx, match.RV.NoActiveTransitions())

			for _, trvr := range clone.TestRVRs() {
				trvr.Await(ctx, tkmatch.Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy)))
			}

			clone.Await(ctx, match.RV.DatameshSizeGE(srcSize))
			clone.Await(ctx, match.RV.Members(layout.ExpectedReplicas()))
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
