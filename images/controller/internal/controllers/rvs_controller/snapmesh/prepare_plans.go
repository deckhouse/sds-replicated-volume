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

package snapmesh

import (
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/dmte"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/idset"
)

const (
	prepareTransitionType = v1alpha1.ReplicatedVolumeDatameshTransitionType("PrepareSnapshot")
	preparePlanID         = dmte.PlanID("prepare-snapshot/v1")
	prepareCleanupPlanID  = dmte.PlanID("prepare-cleanup/v1")
	prepareUnsafeTimeout  = 2 * time.Minute
)

func registerPreparePlans(reg *dmte.Registry[*globalContext, *replicaContext]) {
	rt := reg.ReplicaTransition(prepareTransitionType, prepareSlot)

	rt.Plan(preparePlanID).
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupSync).
		DisplayName("Preparing snapshot").
		Steps(
			dmte.GlobalStep("Track bitmap", applyPrepareStart, confirmTrackBitmap),
			dmte.GlobalStep("Create secondary snapshots", applyGlobalNoop, confirmCreateSecondarySnapshots),
			dmte.GlobalStep("Wait secondary snapshots", applyGlobalNoop, confirmWaitSecondarySnapshots),
			dmte.GlobalStep("Suspend IO", applyGlobalNoop, confirmSuspendIO),
			dmte.GlobalStep("Flush bitmap", applyGlobalNoop, confirmFlushBitmap),
			dmte.GlobalStep("Create primary snapshot", applyGlobalNoop, confirmCreatePrimarySnapshot),
			dmte.GlobalStep("Resume IO", applyGlobalNoop, confirmResumeIO),
			dmte.GlobalStep("Untrack bitmap", applyGlobalNoop, confirmUntrackBitmap),
		).
		Build()

	rt.Plan(prepareCleanupPlanID).
		CancelActiveOnCreate(true).
		Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupSync).
		DisplayName("Cleaning up snapshot prepare phase").
		Steps(
			dmte.GlobalStep("Resume IO", applyGlobalNoop, confirmResumeIO),
			dmte.GlobalStep("Untrack bitmap", applyGlobalNoop, confirmUntrackBitmap),
		).
		Build()
}

func applyPrepareStart(_ *globalContext) bool { return true }

func applyGlobalNoop(_ *globalContext) bool { return false }

func confirmTrackBitmap(gctx *globalContext, _ int64) dmte.ConfirmResult {
	primary := preparePrimaryReplica(gctx)
	must := primaryConfirmSet(primary)
	if primary == nil {
		return dmte.ConfirmResult{MustConfirm: must, Confirmed: must}
	}
	if _, pending := ensurePrepareOperation(
		gctx,
		prepareTrackBitmapOperationName(gctx),
		v1alpha1.DRBDResourceOperationSpec{
			DRBDResourceName: primary.rvrName,
			Type:             v1alpha1.DRBDResourceOperationTrackBitmap,
		},
		must,
	); pending {
		return dmte.ConfirmResult{MustConfirm: must}
	}
	return dmte.ConfirmResult{MustConfirm: must, Confirmed: must}
}

func confirmCreateSecondarySnapshots(gctx *globalContext, _ int64) dmte.ConfirmResult {
	must := prepareReplicaIDs(gctx)
	primary := preparePrimaryReplica(gctx)
	allDone := true
	for i := range gctx.allReplicas {
		rc := &gctx.allReplicas[i]
		if primary != nil && rc == primary {
			continue
		}
		if _, pending := ensurePrepareSnapshot(gctx, rc, idset.Of(rc.id)); pending {
			allDone = false
		}
	}
	if allDone {
		return dmte.ConfirmResult{MustConfirm: must, Confirmed: must}
	}
	return dmte.ConfirmResult{MustConfirm: must}
}

