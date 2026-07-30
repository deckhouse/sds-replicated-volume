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
	"sort"
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

// BE2E-1 / BE2E-2 — the manual recovery path after a diskful replica is lost.
// There is no automatic heal in this version: the operator creates a diskful
// ReplicatedVolumeReplica by hand and the datamesh joins it.
//
// The two cases differ in the join plan, and that difference is the point:
//
//   - r2 (2D+1TB) loses a diskful and is left with ONE voter, so the join goes
//     odd→even and runs `diskful-q-up/v1` — through the Access vestibule, with a
//     quorum raise. That plan is the one bug B-1 used to break: the agent never
//     restored `--bitmap=yes` on a peer leaving the diskless stage, and the
//     kernel refused the connection ("peer is configured to be diskless but
//     presents Inconsistent"). This spec is the regression test for that fix —
//     without it the joining replica never reaches Healthy.
//   - r3 (3D) loses a diskful and keeps TWO voters, so the join goes even→odd and
//     runs `diskful/v1`, which has no Access stage at all. It guards the path
//     that always worked against collateral damage from the fix.
//
// Both specs are Disruptive because they run a raw-device writer: "the data is
// still there" is a claim about the data path, and conditions cannot make it.
// The loss itself is NOT disruptive — see simulateDiskfulLoss: no finalizer is
// ever stripped and nothing outside the volume's own spec is touched.
var _ = Describe("Layout: manual recovery of a lost diskful replica",
	Label(fw.LabelSlow), Label(fw.LabelFeatureMembership), func() {

		It("restores an r2 volume to 2D+1TB by creating a diskful replica by hand",
			SpecTimeout(20*time.Minute), Label(fw.LabelDisruptive), require.MinNodes(2, 1),
			func(ctx SpecContext) {
				By("creating a 2D+1TB volume and letting it converge")
				trv := f.TestRV().FTT(1).GMDR(0)
				trv.Create(ctx)
				trv.Await(ctx, match.RV.FormationComplete())
				trv.Await(ctx, match.RV.Members(3))
				trv.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType,
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverged))
				Expect(membershipLayoutOf(trv)).To(Equal(ptr.To("2D+1TB")))

				diskfulNodes := memberNodesOfType(trv, v1alpha1.DatameshMemberTypeDiskful)
				Expect(diskfulNodes).To(HaveLen(2))
				sort.Strings(diskfulNodes)
				survivor, victim := diskfulNodes[0], diskfulNodes[1]

				tieBreakerNodes := memberNodesOfType(trv, v1alpha1.DatameshMemberTypeTieBreaker)
				Expect(tieBreakerNodes).To(HaveLen(1))
				tieBreakerPeer := drbdPeerNameOn(trv, tieBreakerNodes[0])
				survivorResource := drbdResourceOn(trv, survivor)

				By("attaching on the surviving diskful node and writing to the raw device there")
				// The writer runs on the SURVIVOR on purpose: the datamesh refuses to
				// demote an attached voter, so the victim must stay unattached — and
				// the survivor is where "the data is still readable and writable" has
				// to hold across the whole outage.
				trva := trv.Attach(ctx, survivor)
				trva.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeAttachmentCondAttachedType,
					v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttached))
				io := startVolumeIO(ctx, trv, trva)
				ioBefore := ioProgressed(ctx, io, ioAlive(ctx, io))

				By("losing the diskful replica on " + victim)
				simulateDiskfulLoss(ctx, trv, victim)

				By("observing the volume really shrink to 1D+1TB and report the mismatch")
				// The arithmetic — not the reason — is what proves the shrink actually
				// happened: inside the downgrade window the same reason is reported
				// for the excess.
				trv.Await(ctx, layoutDegraded("1D+1TB", "2D+1TB"))
				Expect(membershipLayoutOf(trv)).To(Equal(ptr.To("1D+1TB")))
				Expect(memberTypeCount(trv, v1alpha1.DatameshMemberTypeDiskful)).To(Equal(1))
				Expect(memberTypeCount(trv, v1alpha1.DatameshMemberTypeTieBreaker)).To(Equal(1))
				trv.Await(ctx, match.RV.Quorum(1))

				By("the kernel agrees the peer is gone: one voter left, quorum lowered to 1")
				f.AwaitDRBDPeers(ctx, survivor, survivorResource, tieBreakerPeer)
				Expect(f.DRBDStatus(ctx, survivor, survivorResource).Quorum).To(BeTrue(),
					"the surviving diskful lost DRBD quorum after the replica left")
				expectDRBDQuorum(ctx, trv)

				By("the metric the alert rule selects reports the degradation")
				awaitLayoutMetric(ctx, trv,
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonTransitionUnsupported, 0)

				By("the single remaining copy keeps serving I/O")
				ioDegraded := ioProgressed(ctx, io, ioBefore)

				By("creating a diskful replica by hand — the documented recovery")
				newRVR := f.TestRVRExact(trv.Name(), trv.FreeReplicaID()).
					Type(v1alpha1.ReplicaTypeDiskful)
				newRVR.Create(ctx)

				By("the join takes the q-up path: odd->even voters, Access vestibule, quorum raise")
				// This is the exact edge B-1 broke. The plan id is set when the
				// transition is dispatched and lives for the whole join, unlike a
				// single step name.
				trv.Await(ctx, addReplicaPlanIDIn("diskful-q-up/v1", "diskful-q-up-qmr-up/v1"))

				By("the new replica syncs and becomes a healthy diskful member")
				// Under B-1 this is where the spec fails: the peer never leaves the
				// diskless stage, the kernel rejects the connection and the replica
				// never reaches Healthy.
				newRVR.Await(ctx, tkmatch.Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy)))
				newRVR.Await(ctx, match.RVR.BackingVolumeState(string(v1alpha1.DiskStateUpToDate)))

				By("the layout converges back to 2D+1TB with the quorum raised to 2")
				trv.Await(ctx, match.RV.Members(3))
				trv.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType,
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverged))
				Expect(membershipLayoutOf(trv)).To(Equal(ptr.To("2D+1TB")))
				Expect(memberTypeCount(trv, v1alpha1.DatameshMemberTypeDiskful)).To(Equal(2))
				trv.Await(ctx, match.RV.Quorum(2))

				By("the kernel sees the recovered peer and enforces the raised threshold")
				f.AwaitDRBDPeers(ctx, survivor, survivorResource,
					tieBreakerPeer, fw.DRBDPeerName(newRVR.ID()))
				expectDRBDQuorum(ctx, trv)

				By("the metric returns to 1/Converged, so the alert cannot fire any more")
				awaitLayoutMetric(ctx, trv,
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverged, 1)

				By("the data path survived the whole cycle")
				ioProgressed(ctx, io, ioDegraded)
			})

		It("restores an r3 volume to 3D by creating a diskful replica by hand",
			SpecTimeout(20*time.Minute), Label(fw.LabelDisruptive), require.MinNodes(3),
			func(ctx SpecContext) {
				By("creating a 3D volume and letting it converge")
				trv := f.TestRV().FTT(1).GMDR(1)
				trv.Create(ctx)
				trv.Await(ctx, match.RV.FormationComplete())
				trv.Await(ctx, match.RV.Members(3))
				trv.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType,
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverged))
				Expect(membershipLayoutOf(trv)).To(Equal(ptr.To("3D")))
				Expect(memberTypeCount(trv, v1alpha1.DatameshMemberTypeTieBreaker)).To(Equal(0))

				diskfulNodes := memberNodesOfType(trv, v1alpha1.DatameshMemberTypeDiskful)
				Expect(diskfulNodes).To(HaveLen(3))
				sort.Strings(diskfulNodes)
				attached, spectator, victim := diskfulNodes[0], diskfulNodes[1], diskfulNodes[2]
				attachedResource := drbdResourceOn(trv, attached)
				spectatorPeer := drbdPeerNameOn(trv, spectator)

				By("attaching on a diskful node that is not the victim, and writing to the raw device")
				trva := trv.Attach(ctx, attached)
				trva.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeAttachmentCondAttachedType,
					v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttached))
				io := startVolumeIO(ctx, trv, trva)
				ioBefore := ioProgressed(ctx, io, ioAlive(ctx, io))

				By("losing the diskful replica on " + victim)
				simulateDiskfulLoss(ctx, trv, victim)

				By("observing the volume really shrink to 2D and report the mismatch")
				trv.Await(ctx, layoutDegraded("2D", "3D"))
				Expect(membershipLayoutOf(trv)).To(Equal(ptr.To("2D")))
				Expect(memberTypeCount(trv, v1alpha1.DatameshMemberTypeDiskful)).To(Equal(2))
				// Two voters left, so the threshold stays 2: the volume now survives
				// no further failure at all, which is exactly what the alert is for.
				trv.Await(ctx, match.RV.Quorum(2))
				f.AwaitDRBDPeers(ctx, attached, attachedResource, spectatorPeer)
				expectDRBDQuorum(ctx, trv)
				awaitLayoutMetric(ctx, trv,
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonTransitionUnsupported, 0)
				ioDegraded := ioProgressed(ctx, io, ioBefore)

				By("creating a diskful replica by hand — the documented recovery")
				newRVR := f.TestRVRExact(trv.Name(), trv.FreeReplicaID()).
					Type(v1alpha1.ReplicaTypeDiskful)
				newRVR.Create(ctx)

				By("the join takes the plain path: even->odd voters, no Access vestibule")
				trv.Await(ctx, addReplicaPlanIDIn("diskful/v1", "diskful-qmr-up/v1"))

				By("the new replica syncs and becomes a healthy diskful member")
				newRVR.Await(ctx, tkmatch.Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy)))
				newRVR.Await(ctx, match.RVR.BackingVolumeState(string(v1alpha1.DiskStateUpToDate)))

				By("the layout converges back to 3D")
				trv.Await(ctx, match.RV.Members(3))
				trv.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType,
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverged))
				Expect(membershipLayoutOf(trv)).To(Equal(ptr.To("3D")))
				Expect(memberTypeCount(trv, v1alpha1.DatameshMemberTypeDiskful)).To(Equal(3))
				trv.Await(ctx, match.RV.Quorum(2))

				By("the kernel sees the recovered peer")
				f.AwaitDRBDPeers(ctx, attached, attachedResource,
					spectatorPeer, fw.DRBDPeerName(newRVR.ID()))
				expectDRBDQuorum(ctx, trv)

				By("the metric returns to 1/Converged")
				awaitLayoutMetric(ctx, trv,
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverged, 1)

				By("the data path survived the whole cycle")
				ioProgressed(ctx, io, ioDegraded)
			})
	})
