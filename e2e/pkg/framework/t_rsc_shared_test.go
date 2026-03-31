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

package framework

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func newTestFramework() *Framework {
	return &Framework{
		runID:  "a1b2c3",
		prefix: "e2e-a1b2c3",
	}
}

// --- rscName ---

var _ = Describe("rscName", func() {
	It("produces deterministic format", func() {
		f := newTestFramework()
		key := rscCacheKey{
			FTT:          0,
			GMDR:         0,
			Topology:     v1alpha1.TopologyIgnored,
			VolumeAccess: v1alpha1.VolumeAccessAny,
			PoolType:     v1alpha1.ReplicatedStoragePoolTypeLVMThin,
		}
		Expect(f.rscName(key)).To(Equal("e2e-a1b2c3-f0g0-ign-any-thn")) //nolint:misspell // "thn" is a 3-char abbreviation
	})

	It("uses the framework prefix", func() {
		f := &Framework{prefix: "e2e-ff0011"}
		key := rscCacheKey{
			FTT: 1, GMDR: 1,
			Topology:     v1alpha1.TopologyTransZonal,
			VolumeAccess: v1alpha1.VolumeAccessLocal,
			PoolType:     v1alpha1.ReplicatedStoragePoolTypeLVM,
		}
		Expect(f.rscName(key)).To(HavePrefix("e2e-ff0011-f"))
	})

	It("different keys produce different names", func() {
		f := newTestFramework()
		k1 := rscCacheKey{FTT: 0, GMDR: 0, Topology: v1alpha1.TopologyIgnored, VolumeAccess: v1alpha1.VolumeAccessAny, PoolType: v1alpha1.ReplicatedStoragePoolTypeLVMThin}
		k2 := rscCacheKey{FTT: 1, GMDR: 0, Topology: v1alpha1.TopologyIgnored, VolumeAccess: v1alpha1.VolumeAccessAny, PoolType: v1alpha1.ReplicatedStoragePoolTypeLVMThin}
		k3 := rscCacheKey{FTT: 0, GMDR: 1, Topology: v1alpha1.TopologyIgnored, VolumeAccess: v1alpha1.VolumeAccessAny, PoolType: v1alpha1.ReplicatedStoragePoolTypeLVMThin}
		k4 := rscCacheKey{FTT: 0, GMDR: 0, Topology: v1alpha1.TopologyZonal, VolumeAccess: v1alpha1.VolumeAccessAny, PoolType: v1alpha1.ReplicatedStoragePoolTypeLVMThin}
		names := []string{f.rscName(k1), f.rscName(k2), f.rscName(k3), f.rscName(k4)}
		for i := 0; i < len(names); i++ {
			for j := i + 1; j < len(names); j++ {
				Expect(names[i]).NotTo(Equal(names[j]),
					"key %d and %d should produce different names", i, j)
			}
		}
	})
})

type rscNameEntry struct {
	key      rscCacheKey
	expected string
}

