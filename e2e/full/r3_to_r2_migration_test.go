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

// E2E-1 / E2E-5 — r3->r2 auto-migration driven by an in-place edit of
// rsc.spec.replication. No rv.spec.replicatedStorageClassName change is ever
// used (that path is out of scope until verdict D-3).
var _ = Describe("Layout: r3->r2 migration by editing rsc.spec.replication",
	Label(fw.LabelSlow), Label(fw.LabelFeatureMembership), func() {

		// E2E-1 — migrate a single 3D volume to 2D+1TB (blocks 1+2).
		//
		// Disruptive: the spec writes to the raw DRBD device through the I/O
		// workload, which is what turns "I/O survives the retype" from a
		// condition reading into a statement about the data path.
		It("migrates a 3D volume to 2D+1TB (one diskful retyped to tie-breaker)",
			SpecTimeout(15*time.Minute), Label(fw.LabelDisruptive), require.MinNodes(3), func(ctx SpecContext) {
				By("creating an r3 storage class and a 3D volume")
				trsc := newMigrationRSC(ctx, v1alpha1.ReplicationConsistencyAndAvailability)

				trv := f.TestRV().RSCName(trsc.Name())
				trv.Create(ctx)
				trv.Await(ctx, match.RV.FormationComplete())
				trv.Await(ctx, match.RV.Members(3))
				trv.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType,
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverged))
				Expect(membershipLayoutOf(trv)).To(Equal(ptr.To("3D")))

				By("attaching the volume and keeping I/O-safety invariants active")
				trv.ActivateSafetyInvariants()
				trva := trv.Attach(ctx, trv.OccupiedNodes()[0])
				trva.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeAttachmentCondAttachedType,
					v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttached))

				By("writing to the raw device and proving the writes land before the migration")
				io := startVolumeIO(ctx, trv, trva)
				ioBefore := ioProgressed(ctx, io, ioAlive(ctx, io))

				By("recording the replica set and guarding against any resync (AddReplica)")
				rvrsBefore := rvrNames(trv)
				trv.Always(noActiveAddReplica())

				By("editing rsc.spec.replication to Availability")
				trsc.Update(ctx, func(rsc *v1alpha1.ReplicatedStorageClass) {
					rsc.Spec.Replication = v1alpha1.ReplicationAvailability //nolint:staticcheck // migration trigger
				})

				By("converging to 2D+1TB via a single in-place retype")
				// First a fresh Converging snapshot (never the stale 3D/Converged one),
				// then the atomic migratedToR2 matcher (Converged + layout 2D+1TB + 1 TB).
				trv.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType,
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverging))

				By("I/O keeps flowing while the retype is in flight")
				ioDuring := ioProgressed(ctx, io, ioBefore)

				trv.Await(ctx, migratedToR2())
				Expect(memberTypeCount(trv, v1alpha1.DatameshMemberTypeDiskful)).To(Equal(2))

				By("I/O keeps flowing after the retype completed")
				ioProgressed(ctx, io, ioDuring)

				By("verifying no replica was added (retype in place, no resync)")
				// The retype flips one existing RVR's spec.type; the RVR set is unchanged.
				// A resync would add a new diskful RVR, changing the set. The Always
				// invariant above additionally fails on any active AddReplica transition.
				Expect(rvrNames(trv)).To(Equal(rvrsBefore))

				By("verifying the retyped replica released its backing LV")
				tieBreakerRVR(trv).Await(ctx, noBackingVolume())

				By("verifying the RSC aggregate reports the rollout complete")
				trsc.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutType,
					v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutReasonRolledOutToAllVolumes))
				trsc.Await(ctx, match.RSC.VolumesAligned(1))
			})

		// E2E-5 — one edit migrates every volume of the class (blocks 1+2).
		It("migrates all volumes of a class with a single rsc.spec.replication edit",
			SpecTimeout(15*time.Minute), require.MinNodes(3), func(ctx SpecContext) {
				const nVolumes = 3

				By("creating an r3 storage class and several 3D volumes")
				trsc := newMigrationRSC(ctx, v1alpha1.ReplicationConsistencyAndAvailability)

				var volumes []*fw.TestRV
				for range nVolumes {
					trv := f.TestRV().RSCName(trsc.Name())
					trv.Create(ctx)
					volumes = append(volumes, trv)
				}
				for _, trv := range volumes {
					trv.Await(ctx, match.RV.FormationComplete())
					trv.Await(ctx, match.RV.Members(3))
					// status.membershipLayout is published with the layout report, not during
					// formation, so the string can only be read once the volume is
					// converged.
					trv.Await(ctx, tkmatch.ConditionReason(
						v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType,
						v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverged))
					Expect(membershipLayoutOf(trv)).To(Equal(ptr.To("3D")))
				}

				By("attaching one volume to keep an attachment in the migration path")
				trva := volumes[0].Attach(ctx, volumes[0].OccupiedNodes()[0])
				trva.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeAttachmentCondAttachedType,
					v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttached))

				By("recording each volume's replica set and guarding against resync")
				namesBefore := make([][]string, len(volumes))
				for i, trv := range volumes {
					namesBefore[i] = rvrNames(trv)
					trv.Always(noActiveAddReplica())
				}

				By("editing rsc.spec.replication to Availability once")
				trsc.Update(ctx, func(rsc *v1alpha1.ReplicatedStorageClass) {
					rsc.Spec.Replication = v1alpha1.ReplicationAvailability //nolint:staticcheck // migration trigger
				})

				By("observing every volume converge to 2D+1TB (atomic matcher, no stale snapshot)")
				for i, trv := range volumes {
					trv.Await(ctx, migratedToR2())
					Expect(memberTypeCount(trv, v1alpha1.DatameshMemberTypeDiskful)).To(Equal(2))
					// Retype in place: the RVR set is unchanged (no add-replica / resync).
					Expect(rvrNames(trv)).To(Equal(namesBefore[i]), "no replica added during migration")
				}

				By("verifying every retyped replica released its backing LV (LLV removed)")
				for _, trv := range volumes {
					tieBreakerRVR(trv).Await(ctx, noBackingVolume())
				}

				By("observing the RSC aggregate reach aligned=N without stalling")
				trsc.Await(ctx, match.RSC.VolumesAligned(nVolumes))
				trsc.Await(ctx, match.RSC.VolumesStale(0))
				trsc.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutType,
					v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutReasonRolledOutToAllVolumes))
			})
	})
