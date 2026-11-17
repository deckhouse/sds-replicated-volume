/*
Copyright 2026 Flant JSC

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

package controller_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-common-lib/logger"
	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/oldcontroller/config"
	"github.com/deckhouse/sds-replicated-volume/images/oldcontroller/pkg/controller"
)

var _ = Describe("StorageClass volumesnapshotclass annotation", func() {
	const (
		thinPool  = "thin-pool"
		thickPool = "thick-pool"
	)

	var (
		ctx                       context.Context
		cl                        client.WithWatch
		scCreates, scDeletes      int
		countStorageClassRecreate bool
	)

	newRSC := func(name, pool string) *srv.ReplicatedStorageClass {
		return &srv.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: srv.ReplicatedStorageClassSpec{
				StoragePool:   pool,
				ReclaimPolicy: controller.ReclaimPolicyDelete,
				Replication:   controller.ReplicationNone,
				VolumeAccess:  controller.VolumeAccessPreferablyLocal,
				Topology:      controller.TopologyIgnored,
			},
		}
	}

	reconcileRSC := func(name string) {
		cfg, err := config.NewConfig()
		Expect(err).NotTo(HaveOccurred())
		shouldRequeue, err := controller.ReconcileReplicatedStorageClassEvent(ctx, cl, logger.NewNop(), cfg,
			reconcile.Request{NamespacedName: types.NamespacedName{Name: name}})
		Expect(err).NotTo(HaveOccurred())
		Expect(shouldRequeue).To(BeFalse())
	}

	getSC := func(name string) *storagev1.StorageClass {
		sc := &storagev1.StorageClass{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: name}, sc)).To(Succeed())
		return sc
	}

	BeforeEach(func() {
		ctx = context.Background()
		scCreates, scDeletes = 0, 0
		countStorageClassRecreate = false

		s := scheme.Scheme
		Expect(srv.AddToScheme(s)).To(Succeed())
		cl = fake.NewClientBuilder().
			WithScheme(s).
			WithStatusSubresource(&srv.ReplicatedStoragePool{}, &srv.ReplicatedStorageClass{}).
			WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					if _, ok := obj.(*storagev1.StorageClass); ok && countStorageClassRecreate {
						scCreates++
					}
					return c.Create(ctx, obj, opts...)
				},
				Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					if _, ok := obj.(*storagev1.StorageClass); ok && countStorageClassRecreate {
						scDeletes++
					}
					return c.Delete(ctx, obj, opts...)
				},
			}).
			Build()

		Expect(cl.Create(ctx, &srv.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: thinPool},
			Spec:       srv.ReplicatedStoragePoolSpec{Type: srv.ReplicatedStoragePoolTypeLVMThin},
		})).To(Succeed())
		Expect(cl.Create(ctx, &srv.ReplicatedStoragePool{
			ObjectMeta: metav1.ObjectMeta{Name: thickPool},
			Spec:       srv.ReplicatedStoragePoolSpec{Type: srv.ReplicatedStoragePoolTypeLVM},
		})).To(Succeed())
	})

	DescribeTable("GetNewStorageClass names the VolumeSnapshotClass only for a thin pool",
		func(poolType srv.ReplicatedStoragePoolType, want bool) {
			sc := controller.GetNewStorageClass(newRSC("rsc", "pool"), false, nil, poolType)
			value, ok := sc.Annotations[controller.RSCStorageClassVolumeSnapshotClassAnnotationKey]
			Expect(ok).To(Equal(want))
			if want {
				Expect(value).To(Equal(controller.RSCStorageClassVolumeSnapshotClassAnnotationValue))
			}
		},
		Entry("LVMThin", srv.ReplicatedStoragePoolTypeLVMThin, true),
		Entry("LVM", srv.ReplicatedStoragePoolTypeLVM, false),
		Entry("unknown pool", srv.ReplicatedStoragePoolType(""), false),
	)

	It("creates the StorageClass of a thin pool with the annotation", func() {
		Expect(cl.Create(ctx, newRSC("thin", thinPool))).To(Succeed())
		reconcileRSC("thin")

		Expect(getSC("thin").Annotations).To(HaveKeyWithValue(
			controller.RSCStorageClassVolumeSnapshotClassAnnotationKey,
			controller.RSCStorageClassVolumeSnapshotClassAnnotationValue))
	})

	It("creates the StorageClass of a thick pool without the annotation", func() {
		Expect(cl.Create(ctx, newRSC("thick", thickPool))).To(Succeed())
		reconcileRSC("thick")

		Expect(getSC("thick").Annotations).NotTo(HaveKey(controller.RSCStorageClassVolumeSnapshotClassAnnotationKey))
	})

	It("creates the StorageClass of a pool that does not exist yet without the annotation", func() {
		Expect(cl.Create(ctx, newRSC("orphan", "missing-pool"))).To(Succeed())
		reconcileRSC("orphan")

		Expect(getSC("orphan").Annotations).NotTo(HaveKey(controller.RSCStorageClassVolumeSnapshotClassAnnotationKey))
	})

	It("removes the annotation from an existing StorageClass of a thick pool in place", func() {
		rsc := newRSC("thick", thickPool)
		Expect(cl.Create(ctx, rsc)).To(Succeed())

		existing := controller.GetNewStorageClass(rsc, false, nil, srv.ReplicatedStoragePoolTypeLVMThin)
		existing.Annotations[controller.DefaultStorageClassAnnotationKey] = "true"
		existing.Labels = map[string]string{"user-label": "kept"}
		Expect(cl.Create(ctx, existing)).To(Succeed())

		countStorageClassRecreate = true
		reconcileRSC("thick")

		sc := getSC("thick")
		Expect(sc.Annotations).NotTo(HaveKey(controller.RSCStorageClassVolumeSnapshotClassAnnotationKey))
		Expect(sc.Annotations).To(HaveKeyWithValue(controller.DefaultStorageClassAnnotationKey, "true"))
		Expect(sc.Labels).To(HaveKeyWithValue("user-label", "kept"))
		Expect(scDeletes).To(BeZero())
		Expect(scCreates).To(BeZero())

		updated := &srv.ReplicatedStorageClass{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: "thick"}, updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(srv.ReplicatedStorageClassPhaseReady))
	})

	It("removes an annotation set by hand on a StorageClass of a thick pool", func() {
		rsc := newRSC("thick", thickPool)
		Expect(cl.Create(ctx, rsc)).To(Succeed())

		existing := controller.GetNewStorageClass(rsc, false, nil, srv.ReplicatedStoragePoolTypeLVM)
		existing.Annotations = map[string]string{controller.RSCStorageClassVolumeSnapshotClassAnnotationKey: "hand-made"}
		Expect(cl.Create(ctx, existing)).To(Succeed())

		countStorageClassRecreate = true
		reconcileRSC("thick")

		Expect(getSC("thick").Annotations).NotTo(HaveKey(controller.RSCStorageClassVolumeSnapshotClassAnnotationKey))
		Expect(scDeletes).To(BeZero())
		Expect(scCreates).To(BeZero())
	})

	It("adds the annotation to an existing StorageClass of a thin pool in place", func() {
		rsc := newRSC("thin", thinPool)
		Expect(cl.Create(ctx, rsc)).To(Succeed())

		existing := controller.GetNewStorageClass(rsc, false, nil, srv.ReplicatedStoragePoolTypeLVM)
		Expect(cl.Create(ctx, existing)).To(Succeed())

		countStorageClassRecreate = true
		reconcileRSC("thin")

		Expect(getSC("thin").Annotations).To(HaveKeyWithValue(
			controller.RSCStorageClassVolumeSnapshotClassAnnotationKey,
			controller.RSCStorageClassVolumeSnapshotClassAnnotationValue))
		Expect(scDeletes).To(BeZero())
		Expect(scCreates).To(BeZero())
	})
})
