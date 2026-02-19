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
			Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
				Topology:        v1alpha1.TopologyIgnored,
				Replication:     v1alpha1.ReplicationNone,
				VolumeAccess:    v1alpha1.VolumeAccessLocal,
				StoragePoolName: "test-pool",
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
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						Topology:        v1alpha1.TopologyTransZonal,
						Replication:     v1alpha1.ReplicationConsistencyAndAvailability,
						VolumeAccess:    v1alpha1.VolumeAccessPreferablyLocal,
						StoragePoolName: "pool-1",
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
			Expect(updated.Status.Configuration.Replication).To(Equal(v1alpha1.ReplicationConsistencyAndAvailability))
			Expect(updated.Status.Configuration.VolumeAccess).To(Equal(v1alpha1.VolumeAccessPreferablyLocal))
			Expect(updated.Status.Configuration.StoragePoolName).To(Equal("pool-1"))
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

		It("sets StaleConfiguration condition when generation does not match RSC", func(ctx SpecContext) {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					ConfigurationGeneration: 10,
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						Topology:        v1alpha1.TopologyZonal,
						Replication:     v1alpha1.ReplicationAvailability,
						VolumeAccess:    v1alpha1.VolumeAccessAny,
						StoragePoolName: "new-pool",
					},
				},
			}
			rsp := newTestRSP("old-pool")

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
					ConfigurationGeneration: 5,
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						Topology:        v1alpha1.TopologyTransZonal,
						Replication:     v1alpha1.ReplicationConsistencyAndAvailability,
						VolumeAccess:    v1alpha1.VolumeAccessPreferablyLocal,
						StoragePoolName: "old-pool",
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
			// Should still have the original configuration (not overwritten).
			Expect(updated.Status.Configuration).NotTo(BeNil())
			Expect(updated.Status.Configuration.Topology).To(Equal(v1alpha1.TopologyTransZonal))
			Expect(updated.Status.Configuration.StoragePoolName).To(Equal("old-pool"))
			Expect(updated.Status.ConfigurationGeneration).To(Equal(int64(5)))

			// Check ConfigurationReady condition - should be StaleConfiguration.
			cond := obju.GetStatusCondition(&updated, v1alpha1.ReplicatedVolumeCondConfigurationReadyType)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonStaleConfiguration))
		})

		It("sets Ready condition when generation matches RSC", func(ctx SpecContext) {
			rsc := &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					ConfigurationGeneration: 5,
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						Topology:        v1alpha1.TopologyTransZonal,
						Replication:     v1alpha1.ReplicationConsistencyAndAvailability,
						VolumeAccess:    v1alpha1.VolumeAccessPreferablyLocal,
						StoragePoolName: "pool-1",
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
					ConfigurationGeneration: 5,
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						Topology:        v1alpha1.TopologyTransZonal,
						Replication:     v1alpha1.ReplicationConsistencyAndAvailability,
						VolumeAccess:    v1alpha1.VolumeAccessPreferablyLocal,
						StoragePoolName: "pool-1",
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

		It("sets ConfigurationRolloutInProgress when ConfigurationGeneration is 0", func(ctx SpecContext) {
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
					// ConfigurationGeneration is 0 (not set).
					Configuration: rsc.Status.Configuration.DeepCopy(),
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

			// Should be ConfigurationRolloutInProgress.
			cond := obju.GetStatusCondition(&updated, v1alpha1.ReplicatedVolumeCondConfigurationReadyType)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonConfigurationRolloutInProgress))
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
						Members: []v1alpha1.ReplicatedVolumeDatameshMember{
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
						Members: []v1alpha1.ReplicatedVolumeDatameshMember{
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
						Members: []v1alpha1.ReplicatedVolumeDatameshMember{
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
		It("returns error for invalid formation phase", func(ctx SpecContext) {
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
					Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
						Topology: v1alpha1.TopologyIgnored, Replication: v1alpha1.ReplicationNone,
						VolumeAccess: v1alpha1.VolumeAccessLocal, StoragePoolName: "test-pool",
					},
					DatameshRevision: 1,
					DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
						{
							Type:      v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation,
							StartedAt: metav1.Now(),
							Formation: &v1alpha1.ReplicatedVolumeDatameshTransitionFormation{
								Phase: "InvalidPhase",
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
			Expect(err.Error()).To(ContainSubstring("invalid formation phase"))
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
			Expect(updated.Status.DatameshTransitions[0].Formation.Phase).To(Equal(v1alpha1.ReplicatedVolumeFormationPhasePreconfigure))
			Expect(updated.Status.DatameshTransitions[0].Message).To(ContainSubstring("Waiting for"))
			Expect(updated.Status.DatameshTransitions[0].Message).To(ContainSubstring("deleting replicas"))
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
			Expect(updated.Status.DatameshTransitions[0].Formation.Phase).To(Equal(v1alpha1.ReplicatedVolumeFormationPhasePreconfigure))
			Expect(updated.Status.DatameshTransitions[0].Message).To(ContainSubstring("Waiting for"))
			Expect(updated.Status.DatameshTransitions[0].Message).To(ContainSubstring("deleting replicas"))
		})
	})
})

var _ = Describe("ensureDatameshPendingReplicaTransitions", func() {
	mkRVR := func(name string, pending *v1alpha1.ReplicatedVolumeReplicaStatusDatameshPendingTransition) *v1alpha1.ReplicatedVolumeReplica {
		return &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshPendingTransition: pending,
			},
		}
	}

	mkPending := func(role v1alpha1.ReplicaType) *v1alpha1.ReplicatedVolumeReplicaStatusDatameshPendingTransition {
		return &v1alpha1.ReplicatedVolumeReplicaStatusDatameshPendingTransition{
			Member: ptr.To(true),
			Type:   role,
		}
	}

	mkRVEntry := func(name string, pending v1alpha1.ReplicatedVolumeReplicaStatusDatameshPendingTransition, firstObserved time.Time) v1alpha1.ReplicatedVolumeDatameshPendingReplicaTransition {
		return v1alpha1.ReplicatedVolumeDatameshPendingReplicaTransition{
			Name:            name,
			Transition:      pending,
			FirstObservedAt: metav1.NewTime(firstObserved),
		}
	}

	It("adds new entry when RVR has pending transition", func(ctx SpecContext) {
		rv := &v1alpha1.ReplicatedVolume{}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rvr-1", mkPending(v1alpha1.ReplicaTypeDiskful)),
		}

		outcome := ensureDatameshPendingReplicaTransitions(ctx, rv, rvrs)

		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rv.Status.DatameshPendingReplicaTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshPendingReplicaTransitions[0].Name).To(Equal("rvr-1"))
		Expect(rv.Status.DatameshPendingReplicaTransitions[0].Transition.Member).To(Equal(ptr.To(true)))
		Expect(rv.Status.DatameshPendingReplicaTransitions[0].Transition.Type).To(Equal(v1alpha1.ReplicaTypeDiskful))
	})

	It("removes entry when RVR no longer has pending transition", func(ctx SpecContext) {
		oldTime := time.Now().Add(-1 * time.Hour)
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshPendingReplicaTransitions: []v1alpha1.ReplicatedVolumeDatameshPendingReplicaTransition{
					mkRVEntry("rvr-1", v1alpha1.ReplicatedVolumeReplicaStatusDatameshPendingTransition{
						Member: ptr.To(true),
						Type:   v1alpha1.ReplicaTypeDiskful,
					}, oldTime),
				},
			},
		}
		// RVR with nil transition (no pending anymore).
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rvr-1", nil),
		}

		outcome := ensureDatameshPendingReplicaTransitions(ctx, rv, rvrs)

		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rv.Status.DatameshPendingReplicaTransitions).To(BeEmpty())
	})

	It("updates entry when transition changed", func(ctx SpecContext) {
		oldTime := time.Now().Add(-1 * time.Hour)
		oldPending := v1alpha1.ReplicatedVolumeReplicaStatusDatameshPendingTransition{
			Member: ptr.To(true),
			Type:   v1alpha1.ReplicaTypeDiskful,
		}
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshPendingReplicaTransitions: []v1alpha1.ReplicatedVolumeDatameshPendingReplicaTransition{
					{
						Name:            "rvr-1",
						Message:         "old message should be cleared",
						Transition:      oldPending,
						FirstObservedAt: metav1.NewTime(oldTime),
					},
				},
			},
		}
		// New transition with different role.
		newPending := mkPending(v1alpha1.ReplicaTypeAccess)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rvr-1", newPending),
		}

		outcome := ensureDatameshPendingReplicaTransitions(ctx, rv, rvrs)

		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rv.Status.DatameshPendingReplicaTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshPendingReplicaTransitions[0].Name).To(Equal("rvr-1"))
		Expect(rv.Status.DatameshPendingReplicaTransitions[0].Transition.Type).To(Equal(v1alpha1.ReplicaTypeAccess))
		Expect(rv.Status.DatameshPendingReplicaTransitions[0].Message).To(BeEmpty())                      // Message cleared.
		Expect(rv.Status.DatameshPendingReplicaTransitions[0].FirstObservedAt.Time).NotTo(Equal(oldTime)) // Timestamp updated.
	})

	It("sorts unsorted existing entries but does not mark changed (sort-only is not a patch reason)", func(ctx SpecContext) {
		oldTime := time.Now().Add(-1 * time.Hour)
		pending := v1alpha1.ReplicatedVolumeReplicaStatusDatameshPendingTransition{
			Member: ptr.To(true),
			Type:   v1alpha1.ReplicaTypeDiskful,
		}
		// Entries are not sorted by name.
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshPendingReplicaTransitions: []v1alpha1.ReplicatedVolumeDatameshPendingReplicaTransition{
					mkRVEntry("rvr-2", pending, oldTime),
					mkRVEntry("rvr-1", pending, oldTime),
				},
			},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rvr-1", mkPending(v1alpha1.ReplicaTypeDiskful)),
			mkRVR("rvr-2", mkPending(v1alpha1.ReplicaTypeDiskful)),
		}

		outcome := ensureDatameshPendingReplicaTransitions(ctx, rv, rvrs)

		// Sort-only does not mark changed (order is semantically irrelevant for the API).
		Expect(outcome.DidChange()).To(BeFalse())
		Expect(rv.Status.DatameshPendingReplicaTransitions).To(HaveLen(2))
	})

	It("no change when already in sync (idempotent)", func(ctx SpecContext) {
		pending := v1alpha1.ReplicatedVolumeReplicaStatusDatameshPendingTransition{
			Member: ptr.To(true),
			Type:   v1alpha1.ReplicaTypeDiskful,
		}
		oldTime := time.Now().Add(-1 * time.Hour)
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshPendingReplicaTransitions: []v1alpha1.ReplicatedVolumeDatameshPendingReplicaTransition{
					{
						Name:            "rvr-1",
						Transition:      pending,
						FirstObservedAt: metav1.NewTime(oldTime),
					},
				},
			},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rvr-1", mkPending(v1alpha1.ReplicaTypeDiskful)),
		}

		outcome := ensureDatameshPendingReplicaTransitions(ctx, rv, rvrs)

		Expect(outcome.DidChange()).To(BeFalse())
		Expect(rv.Status.DatameshPendingReplicaTransitions).To(HaveLen(1))
		// FirstObservedAt should be preserved.
		Expect(rv.Status.DatameshPendingReplicaTransitions[0].FirstObservedAt.Time).To(Equal(oldTime))
	})

	It("handles multiple RVRs with mixed add/remove/update", func(ctx SpecContext) {
		oldTime := time.Now().Add(-1 * time.Hour)
		// Existing entries: rvr-1 (will update), rvr-2 (will remove), rvr-4 (will keep).
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshPendingReplicaTransitions: []v1alpha1.ReplicatedVolumeDatameshPendingReplicaTransition{
					mkRVEntry("rvr-1", v1alpha1.ReplicatedVolumeReplicaStatusDatameshPendingTransition{
						Member: ptr.To(true),
						Type:   v1alpha1.ReplicaTypeDiskful,
					}, oldTime),
					mkRVEntry("rvr-2", v1alpha1.ReplicatedVolumeReplicaStatusDatameshPendingTransition{
						Member: ptr.To(true),
						Type:   v1alpha1.ReplicaTypeDiskful,
					}, oldTime),
					mkRVEntry("rvr-4", v1alpha1.ReplicatedVolumeReplicaStatusDatameshPendingTransition{
						Member: ptr.To(true),
						Type:   v1alpha1.ReplicaTypeDiskful,
					}, oldTime),
				},
			},
		}
		// rvr-1: update role to Access.
		// rvr-2: nil transition (removed).
		// rvr-3: new entry (added).
		// rvr-4: unchanged.
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rvr-1", mkPending(v1alpha1.ReplicaTypeAccess)), // Update.
			mkRVR("rvr-2", nil), // Remove.
			mkRVR("rvr-3", mkPending(v1alpha1.ReplicaTypeTieBreaker)), // Add.
			mkRVR("rvr-4", mkPending(v1alpha1.ReplicaTypeDiskful)),    // Keep.
		}

		outcome := ensureDatameshPendingReplicaTransitions(ctx, rv, rvrs)

		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rv.Status.DatameshPendingReplicaTransitions).To(HaveLen(3))

		// Check rvr-1: updated.
		Expect(rv.Status.DatameshPendingReplicaTransitions[0].Name).To(Equal("rvr-1"))
		Expect(rv.Status.DatameshPendingReplicaTransitions[0].Transition.Type).To(Equal(v1alpha1.ReplicaTypeAccess))

		// Check rvr-3: added.
		Expect(rv.Status.DatameshPendingReplicaTransitions[1].Name).To(Equal("rvr-3"))
		Expect(rv.Status.DatameshPendingReplicaTransitions[1].Transition.Type).To(Equal(v1alpha1.ReplicaTypeTieBreaker))

		// Check rvr-4: kept (should have preserved timestamp).
		Expect(rv.Status.DatameshPendingReplicaTransitions[2].Name).To(Equal("rvr-4"))
		Expect(rv.Status.DatameshPendingReplicaTransitions[2].FirstObservedAt.Time).To(Equal(oldTime))
	})

	It("handles empty RVR list", func(ctx SpecContext) {
		oldTime := time.Now().Add(-1 * time.Hour)
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshPendingReplicaTransitions: []v1alpha1.ReplicatedVolumeDatameshPendingReplicaTransition{
					mkRVEntry("rvr-1", v1alpha1.ReplicatedVolumeReplicaStatusDatameshPendingTransition{
						Member: ptr.To(true),
						Type:   v1alpha1.ReplicaTypeDiskful,
					}, oldTime),
				},
			},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{}

		outcome := ensureDatameshPendingReplicaTransitions(ctx, rv, rvrs)

		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rv.Status.DatameshPendingReplicaTransitions).To(BeEmpty())
	})

	It("handles empty existing entries", func(ctx SpecContext) {
		rv := &v1alpha1.ReplicatedVolume{}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{}

		outcome := ensureDatameshPendingReplicaTransitions(ctx, rv, rvrs)

		Expect(outcome.DidChange()).To(BeFalse())
		Expect(rv.Status.DatameshPendingReplicaTransitions).To(BeEmpty())
	})

	It("skips RVRs with nil transition during merge", func(ctx SpecContext) {
		rv := &v1alpha1.ReplicatedVolume{}
		// Mixed: some with transition, some without.
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rvr-1", nil), // Skip.
			mkRVR("rvr-2", mkPending(v1alpha1.ReplicaTypeDiskful)), // Add.
			mkRVR("rvr-3", nil), // Skip.
			mkRVR("rvr-4", mkPending(v1alpha1.ReplicaTypeAccess)), // Add.
		}

		outcome := ensureDatameshPendingReplicaTransitions(ctx, rv, rvrs)

		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rv.Status.DatameshPendingReplicaTransitions).To(HaveLen(2))
		Expect(rv.Status.DatameshPendingReplicaTransitions[0].Name).To(Equal("rvr-2"))
		Expect(rv.Status.DatameshPendingReplicaTransitions[1].Name).To(Equal("rvr-4"))
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
					Members: []v1alpha1.ReplicatedVolumeDatameshMember{
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
					Members: []v1alpha1.ReplicatedVolumeDatameshMember{
						{Name: "rvr-1", Attached: false},
					},
				},
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach, ReplicaName: "rvr-1", StartedAt: now},
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
					Members: []v1alpha1.ReplicatedVolumeDatameshMember{
						{Name: "rvr-1", Attached: false},
					},
				},
			},
		}
		Expect(rvShouldNotExist(rv)).To(BeTrue())
	})
})

