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

package rsccontroller

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes/testhelpers"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

func TestRSCController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "rsc_controller Reconciler Suite")
}

var _ = Describe("computeActualEligibleNodes", func() {
	var (
		config v1alpha1.ReplicatedStorageClassConfiguration
		rsp    *v1alpha1.ReplicatedStoragePool
		lvgs   []snc.LVMVolumeGroup
		nodes  []corev1.Node
	)

	BeforeEach(func() {
		config = v1alpha1.ReplicatedStorageClassConfiguration{}
		rsp = &v1alpha1.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
			Spec: v1alpha1.ReplicatedStoragePoolSpec{
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
					{Name: "lvg-1"},
				},
			},
		}
		lvgs = []snc.LVMVolumeGroup{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "lvg-1"},
				Spec: snc.LVMVolumeGroupSpec{
					Local: snc.LVMVolumeGroupLocalSpec{
						NodeName: "node-1",
					},
				},
			},
		}
		nodes = []corev1.Node{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-1",
					Labels: map[string]string{
						corev1.LabelTopologyZone: "zone-a",
					},
				},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{
							Type:   corev1.NodeReady,
							Status: corev1.ConditionTrue,
						},
					},
				},
			},
		}
	})

	It("panics when RSP is nil", func() {
		Expect(func() {
			_, _ = computeActualEligibleNodes(config, nil, lvgs, nodes)
		}).To(Panic())
	})

	It("returns eligible node when all conditions match", func() {
		result, _ := computeActualEligibleNodes(config, rsp, lvgs, nodes)

		Expect(result).To(HaveLen(1))
		Expect(result[0].NodeName).To(Equal("node-1"))
		Expect(result[0].ZoneName).To(Equal("zone-a"))
		Expect(result[0].Ready).To(BeTrue())
		Expect(result[0].LVMVolumeGroups).To(HaveLen(1))
		Expect(result[0].LVMVolumeGroups[0].Name).To(Equal("lvg-1"))
	})

	Context("zone filtering", func() {
		It("excludes node not in specified zones", func() {
			config.Zones = []string{"zone-b", "zone-c"}

			result, _ := computeActualEligibleNodes(config, rsp, lvgs, nodes)

			Expect(result).To(BeEmpty())
		})

		It("includes node in specified zones", func() {
			config.Zones = []string{"zone-a", "zone-b"}

			result, _ := computeActualEligibleNodes(config, rsp, lvgs, nodes)

			Expect(result).To(HaveLen(1))
			Expect(result[0].NodeName).To(Equal("node-1"))
		})

		It("includes all nodes when zones is empty", func() {
			config.Zones = []string{}

			result, _ := computeActualEligibleNodes(config, rsp, lvgs, nodes)

			Expect(result).To(HaveLen(1))
		})
	})

	Context("node label selector filtering", func() {
		It("excludes node not matching selector", func() {
			config.NodeLabelSelector = &metav1.LabelSelector{
				MatchLabels: map[string]string{"storage": "fast"},
			}

			result, _ := computeActualEligibleNodes(config, rsp, lvgs, nodes)

			Expect(result).To(BeEmpty())
		})

		It("includes node matching selector", func() {
			nodes[0].Labels["storage"] = "fast"
			config.NodeLabelSelector = &metav1.LabelSelector{
				MatchLabels: map[string]string{"storage": "fast"},
			}

			result, _ := computeActualEligibleNodes(config, rsp, lvgs, nodes)

			Expect(result).To(HaveLen(1))
		})
	})

	Context("LVG matching", func() {
		It("includes node without matching LVG (client-only/tiebreaker nodes)", func() {
			rsp.Spec.LVMVolumeGroups = []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
				{Name: "lvg-2"}, // This LVG does not exist on node-1.
			}

			result, _ := computeActualEligibleNodes(config, rsp, lvgs, nodes)

			// Node is still eligible but without LVGs.
			Expect(result).To(HaveLen(1))
			Expect(result[0].NodeName).To(Equal("node-1"))
			Expect(result[0].LVMVolumeGroups).To(BeEmpty())
		})
	})

	Context("node readiness", func() {
		It("excludes node NotReady beyond grace period", func() {
			config.EligibleNodesPolicy.NotReadyGracePeriod = metav1.Duration{Duration: time.Minute}
			nodes[0].Status.Conditions = []corev1.NodeCondition{
				{
					Type:               corev1.NodeReady,
					Status:             corev1.ConditionFalse,
					LastTransitionTime: metav1.NewTime(time.Now().Add(-2 * time.Hour)),
				},
			}

			result, _ := computeActualEligibleNodes(config, rsp, lvgs, nodes)

			Expect(result).To(BeEmpty())
		})

		It("includes node NotReady within grace period", func() {
			config.EligibleNodesPolicy.NotReadyGracePeriod = metav1.Duration{Duration: time.Hour}
			nodes[0].Status.Conditions = []corev1.NodeCondition{
				{
					Type:               corev1.NodeReady,
					Status:             corev1.ConditionFalse,
					LastTransitionTime: metav1.NewTime(time.Now().Add(-30 * time.Minute)),
				},
			}

			result, _ := computeActualEligibleNodes(config, rsp, lvgs, nodes)

			Expect(result).To(HaveLen(1))
			Expect(result[0].Ready).To(BeFalse())
		})
	})

	Context("LVG unschedulable annotation", func() {
		It("marks LVG as unschedulable when annotation is present", func() {
			lvgs[0].Annotations = map[string]string{
				v1alpha1.LVMVolumeGroupUnschedulableAnnotationKey: "",
			}

			result, _ := computeActualEligibleNodes(config, rsp, lvgs, nodes)

			Expect(result).To(HaveLen(1))
			Expect(result[0].LVMVolumeGroups[0].Unschedulable).To(BeTrue())
		})
	})

	Context("node unschedulable", func() {
		It("marks node as unschedulable when spec.unschedulable is true", func() {
			nodes[0].Spec.Unschedulable = true

			result, _ := computeActualEligibleNodes(config, rsp, lvgs, nodes)

			Expect(result).To(HaveLen(1))
			Expect(result[0].Unschedulable).To(BeTrue())
		})
	})

	It("sorts eligible nodes by name", func() {
		lvgs = append(lvgs, snc.LVMVolumeGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "lvg-2"},
			Spec: snc.LVMVolumeGroupSpec{
				Local: snc.LVMVolumeGroupLocalSpec{NodeName: "node-2"},
			},
		})
		rsp.Spec.LVMVolumeGroups = append(rsp.Spec.LVMVolumeGroups, v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{Name: "lvg-2"})
		nodes = append(nodes, corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "node-2"},
			Status: corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
			},
		})

		result, _ := computeActualEligibleNodes(config, rsp, lvgs, nodes)

		Expect(result).To(HaveLen(2))
		Expect(result[0].NodeName).To(Equal("node-1"))
		Expect(result[1].NodeName).To(Equal("node-2"))
	})
})

