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
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/datamesh"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/idset"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes/testhelpers"
)

func TestRvControllerReconciler(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "rv_controller Reconciler Suite")
}

var _ = BeforeSuite(func() {
	datamesh.BuildRegistry()
})

func RequestFor(object client.Object) reconcile.Request {
	return reconcile.Request{NamespacedName: client.ObjectKeyFromObject(object)}
}

func Requeue() OmegaMatcher {
	return Not(Equal(reconcile.Result{}))
}

// newClientBuilder creates a fake.ClientBuilder with required indexes.
func newClientBuilder(scheme *runtime.Scheme) *fake.ClientBuilder {
	b := fake.NewClientBuilder().WithScheme(scheme)
	b = testhelpers.WithRVAByReplicatedVolumeNameIndex(b)
	b = testhelpers.WithRVRByReplicatedVolumeNameIndex(b)
	return b
}

// newTestRSP creates a minimal ReplicatedStoragePool for tests.
func newTestRSP(name string) *v1alpha1.ReplicatedStoragePool {
	return &v1alpha1.ReplicatedStoragePool{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.ReplicatedStoragePoolSpec{
			Type:               v1alpha1.ReplicatedStoragePoolTypeLVM,
			SystemNetworkNames: []string{"Internal"},
			LVMVolumeGroups:    []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{{Name: "lvg-1"}},
		},
	}
}

// newRSCWithConfiguration creates a RSC with valid configuration for tests.
func newRSCWithConfiguration(name string) *v1alpha1.ReplicatedStorageClass {
	return &v1alpha1.ReplicatedStorageClass{
		ObjectMeta: metav1.ObjectMeta{Name: name, Generation: 1},
		Status: v1alpha1.ReplicatedStorageClassStatus{
			ConfigurationGeneration: 1,
			Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
				Topology:           v1alpha1.TopologyIgnored,
				FailuresToTolerate: 0, GuaranteedMinimumDataRedundancy: 0,
				VolumeAccess:              v1alpha1.VolumeAccessLocal,
				ReplicatedStoragePoolName: "test-pool",
			},
		},
	}
}

var _ = Describe("Reconciler", func() {
	var (
		scheme *runtime.Scheme
	)

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	})

	Describe("Reconcile", func() {
		It("returns no error when ReplicatedVolume does not exist", func(ctx SpecContext) {
			cl := newClientBuilder(scheme).Build()
			rec := NewReconciler(cl, scheme)

			result, err := rec.Reconcile(ctx, RequestFor(&v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "non-existent"},
			}))
			Expect(err).NotTo(HaveOccurred())
			Expect(result).ToNot(Requeue())
		})

		It("adds finalizer and label to new RV", func(ctx SpecContext) {
			rsc := newRSCWithConfiguration("rsc-1")
			rsp := newTestRSP("test-pool")

			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-1",
				},
			}

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsc, rsp).
				WithStatusSubresource(rv, rsc).
				Build()
			rec := NewReconciler(cl, scheme)

			_, err := rec.Reconcile(ctx, RequestFor(rv))
			Expect(err).NotTo(HaveOccurred())

			var updated v1alpha1.ReplicatedVolume
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
			Expect(obju.HasFinalizer(&updated, v1alpha1.RVControllerFinalizer)).To(BeTrue())
			Expect(obju.HasLabelValue(&updated, v1alpha1.ReplicatedStorageClassLabelKey, "rsc-1")).To(BeTrue())
		})

		It("is idempotent when finalizer and label already set", func(ctx SpecContext) {
			rsc := newRSCWithConfiguration("rsc-1")
			rsp := newTestRSP("test-pool")

			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-1",
					Finalizers: []string{v1alpha1.RVControllerFinalizer},
					Labels: map[string]string{
						v1alpha1.ReplicatedStorageClassLabelKey: "rsc-1",
					},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-1",
				},
			}

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsc, rsp).
				WithStatusSubresource(rv, rsc).
				Build()
			rec := NewReconciler(cl, scheme)

			// Reconcile multiple times — the RV enters formation (DatameshRevision=0),
			// so a RequeueAfter for the formation timeout is expected.
			for i := 0; i < 3; i++ {
				_, err := rec.Reconcile(ctx, RequestFor(rv))
				Expect(err).NotTo(HaveOccurred())
			}

			var updated v1alpha1.ReplicatedVolume
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
			Expect(obju.HasFinalizer(&updated, v1alpha1.RVControllerFinalizer)).To(BeTrue())
			Expect(obju.HasLabelValue(&updated, v1alpha1.ReplicatedStorageClassLabelKey, "rsc-1")).To(BeTrue())
		})

		It("removes finalizer when RV is being deleted and has no children", func(ctx SpecContext) {
			now := metav1.Now()
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "rv-1",
					DeletionTimestamp: &now,
					Finalizers:        []string{v1alpha1.RVControllerFinalizer},
					Labels: map[string]string{
						v1alpha1.ReplicatedStorageClassLabelKey: "rsc-1",
					},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-1",
				},
			}

			cl := newClientBuilder(scheme).
				WithObjects(rv).
				WithStatusSubresource(rv).
				Build()
			rec := NewReconciler(cl, scheme)

			result, err := rec.Reconcile(ctx, RequestFor(rv))
			Expect(err).NotTo(HaveOccurred())
			Expect(result).ToNot(Requeue())

			// When finalizer is removed from an object with DeletionTimestamp,
			// the fake client automatically deletes the object.
			var updated v1alpha1.ReplicatedVolume
			err = cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected NotFound after finalizer removal")
		})

		It("removes finalizer when RV is being deleted with RVAs but no RVRs", func(ctx SpecContext) {
			rsc := newRSCWithConfiguration("rsc-1")

			now := metav1.Now()
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "rv-1",
					DeletionTimestamp: &now,
					Finalizers:        []string{v1alpha1.RVControllerFinalizer},
					Labels: map[string]string{
						v1alpha1.ReplicatedStorageClassLabelKey: "rsc-1",
					},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-1",
				},
			}

			rva := &v1alpha1.ReplicatedVolumeAttachment{
				ObjectMeta: metav1.ObjectMeta{Name: "rva-1"},
				Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
					ReplicatedVolumeName: "rv-1",
					NodeName:             "node-1",
				},
			}

			cl := newClientBuilder(scheme).
				WithObjects(rv, rva, rsc).
				WithStatusSubresource(rv, rva, rsc).
				Build()
			rec := NewReconciler(cl, scheme)

			result, err := rec.Reconcile(ctx, RequestFor(rv))
			Expect(err).NotTo(HaveOccurred())
			Expect(result).ToNot(Requeue())

			// RV finalizer should be removed — RVAs do not block RV deletion.
			var updated v1alpha1.ReplicatedVolume
			err = cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "RV should be finalized (no RVRs)")

			// RVA should have deletion conditions set.
			var updatedRVA v1alpha1.ReplicatedVolumeAttachment
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(rva), &updatedRVA)).To(Succeed())
			cond := obju.GetStatusCondition(&updatedRVA, v1alpha1.ReplicatedVolumeAttachmentCondAttachedType)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonWaitingForReplicatedVolume))
		})

		It("keeps finalizer when RV is being deleted but has RVRs", func(ctx SpecContext) {
			rsc := newRSCWithConfiguration("rsc-1")

			now := metav1.Now()
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "rv-1",
					DeletionTimestamp: &now,
					Finalizers:        []string{v1alpha1.RVControllerFinalizer},
					Labels: map[string]string{
						v1alpha1.ReplicatedStorageClassLabelKey: "rsc-1",
					},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-1",
				},
			}

			rvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-1",
					Type:                 v1alpha1.ReplicaTypeDiskful,
					NodeName:             "node-1",
					LVMVolumeGroupName:   "lvg-1",
				},
			}

			cl := newClientBuilder(scheme).
				WithObjects(rv, rvr, rsc).
				WithStatusSubresource(rv, rvr, rsc).
				Build()
			rec := NewReconciler(cl, scheme)

			result, err := rec.Reconcile(ctx, RequestFor(rv))
			Expect(err).NotTo(HaveOccurred())
			Expect(result).ToNot(Requeue())

			var updated v1alpha1.ReplicatedVolume
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
			Expect(obju.HasFinalizer(&updated, v1alpha1.RVControllerFinalizer)).To(BeTrue())
		})

		It("keeps finalizer when RV is being deleted but has both RVAs and RVRs", func(ctx SpecContext) {
			rsc := newRSCWithConfiguration("rsc-1")

			now := metav1.Now()
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "rv-1",
					DeletionTimestamp: &now,
					Finalizers:        []string{v1alpha1.RVControllerFinalizer},
					Labels: map[string]string{
						v1alpha1.ReplicatedStorageClassLabelKey: "rsc-1",
					},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-1",
				},
			}

			rva := &v1alpha1.ReplicatedVolumeAttachment{
				ObjectMeta: metav1.ObjectMeta{Name: "rva-1"},
				Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
					ReplicatedVolumeName: "rv-1",
					NodeName:             "node-1",
				},
			}

			rvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-1",
					Type:                 v1alpha1.ReplicaTypeDiskful,
					NodeName:             "node-1",
					LVMVolumeGroupName:   "lvg-1",
				},
			}

			cl := newClientBuilder(scheme).
				WithObjects(rv, rva, rvr, rsc).
				WithStatusSubresource(rv, rva, rvr, rsc).
				Build()
			rec := NewReconciler(cl, scheme)

			result, err := rec.Reconcile(ctx, RequestFor(rv))
			Expect(err).NotTo(HaveOccurred())
			Expect(result).ToNot(Requeue())

			var updated v1alpha1.ReplicatedVolume
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
			Expect(obju.HasFinalizer(&updated, v1alpha1.RVControllerFinalizer)).To(BeTrue())
		})
	})

	Describe("Error handling", func() {
		It("returns error when Get fails", func(ctx SpecContext) {
			testError := errors.New("get failed")
			cl := newClientBuilder(scheme).
				WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						if _, ok := obj.(*v1alpha1.ReplicatedVolume); ok {
							return testError
						}
						return cl.Get(ctx, key, obj, opts...)
					},
				}).
				Build()
			rec := NewReconciler(cl, scheme)

			_, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKey{Name: "rv-1"}})
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, testError)).To(BeTrue())
		})

		It("returns error when listing RVAs fails", func(ctx SpecContext) {
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-1",
				},
			}

			testError := errors.New("list RVAs failed")
			cl := newClientBuilder(scheme).
				WithObjects(rv).
				WithStatusSubresource(rv).
				WithInterceptorFuncs(interceptor.Funcs{
					List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
						if _, ok := list.(*v1alpha1.ReplicatedVolumeAttachmentList); ok {
							return testError
						}
						return cl.List(ctx, list, opts...)
					},
				}).
				Build()
			rec := NewReconciler(cl, scheme)

			_, err := rec.Reconcile(ctx, RequestFor(rv))
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, testError)).To(BeTrue())
		})

		It("returns error when listing RVRs fails", func(ctx SpecContext) {
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-1",
				},
			}

			testError := errors.New("list RVRs failed")
			cl := newClientBuilder(scheme).
				WithObjects(rv).
				WithStatusSubresource(rv).
				WithInterceptorFuncs(interceptor.Funcs{
					List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
						if _, ok := list.(*v1alpha1.ReplicatedVolumeReplicaList); ok {
							return testError
						}
						return cl.List(ctx, list, opts...)
					},
				}).
				Build()
			rec := NewReconciler(cl, scheme)

			_, err := rec.Reconcile(ctx, RequestFor(rv))
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, testError)).To(BeTrue())
		})

		It("returns error when Patch fails", func(ctx SpecContext) {
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-1",
				},
			}

			testError := errors.New("patch failed")
			cl := newClientBuilder(scheme).
				WithObjects(rv).
				WithStatusSubresource(rv).
				WithInterceptorFuncs(interceptor.Funcs{
					Patch: func(ctx context.Context, cl client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
						if _, ok := obj.(*v1alpha1.ReplicatedVolume); ok {
							return testError
						}
						return cl.Patch(ctx, obj, patch, opts...)
					},
				}).
				Build()
			rec := NewReconciler(cl, scheme)

			_, err := rec.Reconcile(ctx, RequestFor(rv))
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, testError)).To(BeTrue())
		})
	})

	Describe("Label updates", func() {
		It("updates label when storage class name changes", func(ctx SpecContext) {
			rsc := newRSCWithConfiguration("new-rsc")
			rsp := newTestRSP("test-pool")

			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-1",
					Finalizers: []string{v1alpha1.RVControllerFinalizer},
					Labels: map[string]string{
						v1alpha1.ReplicatedStorageClassLabelKey: "old-rsc",
					},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "new-rsc",
				},
			}

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsc, rsp).
				WithStatusSubresource(rv, rsc).
				Build()
			rec := NewReconciler(cl, scheme)

			_, err := rec.Reconcile(ctx, RequestFor(rv))
			Expect(err).NotTo(HaveOccurred())

			var updated v1alpha1.ReplicatedVolume
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
			Expect(obju.HasLabelValue(&updated, v1alpha1.ReplicatedStorageClassLabelKey, "new-rsc")).To(BeTrue())
		})
	})

	Describe("Configuration initialization", func() {
		It("initializes configuration from RSC and sets ConfigurationReady to Ready", func(ctx SpecContext) {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1", Generation: 5},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					ConfigurationGeneration: 5,
					Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
						Topology:           v1alpha1.TopologyTransZonal,
						FailuresToTolerate: 1, GuaranteedMinimumDataRedundancy: 1,
						VolumeAccess:              v1alpha1.VolumeAccessPreferablyLocal,
						ReplicatedStoragePoolName: "pool-1",
					},
				},
			}
			rsp := newTestRSP("pool-1")
			rsp.Spec.Zones = []string{"zone-a", "zone-b", "zone-c"} // 3 zones for FTT=1,GMDR=1.

			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-1",
					Finalizers: []string{v1alpha1.RVControllerFinalizer},
					Labels: map[string]string{
						v1alpha1.ReplicatedStorageClassLabelKey: "rsc-1",
					},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-1",
				},
			}

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsc, rsp).
				WithStatusSubresource(rv, rsc).
				Build()
			rec := NewReconciler(cl, scheme)

			_, err := rec.Reconcile(ctx, RequestFor(rv))
			Expect(err).NotTo(HaveOccurred())

			var updated v1alpha1.ReplicatedVolume
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
			Expect(updated.Status.Configuration).NotTo(BeNil())
			Expect(updated.Status.Configuration.Topology).To(Equal(v1alpha1.TopologyTransZonal))
			Expect(updated.Status.Configuration.FailuresToTolerate).To(Equal(byte(1)))
			Expect(updated.Status.Configuration.GuaranteedMinimumDataRedundancy).To(Equal(byte(1)))
			Expect(updated.Status.Configuration.VolumeAccess).To(Equal(v1alpha1.VolumeAccessPreferablyLocal))
			Expect(updated.Status.Configuration.ReplicatedStoragePoolName).To(Equal("pool-1"))
			Expect(updated.Status.ConfigurationGeneration).To(Equal(int64(5)))

			// Check ConfigurationReady condition.
			cond := obju.GetStatusCondition(&updated, v1alpha1.ReplicatedVolumeCondConfigurationReadyType)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonReady))
		})

		It("sets WaitingForStorageClass condition when RSC has no configuration", func(ctx SpecContext) {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
				Status:     v1alpha1.ReplicatedStorageClassStatus{},
			}

			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-1",
					Finalizers: []string{v1alpha1.RVControllerFinalizer},
					Labels: map[string]string{
						v1alpha1.ReplicatedStorageClassLabelKey: "rsc-1",
					},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-1",
				},
			}

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsc).
				WithStatusSubresource(rv, rsc).
				Build()
			rec := NewReconciler(cl, scheme)

			_, err := rec.Reconcile(ctx, RequestFor(rv))
			Expect(err).NotTo(HaveOccurred())

			var updated v1alpha1.ReplicatedVolume
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())

			// Check ConfigurationReady condition.
			cond := obju.GetStatusCondition(&updated, v1alpha1.ReplicatedVolumeCondConfigurationReadyType)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonWaitingForStorageClass))
			Expect(cond.Message).To(ContainSubstring("configuration not ready"))
		})

		It("sets WaitingForStorageClass condition when RSC does not exist", func(ctx SpecContext) {
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-1",
					Finalizers: []string{v1alpha1.RVControllerFinalizer},
					Labels: map[string]string{
						v1alpha1.ReplicatedStorageClassLabelKey: "rsc-1",
					},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-1",
				},
			}

			cl := newClientBuilder(scheme).
				WithObjects(rv).
				WithStatusSubresource(rv).
				Build()
			rec := NewReconciler(cl, scheme)

			_, err := rec.Reconcile(ctx, RequestFor(rv))
			Expect(err).NotTo(HaveOccurred())

			var updated v1alpha1.ReplicatedVolume
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())

			// Check ConfigurationReady condition.
			cond := obju.GetStatusCondition(&updated, v1alpha1.ReplicatedVolumeCondConfigurationReadyType)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonWaitingForStorageClass))
			Expect(cond.Message).To(ContainSubstring("not found"))
		})

		It("updates configuration when RSC generation changes (normal operation)", func(ctx SpecContext) {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1", Generation: 10},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					ConfigurationGeneration: 10,
					Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
						Topology:           v1alpha1.TopologyZonal,
						FailuresToTolerate: 1, GuaranteedMinimumDataRedundancy: 0,
						VolumeAccess:              v1alpha1.VolumeAccessAny,
						ReplicatedStoragePoolName: "new-pool",
					},
				},
			}
			rspOld := newTestRSP("old-pool")
			rspNew := newTestRSP("new-pool")

			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-1",
					Finalizers: []string{v1alpha1.RVControllerFinalizer},
					Labels: map[string]string{
						v1alpha1.ReplicatedStorageClassLabelKey: "rsc-1",
					},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-1",
				},
				Status: v1alpha1.ReplicatedVolumeStatus{
					DatameshRevision:        1, // Normal operation.
					ConfigurationGeneration: 5,
					Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
						Topology:           v1alpha1.TopologyTransZonal,
						FailuresToTolerate: 1, GuaranteedMinimumDataRedundancy: 1,
						VolumeAccess:              v1alpha1.VolumeAccessPreferablyLocal,
						ReplicatedStoragePoolName: "old-pool",
					},
				},
			}

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsc, rspOld, rspNew).
				WithStatusSubresource(rv, rsc).
				Build()
			rec := NewReconciler(cl, scheme)

			_, err := rec.Reconcile(ctx, RequestFor(rv))
			Expect(err).NotTo(HaveOccurred())

			var updated v1alpha1.ReplicatedVolume
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
			// Configuration should be updated from RSC.
			Expect(updated.Status.Configuration).NotTo(BeNil())
			Expect(updated.Status.Configuration.Topology).To(Equal(v1alpha1.TopologyZonal))
			Expect(updated.Status.Configuration.ReplicatedStoragePoolName).To(Equal("new-pool"))
			Expect(updated.Status.ConfigurationGeneration).To(Equal(int64(10)))

			// ConfigurationReady should be Ready.
			cond := obju.GetStatusCondition(&updated, v1alpha1.ReplicatedVolumeCondConfigurationReadyType)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonReady))
		})

		It("sets Ready condition when generation matches RSC (normal operation)", func(ctx SpecContext) {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1", Generation: 5},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					ConfigurationGeneration: 5,
					Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
						Topology:           v1alpha1.TopologyTransZonal,
						FailuresToTolerate: 1, GuaranteedMinimumDataRedundancy: 1,
						VolumeAccess:              v1alpha1.VolumeAccessPreferablyLocal,
						ReplicatedStoragePoolName: "pool-1",
					},
				},
			}
			rsp := newTestRSP("pool-1")

			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-1",
					Finalizers: []string{v1alpha1.RVControllerFinalizer},
					Labels: map[string]string{
						v1alpha1.ReplicatedStorageClassLabelKey: "rsc-1",
					},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-1",
				},
				Status: v1alpha1.ReplicatedVolumeStatus{
					DatameshRevision:        1, // Normal operation.
					ConfigurationGeneration: 5,
					Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
						Topology:           v1alpha1.TopologyTransZonal,
						FailuresToTolerate: 1, GuaranteedMinimumDataRedundancy: 1,
						VolumeAccess:              v1alpha1.VolumeAccessPreferablyLocal,
						ReplicatedStoragePoolName: "pool-1",
					},
				},
			}

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsc, rsp).
				WithStatusSubresource(rv, rsc).
				Build()
			rec := NewReconciler(cl, scheme)

			_, err := rec.Reconcile(ctx, RequestFor(rv))
			Expect(err).NotTo(HaveOccurred())

			var updated v1alpha1.ReplicatedVolume
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())

			// Check ConfigurationReady condition - should be Ready.
			cond := obju.GetStatusCondition(&updated, v1alpha1.ReplicatedVolumeCondConfigurationReadyType)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonReady))
		})

		It("returns error when getRSC fails", func(ctx SpecContext) {
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-1",
				},
			}

			testError := errors.New("get RSC failed")
			cl := newClientBuilder(scheme).
				WithObjects(rv).
				WithStatusSubresource(rv).
				WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						if _, ok := obj.(*v1alpha1.ReplicatedStorageClass); ok {
							return testError
						}
						return cl.Get(ctx, key, obj, opts...)
					},
				}).
				Build()
			rec := NewReconciler(cl, scheme)

			_, err := rec.Reconcile(ctx, RequestFor(rv))
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, testError)).To(BeTrue())
		})

	})

	Describe("Configuration initialization (Manual mode)", func() {
		It("initializes configuration from ManualConfiguration and sets ConfigurationReady to Ready", func(ctx SpecContext) {
			rsp := newTestRSP("manual-pool")

			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-1",
					Generation: 1,
					Finalizers: []string{v1alpha1.RVControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:              resource.MustParse("10Gi"),
					ConfigurationMode: v1alpha1.ReplicatedVolumeConfigurationModeManual,
					ManualConfiguration: &v1alpha1.ReplicatedVolumeConfiguration{
						Topology:                        v1alpha1.TopologyIgnored,
						FailuresToTolerate:              1,
						GuaranteedMinimumDataRedundancy: 1,
						VolumeAccess:                    v1alpha1.VolumeAccessPreferablyLocal,
						ReplicatedStoragePoolName:       "manual-pool",
					},
				},
			}

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp).
				WithStatusSubresource(rv).
				Build()
			rec := NewReconciler(cl, scheme)

			_, err := rec.Reconcile(ctx, RequestFor(rv))
			Expect(err).NotTo(HaveOccurred())

			var updated v1alpha1.ReplicatedVolume
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
			Expect(updated.Status.Configuration).NotTo(BeNil())
			Expect(updated.Status.Configuration.Topology).To(Equal(v1alpha1.TopologyIgnored))
			Expect(updated.Status.Configuration.FailuresToTolerate).To(Equal(byte(1)))
			Expect(updated.Status.Configuration.GuaranteedMinimumDataRedundancy).To(Equal(byte(1)))
			Expect(updated.Status.Configuration.ReplicatedStoragePoolName).To(Equal("manual-pool"))
			Expect(updated.Status.ConfigurationGeneration).To(Equal(int64(0))) // Manual mode: no RSC generation tracking.

			// ConfigurationReady should be True.
			cond := obju.GetStatusCondition(&updated, v1alpha1.ReplicatedVolumeCondConfigurationReadyType)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonReady))

			// RSC label should NOT be set (Manual mode).
			Expect(obju.HasLabel(&updated, v1alpha1.ReplicatedStorageClassLabelKey)).To(BeFalse())
		})

		It("sets InvalidConfiguration when TransZonal zone count is wrong", func(ctx SpecContext) {
			rsp := &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{Name: "manual-pool"},
				Spec: v1alpha1.ReplicatedStoragePoolSpec{
					Type:               v1alpha1.ReplicatedStoragePoolTypeLVM,
					SystemNetworkNames: []string{"Internal"},
					LVMVolumeGroups:    []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{{Name: "lvg-1"}},
					Zones:              []string{"zone-a", "zone-b"}, // 2 zones.
				},
			}

			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-1",
					Generation: 1,
					Finalizers: []string{v1alpha1.RVControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:              resource.MustParse("10Gi"),
					ConfigurationMode: v1alpha1.ReplicatedVolumeConfigurationModeManual,
					ManualConfiguration: &v1alpha1.ReplicatedVolumeConfiguration{
						Topology:                        v1alpha1.TopologyTransZonal,
						FailuresToTolerate:              1,
						GuaranteedMinimumDataRedundancy: 1,
						VolumeAccess:                    v1alpha1.VolumeAccessPreferablyLocal,
						ReplicatedStoragePoolName:       "manual-pool",
					},
				},
			}

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp).
				WithStatusSubresource(rv).
				Build()
			rec := NewReconciler(cl, scheme)

			_, err := rec.Reconcile(ctx, RequestFor(rv))
			Expect(err).NotTo(HaveOccurred())

			var updated v1alpha1.ReplicatedVolume
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())

			// Configuration should NOT be set (zone validation failed).
			Expect(updated.Status.Configuration).To(BeNil())

			// ConfigurationReady should report InvalidConfiguration.
			cond := obju.GetStatusCondition(&updated, v1alpha1.ReplicatedVolumeCondConfigurationReadyType)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonInvalidConfiguration))
		})

		It("removes RSC label when switching from Auto to Manual mode", func(ctx SpecContext) {
			rsp := newTestRSP("manual-pool")
			rspOld := newTestRSP("auto-pool")

			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-1",
					Generation: 2,
					Finalizers: []string{v1alpha1.RVControllerFinalizer},
					Labels:     map[string]string{v1alpha1.ReplicatedStorageClassLabelKey: "old-rsc"},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:              resource.MustParse("10Gi"),
					ConfigurationMode: v1alpha1.ReplicatedVolumeConfigurationModeManual,
					ManualConfiguration: &v1alpha1.ReplicatedVolumeConfiguration{
						Topology:                        v1alpha1.TopologyIgnored,
						FailuresToTolerate:              0,
						GuaranteedMinimumDataRedundancy: 0,
						VolumeAccess:                    v1alpha1.VolumeAccessLocal,
						ReplicatedStoragePoolName:       "manual-pool",
					},
				},
				Status: v1alpha1.ReplicatedVolumeStatus{
					DatameshRevision:        1,
					ConfigurationGeneration: 1, // Old RSC generation.
					Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
						Topology:                        v1alpha1.TopologyIgnored,
						FailuresToTolerate:              0,
						GuaranteedMinimumDataRedundancy: 0,
						VolumeAccess:                    v1alpha1.VolumeAccessLocal,
						ReplicatedStoragePoolName:       "auto-pool",
					},
				},
			}

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rspOld).
				WithStatusSubresource(rv).
				Build()
			rec := NewReconciler(cl, scheme)

			_, err := rec.Reconcile(ctx, RequestFor(rv))
			Expect(err).NotTo(HaveOccurred())

			var updated v1alpha1.ReplicatedVolume
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())

			// RSC label should be removed (Manual mode).
			Expect(obju.HasLabel(&updated, v1alpha1.ReplicatedStorageClassLabelKey)).To(BeFalse())

			// Configuration should be updated from ManualConfiguration.
			Expect(updated.Status.Configuration.ReplicatedStoragePoolName).To(Equal("manual-pool"))
			Expect(updated.Status.ConfigurationGeneration).To(Equal(int64(0))) // Manual mode: no RSC generation tracking.
		})
	})

	Describe("Deletion", func() {
		It("does not delete RV if there are attached members", func(ctx SpecContext) {
			now := metav1.Now()
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "rv-1",
					DeletionTimestamp: &now,
					Finalizers:        []string{v1alpha1.RVControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-1",
				},
				Status: v1alpha1.ReplicatedVolumeStatus{
					DatameshRevision: 1,
					Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
						Members: []v1alpha1.DatameshMember{
							{Name: "rvr-1", Attached: true}, // Attached member.
						},
					},
				},
			}

			// Need actual RVR object to keep the finalizer in reconcileMetadata.
			rvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rvr-1",
					Finalizers: []string{v1alpha1.RVControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-1",
					Type:                 v1alpha1.ReplicaTypeDiskful,
					NodeName:             "node-1",
					LVMVolumeGroupName:   "lvg-1",
				},
			}

			rsc := newRSCWithConfiguration("rsc-1")
			rsp := newTestRSP("test-pool")

			cl := newClientBuilder(scheme).
				WithObjects(rv, rvr, rsc, rsp).
				WithStatusSubresource(rv, rvr, rsc).
				Build()
			rec := NewReconciler(cl, scheme)

			_, err := rec.Reconcile(ctx, RequestFor(rv))
			Expect(err).NotTo(HaveOccurred())

			// RV should still exist with finalizer (not deleted due to attached member).
			var updated v1alpha1.ReplicatedVolume
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
			Expect(obju.HasFinalizer(&updated, v1alpha1.RVControllerFinalizer)).To(BeTrue())

			// RVR should NOT be deleted (still has finalizer).
			var updatedRVR v1alpha1.ReplicatedVolumeReplica
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), &updatedRVR)).To(Succeed())
			Expect(obju.HasFinalizer(&updatedRVR, v1alpha1.RVControllerFinalizer)).To(BeTrue())
		})

		It("deletes RVRs and clears members when RV is being deleted with no attached members", func(ctx SpecContext) {
			now := metav1.Now()
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "rv-1",
					DeletionTimestamp: &now,
					Finalizers:        []string{v1alpha1.RVControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-1",
				},
				Status: v1alpha1.ReplicatedVolumeStatus{
					Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
						Members: []v1alpha1.DatameshMember{
							{Name: "rvr-1", Attached: false},
							{Name: "rvr-2", Attached: false},
						},
					},
				},
			}

			rvr1 := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rvr-1",
					Finalizers: []string{v1alpha1.RVControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-1",
					Type:                 v1alpha1.ReplicaTypeDiskful,
					NodeName:             "node-1",
					LVMVolumeGroupName:   "lvg-1",
				},
			}

			rvr2 := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rvr-2",
					Finalizers: []string{v1alpha1.RVControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-1",
					Type:                 v1alpha1.ReplicaTypeDiskful,
					NodeName:             "node-2",
					LVMVolumeGroupName:   "lvg-2",
				},
			}

			cl := newClientBuilder(scheme).
				WithObjects(rv, rvr1, rvr2).
				WithStatusSubresource(rv, rvr1, rvr2).
				Build()
			rec := NewReconciler(cl, scheme)

			_, err := rec.Reconcile(ctx, RequestFor(rv))
			Expect(err).NotTo(HaveOccurred())

			// RVRs should have finalizers removed and be deleted.
			var updatedRVR1 v1alpha1.ReplicatedVolumeReplica
			err = cl.Get(ctx, client.ObjectKeyFromObject(rvr1), &updatedRVR1)
			Expect(apierrors.IsNotFound(err) || updatedRVR1.DeletionTimestamp != nil).To(BeTrue(),
				"RVR should be deleted or have DeletionTimestamp")

			var updatedRVR2 v1alpha1.ReplicatedVolumeReplica
			err = cl.Get(ctx, client.ObjectKeyFromObject(rvr2), &updatedRVR2)
			Expect(apierrors.IsNotFound(err) || updatedRVR2.DeletionTimestamp != nil).To(BeTrue(),
				"RVR should be deleted or have DeletionTimestamp")

			// RV datamesh members should be cleared.
			var updated v1alpha1.ReplicatedVolume
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
			Expect(updated.Status.Datamesh.Members).To(BeEmpty())
		})

		It("updates RVA conditions when RV is being deleted", func(ctx SpecContext) {
			now := metav1.Now()
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "rv-1",
					DeletionTimestamp: &now,
					Finalizers:        []string{v1alpha1.RVControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-1",
				},
			}

			rva := &v1alpha1.ReplicatedVolumeAttachment{
				ObjectMeta: metav1.ObjectMeta{Name: "rva-1"},
				Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
					ReplicatedVolumeName: "rv-1",
					NodeName:             "node-1",
				},
				Status: v1alpha1.ReplicatedVolumeAttachmentStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ReplicatedVolumeAttachmentCondAttachedType,
							Status: metav1.ConditionTrue,
							Reason: v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttached,
						},
						{
							Type:   v1alpha1.ReplicatedVolumeAttachmentCondReadyType,
							Status: metav1.ConditionTrue,
							Reason: v1alpha1.ReplicatedVolumeAttachmentCondReadyReasonReady,
						},
						{
							Type:   v1alpha1.ReplicatedVolumeAttachmentCondReplicaReadyType,
							Status: metav1.ConditionTrue,
							Reason: "Ready",
						},
					},
				},
			}

			cl := newClientBuilder(scheme).
				WithObjects(rv, rva).
				WithStatusSubresource(rv, rva).
				Build()
			rec := NewReconciler(cl, scheme)

			_, err := rec.Reconcile(ctx, RequestFor(rv))
			Expect(err).NotTo(HaveOccurred())

			// RVA should have updated conditions.
			var updated v1alpha1.ReplicatedVolumeAttachment
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(rva), &updated)).To(Succeed())

			// Should have exactly 2 conditions.
			Expect(updated.Status.Conditions).To(HaveLen(2))

			// Attached condition should be False with WaitingForReplicatedVolume.
			attachedCond := obju.GetStatusCondition(&updated, v1alpha1.ReplicatedVolumeAttachmentCondAttachedType)
			Expect(attachedCond).NotTo(BeNil())
			Expect(attachedCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(attachedCond.Reason).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonWaitingForReplicatedVolume))

			// Ready condition should be False with NotAttached.
			readyCond := obju.GetStatusCondition(&updated, v1alpha1.ReplicatedVolumeAttachmentCondReadyType)
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(readyCond.Reason).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondReadyReasonNotAttached))

			// Phase should be Pending (RVA is not deleting, RV is deleting → waiting conditions).
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ReplicatedVolumeAttachmentPhasePending))
			Expect(updated.Status.Message).To(ContainSubstring("deleted"))
		})

		It("does not process deletion if RV has other finalizers", func(ctx SpecContext) {
			now := metav1.Now()
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "rv-1",
					DeletionTimestamp: &now,
					Finalizers: []string{
						v1alpha1.RVControllerFinalizer,
						"other-controller/finalizer", // Another finalizer.
					},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-1",
				},
				Status: v1alpha1.ReplicatedVolumeStatus{
					Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
						Members: []v1alpha1.DatameshMember{
							{Name: "rvr-1", Attached: false},
						},
					},
				},
			}

			rvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rvr-1",
					Finalizers: []string{v1alpha1.RVControllerFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-1",
					Type:                 v1alpha1.ReplicaTypeDiskful,
					NodeName:             "node-1",
					LVMVolumeGroupName:   "lvg-1",
				},
			}

			rsc := newRSCWithConfiguration("rsc-1")
			rsp := newTestRSP("test-pool")

			cl := newClientBuilder(scheme).
				WithObjects(rv, rvr, rsc, rsp).
				WithStatusSubresource(rv, rvr, rsc).
				Build()
			rec := NewReconciler(cl, scheme)

			_, err := rec.Reconcile(ctx, RequestFor(rv))
			Expect(err).NotTo(HaveOccurred())

			// RVR should NOT be deleted (still has finalizer).
			var updatedRVR v1alpha1.ReplicatedVolumeReplica
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), &updatedRVR)).To(Succeed())
			Expect(obju.HasFinalizer(&updatedRVR, v1alpha1.RVControllerFinalizer)).To(BeTrue())

			// Datamesh members should NOT be cleared.
			var updated v1alpha1.ReplicatedVolume
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
			Expect(updated.Status.Datamesh.Members).NotTo(BeEmpty())
		})
	})

	Describe("Formation", func() {
		It("returns error for invalid formation step", func(ctx SpecContext) {
			rsc := newRSCWithConfiguration("rsc-1")
			rsp := newTestRSP("test-pool")

			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-1",
					Finalizers: []string{v1alpha1.RVControllerFinalizer},
					Labels: map[string]string{
						v1alpha1.ReplicatedStorageClassLabelKey: "rsc-1",
					},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-1",
				},
				Status: v1alpha1.ReplicatedVolumeStatus{
					ConfigurationGeneration:         1,
					ConfigurationObservedGeneration: 1,
					Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
						Topology: v1alpha1.TopologyIgnored, FailuresToTolerate: 0, GuaranteedMinimumDataRedundancy: 0,
						VolumeAccess: v1alpha1.VolumeAccessLocal, ReplicatedStoragePoolName: "test-pool",
					},
					DatameshRevision: 1,
					DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
						// All 3 steps completed — isFormationInProgress returns stepIdx=0
						// which maps to Preconfigure. Formation will proceed normally.
						// To test invalid step, we'd need a step index >= formationStepCount,
						// which can't happen through isFormationInProgress.
						// Instead, add a 4th step as Active to trigger stepIdx=3 (invalid).
						{
							Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation,
							Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
								{Name: "Preconfigure", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted, StartedAt: ptr.To(metav1.Now()), CompletedAt: ptr.To(metav1.Now())},
								{Name: "Establish connectivity", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted, StartedAt: ptr.To(metav1.Now()), CompletedAt: ptr.To(metav1.Now())},
								{Name: "Bootstrap data", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted, StartedAt: ptr.To(metav1.Now()), CompletedAt: ptr.To(metav1.Now())},
								{Name: "InvalidStep", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, StartedAt: ptr.To(metav1.Now())},
							},
						},
					},
				},
			}

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsc, rsp).
				WithStatusSubresource(rv, rsc).
				Build()
			rec := NewReconciler(cl, scheme)

			_, err := rec.Reconcile(ctx, RequestFor(rv))
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid formation step"))
		})

		It("waits for deleting RVRs before creating new ones", func(ctx SpecContext) {
			rsc := newRSCWithConfiguration("rsc-1")
			rsp := newTestRSP("test-pool")

			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-1",
					Finalizers: []string{v1alpha1.RVControllerFinalizer},
					Labels: map[string]string{
						v1alpha1.ReplicatedStorageClassLabelKey: "rsc-1",
					},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-1",
				},
				// DatameshRevision defaults to 0 → formation in progress.
			}

			now := metav1.Now()
			deletingRVR := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name:              v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0),
					DeletionTimestamp: &now,
					Finalizers:        []string{v1alpha1.RVRControllerFinalizer}, // Blocked by rvr-controller.
				},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-1",
					Type:                 v1alpha1.ReplicaTypeDiskful,
					NodeName:             "node-1",
					LVMVolumeGroupName:   "lvg-1",
				},
			}

			// Track whether Create was called for an RVR.
			rvrCreateCalled := false
			cl := newClientBuilder(scheme).
				WithObjects(rv, rsc, rsp, deletingRVR).
				WithStatusSubresource(rv, rsc, deletingRVR).
				WithInterceptorFuncs(interceptor.Funcs{
					Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
						if _, ok := obj.(*v1alpha1.ReplicatedVolumeReplica); ok {
							rvrCreateCalled = true
						}
						return cl.Create(ctx, obj, opts...)
					},
				}).
				Build()
			rec := NewReconciler(cl, scheme)

			result, err := rec.Reconcile(ctx, RequestFor(rv))
			Expect(err).NotTo(HaveOccurred())

			// No new RVRs should be created while deleting ones exist.
			Expect(rvrCreateCalled).To(BeFalse(), "should not create new RVRs while deleting ones exist")

			// reconcileFormationRestartIfTimeoutPassed schedules a requeue for when
			// the formation timeout expires (formation just started → RequeueAfter ≈ 30s).
			Expect(result.RequeueAfter).To(BeNumerically(">", 0), "should requeue to check formation timeout")

			// Verify formation transition message indicates waiting.
			var updated v1alpha1.ReplicatedVolume
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
			Expect(updated.Status.DatameshTransitions).To(HaveLen(1))
			Expect(updated.Status.DatameshTransitions[0].Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation))
			Expect(updated.Status.DatameshTransitions[0].CurrentStep().Name).To(Equal(formationStepNames[formationStepIdxPreconfigure]))
			Expect(updated.Status.DatameshTransitions[0].CurrentStep().Message).To(ContainSubstring("Waiting for"))
			Expect(updated.Status.DatameshTransitions[0].CurrentStep().Message).To(ContainSubstring("deleting replicas"))
		})

		It("blocks creation when misplaced replicas exist", func(ctx SpecContext) {
			rsc := newRSCWithConfiguration("rsc-1")
			rsp := newTestRSP("test-pool")

			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv-1",
					Finalizers: []string{v1alpha1.RVControllerFinalizer},
					Labels: map[string]string{
						v1alpha1.ReplicatedStorageClassLabelKey: "rsc-1",
					},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-1",
				},
			}

			// A misplaced replica: not deleting, but SatisfyEligibleNodes is False.
			misplacedRVR := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0),
					Finalizers: []string{
						v1alpha1.RVControllerFinalizer,  // Will be removed by deleteRVRWithForcedFinalizerRemoval.
						v1alpha1.RVRControllerFinalizer, // Keeps the object around after Delete (blocks real removal).
					},
				},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "rv-1",
					Type:                 v1alpha1.ReplicaTypeDiskful,
					NodeName:             "node-1",
					LVMVolumeGroupName:   "lvg-1",
				},
			}
			// Mark replica as misplaced (SatisfyEligibleNodes=False, generation-current).
			obju.SetStatusCondition(misplacedRVR, metav1.Condition{
				Type:   v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType,
				Status: metav1.ConditionFalse,
				Reason: v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonNodeMismatch,
			})

			// Track whether Create was called for an RVR.
			rvrCreateCalled := false
			cl := newClientBuilder(scheme).
				WithObjects(rv, rsc, rsp, misplacedRVR).
				WithStatusSubresource(rv, rsc, misplacedRVR).
				WithInterceptorFuncs(interceptor.Funcs{
					Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
						if _, ok := obj.(*v1alpha1.ReplicatedVolumeReplica); ok {
							rvrCreateCalled = true
						}
						return cl.Create(ctx, obj, opts...)
					},
				}).
				Build()
			rec := NewReconciler(cl, scheme)

			result, err := rec.Reconcile(ctx, RequestFor(rv))
			Expect(err).NotTo(HaveOccurred())

			// No new RVRs should be created while misplaced ones are being cleaned up.
			Expect(rvrCreateCalled).To(BeFalse(), "should not create new RVRs while misplaced ones exist")

			// Misplaced replica was deleted and is now "deleting" → formation timeout requeue.
			Expect(result.RequeueAfter).To(BeNumerically(">", 0), "should requeue to check formation timeout")

			// Verify formation transition message indicates waiting for cleanup.
			var updated v1alpha1.ReplicatedVolume
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
			Expect(updated.Status.DatameshTransitions).To(HaveLen(1))
			Expect(updated.Status.DatameshTransitions[0].Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation))
			Expect(updated.Status.DatameshTransitions[0].CurrentStep().Name).To(Equal(formationStepNames[formationStepIdxPreconfigure]))
			Expect(updated.Status.DatameshTransitions[0].CurrentStep().Message).To(ContainSubstring("Waiting for"))
			Expect(updated.Status.DatameshTransitions[0].CurrentStep().Message).To(ContainSubstring("deleting replicas"))
		})
	})
})

