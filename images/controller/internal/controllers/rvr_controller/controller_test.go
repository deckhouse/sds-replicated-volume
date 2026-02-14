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
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes/testhelpers"
)

func TestRVRController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "rvr_controller Suite")
}

var _ = Describe("rvEventHandler", func() {
	var scheme *runtime.Scheme
	var queue *fakeQueue

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		queue = &fakeQueue{}
	})

	Describe("Update with DatameshRevision change", func() {
		It("enqueues all RVRs when old revision is 0 (initial setup)", func() {
			oldRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Status: v1alpha1.ReplicatedVolumeStatus{
					DatameshRevision: 0,
				},
			}
			newRV := oldRV.DeepCopy()
			newRV.Status.DatameshRevision = 1
			newRV.Status.Datamesh.Members = []v1alpha1.ReplicatedVolumeDatameshMember{
				{Name: "rv-1-0"},
				{Name: "rv-1-1"},
			}

			rvr0 := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1-0"},
				Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{ReplicatedVolumeName: "rv-1"},
			}
			rvr1 := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1-1"},
				Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{ReplicatedVolumeName: "rv-1"},
			}
			rvrOther := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-other-0"},
				Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{ReplicatedVolumeName: "rv-other"},
			}

			cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(
				fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(newRV, rvr0, rvr1, rvrOther),
			).Build()

			handler := newRVEventHandler(cl).(*rvEventHandler)
			handler.Update(context.Background(), event.UpdateEvent{ObjectOld: oldRV, ObjectNew: newRV}, queue)

			Expect(queue.items).To(HaveLen(2))
			names := []string{queue.items[0].Name, queue.items[1].Name}
			Expect(names).To(ContainElements("rv-1-0", "rv-1-1"))
		})

		It("enqueues only members from old/new datamesh when revision changes non-zero->non-zero", func() {
			oldRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Status: v1alpha1.ReplicatedVolumeStatus{
					DatameshRevision: 1,
					Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
						Members: []v1alpha1.ReplicatedVolumeDatameshMember{
							{Name: "rv-1-0"},
							{Name: "rv-1-1"},
						},
					},
				},
			}
			newRV := oldRV.DeepCopy()
			newRV.Status.DatameshRevision = 2
			newRV.Status.Datamesh.Members = []v1alpha1.ReplicatedVolumeDatameshMember{
				{Name: "rv-1-1"},
				{Name: "rv-1-2"}, // Added.
			}

			cl := fake.NewClientBuilder().WithScheme(scheme).Build()

			handler := newRVEventHandler(cl).(*rvEventHandler)
			handler.Update(context.Background(), event.UpdateEvent{ObjectOld: oldRV, ObjectNew: newRV}, queue)

			// Should enqueue node IDs 0, 1, 2 (union of old and new members).
			Expect(queue.items).To(HaveLen(3))
			names := []string{queue.items[0].Name, queue.items[1].Name, queue.items[2].Name}
			Expect(names).To(ContainElements("rv-1-0", "rv-1-1", "rv-1-2"))
		})
	})

	Describe("Update with ReplicatedStorageClassName change", func() {
		It("enqueues all RVRs when ReplicatedStorageClassName changes", func() {
			oldRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					ReplicatedStorageClassName: "rsc-old",
				},
				Status: v1alpha1.ReplicatedVolumeStatus{
					DatameshRevision: 1,
				},
			}
			newRV := oldRV.DeepCopy()
			newRV.Spec.ReplicatedStorageClassName = "rsc-new"

			rvr0 := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1-0"},
				Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{ReplicatedVolumeName: "rv-1"},
			}

			cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(
				fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(newRV, rvr0),
			).Build()

			handler := newRVEventHandler(cl).(*rvEventHandler)
			handler.Update(context.Background(), event.UpdateEvent{ObjectOld: oldRV, ObjectNew: newRV}, queue)

			Expect(queue.items).To(HaveLen(1))
			Expect(queue.items[0].Name).To(Equal("rv-1-0"))
		})
	})

	Describe("Update with DatameshPendingReplicaTransitions message change", func() {
		It("enqueues only affected RVRs when message changes", func() {
			oldRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Status: v1alpha1.ReplicatedVolumeStatus{
					DatameshRevision: 1,
					DatameshPendingReplicaTransitions: []v1alpha1.ReplicatedVolumeDatameshPendingReplicaTransition{
						{Name: "rv-1-0", Message: "old message"},
						{Name: "rv-1-1", Message: "unchanged"},
					},
				},
			}
			newRV := oldRV.DeepCopy()
			newRV.Status.DatameshPendingReplicaTransitions[0].Message = "new message"

			cl := fake.NewClientBuilder().WithScheme(scheme).Build()

			handler := newRVEventHandler(cl).(*rvEventHandler)
			handler.Update(context.Background(), event.UpdateEvent{ObjectOld: oldRV, ObjectNew: newRV}, queue)

			// Only rv-1-0 should be enqueued (message changed).
			Expect(queue.items).To(HaveLen(1))
			Expect(queue.items[0].Name).To(Equal("rv-1-0"))
		})

		It("enqueues RVRs for added transitions", func() {
			oldRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Status: v1alpha1.ReplicatedVolumeStatus{
					DatameshRevision: 1,
					DatameshPendingReplicaTransitions: []v1alpha1.ReplicatedVolumeDatameshPendingReplicaTransition{
						{Name: "rv-1-0", Message: "msg"},
					},
				},
			}
			newRV := oldRV.DeepCopy()
			newRV.Status.DatameshPendingReplicaTransitions = append(
				newRV.Status.DatameshPendingReplicaTransitions,
				v1alpha1.ReplicatedVolumeDatameshPendingReplicaTransition{Name: "rv-1-1", Message: "new"},
			)

			cl := fake.NewClientBuilder().WithScheme(scheme).Build()

			handler := newRVEventHandler(cl).(*rvEventHandler)
			handler.Update(context.Background(), event.UpdateEvent{ObjectOld: oldRV, ObjectNew: newRV}, queue)

			// Only rv-1-1 should be enqueued (added).
			Expect(queue.items).To(HaveLen(1))
			Expect(queue.items[0].Name).To(Equal("rv-1-1"))
		})

		It("enqueues nothing when no changes", func() {
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Status: v1alpha1.ReplicatedVolumeStatus{
					DatameshRevision: 1,
					DatameshPendingReplicaTransitions: []v1alpha1.ReplicatedVolumeDatameshPendingReplicaTransition{
						{Name: "rv-1-0", Message: "msg"},
					},
				},
			}

			cl := fake.NewClientBuilder().WithScheme(scheme).Build()

			handler := newRVEventHandler(cl).(*rvEventHandler)
			handler.Update(context.Background(), event.UpdateEvent{ObjectOld: rv, ObjectNew: rv.DeepCopy()}, queue)

			Expect(queue.items).To(BeEmpty())
		})
	})

	Describe("Update with combined changes", func() {
		It("enqueues union of datamesh members and message-changed replicas", func() {
			oldRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Status: v1alpha1.ReplicatedVolumeStatus{
					DatameshRevision: 1,
					Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
						Members: []v1alpha1.ReplicatedVolumeDatameshMember{
							{Name: "rv-1-0"},
							{Name: "rv-1-1"},
						},
					},
					DatameshPendingReplicaTransitions: []v1alpha1.ReplicatedVolumeDatameshPendingReplicaTransition{
						{Name: "rv-1-3", Message: "old message"},
					},
				},
			}
			newRV := oldRV.DeepCopy()
			newRV.Status.DatameshRevision = 2
			newRV.Status.Datamesh.Members = []v1alpha1.ReplicatedVolumeDatameshMember{
				{Name: "rv-1-1"},
				{Name: "rv-1-2"}, // Added to datamesh.
			}
			// Message also changed for pending replica (not in datamesh).
			newRV.Status.DatameshPendingReplicaTransitions[0].Message = "new message"

			cl := fake.NewClientBuilder().WithScheme(scheme).Build()

			handler := newRVEventHandler(cl).(*rvEventHandler)
			handler.Update(context.Background(), event.UpdateEvent{ObjectOld: oldRV, ObjectNew: newRV}, queue)

			// Should enqueue:
			// - node IDs 0, 1, 2 from datamesh (union of old/new members)
			// - node ID 3 from message change
			Expect(queue.items).To(HaveLen(4))
			names := make([]string, len(queue.items))
			for i, item := range queue.items {
				names[i] = item.Name
			}
			Expect(names).To(ContainElements("rv-1-0", "rv-1-1", "rv-1-2", "rv-1-3"))
		})
	})

	Describe("Create/Delete/Generic", func() {
		It("enqueues all RVRs on Create", func() {
			rv := &v1alpha1.ReplicatedVolume{ObjectMeta: metav1.ObjectMeta{Name: "rv-1"}}
			rvr0 := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1-0"},
				Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{ReplicatedVolumeName: "rv-1"},
			}
			rvr1 := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1-1"},
				Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{ReplicatedVolumeName: "rv-1"},
			}

			cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(
				fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(rv, rvr0, rvr1),
			).Build()

			handler := newRVEventHandler(cl).(*rvEventHandler)
			handler.Create(context.Background(), event.CreateEvent{Object: rv}, queue)

			Expect(queue.items).To(HaveLen(2))
			names := []string{queue.items[0].Name, queue.items[1].Name}
			Expect(names).To(ContainElements("rv-1-0", "rv-1-1"))
		})

		It("enqueues all RVRs on Delete", func() {
			rv := &v1alpha1.ReplicatedVolume{ObjectMeta: metav1.ObjectMeta{Name: "rv-1"}}
			rvr0 := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1-0"},
				Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{ReplicatedVolumeName: "rv-1"},
			}

			cl := testhelpers.WithRVRByReplicatedVolumeNameIndex(
				fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(rv, rvr0),
			).Build()

			handler := newRVEventHandler(cl).(*rvEventHandler)
			handler.Delete(context.Background(), event.DeleteEvent{Object: rv}, queue)

			Expect(queue.items).To(HaveLen(1))
			Expect(queue.items[0].Name).To(Equal("rv-1-0"))
		})

		It("does nothing on Generic", func() {
			rv := &v1alpha1.ReplicatedVolume{ObjectMeta: metav1.ObjectMeta{Name: "rv-1"}}
			cl := fake.NewClientBuilder().WithScheme(scheme).Build()

			handler := newRVEventHandler(cl).(*rvEventHandler)
			handler.Generic(context.Background(), event.GenericEvent{Object: rv}, queue)

			Expect(queue.items).To(BeEmpty())
		})
	})
})

