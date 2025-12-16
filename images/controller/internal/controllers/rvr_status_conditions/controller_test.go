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
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	rvrstatusconditions "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_status_conditions"
	rv "github.com/deckhouse/sds-replicated-volume/images/controller/internal/reconcile/rv"
)

var _ = Describe("AgentPodToRVRMapper", func() {
	var (
		scheme *runtime.Scheme
		cl     client.WithWatch
		mapper func(ctx context.Context, obj client.Object) []reconcile.Request
	)

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha3.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
	})

	It("should return nil for non-Pod object", func(ctx SpecContext) {
		cl = fake.NewClientBuilder().WithScheme(scheme).Build()
		mapper = rvrstatusconditions.AgentPodToRVRMapper(cl, GinkgoLogr)

		result := mapper(ctx, &v1alpha3.ReplicatedVolumeReplica{})
		Expect(result).To(BeNil())
	})

	It("should return nil for pod in wrong namespace", func(ctx SpecContext) {
		cl = fake.NewClientBuilder().WithScheme(scheme).Build()
		mapper = rvrstatusconditions.AgentPodToRVRMapper(cl, GinkgoLogr)

		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "agent-pod",
				Namespace: "wrong-namespace",
				Labels:    map[string]string{rvrstatusconditions.AgentPodLabel: rvrstatusconditions.AgentPodValue},
			},
			Spec: corev1.PodSpec{NodeName: "node-1"},
		}

		result := mapper(ctx, pod)
		Expect(result).To(BeNil())
	})

	It("should return nil for pod without agent label", func(ctx SpecContext) {
		cl = fake.NewClientBuilder().WithScheme(scheme).Build()
		mapper = rvrstatusconditions.AgentPodToRVRMapper(cl, GinkgoLogr)

		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "some-pod",
				Namespace: rv.ControllerConfigMapNamespace,
				Labels:    map[string]string{"app": "other"},
			},
			Spec: corev1.PodSpec{NodeName: "node-1"},
		}

		result := mapper(ctx, pod)
		Expect(result).To(BeNil())
	})

	It("should return nil for agent pod without NodeName", func(ctx SpecContext) {
		cl = fake.NewClientBuilder().WithScheme(scheme).Build()
		mapper = rvrstatusconditions.AgentPodToRVRMapper(cl, GinkgoLogr)

		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "agent-pod",
				Namespace: rv.ControllerConfigMapNamespace,
				Labels:    map[string]string{rvrstatusconditions.AgentPodLabel: rvrstatusconditions.AgentPodValue},
			},
		}

		result := mapper(ctx, pod)
		Expect(result).To(BeNil())
	})

	It("should return nil when no RVRs on the node", func(ctx SpecContext) {
		rvr := &v1alpha3.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-other-node"},
			Spec:       v1alpha3.ReplicatedVolumeReplicaSpec{NodeName: "node-2"},
		}

		cl = fake.NewClientBuilder().WithScheme(scheme).WithObjects(rvr).Build()
		mapper = rvrstatusconditions.AgentPodToRVRMapper(cl, GinkgoLogr)

		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "agent-pod",
				Namespace: rv.ControllerConfigMapNamespace,
				Labels:    map[string]string{rvrstatusconditions.AgentPodLabel: rvrstatusconditions.AgentPodValue},
			},
			Spec: corev1.PodSpec{NodeName: "node-1"},
		}

		result := mapper(ctx, pod)
		Expect(result).To(BeEmpty())
	})

	It("should return requests for RVRs on the same node", func(ctx SpecContext) {
		rvr1 := &v1alpha3.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec:       v1alpha3.ReplicatedVolumeReplicaSpec{NodeName: "node-1"},
		}
		rvr2 := &v1alpha3.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-2"},
			Spec:       v1alpha3.ReplicatedVolumeReplicaSpec{NodeName: "node-1"},
		}
		rvrOther := &v1alpha3.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-other"},
			Spec:       v1alpha3.ReplicatedVolumeReplicaSpec{NodeName: "node-2"},
		}

		cl = fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(rvr1, rvr2, rvrOther).
			Build()
		mapper = rvrstatusconditions.AgentPodToRVRMapper(cl, GinkgoLogr)

		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "agent-pod",
				Namespace: rv.ControllerConfigMapNamespace,
				Labels:    map[string]string{rvrstatusconditions.AgentPodLabel: rvrstatusconditions.AgentPodValue},
			},
			Spec: corev1.PodSpec{NodeName: "node-1"},
		}

		result := mapper(ctx, pod)
		Expect(result).To(HaveLen(2))
		names := []string{result[0].Name, result[1].Name}
		Expect(names).To(ContainElements("rvr-1", "rvr-2"))
	})
})
