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

// E2E-TZ — r3->r2 migration of a TransZonal volume.
//
// The zone FTT guard used to model the retype as a plain loss of a zone's
// voter and blocked it, leaving TransZonal volumes Converging forever. The
// truth it missed is that the retyped replica stays in its zone as a
// tie-breaker, so the post-state 2D+1TB still survives the loss of any zone.
// This spec follows that claim end to end, down to the DRBD level.
//
// The stand normally has no zones, so the spec builds three of them out of
// topology.kubernetes.io/zone labels. That is a globally visible edit of
// system state: fw.LabelDisruptive marks it (and auto-injects Serial), and the
// framework restores the exact previous state of every label it touched —
// including labels that did not exist before.
var _ = Describe("Layout: r3->r2 migration with TransZonal topology",
	Label(fw.LabelSlow), Label(fw.LabelDisruptive), Label(fw.LabelFeatureMembership), func() {

		It("migrates 3D in three zones to 2D+1TB with the tie-breaker in the third zone",
			SpecTimeout(20*time.Minute), require.MinNodes(3), func(ctx SpecContext) {
				By("turning three diskful nodes into three zones")
				placements := f.Discovery.UsableDiskfulPlacements()
				Expect(len(placements)).To(BeNumerically(">=", 3),
					"MinNodes(3) admitted the spec, so discovery must offer three placements")
				placements = placements[:3]

				// Zone names are unique per spec, so no node outside the three
				// labelled ones can ever match this class's zones.
				zones := []string{f.UniqueName("zone-a"), f.UniqueName("zone-b"), f.UniqueName("zone-c")}
				zoneByNode := make(map[string]string, len(placements))
				for i, p := range placements {
					zoneByNode[p.NodeName] = zones[i]
				}
				f.SetNodeLabel(ctx, fw.ZoneLabelKey, zoneByNode)

				By("creating a TransZonal r3 storage class over those three zones")
				trsc := f.TestRSC().
					StorageType(v1alpha1.ReplicatedStoragePoolTypeLVMThin).
					StorageLVMVolumeGroups(placementLVGs(placements)...).
					ReclaimPolicy(v1alpha1.RSCReclaimPolicyDelete).
					Topology(v1alpha1.TopologyTransZonal).
					Zones(zones...).
					Replication(v1alpha1.ReplicationConsistencyAndAvailability)
				trsc.Create(ctx)
				trsc.Await(ctx, tkmatch.ConditionStatus(
					v1alpha1.ReplicatedStorageClassCondReadyType, "True"))

				By("creating a 3D volume and checking it really spread over the three zones")
				trv := f.TestRV().RSCName(trsc.Name())
				trv.Create(ctx)
				trv.Await(ctx, match.RV.FormationComplete())
				trv.Await(ctx, match.RV.Members(3))
				trv.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType,
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverged))
				Expect(membershipLayoutOf(trv)).To(Equal(ptr.To("3D")))
				// One diskful per labelled node, each reporting the zone we set:
				// without this the migration assertions below would say nothing
				// about zones.
				Expect(memberZones(trv, v1alpha1.DatameshMemberTypeDiskful)).To(Equal(zoneByNode))

				By("attaching the volume and writing to the raw device")
				trv.ActivateSafetyInvariants()
				trva := trv.Attach(ctx, trv.OccupiedNodes()[0])
				trva.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeAttachmentCondAttachedType,
					v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttached))
				io := startVolumeIO(ctx, trv, trva)
				ioBefore := ioProgressed(ctx, io, ioAlive(ctx, io))

				trv.Always(noActiveAddReplica())
				rvrsBefore := rvrNames(trv)

				By("editing rsc.spec.replication to Availability")
				trsc.Update(ctx, func(rsc *v1alpha1.ReplicatedStorageClass) {
					rsc.Spec.Replication = v1alpha1.ReplicationAvailability //nolint:staticcheck // migration trigger
				})

				By("observing the retype transition actually run")
				// The regression this spec guards against is a guard that never
				// lets the transition be created: the volume would sit in
				// Converging forever. Seeing the ChangeReplicaType transition in
				// flight, and then seeing it finish, is what rules that out.
				trv.Await(ctx, match.RV.HasActiveTransition(
					string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeReplicaType)))

				By("I/O keeps flowing while the retype is in flight")
				ioDuring := ioProgressed(ctx, io, ioBefore)

				trv.Await(ctx, migratedToR2())
				Expect(memberTypeCount(trv, v1alpha1.DatameshMemberTypeDiskful)).To(Equal(2))
				Expect(rvrNames(trv)).To(Equal(rvrsBefore), "retype in place, no replica added")

				By("I/O keeps flowing after the retype completed")
				ioProgressed(ctx, io, ioDuring)

				By("verifying the tie-breaker holds the third zone")
				diskfulZones := memberZones(trv, v1alpha1.DatameshMemberTypeDiskful)
				tieBreakerZones := memberZones(trv, v1alpha1.DatameshMemberTypeTieBreaker)
				Expect(diskfulZones).To(HaveLen(2))
				Expect(tieBreakerZones).To(HaveLen(1))
				for node, zone := range tieBreakerZones {
					Expect(zoneByNode[node]).To(Equal(zone), "tie-breaker changed zone")
					for dNode, dZone := range diskfulZones {
						Expect(dZone).NotTo(Equal(zone),
							"tie-breaker shares zone %s with the diskful on %s", zone, dNode)
					}
				}
				// All three zones are still represented, so the datamesh still
				// survives the loss of any one of them.
				Expect(len(diskfulZones) + len(tieBreakerZones)).To(Equal(len(zones)))

				By("verifying quorum and the tie-breaker peer at the DRBD level")
				Expect(trv.Object().Status.Datamesh.Quorum).To(BeEquivalentTo(2))
				expectDRBDQuorum(ctx, trv)
				// The peers see the tie-breaker (below); this is the other side of
				// the same claim, taken on the tie-breaker's own node: it gave its
				// disk up on purpose rather than lost it.
				trv.AwaitIntentionalDiskless(ctx, 1)
				tbPeer := drbdPeerNameOn(trv, memberNodesOfType(trv, v1alpha1.DatameshMemberTypeTieBreaker)[0])
				for _, dNode := range memberNodesOfType(trv, v1alpha1.DatameshMemberTypeDiskful) {
					st := f.DRBDStatus(ctx, dNode, drbdResourceOn(trv, dNode))
					Expect(st.ConnectedPeerNames()).To(ContainElement(tbPeer),
						"node %s does not see the tie-breaker: %s", dNode, st)
				}
			})
	})
