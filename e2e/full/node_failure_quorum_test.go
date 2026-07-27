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

// E2E-6 — disruptive: a 2D+1TB volume must keep serving I/O when one diskful
// node is lost, because the tie-breaker keeps quorum at 2/3 (block 3, Epic 1).
// The Disruptive label auto-injects Serial + lowest priority and is skipped
// unless E2E_ALLOW_DISRUPTIVE=true.
var _ = Describe("Layout: r2 volume survives a diskful node outage",
	Label(fw.LabelDisruptive), Label(fw.LabelSlow), Label(fw.LabelFeatureQuorum), func() {

		It("keeps I/O on quorum 2/3 while a diskful node reboots, then recovers",
			SpecTimeout(20*time.Minute), require.MinNodes(2, 1), func(ctx SpecContext) {
				By("creating a healthy 2D+1TB volume")
				trv := f.TestRV().FTT(1).GMDR(0)
				trv.Create(ctx)
				trv.Await(ctx, match.RV.FormationComplete())
				trv.Await(ctx, match.RV.Members(3))
				Expect(layoutOf(trv)).To(Equal(ptr.To("2D+1TB")))
				trv.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeCondLayoutConvergedType,
					v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverged))

				diskfulNodes := memberNodesOfType(trv, v1alpha1.DatameshMemberTypeDiskful)
				Expect(diskfulNodes).To(HaveLen(2))
				survivor := diskfulNodes[0]
				victim := diskfulNodes[1]

				By("publishing the volume on the surviving diskful node")
				trva := trv.Attach(ctx, survivor)
				trva.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeAttachmentCondAttachedType,
					v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttached))

				By("writing to the raw device on the surviving node")
				// The reboot briefly disturbs replication, so allow a wider heartbeat
				// gap than the default: a blip is not a stall, but a data path that
				// stops for good still fails the checks below.
				io := startVolumeIO(ctx, trv, trva, func(o *fw.IOWorkloadOptions) {
					o.MaxHeartbeatGap = 90 * time.Second
				})
				ioBefore := ioProgressed(ctx, io, ioAlive(ctx, io))

				victimRVR := rvrOnNode(trv, victim)

				By("pinning the victim replica to Healthy before the reboot (Given: healthy volume)")
				// Establish a fresh Healthy baseline so the PhaseNot(Healthy) below can only
				// pass on a real reboot-induced dip, not a pre-existing transient non-Healthy state.
				victimRVR.Await(ctx, tkmatch.Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy)))

				// Keep the RV-level quorum invariant active for the whole outage — this
				// is the primary "quorum 2/3 holds" check. We do NOT disable it (only the
				// victim replica's per-replica health is expected to dip, and we assert
				// that dip explicitly instead of guarding against it).
				trv.Always(match.RV.QuorumCorrect())

				By("rebooting the other diskful node")
				// RebootNode returns as soon as the reboot is proven to have started, so
				// the outage itself can be observed; the handle is what completion is
				// awaited on further down.
				reboot := f.RebootNode(ctx, victim)

				By("waiting for the outage to actually take effect (victim replica leaves Healthy)")
				// We must observe the failure on a fresh snapshot before asserting
				// survival — otherwise the assertions below would match the stale
				// pre-reboot state and prove nothing.
				victimRVR.Await(ctx, tkmatch.PhaseNot(
					string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy)))

				By("I/O keeps flowing on quorum 2/3 (surviving diskful + tie-breaker)")
				// Verified device writes during the outage, not just conditions: the
				// sequence has to advance while the victim node is down.
				ioDuring := ioProgressed(ctx, io, ioBefore)
				trv.Await(ctx, tkmatch.ConditionStatus(
					v1alpha1.ReplicatedVolumeCondIOReadyType, "True"))
				trva.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeAttachmentCondAttachedType,
					v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttached))

				By("the rebooted replica rejoins and catches up after the node returns")
				reboot.AwaitCompleted(ctx)
				victimRVR.Await(ctx,
					tkmatch.Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy)))

				By("I/O kept flowing across the whole outage")
				ioProgressed(ctx, io, ioDuring)

				By("the layout is intact and converged after recovery")
				trv.Await(ctx, match.RV.Members(3))
				Expect(layoutOf(trv)).To(Equal(ptr.To("2D+1TB")))
				trv.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeCondLayoutConvergedType,
					v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverged))
			})
	})
