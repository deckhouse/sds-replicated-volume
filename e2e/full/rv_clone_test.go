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

var _ = Describe("RV clone", func() {
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

var _ = Describe("RV clone source finalizer", func() {
	// RVCloneSourceFinalizer is placed on the source RV only while the clone
	// is in Formation. Once the clone reaches FormationComplete the finalizer
	// is removed automatically (see rv_controller/reconciler_clone_source.go).
	// During Formation the finalizer prevents the source RV from being fully
	// deleted (it stays in Terminating with deletionTimestamp set).

	It("finalizer is absent on source after clone reaches FormationComplete",
		Label(fw.LabelSlow), SpecTimeout(5*time.Minute), require.MinNodes(1),
		func(ctx SpecContext) {
			srcRV := f.SetupLayout(ctx, fw.TestLayout{FTT: 0, GMDR: 0})

			clone := f.TestRV().
				FTT(0).
				GMDR(0).
				DataSourceRV(srcRV.Name())
			clone.Create(ctx)
			clone.Await(ctx, match.RV.FormationComplete())

			srcRV.Await(ctx, Not(tkmatch.HasFinalizer(v1alpha1.RVCloneSourceFinalizer)))
		})

	It("source deletion is blocked by finalizer while clone is in Formation",
		Label(fw.LabelSlow), SpecTimeout(6*time.Minute), require.MinNodes(1),
		func(ctx SpecContext) {
			srcRV := f.SetupLayout(ctx, fw.TestLayout{FTT: 0, GMDR: 0})

			clone := f.TestRV().
				FTT(0).
				GMDR(0).
				DataSourceRV(srcRV.Name())
			clone.Create(ctx)

			srcRV.Await(ctx, tkmatch.HasFinalizer(v1alpha1.RVCloneSourceFinalizer))

			srcRV.Delete(ctx)

			srcRV.Await(ctx, And(
				tkmatch.IsDeleting(),
				tkmatch.HasFinalizer(v1alpha1.RVCloneSourceFinalizer),
			))

			clone.Await(ctx, match.RV.FormationComplete())

			srcRV.Await(ctx, tkmatch.Deleted())
		})
})

var _ = Describe("RV clone rejected", func() {
	It("does not reach Formation when source RV does not exist",
		Label(fw.LabelSlow), SpecTimeout(2*time.Minute), require.MinNodes(1),
		func(ctx SpecContext) {
			clone := f.TestRV().
				FTT(0).
				GMDR(0).
				DataSourceRV("e2e-nonexistent-source-12345")
			clone.Create(ctx)

			Consistently(ctx, func() bool {
				rv := clone.Object()
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
				"clone must not reach FormationComplete when source RV is missing")
		})
})

var _ = Describe("RV clone parallel from same source", func() {
	It("two clones formed in parallel from one source",
		Label(fw.LabelSlow), SpecTimeout(7*time.Minute), require.MinNodes(1),
		func(ctx SpecContext) {
			srcRV := f.SetupLayout(ctx, fw.TestLayout{FTT: 0, GMDR: 0})

			cloneA := f.TestRV().
				FTT(0).
				GMDR(0).
				DataSourceRV(srcRV.Name())
			cloneB := f.TestRV().
				FTT(0).
				GMDR(0).
				DataSourceRV(srcRV.Name())

			cloneA.Create(ctx)
			cloneB.Create(ctx)

			cloneA.Await(ctx, match.RV.FormationComplete())
			cloneB.Await(ctx, match.RV.FormationComplete())

			srcRV.Await(ctx, Not(tkmatch.HasFinalizer(v1alpha1.RVCloneSourceFinalizer)))
		})
})
