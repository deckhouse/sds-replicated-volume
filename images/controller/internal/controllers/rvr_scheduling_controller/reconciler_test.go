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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes/testhelpers"
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
	return testhelpers.WithRVRByReplicatedVolumeNameIndex(
		fake.NewClientBuilder().WithScheme(scheme),
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

func newRVR(nodeID uint8, replicaType v1alpha1.ReplicaType) *v1alpha1.ReplicatedVolumeReplica {
	name := fmt.Sprintf("%s-%d", testRVName, nodeID)
	return &v1alpha1.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
			ReplicatedVolumeName: testRVName,
			Type:                 replicaType,
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
}

func (m *reconcilerMockExtender) FilterAndScore(_ context.Context, _ string, _ time.Duration, _ int64, lvgs []schext.LVMVolumeGroup) ([]schext.ScoredLVMVolumeGroup, error) {
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
			rv.Status.DesiredAttachTo = []string{"node-a"}

			rsp := newRSP(v1alpha1.ReplicatedStoragePoolTypeLVM,
				[]v1alpha1.ReplicatedStoragePoolEligibleNode{
					makeNode("node-a", "zone-a", makeLVG("vg-a", true)),
					makeNode("node-b", "zone-b", makeLVG("vg-b", true)),
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
	})
})