var _ = Describe("ensureDatameshReplicaRequests", func() {
	mkRVR := func(name string, req *v1alpha1.DatameshMembershipRequest) *v1alpha1.ReplicatedVolumeReplica {
		return &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshRequest: req,
			},
		}
	}

	mkRequest := func(role v1alpha1.ReplicaType) *v1alpha1.DatameshMembershipRequest {
		return &v1alpha1.DatameshMembershipRequest{
			Operation: v1alpha1.DatameshMembershipRequestOperationJoin,
			Type:      role,
		}
	}

	mkReplicaRequest := func(name string, req v1alpha1.DatameshMembershipRequest, firstObserved time.Time) v1alpha1.ReplicatedVolumeDatameshReplicaRequest {
		return v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
			Name:            name,
			Request:         req,
			FirstObservedAt: metav1.NewTime(firstObserved),
		}
	}

	It("adds new entry when RVR has datamesh request", func(ctx SpecContext) {
		rv := &v1alpha1.ReplicatedVolume{}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rvr-1", mkRequest(v1alpha1.ReplicaTypeDiskful)),
		}

		outcome := ensureDatameshReplicaRequests(ctx, rv, rvrs)

		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rv.Status.DatameshReplicaRequests).To(HaveLen(1))
		Expect(rv.Status.DatameshReplicaRequests[0].Name).To(Equal("rvr-1"))
		Expect(rv.Status.DatameshReplicaRequests[0].Request.Operation).To(Equal(v1alpha1.DatameshMembershipRequestOperationJoin))
		Expect(rv.Status.DatameshReplicaRequests[0].Request.Type).To(Equal(v1alpha1.ReplicaTypeDiskful))
	})

	It("removes entry when RVR no longer has datamesh request", func(ctx SpecContext) {
		oldTime := time.Now().Add(-1 * time.Hour)
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshReplicaRequests: []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
					mkReplicaRequest("rvr-1", v1alpha1.DatameshMembershipRequest{
						Operation: v1alpha1.DatameshMembershipRequestOperationJoin,
						Type:      v1alpha1.ReplicaTypeDiskful,
					}, oldTime),
				},
			},
		}
		// RVR with nil transition (no pending anymore).
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rvr-1", nil),
		}

		outcome := ensureDatameshReplicaRequests(ctx, rv, rvrs)

		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rv.Status.DatameshReplicaRequests).To(BeEmpty())
	})

	It("updates entry when transition changed", func(ctx SpecContext) {
		oldTime := time.Now().Add(-1 * time.Hour)
		oldPending := v1alpha1.DatameshMembershipRequest{
			Operation: v1alpha1.DatameshMembershipRequestOperationJoin,
			Type:      v1alpha1.ReplicaTypeDiskful,
		}
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshReplicaRequests: []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
					{
						Name:            "rvr-1",
						Message:         "old message should be cleared",
						Request:         oldPending,
						FirstObservedAt: metav1.NewTime(oldTime),
					},
				},
			},
		}
		// New transition with different role.
		newPending := mkRequest(v1alpha1.ReplicaTypeAccess)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rvr-1", newPending),
		}

		outcome := ensureDatameshReplicaRequests(ctx, rv, rvrs)

		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rv.Status.DatameshReplicaRequests).To(HaveLen(1))
		Expect(rv.Status.DatameshReplicaRequests[0].Name).To(Equal("rvr-1"))
		Expect(rv.Status.DatameshReplicaRequests[0].Request.Type).To(Equal(v1alpha1.ReplicaTypeAccess))
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(BeEmpty())                      // Message cleared.
		Expect(rv.Status.DatameshReplicaRequests[0].FirstObservedAt.Time).NotTo(Equal(oldTime)) // Timestamp updated.
	})

	It("sorts unsorted existing entries but does not mark changed (sort-only is not a patch reason)", func(ctx SpecContext) {
		oldTime := time.Now().Add(-1 * time.Hour)
		req := v1alpha1.DatameshMembershipRequest{
			Operation: v1alpha1.DatameshMembershipRequestOperationJoin,
			Type:      v1alpha1.ReplicaTypeDiskful,
		}
		// Entries are not sorted by name.
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshReplicaRequests: []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
					mkReplicaRequest("rvr-2", req, oldTime),
					mkReplicaRequest("rvr-1", req, oldTime),
				},
			},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rvr-1", mkRequest(v1alpha1.ReplicaTypeDiskful)),
			mkRVR("rvr-2", mkRequest(v1alpha1.ReplicaTypeDiskful)),
		}

		outcome := ensureDatameshReplicaRequests(ctx, rv, rvrs)

		// Sort-only does not mark changed (order is semantically irrelevant for the API).
		Expect(outcome.DidChange()).To(BeFalse())
		Expect(rv.Status.DatameshReplicaRequests).To(HaveLen(2))
	})

	It("no change when already in sync (idempotent)", func(ctx SpecContext) {
		req := v1alpha1.DatameshMembershipRequest{
			Operation: v1alpha1.DatameshMembershipRequestOperationJoin,
			Type:      v1alpha1.ReplicaTypeDiskful,
		}
		oldTime := time.Now().Add(-1 * time.Hour)
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshReplicaRequests: []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
					{
						Name:            "rvr-1",
						Request:         req,
						FirstObservedAt: metav1.NewTime(oldTime),
					},
				},
			},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rvr-1", mkRequest(v1alpha1.ReplicaTypeDiskful)),
		}

		outcome := ensureDatameshReplicaRequests(ctx, rv, rvrs)

		Expect(outcome.DidChange()).To(BeFalse())
		Expect(rv.Status.DatameshReplicaRequests).To(HaveLen(1))
		// FirstObservedAt should be preserved.
		Expect(rv.Status.DatameshReplicaRequests[0].FirstObservedAt.Time).To(Equal(oldTime))
	})

	It("handles multiple RVRs with mixed add/remove/update", func(ctx SpecContext) {
		oldTime := time.Now().Add(-1 * time.Hour)
		// Existing entries: rvr-1 (will update), rvr-2 (will remove), rvr-4 (will keep).
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshReplicaRequests: []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
					mkReplicaRequest("rvr-1", v1alpha1.DatameshMembershipRequest{
						Operation: v1alpha1.DatameshMembershipRequestOperationJoin,
						Type:      v1alpha1.ReplicaTypeDiskful,
					}, oldTime),
					mkReplicaRequest("rvr-2", v1alpha1.DatameshMembershipRequest{
						Operation: v1alpha1.DatameshMembershipRequestOperationJoin,
						Type:      v1alpha1.ReplicaTypeDiskful,
					}, oldTime),
					mkReplicaRequest("rvr-4", v1alpha1.DatameshMembershipRequest{
						Operation: v1alpha1.DatameshMembershipRequestOperationJoin,
						Type:      v1alpha1.ReplicaTypeDiskful,
					}, oldTime),
				},
			},
		}
		// rvr-1: update role to Access.
		// rvr-2: nil transition (removed).
		// rvr-3: new entry (added).
		// rvr-4: unchanged.
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rvr-1", mkRequest(v1alpha1.ReplicaTypeAccess)), // Update.
			mkRVR("rvr-2", nil), // Remove.
			mkRVR("rvr-3", mkRequest(v1alpha1.ReplicaTypeTieBreaker)), // Add.
			mkRVR("rvr-4", mkRequest(v1alpha1.ReplicaTypeDiskful)),    // Keep.
		}

		outcome := ensureDatameshReplicaRequests(ctx, rv, rvrs)

		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rv.Status.DatameshReplicaRequests).To(HaveLen(3))

		// Check rvr-1: updated.
		Expect(rv.Status.DatameshReplicaRequests[0].Name).To(Equal("rvr-1"))
		Expect(rv.Status.DatameshReplicaRequests[0].Request.Type).To(Equal(v1alpha1.ReplicaTypeAccess))

		// Check rvr-3: added.
		Expect(rv.Status.DatameshReplicaRequests[1].Name).To(Equal("rvr-3"))
		Expect(rv.Status.DatameshReplicaRequests[1].Request.Type).To(Equal(v1alpha1.ReplicaTypeTieBreaker))

		// Check rvr-4: kept (should have preserved timestamp).
		Expect(rv.Status.DatameshReplicaRequests[2].Name).To(Equal("rvr-4"))
		Expect(rv.Status.DatameshReplicaRequests[2].FirstObservedAt.Time).To(Equal(oldTime))
	})

	It("handles empty RVR list", func(ctx SpecContext) {
		oldTime := time.Now().Add(-1 * time.Hour)
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshReplicaRequests: []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
					mkReplicaRequest("rvr-1", v1alpha1.DatameshMembershipRequest{
						Operation: v1alpha1.DatameshMembershipRequestOperationJoin,
						Type:      v1alpha1.ReplicaTypeDiskful,
					}, oldTime),
				},
			},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{}

		outcome := ensureDatameshReplicaRequests(ctx, rv, rvrs)

		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rv.Status.DatameshReplicaRequests).To(BeEmpty())
	})

	It("handles empty existing entries", func(ctx SpecContext) {
		rv := &v1alpha1.ReplicatedVolume{}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{}

		outcome := ensureDatameshReplicaRequests(ctx, rv, rvrs)

		Expect(outcome.DidChange()).To(BeFalse())
		Expect(rv.Status.DatameshReplicaRequests).To(BeEmpty())
	})

	It("skips RVRs with nil transition during merge", func(ctx SpecContext) {
		rv := &v1alpha1.ReplicatedVolume{}
		// Mixed: some with transition, some without.
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rvr-1", nil), // Skip.
			mkRVR("rvr-2", mkRequest(v1alpha1.ReplicaTypeDiskful)), // Add.
			mkRVR("rvr-3", nil), // Skip.
			mkRVR("rvr-4", mkRequest(v1alpha1.ReplicaTypeAccess)), // Add.
		}

		outcome := ensureDatameshReplicaRequests(ctx, rv, rvrs)

		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rv.Status.DatameshReplicaRequests).To(HaveLen(2))
		Expect(rv.Status.DatameshReplicaRequests[0].Name).To(Equal("rvr-2"))
		Expect(rv.Status.DatameshReplicaRequests[1].Name).To(Equal("rvr-4"))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// ensureStatusSize tests
