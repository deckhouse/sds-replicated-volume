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
)

var _ = Describe("RVS create", func() {
	DescribeTable("snapshot completes for layout",
		func(ctx SpecContext, layout fw.TestLayout) {
			trv := f.SetupLayout(ctx, layout)
			trv.ActivateSafetyInvariants()

			trvs := trv.Snapshot()
			trvs.Create(ctx)

			trvs.Await(ctx, match.RVS.PrepareComplete())
			trvs.Await(ctx, match.RVS.SyncComplete())
			trvs.Await(ctx, match.RVS.ReadyToUse())
			trvs.Await(ctx, match.RVS.NoActiveTransitions())

			rvs := trvs.Object()
			Expect(rvs.Status.Phase).To(Equal(v1alpha1.ReplicatedVolumeSnapshotPhaseReady))
			Expect(rvs.Status.ReadyToUse).To(BeTrue())

			expectedDiskful := countDiskfulMembers(trv.Object())
			Expect(rvs.Status.Datamesh.Members).To(HaveLen(expectedDiskful))
			Expect(rvs.Status.Datamesh.ReadyCount).To(Equal(expectedDiskful))
			Expect(rvs.Status.Datamesh.TotalCount).To(Equal(expectedDiskful))
			Expect(rvs.Status.SourceReplicaSnapshotName).NotTo(BeEmpty())

			for _, m := range rvs.Status.Datamesh.Members {
				Expect(m.Ready).To(BeTrue(), "member %q on node %q not ready", m.Name, m.NodeName)
				Expect(m.SnapshotHandle).NotTo(BeEmpty(),
					"member %q on node %q has empty snapshotHandle", m.Name, m.NodeName)
			}

			Eventually(ctx, func() int { return trvs.RVRSCount() }).
				Should(Equal(expectedDiskful))

			for _, trvrs := range trvs.TestRVRSs() {
				rvrs := trvrs.Object()
				Expect(rvrs.Status.Phase).To(Equal(v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseReady),
					"RVRS %q has phase %q, expected Ready", rvrs.Name, rvrs.Status.Phase)
				Expect(rvrs.Status.ReadyToUse).To(BeTrue(), "RVRS %q readyToUse=false", rvrs.Name)
				Expect(rvrs.Status.SnapshotHandle).NotTo(BeEmpty(),
					"RVRS %q has empty snapshotHandle", rvrs.Name)
			}

			expectNoOrphanSyncResources(ctx, trvs)
		},

		Entry("1D",
			Label(fw.LabelSlow), SpecTimeout(3*time.Minute), require.MinNodes(1),
			fw.TestLayout{FTT: 0, GMDR: 0}),

		Entry("2D",
			Label(fw.LabelSlow), SpecTimeout(3*time.Minute), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 1}),

		Entry("3D",
			Label(fw.LabelSlow), SpecTimeout(4*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 1}),

		Entry("2D+1TB",
			Label(fw.LabelSlow), SpecTimeout(4*time.Minute), require.MinNodes(2, 1),
			fw.TestLayout{FTT: 1, GMDR: 0}),

		Entry("2D multiattach",
			Label(fw.LabelSlow), SpecTimeout(4*time.Minute), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 1, Attached: 2}),
	)
})

// countDiskfulMembers returns the number of members in the RV datamesh
// that back their data with a local volume (i.e., will receive a snapshot).
func countDiskfulMembers(rv *v1alpha1.ReplicatedVolume) int {
	n := 0
	for _, m := range rv.Status.Datamesh.Members {
		if m.Type.HasBackingVolume() {
			n++
		}
	}
	return n
}

// expectNoOrphanSyncResources asserts that no temp sync DRBDResource or
// DRBDResourceOperation with the RVS name prefix remains in the cluster
// after the snapshot is Ready. Temp resources are named "{rvsName}-*".
func expectNoOrphanSyncResources(ctx SpecContext, trvs *fw.TestRVS) {
	GinkgoHelper()
	prefix := trvs.Name() + "-"

	Eventually(ctx, func() []string {
		var drbdrs v1alpha1.DRBDResourceList
		Expect(f.Client.List(ctx, &drbdrs)).To(Succeed())
		var names []string
		for i := range drbdrs.Items {
			if strings.HasPrefix(drbdrs.Items[i].Name, prefix) &&
				drbdrs.Items[i].DeletionTimestamp == nil {
				names = append(names, drbdrs.Items[i].Name)
			}
		}
		return names
	}).Should(BeEmpty(), "orphan temp DRBDResource objects after RVS Ready")

	Eventually(ctx, func() []string {
		var ops v1alpha1.DRBDResourceOperationList
		Expect(f.Client.List(ctx, &ops)).To(Succeed())
		var names []string
		for i := range ops.Items {
			if strings.HasPrefix(ops.Items[i].Name, prefix) &&
				ops.Items[i].DeletionTimestamp == nil {
				names = append(names, ops.Items[i].Name)
			}
		}
		return names
	}).Should(BeEmpty(), "orphan DRBDResourceOperation objects after RVS Ready")
}
