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
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	fw "github.com/deckhouse/sds-replicated-volume/e2e/pkg/framework"
	tkmatch "github.com/deckhouse/sds-replicated-volume/lib/go/testkit/match"
)

// Everything in this file is a thin composition of framework primitives, a
// projection of an object's state or a Gomega matcher, so it stays with the
// specs. Nothing here reaches the cluster on its own — the moment a helper needs
// a raw client call or an exec of its own, it belongs in e2e/pkg/framework.

// ioContinuityWrites is how many verified writes the suite demands to accept that
// a volume's data path moved at a given point. Every one of them is a write that
// was fsync-ed and read back inside the pod, so a handful is evidence enough —
// and small enough that checking twenty volumes does not dominate a phase.
const ioContinuityWrites = 5

// upgradeVolume ties the writer pod of one volume to the ReplicatedVolume behind
// its claim. Both handles are created in BeforeAll and shared by the phases: the
// writer keeps running across all three, and the RV handle keeps its informers
// registered so an Await in a later phase sees fresh snapshots.
type upgradeVolume struct {
	io *fw.PodIOWorkload
	rv *fw.TestRV
}

// assertIOAlive asserts that the writer is running on a bound claim and has
// already produced a verified write.
//
// Observe asserts nothing by itself — it reports — so the statement "this volume
// is alive right now" is made here.
func assertIOAlive(ctx SpecContext, w *fw.PodIOWorkload) {
	GinkgoHelper()
	st := w.Observe(ctx)
	Expect(st.Terminated).To(BeNil(), "writer %s terminated: %s", writerName(w), st)
	Expect(st.Pod.Ready).To(BeTrue(), "writer %s is not ready: %s", writerName(w), st)
	Expect(st.Stalled).To(BeFalse(), "writer %s is stalled right now: %s", writerName(w), st)
	Expect(st.GapExceeded).To(BeFalse(), "writer %s stalled earlier in the run: %s", writerName(w), st)
	Expect(st.LastSequence).To(BeNumerically(">=", 0),
		"writer %s has not verified a single write: %s", writerName(w), st)
}

// awaitIOProgress waits until the writer completes ioContinuityWrites more
// verified writes than it had when the call was made.
//
// It adds no assertion of its own on purpose: AwaitProgress already fails the
// spec when the writer terminated, when it stalled past the tolerance, or when it
// made no such progress in time. What this helper fixes — in ONE place — is how
// many writes "the data path moves" means in this suite.
func awaitIOProgress(ctx SpecContext, w *fw.PodIOWorkload) {
	GinkgoHelper()
	w.AwaitProgress(ctx, ioContinuityWrites)
}

// assertNoFreeze reads the WHOLE journal of the writer and requires its longest
// gap between two verified writes to stay within the run's tolerance.
//
// The whole journal is the point of the check: a freeze caused by a rollout is a
// gap that ENDED before the spec looked at it, so a tail read — which is what
// every progress check does — can show a happily beating writer and miss it.
func assertNoFreeze(ctx SpecContext, w *fw.PodIOWorkload) {
	GinkgoHelper()
	maxGap, freezes := w.AnalyzeFreezes(ctx)
	Expect(maxGap).To(BeNumerically("<=", maxIOFreeze),
		"writer %s stopped writing for %s, longer than the %s tolerance (%s); freezes: %s; journal: %s",
		writerName(w), maxGap, maxIOFreeze, envMaxIOFreeze, freezeList(freezes), w.JournalPath())
}

// writerName names a writer the way a failure message should: by the volume it
// writes to and by the pod that does the writing.
func writerName(w *fw.PodIOWorkload) string {
	return fmt.Sprintf("%s (pod %s/%s)", w.VolumeName(), w.Namespace(), w.PodName())
}

// freezeList renders the freezes for a failure message.
//
// The empty case is not dead code: the list holds the gaps over the WORKLOAD's
// own tolerance, while the assertion compares the longest gap against the
// SUITE's. They are the same number today (the workload is started with it), so
// an empty list next to a failed assertion would mean the two drifted apart —
// and saying so beats printing "[]".
func freezeList(freezes []fw.PodIOFreeze) string {
	if len(freezes) == 0 {
		return "none over the tolerance"
	}
	out := make([]string, 0, len(freezes))
	for _, f := range freezes {
		out = append(out, f.String())
	}
	return strings.Join(out, "; ")
}

// isR3 matches a ReplicatedVolume whose datamesh is the r3 layout: three members,
// all of them diskful.
//
// It is asserted on BOTH module versions, so it is built out of what both of them
// publish: the members and their types. status.membershipLayout and the layout
// condition are deliberately not read here — the suite is meant to compare an
// arbitrary pair of builds, and the older of the two may predate them (the
// condition was called LayoutConverged before it was renamed).
func isR3() types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		rv, ok := obj.(*v1alpha1.ReplicatedVolume)
		if !ok {
			return false, fmt.Sprintf("expected a ReplicatedVolume, got %T", obj)
		}
		matched := len(rv.Status.Datamesh.Members) == 3 &&
			memberTypeCount(rv, v1alpha1.DatameshMemberTypeDiskful) == 3
		return matched, "3 diskful members: " + datameshSummary(rv)
	})
}

