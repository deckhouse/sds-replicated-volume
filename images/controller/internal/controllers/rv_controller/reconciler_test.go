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
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/idset"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes/testhelpers"
)

func TestRvControllerReconciler(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "rv_controller Reconciler Suite")
}

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
		ObjectMeta: metav1.ObjectMeta{Name: name},
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
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
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
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
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
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
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

	It("returns false when attached members exist", func() {
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
		Expect(rvShouldNotExist(rv)).To(BeFalse())
	})

	It("returns false when Detach transition in progress", func() {
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

	It("returns true when deleting with only our finalizer, no attached members, and no Detach transitions", func() {
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
						{Name: "rvr-1", Attached: false},
					},
				},
			},
		}
		Expect(rvShouldNotExist(rv)).To(BeTrue())
	})
})

var _ = Describe("computeIntendedDiskfulReplicaCount", func() {
	It("returns D = FTT + GMDR + 1 for all valid FTT/GMDR combinations", func() {
		cases := []struct {
			ftt, gmdr byte
			expected  byte
		}{
			{0, 0, 1}, // 1D
			{1, 0, 2}, // 2D+1TB
			{0, 1, 2}, // 2D
			{1, 1, 3}, // 3D
			{2, 1, 4}, // 4D
			{1, 2, 4}, // 4D+1TB
			{2, 2, 5}, // 5D
		}
		for _, tc := range cases {
			rv := &v1alpha1.ReplicatedVolume{
				Status: v1alpha1.ReplicatedVolumeStatus{
					Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
						FailuresToTolerate: tc.ftt, GuaranteedMinimumDataRedundancy: tc.gmdr,
						ReplicatedStoragePoolName: "test-pool",
					},
				},
			}
			Expect(computeIntendedDiskfulReplicaCount(rv)).To(Equal(tc.expected),
				"FTT=%d, GMDR=%d", tc.ftt, tc.gmdr)
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
				EffectiveLayout: v1alpha1.ReplicatedVolumeEffectiveLayout{
					FailuresToTolerate:              ftt,
					GuaranteedMinimumDataRedundancy: gmdr,
				},
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{Members: members},
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

var _ = Describe("applyDatameshMemberAbsent", func() {
	It("returns false when all members are in the set", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0), Type: v1alpha1.DatameshMemberTypeDiskful},
						{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 1), Type: v1alpha1.DatameshMemberTypeDiskful},
					},
				},
			},
		}
		var ids idset.IDSet
		ids.Add(0)
		ids.Add(1)
		changed := applyDatameshMemberAbsent(rv, ids)
		Expect(changed).To(BeFalse())
		Expect(rv.Status.Datamesh.Members).To(HaveLen(2))
	})

	It("removes members not in set and returns true", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0), Type: v1alpha1.DatameshMemberTypeDiskful},
						{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 1), Type: v1alpha1.DatameshMemberTypeDiskful},
						{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 2), Type: v1alpha1.DatameshMemberTypeDiskful},
					},
				},
			},
		}
		var ids idset.IDSet
		ids.Add(0)
		ids.Add(2)
		changed := applyDatameshMemberAbsent(rv, ids)
		Expect(changed).To(BeTrue())
		Expect(rv.Status.Datamesh.Members).To(HaveLen(2))
		Expect(rv.Status.Datamesh.Members[0].Name).To(Equal(v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0)))
		Expect(rv.Status.Datamesh.Members[1].Name).To(Equal(v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 2)))
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
				EffectiveLayout: v1alpha1.ReplicatedVolumeEffectiveLayout{
					FailuresToTolerate: 1, GuaranteedMinimumDataRedundancy: 0,
				},
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
				EffectiveLayout: v1alpha1.ReplicatedVolumeEffectiveLayout{
					FailuresToTolerate: 1, GuaranteedMinimumDataRedundancy: 0,
				},
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
				EffectiveLayout: v1alpha1.ReplicatedVolumeEffectiveLayout{
					FailuresToTolerate: 0, GuaranteedMinimumDataRedundancy: 0,
				},
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
// Tests: datamesh access replicas
//

var _ = Describe("removeDatameshMembers", func() {
	It("removes members in the set and returns true", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-0", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "node-1"},
						{Name: "rv-1-1", Type: v1alpha1.DatameshMemberTypeAccess, NodeName: "node-2"},
					},
				},
			},
		}
		Expect(removeDatameshMembers(rv, idset.Of(1))).To(BeTrue())
		Expect(rv.Status.Datamesh.Members).To(HaveLen(1))
		Expect(rv.Status.Datamesh.Members[0].Name).To(Equal("rv-1-0"))
	})

	It("returns false when no member matches", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.DatameshMember{
						{Name: "rv-1-0", Type: v1alpha1.DatameshMemberTypeDiskful},
					},
				},
			},
		}
		Expect(removeDatameshMembers(rv, idset.Of(5))).To(BeFalse())
		Expect(rv.Status.Datamesh.Members).To(HaveLen(1))
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

var _ = Describe("applyDatameshReplicaRequestMessage", func() {
	It("sets message and returns true when different", func() {
		p := &v1alpha1.ReplicatedVolumeDatameshReplicaRequest{Name: "rv-1-1", Message: "old"}
		Expect(applyDatameshReplicaRequestMessage(p, "new")).To(BeTrue())
		Expect(p.Message).To(Equal("new"))
	})

	It("returns false when message is the same", func() {
		p := &v1alpha1.ReplicatedVolumeDatameshReplicaRequest{Name: "rv-1-0", Message: "same"}
		Expect(applyDatameshReplicaRequestMessage(p, "same")).To(BeFalse())
	})
})
