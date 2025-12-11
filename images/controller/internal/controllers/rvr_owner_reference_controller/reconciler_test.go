/*
Copyright 2025 Flant JSC

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

package rvrownerreferencecontroller_test

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	rvrownerreferencecontroller "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_owner_reference_controller"
)

var _ = Describe("Reconciler", func() {
	scheme := runtime.NewScheme()
	Expect(v1alpha3.AddToScheme(scheme)).To(Succeed())

	var (
		clientBuilder *fake.ClientBuilder
	)

	var (
		cl  client.Client
		rec *rvrownerreferencecontroller.Reconciler
	)

	BeforeEach(func() {
		clientBuilder = fake.NewClientBuilder().
			WithScheme(scheme)

		cl = nil
		rec = nil
	})

	JustBeforeEach(func() {
		cl = clientBuilder.Build()
		rec = rvrownerreferencecontroller.NewReconciler(cl, GinkgoLogr, scheme)
	})

	It("returns no error when ReplicatedVolumeReplica does not exist", func(ctx SpecContext) {
		_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: "non-existent"}})
		Expect(err).NotTo(HaveOccurred())
	})

	When("ReplicatedVolumeReplica exists", func() {
		var rvr *v1alpha3.ReplicatedVolumeReplica
		var rv *v1alpha3.ReplicatedVolume

		BeforeEach(func() {
			rv = &v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "rv1",
					UID:  "good-uid",
				},
			}
			rvr = &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr1"},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: rv.Name,
				},
			}
		})

		JustBeforeEach(func(ctx SpecContext) {
			if rv != nil {
				Expect(cl.Create(ctx, rv)).To(Succeed())
			}
			Expect(cl.Create(ctx, rvr)).To(Succeed())
		})

		It("sets ownerReference to the corresponding ReplicatedVolume", func(ctx SpecContext) {
			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(rvr)})
			Expect(err).NotTo(HaveOccurred())

			got := &v1alpha3.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), got)).To(Succeed())

			Expect(got.OwnerReferences).To(ContainElement(SatisfyAll(
				HaveField("Name", Equal(rv.Name)),
				HaveField("Kind", Equal("ReplicatedVolume")),
				HaveField("APIVersion", Equal("storage.deckhouse.io/v1alpha3")),
				HaveField("Controller", Not(BeNil())),
				HaveField("BlockOwnerDeletion", Not(BeNil())),
			)))
		})

		When("ReplicatedVolumeReplica has DeletionTimestamp", func() {
			BeforeEach(func() {
				rvr.Finalizers = []string{"test-finalizer"}
			})

			JustBeforeEach(func(ctx SpecContext) {
				Expect(cl.Delete(ctx, rvr)).To(Succeed())
				got := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), got)).To(Succeed())
				Expect(got.DeletionTimestamp).NotTo(BeNil())
				Expect(got.Finalizers).To(ContainElement("test-finalizer"))
				Expect(got.OwnerReferences).To(BeEmpty())
			})

			It("skips reconciliation", func(ctx SpecContext) {
				_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(rvr)})
				Expect(err).NotTo(HaveOccurred())

				got := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), got)).To(Succeed())
				Expect(got.DeletionTimestamp).NotTo(BeNil())
				Expect(got.Finalizers).To(ContainElement("test-finalizer"))
				Expect(got.OwnerReferences).To(BeEmpty())
			})
		})

		When("has empty ReplicatedVolumeName", func() {
			BeforeEach(func() {
				rvr.Spec.ReplicatedVolumeName = ""
			})

			It("does nothing and returns no error", func(ctx SpecContext) {
				_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(rvr)})
				Expect(err).NotTo(HaveOccurred())

				got := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), got)).To(Succeed())
				Expect(got.OwnerReferences).To(BeEmpty())
			})
		})

		When("ReplicatedVolume does not exist", func() {
			BeforeEach(func() {
				rv = nil
			})

			It("ignores missing ReplicatedVolume", func(ctx SpecContext) {
				_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(rvr)})
				Expect(err).NotTo(HaveOccurred())

				got := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), got)).To(Succeed())
				Expect(got.OwnerReferences).To(BeEmpty())
			})
		})

		When("Get for ReplicatedVolume fails", func() {
			BeforeEach(func() {
				clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						if _, ok := obj.(*v1alpha3.ReplicatedVolume); ok {
							return errors.NewInternalError(fmt.Errorf("test error"))
						}
						return c.Get(ctx, key, obj, opts...)
					},
				})
			})

			It("returns error from client", func(ctx SpecContext) {
				_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(rvr)})
				Expect(err).To(HaveOccurred())
			})
		})

		When("Patch for ReplicatedVolumeReplica fails", func() {
			BeforeEach(func() {
				clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
					Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
						return errors.NewInternalError(fmt.Errorf("test error"))
					},
				})
			})

			It("returns error from client", func(ctx SpecContext) {
				_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(rvr)})
				Expect(err).To(HaveOccurred())
			})
		})

		When("ReplicatedVolumeReplica has another ownerReference", func() {
			BeforeEach(func() {
				rvr.OwnerReferences = []metav1.OwnerReference{
					{
						Name: "other-owner",
					},
				}
			})

			It("sets another ownerReference to the corresponding ReplicatedVolume and keeps the original ownerReference", func(ctx SpecContext) {
				_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(rvr)})
				Expect(err).NotTo(HaveOccurred())

				got := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), got)).To(Succeed())
				Expect(got.OwnerReferences).To(HaveLen(2))
				Expect(got.OwnerReferences).To(ContainElement(SatisfyAll(
					HaveField("Name", Equal(rv.Name)),
					HaveField("Kind", Equal("ReplicatedVolume")),
					HaveField("APIVersion", Equal("storage.deckhouse.io/v1alpha3")),
					HaveField("Controller", Not(BeNil())),
					HaveField("BlockOwnerDeletion", Not(BeNil())),
				)))
				Expect(got.OwnerReferences).To(ContainElement(HaveField("Name", Equal("other-owner"))))
			})
		})

		When("ReplicatedVolumeReplica already has ownerReference to the correct ReplicatedVolume", func() {
			BeforeEach(func() {
				rvr.OwnerReferences = []metav1.OwnerReference{
					{
						Name:               "rv1",
						Kind:               "ReplicatedVolume",
						APIVersion:         "storage.deckhouse.io/v1alpha3",
						Controller:         ptr.To(true),
						BlockOwnerDeletion: ptr.To(true),
						UID:                "good-uid",
					},
				}

				clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
					Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
						return errors.NewInternalError(fmt.Errorf("test error"))
					},
				})
			})

			It("do nothing and returns no error", func(ctx SpecContext) {
				_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(rvr)})
				Expect(err).NotTo(HaveOccurred())

				got := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), got)).To(Succeed())
				Expect(got.OwnerReferences).To(HaveLen(1))
				Expect(got.OwnerReferences).To(ContainElement(HaveField("Name", Equal("rv1"))))
			})
		})

		When("ReplicatedVolumeReplica already has ownerReference to the ReplicatedVolume with different UID", func() {
			BeforeEach(func() {
				rvr.OwnerReferences = []metav1.OwnerReference{
					{
						Name:               "rv1",
						Kind:               "ReplicatedVolume",
						APIVersion:         "storage.deckhouse.io/v1alpha3",
						Controller:         ptr.To(true),
						BlockOwnerDeletion: ptr.To(true),
						UID:                "bad-uid",
					},
				}
			})

			It("sets ownerReference to the corresponding ReplicatedVolume", func(ctx SpecContext) {
				_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(rvr)})
				Expect(err).NotTo(HaveOccurred())

				got := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), got)).To(Succeed())
				Expect(got.OwnerReferences).To(HaveLen(1))
				Expect(got.OwnerReferences).To(ContainElement(SatisfyAll(
					HaveField("Name", Equal(rv.Name)),
					HaveField("Kind", Equal("ReplicatedVolume")),
					HaveField("APIVersion", Equal("storage.deckhouse.io/v1alpha3")),
					HaveField("Controller", Not(BeNil())),
					HaveField("BlockOwnerDeletion", Not(BeNil())),
					HaveField("UID", Equal(types.UID("good-uid"))),
				)))
			})
		})
	})
})
