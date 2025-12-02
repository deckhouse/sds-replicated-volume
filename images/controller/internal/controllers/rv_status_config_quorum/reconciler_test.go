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

	v1alpha3 "github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	rvquorumcontroller "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_status_config_quorum"
)

var (
	testScheme = func() *runtime.Scheme {
		scheme := runtime.NewScheme()
		_ = v1alpha3.AddToScheme(scheme)
		return scheme
	}()
)

func Requeue() OmegaMatcher {
	return Not(Equal(reconcile.Result{}))
}

var _ = Describe("Reconciler", func() {
	scheme := runtime.NewScheme()
	_ = v1alpha3.AddToScheme(scheme)

	var cl client.Client
	var rec *rvquorumcontroller.Reconciler

	BeforeEach(func() {
		cl = nil
		rec = nil
	})

	JustBeforeEach(func(ctx SpecContext) {
		cl = fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&v1alpha3.ReplicatedVolumeReplica{}, &v1alpha3.ReplicatedVolume{}).
			Build()
		rec = rvquorumcontroller.NewReconciler(
			cl,
			cl,
			nil,
			GinkgoLogr,
		)
	})

	It("returns no error when ReplicatedVolume does not exist", func(ctx SpecContext) {
		Expect(rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "test-rv"},
		})).NotTo(Requeue())
	})

	When("with ReplicatedVolume and ReplicatedVolumeReplicas", func() {
		var rv *v1alpha3.ReplicatedVolume
		var rvrList []*v1alpha3.ReplicatedVolumeReplica
		BeforeEach(func() {
			rv = &v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "test-rv"},
				Status:     &v1alpha3.ReplicatedVolumeStatus{Conditions: []metav1.Condition{}},
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
			Expect(cl.Create(ctx, rv)).To(Succeed())
			for _, rvr := range rvrList {
				Expect(cl.Create(ctx, rvr)).To(Succeed())
			}
		})

		DescribeTableSubtree("ReplicatedVolume is not ready",
			func(beforeEach func()) {
				BeforeEach(beforeEach)

				It("should not set quorum config", func(ctx SpecContext) {
					Expect(rec.Reconcile(ctx, reconcile.Request{
						NamespacedName: client.ObjectKeyFromObject(rv),
					})).NotTo(Requeue())

					// Verify no quorum config was set
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), rv)).To(Succeed())
					if rv.Status != nil {
						Expect(rv.Status.DRBD).To(BeNil())
					}
				})
			},
			Entry("when Status is nil", func() {
				rv.Status = nil
			}),
			Entry("when Conditions is nil", func() {
				if rv.Status == nil {
					rv.Status = &v1alpha3.ReplicatedVolumeStatus{}
				}
				rv.Status.Conditions = nil
			}),
			Entry("when Conditions is empty", func() {
				rv.Status.Conditions = []metav1.Condition{}
			}),
			Entry("when DiskfulReplicaCountReached is false", func() {
				rv.Status.Conditions = []metav1.Condition{
					{
						Type:   v1alpha3.ConditionTypeDiskfulReplicaCountReached,
						Status: metav1.ConditionFalse,
					},
				}
			}),
			Entry("when AllReplicasReady is false", func() {
				rv.Status.Conditions = []metav1.Condition{
					{
						Type:   v1alpha3.ConditionTypeAllReplicasReady,
						Status: metav1.ConditionFalse,
					},
				}
			}),
			Entry("when SharedSecretAlgorithmSelected is false", func() {
				rv.Status.Conditions = []metav1.Condition{
					{
						Type:   v1alpha3.ConditionTypeSharedSecretAlgorithmSelected,
						Status: metav1.ConditionFalse,
					},
				}
			}),
			Entry("when multiple conditions are missing", func() {
				rv.Status.Conditions = []metav1.Condition{
					{
						Type:   v1alpha3.ConditionTypeDiskfulReplicaCountReached,
						Status: metav1.ConditionFalse,
					},
					{
						Type:   v1alpha3.ConditionTypeAllReplicasReady,
						Status: metav1.ConditionFalse,
					},
				}
			}),
		)

		When("ReplicatedVolume is ready", func() {
			BeforeEach(func() {
				rv.Status.Conditions = []metav1.Condition{
					{
						Type:   v1alpha3.ConditionTypeDiskfulReplicaCountReached,
						Status: metav1.ConditionTrue,
					},
					{
						Type:   v1alpha3.ConditionTypeAllReplicasReady,
						Status: metav1.ConditionTrue,
					},
					{
						Type:   v1alpha3.ConditionTypeSharedSecretAlgorithmSelected,
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
				Expect(rvrList[0].Finalizers).To(ContainElement(rvquorumcontroller.QuorumReconfFinalizer))

				// Verify QuorumConfigured condition is set
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), rv)).To(Succeed())
				Expect(rv.Status.Conditions).To(HaveQuorumConfiguredCondition(metav1.ConditionTrue, "QuorumConfigured"))
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
					Expect(rvr.Finalizers).To(ContainElement(rvquorumcontroller.QuorumReconfFinalizer))
				}
			})

			When("single diskful replica", func() {
				BeforeEach(func() {
					rvrList = rvrList[:1]
				})

				It("should not set quorum when diskfulCount <= 1", func(ctx SpecContext) {
					// rvrList[0] is already created in JustBeforeEach

					Expect(rec.Reconcile(ctx, reconcile.Request{
						NamespacedName: types.NamespacedName{
							Name:      "test-rv",
							Namespace: "",
						},
					})).NotTo(Requeue())

					// Verify quorum is 0 (not set) and QuorumConfigured condition is still set
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), rv)).To(Succeed())
					Expect(rv).To(SatisfyAll(
						HaveField("Status.DRBD.Config.Quorum", Equal(byte(0))),
						HaveField("Status.DRBD.Config.QuorumMinimumRedundancy", Equal(byte(0))),
						HaveField("Status.Conditions", HaveQuorumConfiguredCondition(metav1.ConditionTrue)),
					))
				})
			})

			DescribeTableSubtree("checking quorum calculation",
				func(diskfulCount, all int) {
					BeforeEach(func() {
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

						expectedQuorum, expectedQmr := rvquorumcontroller.CalculateQuorum(diskfulCount, all)
						Expect(rv).To(SatisfyAll(
							HaveField("Status.DRBD.Config.Quorum", Equal(expectedQuorum)),
							HaveField("Status.DRBD.Config.QuorumMinimumRedundancy", Equal(expectedQmr)),
							HaveField("Status.Conditions", HaveQuorumConfiguredCondition(metav1.ConditionTrue)),
						))
					})
				},
				func(diskfulCount, all int) string {
					expectedQuorum, expectedQmr := rvquorumcontroller.CalculateQuorum(diskfulCount, all)
					return fmt.Sprintf("diskfulCount=%d, all=%d -> quorum=%d, qmr=%d", diskfulCount, all, expectedQuorum, expectedQmr)
				},
				Entry(nil, 1, 1),
				Entry(nil, 2, 2),
				Entry(nil, 3, 3),
				Entry(nil, 4, 4),
				Entry(nil, 5, 5),
				Entry(nil, 2, 3),
				Entry(nil, 3, 5),
				Entry(nil, 7, 7),
			)

			When("RVR having finalizer and DeletionTimestamp", func() {
				BeforeEach(func() {
					rvrList[0].Finalizers = []string{rvquorumcontroller.QuorumReconfFinalizer, "other-finalizer"}
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
					Expect(rvrList[0].Finalizers).NotTo(ContainElement(rvquorumcontroller.QuorumReconfFinalizer))
					Expect(rvrList[0].Finalizers).To(ContainElement("other-finalizer"))
				})
			})

			When("RVR having finalizer but no DeletionTimestamp", func() {
				BeforeEach(func() {
					rvrList[0].Finalizers = []string{rvquorumcontroller.QuorumReconfFinalizer}
				})

				It("should not remove finalizer from RVR without DeletionTimestamp", func(ctx SpecContext) {
					Expect(rec.Reconcile(ctx, reconcile.Request{
						NamespacedName: types.NamespacedName{Name: "test-rv"},
					})).NotTo(Requeue())

					// Verify finalizer is still present (unsetFinalizers should skip RVR without DeletionTimestamp)
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvrList[0]), rvrList[0])).To(Succeed())
					Expect(rvrList[0].Finalizers).To(ContainElement(rvquorumcontroller.QuorumReconfFinalizer))
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
					Expect(rvrList[0].Finalizers).To(ContainElement("other-finalizer"))
					Expect(rvrList[0].Finalizers).NotTo(ContainElement(rvquorumcontroller.QuorumReconfFinalizer))
				})
			})

			When("multiple RVRs", func() {
				BeforeEach(func() {
					rvrList[0].Finalizers = []string{rvquorumcontroller.QuorumReconfFinalizer}
					rvrList[1].Finalizers = []string{rvquorumcontroller.QuorumReconfFinalizer, "other-finalizer"}
					rvrList[2].Finalizers = []string{rvquorumcontroller.QuorumReconfFinalizer}
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
					Expect(rvrList[1].Finalizers).NotTo(ContainElement(rvquorumcontroller.QuorumReconfFinalizer))
					Expect(rvrList[1].Finalizers).To(ContainElement("other-finalizer"))

					// Verify finalizer kept for RVR without DeletionTimestamp
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvrList[2]), rvrList[2])).To(Succeed())
					Expect(rvrList[2].Finalizers).To(ContainElement(rvquorumcontroller.QuorumReconfFinalizer))
				})
			})

			When("RVR having only quorum-reconf finalizer and DeletionTimestamp", func() {
				BeforeEach(func() {
					rvrList[0].Finalizers = []string{rvquorumcontroller.QuorumReconfFinalizer}
				})

				JustBeforeEach(func(ctx SpecContext) {
					Expect(cl.Delete(ctx, rvrList[0])).To(Succeed())
				})

				It("should handle RVR with only quorum-reconf finalizer and DeletionTimestamp", func(ctx SpecContext) {
					Expect(rec.Reconcile(ctx, reconcile.Request{
						NamespacedName: types.NamespacedName{Name: "test-rv"},
					})).NotTo(Requeue())

					// Verify finalizer was removed (after removal, finalizers list should be empty)
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvrList[0]), rvrList[0])).To(Satisfy(apierrors.IsNotFound))
				})
			})

		})
	})
})

func HaveQuorumConfiguredCondition(status metav1.ConditionStatus, reason ...string) OmegaMatcher {
	matchers := []OmegaMatcher{
		HaveField("Type", Equal(v1alpha3.ConditionTypeQuorumConfigured)),
		HaveField("Status", Equal(status)),
	}
	if len(reason) > 0 {
		matchers = append(matchers, HaveField("Reason", Equal(reason[0])))
	}
	return ContainElement(SatisfyAll(matchers...))
}

var _ = Describe("CalculateQuorum", func() {
	DescribeTable("should calculate correct quorum and qmr values",
		func(diskfulCount, all int, expectedQuorum, expectedQmr byte) {
			quorum, qmr := rvquorumcontroller.CalculateQuorum(diskfulCount, all)
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
})
