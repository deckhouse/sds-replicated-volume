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

package rspcontroller

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// testGracePeriod is the grace period used in tests for NotReady nodes.
const testGracePeriod = 5 * time.Minute

func TestRSPController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "rsp_controller Reconciler Suite")
}

var _ = Describe("computeActualEligibleNodes", func() {
	var (
		rsp              *v1alpha1.ReplicatedStoragePool
		lvgs             map[string]lvgView
		nodes            []nodeView
		agentReadyByNode map[string]bool
	)

	BeforeEach(func() {
		rsp = &v1alpha1.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
			Spec: v1alpha1.ReplicatedStoragePoolSpec{
				Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
					{Name: "lvg-1"},
				},
				EligibleNodesPolicy: v1alpha1.ReplicatedStoragePoolEligibleNodesPolicy{
					NotReadyGracePeriod: metav1.Duration{Duration: testGracePeriod},
				},
			},
		}
		lvgs = map[string]lvgView{
			"lvg-1": {
				name:     "lvg-1",
				nodeName: "node-1",
				ready:    true,
			},
		}
		nodes = []nodeView{
			{
				name:     "node-1",
				zoneName: "zone-a",
				ready: nodeViewReady{
					hasCondition: true,
					status:       true,
				},
			},
		}
		agentReadyByNode = map[string]bool{
			"node-1": true,
		}
	})

	It("returns eligible node when all conditions match", func() {
		result, _ := computeActualEligibleNodes(rsp, lvgs, nodes, agentReadyByNode)

		Expect(result).To(HaveLen(1))
		Expect(result[0].NodeName).To(Equal("node-1"))
		Expect(result[0].ZoneName).To(Equal("zone-a"))
		Expect(result[0].NodeReady).To(BeTrue())
		Expect(result[0].AgentReady).To(BeTrue())
		Expect(result[0].LVMVolumeGroups).To(HaveLen(1))
		Expect(result[0].LVMVolumeGroups[0].Name).To(Equal("lvg-1"))
		Expect(result[0].LVMVolumeGroups[0].Ready).To(BeTrue())
	})

	Context("zone extraction", func() {
		// Note: Zone/label filtering is done in Reconcile before calling computeActualEligibleNodes.
		// This function only extracts the zone label from nodes that are passed to it.

		It("extracts zone label from node", func() {
			nodes[0].zoneName = "zone-x"

			result, _ := computeActualEligibleNodes(rsp, lvgs, nodes, agentReadyByNode)

			Expect(result).To(HaveLen(1))
			Expect(result[0].ZoneName).To(Equal("zone-x"))
		})

		It("sets empty zone when label is missing", func() {
			nodes[0].zoneName = ""

			result, _ := computeActualEligibleNodes(rsp, lvgs, nodes, agentReadyByNode)

			Expect(result).To(HaveLen(1))
			Expect(result[0].ZoneName).To(BeEmpty())
		})
	})

	Context("LVG matching", func() {
		It("includes node without matching LVG (client-only/tiebreaker nodes)", func() {
			rsp.Spec.LVMVolumeGroups = []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
				{Name: "lvg-2"}, // This LVG does not exist on node-1.
			}

			result, _ := computeActualEligibleNodes(rsp, lvgs, nodes, agentReadyByNode)

			// Node is still eligible but without LVGs.
			Expect(result).To(HaveLen(1))
			Expect(result[0].NodeName).To(Equal("node-1"))
			Expect(result[0].LVMVolumeGroups).To(BeEmpty())
		})
	})

	Context("node readiness", func() {
		It("excludes node NotReady beyond grace period", func() {
			nodes[0].ready = nodeViewReady{
				hasCondition:       true,
				status:             false,
				lastTransitionTime: time.Now().Add(-10 * time.Minute),
			}

			result, _ := computeActualEligibleNodes(rsp, lvgs, nodes, agentReadyByNode)

			Expect(result).To(BeEmpty())
		})

		It("includes node NotReady within grace period", func() {
			nodes[0].ready = nodeViewReady{
				hasCondition:       true,
				status:             false,
				lastTransitionTime: time.Now().Add(-2 * time.Minute),
			}

			result, _ := computeActualEligibleNodes(rsp, lvgs, nodes, agentReadyByNode)

			Expect(result).To(HaveLen(1))
			Expect(result[0].NodeReady).To(BeFalse())
		})
	})

	Context("LVG unschedulable annotation", func() {
		It("marks LVG as unschedulable when annotation is present", func() {
			lvg := lvgs["lvg-1"]
			lvg.unschedulable = true
			lvgs["lvg-1"] = lvg

			result, _ := computeActualEligibleNodes(rsp, lvgs, nodes, agentReadyByNode)

			Expect(result).To(HaveLen(1))
			Expect(result[0].LVMVolumeGroups[0].Unschedulable).To(BeTrue())
		})
	})

	Context("node unschedulable", func() {
		It("marks node as unschedulable when spec.unschedulable is true", func() {
			nodes[0].unschedulable = true

			result, _ := computeActualEligibleNodes(rsp, lvgs, nodes, agentReadyByNode)

			Expect(result).To(HaveLen(1))
			Expect(result[0].Unschedulable).To(BeTrue())
		})
	})

	Context("agent readiness", func() {
		It("populates AgentReady from agentReadyByNode map", func() {
			agentReadyByNode["node-1"] = false

			result, _ := computeActualEligibleNodes(rsp, lvgs, nodes, agentReadyByNode)

			Expect(result).To(HaveLen(1))
			Expect(result[0].AgentReady).To(BeFalse())
		})

		It("sets AgentReady to false when node not in map", func() {
			delete(agentReadyByNode, "node-1")

			result, _ := computeActualEligibleNodes(rsp, lvgs, nodes, agentReadyByNode)

			Expect(result).To(HaveLen(1))
			Expect(result[0].AgentReady).To(BeFalse())
		})
	})

	Context("LVG Ready status", func() {
		It("marks LVG as not ready when Ready condition is False", func() {
			lvg := lvgs["lvg-1"]
			lvg.ready = false
			lvgs["lvg-1"] = lvg

			result, _ := computeActualEligibleNodes(rsp, lvgs, nodes, agentReadyByNode)

			Expect(result).To(HaveLen(1))
			Expect(result[0].LVMVolumeGroups[0].Ready).To(BeFalse())
		})

		It("marks LVG as not ready when thin pool is not ready", func() {
			rsp.Spec.Type = v1alpha1.ReplicatedStoragePoolTypeLVMThin
			rsp.Spec.LVMVolumeGroups = []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
				{Name: "lvg-1", ThinPoolName: "thin-pool-1"},
			}
			// thin pool not in thinPoolReady set = not ready

			result, _ := computeActualEligibleNodes(rsp, lvgs, nodes, agentReadyByNode)

			Expect(result).To(HaveLen(1))
			Expect(result[0].LVMVolumeGroups[0].Ready).To(BeFalse())
		})

		It("marks LVG as ready when thin pool is ready", func() {
			rsp.Spec.Type = v1alpha1.ReplicatedStoragePoolTypeLVMThin
			rsp.Spec.LVMVolumeGroups = []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
				{Name: "lvg-1", ThinPoolName: "thin-pool-1"},
			}
			lvg := lvgs["lvg-1"]
			lvg.thinPoolReady = map[string]struct{}{"thin-pool-1": {}}
			lvgs["lvg-1"] = lvg

			result, _ := computeActualEligibleNodes(rsp, lvgs, nodes, agentReadyByNode)

			Expect(result).To(HaveLen(1))
			Expect(result[0].LVMVolumeGroups[0].Ready).To(BeTrue())
		})
	})

	Context("worldStateExpiresAt", func() {
		It("returns nil when no grace period is active", func() {
			_, expiresAt := computeActualEligibleNodes(rsp, lvgs, nodes, agentReadyByNode)

			Expect(expiresAt).To(BeNil())
		})

		It("returns earliest grace expiration time", func() {
			transitionTime := time.Now().Add(-2 * time.Minute)
			nodes[0].ready = nodeViewReady{
				hasCondition:       true,
				status:             false,
				lastTransitionTime: transitionTime,
			}

			_, expiresAt := computeActualEligibleNodes(rsp, lvgs, nodes, agentReadyByNode)

			Expect(expiresAt).NotTo(BeNil())
			expected := transitionTime.Add(testGracePeriod)
			Expect(expiresAt.Sub(expected)).To(BeNumerically("<", time.Second))
		})
	})

	It("sorts eligible nodes by name", func() {
		lvgs["lvg-2"] = lvgView{
			name:     "lvg-2",
			nodeName: "node-2",
			ready:    true,
		}
		rsp.Spec.LVMVolumeGroups = append(rsp.Spec.LVMVolumeGroups, v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{Name: "lvg-2"})
		nodes = append(nodes, nodeView{
			name: "node-2",
			ready: nodeViewReady{
				hasCondition: true,
				status:       true,
			},
		})

		result, _ := computeActualEligibleNodes(rsp, lvgs, nodes, agentReadyByNode)

		Expect(result).To(HaveLen(2))
		Expect(result[0].NodeName).To(Equal("node-1"))
		Expect(result[1].NodeName).To(Equal("node-2"))
	})
})

