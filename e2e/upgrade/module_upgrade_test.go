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

package upgrade

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	fw "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/match"
	tkmatch "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

// The whole suite is one Ordered container: one stand, one set of volumes, three
// phases that MUST run in this order against it.
//
// The labels sit on the CONTAINER, never on the specs. Disruptive auto-injects
// Serial into every node that declares it, and Ginkgo refuses a Serial node
// inside an Ordered container that is not itself Serial — an error raised while
// the tree is built, i.e. one that compiling the package cannot catch and that
// takes the whole suite down before its first spec. On the container the
// injection lands on the container itself, which is exactly the placement Ginkgo
// asks for, and nothing is lost: the class gate reads the labels in scope for the
// spec (container hierarchy included) and so does the timeout policy, which is
// what lets the phases below carry an explicit SpecTimeout above 30s.
//
// LongHaul is deliberately NOT among them. It would license nothing Slow does not
// already license here (every phase states its own SpecTimeout), while adding a
// SECOND opt-in gate: with E2E_ALLOW_LONG_HAUL unset the phases would be skipped
// — and skipped in JustBeforeEach, i.e. after BeforeAll installed the module and
// created every volume.
var _ = Describe("Module upgrade: r3 volumes under load survive a module retag and an r3->r2 migration",
	Ordered, Label(fw.LabelSlow), Label(fw.LabelDisruptive), Label(fw.LabelFeatureMembership),
	func() {
		// Computed HERE, in the container body — that is, during tree
		// construction, which Ginkgo runs inside RunSpecs and therefore after the
		// gate validated the environment. In a package variable initializer these
		// would be computed before TestUpgrade ran at all. See budgetN for why the
		// budgets use a budgeted count rather than the real one.
		nb := budgetN()
		bootstrapBudget := 10*time.Minute + time.Duration(nb)*time.Minute
		phaseABudget := 5*time.Minute + time.Duration(nb)*30*time.Second
		phaseBBudget := 20 * time.Minute
		phaseCBudget := 10*time.Minute + time.Duration(nb)*2*time.Minute

		// The state the three phases share. It is created ONCE, by the BeforeAll
		// below, and only read afterwards.
		var (
			trsc    *fw.TestRSC
			volumes []*upgradeVolume
		)

		// The setup is a BeforeAll placed DIRECTLY in the Ordered container, with
		// no Context or When in between — because of where its cleanups land. A
		// DeferCleanup registered while a BeforeAll runs becomes a CleanupAfterAll
		// of that BeforeAll's IMMEDIATE parent container, so an intermediate
		// container would stop the writers when its own last spec ended. The
		// phases after it would then measure a volume nobody writes to, and the
		// freeze detection would report a green run for a data path that was not
		// being exercised at all.
		//
		// For the same reason everything that has to outlive a single phase is
		// created here and nowhere else: the writer pods, their claims, the
		// storage class and the namespace all register their own cleanup, and
		// registering it from a phase would tie it to that phase.
		//
		// NodeTimeout, not SpecTimeout: this is not a spec. E2E_TIMEOUT_MULTIPLIER
		// does not scale it — the multiplier reaches It nodes only.
		BeforeAll(func(ctx SpecContext) {
			pool := f.Discovery.From(poolType)

			By("requiring a stand that can host a volume with " + fmt.Sprint(diskfulReplicas) + " diskful replicas")
			usable := pool.UsableDiskfulNodeCount()
			Expect(usable).To(BeNumerically(">=", diskfulReplicas),
				"pool %q offers %d usable diskful node(s) and an r3 volume needs %d, one per replica."+
					" The suite fails instead of skipping here: a stand that shrank is a degradation"+
					" to report, not a reason to pass quietly.",
				pool.PoolName(), usable, diskfulReplicas)

			By("creating the test namespace and an r3 storage class")
			tns := f.TestNS()
			tns.Create(ctx)

			// spec.replication and NOT failuresToTolerate/guaranteedMinimumDataRedundancy:
			// the CEL rules of the RSC forbid holding both, and phase C migrates by
			// editing exactly this field. reclaimPolicy is required by the CRD and
			// has no default, and Delete is what takes the PVs — and with them the
			// ReplicatedVolumes and their logical volumes — off the shared stand
			// when the claims are removed.
			trsc = f.TestRSC().
				StorageType(poolType).
				StorageLVMVolumeGroups(pool.LVMVolumeGroups()...).
				ReclaimPolicy(v1alpha1.RSCReclaimPolicyDelete).
				Topology(v1alpha1.TopologyIgnored).
				Replication(v1alpha1.ReplicationConsistencyAndAvailability)
			trsc.Create(ctx)
			trsc.Await(ctx, tkmatch.ConditionStatus(
				v1alpha1.ReplicatedStorageClassCondReadyType, string(metav1.ConditionTrue)))

			By("deciding how many volumes the pool can hold")
			count := f.PlanVolumeCount(ctx, fw.VolumeCountOptions{
				Pool:            pool,
				VolumeSize:      volumeSize,
				DiskfulReplicas: diskfulReplicas,
				Override:        volumesOverride,
			})

			By(fmt.Sprintf("creating %d claims, each with a pod writing to it continuously", count))
			for i := range count {
				w := f.StartPodIOWorkload(ctx, fw.PodIOWorkloadOptions{
					Namespace:        tns.Name(),
					StorageClassName: trsc.Name(),
					Name:             f.UniqueName(fmt.Sprintf("io%d", i)),
					Size:             volumeSize,
					MaxHeartbeatGap:  maxIOFreeze,
				})

				// The claim's volume name is the ReplicatedVolume's name only
				// because the CSI driver names the volume after the CSI request and
				// the provisioner names the PV the same. VerifyVolumeHandle asserts
				// that chain once per volume, so a change in the naming scheme is
				// reported as what it is instead of as a missing object.
				w.VerifyVolumeHandle(ctx)

				trv := f.TestRVExact(w.VolumeName())
				trv.Get(ctx)

				volumes = append(volumes, &upgradeVolume{io: w, rv: trv})
			}

			By("waiting until every writer is past its first writes")
			for _, v := range volumes {
				awaitIOProgress(ctx, v.io)
			}
		}, NodeTimeout(bootstrapBudget))

		It("keeps every r3 volume healthy and writing while the module runs the old version",
			SpecTimeout(phaseABudget), func(ctx SpecContext) {
				By("observing every writer pod alive on its bound claim")
				for _, v := range volumes {
					assertIOAlive(ctx, v.io)
				}

				By("observing every volume formed with three diskful replicas")
				for _, v := range volumes {
					v.rv.Await(ctx, match.RV.FormationComplete())
					v.rv.Await(ctx, isR3())
				}

				By("observing every replica of every volume ready for I/O")
				for _, v := range volumes {
					awaitReplicasReady(ctx, v.rv)
				}

				By("proving the data path of every volume moves")
				for _, v := range volumes {
					awaitIOProgress(ctx, v.io)
				}

				By("verifying the data file each writer recorded a checksum for")
				for _, v := range volumes {
					v.io.VerifyChecksum(ctx)
				}
			})

		It("retags the module to the new version without freezing the I/O of any volume",
			SpecTimeout(phaseBBudget), func(ctx SpecContext) {
				By("retagging the ModulePullOverride to " + toTag + " and waiting for the rollout")
				f.EnsureModuleVersion(ctx, fw.ModuleName, toTag, moduleReadyBudget)

				By("proving every writer kept writing across the rollout")
				for _, v := range volumes {
					awaitIOProgress(ctx, v.io)
				}

				// The whole journal, not the tail every progress check reads: a
				// freeze during the rollout of the agents is precisely a gap that
				// ENDED before anyone looked, and it is the main risk this phase
				// exists for.
				By("proving no volume froze longer than the tolerance while the module rolled out")
				for _, v := range volumes {
					assertNoFreeze(ctx, v.io)
				}

				By("observing every volume still formed with three ready diskful replicas")
				for _, v := range volumes {
					v.rv.Await(ctx, isR3())
					awaitReplicasReady(ctx, v.rv)
				}
			})

		It("migrates every volume to 2D+1TB on the new version with its data intact",
			SpecTimeout(phaseCBudget), func(ctx SpecContext) {
				By("editing rsc.spec.replication from ConsistencyAndAvailability to Availability")
				trsc.Update(ctx, func(rsc *v1alpha1.ReplicatedStorageClass) {
					rsc.Spec.Replication = v1alpha1.ReplicationAvailability //nolint:staticcheck // the r3->r2 migration trigger
				})

				By("observing every volume converge to two diskful replicas and one tie-breaker")
				for _, v := range volumes {
					v.rv.Await(ctx, convergedToR2())
				}

				By("observing every remaining replica ready for I/O")
				for _, v := range volumes {
					awaitReplicasReady(ctx, v.rv)
				}

				By("proving every writer kept writing across the migration")
				for _, v := range volumes {
					awaitIOProgress(ctx, v.io)
					assertNoFreeze(ctx, v.io)
				}

				By("re-reading every data file and comparing it with the checksum recorded before the upgrade")
				for _, v := range volumes {
					v.io.VerifyChecksum(ctx)
				}
			})
	})