var _ = Describe("computeActualVolumesSummary", func() {
	var rsc *v1alpha1.ReplicatedStorageClass

	BeforeEach(func() {
		rsc = &v1alpha1.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
			Status: v1alpha1.ReplicatedStorageClassStatus{
				ConfigurationGeneration: 1,
				EligibleNodesRevision:   1,
			},
		}
	})

	It("returns zero counts for empty RV list", func() {
		counters := computeActualVolumesSummary(rsc, nil)

		Expect(*counters.Total).To(Equal(int32(0)))
		Expect(*counters.Aligned).To(Equal(int32(0)))
		Expect(*counters.StaleConfiguration).To(Equal(int32(0)))
		Expect(*counters.InConflictWithEligibleNodes).To(Equal(int32(0)))
	})

	It("counts total volumes (RVs without status.storageClass are considered acknowledged)", func() {
		rvs := []v1alpha1.ReplicatedVolume{
			{ObjectMeta: metav1.ObjectMeta{Name: "rv-1"}},
			{ObjectMeta: metav1.ObjectMeta{Name: "rv-2"}},
		}

		counters := computeActualVolumesSummary(rsc, rvs)

		Expect(*counters.Total).To(Equal(int32(2)))
	})

	It("counts aligned volumes with both conditions true", func() {
		rvs := []v1alpha1.ReplicatedVolume{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Status: v1alpha1.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ReplicatedVolumeCondConfigurationReadyType,
							Status: metav1.ConditionTrue,
						},
						{
							Type:   v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesType,
							Status: metav1.ConditionTrue,
						},
					},
				},
			},
		}

		counters := computeActualVolumesSummary(rsc, rvs)

		Expect(*counters.Aligned).To(Equal(int32(1)))
	})

	It("counts configuration not aligned volumes (any ConditionFalse)", func() {
		rvs := []v1alpha1.ReplicatedVolume{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Status: v1alpha1.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ReplicatedVolumeCondConfigurationReadyType,
							Status: metav1.ConditionFalse,
						},
					},
				},
			},
		}

		counters := computeActualVolumesSummary(rsc, rvs)

		Expect(*counters.StaleConfiguration).To(Equal(int32(1)))
	})

	It("counts eligible nodes not aligned volumes (any ConditionFalse)", func() {
		rvs := []v1alpha1.ReplicatedVolume{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Status: v1alpha1.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesType,
							Status: metav1.ConditionFalse,
						},
					},
				},
			},
		}

		counters := computeActualVolumesSummary(rsc, rvs)

		Expect(*counters.InConflictWithEligibleNodes).To(Equal(int32(1)))
	})

	It("returns only total when RV has not acknowledged (mismatched configurationGeneration)", func() {
		rvs := []v1alpha1.ReplicatedVolume{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Status: v1alpha1.ReplicatedVolumeStatus{
					StorageClass: &v1alpha1.ReplicatedVolumeStorageClassReference{
						Name:                            "rsc-1",
						ObservedConfigurationGeneration: 0, // Mismatch - RSC has 1
						ObservedEligibleNodesRevision:   1,
					},
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ReplicatedVolumeCondConfigurationReadyType,
							Status: metav1.ConditionTrue,
						},
					},
				},
			},
		}

		counters := computeActualVolumesSummary(rsc, rvs)

		Expect(*counters.Total).To(Equal(int32(1)))
		Expect(counters.Aligned).To(BeNil())
		Expect(counters.StaleConfiguration).To(BeNil())
		Expect(counters.InConflictWithEligibleNodes).To(BeNil())
	})

	It("returns only total when RV has not acknowledged (mismatched eligibleNodesRevision)", func() {
		rvs := []v1alpha1.ReplicatedVolume{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Status: v1alpha1.ReplicatedVolumeStatus{
					StorageClass: &v1alpha1.ReplicatedVolumeStorageClassReference{
						Name:                            "rsc-1",
						ObservedConfigurationGeneration: 1,
						ObservedEligibleNodesRevision:   0, // Mismatch - RSC has 1
					},
				},
			},
		}

		counters := computeActualVolumesSummary(rsc, rvs)

		Expect(*counters.Total).To(Equal(int32(1)))
		Expect(counters.Aligned).To(BeNil())
	})

	It("returns all counters when all RVs have acknowledged", func() {
		rvs := []v1alpha1.ReplicatedVolume{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Status: v1alpha1.ReplicatedVolumeStatus{
					StorageClass: &v1alpha1.ReplicatedVolumeStorageClassReference{
						Name:                            "rsc-1",
						ObservedConfigurationGeneration: 1,
						ObservedEligibleNodesRevision:   1,
					},
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ReplicatedVolumeCondConfigurationReadyType,
							Status: metav1.ConditionTrue,
						},
						{
							Type:   v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesType,
							Status: metav1.ConditionTrue,
						},
					},
				},
			},
		}

		counters := computeActualVolumesSummary(rsc, rvs)

		Expect(*counters.Total).To(Equal(int32(1)))
		Expect(*counters.Aligned).To(Equal(int32(1)))
		Expect(*counters.StaleConfiguration).To(Equal(int32(0)))
		Expect(*counters.InConflictWithEligibleNodes).To(Equal(int32(0)))
	})
})