var _ = DescribeTable("rscName topology/access/pool short strings",
	func(e rscNameEntry) {
		f := newTestFramework()
		Expect(f.rscName(e.key)).To(Equal(e.expected))
	},
	Entry("Ignored/Any/LVMThin", rscNameEntry{
		key:      rscCacheKey{FTT: 0, GMDR: 0, Topology: v1alpha1.TopologyIgnored, VolumeAccess: v1alpha1.VolumeAccessAny, PoolType: v1alpha1.ReplicatedStoragePoolTypeLVMThin},
		expected: "e2e-a1b2c3-f0g0-ign-any-thn", //nolint:misspell // intentional 3-char abbreviation
	}),
	Entry("TransZonal/Local/LVM", rscNameEntry{
		key:      rscCacheKey{FTT: 1, GMDR: 1, Topology: v1alpha1.TopologyTransZonal, VolumeAccess: v1alpha1.VolumeAccessLocal, PoolType: v1alpha1.ReplicatedStoragePoolTypeLVM},
		expected: "e2e-a1b2c3-f1g1-tra-loc-thk",
	}),
	Entry("Zonal/PreferablyLocal/LVMThin", rscNameEntry{
		key:      rscCacheKey{FTT: 0, GMDR: 1, Topology: v1alpha1.TopologyZonal, VolumeAccess: v1alpha1.VolumeAccessPreferablyLocal, PoolType: v1alpha1.ReplicatedStoragePoolTypeLVMThin},
		expected: "e2e-a1b2c3-f0g1-zon-pre-thn", //nolint:misspell // intentional 3-char abbreviation
	}),
	Entry("Ignored/EventuallyLocal/LVM", rscNameEntry{
		key:      rscCacheKey{FTT: 2, GMDR: 1, Topology: v1alpha1.TopologyIgnored, VolumeAccess: v1alpha1.VolumeAccessEventuallyLocal, PoolType: v1alpha1.ReplicatedStoragePoolTypeLVM},
		expected: "e2e-a1b2c3-f2g1-ign-eve-thk",
	}),
	Entry("TransZonal/Any/LVMThin (FTT=2 GMDR=2)", rscNameEntry{
		key:      rscCacheKey{FTT: 2, GMDR: 2, Topology: v1alpha1.TopologyTransZonal, VolumeAccess: v1alpha1.VolumeAccessAny, PoolType: v1alpha1.ReplicatedStoragePoolTypeLVMThin},
		expected: "e2e-a1b2c3-f2g2-tra-any-thn", //nolint:misspell // intentional 3-char abbreviation
	}),
)

// --- rscCacheKey equality ---

var _ = Describe("rscCacheKey", func() {
	It("identical keys are equal", func() {
		k1 := rscCacheKey{FTT: 1, GMDR: 0, Topology: v1alpha1.TopologyZonal, VolumeAccess: v1alpha1.VolumeAccessLocal, PoolType: v1alpha1.ReplicatedStoragePoolTypeLVMThin}
		k2 := rscCacheKey{FTT: 1, GMDR: 0, Topology: v1alpha1.TopologyZonal, VolumeAccess: v1alpha1.VolumeAccessLocal, PoolType: v1alpha1.ReplicatedStoragePoolTypeLVMThin}
		Expect(k1).To(Equal(k2))
	})

	It("different FTT → not equal", func() {
		k1 := rscCacheKey{FTT: 0, GMDR: 0, Topology: v1alpha1.TopologyIgnored, VolumeAccess: v1alpha1.VolumeAccessAny, PoolType: v1alpha1.ReplicatedStoragePoolTypeLVMThin}
		k2 := rscCacheKey{FTT: 1, GMDR: 0, Topology: v1alpha1.TopologyIgnored, VolumeAccess: v1alpha1.VolumeAccessAny, PoolType: v1alpha1.ReplicatedStoragePoolTypeLVMThin}
		Expect(k1).NotTo(Equal(k2))
	})

	It("different GMDR → not equal", func() {
		k1 := rscCacheKey{FTT: 1, GMDR: 0, Topology: v1alpha1.TopologyIgnored, VolumeAccess: v1alpha1.VolumeAccessAny, PoolType: v1alpha1.ReplicatedStoragePoolTypeLVMThin}
		k2 := rscCacheKey{FTT: 1, GMDR: 1, Topology: v1alpha1.TopologyIgnored, VolumeAccess: v1alpha1.VolumeAccessAny, PoolType: v1alpha1.ReplicatedStoragePoolTypeLVMThin}
		Expect(k1).NotTo(Equal(k2))
	})

	It("different Topology → not equal", func() {
		k1 := rscCacheKey{FTT: 1, GMDR: 1, Topology: v1alpha1.TopologyZonal, VolumeAccess: v1alpha1.VolumeAccessAny, PoolType: v1alpha1.ReplicatedStoragePoolTypeLVMThin}
		k2 := rscCacheKey{FTT: 1, GMDR: 1, Topology: v1alpha1.TopologyTransZonal, VolumeAccess: v1alpha1.VolumeAccessAny, PoolType: v1alpha1.ReplicatedStoragePoolTypeLVMThin}
		Expect(k1).NotTo(Equal(k2))
	})

	It("different VolumeAccess → not equal", func() {
		k1 := rscCacheKey{FTT: 0, GMDR: 0, Topology: v1alpha1.TopologyIgnored, VolumeAccess: v1alpha1.VolumeAccessLocal, PoolType: v1alpha1.ReplicatedStoragePoolTypeLVMThin}
		k2 := rscCacheKey{FTT: 0, GMDR: 0, Topology: v1alpha1.TopologyIgnored, VolumeAccess: v1alpha1.VolumeAccessAny, PoolType: v1alpha1.ReplicatedStoragePoolTypeLVMThin}
		Expect(k1).NotTo(Equal(k2))
	})

	It("different PoolType → not equal", func() {
		k1 := rscCacheKey{FTT: 0, GMDR: 0, Topology: v1alpha1.TopologyIgnored, VolumeAccess: v1alpha1.VolumeAccessAny, PoolType: v1alpha1.ReplicatedStoragePoolTypeLVMThin}
		k2 := rscCacheKey{FTT: 0, GMDR: 0, Topology: v1alpha1.TopologyIgnored, VolumeAccess: v1alpha1.VolumeAccessAny, PoolType: v1alpha1.ReplicatedStoragePoolTypeLVM}
		Expect(k1).NotTo(Equal(k2))
	})

	It("can be used as map key", func() {
		m := map[rscCacheKey]string{}
		k := rscCacheKey{FTT: 1, GMDR: 1, Topology: v1alpha1.TopologyZonal, VolumeAccess: v1alpha1.VolumeAccessLocal, PoolType: v1alpha1.ReplicatedStoragePoolTypeLVMThin}
		m[k] = "cached"
		Expect(m[k]).To(Equal("cached"))

		same := rscCacheKey{FTT: 1, GMDR: 1, Topology: v1alpha1.TopologyZonal, VolumeAccess: v1alpha1.VolumeAccessLocal, PoolType: v1alpha1.ReplicatedStoragePoolTypeLVMThin}
		Expect(m[same]).To(Equal("cached"))
	})
})

