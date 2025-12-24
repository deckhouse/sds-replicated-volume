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

	u "github.com/deckhouse/sds-common-lib/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
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
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		clientBuilder = fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&v1alpha1.ReplicatedVolume{})
		cl = nil
		rec = nil
	})

	JustBeforeEach(func() {
		cl = clientBuilder.Build()
		rec = rvstatusconfigdeviceminor.NewReconciler(cl, GinkgoLogr)
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
					Name:       "volume-1",
					Finalizers: []string{v1alpha1.ControllerAppFinalizer},
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
				Expect(rec.Reconcile(ctx, RequestFor(rv))).Error().To(MatchError(testError), "should return error when Get fails")
			})
		})

		When("List fails", func() {
			var testError error

			BeforeEach(func() {
				testError = errors.New("failed to list ReplicatedVolumes")
				clientBuilder = clientBuilder.WithInterceptorFuncs(
					interceptor.Funcs{
						Get: func(ctx context.Context, client client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
							return client.Get(ctx, key, obj, opts...)
						},
						List: func(ctx context.Context, client client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
							if _, ok := list.(*v1alpha1.ReplicatedVolumeList); ok {
								return testError
							}
							return client.List(ctx, list, opts...)
						},
					},
				)
			})

			It("should fail if listing ReplicatedVolumes failed", func(ctx SpecContext) {
				Expect(rec.Reconcile(ctx, RequestFor(rv))).Error().To(MatchError(testError), "should return error when List fails")
			})
		})

		DescribeTableSubtree("when rv has",
			Entry("nil Status", func() { rv.Status = nil }),
			Entry("nil Status.DRBD", func() {
				rv.Status = &v1alpha1.ReplicatedVolumeStatus{DRBD: nil}
			}),
			Entry("nil Status.DRBD.Config", func() {
				rv.Status = &v1alpha1.ReplicatedVolumeStatus{
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
					Expect(updatedRV).To(HaveField("Status.DRBD.Config.DeviceMinor", PointTo(BeNumerically("==", v1alpha1.RVMinDeviceMinor))), "first volume should get deviceMinor RVMinDeviceMinor")
				})
			},
		)

		When("RV without deviceMinor", func() {
			It("detects duplicates and sets/clears error messages", func(ctx SpecContext) {
				By("Creating volumes with duplicate deviceMinors")
				// Group A: 2 volumes with deviceMinor=0 (duplicate)
				rvA1 := &v1alpha1.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name: "volume-dup-a1",
					},
					Status: &v1alpha1.ReplicatedVolumeStatus{
						DRBD: &v1alpha1.DRBDResource{
							Config: &v1alpha1.DRBDResourceConfig{
								DeviceMinor: u.Ptr(v1alpha1.RVMinDeviceMinor),
							},
						},
					},
				}
				rvA2 := &v1alpha1.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "volume-dup-a2",
						Finalizers: []string{v1alpha1.ControllerAppFinalizer},
					},
					Status: &v1alpha1.ReplicatedVolumeStatus{
						DRBD: &v1alpha1.DRBDResource{
							Config: &v1alpha1.DRBDResourceConfig{
								DeviceMinor: u.Ptr(v1alpha1.RVMinDeviceMinor),
							},
						},
					},
				}
				// Group B: 3 volumes with deviceMinor=1 (duplicate)
				rvB1 := &v1alpha1.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name: "volume-dup-b1",
					},
					Status: &v1alpha1.ReplicatedVolumeStatus{
						DRBD: &v1alpha1.DRBDResource{
							Config: &v1alpha1.DRBDResourceConfig{
								DeviceMinor: u.Ptr(v1alpha1.RVMinDeviceMinor + 1),
							},
						},
					},
				}
				rvB2 := &v1alpha1.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name: "volume-dup-b2",
					},
					Status: &v1alpha1.ReplicatedVolumeStatus{
						DRBD: &v1alpha1.DRBDResource{
							Config: &v1alpha1.DRBDResourceConfig{
								DeviceMinor: u.Ptr(v1alpha1.RVMinDeviceMinor + 1),
							},
						},
					},
				}
				rvB3 := &v1alpha1.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "volume-dup-b3",
						Finalizers: []string{v1alpha1.ControllerAppFinalizer},
					},
					Status: &v1alpha1.ReplicatedVolumeStatus{
						DRBD: &v1alpha1.DRBDResource{
							Config: &v1alpha1.DRBDResourceConfig{
								DeviceMinor: u.Ptr(v1alpha1.RVMinDeviceMinor + 1),
							},
						},
					},
				}
				// Group C: 1 volume with deviceMinor=2 (no duplicate)
				rvC1 := &v1alpha1.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "volume-dup-c1",
						Finalizers: []string{v1alpha1.ControllerAppFinalizer},
					},
					Status: &v1alpha1.ReplicatedVolumeStatus{
						DRBD: &v1alpha1.DRBDResource{
							Config: &v1alpha1.DRBDResourceConfig{
								DeviceMinor: u.Ptr(v1alpha1.RVMinDeviceMinor + 2),
							},
						},
					},
				}
				// Volume without deviceMinor
				rvD1 := &v1alpha1.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "volume-dup-d1",
						Finalizers: []string{v1alpha1.ControllerAppFinalizer},
					},
				}

				for _, rv := range []*v1alpha1.ReplicatedVolume{rvA1, rvA2, rvB1, rvB2, rvB3, rvC1, rvD1} {
					Expect(cl.Create(ctx, rv)).To(Succeed(), fmt.Sprintf("should create ReplicatedVolume %s", rv.Name))
				}

				By("Reconciling D1 to assign deviceMinor and trigger duplicate detection")
				Eventually(func(g Gomega) *v1alpha1.ReplicatedVolume {
					g.Expect(rec.Reconcile(ctx, RequestFor(rvD1))).ToNot(Requeue(), "should not requeue after successful assignment")
					updatedRV := &v1alpha1.ReplicatedVolume{}
					g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvD1), updatedRV)).To(Succeed(), "should get updated ReplicatedVolume")
					return updatedRV
				}).Should(HaveField("Status.DRBD.Config.DeviceMinor", PointTo(BeNumerically("==", v1alpha1.RVMinDeviceMinor+3))), "should assign deviceMinor 3 to D1")

				// Reconcile any volume to trigger duplicate detection
				Expect(rec.Reconcile(ctx, RequestFor(rvA1))).ToNot(Requeue(), "should trigger duplicate detection")

				By("Verifying error messages are set for duplicate volumes")
				Eventually(func(g Gomega) {
					// Check A1 and A2 have duplicate error
					updatedA1 := &v1alpha1.ReplicatedVolume{}
					g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvA1), updatedA1)).To(Succeed())
					g.Expect(updatedA1).To(HaveField("Status.Errors.DuplicateDeviceMinor.Message",
						SatisfyAll(
							ContainSubstring("deviceMinor"),
							ContainSubstring("0"),
							ContainSubstring("is used by volumes:"),
							ContainSubstring("volume-dup-a1"),
							ContainSubstring("volume-dup-a2"),
						),
					), "A1 should have duplicate error message")

					updatedA2 := &v1alpha1.ReplicatedVolume{}
					g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvA2), updatedA2)).To(Succeed())
					g.Expect(updatedA2).To(HaveField("Status.Errors.DuplicateDeviceMinor.Message",
						SatisfyAll(
							ContainSubstring("deviceMinor"),
							ContainSubstring("0"),
							ContainSubstring("is used by volumes:"),
							ContainSubstring("volume-dup-a1"),
							ContainSubstring("volume-dup-a2"),
						),
					), "A2 should have duplicate error message")

					// Check B1, B2, B3 have duplicate error
					updatedB1 := &v1alpha1.ReplicatedVolume{}
					g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvB1), updatedB1)).To(Succeed())
					g.Expect(updatedB1).To(HaveField("Status.Errors.DuplicateDeviceMinor.Message",
						SatisfyAll(
							ContainSubstring("deviceMinor"),
							ContainSubstring("1"),
							ContainSubstring("is used by volumes:"),
							ContainSubstring("volume-dup-b1"),
							ContainSubstring("volume-dup-b2"),
							ContainSubstring("volume-dup-b3"),
						),
					), "B1 should have duplicate error message")

					updatedB2 := &v1alpha1.ReplicatedVolume{}
					g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvB2), updatedB2)).To(Succeed())
					g.Expect(updatedB2).To(HaveField("Status.Errors.DuplicateDeviceMinor", Not(BeNil())), "B2 should have duplicate error")

					updatedB3 := &v1alpha1.ReplicatedVolume{}
					g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvB3), updatedB3)).To(Succeed())
					g.Expect(updatedB3).To(HaveField("Status.Errors.DuplicateDeviceMinor", Not(BeNil())), "B3 should have duplicate error")

					// Check C1 has no error (single volume, no duplicate)
					updatedC1 := &v1alpha1.ReplicatedVolume{}
					g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvC1), updatedC1)).To(Succeed())
					g.Expect(updatedC1).To(Or(
						HaveField("Status.Errors", BeNil()),
						HaveField("Status.Errors.DuplicateDeviceMinor", BeNil()),
					), "C1 should not have duplicate error")

					// Check D1 has no error (single volume, no duplicate)
					updatedD1 := &v1alpha1.ReplicatedVolume{}
					g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvD1), updatedD1)).To(Succeed())
					g.Expect(updatedD1).To(Or(
						HaveField("Status.Errors", BeNil()),
						HaveField("Status.Errors.DuplicateDeviceMinor", BeNil()),
					), "D1 should not have duplicate error")
				}).Should(Succeed(), "error messages should be set correctly")

				By("Removing A1 and B1, verifying partial resolution")
				Expect(cl.Delete(ctx, rvA1)).To(Succeed(), "should delete A1")
				Expect(cl.Delete(ctx, rvB1)).To(Succeed(), "should delete B1")
				// Reconcile volumes to trigger error clearing
				// Note: We need to reconcile all volumes to trigger duplicate detection for all volumes
				Expect(rec.Reconcile(ctx, RequestFor(rvA2))).ToNot(Requeue(), "should trigger error clearing for A2")
				Expect(rec.Reconcile(ctx, RequestFor(rvB2))).ToNot(Requeue(), "should trigger error clearing for B2")
				Expect(rec.Reconcile(ctx, RequestFor(rvB3))).ToNot(Requeue(), "should trigger error clearing for B3")

				Eventually(func(g Gomega) {
					// A2 should have no error (only one volume left with deviceMinor=0)
					updatedA2 := &v1alpha1.ReplicatedVolume{}
					g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvA2), updatedA2)).To(Succeed())
					g.Expect(updatedA2).To(Or(
						HaveField("Status.Errors", BeNil()),
						HaveField("Status.Errors.DuplicateDeviceMinor", BeNil()),
					), "A2 should not have duplicate error after A1 deletion")

					// B2 and B3 should still have errors (2 volumes still share deviceMinor=1)
					updatedB2 := &v1alpha1.ReplicatedVolume{}
					g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvB2), updatedB2)).To(Succeed())
					g.Expect(updatedB2).To(HaveField("Status.Errors.DuplicateDeviceMinor", Not(BeNil())), "B2 should still have duplicate error")

					updatedB3 := &v1alpha1.ReplicatedVolume{}
					g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvB3), updatedB3)).To(Succeed())
					g.Expect(updatedB3).To(HaveField("Status.Errors.DuplicateDeviceMinor", Not(BeNil())), "B3 should still have duplicate error")
				}).Should(Succeed(), "partial resolution should work correctly")

				By("Removing B2, verifying full resolution")
				Expect(cl.Delete(ctx, rvB2)).To(Succeed(), "should delete B2")

				// Reconcile B3 to trigger error clearing
				// Note: We need to reconcile volumes to trigger duplicate detection for all volumes
				Expect(rec.Reconcile(ctx, RequestFor(rvB3))).ToNot(Requeue(), "should trigger error clearing for B3")

				Eventually(func(g Gomega) {
					// B3 should have no error (only one volume left with deviceMinor=1)
					updatedB3 := &v1alpha1.ReplicatedVolume{}
					g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvB3), updatedB3)).To(Succeed())
					g.Expect(updatedB3).To(Or(
						HaveField("Status.Errors", BeNil()),
						HaveField("Status.Errors.DuplicateDeviceMinor", BeNil()),
					), "B3 should not have duplicate error after B2 deletion")
				}).Should(Succeed(), "full resolution should work correctly")
			})

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
								Name:       fmt.Sprintf("volume-seq-%d", i+1),
								Finalizers: []string{v1alpha1.ControllerAppFinalizer},
							},
							Status: &v1alpha1.ReplicatedVolumeStatus{
								DRBD: &v1alpha1.DRBDResource{
									Config: &v1alpha1.DRBDResourceConfig{
										DeviceMinor: u.Ptr(uint(i)),
									},
								},
							},
						}
					}
					rv6 = &v1alpha1.ReplicatedVolume{
						ObjectMeta: metav1.ObjectMeta{
							Name:       "volume-seq-6",
							Finalizers: []string{v1alpha1.ControllerAppFinalizer},
						},
					}

					rvGap1 := &v1alpha1.ReplicatedVolume{
						ObjectMeta: metav1.ObjectMeta{
							Name:       "volume-gap-1",
							Finalizers: []string{v1alpha1.ControllerAppFinalizer},
						},
						Status: &v1alpha1.ReplicatedVolumeStatus{
							DRBD: &v1alpha1.DRBDResource{
								Config: &v1alpha1.DRBDResourceConfig{
									DeviceMinor: u.Ptr(uint(6)),
								},
							},
						},
					}
					rvGap2 := &v1alpha1.ReplicatedVolume{
						ObjectMeta: metav1.ObjectMeta{
							Name:       "volume-gap-2",
							Finalizers: []string{v1alpha1.ControllerAppFinalizer},
						},
						Status: &v1alpha1.ReplicatedVolumeStatus{
							DRBD: &v1alpha1.DRBDResource{
								Config: &v1alpha1.DRBDResourceConfig{
									DeviceMinor: u.Ptr(uint(8)),
								},
							},
						},
					}
					rvGap3 := &v1alpha1.ReplicatedVolume{
						ObjectMeta: metav1.ObjectMeta{
							Name:       "volume-gap-3",
							Finalizers: []string{v1alpha1.ControllerAppFinalizer},
						},
						Status: &v1alpha1.ReplicatedVolumeStatus{
							DRBD: &v1alpha1.DRBDResource{
								Config: &v1alpha1.DRBDResourceConfig{
									DeviceMinor: u.Ptr(uint(9)),
								},
							},
						},
					}
					rvGap4 = &v1alpha1.ReplicatedVolume{
						ObjectMeta: metav1.ObjectMeta{
							Name:       "volume-gap-4",
							Finalizers: []string{v1alpha1.ControllerAppFinalizer},
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
				})

				It("assigns deviceMinor sequentially and fills gaps", func(ctx SpecContext) {
					By("Reconciling until volume gets sequential deviceMinor (5) after 0-4")
					Eventually(func(g Gomega) *v1alpha1.ReplicatedVolume {
						g.Expect(rec.Reconcile(ctx, RequestFor(rv6))).ToNot(Requeue(), "should not requeue after successful assignment")
						updatedRV := &v1alpha1.ReplicatedVolume{}
						g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv6), updatedRV)).To(Succeed(), "should get updated ReplicatedVolume")
						return updatedRV
					}).Should(HaveField("Status.DRBD.Config.DeviceMinor", PointTo(BeNumerically("==", 5))), "should assign deviceMinor 5 as next sequential value")

					By("Reconciling until volume gets gap-filled deviceMinor (7) between 6 and 8")
					Eventually(func(g Gomega) *v1alpha1.ReplicatedVolume {
						g.Expect(rec.Reconcile(ctx, RequestFor(rvGap4))).ToNot(Requeue(), "should not requeue after successful assignment")
						updatedRV := &v1alpha1.ReplicatedVolume{}
						g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvGap4), updatedRV)).To(Succeed(), "should get updated ReplicatedVolume")
						return updatedRV
					}).Should(HaveField("Status.DRBD.Config.DeviceMinor", PointTo(BeNumerically("==", 7))), "should assign deviceMinor 7 to fill gap between 6 and 8")
				})
			})
		})

		When("RV with deviceMinor already assigned", func() {
			BeforeEach(func() {
				rv = &v1alpha1.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{Name: "volume-1"},
					Status: &v1alpha1.ReplicatedVolumeStatus{
						DRBD: &v1alpha1.DRBDResource{
							Config: &v1alpha1.DRBDResourceConfig{
								DeviceMinor: u.Ptr(uint(42)),
							},
						},
					},
				}
			})

			It("does not reassign deviceMinor and is idempotent", func(ctx SpecContext) {
				By("Reconciling multiple times and verifying deviceMinor remains unchanged")
				Eventually(func(g Gomega) *v1alpha1.ReplicatedVolume {
					for i := 0; i < 3; i++ {
						g.Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue(), "should not requeue when deviceMinor already assigned")
					}
					updatedRV := &v1alpha1.ReplicatedVolume{}
					g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), updatedRV)).To(Succeed(), "should get updated ReplicatedVolume")
					return updatedRV
				}).Should(HaveField("Status.DRBD.Config.DeviceMinor", PointTo(BeNumerically("==", 42))), "deviceMinor should remain 42 after multiple reconciliations (idempotent)")
			})
		})
	})

	When("RV has DRBD.Config without explicit deviceMinor and 0 is already used", func() {
		var (
			rvExisting *v1alpha1.ReplicatedVolume
			rvNew      *v1alpha1.ReplicatedVolume
		)

		BeforeEach(func() {
			// Existing volume that already uses deviceMinor = RVMinDeviceMinor (0)
			rvExisting = &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "volume-zero-used"},
				Status: &v1alpha1.ReplicatedVolumeStatus{
					DRBD: &v1alpha1.DRBDResource{
						Config: &v1alpha1.DRBDResourceConfig{
							DeviceMinor: u.Ptr(v1alpha1.RVMinDeviceMinor), // 0
						},
					},
				},
			}

			// New volume: DRBD.Config is already initialized, but DeviceMinor was never set explicitly
			// (the pointer stays nil and the field is not present in the JSON). We expect the controller
			// to treat this as "minor is not assigned yet" and pick the next free value (1), instead of
			// reusing 0 which is already taken by another volume.
			rvNew = &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "volume-config-no-minor",
					Finalizers: []string{v1alpha1.ControllerAppFinalizer},
				},
				Status: &v1alpha1.ReplicatedVolumeStatus{
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
		})

		It("treats zero-value deviceMinor as unassigned and picks next free value", func(ctx SpecContext) {
			By("Reconciling the RV with DRBD.Config but zero-value deviceMinor")
			result, err := rec.Reconcile(ctx, RequestFor(rvNew))
			Expect(err).NotTo(HaveOccurred(), "reconciliation should succeed")
			Expect(result).ToNot(Requeue(), "should not requeue after successful assignment")

			By("Verifying next free deviceMinor was assigned (RVMinDeviceMinor + 1)")
			updated := &v1alpha1.ReplicatedVolume{}
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvNew), updated)).To(Succeed(), "should get updated ReplicatedVolume")

			Expect(updated).To(HaveField("Status.DRBD.Config.DeviceMinor",
				PointTo(BeNumerically("==", v1alpha1.RVMinDeviceMinor+1))),
				"new volume should get the next free deviceMinor, since 0 is already used",
			)
		})
	})

	When("Patch fails with non-NotFound error", func() {
		var rv *v1alpha1.ReplicatedVolume
		var testError error

		BeforeEach(func() {
			rv = &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "volume-patch-1",
					Finalizers: []string{v1alpha1.ControllerAppFinalizer},
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
			Expect(rec.Reconcile(ctx, RequestFor(rv))).Error().To(MatchError(testError), "should return error when Patch fails")
		})
	})

	When("Patch fails with 409 Conflict", func() {
		var rv *v1alpha1.ReplicatedVolume
		var conflictError error
		var patchAttempts int

		BeforeEach(func() {
			rv = &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "volume-conflict-1",
					Finalizers: []string{v1alpha1.ControllerAppFinalizer},
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
			Expect(rec.Reconcile(ctx, RequestFor(rv))).Error().To(MatchError(conflictError), "should return conflict error on first attempt")

			By("Reconciling until deviceMinor is assigned after conflict resolved")
			Eventually(func(g Gomega) *v1alpha1.ReplicatedVolume {
				g.Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue(), "retry reconciliation should succeed")
				updatedRV := &v1alpha1.ReplicatedVolume{}
				g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), updatedRV)).To(Succeed(), "should get updated ReplicatedVolume")
				return updatedRV
			}).Should(HaveField("Status.DRBD.Config.DeviceMinor", PointTo(BeNumerically(">=", v1alpha1.RVMinDeviceMinor))), "deviceMinor should be assigned after retry")
		})
	})
})
