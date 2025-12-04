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

package rvstatusconfigsharedsecret_test

import (
	"context"
	"errors"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	gomegatypes "github.com/onsi/gomega/types" // cspell:words gomegatypes
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha3 "github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	rvstatusconfigsharedsecret "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_status_config_shared_secret"
)

func TestReconciler(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Reconciler Suite")
}

var _ = Describe("Reconciler", func() {
	// Available in BeforeEach
	var (
		clientBuilder *fake.ClientBuilder
		scheme        *runtime.Scheme
	)

	// Available in JustBeforeEach
	var (
		cl  client.WithWatch
		rec *rvstatusconfigsharedsecret.Reconciler
	)

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha3.AddToScheme(scheme)).To(Succeed(), "should add v1alpha3 to scheme")
		clientBuilder = fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&v1alpha3.ReplicatedVolume{}).
			WithStatusSubresource(&v1alpha3.ReplicatedVolumeReplica{})
		cl = nil
		rec = nil
	})

	JustBeforeEach(func() {
		cl = clientBuilder.Build()
		rec = rvstatusconfigsharedsecret.NewReconciler(cl, GinkgoLogr)
	})

	It("returns no error when ReplicatedVolume does not exist", func(ctx SpecContext) {
		Expect(rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "non-existent"},
		})).ToNot(Requeue(), "should ignore NotFound errors")
	})

	When("ReplicatedVolume created", func() {
		var rv *v1alpha3.ReplicatedVolume

		BeforeEach(func() {
			rv = &v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-rv",
				},
			}
		})

		JustBeforeEach(func(ctx SpecContext) {
			Expect(cl.Create(ctx, rv)).To(Succeed(), "should create ReplicatedVolume")
		})

		It("generates shared secret initially", func(ctx SpecContext) {
			By("Reconciling ReplicatedVolume without shared secret")
			Expect(rec.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-rv"},
			})).ToNot(Requeue(), "reconciliation should succeed")

			By("Verifying shared secret was generated")
			updatedRV := &v1alpha3.ReplicatedVolume{}
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), updatedRV)).To(Succeed(), "should get updated ReplicatedVolume")
			Expect(updatedRV).To(HaveField("Status.DRBD.Config.SharedSecret", Not(BeEmpty())), "shared secret should be set")
			Expect(updatedRV).To(HaveField("Status.DRBD.Config.SharedSecretAlg", Equal(v1alpha3.SharedSecretAlgSHA256)), "should use first algorithm (sha256)")
		})

		When("shared secret already set", func() {
			BeforeEach(func() {
				rv.Status = &v1alpha3.ReplicatedVolumeStatus{
					DRBD: &v1alpha3.DRBDResource{
						Config: &v1alpha3.DRBDResourceConfig{
							SharedSecret:    "test-secret",
							SharedSecretAlg: v1alpha3.SharedSecretAlgSHA256,
						},
					},
				}
			})

			When("no UnsupportedAlgorithm errors", func() {
				It("does nothing on first reconcile", func(ctx SpecContext) {
					By("First reconcile: should not change anything")
					Expect(rec.Reconcile(ctx, reconcile.Request{
						NamespacedName: types.NamespacedName{Name: "test-rv"},
					})).ToNot(Requeue(), "reconciliation should succeed")

					By("Verifying nothing changed")
					updatedRV := &v1alpha3.ReplicatedVolume{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), updatedRV)).To(Succeed(), "should get ReplicatedVolume")
					Expect(updatedRV).To(HaveField("Status.DRBD.Config.SharedSecret", Equal("test-secret")), "shared secret should remain unchanged")
					Expect(updatedRV).To(HaveField("Status.DRBD.Config.SharedSecretAlg", Equal(v1alpha3.SharedSecretAlgSHA256)), "algorithm should remain unchanged")
				})

				It("does nothing on second consecutive reconcile", func(ctx SpecContext) {
					By("First reconcile: should not change anything")
					Expect(rec.Reconcile(ctx, reconcile.Request{
						NamespacedName: types.NamespacedName{Name: "test-rv"},
					})).ToNot(Requeue(), "first reconciliation should succeed")

					By("Second reconcile: should still not change anything (idempotent)")
					Expect(rec.Reconcile(ctx, reconcile.Request{
						NamespacedName: types.NamespacedName{Name: "test-rv"},
					})).ToNot(Requeue(), "second reconciliation should succeed")

					By("Verifying algorithm did not switch to next one")
					updatedRV := &v1alpha3.ReplicatedVolume{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), updatedRV)).To(Succeed(), "should get ReplicatedVolume")
					Expect(updatedRV).To(HaveField("Status.DRBD.Config.SharedSecretAlg", Equal(v1alpha3.SharedSecretAlgSHA256)), "algorithm should remain sha256, not switch to sha1")
				})
			})

			When("UnsupportedAlgorithm error occurs", func() {
				var rvr *v1alpha3.ReplicatedVolumeReplica

				BeforeEach(func() {
					unsupportedAlg := v1alpha3.SharedSecretAlgSHA256
					rvr = &v1alpha3.ReplicatedVolumeReplica{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-rvr",
						},
						Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "test-rv",
							NodeName:             "node-1",
						},
						Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
							DRBD: &v1alpha3.DRBD{
								Errors: &v1alpha3.DRBDErrors{
									SharedSecretAlgSelectionError: &v1alpha3.SharedSecretUnsupportedAlgError{
										UnsupportedAlg: unsupportedAlg,
									},
								},
							},
						},
					}
				})

				JustBeforeEach(func(ctx SpecContext) {
					Expect(cl.Create(ctx, rvr)).To(Succeed(), "should create ReplicatedVolumeReplica with error")
				})

				It("switches to next algorithm", func(ctx SpecContext) {
					By("Reconciling ReplicatedVolume with UnsupportedAlgorithm error")
					Expect(rec.Reconcile(ctx, reconcile.Request{
						NamespacedName: types.NamespacedName{Name: "test-rv"},
					})).ToNot(Requeue(), "reconciliation should succeed")

					By("Verifying algorithm was switched to sha1")
					updatedRV := &v1alpha3.ReplicatedVolume{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), updatedRV)).To(Succeed(), "should get updated ReplicatedVolume")
					Expect(updatedRV).To(HaveField("Status.DRBD.Config.SharedSecretAlg", Equal(v1alpha3.SharedSecretAlgSHA1)), "should switch to next algorithm (sha1)")
					Expect(updatedRV).To(HaveField("Status.DRBD.Config.SharedSecret", Not(Equal("test-secret"))), "shared secret should be regenerated")
				})

				When("last algorithm already used", func() {
					BeforeEach(func() {
						rv.Status.DRBD.Config.SharedSecretAlg = v1alpha3.SharedSecretAlgSHA1 // Last algorithm
					})

					It("stops trying when all algorithms are exhausted", func(ctx SpecContext) {
						By("Reconciling ReplicatedVolume with last algorithm failed")
						Expect(rec.Reconcile(ctx, reconcile.Request{
							NamespacedName: types.NamespacedName{Name: "test-rv"},
						})).ToNot(Requeue(), "reconciliation should succeed")

						By("Verifying algorithm was not changed (all exhausted)")
						updatedRV := &v1alpha3.ReplicatedVolume{}
						Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), updatedRV)).To(Succeed(), "should get updated ReplicatedVolume")
						Expect(updatedRV).To(HaveField("Status.DRBD.Config.SharedSecretAlg", Equal(v1alpha3.SharedSecretAlgSHA1)), "algorithm should remain unchanged when all exhausted")
					})
				})
			})

			When("algorithm becomes unsupported after initial creation", func() {
				var rvrWithoutError *v1alpha3.ReplicatedVolumeReplica

				BeforeEach(func() {
					// Create RVR without error initially
					rvrWithoutError = &v1alpha3.ReplicatedVolumeReplica{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-rvr-update",
						},
						Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "test-rv",
							NodeName:             "node-2",
						},
						Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
							DRBD: &v1alpha3.DRBD{},
						},
					}
				})

				JustBeforeEach(func(ctx SpecContext) {
					Expect(cl.Create(ctx, rvrWithoutError)).To(Succeed(), "should create ReplicatedVolumeReplica without error")
				})

				It("should reconcile when RVR gets UnsupportedAlgorithm error on update", func(ctx SpecContext) {
					By("First reconcile: should not change anything (no errors)")
					Expect(rec.Reconcile(ctx, reconcile.Request{
						NamespacedName: types.NamespacedName{Name: "test-rv"},
					})).ToNot(Requeue(), "reconciliation should succeed")

					By("Updating RVR to have UnsupportedAlgorithm error")
					updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvrWithoutError), updatedRVR)).To(Succeed(), "should get RVR")
					if updatedRVR.Status.DRBD == nil {
						updatedRVR.Status.DRBD = &v1alpha3.DRBD{}
					}
					updatedRVR.Status.DRBD.Errors = &v1alpha3.DRBDErrors{
						SharedSecretAlgSelectionError: &v1alpha3.SharedSecretUnsupportedAlgError{
							UnsupportedAlg: v1alpha3.SharedSecretAlgSHA256,
						},
					}
					Expect(cl.Status().Update(ctx, updatedRVR)).To(Succeed(), "should update RVR with error")

					By("Second reconcile: should switch to next algorithm")
					Expect(rec.Reconcile(ctx, reconcile.Request{
						NamespacedName: types.NamespacedName{Name: "test-rv"},
					})).ToNot(Requeue(), "reconciliation should succeed")

					By("Verifying algorithm was switched to sha1")
					updatedRV := &v1alpha3.ReplicatedVolume{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), updatedRV)).To(Succeed(), "should get updated ReplicatedVolume")
					Expect(updatedRV).To(HaveField("Status.DRBD.Config.SharedSecretAlg", Equal(v1alpha3.SharedSecretAlgSHA1)), "should switch to next algorithm (sha1) after RVR update")
				})
			})
		})

		When("Get fails with non-NotFound error", func() {
			internalServerError := errors.New("internal server error")
			BeforeEach(func() {
				clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						if _, ok := obj.(*v1alpha3.ReplicatedVolume); ok {
							return internalServerError
						}
						return cl.Get(ctx, key, obj, opts...)
					},
				})
			})

			It("should fail if getting ReplicatedVolume failed with non-NotFound error", func(ctx SpecContext) {
				Expect(rec.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: "test-rv"},
				})).Error().To(MatchError(internalServerError), "should return error when Get fails")
			})
		})

		When("List fails", func() {
			listError := errors.New("failed to list replicas")
			BeforeEach(func() {
				// Set sharedSecret so controller will check RVRs (reconcileHandleUnsupportedAlgorithm)
				rv.Status = &v1alpha3.ReplicatedVolumeStatus{
					DRBD: &v1alpha3.DRBDResource{
						Config: &v1alpha3.DRBDResourceConfig{
							SharedSecret:    "test-secret",
							SharedSecretAlg: v1alpha3.SharedSecretAlgSHA256,
						},
					},
				}
				clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
					List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
						if _, ok := list.(*v1alpha3.ReplicatedVolumeReplicaList); ok {
							return listError
						}
						return cl.List(ctx, list, opts...)
					},
				})
			})

			It("should fail if listing ReplicatedVolumeReplicas failed", func(ctx SpecContext) {
				Expect(rec.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: "test-rv"},
				})).Error().To(MatchError(listError), "should return error when List fails")
			})
		})

		When("Patch fails with non-NotFound error", func() {
			patchError := errors.New("failed to patch status")
			BeforeEach(func() {
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

			It("should fail if patching ReplicatedVolume status failed with non-NotFound error", func(ctx SpecContext) {
				Expect(rec.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: "test-rv"},
				})).Error().To(MatchError(patchError), "should return error when Patch fails")
			})
		})
	})
})

func RequestFor(object client.Object) reconcile.Request {
	return reconcile.Request{NamespacedName: client.ObjectKeyFromObject(object)}
}

func Requeue() gomegatypes.GomegaMatcher {
	return Not(Equal(reconcile.Result{}))
}
