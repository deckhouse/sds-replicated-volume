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

// cspell:words Logr Subresource apimachinery gomega gvks metav onsi

package rvrdiskfulcount_test

import (
	"context"
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha3 "github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	rvquorumcontroller "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_status_config_quorum"
)

func newFakeClientBuilder() *fake.ClientBuilder {
	scheme := runtime.NewScheme()
	Expect(v1alpha3.AddToScheme(scheme)).To(Succeed())

	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha3.ReplicatedVolumeReplica{}, &v1alpha3.ReplicatedVolume{})
}

func wrapClientWithPatchApplying(baseClient client.Client) client.Client {
	// Wrap client to apply PatchWithConflictRetry changes directly via Update
	// This allows testing the logic of PatchWithConflictRetry callbacks
	return &patchApplyingClient{Client: baseClient}
}

// patchApplyingClient wraps a fake client and applies PatchWithConflictRetry changes via Update
// PatchWithConflictRetry modifies the object in the callback, then calls Patch.
// Since fake client doesn't apply MergeFrom patches correctly, we use Update instead.
type patchApplyingClient struct {
	client.Client
}

func (c *patchApplyingClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	// For MergeFrom patches (used by PatchWithConflictRetry), the object has already been
	// modified by the callback function. We can apply it directly via Update.
	// This simulates what would happen in a real cluster.
	// Check if it's a MergeFrom patch by checking the patch type name
	patchType := fmt.Sprintf("%T", patch)
	// PatchWithConflictRetry uses MergeFromWithOptions which creates a *client.MergeFrom
	// The string representation should be "*client.MergeFrom"
	// For any patch that looks like MergeFrom, apply via Update
	if patchType == "*client.MergeFrom" {
		// The object has already been modified by patchFn in PatchWithConflictRetry
		// Just update it directly (ignore PatchOptions as they're not applicable to Update)
		return c.Client.Update(ctx, obj)
	}
	// For other patch types, use the base client
	return c.Client.Patch(ctx, obj, patch, opts...)
}

func (c *patchApplyingClient) Status() client.StatusWriter {
	return &patchApplyingStatusWriter{StatusWriter: c.Client.Status(), baseClient: c.Client}
}

type patchApplyingStatusWriter struct {
	client.StatusWriter
	baseClient client.Client
}

func (w *patchApplyingStatusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	// For MergeFrom patches in status, also apply via Update
	patchType := fmt.Sprintf("%T", patch)
	if patchType == "*client.MergeFrom" {
		return w.baseClient.Status().Update(ctx, obj)
	}
	return w.StatusWriter.Patch(ctx, obj, patch, opts...)
}

func createReplicatedVolume(name string, ready bool) *v1alpha3.ReplicatedVolume {
	rv := &v1alpha3.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Status: &v1alpha3.ReplicatedVolumeStatus{
			Conditions: []metav1.Condition{},
		},
	}

	if ready {
		rv.Status.Conditions = []metav1.Condition{
			{
				Type:   v1alpha3.ConditionTypeDiskfulReplicaCountReached,
				Status: metav1.ConditionTrue,
			},
			{
				Type:   v1alpha3.ConditionTypeAllReplicasReady,
				Status: metav1.ConditionTrue,
			},
			{
				Type:   v1alpha3.ConditionTypeSharedSecretAlgorithmSelected,
				Status: metav1.ConditionTrue,
			},
		}
	}

	return rv
}

func createReplicatedVolumeReplica(name, rvName, nodeName string, diskless bool) *v1alpha3.ReplicatedVolumeReplica {
	replicaType := "Diskful"
	if diskless {
		replicaType = "Access" // Access or TieBreaker are diskless types
	}
	return &v1alpha3.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "storage.deckhouse.io/v1alpha3",
					Kind:       "ReplicatedVolume",
					Name:       rvName,
					Controller: func() *bool { b := true; return &b }(),
				},
			},
		},
		Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
			ReplicatedVolumeName: rvName,
			NodeName:             nodeName,
			Type:                 replicaType,
		},
	}
}

