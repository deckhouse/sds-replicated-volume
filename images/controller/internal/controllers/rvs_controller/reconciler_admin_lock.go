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
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

const (
	adminLockOpNameSuffix = "-admin-lock"

	adminLockAcquireRequeueAfter = 5 * time.Second

	adminLockFailedRequeueAfter = 30 * time.Second
)

// reconcileAcquireAdminLock ensures the cluster-wide DRBD admin lock is held
// for the lifetime of this RVS while its prepare/sync flow runs.
//
// Reconcile pattern: Pure orchestration.
//
// Lifecycle:
//   - DRBDResourceOperation of type LockAdmin is created with an ownerReference
//     to the RVS. The agent on the holder node acquires the kernel lock, sets
//     an AdminLockFinalizer, and reports Phase=Running.
//   - The lock is released via reconcileReleaseAdminLock (which deletes the op;
//     the agent's finalizer hook then runs `drbdsetup unlock`).
//   - Cascade GC of the RVS (when no other finalizers are pending) also deletes
//     the op as a backstop — the same finalizer hook releases the lock.
//
// The op name is `<rvs.Name>-admin-lock` so re-creates are idempotent (a
// restarted controller pod observes the existing op instead of creating a
// duplicate). Outcomes:
//   - Phase=Running              -> Continue (lock acquired, caller may proceed).
//   - Phase=Pending or empty     -> DoneAndRequeueAfter (wait for the agent).
//   - Phase=Failed               -> delete op so the next reconcile creates a fresh
//     one; this implements a tight retry loop for transient failures
//     (e.g. ErrLockHeld while another RVS holds the cluster-wide lock).
//   - op missing                 -> create it; then DoneAndRequeueAfter.
func (r *Reconciler) reconcileAcquireAdminLock(
	ctx context.Context,
	rvs *v1alpha1.ReplicatedVolumeSnapshot,
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "acquire-admin-lock")
	defer rf.OnEnd(&outcome)

	op, err := r.getAdminLockOp(rf.Ctx(), rvs)
	if err != nil {
		return rf.Fail(err)
	}

	if op != nil {
		switch op.Status.Phase {
		case v1alpha1.DRBDOperationPhaseRunning:
			if condOutcome := r.reconcileAdminLockCondition(rf.Ctx(), rvs,
				metav1.ConditionTrue,
				v1alpha1.ReplicatedVolumeSnapshotCondAdminLockedReasonAcquired,
				fmt.Sprintf("Admin lock acquired via op %q", op.Name),
			); condOutcome.ShouldReturn() {
				return condOutcome
			}
			return rf.Continue()
		case v1alpha1.DRBDOperationPhaseFailed:
			if op.Status.CompletedAt != nil {
				elapsed := time.Since(op.Status.CompletedAt.Time)
				if elapsed < adminLockFailedRequeueAfter {
					remaining := adminLockFailedRequeueAfter - elapsed
					if condOutcome := r.reconcileAdminLockCondition(rf.Ctx(), rvs,
						metav1.ConditionFalse,
						v1alpha1.ReplicatedVolumeSnapshotCondAdminLockedReasonRetryingTransient,
						fmt.Sprintf("Admin lock op failed (%q), backoff %s before retry", op.Status.Message, remaining.Round(time.Second)),
					); condOutcome.ShouldReturn() {
						return condOutcome
					}
					return rf.DoneAndRequeueAfter(remaining)
				}
			}
			rf.Log().Info("admin-lock: op Failed, deleting to retry",
				"op", op.Name, "message", op.Status.Message)
			if err := r.deleteAdminLockOp(rf.Ctx(), op); err != nil && !apierrors.IsNotFound(err) {
				return rf.Fail(err)
			}
			if condOutcome := r.reconcileAdminLockCondition(rf.Ctx(), rvs,
				metav1.ConditionFalse,
				v1alpha1.ReplicatedVolumeSnapshotCondAdminLockedReasonRetryingTransient,
				fmt.Sprintf("Admin lock op failed (%q), retrying", op.Status.Message),
			); condOutcome.ShouldReturn() {
				return condOutcome
			}
			return rf.DoneAndRequeueAfter(adminLockFailedRequeueAfter)
		default:
			if condOutcome := r.reconcileAdminLockCondition(rf.Ctx(), rvs,
				metav1.ConditionFalse,
				v1alpha1.ReplicatedVolumeSnapshotCondAdminLockedReasonAcquiring,
				fmt.Sprintf("Waiting for the agent to acquire the admin lock via op %q", op.Name),
			); condOutcome.ShouldReturn() {
				return condOutcome
			}
			return rf.DoneAndRequeueAfter(adminLockAcquireRequeueAfter)
		}
	}

	holder := computeTargetAdminLockHolder(rv, rvrs)
	if holder == nil {
		if condOutcome := r.reconcileAdminLockCondition(rf.Ctx(), rvs,
			metav1.ConditionFalse,
			v1alpha1.ReplicatedVolumeSnapshotCondAdminLockedReasonNoHolder,
			"No diskful member in the datamesh yet to host the admin lock",
		); condOutcome.ShouldReturn() {
			return condOutcome
		}
		rf.Log().Info("admin-lock: no holder candidate yet (no diskful RVR in datamesh)",
			"rvs", rvs.Name, "rv", rv.Name)
		return rf.DoneAndRequeueAfter(adminLockAcquireRequeueAfter)
	}

	holderDR, err := r.getDRBDR(rf.Ctx(), holder.Name)
	if err != nil {
		return rf.Fail(err)
	}
	if holderDR == nil || holderDR.Spec.State != v1alpha1.DRBDResourceStateUp {
		state := v1alpha1.DRBDResourceState("")
		if holderDR != nil {
			state = holderDR.Spec.State
		}
		if condOutcome := r.reconcileAdminLockCondition(rf.Ctx(), rvs,
			metav1.ConditionFalse,
			v1alpha1.ReplicatedVolumeSnapshotCondAdminLockedReasonHolderUnready,
			fmt.Sprintf("Holder DRBDResource %q not ready (state=%q)", holder.Name, state),
		); condOutcome.ShouldReturn() {
			return condOutcome
		}
		rf.Log().Info("admin-lock: holder DRBDResource not ready, waiting",
			"holder", holder.Name, "specState", state)
		return rf.DoneAndRequeueAfter(adminLockAcquireRequeueAfter)
	}

	drbdrs := make([]*v1alpha1.DRBDResource, 0, len(rvrs))
	for _, rvr := range rvrs {
		if rvr == nil {
			continue
		}
		var peerDR *v1alpha1.DRBDResource
		if rvr.Name == holder.Name {
			peerDR = holderDR
		} else {
			peerDR, err = r.getDRBDR(rf.Ctx(), rvr.Name)
			if err != nil {
				return rf.Fail(err)
			}
		}
		if peerDR == nil {
			if condOutcome := r.reconcileAdminLockCondition(rf.Ctx(), rvs,
				metav1.ConditionFalse,
				v1alpha1.ReplicatedVolumeSnapshotCondAdminLockedReasonClusterNotReady,
				fmt.Sprintf("DRBDResource %q is missing", rvr.Name),
			); condOutcome.ShouldReturn() {
				return condOutcome
			}
			rf.Log().Info("admin-lock: peer DRBDResource not yet present, waiting",
				"rvr", rvr.Name)
			return rf.DoneAndRequeueAfter(adminLockAcquireRequeueAfter)
		}
		drbdrs = append(drbdrs, peerDR)
	}

	if ready, reason := computeAdminLockReadiness(rvrs, drbdrs); !ready {
		if condOutcome := r.reconcileAdminLockCondition(rf.Ctx(), rvs,
			metav1.ConditionFalse,
			v1alpha1.ReplicatedVolumeSnapshotCondAdminLockedReasonClusterNotReady,
			reason,
		); condOutcome.ShouldReturn() {
			return condOutcome
		}
		rf.Log().Info("admin-lock: cluster not ready yet, waiting",
			"reason", reason)
		return rf.DoneAndRequeueAfter(adminLockAcquireRequeueAfter)
	}

	if err := r.createAdminLockOp(rf.Ctx(), rvs, holder); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return rf.DoneAndRequeueAfter(adminLockAcquireRequeueAfter)
		}
		return rf.Fail(err)
	}

	if condOutcome := r.reconcileAdminLockCondition(rf.Ctx(), rvs,
		metav1.ConditionFalse,
		v1alpha1.ReplicatedVolumeSnapshotCondAdminLockedReasonAcquiring,
		fmt.Sprintf("Created LockAdmin op %q on holder %q", adminLockOpName(rvs), holder.Name),
	); condOutcome.ShouldReturn() {
		return condOutcome
	}
	rf.Log().Info("admin-lock: created LockAdmin op",
		"op", adminLockOpName(rvs), "holderRVR", holder.Name, "holderNode", holder.Spec.NodeName)
	return rf.DoneAndRequeueAfter(adminLockAcquireRequeueAfter)
}

