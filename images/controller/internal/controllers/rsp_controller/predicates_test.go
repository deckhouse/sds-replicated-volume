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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

var _ = Describe("NodePredicates", func() {
	var predicates []predicate.Predicate

	BeforeEach(func() {
		predicates = NodePredicates()
	})

	It("returns true for label change", func() {
		oldNode := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "node-1",
				Labels: map[string]string{"zone": "a"},
			},
		}
		newNode := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "node-1",
				Labels: map[string]string{"zone": "b"},
			},
		}

		e := event.TypedUpdateEvent[client.Object]{
			ObjectOld: oldNode,
			ObjectNew: newNode,
		}

		result := predicates[0].Update(e)
		Expect(result).To(BeTrue())
	})

	It("returns true for Ready condition status change", func() {
		oldNode := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
			Status: corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{
					{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
				},
			},
		}
		newNode := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
			Status: corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{
					{Type: corev1.NodeReady, Status: corev1.ConditionFalse},
				},
			},
		}

		e := event.TypedUpdateEvent[client.Object]{
			ObjectOld: oldNode,
			ObjectNew: newNode,
		}

		result := predicates[0].Update(e)
		Expect(result).To(BeTrue())
	})

	It("returns true for spec.unschedulable change", func() {
		oldNode := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
			Spec:       corev1.NodeSpec{Unschedulable: false},
		}
		newNode := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
			Spec:       corev1.NodeSpec{Unschedulable: true},
		}

		e := event.TypedUpdateEvent[client.Object]{
			ObjectOld: oldNode,
			ObjectNew: newNode,
		}

		result := predicates[0].Update(e)
		Expect(result).To(BeTrue())
	})

	It("returns false when none of the relevant fields changed", func() {
		oldNode := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
			Status: corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{
					{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
				},
			},
		}
		newNode := oldNode.DeepCopy()
		newNode.ResourceVersion = "2" // Only resource version changed.

		e := event.TypedUpdateEvent[client.Object]{
			ObjectOld: oldNode,
			ObjectNew: newNode,
		}

		result := predicates[0].Update(e)
		Expect(result).To(BeFalse())
	})
})

var _ = Describe("LVGPredicates", func() {
	var predicates []predicate.Predicate

	BeforeEach(func() {
		predicates = LVGPredicates()
	})

	It("returns true for generation change", func() {
		oldLVG := &snc.LVMVolumeGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "lvg-1", Generation: 1},
		}
		newLVG := &snc.LVMVolumeGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "lvg-1", Generation: 2},
		}

		e := event.TypedUpdateEvent[client.Object]{
			ObjectOld: oldLVG,
			ObjectNew: newLVG,
		}

		result := predicates[0].Update(e)
		Expect(result).To(BeTrue())
	})

	It("returns true for unschedulable annotation change", func() {
		oldLVG := &snc.LVMVolumeGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "lvg-1", Generation: 1},
		}
		newLVG := &snc.LVMVolumeGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "lvg-1",
				Generation: 1,
				Annotations: map[string]string{
					v1alpha1.LVMVolumeGroupUnschedulableAnnotationKey: "",
				},
			},
		}

		e := event.TypedUpdateEvent[client.Object]{
			ObjectOld: oldLVG,
			ObjectNew: newLVG,
		}

		result := predicates[0].Update(e)
		Expect(result).To(BeTrue())
	})

	It("returns true for Ready condition status change", func() {
		oldLVG := &snc.LVMVolumeGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "lvg-1", Generation: 1},
			Status: snc.LVMVolumeGroupStatus{
				Conditions: []metav1.Condition{
					{Type: "Ready", Status: metav1.ConditionFalse},
				},
			},
		}
		newLVG := &snc.LVMVolumeGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "lvg-1", Generation: 1},
			Status: snc.LVMVolumeGroupStatus{
				Conditions: []metav1.Condition{
					{Type: "Ready", Status: metav1.ConditionTrue},
				},
			},
		}

		e := event.TypedUpdateEvent[client.Object]{
			ObjectOld: oldLVG,
			ObjectNew: newLVG,
		}

		result := predicates[0].Update(e)
		Expect(result).To(BeTrue())
	})

	It("returns true for ThinPools[].Ready change", func() {
		oldLVG := &snc.LVMVolumeGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "lvg-1", Generation: 1},
			Status: snc.LVMVolumeGroupStatus{
				ThinPools: []snc.LVMVolumeGroupThinPoolStatus{
					{Name: "tp-1", Ready: false},
				},
			},
		}
		newLVG := &snc.LVMVolumeGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "lvg-1", Generation: 1},
			Status: snc.LVMVolumeGroupStatus{
				ThinPools: []snc.LVMVolumeGroupThinPoolStatus{
					{Name: "tp-1", Ready: true},
				},
			},
		}

		e := event.TypedUpdateEvent[client.Object]{
			ObjectOld: oldLVG,
			ObjectNew: newLVG,
		}

		result := predicates[0].Update(e)
		Expect(result).To(BeTrue())
	})

	It("returns false when none of above changed", func() {
		oldLVG := &snc.LVMVolumeGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "lvg-1", Generation: 1},
		}
		newLVG := oldLVG.DeepCopy()
		newLVG.ResourceVersion = "2"

		e := event.TypedUpdateEvent[client.Object]{
			ObjectOld: oldLVG,
			ObjectNew: newLVG,
		}

		result := predicates[0].Update(e)
		Expect(result).To(BeFalse())
	})
})

