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
	"sort"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	gtypes "github.com/onsi/gomega/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apitypes "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	fw "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework"
	"github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework/match"
)

// Waiting budgets for the tie-breaker replacement specs. They bound waits that
// are not driven by a tracked object's event stream; the spec's own
// SpecTimeout (and its context) still cuts everything short.
const (
	// tbReplacementTimeout covers "a replacement appeared / the old one is
	// gone": both go through scheduling and a datamesh transition.
	tbReplacementTimeout = 8 * time.Minute
	tbReplacementPoll    = 2 * time.Second
)

// tieBreakerUID returns the UID of the volume's single tie-breaker replica.
//
// Every wait in these specs is UID-based on purpose: once the old replica is
// gone its id is free again, so a replacement may be handed the very same
// canonical name — a name-based wait would accept the new object as the old one.
func tieBreakerUID(trv *fw.TestRV) apitypes.UID {
	GinkgoHelper()
	return tieBreakerRVR(trv).Object().GetUID()
}

// awaitTieBreakerReplacement waits until a tie-breaker replica other than
// oldUID exists and is not itself being deleted, and returns its handle.
func awaitTieBreakerReplacement(ctx SpecContext, trv *fw.TestRV, oldUID apitypes.UID) *fw.TestRVR {
	GinkgoHelper()

	var found *fw.TestRVR
	Eventually(ctx, func() error {
		found = nil
		for _, r := range trv.TestRVRs() {
			if !r.IsPresent() {
				continue
			}
			obj := r.Object()
			if obj.Spec.Type != v1alpha1.ReplicaTypeTieBreaker ||
				obj.GetUID() == oldUID ||
				obj.DeletionTimestamp != nil {
				continue
			}
			found = r
			return nil
		}
		return fmt.Errorf("no replacement tie-breaker yet (the one being replaced has UID %s)", oldUID)
	}).WithTimeout(tbReplacementTimeout).WithPolling(tbReplacementPoll).Should(Succeed(),
		"volume %s never got a replacement tie-breaker", trv.Name())

	Expect(found.Object().GetUID()).NotTo(Equal(oldUID))
	return found
}

// awaitReplacementOperational waits until DRBD on every diskful node reports
// newPeer — the replacement tie-breaker — as a connected peer, with the
// departure of the tie-breaker being replaced as the deadline.
//
// This is the operational half of strict create-first, and it has to be polled:
// the membership window opens when the layout step is applied, before the
// agents have configured the replacement and long before the DRBD handshake, so
// a single read taken right after the window would be a race.
//
// Finding the old tie-breaker already gone is not a verdict on its own. The two
// observations are not atomic — membership comes from the informer, the peer
// state from an exec per node — and on a fast run the whole release chain (the
// agents report Connected, the guard lifts, Leave runs, the member is dropped)
// fits between two polls. So a departure only triggers one last, fresh read of
// both nodes: connected everywhere means the replacement was operational and
// this wait merely arrived late, which is a pass. What the spec refuses to
// accept is the old tie-breaker gone while the replacement is still not
// connected. Coexistence at the membership level is proven separately by
// tieBreakerReplacementWindow.
func awaitReplacementOperational(
	ctx SpecContext,
	trv *fw.TestRV,
	diskfulNodes []string,
	oldName, newPeer string,
) {
	GinkgoHelper()

	// nodesMissingPeer names the diskful nodes that do not have newPeer
	// connected, with the DRBD status behind the verdict.
	nodesMissingPeer := func() []string {
		var missing []string
		for _, node := range diskfulNodes {
			st := f.DRBDStatus(ctx, node, drbdResourceOn(trv, node))
			if !slices.Contains(st.ConnectedPeerNames(), newPeer) {
				missing = append(missing, fmt.Sprintf("%s: %s", node, st))
			}
		}
		return missing
	}

	Eventually(ctx, func(g Gomega) {
		// Membership first, so the peer state below is always the fresher of
		// the two: a departure is only ever judged by a read taken after it.
		oldStillMember := slices.Contains(datameshMemberNames(trv), oldName)

		missing := nodesMissingPeer()
		if len(missing) == 0 {
			return
		}
		if !oldStillMember {
			StopTrying(fmt.Sprintf(
				"tie-breaker %s left the datamesh and the replacement peer %s is still not connected on %s",
				oldName, newPeer, strings.Join(missing, "; "))).Now()
		}
		g.Expect(missing).To(BeEmpty(),
			"the replacement tie-breaker peer %s is not connected on every diskful node yet", newPeer)
	}).WithTimeout(tbReplacementTimeout).WithPolling(tbReplacementPoll).Should(Succeed(),
		"the replacement tie-breaker %s never became operational on both diskful nodes", newPeer)
}

