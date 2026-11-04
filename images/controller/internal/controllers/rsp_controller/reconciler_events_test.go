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

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// drainEvents returns what the fake recorder has received so far.
func drainEvents(rec *events.FakeRecorder) []string {
	var got []string
	for {
		select {
		case e := <-rec.Events:
			got = append(got, e)
		default:
			return got
		}
	}
}

// The RSP records an Event when its Ready condition moves and only then: a
// pass that repeats the verdict, or only changes the count of eligible nodes in
// its message, must not add to the Events of the pool.
var _ = Describe("Reconciler Events", func() {
	It("records a Warning when Ready turns False and a Normal one when it recovers", func() {
		ctx := context.Background()
		scheme := runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(snc.AddToScheme(scheme)).To(Succeed())

		rsp := &v1alpha1.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: "rsp-1"},
			Spec: v1alpha1.ReplicatedStoragePoolSpec{
				Type:            v1alpha1.ReplicatedStoragePoolTypeLVM,
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{{Name: "lvg-1"}},
				EligibleNodesPolicy: v1alpha1.ReplicatedStoragePoolEligibleNodesPolicy{
					NotReadyGracePeriod: metav1.Duration{Duration: testGracePeriod},
				},
			},
		}
		cl := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(rsp).
			WithStatusSubresource(rsp).
			Build()
		recorder := events.NewFakeRecorder(10)
		r := NewReconciler(cl, logr.Discard(), "test-namespace", nil)
		r.recorder = recorder

		reconcileOnce := func() []string {
			GinkgoHelper()
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKey{Name: "rsp-1"}})
			Expect(err).NotTo(HaveOccurred())
			return drainEvents(recorder)
		}

		By("the LVMVolumeGroup is missing")
		got := reconcileOnce()
		Expect(got).To(HaveLen(1))
		Expect(got[0]).To(HavePrefix("Warning " + v1alpha1.ReplicatedStoragePoolCondReadyReasonLVMVolumeGroupNotFound + " "))

		By("the LVMVolumeGroup is still missing")
		Expect(reconcileOnce()).To(BeEmpty())

		By("the LVMVolumeGroup and its node appear")
		Expect(cl.Create(ctx, &snc.LVMVolumeGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "lvg-1"},
			Spec:       snc.LVMVolumeGroupSpec{Local: snc.LVMVolumeGroupLocalSpec{NodeName: "node-1"}},
			Status: snc.LVMVolumeGroupStatus{
				Conditions: []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue}},
			},
		})).To(Succeed())
		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
			Status: corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
			},
		}
		Expect(cl.Create(ctx, node)).To(Succeed())
		Expect(reconcileOnce()).To(Equal([]string{
			"Normal " + v1alpha1.ReplicatedStoragePoolCondReadyReasonReady + " Eligible nodes calculated successfully: 1 nodes",
		}))

		By("nothing changes")
		Expect(reconcileOnce()).To(BeEmpty())

		By("the node goes away, which changes only the message of Ready")
		Expect(cl.Delete(ctx, node)).To(Succeed())
		Expect(reconcileOnce()).To(BeEmpty())
	})
})
