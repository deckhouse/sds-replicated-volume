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
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes/testhelpers"
)

func TestNodeController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "node_controller Reconciler Suite")
}

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
		It("returns Done when node does not exist", func() {
			cl = testhelpers.WithDRBDResourceByNodeNameIndex(
				testhelpers.WithRSPByEligibleNodeNameIndex(
					fake.NewClientBuilder().WithScheme(scheme),
				),
			).Build()
			rec = NewReconciler(cl)

			result, err := rec.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: client.ObjectKey{Name: "non-existent-node"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
		})

		It("does not patch node that is already in sync (no label, no DRBD, not in RSP)", func() {
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-1",
					Labels: map[string]string{},
				},
			}
			cl = testhelpers.WithDRBDResourceByNodeNameIndex(
				testhelpers.WithRSPByEligibleNodeNameIndex(
					fake.NewClientBuilder().WithScheme(scheme).WithObjects(node),
				),
			).Build()
			rec = NewReconciler(cl)

			result, err := rec.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: client.ObjectKey{Name: "node-1"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			var updatedNode corev1.Node
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "node-1"}, &updatedNode)).To(Succeed())
			Expect(updatedNode.Labels).NotTo(HaveKey(v1alpha1.AgentNodeLabelKey))
		})

		It("removes label from node that has no DRBD and is not in any RSP eligibleNodes", func() {
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-1",
					Labels: map[string]string{
						v1alpha1.AgentNodeLabelKey: "node-1",
					},
				},
			}
			cl = testhelpers.WithDRBDResourceByNodeNameIndex(
				testhelpers.WithRSPByEligibleNodeNameIndex(
					fake.NewClientBuilder().WithScheme(scheme).WithObjects(node),
				),
			).Build()
			rec = NewReconciler(cl)

			result, err := rec.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: client.ObjectKey{Name: "node-1"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			var updatedNode corev1.Node
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "node-1"}, &updatedNode)).To(Succeed())
			Expect(updatedNode.Labels).NotTo(HaveKey(v1alpha1.AgentNodeLabelKey))
		})

		It("adds label to node that has DRBDResource", func() {
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-1",
					Labels: map[string]string{},
				},
			}
			drbdResource := &v1alpha1.DRBDResource{
				ObjectMeta: metav1.ObjectMeta{Name: "drbd-1"},
				Spec:       v1alpha1.DRBDResourceSpec{NodeName: "node-1"},
			}
			cl = testhelpers.WithDRBDResourceByNodeNameIndex(
				testhelpers.WithRSPByEligibleNodeNameIndex(
					fake.NewClientBuilder().WithScheme(scheme).WithObjects(node, drbdResource),
				),
			).Build()
			rec = NewReconciler(cl)

			result, err := rec.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: client.ObjectKey{Name: "node-1"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			var updatedNode corev1.Node
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "node-1"}, &updatedNode)).To(Succeed())
			Expect(updatedNode.Labels).To(HaveKeyWithValue(v1alpha1.AgentNodeLabelKey, "node-1"))
		})

		It("adds label to node that is in RSP eligibleNodes", func() {
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-1",
					Labels: map[string]string{},
				},
			}
			rsp := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
						{NodeName: "node-1"},
					},
				},
			}
			cl = testhelpers.WithDRBDResourceByNodeNameIndex(
				testhelpers.WithRSPByEligibleNodeNameIndex(
					fake.NewClientBuilder().WithScheme(scheme).WithObjects(node, rsp),
				),
			).Build()
			rec = NewReconciler(cl)

			result, err := rec.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: client.ObjectKey{Name: "node-1"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			var updatedNode corev1.Node
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "node-1"}, &updatedNode)).To(Succeed())
			Expect(updatedNode.Labels).To(HaveKeyWithValue(v1alpha1.AgentNodeLabelKey, "node-1"))
		})

		It("does not patch node that is already in sync (has label, has DRBD)", func() {
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-1",
					Labels: map[string]string{
						v1alpha1.AgentNodeLabelKey: "node-1",
					},
				},
			}
			drbdResource := &v1alpha1.DRBDResource{
				ObjectMeta: metav1.ObjectMeta{Name: "drbd-1"},
				Spec:       v1alpha1.DRBDResourceSpec{NodeName: "node-1"},
			}
			cl = testhelpers.WithDRBDResourceByNodeNameIndex(
				testhelpers.WithRSPByEligibleNodeNameIndex(
					fake.NewClientBuilder().WithScheme(scheme).WithObjects(node, drbdResource),
				),
			).Build()
			rec = NewReconciler(cl)

			result, err := rec.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: client.ObjectKey{Name: "node-1"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			var updatedNode corev1.Node
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "node-1"}, &updatedNode)).To(Succeed())
			Expect(updatedNode.Labels).To(HaveKeyWithValue(v1alpha1.AgentNodeLabelKey, "node-1"))
		})

		It("does not patch node that is already in sync (has label, in RSP)", func() {
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-1",
					Labels: map[string]string{
						v1alpha1.AgentNodeLabelKey: "node-1",
					},
				},
			}
			rsp := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
						{NodeName: "node-1"},
					},
				},
			}
			cl = testhelpers.WithDRBDResourceByNodeNameIndex(
				testhelpers.WithRSPByEligibleNodeNameIndex(
					fake.NewClientBuilder().WithScheme(scheme).WithObjects(node, rsp),
				),
			).Build()
			rec = NewReconciler(cl)

			result, err := rec.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: client.ObjectKey{Name: "node-1"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			var updatedNode corev1.Node
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "node-1"}, &updatedNode)).To(Succeed())
			Expect(updatedNode.Labels).To(HaveKeyWithValue(v1alpha1.AgentNodeLabelKey, "node-1"))
		})

		It("keeps label on node with DRBD even when not in any RSP", func() {
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-1",
					Labels: map[string]string{
						v1alpha1.AgentNodeLabelKey: "node-1",
					},
				},
			}
			drbdResource := &v1alpha1.DRBDResource{
				ObjectMeta: metav1.ObjectMeta{Name: "drbd-1"},
				Spec:       v1alpha1.DRBDResourceSpec{NodeName: "node-1"},
			}
			// No RSP with this node in eligibleNodes
			cl = testhelpers.WithDRBDResourceByNodeNameIndex(
				testhelpers.WithRSPByEligibleNodeNameIndex(
					fake.NewClientBuilder().WithScheme(scheme).WithObjects(node, drbdResource),
				),
			).Build()
			rec = NewReconciler(cl)

			result, err := rec.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: client.ObjectKey{Name: "node-1"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			var updatedNode corev1.Node
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "node-1"}, &updatedNode)).To(Succeed())
			Expect(updatedNode.Labels).To(HaveKeyWithValue(v1alpha1.AgentNodeLabelKey, "node-1"))
		})

		It("removes label once node is removed from RSP and has no DRBD", func() {
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-1",
					Labels: map[string]string{
						v1alpha1.AgentNodeLabelKey: "node-1",
					},
				},
			}
			// RSP without node-1 in eligibleNodes (simulating removal)
			rsp := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
						{NodeName: "node-2"}, // not node-1
					},
				},
			}
			cl = testhelpers.WithDRBDResourceByNodeNameIndex(
				testhelpers.WithRSPByEligibleNodeNameIndex(
					fake.NewClientBuilder().WithScheme(scheme).WithObjects(node, rsp),
				),
			).Build()
			rec = NewReconciler(cl)

			result, err := rec.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: client.ObjectKey{Name: "node-1"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			var updatedNode corev1.Node
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "node-1"}, &updatedNode)).To(Succeed())
			Expect(updatedNode.Labels).NotTo(HaveKey(v1alpha1.AgentNodeLabelKey))
		})

		It("adds label when node is in multiple RSPs", func() {
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-1",
					Labels: map[string]string{},
				},
			}
			rsp1 := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
						{NodeName: "node-1"},
					},
				},
			}
			rsp2 := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-2"},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
						{NodeName: "node-1"},
					},
				},
			}
			cl = testhelpers.WithDRBDResourceByNodeNameIndex(
				testhelpers.WithRSPByEligibleNodeNameIndex(
					fake.NewClientBuilder().WithScheme(scheme).WithObjects(node, rsp1, rsp2),
				),
			).Build()
			rec = NewReconciler(cl)

			result, err := rec.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: client.ObjectKey{Name: "node-1"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			var updatedNode corev1.Node
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "node-1"}, &updatedNode)).To(Succeed())
			Expect(updatedNode.Labels).To(HaveKeyWithValue(v1alpha1.AgentNodeLabelKey, "node-1"))
		})

		It("adds label when node has multiple DRBDResources", func() {
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-1",
					Labels: map[string]string{},
				},
			}
			drbd1 := &v1alpha1.DRBDResource{
				ObjectMeta: metav1.ObjectMeta{Name: "drbd-1"},
				Spec:       v1alpha1.DRBDResourceSpec{NodeName: "node-1"},
			}
			drbd2 := &v1alpha1.DRBDResource{
				ObjectMeta: metav1.ObjectMeta{Name: "drbd-2"},
				Spec:       v1alpha1.DRBDResourceSpec{NodeName: "node-1"},
			}
			cl = testhelpers.WithDRBDResourceByNodeNameIndex(
				testhelpers.WithRSPByEligibleNodeNameIndex(
					fake.NewClientBuilder().WithScheme(scheme).WithObjects(node, drbd1, drbd2),
				),
			).Build()
			rec = NewReconciler(cl)

			result, err := rec.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: client.ObjectKey{Name: "node-1"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			var updatedNode corev1.Node
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "node-1"}, &updatedNode)).To(Succeed())
			Expect(updatedNode.Labels).To(HaveKeyWithValue(v1alpha1.AgentNodeLabelKey, "node-1"))
		})

		It("handles node with both DRBD and RSP eligibility", func() {
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-1",
					Labels: map[string]string{},
				},
			}
			drbdResource := &v1alpha1.DRBDResource{
				ObjectMeta: metav1.ObjectMeta{Name: "drbd-1"},
				Spec:       v1alpha1.DRBDResourceSpec{NodeName: "node-1"},
			}
			rsp := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
						{NodeName: "node-1"},
					},
				},
			}
			cl = testhelpers.WithDRBDResourceByNodeNameIndex(
				testhelpers.WithRSPByEligibleNodeNameIndex(
					fake.NewClientBuilder().WithScheme(scheme).WithObjects(node, drbdResource, rsp),
				),
			).Build()
			rec = NewReconciler(cl)

			result, err := rec.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: client.ObjectKey{Name: "node-1"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			var updatedNode corev1.Node
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "node-1"}, &updatedNode)).To(Succeed())
			Expect(updatedNode.Labels).To(HaveKeyWithValue(v1alpha1.AgentNodeLabelKey, "node-1"))
		})

		It("only affects the reconciled node, not others", func() {
			node1 := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-1",
					Labels: map[string]string{},
				},
			}
			node2 := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-2",
					Labels: map[string]string{},
				},
			}
			rsp := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
						{NodeName: "node-1"},
						{NodeName: "node-2"},
					},
				},
			}
			cl = testhelpers.WithDRBDResourceByNodeNameIndex(
				testhelpers.WithRSPByEligibleNodeNameIndex(
					fake.NewClientBuilder().WithScheme(scheme).WithObjects(node1, node2, rsp),
				),
			).Build()
			rec = NewReconciler(cl)

			// Reconcile only node-1
			result, err := rec.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: client.ObjectKey{Name: "node-1"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			var updatedNode1 corev1.Node
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "node-1"}, &updatedNode1)).To(Succeed())
			Expect(updatedNode1.Labels).To(HaveKeyWithValue(v1alpha1.AgentNodeLabelKey, "node-1"))

			// node-2 should remain unchanged (no label)
			var updatedNode2 corev1.Node
			Expect(cl.Get(context.Background(), client.ObjectKey{Name: "node-2"}, &updatedNode2)).To(Succeed())
			Expect(updatedNode2.Labels).NotTo(HaveKey(v1alpha1.AgentNodeLabelKey))
		})
	})
})
