/*
Copyright 2025 Flant JSC

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

package rvrstatusconfigaddress_test

import (
	"context"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	rvrstatusconfigaddress "github.com/deckhouse/sds-replicated-volume/images/agent/internal/controllers/rvr_status_config_address"
)

var _ = Describe("Handlers", func() {
	const nodeName = "test-node"

	var log logr.Logger

	BeforeEach(func() {
		log = GinkgoLogr
	})

	Describe("ReplicatedVolumeReplicaEnqueueHandler", func() {
		var (
			handler func(context.Context, client.Object) []reconcile.Request
			rvr     *v1alpha3.ReplicatedVolumeReplica
		)

		BeforeEach(func() {
			handler = nil
			rvr = &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "test-rvr"},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					NodeName: nodeName,
				},
			}
		})

		JustBeforeEach(func() {
			handler = rvrstatusconfigaddress.NewReplicatedVolumeReplicaEnqueueHandler(nodeName, log)
		})

		It("should enqueue node for RVR on current node", func(ctx SpecContext) {
			Expect(handler(ctx, rvr)).To(SatisfyAll(
				HaveLen(1),
				Enqueue(reconcile.Request{NamespacedName: types.NamespacedName{Name: nodeName}}),
			))
		})

		DescribeTableSubtree("should not enqueue",
			Entry("RVR is on other node", func() client.Object {
				rvr.Spec.NodeName = "other-node"
				return rvr
			}),
			Entry("object is not RVR", func() client.Object {
				return &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{Name: "test-node"},
				}
			}),
			func(getObj func() client.Object) {
				var obj client.Object

				BeforeEach(func() {
					obj = getObj()
				})

				It("should not enqueue", func(ctx SpecContext) {
					Expect(handler(ctx, obj)).To(BeEmpty())
				})
			})
	})

	Describe("ReplicatedVolumeReplicaUpdatePredicate", func() {
		var (
			pred   predicate.Funcs
			oldRVR *v1alpha3.ReplicatedVolumeReplica
			newRVR *v1alpha3.ReplicatedVolumeReplica
			e      event.UpdateEvent
		)

		BeforeEach(func() {
			pred = predicate.Funcs{}
			oldRVR = &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "test-rvr"},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					NodeName: nodeName,
				},
			}
			newRVR = oldRVR.DeepCopy()
		})

		JustBeforeEach(func() {
			pred = rvrstatusconfigaddress.NewReplicatedVolumeReplicaUpdatePredicate(nodeName, log)
			e = event.UpdateEvent{
				ObjectOld: oldRVR,
				ObjectNew: newRVR,
			}
		})

		It("should have UpdateFunc not nil", func() {
			Expect(pred.UpdateFunc).ToNot(BeNil())
		})

		It("should have CreateFunc field nil", func() {
			Expect(pred.CreateFunc).To(BeNil(), "if this failed please add cases for this function")
		})

		It("should have DeleteFunc field nil", func() {
			Expect(pred.DeleteFunc).To(BeNil(), "if this failed please add cases for this function")
		})

		It("should have GenericFunc field nil", func() {
			Expect(pred.GenericFunc).To(BeNil(), "if this failed please add cases for this function")
		})

		It("should have Create() not filtering", func() {
			Expect(pred.Create(event.CreateEvent{})).To(BeTrue())
		})

		It("should have Delete() not filtering", func() {
			Expect(pred.Delete(event.DeleteEvent{})).To(BeTrue())
		})

		It("should have Generic() not filtering", func() {
			Expect(pred.Generic(event.GenericEvent{})).To(BeTrue())
		})

		DescribeTableSubtree("should return true",
			Entry("RVR is on current node", func() {
				oldRVR.Spec.NodeName = nodeName
				newRVR.Spec.NodeName = nodeName
			}),
			Entry("NodeName changes on current node", func() {
				oldRVR.Spec.NodeName = "other-node"
			}),
			func(beforeEach func()) {
				BeforeEach(beforeEach)

				It("should return true", func() {
					Expect(pred.Update(e)).To(BeTrue())
				})
			})

		DescribeTableSubtree("should return false",
			Entry("RVR is on other node", func() {
				oldRVR.Spec.NodeName = "other-node"
				newRVR.Spec.NodeName = "other-node"
			}),
			Entry("object is not RVR", func() {
				e.ObjectOld = &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "test-node"}}
				e.ObjectNew = &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "test-node"}}
			}),
			func(justBeforeEach func()) {
				JustBeforeEach(justBeforeEach)

				It("should return false", func() {
					Expect(pred.Update(e)).To(BeFalse())
				})
			})
	})

	Describe("NodePredicate", func() {
		var (
			pred predicate.Funcs
			node *corev1.Node
		)

		BeforeEach(func() {
			pred = predicate.Funcs{}
			node = &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: nodeName},
			}
		})

		JustBeforeEach(func() {
			pred = rvrstatusconfigaddress.NewNodePredicate(nodeName, log)
		})

		It("should have GenericFunc not nil", func() {
			Expect(pred.GenericFunc).ToNot(BeNil())
		})

		It("should have CreateFunc not nil", func() {
			Expect(pred.CreateFunc).ToNot(BeNil())
		})

		It("should have UpdateFunc not nil", func() {
			Expect(pred.UpdateFunc).ToNot(BeNil())
		})

		It("should have DeleteFunc not nil", func() {
			Expect(pred.DeleteFunc).ToNot(BeNil())
		})

		DescribeTableSubtree("should return true for current node",
			Entry("Generic event", func() any {
				return event.GenericEvent{Object: node}
			}),
			Entry("Create event", func() any {
				return event.CreateEvent{Object: node}
			}),
			Entry("Update event", func() any {
				return event.UpdateEvent{ObjectNew: node, ObjectOld: node}
			}),
			Entry("Delete event", func() any {
				return event.DeleteEvent{Object: node}
			}),
			func(getEvent func() any) {
				var e any

				BeforeEach(func() {
					e = getEvent()
				})

				It("should return true", func() {
					switch ev := e.(type) {
					case event.GenericEvent:
						Expect(pred.Generic(ev)).To(BeTrue())
					case event.CreateEvent:
						Expect(pred.Create(ev)).To(BeTrue())
					case event.UpdateEvent:
						Expect(pred.Update(ev)).To(BeTrue())
					case event.DeleteEvent:
						Expect(pred.Delete(ev)).To(BeTrue())
					}
				})
			})

		DescribeTableSubtree("should return false",
			Entry("node is other node", func() client.Object {
				return &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{Name: "other-node"},
				}
			}),
			Entry("object is not Node", func() client.Object {
				return &v1alpha3.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{Name: "test-rvr"},
				}
			}),
			func(getObj func() client.Object) {
				var obj client.Object

				BeforeEach(func() {
					obj = getObj()
				})

				It("should return false for Generic", func() {
					Expect(pred.Generic(event.GenericEvent{Object: obj})).To(BeFalse())
				})

				It("should return false for Create", func() {
					Expect(pred.Create(event.CreateEvent{Object: obj})).To(BeFalse())
				})

				It("should return false for Update", func() {
					Expect(pred.Update(event.UpdateEvent{ObjectNew: obj, ObjectOld: obj})).To(BeFalse())
				})

				It("should return false for Delete", func() {
					Expect(pred.Delete(event.DeleteEvent{Object: obj})).To(BeFalse())
				})
			})
	})
})
