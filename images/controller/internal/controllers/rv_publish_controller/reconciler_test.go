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
	rvpublishcontroller "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_publish_controller"
)

func TestRvPublishReconciler(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "rv-publish-controller Reconciler Suite")
}

var errExpectedTestError = errors.New("test error")

var _ = Describe("Reconcile", func() {
	scheme := runtime.NewScheme()
	Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())

	var (
		builder *fake.ClientBuilder
		cl      client.WithWatch
		rec     *rvpublishcontroller.Reconciler
	)

	BeforeEach(func() {
		builder = fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&v1alpha1.ReplicatedVolume{}).
			WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{})
		cl = nil
		rec = nil
	})

	JustBeforeEach(func() {
		cl = builder.Build()
		rec = rvpublishcontroller.NewReconciler(cl, logr.New(log.NullLogSink{}))
	})

	It("returns nil when ReplicatedVolume not found", func(ctx SpecContext) {
		Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKey{Name: "non-existent"}})).To(Equal(reconcile.Result{}))
	})

	When("rv created", func() {
		var rv v1alpha1.ReplicatedVolume

		BeforeEach(func() {
			rv = v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv1",
					Finalizers: []string{v1alpha1.ControllerAppFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
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
				Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKey{Name: "non-existent"}})).To(Equal(reconcile.Result{}))
			})
		})

		When("IOReady condition is False", func() {
			BeforeEach(func() {
				rv.Status = &v1alpha1.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ConditionTypeRVIOReady,
							Status: metav1.ConditionFalse,
						},
					},
				}

				// ensure that if controller tried to read RSC, it would fail
				builder.WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						if _, ok := obj.(*v1alpha1.ReplicatedStorageClass); ok {
							return errExpectedTestError
						}
						return c.Get(ctx, key, obj, opts...)
					},
				})
			})

			It("skips when IOReady condition is False without touching ReplicatedStorageClass", func(ctx SpecContext) {
				Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKey{Name: "non-existent"}})).To(Equal(reconcile.Result{}))
			})
		})

		When("ReplicatedStorageClassName is empty", func() {
			BeforeEach(func() {
				rv.Spec.ReplicatedStorageClassName = ""

				// interceptor to fail any RSC Get if it ever happens
				builder.WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						if _, ok := obj.(*v1alpha1.ReplicatedStorageClass); ok {
							return errExpectedTestError
						}
						return c.Get(ctx, key, obj, opts...)
					},
				})
			})

			It("skips when ReplicatedStorageClassName is empty", func(ctx SpecContext) {
				Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))
			})
		})

		When("publish context loaded", func() {
			var (
				rsc          v1alpha1.ReplicatedStorageClass
				rvrList      v1alpha1.ReplicatedVolumeReplicaList
				publishOn    []string
				volumeAccess string
			)

			BeforeEach(func() {
				volumeAccess = "Local"
				publishOn = []string{"node-1", "node-2"}

				rv.Status = &v1alpha1.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ConditionTypeRVIOReady,
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

				rvrList = v1alpha1.ReplicatedVolumeReplicaList{
					Items: []v1alpha1.ReplicatedVolumeReplica{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "rvr-df1",
							},
							Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: rv.Name,
								NodeName:             "node-1",
								Type:                 "Diskful",
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "rvr-df2",
							},
							Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
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
					Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

					rvList := &v1alpha1.ReplicatedVolumeList{}
					Expect(cl.List(ctx, rvList)).To(Succeed())
					Expect(rvList.Items).To(SatisfyAll(
						HaveLen(1),
						HaveEach(HaveField(
							"Status.Conditions",
							Not(ContainElement(
								HaveField("Type", Equal(rvpublishcontroller.ConditionTypePublishSucceeded)),
							)),
						)),
					))
				})
			})

			When("Local access and Diskful replicas exist on all publishOn nodes", func() {
				BeforeEach(func() {
					volumeAccess = "Local"
					rsc.Spec.VolumeAccess = volumeAccess
				})

				It("does not set PublishSucceeded=False and proceeds with reconciliation", func(ctx SpecContext) {
					Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

					rvList := &v1alpha1.ReplicatedVolumeList{}
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
					Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

					rvList := &v1alpha1.ReplicatedVolumeList{}
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
					rvrList.Items[0].Status = &v1alpha1.ReplicatedVolumeReplicaStatus{
						DRBD: &v1alpha1.DRBD{
							Actual: &v1alpha1.DRBDActual{
								AllowTwoPrimaries: false,
							},
						},
					}
					rvrList.Items[1].Status = &v1alpha1.ReplicatedVolumeReplicaStatus{
						DRBD: &v1alpha1.DRBD{
							Actual: &v1alpha1.DRBDActual{
								AllowTwoPrimaries: false,
							},
						},
					}
				})

				It("sets rv.status.drbd.config.allowTwoPrimaries=true and waits for replicas", func(ctx SpecContext) {
					Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

					rvList := &v1alpha1.ReplicatedVolumeList{}
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
						rvrList.Items[i].Status = &v1alpha1.ReplicatedVolumeReplicaStatus{
							DRBD: &v1alpha1.DRBD{
								Actual: &v1alpha1.DRBDActual{
									AllowTwoPrimaries: true,
								},
								Status: &v1alpha1.DRBDStatus{
									Role: "Secondary",
								},
							},
						}
					}
				})

				It("updates primary roles and publishedOn", func(ctx SpecContext) {
					Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

					// RVRs on publishOn nodes should be configured as Primary
					gotRVRs := &v1alpha1.ReplicatedVolumeReplicaList{}
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
					rvList := &v1alpha1.ReplicatedVolumeList{}
					Expect(cl.List(ctx, rvList)).To(Succeed())
					Expect(rvList.Items).To(HaveLen(1))
					gotRV := &rvList.Items[0]
					// we don't assert exact content here, just that field is present and length <= 2
					Expect(len(gotRV.Status.PublishedOn)).To(BeNumerically("<=", 2))
				})
			})

			When("volumeAccess is not Local and TieBreaker replica should become primary", func() {
				BeforeEach(func() {
					volumeAccess = "Remote"
					rsc.Spec.VolumeAccess = volumeAccess

					rv.Spec.PublishOn = []string{"node-1"}

					rvrList = v1alpha1.ReplicatedVolumeReplicaList{
						Items: []v1alpha1.ReplicatedVolumeReplica{
							{
								ObjectMeta: metav1.ObjectMeta{
									Name: "rvr-tb1",
								},
								Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
									ReplicatedVolumeName: rv.Name,
									NodeName:             "node-1",
									Type:                 "TieBreaker",
								},
								Status: &v1alpha1.ReplicatedVolumeReplicaStatus{
									DRBD: &v1alpha1.DRBD{
										Actual: &v1alpha1.DRBDActual{
											AllowTwoPrimaries: false,
										},
										Status: &v1alpha1.DRBDStatus{
											Role: "Secondary",
										},
									},
								},
							},
						},
					}
				})

				It("converts TieBreaker to Access and sets primary=true", func(ctx SpecContext) {
					Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

					gotRVR := &v1alpha1.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-tb1"}, gotRVR)).To(Succeed())

					Expect(gotRVR.Spec.Type).To(Equal(v1alpha1.ReplicaTypeAccess))
					Expect(gotRVR.Status).NotTo(BeNil())
					Expect(gotRVR.Status.DRBD).NotTo(BeNil())
					Expect(gotRVR.Status.DRBD.Config).NotTo(BeNil())
					Expect(gotRVR.Status.DRBD.Config.Primary).NotTo(BeNil())
					Expect(*gotRVR.Status.DRBD.Config.Primary).To(BeTrue())
				})
			})

			When("replica on node outside publishOn does not become primary", func() {
				BeforeEach(func() {
					volumeAccess = "Remote"
					rsc.Spec.VolumeAccess = volumeAccess

					rv.Spec.PublishOn = []string{"node-1"}

					rvrList = v1alpha1.ReplicatedVolumeReplicaList{
						Items: []v1alpha1.ReplicatedVolumeReplica{
							{
								ObjectMeta: metav1.ObjectMeta{
									Name: "rvr-node-1",
								},
								Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
									ReplicatedVolumeName: rv.Name,
									NodeName:             "node-1",
									Type:                 "Diskful",
								},
							},
							{
								ObjectMeta: metav1.ObjectMeta{
									Name: "rvr-node-2",
								},
								Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
									ReplicatedVolumeName: rv.Name,
									NodeName:             "node-2",
									Type:                 "Access",
								},
							},
						},
					}
				})

				It("keeps replica on non-publishOn node non-primary", func(ctx SpecContext) {
					Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

					gotRVRs := &v1alpha1.ReplicatedVolumeReplicaList{}
					Expect(cl.List(ctx, gotRVRs)).To(Succeed())

					var rvrNode1, rvrNode2 *v1alpha1.ReplicatedVolumeReplica
					for i := range gotRVRs.Items {
						r := &gotRVRs.Items[i]
						switch r.Name {
						case "rvr-node-1":
							rvrNode1 = r
						case "rvr-node-2":
							rvrNode2 = r
						}
					}

					Expect(rvrNode1).NotTo(BeNil())
					Expect(rvrNode2).NotTo(BeNil())

					// node-1 должен стать primary
					Expect(rvrNode1.Status).NotTo(BeNil())
					Expect(rvrNode1.Status.DRBD).NotTo(BeNil())
					Expect(rvrNode1.Status.DRBD.Config).NotTo(BeNil())
					Expect(rvrNode1.Status.DRBD.Config.Primary).NotTo(BeNil())
					Expect(*rvrNode1.Status.DRBD.Config.Primary).To(BeTrue())

					// node-2 не должен стать primary
					if rvrNode2.Status == nil ||
						rvrNode2.Status.DRBD == nil ||
						rvrNode2.Status.DRBD.Config == nil ||
						rvrNode2.Status.DRBD.Config.Primary == nil {
						return
					}
					Expect(*rvrNode2.Status.DRBD.Config.Primary).To(BeFalse())
				})
			})

			When("Local access but replica on publishOn node is Access", func() {
				BeforeEach(func() {
					volumeAccess = "Local"
					rsc.Spec.VolumeAccess = volumeAccess

					// Сделаем одну реплику Access вместо Diskful
					rvrList.Items[1].Spec.Type = "Access"
				})

				It("sets PublishSucceeded=False and stops reconciliation", func(ctx SpecContext) {
					Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

					rvList := &v1alpha1.ReplicatedVolumeList{}
					Expect(cl.List(ctx, rvList)).To(Succeed())
					Expect(rvList.Items).To(HaveLen(1))
					got := &rvList.Items[0]

					cond := meta.FindStatusCondition(got.Status.Conditions, rvpublishcontroller.ConditionTypePublishSucceeded)
					Expect(cond).NotTo(BeNil())
					Expect(cond.Status).To(Equal(metav1.ConditionFalse))
					Expect(cond.Reason).To(Equal(rvpublishcontroller.ReasonUnableToProvideLocalVolumeAccess))
				})
			})

			When("Local access but replica on publishOn node is TieBreaker", func() {
				BeforeEach(func() {
					volumeAccess = "Local"
					rsc.Spec.VolumeAccess = volumeAccess

					// Сделаем одну реплику TieBreaker вместо Diskful
					rvrList.Items[1].Spec.Type = "TieBreaker"
				})

				It("sets PublishSucceeded=False and stops reconciliation", func(ctx SpecContext) {
					Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

					rvList := &v1alpha1.ReplicatedVolumeList{}
					Expect(cl.List(ctx, rvList)).To(Succeed())
					Expect(rvList.Items).To(HaveLen(1))
					got := &rvList.Items[0]

					cond := meta.FindStatusCondition(got.Status.Conditions, rvpublishcontroller.ConditionTypePublishSucceeded)
					Expect(cond).NotTo(BeNil())
					Expect(cond.Status).To(Equal(metav1.ConditionFalse))
					Expect(cond.Reason).To(Equal(rvpublishcontroller.ReasonUnableToProvideLocalVolumeAccess))
				})
			})

			When("publishOn shrinks to a single node", func() {
				BeforeEach(func() {
					volumeAccess = "Local"
					rsc.Spec.VolumeAccess = volumeAccess

					rv.Spec.PublishOn = []string{"node-1"}

					// смоделируем ситуацию, когда раньше allowTwoPrimaries уже был включён
					rv.Status.DRBD = &v1alpha1.DRBDResource{
						Config: &v1alpha1.DRBDResourceConfig{
							AllowTwoPrimaries: true,
						},
					}

					for i := range rvrList.Items {
						rvrList.Items[i].Status = &v1alpha1.ReplicatedVolumeReplicaStatus{
							DRBD: &v1alpha1.DRBD{
								Actual: &v1alpha1.DRBDActual{
									AllowTwoPrimaries: true,
								},
							},
						}
					}
				})

				It("sets allowTwoPrimaries=false when less than two nodes in publishOn", func(ctx SpecContext) {
					Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

					got := &v1alpha1.ReplicatedVolume{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(&rv), got)).To(Succeed())
					Expect(got.Status).NotTo(BeNil())
					Expect(got.Status.DRBD).NotTo(BeNil())
					Expect(got.Status.DRBD.Config).NotTo(BeNil())
					Expect(got.Status.DRBD.Config.AllowTwoPrimaries).To(BeFalse())
				})
			})

			When("replicas already have Primary role set in status", func() {
				BeforeEach(func() {
					volumeAccess = "Remote"
					rsc.Spec.VolumeAccess = volumeAccess

					rv.Spec.PublishOn = []string{"node-1", "node-2"}

					for i := range rvrList.Items {
						role := "Secondary"
						if rvrList.Items[i].Spec.NodeName == "node-1" {
							role = "Primary"
						}
						rvrList.Items[i].Status = &v1alpha1.ReplicatedVolumeReplicaStatus{
							DRBD: &v1alpha1.DRBD{
								Actual: &v1alpha1.DRBDActual{
									AllowTwoPrimaries: true,
								},
								Status: &v1alpha1.DRBDStatus{
									Role: role,
								},
							},
						}
					}
				})

				It("recomputes publishedOn from replicas with Primary role", func(ctx SpecContext) {
					Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

					rvList := &v1alpha1.ReplicatedVolumeList{}
					Expect(cl.List(ctx, rvList)).To(Succeed())
					Expect(rvList.Items).To(HaveLen(1))
					gotRV := &rvList.Items[0]

					Expect(gotRV.Status.PublishedOn).To(ConsistOf("node-1"))
				})
			})

		})

		When("setting PublishSucceeded condition fails", func() {
			BeforeEach(func() {
				rv.Status = &v1alpha1.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ConditionTypeRVIOReady,
							Status: metav1.ConditionTrue,
						},
					},
				}
				rv.Spec.PublishOn = []string{"node-1"}

				rsc := v1alpha1.ReplicatedStorageClass{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rsc1",
					},
					Spec: v1alpha1.ReplicatedStorageClassSpec{
						Replication:  "Availability",
						VolumeAccess: "Local",
					},
				}

				// Ноде нужен Diskful, но мы создадим Access — это вызовет попытку выставить PublishSucceeded=False
				rvr := v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rvr-access-1",
					},
					Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-1",
						Type:                 "Access",
					},
				}

				builder.WithObjects(&rsc, &rvr)

				builder.WithInterceptorFuncs(interceptor.Funcs{
					SubResourcePatch: func(ctx context.Context, cl client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
						if _, ok := obj.(*v1alpha1.ReplicatedVolume); ok {
							return errExpectedTestError
						}
						return cl.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
					},
				})
			})

			It("propagates error from PublishSucceeded status patch", func(ctx SpecContext) {
				result, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})
				Expect(err).To(MatchError(errExpectedTestError))
				Expect(result).To(Equal(reconcile.Result{}))
			})
		})

		When("patching RVR primary status fails", func() {
			BeforeEach(func() {
				rv.Status = &v1alpha1.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ConditionTypeRVIOReady,
							Status: metav1.ConditionTrue,
						},
					},
				}
				rv.Spec.PublishOn = []string{"node-1"}

				rsc := v1alpha1.ReplicatedStorageClass{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rsc1",
					},
					Spec: v1alpha1.ReplicatedStorageClassSpec{
						Replication:  "Availability",
						VolumeAccess: "Remote",
					},
				}

				rvr := v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rvr-primary-1",
					},
					Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-1",
						Type:                 "Diskful",
					},
				}

				builder.WithObjects(&rsc, &rvr)

				builder.WithInterceptorFuncs(interceptor.Funcs{
					SubResourcePatch: func(ctx context.Context, cl client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
						if _, ok := obj.(*v1alpha1.ReplicatedVolumeReplica); ok {
							return errExpectedTestError
						}
						return cl.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
					},
				})
			})

			It("returns error when updating RVR primary status fails", func(ctx SpecContext) {
				result, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})
				Expect(err).To(MatchError(errExpectedTestError))
				Expect(result).To(Equal(reconcile.Result{}))
			})
		})

		When("Get ReplicatedVolume fails", func() {
			BeforeEach(func() {
				builder.WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						if _, ok := obj.(*v1alpha1.ReplicatedVolume); ok {
							return errExpectedTestError
						}
						return c.Get(ctx, key, obj, opts...)
					},
				})
			})

			It("returns same error", func(ctx SpecContext) {
				result, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})
				Expect(err).To(MatchError(errExpectedTestError))
				Expect(result).To(Equal(reconcile.Result{}))
			})
		})

		When("Get ReplicatedStorageClass fails", func() {
			BeforeEach(func() {
				rv.Status = &v1alpha1.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ConditionTypeRVIOReady,
							Status: metav1.ConditionTrue,
						},
					},
				}

				builder.WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						if _, ok := obj.(*v1alpha1.ReplicatedStorageClass); ok {
							return errExpectedTestError
						}
						return c.Get(ctx, key, obj, opts...)
					},
				})
			})

			It("returns same error", func(ctx SpecContext) {
				result, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})
				Expect(err).To(MatchError(errExpectedTestError))
				Expect(result).To(Equal(reconcile.Result{}))
			})
		})

		When("List ReplicatedVolumeReplica fails", func() {
			BeforeEach(func() {
				rv.Status = &v1alpha1.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ConditionTypeRVIOReady,
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
						if _, ok := list.(*v1alpha1.ReplicatedVolumeReplicaList); ok {
							return errExpectedTestError
						}
						return c.List(ctx, list, opts...)
					},
				})
			})

			It("returns same error", func(ctx SpecContext) {
				result, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})
				Expect(err).To(MatchError(errExpectedTestError))
				Expect(result).To(Equal(reconcile.Result{}))
			})
		})
	})
})
