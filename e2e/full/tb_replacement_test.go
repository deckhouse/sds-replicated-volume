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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	fw "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/match"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/require"
	tkmatch "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

// E2E-TB1 / E2E-TB2 — replacing a tie-breaker whose replica was deleted.
//
// The contract is strict create-first: the datamesh never releases the
// tie-breaker it is replacing before the replacement is operational, so
// tiebreak protection is not lost for a moment. When no node can host the
// replacement there is nothing to fall back to — the volume says
// CannotConverge and the terminating tie-breaker keeps working until an
// operator frees a node or applies the manual escape.
var _ = Describe("Layout: tie-breaker replacement",
	Label(fw.LabelSlow), Label(fw.LabelFeatureMembership), func() {

		// E2E-TB1 — the free-node case: the whole cycle runs by itself.
		//
		// Four eligible nodes are needed: two diskful, one for the tie-breaker
		// being replaced, one free for its replacement. Only the first two need
		// storage, hence 2 diskful + 2 extra. require.MinNodes skips the spec
		// with an explicit reason on smaller clusters.
		It("replaces a deleted tie-breaker create-first when a free node exists",
			SpecTimeout(20*time.Minute), require.MinNodes(2, 2), func(ctx SpecContext) {
				By("creating a healthy 2D+1TB volume")
				trv := f.TestRV().FTT(1).GMDR(0)
				trv.Create(ctx)
				trv.Await(ctx, match.RV.FormationComplete())
				trv.Await(ctx, match.RV.Members(3))
				trv.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeCondLayoutConvergedType,
					v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverged))
				Expect(layoutOf(trv)).To(Equal(ptr.To("2D+1TB")))

				trv.ActivateSafetyInvariants()
				// Strict create-first as a continuous claim: the volume must
				// never be caught as a bare 2D, i.e. the old tie-breaker is
				// released only once a replacement is a member. The quorum
				// value stays 2 for the whole window — tie-breakers do not
				// vote, so the two diskful members are the only voters even
				// while there are two tie-breakers — and the QuorumCorrect
				// invariant activated above pins it to the current voter count
				// on every snapshot; this matcher adds the membership half.
				trv.Always(tiebreakHeld())

				diskfulNodes := memberNodesOfType(trv, v1alpha1.DatameshMemberTypeDiskful)
				Expect(diskfulNodes).To(HaveLen(2))

				oldTB := tieBreakerRVR(trv)
				oldName, oldUID := oldTB.Name(), tieBreakerUID(trv)
				oldNode := oldTB.Object().Spec.NodeName
				oldPeer := fw.DRBDPeerName(oldTB.ID())

				By("checking both diskful nodes really run with that tie-breaker")
				for _, node := range diskfulNodes {
					cfg := f.DRBDConfig(ctx, node, drbdResourceOn(trv, node))
					Expect(cfg.HasPeer(oldPeer)).To(BeTrue(),
						"node %s does not have the tie-breaker configured: %s", node, cfg)
				}
				expectDRBDQuorum(ctx, trv)

				By("deleting the tie-breaker replica")
				oldTB.Delete(ctx)

				By("observing create-first: the replacement joins while the old one is still a member")
				// One snapshot carries all of it — two tie-breakers, the old one
				// among them, and an honest 2D+2TB layout — so this cannot be
				// satisfied by a before/after pair of observations.
				trv.Await(ctx, tieBreakerReplacementWindow(oldName))

				newTB := awaitTieBreakerReplacement(ctx, trv, oldUID)
				newPeer := fw.DRBDPeerName(newTB.ID())
				Expect(newTB.Object().Spec.NodeName).NotTo(Equal(oldNode),
					"the replacement must be placed on a free node")

				By("verifying the replacement becomes operational on the data path")
				// The membership window above says nothing about the data path:
				// the member appears when the layout step is applied, before the
				// agents confirm it and before DRBD shakes hands. So both diskful
				// nodes are polled until they run with the new peer; the old
				// tie-breaker leaving while they still do not is the failure.
				awaitReplacementOperational(ctx, trv, diskfulNodes, oldName, newPeer)

				By("waiting for the replaced tie-breaker to leave")
				awaitRVRGone(ctx, oldTB, oldUID)
				awaitDatameshMemberGone(ctx, trv, oldName)

				By("the volume is back to a converged 2D+1TB with the new tie-breaker")
				// Same end-state matcher the migration specs use: layout
				// 2D+1TB, exactly one tie-breaker, LayoutConverged=Converged.
				trv.Await(ctx, migratedToR2())
				Expect(tieBreakerRVR(trv).Object().GetUID()).To(Equal(newTB.Object().GetUID()))

				By("verifying on the nodes that the old tie-breaker is gone and the new one is in")
				for _, node := range diskfulNodes {
					otherDiskful := drbdPeerNameOn(trv, otherThan(diskfulNodes, node))
					f.AwaitDRBDPeers(ctx, node, drbdResourceOn(trv, node), otherDiskful, newPeer)

					rvrOnNode(trv, node).Await(ctx, rvrConnectedPeers(
						rvrOnNode(trv, otherThan(diskfulNodes, node)).Name(), newTB.Name()))
				}
				expectDRBDQuorum(ctx, trv)
			})

		// E2E-TB2 — the no-free-node case and the manual escape that ends it.
		//
		// Disruptive twice over: the spec removes a finalizer by hand (the one
		// documented exception, see RUNNING.md), which makes the force-removal
		// irreversible, and it writes to a raw device to prove I/O never
		// stopped. It builds its own three-node eligible set, so it runs on a
		// cluster of any size rather than being skipped.
		It("keeps a terminating tie-breaker working when no node can host a replacement",
			SpecTimeout(30*time.Minute), Label(fw.LabelDisruptive), require.MinNodes(3),
			func(ctx SpecContext) {
				By("carving out an eligible set of exactly three nodes")
				placements := f.Discovery.UsableDiskfulPlacements()
				Expect(len(placements)).To(BeNumerically(">=", 3),
					"MinNodes(3) admitted the spec, so discovery must offer three placements")
				placements = placements[:3]
				scopedNodes := placementNodes(placements)

				// A node selector is the only thing that bounds where a
				// tie-breaker may go: it needs no storage, so restricting the
				// LVG list alone would still leave every other node eligible.
				scope := f.UniqueName("tb-scope")
				scopeByNode := make(map[string]string, len(scopedNodes))
				for _, node := range scopedNodes {
					scopeByNode[node] = scope
				}
				f.SetNodeLabel(ctx, fw.NodeScopeLabelKey, scopeByNode)

				By("creating a storage class pinned to those three nodes")
				trsc := f.TestRSC().
					StorageType(v1alpha1.ReplicatedStoragePoolTypeLVMThin).
					StorageLVMVolumeGroups(placementLVGs(placements)...).
					ReclaimPolicy(v1alpha1.RSCReclaimPolicyDelete).
					Topology(v1alpha1.TopologyIgnored).
					FTT(1).GMDR(0).
					NodeLabelSelector(&metav1.LabelSelector{
						MatchLabels: map[string]string{fw.NodeScopeLabelKey: scope},
					})
				trsc.Create(ctx)
				trsc.Await(ctx, tkmatch.ConditionStatus(
					v1alpha1.ReplicatedStorageClassCondReadyType, "True"))

				By("asserting the eligible set is exactly those three nodes")
				awaitEligibleNodes(ctx, rscPool(ctx, trsc), scopedNodes)

				By("creating a healthy 2D+1TB volume that fills the whole eligible set")
				trv := f.TestRV().RSCName(trsc.Name())
				trv.Create(ctx)
				trv.Await(ctx, match.RV.FormationComplete())
				trv.Await(ctx, match.RV.Members(3))
				trv.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeCondLayoutConvergedType,
					v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverged))
				Expect(layoutOf(trv)).To(Equal(ptr.To("2D+1TB")))

				trv.ActivateSafetyInvariants()
				trv.Always(match.RV.Quorum(2))

				diskfulNodes := memberNodesOfType(trv, v1alpha1.DatameshMemberTypeDiskful)
				Expect(diskfulNodes).To(HaveLen(2))

				oldTB := tieBreakerRVR(trv)
				oldName, oldUID := oldTB.Name(), tieBreakerUID(trv)
				oldNode := oldTB.Object().Spec.NodeName
				oldPeer := fw.DRBDPeerName(oldTB.ID())

				By("attaching the volume and writing to the raw device")
				trva := trv.Attach(ctx, diskfulNodes[0])
				trva.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeAttachmentCondAttachedType,
					v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttached))
				io := startVolumeIO(ctx, trv, trva)
				ioBefore := ioProgressed(ctx, io, ioAlive(ctx, io))

				By("deleting the tie-breaker: no node is left to host a replacement")
				oldTB.Delete(ctx)

				By("the volume reports CannotConverge with the scheduler's own reason")
				trv.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeCondLayoutConvergedType,
					v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonCannotConverge))
				trv.Await(ctx, tkmatch.ConditionMessageContains(
					v1alpha1.ReplicatedVolumeCondLayoutConvergedType,
					"cannot place a replacement"))

				replacement := awaitTieBreakerReplacement(ctx, trv, oldUID)
				replacement.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeReplicaCondScheduledType,
					v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonSchedulingFailed))
				Expect(replacement.Object().Spec.NodeName).To(BeEmpty(),
					"the replacement must stay unplaced while every eligible node is occupied")

				By("the terminating tie-breaker keeps working")
				// Terminating but operational: still a datamesh member and
				// still configured on both diskful nodes, so the volume never
				// lost its tiebreak protection.
				Expect(oldTB.IsPresent()).To(BeTrue())
				Expect(oldTB.Object().DeletionTimestamp).NotTo(BeNil())
				Expect(datameshMemberNames(trv)).To(ContainElement(oldName))
				for _, node := range diskfulNodes {
					cfg := f.DRBDConfig(ctx, node, drbdResourceOn(trv, node))
					Expect(cfg.HasPeer(oldPeer)).To(BeTrue(),
						"node %s dropped the terminating tie-breaker: %s", node, cfg)
				}
				expectDRBDQuorum(ctx, trv)

				By("the state is stable, not slowly converging")
				// The invariant runs on every RV snapshot that arrives while
				// the writer produces a long stretch of verified writes, so
				// this is a real window of cluster time, not a single read.
				stable := tkmatch.NewSwitch(cannotConvergeWithMember(oldName))
				trv.Always(stable)
				ioBefore = ioProgressedBy(ctx, io, ioBefore, ioSustainedWrites)

				By("checking the preconditions the manual escape requires")
				// Removing a finalizer is irreversible, so the recipe demands
				// proof that the two diskful replicas alone are healthy: the
				// state above is about to lose its tiebreak protection.
				for _, node := range diskfulNodes {
					dRVR := rvrOnNode(trv, node)
					peerRVR := rvrOnNode(trv, otherThan(diskfulNodes, node))
					dRVR.Await(ctx, tkmatch.Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy)))
					dRVR.Await(ctx, match.RVR.BackingVolumeState(string(v1alpha1.DiskStateUpToDate)))
					dRVR.Await(ctx, match.RVR.Quorum(true))

					st := f.DRBDStatus(ctx, node, drbdResourceOn(trv, node))
					Expect(st.ConnectedPeerNames()).To(ContainElement(drbdPeerNameOn(trv, peerRVR.Object().Spec.NodeName)),
						"node %s is not connected to the other diskful replica: %s", node, st)
				}
				ioBefore = ioProgressed(ctx, io, ioBefore)

				By("applying the manual escape: removing the finalizer by hand")
				stable.Disable()
				removeRVRFinalizers(ctx, oldTB)
				awaitRVRGone(ctx, oldTB, oldUID)

				By("the orphaned member is force-removed once the peers stop seeing it")
				awaitDatameshMemberGone(ctx, trv, oldName)

				By("the replacement lands on the freed node and the volume converges")
				replacement.Await(ctx, match.RVR.OnNode(oldNode))
				trv.Await(ctx, migratedToR2())
				Expect(tieBreakerRVR(trv).Object().GetUID()).To(Equal(replacement.Object().GetUID()))

				By("verifying on the nodes that the old tie-breaker is gone and the new one is in")
				newPeer := fw.DRBDPeerName(replacement.ID())
				for _, node := range diskfulNodes {
					otherDiskful := drbdPeerNameOn(trv, otherThan(diskfulNodes, node))
					f.AwaitDRBDPeers(ctx, node, drbdResourceOn(trv, node), otherDiskful, newPeer)

					rvrOnNode(trv, node).Await(ctx, rvrConnectedPeers(
						rvrOnNode(trv, otherThan(diskfulNodes, node)).Name(), replacement.Name()))
				}
				expectDRBDQuorum(ctx, trv)

				By("I/O never stopped")
				ioProgressed(ctx, io, ioBefore)
			})
	})
