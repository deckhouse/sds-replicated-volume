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

package rvrstatusconditions_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	rvrstatusconditions "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_status_conditions"
	rv "github.com/deckhouse/sds-replicated-volume/images/controller/internal/reconcile/rv"
)

var _ = Describe("Reconciler", func() {
	var (
		clientBuilder *fake.ClientBuilder
		scheme        *runtime.Scheme
		cl            client.WithWatch
		rec           *rvrstatusconditions.Reconciler
	)

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha3.AddToScheme(scheme)).To(Succeed(), "should add v1alpha3 to scheme")
		Expect(corev1.AddToScheme(scheme)).To(Succeed(), "should add corev1 to scheme")
		clientBuilder = fake.NewClientBuilder().
			WithScheme(scheme).
			// WithStatusSubresource makes fake client mimic real API server behavior
			WithStatusSubresource(&v1alpha3.ReplicatedVolumeReplica{})
	})

	JustBeforeEach(func() {
		cl = clientBuilder.Build()
		rec = rvrstatusconditions.NewReconciler(cl, GinkgoLogr)
	})

	It("returns no error when RVR does not exist", func(ctx SpecContext) {
		Expect(rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "non-existent"},
		})).ToNot(Requeue(), "should ignore NotFound errors")
	})

	When("RVR is being deleted but has finalizers", func() {
		BeforeEach(func() {
			now := metav1.Now()
			rvr := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "test-rvr",
					DeletionTimestamp: &now,
					Finalizers:        []string{"test-finalizer"},
				},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					NodeName: "node-1",
				},
				Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
					Conditions: []metav1.Condition{
						{Type: v1alpha3.ConditionTypeScheduled, Status: metav1.ConditionTrue, Reason: "Scheduled"},
						{Type: v1alpha3.ConditionTypeInitialized, Status: metav1.ConditionTrue, Reason: "Initialized"},
						{Type: v1alpha3.ConditionTypeInQuorum, Status: metav1.ConditionTrue, Reason: "InQuorum"},
						{Type: v1alpha3.ConditionTypeInSync, Status: metav1.ConditionTrue, Reason: "InSync"},
					},
				},
			}
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
					},
				},
			}
			agentPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "agent-node-1",
					Namespace: rv.ControllerConfigMapNamespace,
					Labels:    map[string]string{rvrstatusconditions.AgentPodLabel: rvrstatusconditions.AgentPodValue},
				},
				Spec:   corev1.PodSpec{NodeName: "node-1"},
				Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}},
			}
			clientBuilder = clientBuilder.WithObjects(rvr, node, agentPod)
		})

		It("should still update conditions for finalizer controllers", func(ctx SpecContext) {
			_, err := rec.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-rvr"},
			})
			Expect(err).ToNot(HaveOccurred())

			rvr := &v1alpha3.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, types.NamespacedName{Name: "test-rvr"}, rvr)).To(Succeed())

			Expect(meta.IsStatusConditionTrue(rvr.Status.Conditions, v1alpha3.ConditionTypeOnline)).To(BeTrue())
			Expect(meta.IsStatusConditionTrue(rvr.Status.Conditions, v1alpha3.ConditionTypeIOReady)).To(BeTrue())
		})
	})

	When("all conditions are True and agent is ready", func() {
		BeforeEach(func() {
			rvr := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-rvr",
				},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					NodeName: "node-1",
				},
				Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
					Conditions: []metav1.Condition{
						{Type: v1alpha3.ConditionTypeScheduled, Status: metav1.ConditionTrue, Reason: "Scheduled"},
						{Type: v1alpha3.ConditionTypeInitialized, Status: metav1.ConditionTrue, Reason: "Initialized"},
						{Type: v1alpha3.ConditionTypeInQuorum, Status: metav1.ConditionTrue, Reason: "InQuorum"},
						{Type: v1alpha3.ConditionTypeInSync, Status: metav1.ConditionTrue, Reason: "InSync"},
					},
				},
			}
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-1",
				},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
					},
				},
			}
			agentPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "agent-node-1",
					Namespace: rv.ControllerConfigMapNamespace,
					Labels: map[string]string{
						rvrstatusconditions.AgentPodLabel: rvrstatusconditions.AgentPodValue,
					},
				},
				Spec: corev1.PodSpec{
					NodeName: "node-1",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					Conditions: []corev1.PodCondition{
						{Type: corev1.PodReady, Status: corev1.ConditionTrue},
					},
				},
			}
			clientBuilder = clientBuilder.WithObjects(rvr, node, agentPod)
		})

		It("should set Online=True and IOReady=True", func(ctx SpecContext) {
			_, err := rec.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-rvr"},
			})
			Expect(err).ToNot(HaveOccurred())

			rvr := &v1alpha3.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, types.NamespacedName{Name: "test-rvr"}, rvr)).To(Succeed())

			Expect(meta.IsStatusConditionTrue(rvr.Status.Conditions, v1alpha3.ConditionTypeOnline)).To(BeTrue())
			Expect(meta.IsStatusConditionTrue(rvr.Status.Conditions, v1alpha3.ConditionTypeIOReady)).To(BeTrue())

			onlineCond := meta.FindStatusCondition(rvr.Status.Conditions, v1alpha3.ConditionTypeOnline)
			Expect(onlineCond.Reason).To(Equal(v1alpha3.ReasonOnline))

			ioReadyCond := meta.FindStatusCondition(rvr.Status.Conditions, v1alpha3.ConditionTypeIOReady)
			Expect(ioReadyCond.Reason).To(Equal(v1alpha3.ReasonIOReady))
		})
	})

	When("Scheduled condition is False", func() {
		BeforeEach(func() {
			rvr := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-rvr",
				},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					NodeName: "node-1",
				},
				Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
					Conditions: []metav1.Condition{
						{Type: v1alpha3.ConditionTypeScheduled, Status: metav1.ConditionFalse, Reason: "WaitingForNode"},
						{Type: v1alpha3.ConditionTypeInitialized, Status: metav1.ConditionTrue, Reason: "Initialized"},
						{Type: v1alpha3.ConditionTypeInQuorum, Status: metav1.ConditionTrue, Reason: "InQuorum"},
						{Type: v1alpha3.ConditionTypeInSync, Status: metav1.ConditionTrue, Reason: "InSync"},
					},
				},
			}
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
					},
				},
			}
			agentPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "agent-node-1",
					Namespace: rv.ControllerConfigMapNamespace,
					Labels:    map[string]string{rvrstatusconditions.AgentPodLabel: rvrstatusconditions.AgentPodValue},
				},
				Spec:   corev1.PodSpec{NodeName: "node-1"},
				Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}},
			}
			clientBuilder = clientBuilder.WithObjects(rvr, node, agentPod)
		})

		It("should set Online=False with reason copied from Scheduled condition", func(ctx SpecContext) {
			_, err := rec.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-rvr"},
			})
			Expect(err).ToNot(HaveOccurred())

			rvr := &v1alpha3.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, types.NamespacedName{Name: "test-rvr"}, rvr)).To(Succeed())

			onlineCond := meta.FindStatusCondition(rvr.Status.Conditions, v1alpha3.ConditionTypeOnline)
			Expect(onlineCond.Status).To(Equal(metav1.ConditionFalse))
			// Reason is copied from source Scheduled condition
			Expect(onlineCond.Reason).To(Equal("WaitingForNode"))
		})
	})

	When("Initialized condition is False", func() {
		BeforeEach(func() {
			rvr := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-rvr",
				},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					NodeName: "node-1",
				},
				Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
					Conditions: []metav1.Condition{
						{Type: v1alpha3.ConditionTypeScheduled, Status: metav1.ConditionTrue, Reason: "Scheduled"},
						{Type: v1alpha3.ConditionTypeInitialized, Status: metav1.ConditionFalse, Reason: "WaitingForSync"},
						{Type: v1alpha3.ConditionTypeInQuorum, Status: metav1.ConditionTrue, Reason: "InQuorum"},
						{Type: v1alpha3.ConditionTypeInSync, Status: metav1.ConditionTrue, Reason: "InSync"},
					},
				},
			}
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
					},
				},
			}
			agentPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "agent-node-1",
					Namespace: rv.ControllerConfigMapNamespace,
					Labels:    map[string]string{rvrstatusconditions.AgentPodLabel: rvrstatusconditions.AgentPodValue},
				},
				Spec:   corev1.PodSpec{NodeName: "node-1"},
				Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}},
			}
			clientBuilder = clientBuilder.WithObjects(rvr, node, agentPod)
		})

		It("should set Online=False with reason copied from Initialized condition", func(ctx SpecContext) {
			_, err := rec.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-rvr"},
			})
			Expect(err).ToNot(HaveOccurred())

			rvr := &v1alpha3.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, types.NamespacedName{Name: "test-rvr"}, rvr)).To(Succeed())

			onlineCond := meta.FindStatusCondition(rvr.Status.Conditions, v1alpha3.ConditionTypeOnline)
			Expect(onlineCond.Status).To(Equal(metav1.ConditionFalse))
			// Reason is copied from source Initialized condition
			Expect(onlineCond.Reason).To(Equal("WaitingForSync"))
		})
	})

	When("InQuorum condition is False", func() {
		BeforeEach(func() {
			rvr := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-rvr",
				},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					NodeName: "node-1",
				},
				Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
					Conditions: []metav1.Condition{
						{Type: v1alpha3.ConditionTypeScheduled, Status: metav1.ConditionTrue, Reason: "Scheduled"},
						{Type: v1alpha3.ConditionTypeInitialized, Status: metav1.ConditionTrue, Reason: "Initialized"},
						{Type: v1alpha3.ConditionTypeInQuorum, Status: metav1.ConditionFalse, Reason: "NoQuorum"},
						{Type: v1alpha3.ConditionTypeInSync, Status: metav1.ConditionTrue, Reason: "InSync"},
					},
				},
			}
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
					},
				},
			}
			agentPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "agent-node-1",
					Namespace: rv.ControllerConfigMapNamespace,
					Labels:    map[string]string{rvrstatusconditions.AgentPodLabel: rvrstatusconditions.AgentPodValue},
				},
				Spec:   corev1.PodSpec{NodeName: "node-1"},
				Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}},
			}
			clientBuilder = clientBuilder.WithObjects(rvr, node, agentPod)
		})

		It("should set Online=False with reason copied from InQuorum condition", func(ctx SpecContext) {
			_, err := rec.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-rvr"},
			})
			Expect(err).ToNot(HaveOccurred())

			rvr := &v1alpha3.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, types.NamespacedName{Name: "test-rvr"}, rvr)).To(Succeed())

			onlineCond := meta.FindStatusCondition(rvr.Status.Conditions, v1alpha3.ConditionTypeOnline)
			Expect(onlineCond.Status).To(Equal(metav1.ConditionFalse))
			// Reason is copied from source InQuorum condition
			Expect(onlineCond.Reason).To(Equal("NoQuorum"))
		})
	})

	When("InSync condition is False", func() {
		BeforeEach(func() {
			rvr := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-rvr",
				},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					NodeName: "node-1",
				},
				Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
					Conditions: []metav1.Condition{
						{Type: v1alpha3.ConditionTypeScheduled, Status: metav1.ConditionTrue, Reason: "Scheduled"},
						{Type: v1alpha3.ConditionTypeInitialized, Status: metav1.ConditionTrue, Reason: "Initialized"},
						{Type: v1alpha3.ConditionTypeInQuorum, Status: metav1.ConditionTrue, Reason: "InQuorum"},
						{Type: v1alpha3.ConditionTypeInSync, Status: metav1.ConditionFalse, Reason: "Synchronizing"},
					},
				},
			}
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
					},
				},
			}
			agentPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "agent-node-1",
					Namespace: rv.ControllerConfigMapNamespace,
					Labels:    map[string]string{rvrstatusconditions.AgentPodLabel: rvrstatusconditions.AgentPodValue},
				},
				Spec:   corev1.PodSpec{NodeName: "node-1"},
				Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}},
			}
			clientBuilder = clientBuilder.WithObjects(rvr, node, agentPod)
		})

		It("should set Online=True but IOReady=False with reason Synchronizing", func(ctx SpecContext) {
			_, err := rec.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-rvr"},
			})
			Expect(err).ToNot(HaveOccurred())

			rvr := &v1alpha3.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, types.NamespacedName{Name: "test-rvr"}, rvr)).To(Succeed())

			onlineCond := meta.FindStatusCondition(rvr.Status.Conditions, v1alpha3.ConditionTypeOnline)
			Expect(onlineCond.Status).To(Equal(metav1.ConditionTrue))
			Expect(onlineCond.Reason).To(Equal(v1alpha3.ReasonOnline))

			ioReadyCond := meta.FindStatusCondition(rvr.Status.Conditions, v1alpha3.ConditionTypeIOReady)
			Expect(ioReadyCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(ioReadyCond.Reason).To(Equal(v1alpha3.ReasonSynchronizing))
		})
	})

	When("Agent pod is not ready but node is ready", func() {
		BeforeEach(func() {
			rvr := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-rvr",
				},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					NodeName: "node-1",
				},
				Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
					Conditions: []metav1.Condition{
						{Type: v1alpha3.ConditionTypeScheduled, Status: metav1.ConditionTrue, Reason: "Scheduled"},
						{Type: v1alpha3.ConditionTypeInitialized, Status: metav1.ConditionTrue, Reason: "Initialized"},
						{Type: v1alpha3.ConditionTypeInQuorum, Status: metav1.ConditionTrue, Reason: "InQuorum"},
						{Type: v1alpha3.ConditionTypeInSync, Status: metav1.ConditionTrue, Reason: "InSync"},
					},
				},
			}
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
					},
				},
			}
			// Agent pod is not running
			agentPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "agent-node-1",
					Namespace: rv.ControllerConfigMapNamespace,
					Labels:    map[string]string{rvrstatusconditions.AgentPodLabel: rvrstatusconditions.AgentPodValue},
				},
				Spec:   corev1.PodSpec{NodeName: "node-1"},
				Status: corev1.PodStatus{Phase: corev1.PodPending},
			}
			clientBuilder = clientBuilder.WithObjects(rvr, node, agentPod)
		})

		It("should set Online=False and IOReady=False with reason AgentNotReady", func(ctx SpecContext) {
			_, err := rec.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-rvr"},
			})
			Expect(err).ToNot(HaveOccurred())

			rvr := &v1alpha3.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, types.NamespacedName{Name: "test-rvr"}, rvr)).To(Succeed())

			onlineCond := meta.FindStatusCondition(rvr.Status.Conditions, v1alpha3.ConditionTypeOnline)
			Expect(onlineCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(onlineCond.Reason).To(Equal(v1alpha3.ReasonAgentNotReady))

			ioReadyCond := meta.FindStatusCondition(rvr.Status.Conditions, v1alpha3.ConditionTypeIOReady)
			Expect(ioReadyCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(ioReadyCond.Reason).To(Equal(v1alpha3.ReasonAgentNotReady))
		})
	})

	When("Node is not ready", func() {
		BeforeEach(func() {
			rvr := &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-rvr",
				},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					NodeName: "node-1",
				},
				Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
					Conditions: []metav1.Condition{
						{Type: v1alpha3.ConditionTypeScheduled, Status: metav1.ConditionTrue, Reason: "Scheduled"},
						{Type: v1alpha3.ConditionTypeInitialized, Status: metav1.ConditionTrue, Reason: "Initialized"},
						{Type: v1alpha3.ConditionTypeInQuorum, Status: metav1.ConditionTrue, Reason: "InQuorum"},
						{Type: v1alpha3.ConditionTypeInSync, Status: metav1.ConditionTrue, Reason: "InSync"},
					},
				},
			}
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{Type: corev1.NodeReady, Status: corev1.ConditionFalse},
					},
				},
			}
			clientBuilder = clientBuilder.WithObjects(rvr, node)
		})

		It("should set Online=False and IOReady=False with reason NodeNotReady", func(ctx SpecContext) {
			_, err := rec.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-rvr"},
			})
			Expect(err).ToNot(HaveOccurred())

			rvr := &v1alpha3.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, types.NamespacedName{Name: "test-rvr"}, rvr)).To(Succeed())

			onlineCond := meta.FindStatusCondition(rvr.Status.Conditions, v1alpha3.ConditionTypeOnline)
			Expect(onlineCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(onlineCond.Reason).To(Equal(v1alpha3.ReasonNodeNotReady))

			ioReadyCond := meta.FindStatusCondition(rvr.Status.Conditions, v1alpha3.ConditionTypeIOReady)
			Expect(ioReadyCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(ioReadyCond.Reason).To(Equal(v1alpha3.ReasonNodeNotReady))
		})
	})
})
