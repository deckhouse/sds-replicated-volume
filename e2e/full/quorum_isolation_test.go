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

// Budgets of the writer in these specs.
//
// quorumIOHeartbeatGap is what counts as a stall for the writer. It is wider
// than the default because a link going down disturbs replication briefly even
// on the side that keeps quorum, and narrow enough that the freeze the isolated
// side is supposed to enter is detected in seconds rather than minutes.
//
// quorumIOWait bounds a single progress wait; it has to cover the whole outage,
// because the wait that follows the recovery starts while the writer is still
// frozen.
//
// quorumFreezeBudget is the upper bound E2E-Q1 declares for the ONE freeze it
// expects (see fw.IOWorkload.DeclareFreeze). It is generous but finite, and it
// stays well inside the spec's own timeout, so a volume that never thaws fails
// the spec rather than running out its budget in silence.
// quorumIOStopWait is how long E2E-Q1 gives the writer to react to SIGTERM.
//
// The default 30s is written for a writer that is running. This one may be
// blocked in an uninterruptible device write when the spec fails mid-outage:
// cleanups run last-registered-first, so the blockade comes down and the
// workload's cleanup signals the writer immediately afterwards — while the
// connection is still coming back, which takes 40-60s on the stand. With the
// default the teardown would report a "the writer to stop: timed out" of its
// own making, and escalate to SIGKILL, on top of whatever actually failed —
// in the one spec whose subject is that freeze.
const (
	quorumIOHeartbeatGap = 45 * time.Second
	quorumIOWait         = 8 * time.Minute
	quorumIOStopWait     = 3 * time.Minute
	quorumFreezeBudget   = 15 * time.Minute
)

