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

package drbdrop_test

import (
	"context"
	"slices"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/controllers/drbdrop"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/scheme"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdutils"
	drbdutilsfake "github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdutils/fake"
)

const (
	testLockNodeID uint8 = 3
)

func lockArgs() []string         { return []string{"lock", testDRBDRNameOnNode} }
func forceUnlockArgs() []string  { return []string{"force-unlock", testDRBDRNameOnNode} }
func unlockArgsExpHolder() []string {
	return []string{"unlock", testDRBDRNameOnNode, "--expected-holder-node-id=3"}
}

func newLockTestDRBDR() *v1alpha1.DRBDResource {
	return &v1alpha1.DRBDResource{
		ObjectMeta: metav1.ObjectMeta{Name: testDRBDRName},
		Spec: v1alpha1.DRBDResourceSpec{
			NodeName: testNodeName,
			NodeID:   testLockNodeID,
			State:    v1alpha1.DRBDResourceStateUp,
		},
	}
}

func newLockOp() *v1alpha1.DRBDResourceOperation {
	return &v1alpha1.DRBDResourceOperation{
		ObjectMeta: metav1.ObjectMeta{Name: testDRBDROPName},
		Spec: v1alpha1.DRBDResourceOperationSpec{
			DRBDResourceName: testDRBDRName,
			Type:             v1alpha1.DRBDResourceOperationLockAdmin,
		},
	}
}

func newForceUnlockOp() *v1alpha1.DRBDResourceOperation {
	return &v1alpha1.DRBDResourceOperation{
		ObjectMeta: metav1.ObjectMeta{Name: testDRBDROPName},
		Spec: v1alpha1.DRBDResourceOperationSpec{
			DRBDResourceName: testDRBDRName,
			Type:             v1alpha1.DRBDResourceOperationForceUnlockAdmin,
		},
	}
}

func newLockTestClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	sch, err := scheme.New()
	if err != nil {
		t.Fatal(err)
	}
	return crfake.NewClientBuilder().
		WithScheme(sch).
		WithObjects(objs...).
		WithStatusSubresource(&v1alpha1.DRBDResourceOperation{}).
		Build()
}

func reconcileOnce(t *testing.T, cl client.Client, opName string) {
	t.Helper()
	rec := drbdrop.NewOperationReconciler(cl, testNodeName)
	if _, err := rec.Reconcile(t.Context(), reconcile.Request{
		NamespacedName: client.ObjectKey{Name: opName},
	}); err != nil {
		t.Fatalf("unexpected reconcile error: %v", err)
	}
}

func mustGetOp(t *testing.T, cl client.Client, name string) *v1alpha1.DRBDResourceOperation {
	t.Helper()
	got := &v1alpha1.DRBDResourceOperation{}
	if err := cl.Get(t.Context(), client.ObjectKey{Name: name}, got); err != nil {
		t.Fatalf("get op %q failed: %v", name, err)
	}
	return got
}

// patchOpStatusToRunning forces .status.phase=Running on the freshly-created op
// (mirrors what reconcileLockAdmin would do after a successful acquire).
func patchOpStatusToRunning(t *testing.T, cl client.Client, name string) {
	t.Helper()
	op := mustGetOp(t, cl, name)
	base := op.DeepCopy()
	op.Status.Phase = v1alpha1.DRBDOperationPhaseRunning
	now := metav1.Now()
	op.Status.StartedAt = &now
	if err := cl.Status().Patch(t.Context(), op, client.MergeFrom(base)); err != nil {
		t.Fatalf("patch status: %v", err)
	}
}

// markOpDeleting issues a Delete on the op (with a finalizer it will not be
// removed; only DeletionTimestamp is set).
func markOpDeleting(t *testing.T, cl client.Client, name string) {
	t.Helper()
	op := mustGetOp(t, cl, name)
	if err := cl.Delete(t.Context(), op); err != nil {
		t.Fatalf("delete op: %v", err)
	}
}

