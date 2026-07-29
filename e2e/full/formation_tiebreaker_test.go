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

// E2E-2 / E2E-3 — tie-breaker at formation (block 3) and P2 healing (block 2).
// An r2 volume is created via FTT=1/GMDR=0, which is exactly the Availability
// replication mode (2D+1TB layout).
var _ = Describe("Layout: tie-breaker at formation and healing",
	Label(fw.LabelSlow), Label(fw.LabelFeatureMembership), func() {

		// E2E-2 — a fresh r2 volume forms straight to 2D+1TB (block 3).
		It("forms a new r2 volume directly as 2D+1TB (no post-formation doctoring)",
			SpecTimeout(10*time.Minute), require.MinNodes(2, 1), func(ctx SpecContext) {
				By("creating an r2 (Availability = FTT1/GMDR0) volume")
				trv := f.TestRV().FTT(1).GMDR(0)
				trv.Create(ctx)

				By("guarding against post-formation doctoring of the tie-breaker")
				// Continuous invariants from creation: if formation produced 2D and the
				// tie-breaker were added afterwards, it would show up as an active
				// AddReplica (P2) or ChangeReplicaType (retype) transition and fail here.
				// A tie-breaker created inside the Formation membership shows neither.
				trv.Always(noActiveAddReplica())
				trv.Always(noActiveChangeReplicaType())

				By("verifying the tie-breaker is present the moment formation completes")
				trv.Await(ctx, match.RV.FormationComplete())
				// Assert composition WITHOUT an intervening Await(Members): a regression
				// where formation finishes as 2D and the TB is doctored in later would
				// make this fail and would also trip the invariants above. The layout
				// string is NOT asserted here — the controller publishes no layout while
				// formation runs, so it is only readable on the converged snapshot below.
				Expect(memberTypeCount(trv, v1alpha1.DatameshMemberTypeDiskful)).To(Equal(2))
				Expect(memberTypeCount(trv, v1alpha1.DatameshMemberTypeTieBreaker)).To(Equal(1))

				By("verifying the volume is converged and serves I/O")
				trv.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeCondLayoutConvergedType,
					v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverged))
				Expect(layoutOf(trv)).To(Equal(ptr.To("2D+1TB")))
				trva := trv.Attach(ctx, trv.OccupiedNodes()[0])
				trva.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeAttachmentCondAttachedType,
					v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttached))
			})

		// E2E-3 — a deleted tie-breaker is healed by the P2 convergence pattern (block 2).
		//
		// Disruptive because of the raw-device writer: healing must be invisible
		// to the data path, and that is proven by verified writes flowing through
		// the whole cycle (the io-workload's historical gap check covers every
		// moment between the explicit probes).
		It("heals a deleted tie-breaker via the P2 add-TB pattern",
			Label(fw.LabelDisruptive), SpecTimeout(10*time.Minute), require.MinNodes(2, 1), func(ctx SpecContext) {
				By("creating a healthy 2D+1TB volume")
				trv := f.TestRV().FTT(1).GMDR(0)
				trv.Create(ctx)
				trv.Await(ctx, match.RV.FormationComplete())
				trv.Await(ctx, match.RV.Members(3))
				trv.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeCondLayoutConvergedType,
					v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverged))
				Expect(layoutOf(trv)).To(Equal(ptr.To("2D+1TB")))

				By("attaching the volume and writing to the raw device")
				trva := trv.Attach(ctx, memberNodesOfType(trv, v1alpha1.DatameshMemberTypeDiskful)[0])
				trva.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeAttachmentCondAttachedType,
					v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttached))
				io := startVolumeIO(ctx, trv, trva)
				ioBefore := ioProgressed(ctx, io, ioAlive(ctx, io))

				By("deleting the tie-breaker RVR and waiting for it to actually leave the datamesh")
				deleted := tieBreakerRVR(trv)
				deletedName := deleted.Name()
				deleted.Delete(ctx)
				// The RVControllerFinalizer is removed only after the member leaves the
				// datamesh, so Deleted() proves the removal (2D) actually happened —
				// otherwise the checks below would match the stale pre-deletion snapshot.
				deleted.Await(ctx, tkmatch.Deleted())

				By("observing convergence recreate the tie-breaker (P2)")
				// One atomic match instead of Converging-then-Converged: the snapshot has
				// to report Converged with a tie-breaker that is not the deleted one, so
				// the pre-deletion state cannot satisfy it and the transient Converging
				// report does not have to be caught (see healedTieBreakerOtherThan).
				trv.Await(ctx, healedTieBreakerOtherThan(deletedName))
				trv.Await(ctx, match.RV.Members(3))
				Expect(layoutOf(trv)).To(Equal(ptr.To("2D+1TB")))
				Expect(memberTypeCount(trv, v1alpha1.DatameshMemberTypeTieBreaker)).To(Equal(1))

				By("verifying the healed tie-breaker is a NEW RVR, not the deleted one")
				Expect(tieBreakerRVR(trv).Name()).NotTo(Equal(deletedName))

				By("I/O kept flowing through the healing")
				ioProgressed(ctx, io, ioBefore)
			})
	})