//

var _ = Describe("ensureStatusSize", func() {
	var (
		ctx context.Context
		rv  *v1alpha1.ReplicatedVolume
	)

	BeforeEach(func() {
		ctx = context.Background()
		rv = &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
		}
	})

	mkRVR := func(name string, size *resource.Quantity) *v1alpha1.ReplicatedVolumeReplica {
		return &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Status:     v1alpha1.ReplicatedVolumeReplicaStatus{Size: size},
		}
	}

	It("sets min size from two diskful members", func() {
		rv.Status.Datamesh.Members = []v1alpha1.DatameshMember{
			{Name: "rvr-1", Type: v1alpha1.DatameshMemberTypeDiskful},
			{Name: "rvr-2", Type: v1alpha1.DatameshMemberTypeDiskful},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rvr-1", ptr.To(resource.MustParse("10Gi"))),
			mkRVR("rvr-2", ptr.To(resource.MustParse("20Gi"))),
		}

		outcome := ensureStatusSize(ctx, rv, rvrs)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rv.Status.Size).NotTo(BeNil())
		Expect(rv.Status.Size.String()).To(Equal("10Gi"))
	})

	It("skips diskless members", func() {
		rv.Status.Datamesh.Members = []v1alpha1.DatameshMember{
			{Name: "rvr-1", Type: v1alpha1.DatameshMemberTypeDiskful},
			{Name: "rvr-2", Type: v1alpha1.DatameshMemberTypeAccess},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rvr-1", ptr.To(resource.MustParse("10Gi"))),
			mkRVR("rvr-2", nil),
		}

		outcome := ensureStatusSize(ctx, rv, rvrs)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rv.Status.Size).NotTo(BeNil())
		Expect(rv.Status.Size.String()).To(Equal("10Gi"))
	})

	It("sets nil when all members are diskless", func() {
		rv.Status.Datamesh.Members = []v1alpha1.DatameshMember{
			{Name: "rvr-1", Type: v1alpha1.DatameshMemberTypeAccess},
			{Name: "rvr-2", Type: v1alpha1.DatameshMemberTypeTieBreaker},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rvr-1", nil),
			mkRVR("rvr-2", nil),
		}

		outcome := ensureStatusSize(ctx, rv, rvrs)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeFalse())
		Expect(rv.Status.Size).To(BeNil())
	})

	It("sets nil when no members", func() {
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{}

		outcome := ensureStatusSize(ctx, rv, rvrs)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeFalse())
		Expect(rv.Status.Size).To(BeNil())
	})

	It("clears size when diskful member has nil size", func() {
		rv.Status.Size = ptr.To(resource.MustParse("10Gi"))
		rv.Status.Datamesh.Members = []v1alpha1.DatameshMember{
			{Name: "rvr-1", Type: v1alpha1.DatameshMemberTypeDiskful},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rvr-1", nil),
		}

		outcome := ensureStatusSize(ctx, rv, rvrs)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rv.Status.Size).To(BeNil())
	})

	It("reports no change when already up to date", func() {
		rv.Status.Datamesh.Members = []v1alpha1.DatameshMember{
			{Name: "rvr-1", Type: v1alpha1.DatameshMemberTypeDiskful},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rvr-1", ptr.To(resource.MustParse("10Gi"))),
		}

		outcome1 := ensureStatusSize(ctx, rv, rvrs)
		Expect(outcome1.Error()).NotTo(HaveOccurred())
		Expect(outcome1.DidChange()).To(BeTrue())

		outcome2 := ensureStatusSize(ctx, rv, rvrs)
		Expect(outcome2.Error()).NotTo(HaveOccurred())
		Expect(outcome2.DidChange()).To(BeFalse())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Pure helper function tests
//

var _ = Describe("rvShouldNotExist", func() {
	It("returns true for nil RV", func() {
		Expect(rvShouldNotExist(nil)).To(BeTrue())
	})

	It("returns false when DeletionTimestamp is nil", func() {
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
		}
		Expect(rvShouldNotExist(rv)).To(BeFalse())
	})

	It("returns false when other finalizers present", func() {
		now := metav1.Now()
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "rv-1",
				DeletionTimestamp: &now,
				Finalizers:        []string{v1alpha1.RVControllerFinalizer, "other/finalizer"},
			},
		}
		Expect(rvShouldNotExist(rv)).To(BeFalse())
	})

	It("returns false when attached members exist (post-formation)", func() {
		now := metav1.Now()
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "rv-1",
				DeletionTimestamp: &now,
				Finalizers:        []string{v1alpha1.RVControllerFinalizer},
			},
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshRevision: 1,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rvr-1", Attached: true},
					},
				},
			},
		}
		Expect(rvShouldNotExist(rv)).To(BeFalse())
	})

	It("returns false when Detach transition in progress (post-formation)", func() {
		now := metav1.Now()
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "rv-1",
				DeletionTimestamp: &now,
				Finalizers:        []string{v1alpha1.RVControllerFinalizer},
			},
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshRevision: 1,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rvr-1", Attached: false},
					},
				},
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach, Group: v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment, ReplicaName: "rvr-1", Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, StartedAt: ptr.To(now)}}},
				},
			},
		}
		Expect(rvShouldNotExist(rv)).To(BeFalse())
	})

	It("returns true when deleting with only our finalizer, no attached members, and no Detach transitions (post-formation)", func() {
		now := metav1.Now()
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "rv-1",
				DeletionTimestamp: &now,
				Finalizers:        []string{v1alpha1.RVControllerFinalizer},
			},
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshRevision: 1,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rvr-1", Attached: false},
					},
				},
			},
		}
		Expect(rvShouldNotExist(rv)).To(BeTrue())
	})

	It("returns true during formation even with attached members (DatameshRevision == 0)", func() {
		now := metav1.Now()
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "rv-1",
				DeletionTimestamp: &now,
				Finalizers:        []string{v1alpha1.RVControllerFinalizer},
			},
			Status: v1alpha1.ReplicatedVolumeStatus{
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rvr-1", Attached: true},
					},
				},
			},
		}
		Expect(rvShouldNotExist(rv)).To(BeTrue())
	})

	It("returns true during formation even with attached members (active Formation transition)", func() {
		now := metav1.Now()
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "rv-1",
				DeletionTimestamp: &now,
				Finalizers:        []string{v1alpha1.RVControllerFinalizer},
			},
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshRevision: 1,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rvr-1", Attached: true},
					},
				},
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					{
						Type:  v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation,
						Group: v1alpha1.ReplicatedVolumeDatameshTransitionGroupFormation,
						Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
							{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, StartedAt: ptr.To(now)},
						},
					},
				},
			},
		}
		Expect(rvShouldNotExist(rv)).To(BeTrue())
	})
})

// IntendedLayout is the single source of truth for the D/TB formula that formation and
// computeTargetQuorum consume. Verify both counts across all valid FTT/GMDR combinations.
var _ = Describe("IntendedLayout (D/TB formula used by formation)", func() {
	It("returns D = FTT + GMDR + 1 and the correct tie-breaker count", func() {
		cases := []struct {
			ftt, gmdr     byte
			wantD, wantTB int
			desc          string
		}{
			{0, 0, 1, 0, "1D"},
			{0, 1, 2, 0, "2D (Consistency)"},
			{1, 0, 2, 1, "2D+1TB (Availability, r2)"},
			{1, 1, 3, 0, "3D (ConsistencyAndAvailability, r3)"},
			{2, 1, 4, 1, "4D+1TB (FTT=2=D/2)"},
			{1, 2, 4, 0, "4D (FTT=1!=D/2)"},
			{2, 2, 5, 0, "5D"},
		}
		for _, tc := range cases {
			cfg := v1alpha1.ReplicatedVolumeConfiguration{
				FailuresToTolerate: tc.ftt, GuaranteedMinimumDataRedundancy: tc.gmdr,
			}
			d, tb := cfg.IntendedLayout()
			Expect(d).To(Equal(tc.wantD), "D for %s (FTT=%d, GMDR=%d)", tc.desc, tc.ftt, tc.gmdr)
			Expect(tb).To(Equal(tc.wantTB), "TB for %s (FTT=%d, GMDR=%d)", tc.desc, tc.ftt, tc.gmdr)
		}
	})
})

var _ = Describe("computeTargetQuorum", func() {
	mkRVWithMembers := func(ftt, gmdr byte, diskfulCount int) *v1alpha1.ReplicatedVolume {
		members := make([]v1alpha1.DatameshMember, diskfulCount)
		for i := range diskfulCount {
			members[i] = v1alpha1.DatameshMember{
				Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", uint8(i)),
				Type: v1alpha1.DatameshMemberTypeDiskful,
			}
		}
		return &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
					FailuresToTolerate: ftt, GuaranteedMinimumDataRedundancy: gmdr,
					VolumeAccess: v1alpha1.VolumeAccessPreferablyLocal, ReplicatedStoragePoolName: "test-pool",
				},
				BaselineGuaranteedMinimumDataRedundancy: gmdr,
				Datamesh:                                v1alpha1.ReplicatedVolumeDatamesh{Members: members},
			},
		}
	}

	// q = floor(D/2) + 1 (or floor(voters/2)+1, whichever is larger)
	// qmr = GMDR + 1

	It("returns q=1 qmr=1 for FTT=0,GMDR=0 with 1 voter", func() {
		q, qmr := computeTargetQuorum(mkRVWithMembers(0, 0, 1))
		Expect(q).To(Equal(byte(1)))
		Expect(qmr).To(Equal(byte(1)))
	})

	It("returns q=2 qmr=1 for FTT=1,GMDR=0 with 2 voters", func() {
		// D=2, q=max(floor(2/2)+1, floor(2/2)+1)=2; qmr=GMDR+1=1
		q, qmr := computeTargetQuorum(mkRVWithMembers(1, 0, 2))
		Expect(q).To(Equal(byte(2)))
		Expect(qmr).To(Equal(byte(1)))
	})

	It("returns q=2 qmr=2 for FTT=0,GMDR=1 with 2 voters", func() {
		q, qmr := computeTargetQuorum(mkRVWithMembers(0, 1, 2))
		Expect(q).To(Equal(byte(2)))
		Expect(qmr).To(Equal(byte(2)))
	})

	It("returns q=2 qmr=2 for FTT=1,GMDR=1 with 3 voters", func() {
		q, qmr := computeTargetQuorum(mkRVWithMembers(1, 1, 3))
		Expect(q).To(Equal(byte(2)))
		Expect(qmr).To(Equal(byte(2)))
	})
})

var _ = Describe("computeActualSchedulingFailureMessages", func() {
	mkRVR := func(id uint8, condStatus metav1.ConditionStatus, message string) *v1alpha1.ReplicatedVolumeReplica {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", id),
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1",
				Type:                 v1alpha1.ReplicaTypeDiskful,
			},
		}
		if condStatus != "" {
			obju.SetStatusCondition(rvr, metav1.Condition{
				Type:    v1alpha1.ReplicatedVolumeReplicaCondScheduledType,
				Status:  condStatus,
				Reason:  v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonSchedulingFailed,
				Message: message,
			})
		}
		return rvr
	}

	It("returns nil when no RVRs have Scheduled=False", func() {
		rvr0 := mkRVR(0, "", "")                             // no condition
		rvr1 := mkRVR(1, metav1.ConditionTrue, "all good")   // Scheduled=True
		rvr2 := mkRVR(2, metav1.ConditionUnknown, "waiting") // Scheduled=Unknown
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr0, rvr1, rvr2}
		ids := idset.Of(0).Union(idset.Of(1)).Union(idset.Of(2))
		Expect(computeActualSchedulingFailureMessages(rvrs, ids)).To(BeNil())
	})

	It("returns the message when one RVR has Scheduled=False", func() {
		rvr0 := mkRVR(0, metav1.ConditionFalse, "2 candidates; 2 excluded: node not ready")
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr0}
		ids := idset.Of(0)
		Expect(computeActualSchedulingFailureMessages(rvrs, ids)).To(Equal(
			[]string{"2 candidates; 2 excluded: node not ready"},
		))
	})

	It("deduplicates identical messages from multiple RVRs", func() {
		msg := "4 candidates; 4 excluded: node not ready"
		rvr0 := mkRVR(0, metav1.ConditionFalse, msg)
		rvr1 := mkRVR(1, metav1.ConditionFalse, msg)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr0, rvr1}
		ids := idset.Of(0).Union(idset.Of(1))
		result := computeActualSchedulingFailureMessages(rvrs, ids)
		Expect(result).To(HaveLen(1))
		Expect(result[0]).To(Equal(msg))
	})

	It("returns sorted distinct messages from multiple RVRs", func() {
		rvr0 := mkRVR(0, metav1.ConditionFalse, "extender unavailable")
		rvr1 := mkRVR(1, metav1.ConditionFalse, "2 candidates; 2 excluded: node not ready")
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr0, rvr1}
		ids := idset.Of(0).Union(idset.Of(1))
		Expect(computeActualSchedulingFailureMessages(rvrs, ids)).To(Equal(
			[]string{"2 candidates; 2 excluded: node not ready", "extender unavailable"},
		))
	})

	It("skips RVRs not in the ID set", func() {
		rvr0 := mkRVR(0, metav1.ConditionFalse, "should appear")
		rvr1 := mkRVR(1, metav1.ConditionFalse, "should be skipped")
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr0, rvr1}
		ids := idset.Of(0) // only ID 0
		Expect(computeActualSchedulingFailureMessages(rvrs, ids)).To(Equal(
			[]string{"should appear"},
		))
	})

	It("skips RVRs with Scheduled=False but empty message", func() {
		rvr0 := mkRVR(0, metav1.ConditionFalse, "")
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr0}
		ids := idset.Of(0)
		Expect(computeActualSchedulingFailureMessages(rvrs, ids)).To(BeNil())
	})
})