// reconcileReleaseAdminLock deletes the LockAdmin op so the agent's finalizer
// hook releases the kernel lock. Idempotent: returns Continue when the op is
// already gone, DoneAndRequeueAfter while the agent is still draining the
// finalizer.
//
// Reconcile pattern: Pure orchestration.
func (r *Reconciler) reconcileReleaseAdminLock(
	ctx context.Context,
	rvs *v1alpha1.ReplicatedVolumeSnapshot,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "release-admin-lock")
	defer rf.OnEnd(&outcome)

	op, err := r.getAdminLockOp(rf.Ctx(), rvs)
	if err != nil {
		return rf.Fail(err)
	}
	if op == nil {
		if condOutcome := r.reconcileAdminLockCondition(rf.Ctx(), rvs,
			metav1.ConditionFalse,
			v1alpha1.ReplicatedVolumeSnapshotCondAdminLockedReasonReleased,
			"Admin lock released",
		); condOutcome.ShouldReturn() {
			return condOutcome
		}
		return rf.Continue()
	}

	wasDeleting := op.DeletionTimestamp != nil
	if err := r.deleteAdminLockOp(rf.Ctx(), op); err != nil {
		return rf.Fail(err)
	}
	if !wasDeleting {
		rf.Log().Info("admin-lock: deleted LockAdmin op, waiting for agent to release",
			"op", op.Name, "phase", op.Status.Phase)
	} else {
		rf.Log().V(1).Info("admin-lock: op deletion in progress",
			"op", op.Name, "finalizers", op.Finalizers)
	}
	if condOutcome := r.reconcileAdminLockCondition(rf.Ctx(), rvs,
		metav1.ConditionFalse,
		v1alpha1.ReplicatedVolumeSnapshotCondAdminLockedReasonReleasing,
		fmt.Sprintf("Releasing admin lock via op %q", op.Name),
	); condOutcome.ShouldReturn() {
		return condOutcome
	}
	return rf.DoneAndRequeueAfter(adminLockAcquireRequeueAfter)
}