var _ = Describe("isFormationInProgress", func() {
	It("returns true with empty phase when DatameshRevision is 0", func() {
		rv := &v1alpha1.ReplicatedVolume{}
		forming, phase := isFormationInProgress(rv)
		Expect(forming).To(BeTrue())
		Expect(phase).To(BeEmpty())
	})

	It("returns true with phase when Formation transition exists", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshRevision: 2,
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					{
						Type:      v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation,
						Formation: &v1alpha1.ReplicatedVolumeDatameshTransitionFormation{Phase: v1alpha1.ReplicatedVolumeFormationPhaseEstablishConnectivity},
					},
				},
			},
		}
		forming, phase := isFormationInProgress(rv)
		Expect(forming).To(BeTrue())
		Expect(phase).To(Equal(v1alpha1.ReplicatedVolumeFormationPhaseEstablishConnectivity))
	})

	It("returns false when DatameshRevision > 0 and no Formation transition", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshRevision: 1,
			},
		}
		forming, _ := isFormationInProgress(rv)
		Expect(forming).To(BeFalse())
	})
})

var _ = Describe("computeIntendedDiskfulReplicaCount", func() {
	It("returns 1 for ReplicationNone", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
					Topology: v1alpha1.TopologyIgnored, Replication: v1alpha1.ReplicationNone,
					VolumeAccess: v1alpha1.VolumeAccessLocal, StoragePoolName: "test-pool",
				},
			},
		}
		Expect(computeIntendedDiskfulReplicaCount(rv)).To(Equal(1))
	})

	It("returns 2 for ReplicationAvailability", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
					Topology: v1alpha1.TopologyIgnored, Replication: v1alpha1.ReplicationAvailability,
					VolumeAccess: v1alpha1.VolumeAccessPreferablyLocal, StoragePoolName: "test-pool",
				},
			},
		}
		Expect(computeIntendedDiskfulReplicaCount(rv)).To(Equal(2))
	})

	It("returns 2 for ReplicationConsistency", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
					Topology: v1alpha1.TopologyIgnored, Replication: v1alpha1.ReplicationConsistency,
					VolumeAccess: v1alpha1.VolumeAccessPreferablyLocal, StoragePoolName: "test-pool",
				},
			},
		}
		Expect(computeIntendedDiskfulReplicaCount(rv)).To(Equal(2))
	})

	It("returns 3 for ReplicationConsistencyAndAvailability", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
					Topology: v1alpha1.TopologyIgnored, Replication: v1alpha1.ReplicationConsistencyAndAvailability,
					VolumeAccess: v1alpha1.VolumeAccessPreferablyLocal, StoragePoolName: "test-pool",
				},
			},
		}
		Expect(computeIntendedDiskfulReplicaCount(rv)).To(Equal(3))
	})
})

var _ = Describe("computeTargetQuorum", func() {
	mkRVWithMembers := func(replication v1alpha1.ReplicatedStorageClassReplication, diskfulCount int) *v1alpha1.ReplicatedVolume {
		members := make([]v1alpha1.ReplicatedVolumeDatameshMember, diskfulCount)
		for i := range diskfulCount {
			members[i] = v1alpha1.ReplicatedVolumeDatameshMember{
				Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", uint8(i)),
				Type: v1alpha1.ReplicaTypeDiskful,
			}
		}
		return &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
					Topology: v1alpha1.TopologyIgnored, Replication: replication,
					VolumeAccess: v1alpha1.VolumeAccessPreferablyLocal, StoragePoolName: "test-pool",
				},
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{Members: members},
			},
		}
	}

	It("returns q=1 qmr=1 for ReplicationNone with 1 member", func() {
		q, qmr := computeTargetQuorum(mkRVWithMembers(v1alpha1.ReplicationNone, 1))
		Expect(q).To(Equal(byte(1)))
		Expect(qmr).To(Equal(byte(1)))
	})

	It("returns q=2 qmr=2 for ReplicationAvailability with 2 members", func() {
		// quorum = 2/2+1 = 2; minQ=2, minQMR=1; q=max(2,2)=2, qmr=max(2,1)=2
		q, qmr := computeTargetQuorum(mkRVWithMembers(v1alpha1.ReplicationAvailability, 2))
		Expect(q).To(Equal(byte(2)))
		Expect(qmr).To(Equal(byte(2)))
	})

	It("returns q=2 qmr=2 for ReplicationConsistency with 2 members", func() {
		q, qmr := computeTargetQuorum(mkRVWithMembers(v1alpha1.ReplicationConsistency, 2))
		Expect(q).To(Equal(byte(2)))
		Expect(qmr).To(Equal(byte(2)))
	})

	It("returns q=2 qmr=2 for ReplicationConsistencyAndAvailability with 3 members", func() {
		q, qmr := computeTargetQuorum(mkRVWithMembers(v1alpha1.ReplicationConsistencyAndAvailability, 3))
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

var _ = Describe("generateSharedSecret", func() {
	It("returns non-empty string within DRBD limit", func() {
		secret, err := generateSharedSecret()
		Expect(err).NotTo(HaveOccurred())
		Expect(secret).NotTo(BeEmpty())
		Expect(len(secret)).To(BeNumerically("<=", 64))
	})
})

var _ = Describe("applyFormationTransition", func() {
	It("creates new transition and returns true", func() {
		rv := &v1alpha1.ReplicatedVolume{}
		changed := applyFormationTransition(rv, v1alpha1.ReplicatedVolumeFormationPhasePreconfigure)
		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation))
		Expect(rv.Status.DatameshTransitions[0].Formation.Phase).To(Equal(v1alpha1.ReplicatedVolumeFormationPhasePreconfigure))
	})

	It("returns false when phase is the same", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					{
						Type:      v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation,
						Formation: &v1alpha1.ReplicatedVolumeDatameshTransitionFormation{Phase: v1alpha1.ReplicatedVolumeFormationPhasePreconfigure},
					},
				},
			},
		}
		changed := applyFormationTransition(rv, v1alpha1.ReplicatedVolumeFormationPhasePreconfigure)
		Expect(changed).To(BeFalse())
	})

	It("updates phase and returns true when phase differs", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					{
						Type:      v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation,
						Formation: &v1alpha1.ReplicatedVolumeDatameshTransitionFormation{Phase: v1alpha1.ReplicatedVolumeFormationPhasePreconfigure},
					},
				},
			},
		}
		changed := applyFormationTransition(rv, v1alpha1.ReplicatedVolumeFormationPhaseEstablishConnectivity)
		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions[0].Formation.Phase).To(Equal(v1alpha1.ReplicatedVolumeFormationPhaseEstablishConnectivity))
	})
})

