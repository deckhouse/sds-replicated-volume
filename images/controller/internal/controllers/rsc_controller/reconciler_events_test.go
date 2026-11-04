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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes/testhelpers"
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

// The RSC records an Event when its Ready condition moves and only then: a
// pass that repeats the verdict, or re-words it, must not add to the Events of
// the class, or the one that tells when it broke scrolls away.
var _ = Describe("Reconciler Events", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		Expect(storagev1.AddToScheme(scheme)).To(Succeed())
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(snc.AddToScheme(scheme)).To(Succeed())
	})

	It("records one Event per move of Ready in the status patch", func() {
		ctx := context.Background()
		rsc := &v1alpha1.ReplicatedStorageClass{ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"}}
		cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(rsc).WithStatusSubresource(rsc).Build()
		recorder := events.NewFakeRecorder(10)
		r := NewReconciler(cl)
		r.recorder = recorder

		ready := func(status metav1.ConditionStatus, reason, message string) func(*v1alpha1.ReplicatedStorageClass) bool {
			return func(rsc *v1alpha1.ReplicatedStorageClass) bool {
				return obju.SetStatusCondition(rsc, metav1.Condition{
					Type:    v1alpha1.ReplicatedStorageClassCondReadyType,
					Status:  status,
					Reason:  reason,
					Message: message,
				})
			}
		}

		steps := []struct {
			name   string
			apply  func(*v1alpha1.ReplicatedStorageClass) bool
			events []string
		}{
			{
				"waits for the storage pool",
				ready(metav1.ConditionFalse, v1alpha1.ReplicatedStorageClassCondReadyReasonWaitingForStoragePool, "waiting"),
				[]string{"Warning WaitingForStoragePool waiting"},
			},
			{
				"becomes ready",
				ready(metav1.ConditionTrue, v1alpha1.ReplicatedStorageClassCondReadyReasonReady, "ready"),
				[]string{"Normal Ready ready"},
			},
			{
				"re-words the verdict",
				ready(metav1.ConditionTrue, v1alpha1.ReplicatedStorageClassCondReadyReasonReady, "ready again"),
				nil,
			},
			{
				"degrades",
				ready(metav1.ConditionFalse, v1alpha1.ReplicatedStorageClassCondReadyReasonDegraded, "degraded"),
				[]string{"Warning Degraded degraded"},
			},
			{
				"falls short of eligible nodes, a reason change while still False",
				ready(metav1.ConditionFalse, v1alpha1.ReplicatedStorageClassCondReadyReasonInsufficientEligibleNodes, "short"),
				[]string{"Warning InsufficientEligibleNodes short"},
			},
			{
				"changes another condition only",
				func(rsc *v1alpha1.ReplicatedStorageClass) bool {
					return applyStoragePoolReadyCondFalse(rsc, v1alpha1.ReplicatedStorageClassCondStoragePoolReadyReasonPending, "pending")
				},
				nil,
			},
		}

		for _, s := range steps {
			By(s.name)
			cur := &v1alpha1.ReplicatedStorageClass{}
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(rsc), cur)).To(Succeed())
			base := cur.DeepCopy()
			Expect(s.apply(cur)).To(BeTrue())
			Expect(r.patchRSCStatus(ctx, cur, base)).To(Succeed())
			Expect(drainEvents(recorder)).To(Equal(s.events))
		}
	})

	It("records nothing when the status patch fails", func() {
		ctx := context.Background()
		rsc := &v1alpha1.ReplicatedStorageClass{ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"}}
		cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(rsc).WithStatusSubresource(rsc).Build()
		recorder := events.NewFakeRecorder(10)
		r := NewReconciler(cl)
		r.recorder = recorder

		stale := &v1alpha1.ReplicatedStorageClass{}
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rsc), stale)).To(Succeed())

		// Someone else writes first, so the optimistic lock of the patch fails.
		other := stale.DeepCopy()
		other.Labels = map[string]string{"touched": "true"}
		Expect(cl.Update(ctx, other)).To(Succeed())

		base := stale.DeepCopy()
		applyReadyCondFalse(stale, v1alpha1.ReplicatedStorageClassCondReadyReasonInvalidConfiguration, "invalid")
		Expect(r.patchRSCStatus(ctx, stale, base)).NotTo(Succeed())
		Expect(drainEvents(recorder)).To(BeEmpty())
	})

	It("records the reconcile verdict once across repeated passes", func() {
		ctx := context.Background()
		rsc := &v1alpha1.ReplicatedStorageClass{ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"}}
		cl := testhelpers.WithRSPByUsedByRSCNameIndex(testhelpers.WithRVByReplicatedStorageClassNameIndex(fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(rsc).
			WithStatusSubresource(rsc, &v1alpha1.ReplicatedStoragePool{}))).
			Build()
		recorder := events.NewFakeRecorder(10)
		r := NewReconciler(cl)
		r.recorder = recorder

		for i, want := range [][]string{{"Warning InvalidConfiguration spec.storage is required"}, nil} {
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKey{Name: "rsc-1"}})
			Expect(err).NotTo(HaveOccurred())
			Expect(drainEvents(recorder)).To(Equal(want), "pass %d", i+1)
		}
	})
})
