package rvstatusconfigdeviceminor_test

import (
	"context"
	"fmt"
	"log/slog"
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
	rvstatusconfigdeviceminor "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_status_config_device_minor"
)

func newFakeClient() client.Client {
	scheme := runtime.NewScheme()
	_ = v1alpha3.AddToScheme(scheme)

	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha3.ReplicatedVolume{}).
		Build()
}

func createRV(name string) *v1alpha3.ReplicatedVolume {
	return &v1alpha3.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1alpha3.ReplicatedVolumeSpec{
			// Spec fields as needed
		},
	}
}

func createRVWithDeviceMinor(name string, deviceMinor uint) *v1alpha3.ReplicatedVolume {
	rv := createRV(name)
	rv.Status = &v1alpha3.ReplicatedVolumeStatus{
		DRBD: &v1alpha3.DRBDResource{
			Config: &v1alpha3.DRBDResourceConfig{
				DeviceMinor: deviceMinor,
			},
		},
	}
	return rv
}

var _ = Describe("Reconciler", func() {
	var cl client.Client
	var rec *rvstatusconfigdeviceminor.Reconciler

	BeforeEach(func() {
		cl = newFakeClient()
		rec = &rvstatusconfigdeviceminor.Reconciler{
			Cl:     cl,
			Log:    slog.Default(),
			LogAlt: GinkgoLogr,
		}
	})

	It("assigns deviceMinor to first volume", func(ctx context.Context) {
		// Arrange
		rv := createRV("volume-1")
		Expect(cl.Create(ctx, rv)).To(Succeed())

		// Act
		_, err := rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "volume-1"},
		})

		// Assert
		Expect(err).NotTo(HaveOccurred())

		updatedRV := &v1alpha3.ReplicatedVolume{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: "volume-1"}, updatedRV)).To(Succeed())
		Expect(updatedRV.Status).NotTo(BeNil())
		Expect(updatedRV.Status.DRBD).NotTo(BeNil())
		Expect(updatedRV.Status.DRBD.Config).NotTo(BeNil())
		Expect(updatedRV.Status.DRBD.Config.DeviceMinor).To(Equal(uint(0)))
	})

	It("assigns unique deviceMinors to multiple volumes", func(ctx context.Context) {
		// Arrange
		rv1 := createRVWithDeviceMinor("volume-1", 0)
		rv2 := createRVWithDeviceMinor("volume-2", 1)
		rv3 := createRV("volume-3")
		Expect(cl.Create(ctx, rv1)).To(Succeed())
		Expect(cl.Create(ctx, rv2)).To(Succeed())
		Expect(cl.Create(ctx, rv3)).To(Succeed())

		// Act
		_, err := rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "volume-3"},
		})

		// Assert
		Expect(err).NotTo(HaveOccurred())

		updatedRV := &v1alpha3.ReplicatedVolume{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: "volume-3"}, updatedRV)).To(Succeed())
		Expect(updatedRV.Status.DRBD.Config.DeviceMinor).To(Equal(uint(2)))
	})

	It("assigns unique deviceMinors", func(ctx context.Context) {
		// Arrange - Create 5 volumes with deviceMinors 0, 1, 2, 3, 4
		for i := 0; i < 5; i++ {
			rv := createRVWithDeviceMinor(
				fmt.Sprintf("volume-%d", i+1),
				uint(i),
			)
			Expect(cl.Create(ctx, rv)).To(Succeed())
		}
		// Add one more without deviceMinor
		rv6 := createRV("volume-6")
		Expect(cl.Create(ctx, rv6)).To(Succeed())

		// Act
		_, err := rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "volume-6"},
		})

		// Assert
		Expect(err).NotTo(HaveOccurred())

		updatedRV := &v1alpha3.ReplicatedVolume{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: "volume-6"}, updatedRV)).To(Succeed())
		Expect(updatedRV.Status.DRBD.Config.DeviceMinor).NotTo(BeZero())
		Expect(updatedRV.Status.DRBD.Config.DeviceMinor).To(Equal(uint(5)))
	})

	It("does not reassign deviceMinor if already assigned", func(ctx context.Context) {
		// Arrange
		rv := createRVWithDeviceMinor("volume-1", 42)
		Expect(cl.Create(ctx, rv)).To(Succeed())

		// Act
		_, err := rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "volume-1"},
		})

		// Assert
		Expect(err).NotTo(HaveOccurred())

		updatedRV := &v1alpha3.ReplicatedVolume{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: "volume-1"}, updatedRV)).To(Succeed())
		Expect(updatedRV.Status.DRBD.Config.DeviceMinor).To(Equal(uint(42)))
	})

	It("returns no error when ReplicatedVolume does not exist", func(ctx context.Context) {
		// Act
		_, err := rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "non-existent"},
		})

		// Assert
		Expect(err).NotTo(HaveOccurred())
	})

	It("fills gaps in deviceMinors", func(ctx context.Context) {
		// Arrange - Create volumes with deviceMinors 0, 2, 3 (gap at 1)
		rv1 := createRVWithDeviceMinor("volume-1", 0)
		rv2 := createRVWithDeviceMinor("volume-2", 2)
		rv3 := createRVWithDeviceMinor("volume-3", 3)
		rv4 := createRV("volume-4")
		Expect(cl.Create(ctx, rv1)).To(Succeed())
		Expect(cl.Create(ctx, rv2)).To(Succeed())
		Expect(cl.Create(ctx, rv3)).To(Succeed())
		Expect(cl.Create(ctx, rv4)).To(Succeed())

		// Act
		_, err := rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "volume-4"},
		})

		// Assert
		Expect(err).NotTo(HaveOccurred())

		// Verify deviceMinor was assigned (should be 1, filling the gap)
		updatedRV := &v1alpha3.ReplicatedVolume{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: "volume-4"}, updatedRV)).To(Succeed())
		Expect(updatedRV.Status.DRBD.Config.DeviceMinor).NotTo(BeZero())
		Expect(updatedRV.Status.DRBD.Config.DeviceMinor).To(Equal(uint(1)))
	})

	It("assigns deviceMinor when status is nil", func(ctx context.Context) {
		// Arrange
		rv := &v1alpha3.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name: "volume-1",
			},
			Spec: v1alpha3.ReplicatedVolumeSpec{},
			// Status is nil
		}
		Expect(cl.Create(ctx, rv)).To(Succeed())

		// Act
		_, err := rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "volume-1"},
		})

		// Assert
		Expect(err).NotTo(HaveOccurred())

		updatedRV := &v1alpha3.ReplicatedVolume{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: "volume-1"}, updatedRV)).To(Succeed())
		Expect(updatedRV.Status).NotTo(BeNil())
		Expect(updatedRV.Status.DRBD).NotTo(BeNil())
		Expect(updatedRV.Status.DRBD.Config).NotTo(BeNil())
		Expect(updatedRV.Status.DRBD.Config.DeviceMinor).To(Equal(uint(0)))
	})

	It("assigns deviceMinor when config is nil", func(ctx context.Context) {
		// Arrange
		rv := &v1alpha3.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name: "volume-1",
			},
			Spec:   v1alpha3.ReplicatedVolumeSpec{},
			Status: &v1alpha3.ReplicatedVolumeStatus{
				// Config is nil
			},
		}
		Expect(cl.Create(ctx, rv)).To(Succeed())

		// Act
		_, err := rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "volume-1"},
		})

		// Assert
		Expect(err).NotTo(HaveOccurred())

		updatedRV := &v1alpha3.ReplicatedVolume{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: "volume-1"}, updatedRV)).To(Succeed())
		Expect(updatedRV.Status.DRBD).NotTo(BeNil())
		Expect(updatedRV.Status.DRBD.Config).NotTo(BeNil())
		Expect(updatedRV.Status.DRBD.Config.DeviceMinor).To(Equal(uint(0)))
	})
})

func TestReconciler(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Reconciler Suite")
}
