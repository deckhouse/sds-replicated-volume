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

// E2E-4 — a layout divergence outside the convergence whitelist must be
// reported honestly (reason + exact arithmetic) and must NOT trigger any action
// (block 1). r2->r3 upsize is the negative case for the future US-2.4.
var _ = Describe("Layout: unsupported divergence is reported, not acted upon",
	Label(fw.LabelSlow), Label(fw.LabelFeatureStatus), func() {

		// Disruptive: the spec writes to the raw DRBD device through the I/O
		// workload, which is what turns "the volume keeps serving I/O while the
		// mismatch is reported" from a condition reading into a statement about
		// the data path.
		It("reports TransitionUnsupported for an r2->r3 upsize and leaves the layout intact",
			SpecTimeout(15*time.Minute), Label(fw.LabelDisruptive), require.MinNodes(2, 1),
			func(ctx SpecContext) {
				By("creating an r2 storage class and a 2D+1TB volume")
				trsc := newMigrationRSC(ctx, v1alpha1.ReplicationAvailability)

				trv := f.TestRV().RSCName(trsc.Name())
				trv.Create(ctx)
				trv.Await(ctx, match.RV.FormationComplete())
				trv.Await(ctx, match.RV.Members(3))
				trv.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType,
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverged))
				Expect(membershipLayoutOf(trv)).To(Equal(ptr.To("2D+1TB")))

				By("attaching the volume on a diskful node")
				diskfulNodes := memberNodesOfType(trv, v1alpha1.DatameshMemberTypeDiskful)
				Expect(diskfulNodes).To(HaveLen(2))
				trva := trv.Attach(ctx, diskfulNodes[0])
				trva.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeAttachmentCondAttachedType,
					v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttached))

				By("writing to the raw device and proving the writes land before the edit")
				// The writer starts before the unsupported edit so the same
				// process spans the whole mismatch window: a data path that
				// stops when the layout diverges cannot hide behind a restart.
				io := startVolumeIO(ctx, trv, trva)
				ioBefore := ioProgressed(ctx, io, ioAlive(ctx, io))

				rvrCountBefore := trv.RVRCount()

				By("editing rsc.spec.replication to ConsistencyAndAvailability (upsize, out of whitelist)")
				trsc.Update(ctx, func(rsc *v1alpha1.ReplicatedStorageClass) {
					rsc.Spec.Replication = v1alpha1.ReplicationConsistencyAndAvailability //nolint:staticcheck // deliberate unsupported edit
				})

				By("observing MembershipLayoutConverged=False/TransitionUnsupported with the exact arithmetic")
				trv.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType,
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonTransitionUnsupported))
				trv.Await(ctx, tkmatch.ConditionStatus(
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType, "False"))
				trv.Await(ctx, tkmatch.ConditionMessageContains(
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType, "have 2D+1TB, want 3D"))

				By("verifying the replica composition is untouched (no new RVR / diskful)")
				Expect(membershipLayoutOf(trv)).To(Equal(ptr.To("2D+1TB")))
				Expect(trv.RVRCount()).To(Equal(rvrCountBefore))
				Expect(memberTypeCount(trv, v1alpha1.DatameshMemberTypeDiskful)).To(Equal(2))
				Expect(memberTypeCount(trv, v1alpha1.DatameshMemberTypeTieBreaker)).To(Equal(1))

				By("verifying the volume stays healthy and serving I/O despite the mismatch")
				// Readiness is reported per attachment and per replica, not on the RV.
				// RVA Ready (reason Ready) means Attached=True and ReplicaReady=True and
				// is the condition the CSI driver gates publishing on. ReplicaReady is a
				// copy of the RVR Ready condition, so the replica assertion below is not
				// extra coverage of the replica — it checks that the copy still tracks
				// its source.
				trva.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeAttachmentCondReadyType,
					v1alpha1.ReplicatedVolumeAttachmentCondReadyReasonReady))
				rvrOnNode(trv, diskfulNodes[0]).Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeReplicaCondReadyType,
					v1alpha1.ReplicatedVolumeReplicaCondReadyReasonReady))
				// Verified device writes inside the mismatch window, not only
				// the condition: the sequence has to advance while the volume
				// reports TransitionUnsupported.
				ioDuring := ioProgressed(ctx, io, ioBefore)

				By("verifying the RSC aggregate is honestly not rolled out")
				trsc.Await(ctx, tkmatch.ConditionStatus(
					v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutType, "False"))
				trsc.Await(ctx, match.RSC.VolumesAligned(0))

				By("reverting to Availability and observing MembershipLayoutConverged recover to Converged")
				trsc.Update(ctx, func(rsc *v1alpha1.ReplicatedStorageClass) {
					rsc.Spec.Replication = v1alpha1.ReplicationAvailability //nolint:staticcheck // revert
				})
				trv.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType,
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverged))
				Expect(membershipLayoutOf(trv)).To(Equal(ptr.To("2D+1TB")))

				By("I/O kept flowing across the mismatch window and the revert")
				ioProgressed(ctx, io, ioDuring)
			})
	})