var _ = Describe("validateEligibleNodes", func() {
	// Helper to create eligible node with or without LVG.
	makeNode := func(name, zone string, hasLVG bool) v1alpha1.ReplicatedStorageClassEligibleNode {
		node := v1alpha1.ReplicatedStorageClassEligibleNode{
			NodeName: name,
			ZoneName: zone,
		}
		if hasLVG {
			node.LVMVolumeGroups = []v1alpha1.ReplicatedStorageClassEligibleNodeLVMVolumeGroup{
				{Name: "lvg-1"},
			}
		}
		return node
	}

	Describe("Replication None", func() {
		It("passes with 1 node", func() {
			config := v1alpha1.ReplicatedStorageClassConfiguration{
				Replication: v1alpha1.ReplicationNone,
			}
			nodes := []v1alpha1.ReplicatedStorageClassEligibleNode{
				makeNode("node-1", "", false),
			}

			err := validateEligibleNodes(config, nodes)

			Expect(err).NotTo(HaveOccurred())
		})

		It("fails with 0 nodes", func() {
			config := v1alpha1.ReplicatedStorageClassConfiguration{
				Replication: v1alpha1.ReplicationNone,
			}

			err := validateEligibleNodes(config, nil)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no eligible nodes"))
		})
	})

	Describe("Replication Availability - Ignored topology", func() {
		It("passes with 3 nodes, 2 with disks", func() {
			config := v1alpha1.ReplicatedStorageClassConfiguration{
				Replication: v1alpha1.ReplicationAvailability,
				Topology:    v1alpha1.RSCTopologyIgnored,
			}
			nodes := []v1alpha1.ReplicatedStorageClassEligibleNode{
				makeNode("node-1", "", true),
				makeNode("node-2", "", true),
				makeNode("node-3", "", false),
			}

			err := validateEligibleNodes(config, nodes)

			Expect(err).NotTo(HaveOccurred())
		})

		It("fails with 2 nodes", func() {
			config := v1alpha1.ReplicatedStorageClassConfiguration{
				Replication: v1alpha1.ReplicationAvailability,
				Topology:    v1alpha1.RSCTopologyIgnored,
			}
			nodes := []v1alpha1.ReplicatedStorageClassEligibleNode{
				makeNode("node-1", "", true),
				makeNode("node-2", "", true),
			}

			err := validateEligibleNodes(config, nodes)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 3 nodes"))
		})

		It("fails with 3 nodes but only 1 with disks", func() {
			config := v1alpha1.ReplicatedStorageClassConfiguration{
				Replication: v1alpha1.ReplicationAvailability,
				Topology:    v1alpha1.RSCTopologyIgnored,
			}
			nodes := []v1alpha1.ReplicatedStorageClassEligibleNode{
				makeNode("node-1", "", true),
				makeNode("node-2", "", false),
				makeNode("node-3", "", false),
			}

			err := validateEligibleNodes(config, nodes)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 2 nodes with disks"))
		})
	})

	Describe("Replication Availability - TransZonal topology", func() {
		It("passes with 3 zones, 2 with disks", func() {
			config := v1alpha1.ReplicatedStorageClassConfiguration{
				Replication: v1alpha1.ReplicationAvailability,
				Topology:    v1alpha1.RSCTopologyTransZonal,
			}
			nodes := []v1alpha1.ReplicatedStorageClassEligibleNode{
				makeNode("node-1", "zone-a", true),
				makeNode("node-2", "zone-b", true),
				makeNode("node-3", "zone-c", false),
			}

			err := validateEligibleNodes(config, nodes)

			Expect(err).NotTo(HaveOccurred())
		})

		It("fails with 2 zones", func() {
			config := v1alpha1.ReplicatedStorageClassConfiguration{
				Replication: v1alpha1.ReplicationAvailability,
				Topology:    v1alpha1.RSCTopologyTransZonal,
			}
			nodes := []v1alpha1.ReplicatedStorageClassEligibleNode{
				makeNode("node-1", "zone-a", true),
				makeNode("node-2", "zone-b", true),
			}

			err := validateEligibleNodes(config, nodes)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 3 zones"))
		})

		It("fails with 3 zones but only 1 with disks", func() {
			config := v1alpha1.ReplicatedStorageClassConfiguration{
				Replication: v1alpha1.ReplicationAvailability,
				Topology:    v1alpha1.RSCTopologyTransZonal,
			}
			nodes := []v1alpha1.ReplicatedStorageClassEligibleNode{
				makeNode("node-1", "zone-a", true),
				makeNode("node-2", "zone-b", false),
				makeNode("node-3", "zone-c", false),
			}

			err := validateEligibleNodes(config, nodes)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 2 zones with disks"))
		})
	})

	Describe("Replication Availability - Zonal topology", func() {
		It("passes with per zone: 3 nodes, 2 with disks", func() {
			config := v1alpha1.ReplicatedStorageClassConfiguration{
				Replication: v1alpha1.ReplicationAvailability,
				Topology:    v1alpha1.RSCTopologyZonal,
			}
			nodes := []v1alpha1.ReplicatedStorageClassEligibleNode{
				makeNode("node-1a", "zone-a", true),
				makeNode("node-2a", "zone-a", true),
				makeNode("node-3a", "zone-a", false),
			}

			err := validateEligibleNodes(config, nodes)

			Expect(err).NotTo(HaveOccurred())
		})

		It("fails when zone has only 2 nodes", func() {
			config := v1alpha1.ReplicatedStorageClassConfiguration{
				Replication: v1alpha1.ReplicationAvailability,
				Topology:    v1alpha1.RSCTopologyZonal,
			}
			nodes := []v1alpha1.ReplicatedStorageClassEligibleNode{
				makeNode("node-1a", "zone-a", true),
				makeNode("node-2a", "zone-a", true),
			}

			err := validateEligibleNodes(config, nodes)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 3 nodes in each zone"))
		})

		It("fails when zone has 3 nodes but only 1 with disks", func() {
			config := v1alpha1.ReplicatedStorageClassConfiguration{
				Replication: v1alpha1.ReplicationAvailability,
				Topology:    v1alpha1.RSCTopologyZonal,
			}
			nodes := []v1alpha1.ReplicatedStorageClassEligibleNode{
				makeNode("node-1a", "zone-a", true),
				makeNode("node-2a", "zone-a", false),
				makeNode("node-3a", "zone-a", false),
			}

			err := validateEligibleNodes(config, nodes)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 2 nodes with disks in each zone"))
		})
	})

	Describe("Replication Consistency - Ignored topology", func() {
		It("passes with 2 nodes both with disks", func() {
			config := v1alpha1.ReplicatedStorageClassConfiguration{
				Replication: v1alpha1.ReplicationConsistency,
				Topology:    v1alpha1.RSCTopologyIgnored,
			}
			nodes := []v1alpha1.ReplicatedStorageClassEligibleNode{
				makeNode("node-1", "", true),
				makeNode("node-2", "", true),
			}

			err := validateEligibleNodes(config, nodes)

			Expect(err).NotTo(HaveOccurred())
		})

		It("fails with 1 node with disks", func() {
			config := v1alpha1.ReplicatedStorageClassConfiguration{
				Replication: v1alpha1.ReplicationConsistency,
				Topology:    v1alpha1.RSCTopologyIgnored,
			}
			nodes := []v1alpha1.ReplicatedStorageClassEligibleNode{
				makeNode("node-1", "", true),
			}

			err := validateEligibleNodes(config, nodes)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 2 nodes"))
		})

		It("fails with 2 nodes but only 1 with disks", func() {
			config := v1alpha1.ReplicatedStorageClassConfiguration{
				Replication: v1alpha1.ReplicationConsistency,
				Topology:    v1alpha1.RSCTopologyIgnored,
			}
			nodes := []v1alpha1.ReplicatedStorageClassEligibleNode{
				makeNode("node-1", "", true),
				makeNode("node-2", "", false),
			}

			err := validateEligibleNodes(config, nodes)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 2 nodes with disks"))
		})
	})

	Describe("Replication Consistency - TransZonal topology", func() {
		It("passes with 2 zones with disks", func() {
			config := v1alpha1.ReplicatedStorageClassConfiguration{
				Replication: v1alpha1.ReplicationConsistency,
				Topology:    v1alpha1.RSCTopologyTransZonal,
			}
			nodes := []v1alpha1.ReplicatedStorageClassEligibleNode{
				makeNode("node-1", "zone-a", true),
				makeNode("node-2", "zone-b", true),
			}

			err := validateEligibleNodes(config, nodes)

			Expect(err).NotTo(HaveOccurred())
		})

		It("fails with 1 zone with disks", func() {
			config := v1alpha1.ReplicatedStorageClassConfiguration{
				Replication: v1alpha1.ReplicationConsistency,
				Topology:    v1alpha1.RSCTopologyTransZonal,
			}
			nodes := []v1alpha1.ReplicatedStorageClassEligibleNode{
				makeNode("node-1", "zone-a", true),
				makeNode("node-2", "zone-b", false),
			}

			err := validateEligibleNodes(config, nodes)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 2 zones with disks"))
		})
	})

	Describe("Replication Consistency - Zonal topology", func() {
		It("passes with per zone: 2 nodes with disks", func() {
			config := v1alpha1.ReplicatedStorageClassConfiguration{
				Replication: v1alpha1.ReplicationConsistency,
				Topology:    v1alpha1.RSCTopologyZonal,
			}
			nodes := []v1alpha1.ReplicatedStorageClassEligibleNode{
				makeNode("node-1a", "zone-a", true),
				makeNode("node-2a", "zone-a", true),
			}

			err := validateEligibleNodes(config, nodes)

			Expect(err).NotTo(HaveOccurred())
		})

		It("fails when zone has 1 node with disks", func() {
			config := v1alpha1.ReplicatedStorageClassConfiguration{
				Replication: v1alpha1.ReplicationConsistency,
				Topology:    v1alpha1.RSCTopologyZonal,
			}
			nodes := []v1alpha1.ReplicatedStorageClassEligibleNode{
				makeNode("node-1a", "zone-a", true),
				makeNode("node-2a", "zone-a", false),
			}

			err := validateEligibleNodes(config, nodes)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 2 nodes with disks in each zone"))
		})
	})

	Describe("Replication ConsistencyAndAvailability - Ignored topology", func() {
		It("passes with 3 nodes with disks", func() {
			config := v1alpha1.ReplicatedStorageClassConfiguration{
				Replication: v1alpha1.ReplicationConsistencyAndAvailability,
				Topology:    v1alpha1.RSCTopologyIgnored,
			}
			nodes := []v1alpha1.ReplicatedStorageClassEligibleNode{
				makeNode("node-1", "", true),
				makeNode("node-2", "", true),
				makeNode("node-3", "", true),
			}

			err := validateEligibleNodes(config, nodes)

			Expect(err).NotTo(HaveOccurred())
		})

		It("fails with 2 nodes with disks", func() {
			config := v1alpha1.ReplicatedStorageClassConfiguration{
				Replication: v1alpha1.ReplicationConsistencyAndAvailability,
				Topology:    v1alpha1.RSCTopologyIgnored,
			}
			nodes := []v1alpha1.ReplicatedStorageClassEligibleNode{
				makeNode("node-1", "", true),
				makeNode("node-2", "", true),
			}

			err := validateEligibleNodes(config, nodes)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 3 nodes with disks"))
		})
	})

	Describe("Replication ConsistencyAndAvailability - TransZonal topology", func() {
		It("passes with 3 zones with disks", func() {
			config := v1alpha1.ReplicatedStorageClassConfiguration{
				Replication: v1alpha1.ReplicationConsistencyAndAvailability,
				Topology:    v1alpha1.RSCTopologyTransZonal,
			}
			nodes := []v1alpha1.ReplicatedStorageClassEligibleNode{
				makeNode("node-1", "zone-a", true),
				makeNode("node-2", "zone-b", true),
				makeNode("node-3", "zone-c", true),
			}

			err := validateEligibleNodes(config, nodes)

			Expect(err).NotTo(HaveOccurred())
		})

		It("fails with 2 zones with disks", func() {
			config := v1alpha1.ReplicatedStorageClassConfiguration{
				Replication: v1alpha1.ReplicationConsistencyAndAvailability,
				Topology:    v1alpha1.RSCTopologyTransZonal,
			}
			nodes := []v1alpha1.ReplicatedStorageClassEligibleNode{
				makeNode("node-1", "zone-a", true),
				makeNode("node-2", "zone-b", true),
			}

			err := validateEligibleNodes(config, nodes)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 3 zones with disks"))
		})
	})

	Describe("Replication ConsistencyAndAvailability - Zonal topology", func() {
		It("passes with per zone: 3 nodes with disks", func() {
			config := v1alpha1.ReplicatedStorageClassConfiguration{
				Replication: v1alpha1.ReplicationConsistencyAndAvailability,
				Topology:    v1alpha1.RSCTopologyZonal,
			}
			nodes := []v1alpha1.ReplicatedStorageClassEligibleNode{
				makeNode("node-1a", "zone-a", true),
				makeNode("node-2a", "zone-a", true),
				makeNode("node-3a", "zone-a", true),
			}

			err := validateEligibleNodes(config, nodes)

			Expect(err).NotTo(HaveOccurred())
		})

		It("fails when zone has 2 nodes with disks", func() {
			config := v1alpha1.ReplicatedStorageClassConfiguration{
				Replication: v1alpha1.ReplicationConsistencyAndAvailability,
				Topology:    v1alpha1.RSCTopologyZonal,
			}
			nodes := []v1alpha1.ReplicatedStorageClassEligibleNode{
				makeNode("node-1a", "zone-a", true),
				makeNode("node-2a", "zone-a", true),
			}

			err := validateEligibleNodes(config, nodes)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 3 nodes with disks in each zone"))
		})
	})
})

