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

package nodecontroller

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func TestNodeController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "node_controller Reconciler Suite")
}

var _ = Describe("nodeMatchesRSC", func() {
	var node *corev1.Node

	BeforeEach(func() {
		node = &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-1",
				Labels: map[string]string{
					corev1.LabelTopologyZone: "zone-a",
					"env":                    "prod",
				},
			},
		}
	})

	Context("configuration presence", func() {
		It("returns false when RSC has no configuration", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: nil,
				},
			}

			Expect(nodeMatchesRSC(node, rsc)).To(BeFalse())
		})
	})

	Context("zone matching", func() {
		It("returns true when RSC has no zones specified", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						Zones: nil,
					},
				},
			}

			Expect(nodeMatchesRSC(node, rsc)).To(BeTrue())
		})

		It("returns true when RSC has empty zones", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						Zones: []string{},
					},
				},
			}

			Expect(nodeMatchesRSC(node, rsc)).To(BeTrue())
		})

		It("returns true when node is in one of RSC zones", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						Zones: []string{"zone-a", "zone-b", "zone-c"},
					},
				},
			}

			Expect(nodeMatchesRSC(node, rsc)).To(BeTrue())
		})

		It("returns false when node is not in any of RSC zones", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						Zones: []string{"zone-x", "zone-y"},
					},
				},
			}

			Expect(nodeMatchesRSC(node, rsc)).To(BeFalse())
		})

		It("returns false when node has no zone label but RSC requires zones", func() {
			nodeWithoutZone := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-no-zone",
					Labels: map[string]string{},
				},
			}
			rsc := &v1alpha1.ReplicatedStorageClass{
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						Zones: []string{"zone-a"},
					},
				},
			}

			Expect(nodeMatchesRSC(nodeWithoutZone, rsc)).To(BeFalse())
		})
	})

	Context("nodeLabelSelector matching", func() {
		It("returns true when RSC has no nodeLabelSelector", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						NodeLabelSelector: nil,
					},
				},
			}

			Expect(nodeMatchesRSC(node, rsc)).To(BeTrue())
		})

		It("returns true when node matches nodeLabelSelector", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						NodeLabelSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"env": "prod",
							},
						},
					},
				},
			}

			Expect(nodeMatchesRSC(node, rsc)).To(BeTrue())
		})

		It("returns false when node does not match nodeLabelSelector", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						NodeLabelSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"env": "staging",
							},
						},
					},
				},
			}

			Expect(nodeMatchesRSC(node, rsc)).To(BeFalse())
		})

		It("returns true when node matches nodeLabelSelector with MatchExpressions", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						NodeLabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "env",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"prod", "staging"},
								},
							},
						},
					},
				},
			}

			Expect(nodeMatchesRSC(node, rsc)).To(BeTrue())
		})

		It("returns false when node does not match nodeLabelSelector with MatchExpressions", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						NodeLabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "env",
									Operator: metav1.LabelSelectorOpNotIn,
									Values:   []string{"prod", "staging"},
								},
							},
						},
					},
				},
			}

			Expect(nodeMatchesRSC(node, rsc)).To(BeFalse())
		})

		It("panics when nodeLabelSelector is invalid", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						NodeLabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "env",
									Operator: metav1.LabelSelectorOperator("invalid-operator"),
									Values:   []string{"prod"},
								},
							},
						},
					},
				},
			}

			Expect(func() { nodeMatchesRSC(node, rsc) }).To(Panic())
		})
	})

	Context("combined zone and nodeLabelSelector", func() {
		It("returns true when both zone and nodeLabelSelector match", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						Zones: []string{"zone-a", "zone-b"},
						NodeLabelSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"env": "prod",
							},
						},
					},
				},
			}

			Expect(nodeMatchesRSC(node, rsc)).To(BeTrue())
		})

		It("returns false when zone matches but nodeLabelSelector does not", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						Zones: []string{"zone-a"},
						NodeLabelSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"env": "staging",
							},
						},
					},
				},
			}

			Expect(nodeMatchesRSC(node, rsc)).To(BeFalse())
		})

		It("returns false when nodeLabelSelector matches but zone does not", func() {
			rsc := &v1alpha1.ReplicatedStorageClass{
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						Zones: []string{"zone-x"},
						NodeLabelSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"env": "prod",
							},
						},
					},
				},
			}

			Expect(nodeMatchesRSC(node, rsc)).To(BeFalse())
		})
	})
})

