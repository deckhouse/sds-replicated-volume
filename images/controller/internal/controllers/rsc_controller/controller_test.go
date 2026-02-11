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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

var _ = Describe("Mapper functions", func() {
	Describe("mapRSPToRSC", func() {
		It("returns requests for RSCs from usedBy", func() {
			rsp := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "pool-1"},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					UsedBy: v1alpha1.ReplicatedStoragePoolUsedBy{
						ReplicatedStorageClassNames: []string{"rsc-deleted-1", "rsc-deleted-2"},
					},
				},
			}

			mapFunc := mapRSPToRSC()
			requests := mapFunc(context.Background(), rsp)

			Expect(requests).To(HaveLen(2))
			names := []string{requests[0].Name, requests[1].Name}
			Expect(names).To(ContainElements("rsc-deleted-1", "rsc-deleted-2"))
		})

		It("deduplicates usedBy entries", func() {
			rsp := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "pool-1"},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					UsedBy: v1alpha1.ReplicatedStoragePoolUsedBy{
						ReplicatedStorageClassNames: []string{"rsc-1", "rsc-orphan", "rsc-1"},
					},
				},
			}

			mapFunc := mapRSPToRSC()
			requests := mapFunc(context.Background(), rsp)

			Expect(requests).To(HaveLen(2))
			names := []string{requests[0].Name, requests[1].Name}
			Expect(names).To(ContainElements("rsc-1", "rsc-orphan"))
		})

		It("returns nil when no RSCs reference the RSP and usedBy is empty", func() {
			rsp := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "pool-unused"},
			}

			mapFunc := mapRSPToRSC()
			requests := mapFunc(context.Background(), rsp)

			Expect(requests).To(BeNil())
		})

		It("returns nil for non-RSP object", func() {
			mapFunc := mapRSPToRSC()
			requests := mapFunc(context.Background(), &corev1.Node{})

			Expect(requests).To(BeNil())
		})
	})

	Describe("rvEventHandler", func() {
		var handler = rvEventHandler()
		var queue *fakeQueue

		BeforeEach(func() {
			queue = &fakeQueue{}
		})

		It("enqueues RSC on RV create", func() {
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					ReplicatedStorageClassName: "rsc-1",
				},
			}

			handler.Create(context.Background(), toCreateEvent(rv), queue)

			Expect(queue.items).To(HaveLen(1))
			Expect(queue.items[0].Name).To(Equal("rsc-1"))
		})

		It("enqueues both old and new RSC on RV update with changed RSC", func() {
			oldRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					ReplicatedStorageClassName: "rsc-old",
				},
			}
			newRV := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					ReplicatedStorageClassName: "rsc-new",
				},
			}

			handler.Update(context.Background(), toUpdateEvent(oldRV, newRV), queue)

			Expect(queue.items).To(HaveLen(2))
			names := []string{queue.items[0].Name, queue.items[1].Name}
			Expect(names).To(ContainElements("rsc-old", "rsc-new"))
		})

		It("enqueues RSC on RV delete", func() {
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					ReplicatedStorageClassName: "rsc-1",
				},
			}

			handler.Delete(context.Background(), toDeleteEvent(rv), queue)

			Expect(queue.items).To(HaveLen(1))
			Expect(queue.items[0].Name).To(Equal("rsc-1"))
		})

		It("does not enqueue when RSC name is empty", func() {
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Spec:       v1alpha1.ReplicatedVolumeSpec{},
			}

			handler.Create(context.Background(), toCreateEvent(rv), queue)

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

func toCreateEvent(obj client.Object) event.TypedCreateEvent[client.Object] {
	return event.TypedCreateEvent[client.Object]{Object: obj}
}

func toUpdateEvent(oldObj, newObj client.Object) event.TypedUpdateEvent[client.Object] {
	return event.TypedUpdateEvent[client.Object]{ObjectOld: oldObj, ObjectNew: newObj}
}

func toDeleteEvent(obj client.Object) event.TypedDeleteEvent[client.Object] {
	return event.TypedDeleteEvent[client.Object]{Object: obj}
}
