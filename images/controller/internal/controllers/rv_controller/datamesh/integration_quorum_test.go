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

// Integration test: quorum invariants under stable configuration.
//
// For each canonical layout (FTT/GMDR combination), starts at the minimum
// member count and runs a full add/remove D lifecycle:
//
//  1. Verify removal at minimum is blocked (guards protect FTT/GMDR).
//  2. Sequentially add +1, +2, +3 D members.
//  3. Sequentially remove back to minimum.
//  4. Verify removal at minimum is blocked again.
//
// At every step, two invariants are checked:
//
//   - q == voters/2 + 1 (majority quorum, never inflated)
//   - qmr == config.GMDR + 1 (constant under stable config)
//
// Config stays constant throughout — no FTT/GMDR changes. TB members
// are included in layouts that require them and stay for the duration.
// This test catches regressions in q/qmr computation (raiseQ/lowerQ),
// guard logic (FTT/GMDR preservation), and dispatch plan selection
// (correct q↑/q↓/qmr↑/qmr↓ variant).

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
//

// runUntilStable runs ProcessTransitions in a loop, confirming each step
// by setting all RVR revisions to the current DatameshRevision.
// Fails the test if max iterations exceeded.
func runUntilStable(
	rv *v1alpha1.ReplicatedVolume,
	rsp RSP,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	features FeatureFlags, //nolint:unparam // structurally needed for ProcessTransitions signature
) {
	const maxIter = 30
	for range maxIter {
		changed, _ := ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, features)
		if !changed {
			return
		}
		// Simulate all replicas confirming the latest revision.
		for _, rvr := range rvrs {
			rvr.Status.DatameshRevision = rv.Status.DatameshRevision
		}
		if len(rv.Status.DatameshTransitions) == 0 {
			return
		}
	}
	Fail(fmt.Sprintf("runUntilStable: did not stabilize after %d iterations", maxIter))
}

// layoutEntry describes a canonical layout for the table-driven test.
type layoutEntry struct {
	ftt, gmdr byte
	initD     int
	initTB    int
	initQ     byte
	initQMR   byte
}

// expectedQ returns the expected quorum for the given voter count.
func expectedQ(voters int) byte {
	return byte(voters/2 + 1)
}

// setupLayout builds the initial state for a canonical layout: RV with correct
// config/q/qmr/baseline, members, UpToDate RVRs, and an RSP with extra nodes.
func setupLayout(e layoutEntry) (
	*v1alpha1.ReplicatedVolume,
	*testRSP,
	[]*v1alpha1.ReplicatedVolumeReplica,
) {
	total := e.initD + e.initTB
	members := make([]v1alpha1.DatameshMember, 0, total)
	rvrs := make([]*v1alpha1.ReplicatedVolumeReplica, 0, total+3)

	// D members: rv-1-0..rv-1-{initD-1}.
	for i := 0; i < e.initD; i++ {
		name := fmt.Sprintf("rv-1-%d", i)
		node := fmt.Sprintf("node-%d", i)
		members = append(members, mkMember(name, v1alpha1.DatameshMemberTypeDiskful, node))
		rvrs = append(rvrs, mkRVRUpToDate(name, node, 5))
	}

	// TB members: rv-1-{initD}..
	for i := 0; i < e.initTB; i++ {
		id := e.initD + i
		name := fmt.Sprintf("rv-1-%d", id)
		node := fmt.Sprintf("node-%d", id)
		members = append(members, mkMember(name, v1alpha1.DatameshMemberTypeTieBreaker, node))
		rvrs = append(rvrs, mkRVR(name, node, 5))
	}

	// RSP: all existing nodes + 3 extra for adds.
	nodes := make([]string, 0, total+3)
	for i := 0; i < total+3; i++ {
		nodes = append(nodes, fmt.Sprintf("node-%d", i))
	}
	rsp := mkRSP(nodes...)

	// RV with correct config, q, qmr, baseline.
	rv := mkRV(5, members, nil, nil)
	rv.Status.Configuration.FailuresToTolerate = e.ftt
	rv.Status.Configuration.GuaranteedMinimumDataRedundancy = e.gmdr
	rv.Status.Datamesh.Quorum = e.initQ
	rv.Status.Datamesh.QuorumMinimumRedundancy = e.initQMR
	rv.Status.BaselineGuaranteedMinimumDataRedundancy = e.gmdr

	return rv, rsp, rvrs
}

// addD adds a Diskful member to the layout: creates a Join request and a new
// RVR, runs until stable, and returns the new member ID.
func addD(
	rv *v1alpha1.ReplicatedVolume,
	rsp *testRSP,
	rvrs *[]*v1alpha1.ReplicatedVolumeReplica,
	nextID int,
) {
	name := fmt.Sprintf("rv-1-%d", nextID)
	node := fmt.Sprintf("node-%d", nextID)
	newRVR := mkRVR(name, node, 0)
	*rvrs = append(*rvrs, newRVR)

	rv.Status.DatameshReplicaRequests = []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
		mkJoinRequestD(name),
	}

	runUntilStable(rv, rsp, *rvrs, FeatureFlags{})

	// Clear requests after completion.
	rv.Status.DatameshReplicaRequests = nil
}