func confirmWaitSecondarySnapshots(gctx *globalContext, _ int64) dmte.ConfirmResult {
	must := prepareReplicaIDs(gctx)
	for i := range gctx.allReplicas {
		rc := &gctx.allReplicas[i]
		if preparePrimaryReplica(gctx) == rc {
			continue
		}
		if !prepareSnapshotReady(rc) {
			return dmte.ConfirmResult{MustConfirm: must}
		}
	}
	return dmte.ConfirmResult{MustConfirm: must, Confirmed: must}
}

func confirmSuspendIO(gctx *globalContext, _ int64) dmte.ConfirmResult {
	primary := preparePrimaryReplica(gctx)
	must := primaryConfirmSet(primary)
	if primary == nil {
		return dmte.ConfirmResult{MustConfirm: must, Confirmed: must}
	}
	if confirmed, pending := ensurePrepareOperation(
		gctx,
		preparePrimaryOperationName(gctx, "suspend-io"),
		v1alpha1.DRBDResourceOperationSpec{
			DRBDResourceName: primary.rvrName,
			Type:             v1alpha1.DRBDResourceOperationSuspendIO,
		},
		must,
	); pending {
		return dmte.ConfirmResult{MustConfirm: must}
	} else {
		return dmte.ConfirmResult{MustConfirm: must, Confirmed: confirmed}
	}
}

func confirmFlushBitmap(gctx *globalContext, _ int64) dmte.ConfirmResult {
	primary := preparePrimaryReplica(gctx)
	must := primaryConfirmSet(primary)
	if primary == nil {
		return dmte.ConfirmResult{MustConfirm: must, Confirmed: must}
	}
	if confirmed, pending := ensurePrepareOperation(
		gctx,
		preparePrimaryOperationName(gctx, "flush-bitmap"),
		v1alpha1.DRBDResourceOperationSpec{
			DRBDResourceName: primary.rvrName,
			Type:             v1alpha1.DRBDResourceOperationFlushBitmap,
		},
		must,
	); pending {
		return dmte.ConfirmResult{MustConfirm: must}
	} else {
		return dmte.ConfirmResult{MustConfirm: must, Confirmed: confirmed}
	}
}

func confirmCreatePrimarySnapshot(gctx *globalContext, _ int64) dmte.ConfirmResult {
	l := log.FromContext(gctx.ctx)
	primary := preparePrimaryReplica(gctx)
	must := primaryConfirmSet(primary)
	if primary == nil {
		l.Info("prepare: no primary replica, skipping primary snapshot")
		return dmte.ConfirmResult{MustConfirm: must, Confirmed: must}
	}
	if prepareSnapshotReady(primary) {
		l.Info("prepare: primary snapshot ready", "rvr", primary.rvrName)
		return dmte.ConfirmResult{MustConfirm: must, Confirmed: must}
	}
	if prepareSnapshotFailed(primary) {
		l.Info("prepare: primary snapshot failed, confirming step for cleanup", "rvr", primary.rvrName, "phase", primary.rvrs.Status.Phase)
		return dmte.ConfirmResult{MustConfirm: must, Confirmed: must}
	}
	if _, pending := ensurePrepareSnapshot(gctx, primary, must); pending {
		return dmte.ConfirmResult{MustConfirm: must}
	}
	return dmte.ConfirmResult{
		MustConfirm: must,
		Confirmed:   must,
	}
}

func confirmResumeIO(gctx *globalContext, _ int64) dmte.ConfirmResult {
	primary := preparePrimaryReplica(gctx)
	must := primaryConfirmSet(primary)
	if primary == nil {
		return dmte.ConfirmResult{MustConfirm: must, Confirmed: must}
	}
	if confirmed, pending := ensurePrepareOperation(
		gctx,
		preparePrimaryOperationName(gctx, "resume-io"),
		v1alpha1.DRBDResourceOperationSpec{
			DRBDResourceName: primary.rvrName,
			Type:             v1alpha1.DRBDResourceOperationResumeIO,
		},
		must,
	); pending {
		return dmte.ConfirmResult{MustConfirm: must}
	} else {
		return dmte.ConfirmResult{MustConfirm: must, Confirmed: confirmed}
	}
}

