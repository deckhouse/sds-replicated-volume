package rvrstatusconfignodeid_test

import (
	"context"
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha3 "github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	rvrstatusconfignodeid "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_status_config_node_id"
	e "github.com/deckhouse/sds-replicated-volume/images/controller/internal/errors"
)

var _ = Describe("Reconciler", func() {
	// Available in BeforeEach
	var (
		clientBuilder *fake.ClientBuilder
		scheme        *runtime.Scheme
	)

	// Available in JustBeforeEach
	var (
		cl  client.WithWatch
		rec *rvrstatusconfignodeid.Reconciler
	)

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha3.AddToScheme(scheme)).To(Succeed(), "should add v1alpha3 to scheme")
		clientBuilder = fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&v1alpha3.ReplicatedVolumeReplica{})
		cl = nil
		rec = nil
	})

	JustBeforeEach(func() {
		cl = clientBuilder.Build()
		rec = rvrstatusconfignodeid.NewReconciler(cl, GinkgoLogr)
	})

	It("returns no error when ReplicatedVolumeReplica does not exist", func(ctx SpecContext) {
		By("Reconciling non-existent ReplicatedVolumeReplica")
		Expect(rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "non-existent"},
		})).NotTo(Requeue(), "should ignore NotFound errors")
	})

	When("Get fails with non-NotFound error", func() {
		internalServerError := errors.New("internal server error")
		BeforeEach(func() {
			clientBuilder = clientBuilder.WithInterceptorFuncs(InterceptGet(func(_ *v1alpha3.ReplicatedVolumeReplica) error {
				return internalServerError
			}))
		})

		It("should fail if getting ReplicatedVolumeReplica failed with non-NotFound error", func(ctx SpecContext) {
			By("Reconciling with Get interceptor that returns error")
			Expect(rec.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-rvr"},
			})).Error().To(MatchError(internalServerError), "should return error when Get fails")
		})
	})

	When("RVR created", func() {
		// Base RVRs created in BeforeEach, can be modified in child tests
		var (
			rvr      *v1alpha3.ReplicatedVolumeReplica
			rvrList  []v1alpha3.ReplicatedVolumeReplica
			otherRVR *v1alpha3.ReplicatedVolumeReplica
		)

		BeforeEach(func() {
			// Base RVR for volume-1 - used in most tests
			rvr = &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name: "rvr-1",
				},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "volume-1",
					NodeName:             "node-1",
				},
			}

			// Base RVR for volume-2 - used for isolation tests
			otherRVR = &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name: "rvr-vol2-1",
				},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "volume-2",
					NodeName:             "node-3",
				},
			}

			// Initialize empty list - will be populated in child tests
			rvrList = nil
		})

		JustBeforeEach(func(ctx SpecContext) {
			if rvr != nil {
				Expect(cl.Create(ctx, rvr)).To(Succeed(), "should create base RVR")
			}
			// rvrList and otherRVR are created only in child tests when needed
		})

		DescribeTableSubtree("when rvr has",
			Entry("nil Status", func() { rvr.Status = nil }),
			Entry("nil Status.DRBD", func() { rvr.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{DRBD: nil} }),
			Entry("nil Status.DRBD.Config", func() { rvr.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{DRBD: &v1alpha3.DRBD{Config: nil}} }),
			Entry("nil Status.DRBD.Config.NodeId", func() {
				rvr.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{
					DRBD: &v1alpha3.DRBD{
						Config: &v1alpha3.DRBDConfig{NodeId: nil},
					},
				}
			}),
			func(setup func()) {
				BeforeEach(func() {
					setup()
				})

				It("should reconcile successfully and assign nodeID", func(ctx SpecContext) {
					By("Reconciling ReplicatedVolumeReplica with nil status fields")
					Expect(rec.Reconcile(ctx, RequestFor(rvr))).ToNot(Requeue(), "should not requeue after successful assignment")

					By("Verifying nodeID was assigned")
					updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), updatedRVR)).To(Succeed(), "should get updated RVR")
					Expect(updatedRVR).To(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically("==", rvrstatusconfignodeid.MinNodeID))), "first replica should get nodeID MinNodeID")
				})
			})

		When("RVR without nodeID", func() {
			BeforeEach(func() {
				rvr.Status = nil
			})

			It("assigns nodeID to first replica", func(ctx SpecContext) {
				By("Reconciling first replica without nodeID")
				Expect(rec.Reconcile(ctx, RequestFor(rvr))).ToNot(Requeue(), "should not requeue after successful assignment")

				By("Verifying first replica got nodeID MinNodeID and it is actually assigned")
				updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), updatedRVR)).To(Succeed(), "should get updated RVR")
				Expect(updatedRVR).To(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically("==", rvrstatusconfignodeid.MinNodeID))), "first replica should get nodeID MinNodeID")
			})

			When("multiple RVRs exist", func() {
				When("assigning nodeID sequentially", func() {
					var rvrList []v1alpha3.ReplicatedVolumeReplica

					BeforeEach(func() {
						rvrList = make([]v1alpha3.ReplicatedVolumeReplica, 6)
						for i := 0; i < 5; i++ {
							nodeID := uint(rvrstatusconfignodeid.MinNodeID + i)
							rvrList[i] = v1alpha3.ReplicatedVolumeReplica{
								ObjectMeta: metav1.ObjectMeta{
									Name: fmt.Sprintf("rvr-seq-%d", i+1),
								},
								Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
									ReplicatedVolumeName: "volume-1",
									NodeName:             fmt.Sprintf("node-%d", i+1),
								},
								Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
									DRBD: &v1alpha3.DRBD{
										Config: &v1alpha3.DRBDConfig{
											NodeId: &nodeID,
										},
									},
								},
							}
						}
						// Add one more without nodeId - should get next sequential nodeID
						rvrList[5] = v1alpha3.ReplicatedVolumeReplica{
							ObjectMeta: metav1.ObjectMeta{
								Name: "rvr-seq-6",
							},
							Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "volume-1",
								NodeName:             "node-6",
							},
						}
					})

					JustBeforeEach(func(ctx SpecContext) {
						for i := range rvrList {
							Expect(cl.Create(ctx, &rvrList[i])).To(Succeed(), "should create RVR successfully")
						}
					})

					It("assigns nodeID sequentially and ensures uniqueness", func(ctx SpecContext) {
						By("Reconciling replica without nodeID")
						Expect(rec.Reconcile(ctx, RequestFor(&rvrList[5]))).ToNot(Requeue(), "should not requeue after successful assignment")

						By("Verifying sequential assignment: next available nodeID after 0-4")
						updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
						Expect(cl.Get(ctx, client.ObjectKeyFromObject(&rvrList[5]), updatedRVR)).To(Succeed(), "should get updated RVR")
						Expect(updatedRVR).To(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically("==", rvrstatusconfignodeid.MinNodeID+5))), "should assign nodeID MinNodeID+5 as next sequential value")
					})
				})

				When("isolating nodeIDs by volume", func() {
					BeforeEach(func() {
						nodeID1 := uint(rvrstatusconfignodeid.MinNodeID)
						nodeID2 := uint(rvrstatusconfignodeid.MinNodeID + 1)
						rvrList = []v1alpha3.ReplicatedVolumeReplica{
							{
								ObjectMeta: metav1.ObjectMeta{Name: "rvr-vol1-1"},
								Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
									ReplicatedVolumeName: "volume-1",
									NodeName:             "node-1",
								},
								Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
									DRBD: &v1alpha3.DRBD{
										Config: &v1alpha3.DRBDConfig{NodeId: &nodeID1},
									},
								},
							},
							{
								ObjectMeta: metav1.ObjectMeta{Name: "rvr-vol1-2"},
								Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
									ReplicatedVolumeName: "volume-1",
									NodeName:             "node-2",
								},
								Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
									DRBD: &v1alpha3.DRBD{
										Config: &v1alpha3.DRBDConfig{NodeId: &nodeID2},
									},
								},
							},
						}
					})

					JustBeforeEach(func(ctx SpecContext) {
						for i := range rvrList {
							Expect(cl.Create(ctx, &rvrList[i])).To(Succeed(), "should create RVR for volume-1")
						}
						Expect(cl.Create(ctx, otherRVR)).To(Succeed(), "should create RVR for volume-2")
					})

					It("isolates nodeIDs by volume", func(ctx SpecContext) {
						By("Reconciling RVR in volume-2")
						Expect(rec.Reconcile(ctx, RequestFor(otherRVR))).ToNot(Requeue(), "should not requeue after successful assignment")

						By("Verifying volume-2 is independent: should get nodeID MinNodeID")
						updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
						Expect(cl.Get(ctx, client.ObjectKeyFromObject(otherRVR), updatedRVR)).To(Succeed(), "should get updated RVR")
						Expect(updatedRVR).To(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically("==", rvrstatusconfignodeid.MinNodeID))), "volume-2 should get nodeID MinNodeID independently of volume-1")
					})
				})

				When("filling gaps in nodeIDs", func() {
					var rvrList []v1alpha3.ReplicatedVolumeReplica

					BeforeEach(func() {
						nodeID0 := uint(rvrstatusconfignodeid.MinNodeID)
						nodeID2 := uint(rvrstatusconfignodeid.MinNodeID + 2)
						nodeID3 := uint(rvrstatusconfignodeid.MinNodeID + 3)
						rvrList = []v1alpha3.ReplicatedVolumeReplica{
							{
								ObjectMeta: metav1.ObjectMeta{Name: "rvr-gap-1"},
								Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
									ReplicatedVolumeName: "volume-1",
									NodeName:             "node-1",
								},
								Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
									DRBD: &v1alpha3.DRBD{
										Config: &v1alpha3.DRBDConfig{NodeId: &nodeID0},
									},
								},
							},
							{
								ObjectMeta: metav1.ObjectMeta{Name: "rvr-gap-2"},
								Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
									ReplicatedVolumeName: "volume-1",
									NodeName:             "node-2",
								},
								Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
									DRBD: &v1alpha3.DRBD{
										Config: &v1alpha3.DRBDConfig{NodeId: &nodeID2},
									},
								},
							},
							{
								ObjectMeta: metav1.ObjectMeta{Name: "rvr-gap-3"},
								Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
									ReplicatedVolumeName: "volume-1",
									NodeName:             "node-3",
								},
								Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
									DRBD: &v1alpha3.DRBD{
										Config: &v1alpha3.DRBDConfig{NodeId: &nodeID3},
									},
								},
							},
							{
								ObjectMeta: metav1.ObjectMeta{Name: "rvr-gap-4"},
								Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
									ReplicatedVolumeName: "volume-1",
									NodeName:             "node-4",
								},
							},
						}
					})

					JustBeforeEach(func(ctx SpecContext) {
						for i := range rvrList {
							Expect(cl.Create(ctx, &rvrList[i])).To(Succeed(), "should create RVR successfully")
						}
					})

					It("fills gaps in nodeIDs", func(ctx SpecContext) {
						By("Reconciling replica without nodeID to test gap filling")
						Expect(rec.Reconcile(ctx, RequestFor(&rvrList[3]))).ToNot(Requeue(), "should not requeue after successful assignment")

						By("Verifying gap filling: minimum free nodeID between MinNodeID and MinNodeID+2")
						updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
						Expect(cl.Get(ctx, client.ObjectKeyFromObject(&rvrList[3]), updatedRVR)).To(Succeed(), "should get updated RVR")
						Expect(updatedRVR).To(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically("==", rvrstatusconfignodeid.MinNodeID+1))), "should assign nodeID MinNodeID+1 to fill gap")
					})
				})

				When("nodeID already assigned", func() {
					var rvrList []v1alpha3.ReplicatedVolumeReplica
					var testNodeID uint

					BeforeEach(func() {
						testNodeID = uint(rvrstatusconfignodeid.MinNodeID + 3)
						rvrList = []v1alpha3.ReplicatedVolumeReplica{
							{
								ObjectMeta: metav1.ObjectMeta{Name: "rvr-idemp-1"},
								Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
									ReplicatedVolumeName: "volume-1",
									NodeName:             "node-1",
								},
								Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
									DRBD: &v1alpha3.DRBD{
										Config: &v1alpha3.DRBDConfig{NodeId: &testNodeID},
									},
								},
							},
						}
					})

					JustBeforeEach(func(ctx SpecContext) {
						for i := range rvrList {
							Expect(cl.Create(ctx, &rvrList[i])).To(Succeed(), "should create RVR with nodeID")
						}
					})

					It("does not reassign nodeID if already assigned", func(ctx SpecContext) {
						By("First reconciliation: should not reassign existing nodeID")
						Expect(rec.Reconcile(ctx, RequestFor(&rvrList[0]))).ToNot(Requeue(), "should not requeue when nodeID already assigned")

						By("Verifying nodeID remains unchanged after first reconciliation")
						updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
						Expect(cl.Get(ctx, client.ObjectKeyFromObject(&rvrList[0]), updatedRVR)).To(Succeed(), "should get updated RVR")
						Expect(updatedRVR).To(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically("==", testNodeID))), "nodeID should remain unchanged, not be reassigned")

						By("Second reconciliation: should still not reassign (idempotent)")
						Expect(rec.Reconcile(ctx, RequestFor(&rvrList[0]))).ToNot(Requeue(), "should not requeue when nodeID already assigned")

						By("Verifying nodeID hasn't changed after both reconciliations")
						Expect(cl.Get(ctx, client.ObjectKeyFromObject(&rvrList[0]), updatedRVR)).To(Succeed(), "should get updated RVR")
						Expect(updatedRVR).To(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically("==", testNodeID))), "nodeID should remain unchanged after multiple reconciliations (idempotent)")
					})
				})

				When("ignoring invalid nodeID", func() {
					var rvrList []v1alpha3.ReplicatedVolumeReplica

					BeforeEach(func() {
						invalidNodeID := uint(rvrstatusconfignodeid.MaxNodeID + 1)
						rvrList = []v1alpha3.ReplicatedVolumeReplica{
							{
								ObjectMeta: metav1.ObjectMeta{Name: "rvr-invalid-1"},
								Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
									ReplicatedVolumeName: "volume-1",
									NodeName:             "node-1",
								},
								Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
									DRBD: &v1alpha3.DRBD{
										Config: &v1alpha3.DRBDConfig{NodeId: &invalidNodeID},
									},
								},
							},
							{
								ObjectMeta: metav1.ObjectMeta{Name: "rvr-invalid-2"},
								Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
									ReplicatedVolumeName: "volume-1",
									NodeName:             "node-2",
								},
							},
						}
					})

					JustBeforeEach(func(ctx SpecContext) {
						for i := range rvrList {
							Expect(cl.Create(ctx, &rvrList[i])).To(Succeed(), "should create RVR successfully")
						}
					})

					It("ignores nodeID outside valid range", func(ctx SpecContext) {
						By("Reconciling replica without nodeID")
						Expect(rec.Reconcile(ctx, RequestFor(&rvrList[1]))).ToNot(Requeue(), "should not requeue after successful assignment")

						By("Verifying invalid nodeID was ignored: should get nodeID MinNodeID")
						updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
						Expect(cl.Get(ctx, client.ObjectKeyFromObject(&rvrList[1]), updatedRVR)).To(Succeed(), "should get updated RVR")
						Expect(updatedRVR).To(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically("==", rvrstatusconfignodeid.MinNodeID))), "should get nodeID MinNodeID because invalid nodeID was ignored")
					})
				})

				When("reassigning invalid nodeID", func() {
					var rvrList []v1alpha3.ReplicatedVolumeReplica

					BeforeEach(func() {
						invalidNodeID := uint(rvrstatusconfignodeid.MaxNodeID + 1)
						rvrList = []v1alpha3.ReplicatedVolumeReplica{
							{
								ObjectMeta: metav1.ObjectMeta{Name: "rvr-reassign-1"},
								Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
									ReplicatedVolumeName: "volume-1",
									NodeName:             "node-1",
								},
								Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
									DRBD: &v1alpha3.DRBD{
										Config: &v1alpha3.DRBDConfig{NodeId: &invalidNodeID},
									},
								},
							},
						}
					})

					JustBeforeEach(func(ctx SpecContext) {
						for i := range rvrList {
							Expect(cl.Create(ctx, &rvrList[i])).To(Succeed(), "should create RVR with invalid nodeID")
						}
					})

					It("logs warning and reassigns when nodeID > MaxNodeID is already set", func(ctx SpecContext) {
						By("Reconciling: should log warning about invalid nodeID and reassign valid one")
						Expect(rec.Reconcile(ctx, RequestFor(&rvrList[0]))).ToNot(Requeue(), "should not requeue after successful reassignment")

						By("Verifying invalid nodeID was reset and valid nodeID was assigned")
						updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
						Expect(cl.Get(ctx, client.ObjectKeyFromObject(&rvrList[0]), updatedRVR)).To(Succeed(), "should get updated RVR")
						By("Checking that assigned nodeID is within valid range")
						validNodeIDs := []uint{
							uint(rvrstatusconfignodeid.MinNodeID),
							uint(rvrstatusconfignodeid.MinNodeID + 1),
							uint(rvrstatusconfignodeid.MinNodeID + 2),
						}
						Expect(updatedRVR).To(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically(">=", rvrstatusconfignodeid.MinNodeID))), "should assign valid nodeID >= MinNodeID")
						Expect(updatedRVR).To(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically("<=", rvrstatusconfignodeid.MaxNodeID))), "should assign valid nodeID <= MaxNodeID")
						Expect(*updatedRVR.Status.DRBD.Config.NodeId).To(BeElementOf(validNodeIDs), "should assign one of the first available valid nodeIDs")
					})
				})

				When("resetting invalid nodeID", func() {
					var rvrList []v1alpha3.ReplicatedVolumeReplica

					BeforeEach(func() {
						// Create 6 replicas with valid nodeIDs (MinNodeID+1 to MinNodeID+6), leaving nodeID MaxNodeID free
						rvrList = make([]v1alpha3.ReplicatedVolumeReplica, 7)
						for i := 1; i < 7; i++ {
							nodeID := uint(rvrstatusconfignodeid.MinNodeID + i)
							rvrList[i-1] = v1alpha3.ReplicatedVolumeReplica{
								ObjectMeta: metav1.ObjectMeta{
									Name: fmt.Sprintf("rvr-reset-%d", i+1),
								},
								Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
									ReplicatedVolumeName: "volume-1",
									NodeName:             fmt.Sprintf("node-%d", i+1),
								},
								Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
									DRBD: &v1alpha3.DRBD{
										Config: &v1alpha3.DRBDConfig{NodeId: &nodeID},
									},
								},
							}
						}
						// Create replica with invalid nodeID > MaxNodeID
						invalidNodeID := uint(rvrstatusconfignodeid.MaxNodeID + 1)
						rvrList[6] = v1alpha3.ReplicatedVolumeReplica{
							ObjectMeta: metav1.ObjectMeta{Name: "rvr-reset-invalid"},
							Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "volume-1",
								NodeName:             "node-invalid",
							},
							Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
								DRBD: &v1alpha3.DRBD{
									Config: &v1alpha3.DRBDConfig{NodeId: &invalidNodeID},
								},
							},
						}
					})

					JustBeforeEach(func(ctx SpecContext) {
						for i := range rvrList {
							Expect(cl.Create(ctx, &rvrList[i])).To(Succeed(), "should create RVR successfully")
						}
						By("Ensuring parent rvr-1 has nodeID MinNodeID")
						Expect(rec.Reconcile(ctx, RequestFor(rvr))).ToNot(Requeue(), "should assign nodeID to parent RVR")
						By("Reconciling all created RVRs to ensure they have their nodeIDs")
						for i := 0; i < 6; i++ {
							Expect(rec.Reconcile(ctx, RequestFor(&rvrList[i]))).ToNot(Requeue(), "should not requeue when nodeID already assigned")
						}
					})

					It("resets invalid nodeID and reassigns valid one", func(ctx SpecContext) {
						By("Reconciling: should detect invalid nodeID, reset it, and assign free nodeID (MaxNodeID)")
						Expect(rec.Reconcile(ctx, RequestFor(&rvrList[6]))).ToNot(Requeue(), "should not requeue after successful reassignment")

						By("Verifying invalid nodeID was reset and free nodeID MaxNodeID was assigned")
						expectedNodeIDs := []uint{uint(rvrstatusconfignodeid.MaxNodeID)}
						updatedRVRInvalid := &v1alpha3.ReplicatedVolumeReplica{}
						Expect(cl.Get(ctx, client.ObjectKeyFromObject(&rvrList[6]), updatedRVRInvalid)).To(Succeed(), "should get updated RVR")
						Expect(updatedRVRInvalid).To(HaveField("Status.DRBD.Config.NodeId", PointTo(BeElementOf(expectedNodeIDs))), "should assign free nodeID MaxNodeID after resetting invalid nodeID")
					})
				})

				When("List fails", func() {
					listError := errors.New("failed to list replicas")
					BeforeEach(func() {
						clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
							List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
								if _, ok := list.(*v1alpha3.ReplicatedVolumeReplicaList); ok {
									return listError
								}
								return cl.List(ctx, list, opts...)
							},
						})
					})

					It("should fail if listing replicas failed", func(ctx SpecContext) {
						By("Reconciling with List interceptor that returns error")
						Expect(rec.Reconcile(ctx, RequestFor(rvr))).Error().To(MatchError(listError), "should return error when List fails")
					})
				})
			})

			When("all nodeIDs are used", func() {
				var rvrList []v1alpha3.ReplicatedVolumeReplica
				var rvr9 v1alpha3.ReplicatedVolumeReplica

				BeforeEach(func() {
					// Don't create parent rvr for this test - we'll create all RVRs ourselves
					rvr = nil
					// Create replicas using all nodeIds from MinNodeID to MaxNodeID
					totalNodeIDs := rvrstatusconfignodeid.MaxNodeID - rvrstatusconfignodeid.MinNodeID + 1
					rvrList = make([]v1alpha3.ReplicatedVolumeReplica, totalNodeIDs)
					for i := 0; i < totalNodeIDs; i++ {
						nodeID := uint(rvrstatusconfignodeid.MinNodeID + i)
						rvrList[i] = v1alpha3.ReplicatedVolumeReplica{
							ObjectMeta: metav1.ObjectMeta{
								Name: fmt.Sprintf("rvr-allused-%d", i+1),
							},
							Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "volume-1",
								NodeName:             fmt.Sprintf("node-%d", i+1),
							},
							Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
								DRBD: &v1alpha3.DRBD{
									Config: &v1alpha3.DRBDConfig{NodeId: &nodeID},
								},
							},
						}
					}
					// Create next RVR without nodeID - should fail (all nodeIDs used)
					rvr9 = v1alpha3.ReplicatedVolumeReplica{
						ObjectMeta: metav1.ObjectMeta{
							Name: "rvr-allused-9",
						},
						Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "volume-1",
							NodeName:             "node-9",
						},
					}
				})

				JustBeforeEach(func(ctx SpecContext) {
					for i := range rvrList {
						Expect(cl.Create(ctx, &rvrList[i])).To(Succeed(), "should create RVR with nodeID")
					}
					Expect(cl.Create(ctx, &rvr9)).To(Succeed(), "should create RVR without nodeID")
				})

				It("returns error when all nodeIDs are used and assigns freed nodeID after deletion", func(ctx SpecContext) {
					By("Reconciling should fail when all nodeIDs are used")
					Expect(rec.Reconcile(ctx, RequestFor(&rvr9))).Error().To(MatchError(e.ErrInvalidCluster), "should return ErrInvalidCluster when all nodeIDs are used")

					By("Deleting one RVR to free its nodeID")
					freedNodeID := uint(rvrstatusconfignodeid.MinNodeID + 3)
					Expect(cl.Delete(ctx, &rvrList[3])).To(Succeed(), "should delete RVR successfully")

					By("Reconciling again: should now get the freed nodeID")
					Expect(rec.Reconcile(ctx, RequestFor(&rvr9))).ToNot(Requeue(), "should not requeue after successful assignment")

					By("Verifying freed nodeID was assigned")
					updatedRVR9 := &v1alpha3.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(&rvr9), updatedRVR9)).To(Succeed(), "should get updated RVR")
					Expect(updatedRVR9).To(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically("==", freedNodeID))), "should assign freed nodeID after deletion")
				})
			})
		})

		When("Patch fails with non-NotFound error", func() {
			patchError := errors.New("failed to patch status")
			BeforeEach(func() {
				clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
					SubResourcePatch: func(ctx context.Context, cl client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
						if _, ok := obj.(*v1alpha3.ReplicatedVolumeReplica); ok {
							if subResourceName == "status" {
								return patchError
							}
						}
						return cl.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
					},
				})
			})

			It("should fail if patching ReplicatedVolumeReplica status failed with non-NotFound error", func(ctx SpecContext) {
				By("Reconciling with Patch interceptor that returns error")
				Expect(rec.Reconcile(ctx, RequestFor(rvr))).Error().To(MatchError(patchError), "should return error when Patch fails")
			})
		})

	})
})