// --- poolAbbrev ---

var _ = Describe("poolAbbrev", func() {
	It("returns thn for LVMThin", func() { //nolint:misspell // intentional 3-char abbreviation
		Expect(poolAbbrev(v1alpha1.ReplicatedStoragePoolTypeLVMThin)).To(Equal("thn")) //nolint:misspell // intentional 3-char abbreviation
	})

	It("returns thk for LVM", func() {
		Expect(poolAbbrev(v1alpha1.ReplicatedStoragePoolTypeLVM)).To(Equal("thk"))
	})

	It("truncates unknown type longer than 3 chars", func() {
		Expect(poolAbbrev(v1alpha1.ReplicatedStoragePoolType("Custom"))).To(Equal("cus"))
	})

	It("does not truncate short unknown type", func() {
		Expect(poolAbbrev(v1alpha1.ReplicatedStoragePoolType("Zz"))).To(Equal("zz"))
	})

	It("lowercases unknown type", func() {
		Expect(poolAbbrev(v1alpha1.ReplicatedStoragePoolType("XYZ"))).To(Equal("xyz"))
	})

	It("handles single-char unknown type", func() {
		Expect(poolAbbrev(v1alpha1.ReplicatedStoragePoolType("A"))).To(Equal("a"))
	})
})

// --- sanitizeNodeName ---

var _ = DescribeTable("sanitizeNodeName",
	func(input, expected string) {
		Expect(sanitizeNodeName(input)).To(Equal(expected))
	},
	Entry("simple lowercase", "worker-1", "worker-1"),
	Entry("uppercase → lowercase", "Worker-1", "worker-1"),
	Entry("dots → dashes", "node.example.com", "node-example-com"),
	Entry("mixed", "Node-01.Prod", "node-01-prod"),
	Entry("underscores → dashes", "node_01", "node-01"),
	Entry("all lowercase + digits + dashes pass through", "abc-123", "abc-123"),
	Entry("special chars → dashes", "node@host:1234", "node-host-1234"),
	Entry("empty string", "", ""),
	Entry("all special chars", "@#$%", "----"),
)
