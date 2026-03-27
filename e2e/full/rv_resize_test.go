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
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	fw "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/match"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/require"
	tkmatch "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

var _ = Describe("RV resize", func() {
	DescribeTable("resize completes for layout",
		func(ctx SpecContext, layout fw.TestLayout) {
			trv := f.SetupLayout(ctx, layout)
			trv.ActivateSafetyInvariants()

			initialSize := trv.Object().Status.Datamesh.Size.DeepCopy()
			newSize := initialSize.DeepCopy()
			newSize.Add(initialSize)

			trv.Update(ctx, func(rv *v1alpha1.ReplicatedVolume) {
				rv.Spec.Size = newSize
			})

			trv.Await(ctx, match.RV.DatameshSizeGE(newSize))
			trv.Await(ctx, match.RV.NoActiveTransitions())

			rv := trv.Object()
			Expect(rv.Status.Datamesh.Size.Cmp(newSize)).To(BeNumerically(">=", 0))
			Expect(rv.Status.Size).NotTo(BeNil())
			Expect(rv.Status.Size.Cmp(resource.Quantity{})).To(BeNumerically(">", 0))

			for _, trvr := range trv.TestRVRs() {
				rvr := trvr.Object()
				if rvr.Status.Type == v1alpha1.DRBDResourceTypeDiskful {
					Expect(rvr.Status.Size).NotTo(BeNil())
					Expect(rvr.Status.Size.Cmp(resource.Quantity{})).To(BeNumerically(">", 0))
				}
			}
		},

		Entry("1D",
			require.MinNodes(1),
			fw.TestLayout{FTT: 0, GMDR: 0}),

		Entry("2D",
			Label(fw.LabelSlow), SpecTimeout(60*time.Second), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 1}),

		Entry("3D",
			Label(fw.LabelSlow), SpecTimeout(90*time.Second), require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 1}),

		Entry("2D+1TB",
			Label(fw.LabelSlow), SpecTimeout(90*time.Second), require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 0}),

		Entry("1D attached",
			require.MinNodes(1),
			fw.TestLayout{FTT: 0, GMDR: 0, Attached: 1}),

		Entry("2D+1A (2 att)",
			Label(fw.LabelSlow), SpecTimeout(90*time.Second), require.MinNodes(3),
			fw.TestLayout{FTT: 0, GMDR: 1, Access: 1, Attached: 2}),

		Entry("2D multiattach",
			Label(fw.LabelSlow), SpecTimeout(60*time.Second), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 1, Attached: 2}),

		Entry("3D+2A multiattach (4 att)",
			Label("Bug:MultiattachDRBDLock"),
			Label(fw.LabelSlow), SpecTimeout(150*time.Second), require.MinNodes(5),
			fw.TestLayout{FTT: 1, GMDR: 1, Access: 2, Attached: 2}),
	)

	// Regression test: CEL validation compared resource.Quantity as strings,
	// so "100Gi" < "50Gi" lexicographically ('1' < '5'). This resize triggers
	// that exact pattern — new size starts with a lower ASCII digit.
	It("resize from 50Mi to 100Mi (lexicographic regression)", func(ctx SpecContext) {
		trv := f.TestRV().FTT(0).GMDR(1).Size("50Mi")
		trv.Create(ctx)
		trv.Await(ctx, match.RV.FormationComplete())

		for _, trvr := range trv.TestRVRs() {
			trvr.Await(ctx, tkmatch.Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy)))
		}

		newSize := resource.MustParse("100Mi")
		trv.Update(ctx, func(rv *v1alpha1.ReplicatedVolume) {
			rv.Spec.Size = newSize
		})

		trv.Await(ctx, match.RV.DatameshSizeGE(newSize))
		trv.Await(ctx, match.RV.NoActiveTransitions())

		rv := trv.Object()
		Expect(rv.Status.Datamesh.Size.Cmp(newSize)).To(BeNumerically(">=", 0))

		for _, trvr := range trv.TestRVRs() {
			rvr := trvr.Object()
			if rvr.Status.Type == v1alpha1.DRBDResourceTypeDiskful {
				Expect(rvr.Status.Size).NotTo(BeNil())
				Expect(rvr.Status.Size.Cmp(newSize)).To(BeNumerically(">=", 0))
			}
		}
	}, Label(fw.LabelSlow), SpecTimeout(60*time.Second), require.MinNodes(2))

	It("resize blocked by resync on thick 100Mi volume", func(ctx SpecContext) {
		trv := f.TestRV().FTT(0).GMDR(1).
			WithPoolType(v1alpha1.ReplicatedStoragePoolTypeLVM).
			Size("100Mi")
		trv.Create(ctx)
		trv.Await(ctx, match.RV.FormationComplete())

		for _, trvr := range trv.TestRVRs() {
			trvr.Await(ctx, tkmatch.Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy)))
		}

		id := trv.FreeReplicaID()
		newRVR := f.TestRVRExact(trv.Name(), id).Type(v1alpha1.ReplicaTypeDiskful)
		newRVR.Create(ctx)

		newSize := resource.MustParse("200Mi")
		trv.Update(ctx, func(rv *v1alpha1.ReplicatedVolume) {
			rv.Spec.Size = newSize
		})

		newRVR.Await(ctx, tkmatch.Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy)))

		trv.Await(ctx, match.RV.DatameshSizeGE(newSize))
		trv.Await(ctx, match.RV.NoActiveTransitions())

		rv := trv.Object()
		Expect(rv.Status.Datamesh.Size.Cmp(newSize)).To(BeNumerically(">=", 0))
	}, Label(fw.LabelSlow), SpecTimeout(5*time.Minute), require.MinNodes(3))

	It("resize completes after replica deletion", Label(fw.LabelSlow), func(ctx SpecContext) {
		trv := f.TestRV().FTT(1).GMDR(1)
		trv.Create(ctx)
		trv.Await(ctx, match.RV.FormationComplete())
		for _, trvr := range trv.TestRVRs() {
			trvr.Await(ctx, tkmatch.Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy)))
		}
		trv.ActivateSafetyInvariants()

		initialSize := trv.Object().Status.Datamesh.Size.DeepCopy()
		newSize := initialSize.DeepCopy()
		newSize.Add(initialSize)

		rvrs := trv.TestRVRs()
		Expect(rvrs).To(HaveLen(3))
		victim := rvrs[2]

		trv.WithoutSafetyInvariants(func() { // TODO: remove when quorum will be fixed
			// Delete victim first, then switch to Manual FTT=0/GMDR=0 + resize.
			// Deletion and resize happen concurrently.
			victim.Delete(ctx)

			// Switch to Manual mode with FTT=0/GMDR=0 to allow replica removal (3D is
			// the minimum for FTT=1 GMDR=1 — RemoveReplica(D) would be blocked by guards).
			trv.Update(ctx, func(rv *v1alpha1.ReplicatedVolume) {
				rv.Spec.Size = newSize
				rv.Spec.ConfigurationMode = v1alpha1.ReplicatedVolumeConfigurationModeManual
				rv.Spec.ReplicatedStorageClassName = ""
				rv.Spec.ManualConfiguration = &v1alpha1.ReplicatedVolumeConfiguration{
					ReplicatedStoragePoolName: rv.Status.Configuration.ReplicatedStoragePoolName,
					Topology:                  v1alpha1.TopologyIgnored,
					VolumeAccess:              v1alpha1.VolumeAccessPreferablyLocal,
				}
			})

			victim.Await(ctx, tkmatch.Deleted())

			trv.Await(ctx, match.RV.Members(2))

			trv.Await(ctx, match.RV.DatameshSizeGE(newSize))
			trv.Await(ctx, match.RV.NoActiveTransitions())
		})

		rv := trv.Object()
		Expect(rv.Status.Datamesh.Size.Cmp(newSize)).To(BeNumerically(">=", 0))
	}, Label(fw.LabelSlow), SpecTimeout(150*time.Second), require.MinNodes(3))
})