var _ = Describe("validateConfiguration", func() {
	It("returns nil for nil NodeLabelSelector", func() {
		config := v1alpha1.ReplicatedStorageClassConfiguration{}

		err := validateConfiguration(config)

		Expect(err).NotTo(HaveOccurred())
	})

	It("returns nil for valid NodeLabelSelector", func() {
		config := v1alpha1.ReplicatedStorageClassConfiguration{
			NodeLabelSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"env": "prod"},
			},
		}

		err := validateConfiguration(config)

		Expect(err).NotTo(HaveOccurred())
	})

	It("returns error for invalid NodeLabelSelector", func() {
		config := v1alpha1.ReplicatedStorageClassConfiguration{
			NodeLabelSelector: &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{
						Key:      "key",
						Operator: "InvalidOp",
					},
				},
			},
		}

		err := validateConfiguration(config)

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("invalid NodeLabelSelector"))
	})
})

var _ = Describe("validateRSPAndLVGs", func() {
	It("returns nil when type is not LVMThin", func() {
		rsp := &v1alpha1.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
			Spec: v1alpha1.ReplicatedStoragePoolSpec{
				Type: v1alpha1.RSPTypeLVM,
			},
		}

		err := validateRSPAndLVGs(rsp, nil)

		Expect(err).NotTo(HaveOccurred())
	})

	It("returns error for LVMThin when thinPoolName is empty", func() {
		rsp := &v1alpha1.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
			Spec: v1alpha1.ReplicatedStoragePoolSpec{
				Type: v1alpha1.RSPTypeLVMThin,
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
					{Name: "lvg-1", ThinPoolName: ""},
				},
			},
		}
		lvgs := []snc.LVMVolumeGroup{
			{ObjectMeta: metav1.ObjectMeta{Name: "lvg-1"}},
		}

		err := validateRSPAndLVGs(rsp, lvgs)

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("thinPoolName is required"))
	})

	It("returns error for LVMThin when thinPool not found in LVG", func() {
		rsp := &v1alpha1.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
			Spec: v1alpha1.ReplicatedStoragePoolSpec{
				Type: v1alpha1.RSPTypeLVMThin,
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
					{Name: "lvg-1", ThinPoolName: "missing-pool"},
				},
			},
		}
		lvgs := []snc.LVMVolumeGroup{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "lvg-1"},
				Spec: snc.LVMVolumeGroupSpec{
					ThinPools: []snc.LVMVolumeGroupThinPoolSpec{
						{Name: "other-pool"},
					},
				},
			},
		}

		err := validateRSPAndLVGs(rsp, lvgs)

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not found in Spec.ThinPools"))
	})

	It("returns nil when all validations pass for LVMThin", func() {
		rsp := &v1alpha1.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
			Spec: v1alpha1.ReplicatedStoragePoolSpec{
				Type: v1alpha1.RSPTypeLVMThin,
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
					{Name: "lvg-1", ThinPoolName: "my-pool"},
				},
			},
		}
		lvgs := []snc.LVMVolumeGroup{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "lvg-1"},
				Spec: snc.LVMVolumeGroupSpec{
					ThinPools: []snc.LVMVolumeGroupThinPoolSpec{
						{Name: "my-pool"},
					},
				},
			},
		}

		err := validateRSPAndLVGs(rsp, lvgs)

		Expect(err).NotTo(HaveOccurred())
	})

	It("panics when LVG referenced by RSP is not in lvgByName map", func() {
		rsp := &v1alpha1.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
			Spec: v1alpha1.ReplicatedStoragePoolSpec{
				Type: v1alpha1.RSPTypeLVMThin,
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
					{Name: "missing-lvg", ThinPoolName: "my-pool"},
				},
			},
		}
		lvgs := []snc.LVMVolumeGroup{} // Empty - missing LVG

		Expect(func() {
			_ = validateRSPAndLVGs(rsp, lvgs)
		}).To(Panic())
	})
})

