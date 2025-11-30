// cspell:words Logr Subresource apimachinery gomega metav onsi

package rvrstatusconfignodeid_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha3 "github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	rvrstatusconfignodeid "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_status_config_node_id"
	e "github.com/deckhouse/sds-replicated-volume/images/controller/internal/errors"
)

func newScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = v1alpha3.AddToScheme(scheme)
	return scheme
}

func newFakeClient() client.Client {
	return fake.NewClientBuilder().
		WithScheme(newScheme()).
		WithStatusSubresource(&v1alpha3.ReplicatedVolumeReplica{}).
		Build()
}

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
		Config: &v1alpha3.DRBDConfig{
			NodeId: &nodeID,
		},
	}
	return rvr
}

var _ = Describe("Reconciler", func() {
	var cl client.Client
	var rec *rvrstatusconfignodeid.Reconciler

	BeforeEach(func() {
		cl = newFakeClient()
		rec = rvrstatusconfignodeid.NewReconciler(cl, GinkgoLogr)
	})

	// Test case: Non-existent RVR handling (spec: trigger handling)
	// Reconcile should handle non-existent RVR gracefully (deleted or never existed)
	It("returns no error when ReplicatedVolumeReplica does not exist", func(ctx context.Context) {
		_, err := rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "non-existent"},
		})
		Expect(err).NotTo(HaveOccurred())
	})

	When("RVR created", func() {
		When("RVR without nodeID", func() {
			// Test case: First replica assignment (spec: unique nodeId in range [0; 7])
			// First RVR in a volume should get nodeID MinNodeID
			It("assigns nodeID to first replica", func(ctx context.Context) {
				rvr := createRVR("rvr-1", "volume-1", "node-1")
				Expect(cl.Create(ctx, rvr)).To(Succeed())

				_, err := rec.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: "rvr-1"},
				})
				Expect(err).NotTo(HaveOccurred())

				updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-1"}, updatedRVR)).To(Succeed())
				Expect(updatedRVR.Status.Config.NodeId).NotTo(BeNil())
				Expect(*updatedRVR.Status.Config.NodeId).To(Equal(uint(rvrstatusconfignodeid.MinNodeID)))
			})

			// Test case: Sequential assignment and uniqueness (spec: unique nodeId among all replicas of one RV)
			// When multiple RVRs exist, new RVR should get the next available nodeID
			// Each RVR in the same volume must have a unique nodeID
			It("assigns nodeID sequentially and ensures uniqueness", func(ctx context.Context) {
				// Create 5 replicas with nodeIds starting from MinNodeID
				for i := 0; i < 5; i++ {
					rvr := createRVRWithNodeID(
						fmt.Sprintf("rvr-%d", i+1),
						fmt.Sprintf("node-%d", i+1),
						uint(rvrstatusconfignodeid.MinNodeID+i),
					)
					Expect(cl.Create(ctx, rvr)).To(Succeed())
				}
				// Add one more without nodeId - should get next sequential nodeID
				rvr6 := createRVR("rvr-6", "volume-1", "node-6")
				Expect(cl.Create(ctx, rvr6)).To(Succeed())

				_, err := rec.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: "rvr-6"},
				})
				Expect(err).NotTo(HaveOccurred())

				updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-6"}, updatedRVR)).To(Succeed())
				Expect(updatedRVR.Status.Config.NodeId).NotTo(BeNil())
				Expect(*updatedRVR.Status.Config.NodeId).To(Equal(uint(rvrstatusconfignodeid.MinNodeID + 5)))
			})

			// Test case: Volume isolation (spec: unique nodeId among all replicas of one RV)
			// nodeIDs are scoped per volume - different volumes can have RVRs with the same nodeID
			It("isolates nodeIDs by volume", func(ctx context.Context) {
				// Create RVRs in volume-1 with nodeIDs MinNodeID, MinNodeID+1
				rvr1 := createRVRWithNodeID("rvr-1", "node-1", uint(rvrstatusconfignodeid.MinNodeID))
				rvr2 := createRVRWithNodeID("rvr-2", "node-2", uint(rvrstatusconfignodeid.MinNodeID+1))
				Expect(cl.Create(ctx, rvr1)).To(Succeed())
				Expect(cl.Create(ctx, rvr2)).To(Succeed())

				// Create RVR in volume-2 - should get nodeID MinNodeID (independent of volume-1)
				rvr3 := createRVR("rvr-3", "volume-2", "node-3")
				Expect(cl.Create(ctx, rvr3)).To(Succeed())

				_, err := rec.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: "rvr-3"},
				})
				Expect(err).NotTo(HaveOccurred())

				updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-3"}, updatedRVR)).To(Succeed())
				Expect(updatedRVR.Status.Config.NodeId).NotTo(BeNil())
				Expect(*updatedRVR.Status.Config.NodeId).To(Equal(uint(rvrstatusconfignodeid.MinNodeID))) // volume-2 is independent
			})

			// Test case: Gap filling algorithm (spec: unique nodeId in range [0; 7])
			// When there are gaps in nodeIDs (e.g., MinNodeID, MinNodeID+2, MinNodeID+3), new RVR should get the first gap (MinNodeID+1)
			It("fills gaps in nodeIDs", func(ctx context.Context) {
				rvr1 := createRVRWithNodeID("rvr-1", "node-1", uint(rvrstatusconfignodeid.MinNodeID))
				rvr2 := createRVRWithNodeID("rvr-2", "node-2", uint(rvrstatusconfignodeid.MinNodeID+2))
				rvr3 := createRVRWithNodeID("rvr-3", "node-3", uint(rvrstatusconfignodeid.MinNodeID+3))
				rvr4 := createRVR("rvr-4", "volume-1", "node-4")
				Expect(cl.Create(ctx, rvr1)).To(Succeed())
				Expect(cl.Create(ctx, rvr2)).To(Succeed())
				Expect(cl.Create(ctx, rvr3)).To(Succeed())
				Expect(cl.Create(ctx, rvr4)).To(Succeed())

				_, err := rec.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: "rvr-4"},
				})
				Expect(err).NotTo(HaveOccurred())

				updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-4"}, updatedRVR)).To(Succeed())
				Expect(updatedRVR.Status.Config.NodeId).NotTo(BeNil())
				Expect(*updatedRVR.Status.Config.NodeId).To(Equal(uint(rvrstatusconfignodeid.MinNodeID + 1))) // filled the gap
			})

			// Test case: Idempotency (spec: trigger CREATE(RVR, status.config.nodeId==nil))
			// RVR with nodeID already assigned should not be reassigned
			It("does not reassign nodeID if already assigned", func(ctx context.Context) {
				testNodeID := uint(rvrstatusconfignodeid.MinNodeID + 3)
				rvr := createRVRWithNodeID("rvr-1", "node-1", testNodeID)
				Expect(cl.Create(ctx, rvr)).To(Succeed())

				_, err := rec.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: "rvr-1"},
				})
				Expect(err).NotTo(HaveOccurred())

				updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-1"}, updatedRVR)).To(Succeed())
				Expect(updatedRVR.Status.Config.NodeId).NotTo(BeNil())
				Expect(*updatedRVR.Status.Config.NodeId).To(Equal(testNodeID))

				// Reconcile again - should be idempotent
				_, err = rec.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: "rvr-1"},
				})
				Expect(err).NotTo(HaveOccurred())

				Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-1"}, updatedRVR)).To(Succeed())
				Expect(updatedRVR.Status.Config.NodeId).NotTo(BeNil())
				Expect(*updatedRVR.Status.Config.NodeId).To(Equal(testNodeID)) // still same, not reassigned
			})

			// Test case: Invalid nodeID handling (spec: nodeId in range [0; 7])
			// RVR with nodeID outside valid range should be ignored when collecting used nodeIDs
			It("ignores nodeID outside valid range", func(ctx context.Context) {
				// Create RVR with nodeID > MaxNodeID (should be ignored)
				invalidNodeID := uint(rvrstatusconfignodeid.MaxNodeID + 1)
				rvr1 := createRVRWithNodeID("rvr-1", "node-1", invalidNodeID)
				Expect(cl.Create(ctx, rvr1)).To(Succeed())

				// Create new RVR without nodeID - should get nodeID MinNodeID (invalid nodeID was ignored)
				rvr2 := createRVR("rvr-2", "volume-1", "node-2")
				Expect(cl.Create(ctx, rvr2)).To(Succeed())

				_, err := rec.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: "rvr-2"},
				})
				Expect(err).NotTo(HaveOccurred())

				updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-2"}, updatedRVR)).To(Succeed())
				Expect(updatedRVR.Status.Config.NodeId).NotTo(BeNil())
				// Should get nodeID MinNodeID, because invalid nodeID was ignored
				Expect(*updatedRVR.Status.Config.NodeId).To(Equal(uint(rvrstatusconfignodeid.MinNodeID)))
			})

			// Test case: Invalid nodeID already set in RVR (spec: nodeId in range [0; 7])
			// RVR with nodeID > MaxNodeID already set should log error and continue to reassign
			It("logs error and reassigns when nodeID outside valid range is already set", func(ctx context.Context) {
				// Create RVR with nodeID > MaxNodeID already set
				invalidNodeID := uint(rvrstatusconfignodeid.MaxNodeID + 1)
				rvr := createRVRWithNodeID("rvr-1", "node-1", invalidNodeID)
				Expect(cl.Create(ctx, rvr)).To(Succeed())

				// Reconcile - should log error about invalid nodeID and reassign valid one
				_, err := rec.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: "rvr-1"},
				})
				Expect(err).NotTo(HaveOccurred())

				// Verify RVR got valid nodeID (MinNodeID)
				updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-1"}, updatedRVR)).To(Succeed())
				Expect(updatedRVR.Status.Config.NodeId).NotTo(BeNil())
				Expect(*updatedRVR.Status.Config.NodeId).To(Equal(uint(rvrstatusconfignodeid.MinNodeID)))
			})

			// Test case: Invalid nodeID reset when all valid nodeIDs are used
			// RVR with invalid nodeID should reset it, but if all valid nodeIDs are used, should return error
			// This tests the scenario: 8 RVR with valid nodeIDs (0-7) + 1 RVR with invalid nodeID (8)
			// After reset of invalid nodeID, all valid nodeIDs are still used, so should return error
			It("resets invalid nodeID but returns error when no available nodeIDs", func(ctx context.Context) {
				// Step 1: Create replicas using all valid nodeIds from MinNodeID to MaxNodeID (8 replicas)
				totalNodeIDs := rvrstatusconfignodeid.MaxNodeID - rvrstatusconfignodeid.MinNodeID + 1
				for i := 0; i < totalNodeIDs; i++ {
					rvr := createRVRWithNodeID(
						fmt.Sprintf("rvr-%d", i+1),
						fmt.Sprintf("node-%d", i+1),
						uint(rvrstatusconfignodeid.MinNodeID+i),
					)
					Expect(cl.Create(ctx, rvr)).To(Succeed())
				}

				// Step 2: Create 9th RVR with invalid nodeID (> MaxNodeID)
				// This creates 9 replicas total, which exceeds MaxNodeID+1 (8), so will return "too many replicas" error
				invalidNodeID := uint(rvrstatusconfignodeid.MaxNodeID + 1)
				rvr9 := createRVRWithNodeID("rvr-9", "node-9", invalidNodeID)
				Expect(cl.Create(ctx, rvr9)).To(Succeed())

				// Step 3: Reconcile rvr-9 - should detect invalid nodeID and reset it in memory,
				// but then fail because totalReplicas (9) > MaxNodeID+1 (8)
				// Note: The reset happens in memory, but error is returned before saving
				_, err := rec.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: "rvr-9"},
				})
				Expect(err).To(HaveOccurred())
				Expect(errors.Is(err, e.ErrInvalidCluster)).To(BeTrue())

				// Verify that rvr-9 still has invalid nodeID in API (reset was only in memory, not saved due to error)
				updatedRVR9 := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-9"}, updatedRVR9)).To(Succeed())
				// After error, nodeID is still invalid in API (reset wasn't saved because error was returned)
				// This is expected behavior - we don't save changes if we're going to return an error
				Expect(updatedRVR9.Status.Config.NodeId).NotTo(BeNil())
				Expect(*updatedRVR9.Status.Config.NodeId).To(Equal(invalidNodeID))
			})

			// Test case: Error handling and reuse of freed nodeID (spec: error on too many replicas, reuse after deletion)
			// When all nodeIDs (MinNodeID to MaxNodeID) are assigned and next RVR is created, it should fail
			// After deletion of one RVR, the waiting RVR should get the freed nodeID (lowest available)
			When("all nodeIDs are used", func() {
				It("returns error when all nodeIDs are used and assigns freed nodeID after deletion", func(ctx context.Context) {
					// Step 1: Create replicas using all nodeIds from MinNodeID to MaxNodeID
					totalNodeIDs := rvrstatusconfignodeid.MaxNodeID - rvrstatusconfignodeid.MinNodeID + 1
					for i := 0; i < totalNodeIDs; i++ {
						rvr := createRVRWithNodeID(
							fmt.Sprintf("rvr-%d", i+1),
							fmt.Sprintf("node-%d", i+1),
							uint(rvrstatusconfignodeid.MinNodeID+i),
						)
						Expect(cl.Create(ctx, rvr)).To(Succeed())
					}

					// Step 2: Create next RVR without nodeID - should fail (too many replicas / all nodeIDs used)
					rvr9 := createRVR("rvr-9", "volume-1", "node-9")
					Expect(cl.Create(ctx, rvr9)).To(Succeed())

					_, err := rec.Reconcile(ctx, reconcile.Request{
						NamespacedName: types.NamespacedName{Name: "rvr-9"},
					})
					Expect(err).To(HaveOccurred())
					Expect(errors.Is(err, e.ErrInvalidCluster)).To(BeTrue())

					// Step 3: Delete one RVR (e.g., rvr-4 with nodeID=MinNodeID+3) - frees that nodeID
					freedNodeID := uint(rvrstatusconfignodeid.MinNodeID + 3)
					rvr4 := &v1alpha3.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-4"}, rvr4)).To(Succeed())
					Expect(cl.Delete(ctx, rvr4)).To(Succeed())

					// Step 4: Reconcile rvr-9 again - should now get the freed nodeID (lowest available)
					_, err = rec.Reconcile(ctx, reconcile.Request{
						NamespacedName: types.NamespacedName{Name: "rvr-9"},
					})
					Expect(err).NotTo(HaveOccurred())

					// Verify rvr-9 got the freed nodeID (which is the lowest available after deletion)
					updatedRVR9 := &v1alpha3.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-9"}, updatedRVR9)).To(Succeed())
					Expect(updatedRVR9.Status).NotTo(BeNil())
					Expect(updatedRVR9.Status.Config).NotTo(BeNil())
					Expect(updatedRVR9.Status.Config.NodeId).NotTo(BeNil())
					Expect(*updatedRVR9.Status.Config.NodeId).To(Equal(freedNodeID))
				})
			})
		})

	})
})

func TestReconciler(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Reconciler Suite")
}