var _ = Describe("eligibleNodeLVGEqual", func() {
	It("returns true for identical LVGs", func() {
		a := v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
			Name:          "lvg-1",
			ThinPoolName:  "thin-1",
			Unschedulable: false,
			Ready:         true,
		}
		b := v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
			Name:          "lvg-1",
			ThinPoolName:  "thin-1",
			Unschedulable: false,
			Ready:         true,
		}
		Expect(eligibleNodeLVGEqual(a, b)).To(BeTrue())
	})

	It("returns false when Name differs", func() {
		a := v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{Name: "lvg-1"}
		b := v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{Name: "lvg-2"}
		Expect(eligibleNodeLVGEqual(a, b)).To(BeFalse())
	})

	It("returns false when ThinPoolName differs", func() {
		a := v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{Name: "lvg-1", ThinPoolName: "thin-1"}
		b := v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{Name: "lvg-1", ThinPoolName: "thin-2"}
		Expect(eligibleNodeLVGEqual(a, b)).To(BeFalse())
	})

	It("returns false when Unschedulable differs", func() {
		a := v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{Name: "lvg-1", Unschedulable: false}
		b := v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{Name: "lvg-1", Unschedulable: true}
		Expect(eligibleNodeLVGEqual(a, b)).To(BeFalse())
	})

	It("returns false when Ready differs", func() {
		a := v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{Name: "lvg-1", Ready: true}
		b := v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{Name: "lvg-1", Ready: false}
		Expect(eligibleNodeLVGEqual(a, b)).To(BeFalse())
	})
})

