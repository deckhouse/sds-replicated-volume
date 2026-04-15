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
	"reflect"

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
		return r.reconcileStatus(rf.Ctx(), rvs, rvs.Status.Datamesh,
			v1alpha1.ReplicatedVolumeSnapshotPhaseFailed,
			"ReplicatedVolume not found",
			false,
			rvs.Status.SourceReplicaSnapshotName)
	}

	rvrs, err := r.getRVRsByRVName(rf.Ctx(), rv.Name)
	if err != nil {
		return rf.Fail(err)
	}

	childRVRSs, err := r.getChildRVRSs(rf.Ctx(), rvs.Name)
	if err != nil {
		return rf.Fail(err)
	}

	outcome = r.reconcileAggregateStatus(rf.Ctx(), rvs, rv, rvrs, childRVRSs)
	if outcome.ShouldReturn() {
		return outcome
	}

	if rvs.Status.Phase != v1alpha1.ReplicatedVolumeSnapshotPhaseFailed &&
		rvs.Status.Phase != v1alpha1.ReplicatedVolumeSnapshotPhaseDeleting {
		if prepareNeedsRun(rvs) {
			return r.reconcilePrepareMesh(rf.Ctx(), rvs, rv, rvrs, childRVRSs)
		}
		if false && syncNeedsRun(rvs) { // TODO: remove this after testing
			return r.reconcileSyncMesh(rf.Ctx(), rvs, rv, childRVRSs)
		}
	}

	return rf.Continue()
}

func (r *Reconciler) createMissingRVRSs(
	ctx context.Context,
	rvs *v1alpha1.ReplicatedVolumeSnapshot,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	existingByRVR map[string]*v1alpha1.ReplicatedVolumeReplicaSnapshot,
) (requeue bool, err error) {
	for _, rvr := range rvrs {
		if _, exists := existingByRVR[rvr.Name]; exists {
			continue
		}
		if err := r.createRVRS(ctx, rvs, rvr); err != nil {
			if apierrors.IsAlreadyExists(err) {
				return true, nil
			}
			return false, err
		}
		requeue = true
	}
	return requeue, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: aggregate-status
//

// Reconcile pattern: In-place reconciliation.
func (r *Reconciler) reconcileAggregateStatus(
	ctx context.Context,
	rvs *v1alpha1.ReplicatedVolumeSnapshot,
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	childRVRSs []*v1alpha1.ReplicatedVolumeReplicaSnapshot,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "aggregate-status")
	defer rf.OnEnd(&outcome)

	datamesh := r.buildDatamesh(rvs, rv, rvrs, childRVRSs)
	sourceRVRSName := sourceReplicaSnapshotName(rv.Status.Datamesh.Members, childRVRSs)

	if datamesh.TotalCount == 0 {
		return r.reconcileStatus(rf.Ctx(), rvs, datamesh,
			v1alpha1.ReplicatedVolumeSnapshotPhasePending,
			"Waiting for replica snapshots to be created",
			false,
			sourceRVRSName)
	}

	anyFailed := false
	for _, child := range childRVRSs {
		if child.Status.Phase == v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseFailed {
			anyFailed = true
			break
		}
	}

	if anyFailed {
		if len(rvs.Status.PrepareTransitions) > 0 {
			return r.reconcileStatus(rf.Ctx(), rvs, datamesh,
				v1alpha1.ReplicatedVolumeSnapshotPhaseInProgress,
				"Prepare cleanup is in progress after replica snapshot failure",
				false,
				sourceRVRSName)
		}
		return r.reconcileStatus(rf.Ctx(), rvs, datamesh,
			v1alpha1.ReplicatedVolumeSnapshotPhaseFailed,
			"One or more replica snapshots failed",
			false,
			sourceRVRSName)
	}
	if syncActive(rvs) {
		return r.reconcileStatus(rf.Ctx(), rvs, datamesh,
			v1alpha1.ReplicatedVolumeSnapshotPhaseSynchronizing,
			"Snapshot synchronization is in progress",
			false,
			sourceRVRSName)
	}
	if datamesh.ReadyCount == datamesh.TotalCount {
		if datamesh.TotalCount > 1 && rvs.Status.Phase != v1alpha1.ReplicatedVolumeSnapshotPhaseReady {
			return r.reconcileStatus(rf.Ctx(), rvs, datamesh,
				v1alpha1.ReplicatedVolumeSnapshotPhaseSynchronizing,
				"All replica snapshots created, starting synchronization",
				false,
				sourceRVRSName)
		}
		return r.reconcileStatus(rf.Ctx(), rvs, datamesh,
			v1alpha1.ReplicatedVolumeSnapshotPhaseReady,
			"All replica snapshots are ready",
			true,
			sourceRVRSName)
	}

	return r.reconcileStatus(rf.Ctx(), rvs, datamesh,
		v1alpha1.ReplicatedVolumeSnapshotPhaseInProgress,
		"Replica snapshots are being created",
		false,
		sourceRVRSName)
}

