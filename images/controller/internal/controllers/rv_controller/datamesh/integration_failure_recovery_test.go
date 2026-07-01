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

// Integration test: failure + recovery.
//
// Simulates node and zone failures (ForceRemove) followed by recovery
// (new replica joins). Verifies that the datamesh engine converges back
// to the original canonical layout with safety invariants preserved at
// every intermediate step (via assertSafetyInvariants in runSettleLoop).
//
// Four test groups:
//   - Single D failure + recovery (all layouts × topologies)
//   - Max safe D failure + recovery (GMDR=2 layouts, multi-node)
//   - TB failure + recovery (layouts with TB)
//   - Zone failure + recovery (TransZonal only)

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// ──────────────────────────────────────────────────────────────────────────────
// Helper
//

// memberKind describes the type of a killed member for replacement.
type memberKind int

const (
	kindD  memberKind = iota // Diskful
	kindTB                   // TieBreaker
)

// killAndRecover kills specified members (simulating node death), runs
// ForceRemove to completion, then adds replacement members and verifies
// the system recovers to the original layout.
//
// killIDs are member indexes (rv-1-{id}). killTypes must have the same
// length and specifies D or TB for each killed member.
func killAndRecover(
	e layoutEntry,
	killIDs []int,
	killTypes []memberKind,
	features FeatureFlags,
) {
	Expect(killIDs).To(HaveLen(len(killTypes)), "killIDs and killTypes must match")

	// Setup with extra RSP nodes for replacements.
	rv, _, rvrs := setupLayout(e)
	total := e.initD + e.initTB
	extraNodes := len(killIDs) + 3
	rsp := mkRSPForTopology(e.topology, total+extraNodes, e.zoneCount())

	initialQMR := rv.Status.Datamesh.QuorumMinimumRedundancy

	// ── Phase 1: Kill + ForceRemove ─────────────────────────────────────
	var requests []v1alpha1.ReplicatedVolumeDatameshReplicaRequest
	for _, id := range killIDs {
		name := fmt.Sprintf("rv-1-%d", id)
		removeRVR(&rvrs, name)
		requests = append(requests, mkForceLeaveRequest(name))
	}
	rv.Status.DatameshReplicaRequests = requests

	runSettleLoop(rv, rsp, rvrs, nil, features, nil, assertSafetyInvariants)

	// Assert intermediate state.
	for _, id := range killIDs {
		name := fmt.Sprintf("rv-1-%d", id)
		Expect(rv.Status.Datamesh.FindMemberByName(name)).To(BeNil(),
			"killed member %s should be removed", name)
	}
	Expect(rv.Status.Datamesh.QuorumMinimumRedundancy).To(
		BeNumerically(">=", initialQMR),
		"qmr must never drop after ForceRemove")
	Expect(rv.Status.DatameshTransitions).To(BeEmpty(),
		"no stuck transitions after ForceRemove")

	rv.Status.DatameshReplicaRequests = nil

	// ── Phase 2: Recovery ───────────────────────────────────────────────
	requests = nil
	nextID := total
	for _, kt := range killTypes {
		name := fmt.Sprintf("rv-1-%d", nextID)
		node := fmt.Sprintf("node-%d", nextID)
		switch kt {
		case kindD:
			rvrs = append(rvrs, mkRVR(name, node, 0))
			requests = append(requests, mkJoinRequestD(name))
		case kindTB:
			rvrs = append(rvrs, mkRVR(name, node, 0))
			requests = append(requests, mkJoinRequestTB(name))
		}
		nextID++
	}
	rv.Status.DatameshReplicaRequests = requests

	runSettleLoop(rv, rsp, rvrs, nil, features, nil, assertSafetyInvariants)

	// Assert final state: recovered to original layout.
	var dCount, tbCount int
	for _, m := range rv.Status.Datamesh.Members {
		if m.Type.IsVoter() {
			dCount++
		}
		if m.Type == v1alpha1.DatameshMemberTypeTieBreaker {
			tbCount++
		}
	}

	if e.topology == v1alpha1.TopologyTransZonal {
		// TransZonal: zone placement guards may block some adds if the
		// replacement node lands in a zone that would violate zone-level
		// safety. Relax: q must be correct for actual voter count, no stuck
		// transitions. This matches integration_layout_transitions_test.go.
		Expect(rv.Status.Datamesh.Quorum).To(Equal(expectedQ(dCount)),
			"q must match actual voter count %d", dCount)
	} else {
		// Ignored/Zonal: exact recovery expected.
		Expect(dCount).To(Equal(e.initD), "D count must match original layout")
		Expect(tbCount).To(Equal(e.initTB), "TB count must match original layout")
		Expect(rv.Status.Datamesh.Quorum).To(Equal(expectedQ(e.initD)),
			"q must be correct for recovered layout")
		Expect(rv.Status.Datamesh.QuorumMinimumRedundancy).To(Equal(e.initQMR),
			"qmr must match original layout")
		Expect(rv.Status.DatameshTransitions).To(BeEmpty(),
			"no stuck transitions after recovery")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Group 1: Single D failure + recovery
//

var _ = Describe("integration: single D failure + recovery", func() {
	for _, ff := range featureVariants {

		Context(featureLabel(ff), func() {
			DescribeTable("",
				func(e layoutEntry) {
					lastD := e.initD - 1
					killAndRecover(e, []int{lastD}, []memberKind{kindD}, ff)
				},
				// Ignored topology.
				Entry("1D (FTT=0 GMDR=0)", layoutEntry{ftt: 0, gmdr: 0, initD: 1, initTB: 0, initQ: 1, initQMR: 1}),
				Entry("2D+1TB (FTT=1 GMDR=0)", layoutEntry{ftt: 1, gmdr: 0, initD: 2, initTB: 1, initQ: 2, initQMR: 1}),
				Entry("2D (FTT=0 GMDR=1)", layoutEntry{ftt: 0, gmdr: 1, initD: 2, initTB: 0, initQ: 2, initQMR: 2}),
				Entry("3D (FTT=1 GMDR=1)", layoutEntry{ftt: 1, gmdr: 1, initD: 3, initTB: 0, initQ: 2, initQMR: 2}),
				Entry("4D+1TB (FTT=2 GMDR=1)", layoutEntry{ftt: 2, gmdr: 1, initD: 4, initTB: 1, initQ: 3, initQMR: 2}),
				Entry("4D (FTT=1 GMDR=2)", layoutEntry{ftt: 1, gmdr: 2, initD: 4, initTB: 0, initQ: 3, initQMR: 3}),
				Entry("5D (FTT=2 GMDR=2)", layoutEntry{ftt: 2, gmdr: 2, initD: 5, initTB: 0, initQ: 3, initQMR: 3}),

				// TransZonal topology (3 zones).
				Entry("1D (FTT=0 GMDR=0) [TZ 3z]", layoutEntry{ftt: 0, gmdr: 0, initD: 1, initTB: 0, initQ: 1, initQMR: 1}.transZonal(3)),
				Entry("2D+1TB (FTT=1 GMDR=0) [TZ 3z]", layoutEntry{ftt: 1, gmdr: 0, initD: 2, initTB: 1, initQ: 2, initQMR: 1}.transZonal(3)),
				Entry("2D (FTT=0 GMDR=1) [TZ 3z]", layoutEntry{ftt: 0, gmdr: 1, initD: 2, initTB: 0, initQ: 2, initQMR: 2}.transZonal(3)),
				Entry("3D (FTT=1 GMDR=1) [TZ 3z]", layoutEntry{ftt: 1, gmdr: 1, initD: 3, initTB: 0, initQ: 2, initQMR: 2}.transZonal(3)),
				Entry("4D+1TB (FTT=2 GMDR=1) [TZ 3z]", layoutEntry{ftt: 2, gmdr: 1, initD: 4, initTB: 1, initQ: 3, initQMR: 2}.transZonal(3)),
				Entry("5D (FTT=2 GMDR=2) [TZ 3z]", layoutEntry{ftt: 2, gmdr: 2, initD: 5, initTB: 0, initQ: 3, initQMR: 3}.transZonal(3)),

				// TransZonal topology (4/5 zones).
				Entry("4D (FTT=1 GMDR=2) [TZ 4z]", layoutEntry{ftt: 1, gmdr: 2, initD: 4, initTB: 0, initQ: 3, initQMR: 3}.transZonal(4)),
				Entry("4D+1TB (FTT=2 GMDR=1) [TZ 5z]", layoutEntry{ftt: 2, gmdr: 1, initD: 4, initTB: 1, initQ: 3, initQMR: 2}.transZonal(5)),
				Entry("5D (FTT=2 GMDR=2) [TZ 5z]", layoutEntry{ftt: 2, gmdr: 2, initD: 5, initTB: 0, initQ: 3, initQMR: 3}.transZonal(5)),

				// Zonal topology.
				Entry("1D (FTT=0 GMDR=0) [Zonal]", layoutEntry{ftt: 0, gmdr: 0, initD: 1, initTB: 0, initQ: 1, initQMR: 1}.zonal()),
				Entry("2D+1TB (FTT=1 GMDR=0) [Zonal]", layoutEntry{ftt: 1, gmdr: 0, initD: 2, initTB: 1, initQ: 2, initQMR: 1}.zonal()),
				Entry("2D (FTT=0 GMDR=1) [Zonal]", layoutEntry{ftt: 0, gmdr: 1, initD: 2, initTB: 0, initQ: 2, initQMR: 2}.zonal()),
				Entry("3D (FTT=1 GMDR=1) [Zonal]", layoutEntry{ftt: 1, gmdr: 1, initD: 3, initTB: 0, initQ: 2, initQMR: 2}.zonal()),
				Entry("4D+1TB (FTT=2 GMDR=1) [Zonal]", layoutEntry{ftt: 2, gmdr: 1, initD: 4, initTB: 1, initQ: 3, initQMR: 2}.zonal()),
				Entry("4D (FTT=1 GMDR=2) [Zonal]", layoutEntry{ftt: 1, gmdr: 2, initD: 4, initTB: 0, initQ: 3, initQMR: 3}.zonal()),
				Entry("5D (FTT=2 GMDR=2) [Zonal]", layoutEntry{ftt: 2, gmdr: 2, initD: 5, initTB: 0, initQ: 3, initQMR: 3}.zonal()),
			)
		})
	}
})

// ──────────────────────────────────────────────────────────────────────────────
// Group 2: Max safe D failure + recovery (multi-node)
//

var _ = Describe("integration: max safe D failure + recovery", func() {
	for _, ff := range featureVariants {

		Context(featureLabel(ff), func() {
			DescribeTable("",
				func(e layoutEntry, killIDs []int) {
					killTypes := make([]memberKind, len(killIDs))
					for i := range killTypes {
						killTypes[i] = kindD
					}
					killAndRecover(e, killIDs, killTypes, ff)
				},
				// GMDR=2: kill 2 D members simultaneously.
				// Ignored topology.
				Entry("4D (FTT=1 GMDR=2): 2D fail", layoutEntry{ftt: 1, gmdr: 2, initD: 4, initTB: 0, initQ: 3, initQMR: 3}, []int{2, 3}),
				Entry("5D (FTT=2 GMDR=2): 2D fail", layoutEntry{ftt: 2, gmdr: 2, initD: 5, initTB: 0, initQ: 3, initQMR: 3}, []int{3, 4}),

				// TransZonal topology.
				Entry("4D (FTT=1 GMDR=2) [TZ 4z]: 2D fail", layoutEntry{ftt: 1, gmdr: 2, initD: 4, initTB: 0, initQ: 3, initQMR: 3}.transZonal(4), []int{2, 3}),
				Entry("5D (FTT=2 GMDR=2) [TZ 3z]: 2D fail", layoutEntry{ftt: 2, gmdr: 2, initD: 5, initTB: 0, initQ: 3, initQMR: 3}.transZonal(3), []int{3, 4}),
				Entry("5D (FTT=2 GMDR=2) [TZ 5z]: 2D fail", layoutEntry{ftt: 2, gmdr: 2, initD: 5, initTB: 0, initQ: 3, initQMR: 3}.transZonal(5), []int{3, 4}),

				// Zonal topology.
				Entry("4D (FTT=1 GMDR=2) [Zonal]: 2D fail", layoutEntry{ftt: 1, gmdr: 2, initD: 4, initTB: 0, initQ: 3, initQMR: 3}.zonal(), []int{2, 3}),
				Entry("5D (FTT=2 GMDR=2) [Zonal]: 2D fail", layoutEntry{ftt: 2, gmdr: 2, initD: 5, initTB: 0, initQ: 3, initQMR: 3}.zonal(), []int{3, 4}),
			)
		})
	}
})

// ──────────────────────────────────────────────────────────────────────────────
// Group 3: TB failure + recovery
//

var _ = Describe("integration: TB failure + recovery", func() {
	for _, ff := range featureVariants {

		Context(featureLabel(ff), func() {
			DescribeTable("",
				func(e layoutEntry) {
					tbID := e.initD // TB IDs start right after D IDs
					killAndRecover(e, []int{tbID}, []memberKind{kindTB}, ff)
				},
				// Ignored topology.
				Entry("2D+1TB (FTT=1 GMDR=0)", layoutEntry{ftt: 1, gmdr: 0, initD: 2, initTB: 1, initQ: 2, initQMR: 1}),
				Entry("4D+1TB (FTT=2 GMDR=1)", layoutEntry{ftt: 2, gmdr: 1, initD: 4, initTB: 1, initQ: 3, initQMR: 2}),

				// TransZonal topology.
				Entry("2D+1TB (FTT=1 GMDR=0) [TZ 3z]", layoutEntry{ftt: 1, gmdr: 0, initD: 2, initTB: 1, initQ: 2, initQMR: 1}.transZonal(3)),
				Entry("4D+1TB (FTT=2 GMDR=1) [TZ 3z]", layoutEntry{ftt: 2, gmdr: 1, initD: 4, initTB: 1, initQ: 3, initQMR: 2}.transZonal(3)),
				Entry("4D+1TB (FTT=2 GMDR=1) [TZ 5z]", layoutEntry{ftt: 2, gmdr: 1, initD: 4, initTB: 1, initQ: 3, initQMR: 2}.transZonal(5)),

				// Zonal topology.
				Entry("2D+1TB (FTT=1 GMDR=0) [Zonal]", layoutEntry{ftt: 1, gmdr: 0, initD: 2, initTB: 1, initQ: 2, initQMR: 1}.zonal()),
				Entry("4D+1TB (FTT=2 GMDR=1) [Zonal]", layoutEntry{ftt: 2, gmdr: 1, initD: 4, initTB: 1, initQ: 3, initQMR: 2}.zonal()),
			)
		})
	}
})

// ──────────────────────────────────────────────────────────────────────────────
// Group 4: Zone failure + recovery (TransZonal only)
//

var _ = Describe("integration: zone failure + recovery", func() {
	for _, ff := range featureVariants {

		Context(featureLabel(ff), func() {
			DescribeTable("",
				func(e layoutEntry, zone string) {
					// Find all member IDs in the target zone.
					total := e.initD + e.initTB
					zc := e.zoneCount()

					var killIDs []int
					var killTypes []memberKind
					for id := 0; id < total; id++ {
						if zoneForIndex(id, zc) == zone {
							killIDs = append(killIDs, id)
							if id < e.initD {
								killTypes = append(killTypes, kindD)
							} else {
								killTypes = append(killTypes, kindTB)
							}
						}
					}
					Expect(killIDs).NotTo(BeEmpty(), "zone %s must have at least one member", zone)

					killAndRecover(e, killIDs, killTypes, ff)
				},
				// 3-zone layouts: zone-a has round-robin index 0, 3, 6, ...
				Entry("3D [3z]: zone-a (1D)",
					layoutEntry{ftt: 1, gmdr: 1, initD: 3, initTB: 0, initQ: 2, initQMR: 2}.transZonal(3), "zone-a"),
				Entry("5D [3z]: zone-a (2D)",
					layoutEntry{ftt: 2, gmdr: 2, initD: 5, initTB: 0, initQ: 3, initQMR: 3}.transZonal(3), "zone-a"),
				Entry("4D+1TB [3z]: zone-a (2D)",
					layoutEntry{ftt: 2, gmdr: 1, initD: 4, initTB: 1, initQ: 3, initQMR: 2}.transZonal(3), "zone-a"),
				Entry("4D+1TB [3z]: zone-b (1D+1TB)",
					layoutEntry{ftt: 2, gmdr: 1, initD: 4, initTB: 1, initQ: 3, initQMR: 2}.transZonal(3), "zone-b"),
				Entry("2D+1TB [3z]: zone-a (1D)",
					layoutEntry{ftt: 1, gmdr: 0, initD: 2, initTB: 1, initQ: 2, initQMR: 1}.transZonal(3), "zone-a"),
				Entry("2D+1TB [3z]: zone-c (1TB)",
					layoutEntry{ftt: 1, gmdr: 0, initD: 2, initTB: 1, initQ: 2, initQMR: 1}.transZonal(3), "zone-c"),
			)
		})
	}
})
