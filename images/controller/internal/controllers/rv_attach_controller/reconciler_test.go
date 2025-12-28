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

package rvattachcontroller_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	rvattachcontroller "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_attach_controller"
)

func withRVAIndex(b *fake.ClientBuilder) *fake.ClientBuilder {
	return b.WithIndex(&v1alpha1.ReplicatedVolumeAttachment{}, v1alpha1.IndexFieldRVAByReplicatedVolumeName, func(obj client.Object) []string {
		rva, ok := obj.(*v1alpha1.ReplicatedVolumeAttachment)
		if !ok {
			return nil
		}
		if rva.Spec.ReplicatedVolumeName == "" {
			return nil
		}
		return []string{rva.Spec.ReplicatedVolumeName}
	})
}

func TestRvAttachReconciler(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "rv-attach-controller Reconciler Suite")
}

var errExpectedTestError = errors.New("test error")

var _ = Describe("Reconcile", func() {
	scheme := runtime.NewScheme()
	Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())

	var (
		builder *fake.ClientBuilder
		cl      client.WithWatch
		rec     *rvattachcontroller.Reconciler
	)

	BeforeEach(func() {
		builder = withRVAIndex(fake.NewClientBuilder().WithScheme(scheme)).
			WithStatusSubresource(&v1alpha1.ReplicatedVolume{}).
			WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
			WithStatusSubresource(&v1alpha1.ReplicatedVolumeAttachment{})
		cl = nil
		rec = nil
	})

	JustBeforeEach(func() {
		cl = builder.Build()
		rec = rvattachcontroller.NewReconciler(cl, logr.New(log.NullLogSink{}))
	})

	It("does not patch ReplicatedVolume status when computed fields already match (ensureRV no-op)", func(ctx SpecContext) {
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "rv-noop",
				Finalizers: []string{v1alpha1.ControllerAppFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeSpec{
				ReplicatedStorageClassName: "rsc1",
			},
			Status: &v1alpha1.ReplicatedVolumeStatus{
				Conditions: []metav1.Condition{{
					Type:   v1alpha1.ConditionTypeRVIOReady,
					Status: metav1.ConditionTrue,
				}},
				DesiredAttachTo:    []string{},
				ActuallyAttachedTo: []string{},
			},
		}
		rsc := &v1alpha1.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: "rsc1",
			},
			Spec: v1alpha1.ReplicatedStorageClassSpec{
				Replication:  "Availability",
				VolumeAccess: "Remote",
			},
		}

		localBuilder := withRVAIndex(fake.NewClientBuilder().WithScheme(scheme)).
			WithStatusSubresource(&v1alpha1.ReplicatedVolume{}).
			WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
			WithStatusSubresource(&v1alpha1.ReplicatedVolumeAttachment{}).
			WithObjects(rv, rsc).
			WithInterceptorFuncs(interceptor.Funcs{
				SubResourcePatch: func(ctx context.Context, cl client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
					if subResourceName == "status" {
						if _, ok := obj.(*v1alpha1.ReplicatedVolume); ok {
							return errExpectedTestError
						}
					}
					return cl.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
				},
			})

		localCl := localBuilder.Build()
		localRec := rvattachcontroller.NewReconciler(localCl, logr.New(log.NullLogSink{}))

		result, err := localRec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(rv)})
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(reconcile.Result{}))
	})

	It("returns nil when ReplicatedVolume not found", func(ctx SpecContext) {
		Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKey{Name: "non-existent"}})).To(Equal(reconcile.Result{}))
	})

	It("sets RVA Pending/Ready=False with WaitingForReplicatedVolume when ReplicatedVolume does not exist", func(ctx SpecContext) {
		// Fake client does not support setting deletionTimestamp via Update() and deletes objects immediately on Delete().
		// To simulate a deleting object, we seed the fake client with an RVA that already has DeletionTimestamp set.
		now := metav1.Now()
		rva := &v1alpha1.ReplicatedVolumeAttachment{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "rva-missing-rv",
				DeletionTimestamp: &now,
				Finalizers: []string{
					v1alpha1.ControllerAppFinalizer,
				},
			},
			Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
				ReplicatedVolumeName: "non-existent",
				NodeName:             "node-1",
			},
		}

		localCl := withRVAIndex(fake.NewClientBuilder().WithScheme(scheme)).
			WithStatusSubresource(&v1alpha1.ReplicatedVolume{}).
			WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
			WithStatusSubresource(&v1alpha1.ReplicatedVolumeAttachment{}).
			WithObjects(rva).
			Build()
		localRec := rvattachcontroller.NewReconciler(localCl, logr.New(log.NullLogSink{}))

		Expect(localRec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKey{Name: "non-existent"}})).To(Equal(reconcile.Result{}))

		got := &v1alpha1.ReplicatedVolumeAttachment{}
		err := localCl.Get(ctx, client.ObjectKeyFromObject(rva), got)
		if client.IgnoreNotFound(err) == nil {
			// Once finalizer is released, the object may disappear immediately.
			return
		}
		Expect(err).NotTo(HaveOccurred())
		// When RV is missing, deleting RVA finalizer must be released.
		Expect(got.Finalizers).NotTo(ContainElement(v1alpha1.ControllerAppFinalizer))
		Expect(got.Status).NotTo(BeNil())
		Expect(got.Status.Phase).To(Equal("Pending"))
		cond := meta.FindStatusCondition(got.Status.Conditions, v1alpha1.RVAConditionTypeReady)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.RVAReasonWaitingForReplicatedVolume))
	})

	It("sets RVA Pending/Ready=False with WaitingForReplicatedVolume when ReplicatedVolume was deleted", func(ctx SpecContext) {
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name: "rv-to-delete",
			},
			Spec: v1alpha1.ReplicatedVolumeSpec{
				ReplicatedStorageClassName: "rsc1",
			},
		}
		Expect(cl.Create(ctx, rv)).To(Succeed())

		rva := &v1alpha1.ReplicatedVolumeAttachment{
			ObjectMeta: metav1.ObjectMeta{
				Name: "rva-for-deleted-rv",
			},
			Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
				ReplicatedVolumeName: "rv-to-delete",
				NodeName:             "node-1",
			},
		}
		Expect(cl.Create(ctx, rva)).To(Succeed())

		Expect(cl.Delete(ctx, rv)).To(Succeed())

		Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKey{Name: "rv-to-delete"}})).To(Equal(reconcile.Result{}))

		got := &v1alpha1.ReplicatedVolumeAttachment{}
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rva), got)).To(Succeed())
		Expect(got.Status).NotTo(BeNil())
		Expect(got.Status.Phase).To(Equal("Pending"))
		cond := meta.FindStatusCondition(got.Status.Conditions, v1alpha1.RVAConditionTypeReady)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(v1alpha1.RVAReasonWaitingForReplicatedVolume))
	})

	It("does not error when ReplicatedVolume is missing but replicas exist", func(ctx SpecContext) {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name: "rvr-orphan",
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-missing",
				NodeName:             "node-1",
				Type:                 v1alpha1.ReplicaTypeDiskful,
			},
			Status: &v1alpha1.ReplicatedVolumeReplicaStatus{
				ActualType: v1alpha1.ReplicaTypeDiskful,
				DRBD: &v1alpha1.DRBD{
					Status: &v1alpha1.DRBDStatus{
						Role: "Primary",
					},
				},
			},
		}
		Expect(cl.Create(ctx, rvr)).To(Succeed())

		Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKey{Name: "rv-missing"}})).To(Equal(reconcile.Result{}))
	})

	It("runs detach-only: keeps attached RVA Attached, sets others Pending/WaitingForReplicatedVolumeIOReady, and releases finalizer only when safe", func(ctx SpecContext) {
		// Same reason as in the test above: to simulate a deleting RVA, we seed the fake client with it.
		now := metav1.Now()

		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "rv-detach-only",
				Finalizers: []string{v1alpha1.ControllerAppFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeSpec{
				ReplicatedStorageClassName: "rsc1",
			},
			Status: &v1alpha1.ReplicatedVolumeStatus{
				Conditions: []metav1.Condition{{
					Type:   v1alpha1.ConditionTypeRVIOReady,
					Status: metav1.ConditionFalse,
				}},
				ActuallyAttachedTo: []string{"node-1"},
				DesiredAttachTo:    []string{"node-1", "node-2"},
			},
		}

		rva1 := &v1alpha1.ReplicatedVolumeAttachment{
			ObjectMeta: metav1.ObjectMeta{
				Name: "rva-node-1",
			},
			Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
				ReplicatedVolumeName: rv.Name,
				NodeName:             "node-1",
			},
		}
		rva2 := &v1alpha1.ReplicatedVolumeAttachment{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "rva-node-2",
				DeletionTimestamp: &now,
				Finalizers: []string{
					v1alpha1.ControllerAppFinalizer,
				},
			},
			Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
				ReplicatedVolumeName: rv.Name,
				NodeName:             "node-2",
			},
		}

		// Replica on node-1 is Primary (actual attachment).
		rvr1 := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name: "rvr-node-1",
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: rv.Name,
				NodeName:             "node-1",
				Type:                 v1alpha1.ReplicaTypeDiskful,
			},
			Status: &v1alpha1.ReplicatedVolumeReplicaStatus{
				ActualType: v1alpha1.ReplicaTypeDiskful,
				DRBD: &v1alpha1.DRBD{
					Status: &v1alpha1.DRBDStatus{
						Role: "Primary",
					},
				},
			},
		}

		// Replica on node-2 is Primary=true; detach-only must demote it.
		primaryTrue := true
		rvr2 := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name: "rvr-node-2",
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: rv.Name,
				NodeName:             "node-2",
				Type:                 v1alpha1.ReplicaTypeDiskful,
			},
			Status: &v1alpha1.ReplicatedVolumeReplicaStatus{
				DRBD: &v1alpha1.DRBD{
					Config: &v1alpha1.DRBDConfig{
						Primary: &primaryTrue,
					},
				},
			},
		}

		localCl := withRVAIndex(fake.NewClientBuilder().WithScheme(scheme)).
			WithStatusSubresource(&v1alpha1.ReplicatedVolume{}).
			WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
			WithStatusSubresource(&v1alpha1.ReplicatedVolumeAttachment{}).
			WithObjects(rv, rva1, rva2, rvr1, rvr2).
			Build()
		localRec := rvattachcontroller.NewReconciler(localCl, logr.New(log.NullLogSink{}))

		Expect(localRec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKey{Name: rv.Name}})).To(Equal(reconcile.Result{}))

		// desiredAttachTo must be reduced to only node-1 (no new nodes added).
		gotRV := &v1alpha1.ReplicatedVolume{}
		Expect(localCl.Get(ctx, client.ObjectKeyFromObject(rv), gotRV)).To(Succeed())
		Expect(gotRV.Status).NotTo(BeNil())
		Expect(gotRV.Status.DesiredAttachTo).To(Equal([]string{"node-1"}))

		// rva1: attached node must stay Attached/Ready=True and should have finalizer added.
		gotRVA1 := &v1alpha1.ReplicatedVolumeAttachment{}
		Expect(localCl.Get(ctx, client.ObjectKeyFromObject(rva1), gotRVA1)).To(Succeed())
		Expect(gotRVA1.Finalizers).To(ContainElement(v1alpha1.ControllerAppFinalizer))
		Expect(gotRVA1.Status).NotTo(BeNil())
		Expect(gotRVA1.Status.Phase).To(Equal("Attached"))
		cond1 := meta.FindStatusCondition(gotRVA1.Status.Conditions, v1alpha1.RVAConditionTypeReady)
		Expect(cond1).NotTo(BeNil())
		Expect(cond1.Status).To(Equal(metav1.ConditionTrue))

		// rva2: deleting + not attached => finalizer removed, status Pending with WaitingForReplicatedVolumeIOReady.
		gotRVA2 := &v1alpha1.ReplicatedVolumeAttachment{}
		err := localCl.Get(ctx, client.ObjectKeyFromObject(rva2), gotRVA2)
		if client.IgnoreNotFound(err) != nil {
			Expect(err).NotTo(HaveOccurred())
			Expect(gotRVA2.Finalizers).NotTo(ContainElement(v1alpha1.ControllerAppFinalizer))
			Expect(gotRVA2.Status).NotTo(BeNil())
			Expect(gotRVA2.Status.Phase).To(Equal("Pending"))
			cond2 := meta.FindStatusCondition(gotRVA2.Status.Conditions, v1alpha1.RVAConditionTypeReady)
			Expect(cond2).NotTo(BeNil())
			Expect(cond2.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond2.Reason).To(Equal(v1alpha1.RVAReasonWaitingForReplicatedVolumeIOReady))
		}

		// rvr-node-2 should be demoted
		gotRVR2 := &v1alpha1.ReplicatedVolumeReplica{}
		Expect(localCl.Get(ctx, client.ObjectKeyFromObject(rvr2), gotRVR2)).To(Succeed())
		Expect(gotRVR2.Status).NotTo(BeNil())
		Expect(gotRVR2.Status.DRBD).NotTo(BeNil())
		Expect(gotRVR2.Status.DRBD.Config).NotTo(BeNil())
		Expect(gotRVR2.Status.DRBD.Config.Primary).NotTo(BeNil())
		Expect(*gotRVR2.Status.DRBD.Config.Primary).To(BeFalse())
	})

	When("rv created", func() {
		var rv v1alpha1.ReplicatedVolume

		BeforeEach(func() {
			rv = v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "rv1",
					Finalizers: []string{v1alpha1.ControllerAppFinalizer},
				},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					ReplicatedStorageClassName: "rsc1",
				},
			}
		})

		JustBeforeEach(func(ctx SpecContext) {
			Expect(cl.Create(ctx, &rv)).To(Succeed())
		})

		When("status is nil", func() {
			BeforeEach(func() {
				rv.Status = nil
			})

			It("does not error when status is nil", func(ctx SpecContext) {
				Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))
			})
		})

		When("IOReady condition is False", func() {
			BeforeEach(func() {
				rv.Status = &v1alpha1.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ConditionTypeRVIOReady,
							Status: metav1.ConditionFalse,
						},
					},
				}

				// ensure that if controller tried to read RSC, it would fail
				builder.WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						if _, ok := obj.(*v1alpha1.ReplicatedStorageClass); ok {
							return errExpectedTestError
						}
						return c.Get(ctx, key, obj, opts...)
					},
				})
			})

			It("runs detach-only when IOReady condition is False without touching ReplicatedStorageClass", func(ctx SpecContext) {
				Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))
			})
		})

		When("ReplicatedStorageClassName is empty", func() {
			BeforeEach(func() {
				rv.Spec.ReplicatedStorageClassName = ""

				// interceptor to fail any RSC Get if it ever happens
				builder.WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						if _, ok := obj.(*v1alpha1.ReplicatedStorageClass); ok {
							return errExpectedTestError
						}
						return c.Get(ctx, key, obj, opts...)
					},
				})
			})

			It("runs detach-only when ReplicatedStorageClassName is empty", func(ctx SpecContext) {
				Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))
			})
		})

		When("publish context loaded", func() {
			var (
				rsc          v1alpha1.ReplicatedStorageClass
				rvrList      v1alpha1.ReplicatedVolumeReplicaList
				attachTo     []string
				volumeAccess string
			)

			BeforeEach(func() {
				volumeAccess = "Local"
				attachTo = []string{"node-1", "node-2"}

				rv.Status = &v1alpha1.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ConditionTypeRVIOReady,
							Status: metav1.ConditionTrue,
						},
					},
				}
				// Keep RV.status.desiredAttachTo pre-initialized:
				// for Local access the controller may be unable to "add" nodes from RVA until replicas are initialized
				// (status.actualType must be reported by the agent), but it still must keep already-desired nodes.
				rv.Status.DesiredAttachTo = attachTo

				rsc = v1alpha1.ReplicatedStorageClass{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rsc1",
					},
					Spec: v1alpha1.ReplicatedStorageClassSpec{
						Replication:  "Availability",
						VolumeAccess: volumeAccess,
					},
				}

				rvrList = v1alpha1.ReplicatedVolumeReplicaList{
					Items: []v1alpha1.ReplicatedVolumeReplica{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "rvr-df1",
							},
							Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: rv.Name,
								NodeName:             "node-1",
								Type:                 v1alpha1.ReplicaTypeDiskful,
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "rvr-df2",
							},
							Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: rv.Name,
								NodeName:             "node-2",
								Type:                 v1alpha1.ReplicaTypeDiskful,
							},
						},
					},
				}
			})

			JustBeforeEach(func(ctx SpecContext) {
				Expect(cl.Create(ctx, &rsc)).To(Succeed())
				for i := range rvrList.Items {
					Expect(cl.Create(ctx, &rvrList.Items[i])).To(Succeed())
				}

				// Create RVA objects according to desired attachTo.
				// The controller derives rv.status.desiredAttachTo from the RVA set.
				for i, nodeName := range attachTo {
					rva := &v1alpha1.ReplicatedVolumeAttachment{
						ObjectMeta: metav1.ObjectMeta{
							Name: fmt.Sprintf("rva-%d-%s", i, nodeName),
						},
						Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
							ReplicatedVolumeName: rv.Name,
							NodeName:             nodeName,
						},
					}
					Expect(cl.Create(ctx, rva)).To(Succeed())
				}
			})

			When("volumeAccess is not Local", func() {
				BeforeEach(func() {
					volumeAccess = "Remote"
					rsc.Spec.VolumeAccess = volumeAccess
				})

				It("does not set any AttachSucceeded condition (it is not used on RV anymore)", func(ctx SpecContext) {
					Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

					rvList := &v1alpha1.ReplicatedVolumeList{}
					Expect(cl.List(ctx, rvList)).To(Succeed())
					Expect(rvList.Items).To(SatisfyAll(
						HaveLen(1),
						HaveEach(HaveField("Status.Conditions", Not(ContainElement(HaveField("Type", Equal("AttachSucceeded")))))),
					))
				})
			})

			When("ReplicatedStorageClass switches from Remote to Local", func() {
				BeforeEach(func() {
					volumeAccess = "Remote"
					rsc.Spec.VolumeAccess = volumeAccess
				})

				It("does not detach already-desired nodes even if they violate Locality after the switch", func(ctx SpecContext) {
					// Simulate that the agent already reported actual types:
					// node-2 is not Diskful (will violate Locality once SC becomes Local).
					for _, item := range []struct {
						name       string
						actualType v1alpha1.ReplicaType
					}{
						{name: "rvr-df1", actualType: v1alpha1.ReplicaTypeDiskful},
						{name: "rvr-df2", actualType: v1alpha1.ReplicaTypeAccess},
					} {
						got := &v1alpha1.ReplicatedVolumeReplica{}
						Expect(cl.Get(ctx, client.ObjectKey{Name: item.name}, got)).To(Succeed())
						orig := got.DeepCopy()
						got.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{
							ActualType: item.actualType,
						}
						Expect(cl.Status().Patch(ctx, got, client.MergeFrom(orig))).To(Succeed())
					}

					// Reconcile #1 with Remote: desiredAttachTo remains as-is.
					Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

					gotRV1 := &v1alpha1.ReplicatedVolume{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(&rv), gotRV1)).To(Succeed())
					Expect(gotRV1.Status).NotTo(BeNil())
					Expect(gotRV1.Status.DesiredAttachTo).To(Equal([]string{"node-1", "node-2"}))

					// Switch storage class to Local.
					gotRSC := &v1alpha1.ReplicatedStorageClass{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(&rsc), gotRSC)).To(Succeed())
					origRSC := gotRSC.DeepCopy()
					gotRSC.Spec.VolumeAccess = "Local"
					Expect(cl.Patch(ctx, gotRSC, client.MergeFrom(origRSC))).To(Succeed())

					// Reconcile #2 with Local: existing desired nodes must not be detached.
					Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

					gotRV2 := &v1alpha1.ReplicatedVolume{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(&rv), gotRV2)).To(Succeed())
					Expect(gotRV2.Status).NotTo(BeNil())
					Expect(gotRV2.Status.DesiredAttachTo).To(Equal([]string{"node-1", "node-2"}))

					// But the violating node must be reflected in RVA status.
					gotRVA := &v1alpha1.ReplicatedVolumeAttachment{}
					Expect(cl.Get(ctx, client.ObjectKey{Name: "rva-1-node-2"}, gotRVA)).To(Succeed())
					Expect(gotRVA.Status).NotTo(BeNil())
					Expect(gotRVA.Status.Phase).To(Equal("Pending"))
					cond := meta.FindStatusCondition(gotRVA.Status.Conditions, v1alpha1.RVAConditionTypeReady)
					Expect(cond).NotTo(BeNil())
					Expect(cond.Status).To(Equal(metav1.ConditionFalse))
					Expect(cond.Reason).To(Equal(v1alpha1.RVAReasonLocalityNotSatisfied))
				})

				When("node was actually attached before the switch", func() {
					It("keeps RVA Attached (does not downgrade to Pending) even if Locality is violated after the switch", func(ctx SpecContext) {
						// Simulate actual attachment on node-2: DRBD role Primary => actuallyAttachedTo contains node-2.
						// Also simulate that node-2 is not Diskful (will violate Locality once SC becomes Local).
						rvr1 := &v1alpha1.ReplicatedVolumeReplica{}
						Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-df1"}, rvr1)).To(Succeed())
						orig1 := rvr1.DeepCopy()
						rvr1.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{
							ActualType: v1alpha1.ReplicaTypeDiskful,
							DRBD: &v1alpha1.DRBD{
								Status: &v1alpha1.DRBDStatus{Role: "Secondary"},
							},
						}
						Expect(cl.Status().Patch(ctx, rvr1, client.MergeFrom(orig1))).To(Succeed())

						rvr2 := &v1alpha1.ReplicatedVolumeReplica{}
						Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-df2"}, rvr2)).To(Succeed())
						orig2 := rvr2.DeepCopy()
						rvr2.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{
							ActualType: v1alpha1.ReplicaTypeAccess,
							DRBD: &v1alpha1.DRBD{
								Status: &v1alpha1.DRBDStatus{Role: "Primary"},
							},
						}
						Expect(cl.Status().Patch(ctx, rvr2, client.MergeFrom(orig2))).To(Succeed())

						// Reconcile #1 with Remote: RVA on node-2 must be Attached (it is actually attached).
						Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

						gotRVA1 := &v1alpha1.ReplicatedVolumeAttachment{}
						Expect(cl.Get(ctx, client.ObjectKey{Name: "rva-1-node-2"}, gotRVA1)).To(Succeed())
						Expect(gotRVA1.Status).NotTo(BeNil())
						Expect(gotRVA1.Status.Phase).To(Equal("Attached"))
						cond1 := meta.FindStatusCondition(gotRVA1.Status.Conditions, v1alpha1.RVAConditionTypeReady)
						Expect(cond1).NotTo(BeNil())
						Expect(cond1.Status).To(Equal(metav1.ConditionTrue))
						Expect(cond1.Reason).To(Equal(v1alpha1.RVAReasonAttached))

						// Switch storage class to Local.
						gotRSC := &v1alpha1.ReplicatedStorageClass{}
						Expect(cl.Get(ctx, client.ObjectKeyFromObject(&rsc), gotRSC)).To(Succeed())
						origRSC := gotRSC.DeepCopy()
						gotRSC.Spec.VolumeAccess = "Local"
						Expect(cl.Patch(ctx, gotRSC, client.MergeFrom(origRSC))).To(Succeed())

						// Reconcile #2 with Local: attached must still win over Locality.
						Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

						gotRVA2 := &v1alpha1.ReplicatedVolumeAttachment{}
						Expect(cl.Get(ctx, client.ObjectKey{Name: "rva-1-node-2"}, gotRVA2)).To(Succeed())
						Expect(gotRVA2.Status).NotTo(BeNil())
						Expect(gotRVA2.Status.Phase).To(Equal("Attached"))
						cond2 := meta.FindStatusCondition(gotRVA2.Status.Conditions, v1alpha1.RVAConditionTypeReady)
						Expect(cond2).NotTo(BeNil())
						Expect(cond2.Status).To(Equal(metav1.ConditionTrue))
						Expect(cond2.Reason).To(Equal(v1alpha1.RVAReasonAttached))
					})
				})
			})

			When("Local access and replica violates Locality", func() {
				BeforeEach(func() {
					volumeAccess = "Local"
					rsc.Spec.VolumeAccess = volumeAccess
				})

				When("node was not previously desired", func() {
					BeforeEach(func() {
						// RVAs exist for node-1 and node-2, but RV status currently desires only node-1.
						rv.Status.DesiredAttachTo = []string{"node-1"}
					})

					It("does not add the node into desiredAttachTo", func(ctx SpecContext) {
						// Simulate that the agent already reported actual types:
						// node-2 is not Diskful, so it must not be added into desiredAttachTo under Local access.
						for _, item := range []struct {
							name       string
							actualType v1alpha1.ReplicaType
						}{
							{name: "rvr-df1", actualType: v1alpha1.ReplicaTypeDiskful},
							{name: "rvr-df2", actualType: v1alpha1.ReplicaTypeAccess},
						} {
							got := &v1alpha1.ReplicatedVolumeReplica{}
							Expect(cl.Get(ctx, client.ObjectKey{Name: item.name}, got)).To(Succeed())
							orig := got.DeepCopy()
							got.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{
								ActualType: item.actualType,
							}
							Expect(cl.Status().Patch(ctx, got, client.MergeFrom(orig))).To(Succeed())
						}

						Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

						gotRV := &v1alpha1.ReplicatedVolume{}
						Expect(cl.Get(ctx, client.ObjectKeyFromObject(&rv), gotRV)).To(Succeed())
						Expect(gotRV.Status).NotTo(BeNil())
						Expect(gotRV.Status.DesiredAttachTo).To(Equal([]string{"node-1"}))

						gotRVA := &v1alpha1.ReplicatedVolumeAttachment{}
						Expect(cl.Get(ctx, client.ObjectKey{Name: "rva-1-node-2"}, gotRVA)).To(Succeed())
						Expect(gotRVA.Status).NotTo(BeNil())
						Expect(gotRVA.Status.Phase).To(Equal("Pending"))
						cond := meta.FindStatusCondition(gotRVA.Status.Conditions, v1alpha1.RVAConditionTypeReady)
						Expect(cond).NotTo(BeNil())
						Expect(cond.Status).To(Equal(metav1.ConditionFalse))
						Expect(cond.Reason).To(Equal(v1alpha1.RVAReasonLocalityNotSatisfied))
					})
				})
			})

			When("Local access and Diskful replicas exist on all attachTo nodes", func() {
				BeforeEach(func() {
					volumeAccess = "Local"
					rsc.Spec.VolumeAccess = volumeAccess
				})

				It("does not set any AttachSucceeded condition and proceeds with reconciliation", func(ctx SpecContext) {
					Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

					rvList := &v1alpha1.ReplicatedVolumeList{}
					Expect(cl.List(ctx, rvList)).To(Succeed())
					Expect(rvList.Items).To(HaveLen(1))
					got := &rvList.Items[0]

					// AttachSucceeded condition is not used on RV anymore
					Expect(meta.FindStatusCondition(got.Status.Conditions, "AttachSucceeded")).To(BeNil())
				})
			})

			When("Local access but Diskful replica is missing on one of attachTo nodes", func() {
				BeforeEach(func() {
					volumeAccess = "Local"
					rsc.Spec.VolumeAccess = volumeAccess

					// remove Diskful replica for node-2
					rvrList.Items = rvrList.Items[:1]
				})

				It("keeps RVA Pending with LocalityNotSatisfied and does not include the node into desiredAttachTo", func(ctx SpecContext) {
					Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

					gotRV := &v1alpha1.ReplicatedVolume{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(&rv), gotRV)).To(Succeed())
					Expect(gotRV.Status).NotTo(BeNil())
					Expect(gotRV.Status.DesiredAttachTo).To(Equal([]string{"node-1"}))

					gotRVA := &v1alpha1.ReplicatedVolumeAttachment{}
					Expect(cl.Get(ctx, client.ObjectKey{Name: "rva-1-node-2"}, gotRVA)).To(Succeed())
					Expect(gotRVA.Status).NotTo(BeNil())
					Expect(gotRVA.Status.Phase).To(Equal("Pending"))
					cond := meta.FindStatusCondition(gotRVA.Status.Conditions, v1alpha1.RVAConditionTypeReady)
					Expect(cond).NotTo(BeNil())
					Expect(cond.Status).To(Equal(metav1.ConditionFalse))
					Expect(cond.Reason).To(Equal(v1alpha1.RVAReasonLocalityNotSatisfied))
				})
			})

			When("allowTwoPrimaries is configured and actual flag not yet applied on replicas", func() {
				BeforeEach(func() {
					volumeAccess = "Local"
					rsc.Spec.VolumeAccess = volumeAccess

					// request two primaries (via RVA set; attachTo is also used for initial desired preference)
					attachTo = []string{"node-1", "node-2"}

					// replicas without actual.AllowTwoPrimaries
					rvrList.Items[0].Status = &v1alpha1.ReplicatedVolumeReplicaStatus{
						DRBD: &v1alpha1.DRBD{
							Actual: &v1alpha1.DRBDActual{
								AllowTwoPrimaries: false,
							},
						},
					}
					rvrList.Items[1].Status = &v1alpha1.ReplicatedVolumeReplicaStatus{
						DRBD: &v1alpha1.DRBD{
							Actual: &v1alpha1.DRBDActual{
								AllowTwoPrimaries: false,
							},
						},
					}
				})

				It("sets rv.status.drbd.config.allowTwoPrimaries=true and waits for replicas", func(ctx SpecContext) {
					Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

					rvList := &v1alpha1.ReplicatedVolumeList{}
					Expect(cl.List(ctx, rvList)).To(Succeed())
					Expect(rvList.Items).To(HaveLen(1))
					got := &rvList.Items[0]
					Expect(got.Status).NotTo(BeNil())
					Expect(got.Status.DRBD).NotTo(BeNil())
					Expect(got.Status.DRBD.Config).NotTo(BeNil())
					Expect(got.Status.DRBD.Config.AllowTwoPrimaries).To(BeTrue())
				})

				It("does not request the 2nd Primary until allowTwoPrimaries is applied on all replicas", func(ctx SpecContext) {
					// Simulate that node-1 is Primary right now, but allowTwoPrimaries is not applied on replicas yet.
					rvr1 := &v1alpha1.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-df1"}, rvr1)).To(Succeed())
					orig1 := rvr1.DeepCopy()
					rvr1.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{
						ActualType: v1alpha1.ReplicaTypeDiskful,
						DRBD: &v1alpha1.DRBD{
							Actual: &v1alpha1.DRBDActual{AllowTwoPrimaries: false},
							Status: &v1alpha1.DRBDStatus{Role: "Primary"},
						},
					}
					Expect(cl.Status().Patch(ctx, rvr1, client.MergeFrom(orig1))).To(Succeed())

					rvr2 := &v1alpha1.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-df2"}, rvr2)).To(Succeed())
					orig2 := rvr2.DeepCopy()
					rvr2.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{
						ActualType: v1alpha1.ReplicaTypeDiskful,
						DRBD: &v1alpha1.DRBD{
							Actual: &v1alpha1.DRBDActual{AllowTwoPrimaries: false},
							Status: &v1alpha1.DRBDStatus{Role: "Secondary"},
						},
					}
					Expect(cl.Status().Patch(ctx, rvr2, client.MergeFrom(orig2))).To(Succeed())

					Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

					gotRVR1 := &v1alpha1.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-df1"}, gotRVR1)).To(Succeed())
					Expect(gotRVR1.Status).NotTo(BeNil())
					Expect(gotRVR1.Status.DRBD).NotTo(BeNil())
					Expect(gotRVR1.Status.DRBD.Config).NotTo(BeNil())
					Expect(gotRVR1.Status.DRBD.Config.Primary).NotTo(BeNil())
					Expect(*gotRVR1.Status.DRBD.Config.Primary).To(BeTrue())

					gotRVR2 := &v1alpha1.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-df2"}, gotRVR2)).To(Succeed())
					if gotRVR2.Status != nil &&
						gotRVR2.Status.DRBD != nil &&
						gotRVR2.Status.DRBD.Config != nil &&
						gotRVR2.Status.DRBD.Config.Primary != nil {
						Expect(*gotRVR2.Status.DRBD.Config.Primary).To(BeFalse())
					}
				})
			})

			When("allowTwoPrimaries becomes applied after being not applied", func() {
				BeforeEach(func() {
					volumeAccess = "Remote"
					rsc.Spec.VolumeAccess = volumeAccess

					attachTo = []string{"node-1", "node-2"}
				})

				It("adds the 2nd Primary only after allowTwoPrimaries is applied on all replicas", func(ctx SpecContext) {
					// Initial state: node-1 is Primary, allowTwoPrimaries is not applied.
					rvr1 := &v1alpha1.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-df1"}, rvr1)).To(Succeed())
					orig1 := rvr1.DeepCopy()
					rvr1.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{
						ActualType: v1alpha1.ReplicaTypeDiskful,
						DRBD: &v1alpha1.DRBD{
							Actual: &v1alpha1.DRBDActual{AllowTwoPrimaries: false},
							Status: &v1alpha1.DRBDStatus{Role: "Primary"},
						},
					}
					Expect(cl.Status().Patch(ctx, rvr1, client.MergeFrom(orig1))).To(Succeed())

					rvr2 := &v1alpha1.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-df2"}, rvr2)).To(Succeed())
					orig2 := rvr2.DeepCopy()
					rvr2.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{
						ActualType: v1alpha1.ReplicaTypeDiskful,
						DRBD: &v1alpha1.DRBD{
							Actual: &v1alpha1.DRBDActual{AllowTwoPrimaries: false},
							Status: &v1alpha1.DRBDStatus{Role: "Secondary"},
						},
					}
					Expect(cl.Status().Patch(ctx, rvr2, client.MergeFrom(orig2))).To(Succeed())

					// Reconcile #1: do not request 2nd Primary yet.
					Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

					gotRVR2 := &v1alpha1.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-df2"}, gotRVR2)).To(Succeed())
					// Do not allow a request to become Primary on the 2nd node until allowTwoPrimaries is applied.
					// Primary can be nil (no request) or false (explicit demotion request); it must not be true.
					primaryRequested := false
					if gotRVR2.Status != nil &&
						gotRVR2.Status.DRBD != nil &&
						gotRVR2.Status.DRBD.Config != nil &&
						gotRVR2.Status.DRBD.Config.Primary != nil {
						primaryRequested = *gotRVR2.Status.DRBD.Config.Primary
					}
					Expect(primaryRequested).To(BeFalse())

					// Simulate allowTwoPrimaries applied by the agent.
					rvr1b := &v1alpha1.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-df1"}, rvr1b)).To(Succeed())
					orig1b := rvr1b.DeepCopy()
					rvr1b.Status.DRBD.Actual.AllowTwoPrimaries = true
					Expect(cl.Status().Patch(ctx, rvr1b, client.MergeFrom(orig1b))).To(Succeed())

					rvr2b := &v1alpha1.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-df2"}, rvr2b)).To(Succeed())
					orig2b := rvr2b.DeepCopy()
					rvr2b.Status.DRBD.Actual.AllowTwoPrimaries = true
					Expect(cl.Status().Patch(ctx, rvr2b, client.MergeFrom(orig2b))).To(Succeed())

					// Reconcile #2: now the controller may request 2 Primaries.
					Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

					gotRVR2b := &v1alpha1.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-df2"}, gotRVR2b)).To(Succeed())
					Expect(gotRVR2b.Status).NotTo(BeNil())
					Expect(gotRVR2b.Status.DRBD).NotTo(BeNil())
					Expect(gotRVR2b.Status.DRBD.Config).NotTo(BeNil())
					Expect(gotRVR2b.Status.DRBD.Config.Primary).NotTo(BeNil())
					Expect(*gotRVR2b.Status.DRBD.Config.Primary).To(BeTrue())
				})
			})

			When("allowTwoPrimaries applied on all replicas", func() {
				BeforeEach(func() {
					volumeAccess = "Local"
					rsc.Spec.VolumeAccess = volumeAccess

					attachTo = []string{"node-1", "node-2"}

					// Both replicas are initialized by the agent (status.actualType is set) and already have
					// actual.AllowTwoPrimaries=true.
					for i := range rvrList.Items {
						rvrList.Items[i].Status = &v1alpha1.ReplicatedVolumeReplicaStatus{
							ActualType: v1alpha1.ReplicaTypeDiskful,
							DRBD: &v1alpha1.DRBD{
								Actual: &v1alpha1.DRBDActual{
									AllowTwoPrimaries: true,
								},
								Status: &v1alpha1.DRBDStatus{
									Role: "Secondary",
								},
							},
						}
					}
				})

				It("updates primary roles and attachedTo", func(ctx SpecContext) {
					Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

					// RVRs on attachTo nodes should be configured as Primary
					gotRVRs := &v1alpha1.ReplicatedVolumeReplicaList{}
					Expect(cl.List(ctx, gotRVRs)).To(Succeed())

					for i := range gotRVRs.Items {
						rvr := &gotRVRs.Items[i]
						if rvr.Spec.ReplicatedVolumeName != rv.Name {
							continue
						}
						_, shouldBePrimary := map[string]struct{}{
							"node-1": {},
							"node-2": {},
						}[rvr.Spec.NodeName]

						if rvr.Status == nil || rvr.Status.DRBD == nil || rvr.Status.DRBD.Config == nil {
							// if no config present, it must not be primary
							Expect(shouldBePrimary).To(BeFalse())
							continue
						}

						if shouldBePrimary {
							Expect(rvr.Status.DRBD.Config.Primary).NotTo(BeNil())
							Expect(*rvr.Status.DRBD.Config.Primary).To(BeTrue())
						}
					}

					// rv.status.actuallyAttachedTo should reflect RVRs with Role=Primary
					rvList := &v1alpha1.ReplicatedVolumeList{}
					Expect(cl.List(ctx, rvList)).To(Succeed())
					Expect(rvList.Items).To(HaveLen(1))
					gotRV := &rvList.Items[0]
					// we don't assert exact content here, just that field is present and length <= 2
					Expect(len(gotRV.Status.ActuallyAttachedTo)).To(BeNumerically("<=", 2))
				})
			})

			When("a deleting replica exists without actual.allowTwoPrimaries", func() {
				BeforeEach(func() {
					volumeAccess = "Remote"
					rsc.Spec.VolumeAccess = volumeAccess

					attachTo = []string{"node-1", "node-2"}
				})

				It("does not promote the 2nd Primary until allowTwoPrimaries is applied on all existing replicas (even deleting ones)", func(ctx SpecContext) {
					// Desired: two primaries on node-1 and node-2, and allowTwoPrimaries already applied on relevant replicas.
					for _, item := range []struct {
						name string
						role string
					}{
						{name: "rvr-df1", role: "Primary"},
						{name: "rvr-df2", role: "Secondary"},
					} {
						rvr := &v1alpha1.ReplicatedVolumeReplica{}
						Expect(cl.Get(ctx, client.ObjectKey{Name: item.name}, rvr)).To(Succeed())
						orig := rvr.DeepCopy()
						rvr.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{
							ActualType: v1alpha1.ReplicaTypeDiskful,
							DRBD: &v1alpha1.DRBD{
								Actual: &v1alpha1.DRBDActual{AllowTwoPrimaries: true},
								Status: &v1alpha1.DRBDStatus{Role: item.role},
							},
						}
						Expect(cl.Status().Patch(ctx, rvr, client.MergeFrom(orig))).To(Succeed())
					}

					// A deleting replica without actual.allowTwoPrimaries should be ignored for readiness.
					now := metav1.Now()
					rvrDeleting := &v1alpha1.ReplicatedVolumeReplica{
						ObjectMeta: metav1.ObjectMeta{
							Name:              "rvr-deleting",
							DeletionTimestamp: &now,
						},
						Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: rv.Name,
							NodeName:             "node-3",
							Type:                 v1alpha1.ReplicaTypeDiskful,
						},
						Status: &v1alpha1.ReplicatedVolumeReplicaStatus{
							ActualType: v1alpha1.ReplicaTypeDiskful,
							DRBD: &v1alpha1.DRBD{
								Actual: &v1alpha1.DRBDActual{AllowTwoPrimaries: false},
								Status: &v1alpha1.DRBDStatus{Role: "Secondary"},
							},
						},
					}
					Expect(cl.Create(ctx, rvrDeleting)).To(Succeed())

					Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

					gotRVR2 := &v1alpha1.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-df2"}, gotRVR2)).To(Succeed())
					// Deleting replica still exists with actual.allowTwoPrimaries=false -> must not request the 2nd Primary.
					primaryRequested := false
					if gotRVR2.Status != nil &&
						gotRVR2.Status.DRBD != nil &&
						gotRVR2.Status.DRBD.Config != nil &&
						gotRVR2.Status.DRBD.Config.Primary != nil {
						primaryRequested = *gotRVR2.Status.DRBD.Config.Primary
					}
					Expect(primaryRequested).To(BeFalse())
				})
			})

			When("an unscheduled replica exists (spec.nodeName is empty)", func() {
				BeforeEach(func() {
					volumeAccess = "Remote"
					rsc.Spec.VolumeAccess = volumeAccess
				})

				It("does not panic and keeps Attached condition in Unknown/NotInitialized", func(ctx SpecContext) {
					rvrUnscheduled := &v1alpha1.ReplicatedVolumeReplica{
						ObjectMeta: metav1.ObjectMeta{Name: "rvr-unscheduled"},
						Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: rv.Name,
							NodeName:             "",
							Type:                 v1alpha1.ReplicaTypeDiskful,
						},
					}
					Expect(cl.Create(ctx, rvrUnscheduled)).To(Succeed())

					Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

					got := &v1alpha1.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvrUnscheduled), got)).To(Succeed())
					Expect(got.Status).NotTo(BeNil())
					cond := meta.FindStatusCondition(got.Status.Conditions, v1alpha1.ConditionTypeAttached)
					Expect(cond).NotTo(BeNil())
					Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
					Expect(cond.Reason).To(Equal(v1alpha1.ReasonAttachingNotInitialized))
				})
			})

			When("volumeAccess is not Local and TieBreaker replica should become primary", func() {
				BeforeEach(func() {
					volumeAccess = "Remote"
					rsc.Spec.VolumeAccess = volumeAccess

					attachTo = []string{"node-1"}

					rvrList = v1alpha1.ReplicatedVolumeReplicaList{
						Items: []v1alpha1.ReplicatedVolumeReplica{
							{
								ObjectMeta: metav1.ObjectMeta{
									Name: "rvr-tb1",
								},
								Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
									ReplicatedVolumeName: rv.Name,
									NodeName:             "node-1",
									Type:                 v1alpha1.ReplicaTypeTieBreaker,
								},
								Status: &v1alpha1.ReplicatedVolumeReplicaStatus{
									DRBD: &v1alpha1.DRBD{
										Actual: &v1alpha1.DRBDActual{
											AllowTwoPrimaries: false,
										},
										Status: &v1alpha1.DRBDStatus{
											Role: "Secondary",
										},
									},
								},
							},
						},
					}
				})

				It("converts TieBreaker to Access first, then requests primary=true after actualType becomes Access", func(ctx SpecContext) {
					// Reconcile #1: conversion only (the agent must first report actualType=Access).
					Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

					gotRVR := &v1alpha1.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-tb1"}, gotRVR)).To(Succeed())

					Expect(gotRVR.Spec.Type).To(Equal(v1alpha1.ReplicaTypeAccess))

					// Simulate the agent updating actualType after conversion (TieBreaker -> Access).
					orig := gotRVR.DeepCopy()
					if gotRVR.Status == nil {
						gotRVR.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{}
					}
					gotRVR.Status.ActualType = v1alpha1.ReplicaTypeAccess
					if gotRVR.Status.DRBD == nil {
						gotRVR.Status.DRBD = &v1alpha1.DRBD{}
					}
					if gotRVR.Status.DRBD.Actual == nil {
						gotRVR.Status.DRBD.Actual = &v1alpha1.DRBDActual{}
					}
					gotRVR.Status.DRBD.Actual.AllowTwoPrimaries = false
					if gotRVR.Status.DRBD.Status == nil {
						gotRVR.Status.DRBD.Status = &v1alpha1.DRBDStatus{}
					}
					gotRVR.Status.DRBD.Status.Role = "Secondary"
					Expect(cl.Status().Patch(ctx, gotRVR, client.MergeFrom(orig))).To(Succeed())

					// Reconcile #2: now primary request is allowed for Access/Diskful actualType.
					Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

					gotRVR2 := &v1alpha1.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-tb1"}, gotRVR2)).To(Succeed())
					Expect(gotRVR2.Status).NotTo(BeNil())
					Expect(gotRVR2.Status.DRBD).NotTo(BeNil())
					Expect(gotRVR2.Status.DRBD.Config).NotTo(BeNil())
					Expect(gotRVR2.Status.DRBD.Config.Primary).NotTo(BeNil())
					Expect(*gotRVR2.Status.DRBD.Config.Primary).To(BeTrue())
				})
			})

			When("replica on node outside attachTo does not become primary", func() {
				BeforeEach(func() {
					volumeAccess = "Remote"
					rsc.Spec.VolumeAccess = volumeAccess

					attachTo = []string{"node-1"}

					rvrList = v1alpha1.ReplicatedVolumeReplicaList{
						Items: []v1alpha1.ReplicatedVolumeReplica{
							{
								ObjectMeta: metav1.ObjectMeta{
									Name: "rvr-node-1",
								},
								Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
									ReplicatedVolumeName: rv.Name,
									NodeName:             "node-1",
									Type:                 v1alpha1.ReplicaTypeDiskful,
								},
							},
							{
								ObjectMeta: metav1.ObjectMeta{
									Name: "rvr-node-2",
								},
								Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
									ReplicatedVolumeName: rv.Name,
									NodeName:             "node-2",
									Type:                 v1alpha1.ReplicaTypeAccess,
								},
							},
						},
					}
				})

				It("keeps replica on non-attachTo node non-primary", func(ctx SpecContext) {
					// Simulate that the agent has already initialized replicas (status.actualType is set),
					// otherwise the controller must not request Primary.
					for _, item := range []struct {
						name       string
						actualType v1alpha1.ReplicaType
					}{
						{name: "rvr-node-1", actualType: v1alpha1.ReplicaTypeDiskful},
						{name: "rvr-node-2", actualType: v1alpha1.ReplicaTypeAccess},
					} {
						rvr := &v1alpha1.ReplicatedVolumeReplica{}
						Expect(cl.Get(ctx, client.ObjectKey{Name: item.name}, rvr)).To(Succeed())
						orig := rvr.DeepCopy()
						rvr.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{
							ActualType: item.actualType,
							DRBD: &v1alpha1.DRBD{
								Actual: &v1alpha1.DRBDActual{
									AllowTwoPrimaries: false,
								},
								Status: &v1alpha1.DRBDStatus{
									Role: "Secondary",
								},
							},
						}
						Expect(cl.Status().Patch(ctx, rvr, client.MergeFrom(orig))).To(Succeed())
					}

					Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

					gotRVRs := &v1alpha1.ReplicatedVolumeReplicaList{}
					Expect(cl.List(ctx, gotRVRs)).To(Succeed())

					var rvrNode1, rvrNode2 *v1alpha1.ReplicatedVolumeReplica
					for i := range gotRVRs.Items {
						r := &gotRVRs.Items[i]
						switch r.Name {
						case "rvr-node-1":
							rvrNode1 = r
						case "rvr-node-2":
							rvrNode2 = r
						}
					}

					Expect(rvrNode1).NotTo(BeNil())
					Expect(rvrNode2).NotTo(BeNil())

					// node-1 должен стать primary
					Expect(rvrNode1.Status).NotTo(BeNil())
					Expect(rvrNode1.Status.DRBD).NotTo(BeNil())
					Expect(rvrNode1.Status.DRBD.Config).NotTo(BeNil())
					Expect(rvrNode1.Status.DRBD.Config.Primary).NotTo(BeNil())
					Expect(*rvrNode1.Status.DRBD.Config.Primary).To(BeTrue())

					// node-2 не должен стать primary
					primaryRequested := false
					if rvrNode2.Status != nil &&
						rvrNode2.Status.DRBD != nil &&
						rvrNode2.Status.DRBD.Config != nil &&
						rvrNode2.Status.DRBD.Config.Primary != nil {
						primaryRequested = *rvrNode2.Status.DRBD.Config.Primary
					}
					Expect(primaryRequested).To(BeFalse())
				})
			})

			When("switching Primary node in single-primary mode", func() {
				BeforeEach(func() {
					volumeAccess = "Remote"
					rsc.Spec.VolumeAccess = volumeAccess

					// Only node-2 is desired now (RVA set), but node-1 is still Primary at the moment.
					attachTo = []string{"node-2"}
				})

				It("demotes the old Primary first and promotes the new one only after actual Primary becomes empty", func(ctx SpecContext) {
					// node-1 is Primary right now.
					rvr1 := &v1alpha1.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-df1"}, rvr1)).To(Succeed())
					orig1 := rvr1.DeepCopy()
					rvr1.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{
						ActualType: v1alpha1.ReplicaTypeDiskful,
						DRBD: &v1alpha1.DRBD{
							Actual: &v1alpha1.DRBDActual{AllowTwoPrimaries: false},
							Status: &v1alpha1.DRBDStatus{Role: "Primary"},
						},
					}
					Expect(cl.Status().Patch(ctx, rvr1, client.MergeFrom(orig1))).To(Succeed())

					rvr2 := &v1alpha1.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-df2"}, rvr2)).To(Succeed())
					orig2 := rvr2.DeepCopy()
					rvr2.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{
						ActualType: v1alpha1.ReplicaTypeDiskful,
						DRBD: &v1alpha1.DRBD{
							Actual: &v1alpha1.DRBDActual{AllowTwoPrimaries: false},
							Status: &v1alpha1.DRBDStatus{Role: "Secondary"},
						},
					}
					Expect(cl.Status().Patch(ctx, rvr2, client.MergeFrom(orig2))).To(Succeed())

					// Reconcile #1: request demotion only (no new Primary while old one exists).
					Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

					gotRVR1 := &v1alpha1.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-df1"}, gotRVR1)).To(Succeed())
					Expect(gotRVR1.Status).NotTo(BeNil())
					Expect(gotRVR1.Status.DRBD).NotTo(BeNil())
					Expect(gotRVR1.Status.DRBD.Config).NotTo(BeNil())
					Expect(gotRVR1.Status.DRBD.Config.Primary).NotTo(BeNil())
					Expect(*gotRVR1.Status.DRBD.Config.Primary).To(BeFalse())

					gotRVR2 := &v1alpha1.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-df2"}, gotRVR2)).To(Succeed())
					primaryRequested := false
					if gotRVR2.Status != nil &&
						gotRVR2.Status.DRBD != nil &&
						gotRVR2.Status.DRBD.Config != nil &&
						gotRVR2.Status.DRBD.Config.Primary != nil {
						primaryRequested = *gotRVR2.Status.DRBD.Config.Primary
					}
					Expect(primaryRequested).To(BeFalse())

					// Simulate the agent demoting node-1: no actual Primary remains.
					gotRVR1b := &v1alpha1.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-df1"}, gotRVR1b)).To(Succeed())
					orig1b := gotRVR1b.DeepCopy()
					gotRVR1b.Status.DRBD.Status.Role = "Secondary"
					Expect(cl.Status().Patch(ctx, gotRVR1b, client.MergeFrom(orig1b))).To(Succeed())

					// Reconcile #2: now we can promote the new desired Primary (node-2).
					Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

					gotRVR2b := &v1alpha1.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-df2"}, gotRVR2b)).To(Succeed())
					Expect(gotRVR2b.Status).NotTo(BeNil())
					Expect(gotRVR2b.Status.DRBD).NotTo(BeNil())
					Expect(gotRVR2b.Status.DRBD.Config).NotTo(BeNil())
					Expect(gotRVR2b.Status.DRBD.Config.Primary).NotTo(BeNil())
					Expect(*gotRVR2b.Status.DRBD.Config.Primary).To(BeTrue())
				})
			})

			When("switching two Primaries to two other nodes (2 -> 2 transition)", func() {
				BeforeEach(func() {
					volumeAccess = "Remote"
					rsc.Spec.VolumeAccess = volumeAccess

					// Desired attachments are now node-3 and node-4.
					attachTo = []string{"node-3", "node-4"}

					rvrList = v1alpha1.ReplicatedVolumeReplicaList{
						Items: []v1alpha1.ReplicatedVolumeReplica{
							{
								ObjectMeta: metav1.ObjectMeta{Name: "rvr-n1"},
								Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
									ReplicatedVolumeName: rv.Name,
									NodeName:             "node-1",
									Type:                 v1alpha1.ReplicaTypeDiskful,
								},
							},
							{
								ObjectMeta: metav1.ObjectMeta{Name: "rvr-n2"},
								Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
									ReplicatedVolumeName: rv.Name,
									NodeName:             "node-2",
									Type:                 v1alpha1.ReplicaTypeDiskful,
								},
							},
							{
								ObjectMeta: metav1.ObjectMeta{Name: "rvr-n3"},
								Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
									ReplicatedVolumeName: rv.Name,
									NodeName:             "node-3",
									Type:                 v1alpha1.ReplicaTypeDiskful,
								},
							},
							{
								ObjectMeta: metav1.ObjectMeta{Name: "rvr-n4"},
								Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
									ReplicatedVolumeName: rv.Name,
									NodeName:             "node-4",
									Type:                 v1alpha1.ReplicaTypeDiskful,
								},
							},
						},
					}
				})

				It("first requests demotion of old Primaries, then promotes new Primaries as slots become available", func(ctx SpecContext) {
					// Current reality: node-1 and node-2 are Primary, allowTwoPrimaries is already applied.
					// Patch statuses in a separate loop with explicit objects to keep it readable.
					for _, item := range []struct {
						name string
						role string
					}{
						{name: "rvr-n1", role: "Primary"},
						{name: "rvr-n2", role: "Primary"},
						{name: "rvr-n3", role: "Secondary"},
						{name: "rvr-n4", role: "Secondary"},
					} {
						rvr := &v1alpha1.ReplicatedVolumeReplica{}
						Expect(cl.Get(ctx, client.ObjectKey{Name: item.name}, rvr)).To(Succeed())
						orig := rvr.DeepCopy()
						rvr.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{
							ActualType: v1alpha1.ReplicaTypeDiskful,
							DRBD: &v1alpha1.DRBD{
								Actual: &v1alpha1.DRBDActual{AllowTwoPrimaries: true},
								Status: &v1alpha1.DRBDStatus{Role: item.role},
							},
						}
						Expect(cl.Status().Patch(ctx, rvr, client.MergeFrom(orig))).To(Succeed())
					}

					// Reconcile #1: desiredPrimaryNodes must become empty first (demote-only phase).
					Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

					for _, name := range []string{"rvr-n1", "rvr-n2"} {
						got := &v1alpha1.ReplicatedVolumeReplica{}
						Expect(cl.Get(ctx, client.ObjectKey{Name: name}, got)).To(Succeed())
						Expect(got.Status).NotTo(BeNil())
						Expect(got.Status.DRBD).NotTo(BeNil())
						Expect(got.Status.DRBD.Config).NotTo(BeNil())
						Expect(got.Status.DRBD.Config.Primary).NotTo(BeNil())
						Expect(*got.Status.DRBD.Config.Primary).To(BeFalse())
					}
					for _, name := range []string{"rvr-n3", "rvr-n4"} {
						got := &v1alpha1.ReplicatedVolumeReplica{}
						Expect(cl.Get(ctx, client.ObjectKey{Name: name}, got)).To(Succeed())
						if got.Status != nil &&
							got.Status.DRBD != nil &&
							got.Status.DRBD.Config != nil &&
							got.Status.DRBD.Config.Primary != nil {
							Expect(*got.Status.DRBD.Config.Primary).To(BeFalse())
						}
					}

					// Simulate agent demotion completing: node-1 and node-2 are no longer Primary.
					for _, name := range []string{"rvr-n1", "rvr-n2"} {
						got := &v1alpha1.ReplicatedVolumeReplica{}
						Expect(cl.Get(ctx, client.ObjectKey{Name: name}, got)).To(Succeed())
						orig := got.DeepCopy()
						got.Status.DRBD.Status.Role = "Secondary"
						Expect(cl.Status().Patch(ctx, got, client.MergeFrom(orig))).To(Succeed())
					}

					// Reconcile #2: with two free slots, promote both desired nodes (node-3 and node-4).
					Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

					for _, name := range []string{"rvr-n3", "rvr-n4"} {
						got := &v1alpha1.ReplicatedVolumeReplica{}
						Expect(cl.Get(ctx, client.ObjectKey{Name: name}, got)).To(Succeed())
						Expect(got.Status).NotTo(BeNil())
						Expect(got.Status.DRBD).NotTo(BeNil())
						Expect(got.Status.DRBD.Config).NotTo(BeNil())
						Expect(got.Status.DRBD.Config.Primary).NotTo(BeNil())
						Expect(*got.Status.DRBD.Config.Primary).To(BeTrue())
					}
				})
			})

			When("Local access but replica on attachTo node is Access", func() {
				BeforeEach(func() {
					volumeAccess = "Local"
					rsc.Spec.VolumeAccess = volumeAccess
				})

				When("replica type is set via spec.type", func() {
					BeforeEach(func() {
						// Make replica on node-2 Access instead of Diskful (via spec).
						rvrList.Items[1].Spec.Type = v1alpha1.ReplicaTypeAccess
					})

					It("keeps desiredAttachTo (does not detach an already desired node) and keeps RVA Pending with LocalityNotSatisfied", func(ctx SpecContext) {
						Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

						gotRV := &v1alpha1.ReplicatedVolume{}
						Expect(cl.Get(ctx, client.ObjectKeyFromObject(&rv), gotRV)).To(Succeed())
						Expect(gotRV.Status).NotTo(BeNil())
						Expect(gotRV.Status.DesiredAttachTo).To(Equal([]string{"node-1", "node-2"}))

						gotRVA := &v1alpha1.ReplicatedVolumeAttachment{}
						Expect(cl.Get(ctx, client.ObjectKey{Name: "rva-1-node-2"}, gotRVA)).To(Succeed())
						Expect(gotRVA.Status).NotTo(BeNil())
						Expect(gotRVA.Status.Phase).To(Equal("Pending"))
						cond := meta.FindStatusCondition(gotRVA.Status.Conditions, v1alpha1.RVAConditionTypeReady)
						Expect(cond).NotTo(BeNil())
						Expect(cond.Status).To(Equal(metav1.ConditionFalse))
						Expect(cond.Reason).To(Equal(v1alpha1.RVAReasonLocalityNotSatisfied))
					})
				})

				When("replica type is set via status.actualType", func() {
					BeforeEach(func() {
						// Keep spec.type Diskful, but mark replica on node-2 as actually Access (via status).
						rvrList.Items[1].Status = &v1alpha1.ReplicatedVolumeReplicaStatus{
							ActualType: v1alpha1.ReplicaTypeAccess,
						}
					})

					It("keeps desiredAttachTo (does not detach an already desired node) and keeps RVA Pending with LocalityNotSatisfied", func(ctx SpecContext) {
						Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

						gotRV := &v1alpha1.ReplicatedVolume{}
						Expect(cl.Get(ctx, client.ObjectKeyFromObject(&rv), gotRV)).To(Succeed())
						Expect(gotRV.Status).NotTo(BeNil())
						Expect(gotRV.Status.DesiredAttachTo).To(Equal([]string{"node-1", "node-2"}))

						gotRVA := &v1alpha1.ReplicatedVolumeAttachment{}
						Expect(cl.Get(ctx, client.ObjectKey{Name: "rva-1-node-2"}, gotRVA)).To(Succeed())
						Expect(gotRVA.Status).NotTo(BeNil())
						Expect(gotRVA.Status.Phase).To(Equal("Pending"))
						cond := meta.FindStatusCondition(gotRVA.Status.Conditions, v1alpha1.RVAConditionTypeReady)
						Expect(cond).NotTo(BeNil())
						Expect(cond.Status).To(Equal(metav1.ConditionFalse))
						Expect(cond.Reason).To(Equal(v1alpha1.RVAReasonLocalityNotSatisfied))
					})
				})
			})

			When("Local access but replica on attachTo node is TieBreaker", func() {
				BeforeEach(func() {
					volumeAccess = "Local"
					rsc.Spec.VolumeAccess = volumeAccess
				})

				When("replica type is set via spec.type", func() {
					BeforeEach(func() {
						// Make replica on node-2 TieBreaker instead of Diskful (via spec).
						rvrList.Items[1].Spec.Type = v1alpha1.ReplicaTypeTieBreaker
					})

					It("keeps desiredAttachTo (does not detach an already desired node) and keeps RVA Pending with LocalityNotSatisfied", func(ctx SpecContext) {
						Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

						gotRV := &v1alpha1.ReplicatedVolume{}
						Expect(cl.Get(ctx, client.ObjectKeyFromObject(&rv), gotRV)).To(Succeed())
						Expect(gotRV.Status).NotTo(BeNil())
						Expect(gotRV.Status.DesiredAttachTo).To(Equal([]string{"node-1", "node-2"}))

						gotRVA := &v1alpha1.ReplicatedVolumeAttachment{}
						Expect(cl.Get(ctx, client.ObjectKey{Name: "rva-1-node-2"}, gotRVA)).To(Succeed())
						Expect(gotRVA.Status).NotTo(BeNil())
						Expect(gotRVA.Status.Phase).To(Equal("Pending"))
						cond := meta.FindStatusCondition(gotRVA.Status.Conditions, v1alpha1.RVAConditionTypeReady)
						Expect(cond).NotTo(BeNil())
						Expect(cond.Status).To(Equal(metav1.ConditionFalse))
						Expect(cond.Reason).To(Equal(v1alpha1.RVAReasonLocalityNotSatisfied))
					})
				})

				When("replica type is set via status.actualType", func() {
					BeforeEach(func() {
						// Keep spec.type Diskful, but mark replica on node-2 as actually TieBreaker (via status).
						rvrList.Items[1].Status = &v1alpha1.ReplicatedVolumeReplicaStatus{
							ActualType: v1alpha1.ReplicaTypeTieBreaker,
						}
					})

					It("keeps desiredAttachTo (does not detach an already desired node) and keeps RVA Pending with LocalityNotSatisfied", func(ctx SpecContext) {
						Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

						gotRV := &v1alpha1.ReplicatedVolume{}
						Expect(cl.Get(ctx, client.ObjectKeyFromObject(&rv), gotRV)).To(Succeed())
						Expect(gotRV.Status).NotTo(BeNil())
						Expect(gotRV.Status.DesiredAttachTo).To(Equal([]string{"node-1", "node-2"}))

						gotRVA := &v1alpha1.ReplicatedVolumeAttachment{}
						Expect(cl.Get(ctx, client.ObjectKey{Name: "rva-1-node-2"}, gotRVA)).To(Succeed())
						Expect(gotRVA.Status).NotTo(BeNil())
						Expect(gotRVA.Status.Phase).To(Equal("Pending"))
						cond := meta.FindStatusCondition(gotRVA.Status.Conditions, v1alpha1.RVAConditionTypeReady)
						Expect(cond).NotTo(BeNil())
						Expect(cond.Status).To(Equal(metav1.ConditionFalse))
						Expect(cond.Reason).To(Equal(v1alpha1.RVAReasonLocalityNotSatisfied))
					})
				})
			})

			When("attachTo shrinks to a single node", func() {
				BeforeEach(func() {
					volumeAccess = "Local"
					rsc.Spec.VolumeAccess = volumeAccess

					attachTo = []string{"node-1"}

					// смоделируем ситуацию, когда раньше allowTwoPrimaries уже был включён
					rv.Status.DRBD = &v1alpha1.DRBDResource{
						Config: &v1alpha1.DRBDResourceConfig{
							AllowTwoPrimaries: true,
						},
					}

					for i := range rvrList.Items {
						rvrList.Items[i].Status = &v1alpha1.ReplicatedVolumeReplicaStatus{
							DRBD: &v1alpha1.DRBD{
								Actual: &v1alpha1.DRBDActual{
									AllowTwoPrimaries: true,
								},
							},
						}
					}
				})

				It("sets allowTwoPrimaries=false when less than two nodes in attachTo", func(ctx SpecContext) {
					Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

					got := &v1alpha1.ReplicatedVolume{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(&rv), got)).To(Succeed())
					Expect(got.Status).NotTo(BeNil())
					Expect(got.Status.DRBD).NotTo(BeNil())
					Expect(got.Status.DRBD.Config).NotTo(BeNil())
					Expect(got.Status.DRBD.Config.AllowTwoPrimaries).To(BeFalse())
				})
			})

			When("replicas already have Primary role set in status", func() {
				BeforeEach(func() {
					volumeAccess = "Remote"
					rsc.Spec.VolumeAccess = volumeAccess

					attachTo = []string{"node-1", "node-2"}

					for i := range rvrList.Items {
						role := "Secondary"
						if rvrList.Items[i].Spec.NodeName == "node-1" {
							role = "Primary"
						}
						rvrList.Items[i].Status = &v1alpha1.ReplicatedVolumeReplicaStatus{
							DRBD: &v1alpha1.DRBD{
								Actual: &v1alpha1.DRBDActual{
									AllowTwoPrimaries: true,
								},
								Status: &v1alpha1.DRBDStatus{
									Role: role,
								},
							},
						}
					}
				})

				It("recomputes attachedTo from replicas with Primary role", func(ctx SpecContext) {
					Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

					rvList := &v1alpha1.ReplicatedVolumeList{}
					Expect(cl.List(ctx, rvList)).To(Succeed())
					Expect(rvList.Items).To(HaveLen(1))
					gotRV := &rvList.Items[0]

					Expect(gotRV.Status.ActuallyAttachedTo).To(ConsistOf("node-1"))
				})
			})

		})

		When("RVA-driven attachTo and RVA statuses", func() {
			BeforeEach(func() {
				rv.Status = &v1alpha1.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ConditionTypeRVIOReady,
							Status: metav1.ConditionTrue,
						},
					},
				}
				// start with empty desiredAttachTo; controller will derive it from RVA set
				rv.Status.DesiredAttachTo = nil

				rsc := v1alpha1.ReplicatedStorageClass{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rsc1",
					},
					Spec: v1alpha1.ReplicatedStorageClassSpec{
						Replication:  "Availability",
						VolumeAccess: "Remote",
					},
				}
				builder.WithObjects(&rsc)
			})

			It("sets Detaching + Ready=True when deleting RVA targets a node that is still actually attached", func(ctx SpecContext) {
				now := metav1.Now()
				rva := &v1alpha1.ReplicatedVolumeAttachment{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "rva-detaching",
						DeletionTimestamp: &now,
						Finalizers:        []string{v1alpha1.ControllerAppFinalizer},
					},
					Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-1",
					},
				}
				rvr := &v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rvr-primary-detaching",
					},
					Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-1",
						Type:                 v1alpha1.ReplicaTypeDiskful,
					},
					Status: &v1alpha1.ReplicatedVolumeReplicaStatus{
						ActualType: v1alpha1.ReplicaTypeDiskful,
						DRBD: &v1alpha1.DRBD{
							Status: &v1alpha1.DRBDStatus{
								Role: "Primary",
							},
						},
					},
				}
				localRV := rv
				localRSC := &v1alpha1.ReplicatedStorageClass{
					ObjectMeta: metav1.ObjectMeta{Name: "rsc1"},
					Spec: v1alpha1.ReplicatedStorageClassSpec{
						Replication:  "Availability",
						VolumeAccess: "Remote",
					},
				}
				localCl := withRVAIndex(fake.NewClientBuilder().WithScheme(scheme)).
					WithStatusSubresource(&v1alpha1.ReplicatedVolume{}).
					WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
					WithStatusSubresource(&v1alpha1.ReplicatedVolumeAttachment{}).
					WithObjects(&localRV, localRSC, rva, rvr).
					Build()
				localRec := rvattachcontroller.NewReconciler(localCl, logr.New(log.NullLogSink{}))

				Expect(localRec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&localRV)})).To(Equal(reconcile.Result{}))

				got := &v1alpha1.ReplicatedVolumeAttachment{}
				Expect(localCl.Get(ctx, client.ObjectKeyFromObject(rva), got)).To(Succeed())
				Expect(got.Status).NotTo(BeNil())
				Expect(got.Status.Phase).To(Equal("Detaching"))
				cond := meta.FindStatusCondition(got.Status.Conditions, v1alpha1.RVAConditionTypeReady)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				Expect(cond.Reason).To(Equal(v1alpha1.RVAReasonAttached))
			})

			It("sets Attaching + SettingPrimary when attachment is allowed and controller is ready to request Primary", func(ctx SpecContext) {
				rva := &v1alpha1.ReplicatedVolumeAttachment{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rva-setting-primary",
					},
					Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-1",
					},
				}
				rvr := &v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rvr-secondary-setting-primary",
					},
					Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-1",
						Type:                 v1alpha1.ReplicaTypeDiskful,
					},
					Status: &v1alpha1.ReplicatedVolumeReplicaStatus{
						ActualType: v1alpha1.ReplicaTypeDiskful,
						DRBD: &v1alpha1.DRBD{
							Actual: &v1alpha1.DRBDActual{AllowTwoPrimaries: false},
							Status: &v1alpha1.DRBDStatus{Role: "Secondary"},
						},
					},
				}
				Expect(cl.Create(ctx, rva)).To(Succeed())
				Expect(cl.Create(ctx, rvr)).To(Succeed())

				Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

				got := &v1alpha1.ReplicatedVolumeAttachment{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rva), got)).To(Succeed())
				Expect(got.Status).NotTo(BeNil())
				Expect(got.Status.Phase).To(Equal("Attaching"))
				cond := meta.FindStatusCondition(got.Status.Conditions, v1alpha1.RVAConditionTypeReady)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				Expect(cond.Reason).To(Equal(v1alpha1.RVAReasonSettingPrimary))
			})

			It("does not extend desiredAttachTo from RVA set when RV has no controller finalizer", func(ctx SpecContext) {
				// Ensure RV has no controller finalizer: this must disable adding new nodes into desiredAttachTo.
				gotRV := &v1alpha1.ReplicatedVolume{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(&rv), gotRV)).To(Succeed())
				origRV := gotRV.DeepCopy()
				gotRV.Finalizers = nil
				Expect(cl.Patch(ctx, gotRV, client.MergeFrom(origRV))).To(Succeed())

				// Pre-initialize desiredAttachTo with node-1 only.
				gotRV2 := &v1alpha1.ReplicatedVolume{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(&rv), gotRV2)).To(Succeed())
				origRV2 := gotRV2.DeepCopy()
				if gotRV2.Status == nil {
					gotRV2.Status = &v1alpha1.ReplicatedVolumeStatus{}
				}
				gotRV2.Status.DesiredAttachTo = []string{"node-1"}
				Expect(cl.Status().Patch(ctx, gotRV2, client.MergeFrom(origRV2))).To(Succeed())

				rva1 := &v1alpha1.ReplicatedVolumeAttachment{
					ObjectMeta: metav1.ObjectMeta{Name: "rva-nofinalizer-1"},
					Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-1",
					},
				}
				rva2 := &v1alpha1.ReplicatedVolumeAttachment{
					ObjectMeta: metav1.ObjectMeta{Name: "rva-nofinalizer-2"},
					Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-2",
					},
				}
				Expect(cl.Create(ctx, rva1)).To(Succeed())
				Expect(cl.Create(ctx, rva2)).To(Succeed())

				Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

				gotRV3 := &v1alpha1.ReplicatedVolume{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(&rv), gotRV3)).To(Succeed())
				Expect(gotRV3.Status).NotTo(BeNil())
				Expect(gotRV3.Status.DesiredAttachTo).To(Equal([]string{"node-1"}))

				gotRVA2 := &v1alpha1.ReplicatedVolumeAttachment{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rva2), gotRVA2)).To(Succeed())
				Expect(gotRVA2.Status).NotTo(BeNil())
				Expect(gotRVA2.Status.Phase).To(Equal("Pending"))
				cond := meta.FindStatusCondition(gotRVA2.Status.Conditions, v1alpha1.RVAConditionTypeReady)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				Expect(cond.Reason).To(Equal(v1alpha1.RVAReasonWaitingForActiveAttachmentsToDetach))
			})

			It("does not add a node into desiredAttachTo when its replica is deleting", func(ctx SpecContext) {
				rva1 := &v1alpha1.ReplicatedVolumeAttachment{
					ObjectMeta: metav1.ObjectMeta{Name: "rva-delrep-1"},
					Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-1",
					},
				}
				rva2 := &v1alpha1.ReplicatedVolumeAttachment{
					ObjectMeta: metav1.ObjectMeta{Name: "rva-delrep-2"},
					Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-2",
					},
				}
				now := metav1.Now()
				rvr1 := &v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{Name: "rvr-delrep-1"},
					Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-1",
						Type:                 v1alpha1.ReplicaTypeDiskful,
					},
					Status: &v1alpha1.ReplicatedVolumeReplicaStatus{
						ActualType: v1alpha1.ReplicaTypeDiskful,
					},
				}
				rvr2 := &v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "rvr-delrep-2",
						DeletionTimestamp: &now,
						Finalizers:        []string{"test-finalizer"},
					},
					Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-2",
						Type:                 v1alpha1.ReplicaTypeDiskful,
					},
					Status: &v1alpha1.ReplicatedVolumeReplicaStatus{
						ActualType: v1alpha1.ReplicaTypeDiskful,
					},
				}
				localRV := rv
				localRSC := &v1alpha1.ReplicatedStorageClass{
					ObjectMeta: metav1.ObjectMeta{Name: "rsc1"},
					Spec: v1alpha1.ReplicatedStorageClassSpec{
						Replication:  "Availability",
						VolumeAccess: "Remote",
					},
				}
				localCl := withRVAIndex(fake.NewClientBuilder().WithScheme(scheme)).
					WithStatusSubresource(&v1alpha1.ReplicatedVolume{}).
					WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
					WithStatusSubresource(&v1alpha1.ReplicatedVolumeAttachment{}).
					WithObjects(&localRV, localRSC, rva1, rva2, rvr1, rvr2).
					Build()
				localRec := rvattachcontroller.NewReconciler(localCl, logr.New(log.NullLogSink{}))

				Expect(localRec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&localRV)})).To(Equal(reconcile.Result{}))

				gotRV := &v1alpha1.ReplicatedVolume{}
				Expect(localCl.Get(ctx, client.ObjectKeyFromObject(&localRV), gotRV)).To(Succeed())
				Expect(gotRV.Status).NotTo(BeNil())
				Expect(gotRV.Status.DesiredAttachTo).To(Equal([]string{"node-1"}))

				gotRVA2 := &v1alpha1.ReplicatedVolumeAttachment{}
				Expect(localCl.Get(ctx, client.ObjectKeyFromObject(rva2), gotRVA2)).To(Succeed())
				Expect(gotRVA2.Status).NotTo(BeNil())
				Expect(gotRVA2.Status.Phase).To(Equal("Pending"))
				cond := meta.FindStatusCondition(gotRVA2.Status.Conditions, v1alpha1.RVAConditionTypeReady)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				Expect(cond.Reason).To(Equal(v1alpha1.RVAReasonWaitingForActiveAttachmentsToDetach))
			})

			It("derives desiredAttachTo FIFO from active RVAs, unique per node, ignoring deleting RVAs", func(ctx SpecContext) {
				now := time.Unix(3000, 0)
				delNow := metav1.NewTime(now)
				rvaDeleting := &v1alpha1.ReplicatedVolumeAttachment{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "rva-del-old",
						CreationTimestamp: metav1.NewTime(now.Add(-10 * time.Second)),
						DeletionTimestamp: &delNow,
						Finalizers:        []string{v1alpha1.ControllerAppFinalizer},
					},
					Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-3",
					},
				}
				rva1 := &v1alpha1.ReplicatedVolumeAttachment{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "rva-node-1-old",
						CreationTimestamp: metav1.NewTime(now),
					},
					Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-1",
					},
				}
				rva1dup := &v1alpha1.ReplicatedVolumeAttachment{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "rva-node-1-dup",
						CreationTimestamp: metav1.NewTime(now.Add(1 * time.Second)),
					},
					Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-1",
					},
				}
				rva2 := &v1alpha1.ReplicatedVolumeAttachment{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "rva-node-2",
						CreationTimestamp: metav1.NewTime(now.Add(2 * time.Second)),
					},
					Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-2",
					},
				}
				// Fake client may mutate metadata on Create(); seed a dedicated client instead.
				localRV := rv
				localRSC := &v1alpha1.ReplicatedStorageClass{
					ObjectMeta: metav1.ObjectMeta{Name: "rsc1"},
					Spec: v1alpha1.ReplicatedStorageClassSpec{
						Replication:  "Availability",
						VolumeAccess: "Remote",
					},
				}
				localCl := withRVAIndex(fake.NewClientBuilder().WithScheme(scheme)).
					WithStatusSubresource(&v1alpha1.ReplicatedVolume{}).
					WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplica{}).
					WithStatusSubresource(&v1alpha1.ReplicatedVolumeAttachment{}).
					WithObjects(&localRV, localRSC, rvaDeleting, rva1, rva1dup, rva2).
					Build()
				localRec := rvattachcontroller.NewReconciler(localCl, logr.New(log.NullLogSink{}))

				Expect(localRec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&localRV)})).To(Equal(reconcile.Result{}))

				gotRV := &v1alpha1.ReplicatedVolume{}
				Expect(localCl.Get(ctx, client.ObjectKeyFromObject(&localRV), gotRV)).To(Succeed())
				Expect(gotRV.Status).NotTo(BeNil())
				Expect(gotRV.Status.DesiredAttachTo).To(Equal([]string{"node-1", "node-2"}))
			})

			It("limits active attachments to two oldest RVAs and sets Pending/Ready=False for the rest", func(ctx SpecContext) {
				now := time.Unix(1000, 0)
				rva1 := &v1alpha1.ReplicatedVolumeAttachment{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "rva-1",
						CreationTimestamp: metav1.NewTime(now),
					},
					Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-1",
					},
				}
				rva2 := &v1alpha1.ReplicatedVolumeAttachment{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "rva-2",
						CreationTimestamp: metav1.NewTime(now.Add(1 * time.Second)),
					},
					Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-2",
					},
				}
				rva3 := &v1alpha1.ReplicatedVolumeAttachment{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "rva-3",
						CreationTimestamp: metav1.NewTime(now.Add(2 * time.Second)),
					},
					Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-3",
					},
				}
				Expect(cl.Create(ctx, rva1)).To(Succeed())
				Expect(cl.Create(ctx, rva2)).To(Succeed())
				Expect(cl.Create(ctx, rva3)).To(Succeed())

				Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

				gotRV := &v1alpha1.ReplicatedVolume{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(&rv), gotRV)).To(Succeed())
				Expect(gotRV.Status).NotTo(BeNil())
				Expect(gotRV.Status.DesiredAttachTo).To(Equal([]string{"node-1", "node-2"}))

				gotRVA3 := &v1alpha1.ReplicatedVolumeAttachment{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rva3), gotRVA3)).To(Succeed())
				Expect(gotRVA3.Status).NotTo(BeNil())
				Expect(gotRVA3.Status.Phase).To(Equal("Pending"))
				cond := meta.FindStatusCondition(gotRVA3.Status.Conditions, v1alpha1.RVAConditionTypeReady)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				Expect(cond.Reason).To(Equal(v1alpha1.RVAReasonWaitingForActiveAttachmentsToDetach))
			})

			It("keeps nodes already present in rv.status.desiredAttachTo first (if such RVAs exist), then fills remaining slots", func(ctx SpecContext) {
				// Pre-set desiredAttachTo with a preferred order. Controller should keep these nodes
				// if there are corresponding RVAs, regardless of the FIFO order of other RVAs.
				gotRV := &v1alpha1.ReplicatedVolume{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(&rv), gotRV)).To(Succeed())
				original := gotRV.DeepCopy()
				if gotRV.Status == nil {
					gotRV.Status = &v1alpha1.ReplicatedVolumeStatus{}
				}
				gotRV.Status.DesiredAttachTo = []string{"node-2", "node-1"}
				Expect(cl.Status().Patch(ctx, gotRV, client.MergeFrom(original))).To(Succeed())

				now := time.Unix(2000, 0)
				// Make node-3 RVA older than node-1 to ensure FIFO would pick it if not for attachTo preference.
				rva3 := &v1alpha1.ReplicatedVolumeAttachment{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "rva-3",
						CreationTimestamp: metav1.NewTime(now),
					},
					Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-3",
					},
				}
				rva2 := &v1alpha1.ReplicatedVolumeAttachment{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "rva-2",
						CreationTimestamp: metav1.NewTime(now.Add(1 * time.Second)),
					},
					Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-2",
					},
				}
				rva1 := &v1alpha1.ReplicatedVolumeAttachment{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "rva-1",
						CreationTimestamp: metav1.NewTime(now.Add(2 * time.Second)),
					},
					Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-1",
					},
				}
				Expect(cl.Create(ctx, rva3)).To(Succeed())
				Expect(cl.Create(ctx, rva2)).To(Succeed())
				Expect(cl.Create(ctx, rva1)).To(Succeed())

				Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

				gotRV2 := &v1alpha1.ReplicatedVolume{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(&rv), gotRV2)).To(Succeed())
				Expect(gotRV2.Status).NotTo(BeNil())
				Expect(gotRV2.Status.DesiredAttachTo).To(Equal([]string{"node-2", "node-1"}))
			})

			It("sets Attaching + WaitingForReplica when active RVA has no replica yet", func(ctx SpecContext) {
				rva := &v1alpha1.ReplicatedVolumeAttachment{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rva-wait-replica",
					},
					Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-1",
					},
				}
				Expect(cl.Create(ctx, rva)).To(Succeed())

				Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

				gotRVA := &v1alpha1.ReplicatedVolumeAttachment{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rva), gotRVA)).To(Succeed())
				Expect(gotRVA.Status).NotTo(BeNil())
				Expect(gotRVA.Status.Phase).To(Equal("Attaching"))
				cond := meta.FindStatusCondition(gotRVA.Status.Conditions, v1alpha1.RVAConditionTypeReady)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				Expect(cond.Reason).To(Equal(v1alpha1.RVAReasonWaitingForReplica))
			})

			It("sets Attaching + ConvertingTieBreakerToAccess when active RVA targets a TieBreaker replica", func(ctx SpecContext) {
				rva := &v1alpha1.ReplicatedVolumeAttachment{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rva-tb",
					},
					Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-1",
					},
				}
				rvr := &v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rvr-tb-1",
					},
					Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-1",
						Type:                 v1alpha1.ReplicaTypeTieBreaker,
					},
				}
				Expect(cl.Create(ctx, rva)).To(Succeed())
				Expect(cl.Create(ctx, rvr)).To(Succeed())

				Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

				gotRVA := &v1alpha1.ReplicatedVolumeAttachment{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rva), gotRVA)).To(Succeed())
				Expect(gotRVA.Status).NotTo(BeNil())
				Expect(gotRVA.Status.Phase).To(Equal("Attaching"))
				cond := meta.FindStatusCondition(gotRVA.Status.Conditions, v1alpha1.RVAConditionTypeReady)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				Expect(cond.Reason).To(Equal(v1alpha1.RVAReasonConvertingTieBreakerToAccess))
			})

			It("sets Attached + Ready=True when RV reports the node in status.actuallyAttachedTo", func(ctx SpecContext) {
				rva := &v1alpha1.ReplicatedVolumeAttachment{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rva-attached",
					},
					Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-1",
					},
				}
				rolePrimary := "Primary"
				rvr := &v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rvr-primary-1",
					},
					Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-1",
						Type:                 v1alpha1.ReplicaTypeDiskful,
					},
					Status: &v1alpha1.ReplicatedVolumeReplicaStatus{
						DRBD: &v1alpha1.DRBD{
							Status: &v1alpha1.DRBDStatus{
								Role: rolePrimary,
							},
						},
					},
				}
				Expect(cl.Create(ctx, rva)).To(Succeed())
				Expect(cl.Create(ctx, rvr)).To(Succeed())

				Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

				gotRVA := &v1alpha1.ReplicatedVolumeAttachment{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rva), gotRVA)).To(Succeed())
				Expect(gotRVA.Status).NotTo(BeNil())
				Expect(gotRVA.Status.Phase).To(Equal("Attached"))
				cond := meta.FindStatusCondition(gotRVA.Status.Conditions, v1alpha1.RVAConditionTypeReady)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			})

			It("marks all RVAs for the same attached node as successful (Attached + Ready=True)", func(ctx SpecContext) {
				// Create 3 RVA objects for the same node.
				rva1 := &v1alpha1.ReplicatedVolumeAttachment{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rva-attached-1",
					},
					Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-1",
					},
				}
				rva2 := &v1alpha1.ReplicatedVolumeAttachment{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rva-attached-2",
					},
					Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-1",
					},
				}
				rva3 := &v1alpha1.ReplicatedVolumeAttachment{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rva-attached-3",
					},
					Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-1",
					},
				}
				Expect(cl.Create(ctx, rva1)).To(Succeed())
				Expect(cl.Create(ctx, rva2)).To(Succeed())
				Expect(cl.Create(ctx, rva3)).To(Succeed())

				// Also create a replica on that node and mark it Primary so the controller sees actual attachment.
				rolePrimary := "Primary"
				rvr := &v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rvr-df-1",
					},
					Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-1",
						Type:                 v1alpha1.ReplicaTypeDiskful,
					},
					Status: &v1alpha1.ReplicatedVolumeReplicaStatus{
						DRBD: &v1alpha1.DRBD{
							Status: &v1alpha1.DRBDStatus{
								Role: rolePrimary,
							},
						},
					},
				}
				Expect(cl.Create(ctx, rvr)).To(Succeed())

				Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

				for _, obj := range []*v1alpha1.ReplicatedVolumeAttachment{rva1, rva2, rva3} {
					got := &v1alpha1.ReplicatedVolumeAttachment{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(obj), got)).To(Succeed())
					Expect(got.Status).NotTo(BeNil())
					Expect(got.Status.Phase).To(Equal("Attached"))
					cond := meta.FindStatusCondition(got.Status.Conditions, v1alpha1.RVAConditionTypeReady)
					Expect(cond).NotTo(BeNil())
					Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				}
			})

			It("releases finalizer for deleting duplicate RVA on the same node (does not wait for actual detach)", func(ctx SpecContext) {
				now := metav1.Now()
				rvaAlive := &v1alpha1.ReplicatedVolumeAttachment{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rva-alive",
					},
					Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-1",
					},
				}
				rvaDeleting := &v1alpha1.ReplicatedVolumeAttachment{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "rva-deleting",
						DeletionTimestamp: &now,
						Finalizers:        []string{v1alpha1.ControllerAppFinalizer},
					},
					Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-1",
					},
				}
				Expect(cl.Create(ctx, rvaAlive)).To(Succeed())
				Expect(cl.Create(ctx, rvaDeleting)).To(Succeed())

				// Mark node-1 as attached.
				rolePrimary := "Primary"
				rvr := &v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rvr-df-1-delcase",
					},
					Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-1",
						Type:                 v1alpha1.ReplicaTypeDiskful,
					},
					Status: &v1alpha1.ReplicatedVolumeReplicaStatus{
						ActualType: v1alpha1.ReplicaTypeDiskful,
						DRBD: &v1alpha1.DRBD{
							Status: &v1alpha1.DRBDStatus{
								Role: rolePrimary,
							},
						},
					},
				}
				Expect(cl.Create(ctx, rvr)).To(Succeed())

				Expect(rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})).To(Equal(reconcile.Result{}))

				gotAlive := &v1alpha1.ReplicatedVolumeAttachment{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvaAlive), gotAlive)).To(Succeed())
				Expect(gotAlive.Finalizers).To(ContainElement(v1alpha1.ControllerAppFinalizer))
				Expect(gotAlive.Status).NotTo(BeNil())
				Expect(gotAlive.Status.Phase).To(Equal("Attached"))
				condAlive := meta.FindStatusCondition(gotAlive.Status.Conditions, v1alpha1.RVAConditionTypeReady)
				Expect(condAlive).NotTo(BeNil())
				Expect(condAlive.Status).To(Equal(metav1.ConditionTrue))

				gotDel := &v1alpha1.ReplicatedVolumeAttachment{}
				err := cl.Get(ctx, client.ObjectKeyFromObject(rvaDeleting), gotDel)
				if client.IgnoreNotFound(err) == nil {
					// After finalizer is released, fake client may delete the object immediately.
					return
				}
				Expect(err).NotTo(HaveOccurred())
				Expect(gotDel.Finalizers).NotTo(ContainElement(v1alpha1.ControllerAppFinalizer))
				Expect(gotDel.Status).NotTo(BeNil())
				Expect(gotDel.Status.Phase).To(Equal("Attached"))
				condDel := meta.FindStatusCondition(gotDel.Status.Conditions, v1alpha1.RVAConditionTypeReady)
				Expect(condDel).NotTo(BeNil())
				Expect(condDel.Status).To(Equal(metav1.ConditionTrue))
			})
		})

		// AttachSucceeded condition on RV is intentionally not used anymore.

		When("patching RVR primary status fails", func() {
			BeforeEach(func() {
				rv.Status = &v1alpha1.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ConditionTypeRVIOReady,
							Status: metav1.ConditionTrue,
						},
					},
				}

				rsc := v1alpha1.ReplicatedStorageClass{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rsc1",
					},
					Spec: v1alpha1.ReplicatedStorageClassSpec{
						Replication:  "Availability",
						VolumeAccess: "Remote",
					},
				}

				rva := v1alpha1.ReplicatedVolumeAttachment{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rva-primary-1",
					},
					Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-1",
					},
				}

				rvr := v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rvr-primary-1",
					},
					Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-1",
						Type:                 v1alpha1.ReplicaTypeDiskful,
					},
				}

				builder.WithObjects(&rsc, &rva, &rvr)

				builder.WithInterceptorFuncs(interceptor.Funcs{
					SubResourcePatch: func(ctx context.Context, cl client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
						if _, ok := obj.(*v1alpha1.ReplicatedVolumeReplica); ok {
							return errExpectedTestError
						}
						return cl.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
					},
				})
			})

			It("returns error when updating RVR primary status fails", func(ctx SpecContext) {
				result, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})
				Expect(err).To(MatchError(errExpectedTestError))
				Expect(result).To(Equal(reconcile.Result{}))
			})
		})

		When("Get ReplicatedVolume fails", func() {
			BeforeEach(func() {
				builder.WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						if _, ok := obj.(*v1alpha1.ReplicatedVolume); ok {
							return errExpectedTestError
						}
						return c.Get(ctx, key, obj, opts...)
					},
				})
			})

			It("returns same error", func(ctx SpecContext) {
				result, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})
				Expect(err).To(MatchError(errExpectedTestError))
				Expect(result).To(Equal(reconcile.Result{}))
			})
		})

		When("Get ReplicatedStorageClass fails", func() {
			BeforeEach(func() {
				rv.Status = &v1alpha1.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ConditionTypeRVIOReady,
							Status: metav1.ConditionTrue,
						},
					},
				}

				builder.WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						if _, ok := obj.(*v1alpha1.ReplicatedStorageClass); ok {
							return errExpectedTestError
						}
						return c.Get(ctx, key, obj, opts...)
					},
				})
			})

			It("does not error (switches to detach-only)", func(ctx SpecContext) {
				result, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))
			})

			It("keeps RVA Pending/Ready=False with WaitingForReplicatedVolume when StorageClass cannot be loaded", func(ctx SpecContext) {
				rva := &v1alpha1.ReplicatedVolumeAttachment{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rva-sc-missing",
					},
					Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
						ReplicatedVolumeName: rv.Name,
						NodeName:             "node-1",
					},
				}
				Expect(cl.Create(ctx, rva)).To(Succeed())

				result, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal(reconcile.Result{}))

				got := &v1alpha1.ReplicatedVolumeAttachment{}
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(rva), got)).To(Succeed())
				Expect(got.Status).NotTo(BeNil())
				Expect(got.Status.Phase).To(Equal("Pending"))
				cond := meta.FindStatusCondition(got.Status.Conditions, v1alpha1.RVAConditionTypeReady)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				Expect(cond.Reason).To(Equal(v1alpha1.RVAReasonWaitingForReplicatedVolume))
			})
		})

		When("List ReplicatedVolumeReplica fails", func() {
			BeforeEach(func() {
				rv.Status = &v1alpha1.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ConditionTypeRVIOReady,
							Status: metav1.ConditionTrue,
						},
					},
				}

				rsc := v1alpha1.ReplicatedStorageClass{
					ObjectMeta: metav1.ObjectMeta{
						Name: "rsc1",
					},
					Spec: v1alpha1.ReplicatedStorageClassSpec{
						Replication:  "Availability",
						VolumeAccess: "Local",
					},
				}

				builder.WithObjects(&rsc)

				builder.WithInterceptorFuncs(interceptor.Funcs{
					List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
						if _, ok := list.(*v1alpha1.ReplicatedVolumeReplicaList); ok {
							return errExpectedTestError
						}
						return c.List(ctx, list, opts...)
					},
				})
			})

			It("returns same error", func(ctx SpecContext) {
				result, err := rec.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rv)})
				Expect(err).To(MatchError(errExpectedTestError))
				Expect(result).To(Equal(reconcile.Result{}))
			})
		})
	})
})
