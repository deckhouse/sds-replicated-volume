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
	"encoding/hex"
	"encoding/json"
	"hash/fnv"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
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

	// alignedRV is a volume that observed and applied the current configuration and reports
	// every tracked condition as True for its current generation.
	alignedRV := func(name string) rvView {
		return rvView{
			name:                            name,
			configurationGeneration:         1,
			configurationObservedGeneration: 1,
			conditions: rvViewConditions{
				satisfyEligibleNodes:      condTrue(),
				configurationReady:        condTrue(),
				membershipLayoutConverged: condTrue(),
			},
		}
	}

	It("returns zero counts for empty RV list", func() {
		counters := computeActualVolumesSummary(rsc, nil)

		Expect(*counters.Total).To(Equal(int32(0)))
		Expect(*counters.PendingObservation).To(Equal(int32(0)))
		Expect(*counters.Aligned).To(Equal(int32(0)))
		Expect(*counters.StaleConfiguration).To(Equal(int32(0)))
		Expect(*counters.InConflictWithEligibleNodes).To(Equal(int32(0)))
	})

	It("counts volumes that recorded no observed generation as pending", func() {
		rvs := []rvView{
			{name: "rv-1"},
			{name: "rv-2"},
		}

		counters := computeActualVolumesSummary(rsc, rvs)

		Expect(*counters.Total).To(Equal(int32(2)))
		Expect(*counters.PendingObservation).To(Equal(int32(2)))
		Expect(*counters.Aligned).To(Equal(int32(0)))
		Expect(*counters.StaleConfiguration).To(Equal(int32(0)))
	})

	It("counts aligned volumes when all tracked conditions are true", func() {
		counters := computeActualVolumesSummary(rsc, []rvView{alignedRV("rv-1")})

		Expect(*counters.Aligned).To(Equal(int32(1)))
	})

	It("does not count a volume with MembershipLayoutConverged present and false as aligned; counts it as stale", func() {
		rv := alignedRV("rv-1")
		rv.conditions.membershipLayoutConverged = condFalse()

		counters := computeActualVolumesSummary(rsc, []rvView{rv})

		Expect(*counters.Aligned).To(Equal(int32(0)))
		Expect(*counters.StaleConfiguration).To(Equal(int32(1)))
	})

	It("counts a volume with absent MembershipLayoutConverged as pending", func() {
		// MembershipLayoutConverged is only written post-formation. A volume still forming has produced
		// no verdict, which is a pending state — never a silent "rolled out".
		rv := alignedRV("rv-1")
		rv.conditions.membershipLayoutConverged = condMissing()

		counters := computeActualVolumesSummary(rsc, []rvView{rv})

		Expect(*counters.PendingObservation).To(Equal(int32(1)))
		Expect(*counters.Aligned).To(Equal(int32(0)))
		Expect(*counters.StaleConfiguration).To(Equal(int32(0)))
	})

	It("counts configuration not aligned volumes (configurationReady present and false)", func() {
		rv := alignedRV("rv-1")
		rv.conditions.configurationReady = condFalse()

		counters := computeActualVolumesSummary(rsc, []rvView{rv})

		Expect(*counters.StaleConfiguration).To(Equal(int32(1)))
	})

	It("counts eligible nodes not aligned volumes (satisfyEligibleNodes present and false)", func() {
		rv := alignedRV("rv-1")
		rv.conditions.satisfyEligibleNodes = condFalse()

		counters := computeActualVolumesSummary(rsc, []rvView{rv})

		Expect(*counters.InConflictWithEligibleNodes).To(Equal(int32(1)))
	})

	It("does not count RVs with absent SatisfyEligibleNodes condition as in conflict", func() {
		rv := alignedRV("rv-1")
		rv.conditions.satisfyEligibleNodes = condMissing()

		counters := computeActualVolumesSummary(rsc, []rvView{rv})

		Expect(*counters.InConflictWithEligibleNodes).To(Equal(int32(0)))
	})

	It("reports every counter even when a volume is pending", func() {
		pending := alignedRV("rv-1")
		pending.configurationObservedGeneration = 99 // Mismatch: RSC published generation 1.
		pending.conditions.satisfyEligibleNodes = condFalse()

		counters := computeActualVolumesSummary(rsc, []rvView{pending, alignedRV("rv-2")})

		Expect(*counters.Total).To(Equal(int32(2)))
		Expect(*counters.PendingObservation).To(Equal(int32(1)))
		Expect(*counters.Aligned).To(Equal(int32(1)))
		Expect(*counters.StaleConfiguration).To(Equal(int32(0)))
		// inConflictWithEligibleNodes is calculated regardless of the rollout category.
		Expect(*counters.InConflictWithEligibleNodes).To(Equal(int32(1)))
	})

	It("returns all counters when all RVs have acknowledged", func() {
		counters := computeActualVolumesSummary(rsc, []rvView{alignedRV("rv-1")})

		Expect(*counters.Total).To(Equal(int32(1)))
		Expect(*counters.Aligned).To(Equal(int32(1)))
		Expect(*counters.StaleConfiguration).To(Equal(int32(0)))
		Expect(*counters.InConflictWithEligibleNodes).To(Equal(int32(0)))
	})

	It("collects used storage pool names from RVs", func() {
		rvs := []rvView{
			{
				name:                      "rv-1",
				replicatedStoragePoolName: "pool-b",
			},
			{
				name:                      "rv-2",
				replicatedStoragePoolName: "pool-a",
			},
			{
				name:                      "rv-3",
				replicatedStoragePoolName: "pool-b", // Duplicate.
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
				replicatedStoragePoolName:       "pool-a",
				configurationObservedGeneration: 99, // Not acknowledged (non-zero mismatch with RSC's 1).
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
		It("passes with 1 node with disk", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationNone,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1", "", true),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.GetFTT(), rsc.Spec.GetGMDR())

			Expect(err).NotTo(HaveOccurred())
		})

		It("fails with 0 nodes", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationNone,
				},
			}

			err := validateEligibleNodes(nil, rsc.Spec.Topology, rsc.Spec.GetFTT(), rsc.Spec.GetGMDR())

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("No nodes available in the storage pool"))
		})
	})

	Describe("Replication Availability - Ignored topology", func() {
		It("passes with 3 nodes, 2 with disks", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationAvailability,
					Topology:    v1alpha1.TopologyIgnored,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1", "", true),
				makeNode("node-2", "", true),
				makeNode("node-3", "", false),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.GetFTT(), rsc.Spec.GetGMDR())

			Expect(err).NotTo(HaveOccurred())
		})

		It("fails with 2 nodes", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationAvailability,
					Topology:    v1alpha1.TopologyIgnored,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1", "", true),
				makeNode("node-2", "", true),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.GetFTT(), rsc.Spec.GetGMDR())

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 3 nodes"))
		})

		It("fails with 3 nodes but only 1 with disks", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationAvailability,
					Topology:    v1alpha1.TopologyIgnored,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1", "", true),
				makeNode("node-2", "", false),
				makeNode("node-3", "", false),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.GetFTT(), rsc.Spec.GetGMDR())

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 2 nodes with disks"))
		})
	})

	Describe("Replication Availability - TransZonal topology", func() {
		It("passes with 3 zones, 2 with disks", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationAvailability,
					Topology:    v1alpha1.TopologyTransZonal,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1", "zone-a", true),
				makeNode("node-2", "zone-b", true),
				makeNode("node-3", "zone-c", false),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.GetFTT(), rsc.Spec.GetGMDR())

			Expect(err).NotTo(HaveOccurred())
		})

		It("fails with 2 zones", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationAvailability,
					Topology:    v1alpha1.TopologyTransZonal,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1", "zone-a", true),
				makeNode("node-2", "zone-b", true),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.GetFTT(), rsc.Spec.GetGMDR())

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 3 zones"))
		})

		It("fails with 3 zones but only 1 with disks", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationAvailability,
					Topology:    v1alpha1.TopologyTransZonal,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1", "zone-a", true),
				makeNode("node-2", "zone-b", false),
				makeNode("node-3", "zone-c", false),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.GetFTT(), rsc.Spec.GetGMDR())

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 2 zones with disks"))
		})
	})

	Describe("Replication Availability - Zonal topology", func() {
		It("passes with per zone: 3 nodes, 2 with disks", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationAvailability,
					Topology:    v1alpha1.TopologyZonal,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1a", "zone-a", true),
				makeNode("node-2a", "zone-a", true),
				makeNode("node-3a", "zone-a", false),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.GetFTT(), rsc.Spec.GetGMDR())

			Expect(err).NotTo(HaveOccurred())
		})

		It("fails when zone has only 2 nodes", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationAvailability,
					Topology:    v1alpha1.TopologyZonal,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1a", "zone-a", true),
				makeNode("node-2a", "zone-a", true),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.GetFTT(), rsc.Spec.GetGMDR())

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 3 nodes in each zone"))
		})

		It("fails when zone has 3 nodes but only 1 with disks", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationAvailability,
					Topology:    v1alpha1.TopologyZonal,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1a", "zone-a", true),
				makeNode("node-2a", "zone-a", false),
				makeNode("node-3a", "zone-a", false),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.GetFTT(), rsc.Spec.GetGMDR())

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 2 nodes with disks in each zone"))
		})
	})

	Describe("Replication Consistency - Ignored topology", func() {
		It("passes with 2 nodes both with disks", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationConsistency,
					Topology:    v1alpha1.TopologyIgnored,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1", "", true),
				makeNode("node-2", "", true),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.GetFTT(), rsc.Spec.GetGMDR())

			Expect(err).NotTo(HaveOccurred())
		})

		It("fails with 1 node with disks", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationConsistency,
					Topology:    v1alpha1.TopologyIgnored,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1", "", true),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.GetFTT(), rsc.Spec.GetGMDR())

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 2 nodes"))
		})

		It("fails with 2 nodes but only 1 with disks", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationConsistency,
					Topology:    v1alpha1.TopologyIgnored,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1", "", true),
				makeNode("node-2", "", false),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.GetFTT(), rsc.Spec.GetGMDR())

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 2 nodes with disks"))
		})
	})

	Describe("Replication Consistency - TransZonal topology", func() {
		It("passes with 2 zones with disks", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationConsistency,
					Topology:    v1alpha1.TopologyTransZonal,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1", "zone-a", true),
				makeNode("node-2", "zone-b", true),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.GetFTT(), rsc.Spec.GetGMDR())

			Expect(err).NotTo(HaveOccurred())
		})

		It("fails with 1 zone with disks", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationConsistency,
					Topology:    v1alpha1.TopologyTransZonal,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1", "zone-a", true),
				makeNode("node-2", "zone-b", false),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.GetFTT(), rsc.Spec.GetGMDR())

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 2 zones with disks"))
		})
	})

	Describe("Replication Consistency - Zonal topology", func() {
		It("passes with per zone: 2 nodes with disks", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationConsistency,
					Topology:    v1alpha1.TopologyZonal,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1a", "zone-a", true),
				makeNode("node-2a", "zone-a", true),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.GetFTT(), rsc.Spec.GetGMDR())

			Expect(err).NotTo(HaveOccurred())
		})

		It("fails when zone has 1 node with disks", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationConsistency,
					Topology:    v1alpha1.TopologyZonal,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1a", "zone-a", true),
				makeNode("node-2a", "zone-a", false),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.GetFTT(), rsc.Spec.GetGMDR())

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 2 nodes with disks in each zone"))
		})
	})

	Describe("Replication ConsistencyAndAvailability - Ignored topology", func() {
		It("passes with 3 nodes with disks", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationConsistencyAndAvailability,
					Topology:    v1alpha1.TopologyIgnored,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1", "", true),
				makeNode("node-2", "", true),
				makeNode("node-3", "", true),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.GetFTT(), rsc.Spec.GetGMDR())

			Expect(err).NotTo(HaveOccurred())
		})

		It("fails with 2 nodes with disks", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationConsistencyAndAvailability,
					Topology:    v1alpha1.TopologyIgnored,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1", "", true),
				makeNode("node-2", "", true),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.GetFTT(), rsc.Spec.GetGMDR())

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 3 nodes"))
		})
	})

	Describe("Replication ConsistencyAndAvailability - TransZonal topology", func() {
		It("passes with 3 zones with disks", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationConsistencyAndAvailability,
					Topology:    v1alpha1.TopologyTransZonal,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1", "zone-a", true),
				makeNode("node-2", "zone-b", true),
				makeNode("node-3", "zone-c", true),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.GetFTT(), rsc.Spec.GetGMDR())

			Expect(err).NotTo(HaveOccurred())
		})

		It("fails with 2 zones with disks", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationConsistencyAndAvailability,
					Topology:    v1alpha1.TopologyTransZonal,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1", "zone-a", true),
				makeNode("node-2", "zone-b", true),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.GetFTT(), rsc.Spec.GetGMDR())

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 3 zones"))
		})
	})

	Describe("Unsupported combination - TransZonal topology", func() {
		It("reports an honest error for a pre-cap FTT/GMDR combination instead of panicking", func() {
			// Admission caps FTT/GMDR at 1; only an RSC created before the cap can carry
			// higher values. It must reconcile to an InsufficientEligibleNodes condition,
			// not crash-loop the controller.
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1", "zone-a", true),
				makeNode("node-2", "zone-b", true),
				makeNode("node-3", "zone-c", true),
			}

			err := validateEligibleNodes(nodes, v1alpha1.TopologyTransZonal, 2, 1)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("FTT=2, GMDR=1 is not a supported combination"))
		})
	})

	Describe("Replication ConsistencyAndAvailability - Zonal topology", func() {
		It("passes with per zone: 3 nodes with disks", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationConsistencyAndAvailability,
					Topology:    v1alpha1.TopologyZonal,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1a", "zone-a", true),
				makeNode("node-2a", "zone-a", true),
				makeNode("node-3a", "zone-a", true),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.GetFTT(), rsc.Spec.GetGMDR())

			Expect(err).NotTo(HaveOccurred())
		})

		It("fails when zone has 2 nodes with disks", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationConsistencyAndAvailability,
					Topology:    v1alpha1.TopologyZonal,
				},
			}
			nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
				makeNode("node-1a", "zone-a", true),
				makeNode("node-2a", "zone-a", true),
			}

			err := validateEligibleNodes(nodes, rsc.Spec.Topology, rsc.Spec.GetFTT(), rsc.Spec.GetGMDR())

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("at least 3 nodes in each zone"))
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
				Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
					Topology: v1alpha1.TopologyIgnored, FailuresToTolerate: 1, GuaranteedMinimumDataRedundancy: 1,
					VolumeAccess: v1alpha1.VolumeAccessPreferablyLocal, ReplicatedStoragePoolName: "pool-1",
				},
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
				Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
					Topology: v1alpha1.TopologyIgnored, FailuresToTolerate: 1, GuaranteedMinimumDataRedundancy: 1,
					VolumeAccess: v1alpha1.VolumeAccessPreferablyLocal, ReplicatedStoragePoolName: "pool-1",
				},
				ConfigurationGeneration: 5,
			},
		}

		result := isConfigurationInSync(rsc)

		Expect(result).To(BeTrue())
	})
})

