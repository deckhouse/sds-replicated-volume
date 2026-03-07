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

package datamesh

// Integration test: layout transitions.
//
// Verifies that the datamesh engine correctly handles layout changes when
// config FTT/GMDR changes in a single jump and all necessary RVRs (for D
// and TB members) are created or deleted simultaneously.
//
// The engine should process transitions step-by-step, following the
// multi-step ordering: GMDR-first on upgrade, FTT-first on downgrade.
// Each step is a single VotingMembership or NonVotingMembership transition
// dispatched and completed via runUntilStable.
//
// After all transitions complete, the final state must match the target
// layout exactly: correct D/TB member counts, q = voters/2+1, and
// qmr = config.GMDR + 1.
//
// Two test strategies:
//
// "All at once" — all requests provided simultaneously. Tests engine
// ordering (which transition runs first, guards, plan selection).
//
// "Step by step" — requests fed one at a time, simulating async RVR
// arrival. Verifies that:
//   - The engine processes each request when appropriate (dispatches transition).
//   - Guards block operations that aren't safe yet (e.g., TB removal before D add).
//   - q = voters/2+1 at every intermediate step (quorum invariant).
//   - The final state matches the target regardless of request arrival order.
//
// For transitions involving both D and TB changes, two orderings are tested:
// D-first (all D ops, then TB ops) and TB-first (TB ops first, then D ops).
//
// Some entries may initially fail if the dispatcher does not fully converge
// qmr in multi-step scenarios. This is intentional — the test drives the fix.

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// ──────────────────────────────────────────────────────────────────────────────
// Layout definitions (all 7 canonical layouts)
//

var (
	layout1D   = layoutEntry{ftt: 0, gmdr: 0, initD: 1, initTB: 0, initQ: 1, initQMR: 1}
	layout2DTB = layoutEntry{ftt: 1, gmdr: 0, initD: 2, initTB: 1, initQ: 2, initQMR: 1}
	layout2D   = layoutEntry{ftt: 0, gmdr: 1, initD: 2, initTB: 0, initQ: 2, initQMR: 2}
	layout3D   = layoutEntry{ftt: 1, gmdr: 1, initD: 3, initTB: 0, initQ: 2, initQMR: 2}
	layout4DTB = layoutEntry{ftt: 2, gmdr: 1, initD: 4, initTB: 1, initQ: 3, initQMR: 2}
	layout4D   = layoutEntry{ftt: 1, gmdr: 2, initD: 4, initTB: 0, initQ: 3, initQMR: 3}
	layout5D   = layoutEntry{ftt: 2, gmdr: 2, initD: 5, initTB: 0, initQ: 3, initQMR: 3}
)

// ──────────────────────────────────────────────────────────────────────────────
// Helper
//

