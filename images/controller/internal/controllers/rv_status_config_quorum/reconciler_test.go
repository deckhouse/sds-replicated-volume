package rvrdiskfulcount_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha3 "github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	rvcontroller "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_status_config_quorum"
)

func newFakeClient() client.Client {
	scheme := runtime.NewScheme()
	_ = v1alpha3.AddToScheme(scheme)

	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha3.ReplicatedVolumeReplica{}, &v1alpha3.ReplicatedVolume{}).
		Build()
}

func createReplicatedVolume(name string, ready bool) *v1alpha3.ReplicatedVolume {
	rv := &v1alpha3.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Status: &v1alpha3.ReplicatedVolumeStatus{
			Conditions: []metav1.Condition{},
		},
	}

	if ready {
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
	}

	return rv
}

func createReplicatedVolumeReplica(name, rvName, nodeName string, diskless bool) *v1alpha3.ReplicatedVolumeReplica {
	return &v1alpha3.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "storage.deckhouse.io/v1alpha3",
					Kind:       "ReplicatedVolume",
					Name:       rvName,
					Controller: func() *bool { b := true; return &b }(),
				},
			},
		},
		Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
			ReplicatedVolumeName: rvName,
			NodeName:             nodeName,
			Diskless:             diskless,
		},
	}
}

var _ = Describe("Reconciler", func() {
	var cl client.Client
	var rec *rvcontroller.Reconciler
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
		cl = newFakeClient()
		rec = rvcontroller.NewReconciler(
			cl,
			cl,
			nil,
			GinkgoLogr,
		)
	})

	It("returns no error when ReplicatedVolume does not exist", func() {
		_, err := rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      "test-rv",
				Namespace: "",
			},
		})
		Expect(err).NotTo(HaveOccurred())
	})

	Context("when ReplicatedVolume is not ready", func() {
		It("should do nothing and return no error", func() {
			rv := createReplicatedVolume("test-rv", false)
			Expect(cl.Create(ctx, rv)).To(Succeed())

			_, err := rec.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      "test-rv",
					Namespace: "",
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify no quorum config was set
			updatedRV := &v1alpha3.ReplicatedVolume{}
			Expect(cl.Get(ctx, types.NamespacedName{Name: "test-rv"}, updatedRV)).To(Succeed())
			Expect(updatedRV.Status.Config).To(BeNil())
		})
	})

	Context("when ReplicatedVolume is ready", func() {
		It("should reconcile successfully when RV is ready with RVRs", func() {
			rv := createReplicatedVolume("test-rv", true)
			rv.Status.Config = &v1alpha3.DRBDResourceConfig{}
			Expect(cl.Create(ctx, rv)).To(Succeed())

			// Create diskful replicas
			rvr1 := createReplicatedVolumeReplica("rvr-1", "test-rv", "node-1", false)
			rvr2 := createReplicatedVolumeReplica("rvr-2", "test-rv", "node-2", false)
			Expect(cl.Create(ctx, rvr1)).To(Succeed())
			Expect(cl.Create(ctx, rvr2)).To(Succeed())

			_, err := rec.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      "test-rv",
					Namespace: "",
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify finalizers were added to RVRs
			updatedRVR1 := &v1alpha3.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, types.NamespacedName{Name: "rvr-1"}, updatedRVR1)).To(Succeed())
			Expect(updatedRVR1.Finalizers).To(ContainElement("quorum-reconf"))
		})

		It("should handle multiple replicas with diskful and diskless", func() {
			rv := createReplicatedVolume("test-rv", true)
			rv.Status.Config = &v1alpha3.DRBDResourceConfig{}
			Expect(cl.Create(ctx, rv)).To(Succeed())

			// Create 3 diskful and 1 diskless replica
			rvr1 := createReplicatedVolumeReplica("rvr-1", "test-rv", "node-1", false)
			rvr2 := createReplicatedVolumeReplica("rvr-2", "test-rv", "node-2", false)
			rvr3 := createReplicatedVolumeReplica("rvr-3", "test-rv", "node-3", false)
			rvr4 := createReplicatedVolumeReplica("rvr-4", "test-rv", "node-4", true)
			Expect(cl.Create(ctx, rvr1)).To(Succeed())
			Expect(cl.Create(ctx, rvr2)).To(Succeed())
			Expect(cl.Create(ctx, rvr3)).To(Succeed())
			Expect(cl.Create(ctx, rvr4)).To(Succeed())

			_, err := rec.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      "test-rv",
					Namespace: "",
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify all RVRs got finalizers
			for _, name := range []string{"rvr-1", "rvr-2", "rvr-3", "rvr-4"} {
				rvr := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, types.NamespacedName{Name: name}, rvr)).To(Succeed())
				Expect(rvr.Finalizers).To(ContainElement("quorum-reconf"))
			}
		})

		It("should not set quorum when diskfulCount <= 1", func() {
			rv := createReplicatedVolume("test-rv", true)
			// Initialize Status.Config to ensure patch works correctly
			rv.Status.Config = &v1alpha3.DRBDResourceConfig{}
			Expect(cl.Create(ctx, rv)).To(Succeed())

			// Create only 1 diskful replica
			rvr1 := createReplicatedVolumeReplica("rvr-1", "test-rv", "node-1", false)
			Expect(cl.Create(ctx, rvr1)).To(Succeed())

			_, err := rec.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      "test-rv",
					Namespace: "",
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify quorum is 0 (not set)
			updatedRV := &v1alpha3.ReplicatedVolume{}
			Expect(cl.Get(ctx, types.NamespacedName{Name: "test-rv"}, updatedRV)).To(Succeed())
			Expect(updatedRV.Status).NotTo(BeNil())
			Expect(updatedRV.Status.Config).NotTo(BeNil())
			Expect(updatedRV.Status.Config.Quorum).To(Equal(byte(0)))
			Expect(updatedRV.Status.Config.QuorumMinimumRedundancy).To(Equal(byte(0)))
		})
	})
})

func findCondition(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}
