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
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

// ──────────────────────────────────────────────────────────────────────────────
// Wiring / construction
//

type Reconciler struct {
	cl     client.Client
	scheme *runtime.Scheme
}

func NewReconciler(cl client.Client, scheme *runtime.Scheme) *Reconciler {
	return &Reconciler{
		cl:     cl,
		scheme: scheme,
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile
//

func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	rf := flow.BeginRootReconcile(ctx)

	rvrs, err := r.getRVRS(rf.Ctx(), req.Name)
	if err != nil {
		return rf.Fail(err).ToCtrl()
	}
	if rvrs == nil {
		return rf.Done().ToCtrl()
	}

	if rvrs.DeletionTimestamp != nil {
		return r.reconcileDelete(rf.Ctx(), rvrs).ToCtrl()
	}

	return r.reconcileNormal(rf.Ctx(), rvrs).ToCtrl()
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: normal
//

func (r *Reconciler) reconcileNormal(ctx context.Context, rvrs *v1alpha1.ReplicatedVolumeReplicaSnapshot) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "normal")
	defer rf.OnEnd(&outcome)

	base := rvrs.DeepCopy()
	if obju.AddFinalizer(rvrs, v1alpha1.RVRSControllerFinalizer) {
		if err := r.patchRVRS(rf.Ctx(), rvrs, base); err != nil {
			return rf.Fail(err)
		}
	}

	llvsName := safeLVMName(rvrs.Name)
	llvs, err := r.getLLVS(rf.Ctx(), llvsName)
	if err != nil {
		return rf.Fail(err)
	}

	if llvs == nil {
		llvName, err := r.resolveLLVNameForRVR(rf.Ctx(), rvrs.Spec.ReplicatedVolumeReplicaName)
		if err != nil {
			return r.reconcileRVRSStatus(rf.Ctx(), rvrs,
				v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseFailed,
				fmt.Sprintf("Failed to resolve LLV name: %v", err),
				false, "")
		}

		if err := r.createLLVS(rf.Ctx(), rvrs, llvsName, llvName); err != nil {
			if apierrors.IsAlreadyExists(err) {
				return rf.DoneAndRequeue()
			}
			return rf.Fail(err)
		}
		return r.reconcileRVRSStatus(rf.Ctx(), rvrs,
			v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseInProgress,
			"Snapshot creation started",
			false, "")
	}

	if llvs.Status == nil {
		return r.reconcileRVRSStatus(rf.Ctx(), rvrs,
			v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseInProgress,
			"Waiting for snapshot to be created",
			false, "")
	}

	switch llvs.Status.Phase {
	case "Created":
		return r.reconcileRVRSStatus(rf.Ctx(), rvrs,
			v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseReady,
			"Snapshot created successfully",
			true, llvsName)
	case "Failed":
		msg := "Snapshot creation failed"
		if llvs.Status.Reason != "" {
			msg = llvs.Status.Reason
		}
		return r.reconcileRVRSStatus(rf.Ctx(), rvrs,
			v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseFailed,
			msg, false, "")
	default:
		return r.reconcileRVRSStatus(rf.Ctx(), rvrs,
			v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseInProgress,
			"Waiting for snapshot to be created",
			false, "")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: status
//

func (r *Reconciler) reconcileRVRSStatus(
	ctx context.Context,
	rvrs *v1alpha1.ReplicatedVolumeReplicaSnapshot,
	phase v1alpha1.ReplicatedVolumeReplicaSnapshotPhase,
	message string,
	readyToUse bool,
	snapshotHandle string,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "status")
	defer rf.OnEnd(&outcome)

	if rvrs.Status.Phase == phase &&
		rvrs.Status.Message == message &&
		rvrs.Status.ReadyToUse == readyToUse &&
		rvrs.Status.SnapshotHandle == snapshotHandle {
		return rf.Continue()
	}

	base := rvrs.DeepCopy()

	rvrs.Status.Phase = phase
	rvrs.Status.Message = message
	rvrs.Status.ReadyToUse = readyToUse
	rvrs.Status.SnapshotHandle = snapshotHandle
	if readyToUse && rvrs.Status.CreationTime == nil {
		now := metav1.Now()
		rvrs.Status.CreationTime = &now
	}

	if err := r.patchRVRSStatus(rf.Ctx(), rvrs, base); err != nil {
		return rf.Fail(err)
	}

	return rf.Continue()
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: delete
//

func (r *Reconciler) reconcileDelete(ctx context.Context, rvrs *v1alpha1.ReplicatedVolumeReplicaSnapshot) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "delete")
	defer rf.OnEnd(&outcome)

	if !obju.HasFinalizer(rvrs, v1alpha1.RVRSControllerFinalizer) {
		return rf.Done()
	}

	if rvrs.Status.SnapshotHandle != "" {
		llvsName := rvrs.Status.SnapshotHandle
		llvs, err := r.getLLVS(rf.Ctx(), llvsName)
		if err != nil {
			return rf.Fail(err)
		}

		if llvs != nil {
			if err := r.cl.Delete(rf.Ctx(), llvs); err != nil && !apierrors.IsNotFound(err) {
				return rf.Fail(err)
			}
			return r.reconcileRVRSStatus(rf.Ctx(), rvrs,
				v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseDeleting,
				"Waiting for snapshot to be deleted",
				false, rvrs.Status.SnapshotHandle)
		}
	}

	base := rvrs.DeepCopy()
	obju.RemoveFinalizer(rvrs, v1alpha1.RVRSControllerFinalizer)
	if err := r.patchRVRS(rf.Ctx(), rvrs, base); err != nil {
		return rf.Fail(err)
	}

	return rf.Done()
}

