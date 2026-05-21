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
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

var _ = Describe("reconcileCloneSourceFinalizer", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	})

	makeSource := func(name string) *v1alpha1.ReplicatedVolume {
		return &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:       name,
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeSpec{
				Size:                       resource.MustParse("10Gi"),
				ReplicatedStorageClassName: "rsc-1",
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
					Kind: v1alpha1.VolumeDataSourceKindReplicatedVolume,
					Name: sourceName,
				},
			},
			// DatameshRevision==0 → formation in progress.
		}
	}

	makeTargetFormed := func(name, sourceName string) *v1alpha1.ReplicatedVolume {
		rv := makeTargetForming(name, sourceName)
		rv.Status.DatameshRevision = 1 // formation completed, no Formation transition in flight.
		return rv
	}

	It("is a no-op when RV has no DataSource", func(ctx SpecContext) {
		target := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-target"},
		}
		cl := newClientBuilder(scheme).WithObjects(target).Build()
		rec := NewReconciler(cl, scheme)

		outcome := rec.reconcileCloneSourceFinalizer(ctx, target)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeFalse())
	})

	It("is a no-op when DataSource kind is ReplicatedVolumeSnapshot", func(ctx SpecContext) {
		target := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-target"},
			Spec: v1alpha1.ReplicatedVolumeSpec{
				DataSource: &v1alpha1.VolumeDataSource{
					Kind: v1alpha1.VolumeDataSourceKindReplicatedVolumeSnapshot,
					Name: "rvs-1",
				},
			},
		}
		cl := newClientBuilder(scheme).WithObjects(target).Build()
		rec := NewReconciler(cl, scheme)

		outcome := rec.reconcileCloneSourceFinalizer(ctx, target)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeFalse())
	})

	It("is a no-op when source RV does not exist", func(ctx SpecContext) {
		target := makeTargetForming("rv-target", "rv-source-missing")
		cl := newClientBuilder(scheme).WithObjects(target).Build()
		rec := NewReconciler(cl, scheme)

		outcome := rec.reconcileCloneSourceFinalizer(ctx, target)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeFalse())
	})

	It("adds RVCloneSourceFinalizer to source RV while target is forming", func(ctx SpecContext) {
		source := makeSource("rv-source")
		target := makeTargetForming("rv-target", "rv-source")

		cl := newClientBuilder(scheme).WithObjects(source, target).Build()
		rec := NewReconciler(cl, scheme)

		outcome := rec.reconcileCloneSourceFinalizer(ctx, target)
		Expect(outcome.Error()).NotTo(HaveOccurred())

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(source), &updated)).To(Succeed())
		Expect(obju.HasFinalizer(&updated, v1alpha1.RVCloneSourceFinalizer)).To(BeTrue())
	})

	It("is idempotent when finalizer is already present", func(ctx SpecContext) {
		source := makeSource("rv-source")
		source.Finalizers = append(source.Finalizers, v1alpha1.RVCloneSourceFinalizer)
		target := makeTargetForming("rv-target", "rv-source")

		cl := newClientBuilder(scheme).WithObjects(source, target).Build()
		rec := NewReconciler(cl, scheme)

		outcome := rec.reconcileCloneSourceFinalizer(ctx, target)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeFalse())
	})

	It("does not add finalizer when source RV is already deleting", func(ctx SpecContext) {
		source := makeSource("rv-source")
		source.DeletionTimestamp = ptr.To(metav1.Now())
		target := makeTargetForming("rv-target", "rv-source")

		cl := newClientBuilder(scheme).WithObjects(source, target).Build()
		rec := NewReconciler(cl, scheme)

		outcome := rec.reconcileCloneSourceFinalizer(ctx, target)
		Expect(outcome.Error()).NotTo(HaveOccurred())

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(source), &updated)).To(Succeed())
		Expect(obju.HasFinalizer(&updated, v1alpha1.RVCloneSourceFinalizer)).To(BeFalse())
	})

	It("removes finalizer from source RV once target RV has finished formation", func(ctx SpecContext) {
		source := makeSource("rv-source")
		source.Finalizers = append(source.Finalizers, v1alpha1.RVCloneSourceFinalizer)
		target := makeTargetFormed("rv-target", "rv-source")

		cl := newClientBuilder(scheme).WithObjects(source, target).Build()
		rec := NewReconciler(cl, scheme)

		outcome := rec.reconcileCloneSourceFinalizer(ctx, target)
		Expect(outcome.Error()).NotTo(HaveOccurred())

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(source), &updated)).To(Succeed())
		Expect(obju.HasFinalizer(&updated, v1alpha1.RVCloneSourceFinalizer)).To(BeFalse())
	})

	It("removes finalizer from source RV once target RV enters deletion", func(ctx SpecContext) {
		source := makeSource("rv-source")
		source.Finalizers = append(source.Finalizers, v1alpha1.RVCloneSourceFinalizer)
		target := makeTargetForming("rv-target", "rv-source")
		target.Finalizers = []string{v1alpha1.RVControllerFinalizer}
		target.DeletionTimestamp = ptr.To(metav1.Now())

		cl := newClientBuilder(scheme).WithObjects(source, target).Build()
		rec := NewReconciler(cl, scheme)

		outcome := rec.reconcileCloneSourceFinalizer(ctx, target)
		Expect(outcome.Error()).NotTo(HaveOccurred())

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(source), &updated)).To(Succeed())
		Expect(obju.HasFinalizer(&updated, v1alpha1.RVCloneSourceFinalizer)).To(BeFalse())
	})
})
