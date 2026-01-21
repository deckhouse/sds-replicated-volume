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

package rvcontroller_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	rvcontroller "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller"
)

func TestRvControllerReconciler(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "rv_controller Reconciler Suite")
}

func RequestFor(object client.Object) reconcile.Request {
	return reconcile.Request{NamespacedName: client.ObjectKeyFromObject(object)}
}

func Requeue() OmegaMatcher {
	return Not(Equal(reconcile.Result{}))
}

func InterceptGet[T client.Object](intercept func(T) error) interceptor.Funcs {
	var zero T
	tType := reflect.TypeOf(zero)
	if tType == nil {
		panic("cannot determine type")
	}

	return interceptor.Funcs{
		Get: func(ctx context.Context, client client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if reflect.TypeOf(obj).AssignableTo(tType) {
				return intercept(obj.(T))
			}
			return client.Get(ctx, key, obj, opts...)
		},
		List: func(ctx context.Context, client client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if reflect.TypeOf(list).Elem().Elem().AssignableTo(tType) {
				items := reflect.ValueOf(list).Elem().FieldByName("Items")
				if items.IsValid() && items.Kind() == reflect.Slice {
					for i := 0; i < items.Len(); i++ {
						item := items.Index(i).Addr().Interface().(T)
						if err := intercept(item); err != nil {
							return err
						}
					}
				}
			}
			return client.List(ctx, list, opts...)
		},
	}
}