var _ = Describe("isConfigurationInSync", func() {
	It("returns false when Status.Configuration is nil", func() {
		rsc := &v1alpha1.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{Generation: 1},
			Status:     v1alpha1.ReplicatedStorageClassStatus{},
		}

		result := isConfigurationInSync(rsc)

		Expect(result).To(BeFalse())
	})

	It("returns false when ConfigurationGeneration != Generation", func() {
		rsc := &v1alpha1.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{Generation: 2},
			Status: v1alpha1.ReplicatedStorageClassStatus{
				Configuration:           &v1alpha1.ReplicatedStorageClassConfiguration{},
				ConfigurationGeneration: 1,
			},
		}

		result := isConfigurationInSync(rsc)

		Expect(result).To(BeFalse())
	})

	It("returns true when ConfigurationGeneration == Generation", func() {
		rsc := &v1alpha1.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{Generation: 5},
			Status: v1alpha1.ReplicatedStorageClassStatus{
				Configuration:           &v1alpha1.ReplicatedStorageClassConfiguration{},
				ConfigurationGeneration: 5,
			},
		}

		result := isConfigurationInSync(rsc)

		Expect(result).To(BeTrue())
	})
})

var _ = Describe("areEligibleNodesInSyncWithTheWorld", func() {
	It("returns false when EligibleNodesWorldState is nil", func() {
		rsc := &v1alpha1.ReplicatedStorageClass{
			Status: v1alpha1.ReplicatedStorageClassStatus{},
		}

		result := areEligibleNodesInSyncWithTheWorld(rsc, "abc123")

		Expect(result).To(BeFalse())
	})

	It("returns false when checksums don't match", func() {
		rsc := &v1alpha1.ReplicatedStorageClass{
			Status: v1alpha1.ReplicatedStorageClassStatus{
				EligibleNodesWorldState: &v1alpha1.ReplicatedStorageClassEligibleNodesWorldState{
					Checksum:  "different",
					ExpiresAt: metav1.NewTime(time.Now().Add(time.Hour)),
				},
			},
		}

		result := areEligibleNodesInSyncWithTheWorld(rsc, "abc123")

		Expect(result).To(BeFalse())
	})

	It("returns false when state has expired", func() {
		rsc := &v1alpha1.ReplicatedStorageClass{
			Status: v1alpha1.ReplicatedStorageClassStatus{
				EligibleNodesWorldState: &v1alpha1.ReplicatedStorageClassEligibleNodesWorldState{
					Checksum:  "abc123",
					ExpiresAt: metav1.NewTime(time.Now().Add(-time.Hour)), // Expired
				},
			},
		}

		result := areEligibleNodesInSyncWithTheWorld(rsc, "abc123")

		Expect(result).To(BeFalse())
	})

	It("returns true when checksum matches and not expired", func() {
		rsc := &v1alpha1.ReplicatedStorageClass{
			Status: v1alpha1.ReplicatedStorageClassStatus{
				EligibleNodesWorldState: &v1alpha1.ReplicatedStorageClassEligibleNodesWorldState{
					Checksum:  "abc123",
					ExpiresAt: metav1.NewTime(time.Now().Add(time.Hour)),
				},
			},
		}

		result := areEligibleNodesInSyncWithTheWorld(rsc, "abc123")

		Expect(result).To(BeTrue())
	})
})

