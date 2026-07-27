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
	"strings"
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
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/idset"
)

// mkFormationTransition creates a Formation transition with all steps where the
// step at activeStepIdx is Active (with startedAt=now) and prior steps are Completed.
func mkFormationTransition(activeStepIdx int) v1alpha1.ReplicatedVolumeDatameshTransition {
	now := metav1.Now()
	steps := make([]v1alpha1.ReplicatedVolumeDatameshTransitionStep, formationStepCount)
	for i := range steps {
		steps[i].Name = formationStepNames[i]
		switch {
		case i < activeStepIdx:
			steps[i].Status = v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted
			steps[i].StartedAt = &now
			steps[i].CompletedAt = &now
		case i == activeStepIdx:
			steps[i].Status = v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive
			steps[i].StartedAt = &now
		default:
			steps[i].Status = v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending
		}
	}
	return v1alpha1.ReplicatedVolumeDatameshTransition{
		Type:  v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation,
		Steps: steps,
	}
}

// mkFormationTransitionWithTime creates a Formation transition where the first step
// has the given startedAt time. Used for timeout tests.
func mkFormationTransitionWithTime(activeStepIdx int, startedAt metav1.Time) v1alpha1.ReplicatedVolumeDatameshTransition {
	t := mkFormationTransition(activeStepIdx)
	t.Steps[0].StartedAt = &startedAt
	return t
}

var _ = Describe("isFormationInProgress", func() {
	It("returns true with step 0 when DatameshRevision is 0", func() {
		rv := &v1alpha1.ReplicatedVolume{}
		forming, stepIdx := isFormationInProgress(rv)
		Expect(forming).To(BeTrue())
		Expect(stepIdx).To(Equal(formationStepIdxPreconfigure))
	})

	It("returns true with correct step index when Formation transition exists", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshRevision: 2,
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					mkFormationTransition(formationStepIdxEstablishConnectivity),
				},
			},
		}
		forming, stepIdx := isFormationInProgress(rv)
		Expect(forming).To(BeTrue())
		Expect(stepIdx).To(Equal(formationStepIdxEstablishConnectivity))
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

var _ = Describe("ensureFormationTransition", func() {
	It("creates new transition with all steps (create/v1)", func() {
		rv := &v1alpha1.ReplicatedVolume{}
		t, created := ensureFormationTransition(rv, formationPlanCreate)
		Expect(created).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(t.Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation))
		Expect(t.PlanID).To(Equal(formationPlanCreate))
		Expect(t.Steps).To(HaveLen(formationStepCount))
		Expect(t.Steps[0].Name).To(Equal(formationStepNames[0]))
		Expect(t.Steps[0].Status).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive))
		Expect(t.Steps[0].StartedAt).NotTo(BeNil())
		Expect(t.Steps[1].Status).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending))
		Expect(t.Steps[2].Status).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending))
	})

	It("creates new transition with adopt steps (adopt/v1)", func() {
		rv := &v1alpha1.ReplicatedVolume{}
		t, created := ensureFormationTransition(rv, formationPlanAdopt)
		Expect(created).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(t.Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation))
		Expect(t.PlanID).To(Equal(formationPlanAdopt))
		Expect(t.Steps).To(HaveLen(adoptStepCount))
		Expect(t.Steps[0].Name).To(Equal(adoptStepNames[0]))
		Expect(t.Steps[0].Status).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive))
		Expect(t.Steps[0].StartedAt).NotTo(BeNil())
		Expect(t.Steps[1].Status).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending))
		Expect(t.Steps[2].Status).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending))
	})

	It("returns existing transition without creating", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					mkFormationTransition(formationStepIdxPreconfigure),
				},
			},
		}
		t, created := ensureFormationTransition(rv, formationPlanCreate)
		Expect(created).To(BeFalse())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(t.Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation))
	})
})

var _ = Describe("advanceFormationStep", func() {
	It("completes current step and activates next", func() {
		rv := &v1alpha1.ReplicatedVolume{}
		t, _ := ensureFormationTransition(rv, formationPlanCreate)

		advanceFormationStep(t, formationStepIdxPreconfigure)

		Expect(t.Steps[0].Status).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted))
		Expect(t.Steps[0].CompletedAt).NotTo(BeNil())
		Expect(t.Steps[0].Message).To(BeEmpty())
		Expect(t.Steps[1].Status).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive))
		Expect(t.Steps[1].StartedAt).NotTo(BeNil())
		Expect(t.Steps[2].Status).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending))
	})

	It("clears message on completed step", func() {
		rv := &v1alpha1.ReplicatedVolume{}
		t, _ := ensureFormationTransition(rv, formationPlanCreate)
		t.Steps[0].Message = "waiting for something..."

		advanceFormationStep(t, formationStepIdxPreconfigure)

		Expect(t.Steps[0].Message).To(BeEmpty())
	})
})