var _ = Describe("isEligibleNodesConflictResolutionEnabled", func() {
	It("is enabled for RollingRepair", func() {
		rsc := &v1alpha1.ReplicatedStorageClass{
			Spec: v1alpha1.ReplicatedStorageClassSpec{
				EligibleNodesConflictResolutionStrategy: &v1alpha1.ReplicatedStorageClassEligibleNodesConflictResolutionStrategy{
					Type: v1alpha1.EligibleNodesConflictResolutionRollingRepair,
					RollingRepair: &v1alpha1.ReplicatedStorageClassEligibleNodesConflictResolutionRollingRepair{
						MaxParallel: 10,
					},
				},
			},
		}

		Expect(isEligibleNodesConflictResolutionEnabled(rsc)).To(BeTrue())
	})

	It("is disabled for Manual", func() {
		rsc := &v1alpha1.ReplicatedStorageClass{
			Spec: v1alpha1.ReplicatedStorageClassSpec{
				EligibleNodesConflictResolutionStrategy: &v1alpha1.ReplicatedStorageClassEligibleNodesConflictResolutionStrategy{
					Type: v1alpha1.EligibleNodesConflictResolutionManual,
				},
			},
		}

		Expect(isEligibleNodesConflictResolutionEnabled(rsc)).To(BeFalse())
	})

	It("is enabled when the strategy has not been defaulted yet", func() {
		rsc := &v1alpha1.ReplicatedStorageClass{}

		Expect(isEligibleNodesConflictResolutionEnabled(rsc)).To(BeTrue())
	})
})

var _ = Describe("isConfigurationRolloutEnabled", func() {
	It("is enabled for RollingUpdate", func() {
		rsc := &v1alpha1.ReplicatedStorageClass{
			Spec: v1alpha1.ReplicatedStorageClassSpec{
				ConfigurationRolloutStrategy: &v1alpha1.ReplicatedStorageClassConfigurationRolloutStrategy{
					Type: v1alpha1.ConfigurationRolloutRollingUpdate,
					RollingUpdate: &v1alpha1.ReplicatedStorageClassConfigurationRollingUpdateStrategy{
						MaxParallel: 5,
					},
				},
			},
		}

		Expect(isConfigurationRolloutEnabled(rsc)).To(BeTrue())
	})

	It("is disabled for NewVolumesOnly", func() {
		rsc := &v1alpha1.ReplicatedStorageClass{
			Spec: v1alpha1.ReplicatedStorageClassSpec{
				ConfigurationRolloutStrategy: &v1alpha1.ReplicatedStorageClassConfigurationRolloutStrategy{
					Type: v1alpha1.ConfigurationRolloutNewVolumesOnly,
				},
			},
		}

		Expect(isConfigurationRolloutEnabled(rsc)).To(BeFalse())
	})

	It("is enabled when the strategy has not been defaulted yet", func() {
		rsc := &v1alpha1.ReplicatedStorageClass{}

		Expect(isConfigurationRolloutEnabled(rsc)).To(BeTrue())
	})
})

var _ = Describe("ensureVolumeSummaryAndConditions", func() {
	var (
		ctx context.Context
		rsc *v1alpha1.ReplicatedStorageClass
	)

	// makeAcknowledgedRV creates an rvView that has observed and applied the RSC configuration.
	// Its layout is reported as converged (the common healthy case); tests that need an
	// unconverged layout construct the rvView explicitly.
	makeAcknowledgedRV := func(name string, configOK, nodesOK bool) rvView {
		configReady := condTrue()
		if !configOK {
			configReady = condFalse()
		}
		satisfyEligibleNodes := condTrue()
		if !nodesOK {
			satisfyEligibleNodes = condFalse()
		}
		return rvView{
			name:                            name,
			configurationGeneration:         1,
			configurationObservedGeneration: 1,
			conditions: rvViewConditions{
				satisfyEligibleNodes:      satisfyEligibleNodes,
				configurationReady:        configReady,
				membershipLayoutConverged: condTrue(),
			},
		}
	}

	// makePendingRV creates an rvView that has NOT observed the RSC configuration
	// and has no conditions yet (brand-new RV that hasn't been evaluated).
	makePendingRV := func(name string) rvView {
		return rvView{
			name:                            name,
			configurationObservedGeneration: 99, // Mismatch with the RSC's published generation 1.
		}
	}

	BeforeEach(func() {
		ctx = flow.BeginRootReconcile(context.Background()).Ctx()
		rsc = &v1alpha1.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-rsc",
				Generation: 1,
			},
			Spec: v1alpha1.ReplicatedStorageClassSpec{
				ConfigurationRolloutStrategy: &v1alpha1.ReplicatedStorageClassConfigurationRolloutStrategy{
					Type: v1alpha1.ConfigurationRolloutNewVolumesOnly,
				},
				EligibleNodesConflictResolutionStrategy: &v1alpha1.ReplicatedStorageClassEligibleNodesConflictResolutionStrategy{
					Type: v1alpha1.EligibleNodesConflictResolutionManual,
				},
			},
			Status: v1alpha1.ReplicatedStorageClassStatus{
				ConfigurationGeneration:          1,
				StoragePoolEligibleNodesRevision: 1,
			},
		}
	})

	It("sets ConfigurationRolledOut to Unknown and VolumesSatisfyEligibleNodes to True when all RVs are pending with unknown eligible-nodes condition", func() {
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
		// Pending RVs have no SatisfyEligibleNodes condition yet, so none are "in conflict".
		Expect(rsc.Status.Volumes.InConflictWithEligibleNodes).To(Equal(ptr.To(int32(0))))

		// ConfigurationRolledOut is Unknown because we can't determine config status without acknowledgment.
		configCond := obju.GetStatusCondition(rsc, v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutType)
		Expect(configCond).NotTo(BeNil())
		Expect(configCond.Status).To(Equal(metav1.ConditionUnknown))
		Expect(configCond.Reason).To(Equal(v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutReasonNewConfigurationNotYetObserved))
		Expect(configCond.Message).To(ContainSubstring("3 volume(s) pending observation"))

		// VolumesSatisfyEligibleNodes is True because no RVs are known to be in conflict.
		nodesCond := obju.GetStatusCondition(rsc, v1alpha1.ReplicatedStorageClassCondVolumesSatisfyEligibleNodesType)
		Expect(nodesCond).NotTo(BeNil())
		Expect(nodesCond.Status).To(Equal(metav1.ConditionTrue))
		Expect(nodesCond.Reason).To(Equal(v1alpha1.ReplicatedStorageClassCondVolumesSatisfyEligibleNodesReasonAllVolumesSatisfy))
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
		// Message is honest (reports the stale count), not the old "Not implemented" placeholder.
		Expect(configCond.Message).To(Equal(
			"2 volume(s) not yet aligned with the storage class configuration; automatic rollout is disabled"))
	})

	It("sets ConfigurationRolledOut to False with an honest message when a volume's layout has not converged", func() {
		notConverged := makeAcknowledgedRV("rv-2", true, true)
		notConverged.conditions.membershipLayoutConverged = condFalse() // layout not converged → stale
		rvs := []rvView{
			makeAcknowledgedRV("rv-1", true, true), // fully converged
			notConverged,
		}

		outcome := ensureVolumeSummaryAndConditions(ctx, rsc, rvs)

		Expect(outcome.Error()).To(BeNil())
		Expect(*rsc.Status.Volumes.Aligned).To(Equal(int32(1)))
		Expect(*rsc.Status.Volumes.StaleConfiguration).To(Equal(int32(1)))

		configCond := obju.GetStatusCondition(rsc, v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutType)
		Expect(configCond).NotTo(BeNil())
		Expect(configCond.Status).To(Equal(metav1.ConditionFalse))
		Expect(configCond.Reason).To(Equal(v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutReasonConfigurationRolloutDisabled))
		Expect(configCond.Message).To(Equal(
			"1 volume(s) not yet aligned with the storage class configuration; automatic rollout is disabled"))
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
		Expect(nodesCond.Message).To(Equal("2 volume(s) have replicas on non-eligible nodes; resolution strategy is Manual"))
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
				Topology:     v1alpha1.TopologyTransZonal,
				Replication:  v1alpha1.ReplicationAvailability,
				VolumeAccess: v1alpha1.VolumeAccessLocal,
			},
		}

		config := makeConfiguration(rsc, "my-storage-pool")

		Expect(config.Topology).To(Equal(v1alpha1.TopologyTransZonal))
		Expect(config.FailuresToTolerate).To(Equal(byte(1)))
		Expect(config.GuaranteedMinimumDataRedundancy).To(Equal(byte(0)))
		Expect(config.VolumeAccess).To(Equal(v1alpha1.VolumeAccessLocal))
		Expect(config.ReplicatedStoragePoolName).To(Equal("my-storage-pool"))
	})
})

