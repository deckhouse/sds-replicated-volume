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
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

const (
	testSizingThinPool = "tp0"
	testSizingPoolName = "e2e-pool"
)

// sizingClient serves the LVMVolumeGroups the sizing helper reads. The fake
// client answers a missing group with the API server's own NotFound, which is
// what the read path has to report.
func sizingClient(objs ...client.Object) volumeSizingClient {
	scheme := runtime.NewScheme()
	Expect(snc.AddToScheme(scheme)).To(Succeed())
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

// thickLVGObject builds a volume group publishing free space for thick volumes.
func thickLVGObject(name, vgFree string) *snc.LVMVolumeGroup {
	return &snc.LVMVolumeGroup{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status:     snc.LVMVolumeGroupStatus{VGFree: resource.MustParse(vgFree)},
	}
}

// thinLVGObject builds a volume group publishing the given thin pools.
func thinLVGObject(name string, pools ...snc.LVMVolumeGroupThinPoolStatus) *snc.LVMVolumeGroup {
	return &snc.LVMVolumeGroup{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status:     snc.LVMVolumeGroupStatus{ThinPools: pools},
	}
}

// bytesOf renders a quantity literal as the byte count the sizing code works in.
func bytesOf(quantity string) int64 {
	q := resource.MustParse(quantity)
	return q.Value()
}

func thinPoolStatus(name, availableSpace string) snc.LVMVolumeGroupThinPoolStatus {
	return snc.LVMVolumeGroupThinPoolStatus{
		Name:           name,
		AvailableSpace: resource.MustParse(availableSpace),
	}
}

// sizingRequest is a validated request against a thin pool — the request the
// computation tables run with.
func sizingRequest(size string, replicas, override int) volumeCountRequest {
	req, err := newVolumeCountRequest(testSizingPoolName, v1alpha1.ReplicatedStoragePoolTypeLVMThin,
		VolumeCountOptions{VolumeSize: size, DiskfulReplicas: replicas, Override: override})
	Expect(err).NotTo(HaveOccurred())
	return req
}

// sizingCaps builds one node capacity per free-space value, all measured against
// volumes of size.
func sizingCaps(size string, free ...string) []nodeVolumeCapacity {
	sizeBytes := bytesOf(size)
	out := make([]nodeVolumeCapacity, 0, len(free))
	for i, f := range free {
		out = append(out, newNodeVolumeCapacity(
			DiskfulPlacement{
				NodeName:     fmt.Sprintf("node-%d", i+1),
				LVGName:      fmt.Sprintf("lvg-%d", i+1),
				ThinPoolName: testSizingThinPool,
			},
			bytesOf(f), sizeBytes))
	}
	return out
}

// The table below is the whole contract of ParseVolumesOverride, the single
// parser of E2E_UPGRADE_VOLUMES in the process.
var _ = Describe("ParseVolumesOverride", func() {
	DescribeTable("reads a volume count out of one string",
		func(raw string, expected int, accepted bool) {
			n, err := ParseVolumesOverride(raw)
			if !accepted {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(EnvUpgradeVolumes))
				Expect(err.Error()).To(ContainSubstring(raw))
				Expect(err.Error()).To(ContainSubstring("must be a decimal integer >= 1"))
				Expect(n).To(BeZero(), "a refused value must not travel on as a count")
				return
			}
			Expect(err).NotTo(HaveOccurred())
			Expect(n).To(Equal(expected))
		},

		// Unset — the normal case, and the only one that yields 0.
		Entry("empty means not set", "", 0, true),

		Entry("a count below the ceiling", "5", 5, true),
		Entry("the ceiling itself", "20", 20, true),
		Entry("above the ceiling is the caller's business", "25", 25, true),
		Entry("one volume", "1", 1, true),
		Entry("an explicit sign", "+20", 20, true),

		// Refused: a suite that created 0 volumes would pass vacuously.
		Entry("zero", "0", 0, false),
		Entry("negative", "-1", 0, false),

		// Refused: unreadable values, each of which some shell or CI could produce.
		Entry("not a number", "abc", 0, false),
		Entry("a leading space", " 20", 0, false),
		Entry("scientific notation", "1e2", 0, false),
		Entry("a fraction", "2.5", 0, false),
		Entry("only a sign", "+", 0, false),
	)
})

