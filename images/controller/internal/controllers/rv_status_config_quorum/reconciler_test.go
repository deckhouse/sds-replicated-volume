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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	v1alpha3 "github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	rvquorumcontroller "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_status_config_quorum"
)

var _ = Describe("Reconciler", func() {
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	_ = v1alpha3.AddToScheme(scheme)

	var clientBuilder *fake.ClientBuilder

	var cl client.Client
	var rec *rvquorumcontroller.Reconciler

	BeforeEach(func() {
		cl = nil
		rec = nil
		clientBuilder = fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(
				&v1alpha3.ReplicatedVolumeReplica{},
				&v1alpha3.ReplicatedVolume{})
	})

	JustBeforeEach(func() {
		cl = clientBuilder.Build()
		rec = rvquorumcontroller.NewReconciler(
			cl,
			nil,
			GinkgoLogr,
		)
		clientBuilder = nil
	})

	It("returns no error when ReplicatedVolume does not exist", func(ctx SpecContext) {
		Expect(rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "test-rv"},
		})).NotTo(Requeue())
	})

	When("with ReplicatedVolume and ReplicatedVolumeReplicas", func() {
		var rv *v1alpha3.ReplicatedVolume
		var rsc *v1alpha1.ReplicatedStorageClass
		var rvrList []*v1alpha3.ReplicatedVolumeReplica
		BeforeEach(func() {
			rsc = &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "test-rsc"},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					Replication: v1alpha3.ReplicationConsistencyAndAvailability,
				},
			}
			rv = &v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "test-rv"},
				Spec: v1alpha3.ReplicatedVolumeSpec{
					ReplicatedStorageClassName: rsc.Name,
				},
				Status: &v1alpha3.ReplicatedVolumeStatus{
					Conditions:          []metav1.Condition{},
					DiskfulReplicaCount: "3/3",
				},
			}
			rvrList = make([]*v1alpha3.ReplicatedVolumeReplica, 0, 5)
			for i, rvrType := range []string{"Diskful", "Diskful", "Diskful", "Access", "Access"} {
				rvrList = append(rvrList, &v1alpha3.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name: fmt.Sprintf("rvr-%d", i+1),
						OwnerReferences: []metav1.OwnerReference{
							*metav1.NewControllerRef(rv, v1alpha3.SchemeGroupVersion.WithKind("ReplicatedVolume")),
						},
					},
					Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             fmt.Sprintf("node-%d", i+1),
						Type:                 rvrType,
					},
				})
			}
		})

		JustBeforeEach(func(ctx SpecContext) {
			Expect(cl.Create(ctx, rsc)).To(Succeed())
			Expect(cl.Create(ctx, rv)).To(Succeed())
			for _, rvr := range rvrList {
				Expect(cl.Create(ctx, rvr)).To(Succeed())
			}
		})

		DescribeTableSubtree("When any change disabled and RV is not ready",
			func(beforeEach func()) {
				var isActive bool
				BeforeEach(func() {
					beforeEach()
					isActive = false
					clientBuilder.WithInterceptorFuncs(FailOnAnyChange(func() bool { return isActive }))
				})
				JustBeforeEach(func() {
					isActive = true
				})
				It("should not requeue", func(ctx SpecContext) {
					Expect(rec.Reconcile(ctx, reconcile.Request{
						NamespacedName: client.ObjectKeyFromObject(rv),
					})).NotTo(Requeue())
				})
			},
			Entry("because Status is nil", func() {
				rv.Status = nil
			}),
			Entry("because Conditions is nil", func() {
				if rv.Status == nil {
					rv.Status = &v1alpha3.ReplicatedVolumeStatus{}
				}
				rv.Status.Conditions = nil
			}),
			Entry("because Conditions is empty", func() {
				rv.Status.Conditions = []metav1.Condition{}
			}),
			Entry("because Configured is false", func() {
				rv.Status.Conditions = []metav1.Condition{
					{
						Type:   v1alpha3.ConditionTypeConfigured,
						Status: metav1.ConditionFalse,
					},
				}
			}),
			Entry("because DiskfulReplicaCount is invalid", func() {
				rv.Status.DiskfulReplicaCount = "invalid"
			}),
			Entry("because DiskfulReplicaCount shows not enough replicas", func() {
				rv.Status.DiskfulReplicaCount = "1/3"
			}),
		)

		When("ReplicatedVolume is ready", func() {
			BeforeEach(func() {
				rv.Status.Conditions = []metav1.Condition{
					{
						Type:   v1alpha3.ConditionTypeConfigured,
						Status: metav1.ConditionTrue,
					},
				}
				// Initialize Status.DRBD.Config to ensure patch works correctly
				rv.Status.DRBD = &v1alpha3.DRBDResource{
					Config: &v1alpha3.DRBDResourceConfig{},
				}
			})

			It("should reconcile successfully when RV is ready with RVRs", func(ctx SpecContext) {
				Expect(rec.Reconcile(ctx, reconcile.Request{
					NamespacedName: client.ObjectKeyFromObject(rv),
				})).NotTo(Requeue())

				// Verify finalizers were added to RVRs
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvrList[0]), rvrList[0])).To(Succeed())
			})

			It("should handle multiple replicas with diskful and diskless", func(ctx SpecContext) {
				Expect(rec.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name:      "test-rv",
						Namespace: "",
					},
				})).NotTo(Requeue())

				// Verify all RVRs got finalizers
				for _, name := range []string{"rvr-1", "rvr-2", "rvr-3", "rvr-4"} {
					rvr := &v1alpha3.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, types.NamespacedName{Name: name}, rvr)).To(Succeed())
				}
			})

			When("single diskful replica", func() {
				BeforeEach(func() {
					rvrList = rvrList[:1]
					rv.Status.DiskfulReplicaCount = "1/1"
				})

				It("should not set quorum when diskfulCount <= 1", func(ctx SpecContext) {
					// rvrList[0] is already created in JustBeforeEach

					Expect(rec.Reconcile(ctx, reconcile.Request{
						NamespacedName: types.NamespacedName{
							Name:      "test-rv",
							Namespace: "",
						},
					})).NotTo(Requeue())

					// Verify quorum is 0 (not set)
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), rv)).To(Succeed())
					Expect(rv).To(SatisfyAll(
						HaveField("Status.DRBD.Config.Quorum", Equal(byte(0))),
						HaveField("Status.DRBD.Config.QuorumMinimumRedundancy", Equal(byte(0))),
					))
				})
			})

			DescribeTableSubtree("checking quorum calculation with ConsistencyAndAvailability",
				func(diskfulCount, all int) {
					BeforeEach(func() {
						rsc.Spec.Replication = v1alpha3.ReplicationConsistencyAndAvailability
						rv.Status.DiskfulReplicaCount = fmt.Sprintf("%d/%d", diskfulCount, diskfulCount)
						By(fmt.Sprintf("creating %d RVRs with %d diskfull", all, diskfulCount))
						rvrList = make([]*v1alpha3.ReplicatedVolumeReplica, 0, all)
						for i := 0; i < all; i++ {
							rvrType := "Diskful"
							if i >= diskfulCount {
								rvrType = "Access"
							}
							rvrList = append(rvrList, &v1alpha3.ReplicatedVolumeReplica{
								ObjectMeta: metav1.ObjectMeta{
									Name: fmt.Sprintf("rvr-%d", i+1),
									OwnerReferences: []metav1.OwnerReference{
										*metav1.NewControllerRef(rv, v1alpha3.SchemeGroupVersion.WithKind("ReplicatedVolume")),
									},
								},
								Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
									ReplicatedVolumeName: "test-rv",
									NodeName:             fmt.Sprintf("node-%d", i+1),
									Type:                 rvrType,
								},
							})
						}
					})

					It("should calculate correct quorum and qmr values", func(ctx SpecContext) {
						Expect(rec.Reconcile(ctx, reconcile.Request{
							NamespacedName: types.NamespacedName{Name: "test-rv"},
						})).NotTo(Requeue())

						Expect(cl.Get(ctx, types.NamespacedName{Name: "test-rv"}, rv)).To(Succeed())

						expectedQuorum, expectedQmr := rvquorumcontroller.CalculateQuorum(diskfulCount, all, v1alpha3.ReplicationConsistencyAndAvailability)
						Expect(rv).To(SatisfyAll(
							HaveField("Status.DRBD.Config.Quorum", Equal(expectedQuorum)),
							HaveField("Status.DRBD.Config.QuorumMinimumRedundancy", Equal(expectedQmr)),
						))
					})
				},
				func(diskfulCount, all int) string {
					expectedQuorum, expectedQmr := rvquorumcontroller.CalculateQuorum(diskfulCount, all, v1alpha3.ReplicationConsistencyAndAvailability)
					return fmt.Sprintf("diskfulCount=%d, all=%d -> quorum=%d, qmr=%d", diskfulCount, all, expectedQuorum, expectedQmr)
				},
				Entry(nil, 2, 2),
				Entry(nil, 3, 3),
				Entry(nil, 4, 4),
				Entry(nil, 5, 5),
				Entry(nil, 2, 3),
				Entry(nil, 3, 5),
				Entry(nil, 7, 7),
			)

			DescribeTableSubtree("checking quorum calculation with Availability (QMR should be 0)",
				func(diskfulCount, all int) {
					BeforeEach(func() {
						rsc.Spec.Replication = v1alpha3.ReplicationAvailability
						rv.Status.DiskfulReplicaCount = fmt.Sprintf("%d/%d", diskfulCount, diskfulCount)
						By(fmt.Sprintf("creating %d RVRs with %d diskfull", all, diskfulCount))
						rvrList = make([]*v1alpha3.ReplicatedVolumeReplica, 0, all)
						for i := 0; i < all; i++ {
							rvrType := "Diskful"
							if i >= diskfulCount {
								rvrType = "Access"
							}
							rvrList = append(rvrList, &v1alpha3.ReplicatedVolumeReplica{
								ObjectMeta: metav1.ObjectMeta{
									Name: fmt.Sprintf("rvr-%d", i+1),
									OwnerReferences: []metav1.OwnerReference{
										*metav1.NewControllerRef(rv, v1alpha3.SchemeGroupVersion.WithKind("ReplicatedVolume")),
									},
								},
								Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
									ReplicatedVolumeName: "test-rv",
									NodeName:             fmt.Sprintf("node-%d", i+1),
									Type:                 rvrType,
								},
							})
						}
					})

					It("should calculate correct quorum but QMR should be 0", func(ctx SpecContext) {
						Expect(rec.Reconcile(ctx, reconcile.Request{
							NamespacedName: types.NamespacedName{Name: "test-rv"},
						})).NotTo(Requeue())

						Expect(cl.Get(ctx, types.NamespacedName{Name: "test-rv"}, rv)).To(Succeed())

						expectedQuorum, _ := rvquorumcontroller.CalculateQuorum(diskfulCount, all, v1alpha3.ReplicationAvailability)
						Expect(rv).To(SatisfyAll(
							HaveField("Status.DRBD.Config.Quorum", Equal(expectedQuorum)),
							HaveField("Status.DRBD.Config.QuorumMinimumRedundancy", Equal(byte(0))),
						))
					})
				},
				func(diskfulCount, all int) string {
					expectedQuorum, _ := rvquorumcontroller.CalculateQuorum(diskfulCount, all, v1alpha3.ReplicationAvailability)
					return fmt.Sprintf("diskfulCount=%d, all=%d -> quorum=%d, qmr=0", diskfulCount, all, expectedQuorum)
				},
				Entry(nil, 2, 2),
				Entry(nil, 2, 3),
				Entry(nil, 2, 4),
			)

			When("RVR having finalizer and DeletionTimestamp", func() {
				BeforeEach(func() {
					rvrList[0].Finalizers = []string{"other-finalizer"}
				})

				JustBeforeEach(func(ctx SpecContext) {
					Expect(cl.Delete(ctx, rvrList[0])).To(Succeed())
				})

				It("should remove finalizer from RVR with DeletionTimestamp", func(ctx SpecContext) {
					Expect(rec.Reconcile(ctx, reconcile.Request{
						NamespacedName: types.NamespacedName{Name: "test-rv"},
					})).NotTo(Requeue())

					// Verify finalizer was removed
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvrList[0]), rvrList[0])).To(Succeed())
					Expect(rvrList[0].Finalizers).To(SatisfyAll(
						ContainElement("other-finalizer"),
						HaveLen(1)))
				})
			})

			When("RVR that doesn't have quorum-reconf finalizer", func() {
				BeforeEach(func() {
					rvrList[0].Finalizers = []string{"other-finalizer"}
				})

				JustBeforeEach(func(ctx SpecContext) {
					Expect(cl.Delete(ctx, rvrList[0])).To(Succeed())
				})

				It("should not process RVR that doesn't have quorum-reconf finalizer", func(ctx SpecContext) {
					Expect(rec.Reconcile(ctx, reconcile.Request{
						NamespacedName: types.NamespacedName{Name: "test-rv"},
					})).NotTo(Requeue())

					// Verify other finalizer is still present (unsetFinalizers should skip RVR without quorum-reconf finalizer)
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvrList[0]), rvrList[0])).To(Succeed())
					Expect(rvrList[0].Finalizers).To(SatisfyAll(
						ContainElement("other-finalizer"),
						HaveLen(1)))
				})
			})

			When("multiple RVRs", func() {
				BeforeEach(func() {
					rvrList[0].Finalizers = []string{}
					rvrList[1].Finalizers = []string{"other-finalizer"}
					rvrList[2].Finalizers = []string{}
				})

				JustBeforeEach(func(ctx SpecContext) {
					Expect(cl.Delete(ctx, rvrList[0])).To(Succeed())
					Expect(cl.Delete(ctx, rvrList[1])).To(Succeed())
				})

				It("should process multiple RVRs with DeletionTimestamp", func(ctx SpecContext) {
					Expect(rec.Reconcile(ctx, reconcile.Request{
						NamespacedName: types.NamespacedName{Name: "test-rv"},
					})).NotTo(Requeue())

					// Verify finalizers removed from RVRs with DeletionTimestamp
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvrList[0]), rvrList[0])).To(Satisfy(apierrors.IsNotFound))

					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvrList[1]), rvrList[1])).To(Succeed())
					Expect(rvrList[1].Finalizers).To(SatisfyAll(
						ContainElement("other-finalizer"),
						HaveLen(1),
					))

					// Verify finalizer kept for RVR without DeletionTimestamp
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvrList[2]), rvrList[2])).To(Succeed())
					Expect(rvrList[2].Finalizers).To(HaveLen(0))
				})
			})
		})
	})
})

