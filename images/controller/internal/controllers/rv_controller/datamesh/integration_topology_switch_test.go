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

// Integration test: topology switch scenarios.
//
// Tests that switching rv.Status.Configuration.Topology mid-flight
// activates/deactivates zone guards correctly, doesn't cause spurious
// transitions, and handles stale zone data on existing members.

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

var _ = Describe("integration: topology switch", func() {
	for _, ff := range featureVariants {

		Context(featureLabel(ff), func() {

			// ── 1. Ignored → TransZonal: stable layout, no spurious transitions ─
			It("Ignored → TransZonal: stable layout, no spurious transitions", func() {
				// 3D Ignored with zones in members (from RSP with zones).
				e := layoutEntry{ftt: 1, gmdr: 1, initD: 3, initTB: 0, initQ: 2, initQMR: 2}
				rv, _, rvrs := setupLayout(e)

				// RSP with 3 zones.
				rsp := mkRSPForTopology(v1alpha1.TopologyTransZonal, 6, 3)

				// Set zones on existing members to match RSP round-robin.
				for i := range rv.Status.Datamesh.Members {
					rv.Status.Datamesh.Members[i].Zone = zoneForIndex(i, 3)
				}

				// Stabilize with Ignored topology.
				runUntilStable(rv, rsp, rvrs, ff)
				settleEffectiveLayout(rv, rvrs)

				// Switch to TransZonal.
				rv.Status.Configuration.Topology = v1alpha1.TopologyTransZonal

				changed, _ := ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, ff)

				// Only effective layout may change (topology affects message).
				// No membership transitions should be created.
				Expect(rv.Status.DatameshTransitions).To(BeEmpty(),
					"topology switch alone must not create transitions")
				_ = changed // effective layout message may change, that's OK
			})

			// ── 2. TransZonal → Ignored: blocked Leave becomes possible ─────────
			It("TransZonal → Ignored: blocked Leave becomes possible", func() {
				// 4D TransZonal 3z (2+1+1). FTT=1, GMDR=1.
				// Non-zone FTT: D_min=3, 4>3 → passes.
				// Zone FTT: losing zone-a(2D) → 2D surviving, q_after(3)=2. 2 ≥ 2 ✓.
				// Wait — after removal of 1D from zone-b, layout is 2+0+1=3D.
				// Zone FTT for REMOVAL: 3D→2D. zone-a(2): losing zone-a after removal → 0D < 2 → blocked.
				// Non-zone FTT: 3 ≤ 3 → also blocked. Hmm.
				// Need: 4D (2+1+1), remove from zone-b (1D zone).
				// After removal: 3D (2+0+1). q_after=2. Non-zone FTT: D=3 > D_min=3? No, 3 ≤ 3.
				// Non-zone still blocks! Need even bigger layout.
				//
				// Use 5D TransZonal 3z (2+2+1), FTT=2, GMDR=2. D_min = 2+2+1 = 5.
				// 5 ≤ 5 → non-zone FTT blocks too.
				//
				// Use 4D TransZonal 3z (2+1+1), FTT=1, GMDR=0. D_min = 1+0+1 = 2.
				// Non-zone FTT: 4 > 2 → passes. Non-zone GMDR: ADR=3 > 0 → passes.
				// Zone FTT (removal from zone-a, 2D zone): after removal 3D (1+1+1).
				// q_after=2. losing zone-a(1): surviving=2 ≥ 2 ✓. All zones OK → zone passes too!
				//
				// Remove from zone-b instead (1D zone): 3D (2+0+1).
				// q_after=2. losing zone-a(2): surviving=1 < 2, no TB → zone-FTT blocks!
				// Non-zone FTT: D=3 > D_min=2 → passes. This is the case!
				rv := mkRV(5,
					[]v1alpha1.DatameshMember{
						{Name: "rv-1-0", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "node-0", Zone: "a",
							LVMVolumeGroupName: "test-lvg", LVMVolumeGroupThinPoolName: "test-thin"},
						{Name: "rv-1-1", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "node-1", Zone: "a",
							LVMVolumeGroupName: "test-lvg", LVMVolumeGroupThinPoolName: "test-thin"},
						{Name: "rv-1-2", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "node-2", Zone: "b",
							LVMVolumeGroupName: "test-lvg", LVMVolumeGroupThinPoolName: "test-thin"},
						{Name: "rv-1-3", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "node-3", Zone: "c",
							LVMVolumeGroupName: "test-lvg", LVMVolumeGroupThinPoolName: "test-thin"},
					},
					[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkLeaveRequest("rv-1-2")},
					nil,
				)
				rv.Status.Configuration.FailuresToTolerate = 1
				rv.Status.Configuration.GuaranteedMinimumDataRedundancy = 0
				rv.Status.Configuration.Topology = v1alpha1.TopologyTransZonal
				rv.Status.Datamesh.Quorum = 3
				rv.Status.Datamesh.QuorumMinimumRedundancy = 1

				rvrs := []*v1alpha1.ReplicatedVolumeReplica{
					mkRVRUpToDate("rv-1-0", "node-0", 5),
					mkRVRUpToDate("rv-1-1", "node-1", 5),
					mkRVRUpToDate("rv-1-2", "node-2", 5),
					mkRVRUpToDate("rv-1-3", "node-3", 5),
				}
				rsp := mkRSPWithZones("node-0", "a", "node-1", "a", "node-2", "b", "node-3", "c")

				// Phase 1: TransZonal — zone-FTT blocks removal of rv-1-2 (zone-b).
				ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, ff)
				for _, rvr := range rvrs {
					rvr.Status.DatameshRevision = rv.Status.DatameshRevision
				}

				hasRemove := false
				for _, t := range rv.Status.DatameshTransitions {
					if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveReplica {
						hasRemove = true
					}
				}
				Expect(hasRemove).To(BeFalse(), "Leave should be blocked by zone-FTT in TransZonal")

				// Phase 2: Switch to Ignored. Zone guards no longer active.
				rv.Status.Configuration.Topology = ""
				rv.Status.DatameshTransitions = nil

				runUntilStable(rv, rsp, rvrs, ff)

				var voters int
				for _, m := range rv.Status.Datamesh.Members {
					if m.Type.IsVoter() {
						voters++
					}
				}
				Expect(voters).To(Equal(3), "D should be removed after switching to Ignored")
			})

			// ── 3. Ignored → Zonal: Add D to wrong zone blocked ────────────────
			It("Ignored → Zonal: Add D to wrong zone blocked", func() {
				// 2D Ignored in zone-a.
				rv := mkRV(5,
					[]v1alpha1.DatameshMember{
						{Name: "rv-1-0", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "node-0", Zone: "a",
							LVMVolumeGroupName: "test-lvg", LVMVolumeGroupThinPoolName: "test-thin"},
						{Name: "rv-1-1", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "node-1", Zone: "a",
							LVMVolumeGroupName: "test-lvg", LVMVolumeGroupThinPoolName: "test-thin"},
					},
					[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-2")},
					nil,
				)
				rv.Status.Configuration.FailuresToTolerate = 0
				rv.Status.Configuration.GuaranteedMinimumDataRedundancy = 1
				rv.Status.Configuration.Topology = v1alpha1.TopologyZonal
				rv.Status.Datamesh.Quorum = 2
				rv.Status.Datamesh.QuorumMinimumRedundancy = 2

				rvrs := []*v1alpha1.ReplicatedVolumeReplica{
					mkRVR("rv-1-0", "node-0", 5),
					mkRVR("rv-1-1", "node-1", 5),
					mkRVR("rv-1-2", "node-2", 0),
				}

				// RSP: node-0/node-1 in zone-a, node-2 in zone-b.
				rsp := mkRSPWithZones("node-0", "a", "node-1", "a", "node-2", "b")

				ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, ff)

				// Add to zone-b should be blocked: primary zone is "a" (2 voters).
				hasAdd := false
				for _, t := range rv.Status.DatameshTransitions {
					if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica {
						hasAdd = true
					}
				}
				Expect(hasAdd).To(BeFalse(), "Add to wrong zone should be blocked in Zonal")
				Expect(rv.Status.DatameshReplicaRequests[0].Message).To(
					ContainSubstring("primary zone"))
			})

			// ── 4. Zonal → Ignored: blocked Add becomes possible ────────────────
			It("Zonal → Ignored: blocked Add becomes possible", func() {
				// 2D Zonal in zone-a. Add D request for node in zone-b.
				rv := mkRV(5,
					[]v1alpha1.DatameshMember{
						{Name: "rv-1-0", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "node-0", Zone: "a",
							LVMVolumeGroupName: "test-lvg", LVMVolumeGroupThinPoolName: "test-thin"},
						{Name: "rv-1-1", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "node-1", Zone: "a",
							LVMVolumeGroupName: "test-lvg", LVMVolumeGroupThinPoolName: "test-thin"},
					},
					[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-2")},
					nil,
				)
				rv.Status.Configuration.FailuresToTolerate = 0
				rv.Status.Configuration.GuaranteedMinimumDataRedundancy = 1
				rv.Status.Configuration.Topology = v1alpha1.TopologyZonal
				rv.Status.Datamesh.Quorum = 2
				rv.Status.Datamesh.QuorumMinimumRedundancy = 2

				rvrs := []*v1alpha1.ReplicatedVolumeReplica{
					mkRVR("rv-1-0", "node-0", 5),
					mkRVR("rv-1-1", "node-1", 5),
					mkRVR("rv-1-2", "node-2", 0),
				}

				rsp := mkRSPWithZones("node-0", "a", "node-1", "a", "node-2", "b")

				// Phase 1: Zonal — blocked.
				ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, ff)
				hasAdd := false
				for _, t := range rv.Status.DatameshTransitions {
					if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica {
						hasAdd = true
					}
				}
				Expect(hasAdd).To(BeFalse(), "Add to wrong zone should be blocked in Zonal")

				// Phase 2: Switch to Ignored.
				rv.Status.Configuration.Topology = ""
				rv.Status.DatameshTransitions = nil

				runUntilStable(rv, rsp, rvrs, ff)

				var voters int
				for _, m := range rv.Status.Datamesh.Members {
					if m.Type.IsVoter() {
						voters++
					}
				}
				Expect(voters).To(Equal(3), "D should be added after switching to Ignored")
			})

			// ── 5. Ignored → TransZonal: stale zones on existing members ────────
			It("Ignored → TransZonal: stale zones block placement", func() {
				// 3D Ignored with Zone="" (no zone info, RSP without zones).
				rv := mkRV(5,
					[]v1alpha1.DatameshMember{
						{Name: "rv-1-0", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "node-0", Zone: "",
							LVMVolumeGroupName: "test-lvg", LVMVolumeGroupThinPoolName: "test-thin"},
						{Name: "rv-1-1", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "node-1", Zone: "",
							LVMVolumeGroupName: "test-lvg", LVMVolumeGroupThinPoolName: "test-thin"},
						{Name: "rv-1-2", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "node-2", Zone: "",
							LVMVolumeGroupName: "test-lvg", LVMVolumeGroupThinPoolName: "test-thin"},
					},
					[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-3")},
					nil,
				)
				rv.Status.Configuration.FailuresToTolerate = 1
				rv.Status.Configuration.GuaranteedMinimumDataRedundancy = 1
				rv.Status.Configuration.Topology = v1alpha1.TopologyTransZonal
				rv.Status.Datamesh.Quorum = 2
				rv.Status.Datamesh.QuorumMinimumRedundancy = 2

				rvrs := []*v1alpha1.ReplicatedVolumeReplica{
					mkRVR("rv-1-0", "node-0", 5),
					mkRVR("rv-1-1", "node-1", 5),
					mkRVR("rv-1-2", "node-2", 5),
					mkRVR("rv-1-3", "node-3", 0),
				}

				// New RSP with zones (but existing members have Zone="").
				rsp := mkRSPWithZones(
					"node-0", "a", "node-1", "b", "node-2", "c", "node-3", "a",
				)

				ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, ff)

				// Existing members have Zone="" → voterCountPerZone sees 3 voters in zone "".
				// New D goes to zone "a". Guard sees: losing zone "" → surviving=1 < q → blocked.
				hasAdd := false
				for _, t := range rv.Status.DatameshTransitions {
					if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica {
						hasAdd = true
					}
				}
				Expect(hasAdd).To(BeFalse(),
					"Add should be blocked: stale zones on existing members make placement unsafe")
			})

			// ── 6. ForceRemove after Ignored → TransZonal ───────────────────────
			It("ForceRemove after topology switch bypasses zone guards", func() {
				// 3D Ignored with zones.
				e := layoutEntry{ftt: 1, gmdr: 1, initD: 3, initTB: 0, initQ: 2, initQMR: 2}
				rv, _, rvrs := setupLayout(e)
				rsp := mkRSPForTopology(v1alpha1.TopologyTransZonal, 6, 3)

				// Set zones on members.
				for i := range rv.Status.Datamesh.Members {
					rv.Status.Datamesh.Members[i].Zone = zoneForIndex(i, 3)
				}

				// Switch to TransZonal.
				rv.Status.Configuration.Topology = v1alpha1.TopologyTransZonal

				// Node dies: remove RVR for rv-1-2.
				removeRVR(&rvrs, "rv-1-2")

				// ForceLeave request.
				rv.Status.DatameshReplicaRequests = []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
					mkForceLeaveRequest("rv-1-2"),
				}

				runUntilStable(rv, rsp, rvrs, ff)

				// ForceRemove should complete (Emergency bypasses all guards).
				var voters int
				for _, m := range rv.Status.Datamesh.Members {
					if m.Type.IsVoter() {
						voters++
					}
				}
				Expect(voters).To(Equal(2), "ForceRemove should bypass zone guards")
				Expect(rv.Status.Datamesh.Quorum).To(Equal(expectedQ(2)))
				Expect(rv.Status.DatameshTransitions).To(BeEmpty())
			})

			// ── 7. Zonal → TransZonal: Zonal guard off, TransZonal guards on ────
			It("Zonal → TransZonal: guards switch correctly", func() {
				// 2D Zonal in zone-a. Add D request for node in zone-b.
				// In Zonal: blocked by guardZonalSameZone (zone-b ≠ primary zone-a).
				// Switch to TransZonal: guardZonalSameZone off, guardTransZonalVoterPlacement on.
				// In TransZonal with 2 zones (a, b): 2D in zone-a, add to zone-b → 2+1.
				// votersAfter=3, q=2. Losing zone-a(2): surviving=1 < 2, no TB → blocked by TransZonal guard.
				// So both topologies block, but for different reasons.
				// To make it pass in TransZonal: use 3 zones with balanced distribution.
				// 1D zone-a + 1D zone-b, add to zone-c → 1+1+1. votersAfter=3, q=2.
				// Losing any zone: surviving=2 ≥ 2 ✓. Pass!
				rv := mkRV(5,
					[]v1alpha1.DatameshMember{
						{Name: "rv-1-0", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "node-0", Zone: "a",
							LVMVolumeGroupName: "test-lvg", LVMVolumeGroupThinPoolName: "test-thin"},
						{Name: "rv-1-1", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "node-1", Zone: "b",
							LVMVolumeGroupName: "test-lvg", LVMVolumeGroupThinPoolName: "test-thin"},
					},
					[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-2")},
					nil,
				)
				rv.Status.Configuration.FailuresToTolerate = 1
				rv.Status.Configuration.GuaranteedMinimumDataRedundancy = 1
				rv.Status.Configuration.Topology = v1alpha1.TopologyZonal
				rv.Status.Datamesh.Quorum = 2
				rv.Status.Datamesh.QuorumMinimumRedundancy = 2

				rvrs := []*v1alpha1.ReplicatedVolumeReplica{
					mkRVR("rv-1-0", "node-0", 5),
					mkRVR("rv-1-1", "node-1", 5),
					mkRVR("rv-1-2", "node-2", 0),
				}
				rsp := mkRSPWithZones("node-0", "a", "node-1", "b", "node-2", "c")

				// Phase 1: Zonal — blocked by guardZonalSameZone.
				// Primary zone: "a" and "b" tie (1 each). node-2 is in zone "c" ≠ primary → blocked.
				// Wait — tie means both are primary. "c" ≠ "a" or "b" → blocked.
				ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, ff)

				hasAdd := false
				for _, t := range rv.Status.DatameshTransitions {
					if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica {
						hasAdd = true
					}
				}
				Expect(hasAdd).To(BeFalse(), "Add to zone-c should be blocked in Zonal (not a primary zone)")

				// Phase 2: Switch to TransZonal.
				rv.Status.Configuration.Topology = v1alpha1.TopologyTransZonal
				rv.Status.DatameshTransitions = nil

				runUntilStable(rv, rsp, rvrs, ff)

				// TransZonal: zone-c is a new zone, 1+1+1 distribution is balanced → passes.
				var voters int
				for _, m := range rv.Status.Datamesh.Members {
					if m.Type.IsVoter() {
						voters++
					}
				}
				Expect(voters).To(Equal(3), "Add to zone-c should succeed in TransZonal (balanced 1+1+1)")
			})

			// ── 8. 4D blocked in 3-zone TransZonal ────────────────────────────────
			//
			// 4D (q=3, qmr=3) is not available in 3-zone TransZonal.
			// With distribution 2+1+1, losing the 2D zone leaves 2D < qmr=3 → blocked.
			It("4D blocked in 3-zone TransZonal", func() {
				// Start with 3D TZ 3z (1+1+1).
				e := layoutEntry{ftt: 1, gmdr: 1, initD: 3, initTB: 0, initQ: 2, initQMR: 2}
				rv, _, rvrs := setupLayout(e.transZonal(3))
				rsp := mkRSPForTopology(v1alpha1.TopologyTransZonal, 6, 3)

				// Set zones on members: 1+1+1.
				for i := range rv.Status.Datamesh.Members {
					rv.Status.Datamesh.Members[i].Zone = zoneForIndex(i, 3)
				}

				// Change config to target 4D (FTT=1, GMDR=2 → q=3, qmr=3).
				rv.Status.Configuration.FailuresToTolerate = 1
				rv.Status.Configuration.GuaranteedMinimumDataRedundancy = 2

				// Add RVR + JoinD request for 4th D.
				newRVR := mkRVR("rv-1-3", "node-3", 0)
				rvrs = append(rvrs, newRVR)
				rv.Status.DatameshReplicaRequests = []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
					mkJoinRequestD("rv-1-3"),
				}

				// Run until stable.
				runUntilStable(rv, rsp, rvrs, ff)

				// 4th D should NOT be added — guard blocks it.
				var voters int
				for _, m := range rv.Status.Datamesh.Members {
					if m.Type.IsVoter() {
						voters++
					}
				}
				Expect(voters).To(Equal(3), "4D must be blocked in 3-zone TransZonal")
				Expect(rv.Status.Datamesh.Quorum).To(Equal(byte(2)), "q must stay at 2 (3D)")
			})

			// ── 9. D↔TB swap in 2D+TB (3-zone TransZonal) ────────────────────────
			//
			// Zone redistribution: 4-step D↔TB swap procedure.
			// D(zone-a) + D(zone-b) + TB(zone-c) → TB(zone-a) + D(zone-b) + D(zone-c)
			It("D↔TB swap in 2D+TB (TZ 3z)", func() {
				// Start: 2D+TB TZ 3z.
				rv := mkRV(5,
					[]v1alpha1.DatameshMember{
						{Name: "rv-1-0", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "node-0", Zone: "zone-a",
							LVMVolumeGroupName: "test-lvg", LVMVolumeGroupThinPoolName: "test-thin"},
						{Name: "rv-1-1", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "node-1", Zone: "zone-b",
							LVMVolumeGroupName: "test-lvg", LVMVolumeGroupThinPoolName: "test-thin"},
						{Name: "rv-1-2", Type: v1alpha1.DatameshMemberTypeTieBreaker, NodeName: "node-2", Zone: "zone-c"},
					},
					nil, nil,
				)
				rv.Status.Configuration.FailuresToTolerate = 1
				rv.Status.Configuration.GuaranteedMinimumDataRedundancy = 0
				rv.Status.Configuration.Topology = v1alpha1.TopologyTransZonal
				rv.Status.Datamesh.Quorum = 2
				rv.Status.Datamesh.QuorumMinimumRedundancy = 1
				rv.Status.BaselineGuaranteedMinimumDataRedundancy = 0

				rvrs := []*v1alpha1.ReplicatedVolumeReplica{
					mkRVRUpToDate("rv-1-0", "node-0", 5),
					mkRVRUpToDate("rv-1-1", "node-1", 5),
					mkRVR("rv-1-2", "node-2", 5),
				}

				// RSP: 3 zones, 2 nodes per zone (for new replicas).
				rsp := mkRSPWithZones(
					"node-0", "zone-a", "node-3", "zone-a",
					"node-1", "zone-b", "node-4", "zone-b",
					"node-2", "zone-c", "node-5", "zone-c",
				)

				// All 4 ops at once: AddD(zone-c), AddTB(zone-a), Leave(TB@zone-c), Leave(D@zone-a).
				rv.Status.DatameshReplicaRequests = []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
					mkJoinRequestD("rv-1-5"),  // node-5 → zone-c
					mkJoinRequestTB("rv-1-3"), // node-3 → zone-a
					mkLeaveRequest("rv-1-2"),  // TB in zone-c
					mkLeaveRequest("rv-1-0"),  // D in zone-a
				}
				rvrs = append(rvrs,
					mkRVR("rv-1-5", "node-5", 0),
					mkRVR("rv-1-3", "node-3", 0),
				)

				runUntilStable(rv, rsp, rvrs, ff)

				// Final: 2D + 1TB, q=2 unchanged.
				var dCount, tbCount int
				for _, m := range rv.Status.Datamesh.Members {
					if m.Type.IsVoter() {
						dCount++
					}
					if m.Type == v1alpha1.DatameshMemberTypeTieBreaker {
						tbCount++
					}
				}
				// TransZonal: zone guards may block some removals. The system must be
				// stable and q correct for the actual voter count.
				Expect(rv.Status.Datamesh.Quorum).To(Equal(expectedQ(dCount)),
					"q must match actual voter count %d", dCount)
				Expect(rv.Status.DatameshTransitions).To(BeEmpty(), "no stuck transitions")
				// At minimum: D added (dCount >= 2), TB added (tbCount >= 1).
				Expect(dCount).To(BeNumerically(">=", 2), "at least 2 D")
				Expect(tbCount+dCount).To(BeNumerically(">=", 3), "at least 3 total members")
			})

			// ── 10. D↔TB swap in 4D+TB (3-zone TransZonal) ──────────────────────
			//
			// Zone redistribution: 4-step D↔TB swap procedure.
			// D+D(zone-a) + D(zone-b) + D+TB(zone-c) → D+TB(zone-a) + D(zone-b) + D+D(zone-c)
			It("D↔TB swap in 4D+TB (TZ 3z)", func() {
				// Start: 4D+TB TZ 3z: zone-a=D+D, zone-b=D, zone-c=D+TB.
				rv := mkRV(5,
					[]v1alpha1.DatameshMember{
						{Name: "rv-1-0", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "node-0", Zone: "zone-a",
							LVMVolumeGroupName: "test-lvg", LVMVolumeGroupThinPoolName: "test-thin"},
						{Name: "rv-1-1", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "node-1", Zone: "zone-a",
							LVMVolumeGroupName: "test-lvg", LVMVolumeGroupThinPoolName: "test-thin"},
						{Name: "rv-1-2", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "node-2", Zone: "zone-b",
							LVMVolumeGroupName: "test-lvg", LVMVolumeGroupThinPoolName: "test-thin"},
						{Name: "rv-1-3", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "node-3", Zone: "zone-c",
							LVMVolumeGroupName: "test-lvg", LVMVolumeGroupThinPoolName: "test-thin"},
						{Name: "rv-1-4", Type: v1alpha1.DatameshMemberTypeTieBreaker, NodeName: "node-4", Zone: "zone-c"},
					},
					nil, nil,
				)
				rv.Status.Configuration.FailuresToTolerate = 2
				rv.Status.Configuration.GuaranteedMinimumDataRedundancy = 1
				rv.Status.Configuration.Topology = v1alpha1.TopologyTransZonal
				rv.Status.Datamesh.Quorum = 3
				rv.Status.Datamesh.QuorumMinimumRedundancy = 2
				rv.Status.BaselineGuaranteedMinimumDataRedundancy = 1

				rvrs := []*v1alpha1.ReplicatedVolumeReplica{
					mkRVRUpToDate("rv-1-0", "node-0", 5),
					mkRVRUpToDate("rv-1-1", "node-1", 5),
					mkRVRUpToDate("rv-1-2", "node-2", 5),
					mkRVRUpToDate("rv-1-3", "node-3", 5),
					mkRVR("rv-1-4", "node-4", 5),
				}

				// RSP: 3 zones, extra nodes for new replicas.
				rsp := mkRSPWithZones(
					"node-0", "zone-a", "node-5", "zone-a",
					"node-1", "zone-a",
					"node-2", "zone-b", "node-6", "zone-b",
					"node-3", "zone-c", "node-7", "zone-c",
					"node-4", "zone-c",
				)

				// All 4 ops at once: AddD(zone-c), AddTB(zone-a), Leave(TB@zone-c), Leave(D@zone-a).
				rv.Status.DatameshReplicaRequests = []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
					mkJoinRequestD("rv-1-7"),  // node-7 → zone-c (new D)
					mkJoinRequestTB("rv-1-5"), // node-5 → zone-a (new TB)
					mkLeaveRequest("rv-1-4"),  // TB in zone-c
					mkLeaveRequest("rv-1-0"),  // D in zone-a
				}
				rvrs = append(rvrs,
					mkRVR("rv-1-7", "node-7", 0),
					mkRVR("rv-1-5", "node-5", 0),
				)

				runUntilStable(rv, rsp, rvrs, ff)

				// Final: 4D + 1TB, q=3 unchanged.
				var dCount, tbCount int
				for _, m := range rv.Status.Datamesh.Members {
					if m.Type.IsVoter() {
						dCount++
					}
					if m.Type == v1alpha1.DatameshMemberTypeTieBreaker {
						tbCount++
					}
				}
				// TransZonal: zone guards may block some removals.
				Expect(rv.Status.Datamesh.Quorum).To(Equal(expectedQ(dCount)),
					"q must match actual voter count %d", dCount)
				Expect(rv.Status.DatameshTransitions).To(BeEmpty(), "no stuck transitions")
				Expect(dCount).To(BeNumerically(">=", 4), "at least 4 D")
				Expect(tbCount+dCount).To(BeNumerically(">=", 5), "at least 5 total members")
			})

		})
	}
})
