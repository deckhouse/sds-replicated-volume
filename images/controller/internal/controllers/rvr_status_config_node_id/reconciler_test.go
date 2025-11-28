package rvrstatusconfignodeid_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
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

func newFakeClient() client.Client {
	scheme := runtime.NewScheme()
	_ = v1alpha3.AddToScheme(scheme)

	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha3.ReplicatedVolumeReplica{}).
		Build()
}

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

//nolint:unparam // volumeName is used in tests with different volumes
func createRVRWithNodeID(name, volumeName, nodeName string, nodeID uint) *v1alpha3.ReplicatedVolumeReplica {
	rvr := createRVR(name, volumeName, nodeName)
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
		rec = &rvrstatusconfignodeid.Reconciler{
			Cl:     cl,
			Log:    slog.Default(),
			LogAlt: GinkgoLogr,
		}
	})

	It("assigns nodeID to first replica", func(ctx context.Context) {
		// Arrange
		rvr := createRVR("rvr-1", "volume-1", "node-1")
		Expect(cl.Create(ctx, rvr)).To(Succeed())

		// Act
		_, err := rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "rvr-1"},
		})

		// Assert
		Expect(err).NotTo(HaveOccurred())

		updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-1"}, updatedRVR)).To(Succeed())
		Expect(updatedRVR.Status).NotTo(BeNil())
		Expect(updatedRVR.Status.Config).NotTo(BeNil())
		Expect(updatedRVR.Status.Config.NodeId).NotTo(BeNil())
		Expect(*updatedRVR.Status.Config.NodeId).To(Equal(uint(0)))
	})

	It("assigns nodeID to multiple replicas sequentially", func(ctx context.Context) {
		// Arrange
		rvr1 := createRVRWithNodeID("rvr-1", "volume-1", "node-1", 0)
		rvr2 := createRVRWithNodeID("rvr-2", "volume-1", "node-2", 1)
		rvr3 := createRVR("rvr-3", "volume-1", "node-3")
		Expect(cl.Create(ctx, rvr1)).To(Succeed())
		Expect(cl.Create(ctx, rvr2)).To(Succeed())
		Expect(cl.Create(ctx, rvr3)).To(Succeed())

		// Act
		_, err := rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "rvr-3"},
		})

		// Assert
		Expect(err).NotTo(HaveOccurred())

		updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-3"}, updatedRVR)).To(Succeed())
		Expect(updatedRVR.Status).NotTo(BeNil())
		Expect(updatedRVR.Status.Config).NotTo(BeNil())
		Expect(updatedRVR.Status.Config.NodeId).NotTo(BeNil())
		Expect(*updatedRVR.Status.Config.NodeId).To(Equal(uint(2)))
	})

	It("assigns unique nodeIDs", func(ctx context.Context) {
		// Arrange - Create 5 replicas with nodeIds 0, 1, 2, 3, 4
		for i := 0; i < 5; i++ {
			rvr := createRVRWithNodeID(
				fmt.Sprintf("rvr-%d", i+1),
				"volume-1",
				fmt.Sprintf("node-%d", i+1),
				uint(i),
			)
			Expect(cl.Create(ctx, rvr)).To(Succeed())
		}
		// Add one more without nodeId
		rvr6 := createRVR("rvr-6", "volume-1", "node-6")
		Expect(cl.Create(ctx, rvr6)).To(Succeed())

		// Act
		_, err := rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "rvr-6"},
		})

		// Assert
		Expect(err).NotTo(HaveOccurred())

		updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-6"}, updatedRVR)).To(Succeed())
		Expect(updatedRVR.Status.Config.NodeId).NotTo(BeNil())
		Expect(*updatedRVR.Status.Config.NodeId).To(Equal(uint(5)))
	})

	It("does not reassign nodeID if already assigned", func(ctx context.Context) {
		// Arrange
		rvr := createRVRWithNodeID("rvr-1", "volume-1", "node-1", 3)
		Expect(cl.Create(ctx, rvr)).To(Succeed())

		// Act
		_, err := rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "rvr-1"},
		})

		// Assert
		Expect(err).NotTo(HaveOccurred())

		updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-1"}, updatedRVR)).To(Succeed())
		Expect(updatedRVR.Status.Config.NodeId).NotTo(BeNil())
		Expect(*updatedRVR.Status.Config.NodeId).To(Equal(uint(3)))
	})

	It("returns error when too many replicas", func(ctx context.Context) {
		// Arrange - Create 9 replicas (max is 8: 0-7)
		for i := 0; i < 8; i++ {
			rvr := createRVRWithNodeID(
				fmt.Sprintf("rvr-%d", i+1),
				"volume-1",
				fmt.Sprintf("node-%d", i+1),
				uint(i),
			)
			Expect(cl.Create(ctx, rvr)).To(Succeed())
		}
		rvr9 := createRVR("rvr-9", "volume-1", "node-9")
		Expect(cl.Create(ctx, rvr9)).To(Succeed())

		// Act
		_, err := rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "rvr-9"},
		})

		// Assert
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, e.ErrInvalidCluster)).To(BeTrue())

		// Verify that ConfigurationAdjusted condition was set to False
		updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-9"}, updatedRVR)).To(Succeed())
		cond := meta.FindStatusCondition(updatedRVR.Status.Conditions, v1alpha3.ConditionTypeConfigurationAdjusted)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha3.ReasonConfigurationFailed))
		Expect(cond.Message).NotTo(BeEmpty())
	})

	It("returns error when all nodeIDs are used", func(ctx context.Context) {
		// Arrange - Create 8 replicas using all nodeIds 0-7
		for i := 0; i < 8; i++ {
			rvr := createRVRWithNodeID(
				fmt.Sprintf("rvr-%d", i+1),
				"volume-1",
				fmt.Sprintf("node-%d", i+1),
				uint(i),
			)
			Expect(cl.Create(ctx, rvr)).To(Succeed())
		}
		// Add one more without nodeId (should fail)
		rvr9 := createRVR("rvr-9", "volume-1", "node-9")
		Expect(cl.Create(ctx, rvr9)).To(Succeed())

		// Act
		_, err := rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "rvr-9"},
		})

		// Assert
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, e.ErrInvalidCluster)).To(BeTrue())

		// Verify that ConfigurationAdjusted condition was set to False
		updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-9"}, updatedRVR)).To(Succeed())
		cond := meta.FindStatusCondition(updatedRVR.Status.Conditions, v1alpha3.ConditionTypeConfigurationAdjusted)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha3.ReasonConfigurationFailed))
		Expect(cond.Message).NotTo(BeEmpty())
	})

	It("returns no error when ReplicatedVolumeReplica does not exist", func(ctx context.Context) {
		// Act
		_, err := rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "non-existent"},
		})

		// Assert
		Expect(err).NotTo(HaveOccurred())
	})

	It("assigns nodeID independently for different volumes", func(ctx context.Context) {
		// Arrange - Create replicas for different volumes - they should not interfere
		rvr1 := createRVRWithNodeID("rvr-1", "volume-1", "node-1", 0)
		rvr2 := createRVRWithNodeID("rvr-2", "volume-1", "node-2", 1)
		rvr3 := createRVR("rvr-3", "volume-2", "node-3") // Different volume
		Expect(cl.Create(ctx, rvr1)).To(Succeed())
		Expect(cl.Create(ctx, rvr2)).To(Succeed())
		Expect(cl.Create(ctx, rvr3)).To(Succeed())

		// Act
		_, err := rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "rvr-3"},
		})

		// Assert
		Expect(err).NotTo(HaveOccurred())

		// Verify nodeId was assigned (should be 0, as volume-2 has no replicas with nodeId)
		updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-3"}, updatedRVR)).To(Succeed())
		Expect(updatedRVR.Status.Config.NodeId).NotTo(BeNil())
		Expect(*updatedRVR.Status.Config.NodeId).To(Equal(uint(0)))
	})

	It("fills gaps in nodeIDs", func(ctx context.Context) {
		// Arrange - Create replicas with nodeIds 0, 2, 3 (gap at 1)
		rvr1 := createRVRWithNodeID("rvr-1", "volume-1", "node-1", 0)
		rvr2 := createRVRWithNodeID("rvr-2", "volume-1", "node-2", 2)
		rvr3 := createRVRWithNodeID("rvr-3", "volume-1", "node-3", 3)
		rvr4 := createRVR("rvr-4", "volume-1", "node-4")
		Expect(cl.Create(ctx, rvr1)).To(Succeed())
		Expect(cl.Create(ctx, rvr2)).To(Succeed())
		Expect(cl.Create(ctx, rvr3)).To(Succeed())
		Expect(cl.Create(ctx, rvr4)).To(Succeed())

		// Act
		_, err := rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "rvr-4"},
		})

		// Assert
		Expect(err).NotTo(HaveOccurred())

		// Verify nodeId was assigned (should be 1, filling the gap)
		updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-4"}, updatedRVR)).To(Succeed())
		Expect(updatedRVR.Status.Config.NodeId).NotTo(BeNil())
		Expect(*updatedRVR.Status.Config.NodeId).To(Equal(uint(1)))
	})
})

func TestReconciler(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Reconciler Suite")
}
