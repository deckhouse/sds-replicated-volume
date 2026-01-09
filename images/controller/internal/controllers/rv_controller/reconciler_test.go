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

package rvcontroller_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	u "github.com/deckhouse/sds-common-lib/utils"
	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	rvcontroller "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/idpool"
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

func expectDeviceMinorAssignedTrue(g Gomega, rv *v1alpha1.ReplicatedVolume) {
	cond := apimeta.FindStatusCondition(rv.Status.Conditions, v1alpha1.ReplicatedVolumeCondDeviceMinorAssignedType)
	g.Expect(cond).NotTo(BeNil(), "DeviceMinorAssigned condition must exist")
	g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
	g.Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeCondDeviceMinorAssignedReasonAssigned))
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

// testPoolSource is a simple test implementation of DeviceMinorPoolSource
// that returns a pre-initialized pool immediately without blocking.
type testPoolSource struct {
	pool *idpool.IDPool[v1alpha1.DeviceMinor]
}

func newTestPoolSource(pool *idpool.IDPool[v1alpha1.DeviceMinor]) *testPoolSource {
	return &testPoolSource{pool: pool}
}

func (s *testPoolSource) DeviceMinorPool(_ context.Context) (*idpool.IDPool[v1alpha1.DeviceMinor], error) {
	return s.pool, nil
}

func (s *testPoolSource) DeviceMinorPoolOrNil() *idpool.IDPool[v1alpha1.DeviceMinor] {
	return s.pool
}

// initReconcilerFromClient creates a new reconciler with pool initialized from existing volumes in the client.
// This simulates the production behavior where pool is initialized at controller startup.
func initReconcilerFromClient(ctx context.Context, cl client.Client) *rvcontroller.Reconciler {
	pool := idpool.NewIDPool[v1alpha1.DeviceMinor]()

	rvList := &v1alpha1.ReplicatedVolumeList{}
	ExpectWithOffset(1, cl.List(ctx, rvList)).To(Succeed(), "should list ReplicatedVolumes")

	pairs := make([]idpool.IDNamePair[v1alpha1.DeviceMinor], 0, len(rvList.Items))
	for i := range rvList.Items {
		rv := &rvList.Items[i]
		if rv.Status.DeviceMinor != nil {
			pairs = append(pairs, idpool.IDNamePair[v1alpha1.DeviceMinor]{
				Name: rv.Name,
				ID:   *rv.Status.DeviceMinor,
			})
		}
	}

	errs := pool.Fill(pairs)
	for i, err := range errs {
		ExpectWithOffset(1, err).To(Succeed(), "should initialize pool from existing rv deviceMinor values (pair index=%d)", i)
	}

	return rvcontroller.NewReconciler(cl, newTestPoolSource(pool))
}

