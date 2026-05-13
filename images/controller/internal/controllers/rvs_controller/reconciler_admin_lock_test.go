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

package rvscontroller

import (
	"context"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func contains(s, substr string) bool { return strings.Contains(s, substr) }

func adminLockTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}
	return scheme
}

func adminLockNewClient(scheme *runtime.Scheme, objs ...client.Object) client.Client {
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.ReplicatedVolumeSnapshot{}).
		WithStatusSubresource(&v1alpha1.DRBDResourceOperation{}).
		WithObjects(objs...).
		Build()
}

func adminLockMakeRVS(name string) *v1alpha1.ReplicatedVolumeSnapshot {
	return &v1alpha1.ReplicatedVolumeSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			UID:  types.UID("rvs-uid-" + name),
		},
		Spec: v1alpha1.ReplicatedVolumeSnapshotSpec{
			ReplicatedVolumeName: "rv-1",
		},
	}
}

func adminLockMakeRV(members ...v1alpha1.DatameshMember) *v1alpha1.ReplicatedVolume {
	return &v1alpha1.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
		Status: v1alpha1.ReplicatedVolumeStatus{
			Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
				Members: members,
			},
		},
	}
}

func adminLockMakeUpDRBDR(name string) *v1alpha1.DRBDResource {
	return &v1alpha1.DRBDResource{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.DRBDResourceSpec{
			State: v1alpha1.DRBDResourceStateUp,
		},
		Status: v1alpha1.DRBDResourceStatus{
			DiskState: v1alpha1.DiskStateUpToDate,
		},
	}
}

func adminLockMakeReadyDRBDR(name string, peers ...v1alpha1.DRBDResourcePeerStatus) *v1alpha1.DRBDResource {
	dr := adminLockMakeUpDRBDR(name)
	dr.Status.Peers = peers
	return dr
}

func adminLockReadyPeer(name string, nodeID uint) v1alpha1.DRBDResourcePeerStatus {
	return v1alpha1.DRBDResourcePeerStatus{
		Name:             name,
		NodeID:           nodeID,
		ConnectionState:  v1alpha1.ConnectionStateConnected,
		ReplicationState: v1alpha1.ReplicationStateEstablished,
		DiskState:        v1alpha1.DiskStateUpToDate,
		Type:             v1alpha1.DRBDResourceTypeDiskful,
	}
}

func TestComputeTargetAdminLockHolder(t *testing.T) {
	rvr0 := mkRVRWithName("rvr-0", "node-a", v1alpha1.ReplicaTypeDiskful)
	rvr1 := mkRVRWithName("rvr-1", "node-b", v1alpha1.ReplicaTypeDiskful)
	rvrTie := mkRVRWithName("rvr-tie", "node-c", v1alpha1.ReplicaTypeTieBreaker)

	tests := []struct {
		name    string
		rv      *v1alpha1.ReplicatedVolume
		rvrs    []*v1alpha1.ReplicatedVolumeReplica
		wantNil bool
		want    string
	}{
		{
			name:    "nil RV returns nil",
			rv:      nil,
			rvrs:    []*v1alpha1.ReplicatedVolumeReplica{rvr0},
			wantNil: true,
		},
		{
			name: "prefers attached primary",
			rv: adminLockMakeRV(
				mkMember("rvr-0", false),
				mkMember("rvr-1", true),
			),
			rvrs: []*v1alpha1.ReplicatedVolumeReplica{rvr0, rvr1},
			want: "rvr-1",
		},
		{
			name: "fallback to first diskful when none attached",
			rv: adminLockMakeRV(
				mkMember("rvr-0", false),
				mkMember("rvr-1", false),
			),
			rvrs: []*v1alpha1.ReplicatedVolumeReplica{rvr0, rvr1},
			want: "rvr-0",
		},
		{
			name: "skips non-diskful tiebreaker",
			rv: adminLockMakeRV(
				mkMember("rvr-tie", true),
				mkMember("rvr-1", false),
			),
			rvrs: []*v1alpha1.ReplicatedVolumeReplica{rvrTie, rvr1},
			want: "rvr-1",
		},
		{
			name: "no diskful member returns nil",
			rv: adminLockMakeRV(
				mkMember("rvr-tie", true),
			),
			rvrs:    []*v1alpha1.ReplicatedVolumeReplica{rvrTie},
			wantNil: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := computeTargetAdminLockHolder(tc.rv, tc.rvrs)
			if tc.wantNil {
				if got != nil {
					t.Fatalf("computeTargetAdminLockHolder() = %q, want nil", got.Name)
				}
				return
			}
			if got == nil {
				t.Fatalf("computeTargetAdminLockHolder() = nil, want %q", tc.want)
			}
			if got.Name != tc.want {
				t.Fatalf("computeTargetAdminLockHolder() = %q, want %q", got.Name, tc.want)
			}
		})
	}
}

