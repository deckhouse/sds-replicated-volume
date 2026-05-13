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

package drbdrop

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/controllers/drbdr"
	"github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdutils"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

// AdminLockFinalizer is set on a DRBDResourceOperation of type LockAdmin
// while the cluster-wide DRBD admin_lock is held by this agent. It is
// removed only after `drbdsetup unlock` completes (or kernel reports the
// lock is already not ours). This couples the lock lifetime to the
// DRBDResourceOperation object lifetime: cascade deletion of the parent
// orchestrator (e.g. ReplicatedVolumeSnapshot) automatically releases the
// lock through the finalizer hook.
const AdminLockFinalizer = "drbd.deckhouse.io/admin-lock"

// reconcileLockAdmin drives the LockAdmin lifecycle.
//
// State machine:
//   - !deletionTimestamp + Phase in {empty, Pending}:
//     ensure finalizer is set, then ExecuteLock; on success -> Phase=Running,
//     on error -> Phase=Failed.
//   - !deletionTimestamp + Phase=Running: no-op (we already hold the lock).
//   - deletionTimestamp + finalizer + Phase=Running: ExecuteUnlock with
//     expected-holder-node-id=our DRBD node id (defense-in-depth against
//     accidentally releasing a lock that someone else has re-acquired after
//     a force-unlock); ErrNotLockHolder / ErrLockNotHeld are treated as
//     "already released" and finalizer is removed.
//   - deletionTimestamp + finalizer + Phase != Running: lock was never
//     acquired (Pending/Failed) - just remove the finalizer.
//
// The finalizer is added BEFORE ExecuteLock so that even if the agent pod
// crashes between a successful kernel-side acquire and the status patch,
// the next reconcile (or a restarted pod) sees the finalizer and re-issues
// ExecuteLock idempotently.
func (r *OperationReconciler) reconcileLockAdmin(ctx context.Context, op *v1alpha1.DRBDResourceOperation) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "LockAdmin")
	defer rf.OnEnd(&outcome)

	dr, err := r.getDRBDResourceForOperation(ctx, op)
	if err != nil {
		return rf.Fail(err)
	}
	if dr == nil {
		return rf.Done()
	}

	if op.DeletionTimestamp != nil {
		return r.handleLockAdminDeletion(ctx, op, dr, rf)
	}

	if !hasFinalizer(op, AdminLockFinalizer) {
		if err := r.addFinalizer(ctx, op, AdminLockFinalizer); err != nil {
			return rf.Fail(fmt.Errorf("adding finalizer: %w", err))
		}
	}

	if op.Status.Phase == v1alpha1.DRBDOperationPhaseRunning {
		return rf.Done()
	}

	resName := drbdr.DRBDResourceNameOnTheNode(dr)
	log.FromContext(ctx).Info("admin_lock: acquiring", "resource", resName, "nodeID", dr.Spec.NodeID)

	if err := drbdutils.ExecuteLock(ctx, resName); err != nil {
		if patchErr := r.failOperationAndPatch(ctx, op, fmt.Sprintf("ExecuteLock: %v", err)); patchErr != nil {
			return rf.Fail(patchErr)
		}
		return rf.Done()
	}

	base := op.DeepCopy()
	runOutcome := ensureOperationStatusRunning(ctx, op, metav1.NewTime(time.Now()))
	if runOutcome.Error() != nil {
		return rf.Fail(runOutcome.Error())
	}
	if runOutcome.DidChange() {
		if err := r.patchOperationStatus(ctx, op, base); err != nil {
			return rf.Fail(err)
		}
	}

	log.FromContext(ctx).Info("admin_lock: acquired", "resource", resName, "nodeID", dr.Spec.NodeID)
	return rf.Done()
}