var _ = Describe("applyFormationTransitionAbsent", func() {
	It("removes formation and returns true", func() {
		rv := &v1alpha1.ReplicatedVolume{
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					mkFormationTransition(formationStepIdxPreconfigure),
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
	// scheduled, DRBDConfigured=PendingDatameshJoin, datamesh request operation=Join.
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
				DatameshRequest: &v1alpha1.DatameshMembershipRequest{
					Operation:          v1alpha1.DatameshMembershipRequestOperationJoin,
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
		Expect(updated.Status.DatameshTransitions[0].CurrentStep().Message).To(ContainSubstring("pending scheduling"))
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
		Expect(updated.Status.DatameshTransitions[0].CurrentStep().Message).To(ContainSubstring("scheduling failed [#0]"))
		Expect(updated.Status.DatameshTransitions[0].CurrentStep().Message).To(ContainSubstring("(2 candidates; 2 excluded: node not ready)"))
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
		Expect(updated.Status.DatameshTransitions[0].CurrentStep().Message).To(ContainSubstring("preconfiguring"))
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
		Expect(updated.Status.DatameshTransitions[0].CurrentStep().Message).To(ContainSubstring("Address configuration mismatch"))
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
		Expect(updated.Status.DatameshTransitions[0].CurrentStep().Message).To(ContainSubstring("not in eligible nodes"))
	})

	It("detects spec mismatch with membership request", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1")
		rsp := newTestRSPWithNodes("test-pool", "node-1")
		rv := newFormationRV("rsc-1")

		rvr := newPreconfiguredRVR("rv-1", 0, "node-1")
		// Create spec mismatch: RVR spec says lvg-1, but pending transition says lvg-2.
		rvr.Status.DatameshRequest.LVMVolumeGroupName = "lvg-2"

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
		Expect(updated.Status.DatameshTransitions[0].CurrentStep().Message).To(ContainSubstring("spec changes"))
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
		Expect(updated.Status.DatameshTransitions[0].CurrentStep().Message).To(ContainSubstring("insufficient backing volume size"))
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

	// ──────────────────────────────────────────────────────────────────────────
	// Tie-breaker formation (block 3): formation must create the tie-breaker of the
	// intended layout so a volume leaves formation directly at e.g. 2D+1TB.

	// newPreconfiguredTBRVR builds a TieBreaker RVR that is fully preconfigured for create/v1
	// formation: scheduled, DRBDConfigured=PendingDatameshJoin, addresses reported, and a
	// datamesh Join request of type TieBreaker. Diskless: no LVG and no backing volume.
	//nolint:unparam // rvName is always "rv-1" in current tests, kept for symmetry with newPreconfiguredRVR.
	newPreconfiguredTBRVR := func(rvName string, id uint8, nodeName string) *v1alpha1.ReplicatedVolumeReplica {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name:       v1alpha1.FormatReplicatedVolumeReplicaName(rvName, id),
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: rvName,
				Type:                 v1alpha1.ReplicaTypeTieBreaker,
				NodeName:             nodeName,
			},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				Addresses: []v1alpha1.DRBDResourceAddressStatus{
					{SystemNetworkName: "Internal"},
				},
				DatameshRequest: &v1alpha1.DatameshMembershipRequest{
					Operation: v1alpha1.DatameshMembershipRequestOperationJoin,
					Type:      v1alpha1.ReplicaTypeTieBreaker,
				},
			},
		}
		obju.SetStatusCondition(rvr, metav1.Condition{
			Type:   v1alpha1.ReplicatedVolumeReplicaCondScheduledType,
			Status: metav1.ConditionTrue,
			Reason: v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonScheduled,
		})
		obju.SetStatusCondition(rvr, metav1.Condition{
			Type:   v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType,
			Status: metav1.ConditionTrue,
			Reason: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonPendingDatameshJoin,
		})
		obju.SetStatusCondition(rvr, metav1.Condition{
			Type:   v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType,
			Status: metav1.ConditionTrue,
			Reason: v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonSatisfied,
		})
		return rvr
	}

	// countRVRTypes lists RVRs and returns (diskful, tie-breaker) counts.
	countRVRTypes := func(ctx context.Context, cl client.Client) (diskful, tiebreaker int) {
		var list v1alpha1.ReplicatedVolumeReplicaList
		Expect(cl.List(ctx, &list)).To(Succeed())
		for i := range list.Items {
			switch list.Items[i].Spec.Type {
			case v1alpha1.ReplicaTypeDiskful:
				diskful++
			case v1alpha1.ReplicaTypeTieBreaker:
				tiebreaker++
			}
		}
		return diskful, tiebreaker
	}

	It("creates the intended diskful + tie-breaker layout for each replication setting", func(ctx SpecContext) {
		cases := []struct {
			name                    string
			ftt, gmdr               byte
			wantDiskful, wantTBreak int
		}{
			{"None → 1D", 0, 0, 1, 0},
			{"Consistency → 2D", 0, 1, 2, 0},
			{"Availability → 2D+1TB", 1, 0, 2, 1},
			{"ConsistencyAndAvailability → 3D", 1, 1, 3, 0},
			{"Manual FTT=2,GMDR=1 → 4D+1TB", 2, 1, 4, 1},
		}
		for _, tc := range cases {
			rsc := newRSCWithConfiguration("rsc-1")
			rsc.Status.Configuration.FailuresToTolerate = tc.ftt
			rsc.Status.Configuration.GuaranteedMinimumDataRedundancy = tc.gmdr
			rsp := newTestRSPWithNodes("test-pool", "node-1", "node-2", "node-3", "node-4", "node-5")
			rv := newFormationRV("rsc-1")

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsc, rsp).
				WithStatusSubresource(rv, rsc).
				Build()
			rec := NewReconciler(cl, scheme)

			// A single reconcile creates every missing replica of the layout (no early
			// return between the diskful and tie-breaker create loops).
			_, err := rec.Reconcile(ctx, RequestFor(rv))
			Expect(err).NotTo(HaveOccurred(), tc.name)

			diskful, tiebreaker := countRVRTypes(ctx, cl)
			Expect(diskful).To(Equal(tc.wantDiskful), "diskful count for %s", tc.name)
			Expect(tiebreaker).To(Equal(tc.wantTBreak), "tie-breaker count for %s", tc.name)
		}
	})

	It("adds the tie-breaker to the datamesh during establish-connectivity (2D+1TB)", func(ctx SpecContext) {
		// r2 layout: FTT=1, GMDR=0 → 2D+1TB. All replicas already preconfigured, so a single
		// reconcile passes preconfigure and falls through to establish-connectivity.
		rsc := newRSCWithConfiguration("rsc-1")
		rsc.Status.Configuration.FailuresToTolerate = 1
		rsp := newTestRSPWithNodes("test-pool", "node-1", "node-2", "node-3")
		rv := newFormationRV("rsc-1")

		d0 := newPreconfiguredRVR("rv-1", 0, "node-1")
		d1 := newPreconfiguredRVR("rv-1", 1, "node-2")
		tb := newPreconfiguredTBRVR("rv-1", 2, "node-3")

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, d0, d1, tb).
			WithStatusSubresource(rv, rsc, d0, d1, tb).
			Build()
		rec := NewReconciler(cl, scheme)

		_, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())

		// The datamesh is formed with the full 2D+1TB layout in one bulk-add.
		Expect(updated.Status.Datamesh.SharedSecret).NotTo(BeEmpty())
		Expect(updated.Status.Datamesh.Members).To(HaveLen(3))
		memberTypes := map[v1alpha1.DatameshMemberType]int{}
		for _, m := range updated.Status.Datamesh.Members {
			memberTypes[m.Type]++
		}
		Expect(memberTypes[v1alpha1.DatameshMemberTypeDiskful]).To(Equal(2))
		Expect(memberTypes[v1alpha1.DatameshMemberTypeTieBreaker]).To(Equal(1))
		// Tie-breaker is not a voter, so quorum stays floor(2/2)+1 = 2, qmr = GMDR+1 = 1.
		Expect(updated.Status.Datamesh.Quorum).To(Equal(byte(2)))
		Expect(updated.Status.Datamesh.QuorumMinimumRedundancy).To(Equal(byte(1)))
	})

	It("does not create a second tie-breaker on repeated reconcile (idempotent)", func(ctx SpecContext) {
		// r2 layout with the full 2D+1TB already present and preconfigured.
		rsc := newRSCWithConfiguration("rsc-1")
		rsc.Status.Configuration.FailuresToTolerate = 1
		rsp := newTestRSPWithNodes("test-pool", "node-1", "node-2", "node-3")
		rv := newFormationRV("rsc-1")

		d0 := newPreconfiguredRVR("rv-1", 0, "node-1")
		d1 := newPreconfiguredRVR("rv-1", 1, "node-2")
		tb := newPreconfiguredTBRVR("rv-1", 2, "node-3")

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, d0, d1, tb).
			WithStatusSubresource(rv, rsc, d0, d1, tb).
			Build()
		rec := NewReconciler(cl, scheme)

		for i := 0; i < 3; i++ {
			_, err := rec.Reconcile(ctx, RequestFor(rv))
			Expect(err).NotTo(HaveOccurred())
		}

		diskful, tiebreaker := countRVRTypes(ctx, cl)
		Expect(diskful).To(Equal(2), "diskful count must stay 2")
		Expect(tiebreaker).To(Equal(1), "tie-breaker count must stay 1 (no duplicate)")
	})

	It("requeues without error when the tie-breaker create returns AlreadyExists", func(ctx SpecContext) {
		// r2 layout: 2 diskful create successfully, the tie-breaker create hits a stale cache.
		rsc := newRSCWithConfiguration("rsc-1")
		rsc.Status.Configuration.FailuresToTolerate = 1
		rsp := newTestRSPWithNodes("test-pool", "node-1", "node-2", "node-3")
		rv := newFormationRV("rsc-1")

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp).
			WithStatusSubresource(rv, rsc).
			WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					if rvr, ok := obj.(*v1alpha1.ReplicatedVolumeReplica); ok && rvr.Spec.Type == v1alpha1.ReplicaTypeTieBreaker {
						return apierrors.NewAlreadyExists(
							schema.GroupResource{Group: v1alpha1.SchemeGroupVersion.Group, Resource: "replicatedvolumereplicas"},
							rvr.Name,
						)
					}
					return cl.Create(ctx, obj, opts...)
				},
			}).
			Build()
		rec := NewReconciler(cl, scheme)

		result, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())
		// AlreadyExists on the tie-breaker → DoneAndRequeue (no error surfaced).
		Expect(result.Requeue).To(BeTrue()) //nolint:staticcheck // Requeue field is set by flow.DoneAndRequeue
	})

	It("waits with an honest status when the tie-breaker cannot be scheduled", func(ctx SpecContext) {
		// r2 layout: diskful placed, but the tie-breaker cannot be scheduled (no third node/zone).
		// The scheduler set Scheduled=False; formation must report it and keep waiting, not advance
		// to a 2D-only datamesh.
		rsc := newRSCWithConfiguration("rsc-1")
		rsc.Status.Configuration.FailuresToTolerate = 1
		rsp := newTestRSPWithNodes("test-pool", "node-1", "node-2")
		rv := newFormationRV("rsc-1")

		d0 := newPreconfiguredRVR("rv-1", 0, "node-1")
		d1 := newPreconfiguredRVR("rv-1", 1, "node-2")
		// Tie-breaker exists but scheduling failed.
		tb := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name:       v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 2),
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1",
				Type:                 v1alpha1.ReplicaTypeTieBreaker,
			},
		}
		obju.SetStatusCondition(tb, metav1.Condition{
			Type:    v1alpha1.ReplicatedVolumeReplicaCondScheduledType,
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonSchedulingFailed,
			Message: "no node can host a tie-breaker",
		})
		obju.SetStatusCondition(tb, metav1.Condition{
			Type:   v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType,
			Status: metav1.ConditionTrue,
			Reason: v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonSatisfied,
		})

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, d0, d1, tb).
			WithStatusSubresource(rv, rsc, d0, d1, tb).
			Build()
		rec := NewReconciler(cl, scheme)

		result, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())
		// Formation waits (requeue), does not silently hang or advance.
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
		// Honest status: the wait message names the tie-breaker's scheduling failure.
		msg := updated.Status.DatameshTransitions[0].CurrentStep().Message
		Expect(msg).To(ContainSubstring("scheduling failed [#2]"))
		Expect(msg).To(ContainSubstring("no node can host a tie-breaker"))
		// Datamesh not formed: no members added while the layout is incomplete.
		Expect(updated.Status.Datamesh.Members).To(BeEmpty())
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
					mkFormationTransitionWithTime(formationStepIdxEstablishConnectivity, metav1.Now()),
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
		Expect(updated.Status.DatameshTransitions[0].CurrentStep().Message).To(ContainSubstring("fully configured"))
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
		Expect(updated.Status.DatameshTransitions[0].CurrentStep().Message).To(ContainSubstring("establish connections"))
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
		Expect(updated.Status.DatameshTransitions[0].CurrentStep().Message).To(ContainSubstring("Datamesh members mismatch"))
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
		Expect(updated.Status.DatameshTransitions[0].CurrentStep().Message).To(ContainSubstring("ready for data bootstrap"))
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

	It("waits without panicking when a member has no datamesh request at member-add", func(ctx SpecContext) {
		// Members are empty, so the bulk member-add path runs. A candidate member whose
		// DatameshRequest was cleared (stale cache / manual intervention / RSP flap) must not be
		// dereferenced: formation waits with an honest status instead of panicking or building a
		// partial datamesh.
		rsc := newRSCWithConfiguration("rsc-1")
		rsp := newTestRSP("test-pool")
		rsp.Status.EligibleNodes = []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1", LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{{Name: "lvg-1"}}},
		}
		rv := newRVInEstablishConnectivity()
		rv.Status.Datamesh.Members = nil // force the bulk member-add path

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
				DatameshRequest: nil, // cleared: must not be dereferenced during member-add
			},
		}

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr).
			WithStatusSubresource(rv, rsc, rvr).
			Build()
		rec := NewReconciler(cl, scheme)

		result, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())
		// Formation waits (no panic, no partial datamesh).
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
		Expect(updated.Status.Datamesh.Members).To(BeEmpty(), "no member should be added while a request is missing")
		Expect(updated.Status.DatameshTransitions[0].CurrentStep().Message).
			To(ContainSubstring("membership request"))
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
					mkFormationTransitionWithTime(formationStepIdxBootstrapData, formationStartedAt),
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
		// NodeName must be set to the node that owns the chosen diskful replica.
		Expect(drbdrOp.Spec.NodeName).To(Equal("node-1"))
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
		Expect(updated.Status.DatameshTransitions[0].CurrentStep().Message).To(ContainSubstring("waiting for operation"))
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
		Expect(updated.Status.DatameshTransitions[0].CurrentStep().Message).To(ContainSubstring("failed"))
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
				CreationTimestamp: metav1.NewTime(rv.Status.DatameshTransitions[0].StartedAt().Add(-1 * time.Hour)),
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
		Expect(updated.Status.DatameshTransitions[0].CurrentStep().Message).To(ContainSubstring("unexpected parameters"))
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
		Expect(updated.Status.DatameshTransitions[0].CurrentStep().Message).To(ContainSubstring("Data bootstrap in progress"))
		Expect(updated.Status.DatameshTransitions[0].CurrentStep().Message).To(ContainSubstring("UpToDate"))
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
			ObjectMeta: metav1.ObjectMeta{Name: "rsc-1", Generation: 1},
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
					mkFormationTransitionWithTime(formationStepIdxPreconfigure, metav1.Now()), // Just started.
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
					mkFormationTransitionWithTime(formationStepIdxPreconfigure, metav1.NewTime(time.Now().Add(-1*time.Hour))), // Started long ago.
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
					mkFormationTransitionWithTime(formationStepIdxPreconfigure, metav1.NewTime(time.Now().Add(-1*time.Hour))),
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
// Formation: Adopt
//