var _ = Describe("buildLVGByNodeMap", func() {
	var (
		rsp  *v1alpha1.ReplicatedStoragePool
		lvgs map[string]lvgView
	)

	BeforeEach(func() {
		rsp = &v1alpha1.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
			Spec: v1alpha1.ReplicatedStoragePoolSpec{
				Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
					{Name: "lvg-1"},
				},
				EligibleNodesPolicy: v1alpha1.ReplicatedStoragePoolEligibleNodesPolicy{
					NotReadyGracePeriod: metav1.Duration{Duration: testGracePeriod},
				},
			},
		}
		lvgs = map[string]lvgView{
			"lvg-1": {
				name:     "lvg-1",
				nodeName: "node-1",
				ready:    true,
			},
		}
	})

	It("returns empty map for empty LVGs", func() {
		result := buildLVGByNodeMap(nil, rsp)

		Expect(result).To(BeEmpty())
	})

	It("maps LVG to node correctly", func() {
		result := buildLVGByNodeMap(lvgs, rsp)

		Expect(result).To(HaveKey("node-1"))
		Expect(result["node-1"]).To(HaveLen(1))
		Expect(result["node-1"][0].Name).To(Equal("lvg-1"))
	})

	It("skips LVG not referenced by RSP", func() {
		lvgs["lvg-not-referenced"] = lvgView{
			name:     "lvg-not-referenced",
			nodeName: "node-2",
			ready:    true,
		}

		result := buildLVGByNodeMap(lvgs, rsp)

		Expect(result).NotTo(HaveKey("node-2"))
	})

	It("skips LVG with empty nodeName", func() {
		lvg := lvgs["lvg-1"]
		lvg.nodeName = ""
		lvgs["lvg-1"] = lvg

		result := buildLVGByNodeMap(lvgs, rsp)

		Expect(result).To(BeEmpty())
	})

	It("sorts LVGs by name per node", func() {
		rsp.Spec.LVMVolumeGroups = []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
			{Name: "lvg-c"},
			{Name: "lvg-a"},
			{Name: "lvg-b"},
		}
		lvgs = map[string]lvgView{
			"lvg-c": {name: "lvg-c", nodeName: "node-1", ready: true},
			"lvg-a": {name: "lvg-a", nodeName: "node-1", ready: true},
			"lvg-b": {name: "lvg-b", nodeName: "node-1", ready: true},
		}

		result := buildLVGByNodeMap(lvgs, rsp)

		Expect(result["node-1"]).To(HaveLen(3))
		Expect(result["node-1"][0].Name).To(Equal("lvg-a"))
		Expect(result["node-1"][1].Name).To(Equal("lvg-b"))
		Expect(result["node-1"][2].Name).To(Equal("lvg-c"))
	})

	It("sets Ready field based on LVG condition", func() {
		lvg := lvgs["lvg-1"]
		lvg.ready = false
		lvgs["lvg-1"] = lvg

		result := buildLVGByNodeMap(lvgs, rsp)

		Expect(result["node-1"][0].Ready).To(BeFalse())
	})

	It("sets Ready field based on thin pool status for LVMThin", func() {
		rsp.Spec.Type = v1alpha1.ReplicatedStoragePoolTypeLVMThin
		rsp.Spec.LVMVolumeGroups = []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
			{Name: "lvg-1", ThinPoolName: "thin-pool-1"},
		}
		// thin pool not in thinPoolReady set = not ready

		result := buildLVGByNodeMap(lvgs, rsp)

		Expect(result["node-1"][0].Ready).To(BeFalse())
		Expect(result["node-1"][0].ThinPoolName).To(Equal("thin-pool-1"))
	})

	It("marks LVG as unschedulable when annotation present", func() {
		lvg := lvgs["lvg-1"]
		lvg.unschedulable = true
		lvgs["lvg-1"] = lvg

		result := buildLVGByNodeMap(lvgs, rsp)

		Expect(result["node-1"][0].Unschedulable).To(BeTrue())
	})
})

