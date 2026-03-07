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

// Performance tests for the datamesh engine.
//
// Two flavors:
//
//  1. Go benchmarks (func BenchmarkXxx) — run with `go test -bench=. -benchmem`.
//     Report ns/op, allocs/op, bytes/op for regression tracking.
//
//  2. Ginkgo allocation tests — use testing.AllocsPerRun inside It blocks
//     to assert allocation bounds. Fail if the engine regresses.

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// ──────────────────────────────────────────────────────────────────────────────
// Setup helpers
//

// perfLayout builds a settled layout with N Diskful + M TieBreaker members,
// pre-settled EffectiveLayout, and all RVR revisions matching.
// topology and zoneCount control zone assignment (0 zoneCount = Ignored).
// Returns objects ready for a no-op ProcessTransitions call.
func perfLayout(nD, nTB int, topology v1alpha1.ReplicatedStorageClassTopology, zoneCount int) (
	*v1alpha1.ReplicatedVolume,
	RSP,
	[]*v1alpha1.ReplicatedVolumeReplica,
) {
	total := nD + nTB
	members := make([]v1alpha1.DatameshMember, 0, total)
	rvrs := make([]*v1alpha1.ReplicatedVolumeReplica, 0, total)

	for i := 0; i < nD; i++ {
		name := fmt.Sprintf("rv-1-%d", i)
		node := fmt.Sprintf("node-%d", i)
		m := mkMember(name, v1alpha1.DatameshMemberTypeDiskful, node)
		if topology == v1alpha1.TopologyTransZonal && zoneCount > 0 {
			m.Zone = zoneForIndex(i, zoneCount)
		}
		members = append(members, m)
		rvrs = append(rvrs, mkRVRUpToDate(name, node, 5))
	}
	for i := 0; i < nTB; i++ {
		id := nD + i
		name := fmt.Sprintf("rv-1-%d", id)
		node := fmt.Sprintf("node-%d", id)
		m := mkMember(name, v1alpha1.DatameshMemberTypeTieBreaker, node)
		if topology == v1alpha1.TopologyTransZonal && zoneCount > 0 {
			m.Zone = zoneForIndex(id, zoneCount)
		}
		members = append(members, m)
		rvrs = append(rvrs, mkRVR(name, node, 5))
	}

	q := byte(nD/2 + 1)
	rv := mkRV(5, members, nil, nil)
	rv.Status.Datamesh.Quorum = q
	rv.Status.Datamesh.QuorumMinimumRedundancy = 1
	rv.Status.Configuration.Topology = topology

	rsp := mkRSPForTopology(topology, total, max(zoneCount, 1))

	// Pre-settle EffectiveLayout so ProcessTransitions returns changed=false.
	settleEffectiveLayout(rv, rvrs)

	return rv, rsp, rvrs
}

// ensureRegistry builds the registry if not yet built (for Go benchmarks
// that run outside of Ginkgo BeforeSuite).
func ensureRegistry() {
	if registry == nil {
		BuildRegistry()
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Go benchmarks (run with: go test -bench=. -benchmem -run=^$)
//

func BenchmarkNoOp_1D(b *testing.B) {
	ensureRegistry()
	rv, rsp, rvrs := perfLayout(1, 0, "", 0)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, FeatureFlags{})
	}
}

func BenchmarkNoOp_3D(b *testing.B) {
	ensureRegistry()
	rv, rsp, rvrs := perfLayout(3, 0, "", 0)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, FeatureFlags{})
	}
}

func BenchmarkNoOp_5D(b *testing.B) {
	ensureRegistry()
	rv, rsp, rvrs := perfLayout(5, 0, "", 0)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, FeatureFlags{})
	}
}

func BenchmarkNoOp_4D_1TB(b *testing.B) {
	ensureRegistry()
	rv, rsp, rvrs := perfLayout(4, 1, "", 0)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, FeatureFlags{})
	}
}

func BenchmarkNoOp_8D(b *testing.B) {
	ensureRegistry()
	rv, rsp, rvrs := perfLayout(8, 0, "", 0)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, FeatureFlags{})
	}
}

func BenchmarkNoOp_16D(b *testing.B) {
	ensureRegistry()
	rv, rsp, rvrs := perfLayout(16, 0, "", 0)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, FeatureFlags{})
	}
}

func BenchmarkNoOp_32D(b *testing.B) {
	ensureRegistry()
	rv, rsp, rvrs := perfLayout(32, 0, "", 0)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, FeatureFlags{})
	}
}

func BenchmarkNoOp_5D_sD(b *testing.B) {
	ensureRegistry()
	rv, rsp, rvrs := perfLayout(5, 0, "", 0)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, FeatureFlags{ShadowDiskful: true})
	}
}

func BenchmarkNoOp_5D_TransZonal(b *testing.B) {
	ensureRegistry()
	rv, rsp, rvrs := perfLayout(5, 0, v1alpha1.TopologyTransZonal, 3)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, FeatureFlags{})
	}
}

func BenchmarkNoOp_5D_Zonal(b *testing.B) {
	ensureRegistry()
	rv, rsp, rvrs := perfLayout(5, 0, v1alpha1.TopologyZonal, 1)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, FeatureFlags{})
	}
}

func BenchmarkNoOp_32D_TransZonal(b *testing.B) {
	ensureRegistry()
	rv, rsp, rvrs := perfLayout(32, 0, v1alpha1.TopologyTransZonal, 5)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, FeatureFlags{})
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Ginkgo allocation tests
//