var _ = Describe("computeRollingStrategiesConfiguration", func() {
	It("returns (0, 0) when both policies are not RollingUpdate/RollingRepair", func() {
		rsc := &v1alpha1.ReplicatedStorageClass{
			Spec: v1alpha1.ReplicatedStorageClassSpec{
				ConfigurationRolloutStrategy: v1alpha1.ReplicatedStorageClassConfigurationRolloutStrategy{
					Type: v1alpha1.ReplicatedStorageClassConfigurationRolloutStrategyTypeNewVolumesOnly,
				},
				EligibleNodesConflictResolutionStrategy: v1alpha1.ReplicatedStorageClassEligibleNodesConflictResolutionStrategy{
					Type: v1alpha1.ReplicatedStorageClassEligibleNodesConflictResolutionStrategyTypeManual,
				},
			},
		}

		rollouts, conflicts := computeRollingStrategiesConfiguration(rsc)

		Expect(rollouts).To(Equal(int32(0)))
		Expect(conflicts).To(Equal(int32(0)))
	})

	It("returns maxParallel for rollouts when ConfigurationRolloutStrategy is RollingUpdate", func() {
		rsc := &v1alpha1.ReplicatedStorageClass{
			Spec: v1alpha1.ReplicatedStorageClassSpec{
				ConfigurationRolloutStrategy: v1alpha1.ReplicatedStorageClassConfigurationRolloutStrategy{
					Type: v1alpha1.ReplicatedStorageClassConfigurationRolloutStrategyTypeRollingUpdate,
					RollingUpdate: &v1alpha1.ReplicatedStorageClassConfigurationRollingUpdateStrategy{
						MaxParallel: 5,
					},
				},
				EligibleNodesConflictResolutionStrategy: v1alpha1.ReplicatedStorageClassEligibleNodesConflictResolutionStrategy{
					Type: v1alpha1.ReplicatedStorageClassEligibleNodesConflictResolutionStrategyTypeManual,
				},
			},
		}

		rollouts, conflicts := computeRollingStrategiesConfiguration(rsc)

		Expect(rollouts).To(Equal(int32(5)))
		Expect(conflicts).To(Equal(int32(0)))
	})

	It("returns maxParallel for conflicts when EligibleNodesConflictResolutionStrategy is RollingRepair", func() {
		rsc := &v1alpha1.ReplicatedStorageClass{
			Spec: v1alpha1.ReplicatedStorageClassSpec{
				ConfigurationRolloutStrategy: v1alpha1.ReplicatedStorageClassConfigurationRolloutStrategy{
					Type: v1alpha1.ReplicatedStorageClassConfigurationRolloutStrategyTypeNewVolumesOnly,
				},
				EligibleNodesConflictResolutionStrategy: v1alpha1.ReplicatedStorageClassEligibleNodesConflictResolutionStrategy{
					Type: v1alpha1.ReplicatedStorageClassEligibleNodesConflictResolutionStrategyTypeRollingRepair,
					RollingRepair: &v1alpha1.ReplicatedStorageClassEligibleNodesConflictResolutionRollingRepair{
						MaxParallel: 10,
					},
				},
			},
		}

		rollouts, conflicts := computeRollingStrategiesConfiguration(rsc)

		Expect(rollouts).To(Equal(int32(0)))
		Expect(conflicts).To(Equal(int32(10)))
	})

	It("returns both maxParallel values when both policies are rolling", func() {
		rsc := &v1alpha1.ReplicatedStorageClass{
			Spec: v1alpha1.ReplicatedStorageClassSpec{
				ConfigurationRolloutStrategy: v1alpha1.ReplicatedStorageClassConfigurationRolloutStrategy{
					Type: v1alpha1.ReplicatedStorageClassConfigurationRolloutStrategyTypeRollingUpdate,
					RollingUpdate: &v1alpha1.ReplicatedStorageClassConfigurationRollingUpdateStrategy{
						MaxParallel: 3,
					},
				},
				EligibleNodesConflictResolutionStrategy: v1alpha1.ReplicatedStorageClassEligibleNodesConflictResolutionStrategy{
					Type: v1alpha1.ReplicatedStorageClassEligibleNodesConflictResolutionStrategyTypeRollingRepair,
					RollingRepair: &v1alpha1.ReplicatedStorageClassEligibleNodesConflictResolutionRollingRepair{
						MaxParallel: 7,
					},
				},
			},
		}

		rollouts, conflicts := computeRollingStrategiesConfiguration(rsc)

		Expect(rollouts).To(Equal(int32(3)))
		Expect(conflicts).To(Equal(int32(7)))
	})

	It("panics when ConfigurationRolloutStrategy is RollingUpdate but RollingUpdate config is nil", func() {
		rsc := &v1alpha1.ReplicatedStorageClass{
			Spec: v1alpha1.ReplicatedStorageClassSpec{
				ConfigurationRolloutStrategy: v1alpha1.ReplicatedStorageClassConfigurationRolloutStrategy{
					Type:          v1alpha1.ReplicatedStorageClassConfigurationRolloutStrategyTypeRollingUpdate,
					RollingUpdate: nil,
				},
				EligibleNodesConflictResolutionStrategy: v1alpha1.ReplicatedStorageClassEligibleNodesConflictResolutionStrategy{
					Type: v1alpha1.ReplicatedStorageClassEligibleNodesConflictResolutionStrategyTypeManual,
				},
			},
		}

		Expect(func() {
			computeRollingStrategiesConfiguration(rsc)
		}).To(Panic())
	})

	It("panics when EligibleNodesConflictResolutionStrategy is RollingRepair but RollingRepair config is nil", func() {
		rsc := &v1alpha1.ReplicatedStorageClass{
			Spec: v1alpha1.ReplicatedStorageClassSpec{
				ConfigurationRolloutStrategy: v1alpha1.ReplicatedStorageClassConfigurationRolloutStrategy{
					Type: v1alpha1.ReplicatedStorageClassConfigurationRolloutStrategyTypeNewVolumesOnly,
				},
				EligibleNodesConflictResolutionStrategy: v1alpha1.ReplicatedStorageClassEligibleNodesConflictResolutionStrategy{
					Type:          v1alpha1.ReplicatedStorageClassEligibleNodesConflictResolutionStrategyTypeRollingRepair,
					RollingRepair: nil,
				},
			},
		}

		Expect(func() {
			computeRollingStrategiesConfiguration(rsc)
		}).To(Panic())
	})
})