var _ = Describe("isLVGReady", func() {
	var lvg *lvgView

	BeforeEach(func() {
		lvg = &lvgView{
			name:  "lvg-1",
			ready: true,
		}
	})

	It("returns false when LVG has no Ready condition", func() {
		lvg.ready = false

		result := isLVGReady(lvg, "")

		Expect(result).To(BeFalse())
	})

	It("returns false when LVG Ready=False", func() {
		lvg.ready = false

		result := isLVGReady(lvg, "")

		Expect(result).To(BeFalse())
	})

	It("returns true when LVG Ready=True and no thin pool specified", func() {
		result := isLVGReady(lvg, "")

		Expect(result).To(BeTrue())
	})

	It("returns true when LVG Ready=True and thin pool Ready=true", func() {
		lvg.thinPoolReady = map[string]struct{}{"thin-pool-1": {}}

		result := isLVGReady(lvg, "thin-pool-1")

		Expect(result).To(BeTrue())
	})

	It("returns false when LVG Ready=True but thin pool Ready=false", func() {
		// thin pool not in thinPoolReady set = not ready

		result := isLVGReady(lvg, "thin-pool-1")

		Expect(result).To(BeFalse())
	})

	It("returns false when thin pool not found in status", func() {
		lvg.thinPoolReady = map[string]struct{}{"other-pool": {}}

		result := isLVGReady(lvg, "thin-pool-1")

		Expect(result).To(BeFalse())
	})
})

