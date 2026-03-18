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

func thinDiscovery(nodes ...v1alpha1.ReplicatedStoragePoolEligibleNode) *Discovery {
	return &Discovery{
		PoolScope: PoolScope{rsp: &v1alpha1.ReplicatedStoragePool{
			Status: v1alpha1.ReplicatedStoragePoolStatus{EligibleNodes: nodes},
		}},
		thick: &v1alpha1.ReplicatedStoragePool{},
	}
}

func thickDiscovery(nodes ...v1alpha1.ReplicatedStoragePoolEligibleNode) *Discovery {
	return &Discovery{
		PoolScope: PoolScope{rsp: &v1alpha1.ReplicatedStoragePool{}},
		thick: &v1alpha1.ReplicatedStoragePool{
			Status: v1alpha1.ReplicatedStoragePoolStatus{EligibleNodes: nodes},
		},
	}
}

func eligibleNode(name string, lvgs ...v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup) v1alpha1.ReplicatedStoragePoolEligibleNode {
	return v1alpha1.ReplicatedStoragePoolEligibleNode{
		NodeName:        name,
		LVMVolumeGroups: lvgs,
	}
}

func lvg(name, thinPool string) v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup {
	return v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
		Name:         name,
		ThinPoolName: thinPool,
	}
}

var thick = v1alpha1.ReplicatedStoragePoolTypeLVM

var _ = Describe("From", func() {
	It("returns thin pool scope by default", func() {
		d := thinDiscovery(eligibleNode("t1"))
		Expect(d.AnyNode()).To(Equal("t1"))
	})

	It("returns thick pool scope via From", func() {
		d := thickDiscovery(eligibleNode("k1"))
		Expect(d.From(thick).AnyNode()).To(Equal("k1"))
	})

	It("From(LVMThin) returns the same as direct call", func() {
		d := thinDiscovery(eligibleNode("t1"))
		Expect(d.From(v1alpha1.ReplicatedStoragePoolTypeLVMThin).AnyNode()).To(Equal("t1"))
	})
})

var _ = Describe("AnyNode", func() {
	It("returns a node from eligible nodes", func() {
		d := thinDiscovery(
			eligibleNode("n1"),
			eligibleNode("n2"),
			eligibleNode("n3"),
		)
		node := d.AnyNode()
		Expect([]string{"n1", "n2", "n3"}).To(ContainElement(node))
	})

	It("returns the only node when single node available", func() {
		d := thinDiscovery(eligibleNode("solo"))
		Expect(d.AnyNode()).To(Equal("solo"))
	})

	It("excludes nodes listed in except", func() {
		d := thinDiscovery(
			eligibleNode("n1"),
			eligibleNode("n2"),
			eligibleNode("n3"),
		)
		node := d.AnyNode("n1", "n3")
		Expect(node).To(Equal("n2"))
	})

	It("includes nodes without LVGs", func() {
		d := thinDiscovery(
			eligibleNode("no-lvg"),
			eligibleNode("has-lvg", lvg("vg1", "")),
		)
		got := map[string]bool{}
		for range 100 {
			got[d.AnyNode()] = true
		}
		Expect(got).To(HaveKey("no-lvg"))
	})

	It("works via From for thick pool", func() {
		d := thickDiscovery(
			eligibleNode("k1"),
			eligibleNode("k2"),
		)
		node := d.From(thick).AnyNode("k1")
		Expect(node).To(Equal("k2"))
	})
})

var _ = Describe("AnyDiskfulNode", func() {
	It("returns a node that has LVGs", func() {
		d := thinDiscovery(
			eligibleNode("no-lvg"),
			eligibleNode("has-lvg", lvg("vg1", "")),
		)
		Expect(d.AnyDiskfulNode()).To(Equal("has-lvg"))
	})

	It("excludes nodes listed in except", func() {
		d := thinDiscovery(
			eligibleNode("n1", lvg("vg1", "")),
			eligibleNode("n2", lvg("vg2", "")),
		)
		Expect(d.AnyDiskfulNode("n1")).To(Equal("n2"))
	})

	It("skips nodes without LVGs", func() {
		d := thinDiscovery(
			eligibleNode("empty"),
			eligibleNode("good", lvg("vg1", "")),
		)
		got := map[string]bool{}
		for range 100 {
			got[d.AnyDiskfulNode()] = true
		}
		Expect(got).To(Equal(map[string]bool{"good": true}))
	})
})

var _ = Describe("AnyDiskfulPlacement", func() {
	It("returns placement with node and LVG", func() {
		d := thinDiscovery(
			eligibleNode("n1", lvg("vg1", "")),
		)
		p := d.AnyDiskfulPlacement()
		Expect(p.NodeName).To(Equal("n1"))
		Expect(p.LVGName).To(Equal("vg1"))
		Expect(p.ThinPoolName).To(BeEmpty())
	})

	It("returns placement with thin pool name for thin LVG", func() {
		d := thinDiscovery(
			eligibleNode("n1", lvg("vg-thin", "tp0")),
		)
		p := d.AnyDiskfulPlacement()
		Expect(p.NodeName).To(Equal("n1"))
		Expect(p.LVGName).To(Equal("vg-thin"))
		Expect(p.ThinPoolName).To(Equal("tp0"))
	})

	It("excludes nodes listed in except", func() {
		d := thinDiscovery(
			eligibleNode("n1", lvg("vg1", "")),
			eligibleNode("n2", lvg("vg2", "")),
		)
		p := d.AnyDiskfulPlacement("n1")
		Expect(p.NodeName).To(Equal("n2"))
		Expect(p.LVGName).To(Equal("vg2"))
	})

	It("picks among multiple LVGs on the same node", func() {
		d := thinDiscovery(
			eligibleNode("n1", lvg("vg-a", ""), lvg("vg-b", "")),
		)
		got := map[string]bool{}
		for range 200 {
			p := d.AnyDiskfulPlacement()
			got[p.LVGName] = true
		}
		Expect(got).To(HaveKey("vg-a"))
		Expect(got).To(HaveKey("vg-b"))
	})

	It("skips nodes without LVGs", func() {
		d := thinDiscovery(
			eligibleNode("empty"),
			eligibleNode("good", lvg("vg1", "")),
		)
		p := d.AnyDiskfulPlacement()
		Expect(p.NodeName).To(Equal("good"))
	})

	It("works via From for thick pool", func() {
		d := thickDiscovery(
			eligibleNode("k1", lvg("vg-thick", "")),
		)
		p := d.From(thick).AnyDiskfulPlacement()
		Expect(p.NodeName).To(Equal("k1"))
		Expect(p.LVGName).To(Equal("vg-thick"))
	})
})
