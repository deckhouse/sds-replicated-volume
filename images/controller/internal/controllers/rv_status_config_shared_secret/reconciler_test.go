package rvstatusconfigsharedsecret_test

import (
	"context"
	"log/slog"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha3 "github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	rvstatusconfigsharedsecret "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_status_config_shared_secret"
)

func newFakeClient() client.Client {
	scheme := runtime.NewScheme()
	_ = v1alpha3.AddToScheme(scheme)

	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha3.ReplicatedVolume{}).
		Build()
}

//nolint:unparam // name is used for different test cases
func createRV(name string) *v1alpha3.ReplicatedVolume {
	return &v1alpha3.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1alpha3.ReplicatedVolumeSpec{
			Size:                       parseQuantity("10Gi"),
			ReplicatedStorageClassName: "test-storage-class",
		},
	}
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

func createRVRWithUnsupportedAlgorithm(name, volumeName, nodeName string) *v1alpha3.ReplicatedVolumeReplica {
	rvr := createRVR(name, volumeName, nodeName)
	rvr.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{
		Conditions: []metav1.Condition{
			{
				Type:               v1alpha3.ConditionTypeConfigurationAdjusted,
				Status:             metav1.ConditionFalse,
				Reason:             "UnsupportedAlgorithm",
				Message:            "Algorithm not supported",
				ObservedGeneration: 1,
				LastTransitionTime: metav1.Now(),
			},
		},
	}
	return rvr
}

func parseQuantity(s string) resource.Quantity {
	q, _ := resource.ParseQuantity(s)
	return q
}

var _ = Describe("Reconciler", func() {
	var cl client.Client
	var rec *rvstatusconfigsharedsecret.Reconciler

	BeforeEach(func() {
		cl = newFakeClient()
		rec = &rvstatusconfigsharedsecret.Reconciler{
			Cl:     cl,
			Log:    slog.Default(),
			LogAlt: GinkgoLogr,
		}
	})

	It("generates shared secret initially", func(ctx context.Context) {
		// Arrange
		rv := createRV("test-rv")
		Expect(cl.Create(ctx, rv)).To(Succeed())

		// Act
		_, err := rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "test-rv"},
		})

		// Assert
		Expect(err).NotTo(HaveOccurred())

		// Verify shared secret was generated
		updatedRV := &v1alpha3.ReplicatedVolume{}
		Expect(cl.Get(ctx, types.NamespacedName{Name: "test-rv"}, updatedRV)).To(Succeed())
		Expect(updatedRV.Status).NotTo(BeNil())
		Expect(updatedRV.Status.Config).NotTo(BeNil())
		Expect(updatedRV.Status.Config.SharedSecret).NotTo(BeEmpty())
		Expect(updatedRV.Status.Config.SharedSecretAlg).To(Equal("sha256"))

		// Verify condition
		cond := meta.FindStatusCondition(updatedRV.Status.Conditions, "SharedSecretAlgorithmSelected")
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal("AlgorithmSelected"))
	})

	It("switches to next algorithm when UnsupportedAlgorithm error occurs", func(ctx context.Context) {
		// Arrange
		rv := createRV("test-rv")
		rv.Status = &v1alpha3.ReplicatedVolumeStatus{
			Config: &v1alpha3.DRBDResourceConfig{
				SharedSecret:    "test-secret",
				SharedSecretAlg: "sha256",
			},
		}
		Expect(cl.Create(ctx, rv)).To(Succeed())

		rvr := createRVRWithUnsupportedAlgorithm("test-rvr", "test-rv", "node-1")
		Expect(cl.Create(ctx, rvr)).To(Succeed())

		// Act
		_, err := rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "test-rv"},
		})

		// Assert
		Expect(err).NotTo(HaveOccurred())

		// Verify algorithm was switched
		updatedRV := &v1alpha3.ReplicatedVolume{}
		Expect(cl.Get(ctx, types.NamespacedName{Name: "test-rv"}, updatedRV)).To(Succeed())
		Expect(updatedRV.Status.Config.SharedSecretAlg).To(Equal("sha1"))
		Expect(updatedRV.Status.Config.SharedSecret).NotTo(Equal("test-secret"))

		// Verify condition
		cond := meta.FindStatusCondition(updatedRV.Status.Conditions, "SharedSecretAlgorithmSelected")
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
	})

	It("sets condition to False when all algorithms are exhausted", func(ctx context.Context) {
		// Arrange
		rv := createRV("test-rv")
		rv.Status = &v1alpha3.ReplicatedVolumeStatus{
			Config: &v1alpha3.DRBDResourceConfig{
				SharedSecret:    "test-secret",
				SharedSecretAlg: "sha1", // Last algorithm
			},
		}
		Expect(cl.Create(ctx, rv)).To(Succeed())

		rvr := createRVRWithUnsupportedAlgorithm("test-rvr", "test-rv", "node-1")
		Expect(cl.Create(ctx, rvr)).To(Succeed())

		// Act
		_, err := rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "test-rv"},
		})

		// Assert
		Expect(err).NotTo(HaveOccurred())

		// Verify condition is set to False
		updatedRV := &v1alpha3.ReplicatedVolume{}
		Expect(cl.Get(ctx, types.NamespacedName{Name: "test-rv"}, updatedRV)).To(Succeed())

		cond := meta.FindStatusCondition(updatedRV.Status.Conditions, "SharedSecretAlgorithmSelected")
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal("UnableToSelectSharedSecretAlgorithm"))
	})

	It("does nothing when no UnsupportedAlgorithm errors are present", func(ctx context.Context) {
		// Arrange
		rv := createRV("test-rv")
		rv.Status = &v1alpha3.ReplicatedVolumeStatus{
			Config: &v1alpha3.DRBDResourceConfig{
				SharedSecret:    "test-secret",
				SharedSecretAlg: "sha256",
			},
		}
		Expect(cl.Create(ctx, rv)).To(Succeed())

		rvr := createRVR("test-rvr", "test-rv", "node-1")
		Expect(cl.Create(ctx, rvr)).To(Succeed())

		// Act
		_, err := rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "test-rv"},
		})

		// Assert
		Expect(err).NotTo(HaveOccurred())

		// Verify nothing changed
		updatedRV := &v1alpha3.ReplicatedVolume{}
		Expect(cl.Get(ctx, types.NamespacedName{Name: "test-rv"}, updatedRV)).To(Succeed())
		Expect(updatedRV.Status.Config.SharedSecret).To(Equal("test-secret"))
		Expect(updatedRV.Status.Config.SharedSecretAlg).To(Equal("sha256"))
	})

	It("returns no error when ReplicatedVolume does not exist", func(ctx context.Context) {
		// Act
		_, err := rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "non-existent"},
		})

		// Assert
		Expect(err).NotTo(HaveOccurred())
	})
})

func TestReconciler(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Reconciler Suite")
}
