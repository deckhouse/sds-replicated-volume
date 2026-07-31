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
	"fmt"
	"slices"
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

// rolloutMaxParallel is the rollout budget E2E-5 gives its storage class: the
// number of volumes of that class that may carry the new configuration before it
// has converged anywhere.
//
// Two, and not a number derived from the volume count, because the budget is
// what the spec is about. It is deliberately far below any volume count the run
// can be given, so the scenario always has a queue to serve; the suite's gate
// refuses a count that would leave it without one.
const rolloutMaxParallel = 2

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
				llvsBefore := snapshotBackingLLVs(trv)
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
				assertBackingLLVIdentityAfterRetype(ctx, trv, llvsBefore)

				By("verifying the RSC aggregate reports the rollout complete")
				trsc.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutType,
					v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutReasonRolledOutToAllVolumes))
				trsc.Await(ctx, match.RSC.VolumesAligned(1))
			})

		// E2E-5 — one edit migrates every volume of the class, two at a time
		// (blocks 1+2).
		//
		// maxParallel=2 only means something if more volumes than that need the
		// new configuration and the ones that take the budget cannot hand it back
		// straight away. Both halves are arranged here: rolloutVolumes volumes —
		// ten waves of two at the default — and a maintenance hold on the DRBD
		// resources of the two the controller admits first, which stops their
		// retype from completing. What the rest do meanwhile — wait, and say so —
		// is the property under test, and it is asserted from an event-ordered
		// observer rather than from a poll, so a third volume slipping into the
		// rollout for a moment cannot go unseen.
		//
		// The wave count is what makes the limit more than a first impression: one
		// wave shows a limit exists, while a queue served over and over shows it
		// keeps holding — every slot a converged volume frees goes to exactly one
		// waiting volume, as many times in a row as there are waves.
		It("limits an r3->r2 rollout to two concurrent volumes and migrates them all",
			SpecTimeout(rolloutSpecBudget(rolloutVolumes)), require.MinNodes(3), func(ctx SpecContext) {
				nVolumes := rolloutVolumes
				const maxParallel = rolloutMaxParallel

				By("creating an r3 storage class that rolls out to two volumes at a time")
				// A dedicated class: maxParallel is a class-wide property, and the
				// shared class is shared with the specs running next to this one.
				trsc := newMigrationRSC(ctx, v1alpha1.ReplicationConsistencyAndAvailability,
					func(rsc *fw.TestRSC) {
						rsc.ConfigurationRolloutStrategyType(v1alpha1.ConfigurationRolloutRollingUpdate).
							ConfigurationRolloutStrategyMaxParallel(maxParallel)
					})

				By(fmt.Sprintf("creating %d 3D volumes with deterministic, sorted names", nVolumes))
				// The controller hands out the budget in name order, so the names are
				// what decide which two volumes go first — and therefore which two the
				// hold below has to cover. rolloutVolumeSuffix is what keeps the two
				// orders the same at any volume count.
				volumes := make([]*fw.TestRV, 0, nVolumes)
				volumeNames := make([]string, 0, nVolumes)
				for i := range nVolumes {
					trv := f.TestRV(rolloutVolumeSuffix(i, nVolumes)).RSCName(trsc.Name())
					trv.Create(ctx)
					volumes = append(volumes, trv)
					volumeNames = append(volumeNames, trv.Name())
				}
				Expect(slices.IsSorted(volumeNames)).To(BeTrue(),
					"the volumes must be named in the order the controller admits them")

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

				By("attaching the last volume to keep an attachment in the migration path")
				// The last one: it migrates in the final wave, so the attachment is
				// exercised by a volume that had to sit through the whole queue, and it
				// stays clear of the maintenance hold.
				last := volumes[nVolumes-1]
				trva := last.Attach(ctx, last.OccupiedNodes()[0])
				trva.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeAttachmentCondAttachedType,
					v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttached))

				By("recording each volume's replica set and backing LVs, and guarding against resync")
				rvrsBefore := make([][]string, nVolumes)
				llvsBefore := make([]map[string]fw.LLVIdentity, nVolumes)
				for i, trv := range volumes {
					rvrsBefore[i] = rvrNames(trv)
					llvsBefore[i] = snapshotBackingLLVs(trv)
					trv.Always(noActiveAddReplica())
				}

				By("holding the DRBD resources of the first two volumes in maintenance")
				lifts := make([]func(SpecContext), 0, maxParallel)
				for _, trv := range volumes[:maxParallel] {
					lifts = append(lifts, pauseDRBDReconciliation(ctx, trv))
				}

				By("watching the configuration rollout of the whole class")
				// Started before the edit: the observer accounts every volume that
				// already exists before it returns, so nothing about the rollout can
				// happen behind its back.
				observer := f.ObserveConfigurationRollout(ctx, fw.ConfigurationRolloutObserverOptions{
					RSCName:     trsc.Name(),
					MaxParallel: maxParallel,
					Active:      rolloutActive,
					Waiting:     rolloutWaiting,
				})

				By("editing rsc.spec.replication to Availability once")
				trsc.Update(ctx, func(rsc *v1alpha1.ReplicatedStorageClass) {
					rsc.Spec.Replication = v1alpha1.ReplicationAvailability //nolint:staticcheck // migration trigger
				})

				By("exactly two volumes take the budget and the rest wait for a slot")
				blocked := observer.Await(ctx,
					fmt.Sprintf("%d volumes rolling out and the other %d waiting for a slot",
						maxParallel, nVolumes-maxParallel),
					func(s fw.ConfigurationRolloutSnapshot) bool {
						return len(s.Active) == maxParallel && len(s.EverWaiting) >= nVolumes-maxParallel
					})
				Expect(blocked.Active).To(Equal(volumeNames[:maxParallel]),
					"the budget must go to the first volumes in name order")
				Expect(blocked.EverWaiting).To(ContainElements(volumeNames[maxParallel:]),
					"the volumes left out of the budget must report that they are queued")

				By("lifting the maintenance hold so the first two can finish")
				for _, lift := range lifts {
					lift(ctx)
				}

				By("observing every volume converge to 2D+1TB (atomic matcher, no stale snapshot)")
				for i, trv := range volumes {
					trv.Await(ctx, migratedToR2())
					Expect(memberTypeCount(trv, v1alpha1.DatameshMemberTypeDiskful)).To(Equal(2))
					// Retype in place: the RVR set is unchanged (no add-replica / resync).
					Expect(rvrNames(trv)).To(Equal(rvrsBefore[i]), "no replica added during migration")
				}

				By("the rollout drained without ever exceeding the budget")
				// Awaited on the observer, not asserted straight away: this is what
				// makes the accounting below cover the whole rollout, every wave of it,
				// instead of whatever had reached it by then.
				drained := observer.Await(ctx, "the rollout to drain",
					func(s fw.ConfigurationRolloutSnapshot) bool {
						return len(s.Active) == 0 && len(s.Waiting) == 0
					})
				Expect(drained.OverLimit).To(BeFalse(),
					"the class rolled out to more than %d volumes at a time: %s", maxParallel, drained)
				Expect(drained.MaxActive).To(Equal(maxParallel),
					"the class never used its whole rollout budget: %s", drained)
				Expect(len(drained.EverWaiting)).To(BeNumerically(">=", nVolumes-maxParallel),
					"the volumes outside the budget must have been observed waiting: %s", drained)

				By("verifying every retyped replica released its backing LV (LLV removed)")
				for i, trv := range volumes {
					tieBreakerRVR(trv).Await(ctx, noBackingVolume())
					assertBackingLLVIdentityAfterRetype(ctx, trv, llvsBefore[i])
				}

				By("observing the RSC aggregate reach aligned=N without stalling")
				trsc.Await(ctx, match.RSC.VolumesAligned(int32(nVolumes)))
				trsc.Await(ctx, match.RSC.VolumesStale(0))
				trsc.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutType,
					v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutReasonRolledOutToAllVolumes))
			})
	})