var _ = Describe("isNodeReadyOrWithinGrace", func() {
	var ready nodeViewReady

	BeforeEach(func() {
		ready = nodeViewReady{
			hasCondition: true,
			status:       true,
		}
	})

	It("returns (true, false, zero) for Ready node", func() {
		isReady, excluded, expiresAt := isNodeReadyOrWithinGrace(ready, testGracePeriod)

		Expect(isReady).To(BeTrue())
		Expect(excluded).To(BeFalse())
		Expect(expiresAt.IsZero()).To(BeTrue())
	})

	It("returns (false, false, zero) for node without Ready condition (unknown state)", func() {
		ready.hasCondition = false

		isReady, excluded, expiresAt := isNodeReadyOrWithinGrace(ready, testGracePeriod)

		Expect(isReady).To(BeFalse())
		Expect(excluded).To(BeFalse()) // Unknown state is treated as within grace.
		Expect(expiresAt.IsZero()).To(BeTrue())
	})

	It("returns (false, true, zero) for NotReady beyond grace period", func() {
		ready = nodeViewReady{
			hasCondition:       true,
			status:             false,
			lastTransitionTime: time.Now().Add(-10 * time.Minute),
		}

		isReady, excluded, expiresAt := isNodeReadyOrWithinGrace(ready, testGracePeriod)

		Expect(isReady).To(BeFalse())
		Expect(excluded).To(BeTrue())
		Expect(expiresAt.IsZero()).To(BeTrue())
	})

	It("returns (false, false, expiresAt) for NotReady within grace period", func() {
		transitionTime := time.Now().Add(-2 * time.Minute)
		ready = nodeViewReady{
			hasCondition:       true,
			status:             false,
			lastTransitionTime: transitionTime,
		}

		isReady, excluded, expiresAt := isNodeReadyOrWithinGrace(ready, testGracePeriod)

		Expect(isReady).To(BeFalse())
		Expect(excluded).To(BeFalse())
		expected := transitionTime.Add(testGracePeriod)
		Expect(expiresAt.Sub(expected)).To(BeNumerically("<", time.Second))
	})
})

var _ = Describe("validateRSPAndLVGs", func() {
	var (
		rsp  *v1alpha1.ReplicatedStoragePool
		lvgs map[string]lvgView
	)

	BeforeEach(func() {
		rsp = &v1alpha1.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
			Spec: v1alpha1.ReplicatedStoragePoolSpec{
				Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
					{Name: "lvg-1"},
				},
				EligibleNodesPolicy: v1alpha1.ReplicatedStoragePoolEligibleNodesPolicy{
					NotReadyGracePeriod: metav1.Duration{Duration: testGracePeriod},
				},
			},
		}
		lvgs = map[string]lvgView{
			"lvg-1": {
				name:     "lvg-1",
				nodeName: "node-1",
			},
		}
	})

	It("returns nil when type is not LVMThin", func() {
		err := validateRSPAndLVGs(rsp, lvgs)

		Expect(err).NotTo(HaveOccurred())
	})

	It("returns error for LVMThin when thinPoolName is empty", func() {
		rsp.Spec.Type = v1alpha1.ReplicatedStoragePoolTypeLVMThin
		// thinPoolName is empty

		err := validateRSPAndLVGs(rsp, lvgs)

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("thinPoolName is required"))
	})

	It("returns error for LVMThin when thinPool not found in LVG spec", func() {
		rsp.Spec.Type = v1alpha1.ReplicatedStoragePoolTypeLVMThin
		rsp.Spec.LVMVolumeGroups = []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
			{Name: "lvg-1", ThinPoolName: "missing-thin-pool"},
		}
		lvg := lvgs["lvg-1"]
		lvg.specThinPoolNames = map[string]struct{}{"other-thin-pool": {}}
		lvgs["lvg-1"] = lvg

		err := validateRSPAndLVGs(rsp, lvgs)

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not found in Spec.ThinPools"))
	})

	It("returns nil when all validations pass for LVMThin", func() {
		rsp.Spec.Type = v1alpha1.ReplicatedStoragePoolTypeLVMThin
		rsp.Spec.LVMVolumeGroups = []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
			{Name: "lvg-1", ThinPoolName: "thin-pool-1"},
		}
		lvg := lvgs["lvg-1"]
		lvg.specThinPoolNames = map[string]struct{}{"thin-pool-1": {}}
		lvgs["lvg-1"] = lvg

		err := validateRSPAndLVGs(rsp, lvgs)

		Expect(err).NotTo(HaveOccurred())
	})

	It("panics when LVG referenced by RSP not in lvgs map", func() {
		rsp.Spec.Type = v1alpha1.ReplicatedStoragePoolTypeLVMThin
		rsp.Spec.LVMVolumeGroups = []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
			{Name: "lvg-missing", ThinPoolName: "thin-pool-1"},
		}

		Expect(func() {
			_ = validateRSPAndLVGs(rsp, lvgs)
		}).To(Panic())
	})
})