var _ = Describe("Formation: Adopt", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	})

	newAdoptRV := func() *v1alpha1.ReplicatedVolume {
		return &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "rv-1",
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
				Labels: map[string]string{
					v1alpha1.ReplicatedStorageClassLabelKey: "rsc-1",
				},
				Annotations: map[string]string{
					v1alpha1.AdoptRVRAnnotationKey: "",
				},
			},
			Spec: v1alpha1.ReplicatedVolumeSpec{
				Size:                       resource.MustParse("10Gi"),
				ReplicatedStorageClassName: "rsc-1",
			},
		}
	}

	// newPreconfiguredRVR creates an RVR that is preconfigured for adopt:
	// scheduled, DRBDConfigured=Unknown/InMaintenance, datamesh request operation=Join.
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
					State: v1alpha1.DiskStateUpToDate,
				},
				DatameshRequest: &v1alpha1.DatameshMembershipRequest{
					Operation:          v1alpha1.DatameshMembershipRequestOperationJoin,
					Type:               v1alpha1.ReplicaTypeDiskful,
					LVMVolumeGroupName: "lvg-1",
				},
			},
		}
		obju.SetStatusCondition(rvr, metav1.Condition{
			Type:   v1alpha1.ReplicatedVolumeReplicaCondScheduledType,
			Status: metav1.ConditionTrue,
			Reason: v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonScheduled,
		})
		// Mark as in maintenance (adopt requires DRBDRs to be in maintenance mode).
		obju.SetStatusCondition(rvr, metav1.Condition{
			Type:   v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType,
			Status: metav1.ConditionUnknown,
			Reason: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonInMaintenance,
		})
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

	//nolint:unparam // rvName is always "rv-1" in current tests, but kept as param for future extensibility.
	newPreconfiguredAccessRVR := func(rvName string, id uint8, nodeName string) *v1alpha1.ReplicatedVolumeReplica {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name:       v1alpha1.FormatReplicatedVolumeReplicaName(rvName, id),
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: rvName,
				Type:                 v1alpha1.ReplicaTypeAccess,
				NodeName:             nodeName,
			},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				Addresses: []v1alpha1.DRBDResourceAddressStatus{
					{SystemNetworkName: "Internal"},
				},
				DatameshRequest: &v1alpha1.DatameshMembershipRequest{
					Operation: v1alpha1.DatameshMembershipRequestOperationJoin,
					Type:      v1alpha1.ReplicaTypeAccess,
				},
			},
		}
		obju.SetStatusCondition(rvr, metav1.Condition{
			Type:   v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType,
			Status: metav1.ConditionUnknown,
			Reason: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonInMaintenance,
		})
		obju.SetStatusCondition(rvr, metav1.Condition{
			Type:   v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType,
			Status: metav1.ConditionTrue,
			Reason: v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonSatisfied,
		})
		return rvr
	}

	//nolint:unparam // rvName is always "rv-1" in current tests, but kept as param for future extensibility.
	newPreconfiguredTieBreakerRVR := func(rvName string, id uint8, nodeName string) *v1alpha1.ReplicatedVolumeReplica {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name:       v1alpha1.FormatReplicatedVolumeReplicaName(rvName, id),
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: rvName,
				Type:                 v1alpha1.ReplicaTypeTieBreaker,
				NodeName:             nodeName,
			},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				Addresses: []v1alpha1.DRBDResourceAddressStatus{
					{SystemNetworkName: "Internal"},
				},
				DatameshRequest: &v1alpha1.DatameshMembershipRequest{
					Operation: v1alpha1.DatameshMembershipRequestOperationJoin,
					Type:      v1alpha1.ReplicaTypeTieBreaker,
				},
			},
		}
		obju.SetStatusCondition(rvr, metav1.Condition{
			Type:   v1alpha1.ReplicatedVolumeReplicaCondScheduledType,
			Status: metav1.ConditionTrue,
			Reason: v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonScheduled,
		})
		obju.SetStatusCondition(rvr, metav1.Condition{
			Type:   v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType,
			Status: metav1.ConditionUnknown,
			Reason: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonInMaintenance,
		})
		obju.SetStatusCondition(rvr, metav1.Condition{
			Type:   v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType,
			Status: metav1.ConditionTrue,
			Reason: v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonSatisfied,
		})
		return rvr
	}

	mkAdoptTransition := func(activeStepIdx int) v1alpha1.ReplicatedVolumeDatameshTransition {
		now := metav1.Now()
		steps := make([]v1alpha1.ReplicatedVolumeDatameshTransitionStep, adoptStepCount)
		for i := range steps {
			steps[i].Name = adoptStepNames[i]
			switch {
			case i < activeStepIdx:
				steps[i].Status = v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted
				steps[i].StartedAt = &now
				steps[i].CompletedAt = &now
			case i == activeStepIdx:
				steps[i].Status = v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive
				steps[i].StartedAt = &now
			default:
				steps[i].Status = v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending
			}
		}
		return v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:   v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation,
			Group:  v1alpha1.ReplicatedVolumeDatameshTransitionGroupFormation,
			PlanID: formationPlanAdopt,
			Steps:  steps,
		}
	}

	It("creates adopt/v1 transition and waits for preconfigured RVRs (no creation)", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1")
		rsp := newTestRSPWithNodes("test-pool", "node-1")
		rv := newAdoptRV()

		// Unscheduled RVR — adopt should wait, not create new ones.
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

		rvrCreateCalled := false
		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr).
			WithStatusSubresource(rv, rsc, rvr).
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
		Expect(result.Requeue).To(BeTrue()) //nolint:staticcheck // ContinueAndRequeue
		Expect(rvrCreateCalled).To(BeFalse(), "adopt must NOT create new RVRs")

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
		Expect(updated.Status.DatameshTransitions).To(HaveLen(1))
		Expect(updated.Status.DatameshTransitions[0].PlanID).To(Equal(formationPlanAdopt))
		Expect(updated.Status.DatameshTransitions[0].Steps).To(HaveLen(adoptStepCount))
	})

	It("does NOT delete excess RVRs", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1") // FTT=0,GMDR=0 → 1 diskful in create, but adopt keeps all
		rsp := newTestRSPWithNodes("test-pool", "node-1", "node-2")
		rv := newAdoptRV()

		rvr0 := newPreconfiguredRVR("rv-1", 0, "node-1")
		rvr1 := newPreconfiguredRVR("rv-1", 1, "node-2")

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr0, rvr1).
			WithStatusSubresource(rv, rsc, rvr0, rvr1).
			Build()
		rec := NewReconciler(cl, scheme)

		_, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())

		// Both RVRs should still exist (not deleted).
		var updatedRVR0 v1alpha1.ReplicatedVolumeReplica
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr0), &updatedRVR0)).To(Succeed())
		Expect(updatedRVR0.DeletionTimestamp).To(BeNil())

		var updatedRVR1 v1alpha1.ReplicatedVolumeReplica
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr1), &updatedRVR1)).To(Succeed())
		Expect(updatedRVR1.DeletionTimestamp).To(BeNil())
	})

	It("transitions to PopulateAndVerifyDatamesh when prerequisites are verified", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1")
		rsp := newTestRSPWithNodes("test-pool", "node-1")
		rv := newAdoptRV()

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

		// Should have shared secret set and members added.
		Expect(updated.Status.Datamesh.SharedSecret).NotTo(BeEmpty())
		Expect(updated.Status.Datamesh.Members).To(HaveLen(1))
		Expect(updated.Status.DatameshRevision).To(Equal(int64(2)))
	})

	It("uses shared secret from adopt-shared-secret annotation", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1")
		rsp := newTestRSPWithNodes("test-pool", "node-1")
		rv := newAdoptRV()
		rv.Annotations[v1alpha1.AdoptSharedSecretAnnotationKey] = "my-preexisting-secret"

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

		Expect(updated.Status.Datamesh.SharedSecret).To(Equal("my-preexisting-secret"))
		Expect(updated.Status.Datamesh.SharedSecretAlg).To(Equal(v1alpha1.SharedSecretAlgSHA256))
		Expect(updated.Status.Datamesh.Members).To(HaveLen(1))
	})

	It("fails when adopt-shared-secret annotation is present but empty", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1")
		rsp := newTestRSPWithNodes("test-pool", "node-1")
		rv := newAdoptRV()
		rv.Annotations[v1alpha1.AdoptSharedSecretAnnotationKey] = ""

		rvr := newPreconfiguredRVR("rv-1", 0, "node-1")

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr).
			WithStatusSubresource(rv, rsc, rvr).
			Build()
		rec := NewReconciler(cl, scheme)

		_, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("empty"))
	})

	It("fails when adopt-shared-secret annotation exceeds 64 characters", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1")
		rsp := newTestRSPWithNodes("test-pool", "node-1")
		rv := newAdoptRV()
		rv.Annotations[v1alpha1.AdoptSharedSecretAnnotationKey] = strings.Repeat("x", 65)

		rvr := newPreconfiguredRVR("rv-1", 0, "node-1")

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr).
			WithStatusSubresource(rv, rsc, rvr).
			Build()
		rec := NewReconciler(cl, scheme)

		_, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("exceeds"))
	})

	It("generates random secret when adopt-shared-secret annotation is absent", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1")
		rsp := newTestRSPWithNodes("test-pool", "node-1")
		rv := newAdoptRV()

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

		Expect(updated.Status.Datamesh.SharedSecret).NotTo(BeEmpty())
		Expect(updated.Status.Datamesh.SharedSecret).NotTo(Equal("my-preexisting-secret"))
		Expect(updated.Status.Datamesh.SharedSecretAlg).To(Equal(v1alpha1.SharedSecretAlgSHA256))
	})

	It("completes formation when all replicas are healthy (ExitMaintenance)", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1")
		rsp := newTestRSPWithNodes("test-pool", "node-1")

		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "rv-1",
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
				Labels:     map[string]string{v1alpha1.ReplicatedStorageClassLabelKey: "rsc-1"},
				Annotations: map[string]string{
					v1alpha1.AdoptRVRAnnotationKey: "",
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
							Name: v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", 0),
							Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "node-1",
							Addresses:          []v1alpha1.DRBDResourceAddressStatus{{SystemNetworkName: "Internal"}},
							LVMVolumeGroupName: "lvg-1",
						},
					},
				},
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					{
						Type:   v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation,
						Group:  v1alpha1.ReplicatedVolumeDatameshTransitionGroupFormation,
						PlanID: formationPlanAdopt,
						Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
							{Name: adoptStepNames[0], Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted},
							{Name: adoptStepNames[1], Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted},
							{Name: adoptStepNames[2], Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
								StartedAt: ptr.To(metav1.Now())},
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
				ReplicatedVolumeName: "rv-1", Type: v1alpha1.ReplicaTypeDiskful,
				NodeName: "node-1", LVMVolumeGroupName: "lvg-1",
			},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshRevision: 1,
				BackingVolume: &v1alpha1.ReplicatedVolumeReplicaStatusBackingVolume{
					Size:  ptr.To(resource.MustParse("11Gi")),
					State: v1alpha1.DiskStateUpToDate,
				},
			},
		}
		obju.SetStatusCondition(rvr, metav1.Condition{
			Type: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType, Status: metav1.ConditionTrue,
			Reason: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonConfigured,
		})
		obju.SetStatusCondition(rvr, metav1.Condition{
			Type: v1alpha1.ReplicatedVolumeReplicaCondReadyType, Status: metav1.ConditionTrue,
			Reason: v1alpha1.ReplicatedVolumeReplicaCondReadyReasonReady,
		})

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr).
			WithStatusSubresource(rv, rsc, rvr).
			Build()
		rec := NewReconciler(cl, scheme)

		_, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
		Expect(updated.Status.DatameshTransitions).To(BeEmpty(), "formation should be completed")
	})

	It("does not create DRBDResourceOperation", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1")
		rsp := newTestRSPWithNodes("test-pool", "node-1")
		rv := newAdoptRV()

		rvr := newPreconfiguredRVR("rv-1", 0, "node-1")

		drbdrOpCreateCalled := false
		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr).
			WithStatusSubresource(rv, rsc, rvr).
			WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					if _, ok := obj.(*v1alpha1.DRBDResourceOperation); ok {
						drbdrOpCreateCalled = true
					}
					return cl.Create(ctx, obj, opts...)
				},
			}).
			Build()
		rec := NewReconciler(cl, scheme)

		_, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())
		Expect(drbdrOpCreateCalled).To(BeFalse(), "adopt must NOT create DRBDResourceOperation")
	})

	// ── VerifyPrerequisites ──

	It("Access RVR with nodeName passes scheduling gate", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1") // FTT=0, GMDR=0 → 1 D, no TB
		rsp := newTestRSPWithNodes("test-pool", "node-1", "node-2")
		rv := newAdoptRV()

		rvr0 := newPreconfiguredRVR("rv-1", 0, "node-1")
		rvr1 := newPreconfiguredAccessRVR("rv-1", 1, "node-2")

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr0, rvr1).
			WithStatusSubresource(rv, rsc, rvr0, rvr1).
			Build()
		rec := NewReconciler(cl, scheme)

		_, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
		Expect(updated.Status.Datamesh.SharedSecret).NotTo(BeEmpty(), "should advance past VerifyPrerequisites")
		Expect(updated.Status.Datamesh.Members).To(HaveLen(2))

		memberTypes := map[v1alpha1.DatameshMemberType]bool{}
		for _, m := range updated.Status.Datamesh.Members {
			memberTypes[m.Type] = true
		}
		Expect(memberTypes).To(HaveKey(v1alpha1.DatameshMemberTypeDiskful))
		Expect(memberTypes).To(HaveKey(v1alpha1.DatameshMemberTypeAccess))
	})

	It("Access RVR without nodeName blocks scheduling gate", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1")
		rsp := newTestRSPWithNodes("test-pool", "node-1")
		rv := newAdoptRV()

		rvr0 := newPreconfiguredRVR("rv-1", 0, "node-1")
		rvr1 := newPreconfiguredAccessRVR("rv-1", 1, "") // empty nodeName

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr0, rvr1).
			WithStatusSubresource(rv, rsc, rvr0, rvr1).
			Build()
		rec := NewReconciler(cl, scheme)

		result, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Requeue).To(BeTrue()) //nolint:staticcheck // ContinueAndRequeue

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
		Expect(updated.Status.Datamesh.SharedSecret).To(BeEmpty(), "should not advance past VerifyPrerequisites")
		Expect(updated.Status.DatameshTransitions).To(HaveLen(1))
		Expect(updated.Status.DatameshTransitions[0].Steps[adoptStepIdxVerifyPrerequisites].Message).
			To(ContainSubstring("scheduled"))
	})

	It("proceeds when TB count exceeds expected (found 1, need 0)", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1") // FTT=0, GMDR=0 → D=1, no TB needed
		rsp := newTestRSPWithNodes("test-pool", "node-1", "node-2")
		rv := newAdoptRV()

		rvr0 := newPreconfiguredRVR("rv-1", 0, "node-1")
		rvr1 := newPreconfiguredTieBreakerRVR("rv-1", 1, "node-2")

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr0, rvr1).
			WithStatusSubresource(rv, rsc, rvr0, rvr1).
			Build()
		rec := NewReconciler(cl, scheme)

		_, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
		Expect(updated.Status.Datamesh.SharedSecret).NotTo(BeEmpty(), "should advance past VerifyPrerequisites")
		Expect(updated.Status.Datamesh.Members).To(HaveLen(2))
	})

	// ── PopulateAndVerifyDatamesh ──

	It("populates all member types including TieBreaker and Access", func(ctx SpecContext) {
		rsc := &v1alpha1.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{Name: "rsc-1", Generation: 1},
			Status: v1alpha1.ReplicatedStorageClassStatus{
				ConfigurationGeneration: 1,
				Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
					Topology:                        v1alpha1.TopologyIgnored,
					FailuresToTolerate:              1,
					GuaranteedMinimumDataRedundancy: 0,
					VolumeAccess:                    v1alpha1.VolumeAccessLocal,
					ReplicatedStoragePoolName:       "test-pool",
				},
			},
		}
		rsp := newTestRSPWithNodes("test-pool", "node-1", "node-2", "node-3", "node-4")

		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "rv-1",
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
				Labels:     map[string]string{v1alpha1.ReplicatedStorageClassLabelKey: "rsc-1"},
				Annotations: map[string]string{
					v1alpha1.AdoptRVRAnnotationKey: "",
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
					Topology:                        v1alpha1.TopologyIgnored,
					FailuresToTolerate:              1,
					GuaranteedMinimumDataRedundancy: 0,
					VolumeAccess:                    v1alpha1.VolumeAccessLocal,
					ReplicatedStoragePoolName:       "test-pool",
				},
				DatameshRevision: 1,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					SystemNetworkNames: []string{"Internal"},
					Size:               resource.MustParse("10Gi"),
				},
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					mkAdoptTransition(adoptStepIdxPopulateAndVerifyDatamesh),
				},
			},
		}
		obju.SetStatusCondition(rv, metav1.Condition{
			Type: v1alpha1.ReplicatedVolumeCondConfigurationReadyType, Status: metav1.ConditionTrue,
			Reason: v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonReady, Message: "Configuration is ready",
		})

		rvr0 := newPreconfiguredRVR("rv-1", 0, "node-1")
		rvr1 := newPreconfiguredRVR("rv-1", 1, "node-2")
		rvr2 := newPreconfiguredTieBreakerRVR("rv-1", 2, "node-3")
		rvr3 := newPreconfiguredAccessRVR("rv-1", 3, "node-4")

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr0, rvr1, rvr2, rvr3).
			WithStatusSubresource(rv, rsc, rvr0, rvr1, rvr2, rvr3).
			Build()
		rec := NewReconciler(cl, scheme)

		_, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
		Expect(updated.Status.Datamesh.SharedSecret).NotTo(BeEmpty())
		Expect(updated.Status.Datamesh.Members).To(HaveLen(4))

		memberTypes := map[v1alpha1.DatameshMemberType]int{}
		for _, m := range updated.Status.Datamesh.Members {
			memberTypes[m.Type]++
		}
		Expect(memberTypes[v1alpha1.DatameshMemberTypeDiskful]).To(Equal(2))
		Expect(memberTypes[v1alpha1.DatameshMemberTypeTieBreaker]).To(Equal(1))
		Expect(memberTypes[v1alpha1.DatameshMemberTypeAccess]).To(Equal(1))
	})

	It("sets Multiattach when multiple replicas are attached", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1")
		rsp := newTestRSPWithNodes("test-pool", "node-1", "node-2")

		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "rv-1",
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
				Labels:     map[string]string{v1alpha1.ReplicatedStorageClassLabelKey: "rsc-1"},
				Annotations: map[string]string{
					v1alpha1.AdoptRVRAnnotationKey: "",
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
					Topology:           v1alpha1.TopologyIgnored,
					FailuresToTolerate: 0, GuaranteedMinimumDataRedundancy: 0,
					VolumeAccess:              v1alpha1.VolumeAccessLocal,
					ReplicatedStoragePoolName: "test-pool",
				},
				DatameshRevision: 1,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					SystemNetworkNames: []string{"Internal"},
					Size:               resource.MustParse("10Gi"),
				},
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					mkAdoptTransition(adoptStepIdxPopulateAndVerifyDatamesh),
				},
			},
		}
		obju.SetStatusCondition(rv, metav1.Condition{
			Type: v1alpha1.ReplicatedVolumeCondConfigurationReadyType, Status: metav1.ConditionTrue,
			Reason: v1alpha1.ReplicatedVolumeCondConfigurationReadyReasonReady, Message: "Configuration is ready",
		})

		rvr0 := newPreconfiguredRVR("rv-1", 0, "node-1")
		rvr0.Status.Attachment = &v1alpha1.ReplicatedVolumeReplicaStatusAttachment{}

		rvr1 := newPreconfiguredAccessRVR("rv-1", 1, "node-2")
		rvr1.Status.Attachment = &v1alpha1.ReplicatedVolumeReplicaStatusAttachment{}

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr0, rvr1).
			WithStatusSubresource(rv, rsc, rvr0, rvr1).
			Build()
		rec := NewReconciler(cl, scheme)

		_, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
		Expect(updated.Status.Datamesh.Multiattach).To(BeTrue(), "Multiattach should be set when 2 replicas are attached")
	})

	// ── ExitMaintenance ──

	It("waits for replicas to exit maintenance mode", func(ctx SpecContext) {
		rsc := newRSCWithConfiguration("rsc-1")
		rsp := newTestRSPWithNodes("test-pool", "node-1")

		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "rv-1",
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
				Labels:     map[string]string{v1alpha1.ReplicatedStorageClassLabelKey: "rsc-1"},
				Annotations: map[string]string{
					v1alpha1.AdoptRVRAnnotationKey: "",
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
					Topology:           v1alpha1.TopologyIgnored,
					FailuresToTolerate: 0, GuaranteedMinimumDataRedundancy: 0,
					VolumeAccess:              v1alpha1.VolumeAccessLocal,
					ReplicatedStoragePoolName: "test-pool",
				},
				DatameshRevision: 2,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					SharedSecret:            "test-secret",
					SharedSecretAlg:         v1alpha1.SharedSecretAlgSHA256,
					SystemNetworkNames:      []string{"Internal"},
					Size:                    resource.MustParse("10Gi"),
					Quorum:                  1,
					QuorumMinimumRedundancy: 1,
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
					mkAdoptTransition(adoptStepIdxExitMaintenance),
				},
			},
		}

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
				DatameshRevision: 2,
			},
		}
		// Still in maintenance.
		obju.SetStatusCondition(rvr, metav1.Condition{
			Type: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType, Status: metav1.ConditionUnknown,
			Reason: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonInMaintenance,
		})

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, rvr).
			WithStatusSubresource(rv, rsc, rvr).
			Build()
		rec := NewReconciler(cl, scheme)

		result, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Requeue).To(BeTrue()) //nolint:staticcheck // ContinueAndRequeue

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
		Expect(updated.Status.DatameshTransitions).To(HaveLen(1))
		Expect(updated.Status.DatameshTransitions[0].Steps[adoptStepIdxExitMaintenance].Message).
			To(ContainSubstring("exit maintenance"))
	})

})