var _ = Describe("Reconciler", func() {
	// Available in BeforeEach
	var (
		clientBuilder *fake.ClientBuilder
		scheme        *runtime.Scheme
	)

	// Available in JustBeforeEach
	var (
		cl  client.Client
		rec *rvquorumcontroller.Reconciler
	)

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha3.AddToScheme(scheme)).To(Succeed())
		clientBuilder = newFakeClientBuilder()

		// To be safe. To make sure we don't use client from previous iterations
		cl = nil
		rec = nil
	})

	JustBeforeEach(func() {
		baseClient := clientBuilder.Build()
		cl = wrapClientWithPatchApplying(baseClient)
		rec = rvquorumcontroller.NewReconciler(
			cl,
			cl,
			scheme,
			GinkgoLogr,
		)
	})

	It("returns no error when ReplicatedVolume does not exist", func(ctx SpecContext) {
		Expect(rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "not-existing-rv"},
		})).NotTo(Requeue())
	})

	When("Get fails with non-NotFound error", func() {
		internalServerError := errors.New("internal server error")
		BeforeEach(func() {
			clientBuilder = clientBuilder.WithInterceptorFuncs(InterceptGet(func(_ *v1alpha3.ReplicatedVolume) error {
				return internalServerError
			}))
		})

		It("should fail if getting ReplicatedVolume failed with non-NotFound error", func(ctx SpecContext) {
			Expect(rec.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-rv"},
			})).Error().To(MatchError(internalServerError))
		})
	})

	DescribeTableSubtree("when rv is not ready because",
		Entry("missing DiskfulReplicaCountReached condition", func(rv *v1alpha3.ReplicatedVolume) {
			rv.Status = &v1alpha3.ReplicatedVolumeStatus{
				Conditions: []metav1.Condition{
					{
						Type:   v1alpha3.ConditionTypeAllReplicasReady,
						Status: metav1.ConditionTrue,
					},
					{
						Type:   v1alpha3.ConditionTypeSharedSecretAlgorithmSelected,
						Status: metav1.ConditionTrue,
					},
				},
			}
		}),
		Entry("missing AllReplicasReady condition", func(rv *v1alpha3.ReplicatedVolume) {
			rv.Status = &v1alpha3.ReplicatedVolumeStatus{
				Conditions: []metav1.Condition{
					{
						Type:   v1alpha3.ConditionTypeDiskfulReplicaCountReached,
						Status: metav1.ConditionTrue,
					},
					{
						Type:   v1alpha3.ConditionTypeSharedSecretAlgorithmSelected,
						Status: metav1.ConditionTrue,
					},
				},
			}
		}),
		Entry("missing SharedSecretAlgorithmSelected condition", func(rv *v1alpha3.ReplicatedVolume) {
			rv.Status = &v1alpha3.ReplicatedVolumeStatus{
				Conditions: []metav1.Condition{
					{
						Type:   v1alpha3.ConditionTypeDiskfulReplicaCountReached,
						Status: metav1.ConditionTrue,
					},
					{
						Type:   v1alpha3.ConditionTypeAllReplicasReady,
						Status: metav1.ConditionTrue,
					},
				},
			}
		}),
		func(setup func(*v1alpha3.ReplicatedVolume)) {
			var rv *v1alpha3.ReplicatedVolume

			BeforeEach(func() {
				rv = createReplicatedVolume("test-rv", false)
				setup(rv)
			})

			JustBeforeEach(func(ctx SpecContext) {
				Expect(cl.Create(ctx, rv)).To(Succeed())
			})

			It("should do nothing and return no error", func(ctx SpecContext) {
				Expect(rec.Reconcile(ctx, RequestFor(rv))).NotTo(Requeue())

				// Verify no quorum config was set
				updatedRV := &v1alpha3.ReplicatedVolume{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), updatedRV)).To(Succeed())
				Expect(updatedRV.Status.DRBD).To(BeNil())
			})
		})

	When("ReplicatedVolume is ready", func() {
		var rv *v1alpha3.ReplicatedVolume

		BeforeEach(func() {
			rv = createReplicatedVolume("test-rv", true)
			rv.Status.DRBD = &v1alpha3.DRBDResource{
				Config: &v1alpha3.DRBDResourceConfig{},
			}
		})

		JustBeforeEach(func(ctx SpecContext) {
			Expect(cl.Create(ctx, rv)).To(Succeed())
		})

		When("first replicas created", func() {
			var rvr1, rvr2 *v1alpha3.ReplicatedVolumeReplica

			BeforeEach(func() {
				rvr1 = createReplicatedVolumeReplica("rvr-1", "test-rv", "node-1", false)
				rvr2 = createReplicatedVolumeReplica("rvr-2", "test-rv", "node-2", false)
			})

			JustBeforeEach(func(ctx SpecContext) {
				Expect(cl.Create(ctx, rvr1)).To(Succeed())
				Expect(cl.Create(ctx, rvr2)).To(Succeed())
			})

			It("should reconcile successfully when RV is ready with RVRs", func(ctx SpecContext) {
				Expect(rec.Reconcile(ctx, RequestFor(rv))).NotTo(Requeue())

				// Verify finalizers were added to RVRs
				updatedRVR1 := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr1), updatedRVR1)).To(Succeed())
				Expect(updatedRVR1.Finalizers).To(ContainElement(rvquorumcontroller.QuorumReconfFinalizer))

				// Verify QuorumConfigured condition is set
				updatedRV := &v1alpha3.ReplicatedVolume{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), updatedRV)).To(Succeed())
				cond := findCondition(updatedRV.Status.Conditions, v1alpha3.ConditionTypeQuorumConfigured)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				Expect(cond.Reason).To(Equal("QuorumConfigured"))
			})

			When("List fails", func() {
				listError := errors.New("failed to list replicas")
				BeforeEach(func() {
					clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
						List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
							if _, ok := list.(*v1alpha3.ReplicatedVolumeReplicaList); ok {
								return listError
							}
							return cl.List(ctx, list, opts...)
						},
					})
				})

				It("should fail if listing replicas failed", func(ctx SpecContext) {
					Expect(rec.Reconcile(ctx, RequestFor(rv))).Error().To(MatchError(listError))
				})
			})

			When("Patch fails with non-NotFound error", func() {
				patchError := errors.New("failed to patch")
				BeforeEach(func() {
					clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
						Patch: func(ctx context.Context, cl client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
							if _, ok := obj.(*v1alpha3.ReplicatedVolumeReplica); ok {
								return patchError
							}
							return cl.Patch(ctx, obj, patch, opts...)
						},
					})
				})

				It("should fail if patching ReplicatedVolumeReplica failed with non-NotFound error", func(ctx SpecContext) {
					Expect(rec.Reconcile(ctx, RequestFor(rv))).Error().To(MatchError(patchError))
				})
			})

			When("PatchStatus fails with non-NotFound error", func() {
				patchError := errors.New("failed to patch status")
				BeforeEach(func() {
					clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
						SubResourcePatch: func(ctx context.Context, cl client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
							if _, ok := obj.(*v1alpha3.ReplicatedVolume); ok {
								if subResourceName == "status" {
									return patchError
								}
							}
							return cl.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
						},
					})
				})

				It("should fail if patching ReplicatedVolume status failed with non-NotFound error", func(ctx SpecContext) {
					Expect(rec.Reconcile(ctx, RequestFor(rv))).Error().To(MatchError(patchError))
				})
			})

			It("should handle multiple replicas with diskful and diskless", func(ctx SpecContext) {
				// Create 3 diskful and 1 diskless replica
				rvr3 := createReplicatedVolumeReplica("rvr-3", "test-rv", "node-3", false)
				rvr4 := createReplicatedVolumeReplica("rvr-4", "test-rv", "node-4", true)
				Expect(cl.Create(ctx, rvr3)).To(Succeed())
				Expect(cl.Create(ctx, rvr4)).To(Succeed())

				Expect(rec.Reconcile(ctx, RequestFor(rv))).NotTo(Requeue())

				// Verify all RVRs got finalizers
				for _, rvr := range []*v1alpha3.ReplicatedVolumeReplica{rvr1, rvr2, rvr3, rvr4} {
					updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), updatedRVR)).To(Succeed())
					Expect(updatedRVR.Finalizers).To(ContainElement(rvquorumcontroller.QuorumReconfFinalizer))
				}
			})
		})

		When("only one diskful replica exists", func() {
			var rvr1 *v1alpha3.ReplicatedVolumeReplica

			BeforeEach(func() {
				rvr1 = createReplicatedVolumeReplica("rvr-1", "test-rv", "node-1", false)
			})

			JustBeforeEach(func(ctx SpecContext) {
				Expect(cl.Create(ctx, rvr1)).To(Succeed())
			})

			It("should not set quorum when diskfulCount <= 1", func(ctx SpecContext) {
				Expect(rec.Reconcile(ctx, RequestFor(rv))).NotTo(Requeue())

				// Verify quorum is 0 (not set) and QuorumConfigured condition is still set
				updatedRV := &v1alpha3.ReplicatedVolume{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), updatedRV)).To(Succeed())
				Expect(updatedRV.Status).NotTo(BeNil())
				Expect(updatedRV.Status.DRBD).NotTo(BeNil())
				Expect(updatedRV.Status.DRBD.Config).NotTo(BeNil())
				Expect(updatedRV.Status.DRBD.Config.Quorum).To(Equal(byte(0)))
				Expect(updatedRV.Status.DRBD.Config.QuorumMinimumRedundancy).To(Equal(byte(0)))
				// Even with diskfulCount <= 1, condition should be set (quorumPatch is called)
				cond := findCondition(updatedRV.Status.Conditions, v1alpha3.ConditionTypeQuorumConfigured)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			})
		})

		Context("Quorum and QuorumMinimumRedundancy calculations", func() {
			It("should calculate quorum=2, qmr=2 for 2 diskful replicas", func(ctx SpecContext) {
				// Create 2 diskful replicas: all=2, diskful=2
				// Expected: quorum = max(2, 2/2+1) = max(2, 2) = 2
				// Expected: qmr = max(2, 2/2+1) = max(2, 2) = 2
				rvr1 := createReplicatedVolumeReplica("rvr-1", "test-rv", "node-1", false)
				rvr2 := createReplicatedVolumeReplica("rvr-2", "test-rv", "node-2", false)
				Expect(cl.Create(ctx, rvr1)).To(Succeed())
				Expect(cl.Create(ctx, rvr2)).To(Succeed())

				Expect(rec.Reconcile(ctx, RequestFor(rv))).NotTo(Requeue())

				// Verify QuorumConfigured condition is set
				updatedRV := &v1alpha3.ReplicatedVolume{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), updatedRV)).To(Succeed())
				cond := findCondition(updatedRV.Status.Conditions, v1alpha3.ConditionTypeQuorumConfigured)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionTrue))

				// Verify quorum values are applied correctly via PatchStatusWithConflictRetry
				Expect(updatedRV.Status.DRBD).NotTo(BeNil())
				Expect(updatedRV.Status.DRBD.Config).NotTo(BeNil())
				Expect(updatedRV.Status.DRBD.Config.Quorum).To(Equal(byte(2)))
				Expect(updatedRV.Status.DRBD.Config.QuorumMinimumRedundancy).To(Equal(byte(2)))
			})

			It("should calculate quorum=2, qmr=2 for 3 diskful replicas", func(ctx SpecContext) {
				// Create 3 diskful replicas: all=3, diskful=3
				// Expected: quorum = max(2, 3/2+1) = max(2, 2) = 2
				// Expected: qmr = max(2, 3/2+1) = max(2, 2) = 2
				rvr1 := createReplicatedVolumeReplica("rvr-1", "test-rv", "node-1", false)
				rvr2 := createReplicatedVolumeReplica("rvr-2", "test-rv", "node-2", false)
				rvr3 := createReplicatedVolumeReplica("rvr-3", "test-rv", "node-3", false)
				Expect(cl.Create(ctx, rvr1)).To(Succeed())
				Expect(cl.Create(ctx, rvr2)).To(Succeed())
				Expect(cl.Create(ctx, rvr3)).To(Succeed())

				Expect(rec.Reconcile(ctx, RequestFor(rv))).NotTo(Requeue())

				// Verify QuorumConfigured condition is set and quorum values are applied
				updatedRV := &v1alpha3.ReplicatedVolume{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), updatedRV)).To(Succeed())
				cond := findCondition(updatedRV.Status.Conditions, v1alpha3.ConditionTypeQuorumConfigured)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionTrue))

				// Verify quorum values are applied correctly via PatchStatusWithConflictRetry
				Expect(updatedRV.Status.DRBD).NotTo(BeNil())
				Expect(updatedRV.Status.DRBD.Config).NotTo(BeNil())
				Expect(updatedRV.Status.DRBD.Config.Quorum).To(Equal(byte(2)))
				Expect(updatedRV.Status.DRBD.Config.QuorumMinimumRedundancy).To(Equal(byte(2)))
			})

			It("should calculate quorum=3, qmr=3 for 4 diskful replicas", func(ctx SpecContext) {

				// Create 4 diskful replicas: all=4, diskful=4
				// Expected: quorum = max(2, 4/2+1) = max(2, 3) = 3
				// Expected: qmr = max(2, 4/2+1) = max(2, 3) = 3
				rvr1 := createReplicatedVolumeReplica("rvr-1", "test-rv", "node-1", false)
				rvr2 := createReplicatedVolumeReplica("rvr-2", "test-rv", "node-2", false)
				rvr3 := createReplicatedVolumeReplica("rvr-3", "test-rv", "node-3", false)
				rvr4 := createReplicatedVolumeReplica("rvr-4", "test-rv", "node-4", false)
				Expect(cl.Create(ctx, rvr1)).To(Succeed())
				Expect(cl.Create(ctx, rvr2)).To(Succeed())
				Expect(cl.Create(ctx, rvr3)).To(Succeed())
				Expect(cl.Create(ctx, rvr4)).To(Succeed())

				Expect(rec.Reconcile(ctx, RequestFor(rv))).NotTo(Requeue())

				// Verify QuorumConfigured condition is set and quorum values are applied
				updatedRV := &v1alpha3.ReplicatedVolume{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), updatedRV)).To(Succeed())
				cond := findCondition(updatedRV.Status.Conditions, v1alpha3.ConditionTypeQuorumConfigured)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionTrue))

				// Verify quorum values are applied correctly via PatchStatusWithConflictRetry
				Expect(updatedRV.Status.DRBD).NotTo(BeNil())
				Expect(updatedRV.Status.DRBD.Config).NotTo(BeNil())
				Expect(updatedRV.Status.DRBD.Config.Quorum).To(Equal(byte(3)))
				Expect(updatedRV.Status.DRBD.Config.QuorumMinimumRedundancy).To(Equal(byte(3)))
			})

			It("should calculate quorum=3, qmr=3 for 5 diskful replicas", func(ctx SpecContext) {

				// Create 5 diskful replicas: all=5, diskful=5
				// Expected: quorum = max(2, 5/2+1) = max(2, 3) = 3
				// Expected: qmr = max(2, 5/2+1) = max(2, 3) = 3
				for i := 1; i <= 5; i++ {
					rvr := createReplicatedVolumeReplica(
						fmt.Sprintf("rvr-%d", i),
						"test-rv",
						fmt.Sprintf("node-%d", i),
						false,
					)
					Expect(cl.Create(ctx, rvr)).To(Succeed())
				}

				Expect(rec.Reconcile(ctx, RequestFor(rv))).NotTo(Requeue())

				// Verify QuorumConfigured condition is set and quorum values are applied
				updatedRV := &v1alpha3.ReplicatedVolume{}
				Expect(cl.Get(ctx, types.NamespacedName{Name: "test-rv"}, updatedRV)).To(Succeed())
				cond := findCondition(updatedRV.Status.Conditions, v1alpha3.ConditionTypeQuorumConfigured)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionTrue))

				// Verify quorum values are applied correctly via PatchStatusWithConflictRetry
				Expect(updatedRV.Status.DRBD).NotTo(BeNil())
				Expect(updatedRV.Status.DRBD.Config).NotTo(BeNil())
				Expect(updatedRV.Status.DRBD.Config.Quorum).To(Equal(byte(3)))
				Expect(updatedRV.Status.DRBD.Config.QuorumMinimumRedundancy).To(Equal(byte(3)))
			})

			It("should calculate quorum=2, qmr=2 for 2 diskful + 1 diskless replicas", func(ctx SpecContext) {

				// Create 2 diskful + 1 diskless: all=3, diskful=2
				// Expected: quorum = max(2, 3/2+1) = max(2, 2) = 2
				// Expected: qmr = max(2, 2/2+1) = max(2, 2) = 2
				rvr1 := createReplicatedVolumeReplica("rvr-1", "test-rv", "node-1", false)
				rvr2 := createReplicatedVolumeReplica("rvr-2", "test-rv", "node-2", false)
				rvr3 := createReplicatedVolumeReplica("rvr-3", "test-rv", "node-3", true)
				Expect(cl.Create(ctx, rvr1)).To(Succeed())
				Expect(cl.Create(ctx, rvr2)).To(Succeed())
				Expect(cl.Create(ctx, rvr3)).To(Succeed())

				Expect(rec.Reconcile(ctx, RequestFor(rv))).NotTo(Requeue())

				// Verify QuorumConfigured condition is set and quorum values are applied
				updatedRV := &v1alpha3.ReplicatedVolume{}
				Expect(cl.Get(ctx, types.NamespacedName{Name: "test-rv"}, updatedRV)).To(Succeed())
				cond := findCondition(updatedRV.Status.Conditions, v1alpha3.ConditionTypeQuorumConfigured)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionTrue))

				// Verify quorum values are applied correctly via PatchStatusWithConflictRetry
				Expect(updatedRV.Status.DRBD).NotTo(BeNil())
				Expect(updatedRV.Status.DRBD.Config).NotTo(BeNil())
				Expect(updatedRV.Status.DRBD.Config.Quorum).To(Equal(byte(2)))
				Expect(updatedRV.Status.DRBD.Config.QuorumMinimumRedundancy).To(Equal(byte(2)))
			})

			It("should calculate quorum=3, qmr=2 for 3 diskful + 2 diskless replicas", func(ctx SpecContext) {

				// Create 3 diskful + 2 diskless: all=5, diskful=3
				// Expected: quorum = max(2, 5/2+1) = max(2, 3) = 3
				// Expected: qmr = max(2, 3/2+1) = max(2, 2) = 2
				rvr1 := createReplicatedVolumeReplica("rvr-1", "test-rv", "node-1", false)
				rvr2 := createReplicatedVolumeReplica("rvr-2", "test-rv", "node-2", false)
				rvr3 := createReplicatedVolumeReplica("rvr-3", "test-rv", "node-3", false)
				rvr4 := createReplicatedVolumeReplica("rvr-4", "test-rv", "node-4", true)
				rvr5 := createReplicatedVolumeReplica("rvr-5", "test-rv", "node-5", true)
				Expect(cl.Create(ctx, rvr1)).To(Succeed())
				Expect(cl.Create(ctx, rvr2)).To(Succeed())
				Expect(cl.Create(ctx, rvr3)).To(Succeed())
				Expect(cl.Create(ctx, rvr4)).To(Succeed())
				Expect(cl.Create(ctx, rvr5)).To(Succeed())

				Expect(rec.Reconcile(ctx, RequestFor(rv))).NotTo(Requeue())

				// Verify QuorumConfigured condition is set and quorum values are applied
				updatedRV := &v1alpha3.ReplicatedVolume{}
				Expect(cl.Get(ctx, types.NamespacedName{Name: "test-rv"}, updatedRV)).To(Succeed())
				cond := findCondition(updatedRV.Status.Conditions, v1alpha3.ConditionTypeQuorumConfigured)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionTrue))

				// Verify quorum values are applied correctly via PatchStatusWithConflictRetry
				Expect(updatedRV.Status.DRBD).NotTo(BeNil())
				Expect(updatedRV.Status.DRBD.Config).NotTo(BeNil())
				Expect(updatedRV.Status.DRBD.Config.Quorum).To(Equal(byte(3)))
				Expect(updatedRV.Status.DRBD.Config.QuorumMinimumRedundancy).To(Equal(byte(2)))
			})

			It("should calculate quorum=4, qmr=4 for 7 diskful replicas", func(ctx SpecContext) {

				// Create 7 diskful replicas: all=7, diskful=7
				// Expected: quorum = max(2, 7/2+1) = max(2, 4) = 4
				// Expected: qmr = max(2, 7/2+1) = max(2, 4) = 4
				for i := 1; i <= 7; i++ {
					rvr := createReplicatedVolumeReplica(
						fmt.Sprintf("rvr-%d", i),
						"test-rv",
						fmt.Sprintf("node-%d", i),
						false,
					)
					Expect(cl.Create(ctx, rvr)).To(Succeed())
				}

				Expect(rec.Reconcile(ctx, RequestFor(rv))).NotTo(Requeue())

				// Verify QuorumConfigured condition is set and quorum values are applied
				updatedRV := &v1alpha3.ReplicatedVolume{}
				Expect(cl.Get(ctx, types.NamespacedName{Name: "test-rv"}, updatedRV)).To(Succeed())
				cond := findCondition(updatedRV.Status.Conditions, v1alpha3.ConditionTypeQuorumConfigured)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionTrue))

				// Verify quorum values are applied correctly via PatchStatusWithConflictRetry
				Expect(updatedRV.Status.DRBD).NotTo(BeNil())
				Expect(updatedRV.Status.DRBD.Config).NotTo(BeNil())
				Expect(updatedRV.Status.DRBD.Config.Quorum).To(Equal(byte(4)))
				Expect(updatedRV.Status.DRBD.Config.QuorumMinimumRedundancy).To(Equal(byte(4)))
			})

		})

		Context("unsetFinalizers", func() {
			It("should remove finalizer from RVR with DeletionTimestamp", func(ctx SpecContext) {

				// Create RVR with finalizer and DeletionTimestamp
				rvr1 := createReplicatedVolumeReplica("rvr-1", "test-rv", "node-1", false)
				rvr1.Finalizers = []string{rvquorumcontroller.QuorumReconfFinalizer, "other-finalizer"}
				Expect(cl.Create(ctx, rvr1)).To(Succeed())
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr1), rvr1)).To(Succeed())
				Expect(cl.Delete(ctx, rvr1)).To(Succeed())

				Expect(rec.Reconcile(ctx, RequestFor(rv))).NotTo(Requeue())

				// Verify finalizer was removed
				updatedRVR1 := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, types.NamespacedName{Name: "rvr-1"}, updatedRVR1)).To(Succeed())
				Expect(updatedRVR1.Finalizers).NotTo(ContainElement(rvquorumcontroller.QuorumReconfFinalizer))
				Expect(updatedRVR1.Finalizers).To(ContainElement("other-finalizer"))
			})

			It("should not remove finalizer from RVR without DeletionTimestamp", func(ctx SpecContext) {

				// Create RVR with finalizer but no DeletionTimestamp
				rvr1 := createReplicatedVolumeReplica("rvr-1", "test-rv", "node-1", false)
				rvr1.Finalizers = []string{rvquorumcontroller.QuorumReconfFinalizer}
				Expect(cl.Create(ctx, rvr1)).To(Succeed())

				Expect(rec.Reconcile(ctx, RequestFor(rv))).NotTo(Requeue())

				// Verify finalizer is still present (unsetFinalizers should skip RVR without DeletionTimestamp)
				updatedRVR1 := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, types.NamespacedName{Name: "rvr-1"}, updatedRVR1)).To(Succeed())
				Expect(updatedRVR1.Finalizers).To(ContainElement(rvquorumcontroller.QuorumReconfFinalizer))
			})

			It("should not process RVR that doesn't have quorum-reconf finalizer", func(ctx SpecContext) {

				// Create RVR with DeletionTimestamp but no quorum-reconf finalizer
				rvr1 := createReplicatedVolumeReplica("rvr-1", "test-rv", "node-1", false)
				rvr1.Finalizers = []string{"other-finalizer"}
				Expect(cl.Create(ctx, rvr1)).To(Succeed())
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr1), rvr1)).To(Succeed())
				Expect(cl.Delete(ctx, rvr1)).To(Succeed())

				Expect(rec.Reconcile(ctx, RequestFor(rv))).NotTo(Requeue())

				// Verify other finalizer is still present (unsetFinalizers should skip RVR without quorum-reconf finalizer)
				updatedRVR1 := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, types.NamespacedName{Name: "rvr-1"}, updatedRVR1)).To(Succeed())
				Expect(updatedRVR1.Finalizers).To(ContainElement("other-finalizer"))
				Expect(updatedRVR1.Finalizers).NotTo(ContainElement(rvquorumcontroller.QuorumReconfFinalizer))
			})

			It("should process multiple RVRs with DeletionTimestamp", func(ctx SpecContext) {

				// Create multiple RVRs with finalizers and DeletionTimestamp
				rvr1 := createReplicatedVolumeReplica("rvr-1", "test-rv", "node-1", false)
				rvr1.Finalizers = []string{rvquorumcontroller.QuorumReconfFinalizer}
				Expect(cl.Create(ctx, rvr1)).To(Succeed())
				Expect(cl.Delete(ctx, rvr1)).To(Succeed())

				rvr2 := createReplicatedVolumeReplica("rvr-2", "test-rv", "node-2", false)
				rvr2.Finalizers = []string{rvquorumcontroller.QuorumReconfFinalizer, "other-finalizer"}
				Expect(cl.Create(ctx, rvr2)).To(Succeed())
				Expect(cl.Delete(ctx, rvr2)).To(Succeed())

				// Create RVR without DeletionTimestamp (should not be processed by unsetFinalizers)
				rvr3 := createReplicatedVolumeReplica("rvr-3", "test-rv", "node-3", false)
				rvr3.Finalizers = []string{rvquorumcontroller.QuorumReconfFinalizer}
				Expect(cl.Create(ctx, rvr3)).To(Succeed())

				Expect(rec.Reconcile(ctx, RequestFor(rv))).NotTo(Requeue())

				// Verify finalizers removed from RVRs with DeletionTimestamp
				updatedRVR1 := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, types.NamespacedName{Name: "rvr-1"}, updatedRVR1)).To(Satisfy(apierrors.IsNotFound))

				updatedRVR2 := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, types.NamespacedName{Name: "rvr-2"}, updatedRVR2)).To(Succeed())
				Expect(updatedRVR2.Finalizers).NotTo(ContainElement(rvquorumcontroller.QuorumReconfFinalizer))
				Expect(updatedRVR2.Finalizers).To(ContainElement("other-finalizer"))

				// Verify finalizer kept for RVR without DeletionTimestamp
				updatedRVR3 := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, types.NamespacedName{Name: "rvr-3"}, updatedRVR3)).To(Succeed())
				Expect(updatedRVR3.Finalizers).To(ContainElement(rvquorumcontroller.QuorumReconfFinalizer))
			})

			It("should handle RVR with only quorum-reconf finalizer and DeletionTimestamp", func(ctx SpecContext) {

				// Create RVR with only quorum-reconf finalizer and DeletionTimestamp
				rvr1 := createReplicatedVolumeReplica("rvr-1", "test-rv", "node-1", false)
				rvr1.Finalizers = []string{rvquorumcontroller.QuorumReconfFinalizer}
				Expect(cl.Create(ctx, rvr1)).To(Succeed())
				Expect(cl.Delete(ctx, rvr1)).To(Succeed())

				Expect(rec.Reconcile(ctx, RequestFor(rv))).NotTo(Requeue())

				// Verify finalizer was removed (after removal, finalizers list should be empty)
				updatedRVR1 := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, types.NamespacedName{Name: "rvr-1"}, updatedRVR1)).To(Satisfy(apierrors.IsNotFound))

			})
		})
	})
})

func findCondition(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}