var _ = Describe("eligibleNodeEqual", func() {
	It("returns true for identical nodes", func() {
		a := v1alpha1.ReplicatedStoragePoolEligibleNode{
			NodeName:      "node-1",
			Unschedulable: false,
			NodeReady:     true,
			AgentReady:    true,
			LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
				{Name: "lvg-1", Ready: true},
			},
		}
		b := v1alpha1.ReplicatedStoragePoolEligibleNode{
			NodeName:      "node-1",
			Unschedulable: false,
			NodeReady:     true,
			AgentReady:    true,
			LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
				{Name: "lvg-1", Ready: true},
			},
		}
		Expect(eligibleNodeEqual(a, b)).To(BeTrue())
	})

	It("returns true for nodes with empty LVMVolumeGroups", func() {
		a := v1alpha1.ReplicatedStoragePoolEligibleNode{NodeName: "node-1"}
		b := v1alpha1.ReplicatedStoragePoolEligibleNode{NodeName: "node-1"}
		Expect(eligibleNodeEqual(a, b)).To(BeTrue())
	})

	It("returns false when NodeName differs", func() {
		a := v1alpha1.ReplicatedStoragePoolEligibleNode{NodeName: "node-1"}
		b := v1alpha1.ReplicatedStoragePoolEligibleNode{NodeName: "node-2"}
		Expect(eligibleNodeEqual(a, b)).To(BeFalse())
	})

	It("returns false when Unschedulable differs", func() {
		a := v1alpha1.ReplicatedStoragePoolEligibleNode{NodeName: "node-1", Unschedulable: false}
		b := v1alpha1.ReplicatedStoragePoolEligibleNode{NodeName: "node-1", Unschedulable: true}
		Expect(eligibleNodeEqual(a, b)).To(BeFalse())
	})

	It("returns false when NodeReady differs", func() {
		a := v1alpha1.ReplicatedStoragePoolEligibleNode{NodeName: "node-1", NodeReady: true}
		b := v1alpha1.ReplicatedStoragePoolEligibleNode{NodeName: "node-1", NodeReady: false}
		Expect(eligibleNodeEqual(a, b)).To(BeFalse())
	})

	It("returns false when AgentReady differs", func() {
		a := v1alpha1.ReplicatedStoragePoolEligibleNode{NodeName: "node-1", AgentReady: true}
		b := v1alpha1.ReplicatedStoragePoolEligibleNode{NodeName: "node-1", AgentReady: false}
		Expect(eligibleNodeEqual(a, b)).To(BeFalse())
	})

	It("returns false when LVMVolumeGroups count differs", func() {
		a := v1alpha1.ReplicatedStoragePoolEligibleNode{
			NodeName:        "node-1",
			LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{{Name: "lvg-1"}},
		}
		b := v1alpha1.ReplicatedStoragePoolEligibleNode{
			NodeName:        "node-1",
			LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{},
		}
		Expect(eligibleNodeEqual(a, b)).To(BeFalse())
	})

	It("returns false when LVMVolumeGroup content differs", func() {
		a := v1alpha1.ReplicatedStoragePoolEligibleNode{
			NodeName:        "node-1",
			LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{{Name: "lvg-1", Ready: true}},
		}
		b := v1alpha1.ReplicatedStoragePoolEligibleNode{
			NodeName:        "node-1",
			LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{{Name: "lvg-1", Ready: false}},
		}
		Expect(eligibleNodeEqual(a, b)).To(BeFalse())
	})
})