var _ = Describe("ensureVolumeConditions", func() {
	var (
		ctx context.Context
		rsc *v1alpha1.ReplicatedStorageClass
	)

	BeforeEach(func() {
		ctx = flow.BeginRootReconcile(context.Background()).Ctx()
		rsc = &v1alpha1.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-rsc",
			},
			Spec: v1alpha1.ReplicatedStorageClassSpec{
				ConfigurationRolloutStrategy: v1alpha1.ReplicatedStorageClassConfigurationRolloutStrategy{
					Type: v1alpha1.ReplicatedStorageClassConfigurationRolloutStrategyTypeNewVolumesOnly,
				},
				EligibleNodesConflictResolutionStrategy: v1alpha1.ReplicatedStorageClassEligibleNodesConflictResolutionStrategy{
					Type: v1alpha1.ReplicatedStorageClassEligibleNodesConflictResolutionStrategyTypeManual,
				},
			},
		}
	})

	It("panics when PendingObservation is nil", func() {
		rsc.Status.Volumes = v1alpha1.ReplicatedStorageClassVolumesSummary{
			PendingObservation: nil,
		}

		Expect(func() {
			ensureVolumeConditions(ctx, rsc, nil)
		}).To(Panic())
	})

	It("sets both conditions to Unknown when PendingObservation > 0", func() {
		rsc.Status.Volumes = v1alpha1.ReplicatedStorageClassVolumesSummary{
			PendingObservation: ptr.To(int32(3)),
		}

		outcome := ensureVolumeConditions(ctx, rsc, nil)

		Expect(outcome.Error()).To(BeNil())
		Expect(outcome.DidChange()).To(BeTrue())

		configCond := obju.GetStatusCondition(rsc, v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutType)
		Expect(configCond).NotTo(BeNil())
		Expect(configCond.Status).To(Equal(metav1.ConditionUnknown))
		Expect(configCond.Reason).To(Equal(v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutReasonNewConfigurationNotYetObserved))
		Expect(configCond.Message).To(ContainSubstring("3 volume(s) pending observation"))

		nodesCond := obju.GetStatusCondition(rsc, v1alpha1.ReplicatedStorageClassCondVolumesSatisfyEligibleNodesType)
		Expect(nodesCond).NotTo(BeNil())
		Expect(nodesCond.Status).To(Equal(metav1.ConditionUnknown))
		Expect(nodesCond.Reason).To(Equal(v1alpha1.ReplicatedStorageClassCondVolumesSatisfyEligibleNodesReasonUpdatedEligibleNodesNotYetObserved))
	})

	It("panics when StaleConfiguration is nil (after PendingObservation check passes)", func() {
		rsc.Status.Volumes = v1alpha1.ReplicatedStorageClassVolumesSummary{
			PendingObservation:          ptr.To(int32(0)),
			StaleConfiguration:          nil,
			InConflictWithEligibleNodes: ptr.To(int32(0)),
		}

		Expect(func() {
			ensureVolumeConditions(ctx, rsc, nil)
		}).To(Panic())
	})

	It("panics when InConflictWithEligibleNodes is nil (after PendingObservation check passes)", func() {
		rsc.Status.Volumes = v1alpha1.ReplicatedStorageClassVolumesSummary{
			PendingObservation:          ptr.To(int32(0)),
			StaleConfiguration:          ptr.To(int32(0)),
			InConflictWithEligibleNodes: nil,
		}

		Expect(func() {
			ensureVolumeConditions(ctx, rsc, nil)
		}).To(Panic())
	})

	It("sets ConfigurationRolledOut to False when StaleConfiguration > 0", func() {
		rsc.Status.Volumes = v1alpha1.ReplicatedStorageClassVolumesSummary{
			PendingObservation:          ptr.To(int32(0)),
			StaleConfiguration:          ptr.To(int32(2)),
			InConflictWithEligibleNodes: ptr.To(int32(0)),
		}

		outcome := ensureVolumeConditions(ctx, rsc, nil)

		Expect(outcome.Error()).To(BeNil())
		Expect(outcome.DidChange()).To(BeTrue())

		configCond := obju.GetStatusCondition(rsc, v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutType)
		Expect(configCond).NotTo(BeNil())
		Expect(configCond.Status).To(Equal(metav1.ConditionFalse))
		Expect(configCond.Reason).To(Equal(v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutReasonConfigurationRolloutDisabled))
	})

	It("sets ConfigurationRolledOut to True when StaleConfiguration == 0", func() {
		rsc.Status.Volumes = v1alpha1.ReplicatedStorageClassVolumesSummary{
			PendingObservation:          ptr.To(int32(0)),
			StaleConfiguration:          ptr.To(int32(0)),
			InConflictWithEligibleNodes: ptr.To(int32(0)),
		}

		outcome := ensureVolumeConditions(ctx, rsc, nil)

		Expect(outcome.Error()).To(BeNil())
		Expect(outcome.DidChange()).To(BeTrue())

		configCond := obju.GetStatusCondition(rsc, v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutType)
		Expect(configCond).NotTo(BeNil())
		Expect(configCond.Status).To(Equal(metav1.ConditionTrue))
		Expect(configCond.Reason).To(Equal(v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutReasonRolledOutToAllVolumes))
	})

	It("sets VolumesSatisfyEligibleNodes to False when InConflictWithEligibleNodes > 0", func() {
		rsc.Status.Volumes = v1alpha1.ReplicatedStorageClassVolumesSummary{
			PendingObservation:          ptr.To(int32(0)),
			StaleConfiguration:          ptr.To(int32(0)),
			InConflictWithEligibleNodes: ptr.To(int32(5)),
		}

		outcome := ensureVolumeConditions(ctx, rsc, nil)

		Expect(outcome.Error()).To(BeNil())
		Expect(outcome.DidChange()).To(BeTrue())

		nodesCond := obju.GetStatusCondition(rsc, v1alpha1.ReplicatedStorageClassCondVolumesSatisfyEligibleNodesType)
		Expect(nodesCond).NotTo(BeNil())
		Expect(nodesCond.Status).To(Equal(metav1.ConditionFalse))
		Expect(nodesCond.Reason).To(Equal(v1alpha1.ReplicatedStorageClassCondVolumesSatisfyEligibleNodesReasonManualConflictResolution))
	})

	It("sets VolumesSatisfyEligibleNodes to True when InConflictWithEligibleNodes == 0", func() {
		rsc.Status.Volumes = v1alpha1.ReplicatedStorageClassVolumesSummary{
			PendingObservation:          ptr.To(int32(0)),
			StaleConfiguration:          ptr.To(int32(0)),
			InConflictWithEligibleNodes: ptr.To(int32(0)),
		}

		outcome := ensureVolumeConditions(ctx, rsc, nil)

		Expect(outcome.Error()).To(BeNil())
		Expect(outcome.DidChange()).To(BeTrue())

		nodesCond := obju.GetStatusCondition(rsc, v1alpha1.ReplicatedStorageClassCondVolumesSatisfyEligibleNodesType)
		Expect(nodesCond).NotTo(BeNil())
		Expect(nodesCond.Status).To(Equal(metav1.ConditionTrue))
		Expect(nodesCond.Reason).To(Equal(v1alpha1.ReplicatedStorageClassCondVolumesSatisfyEligibleNodesReasonAllVolumesSatisfy))
	})

	It("sets both conditions correctly when StaleConfiguration > 0 and InConflictWithEligibleNodes > 0", func() {
		rsc.Status.Volumes = v1alpha1.ReplicatedStorageClassVolumesSummary{
			PendingObservation:          ptr.To(int32(0)),
			StaleConfiguration:          ptr.To(int32(2)),
			InConflictWithEligibleNodes: ptr.To(int32(3)),
		}

		outcome := ensureVolumeConditions(ctx, rsc, nil)

		Expect(outcome.Error()).To(BeNil())
		Expect(outcome.DidChange()).To(BeTrue())

		configCond := obju.GetStatusCondition(rsc, v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutType)
		Expect(configCond).NotTo(BeNil())
		Expect(configCond.Status).To(Equal(metav1.ConditionFalse))
		Expect(configCond.Reason).To(Equal(v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutReasonConfigurationRolloutDisabled))

		nodesCond := obju.GetStatusCondition(rsc, v1alpha1.ReplicatedStorageClassCondVolumesSatisfyEligibleNodesType)
		Expect(nodesCond).NotTo(BeNil())
		Expect(nodesCond.Status).To(Equal(metav1.ConditionFalse))
		Expect(nodesCond.Reason).To(Equal(v1alpha1.ReplicatedStorageClassCondVolumesSatisfyEligibleNodesReasonManualConflictResolution))
	})

	It("reports no change when conditions already match the target state", func() {
		rsc.Status.Volumes = v1alpha1.ReplicatedStorageClassVolumesSummary{
			PendingObservation:          ptr.To(int32(0)),
			StaleConfiguration:          ptr.To(int32(0)),
			InConflictWithEligibleNodes: ptr.To(int32(0)),
		}

		// First call to set conditions
		outcome := ensureVolumeConditions(ctx, rsc, nil)
		Expect(outcome.DidChange()).To(BeTrue())

		// Second call should report no change
		outcome = ensureVolumeConditions(ctx, rsc, nil)
		Expect(outcome.Error()).To(BeNil())
		Expect(outcome.DidChange()).To(BeFalse())
	})
})

