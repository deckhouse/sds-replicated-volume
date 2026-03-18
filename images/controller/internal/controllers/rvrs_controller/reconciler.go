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

// Reconcile pattern: Pure orchestration.
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

// Reconcile pattern: In-place reconciliation.
func (r *Reconciler) reconcileNormal(ctx context.Context, rvrs *v1alpha1.ReplicatedVolumeReplicaSnapshot) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "normal")
	defer rf.OnEnd(&outcome)

	base := rvrs.DeepCopy()
	if obju.AddFinalizer(rvrs, v1alpha1.RVRSControllerFinalizer) {
		if err := r.patchRVRS(rf.Ctx(), rvrs, base); err != nil {
			return rf.Fail(err)
		}
	}

	droName := computeCreateSnapshotDROName(rvrs.Name)
	dro, err := r.getDRO(rf.Ctx(), droName)
	if err != nil {
		return rf.Fail(err)
	}

	if dro == nil {
		snapshotName := rvrs.Name
		if err := r.createDRO(rf.Ctx(), rvrs, droName, v1alpha1.DRBDResourceOperationSpec{
			DRBDResourceName: rvrs.Spec.ReplicatedVolumeReplicaName,
			Type:             v1alpha1.DRBDResourceOperationCreateSnapshot,
			CreateSnapshot: &v1alpha1.CreateSnapshotParams{
				SnapshotName: snapshotName,
			},
		}); err != nil {
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

	switch dro.Status.Phase {
	case v1alpha1.DRBDOperationPhaseSucceeded:
		snapshotHandle := dro.Status.Result
		if snapshotHandle == "" {
			snapshotHandle = rvrs.Name
		}
		return r.reconcileRVRSStatus(rf.Ctx(), rvrs,
			v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseReady,
			"Snapshot created successfully",
			true, snapshotHandle)
	case v1alpha1.DRBDOperationPhaseFailed:
		msg := "Snapshot creation failed"
		if dro.Status.Message != "" {
			msg = dro.Status.Message
		}
		return r.reconcileRVRSStatus(rf.Ctx(), rvrs,
			v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseFailed,
			msg, false, "")
	default:
		return r.reconcileRVRSStatus(rf.Ctx(), rvrs,
			v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseInProgress,
			"Waiting for snapshot operation to complete",
			false, "")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: status
//

// Reconcile pattern: In-place reconciliation.
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

// Reconcile pattern: In-place reconciliation.
func (r *Reconciler) reconcileDelete(ctx context.Context, rvrs *v1alpha1.ReplicatedVolumeReplicaSnapshot) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "delete")
	defer rf.OnEnd(&outcome)

	if !obju.HasFinalizer(rvrs, v1alpha1.RVRSControllerFinalizer) {
		return rf.Done()
	}

	snapshotHandle := rvrs.Status.SnapshotHandle
	if snapshotHandle != "" {
		droName := computeDeleteSnapshotDROName(rvrs.Name)
		dro, err := r.getDRO(rf.Ctx(), droName)
		if err != nil {
			return rf.Fail(err)
		}

		if dro == nil {
			if err := r.createDRO(rf.Ctx(), rvrs, droName, v1alpha1.DRBDResourceOperationSpec{
				DRBDResourceName: rvrs.Spec.ReplicatedVolumeReplicaName,
				Type:             v1alpha1.DRBDResourceOperationDeleteSnapshot,
				DeleteSnapshot: &v1alpha1.DeleteSnapshotParams{
					SnapshotName: snapshotHandle,
				},
			}); err != nil {
				if apierrors.IsAlreadyExists(err) {
					return rf.DoneAndRequeue()
				}
				return rf.Fail(err)
			}
			return r.reconcileRVRSStatus(rf.Ctx(), rvrs,
				v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseDeleting,
				"Snapshot deletion started",
				false, snapshotHandle)
		}

		switch dro.Status.Phase {
		case v1alpha1.DRBDOperationPhaseSucceeded:
			// ok, proceed to remove finalizer
		case v1alpha1.DRBDOperationPhaseFailed:
			msg := "Snapshot deletion failed"
			if dro.Status.Message != "" {
				msg = dro.Status.Message
			}
			return r.reconcileRVRSStatus(rf.Ctx(), rvrs,
				v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseFailed,
				msg, false, snapshotHandle)
		default:
			return r.reconcileRVRSStatus(rf.Ctx(), rvrs,
				v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseDeleting,
				"Waiting for snapshot deletion to complete",
				false, snapshotHandle)
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
// Helpers: Reconcile (non-I/O)
//

func computeCreateSnapshotDROName(rvrsName string) string {
	return fmt.Sprintf("%s-create-snap", rvrsName)
}

func computeDeleteSnapshotDROName(rvrsName string) string {
	return fmt.Sprintf("%s-delete-snap", rvrsName)
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

// --- DRO ---

func (r *Reconciler) getDRO(ctx context.Context, name string) (*v1alpha1.DRBDResourceOperation, error) {
	dro := &v1alpha1.DRBDResourceOperation{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: name}, dro); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return dro, nil
}

func (r *Reconciler) createDRO(
	ctx context.Context,
	owner *v1alpha1.ReplicatedVolumeReplicaSnapshot,
	name string,
	spec v1alpha1.DRBDResourceOperationSpec,
) error {
	obj := &v1alpha1.DRBDResourceOperation{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: spec,
	}
	if _, err := obju.SetControllerRef(obj, owner, r.scheme); err != nil {
		return err
	}
	return r.cl.Create(ctx, obj)
}