var _ = Describe("computeChangedEligibleNodes", func() {
	It("returns empty for two empty lists", func() {
		changed := computeChangedEligibleNodes(nil, nil)
		Expect(changed).To(BeEmpty())
	})

	It("returns all added nodes", func() {
		newNodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1"},
			{NodeName: "node-2"},
		}
		changed := computeChangedEligibleNodes(nil, newNodes)
		Expect(changed).To(ConsistOf("node-1", "node-2"))
	})

	It("returns all removed nodes", func() {
		oldNodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1"},
			{NodeName: "node-2"},
		}
		changed := computeChangedEligibleNodes(oldNodes, nil)
		Expect(changed).To(ConsistOf("node-1", "node-2"))
	})

	It("returns empty when lists are identical", func() {
		nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1", NodeReady: true},
			{NodeName: "node-2", NodeReady: true},
		}
		changed := computeChangedEligibleNodes(nodes, nodes)
		Expect(changed).To(BeEmpty())
	})

	It("returns modified node", func() {
		oldNodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1", NodeReady: true},
			{NodeName: "node-2", NodeReady: true},
		}
		newNodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1", NodeReady: true},
			{NodeName: "node-2", NodeReady: false}, // Changed.
		}
		changed := computeChangedEligibleNodes(oldNodes, newNodes)
		Expect(changed).To(ConsistOf("node-2"))
	})

	It("returns added, removed, and modified nodes", func() {
		oldNodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1", NodeReady: true},
			{NodeName: "node-2", NodeReady: true},
			{NodeName: "node-3", NodeReady: true},
		}
		newNodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-2", NodeReady: false}, // Modified.
			{NodeName: "node-3", NodeReady: true},  // Unchanged.
			{NodeName: "node-4", NodeReady: true},  // Added.
		}
		// node-1 removed, node-2 modified, node-4 added.
		changed := computeChangedEligibleNodes(oldNodes, newNodes)
		Expect(changed).To(ConsistOf("node-1", "node-2", "node-4"))
	})

	It("handles interleaved additions and removals correctly", func() {
		oldNodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-a"},
			{NodeName: "node-c"},
			{NodeName: "node-e"},
		}
		newNodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-b"},
			{NodeName: "node-c"},
			{NodeName: "node-d"},
		}
		// node-a removed, node-b added, node-c unchanged, node-d added, node-e removed.
		changed := computeChangedEligibleNodes(oldNodes, newNodes)
		Expect(changed).To(ConsistOf("node-a", "node-b", "node-d", "node-e"))
	})

	It("handles unsorted input correctly (builds sorted index)", func() {
		// Unsorted old nodes.
		oldNodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-c", NodeReady: true},
			{NodeName: "node-a", NodeReady: true},
			{NodeName: "node-b", NodeReady: true},
		}
		// Unsorted new nodes with modifications.
		newNodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-b", NodeReady: false}, // Modified.
			{NodeName: "node-d", NodeReady: true},  // Added.
			{NodeName: "node-a", NodeReady: true},  // Unchanged.
		}
		// node-c removed, node-b modified, node-d added.
		changed := computeChangedEligibleNodes(oldNodes, newNodes)
		Expect(changed).To(ConsistOf("node-c", "node-b", "node-d"))
	})
})

