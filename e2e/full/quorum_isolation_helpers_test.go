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
	"github.com/onsi/gomega/types"
	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	fw "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/match"
	tkmatch "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

// Budgets for the node-side waits of the isolation specs. They are wide on
// purpose: with the packets dropped silently, a peer is declared dead only when
// DRBD's own timers expire, and after the blockade is lifted the connection
// comes back on its own connect-int. Each wait is additionally capped by the
// spec's context, so the scaled SpecTimeout stays the outer bound.
const (
	quorumIsolationTimeout = 6 * time.Minute
	quorumRecoveryTimeout  = 10 * time.Minute
	quorumIsolationPoll    = 5 * time.Second
)

// drbdConnectionStateStandAlone is the state a connection is left in when DRBD
// gives up on it for good. It never heals by itself, so a replica sitting in it
// after the blockade is lifted is a defect and not a slow recovery.
const drbdConnectionStateStandAlone = "StandAlone"

// drbdReplicationEstablished is the replication state of a peer device that is
// connected and carries no pending resync.
const drbdReplicationEstablished = "Established"

// withoutGuards turns every guard off for the duration of fn and back on
// afterwards, whatever fn does.
//
// tkmatch.WithDisabled is the same idea for a single switch; a spec arms one
// guard per invariant per replica, and flipping them one at a time would leave
// the set half-disabled the moment an assertion inside the scope fails.
func withoutGuards(guards []*tkmatch.Switch, fn func()) {
	for _, g := range guards {
		g.Disable()
	}
	defer func() {
		for _, g := range guards {
			g.Enable()
		}
	}()
	fn()
}

// isolateReplica silently drops every replication packet of trv's replica on
// nodeName, in both directions, and returns the handle that lifts the blockade.
//
// wantPeers is the number of peers the replica is expected to have, and it is
// asserted rather than discovered: "isolate this replica from both of its
// peers" must not quietly degrade into isolating it from one, which would be a
// different scenario with a different expected outcome.
//
// The endpoints come from the DRBDResource object — the local one from
// status.addresses, the remote one from spec.peers[].paths[] — so the rules
// name exactly this volume's ports on exactly these two hosts. The blockade's
// cleanup is registered by the framework before the first rule is inserted.
func isolateReplica(ctx SpecContext, trv *fw.TestRV, nodeName string, wantPeers int) *fw.DRBDLinkBlock {
	GinkgoHelper()

	tdrbdr := rvrOnNode(trv, nodeName).DRBDR()
	tdrbdr.Await(ctx, tkmatch.Present())
	tdrbdr.Await(ctx, match.DRBDR.HasAddresses())

	links := fw.DRBDLinks(tdrbdr.Object())
	peers := linkPeers(links)
	Expect(peers).To(HaveLen(wantPeers),
		"replica %s on node %s has links to peers %v, expected %d peer(s)",
		tdrbdr.Name(), nodeName, peers, wantPeers)

	return f.BlockDRBDLinks(ctx, nodeName, links)
}

// linkPeers returns the sorted, de-duplicated peers a set of links leads to.
func linkPeers(links []fw.DRBDLink) []string {
	var peers []string
	for _, l := range links {
		if !slices.Contains(peers, l.PeerName) {
			peers = append(peers, l.PeerName)
		}
	}
	slices.Sort(peers)
	return peers
}

// awaitPeersDeclaredDead waits until the node's own kernel reports not a single
// established connection for the resource.
//
// This is the step that makes the rest of the spec meaningful. A silent drop is
// invisible to DRBD until its timers expire, so quorum is not re-evaluated for
// up to a minute after the rules land — and every assertion made before that
// would pass on the pre-outage state and prove nothing.
func awaitPeersDeclaredDead(ctx SpecContext, nodeName, resource string) {
	GinkgoHelper()
	Eventually(ctx, func() error {
		st := f.DRBDStatus(ctx, nodeName, resource)
		if len(st.ConnectedPeerNames()) == 0 {
			return nil
		}
		return fmt.Errorf("node %s still has established connections %v: %s",
			nodeName, st.ConnectedPeerNames(), st)
	}).WithTimeout(quorumIsolationTimeout).WithPolling(quorumIsolationPoll).Should(Succeed(),
		"DRBD on node %s never declared the peers of %s dead, so the blockade did not reach the kernel",
		nodeName, resource)
}