// E2E-Q1/E2E-Q2 — disruptive: the negative side of quorum on a 2D+1TB volume.
// The suite proves in several places that quorum is KEPT when it should be
// (E2E-6 and the tie-breaker cases); these two prove that it is LOST when it
// must be, which is the side a bug that makes quorum too permissive slips
// through. Such a bug satisfies "quorum was never lost" all the better the
// worse it is, so only an explicitly negative spec can catch it.
//
// THE OUTAGE MUST STAY A SILENT PACKET DROP (`iptables … -j DROP`). Neither
// `drbdsetup disconnect` nor `-j REJECT` may be substituted for it, however much
// faster they would make these specs:
//
//   - `disconnect` is an in-band administrative path. DRBD is told to close the
//     connection and closes it at once, so the timeout code — the code that
//     decides a peer is dead in a real outage, and the code whose quorum
//     arithmetic is under test here — never executes.
//   - `REJECT` answers with an RST or an ICMP error, which tears the connection
//     down just as promptly and is therefore observationally the same thing.
//
// Only silence makes the peer's death a matter of DRBD's own timers. A future
// "optimisation" to either of the two turns this file back into a test of the
// orderly path while still passing, which is the failure mode this paragraph
// exists to prevent. See fw.Framework.BlockDRBDLinks for the rest of the
// mechanics (narrowness, cleanup, watchdog).
//
// The Disruptive label auto-injects Serial + lowest priority and is skipped
// unless E2E_ALLOW_DISRUPTIVE=true or E2E_RUN_ALL=true (values are parsed as
// booleans; false or an unrecognized value keeps the class skipped).
var _ = Describe("Layout: r2 volume loses quorum when a replica is cut off",
	Label(fw.LabelDisruptive), Label(fw.LabelSlow), Label(fw.LabelFeatureQuorum), func() {

		It("freezes an isolated primary while the diskful+tie-breaker majority keeps serving, then recovers",
			SpecTimeout(25*time.Minute), require.MinNodes(2, 1), func(ctx SpecContext) {
				By("creating a healthy 2D+1TB volume")
				trv := f.TestRV().FTT(1).GMDR(0)
				trv.Create(ctx)
				trv.Await(ctx, match.RV.FormationComplete())
				trv.Await(ctx, match.RV.Members(3))
				trv.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType,
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverged))
				Expect(membershipLayoutOf(trv)).To(Equal(ptr.To("2D+1TB")))
				membersBefore := memberNames(trv)

				diskfulNodes := memberNodesOfType(trv, v1alpha1.DatameshMemberTypeDiskful)
				Expect(diskfulNodes).To(HaveLen(2))
				victim := diskfulNodes[0]
				survivor := diskfulNodes[1]

				victimRVR := rvrOnNode(trv, victim)
				survivorRVR := rvrOnNode(trv, survivor)
				tbRVR := tieBreakerRVR(trv)
				tbNode := tbRVR.Object().Spec.NodeName
				Expect(tbNode).NotTo(BeEmpty(), "the tie-breaker is not scheduled on any node")

				victimRes := drbdResourceOn(trv, victim)
				survivorRes := drbdResourceOn(trv, survivor)
				tbRes := drbdResourceOn(trv, tbNode)

				By("publishing the volume on the diskful node that will be cut off")
				// The isolated replica has to be the PRIMARY one: on-no-quorum
				// freezes the I/O of the node that serves it, and a spec that
				// isolated an idle replica would have no data path to observe.
				trva := trv.Attach(ctx, victim)
				trva.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeAttachmentCondAttachedType,
					v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttached))

				By("verifying the tie-breaker holds its vote as an intentional diskless client")
				// The kernel counts a diskless voter differently depending on
				// this flag — an unintentionally diskless device counts itself
				// as `unknown` rather than `diskless` — so establishing it here
				// is what makes the arithmetic below the one the layout was
				// designed for, on both sides of the split.
				trv.AwaitIntentionalDiskless(ctx, 1)

				By("writing to the raw device on the node that will be cut off")
				io := startVolumeIO(ctx, trv, trva, func(o *fw.IOWorkloadOptions) {
					o.MaxHeartbeatGap = quorumIOHeartbeatGap
					o.StartTimeout = quorumIOWait
					// The writer may still be frozen when the teardown reaches
					// it — see quorumIOStopWait.
					o.StopTimeout = quorumIOStopWait
				})
				// One freeze is the EXPECTED outcome here, so the framework is
				// told to stop treating it as a defect — within a finite bound,
				// and only once. The spec still has to prove the freeze
				// happened (ioFroze below); the declaration only removes the
				// veto, it does not make the freeze optional.
				io.DeclareFreeze(quorumFreezeBudget)
				// Progress BEFORE the outage, so the freeze below is a change
				// of state and not a writer that never started.
				ioProgressed(ctx, io, ioAlive(ctx, io))

				By("pinning every replica Healthy before the outage (Given: healthy volume)")
				for _, r := range []*fw.TestRVR{victimRVR, survivorRVR, tbRVR} {
					r.Await(ctx, tkmatch.Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy)))
				}

				By("arming quorum-survival invariants on the majority side only")
				// The majority must ride through the whole outage untouched.
				// The isolated replica is deliberately NOT armed: losing
				// quorum, reporting Critical and freezing its I/O is exactly
				// what this spec demands of it.
				//
				// This is also why the volume is built with f.TestRV() and
				// trv.ActivateSafetyInvariants() is never called: the standard
				// set arms NeverLoseQuorum/NeverCritical/NeverIOSuspended on
				// EVERY replica, including the one whose freeze is the subject
				// here, and a WithoutSafetyInvariants window around the outage
				// would switch them off for the survivors too — exactly where
				// they are worth the most. Arming them per replica (the shape
				// E2E-6 uses) keeps the majority under continuous watch while
				// leaving the isolated replica free to fail as it must.
				for _, r := range []*fw.TestRVR{survivorRVR, tbRVR} {
					r.Always(match.RVR.NeverLoseQuorum())
					r.Always(match.RVR.NeverCritical())
					r.Always(match.RVR.NeverIOSuspended())
				}

				By("silently dropping every replication packet of the primary, to both of its peers")
				block := isolateReplica(ctx, trv, victim, 2)

				By("waiting for DRBD on the isolated node to declare both peers dead")
				// A dropped packet is invisible until DRBD's timers expire, and
				// quorum is re-evaluated only at that declaration. Asserting
				// anything before it would match the pre-outage state.
				awaitPeersDeclaredDead(ctx, victim, victimRes)
				victimRVR.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedType,
					v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedReasonNotConnected))

				By("the isolated primary loses quorum and freezes its I/O")
				// Kernel ground truth first, then the control plane's report of
				// it. Quorum(false) demands the value false, not merely "not
				// true": a replica whose agent went silent reports nothing, and
				// that must not be credited as a loss of quorum.
				awaitNodeQuorum(ctx, victim, victimRes, false)
				awaitDeviceFrozenByQuorum(ctx, victim, victimRes)
				victimRVR.Await(ctx, match.RVR.Quorum(false))
				victimRVR.Await(ctx, conditionIs(
					v1alpha1.ReplicatedVolumeReplicaCondReadyType, "False",
					v1alpha1.ReplicatedVolumeReplicaCondReadyReasonQuorumLost))
				victimRVR.Await(ctx, conditionIs(
					v1alpha1.ReplicatedVolumeReplicaCondAttachedType, "False",
					v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonIOSuspended))

				By("the writer blocks instead of erroring out")
				// The data path is the claim; the conditions above are the
				// report of it. A writer that kept going would mean the volume
				// served I/O without quorum, and a writer that DIED would mean
				// it answered with errors instead of freezing.
				frozen := ioFroze(ctx, io)

				By("in the very same window, the surviving diskful and the tie-breaker keep quorum")
				// This is the asymmetry the suite was missing: one side frozen
				// while the other is provably alive, asserted together.
				awaitNodeQuorum(ctx, survivor, survivorRes, true)
				awaitNodeQuorum(ctx, tbNode, tbRes, true)
				expectDeviceServingIO(ctx, survivor, survivorRes)
				survivorRVR.Await(ctx, match.RVR.Quorum(true))
				tbRVR.Await(ctx, match.RVR.Quorum(true))
				survivorRVR.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeReplicaCondReadyType,
					v1alpha1.ReplicatedVolumeReplicaCondReadyReasonReady))

				By("lifting the blockade")
				// Nothing is done to help DRBD reconnect: it dials again on its
				// own connect-int, and recovering from the outage is under test
				// just as much as the outage itself.
				block.Remove(ctx)

				By("all three replicas reconnect on their own, none left StandAlone")
				for _, n := range []struct{ node, resource string }{
					{victim, victimRes}, {survivor, survivorRes}, {tbNode, tbRes},
				} {
					awaitPeersReconnected(ctx, n.node, n.resource, 2)
				}
				for _, r := range []*fw.TestRVR{victimRVR, survivorRVR, tbRVR} {
					r.Await(ctx, tkmatch.ConditionReason(
						v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedType,
						v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedReasonFullyConnected))
				}

				By("quorum returns everywhere, including the replica that lost it")
				awaitNodeQuorum(ctx, victim, victimRes, true)
				for _, r := range []*fw.TestRVR{victimRVR, survivorRVR, tbRVR} {
					r.Await(ctx, match.RVR.Quorum(true))
				}

				By("the thawed primary serves I/O again")
				victimRVR.DRBDR().Await(ctx, deviceIOResumed())
				victimRVR.Await(ctx, conditionIs(
					v1alpha1.ReplicatedVolumeReplicaCondAttachedType, "True",
					v1alpha1.ReplicatedVolumeReplicaCondAttachedReasonAttached))
				trva.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeAttachmentCondReadyType,
					v1alpha1.ReplicatedVolumeAttachmentCondReadyReasonReady))

				By("the data path moves again, and every beat proves its own integrity")
				// Each beat is a write, an fdatasync, a read-back and a
				// checksum comparison, so progress IS the integrity check.
				ioResumed(ctx, io, frozen)

				By("the resync converges with nothing left out of sync")
				awaitResyncConverged(ctx, trv, diskfulNodes)

				By("the layout is intact and every replica is Healthy again")
				trv.Await(ctx, match.RV.Members(3))
				trv.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType,
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverged))
				Expect(membershipLayoutOf(trv)).To(Equal(ptr.To("2D+1TB")))
				Expect(memberNames(trv)).To(Equal(membersBefore),
					"the outage changed the datamesh composition, which nothing asked for")

				By("the majority held quorum through the whole outage")
				// The closing Awaits also surface an invariant violation the
				// armed replicas recorded on a snapshot no assertion looked at.
				for _, r := range []*fw.TestRVR{victimRVR, survivorRVR, tbRVR} {
					r.Await(ctx, tkmatch.Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy)))
				}
			})

		It("denies quorum to an isolated tie-breaker while both diskful replicas keep serving, then recovers",
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
				membersBefore := memberNames(trv)

				diskfulNodes := memberNodesOfType(trv, v1alpha1.DatameshMemberTypeDiskful)
				Expect(diskfulNodes).To(HaveLen(2))
				primary := diskfulNodes[0]
				secondary := diskfulNodes[1]

				primaryRVR := rvrOnNode(trv, primary)
				secondaryRVR := rvrOnNode(trv, secondary)
				tbRVR := tieBreakerRVR(trv)
				tbNode := tbRVR.Object().Spec.NodeName
				Expect(tbNode).NotTo(BeEmpty(), "the tie-breaker is not scheduled on any node")

				primaryRes := drbdResourceOn(trv, primary)
				secondaryRes := drbdResourceOn(trv, secondary)
				tbRes := drbdResourceOn(trv, tbNode)

				By("publishing the volume on a diskful node")
				trva := trv.Attach(ctx, primary)
				trva.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeAttachmentCondAttachedType,
					v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttached))

				By("verifying the tie-breaker holds its vote as an intentional diskless client")
				trv.AwaitIntentionalDiskless(ctx, 1)

				By("writing to the raw device on the attached node")
				// No freeze is declared here, deliberately: the two diskful
				// voters are a majority of themselves, so the data path must
				// never stop — and if it does, the plain continuity rule fails
				// the spec exactly as it does everywhere else in the suite.
				io := startVolumeIO(ctx, trv, trva, func(o *fw.IOWorkloadOptions) {
					o.MaxHeartbeatGap = quorumIOHeartbeatGap
					o.StartTimeout = quorumIOWait
				})
				ioBefore := ioProgressed(ctx, io, ioAlive(ctx, io))

				By("pinning every replica Healthy before the outage (Given: healthy volume)")
				for _, r := range []*fw.TestRVR{primaryRVR, secondaryRVR, tbRVR} {
					r.Await(ctx, tkmatch.Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy)))
				}

				By("arming quorum-survival invariants on both diskful replicas")
				// The tie-breaker is not armed: it is the one that must end up
				// without quorum.
				for _, r := range []*fw.TestRVR{primaryRVR, secondaryRVR} {
					r.Always(match.RVR.NeverLoseQuorum())
					r.Always(match.RVR.NeverCritical())
					r.Always(match.RVR.NeverIOSuspended())
				}

				By("silently dropping every replication packet of the tie-breaker, to both diskful peers")
				block := isolateReplica(ctx, trv, tbNode, 2)

				By("waiting for DRBD on the tie-breaker to declare both peers dead")
				awaitPeersDeclaredDead(ctx, tbNode, tbRes)
				tbRVR.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedType,
					v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedReasonNotConnected))

				By("the isolated tie-breaker produces no quorum of its own")
				// A tie-breaker does not vote: the controller gives every
				// non-voter an impossibly high threshold precisely so it can
				// never satisfy the quorum condition alone, and its quorum
				// comes from connected diskful peers — of which it now has
				// none. A build in which it reported quorum here would be the
				// "quorum is too permissive" class of bug this spec exists for.
				awaitNodeQuorum(ctx, tbNode, tbRes, false)
				tbRVR.Await(ctx, match.RVR.Quorum(false))
				tbRVR.Await(ctx, conditionIs(
					v1alpha1.ReplicatedVolumeReplicaCondReadyType, "False",
					v1alpha1.ReplicatedVolumeReplicaCondReadyReasonQuorumViaPeers))

				By("in the very same window, both diskful replicas keep quorum and keep writing")
				awaitNodeQuorum(ctx, primary, primaryRes, true)
				awaitNodeQuorum(ctx, secondary, secondaryRes, true)
				expectDeviceServingIO(ctx, primary, primaryRes)
				primaryRVR.Await(ctx, match.RVR.Quorum(true))
				secondaryRVR.Await(ctx, match.RVR.Quorum(true))
				// Verified device writes DURING the outage, not merely
				// conditions: two voters out of two are a majority, so losing
				// the tie-breaker may not cost a single beat.
				ioDuring := ioProgressed(ctx, io, ioBefore)

				By("lifting the blockade")
				block.Remove(ctx)

				By("all three replicas reconnect on their own, none left StandAlone")
				for _, n := range []struct{ node, resource string }{
					{primary, primaryRes}, {secondary, secondaryRes}, {tbNode, tbRes},
				} {
					awaitPeersReconnected(ctx, n.node, n.resource, 2)
				}
				for _, r := range []*fw.TestRVR{primaryRVR, secondaryRVR, tbRVR} {
					r.Await(ctx, tkmatch.ConditionReason(
						v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedType,
						v1alpha1.ReplicatedVolumeReplicaCondFullyConnectedReasonFullyConnected))
				}

				By("quorum returns to the tie-breaker as well")
				awaitNodeQuorum(ctx, tbNode, tbRes, true)
				for _, r := range []*fw.TestRVR{primaryRVR, secondaryRVR, tbRVR} {
					r.Await(ctx, match.RVR.Quorum(true))
				}
				tbRVR.Await(ctx, conditionIs(
					v1alpha1.ReplicatedVolumeReplicaCondReadyType, "True",
					v1alpha1.ReplicatedVolumeReplicaCondReadyReasonQuorumViaPeers))

				By("the tie-breaker is still an intentional diskless client after the outage")
				// It came back through a reconnect, not through a re-create, so
				// its device_conf flag must be the one it was created with.
				trv.AwaitIntentionalDiskless(ctx, 1)

				By("the data path never stopped")
				ioProgressed(ctx, io, ioDuring)
				trva.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeAttachmentCondReadyType,
					v1alpha1.ReplicatedVolumeAttachmentCondReadyReasonReady))

				By("the resync converges with nothing left out of sync")
				awaitResyncConverged(ctx, trv, diskfulNodes)

				By("the layout is intact and every replica is Healthy again")
				trv.Await(ctx, match.RV.Members(3))
				trv.Await(ctx, tkmatch.ConditionReason(
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType,
					v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverged))
				Expect(membershipLayoutOf(trv)).To(Equal(ptr.To("2D+1TB")))
				Expect(memberNames(trv)).To(Equal(membersBefore),
					"the outage changed the datamesh composition, which nothing asked for")

				By("the diskful replicas held quorum through the whole outage")
				for _, r := range []*fw.TestRVR{primaryRVR, secondaryRVR, tbRVR} {
					r.Await(ctx, tkmatch.Phase(string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy)))
				}
			})
	})
