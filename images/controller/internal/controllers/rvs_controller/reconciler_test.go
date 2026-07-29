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
	"sync"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvs_controller/snapmesh"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes"
	indextest "github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes/testhelpers"
)

var snapmeshRegistryOnce sync.Once

func ensureSnapmeshRegistry() {
	snapmeshRegistryOnce.Do(snapmesh.BuildRegistry)
}

func mkRVRWithName(name, nodeName string, rType v1alpha1.ReplicaType) *v1alpha1.ReplicatedVolumeReplica {
	return &v1alpha1.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
			Type:     rType,
			NodeName: nodeName,
		},
	}
}

func mkMember(name string, attached bool) v1alpha1.DatameshMember {
	return v1alpha1.DatameshMember{
		Name:     name,
		Attached: attached,
	}
}

func TestSplitRVRsByAttachment(t *testing.T) {
	tests := []struct {
		name          string
		rvrs          []*v1alpha1.ReplicatedVolumeReplica
		members       []v1alpha1.DatameshMember
		wantSecondary []string
		wantPrimary   []string
	}{
		{
			name: "all secondary (no attached members)",
			rvrs: []*v1alpha1.ReplicatedVolumeReplica{
				mkRVRWithName("rvr-0", "node-a", v1alpha1.ReplicaTypeDiskful),
				mkRVRWithName("rvr-1", "node-b", v1alpha1.ReplicaTypeDiskful),
			},
			members: []v1alpha1.DatameshMember{
				mkMember("rvr-0", false),
				mkMember("rvr-1", false),
			},
			wantSecondary: []string{"rvr-0", "rvr-1"},
			wantPrimary:   nil,
		},
		{
			name: "one primary, one secondary",
			rvrs: []*v1alpha1.ReplicatedVolumeReplica{
				mkRVRWithName("rvr-0", "node-a", v1alpha1.ReplicaTypeDiskful),
				mkRVRWithName("rvr-1", "node-b", v1alpha1.ReplicaTypeDiskful),
			},
			members: []v1alpha1.DatameshMember{
				mkMember("rvr-0", true),
				mkMember("rvr-1", false),
			},
			wantSecondary: []string{"rvr-1"},
			wantPrimary:   []string{"rvr-0"},
		},
		{
			name: "skips non-diskful and empty nodeName",
			rvrs: []*v1alpha1.ReplicatedVolumeReplica{
				mkRVRWithName("rvr-0", "node-a", v1alpha1.ReplicaTypeDiskful),
				mkRVRWithName("rvr-1", "", v1alpha1.ReplicaTypeDiskful),
				mkRVRWithName("rvr-2", "node-c", v1alpha1.ReplicaTypeTieBreaker),
			},
			members: []v1alpha1.DatameshMember{
				mkMember("rvr-0", false),
			},
			wantSecondary: []string{"rvr-0"},
			wantPrimary:   nil,
		},
		{
			name:          "empty input",
			rvrs:          nil,
			members:       nil,
			wantSecondary: nil,
			wantPrimary:   nil,
		},
		{
			name: "multiple attached (multi-attach)",
			rvrs: []*v1alpha1.ReplicatedVolumeReplica{
				mkRVRWithName("rvr-0", "node-a", v1alpha1.ReplicaTypeDiskful),
				mkRVRWithName("rvr-1", "node-b", v1alpha1.ReplicaTypeDiskful),
				mkRVRWithName("rvr-2", "node-c", v1alpha1.ReplicaTypeDiskful),
			},
			members: []v1alpha1.DatameshMember{
				mkMember("rvr-0", true),
				mkMember("rvr-1", true),
				mkMember("rvr-2", false),
			},
			wantSecondary: []string{"rvr-2"},
			wantPrimary:   []string{"rvr-0", "rvr-1"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			secondary, primary := splitRVRsByAttachment(tc.rvrs, tc.members)

			secNames := rvrNames(secondary)
			priNames := rvrNames(primary)

			if !strSliceEqual(secNames, tc.wantSecondary) {
				t.Errorf("secondary = %v, want %v", secNames, tc.wantSecondary)
			}
			if !strSliceEqual(priNames, tc.wantPrimary) {
				t.Errorf("primary = %v, want %v", priNames, tc.wantPrimary)
			}
		})
	}
}

