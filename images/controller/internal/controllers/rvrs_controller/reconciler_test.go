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

package rvrscontroller

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
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

const (
	testRVRSName = "snap-1-rvr-0"
	testRVRName  = "rvr-0"
	testLLVName  = "llv-rvr-0"
	testNodeName = "node-a"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme(srv) error = %v", err)
	}
	if err := snc.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme(snc) error = %v", err)
	}
	return scheme
}

func newClient(scheme *runtime.Scheme, objs ...client.Object) client.Client {
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.ReplicatedVolumeReplicaSnapshot{}).
		WithObjects(objs...).
		Build()
}

func makeRVRS(mutators ...func(*v1alpha1.ReplicatedVolumeReplicaSnapshot)) *v1alpha1.ReplicatedVolumeReplicaSnapshot {
	rvrs := &v1alpha1.ReplicatedVolumeReplicaSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name: testRVRSName,
			UID:  types.UID("rvrs-uid"),
		},
		Spec: v1alpha1.ReplicatedVolumeReplicaSnapshotSpec{
			ReplicatedVolumeSnapshotName: "snap-1",
			ReplicatedVolumeReplicaName:  testRVRName,
			NodeName:                     testNodeName,
		},
	}
	for _, m := range mutators {
		m(rvrs)
	}
	return rvrs
}

// makeDRBDR is the object the controller reads the backing LLV name from.
func makeDRBDR(llvName string) *v1alpha1.DRBDResource {
	return &v1alpha1.DRBDResource{
		ObjectMeta: metav1.ObjectMeta{Name: testRVRName},
		Spec: v1alpha1.DRBDResourceSpec{
			LVMLogicalVolumeName: llvName,
		},
	}
}

func makeLLVS(name string, status *snc.LVMLogicalVolumeSnapshotStatus) *snc.LVMLogicalVolumeSnapshot {
	llvs := &snc.LVMLogicalVolumeSnapshot{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: snc.LVMLogicalVolumeSnapshotSpec{
			ActualSnapshotNameOnTheNode: name,
			LVMLogicalVolumeName:        testLLVName,
		},
	}
	if status != nil {
		llvs.Status = status
	}
	return llvs
}

func getRVRS(t *testing.T, cl client.Client) *v1alpha1.ReplicatedVolumeReplicaSnapshot {
	t.Helper()
	got := &v1alpha1.ReplicatedVolumeReplicaSnapshot{}
	if err := cl.Get(context.Background(), client.ObjectKey{Name: testRVRSName}, got); err != nil {
		t.Fatalf("get RVRS: %v", err)
	}
	return got
}

func reconcileRVRS(t *testing.T, cl client.Client, scheme *runtime.Scheme) {
	t.Helper()
	r := NewReconciler(cl, scheme)
	if _, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: testRVRSName},
	}); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile entry point
//