var _ = Describe("nodeMatchesAnyRSC", func() {
	var node *corev1.Node

	BeforeEach(func() {
		node = &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-1",
				Labels: map[string]string{
					corev1.LabelTopologyZone: "zone-a",
				},
			},
		}
	})

	It("returns false when RSC list is empty", func() {
		rscs := []v1alpha1.ReplicatedStorageClass{}

		Expect(nodeMatchesAnyRSC(node, rscs)).To(BeFalse())
	})

	It("returns false when all RSCs have no configuration", func() {
		rscs := []v1alpha1.ReplicatedStorageClass{
			{
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: nil,
				},
			},
			{
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: nil,
				},
			},
		}

		Expect(nodeMatchesAnyRSC(node, rscs)).To(BeFalse())
	})

	It("returns true when node matches at least one RSC", func() {
		rscs := []v1alpha1.ReplicatedStorageClass{
			{
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						Zones: []string{"zone-x"},
					},
				},
			},
			{
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						Zones: []string{"zone-a"}, // matches
					},
				},
			},
		}

		Expect(nodeMatchesAnyRSC(node, rscs)).To(BeTrue())
	})

	It("returns false when node matches no RSC", func() {
		rscs := []v1alpha1.ReplicatedStorageClass{
			{
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						Zones: []string{"zone-x"},
					},
				},
			},
			{
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						Zones: []string{"zone-y"},
					},
				},
			},
		}

		Expect(nodeMatchesAnyRSC(node, rscs)).To(BeFalse())
	})

	It("returns true when node matches first RSC", func() {
		rscs := []v1alpha1.ReplicatedStorageClass{
			{
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						Zones: []string{"zone-a"}, // matches first
					},
				},
			},
			{
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						Zones: []string{"zone-x"},
					},
				},
			},
		}

		Expect(nodeMatchesAnyRSC(node, rscs)).To(BeTrue())
	})

	It("skips RSCs without configuration and matches one with configuration", func() {
		rscs := []v1alpha1.ReplicatedStorageClass{
			{
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: nil, // no configuration — skip
				},
			},
			{
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						Zones: []string{"zone-a"}, // matches
					},
				},
			},
		}

		Expect(nodeMatchesAnyRSC(node, rscs)).To(BeTrue())
	})
})

var _ = Describe("computeNodesWithDRBDResources", func() {
	It("returns empty map when DRBDResources list is empty", func() {
		drbdResources := []v1alpha1.DRBDResource{}

		result := computeNodesWithDRBDResources(drbdResources)

		Expect(result).To(BeEmpty())
	})

	It("returns nodes that have DRBDResources", func() {
		drbdResources := []v1alpha1.DRBDResource{
			{Spec: v1alpha1.DRBDResourceSpec{NodeName: "node-1"}},
			{Spec: v1alpha1.DRBDResourceSpec{NodeName: "node-2"}},
		}

		result := computeNodesWithDRBDResources(drbdResources)

		Expect(result).To(HaveLen(2))
		Expect(result["node-1"]).To(BeTrue())
		Expect(result["node-2"]).To(BeTrue())
	})

	It("handles multiple DRBDResources on the same node", func() {
		drbdResources := []v1alpha1.DRBDResource{
			{Spec: v1alpha1.DRBDResourceSpec{NodeName: "node-1"}},
			{Spec: v1alpha1.DRBDResourceSpec{NodeName: "node-1"}},
			{Spec: v1alpha1.DRBDResourceSpec{NodeName: "node-2"}},
		}

		result := computeNodesWithDRBDResources(drbdResources)

		Expect(result).To(HaveLen(2))
		Expect(result["node-1"]).To(BeTrue())
		Expect(result["node-2"]).To(BeTrue())
	})

	It("skips DRBDResources with empty nodeName", func() {
		drbdResources := []v1alpha1.DRBDResource{
			{Spec: v1alpha1.DRBDResourceSpec{NodeName: "node-1"}},
			{Spec: v1alpha1.DRBDResourceSpec{NodeName: ""}},
		}

		result := computeNodesWithDRBDResources(drbdResources)

		Expect(result).To(HaveLen(1))
		Expect(result["node-1"]).To(BeTrue())
	})
})