// handleLockAdminDeletion runs the finalizer hook for a LockAdmin DRBDOp
// that is being deleted. It performs ExecuteUnlock (only if the lock was
// ever acquired - i.e. Phase==Running) and then removes the finalizer.
func (r *OperationReconciler) handleLockAdminDeletion(
	ctx context.Context,
	op *v1alpha1.DRBDResourceOperation,
	dr *v1alpha1.DRBDResource,
	rf flow.ReconcileFlow,
) (outcome flow.ReconcileOutcome) {
	if !hasFinalizer(op, AdminLockFinalizer) {
		return rf.Done()
	}

	if op.Status.Phase == v1alpha1.DRBDOperationPhaseRunning {
		resName := drbdr.DRBDResourceNameOnTheNode(dr)
		nodeID := int8(dr.Spec.NodeID)
		log.FromContext(ctx).Info("admin_lock: releasing on finalizer", "resource", resName, "nodeID", nodeID)

		if err := drbdutils.ExecuteUnlock(ctx, resName, &nodeID, nil); err != nil {
			if !errors.Is(err, drbdutils.ErrNotLockHolder) &&
				!errors.Is(err, drbdutils.ErrLockNotHeld) {
				return rf.Fail(fmt.Errorf("ExecuteUnlock: %w", err))
			}
			log.FromContext(ctx).Info("admin_lock: unlock idempotent (lock not ours / not held)",
				"resource", resName, "reason", err.Error())
		} else {
			log.FromContext(ctx).Info("admin_lock: released on finalizer", "resource", resName)
		}
	}

	if err := r.removeFinalizer(ctx, op, AdminLockFinalizer); err != nil {
		return rf.Fail(fmt.Errorf("removing finalizer: %w", err))
	}
	return rf.Done()
}

// reconcileForceUnlockAdmin runs DRBD_ADM_FORCE_UNLOCK once. Operator
// escape hatch only - clears local admin_lock state without TWOPC. Always
// transitions to Succeeded on a non-error reply from drbdsetup.
func (r *OperationReconciler) reconcileForceUnlockAdmin(ctx context.Context, op *v1alpha1.DRBDResourceOperation) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "ForceUnlockAdmin")
	defer rf.OnEnd(&outcome)

	dr, err := r.getDRBDResourceForOperation(ctx, op)
	if err != nil {
		return rf.Fail(err)
	}
	if dr == nil {
		return rf.Done()
	}

	base := op.DeepCopy()
	runOutcome := ensureOperationStatusRunning(ctx, op, metav1.NewTime(time.Now()))
	if runOutcome.Error() != nil {
		return rf.Fail(runOutcome.Error())
	}
	if runOutcome.DidChange() {
		if err := r.patchOperationStatus(ctx, op, base); err != nil {
			return rf.Fail(err)
		}
	}

	resName := drbdr.DRBDResourceNameOnTheNode(dr)
	log.FromContext(ctx).Info("admin_lock: force-unlocking", "resource", resName)

	if err := drbdutils.ExecuteForceUnlock(ctx, resName); err != nil {
		if patchErr := r.failOperationAndPatch(ctx, op, fmt.Sprintf("ExecuteForceUnlock: %v", err)); patchErr != nil {
			return rf.Fail(patchErr)
		}
		return rf.Done()
	}

	if err := r.succeedOperationAndPatch(ctx, op); err != nil {
		return rf.Fail(err)
	}
	log.FromContext(ctx).Info("admin_lock: force-unlocked", "resource", resName)
	return rf.Done()
}

func hasFinalizer(op *v1alpha1.DRBDResourceOperation, name string) bool {
	return slices.Contains(op.Finalizers, name)
}

func (r *OperationReconciler) addFinalizer(ctx context.Context, op *v1alpha1.DRBDResourceOperation, name string) error {
	base := op.DeepCopy()
	op.Finalizers = append(op.Finalizers, name)
	return r.cl.Patch(ctx, op, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
}

func (r *OperationReconciler) removeFinalizer(ctx context.Context, op *v1alpha1.DRBDResourceOperation, name string) error {
	base := op.DeepCopy()
	op.Finalizers = slices.DeleteFunc(op.Finalizers, func(f string) bool { return f == name })
	return r.cl.Patch(ctx, op, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
}