// convergedToR2 matches a ReplicatedVolume that finished the r3->r2 migration:
// the layout report says converged AND the datamesh is composed of two diskful
// members and one tie-breaker.
//
// Both halves are needed and they are evaluated on ONE snapshot. The condition
// alone would already be true of the pre-migration 3D volume (it was converged
// then too); the composition alone would be reached before the transition
// reports itself complete. Together, on a single snapshot, neither a stale
// pre-migration snapshot nor a mid-migration one can satisfy them.
//
// The composition is counted rather than compared with status.membershipLayout:
// the string is a report about the members, the members are the fact, and this
// phase runs on the NEW version where the counting types are guaranteed. The
// layout string is still reported in the message, because it is what an operator
// greps for.
func convergedToR2() types.GomegaMatcher {
	return tkmatch.NewMatcher(func(obj client.Object) (bool, string) {
		rv, ok := obj.(*v1alpha1.ReplicatedVolume)
		if !ok {
			return false, fmt.Sprintf("expected a ReplicatedVolume, got %T", obj)
		}
		cond := meta.FindStatusCondition(rv.Status.Conditions,
			v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType)
		converged := cond != nil &&
			cond.Status == metav1.ConditionTrue &&
			cond.Reason == v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverged
		matched := converged &&
			len(rv.Status.Datamesh.Members) == 3 &&
			memberTypeCount(rv, v1alpha1.DatameshMemberTypeDiskful) == 2 &&
			memberTypeCount(rv, v1alpha1.DatameshMemberTypeTieBreaker) == 1

		return matched, fmt.Sprintf("converged to 2 diskful + 1 tie-breaker: %s, %s",
			conditionSummary(cond), datameshSummary(rv))
	})
}

// memberTypeCount counts the datamesh members of the given type.
func memberTypeCount(rv *v1alpha1.ReplicatedVolume, t v1alpha1.DatameshMemberType) int {
	count := 0
	for i := range rv.Status.Datamesh.Members {
		if rv.Status.Datamesh.Members[i].Type == t {
			count++
		}
	}
	return count
}

// datameshSummary renders the composition of the datamesh for a matcher message:
// the member types with their nodes, plus the layout string the controller
// reports for them.
func datameshSummary(rv *v1alpha1.ReplicatedVolume) string {
	members := make([]string, 0, len(rv.Status.Datamesh.Members))
	for _, m := range rv.Status.Datamesh.Members {
		members = append(members, fmt.Sprintf("%s=%s@%s", m.Name, m.Type, m.NodeName))
	}
	layout := "<none>"
	if rv.Status.MembershipLayout != nil {
		layout = *rv.Status.MembershipLayout
	}
	if len(members) == 0 {
		return "no members, layout " + layout
	}
	return strings.Join(members, ", ") + ", layout " + layout
}

// conditionSummary renders a condition for a matcher message, including its
// absence.
func conditionSummary(cond *metav1.Condition) string {
	if cond == nil {
		return v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType + " absent"
	}
	return fmt.Sprintf("%s=%s/%s", cond.Type, cond.Status, cond.Reason)
}

// awaitReplicasReady waits until every replica the volume's datamesh names
// reports Ready.
//
// The replica set comes from the datamesh members rather than from whatever the
// group informer happens to have delivered, so the check cannot pass by looking
// at fewer replicas than the volume has. Ready is asserted by STATUS and not by
// reason: a diskful replica is Ready/Ready while a tie-breaker is
// Ready/QuorumViaPeers, and the reason set is exactly the kind of detail that may
// differ between the two module versions this suite compares.
func awaitReplicasReady(ctx SpecContext, trv *fw.TestRV) {
	GinkgoHelper()
	members := trv.Object().Status.Datamesh.Members
	Expect(members).NotTo(BeEmpty(), "volume %s reports no datamesh members", trv.Name())
	for _, m := range members {
		trv.TestRVR(replicaID(m.Name)).Await(ctx, tkmatch.ConditionStatus(
			v1alpha1.ReplicatedVolumeReplicaCondReadyType, string(metav1.ConditionTrue)))
	}
}

// replicaID extracts the replica index from a datamesh member name ("<rv>-7" is
// replica 7), which is how the RV handle addresses its tracked replicas. Member
// names and replica names are the same names — the API guarantees the "prefix-N"
// format for both.
func replicaID(memberName string) int {
	return int((&v1alpha1.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{Name: memberName},
	}).ID())
}