var _ = Describe("computeFormationPreconfigureWaitMessage", func() {
	mkRVR := func(id uint8, condStatus metav1.ConditionStatus, message string) *v1alpha1.ReplicatedVolumeReplica {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", id),
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1",
				Type:                 v1alpha1.ReplicaTypeDiskful,
			},
		}
		if condStatus != "" {
			obju.SetStatusCondition(rvr, metav1.Condition{
				Type:    v1alpha1.ReplicatedVolumeReplicaCondScheduledType,
				Status:  condStatus,
				Reason:  v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonSchedulingFailed,
				Message: message,
			})
		}
		return rvr
	}

	It("shows only pending scheduling when no failures or preconfiguring", func() {
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR(0, "", "")}
		msg := computeFormationPreconfigureWaitMessage(rvrs, 3,
			idset.Of(0), idset.IDSet(0), idset.IDSet(0))
		Expect(msg).To(Equal("Waiting for 1/3 replicas: pending scheduling [#0]"))
	})

	It("shows only scheduling failed with inline error", func() {
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR(0, metav1.ConditionFalse, "2 candidates; 2 excluded: node not ready"),
		}
		msg := computeFormationPreconfigureWaitMessage(rvrs, 3,
			idset.IDSet(0), idset.Of(0), idset.IDSet(0))
		Expect(msg).To(Equal("Waiting for 1/3 replicas: scheduling failed [#0] (2 candidates; 2 excluded: node not ready)"))
	})

	It("shows only preconfiguring", func() {
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR(0, "", "")}
		msg := computeFormationPreconfigureWaitMessage(rvrs, 3,
			idset.IDSet(0), idset.IDSet(0), idset.Of(0))
		Expect(msg).To(Equal("Waiting for 1/3 replicas: preconfiguring [#0]"))
	})

	It("shows all three groups when all non-empty", func() {
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR(0, "", ""),
			mkRVR(1, metav1.ConditionFalse, "node not ready"),
			mkRVR(2, "", ""),
		}
		msg := computeFormationPreconfigureWaitMessage(rvrs, 3,
			idset.Of(0), idset.Of(1), idset.Of(2))
		Expect(msg).To(Equal("Waiting for 3/3 replicas: pending scheduling [#0], scheduling failed [#1] (node not ready), preconfiguring [#2]"))
	})

	It("counts waiting replicas correctly (not total diskful)", func() {
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR(2, "", "")}
		msg := computeFormationPreconfigureWaitMessage(rvrs, 3,
			idset.IDSet(0), idset.IDSet(0), idset.Of(2))
		Expect(msg).To(HavePrefix("Waiting for 1/3"))
	})

	It("joins multiple scheduling failure messages with pipe", func() {
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR(0, metav1.ConditionFalse, "extender unavailable"),
			mkRVR(1, metav1.ConditionFalse, "2 candidates; 2 excluded: node not ready"),
		}
		msg := computeFormationPreconfigureWaitMessage(rvrs, 3,
			idset.IDSet(0), idset.Of(0).Union(idset.Of(1)), idset.IDSet(0))
		Expect(msg).To(ContainSubstring("(2 candidates; 2 excluded: node not ready | extender unavailable)"))
	})

	It("omits parentheses when scheduling failed has no messages", func() {
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR(0, metav1.ConditionFalse, ""), // Scheduled=False but empty message
		}
		msg := computeFormationPreconfigureWaitMessage(rvrs, 1,
			idset.IDSet(0), idset.Of(0), idset.IDSet(0))
		Expect(msg).To(Equal("Waiting for 1/1 replicas: scheduling failed [#0]"))
		Expect(msg).NotTo(ContainSubstring("("))
	})
})

var _ = Describe("applyDatameshMember", func() {
	It("adds new member and returns true", func() {
		rv := &v1alpha1.ReplicatedVolume{}
		member := v1alpha1.DatameshMember{
			Name:     v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0),
			Type:     v1alpha1.DatameshMemberTypeDiskful,
			NodeName: "node-1",
		}
		changed := applyDatameshMember(rv, member)
		Expect(changed).To(BeTrue())
		Expect(rv.Status.Datamesh.Members).To(HaveLen(1))
		Expect(rv.Status.Datamesh.Members[0].NodeName).To(Equal("node-1"))
	})

	It("returns false when member data is identical", func() {
		member := v1alpha1.DatameshMember{
			Name:     v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0),
			Type:     v1alpha1.DatameshMemberTypeDiskful,
			NodeName: "node-1",
		}
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{member},
				},
			},
		}
		changed := applyDatameshMember(rv, member)
		Expect(changed).To(BeFalse())
	})

	It("updates changed field and returns true", func() {
		existing := v1alpha1.DatameshMember{
			Name:     v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0),
			Type:     v1alpha1.DatameshMemberTypeDiskful,
			NodeName: "node-1",
		}
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{existing},
				},
			},
		}
		updated := existing
		updated.NodeName = "node-2"
		changed := applyDatameshMember(rv, updated)
		Expect(changed).To(BeTrue())
		Expect(rv.Status.Datamesh.Members[0].NodeName).To(Equal("node-2"))
	})
})

var _ = Describe("ensureDatameshMemberAddresses", func() {
	addr := func(ip string, port uint) v1alpha1.DRBDResourceAddressStatus {
		return v1alpha1.DRBDResourceAddressStatus{
			SystemNetworkName: "Internal",
			Address:           v1alpha1.DRBDAddress{IPv4: ip, Port: port},
		}
	}

	It("returns false when addresses match", func() {
		addrs := []v1alpha1.DRBDResourceAddressStatus{addr("10.0.0.1", 7000)}
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshRevision: 5,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0), Addresses: addrs},
					},
				},
			},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{{
			ObjectMeta: metav1.ObjectMeta{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0)},
			Status:     v1alpha1.ReplicatedVolumeReplicaStatus{Addresses: addrs},
		}}
		Expect(ensureDatameshMemberAddresses(rv, rvrs)).To(BeFalse())
		Expect(rv.Status.DatameshRevision).To(BeEquivalentTo(5))
	})

	It("updates addresses and bumps revision when port changed", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshRevision: 5,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0),
							Addresses: []v1alpha1.DRBDResourceAddressStatus{addr("10.0.0.1", 7000)}},
					},
				},
			},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{{
			ObjectMeta: metav1.ObjectMeta{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0)},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				Addresses: []v1alpha1.DRBDResourceAddressStatus{addr("10.0.0.1", 7078)},
			},
		}}
		Expect(ensureDatameshMemberAddresses(rv, rvrs)).To(BeTrue())
		Expect(rv.Status.DatameshRevision).To(BeEquivalentTo(6))
		Expect(rv.Status.Datamesh.Members[0].Addresses[0].Address.Port).To(Equal(uint(7078)))
	})

	It("bumps revision only once for multiple address changes", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshRevision: 3,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0),
							Addresses: []v1alpha1.DRBDResourceAddressStatus{addr("10.0.0.1", 7000)}},
						{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 1),
							Addresses: []v1alpha1.DRBDResourceAddressStatus{addr("10.0.0.2", 7000)}},
					},
				},
			},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			{
				ObjectMeta: metav1.ObjectMeta{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0)},
				Status: v1alpha1.ReplicatedVolumeReplicaStatus{
					Addresses: []v1alpha1.DRBDResourceAddressStatus{addr("10.0.0.1", 8000)},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 1)},
				Status: v1alpha1.ReplicatedVolumeReplicaStatus{
					Addresses: []v1alpha1.DRBDResourceAddressStatus{addr("10.0.0.2", 9000)},
				},
			},
		}
		Expect(ensureDatameshMemberAddresses(rv, rvrs)).To(BeTrue())
		Expect(rv.Status.DatameshRevision).To(BeEquivalentTo(4))
	})

	It("skips members without matching RVR", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshRevision: 5,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0),
							Addresses: []v1alpha1.DRBDResourceAddressStatus{addr("10.0.0.1", 7000)}},
					},
				},
			},
		}
		Expect(ensureDatameshMemberAddresses(rv, nil)).To(BeFalse())
		Expect(rv.Status.DatameshRevision).To(BeEquivalentTo(5))
	})
})

var _ = Describe("applyDatameshReplicaRequestMessages", func() {
	It("updates message for matching node and returns true", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshReplicaRequests: []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
					{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0), Message: "old"},
					{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 1), Message: "other"},
				},
			},
		}
		var ids idset.IDSet
		ids.Add(0)
		changed := applyDatameshReplicaRequestMessages(rv, ids, "new")
		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(Equal("new"))
		Expect(rv.Status.DatameshReplicaRequests[1].Message).To(Equal("other"))
	})

	It("returns false when message is already the same", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshReplicaRequests: []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
					{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0), Message: "same"},
				},
			},
		}
		var ids idset.IDSet
		ids.Add(0)
		changed := applyDatameshReplicaRequestMessages(rv, ids, "same")
		Expect(changed).To(BeFalse())
	})

	It("returns false when no nodes match", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshReplicaRequests: []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
					{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0), Message: "msg"},
				},
			},
		}
		var ids idset.IDSet
		ids.Add(5) // ID 5 does not match any entry.
		changed := applyDatameshReplicaRequestMessages(rv, ids, "new")
		Expect(changed).To(BeFalse())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Formation: Preconfigure (normal path, safety checks, excess removal)
//

var _ = Describe("isRVMetadataInSync", func() {
	It("returns true when finalizer present, label matches, targetFinalizerPresent=true", func() {
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
				Labels:     map[string]string{v1alpha1.ReplicatedStorageClassLabelKey: "rsc-1"},
			},
			Spec: v1alpha1.ReplicatedVolumeSpec{ReplicatedStorageClassName: "rsc-1"},
		}
		Expect(isRVMetadataInSync(rv, true)).To(BeTrue())
	})

	It("returns false when finalizer absent but targetFinalizerPresent=true", func() {
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{v1alpha1.ReplicatedStorageClassLabelKey: "rsc-1"},
			},
			Spec: v1alpha1.ReplicatedVolumeSpec{ReplicatedStorageClassName: "rsc-1"},
		}
		Expect(isRVMetadataInSync(rv, true)).To(BeFalse())
	})

	It("returns false when finalizer present but targetFinalizerPresent=false", func() {
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
			},
		}
		Expect(isRVMetadataInSync(rv, false)).To(BeFalse())
	})

	It("returns false when label does not match spec", func() {
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
				Labels:     map[string]string{v1alpha1.ReplicatedStorageClassLabelKey: "old-rsc"},
			},
			Spec: v1alpha1.ReplicatedVolumeSpec{ReplicatedStorageClassName: "new-rsc"},
		}
		Expect(isRVMetadataInSync(rv, true)).To(BeFalse())
	})

	It("returns true when ReplicatedStorageClassName is empty (label not checked)", func() {
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeSpec{ReplicatedStorageClassName: ""},
		}
		Expect(isRVMetadataInSync(rv, true)).To(BeTrue())
	})

	It("returns true when no finalizer and targetFinalizerPresent=false", func() {
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{},
			Spec:       v1alpha1.ReplicatedVolumeSpec{ReplicatedStorageClassName: ""},
		}
		Expect(isRVMetadataInSync(rv, false)).To(BeTrue())
	})
})

var _ = Describe("applyRVMetadata", func() {
	It("adds finalizer and returns true", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Spec: v1alpha1.ReplicatedVolumeSpec{ReplicatedStorageClassName: ""},
		}
		changed := applyRVMetadata(rv, true)
		Expect(changed).To(BeTrue())
		Expect(obju.HasFinalizer(rv, v1alpha1.RVControllerFinalizer)).To(BeTrue())
	})

	It("removes finalizer and returns true", func() {
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Finalizers: []string{v1alpha1.RVControllerFinalizer}},
			Spec:       v1alpha1.ReplicatedVolumeSpec{ReplicatedStorageClassName: ""},
		}
		changed := applyRVMetadata(rv, false)
		Expect(changed).To(BeTrue())
		Expect(obju.HasFinalizer(rv, v1alpha1.RVControllerFinalizer)).To(BeFalse())
	})

	It("sets label and returns true", func() {
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Finalizers: []string{v1alpha1.RVControllerFinalizer}},
			Spec:       v1alpha1.ReplicatedVolumeSpec{ReplicatedStorageClassName: "rsc-1"},
		}
		changed := applyRVMetadata(rv, true)
		Expect(changed).To(BeTrue())
		Expect(obju.HasLabelValue(rv, v1alpha1.ReplicatedStorageClassLabelKey, "rsc-1")).To(BeTrue())
	})

	It("returns false when nothing changed (idempotent)", func() {
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
				Labels:     map[string]string{v1alpha1.ReplicatedStorageClassLabelKey: "rsc-1"},
			},
			Spec: v1alpha1.ReplicatedVolumeSpec{ReplicatedStorageClassName: "rsc-1"},
		}
		changed := applyRVMetadata(rv, true)
		Expect(changed).To(BeFalse())
	})

	It("skips label when ReplicatedStorageClassName is empty", func() {
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Finalizers: []string{v1alpha1.RVControllerFinalizer}},
			Spec:       v1alpha1.ReplicatedVolumeSpec{ReplicatedStorageClassName: ""},
		}
		changed := applyRVMetadata(rv, true)
		Expect(changed).To(BeFalse())
		Expect(rv.Labels).To(BeNil())
	})
})

// isTransZonalZoneCountValid
//

var _ = Describe("isTransZonalZoneCountValid", func() {
	It("validates all FTT/GMDR + zone count combinations", func() {
		cases := []struct {
			ftt, gmdr byte
			zones     int
			valid     bool
		}{
			// FTT=0, GMDR=0: not TransZonal.
			{0, 0, 1, false},
			{0, 0, 3, false},
			// FTT=0, GMDR=1: 2D → exactly 2 zones.
			{0, 1, 1, false},
			{0, 1, 2, true},
			{0, 1, 3, false},
			// FTT=1, GMDR=0: 2D+1TB → exactly 3 zones.
			{1, 0, 2, false},
			{1, 0, 3, true},
			{1, 0, 4, false},
			// FTT=1, GMDR=1: 3D → exactly 3 zones.
			{1, 1, 2, false},
			{1, 1, 3, true},
			{1, 1, 4, false},
			// FTT=1, GMDR=2: 4D+1TB → 3 or 5 zones.
			{1, 2, 2, false},
			{1, 2, 3, true},
			{1, 2, 4, false},
			{1, 2, 5, true},
			{1, 2, 6, false},
			// FTT=2, GMDR=1: 4D → exactly 4 zones.
			{2, 1, 3, false},
			{2, 1, 4, true},
			{2, 1, 5, false},
			// FTT=2, GMDR=2: 5D → 3 or 5 zones.
			{2, 2, 2, false},
			{2, 2, 3, true},
			{2, 2, 4, false},
			{2, 2, 5, true},
			{2, 2, 6, false},
		}
		for _, tc := range cases {
			result := isTransZonalZoneCountValid(tc.ftt, tc.gmdr, tc.zones)
			Expect(result).To(Equal(tc.valid),
				"FTT=%d, GMDR=%d, zones=%d: expected %v, got %v",
				tc.ftt, tc.gmdr, tc.zones, tc.valid, result)
		}
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// computeTargetQuorum edge cases
//

var _ = Describe("computeTargetQuorum edge cases", func() {
	It("counts liminal Diskful members as intended diskful", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
					Topology: v1alpha1.TopologyIgnored, FailuresToTolerate: 1, GuaranteedMinimumDataRedundancy: 0,
					VolumeAccess: v1alpha1.VolumeAccessPreferablyLocal, ReplicatedStoragePoolName: "test-pool",
				},
				BaselineGuaranteedMinimumDataRedundancy: 0,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0), Type: v1alpha1.DatameshMemberTypeDiskful},
						// LiminalDiskful member should count as diskful.
						{
							Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 1),
							Type: v1alpha1.DatameshMemberTypeLiminalDiskful,
						},
					},
				},
			},
		}
		q, qmr := computeTargetQuorum(rv)
		// 2 voters (D + D∅) → q=max(2/2+1, 2/2+1)=2; qmr=GMDR+1=1
		Expect(q).To(Equal(byte(2)))
		Expect(qmr).To(Equal(byte(1)))
	})

	It("does not count ShadowDiskful as voter", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
					Topology: v1alpha1.TopologyIgnored, FailuresToTolerate: 1, GuaranteedMinimumDataRedundancy: 0,
					VolumeAccess: v1alpha1.VolumeAccessPreferablyLocal, ReplicatedStoragePoolName: "test-pool",
				},
				BaselineGuaranteedMinimumDataRedundancy: 0,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0), Type: v1alpha1.DatameshMemberTypeDiskful},
						{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 1), Type: v1alpha1.DatameshMemberTypeDiskful},
						// ShadowDiskful should NOT be counted as a voter.
						{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 2), Type: v1alpha1.DatameshMemberTypeShadowDiskful},
					},
				},
			},
		}
		q, qmr := computeTargetQuorum(rv)
		// Only 2 voters (Diskful) → q=max(2/2+1, 2/2+1)=2; qmr=GMDR+1=1
		Expect(q).To(Equal(byte(2)))
		Expect(qmr).To(Equal(byte(1)))
	})

	It("does not count non-diskful members", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
					Topology: v1alpha1.TopologyIgnored, FailuresToTolerate: 0, GuaranteedMinimumDataRedundancy: 0,
					VolumeAccess: v1alpha1.VolumeAccessLocal, ReplicatedStoragePoolName: "test-pool",
				},
				BaselineGuaranteedMinimumDataRedundancy: 0,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0), Type: v1alpha1.DatameshMemberTypeDiskful},
						{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 1), Type: v1alpha1.DatameshMemberTypeAccess},
						{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 2), Type: v1alpha1.DatameshMemberTypeTieBreaker},
					},
				},
			},
		}
		q, qmr := computeTargetQuorum(rv)
		// Only 1 diskful → quorum = 1/2+1 = 1; minQ=1, minQMR=1 → q=1, qmr=1
		Expect(q).To(Equal(byte(1)))
		Expect(qmr).To(Equal(byte(1)))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// I/O helper edge cases
//

var _ = Describe("deleteRVRWithForcedFinalizerRemoval", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	})

	It("handles NotFound during patch (stale cache)", func(ctx SpecContext) {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "rvr-1",
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1",
				Type:                 v1alpha1.ReplicaTypeDiskful,
			},
		}

		notFoundErr := apierrors.NewNotFound(
			schema.GroupResource{Group: v1alpha1.SchemeGroupVersion.Group, Resource: "replicatedvolumereplicas"}, "rvr-1")
		cl := newClientBuilder(scheme).
			WithObjects(rvr).
			WithStatusSubresource(rvr).
			WithInterceptorFuncs(interceptor.Funcs{
				Patch: func(ctx context.Context, cl client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
					if _, ok := obj.(*v1alpha1.ReplicatedVolumeReplica); ok {
						return notFoundErr
					}
					return cl.Patch(ctx, obj, patch, opts...)
				},
			}).
			Build()
		rec := NewReconciler(cl, scheme)

		err := rec.deleteRVRWithForcedFinalizerRemoval(ctx, rvr)
		Expect(err).NotTo(HaveOccurred())
		// Should set DeletionTimestamp locally.
		Expect(rvr.DeletionTimestamp).NotTo(BeNil())
	})

	It("skips patch when no finalizer present", func(ctx SpecContext) {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1",
				Type:                 v1alpha1.ReplicaTypeDiskful,
			},
		}

		patchCalled := false
		cl := newClientBuilder(scheme).
			WithObjects(rvr).
			WithStatusSubresource(rvr).
			WithInterceptorFuncs(interceptor.Funcs{
				Patch: func(ctx context.Context, cl client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
					if _, ok := obj.(*v1alpha1.ReplicatedVolumeReplica); ok {
						patchCalled = true
					}
					return cl.Patch(ctx, obj, patch, opts...)
				},
			}).
			Build()
		rec := NewReconciler(cl, scheme)

		err := rec.deleteRVRWithForcedFinalizerRemoval(ctx, rvr)
		Expect(err).NotTo(HaveOccurred())
		Expect(patchCalled).To(BeFalse(), "should not patch when no finalizer present")
	})

	It("propagates non-NotFound patch errors", func(ctx SpecContext) {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "rvr-1",
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1",
				Type:                 v1alpha1.ReplicaTypeDiskful,
			},
		}

		testErr := errors.New("patch failed")
		cl := newClientBuilder(scheme).
			WithObjects(rvr).
			WithStatusSubresource(rvr).
			WithInterceptorFuncs(interceptor.Funcs{
				Patch: func(ctx context.Context, cl client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
					if _, ok := obj.(*v1alpha1.ReplicatedVolumeReplica); ok {
						return testErr
					}
					return cl.Patch(ctx, obj, patch, opts...)
				},
			}).
			Build()
		rec := NewReconciler(cl, scheme)

		err := rec.deleteRVRWithForcedFinalizerRemoval(ctx, rvr)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, testErr)).To(BeTrue())
	})
})

var _ = Describe("reconcileDeletion error paths", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	})

	It("returns error when patchRVAStatus fails", func(ctx SpecContext) {
		now := metav1.Now()
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "rv-1",
				DeletionTimestamp: &now,
				Finalizers:        []string{v1alpha1.RVControllerFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeSpec{
				Size:                       resource.MustParse("10Gi"),
				ReplicatedStorageClassName: "rsc-1",
			},
		}

		rva := &v1alpha1.ReplicatedVolumeAttachment{
			ObjectMeta: metav1.ObjectMeta{Name: "rva-1"},
			Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
				ReplicatedVolumeName: "rv-1",
				NodeName:             "node-1",
			},
		}

		testErr := errors.New("status patch failed")
		cl := newClientBuilder(scheme).
			WithObjects(rv, rva).
			WithStatusSubresource(rv, rva).
			WithInterceptorFuncs(interceptor.Funcs{
				SubResourcePatch: func(ctx context.Context, cl client.Client, _ string, obj client.Object, patch client.Patch, _ ...client.SubResourcePatchOption) error {
					if _, ok := obj.(*v1alpha1.ReplicatedVolumeAttachment); ok {
						return testErr
					}
					return cl.Status().Patch(ctx, obj, patch)
				},
			}).
			Build()
		rec := NewReconciler(cl, scheme)

		_, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, testErr)).To(BeTrue())
	})

	It("ignores NotFound from patchRVAStatus (RVA deleted between read and patch)", func(ctx SpecContext) {
		now := metav1.Now()
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "rv-1",
				DeletionTimestamp: &now,
				Finalizers:        []string{v1alpha1.RVControllerFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeSpec{
				Size:                       resource.MustParse("10Gi"),
				ReplicatedStorageClassName: "rsc-1",
			},
		}

		rva := &v1alpha1.ReplicatedVolumeAttachment{
			ObjectMeta: metav1.ObjectMeta{Name: "rva-1"},
			Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
				ReplicatedVolumeName: "rv-1",
				NodeName:             "node-1",
			},
		}

		cl := newClientBuilder(scheme).
			WithObjects(rv, rva).
			WithStatusSubresource(rv, rva).
			WithInterceptorFuncs(interceptor.Funcs{
				SubResourcePatch: func(ctx context.Context, cl client.Client, _ string, obj client.Object, patch client.Patch, _ ...client.SubResourcePatchOption) error {
					if _, ok := obj.(*v1alpha1.ReplicatedVolumeAttachment); ok {
						return apierrors.NewNotFound(schema.GroupResource{
							Group:    v1alpha1.SchemeGroupVersion.Group,
							Resource: "replicatedvolumeattachments",
						}, "rva-1")
					}
					return cl.Status().Patch(ctx, obj, patch)
				},
			}).
			Build()
		rec := NewReconciler(cl, scheme)

		_, err := rec.Reconcile(ctx, RequestFor(rv))
		// NotFound from patchRVAStatus should be ignored — no error propagated from RVA patching.
		// The reconcile may still return an error from other steps (e.g., requeue),
		// but it must NOT be a NotFound error for the RVA.
		if err != nil {
			Expect(apierrors.IsNotFound(err)).To(BeFalse(), "NotFound from patchRVAStatus should be ignored, got: %v", err)
		}
	})

	It("returns error when deleteRVRWithForcedFinalizerRemoval fails during deletion", func(ctx SpecContext) {
		now := metav1.Now()
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "rv-1",
				DeletionTimestamp: &now,
				Finalizers:        []string{v1alpha1.RVControllerFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeSpec{
				Size:                       resource.MustParse("10Gi"),
				ReplicatedStorageClassName: "rsc-1",
			},
		}

		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "rvr-1",
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1",
				Type:                 v1alpha1.ReplicaTypeDiskful,
				NodeName:             "node-1",
				LVMVolumeGroupName:   "lvg-1",
			},
		}

		testErr := errors.New("patch RVR failed")
		cl := newClientBuilder(scheme).
			WithObjects(rv, rvr).
			WithStatusSubresource(rv, rvr).
			WithInterceptorFuncs(interceptor.Funcs{
				Patch: func(ctx context.Context, cl client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
					if _, ok := obj.(*v1alpha1.ReplicatedVolumeReplica); ok {
						return testErr
					}
					return cl.Patch(ctx, obj, patch, opts...)
				},
			}).
			Build()
		rec := NewReconciler(cl, scheme)

		_, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, testErr)).To(BeTrue())
	})

	It("returns error when patchRVStatus fails during member clearing", func(ctx SpecContext) {
		now := metav1.Now()
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "rv-1",
				DeletionTimestamp: &now,
				Finalizers:        []string{v1alpha1.RVControllerFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeSpec{
				Size:                       resource.MustParse("10Gi"),
				ReplicatedStorageClassName: "rsc-1",
			},
			Status: v1alpha1.ReplicatedVolumeStatus{
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rvr-1", Attached: false},
					},
				},
			},
		}

		testErr := errors.New("status patch failed")
		cl := newClientBuilder(scheme).
			WithObjects(rv).
			WithStatusSubresource(rv).
			WithInterceptorFuncs(interceptor.Funcs{
				SubResourcePatch: func(ctx context.Context, cl client.Client, _ string, obj client.Object, patch client.Patch, _ ...client.SubResourcePatchOption) error {
					if _, ok := obj.(*v1alpha1.ReplicatedVolume); ok {
						return testErr
					}
					return cl.Status().Patch(ctx, obj, patch)
				},
			}).
			Build()
		rec := NewReconciler(cl, scheme)

		_, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, testErr)).To(BeTrue())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Root Reconcile: additional error/edge cases