// ─── reconcileLockAdmin: acquire path ────────────────────────────────────────

func TestReconcileLockAdmin_AcquiresAndAddsFinalizer(t *testing.T) {
	cl := newLockTestClient(t, newLockTestDRBDR(), newLockOp())

	exec := &drbdutilsfake.Exec{}
	exec.ExpectCommands(&drbdutilsfake.ExpectedCmd{Name: "drbdsetup", Args: lockArgs()})
	exec.Setup(t)

	reconcileOnce(t, cl, testDRBDROPName)

	got := mustGetOp(t, cl, testDRBDROPName)
	if got.Status.Phase != v1alpha1.DRBDOperationPhaseRunning {
		t.Errorf("phase: got %q want Running (msg: %q)", got.Status.Phase, got.Status.Message)
	}
	if !slices.Contains(got.Finalizers, drbdrop.AdminLockFinalizer) {
		t.Errorf("finalizer %q missing, finalizers=%v", drbdrop.AdminLockFinalizer, got.Finalizers)
	}
	if got.Status.StartedAt == nil {
		t.Errorf("expected StartedAt to be set")
	}
}

func TestReconcileLockAdmin_NoOpWhenAlreadyRunning(t *testing.T) {
	op := newLockOp()
	op.Finalizers = []string{drbdrop.AdminLockFinalizer}
	cl := newLockTestClient(t, newLockTestDRBDR(), op)
	patchOpStatusToRunning(t, cl, testDRBDROPName)

	exec := &drbdutilsfake.Exec{}
	exec.Setup(t)

	reconcileOnce(t, cl, testDRBDROPName)

	got := mustGetOp(t, cl, testDRBDROPName)
	if got.Status.Phase != v1alpha1.DRBDOperationPhaseRunning {
		t.Errorf("phase: got %q want Running", got.Status.Phase)
	}
	if !slices.Contains(got.Finalizers, drbdrop.AdminLockFinalizer) {
		t.Errorf("finalizer must remain")
	}
}

func TestReconcileLockAdmin_AcquireFailure_PhaseFailedFinalizerRemains(t *testing.T) {
	cl := newLockTestClient(t, newLockTestDRBDR(), newLockOp())

	exec := &drbdutilsfake.Exec{}
	exec.ExpectCommands(&drbdutilsfake.ExpectedCmd{
		Name:         "drbdsetup",
		Args:         lockArgs(),
		ResultOutput: []byte("Failure: (178) admin_lock acquire timeout"),
		ResultErr:    drbdutilsfake.ExitErr{Code: 10},
	})
	exec.Setup(t)

	reconcileOnce(t, cl, testDRBDROPName)

	got := mustGetOp(t, cl, testDRBDROPName)
	if got.Status.Phase != v1alpha1.DRBDOperationPhaseFailed {
		t.Errorf("phase: got %q want Failed", got.Status.Phase)
	}
	if got.Status.Message == "" || got.Status.Message[:11] != "ExecuteLock" {
		t.Errorf("expected message starting with %q, got %q", "ExecuteLock", got.Status.Message)
	}
	if !slices.Contains(got.Finalizers, drbdrop.AdminLockFinalizer) {
		t.Errorf("finalizer must remain after acquire failure (cleanup happens on delete), finalizers=%v", got.Finalizers)
	}
}

// ─── reconcileLockAdmin: deletion / finalizer path ──────────────────────────