var _ = Describe("mapAgentPodToRVRs", func() {
	var scheme *runtime.Scheme
	const testNamespace = "d8-sds-replicated-volume"

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
	})

	It("returns requests for RVRs on the same node", func() {
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
		rvr1 := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-1"},
		}
		rvr2 := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-2"},
			Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-1"},
		}
		rvrOther := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-other"},
			Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-2"},
		}

		cl := testhelpers.WithRVRByNodeNameIndex(
			fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(pod, rvr1, rvr2, rvrOther),
		).Build()

		mapFunc := mapAgentPodToRVRs(cl, testNamespace)
		requests := mapFunc(context.Background(), pod)

		Expect(requests).To(HaveLen(2))
		names := []string{requests[0].Name, requests[1].Name}
		Expect(names).To(ContainElements("rvr-1", "rvr-2"))
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
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-1"},
		}

		cl := testhelpers.WithRVRByNodeNameIndex(
			fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(pod, rvr),
		).Build()

		mapFunc := mapAgentPodToRVRs(cl, testNamespace)
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
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-1"},
		}

		cl := testhelpers.WithRVRByNodeNameIndex(
			fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(pod, rvr),
		).Build()

		mapFunc := mapAgentPodToRVRs(cl, testNamespace)
		requests := mapFunc(context.Background(), pod)

		Expect(requests).To(BeNil())
	})

	It("returns nil for unscheduled pod (empty NodeName)", func() {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "agent-pod-1",
				Namespace: testNamespace,
				Labels:    map[string]string{"app": "agent"},
			},
			// No NodeName set - pod not yet scheduled.
		}
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-1"},
		}

		cl := testhelpers.WithRVRByNodeNameIndex(
			fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(pod, rvr),
		).Build()

		mapFunc := mapAgentPodToRVRs(cl, testNamespace)
		requests := mapFunc(context.Background(), pod)

		Expect(requests).To(BeNil())
	})

	It("returns nil for non-Pod object", func() {
		cl := fake.NewClientBuilder().WithScheme(scheme).Build()

		mapFunc := mapAgentPodToRVRs(cl, testNamespace)
		requests := mapFunc(context.Background(), &corev1.Node{})

		Expect(requests).To(BeNil())
	})

	It("returns nil for nil object", func() {
		cl := fake.NewClientBuilder().WithScheme(scheme).Build()

		mapFunc := mapAgentPodToRVRs(cl, testNamespace)
		requests := mapFunc(context.Background(), nil)

		Expect(requests).To(BeNil())
	})

	It("returns nil when List fails", func() {
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

		cl := fake.NewClientBuilder().
			WithScheme(scheme).
			WithInterceptorFuncs(interceptor.Funcs{
				List: func(_ context.Context, _ client.WithWatch, _ client.ObjectList, _ ...client.ListOption) error {
					return errors.New("list error")
				},
			}).
			Build()

		mapFunc := mapAgentPodToRVRs(cl, testNamespace)
		requests := mapFunc(context.Background(), pod)

		Expect(requests).To(BeNil())
	})
})