var _ = Describe("computeTargetNodes", func() {
	var emptyDRBDResources []v1alpha1.DRBDResource

	BeforeEach(func() {
		emptyDRBDResources = []v1alpha1.DRBDResource{}
	})

	It("returns empty map when both RSCs and nodes are empty", func() {
		rscs := []v1alpha1.ReplicatedStorageClass{}
		nodes := []corev1.Node{}

		target := computeTargetNodes(rscs, emptyDRBDResources, nodes)

		Expect(target).To(BeEmpty())
	})

	It("returns all false when no RSCs exist", func() {
		rscs := []v1alpha1.ReplicatedStorageClass{}
		nodes := []corev1.Node{
			{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}},
			{ObjectMeta: metav1.ObjectMeta{Name: "node-2"}},
		}

		target := computeTargetNodes(rscs, emptyDRBDResources, nodes)

		Expect(target).To(HaveLen(2))
		Expect(target["node-1"]).To(BeFalse())
		Expect(target["node-2"]).To(BeFalse())
	})

	It("returns all false when all RSCs have no configuration", func() {
		rscs := []v1alpha1.ReplicatedStorageClass{
			{
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: nil,
				},
			},
		}
		nodes := []corev1.Node{
			{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}},
			{ObjectMeta: metav1.ObjectMeta{Name: "node-2"}},
		}

		target := computeTargetNodes(rscs, emptyDRBDResources, nodes)

		Expect(target).To(HaveLen(2))
		Expect(target["node-1"]).To(BeFalse())
		Expect(target["node-2"]).To(BeFalse())
	})

	It("returns correct target when RSC configuration has no constraints", func() {
		rscs := []v1alpha1.ReplicatedStorageClass{
			{
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{},
				},
			},
		}
		nodes := []corev1.Node{
			{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}},
			{ObjectMeta: metav1.ObjectMeta{Name: "node-2"}},
		}

		target := computeTargetNodes(rscs, emptyDRBDResources, nodes)

		Expect(target).To(HaveLen(2))
		Expect(target["node-1"]).To(BeTrue())
		Expect(target["node-2"]).To(BeTrue())
	})

	It("returns correct target based on zone filtering", func() {
		rscs := []v1alpha1.ReplicatedStorageClass{
			{
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						Zones: []string{"zone-a", "zone-b"},
					},
				},
			},
		}
		nodes := []corev1.Node{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-1",
					Labels: map[string]string{corev1.LabelTopologyZone: "zone-a"},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-2",
					Labels: map[string]string{corev1.LabelTopologyZone: "zone-c"},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-3",
					Labels: map[string]string{corev1.LabelTopologyZone: "zone-b"},
				},
			},
		}

		target := computeTargetNodes(rscs, emptyDRBDResources, nodes)

		Expect(target).To(HaveLen(3))
		Expect(target["node-1"]).To(BeTrue())
		Expect(target["node-2"]).To(BeFalse())
		Expect(target["node-3"]).To(BeTrue())
	})

	It("returns correct target based on nodeLabelSelector filtering", func() {
		rscs := []v1alpha1.ReplicatedStorageClass{
			{
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						NodeLabelSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"storage": "fast",
							},
						},
					},
				},
			},
		}
		nodes := []corev1.Node{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-1",
					Labels: map[string]string{"storage": "fast"},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-2",
					Labels: map[string]string{"storage": "slow"},
				},
			},
		}

		target := computeTargetNodes(rscs, emptyDRBDResources, nodes)

		Expect(target).To(HaveLen(2))
		Expect(target["node-1"]).To(BeTrue())
		Expect(target["node-2"]).To(BeFalse())
	})

	It("returns true if node matches any RSC", func() {
		rscs := []v1alpha1.ReplicatedStorageClass{
			{
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						Zones: []string{"zone-a"},
					},
				},
			},
			{
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						Zones: []string{"zone-b"},
					},
				},
			},
		}
		nodes := []corev1.Node{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-1",
					Labels: map[string]string{corev1.LabelTopologyZone: "zone-a"},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-2",
					Labels: map[string]string{corev1.LabelTopologyZone: "zone-b"},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-3",
					Labels: map[string]string{corev1.LabelTopologyZone: "zone-c"},
				},
			},
		}

		target := computeTargetNodes(rscs, emptyDRBDResources, nodes)

		Expect(target).To(HaveLen(3))
		Expect(target["node-1"]).To(BeTrue())
		Expect(target["node-2"]).To(BeTrue())
		Expect(target["node-3"]).To(BeFalse())
	})

	Context("DRBDResource protection", func() {
		It("returns true for node with DRBDResource even if it does not match any RSC", func() {
			rscs := []v1alpha1.ReplicatedStorageClass{
				{
					Status: v1alpha1.ReplicatedStorageClassStatus{
						Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
							Zones: []string{"zone-a"},
						},
					},
				},
			}
			drbdResources := []v1alpha1.DRBDResource{
				{Spec: v1alpha1.DRBDResourceSpec{NodeName: "node-2"}},
			}
			nodes := []corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "node-1",
						Labels: map[string]string{corev1.LabelTopologyZone: "zone-a"},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "node-2",
						Labels: map[string]string{corev1.LabelTopologyZone: "zone-b"}, // does not match RSC
					},
				},
			}

			target := computeTargetNodes(rscs, drbdResources, nodes)

			Expect(target).To(HaveLen(2))
			Expect(target["node-1"]).To(BeTrue()) // matches RSC
			Expect(target["node-2"]).To(BeTrue()) // has DRBDResource
		})

		It("returns true for node that matches RSC and has DRBDResource", func() {
			rscs := []v1alpha1.ReplicatedStorageClass{
				{
					Status: v1alpha1.ReplicatedStorageClassStatus{
						Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
							Zones: []string{"zone-a"},
						},
					},
				},
			}
			drbdResources := []v1alpha1.DRBDResource{
				{Spec: v1alpha1.DRBDResourceSpec{NodeName: "node-1"}},
			}
			nodes := []corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "node-1",
						Labels: map[string]string{corev1.LabelTopologyZone: "zone-a"},
					},
				},
			}

			target := computeTargetNodes(rscs, drbdResources, nodes)

			Expect(target).To(HaveLen(1))
			Expect(target["node-1"]).To(BeTrue())
		})

		It("returns false for node without DRBDResource and not matching RSC", func() {
			rscs := []v1alpha1.ReplicatedStorageClass{
				{
					Status: v1alpha1.ReplicatedStorageClassStatus{
						Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
							Zones: []string{"zone-a"},
						},
					},
				},
			}
			drbdResources := []v1alpha1.DRBDResource{
				{Spec: v1alpha1.DRBDResourceSpec{NodeName: "node-1"}},
			}
			nodes := []corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "node-1",
						Labels: map[string]string{corev1.LabelTopologyZone: "zone-a"},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "node-2",
						Labels: map[string]string{corev1.LabelTopologyZone: "zone-b"},
					},
				},
			}

			target := computeTargetNodes(rscs, drbdResources, nodes)

			Expect(target).To(HaveLen(2))
			Expect(target["node-1"]).To(BeTrue())  // matches RSC and has DRBDResource
			Expect(target["node-2"]).To(BeFalse()) // neither matches RSC nor has DRBDResource
		})

		It("keeps label when RSC selector changes but node has DRBDResource", func() {
			// This test verifies the main use case: node had RSC match before,
			// RSC selector changed so node no longer matches, but node has DRBDResource.
			rscs := []v1alpha1.ReplicatedStorageClass{
				{
					Status: v1alpha1.ReplicatedStorageClassStatus{
						Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
							NodeLabelSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"tier": "premium", // changed from "standard" to "premium"
								},
							},
						},
					},
				},
			}
			drbdResources := []v1alpha1.DRBDResource{
				{Spec: v1alpha1.DRBDResourceSpec{NodeName: "node-1"}}, // has DRBD on node-1
			}
			nodes := []corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "node-1",
						Labels: map[string]string{"tier": "standard"}, // no longer matches RSC
					},
				},
			}

			target := computeTargetNodes(rscs, drbdResources, nodes)

			Expect(target).To(HaveLen(1))
			Expect(target["node-1"]).To(BeTrue()) // protected by DRBDResource presence
		})
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
		cl = nil
		rec = nil
	})

	Describe("Reconcile", func() {
		It("adds label to node that matches RSC", func() {
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-1",
					Labels: map[string]string{corev1.LabelTopologyZone: "zone-a"},
				},
			}
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						Zones: []string{"zone-a"},
					},
				},
			}
			cl = fake.NewClientBuilder().WithScheme(scheme).WithObjects(node, rsc).Build()
			rec = NewReconciler(cl)

			result, err := rec.Reconcile(context.Background(), reconcile.Request{})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			var updatedNode corev1.Node
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "node-1"}, &updatedNode)).To(Succeed())
			Expect(updatedNode.Labels).To(HaveKey(v1alpha1.AgentNodeLabelKey))
			Expect(updatedNode.Labels[v1alpha1.AgentNodeLabelKey]).To(Equal("node-1"))
		})

		It("removes label from node that does not match any RSC", func() {
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-1",
					Labels: map[string]string{
						corev1.LabelTopologyZone:   "zone-a",
						v1alpha1.AgentNodeLabelKey: "node-1",
					},
				},
			}
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						Zones: []string{"zone-x"}, // node-1 is in zone-a, not zone-x
					},
				},
			}
			cl = fake.NewClientBuilder().WithScheme(scheme).WithObjects(node, rsc).Build()
			rec = NewReconciler(cl)

			result, err := rec.Reconcile(context.Background(), reconcile.Request{})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			var updatedNode corev1.Node
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "node-1"}, &updatedNode)).To(Succeed())
			Expect(updatedNode.Labels).NotTo(HaveKey(v1alpha1.AgentNodeLabelKey))
		})

		It("removes label from node when RSC has no configuration yet", func() {
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-1",
					Labels: map[string]string{
						corev1.LabelTopologyZone:   "zone-a",
						v1alpha1.AgentNodeLabelKey: "node-1",
					},
				},
			}
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: nil, // no configuration yet
				},
			}
			cl = fake.NewClientBuilder().WithScheme(scheme).WithObjects(node, rsc).Build()
			rec = NewReconciler(cl)

			result, err := rec.Reconcile(context.Background(), reconcile.Request{})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			var updatedNode corev1.Node
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "node-1"}, &updatedNode)).To(Succeed())
			Expect(updatedNode.Labels).NotTo(HaveKey(v1alpha1.AgentNodeLabelKey))
		})

		It("does not patch node that is already in sync (has label and should have it)", func() {
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-1",
					Labels: map[string]string{
						corev1.LabelTopologyZone:   "zone-a",
						v1alpha1.AgentNodeLabelKey: "node-1",
					},
				},
			}
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						Zones: []string{"zone-a"},
					},
				},
			}
			cl = fake.NewClientBuilder().WithScheme(scheme).WithObjects(node, rsc).Build()
			rec = NewReconciler(cl)

			result, err := rec.Reconcile(context.Background(), reconcile.Request{})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			var updatedNode corev1.Node
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "node-1"}, &updatedNode)).To(Succeed())
			Expect(updatedNode.Labels).To(HaveKeyWithValue(v1alpha1.AgentNodeLabelKey, "node-1"))
		})

		It("does not patch node that is already in sync (no label and should not have it)", func() {
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-1",
					Labels: map[string]string{corev1.LabelTopologyZone: "zone-a"},
				},
			}
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						Zones: []string{"zone-x"},
					},
				},
			}
			cl = fake.NewClientBuilder().WithScheme(scheme).WithObjects(node, rsc).Build()
			rec = NewReconciler(cl)

			result, err := rec.Reconcile(context.Background(), reconcile.Request{})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			var updatedNode corev1.Node
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "node-1"}, &updatedNode)).To(Succeed())
			Expect(updatedNode.Labels).NotTo(HaveKey(v1alpha1.AgentNodeLabelKey))
		})

		It("handles multiple nodes and RSCs correctly", func() {
			node1 := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-1",
					Labels: map[string]string{corev1.LabelTopologyZone: "zone-a"},
				},
			}
			node2 := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-2",
					Labels: map[string]string{corev1.LabelTopologyZone: "zone-b"},
				},
			}
			node3 := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-3",
					Labels: map[string]string{corev1.LabelTopologyZone: "zone-c"},
				},
			}
			rsc1 := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						Zones: []string{"zone-a"},
					},
				},
			}
			rsc2 := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-2"},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						Zones: []string{"zone-b"},
					},
				},
			}
			cl = fake.NewClientBuilder().WithScheme(scheme).WithObjects(node1, node2, node3, rsc1, rsc2).Build()
			rec = NewReconciler(cl)

			result, err := rec.Reconcile(context.Background(), reconcile.Request{})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			var updatedNode1 corev1.Node
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "node-1"}, &updatedNode1)).To(Succeed())
			Expect(updatedNode1.Labels).To(HaveKey(v1alpha1.AgentNodeLabelKey))

			var updatedNode2 corev1.Node
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "node-2"}, &updatedNode2)).To(Succeed())
			Expect(updatedNode2.Labels).To(HaveKey(v1alpha1.AgentNodeLabelKey))

			var updatedNode3 corev1.Node
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "node-3"}, &updatedNode3)).To(Succeed())
			Expect(updatedNode3.Labels).NotTo(HaveKey(v1alpha1.AgentNodeLabelKey))
		})

		It("removes label from all nodes when no RSCs exist", func() {
			node1 := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-1",
					Labels: map[string]string{
						v1alpha1.AgentNodeLabelKey: "node-1",
					},
				},
			}
			node2 := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-2",
					Labels: map[string]string{
						v1alpha1.AgentNodeLabelKey: "node-2",
					},
				},
			}
			cl = fake.NewClientBuilder().WithScheme(scheme).WithObjects(node1, node2).Build()
			rec = NewReconciler(cl)

			result, err := rec.Reconcile(context.Background(), reconcile.Request{})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			var updatedNode1 corev1.Node
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "node-1"}, &updatedNode1)).To(Succeed())
			Expect(updatedNode1.Labels).NotTo(HaveKey(v1alpha1.AgentNodeLabelKey))

			var updatedNode2 corev1.Node
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "node-2"}, &updatedNode2)).To(Succeed())
			Expect(updatedNode2.Labels).NotTo(HaveKey(v1alpha1.AgentNodeLabelKey))
		})

		It("handles RSC with nodeLabelSelector", func() {
			node1 := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-1",
					Labels: map[string]string{"storage": "fast"},
				},
			}
			node2 := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-2",
					Labels: map[string]string{"storage": "slow"},
				},
			}
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						NodeLabelSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"storage": "fast",
							},
						},
					},
				},
			}
			cl = fake.NewClientBuilder().WithScheme(scheme).WithObjects(node1, node2, rsc).Build()
			rec = NewReconciler(cl)

			result, err := rec.Reconcile(context.Background(), reconcile.Request{})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			var updatedNode1 corev1.Node
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "node-1"}, &updatedNode1)).To(Succeed())
			Expect(updatedNode1.Labels).To(HaveKey(v1alpha1.AgentNodeLabelKey))

			var updatedNode2 corev1.Node
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "node-2"}, &updatedNode2)).To(Succeed())
			Expect(updatedNode2.Labels).NotTo(HaveKey(v1alpha1.AgentNodeLabelKey))
		})

		Context("DRBDResource protection", func() {
			It("keeps label on node with DRBDResource even when RSC selector changes", func() {
				// Scenario: node had the label, RSC selector changed so node no longer matches,
				// but node has DRBDResource — label should be kept.
				node := &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-1",
						Labels: map[string]string{
							"tier":                     "standard",
							v1alpha1.AgentNodeLabelKey: "node-1",
						},
					},
				}
				rsc := &v1alpha1.ReplicatedStorageClass{
					ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
					Status: v1alpha1.ReplicatedStorageClassStatus{
						Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
							NodeLabelSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"tier": "premium", // node-1 has "standard", not "premium"
								},
							},
						},
					},
				}
				drbdResource := &v1alpha1.DRBDResource{
					ObjectMeta: metav1.ObjectMeta{Name: "drbd-1"},
					Spec:       v1alpha1.DRBDResourceSpec{NodeName: "node-1"},
				}
				cl = fake.NewClientBuilder().WithScheme(scheme).WithObjects(node, rsc, drbdResource).Build()
				rec = NewReconciler(cl)

				result, err := rec.Reconcile(context.Background(), reconcile.Request{})

				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))

				var updatedNode corev1.Node
				Expect(cl.Get(context.Background(), client.ObjectKey{Name: "node-1"}, &updatedNode)).To(Succeed())
				Expect(updatedNode.Labels).To(HaveKey(v1alpha1.AgentNodeLabelKey))
			})

			It("adds label to node with DRBDResource even when node does not match any RSC", func() {
				node := &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "node-1",
						Labels: map[string]string{"tier": "standard"},
					},
				}
				rsc := &v1alpha1.ReplicatedStorageClass{
					ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
					Status: v1alpha1.ReplicatedStorageClassStatus{
						Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
							NodeLabelSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"tier": "premium",
								},
							},
						},
					},
				}
				drbdResource := &v1alpha1.DRBDResource{
					ObjectMeta: metav1.ObjectMeta{Name: "drbd-1"},
					Spec:       v1alpha1.DRBDResourceSpec{NodeName: "node-1"},
				}
				cl = fake.NewClientBuilder().WithScheme(scheme).WithObjects(node, rsc, drbdResource).Build()
				rec = NewReconciler(cl)

				result, err := rec.Reconcile(context.Background(), reconcile.Request{})

				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))

				var updatedNode corev1.Node
				Expect(cl.Get(context.Background(), client.ObjectKey{Name: "node-1"}, &updatedNode)).To(Succeed())
				Expect(updatedNode.Labels).To(HaveKey(v1alpha1.AgentNodeLabelKey))
			})

			It("removes label from node without DRBDResource when RSC selector changes", func() {
				node := &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-1",
						Labels: map[string]string{
							"tier":                     "standard",
							v1alpha1.AgentNodeLabelKey: "node-1",
						},
					},
				}
				rsc := &v1alpha1.ReplicatedStorageClass{
					ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
					Status: v1alpha1.ReplicatedStorageClassStatus{
						Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
							NodeLabelSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"tier": "premium",
								},
							},
						},
					},
				}
				// No DRBDResource on node-1
				cl = fake.NewClientBuilder().WithScheme(scheme).WithObjects(node, rsc).Build()
				rec = NewReconciler(cl)

				result, err := rec.Reconcile(context.Background(), reconcile.Request{})

				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))

				var updatedNode corev1.Node
				Expect(cl.Get(context.Background(), client.ObjectKey{Name: "node-1"}, &updatedNode)).To(Succeed())
				Expect(updatedNode.Labels).NotTo(HaveKey(v1alpha1.AgentNodeLabelKey))
			})

			It("removes label once DRBDResource is deleted and node no longer matches RSC", func() {
				// First reconcile: node has DRBDResource, label is kept
				node := &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-1",
						Labels: map[string]string{
							"tier":                     "standard",
							v1alpha1.AgentNodeLabelKey: "node-1",
						},
					},
				}
				rsc := &v1alpha1.ReplicatedStorageClass{
					ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
					Status: v1alpha1.ReplicatedStorageClassStatus{
						Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
							NodeLabelSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"tier": "premium",
								},
							},
						},
					},
				}
				// No DRBDResource — simulating after deletion
				cl = fake.NewClientBuilder().WithScheme(scheme).WithObjects(node, rsc).Build()
				rec = NewReconciler(cl)

				result, err := rec.Reconcile(context.Background(), reconcile.Request{})

				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))

				var updatedNode corev1.Node
				Expect(cl.Get(context.Background(), client.ObjectKey{Name: "node-1"}, &updatedNode)).To(Succeed())
				Expect(updatedNode.Labels).NotTo(HaveKey(v1alpha1.AgentNodeLabelKey))
			})

			It("handles multiple nodes with different DRBDResource presence", func() {
				node1 := &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-1",
						Labels: map[string]string{
							corev1.LabelTopologyZone:   "zone-a",
							v1alpha1.AgentNodeLabelKey: "node-1",
						},
					},
				}
				node2 := &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-2",
						Labels: map[string]string{
							corev1.LabelTopologyZone:   "zone-b",
							v1alpha1.AgentNodeLabelKey: "node-2",
						},
					},
				}
				node3 := &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-3",
						Labels: map[string]string{
							corev1.LabelTopologyZone:   "zone-c",
							v1alpha1.AgentNodeLabelKey: "node-3",
						},
					},
				}
				rsc := &v1alpha1.ReplicatedStorageClass{
					ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
					Status: v1alpha1.ReplicatedStorageClassStatus{
						Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
							Zones: []string{"zone-a"}, // only node-1 matches
						},
					},
				}
				// DRBDResource on node-2
				drbdResource := &v1alpha1.DRBDResource{
					ObjectMeta: metav1.ObjectMeta{Name: "drbd-1"},
					Spec:       v1alpha1.DRBDResourceSpec{NodeName: "node-2"},
				}
				cl = fake.NewClientBuilder().WithScheme(scheme).WithObjects(node1, node2, node3, rsc, drbdResource).Build()
				rec = NewReconciler(cl)

				result, err := rec.Reconcile(context.Background(), reconcile.Request{})

				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))

				var updatedNode1 corev1.Node
				Expect(cl.Get(context.Background(), client.ObjectKey{Name: "node-1"}, &updatedNode1)).To(Succeed())
				Expect(updatedNode1.Labels).To(HaveKey(v1alpha1.AgentNodeLabelKey)) // matches RSC

				var updatedNode2 corev1.Node
				Expect(cl.Get(context.Background(), client.ObjectKey{Name: "node-2"}, &updatedNode2)).To(Succeed())
				Expect(updatedNode2.Labels).To(HaveKey(v1alpha1.AgentNodeLabelKey)) // has DRBDResource

				var updatedNode3 corev1.Node
				Expect(cl.Get(context.Background(), client.ObjectKey{Name: "node-3"}, &updatedNode3)).To(Succeed())
				Expect(updatedNode3.Labels).NotTo(HaveKey(v1alpha1.AgentNodeLabelKey)) // neither
			})
		})
	})
})