func TestReconcileLockAdmin_DeletionInRunning_ReleasesAndRemovesFinalizer(t *testing.T) {
	op := newLockOp()
	op.Finalizers = []string{drbdrop.AdminLockFinalizer}
	cl := newLockTestClient(t, newLockTestDRBDR(), op)
	patchOpStatusToRunning(t, cl, testDRBDROPName)
	markOpDeleting(t, cl, testDRBDROPName)

	exec := &drbdutilsfake.Exec{}
	exec.ExpectCommands(&drbdutilsfake.ExpectedCmd{Name: "drbdsetup", Args: unlockArgsExpHolder()})
	exec.Setup(t)

	reconcileOnce(t, cl, testDRBDROPName)

	got := &v1alpha1.DRBDResourceOperation{}
	err := cl.Get(t.Context(), client.ObjectKey{Name: testDRBDROPName}, got)
	if err == nil {
		if slices.Contains(got.Finalizers, drbdrop.AdminLockFinalizer) {
			t.Errorf("finalizer still present after deletion reconcile: %v", got.Finalizers)
		}
		return
	}
	if !apierrors.IsNotFound(err) {
		t.Fatalf("unexpected get error: %v", err)
	}
}

func TestReconcileLockAdmin_DeletionUnlockNotHolder_StillRemovesFinalizer(t *testing.T) {
	op := newLockOp()
	op.Finalizers = []string{drbdrop.AdminLockFinalizer}
	cl := newLockTestClient(t, newLockTestDRBDR(), op)
	patchOpStatusToRunning(t, cl, testDRBDROPName)
	markOpDeleting(t, cl, testDRBDROPName)

	exec := &drbdutilsfake.Exec{}
	exec.ExpectCommands(&drbdutilsfake.ExpectedCmd{
		Name:         "drbdsetup",
		Args:         unlockArgsExpHolder(),
		ResultOutput: []byte("Failure: (177) holder mismatch"),
		ResultErr:    drbdutilsfake.ExitErr{Code: 10},
	})
	exec.Setup(t)

	reconcileOnce(t, cl, testDRBDROPName)

	got := &v1alpha1.DRBDResourceOperation{}
	err := cl.Get(t.Context(), client.ObjectKey{Name: testDRBDROPName}, got)
	if err != nil && !apierrors.IsNotFound(err) {
		t.Fatalf("unexpected get error: %v", err)
	}
	if err == nil && slices.Contains(got.Finalizers, drbdrop.AdminLockFinalizer) {
		t.Errorf("finalizer must be removed on ErrNotLockHolder, got finalizers=%v", got.Finalizers)
	}
}

func TestReconcileLockAdmin_DeletionUnlockNotHeld_StillRemovesFinalizer(t *testing.T) {
	op := newLockOp()
	op.Finalizers = []string{drbdrop.AdminLockFinalizer}
	cl := newLockTestClient(t, newLockTestDRBDR(), op)
	patchOpStatusToRunning(t, cl, testDRBDROPName)
	markOpDeleting(t, cl, testDRBDROPName)

	exec := &drbdutilsfake.Exec{}
	exec.ExpectCommands(&drbdutilsfake.ExpectedCmd{
		Name:         "drbdsetup",
		Args:         unlockArgsExpHolder(),
		ResultOutput: []byte("Failure: (179) admin_lock not held"),
		ResultErr:    drbdutilsfake.ExitErr{Code: 10},
	})
	exec.Setup(t)

	reconcileOnce(t, cl, testDRBDROPName)

	got := &v1alpha1.DRBDResourceOperation{}
	err := cl.Get(t.Context(), client.ObjectKey{Name: testDRBDROPName}, got)
	if err != nil && !apierrors.IsNotFound(err) {
		t.Fatalf("unexpected get error: %v", err)
	}
	if err == nil && slices.Contains(got.Finalizers, drbdrop.AdminLockFinalizer) {
		t.Errorf("finalizer must be removed on ErrLockNotHeld, got finalizers=%v", got.Finalizers)
	}
}

