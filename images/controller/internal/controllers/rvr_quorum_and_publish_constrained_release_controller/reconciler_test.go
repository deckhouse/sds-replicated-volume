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

package rvrqnpccontroller_test

import (
	"context"
	"fmt"
	"time"

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
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	rvrqnpccontroller "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_quorum_and_publish_constrained_release_controller"
)

var _ = Describe("Reconcile", func() {
	var (
		scheme *runtime.Scheme
		cl     client.WithWatch
		rec    *rvrqnpccontroller.Reconciler
	)

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(v1alpha3.AddToScheme(scheme)).To(Succeed())

		cl = nil
		rec = nil
	})

	JustBeforeEach(func() {
		builder := fake.NewClientBuilder().
			WithScheme(scheme)

		cl = builder.Build()
		rec = rvrqnpccontroller.NewReconciler(cl, logr.New(log.NullLogSink{}), scheme)
	})

	It("returns no error when ReplicatedVolumeReplica does not exist", func(ctx SpecContext) {
		rvr := &v1alpha3.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name: "non-existent",
			},
		}

		result, err := rec.Reconcile(ctx, RequestFor(rvr))
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(reconcile.Result{}))
	})

	It("skips RVR that is not being deleted", func(ctx SpecContext) {
		rvr := &v1alpha3.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name: "rvr-1",
			},
			Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1",
				Type:                 "Diskful",
			},
		}

		Expect(cl.Create(ctx, rvr)).To(Succeed())

		result, err := rec.Reconcile(ctx, RequestFor(rvr))
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(reconcile.Result{}))
	})

	When("RVR is being deleted", func() {
		var (
			rv  *v1alpha3.ReplicatedVolume
			rsc *v1alpha1.ReplicatedStorageClass
			rvr *v1alpha3.ReplicatedVolumeReplica
			now time.Time
		)

		BeforeEach(func() {
			now = time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)

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

			rv = &v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "rv-1",
				},
				Spec: v1alpha3.ReplicatedVolumeSpec{
					ReplicatedStorageClassName: rsc.Name,
				},
				Status: &v1alpha3.ReplicatedVolumeStatus{
					DRBD: &v1alpha3.DRBDResource{
						Config: &v1alpha3.DRBDResourceConfig{
							Quorum: 2,
						},
					},
				},
			}

			rvr = &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rvr-deleting",
					Finalizers: []string{"other-finalizer", "sds-replicated-volume.deckhouse.io/controller"},
				},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: rv.Name,
					NodeName:             "node-1",
					Type:                 "Diskful",
				},
				Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
					ActualType: "Diskful",
					Conditions: []metav1.Condition{
						{
							Type:   "Ready",
							Status: metav1.ConditionTrue,
						},
						{
							Type:   "FullyConnected",
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

			got := &v1alpha3.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), got)).To(Succeed())
			Expect(got.Finalizers).To(ContainElement("sds-replicated-volume.deckhouse.io/controller"))
		})

		When("there are extra replicas", func() {
			var (
				rvr2 *v1alpha3.ReplicatedVolumeReplica
				rvr3 *v1alpha3.ReplicatedVolumeReplica
			)

			BeforeEach(func() {
				baseStatus := &v1alpha3.ReplicatedVolumeReplicaStatus{
					ActualType: "Diskful",
					Conditions: []metav1.Condition{
						{
							Type:   "Ready",
							Status: metav1.ConditionTrue,
						},
						{
							Type:   "FullyConnected",
							Status: metav1.ConditionTrue,
						},
					},
				}

				rvr2 = &v1alpha3.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rvr-2",
					},
					Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-2",
						Type:                 "Diskful",
					},
					Status: baseStatus.DeepCopy(),
				}

				rvr3 = &v1alpha3.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rvr-3",
					},
					Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-3",
						Type:                 "Diskful",
					},
					Status: baseStatus.DeepCopy(),
				}
			})

			JustBeforeEach(func(ctx SpecContext) {
				Expect(cl.Create(ctx, rvr2)).To(Succeed())
				Expect(cl.Create(ctx, rvr3)).To(Succeed())
			})

			When("replication condition is not satisfied", func() {
				BeforeEach(func(SpecContext) {
					rvr2.Status.ActualType = "Access"
					rvr3.Status.ActualType = "Access"
				})

				It("does not remove controller finalizer", func(ctx SpecContext) {
					result, err := rec.Reconcile(ctx, RequestFor(rvr))
					Expect(err).NotTo(HaveOccurred())
					Expect(result).To(Equal(reconcile.Result{}))

					got := &v1alpha3.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), got)).To(Succeed())
					Expect(got.Finalizers).To(ContainElement("sds-replicated-volume.deckhouse.io/controller"))
				})
			})

			When("deleting replica is published", func() {
				JustBeforeEach(func(ctx SpecContext) {
					rvr2.Status.ActualType = "Diskful"
					rvr3.Status.ActualType = "Diskful"
					Expect(cl.Update(ctx, rvr2)).To(Succeed())
					Expect(cl.Update(ctx, rvr3)).To(Succeed())

					rv.Status.PublishedOn = []string{rvr.Spec.NodeName}
					Expect(cl.Update(ctx, rv)).To(Succeed())
				})

				It("does not remove controller finalizer", func(ctx SpecContext) {
					result, err := rec.Reconcile(ctx, RequestFor(rvr))
					Expect(err).NotTo(HaveOccurred())
					Expect(result).To(Equal(reconcile.Result{}))

					got := &v1alpha3.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), got)).To(Succeed())
					Expect(got.Finalizers).To(ContainElement("sds-replicated-volume.deckhouse.io/controller"))
				})
			})

			When("all conditions are satisfied", func() {
				JustBeforeEach(func(ctx SpecContext) {
					rvr2.Status.ActualType = "Diskful"
					rvr3.Status.ActualType = "Diskful"
					Expect(cl.Update(ctx, rvr2)).To(Succeed())
					Expect(cl.Update(ctx, rvr3)).To(Succeed())

					rv.Status.PublishedOn = []string{}
					Expect(cl.Update(ctx, rv)).To(Succeed())

					currentRsc := &v1alpha1.ReplicatedStorageClass{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rsc), currentRsc)).To(Succeed())
					currentRv := &v1alpha3.ReplicatedVolume{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), currentRv)).To(Succeed())
					currentRvr := &v1alpha3.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), currentRvr)).To(Succeed())
					currentRvr2 := &v1alpha3.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr2), currentRvr2)).To(Succeed())
					currentRvr3 := &v1alpha3.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr3), currentRvr3)).To(Succeed())

					currentRsc.ResourceVersion = ""
					currentRv.ResourceVersion = ""
					currentRvr.ResourceVersion = ""
					currentRvr2.ResourceVersion = ""
					currentRvr3.ResourceVersion = ""

					if currentRvr.DeletionTimestamp == nil {
						currentRvr.DeletionTimestamp = &metav1.Time{Time: now}
					}

					builder := fake.NewClientBuilder().
						WithScheme(scheme).
						WithObjects(currentRsc, currentRv, currentRvr, currentRvr2, currentRvr3).
						WithInterceptorFuncs(interceptor.Funcs{
							Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
								if err := c.Get(ctx, key, obj, opts...); err != nil {
									return err
								}
								if rvrObj, ok := obj.(*v1alpha3.ReplicatedVolumeReplica); ok && rvrObj.Name == rvr.Name {
									if rvrObj.DeletionTimestamp == nil {
										rvrObj.DeletionTimestamp = &metav1.Time{Time: now}
									}
								}
								return nil
							},
						})

					cl = builder.Build()
					rec = rvrqnpccontroller.NewReconciler(cl, logr.New(log.NullLogSink{}), scheme)
				})

				It("removes only controller finalizer", func(ctx SpecContext) {
					result, err := rec.Reconcile(ctx, RequestFor(rvr))
					Expect(err).NotTo(HaveOccurred())
					Expect(result).To(Equal(reconcile.Result{}))

					got := &v1alpha3.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), got)).To(Succeed())
					Expect(got.Finalizers).To(ConsistOf("other-finalizer"))
				})
			})
		})

		When("Get or List fail", func() {
			var expectedErr error

			BeforeEach(func() {
				expectedErr = fmt.Errorf("test error")
			})

			It("returns error when getting ReplicatedVolume fails with non-NotFound error", func(ctx SpecContext) {
				builder := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(rvr).
					WithInterceptorFuncs(interceptor.Funcs{
						Get: func(_ context.Context, _ client.WithWatch, _ client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
							return expectedErr
						},
						List: func(_ context.Context, _ client.WithWatch, _ client.ObjectList, _ ...client.ListOption) error {
							return expectedErr
						},
					})

				cl = builder.Build()
				rec = rvrqnpccontroller.NewReconciler(cl, logr.New(log.NullLogSink{}), scheme)

				_, err := rec.Reconcile(ctx, RequestFor(rvr))
				Expect(err).To(MatchError(expectedErr))
			})

			It("returns error when listing ReplicatedVolumeReplica fails", func(ctx SpecContext) {
				builder := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(rsc, rv, rvr).
					WithInterceptorFuncs(interceptor.Funcs{
						Get: func(_ context.Context, _ client.WithWatch, _ client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
							return expectedErr
						},
						List: func(_ context.Context, _ client.WithWatch, _ client.ObjectList, _ ...client.ListOption) error {
							return expectedErr
						},
					})

				cl = builder.Build()
				rec = rvrqnpccontroller.NewReconciler(cl, logr.New(log.NullLogSink{}), scheme)

				_, err := rec.Reconcile(ctx, RequestFor(rvr))
				Expect(err).To(MatchError(expectedErr))
			})
		})
	})
})