func TestAllRVRSReady(t *testing.T) {
	readyRVRS := &v1alpha1.ReplicatedVolumeReplicaSnapshot{
		Status: v1alpha1.ReplicatedVolumeReplicaSnapshotStatus{
			Phase: v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseReady,
		},
	}
	pendingRVRS := &v1alpha1.ReplicatedVolumeReplicaSnapshot{
		Status: v1alpha1.ReplicatedVolumeReplicaSnapshotStatus{
			Phase: v1alpha1.ReplicatedVolumeReplicaSnapshotPhasePending,
		},
	}

	tests := []struct {
		name          string
		rvrs          []*v1alpha1.ReplicatedVolumeReplica
		existingByRVR map[string]*v1alpha1.ReplicatedVolumeReplicaSnapshot
		want          bool
	}{
		{
			name:          "empty list",
			rvrs:          nil,
			existingByRVR: nil,
			want:          true,
		},
		{
			name: "all ready",
			rvrs: []*v1alpha1.ReplicatedVolumeReplica{
				mkRVRWithName("rvr-0", "node-a", v1alpha1.ReplicaTypeDiskful),
				mkRVRWithName("rvr-1", "node-b", v1alpha1.ReplicaTypeDiskful),
			},
			existingByRVR: map[string]*v1alpha1.ReplicatedVolumeReplicaSnapshot{
				"rvr-0": readyRVRS,
				"rvr-1": readyRVRS,
			},
			want: true,
		},
		{
			name: "one not ready",
			rvrs: []*v1alpha1.ReplicatedVolumeReplica{
				mkRVRWithName("rvr-0", "node-a", v1alpha1.ReplicaTypeDiskful),
				mkRVRWithName("rvr-1", "node-b", v1alpha1.ReplicaTypeDiskful),
			},
			existingByRVR: map[string]*v1alpha1.ReplicatedVolumeReplicaSnapshot{
				"rvr-0": readyRVRS,
				"rvr-1": pendingRVRS,
			},
			want: false,
		},
		{
			name: "rvrs not yet created",
			rvrs: []*v1alpha1.ReplicatedVolumeReplica{
				mkRVRWithName("rvr-0", "node-a", v1alpha1.ReplicaTypeDiskful),
			},
			existingByRVR: map[string]*v1alpha1.ReplicatedVolumeReplicaSnapshot{},
			want:          false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := allRVRSReady(tc.rvrs, tc.existingByRVR)
			if got != tc.want {
				t.Errorf("allRVRSReady() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSnapshotCreationFlowGatesPrimaryBySecondaryReadiness(t *testing.T) {
	rvrs := []*v1alpha1.ReplicatedVolumeReplica{
		mkRVRWithName("rvr-primary", "node-a", v1alpha1.ReplicaTypeDiskful),
		mkRVRWithName("rvr-secondary", "node-b", v1alpha1.ReplicaTypeDiskful),
	}
	members := []v1alpha1.DatameshMember{
		mkMember("rvr-primary", true),
		mkMember("rvr-secondary", false),
	}

	secondary, primary := splitRVRsByAttachment(rvrs, members)

	if !strSliceEqual(rvrNames(secondary), []string{"rvr-secondary"}) {
		t.Fatalf("secondary = %v, want [rvr-secondary]", rvrNames(secondary))
	}
	if !strSliceEqual(rvrNames(primary), []string{"rvr-primary"}) {
		t.Fatalf("primary = %v, want [rvr-primary]", rvrNames(primary))
	}

	if allRVRSReady(secondary, map[string]*v1alpha1.ReplicatedVolumeReplicaSnapshot{}) {
		t.Fatalf("allRVRSReady(secondary, empty) = true, want false")
	}

	if !allRVRSReady(secondary, map[string]*v1alpha1.ReplicatedVolumeReplicaSnapshot{
		"rvr-secondary": {
			Status: v1alpha1.ReplicatedVolumeReplicaSnapshotStatus{
				Phase: v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseReady,
			},
		},
	}) {
		t.Fatalf("allRVRSReady(secondary, ready secondary) = false, want true")
	}
}

func TestCreateMissingRVRSsCreatesExpectedSnapshotObjects(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}

	rvs := &v1alpha1.ReplicatedVolumeSnapshot{
		ObjectMeta: metav1.ObjectMeta{Name: "snap-1"},
	}
	rvr := mkRVRWithName("rvr-secondary", "node-b", v1alpha1.ReplicaTypeDiskful)

	reconciler := &Reconciler{
		cl:     fake.NewClientBuilder().WithScheme(scheme).Build(),
		scheme: scheme,
	}

	requeue, err := reconciler.createMissingRVRSs(context.Background(), rvs, []*v1alpha1.ReplicatedVolumeReplica{rvr}, nil)
	if err != nil {
		t.Fatalf("createMissingRVRSs() error = %v", err)
	}
	if !requeue {
		t.Fatalf("createMissingRVRSs() requeue = false, want true")
	}

	got := &v1alpha1.ReplicatedVolumeReplicaSnapshot{}
	if err := reconciler.cl.Get(context.Background(), client.ObjectKey{Name: "snap-1-node-b"}, got); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Spec.ReplicatedVolumeSnapshotName != "snap-1" {
		t.Fatalf("ReplicatedVolumeSnapshotName = %q, want %q", got.Spec.ReplicatedVolumeSnapshotName, "snap-1")
	}
	if got.Spec.ReplicatedVolumeReplicaName != "rvr-secondary" {
		t.Fatalf("ReplicatedVolumeReplicaName = %q, want %q", got.Spec.ReplicatedVolumeReplicaName, "rvr-secondary")
	}
	if got.Spec.NodeName != "node-b" {
		t.Fatalf("NodeName = %q, want %q", got.Spec.NodeName, "node-b")
	}
}

func TestReconcileNormalRoutesSnapshotCreationThroughPrepare(t *testing.T) {
	ensureSnapmeshRegistry()

	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}

	rvs := &v1alpha1.ReplicatedVolumeSnapshot{
		ObjectMeta: metav1.ObjectMeta{Name: "snap-1"},
		Spec: v1alpha1.ReplicatedVolumeSnapshotSpec{
			ReplicatedVolumeName: "rv-1",
		},
	}
	rv := &v1alpha1.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
		Status: v1alpha1.ReplicatedVolumeStatus{
			Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
				Members: []v1alpha1.DatameshMember{
					{Name: "rvr-0", NodeName: "node-a", Attached: true},
					{Name: "rvr-1", NodeName: "node-b", Attached: false},
				},
			},
		},
	}
	primary := &v1alpha1.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{Name: "rvr-0"},
		Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
			ReplicatedVolumeName:       "rv-1",
			Type:                       v1alpha1.ReplicaTypeDiskful,
			NodeName:                   "node-a",
			LVMVolumeGroupName:         "lvg-1",
			LVMVolumeGroupThinPoolName: "thin-1",
		},
	}
	secondary := &v1alpha1.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
			ReplicatedVolumeName:       "rv-1",
			Type:                       v1alpha1.ReplicaTypeDiskful,
			NodeName:                   "node-b",
			LVMVolumeGroupName:         "lvg-1",
			LVMVolumeGroupThinPoolName: "thin-1",
		},
	}

	holderDRBDR := &v1alpha1.DRBDResource{
		ObjectMeta: metav1.ObjectMeta{Name: "rvr-0"},
		Spec:       v1alpha1.DRBDResourceSpec{State: v1alpha1.DRBDResourceStateUp},
	}
	adminLockOp := &v1alpha1.DRBDResourceOperation{
		ObjectMeta: metav1.ObjectMeta{Name: "snap-1-admin-lock"},
		Spec: v1alpha1.DRBDResourceOperationSpec{
			NodeName:         "node-a",
			DRBDResourceName: "rvr-0",
			Type:             v1alpha1.DRBDResourceOperationLockAdmin,
		},
		Status: v1alpha1.DRBDResourceOperationStatus{
			Phase: v1alpha1.DRBDOperationPhaseRunning,
		},
	}

	builder := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.ReplicatedVolumeSnapshot{}).
		WithObjects(rvs, rv, primary, secondary, holderDRBDR, adminLockOp)
	builder = indextest.WithRVRByReplicatedVolumeNameIndex(builder)
	builder = builder.WithIndex(&v1alpha1.ReplicatedVolumeReplicaSnapshot{}, indexes.IndexFieldRVRSBySnapshotName, func(obj client.Object) []string {
		rvrs, ok := obj.(*v1alpha1.ReplicatedVolumeReplicaSnapshot)
		if !ok || rvrs.Spec.ReplicatedVolumeSnapshotName == "" {
			return nil
		}
		return []string{rvrs.Spec.ReplicatedVolumeSnapshotName}
	})

	reconciler := &Reconciler{
		cl:     builder.Build(),
		scheme: scheme,
	}

	outcome := reconciler.reconcileNormal(context.Background(), rvs)
	if !outcome.ShouldReturn() {
		t.Fatalf("reconcileNormal() should return after prepare")
	}

	if len(rvs.Status.PrepareTransitions) == 0 {
		t.Fatalf("PrepareTransitions is empty, want active prepare transition")
	}

	trackOp := &v1alpha1.DRBDResourceOperation{}
	if err := reconciler.cl.Get(context.Background(), client.ObjectKey{Name: "snap-1-track-bitmap"}, trackOp); err != nil {
		t.Fatalf("Get track bitmap operation() error = %v", err)
	}
	if trackOp.Spec.Type != v1alpha1.DRBDResourceOperationTrackBitmap {
		t.Fatalf("Track bitmap operation type = %q, want %q", trackOp.Spec.Type, v1alpha1.DRBDResourceOperationTrackBitmap)
	}

	createdSecondary := &v1alpha1.ReplicatedVolumeReplicaSnapshot{}
	if err := reconciler.cl.Get(context.Background(), client.ObjectKey{Name: "snap-1-node-b"}, createdSecondary); err == nil {
		t.Fatalf("secondary RVRS was created on first prepare pass")
	}
}

