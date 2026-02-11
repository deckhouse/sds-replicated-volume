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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

var _ = Describe("nodePredicates", func() {
	var preds []func(event.TypedUpdateEvent[client.Object]) bool

	BeforeEach(func() {
		predicates := nodePredicates()
		preds = make([]func(event.TypedUpdateEvent[client.Object]) bool, 0)
		for _, p := range predicates {
			if fp, ok := p.(predicate.TypedFuncs[client.Object]); ok {
				if fp.UpdateFunc != nil {
					preds = append(preds, fp.UpdateFunc)
				}
			}
		}
	})

	Describe("UpdateFunc", func() {
		It("returns true when AgentNodeLabelKey is added", func() {
			oldNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-1",
					Labels: map[string]string{},
				},
			}
			newNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-1",
					Labels: map[string]string{
						v1alpha1.AgentNodeLabelKey: "",
					},
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldNode,
				ObjectNew: newNode,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})

		It("returns true when AgentNodeLabelKey is removed", func() {
			oldNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-1",
					Labels: map[string]string{
						v1alpha1.AgentNodeLabelKey: "",
					},
				},
			}
			newNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-1",
					Labels: map[string]string{},
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldNode,
				ObjectNew: newNode,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})

		It("returns false when AgentNodeLabelKey is unchanged (both have)", func() {
			oldNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-1",
					Labels: map[string]string{
						v1alpha1.AgentNodeLabelKey: "",
					},
				},
			}
			newNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-1",
					Labels: map[string]string{
						v1alpha1.AgentNodeLabelKey: "",
					},
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldNode,
				ObjectNew: newNode,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeFalse())
			}
		})

		It("returns false when AgentNodeLabelKey is unchanged (both lack)", func() {
			oldNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-1",
					Labels: map[string]string{},
				},
			}
			newNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-1",
					Labels: map[string]string{},
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldNode,
				ObjectNew: newNode,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeFalse())
			}
		})

		It("returns true when AgentNodeLabelKey value changed from canonical empty to non-empty", func() {
			oldNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-1",
					Labels: map[string]string{
						v1alpha1.AgentNodeLabelKey: "",
					},
				},
			}
			newNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-1",
					Labels: map[string]string{
						v1alpha1.AgentNodeLabelKey: "wrong-value",
					},
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldNode,
				ObjectNew: newNode,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})

		It("returns true when AgentNodeLabelKey value changed from non-empty to canonical empty", func() {
			oldNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-1",
					Labels: map[string]string{
						v1alpha1.AgentNodeLabelKey: "wrong-value",
					},
				},
			}
			newNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-1",
					Labels: map[string]string{
						v1alpha1.AgentNodeLabelKey: "",
					},
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldNode,
				ObjectNew: newNode,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})

		It("returns false when other labels change but AgentNodeLabelKey unchanged", func() {
			oldNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-1",
					Labels: map[string]string{
						"env": "prod",
					},
				},
			}
			newNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-1",
					Labels: map[string]string{
						"env": "staging",
					},
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldNode,
				ObjectNew: newNode,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeFalse())
			}
		})
	})

	Describe("DeleteFunc", func() {
		It("returns false always", func() {
			predicates := nodePredicates()
			for _, p := range predicates {
				if fp, ok := p.(predicate.TypedFuncs[client.Object]); ok && fp.DeleteFunc != nil {
					node := &corev1.Node{
						ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
					}
					e := event.TypedDeleteEvent[client.Object]{Object: node}
					Expect(fp.DeleteFunc(e)).To(BeFalse())
				}
			}
		})
	})
})