var _ = Describe("newVolumeCountRequest", func() {
	It("defaults the volume size and the replica count", func() {
		req, err := newVolumeCountRequest(testSizingPoolName, v1alpha1.ReplicatedStoragePoolTypeLVMThin,
			VolumeCountOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(req.size).To(Equal(DefaultVolumeSize))
		Expect(req.sizeBytes).To(Equal(bytesOf(DefaultVolumeSize)))
		Expect(req.replicas).To(Equal(defaultDiskfulReplicas))
		Expect(req.override).To(BeZero())
	})

	It("keeps what the caller passed", func() {
		req, err := newVolumeCountRequest(testSizingPoolName, v1alpha1.ReplicatedStoragePoolTypeLVM,
			VolumeCountOptions{VolumeSize: "500Mi", DiskfulReplicas: 2, Override: 7})
		Expect(err).NotTo(HaveOccurred())
		Expect(req.poolType).To(Equal(v1alpha1.ReplicatedStoragePoolTypeLVM))
		Expect(req.sizeBytes).To(Equal(bytesOf("500Mi")))
		Expect(req.replicas).To(Equal(2))
		Expect(req.override).To(Equal(7))
	})

	DescribeTable("refuses a request it cannot compute from",
		func(poolName string, poolType v1alpha1.ReplicatedStoragePoolType, opts VolumeCountOptions, expected string) {
			_, err := newVolumeCountRequest(poolName, poolType, opts)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(expected))
		},

		Entry("a pool with no name", "", v1alpha1.ReplicatedStoragePoolTypeLVMThin,
			VolumeCountOptions{}, "the pool has no name"),
		Entry("a pool type the snapshot never delivered", testSizingPoolName,
			v1alpha1.ReplicatedStoragePoolType(""), VolumeCountOptions{}, "neither LVM nor LVMThin"),
		Entry("a size that is not a quantity", testSizingPoolName, v1alpha1.ReplicatedStoragePoolTypeLVMThin,
			VolumeCountOptions{VolumeSize: "1 gigabyte"}, "is not a Kubernetes quantity"),
		Entry("a size of zero", testSizingPoolName, v1alpha1.ReplicatedStoragePoolTypeLVMThin,
			VolumeCountOptions{VolumeSize: "0"}, "must be positive"),
		Entry("a negative size", testSizingPoolName, v1alpha1.ReplicatedStoragePoolTypeLVMThin,
			VolumeCountOptions{VolumeSize: "-1Gi"}, "must be positive"),
		Entry("a negative replica count", testSizingPoolName, v1alpha1.ReplicatedStoragePoolTypeLVMThin,
			VolumeCountOptions{DiskfulReplicas: -1}, "DiskfulReplicas must be at least 1"),
		Entry("a negative override", testSizingPoolName, v1alpha1.ReplicatedStoragePoolTypeLVMThin,
			VolumeCountOptions{Override: -3}, "Override must not be negative"),
	)
})