func TestReconcileAggregateStatusDefersFailureWhilePrepareActive(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}

	rvs := &v1alpha1.ReplicatedVolumeSnapshot{
		ObjectMeta: metav1.ObjectMeta{Name: "snap-1"},
		Spec: v1alpha1.ReplicatedVolumeSnapshotSpec{
			ReplicatedVolumeName: "rv-1",
		},
		Status: v1alpha1.ReplicatedVolumeSnapshotStatus{
			PrepareTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
				{
					Type:   v1alpha1.ReplicatedVolumeDatameshTransitionType("PrepareSnapshot"),
					Group:  v1alpha1.ReplicatedVolumeDatameshTransitionGroupSync,
					PlanID: "prepare-snapshot/v1",
					Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
						{
							Name:   "Create primary snapshot",
							Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
						},
					},
				},
			},
		},
	}
	rv := &v1alpha1.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
		Status: v1alpha1.ReplicatedVolumeStatus{
			Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
				Members: []v1alpha1.DatameshMember{
					{Name: "rvr-0", NodeName: "node-a", Attached: true},
				},
			},
		},
	}
	rvr := &v1alpha1.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{Name: "rvr-0"},
		Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
			ReplicatedVolumeName: "rv-1",
			Type:                 v1alpha1.ReplicaTypeDiskful,
			NodeName:             "node-a",
		},
	}
	failedRVRS := &v1alpha1.ReplicatedVolumeReplicaSnapshot{
		ObjectMeta: metav1.ObjectMeta{Name: "snap-1-node-a"},
		Spec: v1alpha1.ReplicatedVolumeReplicaSnapshotSpec{
			ReplicatedVolumeSnapshotName: "snap-1",
			ReplicatedVolumeReplicaName:  "rvr-0",
			NodeName:                     "node-a",
		},
		Status: v1alpha1.ReplicatedVolumeReplicaSnapshotStatus{
			Phase: v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseFailed,
		},
	}

	reconciler := &Reconciler{
		cl: fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&v1alpha1.ReplicatedVolumeSnapshot{}).
			WithObjects(rvs).
			Build(),
		scheme: scheme,
	}

	outcome := reconciler.reconcileAggregateStatus(context.Background(), rvs, rv, []*v1alpha1.ReplicatedVolumeReplica{rvr}, []*v1alpha1.ReplicatedVolumeReplicaSnapshot{failedRVRS})
	if outcome.ShouldReturn() {
		t.Fatalf("reconcileAggregateStatus() unexpectedly returned terminal outcome")
	}
	if rvs.Status.Phase != v1alpha1.ReplicatedVolumeSnapshotPhaseInProgress {
		t.Fatalf("Phase = %q, want %q", rvs.Status.Phase, v1alpha1.ReplicatedVolumeSnapshotPhaseInProgress)
	}
	if rvs.Status.Message != "Prepare cleanup is in progress after replica snapshot failure" {
		t.Fatalf("Message = %q, want cleanup message", rvs.Status.Message)
	}
}