var _ = Describe("applyFormationTransitionMessage", func() {
	mkRVWithFormation := func() *v1alpha1.ReplicatedVolume {
		return &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					{
						Type:      v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation,
						Formation: &v1alpha1.ReplicatedVolumeDatameshTransitionFormation{Phase: v1alpha1.ReplicatedVolumeFormationPhasePreconfigure},
						Message:   "old message",
					},
				},
			},
		}
	}

	It("updates message and returns true", func() {
		rv := mkRVWithFormation()
		changed := applyFormationTransitionMessage(rv, "new message")
		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions[0].Message).To(Equal("new message"))
	})

	It("returns false when message is the same", func() {
		rv := mkRVWithFormation()
		changed := applyFormationTransitionMessage(rv, "old message")
		Expect(changed).To(BeFalse())
	})
})

var _ = Describe("applyFormationTransitionAbsent", func() {
	It("removes formation and returns true", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					{
						Type:      v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation,
						Formation: &v1alpha1.ReplicatedVolumeDatameshTransitionFormation{Phase: v1alpha1.ReplicatedVolumeFormationPhasePreconfigure},
					},
				},
			},
		}
		changed := applyFormationTransitionAbsent(rv)
		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})

	It("returns false when no formation exists", func() {
		rv := &v1alpha1.ReplicatedVolume{}
		changed := applyFormationTransitionAbsent(rv)
		Expect(changed).To(BeFalse())
	})
})

var _ = Describe("applyDatameshMember", func() {
	It("adds new member and returns true", func() {
		rv := &v1alpha1.ReplicatedVolume{}
		member := v1alpha1.ReplicatedVolumeDatameshMember{
			Name:     v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0),
			Type:     v1alpha1.ReplicaTypeDiskful,
			NodeName: "node-1",
		}
		changed := applyDatameshMember(rv, member)
		Expect(changed).To(BeTrue())
		Expect(rv.Status.Datamesh.Members).To(HaveLen(1))
		Expect(rv.Status.Datamesh.Members[0].NodeName).To(Equal("node-1"))
	})

	It("returns false when member data is identical", func() {
		member := v1alpha1.ReplicatedVolumeDatameshMember{
			Name:     v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0),
			Type:     v1alpha1.ReplicaTypeDiskful,
			NodeName: "node-1",
		}
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.ReplicatedVolumeDatameshMember{member},
				},
			},
		}
		changed := applyDatameshMember(rv, member)
		Expect(changed).To(BeFalse())
	})

	It("updates changed field and returns true", func() {
		existing := v1alpha1.ReplicatedVolumeDatameshMember{
			Name:     v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0),
			Type:     v1alpha1.ReplicaTypeDiskful,
			NodeName: "node-1",
		}
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.ReplicatedVolumeDatameshMember{existing},
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
					Members: []v1alpha1.ReplicatedVolumeDatameshMember{
						{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0), Type: v1alpha1.ReplicaTypeDiskful},
						{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 1), Type: v1alpha1.ReplicaTypeDiskful},
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
					Members: []v1alpha1.ReplicatedVolumeDatameshMember{
						{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0), Type: v1alpha1.ReplicaTypeDiskful},
						{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 1), Type: v1alpha1.ReplicaTypeDiskful},
						{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 2), Type: v1alpha1.ReplicaTypeDiskful},
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

var _ = Describe("applyPendingReplicaMessages", func() {
	It("updates message for matching node and returns true", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshPendingReplicaTransitions: []v1alpha1.ReplicatedVolumeDatameshPendingReplicaTransition{
					{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0), Message: "old"},
					{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 1), Message: "other"},
				},
			},
		}
		var ids idset.IDSet
		ids.Add(0)
		changed := applyPendingReplicaMessages(rv, ids, "new")
		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshPendingReplicaTransitions[0].Message).To(Equal("new"))
		Expect(rv.Status.DatameshPendingReplicaTransitions[1].Message).To(Equal("other"))
	})

	It("returns false when message is already the same", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshPendingReplicaTransitions: []v1alpha1.ReplicatedVolumeDatameshPendingReplicaTransition{
					{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0), Message: "same"},
				},
			},
		}
		var ids idset.IDSet
		ids.Add(0)
		changed := applyPendingReplicaMessages(rv, ids, "same")
		Expect(changed).To(BeFalse())
	})

	It("returns false when no nodes match", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshPendingReplicaTransitions: []v1alpha1.ReplicatedVolumeDatameshPendingReplicaTransition{
					{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0), Message: "msg"},
				},
			},
		}
		var ids idset.IDSet
		ids.Add(5) // ID 5 does not match any entry.
		changed := applyPendingReplicaMessages(rv, ids, "new")
		Expect(changed).To(BeFalse())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Formation: Preconfigure (normal path, safety checks, excess removal)
//