// removeD removes the last-added Diskful member: creates a Leave request,
// makes all D RVRs UpToDate, and runs until stable.
func removeD(
	rv *v1alpha1.ReplicatedVolume,
	rsp *testRSP,
	rvrs *[]*v1alpha1.ReplicatedVolumeReplica,
	memberName string,
) {
	// Make all RVRs UpToDate for GMDR guard.
	for _, rvr := range *rvrs {
		if rvr.Status.BackingVolume == nil {
			// Check if this RVR is for a D member.
			for _, m := range rv.Status.Datamesh.Members {
				if m.Name == rvr.Name && m.Type.HasBackingVolume() {
					rvr.Status.BackingVolume = &v1alpha1.ReplicatedVolumeReplicaStatusBackingVolume{
						State: v1alpha1.DiskStateUpToDate,
					}
					break
				}
			}
		}
	}

	rv.Status.DatameshReplicaRequests = []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
		mkLeaveRequest(memberName),
	}

	runUntilStable(rv, rsp, *rvrs, FeatureFlags{})

	// Clear requests after completion.
	rv.Status.DatameshReplicaRequests = nil
}

// assertQuorum checks q and qmr invariants.
func assertQuorum(rv *v1alpha1.ReplicatedVolume, voters int, expectedQMR byte, context string) {
	ExpectWithOffset(1, rv.Status.Datamesh.Quorum).To(
		Equal(expectedQ(voters)),
		"%s: q should be %d for %d voters", context, expectedQ(voters), voters)
	ExpectWithOffset(1, rv.Status.Datamesh.QuorumMinimumRedundancy).To(
		Equal(expectedQMR),
		"%s: qmr should be %d", context, expectedQMR)
	ExpectWithOffset(1, rv.Status.DatameshTransitions).To(BeEmpty(),
		"%s: no active transitions expected", context)
}

// ──────────────────────────────────────────────────────────────────────────────
// Tests
//

var _ = DescribeTable("integration: quorum invariants",
	func(e layoutEntry) {
		rv, rsp, rvrs := setupLayout(e)

		// Verify initial state.
		assertQuorum(rv, e.initD, e.initQMR, "initial")

		// ── Step 1: try remove D at minimum → blocked ──────────────────
		{
			lastD := fmt.Sprintf("rv-1-%d", e.initD-1)
			// Make all D RVRs UpToDate so only FTT guard blocks (not GMDR).
			for _, rvr := range rvrs {
				for _, m := range rv.Status.Datamesh.Members {
					if m.Name == rvr.Name && m.Type.HasBackingVolume() {
						rvr.Status.BackingVolume = &v1alpha1.ReplicatedVolumeReplicaStatusBackingVolume{
							State: v1alpha1.DiskStateUpToDate,
						}
					}
				}
			}
			rv.Status.DatameshReplicaRequests = []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkLeaveRequest(lastD),
			}

			ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, FeatureFlags{})

			// No RemoveReplica transition should be created.
			for _, t := range rv.Status.DatameshTransitions {
				Expect(t.Type).NotTo(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveReplica),
					"removal at minimum should be blocked")
			}
			rv.Status.DatameshReplicaRequests = nil
			rv.Status.DatameshTransitions = nil
		}

		// ── Step 2: add +1, +2, +3 D ──────────────────────────────────
		nextID := e.initD + e.initTB // first available ID after D + TB members
		for i := 1; i <= 3; i++ {
			addD(rv, rsp, &rvrs, nextID)
			nextID++
			voters := e.initD + i
			assertQuorum(rv, voters, e.initQMR,
				fmt.Sprintf("after add +%d (D=%d)", i, voters))
		}

		// ── Step 3: remove -1, -2, -3 D back to canonical ─────────────
		for i := 1; i <= 3; i++ {
			// Remove the last-added D (in reverse order).
			removedID := nextID - i
			removedName := fmt.Sprintf("rv-1-%d", removedID)
			removeD(rv, rsp, &rvrs, removedName)
			voters := e.initD + 3 - i
			assertQuorum(rv, voters, e.initQMR,
				fmt.Sprintf("after remove -%d (D=%d)", i, voters))
		}

		// ── Step 4: try remove D at minimum again → blocked ────────────
		{
			lastD := fmt.Sprintf("rv-1-%d", e.initD-1)
			rv.Status.DatameshReplicaRequests = []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkLeaveRequest(lastD),
			}

			ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, FeatureFlags{})

			for _, t := range rv.Status.DatameshTransitions {
				Expect(t.Type).NotTo(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveReplica),
					"removal at minimum should be blocked (after cycle)")
			}
			rv.Status.DatameshReplicaRequests = nil
			rv.Status.DatameshTransitions = nil
		}
	},
	Entry("1D (FTT=0 GMDR=0)", layoutEntry{ftt: 0, gmdr: 0, initD: 1, initTB: 0, initQ: 1, initQMR: 1}),
	Entry("2D+1TB (FTT=1 GMDR=0)", layoutEntry{ftt: 1, gmdr: 0, initD: 2, initTB: 1, initQ: 2, initQMR: 1}),
	Entry("2D (FTT=0 GMDR=1)", layoutEntry{ftt: 0, gmdr: 1, initD: 2, initTB: 0, initQ: 2, initQMR: 2}),
	Entry("3D (FTT=1 GMDR=1)", layoutEntry{ftt: 1, gmdr: 1, initD: 3, initTB: 0, initQ: 2, initQMR: 2}),
	Entry("4D+1TB (FTT=2 GMDR=1)", layoutEntry{ftt: 2, gmdr: 1, initD: 4, initTB: 1, initQ: 3, initQMR: 2}),
	Entry("4D (FTT=1 GMDR=2)", layoutEntry{ftt: 1, gmdr: 2, initD: 4, initTB: 0, initQ: 3, initQMR: 3}),
	Entry("5D (FTT=2 GMDR=2)", layoutEntry{ftt: 2, gmdr: 2, initD: 5, initTB: 0, initQ: 3, initQMR: 3}),
)