func (r *Reconciler) buildDatamesh(
	rvs *v1alpha1.ReplicatedVolumeSnapshot,
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	childRVRSs []*v1alpha1.ReplicatedVolumeReplicaSnapshot,
) v1alpha1.ReplicatedVolumeSnapshotDatamesh {
	rvrsByName := make(map[string]*v1alpha1.ReplicatedVolumeReplica, len(rvrs))
	for _, rvr := range rvrs {
		rvrsByName[rvr.Name] = rvr
	}

	childByRVRName := make(map[string]*v1alpha1.ReplicatedVolumeReplicaSnapshot, len(childRVRSs))
	for _, child := range childRVRSs {
		childByRVRName[child.Spec.ReplicatedVolumeReplicaName] = child
	}

	members := make([]v1alpha1.SnapshotDatameshMember, 0, len(rv.Status.Datamesh.Members))
	var readyCount int

	for _, member := range rv.Status.Datamesh.Members {
		if member.Name == "" {
			continue
		}

		rvr := rvrsByName[member.Name]
		if rvr == nil || rvr.Spec.Type != v1alpha1.ReplicaTypeDiskful || rvr.Spec.NodeName == "" {
			continue
		}

		child := childByRVRName[rvr.Name]
		ready := child != nil && child.Status.Phase == v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseReady
		if ready {
			readyCount++
		}

		member := v1alpha1.SnapshotDatameshMember{
			Name:     rvr.Name,
			NodeName: rvr.Spec.NodeName,
			Ready:    ready,
		}
		if child != nil {
			member.SnapshotHandle = child.Status.SnapshotHandle
		}
		members = append(members, member)
	}

	return v1alpha1.ReplicatedVolumeSnapshotDatamesh{
		ReplicatedVolumeName: rvs.Spec.ReplicatedVolumeName,
		Members:              members,
		ReadyCount:           readyCount,
		TotalCount:           len(members),
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Reconcile: status
//

// Reconcile pattern: In-place reconciliation.
func (r *Reconciler) reconcileStatus(
	ctx context.Context,
	rvs *v1alpha1.ReplicatedVolumeSnapshot,
	datamesh v1alpha1.ReplicatedVolumeSnapshotDatamesh,
	phase v1alpha1.ReplicatedVolumeSnapshotPhase,
	message string,
	readyToUse bool,
	sourceReplicaSnapshotName string,
) (outcome flow.ReconcileOutcome) {
	rf := flow.BeginReconcile(ctx, "status")
	defer rf.OnEnd(&outcome)

	base := rvs.DeepCopy()

	rvs.Status.Phase = phase
	rvs.Status.Message = message
	rvs.Status.ReadyToUse = readyToUse
	rvs.Status.Datamesh = datamesh
	rvs.Status.SourceReplicaSnapshotName = sourceReplicaSnapshotName
	if readyToUse && rvs.Status.CreationTime == nil {
		now := metav1.Now()
		rvs.Status.CreationTime = &now
	}

	if reflect.DeepEqual(rvs.Status, base.Status) {
		return rf.Continue()
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
			return r.reconcileStatus(rf.Ctx(), rvs, rvs.Status.Datamesh,
				v1alpha1.ReplicatedVolumeSnapshotPhaseDeleting,
				"Waiting for replica snapshots to be deleted",
				false,
				rvs.Status.SourceReplicaSnapshotName).Enrichf("waiting for children deletion")
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

// ──────────────────────────────────────────────────────────────────────────────
// Pure helpers
//

func syncActive(rvs *v1alpha1.ReplicatedVolumeSnapshot) bool {
	return rvs.Status.SyncRevision > 0 || len(rvs.Status.SyncTransitions) > 0
}

func syncCanStart(rvs *v1alpha1.ReplicatedVolumeSnapshot) bool {
	return !syncActive(rvs) &&
		!rvs.Status.ReadyToUse &&
		rvs.Status.Datamesh.TotalCount > 1 &&
		rvs.Status.Datamesh.ReadyCount == rvs.Status.Datamesh.TotalCount
}

func syncNeedsRun(rvs *v1alpha1.ReplicatedVolumeSnapshot) bool {
	return syncActive(rvs) || syncCanStart(rvs)
}

func splitRVRsByAttachment(
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	members []v1alpha1.DatameshMember,
) (secondary []*v1alpha1.ReplicatedVolumeReplica, primary []*v1alpha1.ReplicatedVolumeReplica) {
	attached := make(map[string]bool, len(members))
	for _, member := range members {
		attached[member.Name] = member.Attached
	}

	for _, rvr := range rvrs {
		if rvr.Spec.Type != v1alpha1.ReplicaTypeDiskful || rvr.Spec.NodeName == "" {
			continue
		}
		if attached[rvr.Name] {
			primary = append(primary, rvr)
		} else {
			secondary = append(secondary, rvr)
		}
	}

	return secondary, primary
}

func allRVRSReady(
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	existingByRVR map[string]*v1alpha1.ReplicatedVolumeReplicaSnapshot,
) bool {
	for _, rvr := range rvrs {
		rvrsObj := existingByRVR[rvr.Name]
		if rvrsObj == nil || rvrsObj.Status.Phase != v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseReady {
			return false
		}
	}
	return true
}

func sourceReplicaSnapshotName(
	members []v1alpha1.DatameshMember,
	childRVRSs []*v1alpha1.ReplicatedVolumeReplicaSnapshot,
) string {
	var primaryRVRName string
	for _, member := range members {
		if member.Attached {
			primaryRVRName = member.Name
			break
		}
	}
	if primaryRVRName == "" {
		return ""
	}
	for _, child := range childRVRSs {
		if child.Spec.ReplicatedVolumeReplicaName == primaryRVRName {
			return child.Name
		}
	}
	return ""
}