var _ = Describe("applyEligibleNodesAndIncrementRevisionIfChanged", func() {
	var rsp *v1alpha1.ReplicatedStoragePool

	BeforeEach(func() {
		rsp = &v1alpha1.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
			Status: v1alpha1.ReplicatedStoragePoolStatus{
				EligibleNodesRevision: 1,
				EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
					{NodeName: "node-1"},
				},
			},
		}
	})

	It("returns false when eligible nodes unchanged", func() {
		newNodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1"},
		}

		changed := applyEligibleNodesAndIncrementRevisionIfChanged(rsp, newNodes)

		Expect(changed).To(BeFalse())
		Expect(rsp.Status.EligibleNodesRevision).To(Equal(int64(1)))
	})

	It("returns true and increments revision when nodes changed", func() {
		newNodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1"},
			{NodeName: "node-2"},
		}

		changed := applyEligibleNodesAndIncrementRevisionIfChanged(rsp, newNodes)

		Expect(changed).To(BeTrue())
		Expect(rsp.Status.EligibleNodesRevision).To(Equal(int64(2)))
		Expect(rsp.Status.EligibleNodes).To(HaveLen(2))
	})

	It("detects change in NodeReady field", func() {
		newNodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1", NodeReady: true},
		}

		changed := applyEligibleNodesAndIncrementRevisionIfChanged(rsp, newNodes)

		Expect(changed).To(BeTrue())
	})
})

var _ = Describe("areEligibleNodesEqual", func() {
	It("returns true for equal slices", func() {
		a := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1", ZoneName: "zone-a"},
		}
		b := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1", ZoneName: "zone-a"},
		}

		Expect(areEligibleNodesEqual(a, b)).To(BeTrue())
	})

	It("returns false for different lengths", func() {
		a := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1"},
		}
		b := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1"},
			{NodeName: "node-2"},
		}

		Expect(areEligibleNodesEqual(a, b)).To(BeFalse())
	})

	It("returns false for different field values", func() {
		a := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1", AgentReady: true},
		}
		b := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1", AgentReady: false},
		}

		Expect(areEligibleNodesEqual(a, b)).To(BeFalse())
	})

	It("handles empty slices", func() {
		var a []v1alpha1.ReplicatedStoragePoolEligibleNode
		var b []v1alpha1.ReplicatedStoragePoolEligibleNode

		Expect(areEligibleNodesEqual(a, b)).To(BeTrue())
	})
})

var _ = Describe("areLVGsEqual", func() {
	It("returns true for equal slices", func() {
		a := []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
			{Name: "lvg-1", ThinPoolName: "tp-1", Unschedulable: false, Ready: true},
		}
		b := []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
			{Name: "lvg-1", ThinPoolName: "tp-1", Unschedulable: false, Ready: true},
		}

		Expect(areLVGsEqual(a, b)).To(BeTrue())
	})

	It("returns false for different lengths", func() {
		a := []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
			{Name: "lvg-1"},
		}
		b := []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
			{Name: "lvg-1"},
			{Name: "lvg-2"},
		}

		Expect(areLVGsEqual(a, b)).To(BeFalse())
	})

	It("returns false for different Ready values", func() {
		a := []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
			{Name: "lvg-1", Ready: true},
		}
		b := []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
			{Name: "lvg-1", Ready: false},
		}

		Expect(areLVGsEqual(a, b)).To(BeFalse())
	})

	It("handles empty slices", func() {
		var a []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup
		var b []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup

		Expect(areLVGsEqual(a, b)).To(BeTrue())
	})
})

// =============================================================================
// Integration Tests
// =============================================================================

