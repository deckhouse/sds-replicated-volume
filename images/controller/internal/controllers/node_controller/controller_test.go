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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// testQueue is a minimal implementation to capture enqueued requests.
type testQueue struct {
	items []reconcile.Request
}

func (q *testQueue) Add(item reconcile.Request)                { q.items = append(q.items, item) }
func (q *testQueue) Len() int                                  { return len(q.items) }
func (q *testQueue) Get() (reconcile.Request, bool)            { return reconcile.Request{}, false }
func (q *testQueue) Done(reconcile.Request)                    {}
func (q *testQueue) ShutDown()                                 {}
func (q *testQueue) ShutDownWithDrain()                        {}
func (q *testQueue) ShuttingDown() bool                        { return false }
func (q *testQueue) AddAfter(reconcile.Request, time.Duration) {}
func (q *testQueue) AddRateLimited(reconcile.Request)          {}
func (q *testQueue) Forget(reconcile.Request)                  {}
func (q *testQueue) NumRequeues(reconcile.Request) int         { return 0 }

func requestNames(items []reconcile.Request) []string {
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.Name)
	}
	return names
}

var _ = Describe("enqueueEligibleNodesDelta", func() {
	var q *testQueue

	BeforeEach(func() {
		q = &testQueue{}
	})

	It("enqueues nothing when both slices are empty", func() {
		enqueueEligibleNodesDelta(q, nil, nil)

		Expect(q.items).To(BeEmpty())
	})

	It("enqueues nothing when slices are equal", func() {
		nodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1"},
			{NodeName: "node-2"},
		}

		enqueueEligibleNodesDelta(q, nodes, nodes)

		Expect(q.items).To(BeEmpty())
	})

	It("enqueues added node", func() {
		oldNodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1"},
		}
		newNodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1"},
			{NodeName: "node-2"},
		}

		enqueueEligibleNodesDelta(q, oldNodes, newNodes)

		Expect(requestNames(q.items)).To(ConsistOf("node-2"))
	})

	It("enqueues removed node", func() {
		oldNodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1"},
			{NodeName: "node-2"},
		}
		newNodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1"},
		}

		enqueueEligibleNodesDelta(q, oldNodes, newNodes)

		Expect(requestNames(q.items)).To(ConsistOf("node-2"))
	})

	It("enqueues all changed nodes (added and removed)", func() {
		oldNodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1"},
			{NodeName: "node-3"},
		}
		newNodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-2"},
			{NodeName: "node-3"},
		}

		enqueueEligibleNodesDelta(q, oldNodes, newNodes)

		Expect(requestNames(q.items)).To(ConsistOf("node-1", "node-2"))
	})

	It("enqueues all nodes when old is empty", func() {
		newNodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1"},
			{NodeName: "node-2"},
		}

		enqueueEligibleNodesDelta(q, nil, newNodes)

		Expect(requestNames(q.items)).To(ConsistOf("node-1", "node-2"))
	})

	It("enqueues all nodes when new is empty", func() {
		oldNodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1"},
			{NodeName: "node-2"},
		}

		enqueueEligibleNodesDelta(q, oldNodes, nil)

		Expect(requestNames(q.items)).To(ConsistOf("node-1", "node-2"))
	})

	It("handles completely different sets", func() {
		oldNodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-a"},
			{NodeName: "node-b"},
		}
		newNodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-x"},
			{NodeName: "node-y"},
		}

		enqueueEligibleNodesDelta(q, oldNodes, newNodes)

		Expect(requestNames(q.items)).To(ConsistOf("node-a", "node-b", "node-x", "node-y"))
	})

	It("skips nodes with empty names", func() {
		oldNodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: ""},
			{NodeName: "node-1"},
		}
		newNodes := []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1"},
			{NodeName: "node-2"},
		}

		enqueueEligibleNodesDelta(q, oldNodes, newNodes)

		Expect(requestNames(q.items)).To(ConsistOf("node-2"))
	})
})

var _ = Describe("mapDRBDResourceToNode", func() {
	It("returns request for node when nodeName is set", func() {
		dr := &v1alpha1.DRBDResource{
			ObjectMeta: metav1.ObjectMeta{Name: "drbd-1"},
			Spec:       v1alpha1.DRBDResourceSpec{NodeName: "node-1"},
		}

		requests := mapDRBDResourceToNode(context.Background(), dr)

		Expect(requests).To(HaveLen(1))
		Expect(requests[0].Name).To(Equal("node-1"))
	})

	It("returns nil when nodeName is empty", func() {
		dr := &v1alpha1.DRBDResource{
			ObjectMeta: metav1.ObjectMeta{Name: "drbd-1"},
			Spec:       v1alpha1.DRBDResourceSpec{NodeName: ""},
		}

		requests := mapDRBDResourceToNode(context.Background(), dr)

		Expect(requests).To(BeNil())
	})

	It("returns nil when object is not DRBDResource", func() {
		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
		}

		requests := mapDRBDResourceToNode(context.Background(), node)

		Expect(requests).To(BeNil())
	})

	It("returns nil when object is nil", func() {
		requests := mapDRBDResourceToNode(context.Background(), nil)

		Expect(requests).To(BeNil())
	})
})

var _ = Describe("enqueueNodesFromRSP", func() {
	var q *testQueue

	BeforeEach(func() {
		q = &testQueue{}
	})

	It("enqueues all nodes from RSP eligibleNodes", func() {
		rsp := &v1alpha1.ReplicatedStoragePool{
			Status: v1alpha1.ReplicatedStoragePoolStatus{
				EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
					{NodeName: "node-1"},
					{NodeName: "node-2"},
					{NodeName: "node-3"},
				},
			},
		}

		enqueueNodesFromRSP(q, rsp)

		Expect(requestNames(q.items)).To(ConsistOf("node-1", "node-2", "node-3"))
	})

	It("enqueues nothing when eligibleNodes is empty", func() {
		rsp := &v1alpha1.ReplicatedStoragePool{
			Status: v1alpha1.ReplicatedStoragePoolStatus{
				EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{},
			},
		}

		enqueueNodesFromRSP(q, rsp)

		Expect(q.items).To(BeEmpty())
	})

	It("skips nodes with empty names", func() {
		rsp := &v1alpha1.ReplicatedStoragePool{
			Status: v1alpha1.ReplicatedStoragePoolStatus{
				EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
					{NodeName: "node-1"},
					{NodeName: ""},
					{NodeName: "node-2"},
				},
			},
		}

		enqueueNodesFromRSP(q, rsp)

		Expect(requestNames(q.items)).To(ConsistOf("node-1", "node-2"))
	})
})