var _ = Describe("Reconciler", func() {
	// Note: Some edge cases are not tested:
	// 1. Invalid deviceMinor (outside DeviceMinor.Min()-DeviceMinor.Max() range):
	//    - Not needed: API validates values, invalid deviceMinor never reaches controller
	//    - System limits ensure only valid values exist in real system
	// 2. All deviceMinors used (1,048,576 objects):
	//    - Not needed: Would require creating 1,048,576 test objects, too slow and impractical
	//    - Extremely unlikely in real system, not worth the test complexity
	// Current coverage (85.4%) covers all practical scenarios: happy path, sequential assignment,
	// gap filling, idempotency, error handling (Get/List), and nil status combinations.

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
		// Use a test pool source that returns an empty pool immediately.
		rec = rvcontroller.NewReconciler(
			cl,
			newTestPoolSource(idpool.NewIDPool[v1alpha1.DeviceMinor]()),
		)
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
				localRec := rvcontroller.NewReconciler(
					localCl,
					newTestPoolSource(idpool.NewIDPool[v1alpha1.DeviceMinor]()),
				)

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

		DescribeTableSubtree("when rv has",
			Entry("empty Status", func() { rv.Status = v1alpha1.ReplicatedVolumeStatus{} }),
			Entry("nil Status.DRBD", func() {
				rv.Status = v1alpha1.ReplicatedVolumeStatus{DRBD: nil}
			}),
			Entry("nil Status.DRBD.Config", func() {
				rv.Status = v1alpha1.ReplicatedVolumeStatus{
					DRBD: &v1alpha1.DRBDResource{Config: nil},
				}
			}),
			func(setup func()) {
				BeforeEach(func() {
					setup()
				})

				It("assigns deviceMinor successfully", func(ctx SpecContext) {
					By("Reconciling ReplicatedVolume with nil status fields")
					result, err := rec.Reconcile(ctx, RequestFor(rv))
					Expect(err).NotTo(HaveOccurred(), "reconciliation should succeed")
					Expect(result).ToNot(Requeue(), "should not requeue after successful assignment")

					By("Verifying deviceMinor was assigned")
					updatedRV := &v1alpha1.ReplicatedVolume{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), updatedRV)).To(Succeed(), "should get updated ReplicatedVolume")
					Expect(updatedRV).To(HaveField("Status.DeviceMinor", PointTo(BeNumerically("==", v1alpha1.DeviceMinor(0).Min()))), "first volume should get minimal deviceMinor")
					expectDeviceMinorAssignedTrue(Default, updatedRV)
				})
			},
		)

		When("RV without deviceMinor", func() {
			When("assigning deviceMinor sequentially and filling gaps", func() {
				var (
					rvSeqList []*v1alpha1.ReplicatedVolume
					rv6       *v1alpha1.ReplicatedVolume
					rvGapList []*v1alpha1.ReplicatedVolume
					rvGap4    *v1alpha1.ReplicatedVolume
				)

				BeforeEach(func() {
					rv = nil
					rvSeqList = make([]*v1alpha1.ReplicatedVolume, 5)
					for i := 0; i < 5; i++ {
						rvSeqList[i] = &v1alpha1.ReplicatedVolume{
							ObjectMeta: metav1.ObjectMeta{
								Name: fmt.Sprintf("volume-seq-%d", i+1),
							},
							Status: v1alpha1.ReplicatedVolumeStatus{
								DeviceMinor: u.Ptr(v1alpha1.DeviceMinor(i)),
							},
						}
					}
					rv6 = &v1alpha1.ReplicatedVolume{
						ObjectMeta: metav1.ObjectMeta{
							Name: "volume-seq-6",
						},
					}

					rvGap1 := &v1alpha1.ReplicatedVolume{
						ObjectMeta: metav1.ObjectMeta{
							Name: "volume-gap-1",
						},
						Status: v1alpha1.ReplicatedVolumeStatus{
							DeviceMinor: u.Ptr(v1alpha1.DeviceMinor(6)),
						},
					}
					rvGap2 := &v1alpha1.ReplicatedVolume{
						ObjectMeta: metav1.ObjectMeta{
							Name: "volume-gap-2",
						},
						Status: v1alpha1.ReplicatedVolumeStatus{
							DeviceMinor: u.Ptr(v1alpha1.DeviceMinor(8)),
						},
					}
					rvGap3 := &v1alpha1.ReplicatedVolume{
						ObjectMeta: metav1.ObjectMeta{
							Name: "volume-gap-3",
						},
						Status: v1alpha1.ReplicatedVolumeStatus{
							DeviceMinor: u.Ptr(v1alpha1.DeviceMinor(9)),
						},
					}
					rvGap4 = &v1alpha1.ReplicatedVolume{
						ObjectMeta: metav1.ObjectMeta{
							Name: "volume-gap-4",
						},
					}
					rvGapList = []*v1alpha1.ReplicatedVolume{rvGap1, rvGap2, rvGap3, rvGap4}
				})

				JustBeforeEach(func(ctx SpecContext) {
					for _, rv := range rvSeqList {
						Expect(cl.Create(ctx, rv)).To(Succeed(), "should create ReplicatedVolume")
					}
					Expect(cl.Create(ctx, rv6)).To(Succeed(), "should create ReplicatedVolume")
					for _, rv := range rvGapList {
						Expect(cl.Create(ctx, rv)).To(Succeed(), "should create ReplicatedVolume")
					}
					// Reinitialize reconciler with cache populated from existing volumes
					rec = initReconcilerFromClient(ctx, cl)
				})

				It("assigns deviceMinor sequentially and fills gaps", func(ctx SpecContext) {
					By("Reconciling until volume gets sequential deviceMinor (5) after 0-4")
					Eventually(func(g Gomega) *v1alpha1.ReplicatedVolume {
						g.Expect(rec.Reconcile(ctx, RequestFor(rv6))).ToNot(Requeue(), "should not requeue after successful assignment")
						updatedRV := &v1alpha1.ReplicatedVolume{}
						g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv6), updatedRV)).To(Succeed(), "should get updated ReplicatedVolume")
						expectDeviceMinorAssignedTrue(g, updatedRV)
						return updatedRV
					}).Should(HaveField("Status.DeviceMinor", PointTo(BeNumerically("==", 5))), "should assign deviceMinor 5 as next sequential value")

					By("Reconciling until volume gets gap-filled deviceMinor (7) between 6 and 8")
					Eventually(func(g Gomega) *v1alpha1.ReplicatedVolume {
						g.Expect(rec.Reconcile(ctx, RequestFor(rvGap4))).ToNot(Requeue(), "should not requeue after successful assignment")
						updatedRV := &v1alpha1.ReplicatedVolume{}
						g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvGap4), updatedRV)).To(Succeed(), "should get updated ReplicatedVolume")
						expectDeviceMinorAssignedTrue(g, updatedRV)
						return updatedRV
					}).Should(HaveField("Status.DeviceMinor", PointTo(BeNumerically("==", 7))), "should assign deviceMinor 7 to fill gap between 6 and 8")
				})
			})
		})

		When("RV with deviceMinor already assigned", func() {
			BeforeEach(func() {
				rv = &v1alpha1.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{Name: "volume-1"},
					Status: v1alpha1.ReplicatedVolumeStatus{
						DeviceMinor: u.Ptr(v1alpha1.DeviceMinor(42)),
					},
				}
			})

			It("does not reassign deviceMinor and is idempotent", func(ctx SpecContext) {
				// Reinitialize reconciler with cache populated from existing volumes
				rec = initReconcilerFromClient(ctx, cl)
				By("Reconciling multiple times and verifying deviceMinor remains unchanged")
				Eventually(func(g Gomega) *v1alpha1.ReplicatedVolume {
					for i := 0; i < 3; i++ {
						g.Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue(), "should not requeue when deviceMinor already assigned")
					}
					updatedRV := &v1alpha1.ReplicatedVolume{}
					g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), updatedRV)).To(Succeed(), "should get updated ReplicatedVolume")
					expectDeviceMinorAssignedTrue(g, updatedRV)
					return updatedRV
				}).Should(HaveField("Status.DeviceMinor", PointTo(BeNumerically("==", 42))), "deviceMinor should remain 42 after multiple reconciliations (idempotent)")
			})
		})
	})

	When("RV has DRBD.Config without explicit deviceMinor and 0 is already used", func() {
		var (
			rvExisting *v1alpha1.ReplicatedVolume
			rvNew      *v1alpha1.ReplicatedVolume
		)

		BeforeEach(func() {
			// Existing volume that already uses deviceMinor = DeviceMinor.Min() (0)
			rvExisting = &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "volume-zero-used"},
				Status: v1alpha1.ReplicatedVolumeStatus{
					DeviceMinor: u.Ptr(v1alpha1.DeviceMinor(v1alpha1.DeviceMinor(0).Min())), // 0
				},
			}

			// New volume: DRBD.Config is already initialized, but DeviceMinor was never set explicitly
			// (the pointer stays nil and the field is not present in the JSON). We expect the controller
			// to treat this as "minor is not assigned yet" and pick the next free value (1), instead of
			// reusing 0 which is already taken by another volume.
			rvNew = &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "volume-config-no-minor",
				},
				Status: v1alpha1.ReplicatedVolumeStatus{
					DRBD: &v1alpha1.DRBDResource{
						Config: &v1alpha1.DRBDResourceConfig{
							SharedSecret:    "test-secret",
							SharedSecretAlg: "alg",
							// DeviceMinor is not set here – the pointer remains nil and the field is not present in JSON.
						},
					},
				},
			}
		})

		JustBeforeEach(func(ctx SpecContext) {
			Expect(cl.Create(ctx, rvExisting)).To(Succeed(), "should create existing ReplicatedVolume")
			Expect(cl.Create(ctx, rvNew)).To(Succeed(), "should create new ReplicatedVolume")
			// Reinitialize reconciler with cache populated from existing volumes
			rec = initReconcilerFromClient(ctx, cl)
		})

		It("treats zero-value deviceMinor as unassigned and picks next free value", func(ctx SpecContext) {
			By("Reconciling the RV with DRBD.Config but zero-value deviceMinor")
			result, err := rec.Reconcile(ctx, RequestFor(rvNew))
			Expect(err).NotTo(HaveOccurred(), "reconciliation should succeed")
			Expect(result).ToNot(Requeue(), "should not requeue after successful assignment")

			By("Verifying next free deviceMinor was assigned (DeviceMinor.Min() + 1)")
			updated := &v1alpha1.ReplicatedVolume{}
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvNew), updated)).To(Succeed(), "should get updated ReplicatedVolume")

			Expect(updated).To(HaveField("Status.DeviceMinor",
				PointTo(BeNumerically("==", v1alpha1.DeviceMinor(0).Min()+1))),
				"new volume should get the next free deviceMinor, since 0 is already used",
			)
			expectDeviceMinorAssignedTrue(Default, updated)
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
			}
			testError = errors.New("failed to patch status")
			clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
				SubResourcePatch: func(ctx context.Context, cl client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
					if _, ok := obj.(*v1alpha1.ReplicatedVolume); ok {
						if subResourceName == "status" {
							return testError
						}
					}
					return cl.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
				},
			})
		})

		JustBeforeEach(func(ctx SpecContext) {
			Expect(cl.Create(ctx, rv)).To(Succeed(), "should create ReplicatedVolume")
		})

		It("should fail if patching ReplicatedVolume status failed with non-NotFound error", func(ctx SpecContext) {
			_, err := rec.Reconcile(ctx, RequestFor(rv))
			Expect(err).To(HaveOccurred(), "should return error when Patch fails")
			Expect(errors.Is(err, testError)).To(BeTrue(), "returned error should wrap the original Patch error")
		})
	})

	When("Patch fails with 409 Conflict", func() {
		var rv *v1alpha1.ReplicatedVolume
		var conflictError error
		var patchAttempts int

		BeforeEach(func() {
			rv = &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "volume-conflict-1",
				},
			}
			patchAttempts = 0
			conflictError = kerrors.NewConflict(
				schema.GroupResource{Group: "storage.deckhouse.io", Resource: "replicatedvolumes"},
				rv.Name,
				errors.New("resourceVersion conflict: the object has been modified"),
			)
			clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
				SubResourcePatch: func(ctx context.Context, cl client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
					if rvObj, ok := obj.(*v1alpha1.ReplicatedVolume); ok {
						if subResourceName == "status" && rvObj.Name == rv.Name {
							patchAttempts++
							if patchAttempts == 1 {
								return conflictError
							}
						}
					}
					return cl.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
				},
			})
		})

		JustBeforeEach(func(ctx SpecContext) {
			Expect(cl.Create(ctx, rv)).To(Succeed(), "should create ReplicatedVolume")
		})

		It("should return error on 409 Conflict and succeed on retry", func(ctx SpecContext) {
			By("First reconcile: should fail with 409 Conflict")
			_, err := rec.Reconcile(ctx, RequestFor(rv))
			Expect(err).To(HaveOccurred(), "should return conflict error on first attempt")
			Expect(kerrors.IsConflict(err)).To(BeTrue(), "should return 409 Conflict on first attempt")

			By("Reconciling until deviceMinor is assigned after conflict resolved")
			Eventually(func(g Gomega) *v1alpha1.ReplicatedVolume {
				g.Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue(), "retry reconciliation should succeed")
				updatedRV := &v1alpha1.ReplicatedVolume{}
				g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), updatedRV)).To(Succeed(), "should get updated ReplicatedVolume")
				return updatedRV
			}).Should(HaveField("Status.DeviceMinor", PointTo(BeNumerically(">=", v1alpha1.DeviceMinor(0).Min()))), "deviceMinor should be assigned after retry")
		})
	})
})