// transitionLayout sets up the `from` layout, changes config to `to`,
// provides all needed RVRs and requests at once, and runs until stable.
// Then asserts the final state matches `to`.
func transitionLayout(from, to layoutEntry) {
	rv, _, rvrs := setupLayout(from)

	// RSP with plenty of nodes for both layouts.
	maxNodes := max(from.initD+from.initTB, to.initD+to.initTB) + 2
	nodes := make([]string, maxNodes)
	for i := range nodes {
		nodes[i] = fmt.Sprintf("node-%d", i)
	}
	rsp := mkRSP(nodes...)

	// Change config to target.
	rv.Status.Configuration.FailuresToTolerate = to.ftt
	rv.Status.Configuration.GuaranteedMinimumDataRedundancy = to.gmdr

	// Build requests and RVRs for the diff.
	var requests []v1alpha1.ReplicatedVolumeDatameshReplicaRequest

	deltaD := to.initD - from.initD
	deltaTB := to.initTB - from.initTB

	if deltaD > 0 {
		// Add D members after existing D + TB.
		startID := from.initD + from.initTB
		for i := 0; i < deltaD; i++ {
			id := startID + i
			name := fmt.Sprintf("rv-1-%d", id)
			node := fmt.Sprintf("node-%d", id)
			rvrs = append(rvrs, mkRVR(name, node, 0))
			requests = append(requests, mkJoinRequestD(name))
		}
	} else if deltaD < 0 {
		// Remove D members (last ones first). Make all D RVRs UpToDate for guards.
		for _, rvr := range rvrs {
			for _, m := range rv.Status.Datamesh.Members {
				if m.Name == rvr.Name && m.Type.HasBackingVolume() {
					rvr.Status.BackingVolume = &v1alpha1.ReplicatedVolumeReplicaStatusBackingVolume{
						State: v1alpha1.DiskStateUpToDate,
					}
				}
			}
		}
		for i := 0; i < -deltaD; i++ {
			// Remove from the end of D members.
			removeID := from.initD - 1 - i
			name := fmt.Sprintf("rv-1-%d", removeID)
			requests = append(requests, mkLeaveRequest(name))
		}
	}

	if deltaTB > 0 {
		// Add TB members after all existing + new D.
		startID := max(from.initD+from.initTB, to.initD+from.initTB)
		// Place TB after all D members in ID space.
		if deltaD > 0 {
			startID = from.initD + from.initTB + deltaD
		}
		for i := 0; i < deltaTB; i++ {
			id := startID + i
			name := fmt.Sprintf("rv-1-%d", id)
			node := fmt.Sprintf("node-%d", id)
			rvrs = append(rvrs, mkRVR(name, node, 0))
			requests = append(requests, mkJoinRequestTB(name))
		}
	} else if deltaTB < 0 {
		// Remove TB members (from the end of TB range).
		for i := 0; i < -deltaTB; i++ {
			tbID := from.initD + from.initTB - 1 - i
			name := fmt.Sprintf("rv-1-%d", tbID)
			requests = append(requests, mkLeaveRequest(name))
		}
	}

	rv.Status.DatameshReplicaRequests = requests

	// Run until all transitions complete.
	runUntilStable(rv, rsp, rvrs, FeatureFlags{})

	// Assert final state.
	// Count D and TB members.
	var dCount, tbCount int
	for _, m := range rv.Status.Datamesh.Members {
		switch {
		case m.Type.IsVoter():
			dCount++
		case m.Type == v1alpha1.DatameshMemberTypeTieBreaker:
			tbCount++
		}
	}

	context := fmt.Sprintf("%dD", from.initD)
	if from.initTB > 0 {
		context += fmt.Sprintf("+%dTB", from.initTB)
	}
	context += fmt.Sprintf("(%d,%d)", from.gmdr, from.ftt)
	context += " → "
	context += fmt.Sprintf("%dD", to.initD)
	if to.initTB > 0 {
		context += fmt.Sprintf("+%dTB", to.initTB)
	}
	context += fmt.Sprintf("(%d,%d)", to.gmdr, to.ftt)

	Expect(dCount).To(Equal(to.initD), "%s: D count", context)
	Expect(tbCount).To(Equal(to.initTB), "%s: TB count", context)
	Expect(rv.Status.Datamesh.Quorum).To(Equal(expectedQ(to.initD)), "%s: q", context)
	Expect(rv.Status.Datamesh.QuorumMinimumRedundancy).To(Equal(to.initQMR), "%s: qmr", context)
	Expect(rv.Status.DatameshTransitions).To(BeEmpty(), "%s: no active transitions", context)
}

// ──────────────────────────────────────────────────────────────────────────────
// Stepwise helper
//

// opKind describes a single member operation.
type opKind int

const (
	opAddD opKind = iota
	opRemoveD
	opAddTB
	opRemoveTB
)

type memberOp struct {
	kind opKind
	name string
	node string
}