var _ = Describe("performance", func() {
	// noOpAllocs runs ProcessTransitions on a settled layout and returns
	// average allocations per call.
	noOpAllocs := func(nD, nTB int, ff FeatureFlags) float64 {
		rv, rsp, rvrs := perfLayout(nD, nTB, "", 0)
		return testing.AllocsPerRun(100, func() {
			ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, ff)
		})
	}

	// noOpAllocsTopo is like noOpAllocs with explicit topology.
	noOpAllocsTopo := func(nD int, topo v1alpha1.ReplicatedStorageClassTopology, zones int) float64 {
		rv, rsp, rvrs := perfLayout(nD, 0, topo, zones)
		return testing.AllocsPerRun(100, func() {
			ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, FeatureFlags{})
		})
	}

	// ── No-op allocation bounds ─────────────────────────────────────────
	// These thresholds are set generously above observed values.
	// If a refactor causes a regression, the test will catch it.

	// Thresholds: ~2x above observed values (1D=21, 3D=24, 5D=25,
	// 4D+TB=28, 8D=28, 16D=36, 32D=52 allocs/op on Apple M3).
	It("no-op 1D: bounded allocations", func() {
		allocs := noOpAllocs(1, 0, FeatureFlags{})
		Expect(allocs).To(BeNumerically("<", 50),
			"1D no-op: %.0f allocs, expected < 50", allocs)
	})

	It("no-op 3D: bounded allocations", func() {
		allocs := noOpAllocs(3, 0, FeatureFlags{})
		Expect(allocs).To(BeNumerically("<", 55),
			"3D no-op: %.0f allocs, expected < 55", allocs)
	})

	It("no-op 5D: bounded allocations", func() {
		allocs := noOpAllocs(5, 0, FeatureFlags{})
		Expect(allocs).To(BeNumerically("<", 55),
			"5D no-op: %.0f allocs, expected < 55", allocs)
	})

	It("no-op 4D+1TB: bounded allocations", func() {
		allocs := noOpAllocs(4, 1, FeatureFlags{})
		Expect(allocs).To(BeNumerically("<", 60),
			"4D+1TB no-op: %.0f allocs, expected < 60", allocs)
	})

	It("no-op 8D: bounded allocations", func() {
		allocs := noOpAllocs(8, 0, FeatureFlags{})
		Expect(allocs).To(BeNumerically("<", 60),
			"8D no-op: %.0f allocs, expected < 60", allocs)
	})

	It("no-op 16D: bounded allocations", func() {
		allocs := noOpAllocs(16, 0, FeatureFlags{})
		Expect(allocs).To(BeNumerically("<", 80),
			"16D no-op: %.0f allocs, expected < 80", allocs)
	})

	It("no-op 32D: bounded allocations", func() {
		allocs := noOpAllocs(32, 0, FeatureFlags{})
		Expect(allocs).To(BeNumerically("<", 110),
			"32D no-op: %.0f allocs, expected < 110", allocs)
	})

	It("no-op 5D [sD]: same order as default", func() {
		allocsDefault := noOpAllocs(5, 0, FeatureFlags{})
		allocsSD := noOpAllocs(5, 0, FeatureFlags{ShadowDiskful: true})
		// sD flag changes plan selection, not no-op overhead. Should be similar.
		Expect(allocsSD).To(BeNumerically("<", allocsDefault*1.5+10),
			"sD variant should not allocate significantly more: default=%.0f, sD=%.0f", allocsDefault, allocsSD)
	})

	// ── Topology variants ───────────────────────────────────────────────

	It("no-op 5D [Zonal]: bounded allocations", func() {
		allocs := noOpAllocsTopo(5, v1alpha1.TopologyZonal, 1)
		Expect(allocs).To(BeNumerically("<", 55),
			"5D Zonal no-op: %.0f allocs, expected < 55", allocs)
	})

	It("no-op 5D [TZ 3z]: bounded allocations", func() {
		allocs := noOpAllocsTopo(5, v1alpha1.TopologyTransZonal, 3)
		Expect(allocs).To(BeNumerically("<", 55),
			"5D TZ 3z no-op: %.0f allocs, expected < 55", allocs)
	})

	It("no-op 32D [TZ 5z]: bounded allocations", func() {
		allocs := noOpAllocsTopo(32, v1alpha1.TopologyTransZonal, 5)
		Expect(allocs).To(BeNumerically("<", 110),
			"32D TZ 5z no-op: %.0f allocs, expected < 110", allocs)
	})

	It("topology overhead: 5D TZ vs Ignored within 2x", func() {
		allocsIgnored := noOpAllocs(5, 0, FeatureFlags{})
		allocsTZ := noOpAllocsTopo(5, v1alpha1.TopologyTransZonal, 3)
		Expect(allocsTZ).To(BeNumerically("<", allocsIgnored*2+10),
			"TZ overhead: Ignored=%.0f, TZ=%.0f", allocsIgnored, allocsTZ)
	})

	// ── Scaling: 32D should not be dramatically worse than 5D ────────────
	It("scaling: 32D no-op ≤ 5x of 5D no-op", func() {
		allocs5 := noOpAllocs(5, 0, FeatureFlags{})
		allocs32 := noOpAllocs(32, 0, FeatureFlags{})
		ratio := allocs32 / allocs5
		Expect(ratio).To(BeNumerically("<", 5.0),
			"32D/5D alloc ratio: %.1f (32D=%.0f, 5D=%.0f), expected < 5x", ratio, allocs32, allocs5)
	})
})