func rvrNames(rvrs []*v1alpha1.ReplicatedVolumeReplica) []string {
	if len(rvrs) == 0 {
		return nil
	}
	names := make([]string, len(rvrs))
	for i, rvr := range rvrs {
		names[i] = rvr.Name
	}
	return names
}

func TestFindThickDiskfulReplica(t *testing.T) {
	thin := func(name, node string) *v1alpha1.ReplicatedVolumeReplica {
		rvr := mkRVRWithName(name, node, v1alpha1.ReplicaTypeDiskful)
		rvr.Spec.LVMVolumeGroupThinPoolName = "thin-1"
		return rvr
	}
	thick := func(name, node string) *v1alpha1.ReplicatedVolumeReplica {
		return mkRVRWithName(name, node, v1alpha1.ReplicaTypeDiskful)
	}

	tests := []struct {
		name string
		rvrs []*v1alpha1.ReplicatedVolumeReplica
		want string
	}{
		{
			name: "no replicas",
			want: "",
		},
		{
			name: "all diskful replicas thin",
			rvrs: []*v1alpha1.ReplicatedVolumeReplica{thin("rvr-0", "node-a"), thin("rvr-1", "node-b")},
			want: "",
		},
		{
			name: "one diskful replica thick",
			rvrs: []*v1alpha1.ReplicatedVolumeReplica{thin("rvr-0", "node-a"), thick("rvr-1", "node-b")},
			want: "rvr-1",
		},
		{
			// Diskless replicas have no backing volume at all, so a missing
			// thin pool on them says nothing about the volume.
			name: "diskless replicas without thin pool are ignored",
			rvrs: []*v1alpha1.ReplicatedVolumeReplica{
				thin("rvr-0", "node-a"),
				mkRVRWithName("rvr-1", "node-b", v1alpha1.ReplicaTypeAccess),
				mkRVRWithName("rvr-2", "node-c", v1alpha1.ReplicaTypeTieBreaker),
			},
			want: "",
		},
		{
			name: "nil entries are skipped",
			rvrs: []*v1alpha1.ReplicatedVolumeReplica{nil, thin("rvr-0", "node-a")},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := findThickDiskfulReplica(tt.rvrs); got != tt.want {
				t.Errorf("findThickDiskfulReplica() = %q, want %q", got, tt.want)
			}
		})
	}
}