var _ = Describe("Formation: Preconfigure", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	})

	// newFormationRV creates an RV in formation state (DatameshRevision=0, finalizer/labels set).
	//nolint:unparam // rscName is always "rsc-1" in current tests, but kept as param for future extensibility.
	newFormationRV := func(rscName string) *v1alpha1.ReplicatedVolume {
		return &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "rv-1",
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
				Labels: map[string]string{
					v1alpha1.ReplicatedStorageClassLabelKey: rscName,
				},
			},
			Spec: v1alpha1.ReplicatedVolumeSpec{
				Size:                       resource.MustParse("10Gi"),
				ReplicatedStorageClassName: rscName,
			},
			// DatameshRevision defaults to 0 → formation in progress.
		}
	}

	// newPreconfiguredRVR creates an RVR that is fully preconfigured:
	// scheduled, DRBDConfigured=PendingDatameshJoin, pending transition member=true.
	//nolint:unparam // rvName is always "rv-1" in current tests, but kept as param for future extensibility.
	newPreconfiguredRVR := func(rvName string, id uint8, nodeName string) *v1alpha1.ReplicatedVolumeReplica {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name:       v1alpha1.FormatReplicatedVolumeReplicaName(rvName, id),
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: rvName,
				Type:                 v1alpha1.ReplicaTypeDiskful,
				NodeName:             nodeName,
				LVMVolumeGroupName:   "lvg-1",
			},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				Addresses: []v1alpha1.DRBDResourceAddressStatus{
					{SystemNetworkName: "Internal"},
				},
				BackingVolume: &v1alpha1.ReplicatedVolumeReplicaStatusBackingVolume{
					Size:  ptr.To(resource.MustParse("11Gi")),
					State: v1alpha1.DiskStateInconsistent,
				},
				DatameshPendingTransition: &v1alpha1.ReplicatedVolumeReplicaStatusDatameshPendingTransition{
					Member:             ptr.To(true),
					Type:               v1alpha1.ReplicaTypeDiskful,
					LVMVolumeGroupName: "lvg-1",
				},
			},
		}
		// Mark as scheduled.
		obju.SetStatusCondition(rvr, metav1.Condition{
			Type:   v1alpha1.ReplicatedVolumeReplicaCondScheduledType,
			Status: metav1.ConditionTrue,
			Reason: v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonScheduled,
		})
		// Mark as preconfigured (DRBDConfigured with PendingDatameshJoin).
		obju.SetStatusCondition(rvr, metav1.Condition{
			Type:   v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType,
			Status: metav1.ConditionTrue,
			Reason: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonPendingDatameshJoin,
		})
		// Mark as on eligible nodes.
		obju.SetStatusCondition(rvr, metav1.Condition{
			Type:   v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType,
			Status: metav1.ConditionTrue,
			Reason: v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonSatisfied,
		})
		return rvr
	}

	//nolint:unparam // name is always "test-pool" in current tests, but kept as param for future extensibility.
	newTestRSPWithNodes := func(name string, nodeNames ...string) *v1alpha1.ReplicatedStoragePool {
		rsp := newTestRSP(name)
		rsp.Status.EligibleNodes = make([]v1alpha1.ReplicatedStoragePoolEligibleNode, len(nodeNames))
		for i, nn := range nodeNames {
			rsp.Status.EligibleNodes[i] = v1alpha1.ReplicatedStoragePoolEligibleNode{
				NodeName: nn,
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
					{Name: "lvg-1"},
				},
			}
		}
		return rsp
	}

	It("creates diskful RVR when no replicas exist (normal path)", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1") // ReplicationNone → 1 diskful
		rsp := newTestRSPWithNodes("test-pool", "node-1")
		rv := newFormationRV("rsc-1")

		rvrCreateCalled := false
		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp).
			WithStatusSubresource(rv, rsc).
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

		_, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())
		Expect(rvrCreateCalled).To(BeTrue(), "should create diskful RVR")

		// Verify RVR was created with correct spec.
		var rvrList v1alpha1.ReplicatedVolumeReplicaList
		Expect(cl.List(ctx, &rvrList)).To(Succeed())
		Expect(rvrList.Items).To(HaveLen(1))
		Expect(rvrList.Items[0].Spec.ReplicatedVolumeName).To(Equal("rv-1"))
		Expect(rvrList.Items[0].Spec.Type).To(Equal(v1alpha1.ReplicaTypeDiskful))
	})

	It("waits for scheduling when RVR is not yet scheduled", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1")
		rsp := newTestRSPWithNodes("test-pool", "node-1")
		rv := newFormationRV("rsc-1")

		// Create an unscheduled RVR (no Scheduled condition).
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name:       v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0),
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1",
				Type:                 v1alpha1.ReplicaTypeDiskful,
			},
		}

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr).
			WithStatusSubresource(rv, rsc, rvr).
			Build()
		rec := NewReconciler(cl, scheme)

		result, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())
		// Should requeue (formation timeout).
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
		Expect(updated.Status.DatameshTransitions).To(HaveLen(1))
		Expect(updated.Status.DatameshTransitions[0].Message).To(ContainSubstring("pending scheduling"))
	})

	It("includes scheduling failure details when RVR has Scheduled=False", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1")
		rsp := newTestRSPWithNodes("test-pool", "node-1")
		rv := newFormationRV("rsc-1")

		// Create an RVR with Scheduled=False and a diagnostic message.
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name:       v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0),
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1",
				Type:                 v1alpha1.ReplicaTypeDiskful,
			},
		}
		obju.SetStatusCondition(rvr, metav1.Condition{
			Type:    v1alpha1.ReplicatedVolumeReplicaCondScheduledType,
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonSchedulingFailed,
			Message: "2 candidates; 2 excluded: node not ready",
		})

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr).
			WithStatusSubresource(rv, rsc, rvr).
			Build()
		rec := NewReconciler(cl, scheme)

		result, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
		Expect(updated.Status.DatameshTransitions).To(HaveLen(1))
		Expect(updated.Status.DatameshTransitions[0].Message).To(ContainSubstring("scheduling failed [#0]"))
		Expect(updated.Status.DatameshTransitions[0].Message).To(ContainSubstring("(2 candidates; 2 excluded: node not ready)"))
	})

	It("waits for preconfiguration when RVR is scheduled but not preconfigured", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1")
		rsp := newTestRSPWithNodes("test-pool", "node-1")
		rv := newFormationRV("rsc-1")

		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name:       v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0),
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1",
				Type:                 v1alpha1.ReplicaTypeDiskful,
				NodeName:             "node-1",
				LVMVolumeGroupName:   "lvg-1",
			},
		}
		// Mark as scheduled but NOT preconfigured.
		obju.SetStatusCondition(rvr, metav1.Condition{
			Type:   v1alpha1.ReplicatedVolumeReplicaCondScheduledType,
			Status: metav1.ConditionTrue,
			Reason: v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonScheduled,
		})
		obju.SetStatusCondition(rvr, metav1.Condition{
			Type:   v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType,
			Status: metav1.ConditionTrue,
			Reason: v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonSatisfied,
		})

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr).
			WithStatusSubresource(rv, rsc, rvr).
			Build()
		rec := NewReconciler(cl, scheme)

		result, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
		Expect(updated.Status.DatameshTransitions).To(HaveLen(1))
		Expect(updated.Status.DatameshTransitions[0].Message).To(ContainSubstring("preconfiguring"))
	})

	It("transitions to establish-connectivity when all replicas are preconfigured", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1")
		rsp := newTestRSPWithNodes("test-pool", "node-1")
		rv := newFormationRV("rsc-1")

		rvr := newPreconfiguredRVR("rv-1", 0, "node-1")

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr).
			WithStatusSubresource(rv, rsc, rvr).
			Build()
		rec := NewReconciler(cl, scheme)

		_, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())

		// Should have shared secret set and members added → establish-connectivity phase.
		// DatameshRevision: set to 1 in preconfigure, then incremented to 2 in establish-connectivity.
		Expect(updated.Status.Datamesh.SharedSecret).NotTo(BeEmpty())
		Expect(updated.Status.Datamesh.Members).To(HaveLen(1))
		Expect(updated.Status.DatameshRevision).To(Equal(int64(2)))
	})

	It("removes excess replicas preferring less-progressed ones", func(ctx SpecContext) {
		// ReplicationNone → wants 1 diskful, but we have 2.
		rsc := newRSCWithConfiguration("rsc-1")
		rsp := newTestRSPWithNodes("test-pool", "node-1", "node-2")
		rv := newFormationRV("rsc-1")

		rvr0 := newPreconfiguredRVR("rv-1", 0, "node-1")
		// rvr1 is scheduled but NOT preconfigured → less progressed, should be removed.
		rvr1 := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 1),
				Finalizers: []string{
					v1alpha1.RVControllerFinalizer,
					v1alpha1.RVRControllerFinalizer,
				},
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1",
				Type:                 v1alpha1.ReplicaTypeDiskful,
				NodeName:             "node-2",
				LVMVolumeGroupName:   "lvg-1",
			},
		}
		obju.SetStatusCondition(rvr1, metav1.Condition{
			Type:   v1alpha1.ReplicatedVolumeReplicaCondScheduledType,
			Status: metav1.ConditionTrue,
			Reason: v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonScheduled,
		})
		obju.SetStatusCondition(rvr1, metav1.Condition{
			Type:   v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType,
			Status: metav1.ConditionTrue,
			Reason: v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonSatisfied,
		})

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr0, rvr1).
			WithStatusSubresource(rv, rsc, rvr0, rvr1).
			Build()
		rec := NewReconciler(cl, scheme)

		_, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())

		// rvr1 (less progressed, higher ID) should be deleted.
		var updatedRVR1 v1alpha1.ReplicatedVolumeReplica
		err = cl.Get(ctx, client.ObjectKeyFromObject(rvr1), &updatedRVR1)
		if err == nil {
			Expect(updatedRVR1.DeletionTimestamp).NotTo(BeNil(), "excess RVR should be deleted")
		}
		// rvr0 should still exist.
		var updatedRVR0 v1alpha1.ReplicatedVolumeReplica
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr0), &updatedRVR0)).To(Succeed())
	})

	It("detects address mismatch and waits with message", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1")
		rsp := newTestRSPWithNodes("test-pool", "node-1")
		rv := newFormationRV("rsc-1")

		rvr := newPreconfiguredRVR("rv-1", 0, "node-1")
		// Remove addresses → address mismatch.
		rvr.Status.Addresses = nil

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr).
			WithStatusSubresource(rv, rsc, rvr).
			Build()
		rec := NewReconciler(cl, scheme)

		result, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
		Expect(updated.Status.DatameshTransitions).To(HaveLen(1))
		Expect(updated.Status.DatameshTransitions[0].Message).To(ContainSubstring("Address configuration mismatch"))
	})

	It("detects replicas not on eligible nodes", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1")
		// RSP has "node-2" as eligible, but RVR is on "node-1".
		rsp := newTestRSPWithNodes("test-pool", "node-2")
		rv := newFormationRV("rsc-1")

		rvr := newPreconfiguredRVR("rv-1", 0, "node-1")

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr).
			WithStatusSubresource(rv, rsc, rvr).
			Build()
		rec := NewReconciler(cl, scheme)

		result, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
		Expect(updated.Status.DatameshTransitions[0].Message).To(ContainSubstring("not in eligible nodes"))
	})

	It("detects spec mismatch with pending transition", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1")
		rsp := newTestRSPWithNodes("test-pool", "node-1")
		rv := newFormationRV("rsc-1")

		rvr := newPreconfiguredRVR("rv-1", 0, "node-1")
		// Create spec mismatch: RVR spec says lvg-1, but pending transition says lvg-2.
		rvr.Status.DatameshPendingTransition.LVMVolumeGroupName = "lvg-2"

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr).
			WithStatusSubresource(rv, rsc, rvr).
			Build()
		rec := NewReconciler(cl, scheme)

		result, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
		Expect(updated.Status.DatameshTransitions[0].Message).To(ContainSubstring("spec changes"))
	})

	It("detects insufficient backing volume size", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1")
		rsp := newTestRSPWithNodes("test-pool", "node-1")
		rv := newFormationRV("rsc-1")

		rvr := newPreconfiguredRVR("rv-1", 0, "node-1")
		// Set tiny backing volume size that cannot fit datamesh size.
		rvr.Status.BackingVolume.Size = ptr.To(resource.MustParse("1Ki"))

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr).
			WithStatusSubresource(rv, rsc, rvr).
			Build()
		rec := NewReconciler(cl, scheme)

		result, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
		Expect(updated.Status.DatameshTransitions[0].Message).To(ContainSubstring("insufficient backing volume size"))
	})

	It("returns error when createRVR fails with non-AlreadyExists error", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1") // ReplicationNone → 1 diskful
		rsp := newTestRSPWithNodes("test-pool", "node-1")
		rv := newFormationRV("rsc-1")

		testErr := errors.New("create failed")
		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp).
			WithStatusSubresource(rv, rsc).
			WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					if _, ok := obj.(*v1alpha1.ReplicatedVolumeReplica); ok {
						return testErr
					}
					return cl.Create(ctx, obj, opts...)
				},
			}).
			Build()
		rec := NewReconciler(cl, scheme)

		_, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, testErr)).To(BeTrue())
	})

	It("requeues when createRVR returns AlreadyExists", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1") // ReplicationNone → 1 diskful
		rsp := newTestRSPWithNodes("test-pool", "node-1")
		rv := newFormationRV("rsc-1")

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp).
			WithStatusSubresource(rv, rsc).
			WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					if _, ok := obj.(*v1alpha1.ReplicatedVolumeReplica); ok {
						return apierrors.NewAlreadyExists(
							schema.GroupResource{Group: v1alpha1.SchemeGroupVersion.Group, Resource: "replicatedvolumereplicas"},
							"rvr-exists",
						)
					}
					return cl.Create(ctx, obj, opts...)
				},
			}).
			Build()
		rec := NewReconciler(cl, scheme)

		result, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())
		// AlreadyExists → DoneAndRequeue.
		Expect(result.Requeue).To(BeTrue()) //nolint:staticcheck // Requeue field is set by flow.DoneAndRequeue/ContinueAndRequeue
	})

	It("removes excess unscheduled replicas first (not-scheduled priority)", func(ctx SpecContext) {
		// ReplicationNone → wants 1 diskful, but we have 2: one preconfigured, one unscheduled.
		rsc := newRSCWithConfiguration("rsc-1")
		rsp := newTestRSPWithNodes("test-pool", "node-1", "node-2")
		rv := newFormationRV("rsc-1")

		rvr0 := newPreconfiguredRVR("rv-1", 0, "node-1")
		// rvr1 is NOT scheduled at all → least progressed, should be removed first.
		rvr1 := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 1),
				Finalizers: []string{
					v1alpha1.RVControllerFinalizer,
					v1alpha1.RVRControllerFinalizer,
				},
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1",
				Type:                 v1alpha1.ReplicaTypeDiskful,
				// Not scheduled: no NodeName, no Scheduled condition.
			},
		}
		obju.SetStatusCondition(rvr1, metav1.Condition{
			Type:   v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType,
			Status: metav1.ConditionTrue,
			Reason: v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonSatisfied,
		})

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr0, rvr1).
			WithStatusSubresource(rv, rsc, rvr0, rvr1).
			Build()
		rec := NewReconciler(cl, scheme)

		_, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())

		// rvr1 (unscheduled, higher ID) should be deleted.
		var updatedRVR1 v1alpha1.ReplicatedVolumeReplica
		err = cl.Get(ctx, client.ObjectKeyFromObject(rvr1), &updatedRVR1)
		if err == nil {
			Expect(updatedRVR1.DeletionTimestamp).NotTo(BeNil(), "unscheduled excess RVR should be deleted")
		}
		// rvr0 (preconfigured) should still exist.
		var updatedRVR0 v1alpha1.ReplicatedVolumeReplica
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr0), &updatedRVR0)).To(Succeed())
	})

	It("removes excess replicas with highest ID when all equally progressed", func(ctx SpecContext) {
		// ReplicationNone → wants 1 diskful, but we have 2 preconfigured replicas.
		// Both are fully preconfigured → "any" fallback → remove highest ID.
		rsc := newRSCWithConfiguration("rsc-1")
		rsp := newTestRSPWithNodes("test-pool", "node-1", "node-2")
		rv := newFormationRV("rsc-1")

		rvr0 := newPreconfiguredRVR("rv-1", 0, "node-1")
		rvr1 := newPreconfiguredRVR("rv-1", 1, "node-2")
		// Both have RVRControllerFinalizer to keep them around after Delete.
		rvr1.Finalizers = append(rvr1.Finalizers, v1alpha1.RVRControllerFinalizer)

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr0, rvr1).
			WithStatusSubresource(rv, rsc, rvr0, rvr1).
			Build()
		rec := NewReconciler(cl, scheme)

		_, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())

		// rvr1 (higher ID) should be deleted even though both equally progressed.
		var updatedRVR1 v1alpha1.ReplicatedVolumeReplica
		err = cl.Get(ctx, client.ObjectKeyFromObject(rvr1), &updatedRVR1)
		if err == nil {
			Expect(updatedRVR1.DeletionTimestamp).NotTo(BeNil(), "higher ID excess RVR should be deleted")
		}
		// rvr0 (lower ID) should still exist.
		var updatedRVR0 v1alpha1.ReplicatedVolumeReplica
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr0), &updatedRVR0)).To(Succeed())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Formation: EstablishConnectivity
//

