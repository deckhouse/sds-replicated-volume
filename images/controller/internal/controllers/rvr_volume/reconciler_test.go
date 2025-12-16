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

// cspell:words Diskless Logr Subresource apimachinery gomega gvks metav onsi

package rvrvolume_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors" // cspell:words apierrors
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil" // cspell:words controllerutil
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	rvrvolume "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_volume"
)

var _ = Describe("Reconciler", func() {
	scheme := runtime.NewScheme()
	Expect(v1alpha3.AddToScheme(scheme)).To(Succeed())
	Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	Expect(snc.AddToScheme(scheme)).To(Succeed())

	// Available in BeforeEach
	var (
		clientBuilder *fake.ClientBuilder
	)

	// Available in JustBeforeEach
	var (
		cl  client.WithWatch
		rec *rvrvolume.Reconciler
	)

	BeforeEach(func() {
		clientBuilder = fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(
				&v1alpha3.ReplicatedVolumeReplica{},
				&v1alpha3.ReplicatedVolume{})

		// To be safe. To make sure we don't use client from previous iterations
		cl = nil
		rec = nil
	})

	JustBeforeEach(func() {
		cl = clientBuilder.Build()
		rec = rvrvolume.NewReconciler(cl, GinkgoLogr, scheme)
	})

	It("returns no error when ReplicatedVolumeReplica does not exist", func(ctx SpecContext) {
		Expect(rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "not-existing-rvr"},
		})).NotTo(Requeue())
	})

	When("Get fails with non-NotFound error", func() {
		internalServerError := errors.New("internal server error")
		BeforeEach(func() {
			clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if _, ok := obj.(*v1alpha3.ReplicatedVolumeReplica); ok {
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
		var rvr *v1alpha3.ReplicatedVolumeReplica

		BeforeEach(func() {
			rvr = &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-rvr",
					UID:  "test-uid",
				},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "test-rv",
					Type:                 v1alpha3.ReplicaTypeDiskful,
					NodeName:             "node-1",
				},
			}
		})

		When("RVR has DeletionTimestamp", func() {
			BeforeEach(func() {
				rvr.Finalizers = []string{}
				// Ensure status is set before creating RVR
				if rvr.Status == nil {
					rvr.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{}
				}
			})

			JustBeforeEach(func(ctx SpecContext) {
				By("Adding finalizer to RVR so it can be marked for deletion")
				rvr.Finalizers = append(rvr.Finalizers, "test-finalizer")

				By("Create RVR first, then delete it to set DeletionTimestamp")
				Expect(cl.Create(ctx, rvr)).To(Succeed())
				Expect(cl.Delete(ctx, rvr)).To(Succeed())
			})

			DescribeTableSubtree("when status does not have LLV name because",
				Entry("nil Status", func() { rvr.Status = nil }),
				Entry("empty LVMLogicalVolumeName", func() {
					rvr.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{LVMLogicalVolumeName: ""}
				}),
				func(setup func()) {
					BeforeEach(func() {
						setup()
						// Finalizer is already set in parent BeforeEach
					})

					It("should reconcile successfully without error", func(ctx SpecContext) {
						// reconcileLLVDeletion should return early when status is nil or empty
						// The RVR is already created and deleted in parent JustBeforeEach, setting DeletionTimestamp
						Expect(rec.Reconcile(ctx, RequestFor(rvr))).NotTo(Requeue())
					})
				})

			When("status has LVMLogicalVolumeName", func() {
				BeforeEach(func() {
					rvr.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{
						LVMLogicalVolumeName: "test-llv",
					}
				})

				When("LLV does not exist in cluster", func() {
					It("should clear LVMLogicalVolumeName from status", func(ctx SpecContext) {
						Expect(rec.Reconcile(ctx, RequestFor(rvr))).NotTo(Requeue())

						By("Refreshing RVR from cluster to get updated status")
						Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), rvr)).To(Succeed())
						Expect(rvr).To(HaveNoLVMLogicalVolumeName())
					})

					When("clearing status fails", func() {
						statusPatchError := errors.New("failed to patch status")
						BeforeEach(func() {
							clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
								SubResourcePatch: func(ctx context.Context, cl client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
									if rvrObj, ok := obj.(*v1alpha3.ReplicatedVolumeReplica); ok && rvrObj.Name == "test-rvr" {
										if subResourceName == "status" {
											return statusPatchError
										}
									}
									return cl.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
								},
							})
						})

						// RVR is already created and deleted in parent JustBeforeEach
						// Client is already created in top-level JustBeforeEach with interceptors from BeforeEach

						It("should fail if patching status failed", func(ctx SpecContext) {
							Expect(rec.Reconcile(ctx, RequestFor(rvr))).Error().To(MatchError(ContainSubstring("clearing LVMLogicalVolumeName from status")))
						})
					})
				})

				When("LLV exists in cluster", func() {
					var llv *snc.LVMLogicalVolume

					BeforeEach(func() {
						llv = &snc.LVMLogicalVolume{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test-llv",
							},
						}
					})

					JustBeforeEach(func(ctx SpecContext) {
						Expect(cl.Create(ctx, llv)).To(Succeed())
					})

					When("LLV is not marked for deletion", func() {
						It("should mark LLV for deletion", func(ctx SpecContext) {
							Expect(rec.Reconcile(ctx, RequestFor(rvr))).NotTo(Requeue())

							// LLV should be marked for deletion (fake client doesn't delete immediately)
							updatedLLV := &snc.LVMLogicalVolume{}
							err := cl.Get(ctx, client.ObjectKeyFromObject(llv), updatedLLV)
							if err == nil {
								// If still exists, it should be marked for deletion
								Expect(updatedLLV.DeletionTimestamp).NotTo(BeNil())
							} else {
								// Or it might be deleted
								Expect(apierrors.IsNotFound(err)).To(BeTrue())
							}
						})

						When("LLV has another finalizer", func() {
							BeforeEach(func() {
								llv.Finalizers = []string{"other-finalizer"}
							})

							It("should keep other finalizers and set DeletionTimestamp", func(ctx SpecContext) {
								Expect(rec.Reconcile(ctx, RequestFor(rvr))).NotTo(Requeue())

								updatedLLV := &snc.LVMLogicalVolume{}
								Expect(cl.Get(ctx, client.ObjectKeyFromObject(llv), updatedLLV)).To(Succeed())
								Expect(updatedLLV.Finalizers).To(ConsistOf("other-finalizer"))
								Expect(updatedLLV.DeletionTimestamp).NotTo(BeNil())
							})
						})

						When("Delete fails", func() {
							deleteError := errors.New("failed to delete")
							BeforeEach(func() {
								clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
									Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
										if llvObj, ok := obj.(*snc.LVMLogicalVolume); ok && llvObj.Name == "test-llv" {
											return deleteError
										}
										return cl.Delete(ctx, obj, opts...)
									},
								})
							})

							// RVR and LLV are already created in parent JustBeforeEach
							// Client is already created in top-level JustBeforeEach with interceptors from BeforeEach

							It("should fail if deleting LLV failed", func(ctx SpecContext) {
								Expect(rec.Reconcile(ctx, RequestFor(rvr))).Error().To(MatchError(ContainSubstring("deleting llv")))

								Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), rvr)).To(Succeed())
								Expect(rvr).To(HaveBackingVolumeCreatedConditionDeletionFailed())
							})
						})

						When("LLV is marked for deletion", func() {
							JustBeforeEach(func(ctx SpecContext) {
								existingLLV := &snc.LVMLogicalVolume{}
								Expect(cl.Get(ctx, client.ObjectKeyFromObject(llv), existingLLV)).To(Succeed())
								Expect(cl.Delete(ctx, existingLLV)).To(Succeed())
							})

							It("should reconcile successfully when LLV already deleting", func(ctx SpecContext) {
								Expect(rec.Reconcile(ctx, RequestFor(rvr))).NotTo(Requeue())

								existingLLV := &snc.LVMLogicalVolume{}
								err := cl.Get(ctx, client.ObjectKeyFromObject(llv), existingLLV)
								if err == nil {
									Expect(existingLLV.DeletionTimestamp).NotTo(BeNil())
								} else {
									Expect(apierrors.IsNotFound(err)).To(BeTrue())
								}
							})
						})

						When("Get LLV fails with non-NotFound error", func() {
							getError := errors.New("failed to get")
							BeforeEach(func() {
								clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
									Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
										if _, ok := obj.(*snc.LVMLogicalVolume); ok && key.Name == "test-llv" {
											return getError
										}
										return cl.Get(ctx, key, obj, opts...)
									},
								})
							})

							// RVR and LLV are already created in parent JustBeforeEach
							// Client is already created in top-level JustBeforeEach with interceptors from BeforeEach

							It("should fail if getting LLV failed", func(ctx SpecContext) {
								Expect(rec.Reconcile(ctx, RequestFor(rvr))).Error().To(MatchError(ContainSubstring("checking if llv exists")))
							})
						})
					})
				})
			})

			When("RVR does not have DeletionTimestamp", func() {
				DescribeTableSubtree("when RVR is not diskful because",
					Entry("Type is Access", func() { rvr.Spec.Type = v1alpha3.ReplicaTypeAccess }),
					Entry("Type is TieBreaker", func() { rvr.Spec.Type = v1alpha3.ReplicaTypeTieBreaker }),
					func(setup func()) {
						BeforeEach(func() {
							setup()
						})

						When("ActualType matches Spec.Type", func() {
							BeforeEach(func() {
								rvr.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{
									ActualType: rvr.Spec.Type,
								}
							})

							It("should call reconcileLLVDeletion", func(ctx SpecContext) {
								Expect(rec.Reconcile(ctx, RequestFor(rvr))).NotTo(Requeue())

								Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), rvr)).To(Succeed())
								Expect(rvr).To(HaveBackingVolumeCreatedConditionNotApplicable())
							})
						})

						When("ActualType does not match Spec.Type", func() {
							BeforeEach(func() {
								rvr.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{
									ActualType:           v1alpha3.ReplicaTypeDiskful,
									LVMLogicalVolumeName: "keep-llv",
								}
							})

							It("should reconcile successfully without error", func(ctx SpecContext) {
								Expect(rec.Reconcile(ctx, RequestFor(rvr))).NotTo(Requeue())
							})
						})

						When("Status is nil", func() {
							BeforeEach(func() {
								rvr.Status = nil
							})

							It("should reconcile successfully without error", func(ctx SpecContext) {
								Expect(rec.Reconcile(ctx, RequestFor(rvr))).NotTo(Requeue())

								Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), rvr)).To(Succeed())
								Expect(rvr).To(HaveBackingVolumeCreatedConditionNotApplicable())
							})
						})
					})

				When("RVR is Diskful", func() {
					BeforeEach(func() {
						rvr.Spec.Type = v1alpha3.ReplicaTypeDiskful
					})

					DescribeTableSubtree("when RVR cannot create LLV because",
						Entry("NodeName is empty", func() { rvr.Spec.NodeName = "" }),
						Entry("Type is not Diskful", func() { rvr.Spec.Type = v1alpha3.ReplicaTypeAccess }),
						func(setup func()) {
							BeforeEach(func() {
								setup()
							})

							It("should reconcile successfully without error", func(ctx SpecContext) {
								Expect(rec.Reconcile(ctx, RequestFor(rvr))).NotTo(Requeue())
							})
						})

					When("RVR has NodeName and is Diskful", func() {
						BeforeEach(func() {
							rvr.Spec.NodeName = "node-1"
							rvr.Spec.Type = v1alpha3.ReplicaTypeDiskful
						})

						When("Status is nil", func() {
							BeforeEach(func() {
								rvr.Status = nil
							})

							It("should call reconcileLLVNormal", func(ctx SpecContext) {
								Expect(rec.Reconcile(ctx, RequestFor(rvr))).NotTo(Requeue())
							})
						})

						When("Status.LVMLogicalVolumeName is empty", func() {
							BeforeEach(func() {
								rvr.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{
									LVMLogicalVolumeName: "",
								}
							})

							It("should call reconcileLLVNormal", func(ctx SpecContext) {
								Expect(rec.Reconcile(ctx, RequestFor(rvr))).NotTo(Requeue())
							})
						})

						When("Status.LVMLogicalVolumeName is set", func() {
							BeforeEach(func() {
								rvr.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{
									LVMLogicalVolumeName: "existing-llv",
								}
							})

							It("should reconcile successfully without error", func(ctx SpecContext) {
								Expect(rec.Reconcile(ctx, RequestFor(rvr))).NotTo(Requeue())
							})
						})
					})
				})
			})
		})

		When("reconcileLLVNormal scenarios", func() {
			var rvr *v1alpha3.ReplicatedVolumeReplica
			var rv *v1alpha3.ReplicatedVolume
			var rsc *v1alpha1.ReplicatedStorageClass
			var rsp *v1alpha1.ReplicatedStoragePool
			var lvg *snc.LVMVolumeGroup

			BeforeEach(func() {
				rvr = &v1alpha3.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-rvr",
						UID:  "test-uid",
					},
					Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: "test-rv",
						Type:                 v1alpha3.ReplicaTypeDiskful,
						NodeName:             "node-1",
					},
				}

				rv = &v1alpha3.ReplicatedVolume{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-rv",
					},
					Spec: v1alpha3.ReplicatedVolumeSpec{
						Size:                       resource.MustParse("1Gi"),
						ReplicatedStorageClassName: "test-rsc",
					},
				}

				rsc = &v1alpha1.ReplicatedStorageClass{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-rsc",
					},
					Spec: v1alpha1.ReplicatedStorageClassSpec{
						StoragePool: "test-rsp",
					},
				}

				rsp = &v1alpha1.ReplicatedStoragePool{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-rsp",
					},
					Spec: v1alpha1.ReplicatedStoragePoolSpec{
						LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
							{
								Name:         "test-lvg",
								ThinPoolName: "",
							},
						},
					},
				}

				lvg = &snc.LVMVolumeGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-lvg",
					},
					Spec: snc.LVMVolumeGroupSpec{
						Local: snc.LVMVolumeGroupLocalSpec{
							NodeName: "node-1",
						},
					},
				}
			})

			JustBeforeEach(func(ctx SpecContext) {
				// Clear metadata before creating to avoid ResourceVersion issues
				rvrCopy := rvr.DeepCopy()
				rvrCopy.ResourceVersion = ""
				rvrCopy.UID = ""
				rvrCopy.Generation = 0
				Expect(cl.Create(ctx, rvrCopy)).To(Succeed())
				if rv != nil {
					rvCopy := rv.DeepCopy()
					rvCopy.ResourceVersion = ""
					rvCopy.UID = ""
					rvCopy.Generation = 0
					Expect(cl.Create(ctx, rvCopy)).To(Succeed())
				}
				if rsc != nil {
					rscCopy := rsc.DeepCopy()
					rscCopy.ResourceVersion = ""
					rscCopy.UID = ""
					rscCopy.Generation = 0
					Expect(cl.Create(ctx, rscCopy)).To(Succeed())
				}
				if rsp != nil {
					rspCopy := rsp.DeepCopy()
					rspCopy.ResourceVersion = ""
					rspCopy.UID = ""
					rspCopy.Generation = 0
					Expect(cl.Create(ctx, rspCopy)).To(Succeed())
				}
				if lvg != nil {
					lvgCopy := lvg.DeepCopy()
					lvgCopy.ResourceVersion = ""
					lvgCopy.UID = ""
					lvgCopy.Generation = 0
					Expect(cl.Create(ctx, lvgCopy)).To(Succeed())
				}
			})

			When("RVR is Diskful with NodeName and no LLV name in status", func() {
				BeforeEach(func() {
					rvr.Status = nil
				})

				When("LLV does not exist", func() {
					It("should create LLV", func(ctx SpecContext) {
						Expect(rec.Reconcile(ctx, RequestFor(rvr))).NotTo(Requeue())

						var llvList snc.LVMLogicalVolumeList
						Expect(cl.List(ctx, &llvList)).To(Succeed())
						Expect(llvList.Items).To(HaveLen(1))

						llv := &llvList.Items[0]
						Expect(llv).To(HaveLLVWithOwnerReference(rvr.Name))
						Expect(llv.Name).To(Equal(rvr.Name))
						Expect(llv.Spec.LVMVolumeGroupName).To(Equal("test-lvg"))
						Expect(llv.Spec.Size).To(Equal("1Gi"))
						Expect(llv.Spec.Type).To(Equal("Thick"))
						Expect(llv.Spec.ActualLVNameOnTheNode).To(Equal("test-rv"))

						Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), rvr)).To(Succeed())
						Expect(rvr).To(HaveNoLVMLogicalVolumeName())
						Expect(rvr).To(HaveBackingVolumeCreatedConditionNotReady())
					})

					When("ActualType was Access before switching to Diskful", func() {
						BeforeEach(func() {
							rvr.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{
								ActualType: v1alpha3.ReplicaTypeAccess,
							}
						})

						It("should create LLV for Diskful mode", func(ctx SpecContext) {
							Expect(rec.Reconcile(ctx, RequestFor(rvr))).NotTo(Requeue())

							var llvList snc.LVMLogicalVolumeList
							Expect(cl.List(ctx, &llvList)).To(Succeed())
							Expect(llvList.Items).To(HaveLen(1))

							llv := &llvList.Items[0]
							Expect(llv).To(HaveLLVWithOwnerReference(rvr.Name))
							Expect(llv.Name).To(Equal(rvr.Name))
							Expect(llv.Spec.LVMVolumeGroupName).To(Equal("test-lvg"))
							Expect(llv.Spec.Size).To(Equal("1Gi"))
							Expect(llv.Spec.Type).To(Equal("Thick"))
							Expect(llv.Spec.ActualLVNameOnTheNode).To(Equal("test-rv"))

							Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), rvr)).To(Succeed())
							Expect(rvr).To(HaveNoLVMLogicalVolumeName())
						})
					})

					When("ReplicatedVolume does not exist", func() {
						BeforeEach(func() {
							rv = nil
						})

						JustBeforeEach(func(ctx SpecContext) {
							// RVR is already created in parent JustBeforeEach, don't recreate it
							// Don't create RV (it's nil), but create other objects if they don't exist
							if rsc != nil {
								existingRSC := &v1alpha1.ReplicatedStorageClass{}
								err := cl.Get(ctx, client.ObjectKeyFromObject(rsc), existingRSC)
								if err != nil {
									rscCopy := rsc.DeepCopy()
									rscCopy.ResourceVersion = ""
									rscCopy.UID = ""
									rscCopy.Generation = 0
									Expect(cl.Create(ctx, rscCopy)).To(Succeed())
								}
							}
							if rsp != nil {
								existingRSP := &v1alpha1.ReplicatedStoragePool{}
								err := cl.Get(ctx, client.ObjectKeyFromObject(rsp), existingRSP)
								if err != nil {
									rspCopy := rsp.DeepCopy()
									rspCopy.ResourceVersion = ""
									rspCopy.UID = ""
									rspCopy.Generation = 0
									Expect(cl.Create(ctx, rspCopy)).To(Succeed())
								}
							}
							if lvg != nil {
								existingLVG := &snc.LVMVolumeGroup{}
								err := cl.Get(ctx, client.ObjectKeyFromObject(lvg), existingLVG)
								if err != nil {
									lvgCopy := lvg.DeepCopy()
									lvgCopy.ResourceVersion = ""
									lvgCopy.UID = ""
									lvgCopy.Generation = 0
									Expect(cl.Create(ctx, lvgCopy)).To(Succeed())
								}
							}
						})

						It("should fail if getting ReplicatedVolume failed", func(ctx SpecContext) {
							Expect(rec.Reconcile(ctx, RequestFor(rvr))).Error().To(MatchError(ContainSubstring("getting ReplicatedVolume")))
						})
					})

					When("ReplicatedStorageClass does not exist", func() {
						BeforeEach(func() {
							rsc = nil
						})

						JustBeforeEach(func(ctx SpecContext) {
							// RVR and RV are already created in parent JustBeforeEach, don't recreate them
							// Don't create RSC (it's nil), but create other objects if they don't exist
							if rsp != nil {
								existingRSP := &v1alpha1.ReplicatedStoragePool{}
								err := cl.Get(ctx, client.ObjectKeyFromObject(rsp), existingRSP)
								if err != nil {
									rspCopy := rsp.DeepCopy()
									rspCopy.ResourceVersion = ""
									rspCopy.UID = ""
									rspCopy.Generation = 0
									Expect(cl.Create(ctx, rspCopy)).To(Succeed())
								}
							}
							if lvg != nil {
								existingLVG := &snc.LVMVolumeGroup{}
								err := cl.Get(ctx, client.ObjectKeyFromObject(lvg), existingLVG)
								if err != nil {
									lvgCopy := lvg.DeepCopy()
									lvgCopy.ResourceVersion = ""
									lvgCopy.UID = ""
									lvgCopy.Generation = 0
									Expect(cl.Create(ctx, lvgCopy)).To(Succeed())
								}
							}
						})

						It("should fail if getting ReplicatedStorageClass failed", func(ctx SpecContext) {
							Expect(rec.Reconcile(ctx, RequestFor(rvr))).Error().To(MatchError(ContainSubstring("getting LVMVolumeGroupName and ThinPoolName")))
						})
					})

					When("ReplicatedStoragePool does not exist", func() {
						BeforeEach(func() {
							rsp = nil
						})

						JustBeforeEach(func(ctx SpecContext) {
							// RVR, RV, and RSC are already created in parent JustBeforeEach, don't recreate them
							// Don't create RSP (it's nil), but create other objects if they don't exist
							if lvg != nil {
								existingLVG := &snc.LVMVolumeGroup{}
								err := cl.Get(ctx, client.ObjectKeyFromObject(lvg), existingLVG)
								if err != nil {
									lvgCopy := lvg.DeepCopy()
									lvgCopy.ResourceVersion = ""
									lvgCopy.UID = ""
									lvgCopy.Generation = 0
									Expect(cl.Create(ctx, lvgCopy)).To(Succeed())
								}
							}
						})

						It("should fail if getting ReplicatedStoragePool failed", func(ctx SpecContext) {
							Expect(rec.Reconcile(ctx, RequestFor(rvr))).Error().To(MatchError(ContainSubstring("getting ReplicatedStoragePool")))
						})
					})

					When("LVMVolumeGroup does not exist", func() {
						BeforeEach(func() {
							lvg = nil
						})

						JustBeforeEach(func() {
							// RVR, RV, RSC, and RSP are already created in parent JustBeforeEach, don't recreate them
							// Don't create LVG (it's nil)
						})

						It("should fail if getting LVMVolumeGroup failed", func(ctx SpecContext) {
							Expect(rec.Reconcile(ctx, RequestFor(rvr))).Error().To(MatchError(ContainSubstring("getting LVMVolumeGroup")))
						})
					})

					When("no LVMVolumeGroup matches node", func() {
						BeforeEach(func() {
							lvg.Spec.Local.NodeName = "other-node"
						})

						It("should fail if no LVMVolumeGroup found for node", func(ctx SpecContext) {
							Expect(rec.Reconcile(ctx, RequestFor(rvr))).Error().To(MatchError(ContainSubstring("no LVMVolumeGroup found")))
						})
					})

					When("Create LLV fails", func() {
						createError := errors.New("failed to create")
						BeforeEach(func() {
							clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
								Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
									if llvObj, ok := obj.(*snc.LVMLogicalVolume); ok && llvObj.Name == "test-rvr" {
										return createError
									}
									return cl.Create(ctx, obj, opts...)
								},
							})
						})

						// RVR, RV, RSC, RSP, and LVG are already created in parent JustBeforeEach
						// Client is already created in top-level JustBeforeEach with interceptors from BeforeEach

						It("should fail if creating LLV failed", func(ctx SpecContext) {
							Expect(rec.Reconcile(ctx, RequestFor(rvr))).Error().To(MatchError(ContainSubstring("creating LVMLogicalVolume")))

							Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), rvr)).To(Succeed())
							Expect(rvr).To(HaveBackingVolumeCreatedConditionCreationFailed())
						})
					})

					When("ThinPool is specified", func() {
						BeforeEach(func() {
							rsp.Spec.LVMVolumeGroups[0].ThinPoolName = "test-thin-pool"
						})

						It("should create LLV with Thin type", func(ctx SpecContext) {
							Expect(rec.Reconcile(ctx, RequestFor(rvr))).NotTo(Requeue())

							var llvList snc.LVMLogicalVolumeList
							Expect(cl.List(ctx, &llvList)).To(Succeed())
							Expect(llvList.Items).To(HaveLen(1))

							llv := &llvList.Items[0]
							Expect(llv.Spec.Type).To(Equal("Thin"))
							Expect(llv.Spec.Thin).NotTo(BeNil())
							Expect(llv.Spec.Thin.PoolName).To(Equal("test-thin-pool"))
						})
					})
				})

				When("LLV exists with ownerReference", func() {
					var llv *snc.LVMLogicalVolume

					BeforeEach(func() {
						llv = &snc.LVMLogicalVolume{
							ObjectMeta: metav1.ObjectMeta{
								Name: rvr.Name,
							},
						}
						Expect(controllerutil.SetControllerReference(rvr, llv, scheme)).To(Succeed())
					})

					JustBeforeEach(func(ctx SpecContext) {
						// RVR is already created in parent JustBeforeEach
						// Get the created RVR to set ownerReference correctly
						createdRVR := &v1alpha3.ReplicatedVolumeReplica{}
						Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), createdRVR)).To(Succeed())
						// Clear metadata and recreate ownerReference
						llvCopy := llv.DeepCopy()
						llvCopy.ResourceVersion = ""
						llvCopy.UID = ""
						llvCopy.Generation = 0
						llvCopy.OwnerReferences = nil
						// Set status if available (it might be set in nested BeforeEach)
						// We'll create with status, and nested JustBeforeEach can update if needed
						if llv.Status != nil {
							llvCopy.Status = llv.Status.DeepCopy()
						}
						Expect(controllerutil.SetControllerReference(createdRVR, llvCopy, scheme)).To(Succeed())
						Expect(cl.Create(ctx, llvCopy)).To(Succeed())
						// If status was set, update it after creation (fake client might need this)
						if llvCopy.Status != nil {
							createdLLV := &snc.LVMLogicalVolume{}
							if err := cl.Get(ctx, client.ObjectKeyFromObject(llvCopy), createdLLV); err == nil {
								createdLLV.Status = llvCopy.Status.DeepCopy()
								// Try to update status, but don't fail if it doesn't work
								_ = cl.Status().Update(ctx, createdLLV)
							}
						}
					})

					When("LLV phase is Created", func() {
						BeforeEach(func() {
							llv.Status = &snc.LVMLogicalVolumeStatus{
								Phase: "Created",
							}
						})

						// Status is already set in parent JustBeforeEach when creating LLV
						// No need to update it here

						When("RVR status does not have LLV name", func() {
							BeforeEach(func() {
								rvr.Status = nil
							})

							It("should update RVR status with LLV name", func(ctx SpecContext) {
								Expect(rec.Reconcile(ctx, RequestFor(rvr))).NotTo(Requeue())

								Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), rvr)).To(Succeed())
								Expect(rvr).To(HaveLVMLogicalVolumeName(llv.Name))
								Expect(rvr).To(HaveBackingVolumeCreatedConditionReady())
							})

							When("updating status fails", func() {
								statusPatchError := errors.New("failed to patch status")
								BeforeEach(func() {
									clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
										SubResourcePatch: func(ctx context.Context, cl client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
											if rvrObj, ok := obj.(*v1alpha3.ReplicatedVolumeReplica); ok && rvrObj.Name == "test-rvr" {
												if subResourceName == "status" {
													return statusPatchError
												}
											}
											return cl.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
										},
									})
								})

								// RVR, RV, RSC, RSP, LVG, and LLV are already created in parent JustBeforeEach
								// Client is already created in top-level JustBeforeEach with interceptors from BeforeEach

								It("should fail if patching status failed", func(ctx SpecContext) {
									Expect(rec.Reconcile(ctx, RequestFor(rvr))).Error().To(MatchError(ContainSubstring("updating LVMLogicalVolumeName in status")))
								})
							})
						})

						When("RVR status already has LLV name", func() {
							BeforeEach(func() {
								rvr.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{
									LVMLogicalVolumeName: llv.Name,
								}
							})

							It("should reconcile successfully without error", func(ctx SpecContext) {
								Expect(rec.Reconcile(ctx, RequestFor(rvr))).NotTo(Requeue())
							})
						})
					})

					DescribeTableSubtree("when LLV phase is not Created because",
						Entry("phase is empty", func() {
							llv.Status = &snc.LVMLogicalVolumeStatus{Phase: ""}
						}),
						Entry("phase is Pending", func() {
							llv.Status = &snc.LVMLogicalVolumeStatus{Phase: "Pending"}
						}),
						Entry("status is nil", func() {
							llv.Status = nil
						}),
						func(setup func()) {
							BeforeEach(func() {
								setup()
							})

							// Status is already set in parent JustBeforeEach when creating LLV
							// No need to update it here - parent JustBeforeEach handles it

							It("should reconcile successfully and wait", func(ctx SpecContext) {
								Expect(rec.Reconcile(ctx, RequestFor(rvr))).NotTo(Requeue())
							})
						})

					When("List LLVs fails", func() {
						listError := errors.New("failed to list")
						BeforeEach(func() {
							clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
								List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
									if _, ok := list.(*snc.LVMLogicalVolumeList); ok {
										return listError
									}
									return cl.List(ctx, list, opts...)
								},
							})
						})

						// RVR, RV, RSC, RSP, LVG, and LLV are already created in parent JustBeforeEach
						// Client is already created in top-level JustBeforeEach with interceptors from BeforeEach

						It("should reconcile successfully without listing LLVs", func(ctx SpecContext) {
							Expect(rec.Reconcile(ctx, RequestFor(rvr))).NotTo(Requeue())
						})
					})
				})
			})
		})
	})

	When("Spec.Type changes from Diskful to Access", func() {
		var rvr *v1alpha3.ReplicatedVolumeReplica
		var llv *snc.LVMLogicalVolume

		BeforeEach(func() {
			rvr = &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name: "type-switch-rvr",
				},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "type-switch-rv",
					Type:                 v1alpha3.ReplicaTypeAccess,
				},
				Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
					ActualType:           v1alpha3.ReplicaTypeAccess,
					LVMLogicalVolumeName: "type-switch-llv",
				},
			}

			llv = &snc.LVMLogicalVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "type-switch-llv",
					Finalizers: []string{"other-finalizer"},
				},
			}
		})

		JustBeforeEach(func(ctx SpecContext) {
			rvrCopy := rvr.DeepCopy()
			rvrCopy.ResourceVersion = ""
			rvrCopy.UID = ""
			rvrCopy.Generation = 0
			Expect(cl.Create(ctx, rvrCopy)).To(Succeed())

			llvCopy := llv.DeepCopy()
			llvCopy.ResourceVersion = ""
			llvCopy.UID = ""
			llvCopy.Generation = 0
			Expect(cl.Create(ctx, llvCopy)).To(Succeed())
		})

		It("should mark LLV for deletion and keep other finalizers", func(ctx SpecContext) {
			Expect(rec.Reconcile(ctx, RequestFor(rvr))).NotTo(Requeue())

			updatedLLV := &snc.LVMLogicalVolume{}
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(llv), updatedLLV)).To(Succeed())
			Expect(updatedLLV.DeletionTimestamp).NotTo(BeNil())
			Expect(updatedLLV.Finalizers).To(ConsistOf("other-finalizer"))
		})

		When("LLV has no finalizers and gets fully removed", func() {
			BeforeEach(func() {
				llv.Finalizers = nil
			})

			It("should clear LVMLogicalVolumeName in status", func(ctx SpecContext) {
				// First reconcile: delete LLV (it disappears immediately because no finalizers)
				Expect(rec.Reconcile(ctx, RequestFor(rvr))).NotTo(Requeue())
				err := cl.Get(ctx, client.ObjectKeyFromObject(llv), &snc.LVMLogicalVolume{})
				Expect(apierrors.IsNotFound(err)).To(BeTrue())

				// Second reconcile: see LLV gone and clear status
				Expect(rec.Reconcile(ctx, RequestFor(rvr))).NotTo(Requeue())

				fetchedRVR := &v1alpha3.ReplicatedVolumeReplica{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), fetchedRVR)).To(Succeed())
				Expect(fetchedRVR.Status.LVMLogicalVolumeName).To(BeEmpty())
			})
		})
	})

	When("Spec.Type is Access but ActualType is Diskful and LLV exists", func() {
		var rvr *v1alpha3.ReplicatedVolumeReplica
		var llv *snc.LVMLogicalVolume

		BeforeEach(func() {
			rvr = &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name: "mismatch-rvr",
				},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "mismatch-rv",
					Type:                 v1alpha3.ReplicaTypeAccess,
				},
				Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
					ActualType:           v1alpha3.ReplicaTypeDiskful,
					LVMLogicalVolumeName: "keep-llv",
				},
			}

			llv = &snc.LVMLogicalVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "keep-llv",
				},
			}
		})

		JustBeforeEach(func(ctx SpecContext) {
			rvrCopy := rvr.DeepCopy()
			rvrCopy.ResourceVersion = ""
			rvrCopy.UID = ""
			rvrCopy.Generation = 0
			Expect(cl.Create(ctx, rvrCopy)).To(Succeed())

			llvCopy := llv.DeepCopy()
			llvCopy.ResourceVersion = ""
			llvCopy.UID = ""
			llvCopy.Generation = 0
			Expect(cl.Create(ctx, llvCopy)).To(Succeed())
		})

		It("should leave LLV intact when ActualType differs", func(ctx SpecContext) {
			Expect(rec.Reconcile(ctx, RequestFor(rvr))).NotTo(Requeue())

			existingLLV := &snc.LVMLogicalVolume{}
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(llv), existingLLV)).To(Succeed())
		})
	})

	When("integration test for full controller lifecycle", func() {
		var rvr *v1alpha3.ReplicatedVolumeReplica
		var rv *v1alpha3.ReplicatedVolume
		var rsc *v1alpha1.ReplicatedStorageClass
		var rsp *v1alpha1.ReplicatedStoragePool
		var lvg *snc.LVMVolumeGroup

		BeforeEach(func() {
			rvr = &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-rvr",
					UID:  "test-uid",
				},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "test-rv",
					Type:                 v1alpha3.ReplicaTypeDiskful,
					NodeName:             "node-1",
				},
				Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
					LVMLogicalVolumeName: "",
				},
			}

			rv = &v1alpha3.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-rv",
				},
				Spec: v1alpha3.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("1Gi"),
					ReplicatedStorageClassName: "test-rsc",
				},
			}

			rsc = &v1alpha1.ReplicatedStorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-rsc",
				},
				Spec: v1alpha1.ReplicatedStorageClassSpec{
					StoragePool: "test-rsp",
				},
			}

			rsp = &v1alpha1.ReplicatedStoragePool{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-rsp",
				},
				Spec: v1alpha1.ReplicatedStoragePoolSpec{
					LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{
						{
							Name:         "test-lvg",
							ThinPoolName: "",
						},
					},
				},
			}

			lvg = &snc.LVMVolumeGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-lvg",
				},
				Spec: snc.LVMVolumeGroupSpec{
					Local: snc.LVMVolumeGroupLocalSpec{
						NodeName: "node-1",
					},
				},
			}
		})

		JustBeforeEach(func(ctx SpecContext) {
			// Create all required objects
			Expect(cl.Create(ctx, rvr)).To(Succeed())

			rvCopy := rv.DeepCopy()
			rvCopy.ResourceVersion = ""
			rvCopy.UID = ""
			rvCopy.Generation = 0
			Expect(cl.Create(ctx, rvCopy)).To(Succeed())

			rscCopy := rsc.DeepCopy()
			rscCopy.ResourceVersion = ""
			rscCopy.UID = ""
			rscCopy.Generation = 0
			Expect(cl.Create(ctx, rscCopy)).To(Succeed())

			rspCopy := rsp.DeepCopy()
			rspCopy.ResourceVersion = ""
			rspCopy.UID = ""
			rspCopy.Generation = 0
			Expect(cl.Create(ctx, rspCopy)).To(Succeed())

			lvgCopy := lvg.DeepCopy()
			lvgCopy.ResourceVersion = ""
			lvgCopy.UID = ""
			lvgCopy.Generation = 0
			Expect(cl.Create(ctx, lvgCopy)).To(Succeed())
		})

		It("should handle full controller lifecycle", func(ctx SpecContext) {
			// Step 1: Initial reconcile - should create LLV
			Expect(rec.Reconcile(ctx, RequestFor(rvr))).NotTo(Requeue())

			// Verify LLV was created
			var llvList snc.LVMLogicalVolumeList
			Expect(cl.List(ctx, &llvList)).To(Succeed())
			Expect(llvList.Items).To(HaveLen(1))
			llvName := llvList.Items[0].Name
			Expect(llvName).To(Equal(rvr.Name))

			// Verify condition is set to NotReady after LLV creation
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), rvr)).To(Succeed())
			Expect(rvr).To(HaveBackingVolumeCreatedConditionNotReady())

			// Step 2: Set LLV phase to Pending and reconcile - should not update RVR status
			// Get the created LLV
			llv := &snc.LVMLogicalVolume{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: llvName}, llv)).To(Succeed())
			llv.Status = &snc.LVMLogicalVolumeStatus{
				Phase: "Pending",
			}
			// Use regular Update for LLV status in fake client
			Expect(cl.Update(ctx, llv)).To(Succeed())
			Expect(cl.Get(ctx, client.ObjectKey{Name: llvName}, llv)).To(Succeed())
			Expect(llv.Status.Phase).To(Equal("Pending"))

			Eventually(func(g Gomega) *v1alpha3.ReplicatedVolumeReplica {
				g.Expect(rec.Reconcile(ctx, RequestFor(rvr))).NotTo(Requeue())

				// Verify RVR status was not updated with LLV name
				notUpdatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
				g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), notUpdatedRVR)).To(Succeed())
				return notUpdatedRVR
			}).WithContext(ctx).Should(HaveNoLVMLogicalVolumeName())

			// Step 3: Set LLV phase to Created and reconcile - should update RVR status
			// Get LLV again to get fresh state
			Expect(cl.Get(ctx, client.ObjectKey{Name: llvName}, llv)).To(Succeed())
			llv.Status.Phase = "Created"
			// Use regular Update for LLV status in fake client
			Expect(cl.Update(ctx, llv)).To(Succeed())

			// Use Eventually to support future async client migration
			Eventually(func(g Gomega) *v1alpha3.ReplicatedVolumeReplica {
				g.Expect(rec.Reconcile(ctx, RequestFor(rvr))).NotTo(Requeue())

				// Verify RVR status was updated with LLV name
				updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
				g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), updatedRVR)).To(Succeed())
				return updatedRVR
			}).WithContext(ctx).Should(And(
				HaveLVMLogicalVolumeName(rvr.Name),
				HaveBackingVolumeCreatedConditionReady(),
			))

			// Get updatedRVR for next steps
			updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), updatedRVR)).To(Succeed())

			// Step 4: Change RVR type to Access - LLV should remain
			// updatedRVR already obtained above
			updatedRVR.Spec.Type = v1alpha3.ReplicaTypeAccess
			Expect(cl.Update(ctx, updatedRVR)).To(Succeed())
			Expect(rec.Reconcile(ctx, RequestFor(rvr))).NotTo(Requeue())

			// Verify LLV still exists
			Expect(cl.Get(ctx, client.ObjectKey{Name: llvName}, llv)).To(Succeed())

			// Step 5: Set actualType to Access - LLV should be deleted
			// Get fresh RVR state
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), updatedRVR)).To(Succeed())
			updatedRVR.Status.ActualType = v1alpha3.ReplicaTypeAccess
			Expect(cl.Status().Update(ctx, updatedRVR)).To(Succeed())
			Expect(rec.Reconcile(ctx, RequestFor(rvr))).NotTo(Requeue())

			// Verify LLV was deleted
			err := cl.Get(ctx, client.ObjectKey{Name: llvName}, &snc.LVMLogicalVolume{})
			Expect(apierrors.IsNotFound(err)).To(BeTrue())

			// Step 6: Reconcile again - should clear LVMLogicalVolumeName from status
			Expect(rec.Reconcile(ctx, RequestFor(rvr))).NotTo(Requeue())

			// Verify status was cleared and condition is set to NotApplicable
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), updatedRVR)).To(Succeed())
			Expect(updatedRVR).To(HaveNoLVMLogicalVolumeName())
			Expect(updatedRVR).To(HaveBackingVolumeCreatedConditionNotApplicable())

			// Step 7: Change type back to Diskful - should create LLV again
			updatedRVR.Spec.Type = v1alpha3.ReplicaTypeDiskful
			Expect(cl.Update(ctx, updatedRVR)).To(Succeed())
			Expect(rec.Reconcile(ctx, RequestFor(rvr))).NotTo(Requeue())

			// Verify LLV was created again
			Expect(cl.List(ctx, &llvList)).To(Succeed())
			Expect(llvList.Items).To(HaveLen(1))
			Expect(llvList.Items[0].Name).To(Equal(rvr.Name))
		})
	})
})