var _ = Describe("newNodeVolumeCapacity", func() {
	It("takes the headroom off and floors the rest to whole volumes", func() {
		c := newNodeVolumeCapacity(
			DiskfulPlacement{NodeName: "node-1", LVGName: "lvg-1", ThinPoolName: testSizingThinPool},
			bytesOf("10Gi"), bytesOf("1Gi"))
		Expect(c.FreeBytes).To(Equal(bytesOf("10Gi")))
		Expect(c.UsableBytes).To(Equal(bytesOf("7Gi")))
		Expect(c.Volumes).To(Equal(int64(7)))
		Expect(c.String()).To(Equal("node-1 lvg-1/tp0 free 10Gi, usable 7Gi, fits 7"))
	})

	It("does not offer the headroom to the last volume", func() {
		// One volume fits the free space exactly, and therefore does not fit the
		// part of it the computation may fill.
		c := newNodeVolumeCapacity(DiskfulPlacement{NodeName: "node-1", LVGName: "lvg-1"},
			bytesOf("1Gi"), bytesOf("1Gi"))
		Expect(c.Volumes).To(BeZero())
	})

	It("reads a negative free space as none", func() {
		c := newNodeVolumeCapacity(DiskfulPlacement{NodeName: "node-1", LVGName: "lvg-1"},
			bytesOf("-1Gi"), bytesOf("1Gi"))
		Expect(c.FreeBytes).To(BeZero())
		Expect(c.UsableBytes).To(BeZero())
		Expect(c.Volumes).To(BeZero())
	})

	It("names the volume group alone for a thick pool", func() {
		c := newNodeVolumeCapacity(DiskfulPlacement{NodeName: "node-1", LVGName: "lvg-1"},
			bytesOf("100Gi"), bytesOf("1Gi"))
		Expect(c.String()).To(Equal("node-1 lvg-1 free 100Gi, usable 70Gi, fits 70"))
	})
})

var _ = Describe("humanBytes", func() {
	DescribeTable("renders a byte count for a message",
		func(bytes int64, expected string) {
			Expect(humanBytes(bytes)).To(Equal(expected))
		},

		Entry("nothing", int64(0), "0"),
		Entry("a round binary multiple keeps its own form", bytesOf("10Gi"), "10Gi"),
		Entry("a small odd value stays exact", int64(1023), "1023"),

		// What a percentage of a volume group's free space looks like: no binary
		// form at all, so it is approximated instead of printed as ten digits.
		Entry("70% of 6Gi", bytesOf("6Gi")*volumeSpaceHeadroomPercent/100, "~4300Mi"),
		Entry("just above a mebibyte", int64(1<<20)+1, "~1Mi"),
	)
})

// sizingCase is one row of the computation table: a pool described by the free
// space of its nodes, and the count the formula has to answer with.
type sizingCase struct {
	size     string
	replicas int
	override int
	free     []string

	expect    int
	expectErr []string
}