var _ = Describe("Formation: EstablishConnectivity", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	})

	// newRVInEstablishConnectivity creates an RV that has entered establish-connectivity:
	// members added, shared secret set, DatameshRevision=1.
	newRVInEstablishConnectivity := func() *v1alpha1.ReplicatedVolume {
		return &v1alpha1.ReplicatedVolume{
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
				Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
					Topology:        v1alpha1.TopologyIgnored,
					Replication:     v1alpha1.ReplicationNone,
					VolumeAccess:    v1alpha1.VolumeAccessLocal,
					StoragePoolName: "test-pool",
				},
				DatameshRevision: 1,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					SharedSecret:            "test-secret",
					SharedSecretAlg:         v1alpha1.SharedSecretAlgSHA256,
					SystemNetworkNames:      []string{"Internal"},
					Size:                    resource.MustParse("10Gi"),
					Quorum:                  1,
					QuorumMinimumRedundancy: 1,
					Members: []v1alpha1.ReplicatedVolumeDatameshMember{
						{
							Name:               v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0),
							Type:               v1alpha1.ReplicaTypeDiskful,
							NodeName:           "node-1",
							Addresses:          []v1alpha1.DRBDResourceAddressStatus{{SystemNetworkName: "Internal"}},
							LVMVolumeGroupName: "lvg-1",
						},
					},
				},
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					{
						Type:      v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation,
						StartedAt: metav1.Now(),
						Formation: &v1alpha1.ReplicatedVolumeDatameshTransitionFormation{
							Phase: v1alpha1.ReplicatedVolumeFormationPhaseEstablishConnectivity,
						},
					},
				},
			},
		}
	}

	It("waits for replicas to be configured for current revision", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1")
		rsp := newTestRSP("test-pool")
		rsp.Status.EligibleNodes = []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1", LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{{Name: "lvg-1"}}},
		}
		rv := newRVInEstablishConnectivity()

		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name:       v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0),
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1",
				Type:                 v1alpha1.ReplicaTypeDiskful,
				NodeName:             "node-1",
				LVMVolumeGroupName:   "lvg-1",
			},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshRevision: 0, // Not yet configured for revision 1.
			},
		}

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr).
			WithStatusSubresource(rv, rsc, rvr).
			Build()
		rec := NewReconciler(cl, scheme)

		result, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
		Expect(updated.Status.DatameshTransitions[0].Message).To(ContainSubstring("fully configured"))
	})

	It("waits for replicas to establish connections", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1")
		rsp := newTestRSP("test-pool")
		rsp.Status.EligibleNodes = []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1", LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{{Name: "lvg-1"}}},
		}

		// Use a 2-replica setup to test connection checks.
		rv := newRVInEstablishConnectivity()
		rv.Status.Configuration.Replication = v1alpha1.ReplicationAvailability
		rv.Status.Datamesh.Members = append(rv.Status.Datamesh.Members, v1alpha1.ReplicatedVolumeDatameshMember{
			Name:               v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 1),
			Type:               v1alpha1.ReplicaTypeDiskful,
			NodeName:           "node-2",
			Addresses:          []v1alpha1.DRBDResourceAddressStatus{{SystemNetworkName: "Internal"}},
			LVMVolumeGroupName: "lvg-1",
		})
		rv.Status.Datamesh.Quorum = 2
		rv.Status.Datamesh.QuorumMinimumRedundancy = 2
		rsp.Status.EligibleNodes = append(rsp.Status.EligibleNodes, v1alpha1.ReplicatedStoragePoolEligibleNode{
			NodeName: "node-2", LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{{Name: "lvg-1"}},
		})

		// Both RVRs configured but not connected.
		rvr0 := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name:       v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0),
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1", Type: v1alpha1.ReplicaTypeDiskful,
				NodeName: "node-1", LVMVolumeGroupName: "lvg-1",
			},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshRevision: 1,
				Peers: []v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{
					{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 1), Type: v1alpha1.ReplicaTypeDiskful, ConnectionState: v1alpha1.ConnectionStateConnecting},
				},
			},
		}
		obju.SetStatusCondition(rvr0, metav1.Condition{
			Type: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType, Status: metav1.ConditionTrue,
			Reason: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonConfigured,
		})

		rvr1 := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name:       v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 1),
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1", Type: v1alpha1.ReplicaTypeDiskful,
				NodeName: "node-2", LVMVolumeGroupName: "lvg-1",
			},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshRevision: 1,
				Peers: []v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{
					{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0), Type: v1alpha1.ReplicaTypeDiskful, ConnectionState: v1alpha1.ConnectionStateConnecting},
				},
			},
		}
		obju.SetStatusCondition(rvr1, metav1.Condition{
			Type: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType, Status: metav1.ConditionTrue,
			Reason: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonConfigured,
		})

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr0, rvr1).
			WithStatusSubresource(rv, rsc, rvr0, rvr1).
			Build()
		rec := NewReconciler(cl, scheme)

		result, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
		Expect(updated.Status.DatameshTransitions[0].Message).To(ContainSubstring("establish connections"))
	})

	It("detects datamesh members mismatch with active replicas", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1")
		rsp := newTestRSP("test-pool")
		rsp.Status.EligibleNodes = []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1", LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{{Name: "lvg-1"}}},
		}
		rv := newRVInEstablishConnectivity()
		// rv has member for ID 0, but we add a member for ID 1 that has no matching RVR.
		rv.Status.Datamesh.Members = append(rv.Status.Datamesh.Members, v1alpha1.ReplicatedVolumeDatameshMember{
			Name:               v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 1),
			Type:               v1alpha1.ReplicaTypeDiskful,
			NodeName:           "node-2",
			Addresses:          []v1alpha1.DRBDResourceAddressStatus{{SystemNetworkName: "Internal"}},
			LVMVolumeGroupName: "lvg-1",
		})

		// Only one active RVR (ID 0), but datamesh has members for 0 and 1.
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name:       v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0),
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1", Type: v1alpha1.ReplicaTypeDiskful,
				NodeName: "node-1", LVMVolumeGroupName: "lvg-1",
			},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{DatameshRevision: 1},
		}

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr).
			WithStatusSubresource(rv, rsc, rvr).
			Build()
		rec := NewReconciler(cl, scheme)

		result, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
		Expect(updated.Status.DatameshTransitions[0].Message).To(ContainSubstring("Datamesh members mismatch"))
	})

	It("waits for replicas to be ready for data bootstrap", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1")
		rsp := newTestRSP("test-pool")
		rsp.Status.EligibleNodes = []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1", LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{{Name: "lvg-1"}}},
		}
		rv := newRVInEstablishConnectivity()

		// RVR is configured, connected, but backing volume is UpToDate (not Inconsistent).
		// readyForDataBootstrap requires Inconsistent + Established replication.
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name:       v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0),
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1", Type: v1alpha1.ReplicaTypeDiskful,
				NodeName: "node-1", LVMVolumeGroupName: "lvg-1",
			},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshRevision: 1,
				BackingVolume: &v1alpha1.ReplicatedVolumeReplicaStatusBackingVolume{
					Size:  ptr.To(resource.MustParse("11Gi")),
					State: v1alpha1.DiskStateUpToDate, // Not Inconsistent → not ready for bootstrap.
				},
			},
		}
		obju.SetStatusCondition(rvr, metav1.Condition{
			Type: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType, Status: metav1.ConditionTrue,
			Reason: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonConfigured,
		})

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr).
			WithStatusSubresource(rv, rsc, rvr).
			Build()
		rec := NewReconciler(cl, scheme)

		result, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
		Expect(updated.Status.DatameshTransitions[0].Message).To(ContainSubstring("ready for data bootstrap"))
	})

	It("transitions to bootstrap-data when single replica is ready", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1")
		rsp := newTestRSP("test-pool")
		rsp.Status.EligibleNodes = []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1", LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{{Name: "lvg-1"}}},
		}
		rv := newRVInEstablishConnectivity()

		// Single replica: configured, connected, backing volume Inconsistent.
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name:       v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0),
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1", Type: v1alpha1.ReplicaTypeDiskful,
				NodeName: "node-1", LVMVolumeGroupName: "lvg-1",
			},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshRevision: 1,
				BackingVolume: &v1alpha1.ReplicatedVolumeReplicaStatusBackingVolume{
					Size:  ptr.To(resource.MustParse("11Gi")),
					State: v1alpha1.DiskStateInconsistent,
				},
			},
		}
		obju.SetStatusCondition(rvr, metav1.Condition{
			Type: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType, Status: metav1.ConditionTrue,
			Reason: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonConfigured,
		})

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr).
			WithStatusSubresource(rv, rsc, rvr).
			Build()
		rec := NewReconciler(cl, scheme)

		_, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())

		// After passing all connectivity checks, it should transition to bootstrap-data
		// and create the DRBDResourceOperation.
		var drbdrOp v1alpha1.DRBDResourceOperation
		Expect(cl.Get(ctx, client.ObjectKey{Name: "rv-1-formation"}, &drbdrOp)).To(Succeed())
		Expect(drbdrOp.Spec.Type).To(Equal(v1alpha1.DRBDResourceOperationCreateNewUUID))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Formation: BootstrapData
