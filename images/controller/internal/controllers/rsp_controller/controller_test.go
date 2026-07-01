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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes/testhelpers"
)

func requestNames(requests []reconcile.Request) []string {
	names := make([]string, 0, len(requests))
	for _, r := range requests {
		names = append(names, r.Name)
	}
	return names
}

var _ = Describe("mapNodeToRSP", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
	})

	It("returns RSPs where node is in EligibleNodes or could be added", func() {
		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
		}
		rsp1 := &v1alpha1.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
			Status: v1alpha1.ReplicatedStoragePoolStatus{
				EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
					{NodeName: "node-1"},
				},
			},
		}
		// rsp2 has no filtering criteria, so any node could potentially be added.
		rsp2 := &v1alpha1.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: "rsp-2"},
			Status: v1alpha1.ReplicatedStoragePoolStatus{
				EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
					{NodeName: "node-2"},
				},
			},
		}

		cl := testhelpers.WithRSPByEligibleNodeNameIndex(
			fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(node, rsp1, rsp2),
		).Build()

		mapFunc := mapNodeToRSP(cl)
		requests := mapFunc(context.Background(), node)

		// Both RSPs returned: rsp-1 (node in EligibleNodes) and rsp-2 (no selector, any node matches).
		Expect(requestNames(requests)).To(ConsistOf("rsp-1", "rsp-2"))
	})

	It("returns RSPs where node matches NodeLabelSelector and Zones", func() {
		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-1",
				Labels: map[string]string{
					"env":                    "prod",
					corev1.LabelTopologyZone: "zone-a",
				},
			},
		}
		rspMatches := &v1alpha1.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: "rsp-matches"},
			Spec: v1alpha1.ReplicatedStoragePoolSpec{
				Zones: []string{"zone-a"},
				NodeLabelSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"env": "prod"},
				},
			},
		}
		rspWrongZone := &v1alpha1.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: "rsp-wrong-zone"},
			Spec: v1alpha1.ReplicatedStoragePoolSpec{
				Zones: []string{"zone-b"},
			},
		}
		rspWrongSelector := &v1alpha1.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: "rsp-wrong-selector"},
			Spec: v1alpha1.ReplicatedStoragePoolSpec{
				NodeLabelSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"env": "dev"},
				},
			},
		}

		cl := testhelpers.WithRSPByEligibleNodeNameIndex(
			fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(node, rspMatches, rspWrongZone, rspWrongSelector),
		).Build()

		mapFunc := mapNodeToRSP(cl)
		requests := mapFunc(context.Background(), node)

		Expect(requestNames(requests)).To(ConsistOf("rsp-matches"))
	})

	It("deduplicates RSPs when node is in eligibleNodes AND matches selector", func() {
		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-1",
				Labels: map[string]string{
					"env":                    "prod",
					corev1.LabelTopologyZone: "zone-a",
				},
			},
		}
		rsp := &v1alpha1.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
			Spec: v1alpha1.ReplicatedStoragePoolSpec{
				Zones: []string{"zone-a"},
				NodeLabelSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"env": "prod"},
				},
			},
			Status: v1alpha1.ReplicatedStoragePoolStatus{
				EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
					{NodeName: "node-1"},
				},
			},
		}

		cl := testhelpers.WithRSPByEligibleNodeNameIndex(
			fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(node, rsp),
		).Build()

		mapFunc := mapNodeToRSP(cl)
		requests := mapFunc(context.Background(), node)

		// Should appear only once despite matching both index and selector
		Expect(requests).To(HaveLen(1))
		Expect(requests[0].Name).To(Equal("rsp-1"))
	})

	It("returns empty when node matches nothing", func() {
		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-orphan",
				Labels: map[string]string{
					corev1.LabelTopologyZone: "zone-x",
				},
			},
		}
		rsp := &v1alpha1.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
			Spec: v1alpha1.ReplicatedStoragePoolSpec{
				Zones: []string{"zone-a"},
			},
			Status: v1alpha1.ReplicatedStoragePoolStatus{
				EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
					{NodeName: "other-node"},
				},
			},
		}

		cl := testhelpers.WithRSPByEligibleNodeNameIndex(
			fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(node, rsp),
		).Build()

		mapFunc := mapNodeToRSP(cl)
		requests := mapFunc(context.Background(), node)

		Expect(requests).To(BeEmpty())
	})

	It("returns nil for non-Node object", func() {
		cl := fake.NewClientBuilder().WithScheme(scheme).Build()

		mapFunc := mapNodeToRSP(cl)
		requests := mapFunc(context.Background(), &v1alpha1.ReplicatedStoragePool{})

		Expect(requests).To(BeNil())
	})

	It("returns nil for nil object", func() {
		cl := fake.NewClientBuilder().WithScheme(scheme).Build()

		mapFunc := mapNodeToRSP(cl)
		requests := mapFunc(context.Background(), nil)

		Expect(requests).To(BeNil())
	})
})