// awaitRVRGone waits until the replica object with exactly this UID no longer
// exists. A different UID under the same name counts as gone.
func awaitRVRGone(ctx SpecContext, trvr *fw.TestRVR, uid apitypes.UID) {
	GinkgoHelper()
	Eventually(ctx, func() error {
		if !trvr.IsPresent() {
			return nil
		}
		if current := trvr.Object().GetUID(); current != uid {
			return nil
		}
		return fmt.Errorf("replica %s (UID %s) still exists", trvr.Name(), uid)
	}).WithTimeout(tbReplacementTimeout).WithPolling(tbReplacementPoll).Should(Succeed(),
		"replica %s was never removed", trvr.Name())
}

// awaitDatameshMemberGone waits until the datamesh no longer lists the member.
//
// Force-removing an orphaned member is not immediate: the datamesh refuses it
// while any surviving peer still reports the member as connected, so the wait
// covers the peers noticing the departure as well.
func awaitDatameshMemberGone(ctx SpecContext, trv *fw.TestRV, memberName string) {
	GinkgoHelper()
	Eventually(ctx, func() error {
		if slices.Contains(datameshMemberNames(trv), memberName) {
			return fmt.Errorf("%s is still a datamesh member", memberName)
		}
		return nil
	}).WithTimeout(tbReplacementTimeout).WithPolling(tbReplacementPoll).Should(Succeed(),
		"member %s was never force-removed from the datamesh", memberName)
}

// datameshMemberNames returns the names of the current datamesh members.
func datameshMemberNames(trv *fw.TestRV) []string {
	var names []string
	for _, m := range trv.Object().Status.Datamesh.Members {
		names = append(names, m.Name)
	}
	return names
}

// awaitEligibleNodes waits until the pool publishes exactly these usable
// eligible nodes. Asserting the exact set is what turns "the node selector
// worked" into a fact: a spec that silently ran with a wider set would prove
// nothing about the scenario it claims to cover.
func awaitEligibleNodes(ctx SpecContext, trsp *fw.TestRSP, want []string) {
	GinkgoHelper()
	expected := slices.Clone(want)
	sort.Strings(expected)

	Eventually(ctx, func() error {
		got := usableEligibleNodes(trsp)
		if slices.Equal(got, expected) {
			return nil
		}
		return fmt.Errorf("pool %s reports eligible nodes %v, want exactly %v", trsp.Name(), got, expected)
	}).WithTimeout(tbReplacementTimeout).WithPolling(tbReplacementPoll).Should(Succeed())
}

// removeRVRFinalizers strips every finalizer from the replica with a direct
// merge patch.
//
// This is the manual escape from debug_and_problem_solving.md, and it is the
// one place in the suite allowed to remove a finalizer by hand (see
// RUNNING.md). The tracked Update helper cannot be used: dropping the last
// finalizer of an object that already has a deletion timestamp makes it vanish,
// and Update would then wait for a resourceVersion nobody will ever publish.
func removeRVRFinalizers(ctx SpecContext, trvr *fw.TestRVR) {
	GinkgoHelper()
	rvr := &v1alpha1.ReplicatedVolumeReplica{ObjectMeta: metav1.ObjectMeta{Name: trvr.Name()}}
	patch := client.RawPatch(apitypes.MergePatchType, []byte(`{"metadata":{"finalizers":null}}`))
	Expect(client.IgnoreNotFound(f.Client.Patch(ctx, rvr, patch))).To(Succeed(),
		"removing the finalizers of %s", trvr.Name())
}

// ---------------------------------------------------------------------------
// Matchers
// ---------------------------------------------------------------------------