var _ = Describe("rspEventHandler", func() {
	var scheme *runtime.Scheme
	var queue *fakeQueue

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		queue = &fakeQueue{}
	})

	Describe("Create", func() {
		It("enqueues all RVRs for RVs using the RSP", func() {
			rsp := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
						{NodeName: "node-1"},
						{NodeName: "node-2"},
					},
				},
			}
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Status: v1alpha1.ReplicatedVolumeStatus{
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						Topology:        v1alpha1.TopologyIgnored,
						Replication:     v1alpha1.ReplicationConsistencyAndAvailability,
						VolumeAccess:    v1alpha1.VolumeAccessPreferablyLocal,
						StoragePoolName: "rsp-1",
					},
				},
			}
			rvr1 := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-1",
					NodeName:             "node-1",
				},
			}
			rvr2 := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-2"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-1",
					NodeName:             "node-2",
				},
			}
			rvrOther := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-other"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-other",
					NodeName:             "node-1",
				},
			}

			cl := testhelpers.WithRVByStoragePoolNameIndex(
				testhelpers.WithRVRByReplicatedVolumeNameIndex(
					fake.NewClientBuilder().
						WithScheme(scheme).
						WithObjects(rsp, rv, rvr1, rvr2, rvrOther),
				),
			).Build()

			handler := newRSPEventHandler(cl).(*rspEventHandler)
			handler.Create(context.Background(), event.CreateEvent{Object: rsp}, queue)

			Expect(queue.items).To(HaveLen(2))
			names := []string{queue.items[0].Name, queue.items[1].Name}
			Expect(names).To(ContainElements("rvr-1", "rvr-2"))
		})

		It("does nothing when no RVs use the RSP", func() {
			rsp := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-unused"},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
						{NodeName: "node-1"},
					},
				},
			}
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Status: v1alpha1.ReplicatedVolumeStatus{
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						Topology:        v1alpha1.TopologyIgnored,
						Replication:     v1alpha1.ReplicationConsistencyAndAvailability,
						VolumeAccess:    v1alpha1.VolumeAccessPreferablyLocal,
						StoragePoolName: "other-rsp",
					},
				},
			}

			cl := testhelpers.WithRVByStoragePoolNameIndex(
				testhelpers.WithRVRByReplicatedVolumeNameIndex(
					fake.NewClientBuilder().
						WithScheme(scheme).
						WithObjects(rsp, rv),
				),
			).Build()

			handler := newRSPEventHandler(cl).(*rspEventHandler)
			handler.Create(context.Background(), event.CreateEvent{Object: rsp}, queue)

			Expect(queue.items).To(BeEmpty())
		})

		It("does nothing for non-RSP object", func() {
			cl := fake.NewClientBuilder().WithScheme(scheme).Build()

			handler := newRSPEventHandler(cl).(*rspEventHandler)
			handler.Create(context.Background(), event.CreateEvent{Object: &corev1.Node{}}, queue)

			Expect(queue.items).To(BeEmpty())
		})
	})

	Describe("Update", func() {
		It("enqueues RVRs only on changed nodes", func() {
			oldRSP := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
						{NodeName: "node-1", NodeReady: true},
						{NodeName: "node-2", NodeReady: true},
					},
				},
			}
			newRSP := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
						{NodeName: "node-1", NodeReady: true},
						{NodeName: "node-2", NodeReady: false}, // Changed.
					},
				},
			}
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Status: v1alpha1.ReplicatedVolumeStatus{
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						Topology:        v1alpha1.TopologyIgnored,
						Replication:     v1alpha1.ReplicationConsistencyAndAvailability,
						VolumeAccess:    v1alpha1.VolumeAccessPreferablyLocal,
						StoragePoolName: "rsp-1",
					},
				},
			}
			rvr1 := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-1",
					NodeName:             "node-1",
				},
			}
			rvr2 := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-2"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-1",
					NodeName:             "node-2",
				},
			}

			cl := testhelpers.WithRVByStoragePoolNameIndex(
				testhelpers.WithRVRByRVAndNodeIndex(
					fake.NewClientBuilder().
						WithScheme(scheme).
						WithObjects(newRSP, rv, rvr1, rvr2),
				),
			).Build()

			handler := newRSPEventHandler(cl).(*rspEventHandler)
			handler.Update(context.Background(), event.UpdateEvent{ObjectOld: oldRSP, ObjectNew: newRSP}, queue)

			// Only rvr-2 on node-2 should be enqueued.
			Expect(queue.items).To(HaveLen(1))
			Expect(queue.items[0].Name).To(Equal("rvr-2"))
		})

		It("does nothing when no eligible nodes changed", func() {
			oldRSP := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
						{NodeName: "node-1", NodeReady: true},
					},
				},
			}
			newRSP := oldRSP.DeepCopy()

			cl := fake.NewClientBuilder().WithScheme(scheme).Build()

			handler := newRSPEventHandler(cl).(*rspEventHandler)
			handler.Update(context.Background(), event.UpdateEvent{ObjectOld: oldRSP, ObjectNew: newRSP}, queue)

			Expect(queue.items).To(BeEmpty())
		})

		It("enqueues RVRs on added nodes", func() {
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
						{NodeName: "node-2"}, // Added.
					},
				},
			}
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Status: v1alpha1.ReplicatedVolumeStatus{
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						Topology:        v1alpha1.TopologyIgnored,
						Replication:     v1alpha1.ReplicationConsistencyAndAvailability,
						VolumeAccess:    v1alpha1.VolumeAccessPreferablyLocal,
						StoragePoolName: "rsp-1",
					},
				},
			}
			rvr2 := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-2"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-1",
					NodeName:             "node-2",
				},
			}

			cl := testhelpers.WithRVByStoragePoolNameIndex(
				testhelpers.WithRVRByRVAndNodeIndex(
					fake.NewClientBuilder().
						WithScheme(scheme).
						WithObjects(newRSP, rv, rvr2),
				),
			).Build()

			handler := newRSPEventHandler(cl).(*rspEventHandler)
			handler.Update(context.Background(), event.UpdateEvent{ObjectOld: oldRSP, ObjectNew: newRSP}, queue)

			Expect(queue.items).To(HaveLen(1))
			Expect(queue.items[0].Name).To(Equal("rvr-2"))
		})

		It("does nothing for non-RSP object", func() {
			cl := fake.NewClientBuilder().WithScheme(scheme).Build()

			handler := newRSPEventHandler(cl).(*rspEventHandler)
			handler.Update(context.Background(), event.UpdateEvent{
				ObjectOld: &corev1.Node{},
				ObjectNew: &corev1.Node{},
			}, queue)

			Expect(queue.items).To(BeEmpty())
		})
	})

	Describe("Delete", func() {
		It("enqueues all RVRs for RVs using the RSP", func() {
			rsp := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
						{NodeName: "node-1"},
					},
				},
			}
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Status: v1alpha1.ReplicatedVolumeStatus{
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						Topology:        v1alpha1.TopologyIgnored,
						Replication:     v1alpha1.ReplicationConsistencyAndAvailability,
						VolumeAccess:    v1alpha1.VolumeAccessPreferablyLocal,
						StoragePoolName: "rsp-1",
					},
				},
			}
			rvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-1",
					NodeName:             "node-1",
				},
			}

			cl := testhelpers.WithRVByStoragePoolNameIndex(
				testhelpers.WithRVRByReplicatedVolumeNameIndex(
					fake.NewClientBuilder().
						WithScheme(scheme).
						WithObjects(rsp, rv, rvr),
				),
			).Build()

			handler := newRSPEventHandler(cl).(*rspEventHandler)
			handler.Delete(context.Background(), event.DeleteEvent{Object: rsp}, queue)

			Expect(queue.items).To(HaveLen(1))
			Expect(queue.items[0].Name).To(Equal("rvr-1"))
		})

		It("does nothing for non-RSP object", func() {
			cl := fake.NewClientBuilder().WithScheme(scheme).Build()

			handler := newRSPEventHandler(cl).(*rspEventHandler)
			handler.Delete(context.Background(), event.DeleteEvent{Object: &corev1.Node{}}, queue)

			Expect(queue.items).To(BeEmpty())
		})
	})
})

// fakeQueue implements workqueue.TypedRateLimitingInterface for testing.
type fakeQueue struct {
	items []reconcile.Request
}

func (q *fakeQueue) Add(item reconcile.Request)     { q.items = append(q.items, item) }
func (q *fakeQueue) Len() int                       { return len(q.items) }
func (q *fakeQueue) Get() (reconcile.Request, bool) { return reconcile.Request{}, false }
func (q *fakeQueue) Done(reconcile.Request)         {}
func (q *fakeQueue) ShutDown()                      {}
func (q *fakeQueue) ShutDownWithDrain()             {}
func (q *fakeQueue) ShuttingDown() bool             { return false }
func (q *fakeQueue) AddAfter(item reconcile.Request, _ time.Duration) {
	q.items = append(q.items, item)
}
func (q *fakeQueue) AddRateLimited(reconcile.Request)  {}
func (q *fakeQueue) Forget(reconcile.Request)          {}
func (q *fakeQueue) NumRequeues(reconcile.Request) int { return 0 }
