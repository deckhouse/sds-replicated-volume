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
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

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
		rsc := newRSCWithConfiguration("rsc-1") // FTT=0,GMDR=0 → 1 diskful
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
		// FTT=0,GMDR=0 → wants 1 diskful, but we have 2.
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
		rsc := newRSCWithConfiguration("rsc-1") // FTT=0,GMDR=0 → 1 diskful
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
		rsc := newRSCWithConfiguration("rsc-1") // FTT=0,GMDR=0 → 1 diskful
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
		// FTT=0,GMDR=0 → wants 1 diskful, but we have 2: one preconfigured, one unscheduled.
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
		// FTT=0,GMDR=0 → wants 1 diskful, but we have 2 preconfigured replicas.
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
				Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
					Topology:           v1alpha1.TopologyIgnored,
					FailuresToTolerate: 0, GuaranteedMinimumDataRedundancy: 0,
					VolumeAccess:              v1alpha1.VolumeAccessLocal,
					ReplicatedStoragePoolName: "test-pool",
				},
				DatameshRevision: 1,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					SharedSecret:            "test-secret",
					SharedSecretAlg:         v1alpha1.SharedSecretAlgSHA256,
					SystemNetworkNames:      []string{"Internal"},
					Size:                    resource.MustParse("10Gi"),
					Quorum:                  1,
					QuorumMinimumRedundancy: 1,
					Members: []v1alpha1.DatameshMember{
						{
							Name:               v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0),
							Type:               v1alpha1.DatameshMemberTypeDiskful,
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

		// Use a 2-replica setup to test connection checks (FTT=1,GMDR=0 → D=2).
		rv := newRVInEstablishConnectivity()
		rv.Status.Configuration.FailuresToTolerate = 1
		rv.Status.Configuration.GuaranteedMinimumDataRedundancy = 0
		rv.Status.Datamesh.Members = append(rv.Status.Datamesh.Members, v1alpha1.DatameshMember{
			Name:               v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 1),
			Type:               v1alpha1.DatameshMemberTypeDiskful,
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
		rv.Status.Datamesh.Members = append(rv.Status.Datamesh.Members, v1alpha1.DatameshMember{
			Name:               v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 1),
			Type:               v1alpha1.DatameshMemberTypeDiskful,
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
				Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
					Topology: v1alpha1.TopologyIgnored, FailuresToTolerate: 0, GuaranteedMinimumDataRedundancy: 0,
					VolumeAccess: v1alpha1.VolumeAccessLocal, ReplicatedStoragePoolName: "test-pool",
				},
				DatameshRevision: 1,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					SharedSecret: "test-secret", SharedSecretAlg: v1alpha1.SharedSecretAlgSHA256,
					SystemNetworkNames: []string{"Internal"}, Size: resource.MustParse("10Gi"),
					Quorum: 1, QuorumMinimumRedundancy: 1,
					Members: []v1alpha1.DatameshMember{
						{
							Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0),
							Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "node-1",
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
				Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
					Topology:           v1alpha1.TopologyIgnored,
					FailuresToTolerate: 1, GuaranteedMinimumDataRedundancy: 0,
					VolumeAccess:              v1alpha1.VolumeAccessLocal,
					ReplicatedStoragePoolName: "test-pool",
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
		rv.Status.Configuration.FailuresToTolerate = 1
		rv.Status.Configuration.GuaranteedMinimumDataRedundancy = 0
		rv.Status.Datamesh.Members = append(rv.Status.Datamesh.Members, v1alpha1.DatameshMember{
			Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 1), Type: v1alpha1.DatameshMemberTypeDiskful,
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
				Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
					Topology: v1alpha1.TopologyIgnored, FailuresToTolerate: 0, GuaranteedMinimumDataRedundancy: 0,
					VolumeAccess: v1alpha1.VolumeAccessLocal, ReplicatedStoragePoolName: "test-pool",
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
				Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
					Topology: v1alpha1.TopologyIgnored, FailuresToTolerate: 0, GuaranteedMinimumDataRedundancy: 0,
					VolumeAccess: v1alpha1.VolumeAccessLocal, ReplicatedStoragePoolName: "test-pool",
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
				Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
					Topology: v1alpha1.TopologyIgnored, FailuresToTolerate: 0, GuaranteedMinimumDataRedundancy: 0,
					VolumeAccess: v1alpha1.VolumeAccessLocal, ReplicatedStoragePoolName: "test-pool",
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
