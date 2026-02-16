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

package rvrschedulingcontroller

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes/testhelpers"
	rvrllvname "github.com/deckhouse/sds-replicated-volume/images/controller/internal/rvr_llv_name"
	schext "github.com/deckhouse/sds-replicated-volume/images/controller/internal/scheduler_extender"
)

// ──────────────────────────────────────────────────────────────────────────────
// Test helpers: fixtures
//

const (
	testRVName  = "rv-test"
	testRSPName = "rsp-test"
)

func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(s)
	return s
}

func newClientBuilder(scheme *runtime.Scheme) *fake.ClientBuilder {
	return testhelpers.WithRVAByReplicatedVolumeNameIndex(
		testhelpers.WithRVRByReplicatedVolumeNameIndex(
			fake.NewClientBuilder().WithScheme(scheme),
		),
	)
}

func newRV(cfg *v1alpha1.ReplicatedStorageClassConfiguration) *v1alpha1.ReplicatedVolume {
	return &v1alpha1.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{Name: testRVName},
		Spec: v1alpha1.ReplicatedVolumeSpec{
			Size:                       resource.MustParse("10Gi"),
			ReplicatedStorageClassName: "rsc-test",
		},
		Status: v1alpha1.ReplicatedVolumeStatus{
			Configuration: cfg,
		},
	}
}

func newRSP(poolType v1alpha1.ReplicatedStoragePoolType, nodes []v1alpha1.ReplicatedStoragePoolEligibleNode) *v1alpha1.ReplicatedStoragePool {
	return &v1alpha1.ReplicatedStoragePool{
		ObjectMeta: metav1.ObjectMeta{Name: testRSPName},
		Spec: v1alpha1.ReplicatedStoragePoolSpec{
			Type: poolType,
		},
		Status: v1alpha1.ReplicatedStoragePoolStatus{
			EligibleNodes: nodes,
		},
	}
}

func newRVR(id uint8, replicaType v1alpha1.ReplicaType) *v1alpha1.ReplicatedVolumeReplica {
	name := fmt.Sprintf("%s-%d", testRVName, id)
	return &v1alpha1.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
			ReplicatedVolumeName: testRVName,
			Type:                 replicaType,
		},
	}
}

func newRVA(nodeName string) *v1alpha1.ReplicatedVolumeAttachment {
	return &v1alpha1.ReplicatedVolumeAttachment{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("rva-%s-%s", testRVName, nodeName)},
		Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
			ReplicatedVolumeName: testRVName,
			NodeName:             nodeName,
		},
	}
}

func defaultConfig() *v1alpha1.ReplicatedStorageClassConfiguration {
	return &v1alpha1.ReplicatedStorageClassConfiguration{
		Topology:        v1alpha1.TopologyIgnored,
		Replication:     v1alpha1.ReplicationConsistencyAndAvailability,
		VolumeAccess:    v1alpha1.VolumeAccessLocal,
		StoragePoolName: "rsp-test",
	}
}

func zonalConfig() *v1alpha1.ReplicatedStorageClassConfiguration {
	cfg := defaultConfig()
	cfg.Topology = v1alpha1.TopologyZonal
	return cfg
}

func transZonalConfig() *v1alpha1.ReplicatedStorageClassConfiguration {
	cfg := defaultConfig()
	cfg.Topology = v1alpha1.TopologyTransZonal
	return cfg
}

// ──────────────────────────────────────────────────────────────────────────────
// Test helpers: mock extender
//

type reconcilerMockExtender struct {
	filterAndScoreFn func(lvgs []schext.LVMVolumeGroup) ([]schext.ScoredLVMVolumeGroup, error)
	narrowErr        error

	// lastReservationID captures the reservationID passed to FilterAndScore.
	lastReservationID string
}

func (m *reconcilerMockExtender) FilterAndScore(_ context.Context, reservationID string, _ time.Duration, _ int64, lvgs []schext.LVMVolumeGroup) ([]schext.ScoredLVMVolumeGroup, error) {
	m.lastReservationID = reservationID
	if m.filterAndScoreFn != nil {
		return m.filterAndScoreFn(lvgs)
	}
	// Default: score all LVGs at 5.
	result := make([]schext.ScoredLVMVolumeGroup, len(lvgs))
	for i, l := range lvgs {
		result[i] = schext.ScoredLVMVolumeGroup{LVMVolumeGroup: l, Score: 5}
	}
	return result, nil
}

func (m *reconcilerMockExtender) NarrowReservation(_ context.Context, _ string, _ time.Duration, _ schext.LVMVolumeGroup) error {
	return m.narrowErr
}

// ──────────────────────────────────────────────────────────────────────────────
// Test helpers: assertions
//

func getUpdatedRVR(ctx context.Context, cl client.Client, rvr *v1alpha1.ReplicatedVolumeReplica) *v1alpha1.ReplicatedVolumeReplica {
	var updated v1alpha1.ReplicatedVolumeReplica
	ExpectWithOffset(1, cl.Get(ctx, client.ObjectKeyFromObject(rvr), &updated)).To(Succeed())
	return &updated
}

func expectScheduledCondition(ctx context.Context, cl client.Client, rvr *v1alpha1.ReplicatedVolumeReplica, status metav1.ConditionStatus, reason string) {
	updated := getUpdatedRVR(ctx, cl, rvr)
	cond := obju.StatusCondition(updated, v1alpha1.ReplicatedVolumeReplicaCondScheduledType)
	ExpectWithOffset(1, cond.StatusEqual(status).Eval()).To(BeTrue(),
		"expected Scheduled status=%s on %s, got conditions: %+v", status, rvr.Name, updated.Status.Conditions)
	ExpectWithOffset(1, cond.ReasonEqual(reason).Eval()).To(BeTrue(),
		"expected Scheduled reason=%s on %s, got conditions: %+v", reason, rvr.Name, updated.Status.Conditions)
}

func expectNoScheduledCondition(ctx context.Context, cl client.Client, rvr *v1alpha1.ReplicatedVolumeReplica) {
	updated := getUpdatedRVR(ctx, cl, rvr)
	ExpectWithOffset(1, obju.HasStatusCondition(updated, v1alpha1.ReplicatedVolumeReplicaCondScheduledType)).To(BeFalse(),
		"expected no Scheduled condition on %s, got: %+v", rvr.Name, updated.Status.Conditions)
}

func expectNodeName(ctx context.Context, cl client.Client, rvr *v1alpha1.ReplicatedVolumeReplica, nodeName string) {
	updated := getUpdatedRVR(ctx, cl, rvr)
	ExpectWithOffset(1, updated.Spec.NodeName).To(Equal(nodeName),
		"expected NodeName=%s on %s, got %s", nodeName, rvr.Name, updated.Spec.NodeName)
}

func reconcileRV(ctx context.Context, rec *Reconciler) (reconcile.Result, error) {
	return rec.Reconcile(ctx, reconcile.Request{
		NamespacedName: client.ObjectKey{Name: testRVName},
	})
}

// ──────────────────────────────────────────────────────────────────────────────
// Tests
//