// awaitNodeQuorum waits until the kernel's own quorum verdict for the resource
// equals want.
//
// The kernel is asked directly because it is the only place the verdict is
// actually made; the replica's status is the agent's report of it and can lag.
// The comparison is against the value want, not against "not the other one": a
// node whose agent went silent reports nothing, and that must not be credited
// as a loss of quorum.
func awaitNodeQuorum(ctx SpecContext, nodeName, resource string, want bool) {
	GinkgoHelper()
	Eventually(ctx, func() error {
		st := f.DRBDStatus(ctx, nodeName, resource)
		if st.Quorum == want {
			return nil
		}
		return fmt.Errorf("node %s reports quorum=%t for %s, want %t: %s",
			nodeName, st.Quorum, resource, want, st)
	}).WithTimeout(quorumIsolationTimeout).WithPolling(quorumIsolationPoll).Should(Succeed(),
		"DRBD on node %s never reported quorum=%t for %s", nodeName, want, resource)
}

// awaitDeviceFrozenByQuorum waits until the node reports the resource's I/O
// frozen AND names quorum as the cause.
//
// The cause matters: `suspended` alone is also raised by a user freeze or by
// fencing, and only `suspended-quorum` says the freeze is the one the layout's
// on-no-quorum policy is supposed to produce.
func awaitDeviceFrozenByQuorum(ctx SpecContext, nodeName, resource string) {
	GinkgoHelper()
	Eventually(ctx, func() error {
		st := f.DRBDStatus(ctx, nodeName, resource)
		switch {
		case !st.Suspended:
			return fmt.Errorf("node %s does not report I/O as suspended for %s: %s", nodeName, resource, st)
		case !st.SuspendedQuorum:
			return fmt.Errorf("node %s froze the I/O of %s for a reason other than quorum: %s",
				nodeName, resource, st)
		}
		return nil
	}).WithTimeout(quorumIsolationTimeout).WithPolling(quorumIsolationPoll).Should(Succeed(),
		"DRBD on node %s never froze the I/O of %s on quorum loss", nodeName, resource)
}

// expectDeviceServingIO asserts the node is NOT holding the resource's I/O
// frozen right now.
func expectDeviceServingIO(ctx SpecContext, nodeName, resource string) {
	GinkgoHelper()
	st := f.DRBDStatus(ctx, nodeName, resource)
	Expect(st.Suspended).To(BeFalse(),
		"node %s froze the I/O of %s although it is on the majority side: %s", nodeName, resource, st)
}

// awaitPeersReconnected waits until every configured connection of the resource
// is established again, and refuses to wait out a connection that went
// StandAlone — that state does not heal on its own, so waiting for it would
// only burn the budget and report a timeout instead of the real fault.
func awaitPeersReconnected(ctx SpecContext, nodeName, resource string, wantPeers int) {
	GinkgoHelper()
	Eventually(ctx, func() error {
		st := f.DRBDStatus(ctx, nodeName, resource)
		for _, c := range st.Connections {
			if c.ConnectionState == drbdConnectionStateStandAlone {
				StopTrying(fmt.Sprintf(
					"connection %s of %s on node %s is StandAlone after the blockade was lifted;"+
						" DRBD never leaves that state by itself: %s", c.Name, resource, nodeName, st)).Now()
			}
		}
		connected := st.ConnectedPeerNames()
		if len(connected) == wantPeers && len(st.Connections) == wantPeers {
			return nil
		}
		return fmt.Errorf("node %s has %d of %d peers connected for %s: %s",
			nodeName, len(connected), wantPeers, resource, st)
	}).WithTimeout(quorumRecoveryTimeout).WithPolling(quorumIsolationPoll).Should(Succeed(),
		"the replication links of %s on node %s never came back", resource, nodeName)
}