// reconcileAdminLockCondition publishes (or updates) the AdminLocked condition
// on RVS.Status.Conditions and patches the status sub-resource only when the
// condition value actually changes (so we do not churn ResourceVersion on
// every requeue).
//
// Reconcile pattern: In-place reconciliation.
func (r *Reconciler) reconcileAdminLockCondition(
	ctx context.Context,
	rvs *v1alpha1.ReplicatedVolumeSnapshot,
	status metav1.ConditionStatus,
	reason, message string,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "admin-lock-condition")
	defer rf.OnEnd(&outcome)

	base := rvs.DeepCopy()
	if !obju.SetStatusCondition(rvs, metav1.Condition{
		Type:    v1alpha1.ReplicatedVolumeSnapshotCondAdminLockedType,
		Status:  status,
		Reason:  reason,
		Message: message,
	}) {
		return rf.Continue()
	}
	if err := r.patchRVSStatus(rf.Ctx(), rvs, base); err != nil {
		return rf.Fail(err)
	}
	return rf.Continue()
}

// ──────────────────────────────────────────────────────────────────────────────
// Single-call I/O helpers
//

func (r *Reconciler) getAdminLockOp(
	ctx context.Context,
	rvs *v1alpha1.ReplicatedVolumeSnapshot,
) (*v1alpha1.DRBDResourceOperation, error) {
	op := &v1alpha1.DRBDResourceOperation{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: adminLockOpName(rvs)}, op); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return op, nil
}

func (r *Reconciler) createAdminLockOp(
	ctx context.Context,
	rvs *v1alpha1.ReplicatedVolumeSnapshot,
	holder *v1alpha1.ReplicatedVolumeReplica,
) error {
	op := &v1alpha1.DRBDResourceOperation{
		ObjectMeta: metav1.ObjectMeta{
			Name: adminLockOpName(rvs),
		},
		Spec: v1alpha1.DRBDResourceOperationSpec{
			NodeName:         holder.Spec.NodeName,
			DRBDResourceName: holder.Name,
			Type:             v1alpha1.DRBDResourceOperationLockAdmin,
		},
	}
	if _, err := obju.SetControllerRef(op, rvs, r.scheme); err != nil {
		return fmt.Errorf("setting controllerRef on LockAdmin op: %w", err)
	}
	return r.cl.Create(ctx, op)
}