func TestReconcileMissingRVRSIsNoop(t *testing.T) {
	scheme := testScheme(t)
	cl := newClient(scheme)

	r := NewReconciler(cl, scheme)
	if _, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "does-not-exist"},
	}); err != nil {
		t.Fatalf("Reconcile() on a missing RVRS should not error, got %v", err)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Normal reconcile: creating the LLVS
//

func TestReconcileNormalCreatesLLVSAndAddsFinalizer(t *testing.T) {
	scheme := testScheme(t)
	cl := newClient(scheme, makeRVRS(), makeDRBDR(testLLVName))

	reconcileRVRS(t, cl, scheme)

	got := getRVRS(t, cl)
	if !hasFinalizer(got, v1alpha1.RVRSControllerFinalizer) {
		t.Errorf("finalizer %q not added", v1alpha1.RVRSControllerFinalizer)
	}
	if got.Status.Phase != v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseInProgress {
		t.Errorf("phase = %q, want %q", got.Status.Phase, v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseInProgress)
	}
	if got.Status.ReadyToUse {
		t.Error("readyToUse = true, want false while the snapshot is being created")
	}

	llvs := &snc.LVMLogicalVolumeSnapshot{}
	if err := cl.Get(context.Background(), client.ObjectKey{Name: testRVRSName}, llvs); err != nil {
		t.Fatalf("expected an LLVS named after the RVRS: %v", err)
	}
	if llvs.Spec.LVMLogicalVolumeName != testLLVName {
		t.Errorf("LLVS source LLV = %q, want %q", llvs.Spec.LVMLogicalVolumeName, testLLVName)
	}
	if llvs.Spec.ActualSnapshotNameOnTheNode != testRVRSName {
		t.Errorf("LLVS on-node name = %q, want %q", llvs.Spec.ActualSnapshotNameOnTheNode, testRVRSName)
	}
	// The owner reference is what makes the LLVS garbage-collected with the RVRS.
	if len(llvs.OwnerReferences) != 1 {
		t.Fatalf("expected exactly one owner reference, got %d", len(llvs.OwnerReferences))
	}
	owner := llvs.OwnerReferences[0]
	if owner.Kind != "ReplicatedVolumeReplicaSnapshot" || owner.Name != testRVRSName {
		t.Errorf("owner = %s/%s, want ReplicatedVolumeReplicaSnapshot/%s", owner.Kind, owner.Name, testRVRSName)
	}
	if owner.Controller == nil || !*owner.Controller {
		t.Error("owner reference is not marked as controller")
	}
}

// The LLVS name must dodge the LVM-reserved prefixes, otherwise LVM refuses to
// create the volume.
func TestReconcileNormalUsesSafeLVMNameForLLVS(t *testing.T) {
	scheme := testScheme(t)
	const reserved = "snapshot-of-rvr-0"
	rvrs := makeRVRS(func(r *v1alpha1.ReplicatedVolumeReplicaSnapshot) { r.Name = reserved })
	cl := newClient(scheme, rvrs, makeDRBDR(testLLVName))

	r := NewReconciler(cl, scheme)
	if _, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: reserved},
	}); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	want := safeLVMName(reserved)
	if want == reserved {
		t.Fatalf("test setup: %q is expected to need escaping", reserved)
	}
	if err := cl.Get(context.Background(), client.ObjectKey{Name: want}, &snc.LVMLogicalVolumeSnapshot{}); err != nil {
		t.Fatalf("expected LLVS named %q: %v", want, err)
	}
}

