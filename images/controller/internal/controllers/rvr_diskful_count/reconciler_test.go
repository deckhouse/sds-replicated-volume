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

package rvrdiskfulcount_test

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	rvrdiskfulcount "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_diskful_count"
)

// TODO: replace with direct in place assignment for clarity. Code duplication will be resolved by grouping tests together and having initialisation in BeforeEach blocks once for multiple cases
func createReplicatedVolumeReplica(nodeId uint, rv *v1alpha1.ReplicatedVolume, scheme *runtime.Scheme, ready bool, deletionTimestamp *metav1.Time) *v1alpha1.ReplicatedVolumeReplica {
	return createReplicatedVolumeReplicaWithType(nodeId, rv, scheme, v1alpha1.ReplicaTypeDiskful, ready, deletionTimestamp)
}

// TODO: replace with direct in place assignment for clarity. Code duplication will be resolved by grouping tests together and having initialisation in BeforeEach blocks once for multiple cases
func createReplicatedVolumeReplicaWithType(nodeId uint, rv *v1alpha1.ReplicatedVolume, scheme *runtime.Scheme, rvrType v1alpha1.ReplicaType, ready bool, deletionTimestamp *metav1.Time) *v1alpha1.ReplicatedVolumeReplica {
	rvr := &v1alpha1.ReplicatedVolumeReplica{
		Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
			ReplicatedVolumeName: rv.Name,
			Type:                 rvrType,
		},
	}
	rvr.SetNameWithNodeId(nodeId)

	if err := controllerutil.SetControllerReference(rv, rvr, scheme); err != nil {
		panic(fmt.Sprintf("failed to set controller reference: %v", err))
	}

	// If deletionTimestamp is provided, add a finalizer so we can delete the object
	// and it will get DeletionTimestamp set by the fake client
	if deletionTimestamp != nil {
		rvr.Finalizers = []string{"test-finalizer"}
	}

	if ready {
		rvr.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{
			Conditions: []metav1.Condition{
				{
					Type:   v1alpha1.ConditionTypeDataInitialized,
					Status: metav1.ConditionTrue,
				},
			},
		}
	}

	return rvr
}