// buildOps computes the list of member operations for a layout transition.
// Uses the same ID/node naming as transitionLayout.
func buildOps(from, to layoutEntry) []memberOp {
	var ops []memberOp

	deltaD := to.initD - from.initD
	deltaTB := to.initTB - from.initTB

	if deltaD > 0 {
		startID := from.initD + from.initTB
		for i := 0; i < deltaD; i++ {
			id := startID + i
			ops = append(ops, memberOp{
				kind: opAddD,
				name: fmt.Sprintf("rv-1-%d", id),
				node: fmt.Sprintf("node-%d", id),
			})
		}
	} else if deltaD < 0 {
		for i := 0; i < -deltaD; i++ {
			removeID := from.initD - 1 - i
			ops = append(ops, memberOp{
				kind: opRemoveD,
				name: fmt.Sprintf("rv-1-%d", removeID),
			})
		}
	}

	if deltaTB > 0 {
		startID := max(from.initD+from.initTB, to.initD+from.initTB)
		if deltaD > 0 {
			startID = from.initD + from.initTB + deltaD
		}
		for i := 0; i < deltaTB; i++ {
			id := startID + i
			ops = append(ops, memberOp{
				kind: opAddTB,
				name: fmt.Sprintf("rv-1-%d", id),
				node: fmt.Sprintf("node-%d", id),
			})
		}
	} else if deltaTB < 0 {
		for i := 0; i < -deltaTB; i++ {
			tbID := from.initD + from.initTB - 1 - i
			ops = append(ops, memberOp{
				kind: opRemoveTB,
				name: fmt.Sprintf("rv-1-%d", tbID),
			})
		}
	}

	return ops
}

// orderOps returns ops ordered by D-first or TB-first.
func orderOps(ops []memberOp, dFirst bool) []memberOp {
	var dOps, tbOps []memberOp
	for _, op := range ops {
		switch op.kind {
		case opAddD, opRemoveD:
			dOps = append(dOps, op)
		case opAddTB, opRemoveTB:
			tbOps = append(tbOps, op)
		}
	}
	if dFirst {
		return append(dOps, tbOps...)
	}
	return append(tbOps, dOps...)
}

// hasMixedOps returns true if the transition involves both D and TB changes.
func hasMixedOps(from, to layoutEntry) bool {
	return from.initD != to.initD && from.initTB != to.initTB
}

// transitionLayoutStepwise feeds requests one at a time in the given ordering,
// checking q = voters/2+1 at every intermediate step.
func transitionLayoutStepwise(from, to layoutEntry, dFirst bool) {
	rv, _, rvrs := setupLayout(from)

	// RSP with plenty of nodes.
	maxNodes := max(from.initD+from.initTB, to.initD+to.initTB) + 2
	nodes := make([]string, maxNodes)
	for i := range nodes {
		nodes[i] = fmt.Sprintf("node-%d", i)
	}
	rsp := mkRSP(nodes...)

	// Change config to target.
	rv.Status.Configuration.FailuresToTolerate = to.ftt
	rv.Status.Configuration.GuaranteedMinimumDataRedundancy = to.gmdr

	// Make all existing D RVRs UpToDate (needed for removal guards).
	for _, rvr := range rvrs {
		for _, m := range rv.Status.Datamesh.Members {
			if m.Name == rvr.Name && m.Type.HasBackingVolume() {
				rvr.Status.BackingVolume = &v1alpha1.ReplicatedVolumeReplicaStatusBackingVolume{
					State: v1alpha1.DiskStateUpToDate,
				}
			}
		}
	}

	// Build and order ops.
	ops := orderOps(buildOps(from, to), dFirst)

	// Feed one op at a time.
	for i, op := range ops {
		switch op.kind {
		case opAddD:
			rvrs = append(rvrs, mkRVR(op.name, op.node, 0))
			rv.Status.DatameshReplicaRequests = append(
				rv.Status.DatameshReplicaRequests,
				mkJoinRequestD(op.name),
			)
		case opRemoveD:
			rv.Status.DatameshReplicaRequests = append(
				rv.Status.DatameshReplicaRequests,
				mkLeaveRequest(op.name),
			)
		case opAddTB:
			rvrs = append(rvrs, mkRVR(op.name, op.node, 0))
			rv.Status.DatameshReplicaRequests = append(
				rv.Status.DatameshReplicaRequests,
				mkJoinRequestTB(op.name),
			)
		case opRemoveTB:
			rv.Status.DatameshReplicaRequests = append(
				rv.Status.DatameshReplicaRequests,
				mkLeaveRequest(op.name),
			)
		}

		runUntilStable(rv, rsp, rvrs, FeatureFlags{})

		// Intermediate invariant: q = voters/2+1.
		var voters int
		for _, m := range rv.Status.Datamesh.Members {
			if m.Type.IsVoter() {
				voters++
			}
		}
		step := fmt.Sprintf("step %d/%d (%s)", i+1, len(ops), op.name)
		Expect(rv.Status.Datamesh.Quorum).To(
			Equal(expectedQ(voters)),
			"%s: q should be %d for %d voters", step, expectedQ(voters), voters)
	}

	// Final assertion: matches target.
	var dCount, tbCount int
	for _, m := range rv.Status.Datamesh.Members {
		switch {
		case m.Type.IsVoter():
			dCount++
		case m.Type == v1alpha1.DatameshMemberTypeTieBreaker:
			tbCount++
		}
	}

	order := "D-first"
	if !dFirst {
		order = "TB-first"
	}
	ctx := fmt.Sprintf("%s: final", order)

	Expect(dCount).To(Equal(to.initD), "%s: D count", ctx)
	Expect(tbCount).To(Equal(to.initTB), "%s: TB count", ctx)
	Expect(rv.Status.Datamesh.Quorum).To(Equal(expectedQ(to.initD)), "%s: q", ctx)
	Expect(rv.Status.Datamesh.QuorumMinimumRedundancy).To(Equal(to.initQMR), "%s: qmr", ctx)
	Expect(rv.Status.DatameshTransitions).To(BeEmpty(), "%s: no active transitions", ctx)
}