var _ = Describe("nodeMatchesRSP", func() {
	It("returns true when RSP has no zones and no selector (matches all)", func() {
		rsp := &v1alpha1.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
			Spec:       v1alpha1.ReplicatedStoragePoolSpec{},
		}
		nodeLabels := labels.Set{"any": "label"}
		nodeZone := "any-zone"

		Expect(nodeMatchesRSP(rsp, nodeLabels, nodeZone)).To(BeTrue())
	})

	It("returns true when node is in RSP zones", func() {
		rsp := &v1alpha1.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
			Spec: v1alpha1.ReplicatedStoragePoolSpec{
				Zones: []string{"zone-a", "zone-b"},
			},
		}
		nodeLabels := labels.Set{}
		nodeZone := "zone-a"

		Expect(nodeMatchesRSP(rsp, nodeLabels, nodeZone)).To(BeTrue())
	})

	It("returns false when node is not in RSP zones", func() {
		rsp := &v1alpha1.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
			Spec: v1alpha1.ReplicatedStoragePoolSpec{
				Zones: []string{"zone-a", "zone-b"},
			},
		}
		nodeLabels := labels.Set{}
		nodeZone := "zone-c"

		Expect(nodeMatchesRSP(rsp, nodeLabels, nodeZone)).To(BeFalse())
	})

	It("returns true when node matches selector", func() {
		rsp := &v1alpha1.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
			Spec: v1alpha1.ReplicatedStoragePoolSpec{
				NodeLabelSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"env": "prod"},
				},
			},
		}
		nodeLabels := labels.Set{"env": "prod", "other": "value"}
		nodeZone := ""

		Expect(nodeMatchesRSP(rsp, nodeLabels, nodeZone)).To(BeTrue())
	})

	It("returns false when node does not match selector", func() {
		rsp := &v1alpha1.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
			Spec: v1alpha1.ReplicatedStoragePoolSpec{
				NodeLabelSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"env": "prod"},
				},
			},
		}
		nodeLabels := labels.Set{"env": "dev"}
		nodeZone := ""

		Expect(nodeMatchesRSP(rsp, nodeLabels, nodeZone)).To(BeFalse())
	})

	It("returns true for invalid selector (conservative)", func() {
		rsp := &v1alpha1.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
			Spec: v1alpha1.ReplicatedStoragePoolSpec{
				NodeLabelSelector: &metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{
							Key:      "key",
							Operator: "InvalidOperator", // Invalid operator
							Values:   []string{"value"},
						},
					},
				},
			},
		}
		nodeLabels := labels.Set{"any": "label"}
		nodeZone := ""

		// Should return true (be conservative) if selector cannot be parsed
		Expect(nodeMatchesRSP(rsp, nodeLabels, nodeZone)).To(BeTrue())
	})

	It("returns true when both zones and selector match", func() {
		rsp := &v1alpha1.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
			Spec: v1alpha1.ReplicatedStoragePoolSpec{
				Zones: []string{"zone-a"},
				NodeLabelSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"env": "prod"},
				},
			},
		}
		nodeLabels := labels.Set{"env": "prod"}
		nodeZone := "zone-a"

		Expect(nodeMatchesRSP(rsp, nodeLabels, nodeZone)).To(BeTrue())
	})

	It("returns false when zones match but selector does not", func() {
		rsp := &v1alpha1.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
			Spec: v1alpha1.ReplicatedStoragePoolSpec{
				Zones: []string{"zone-a"},
				NodeLabelSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"env": "prod"},
				},
			},
		}
		nodeLabels := labels.Set{"env": "dev"}
		nodeZone := "zone-a"

		Expect(nodeMatchesRSP(rsp, nodeLabels, nodeZone)).To(BeFalse())
	})
})

var _ = Describe("mapLVGToRSP", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(snc.AddToScheme(scheme)).To(Succeed())
	})

	It("returns RSPs that reference LVG via spec.lvmVolumeGroups", func() {
		lvg := &snc.LVMVolumeGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "lvg-1"},
		}
		rsp1 := &v1alpha1.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
			Spec: v1alpha1.ReplicatedStoragePoolSpec{
				Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
					{Name: "lvg-1"},
				},
			},
		}
		rsp2 := &v1alpha1.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: "rsp-2"},
			Spec: v1alpha1.ReplicatedStoragePoolSpec{
				Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
					{Name: "lvg-1"},
					{Name: "lvg-2"},
				},
			},
		}
		rspOther := &v1alpha1.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: "rsp-other"},
			Spec: v1alpha1.ReplicatedStoragePoolSpec{
				Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
					{Name: "lvg-other"},
				},
			},
		}

		cl := testhelpers.WithRSPByLVMVolumeGroupNameIndex(
			fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(lvg, rsp1, rsp2, rspOther),
		).Build()

		mapFunc := mapLVGToRSP(cl)
		requests := mapFunc(context.Background(), lvg)

		Expect(requestNames(requests)).To(ConsistOf("rsp-1", "rsp-2"))
	})

	It("returns empty when no RSPs reference LVG", func() {
		lvg := &snc.LVMVolumeGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "lvg-orphan"},
		}
		rsp := &v1alpha1.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
			Spec: v1alpha1.ReplicatedStoragePoolSpec{
				Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
					{Name: "lvg-other"},
				},
			},
		}

		cl := testhelpers.WithRSPByLVMVolumeGroupNameIndex(
			fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(lvg, rsp),
		).Build()

		mapFunc := mapLVGToRSP(cl)
		requests := mapFunc(context.Background(), lvg)

		Expect(requests).To(BeEmpty())
	})

	It("returns nil for non-LVG object", func() {
		cl := fake.NewClientBuilder().WithScheme(scheme).Build()

		mapFunc := mapLVGToRSP(cl)
		requests := mapFunc(context.Background(), &corev1.Node{})

		Expect(requests).To(BeNil())
	})

	It("returns nil for nil object", func() {
		cl := fake.NewClientBuilder().WithScheme(scheme).Build()

		mapFunc := mapLVGToRSP(cl)
		requests := mapFunc(context.Background(), nil)

		Expect(requests).To(BeNil())
	})
})