//

var _ = Describe("Formation: BootstrapData", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	})

	// formationStartedAt is a fixed time used as the formation StartedAt in bootstrap-data tests.
	// Must be recent enough that the formation timeout (1 min base) has NOT passed.
	// DRBDResourceOperation objects in these tests must have CreationTimestamp after this time
	// to not be considered stale by the reconciler.
	formationStartedAt := metav1.NewTime(time.Now().Add(-5 * time.Second))

	newRVInBootstrapData := func() *v1alpha1.ReplicatedVolume {
		return &v1alpha1.ReplicatedVolume{
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
				Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
					Topology: v1alpha1.TopologyIgnored, Replication: v1alpha1.ReplicationNone,
					VolumeAccess: v1alpha1.VolumeAccessLocal, StoragePoolName: "test-pool",
				},
				DatameshRevision: 1,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					SharedSecret: "test-secret", SharedSecretAlg: v1alpha1.SharedSecretAlgSHA256,
					SystemNetworkNames: []string{"Internal"}, Size: resource.MustParse("10Gi"),
					Quorum: 1, QuorumMinimumRedundancy: 1,
					Members: []v1alpha1.ReplicatedVolumeDatameshMember{
						{
							Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0),
							Type: v1alpha1.ReplicaTypeDiskful, NodeName: "node-1",
							Addresses:          []v1alpha1.DRBDResourceAddressStatus{{SystemNetworkName: "Internal"}},
							LVMVolumeGroupName: "lvg-1",
						},
					},
				},
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					{
						Type:      v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation,
						StartedAt: formationStartedAt,
						Formation: &v1alpha1.ReplicatedVolumeDatameshTransitionFormation{
							Phase: v1alpha1.ReplicatedVolumeFormationPhaseBootstrapData,
						},
					},
				},
			},
		}
	}

	newConfiguredConnectedRVR := func() *v1alpha1.ReplicatedVolumeReplica {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name:       v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0),
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1", Type: v1alpha1.ReplicaTypeDiskful,
				NodeName: "node-1", LVMVolumeGroupName: "lvg-1",
			},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshRevision: 1,
				BackingVolume: &v1alpha1.ReplicatedVolumeReplicaStatusBackingVolume{
					Size:  ptr.To(resource.MustParse("11Gi")),
					State: v1alpha1.DiskStateInconsistent,
				},
			},
		}
		obju.SetStatusCondition(rvr, metav1.Condition{
			Type: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType, Status: metav1.ConditionTrue,
			Reason: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonConfigured,
		})
		return rvr
	}

	It("creates DRBDResourceOperation for data bootstrap", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1")
		rsp := newTestRSP("test-pool")
		rsp.Status.EligibleNodes = []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1", LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{{Name: "lvg-1"}}},
		}
		rv := newRVInBootstrapData()
		rvr := newConfiguredConnectedRVR()

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr).
			WithStatusSubresource(rv, rsc, rvr).
			Build()
		rec := NewReconciler(cl, scheme)

		_, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())

		// Verify DRBDResourceOperation was created.
		var drbdrOp v1alpha1.DRBDResourceOperation
		Expect(cl.Get(ctx, client.ObjectKey{Name: "rv-1-formation"}, &drbdrOp)).To(Succeed())
		Expect(drbdrOp.Spec.Type).To(Equal(v1alpha1.DRBDResourceOperationCreateNewUUID))
		// Single replica + LVM → clear-bitmap (no force-resync).
		Expect(drbdrOp.Spec.CreateNewUUID.ClearBitmap).To(BeTrue())
		Expect(drbdrOp.Spec.CreateNewUUID.ForceResync).To(BeFalse())
	})

	It("completes formation when DRBDResourceOperation succeeds and replicas are UpToDate", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1")
		rsp := newTestRSP("test-pool")
		rsp.Status.EligibleNodes = []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1", LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{{Name: "lvg-1"}}},
		}
		rv := newRVInBootstrapData()

		rvr := newConfiguredConnectedRVR()
		rvr.Status.BackingVolume.State = v1alpha1.DiskStateUpToDate

		// Pre-create a succeeded DRBDResourceOperation (after formation started).
		drbdrOp := &v1alpha1.DRBDResourceOperation{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "rv-1-formation",
				CreationTimestamp: metav1.NewTime(formationStartedAt.Add(1 * time.Second)),
			},
			Spec: v1alpha1.DRBDResourceOperationSpec{
				DRBDResourceName: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0),
				Type:             v1alpha1.DRBDResourceOperationCreateNewUUID,
				CreateNewUUID:    &v1alpha1.CreateNewUUIDParams{ClearBitmap: true},
			},
			Status: v1alpha1.DRBDResourceOperationStatus{
				Phase: v1alpha1.DRBDOperationPhaseSucceeded,
			},
		}

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr, drbdrOp).
			WithStatusSubresource(rv, rsc, rvr, drbdrOp).
			Build()
		rec := NewReconciler(cl, scheme)

		_, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
		// Formation should be removed (completed).
		Expect(updated.Status.DatameshTransitions).To(BeEmpty())
	})

	It("waits when DRBDResourceOperation is still pending", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1")
		rsp := newTestRSP("test-pool")
		rsp.Status.EligibleNodes = []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1", LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{{Name: "lvg-1"}}},
		}
		rv := newRVInBootstrapData()
		rvr := newConfiguredConnectedRVR()

		drbdrOp := &v1alpha1.DRBDResourceOperation{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "rv-1-formation",
				CreationTimestamp: metav1.NewTime(formationStartedAt.Add(1 * time.Second)),
			},
			Spec: v1alpha1.DRBDResourceOperationSpec{
				DRBDResourceName: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0),
				Type:             v1alpha1.DRBDResourceOperationCreateNewUUID,
				CreateNewUUID:    &v1alpha1.CreateNewUUIDParams{ClearBitmap: true},
			},
			Status: v1alpha1.DRBDResourceOperationStatus{
				Phase: v1alpha1.DRBDOperationPhasePending,
			},
		}

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr, drbdrOp).
			WithStatusSubresource(rv, rsc, rvr, drbdrOp).
			Build()
		rec := NewReconciler(cl, scheme)

		result, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
		Expect(updated.Status.DatameshTransitions[0].Message).To(ContainSubstring("waiting for operation"))
	})

	It("restarts formation when DRBDResourceOperation fails", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1")
		rsp := newTestRSP("test-pool")
		rsp.Status.EligibleNodes = []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1", LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{{Name: "lvg-1"}}},
		}
		rv := newRVInBootstrapData()
		rvr := newConfiguredConnectedRVR()

		drbdrOp := &v1alpha1.DRBDResourceOperation{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "rv-1-formation",
				CreationTimestamp: metav1.NewTime(formationStartedAt.Add(1 * time.Second)),
			},
			Spec: v1alpha1.DRBDResourceOperationSpec{
				DRBDResourceName: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0),
				Type:             v1alpha1.DRBDResourceOperationCreateNewUUID,
				CreateNewUUID:    &v1alpha1.CreateNewUUIDParams{ClearBitmap: true},
			},
			Status: v1alpha1.DRBDResourceOperationStatus{
				Phase:   v1alpha1.DRBDOperationPhaseFailed,
				Message: "some error",
			},
		}

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr, drbdrOp).
			WithStatusSubresource(rv, rsc, rvr, drbdrOp).
			Build()
		rec := NewReconciler(cl, scheme)

		result, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
		Expect(updated.Status.DatameshTransitions[0].Message).To(ContainSubstring("failed"))
	})

	It("deletes stale DRBDResourceOperation created before current formation", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1")
		rsp := newTestRSP("test-pool")
		rsp.Status.EligibleNodes = []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1", LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{{Name: "lvg-1"}}},
		}
		rv := newRVInBootstrapData()
		rvr := newConfiguredConnectedRVR()

		// Stale DRBDResourceOperation: created 1 hour before formation started.
		staleDRBDROp := &v1alpha1.DRBDResourceOperation{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "rv-1-formation",
				CreationTimestamp: metav1.NewTime(rv.Status.DatameshTransitions[0].StartedAt.Add(-1 * time.Hour)),
			},
			Spec: v1alpha1.DRBDResourceOperationSpec{
				DRBDResourceName: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0),
				Type:             v1alpha1.DRBDResourceOperationCreateNewUUID,
				CreateNewUUID:    &v1alpha1.CreateNewUUIDParams{ClearBitmap: true},
			},
			Status: v1alpha1.DRBDResourceOperationStatus{
				Phase: v1alpha1.DRBDOperationPhaseSucceeded,
			},
		}

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr, staleDRBDROp).
			WithStatusSubresource(rv, rsc, rvr, staleDRBDROp).
			Build()
		rec := NewReconciler(cl, scheme)

		_, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())

		// Stale DRBDROp should be deleted and a new one created.
		var drbdrOp v1alpha1.DRBDResourceOperation
		Expect(cl.Get(ctx, client.ObjectKey{Name: "rv-1-formation"}, &drbdrOp)).To(Succeed())
		// New DRBDROp should have been created (creation timestamp after formation start).
		Expect(drbdrOp.CreationTimestamp.Time).NotTo(Equal(staleDRBDROp.CreationTimestamp.Time))
	})

	It("restarts formation when existing DRBDResourceOperation has parameter mismatch", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1")
		rsp := newTestRSP("test-pool")
		rsp.Status.EligibleNodes = []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1", LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{{Name: "lvg-1"}}},
		}
		rv := newRVInBootstrapData()
		rvr := newConfiguredConnectedRVR()

		// DRBDROp with wrong parameters: ForceResync=true but single replica LVM should use ClearBitmap=true.
		mismatchedDRBDROp := &v1alpha1.DRBDResourceOperation{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "rv-1-formation",
				CreationTimestamp: metav1.NewTime(formationStartedAt.Add(1 * time.Second)),
			},
			Spec: v1alpha1.DRBDResourceOperationSpec{
				DRBDResourceName: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0),
				Type:             v1alpha1.DRBDResourceOperationCreateNewUUID,
				CreateNewUUID:    &v1alpha1.CreateNewUUIDParams{ClearBitmap: false, ForceResync: true},
			},
			Status: v1alpha1.DRBDResourceOperationStatus{Phase: v1alpha1.DRBDOperationPhasePending},
		}

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr, mismatchedDRBDROp).
			WithStatusSubresource(rv, rsc, rvr, mismatchedDRBDROp).
			Build()
		rec := NewReconciler(cl, scheme)

		result, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
		Expect(updated.Status.DatameshTransitions[0].Message).To(ContainSubstring("unexpected parameters"))
	})

	It("waits for replicas to reach UpToDate when DRBDResourceOperation succeeded", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1")
		rsp := newTestRSP("test-pool")
		rsp.Status.EligibleNodes = []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1", LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{{Name: "lvg-1"}}},
		}
		rv := newRVInBootstrapData()

		// RVR backing volume still Inconsistent after DRBDROp succeeded.
		rvr := newConfiguredConnectedRVR()
		// BackingVolume.State remains DiskStateInconsistent (default from helper).

		drbdrOp := &v1alpha1.DRBDResourceOperation{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "rv-1-formation",
				CreationTimestamp: metav1.NewTime(formationStartedAt.Add(1 * time.Second)),
			},
			Spec: v1alpha1.DRBDResourceOperationSpec{
				DRBDResourceName: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0),
				Type:             v1alpha1.DRBDResourceOperationCreateNewUUID,
				CreateNewUUID:    &v1alpha1.CreateNewUUIDParams{ClearBitmap: true},
			},
			Status: v1alpha1.DRBDResourceOperationStatus{
				Phase: v1alpha1.DRBDOperationPhaseSucceeded,
			},
		}

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr, drbdrOp).
			WithStatusSubresource(rv, rsc, rvr, drbdrOp).
			Build()
		rec := NewReconciler(cl, scheme)

		result, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
		Expect(updated.Status.DatameshTransitions[0].Message).To(ContainSubstring("Data bootstrap in progress"))
		Expect(updated.Status.DatameshTransitions[0].Message).To(ContainSubstring("UpToDate"))
	})

	It("returns error when DRBDResourceOperation creation fails", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1")
		rsp := newTestRSP("test-pool")
		rsp.Status.EligibleNodes = []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1", LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{{Name: "lvg-1"}}},
		}
		rv := newRVInBootstrapData()
		rvr := newConfiguredConnectedRVR()

		testErr := errors.New("create DRBDROp failed")
		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr).
			WithStatusSubresource(rv, rsc, rvr).
			WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					if _, ok := obj.(*v1alpha1.DRBDResourceOperation); ok {
						return testErr
					}
					return cl.Create(ctx, obj, opts...)
				},
			}).
			Build()
		rec := NewReconciler(cl, scheme)

		_, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, testErr)).To(BeTrue())
	})

	It("requeues when DRBDResourceOperation creation returns AlreadyExists", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1")
		rsp := newTestRSP("test-pool")
		rsp.Status.EligibleNodes = []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1", LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{{Name: "lvg-1"}}},
		}
		rv := newRVInBootstrapData()
		rvr := newConfiguredConnectedRVR()

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr).
			WithStatusSubresource(rv, rsc, rvr).
			WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					if _, ok := obj.(*v1alpha1.DRBDResourceOperation); ok {
						return apierrors.NewAlreadyExists(
							schema.GroupResource{Group: v1alpha1.SchemeGroupVersion.Group, Resource: "drbdresourceoperations"},
							"rv-1-formation",
						)
					}
					return cl.Create(ctx, obj, opts...)
				},
			}).
			Build()
		rec := NewReconciler(cl, scheme)

		result, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Requeue).To(BeTrue()) //nolint:staticcheck // Requeue field is set by flow.DoneAndRequeue/ContinueAndRequeue
	})

	It("uses force-resync for thick multi-replica setup", func(ctx SpecContext) {
		rsc := &v1alpha1.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{Name: "rsc-1"},
			Status: v1alpha1.ReplicatedStorageClassStatus{
				ConfigurationGeneration: 1,
				Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
					Topology:        v1alpha1.TopologyIgnored,
					Replication:     v1alpha1.ReplicationAvailability,
					VolumeAccess:    v1alpha1.VolumeAccessLocal,
					StoragePoolName: "test-pool",
				},
			},
		}
		rsp := newTestRSP("test-pool")
		rsp.Spec.Type = v1alpha1.ReplicatedStoragePoolTypeLVM // Thick provisioning.
		rsp.Status.EligibleNodes = []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1", LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{{Name: "lvg-1"}}},
			{NodeName: "node-2", LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{{Name: "lvg-1"}}},
		}
		rv := newRVInBootstrapData()
		rv.Status.Configuration.Replication = v1alpha1.ReplicationAvailability
		rv.Status.Datamesh.Members = append(rv.Status.Datamesh.Members, v1alpha1.ReplicatedVolumeDatameshMember{
			Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 1), Type: v1alpha1.ReplicaTypeDiskful,
			NodeName: "node-2", Addresses: []v1alpha1.DRBDResourceAddressStatus{{SystemNetworkName: "Internal"}},
			LVMVolumeGroupName: "lvg-1",
		})
		rv.Status.Datamesh.Quorum = 2
		rv.Status.Datamesh.QuorumMinimumRedundancy = 2

		rvr0 := newConfiguredConnectedRVR()
		rvr1 := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name:       v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 1),
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1", Type: v1alpha1.ReplicaTypeDiskful,
				NodeName: "node-2", LVMVolumeGroupName: "lvg-1",
			},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshRevision: 1,
				BackingVolume: &v1alpha1.ReplicatedVolumeReplicaStatusBackingVolume{
					Size: ptr.To(resource.MustParse("11Gi")), State: v1alpha1.DiskStateInconsistent,
				},
			},
		}
		obju.SetStatusCondition(rvr1, metav1.Condition{
			Type: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType, Status: metav1.ConditionTrue,
			Reason: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonConfigured,
		})

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr0, rvr1).
			WithStatusSubresource(rv, rsc, rvr0, rvr1).
			Build()
		rec := NewReconciler(cl, scheme)

		_, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())

		var drbdrOp v1alpha1.DRBDResourceOperation
		Expect(cl.Get(ctx, client.ObjectKey{Name: "rv-1-formation"}, &drbdrOp)).To(Succeed())
		Expect(drbdrOp.Spec.CreateNewUUID.ClearBitmap).To(BeFalse(), "thick multi-replica should NOT use clear-bitmap")
		Expect(drbdrOp.Spec.CreateNewUUID.ForceResync).To(BeTrue(), "thick multi-replica should use force-resync")
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Formation: Restart
//