//

var _ = Describe("Root Reconcile edge cases", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	})

	It("returns error when getRSP fails during formation", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1")

		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "rv-1",
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
				Labels:     map[string]string{v1alpha1.ReplicatedStorageClassLabelKey: "rsc-1"},
			},
			Spec: v1alpha1.ReplicatedVolumeSpec{
				Size:                       resource.MustParse("10Gi"),
				ReplicatedStorageClassName: "rsc-1",
			},
			Status: v1alpha1.ReplicatedVolumeStatus{
				ConfigurationGeneration:         1,
				ConfigurationObservedGeneration: 1,
				Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
					Topology: v1alpha1.TopologyIgnored, FailuresToTolerate: 0, GuaranteedMinimumDataRedundancy: 0,
					VolumeAccess: v1alpha1.VolumeAccessLocal, ReplicatedStoragePoolName: "test-pool",
				},
			},
		}

		testErr := errors.New("get RSP failed")
		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc).
			WithStatusSubresource(rv, rsc).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if _, ok := obj.(*v1alpha1.ReplicatedStoragePool); ok {
						return testErr
					}
					return cl.Get(ctx, key, obj, opts...)
				},
			}).
			Build()
		rec := NewReconciler(cl, scheme)

		_, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, testErr)).To(BeTrue())
	})

	It("returns error when final patchRVStatus fails", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1")
		rsp := newTestRSP("test-pool")

		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "rv-1",
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
				Labels:     map[string]string{v1alpha1.ReplicatedStorageClassLabelKey: "rsc-1"},
			},
			Spec: v1alpha1.ReplicatedVolumeSpec{
				Size:                       resource.MustParse("10Gi"),
				ReplicatedStorageClassName: "rsc-1",
			},
			// Configuration nil → will be initialized, causing a change → patchRVStatus needed.
		}

		testErr := errors.New("status patch failed")
		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp).
			WithStatusSubresource(rv, rsc).
			WithInterceptorFuncs(interceptor.Funcs{
				SubResourcePatch: func(ctx context.Context, cl client.Client, _ string, obj client.Object, patch client.Patch, _ ...client.SubResourcePatchOption) error {
					if _, ok := obj.(*v1alpha1.ReplicatedVolume); ok {
						return testErr
					}
					return cl.Status().Patch(ctx, obj, patch)
				},
			}).
			Build()
		rec := NewReconciler(cl, scheme)

		_, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, testErr)).To(BeTrue())
	})

	It("skips formation when configuration is nil", func(ctx SpecContext) {
		// RSC exists but has no configuration → RV configuration stays nil → formation skipped.
		rsc := &v1alpha1.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
			Status:     v1alpha1.ReplicatedStorageClassStatus{},
		}

		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "rv-1",
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
				Labels:     map[string]string{v1alpha1.ReplicatedStorageClassLabelKey: "rsc-1"},
			},
			Spec: v1alpha1.ReplicatedVolumeSpec{
				Size:                       resource.MustParse("10Gi"),
				ReplicatedStorageClassName: "rsc-1",
			},
		}

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc).
			WithStatusSubresource(rv, rsc).
			Build()
		rec := NewReconciler(cl, scheme)

		result, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())
		Expect(result).ToNot(Requeue())

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
		// Configuration should remain nil (RSC has no config).
		Expect(updated.Status.Configuration).To(BeNil())
		// ConfigurationReady condition should be set to WaitingForStorageClass.
		cond := obju.GetStatusCondition(&updated, v1alpha1.ReplicatedVolumeCondConfigurationReadyType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonWaitingForStorageClass))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Root Reconcile: RV deletion with attach state
//

var _ = Describe("Root Reconcile deletion with attach state", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	})

	makeDeletingRV := func() *v1alpha1.ReplicatedVolume {
		return &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "rv-1",
				DeletionTimestamp: ptr.To(metav1.Now()),
				Finalizers:        []string{v1alpha1.RVControllerFinalizer},
				Labels:            map[string]string{v1alpha1.ReplicatedStorageClassLabelKey: "rsc-1"},
			},
			Spec: v1alpha1.ReplicatedVolumeSpec{
				Size:                       resource.MustParse("10Gi"),
				ReplicatedStorageClassName: "rsc-1",
			},
		}
	}

	It("does NOT enter deletion path when member is still attached", func(ctx SpecContext) {
		rv := makeDeletingRV()
		rv.Status.DatameshRevision = 1
		rv.Status.Datamesh.Members = []v1alpha1.DatameshMember{
			{Name: "rv-1-0", NodeName: "node-1", Attached: true},
		}

		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "rv-1-0",
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1",
				Type:                 v1alpha1.ReplicaTypeDiskful,
			},
		}

		cl := newClientBuilder(scheme).
			WithObjects(rv, rvr).
			WithStatusSubresource(rv, rvr).
			Build()
		rec := NewReconciler(cl, scheme)

		result, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())
		Expect(result).ToNot(Requeue())

		// RVR should NOT be deleted (still attached).
		var updatedRVR v1alpha1.ReplicatedVolumeReplica
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), &updatedRVR)).To(Succeed())
		Expect(updatedRVR.DeletionTimestamp).To(BeNil())

		// RV finalizer should still be present.
		var updatedRV v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updatedRV)).To(Succeed())
		Expect(updatedRV.Finalizers).To(ContainElement(v1alpha1.RVControllerFinalizer))
	})

	It("does NOT enter deletion path when Detach transition in progress", func(ctx SpecContext) {
		rv := makeDeletingRV()
		rv.Status.DatameshRevision = 1
		rv.Status.Datamesh.Members = []v1alpha1.DatameshMember{
			{Name: "rv-1-0", NodeName: "node-1", Attached: false},
		}
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{
			{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach, Group: v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment, ReplicaName: "rv-1-0", Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, StartedAt: ptr.To(metav1.Now())}}},
		}

		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "rv-1-0",
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1",
				Type:                 v1alpha1.ReplicaTypeDiskful,
			},
		}

		cl := newClientBuilder(scheme).
			WithObjects(rv, rvr).
			WithStatusSubresource(rv, rvr).
			Build()
		rec := NewReconciler(cl, scheme)

		result, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())
		Expect(result).ToNot(Requeue())

		// RVR should NOT be deleted (detach still in progress).
		var updatedRVR v1alpha1.ReplicatedVolumeReplica
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), &updatedRVR)).To(Succeed())
		Expect(updatedRVR.DeletionTimestamp).To(BeNil())
	})

	It("enters deletion path and cleans up when nothing is attached", func(ctx SpecContext) {
		rv := makeDeletingRV()
		rv.Status.Datamesh.Members = []v1alpha1.DatameshMember{
			{Name: "rv-1-0", NodeName: "node-1", Attached: false},
		}

		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "rv-1-0",
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1",
				Type:                 v1alpha1.ReplicaTypeDiskful,
			},
		}

		cl := newClientBuilder(scheme).
			WithObjects(rv, rvr).
			WithStatusSubresource(rv, rvr).
			Build()
		rec := NewReconciler(cl, scheme)

		result, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())
		Expect(result).ToNot(Requeue())

		// RVR should be deleted.
		var updatedRVR v1alpha1.ReplicatedVolumeReplica
		err = cl.Get(ctx, client.ObjectKeyFromObject(rvr), &updatedRVR)
		Expect(apierrors.IsNotFound(err) || updatedRVR.DeletionTimestamp != nil).To(BeTrue())

		// RV finalizer stays on the first reconcile cycle — reconcileMetadata sees
		// the original rvrs slice (split-client: cache not yet updated).
		// On the next reconcile the RVR would be gone and the finalizer would be removed.
		var updatedRV v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updatedRV)).To(Succeed())
		Expect(updatedRV.Finalizers).To(ContainElement(v1alpha1.RVControllerFinalizer))
	})

	It("removes RVA finalizer during deletion when node is not attached", func(ctx SpecContext) {
		rv := makeDeletingRV()

		rva := &v1alpha1.ReplicatedVolumeAttachment{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "rva-1",
				DeletionTimestamp: ptr.To(metav1.Now()),
				Finalizers:        []string{v1alpha1.RVControllerFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
				ReplicatedVolumeName: "rv-1",
				NodeName:             "node-1",
			},
		}

		cl := newClientBuilder(scheme).
			WithObjects(rv, rva).
			WithStatusSubresource(rv, rva).
			Build()
		rec := NewReconciler(cl, scheme)

		result, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())
		Expect(result).ToNot(Requeue())

		// RVA should be finalized (no attached member → finalizer removed → object deleted).
		var updatedRVA v1alpha1.ReplicatedVolumeAttachment
		err = cl.Get(ctx, client.ObjectKeyFromObject(rva), &updatedRVA)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// RVR finalizer helpers tests
//

var _ = Describe("isRVRMemberOrLeavingDatamesh", func() {
	It("returns false when rv is nil", func() {
		Expect(isRVRMemberOrLeavingDatamesh(nil, "rv-1-0")).To(BeFalse())
	})

	It("returns false when no members and no transitions", func() {
		rv := &v1alpha1.ReplicatedVolume{}
		Expect(isRVRMemberOrLeavingDatamesh(rv, "rv-1-0")).To(BeFalse())
	})

	It("returns true when RVR is a datamesh member", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-0", NodeName: "node-1"},
					},
				},
			},
		}
		Expect(isRVRMemberOrLeavingDatamesh(rv, "rv-1-0")).To(BeTrue())
	})

	It("returns false when a different RVR is a member", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-1", NodeName: "node-2"},
					},
				},
			},
		}
		Expect(isRVRMemberOrLeavingDatamesh(rv, "rv-1-0")).To(BeFalse())
	})

	It("returns true when RemoveReplica transition exists for the RVR", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					makeDatameshSingleStepTransition(
						v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveReplica,
						v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership,
						"rv-1-0", v1alpha1.ReplicaTypeAccess,
						"", 0,
					),
				},
			},
		}
		Expect(isRVRMemberOrLeavingDatamesh(rv, "rv-1-0")).To(BeTrue())
	})

	It("returns false when RemoveReplica transition is for a different RVR", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					makeDatameshSingleStepTransition(
						v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveReplica,
						v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership,
						"rv-1-1", v1alpha1.ReplicaTypeAccess,
						"", 0,
					),
				},
			},
		}
		Expect(isRVRMemberOrLeavingDatamesh(rv, "rv-1-0")).To(BeFalse())
	})

	It("ignores non-RemoveReplica transitions", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					makeDatameshSingleStepTransition(
						v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica,
						v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership,
						"rv-1-0", v1alpha1.ReplicaTypeAccess,
						"", 0,
					),
				},
			},
		}
		Expect(isRVRMemberOrLeavingDatamesh(rv, "rv-1-0")).To(BeFalse())
	})
})

var _ = Describe("reconcileRVRFinalizers", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	})

	makeRVR := func(name, rvName string) *v1alpha1.ReplicatedVolumeReplica {
		return &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: rvName,
				Type:                 v1alpha1.ReplicaTypeDiskful,
			},
		}
	}

	makeDeletingRVR := func(name, rvName string) *v1alpha1.ReplicatedVolumeReplica {
		rvr := makeRVR(name, rvName)
		rvr.Finalizers = []string{v1alpha1.RVControllerFinalizer}
		rvr.DeletionTimestamp = ptr.To(metav1.Now())
		return rvr
	}

	It("adds finalizer to non-deleting RVR", func(ctx SpecContext) {
		rvr := makeRVR("rv-1-0", "rv-1")
		cl := newClientBuilder(scheme).WithObjects(rvr).Build()
		rec := NewReconciler(cl, scheme)

		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}
		outcome := rec.reconcileRVRFinalizers(ctx, &v1alpha1.ReplicatedVolume{}, rvrs)
		Expect(outcome.Error()).NotTo(HaveOccurred())

		var updated v1alpha1.ReplicatedVolumeReplica
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), &updated)).To(Succeed())
		Expect(updated.Finalizers).To(ContainElement(v1alpha1.RVControllerFinalizer))
	})

	It("skips non-deleting RVR that already has finalizer", func(ctx SpecContext) {
		rvr := makeRVR("rv-1-0", "rv-1")
		rvr.Finalizers = []string{v1alpha1.RVControllerFinalizer}
		cl := newClientBuilder(scheme).WithObjects(rvr).Build()
		rec := NewReconciler(cl, scheme)

		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}
		outcome := rec.reconcileRVRFinalizers(ctx, &v1alpha1.ReplicatedVolume{}, rvrs)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeFalse())
	})

	It("removes finalizer from deleting RVR when rv is nil", func(ctx SpecContext) {
		rvr := makeDeletingRVR("rv-1-0", "rv-1")
		cl := newClientBuilder(scheme).WithObjects(rvr).Build()
		rec := NewReconciler(cl, scheme)

		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}
		outcome := rec.reconcileRVRFinalizers(ctx, nil, rvrs)
		Expect(outcome.Error()).NotTo(HaveOccurred())

		// After removing the last finalizer, the fake client finalizes the object (deletes it).
		var updated v1alpha1.ReplicatedVolumeReplica
		err := cl.Get(ctx, client.ObjectKeyFromObject(rvr), &updated)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	It("keeps finalizer on deleting RVR that is a datamesh member", func(ctx SpecContext) {
		rvr := makeDeletingRVR("rv-1-0", "rv-1")
		cl := newClientBuilder(scheme).WithObjects(rvr).Build()
		rec := NewReconciler(cl, scheme)

		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-0", NodeName: "node-1"},
					},
				},
			},
		}

		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}
		outcome := rec.reconcileRVRFinalizers(ctx, rv, rvrs)
		Expect(outcome.Error()).NotTo(HaveOccurred())

		var updated v1alpha1.ReplicatedVolumeReplica
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), &updated)).To(Succeed())
		Expect(updated.Finalizers).To(ContainElement(v1alpha1.RVControllerFinalizer))
	})

	It("keeps finalizer on deleting RVR when RemoveReplica transition in progress", func(ctx SpecContext) {
		rvr := makeDeletingRVR("rv-1-0", "rv-1")
		cl := newClientBuilder(scheme).WithObjects(rvr).Build()
		rec := NewReconciler(cl, scheme)

		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveReplica, Group: v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership, ReplicaName: "rv-1-0", Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, StartedAt: ptr.To(metav1.Now())}}},
				},
			},
		}

		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}
		outcome := rec.reconcileRVRFinalizers(ctx, rv, rvrs)
		Expect(outcome.Error()).NotTo(HaveOccurred())

		var updated v1alpha1.ReplicatedVolumeReplica
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), &updated)).To(Succeed())
		Expect(updated.Finalizers).To(ContainElement(v1alpha1.RVControllerFinalizer))
	})

	It("removes finalizer from deleting RVR that is not a member and has no transition", func(ctx SpecContext) {
		rvr := makeDeletingRVR("rv-1-0", "rv-1")
		cl := newClientBuilder(scheme).WithObjects(rvr).Build()
		rec := NewReconciler(cl, scheme)

		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-1", NodeName: "node-2"}, // different member
					},
				},
			},
		}

		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}
		outcome := rec.reconcileRVRFinalizers(ctx, rv, rvrs)
		Expect(outcome.Error()).NotTo(HaveOccurred())

		// Deleting RVR finalized.
		var updated v1alpha1.ReplicatedVolumeReplica
		err := cl.Get(ctx, client.ObjectKeyFromObject(rvr), &updated)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// reconcileOrphanedRVRs
//

var _ = Describe("reconcileOrphanedRVRs", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	})

	It("removes finalizer from deleting orphaned RVR", func(ctx SpecContext) {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "rvr-1",
				DeletionTimestamp: ptr.To(metav1.Now()),
				Finalizers:        []string{v1alpha1.RVControllerFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1",
			},
		}

		cl := newClientBuilder(scheme).
			WithObjects(rvr).
			Build()
		rec := NewReconciler(cl, scheme)

		outcome := rec.reconcileOrphanedRVRs(ctx, "rv-1")
		Expect(outcome.Error()).NotTo(HaveOccurred())

		// RVR should be finalized (finalizer removed → object deleted by fake client).
		var updated v1alpha1.ReplicatedVolumeReplica
		err := cl.Get(ctx, client.ObjectKeyFromObject(rvr), &updated)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	It("adds finalizer to non-deleting orphaned RVR", func(ctx SpecContext) {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name: "rvr-1",
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1",
			},
		}

		cl := newClientBuilder(scheme).
			WithObjects(rvr).
			Build()
		rec := NewReconciler(cl, scheme)

		outcome := rec.reconcileOrphanedRVRs(ctx, "rv-1")
		Expect(outcome.Error()).NotTo(HaveOccurred())

		var updated v1alpha1.ReplicatedVolumeReplica
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), &updated)).To(Succeed())
		Expect(obju.HasFinalizer(&updated, v1alpha1.RVControllerFinalizer)).To(BeTrue())
	})

	It("returns Done when no RVRs exist for the RV", func(ctx SpecContext) {
		cl := newClientBuilder(scheme).Build()
		rec := NewReconciler(cl, scheme)

		outcome := rec.reconcileOrphanedRVRs(ctx, "rv-1")
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.ShouldReturn()).To(BeTrue()) // Done is terminal.
	})

	It("skips deleting RVR without finalizer", func(ctx SpecContext) {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "rvr-1",
				DeletionTimestamp: ptr.To(metav1.Now()),
				Finalizers:        []string{"some-other-finalizer"},
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1",
			},
		}

		cl := newClientBuilder(scheme).
			WithObjects(rvr).
			Build()
		rec := NewReconciler(cl, scheme)

		outcome := rec.reconcileOrphanedRVRs(ctx, "rv-1")
		Expect(outcome.Error()).NotTo(HaveOccurred())

		// RVR should still exist (only our finalizer is managed).
		var updated v1alpha1.ReplicatedVolumeReplica
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), &updated)).To(Succeed())
		Expect(obju.HasFinalizer(&updated, v1alpha1.RVControllerFinalizer)).To(BeFalse())
	})
})

var _ = Describe("applyDatameshTransitionStepMessage", func() {
	It("sets message and returns true when different", func() {
		step := &v1alpha1.ReplicatedVolumeDatameshTransitionStep{Message: "old"}
		Expect(applyDatameshTransitionStepMessage(step, "new")).To(BeTrue())
		Expect(step.Message).To(Equal("new"))
	})

	It("returns false when message is the same", func() {
		step := &v1alpha1.ReplicatedVolumeDatameshTransitionStep{Message: "same"}
		Expect(applyDatameshTransitionStepMessage(step, "same")).To(BeFalse())
	})

	It("returns false when step is nil", func() {
		Expect(applyDatameshTransitionStepMessage(nil, "msg")).To(BeFalse())
	})
})

// layoutMember builds a minimal datamesh member of the given type.
func layoutMember(t v1alpha1.DatameshMemberType) v1alpha1.DatameshMember {
	return v1alpha1.DatameshMember{Type: t}
}

// layoutMembers builds a members slice: n diskful voters plus tb tie-breakers, each with a
// unique name (rvr-N) and node (node-N) so layout convergence can match them to RVRs and select
// a deterministic retype candidate.
func layoutMembers(diskful, tb int) []v1alpha1.DatameshMember {
	members := make([]v1alpha1.DatameshMember, 0, diskful+tb)
	idx := 0
	add := func(t v1alpha1.DatameshMemberType) {
		members = append(members, v1alpha1.DatameshMember{
			Name:     fmt.Sprintf("rvr-%d", idx),
			NodeName: fmt.Sprintf("node-%d", idx),
			Type:     t,
		})
		idx++
	}
	for range diskful {
		add(v1alpha1.DatameshMemberTypeDiskful)
	}
	for range tb {
		add(v1alpha1.DatameshMemberTypeTieBreaker)
	}
	return members
}

// layoutRVRs builds RVRs matching the given datamesh members (same name and node, spec.type
// derived directly from the member type — Diskful/TieBreaker), so layout convergence sees a
// consistent intent for each member.
func layoutRVRs(members []v1alpha1.DatameshMember) []*v1alpha1.ReplicatedVolumeReplica {
	rvrs := make([]*v1alpha1.ReplicatedVolumeReplica, 0, len(members))
	for _, m := range members {
		rvrs = append(rvrs, &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: m.Name},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				Type:     v1alpha1.ReplicaType(m.Type),
				NodeName: m.NodeName,
			},
		})
	}
	return rvrs
}

