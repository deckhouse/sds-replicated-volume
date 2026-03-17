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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes"
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

	rvs, err := r.getRVS(rf.Ctx(), req.Name)
	if err != nil {
		return rf.Fail(err).ToCtrl()
	}
	if rvs == nil {
		return rf.Done().ToCtrl()
	}

	if rvs.DeletionTimestamp != nil {
		return r.reconcileDelete(rf.Ctx(), rvs).ToCtrl()
	}

	return r.reconcileNormal(rf.Ctx(), rvs).ToCtrl()
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: normal
//

// Reconcile pattern: In-place reconciliation.
func (r *Reconciler) reconcileNormal(ctx context.Context, rvs *v1alpha1.ReplicatedVolumeSnapshot) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "normal")
	defer rf.OnEnd(&outcome)

	base := rvs.DeepCopy()
	if obju.AddFinalizer(rvs, v1alpha1.RVSControllerFinalizer) {
		if err := r.patchRVS(rf.Ctx(), rvs, base); err != nil {
			return rf.Fail(err)
		}
	}

	rv, err := r.getRV(rf.Ctx(), rvs.Spec.ReplicatedVolumeName)
	if err != nil {
		return rf.Fail(err)
	}
	if rv == nil {
		return r.reconcileStatus(rf.Ctx(), rvs,
			v1alpha1.ReplicatedVolumeSnapshotPhaseFailed,
			"ReplicatedVolume not found",
			false)
	}

	rvrs, err := r.getRVRsByRVName(rf.Ctx(), rv.Name)
	if err != nil {
		return rf.Fail(err)
	}

	childRVRSs, err := r.getChildRVRSs(rf.Ctx(), rvs.Name)
	if err != nil {
		return rf.Fail(err)
	}

	outcome = r.reconcileChildren(rf.Ctx(), rvs, rvrs, childRVRSs)
	if outcome.ShouldReturn() {
		return outcome
	}

	childRVRSs, err = r.getChildRVRSs(rf.Ctx(), rvs.Name)
	if err != nil {
		return rf.Fail(err)
	}

	return r.reconcileAggregateStatus(rf.Ctx(), rvs, childRVRSs)
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: children
//

// Reconcile pattern: In-place reconciliation.
func (r *Reconciler) reconcileChildren(
	ctx context.Context,
	rvs *v1alpha1.ReplicatedVolumeSnapshot,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	existingRVRSs []*v1alpha1.ReplicatedVolumeReplicaSnapshot,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "children")
	defer rf.OnEnd(&outcome)

	existingByRVR := make(map[string]*v1alpha1.ReplicatedVolumeReplicaSnapshot, len(existingRVRSs))
	for _, child := range existingRVRSs {
		existingByRVR[child.Spec.ReplicatedVolumeReplicaName] = child
	}

	for _, rvr := range rvrs {
		if rvr.Spec.Type != v1alpha1.ReplicaTypeDiskful {
			continue
		}
		if rvr.Spec.NodeName == "" {
			continue
		}
		if _, exists := existingByRVR[rvr.Name]; exists {
			continue
		}

		if err := r.createRVRS(rf.Ctx(), rvs, rvr); err != nil {
			if apierrors.IsAlreadyExists(err) {
				return rf.DoneAndRequeue()
			}
			return rf.Fail(err)
		}
	}

	return rf.Continue()
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: aggregate-status
//

// Reconcile pattern: In-place reconciliation.
func (r *Reconciler) reconcileAggregateStatus(
	ctx context.Context,
	rvs *v1alpha1.ReplicatedVolumeSnapshot,
	childRVRSs []*v1alpha1.ReplicatedVolumeReplicaSnapshot,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "aggregate-status")
	defer rf.OnEnd(&outcome)

	if len(childRVRSs) == 0 {
		return r.reconcileStatus(rf.Ctx(), rvs,
			v1alpha1.ReplicatedVolumeSnapshotPhasePending,
			"Waiting for replica snapshots to be created",
			false)
	}

	allReady := true
	anyFailed := false
	anyInProgress := false

	for _, child := range childRVRSs {
		switch child.Status.Phase {
		case v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseFailed:
			anyFailed = true
		case v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseReady:
			// ok
		case v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseInProgress:
			anyInProgress = true
			allReady = false
		default:
			allReady = false
		}
	}

	if anyFailed {
		return r.reconcileStatus(rf.Ctx(), rvs,
			v1alpha1.ReplicatedVolumeSnapshotPhaseFailed,
			"One or more replica snapshots failed",
			false)
	}
	if allReady {
		return r.reconcileStatus(rf.Ctx(), rvs,
			v1alpha1.ReplicatedVolumeSnapshotPhaseReady,
			"All replica snapshots are ready",
			true)
	}
	if anyInProgress {
		return r.reconcileStatus(rf.Ctx(), rvs,
			v1alpha1.ReplicatedVolumeSnapshotPhaseInProgress,
			"Replica snapshots are being created",
			false)
	}

	return r.reconcileStatus(rf.Ctx(), rvs,
		v1alpha1.ReplicatedVolumeSnapshotPhasePending,
		"Waiting for replica snapshots",
		false)
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: status
//