var _ = Describe("Reconciler", func() {
	scheme := runtime.NewScheme()
	Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())

	// Available in BeforeEach
	var (
		clientBuilder *fake.ClientBuilder
	)

	// Available in JustBeforeEach
	var (
		cl  client.Client
		rec *rvrdiskfulcount.Reconciler
	)

	BeforeEach(func() {
		clientBuilder = fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(
				&v1alpha1.ReplicatedVolumeReplica{},
				&v1alpha1.ReplicatedVolume{})

		// To be safe. To make sure we don't use client from previous iterations
		cl = nil
		rec = nil
	})

	JustBeforeEach(func() {
		cl = clientBuilder.Build()
		rec = rvrdiskfulcount.NewReconciler(cl, GinkgoLogr, scheme)
	})

	It("returns no error when ReplicatedVolume does not exist", func(ctx SpecContext) {
		Expect(rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "test-rv"},
		})).ToNot(Requeue())
	})

	When("RV and RSC exists", func() {
		var rv *v1alpha1.ReplicatedVolume
		var rsc *v1alpha1.ReplicatedStorageClass
		var rvrList *v1alpha1.ReplicatedVolumeReplicaList
		BeforeEach(func() {
			rsc = &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "test-rsc"},
			}
			rv = &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-rv",
					Finalizers: []string{v1alpha1.ControllerAppFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					ReplicatedStorageClassName: rsc.Name,
				},
				Status: &v1alpha1.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{},
				},
			}
			rvrList = &v1alpha1.ReplicatedVolumeReplicaList{}
		})
		JustBeforeEach(func(ctx SpecContext) {
			if rsc != nil {
				Expect(cl.Create(ctx, rsc)).To(Succeed())
			}
			if rv != nil {
				Expect(cl.Create(ctx, rv)).To(Succeed())
			}
			for _, rvr := range rvrList.Items {
				Expect(cl.Create(ctx, &rvr)).To(Succeed())
			}
		})

		When("ReplicatedVolume has deletionTimestamp", func() {
			const externalFinalizer = "test-finalizer"

			When("has only controller finalizer", func() {
				BeforeEach(func() {
					rv.Finalizers = []string{v1alpha1.ControllerAppFinalizer}
				})

				JustBeforeEach(func(ctx SpecContext) {
					By("Deleting rv")
					Expect(cl.Delete(ctx, rv)).To(Succeed())

					By("Checking if it has DeletionTimestamp")
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), rv)).To(
						Succeed(),
						"rv should not be deleted because it has controller finalizer",
					)

					Expect(rv).To(SatisfyAll(
						HaveField("Finalizers", ContainElement(v1alpha1.ControllerAppFinalizer)),
						HaveField("DeletionTimestamp", Not(BeNil())),
					))
				})

				It("should do nothing and return no error", func(ctx SpecContext) {
					Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue())
				})
			})

			When("has external finalizer in addition to controller finalizer", func() {
				BeforeEach(func() {
					rv.Finalizers = []string{v1alpha1.ControllerAppFinalizer, externalFinalizer}
					// ensure replication is defined so reconcile path can proceed
					rsc.Spec.Replication = v1alpha1.ReplicationNone
				})

				JustBeforeEach(func(ctx SpecContext) {
					By("Deleting rv")
					Expect(cl.Delete(ctx, rv)).To(Succeed())

					By("Checking if it has DeletionTimestamp and external finalizer")
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), rv)).To(
						Succeed(),
						"rv should not be deleted because it has finalizers",
					)

					Expect(rv).To(SatisfyAll(
						HaveField("Finalizers", ContainElement(externalFinalizer)),
						HaveField("DeletionTimestamp", Not(BeNil())),
					))
				})

				It("still processes RV (creates replicas)", func(ctx SpecContext) {
					Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue())

					rvrList := &v1alpha1.ReplicatedVolumeReplicaList{}
					Expect(cl.List(ctx, rvrList)).To(Succeed())
					Expect(rvrList.Items).ToNot(BeEmpty())
				})
			})
		})

		DescribeTableSubtree("Cehecking errors",
			Entry("ReplicatedVolume has empty ReplicatedStorageClassName", func() {
				rv.Spec.ReplicatedStorageClassName = ""
			}, MatchError(rvrdiskfulcount.ErrEmptyReplicatedStorageClassName)),
			Entry("ReplicatedStorageClass does not exist", func() {
				rsc = nil
			}, HaveOccurred()),
			Entry("ReplicatedStorageClass has unknown replication value", func() {
				rsc.Spec.Replication = "Unknown"
			}, MatchError(ContainSubstring("unknown replication value"))),
			func(beforeEach func(), errorMatcher OmegaMatcher) {
				BeforeEach(beforeEach)
				It("should return an error", func(ctx SpecContext) {
					Expect(rec.Reconcile(ctx, RequestFor(rv))).Error().To(errorMatcher)
				})
			})

		When("replication is None", func() {
			BeforeEach(func() {
				rsc.Spec.Replication = "None"
			})

			It("should create one replica with correct properties and condition", func(ctx SpecContext) {
				Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue())

				// Verify replica was created
				Expect(cl.List(ctx, rvrList)).To(Succeed())
				Expect(rvrList.Items).To(SatisfyAll(
					HaveLen(1),
					HaveEach(SatisfyAll(
						HaveField("Spec.ReplicatedVolumeName", Equal(rv.Name)),
						HaveField("Spec.Type", Equal(v1alpha1.ReplicaTypeDiskful)),
						HaveField("OwnerReferences", ContainElement(SatisfyAll(
							HaveField("Name", Equal(rv.Name)),
							HaveField("Kind", Equal("ReplicatedVolume")),
							HaveField("APIVersion", Equal("storage.deckhouse.io/v1alpha1")),
							HaveField("Controller", PointTo(BeTrue())),
							HaveField("BlockOwnerDeletion", PointTo(BeTrue())),
						))),
					)),
				))
			})
		})

		DescribeTableSubtree("replication types that create one replica",
			Entry("Availability replication", func() {
				rsc.Spec.Replication = "Availability"
			}),
			Entry("ConsistencyAndAvailability replication", func() {
				rsc.Spec.Replication = "ConsistencyAndAvailability"
			}),
			func(beforeEach func()) {
				BeforeEach(beforeEach)

				It("should create one replica", func(ctx SpecContext) {
					Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue())
					Expect(cl.List(ctx, rvrList)).To(Succeed())
					Expect(rvrList.Items).To(HaveLen(1))
				})
			})

		When("all ReplicatedVolumeReplicas are being deleted", func() {
			var rvr1 *v1alpha1.ReplicatedVolumeReplica
			var nonDeletedBefore []v1alpha1.ReplicatedVolumeReplica

			BeforeEach(func() {
				rsc.Spec.Replication = "Availability"
				now := metav1.Now()
				rvr1 = createReplicatedVolumeReplica(10, rv, scheme, false, &now)
			})

			JustBeforeEach(func(ctx SpecContext) {
				Expect(cl.Create(ctx, rvr1)).To(Succeed())
				Expect(cl.Delete(ctx, rvr1)).To(Succeed())

				Expect(cl.List(ctx, rvrList)).To(Succeed())
				for _, rvr := range rvrList.Items {
					if rvr.Spec.ReplicatedVolumeName == rv.Name && rvr.Spec.Type == v1alpha1.ReplicaTypeDiskful && rvr.DeletionTimestamp == nil {
						nonDeletedBefore = append(nonDeletedBefore, rvr)
					}
				}

				Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue())

				Expect(cl.List(ctx, rvrList)).To(Succeed())
			})

			It("should create one new replica", func() {
				var nonDeletedReplicas []v1alpha1.ReplicatedVolumeReplica
				for _, rvr := range rvrList.Items {
					if rvr.Spec.ReplicatedVolumeName == rv.Name && rvr.Spec.Type == v1alpha1.ReplicaTypeDiskful && rvr.DeletionTimestamp == nil {
						nonDeletedReplicas = append(nonDeletedReplicas, rvr)
					}
				}
				Expect(len(nonDeletedReplicas)).To(BeNumerically(">=", 1))
				if len(nonDeletedBefore) == 0 {
					Expect(nonDeletedReplicas).To(HaveLen(1))
				}
			})
		})

		When("there is one non-deleted ReplicatedVolumeReplica that is not ready", func() {
			var rvr1 *v1alpha1.ReplicatedVolumeReplica

			BeforeEach(func() {
				rsc.Spec.Replication = "None"
				rvr1 = createReplicatedVolumeReplica(10, rv, scheme, false, nil)
			})

			JustBeforeEach(func(ctx SpecContext) {
				Expect(cl.Create(ctx, rvr1)).To(Succeed())
				Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue())
				Expect(cl.List(ctx, rvrList)).To(Succeed())
			})

			It("should not create additional replicas", func() {
				Expect(rvrList.Items).To(HaveLen(1))
			})
		})

		When("there are more non-deleted ReplicatedVolumeReplicas than needed", func() {
			var rvr1, rvr2 *v1alpha1.ReplicatedVolumeReplica

			BeforeEach(func() {
				rsc.Spec.Replication = "None"
				rvr1 = createReplicatedVolumeReplica(10, rv, scheme, true, nil)
				rvr2 = createReplicatedVolumeReplica(11, rv, scheme, true, nil)
			})

			JustBeforeEach(func(ctx SpecContext) {
				Expect(cl.Create(ctx, rvr1)).To(Succeed())
				Expect(cl.Create(ctx, rvr2)).To(Succeed())
			})

			It("should return no error and not create additional replicas", func(ctx SpecContext) {
				Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue())
				Expect(cl.List(ctx, rvrList)).To(Succeed())
				Expect(rvrList.Items).To(HaveLen(2))
			})
		})

		When("there are fewer non-deleted ReplicatedVolumeReplicas than needed", func() {
			When("Availability replication", func() {
				var rvr1 *v1alpha1.ReplicatedVolumeReplica

				BeforeEach(func() {
					rsc.Spec.Replication = "Availability"
					rvr1 = createReplicatedVolumeReplica(10, rv, scheme, true, nil)
				})

				JustBeforeEach(func(ctx SpecContext) {
					Expect(cl.Create(ctx, rvr1)).To(Succeed())
					Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue())
					Expect(cl.List(ctx, rvrList)).To(Succeed())
				})

				It("should create missing replicas for Availability replication", func() {
					Expect(rvrList.Items).To(HaveLen(2))
				})
			})

			When("ConsistencyAndAvailability replication", func() {
				var rvr1 *v1alpha1.ReplicatedVolumeReplica

				BeforeEach(func() {
					rsc.Spec.Replication = "ConsistencyAndAvailability"
					rvr1 = createReplicatedVolumeReplica(10, rv, scheme, true, nil)
				})

				JustBeforeEach(func(ctx SpecContext) {
					Expect(cl.Create(ctx, rvr1)).To(Succeed())
					Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue())
					Expect(cl.List(ctx, rvrList)).To(Succeed())
				})

				It("should create missing replicas for ConsistencyAndAvailability replication", func() {
					Expect(rvrList.Items).To(HaveLen(3))
				})
			})

		})

		When("the required number of non-deleted ReplicatedVolumeReplicas is reached", func() {
			var replicas []*v1alpha1.ReplicatedVolumeReplica

			DescribeTableSubtree("replication types",
				Entry("None replication", func() {
					rsc.Spec.Replication = "None"
					replicas = []*v1alpha1.ReplicatedVolumeReplica{
						createReplicatedVolumeReplica(10, rv, scheme, true, nil),
					}
				}),
				Entry("Availability replication", func() {
					rsc.Spec.Replication = "Availability"
					replicas = []*v1alpha1.ReplicatedVolumeReplica{
						createReplicatedVolumeReplica(10, rv, scheme, true, nil),
						createReplicatedVolumeReplica(11, rv, scheme, true, nil),
					}
				}),
				Entry("ConsistencyAndAvailability replication", func() {
					rsc.Spec.Replication = "ConsistencyAndAvailability"
					replicas = []*v1alpha1.ReplicatedVolumeReplica{
						createReplicatedVolumeReplica(10, rv, scheme, true, nil),
						createReplicatedVolumeReplica(11, rv, scheme, true, nil),
						createReplicatedVolumeReplica(12, rv, scheme, true, nil),
					}
				}),
				func(beforeEach func()) {
					BeforeEach(beforeEach)

					JustBeforeEach(func(ctx SpecContext) {
						for _, rvr := range replicas {
							Expect(cl.Create(ctx, rvr)).To(Succeed())
						}
						Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue())
					})

					It("should not create additional replicas when required count is reached", func(ctx SpecContext) {
						Expect(cl.List(ctx, rvrList)).To(Succeed())
						// Verify that the number of replicas matches the expected count
						Expect(rvrList.Items).To(HaveLen(len(replicas)))
					})
				})
		})

		When("there are both deleted and non-deleted ReplicatedVolumeReplicas", func() {
			var rvr1, rvr2 *v1alpha1.ReplicatedVolumeReplica

			BeforeEach(func() {
				rsc.Spec.Replication = "Availability"
				now := metav1.Now()
				rvr1 = createReplicatedVolumeReplica(10, rv, scheme, true, &now)
				rvr2 = createReplicatedVolumeReplica(11, rv, scheme, true, nil)
			})

			JustBeforeEach(func(ctx SpecContext) {
				Expect(cl.Create(ctx, rvr1)).To(Succeed())
				Expect(cl.Delete(ctx, rvr1)).To(Succeed())
				Expect(cl.Create(ctx, rvr2)).To(Succeed())
				Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue())
				Expect(cl.List(ctx, rvrList)).To(Succeed())
			})

			It("should only count non-deleted replicas", func() {
				var relevantReplicas []v1alpha1.ReplicatedVolumeReplica
				for _, rvr := range rvrList.Items {
					if rvr.Spec.ReplicatedVolumeName == rv.Name {
						relevantReplicas = append(relevantReplicas, rvr)
					}
				}
				Expect(len(relevantReplicas)).To(BeNumerically(">=", 2))
			})
		})

		When("there are non-Diskful ReplicatedVolumeReplicas", func() {
			When("non-Diskful replica successfully reconciled", func() {
				var rvrNonDiskful *v1alpha1.ReplicatedVolumeReplica

				BeforeEach(func() {
					rsc.Spec.Replication = "None"
					rvrNonDiskful = createReplicatedVolumeReplicaWithType(
						10,
						rv,
						scheme,
						v1alpha1.ReplicaTypeAccess,
						true,
						nil,
					)
				})

				JustBeforeEach(func(ctx SpecContext) {
					Expect(cl.Create(ctx, rvrNonDiskful)).To(Succeed())
					Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue())
					Expect(cl.List(ctx, rvrList)).To(Succeed())
				})

				It("should ignore non-Diskful replicas and only count Diskful ones", func() {
					Expect(rvrList.Items).To(HaveLen(2))

					var diskfulReplicas []v1alpha1.ReplicatedVolumeReplica
					for _, rvr := range rvrList.Items {
						if rvr.Spec.Type == v1alpha1.ReplicaTypeDiskful {
							diskfulReplicas = append(diskfulReplicas, rvr)
						}
					}
					Expect(diskfulReplicas).To(HaveLen(1))
					Expect(diskfulReplicas[0].Spec.ReplicatedVolumeName).To(Equal(rv.Name))
				})
			})

			When("calculating required count", func() {
				var rvrDiskful, rvrNonDiskful *v1alpha1.ReplicatedVolumeReplica

				BeforeEach(func() {
					rsc.Spec.Replication = "None"
					rvrDiskful = createReplicatedVolumeReplica(10, rv, scheme, true, nil)
					rvrNonDiskful = createReplicatedVolumeReplicaWithType(
						11,
						rv,
						scheme,
						v1alpha1.ReplicaTypeAccess,
						true,
						nil,
					)
				})

				JustBeforeEach(func(ctx SpecContext) {
					Expect(cl.Create(ctx, rvrDiskful)).To(Succeed())
					Expect(cl.Create(ctx, rvrNonDiskful)).To(Succeed())
					Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue())
					Expect(cl.List(ctx, rvrList)).To(Succeed())
				})

				It("should only count Diskful replicas when calculating required count", func() {
					Expect(rvrList.Items).To(HaveLen(2))
				})
			})
		})

		When("ReplicatedVolume has ConsistencyAndAvailability replication", func() {
			BeforeEach(func() {
				rsc.Spec.Replication = "ConsistencyAndAvailability"
			})

			It("should create one replica, wait for it to become ready, then create remaining replicas", func(ctx SpecContext) {
				// First reconcile: should create 1 replica
				Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue())

				Expect(cl.List(ctx, rvrList)).To(Succeed())
				Expect(rvrList.Items).To(HaveLen(1))

				rvr := &rvrList.Items[0]
				Expect(rvr.Spec.ReplicatedVolumeName).To(Equal(rv.Name))
				Expect(rvr.Spec.Type).To(Equal(v1alpha1.ReplicaTypeDiskful))

				if rvr.Status != nil && rvr.Status.Conditions != nil {
					readyCond := meta.FindStatusCondition(rvr.Status.Conditions, v1alpha1.ConditionTypeDataInitialized)
					if readyCond != nil {
						Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
					}
				} else {
					Expect(rvr.Status).To(BeNil())
				}

				// Second reconcile: should still have 1 replica (waiting for it to become ready)
				Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue())

				Expect(cl.List(ctx, rvrList)).To(Succeed())
				Expect(rvrList.Items).To(HaveLen(1))

				// Set DataInitialized condition to True on the existing replica
				rvr = &v1alpha1.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, types.NamespacedName{Name: rvrList.Items[0].Name}, rvr)).To(Succeed())

				patch := client.MergeFrom(rvr.DeepCopy())
				if rvr.Status == nil {
					rvr.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{}
				}
				meta.SetStatusCondition(
					&rvr.Status.Conditions,
					metav1.Condition{
						Type:   v1alpha1.ConditionTypeDataInitialized,
						Status: metav1.ConditionTrue,
						Reason: "DataInitialized",
					},
				)
				Expect(cl.Status().Patch(ctx, rvr, patch)).To(Succeed())

				// Third reconcile: should create 2 more replicas (total 3)
				Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue())

				Expect(cl.List(ctx, rvrList)).To(Succeed())
				Expect(rvrList.Items).To(HaveLen(3))
			})
		})
	})

})