// tieBreakerReplacementWindow matches the create-first window: the tie-breaker
// being replaced is STILL a datamesh member while a second one has already
// joined, and status.layout reports that honestly as 2D+2TB.
//
// Evaluated on one snapshot, so it cannot be satisfied by a "before" and an
// "after" observation of two different states.
func tieBreakerReplacementWindow(oldName string) gtypes.GomegaMatcher {
	return match.RV.Custom("replacement joined while the old tie-breaker is still a member",
		func(rv *v1alpha1.ReplicatedVolume) bool {
			tieBreakers, sawOld := 0, false
			for i := range rv.Status.Datamesh.Members {
				m := &rv.Status.Datamesh.Members[i]
				if m.Type != v1alpha1.DatameshMemberTypeTieBreaker {
					continue
				}
				tieBreakers++
				if m.Name == oldName {
					sawOld = true
				}
			}
			return tieBreakers == 2 && sawOld &&
				rv.Status.Layout != nil && *rv.Status.Layout == "2D+2TB"
		})
}

// tiebreakHeld matches an RV whose datamesh still holds its two diskful voters
// and at least one tie-breaker.
//
// This is what strict create-first buys, stated as a continuous invariant: the
// old tie-breaker is released only after the replacement joined, so the volume
// is never left as a bare 2D — a layout where losing either diskful node
// freezes I/O.
//
// The quorum value is not restated here because it never moves: tie-breakers
// are not voters (DatameshMemberType.IsVoter), so the two diskful members are
// the only voters and the correct quorum is 2 in every snapshot this matcher
// accepts — including the 2D+2TB window. The framework's QuorumCorrect
// invariant checks the published quorum against the current voter count, so the
// two invariants together are exactly "quorum is 2 throughout".
func tiebreakHeld() gtypes.GomegaMatcher {
	return match.RV.Custom("two diskful and at least one tie-breaker",
		func(rv *v1alpha1.ReplicatedVolume) bool {
			diskful, tieBreakers := 0, 0
			for i := range rv.Status.Datamesh.Members {
				switch rv.Status.Datamesh.Members[i].Type {
				case v1alpha1.DatameshMemberTypeDiskful:
					diskful++
				case v1alpha1.DatameshMemberTypeTieBreaker:
					tieBreakers++
				default:
				}
			}
			return diskful == 2 && tieBreakers >= 1
		})
}

// cannotConvergeWithMember matches an RV that reports CannotConverge for the
// terminating tie-breaker while that tie-breaker is still a datamesh member.
//
// As a continuous invariant this is the "stable, not slowly converging" claim
// of the no-free-node case: the volume must neither heal by itself nor lose
// the tie-breaker that is holding its tiebreak protection.
func cannotConvergeWithMember(memberName string) gtypes.GomegaMatcher {
	return match.RV.Custom("CannotConverge with "+memberName+" still a member",
		func(rv *v1alpha1.ReplicatedVolume) bool {
			member := false
			for i := range rv.Status.Datamesh.Members {
				if rv.Status.Datamesh.Members[i].Name == memberName {
					member = true
					break
				}
			}
			if !member {
				return false
			}
			for i := range rv.Status.Conditions {
				c := &rv.Status.Conditions[i]
				if c.Type == v1alpha1.ReplicatedVolumeCondLayoutConvergedType {
					return c.Status == metav1.ConditionFalse &&
						c.Reason == v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonCannotConverge
				}
			}
			return false
		})
}

// rvrConnectedPeers matches a replica whose own status reports exactly these
// peers as Connected. It is the agent's view of the same fact drbdsetup
// reports from the node, and the two are asserted together on purpose.
func rvrConnectedPeers(names ...string) gtypes.GomegaMatcher {
	expected := slices.Clone(names)
	sort.Strings(expected)
	return match.RVR.Custom("connected peers are exactly "+fmt.Sprint(expected),
		func(r *v1alpha1.ReplicatedVolumeReplica) bool {
			var got []string
			for i := range r.Status.Peers {
				if r.Status.Peers[i].ConnectionState == v1alpha1.ConnectionStateConnected {
					got = append(got, r.Status.Peers[i].Name)
				}
			}
			sort.Strings(got)
			return slices.Equal(got, expected)
		})
}

// otherThan returns the single element of nodes that is not self.
func otherThan(nodes []string, self string) string {
	GinkgoHelper()
	var rest []string
	for _, n := range nodes {
		if n != self {
			rest = append(rest, n)
		}
	}
	Expect(rest).To(HaveLen(1), "expected exactly one node besides %s in %v", self, nodes)
	return rest[0]
}