func TestReconcileAcquireAdminLockNoHolderRequeues(t *testing.T) {
	scheme := adminLockTestScheme(t)
	rvs := adminLockMakeRVS("snap-1")
	rv := adminLockMakeRV() // no members

	r := &Reconciler{
		cl:     adminLockNewClient(scheme, rvs, rv),
		scheme: scheme,
	}

	outcome := r.reconcileAcquireAdminLock(context.Background(), rvs, rv, nil)
	if !outcome.ShouldReturn() {
		t.Fatalf("expected requeue outcome when no holder, got Continue")
	}
	if outcome.Error() != nil {
		t.Fatalf("unexpected error: %v", outcome.Error())
	}

	// No DRBDOp must be created.
	op := &v1alpha1.DRBDResourceOperation{}
	err := r.cl.Get(context.Background(), client.ObjectKey{Name: adminLockOpName(rvs)}, op)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected NotFound for op, got: %v", err)
	}
}

func TestReconcileAcquireAdminLockWaitsForHolderDRBDR(t *testing.T) {
	scheme := adminLockTestScheme(t)
	rvs := adminLockMakeRVS("snap-1")
	rv := adminLockMakeRV(mkMember("rvr-0", true))
	rvr := mkRVRWithName("rvr-0", "node-a", v1alpha1.ReplicaTypeDiskful)

	r := &Reconciler{
		cl:     adminLockNewClient(scheme, rvs, rv),
		scheme: scheme,
	}

	outcome := r.reconcileAcquireAdminLock(context.Background(), rvs, rv, []*v1alpha1.ReplicatedVolumeReplica{rvr})
	if !outcome.ShouldReturn() {
		t.Fatalf("expected requeue while holder DRBDResource missing")
	}

	op := &v1alpha1.DRBDResourceOperation{}
	if err := r.cl.Get(context.Background(), client.ObjectKey{Name: adminLockOpName(rvs)}, op); !apierrors.IsNotFound(err) {
		t.Fatalf("expected NotFound for op, got: %v", err)
	}
}

func TestReconcileAcquireAdminLockWaitsForHolderUp(t *testing.T) {
	scheme := adminLockTestScheme(t)
	rvs := adminLockMakeRVS("snap-1")
	rv := adminLockMakeRV(mkMember("rvr-0", true))
	rvr := mkRVRWithName("rvr-0", "node-a", v1alpha1.ReplicaTypeDiskful)
	dr := &v1alpha1.DRBDResource{
		ObjectMeta: metav1.ObjectMeta{Name: "rvr-0"},
		Spec: v1alpha1.DRBDResourceSpec{
			State: v1alpha1.DRBDResourceStateDown,
		},
	}

	r := &Reconciler{
		cl:     adminLockNewClient(scheme, rvs, rv, dr),
		scheme: scheme,
	}

	outcome := r.reconcileAcquireAdminLock(context.Background(), rvs, rv, []*v1alpha1.ReplicatedVolumeReplica{rvr})
	if !outcome.ShouldReturn() {
		t.Fatalf("expected requeue while holder DRBDResource not Up")
	}

	op := &v1alpha1.DRBDResourceOperation{}
	if err := r.cl.Get(context.Background(), client.ObjectKey{Name: adminLockOpName(rvs)}, op); !apierrors.IsNotFound(err) {
		t.Fatalf("expected NotFound for op while holder Down, got: %v", err)
	}
}

