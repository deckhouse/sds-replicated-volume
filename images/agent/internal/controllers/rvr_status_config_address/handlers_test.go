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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/cluster"
	rvrstatusconfigaddress "github.com/deckhouse/sds-replicated-volume/images/agent/internal/controllers/rvr_status_config_address"
)

var _ = Describe("Handlers", func() {
	const nodeName = "test-node"

	var log logr.Logger

	BeforeEach(func() {
		log = GinkgoLogr
	})

	Describe("ConfigMap", func() {
		var (
			configMap *corev1.ConfigMap
			handler   func(context.Context, client.Object) []reconcile.Request
			pred      predicate.Funcs
			oldCM     *corev1.ConfigMap
			newCM     *corev1.ConfigMap
			e         event.UpdateEvent
		)

		BeforeEach(func() {
			configMap = &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cluster.ConfigMapName,
					Namespace: cluster.ConfigMapNamespace,
				},
			}
			handler = nil
			pred = predicate.Funcs{}
			oldCM = configMap.DeepCopy()
			oldCM.Data = map[string]string{
				"drbdMinPort": "7000",
				"drbdMaxPort": "9000",
			}
			newCM = oldCM.DeepCopy()
		})

		JustBeforeEach(func() {
			handler = rvrstatusconfigaddress.NewConfigMapEnqueueHandler(nodeName, log)
			pred = rvrstatusconfigaddress.NewConfigMapUpdatePredicate(log)
			e = event.UpdateEvent{
				ObjectOld: oldCM,
				ObjectNew: newCM,
			}
		})

		It("should enqueue node for agent-config ConfigMap", func(ctx SpecContext) {
			ExpectEnqueueNodeForRequest(handler, ctx, configMap, nodeName)
		})

		DescribeTableSubtree("should not enqueue",
			Entry("ConfigMap has wrong name", func() client.Object {
				configMap.Name = "wrong-name"
				return configMap
			}),
			Entry("ConfigMap has wrong namespace", func() client.Object {
				configMap.Namespace = "wrong-namespace"
				return configMap
			}),
			Entry("object is not ConfigMap", func() client.Object {
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

		DescribeTableSubtree("should return true when port settings change",
			Entry("min port changes", func() {
				newCM.Data["drbdMinPort"] = "8000"
			}),
			Entry("max port changes", func() {
				newCM.Data["drbdMaxPort"] = "10000"
			}),
			func(beforeEach func()) {
				BeforeEach(beforeEach)

				It("should return true", func() {
					Expect(pred.Update(e)).To(BeTrue())
				})
			})

		DescribeTableSubtree("should return false",
			Entry("port settings do not change", func() {
				newCM.Data["otherKey"] = "otherValue"
			}),
			Entry("other Data fields change", func() {
				newCM.Data["drbdMinPort"] = "7000"
				newCM.Data["drbdMaxPort"] = "9000"
				newCM.Data["otherKey"] = "otherValue"
			}),
			Entry("Labels change", func() {
				newCM.Labels = map[string]string{"key": "value"}
			}),
			Entry("Annotations change", func() {
				newCM.Annotations = map[string]string{"key": "value"}
			}),
			Entry("ConfigMap has wrong name", func() {
				oldCM.Name = "wrong-name"
				newCM.Name = "wrong-name"
			}),
			Entry("old object is not ConfigMap", func() {
				e.ObjectOld = &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "test-node"}}
			}),
			Entry("new object is not ConfigMap", func() {
				e.ObjectNew = &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "test-node"}}
			}),
			Entry("both objects are not ConfigMap", func() {
				e.ObjectOld = &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "test-node"}}
				e.ObjectNew = &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "test-node"}}
			}),
			func(beforeEach func()) {
				BeforeEach(beforeEach)

				It("should return false", func() {
					Expect(pred.Update(e)).To(BeFalse())
				})
			})
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
			ExpectEnqueueNodeForRequest(handler, ctx, rvr, nodeName)
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

		It("should have Create field nil", func() {
			Expect(pred.Create).To(BeNil())
		})

		It("should have Delete field nil", func() {
			Expect(pred.Delete).To(BeNil())
		})

		It("should have Generic field nil", func() {
			Expect(pred.Generic).To(BeNil())
		})

		DescribeTableSubtree("should return true",
			Entry("RVR is on current node", func() {
				_ = oldRVR
				_ = newRVR
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
})
