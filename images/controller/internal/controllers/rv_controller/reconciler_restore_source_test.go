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

package rvcontroller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

var _ = Describe("reconcileRestoreSourceFinalizer", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	})

	makeSourceRVS := func() *v1alpha1.ReplicatedVolumeSnapshot {
		return &v1alpha1.ReplicatedVolumeSnapshot{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "rvs-source",
				Finalizers: []string{v1alpha1.RVSControllerFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeSnapshotSpec{
				ReplicatedVolumeName: "rv-origin",
			},
			Status: v1alpha1.ReplicatedVolumeSnapshotStatus{
				Phase:      v1alpha1.ReplicatedVolumeSnapshotPhaseReady,
				ReadyToUse: true,
			},
		}
	}

	makeTargetForming := func(name, sourceName string) *v1alpha1.ReplicatedVolume {
		return &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: v1alpha1.ReplicatedVolumeSpec{
				Size:                       resource.MustParse("10Gi"),
				ReplicatedStorageClassName: "rsc-1",
				DataSource: &v1alpha1.VolumeDataSource{
					Kind: v1alpha1.VolumeDataSourceKindReplicatedVolumeSnapshot,
					Name: sourceName,
				},
			},
			// DatameshRevision==0 → formation in progress.
		}
	}

	makeTargetFormed := func(name, sourceName string) *v1alpha1.ReplicatedVolume {
		rv := makeTargetForming(name, sourceName)
		rv.Status.DatameshRevision = 1
		return rv
	}

	getRVS := func(ctx SpecContext, cl client.Client, name string) *v1alpha1.ReplicatedVolumeSnapshot {
		var updated v1alpha1.ReplicatedVolumeSnapshot
		Expect(cl.Get(ctx, client.ObjectKey{Name: name}, &updated)).To(Succeed())
		return &updated
	}

	It("is a no-op when RV has no DataSource", func(ctx SpecContext) {
		target := &v1alpha1.ReplicatedVolume{ObjectMeta: metav1.ObjectMeta{Name: "rv-target"}}
		cl := newClientBuilder(scheme).WithObjects(target).Build()
		rec := NewReconciler(cl, scheme)

		outcome := rec.reconcileRestoreSourceFinalizer(ctx, target)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeFalse())
	})

	// The clone path has its own finalizer; this one must keep its hands off it.
	It("is a no-op when DataSource kind is ReplicatedVolume", func(ctx SpecContext) {
		target := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-target"},
			Spec: v1alpha1.ReplicatedVolumeSpec{
				DataSource: &v1alpha1.VolumeDataSource{
					Kind: v1alpha1.VolumeDataSourceKindReplicatedVolume,
					Name: "rv-source",
				},
			},
		}
		cl := newClientBuilder(scheme).WithObjects(target).Build()
		rec := NewReconciler(cl, scheme)

		outcome := rec.reconcileRestoreSourceFinalizer(ctx, target)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeFalse())
	})

	It("is a no-op when the source RVS does not exist", func(ctx SpecContext) {
		target := makeTargetForming("rv-target", "rvs-missing")
		cl := newClientBuilder(scheme).WithObjects(target).Build()
		rec := NewReconciler(cl, scheme)

		outcome := rec.reconcileRestoreSourceFinalizer(ctx, target)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeFalse())
	})

	It("adds the finalizer to the source RVS while the target is forming", func(ctx SpecContext) {
		source := makeSourceRVS()
		target := makeTargetForming("rv-target", source.Name)
		cl := newClientBuilder(scheme).WithObjects(source, target).Build()
		rec := NewReconciler(cl, scheme)

		Expect(rec.reconcileRestoreSourceFinalizer(ctx, target).Error()).NotTo(HaveOccurred())

		Expect(obju.HasFinalizer(getRVS(ctx, cl, source.Name), v1alpha1.RVSRestoreSourceFinalizer)).To(BeTrue())
	})

	It("is idempotent when the finalizer is already present", func(ctx SpecContext) {
		source := makeSourceRVS()
		source.Finalizers = append(source.Finalizers, v1alpha1.RVSRestoreSourceFinalizer)
		target := makeTargetForming("rv-target", source.Name)
		cl := newClientBuilder(scheme).WithObjects(source, target).Build()
		rec := NewReconciler(cl, scheme)

		Expect(rec.reconcileRestoreSourceFinalizer(ctx, target).Error()).NotTo(HaveOccurred())

		updated := getRVS(ctx, cl, source.Name)
		Expect(obju.HasFinalizer(updated, v1alpha1.RVSRestoreSourceFinalizer)).To(BeTrue())
		count := 0
		for _, f := range updated.Finalizers {
			if f == v1alpha1.RVSRestoreSourceFinalizer {
				count++
			}
		}
		Expect(count).To(Equal(1))
	})

	It("removes the finalizer once the target has finished forming", func(ctx SpecContext) {
		source := makeSourceRVS()
		source.Finalizers = append(source.Finalizers, v1alpha1.RVSRestoreSourceFinalizer)
		target := makeTargetFormed("rv-target", source.Name)
		cl := newClientBuilder(scheme).WithObjects(source, target).Build()
		rec := NewReconciler(cl, scheme)

		Expect(rec.reconcileRestoreSourceFinalizer(ctx, target).Error()).NotTo(HaveOccurred())

		Expect(obju.HasFinalizer(getRVS(ctx, cl, source.Name), v1alpha1.RVSRestoreSourceFinalizer)).To(BeFalse())
	})

	It("removes the finalizer once the target starts deleting", func(ctx SpecContext) {
		source := makeSourceRVS()
		source.Finalizers = append(source.Finalizers, v1alpha1.RVSRestoreSourceFinalizer)
		target := makeTargetForming("rv-target", source.Name)
		now := metav1.Now()
		target.DeletionTimestamp = &now
		target.Finalizers = []string{v1alpha1.RVControllerFinalizer}
		cl := newClientBuilder(scheme).WithObjects(source, target).Build()
		rec := NewReconciler(cl, scheme)

		Expect(rec.reconcileRestoreSourceFinalizer(ctx, target).Error()).NotTo(HaveOccurred())

		Expect(obju.HasFinalizer(getRVS(ctx, cl, source.Name), v1alpha1.RVSRestoreSourceFinalizer)).To(BeFalse())
	})

	// Holding a deleting snapshot open forever helps nobody: the restore either
	// completes with what it has or fails.
	It("does not add the finalizer to an RVS that is already deleting", func(ctx SpecContext) {
		source := makeSourceRVS()
		now := metav1.Now()
		source.DeletionTimestamp = &now
		target := makeTargetForming("rv-target", source.Name)
		cl := newClientBuilder(scheme).WithObjects(source, target).Build()
		rec := NewReconciler(cl, scheme)

		Expect(rec.reconcileRestoreSourceFinalizer(ctx, target).Error()).NotTo(HaveOccurred())

		Expect(obju.HasFinalizer(getRVS(ctx, cl, source.Name), v1alpha1.RVSRestoreSourceFinalizer)).To(BeFalse())
	})
})