func TestReconcileAcquireAdminLockCreatesOpWhenReady(t *testing.T) {
	scheme := adminLockTestScheme(t)
	rvs := adminLockMakeRVS("snap-1")
	rv := adminLockMakeRV(mkMember("rvr-0", true))
	rvr := mkRVRWithName("rvr-0", "node-a", v1alpha1.ReplicaTypeDiskful)
	dr := adminLockMakeUpDRBDR("rvr-0")

	r := &Reconciler{
		cl:     adminLockNewClient(scheme, rvs, rv, dr),
		scheme: scheme,
	}

	outcome := r.reconcileAcquireAdminLock(context.Background(), rvs, rv, []*v1alpha1.ReplicatedVolumeReplica{rvr})
	if !outcome.ShouldReturn() {
		t.Fatalf("expected requeue after creating op, got Continue")
	}

	op := &v1alpha1.DRBDResourceOperation{}
	if err := r.cl.Get(context.Background(), client.ObjectKey{Name: adminLockOpName(rvs)}, op); err != nil {
		t.Fatalf("expected op to be created, got err: %v", err)
	}

	if op.Spec.Type != v1alpha1.DRBDResourceOperationLockAdmin {
		t.Fatalf("op.Spec.Type = %q, want LockAdmin", op.Spec.Type)
	}
	if op.Spec.DRBDResourceName != "rvr-0" {
		t.Fatalf("op.Spec.DRBDResourceName = %q, want rvr-0", op.Spec.DRBDResourceName)
	}

	if len(op.OwnerReferences) != 1 {
		t.Fatalf("expected exactly one ownerRef, got %d", len(op.OwnerReferences))
	}
	owner := op.OwnerReferences[0]
	if owner.Name != rvs.Name || owner.Kind != "ReplicatedVolumeSnapshot" {
		t.Fatalf("ownerRef = %+v, want ReplicatedVolumeSnapshot/%s", owner, rvs.Name)
	}
	if owner.Controller == nil || !*owner.Controller {
		t.Fatalf("ownerRef.Controller = nil/false, want true")
	}
}

func TestReconcileAcquireAdminLockContinuesWhenRunning(t *testing.T) {
	scheme := adminLockTestScheme(t)
	rvs := adminLockMakeRVS("snap-1")
	rv := adminLockMakeRV(mkMember("rvr-0", true))
	rvr := mkRVRWithName("rvr-0", "node-a", v1alpha1.ReplicaTypeDiskful)
	dr := adminLockMakeUpDRBDR("rvr-0")
	op := &v1alpha1.DRBDResourceOperation{
		ObjectMeta: metav1.ObjectMeta{Name: adminLockOpName(rvs)},
		Spec: v1alpha1.DRBDResourceOperationSpec{
			DRBDResourceName: "rvr-0",
			Type:             v1alpha1.DRBDResourceOperationLockAdmin,
		},
		Status: v1alpha1.DRBDResourceOperationStatus{
			Phase: v1alpha1.DRBDOperationPhaseRunning,
		},
	}

	r := &Reconciler{
		cl:     adminLockNewClient(scheme, rvs, rv, dr, op),
		scheme: scheme,
	}

	outcome := r.reconcileAcquireAdminLock(context.Background(), rvs, rv, []*v1alpha1.ReplicatedVolumeReplica{rvr})
	if outcome.ShouldReturn() {
		t.Fatalf("expected Continue on Phase=Running, got terminal outcome (err=%v)", outcome.Error())
	}

	got := &v1alpha1.ReplicatedVolumeSnapshot{}
	if err := r.cl.Get(context.Background(), client.ObjectKey{Name: rvs.Name}, got); err != nil {
		t.Fatalf("get rvs: %v", err)
	}
	cond := findCondition(got.Status.Conditions, v1alpha1.ReplicatedVolumeSnapshotCondAdminLockedType)
	if cond == nil {
		t.Fatalf("expected AdminLocked condition to be set")
	}
	if cond.Status != metav1.ConditionTrue || cond.Reason != v1alpha1.ReplicatedVolumeSnapshotCondAdminLockedReasonAcquired {
		t.Fatalf("AdminLocked = %s/%s, want True/Acquired", cond.Status, cond.Reason)
	}
}