// Reconcile pattern: In-place reconciliation.
func (r *Reconciler) reconcileStatus(
	ctx context.Context,
	rvs *v1alpha1.ReplicatedVolumeSnapshot,
	phase v1alpha1.ReplicatedVolumeSnapshotPhase,
	message string,
	readyToUse bool,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "status")
	defer rf.OnEnd(&outcome)

	if rvs.Status.Phase == phase && rvs.Status.Message == message && rvs.Status.ReadyToUse == readyToUse {
		return rf.Continue()
	}

	base := rvs.DeepCopy()

	rvs.Status.Phase = phase
	rvs.Status.Message = message
	rvs.Status.ReadyToUse = readyToUse
	if readyToUse && rvs.Status.CreationTime == nil {
		now := metav1.Now()
		rvs.Status.CreationTime = &now
	}

	if err := r.patchRVSStatus(rf.Ctx(), rvs, base); err != nil {
		return rf.Fail(err)
	}

	return rf.Continue()
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: delete
//

// Reconcile pattern: In-place reconciliation.
func (r *Reconciler) reconcileDelete(ctx context.Context, rvs *v1alpha1.ReplicatedVolumeSnapshot) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "delete")
	defer rf.OnEnd(&outcome)

	if !obju.HasFinalizer(rvs, v1alpha1.RVSControllerFinalizer) {
		return rf.Done()
	}

	childRVRSs, err := r.getChildRVRSs(rf.Ctx(), rvs.Name)
	if err != nil {
		return rf.Fail(err)
	}

	for _, child := range childRVRSs {
		if child.DeletionTimestamp != nil {
			continue
		}
		if err := r.cl.Delete(rf.Ctx(), child); err != nil && !apierrors.IsNotFound(err) {
			return rf.Fail(err)
		}
	}

	if len(childRVRSs) > 0 {
		allDeleted := true
		for _, child := range childRVRSs {
			if child.DeletionTimestamp == nil {
				allDeleted = false
				break
			}
		}
		if !allDeleted {
			return r.reconcileStatus(rf.Ctx(), rvs,
				v1alpha1.ReplicatedVolumeSnapshotPhaseDeleting,
				"Waiting for replica snapshots to be deleted",
				false).Enrichf("waiting for children deletion")
		}

		return rf.DoneAndRequeue()
	}

	base := rvs.DeepCopy()
	obju.RemoveFinalizer(rvs, v1alpha1.RVSControllerFinalizer)
	if err := r.patchRVS(rf.Ctx(), rvs, base); err != nil {
		return rf.Fail(err)
	}

	return rf.Done()
}

// ──────────────────────────────────────────────────────────────────────────────
// Single-call I/O helpers
//

// --- RVS ---

func (r *Reconciler) getRVS(ctx context.Context, name string) (*v1alpha1.ReplicatedVolumeSnapshot, error) {
	rvs := &v1alpha1.ReplicatedVolumeSnapshot{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: name}, rvs); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return rvs, nil
}

func (r *Reconciler) patchRVS(
	ctx context.Context,
	rvs *v1alpha1.ReplicatedVolumeSnapshot,
	base *v1alpha1.ReplicatedVolumeSnapshot,
) error {
	patch := client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})
	return r.cl.Patch(ctx, rvs, patch)
}

func (r *Reconciler) patchRVSStatus(
	ctx context.Context,
	rvs *v1alpha1.ReplicatedVolumeSnapshot,
	base *v1alpha1.ReplicatedVolumeSnapshot,
) error {
	patch := client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})
	return r.cl.Status().Patch(ctx, rvs, patch)
}

// --- RVRS ---

func (r *Reconciler) getChildRVRSs(ctx context.Context, rvsName string) ([]*v1alpha1.ReplicatedVolumeReplicaSnapshot, error) {
	list := &v1alpha1.ReplicatedVolumeReplicaSnapshotList{}
	if err := r.cl.List(ctx, list, client.MatchingFields{
		indexes.IndexFieldRVRSBySnapshotName: rvsName,
	}); err != nil {
		return nil, err
	}
	result := make([]*v1alpha1.ReplicatedVolumeReplicaSnapshot, len(list.Items))
	for i := range list.Items {
		result[i] = &list.Items[i]
	}
	return result, nil
}

func (r *Reconciler) createRVRS(
	ctx context.Context,
	rvs *v1alpha1.ReplicatedVolumeSnapshot,
	rvr *v1alpha1.ReplicatedVolumeReplica,
) error {
	rvrsName := fmt.Sprintf("%s-%s", rvs.Name, rvr.Spec.NodeName)

	obj := &v1alpha1.ReplicatedVolumeReplicaSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name: rvrsName,
		},
		Spec: v1alpha1.ReplicatedVolumeReplicaSnapshotSpec{
			ReplicatedVolumeSnapshotName: rvs.Name,
			ReplicatedVolumeReplicaName:  rvr.Name,
			NodeName:                     rvr.Spec.NodeName,
		},
	}

	if _, err := obju.SetControllerRef(obj, rvs, r.scheme); err != nil {
		return err
	}

	return r.cl.Create(ctx, obj)
}

// --- RV ---

func (r *Reconciler) getRV(ctx context.Context, name string) (*v1alpha1.ReplicatedVolume, error) {
	rv := &v1alpha1.ReplicatedVolume{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: name}, rv); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return rv, nil
}

// --- RVR ---

func (r *Reconciler) getRVRsByRVName(ctx context.Context, rvName string) ([]*v1alpha1.ReplicatedVolumeReplica, error) {
	list := &v1alpha1.ReplicatedVolumeReplicaList{}
	if err := r.cl.List(ctx, list, client.MatchingFields{
		indexes.IndexFieldRVRByReplicatedVolumeName: rvName,
	}); err != nil {
		return nil, err
	}
	result := make([]*v1alpha1.ReplicatedVolumeReplica, len(list.Items))
	for i := range list.Items {
		result[i] = &list.Items[i]
	}
	return result, nil
}
