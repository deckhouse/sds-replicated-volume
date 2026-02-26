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

package rvrcontroller

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

var _ = Describe("rvrPredicates", func() {
	Describe("UpdateFunc", func() {
		var preds []func(event.TypedUpdateEvent[client.Object]) bool

		BeforeEach(func() {
			predicates := rvrPredicates()
			preds = make([]func(event.TypedUpdateEvent[client.Object]) bool, 0)
			for _, p := range predicates {
				if fp, ok := p.(predicate.TypedFuncs[client.Object]); ok && fp.UpdateFunc != nil {
					preds = append(preds, fp.UpdateFunc)
				}
			}
		})

		It("returns true when Generation changes", func() {
			oldRVR := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-1", Generation: 1},
			}
			newRVR := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-1", Generation: 2},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRVR,
				ObjectNew: newRVR,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})

		It("returns true when Finalizers change", func() {
			oldRVR := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-1", Generation: 1},
			}
			newRVR := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rvr-1",
					Generation: 1,
					Finalizers: []string{"test-finalizer"},
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRVR,
				ObjectNew: newRVR,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})

		It("returns false when no relevant changes", func() {
			oldRVR := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-1", Generation: 1},
			}
			newRVR := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-1", Generation: 1},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRVR,
				ObjectNew: newRVR,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeFalse())
			}
		})

		It("returns false when only Labels change", func() {
			oldRVR := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-1", Generation: 1},
			}
			newRVR := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rvr-1",
					Generation: 1,
					Labels:     map[string]string{"new": "label"},
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRVR,
				ObjectNew: newRVR,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeFalse())
			}
		})

		It("returns false when only Annotations change", func() {
			oldRVR := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-1", Generation: 1},
			}
			newRVR := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "rvr-1",
					Generation:  1,
					Annotations: map[string]string{"new": "annotation"},
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRVR,
				ObjectNew: newRVR,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeFalse())
			}
		})
	})
})

var _ = Describe("rvPredicates", func() {
	Describe("UpdateFunc", func() {
		var preds []func(event.TypedUpdateEvent[client.Object]) bool

		BeforeEach(func() {
			predicates := rvPredicates()
			preds = make([]func(event.TypedUpdateEvent[client.Object]) bool, 0)
			for _, p := range predicates {
				if fp, ok := p.(predicate.TypedFuncs[client.Object]); ok && fp.UpdateFunc != nil {
					preds = append(preds, fp.UpdateFunc)
				}
			}
		})

		It("returns true when DatameshRevision changes", func() {
			oldRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Status: v1alpha1.ReplicatedVolumeStatus{
					DatameshRevision: 1,
				},
			}
			newRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Status: v1alpha1.ReplicatedVolumeStatus{
					DatameshRevision: 2,
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRV,
				ObjectNew: newRV,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})

		It("returns false when DatameshRevision unchanged", func() {
			oldRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Status: v1alpha1.ReplicatedVolumeStatus{
					DatameshRevision: 1,
				},
			}
			newRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Status: v1alpha1.ReplicatedVolumeStatus{
					DatameshRevision: 1,
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRV,
				ObjectNew: newRV,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeFalse())
			}
		})

		It("returns false when other status fields change but DatameshRevision is the same", func() {
			oldRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Status: v1alpha1.ReplicatedVolumeStatus{
					DatameshRevision:                1,
					ConfigurationObservedGeneration: 1,
				},
			}
			newRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Status: v1alpha1.ReplicatedVolumeStatus{
					DatameshRevision:                1,
					ConfigurationObservedGeneration: 2, // Changed, but we only care about DatameshRevision
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRV,
				ObjectNew: newRV,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeFalse())
			}
		})

		It("returns true when ReplicatedStorageClassName changes", func() {
			oldRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					ReplicatedStorageClassName: "old-class",
				},
			}
			newRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					ReplicatedStorageClassName: "new-class",
				},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRV,
				ObjectNew: newRV,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
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
			newRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: nil,
				ObjectNew: newRV,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})

		It("returns true when new is nil (conservative)", func() {
			oldRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
			}
			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldRV,
				ObjectNew: nil,
			}

			for _, pred := range preds {
				Expect(pred(e)).To(BeTrue())
			}
		})
	})

	Describe("CreateFunc", func() {
		It("returns false always", func() {
			predicates := rvPredicates()
			for _, p := range predicates {
				if fp, ok := p.(predicate.TypedFuncs[client.Object]); ok && fp.CreateFunc != nil {
					rv := &v1alpha1.ReplicatedVolume{
						ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
					}
					e := event.TypedCreateEvent[client.Object]{Object: rv}
					Expect(fp.CreateFunc(e)).To(BeFalse())
				}
			}
		})
	})

	Describe("DeleteFunc", func() {
		It("returns false always", func() {
			predicates := rvPredicates()
			for _, p := range predicates {
				if fp, ok := p.(predicate.TypedFuncs[client.Object]); ok && fp.DeleteFunc != nil {
					rv := &v1alpha1.ReplicatedVolume{
						ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
					}
					e := event.TypedDeleteEvent[client.Object]{Object: rv}
					Expect(fp.DeleteFunc(e)).To(BeFalse())
				}
			}
		})
	})

	Describe("GenericFunc", func() {
		It("returns false always", func() {
			predicates := rvPredicates()
			for _, p := range predicates {
				if fp, ok := p.(predicate.TypedFuncs[client.Object]); ok && fp.GenericFunc != nil {
					rv := &v1alpha1.ReplicatedVolume{
						ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
					}
					e := event.TypedGenericEvent[client.Object]{Object: rv}
					Expect(fp.GenericFunc(e)).To(BeFalse())
				}
			}
		})
	})
})