// layoutReportOf computes the layout report for rv, deriving matching RVRs from its members and
// assuming no attachments. Used by computeLayoutReport tests where RVR intent mirrors the members.
func layoutReportOf(rv *v1alpha1.ReplicatedVolume) layoutReport {
	return computeLayoutReport(rv, layoutRVRs(rv.Status.Datamesh.Members), nil)
}

// layoutRV builds an RV with the given intended config (FTT/GMDR), actual members and,
// optionally, an active layout-changing transition (ChangeReplicaType Diskful→TieBreaker).
func layoutRV(ftt, gmdr byte, members []v1alpha1.DatameshMember, activeLayoutChangingTransition bool) *v1alpha1.ReplicatedVolume {
	var transitions []v1alpha1.ReplicatedVolumeDatameshTransition
	if activeLayoutChangingTransition {
		transitions = []v1alpha1.ReplicatedVolumeDatameshTransition{{
			Type:            v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeReplicaType,
			FromReplicaType: v1alpha1.ReplicaTypeDiskful,
			ToReplicaType:   v1alpha1.ReplicaTypeTieBreaker,
		}}
	}
	return &v1alpha1.ReplicatedVolume{
		Status: v1alpha1.ReplicatedVolumeStatus{
			Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
				FailuresToTolerate: ftt, GuaranteedMinimumDataRedundancy: gmdr,
			},
			Datamesh:            v1alpha1.ReplicatedVolumeDatamesh{Members: members},
			DatameshTransitions: transitions,
		},
	}
}

var _ = Describe("computeActualLayout", func() {
	It("counts Diskful and LiminalDiskful as diskful voters", func() {
		rv := &v1alpha1.ReplicatedVolume{Status: v1alpha1.ReplicatedVolumeStatus{
			Datamesh: v1alpha1.ReplicatedVolumeDatamesh{Members: []v1alpha1.DatameshMember{
				layoutMember(v1alpha1.DatameshMemberTypeDiskful),
				layoutMember(v1alpha1.DatameshMemberTypeLiminalDiskful),
			}},
		}}
		d, tb := computeActualLayout(rv)
		Expect(d).To(Equal(2))
		Expect(tb).To(Equal(0))
	})

	It("counts TieBreaker members as tie-breakers", func() {
		rv := &v1alpha1.ReplicatedVolume{Status: v1alpha1.ReplicatedVolumeStatus{
			Datamesh: v1alpha1.ReplicatedVolumeDatamesh{Members: layoutMembers(2, 1)},
		}}
		d, tb := computeActualLayout(rv)
		Expect(d).To(Equal(2))
		Expect(tb).To(Equal(1))
	})

	It("ignores Access and ShadowDiskful members", func() {
		rv := &v1alpha1.ReplicatedVolume{Status: v1alpha1.ReplicatedVolumeStatus{
			Datamesh: v1alpha1.ReplicatedVolumeDatamesh{Members: []v1alpha1.DatameshMember{
				layoutMember(v1alpha1.DatameshMemberTypeDiskful),
				layoutMember(v1alpha1.DatameshMemberTypeAccess),
				layoutMember(v1alpha1.DatameshMemberTypeShadowDiskful),
				layoutMember(v1alpha1.DatameshMemberTypeLiminalShadowDiskful),
			}},
		}}
		d, tb := computeActualLayout(rv)
		Expect(d).To(Equal(1))
		Expect(tb).To(Equal(0))
	})
})

var _ = Describe("formatLayout", func() {
	It("omits the tie-breaker suffix when there are none", func() {
		Expect(formatLayout(3, 0)).To(Equal("3D"))
		Expect(formatLayout(1, 0)).To(Equal("1D"))
	})

	It("includes the tie-breaker count when present", func() {
		Expect(formatLayout(2, 1)).To(Equal("2D+1TB"))
		Expect(formatLayout(4, 1)).To(Equal("4D+1TB"))
	})
})

var _ = Describe("computeLayoutReport", func() {
	It("reports Converged when actual matches intended (r2 = 2D+1TB)", func() {
		report := layoutReportOf(layoutRV(1, 0, layoutMembers(2, 1), false))
		Expect(report.layout).To(Equal(ptr.To("2D+1TB")))
		Expect(report.converged.status).To(Equal(metav1.ConditionTrue))
		Expect(report.converged.reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverged))
		Expect(report.converged.message).To(Equal("layout converged: 2D+1TB"))
	})

	It("reports Converged when actual matches intended (None = 1D)", func() {
		report := layoutReportOf(layoutRV(0, 0, layoutMembers(1, 0), false))
		Expect(report.layout).To(Equal(ptr.To("1D")))
		Expect(report.converged.status).To(Equal(metav1.ConditionTrue))
		Expect(report.converged.reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverged))
		Expect(report.converged.message).To(Equal("layout converged: 1D"))
	})

	It("reports Converging while a layout-changing transition is active, even if the layout matches", func() {
		// Mid-flight D→TB: on the second step the member is already a TieBreaker, so the
		// counted layout momentarily equals the intended one while the transition is still
		// running. Reporting Converged there would flip the condition to True and back.
		report := layoutReportOf(layoutRV(1, 1, layoutMembers(3, 0), true))
		Expect(report.layout).To(Equal(ptr.To("3D")))
		Expect(report.converged.status).To(Equal(metav1.ConditionFalse))
		Expect(report.converged.reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverging))
	})

	It("reports Converged when the active transition does not change the layout composition", func() {
		// AddReplica(Access) is a membership transition, but Access members are not part
		// of the layout — treating it as convergence progress would make the condition
		// flap on unrelated activity.
		rv := layoutRV(1, 1, layoutMembers(3, 0), false)
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica,
			ReplicaType: v1alpha1.ReplicaTypeAccess,
		}}
		report := layoutReportOf(rv)
		Expect(report.converged.status).To(Equal(metav1.ConditionTrue))
		Expect(report.converged.reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverged))
	})

	It("reports Converging while a ForceRemoveReplica(Diskful) is active", func() {
		// ForceRemove lives in the Emergency group, so classification must go by the
		// record's fields, not by Group.
		rv := layoutRV(1, 1, layoutMembers(3, 0), false)
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeForceRemoveReplica,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupEmergency,
			ReplicaType: v1alpha1.ReplicaTypeDiskful,
		}}
		report := layoutReportOf(rv)
		Expect(report.converged.status).To(Equal(metav1.ConditionFalse))
		Expect(report.converged.reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverging))
	})

	It("reports Converging (P1 retype) for 3D at an r2 config (whitelist)", func() {
		// Block 2: 3D at an r2 config is now a P1 retype candidate (was TransitionUnsupported
		// in block 1). With three unattached Diskful RVRs the lexicographically last is chosen.
		report := layoutReportOf(layoutRV(1, 0, layoutMembers(3, 0), false))
		Expect(report.layout).To(Equal(ptr.To("3D")))
		Expect(report.converged.status).To(Equal(metav1.ConditionFalse))
		Expect(report.converged.reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverging))
		Expect(report.converged.message).To(Equal("retyping Diskful replica rvr-2 to tie-breaker: have 3D, want 2D+1TB"))
	})

	It("reports Converging (P2 heal) for 2D at an r2 config (tie-breaker deficit)", func() {
		// Block 2: 2D at an r2 config is now a P2 heal (create the missing tie-breaker).
		report := layoutReportOf(layoutRV(1, 0, layoutMembers(2, 0), false))
		Expect(report.layout).To(Equal(ptr.To("2D")))
		Expect(report.converged.status).To(Equal(metav1.ConditionFalse))
		Expect(report.converged.reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverging))
		Expect(report.converged.message).To(Equal("creating tie-breaker replica: have 2D, want 2D+1TB"))
	})

	It("reports TransitionUnsupported when diskful count is insufficient (2D at an r3 config)", func() {
		report := layoutReportOf(layoutRV(1, 1, layoutMembers(2, 0), false))
		Expect(report.layout).To(Equal(ptr.To("2D")))
		Expect(report.converged.status).To(Equal(metav1.ConditionFalse))
		Expect(report.converged.reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonTransitionUnsupported))
		Expect(report.converged.message).To(Equal(
			"layout mismatch: have 2D, want 3D; automatic transition is not supported, manual intervention required"))
	})

	It("reports Converging on a mismatch while a membership transition is active", func() {
		report := layoutReportOf(layoutRV(1, 0, layoutMembers(3, 0), true))
		Expect(report.layout).To(Equal(ptr.To("3D")))
		Expect(report.converged.status).To(Equal(metav1.ConditionFalse))
		Expect(report.converged.reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverging))
		Expect(report.converged.message).To(Equal("layout transition in progress: have 3D, want 2D+1TB"))
	})

	It("still converges (P1) when only non-membership transitions are active", func() {
		// Block 2: Attach/Resize do not gate the whitelist (only membership transitions do), so a
		// 3D volume at an r2 config with unattached candidates still plans a retype.
		rv := layoutRV(1, 0, layoutMembers(3, 0), false) // r2 config, actual 3D
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{
			{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach},
			{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeResizeVolume},
		}

		report := layoutReportOf(rv)
		Expect(report.converged.status).To(Equal(metav1.ConditionFalse))
		Expect(report.converged.reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverging))
		Expect(report.converged.message).To(Equal("retyping Diskful replica rvr-2 to tie-breaker: have 3D, want 2D+1TB"))
	})
})

var _ = Describe("hasLayoutChangingTransition", func() {
	mkRV := func(transitions ...v1alpha1.ReplicatedVolumeDatameshTransition) *v1alpha1.ReplicatedVolume {
		return &v1alpha1.ReplicatedVolume{Status: v1alpha1.ReplicatedVolumeStatus{DatameshTransitions: transitions}}
	}
	addRemove := func(
		t v1alpha1.ReplicatedVolumeDatameshTransitionType, rt v1alpha1.ReplicaType,
	) v1alpha1.ReplicatedVolumeDatameshTransition {
		return v1alpha1.ReplicatedVolumeDatameshTransition{Type: t, ReplicaType: rt}
	}
	changeType := func(from, to v1alpha1.ReplicaType) v1alpha1.ReplicatedVolumeDatameshTransition {
		return v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:            v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeReplicaType,
			FromReplicaType: from, ToReplicaType: to,
		}
	}

	It("returns false when there are no transitions", func() {
		Expect(hasLayoutChangingTransition(mkRV())).To(BeFalse())
	})

	It("returns true for Add/Remove/ForceRemove of a Diskful or TieBreaker replica", func() {
		for _, tt := range []v1alpha1.ReplicatedVolumeDatameshTransitionType{
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica,
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveReplica,
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeForceRemoveReplica,
		} {
			for _, rt := range []v1alpha1.ReplicaType{v1alpha1.ReplicaTypeDiskful, v1alpha1.ReplicaTypeTieBreaker} {
				Expect(hasLayoutChangingTransition(mkRV(addRemove(tt, rt)))).To(BeTrue(), "%s/%s", tt, rt)
			}
		}
	})

	It("classifies ForceRemoveReplica by its fields, not by its Emergency group", func() {
		t := addRemove(v1alpha1.ReplicatedVolumeDatameshTransitionTypeForceRemoveReplica, v1alpha1.ReplicaTypeDiskful)
		t.Group = v1alpha1.ReplicatedVolumeDatameshTransitionGroupEmergency
		Expect(hasLayoutChangingTransition(mkRV(t))).To(BeTrue())
	})

	It("returns false for Add/Remove of replicas outside the layout (Access, ShadowDiskful)", func() {
		for _, tt := range []v1alpha1.ReplicatedVolumeDatameshTransitionType{
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica,
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveReplica,
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeForceRemoveReplica,
		} {
			for _, rt := range []v1alpha1.ReplicaType{v1alpha1.ReplicaTypeAccess, v1alpha1.ReplicaTypeShadowDiskful} {
				Expect(hasLayoutChangingTransition(mkRV(addRemove(tt, rt)))).To(BeFalse(), "%s/%s", tt, rt)
			}
		}
	})

	It("returns true for ChangeReplicaType touching Diskful or TieBreaker on either end", func() {
		for _, tr := range []v1alpha1.ReplicatedVolumeDatameshTransition{
			changeType(v1alpha1.ReplicaTypeDiskful, v1alpha1.ReplicaTypeTieBreaker),
			changeType(v1alpha1.ReplicaTypeTieBreaker, v1alpha1.ReplicaTypeDiskful),
			changeType(v1alpha1.ReplicaTypeDiskful, v1alpha1.ReplicaTypeShadowDiskful),
			changeType(v1alpha1.ReplicaTypeAccess, v1alpha1.ReplicaTypeTieBreaker),
		} {
			Expect(hasLayoutChangingTransition(mkRV(tr))).To(BeTrue(),
				"%s→%s", tr.FromReplicaType, tr.ToReplicaType)
		}
	})

	It("returns false for ChangeReplicaType between replica types outside the layout", func() {
		Expect(hasLayoutChangingTransition(mkRV(
			changeType(v1alpha1.ReplicaTypeAccess, v1alpha1.ReplicaTypeShadowDiskful)))).To(BeFalse())
		Expect(hasLayoutChangingTransition(mkRV(
			changeType(v1alpha1.ReplicaTypeShadowDiskful, v1alpha1.ReplicaTypeAccess)))).To(BeFalse())
	})

	It("returns false for transition types that never change the layout", func() {
		for _, t := range []v1alpha1.ReplicatedVolumeDatameshTransitionType{
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach,
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach,
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeForceDetach,
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeResizeVolume,
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeQuorum,
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeSystemNetworks,
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeEnableMultiattach,
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeDisableMultiattach,
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeRepairNetworkAddresses,
		} {
			// Replica type is populated to prove the type check is what rejects them.
			Expect(hasLayoutChangingTransition(mkRV(addRemove(t, v1alpha1.ReplicaTypeDiskful)))).
				To(BeFalse(), string(t))
		}
	})

	It("returns true when a layout-changing transition is present among unrelated ones", func() {
		Expect(hasLayoutChangingTransition(mkRV(
			addRemove(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach, v1alpha1.ReplicaTypeDiskful),
			changeType(v1alpha1.ReplicaTypeDiskful, v1alpha1.ReplicaTypeTieBreaker),
		))).To(BeTrue())
	})
})

var _ = Describe("reconcileLayoutStatus", func() {
	var (
		scheme *runtime.Scheme
		rec    *Reconciler
	)

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		rec = NewReconciler(newClientBuilder(scheme).Build(), scheme)
	})

	It("sets status.layout and the LayoutConverged condition", func(ctx SpecContext) {
		rv := layoutRV(1, 1, layoutMembers(2, 0), false) // r3 config, actual 2D → unsupported (deficit)
		members := rv.Status.Datamesh.Members
		outcome := rec.reconcileLayoutStatus(ctx, rv, layoutRVRs(members), nil)

		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rv.Status.Layout).To(Equal(ptr.To("2D")))

		cond := obju.GetStatusCondition(rv, v1alpha1.ReplicatedVolumeCondLayoutConvergedType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonTransitionUnsupported))
	})

	It("does not flap on repeated reconciles (idempotent, no patch churn)", func(ctx SpecContext) {
		rv := layoutRV(1, 0, layoutMembers(2, 1), false) // converged
		rvrs := layoutRVRs(rv.Status.Datamesh.Members)
		Expect(rec.reconcileLayoutStatus(ctx, rv, rvrs, nil).DidChange()).To(BeTrue())

		before := obju.GetStatusCondition(rv, v1alpha1.ReplicatedVolumeCondLayoutConvergedType).DeepCopy()
		outcome := rec.reconcileLayoutStatus(ctx, rv, rvrs, nil)
		Expect(outcome.DidChange()).To(BeFalse())

		after := obju.GetStatusCondition(rv, v1alpha1.ReplicatedVolumeCondLayoutConvergedType)
		Expect(after.LastTransitionTime).To(Equal(before.LastTransitionTime))
		Expect(after.Reason).To(Equal(before.Reason))
		Expect(after.Message).To(Equal(before.Message))
	})

	It("is a no-op when configuration is not yet acknowledged (nil)", func(ctx SpecContext) {
		rv := &v1alpha1.ReplicatedVolume{}
		outcome := rec.reconcileLayoutStatus(ctx, rv, nil, nil)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeFalse())
		Expect(obju.GetStatusCondition(rv, v1alpha1.ReplicatedVolumeCondLayoutConvergedType)).To(BeNil())
		Expect(rv.Status.Layout).To(BeNil(), "status.layout stays unset (nil), never an empty string")
	})

	It("sets LayoutConverged=Unknown/VolumeDeleting on the normal-operation deletion path", func(ctx SpecContext) {
		// A deleting-but-still-attached RV keeps going through normal operation. Leaving a
		// Converging message there would promise an action the deletion path never takes.
		rv := layoutRV(1, 0, layoutMembers(3, 0), false) // r2 config, actual 3D → would be Converging
		now := metav1.Now()
		rv.DeletionTimestamp = &now

		outcome := rec.reconcileLayoutStatus(ctx, rv, layoutRVRs(rv.Status.Datamesh.Members), nil)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.DidChange()).To(BeTrue())

		cond := obju.GetStatusCondition(rv, v1alpha1.ReplicatedVolumeCondLayoutConvergedType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonVolumeDeleting))
		Expect(cond.Message).To(Equal("volume is being deleted; layout convergence suspended"))
	})
})

var _ = Describe("reconcileLayoutStatus wiring", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	})

	It("does not set LayoutConverged while formation is in progress", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1")
		rsp := newTestRSP("test-pool")

		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "rv-1",
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
				Labels:     map[string]string{v1alpha1.ReplicatedStorageClassLabelKey: "rsc-1"},
			},
			Spec: v1alpha1.ReplicatedVolumeSpec{
				Size:                       resource.MustParse("10Gi"),
				ReplicatedStorageClassName: "rsc-1",
			},
			Status: v1alpha1.ReplicatedVolumeStatus{
				ConfigurationGeneration:         1,
				ConfigurationObservedGeneration: 1,
				Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
					Topology: v1alpha1.TopologyIgnored, FailuresToTolerate: 0, GuaranteedMinimumDataRedundancy: 0,
					VolumeAccess: v1alpha1.VolumeAccessLocal, ReplicatedStoragePoolName: "test-pool",
				},
				// DatameshRevision defaults to 0 → formation in progress.
			},
		}

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp).
			WithStatusSubresource(rv, rsc).
			Build()
		rec := NewReconciler(cl, scheme)

		_, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
		Expect(obju.GetStatusCondition(&updated, v1alpha1.ReplicatedVolumeCondLayoutConvergedType)).
			To(BeNil(), "LayoutConverged must not be written during formation")
		Expect(updated.Status.Layout).
			To(BeNil(), "status.layout must stay unset (nil), not an empty string, during formation")
	})
})

var _ = Describe("applyLayout", func() {
	It("publishes the layout and is idempotent", func() {
		rv := &v1alpha1.ReplicatedVolume{}

		Expect(applyLayout(rv, ptr.To("2D+1TB"))).To(BeTrue())
		Expect(rv.Status.Layout).To(Equal(ptr.To("2D+1TB")))
		Expect(applyLayout(rv, ptr.To("2D+1TB"))).To(BeFalse())
		Expect(rv.Status.Layout).To(Equal(ptr.To("2D+1TB")))
	})

	It("does not share the report's pointer with the object", func() {
		rv := &v1alpha1.ReplicatedVolume{}
		reported := ptr.To("3D")

		Expect(applyLayout(rv, reported)).To(BeTrue())
		Expect(rv.Status.Layout).NotTo(BeIdenticalTo(reported))
		Expect(rv.Status.Layout).To(Equal(ptr.To("3D")))
	})

	It("treats a nil layout as unset and clears the field", func() {
		// nil and "" are different states: nil means "not computed yet", and "" is never a
		// valid layout, so clearing must not publish an empty string.
		rv := &v1alpha1.ReplicatedVolume{Status: v1alpha1.ReplicatedVolumeStatus{Layout: ptr.To("3D")}}

		Expect(applyLayout(rv, nil)).To(BeTrue())
		Expect(rv.Status.Layout).To(BeNil())
		Expect(applyLayout(rv, nil)).To(BeFalse())
		Expect(rv.Status.Layout).To(BeNil())
	})

	It("does not treat a stored empty string as already unset", func() {
		// The pointer exists to keep "" and nil distinguishable: a stored "" is a value that
		// still has to be cleared, not an alias for "unset".
		rv := &v1alpha1.ReplicatedVolume{Status: v1alpha1.ReplicatedVolumeStatus{Layout: ptr.To("")}}

		Expect(applyLayout(rv, nil)).To(BeTrue())
		Expect(rv.Status.Layout).To(BeNil())
	})
})

// convergenceFixture builds an RV named "rv-1" with the given intended config (Ignored topology,
// FailuresToTolerate fixed at 1, and the given GMDR — gmdr=0 → r2 = 2D+1TB, gmdr=1 → r3 = 3D) and
// a datamesh of `diskful` Diskful members + `tb` TieBreaker members, together with matching RVRs
// (consistent names/nodes, RV controller finalizer, spec.type equal to the member type). It is the
// steady-state starting point for convergence tests.
func convergenceFixture(gmdr byte, diskful, tb int) (*v1alpha1.ReplicatedVolume, []*v1alpha1.ReplicatedVolumeReplica) {
	rv := &v1alpha1.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "rv-1", UID: "rv-1-uid"},
		Status: v1alpha1.ReplicatedVolumeStatus{
			Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
				Topology:                        v1alpha1.TopologyIgnored,
				FailuresToTolerate:              1,
				GuaranteedMinimumDataRedundancy: gmdr,
				ReplicatedStoragePoolName:       "test-pool",
			},
		},
	}
	var members []v1alpha1.DatameshMember
	var rvrs []*v1alpha1.ReplicatedVolumeReplica
	i := 0
	add := func(mt v1alpha1.DatameshMemberType, rt v1alpha1.ReplicaType) {
		name := v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", uint8(i))
		node := fmt.Sprintf("node-%d", i)
		members = append(members, v1alpha1.DatameshMember{Name: name, NodeName: node, Type: mt})
		rvrs = append(rvrs, &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: name, Finalizers: []string{v1alpha1.RVControllerFinalizer}},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1", Type: rt, NodeName: node,
			},
		})
		i++
	}
	for range diskful {
		add(v1alpha1.DatameshMemberTypeDiskful, v1alpha1.ReplicaTypeDiskful)
	}
	for range tb {
		add(v1alpha1.DatameshMemberTypeTieBreaker, v1alpha1.ReplicaTypeTieBreaker)
	}
	rv.Status.Datamesh.Members = members
	return rv, rvrs
}

