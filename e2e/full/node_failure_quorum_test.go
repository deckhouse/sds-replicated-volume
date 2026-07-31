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
// unless E2E_ALLOW_DISRUPTIVE=true or E2E_RUN_ALL=true (values are parsed as
// booleans; false or an unrecognized value keeps the class skipped).
var _ = Describe("Layout: r2 volume survives a diskful node outage",
	Label(fw.LabelDisruptive), Label(fw.LabelSlow), Label(fw.LabelFeatureQuorum), func() {

		It("keeps I/O on quorum 2/3 while a diskful node reboots, then recovers",
			SpecTimeout(20*time.Minute), require.MinNodes(2, 1), func(ctx SpecContext) {
				By("creating a healthy 2D+1TB volume")
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
				survivorRVR := rvrOnNode(trv, survivor)
				tbRVR := tieBreakerRVR(trv)

				By("verifying the tie-breaker holds its vote as an intentional diskless client")
				// The whole spec rests on the tie-breaker's vote, and the kernel
				// counts a diskless voter differently depending on whether it is
				// diskless on purpose: an unintentionally diskless device counts
				// itself as `unknown` rather than `diskless`. Establishing the flag
				// before the outage is what makes the quorum arithmetic below the
				// one the layout was designed for.
				trv.AwaitIntentionalDiskless(ctx, 1)

				By("pinning the victim replica to Healthy before the reboot (Given: healthy volume)")
				// Establish a fresh Healthy baseline so the PhaseNot(Healthy) below can only
				// pass on a real reboot-induced dip, not a pre-existing transient non-Healthy state.
				victimRVR.Await(ctx, tkmatch.Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy)))

				By("arming quorum-survival invariants on the surviving replicas")
				// The RV-level threshold arithmetic must stay correct for the whole
				// outage; whether quorum is actually HELD is kernel truth and is
				// asserted per replica below.
				trv.Always(match.RV.QuorumThresholdCorrect())
				// The survivors must ride through the outage on quorum 2/3: neither
				// the surviving diskful nor the tie-breaker may lose quorum, report
				// Critical, or have its I/O suspended at any point. The victim is
				// deliberately NOT armed — it legitimately dips while its node is
				// down (and briefly reports Critical while it rejoins). Each armed
				// object gets a closing Await after recovery, which is what surfaces
				// a violation recorded on a snapshot no assertion looked at.
				for _, r := range []*fw.TestRVR{survivorRVR, tbRVR} {
					r.Await(ctx, tkmatch.Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy)))
					r.Always(match.RVR.NeverLoseQuorum())
					r.Always(match.RVR.NeverCritical())
					r.Always(match.RVR.NeverIOSuspended())
				}

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

				By("waiting for DRBD to declare the dead peer (survivor sees 1 of 2 peers)")
				// The kubelet notices the reboot within seconds, but DRBD keeps the
				// peer alive until its ping timeout (~a minute) — and quorum is only
				// re-evaluated at that declaration. I/O through the outage is proven
				// only by writes that land AFTER it; probing earlier would pass on a
				// volume that freezes the moment the peer is declared dead.
				survivorRVR.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedType,
					v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedReasonPartiallyConnected))

				By("I/O keeps flowing on quorum 2/3 (surviving diskful + tie-breaker)")
				// Verified device writes during the outage, not just conditions: the
				// sequence has to advance while the victim node is down and provably
				// dead to DRBD.
				ioDuring := ioProgressed(ctx, io, ioBefore)
				// Readiness is reported per attachment and per replica, not on the RV.
				// RVA Ready (reason Ready) is strictly stronger than Attached=True — it
				// also requires ReplicaReady=True — so the Attached assertion is covered.
				// ReplicaReady is a copy of the RVR Ready condition; the replica
				// assertion below checks that the copy still tracks its source across
				// the outage.
				trva.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeAttachmentCondReadyType,
					v1alpha1.ReplicatedVolumeAttachmentCondReadyReasonReady))
				survivorRVR.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeReplicaCondReadyType,
					v1alpha1.ReplicatedVolumeReplicaCondReadyReasonReady))

				By("the rebooted replica rejoins and catches up after the node returns")
				reboot.AwaitCompleted(ctx)
				victimRVR.Await(ctx,
					tkmatch.Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy)))

				By("the surviving replicas held quorum through the whole outage")
				// These closing Awaits also surface any invariant violation the armed
				// survivors recorded on snapshots no assertion happened to look at.
				survivorRVR.Await(ctx, tkmatch.Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy)))
				tbRVR.Await(ctx, tkmatch.Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy)))

				By("I/O kept flowing across the whole outage")
				ioProgressed(ctx, io, ioDuring)

				By("the layout is intact and converged after recovery")
				trv.Await(ctx, match.RV.Members(3))
				trv.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType,
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverged))
				Expect(membershipLayoutOf(trv)).To(Equal(ptr.To("2D+1TB")))
			})
	})