var _ = Describe("mapAgentPodToRSP", func() {
	var scheme *runtime.Scheme
	const testNamespace = "d8-sds-replicated-volume"

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
	})

	It("returns RSPs where pod's node is in EligibleNodes", func() {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "agent-pod-1",
				Namespace: testNamespace,
				Labels:    map[string]string{"app": "agent"},
			},
			Spec: corev1.PodSpec{
				NodeName: "node-1",
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
					{NodeName: "node-2"},
				},
			},
		}

		cl := testhelpers.WithRSPByEligibleNodeNameIndex(
			fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(pod, rsp1, rsp2),
		).Build()

		mapFunc := mapAgentPodToRSP(cl, testNamespace)
		requests := mapFunc(context.Background(), pod)

		Expect(requestNames(requests)).To(ConsistOf("rsp-1"))
	})

	It("returns nil for pod in wrong namespace", func() {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "agent-pod-1",
				Namespace: "other-namespace",
				Labels:    map[string]string{"app": "agent"},
			},
			Spec: corev1.PodSpec{
				NodeName: "node-1",
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

		cl := testhelpers.WithRSPByEligibleNodeNameIndex(
			fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(pod, rsp),
		).Build()

		mapFunc := mapAgentPodToRSP(cl, testNamespace)
		requests := mapFunc(context.Background(), pod)

		Expect(requests).To(BeNil())
	})

	It("returns nil for pod without app=agent label", func() {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "other-pod",
				Namespace: testNamespace,
				Labels:    map[string]string{"app": "other"},
			},
			Spec: corev1.PodSpec{
				NodeName: "node-1",
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

		cl := testhelpers.WithRSPByEligibleNodeNameIndex(
			fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(pod, rsp),
		).Build()

		mapFunc := mapAgentPodToRSP(cl, testNamespace)
		requests := mapFunc(context.Background(), pod)

		Expect(requests).To(BeNil())
	})

	It("returns nil for unscheduled pod (empty NodeName)", func() {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "agent-pod-unscheduled",
				Namespace: testNamespace,
				Labels:    map[string]string{"app": "agent"},
			},
			Spec: corev1.PodSpec{
				NodeName: "", // Not yet scheduled
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

		cl := testhelpers.WithRSPByEligibleNodeNameIndex(
			fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(pod, rsp),
		).Build()

		mapFunc := mapAgentPodToRSP(cl, testNamespace)
		requests := mapFunc(context.Background(), pod)

		Expect(requests).To(BeNil())
	})

	It("returns nil for non-Pod object", func() {
		cl := fake.NewClientBuilder().WithScheme(scheme).Build()

		mapFunc := mapAgentPodToRSP(cl, testNamespace)
		requests := mapFunc(context.Background(), &corev1.Node{})

		Expect(requests).To(BeNil())
	})

	It("returns nil for nil object", func() {
		cl := fake.NewClientBuilder().WithScheme(scheme).Build()

		mapFunc := mapAgentPodToRSP(cl, testNamespace)
		requests := mapFunc(context.Background(), nil)

		Expect(requests).To(BeNil())
	})

	It("returns empty when node is not in any RSP eligibleNodes", func() {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "agent-pod-1",
				Namespace: testNamespace,
				Labels:    map[string]string{"app": "agent"},
			},
			Spec: corev1.PodSpec{
				NodeName: "orphan-node",
			},
		}
		rsp := &v1alpha1.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
			Status: v1alpha1.ReplicatedStoragePoolStatus{
				EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
					{NodeName: "other-node"},
				},
			},
		}

		cl := testhelpers.WithRSPByEligibleNodeNameIndex(
			fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(pod, rsp),
		).Build()

		mapFunc := mapAgentPodToRSP(cl, testNamespace)
		requests := mapFunc(context.Background(), pod)

		Expect(requests).To(BeEmpty())
	})
})
