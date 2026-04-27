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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	fw "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/match"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/require"
	tkmatch "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

var _ = Describe("RVS delete", func() {
	It("deletes RVS during InProgress (prepare) and cleans up children",
		Label(fw.LabelSlow), SpecTimeout(5*time.Minute), require.MinNodes(3),
		func(ctx SpecContext) {
			trv := f.SetupLayout(ctx, fw.TestLayout{FTT: 1, GMDR: 1})

			trvs := trv.Snapshot()
			trvs.Create(ctx)

			trvs.Await(ctx, match.RVS.PhaseIs(v1alpha1.ReplicatedVolumeSnapshotPhaseInProgress))

			trvs.Delete(ctx)
			trvs.Await(ctx, tkmatch.Deleted())

			expectRVSChildrenGone(ctx, trvs.Name())
		})

	It("deletes RVS during Synchronizing and cleans up children",
		Label(fw.LabelSlow), SpecTimeout(5*time.Minute), require.MinNodes(3),
		func(ctx SpecContext) {
			trv := f.SetupLayout(ctx, fw.TestLayout{FTT: 1, GMDR: 1})

			trvs := trv.Snapshot()
			trvs.Create(ctx)

			trvs.Await(ctx, match.RVS.PrepareComplete())
			trvs.Await(ctx, match.RVS.PhaseIs(v1alpha1.ReplicatedVolumeSnapshotPhaseSynchronizing))

			trvs.Delete(ctx)
			trvs.Await(ctx, tkmatch.Deleted())

			expectRVSChildrenGone(ctx, trvs.Name())
		})

	It("deletes RVS after Ready and cleans up children",
		Label(fw.LabelSlow), SpecTimeout(3*time.Minute), require.MinNodes(1),
		func(ctx SpecContext) {
			trv := f.SetupLayout(ctx, fw.TestLayout{FTT: 0, GMDR: 0})

			trvs := f.SetupRVS(ctx, trv)

			trvs.Delete(ctx)
			trvs.Await(ctx, tkmatch.Deleted())

			expectRVSChildrenGone(ctx, trvs.Name())
		})
})

// expectRVSChildrenGone asserts that after RVS deletion, no RVRS, temp
// DRBDResource, or DRBDResourceOperation with the "{rvsName}-" prefix
// remains in the cluster (all are cascade-deleted via ownerRef).
func expectRVSChildrenGone(ctx SpecContext, rvsName string) {
	GinkgoHelper()
	prefix := rvsName + "-"

	Eventually(ctx, func() []string {
		var rvrss v1alpha1.ReplicatedVolumeReplicaSnapshotList
		Expect(f.Client.List(ctx, &rvrss)).To(Succeed())
		var names []string
		for i := range rvrss.Items {
			if rvrss.Items[i].Spec.ReplicatedVolumeSnapshotName == rvsName {
				names = append(names, rvrss.Items[i].Name)
			}
		}
		return names
	}).Should(BeEmpty(), "RVRS children remain after RVS deletion")

	Eventually(ctx, func() []string {
		var drbdrs v1alpha1.DRBDResourceList
		Expect(f.Client.List(ctx, &drbdrs)).To(Succeed())
		var names []string
		for i := range drbdrs.Items {
			if strings.HasPrefix(drbdrs.Items[i].Name, prefix) {
				names = append(names, drbdrs.Items[i].Name)
			}
		}
		return names
	}).Should(BeEmpty(), "temp DRBDResource objects remain after RVS deletion")

	Eventually(ctx, func() []string {
		var ops v1alpha1.DRBDResourceOperationList
		Expect(f.Client.List(ctx, &ops)).To(Succeed())
		var names []string
		for i := range ops.Items {
			if strings.HasPrefix(ops.Items[i].Name, prefix) {
				names = append(names, ops.Items[i].Name)
			}
		}
		return names
	}).Should(BeEmpty(), "DRBDResourceOperation objects remain after RVS deletion")
}