// A snapshot of a thick volume must be rejected up front, before the admin lock
// is taken or any RVRS is created, because sds-node-configurator only snapshots
// thin LVs.
func TestReconcileNormalRejectsThickVolume(t *testing.T) {
	ensureSnapmeshRegistry()

	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}

	rvs := &v1alpha1.ReplicatedVolumeSnapshot{
		ObjectMeta: metav1.ObjectMeta{Name: "snap-1"},
		Spec:       v1alpha1.ReplicatedVolumeSnapshotSpec{ReplicatedVolumeName: "rv-1"},
	}
	rv := &v1alpha1.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
		Status: v1alpha1.ReplicatedVolumeStatus{
			Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
				Members: []v1alpha1.DatameshMember{
					{Name: "rvr-0", NodeName: "node-a", Attached: true},
				},
			},
		},
	}
	// Diskful, but backed by a thick LV: no thin pool.
	rvr := &v1alpha1.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{Name: "rvr-0"},
		Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
			ReplicatedVolumeName: "rv-1",
			Type:                 v1alpha1.ReplicaTypeDiskful,
			NodeName:             "node-a",
			LVMVolumeGroupName:   "lvg-1",
		},
	}

	builder := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.ReplicatedVolumeSnapshot{}).
		WithObjects(rvs, rv, rvr)
	builder = indextest.WithRVRByReplicatedVolumeNameIndex(builder)

	reconciler := &Reconciler{cl: builder.Build(), scheme: scheme}

	if outcome := reconciler.reconcileNormal(context.Background(), rvs); outcome.Error() != nil {
		t.Fatalf("reconcileNormal() error = %v", outcome.Error())
	}

	got := &v1alpha1.ReplicatedVolumeSnapshot{}
	if err := reconciler.cl.Get(context.Background(), client.ObjectKey{Name: "snap-1"}, got); err != nil {
		t.Fatalf("get RVS: %v", err)
	}
	if got.Status.Phase != v1alpha1.ReplicatedVolumeSnapshotPhaseFailed {
		t.Errorf("phase = %q, want %q", got.Status.Phase, v1alpha1.ReplicatedVolumeSnapshotPhaseFailed)
	}
	if got.Status.ReadyToUse {
		t.Error("readyToUse = true, want false")
	}
	if !contains(got.Status.Message, "LVMThin") {
		t.Errorf("message should point at the required pool type, got %q", got.Status.Message)
	}

	// No admin lock op and no per-replica snapshot may have been created.
	opList := &v1alpha1.DRBDResourceOperationList{}
	if err := reconciler.cl.List(context.Background(), opList); err != nil {
		t.Fatalf("list ops: %v", err)
	}
	if len(opList.Items) != 0 {
		t.Errorf("expected no DRBDResourceOperation, got %d", len(opList.Items))
	}
	rvrsList := &v1alpha1.ReplicatedVolumeReplicaSnapshotList{}
	if err := reconciler.cl.List(context.Background(), rvrsList); err != nil {
		t.Fatalf("list RVRS: %v", err)
	}
	if len(rvrsList.Items) != 0 {
		t.Errorf("expected no ReplicatedVolumeReplicaSnapshot, got %d", len(rvrsList.Items))
	}
}

