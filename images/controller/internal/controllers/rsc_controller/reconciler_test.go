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
	"k8s.io/apimachinery/pkg/api/meta"
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

var _ = Describe("computeActualVolumesSummary", func() {
	var rsc *v1alpha1.ReplicatedStorageClass

	BeforeEach(func() {
		rsc = &v1alpha1.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
			Status: v1alpha1.ReplicatedStorageClassStatus{
				ConfigurationGeneration:          1,
				StoragePoolEligibleNodesRevision: 1,
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

	It("counts total volumes (RVs without configurationObservedGeneration are considered acknowledged)", func() {
		rvs := []rvView{
			{name: "rv-1"},
			{name: "rv-2"},
		}

		counters := computeActualVolumesSummary(rsc, rvs)

		Expect(*counters.Total).To(Equal(int32(2)))
	})

	It("counts aligned volumes with both conditions true", func() {
		rvs := []rvView{
			{
				name:                            "rv-1",
				configurationObservedGeneration: 1, // Matches rsc.Status.ConfigurationGeneration.
				conditions: rvViewConditions{
					configurationReady:   true,
					satisfyEligibleNodes: true,
				},
			},
		}

		counters := computeActualVolumesSummary(rsc, rvs)

		Expect(*counters.Aligned).To(Equal(int32(1)))
	})

	It("counts configuration not aligned volumes (configurationReady false)", func() {
		rvs := []rvView{
			{
				name:                            "rv-1",
				configurationObservedGeneration: 1, // Matches rsc.Status.ConfigurationGeneration.
				conditions: rvViewConditions{
					configurationReady:   false,
					satisfyEligibleNodes: true,
				},
			},
		}

		counters := computeActualVolumesSummary(rsc, rvs)

		Expect(*counters.StaleConfiguration).To(Equal(int32(1)))
	})

	It("counts eligible nodes not aligned volumes (satisfyEligibleNodes false)", func() {
		rvs := []rvView{
			{
				name: "rv-1",
				conditions: rvViewConditions{
					configurationReady:   true,
					satisfyEligibleNodes: false,
				},
			},
		}

		counters := computeActualVolumesSummary(rsc, rvs)

		Expect(*counters.InConflictWithEligibleNodes).To(Equal(int32(1)))
	})

	It("returns total and inConflictWithEligibleNodes when RV has not acknowledged (mismatched configurationGeneration)", func() {
		rvs := []rvView{
			{
				name:                            "rv-1",
				configurationObservedGeneration: 0, // Mismatch - RSC has 1
				conditions: rvViewConditions{
					configurationReady:   true,
					satisfyEligibleNodes: false, // nodesOK=false
				},
			},
		}

		counters := computeActualVolumesSummary(rsc, rvs)

		Expect(*counters.Total).To(Equal(int32(1)))
		Expect(*counters.PendingObservation).To(Equal(int32(1)))
		Expect(counters.Aligned).To(BeNil())
		Expect(counters.StaleConfiguration).To(BeNil())
		// inConflictWithEligibleNodes is calculated regardless of acknowledgment
		Expect(*counters.InConflictWithEligibleNodes).To(Equal(int32(1)))
	})

	It("returns all counters when all RVs have acknowledged", func() {
		rvs := []rvView{
			{
				name:                            "rv-1",
				configurationObservedGeneration: 1,
				conditions: rvViewConditions{
					configurationReady:   true,
					satisfyEligibleNodes: true,
				},
			},
		}

		counters := computeActualVolumesSummary(rsc, rvs)

		Expect(*counters.Total).To(Equal(int32(1)))
		Expect(*counters.Aligned).To(Equal(int32(1)))
		Expect(*counters.StaleConfiguration).To(Equal(int32(0)))
		Expect(*counters.InConflictWithEligibleNodes).To(Equal(int32(0)))
	})

	It("collects used storage pool names from RVs", func() {
		rvs := []rvView{
			{
				name:                         "rv-1",
				configurationStoragePoolName: "pool-b",
			},
			{
				name:                         "rv-2",
				configurationStoragePoolName: "pool-a",
			},
			{
				name:                         "rv-3",
				configurationStoragePoolName: "pool-b", // Duplicate.
			},
		}

		counters := computeActualVolumesSummary(rsc, rvs)

		// Should be sorted and deduplicated.
		Expect(counters.UsedStoragePoolNames).To(Equal([]string{"pool-a", "pool-b"}))
	})

	It("returns empty UsedStoragePoolNames when no RVs have storage pool", func() {
		rvs := []rvView{
			{name: "rv-1"},
			{name: "rv-2"},
		}

		counters := computeActualVolumesSummary(rsc, rvs)

		Expect(counters.UsedStoragePoolNames).To(BeEmpty())
	})

	It("includes UsedStoragePoolNames even when RVs have not acknowledged", func() {
		rvs := []rvView{
			{
				name:                            "rv-1",
				configurationStoragePoolName:    "pool-a",
				configurationObservedGeneration: 0, // Not acknowledged.
			},
		}

		counters := computeActualVolumesSummary(rsc, rvs)

		Expect(*counters.PendingObservation).To(Equal(int32(1)))
		Expect(counters.UsedStoragePoolNames).To(Equal([]string{"pool-a"}))
	})
})

var _ = Describe("validateEligibleNodes", func() {
	// Helper to create eligible node with or without LVG.
	makeNode := func(name, zone string, hasLVG bool) v1alpha1.ReplicatedStoragePoolEligibleNode {
		node := v1alpha1.ReplicatedStoragePoolEligibleNode{
			NodeName: name,
			ZoneName: zone,
		}
		if hasLVG {
			node.LVMVolumeGroups = []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
				{Name: "lvg-1"},
			}
		}
		return node
	}

	Describe("Replication None", func() {
		It("passes with 1 node", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationNone,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1", "", false),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.Replication)

			Expect(err).NotTo(HaveOccurred())
		})

		It("fails with 0 nodes", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationNone,
				},
			}

			err := validateEligibleNodes(nil, rsc.Spec.Topology, rsc.Spec.Replication)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no eligible nodes"))
		})
	})

	Describe("Replication Availability - Ignored topology", func() {
		It("passes with 3 nodes, 2 with disks", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationAvailability,
					Topology:    v1alpha1.RSCTopologyIgnored,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1", "", true),
				makeNode("node-2", "", true),
				makeNode("node-3", "", false),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.Replication)

			Expect(err).NotTo(HaveOccurred())
		})

		It("fails with 2 nodes", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationAvailability,
					Topology:    v1alpha1.RSCTopologyIgnored,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1", "", true),
				makeNode("node-2", "", true),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.Replication)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 3 nodes"))
		})

		It("fails with 3 nodes but only 1 with disks", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationAvailability,
					Topology:    v1alpha1.RSCTopologyIgnored,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1", "", true),
				makeNode("node-2", "", false),
				makeNode("node-3", "", false),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.Replication)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 2 nodes with disks"))
		})
	})

	Describe("Replication Availability - TransZonal topology", func() {
		It("passes with 3 zones, 2 with disks", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationAvailability,
					Topology:    v1alpha1.RSCTopologyTransZonal,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1", "zone-a", true),
				makeNode("node-2", "zone-b", true),
				makeNode("node-3", "zone-c", false),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.Replication)

			Expect(err).NotTo(HaveOccurred())
		})

		It("fails with 2 zones", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationAvailability,
					Topology:    v1alpha1.RSCTopologyTransZonal,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1", "zone-a", true),
				makeNode("node-2", "zone-b", true),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.Replication)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 3 zones"))
		})

		It("fails with 3 zones but only 1 with disks", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationAvailability,
					Topology:    v1alpha1.RSCTopologyTransZonal,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1", "zone-a", true),
				makeNode("node-2", "zone-b", false),
				makeNode("node-3", "zone-c", false),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.Replication)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 2 zones with disks"))
		})
	})

	Describe("Replication Availability - Zonal topology", func() {
		It("passes with per zone: 3 nodes, 2 with disks", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationAvailability,
					Topology:    v1alpha1.RSCTopologyZonal,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1a", "zone-a", true),
				makeNode("node-2a", "zone-a", true),
				makeNode("node-3a", "zone-a", false),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.Replication)

			Expect(err).NotTo(HaveOccurred())
		})

		It("fails when zone has only 2 nodes", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationAvailability,
					Topology:    v1alpha1.RSCTopologyZonal,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1a", "zone-a", true),
				makeNode("node-2a", "zone-a", true),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.Replication)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 3 nodes in each zone"))
		})

		It("fails when zone has 3 nodes but only 1 with disks", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationAvailability,
					Topology:    v1alpha1.RSCTopologyZonal,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1a", "zone-a", true),
				makeNode("node-2a", "zone-a", false),
				makeNode("node-3a", "zone-a", false),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.Replication)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 2 nodes with disks in each zone"))
		})
	})

	Describe("Replication Consistency - Ignored topology", func() {
		It("passes with 2 nodes both with disks", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationConsistency,
					Topology:    v1alpha1.RSCTopologyIgnored,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1", "", true),
				makeNode("node-2", "", true),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.Replication)

			Expect(err).NotTo(HaveOccurred())
		})

		It("fails with 1 node with disks", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationConsistency,
					Topology:    v1alpha1.RSCTopologyIgnored,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1", "", true),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.Replication)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 2 nodes"))
		})

		It("fails with 2 nodes but only 1 with disks", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationConsistency,
					Topology:    v1alpha1.RSCTopologyIgnored,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1", "", true),
				makeNode("node-2", "", false),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.Replication)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 2 nodes with disks"))
		})
	})

	Describe("Replication Consistency - TransZonal topology", func() {
		It("passes with 2 zones with disks", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationConsistency,
					Topology:    v1alpha1.RSCTopologyTransZonal,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1", "zone-a", true),
				makeNode("node-2", "zone-b", true),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.Replication)

			Expect(err).NotTo(HaveOccurred())
		})

		It("fails with 1 zone with disks", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationConsistency,
					Topology:    v1alpha1.RSCTopologyTransZonal,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1", "zone-a", true),
				makeNode("node-2", "zone-b", false),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.Replication)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 2 zones with disks"))
		})
	})

	Describe("Replication Consistency - Zonal topology", func() {
		It("passes with per zone: 2 nodes with disks", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationConsistency,
					Topology:    v1alpha1.RSCTopologyZonal,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1a", "zone-a", true),
				makeNode("node-2a", "zone-a", true),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.Replication)

			Expect(err).NotTo(HaveOccurred())
		})

		It("fails when zone has 1 node with disks", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationConsistency,
					Topology:    v1alpha1.RSCTopologyZonal,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1a", "zone-a", true),
				makeNode("node-2a", "zone-a", false),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.Replication)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 2 nodes with disks in each zone"))
		})
	})

	Describe("Replication ConsistencyAndAvailability - Ignored topology", func() {
		It("passes with 3 nodes with disks", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationConsistencyAndAvailability,
					Topology:    v1alpha1.RSCTopologyIgnored,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1", "", true),
				makeNode("node-2", "", true),
				makeNode("node-3", "", true),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.Replication)

			Expect(err).NotTo(HaveOccurred())
		})

		It("fails with 2 nodes with disks", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationConsistencyAndAvailability,
					Topology:    v1alpha1.RSCTopologyIgnored,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1", "", true),
				makeNode("node-2", "", true),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.Replication)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 3 nodes with disks"))
		})
	})

	Describe("Replication ConsistencyAndAvailability - TransZonal topology", func() {
		It("passes with 3 zones with disks", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationConsistencyAndAvailability,
					Topology:    v1alpha1.RSCTopologyTransZonal,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1", "zone-a", true),
				makeNode("node-2", "zone-b", true),
				makeNode("node-3", "zone-c", true),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.Replication)

			Expect(err).NotTo(HaveOccurred())
		})

		It("fails with 2 zones with disks", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationConsistencyAndAvailability,
					Topology:    v1alpha1.RSCTopologyTransZonal,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1", "zone-a", true),
				makeNode("node-2", "zone-b", true),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.Replication)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 3 zones with disks"))
		})
	})

	Describe("Replication ConsistencyAndAvailability - Zonal topology", func() {
		It("passes with per zone: 3 nodes with disks", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationConsistencyAndAvailability,
					Topology:    v1alpha1.RSCTopologyZonal,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1a", "zone-a", true),
				makeNode("node-2a", "zone-a", true),
				makeNode("node-3a", "zone-a", true),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.Replication)

			Expect(err).NotTo(HaveOccurred())
		})

		It("fails when zone has 2 nodes with disks", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationConsistencyAndAvailability,
					Topology:    v1alpha1.RSCTopologyZonal,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1a", "zone-a", true),
				makeNode("node-2a", "zone-a", true),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.Replication)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 3 nodes with disks in each zone"))
		})
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