func TestReconcileNormalFailsWhenLLVNameUnresolvable(t *testing.T) {
	tests := []struct {
		name string
		objs []client.Object
	}{
		{
			name: "DRBDResource missing",
			objs: []client.Object{makeRVRS()},
		},
		{
			name: "DRBDResource has no backing LLV",
			objs: []client.Object{makeRVRS(), makeDRBDR("")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := testScheme(t)
			cl := newClient(scheme, tt.objs...)

			reconcileRVRS(t, cl, scheme)

			got := getRVRS(t, cl)
			if got.Status.Phase != v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseFailed {
				t.Errorf("phase = %q, want %q", got.Status.Phase, v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseFailed)
			}
			if got.Status.ReadyToUse {
				t.Error("readyToUse = true, want false")
			}
			if !strings.Contains(got.Status.Message, "Failed to resolve LLV name") {
				t.Errorf("message = %q, want it to mention the unresolved LLV name", got.Status.Message)
			}
			// No LLVS may be created when we do not know the source volume.
			list := &snc.LVMLogicalVolumeSnapshotList{}
			if err := cl.List(context.Background(), list); err != nil {
				t.Fatalf("list LLVS: %v", err)
			}
			if len(list.Items) != 0 {
				t.Errorf("expected no LLVS, got %d", len(list.Items))
			}
		})
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Normal reconcile: mirroring the LLVS phase
//

func TestReconcileNormalMirrorsLLVSPhase(t *testing.T) {
	tests := []struct {
		name           string
		llvsStatus     *snc.LVMLogicalVolumeSnapshotStatus
		wantPhase      v1alpha1.ReplicatedVolumeReplicaSnapshotPhase
		wantReadyToUse bool
		wantHandle     string
		wantMessage    string
	}{
		{
			name:        "status not populated yet",
			llvsStatus:  nil,
			wantPhase:   v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseInProgress,
			wantMessage: "Waiting for snapshot to be created",
		},
		{
			name:        "phase pending",
			llvsStatus:  &snc.LVMLogicalVolumeSnapshotStatus{Phase: snc.PhasePending},
			wantPhase:   v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseInProgress,
			wantMessage: "Waiting for snapshot to be created",
		},
		{
			name:           "phase created",
			llvsStatus:     &snc.LVMLogicalVolumeSnapshotStatus{Phase: snc.PhaseCreated, ActualVGNameOnTheNode: "vg-1"},
			wantPhase:      v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseReady,
			wantReadyToUse: true,
			wantHandle:     testRVRSName,
			wantMessage:    "Snapshot created successfully",
		},
		{
			name:        "phase failed carries the LLVS reason",
			llvsStatus:  &snc.LVMLogicalVolumeSnapshotStatus{Phase: snc.PhaseFailed, Reason: "thin pool is out of space"},
			wantPhase:   v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseFailed,
			wantMessage: "thin pool is out of space",
		},
		{
			name:        "phase failed without a reason falls back to a generic message",
			llvsStatus:  &snc.LVMLogicalVolumeSnapshotStatus{Phase: snc.PhaseFailed},
			wantPhase:   v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseFailed,
			wantMessage: "Snapshot creation failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := testScheme(t)
			cl := newClient(scheme,
				makeRVRS(),
				makeDRBDR(testLLVName),
				makeLLVS(testRVRSName, tt.llvsStatus),
			)

			reconcileRVRS(t, cl, scheme)

			got := getRVRS(t, cl)
			if got.Status.Phase != tt.wantPhase {
				t.Errorf("phase = %q, want %q", got.Status.Phase, tt.wantPhase)
			}
			if got.Status.ReadyToUse != tt.wantReadyToUse {
				t.Errorf("readyToUse = %v, want %v", got.Status.ReadyToUse, tt.wantReadyToUse)
			}
			if got.Status.SnapshotHandle != tt.wantHandle {
				t.Errorf("snapshotHandle = %q, want %q", got.Status.SnapshotHandle, tt.wantHandle)
			}
			if got.Status.Message != tt.wantMessage {
				t.Errorf("message = %q, want %q", got.Status.Message, tt.wantMessage)
			}
			// creationTime is stamped exactly when the snapshot becomes usable.
			if tt.wantReadyToUse && got.Status.CreationTime == nil {
				t.Error("creationTime not set for a ready snapshot")
			}
			if !tt.wantReadyToUse && got.Status.CreationTime != nil {
				t.Errorf("creationTime = %v, want unset while not ready", got.Status.CreationTime)
			}
		})
	}
}

// A second reconcile of a ready snapshot must not restamp creationTime.
func TestReconcileNormalKeepsCreationTimeStable(t *testing.T) {
	scheme := testScheme(t)
	cl := newClient(scheme,
		makeRVRS(),
		makeDRBDR(testLLVName),
		makeLLVS(testRVRSName, &snc.LVMLogicalVolumeSnapshotStatus{Phase: snc.PhaseCreated}),
	)

	reconcileRVRS(t, cl, scheme)
	first := getRVRS(t, cl).Status.CreationTime
	if first == nil {
		t.Fatal("creationTime not set after the first reconcile")
	}

	reconcileRVRS(t, cl, scheme)
	second := getRVRS(t, cl).Status.CreationTime
	if second == nil || !second.Equal(first) {
		t.Errorf("creationTime changed across reconciles: %v -> %v", first, second)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Deletion
//

func TestReconcileDeleteRemovesLLVSThenFinalizer(t *testing.T) {
	scheme := testScheme(t)
	now := metav1.Now()
	rvrs := makeRVRS(func(r *v1alpha1.ReplicatedVolumeReplicaSnapshot) {
		r.Finalizers = []string{v1alpha1.RVRSControllerFinalizer}
		r.DeletionTimestamp = &now
		r.Status.SnapshotHandle = testRVRSName
		r.Status.Phase = v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseReady
		r.Status.ReadyToUse = true
	})
	cl := newClient(scheme,
		rvrs,
		makeLLVS(testRVRSName, &snc.LVMLogicalVolumeSnapshotStatus{Phase: snc.PhaseCreated}),
	)

	// First pass: the LLVS is deleted and the RVRS reports Deleting. The
	// finalizer must be held until the LLVS is actually gone.
	reconcileRVRS(t, cl, scheme)

	if err := cl.Get(context.Background(), client.ObjectKey{Name: testRVRSName}, &snc.LVMLogicalVolumeSnapshot{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected the LLVS to be deleted, got err: %v", err)
	}
	got := getRVRS(t, cl)
	if got.Status.Phase != v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseDeleting {
		t.Errorf("phase = %q, want %q", got.Status.Phase, v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseDeleting)
	}
	if got.Status.ReadyToUse {
		t.Error("readyToUse = true, want false once deletion started")
	}
	if !hasFinalizer(got, v1alpha1.RVRSControllerFinalizer) {
		t.Fatal("finalizer released before the LLVS was gone")
	}

	// Second pass: the LLVS is gone, so the finalizer is released and the object
	// disappears from the API.
	reconcileRVRS(t, cl, scheme)

	err := cl.Get(context.Background(), client.ObjectKey{Name: testRVRSName}, &v1alpha1.ReplicatedVolumeReplicaSnapshot{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected the RVRS to be gone after the finalizer was released, got err: %v", err)
	}
}

// Deleting an RVRS that never produced a snapshot must not hang: there is no
// LLVS to remove, so the finalizer goes immediately.
func TestReconcileDeleteWithoutSnapshotHandleReleasesFinalizer(t *testing.T) {
	scheme := testScheme(t)
	now := metav1.Now()
	rvrs := makeRVRS(func(r *v1alpha1.ReplicatedVolumeReplicaSnapshot) {
		r.Finalizers = []string{v1alpha1.RVRSControllerFinalizer}
		r.DeletionTimestamp = &now
		r.Status.Phase = v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseInProgress
	})
	cl := newClient(scheme, rvrs)

	reconcileRVRS(t, cl, scheme)

	err := cl.Get(context.Background(), client.ObjectKey{Name: testRVRSName}, &v1alpha1.ReplicatedVolumeReplicaSnapshot{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected the RVRS to be gone, got err: %v", err)
	}
}

// A stale snapshotHandle pointing at an already-removed LLVS must also release
// the finalizer rather than wedge deletion.
func TestReconcileDeleteWithStaleSnapshotHandleReleasesFinalizer(t *testing.T) {
	scheme := testScheme(t)
	now := metav1.Now()
	rvrs := makeRVRS(func(r *v1alpha1.ReplicatedVolumeReplicaSnapshot) {
		r.Finalizers = []string{v1alpha1.RVRSControllerFinalizer}
		r.DeletionTimestamp = &now
		r.Status.SnapshotHandle = "llvs-already-gone"
	})
	cl := newClient(scheme, rvrs)

	reconcileRVRS(t, cl, scheme)

	err := cl.Get(context.Background(), client.ObjectKey{Name: testRVRSName}, &v1alpha1.ReplicatedVolumeReplicaSnapshot{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected the RVRS to be gone, got err: %v", err)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Naming
//

func TestSafeLVMName(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "snap-1-rvr-0", want: "snap-1-rvr-0"},
		{in: "pvc-abc-snap", want: "pvc-abc-snap"},
		{in: "snapshot-1", want: "sds-snapshot-1"},
		{in: "pvmove-1", want: "sds-pvmove-1"},
		// The prefix rule is anchored: a reserved word in the middle is fine.
		{in: "my-snapshot-1", want: "my-snapshot-1"},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := safeLVMName(tt.in); got != tt.want {
				t.Errorf("safeLVMName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func hasFinalizer(obj client.Object, finalizer string) bool {
	for _, f := range obj.GetFinalizers() {
		if f == finalizer {
			return true
		}
	}
	return false
}