var _ = Describe("CalculateQuorum", func() {
	DescribeTable("should calculate correct quorum and qmr values for ConsistencyAndAvailability",
		func(diskfulCount, all int, expectedQuorum, expectedQmr byte) {
			quorum, qmr := rvquorumcontroller.CalculateQuorum(diskfulCount, all, v1alpha3.ReplicationConsistencyAndAvailability)
			Expect(quorum).To(Equal(expectedQuorum))
			Expect(qmr).To(Equal(expectedQmr))
		},
		func(diskfulCount, all int, expectedQuorum, expectedQmr byte) string {
			return fmt.Sprintf("diskfulCount=%d, all=%d -> quorum=%d, qmr=%d", diskfulCount, all, expectedQuorum, expectedQmr)
		},
		// Edge cases: diskfulCount <= 1
		Entry(nil, 0, 1, byte(0), byte(0)),
		Entry(nil, 1, 1, byte(0), byte(0)),
		Entry(nil, 1, 2, byte(0), byte(0)),
		Entry(nil, 1, 3, byte(0), byte(0)),
		// Small numbers
		Entry(nil, 2, 2, byte(2), byte(2)),
		Entry(nil, 2, 3, byte(2), byte(2)),
		Entry(nil, 2, 4, byte(3), byte(2)),
		Entry(nil, 2, 5, byte(3), byte(2)),
		Entry(nil, 3, 3, byte(2), byte(2)),
		Entry(nil, 3, 4, byte(3), byte(2)),
		Entry(nil, 3, 5, byte(3), byte(2)),
		Entry(nil, 3, 6, byte(4), byte(2)),
		Entry(nil, 3, 7, byte(4), byte(2)),
		Entry(nil, 4, 4, byte(3), byte(3)),
		Entry(nil, 4, 5, byte(3), byte(3)),
		Entry(nil, 4, 6, byte(4), byte(3)),
		Entry(nil, 4, 7, byte(4), byte(3)),
		Entry(nil, 4, 8, byte(5), byte(3)),
		Entry(nil, 5, 5, byte(3), byte(3)),
		Entry(nil, 5, 6, byte(4), byte(3)),
		Entry(nil, 5, 7, byte(4), byte(3)),
		Entry(nil, 5, 8, byte(5), byte(3)),
		Entry(nil, 5, 9, byte(5), byte(3)),
		Entry(nil, 5, 10, byte(6), byte(3)),
		// Medium numbers
		Entry(nil, 6, 6, byte(4), byte(4)),
		Entry(nil, 6, 7, byte(4), byte(4)),
		Entry(nil, 6, 8, byte(5), byte(4)),
		Entry(nil, 6, 9, byte(5), byte(4)),
		Entry(nil, 6, 10, byte(6), byte(4)),
		Entry(nil, 7, 7, byte(4), byte(4)),
		Entry(nil, 7, 8, byte(5), byte(4)),
		Entry(nil, 7, 9, byte(5), byte(4)),
		Entry(nil, 7, 10, byte(6), byte(4)),
		Entry(nil, 8, 8, byte(5), byte(5)),
		Entry(nil, 8, 9, byte(5), byte(5)),
		Entry(nil, 8, 10, byte(6), byte(5)),
		Entry(nil, 9, 9, byte(5), byte(5)),
		Entry(nil, 9, 10, byte(6), byte(5)),
		Entry(nil, 10, 10, byte(6), byte(6)),
	)

	DescribeTable("should not set QMR for Availability replication",
		func(diskfulCount, all int, expectedQuorum byte) {
			quorum, qmr := rvquorumcontroller.CalculateQuorum(diskfulCount, all, v1alpha3.ReplicationAvailability)
			Expect(quorum).To(Equal(expectedQuorum))
			Expect(qmr).To(Equal(byte(0)), "QMR should be 0 for Availability replication")
		},
		func(diskfulCount, all int, expectedQuorum byte) string {
			return fmt.Sprintf("diskfulCount=%d, all=%d -> quorum=%d, qmr=0", diskfulCount, all, expectedQuorum)
		},
		Entry(nil, 2, 2, byte(2)),
		Entry(nil, 2, 3, byte(2)),
		Entry(nil, 2, 4, byte(3)),
		Entry(nil, 3, 3, byte(2)),
		Entry(nil, 3, 4, byte(3)),
		Entry(nil, 4, 4, byte(3)),
		Entry(nil, 4, 5, byte(3)),
	)

	DescribeTable("should not set QMR for None replication",
		func(diskfulCount, all int, expectedQuorum byte) {
			quorum, qmr := rvquorumcontroller.CalculateQuorum(diskfulCount, all, v1alpha3.ReplicationNone)
			Expect(quorum).To(Equal(expectedQuorum))
			Expect(qmr).To(Equal(byte(0)), "QMR should be 0 for None replication")
		},
		func(diskfulCount, all int, expectedQuorum byte) string {
			return fmt.Sprintf("diskfulCount=%d, all=%d -> quorum=%d, qmr=0", diskfulCount, all, expectedQuorum)
		},
		Entry(nil, 1, 1, byte(0)),
		Entry(nil, 1, 2, byte(0)),
		Entry(nil, 2, 2, byte(2)),
		Entry(nil, 2, 3, byte(2)),
	)
})