func confirmUntrackBitmap(gctx *globalContext, _ int64) dmte.ConfirmResult {
	primary := preparePrimaryReplica(gctx)
	must := primaryConfirmSet(primary)
	if primary == nil {
		return dmte.ConfirmResult{MustConfirm: must, Confirmed: must}
	}
	if _, pending := ensurePrepareOperation(
		gctx,
		prepareUntrackBitmapOperationName(gctx),
		v1alpha1.DRBDResourceOperationSpec{
			DRBDResourceName: primary.rvrName,
			Type:             v1alpha1.DRBDResourceOperationUntrackBitmap,
		},
		must,
	); pending {
		return dmte.ConfirmResult{MustConfirm: must}
	}
	return dmte.ConfirmResult{MustConfirm: must, Confirmed: must}
}

func preparePrimaryReplica(gctx *globalContext) *replicaContext {
	for i := range gctx.allReplicas {
		rc := &gctx.allReplicas[i]
		for _, member := range gctx.rv.Status.Datamesh.Members {
			if member.Attached && member.Name == rc.rvrName {
				return rc
			}
		}
	}
	return nil
}

func prepareReplicaIDs(gctx *globalContext) idset.IDSet {
	ids := make([]uint8, 0, len(gctx.allReplicas))
	for i := range gctx.allReplicas {
		ids = append(ids, gctx.allReplicas[i].id)
	}
	return idset.Of(ids...)
}

func primaryConfirmSet(primary *replicaContext) idset.IDSet {
	if primary == nil {
		return 0
	}
	return idset.Of(primary.id)
}

func ensurePrepareOperation(
	gctx *globalContext,
	name string,
	spec v1alpha1.DRBDResourceOperationSpec,
	confirmed idset.IDSet,
) (idset.IDSet, bool) {
	l := log.FromContext(gctx.ctx).WithValues("op", name, "type", spec.Type)
	op := &v1alpha1.DRBDResourceOperation{}
	if err := gctx.cl.Get(gctx.ctx, client.ObjectKey{Name: name}, op); err == nil {
		if op.Status.Phase == v1alpha1.DRBDOperationPhaseSucceeded {
			l.Info("prepare: operation succeeded")
			return confirmed, false
		}
		if op.Status.Phase == v1alpha1.DRBDOperationPhaseFailed {
			l.Info("prepare: operation failed, deleting to retry", "phase", op.Status.Phase)
			if err := gctx.cl.Delete(gctx.ctx, op); err != nil && !apierrors.IsNotFound(err) {
				l.Error(err, "prepare: failed to delete failed operation")
			}
			return 0, true
		} else {
			l.V(1).Info("prepare: operation pending", "phase", op.Status.Phase)
			return 0, true
		}
	} else if !apierrors.IsNotFound(err) {
		l.Error(err, "prepare: failed to get operation")
		return 0, true
	}

	op.Name = name
	op.Spec = spec

	if _, err := obju.SetControllerRef(op, gctx.rvs, gctx.scheme); err != nil {
		l.Error(err, "prepare: failed to set controller ref")
		return 0, true
	}
	if err := gctx.cl.Create(gctx.ctx, op); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			l.Error(err, "prepare: failed to create operation")
			return 0, true
		}
	}

	l.Info("prepare: created operation", "resource", spec.DRBDResourceName)
	return 0, true
}