var _ = Describe("makeConfiguration", func() {
	It("copies all fields from spec correctly", func() {
		rsc := &v1alpha1.ReplicatedStorageClass{
			Spec: v1alpha1.ReplicatedStorageClassSpec{
				Topology:           v1alpha1.RSCTopologyTransZonal,
				Replication:        v1alpha1.ReplicationAvailability,
				VolumeAccess:       v1alpha1.VolumeAccessLocal,
				Zones:              []string{"zone-c", "zone-a", "zone-b"},
				SystemNetworkNames: []string{"net-b", "net-a"},
				EligibleNodesPolicy: v1alpha1.ReplicatedStorageClassEligibleNodesPolicy{
					NotReadyGracePeriod: metav1.Duration{Duration: 5 * time.Minute},
				},
			},
		}

		config := makeConfiguration(rsc)

		Expect(config.Topology).To(Equal(v1alpha1.RSCTopologyTransZonal))
		Expect(config.Replication).To(Equal(v1alpha1.ReplicationAvailability))
		Expect(config.VolumeAccess).To(Equal(v1alpha1.VolumeAccessLocal))
		Expect(config.EligibleNodesPolicy.NotReadyGracePeriod.Duration).To(Equal(5 * time.Minute))
	})

	It("sorts Zones slice", func() {
		rsc := &v1alpha1.ReplicatedStorageClass{
			Spec: v1alpha1.ReplicatedStorageClassSpec{
				Zones: []string{"zone-c", "zone-a", "zone-b"},
			},
		}

		config := makeConfiguration(rsc)

		Expect(config.Zones).To(Equal([]string{"zone-a", "zone-b", "zone-c"}))
	})

	It("sorts SystemNetworkNames slice", func() {
		rsc := &v1alpha1.ReplicatedStorageClass{
			Spec: v1alpha1.ReplicatedStorageClassSpec{
				SystemNetworkNames: []string{"net-b", "net-a", "net-c"},
			},
		}

		config := makeConfiguration(rsc)

		Expect(config.SystemNetworkNames).To(Equal([]string{"net-a", "net-b", "net-c"}))
	})

	It("deep copies NodeLabelSelector (not shared reference)", func() {
		rsc := &v1alpha1.ReplicatedStorageClass{
			Spec: v1alpha1.ReplicatedStorageClassSpec{
				NodeLabelSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"env": "prod"},
				},
			},
		}

		config := makeConfiguration(rsc)

		// Modify original - config should not change.
		rsc.Spec.NodeLabelSelector.MatchLabels["env"] = "dev"

		Expect(config.NodeLabelSelector).NotTo(BeNil())
		Expect(config.NodeLabelSelector.MatchLabels["env"]).To(Equal("prod"))
	})

	It("handles nil NodeLabelSelector", func() {
		rsc := &v1alpha1.ReplicatedStorageClass{
			Spec: v1alpha1.ReplicatedStorageClassSpec{
				NodeLabelSelector: nil,
			},
		}

		config := makeConfiguration(rsc)

		Expect(config.NodeLabelSelector).To(BeNil())
	})
})

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
		It("does nothing when RSC is not found", func() {
			cl = fake.NewClientBuilder().WithScheme(scheme).Build()
			rec = NewReconciler(cl)

			result, err := rec.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: client.ObjectKey{Name: "rsc-1"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
		})

		It("updates status with eligible nodes when all resources exist", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					StoragePool: "rsp-1",
				},
			}
			rsp := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
				Spec: v1alpha1.ReplicatedStoragePoolSpec{
					LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
						{Name: "lvg-1"},
					},
				},
			}
			lvg := &snc.LVMVolumeGroup{
				ObjectMeta: metav1.ObjectMeta{Name: "lvg-1"},
				Spec: snc.LVMVolumeGroupSpec{
					Local: snc.LVMVolumeGroupLocalSpec{NodeName: "node-1"},
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
			cl = testhelpers.WithRVByReplicatedStorageClassNameIndex(fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rsc, rsp, lvg, node).
				WithStatusSubresource(rsc)).
				Build()
			rec = NewReconciler(cl)

			result, err := rec.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: client.ObjectKey{Name: "rsc-1"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			var updatedRSC v1alpha1.ReplicatedStorageClass
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "rsc-1"}, &updatedRSC)).To(Succeed())
			Expect(updatedRSC.Status.EligibleNodes).To(HaveLen(1))
			Expect(updatedRSC.Status.EligibleNodes[0].NodeName).To(Equal("node-1"))
			Expect(updatedRSC.Status.EligibleNodesRevision).To(BeNumerically(">", 0))
		})

		It("updates status with volume summary from RVs", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					StoragePool: "rsp-1",
				},
			}
			rsp := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
				Spec: v1alpha1.ReplicatedStoragePoolSpec{
					LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
						{Name: "lvg-1"},
					},
				},
			}
			lvg := &snc.LVMVolumeGroup{
				ObjectMeta: metav1.ObjectMeta{Name: "lvg-1"},
				Spec: snc.LVMVolumeGroupSpec{
					Local: snc.LVMVolumeGroupLocalSpec{NodeName: "node-1"},
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
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					ReplicatedStorageClassName: "rsc-1",
				},
				Status: v1alpha1.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ReplicatedVolumeCondConfigurationReadyType,
							Status: metav1.ConditionTrue,
						},
						{
							Type:   v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesType,
							Status: metav1.ConditionTrue,
						},
					},
				},
			}
			cl = testhelpers.WithRVByReplicatedStorageClassNameIndex(fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rsc, rsp, lvg, node, rv).
				WithStatusSubresource(rsc)).
				Build()
			rec = NewReconciler(cl)

			result, err := rec.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: client.ObjectKey{Name: "rsc-1"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			var updatedRSC v1alpha1.ReplicatedStorageClass
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "rsc-1"}, &updatedRSC)).To(Succeed())
			Expect(*updatedRSC.Status.Volumes.Total).To(Equal(int32(1)))
			Expect(*updatedRSC.Status.Volumes.Aligned).To(Equal(int32(1)))
		})

		It("updates status with empty eligible nodes when RSP is not found", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					StoragePool: "rsp-not-found",
				},
			}
			cl = testhelpers.WithRVByReplicatedStorageClassNameIndex(fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rsc).
				WithStatusSubresource(rsc)).
				Build()
			rec = NewReconciler(cl)

			result, err := rec.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: client.ObjectKey{Name: "rsc-1"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			var updatedRSC v1alpha1.ReplicatedStorageClass
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "rsc-1"}, &updatedRSC)).To(Succeed())
			Expect(updatedRSC.Status.EligibleNodes).To(BeEmpty())
		})

		It("adds finalizer when RSC is created", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					StoragePool: "rsp-1",
				},
			}
			cl = testhelpers.WithRVByReplicatedStorageClassNameIndex(fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rsc).
				WithStatusSubresource(rsc)).
				Build()
			rec = NewReconciler(cl)

			result, err := rec.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: client.ObjectKey{Name: "rsc-1"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			var updatedRSC v1alpha1.ReplicatedStorageClass
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "rsc-1"}, &updatedRSC)).To(Succeed())
			Expect(updatedRSC.Finalizers).To(ContainElement(v1alpha1.RSCControllerFinalizer))
		})

		It("keeps finalizer when RSC has deletionTimestamp but RVs exist", func() {
			now := metav1.Now()
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "rsc-1",
					Finalizers:        []string{v1alpha1.RSCControllerFinalizer},
					DeletionTimestamp: &now,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					StoragePool: "rsp-1",
				},
			}
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					ReplicatedStorageClassName: "rsc-1",
				},
			}
			cl = testhelpers.WithRVByReplicatedStorageClassNameIndex(fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rsc, rv).
				WithStatusSubresource(rsc)).
				Build()
			rec = NewReconciler(cl)

			result, err := rec.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: client.ObjectKey{Name: "rsc-1"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			var updatedRSC v1alpha1.ReplicatedStorageClass
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "rsc-1"}, &updatedRSC)).To(Succeed())
			Expect(updatedRSC.Finalizers).To(ContainElement(v1alpha1.RSCControllerFinalizer))
		})

		It("removes finalizer when RSC has deletionTimestamp and no RVs", func() {
			now := metav1.Now()
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "rsc-1",
					Finalizers:        []string{v1alpha1.RSCControllerFinalizer},
					DeletionTimestamp: &now,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					StoragePool: "rsp-1",
				},
			}
			cl = testhelpers.WithRVByReplicatedStorageClassNameIndex(fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rsc).
				WithStatusSubresource(rsc)).
				Build()
			rec = NewReconciler(cl)

			result, err := rec.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: client.ObjectKey{Name: "rsc-1"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			// After removing the finalizer, the object is deleted by the API server.
			var updatedRSC v1alpha1.ReplicatedStorageClass
			err = cl.Get(context.Background(), client.ObjectKey{Name: "rsc-1"}, &updatedRSC)
			Expect(err).To(HaveOccurred())
			Expect(client.IgnoreNotFound(err)).To(BeNil())
		})
	})
})
