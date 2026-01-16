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

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	rvstatusconfigsharedsecret "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_status_config_shared_secret"
	indextest "github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes/testhelpers"
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

	// Algorithm shortcuts for readability.
	// NOTE: Tests assume at least 2 algorithms in SharedSecretAlgorithms().
	// If list shrinks to 1, tests will panic (intentionally) as signal to review logic.
	algs := v1alpha1.SharedSecretAlgorithms
	firstAlg := func() string { return string(algs()[0]) }
	secondAlg := func() string { return string(algs()[1]) }
	lastAlg := func() string { return string(algs()[len(algs())-1]) }

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed(), "should add v1alpha1 to scheme")
		// Ensure test assumptions are met
		Expect(len(algs())).To(BeNumerically(">=", 2),
			"tests require at least 2 algorithms to test switching logic")
		clientBuilder = indextest.WithRVRByReplicatedVolumeNameIndex(fake.NewClientBuilder().
			WithScheme(scheme)).
			WithStatusSubresource(&v1alpha1.ReplicatedVolume{}).
			WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{})
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
		var rv *v1alpha1.ReplicatedVolume

		BeforeEach(func() {
			rv = &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-rv",
					Finalizers: []string{v1alpha1.ControllerFinalizer},
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
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), rv)).To(Succeed(), "should get updated ReplicatedVolume")
			Expect(rv).To(HaveField("Status.DRBD.Config.SharedSecret", Not(BeEmpty())), "shared secret should be set")
			Expect(rv).To(HaveField("Status.DRBD.Config.SharedSecretAlg", Equal(v1alpha1.SharedSecretAlg(firstAlg()))), "should use first algorithm ("+firstAlg()+")")
		})

		When("RVR exists without errors", func() {
			var rvr *v1alpha1.ReplicatedVolumeReplica

			BeforeEach(func() {
				rvr = &v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-rvr-no-error",
					},
					Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "test-rv",
						NodeName:             "node-1",
					},
					Status: v1alpha1.ReplicatedVolumeReplicaStatus{
						DRBD: &v1alpha1.DRBD{},
					},
				}
			})

			JustBeforeEach(func(ctx SpecContext) {
				Expect(cl.Create(ctx, rvr)).To(Succeed(), "should create ReplicatedVolumeReplica without error")
			})

			It("generates shared secret even when RVR exists without errors", func(ctx SpecContext) {
				By("Reconciling ReplicatedVolume without shared secret, but with RVR without errors")
				Expect(rec.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: "test-rv"},
				})).ToNot(Requeue(), "reconciliation should succeed")

				By("Verifying shared secret was generated despite RVR without errors")
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), rv)).To(Succeed(), "should get updated ReplicatedVolume")
				Expect(rv).To(HaveField("Status.DRBD.Config.SharedSecret", Not(BeEmpty())), "shared secret should be set even with RVR without errors")
				Expect(rv).To(HaveField("Status.DRBD.Config.SharedSecretAlg", Equal(v1alpha1.SharedSecretAlg(firstAlg()))), "should use first algorithm ("+firstAlg()+")")
			})
		})

		When("shared secret already set", func() {
			BeforeEach(func() {
				rv.Status = v1alpha1.ReplicatedVolumeStatus{
					DRBD: &v1alpha1.DRBDResource{
						Config: &v1alpha1.DRBDResourceConfig{
							SharedSecret:    "test-secret",
							SharedSecretAlg: v1alpha1.SharedSecretAlg(firstAlg()),
						},
					},
				}
			})

			When("no UnsupportedAlgorithm errors", func() {
				It("does nothing on consecutive reconciles (idempotent)", func(ctx SpecContext) {
					By("First reconcile: should not change anything")
					Expect(rec.Reconcile(ctx, reconcile.Request{
						NamespacedName: types.NamespacedName{Name: "test-rv"},
					})).ToNot(Requeue(), "first reconciliation should succeed")

					By("Verifying nothing changed after first reconcile")
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), rv)).To(Succeed(), "should get ReplicatedVolume")
					Expect(rv).To(HaveField("Status.DRBD.Config.SharedSecret", Equal("test-secret")), "shared secret should remain unchanged")
					Expect(rv).To(HaveField("Status.DRBD.Config.SharedSecretAlg", Equal(v1alpha1.SharedSecretAlg(firstAlg()))), "algorithm should remain unchanged ("+firstAlg()+")")

					By("Second reconcile: should still not change anything (idempotent)")
					Expect(rec.Reconcile(ctx, reconcile.Request{
						NamespacedName: types.NamespacedName{Name: "test-rv"},
					})).ToNot(Requeue(), "second reconciliation should succeed")

					By("Verifying nothing changed after second reconcile")
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), rv)).To(Succeed(), "should get ReplicatedVolume")
					Expect(rv).To(HaveField("Status.DRBD.Config.SharedSecret", Equal("test-secret")), "shared secret should remain unchanged")
					Expect(rv).To(HaveField("Status.DRBD.Config.SharedSecretAlg", Equal(v1alpha1.SharedSecretAlg(firstAlg()))), "algorithm should remain "+firstAlg()+", not switch")
				})
			})

			When("UnsupportedAlgorithm error occurs", func() {
				var rvr *v1alpha1.ReplicatedVolumeReplica

				BeforeEach(func() {
					rvr = &v1alpha1.ReplicatedVolumeReplica{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-rvr",
						},
						Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "test-rv",
							NodeName:             "node-1",
						},
						Status: v1alpha1.ReplicatedVolumeReplicaStatus{
							DRBD: &v1alpha1.DRBD{
								Errors: &v1alpha1.DRBDErrors{},
							},
						},
					}
					rvr.Status.DRBD.Errors.SharedSecretAlgSelectionError = &v1alpha1.SharedSecretUnsupportedAlgError{
						UnsupportedAlg: firstAlg(),
					}
				})

				JustBeforeEach(func(ctx SpecContext) {
					Expect(cl.Create(ctx, rvr)).To(Succeed(), "should create ReplicatedVolumeReplica with error")
				})

				It("switches to next algorithm and is idempotent", func(ctx SpecContext) {
					By("First reconcile: switching algorithm " + firstAlg() + " -> " + secondAlg())
					Expect(rec.Reconcile(ctx, reconcile.Request{
						NamespacedName: types.NamespacedName{Name: "test-rv"},
					})).ToNot(Requeue(), "first reconciliation should succeed")

					By("Verifying algorithm was switched to " + secondAlg())
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), rv)).To(Succeed(), "should get updated ReplicatedVolume")
					Expect(rv).To(HaveField("Status.DRBD.Config.SharedSecretAlg", Equal(v1alpha1.SharedSecretAlg(secondAlg()))), "should switch to next algorithm ("+secondAlg()+")")
					// Secret is not regenerated if it already exists (idempotency check in controller)
					Expect(rv).To(HaveField("Status.DRBD.Config.SharedSecret", Equal("test-secret")), "shared secret should remain unchanged when switching algorithm")
					firstSecret := rv.Status.DRBD.Config.SharedSecret
					Expect(firstSecret).ToNot(BeEmpty(), "secret should be set")

					By("Second reconcile: should not change anything (idempotent)")
					Expect(rec.Reconcile(ctx, reconcile.Request{
						NamespacedName: types.NamespacedName{Name: "test-rv"},
					})).ToNot(Requeue(), "second reconciliation should succeed")

					By("Verifying nothing changed on second reconcile")
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), rv)).To(Succeed(), "should get ReplicatedVolume")
					Expect(rv).To(HaveField("Status.DRBD.Config.SharedSecretAlg", Equal(v1alpha1.SharedSecretAlg(secondAlg()))), "algorithm should remain "+secondAlg())
					Expect(rv).To(HaveField("Status.DRBD.Config.SharedSecret", Equal(firstSecret)), "secret should remain unchanged")
				})

				When("multiple RVRs with different algorithms", func() {
					var rvr2, rvrOtherRV *v1alpha1.ReplicatedVolumeReplica

					BeforeEach(func() {
						// RVR2: lastAlg - maximum index (all exhausted)
						rvr2 = &v1alpha1.ReplicatedVolumeReplica{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test-rvr-2",
							},
							Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "test-rv",
								NodeName:             "node-2",
							},
							Status: v1alpha1.ReplicatedVolumeReplicaStatus{
								DRBD: &v1alpha1.DRBD{
									Errors: &v1alpha1.DRBDErrors{},
								},
							},
						}
						rvr2.Status.DRBD.Errors.SharedSecretAlgSelectionError = &v1alpha1.SharedSecretUnsupportedAlgError{
							UnsupportedAlg: lastAlg(),
						}

						// RVR for another RV - should be ignored
						rvrOtherRV = &v1alpha1.ReplicatedVolumeReplica{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test-rvr-other",
							},
							Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "other-rv",
								NodeName:             "node-3",
							},
							Status: v1alpha1.ReplicatedVolumeReplicaStatus{
								DRBD: &v1alpha1.DRBD{
									Errors: &v1alpha1.DRBDErrors{},
								},
							},
						}
						rvrOtherRV.Status.DRBD.Errors.SharedSecretAlgSelectionError = &v1alpha1.SharedSecretUnsupportedAlgError{
							UnsupportedAlg: firstAlg(),
						}
					})

					JustBeforeEach(func(ctx SpecContext) {
						Expect(cl.Create(ctx, rvr2)).To(Succeed(), "should create RVR2")
						Expect(cl.Create(ctx, rvrOtherRV)).To(Succeed(), "should create RVR for other RV")
					})

					It("selects maximum algorithm index and ignores RVRs from other volumes", func(ctx SpecContext) {
						By("Reconciling with multiple RVRs having different algorithms")
						Expect(rec.Reconcile(ctx, reconcile.Request{
							NamespacedName: types.NamespacedName{Name: "test-rv"},
						})).ToNot(Requeue(), "reconciliation should succeed")

						By("Verifying algorithm was not changed (" + lastAlg() + " is last, all exhausted)")
						Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), rv)).To(Succeed(), "should get updated ReplicatedVolume")
						Expect(rv).To(HaveField("Status.DRBD.Config.SharedSecretAlg", Equal(v1alpha1.SharedSecretAlg(firstAlg()))), "should remain "+firstAlg()+" (all exhausted)")
					})
				})

				When("RVRs with empty UnsupportedAlg", func() {
					var rvrWithAlg, rvrWithoutAlg, rvrWithUnknownAlg *v1alpha1.ReplicatedVolumeReplica

					BeforeEach(func() {
						// RVR with UnsupportedAlg
						rvrWithAlg = &v1alpha1.ReplicatedVolumeReplica{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test-rvr-with-alg",
							},
							Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "test-rv",
								NodeName:             "node-2",
							},
							Status: v1alpha1.ReplicatedVolumeReplicaStatus{
								DRBD: &v1alpha1.DRBD{
									Errors: &v1alpha1.DRBDErrors{},
								},
							},
						}
						rvrWithAlg.Status.DRBD.Errors.SharedSecretAlgSelectionError = &v1alpha1.SharedSecretUnsupportedAlgError{
							UnsupportedAlg: firstAlg(),
						}

						// RVR with error but empty UnsupportedAlg
						rvrWithoutAlg = &v1alpha1.ReplicatedVolumeReplica{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test-rvr-no-alg",
							},
							Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "test-rv",
								NodeName:             "node-3",
							},
							Status: v1alpha1.ReplicatedVolumeReplicaStatus{
								DRBD: &v1alpha1.DRBD{
									Errors: &v1alpha1.DRBDErrors{},
								},
							},
						}
						rvrWithoutAlg.Status.DRBD.Errors.SharedSecretAlgSelectionError = &v1alpha1.SharedSecretUnsupportedAlgError{
							UnsupportedAlg: "", // Empty
						}

						// RVR with unknown algorithm (not in SharedSecretAlgorithms list)
						// This simulates a scenario where algorithm list changes or RVR reports unexpected value
						rvrWithUnknownAlg = &v1alpha1.ReplicatedVolumeReplica{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test-rvr-unknown-alg",
							},
							Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "test-rv",
								NodeName:             "node-4",
							},
							Status: v1alpha1.ReplicatedVolumeReplicaStatus{
								DRBD: &v1alpha1.DRBD{
									Errors: &v1alpha1.DRBDErrors{},
								},
							},
						}
						rvrWithUnknownAlg.Status.DRBD.Errors.SharedSecretAlgSelectionError = &v1alpha1.SharedSecretUnsupportedAlgError{
							UnsupportedAlg: "md5", // Unknown algorithm (not in SharedSecretAlgorithms)
						}
					})

					JustBeforeEach(func(ctx SpecContext) {
						Expect(cl.Create(ctx, rvrWithAlg)).To(Succeed(), "should create RVR with alg")
						Expect(cl.Create(ctx, rvrWithoutAlg)).To(Succeed(), "should create RVR without alg")
						Expect(cl.Create(ctx, rvrWithUnknownAlg)).To(Succeed(), "should create RVR with unknown alg")
					})

					It("uses RVR with valid UnsupportedAlg and ignores empty and unknown ones", func(ctx SpecContext) {
						By("Reconciling with mixed RVRs (valid, empty, and unknown algorithms)")
						Expect(rec.Reconcile(ctx, reconcile.Request{
							NamespacedName: types.NamespacedName{Name: "test-rv"},
						})).ToNot(Requeue(), "reconciliation should succeed")

						By("Verifying algorithm switched to " + secondAlg() + " (next after " + firstAlg() + ", ignoring empty and unknown)")
						Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), rv)).To(Succeed(), "should get updated ReplicatedVolume")
						Expect(rv).To(HaveField("Status.DRBD.Config.SharedSecretAlg", Equal(v1alpha1.SharedSecretAlg(secondAlg()))), "should switch to "+secondAlg()+" using valid algorithm, ignoring empty and unknown")
					})

					When("all RVRs have empty UnsupportedAlg", func() {
						BeforeEach(func() {
							// Set all RVRs to have empty UnsupportedAlg
							// Parent rvr should also have empty UnsupportedAlg
							rvr.Status.DRBD.Errors.SharedSecretAlgSelectionError.UnsupportedAlg = ""
							// Set rvrWithAlg to also have empty UnsupportedAlg
							rvrWithAlg.Status.DRBD.Errors.SharedSecretAlgSelectionError.UnsupportedAlg = ""
						})

						It("does not switch algorithm when all RVRs have empty UnsupportedAlg", func(ctx SpecContext) {
							By("Reconciling with all RVRs having empty UnsupportedAlg")
							Expect(rec.Reconcile(ctx, reconcile.Request{
								NamespacedName: types.NamespacedName{Name: "test-rv"},
							})).ToNot(Requeue(), "reconciliation should succeed")

							By("Verifying algorithm was not changed (cannot determine which algorithm is unsupported)")
							Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), rv)).To(Succeed(), "should get updated ReplicatedVolume")
							Expect(rv).To(HaveField("Status.DRBD.Config.SharedSecretAlg", Equal(v1alpha1.SharedSecretAlg(firstAlg()))), "algorithm should remain "+firstAlg()+" (cannot switch without knowing which algorithm is unsupported)")
						})
					})
				})
			})
		})

		When("Get fails with non-NotFound error", func() {
			internalServerError := errors.New("internal server error")
			BeforeEach(func() {
				clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						if _, ok := obj.(*v1alpha1.ReplicatedVolume); ok {
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
				// Set sharedSecret so controller will check RVRs (reconcileSwitchAlgorithm)
				rv.Status = v1alpha1.ReplicatedVolumeStatus{
					DRBD: &v1alpha1.DRBDResource{
						Config: &v1alpha1.DRBDResourceConfig{
							SharedSecret:    "test-secret",
							SharedSecretAlg: v1alpha1.SharedSecretAlg(firstAlg()),
						},
					},
				}
				clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
					List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
						if _, ok := list.(*v1alpha1.ReplicatedVolumeReplicaList); ok {
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
						if _, ok := obj.(*v1alpha1.ReplicatedVolume); ok {
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