var _ = Describe("Reconciler", func() {
	var (
		scheme *runtime.Scheme
		cl     client.WithWatch
		rec    *Reconciler
	)

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(snc.AddToScheme(scheme)).To(Succeed())
		cl = nil
		rec = nil
	})

	Describe("Reconcile", func() {
		It("does nothing when RSP is not found", func() {
			cl = fake.NewClientBuilder().WithScheme(scheme).Build()
			rec = NewReconciler(cl, logr.Discard(), "test-namespace")

			result, err := rec.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: client.ObjectKey{Name: "rsp-not-found"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
		})

		It("sets Ready=False when LVGs not found", func() {
			rsp := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
				Spec: v1alpha1.ReplicatedStoragePoolSpec{
					Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
					LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
						{Name: "lvg-missing"},
					},
					EligibleNodesPolicy: v1alpha1.ReplicatedStoragePoolEligibleNodesPolicy{
						NotReadyGracePeriod: metav1.Duration{Duration: testGracePeriod},
					},
				},
			}
			cl = fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rsp).
				WithStatusSubresource(rsp).
				Build()
			rec = NewReconciler(cl, logr.Discard(), "test-namespace")

			result, err := rec.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: client.ObjectKey{Name: "rsp-1"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			var updatedRSP v1alpha1.ReplicatedStoragePool
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "rsp-1"}, &updatedRSP)).To(Succeed())
			readyCond := obju.GetStatusCondition(&updatedRSP, v1alpha1.ReplicatedStoragePoolCondReadyType)
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(readyCond.Reason).To(Equal(v1alpha1.ReplicatedStoragePoolCondReadyReasonLVMVolumeGroupNotFound))
		})

		It("sets Ready=False when validation fails for LVMThin", func() {
			rsp := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
				Spec: v1alpha1.ReplicatedStoragePoolSpec{
					Type: v1alpha1.ReplicatedStoragePoolTypeLVMThin,
					LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
						{Name: "lvg-1"}, // Missing thinPoolName.
					},
					EligibleNodesPolicy: v1alpha1.ReplicatedStoragePoolEligibleNodesPolicy{
						NotReadyGracePeriod: metav1.Duration{Duration: testGracePeriod},
					},
				},
			}
			lvg := &snc.LVMVolumeGroup{
				ObjectMeta: metav1.ObjectMeta{Name: "lvg-1"},
				Spec: snc.LVMVolumeGroupSpec{
					Local: snc.LVMVolumeGroupLocalSpec{NodeName: "node-1"},
				},
			}
			cl = fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rsp, lvg).
				WithStatusSubresource(rsp).
				Build()
			rec = NewReconciler(cl, logr.Discard(), "test-namespace")

			result, err := rec.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: client.ObjectKey{Name: "rsp-1"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			var updatedRSP v1alpha1.ReplicatedStoragePool
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "rsp-1"}, &updatedRSP)).To(Succeed())
			readyCond := obju.GetStatusCondition(&updatedRSP, v1alpha1.ReplicatedStoragePoolCondReadyType)
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(readyCond.Reason).To(Equal(v1alpha1.ReplicatedStoragePoolCondReadyReasonInvalidLVMVolumeGroup))
		})

		It("sets Ready=False when NodeLabelSelector is invalid", func() {
			rsp := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
				Spec: v1alpha1.ReplicatedStoragePoolSpec{
					Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
					LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
						{Name: "lvg-1"},
					},
					NodeLabelSelector: &metav1.LabelSelector{
						MatchExpressions: []metav1.LabelSelectorRequirement{
							{
								Key:      "invalid key with spaces",
								Operator: metav1.LabelSelectorOpIn,
								Values:   []string{"value"},
							},
						},
					},
					EligibleNodesPolicy: v1alpha1.ReplicatedStoragePoolEligibleNodesPolicy{
						NotReadyGracePeriod: metav1.Duration{Duration: testGracePeriod},
					},
				},
			}
			lvg := &snc.LVMVolumeGroup{
				ObjectMeta: metav1.ObjectMeta{Name: "lvg-1"},
				Spec: snc.LVMVolumeGroupSpec{
					Local: snc.LVMVolumeGroupLocalSpec{NodeName: "node-1"},
				},
				Status: snc.LVMVolumeGroupStatus{
					Conditions: []metav1.Condition{
						{Type: "Ready", Status: metav1.ConditionTrue},
					},
				},
			}
			cl = fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rsp, lvg).
				WithStatusSubresource(rsp).
				Build()
			rec = NewReconciler(cl, logr.Discard(), "test-namespace")

			result, err := rec.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: client.ObjectKey{Name: "rsp-1"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			var updatedRSP v1alpha1.ReplicatedStoragePool
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "rsp-1"}, &updatedRSP)).To(Succeed())
			readyCond := obju.GetStatusCondition(&updatedRSP, v1alpha1.ReplicatedStoragePoolCondReadyType)
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(readyCond.Reason).To(Equal(v1alpha1.ReplicatedStoragePoolCondReadyReasonInvalidNodeLabelSelector))
			Expect(readyCond.Message).To(ContainSubstring("Invalid NodeLabelSelector"))
		})

		It("sets Ready=True and updates EligibleNodes on success", func() {
			rsp := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
				Spec: v1alpha1.ReplicatedStoragePoolSpec{
					Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
					LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
						{Name: "lvg-1"},
					},
					EligibleNodesPolicy: v1alpha1.ReplicatedStoragePoolEligibleNodesPolicy{
						NotReadyGracePeriod: metav1.Duration{Duration: testGracePeriod},
					},
				},
			}
			lvg := &snc.LVMVolumeGroup{
				ObjectMeta: metav1.ObjectMeta{Name: "lvg-1"},
				Spec: snc.LVMVolumeGroupSpec{
					Local: snc.LVMVolumeGroupLocalSpec{NodeName: "node-1"},
				},
				Status: snc.LVMVolumeGroupStatus{
					Conditions: []metav1.Condition{
						{Type: "Ready", Status: metav1.ConditionTrue},
					},
				},
			}
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-1",
					Labels: map[string]string{corev1.LabelTopologyZone: "zone-a"},
				},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
					},
				},
			}
			cl = fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rsp, lvg, node).
				WithStatusSubresource(rsp).
				Build()
			rec = NewReconciler(cl, logr.Discard(), "test-namespace")

			result, err := rec.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: client.ObjectKey{Name: "rsp-1"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			var updatedRSP v1alpha1.ReplicatedStoragePool
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "rsp-1"}, &updatedRSP)).To(Succeed())
			readyCond := obju.GetStatusCondition(&updatedRSP, v1alpha1.ReplicatedStoragePoolCondReadyType)
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
			Expect(updatedRSP.Status.EligibleNodes).To(HaveLen(1))
			Expect(updatedRSP.Status.EligibleNodes[0].NodeName).To(Equal("node-1"))
			Expect(updatedRSP.Status.EligibleNodes[0].ZoneName).To(Equal("zone-a"))
			Expect(updatedRSP.Status.EligibleNodesRevision).To(BeNumerically(">", 0))
		})

		It("increments EligibleNodesRevision when nodes change", func() {
			rsp := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
				Spec: v1alpha1.ReplicatedStoragePoolSpec{
					Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
					LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
						{Name: "lvg-1"},
					},
					EligibleNodesPolicy: v1alpha1.ReplicatedStoragePoolEligibleNodesPolicy{
						NotReadyGracePeriod: metav1.Duration{Duration: testGracePeriod},
					},
				},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					EligibleNodesRevision: 5,
					EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
						{NodeName: "node-old"},
					},
				},
			}
			lvg := &snc.LVMVolumeGroup{
				ObjectMeta: metav1.ObjectMeta{Name: "lvg-1"},
				Spec: snc.LVMVolumeGroupSpec{
					Local: snc.LVMVolumeGroupLocalSpec{NodeName: "node-1"},
				},
				Status: snc.LVMVolumeGroupStatus{
					Conditions: []metav1.Condition{
						{Type: "Ready", Status: metav1.ConditionTrue},
					},
				},
			}
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
					},
				},
			}
			cl = fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rsp, lvg, node).
				WithStatusSubresource(rsp).
				Build()
			rec = NewReconciler(cl, logr.Discard(), "test-namespace")

			result, err := rec.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: client.ObjectKey{Name: "rsp-1"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			var updatedRSP v1alpha1.ReplicatedStoragePool
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "rsp-1"}, &updatedRSP)).To(Succeed())
			Expect(updatedRSP.Status.EligibleNodesRevision).To(Equal(int64(6)))
		})

		It("requeues when grace period will expire", func() {
			rsp := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
				Spec: v1alpha1.ReplicatedStoragePoolSpec{
					Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
					LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
						{Name: "lvg-1"},
					},
					EligibleNodesPolicy: v1alpha1.ReplicatedStoragePoolEligibleNodesPolicy{
						NotReadyGracePeriod: metav1.Duration{Duration: testGracePeriod},
					},
				},
			}
			lvg := &snc.LVMVolumeGroup{
				ObjectMeta: metav1.ObjectMeta{Name: "lvg-1"},
				Spec: snc.LVMVolumeGroupSpec{
					Local: snc.LVMVolumeGroupLocalSpec{NodeName: "node-1"},
				},
				Status: snc.LVMVolumeGroupStatus{
					Conditions: []metav1.Condition{
						{Type: "Ready", Status: metav1.ConditionTrue},
					},
				},
			}
			// Node is NotReady within grace period.
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{
							Type:               corev1.NodeReady,
							Status:             corev1.ConditionFalse,
							LastTransitionTime: metav1.NewTime(time.Now().Add(-2 * time.Minute)),
						},
					},
				},
			}
			cl = fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rsp, lvg, node).
				WithStatusSubresource(rsp).
				Build()
			rec = NewReconciler(cl, logr.Discard(), "test-namespace")

			result, err := rec.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: client.ObjectKey{Name: "rsp-1"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))
			Expect(result.RequeueAfter).To(BeNumerically("<=", testGracePeriod))
		})
	})
})

