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
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes/testhelpers"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/nodeidset"
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

		It("keeps finalizer when RV is being deleted but has RVAs", func(ctx SpecContext) {
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

			var updated v1alpha1.ReplicatedVolume
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
			Expect(obju.HasFinalizer(&updated, v1alpha1.RVControllerFinalizer)).To(BeTrue())
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

	It("returns true when deleting with only our finalizer and no attached members", func() {
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
				Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{Replication: v1alpha1.ReplicationNone},
			},
		}
		Expect(computeIntendedDiskfulReplicaCount(rv)).To(Equal(1))
	})

	It("returns 2 for ReplicationAvailability", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{Replication: v1alpha1.ReplicationAvailability},
			},
		}
		Expect(computeIntendedDiskfulReplicaCount(rv)).To(Equal(2))
	})

	It("returns 2 for ReplicationConsistency", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{Replication: v1alpha1.ReplicationConsistency},
			},
		}
		Expect(computeIntendedDiskfulReplicaCount(rv)).To(Equal(2))
	})

	It("returns 3 for ReplicationConsistencyAndAvailability", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{Replication: v1alpha1.ReplicationConsistencyAndAvailability},
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
				Configuration: &v1alpha1.ReplicatedStorageClassConfiguration{Replication: replication},
				Datamesh:      v1alpha1.ReplicatedVolumeDatamesh{Members: members},
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
		var ids nodeidset.NodeIDSet
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
		var ids nodeidset.NodeIDSet
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
		var ids nodeidset.NodeIDSet
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
		var ids nodeidset.NodeIDSet
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
		var ids nodeidset.NodeIDSet
		ids.Add(5) // NodeID 5 does not match any entry.
		changed := applyPendingReplicaMessages(rv, ids, "new")
		Expect(changed).To(BeFalse())
	})
})

var _ = Describe("isRVADeletionConditionsInSync", func() {
	It("returns true when conditions match expected deletion state", func() {
		rva := &v1alpha1.ReplicatedVolumeAttachment{
			Status: v1alpha1.ReplicatedVolumeAttachmentStatus{
				Conditions: []metav1.Condition{
					{
						Type:   v1alpha1.ReplicatedVolumeAttachmentCondAttachedType,
						Status: metav1.ConditionFalse,
						Reason: v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonWaitingForReplicatedVolume,
					},
					{
						Type:   v1alpha1.ReplicatedVolumeAttachmentCondReadyType,
						Status: metav1.ConditionFalse,
						Reason: v1alpha1.ReplicatedVolumeAttachmentCondReadyReasonNotAttached,
					},
				},
			},
		}
		Expect(isRVADeletionConditionsInSync(rva)).To(BeTrue())
	})

	It("returns false when wrong number of conditions", func() {
		rva := &v1alpha1.ReplicatedVolumeAttachment{
			Status: v1alpha1.ReplicatedVolumeAttachmentStatus{
				Conditions: []metav1.Condition{
					{
						Type:   v1alpha1.ReplicatedVolumeAttachmentCondAttachedType,
						Status: metav1.ConditionFalse,
						Reason: v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonWaitingForReplicatedVolume,
					},
				},
			},
		}
		Expect(isRVADeletionConditionsInSync(rva)).To(BeFalse())
	})

	It("returns false when condition has wrong reason", func() {
		rva := &v1alpha1.ReplicatedVolumeAttachment{
			Status: v1alpha1.ReplicatedVolumeAttachmentStatus{
				Conditions: []metav1.Condition{
					{
						Type:   v1alpha1.ReplicatedVolumeAttachmentCondAttachedType,
						Status: metav1.ConditionTrue, // Wrong status.
						Reason: v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttached,
					},
					{
						Type:   v1alpha1.ReplicatedVolumeAttachmentCondReadyType,
						Status: metav1.ConditionFalse,
						Reason: v1alpha1.ReplicatedVolumeAttachmentCondReadyReasonNotAttached,
					},
				},
			},
		}
		Expect(isRVADeletionConditionsInSync(rva)).To(BeFalse())
	})
})