func (r *Reconciler) deleteAdminLockOp(
	ctx context.Context,
	op *v1alpha1.DRBDResourceOperation,
) error {
	if op.DeletionTimestamp != nil {
		return nil
	}
	if err := client.IgnoreNotFound(r.cl.Delete(ctx, op)); err != nil {
		return err
	}
	op.DeletionTimestamp = ptr.To(metav1.Now())
	return nil
}

func (r *Reconciler) getDRBDR(
	ctx context.Context,
	name string,
) (*v1alpha1.DRBDResource, error) {
	dr := &v1alpha1.DRBDResource{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: name}, dr); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return dr, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Pure helpers
//

// computeTargetAdminLockHolder selects the RVR whose node will hold the
// cluster-wide admin lock. Preference: the first attached diskful primary in
// rv.Status.Datamesh.Members; otherwise the first diskful RVR in rvrs that has
// a corresponding member entry. Returns nil if no diskful RVR is mapped to a
// datamesh member yet.
func computeTargetAdminLockHolder(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
) *v1alpha1.ReplicatedVolumeReplica {
	if rv == nil {
		return nil
	}
	rvrByName := make(map[string]*v1alpha1.ReplicatedVolumeReplica, len(rvrs))
	for _, rvr := range rvrs {
		if rvr == nil || rvr.Spec.Type != v1alpha1.ReplicaTypeDiskful || rvr.Spec.NodeName == "" {
			continue
		}
		rvrByName[rvr.Name] = rvr
	}

	var fallback *v1alpha1.ReplicatedVolumeReplica
	for _, m := range rv.Status.Datamesh.Members {
		rvr := rvrByName[m.Name]
		if rvr == nil {
			continue
		}
		if m.Attached {
			return rvr
		}
		if fallback == nil {
			fallback = rvr
		}
	}
	return fallback
}

func adminLockOpName(rvs *v1alpha1.ReplicatedVolumeSnapshot) string {
	return rvs.Name + adminLockOpNameSuffix
}

// computeAdminLockReadiness checks that every RVR's DRBDResource is in a
// healthy steady state — i.e. local DiskState matches its replica type
// (UpToDate for diskful, Diskless for tie-breaker) and every peer connection
// is fully Established. This is a precondition for taking the cluster-wide
// admin lock: the kernel's TWOPC (and its wait_for_local_drain timeout) will
// otherwise block on peers that are still handshaking or syncing, leading to
// long IO suspensions and likely lock-acquire timeouts.
//
// Returns (true, "") when all members are ready. Otherwise returns (false,
// "<short reason for the first non-ready member>") suitable for logs/status.
func computeAdminLockReadiness(
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	drbdrs []*v1alpha1.DRBDResource,
) (bool, string) {
	drByName := make(map[string]*v1alpha1.DRBDResource, len(drbdrs))
	for _, dr := range drbdrs {
		if dr == nil {
			continue
		}
		drByName[dr.Name] = dr
	}

	for _, rvr := range rvrs {
		if rvr == nil {
			continue
		}
		dr := drByName[rvr.Name]
		if dr == nil {
			return false, fmt.Sprintf("DRBDResource %q is missing", rvr.Name)
		}

		switch rvr.Spec.Type {
		case v1alpha1.ReplicaTypeDiskful:
			if dr.Status.DiskState != v1alpha1.DiskStateUpToDate {
				return false, fmt.Sprintf("DRBDResource %q diskState=%q (want UpToDate)",
					rvr.Name, dr.Status.DiskState)
			}
		case v1alpha1.ReplicaTypeTieBreaker:
			if dr.Status.DiskState != v1alpha1.DiskStateDiskless {
				return false, fmt.Sprintf("DRBDResource %q diskState=%q (want Diskless for TieBreaker)",
					rvr.Name, dr.Status.DiskState)
			}
		}

		for _, peer := range dr.Status.Peers {
			if peer.ConnectionState != v1alpha1.ConnectionStateConnected {
				return false, fmt.Sprintf("DRBDResource %q peer %q connectionState=%q (want Connected)",
					rvr.Name, peer.Name, peer.ConnectionState)
			}
			if peer.ReplicationState != v1alpha1.ReplicationStateEstablished {
				return false, fmt.Sprintf("DRBDResource %q peer %q replicationState=%q (want Established)",
					rvr.Name, peer.Name, peer.ReplicationState)
			}
		}
	}

	return true, ""
}