// awaitResyncConverged waits until the two diskful replicas are fully caught up
// with each other: both devices UpToDate, the replication between them
// Established, and nothing left out of sync.
//
// Reconnecting is not the same as agreeing on the data. A resync starts after
// the link returns, and only its completion — with an out-of-sync counter the
// node actually reported as zero — says the outage left no divergence behind.
func awaitResyncConverged(ctx SpecContext, trv *fw.TestRV, diskfulNodes []string) {
	GinkgoHelper()
	Expect(diskfulNodes).To(HaveLen(2), "the resync check is written for a two-diskful volume")

	for i, node := range diskfulNodes {
		peerNode := diskfulNodes[1-i]
		resource := drbdResourceOn(trv, node)
		peerName := drbdPeerNameOn(trv, peerNode)

		Eventually(ctx, func() error {
			st := f.DRBDStatus(ctx, node, resource)
			if st.DiskState != drbdDiskStateUpToDate {
				return fmt.Errorf("node %s reports disk %s for %s: %s", node, st.DiskState, resource, st)
			}
			conn, ok := st.Connection(peerName)
			switch {
			case !ok:
				return fmt.Errorf("node %s has no connection %s for %s: %s", node, peerName, resource, st)
			case conn.ReplicationState != drbdReplicationEstablished:
				return fmt.Errorf("node %s is still replicating %s in state %s: %s",
					node, peerName, conn.ReplicationState, st)
			case !conn.InSync():
				return fmt.Errorf("node %s still has out-of-sync data towards %s: %s", node, peerName, st)
			}
			return nil
		}).WithTimeout(quorumRecoveryTimeout).WithPolling(quorumIsolationPoll).Should(Succeed(),
			"the resync between %s and %s never converged", node, peerNode)
	}
}

// drbdDiskStateUpToDate is the only disk state that says a diskful replica
// holds the whole current content.
const drbdDiskStateUpToDate = "UpToDate"

// ---------------------------------------------------------------------------
// Matchers
// ---------------------------------------------------------------------------

// conditionIs matches a condition that carries this status AND this reason, in
// one snapshot.
//
// Two independent Awaits would not do: a reason like QuorumViaPeers is
// published with both True and False (it names the mechanism, not the verdict),
// so a spec that checked the reason and the status separately could match each
// on a different snapshot and pass on a replica that was never in the state it
// claims.
func conditionIs(name, status, reason string) types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		conditioned, ok := obj.(tkmatch.Conditioned)
		if !ok {
			return false, fmt.Sprintf("object %T publishes no conditions", obj)
		}
		cond := meta.FindStatusCondition(conditioned.GetStatusConditions(), name)
		if cond == nil {
			return false, fmt.Sprintf("condition %s not found", name)
		}
		if string(cond.Status) == status && cond.Reason == reason {
			return true, fmt.Sprintf("condition %s is %s/%s", name, status, reason)
		}
		return false, fmt.Sprintf("condition %s is %s/%s, expected %s/%s",
			name, cond.Status, cond.Reason, status, reason)
	})
}

// deviceIOResumed matches a DRBDResource that explicitly reports its device as
// NOT frozen.
//
// An absent field is deliberately not a match. The claim being made is that the
// agent observed the freeze end, and "the agent reported nothing" is a
// different statement — the same trap as asserting "not quorum:true" where
// "quorum:false" is meant.
func deviceIOResumed() types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		d, ok := obj.(*v1alpha1.DRBDResource)
		if !ok {
			return false, fmt.Sprintf("object %T is not a DRBDResource", obj)
		}
		switch {
		case d.Status.DeviceIOSuspended == nil:
			return false, "device I/O suspension is not reported at all"
		case *d.Status.DeviceIOSuspended:
			return false, "device I/O is still suspended"
		}
		return true, "device I/O is not suspended"
	})
}