func TestReconcileLockAdmin_DeletionInFailed_NoUnlockButRemovesFinalizer(t *testing.T) {
	op := newLockOp()
	op.Finalizers = []string{drbdrop.AdminLockFinalizer}
	cl := newLockTestClient(t, newLockTestDRBDR(), op)

	cur := mustGetOp(t, cl, testDRBDROPName)
	base := cur.DeepCopy()
	cur.Status.Phase = v1alpha1.DRBDOperationPhaseFailed
	cur.Status.Message = "previous acquire failed"
	if err := cl.Status().Patch(t.Context(), cur, client.MergeFrom(base)); err != nil {
		t.Fatalf("patch status: %v", err)
	}
	markOpDeleting(t, cl, testDRBDROPName)

	exec := &drbdutilsfake.Exec{}
	exec.Setup(t)

	reconcileOnce(t, cl, testDRBDROPName)

	got := &v1alpha1.DRBDResourceOperation{}
	err := cl.Get(t.Context(), client.ObjectKey{Name: testDRBDROPName}, got)
	if err != nil && !apierrors.IsNotFound(err) {
		t.Fatalf("unexpected get error: %v", err)
	}
	if err == nil && slices.Contains(got.Finalizers, drbdrop.AdminLockFinalizer) {
		t.Errorf("finalizer must be removed even when no acquire happened, got finalizers=%v", got.Finalizers)
	}
}

// ─── reconcileLockAdmin: foreign node ────────────────────────────────────────

func TestReconcileLockAdmin_OtherNode_NoOp(t *testing.T) {
	dr := newLockTestDRBDR()
	dr.Spec.NodeName = "some-other-node"
	cl := newLockTestClient(t, dr, newLockOp())

	exec := &drbdutilsfake.Exec{}
	exec.Setup(t)

	reconcileOnce(t, cl, testDRBDROPName)

	got := mustGetOp(t, cl, testDRBDROPName)
	if got.Status.Phase != "" {
		t.Errorf("phase must remain empty on a foreign-node DRBDR, got %q", got.Status.Phase)
	}
	if len(got.Finalizers) != 0 {
		t.Errorf("no finalizers expected on a foreign-node DRBDR, got %v", got.Finalizers)
	}
}

// ─── reconcileForceUnlockAdmin ───────────────────────────────────────────────

func TestReconcileForceUnlockAdmin_Succeeds(t *testing.T) {
	cl := newLockTestClient(t, newLockTestDRBDR(), newForceUnlockOp())

	exec := &drbdutilsfake.Exec{}
	exec.ExpectCommands(&drbdutilsfake.ExpectedCmd{Name: "drbdsetup", Args: forceUnlockArgs()})
	exec.Setup(t)

	reconcileOnce(t, cl, testDRBDROPName)

	got := mustGetOp(t, cl, testDRBDROPName)
	if got.Status.Phase != v1alpha1.DRBDOperationPhaseSucceeded {
		t.Errorf("phase: got %q want Succeeded (msg: %q)", got.Status.Phase, got.Status.Message)
	}
	if slices.Contains(got.Finalizers, drbdrop.AdminLockFinalizer) {
		t.Errorf("ForceUnlockAdmin must not add admin-lock finalizer, finalizers=%v", got.Finalizers)
	}
}

func TestReconcileForceUnlockAdmin_Failure_PhaseFailed(t *testing.T) {
	cl := newLockTestClient(t, newLockTestDRBDR(), newForceUnlockOp())

	exec := &drbdutilsfake.Exec{}
	exec.ExpectCommands(&drbdutilsfake.ExpectedCmd{
		Name:         "drbdsetup",
		Args:         forceUnlockArgs(),
		ResultOutput: []byte("Failure: (158) resource not found"),
		ResultErr:    drbdutilsfake.ExitErr{Code: 10},
	})
	exec.Setup(t)

	reconcileOnce(t, cl, testDRBDROPName)

	got := mustGetOp(t, cl, testDRBDROPName)
	if got.Status.Phase != v1alpha1.DRBDOperationPhaseFailed {
		t.Errorf("phase: got %q want Failed", got.Status.Phase)
	}
}

// ─── ExecuteUnlock signature smoke (compile-time guard) ─────────────────────

// _ keeps drbdutils imported even if this file's only Execute*-related ref is
// the package-level ErrNotLockHolder/ErrLockNotHeld used in error matchers.
var _ = drbdutils.ErrNotLockHolder
var _ = context.Background