func ensurePrepareSnapshot(gctx *globalContext, rctx *replicaContext, confirmed idset.IDSet) (idset.IDSet, bool) {
	l := log.FromContext(gctx.ctx).WithValues("rvr", rctx.rvrName, "node", rctx.nodeName)
	if prepareSnapshotReady(rctx) {
		l.Info("prepare: snapshot ready", "rvrs", rctx.rvrsName)
		return confirmed, false
	}
	if rctx.rvrs != nil {
		l.V(1).Info("prepare: snapshot pending", "rvrs", rctx.rvrsName, "phase", rctx.rvrs.Status.Phase)
		return 0, true
	}

	rvrsName := prepareReplicaSnapshotName(gctx, rctx)
	rvrs := &v1alpha1.ReplicatedVolumeReplicaSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name: rvrsName,
		},
		Spec: v1alpha1.ReplicatedVolumeReplicaSnapshotSpec{
			ReplicatedVolumeSnapshotName: gctx.rvs.Name,
			ReplicatedVolumeReplicaName:  rctx.rvrName,
			NodeName:                     rctx.nodeName,
		},
	}

	if _, err := obju.SetControllerRef(rvrs, gctx.rvs, gctx.scheme); err != nil {
		l.Error(err, "prepare: failed to set controller ref for snapshot")
		return 0, true
	}
	if err := gctx.cl.Create(gctx.ctx, rvrs); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			l.Error(err, "prepare: failed to create snapshot")
			return 0, true
		}
		if err := gctx.cl.Get(gctx.ctx, client.ObjectKeyFromObject(rvrs), rvrs); err != nil {
			l.Error(err, "prepare: failed to get existing snapshot")
			return 0, true
		}
	}

	l.Info("prepare: created snapshot RVRS", "rvrs", rvrsName)
	rctx.rvrs = rvrs
	rctx.rvrsName = rvrs.Name

	return 0, true
}

func prepareSnapshotReady(rctx *replicaContext) bool {
	return rctx.rvrs != nil && rctx.rvrs.Status.SnapshotHandle != ""
}

func prepareSnapshotFailed(rctx *replicaContext) bool {
	if rctx.rvrs == nil {
		return false
	}
	return rctx.rvrs.Status.Phase == v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseFailed ||
		rctx.rvrs.Status.Phase == v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseDeleting
}

func prepareOperationSucceeded(gctx *globalContext, name string) bool {
	op := &v1alpha1.DRBDResourceOperation{}
	if err := gctx.cl.Get(gctx.ctx, client.ObjectKey{Name: name}, op); err != nil {
		return false
	}
	return op.Status.Phase == v1alpha1.DRBDOperationPhaseSucceeded
}

func prepareCleanupNeeded(gctx *globalContext) bool {
	l := log.FromContext(gctx.ctx)
	if !prepareOperationSucceeded(gctx, preparePrimaryOperationName(gctx, "suspend-io")) {
		return false
	}
	if prepareOperationSucceeded(gctx, preparePrimaryOperationName(gctx, "resume-io")) {
		return false
	}
	primary := preparePrimaryReplica(gctx)
	if primary != nil && prepareSnapshotFailed(primary) {
		l.Info("prepare: cleanup needed — primary snapshot failed after suspend-io", "rvr", primary.rvrName)
		return true
	}
	if prepareUnsafeStepTimedOut(gctx) {
		l.Info("prepare: cleanup needed — unsafe step timed out after suspend-io", "timeout", prepareUnsafeTimeout)
		return true
	}
	return false
}

func prepareUnsafeStepTimedOut(gctx *globalContext) bool {
	now := time.Now()
	for i := range gctx.rvs.Status.PrepareTransitions {
		step := gctx.rvs.Status.PrepareTransitions[i].CurrentStep()
		if step == nil || step.StartedAt == nil {
			continue
		}
		if step.Name != "Flush bitmap" && step.Name != "Create primary snapshot" {
			continue
		}
		if step.Status == v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive &&
			now.Sub(step.StartedAt.Time) >= prepareUnsafeTimeout {
			return true
		}
	}
	return false
}

func prepareReplicaSnapshotName(gctx *globalContext, rctx *replicaContext) string {
	return fmt.Sprintf("%s-%s", gctx.rvs.Name, rctx.nodeName)
}

func prepareTrackBitmapOperationName(gctx *globalContext) string {
	return fmt.Sprintf("%s-track-bitmap", gctx.rvs.Name)
}

func prepareUntrackBitmapOperationName(gctx *globalContext) string {
	return fmt.Sprintf("%s-untrack-bitmap", gctx.rvs.Name)
}

func preparePrimaryOperationName(gctx *globalContext, action string) string {
	return fmt.Sprintf("%s-%s-primary", gctx.rvs.Name, action)
}
