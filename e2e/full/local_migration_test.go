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

// E2E-LOCAL — r3->r2 migration of a volume served by a Local storage class.
//
// volumeAccess=Local means the workload can only reach the volume from a node
// that holds a diskful replica. The retype therefore MUST NOT pick the attached
// node as the one to demote to a tie-breaker: doing so would cut the running
// workload off from its data. The spec proves the guard from the volume's point
// of view — the attached node stays Diskful and the writes never stop.
var _ = Describe("Layout: r3->r2 migration with volumeAccess=Local",
	Label(fw.LabelSlow), Label(fw.LabelFeatureMembership), func() {

		// Disruptive: writes go to the raw DRBD device of the attached node.
		It("retypes a non-attached replica and keeps the attached node diskful",
			SpecTimeout(15*time.Minute), Label(fw.LabelDisruptive), require.MinNodes(3), func(ctx SpecContext) {
				By("creating a Local r3 storage class and a 3D volume")
				trsc := newMigrationRSC(ctx, v1alpha1.ReplicationConsistencyAndAvailability,
					func(rsc *fw.TestRSC) { rsc.VolumeAccess(v1alpha1.VolumeAccessLocal) })

				trv := f.TestRV().RSCName(trsc.Name())
				trv.Create(ctx)
				trv.Await(ctx, match.RV.FormationComplete())
				trv.Await(ctx, match.RV.Members(3))
				trv.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType,
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverged))
				Expect(membershipLayoutOf(trv)).To(Equal(ptr.To("3D")))

				By("attaching the volume on one of the diskful nodes")
				trv.ActivateSafetyInvariants()
				attachedNode := trv.OccupiedNodes()[0]
				trva := trv.Attach(ctx, attachedNode)
				trva.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeAttachmentCondAttachedType,
					v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttached))

				By("writing to the raw device and proving the writes land before the migration")
				io := startVolumeIO(ctx, trv, trva)
				ioBefore := ioProgressed(ctx, io, ioAlive(ctx, io))

				By("guarding the attached node against demotion for the whole migration")
				// The invariant is what makes this spec different from E2E-1: at no
				// point — not even transiently, in a liminal step — may the attached
				// node stop being a diskful voter.
				trv.Always(memberOnNodeIsDiskful(attachedNode))
				trv.Always(noActiveAddReplica())
				rvrsBefore := rvrNames(trv)

				By("editing rsc.spec.replication to Availability")
				trsc.Update(ctx, func(rsc *v1alpha1.ReplicatedStorageClass) {
					rsc.Spec.Replication = v1alpha1.ReplicationAvailability //nolint:staticcheck // migration trigger
				})

				By("converging to 2D+1TB")
				trv.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType,
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverging))

				By("I/O keeps flowing while the retype is in flight")
				ioDuring := ioProgressed(ctx, io, ioBefore)

				trv.Await(ctx, migratedToR2())

				By("I/O keeps flowing after the retype completed")
				ioProgressed(ctx, io, ioDuring)

				By("verifying the tie-breaker did NOT land on the attached node")
				tb := tieBreakerRVR(trv)
				Expect(tb.Object().Spec.NodeName).NotTo(Equal(attachedNode),
					"volumeAccess=Local must never demote the attached node")
				Expect(memberNodesOfType(trv, v1alpha1.DatameshMemberTypeDiskful)).
					To(ContainElement(attachedNode))
				Expect(memberTypeCount(trv, v1alpha1.DatameshMemberTypeDiskful)).To(Equal(2))

				By("verifying the retype happened in place (no resync, same replica set)")
				Expect(rvrNames(trv)).To(Equal(rvrsBefore))
				tb.Await(ctx, noBackingVolume())

				By("verifying the attachment survived the migration")
				trva.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeAttachmentCondAttachedType,
					v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttached))
			})
	})