var _ = Describe("AgentPodPredicates", func() {
	var predicates []predicate.Predicate
	const testNamespace = "test-namespace"

	BeforeEach(func() {
		predicates = AgentPodPredicates(testNamespace)
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
	})

	Context("UpdateFunc", func() {
		It("returns true when Ready condition changes", func() {
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
	})

	Context("DeleteFunc", func() {
		It("returns true for agent pod", func() {
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

	Describe("lvgReadyConditionStatus", func() {
		It("returns status when Ready condition exists", func() {
			lvg := &snc.LVMVolumeGroup{
				Status: snc.LVMVolumeGroupStatus{
					Conditions: []metav1.Condition{
						{Type: "Ready", Status: metav1.ConditionTrue},
					},
				},
			}
			Expect(lvgReadyConditionStatus(lvg)).To(Equal(metav1.ConditionTrue))
		})

		It("returns Unknown when no Ready condition", func() {
			lvg := &snc.LVMVolumeGroup{}
			Expect(lvgReadyConditionStatus(lvg)).To(Equal(metav1.ConditionUnknown))
		})
	})

	Describe("areThinPoolsReadyEqual", func() {
		It("returns true for equal Ready states", func() {
			oldPools := []snc.LVMVolumeGroupThinPoolStatus{
				{Name: "tp-1", Ready: true},
			}
			newPools := []snc.LVMVolumeGroupThinPoolStatus{
				{Name: "tp-1", Ready: true},
			}
			Expect(areThinPoolsReadyEqual(oldPools, newPools)).To(BeTrue())
		})

		It("returns false for different Ready states", func() {
			oldPools := []snc.LVMVolumeGroupThinPoolStatus{
				{Name: "tp-1", Ready: false},
			}
			newPools := []snc.LVMVolumeGroupThinPoolStatus{
				{Name: "tp-1", Ready: true},
			}
			Expect(areThinPoolsReadyEqual(oldPools, newPools)).To(BeFalse())
		})

		It("returns false for different length", func() {
			oldPools := []snc.LVMVolumeGroupThinPoolStatus{
				{Name: "tp-1", Ready: true},
			}
			newPools := []snc.LVMVolumeGroupThinPoolStatus{
				{Name: "tp-1", Ready: true},
				{Name: "tp-2", Ready: true},
			}
			Expect(areThinPoolsReadyEqual(oldPools, newPools)).To(BeFalse())
		})
	})
})