var _ = Describe("Formation: Restart", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	})

	It("requeues when timeout has not passed", func(ctx SpecContext) {
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
				Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
					Topology: v1alpha1.TopologyIgnored, Replication: v1alpha1.ReplicationNone,
					VolumeAccess: v1alpha1.VolumeAccessLocal, StoragePoolName: "test-pool",
				},
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					{
						Type:      v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation,
						StartedAt: metav1.Now(), // Just started.
						Formation: &v1alpha1.ReplicatedVolumeDatameshTransitionFormation{
							Phase: v1alpha1.ReplicatedVolumeFormationPhasePreconfigure,
						},
					},
				},
			},
		}

		// Unscheduled RVR → triggers wait for scheduling → calls restart with 30s timeout.
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name:       v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0),
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1",
				Type:                 v1alpha1.ReplicaTypeDiskful,
			},
		}

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr).
			WithStatusSubresource(rv, rsc, rvr).
			Build()
		rec := NewReconciler(cl, scheme)

		result, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())
		// Should requeue with delay (timeout not yet passed).
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		Expect(result.RequeueAfter).To(BeNumerically("<=", 30*time.Second))
	})

	It("resets formation when timeout has passed", func(ctx SpecContext) {
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
				Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
					Topology: v1alpha1.TopologyIgnored, Replication: v1alpha1.ReplicationNone,
					VolumeAccess: v1alpha1.VolumeAccessLocal, StoragePoolName: "test-pool",
				},
				DatameshRevision: 1,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					SharedSecret:       "old-secret",
					SharedSecretAlg:    v1alpha1.SharedSecretAlgSHA256,
					SystemNetworkNames: []string{"Internal"},
					Size:               resource.MustParse("10Gi"),
				},
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					{
						Type:      v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation,
						StartedAt: metav1.NewTime(time.Now().Add(-1 * time.Hour)), // Started long ago.
						Formation: &v1alpha1.ReplicatedVolumeDatameshTransitionFormation{
							Phase: v1alpha1.ReplicatedVolumeFormationPhasePreconfigure,
						},
					},
				},
			},
		}

		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name:       v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0),
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1",
				Type:                 v1alpha1.ReplicaTypeDiskful,
			},
		}

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr).
			WithStatusSubresource(rv, rsc, rvr).
			Build()
		rec := NewReconciler(cl, scheme)

		result, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())
		// Should requeue immediately (formation reset).
		Expect(result.Requeue).To(BeTrue()) //nolint:staticcheck // Requeue field is set by flow.DoneAndRequeue/ContinueAndRequeue

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())

		// Configuration should be re-initialized from RSC (not nil).
		Expect(updated.Status.Configuration).NotTo(BeNil())
		// DatameshRevision should be reset.
		Expect(updated.Status.DatameshRevision).To(Equal(int64(0)))
		// Datamesh state should be reset.
		Expect(updated.Status.Datamesh.SharedSecret).To(BeEmpty())

		// RVR should be deleted.
		var updatedRVR v1alpha1.ReplicatedVolumeReplica
		err = cl.Get(ctx, client.ObjectKeyFromObject(rvr), &updatedRVR)
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "RVR should be deleted after restart")
	})

	It("deletes existing DRBDResourceOperation during restart", func(ctx SpecContext) {
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
				Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
					Topology: v1alpha1.TopologyIgnored, Replication: v1alpha1.ReplicationNone,
					VolumeAccess: v1alpha1.VolumeAccessLocal, StoragePoolName: "test-pool",
				},
				DatameshRevision: 1,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					SharedSecret:       "old-secret",
					SharedSecretAlg:    v1alpha1.SharedSecretAlgSHA256,
					SystemNetworkNames: []string{"Internal"},
					Size:               resource.MustParse("10Gi"),
				},
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					{
						Type:      v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation,
						StartedAt: metav1.NewTime(time.Now().Add(-1 * time.Hour)),
						Formation: &v1alpha1.ReplicatedVolumeDatameshTransitionFormation{
							Phase: v1alpha1.ReplicatedVolumeFormationPhasePreconfigure,
						},
					},
				},
			},
		}

		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name:       v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0),
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1",
				Type:                 v1alpha1.ReplicaTypeDiskful,
			},
		}

		// Pre-existing DRBDResourceOperation that should be cleaned up during restart.
		drbdrOp := &v1alpha1.DRBDResourceOperation{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-1-formation"},
			Spec: v1alpha1.DRBDResourceOperationSpec{
				DRBDResourceName: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0),
				Type:             v1alpha1.DRBDResourceOperationCreateNewUUID,
				CreateNewUUID:    &v1alpha1.CreateNewUUIDParams{ClearBitmap: true},
			},
		}

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr, drbdrOp).
			WithStatusSubresource(rv, rsc, rvr, drbdrOp).
			Build()
		rec := NewReconciler(cl, scheme)

		result, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Requeue).To(BeTrue()) //nolint:staticcheck // Requeue field is set by flow.DoneAndRequeue/ContinueAndRequeue

		// DRBDResourceOperation should be deleted during restart.
		var updatedOp v1alpha1.DRBDResourceOperation
		err = cl.Get(ctx, client.ObjectKey{Name: "rv-1-formation"}, &updatedOp)
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "DRBDResourceOperation should be deleted after restart")
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Pure helpers: isRVMetadataInSync, applyRVMetadata
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