func TestReconcileAcquireAdminLockRequeuesWhenPending(t *testing.T) {
	scheme := adminLockTestScheme(t)
	rvs := adminLockMakeRVS("snap-1")
	rv := adminLockMakeRV(mkMember("rvr-0", true))
	rvr := mkRVRWithName("rvr-0", "node-a", v1alpha1.ReplicaTypeDiskful)
	dr := adminLockMakeUpDRBDR("rvr-0")
	op := &v1alpha1.DRBDResourceOperation{
		ObjectMeta: metav1.ObjectMeta{Name: adminLockOpName(rvs)},
		Spec: v1alpha1.DRBDResourceOperationSpec{
			DRBDResourceName: "rvr-0",
			Type:             v1alpha1.DRBDResourceOperationLockAdmin,
		},
		Status: v1alpha1.DRBDResourceOperationStatus{
			Phase: v1alpha1.DRBDOperationPhasePending,
		},
	}

	r := &Reconciler{
		cl:     adminLockNewClient(scheme, rvs, rv, dr, op),
		scheme: scheme,
	}

	outcome := r.reconcileAcquireAdminLock(context.Background(), rvs, rv, []*v1alpha1.ReplicatedVolumeReplica{rvr})
	if !outcome.ShouldReturn() {
		t.Fatalf("expected requeue on Phase=Pending")
	}
	if outcome.Error() != nil {
		t.Fatalf("unexpected error: %v", outcome.Error())
	}
}

func TestReconcileAcquireAdminLockDeletesFailedOpForRetry(t *testing.T) {
	scheme := adminLockTestScheme(t)
	rvs := adminLockMakeRVS("snap-1")
	rv := adminLockMakeRV(mkMember("rvr-0", true))
	rvr := mkRVRWithName("rvr-0", "node-a", v1alpha1.ReplicaTypeDiskful)
	dr := adminLockMakeUpDRBDR("rvr-0")
	op := &v1alpha1.DRBDResourceOperation{
		ObjectMeta: metav1.ObjectMeta{Name: adminLockOpName(rvs)},
		Spec: v1alpha1.DRBDResourceOperationSpec{
			DRBDResourceName: "rvr-0",
			Type:             v1alpha1.DRBDResourceOperationLockAdmin,
		},
		Status: v1alpha1.DRBDResourceOperationStatus{
			Phase:   v1alpha1.DRBDOperationPhaseFailed,
			Message: "ErrLockHeld",
		},
	}

	r := &Reconciler{
		cl:     adminLockNewClient(scheme, rvs, rv, dr, op),
		scheme: scheme,
	}

	outcome := r.reconcileAcquireAdminLock(context.Background(), rvs, rv, []*v1alpha1.ReplicatedVolumeReplica{rvr})
	if !outcome.ShouldReturn() {
		t.Fatalf("expected requeue after deleting Failed op")
	}

	got := &v1alpha1.DRBDResourceOperation{}
	if err := r.cl.Get(context.Background(), client.ObjectKey{Name: adminLockOpName(rvs)}, got); !apierrors.IsNotFound(err) {
		t.Fatalf("expected Failed op to be deleted, got err: %v", err)
	}
}

