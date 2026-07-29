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
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	fw "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/match"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/require"
	tkmatch "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

var _ = Describe("RVS restore-source protection", func() {
	// Deleting a snapshot while a volume is still being restored from it must not
	// pull the per-replica snapshots out from under the restore. The snapshot
	// object survives on RVSRestoreSourceFinalizer until the target settles.
	It("defers snapshot deletion until the restore finishes",
		Label(fw.LabelSlow), SpecTimeout(8*time.Minute), require.MinNodes(2),
		func(ctx SpecContext) {
			srcRV := f.SetupLayout(ctx, fw.TestLayout{FTT: 0, GMDR: 1})
			srcRVS := f.SetupRVS(ctx, srcRV)

			restored := f.TestRV().
				FTT(0).
				GMDR(1).
				DataSourceRVS(srcRVS.Name())
			restored.Create(ctx)

			// rv-controller pins the snapshot as soon as the target starts forming.
			srcRVS.Await(ctx, match.RVS.Custom("has restore-source finalizer",
				func(rvs *v1alpha1.ReplicatedVolumeSnapshot) bool {
					return slices.Contains(rvs.Finalizers, v1alpha1.RVSRestoreSourceFinalizer)
				}))

			// Ask for deletion mid-restore. It must be accepted but not carried out.
			srcRVS.Delete(ctx)

			srcRVS.Await(ctx, match.RVS.PhaseIs(v1alpha1.ReplicatedVolumeSnapshotPhaseDeleting))

			// The children the restore reads from must still be there.
			Expect(srcRVS.RVRSCount()).To(BeNumerically(">", 0),
				"per-replica snapshots were removed while the restore was still running")

			// The restore completes despite the pending deletion.
			restored.Await(ctx, match.RV.FormationComplete())
			restored.Await(ctx, match.RV.NoActiveTransitions())
			for _, trvr := range restored.TestRVRs() {
				trvr.Await(ctx, tkmatch.Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy)))
			}

			// Only then does the snapshot actually go away.
			srcRVS.Await(ctx, tkmatch.Deleted())
			for _, trvrs := range srcRVS.TestRVRSs() {
				trvrs.Await(ctx, tkmatch.Deleted())
			}
		})

	// Regression: the finalizer used to be placed on a snapshot in any phase. A
	// snapshot pinned before it settled could never become Ready — its controller
	// stops advancing prepare/sync once deletion starts — so the restore hung and
	// the DRBD admin lock stayed held cluster-wide.
	It("does not pin a snapshot that has not become Ready",
		Label(fw.LabelSlow), SpecTimeout(6*time.Minute), require.MinNodes(2),
		func(ctx SpecContext) {
			srcRV := f.SetupLayout(ctx, fw.TestLayout{FTT: 0, GMDR: 1})

			trvs := srcRV.Snapshot()
			trvs.Create(ctx)
			trvs.Await(ctx, match.RVS.PhaseIs(v1alpha1.ReplicatedVolumeSnapshotPhaseInProgress))

			// Point a restore at the still-unfinished snapshot.
			restored := f.TestRV().
				FTT(0).
				GMDR(1).
				DataSourceRVS(trvs.Name())
			restored.Create(ctx)

			// Deleting it now must not be deferred: nothing may be pinned yet.
			trvs.Delete(ctx)
			trvs.Await(ctx, tkmatch.Deleted())

			// The admin lock must be gone with it — no orphan lock operation left.
			expectNoOrphanSyncResources(ctx, trvs)
		})
})

var _ = Describe("RVS outlives its source volume", func() {
	// A VolumeSnapshot is independent of its source claim, so a finished snapshot
	// has to stay restorable after the volume it was taken from is deleted —
	// otherwise a snapshot is useless as a backup.
	It("stays Ready and restorable after the source RV is deleted",
		Label(fw.LabelSlow), SpecTimeout(8*time.Minute), require.MinNodes(2),
		func(ctx SpecContext) {
			srcRV := f.SetupLayout(ctx, fw.TestLayout{FTT: 0, GMDR: 1})
			srcRVS := f.SetupRVS(ctx, srcRV)
			srcSize := srcRV.Object().Status.Datamesh.Size.DeepCopy()

			srcRV.Delete(ctx)
			srcRV.Await(ctx, tkmatch.Deleted())

			// Losing the source must not invalidate the snapshot.
			srcRVS.Await(ctx, match.RVS.PhaseIs(v1alpha1.ReplicatedVolumeSnapshotPhaseReady))
			srcRVS.Await(ctx, match.RVS.ReadyToUse())

			// And it must still be usable as a data source.
			restored := f.TestRV().
				FTT(0).
				GMDR(1).
				DataSourceRVS(srcRVS.Name())
			restored.Create(ctx)

			restored.Await(ctx, match.RV.FormationComplete())
			restored.Await(ctx, match.RV.NoActiveTransitions())
			restored.Await(ctx, match.RV.DatameshSizeGE(srcSize))
		})

	// An unfinished snapshot, on the other hand, has nothing left to finish with:
	// prepare and sync both need the source replicas.
	It("fails an unfinished snapshot when the source RV disappears",
		Label(fw.LabelSlow), SpecTimeout(6*time.Minute), require.MinNodes(2),
		func(ctx SpecContext) {
			srcRV := f.SetupLayout(ctx, fw.TestLayout{FTT: 0, GMDR: 1})

			trvs := srcRV.Snapshot()
			trvs.Create(ctx)
			trvs.Await(ctx, match.RVS.PhaseIs(v1alpha1.ReplicatedVolumeSnapshotPhaseInProgress))

			srcRV.Delete(ctx)

			trvs.Await(ctx, match.RVS.PhaseIs(v1alpha1.ReplicatedVolumeSnapshotPhaseFailed))
			trvs.Await(ctx, match.RVS.NotReadyToUse())
		})
})