var _ = Describe("computeVolumeCount", func() {
	DescribeTable("turns per-node free space into a number of volumes",
		func(tc sizingCase) {
			plan, err := computeVolumeCount(sizingCaps(tc.size, tc.free...),
				sizingRequest(tc.size, tc.replicas, tc.override))
			if len(tc.expectErr) > 0 {
				Expect(err).To(HaveOccurred())
				for _, want := range tc.expectErr {
					Expect(err.Error()).To(ContainSubstring(want))
				}
				return
			}
			Expect(err).NotTo(HaveOccurred())
			Expect(plan.Count).To(Equal(tc.expect))
		},

		// A roomy pool is capped by the ceiling, not by its space.
		Entry("a roomy pool is clamped to the ceiling", sizingCase{
			size: "1Gi", replicas: 3, free: []string{"100Gi", "100Gi", "100Gi", "100Gi"},
			expect: VolumeCountCeiling,
		}),

		// Three nodes, 7 volumes each after the headroom: with three replicas per
		// volume every node holds every volume, so 7 is the answer.
		Entry("a tight pool answers with what fits", sizingCase{
			size: "1Gi", replicas: 3, free: []string{"10Gi", "10Gi", "10Gi"},
			expect: 7,
		}),

		// The reason the count is computed per node: the pool's TOTAL free space
		// (330Gi, i.e. 231 volumes of usable space over 3 replicas = 77) says the
		// ceiling is reachable, while the three small nodes cap it at 10 — every
		// volume needs a replica on one of them.
		Entry("space on one big node does not lift the small ones", sizingCase{
			size: "1Gi", replicas: 3, free: []string{"10Gi", "10Gi", "10Gi", "300Gi"},
			expect: 10,
		}),

		// Same rule at its extreme: a single node with room for 210 volumes cannot
		// host a 3-replica volume on its own.
		Entry("one node alone cannot host a replicated volume", sizingCase{
			size: "1Gi", replicas: 3, free: []string{"300Gi", "0", "0"},
			expectErr: []string{"fits at most 0 volumes", "fewer than the 5"},
		}),

		Entry("a pool too small for the floor fails with the numbers", sizingCase{
			size: "1Gi", replicas: 3, free: []string{"6Gi", "6Gi", "6Gi"},
			expectErr: []string{
				`pool "e2e-pool" fits at most 4 volumes of 1Gi with 3 diskful replicas each`,
				"fewer than the 5",
				"usable space is 70% of free",
				"node-1 lvg-1/tp0 free 6Gi, usable ~4300Mi, fits 4",
				"reclaim space",
			},
		}),

		Entry("an empty pool fails instead of creating nothing", sizingCase{
			size: "1Gi", replicas: 3, free: []string{"0", "0", "0"},
			expectErr: []string{"fits at most 0 volumes", "fewer than the 5"},
		}),

		Entry("a pool with fewer usable nodes than replicas fails", sizingCase{
			size: "1Gi", replicas: 3, free: []string{"100Gi", "100Gi"},
			expectErr: []string{
				`pool "e2e-pool" offers 2 usable diskful node(s), fewer than the 3 diskful replicas`,
			},
		}),

		// An override is an explicit request: neither clamp applies to it.
		Entry("an override below the floor is obeyed", sizingCase{
			size: "1Gi", replicas: 3, override: 2, free: []string{"100Gi", "100Gi", "100Gi", "100Gi"},
			expect: 2,
		}),
		Entry("an override above the ceiling is obeyed", sizingCase{
			size: "1Gi", replicas: 3, override: 25, free: []string{"100Gi", "100Gi", "100Gi", "100Gi"},
			expect: 25,
		}),

		// ... but it is still checked against the free space, so the run fails here
		// instead of on the 8th volume's provisioning.
		Entry("an override that does not fit fails with the numbers", sizingCase{
			size: "1Gi", replicas: 3, override: 25, free: []string{"10Gi", "10Gi", "10Gi"},
			expectErr: []string{
				EnvUpgradeVolumes + " asks for 25 volumes of 1Gi with 3 diskful replicas each",
				"fits at most 7",
			},
		}),

		// The size and the replica count are the other two factors of the same
		// formula: ten times the volume divides the count by ten, and one replica
		// less spreads the same space over more volumes.
		Entry("a larger volume size lowers the count", sizingCase{
			size: "10Gi", replicas: 3, free: []string{"100Gi", "100Gi", "100Gi"},
			expect: 7,
		}),
		Entry("fewer replicas raise the count", sizingCase{
			size: "1Gi", replicas: 2, free: []string{"10Gi", "10Gi", "10Gi"},
			expect: 10,
		}),
		Entry("a single replica needs no spreading at all", sizingCase{
			size: "1Gi", replicas: 1, free: []string{"10Gi"},
			expect: 7,
		}),
	)

	It("reports the derivation it decided from", func() {
		plan, err := computeVolumeCount(sizingCaps("1Gi", "10Gi", "10Gi", "10Gi"),
			sizingRequest("1Gi", 3, 0))
		Expect(err).NotTo(HaveOccurred())
		Expect(plan.Count).To(Equal(7))
		Expect(plan.MaxFitting).To(Equal(int64(7)))
		Expect(plan.Overridden).To(BeFalse())
		Expect(plan.String()).To(ContainSubstring(`pool "e2e-pool" (LVMThin): 7 volumes of 1Gi with 3 diskful replicas each`))
		Expect(plan.String()).To(ContainSubstring(fmt.Sprintf("computed, clamped to [%d, %d]",
			VolumeCountFloor, VolumeCountCeiling)))
		Expect(plan.String()).To(ContainSubstring("node-3 lvg-3/tp0 free 10Gi, usable 7Gi, fits 7"))
	})

	It("marks a count that came from the override", func() {
		plan, err := computeVolumeCount(sizingCaps("1Gi", "100Gi", "100Gi", "100Gi"),
			sizingRequest("1Gi", 3, 25))
		Expect(err).NotTo(HaveOccurred())
		Expect(plan.Count).To(Equal(25))
		Expect(plan.Overridden).To(BeTrue())
		Expect(plan.String()).To(ContainSubstring(EnvUpgradeVolumes))
	})
})

