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
	"k8s.io/utils/ptr"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	fw "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/match"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/require"
	tkmatch "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

var _ = Describe("Multiattach: add replica to live datamesh", Label(fw.LabelSlow), func() {
	DescribeTable("adds replica while multiattach is active",
		func(ctx SpecContext, layout fw.TestLayout, add v1alpha1.ReplicaType) {
			trv := f.SetupLayout(ctx, layout)

			initialMembers := len(trv.Object().Status.Datamesh.Members)

			switch add {
			case v1alpha1.ReplicaTypeAccess:
				trv.Update(ctx, func(rv *v1alpha1.ReplicatedVolume) {
					rv.Spec.MaxAttachments = ptr.To(byte(layout.Attached + 1))
				})

				node := f.Discovery.AnyNode(trv.OccupiedNodes()...)
				trva := trv.Attach(ctx, node)
				trva.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeAttachmentCondAttachedType,
					v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttached))

			case v1alpha1.ReplicaTypeTieBreaker:
				rvr := f.TestRVRExact(trv.Name(), trv.FreeReplicaID()).
					Type(v1alpha1.ReplicaTypeTieBreaker)
				rvr.Create(ctx)
				rvr.Await(ctx, tkmatch.Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy)))

			case v1alpha1.ReplicaTypeDiskful:
				rvr := f.TestRVRExact(trv.Name(), trv.FreeReplicaID()).
					Type(v1alpha1.ReplicaTypeDiskful)
				rvr.Create(ctx)
				rvr.Await(ctx, tkmatch.Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy)))
			}

			trv.Await(ctx, match.RV.Members(initialMembers+1))

			Expect(trv.Object().Status.Datamesh.Members).NotTo(BeEmpty())
		},

		// ── 1D (FTT=0, GMDR=0), 2 att (1D+1A) — two Primaries baseline ──

		Entry("1D (1D+1A att) + Access",
			SpecTimeout(3*time.Minute), require.MinNodes(1, 1),
			fw.TestLayout{FTT: 0, GMDR: 0, Access: 1, Attached: 2}, v1alpha1.ReplicaTypeAccess),

		// ── 2D (FTT=0, GMDR=1), 2D att — both Diskful are Primary ──

		Entry("2D (2D att) + Access",
			Label("Bug:MultiattachDRBDLock"),
			SpecTimeout(3*time.Minute), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 1, Attached: 2}, v1alpha1.ReplicaTypeAccess),
		Entry("2D (2D att) + TieBreaker",
			Label("Bug:MultiattachDRBDLock"),
			SpecTimeout(3*time.Minute), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 1, Attached: 2}, v1alpha1.ReplicaTypeTieBreaker),
		Entry("2D (2D att) + Diskful",
			Label("Bug:MultiattachDRBDLock"),
			SpecTimeout(3*time.Minute), require.MinNodes(2),
			fw.TestLayout{FTT: 0, GMDR: 1, Attached: 2}, v1alpha1.ReplicaTypeDiskful),

		// ── 2D+1A (FTT=0, GMDR=1), 1D+1A att — mixed Primary ──

		Entry("2D (1D+1A att) + Access",
			SpecTimeout(3*time.Minute), require.MinNodes(2, 1),
			fw.TestLayout{FTT: 0, GMDR: 1, Access: 1, Attached: 2}, v1alpha1.ReplicaTypeAccess),
		Entry("2D (1D+1A att) + TieBreaker",
			Label("Bug:MultiattachDRBDLock"), // fails if two D attached
			SpecTimeout(3*time.Minute), require.MinNodes(2, 1),
			fw.TestLayout{FTT: 0, GMDR: 1, Access: 1, Attached: 2}, v1alpha1.ReplicaTypeTieBreaker),
		Entry("2D (1D+1A att) + Diskful",
			Label("Bug:MultiattachDRBDLock"), // fails if two D attached
			SpecTimeout(3*time.Minute), require.MinNodes(2, 1),
			fw.TestLayout{FTT: 0, GMDR: 1, Access: 1, Attached: 2}, v1alpha1.ReplicaTypeDiskful),

		// ── 3D (FTT=1, GMDR=1), 2D att — two Diskful Primary, one Diskful Secondary ──

		Entry("3D (2D att) + Access",
			Label("Bug:MultiattachDRBDLock"),
			SpecTimeout(3*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 1, Attached: 2}, v1alpha1.ReplicaTypeAccess),
		Entry("3D (2D att) + TieBreaker",
			Label("Bug:MultiattachDRBDLock"),
			SpecTimeout(3*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 1, Attached: 2}, v1alpha1.ReplicaTypeTieBreaker),
		Entry("3D (2D att) + Diskful",
			Label("Bug:MultiattachDRBDLock"),
			SpecTimeout(3*time.Minute), require.MinNodes(3),
			fw.TestLayout{FTT: 1, GMDR: 1, Attached: 2}, v1alpha1.ReplicaTypeDiskful),

		// ── 3D+1A (FTT=1, GMDR=1), 1D+1A att — mixed Primary ──

		Entry("3D (1D+1A att) + Access",
			Label("Bug:MultiattachDRBDLock"), // fails if two D attached
			SpecTimeout(3*time.Minute), require.MinNodes(3, 1),
			fw.TestLayout{FTT: 1, GMDR: 1, Access: 1, Attached: 2}, v1alpha1.ReplicaTypeAccess),
		Entry("3D (1D+1A att) + TieBreaker",
			Label("Bug:MultiattachDRBDLock"), // fails if two D attached
			SpecTimeout(3*time.Minute), require.MinNodes(3, 1),
			fw.TestLayout{FTT: 1, GMDR: 1, Access: 1, Attached: 2}, v1alpha1.ReplicaTypeTieBreaker),
		Entry("3D (1D+1A att) + Diskful",
			Label("Bug:MultiattachDRBDLock"), // fails if two D attached
			SpecTimeout(3*time.Minute), require.MinNodes(3, 1),
			fw.TestLayout{FTT: 1, GMDR: 1, Access: 1, Attached: 2}, v1alpha1.ReplicaTypeDiskful),
	)
})
