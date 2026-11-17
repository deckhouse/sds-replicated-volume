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

package rsccontroller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// The storage-foundation VolumeSnapshot webhook takes the class of a snapshot
// from the StorageClass annotation, so every StorageClass of an RSC with thin
// volumes must carry it, and a StorageClass of thick volumes, which cannot be
// snapshotted, must not. The annotation is metadata, not spec, so it is set or
// removed without recreating the StorageClass.
var _ = Describe("reconcileStorageClass volume snapshot class annotation", func() {
	var (
		ctx     context.Context
		rsc     *v1alpha1.ReplicatedStorageClass
		deletes int
		creates int
	)

	newClient := func(objs ...client.Object) client.Client {
		scheme := runtime.NewScheme()
		Expect(storagev1.AddToScheme(scheme)).To(Succeed())
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		return fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(objs...).
			WithStatusSubresource(&v1alpha1.ReplicatedStorageClass{}).
			WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					if _, ok := obj.(*storagev1.StorageClass); ok {
						creates++
					}
					return c.Create(ctx, obj, opts...)
				},
				Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					if _, ok := obj.(*storagev1.StorageClass); ok {
						deletes++
					}
					return c.Delete(ctx, obj, opts...)
				},
			}).
			Build()
	}

	// existingSC returns the StorageClass the controller would create for rsc,
	// with the given annotations in place of the computed ones.
	existingSC := func(annotations map[string]string) *storagev1.StorageClass {
		sc, err := computeTargetStorageClass(rsc, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		sc.UID = types.UID("sc-uid")
		sc.Annotations = annotations
		return sc
	}

	getSC := func(cl client.Client) *storagev1.StorageClass {
		var sc storagev1.StorageClass
		Expect(cl.Get(ctx, client.ObjectKey{Name: rsc.Name}, &sc)).To(Succeed())
		return &sc
	}

	BeforeEach(func() {
		ctx = context.Background()
		deletes, creates = 0, 0
		rsc = &v1alpha1.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{Name: "rsc-1", Generation: 1},
			Spec: v1alpha1.ReplicatedStorageClassSpec{
				ReclaimPolicy: "Delete",
				VolumeAccess:  v1alpha1.VolumeAccessLocal,
				Storage: &v1alpha1.ReplicatedStorageClassStorage{
					Type:            v1alpha1.ReplicatedStoragePoolTypeLVMThin,
					LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{{Name: "lvg-1", ThinPoolName: "thin"}},
				},
			},
		}
	})
	It("names the module's VolumeSnapshotClass on a new StorageClass", func() {
		cl := newClient(rsc)
		rec := NewReconciler(cl)

		Expect(rec.reconcileStorageClass(ctx, rsc).ShouldReturn()).To(BeFalse())

		sc := getSC(cl)
		Expect(sc.Annotations).To(HaveKeyWithValue(
			v1alpha1.StorageClassVolumeSnapshotClassAnnotationKey, v1alpha1.VolumeSnapshotClassName))
		Expect(creates).To(Equal(1))
	})

	It("adds the annotation to an existing StorageClass in place", func() {
		cl := newClient(rsc, existingSC(map[string]string{"example.com/note": "kept"}))
		rec := NewReconciler(cl)

		Expect(rec.reconcileStorageClass(ctx, rsc).ShouldReturn()).To(BeFalse())

		sc := getSC(cl)
		Expect(sc.Annotations).To(Equal(map[string]string{
			"example.com/note": "kept",
			v1alpha1.StorageClassVolumeSnapshotClassAnnotationKey: v1alpha1.VolumeSnapshotClassName,
		}))
		Expect(sc.UID).To(Equal(types.UID("sc-uid")))
		Expect(sc.DeletionTimestamp).To(BeNil())
		Expect(deletes).To(BeZero())
		Expect(creates).To(BeZero())
	})

	It("replaces a value set by hand in place", func() {
		cl := newClient(rsc, existingSC(map[string]string{
			v1alpha1.StorageClassVolumeSnapshotClassAnnotationKey: "hand-made",
		}))
		rec := NewReconciler(cl)

		Expect(rec.reconcileStorageClass(ctx, rsc).ShouldReturn()).To(BeFalse())

		sc := getSC(cl)
		Expect(sc.Annotations).To(HaveKeyWithValue(
			v1alpha1.StorageClassVolumeSnapshotClassAnnotationKey, v1alpha1.VolumeSnapshotClassName))
		Expect(sc.UID).To(Equal(types.UID("sc-uid")))
		Expect(deletes).To(BeZero())
		Expect(creates).To(BeZero())
	})

	It("does not touch a StorageClass that already has the annotation", func() {
		cl := newClient(rsc, existingSC(map[string]string{
			v1alpha1.StorageClassVolumeSnapshotClassAnnotationKey: v1alpha1.VolumeSnapshotClassName,
		}))
		rec := NewReconciler(cl)
		before := getSC(cl)

		Expect(rec.reconcileStorageClass(ctx, rsc).ShouldReturn()).To(BeFalse())

		Expect(getSC(cl).ResourceVersion).To(Equal(before.ResourceVersion))
		Expect(deletes).To(BeZero())
		Expect(creates).To(BeZero())
	})

	It("does not name the VolumeSnapshotClass on a new StorageClass of thick volumes", func() {
		rsc.Spec.Storage.Type = v1alpha1.ReplicatedStoragePoolTypeLVM
		rsc.Spec.Storage.LVMVolumeGroups[0].ThinPoolName = ""
		cl := newClient(rsc)
		rec := NewReconciler(cl)

		Expect(rec.reconcileStorageClass(ctx, rsc).ShouldReturn()).To(BeFalse())

		Expect(getSC(cl).Annotations).NotTo(HaveKey(v1alpha1.StorageClassVolumeSnapshotClassAnnotationKey))
		Expect(creates).To(Equal(1))
	})

	It("removes the annotation from an existing StorageClass of thick volumes in place", func() {
		rsc.Spec.Storage.Type = v1alpha1.ReplicatedStoragePoolTypeLVM
		rsc.Spec.Storage.LVMVolumeGroups[0].ThinPoolName = ""
		cl := newClient(rsc, existingSC(map[string]string{
			"example.com/note": "kept",
			v1alpha1.StorageClassVolumeSnapshotClassAnnotationKey: v1alpha1.VolumeSnapshotClassName,
		}))
		rec := NewReconciler(cl)

		Expect(rec.reconcileStorageClass(ctx, rsc).ShouldReturn()).To(BeFalse())

		sc := getSC(cl)
		Expect(sc.Annotations).To(Equal(map[string]string{"example.com/note": "kept"}))
		Expect(sc.UID).To(Equal(types.UID("sc-uid")))
		Expect(sc.DeletionTimestamp).To(BeNil())
		Expect(deletes).To(BeZero())
		Expect(creates).To(BeZero())
	})
})