var _ = Describe("ensureVolumeSummaryAndConditions", func() {
	var (
		ctx context.Context
		rsc *v1alpha1.ReplicatedStorageClass
	)

	// makeAcknowledgedRV creates an rvView that has acknowledged the RSC configuration.
	makeAcknowledgedRV := func(name string, configOK, nodesOK bool) rvView {
		return rvView{
			name:                            name,
			configurationObservedGeneration: 1,
			conditions: rvViewConditions{
				configurationReady:   configOK,
				satisfyEligibleNodes: nodesOK,
			},
		}
	}

	// makePendingRV creates an rvView that has NOT acknowledged the RSC configuration.
	makePendingRV := func(name string) rvView {
		return rvView{
			name:                            name,
			configurationObservedGeneration: 0, // Mismatch - RSC has 1
			conditions: rvViewConditions{
				configurationReady:   false,
				satisfyEligibleNodes: false,
			},
		}
	}

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
			Status: v1alpha1.ReplicatedStorageClassStatus{
				ConfigurationGeneration:          1,
				StoragePoolEligibleNodesRevision: 1,
			},
		}
	})

	It("sets ConfigurationRolledOut to Unknown and VolumesSatisfyEligibleNodes based on actual when PendingObservation > 0", func() {
		rvs := []rvView{
			makePendingRV("rv-1"),
			makePendingRV("rv-2"),
			makePendingRV("rv-3"),
		}

		outcome := ensureVolumeSummaryAndConditions(ctx, rsc, rvs)

		Expect(outcome.Error()).To(BeNil())
		Expect(outcome.DidChange()).To(BeTrue())

		// Check summary
		Expect(rsc.Status.Volumes.PendingObservation).To(Equal(ptr.To(int32(3))))
		Expect(rsc.Status.Volumes.InConflictWithEligibleNodes).To(Equal(ptr.To(int32(3))))

		// ConfigurationRolledOut is Unknown because we can't determine config status without acknowledgment.
		configCond := obju.GetStatusCondition(rsc, v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutType)
		Expect(configCond).NotTo(BeNil())
		Expect(configCond.Status).To(Equal(metav1.ConditionUnknown))
		Expect(configCond.Reason).To(Equal(v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutReasonNewConfigurationNotYetObserved))
		Expect(configCond.Message).To(ContainSubstring("3 volume(s) pending observation"))

		// VolumesSatisfyEligibleNodes is calculated regardless of acknowledgment.
		nodesCond := obju.GetStatusCondition(rsc, v1alpha1.ReplicatedStorageClassCondVolumesSatisfyEligibleNodesType)
		Expect(nodesCond).NotTo(BeNil())
		Expect(nodesCond.Status).To(Equal(metav1.ConditionFalse))
		Expect(nodesCond.Reason).To(Equal(v1alpha1.ReplicatedStorageClassCondVolumesSatisfyEligibleNodesReasonManualConflictResolution))
	})

	It("sets ConfigurationRolledOut to False when StaleConfiguration > 0", func() {
		rvs := []rvView{
			makeAcknowledgedRV("rv-1", false, true), // configOK=false
			makeAcknowledgedRV("rv-2", false, true), // configOK=false
		}

		outcome := ensureVolumeSummaryAndConditions(ctx, rsc, rvs)

		Expect(outcome.Error()).To(BeNil())
		Expect(outcome.DidChange()).To(BeTrue())

		configCond := obju.GetStatusCondition(rsc, v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutType)
		Expect(configCond).NotTo(BeNil())
		Expect(configCond.Status).To(Equal(metav1.ConditionFalse))
		Expect(configCond.Reason).To(Equal(v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutReasonConfigurationRolloutDisabled))
	})

	It("sets ConfigurationRolledOut to True when StaleConfiguration == 0", func() {
		rvs := []rvView{
			makeAcknowledgedRV("rv-1", true, true),
			makeAcknowledgedRV("rv-2", true, true),
		}

		outcome := ensureVolumeSummaryAndConditions(ctx, rsc, rvs)

		Expect(outcome.Error()).To(BeNil())
		Expect(outcome.DidChange()).To(BeTrue())

		configCond := obju.GetStatusCondition(rsc, v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutType)
		Expect(configCond).NotTo(BeNil())
		Expect(configCond.Status).To(Equal(metav1.ConditionTrue))
		Expect(configCond.Reason).To(Equal(v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutReasonRolledOutToAllVolumes))
	})

	It("sets VolumesSatisfyEligibleNodes to False when InConflictWithEligibleNodes > 0", func() {
		rvs := []rvView{
			makeAcknowledgedRV("rv-1", true, false), // nodesOK=false
			makeAcknowledgedRV("rv-2", true, false), // nodesOK=false
		}

		outcome := ensureVolumeSummaryAndConditions(ctx, rsc, rvs)

		Expect(outcome.Error()).To(BeNil())
		Expect(outcome.DidChange()).To(BeTrue())

		nodesCond := obju.GetStatusCondition(rsc, v1alpha1.ReplicatedStorageClassCondVolumesSatisfyEligibleNodesType)
		Expect(nodesCond).NotTo(BeNil())
		Expect(nodesCond.Status).To(Equal(metav1.ConditionFalse))
		Expect(nodesCond.Reason).To(Equal(v1alpha1.ReplicatedStorageClassCondVolumesSatisfyEligibleNodesReasonManualConflictResolution))
	})

	It("sets VolumesSatisfyEligibleNodes to True when InConflictWithEligibleNodes == 0", func() {
		rvs := []rvView{
			makeAcknowledgedRV("rv-1", true, true),
			makeAcknowledgedRV("rv-2", true, true),
		}

		outcome := ensureVolumeSummaryAndConditions(ctx, rsc, rvs)

		Expect(outcome.Error()).To(BeNil())
		Expect(outcome.DidChange()).To(BeTrue())

		nodesCond := obju.GetStatusCondition(rsc, v1alpha1.ReplicatedStorageClassCondVolumesSatisfyEligibleNodesType)
		Expect(nodesCond).NotTo(BeNil())
		Expect(nodesCond.Status).To(Equal(metav1.ConditionTrue))
		Expect(nodesCond.Reason).To(Equal(v1alpha1.ReplicatedStorageClassCondVolumesSatisfyEligibleNodesReasonAllVolumesSatisfy))
	})

	It("sets both conditions correctly when StaleConfiguration > 0 and InConflictWithEligibleNodes > 0", func() {
		rvs := []rvView{
			makeAcknowledgedRV("rv-1", false, false), // both false
			makeAcknowledgedRV("rv-2", false, false), // both false
		}

		outcome := ensureVolumeSummaryAndConditions(ctx, rsc, rvs)

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
		rvs := []rvView{
			makeAcknowledgedRV("rv-1", true, true),
		}

		// First call to set conditions
		outcome := ensureVolumeSummaryAndConditions(ctx, rsc, rvs)
		Expect(outcome.DidChange()).To(BeTrue())

		// Second call should report no change
		outcome = ensureVolumeSummaryAndConditions(ctx, rsc, rvs)
		Expect(outcome.Error()).To(BeNil())
		Expect(outcome.DidChange()).To(BeFalse())
	})

	It("sets conditions to True when no volumes exist", func() {
		rvs := []rvView{}

		outcome := ensureVolumeSummaryAndConditions(ctx, rsc, rvs)

		Expect(outcome.Error()).To(BeNil())
		Expect(outcome.DidChange()).To(BeTrue())

		// Check summary
		Expect(rsc.Status.Volumes.Total).To(Equal(ptr.To(int32(0))))
		Expect(rsc.Status.Volumes.PendingObservation).To(Equal(ptr.To(int32(0))))

		configCond := obju.GetStatusCondition(rsc, v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutType)
		Expect(configCond).NotTo(BeNil())
		Expect(configCond.Status).To(Equal(metav1.ConditionTrue))

		nodesCond := obju.GetStatusCondition(rsc, v1alpha1.ReplicatedStorageClassCondVolumesSatisfyEligibleNodesType)
		Expect(nodesCond).NotTo(BeNil())
		Expect(nodesCond.Status).To(Equal(metav1.ConditionTrue))
	})
})