var _ = Describe("rscShouldBeDeleted", func() {
	It("returns true when rsc is nil", func() {
		Expect(rscShouldBeDeleted(nil, nil)).To(BeTrue())
	})

	It("returns true when rsc is nil even with rvs", func() {
		rvs := []rvView{{name: "rv-1"}}
		Expect(rscShouldBeDeleted(nil, rvs)).To(BeTrue())
	})

	It("returns true when DeletionTimestamp is set and no RVs", func() {
		now := metav1.Now()
		rsc := &v1alpha1.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "rsc-1",
				DeletionTimestamp: &now,
			},
		}
		Expect(rscShouldBeDeleted(rsc, nil)).To(BeTrue())
	})

	It("returns false when DeletionTimestamp is set but RVs exist", func() {
		now := metav1.Now()
		rsc := &v1alpha1.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "rsc-1",
				DeletionTimestamp: &now,
			},
		}
		rvs := []rvView{{name: "rv-1"}}
		Expect(rscShouldBeDeleted(rsc, rvs)).To(BeFalse())
	})

	It("returns false when DeletionTimestamp is nil", func() {
		rsc := &v1alpha1.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
		}
		Expect(rscShouldBeDeleted(rsc, nil)).To(BeFalse())
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
		Expect(storagev1.AddToScheme(scheme)).To(Succeed())
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(snc.AddToScheme(scheme)).To(Succeed())
		cl = nil
		rec = nil
	})

	Describe("Reconcile", func() {
		It("does nothing when RSC is not found", func() {
			cl = testhelpers.WithRSPByUsedByRSCNameIndex(
				testhelpers.WithRVByReplicatedStorageClassNameIndex(
					fake.NewClientBuilder().WithScheme(scheme),
				),
			).Build()
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
			cl = testhelpers.WithRSPByUsedByRSCNameIndex(testhelpers.WithRVByReplicatedStorageClassNameIndex(fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rsc, rsp).
				WithStatusSubresource(rsc, &v1alpha1.ReplicatedStoragePool{}))).
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
			Expect(updatedRSC.Spec.StoragePool).To(BeEmpty()) //nolint:staticcheck // SA1019: testing migration from deprecated StoragePool

			// spec.Storage should contain data from RSP.
			Expect(updatedRSC.Spec.Storage.Type).To(Equal(v1alpha1.ReplicatedStoragePoolTypeLVMThin))
			Expect(updatedRSC.Spec.Storage.LVMVolumeGroups).To(HaveLen(2))
			Expect(updatedRSC.Spec.Storage.LVMVolumeGroups[0].Name).To(Equal("lvg-1"))
			Expect(updatedRSC.Spec.Storage.LVMVolumeGroups[1].Name).To(Equal("lvg-2"))

			// Finalizer should be added.
			Expect(updatedRSC.Finalizers).To(ContainElement(v1alpha1.RSCControllerFinalizer))
		})

		It("keeps already-set spec.Storage and only clears storagePool when they match", func() {
			// Legacy object carrying both storagePool and storage (reachable because the RSC
			// webhook is not registered in legacy installs). Content matches the RSP.
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					StoragePool: "rsp-1",
					Storage: &v1alpha1.ReplicatedStorageClassStorage{
						Type: v1alpha1.ReplicatedStoragePoolTypeLVMThin,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
							{Name: "lvg-1"},
							{Name: "lvg-2"},
						},
					},
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
			cl = testhelpers.WithRSPByUsedByRSCNameIndex(testhelpers.WithRVByReplicatedStorageClassNameIndex(fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rsc, rsp).
				WithStatusSubresource(rsc, &v1alpha1.ReplicatedStoragePool{}))).
				Build()
			rec = NewReconciler(cl)

			result, err := rec.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: client.ObjectKey{Name: "rsc-1"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			var updatedRSC v1alpha1.ReplicatedStorageClass
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "rsc-1"}, &updatedRSC)).To(Succeed())

			// storagePool cleared, storage kept unchanged (not overwritten).
			Expect(updatedRSC.Spec.StoragePool).To(BeEmpty()) //nolint:staticcheck // SA1019: testing migration from deprecated StoragePool
			Expect(updatedRSC.Spec.Storage).NotTo(BeNil())
			Expect(updatedRSC.Spec.Storage.Type).To(Equal(v1alpha1.ReplicatedStoragePoolTypeLVMThin))
			Expect(updatedRSC.Spec.Storage.LVMVolumeGroups).To(HaveLen(2))
			Expect(updatedRSC.Spec.Storage.LVMVolumeGroups[0].Name).To(Equal("lvg-1"))
			Expect(updatedRSC.Spec.Storage.LVMVolumeGroups[1].Name).To(Equal("lvg-2"))

			// Matching content is not a conflict: reconcile continued (finalizer added).
			Expect(updatedRSC.Finalizers).To(ContainElement(v1alpha1.RSCControllerFinalizer))
		})

		It("does not report a conflict when spec.Storage lists the same LVGs in another order", func() {
			// spec.storage.lvmVolumeGroups is a listType=map keyed by name, so its order
			// carries no meaning: the apiserver and server-side apply are free to reorder it.
			// A legacy object whose list happens to be ordered differently from the RSP it
			// migrates from describes exactly the same storage and must not be flagged as a
			// conflict.
			// Type LVM keeps thinPoolName absent on both sides, which is what the CEL
			// rules on ReplicatedStorageClassStorage / ReplicatedStoragePoolSpec require.
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					StoragePool: "rsp-1",
					Storage: &v1alpha1.ReplicatedStorageClassStorage{
						Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
							{Name: "lvg-2"},
							{Name: "lvg-1"},
						},
					},
				},
			}
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
			cl = testhelpers.WithRSPByUsedByRSCNameIndex(testhelpers.WithRVByReplicatedStorageClassNameIndex(fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rsc, rsp).
				WithStatusSubresource(rsc, &v1alpha1.ReplicatedStoragePool{}))).
				Build()
			rec = NewReconciler(cl)

			_, err := rec.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: client.ObjectKey{Name: "rsc-1"},
			})
			Expect(err).NotTo(HaveOccurred())

			var updatedRSC v1alpha1.ReplicatedStorageClass
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "rsc-1"}, &updatedRSC)).To(Succeed())

			Expect(updatedRSC.Status.Phase).NotTo(Equal(v1alpha1.ReplicatedStorageClassPhaseInvalidConfiguration))
			// Equivalent content is not a conflict: reconcile continued (finalizer added).
			Expect(updatedRSC.Finalizers).To(ContainElement(v1alpha1.RSCControllerFinalizer))
		})

		It("keeps already-set spec.Storage and clears storagePool without a one-pass status on content conflict", func() {
			// Legacy object with both fields set, but storage disagrees with the RSP the
			// deprecated storagePool points to. storage is authoritative, so it must not be
			// overwritten (that would silently move the class to another pool). The conflict is
			// resolved right here, so it is only logged: a Ready=False/InvalidConfiguration written
			// for this pass would be gone on the next one, yet the linstor-migrator could read the
			// phase as terminal and block the volumes of the class.
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					StoragePool: "rsp-1",
					Storage: &v1alpha1.ReplicatedStorageClassStorage{
						Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
							{Name: "lvg-existing"},
						},
					},
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
			cl = testhelpers.WithRSPByUsedByRSCNameIndex(testhelpers.WithRVByReplicatedStorageClassNameIndex(fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rsc, rsp).
				WithStatusSubresource(rsc, &v1alpha1.ReplicatedStoragePool{}))).
				Build()
			rec = NewReconciler(cl)

			result, err := rec.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: client.ObjectKey{Name: "rsc-1"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			var updatedRSC v1alpha1.ReplicatedStorageClass
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "rsc-1"}, &updatedRSC)).To(Succeed())

			// storagePool cleared, but existing storage is NOT overwritten with the RSP config.
			Expect(updatedRSC.Spec.StoragePool).To(BeEmpty()) //nolint:staticcheck // SA1019: testing migration from deprecated StoragePool
			Expect(updatedRSC.Spec.Storage).NotTo(BeNil())
			Expect(updatedRSC.Spec.Storage.Type).To(Equal(v1alpha1.ReplicatedStoragePoolTypeLVM))
			Expect(updatedRSC.Spec.Storage.LVMVolumeGroups).To(HaveLen(1))
			Expect(updatedRSC.Spec.Storage.LVMVolumeGroups[0].Name).To(Equal("lvg-existing"))

			// No InvalidConfiguration in status: the class goes on with the regular reconcile.
			readyCond := meta.FindStatusCondition(updatedRSC.Status.Conditions, v1alpha1.ReplicatedStorageClassCondReadyType)
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Reason).NotTo(Equal(v1alpha1.ReplicatedStorageClassCondReadyReasonInvalidConfiguration))
			Expect(updatedRSC.Status.Phase).NotTo(Equal(v1alpha1.ReplicatedStorageClassPhaseInvalidConfiguration))
			Expect(updatedRSC.Finalizers).To(ContainElement(v1alpha1.RSCControllerFinalizer))
		})

		It("sets conditions when RSP is not found", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					StoragePool: "rsp-not-found",
				},
			}
			cl = testhelpers.WithRSPByUsedByRSCNameIndex(testhelpers.WithRVByReplicatedStorageClassNameIndex(fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rsc).
				WithStatusSubresource(rsc))).
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
			Expect(updatedRSC.Spec.StoragePool).To(Equal("rsp-not-found")) //nolint:staticcheck // SA1019: testing migration from deprecated StoragePool

			// Finalizer should NOT be added (reconcileMigrationFromRSP returns Done before reconcileMetadata).
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
					Storage: &v1alpha1.ReplicatedStorageClassStorage{
						Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
							{Name: "lvg-existing"},
						},
					},
				},
			}
			cl = testhelpers.WithRSPByUsedByRSCNameIndex(testhelpers.WithRVByReplicatedStorageClassNameIndex(fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rsc).
				WithStatusSubresource(rsc, &v1alpha1.ReplicatedStoragePool{}))).
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
			Expect(updatedRSC.Spec.StoragePool).To(BeEmpty()) //nolint:staticcheck // SA1019: testing migration from deprecated StoragePool
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
			cl = testhelpers.WithRSPByUsedByRSCNameIndex(testhelpers.WithRVByReplicatedStorageClassNameIndex(fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rsc).
				WithStatusSubresource(rsc))).
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
					Storage: &v1alpha1.ReplicatedStorageClassStorage{
						Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
							{Name: "lvg-1"},
						},
					},
				},
			}
			cl = testhelpers.WithRSPByUsedByRSCNameIndex(testhelpers.WithRVByReplicatedStorageClassNameIndex(fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rsc).
				WithStatusSubresource(rsc, &v1alpha1.ReplicatedStoragePool{}))).
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

		It("reports phase InvalidConfiguration when spec.storage is missing", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
			}
			cl = testhelpers.WithRSPByUsedByRSCNameIndex(testhelpers.WithRVByReplicatedStorageClassNameIndex(fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rsc).
				WithStatusSubresource(rsc, &v1alpha1.ReplicatedStoragePool{}))).
				Build()
			rec = NewReconciler(cl)

			_, err := rec.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: client.ObjectKey{Name: "rsc-1"},
			})
			Expect(err).NotTo(HaveOccurred())

			var updatedRSC v1alpha1.ReplicatedStorageClass
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "rsc-1"}, &updatedRSC)).To(Succeed())
			Expect(updatedRSC.Status.Phase).To(Equal(v1alpha1.ReplicatedStorageClassPhaseInvalidConfiguration))
			Expect(updatedRSC.Status.Message).To(Equal("spec.storage is required"))
		})

		It("moves the class to a new storage pool when eligibleNodesPolicy changes", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1", Generation: 1},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Storage: &v1alpha1.ReplicatedStorageClassStorage{
						Type:            v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{{Name: "lvg-1"}},
					},
					EligibleNodesPolicy: &v1alpha1.ReplicatedStoragePoolEligibleNodesPolicy{
						NotReadyGracePeriod: metav1.Duration{Duration: 10 * time.Minute},
					},
				},
			}
			cl = testhelpers.WithRSPByUsedByRSCNameIndex(testhelpers.WithRVByReplicatedStorageClassNameIndex(fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rsc).
				WithStatusSubresource(rsc, &v1alpha1.ReplicatedStoragePool{}))).
				Build()
			rec = NewReconciler(cl)
			req := reconcile.Request{NamespacedName: client.ObjectKey{Name: "rsc-1"}}

			_, err := rec.Reconcile(context.Background(), req)
			Expect(err).NotTo(HaveOccurred())

			var updatedRSC v1alpha1.ReplicatedStorageClass
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "rsc-1"}, &updatedRSC)).To(Succeed())
			previousPool := updatedRSC.Status.StoragePoolName
			Expect(previousPool).NotTo(BeEmpty())

			// The fake client does not bump metadata.generation on spec updates, so do it here.
			updatedRSC.Spec.EligibleNodesPolicy.NotReadyGracePeriod = metav1.Duration{Duration: 5 * time.Minute}
			updatedRSC.Generation++
			Expect(cl.Update(context.Background(), &updatedRSC)).To(Succeed())

			_, err = rec.Reconcile(context.Background(), req)
			Expect(err).NotTo(HaveOccurred())

			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "rsc-1"}, &updatedRSC)).To(Succeed())
			Expect(updatedRSC.Status.StoragePoolName).NotTo(Equal(previousPool))

			var newRSP v1alpha1.ReplicatedStoragePool
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: updatedRSC.Status.StoragePoolName}, &newRSP)).To(Succeed())
			Expect(newRSP.Spec.EligibleNodesPolicy.NotReadyGracePeriod.Duration).To(Equal(5 * time.Minute))

			// No volume uses the previous pool, so it is released.
			var previousRSP v1alpha1.ReplicatedStoragePool
			err = cl.Get(context.Background(), client.ObjectKey{Name: previousPool}, &previousRSP)
			Expect(err).To(HaveOccurred())
			Expect(client.IgnoreNotFound(err)).To(BeNil())
		})

		It("keeps a pool named by an older checksum until the next spec change of any field", func() {
			// After a module update that changes the pool name format, the name cached for the
			// current generation stays in use; the class moves to the new name only once its spec
			// changes, even when the changed field is not part of the pool identity.
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1", Generation: 1},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Storage: &v1alpha1.ReplicatedStorageClassStorage{
						Type:            v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{{Name: "lvg-1"}},
					},
					ReclaimPolicy: v1alpha1.RSCReclaimPolicyDelete,
					EligibleNodesPolicy: &v1alpha1.ReplicatedStoragePoolEligibleNodesPolicy{
						NotReadyGracePeriod: metav1.Duration{Duration: 10 * time.Minute},
					},
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					StoragePoolName:              "auto-rsp-older-checksum",
					StoragePoolBasedOnGeneration: 1,
				},
			}
			olderRSP := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "auto-rsp-older-checksum",
					Finalizers: []string{v1alpha1.RSCControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedStoragePoolSpec{
					Type:            v1alpha1.ReplicatedStoragePoolTypeLVM,
					LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{{Name: "lvg-1"}},
				},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					UsedBy: v1alpha1.ReplicatedStoragePoolUsedBy{ReplicatedStorageClassNames: []string{"rsc-1"}},
				},
			}
			cl = testhelpers.WithRSPByUsedByRSCNameIndex(testhelpers.WithRVByReplicatedStorageClassNameIndex(fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rsc, olderRSP).
				WithStatusSubresource(rsc, &v1alpha1.ReplicatedStoragePool{}))).
				Build()
			rec = NewReconciler(cl)
			req := reconcile.Request{NamespacedName: client.ObjectKey{Name: "rsc-1"}}

			_, err := rec.Reconcile(context.Background(), req)
			Expect(err).NotTo(HaveOccurred())

			var updatedRSC v1alpha1.ReplicatedStorageClass
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "rsc-1"}, &updatedRSC)).To(Succeed())
			Expect(updatedRSC.Status.StoragePoolName).To(Equal("auto-rsp-older-checksum"))

			// A change unrelated to the pool identity. The fake client does not bump
			// metadata.generation on spec updates, so do it here.
			updatedRSC.Spec.ReclaimPolicy = v1alpha1.RSCReclaimPolicyRetain
			updatedRSC.Generation++
			Expect(cl.Update(context.Background(), &updatedRSC)).To(Succeed())

			_, err = rec.Reconcile(context.Background(), req)
			Expect(err).NotTo(HaveOccurred())

			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "rsc-1"}, &updatedRSC)).To(Succeed())
			Expect(updatedRSC.Status.StoragePoolName).To(Equal("auto-rsp-" + computeStoragePoolChecksum(&updatedRSC)))

			// No volume uses the older pool, so it is released.
			err = cl.Get(context.Background(), client.ObjectKey{Name: "auto-rsp-older-checksum"}, &v1alpha1.ReplicatedStoragePool{})
			Expect(err).To(HaveOccurred())
			Expect(client.IgnoreNotFound(err)).To(BeNil())
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
					Storage: &v1alpha1.ReplicatedStorageClassStorage{
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
			cl = testhelpers.WithRSPByUsedByRSCNameIndex(testhelpers.WithRVByReplicatedStorageClassNameIndex(fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rsc, rv).
				WithStatusSubresource(rsc, &v1alpha1.ReplicatedStoragePool{}))).
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
					Storage: &v1alpha1.ReplicatedStorageClassStorage{
						Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
							{Name: "lvg-1"},
						},
					},
				},
			}
			cl = testhelpers.WithRSPByUsedByRSCNameIndex(testhelpers.WithRVByReplicatedStorageClassNameIndex(fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rsc).
				WithStatusSubresource(rsc))).
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

		It("migrates legacy finalizer to current finalizer on existing RSCs", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rsc-1",
					Finalizers: []string{v1alpha1.RSCControllerFinalizerLegacy},
				},
			}
			cl = fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rsc).
				WithStatusSubresource(rsc).
				Build()
			rec = NewReconciler(cl)

			ctx := flow.BeginRootReconcile(context.Background()).Ctx()
			outcome := rec.reconcileMetadata(ctx, rsc)

			Expect(outcome.Error()).To(BeNil())

			// Legacy finalizer should be removed.
			Expect(rsc.Finalizers).NotTo(ContainElement(v1alpha1.RSCControllerFinalizerLegacy))
			// Current finalizer should be set.
			Expect(rsc.Finalizers).To(ContainElement(v1alpha1.RSCControllerFinalizer))

			// Changes should be persisted.
			var updatedRSC v1alpha1.ReplicatedStorageClass
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "rsc-1"}, &updatedRSC)).To(Succeed())
			Expect(updatedRSC.Finalizers).NotTo(ContainElement(v1alpha1.RSCControllerFinalizerLegacy))
			Expect(updatedRSC.Finalizers).To(ContainElement(v1alpha1.RSCControllerFinalizer))
		})
	})

	Describe("reconcileDeletion", func() {
		It("releases RSPs from usedBy and removes finalizer", func() {
			now := metav1.Now()
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "rsc-1",
					Finalizers:        []string{v1alpha1.RSCControllerFinalizer},
					DeletionTimestamp: &now,
				},
			}
			rsp := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "auto-rsp-abc",
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
				WithObjects(rsc, rsp).
				WithStatusSubresource(rsc, rsp).
				Build()
			rec = NewReconciler(cl)

			ctx := flow.BeginRootReconcile(context.Background()).Ctx()
			outcome := rec.reconcileDeletion(ctx, "rsc-1", rsc, []string{"auto-rsp-abc"})

			Expect(outcome.Error()).To(BeNil())

			// RSP should have rsc-1 removed from usedBy.
			var updatedRSP v1alpha1.ReplicatedStoragePool
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "auto-rsp-abc"}, &updatedRSP)).To(Succeed())
			Expect(updatedRSP.Status.UsedBy.ReplicatedStorageClassNames).To(Equal([]string{"other-rsc"}))

			// Finalizer should be removed from RSC.
			var updatedRSC v1alpha1.ReplicatedStorageClass
			err := cl.Get(context.Background(), client.ObjectKey{Name: "rsc-1"}, &updatedRSC)
			if err == nil {
				Expect(updatedRSC.Finalizers).NotTo(ContainElement(v1alpha1.RSCControllerFinalizer))
			}
		})

		It("cleans usedBy when rsc is nil (already deleted)", func() {
			rsp := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "auto-rsp-abc",
					Finalizers: []string{v1alpha1.RSCControllerFinalizer},
				},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					UsedBy: v1alpha1.ReplicatedStoragePoolUsedBy{
						ReplicatedStorageClassNames: []string{"rsc-deleted"},
					},
				},
			}
			cl = fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rsp).
				WithStatusSubresource(rsp).
				Build()
			rec = NewReconciler(cl)

			ctx := flow.BeginRootReconcile(context.Background()).Ctx()
			outcome := rec.reconcileDeletion(ctx, "rsc-deleted", nil, []string{"auto-rsp-abc"})

			Expect(outcome.Error()).To(BeNil())

			// RSP should be deleted (last user removed).
			var updatedRSP v1alpha1.ReplicatedStoragePool
			err := cl.Get(context.Background(), client.ObjectKey{Name: "auto-rsp-abc"}, &updatedRSP)
			Expect(err).To(HaveOccurred())
			Expect(client.IgnoreNotFound(err)).To(BeNil())
		})

		It("removes finalizer when no RSPs in usedBy", func() {
			now := metav1.Now()
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "rsc-1",
					Finalizers:        []string{v1alpha1.RSCControllerFinalizer},
					DeletionTimestamp: &now,
				},
			}
			cl = fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rsc).
				WithStatusSubresource(rsc).
				Build()
			rec = NewReconciler(cl)

			ctx := flow.BeginRootReconcile(context.Background()).Ctx()
			outcome := rec.reconcileDeletion(ctx, "rsc-1", rsc, nil)

			Expect(outcome.Error()).To(BeNil())

			// RSC should have finalizer removed (and be deleted by fake client).
			var updatedRSC v1alpha1.ReplicatedStorageClass
			err := cl.Get(context.Background(), client.ObjectKey{Name: "rsc-1"}, &updatedRSC)
			Expect(err).To(HaveOccurred())
			Expect(client.IgnoreNotFound(err)).To(BeNil())
		})

		It("does nothing when rsc is nil and no RSPs in usedBy", func() {
			cl = fake.NewClientBuilder().
				WithScheme(scheme).
				Build()
			rec = NewReconciler(cl)

			ctx := flow.BeginRootReconcile(context.Background()).Ctx()
			outcome := rec.reconcileDeletion(ctx, "rsc-gone", nil, nil)

			Expect(outcome.Error()).To(BeNil())
		})

		It("removes legacy finalizer when RSC has only legacy finalizer", func() {
			now := metav1.Now()
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "rsc-1",
					Finalizers:        []string{v1alpha1.RSCControllerFinalizerLegacy},
					DeletionTimestamp: &now,
				},
			}
			cl = fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rsc).
				WithStatusSubresource(rsc).
				Build()
			rec = NewReconciler(cl)

			ctx := flow.BeginRootReconcile(context.Background()).Ctx()
			outcome := rec.reconcileDeletion(ctx, "rsc-1", rsc, nil)

			Expect(outcome.Error()).To(BeNil())

			// RSC should be deleted (legacy finalizer removed).
			var updatedRSC v1alpha1.ReplicatedStorageClass
			err := cl.Get(context.Background(), client.ObjectKey{Name: "rsc-1"}, &updatedRSC)
			Expect(err).To(HaveOccurred())
			Expect(client.IgnoreNotFound(err)).To(BeNil())
		})

		It("removes both legacy and current finalizers when present", func() {
			now := metav1.Now()
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "rsc-1",
					Finalizers: []string{
						v1alpha1.RSCControllerFinalizerLegacy,
						v1alpha1.RSCControllerFinalizer,
					},
					DeletionTimestamp: &now,
				},
			}
			cl = fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(rsc).
				WithStatusSubresource(rsc).
				Build()
			rec = NewReconciler(cl)

			ctx := flow.BeginRootReconcile(context.Background()).Ctx()
			outcome := rec.reconcileDeletion(ctx, "rsc-1", rsc, nil)

			Expect(outcome.Error()).To(BeNil())

			// RSC should be deleted (both finalizers removed).
			var updatedRSC v1alpha1.ReplicatedStorageClass
			err := cl.Get(context.Background(), client.ObjectKey{Name: "rsc-1"}, &updatedRSC)
			Expect(err).To(HaveOccurred())
			Expect(client.IgnoreNotFound(err)).To(BeNil())
		})
	})

	Describe("Reconcile orphaned usedBy cleanup", func() {
		It("cleans up orphaned usedBy when RSC is not found", func() {
			// RSP has a deleted RSC in usedBy but RSC no longer exists.
			rsp := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "auto-rsp-abc",
					Finalizers: []string{v1alpha1.RSCControllerFinalizer},
				},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					UsedBy: v1alpha1.ReplicatedStoragePoolUsedBy{
						ReplicatedStorageClassNames: []string{"rsc-deleted"},
					},
				},
			}
			cl = testhelpers.WithRSPByUsedByRSCNameIndex(
				testhelpers.WithRVByReplicatedStorageClassNameIndex(
					fake.NewClientBuilder().
						WithScheme(scheme).
						WithObjects(rsp).
						WithStatusSubresource(rsp),
				),
			).Build()
			rec = NewReconciler(cl)

			result, err := rec.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: client.ObjectKey{Name: "rsc-deleted"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			// RSP should be deleted (last user removed).
			var updatedRSP v1alpha1.ReplicatedStoragePool
			getErr := cl.Get(context.Background(), client.ObjectKey{Name: "auto-rsp-abc"}, &updatedRSP)
			Expect(getErr).To(HaveOccurred())
			Expect(client.IgnoreNotFound(getErr)).To(BeNil())
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
					Storage: &v1alpha1.ReplicatedStorageClassStorage{
						Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
							{Name: "lvg-1"},
							{Name: "lvg-2"},
						},
					},
					Zones:              []string{"zone-a", "zone-b"},
					SystemNetworkNames: []string{"Internal"},
					EligibleNodesPolicy: &v1alpha1.ReplicatedStoragePoolEligibleNodesPolicy{
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
			rsp, outcome := rec.reconcileRSP(context.Background(), rsc, targetStoragePoolName)

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
					Storage: &v1alpha1.ReplicatedStorageClassStorage{
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

			rsp, outcome := rec.reconcileRSP(context.Background(), rsc, "auto-rsp-existing")

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
					Storage: &v1alpha1.ReplicatedStorageClassStorage{
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

			rsp, outcome := rec.reconcileRSP(context.Background(), rsc, "auto-rsp-existing")

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
					Storage: &v1alpha1.ReplicatedStorageClassStorage{
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

			rsp, outcome := rec.reconcileRSP(context.Background(), rsc, "auto-rsp-existing")

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
					Storage: &v1alpha1.ReplicatedStorageClassStorage{
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
					EligibleNodesPolicy: &v1alpha1.ReplicatedStoragePoolEligibleNodesPolicy{
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
					Storage: &v1alpha1.ReplicatedStorageClassStorage{
						Type:            v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{{Name: "lvg-1"}},
					},
					SystemNetworkNames:  []string{"Internal"},
					EligibleNodesPolicy: &v1alpha1.ReplicatedStoragePoolEligibleNodesPolicy{},
				},
			}

			rsp := newRSP("auto-rsp-xyz", rsc)

			Expect(rsp.Spec.NodeLabelSelector).To(BeNil())
		})

		It("does not share slices with RSC", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Storage: &v1alpha1.ReplicatedStorageClassStorage{
						Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
							{Name: "lvg-1"},
						},
					},
					Zones:               []string{"zone-a"},
					SystemNetworkNames:  []string{"Internal"},
					EligibleNodesPolicy: &v1alpha1.ReplicatedStoragePoolEligibleNodesPolicy{},
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

	Describe("computePhaseAndMessage", func() {
		It("returns Terminating when DeletionTimestamp is set", func() {
			now := metav1.Now()
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &now},
			}

			phase, message := computePhaseAndMessage(rsc)

			Expect(phase).To(Equal(v1alpha1.ReplicatedStorageClassPhaseTerminating))
			Expect(message).To(Equal("Storage class is terminating"))
		})

		It("returns Terminating with the number of volumes it waits for", func() {
			now := metav1.Now()
			total := int32(2)
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &now},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Volumes: v1alpha1.ReplicatedStorageClassVolumesSummary{Total: &total},
				},
			}

			phase, message := computePhaseAndMessage(rsc)

			Expect(phase).To(Equal(v1alpha1.ReplicatedStorageClassPhaseTerminating))
			Expect(message).To(Equal("Storage class is terminating; waiting for 2 volume(s) to be deleted"))
		})

		It("returns WaitingForStoragePool when StoragePoolReady is False", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Conditions: []metav1.Condition{
						{Type: v1alpha1.ReplicatedStorageClassCondStoragePoolReadyType, Status: metav1.ConditionFalse, Message: "RSP not found"},
					},
				},
			}

			phase, message := computePhaseAndMessage(rsc)

			Expect(phase).To(Equal(v1alpha1.ReplicatedStorageClassPhaseWaitingForStoragePool))
			Expect(message).To(Equal("RSP not found"))
		})

		It("returns InsufficientNodes when Ready=False/InsufficientEligibleNodes", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Conditions: []metav1.Condition{
						{Type: v1alpha1.ReplicatedStorageClassCondStoragePoolReadyType, Status: metav1.ConditionTrue},
						{Type: v1alpha1.ReplicatedStorageClassCondReadyType, Status: metav1.ConditionFalse,
							Reason: v1alpha1.ReplicatedStorageClassCondReadyReasonInsufficientEligibleNodes, Message: "need 3 zones"},
					},
				},
			}

			phase, message := computePhaseAndMessage(rsc)

			Expect(phase).To(Equal(v1alpha1.ReplicatedStorageClassPhaseInsufficientNodes))
			Expect(message).To(Equal("need 3 zones"))
		})

		It("returns InvalidConfiguration when Ready=False with other reason", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Conditions: []metav1.Condition{
						{Type: v1alpha1.ReplicatedStorageClassCondStoragePoolReadyType, Status: metav1.ConditionTrue},
						{Type: v1alpha1.ReplicatedStorageClassCondReadyType, Status: metav1.ConditionFalse,
							Reason: v1alpha1.ReplicatedStorageClassCondReadyReasonInvalidConfiguration, Message: "bad config"},
					},
				},
			}

			phase, message := computePhaseAndMessage(rsc)

			Expect(phase).To(Equal(v1alpha1.ReplicatedStorageClassPhaseInvalidConfiguration))
			Expect(message).To(Equal("bad config"))
		})

		It("returns InvalidConfiguration when Ready=False/InvalidConfiguration and StoragePoolReady is absent", func() {
			// spec.storage is missing, so there is no storage pool to wait for.
			rsc := &v1alpha1.ReplicatedStorageClass{
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Conditions: []metav1.Condition{
						{Type: v1alpha1.ReplicatedStorageClassCondReadyType, Status: metav1.ConditionFalse,
							Reason: v1alpha1.ReplicatedStorageClassCondReadyReasonInvalidConfiguration, Message: "spec.storage is required"},
					},
				},
			}

			phase, message := computePhaseAndMessage(rsc)

			Expect(phase).To(Equal(v1alpha1.ReplicatedStorageClassPhaseInvalidConfiguration))
			Expect(message).To(Equal("spec.storage is required"))
		})

		DescribeTable("returns Degraded when Ready=False/Degraded whatever StoragePoolReady reports",
			func(storagePoolReady metav1.ConditionStatus) {
				rsc := &v1alpha1.ReplicatedStorageClass{
					Status: v1alpha1.ReplicatedStorageClassStatus{
						Conditions: []metav1.Condition{
							{Type: v1alpha1.ReplicatedStorageClassCondStoragePoolReadyType, Status: storagePoolReady},
							{Type: v1alpha1.ReplicatedStorageClassCondReadyType, Status: metav1.ConditionFalse,
								Reason: v1alpha1.ReplicatedStorageClassCondReadyReasonDegraded, Message: "pool degraded"},
						},
					},
				}

				phase, message := computePhaseAndMessage(rsc)

				Expect(phase).To(Equal(v1alpha1.ReplicatedStorageClassPhaseDegraded))
				Expect(message).To(Equal("pool degraded"))
			},
			Entry("storage pool not ready", metav1.ConditionFalse),
			Entry("storage pool ready, eligible nodes degraded", metav1.ConditionTrue),
		)

		It("returns Ready when all conditions True and no divergence", func() {
			total := int32(5)
			aligned := int32(5)
			zero := int32(0)
			rsc := &v1alpha1.ReplicatedStorageClass{
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Conditions: []metav1.Condition{
						{Type: v1alpha1.ReplicatedStorageClassCondStoragePoolReadyType, Status: metav1.ConditionTrue},
						{Type: v1alpha1.ReplicatedStorageClassCondReadyType, Status: metav1.ConditionTrue},
						{Type: v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutType, Status: metav1.ConditionTrue},
						{Type: v1alpha1.ReplicatedStorageClassCondVolumesSatisfyEligibleNodesType, Status: metav1.ConditionTrue},
					},
					Volumes: v1alpha1.ReplicatedStorageClassVolumesSummary{
						Total: &total, Aligned: &aligned, StaleConfiguration: &zero, InConflictWithEligibleNodes: &zero, PendingObservation: &zero,
					},
				},
			}

			phase, message := computePhaseAndMessage(rsc)

			Expect(phase).To(Equal(v1alpha1.ReplicatedStorageClassPhaseReady))
			Expect(message).To(ContainSubstring("5 volumes, all aligned"))
		})

		It("returns Ready with no volumes", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Conditions: []metav1.Condition{
						{Type: v1alpha1.ReplicatedStorageClassCondStoragePoolReadyType, Status: metav1.ConditionTrue},
						{Type: v1alpha1.ReplicatedStorageClassCondReadyType, Status: metav1.ConditionTrue},
						{Type: v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutType, Status: metav1.ConditionTrue},
						{Type: v1alpha1.ReplicatedStorageClassCondVolumesSatisfyEligibleNodesType, Status: metav1.ConditionTrue},
					},
				},
			}

			phase, message := computePhaseAndMessage(rsc)

			Expect(phase).To(Equal(v1alpha1.ReplicatedStorageClassPhaseReady))
			Expect(message).To(Equal("Storage class is ready"))
		})

		It("returns RollingOut when config rollout is active", func() {
			total := int32(5)
			aligned := int32(3)
			stale := int32(2)
			zero := int32(0)
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					ConfigurationRolloutStrategy: &v1alpha1.ReplicatedStorageClassConfigurationRolloutStrategy{
						Type: v1alpha1.ConfigurationRolloutRollingUpdate,
					},
					EligibleNodesConflictResolutionStrategy: &v1alpha1.ReplicatedStorageClassEligibleNodesConflictResolutionStrategy{
						Type: v1alpha1.EligibleNodesConflictResolutionManual,
					},
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Conditions: []metav1.Condition{
						{Type: v1alpha1.ReplicatedStorageClassCondStoragePoolReadyType, Status: metav1.ConditionTrue},
						{Type: v1alpha1.ReplicatedStorageClassCondReadyType, Status: metav1.ConditionTrue},
						{Type: v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutType, Status: metav1.ConditionFalse},
						{Type: v1alpha1.ReplicatedStorageClassCondVolumesSatisfyEligibleNodesType, Status: metav1.ConditionTrue},
					},
					Volumes: v1alpha1.ReplicatedStorageClassVolumesSummary{
						Total: &total, Aligned: &aligned, StaleConfiguration: &stale, InConflictWithEligibleNodes: &zero, PendingObservation: &zero,
					},
				},
			}

			phase, message := computePhaseAndMessage(rsc)

			Expect(phase).To(Equal(v1alpha1.ReplicatedStorageClassPhaseRollingOut))
			Expect(message).To(ContainSubstring("Configuration rollout in progress"))
			Expect(message).To(ContainSubstring("3/5"))
		})

		It("returns PartiallyAligned when config rollout is disabled", func() {
			total := int32(5)
			stale := int32(2)
			zero := int32(0)
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					ConfigurationRolloutStrategy: &v1alpha1.ReplicatedStorageClassConfigurationRolloutStrategy{
						Type: v1alpha1.ConfigurationRolloutNewVolumesOnly,
					},
					EligibleNodesConflictResolutionStrategy: &v1alpha1.ReplicatedStorageClassEligibleNodesConflictResolutionStrategy{
						Type: v1alpha1.EligibleNodesConflictResolutionManual,
					},
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Conditions: []metav1.Condition{
						{Type: v1alpha1.ReplicatedStorageClassCondStoragePoolReadyType, Status: metav1.ConditionTrue},
						{Type: v1alpha1.ReplicatedStorageClassCondReadyType, Status: metav1.ConditionTrue},
						{Type: v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutType, Status: metav1.ConditionFalse},
						{Type: v1alpha1.ReplicatedStorageClassCondVolumesSatisfyEligibleNodesType, Status: metav1.ConditionTrue},
					},
					Volumes: v1alpha1.ReplicatedStorageClassVolumesSummary{
						Total: &total, StaleConfiguration: &stale, InConflictWithEligibleNodes: &zero, PendingObservation: &zero,
					},
				},
			}

			phase, message := computePhaseAndMessage(rsc)

			Expect(phase).To(Equal(v1alpha1.ReplicatedStorageClassPhasePartiallyAligned))
			Expect(message).To(ContainSubstring("stale configuration"))
			Expect(message).To(ContainSubstring("NewVolumesOnly"))
		})

		It("returns RollingOut when nodes repair is active even if config is disabled", func() {
			total := int32(5)
			stale := int32(1)
			conflict := int32(1)
			zero := int32(0)
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					ConfigurationRolloutStrategy: &v1alpha1.ReplicatedStorageClassConfigurationRolloutStrategy{
						Type: v1alpha1.ConfigurationRolloutNewVolumesOnly,
					},
					EligibleNodesConflictResolutionStrategy: &v1alpha1.ReplicatedStorageClassEligibleNodesConflictResolutionStrategy{
						Type: v1alpha1.EligibleNodesConflictResolutionRollingRepair,
					},
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Conditions: []metav1.Condition{
						{Type: v1alpha1.ReplicatedStorageClassCondStoragePoolReadyType, Status: metav1.ConditionTrue},
						{Type: v1alpha1.ReplicatedStorageClassCondReadyType, Status: metav1.ConditionTrue},
						{Type: v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutType, Status: metav1.ConditionFalse},
						{Type: v1alpha1.ReplicatedStorageClassCondVolumesSatisfyEligibleNodesType, Status: metav1.ConditionFalse},
					},
					Volumes: v1alpha1.ReplicatedStorageClassVolumesSummary{
						Total: &total, StaleConfiguration: &stale, InConflictWithEligibleNodes: &conflict, PendingObservation: &zero,
					},
				},
			}

			phase, message := computePhaseAndMessage(rsc)

			Expect(phase).To(Equal(v1alpha1.ReplicatedStorageClassPhaseRollingOut))
			Expect(message).To(ContainSubstring("1 volume(s) on non-eligible nodes (resolution strategy: RollingRepair, not implemented yet)"))
			Expect(message).To(ContainSubstring("NewVolumesOnly"))
		})

		It("returns PartiallyAligned when both rollouts are disabled", func() {
			total := int32(5)
			stale := int32(1)
			conflict := int32(1)
			zero := int32(0)
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					ConfigurationRolloutStrategy: &v1alpha1.ReplicatedStorageClassConfigurationRolloutStrategy{
						Type: v1alpha1.ConfigurationRolloutNewVolumesOnly,
					},
					EligibleNodesConflictResolutionStrategy: &v1alpha1.ReplicatedStorageClassEligibleNodesConflictResolutionStrategy{
						Type: v1alpha1.EligibleNodesConflictResolutionManual,
					},
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Conditions: []metav1.Condition{
						{Type: v1alpha1.ReplicatedStorageClassCondStoragePoolReadyType, Status: metav1.ConditionTrue},
						{Type: v1alpha1.ReplicatedStorageClassCondReadyType, Status: metav1.ConditionTrue},
						{Type: v1alpha1.ReplicatedStorageClassCondConfigurationRolledOutType, Status: metav1.ConditionFalse},
						{Type: v1alpha1.ReplicatedStorageClassCondVolumesSatisfyEligibleNodesType, Status: metav1.ConditionFalse},
					},
					Volumes: v1alpha1.ReplicatedStorageClassVolumesSummary{
						Total: &total, StaleConfiguration: &stale, InConflictWithEligibleNodes: &conflict, PendingObservation: &zero,
					},
				},
			}

			phase, message := computePhaseAndMessage(rsc)

			Expect(phase).To(Equal(v1alpha1.ReplicatedStorageClassPhasePartiallyAligned))
			Expect(message).To(ContainSubstring("NewVolumesOnly"))
			Expect(message).To(ContainSubstring("Manual"))
		})
	})

	Describe("applyPhase", func() {
		It("sets phase and message when both are empty", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{}

			changed := applyPhase(rsc, v1alpha1.ReplicatedStorageClassPhaseReady, "ready")

			Expect(changed).To(BeTrue())
			Expect(rsc.Status.Phase).To(Equal(v1alpha1.ReplicatedStorageClassPhaseReady))
			Expect(rsc.Status.Message).To(Equal("ready"))
		})

		It("returns false when nothing changes", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Phase:   v1alpha1.ReplicatedStorageClassPhaseReady,
					Message: "ready",
				},
			}

			changed := applyPhase(rsc, v1alpha1.ReplicatedStorageClassPhaseReady, "ready")

			Expect(changed).To(BeFalse())
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

		It("sets Ready=False when StoragePoolReady is not True (initial config)", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rsc-1",
					Generation: 5,
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					StoragePoolBasedOnGeneration: 5,
					// No StoragePoolReady condition - defaults to not-true.
					// No Configuration - initial config.
				},
			}
			rsp := &v1alpha1.ReplicatedStoragePool{}

			outcome := ensureConfiguration(context.Background(), rsc, rsp)

			Expect(outcome.DidChange()).To(BeTrue())
			cond := obju.GetStatusCondition(rsc, v1alpha1.ReplicatedStorageClassCondReadyType)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedStorageClassCondReadyReasonWaitingForStoragePool))
			Expect(cond.Message).To(Equal("Pending: initial configuration. Waiting for ReplicatedStoragePool to become ready"))
		})

		It("sets Ready=False with diff when StoragePoolReady is not True (config changed)", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rsc-1",
					Generation: 6,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication:  v1alpha1.ReplicationConsistencyAndAvailability,
					Topology:     v1alpha1.TopologyIgnored,
					VolumeAccess: v1alpha1.VolumeAccessPreferablyLocal,
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					StoragePoolBasedOnGeneration: 6,
					StoragePoolName:              "pool-1",
					Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
						FailuresToTolerate: 0, GuaranteedMinimumDataRedundancy: 0,
						Topology:                  v1alpha1.TopologyIgnored,
						VolumeAccess:              v1alpha1.VolumeAccessPreferablyLocal,
						ReplicatedStoragePoolName: "pool-1",
					},
					// No StoragePoolReady condition.
				},
			}
			rsp := &v1alpha1.ReplicatedStoragePool{}

			outcome := ensureConfiguration(context.Background(), rsc, rsp)

			Expect(outcome.DidChange()).To(BeTrue())
			cond := obju.GetStatusCondition(rsc, v1alpha1.ReplicatedStorageClassCondReadyType)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedStorageClassCondReadyReasonWaitingForStoragePool))
			Expect(cond.Message).To(Equal("Pending: failuresToTolerate 0 -> 1, guaranteedMinimumDataRedundancy 0 -> 1. Waiting for ReplicatedStoragePool to become ready"))
		})

		It("sets Ready=False when eligible nodes validation fails (initial config)", func() {
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
			Expect(cond.Message).To(HavePrefix("Pending: initial configuration. "))
		})

		It("sets Ready=False with diff when eligible nodes validation fails (config changed)", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rsc-1",
					Generation: 6,
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication:  v1alpha1.ReplicationConsistencyAndAvailability,
					Topology:     v1alpha1.TopologyIgnored,
					VolumeAccess: v1alpha1.VolumeAccessPreferablyLocal,
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					StoragePoolBasedOnGeneration:     6,
					StoragePoolName:                  "pool-1",
					StoragePoolEligibleNodesRevision: 1,
					Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
						FailuresToTolerate: 0, GuaranteedMinimumDataRedundancy: 0,
						Topology:                  v1alpha1.TopologyIgnored,
						VolumeAccess:              v1alpha1.VolumeAccessPreferablyLocal,
						ReplicatedStoragePoolName: "pool-1",
					},
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
			Expect(cond.Message).To(HavePrefix("Pending: failuresToTolerate 0 -> 1, guaranteedMinimumDataRedundancy 0 -> 1. "))
		})

		Context("accepted configuration", func() {
			// acceptedRSC returns a class whose configuration is accepted for its current generation,
			// with the given StoragePoolReady status.
			acceptedRSC := func(storagePoolReady metav1.ConditionStatus) *v1alpha1.ReplicatedStorageClass {
				return &v1alpha1.ReplicatedStorageClass{
					ObjectMeta: metav1.ObjectMeta{Name: "rsc-1", Generation: 5},
					Spec: v1alpha1.ReplicatedStorageClassSpec{
						Replication:  v1alpha1.ReplicationConsistencyAndAvailability,
						Topology:     v1alpha1.TopologyIgnored,
						VolumeAccess: v1alpha1.VolumeAccessPreferablyLocal,
					},
					Status: v1alpha1.ReplicatedStorageClassStatus{
						StoragePoolBasedOnGeneration:     5,
						StoragePoolName:                  "pool-1",
						StoragePoolEligibleNodesRevision: 1,
						ConfigurationGeneration:          5,
						Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
							FailuresToTolerate: 1, GuaranteedMinimumDataRedundancy: 1,
							Topology:                  v1alpha1.TopologyIgnored,
							VolumeAccess:              v1alpha1.VolumeAccessPreferablyLocal,
							ReplicatedStoragePoolName: "pool-1",
						},
						Conditions: []metav1.Condition{
							{
								Type:               v1alpha1.ReplicatedStorageClassCondStoragePoolReadyType,
								Status:             storagePoolReady,
								Reason:             "Ready",
								ObservedGeneration: 5,
							},
						},
					},
				}
			}

			It("sets Ready=False/Degraded and keeps the configuration when the storage pool is not ready", func() {
				rsc := acceptedRSC(metav1.ConditionFalse)
				configuration := *rsc.Status.Configuration

				outcome := ensureConfiguration(context.Background(), rsc, &v1alpha1.ReplicatedStoragePool{})

				Expect(outcome.DidChange()).To(BeTrue())
				cond := obju.GetStatusCondition(rsc, v1alpha1.ReplicatedStorageClassCondReadyType)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedStorageClassCondReadyReasonDegraded))
				Expect(cond.Message).To(Equal("Storage pool pool-1 is not ready (see StoragePoolReady); configuration generation 5 stays in effect"))
				Expect(*rsc.Status.Configuration).To(Equal(configuration))
				Expect(rsc.Status.ConfigurationGeneration).To(Equal(int64(5)))
			})

			It("sets Ready=False/Degraded and keeps the configuration when eligible nodes no longer suffice", func() {
				rsc := acceptedRSC(metav1.ConditionTrue)
				configuration := *rsc.Status.Configuration
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
				Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedStorageClassCondReadyReasonDegraded))
				Expect(cond.Message).NotTo(HavePrefix("Pending"))
				Expect(cond.Message).To(HaveSuffix("; configuration generation 5 stays in effect"))
				Expect(*rsc.Status.Configuration).To(Equal(configuration))
				// The failed revision is not acknowledged, so it is validated again on the next pass.
				Expect(rsc.Status.StoragePoolEligibleNodesRevision).To(Equal(int64(1)))
			})
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
						{NodeName: "node-1", LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{{Name: "vg-1"}}}, // Enough for FTT=0, GMDR=0.
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
					Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
						Topology:           v1alpha1.TopologyIgnored,
						FailuresToTolerate: 0, GuaranteedMinimumDataRedundancy: 0,
						VolumeAccess:              v1alpha1.VolumeAccessPreferablyLocal,
						ReplicatedStoragePoolName: "my-pool",
					},
					Conditions: []metav1.Condition{
						{
							Type:               v1alpha1.ReplicatedStorageClassCondStoragePoolReadyType,
							Status:             metav1.ConditionTrue,
							Reason:             "Ready",
							ObservedGeneration: 5,
						},
						{
							Type:               v1alpha1.ReplicatedStorageClassCondReadyType,
							Status:             metav1.ConditionTrue,
							Reason:             v1alpha1.ReplicatedStorageClassCondReadyReasonReady,
							Message:            "Storage class is ready",
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

		It("re-asserts Ready=True when in-sync but Ready was cleared by transient error", func() {
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
					StoragePoolEligibleNodesRevision: 2,
					ConfigurationGeneration:          5,
					Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
						Topology:           v1alpha1.TopologyIgnored,
						FailuresToTolerate: 0, GuaranteedMinimumDataRedundancy: 0,
						VolumeAccess:              v1alpha1.VolumeAccessPreferablyLocal,
						ReplicatedStoragePoolName: "my-pool",
					},
					Conditions: []metav1.Condition{
						{
							Type:               v1alpha1.ReplicatedStorageClassCondStoragePoolReadyType,
							Status:             metav1.ConditionTrue,
							Reason:             "Ready",
							ObservedGeneration: 5,
						},
						{
							Type:               v1alpha1.ReplicatedStorageClassCondReadyType,
							Status:             metav1.ConditionFalse,
							Reason:             v1alpha1.ReplicatedStorageClassCondReadyReasonWaitingForStoragePool,
							Message:            "Waiting for ReplicatedStoragePool to become ready",
							ObservedGeneration: 5,
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
			readyCond := meta.FindStatusCondition(rsc.Status.Conditions, v1alpha1.ReplicatedStorageClassCondReadyType)
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
			Expect(readyCond.Reason).To(Equal(v1alpha1.ReplicatedStorageClassCondReadyReasonReady))
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
					Topology:     v1alpha1.TopologyIgnored,
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
						{NodeName: "node-1", LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{{Name: "vg-1"}}},
					},
				},
			}

			outcome := ensureConfiguration(context.Background(), rsc, rsp)

			Expect(outcome.DidChange()).To(BeTrue())
			Expect(rsc.Status.ConfigurationGeneration).To(Equal(int64(6)))
			Expect(rsc.Status.Configuration).NotTo(BeNil())
			Expect(rsc.Status.Configuration.ReplicatedStoragePoolName).To(Equal("my-pool"))

			// Ready should be True.
			cond := obju.GetStatusCondition(rsc, v1alpha1.ReplicatedStorageClassCondReadyType)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedStorageClassCondReadyReasonReady))
		})
	})

	Describe("computePendingConfigurationDiffMessage", func() {
		It("returns 'Pending: initial configuration' when status.configuration is nil", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha1.ReplicationNone,
				},
			}
			msg := computePendingConfigurationDiffMessage(rsc, "pool-1")
			Expect(msg).To(Equal("Pending: initial configuration"))
		})

		It("returns empty string when all fields match", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication:  v1alpha1.ReplicationNone,
					Topology:     v1alpha1.TopologyIgnored,
					VolumeAccess: v1alpha1.VolumeAccessPreferablyLocal,
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
						FailuresToTolerate: 0, GuaranteedMinimumDataRedundancy: 0,
						Topology:                  v1alpha1.TopologyIgnored,
						VolumeAccess:              v1alpha1.VolumeAccessPreferablyLocal,
						ReplicatedStoragePoolName: "pool-1",
					},
				},
			}
			msg := computePendingConfigurationDiffMessage(rsc, "pool-1")
			Expect(msg).To(BeEmpty())
		})

		It("shows replication diff", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication:  v1alpha1.ReplicationConsistencyAndAvailability,
					Topology:     v1alpha1.TopologyIgnored,
					VolumeAccess: v1alpha1.VolumeAccessPreferablyLocal,
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
						FailuresToTolerate: 0, GuaranteedMinimumDataRedundancy: 0,
						Topology:                  v1alpha1.TopologyIgnored,
						VolumeAccess:              v1alpha1.VolumeAccessPreferablyLocal,
						ReplicatedStoragePoolName: "pool-1",
					},
				},
			}
			msg := computePendingConfigurationDiffMessage(rsc, "pool-1")
			Expect(msg).To(Equal("Pending: failuresToTolerate 0 -> 1, guaranteedMinimumDataRedundancy 0 -> 1"))
		})

		It("shows topology diff", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication:  v1alpha1.ReplicationNone,
					Topology:     v1alpha1.TopologyTransZonal,
					VolumeAccess: v1alpha1.VolumeAccessPreferablyLocal,
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
						FailuresToTolerate: 0, GuaranteedMinimumDataRedundancy: 0,
						Topology:                  v1alpha1.TopologyIgnored,
						VolumeAccess:              v1alpha1.VolumeAccessPreferablyLocal,
						ReplicatedStoragePoolName: "pool-1",
					},
				},
			}
			msg := computePendingConfigurationDiffMessage(rsc, "pool-1")
			Expect(msg).To(Equal("Pending: topology Ignored -> TransZonal"))
		})

		It("shows volumeAccess diff", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication:  v1alpha1.ReplicationNone,
					Topology:     v1alpha1.TopologyIgnored,
					VolumeAccess: v1alpha1.VolumeAccessLocal,
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
						FailuresToTolerate: 0, GuaranteedMinimumDataRedundancy: 0,
						Topology:                  v1alpha1.TopologyIgnored,
						VolumeAccess:              v1alpha1.VolumeAccessPreferablyLocal,
						ReplicatedStoragePoolName: "pool-1",
					},
				},
			}
			msg := computePendingConfigurationDiffMessage(rsc, "pool-1")
			Expect(msg).To(Equal("Pending: volumeAccess PreferablyLocal -> Local"))
		})

		It("shows storage pool diff", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication:  v1alpha1.ReplicationNone,
					Topology:     v1alpha1.TopologyIgnored,
					VolumeAccess: v1alpha1.VolumeAccessPreferablyLocal,
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
						FailuresToTolerate: 0, GuaranteedMinimumDataRedundancy: 0,
						Topology:                  v1alpha1.TopologyIgnored,
						VolumeAccess:              v1alpha1.VolumeAccessPreferablyLocal,
						ReplicatedStoragePoolName: "auto-rsp-old",
					},
				},
			}
			msg := computePendingConfigurationDiffMessage(rsc, "auto-rsp-new")
			Expect(msg).To(Equal("Pending: storage: not yet accepted (current pool: auto-rsp-old, pending pool: auto-rsp-new)"))
		})

		It("shows multiple diffs joined by comma", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication:  v1alpha1.ReplicationConsistencyAndAvailability,
					Topology:     v1alpha1.TopologyTransZonal,
					VolumeAccess: v1alpha1.VolumeAccessLocal,
				},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
						FailuresToTolerate: 0, GuaranteedMinimumDataRedundancy: 0,
						Topology:                  v1alpha1.TopologyIgnored,
						VolumeAccess:              v1alpha1.VolumeAccessPreferablyLocal,
						ReplicatedStoragePoolName: "pool-old",
					},
				},
			}
			msg := computePendingConfigurationDiffMessage(rsc, "pool-new")
			Expect(msg).To(Equal("Pending: failuresToTolerate 0 -> 1, guaranteedMinimumDataRedundancy 0 -> 1, topology Ignored -> TransZonal, volumeAccess PreferablyLocal -> Local, storage: not yet accepted (current pool: pool-old, pending pool: pool-new)"))
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

	// eligibleNodesPolicy builds the policy that reconcileDefaults guarantees to be set
	// before the storage pool checksum is computed.
	eligibleNodesPolicy := func(gracePeriod time.Duration) *v1alpha1.ReplicatedStoragePoolEligibleNodesPolicy {
		return &v1alpha1.ReplicatedStoragePoolEligibleNodesPolicy{NotReadyGracePeriod: metav1.Duration{Duration: gracePeriod}}
	}

	Describe("computeStoragePoolChecksum", func() {
		It("produces deterministic output for same parameters", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					EligibleNodesPolicy: eligibleNodesPolicy(10 * time.Minute),
					Storage: &v1alpha1.ReplicatedStorageClassStorage{
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
					EligibleNodesPolicy: eligibleNodesPolicy(10 * time.Minute),
					Storage: &v1alpha1.ReplicatedStorageClassStorage{
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
					EligibleNodesPolicy: eligibleNodesPolicy(10 * time.Minute),
					Storage: &v1alpha1.ReplicatedStorageClassStorage{
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
					EligibleNodesPolicy: eligibleNodesPolicy(10 * time.Minute),
					Storage: &v1alpha1.ReplicatedStorageClassStorage{
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
					EligibleNodesPolicy: eligibleNodesPolicy(10 * time.Minute),
					Storage: &v1alpha1.ReplicatedStorageClassStorage{
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
					EligibleNodesPolicy: eligibleNodesPolicy(10 * time.Minute),
					Storage: &v1alpha1.ReplicatedStorageClassStorage{
						Type:            v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{{Name: "lvg-1"}},
					},
				},
			}
			rsc2 := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-2"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					EligibleNodesPolicy: eligibleNodesPolicy(10 * time.Minute),
					Storage: &v1alpha1.ReplicatedStorageClassStorage{
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
					EligibleNodesPolicy: eligibleNodesPolicy(10 * time.Minute),
					Storage: &v1alpha1.ReplicatedStorageClassStorage{
						Type:            v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{{Name: "lvg-1"}},
					},
				},
			}
			rsc2 := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-2"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					EligibleNodesPolicy: eligibleNodesPolicy(10 * time.Minute),
					Storage: &v1alpha1.ReplicatedStorageClassStorage{
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
					EligibleNodesPolicy: eligibleNodesPolicy(10 * time.Minute),
					Storage: &v1alpha1.ReplicatedStorageClassStorage{
						Type:            v1alpha1.ReplicatedStoragePoolTypeLVM,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{{Name: "lvg-1"}},
					},
					Zones: []string{"zone-a"},
				},
			}
			rsc2 := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-2"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					EligibleNodesPolicy: eligibleNodesPolicy(10 * time.Minute),
					Storage: &v1alpha1.ReplicatedStorageClassStorage{
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
					EligibleNodesPolicy: eligibleNodesPolicy(10 * time.Minute),
					Storage: &v1alpha1.ReplicatedStorageClassStorage{
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
					EligibleNodesPolicy: eligibleNodesPolicy(10 * time.Minute),
					Storage: &v1alpha1.ReplicatedStorageClassStorage{
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

		Context("NodeLabelSelector order", func() {
			rscWithSelector := func(sel *metav1.LabelSelector) *v1alpha1.ReplicatedStorageClass {
				return &v1alpha1.ReplicatedStorageClass{
					ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
					Spec: v1alpha1.ReplicatedStorageClassSpec{
						EligibleNodesPolicy: eligibleNodesPolicy(10 * time.Minute),
						Storage: &v1alpha1.ReplicatedStorageClassStorage{
							Type:            v1alpha1.ReplicatedStoragePoolTypeLVM,
							LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{{Name: "lvg-1"}},
						},
						NodeLabelSelector: sel,
					},
				}
			}
			zoneIn := func(values ...string) metav1.LabelSelectorRequirement {
				return metav1.LabelSelectorRequirement{Key: "zone", Operator: metav1.LabelSelectorOpIn, Values: values}
			}
			tierExists := metav1.LabelSelectorRequirement{Key: "tier", Operator: metav1.LabelSelectorOpExists}

			It("ignores the order of matchExpressions", func() {
				checksum1 := computeStoragePoolChecksum(rscWithSelector(&metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{tierExists, zoneIn("a")},
				}))
				checksum2 := computeStoragePoolChecksum(rscWithSelector(&metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{zoneIn("a"), tierExists},
				}))

				Expect(checksum1).To(Equal(checksum2))
			})

			It("ignores the order of values within a requirement", func() {
				checksum1 := computeStoragePoolChecksum(rscWithSelector(&metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{zoneIn("b", "a")},
				}))
				checksum2 := computeStoragePoolChecksum(rscWithSelector(&metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{zoneIn("a", "b")},
				}))

				Expect(checksum1).To(Equal(checksum2))
			})

			It("does not mutate the selector of the RSC", func() {
				sel := &metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{zoneIn("b", "a"), tierExists},
				}
				computeStoragePoolChecksum(rscWithSelector(sel))

				Expect(sel.MatchExpressions).To(Equal([]metav1.LabelSelectorRequirement{zoneIn("b", "a"), tierExists}))
			})

			It("serializes an already sorted selector as is", func() {
				// The canonical copy of an already sorted selector must serialize exactly like
				// json.Marshal of the selector itself, so normalization never renames a pool.
				sel := &metav1.LabelSelector{
					MatchLabels:      map[string]string{"role": "storage"},
					MatchExpressions: []metav1.LabelSelectorRequirement{tierExists, zoneIn("a", "b")},
				}
				selectorBytes, err := json.Marshal(sel)
				Expect(err).NotTo(HaveOccurred())

				h := fnv.New128a()
				h.Write([]byte(v1alpha1.ReplicatedStoragePoolTypeLVM))
				h.Write([]byte{0})
				h.Write([]byte("lvg-1:"))
				h.Write([]byte{0})
				h.Write(selectorBytes)
				h.Write([]byte{0})
				h.Write([]byte("10m0s"))
				h.Write([]byte{0})

				Expect(computeStoragePoolChecksum(rscWithSelector(sel))).To(Equal(hex.EncodeToString(h.Sum(nil))))
			})
		})

		It("produces 32-character hex string (FNV-128)", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					EligibleNodesPolicy: eligibleNodesPolicy(10 * time.Minute),
					Storage: &v1alpha1.ReplicatedStorageClassStorage{
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
					EligibleNodesPolicy: eligibleNodesPolicy(10 * time.Minute),
					Storage: &v1alpha1.ReplicatedStorageClassStorage{
						Type:            v1alpha1.ReplicatedStoragePoolTypeLVMThin,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{{Name: "lvg-1", ThinPoolName: "thin-1"}},
					},
				},
			}
			rsc2 := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-2"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					EligibleNodesPolicy: eligibleNodesPolicy(10 * time.Minute),
					Storage: &v1alpha1.ReplicatedStorageClassStorage{
						Type:            v1alpha1.ReplicatedStoragePoolTypeLVMThin,
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{{Name: "lvg-1", ThinPoolName: "thin-2"}},
					},
				},
			}

			checksum1 := computeStoragePoolChecksum(rsc1)
			checksum2 := computeStoragePoolChecksum(rsc2)

			Expect(checksum1).NotTo(Equal(checksum2))
		})

		Context("eligibleNodesPolicy", func() {
			rscWithGracePeriod := func(gracePeriod time.Duration) *v1alpha1.ReplicatedStorageClass {
				return &v1alpha1.ReplicatedStorageClass{
					ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
					Spec: v1alpha1.ReplicatedStorageClassSpec{
						Storage: &v1alpha1.ReplicatedStorageClassStorage{
							Type:            v1alpha1.ReplicatedStoragePoolTypeLVM,
							LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{{Name: "lvg-1"}},
						},
						EligibleNodesPolicy: eligibleNodesPolicy(gracePeriod),
					},
				}
			}

			It("produces different checksums for different notReadyGracePeriod", func() {
				checksum1 := computeStoragePoolChecksum(rscWithGracePeriod(10 * time.Minute))
				checksum2 := computeStoragePoolChecksum(rscWithGracePeriod(5 * time.Minute))

				Expect(checksum1).NotTo(Equal(checksum2))
			})
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
					EligibleNodesPolicy: eligibleNodesPolicy(10 * time.Minute),
					Storage: &v1alpha1.ReplicatedStorageClassStorage{
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
					EligibleNodesPolicy: eligibleNodesPolicy(10 * time.Minute),
					Storage: &v1alpha1.ReplicatedStorageClassStorage{
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
					EligibleNodesPolicy: eligibleNodesPolicy(10 * time.Minute),
					Storage: &v1alpha1.ReplicatedStorageClassStorage{
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
					EligibleNodesPolicy: eligibleNodesPolicy(10 * time.Minute),
					Storage: &v1alpha1.ReplicatedStorageClassStorage{
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
					EligibleNodesPolicy: eligibleNodesPolicy(10 * time.Minute),
					Storage: &v1alpha1.ReplicatedStorageClassStorage{
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