var _ = Describe("computeTargetLayoutAction", func() {
	It("takes no action when converged (2D+1TB at r2)", func() {
		rv, rvrs := convergenceFixture(0, 2, 1)
		action, report := computeTargetLayoutAction(rv, rvrs, nil)
		Expect(action.kind).To(Equal(layoutActionNone))
		Expect(report.status).To(Equal(metav1.ConditionTrue))
		Expect(report.reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverged))
	})

	It("P1: plans a retype for 3D at an r2 config, picking the lexicographically last RVR", func() {
		rv, rvrs := convergenceFixture(0, 3, 0)
		action, report := computeTargetLayoutAction(rv, rvrs, nil)
		Expect(action.kind).To(Equal(layoutActionRetypeToTieBreaker))
		Expect(action.retypeRVRName).To(Equal(v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 2)))
		Expect(report.status).To(Equal(metav1.ConditionFalse))
		Expect(report.reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverging))
	})

	It("P1: is idempotent while a retype is already requested (spec flipped, no transition yet)", func() {
		rv, rvrs := convergenceFixture(0, 3, 0)
		// Simulate a previous pass: one Diskful member's RVR already retyped to TieBreaker,
		// but the member type has not changed yet and no membership transition exists.
		rvrs[2].Spec.Type = v1alpha1.ReplicaTypeTieBreaker

		action, report := computeTargetLayoutAction(rv, rvrs, nil)
		Expect(action.kind).To(Equal(layoutActionNone))
		Expect(report.reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverging))
		Expect(report.message).To(Equal("retype to tie-breaker requested: have 3D, want 2D+1TB"))
	})

	It("P1: is a no-op while a membership transition is active", func() {
		rv, rvrs := convergenceFixture(0, 3, 0)
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{
			{
				Type:            v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeReplicaType,
				FromReplicaType: v1alpha1.ReplicaTypeDiskful,
				ToReplicaType:   v1alpha1.ReplicaTypeTieBreaker,
			},
		}
		action, report := computeTargetLayoutAction(rv, rvrs, nil)
		Expect(action.kind).To(Equal(layoutActionNone))
		Expect(report.reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverging))
		Expect(report.message).To(Equal("layout transition in progress: have 3D, want 2D+1TB"))
	})

	It("P1: excludes attached replicas (member.Attached) from candidates", func() {
		rv, rvrs := convergenceFixture(0, 3, 0)
		// The lexicographically last diskful member is attached → the next one is chosen.
		rv.Status.Datamesh.Members[2].Attached = true
		action, _ := computeTargetLayoutAction(rv, rvrs, nil)
		Expect(action.kind).To(Equal(layoutActionRetypeToTieBreaker))
		Expect(action.retypeRVRName).To(Equal(v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 1)))
	})

	It("P1: excludes replicas with an active RVA on their node", func() {
		rv, rvrs := convergenceFixture(0, 3, 0)
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{{
			Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{ReplicatedVolumeName: "rv-1", NodeName: "node-2"},
		}}
		action, _ := computeTargetLayoutAction(rv, rvrs, rvas)
		Expect(action.kind).To(Equal(layoutActionRetypeToTieBreaker))
		Expect(action.retypeRVRName).To(Equal(v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 1)))
	})

	It("P1: reports CannotConverge when all diskful replicas are attached", func() {
		rv, rvrs := convergenceFixture(0, 3, 0)
		for i := range rv.Status.Datamesh.Members {
			rv.Status.Datamesh.Members[i].Attached = true
		}
		action, report := computeTargetLayoutAction(rv, rvrs, nil)
		Expect(action.kind).To(Equal(layoutActionNone))
		Expect(report.reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonCannotConverge))
		Expect(report.message).To(ContainSubstring("all diskful replicas are attached"))
	})

	It("P1: excludes TransZonal candidates whose zone holds more than one diskful voter", func() {
		// 4D TransZonal at an r2 config: zone-b and zone-c hold one voter each, zone-a holds two.
		// Only the single-voter zones may host a tie-breaker, so the lexicographically last
		// admissible replica (rv-1-1, zone-c) wins over the later-named zone-a replicas.
		rv, rvrs := convergenceFixture(0, 4, 0)
		rv.Status.Configuration.Topology = v1alpha1.TopologyTransZonal
		rv.Status.Datamesh.Members[0].Zone = "zone-b"
		rv.Status.Datamesh.Members[1].Zone = "zone-c"
		rv.Status.Datamesh.Members[2].Zone = "zone-a"
		rv.Status.Datamesh.Members[3].Zone = "zone-a"

		action, _ := computeTargetLayoutAction(rv, rvrs, nil)
		Expect(action.kind).To(Equal(layoutActionRetypeToTieBreaker))
		Expect(action.retypeRVRName).To(Equal(v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 1)))
	})

	It("P1: reports CannotConverge when no zone can host a tie-breaker", func() {
		// 3D TransZonal, all diskful voters share one zone → no admissible candidate.
		rv, rvrs := convergenceFixture(0, 3, 0)
		rv.Status.Configuration.Topology = v1alpha1.TopologyTransZonal
		for i := range rv.Status.Datamesh.Members {
			rv.Status.Datamesh.Members[i].Zone = "zone-a"
		}
		action, report := computeTargetLayoutAction(rv, rvrs, nil)
		Expect(action.kind).To(Equal(layoutActionNone))
		Expect(report.reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonCannotConverge))
		Expect(report.message).To(ContainSubstring("without violating zone placement"))
	})

	It("P1: excludes Zonal candidates outside the primary (max voter count) zone", func() {
		// Degenerate Zonal layout (e.g. after manual ops/adopt): two voters in zone-a, one in
		// zone-b. A Zonal tie-breaker gain must land in the primary zone (guardZonalSameZone), so
		// the minority-zone replica is skipped even though it is the lexicographically last name.
		rv, rvrs := convergenceFixture(0, 3, 0)
		rv.Status.Configuration.Topology = v1alpha1.TopologyZonal
		rv.Status.Datamesh.Members[0].Zone = "zone-a" // primary zone (2 voters)
		rv.Status.Datamesh.Members[1].Zone = "zone-a"
		rv.Status.Datamesh.Members[2].Zone = "zone-b" // minority zone (1 voter)

		action, _ := computeTargetLayoutAction(rv, rvrs, nil)
		Expect(action.kind).To(Equal(layoutActionRetypeToTieBreaker))
		Expect(action.retypeRVRName).To(Equal(v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 1)))
	})

	It("P1: reports CannotConverge when only a minority-zone Zonal candidate remains", func() {
		// Both primary-zone (zone-a) replicas are attached; the only free replica is in the
		// minority zone (zone-b), which a Zonal tie-breaker may not join → no admissible candidate.
		rv, rvrs := convergenceFixture(0, 3, 0)
		rv.Status.Configuration.Topology = v1alpha1.TopologyZonal
		rv.Status.Datamesh.Members[0].Zone = "zone-a"
		rv.Status.Datamesh.Members[0].Attached = true
		rv.Status.Datamesh.Members[1].Zone = "zone-a"
		rv.Status.Datamesh.Members[1].Attached = true
		rv.Status.Datamesh.Members[2].Zone = "zone-b"

		action, report := computeTargetLayoutAction(rv, rvrs, nil)
		Expect(action.kind).To(Equal(layoutActionNone))
		Expect(report.reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonCannotConverge))
		Expect(report.message).To(ContainSubstring("without violating zone placement"))
	})

	It("P2: plans a tie-breaker creation for 2D at an r2 config", func() {
		rv, rvrs := convergenceFixture(0, 2, 0)
		action, report := computeTargetLayoutAction(rv, rvrs, nil)
		Expect(action.kind).To(Equal(layoutActionCreateTieBreaker))
		Expect(report.status).To(Equal(metav1.ConditionFalse))
		Expect(report.reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverging))
	})

	It("P2: is idempotent while a tie-breaker RVR already exists but is not yet a member", func() {
		rv, rvrs := convergenceFixture(0, 2, 0)
		// A TieBreaker RVR was created in a previous pass but has not joined the datamesh yet.
		rvrs = append(rvrs, &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 2)},
			Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{ReplicatedVolumeName: "rv-1", Type: v1alpha1.ReplicaTypeTieBreaker},
		})
		action, report := computeTargetLayoutAction(rv, rvrs, nil)
		Expect(action.kind).To(Equal(layoutActionNone))
		Expect(report.reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverging))
		Expect(report.message).To(Equal("tie-breaker creation pending: have 2D, want 2D+1TB"))
	})

	It("reports TransitionUnsupported outside the whitelist (2D+1TB at an r3 config, upsize)", func() {
		rv, rvrs := convergenceFixture(1, 2, 1) // r3 config wants 3D, actual 2D+1TB
		action, report := computeTargetLayoutAction(rv, rvrs, nil)
		Expect(action.kind).To(Equal(layoutActionNone))
		Expect(report.reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonTransitionUnsupported))
		Expect(report.message).To(Equal(
			"layout mismatch: have 2D+1TB, want 3D; automatic transition is not supported, manual intervention required"))
	})

	It("reports TransitionUnsupported when the diskful count is insufficient (2D at an r3 config)", func() {
		rv, rvrs := convergenceFixture(1, 2, 0)
		action, report := computeTargetLayoutAction(rv, rvrs, nil)
		Expect(action.kind).To(Equal(layoutActionNone))
		Expect(report.reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonTransitionUnsupported))
	})

	// ── №9: the scheduler's verdict on a pending tie-breaker ──────────────────

	// pendingTBRVR appends a TieBreaker RVR with the given ID that is not yet a datamesh
	// member, carrying the given Scheduled condition (status/reason/message) at the given
	// observed generation relative to the RVR generation.
	pendingTBRVR := func(
		rvrs []*v1alpha1.ReplicatedVolumeReplica,
		id uint8,
		cond *metav1.Condition,
	) []*v1alpha1.ReplicatedVolumeReplica {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name:       v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", id),
				Generation: 3,
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1", Type: v1alpha1.ReplicaTypeTieBreaker,
			},
		}
		if cond != nil {
			rvr.Status.Conditions = []metav1.Condition{*cond}
		}
		return append(rvrs, rvr)
	}
	scheduledCond := func(status metav1.ConditionStatus, reason, message string, observedGeneration int64) *metav1.Condition {
		return &metav1.Condition{
			Type: v1alpha1.ReplicatedVolumeReplicaCondScheduledType, Status: status,
			Reason: reason, Message: message, ObservedGeneration: observedGeneration,
		}
	}

	It("P2: reports CannotConverge when the pending tie-breaker has a CURRENT Scheduled=False", func() {
		rv, rvrs := convergenceFixture(0, 2, 0)
		rvrs = pendingTBRVR(rvrs, 2, scheduledCond(metav1.ConditionFalse,
			v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonSchedulingFailed,
			"no eligible node available", 3))

		action, report := computeTargetLayoutAction(rv, rvrs, nil)
		Expect(action.kind).To(Equal(layoutActionNone))
		Expect(report.reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonCannotConverge))
		Expect(report.message).To(ContainSubstring("no eligible node available"))
	})

	It("P2: reports Converging when the pending tie-breaker has no Scheduled condition yet", func() {
		rv, rvrs := convergenceFixture(0, 2, 0)
		rvrs = pendingTBRVR(rvrs, 2, nil)

		action, report := computeTargetLayoutAction(rv, rvrs, nil)
		Expect(action.kind).To(Equal(layoutActionNone))
		Expect(report.reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverging))
		Expect(report.message).To(Equal("tie-breaker creation pending: have 2D, want 2D+1TB"))
	})

	It("P2: reports Converging when Scheduled=False is stale (scheduler has not re-evaluated)", func() {
		rv, rvrs := convergenceFixture(0, 2, 0)
		rvrs = pendingTBRVR(rvrs, 2, scheduledCond(metav1.ConditionFalse,
			v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonSchedulingFailed,
			"no eligible node available", 2)) // generation is 3 → stale

		_, report := computeTargetLayoutAction(rv, rvrs, nil)
		Expect(report.reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverging))
	})

	It("P2: reports Converging when Scheduled is Unknown (no verdict yet)", func() {
		rv, rvrs := convergenceFixture(0, 2, 0)
		rvrs = pendingTBRVR(rvrs, 2, scheduledCond(metav1.ConditionUnknown,
			v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonWaitingForReplicatedVolume, "waiting", 3))

		_, report := computeTargetLayoutAction(rv, rvrs, nil)
		Expect(report.reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverging))
	})

	It("P2: returns to Converging once the pending tie-breaker becomes Scheduled=True", func() {
		rv, rvrs := convergenceFixture(0, 2, 0)
		rvrs = pendingTBRVR(rvrs, 2, scheduledCond(metav1.ConditionTrue,
			v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonScheduled, "", 3))

		_, report := computeTargetLayoutAction(rv, rvrs, nil)
		Expect(report.reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverging))
	})

	// ── №17: deletion ─────────────────────────────────────────────────────────

	It("reports Unknown/VolumeDeleting and takes no action while the RV is being deleted", func() {
		rv, rvrs := convergenceFixture(0, 3, 0) // would otherwise plan a P1 retype
		now := metav1.Now()
		rv.DeletionTimestamp = &now

		action, report := computeTargetLayoutAction(rv, rvrs, nil)
		Expect(action.kind).To(Equal(layoutActionNone))
		Expect(report.status).To(Equal(metav1.ConditionUnknown))
		Expect(report.reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonVolumeDeleting))
		Expect(report.message).To(Equal("volume is being deleted; layout convergence suspended"))
	})

	// ── №19: preselection mirrors the lose-side zone FTT guard ────────────────

	It("P1: picks a TransZonal candidate whose retype keeps every zone loss survivable", func() {
		// 3D in three zones at an r2 config: the post-state 2D+1TB survives losing any
		// zone, so the lexicographically last replica is admissible.
		rv, rvrs := convergenceFixture(0, 3, 0)
		rv.Status.Configuration.Topology = v1alpha1.TopologyTransZonal
		rv.Status.Datamesh.Members[0].Zone = "zone-a"
		rv.Status.Datamesh.Members[1].Zone = "zone-b"
		rv.Status.Datamesh.Members[2].Zone = "zone-c"

		action, _ := computeTargetLayoutAction(rv, rvrs, nil)
		Expect(action.kind).To(Equal(layoutActionRetypeToTieBreaker))
		Expect(action.retypeRVRName).To(Equal(v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 2)))
	})

	It("P1: reports CannotConverge for a TransZonal 2-zone 2D+1D layout (dispatch would stay blocked)", func() {
		// zone-a holds 2D, zone-b holds 1D. The zone-b replica passes the gain-side TB
		// placement check, but retyping it leaves 0D+1TB when zone-a is lost, so the
		// lose-side guard would block the transition forever. Report it honestly instead
		// of flipping the spec and waiting for a dispatch that never happens.
		rv, rvrs := convergenceFixture(0, 3, 0)
		rv.Status.Configuration.Topology = v1alpha1.TopologyTransZonal
		rv.Status.Datamesh.Members[0].Zone = "zone-a"
		rv.Status.Datamesh.Members[1].Zone = "zone-a"
		rv.Status.Datamesh.Members[2].Zone = "zone-b"

		action, report := computeTargetLayoutAction(rv, rvrs, nil)
		Expect(action.kind).To(Equal(layoutActionNone))
		Expect(report.reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonCannotConverge))
		Expect(report.message).To(ContainSubstring("zone would lose quorum"))
	})

	// ── №4: tie-breaker replacement (strict create-first) ─────────────────────
	//
	// A tie-breaker whose RVR is being deleted is still a datamesh member (its finalizer holds
	// it) and still counted by the raw layout, but the datamesh will not release it before an
	// operational replacement exists (guardTBSufficient). The replacement deficit is therefore
	// computed separately, over non-deleting tie-breakers. One test per row of the state table.

	// markDeleting stamps a deletion timestamp on the named RVR, as the API server does when the
	// object is deleted while a finalizer still holds it.
	markDeleting := func(rvrs []*v1alpha1.ReplicatedVolumeReplica, name string) {
		now := metav1.Now()
		rvr := findRVRByName(rvrs, name)
		Expect(rvr).NotTo(BeNil())
		rvr.DeletionTimestamp = &now
	}

	It("P2: a terminating pending tie-breaker does not hold back the creation", func() {
		// A tie-breaker RVR that never joined and is now being deleted will never satisfy the
		// deficit — waiting for it would stall the heal until it is finalized.
		rv, rvrs := convergenceFixture(0, 2, 0)
		rvrs = pendingTBRVR(rvrs, 2, nil)
		now := metav1.Now()
		rvrs[len(rvrs)-1].DeletionTimestamp = &now

		action, _ := computeTargetLayoutAction(rv, rvrs, nil)
		Expect(action.kind).To(Equal(layoutActionCreateTieBreaker))
	})

	It("replacement: creates one when the only tie-breaker is terminating", func() {
		rv, rvrs := convergenceFixture(0, 2, 1)
		markDeleting(rvrs, v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 2))

		action, report := computeTargetLayoutAction(rv, rvrs, nil)
		Expect(action.kind).To(Equal(layoutActionCreateTieBreaker))
		Expect(report.status).To(Equal(metav1.ConditionFalse))
		Expect(report.reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverging))
		Expect(report.message).To(Equal(
			"tie-breaker rv-1-2 is terminating: creating a replacement (have 2D+1TB, want 2D+1TB)"))
	})

	It("replacement: reports Converging while the replacement RVR is created but not a member", func() {
		rv, rvrs := convergenceFixture(0, 2, 1)
		markDeleting(rvrs, v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 2))
		rvrs = pendingTBRVR(rvrs, 3, nil)

		action, report := computeTargetLayoutAction(rv, rvrs, nil)
		Expect(action.kind).To(Equal(layoutActionNone))
		Expect(report.reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverging))
		Expect(report.message).To(Equal(
			"tie-breaker rv-1-2 is terminating: replacement tie-breaker creation pending (have 2D+1TB, want 2D+1TB)"))
	})

	It("replacement: reports CannotConverge when no free eligible node can host it", func() {
		// Every eligible node is occupied (the terminating tie-breaker still holds its own), so
		// the scheduler cannot place the replacement. Strict create-first: the old tie-breaker
		// keeps working, the replacement RVR stays pending and is placed once a node frees up.
		rv, rvrs := convergenceFixture(0, 2, 1)
		markDeleting(rvrs, v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 2))
		rvrs = pendingTBRVR(rvrs, 3, scheduledCond(metav1.ConditionFalse,
			v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonSchedulingFailed,
			"no eligible node available", 3))

		action, report := computeTargetLayoutAction(rv, rvrs, nil)
		Expect(action.kind).To(Equal(layoutActionNone))
		Expect(report.reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonCannotConverge))
		Expect(report.message).To(Equal(
			"tie-breaker rv-1-2 is terminating: cannot place a replacement (have 2D+1TB, want 2D+1TB): " +
				"rv-1-3: no eligible node available"))
	})

	It("replacement: a stale Scheduled=False on the replacement is not a verdict", func() {
		rv, rvrs := convergenceFixture(0, 2, 1)
		markDeleting(rvrs, v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 2))
		rvrs = pendingTBRVR(rvrs, 3, scheduledCond(metav1.ConditionFalse,
			v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonSchedulingFailed,
			"no eligible node available", 2)) // generation is 3 → stale

		_, report := computeTargetLayoutAction(rv, rvrs, nil)
		Expect(report.reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverging))
	})

	It("replacement: reports Converging once the replacement joined the datamesh (raw 2D+2TB)", func() {
		// Covers both "member but not operational yet" and "operational": whether the old
		// tie-breaker may be released is the DMTE guard's decision, convergence only reports.
		rv, rvrs := convergenceFixture(0, 2, 2)
		markDeleting(rvrs, v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 2))

		action, report := computeTargetLayoutAction(rv, rvrs, nil)
		Expect(action.kind).To(Equal(layoutActionNone))
		Expect(report.reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverging))
		Expect(report.message).To(Equal(
			"tie-breaker rv-1-2 is terminating: its replacement joined the datamesh, " +
				"waiting for it to leave (have 2D+2TB, want 2D+1TB)"))

		// status.layout stays raw and honest during the replacement window.
		Expect(computeLayoutReport(rv, rvrs, nil).layout).To(Equal(ptr.To("2D+2TB")))
	})

	It("replacement: a terminating tie-breaker the layout does not need is just awaited", func() {
		// r3 (3D) with a leftover tie-breaker: nothing to create, the departure is legitimate
		// (TB_min=0 for an odd diskful count) and simply in progress.
		rv, rvrs := convergenceFixture(1, 3, 1)
		markDeleting(rvrs, v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 3))

		action, report := computeTargetLayoutAction(rv, rvrs, nil)
		Expect(action.kind).To(Equal(layoutActionNone))
		Expect(report.reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverging))
		Expect(report.message).To(Equal(
			"tie-breaker rv-1-3 is terminating: waiting for it to leave the datamesh (have 3D+1TB, want 3D)"))
	})

	It("replacement: reports Converged once the old tie-breaker has left (2D+1TB)", func() {
		rv, rvrs := convergenceFixture(0, 2, 1)

		action, report := computeTargetLayoutAction(rv, rvrs, nil)
		Expect(action.kind).To(Equal(layoutActionNone))
		Expect(report.status).To(Equal(metav1.ConditionTrue))
		Expect(report.reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverged))
	})

	It("replacement: a tie-breaker member whose RVR is gone is left to the orphan path", func() {
		// No RVR at all is not a graceful departure: the datamesh force-removes the orphan
		// (no tie-breaker guard is involved), and the plain P2 deficit then heals the layout.
		// Creating a replacement in parallel would race with the force-removal.
		rv, rvrs := convergenceFixture(0, 2, 1)
		rvrs = rvrs[:2] // the tie-breaker RVR vanished together with its node

		action, report := computeTargetLayoutAction(rv, rvrs, nil)
		Expect(action.kind).To(Equal(layoutActionNone))
		Expect(report.status).To(Equal(metav1.ConditionTrue))
		Expect(report.reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverged))
	})

	It("replacement: a terminating Diskful replica does not trigger a tie-breaker replacement", func() {
		rv, rvrs := convergenceFixture(0, 2, 1)
		markDeleting(rvrs, v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0))

		action, report := computeTargetLayoutAction(rv, rvrs, nil)
		Expect(action.kind).To(Equal(layoutActionNone))
		Expect(report.status).To(Equal(metav1.ConditionTrue))
		Expect(report.reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverged))
	})

	It("replacement: an active layout-changing transition still wins over the deficit", func() {
		// The old tie-breaker's RemoveReplica is running — the departure is in flight, nothing
		// to plan this pass.
		rv, rvrs := convergenceFixture(0, 2, 2)
		markDeleting(rvrs, v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 2))
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveReplica,
			ReplicaType: v1alpha1.ReplicaTypeTieBreaker,
		}}

		action, report := computeTargetLayoutAction(rv, rvrs, nil)
		Expect(action.kind).To(Equal(layoutActionNone))
		Expect(report.message).To(Equal("layout transition in progress: have 2D+2TB, want 2D+1TB"))
	})

	It("replacement: a genuine tie-breaker surplus is still reported as unsupported", func() {
		// 2D+3TB with one terminating leaves two tie-breakers for an r2 layout — beyond the
		// replacement domain, so it is reported honestly instead of being papered over.
		rv, rvrs := convergenceFixture(0, 2, 3)
		markDeleting(rvrs, v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 2))

		action, report := computeTargetLayoutAction(rv, rvrs, nil)
		Expect(action.kind).To(Equal(layoutActionNone))
		Expect(report.reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonTransitionUnsupported))
		Expect(report.message).To(ContainSubstring("have 2D+3TB, want 2D+1TB"))
	})

	It("replacement: a wrong diskful count is reported honestly, not healed with a tie-breaker", func() {
		rv, rvrs := convergenceFixture(1, 2, 1) // r3 config wants 3D
		markDeleting(rvrs, v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 2))

		action, report := computeTargetLayoutAction(rv, rvrs, nil)
		Expect(action.kind).To(Equal(layoutActionNone))
		Expect(report.reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonTransitionUnsupported))
	})

	It("4D at an r2 config: plans exactly one retype, then reports Unsupported at 3D+1TB", func() {
		// One retype fills the single tie-breaker deficit (never removes the extra diskful voters).
		rv4, rvrs4 := convergenceFixture(0, 4, 0)
		action, _ := computeTargetLayoutAction(rv4, rvrs4, nil)
		Expect(action.kind).To(Equal(layoutActionRetypeToTieBreaker))

		// After the retype resolves the datamesh is 3D+1TB — still not the intended 2D+1TB, but
		// no whitelisted transition applies, so it is reported honestly.
		rv3, rvrs3 := convergenceFixture(0, 3, 1)
		action, report := computeTargetLayoutAction(rv3, rvrs3, nil)
		Expect(action.kind).To(Equal(layoutActionNone))
		Expect(report.reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonTransitionUnsupported))
		Expect(report.message).To(Equal(
			"layout mismatch: have 3D+1TB, want 2D+1TB; automatic transition is not supported, manual intervention required"))
	})
})