var _ = Describe("rspPredicates", func() {
	Describe("UpdateFunc", func() {
		var preds []func(event.TypedUpdateEvent[client.Object]) bool

		BeforeEach(func() {
			predicates := rspPredicates()
			preds = make([]func(event.TypedUpdateEvent[client.Object]) bool, 0)
			for _, p := range predicates {
				if fp, ok := p.(predicate.TypedFuncs[client.Object]); ok && fp.UpdateFunc != nil {
					preds = append(preds, fp.UpdateFunc)
				}
			}
		})

		It("returns true when eligibleNodes changed (node added)", func() {
			oldRSP := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
						{NodeName: "node-1"},
					},
				},
			}
			newRSP := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
						{NodeName: "node-1"},
						{NodeName: "node-2"},
					},
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRSP,
				ObjectNew: newRSP,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})

		It("returns true when eligibleNodes changed (node removed)", func() {
			oldRSP := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
						{NodeName: "node-1"},
						{NodeName: "node-2"},
					},
				},
			}
			newRSP := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
						{NodeName: "node-1"},
					},
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRSP,
				ObjectNew: newRSP,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})

		It("returns true when eligibleNodes changed (different nodes)", func() {
			oldRSP := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
						{NodeName: "node-1"},
					},
				},
			}
			newRSP := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
						{NodeName: "node-2"},
					},
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRSP,
				ObjectNew: newRSP,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})

		It("returns false when eligibleNodes unchanged", func() {
			oldRSP := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
						{NodeName: "node-1"},
						{NodeName: "node-2"},
					},
				},
			}
			newRSP := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
						{NodeName: "node-1"},
						{NodeName: "node-2"},
					},
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRSP,
				ObjectNew: newRSP,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeFalse())
			}
		})

		It("returns false when eligibleNodes both empty", func() {
			oldRSP := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{},
				},
			}
			newRSP := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{},
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRSP,
				ObjectNew: newRSP,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeFalse())
			}
		})

		It("returns true when cast fails (conservative)", func() {
			oldNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
			}
			newNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldNode,
				ObjectNew: newNode,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})

		It("returns true when old is nil (conservative)", func() {
			newRSP := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: nil,
				ObjectNew: newRSP,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})

		It("returns true when new is nil (conservative)", func() {
			oldRSP := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRSP,
				ObjectNew: nil,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})
	})
})

var _ = Describe("eligibleNodesEqual", func() {
	It("returns true for empty slices", func() {
		a := []v1alpha1.ReplicatedStoragePoolEligibleNode{}
		b := []v1alpha1.ReplicatedStoragePoolEligibleNode{}

		Expect(eligibleNodesEqual(a, b)).To(BeTrue())
	})

	It("returns true for nil slices", func() {
		var a []v1alpha1.ReplicatedStoragePoolEligibleNode
		var b []v1alpha1.ReplicatedStoragePoolEligibleNode

		Expect(eligibleNodesEqual(a, b)).To(BeTrue())
	})

	It("returns true for equal slices", func() {
		a := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1"},
			{NodeName: "node-2"},
		}
		b := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1"},
			{NodeName: "node-2"},
		}

		Expect(eligibleNodesEqual(a, b)).To(BeTrue())
	})

	It("returns false for different lengths", func() {
		a := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1"},
		}
		b := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1"},
			{NodeName: "node-2"},
		}

		Expect(eligibleNodesEqual(a, b)).To(BeFalse())
	})

	It("returns false for different node names", func() {
		a := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1"},
			{NodeName: "node-2"},
		}
		b := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1"},
			{NodeName: "node-3"},
		}

		Expect(eligibleNodesEqual(a, b)).To(BeFalse())
	})

	It("ignores other fields in EligibleNode (only compares NodeName)", func() {
		a := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1", ZoneName: "zone-a"},
		}
		b := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1", ZoneName: "zone-b"},
		}

		Expect(eligibleNodesEqual(a, b)).To(BeTrue())
	})

	It("returns true for equal unsorted slices (builds sorted index)", func() {
		// Same nodes but in different order.
		a := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-3"},
			{NodeName: "node-1"},
			{NodeName: "node-2"},
		}
		b := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-2"},
			{NodeName: "node-3"},
			{NodeName: "node-1"},
		}

		Expect(eligibleNodesEqual(a, b)).To(BeTrue())
	})

	It("returns false for different unsorted slices (builds sorted index)", func() {
		// Different nodes in unsorted order.
		a := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-3"},
			{NodeName: "node-1"},
			{NodeName: "node-2"},
		}
		b := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-2"},
			{NodeName: "node-4"}, // Different.
			{NodeName: "node-1"},
		}

		Expect(eligibleNodesEqual(a, b)).To(BeFalse())
	})
})

var _ = Describe("drbdResourcePredicates", func() {
	Describe("UpdateFunc", func() {
		It("returns false always", func() {
			predicates := drbdResourcePredicates()
			for _, p := range predicates {
				if fp, ok := p.(predicate.TypedFuncs[client.Object]); ok && fp.UpdateFunc != nil {
					oldDR := &v1alpha1.DRBDResource{
						ObjectMeta: metav1.ObjectMeta{Name: "drbd-1"},
						Spec:       v1alpha1.DRBDResourceSpec{NodeName: "node-1"},
					}
					newDR := &v1alpha1.DRBDResource{
						ObjectMeta: metav1.ObjectMeta{Name: "drbd-1"},
						Spec:       v1alpha1.DRBDResourceSpec{NodeName: "node-1"},
					}
					e := event.TypedUpdateEvent[client.Object]{
						ObjectOld: oldDR,
						ObjectNew: newDR,
					}
					Expect(fp.UpdateFunc(e)).To(BeFalse())
				}
			}
		})
	})
})