var _ = Describe("agentPodPredicates", func() {
	var predicates []predicate.Predicate
	const testNamespace = "test-namespace"

	BeforeEach(func() {
		predicates = agentPodPredicates(testNamespace)
	})

	Context("CreateFunc", func() {
		It("returns true for agent pod in namespace", func() {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "agent-abc",
					Namespace: testNamespace,
					Labels:    map[string]string{"app": "agent"},
				},
			}

			e := event.TypedCreateEvent[client.Object]{Object: pod}
			result := predicates[0].Create(e)
			Expect(result).To(BeTrue())
		})

		It("returns false for non-agent pod", func() {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "other-pod",
					Namespace: testNamespace,
					Labels:    map[string]string{"app": "other"},
				},
			}

			e := event.TypedCreateEvent[client.Object]{Object: pod}
			result := predicates[0].Create(e)
			Expect(result).To(BeFalse())
		})

		It("returns false for pod in wrong namespace", func() {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "agent-abc",
					Namespace: "other-namespace",
					Labels:    map[string]string{"app": "agent"},
				},
			}

			e := event.TypedCreateEvent[client.Object]{Object: pod}
			result := predicates[0].Create(e)
			Expect(result).To(BeFalse())
		})

		It("returns true on type assertion failure (conservative)", func() {
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
			}

			e := event.TypedCreateEvent[client.Object]{Object: node}
			result := predicates[0].Create(e)
			Expect(result).To(BeTrue())
		})
	})

	Context("UpdateFunc", func() {
		It("returns true when Ready condition changes (false->true)", func() {
			oldPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "agent-abc",
					Namespace: testNamespace,
					Labels:    map[string]string{"app": "agent"},
				},
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{
						{Type: corev1.PodReady, Status: corev1.ConditionFalse},
					},
				},
			}
			newPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "agent-abc",
					Namespace: testNamespace,
					Labels:    map[string]string{"app": "agent"},
				},
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{
						{Type: corev1.PodReady, Status: corev1.ConditionTrue},
					},
				},
			}

			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldPod,
				ObjectNew: newPod,
			}
			result := predicates[0].Update(e)
			Expect(result).To(BeTrue())
		})

		It("returns true when Ready condition changes (true->false)", func() {
			oldPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "agent-abc",
					Namespace: testNamespace,
					Labels:    map[string]string{"app": "agent"},
				},
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{
						{Type: corev1.PodReady, Status: corev1.ConditionTrue},
					},
				},
			}
			newPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "agent-abc",
					Namespace: testNamespace,
					Labels:    map[string]string{"app": "agent"},
				},
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{
						{Type: corev1.PodReady, Status: corev1.ConditionFalse},
					},
				},
			}

			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldPod,
				ObjectNew: newPod,
			}
			result := predicates[0].Update(e)
			Expect(result).To(BeTrue())
		})

		It("returns false when Ready unchanged", func() {
			oldPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "agent-abc",
					Namespace: testNamespace,
					Labels:    map[string]string{"app": "agent"},
				},
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{
						{Type: corev1.PodReady, Status: corev1.ConditionTrue},
					},
				},
			}
			newPod := oldPod.DeepCopy()
			newPod.ResourceVersion = "2"

			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldPod,
				ObjectNew: newPod,
			}
			result := predicates[0].Update(e)
			Expect(result).To(BeFalse())
		})

		It("returns false for non-agent pod", func() {
			oldPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "other-pod",
					Namespace: testNamespace,
					Labels:    map[string]string{"app": "other"},
				},
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{
						{Type: corev1.PodReady, Status: corev1.ConditionFalse},
					},
				},
			}
			newPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "other-pod",
					Namespace: testNamespace,
					Labels:    map[string]string{"app": "other"},
				},
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{
						{Type: corev1.PodReady, Status: corev1.ConditionTrue},
					},
				},
			}

			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldPod,
				ObjectNew: newPod,
			}
			result := predicates[0].Update(e)
			Expect(result).To(BeFalse())
		})

		It("returns false for pod in wrong namespace", func() {
			oldPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "agent-abc",
					Namespace: "other-namespace",
					Labels:    map[string]string{"app": "agent"},
				},
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{
						{Type: corev1.PodReady, Status: corev1.ConditionFalse},
					},
				},
			}
			newPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "agent-abc",
					Namespace: "other-namespace",
					Labels:    map[string]string{"app": "agent"},
				},
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{
						{Type: corev1.PodReady, Status: corev1.ConditionTrue},
					},
				},
			}

			e := event.TypedUpdateEvent[client.Object]{
				ObjectOld: oldPod,
				ObjectNew: newPod,
			}
			result := predicates[0].Update(e)
			Expect(result).To(BeFalse())
		})

		It("returns true on type assertion failure (conservative)", func() {
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
			result := predicates[0].Update(e)
			Expect(result).To(BeTrue())
		})
	})

	Context("DeleteFunc", func() {
		It("returns true for agent pod in namespace", func() {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "agent-abc",
					Namespace: testNamespace,
					Labels:    map[string]string{"app": "agent"},
				},
			}

			e := event.TypedDeleteEvent[client.Object]{Object: pod}
			result := predicates[0].Delete(e)
			Expect(result).To(BeTrue())
		})

		It("returns false for non-agent pod", func() {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "other-pod",
					Namespace: testNamespace,
					Labels:    map[string]string{"app": "other"},
				},
			}

			e := event.TypedDeleteEvent[client.Object]{Object: pod}
			result := predicates[0].Delete(e)
			Expect(result).To(BeFalse())
		})

		It("returns false for pod in wrong namespace", func() {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "agent-abc",
					Namespace: "other-namespace",
					Labels:    map[string]string{"app": "agent"},
				},
			}

			e := event.TypedDeleteEvent[client.Object]{Object: pod}
			result := predicates[0].Delete(e)
			Expect(result).To(BeFalse())
		})

		It("returns true on type assertion failure (conservative)", func() {
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
			}

			e := event.TypedDeleteEvent[client.Object]{Object: node}
			result := predicates[0].Delete(e)
			Expect(result).To(BeTrue())
		})
	})
})

