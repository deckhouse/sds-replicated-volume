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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// The StorageClass is named after the RSC, so the name may already be held by a
// StorageClass of another provisioner. These specs pin that such a StorageClass
// is left alone and the conflict is reported on the RSC.
var _ = Describe("reconcileStorageClass", func() {
	const foreignProvisioner = "e2e.example.com/foreign"

	var (
		ctx context.Context
		cl  client.Client
		rec *Reconciler
		rsc *v1alpha1.ReplicatedStorageClass
	)

	newClient := func(objs ...client.Object) client.Client {
		scheme := runtime.NewScheme()
		Expect(storagev1.AddToScheme(scheme)).To(Succeed())
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		return fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(objs...).
			WithStatusSubresource(&v1alpha1.ReplicatedStorageClass{}).
			Build()
	}

	BeforeEach(func() {
		ctx = context.Background()
		rsc = &v1alpha1.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{Name: "rsc-1", Generation: 1},
			Spec: v1alpha1.ReplicatedStorageClassSpec{
				ReclaimPolicy: "Delete",
				VolumeAccess:  v1alpha1.VolumeAccessLocal,
			},
		}
	})

	It("creates the StorageClass when the name is free", func() {
		cl = newClient(rsc)
		rec = NewReconciler(cl)

		outcome := rec.reconcileStorageClass(ctx, rsc)
		Expect(outcome.ShouldReturn()).To(BeFalse())

		var sc storagev1.StorageClass
		Expect(cl.Get(ctx, client.ObjectKey{Name: rsc.Name}, &sc)).To(Succeed())
		Expect(sc.Provisioner).To(Equal(v1alpha1.CSIProvisioner))
	})

	It("recreates its own StorageClass whose spec is out of sync", func() {
		own := &storagev1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:       rsc.Name,
				Finalizers: []string{v1alpha1.StorageClassFinalizer},
			},
			Provisioner: v1alpha1.CSIProvisioner,
			Parameters:  map[string]string{"stale": "true"},
		}
		cl = newClient(rsc, own)
		rec = NewReconciler(cl)

		outcome := rec.reconcileStorageClass(ctx, rsc)
		Expect(outcome.ShouldReturn()).To(BeFalse())

		var sc storagev1.StorageClass
		Expect(cl.Get(ctx, client.ObjectKey{Name: rsc.Name}, &sc)).To(Succeed())
		Expect(sc.Parameters).NotTo(HaveKey("stale"))
		Expect(sc.Parameters).To(HaveKeyWithValue(v1alpha1.CSIParamRSCNameKey, rsc.Name))
	})

	It("passes the volume cleanup method to the CSI driver as a parameter", func() {
		rsc.Spec.VolumeCleanup = v1alpha1.VolumeCleanupRandomFillSinglePass
		cl = newClient(rsc)
		rec = NewReconciler(cl)

		Expect(rec.reconcileStorageClass(ctx, rsc).ShouldReturn()).To(BeFalse())

		var sc storagev1.StorageClass
		Expect(cl.Get(ctx, client.ObjectKey{Name: rsc.Name}, &sc)).To(Succeed())
		Expect(sc.Parameters).To(HaveKeyWithValue(v1alpha1.CSIParamVolumeCleanupKey, "RandomFillSinglePass"))
	})

	It("leaves the volume cleanup parameter out when the class has no method", func() {
		cl = newClient(rsc)
		rec = NewReconciler(cl)

		Expect(rec.reconcileStorageClass(ctx, rsc).ShouldReturn()).To(BeFalse())

		var sc storagev1.StorageClass
		Expect(cl.Get(ctx, client.ObjectKey{Name: rsc.Name}, &sc)).To(Succeed())
		Expect(sc.Parameters).NotTo(HaveKey(v1alpha1.CSIParamVolumeCleanupKey))
	})

	It("recreates the StorageClass when the volume cleanup method changes", func() {
		rsc.Spec.VolumeCleanup = v1alpha1.VolumeCleanupRandomFillSinglePass
		cl = newClient(rsc)
		rec = NewReconciler(cl)
		Expect(rec.reconcileStorageClass(ctx, rsc).ShouldReturn()).To(BeFalse())

		rsc.Spec.VolumeCleanup = v1alpha1.VolumeCleanupDiscard
		Expect(rec.reconcileStorageClass(ctx, rsc).ShouldReturn()).To(BeFalse())

		var sc storagev1.StorageClass
		Expect(cl.Get(ctx, client.ObjectKey{Name: rsc.Name}, &sc)).To(Succeed())
		Expect(sc.Parameters).To(HaveKeyWithValue(v1alpha1.CSIParamVolumeCleanupKey, "Discard"))
	})

	It("leaves a StorageClass of another provisioner alone and reports the conflict", func() {
		foreign := &storagev1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:       rsc.Name,
				Finalizers: []string{"example.com/keep"},
			},
			Provisioner: foreignProvisioner,
			Parameters:  map[string]string{"owner": "someone-else"},
		}
		cl = newClient(rsc, foreign)
		rec = NewReconciler(cl)

		outcome := rec.reconcileStorageClass(ctx, rsc)
		Expect(outcome.ShouldReturn()).To(BeTrue())
		result, err := outcome.ToCtrl()
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(storageClassConflictRequeueInterval))

		var sc storagev1.StorageClass
		Expect(cl.Get(ctx, client.ObjectKey{Name: rsc.Name}, &sc)).To(Succeed())
		Expect(sc.DeletionTimestamp).To(BeNil())
		Expect(sc.Provisioner).To(Equal(foreignProvisioner))
		Expect(sc.Parameters).To(Equal(foreign.Parameters))
		Expect(sc.Finalizers).To(Equal(foreign.Finalizers))
		Expect(sc.Labels).NotTo(HaveKey(v1alpha1.ManagedByLabelKey))

		var updated v1alpha1.ReplicatedStorageClass
		Expect(cl.Get(ctx, client.ObjectKey{Name: rsc.Name}, &updated)).To(Succeed())
		cond := obju.GetStatusCondition(&updated, v1alpha1.ReplicatedStorageClassCondReadyType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedStorageClassCondReadyReasonStorageClassConflict))
		Expect(cond.Message).To(ContainSubstring(foreignProvisioner))
		Expect(updated.Status.Phase).To(Equal(v1alpha1.ReplicatedStorageClassPhaseInvalidConfiguration))
		Expect(updated.Status.Message).To(Equal(cond.Message))
	})

	It("does not patch the status again while the conflict stays the same", func() {
		foreign := &storagev1.StorageClass{
			ObjectMeta:  metav1.ObjectMeta{Name: rsc.Name},
			Provisioner: foreignProvisioner,
		}
		cl = newClient(rsc, foreign)
		rec = NewReconciler(cl)

		Expect(rec.reconcileStorageClass(ctx, rsc).ShouldReturn()).To(BeTrue())
		var first v1alpha1.ReplicatedStorageClass
		Expect(cl.Get(ctx, client.ObjectKey{Name: rsc.Name}, &first)).To(Succeed())

		Expect(rec.reconcileStorageClass(ctx, first.DeepCopy()).ShouldReturn()).To(BeTrue())
		var second v1alpha1.ReplicatedStorageClass
		Expect(cl.Get(ctx, client.ObjectKey{Name: rsc.Name}, &second)).To(Succeed())
		Expect(second.ResourceVersion).To(Equal(first.ResourceVersion))
	})
})