var _ = Describe("isMemberAttached", func() {
	It("is true when the member is marked Attached", func() {
		m := &v1alpha1.DatameshMember{Name: "rvr-0", NodeName: "node-0", Attached: true}
		Expect(isMemberAttached(m, nil)).To(BeTrue())
	})

	It("is true when an active RVA targets the member's node", func() {
		m := &v1alpha1.DatameshMember{Name: "rvr-0", NodeName: "node-0"}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{{
			Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{NodeName: "node-0"},
		}}
		Expect(isMemberAttached(m, rvas)).To(BeTrue())
	})

	It("ignores deleting RVAs and RVAs on other nodes", func() {
		now := metav1.Now()
		m := &v1alpha1.DatameshMember{Name: "rvr-0", NodeName: "node-0"}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{
			{Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{NodeName: "node-1"}},
			{ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &now, Finalizers: []string{"f"}},
				Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{NodeName: "node-0"}},
		}
		Expect(isMemberAttached(m, rvas)).To(BeFalse())
	})
})

var _ = Describe("reconcileLayoutConvergence", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	})

	// buildRec builds a fake client seeded with the given RVRs and returns the reconciler plus
	// the RVRs re-read from the client (so they carry a resourceVersion for optimistic-lock patches).
	buildRec := func(ctx context.Context, rvrs []*v1alpha1.ReplicatedVolumeReplica, extra ...interceptor.Funcs) (*Reconciler, []*v1alpha1.ReplicatedVolumeReplica) {
		objs := make([]client.Object, 0, len(rvrs))
		for _, r := range rvrs {
			objs = append(objs, r)
		}
		b := newClientBuilder(scheme).WithObjects(objs...)
		if len(extra) > 0 {
			b = b.WithInterceptorFuncs(extra[0])
		}
		rec := NewReconciler(b.Build(), scheme)
		fresh, err := rec.getRVRsSorted(ctx, "rv-1")
		Expect(err).NotTo(HaveOccurred())
		return rec, fresh
	}

	It("P1: retypes exactly one Diskful RVR to TieBreaker and requeues without stopping the pass", func(ctx SpecContext) {
		rv, rvrs := convergenceFixture(0, 3, 0) // r2 config, actual 3D
		rec, fresh := buildRec(ctx, rvrs)

		outcome := rec.reconcileLayoutConvergence(ctx, rv, &fresh, nil)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		// ContinueAndRequeue: the root Reconcile must still reach its status patch.
		Expect(outcome.ShouldReturn()).To(BeFalse())
		Expect(outcome.ToCtrl()).To(Requeue())

		retyped := 0
		for _, r := range fresh {
			if r.Spec.Type == v1alpha1.ReplicaTypeTieBreaker {
				retyped++
				Expect(r.Name).To(Equal(v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 2)))
			}
		}
		Expect(retyped).To(Equal(1))

		// Persisted to the API server.
		var updated v1alpha1.ReplicatedVolumeReplica
		Expect(rec.cl.Get(ctx, client.ObjectKey{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 2)}, &updated)).To(Succeed())
		Expect(updated.Spec.Type).To(Equal(v1alpha1.ReplicaTypeTieBreaker))
	})

	It("P1: is a no-op (idempotent) while a membership transition is active", func(ctx SpecContext) {
		rv, rvrs := convergenceFixture(0, 3, 0)
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{
			{
				Type:            v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeReplicaType,
				FromReplicaType: v1alpha1.ReplicaTypeDiskful,
				ToReplicaType:   v1alpha1.ReplicaTypeTieBreaker,
			},
		}
		rec, fresh := buildRec(ctx, rvrs)

		outcome := rec.reconcileLayoutConvergence(ctx, rv, &fresh, nil)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.ShouldReturn()).To(BeFalse())
		for _, r := range fresh {
			Expect(r.Spec.Type).To(Equal(v1alpha1.ReplicaTypeDiskful))
		}
	})

	It("P2: creates exactly one TieBreaker RVR and requeues without stopping the pass", func(ctx SpecContext) {
		rv, rvrs := convergenceFixture(0, 2, 0) // r2 config, actual 2D
		rec, fresh := buildRec(ctx, rvrs)

		outcome := rec.reconcileLayoutConvergence(ctx, rv, &fresh, nil)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.ShouldReturn()).To(BeFalse()) // ContinueAndRequeue
		Expect(outcome.ToCtrl()).To(Requeue())
		Expect(fresh).To(HaveLen(3))

		var list v1alpha1.ReplicatedVolumeReplicaList
		Expect(rec.cl.List(ctx, &list)).To(Succeed())
		Expect(list.Items).To(HaveLen(3))
		tbCount := 0
		for i := range list.Items {
			if list.Items[i].Spec.Type == v1alpha1.ReplicaTypeTieBreaker {
				tbCount++
			}
		}
		Expect(tbCount).To(Equal(1))
	})

	It("P2: does not create a second tie-breaker while one is pending", func(ctx SpecContext) {
		rv, rvrs := convergenceFixture(0, 2, 0)
		// A TieBreaker RVR already exists but has not joined the datamesh yet.
		rvrs = append(rvrs, &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 2),
				Finalizers: []string{v1alpha1.RVControllerFinalizer}},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{ReplicatedVolumeName: "rv-1", Type: v1alpha1.ReplicaTypeTieBreaker},
		})
		rec, fresh := buildRec(ctx, rvrs)

		outcome := rec.reconcileLayoutConvergence(ctx, rv, &fresh, nil)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.ShouldReturn()).To(BeFalse())

		var list v1alpha1.ReplicatedVolumeReplicaList
		Expect(rec.cl.List(ctx, &list)).To(Succeed())
		Expect(list.Items).To(HaveLen(3)) // unchanged
	})

	It("P2: treats AlreadyExists on create as an expected race (requeue, no error)", func(ctx SpecContext) {
		rv, rvrs := convergenceFixture(0, 2, 0)
		rec, fresh := buildRec(ctx, rvrs, interceptor.Funcs{
			Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if _, ok := obj.(*v1alpha1.ReplicatedVolumeReplica); ok {
					return apierrors.NewAlreadyExists(
						schema.GroupResource{Group: v1alpha1.SchemeGroupVersion.Group, Resource: "replicatedvolumereplicas"},
						"rvr-exists",
					)
				}
				return cl.Create(ctx, obj, opts...)
			},
		})

		outcome := rec.reconcileLayoutConvergence(ctx, rv, &fresh, nil)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.ShouldReturn()).To(BeFalse()) // ContinueAndRequeue
		Expect(outcome.ToCtrl()).To(Requeue())
	})

	It("replacement: creates exactly one tie-breaker for a terminating one, and no second one", func(ctx SpecContext) {
		rv, rvrs := convergenceFixture(0, 2, 1) // r2 config, actual 2D+1TB
		now := metav1.Now()
		rvrs[2].DeletionTimestamp = &now // the tie-breaker is leaving, held by its finalizer
		rec, fresh := buildRec(ctx, rvrs)

		outcome := rec.reconcileLayoutConvergence(ctx, rv, &fresh, nil)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.ShouldReturn()).To(BeFalse()) // ContinueAndRequeue
		Expect(outcome.ToCtrl()).To(Requeue())
		Expect(fresh).To(HaveLen(4))

		// The next pass sees the replacement pending and must not create a second one.
		outcome = rec.reconcileLayoutConvergence(ctx, rv, &fresh, nil)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(fresh).To(HaveLen(4))

		var list v1alpha1.ReplicatedVolumeReplicaList
		Expect(rec.cl.List(ctx, &list)).To(Succeed())
		Expect(list.Items).To(HaveLen(4))
		live, deleting := 0, 0
		for i := range list.Items {
			if list.Items[i].Spec.Type != v1alpha1.ReplicaTypeTieBreaker {
				continue
			}
			if list.Items[i].DeletionTimestamp != nil {
				deleting++
			} else {
				live++
			}
		}
		Expect(deleting).To(Equal(1))
		Expect(live).To(Equal(1))
	})

	It("takes no action for a mismatch outside the whitelist (2D at an r3 config)", func(ctx SpecContext) {
		rv, rvrs := convergenceFixture(1, 2, 0) // r3 config wants 3D, actual 2D
		rec, fresh := buildRec(ctx, rvrs)

		outcome := rec.reconcileLayoutConvergence(ctx, rv, &fresh, nil)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.ShouldReturn()).To(BeFalse())
		Expect(fresh).To(HaveLen(2))
		for _, r := range fresh {
			Expect(r.Spec.Type).To(Equal(v1alpha1.ReplicaTypeDiskful))
		}
	})

	It("is a no-op when the RV is being deleted", func(ctx SpecContext) {
		rv, rvrs := convergenceFixture(0, 3, 0)
		now := metav1.Now()
		rv.DeletionTimestamp = &now
		rec, fresh := buildRec(ctx, rvrs)

		outcome := rec.reconcileLayoutConvergence(ctx, rv, &fresh, nil)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(outcome.ShouldReturn()).To(BeFalse())
		for _, r := range fresh {
			Expect(r.Spec.Type).To(Equal(v1alpha1.ReplicaTypeDiskful))
		}
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: layout convergence — status of the acting pass reaches the patch
//

var _ = Describe("Root Reconcile layout convergence status persistence", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	})

	It("persists status.layout and LayoutConverged on the pass that performs the retype", func(ctx SpecContext) {
		// 3D at an r2 config: layout convergence retypes one Diskful RVR. A terminal
		// outcome would make the root Reconcile return before patchRVStatus, dropping
		// every status change computed in the same pass.
		cfg := v1alpha1.ReplicatedVolumeConfiguration{
			Topology: v1alpha1.TopologyIgnored, FailuresToTolerate: 1,
			GuaranteedMinimumDataRedundancy: 0,
			VolumeAccess:                    v1alpha1.VolumeAccessPreferablyLocal,
			ReplicatedStoragePoolName:       "test-pool",
		}
		rsc := &v1alpha1.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{Name: "rsc-1", Generation: 1},
			Status: v1alpha1.ReplicatedStorageClassStatus{
				ConfigurationGeneration: 1,
				Configuration:           cfg.DeepCopy(),
			},
		}
		rsp := newTestRSP("test-pool")

		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "rv-1",
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
				Labels:     map[string]string{v1alpha1.ReplicatedStorageClassLabelKey: "rsc-1"},
			},
			Spec: v1alpha1.ReplicatedVolumeSpec{
				Size:                       resource.MustParse("10Gi"),
				ReplicatedStorageClassName: "rsc-1",
			},
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshRevision:                1, // post-formation → normal operation
				ConfigurationGeneration:         1,
				ConfigurationObservedGeneration: 1,
				Configuration:                   cfg.DeepCopy(),
			},
		}
		fixture, rvrs := convergenceFixture(0, 3, 0) // r2 config wants 2D+1TB, actual 3D
		rv.Status.Datamesh = fixture.Status.Datamesh

		objs := []client.Object{rv, rsc, rsp}
		for _, r := range rvrs {
			objs = append(objs, r)
		}
		cl := newClientBuilder(scheme).
			WithObjects(objs...).
			WithStatusSubresource(rv, rsc).
			Build()
		rec := NewReconciler(cl, scheme)

		result, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Requeue())

		// The retype landed...
		var retyped v1alpha1.ReplicatedVolumeReplica
		Expect(cl.Get(ctx, client.ObjectKey{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 2)}, &retyped)).
			To(Succeed())
		Expect(retyped.Spec.Type).To(Equal(v1alpha1.ReplicaTypeTieBreaker))

		// ...and so did the status computed in the same pass.
		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
		Expect(updated.Status.Layout).To(Equal(ptr.To("3D")))
		cond := obju.GetStatusCondition(&updated, v1alpha1.ReplicatedVolumeCondLayoutConvergedType)
		Expect(cond).NotTo(BeNil(), "LayoutConverged computed in the acting pass must be persisted")
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverging))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: tie-breaker replacement (strict create-first)
//

var _ = Describe("Root Reconcile tie-breaker replacement", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	})

	// r2 configuration (2D+1TB) shared by the RSC and the RV status.
	r2Config := func() v1alpha1.ReplicatedVolumeConfiguration {
		return v1alpha1.ReplicatedVolumeConfiguration{
			Topology: v1alpha1.TopologyIgnored, FailuresToTolerate: 1,
			GuaranteedMinimumDataRedundancy: 0,
			VolumeAccess:                    v1alpha1.VolumeAccessPreferablyLocal,
			ReplicatedStoragePoolName:       "test-pool",
		}
	}

	// deletingTBCluster builds a formed r2 volume (2D+1TB) whose tie-breaker RVR is being
	// deleted: it carries a deletion timestamp, the RV controller finalizer that holds it,
	// and a Leave request for the datamesh. Everything else is settled (quorum, qmr and
	// system networks match), so the only pending work is the tie-breaker's departure.
	deletingTBCluster := func(ctx context.Context) (*Reconciler, *v1alpha1.ReplicatedVolume) {
		cfg := r2Config()
		rsc := &v1alpha1.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{Name: "rsc-1", Generation: 1},
			Status: v1alpha1.ReplicatedStorageClassStatus{
				ConfigurationGeneration: 1,
				Configuration:           cfg.DeepCopy(),
			},
		}
		fixture, rvrs := convergenceFixture(0, 2, 1)
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "rv-1",
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
				Labels:     map[string]string{v1alpha1.ReplicatedStorageClassLabelKey: "rsc-1"},
			},
			Spec: v1alpha1.ReplicatedVolumeSpec{
				Size:                       resource.MustParse("10Gi"),
				ReplicatedStorageClassName: "rsc-1",
			},
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshRevision:                1, // post-formation → normal operation
				ConfigurationGeneration:         1,
				ConfigurationObservedGeneration: 1,
				Configuration:                   cfg.DeepCopy(),
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members:                 fixture.Status.Datamesh.Members,
					Quorum:                  2, // voters/2+1 → the quorum dispatcher stays quiet
					QuorumMinimumRedundancy: 1, // GMDR+1
					SystemNetworkNames:      []string{"Internal"},
				},
			},
		}

		// The tie-breaker RVR is being deleted and asked the datamesh to let it leave.
		now := metav1.Now()
		tb := rvrs[2]
		tb.DeletionTimestamp = &now
		tb.Status.DatameshRequest = &v1alpha1.DatameshMembershipRequest{
			Operation: v1alpha1.DatameshMembershipRequestOperationLeave,
		}

		objs := []client.Object{rv, rsc, newTestRSP("test-pool")}
		for _, r := range rvrs {
			objs = append(objs, r)
		}
		cl := newClientBuilder(scheme).
			WithObjects(objs...).
			WithStatusSubresource(rv, rsc).
			Build()
		rec := NewReconciler(cl, scheme)
		fresh, err := rec.getRV(ctx, "rv-1")
		Expect(err).NotTo(HaveOccurred())
		return rec, fresh
	}

	// tieBreakerRVRs returns the tie-breaker RVRs of rv-1, split into live and deleting.
	tieBreakerRVRs := func(ctx context.Context, rec *Reconciler) (live, deleting []string) {
		var list v1alpha1.ReplicatedVolumeReplicaList
		Expect(rec.cl.List(ctx, &list)).To(Succeed())
		for i := range list.Items {
			if list.Items[i].Spec.Type != v1alpha1.ReplicaTypeTieBreaker {
				continue
			}
			if list.Items[i].DeletionTimestamp != nil {
				deleting = append(deleting, list.Items[i].Name)
			} else {
				live = append(live, list.Items[i].Name)
			}
		}
		return live, deleting
	}

	It("deadlock repro: the last tie-breaker cannot leave, so convergence must create its replacement", func(ctx SpecContext) {
		rec, rv := deletingTBCluster(ctx)

		_, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())

		var updated v1alpha1.ReplicatedVolume
		Expect(rec.cl.Get(ctx, client.ObjectKey{Name: "rv-1"}, &updated)).To(Succeed())

		// Fact 1: the datamesh refuses to release the only tie-breaker — no RemoveReplica
		// transition is dispatched and the member stays in the datamesh (the terminating
		// RVR keeps serving as a DRBD peer, held by its finalizer).
		for i := range updated.Status.DatameshTransitions {
			Expect(updated.Status.DatameshTransitions[i].Type).NotTo(
				Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveReplica))
		}
		_, actualTB := computeActualLayout(&updated)
		Expect(actualTB).To(Equal(1), "the deleting tie-breaker must stay a datamesh member")
		Expect(updated.Status.DatameshReplicaRequests).To(HaveLen(1))
		Expect(updated.Status.DatameshReplicaRequests[0].Message).To(ContainSubstring("TieBreaker required"))

		// Fact 2: with the departure blocked, layout convergence must create the replacement
		// tie-breaker (strict create-first) instead of reporting the layout converged.
		live, deleting := tieBreakerRVRs(ctx, rec)
		Expect(deleting).To(HaveLen(1), "the old tie-breaker is still terminating")
		Expect(live).To(HaveLen(1), "a replacement tie-breaker must be created while the old one leaves")

		Expect(updated.Status.Layout).To(Equal(ptr.To("2D+1TB")), "status.layout reports the raw member composition")
		cond := obju.GetStatusCondition(&updated, v1alpha1.ReplicatedVolumeCondLayoutConvergedType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonConverging))
	})
})

var _ = Describe("reconcileDeletion layout condition", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	})

	// deletingRV builds a deleting RV with the given datamesh members, seeded in a fake
	// client together with its RVRs, and returns the reconciler plus the RV re-read from
	// the client (so it carries a resourceVersion for optimistic-lock patches).
	deletingRV := func(ctx context.Context, members []v1alpha1.DatameshMember) (*Reconciler, *v1alpha1.ReplicatedVolume) {
		now := metav1.Now()
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name: "rv-1", DeletionTimestamp: &now,
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
			},
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshRevision: 1,
				Datamesh:         v1alpha1.ReplicatedVolumeDatamesh{Members: members},
			},
		}
		cl := newClientBuilder(scheme).WithObjects(rv).WithStatusSubresource(rv).Build()
		rec := NewReconciler(cl, scheme)
		fresh, err := rec.getRV(ctx, "rv-1")
		Expect(err).NotTo(HaveOccurred())
		return rec, fresh
	}

	It("sets LayoutConverged=Unknown/VolumeDeleting on the early deletion path", func(ctx SpecContext) {
		// An unattached RV goes straight to reconcileDeletion, never through normal
		// operation, so this branch must publish the condition itself.
		rec, rv := deletingRV(ctx, layoutMembers(2, 1))
		var rvrs []*v1alpha1.ReplicatedVolumeReplica

		outcome := rec.reconcileDeletion(ctx, rv, nil, &rvrs)
		Expect(outcome.Error()).NotTo(HaveOccurred())

		var updated v1alpha1.ReplicatedVolume
		Expect(rec.cl.Get(ctx, client.ObjectKey{Name: "rv-1"}, &updated)).To(Succeed())
		Expect(updated.Status.Datamesh.Members).To(BeEmpty())
		cond := obju.GetStatusCondition(&updated, v1alpha1.ReplicatedVolumeCondLayoutConvergedType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonVolumeDeleting))
		Expect(cond.Message).To(Equal("volume is being deleted; layout convergence suspended"))
	})

	It("publishes the condition even when there are no datamesh members to clear", func(ctx SpecContext) {
		rec, rv := deletingRV(ctx, nil)
		var rvrs []*v1alpha1.ReplicatedVolumeReplica

		outcome := rec.reconcileDeletion(ctx, rv, nil, &rvrs)
		Expect(outcome.Error()).NotTo(HaveOccurred())

		var updated v1alpha1.ReplicatedVolume
		Expect(rec.cl.Get(ctx, client.ObjectKey{Name: "rv-1"}, &updated)).To(Succeed())
		cond := obju.GetStatusCondition(&updated, v1alpha1.ReplicatedVolumeCondLayoutConvergedType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeCondLayoutConvergedReasonVolumeDeleting))
	})

	It("is idempotent: a second pass reports no change", func(ctx SpecContext) {
		rec, rv := deletingRV(ctx, nil)
		var rvrs []*v1alpha1.ReplicatedVolumeReplica

		Expect(rec.reconcileDeletion(ctx, rv, nil, &rvrs).Error()).NotTo(HaveOccurred())
		before := obju.GetStatusCondition(rv, v1alpha1.ReplicatedVolumeCondLayoutConvergedType).DeepCopy()

		Expect(rec.reconcileDeletion(ctx, rv, nil, &rvrs).Error()).NotTo(HaveOccurred())
		after := obju.GetStatusCondition(rv, v1alpha1.ReplicatedVolumeCondLayoutConvergedType)
		Expect(after.LastTransitionTime).To(Equal(before.LastTransitionTime))
	})
})