// ──────────────────────────────────────────────────────────────────────────────
// ensureRVConfiguration unit tests
//

var _ = Describe("ensureRVConfiguration", func() {
	It("does nothing when RSC is nil", func(ctx SpecContext) {
		rv := &v1alpha1.ReplicatedVolume{}
		outcome := ensureRVConfiguration(ctx, rv, nil)
		Expect(outcome.DidChange()).To(BeFalse())
		Expect(rv.Status.Configuration).To(BeNil())
	})

	It("does nothing when RSC has no configuration", func(ctx SpecContext) {
		rv := &v1alpha1.ReplicatedVolume{}
		rsc := &v1alpha1.ReplicatedStorageClass{}
		outcome := ensureRVConfiguration(ctx, rv, rsc)
		Expect(outcome.DidChange()).To(BeFalse())
		Expect(rv.Status.Configuration).To(BeNil())
	})

	It("initializes configuration from RSC", func(ctx SpecContext) {
		rv := &v1alpha1.ReplicatedVolume{}
		rsc := newRSCWithConfiguration("rsc-1")

		outcome := ensureRVConfiguration(ctx, rv, rsc)
		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rv.Status.Configuration).NotTo(BeNil())
		Expect(rv.Status.Configuration.StoragePoolName).To(Equal("test-pool"))
		Expect(rv.Status.ConfigurationGeneration).To(Equal(int64(1)))
	})

	It("does not overwrite existing configuration", func(ctx SpecContext) {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				ConfigurationGeneration: 5,
				Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
					Topology:        v1alpha1.TopologyTransZonal,
					Replication:     v1alpha1.ReplicationConsistencyAndAvailability,
					VolumeAccess:    v1alpha1.VolumeAccessPreferablyLocal,
					StoragePoolName: "old-pool",
				},
			},
		}
		rsc := newRSCWithConfiguration("rsc-1")

		outcome := ensureRVConfiguration(ctx, rv, rsc)
		Expect(outcome.DidChange()).To(BeFalse())
		Expect(rv.Status.Configuration.StoragePoolName).To(Equal("old-pool"))
		Expect(rv.Status.ConfigurationGeneration).To(Equal(int64(5)))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// computeTargetQuorum edge cases
//

var _ = Describe("computeTargetQuorum edge cases", func() {
	It("counts TypeTransition=ToDiskful members as intended diskful", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
					Topology: v1alpha1.TopologyIgnored, Replication: v1alpha1.ReplicationAvailability,
					VolumeAccess: v1alpha1.VolumeAccessPreferablyLocal, StoragePoolName: "test-pool",
				},
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.ReplicatedVolumeDatameshMember{
						{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0), Type: v1alpha1.ReplicaTypeDiskful},
						// TieBreaker transitioning to Diskful should count.
						{
							Name:           v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 1),
							Type:           v1alpha1.ReplicaTypeTieBreaker,
							TypeTransition: v1alpha1.ReplicatedVolumeDatameshMemberTypeTransitionToDiskful,
						},
					},
				},
			},
		}
		q, qmr := computeTargetQuorum(rv)
		// 2 intended diskful → quorum = 2/2+1 = 2; minQ=2, minQMR=1 → q=2, qmr=2
		Expect(q).To(Equal(byte(2)))
		Expect(qmr).To(Equal(byte(2)))
	})

	It("uses default quorum for unknown replication mode", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
					Topology: v1alpha1.TopologyIgnored, Replication: "UnknownMode",
					VolumeAccess: v1alpha1.VolumeAccessPreferablyLocal, StoragePoolName: "test-pool",
				},
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.ReplicatedVolumeDatameshMember{
						{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0), Type: v1alpha1.ReplicaTypeDiskful},
						{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 1), Type: v1alpha1.ReplicaTypeDiskful},
						{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 2), Type: v1alpha1.ReplicaTypeDiskful},
					},
				},
			},
		}
		q, qmr := computeTargetQuorum(rv)
		// default: minQ=2, minQMR=2; quorum = 3/2+1 = 2 → q=2, qmr=2
		Expect(q).To(Equal(byte(2)))
		Expect(qmr).To(Equal(byte(2)))
	})

	It("does not count non-diskful members without TypeTransition", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
					Topology: v1alpha1.TopologyIgnored, Replication: v1alpha1.ReplicationNone,
					VolumeAccess: v1alpha1.VolumeAccessLocal, StoragePoolName: "test-pool",
				},
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: []v1alpha1.ReplicatedVolumeDatameshMember{
						{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0), Type: v1alpha1.ReplicaTypeDiskful},
						{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 1), Type: v1alpha1.ReplicaTypeAccess},
						{Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 2), Type: v1alpha1.ReplicaTypeTieBreaker},
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
					Members: []v1alpha1.ReplicatedVolumeDatameshMember{
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
				Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{
					Topology: v1alpha1.TopologyIgnored, Replication: v1alpha1.ReplicationNone,
					VolumeAccess: v1alpha1.VolumeAccessLocal, StoragePoolName: "test-pool",
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
		rv.Status.Datamesh.Members = []v1alpha1.ReplicatedVolumeDatameshMember{
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
		rv.Status.Datamesh.Members = []v1alpha1.ReplicatedVolumeDatameshMember{
			{Name: "rv-1-0", NodeName: "node-1", Attached: false},
		}
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{
			{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach, ReplicaName: "rv-1-0", StartedAt: metav1.Now()},
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
		rv.Status.Datamesh.Members = []v1alpha1.ReplicatedVolumeDatameshMember{
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
					Members: []v1alpha1.ReplicatedVolumeDatameshMember{
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
					Members: []v1alpha1.ReplicatedVolumeDatameshMember{
						{Name: "rv-1-1", NodeName: "node-2"},
					},
				},
			},
		}
		Expect(isRVRMemberOrLeavingDatamesh(rv, "rv-1-0")).To(BeFalse())
	})

	It("returns true when RemoveAccessReplica transition exists for the RVR", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					{
						Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveAccessReplica,
						ReplicaName: "rv-1-0",
						StartedAt:   metav1.Now(),
					},
				},
			},
		}
		Expect(isRVRMemberOrLeavingDatamesh(rv, "rv-1-0")).To(BeTrue())
	})

	It("returns false when RemoveAccessReplica transition is for a different RVR", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					{
						Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveAccessReplica,
						ReplicaName: "rv-1-1",
						StartedAt:   metav1.Now(),
					},
				},
			},
		}
		Expect(isRVRMemberOrLeavingDatamesh(rv, "rv-1-0")).To(BeFalse())
	})

	It("ignores non-RemoveAccessReplica transitions", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					{
						Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddAccessReplica,
						ReplicaName: "rv-1-0",
						StartedAt:   metav1.Now(),
					},
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
					Members: []v1alpha1.ReplicatedVolumeDatameshMember{
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

	It("keeps finalizer on deleting RVR when RemoveAccessReplica transition in progress", func(ctx SpecContext) {
		rvr := makeDeletingRVR("rv-1-0", "rv-1")
		cl := newClientBuilder(scheme).WithObjects(rvr).Build()
		rec := NewReconciler(cl, scheme)

		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveAccessReplica, ReplicaName: "rv-1-0", StartedAt: metav1.Now()},
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
					Members: []v1alpha1.ReplicatedVolumeDatameshMember{
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
					Members: []v1alpha1.ReplicatedVolumeDatameshMember{
						{Name: "rv-1-0", Type: v1alpha1.ReplicaTypeDiskful, NodeName: "node-1"},
						{Name: "rv-1-1", Type: v1alpha1.ReplicaTypeAccess, NodeName: "node-2"},
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
					Members: []v1alpha1.ReplicatedVolumeDatameshMember{
						{Name: "rv-1-0", Type: v1alpha1.ReplicaTypeDiskful},
					},
				},
			},
		}
		Expect(removeDatameshMembers(rv, idset.Of(5))).To(BeFalse())
		Expect(rv.Status.Datamesh.Members).To(HaveLen(1))
	})
})

var _ = Describe("applyTransitionMessage", func() {
	It("sets message and returns true when different", func() {
		t := &v1alpha1.ReplicatedVolumeDatameshTransition{Message: "old"}
		Expect(applyTransitionMessage(t, "new")).To(BeTrue())
		Expect(t.Message).To(Equal("new"))
	})

	It("returns false when message is the same", func() {
		t := &v1alpha1.ReplicatedVolumeDatameshTransition{Message: "same"}
		Expect(applyTransitionMessage(t, "same")).To(BeFalse())
	})
})

var _ = Describe("applyPendingReplicaTransitionMessage", func() {
	It("sets message and returns true when different", func() {
		p := &v1alpha1.ReplicatedVolumeDatameshPendingReplicaTransition{Name: "rv-1-1", Message: "old"}
		Expect(applyPendingReplicaTransitionMessage(p, "new")).To(BeTrue())
		Expect(p.Message).To(Equal("new"))
	})

	It("returns false when message is the same", func() {
		p := &v1alpha1.ReplicatedVolumeDatameshPendingReplicaTransition{Name: "rv-1-0", Message: "same"}
		Expect(applyPendingReplicaTransitionMessage(p, "same")).To(BeFalse())
	})
})