// ──────────────────────────────────────────────────────────────────────────────
// All layout pairs (used by both all-at-once and step-by-step tests)
//

// layoutPair defines a from→to layout transition.
type layoutPair struct {
	name string
	from layoutEntry
	to   layoutEntry
}

// allLayoutPairs contains all 42 ordered pairs of the 7 canonical layouts.
var allLayoutPairs = func() []layoutPair {
	layouts := []struct {
		name   string
		layout layoutEntry
	}{
		{"1D", layout1D},
		{"2D+TB", layout2DTB},
		{"2D", layout2D},
		{"3D", layout3D},
		{"4D+TB", layout4DTB},
		{"4D", layout4D},
		{"5D", layout5D},
	}
	var pairs []layoutPair
	for _, from := range layouts {
		for _, to := range layouts {
			if from.name == to.name {
				continue
			}
			pairs = append(pairs, layoutPair{
				name: from.name + " → " + to.name,
				from: from.layout,
				to:   to.layout,
			})
		}
	}
	return pairs
}()

// ──────────────────────────────────────────────────────────────────────────────
// Tests
//

var _ = Describe("integration: layout transitions", func() {
	// ── All at once (42 pairs) ─────────────────────────────────────────
	//
	// All requests provided simultaneously. Engine processes them in the
	// correct order via dispatch + guards + concurrency tracker.
	Context("all at once", func() {
		for _, tc := range allLayoutPairs {
			It(tc.name, func() { transitionLayout(tc.from, tc.to) })
		}
	})

	// ── Step by step (42 D-first + 16 TB-first = 58 entries) ──────────
	//
	// Requests fed one at a time. q invariant checked at every step.
	// Two orderings for transitions with both D and TB ops.
	Context("step by step", func() {
		for _, tc := range allLayoutPairs {
			It(tc.name+" (D-first)", func() {
				transitionLayoutStepwise(tc.from, tc.to, true)
			})
			if hasMixedOps(tc.from, tc.to) {
				It(tc.name+" (TB-first)", func() {
					transitionLayoutStepwise(tc.from, tc.to, false)
				})
			}
		}
	})
})