func TestComputeAdminLockReadiness(t *testing.T) {
	rvrDiskful0 := mkRVRWithName("rvr-0", "node-a", v1alpha1.ReplicaTypeDiskful)
	rvrDiskful1 := mkRVRWithName("rvr-1", "node-b", v1alpha1.ReplicaTypeDiskful)
	rvrTie := mkRVRWithName("rvr-tie", "node-c", v1alpha1.ReplicaTypeTieBreaker)

	tests := []struct {
		name       string
		rvrs       []*v1alpha1.ReplicatedVolumeReplica
		drbdrs     []*v1alpha1.DRBDResource
		wantReady  bool
		wantSubstr string
	}{
		{
			name: "all healthy with peers established",
			rvrs: []*v1alpha1.ReplicatedVolumeReplica{rvrDiskful0, rvrDiskful1},
			drbdrs: []*v1alpha1.DRBDResource{
				adminLockMakeReadyDRBDR("rvr-0", adminLockReadyPeer("rvr-1", 1)),
				adminLockMakeReadyDRBDR("rvr-1", adminLockReadyPeer("rvr-0", 0)),
			},
			wantReady: true,
		},
		{
			name: "missing DRBDResource for some RVR",
			rvrs: []*v1alpha1.ReplicatedVolumeReplica{rvrDiskful0, rvrDiskful1},
			drbdrs: []*v1alpha1.DRBDResource{
				adminLockMakeReadyDRBDR("rvr-0"),
			},
			wantReady:  false,
			wantSubstr: "DRBDResource \"rvr-1\" is missing",
		},
		{
			name: "diskful with non-UpToDate diskState",
			rvrs: []*v1alpha1.ReplicatedVolumeReplica{rvrDiskful0},
			drbdrs: []*v1alpha1.DRBDResource{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rvr-0"},
					Status: v1alpha1.DRBDResourceStatus{
						DiskState: v1alpha1.DiskStateInconsistent,
					},
				},
			},
			wantReady:  false,
			wantSubstr: "diskState=\"Inconsistent\"",
		},
		{
			name: "tiebreaker with Diskless is healthy",
			rvrs: []*v1alpha1.ReplicatedVolumeReplica{rvrDiskful0, rvrTie},
			drbdrs: []*v1alpha1.DRBDResource{
				adminLockMakeReadyDRBDR("rvr-0", adminLockReadyPeer("rvr-tie", 2)),
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rvr-tie"},
					Status: v1alpha1.DRBDResourceStatus{
						DiskState: v1alpha1.DiskStateDiskless,
						Peers: []v1alpha1.DRBDResourcePeerStatus{
							adminLockReadyPeer("rvr-0", 0),
						},
					},
				},
			},
			wantReady: true,
		},
		{
			name: "tiebreaker with non-Diskless is not ready",
			rvrs: []*v1alpha1.ReplicatedVolumeReplica{rvrTie},
			drbdrs: []*v1alpha1.DRBDResource{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rvr-tie"},
					Status: v1alpha1.DRBDResourceStatus{
						DiskState: v1alpha1.DiskStateUpToDate,
					},
				},
			},
			wantReady:  false,
			wantSubstr: "want Diskless for TieBreaker",
		},
		{
			name: "peer not Connected blocks readiness",
			rvrs: []*v1alpha1.ReplicatedVolumeReplica{rvrDiskful0},
			drbdrs: []*v1alpha1.DRBDResource{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rvr-0"},
					Status: v1alpha1.DRBDResourceStatus{
						DiskState: v1alpha1.DiskStateUpToDate,
						Peers: []v1alpha1.DRBDResourcePeerStatus{
							{
								Name:             "rvr-1",
								NodeID:           1,
								ConnectionState:  v1alpha1.ConnectionStateConnecting,
								ReplicationState: v1alpha1.ReplicationStateOff,
							},
						},
					},
				},
			},
			wantReady:  false,
			wantSubstr: "connectionState=\"Connecting\"",
		},
		{
			name: "peer in active sync blocks readiness",
			rvrs: []*v1alpha1.ReplicatedVolumeReplica{rvrDiskful0},
			drbdrs: []*v1alpha1.DRBDResource{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rvr-0"},
					Status: v1alpha1.DRBDResourceStatus{
						DiskState: v1alpha1.DiskStateUpToDate,
						Peers: []v1alpha1.DRBDResourcePeerStatus{
							{
								Name:             "rvr-1",
								NodeID:           1,
								ConnectionState:  v1alpha1.ConnectionStateConnected,
								ReplicationState: v1alpha1.ReplicationStateSyncTarget,
							},
						},
					},
				},
			},
			wantReady:  false,
			wantSubstr: "replicationState=\"SyncTarget\"",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ready, reason := computeAdminLockReadiness(tc.rvrs, tc.drbdrs)
			if ready != tc.wantReady {
				t.Fatalf("ready = %v (reason=%q), want %v", ready, reason, tc.wantReady)
			}
			if !tc.wantReady {
				if !contains(reason, tc.wantSubstr) {
					t.Fatalf("reason = %q, want substring %q", reason, tc.wantSubstr)
				}
			}
		})
	}
}

