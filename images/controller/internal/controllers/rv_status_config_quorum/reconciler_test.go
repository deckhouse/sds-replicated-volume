package rvrdiskfulcount_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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

var _ = Describe("Reconciler", func() {
	var cl client.Client
	var rec *rvcontroller.Reconciler
	BeforeEach(func() {
		cl = newFakeClient()
		rec = rvcontroller.NewReconciler(
			cl,
			cl,
			nil,
			GinkgoLogr,
		)
	})

	It("returns no error when ReplicatedVolume does not exist", func(ctx context.Context) {
		_, err := rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      "test-rv",
				Namespace: "",
			},
		})
		Expect(err).NotTo(HaveOccurred())
	})
})
