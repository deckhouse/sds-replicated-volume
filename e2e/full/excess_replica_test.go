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

// BE2E-3 — the other half of the manual membership contract: a volume with MORE
// replicas than its configuration asks for is reported honestly and is NOT
// trimmed automatically, and the operator's shrink procedure works as it is.
//
// Unlike the recovery specs this one needs no configuration downgrade: with 3
// voters against dMin=2 the guards let a diskful go, which is precisely why
// "just delete the excess RVR" is the documented procedure.
//
// The spec also pins the metric side of the alerting pipeline on live data: the
// series the D8ReplicatedVolumeLayoutDegraded rule selects must read 0 for this
// volume while the excess is there, and 1 again once it is gone. The rest of the
// pipeline (scrape → rule → Alertmanager → ClusterAlert) is BE2E-4's subject.
var _ = Describe("Layout: an excess replica is reported and removed by hand",
	Label(fw.LabelSlow), Label(fw.LabelFeatureStatus), func() {

		It("reports an excess diskful without removing it and converges after a manual delete",
			SpecTimeout(20*time.Minute), Label(fw.LabelDisruptive), require.MinNodes(3, 1),
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
				attached := diskfulNodes[0]

				By("attaching the volume and writing to the raw device")
				trva := trv.Attach(ctx, attached)
				trva.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeAttachmentCondAttachedType,
					v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttached))
				io := startVolumeIO(ctx, trv, trva)
				ioBefore := ioProgressed(ctx, io, ioAlive(ctx, io))

				By("the converged volume reports 1 on the layout metric")
				awaitLayoutMetric(ctx, trv,
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverged, 1)

				By("adding a third diskful replica by hand, past what the configuration asks for")
				excess := f.TestRVRExact(trv.Name(), trv.FreeReplicaID()).
					Type(v1alpha1.ReplicaTypeDiskful)
				excess.Create(ctx)
				excess.Await(ctx, tkmatch.Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy)))
				trv.Await(ctx, match.RV.Members(4))

				By("the excess is reported honestly, with the exact arithmetic")
				trv.Await(ctx, layoutDegraded("3D+1TB", "2D+1TB"))
				Expect(membershipLayoutOf(trv)).To(Equal(ptr.To("3D+1TB")))
				Expect(memberTypeCount(trv, v1alpha1.DatameshMemberTypeDiskful)).To(Equal(3))
				Expect(memberTypeCount(trv, v1alpha1.DatameshMemberTypeTieBreaker)).To(Equal(1))

				By("pinning the composition: convergence must not trim the volume on its own")
				// Both invariants run on every snapshot that arrives from here on. They
				// are not vacuous: the stretch below is minutes of sustained verified
				// I/O with the reconciler free to act, so "nothing happened" is an
				// observation, not an absence of observations.
				frozen := tkmatch.NewSwitch(membersAre(memberNames(trv)))
				noRemoval := tkmatch.NewSwitch(noActiveRemoveReplica())
				trv.Always(frozen)
				trv.Always(noRemoval)

				By("the metric drops to 0 with the reason the alert rule selects")
				awaitLayoutMetric(ctx, trv,
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonTransitionUnsupported, 0)

				By("the state is stable, not slowly converging, and I/O keeps flowing")
				ioExcess := ioProgressedBy(ctx, io, ioBefore, ioSustainedWrites)
				trv.Await(ctx, layoutDegraded("3D+1TB", "2D+1TB"))

				By("removing the excess replica by hand — the documented shrink")
				// No configuration downgrade here: 3 voters against dMin=2 leave the
				// FTT/GMDR guards satisfied, so the ordinary delete is allowed. The
				// victim is not the attached one.
				frozen.Disable()
				noRemoval.Disable()
				excess.Delete(ctx)
				excess.Await(ctx, tkmatch.Deleted())

				By("the layout converges back to 2D+1TB")
				trv.Await(ctx, match.RV.Members(3))
				trv.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType,
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverged))
				Expect(membershipLayoutOf(trv)).To(Equal(ptr.To("2D+1TB")))
				Expect(memberTypeCount(trv, v1alpha1.DatameshMemberTypeDiskful)).To(Equal(2))
				trv.Await(ctx, match.RV.Quorum(2))
				expectDRBDQuorum(ctx, trv)

				By("the metric returns to 1/Converged and the data path is intact")
				awaitLayoutMetric(ctx, trv,
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverged, 1)
				ioProgressed(ctx, io, ioExcess)
			})
	})