func TestReconcileAcquireAdminLockWaitsForPeerNotReady(t *testing.T) {
	scheme := adminLockTestScheme(t)
	rvs := adminLockMakeRVS("snap-1")
	rv := adminLockMakeRV(
		mkMember("rvr-0", true),
		mkMember("rvr-1", false),
	)
	rvr0 := mkRVRWithName("rvr-0", "node-a", v1alpha1.ReplicaTypeDiskful)
	rvr1 := mkRVRWithName("rvr-1", "node-b", v1alpha1.ReplicaTypeDiskful)
	holderDR := adminLockMakeReadyDRBDR("rvr-0", adminLockReadyPeer("rvr-1", 1))
	peerDR := &v1alpha1.DRBDResource{
		ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		Spec: v1alpha1.DRBDResourceSpec{
			State: v1alpha1.DRBDResourceStateUp,
		},
		Status: v1alpha1.DRBDResourceStatus{
			DiskState: v1alpha1.DiskStateInconsistent,
		},
	}

	r := &Reconciler{
		cl:     adminLockNewClient(scheme, rvs, rv, holderDR, peerDR),
		scheme: scheme,
	}

	outcome := r.reconcileAcquireAdminLock(context.Background(), rvs, rv, []*v1alpha1.ReplicatedVolumeReplica{rvr0, rvr1})
	if !outcome.ShouldReturn() {
		t.Fatalf("expected requeue while peer DRBDResource not ready")
	}

	op := &v1alpha1.DRBDResourceOperation{}
	if err := r.cl.Get(context.Background(), client.ObjectKey{Name: adminLockOpName(rvs)}, op); !apierrors.IsNotFound(err) {
		t.Fatalf("expected no LockAdmin op while pre-check fails, got err: %v", err)
	}

	got := &v1alpha1.ReplicatedVolumeSnapshot{}
	if err := r.cl.Get(context.Background(), client.ObjectKey{Name: rvs.Name}, got); err != nil {
		t.Fatalf("get rvs: %v", err)
	}
	cond := findCondition(got.Status.Conditions, v1alpha1.ReplicatedVolumeSnapshotCondAdminLockedType)
	if cond == nil || cond.Status != metav1.ConditionFalse ||
		cond.Reason != v1alpha1.ReplicatedVolumeSnapshotCondAdminLockedReasonClusterNotReady {
		t.Fatalf("AdminLocked condition = %+v, want False/ClusterNotReady", cond)
	}
}

func TestReconcileReleaseAdminLockNoOpReturnsContinue(t *testing.T) {
	scheme := adminLockTestScheme(t)
	rvs := adminLockMakeRVS("snap-1")

	r := &Reconciler{
		cl:     adminLockNewClient(scheme, rvs),
		scheme: scheme,
	}

	outcome := r.reconcileReleaseAdminLock(context.Background(), rvs)
	if outcome.ShouldReturn() {
		t.Fatalf("expected Continue when op missing, got terminal (err=%v)", outcome.Error())
	}

	got := &v1alpha1.ReplicatedVolumeSnapshot{}
	if err := r.cl.Get(context.Background(), client.ObjectKey{Name: rvs.Name}, got); err != nil {
		t.Fatalf("get rvs: %v", err)
	}
	cond := findCondition(got.Status.Conditions, v1alpha1.ReplicatedVolumeSnapshotCondAdminLockedType)
	if cond == nil || cond.Status != metav1.ConditionFalse ||
		cond.Reason != v1alpha1.ReplicatedVolumeSnapshotCondAdminLockedReasonReleased {
		t.Fatalf("AdminLocked condition = %+v, want False/Released", cond)
	}
}