var _ = Describe("readPoolCapacity", func() {
	It("reads status.vgFree for a thick pool", func() {
		req, err := newVolumeCountRequest(testSizingPoolName, v1alpha1.ReplicatedStoragePoolTypeLVM,
			VolumeCountOptions{VolumeSize: "1Gi"})
		Expect(err).NotTo(HaveOccurred())

		cl := sizingClient(
			thickLVGObject("lvg-1", "10Gi"),
			thickLVGObject("lvg-2", "20Gi"),
		)
		caps, err := readPoolCapacity(context.Background(), cl, []DiskfulPlacement{
			{NodeName: "node-1", LVGName: "lvg-1"},
			{NodeName: "node-2", LVGName: "lvg-2"},
		}, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(caps).To(HaveLen(2))
		Expect(caps[0].NodeName).To(Equal("node-1"))
		Expect(caps[0].FreeBytes).To(Equal(bytesOf("10Gi")))
		Expect(caps[0].Volumes).To(Equal(int64(7)))
		Expect(caps[1].Volumes).To(Equal(int64(14)))
	})

	It("ignores the thin pools of a thick pool's volume group", func() {
		// A volume group may serve both types: vgFree already excludes the space
		// the thin pool took, so nothing has to be subtracted here.
		req, err := newVolumeCountRequest(testSizingPoolName, v1alpha1.ReplicatedStoragePoolTypeLVM,
			VolumeCountOptions{VolumeSize: "1Gi"})
		Expect(err).NotTo(HaveOccurred())

		lvgObj := thickLVGObject("lvg-1", "10Gi")
		lvgObj.Status.ThinPools = []snc.LVMVolumeGroupThinPoolStatus{thinPoolStatus(testSizingThinPool, "500Gi")}

		caps, err := readPoolCapacity(context.Background(), sizingClient(lvgObj),
			[]DiskfulPlacement{{NodeName: "node-1", LVGName: "lvg-1"}}, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(caps[0].Volumes).To(Equal(int64(7)))
	})

	It("reads the availableSpace of the thin pool the placement names", func() {
		req := sizingRequest("1Gi", 3, 0)
		cl := sizingClient(thinLVGObject("lvg-1",
			thinPoolStatus("other", "500Gi"),
			thinPoolStatus(testSizingThinPool, "10Gi"),
			thinPoolStatus("yet-another", "900Gi"),
		))
		caps, err := readPoolCapacity(context.Background(), cl,
			[]DiskfulPlacement{{NodeName: "node-1", LVGName: "lvg-1", ThinPoolName: testSizingThinPool}}, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(caps[0].ThinPoolName).To(Equal(testSizingThinPool))
		Expect(caps[0].FreeBytes).To(Equal(bytesOf("10Gi")))
		Expect(caps[0].Volumes).To(Equal(int64(7)))
	})

	It("fails when the volume group publishes no such thin pool", func() {
		req := sizingRequest("1Gi", 3, 0)
		cl := sizingClient(thinLVGObject("lvg-1", thinPoolStatus("other", "500Gi")))
		_, err := readPoolCapacity(context.Background(), cl,
			[]DiskfulPlacement{{NodeName: "node-1", LVGName: "lvg-1", ThinPoolName: testSizingThinPool}}, req)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(`LVMVolumeGroup "lvg-1" of node "node-1" reports no thin pool "tp0"`))
		Expect(err.Error()).To(ContainSubstring("[other]"), "the message has to show what was there instead")
	})

	It("fails when a thin pool's placement carries no thin pool name", func() {
		req := sizingRequest("1Gi", 3, 0)
		cl := sizingClient(thinLVGObject("lvg-1", thinPoolStatus(testSizingThinPool, "10Gi")))
		_, err := readPoolCapacity(context.Background(), cl,
			[]DiskfulPlacement{{NodeName: "node-1", LVGName: "lvg-1"}}, req)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("without a thin pool name"))
	})

	It("fails when the volume group is gone", func() {
		req := sizingRequest("1Gi", 3, 0)
		_, err := readPoolCapacity(context.Background(), sizingClient(),
			[]DiskfulPlacement{{NodeName: "node-1", LVGName: "lvg-1", ThinPoolName: testSizingThinPool}}, req)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(`reading LVMVolumeGroup "lvg-1" of node "node-1"`))
		Expect(err.Error()).To(ContainSubstring("not found"))
	})

	It("returns no capacity for a pool with no usable placement", func() {
		req := sizingRequest("1Gi", 3, 0)
		caps, err := readPoolCapacity(context.Background(), sizingClient(), nil, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(caps).To(BeEmpty())
	})
})

var _ = Describe("planVolumeCount", func() {
	// thinSizingPool is an RSP snapshot the way discovery tracks one: three usable
	// nodes and a fourth whose agent is down.
	thinSizingPool := func() PoolScope {
		notReady := eligibleNode("node-4", lvg("lvg-4", testSizingThinPool))
		notReady.AgentReady = false
		return testPoolScope(&v1alpha1.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: testSizingPoolName},
			Spec:       v1alpha1.ReplicatedStoragePoolSpec{Type: v1alpha1.ReplicatedStoragePoolTypeLVMThin},
			Status: v1alpha1.ReplicatedStoragePoolStatus{
				EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
					eligibleNode("node-1", lvg("lvg-1", testSizingThinPool)),
					eligibleNode("node-2", lvg("lvg-2", testSizingThinPool)),
					eligibleNode("node-3", lvg("lvg-3", testSizingThinPool)),
					notReady,
				},
			},
		})
	}

	thinLVGs := func(available ...string) []client.Object {
		out := make([]client.Object, 0, len(available))
		for i, space := range available {
			out = append(out, thinLVGObject(fmt.Sprintf("lvg-%d", i+1),
				thinPoolStatus(testSizingThinPool, space)))
		}
		return out
	}

	It("computes the count from the pool's usable nodes only", func() {
		pool := thinSizingPool()
		// node-4 has room for hundreds of volumes, but its agent is down: the count
		// may only come from the three usable ones.
		cl := sizingClient(thinLVGs("10Gi", "10Gi", "10Gi", "10Ti")...)

		plan, err := planVolumeCount(context.Background(), cl, VolumeCountOptions{Pool: &pool})
		Expect(err).NotTo(HaveOccurred())
		Expect(plan.Count).To(Equal(7))
		Expect(plan.Capacities).To(HaveLen(3))
		Expect(plan.String()).To(ContainSubstring(`pool "e2e-pool" (LVMThin)`))
		Expect(plan.String()).NotTo(ContainSubstring("node-4"))
	})

	It("defaults the volume size to DefaultVolumeSize", func() {
		pool := thinSizingPool()
		cl := sizingClient(thinLVGs("10Gi", "10Gi", "10Gi")...)

		plan, err := planVolumeCount(context.Background(), cl, VolumeCountOptions{Pool: &pool})
		Expect(err).NotTo(HaveOccurred())
		Expect(plan.req.size).To(Equal(DefaultVolumeSize))
		Expect(plan.req.replicas).To(Equal(defaultDiskfulReplicas))
	})

	It("obeys an override that fits", func() {
		pool := thinSizingPool()
		cl := sizingClient(thinLVGs("10Gi", "10Gi", "10Gi")...)

		plan, err := planVolumeCount(context.Background(), cl,
			VolumeCountOptions{Pool: &pool, Override: 3})
		Expect(err).NotTo(HaveOccurred())
		Expect(plan.Count).To(Equal(3))
		Expect(plan.Overridden).To(BeTrue())
	})

	It("fails when the pool's volume groups cannot be read", func() {
		pool := thinSizingPool()
		_, err := planVolumeCount(context.Background(), sizingClient(), VolumeCountOptions{Pool: &pool})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(`reading LVMVolumeGroup "lvg-1" of node "node-1"`))
	})

	It("refuses a request without a pool", func() {
		_, err := planVolumeCount(context.Background(), sizingClient(), VolumeCountOptions{})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("Pool must not be nil"))
	})
})
