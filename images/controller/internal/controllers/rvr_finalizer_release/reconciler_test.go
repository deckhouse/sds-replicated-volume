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

package rvrfinalizerrelease_test

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	rvrfinalizerrelease "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_finalizer_release"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes"
)

func withRVRIndex(b *fake.ClientBuilder) *fake.ClientBuilder {
	return b.WithIndex(&v1alpha1.ReplicatedVolumeReplica{}, indexes.IndexFieldRVRByReplicatedVolumeName, func(obj client.Object) []string {
		rvr, ok := obj.(*v1alpha1.ReplicatedVolumeReplica)
		if !ok {
			return nil
		}
		if rvr.Spec.ReplicatedVolumeName == "" {
			return nil
		}
		return []string{rvr.Spec.ReplicatedVolumeName}
	})
}

var _ = Describe("Reconcile", func() {
	var (
		scheme *runtime.Scheme
		cl     client.WithWatch
		rec    *rvrfinalizerrelease.Reconciler
	)

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())

		cl = nil
		rec = nil
	})

	JustBeforeEach(func() {
		builder := withRVRIndex(fake.NewClientBuilder().
			WithScheme(scheme))

		cl = builder.Build()
		rec = rvrfinalizerrelease.NewReconciler(cl, logr.New(log.NullLogSink{}), scheme)
	})

	It("returns no error when ReplicatedVolumeReplica does not exist", func(ctx SpecContext) {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name: "non-existent",
			},
		}

		result, err := rec.Reconcile(ctx, RequestFor(rvr))
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(reconcile.Result{}))
	})

	It("skips RVR that is not being deleted", func(ctx SpecContext) {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name: "rvr-1",
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1",
				Type:                 v1alpha1.ReplicaTypeDiskful,
			},
		}

		Expect(cl.Create(ctx, rvr)).To(Succeed())

		result, err := rec.Reconcile(ctx, RequestFor(rvr))
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(reconcile.Result{}))
	})

	When("RVR is being deleted", func() {
		var (
			rv  *v1alpha1.ReplicatedVolume
			rsc *v1alpha1.ReplicatedStorageClass
			rvr *v1alpha1.ReplicatedVolumeReplica
		)

		BeforeEach(func() {
			rsc = &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "rsc-1",
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication:   "Availability",
					StoragePool:   "pool",
					ReclaimPolicy: "Delete",
					VolumeAccess:  "Local",
					Topology:      "Zonal",
				},
			}

			rv = &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "rv-1",
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					ReplicatedStorageClassName: rsc.Name,
				},
				Status: v1alpha1.ReplicatedVolumeStatus{
					DRBD: &v1alpha1.DRBDResource{
						Config: &v1alpha1.DRBDResourceConfig{
							Quorum: 2,
						},
					},
				},
			}

			rvr = &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rvr-deleting",
					Finalizers: []string{"other-finalizer", v1alpha1.ControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: rv.Name,
					NodeName:             "node-1",
					Type:                 v1alpha1.ReplicaTypeDiskful,
				},
				Status: v1alpha1.ReplicatedVolumeReplicaStatus{
					ActualType: v1alpha1.ReplicaTypeDiskful,
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ReplicatedVolumeReplicaCondOnlineType,
							Status: metav1.ConditionTrue,
						},
						{
							Type:   v1alpha1.ReplicatedVolumeReplicaCondIOReadyType,
							Status: metav1.ConditionTrue,
						},
					},
				},
			}
		})

		JustBeforeEach(func(ctx SpecContext) {
			Expect(cl.Create(ctx, rsc)).To(Succeed())
			Expect(cl.Create(ctx, rv)).To(Succeed())
			Expect(cl.Create(ctx, rvr)).To(Succeed())
		})

		It("does not remove controller finalizer when quorum is not satisfied", func(ctx SpecContext) {
			// only deleting RVR exists, so replicasForRV has len 1 and quorum=2 is not satisfied
			result, err := rec.Reconcile(ctx, RequestFor(rvr))
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			got := &v1alpha1.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), got)).To(Succeed())
			Expect(got.Finalizers).To(ContainElement(v1alpha1.ControllerFinalizer))
		})

		When("deleting RVR is the last replica and RV is deleting", func() {
			JustBeforeEach(func(ctx SpecContext) {
				// Ensure RV has controller finalizer so we can observe removal, and keep an extra finalizer
				// so fake client won't delete the object immediately.
				currentRV := &v1alpha1.ReplicatedVolume{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), currentRV)).To(Succeed())
				currentRV.Finalizers = []string{"keep-me", v1alpha1.ControllerFinalizer}
				currentRV.Status.ActuallyAttachedTo = []string{}
				Expect(cl.Update(ctx, currentRV)).To(Succeed())

				// Mark RV deleting (sets DeletionTimestamp in fake client).
				Expect(cl.Delete(ctx, currentRV)).To(Succeed())
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(currentRV), currentRV)).To(Succeed())
				Expect(currentRV.DeletionTimestamp).NotTo(BeNil())

				// Mark RVR deleting (sets DeletionTimestamp in fake client).
				currentRVR := &v1alpha1.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), currentRVR)).To(Succeed())
				Expect(cl.Delete(ctx, currentRVR)).To(Succeed())
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(currentRVR), currentRVR)).To(Succeed())
				Expect(currentRVR.DeletionTimestamp).NotTo(BeNil())
			})

			It("removes controller finalizer from RVR and from RV", func(ctx SpecContext) {
				result, err := rec.Reconcile(ctx, RequestFor(rvr))
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))

				gotRVR := &v1alpha1.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), gotRVR)).To(Succeed())
				Expect(gotRVR.Finalizers).NotTo(ContainElement(v1alpha1.ControllerFinalizer))

				gotRV := &v1alpha1.ReplicatedVolume{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), gotRV)).To(Succeed())
				Expect(gotRV.Finalizers).To(ContainElement("keep-me"))
				Expect(gotRV.Finalizers).NotTo(ContainElement(v1alpha1.ControllerFinalizer))
			})
		})

		When("there are extra replicas", func() {
			var (
				rvr2 *v1alpha1.ReplicatedVolumeReplica
				rvr3 *v1alpha1.ReplicatedVolumeReplica
			)

			BeforeEach(func() {
				baseStatus := &v1alpha1.ReplicatedVolumeReplicaStatus{
					ActualType: v1alpha1.ReplicaTypeDiskful,
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ReplicatedVolumeReplicaCondOnlineType,
							Status: metav1.ConditionTrue,
						},
						{
							Type:   v1alpha1.ReplicatedVolumeReplicaCondIOReadyType,
							Status: metav1.ConditionTrue,
						},
					},
				}

				rvr2 = &v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "rvr-2",
						Finalizers: []string{"other-finalizer", v1alpha1.ControllerFinalizer},
					},
					Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-2",
						Type:                 v1alpha1.ReplicaTypeDiskful,
					},
					Status: *baseStatus.DeepCopy(),
				}

				rvr3 = &v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "rvr-3",
						Finalizers: []string{"other-finalizer", v1alpha1.ControllerFinalizer},
					},
					Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-3",
						Type:                 v1alpha1.ReplicaTypeDiskful,
					},
					Status: *baseStatus.DeepCopy(),
				}
			})

			JustBeforeEach(func(ctx SpecContext) {
				Expect(cl.Create(ctx, rvr2)).To(Succeed())
				Expect(cl.Create(ctx, rvr3)).To(Succeed())
			})

			When("replication condition is not satisfied", func() {
				BeforeEach(func(SpecContext) {
					rvr2.Status.ActualType = v1alpha1.ReplicaTypeAccess
					rvr3.Status.ActualType = v1alpha1.ReplicaTypeAccess
				})

				It("does not remove controller finalizer", func(ctx SpecContext) {
					result, err := rec.Reconcile(ctx, RequestFor(rvr))
					Expect(err).NotTo(HaveOccurred())
					Expect(result).To(Equal(reconcile.Result{}))

					got := &v1alpha1.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), got)).To(Succeed())
					Expect(got.Finalizers).To(ContainElement(v1alpha1.ControllerFinalizer))
				})
			})

			When("deleting replica is attached", func() {
				JustBeforeEach(func(ctx SpecContext) {
					rvr2.Status.ActualType = v1alpha1.ReplicaTypeDiskful
					rvr3.Status.ActualType = v1alpha1.ReplicaTypeDiskful
					Expect(cl.Update(ctx, rvr2)).To(Succeed())
					Expect(cl.Update(ctx, rvr3)).To(Succeed())

					rv.Status.ActuallyAttachedTo = []string{rvr.Spec.NodeName}
					Expect(cl.Update(ctx, rv)).To(Succeed())
				})

				It("does not remove controller finalizer", func(ctx SpecContext) {
					result, err := rec.Reconcile(ctx, RequestFor(rvr))
					Expect(err).NotTo(HaveOccurred())
					Expect(result).To(Equal(reconcile.Result{}))

					got := &v1alpha1.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), got)).To(Succeed())
					Expect(got.Finalizers).To(ContainElement(v1alpha1.ControllerFinalizer))
				})
			})

			When("all conditions are satisfied", func() {
				JustBeforeEach(func(ctx SpecContext) {
					rvr2.Status.ActualType = v1alpha1.ReplicaTypeDiskful
					rvr3.Status.ActualType = v1alpha1.ReplicaTypeDiskful
					Expect(cl.Update(ctx, rvr2)).To(Succeed())
					Expect(cl.Update(ctx, rvr3)).To(Succeed())

					rv.Status.ActuallyAttachedTo = []string{}
					Expect(cl.Update(ctx, rv)).To(Succeed())

					currentRsc := &v1alpha1.ReplicatedStorageClass{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rsc), currentRsc)).To(Succeed())
					currentRv := &v1alpha1.ReplicatedVolume{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), currentRv)).To(Succeed())
					currentRvr := &v1alpha1.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), currentRvr)).To(Succeed())
					currentRvr2 := &v1alpha1.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr2), currentRvr2)).To(Succeed())
					currentRvr3 := &v1alpha1.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr3), currentRvr3)).To(Succeed())

					Expect(currentRsc.Spec.Replication).To(Equal(v1alpha1.ReplicationAvailability))
					Expect(currentRvr.DeletionTimestamp).To(BeNil())
					Expect(currentRvr2.DeletionTimestamp).To(BeNil())
					Expect(currentRvr3.DeletionTimestamp).To(BeNil())
					Expect(currentRv.DeletionTimestamp).To(BeNil())

					// Remove one rvr
					Expect(cl.Delete(ctx, currentRvr)).To(Succeed())
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(currentRvr), currentRvr)).To(Succeed())
					Expect(currentRvr.DeletionTimestamp).NotTo(BeNil())
					Expect(currentRvr.Finalizers).To(HaveLen(2))
					Expect(currentRvr.Finalizers).To(ContainElement("other-finalizer"))
					Expect(currentRvr.Finalizers).To(ContainElement(v1alpha1.ControllerFinalizer))
					Expect(currentRvr2.Finalizers).To(HaveLen(2))
					Expect(currentRvr2.Finalizers).To(ContainElement("other-finalizer"))
					Expect(currentRvr2.Finalizers).To(ContainElement(v1alpha1.ControllerFinalizer))
					Expect(currentRvr3.Finalizers).To(HaveLen(2))
					Expect(currentRvr3.Finalizers).To(ContainElement("other-finalizer"))
					Expect(currentRvr3.Finalizers).To(ContainElement(v1alpha1.ControllerFinalizer))

					// cl = builder.Build()
					// rec = rvrfinalizerrelease.NewReconciler(cl, logr.New(log.NullLogSink{}), scheme)
				})
				It("removes only controller finalizer from rvr that is being deleted", func(ctx SpecContext) {
					result, err := rec.Reconcile(ctx, RequestFor(rvr))
					Expect(err).NotTo(HaveOccurred())
					Expect(result).To(Equal(reconcile.Result{}))

					deletedRvr := &v1alpha1.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), deletedRvr)).To(Succeed())
					Expect(deletedRvr.Finalizers).To(HaveLen(1))
					Expect(deletedRvr.Finalizers).To(ContainElement("other-finalizer"))
					Expect(deletedRvr.Finalizers).NotTo(ContainElement(v1alpha1.ControllerFinalizer))

					notDeletedRvr2 := &v1alpha1.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr2), notDeletedRvr2)).To(Succeed())
					Expect(notDeletedRvr2.Finalizers).To(HaveLen(2))
					Expect(notDeletedRvr2.Finalizers).To(ContainElement("other-finalizer"))
					Expect(notDeletedRvr2.Finalizers).To(ContainElement(v1alpha1.ControllerFinalizer))

					notDeletedRvr3 := &v1alpha1.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr3), notDeletedRvr3)).To(Succeed())
					Expect(notDeletedRvr3.Finalizers).To(HaveLen(2))
					Expect(notDeletedRvr3.Finalizers).To(ContainElement("other-finalizer"))
					Expect(notDeletedRvr3.Finalizers).To(ContainElement(v1alpha1.ControllerFinalizer))
				})
			})
		})

		When("Get or List fail", func() {
			var expectedErr error

			BeforeEach(func() {
				expectedErr = fmt.Errorf("test error")
			})

			It("returns error when getting ReplicatedVolume fails with non-NotFound error", func(ctx SpecContext) {
				builder := withRVRIndex(fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(rvr).
					WithInterceptorFuncs(interceptor.Funcs{
						Get: func(_ context.Context, _ client.WithWatch, _ client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
							return expectedErr
						},
						List: func(_ context.Context, _ client.WithWatch, _ client.ObjectList, _ ...client.ListOption) error {
							return expectedErr
						},
					}))

				cl = builder.Build()
				rec = rvrfinalizerrelease.NewReconciler(cl, logr.New(log.NullLogSink{}), scheme)

				_, err := rec.Reconcile(ctx, RequestFor(rvr))
				Expect(err).To(MatchError(expectedErr))
			})

			It("returns error when listing ReplicatedVolumeReplica fails", func(ctx SpecContext) {
				builder := withRVRIndex(fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(rsc, rv, rvr).
					WithInterceptorFuncs(interceptor.Funcs{
						Get: func(_ context.Context, _ client.WithWatch, _ client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
							return expectedErr
						},
						List: func(_ context.Context, _ client.WithWatch, _ client.ObjectList, _ ...client.ListOption) error {
							return expectedErr
						},
					}))

				cl = builder.Build()
				rec = rvrfinalizerrelease.NewReconciler(cl, logr.New(log.NullLogSink{}), scheme)

				_, err := rec.Reconcile(ctx, RequestFor(rvr))
				Expect(err).To(MatchError(expectedErr))
			})
		})
	})
})
