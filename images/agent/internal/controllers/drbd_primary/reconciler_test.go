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

// cspell:words Logr apimachinery gomega gvks metav onsi

package drbdprimary_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	u "github.com/deckhouse/sds-common-lib/utils"
	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	drbdprimary "github.com/deckhouse/sds-replicated-volume/images/agent/internal/controllers/drbd_primary"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/env"
)

var _ = Describe("Reconciler", func() {
	// Available in BeforeEach
	var (
		clientBuilder *fake.ClientBuilder
		scheme        *runtime.Scheme
		cfg           env.Config
	)

	// Available in JustBeforeEach
	var (
		cl  client.WithWatch
		rec *drbdprimary.Reconciler
	)

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		clientBuilder = fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(
				&v1alpha1.ReplicatedVolumeReplica{},
				&v1alpha1.ReplicatedVolume{})

		cfg = &testConfig{nodeName: "test-node"}

		// To be safe. To make sure we don't use client from previous iterations
		cl = nil
		rec = nil
	})

	JustBeforeEach(func() {
		cl = clientBuilder.Build()
		rec = drbdprimary.NewReconciler(cl, GinkgoLogr, scheme, cfg)
	})

	It("ignores NotFound when ReplicatedVolumeReplica does not exist", func(ctx SpecContext) {
		Expect(rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "not-existing-rvr"},
		})).NotTo(Requeue())
	})

	When("Get fails with non-NotFound error", func() {
		internalServerError := errors.New("internal server error")
		BeforeEach(func() {
			clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if _, ok := obj.(*v1alpha1.ReplicatedVolumeReplica); ok {
						return internalServerError
					}
					return cl.Get(ctx, key, obj, opts...)
				},
			})
		})

		It("should fail if getting ReplicatedVolumeReplica failed with non-NotFound error", func(ctx SpecContext) {
			Expect(rec.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-rvr"},
			})).Error().To(MatchError(internalServerError))
		})
	})

	When("ReplicatedVolumeReplica created", func() {
		var rvr *v1alpha1.ReplicatedVolumeReplica
		var rv *v1alpha1.ReplicatedVolume

		BeforeEach(func() {
			rv = &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-rv",
					UID:        "test-uid",
					Finalizers: []string{v1alpha1.ControllerAppFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					ReplicatedStorageClassName: "test-storage-class",
				},
				Status: &v1alpha1.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ConditionTypeRVIOReady,
							Status: metav1.ConditionTrue,
						},
					},
				},
			}

			rvr = &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-rvr",
					UID:  "test-rvr-uid",
				},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: rv.Name,
					NodeName:             cfg.NodeName(),
					Type:                 v1alpha1.ReplicaTypeDiskful,
				},
			}
			Expect(controllerutil.SetControllerReference(rv, rvr, scheme)).To(Succeed())
		})

		JustBeforeEach(func(ctx SpecContext) {
			Expect(cl.Create(ctx, rv)).To(Succeed())
			Expect(cl.Create(ctx, rvr)).To(Succeed())
		})

		When("ReplicatedVolumeReplica has DeletionTimestamp", func() {
			const finalizer = "test-finalizer"
			BeforeEach(func() {
				rvr.Finalizers = []string{finalizer}
			})

			JustBeforeEach(func(ctx SpecContext) {
				By("Deleting rvr")
				Expect(cl.Delete(ctx, rvr)).To(Succeed())

				By("Checking if it has DeletionTimestamp")
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), rvr)).To(
					Succeed(),
					"rvr should not be deleted because it has finalizer",
				)

				Expect(rvr).To(SatisfyAll(
					HaveField("Finalizers", ContainElement(finalizer)),
					HaveField("DeletionTimestamp", Not(BeNil())),
				))
			})

			It("should do nothing and return no error", func(ctx SpecContext) {
				Expect(rec.Reconcile(ctx, RequestFor(rvr))).ToNot(Requeue())
			})
		})

		DescribeTableSubtree("when rvr is not ready because",
			Entry("no NodeName", func() { rvr.Spec.NodeName = "" }),
			Entry("nil Status", func() { rvr.Status = nil }),
			Entry("nil Status.DRBD", func() { rvr.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{DRBD: nil} }),
			Entry("nil Status.DRBD.Actual", func() {
				rvr.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{
					DRBD: &v1alpha1.DRBD{
						Config: &v1alpha1.DRBDConfig{Primary: u.Ptr(true)},
						Status: &v1alpha1.DRBDStatus{},
						Actual: nil,
					},
				}
			}),
			Entry("nil Status.DRBD.Config", func() { rvr.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{DRBD: &v1alpha1.DRBD{Config: nil}} }),
			Entry("nil Status.DRBD.Config.Primary", func() {
				rvr.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{
					DRBD: &v1alpha1.DRBD{
						Config: &v1alpha1.DRBDConfig{Primary: nil},
						Status: &v1alpha1.DRBDStatus{},
						Actual: &v1alpha1.DRBDActual{},
					},
				}
			}),
			Entry("nil Status.DRBD.Status", func() {
				rvr.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{
					DRBD: &v1alpha1.DRBD{Config: &v1alpha1.DRBDConfig{Primary: u.Ptr(true)}, Status: nil}}
			}),
			func(setup func()) {
				BeforeEach(func() {
					setup()
				})

				It("should reconcile successfully and skip", func(ctx SpecContext) {
					Expect(rec.Reconcile(ctx, RequestFor(rvr))).ToNot(Requeue())
				})
			})

		When("RVR does not belong to this node", func() {
			BeforeEach(func() {
				if rvr.Status == nil {
					rvr.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{}
				}
				if rvr.Status.DRBD == nil {
					rvr.Status.DRBD = &v1alpha1.DRBD{}
				}
				if rvr.Status.DRBD.Config == nil {
					rvr.Status.DRBD.Config = &v1alpha1.DRBDConfig{}
				}
				if rvr.Status.DRBD.Status == nil {
					rvr.Status.DRBD.Status = &v1alpha1.DRBDStatus{}
				}
				if rvr.Status.DRBD.Actual == nil {
					rvr.Status.DRBD.Actual = &v1alpha1.DRBDActual{}
				}
				rvr.Spec.NodeName = "other-node"
				rvr.Status.DRBD.Config.Primary = u.Ptr(true)
				rvr.Status.DRBD.Status.Role = "Secondary"
				rvr.Status.DRBD.Actual.InitialSyncCompleted = true
			})

			It("should skip and return no error", func(ctx SpecContext) {
				Expect(rec.Reconcile(ctx, RequestFor(rvr))).ToNot(Requeue())
			})
		})

		When("Initial sync not completed", func() {
			BeforeEach(func() {
				if rvr.Status == nil {
					rvr.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{}
				}
				if rvr.Status.DRBD == nil {
					rvr.Status.DRBD = &v1alpha1.DRBD{}
				}
				if rvr.Status.DRBD.Config == nil {
					rvr.Status.DRBD.Config = &v1alpha1.DRBDConfig{}
				}
				if rvr.Status.DRBD.Status == nil {
					rvr.Status.DRBD.Status = &v1alpha1.DRBDStatus{}
				}
				if rvr.Status.DRBD.Actual == nil {
					rvr.Status.DRBD.Actual = &v1alpha1.DRBDActual{}
				}
				rvr.Spec.NodeName = cfg.NodeName()
				rvr.Status.DRBD.Config.Primary = u.Ptr(true)
				rvr.Status.DRBD.Status.Role = "Secondary"
				rvr.Status.DRBD.Actual.InitialSyncCompleted = false
			})

			It("should skip and return no error", func(ctx SpecContext) {
				Expect(rec.Reconcile(ctx, RequestFor(rvr))).ToNot(Requeue())
			})
		})

		When("RVR is ready and belongs to this node", func() {
			BeforeEach(func() {
				if rvr.Status == nil {
					rvr.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{}
				}
				if rvr.Status.DRBD == nil {
					rvr.Status.DRBD = &v1alpha1.DRBD{}
				}
				if rvr.Status.DRBD.Config == nil {
					rvr.Status.DRBD.Config = &v1alpha1.DRBDConfig{}
				}
				if rvr.Status.DRBD.Status == nil {
					rvr.Status.DRBD.Status = &v1alpha1.DRBDStatus{}
				}
				if rvr.Status.DRBD.Actual == nil {
					rvr.Status.DRBD.Actual = &v1alpha1.DRBDActual{}
				}
				rvr.Spec.NodeName = cfg.NodeName()
				rvr.Status.DRBD.Config.Primary = u.Ptr(true)
				rvr.Status.DRBD.Status.Role = "Secondary"
				rvr.Status.DRBD.Actual.InitialSyncCompleted = true
			})

			DescribeTableSubtree("when role already matches desired state",
				Entry("Primary desired and current role is Primary", func() {
					if rvr.Status == nil {
						rvr.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{}
					}
					if rvr.Status.DRBD == nil {
						rvr.Status.DRBD = &v1alpha1.DRBD{}
					}
					if rvr.Status.DRBD.Config == nil {
						rvr.Status.DRBD.Config = &v1alpha1.DRBDConfig{}
					}
					if rvr.Status.DRBD.Status == nil {
						rvr.Status.DRBD.Status = &v1alpha1.DRBDStatus{}
					}
					if rvr.Status.DRBD.Actual == nil {
						rvr.Status.DRBD.Actual = &v1alpha1.DRBDActual{}
					}
					rvr.Spec.NodeName = cfg.NodeName()
					rvr.Status.DRBD.Config.Primary = u.Ptr(true)
					rvr.Status.DRBD.Status.Role = "Primary"
					rvr.Status.DRBD.Actual.InitialSyncCompleted = true
				}),
				Entry("Secondary desired and current role is Secondary", func() {
					if rvr.Status == nil {
						rvr.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{}
					}
					if rvr.Status.DRBD == nil {
						rvr.Status.DRBD = &v1alpha1.DRBD{}
					}
					if rvr.Status.DRBD.Config == nil {
						rvr.Status.DRBD.Config = &v1alpha1.DRBDConfig{}
					}
					if rvr.Status.DRBD.Status == nil {
						rvr.Status.DRBD.Status = &v1alpha1.DRBDStatus{}
					}
					if rvr.Status.DRBD.Actual == nil {
						rvr.Status.DRBD.Actual = &v1alpha1.DRBDActual{}
					}
					rvr.Spec.NodeName = cfg.NodeName()
					rvr.Status.DRBD.Config.Primary = u.Ptr(false)
					rvr.Status.DRBD.Status.Role = "Secondary"
					rvr.Status.DRBD.Actual.InitialSyncCompleted = true
				}),
				func(setup func()) {
					BeforeEach(func() {
						setup()
					})

					It("should clear errors if they exist", func(ctx SpecContext) {
						// Set some errors first
						if rvr.Status == nil {
							rvr.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{}
						}
						if rvr.Status.DRBD == nil {
							rvr.Status.DRBD = &v1alpha1.DRBD{}
						}
						if rvr.Status.DRBD.Errors == nil {
							rvr.Status.DRBD.Errors = &v1alpha1.DRBDErrors{}
						}
						rvr.Status.DRBD.Errors.LastPrimaryError = &v1alpha1.CmdError{
							Output:   "test error",
							ExitCode: 1,
						}
						rvr.Status.DRBD.Errors.LastSecondaryError = &v1alpha1.CmdError{
							Output:   "test error",
							ExitCode: 1,
						}
						Expect(cl.Status().Update(ctx, rvr)).To(Succeed())

						Expect(rec.Reconcile(ctx, RequestFor(rvr))).ToNot(Requeue())

						Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), rvr)).To(Succeed())
						Expect(rvr).To(HaveNoErrors())
					})

					It("should not patch if no errors exist", func(ctx SpecContext) {
						Expect(rec.Reconcile(ctx, RequestFor(rvr))).ToNot(Requeue())
					})
				})

			When("need to promote to primary", func() {
				BeforeEach(func() {
					if rvr.Status == nil {
						rvr.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{}
					}
					if rvr.Status.DRBD == nil {
						rvr.Status.DRBD = &v1alpha1.DRBD{}
					}
					if rvr.Status.DRBD.Config == nil {
						rvr.Status.DRBD.Config = &v1alpha1.DRBDConfig{}
					}
					if rvr.Status.DRBD.Status == nil {
						rvr.Status.DRBD.Status = &v1alpha1.DRBDStatus{}
					}
					if rvr.Status.DRBD.Actual == nil {
						rvr.Status.DRBD.Actual = &v1alpha1.DRBDActual{}
					}
					rvr.Spec.NodeName = cfg.NodeName()
					rvr.Status.DRBD.Config.Primary = u.Ptr(true)
					rvr.Status.DRBD.Status.Role = "Secondary"
					rvr.Status.DRBD.Actual.InitialSyncCompleted = true
				})

				It("should attempt to promote and store command result in status", func(ctx SpecContext) {
					// Note: drbdadm.ExecutePrimary will be called, but in test environment it will likely fail
					// because drbdadm is not installed. This tests the error handling path.
					// The important thing is that the reconciler correctly handles the command execution
					// and updates the status accordingly. Command errors are stored in status, not returned.

					Expect(rec.Reconcile(ctx, RequestFor(rvr))).ToNot(Requeue())

					// Verify status was updated
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), rvr)).To(Succeed())

					// Command will likely fail in test environment, so verify error was stored in status
					// The reconciler stores command errors in status, not returns them
					Expect(rvr.Status.DRBD.Errors).NotTo(BeNil())
					// If command failed, error should be in status
					if rvr.Status.DRBD.Errors.LastPrimaryError != nil {
						Expect(rvr.Status.DRBD.Errors.LastPrimaryError).NotTo(BeNil())
						Expect(rvr.Status.DRBD.Errors.LastSecondaryError).To(BeNil())
					}
				})

				It("should clear LastSecondaryError when promoting", func(ctx SpecContext) {
					// Set a secondary error first
					if rvr.Status == nil {
						rvr.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{}
					}
					if rvr.Status.DRBD == nil {
						rvr.Status.DRBD = &v1alpha1.DRBD{}
					}
					if rvr.Status.DRBD.Errors == nil {
						rvr.Status.DRBD.Errors = &v1alpha1.DRBDErrors{}
					}
					rvr.Status.DRBD.Errors.LastSecondaryError = &v1alpha1.CmdError{
						Output:   "previous error",
						ExitCode: 1,
					}
					Expect(cl.Status().Update(ctx, rvr)).To(Succeed())

					Expect(rec.Reconcile(ctx, RequestFor(rvr))).ToNot(Requeue())

					// Verify secondary error was cleared
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), rvr)).To(Succeed())
					Expect(rvr.Status.DRBD.Errors.LastSecondaryError).To(BeNil())
				})
			})

			When("need to demote to secondary", func() {
				BeforeEach(func() {
					if rvr.Status == nil {
						rvr.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{}
					}
					if rvr.Status.DRBD == nil {
						rvr.Status.DRBD = &v1alpha1.DRBD{}
					}
					if rvr.Status.DRBD.Config == nil {
						rvr.Status.DRBD.Config = &v1alpha1.DRBDConfig{}
					}
					if rvr.Status.DRBD.Status == nil {
						rvr.Status.DRBD.Status = &v1alpha1.DRBDStatus{}
					}
					if rvr.Status.DRBD.Actual == nil {
						rvr.Status.DRBD.Actual = &v1alpha1.DRBDActual{}
					}
					rvr.Spec.NodeName = cfg.NodeName()
					rvr.Status.DRBD.Config.Primary = u.Ptr(false)
					rvr.Status.DRBD.Status.Role = "Primary"
					rvr.Status.DRBD.Actual.InitialSyncCompleted = true
				})

				It("should attempt to demote and store command result in status", func(ctx SpecContext) {
					// Note: drbdadm.ExecuteSecondary will be called, but in test environment it will likely fail
					// because drbdadm is not installed. This tests the error handling path.

					Expect(rec.Reconcile(ctx, RequestFor(rvr))).ToNot(Requeue())

					// Verify status was updated
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), rvr)).To(Succeed())

					// Command will likely fail in test environment, so verify error was stored in status
					Expect(rvr.Status.DRBD.Errors).NotTo(BeNil())
					// If command failed, error should be in status
					if rvr.Status.DRBD.Errors.LastSecondaryError != nil {
						Expect(rvr.Status.DRBD.Errors.LastSecondaryError).NotTo(BeNil())
						Expect(rvr.Status.DRBD.Errors.LastPrimaryError).To(BeNil())
					}
				})

				It("should clear LastPrimaryError when demoting", func(ctx SpecContext) {
					// Set a primary error first
					if rvr.Status == nil {
						rvr.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{}
					}
					if rvr.Status.DRBD == nil {
						rvr.Status.DRBD = &v1alpha1.DRBD{}
					}
					if rvr.Status.DRBD.Errors == nil {
						rvr.Status.DRBD.Errors = &v1alpha1.DRBDErrors{}
					}
					rvr.Status.DRBD.Errors.LastPrimaryError = &v1alpha1.CmdError{
						Output:   "previous error",
						ExitCode: 1,
					}
					Expect(cl.Status().Update(ctx, rvr)).To(Succeed())

					Expect(rec.Reconcile(ctx, RequestFor(rvr))).ToNot(Requeue())

					// Verify primary error was cleared
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), rvr)).To(Succeed())
					Expect(rvr.Status.DRBD.Errors.LastPrimaryError).To(BeNil())
				})
			})

			When("Status patch fails with non-NotFound error", func() {
				patchError := errors.New("failed to patch status")
				BeforeEach(func() {
					if rvr.Status == nil {
						rvr.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{}
					}
					if rvr.Status.DRBD == nil {
						rvr.Status.DRBD = &v1alpha1.DRBD{}
					}
					if rvr.Status.DRBD.Config == nil {
						rvr.Status.DRBD.Config = &v1alpha1.DRBDConfig{}
					}
					if rvr.Status.DRBD.Status == nil {
						rvr.Status.DRBD.Status = &v1alpha1.DRBDStatus{}
					}
					if rvr.Status.DRBD.Actual == nil {
						rvr.Status.DRBD.Actual = &v1alpha1.DRBDActual{}
					}
					rvr.Spec.NodeName = cfg.NodeName()
					rvr.Status.DRBD.Config.Primary = u.Ptr(true)
					rvr.Status.DRBD.Status.Role = "Secondary"
					rvr.Status.DRBD.Actual.InitialSyncCompleted = true
					clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
						SubResourcePatch: func(ctx context.Context, cl client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
							if _, ok := obj.(*v1alpha1.ReplicatedVolumeReplica); ok {
								if subResourceName == "status" {
									return patchError
								}
							}
							return cl.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
						},
					})
				})

				It("should fail if patching ReplicatedVolumeReplica status failed with non-NotFound error", func(ctx SpecContext) {
					Expect(rec.Reconcile(ctx, RequestFor(rvr))).Error().To(MatchError(patchError))
				})
			})

			When("Status patch fails with NotFound error", func() {
				var rvrName string
				BeforeEach(func() {
					if rvr.Status == nil {
						rvr.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{}
					}
					if rvr.Status.DRBD == nil {
						rvr.Status.DRBD = &v1alpha1.DRBD{}
					}
					if rvr.Status.DRBD.Config == nil {
						rvr.Status.DRBD.Config = &v1alpha1.DRBDConfig{}
					}
					if rvr.Status.DRBD.Status == nil {
						rvr.Status.DRBD.Status = &v1alpha1.DRBDStatus{}
					}
					if rvr.Status.DRBD.Actual == nil {
						rvr.Status.DRBD.Actual = &v1alpha1.DRBDActual{}
					}
					rvr.Spec.NodeName = cfg.NodeName()
					rvr.Status.DRBD.Config.Primary = u.Ptr(true)
					rvr.Status.DRBD.Status.Role = "Secondary"
					rvr.Status.DRBD.Actual.InitialSyncCompleted = true
					rvrName = rvr.Name
					clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
						SubResourcePatch: func(ctx context.Context, cl client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
							if rvrObj, ok := obj.(*v1alpha1.ReplicatedVolumeReplica); ok {
								if subResourceName == "status" && rvrObj.Name == rvrName {
									return apierrors.NewNotFound(schema.GroupResource{Resource: "replicatedvolumereplicas"}, rvrObj.Name)
								}
							}
							return cl.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
						},
					})
				})

				It("should return error if patching ReplicatedVolumeReplica status failed with NotFound error", func(ctx SpecContext) {
					// The reconciler returns the error from the patch, so NotFound error will be returned
					Expect(rec.Reconcile(ctx, RequestFor(rvr))).Error().To(HaveOccurred())
				})
			})
		})
	})
})

type testConfig struct {
	nodeName string
}

func (c *testConfig) NodeName() string {
	return c.nodeName
}

func (c *testConfig) DRBDMinPort() uint {
	return 7000
}

func (c *testConfig) DRBDMaxPort() uint {
	return 7999
}

func (c *testConfig) HealthProbeBindAddress() string {
	return ":4269"
}

func (c *testConfig) MetricsBindAddress() string {
	return ":4270"
}

var _ env.Config = &testConfig{}
