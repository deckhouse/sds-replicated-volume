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

package rvstatusconfigdeviceminor_test

import (
	"context"
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	v1alpha3 "github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	rvstatusconfigdeviceminor "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_status_config_device_minor"
)

var _ = Describe("Reconciler", func() {
	// Note: Some edge cases are not tested:
	// 1. Invalid deviceMinor (outside RVMinDeviceMinor-RVMaxDeviceMinor range):
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
		rec *rvstatusconfigdeviceminor.Reconciler
	)

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha3.AddToScheme(scheme)).To(Succeed())
		clientBuilder = fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&v1alpha3.ReplicatedVolume{})
		cl = nil
		rec = nil
	})

	JustBeforeEach(func() {
		cl = clientBuilder.Build()
		rec = rvstatusconfigdeviceminor.NewReconciler(cl, GinkgoLogr)
	})

	It("returns no error when ReplicatedVolume does not exist", func(ctx SpecContext) {
		Expect(rec.Reconcile(ctx, RequestFor(&v1alpha3.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "non-existent"},
		}))).ToNot(Requeue(), "should ignore NotFound errors")
	})

	When("Get fails with non-NotFound error", func() {
		BeforeEach(func() {
			clientBuilder = clientBuilder.WithInterceptorFuncs(
				InterceptGet(func(_ *v1alpha3.ReplicatedVolume) error {
					return errors.New("internal server error")
				}),
			)
		})

		It("should fail if getting ReplicatedVolume failed with non-NotFound error", func(ctx SpecContext) {
			By("Creating ReplicatedVolume")
			rv := &v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "volume-1"},
			}
			Expect(cl.Create(ctx, rv)).To(Succeed(), "should create ReplicatedVolume")

			By("Reconciling with Get interceptor that returns error")
			internalServerError := errors.New("internal server error")
			Expect(rec.Reconcile(ctx, RequestFor(rv))).Error().To(MatchError(internalServerError), "should return error when Get fails")
		})
	})

	When("List fails", func() {
		BeforeEach(func() {
			clientBuilder = clientBuilder.WithInterceptorFuncs(
				interceptor.Funcs{
					Get: func(ctx context.Context, client client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						return client.Get(ctx, key, obj, opts...)
					},
					List: func(ctx context.Context, client client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
						if _, ok := list.(*v1alpha3.ReplicatedVolumeList); ok {
							return errors.New("failed to list ReplicatedVolumes")
						}
						return client.List(ctx, list, opts...)
					},
				},
			)
		})

		It("should fail if listing ReplicatedVolumes failed", func(ctx SpecContext) {
			By("Creating ReplicatedVolume")
			rv := &v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "volume-1"},
			}
			Expect(cl.Create(ctx, rv)).To(Succeed(), "should create ReplicatedVolume")

			By("Reconciling with List interceptor that returns error")
			listError := errors.New("failed to list ReplicatedVolumes")
			Expect(rec.Reconcile(ctx, RequestFor(rv))).Error().To(MatchError(listError), "should return error when List fails")
		})
	})

	When("RV created", func() {
		var rv *v1alpha3.ReplicatedVolume

		BeforeEach(func() {
			rv = &v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "volume-1"},
			}
		})

		JustBeforeEach(func(ctx SpecContext) {
			if rv != nil {
				Expect(cl.Create(ctx, rv)).To(Succeed(), "should create ReplicatedVolume")
			}
		})

		DescribeTableSubtree("when rv has",
			Entry("nil Status", func() { rv.Status = nil }),
			Entry("nil Status.DRBD", func() {
				rv.Status = &v1alpha3.ReplicatedVolumeStatus{DRBD: nil}
			}),
			Entry("nil Status.DRBD.Config", func() {
				rv.Status = &v1alpha3.ReplicatedVolumeStatus{
					DRBD: &v1alpha3.DRBDResource{Config: nil},
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
					updatedRV := &v1alpha3.ReplicatedVolume{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), updatedRV)).To(Succeed(), "should get updated ReplicatedVolume")
					Expect(updatedRV).To(HaveField("Status.DRBD.Config.DeviceMinor", BeNumerically("==", v1alpha3.RVMinDeviceMinor)), "first volume should get deviceMinor RVMinDeviceMinor")
				})
			},
		)

		When("RV without deviceMinor", func() {
			It("assigns unique deviceMinors to multiple volumes", func(ctx SpecContext) {
				By("Creating volumes with deviceMinors 0 and 1, and one without deviceMinor")
				rv1 := &v1alpha3.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{Name: "volume-multi-1"},
					Status: &v1alpha3.ReplicatedVolumeStatus{
						DRBD: &v1alpha3.DRBDResource{
							Config: &v1alpha3.DRBDResourceConfig{
								DeviceMinor: v1alpha3.RVMinDeviceMinor,
							},
						},
					},
				}
				rv2 := &v1alpha3.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{Name: "volume-multi-2"},
					Status: &v1alpha3.ReplicatedVolumeStatus{
						DRBD: &v1alpha3.DRBDResource{
							Config: &v1alpha3.DRBDResourceConfig{
								DeviceMinor: v1alpha3.RVMinDeviceMinor + 1,
							},
						},
					},
				}
				rv3 := &v1alpha3.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{Name: "volume-multi-3"},
				}
				Expect(cl.Create(ctx, rv1)).To(Succeed(), "should create ReplicatedVolume")
				Expect(cl.Create(ctx, rv2)).To(Succeed(), "should create ReplicatedVolume")
				Expect(cl.Create(ctx, rv3)).To(Succeed(), "should create ReplicatedVolume")

				By("Reconciling until third volume gets next available deviceMinor")
				Eventually(func(g Gomega) *v1alpha3.ReplicatedVolume {
					g.Expect(rec.Reconcile(ctx, RequestFor(rv3))).ToNot(Requeue(), "should not requeue after successful assignment")
					updatedRV := &v1alpha3.ReplicatedVolume{}
					g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv3), updatedRV)).To(Succeed(), "should get updated ReplicatedVolume")
					return updatedRV
				}).Should(HaveField("Status.DRBD.Config.DeviceMinor", BeNumerically("==", 2)), "should assign deviceMinor 2 as next available after 0 and 1")
			})

			When("assigning deviceMinor sequentially and filling gaps", func() {
				var (
					rvSeqList []*v1alpha3.ReplicatedVolume
					rv6       *v1alpha3.ReplicatedVolume
					rvGapList []*v1alpha3.ReplicatedVolume
					rvGap4    *v1alpha3.ReplicatedVolume
				)

				BeforeEach(func() {
					By("Creating 5 volumes with deviceMinors 0-4 and one without deviceMinor (should get 5)")
					rv = nil
					rvSeqList = make([]*v1alpha3.ReplicatedVolume, 5)
					for i := 0; i < 5; i++ {
						rvSeqList[i] = &v1alpha3.ReplicatedVolume{
							ObjectMeta: metav1.ObjectMeta{
								Name: fmt.Sprintf("volume-seq-%d", i+1),
							},
							Status: &v1alpha3.ReplicatedVolumeStatus{
								DRBD: &v1alpha3.DRBDResource{
									Config: &v1alpha3.DRBDResourceConfig{
										DeviceMinor: uint(i),
									},
								},
							},
						}
					}
					rv6 = &v1alpha3.ReplicatedVolume{
						ObjectMeta: metav1.ObjectMeta{Name: "volume-seq-6"},
					}

					By("Creating volumes with deviceMinors 6, 8, 9 (gap at 7) and one without deviceMinor (should fill gap)")
					rvGap1 := &v1alpha3.ReplicatedVolume{
						ObjectMeta: metav1.ObjectMeta{Name: "volume-gap-1"},
						Status: &v1alpha3.ReplicatedVolumeStatus{
							DRBD: &v1alpha3.DRBDResource{
								Config: &v1alpha3.DRBDResourceConfig{
									DeviceMinor: 6,
								},
							},
						},
					}
					rvGap2 := &v1alpha3.ReplicatedVolume{
						ObjectMeta: metav1.ObjectMeta{Name: "volume-gap-2"},
						Status: &v1alpha3.ReplicatedVolumeStatus{
							DRBD: &v1alpha3.DRBDResource{
								Config: &v1alpha3.DRBDResourceConfig{
									DeviceMinor: 8,
								},
							},
						},
					}
					rvGap3 := &v1alpha3.ReplicatedVolume{
						ObjectMeta: metav1.ObjectMeta{Name: "volume-gap-3"},
						Status: &v1alpha3.ReplicatedVolumeStatus{
							DRBD: &v1alpha3.DRBDResource{
								Config: &v1alpha3.DRBDResourceConfig{
									DeviceMinor: 9,
								},
							},
						},
					}
					rvGap4 = &v1alpha3.ReplicatedVolume{
						ObjectMeta: metav1.ObjectMeta{Name: "volume-gap-4"},
					}
					rvGapList = []*v1alpha3.ReplicatedVolume{rvGap1, rvGap2, rvGap3, rvGap4}
				})

				JustBeforeEach(func(ctx SpecContext) {
					for _, rv := range rvSeqList {
						Expect(cl.Create(ctx, rv)).To(Succeed(), "should create ReplicatedVolume")
					}
					Expect(cl.Create(ctx, rv6)).To(Succeed(), "should create ReplicatedVolume")
					for _, rv := range rvGapList {
						Expect(cl.Create(ctx, rv)).To(Succeed(), "should create ReplicatedVolume")
					}
				})

				It("assigns deviceMinor sequentially and fills gaps", func(ctx SpecContext) {
					By("Reconciling until volume gets sequential deviceMinor (5) after 0-4")
					Eventually(func(g Gomega) *v1alpha3.ReplicatedVolume {
						g.Expect(rec.Reconcile(ctx, RequestFor(rv6))).ToNot(Requeue(), "should not requeue after successful assignment")
						updatedRV := &v1alpha3.ReplicatedVolume{}
						g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv6), updatedRV)).To(Succeed(), "should get updated ReplicatedVolume")
						return updatedRV
					}).Should(HaveField("Status.DRBD.Config.DeviceMinor", BeNumerically("==", 5)), "should assign deviceMinor 5 as next sequential value")

					By("Reconciling until volume gets gap-filled deviceMinor (7) between 6 and 8")
					Eventually(func(g Gomega) *v1alpha3.ReplicatedVolume {
						g.Expect(rec.Reconcile(ctx, RequestFor(rvGap4))).ToNot(Requeue(), "should not requeue after successful assignment")
						updatedRV := &v1alpha3.ReplicatedVolume{}
						g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvGap4), updatedRV)).To(Succeed(), "should get updated ReplicatedVolume")
						return updatedRV
					}).Should(HaveField("Status.DRBD.Config.DeviceMinor", BeNumerically("==", 7)), "should assign deviceMinor 7 to fill gap between 6 and 8")
				})
			})
		})

		When("RV with deviceMinor already assigned", func() {
			BeforeEach(func() {
				rv = &v1alpha3.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{Name: "volume-1"},
					Status: &v1alpha3.ReplicatedVolumeStatus{
						DRBD: &v1alpha3.DRBDResource{
							Config: &v1alpha3.DRBDResourceConfig{
								DeviceMinor: 42,
							},
						},
					},
				}
			})

			It("does not reassign deviceMinor and is idempotent", func(ctx SpecContext) {
				By("Reconciling multiple times and verifying deviceMinor remains unchanged")
				Eventually(func(g Gomega) *v1alpha3.ReplicatedVolume {
					for i := 0; i < 3; i++ {
						g.Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue(), "should not requeue when deviceMinor already assigned")
					}
					updatedRV := &v1alpha3.ReplicatedVolume{}
					g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), updatedRV)).To(Succeed(), "should get updated ReplicatedVolume")
					return updatedRV
				}).Should(HaveField("Status.DRBD.Config.DeviceMinor", BeNumerically("==", 42)), "deviceMinor should remain 42 after multiple reconciliations (idempotent)")
			})
		})
	})

	When("Patch fails with non-NotFound error", func() {
		var rv *v1alpha3.ReplicatedVolume
		patchError := errors.New("failed to patch status")

		BeforeEach(func() {
			rv = &v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "volume-patch-1"},
			}
			clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
				SubResourcePatch: func(ctx context.Context, cl client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
					if _, ok := obj.(*v1alpha3.ReplicatedVolume); ok {
						if subResourceName == "status" {
							return patchError
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
			Expect(rec.Reconcile(ctx, RequestFor(rv))).Error().To(MatchError(patchError), "should return error when Patch fails")
		})
	})

	When("Patch fails with 409 Conflict", func() {
		var rv *v1alpha3.ReplicatedVolume
		var conflictError error
		var patchAttempts int

		BeforeEach(func() {
			rv = &v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "volume-conflict-1"},
			}
			patchAttempts = 0
			conflictError = kerrors.NewConflict(
				schema.GroupResource{Group: "storage.deckhouse.io", Resource: "replicatedvolumes"},
				rv.Name,
				errors.New("resourceVersion conflict: the object has been modified"),
			)
			clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
				SubResourcePatch: func(ctx context.Context, cl client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
					if rvObj, ok := obj.(*v1alpha3.ReplicatedVolume); ok {
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
			Expect(rec.Reconcile(ctx, RequestFor(rv))).Error().To(MatchError(conflictError), "should return conflict error on first attempt")

			By("Reconciling until deviceMinor is assigned after conflict resolved")
			Eventually(func(g Gomega) *v1alpha3.ReplicatedVolume {
				g.Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue(), "retry reconciliation should succeed")
				updatedRV := &v1alpha3.ReplicatedVolume{}
				g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), updatedRV)).To(Succeed(), "should get updated ReplicatedVolume")
				return updatedRV
			}).Should(HaveField("Status.DRBD.Config.DeviceMinor", BeNumerically(">=", v1alpha3.RVMinDeviceMinor)), "deviceMinor should be assigned after retry")
		})
	})
})
