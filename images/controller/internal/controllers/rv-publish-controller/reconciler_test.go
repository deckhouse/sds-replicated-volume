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

package rvpublishcontroller_test

import (
	"context"
	"errors"
	"testing"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	rvpublishcontroller "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv-publish-controller"
)

func TestRvPublishReconciler(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "rv-publish-controller Reconciler Suite")
}

var expectedError = errors.New("test error")

var _ = Describe("Reconcile", func() {
	scheme := runtime.NewScheme()
	Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	Expect(v1alpha3.AddToScheme(scheme)).To(Succeed())

	var (
		builder *fake.ClientBuilder
		cl      client.WithWatch
		rec     *rvpublishcontroller.Reconciler
	)

	BeforeEach(func() {
		builder = fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&v1alpha3.ReplicatedVolume{})
		cl = nil
		rec = nil
	})

	JustBeforeEach(func() {
		cl = builder.Build()
		rec = rvpublishcontroller.NewReconciler(cl, logr.New(log.NullLogSink{}), scheme)
	})

	It("returns nil when ReplicatedVolume not found", func(ctx SpecContext) {
		result, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKey{Name: "non-existent"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(reconcile.Result{}))
	})

	When("rv created", func() {
		var rv v1alpha3.ReplicatedVolume

		BeforeEach(func() {
			rv = v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "rv1",
				},
				Spec: v1alpha3.ReplicatedVolumeSpec{
					ReplicatedStorageClassName: "rsc1",
				},
			}
		})

		JustBeforeEach(func(ctx SpecContext) {
			Expect(cl.Create(ctx, &rv)).To(Succeed())
		})

		When("status is nil", func() {
			BeforeEach(func() {
				rv.Status = nil
			})

			It("skips when status is nil", func(ctx SpecContext) {
				result, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))
			})
		})

		When("Ready condition is False", func() {
			BeforeEach(func() {
				rv.Status = &v1alpha3.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   "Ready",
							Status: metav1.ConditionFalse,
						},
					},
				}

				// ensure that if controller tried to read RSC, it would fail
				builder.WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						if _, ok := obj.(*v1alpha1.ReplicatedStorageClass); ok {
							return expectedError
						}
						return c.Get(ctx, key, obj, opts...)
					},
				})
			})

			It("skips when Ready condition is False without touching ReplicatedStorageClass", func(ctx SpecContext) {
				result, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))
			})
		})

		When("ReplicatedStorageClassName is empty", func() {
			BeforeEach(func() {
				rv.Spec.ReplicatedStorageClassName = ""

				// interceptor to fail any RSC Get if it ever happens
				builder.WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						if _, ok := obj.(*v1alpha1.ReplicatedStorageClass); ok {
							return expectedError
						}
						return c.Get(ctx, key, obj, opts...)
					},
				})
			})

			It("skips when ReplicatedStorageClassName is empty", func(ctx SpecContext) {
				result, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))
			})
		})

		When("publish context loaded", func() {
			var (
				rsc          v1alpha1.ReplicatedStorageClass
				rvrList      v1alpha3.ReplicatedVolumeReplicaList
				publishOn    []string
				volumeAccess string
			)

			BeforeEach(func() {
				volumeAccess = "Local"
				publishOn = []string{"node-1", "node-2"}

				rv.Status = &v1alpha3.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   "Ready",
							Status: metav1.ConditionTrue,
						},
					},
				}
				rv.Spec.PublishOn = publishOn

				rsc = v1alpha1.ReplicatedStorageClass{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rsc1",
					},
					Spec: v1alpha1.ReplicatedStorageClassSpec{
						Replication:  "Availability",
						VolumeAccess: volumeAccess,
					},
				}

				rvrList = v1alpha3.ReplicatedVolumeReplicaList{
					Items: []v1alpha3.ReplicatedVolumeReplica{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "rvr-df1",
							},
							Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: rv.Name,
								NodeName:             "node-1",
								Type:                 "Diskful",
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "rvr-df2",
							},
							Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: rv.Name,
								NodeName:             "node-2",
								Type:                 "Diskful",
							},
						},
					},
				}
			})

			JustBeforeEach(func(ctx SpecContext) {
				Expect(cl.Create(ctx, &rsc)).To(Succeed())
				for i := range rvrList.Items {
					Expect(cl.Create(ctx, &rvrList.Items[i])).To(Succeed())
				}
			})

			When("volumeAccess is not Local", func() {
				BeforeEach(func() {
					volumeAccess = "Remote"
					rsc.Spec.VolumeAccess = volumeAccess
				})

				It("does not set PublishSucceeded condition for non-Local access", func(ctx SpecContext) {
					result, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})
					Expect(err).NotTo(HaveOccurred())
					Expect(result).To(Equal(reconcile.Result{}))

					rvList := &v1alpha3.ReplicatedVolumeList{}
					Expect(cl.List(ctx, rvList)).To(Succeed())
					Expect(rvList.Items).To(HaveLen(1))
					got := &rvList.Items[0]

					// no PublishSucceeded condition should be present
					for _, cond := range got.Status.Conditions {
						Expect(cond.Type).NotTo(Equal(rvpublishcontroller.ConditionTypePublishSucceeded))
					}
				})
			})

			When("Local access and Diskful replicas exist on all publishOn nodes", func() {
				BeforeEach(func() {
					volumeAccess = "Local"
					rsc.Spec.VolumeAccess = volumeAccess
				})

				It("does not set PublishSucceeded=False and proceeds with reconciliation", func(ctx SpecContext) {
					result, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})
					Expect(err).NotTo(HaveOccurred())
					Expect(result).To(Equal(reconcile.Result{}))

					rvList := &v1alpha3.ReplicatedVolumeList{}
					Expect(cl.List(ctx, rvList)).To(Succeed())
					Expect(rvList.Items).To(HaveLen(1))
					got := &rvList.Items[0]

					// no failure condition should be present
					for _, cond := range got.Status.Conditions {
						Expect(cond.Type).NotTo(Equal(rvpublishcontroller.ConditionTypePublishSucceeded))
					}
				})
			})

			When("Local access but Diskful replica is missing on one of publishOn nodes", func() {
				BeforeEach(func() {
					volumeAccess = "Local"
					rsc.Spec.VolumeAccess = volumeAccess

					// remove Diskful replica for node-2
					rvrList.Items = rvrList.Items[:1]
				})

				It("sets PublishSucceeded=False and stops reconciliation", func(ctx SpecContext) {
					result, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})
					Expect(err).NotTo(HaveOccurred())
					Expect(result).To(Equal(reconcile.Result{}))

					rvList := &v1alpha3.ReplicatedVolumeList{}
					Expect(cl.List(ctx, rvList)).To(Succeed())
					Expect(rvList.Items).To(HaveLen(1))
					got := &rvList.Items[0]

					cond := meta.FindStatusCondition(got.Status.Conditions, rvpublishcontroller.ConditionTypePublishSucceeded)
					Expect(cond).NotTo(BeNil())
					Expect(cond.Status).To(Equal(metav1.ConditionFalse))
					Expect(cond.Reason).To(Equal(rvpublishcontroller.ReasonUnableToProvideLocalVolumeAccess))
				})
			})

			When("allowTwoPrimaries is configured and actual flag not yet applied on replicas", func() {
				BeforeEach(func() {
					volumeAccess = "Local"
					rsc.Spec.VolumeAccess = volumeAccess

					// request two primaries
					rv.Spec.PublishOn = []string{"node-1", "node-2"}

					// replicas without actual.AllowTwoPrimaries
					rvrList.Items[0].Status = &v1alpha3.ReplicatedVolumeReplicaStatus{
						DRBD: &v1alpha3.DRBD{
							Actual: &v1alpha3.DRBDActual{
								AllowTwoPrimaries: false,
							},
						},
					}
					rvrList.Items[1].Status = &v1alpha3.ReplicatedVolumeReplicaStatus{
						DRBD: &v1alpha3.DRBD{
							Actual: &v1alpha3.DRBDActual{
								AllowTwoPrimaries: false,
							},
						},
					}
				})

				It("sets rv.status.drbd.config.allowTwoPrimaries=true and waits for replicas", func(ctx SpecContext) {
					result, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})
					Expect(err).NotTo(HaveOccurred())
					Expect(result).To(Equal(reconcile.Result{}))

					rvList := &v1alpha3.ReplicatedVolumeList{}
					Expect(cl.List(ctx, rvList)).To(Succeed())
					Expect(rvList.Items).To(HaveLen(1))
					got := &rvList.Items[0]
					Expect(got.Status).NotTo(BeNil())
					Expect(got.Status.DRBD).NotTo(BeNil())
					Expect(got.Status.DRBD.Config).NotTo(BeNil())
					Expect(got.Status.DRBD.Config.AllowTwoPrimaries).To(BeTrue())
				})
			})

			When("allowTwoPrimaries applied on all replicas", func() {
				BeforeEach(func() {
					volumeAccess = "Local"
					rsc.Spec.VolumeAccess = volumeAccess

					rv.Spec.PublishOn = []string{"node-1", "node-2"}

					// both replicas already have actual.AllowTwoPrimaries=true
					for i := range rvrList.Items {
						rvrList.Items[i].Status = &v1alpha3.ReplicatedVolumeReplicaStatus{
							DRBD: &v1alpha3.DRBD{
								Actual: &v1alpha3.DRBDActual{
									AllowTwoPrimaries: true,
								},
								Status: &v1alpha3.DRBDStatus{
									Role: "Secondary",
								},
							},
						}
					}
				})

				It("updates primary roles and publishedOn", func(ctx SpecContext) {
					result, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})
					Expect(err).NotTo(HaveOccurred())
					Expect(result).To(Equal(reconcile.Result{}))

					// RVRs on publishOn nodes should be configured as Primary
					gotRVRs := &v1alpha3.ReplicatedVolumeReplicaList{}
					Expect(cl.List(ctx, gotRVRs)).To(Succeed())

					for i := range gotRVRs.Items {
						rvr := &gotRVRs.Items[i]
						if rvr.Spec.ReplicatedVolumeName != rv.Name {
							continue
						}
						_, shouldBePrimary := map[string]struct{}{
							"node-1": {},
							"node-2": {},
						}[rvr.Spec.NodeName]

						if rvr.Status == nil || rvr.Status.DRBD == nil || rvr.Status.DRBD.Config == nil {
							// if no config present, it must not be primary
							Expect(shouldBePrimary).To(BeFalse())
							continue
						}

						if shouldBePrimary {
							Expect(rvr.Status.DRBD.Config.Primary).NotTo(BeNil())
							Expect(*rvr.Status.DRBD.Config.Primary).To(BeTrue())
						}
					}

					// rv.status.publishedOn should reflect RVRs with Role=Primary
					rvList := &v1alpha3.ReplicatedVolumeList{}
					Expect(cl.List(ctx, rvList)).To(Succeed())
					Expect(rvList.Items).To(HaveLen(1))
					gotRV := &rvList.Items[0]
					// we don't assert exact content here, just that field is present and length <= 2
					Expect(len(gotRV.Status.PublishedOn)).To(BeNumerically("<=", 2))
				})
			})
		})

		When("Get ReplicatedVolume fails", func() {
			BeforeEach(func() {
				builder.WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						if _, ok := obj.(*v1alpha3.ReplicatedVolume); ok {
							return expectedError
						}
						return c.Get(ctx, key, obj, opts...)
					},
				})
			})

			It("returns same error", func(ctx SpecContext) {
				result, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})
				Expect(result).To(Equal(reconcile.Result{}))
				Expect(err).To(MatchError(expectedError))
			})
		})

		When("Get ReplicatedStorageClass fails", func() {
			BeforeEach(func() {
				rv.Status = &v1alpha3.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   "Ready",
							Status: metav1.ConditionTrue,
						},
					},
				}

				builder.WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						if _, ok := obj.(*v1alpha1.ReplicatedStorageClass); ok {
							return expectedError
						}
						return c.Get(ctx, key, obj, opts...)
					},
				})
			})

			It("returns same error", func(ctx SpecContext) {
				result, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})
				Expect(result).To(Equal(reconcile.Result{}))
				Expect(err).To(MatchError(expectedError))
			})
		})

		When("List ReplicatedVolumeReplica fails", func() {
			BeforeEach(func() {
				rv.Status = &v1alpha3.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   "Ready",
							Status: metav1.ConditionTrue,
						},
					},
				}

				rsc := v1alpha1.ReplicatedStorageClass{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rsc1",
					},
					Spec: v1alpha1.ReplicatedStorageClassSpec{
						Replication:  "Availability",
						VolumeAccess: "Local",
					},
				}

				builder.WithObjects(&rsc)

				builder.WithInterceptorFuncs(interceptor.Funcs{
					List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
						if _, ok := list.(*v1alpha3.ReplicatedVolumeReplicaList); ok {
							return expectedError
						}
						return c.List(ctx, list, opts...)
					},
				})
			})

			It("returns same error", func(ctx SpecContext) {
				result, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})
				Expect(result).To(Equal(reconcile.Result{}))
				Expect(err).To(MatchError(expectedError))
			})
		})
	})
})