var _ = Describe("rspPredicates", func() {
	Describe("CreateFunc", func() {
		It("returns true always", func() {
			predicates := rspPredicates()
			rsp := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
			}
			for _, p := range predicates {
				if fp, ok := p.(predicate.TypedFuncs[client.Object]); ok && fp.CreateFunc != nil {
					e := event.TypedCreateEvent[client.Object]{Object: rsp}
					Expect(fp.CreateFunc(e)).To(BeTrue())
				}
			}
		})
	})

	Describe("DeleteFunc", func() {
		It("returns true always", func() {
			predicates := rspPredicates()
			rsp := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
			}
			for _, p := range predicates {
				if fp, ok := p.(predicate.TypedFuncs[client.Object]); ok && fp.DeleteFunc != nil {
					e := event.TypedDeleteEvent[client.Object]{Object: rsp}
					Expect(fp.DeleteFunc(e)).To(BeTrue())
				}
			}
		})
	})

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

		It("returns true when EligibleNodesRevision changes", func() {
			oldRSP := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					EligibleNodesRevision: 1,
				},
			}
			newRSP := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					EligibleNodesRevision: 2,
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

		It("returns false when EligibleNodesRevision unchanged", func() {
			oldRSP := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					EligibleNodesRevision: 1,
				},
			}
			newRSP := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					EligibleNodesRevision: 1,
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

		It("returns false when other status fields change but EligibleNodesRevision unchanged", func() {
			oldRSP := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					EligibleNodesRevision: 1,
					Phase:                 "Ready",
				},
			}
			newRSP := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					EligibleNodesRevision: 1,
					Phase:                 "NotReady",
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

		It("returns true on type assertion failure (conservative)", func() {
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

var _ = Describe("Helper functions", func() {
	Describe("isPodReady", func() {
		It("returns true when PodReady=True", func() {
			pod := &corev1.Pod{
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{
						{Type: corev1.PodReady, Status: corev1.ConditionTrue},
					},
				},
			}
			Expect(isPodReady(pod)).To(BeTrue())
		})

		It("returns false when PodReady=False", func() {
			pod := &corev1.Pod{
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{
						{Type: corev1.PodReady, Status: corev1.ConditionFalse},
					},
				},
			}
			Expect(isPodReady(pod)).To(BeFalse())
		})

		It("returns false when no PodReady condition", func() {
			pod := &corev1.Pod{}
			Expect(isPodReady(pod)).To(BeFalse())
		})
	})
})