// ──────────────────────────────────────────────────────────────────────────────
// Single-call I/O helpers
//

// --- RVRS ---

func (r *Reconciler) getRVRS(ctx context.Context, name string) (*v1alpha1.ReplicatedVolumeReplicaSnapshot, error) {
	rvrs := &v1alpha1.ReplicatedVolumeReplicaSnapshot{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: name}, rvrs); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return rvrs, nil
}

func (r *Reconciler) patchRVRS(
	ctx context.Context,
	rvrs *v1alpha1.ReplicatedVolumeReplicaSnapshot,
	base *v1alpha1.ReplicatedVolumeReplicaSnapshot,
) error {
	patch := client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})
	return r.cl.Patch(ctx, rvrs, patch)
}

func (r *Reconciler) patchRVRSStatus(
	ctx context.Context,
	rvrs *v1alpha1.ReplicatedVolumeReplicaSnapshot,
	base *v1alpha1.ReplicatedVolumeReplicaSnapshot,
) error {
	patch := client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})
	return r.cl.Status().Patch(ctx, rvrs, patch)
}

// --- LLVS ---

func (r *Reconciler) getLLVS(ctx context.Context, name string) (*snc.LVMLogicalVolumeSnapshot, error) {
	llvs := &snc.LVMLogicalVolumeSnapshot{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: name}, llvs); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return llvs, nil
}

func (r *Reconciler) createLLVS(
	ctx context.Context,
	owner *v1alpha1.ReplicatedVolumeReplicaSnapshot,
	llvsName string,
	llvName string,
) error {
	obj := &snc.LVMLogicalVolumeSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name: llvsName,
		},
		Spec: snc.LVMLogicalVolumeSnapshotSpec{
			ActualSnapshotNameOnTheNode: llvsName,
			LVMLogicalVolumeName:        llvName,
		},
	}
	if _, err := obju.SetControllerRef(obj, owner, r.scheme); err != nil {
		return err
	}
	return r.cl.Create(ctx, obj)
}

// --- DRBDResource ---

func (r *Reconciler) resolveLLVNameForRVR(ctx context.Context, rvrName string) (string, error) {
	drbdr := &v1alpha1.DRBDResource{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: rvrName}, drbdr); err != nil {
		return "", fmt.Errorf("getting DRBDResource %s: %w", rvrName, err)
	}
	if drbdr.Spec.LVMLogicalVolumeName == "" {
		return "", fmt.Errorf("DRBDResource %s has no lvmLogicalVolumeName", rvrName)
	}
	return drbdr.Spec.LVMLogicalVolumeName, nil
}
