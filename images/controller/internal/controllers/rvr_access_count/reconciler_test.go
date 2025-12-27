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

package rvraccesscount_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	rvraccesscount "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_access_count"
)

var _ = Describe("Reconciler", func() {
	var (
		clientBuilder *fake.ClientBuilder
		scheme        *runtime.Scheme
		cl            client.WithWatch
		rec           *rvraccesscount.Reconciler
	)

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed(), "should add v1alpha1 to scheme")
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed(), "should add v1alpha1 to scheme")
		clientBuilder = fake.NewClientBuilder().
			WithScheme(scheme).
			// WithStatusSubresource makes fake client mimic real API server behavior:
			// - Create() ignores status field
			// - Update() ignores status field
			// - Status().Update() updates only status
			// This means tests must use Status().Update() to set status after Create().
			WithStatusSubresource(&v1alpha1.ReplicatedVolume{}, &v1alpha1.ReplicatedVolumeReplica{})
	})

	JustBeforeEach(func() {
		cl = clientBuilder.Build()
		rec = rvraccesscount.NewReconciler(cl, GinkgoLogr, scheme)
	})

	It("returns no error when ReplicatedVolume does not exist", func(ctx SpecContext) {
		Expect(rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "non-existent"},
		})).ToNot(Requeue(), "should ignore NotFound errors")
	})

	When("Get RV fails with non-NotFound error", func() {
		testError := errors.New("internal server error")

		BeforeEach(func() {
			clientBuilder = clientBuilder.WithInterceptorFuncs(
				InterceptGet(func(_ *v1alpha1.ReplicatedVolume) error {
					return testError
				}),
			)
		})

		It("should return error", func(ctx SpecContext) {
			Expect(rec.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-rv"},
			})).Error().To(MatchError(testError), "should return error when Get fails")
		})
	})

	When("RV created", func() {
		var (
			rv  *v1alpha1.ReplicatedVolume
			rsc *v1alpha1.ReplicatedStorageClass
		)

		BeforeEach(func() {
			rv = &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-volume",
					UID:        "test-uid",
					Finalizers: []string{v1alpha1.ControllerAppFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					ReplicatedStorageClassName: "test-rsc",
					AttachTo:                   []string{},
				},
			}
			rsc = &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-rsc",
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					VolumeAccess: v1alpha1.VolumeAccessPreferablyLocal,
				},
			}
		})

		JustBeforeEach(func(ctx SpecContext) {
			Expect(cl.Create(ctx, rsc)).To(Succeed(), "should create RSC")
			Expect(cl.Create(ctx, rv)).To(Succeed(), "should create RV")
		})

		When("RV is being deleted", func() {
			BeforeEach(func() {
				rv.Finalizers = []string{"test-finalizer"}
			})

			JustBeforeEach(func(ctx SpecContext) {
				Expect(cl.Delete(ctx, rv)).To(Succeed(), "should delete RV")

				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), rv)).To(Succeed(), "should get RV after delete")
				Expect(rv.DeletionTimestamp).ToNot(BeNil(), "DeletionTimestamp should be set after Delete")
			})

			It("should skip without error", func(ctx SpecContext) {
				Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue(), "should not requeue when RV is being deleted")
			})
		})

		When("volumeAccess is Local", func() {
			BeforeEach(func() {
				rsc.Spec.VolumeAccess = v1alpha1.VolumeAccessLocal
			})

			It("should skip without creating Access RVR", func(ctx SpecContext) {
				rv.Spec.AttachTo = []string{"node-1"}
				Expect(cl.Update(ctx, rv)).To(Succeed())

				Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue(), "should not requeue for Local volumeAccess")

				By("Verifying no Access RVR was created")
				rvrList := &v1alpha1.ReplicatedVolumeReplicaList{}
				Expect(cl.List(ctx, rvrList)).To(Succeed())
				Expect(rvrList.Items).To(BeEmpty(), "should not create Access RVR for Local volumeAccess")
			})
		})

		When("attachTo has node without replicas", func() {
			BeforeEach(func() {
				rv.Spec.AttachTo = []string{"node-1"}
			})

			It("should create Access RVR", func(ctx SpecContext) {
				By("Reconciling RV with attachTo node")
				Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue(), "should not requeue after creating Access RVR")

				By("Verifying Access RVR was created")
				rvrList := &v1alpha1.ReplicatedVolumeReplicaList{}
				Expect(cl.List(ctx, rvrList)).To(Succeed())
				Expect(rvrList.Items).To(HaveLen(1), "should create one Access RVR")
				Expect(rvrList.Items[0].Spec.Type).To(Equal(v1alpha1.ReplicaTypeAccess), "should be Access type")
				Expect(rvrList.Items[0].Spec.NodeName).To(Equal("node-1"), "should be on node-1")
				Expect(rvrList.Items[0].Spec.ReplicatedVolumeName).To(Equal("test-volume"), "should reference the RV")
			})
		})

		When("attachTo has node with Diskful replica", func() {
			var diskfulRVR *v1alpha1.ReplicatedVolumeReplica

			BeforeEach(func() {
				rv.Spec.AttachTo = []string{"node-1"}
				diskfulRVR = &v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "storage.deckhouse.io/v1alpha1",
								Kind:       "ReplicatedVolume",
								Name:       "test-volume",
								UID:        "test-uid",
							},
						},
					},
					Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "test-volume",
						NodeName:             "node-1",
						Type:                 v1alpha1.ReplicaTypeDiskful,
					},
				}
				diskfulRVR.SetNameWithNodeID(10)
			})

			JustBeforeEach(func(ctx SpecContext) {
				Expect(cl.Create(ctx, diskfulRVR)).To(Succeed(), "should create Diskful RVR")
			})

			It("should NOT create Access RVR", func(ctx SpecContext) {
				By("Reconciling RV with Diskful replica on attachTo node")
				Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue(), "should not requeue")

				By("Verifying no additional RVR was created")
				rvrList := &v1alpha1.ReplicatedVolumeReplicaList{}
				Expect(cl.List(ctx, rvrList)).To(Succeed())
				Expect(rvrList.Items).To(HaveLen(1), "should only have the Diskful RVR")
				Expect(rvrList.Items[0].Spec.Type).To(Equal(v1alpha1.ReplicaTypeDiskful), "should be Diskful type")
			})
		})

		When("attachTo has node with TieBreaker replica", func() {
			var tieBreakerRVR *v1alpha1.ReplicatedVolumeReplica

			BeforeEach(func() {
				rv.Spec.AttachTo = []string{"node-1"}
				tieBreakerRVR = &v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "storage.deckhouse.io/v1alpha1",
								Kind:       "ReplicatedVolume",
								Name:       "test-volume",
								UID:        "test-uid",
							},
						},
					},
					Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "test-volume",
						NodeName:             "node-1",
						Type:                 v1alpha1.ReplicaTypeTieBreaker,
					},
				}
				tieBreakerRVR.SetNameWithNodeID(10)
			})

			JustBeforeEach(func(ctx SpecContext) {
				Expect(cl.Create(ctx, tieBreakerRVR)).To(Succeed(), "should create TieBreaker RVR")
			})

			It("should NOT create Access RVR (TieBreaker can be converted to Access by rv-attach-controller)", func(ctx SpecContext) {
				By("Reconciling RV with TieBreaker replica on attachTo node")
				Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue(), "should not requeue")

				By("Verifying no additional RVR was created")
				rvrList := &v1alpha1.ReplicatedVolumeReplicaList{}
				Expect(cl.List(ctx, rvrList)).To(Succeed())
				Expect(rvrList.Items).To(HaveLen(1), "should only have the TieBreaker RVR")
				Expect(rvrList.Items[0].Spec.Type).To(Equal(v1alpha1.ReplicaTypeTieBreaker), "should be TieBreaker type")
			})
		})

		When("Access RVR exists on node not in attachTo and not in attachedTo", func() {
			var accessRVR *v1alpha1.ReplicatedVolumeReplica

			BeforeEach(func() {
				rv.Spec.AttachTo = []string{}
				accessRVR = &v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "storage.deckhouse.io/v1alpha1",
								Kind:       "ReplicatedVolume",
								Name:       "test-volume",
								UID:        "test-uid",
							},
						},
					},
					Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "test-volume",
						NodeName:             "node-1",
						Type:                 v1alpha1.ReplicaTypeAccess,
					},
				}
				accessRVR.SetNameWithNodeID(10)
			})

			JustBeforeEach(func(ctx SpecContext) {
				Expect(cl.Create(ctx, accessRVR)).To(Succeed(), "should create Access RVR")
			})

			It("should delete Access RVR", func(ctx SpecContext) {
				By("Reconciling RV with Access RVR on node not in attachTo/attachedTo")
				Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue(), "should not requeue")

				By("Verifying Access RVR was deleted")
				rvrList := &v1alpha1.ReplicatedVolumeReplicaList{}
				Expect(cl.List(ctx, rvrList)).To(Succeed())
				Expect(rvrList.Items).To(BeEmpty(), "should delete Access RVR")
			})
		})

		When("Access RVR exists on node not in attachTo but in attachedTo", func() {
			var accessRVR *v1alpha1.ReplicatedVolumeReplica

			BeforeEach(func() {
				rv.Spec.AttachTo = []string{}
				rv.Status = &v1alpha1.ReplicatedVolumeStatus{
					AttachedTo: []string{"node-1"},
				}
				accessRVR = &v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "storage.deckhouse.io/v1alpha1",
								Kind:       "ReplicatedVolume",
								Name:       "test-volume",
								UID:        "test-uid",
							},
						},
					},
					Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "test-volume",
						NodeName:             "node-1",
						Type:                 v1alpha1.ReplicaTypeAccess,
					},
				}
				accessRVR.SetNameWithNodeID(10)
			})

			JustBeforeEach(func(ctx SpecContext) {
				Expect(cl.Create(ctx, accessRVR)).To(Succeed(), "should create Access RVR")
				// Update RV with status
				Expect(cl.Status().Update(ctx, rv)).To(Succeed(), "should update RV status")
			})

			It("should NOT delete Access RVR", func(ctx SpecContext) {
				By("Reconciling RV with Access RVR on node in attachedTo")
				Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue(), "should not requeue")

				By("Verifying Access RVR was NOT deleted")
				rvrList := &v1alpha1.ReplicatedVolumeReplicaList{}
				Expect(cl.List(ctx, rvrList)).To(Succeed())
				Expect(rvrList.Items).To(HaveLen(1), "should keep Access RVR")
				Expect(rvrList.Items[0].Spec.Type).To(Equal(v1alpha1.ReplicaTypeAccess), "should be Access type")
			})
		})

		When("multiple nodes in attachTo", func() {
			BeforeEach(func() {
				rv.Spec.AttachTo = []string{"node-1", "node-2"}
			})

			It("should create Access RVR for each node without replicas", func(ctx SpecContext) {
				By("Reconciling RV with multiple attachTo nodes")
				Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue(), "should not requeue")

				By("Verifying Access RVRs were created for both nodes")
				rvrList := &v1alpha1.ReplicatedVolumeReplicaList{}
				Expect(cl.List(ctx, rvrList)).To(Succeed())
				Expect(rvrList.Items).To(HaveLen(2), "should create two Access RVRs")

				nodeNames := make(map[string]bool)
				for _, rvr := range rvrList.Items {
					Expect(rvr.Spec.Type).To(Equal(v1alpha1.ReplicaTypeAccess), "should be Access type")
					nodeNames[rvr.Spec.NodeName] = true
				}
				Expect(nodeNames).To(HaveKey("node-1"))
				Expect(nodeNames).To(HaveKey("node-2"))
			})
		})

		When("reconcile is called twice (idempotency)", func() {
			BeforeEach(func() {
				rv.Spec.AttachTo = []string{"node-1"}
			})

			It("should not create duplicate Access RVRs", func(ctx SpecContext) {
				By("First reconcile - creates Access RVR")
				Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue(), "should not requeue on first reconcile")

				By("Verifying one Access RVR was created")
				rvrList := &v1alpha1.ReplicatedVolumeReplicaList{}
				Expect(cl.List(ctx, rvrList)).To(Succeed())
				Expect(rvrList.Items).To(HaveLen(1), "should create one Access RVR")

				By("Second reconcile - should be idempotent")
				Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue(), "should not requeue on second reconcile")

				By("Verifying still only one Access RVR exists (no duplicates)")
				Expect(cl.List(ctx, rvrList)).To(Succeed())
				Expect(rvrList.Items).To(HaveLen(1), "should still have only one Access RVR (idempotent)")
				Expect(rvrList.Items[0].Spec.Type).To(Equal(v1alpha1.ReplicaTypeAccess), "should be Access type")
				Expect(rvrList.Items[0].Spec.NodeName).To(Equal("node-1"), "should be on node-1")
			})
		})
	})

	When("Get RSC fails", func() {
		var (
			rv        *v1alpha1.ReplicatedVolume
			rsc       *v1alpha1.ReplicatedStorageClass
			testError error
		)

		BeforeEach(func() {
			testError = errors.New("RSC get error")
			rv = &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-volume",
					UID:        "test-uid",
					Finalizers: []string{v1alpha1.ControllerAppFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					ReplicatedStorageClassName: "test-rsc",
					AttachTo:                   []string{"node-1"},
				},
			}
			rsc = &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-rsc",
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					VolumeAccess: v1alpha1.VolumeAccessPreferablyLocal,
				},
			}
			clientBuilder = clientBuilder.WithInterceptorFuncs(
				InterceptGet(func(obj *v1alpha1.ReplicatedStorageClass) error {
					if obj != nil && obj.Name == "test-rsc" {
						return testError
					}
					return nil
				}),
			)
		})

		It("should return error", func(ctx SpecContext) {
			Expect(cl.Create(ctx, rsc)).To(Succeed(), "should create RSC")
			Expect(cl.Create(ctx, rv)).To(Succeed(), "should create RV")

			Expect(rec.Reconcile(ctx, RequestFor(rv))).Error().To(MatchError(testError), "should return error when Get RSC fails")
		})
	})

	When("List RVRs fails", func() {
		var (
			rv        *v1alpha1.ReplicatedVolume
			rsc       *v1alpha1.ReplicatedStorageClass
			testError error
		)

		BeforeEach(func() {
			testError = errors.New("List RVRs error")
			rv = &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-volume",
					UID:        "test-uid",
					Finalizers: []string{v1alpha1.ControllerAppFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					ReplicatedStorageClassName: "test-rsc",
					AttachTo:                   []string{"node-1"},
				},
			}
			rsc = &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-rsc",
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					VolumeAccess: v1alpha1.VolumeAccessPreferablyLocal,
				},
			}
			clientBuilder = clientBuilder.WithInterceptorFuncs(
				interceptor.Funcs{
					List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
						if _, ok := list.(*v1alpha1.ReplicatedVolumeReplicaList); ok {
							return testError
						}
						return c.List(ctx, list, opts...)
					},
				},
			)
		})

		It("should return error", func(ctx SpecContext) {
			Expect(cl.Create(ctx, rsc)).To(Succeed(), "should create RSC")
			Expect(cl.Create(ctx, rv)).To(Succeed(), "should create RV")

			Expect(rec.Reconcile(ctx, RequestFor(rv))).Error().To(MatchError(testError), "should return error when List RVRs fails")
		})
	})

	When("Create Access RVR fails", func() {
		var (
			rv        *v1alpha1.ReplicatedVolume
			rsc       *v1alpha1.ReplicatedStorageClass
			testError error
		)

		BeforeEach(func() {
			testError = errors.New("Create RVR error")
			rv = &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-volume",
					UID:        "test-uid",
					Finalizers: []string{v1alpha1.ControllerAppFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					ReplicatedStorageClassName: "test-rsc",
					AttachTo:                   []string{"node-1"},
				},
			}
			rsc = &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-rsc",
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					VolumeAccess: v1alpha1.VolumeAccessPreferablyLocal,
				},
			}
			clientBuilder = clientBuilder.WithInterceptorFuncs(
				interceptor.Funcs{
					Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
						if _, ok := obj.(*v1alpha1.ReplicatedVolumeReplica); ok {
							return testError
						}
						return c.Create(ctx, obj, opts...)
					},
				},
			)
		})

		It("should return error", func(ctx SpecContext) {
			Expect(cl.Create(ctx, rsc)).To(Succeed(), "should create RSC")
			Expect(cl.Create(ctx, rv)).To(Succeed(), "should create RV")

			Expect(rec.Reconcile(ctx, RequestFor(rv))).Error().To(MatchError(testError), "should return error when Create RVR fails")
		})
	})

	When("Delete Access RVR fails with non-NotFound error", func() {
		var (
			rv        *v1alpha1.ReplicatedVolume
			rsc       *v1alpha1.ReplicatedStorageClass
			accessRVR *v1alpha1.ReplicatedVolumeReplica
			testError error
		)

		BeforeEach(func() {
			testError = errors.New("Delete RVR error")
			rv = &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-volume",
					UID:        "test-uid",
					Finalizers: []string{v1alpha1.ControllerAppFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					ReplicatedStorageClassName: "test-rsc",
					AttachTo:                   []string{}, // No attachTo - will trigger delete
				},
			}
			rsc = &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-rsc",
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					VolumeAccess: v1alpha1.VolumeAccessPreferablyLocal,
				},
			}
			accessRVR = &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "storage.deckhouse.io/v1alpha1",
							Kind:       "ReplicatedVolume",
							Name:       "test-volume",
							UID:        "test-uid",
						},
					},
				},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "test-volume",
					NodeName:             "node-1",
					Type:                 v1alpha1.ReplicaTypeAccess,
				},
			}
			accessRVR.SetNameWithNodeID(10)
			clientBuilder = clientBuilder.WithInterceptorFuncs(
				interceptor.Funcs{
					Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
						if rvr, ok := obj.(*v1alpha1.ReplicatedVolumeReplica); ok && rvr.Spec.Type == v1alpha1.ReplicaTypeAccess {
							return testError
						}
						return c.Delete(ctx, obj, opts...)
					},
				},
			)
		})

		It("should return error", func(ctx SpecContext) {
			Expect(cl.Create(ctx, rsc)).To(Succeed(), "should create RSC")
			Expect(cl.Create(ctx, rv)).To(Succeed(), "should create RV")
			Expect(cl.Create(ctx, accessRVR)).To(Succeed(), "should create Access RVR")

			Expect(rec.Reconcile(ctx, RequestFor(rv))).Error().To(MatchError(testError), "should return error when Delete RVR fails")
		})
	})
})