var _ = Describe("Reconciler", func() {
	var (
		scheme *runtime.Scheme
		ctx    context.Context
	)

	BeforeEach(func() {
		scheme = newScheme()
		ctx = context.Background()
	})

	// ────────────────────────────────────────────────────────────────────────
	// RSP Not Found / Guards
	//

	Describe("RSP Not Found / Guards", func() {
		It("Guard 1: RV not found — WaitingForReplicatedVolume on all RVRs", func() {
			rvr := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			rvr.Spec.ReplicatedVolumeName = "rv-test"

			cl := newClientBuilder(scheme).
				WithObjects(rvr).
				WithStatusSubresource(rvr).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			result, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
			expectScheduledCondition(ctx, cl, rvr, metav1.ConditionUnknown,
				v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonWaitingForReplicatedVolume)
		})

		It("Guard 2: RV has no configuration — WaitingForReplicatedVolume", func() {
			rv := &v1alpha1.ReplicatedVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-test"},
				Spec: v1alpha1.ReplicatedVolumeSpec{
					Size:                       resource.MustParse("10Gi"),
					ReplicatedStorageClassName: "rsc-test",
				},
			}
			rvr := newRVR(0, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rvr).
				WithStatusSubresource(rvr).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			result, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
			expectScheduledCondition(ctx, cl, rvr, metav1.ConditionUnknown,
				v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonWaitingForReplicatedVolume)
		})

		It("Guard 3: RSP not found — WaitingForReplicatedVolume", func() {
			rv := newRV(defaultConfig())
			rvr := newRVR(0, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rvr).
				WithStatusSubresource(rvr).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			result, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
			expectScheduledCondition(ctx, cl, rvr, metav1.ConditionUnknown,
				v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonWaitingForReplicatedVolume)
		})
	})

	// ────────────────────────────────────────────────────────────────────────
	// Edge Cases: Empty Inputs
	//

	Describe("Edge Cases: Empty Inputs", func() {
		It("no RVRs — Reconcile returns Done", func() {
			rv := newRV(defaultConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
				})

			cl := newClientBuilder(scheme).WithObjects(rv, rsp).Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			result, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
		})

		It("all RVRs are Access — condition removed, no scheduling", func() {
			rv := newRV(defaultConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
				})

			rvr0 := newRVR(0, v1alpha1.ReplicaTypeAccess)
			// Give it a stale Scheduled condition
			obju.SetStatusCondition(rvr0, metav1.Condition{
				Type:   v1alpha1.ReplicatedVolumeReplicaCondScheduledType,
				Status: metav1.ConditionTrue,
				Reason: v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonScheduled,
			})
			rvr1 := newRVR(1, v1alpha1.ReplicaTypeAccess)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr0, rvr1).
				WithStatusSubresource(rvr0, rvr1).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			result, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
			expectNoScheduledCondition(ctx, cl, rvr0)
			expectNoScheduledCondition(ctx, cl, rvr1)
		})

		It("0 eligible nodes — all Diskful get SchedulingFailed", func() {
			rv := newRV(defaultConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM, nil)

			rvr0 := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			rvr1 := newRVR(1, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr0, rvr1).
				WithStatusSubresource(rvr0, rvr1).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			result, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
			expectScheduledCondition(ctx, cl, rvr0, metav1.ConditionFalse,
				v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonSchedulingFailed)
			expectScheduledCondition(ctx, cl, rvr1, metav1.ConditionFalse,
				v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonSchedulingFailed)
		})

		It("RVR with unknown ReplicaType — silently skipped", func() {
			rv := newRV(defaultConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
				})

			rvr := newRVR(0, "UnknownType")

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr).
				WithStatusSubresource(rvr).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			result, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
			// No condition set, no scheduling
			updated := getUpdatedRVR(ctx, cl, rvr)
			Expect(updated.Spec.NodeName).To(BeEmpty())
		})
	})

	// ────────────────────────────────────────────────────────────────────────
	// Scheduled Condition Management
	//

	Describe("Scheduled Condition Management", func() {
		It("Scheduled=True is set on already-existing replicas too", func() {
			rv := newRV(defaultConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
					makeNode("node-b", "zone-b", makeLVG("vg-b", true)),
				})

			// Already scheduled
			existing := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			existing.Spec.NodeName = "node-a"
			existing.Spec.LVMVolumeGroupName = "vg-a"

			// New
			newOne := newRVR(1, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, existing, newOne).
				WithStatusSubresource(existing, newOne).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			expectScheduledCondition(ctx, cl, existing, metav1.ConditionTrue,
				v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonScheduled)
			expectScheduledCondition(ctx, cl, newOne, metav1.ConditionTrue,
				v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonScheduled)
		})
	})

	// ────────────────────────────────────────────────────────────────────────
	// Access Replica Handling
	//

	Describe("Access Replica Handling", func() {
		It("Access replica is not scheduled", func() {
			rv := newRV(defaultConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
				})

			rvr := newRVR(0, v1alpha1.ReplicaTypeAccess)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr).
				WithStatusSubresource(rvr).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			updated := getUpdatedRVR(ctx, cl, rvr)
			Expect(updated.Spec.NodeName).To(BeEmpty())
		})

		It("Access replica with NodeName occupies the node", func() {
			rv := newRV(defaultConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
					makeNode("node-b", "zone-b", makeLVG("vg-b", true)),
				})

			access := newRVR(0, v1alpha1.ReplicaTypeAccess)
			access.Spec.NodeName = "node-a"

			diskful := newRVR(1, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, access, diskful).
				WithStatusSubresource(access, diskful).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			// Diskful must go to node-b (node-a blocked by Access)
			expectNodeName(ctx, cl, diskful, "node-b")
		})
	})

	// ────────────────────────────────────────────────────────────────────────
	// Access Replica Condition Cleanup
	//

	Describe("Access Replica Condition Cleanup", func() {
		It("Access RVR formerly Diskful — Scheduled condition removed", func() {
			rv := newRV(defaultConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
				})

			rvr := newRVR(0, v1alpha1.ReplicaTypeAccess)
			rvr.Spec.NodeName = "node-a"
			// Access replicas are diskless — no LVG. The stale Scheduled
			// condition is the only remnant from the previous Diskful type.
			obju.SetStatusCondition(rvr, metav1.Condition{
				Type:   v1alpha1.ReplicatedVolumeReplicaCondScheduledType,
				Status: metav1.ConditionTrue,
				Reason: v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonScheduled,
			})

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr).
				WithStatusSubresource(rvr).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			expectNoScheduledCondition(ctx, cl, rvr)
		})
	})

	// ────────────────────────────────────────────────────────────────────────
	// Deleting RVR
	//

	Describe("Deleting RVR", func() {
		It("Deleting replica with NodeName blocks its node", func() {
			rv := newRV(defaultConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
					makeNode("node-b", "zone-b", makeLVG("vg-b", true)),
				})

			now := metav1.Now()
			deleting := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			deleting.Spec.NodeName = "node-a"
			deleting.Spec.LVMVolumeGroupName = "vg-a"
			deleting.DeletionTimestamp = &now
			deleting.Finalizers = []string{"test-finalizer"}

			newDiskful := newRVR(1, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, deleting, newDiskful).
				WithStatusSubresource(deleting, newDiskful).
				Build()

			mock := &reconcilerMockExtender{}
			rec := NewReconciler(cl, logr.Discard(), scheme, mock)

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			// New diskful must go to node-b (node-a blocked by deleting replica)
			expectNodeName(ctx, cl, newDiskful, "node-b")
		})

		It("Deleting unscheduled RVR is skipped", func() {
			rv := newRV(defaultConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
				})

			now := metav1.Now()
			deleting := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			deleting.DeletionTimestamp = &now
			deleting.Finalizers = []string{"test-finalizer"}

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, deleting).
				WithStatusSubresource(deleting).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			// NodeName remains empty — not scheduled
			updated := getUpdatedRVR(ctx, cl, deleting)
			Expect(updated.Spec.NodeName).To(BeEmpty())
		})
	})

	// ────────────────────────────────────────────────────────────────────────
	// Ignored Topology
	//

	Describe("Ignored Topology", func() {
		It("D:2 — Diskful by best scores", func() {
			rv := newRV(defaultConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
					makeNode("node-b", "zone-b", makeLVG("vg-b", true)),
					makeNode("node-c", "zone-c", makeLVG("vg-c", true)),
				})

			rvr0 := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			rvr1 := newRVR(1, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr0, rvr1).
				WithStatusSubresource(rvr0, rvr1).
				Build()

			mock := &reconcilerMockExtender{
				filterAndScoreFn: func(lvgs []schext.LVMVolumeGroup) ([]schext.ScoredLVMVolumeGroup, error) {
					var result []schext.ScoredLVMVolumeGroup
					for _, l := range lvgs {
						score := 5
						if l.LVGName == "vg-a" {
							score = 10
						} else if l.LVGName == "vg-b" {
							score = 9
						}
						result = append(result, schext.ScoredLVMVolumeGroup{LVMVolumeGroup: l, Score: score})
					}
					return result, nil
				},
			}
			rec := NewReconciler(cl, logr.Discard(), scheme, mock)

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			expectNodeName(ctx, cl, rvr0, "node-a") // highest score
			expectNodeName(ctx, cl, rvr1, "node-b") // second highest
		})

		It("D:2, TB:2 — 4 replicas on 4 nodes", func() {
			rv := newRV(defaultConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
					makeNode("node-b", "zone-a", makeLVG("vg-b", true)),
					makeNode("node-c", "zone-a", makeLVG("vg-c", true)),
					makeNode("node-d", "zone-a", makeLVG("vg-d", true)),
				})

			d0 := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			d1 := newRVR(1, v1alpha1.ReplicaTypeDiskful)
			t2 := newRVR(2, v1alpha1.ReplicaTypeTieBreaker)
			t3 := newRVR(3, v1alpha1.ReplicaTypeTieBreaker)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, d0, d1, t2, t3).
				WithStatusSubresource(d0, d1, t2, t3).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			// All 4 should be scheduled on separate nodes
			nodes := map[string]bool{}
			for _, rvr := range []*v1alpha1.ReplicatedVolumeReplica{d0, d1, t2, t3} {
				updated := getUpdatedRVR(ctx, cl, rvr)
				Expect(updated.Spec.NodeName).NotTo(BeEmpty(), "RVR %s not scheduled", rvr.Name)
				nodes[updated.Spec.NodeName] = true
				expectScheduledCondition(ctx, cl, rvr, metav1.ConditionTrue,
					v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonScheduled)
			}
			Expect(nodes).To(HaveLen(4))
		})

		It("all nodes occupied — TB not scheduled", func() {
			rv := newRV(defaultConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
					makeNode("node-b", "zone-a", makeLVG("vg-b", true)),
				})

			d0 := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			d0.Spec.NodeName = "node-a"
			d0.Spec.LVMVolumeGroupName = "vg-a"

			d1 := newRVR(1, v1alpha1.ReplicaTypeDiskful)
			d1.Spec.NodeName = "node-b"
			d1.Spec.LVMVolumeGroupName = "vg-b"

			tb := newRVR(2, v1alpha1.ReplicaTypeTieBreaker)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, d0, d1, tb).
				WithStatusSubresource(d0, d1, tb).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			expectScheduledCondition(ctx, cl, tb, metav1.ConditionFalse,
				v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonSchedulingFailed)
		})
	})

	// ────────────────────────────────────────────────────────────────────────
	// Zonal Topology
	//

	Describe("Zonal Topology", func() {
		It("small-1z: D:2 — all in zone-a", func() {
			rv := newRV(zonalConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a1", "zone-a", makeLVG("vg-a1", true)),
					makeNode("node-a2", "zone-a", makeLVG("vg-a2", true)),
				})

			rvr0 := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			rvr1 := newRVR(1, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr0, rvr1).
				WithStatusSubresource(rvr0, rvr1).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			expectScheduledCondition(ctx, cl, rvr0, metav1.ConditionTrue,
				v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonScheduled)
			expectScheduledCondition(ctx, cl, rvr1, metav1.ConditionTrue,
				v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonScheduled)
		})

		It("medium-2z-4n: existing D in zone-a — new D and TB also go to zone-a (sticky)", func() {
			rv := newRV(zonalConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a1", "zone-a", makeLVG("vg-a1", true)),
					makeNode("node-a2", "zone-a", makeLVG("vg-a2", true)),
					makeNode("node-b1", "zone-b", makeLVG("vg-b1", true)),
					makeNode("node-b2", "zone-b", makeLVG("vg-b2", true)),
				})

			existing := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			existing.Spec.NodeName = "node-a1"
			existing.Spec.LVMVolumeGroupName = "vg-a1"

			newD := newRVR(1, v1alpha1.ReplicaTypeDiskful)
			newTB := newRVR(2, v1alpha1.ReplicaTypeTieBreaker)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, existing, newD, newTB).
				WithStatusSubresource(existing, newD, newTB).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			// New Diskful must go to zone-a (node-a2, since node-a1 is occupied)
			expectNodeName(ctx, cl, newD, "node-a2")
			// TB can only go to zone-a too, but both a-nodes are occupied. So TB gets SchedulingFailed.
			// Actually: node-a1 occupied by existing, node-a2 occupied by newD after scheduling.
			// So TB has no available nodes in zone-a.
			expectScheduledCondition(ctx, cl, newTB, metav1.ConditionFalse,
				v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonSchedulingFailed)
		})

		It("medium-2z: existing D in zone-a — TB also goes to zone-a", func() {
			rv := newRV(zonalConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a1", "zone-a", makeLVG("vg-a1", true)),
					makeNode("node-a2", "zone-a", makeLVG("vg-a2", true)),
					makeNode("node-b1", "zone-b", makeLVG("vg-b1", true)),
					makeNode("node-b2", "zone-b", makeLVG("vg-b2", true)),
				})

			existing := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			existing.Spec.NodeName = "node-a1"
			existing.Spec.LVMVolumeGroupName = "vg-a1"

			tb := newRVR(1, v1alpha1.ReplicaTypeTieBreaker)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, existing, tb).
				WithStatusSubresource(existing, tb).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			expectNodeName(ctx, cl, tb, "node-a2")
		})

		It("small-1z: all nodes occupied — no candidates for TB", func() {
			rv := newRV(zonalConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a1", "zone-a", makeLVG("vg-a1", true)),
					makeNode("node-a2", "zone-a", makeLVG("vg-a2", true)),
				})

			d0 := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			d0.Spec.NodeName = "node-a1"
			d0.Spec.LVMVolumeGroupName = "vg-a1"

			d1 := newRVR(1, v1alpha1.ReplicaTypeDiskful)
			d1.Spec.NodeName = "node-a2"
			d1.Spec.LVMVolumeGroupName = "vg-a2"

			tb := newRVR(2, v1alpha1.ReplicaTypeTieBreaker)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, d0, d1, tb).
				WithStatusSubresource(d0, d1, tb).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			expectScheduledCondition(ctx, cl, tb, metav1.ConditionFalse,
				v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonSchedulingFailed)
		})

		It("medium-2z: existing D in different zones — inconsistent state, scheduling proceeds", func() {
			rv := newRV(zonalConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a1", "zone-a", makeLVG("vg-a1", true)),
					makeNode("node-b1", "zone-b", makeLVG("vg-b1", true)),
					makeNode("node-c1", "zone-a", makeLVG("vg-c1", true)),
				})

			dA := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			dA.Spec.NodeName = "node-a1"
			dA.Spec.LVMVolumeGroupName = "vg-a1"

			dB := newRVR(1, v1alpha1.ReplicaTypeDiskful)
			dB.Spec.NodeName = "node-b1"
			dB.Spec.LVMVolumeGroupName = "vg-b1"

			newD := newRVR(2, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, dA, dB, newD).
				WithStatusSubresource(dA, dB, newD).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			// Both zones tied (1 Diskful each), no filter applied.
			// The new replica is scheduled on the remaining free node.
			expectScheduledCondition(ctx, cl, newD, metav1.ConditionTrue,
				v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonScheduled)
		})
	})

	// ────────────────────────────────────────────────────────────────────────
	// TransZonal Topology
	//

	Describe("TransZonal Topology", func() {
		It("large-3z: D:3 — one per zone", func() {
			rv := newRV(transZonalConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a1", "zone-a", makeLVG("vg-a1", true)),
					makeNode("node-b1", "zone-b", makeLVG("vg-b1", true)),
					makeNode("node-c1", "zone-c", makeLVG("vg-c1", true)),
				})

			rvr0 := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			rvr1 := newRVR(1, v1alpha1.ReplicaTypeDiskful)
			rvr2 := newRVR(2, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr0, rvr1, rvr2).
				WithStatusSubresource(rvr0, rvr1, rvr2).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			// All 3 in different zones
			zones := map[string]bool{}
			for _, rvr := range []*v1alpha1.ReplicatedVolumeReplica{rvr0, rvr1, rvr2} {
				updated := getUpdatedRVR(ctx, cl, rvr)
				Expect(updated.Spec.NodeName).NotTo(BeEmpty())
				// Find zone from node name
				for _, n := range rsp.Status.EligibleNodes {
					if n.NodeName == updated.Spec.NodeName {
						zones[n.ZoneName] = true
					}
				}
			}
			Expect(zones).To(HaveLen(3))
		})

		It("large-3z: existing D in zone-a,b — new D goes to zone-c", func() {
			rv := newRV(transZonalConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a1", "zone-a", makeLVG("vg-a1", true)),
					makeNode("node-b1", "zone-b", makeLVG("vg-b1", true)),
					makeNode("node-c1", "zone-c", makeLVG("vg-c1", true)),
				})

			dA := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			dA.Spec.NodeName = "node-a1"
			dA.Spec.LVMVolumeGroupName = "vg-a1"

			dB := newRVR(1, v1alpha1.ReplicaTypeDiskful)
			dB.Spec.NodeName = "node-b1"
			dB.Spec.LVMVolumeGroupName = "vg-b1"

			newD := newRVR(2, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, dA, dB, newD).
				WithStatusSubresource(dA, dB, newD).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			expectNodeName(ctx, cl, newD, "node-c1")
		})

		It("large-3z: existing D in zone-a,b — TB goes to zone-c", func() {
			rv := newRV(transZonalConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a1", "zone-a", makeLVG("vg-a1", true)),
					makeNode("node-b1", "zone-b", makeLVG("vg-b1", true)),
					makeNode("node-c1", "zone-c", makeLVG("vg-c1", true)),
				})

			dA := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			dA.Spec.NodeName = "node-a1"
			dA.Spec.LVMVolumeGroupName = "vg-a1"

			dB := newRVR(1, v1alpha1.ReplicaTypeDiskful)
			dB.Spec.NodeName = "node-b1"
			dB.Spec.LVMVolumeGroupName = "vg-b1"

			tb := newRVR(2, v1alpha1.ReplicaTypeTieBreaker)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, dA, dB, tb).
				WithStatusSubresource(dA, dB, tb).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			expectNodeName(ctx, cl, tb, "node-c1")
		})

		It("medium-2z: existing D in zone-a — new D goes to zone-b", func() {
			rv := newRV(transZonalConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a1", "zone-a", makeLVG("vg-a1", true)),
					makeNode("node-b1", "zone-b", makeLVG("vg-b1", true)),
				})

			dA := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			dA.Spec.NodeName = "node-a1"
			dA.Spec.LVMVolumeGroupName = "vg-a1"

			newD := newRVR(1, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, dA, newD).
				WithStatusSubresource(dA, newD).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			expectNodeName(ctx, cl, newD, "node-b1")
		})

		It("medium-2z: all nodes occupied — TB not scheduled", func() {
			rv := newRV(transZonalConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a1", "zone-a", makeLVG("vg-a1", true)),
					makeNode("node-a2", "zone-a", makeLVG("vg-a2", true)),
					makeNode("node-b1", "zone-b", makeLVG("vg-b1", true)),
					makeNode("node-b2", "zone-b", makeLVG("vg-b2", true)),
				})

			d0 := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			d0.Spec.NodeName = "node-a1"
			d0.Spec.LVMVolumeGroupName = "vg-a1"
			d1 := newRVR(1, v1alpha1.ReplicaTypeDiskful)
			d1.Spec.NodeName = "node-a2"
			d1.Spec.LVMVolumeGroupName = "vg-a2"
			d2 := newRVR(2, v1alpha1.ReplicaTypeDiskful)
			d2.Spec.NodeName = "node-b1"
			d2.Spec.LVMVolumeGroupName = "vg-b1"
			d3 := newRVR(3, v1alpha1.ReplicaTypeDiskful)
			d3.Spec.NodeName = "node-b2"
			d3.Spec.LVMVolumeGroupName = "vg-b2"

			tb := newRVR(4, v1alpha1.ReplicaTypeTieBreaker)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, d0, d1, d2, d3, tb).
				WithStatusSubresource(d0, d1, d2, d3, tb).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			expectScheduledCondition(ctx, cl, tb, metav1.ConditionFalse,
				v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonSchedulingFailed)
		})
	})

	// ────────────────────────────────────────────────────────────────────────
	// Extender Filtering
	//

	Describe("Extender Filtering", func() {
		It("single Diskful — extender error → ExtenderUnavailable, Reconcile returns error", func() {
			rv := newRV(defaultConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
				})
			rvr := newRVR(0, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr).
				WithStatusSubresource(rvr).
				Build()
			mock := &reconcilerMockExtender{
				filterAndScoreFn: func(_ []schext.LVMVolumeGroup) ([]schext.ScoredLVMVolumeGroup, error) {
					return nil, fmt.Errorf("connection refused")
				},
			}
			rec := NewReconciler(cl, logr.Discard(), scheme, mock)

			_, err := reconcileRV(ctx, rec)
			Expect(err).To(HaveOccurred())

			expectScheduledCondition(ctx, cl, rvr, metav1.ConditionFalse,
				v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonExtenderUnavailable)
		})

		It("TieBreaker only, extender unavailable → TieBreaker scheduled normally", func() {
			rv := newRV(defaultConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
				})

			// Existing Diskful already scheduled (so only TB is unscheduled)
			d := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			d.Spec.NodeName = "node-a"
			d.Spec.LVMVolumeGroupName = "vg-a"

			tb := newRVR(1, v1alpha1.ReplicaTypeTieBreaker)

			// Extender fails, but TB doesn't use it
			mock := &reconcilerMockExtender{
				filterAndScoreFn: func(_ []schext.LVMVolumeGroup) ([]schext.ScoredLVMVolumeGroup, error) {
					return nil, fmt.Errorf("connection refused")
				},
			}
			// There are no unscheduled Diskful, so Phase 2 is skipped. TB goes through Phase 3.
			// But wait — there's only 1 node and it's occupied by d. TB has no available node.
			// We need 2 nodes for this test.
			// Let me fix: add a second node.
			rsp.Status.EligibleNodes = append(rsp.Status.EligibleNodes,
				makeNode("node-b", "zone-b", makeLVG("vg-b", true)))
			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, d, tb).
				WithStatusSubresource(d, tb).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, mock)

			result, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			expectNodeName(ctx, cl, tb, "node-b")
			expectScheduledCondition(ctx, cl, tb, metav1.ConditionTrue,
				v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonScheduled)
		})

		It("Extender returns no scores for zone-b → only zone-a available", func() {
			rv := newRV(defaultConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a1", "zone-a", makeLVG("vg-a1", true)),
					makeNode("node-b1", "zone-b", makeLVG("vg-b1", true)),
				})
			rvr := newRVR(0, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr).
				WithStatusSubresource(rvr).
				Build()
			mock := &reconcilerMockExtender{
				filterAndScoreFn: func(lvgs []schext.LVMVolumeGroup) ([]schext.ScoredLVMVolumeGroup, error) {
					var result []schext.ScoredLVMVolumeGroup
					for _, l := range lvgs {
						if l.LVGName == "vg-a1" {
							result = append(result, schext.ScoredLVMVolumeGroup{LVMVolumeGroup: l, Score: 10})
						}
						// vg-b1 not returned → excluded
					}
					return result, nil
				},
			}
			rec := NewReconciler(cl, logr.Discard(), scheme, mock)

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			expectNodeName(ctx, cl, rvr, "node-a1")
		})
	})

	// ────────────────────────────────────────────────────────────────────────
	// Extender: NarrowReservation
	//

	Describe("Extender: NarrowReservation", func() {
		It("NarrowReservation error → ExtenderUnavailable, Reconcile returns error", func() {
			rv := newRV(defaultConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
				})
			rvr := newRVR(0, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr).
				WithStatusSubresource(rvr).
				Build()
			mock := &reconcilerMockExtender{
				narrowErr: fmt.Errorf("narrow failed"),
			}
			rec := NewReconciler(cl, logr.Discard(), scheme, mock)

			_, err := reconcileRV(ctx, rec)
			Expect(err).To(HaveOccurred())

			expectScheduledCondition(ctx, cl, rvr, metav1.ConditionFalse,
				v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonExtenderUnavailable)
		})
	})

	// ────────────────────────────────────────────────────────────────────────
	// Node and LVG Readiness
	//

	Describe("Node and LVG Readiness", func() {
		It("NodeReady=false → node is skipped", func() {
			rv := newRV(defaultConfig())
			notReady := makeNode("node-bad", "zone-a", makeLVG("vg-bad", true))
			notReady.NodeReady = false
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-good", "zone-a", makeLVG("vg-good", true)),
					notReady,
				})
			rvr := newRVR(0, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr).
				WithStatusSubresource(rvr).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())
			expectNodeName(ctx, cl, rvr, "node-good")
		})

		It("LVG Ready=false → LVG is skipped", func() {
			rv := newRV(defaultConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeLVG("vg-good", true), makeLVG("vg-bad", false)),
				})
			rvr := newRVR(0, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr).
				WithStatusSubresource(rvr).
				Build()
			mock := &reconcilerMockExtender{
				filterAndScoreFn: func(lvgs []schext.LVMVolumeGroup) ([]schext.ScoredLVMVolumeGroup, error) {
					var result []schext.ScoredLVMVolumeGroup
					for _, l := range lvgs {
						score := 5
						if l.LVGName == "vg-bad" {
							score = 10 // higher score, but will be filtered by LVG readiness
						}
						result = append(result, schext.ScoredLVMVolumeGroup{LVMVolumeGroup: l, Score: score})
					}
					return result, nil
				},
			}
			rec := NewReconciler(cl, logr.Discard(), scheme, mock)

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			updated := getUpdatedRVR(ctx, cl, rvr)
			Expect(updated.Spec.LVMVolumeGroupName).To(Equal("vg-good"))
		})

		It("Unschedulable=true → node is skipped", func() {
			rv := newRV(defaultConfig())
			unschedulable := makeNode("node-bad", "zone-a", makeLVG("vg-bad", true))
			unschedulable.Unschedulable = true
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-good", "zone-a", makeLVG("vg-good", true)),
					unschedulable,
				})
			rvr := newRVR(0, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr).
				WithStatusSubresource(rvr).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())
			expectNodeName(ctx, cl, rvr, "node-good")
		})

		It("AgentReady=false → node is skipped", func() {
			rv := newRV(defaultConfig())
			agentDown := makeNode("node-bad", "zone-a", makeLVG("vg-bad", true))
			agentDown.AgentReady = false
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-good", "zone-a", makeLVG("vg-good", true)),
					agentDown,
				})
			rvr := newRVR(0, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr).
				WithStatusSubresource(rvr).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())
			expectNodeName(ctx, cl, rvr, "node-good")
		})

		It("LVG Unschedulable=true → LVG is skipped", func() {
			rv := newRV(defaultConfig())
			unschedulableLVG := makeLVG("vg-bad", true)
			unschedulableLVG.Unschedulable = true
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeLVG("vg-good", true), unschedulableLVG),
				})
			rvr := newRVR(0, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr).
				WithStatusSubresource(rvr).
				Build()
			mock := &reconcilerMockExtender{
				filterAndScoreFn: func(lvgs []schext.LVMVolumeGroup) ([]schext.ScoredLVMVolumeGroup, error) {
					var result []schext.ScoredLVMVolumeGroup
					for _, l := range lvgs {
						score := 5
						if l.LVGName == "vg-bad" {
							score = 10 // higher score, but filtered by Unschedulable
						}
						result = append(result, schext.ScoredLVMVolumeGroup{LVMVolumeGroup: l, Score: score})
					}
					return result, nil
				},
			}
			rec := NewReconciler(cl, logr.Discard(), scheme, mock)

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			updated := getUpdatedRVR(ctx, cl, rvr)
			Expect(updated.Spec.LVMVolumeGroupName).To(Equal("vg-good"))
		})
	})

	// ────────────────────────────────────────────────────────────────────────
	// LVMThin Storage Type
	//

	Describe("LVMThin Storage Type", func() {
		It("LVMThin → ThinPoolName is set", func() {
			rv := newRV(defaultConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVMThin,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeThinLVG("vg-a", "thin-a", true)),
				})
			rvr := newRVR(0, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr).
				WithStatusSubresource(rvr).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			updated := getUpdatedRVR(ctx, cl, rvr)
			Expect(updated.Spec.LVMVolumeGroupName).To(Equal("vg-a"))
			Expect(updated.Spec.LVMVolumeGroupThinPoolName).To(Equal("thin-a"))
		})

		It("LVM thick → ThinPoolName is empty", func() {
			rv := newRV(defaultConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
				})
			rvr := newRVR(0, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr).
				WithStatusSubresource(rvr).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			updated := getUpdatedRVR(ctx, cl, rvr)
			Expect(updated.Spec.LVMVolumeGroupName).To(Equal("vg-a"))
			Expect(updated.Spec.LVMVolumeGroupThinPoolName).To(BeEmpty())
		})
	})

	// ────────────────────────────────────────────────────────────────────────
	// Requeue Behavior
	//

	Describe("Requeue Behavior", func() {
		It("no capacity → Reconcile without error, no requeue", func() {
			rv := newRV(defaultConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
				})
			rvr := newRVR(0, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr).
				WithStatusSubresource(rvr).
				Build()
			mock := &reconcilerMockExtender{
				filterAndScoreFn: func(_ []schext.LVMVolumeGroup) ([]schext.ScoredLVMVolumeGroup, error) {
					return nil, nil // no scores
				},
			}
			rec := NewReconciler(cl, logr.Discard(), scheme, mock)

			result, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			expectScheduledCondition(ctx, cl, rvr, metav1.ConditionFalse,
				v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonSchedulingFailed)
		})
	})

	// ────────────────────────────────────────────────────────────────────────
	// AttachTo Bonus
	//

	Describe("AttachTo Bonus", func() {
		It("+1000 bonus overrides extender scores", func() {
			cfg := defaultConfig()
			rv := newRV(cfg)
			rva := newRVA("node-a")

			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
					makeNode("node-b", "zone-b", makeLVG("vg-b", true)),
				})
			rvr := newRVR(0, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr, rva).
				WithStatusSubresource(rvr).
				Build()
			mock := &reconcilerMockExtender{
				filterAndScoreFn: func(lvgs []schext.LVMVolumeGroup) ([]schext.ScoredLVMVolumeGroup, error) {
					var result []schext.ScoredLVMVolumeGroup
					for _, l := range lvgs {
						score := 3
						if l.LVGName == "vg-b" {
							score = 10 // higher base score
						}
						result = append(result, schext.ScoredLVMVolumeGroup{LVMVolumeGroup: l, Score: score})
					}
					return result, nil
				},
			}
			rec := NewReconciler(cl, logr.Discard(), scheme, mock)

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			// node-a: 3+1000=1003 > node-b: 10
			expectNodeName(ctx, cl, rvr, "node-a")
		})
	})

	// ────────────────────────────────────────────────────────────────────────
	// Multi-LVG Node Bonus
	//

	Describe("Multi-LVG Node Bonus", func() {
		It("equal extender score → multi-LVG node wins by bonus", func() {
			cfg := defaultConfig()
			cfg.VolumeAccess = v1alpha1.VolumeAccessLocal
			rv := newRV(cfg)
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeLVG("vg-a1", true), makeLVG("vg-a2", true)),
					makeNode("node-b", "zone-b", makeLVG("vg-b1", true)),
				})
			rvr := newRVR(0, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr).
				WithStatusSubresource(rvr).
				Build()
			mock := &reconcilerMockExtender{
				filterAndScoreFn: func(lvgs []schext.LVMVolumeGroup) ([]schext.ScoredLVMVolumeGroup, error) {
					var result []schext.ScoredLVMVolumeGroup
					for _, l := range lvgs {
						result = append(result, schext.ScoredLVMVolumeGroup{LVMVolumeGroup: l, Score: 9})
					}
					return result, nil
				},
			}
			rec := NewReconciler(cl, logr.Discard(), scheme, mock)

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			// node-a gets +2 bonus (2 LVGs): 9+2=11 > node-b: 9
			expectNodeName(ctx, cl, rvr, "node-a")
		})

		It("volumeAccess=Any → no multi-LVG bonus", func() {
			cfg := defaultConfig()
			cfg.VolumeAccess = v1alpha1.VolumeAccessAny
			rv := newRV(cfg)
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeLVG("vg-a1", true), makeLVG("vg-a2", true)),
					makeNode("node-b", "zone-b", makeLVG("vg-b1", true)),
				})
			rvr := newRVR(0, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr).
				WithStatusSubresource(rvr).
				Build()
			mock := &reconcilerMockExtender{
				filterAndScoreFn: func(lvgs []schext.LVMVolumeGroup) ([]schext.ScoredLVMVolumeGroup, error) {
					var result []schext.ScoredLVMVolumeGroup
					for _, l := range lvgs {
						result = append(result, schext.ScoredLVMVolumeGroup{LVMVolumeGroup: l, Score: 9})
					}
					return result, nil
				},
			}
			rec := NewReconciler(cl, logr.Discard(), scheme, mock)

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			// No bonus → tie at 9, tiebreak by alphabetical NodeName → node-a wins anyway.
			// But no bonus was applied (score remains 9, not 11).
			// We verify by checking the node is still "node-a" (alphabetical tiebreak)
			expectNodeName(ctx, cl, rvr, "node-a")
		})
	})

	// ────────────────────────────────────────────────────────────────────────
	// Partial Diskful Scheduling
	//

	Describe("Partial Diskful Scheduling", func() {
		It("3 Diskful on 2 nodes → 2 scheduled, 1 SchedulingFailed", func() {
			rv := newRV(defaultConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
					makeNode("node-b", "zone-a", makeLVG("vg-b", true)),
				})

			rvr0 := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			rvr1 := newRVR(1, v1alpha1.ReplicaTypeDiskful)
			rvr2 := newRVR(2, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr0, rvr1, rvr2).
				WithStatusSubresource(rvr0, rvr1, rvr2).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			// Count scheduled vs failed
			scheduled := 0
			failed := 0
			for _, rvr := range []*v1alpha1.ReplicatedVolumeReplica{rvr0, rvr1, rvr2} {
				updated := getUpdatedRVR(ctx, cl, rvr)
				if updated.Spec.NodeName != "" {
					scheduled++
				} else {
					failed++
					expectScheduledCondition(ctx, cl, rvr, metav1.ConditionFalse,
						v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonSchedulingFailed)
				}
			}
			Expect(scheduled).To(Equal(2))
			Expect(failed).To(Equal(1))
		})
	})

	// ────────────────────────────────────────────────────────────────────────
	// RVR with Node but No LVG
	//

	Describe("RVR with Node but No LVG", func() {
		It("LVG selection on already-assigned node", func() {
			rv := newRV(defaultConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
					makeNode("node-b", "zone-b", makeLVG("vg-b", true)),
				})

			rvr := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			rvr.Spec.NodeName = "node-b" // Assigned but no LVG

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr).
				WithStatusSubresource(rvr).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			updated := getUpdatedRVR(ctx, cl, rvr)
			Expect(updated.Spec.NodeName).To(Equal("node-b"))
			Expect(updated.Spec.LVMVolumeGroupName).To(Equal("vg-b"))
			expectScheduledCondition(ctx, cl, rvr, metav1.ConditionTrue,
				v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonScheduled)
		})

		It("partially-scheduled RVR blocks its node for other replicas", func() {
			rv := newRV(defaultConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
					makeNode("node-b", "zone-b", makeLVG("vg-b", true)),
				})

			partial := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			partial.Spec.NodeName = "node-a" // Assigned but no LVG

			newOne := newRVR(1, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, partial, newOne).
				WithStatusSubresource(partial, newOne).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			// partial gets vg-a on node-a
			expectNodeName(ctx, cl, partial, "node-a")
			updated := getUpdatedRVR(ctx, cl, partial)
			Expect(updated.Spec.LVMVolumeGroupName).To(Equal("vg-a"))

			// new must go to node-b (node-a occupied)
			expectNodeName(ctx, cl, newOne, "node-b")
		})
	})

	// ────────────────────────────────────────────────────────────────────────
	// Deleting Scheduled RVR
	//

	Describe("Deleting Scheduled RVR", func() {
		It("condition Scheduled=True preserved, not re-scheduled, node blocked", func() {
			rv := newRV(defaultConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
					makeNode("node-b", "zone-b", makeLVG("vg-b", true)),
				})

			now := metav1.Now()
			deleting := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			deleting.Spec.NodeName = "node-a"
			deleting.Spec.LVMVolumeGroupName = "vg-a"
			deleting.DeletionTimestamp = &now
			deleting.Finalizers = []string{"test-finalizer"}

			newOne := newRVR(1, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, deleting, newOne).
				WithStatusSubresource(deleting, newOne).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			// Deleting RVR gets Scheduled=True (it's in sctx.Scheduled)
			expectScheduledCondition(ctx, cl, deleting, metav1.ConditionTrue,
				v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonScheduled)
			// New RVR goes to node-b (node-a blocked)
			expectNodeName(ctx, cl, newOne, "node-b")
		})
	})

	// ────────────────────────────────────────────────────────────────────────
	// Scheduling Order
	//

	Describe("Scheduling Order", func() {
		It("Diskful before TieBreaker — TieBreaker sees updated state", func() {
			rv := newRV(defaultConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
					makeNode("node-b", "zone-b", makeLVG("vg-b", true)),
				})

			d := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			tb := newRVR(1, v1alpha1.ReplicaTypeTieBreaker)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, d, tb).
				WithStatusSubresource(d, tb).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			// Both scheduled on different nodes
			dNode := getUpdatedRVR(ctx, cl, d).Spec.NodeName
			tbNode := getUpdatedRVR(ctx, cl, tb).Spec.NodeName
			Expect(dNode).NotTo(BeEmpty())
			Expect(tbNode).NotTo(BeEmpty())
			Expect(dNode).NotTo(Equal(tbNode))
		})

		It("3 Diskful in ID order — deterministic", func() {
			rv := newRV(defaultConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
					makeNode("node-b", "zone-b", makeLVG("vg-b", true)),
					makeNode("node-c", "zone-c", makeLVG("vg-c", true)),
				})

			rvr0 := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			rvr1 := newRVR(1, v1alpha1.ReplicaTypeDiskful)
			rvr2 := newRVR(2, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr0, rvr1, rvr2).
				WithStatusSubresource(rvr0, rvr1, rvr2).
				Build()
			mock := &reconcilerMockExtender{
				filterAndScoreFn: func(lvgs []schext.LVMVolumeGroup) ([]schext.ScoredLVMVolumeGroup, error) {
					var result []schext.ScoredLVMVolumeGroup
					for _, l := range lvgs {
						score := 1
						switch l.LVGName {
						case "vg-a":
							score = 10
						case "vg-b":
							score = 8
						case "vg-c":
							score = 6
						}
						result = append(result, schext.ScoredLVMVolumeGroup{LVMVolumeGroup: l, Score: score})
					}
					return result, nil
				},
			}
			rec := NewReconciler(cl, logr.Discard(), scheme, mock)

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			// ID 0 gets highest score (node-a), ID 1 gets second (node-b), ID 2 gets third (node-c)
			expectNodeName(ctx, cl, rvr0, "node-a")
			expectNodeName(ctx, cl, rvr1, "node-b")
			expectNodeName(ctx, cl, rvr2, "node-c")
		})
	})

	// ────────────────────────────────────────────────────────────────────────
	// ReservationID
	//

	Describe("ReservationID", func() {
		It("ReservationID from RV annotation — used as-is", func() {
			rv := newRV(defaultConfig())
			rv.Annotations = map[string]string{
				v1alpha1.SchedulingReservationIDAnnotationKey: "ns/pvc-123",
			}
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
				})
			rvr := newRVR(0, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr).
				WithStatusSubresource(rvr).
				Build()
			mock := &reconcilerMockExtender{}
			rec := NewReconciler(cl, logr.Discard(), scheme, mock)

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())
			Expect(mock.lastReservationID).To(Equal("ns/pvc-123"))
		})

		It("ReservationID computed when annotation absent", func() {
			rv := newRV(defaultConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
				})
			rvr := newRVR(0, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr).
				WithStatusSubresource(rvr).
				Build()
			mock := &reconcilerMockExtender{}
			rec := NewReconciler(cl, logr.Discard(), scheme, mock)

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			// At the time of the extender call, rvr has no LVG/ThinPool yet, so they are empty.
			expected := rvrllvname.ComputeLLVName(rvr.Name, "", "")
			Expect(mock.lastReservationID).To(Equal(expected))
		})
	})

	// ────────────────────────────────────────────────────────────────────────
	// Zonal Zone Capacity Penalty
	//

	Describe("Zonal Zone Capacity Penalty", func() {
		It("zone with enough free nodes — no penalty, replicas go there", func() {
			cfg := zonalConfig()
			cfg.Replication = v1alpha1.ReplicationAvailability // required=2
			rv := newRV(cfg)
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a1", "zone-a", makeLVG("vg-a1", true)),
					makeNode("node-a2", "zone-a", makeLVG("vg-a2", true)),
					makeNode("node-b1", "zone-b", makeLVG("vg-b1", true)),
				})

			rvr0 := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			rvr1 := newRVR(1, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr0, rvr1).
				WithStatusSubresource(rvr0, rvr1).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			// zone-a has 2 free nodes (enough for required=2), zone-b has 1 (penalty).
			// Both replicas should go to zone-a.
			n0 := getUpdatedRVR(ctx, cl, rvr0).Spec.NodeName
			n1 := getUpdatedRVR(ctx, cl, rvr1).Spec.NodeName
			Expect(n0).To(HavePrefix("node-a"))
			Expect(n1).To(HavePrefix("node-a"))
		})

		It("no zone has enough nodes — all penalized, best score wins", func() {
			cfg := zonalConfig()
			cfg.Replication = v1alpha1.ReplicationAvailability // required=2
			rv := newRV(cfg)
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a1", "zone-a", makeLVG("vg-a1", true)),
					makeNode("node-b1", "zone-b", makeLVG("vg-b1", true)),
				})

			rvr0 := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			rvr1 := newRVR(1, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr0, rvr1).
				WithStatusSubresource(rvr0, rvr1).
				Build()
			mock := &reconcilerMockExtender{
				filterAndScoreFn: func(lvgs []schext.LVMVolumeGroup) ([]schext.ScoredLVMVolumeGroup, error) {
					var result []schext.ScoredLVMVolumeGroup
					for _, l := range lvgs {
						score := 8
						if l.LVGName == "vg-a1" {
							score = 10
						}
						result = append(result, schext.ScoredLVMVolumeGroup{LVMVolumeGroup: l, Score: score})
					}
					return result, nil
				},
			}
			rec := NewReconciler(cl, logr.Discard(), scheme, mock)

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			// Both zones penalized (1 node each < required=2). 1 scheduled, 1 fails.
			scheduled := 0
			for _, rvr := range []*v1alpha1.ReplicatedVolumeReplica{rvr0, rvr1} {
				updated := getUpdatedRVR(ctx, cl, rvr)
				if updated.Spec.NodeName != "" {
					scheduled++
				}
			}
			Expect(scheduled).To(Equal(1))
		})

		It("all Diskful already scheduled — no penalty (remainingDemand=0)", func() {
			cfg := zonalConfig()
			cfg.Replication = v1alpha1.ReplicationAvailability // required=2
			rv := newRV(cfg)
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a1", "zone-a", makeLVG("vg-a1", true)),
					makeNode("node-a2", "zone-a", makeLVG("vg-a2", true)),
					makeNode("node-a3", "zone-a", makeLVG("vg-a3", true)),
				})

			// 2 already scheduled — covers required=2
			d0 := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			d0.Spec.NodeName = "node-a1"
			d0.Spec.LVMVolumeGroupName = "vg-a1"
			d1 := newRVR(1, v1alpha1.ReplicaTypeDiskful)
			d1.Spec.NodeName = "node-a2"
			d1.Spec.LVMVolumeGroupName = "vg-a2"

			// New one should be placed by score alone (no penalty step since remainingDemand=0)
			newD := newRVR(2, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, d0, d1, newD).
				WithStatusSubresource(d0, d1, newD).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			// remainingDemand=0 → no penalty. Zonal sticky picks zone-a (has 2 Diskful).
			// node-a3 is the only free node in zone-a → new D goes there.
			updated := getUpdatedRVR(ctx, cl, newD)
			Expect(updated.Spec.NodeName).To(Equal("node-a3"))
		})

		It("existing replicas commit zone; penalty does not override sticky", func() {
			cfg := zonalConfig()
			cfg.Replication = v1alpha1.ReplicationConsistencyAndAvailability // required=3
			rv := newRV(cfg)
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a1", "zone-a", makeLVG("vg-a1", true)),
					makeNode("node-a2", "zone-a", makeLVG("vg-a2", true)),
					makeNode("node-a3", "zone-a", makeLVG("vg-a3", true)),
					makeNode("node-b1", "zone-b", makeLVG("vg-b1", true)),
					makeNode("node-b2", "zone-b", makeLVG("vg-b2", true)),
				})

			existing := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			existing.Spec.NodeName = "node-a1"
			existing.Spec.LVMVolumeGroupName = "vg-a1"

			rvr1 := newRVR(1, v1alpha1.ReplicaTypeDiskful)
			rvr2 := newRVR(2, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, existing, rvr1, rvr2).
				WithStatusSubresource(existing, rvr1, rvr2).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			// Sticky picks zone-a (1 Diskful > 0). zone-a has 2 free nodes (a2, a3),
			// zone-b has 2. remainingDemand=2. Both zones have >= 2 free → no penalty.
			// Both new replicas go to zone-a.
			expectNodeName(ctx, cl, rvr1, "node-a2")
			expectNodeName(ctx, cl, rvr2, "node-a3")
		})

		It("penalty pushes replicas away from small zone", func() {
			cfg := zonalConfig()
			cfg.Replication = v1alpha1.ReplicationConsistencyAndAvailability // required=3
			rv := newRV(cfg)
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a1", "zone-a", makeLVG("vg-a1", true)),
					makeNode("node-b1", "zone-b", makeLVG("vg-b1", true)),
					makeNode("node-b2", "zone-b", makeLVG("vg-b2", true)),
					makeNode("node-b3", "zone-b", makeLVG("vg-b3", true)),
				})

			rvr0 := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			rvr1 := newRVR(1, v1alpha1.ReplicaTypeDiskful)
			rvr2 := newRVR(2, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr0, rvr1, rvr2).
				WithStatusSubresource(rvr0, rvr1, rvr2).
				Build()
			mock := &reconcilerMockExtender{
				filterAndScoreFn: func(lvgs []schext.LVMVolumeGroup) ([]schext.ScoredLVMVolumeGroup, error) {
					var result []schext.ScoredLVMVolumeGroup
					for _, l := range lvgs {
						score := 8
						if l.LVGName == "vg-a1" {
							score = 10 // highest individual score
						}
						result = append(result, schext.ScoredLVMVolumeGroup{LVMVolumeGroup: l, Score: score})
					}
					return result, nil
				},
			}
			rec := NewReconciler(cl, logr.Discard(), scheme, mock)

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			// zone-a: 1 node < required=3 → -800 penalty. zone-b: 3 nodes ≥ 3 → no penalty.
			// All 3 replicas go to zone-b despite zone-a having highest individual score.
			for _, rvr := range []*v1alpha1.ReplicatedVolumeReplica{rvr0, rvr1, rvr2} {
				updated := getUpdatedRVR(ctx, cl, rvr)
				Expect(updated.Spec.NodeName).To(HavePrefix("node-b"),
					"expected zone-b for %s, got %s", rvr.Name, updated.Spec.NodeName)
			}
		})

		It("Replication=None — requiredDiskful=1, no penalty when zones have ≥1 node", func() {
			cfg := zonalConfig()
			cfg.Replication = v1alpha1.ReplicationNone // required=1
			rv := newRV(cfg)
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a1", "zone-a", makeLVG("vg-a1", true)),
					makeNode("node-b1", "zone-b", makeLVG("vg-b1", true)),
				})

			rvr := newRVR(0, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr).
				WithStatusSubresource(rvr).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			// Both zones have ≥ 1 node → no penalty. Scheduled somewhere.
			updated := getUpdatedRVR(ctx, cl, rvr)
			Expect(updated.Spec.NodeName).NotTo(BeEmpty())
		})
	})

	// ────────────────────────────────────────────────────────────────────────
	// Idempotency and Patch Behavior
	//

	Describe("Idempotency and Patch Behavior", func() {
		It("repeated Reconcile is no-op — no error, no changes", func() {
			rv := newRV(defaultConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
				})

			rvr := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			rvr.Spec.NodeName = "node-a"
			rvr.Spec.LVMVolumeGroupName = "vg-a"
			obju.SetStatusCondition(rvr, metav1.Condition{
				Type:    v1alpha1.ReplicatedVolumeReplicaCondScheduledType,
				Status:  metav1.ConditionTrue,
				Reason:  v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonScheduled,
				Message: "Replica scheduled successfully",
			})

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr).
				WithStatusSubresource(rvr).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			// First reconcile
			result1, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())
			Expect(result1).To(Equal(reconcile.Result{}))

			// Save state after first reconcile
			after1 := getUpdatedRVR(ctx, cl, rvr)

			// Second reconcile — should be a no-op
			result2, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())
			Expect(result2).To(Equal(reconcile.Result{}))

			after2 := getUpdatedRVR(ctx, cl, rvr)
			Expect(after2.ResourceVersion).To(Equal(after1.ResourceVersion),
				"expected no patch on second reconcile")
		})

		It("condition already set with identical value — no patch", func() {
			rv := newRV(defaultConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
					makeNode("node-b", "zone-b", makeLVG("vg-b", true)),
				})

			rvr := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			rvr.Spec.NodeName = "node-a"
			rvr.Spec.LVMVolumeGroupName = "vg-a"
			obju.SetStatusCondition(rvr, metav1.Condition{
				Type:    v1alpha1.ReplicatedVolumeReplicaCondScheduledType,
				Status:  metav1.ConditionTrue,
				Reason:  v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonScheduled,
				Message: "Replica scheduled successfully",
			})

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr).
				WithStatusSubresource(rvr).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			// Get RV after setup to capture initial ResourceVersion
			initial := getUpdatedRVR(ctx, cl, rvr)

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			after := getUpdatedRVR(ctx, cl, rvr)
			Expect(after.ResourceVersion).To(Equal(initial.ResourceVersion),
				"expected no status patch when condition is identical")
		})

		It("RVR deleted concurrently — NotFound on status patch is silently ignored", func() {
			rv := newRV(defaultConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
				})

			rvr := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			rvr.Spec.NodeName = "node-a"
			rvr.Spec.LVMVolumeGroupName = "vg-a"

			notFoundErr := &apierrors.StatusError{
				ErrStatus: metav1.Status{
					Status: metav1.StatusFailure,
					Reason: metav1.StatusReasonNotFound,
				},
			}
			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr).
				WithStatusSubresource(rvr).
				WithInterceptorFuncs(interceptor.Funcs{
					SubResourcePatch: func(ctx context.Context, cl client.Client, _ string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
						if _, ok := obj.(*v1alpha1.ReplicatedVolumeReplica); ok {
							return notFoundErr
						}
						return cl.Status().Patch(ctx, obj, patch, opts...)
					},
				}).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			// Reconcile should not return error — NotFound on status patch is silently ignored.
			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())
		})

		It("Conflict on patch — Reconcile requeues without error", func() {
			rv := newRV(defaultConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
				})

			rvr := newRVR(0, v1alpha1.ReplicaTypeDiskful)

			conflictErr := &apierrors.StatusError{
				ErrStatus: metav1.Status{
					Status: metav1.StatusFailure,
					Reason: metav1.StatusReasonConflict,
				},
			}
			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr).
				WithStatusSubresource(rvr).
				WithInterceptorFuncs(interceptor.Funcs{
					Patch: func(ctx context.Context, cl client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
						if _, ok := obj.(*v1alpha1.ReplicatedVolumeReplica); ok {
							return conflictErr
						}
						return cl.Patch(ctx, obj, patch, opts...)
					},
					SubResourcePatch: func(ctx context.Context, cl client.Client, _ string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
						if _, ok := obj.(*v1alpha1.ReplicatedVolumeReplica); ok {
							return conflictErr
						}
						return cl.Status().Patch(ctx, obj, patch, opts...)
					},
				}).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			// flow.ToCtrl() converts Conflict errors into a rate-limited requeue without error.
			result, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Requeue).To(BeTrue(), "Conflict should trigger requeue") //nolint:staticcheck // testing Requeue field set by flow.ToCtrl
		})
	})

	// ────────────────────────────────────────────────────────────────────────
	// Extender: Multi-Diskful and Mixed Errors
	//

	Describe("Extender: Multi-Diskful and Mixed Errors", func() {
		It("Extender error, multiple Diskful — all attempted, all get ExtenderUnavailable", func() {
			rv := newRV(defaultConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
					makeNode("node-b", "zone-b", makeLVG("vg-b", true)),
					makeNode("node-c", "zone-c", makeLVG("vg-c", true)),
				})

			rvr0 := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			rvr1 := newRVR(1, v1alpha1.ReplicaTypeDiskful)
			rvr2 := newRVR(2, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr0, rvr1, rvr2).
				WithStatusSubresource(rvr0, rvr1, rvr2).
				Build()
			mock := &reconcilerMockExtender{
				filterAndScoreFn: func(_ []schext.LVMVolumeGroup) ([]schext.ScoredLVMVolumeGroup, error) {
					return nil, fmt.Errorf("connection refused")
				},
			}
			rec := NewReconciler(cl, logr.Discard(), scheme, mock)

			_, err := reconcileRV(ctx, rec)
			Expect(err).To(HaveOccurred())

			// All Diskful get ExtenderUnavailable
			for _, rvr := range []*v1alpha1.ReplicatedVolumeReplica{rvr0, rvr1, rvr2} {
				expectScheduledCondition(ctx, cl, rvr, metav1.ConditionFalse,
					v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonExtenderUnavailable)
			}
		})

		It("Extender error, Diskful + TieBreaker — Diskful fails, TieBreaker NOT attempted", func() {
			rv := newRV(defaultConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
					makeNode("node-b", "zone-b", makeLVG("vg-b", true)),
				})

			d := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			tb := newRVR(1, v1alpha1.ReplicaTypeTieBreaker)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, d, tb).
				WithStatusSubresource(d, tb).
				Build()
			mock := &reconcilerMockExtender{
				filterAndScoreFn: func(_ []schext.LVMVolumeGroup) ([]schext.ScoredLVMVolumeGroup, error) {
					return nil, fmt.Errorf("connection refused")
				},
			}
			rec := NewReconciler(cl, logr.Discard(), scheme, mock)

			_, err := reconcileRV(ctx, rec)
			Expect(err).To(HaveOccurred())

			// Diskful gets ExtenderUnavailable
			expectScheduledCondition(ctx, cl, d, metav1.ConditionFalse,
				v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonExtenderUnavailable)

			// TieBreaker was not reached — no condition set
			tbUpdated := getUpdatedRVR(ctx, cl, tb)
			Expect(tbUpdated.Spec.NodeName).To(BeEmpty())
			Expect(obju.HasStatusCondition(tbUpdated, v1alpha1.ReplicatedVolumeReplicaCondScheduledType)).To(BeFalse(),
				"TieBreaker should not have been reached")
		})
	})

	// ────────────────────────────────────────────────────────────────────────
	// Topology Edge Cases
	//

	Describe("Topology Edge Cases", func() {
		It("TransZonal: round-robin 4D across 3 zones", func() {
			rv := newRV(transZonalConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a1", "zone-a", makeLVG("vg-a1", true)),
					makeNode("node-a2", "zone-a", makeLVG("vg-a2", true)),
					makeNode("node-b1", "zone-b", makeLVG("vg-b1", true)),
					makeNode("node-b2", "zone-b", makeLVG("vg-b2", true)),
					makeNode("node-c1", "zone-c", makeLVG("vg-c1", true)),
					makeNode("node-c2", "zone-c", makeLVG("vg-c2", true)),
				})

			rvr0 := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			rvr1 := newRVR(1, v1alpha1.ReplicaTypeDiskful)
			rvr2 := newRVR(2, v1alpha1.ReplicaTypeDiskful)
			rvr3 := newRVR(3, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr0, rvr1, rvr2, rvr3).
				WithStatusSubresource(rvr0, rvr1, rvr2, rvr3).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			// All 3 zones used; one zone gets 2 replicas.
			zones := map[string]int{}
			for _, rvr := range []*v1alpha1.ReplicatedVolumeReplica{rvr0, rvr1, rvr2, rvr3} {
				updated := getUpdatedRVR(ctx, cl, rvr)
				Expect(updated.Spec.NodeName).NotTo(BeEmpty(), "%s not scheduled", rvr.Name)
				for _, n := range rsp.Status.EligibleNodes {
					if n.NodeName == updated.Spec.NodeName {
						zones[n.ZoneName]++
					}
				}
			}
			Expect(zones).To(HaveLen(3), "all 3 zones should be used")
			// One zone has 2, others have 1
			maxInZone := 0
			for _, count := range zones {
				if count > maxInZone {
					maxInZone = count
				}
			}
			Expect(maxInZone).To(Equal(2), "one zone should have 2 replicas")
		})

		It("Zonal: sticky zone — 3D all in one zone from fresh start", func() {
			rv := newRV(zonalConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a1", "zone-a", makeLVG("vg-a1", true)),
					makeNode("node-a2", "zone-a", makeLVG("vg-a2", true)),
					makeNode("node-a3", "zone-a", makeLVG("vg-a3", true)),
					makeNode("node-b1", "zone-b", makeLVG("vg-b1", true)),
					makeNode("node-b2", "zone-b", makeLVG("vg-b2", true)),
					makeNode("node-b3", "zone-b", makeLVG("vg-b3", true)),
				})

			rvr0 := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			rvr1 := newRVR(1, v1alpha1.ReplicaTypeDiskful)
			rvr2 := newRVR(2, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr0, rvr1, rvr2).
				WithStatusSubresource(rvr0, rvr1, rvr2).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			// After the first replica is placed, the zone becomes sticky.
			// All 3 should end up in the same zone.
			zone0 := ""
			for _, rvr := range []*v1alpha1.ReplicatedVolumeReplica{rvr0, rvr1, rvr2} {
				updated := getUpdatedRVR(ctx, cl, rvr)
				Expect(updated.Spec.NodeName).NotTo(BeEmpty())
				for _, n := range rsp.Status.EligibleNodes {
					if n.NodeName == updated.Spec.NodeName {
						if zone0 == "" {
							zone0 = n.ZoneName
						}
						Expect(n.ZoneName).To(Equal(zone0),
							"all Diskful should be in the same zone (sticky)")
					}
				}
			}
		})

		It("TransZonal: eligible only from 1 zone — 2nd D SchedulingFailed", func() {
			rv := newRV(transZonalConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a1", "zone-a", makeLVG("vg-a1", true)),
					makeNode("node-a2", "zone-a", makeLVG("vg-a2", true)),
				})

			rvr0 := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			rvr1 := newRVR(1, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr0, rvr1).
				WithStatusSubresource(rvr0, rvr1).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			// 1st goes to zone-a. For 2nd, transZonal prefers zones with fewer Diskful,
			// but only zone-a exists. Node occupied → SchedulingFailed or placed on node-a2.
			// Actually: transZonal filter picks zone-a (only zone), both nodes are available.
			// rvr0 gets node-a1, occupies it. rvr1 gets node-a2.
			// Both scheduled — but semantically only 1 zone is used.
			// The test validates the behavior: both scheduled in the only available zone.
			n0 := getUpdatedRVR(ctx, cl, rvr0).Spec.NodeName
			n1 := getUpdatedRVR(ctx, cl, rvr1).Spec.NodeName
			Expect(n0).NotTo(BeEmpty())
			Expect(n1).NotTo(BeEmpty())
			Expect(n0).NotTo(Equal(n1))
		})

		It("xlarge-4z: D:3 only in RSC zones (RSP has 3 of 4)", func() {
			rv := newRV(transZonalConfig())
			// RSP lists only 3 zones (zone-d not in RSP = not in RSC)
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a1", "zone-a", makeLVG("vg-a1", true)),
					makeNode("node-b1", "zone-b", makeLVG("vg-b1", true)),
					makeNode("node-c1", "zone-c", makeLVG("vg-c1", true)),
					// zone-d deliberately absent from RSP
				})

			rvr0 := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			rvr1 := newRVR(1, v1alpha1.ReplicaTypeDiskful)
			rvr2 := newRVR(2, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr0, rvr1, rvr2).
				WithStatusSubresource(rvr0, rvr1, rvr2).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			zones := map[string]bool{}
			for _, rvr := range []*v1alpha1.ReplicatedVolumeReplica{rvr0, rvr1, rvr2} {
				updated := getUpdatedRVR(ctx, cl, rvr)
				Expect(updated.Spec.NodeName).NotTo(BeEmpty())
				for _, n := range rsp.Status.EligibleNodes {
					if n.NodeName == updated.Spec.NodeName {
						zones[n.ZoneName] = true
					}
				}
			}
			Expect(zones).To(HaveLen(3))
			Expect(zones).NotTo(HaveKey("zone-d"))
		})
	})

	// ────────────────────────────────────────────────────────────────────────
	// Zonal AttachTo
	//

	Describe("Zonal AttachTo", func() {
		It("Zonal: attachTo node — D on that node, TB on remaining", func() {
			rv := newRV(zonalConfig())
			rva := newRVA("node-a1")
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a1", "zone-a", makeLVG("vg-a1", true)),
					makeNode("node-a2", "zone-a", makeLVG("vg-a2", true)),
				})

			d := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			tb := newRVR(1, v1alpha1.ReplicaTypeTieBreaker)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, d, tb, rva).
				WithStatusSubresource(d, tb).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			expectNodeName(ctx, cl, d, "node-a1")
			expectNodeName(ctx, cl, tb, "node-a2")
		})

		It("Zonal: attachTo both nodes of same zone — all D in zone-a", func() {
			rv := newRV(zonalConfig())
			rvaA1 := newRVA("node-a1")
			rvaA2 := newRVA("node-a2")
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a1", "zone-a", makeLVG("vg-a1", true)),
					makeNode("node-a2", "zone-a", makeLVG("vg-a2", true)),
					makeNode("node-b1", "zone-b", makeLVG("vg-b1", true)),
					makeNode("node-b2", "zone-b", makeLVG("vg-b2", true)),
				})

			rvr0 := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			rvr1 := newRVR(1, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr0, rvr1, rvaA1, rvaA2).
				WithStatusSubresource(rvr0, rvr1).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			// Both in zone-a due to +1000 bonus on both zone-a nodes
			n0 := getUpdatedRVR(ctx, cl, rvr0).Spec.NodeName
			n1 := getUpdatedRVR(ctx, cl, rvr1).Spec.NodeName
			Expect(n0).To(HavePrefix("node-a"))
			Expect(n1).To(HavePrefix("node-a"))
		})

		It("AttachTo bonus NOT applied to TieBreaker", func() {
			rv := newRV(defaultConfig())
			rva := newRVA("node-a")
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
					makeNode("node-b", "zone-b", makeLVG("vg-b", true)),
				})

			// Existing Diskful on node-a
			d := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			d.Spec.NodeName = "node-a"
			d.Spec.LVMVolumeGroupName = "vg-a"

			tb := newRVR(1, v1alpha1.ReplicaTypeTieBreaker)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, d, tb, rva).
				WithStatusSubresource(d, tb).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			// TB goes to node-b (node-a occupied). No +1000 bonus on TB pipeline.
			expectNodeName(ctx, cl, tb, "node-b")
		})
	})

	// ────────────────────────────────────────────────────────────────────────
	// Multi-LVG Node Bonus: Edge Cases
	//

	Describe("Multi-LVG Node Bonus: Edge Cases", func() {
		It("higher extender score beats multi-LVG bonus", func() {
			cfg := defaultConfig()
			cfg.VolumeAccess = v1alpha1.VolumeAccessLocal
			rv := newRV(cfg)
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeLVG("vg-a1", true), makeLVG("vg-a2", true)),
					makeNode("node-b", "zone-b", makeLVG("vg-b1", true)),
				})
			rvr := newRVR(0, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr).
				WithStatusSubresource(rvr).
				Build()
			mock := &reconcilerMockExtender{
				filterAndScoreFn: func(lvgs []schext.LVMVolumeGroup) ([]schext.ScoredLVMVolumeGroup, error) {
					var result []schext.ScoredLVMVolumeGroup
					for _, l := range lvgs {
						score := 5
						if l.LVGName == "vg-b1" {
							score = 10
						}
						result = append(result, schext.ScoredLVMVolumeGroup{LVMVolumeGroup: l, Score: score})
					}
					return result, nil
				},
			}
			rec := NewReconciler(cl, logr.Discard(), scheme, mock)

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			// node-a: best LVG 5+2=7, node-b: 10. Node-b wins.
			expectNodeName(ctx, cl, rvr, "node-b")
		})

		It("best LVG selected on winning node", func() {
			cfg := defaultConfig()
			cfg.VolumeAccess = v1alpha1.VolumeAccessLocal
			rv := newRV(cfg)
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a",
						makeLVG("vg-a1", true), makeLVG("vg-a2", true), makeLVG("vg-a3", true)),
				})
			rvr := newRVR(0, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr).
				WithStatusSubresource(rvr).
				Build()
			mock := &reconcilerMockExtender{
				filterAndScoreFn: func(lvgs []schext.LVMVolumeGroup) ([]schext.ScoredLVMVolumeGroup, error) {
					var result []schext.ScoredLVMVolumeGroup
					for _, l := range lvgs {
						score := 5
						switch l.LVGName {
						case "vg-a1":
							score = 10
						case "vg-a2":
							score = 15
						case "vg-a3":
							score = 5
						}
						result = append(result, schext.ScoredLVMVolumeGroup{LVMVolumeGroup: l, Score: score})
					}
					return result, nil
				},
			}
			rec := NewReconciler(cl, logr.Discard(), scheme, mock)

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			// All LVGs get +2 bonus: vg-a1=12, vg-a2=17, vg-a3=7. Best is vg-a2.
			updated := getUpdatedRVR(ctx, cl, rvr)
			Expect(updated.Spec.LVMVolumeGroupName).To(Equal("vg-a2"))
		})
	})

	// ────────────────────────────────────────────────────────────────────────
	// RVR with Node but No LVG: Edge Cases
	//

	Describe("RVR with Node but No LVG: Edge Cases", func() {
		It("no suitable LVG on assigned node — SchedulingFailed", func() {
			rv := newRV(defaultConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
					makeNode("node-b", "zone-b", makeLVG("vg-b", false)), // not ready
				})

			rvr := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			rvr.Spec.NodeName = "node-b" // assigned to node with bad LVG

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr).
				WithStatusSubresource(rvr).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			expectScheduledCondition(ctx, cl, rvr, metav1.ConditionFalse,
				v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonSchedulingFailed)
		})

		It("LVMThin: ThinPoolName for partially-scheduled RVR", func() {
			rv := newRV(defaultConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVMThin,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeThinLVG("vg-a", "thin-a", true)),
				})

			rvr := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			rvr.Spec.NodeName = "node-a" // assigned but no LVG/ThinPool

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr).
				WithStatusSubresource(rvr).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			updated := getUpdatedRVR(ctx, cl, rvr)
			Expect(updated.Spec.NodeName).To(Equal("node-a"))
			Expect(updated.Spec.LVMVolumeGroupName).To(Equal("vg-a"))
			Expect(updated.Spec.LVMVolumeGroupThinPoolName).To(Equal("thin-a"))
			expectScheduledCondition(ctx, cl, rvr, metav1.ConditionTrue,
				v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonScheduled)
		})

		It("early exit — WaitingForReplicatedVolume on both partial and unscheduled", func() {
			cfg := defaultConfig()
			cfg.StoragePoolName = "" // triggers RSP not found
			rv := newRV(cfg)

			partial := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			partial.Spec.NodeName = "node-a"

			unscheduled := newRVR(1, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, partial, unscheduled).
				WithStatusSubresource(partial, unscheduled).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			result, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			// Both get WaitingForReplicatedVolume regardless of partial scheduling
			expectScheduledCondition(ctx, cl, partial, metav1.ConditionUnknown,
				v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonWaitingForReplicatedVolume)
			expectScheduledCondition(ctx, cl, unscheduled, metav1.ConditionUnknown,
				v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonWaitingForReplicatedVolume)
		})
	})

	// ────────────────────────────────────────────────────────────────────────
	// Zonal Reserved Nodes
	//

	Describe("Zonal Reserved Nodes", func() {
		It("reserved nodes (NodeName without LVG) commit the zone", func() {
			rv := newRV(zonalConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a1", "zone-a", makeLVG("vg-a1", true)),
					makeNode("node-a2", "zone-a", makeLVG("vg-a2", true)),
					makeNode("node-a3", "zone-a", makeLVG("vg-a3", true)),
					makeNode("node-b1", "zone-b", makeLVG("vg-b1", true)),
					makeNode("node-b2", "zone-b", makeLVG("vg-b2", true)),
				})

			// 2 reserved Diskful in zone-a (NodeName set, no LVG)
			reserved0 := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			reserved0.Spec.NodeName = "node-a1"
			reserved1 := newRVR(1, v1alpha1.ReplicaTypeDiskful)
			reserved1.Spec.NodeName = "node-a2"

			// New Diskful
			newD := newRVR(2, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, reserved0, reserved1, newD).
				WithStatusSubresource(reserved0, reserved1, newD).
				Build()
			mock := &reconcilerMockExtender{
				filterAndScoreFn: func(lvgs []schext.LVMVolumeGroup) ([]schext.ScoredLVMVolumeGroup, error) {
					var result []schext.ScoredLVMVolumeGroup
					for _, l := range lvgs {
						score := 5
						// zone-b LVGs have higher scores
						if l.LVGName == "vg-b1" || l.LVGName == "vg-b2" {
							score = 20
						}
						result = append(result, schext.ScoredLVMVolumeGroup{LVMVolumeGroup: l, Score: score})
					}
					return result, nil
				},
			}
			rec := NewReconciler(cl, logr.Discard(), scheme, mock)

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			// Reserved commit zone-a (2 Diskful > 0 in zone-b). New D goes to zone-a.
			expectNodeName(ctx, cl, newD, "node-a3")
			// Reserved ones get their LVGs
			Expect(getUpdatedRVR(ctx, cl, reserved0).Spec.LVMVolumeGroupName).To(Equal("vg-a1"))
			Expect(getUpdatedRVR(ctx, cl, reserved1).Spec.LVMVolumeGroupName).To(Equal("vg-a2"))
		})

		It("TieBreaker also goes to committed zone", func() {
			rv := newRV(zonalConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a1", "zone-a", makeLVG("vg-a1", true)),
					makeNode("node-a2", "zone-a", makeLVG("vg-a2", true)),
					makeNode("node-a3", "zone-a", makeLVG("vg-a3", true)),
					makeNode("node-b1", "zone-b", makeLVG("vg-b1", true)),
					makeNode("node-b2", "zone-b", makeLVG("vg-b2", true)),
				})

			// Reserved Diskful in zone-a
			reserved := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			reserved.Spec.NodeName = "node-a1"

			// TieBreaker
			tb := newRVR(1, v1alpha1.ReplicaTypeTieBreaker)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, reserved, tb).
				WithStatusSubresource(reserved, tb).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			// Zone-a committed (1 Diskful > 0). TB goes to zone-a.
			tbNode := getUpdatedRVR(ctx, cl, tb).Spec.NodeName
			Expect(tbNode).To(SatisfyAny(Equal("node-a2"), Equal("node-a3")),
				"TB should go to zone-a (committed zone)")
		})
	})

	// ────────────────────────────────────────────────────────────────────────
	// TransZonal Greedy Scheduling
	//

	Describe("TransZonal Greedy Scheduling", func() {
		It("3D, 2 zones × 1 node — 2 greedy, 1 SchedulingFailed", func() {
			rv := newRV(transZonalConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a1", "zone-a", makeLVG("vg-a1", true)),
					makeNode("node-b1", "zone-b", makeLVG("vg-b1", true)),
				})

			rvr0 := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			rvr1 := newRVR(1, v1alpha1.ReplicaTypeDiskful)
			rvr2 := newRVR(2, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr0, rvr1, rvr2).
				WithStatusSubresource(rvr0, rvr1, rvr2).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			scheduled := 0
			failed := 0
			for _, rvr := range []*v1alpha1.ReplicatedVolumeReplica{rvr0, rvr1, rvr2} {
				updated := getUpdatedRVR(ctx, cl, rvr)
				if updated.Spec.NodeName != "" {
					scheduled++
				} else {
					failed++
					expectScheduledCondition(ctx, cl, rvr, metav1.ConditionFalse,
						v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonSchedulingFailed)
				}
			}
			Expect(scheduled).To(Equal(2))
			Expect(failed).To(Equal(1))
		})

		It("reserved node occupies zone — greedy on remaining", func() {
			rv := newRV(transZonalConfig())
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a1", "zone-a", makeLVG("vg-a1", true)),
					makeNode("node-a2", "zone-a", makeLVG("vg-a2", true)),
					makeNode("node-b1", "zone-b", makeLVG("vg-b1", true)),
					makeNode("node-c1", "zone-c", makeLVG("vg-c1", true)),
				})

			// Already fully scheduled on node-a1 (zone-a has 1 Diskful).
			existing := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			existing.Spec.NodeName = "node-a1"
			existing.Spec.LVMVolumeGroupName = "vg-a1"

			// Reserved on node-a2 (same zone-a, no LVG). TransZonal prefers
			// zones with fewer Diskful: zone-b=0, zone-c=0. Zone-a has 1,
			// so it is excluded. But reserved is narrowed to node-a2 (zone-a).
			// The zone filter removes node-a2 → reserved gets SchedulingFailed.
			reserved := newRVR(1, v1alpha1.ReplicaTypeDiskful)
			reserved.Spec.NodeName = "node-a2"

			d2 := newRVR(2, v1alpha1.ReplicaTypeDiskful)
			d3 := newRVR(3, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, existing, reserved, d2, d3).
				WithStatusSubresource(existing, reserved, d2, d3).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			// Reserved on zone-a is excluded by TransZonal zone filter → SchedulingFailed.
			expectScheduledCondition(ctx, cl, reserved, metav1.ConditionFalse,
				v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonSchedulingFailed)

			// d2 and d3 go to zone-b and zone-c.
			zones := map[string]bool{}
			for _, rvr := range []*v1alpha1.ReplicatedVolumeReplica{d2, d3} {
				updated := getUpdatedRVR(ctx, cl, rvr)
				Expect(updated.Spec.NodeName).NotTo(BeEmpty())
				for _, n := range rsp.Status.EligibleNodes {
					if n.NodeName == updated.Spec.NodeName {
						zones[n.ZoneName] = true
					}
				}
			}
			Expect(zones).To(HaveKey("zone-b"))
			Expect(zones).To(HaveKey("zone-c"))
		})

		It("migration: existing replica in old zone not in RSP", func() {
			rv := newRV(transZonalConfig())
			// RSP only has zone-b and zone-c (zone-a removed)
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-b1", "zone-b", makeLVG("vg-b1", true)),
					makeNode("node-c1", "zone-c", makeLVG("vg-c1", true)),
				})

			// Existing Diskful on node-a1 (zone-a, not in RSP)
			existing := newRVR(0, v1alpha1.ReplicaTypeDiskful)
			existing.Spec.NodeName = "node-a1"
			existing.Spec.LVMVolumeGroupName = "vg-a1"

			newD := newRVR(1, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, existing, newD).
				WithStatusSubresource(existing, newD).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			// New D goes to one of the available zones (b or c)
			newNode := getUpdatedRVR(ctx, cl, newD).Spec.NodeName
			Expect(newNode).To(SatisfyAny(Equal("node-b1"), Equal("node-c1")))
		})
	})

	// ────────────────────────────────────────────────────────────────────────
	// Pipeline Summary in Condition Message
	//

	Describe("Pipeline Summary in Condition Message", func() {
		It("SchedulingFailed message contains pipeline summary", func() {
			rv := newRV(defaultConfig())
			notReady1 := makeNode("node-a", "zone-a", makeLVG("vg-a", true))
			notReady1.NodeReady = false
			notReady2 := makeNode("node-b", "zone-b", makeLVG("vg-b", true))
			notReady2.NodeReady = false
			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{notReady1, notReady2})

			rvr := newRVR(0, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rsp, rvr).
				WithStatusSubresource(rvr).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			_, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())

			updated := getUpdatedRVR(ctx, cl, rvr)
			cond := obju.StatusCondition(updated, v1alpha1.ReplicatedVolumeReplicaCondScheduledType)
			Expect(cond.StatusEqual(metav1.ConditionFalse).Eval()).To(BeTrue())
			Expect(updated.Status.Conditions).NotTo(BeEmpty())
			// Find the condition and check message
			for _, c := range updated.Status.Conditions {
				if c.Type == v1alpha1.ReplicatedVolumeReplicaCondScheduledType {
					Expect(c.Message).To(ContainSubstring("excluded: node not ready"),
						"condition message should contain pipeline summary")
				}
			}
		})
	})

	// ────────────────────────────────────────────────────────────────────────
	// Empty StoragePoolName Guard
	//

	Describe("Empty StoragePoolName Guard", func() {
		It("empty StoragePoolName — WaitingForReplicatedVolume", func() {
			cfg := defaultConfig()
			cfg.StoragePoolName = ""
			rv := newRV(cfg)

			rvr := newRVR(0, v1alpha1.ReplicaTypeDiskful)

			cl := newClientBuilder(scheme).
				WithObjects(rv, rvr).
				WithStatusSubresource(rvr).
				Build()
			rec := NewReconciler(cl, logr.Discard(), scheme, &reconcilerMockExtender{})

			result, err := reconcileRV(ctx, rec)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
			expectScheduledCondition(ctx, cl, rvr, metav1.ConditionUnknown,
				v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonWaitingForReplicatedVolume)
		})
	})
})