// ──────────────────────────────────────────────────────────────────────────────
// Pure helpers: isRVMetadataInSync, applyRVMetadata
//

// ──────────────────────────────────────────────────────────────────────────────
// Formation: tie-breaker readiness (review finding #5)
//

var _ = Describe("Formation: tie-breaker readiness", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	})

	// formationStartedAt is recent enough that the formation restart timeout has NOT passed:
	// these specs assert on wait messages, not on restarts.
	formationStartedAt := metav1.NewTime(time.Now().Add(-5 * time.Second))

	rvrName := func(id uint8) string { return v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", id) }

	newRSPWith := func(nodeNames ...string) *v1alpha1.ReplicatedStoragePool {
		rsp := newTestRSP("test-pool")
		rsp.Status.EligibleNodes = make([]v1alpha1.ReplicatedStoragePoolEligibleNode, len(nodeNames))
		for i, nn := range nodeNames {
			rsp.Status.EligibleNodes[i] = v1alpha1.ReplicatedStoragePoolEligibleNode{
				NodeName:        nn,
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{{Name: "lvg-1"}},
			}
		}
		return rsp
	}

	newRSCr2 := func() *v1alpha1.ReplicatedStorageClass {
		rsc := newRSCWithConfiguration("rsc-1")
		rsc.Status.Configuration.FailuresToTolerate = 1
		return rsc
	}

	// newRV2D1TB builds an r2 volume (FTT=1 → 2D+1TB) whose datamesh is already populated,
	// sitting at the given create/v1 formation step.
	newRV2D1TB := func(stepIdx int) *v1alpha1.ReplicatedVolume {
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
					Topology: v1alpha1.TopologyIgnored, FailuresToTolerate: 1, GuaranteedMinimumDataRedundancy: 0,
					VolumeAccess: v1alpha1.VolumeAccessLocal, ReplicatedStoragePoolName: "test-pool",
				},
				DatameshRevision: 1,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					SharedSecret: "test-secret", SharedSecretAlg: v1alpha1.SharedSecretAlgSHA256,
					SystemNetworkNames: []string{"Internal"}, Size: resource.MustParse("10Gi"),
					Quorum: 2, QuorumMinimumRedundancy: 1,
					Members: []v1alpha1.DatameshMember{
						{
							Name: rvrName(0), Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "node-1",
							Addresses:          []v1alpha1.DRBDResourceAddressStatus{{SystemNetworkName: "Internal"}},
							LVMVolumeGroupName: "lvg-1",
						},
						{
							Name: rvrName(1), Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "node-2",
							Addresses:          []v1alpha1.DRBDResourceAddressStatus{{SystemNetworkName: "Internal"}},
							LVMVolumeGroupName: "lvg-1",
						},
						{
							Name: rvrName(2), Type: v1alpha1.DatameshMemberTypeTieBreaker, NodeName: "node-3",
							Addresses: []v1alpha1.DRBDResourceAddressStatus{{SystemNetworkName: "Internal"}},
						},
					},
				},
				DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
					mkFormationTransitionWithTime(stepIdx, formationStartedAt),
				},
			},
		}
	}

	// newReadyDiskful builds a diskful RVR that passes every diskful gate of establish-connectivity:
	// current datamesh revision, DRBDConfigured=True, Connected + Established replication with the
	// other diskful peer.
	newReadyDiskful := func(id uint8, nodeName string, diskState v1alpha1.DiskState, peerIDs ...uint8) *v1alpha1.ReplicatedVolumeReplica {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: rvrName(id), Finalizers: []string{v1alpha1.RVControllerFinalizer}},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1", Type: v1alpha1.ReplicaTypeDiskful,
				NodeName: nodeName, LVMVolumeGroupName: "lvg-1",
			},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshRevision: 1,
				Addresses:        []v1alpha1.DRBDResourceAddressStatus{{SystemNetworkName: "Internal"}},
				BackingVolume: &v1alpha1.ReplicatedVolumeReplicaStatusBackingVolume{
					Size: ptr.To(resource.MustParse("11Gi")), State: diskState,
				},
			},
		}
		for _, peerID := range peerIDs {
			rvr.Status.Peers = append(rvr.Status.Peers, v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{
				Name: rvrName(peerID), Type: v1alpha1.ReplicaTypeDiskful,
				ConnectionState:  v1alpha1.ConnectionStateConnected,
				ReplicationState: v1alpha1.ReplicationStateEstablished,
			})
		}
		obju.SetStatusCondition(rvr, metav1.Condition{
			Type: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType, Status: metav1.ConditionTrue,
			Reason: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonConfigured,
		})
		return rvr
	}

	// newOperationalTieBreaker builds a tie-breaker RVR that satisfies every readiness gate:
	// current datamesh revision, DRBDConfigured=True, and a Connected report for each diskful peer.
	newOperationalTieBreaker := func(id uint8, nodeName string, peerIDs ...uint8) *v1alpha1.ReplicatedVolumeReplica {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: rvrName(id), Finalizers: []string{v1alpha1.RVControllerFinalizer}},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1", Type: v1alpha1.ReplicaTypeTieBreaker, NodeName: nodeName,
			},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DatameshRevision: 1,
				Addresses:        []v1alpha1.DRBDResourceAddressStatus{{SystemNetworkName: "Internal"}},
			},
		}
		for _, peerID := range peerIDs {
			rvr.Status.Peers = append(rvr.Status.Peers, v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{
				Name: rvrName(peerID), Type: v1alpha1.ReplicaTypeDiskful,
				ConnectionState: v1alpha1.ConnectionStateConnected,
			})
		}
		obju.SetStatusCondition(rvr, metav1.Condition{
			Type: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType, Status: metav1.ConditionTrue,
			Reason: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonConfigured,
		})
		return rvr
	}

	// newSucceededBootstrapOp builds the data bootstrap operation of a 2D volume on thick LVM
	// (force-resync) in Succeeded phase.
	newSucceededBootstrapOp := func() *v1alpha1.DRBDResourceOperation {
		return &v1alpha1.DRBDResourceOperation{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "rv-1-formation",
				CreationTimestamp: metav1.NewTime(formationStartedAt.Add(1 * time.Second)),
			},
			Spec: v1alpha1.DRBDResourceOperationSpec{
				NodeName: "node-1", DRBDResourceName: rvrName(0),
				Type:          v1alpha1.DRBDResourceOperationCreateNewUUID,
				CreateNewUUID: &v1alpha1.CreateNewUUIDParams{ForceResync: true},
			},
			Status: v1alpha1.DRBDResourceOperationStatus{Phase: v1alpha1.DRBDOperationPhaseSucceeded},
		}
	}

	It("does not advance to data bootstrap while the tie-breaker is not operational", func(ctx SpecContext) {
		// Red repro (#5): every diskful gate passes, but the tie-breaker has neither applied the
		// datamesh revision nor reported a single connection. Formation must not walk past
		// establish-connectivity with a tie-breaker that breaks the tie for nobody.
		rsc := newRSCr2()
		rsp := newRSPWith("node-1", "node-2", "node-3")
		rv := newRV2D1TB(formationStepIdxEstablishConnectivity)

		d0 := newReadyDiskful(0, "node-1", v1alpha1.DiskStateInconsistent, 1)
		d1 := newReadyDiskful(1, "node-2", v1alpha1.DiskStateInconsistent, 0)
		tb := newOperationalTieBreaker(2, "node-3", 0, 1)
		tb.Status.DatameshRevision = 0
		tb.Status.Peers = nil

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, d0, d1, tb).
			WithStatusSubresource(rv, rsc, d0, d1, tb).
			Build()
		rec := NewReconciler(cl, scheme)

		result, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Requeue())

		var drbdrOp v1alpha1.DRBDResourceOperation
		err = cl.Get(ctx, client.ObjectKey{Name: "rv-1-formation"}, &drbdrOp)
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "data bootstrap must not start before the tie-breaker is ready")

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
		Expect(updated.Status.DatameshTransitions).To(HaveLen(1))
		Expect(updated.Status.DatameshTransitions[0].CurrentStep().Message).To(ContainSubstring("tie-breaker"))
	})

	It("does not complete formation while the tie-breaker is not operational", func(ctx SpecContext) {
		// Red repro (#5): data bootstrap succeeded and both diskful replicas are UpToDate, but the
		// tie-breaker is not operational — formation must not be declared complete.
		rsc := newRSCr2()
		rsp := newRSPWith("node-1", "node-2", "node-3")
		rv := newRV2D1TB(formationStepIdxBootstrapData)

		d0 := newReadyDiskful(0, "node-1", v1alpha1.DiskStateUpToDate, 1)
		d1 := newReadyDiskful(1, "node-2", v1alpha1.DiskStateUpToDate, 0)
		tb := newOperationalTieBreaker(2, "node-3", 0, 1)
		tb.Status.DatameshRevision = 0
		tb.Status.Peers = nil

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, d0, d1, tb, newSucceededBootstrapOp()).
			WithStatusSubresource(rv, rsc, d0, d1, tb).
			Build()
		rec := NewReconciler(cl, scheme)

		_, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
		Expect(updated.Status.DatameshTransitions).To(HaveLen(1), "formation must not complete without a ready tie-breaker")
		Expect(updated.Status.DatameshTransitions[0].CurrentStep().Message).To(ContainSubstring("tie-breaker"))
	})

	It("completes formation when the tie-breaker is operational", func(ctx SpecContext) {
		rsc := newRSCr2()
		rsp := newRSPWith("node-1", "node-2", "node-3")
		rv := newRV2D1TB(formationStepIdxBootstrapData)

		d0 := newReadyDiskful(0, "node-1", v1alpha1.DiskStateUpToDate, 1)
		d1 := newReadyDiskful(1, "node-2", v1alpha1.DiskStateUpToDate, 0)
		tb := newOperationalTieBreaker(2, "node-3", 0, 1)

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, d0, d1, tb, newSucceededBootstrapOp()).
			WithStatusSubresource(rv, rsc, d0, d1, tb).
			Build()
		rec := NewReconciler(cl, scheme)

		_, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
		Expect(updated.Status.DatameshTransitions).To(BeEmpty(), "formation completes once the tie-breaker is operational")
	})

	It("waits when the tie-breaker loses readiness during data bootstrap, completes after recovery", func(ctx SpecContext) {
		// The establish-connectivity gate passed a whole data bootstrap ago; connectivity can be
		// lost while the resync runs. The final re-check must catch that — and must only wait, so
		// that recovery completes formation instead of restarting it.
		rsc := newRSCr2()
		rsp := newRSPWith("node-1", "node-2", "node-3")
		rv := newRV2D1TB(formationStepIdxBootstrapData)

		d0 := newReadyDiskful(0, "node-1", v1alpha1.DiskStateUpToDate, 1)
		d1 := newReadyDiskful(1, "node-2", v1alpha1.DiskStateUpToDate, 0)
		tb := newOperationalTieBreaker(2, "node-3", 0, 1)
		tb.Status.Peers = nil // connections lost during bootstrap

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, d0, d1, tb, newSucceededBootstrapOp()).
			WithStatusSubresource(rv, rsc, d0, d1, tb).
			Build()
		rec := NewReconciler(cl, scheme)

		_, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
		Expect(updated.Status.DatameshTransitions).To(HaveLen(1), "formation must neither complete nor restart")
		Expect(updated.Status.DatameshTransitions[0].CurrentStep().Name).To(Equal(formationStepNames[formationStepIdxBootstrapData]))
		Expect(updated.Status.DatameshTransitions[0].CurrentStep().Message).To(ContainSubstring("connection to"))

		// The replicas are still there — waiting did not delete a bootstrapped layout.
		var rvrList v1alpha1.ReplicatedVolumeReplicaList
		Expect(cl.List(ctx, &rvrList)).To(Succeed())
		Expect(rvrList.Items).To(HaveLen(3))

		// The tie-breaker reconnects.
		var updatedTB v1alpha1.ReplicatedVolumeReplica
		Expect(cl.Get(ctx, client.ObjectKey{Name: rvrName(2)}, &updatedTB)).To(Succeed())
		updatedTB.Status.Peers = []v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{
			{Name: rvrName(0), Type: v1alpha1.ReplicaTypeDiskful, ConnectionState: v1alpha1.ConnectionStateConnected},
			{Name: rvrName(1), Type: v1alpha1.ReplicaTypeDiskful, ConnectionState: v1alpha1.ConnectionStateConnected},
		}
		Expect(cl.Status().Update(ctx, &updatedTB)).To(Succeed())

		_, err = rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())

		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
		Expect(updated.Status.DatameshTransitions).To(BeEmpty(), "formation completes after the tie-breaker recovers")
	})

	It("completes r3 formation unchanged (no tie-breaker to gate)", func(ctx SpecContext) {
		// Regression control: a layout without tie-breakers must not be affected by the gates.
		rsc := newRSCr2()
		rsc.Status.Configuration.GuaranteedMinimumDataRedundancy = 1 // FTT=1, GMDR=1 → 3D, no TB
		rsp := newRSPWith("node-1", "node-2", "node-3")
		rv := newRV2D1TB(formationStepIdxBootstrapData)
		rv.Status.Configuration.GuaranteedMinimumDataRedundancy = 1
		rv.Status.Datamesh.Members[2] = v1alpha1.DatameshMember{
			Name: rvrName(2), Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "node-3",
			Addresses:          []v1alpha1.DRBDResourceAddressStatus{{SystemNetworkName: "Internal"}},
			LVMVolumeGroupName: "lvg-1",
		}

		d0 := newReadyDiskful(0, "node-1", v1alpha1.DiskStateUpToDate, 1, 2)
		d1 := newReadyDiskful(1, "node-2", v1alpha1.DiskStateUpToDate, 0, 2)
		d2 := newReadyDiskful(2, "node-3", v1alpha1.DiskStateUpToDate, 0, 1)

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, d0, d1, d2, newSucceededBootstrapOp()).
			WithStatusSubresource(rv, rsc, d0, d1, d2).
			Build()
		rec := NewReconciler(cl, scheme)

		_, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
		Expect(updated.Status.DatameshTransitions).To(BeEmpty(), "r3 formation completes as before")
	})

	It("completes adopt formation with a degraded tie-breaker (adopt is not gated)", func(ctx SpecContext) {
		// Adopt accepts pre-existing replicas as-is, even degraded ones — normal operation heals
		// them afterwards. Gating adopt on tie-breaker readiness would keep such volumes in
		// formation forever, so the gates are deliberately create/v1-only.
		rsc := newRSCr2()
		rsp := newRSPWith("node-1", "node-2", "node-3")
		// Same 2D+1TB datamesh, but formed by adopt/v1 — the transition below replaces the
		// create/v1 one, so the step index passed here is irrelevant.
		rv := newRV2D1TB(formationStepIdxBootstrapData)
		rv.Annotations = map[string]string{v1alpha1.AdoptRVRAnnotationKey: ""}
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{{
			Type:   v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation,
			Group:  v1alpha1.ReplicatedVolumeDatameshTransitionGroupFormation,
			PlanID: formationPlanAdopt,
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: adoptStepNames[0], Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted},
				{Name: adoptStepNames[1], Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted},
				{
					Name: adoptStepNames[2], Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					StartedAt: ptr.To(formationStartedAt),
				},
			},
		}}

		d0 := newReadyDiskful(0, "node-1", v1alpha1.DiskStateUpToDate, 1)
		d1 := newReadyDiskful(1, "node-2", v1alpha1.DiskStateUpToDate, 0)
		tb := newOperationalTieBreaker(2, "node-3", 0, 1)
		tb.Status.DatameshRevision = 0 // degraded: no revision applied
		tb.Status.Peers = nil          // and no confirmed connection

		cl := newClientBuilder(scheme).
			WithObjects(rv, rsc, rsp, d0, d1, tb).
			WithStatusSubresource(rv, rsc, d0, d1, tb).
			Build()
		rec := NewReconciler(cl, scheme)

		_, err := rec.Reconcile(ctx, RequestFor(rv))
		Expect(err).NotTo(HaveOccurred())

		var updated v1alpha1.ReplicatedVolume
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), &updated)).To(Succeed())
		Expect(updated.Status.DatameshTransitions).To(BeEmpty(), "adopt completes regardless of tie-breaker readiness")
	})

	// ── gates, one negative each (pure helper) ──

	Describe("computeActualTieBreakerReadiness", func() {
		var (
			rv         *v1alpha1.ReplicatedVolume
			d0, d1, tb *v1alpha1.ReplicatedVolumeReplica
		)

		BeforeEach(func() {
			rv = newRV2D1TB(formationStepIdxEstablishConnectivity)
			d0 = newReadyDiskful(0, "node-1", v1alpha1.DiskStateInconsistent, 1)
			d1 = newReadyDiskful(1, "node-2", v1alpha1.DiskStateInconsistent, 0)
			tb = newOperationalTieBreaker(2, "node-3", 0, 1)
		})

		readiness := func(rvrs ...*v1alpha1.ReplicatedVolumeReplica) (idset.IDSet, string) {
			if len(rvrs) == 0 {
				rvrs = []*v1alpha1.ReplicatedVolumeReplica{d0, d1, tb}
			}
			return computeActualTieBreakerReadiness(rv, rvrs)
		}

		// reportTieBreakerConnected makes the given diskful replicas report the tie-breaker as
		// Connected — the other side of the "one fresh side is enough" rule.
		reportTieBreakerConnected := func(rvrs ...*v1alpha1.ReplicatedVolumeReplica) {
			for _, rvr := range rvrs {
				rvr.Status.Peers = append(rvr.Status.Peers, v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{
					Name: rvrName(2), Type: v1alpha1.ReplicaTypeTieBreaker,
					ConnectionState: v1alpha1.ConnectionStateConnected,
				})
			}
		}

		It("reports ready when every gate passes", func() {
			notReady, msg := readiness()
			Expect(msg).To(BeEmpty())
			Expect(notReady.IsEmpty()).To(BeTrue())
		})

		It("reports ready for a layout without tie-breakers", func() {
			rv.Status.Datamesh.Members = rv.Status.Datamesh.Members[:2]
			notReady, msg := readiness(d0, d1)
			Expect(msg).To(BeEmpty())
			Expect(notReady.IsEmpty()).To(BeTrue())
		})

		// Gate 1: members == active tie-breaker replicas.

		It("gate 1: flags a tie-breaker member whose replica is terminating", func() {
			now := metav1.Now()
			tb.DeletionTimestamp = &now

			notReady, msg := readiness()
			Expect(msg).To(ContainSubstring("Datamesh tie-breaker members mismatch"))
			Expect(msg).To(ContainSubstring("datamesh has [#2]"))
			Expect(notReady).To(Equal(idset.Of(2)))
		})

		It("gate 1: flags an active tie-breaker replica that is not a datamesh member", func() {
			rv.Status.Datamesh.Members = rv.Status.Datamesh.Members[:2]

			notReady, msg := readiness()
			Expect(msg).To(ContainSubstring("Datamesh tie-breaker members mismatch"))
			Expect(notReady).To(Equal(idset.Of(2)))
		})

		// Gate 2: current datamesh revision applied.

		It("gate 2: flags a tie-breaker that has not applied the datamesh revision", func() {
			tb.Status.DatameshRevision = 0

			notReady, msg := readiness()
			Expect(msg).To(ContainSubstring("datamesh revision 0 applied, want 1"))
			Expect(notReady).To(Equal(idset.Of(2)))
		})

		It("gate 2: accepts a tie-breaker that is ahead of the datamesh revision (cache skew)", func() {
			tb.Status.DatameshRevision = 2

			_, msg := readiness()
			Expect(msg).To(BeEmpty())
		})

		// Gate 3: DRBDConfigured=True with a current ObservedGeneration.

		It("gate 3: flags a tie-breaker without a DRBDConfigured condition", func() {
			tb.Status.Conditions = nil

			notReady, msg := readiness()
			Expect(msg).To(ContainSubstring("DRBD is not configured"))
			Expect(notReady).To(Equal(idset.Of(2)))
		})

		It("gate 3: flags a tie-breaker with DRBDConfigured=False", func() {
			obju.SetStatusCondition(tb, metav1.Condition{
				Type: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType, Status: metav1.ConditionFalse,
				Reason: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonAgentNotReady,
			})

			_, msg := readiness()
			Expect(msg).To(ContainSubstring("DRBD is not configured"))
		})

		It("gate 3: flags a tie-breaker whose DRBDConfigured is stale", func() {
			tb.Generation = 2 // condition still observes generation 0

			_, msg := readiness()
			Expect(msg).To(ContainSubstring("DRBD is not configured"))
		})

		// Gate 4: every tie-breaker↔diskful connection confirmed by one fresh side.

		It("gate 4: flags an unconfirmed connection to one of the diskful members", func() {
			tb.Status.Peers = tb.Status.Peers[:1] // reports only the connection to #0

			notReady, msg := readiness()
			Expect(msg).To(ContainSubstring("connection to " + rvrName(1) + " is not confirmed"))
			Expect(notReady).To(Equal(idset.Of(2)))
		})

		It("gate 4: accepts a connection confirmed by the diskful side alone", func() {
			tb.Status.Peers = nil
			reportTieBreakerConnected(d0, d1)

			_, msg := readiness()
			Expect(msg).To(BeEmpty())
		})

		It("gate 4: rejects a confirmation from a diskful side with a stale revision", func() {
			tb.Status.Peers = nil
			reportTieBreakerConnected(d0, d1)
			d0.Status.DatameshRevision = 0
			d1.Status.DatameshRevision = 0

			_, msg := readiness()
			Expect(msg).To(ContainSubstring("is not confirmed"))
		})

		It("gate 4: rejects a confirmation from a diskful side whose agent is not ready", func() {
			tb.Status.Peers = nil
			reportTieBreakerConnected(d0, d1)
			for _, rvr := range []*v1alpha1.ReplicatedVolumeReplica{d0, d1} {
				obju.SetStatusCondition(rvr, metav1.Condition{
					Type: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType, Status: metav1.ConditionTrue,
					Reason: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonAgentNotReady,
				})
			}

			_, msg := readiness()
			Expect(msg).To(ContainSubstring("is not confirmed"))
		})

		It("gate 4: accepts a member without a replica object when the tie-breaker confirms it", func() {
			// The peer replica is gone from the list; the tie-breaker's own fresh report is still
			// a valid confirmation (the same rule the datamesh engine applies).
			_, msg := readiness(d0, tb)
			Expect(msg).To(BeEmpty())
		})
	})
})