var _ = Describe("getLVGsByRSP", func() {
	var (
		scheme *runtime.Scheme
		cl     client.WithWatch
		rec    *Reconciler
	)

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(snc.AddToScheme(scheme)).To(Succeed())
	})

	It("returns nil for nil RSP", func() {
		cl = fake.NewClientBuilder().WithScheme(scheme).Build()
		rec = NewReconciler(cl, logr.Discard(), "test-namespace")

		lvgs, notFoundErr, err := rec.getLVGsByRSP(context.Background(), nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(notFoundErr).To(BeNil())
		Expect(lvgs).To(BeNil())
	})

	It("returns nil for RSP with empty LVMVolumeGroups", func() {
		rsp := &v1alpha1.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
			Spec: v1alpha1.ReplicatedStoragePoolSpec{
				Type:            v1alpha1.ReplicatedStoragePoolTypeLVM,
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{},
			},
		}
		cl = fake.NewClientBuilder().WithScheme(scheme).Build()
		rec = NewReconciler(cl, logr.Discard(), "test-namespace")

		lvgs, notFoundErr, err := rec.getLVGsByRSP(context.Background(), rsp)

		Expect(err).NotTo(HaveOccurred())
		Expect(notFoundErr).To(BeNil())
		Expect(lvgs).To(BeNil())
	})

	It("returns all LVGs when all are found", func() {
		rsp := &v1alpha1.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
			Spec: v1alpha1.ReplicatedStoragePoolSpec{
				Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
					{Name: "lvg-1"},
					{Name: "lvg-2"},
				},
			},
		}
		lvg1 := &snc.LVMVolumeGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "lvg-1"},
		}
		lvg2 := &snc.LVMVolumeGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "lvg-2"},
		}
		cl = fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(lvg1, lvg2).
			Build()
		rec = NewReconciler(cl, logr.Discard(), "test-namespace")

		lvgs, notFoundErr, err := rec.getLVGsByRSP(context.Background(), rsp)

		Expect(err).NotTo(HaveOccurred())
		Expect(notFoundErr).To(BeNil())
		Expect(lvgs).To(HaveLen(2))
		Expect(lvgs).To(HaveKey("lvg-1"))
		Expect(lvgs).To(HaveKey("lvg-2"))
		Expect(lvgs["lvg-1"].name).To(Equal("lvg-1"))
		Expect(lvgs["lvg-2"].name).To(Equal("lvg-2"))
	})

	It("returns found LVGs + NotFoundErr when some LVGs are missing", func() {
		rsp := &v1alpha1.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
			Spec: v1alpha1.ReplicatedStoragePoolSpec{
				Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
					{Name: "lvg-1"},
					{Name: "lvg-missing-1"},
					{Name: "lvg-2"},
					{Name: "lvg-missing-2"},
				},
			},
		}
		lvg1 := &snc.LVMVolumeGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "lvg-1"},
		}
		lvg2 := &snc.LVMVolumeGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "lvg-2"},
		}
		cl = fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(lvg1, lvg2).
			Build()
		rec = NewReconciler(cl, logr.Discard(), "test-namespace")

		lvgs, notFoundErr, err := rec.getLVGsByRSP(context.Background(), rsp)

		Expect(err).NotTo(HaveOccurred())
		Expect(notFoundErr).To(HaveOccurred())
		Expect(notFoundErr.Error()).To(ContainSubstring("lvg-missing-1"))
		Expect(notFoundErr.Error()).To(ContainSubstring("lvg-missing-2"))
		Expect(lvgs).To(HaveLen(2))
		Expect(lvgs).To(HaveKey("lvg-1"))
		Expect(lvgs).To(HaveKey("lvg-2"))
	})

	It("returns LVGs as map keyed by name", func() {
		rsp := &v1alpha1.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
			Spec: v1alpha1.ReplicatedStoragePoolSpec{
				Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
					{Name: "lvg-c"},
					{Name: "lvg-a"},
					{Name: "lvg-b"},
				},
			},
		}
		lvgC := &snc.LVMVolumeGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "lvg-c"},
		}
		lvgA := &snc.LVMVolumeGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "lvg-a"},
		}
		lvgB := &snc.LVMVolumeGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "lvg-b"},
		}
		cl = fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(lvgC, lvgA, lvgB).
			Build()
		rec = NewReconciler(cl, logr.Discard(), "test-namespace")

		lvgs, notFoundErr, err := rec.getLVGsByRSP(context.Background(), rsp)

		Expect(err).NotTo(HaveOccurred())
		Expect(notFoundErr).To(BeNil())
		Expect(lvgs).To(HaveLen(3))
		Expect(lvgs).To(HaveKey("lvg-a"))
		Expect(lvgs).To(HaveKey("lvg-b"))
		Expect(lvgs).To(HaveKey("lvg-c"))
	})
})