var _ = Describe("Reconciler", func() {
	var (
		clientBuilder *fake.ClientBuilder
		scheme        *runtime.Scheme
	)
	var (
		cl  client.WithWatch
		rec *rvcontroller.Reconciler
	)

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		clientBuilder = fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&v1alpha1.ReplicatedVolume{})
		cl = nil
		rec = nil
	})

	JustBeforeEach(func() {
		cl = clientBuilder.Build()
		rec = rvcontroller.NewReconciler(cl, nil)
	})

	Describe("Reconcile (metadata)", func() {
		type tc struct {
			name       string
			objects    []client.Object
			reqName    string
			wantLabels map[string]string
		}

		DescribeTable(
			"updates labels",
			func(ctx SpecContext, tt tc) {
				localCl := fake.NewClientBuilder().
					WithScheme(scheme).
					WithStatusSubresource(&v1alpha1.ReplicatedVolume{}).
					WithObjects(tt.objects...).
					Build()
				localRec := rvcontroller.NewReconciler(localCl, nil)

				_, err := localRec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKey{Name: tt.reqName}})
				Expect(err).NotTo(HaveOccurred())

				rv := &v1alpha1.ReplicatedVolume{}
				Expect(localCl.Get(ctx, client.ObjectKey{Name: tt.reqName}, rv)).To(Succeed())

				for k, want := range tt.wantLabels {
					Expect(rv.Labels).To(HaveKeyWithValue(k, want))
				}
			},
			Entry("adds label when rsc specified", tc{
				name: "adds label when rsc specified",
				objects: []client.Object{
					&v1alpha1.ReplicatedVolume{
						ObjectMeta: metav1.ObjectMeta{Name: "rv-with-rsc", ResourceVersion: "1"},
						Spec:       v1alpha1.ReplicatedVolumeSpec{ReplicatedStorageClassName: "my-storage-class"},
					},
				},
				reqName: "rv-with-rsc",
				wantLabels: map[string]string{
					v1alpha1.ReplicatedStorageClassLabelKey: "my-storage-class",
				},
			}),
			Entry("does not change label if already set correctly", tc{
				name: "does not change label if already set correctly",
				objects: []client.Object{
					&v1alpha1.ReplicatedVolume{
						ObjectMeta: metav1.ObjectMeta{
							Name:            "rv-with-label",
							ResourceVersion: "1",
							Labels: map[string]string{
								v1alpha1.ReplicatedStorageClassLabelKey: "existing-class",
							},
						},
						Spec: v1alpha1.ReplicatedVolumeSpec{
							ReplicatedStorageClassName: "existing-class",
						},
					},
				},
				reqName: "rv-with-label",
				wantLabels: map[string]string{
					v1alpha1.ReplicatedStorageClassLabelKey: "existing-class",
				},
			}),
		)
	})

	It("returns no error when ReplicatedVolume does not exist", func(ctx SpecContext) {
		Expect(rec.Reconcile(ctx, RequestFor(&v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "non-existent"},
		}))).ToNot(Requeue(), "should ignore NotFound errors")
	})

	When("RV created", func() {
		var rv *v1alpha1.ReplicatedVolume

		BeforeEach(func() {
			rv = &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "volume-1",
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("1Gi"),
					ReplicatedStorageClassName: "my-storage-class",
				},
			}
		})

		JustBeforeEach(func(ctx SpecContext) {
			if rv != nil {
				Expect(cl.Create(ctx, rv)).To(Succeed(), "should create ReplicatedVolume")
			}
		})

		When("Get fails with non-NotFound error", func() {
			var testError error

			BeforeEach(func() {
				testError = errors.New("internal server error")
				clientBuilder = clientBuilder.WithInterceptorFuncs(
					InterceptGet(func(_ *v1alpha1.ReplicatedVolume) error {
						return testError
					}),
				)
			})

			It("should fail if getting ReplicatedVolume failed with non-NotFound error", func(ctx SpecContext) {
				_, err := rec.Reconcile(ctx, RequestFor(rv))
				Expect(err).To(HaveOccurred(), "should return error when Get fails")
				Expect(errors.Is(err, testError)).To(BeTrue(), "returned error should wrap the original Get error")
			})
		})

		It("sets label on RV", func(ctx SpecContext) {
			By("Reconciling ReplicatedVolume")
			result, err := rec.Reconcile(ctx, RequestFor(rv))
			Expect(err).NotTo(HaveOccurred(), "reconciliation should succeed")
			Expect(result).ToNot(Requeue(), "should not requeue after successful reconciliation")

			By("Verifying label was set")
			updatedRV := &v1alpha1.ReplicatedVolume{}
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), updatedRV)).To(Succeed(), "should get updated ReplicatedVolume")
			Expect(updatedRV.Labels).To(HaveKeyWithValue(v1alpha1.ReplicatedStorageClassLabelKey, "my-storage-class"))
		})

		When("label already set correctly", func() {
			BeforeEach(func() {
				rv.Labels = map[string]string{
					v1alpha1.ReplicatedStorageClassLabelKey: "my-storage-class",
				}
			})

			It("is idempotent and does not modify RV", func(ctx SpecContext) {
				By("Reconciling multiple times")
				for i := 0; i < 3; i++ {
					result, err := rec.Reconcile(ctx, RequestFor(rv))
					Expect(err).NotTo(HaveOccurred(), "reconciliation should succeed")
					Expect(result).ToNot(Requeue(), "should not requeue")
				}

				By("Verifying label remains unchanged")
				updatedRV := &v1alpha1.ReplicatedVolume{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), updatedRV)).To(Succeed())
				Expect(updatedRV.Labels).To(HaveKeyWithValue(v1alpha1.ReplicatedStorageClassLabelKey, "my-storage-class"))
			})
		})
	})

	When("Patch fails with non-NotFound error", func() {
		var rv *v1alpha1.ReplicatedVolume
		var testError error

		BeforeEach(func() {
			rv = &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "volume-patch-1",
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					ReplicatedStorageClassName: "my-storage-class",
				},
			}
			testError = errors.New("failed to patch")
			clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
				Patch: func(ctx context.Context, cl client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
					if _, ok := obj.(*v1alpha1.ReplicatedVolume); ok {
						return testError
					}
					return cl.Patch(ctx, obj, patch, opts...)
				},
			})
		})

		JustBeforeEach(func(ctx SpecContext) {
			Expect(cl.Create(ctx, rv)).To(Succeed(), "should create ReplicatedVolume")
		})

		It("should fail if patching ReplicatedVolume failed with non-NotFound error", func(ctx SpecContext) {
			_, err := rec.Reconcile(ctx, RequestFor(rv))
			Expect(err).To(HaveOccurred(), "should return error when Patch fails")
			Expect(errors.Is(err, testError)).To(BeTrue(), "returned error should wrap the original Patch error")
		})
	})
})