func TestReconcileReleaseAdminLockDeletesActiveOp(t *testing.T) {
	scheme := adminLockTestScheme(t)
	rvs := adminLockMakeRVS("snap-1")
	op := &v1alpha1.DRBDResourceOperation{
		ObjectMeta: metav1.ObjectMeta{Name: adminLockOpName(rvs)},
		Spec: v1alpha1.DRBDResourceOperationSpec{
			DRBDResourceName: "rvr-0",
			Type:             v1alpha1.DRBDResourceOperationLockAdmin,
		},
		Status: v1alpha1.DRBDResourceOperationStatus{
			Phase: v1alpha1.DRBDOperationPhaseRunning,
		},
	}

	r := &Reconciler{
		cl:     adminLockNewClient(scheme, rvs, op),
		scheme: scheme,
	}

	outcome := r.reconcileReleaseAdminLock(context.Background(), rvs)
	if !outcome.ShouldReturn() {
		t.Fatalf("expected requeue after Delete")
	}

	got := &v1alpha1.DRBDResourceOperation{}
	err := r.cl.Get(context.Background(), client.ObjectKey{Name: adminLockOpName(rvs)}, got)
	if err != nil && !apierrors.IsNotFound(err) {
		t.Fatalf("Get after delete: %v", err)
	}

	gotRVS := &v1alpha1.ReplicatedVolumeSnapshot{}
	if err := r.cl.Get(context.Background(), client.ObjectKey{Name: rvs.Name}, gotRVS); err != nil {
		t.Fatalf("get rvs: %v", err)
	}
	cond := findCondition(gotRVS.Status.Conditions, v1alpha1.ReplicatedVolumeSnapshotCondAdminLockedType)
	if cond == nil || cond.Status != metav1.ConditionFalse ||
		cond.Reason != v1alpha1.ReplicatedVolumeSnapshotCondAdminLockedReasonReleasing {
		t.Fatalf("AdminLocked condition = %+v, want False/Releasing", cond)
	}
}

func TestReconcileReleaseAdminLockWhileDeleting(t *testing.T) {
	scheme := adminLockTestScheme(t)
	rvs := adminLockMakeRVS("snap-1")

	now := metav1.Now()
	op := &v1alpha1.DRBDResourceOperation{
		ObjectMeta: metav1.ObjectMeta{
			Name:              adminLockOpName(rvs),
			DeletionTimestamp: &now,
			Finalizers:        []string{"drbd.deckhouse.io/admin-lock"},
		},
		Spec: v1alpha1.DRBDResourceOperationSpec{
			DRBDResourceName: "rvr-0",
			Type:             v1alpha1.DRBDResourceOperationLockAdmin,
		},
		Status: v1alpha1.DRBDResourceOperationStatus{
			Phase: v1alpha1.DRBDOperationPhaseRunning,
		},
	}

	r := &Reconciler{
		cl:     adminLockNewClient(scheme, rvs, op),
		scheme: scheme,
	}

	outcome := r.reconcileReleaseAdminLock(context.Background(), rvs)
	if !outcome.ShouldReturn() {
		t.Fatalf("expected requeue while op still terminating")
	}
	if outcome.Error() != nil {
		t.Fatalf("unexpected error: %v", outcome.Error())
	}
}

func findCondition(conds []metav1.Condition, t string) *metav1.Condition {
	for i := range conds {
		if conds[i].Type == t {
			return &conds[i]
		}
	}
	return nil
}