var _ = Describe("makeConfiguration", func() {
	It("copies all fields from spec correctly", func() {
		rsc := &v1alpha1.ReplicatedStorageClass{
			Spec: v1alpha1.ReplicatedStorageClassSpec{
				Topology:     v1alpha1.RSCTopologyTransZonal,
				Replication:  v1alpha1.ReplicationAvailability,
				VolumeAccess: v1alpha1.VolumeAccessLocal,
			},
		}

		config := makeConfiguration(rsc, "my-storage-pool")

		Expect(config.Topology).To(Equal(v1alpha1.RSCTopologyTransZonal))
		Expect(config.Replication).To(Equal(v1alpha1.ReplicationAvailability))
		Expect(config.VolumeAccess).To(Equal(v1alpha1.VolumeAccessLocal))
		Expect(config.StoragePoolName).To(Equal("my-storage-pool"))
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

		It("migrates StoragePool to spec.Storage when RSP exists", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					StoragePool: "rsp-1",
				},
			}
			rsp := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
				Spec: v1alpha1.ReplicatedStoragePoolSpec{
					Type: v1alpha1.ReplicatedStoragePoolTypeLVMThin,
					LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
						{Name: "lvg-1"},
						{Name: "lvg-2"},
					},
				},
			}
			cl = testhelpers.WithRVByReplicatedStorageClassNameIndex(fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rsc, rsp).
				WithStatusSubresource(rsc, &v1alpha1.ReplicatedStoragePool{})).
				Build()
			rec = NewReconciler(cl)

			result, err := rec.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: client.ObjectKey{Name: "rsc-1"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			var updatedRSC v1alpha1.ReplicatedStorageClass
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "rsc-1"}, &updatedRSC)).To(Succeed())

			// StoragePool should be cleared.
			Expect(updatedRSC.Spec.StoragePool).To(BeEmpty())

			// spec.Storage should contain data from RSP.
			Expect(updatedRSC.Spec.Storage.Type).To(Equal(v1alpha1.ReplicatedStoragePoolTypeLVMThin))
			Expect(updatedRSC.Spec.Storage.LVMVolumeGroups).To(HaveLen(2))
			Expect(updatedRSC.Spec.Storage.LVMVolumeGroups[0].Name).To(Equal("lvg-1"))
			Expect(updatedRSC.Spec.Storage.LVMVolumeGroups[1].Name).To(Equal("lvg-2"))

			// Finalizer should be added.
			Expect(updatedRSC.Finalizers).To(ContainElement(v1alpha1.RSCControllerFinalizer))
		})

		It("sets conditions when RSP is not found", func() {
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

			// StoragePool should remain unchanged (waiting for RSP to exist).
			Expect(updatedRSC.Spec.StoragePool).To(Equal("rsp-not-found"))

			// Finalizer should NOT be added (reconcileMigrationFromRSP returns Done before reconcileMain).
			Expect(updatedRSC.Finalizers).To(BeEmpty())

			// Conditions should be set.
			readyCond := meta.FindStatusCondition(updatedRSC.Status.Conditions, v1alpha1.ReplicatedStorageClassCondReadyType)
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(readyCond.Reason).To(Equal(v1alpha1.ReplicatedStorageClassCondReadyReasonWaitingForStoragePool))

			storagePoolReadyCond := meta.FindStatusCondition(updatedRSC.Status.Conditions, v1alpha1.ReplicatedStorageClassCondStoragePoolReadyType)
			Expect(storagePoolReadyCond).NotTo(BeNil())
			Expect(storagePoolReadyCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(storagePoolReadyCond.Reason).To(Equal(v1alpha1.ReplicatedStorageClassCondStoragePoolReadyReasonStoragePoolNotFound))
		})

		It("does nothing when storagePool is already empty", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rsc-1",
					Finalizers: []string{v1alpha1.RSCControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					StoragePool: "", // Already empty - no migration needed.
					Storage: v1alpha1.ReplicatedStorageClassStorage{
						Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
							{Name: "lvg-existing"},
						},
					},
				},
			}
			cl = testhelpers.WithRVByReplicatedStorageClassNameIndex(fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rsc).
				WithStatusSubresource(rsc, &v1alpha1.ReplicatedStoragePool{})).
				Build()
			rec = NewReconciler(cl)

			result, err := rec.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: client.ObjectKey{Name: "rsc-1"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			var updatedRSC v1alpha1.ReplicatedStorageClass
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "rsc-1"}, &updatedRSC)).To(Succeed())

			// Nothing should change.
			Expect(updatedRSC.Spec.StoragePool).To(BeEmpty())
			Expect(updatedRSC.Spec.Storage.Type).To(Equal(v1alpha1.ReplicatedStoragePoolTypeLVM))
			Expect(updatedRSC.Spec.Storage.LVMVolumeGroups).To(HaveLen(1))
			Expect(updatedRSC.Spec.Storage.LVMVolumeGroups[0].Name).To(Equal("lvg-existing"))
		})

		It("sets condition StoragePoolReady=False when RSP is not found during migration", func() {
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

			// Check Ready condition is false.
			readyCond := obju.GetStatusCondition(&updatedRSC, v1alpha1.ReplicatedStorageClassCondReadyType)
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(readyCond.Reason).To(Equal(v1alpha1.ReplicatedStorageClassCondReadyReasonWaitingForStoragePool))

			// Check StoragePoolReady condition is false.
			storagePoolCond := obju.GetStatusCondition(&updatedRSC, v1alpha1.ReplicatedStorageClassCondStoragePoolReadyType)
			Expect(storagePoolCond).NotTo(BeNil())
			Expect(storagePoolCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(storagePoolCond.Reason).To(Equal(v1alpha1.ReplicatedStorageClassCondStoragePoolReadyReasonStoragePoolNotFound))
		})

		It("adds finalizer when RSC is created", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					// No storagePool - using direct storage configuration.
					Storage: v1alpha1.ReplicatedStorageClassStorage{
						Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
							{Name: "lvg-1"},
						},
					},
				},
			}
			cl = testhelpers.WithRVByReplicatedStorageClassNameIndex(fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rsc).
				WithStatusSubresource(rsc, &v1alpha1.ReplicatedStoragePool{})).
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
					// No storagePool - using direct storage configuration.
					Storage: v1alpha1.ReplicatedStorageClassStorage{
						Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
							{Name: "lvg-1"},
						},
					},
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
				WithStatusSubresource(rsc, &v1alpha1.ReplicatedStoragePool{})).
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
					// No storagePool - using direct storage configuration.
					Storage: v1alpha1.ReplicatedStorageClassStorage{
						Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
							{Name: "lvg-1"},
						},
					},
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

	Describe("reconcileRSP", func() {
		It("creates RSP when it does not exist", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rsc-1",
					Generation: 1,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Storage: v1alpha1.ReplicatedStorageClassStorage{
						Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
							{Name: "lvg-1"},
							{Name: "lvg-2"},
						},
					},
					Zones:              []string{"zone-a", "zone-b"},
					SystemNetworkNames: []string{"Internal"},
					EligibleNodesPolicy: v1alpha1.ReplicatedStoragePoolEligibleNodesPolicy{
						NotReadyGracePeriod: metav1.Duration{Duration: 5 * time.Minute},
					},
				},
			}
			cl = fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rsc).
				WithStatusSubresource(rsc, &v1alpha1.ReplicatedStoragePool{}).
				Build()
			rec = NewReconciler(cl)

			targetStoragePoolName := "auto-rsp-test123"
			outcome, rsp := rec.reconcileRSP(context.Background(), rsc, targetStoragePoolName)

			Expect(outcome.ShouldReturn()).To(BeFalse())
			Expect(rsp).NotTo(BeNil())
			Expect(rsp.Name).To(Equal(targetStoragePoolName))

			// Verify RSP was created.
			var createdRSP v1alpha1.ReplicatedStoragePool
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: targetStoragePoolName}, &createdRSP)).To(Succeed())

			// Verify finalizer is set.
			Expect(createdRSP.Finalizers).To(ContainElement(v1alpha1.RSCControllerFinalizer))

			// Verify spec.
			Expect(createdRSP.Spec.Type).To(Equal(v1alpha1.ReplicatedStoragePoolTypeLVM))
			Expect(createdRSP.Spec.LVMVolumeGroups).To(HaveLen(2))
			Expect(createdRSP.Spec.Zones).To(Equal([]string{"zone-a", "zone-b"}))
			Expect(createdRSP.Spec.SystemNetworkNames).To(Equal([]string{"Internal"}))
			Expect(createdRSP.Spec.EligibleNodesPolicy.NotReadyGracePeriod.Duration).To(Equal(5 * time.Minute))

			// Verify usedBy is set.
			Expect(createdRSP.Status.UsedBy.ReplicatedStorageClassNames).To(ContainElement("rsc-1"))
		})

		It("adds finalizer to existing RSP without finalizer", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rsc-1",
					Generation: 1,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Storage: v1alpha1.ReplicatedStorageClassStorage{
						Type:            v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{{Name: "lvg-1"}},
					},
				},
			}
			existingRSP := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{
					Name: "auto-rsp-existing",
					// No finalizer.
				},
				Spec: v1alpha1.ReplicatedStoragePoolSpec{
					Type:            v1alpha1.ReplicatedStoragePoolTypeLVM,
					LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{{Name: "lvg-1"}},
				},
			}
			cl = fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rsc, existingRSP).
				WithStatusSubresource(rsc, existingRSP).
				Build()
			rec = NewReconciler(cl)

			outcome, rsp := rec.reconcileRSP(context.Background(), rsc, "auto-rsp-existing")

			Expect(outcome.ShouldReturn()).To(BeFalse())
			Expect(rsp).NotTo(BeNil())

			// Verify finalizer was added.
			var updatedRSP v1alpha1.ReplicatedStoragePool
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "auto-rsp-existing"}, &updatedRSP)).To(Succeed())
			Expect(updatedRSP.Finalizers).To(ContainElement(v1alpha1.RSCControllerFinalizer))
		})

		It("adds RSC name to usedBy", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rsc-1",
					Generation: 1,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Storage: v1alpha1.ReplicatedStorageClassStorage{
						Type:            v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{{Name: "lvg-1"}},
					},
				},
			}
			existingRSP := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "auto-rsp-existing",
					Finalizers: []string{v1alpha1.RSCControllerFinalizer}, // Already has finalizer.
				},
				Spec: v1alpha1.ReplicatedStoragePoolSpec{
					Type:            v1alpha1.ReplicatedStoragePoolTypeLVM,
					LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{{Name: "lvg-1"}},
				},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					UsedBy: v1alpha1.ReplicatedStoragePoolUsedBy{
						ReplicatedStorageClassNames: []string{"other-rsc"}, // Another RSC already uses this.
					},
				},
			}
			cl = fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rsc, existingRSP).
				WithStatusSubresource(rsc, existingRSP).
				Build()
			rec = NewReconciler(cl)

			outcome, rsp := rec.reconcileRSP(context.Background(), rsc, "auto-rsp-existing")

			Expect(outcome.ShouldReturn()).To(BeFalse())
			Expect(rsp).NotTo(BeNil())

			// Verify RSC name was added to usedBy.
			var updatedRSP v1alpha1.ReplicatedStoragePool
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "auto-rsp-existing"}, &updatedRSP)).To(Succeed())
			Expect(updatedRSP.Status.UsedBy.ReplicatedStorageClassNames).To(ContainElement("rsc-1"))
			Expect(updatedRSP.Status.UsedBy.ReplicatedStorageClassNames).To(ContainElement("other-rsc"))
			// Verify sorted order.
			Expect(updatedRSP.Status.UsedBy.ReplicatedStorageClassNames).To(Equal([]string{"other-rsc", "rsc-1"}))
		})

		It("does not update when RSP already has finalizer and usedBy contains RSC", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rsc-1",
					Generation: 1,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Storage: v1alpha1.ReplicatedStorageClassStorage{
						Type:            v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{{Name: "lvg-1"}},
					},
				},
			}
			existingRSP := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "auto-rsp-existing",
					Finalizers:      []string{v1alpha1.RSCControllerFinalizer},
					ResourceVersion: "123",
				},
				Spec: v1alpha1.ReplicatedStoragePoolSpec{
					Type:            v1alpha1.ReplicatedStoragePoolTypeLVM,
					LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{{Name: "lvg-1"}},
				},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					UsedBy: v1alpha1.ReplicatedStoragePoolUsedBy{
						ReplicatedStorageClassNames: []string{"rsc-1"}, // Already has this RSC.
					},
				},
			}
			cl = fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rsc, existingRSP).
				WithStatusSubresource(rsc, existingRSP).
				Build()
			rec = NewReconciler(cl)

			outcome, rsp := rec.reconcileRSP(context.Background(), rsc, "auto-rsp-existing")

			Expect(outcome.ShouldReturn()).To(BeFalse())
			Expect(rsp).NotTo(BeNil())

			// Verify nothing changed.
			var updatedRSP v1alpha1.ReplicatedStoragePool
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "auto-rsp-existing"}, &updatedRSP)).To(Succeed())
			// ResourceVersion should be unchanged if no updates were made.
			// Note: fake client may update resourceVersion anyway, so we check content instead.
			Expect(updatedRSP.Status.UsedBy.ReplicatedStorageClassNames).To(Equal([]string{"rsc-1"}))
		})
	})

	Describe("reconcileRSPRelease", func() {
		It("does nothing when RSP does not exist", func() {
			cl = fake.NewClientBuilder().
				WithScheme(scheme).
				Build()
			rec = NewReconciler(cl)

			outcome := rec.reconcileRSPRelease(context.Background(), "rsc-1", "non-existent-rsp")

			Expect(outcome.ShouldReturn()).To(BeFalse())
		})

		It("does nothing when RSC not in usedBy", func() {
			rsp := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "my-rsp",
					Finalizers: []string{v1alpha1.RSCControllerFinalizer},
				},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					UsedBy: v1alpha1.ReplicatedStoragePoolUsedBy{
						ReplicatedStorageClassNames: []string{"other-rsc"},
					},
				},
			}
			cl = fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rsp).
				WithStatusSubresource(rsp).
				Build()
			rec = NewReconciler(cl)

			outcome := rec.reconcileRSPRelease(context.Background(), "rsc-1", "my-rsp")

			Expect(outcome.ShouldReturn()).To(BeFalse())

			// RSP should be unchanged.
			var updatedRSP v1alpha1.ReplicatedStoragePool
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "my-rsp"}, &updatedRSP)).To(Succeed())
			Expect(updatedRSP.Status.UsedBy.ReplicatedStorageClassNames).To(Equal([]string{"other-rsc"}))
		})

		It("removes RSC from usedBy when RSC is in usedBy", func() {
			rsp := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "my-rsp",
					Finalizers: []string{v1alpha1.RSCControllerFinalizer},
				},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					UsedBy: v1alpha1.ReplicatedStoragePoolUsedBy{
						ReplicatedStorageClassNames: []string{"other-rsc", "rsc-1"},
					},
				},
			}
			cl = fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rsp).
				WithStatusSubresource(rsp).
				Build()
			rec = NewReconciler(cl)

			outcome := rec.reconcileRSPRelease(context.Background(), "rsc-1", "my-rsp")

			Expect(outcome.ShouldReturn()).To(BeFalse())

			// RSP should have rsc-1 removed from usedBy.
			var updatedRSP v1alpha1.ReplicatedStoragePool
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "my-rsp"}, &updatedRSP)).To(Succeed())
			Expect(updatedRSP.Status.UsedBy.ReplicatedStorageClassNames).To(Equal([]string{"other-rsc"}))
			// RSP should still exist.
			Expect(updatedRSP.Finalizers).To(ContainElement(v1alpha1.RSCControllerFinalizer))
		})

		It("deletes RSP when usedBy becomes empty", func() {
			rsp := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "my-rsp",
					Finalizers: []string{v1alpha1.RSCControllerFinalizer},
				},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					UsedBy: v1alpha1.ReplicatedStoragePoolUsedBy{
						ReplicatedStorageClassNames: []string{"rsc-1"},
					},
				},
			}
			cl = fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rsp).
				WithStatusSubresource(rsp).
				Build()
			rec = NewReconciler(cl)

			outcome := rec.reconcileRSPRelease(context.Background(), "rsc-1", "my-rsp")

			Expect(outcome.ShouldReturn()).To(BeFalse())

			// RSP should be deleted.
			var updatedRSP v1alpha1.ReplicatedStoragePool
			err := cl.Get(context.Background(), client.ObjectKey{Name: "my-rsp"}, &updatedRSP)
			Expect(err).To(HaveOccurred())
			Expect(client.IgnoreNotFound(err)).To(BeNil())
		})
	})

	Describe("newRSP", func() {
		It("builds RSP with correct spec from RSC", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Storage: v1alpha1.ReplicatedStorageClassStorage{
						Type: v1alpha1.ReplicatedStoragePoolTypeLVMThin,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
							{Name: "lvg-1", ThinPoolName: "thin-1"},
							{Name: "lvg-2", ThinPoolName: "thin-2"},
						},
					},
					Zones: []string{"zone-a", "zone-b", "zone-c"},
					NodeLabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"node-type": "storage"},
					},
					SystemNetworkNames: []string{"Internal"},
					EligibleNodesPolicy: v1alpha1.ReplicatedStoragePoolEligibleNodesPolicy{
						NotReadyGracePeriod: metav1.Duration{Duration: 15 * time.Minute},
					},
				},
			}

			rsp := newRSP("auto-rsp-abc123", rsc)

			Expect(rsp.Name).To(Equal("auto-rsp-abc123"))
			Expect(rsp.Finalizers).To(ContainElement(v1alpha1.RSCControllerFinalizer))

			Expect(rsp.Spec.Type).To(Equal(v1alpha1.ReplicatedStoragePoolTypeLVMThin))
			Expect(rsp.Spec.LVMVolumeGroups).To(HaveLen(2))
			Expect(rsp.Spec.LVMVolumeGroups[0].Name).To(Equal("lvg-1"))
			Expect(rsp.Spec.LVMVolumeGroups[0].ThinPoolName).To(Equal("thin-1"))
			Expect(rsp.Spec.LVMVolumeGroups[1].Name).To(Equal("lvg-2"))
			Expect(rsp.Spec.LVMVolumeGroups[1].ThinPoolName).To(Equal("thin-2"))

			Expect(rsp.Spec.Zones).To(Equal([]string{"zone-a", "zone-b", "zone-c"}))
			Expect(rsp.Spec.NodeLabelSelector).NotTo(BeNil())
			Expect(rsp.Spec.NodeLabelSelector.MatchLabels).To(HaveKeyWithValue("node-type", "storage"))
			Expect(rsp.Spec.SystemNetworkNames).To(Equal([]string{"Internal"}))
			Expect(rsp.Spec.EligibleNodesPolicy.NotReadyGracePeriod.Duration).To(Equal(15 * time.Minute))
		})

		It("builds RSP without NodeLabelSelector when not set", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Storage: v1alpha1.ReplicatedStorageClassStorage{
						Type:            v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{{Name: "lvg-1"}},
					},
					SystemNetworkNames: []string{"Internal"},
				},
			}

			rsp := newRSP("auto-rsp-xyz", rsc)

			Expect(rsp.Spec.NodeLabelSelector).To(BeNil())
		})

		It("does not share slices with RSC", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Storage: v1alpha1.ReplicatedStorageClassStorage{
						Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
							{Name: "lvg-1"},
						},
					},
					Zones:              []string{"zone-a"},
					SystemNetworkNames: []string{"Internal"},
				},
			}

			rsp := newRSP("auto-rsp-test", rsc)

			// Modify RSP slices.
			rsp.Spec.LVMVolumeGroups[0].Name = "modified"
			rsp.Spec.Zones[0] = "modified"
			rsp.Spec.SystemNetworkNames[0] = "modified"

			// Verify RSC slices are unchanged.
			Expect(rsc.Spec.Storage.LVMVolumeGroups[0].Name).To(Equal("lvg-1"))
			Expect(rsc.Spec.Zones[0]).To(Equal("zone-a"))
			Expect(rsc.Spec.SystemNetworkNames[0]).To(Equal("Internal"))
		})
	})

	Describe("ensureStoragePool", func() {
		It("updates storagePoolName and storagePoolBasedOnGeneration when not in sync", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rsc-1",
					Generation: 3,
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					StoragePoolBasedOnGeneration: 2, // Different from Generation.
					StoragePoolName:              "old-pool-name",
				},
			}
			rsp := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "new-pool-name"},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					Conditions: []metav1.Condition{
						{Type: v1alpha1.ReplicatedStoragePoolCondReadyType, Status: metav1.ConditionTrue, Reason: "Ready"},
					},
				},
			}

			outcome := ensureStoragePool(context.Background(), rsc, "new-pool-name", rsp)

			Expect(outcome.DidChange()).To(BeTrue())
			Expect(rsc.Status.StoragePoolName).To(Equal("new-pool-name"))
			Expect(rsc.Status.StoragePoolBasedOnGeneration).To(Equal(int64(3)))
		})

		It("reports no change when already in sync", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rsc-1",
					Generation: 5,
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					StoragePoolBasedOnGeneration: 5,
					StoragePoolName:              "my-pool",
					Conditions: []metav1.Condition{
						{
							Type:               v1alpha1.ReplicatedStorageClassCondStoragePoolReadyType,
							Status:             metav1.ConditionTrue,
							Reason:             "Ready",
							ObservedGeneration: 5, // Must match RSC Generation.
						},
					},
				},
			}
			rsp := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "my-pool"},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					Conditions: []metav1.Condition{
						{Type: v1alpha1.ReplicatedStoragePoolCondReadyType, Status: metav1.ConditionTrue, Reason: "Ready"},
					},
				},
			}

			outcome := ensureStoragePool(context.Background(), rsc, "my-pool", rsp)

			Expect(outcome.DidChange()).To(BeFalse())
		})

		It("sets StoragePoolReady=False when RSP is nil", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rsc-1",
					Generation: 1,
				},
			}

			outcome := ensureStoragePool(context.Background(), rsc, "missing-pool", nil)

			Expect(outcome.DidChange()).To(BeTrue())
			cond := obju.GetStatusCondition(rsc, v1alpha1.ReplicatedStorageClassCondStoragePoolReadyType)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedStorageClassCondStoragePoolReadyReasonStoragePoolNotFound))
			Expect(cond.Message).To(ContainSubstring("missing-pool"))
		})

		It("sets StoragePoolReady=Unknown when RSP has no Ready condition", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rsc-1",
					Generation: 1,
				},
			}
			rsp := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "new-pool"},
				// No conditions.
			}

			outcome := ensureStoragePool(context.Background(), rsc, "new-pool", rsp)

			Expect(outcome.DidChange()).To(BeTrue())
			cond := obju.GetStatusCondition(rsc, v1alpha1.ReplicatedStorageClassCondStoragePoolReadyType)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
			Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedStorageClassCondStoragePoolReadyReasonPending))
		})

		It("copies RSP Ready=True to RSC StoragePoolReady=True", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rsc-1",
					Generation: 1,
				},
			}
			rsp := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "ready-pool"},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					Conditions: []metav1.Condition{
						{
							Type:    v1alpha1.ReplicatedStoragePoolCondReadyType,
							Status:  metav1.ConditionTrue,
							Reason:  "AllNodesEligible",
							Message: "All nodes are eligible",
						},
					},
				},
			}

			outcome := ensureStoragePool(context.Background(), rsc, "ready-pool", rsp)

			Expect(outcome.DidChange()).To(BeTrue())
			cond := obju.GetStatusCondition(rsc, v1alpha1.ReplicatedStorageClassCondStoragePoolReadyType)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal("AllNodesEligible"))
			Expect(cond.Message).To(Equal("All nodes are eligible"))
		})

		It("copies RSP Ready=False to RSC StoragePoolReady=False", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rsc-1",
					Generation: 1,
				},
			}
			rsp := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "not-ready-pool"},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					Conditions: []metav1.Condition{
						{
							Type:    v1alpha1.ReplicatedStoragePoolCondReadyType,
							Status:  metav1.ConditionFalse,
							Reason:  "LVGNotReady",
							Message: "LVMVolumeGroup is not ready",
						},
					},
				},
			}

			outcome := ensureStoragePool(context.Background(), rsc, "not-ready-pool", rsp)

			Expect(outcome.DidChange()).To(BeTrue())
			cond := obju.GetStatusCondition(rsc, v1alpha1.ReplicatedStorageClassCondStoragePoolReadyType)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal("LVGNotReady"))
			Expect(cond.Message).To(Equal("LVMVolumeGroup is not ready"))
		})
	})

	Describe("ensureConfiguration", func() {
		It("panics when StoragePoolBasedOnGeneration != Generation", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rsc-1",
					Generation: 5,
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					StoragePoolBasedOnGeneration: 4, // Mismatch.
				},
			}
			rsp := &v1alpha1.ReplicatedStoragePool{}

			Expect(func() {
				ensureConfiguration(context.Background(), rsc, rsp)
			}).To(Panic())
		})

		It("sets Ready=False when StoragePoolReady is not True", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rsc-1",
					Generation: 5,
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					StoragePoolBasedOnGeneration: 5,
					// No StoragePoolReady condition - defaults to not-true.
				},
			}
			rsp := &v1alpha1.ReplicatedStoragePool{}

			outcome := ensureConfiguration(context.Background(), rsc, rsp)

			Expect(outcome.DidChange()).To(BeTrue())
			cond := obju.GetStatusCondition(rsc, v1alpha1.ReplicatedStorageClassCondReadyType)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedStorageClassCondReadyReasonWaitingForStoragePool))
		})

		It("sets Ready=False when eligible nodes validation fails", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rsc-1",
					Generation: 5,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationConsistencyAndAvailability,
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					StoragePoolBasedOnGeneration:     5,
					StoragePoolEligibleNodesRevision: 1, // Different from RSP.
					Conditions: []metav1.Condition{
						{
							Type:               v1alpha1.ReplicatedStorageClassCondStoragePoolReadyType,
							Status:             metav1.ConditionTrue,
							Reason:             "Ready",
							ObservedGeneration: 5,
						},
					},
				},
			}
			rsp := &v1alpha1.ReplicatedStoragePool{
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					EligibleNodesRevision: 2, // Changed.
					EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
						{NodeName: "node-1"}, // Not enough for ConsistencyAndAvailability.
					},
				},
			}

			outcome := ensureConfiguration(context.Background(), rsc, rsp)

			Expect(outcome.DidChange()).To(BeTrue())
			cond := obju.GetStatusCondition(rsc, v1alpha1.ReplicatedStorageClassCondReadyType)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedStorageClassCondReadyReasonInsufficientEligibleNodes))
		})

		It("updates StoragePoolEligibleNodesRevision when RSP revision changes and validation passes", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rsc-1",
					Generation: 5,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationNone,
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					StoragePoolBasedOnGeneration:     5,
					StoragePoolEligibleNodesRevision: 1,
					ConfigurationGeneration:          5, // Already in sync.
					Conditions: []metav1.Condition{
						{
							Type:               v1alpha1.ReplicatedStorageClassCondStoragePoolReadyType,
							Status:             metav1.ConditionTrue,
							Reason:             "Ready",
							ObservedGeneration: 5,
						},
					},
				},
			}
			rsp := &v1alpha1.ReplicatedStoragePool{
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					EligibleNodesRevision: 2, // Changed.
					EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
						{NodeName: "node-1"}, // Enough for ReplicationNone.
					},
				},
			}

			outcome := ensureConfiguration(context.Background(), rsc, rsp)

			Expect(outcome.DidChange()).To(BeTrue())
			Expect(rsc.Status.StoragePoolEligibleNodesRevision).To(Equal(int64(2)))
		})

		It("skips configuration update when ConfigurationGeneration matches Generation", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rsc-1",
					Generation: 5,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationNone,
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					StoragePoolBasedOnGeneration:     5,
					StoragePoolName:                  "my-pool",
					StoragePoolEligibleNodesRevision: 2, // Already in sync.
					ConfigurationGeneration:          5, // Already in sync.
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						StoragePoolName: "my-pool",
					},
					Conditions: []metav1.Condition{
						{
							Type:               v1alpha1.ReplicatedStorageClassCondStoragePoolReadyType,
							Status:             metav1.ConditionTrue,
							Reason:             "Ready",
							ObservedGeneration: 5,
						},
					},
				},
			}
			rsp := &v1alpha1.ReplicatedStoragePool{
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					EligibleNodesRevision: 2, // Same as rsc.
					EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
						{NodeName: "node-1"},
					},
				},
			}

			outcome := ensureConfiguration(context.Background(), rsc, rsp)

			Expect(outcome.DidChange()).To(BeFalse())
		})

		It("updates configuration and sets Ready=True when generation mismatch", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rsc-1",
					Generation: 6, // New generation.
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication:  v1alpha1.ReplicationNone,
					VolumeAccess: v1alpha1.VolumeAccessPreferablyLocal,
					Topology:     v1alpha1.RSCTopologyIgnored,
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					StoragePoolBasedOnGeneration:     6,
					StoragePoolName:                  "my-pool",
					StoragePoolEligibleNodesRevision: 2,
					ConfigurationGeneration:          5, // Old generation.
					Conditions: []metav1.Condition{
						{
							Type:               v1alpha1.ReplicatedStorageClassCondStoragePoolReadyType,
							Status:             metav1.ConditionTrue,
							Reason:             "Ready",
							ObservedGeneration: 6,
						},
					},
				},
			}
			rsp := &v1alpha1.ReplicatedStoragePool{
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					EligibleNodesRevision: 2,
					EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
						{NodeName: "node-1"},
					},
				},
			}

			outcome := ensureConfiguration(context.Background(), rsc, rsp)

			Expect(outcome.DidChange()).To(BeTrue())
			Expect(outcome.OptimisticLockRequired()).To(BeTrue())
			Expect(rsc.Status.ConfigurationGeneration).To(Equal(int64(6)))
			Expect(rsc.Status.Configuration).NotTo(BeNil())
			Expect(rsc.Status.Configuration.StoragePoolName).To(Equal("my-pool"))

			// Ready should be True.
			cond := obju.GetStatusCondition(rsc, v1alpha1.ReplicatedStorageClassCondReadyType)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedStorageClassCondReadyReasonReady))
		})
	})

	Describe("applyStoragePool", func() {
		It("returns true and updates when generation differs", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rsc-1",
					Generation: 5,
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					StoragePoolBasedOnGeneration: 4,
					StoragePoolName:              "old-name",
				},
			}

			changed := applyStoragePool(rsc, "new-name")

			Expect(changed).To(BeTrue())
			Expect(rsc.Status.StoragePoolBasedOnGeneration).To(Equal(int64(5)))
			Expect(rsc.Status.StoragePoolName).To(Equal("new-name"))
		})

		It("returns true and updates when name differs", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rsc-1",
					Generation: 5,
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					StoragePoolBasedOnGeneration: 5,
					StoragePoolName:              "old-name",
				},
			}

			changed := applyStoragePool(rsc, "new-name")

			Expect(changed).To(BeTrue())
			Expect(rsc.Status.StoragePoolName).To(Equal("new-name"))
		})

		It("returns false when already in sync", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rsc-1",
					Generation: 5,
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					StoragePoolBasedOnGeneration: 5,
					StoragePoolName:              "same-name",
				},
			}

			changed := applyStoragePool(rsc, "same-name")

			Expect(changed).To(BeFalse())
		})
	})

	Describe("applyRSPRemoveUsedBy", func() {
		It("removes RSC name and returns true when present", func() {
			rsp := &v1alpha1.ReplicatedStoragePool{
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					UsedBy: v1alpha1.ReplicatedStoragePoolUsedBy{
						ReplicatedStorageClassNames: []string{"rsc-a", "rsc-b", "rsc-c"},
					},
				},
			}

			changed := applyRSPRemoveUsedBy(rsp, "rsc-b")

			Expect(changed).To(BeTrue())
			Expect(rsp.Status.UsedBy.ReplicatedStorageClassNames).To(Equal([]string{"rsc-a", "rsc-c"}))
		})

		It("returns false when RSC name not present", func() {
			rsp := &v1alpha1.ReplicatedStoragePool{
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					UsedBy: v1alpha1.ReplicatedStoragePoolUsedBy{
						ReplicatedStorageClassNames: []string{"rsc-a", "rsc-c"},
					},
				},
			}

			changed := applyRSPRemoveUsedBy(rsp, "rsc-b")

			Expect(changed).To(BeFalse())
			Expect(rsp.Status.UsedBy.ReplicatedStorageClassNames).To(Equal([]string{"rsc-a", "rsc-c"}))
		})

		It("handles empty usedBy list", func() {
			rsp := &v1alpha1.ReplicatedStoragePool{
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					UsedBy: v1alpha1.ReplicatedStoragePoolUsedBy{
						ReplicatedStorageClassNames: []string{},
					},
				},
			}

			changed := applyRSPRemoveUsedBy(rsp, "rsc-a")

			Expect(changed).To(BeFalse())
			Expect(rsp.Status.UsedBy.ReplicatedStorageClassNames).To(BeEmpty())
		})

		It("removes last element correctly", func() {
			rsp := &v1alpha1.ReplicatedStoragePool{
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					UsedBy: v1alpha1.ReplicatedStoragePoolUsedBy{
						ReplicatedStorageClassNames: []string{"rsc-only"},
					},
				},
			}

			changed := applyRSPRemoveUsedBy(rsp, "rsc-only")

			Expect(changed).To(BeTrue())
			Expect(rsp.Status.UsedBy.ReplicatedStorageClassNames).To(BeEmpty())
		})
	})

	Describe("computeStoragePoolChecksum", func() {
		It("produces deterministic output for same parameters", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Storage: v1alpha1.ReplicatedStorageClassStorage{
						Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
							{Name: "lvg-1"},
							{Name: "lvg-2"},
						},
					},
					Zones:              []string{"zone-a", "zone-b"},
					SystemNetworkNames: []string{"Internal"},
				},
			}

			checksum1 := computeStoragePoolChecksum(rsc)
			checksum2 := computeStoragePoolChecksum(rsc)

			Expect(checksum1).To(Equal(checksum2))
		})

		It("produces same checksum regardless of LVMVolumeGroups order", func() {
			rsc1 := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Storage: v1alpha1.ReplicatedStorageClassStorage{
						Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
							{Name: "lvg-a"},
							{Name: "lvg-b"},
						},
					},
				},
			}
			rsc2 := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-2"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Storage: v1alpha1.ReplicatedStorageClassStorage{
						Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
							{Name: "lvg-b"},
							{Name: "lvg-a"},
						},
					},
				},
			}

			checksum1 := computeStoragePoolChecksum(rsc1)
			checksum2 := computeStoragePoolChecksum(rsc2)

			Expect(checksum1).To(Equal(checksum2))
		})

		It("produces same checksum regardless of zones order", func() {
			rsc1 := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Storage: v1alpha1.ReplicatedStorageClassStorage{
						Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
							{Name: "lvg-1"},
						},
					},
					Zones: []string{"zone-a", "zone-b", "zone-c"},
				},
			}
			rsc2 := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-2"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Storage: v1alpha1.ReplicatedStorageClassStorage{
						Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
							{Name: "lvg-1"},
						},
					},
					Zones: []string{"zone-c", "zone-a", "zone-b"},
				},
			}

			checksum1 := computeStoragePoolChecksum(rsc1)
			checksum2 := computeStoragePoolChecksum(rsc2)

			Expect(checksum1).To(Equal(checksum2))
		})

		It("produces different checksums for different types", func() {
			rsc1 := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Storage: v1alpha1.ReplicatedStorageClassStorage{
						Type:            v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{{Name: "lvg-1"}},
					},
				},
			}
			rsc2 := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-2"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Storage: v1alpha1.ReplicatedStorageClassStorage{
						Type:            v1alpha1.ReplicatedStoragePoolTypeLVMThin,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{{Name: "lvg-1"}},
					},
				},
			}

			checksum1 := computeStoragePoolChecksum(rsc1)
			checksum2 := computeStoragePoolChecksum(rsc2)

			Expect(checksum1).NotTo(Equal(checksum2))
		})

		It("produces different checksums for different LVMVolumeGroups", func() {
			rsc1 := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Storage: v1alpha1.ReplicatedStorageClassStorage{
						Type:            v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{{Name: "lvg-1"}},
					},
				},
			}
			rsc2 := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-2"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Storage: v1alpha1.ReplicatedStorageClassStorage{
						Type:            v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{{Name: "lvg-2"}},
					},
				},
			}

			checksum1 := computeStoragePoolChecksum(rsc1)
			checksum2 := computeStoragePoolChecksum(rsc2)

			Expect(checksum1).NotTo(Equal(checksum2))
		})

		It("produces different checksums for different zones", func() {
			rsc1 := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Storage: v1alpha1.ReplicatedStorageClassStorage{
						Type:            v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{{Name: "lvg-1"}},
					},
					Zones: []string{"zone-a"},
				},
			}
			rsc2 := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-2"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Storage: v1alpha1.ReplicatedStorageClassStorage{
						Type:            v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{{Name: "lvg-1"}},
					},
					Zones: []string{"zone-b"},
				},
			}

			checksum1 := computeStoragePoolChecksum(rsc1)
			checksum2 := computeStoragePoolChecksum(rsc2)

			Expect(checksum1).NotTo(Equal(checksum2))
		})

		It("produces different checksums for different NodeLabelSelector", func() {
			rsc1 := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Storage: v1alpha1.ReplicatedStorageClassStorage{
						Type:            v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{{Name: "lvg-1"}},
					},
					NodeLabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"tier": "storage"},
					},
				},
			}
			rsc2 := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-2"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Storage: v1alpha1.ReplicatedStorageClassStorage{
						Type:            v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{{Name: "lvg-1"}},
					},
					NodeLabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"tier": "compute"},
					},
				},
			}

			checksum1 := computeStoragePoolChecksum(rsc1)
			checksum2 := computeStoragePoolChecksum(rsc2)

			Expect(checksum1).NotTo(Equal(checksum2))
		})

		It("produces 32-character hex string (FNV-128)", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Storage: v1alpha1.ReplicatedStorageClassStorage{
						Type:            v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{{Name: "lvg-1"}},
					},
				},
			}

			checksum := computeStoragePoolChecksum(rsc)

			Expect(checksum).To(HaveLen(32))
			// Verify it's a valid hex string.
			Expect(checksum).To(MatchRegexp("^[0-9a-f]{32}$"))
		})

		It("includes thinPoolName in checksum", func() {
			rsc1 := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Storage: v1alpha1.ReplicatedStorageClassStorage{
						Type:            v1alpha1.ReplicatedStoragePoolTypeLVMThin,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{{Name: "lvg-1", ThinPoolName: "thin-1"}},
					},
				},
			}
			rsc2 := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-2"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Storage: v1alpha1.ReplicatedStorageClassStorage{
						Type:            v1alpha1.ReplicatedStoragePoolTypeLVMThin,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{{Name: "lvg-1", ThinPoolName: "thin-2"}},
					},
				},
			}

			checksum1 := computeStoragePoolChecksum(rsc1)
			checksum2 := computeStoragePoolChecksum(rsc2)

			Expect(checksum1).NotTo(Equal(checksum2))
		})
	})

	Describe("computeTargetStoragePool", func() {
		It("returns auto-rsp-<checksum> format", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rsc-1",
					Generation: 1,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Storage: v1alpha1.ReplicatedStorageClassStorage{
						Type:            v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{{Name: "lvg-1"}},
					},
				},
			}

			name := computeTargetStoragePool(rsc)

			Expect(name).To(HavePrefix("auto-rsp-"))
			Expect(name).To(HaveLen(9 + 32)) // "auto-rsp-" + 32-char checksum
		})

		It("returns cached value when StoragePoolBasedOnGeneration matches Generation", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rsc-1",
					Generation: 5,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Storage: v1alpha1.ReplicatedStorageClassStorage{
						Type:            v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{{Name: "lvg-1"}},
					},
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					StoragePoolBasedOnGeneration: 5, // Matches Generation.
					StoragePoolName:              "auto-rsp-cached-value",
				},
			}

			name := computeTargetStoragePool(rsc)

			Expect(name).To(Equal("auto-rsp-cached-value"))
		})

		It("recomputes when StoragePoolBasedOnGeneration does not match Generation", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rsc-1",
					Generation: 6, // Changed from 5.
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Storage: v1alpha1.ReplicatedStorageClassStorage{
						Type:            v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{{Name: "lvg-1"}},
					},
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					StoragePoolBasedOnGeneration: 5, // Does not match Generation.
					StoragePoolName:              "auto-rsp-old-value",
				},
			}

			name := computeTargetStoragePool(rsc)

			Expect(name).NotTo(Equal("auto-rsp-old-value"))
			Expect(name).To(HavePrefix("auto-rsp-"))
		})

		It("recomputes when StoragePoolName is empty even if generation matches", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rsc-1",
					Generation: 5,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Storage: v1alpha1.ReplicatedStorageClassStorage{
						Type:            v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{{Name: "lvg-1"}},
					},
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					StoragePoolBasedOnGeneration: 5,
					StoragePoolName:              "", // Empty.
				},
			}

			name := computeTargetStoragePool(rsc)

			Expect(name).To(HavePrefix("auto-rsp-"))
			Expect(name).NotTo(BeEmpty())
		})

		It("is deterministic for same spec", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rsc-1",
					Generation: 1,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Storage: v1alpha1.ReplicatedStorageClassStorage{
						Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
							{Name: "lvg-1"},
							{Name: "lvg-2"},
						},
					},
					Zones:              []string{"zone-a", "zone-b"},
					SystemNetworkNames: []string{"Internal"},
				},
			}

			name1 := computeTargetStoragePool(rsc)
			name2 := computeTargetStoragePool(rsc)

			Expect(name1).To(Equal(name2))
		})
	})
})