var _ = Describe("applyLegacyFieldsCleared", func() {
	It("clears phase and reason when both are set", func() {
		rsp := &v1alpha1.ReplicatedStoragePool{
			Status: v1alpha1.ReplicatedStoragePoolStatus{
				Phase:  v1alpha1.RSPPhaseCompleted, //nolint:staticcheck // SA1019: testing deprecated legacy field cleanup
				Reason: "pool creation completed",
			},
		}

		changed := applyLegacyFieldsCleared(rsp)

		Expect(changed).To(BeTrue())
		Expect(rsp.Status.Phase).To(Equal(v1alpha1.ReplicatedStoragePoolPhase(""))) //nolint:staticcheck // SA1019: testing deprecated legacy field cleanup
		Expect(rsp.Status.Reason).To(BeEmpty())
	})

	It("clears only phase when reason is already empty", func() {
		rsp := &v1alpha1.ReplicatedStoragePool{
			Status: v1alpha1.ReplicatedStoragePoolStatus{
				Phase: v1alpha1.RSPPhaseFailed, //nolint:staticcheck // SA1019: testing deprecated legacy field cleanup
			},
		}

		changed := applyLegacyFieldsCleared(rsp)

		Expect(changed).To(BeTrue())
		Expect(rsp.Status.Phase).To(Equal(v1alpha1.ReplicatedStoragePoolPhase(""))) //nolint:staticcheck // SA1019: testing deprecated legacy field cleanup
	})

	It("clears only reason when phase is already empty", func() {
		rsp := &v1alpha1.ReplicatedStoragePool{
			Status: v1alpha1.ReplicatedStoragePoolStatus{
				Reason: "leftover reason",
			},
		}

		changed := applyLegacyFieldsCleared(rsp)

		Expect(changed).To(BeTrue())
		Expect(rsp.Status.Reason).To(BeEmpty())
	})

	It("reports no change when both are already empty", func() {
		rsp := &v1alpha1.ReplicatedStoragePool{
			Status: v1alpha1.ReplicatedStoragePoolStatus{},
		}

		changed := applyLegacyFieldsCleared(rsp)

		Expect(changed).To(BeFalse())
		Expect(rsp.Status.Phase).To(Equal(v1alpha1.ReplicatedStoragePoolPhase(""))) //nolint:staticcheck // SA1019: testing deprecated legacy field cleanup
		Expect(rsp.Status.Reason).To(BeEmpty())
	})
})