func strSliceEqual(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Deleting a snapshot while a volume is still being restored from it must not
// tear down the per-replica snapshots the restore reads from. rv-controller owns
// the restore-source finalizer; until it is gone the cascade stays parked.
func TestReconcileDeleteDefersWhileRestoreSourceFinalizerPresent(t *testing.T) {
	ensureSnapmeshRegistry()

	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}

	now := metav1.Now()
	rvs := &v1alpha1.ReplicatedVolumeSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "snap-1",
			DeletionTimestamp: &now,
			Finalizers: []string{
				v1alpha1.RVSControllerFinalizer,
				v1alpha1.RVSRestoreSourceFinalizer,
			},
		},
		Spec: v1alpha1.ReplicatedVolumeSnapshotSpec{ReplicatedVolumeName: "rv-1"},
		Status: v1alpha1.ReplicatedVolumeSnapshotStatus{
			Phase:      v1alpha1.ReplicatedVolumeSnapshotPhaseReady,
			ReadyToUse: true,
		},
	}
	child := &v1alpha1.ReplicatedVolumeReplicaSnapshot{
		ObjectMeta: metav1.ObjectMeta{Name: "snap-1-rvr-0"},
		Spec: v1alpha1.ReplicatedVolumeReplicaSnapshotSpec{
			ReplicatedVolumeSnapshotName: "snap-1",
			ReplicatedVolumeReplicaName:  "rvr-0",
			NodeName:                     "node-a",
		},
	}

	builder := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.ReplicatedVolumeSnapshot{}).
		WithObjects(rvs, child)
	builder = builder.WithIndex(&v1alpha1.ReplicatedVolumeReplicaSnapshot{}, indexes.IndexFieldRVRSBySnapshotName, func(obj client.Object) []string {
		o, ok := obj.(*v1alpha1.ReplicatedVolumeReplicaSnapshot)
		if !ok || o.Spec.ReplicatedVolumeSnapshotName == "" {
			return nil
		}
		return []string{o.Spec.ReplicatedVolumeSnapshotName}
	})

	reconciler := &Reconciler{cl: builder.Build(), scheme: scheme}

	if outcome := reconciler.reconcileDelete(context.Background(), rvs); outcome.Error() != nil {
		t.Fatalf("reconcileDelete() error = %v", outcome.Error())
	}

	// The child snapshot must survive and keep no deletionTimestamp.
	got := &v1alpha1.ReplicatedVolumeReplicaSnapshot{}
	if err := reconciler.cl.Get(context.Background(), client.ObjectKey{Name: child.Name}, got); err != nil {
		t.Fatalf("child RVRS was removed while the restore was still running: %v", err)
	}
	if got.DeletionTimestamp != nil {
		t.Error("child RVRS marked for deletion while the restore was still running")
	}

	// Our own finalizer must stay, otherwise the RVS would vanish as soon as
	// rv-controller drops the restore-source one.
	gotRVS := &v1alpha1.ReplicatedVolumeSnapshot{}
	if err := reconciler.cl.Get(context.Background(), client.ObjectKey{Name: "snap-1"}, gotRVS); err != nil {
		t.Fatalf("get RVS: %v", err)
	}
	if !obju.HasFinalizer(gotRVS, v1alpha1.RVSControllerFinalizer) {
		t.Error("controller finalizer released while deletion was deferred")
	}
	if gotRVS.Status.Phase != v1alpha1.ReplicatedVolumeSnapshotPhaseDeleting {
		t.Errorf("phase = %q, want %q", gotRVS.Status.Phase, v1alpha1.ReplicatedVolumeSnapshotPhaseDeleting)
	}
	if !contains(gotRVS.Status.Message, "restored") {
		t.Errorf("message does not explain the wait: %q", gotRVS.Status.Message)
	}
}
