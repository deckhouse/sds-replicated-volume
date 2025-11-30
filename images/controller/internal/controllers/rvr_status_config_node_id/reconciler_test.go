// cspell:words Logr Subresource apimachinery gomega metav onsi

package rvrstatusconfignodeid_test

import (
	"context"
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha3 "github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	rvrstatusconfignodeid "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_status_config_node_id"
	e "github.com/deckhouse/sds-replicated-volume/images/controller/internal/errors"
)

// createRVR creates a basic RVR without nodeID
func createRVR(name, volumeName, nodeName string) *v1alpha3.ReplicatedVolumeReplica {
	return &v1alpha3.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
			ReplicatedVolumeName: volumeName,
			NodeName:             nodeName,
		},
	}
}

// createRVRWithNodeID creates an RVR with nodeID already assigned
func createRVRWithNodeID(name, nodeName string, nodeID uint) *v1alpha3.ReplicatedVolumeReplica {
	rvr := createRVR(name, "volume-1", nodeName)
	rvr.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{
		DRBD: &v1alpha3.DRBD{
			Config: &v1alpha3.DRBDConfig{
				NodeId: &nodeID,
			},
		},
	}
	return rvr
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
		rec *rvrstatusconfignodeid.Reconciler
	)

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha3.AddToScheme(scheme)).To(Succeed())
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
		_, err := rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: client.ObjectKey{Name: "non-existent"},
		})
		Expect(err).NotTo(HaveOccurred())
	})

	When("Get fails with non-NotFound error", func() {
		internalServerError := errors.New("internal server error")
		BeforeEach(func() {
			clientBuilder = clientBuilder.WithInterceptorFuncs(InterceptGet(func(_ *v1alpha3.ReplicatedVolumeReplica) error {
				return internalServerError
			}))
		})

		It("should fail if getting ReplicatedVolumeReplica failed with non-NotFound error", func(ctx SpecContext) {
			Expect(rec.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKey{Name: "test-rvr"},
			})).Error().To(MatchError(internalServerError))
		})
	})

	When("RVR created", func() {
		var rvr *v1alpha3.ReplicatedVolumeReplica

		BeforeEach(func() {
			rvr = createRVR("rvr-1", "volume-1", "node-1")
		})

		JustBeforeEach(func(ctx SpecContext) {
			if rvr != nil {
				Expect(cl.Create(ctx, rvr)).To(Succeed())
			}
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
					Expect(rec.Reconcile(ctx, RequestFor(rvr))).ToNot(Requeue())

					updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), updatedRVR)).To(Succeed())
					Expect(updatedRVR.Status.DRBD.Config.NodeId).NotTo(BeNil())
					Expect(*updatedRVR.Status.DRBD.Config.NodeId).To(Equal(uint(rvrstatusconfignodeid.MinNodeID)))
				})
			})

		When("RVR without nodeID", func() {
			It("assigns nodeID to first replica", func(ctx SpecContext) {
				Expect(rec.Reconcile(ctx, RequestFor(rvr))).ToNot(Requeue())

				updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), updatedRVR)).To(Succeed())
				Expect(updatedRVR.Status.DRBD.Config.NodeId).NotTo(BeNil())
				Expect(*updatedRVR.Status.DRBD.Config.NodeId).To(Equal(uint(rvrstatusconfignodeid.MinNodeID)))
			})

			It("assigns nodeID sequentially and ensures uniqueness", func(ctx SpecContext) {
				// Create 5 replicas with nodeIds starting from MinNodeID
				for i := 0; i < 5; i++ {
					testRvr := createRVRWithNodeID(
						fmt.Sprintf("rvr-seq-%d", i+1),
						fmt.Sprintf("node-%d", i+1),
						uint(rvrstatusconfignodeid.MinNodeID+i),
					)
					Expect(cl.Create(ctx, testRvr)).To(Succeed())
				}
				// Add one more without nodeId - should get next sequential nodeID
				rvr6 := createRVR("rvr-seq-6", "volume-1", "node-6")
				Expect(cl.Create(ctx, rvr6)).To(Succeed())

				Expect(rec.Reconcile(ctx, RequestFor(rvr6))).ToNot(Requeue())

				updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr6), updatedRVR)).To(Succeed())
				Expect(updatedRVR.Status.DRBD.Config.NodeId).NotTo(BeNil())
				Expect(*updatedRVR.Status.DRBD.Config.NodeId).To(Equal(uint(rvrstatusconfignodeid.MinNodeID + 5)))
			})

			It("isolates nodeIDs by volume", func(ctx SpecContext) {
				// Create RVRs in volume-1 with nodeIDs MinNodeID, MinNodeID+1
				rvr1 := createRVRWithNodeID("rvr-vol1-1", "node-1", uint(rvrstatusconfignodeid.MinNodeID))
				rvr2 := createRVRWithNodeID("rvr-vol1-2", "node-2", uint(rvrstatusconfignodeid.MinNodeID+1))
				Expect(cl.Create(ctx, rvr1)).To(Succeed())
				Expect(cl.Create(ctx, rvr2)).To(Succeed())

				// Create RVR in volume-2 - should get nodeID MinNodeID (independent of volume-1)
				rvr3 := createRVR("rvr-vol2-1", "volume-2", "node-3")
				Expect(cl.Create(ctx, rvr3)).To(Succeed())

				Expect(rec.Reconcile(ctx, RequestFor(rvr3))).ToNot(Requeue())

				updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr3), updatedRVR)).To(Succeed())
				Expect(updatedRVR.Status.DRBD.Config.NodeId).NotTo(BeNil())
				Expect(*updatedRVR.Status.DRBD.Config.NodeId).To(Equal(uint(rvrstatusconfignodeid.MinNodeID))) // volume-2 is independent
			})

			It("fills gaps in nodeIDs", func(ctx SpecContext) {
				rvr1 := createRVRWithNodeID("rvr-gap-1", "node-1", uint(rvrstatusconfignodeid.MinNodeID))
				rvr2 := createRVRWithNodeID("rvr-gap-2", "node-2", uint(rvrstatusconfignodeid.MinNodeID+2))
				rvr3 := createRVRWithNodeID("rvr-gap-3", "node-3", uint(rvrstatusconfignodeid.MinNodeID+3))
				rvr4 := createRVR("rvr-gap-4", "volume-1", "node-4")
				Expect(cl.Create(ctx, rvr1)).To(Succeed())
				Expect(cl.Create(ctx, rvr2)).To(Succeed())
				Expect(cl.Create(ctx, rvr3)).To(Succeed())
				Expect(cl.Create(ctx, rvr4)).To(Succeed())

				Expect(rec.Reconcile(ctx, RequestFor(rvr4))).ToNot(Requeue())

				updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr4), updatedRVR)).To(Succeed())
				Expect(updatedRVR.Status.DRBD.Config.NodeId).NotTo(BeNil())
				Expect(*updatedRVR.Status.DRBD.Config.NodeId).To(Equal(uint(rvrstatusconfignodeid.MinNodeID + 1))) // filled the gap
			})

			It("does not reassign nodeID if already assigned", func(ctx SpecContext) {
				testNodeID := uint(rvrstatusconfignodeid.MinNodeID + 3)
				testRvr := createRVRWithNodeID("rvr-idemp-1", "node-1", testNodeID)
				Expect(cl.Create(ctx, testRvr)).To(Succeed())

				Expect(rec.Reconcile(ctx, RequestFor(testRvr))).ToNot(Requeue())

				updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(testRvr), updatedRVR)).To(Succeed())
				Expect(updatedRVR.Status.DRBD.Config.NodeId).NotTo(BeNil())
				Expect(*updatedRVR.Status.DRBD.Config.NodeId).To(Equal(testNodeID))

				// Reconcile again - should be idempotent
				Expect(rec.Reconcile(ctx, RequestFor(testRvr))).ToNot(Requeue())

				Expect(cl.Get(ctx, client.ObjectKeyFromObject(testRvr), updatedRVR)).To(Succeed())
				Expect(updatedRVR.Status.DRBD.Config.NodeId).NotTo(BeNil())
				Expect(*updatedRVR.Status.DRBD.Config.NodeId).To(Equal(testNodeID)) // still same, not reassigned
			})

			It("ignores nodeID outside valid range", func(ctx SpecContext) {
				// Create RVR with nodeID > MaxNodeID (should be ignored)
				invalidNodeID := uint(rvrstatusconfignodeid.MaxNodeID + 1)
				rvr1 := createRVRWithNodeID("rvr-invalid-1", "node-1", invalidNodeID)
				Expect(cl.Create(ctx, rvr1)).To(Succeed())

				// Create new RVR without nodeID - should get nodeID MinNodeID (invalid nodeID was ignored)
				rvr2 := createRVR("rvr-invalid-2", "volume-1", "node-2")
				Expect(cl.Create(ctx, rvr2)).To(Succeed())

				Expect(rec.Reconcile(ctx, RequestFor(rvr2))).ToNot(Requeue())

				updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr2), updatedRVR)).To(Succeed())
				Expect(updatedRVR.Status.DRBD.Config.NodeId).NotTo(BeNil())
				// Should get nodeID MinNodeID, because invalid nodeID was ignored
				Expect(*updatedRVR.Status.DRBD.Config.NodeId).To(Equal(uint(rvrstatusconfignodeid.MinNodeID)))
			})

			It("logs error and reassigns when nodeID outside valid range is already set", func(ctx SpecContext) {
				// Create RVR with nodeID > MaxNodeID already set
				invalidNodeID := uint(rvrstatusconfignodeid.MaxNodeID + 1)
				testRvr := createRVRWithNodeID("rvr-reassign-1", "node-1", invalidNodeID)
				Expect(cl.Create(ctx, testRvr)).To(Succeed())

				// Reconcile - should log error about invalid nodeID and reassign valid one
				Expect(rec.Reconcile(ctx, RequestFor(testRvr))).ToNot(Requeue())

				// Verify RVR got valid nodeID (MinNodeID)
				updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(testRvr), updatedRVR)).To(Succeed())
				Expect(updatedRVR.Status.DRBD.Config.NodeId).NotTo(BeNil())
				Expect(*updatedRVR.Status.DRBD.Config.NodeId).To(Equal(uint(rvrstatusconfignodeid.MinNodeID)))
			})

			It("resets invalid nodeID and reassigns valid one", func(ctx SpecContext) {
				// Step 1: Ensure parent rvr-1 has nodeID 0
				Expect(rec.Reconcile(ctx, RequestFor(rvr))).ToNot(Requeue())

				// Step 2: Create 6 more replicas with valid nodeIDs (1-6), leaving nodeID 7 free
				for i := 1; i < 7; i++ {
					testRvr := createRVRWithNodeID(
						fmt.Sprintf("rvr-reset-%d", i+1),
						fmt.Sprintf("node-%d", i+1),
						uint(rvrstatusconfignodeid.MinNodeID+i),
					)
					Expect(cl.Create(ctx, testRvr)).To(Succeed())
					// Reconcile to ensure nodeID is properly set in status
					Expect(rec.Reconcile(ctx, RequestFor(testRvr))).ToNot(Requeue())
				}

				// Step 3: Create RVR with invalid nodeID (> MaxNodeID)
				// Total: 8 replicas (1 parent with nodeID 0 + 6 with nodeID 1-6 + 1 invalid) - within limit
				// But one has invalid nodeID that should be reset
				invalidNodeID := uint(rvrstatusconfignodeid.MaxNodeID + 1)
				rvrInvalid := createRVRWithNodeID("rvr-reset-invalid", "node-invalid", invalidNodeID)
				Expect(cl.Create(ctx, rvrInvalid)).To(Succeed())

				// Step 4: Reconcile rvr-invalid - should detect invalid nodeID, reset it, and assign free nodeID (7)
				Expect(rec.Reconcile(ctx, RequestFor(rvrInvalid))).ToNot(Requeue())

				// Verify that rvr-invalid got valid nodeID (7, which was free)
				expectedNodeID := uint(rvrstatusconfignodeid.MaxNodeID) // nodeID 7
				updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvrInvalid), updatedRVR)).To(Succeed())
				Expect(updatedRVR.Status.DRBD.Config.NodeId).NotTo(BeNil())
				Expect(*updatedRVR.Status.DRBD.Config.NodeId).To(Equal(expectedNodeID))
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
					Expect(rec.Reconcile(ctx, RequestFor(rvr))).Error().To(MatchError(listError))
				})
			})

			When("all nodeIDs are used", func() {
				BeforeEach(func() {
					// Don't create parent rvr for this test - we'll create all RVRs ourselves
					rvr = nil
				})

				JustBeforeEach(func(ctx SpecContext) {
					// Create replicas using all nodeIds from MinNodeID to MaxNodeID
					totalNodeIDs := rvrstatusconfignodeid.MaxNodeID - rvrstatusconfignodeid.MinNodeID + 1
					for i := 0; i < totalNodeIDs; i++ {
						testRvr := createRVRWithNodeID(
							fmt.Sprintf("rvr-allused-%d", i+1),
							fmt.Sprintf("node-%d", i+1),
							uint(rvrstatusconfignodeid.MinNodeID+i),
						)
						Expect(cl.Create(ctx, testRvr)).To(Succeed())
					}
				})

				It("returns error when all nodeIDs are used and assigns freed nodeID after deletion", func(ctx SpecContext) {
					// Step 1: Create next RVR without nodeID - should fail (too many replicas / all nodeIDs used)
					rvr9 := createRVR("rvr-allused-9", "volume-1", "node-9")
					Expect(cl.Create(ctx, rvr9)).To(Succeed())

					_, err := rec.Reconcile(ctx, RequestFor(rvr9))
					Expect(err).To(HaveOccurred())
					Expect(errors.Is(err, e.ErrInvalidCluster)).To(BeTrue())

					// Step 2: Delete one RVR (e.g., rvr-allused-4 with nodeID=MinNodeID+3) - frees that nodeID
					freedNodeID := uint(rvrstatusconfignodeid.MinNodeID + 3)
					rvr4 := &v1alpha3.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-allused-4"}, rvr4)).To(Succeed())
					Expect(cl.Delete(ctx, rvr4)).To(Succeed())

					// Step 3: Reconcile rvr-9 again - should now get the freed nodeID (lowest available)
					Expect(rec.Reconcile(ctx, RequestFor(rvr9))).ToNot(Requeue())

					// Verify rvr-9 got the freed nodeID (which is the lowest available after deletion)
					updatedRVR9 := &v1alpha3.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr9), updatedRVR9)).To(Succeed())
					Expect(updatedRVR9.Status).NotTo(BeNil())
					Expect(updatedRVR9.Status.DRBD).NotTo(BeNil())
					Expect(updatedRVR9.Status.DRBD.Config).NotTo(BeNil())
					Expect(updatedRVR9.Status.DRBD.Config.NodeId).NotTo(BeNil())
					Expect(*updatedRVR9.Status.DRBD.Config.NodeId).To(Equal(freedNodeID))
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
				Expect(rec.Reconcile(ctx, RequestFor(rvr))).Error().To(MatchError(patchError))
			})
		})

	})
})
