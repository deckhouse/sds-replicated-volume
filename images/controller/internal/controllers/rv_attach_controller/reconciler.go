/*
Copyright 2025 Flant JSC

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

package rvattachcontroller

import (
	"context"
	"errors"
	"slices"
	"sort"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

type Reconciler struct {
	cl  client.Client
	log logr.Logger
}

func NewReconciler(cl client.Client, log logr.Logger) *Reconciler {
	return &Reconciler{
		cl:  cl,
		log: log,
	}
}

var _ reconcile.Reconciler = &Reconciler{}

func (r *Reconciler) Reconcile(
	ctx context.Context,
	req reconcile.Request,
) (reconcile.Result, error) {
	log := r.log.WithName("Reconcile").WithValues("request", req)

	// Fetch ReplicatedVolume, if possible (RV might be missing)
	rv, err := r.getReplicatedVolume(ctx, req.Name)
	if err != nil {
		log.Error(err, "unable to get ReplicatedVolume")
		return reconcile.Result{}, err
	}

	// Fetch ReplicatedStorageClass, if possible (SC might be missing)
	var sc *v1alpha1.ReplicatedStorageClass
	if rv != nil {
		sc, err = r.getReplicatedVolumeStorageClass(ctx, *rv)
		if err != nil {
			// If ReplicatedStorageClass cannot be loaded, proceed in detach-only mode.
			log.Error(err, "unable to get ReplicatedStorageClass; proceeding in detach-only mode")
			sc = nil
		}
	}

	// Fetch ReplicatedVolumeReplicas
	replicas, err := r.getReplicatedVolumeReplicas(ctx, req.Name)
	if err != nil {
		log.Error(err, "unable to get ReplicatedVolumeReplicas")
		return reconcile.Result{}, err
	}

	// Fetch ReplicatedVolumeAttachments
	rvas, err := r.getSortedReplicatedVolumeAttachments(ctx, req.Name)
	if err != nil {
		log.Error(err, "unable to get ReplicatedVolumeAttachments")
		return reconcile.Result{}, err
	}

	// Compute actuallyAttachedTo (based on RVRs)
	actuallyAttachedTo := computeActuallyAttachedTo(replicas)

	// Compute desiredAttachTo (based on RVAs and RVRs)
	rvaDesiredAttachTo := computeDesiredAttachToBaseOnlyOnRVA(rvas)
	desiredAttachTo := computeDesiredAttachTo(rv, sc, replicas, rvaDesiredAttachTo)

	// Compute desiredAllowTwoPrimaries (based on RVAs and actual attachments)
	desiredAllowTwoPrimaries := computeDesiredTwoPrimaries(desiredAttachTo, actuallyAttachedTo)

	// Reconcile RVA finalizers (don't release deleting RVA while it's still attached).
	if err := r.reconcileRVAsFinalizers(ctx, rvas, actuallyAttachedTo, rvaDesiredAttachTo); err != nil {
		log.Error(err, "unable to reconcile ReplicatedVolumeAttachments finalizers", "rvaCount", len(rvas))
		return reconcile.Result{}, err
	}

	// Reconcile RV status (desiredAttachTo + actuallyAttachedTo), if possible
	if rv != nil {
		if err := r.ensureRV(ctx, rv, desiredAttachTo, actuallyAttachedTo, desiredAllowTwoPrimaries); err != nil {
			log.Error(err, "unable to patch ReplicatedVolume status")
			return reconcile.Result{}, err
		}
	}

	// Reconcile RVAs statuses even when RV is missing or deleting:
	// RVA finalizers/statuses must remain consistent for external waiters and for safe cleanup.
	if err := r.reconcileRVAsStatus(ctx, rvas, rv, sc, desiredAttachTo, actuallyAttachedTo, replicas); err != nil {
		log.Error(err, "unable to reconcile ReplicatedVolumeAttachments status", "rvaCount", len(rvas))
		return reconcile.Result{}, err
	}

	// If RV does not exist, stop reconciliation after we have reconciled RVAs.
	// Having replicas without the corresponding RV is unexpected and likely indicates a bug in other controllers.
	if rv == nil {
		if len(replicas) > 0 {
			log.Error(nil, "ReplicatedVolume not found, but ReplicatedVolumeReplicas exist; this is likely a bug in other controllers",
				"replicaCount", len(replicas))
		}
		return reconcile.Result{}, nil
	}

	// Reconcile RVRs
	if err := r.reconcileRVRs(ctx, replicas, desiredAttachTo, actuallyAttachedTo); err != nil {
		log.Error(err, "unable to reconcile ReplicatedVolumeReplicas", "replicaCount", len(replicas))
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

// getReplicatedVolume fetches ReplicatedVolume by name.
// If the object does not exist, it returns (nil, nil).
func (r *Reconciler) getReplicatedVolume(ctx context.Context, rvName string) (*v1alpha1.ReplicatedVolume, error) {
	rv := &v1alpha1.ReplicatedVolume{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: rvName}, rv); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return nil, nil
		}
		return nil, err
	}
	return rv, nil
}

// getReplicatedVolumeStorageClass fetches ReplicatedStorageClass referenced by the given RV.
// If RV does not reference a storage class (empty name) or the class does not exist, it returns (nil, nil).
func (r *Reconciler) getReplicatedVolumeStorageClass(ctx context.Context, rv v1alpha1.ReplicatedVolume) (*v1alpha1.ReplicatedStorageClass, error) {
	if rv.Spec.ReplicatedStorageClassName == "" {
		return nil, nil
	}

	sc := &v1alpha1.ReplicatedStorageClass{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: rv.Spec.ReplicatedStorageClassName}, sc); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return nil, nil
		}
		return nil, err
	}
	return sc, nil
}

// getReplicatedVolumeReplicas lists all ReplicatedVolumeReplica objects and returns those belonging to the given RV.
func (r *Reconciler) getReplicatedVolumeReplicas(ctx context.Context, rvName string) ([]v1alpha1.ReplicatedVolumeReplica, error) {
	rvrList := &v1alpha1.ReplicatedVolumeReplicaList{}
	if err := r.cl.List(ctx, rvrList); err != nil {
		return nil, err
	}

	var replicasForRV []v1alpha1.ReplicatedVolumeReplica
	for _, rvr := range rvrList.Items {
		if rvr.Spec.ReplicatedVolumeName == rvName {
			replicasForRV = append(replicasForRV, rvr)
		}
	}

	return replicasForRV, nil
}

// getSortedReplicatedVolumeAttachments lists all ReplicatedVolumeAttachment objects and returns those belonging
// to the given RV, sorted by creation timestamp (FIFO).
func (r *Reconciler) getSortedReplicatedVolumeAttachments(ctx context.Context, rvName string) ([]v1alpha1.ReplicatedVolumeAttachment, error) {
	rvaList := &v1alpha1.ReplicatedVolumeAttachmentList{}
	if err := r.cl.List(ctx, rvaList, client.MatchingFields{
		v1alpha1.IndexFieldRVAByReplicatedVolumeName: rvName,
	}); err != nil {
		return nil, err
	}

	rvasForRV := rvaList.Items

	// Sort by creation timestamp
	sort.SliceStable(rvasForRV, func(i, j int) bool {
		ti := rvasForRV[i].CreationTimestamp.Time
		tj := rvasForRV[j].CreationTimestamp.Time
		if ti.Equal(tj) {
			return false
		}
		return ti.Before(tj)
	})

	return rvasForRV, nil
}

// computeActuallyAttachedTo returns a sorted list of node names where the volume is actually attached.
// We treat a node as "attached" when its replica reports DRBD role "Primary".
// The returned slice is kept sorted and unique while building it (BinarySearch + Insert).
func computeActuallyAttachedTo(replicas []v1alpha1.ReplicatedVolumeReplica) []string {
	out := make([]string, 0, 2)

	for _, rvr := range replicas {
		if rvr.Spec.NodeName == "" {
			continue
		}
		if rvr.Status == nil || rvr.Status.DRBD == nil || rvr.Status.DRBD.Status == nil {
			continue
		}
		if rvr.Status.DRBD.Status.Role != "Primary" {
			continue
		}

		i, found := slices.BinarySearch(out, rvr.Spec.NodeName)
		if found {
			continue
		}
		out = slices.Insert(out, i, rvr.Spec.NodeName)
	}

	return out
}

// computeDesiredAttachTo calculates rv.status.desiredAttachTo using current RV status and the RVA set.
//
// High-level rules:
//   - Start from current desiredAttachTo stored in RV status (if any).
//   - Remove nodes that no longer have an active (non-deleting) RVA.
//   - If attaching is not allowed (RV is nil/deleting, no controller finalizer, no status, not IOReady, or no StorageClass),
//     return the filtered desiredAttachTo as-is (detach-only mode: we do not add new nodes).
//   - If attaching is allowed, we may add new nodes from RVA set (FIFO order, assuming rvas are sorted by creationTimestamp),
//     but we keep at most 2 nodes in desiredAttachTo.
//   - For Local volume access, new attachments are only allowed on nodes that have a Diskful replica according to
//     ReplicatedVolumeReplica status.actualType.
//   - New attachments are not allowed on nodes whose replicas are marked for deletion.
func computeDesiredAttachTo(
	rv *v1alpha1.ReplicatedVolume,
	sc *v1alpha1.ReplicatedStorageClass,
	replicas []v1alpha1.ReplicatedVolumeReplica,
	rvaDesiredAttachTo []string,
) []string {
	desired := []string(nil)

	// Get current desiredAttachTo from ReplicatedVolume status.
	if rv != nil && rv.Status != nil {
		desired = rv.Status.DesiredAttachTo
	}

	// Exclude nodes that are not any more desired by existing RVA.
	desired = slices.DeleteFunc(desired, func(node string) bool {
		return !slices.Contains(rvaDesiredAttachTo, node)
	})

	attachEnabled :=
		rv != nil &&
			rv.DeletionTimestamp.IsZero() &&
			v1alpha1.HasControllerFinalizer(rv) &&
			rv.Status != nil &&
			meta.IsStatusConditionTrue(rv.Status.Conditions, v1alpha1.ConditionTypeRVIOReady) &&
			sc != nil

	// Finish early if we are not allowed to attach.
	if !attachEnabled {
		return desired
	}

	nodesWithDiskfulReplicas := make([]string, 0, len(replicas))
	nodesWithDeletingReplicas := make([]string, 0, len(replicas))
	nodesWithAnyReplica := make([]string, 0, len(replicas))
	for _, rvr := range replicas {
		// Skip replicas without node
		if rvr.Spec.NodeName == "" {
			continue
		}

		// No uniqueness check required: per design there can't be two replicas on the same node.

		// Add to nodesWithAnyReplica to check if the node has any replica at all.
		nodesWithAnyReplica = append(nodesWithAnyReplica, rvr.Spec.NodeName)

		// Add to nodesWithDeletingReplicas to check if the node is marked for deletion.
		if !rvr.DeletionTimestamp.IsZero() {
			nodesWithDeletingReplicas = append(nodesWithDeletingReplicas, rvr.Spec.NodeName)
		}

		// Add to nodesWithDiskfulReplicas to check if the node has a Diskful replica.
		if rvr.Status != nil && rvr.Spec.Type == v1alpha1.ReplicaTypeDiskful && rvr.Status.ActualType == v1alpha1.ReplicaTypeDiskful {
			nodesWithDiskfulReplicas = append(nodesWithDiskfulReplicas, rvr.Spec.NodeName)
		}
	}

	filteredRvaDesiredAttachTo := append([]string(nil), rvaDesiredAttachTo...)

	// For Local volume access, we must not keep a desired node that has no replica at all.
	// (Unlike the "non-Diskful replica" case: an existing desired node may remain even if it violates Locality.)
	if sc.Spec.VolumeAccess == v1alpha1.VolumeAccessLocal {
		desired = slices.DeleteFunc(desired, func(node string) bool {
			return !slices.Contains(nodesWithAnyReplica, node)
		})
	}

	// For Local volume access, new attachments are only possible on nodes that have a Diskful replica.
	if sc.Spec.VolumeAccess == v1alpha1.VolumeAccessLocal {
		filteredRvaDesiredAttachTo = slices.DeleteFunc(filteredRvaDesiredAttachTo, func(node string) bool {
			return !slices.Contains(nodesWithDiskfulReplicas, node)
		})
	}

	// New attachments are only possible on replicas not marked for deletion.
	filteredRvaDesiredAttachTo = slices.DeleteFunc(filteredRvaDesiredAttachTo, func(node string) bool {
		return slices.Contains(nodesWithDeletingReplicas, node)
	})

	// Fill desired from RVA (FIFO) until we reach 2 nodes, skipping duplicates.
	for _, node := range filteredRvaDesiredAttachTo {
		if len(desired) >= 2 {
			break
		}
		if slices.Contains(desired, node) {
			continue
		}
		desired = append(desired, node)
	}

	return desired
}

// computeDesiredAttachToBaseOnlyOnRVA computes desiredAttachTo based only on active RVAs.
// It picks unique node names from the given RVA list, preserving the order of RVAs
// (caller is expected to pass RVAs sorted by creation timestamp if FIFO semantics are desired).
func computeDesiredAttachToBaseOnlyOnRVA(rvas []v1alpha1.ReplicatedVolumeAttachment) []string {
	desired := make([]string, 0, len(rvas))
	seen := map[string]struct{}{}

	for _, rva := range rvas {
		if rva.Spec.NodeName == "" {
			continue
		}
		// Only active (non-deleting) RVAs participate in desiredAttachTo.
		if !rva.DeletionTimestamp.IsZero() {
			continue
		}
		if _, ok := seen[rva.Spec.NodeName]; ok {
			continue
		}
		seen[rva.Spec.NodeName] = struct{}{}
		desired = append(desired, rva.Spec.NodeName)
	}

	return desired
}

// reconcileRVAsFinalizers reconciles finalizers for all provided RVAs.
// It continues through all RVAs, joining any errors encountered.
func (r *Reconciler) reconcileRVAsFinalizers(
	ctx context.Context,
	rvas []v1alpha1.ReplicatedVolumeAttachment,
	actuallyAttachedTo []string,
	rvaDesiredAttachTo []string,
) error {
	var joinedErr error
	for i := range rvas {
		rva := &rvas[i]
		if err := r.reconcileRVAFinalizers(ctx, rva, actuallyAttachedTo, rvaDesiredAttachTo); err != nil {
			joinedErr = errors.Join(joinedErr, err)
		}
	}
	return joinedErr
}

// reconcileRVAsStatus reconciles status (phase + conditions) for all provided RVAs.
// It continues through all RVAs, joining any errors encountered.
func (r *Reconciler) reconcileRVAsStatus(
	ctx context.Context,
	rvas []v1alpha1.ReplicatedVolumeAttachment,
	rv *v1alpha1.ReplicatedVolume,
	sc *v1alpha1.ReplicatedStorageClass,
	desiredAttachTo []string,
	actuallyAttachedTo []string,
	replicas []v1alpha1.ReplicatedVolumeReplica,
) error {
	var joinedErr error
	for i := range rvas {
		rva := &rvas[i]

		// Find replica on RVA node (include deleting replicas if any).
		var replicaOnNode *v1alpha1.ReplicatedVolumeReplica
		for i := range replicas {
			if replicas[i].Spec.NodeName == rva.Spec.NodeName && replicas[i].Spec.NodeName != "" {
				replicaOnNode = &replicas[i]
				break
			}
		}

		if err := r.reconcileRVAStatus(ctx, rva, rv, sc, desiredAttachTo, actuallyAttachedTo, replicaOnNode); err != nil {
			joinedErr = errors.Join(joinedErr, err)
		}
	}
	return joinedErr
}

// reconcileRVAFinalizers ensures RVA finalizers are in the desired state:
// - If RVA is not deleting, it ensures ControllerAppFinalizer is present.
// - If RVA is deleting, it removes ControllerAppFinalizer only when the node is not actually attached anymore (or a duplicate RVA exists).
//
// It persists changes to the API via ensureRVAFinalizers (optimistic lock) and performs no-op when no changes are needed.
func (r *Reconciler) reconcileRVAFinalizers(
	ctx context.Context,
	rva *v1alpha1.ReplicatedVolumeAttachment,
	actuallyAttachedTo []string,
	rvaDesiredAttachTo []string,
) error {
	if rva == nil {
		panic("reconcileRVAFinalizers: nil rva (programmer error)")
	}

	if rva.DeletionTimestamp.IsZero() {
		// Add controller finalizer if RVA is not deleting.
		desiredFinalizers := append([]string(nil), rva.Finalizers...)
		if !slices.Contains(desiredFinalizers, v1alpha1.ControllerAppFinalizer) {
			desiredFinalizers = append(desiredFinalizers, v1alpha1.ControllerAppFinalizer)
		}
		return r.ensureRVAFinalizers(ctx, rva, desiredFinalizers)
	}

	// RVA is deleting: remove controller finalizer only when safe.
	// Safe when:
	// - the node is not actually attached anymore, OR
	// - the node is still attached, but there is another active RVA for the same node (so we don't need to wait for detach).
	if !slices.Contains(actuallyAttachedTo, rva.Spec.NodeName) || slices.Contains(rvaDesiredAttachTo, rva.Spec.NodeName) {
		currentFinalizers := append([]string(nil), rva.Finalizers...)
		desiredFinalizers := slices.DeleteFunc(currentFinalizers, func(f string) bool {
			return f == v1alpha1.ControllerAppFinalizer
		})
		return r.ensureRVAFinalizers(ctx, rva, desiredFinalizers)
	}

	return nil
}

// ensureRVAFinalizers ensures RVA finalizers match the desired set.
// It patches the object with optimistic lock only when finalizers actually change.
func (r *Reconciler) ensureRVAFinalizers(
	ctx context.Context,
	rva *v1alpha1.ReplicatedVolumeAttachment,
	desiredFinalizers []string,
) error {
	if rva == nil {
		panic("ensureRVAFinalizers: nil rva (programmer error)")
	}

	if slices.Equal(rva.Finalizers, desiredFinalizers) {
		return nil
	}

	original := rva.DeepCopy()
	rva.Finalizers = append([]string(nil), desiredFinalizers...)
	if err := r.cl.Patch(ctx, rva, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})); err != nil {
		return err
	}

	return nil
}

// reconcileRVAStatus computes desired phase and RVA conditions for a single RVA and persists them via ensureRVAStatus.
func (r *Reconciler) reconcileRVAStatus(
	ctx context.Context,
	rva *v1alpha1.ReplicatedVolumeAttachment,
	rv *v1alpha1.ReplicatedVolume,
	sc *v1alpha1.ReplicatedStorageClass,
	desiredAttachTo []string,
	actuallyAttachedTo []string,
	replicaOnNode *v1alpha1.ReplicatedVolumeReplica,
) error {
	if rva == nil {
		panic("reconcileRVAStatus: nil rva (programmer error)")
	}

	desiredPhase := ""
	var desiredAttachedCondition metav1.Condition

	// ReplicaIOReady mirrors replica condition IOReady (if available).
	desiredReplicaIOReadyCondition := metav1.Condition{
		Status:  metav1.ConditionUnknown,
		Reason:  v1alpha1.RVAReplicaIOReadyReasonWaitingForReplica,
		Message: "Waiting for replica IOReady condition on the requested node",
	}

	// Helper: if we have replica and its IOReady condition, mirror it.
	if replicaOnNode != nil && replicaOnNode.Status != nil {
		if rvrIOReady := meta.FindStatusCondition(replicaOnNode.Status.Conditions, v1alpha1.ConditionTypeIOReady); rvrIOReady != nil {
			desiredReplicaIOReadyCondition.Status = rvrIOReady.Status
			desiredReplicaIOReadyCondition.Reason = rvrIOReady.Reason
			desiredReplicaIOReadyCondition.Message = rvrIOReady.Message
		}
	}

	// Attached always wins (even if RVA/RV are deleting): reflect the actual state.
	if slices.Contains(actuallyAttachedTo, rva.Spec.NodeName) {
		if !rva.DeletionTimestamp.IsZero() {
			desiredPhase = v1alpha1.ReplicatedVolumeAttachmentPhaseDetaching
		} else {
			desiredPhase = v1alpha1.ReplicatedVolumeAttachmentPhaseAttached
		}
		desiredAttachedCondition = metav1.Condition{
			Status:  metav1.ConditionTrue,
			Reason:  v1alpha1.RVAAttachedReasonAttached,
			Message: "Volume is attached to the requested node",
		}
		return r.ensureRVAStatus(ctx, rva, desiredPhase, desiredAttachedCondition, desiredReplicaIOReadyCondition, computeAggregateReadyCondition(desiredAttachedCondition, desiredReplicaIOReadyCondition))
	}

	// RV might be missing (not yet created / already deleted). In this case we can't attach and keep RVA Pending.
	if rv == nil {
		desiredPhase = v1alpha1.ReplicatedVolumeAttachmentPhasePending
		desiredAttachedCondition = metav1.Condition{
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha1.RVAAttachedReasonWaitingForReplicatedVolume,
			Message: "Waiting for ReplicatedVolume to exist",
		}
		return r.ensureRVAStatus(ctx, rva, desiredPhase, desiredAttachedCondition, desiredReplicaIOReadyCondition, computeAggregateReadyCondition(desiredAttachedCondition, desiredReplicaIOReadyCondition))
	}

	// StorageClass might be missing (not yet created / already deleted). In this case we can't attach and keep RVA Pending.
	if sc == nil {
		desiredPhase = v1alpha1.ReplicatedVolumeAttachmentPhasePending
		desiredAttachedCondition = metav1.Condition{
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha1.RVAAttachedReasonWaitingForReplicatedVolume,
			Message: "Waiting for ReplicatedStorageClass to exist",
		}
		return r.ensureRVAStatus(ctx, rva, desiredPhase, desiredAttachedCondition, desiredReplicaIOReadyCondition, computeAggregateReadyCondition(desiredAttachedCondition, desiredReplicaIOReadyCondition))
	}

	// For Local volume access, attachment is only possible when the requested node has a Diskful replica.
	// If this is not satisfied, keep RVA in Pending (do not move to Attaching).
	if sc.Spec.VolumeAccess == v1alpha1.VolumeAccessLocal {
		if replicaOnNode == nil || replicaOnNode.Status == nil || replicaOnNode.Status.ActualType != v1alpha1.ReplicaTypeDiskful {
			desiredPhase = v1alpha1.ReplicatedVolumeAttachmentPhasePending
			desiredAttachedCondition = metav1.Condition{
				Status:  metav1.ConditionFalse,
				Reason:  v1alpha1.RVAAttachedReasonLocalityNotSatisfied,
				Message: "Local volume access requires a Diskful replica on the requested node",
			}
			return r.ensureRVAStatus(ctx, rva, desiredPhase, desiredAttachedCondition, desiredReplicaIOReadyCondition, computeAggregateReadyCondition(desiredAttachedCondition, desiredReplicaIOReadyCondition))
		}
	}

	// If RV status is not initialized or not IOReady, we can't progress attachment; keep informative Pending.
	if rv.Status == nil || !meta.IsStatusConditionTrue(rv.Status.Conditions, v1alpha1.ConditionTypeRVIOReady) {
		desiredPhase = v1alpha1.ReplicatedVolumeAttachmentPhasePending
		desiredAttachedCondition = metav1.Condition{
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha1.RVAAttachedReasonWaitingForReplicatedVolumeIOReady,
			Message: "Waiting for ReplicatedVolume to become IOReady",
		}
		return r.ensureRVAStatus(ctx, rva, desiredPhase, desiredAttachedCondition, desiredReplicaIOReadyCondition, computeAggregateReadyCondition(desiredAttachedCondition, desiredReplicaIOReadyCondition))
	}

	// Not active (not in desiredAttachTo): must wait until one of the active nodes detaches.
	if !slices.Contains(desiredAttachTo, rva.Spec.NodeName) {
		desiredPhase = v1alpha1.ReplicatedVolumeAttachmentPhasePending
		desiredAttachedCondition = metav1.Condition{
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha1.RVAAttachedReasonWaitingForActiveAttachmentsToDetach,
			Message: "Waiting for active nodes to detach (maximum 2 nodes are supported)",
		}
		return r.ensureRVAStatus(ctx, rva, desiredPhase, desiredAttachedCondition, desiredReplicaIOReadyCondition, computeAggregateReadyCondition(desiredAttachedCondition, desiredReplicaIOReadyCondition))
	}

	// Active but not yet attached.
	if replicaOnNode == nil {
		desiredPhase = v1alpha1.ReplicatedVolumeAttachmentPhaseAttaching
		desiredAttachedCondition = metav1.Condition{
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha1.RVAAttachedReasonWaitingForReplica,
			Message: "Waiting for replica on the requested node",
		}
		return r.ensureRVAStatus(ctx, rva, desiredPhase, desiredAttachedCondition, desiredReplicaIOReadyCondition, computeAggregateReadyCondition(desiredAttachedCondition, desiredReplicaIOReadyCondition))
	}

	// TieBreaker replica cannot be promoted directly; it must be converted first.
	if replicaOnNode.Spec.Type == v1alpha1.ReplicaTypeTieBreaker ||
		(replicaOnNode.Status != nil && replicaOnNode.Status.ActualType == v1alpha1.ReplicaTypeTieBreaker) {
		desiredPhase = v1alpha1.ReplicatedVolumeAttachmentPhaseAttaching
		desiredAttachedCondition = metav1.Condition{
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha1.RVAAttachedReasonConvertingTieBreakerToAccess,
			Message: "Converting TieBreaker replica to Access to allow promotion",
		}
		return r.ensureRVAStatus(ctx, rva, desiredPhase, desiredAttachedCondition, desiredReplicaIOReadyCondition, computeAggregateReadyCondition(desiredAttachedCondition, desiredReplicaIOReadyCondition))
	}

	desiredPhase = v1alpha1.ReplicatedVolumeAttachmentPhaseAttaching
	desiredAttachedCondition = metav1.Condition{
		Status:  metav1.ConditionFalse,
		Reason:  v1alpha1.RVAAttachedReasonSettingPrimary,
		Message: "Waiting for replica to become Primary",
	}
	return r.ensureRVAStatus(ctx, rva, desiredPhase, desiredAttachedCondition, desiredReplicaIOReadyCondition, computeAggregateReadyCondition(desiredAttachedCondition, desiredReplicaIOReadyCondition))
}

func statusConditionEqual(current *metav1.Condition, desired metav1.Condition) bool {
	if current == nil {
		return false
	}
	return current.Type == desired.Type &&
		current.Status == desired.Status &&
		current.Reason == desired.Reason &&
		current.Message == desired.Message &&
		current.ObservedGeneration == desired.ObservedGeneration
}

func computeAggregateReadyCondition(attached metav1.Condition, replicaIOReady metav1.Condition) metav1.Condition {
	// Ready is a strict aggregate: Attached=True AND ReplicaIOReady=True
	if attached.Status != metav1.ConditionTrue {
		return metav1.Condition{
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha1.RVAReadyReasonNotAttached,
			Message: "Waiting for volume to be attached to the requested node",
		}
	}
	if replicaIOReady.Status != metav1.ConditionTrue {
		return metav1.Condition{
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha1.RVAReadyReasonReplicaNotIOReady,
			Message: "Waiting for replica on the requested node to become IOReady",
		}
	}
	return metav1.Condition{
		Status:  metav1.ConditionTrue,
		Reason:  v1alpha1.RVAReadyReasonReady,
		Message: "Volume is attached and replica is IOReady on the requested node",
	}
}

// ensureRVAStatus ensures RVA status.phase and conditions match desired values.
// It patches status with optimistic lock only when something actually changes.
func (r *Reconciler) ensureRVAStatus(
	ctx context.Context,
	rva *v1alpha1.ReplicatedVolumeAttachment,
	desiredPhase string,
	desiredAttachedCondition metav1.Condition,
	desiredReplicaIOReadyCondition metav1.Condition,
	desiredReadyCondition metav1.Condition,
) error {
	if rva == nil {
		panic("ensureRVAStatus: nil rva (programmer error)")
	}

	desiredAttachedCondition.Type = v1alpha1.RVAConditionTypeAttached
	desiredReplicaIOReadyCondition.Type = v1alpha1.RVAConditionTypeReplicaIOReady
	desiredReadyCondition.Type = v1alpha1.RVAConditionTypeReady

	desiredAttachedCondition.ObservedGeneration = rva.Generation
	desiredReplicaIOReadyCondition.ObservedGeneration = rva.Generation
	desiredReadyCondition.ObservedGeneration = rva.Generation

	currentPhase := ""
	var currentAttached, currentReplicaIOReady, currentReady *metav1.Condition
	if rva.Status != nil {
		currentPhase = rva.Status.Phase
		currentAttached = meta.FindStatusCondition(rva.Status.Conditions, v1alpha1.RVAConditionTypeAttached)
		currentReplicaIOReady = meta.FindStatusCondition(rva.Status.Conditions, v1alpha1.RVAConditionTypeReplicaIOReady)
		currentReady = meta.FindStatusCondition(rva.Status.Conditions, v1alpha1.RVAConditionTypeReady)
	}

	phaseEqual := currentPhase == desiredPhase
	attachedEqual := statusConditionEqual(currentAttached, desiredAttachedCondition)
	replicaIOReadyEqual := statusConditionEqual(currentReplicaIOReady, desiredReplicaIOReadyCondition)
	readyEqual := statusConditionEqual(currentReady, desiredReadyCondition)
	if phaseEqual && attachedEqual && replicaIOReadyEqual && readyEqual {
		return nil
	}

	original := rva.DeepCopy()
	if rva.Status == nil {
		rva.Status = &v1alpha1.ReplicatedVolumeAttachmentStatus{}
	}
	rva.Status.Phase = desiredPhase
	meta.SetStatusCondition(&rva.Status.Conditions, desiredAttachedCondition)
	meta.SetStatusCondition(&rva.Status.Conditions, desiredReplicaIOReadyCondition)
	meta.SetStatusCondition(&rva.Status.Conditions, desiredReadyCondition)

	if err := r.cl.Status().Patch(ctx, rva, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return nil
		}
		return err
	}

	return nil
}

// ensureRV updates ReplicatedVolume status fields derived from replicas/RVAs:
// - status.desiredAttachTo
// - status.actuallyAttachedTo
//
// It patches status with optimistic lock only when something actually changes.
func (r *Reconciler) ensureRV(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	desiredAttachTo []string,
	actuallyAttachedTo []string,
	desiredAllowTwoPrimaries bool,
) error {
	if rv == nil {
		panic("ensureRV: nil rv (programmer error)")
	}

	currentDesired := []string(nil)
	currentActual := []string(nil)
	currentAllowTwoPrimaries := false
	if rv.Status != nil {
		currentDesired = rv.Status.DesiredAttachTo
		currentActual = rv.Status.ActuallyAttachedTo
		if rv.Status.DRBD != nil && rv.Status.DRBD.Config != nil {
			currentAllowTwoPrimaries = rv.Status.DRBD.Config.AllowTwoPrimaries
		}
	}

	if slices.Equal(currentDesired, desiredAttachTo) &&
		slices.Equal(currentActual, actuallyAttachedTo) &&
		currentAllowTwoPrimaries == desiredAllowTwoPrimaries {
		return nil
	}

	original := rv.DeepCopy()
	if rv.Status == nil {
		rv.Status = &v1alpha1.ReplicatedVolumeStatus{}
	}
	if rv.Status.DRBD == nil {
		rv.Status.DRBD = &v1alpha1.DRBDResource{}
	}
	if rv.Status.DRBD.Config == nil {
		rv.Status.DRBD.Config = &v1alpha1.DRBDResourceConfig{}
	}
	rv.Status.DesiredAttachTo = append([]string(nil), desiredAttachTo...)
	rv.Status.ActuallyAttachedTo = append([]string(nil), actuallyAttachedTo...)
	rv.Status.DRBD.Config.AllowTwoPrimaries = desiredAllowTwoPrimaries

	if err := r.cl.Status().Patch(ctx, rv, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})); err != nil {
		return err
	}

	return nil
}

// reconcileRVRs reconciles status for all ReplicatedVolumeReplica objects of an RV.
//
// It computes the desired DRBD configuration (including allowTwoPrimaries and which nodes should be Primary),
// applies it to each replica via reconcileRVR, and joins errors (does not fail-fast).
//
// Safety notes:
// - never request 2 Primaries until allowTwoPrimaries is confirmed applied everywhere;
// - when switching the active Primary node without allowTwoPrimaries, do it as "demote first, then promote".
func (r *Reconciler) reconcileRVRs(
	ctx context.Context,
	replicas []v1alpha1.ReplicatedVolumeReplica,
	desiredAttachTo []string,
	actuallyAttachedTo []string,
) error {
	actualAllowTwoPrimaries := computeActualTwoPrimaries(replicas)

	// DRBD safety rule #1:
	// - we only allow 2 Primaries after allowTwoPrimaries is confirmed applied everywhere;
	// - until then, we keep at most 1 Primary to reduce split-brain risk;

	// DRBD safety rule #2:
	// - when switching the active Primary node (in any mode), the transition must be "demote first, then promote"
	//   (i.e. never request two Primaries without allowTwoPrimaries).

	// Start from the current reality: nodes that are Primary right now.
	desiredPrimaryNodes := append([]string(nil), actuallyAttachedTo...)

	// Try to promote additional desired nodes if we have capacity (capacity depends on actualAllowTwoPrimaries).
	desiredPrimaryNodes = promoteNewDesiredNodesIfPossible(actualAllowTwoPrimaries, desiredPrimaryNodes, desiredAttachTo)

	// Demote nodes that are Primary but are no longer desired. This is necessary to free up "places" for furutre new promotions.
	desiredPrimaryNodes = demoteNotAnyMoreDesiredNodes(desiredPrimaryNodes, desiredAttachTo)

	var joinedErr error
	for i := range replicas {
		rvr := &replicas[i]
		if err := r.reconcileRVR(ctx, rvr, desiredPrimaryNodes); err != nil {
			joinedErr = errors.Join(joinedErr, err)
		}
	}
	return joinedErr
}

// computeDesiredTwoPrimaries returns whether we want to allow two Primary replicas.
//
// Rule:
// - if we desire two attachments, we must allow two Primaries;
// - if we already have >1 Primary (actuallyAttachedTo), we MUST NOT disable allowTwoPrimaries until we demote down to <=1.
func computeDesiredTwoPrimaries(desiredAttachTo []string, actuallyAttachedTo []string) bool {
	// The desiredAttachTo list can't be more than 2 nodes; this is enforced by computeDesiredAttachTo.
	return len(desiredAttachTo) == 2 || len(actuallyAttachedTo) > 1
}

// computeActualTwoPrimaries returns whether allowTwoPrimaries is actually applied on all relevant replicas.
// A replica is considered relevant when it is assigned to a node.
func computeActualTwoPrimaries(replicas []v1alpha1.ReplicatedVolumeReplica) bool {
	for _, rvr := range replicas {
		// Skip replicas without a node (unscheduled replicas or TieBreaker without node assignment).
		if rvr.Spec.NodeName == "" {
			continue
		}
		if rvr.Status == nil || rvr.Status.DRBD == nil || rvr.Status.DRBD.Actual == nil || !rvr.Status.DRBD.Actual.AllowTwoPrimaries {
			return false
		}
	}
	return true
}

// promoteNewDesiredNodesIfPossible returns actualPrimaryNodes extended with 0..2 additional desired nodes, if possible.
//
// The function respects the current allowTwoPrimaries readiness:
// - if actualAllowTwoPrimaries is false: maxNodesAllowed=1
// - if actualAllowTwoPrimaries is true: maxNodesAllowed=2
//
// Output size is 0..2.
func promoteNewDesiredNodesIfPossible(
	actualAllowTwoPrimaries bool,
	actualPrimaryNodes []string,
	desiredPrimaryNodes []string,
) []string {
	maxNodesAllowed := 1
	if actualAllowTwoPrimaries {
		maxNodesAllowed = 2
	}

	// Start with actual Primary nodes.
	out := append([]string(nil), actualPrimaryNodes...)

	// Add missing desired nodes (FIFO) until we reach maxNodesAllowed or run out of candidates.
	if len(out) >= maxNodesAllowed {
		return out
	}
	for _, node := range desiredPrimaryNodes {
		if slices.Contains(out, node) {
			continue
		}
		out = append(out, node)
		if len(out) >= maxNodesAllowed {
			break
		}
	}

	return out
}

// demoteNotAnyMoreDesiredNodes returns actualPrimaryNodes with nodes that are not present in desiredPrimaryNodes removed.
// The order of remaining nodes is preserved.
func demoteNotAnyMoreDesiredNodes(
	actualPrimaryNodes []string,
	desiredPrimaryNodes []string,
) []string {
	out := make([]string, 0, len(actualPrimaryNodes))
	for _, node := range actualPrimaryNodes {
		if slices.Contains(desiredPrimaryNodes, node) {
			out = append(out, node)
		}
	}
	return out
}

// reconcileRVR reconciles a single replica (spec.type + status: DRBD config.primary and Attached condition)
// for the given RV plan. desiredPrimary is derived from whether the replica node is present in desiredPrimaryNodes.
func (r *Reconciler) reconcileRVR(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	desiredPrimaryNodes []string,
) error {
	if rvr == nil {
		panic("reconcileRVR: rvr is nil")
	}

	desiredPrimaryWanted := slices.Contains(desiredPrimaryNodes, rvr.Spec.NodeName)

	// TieBreaker cannot be promoted, so convert it to Access first.
	desiredType := rvr.Spec.Type
	if desiredPrimaryWanted && rvr.Spec.Type == v1alpha1.ReplicaTypeTieBreaker {
		desiredType = v1alpha1.ReplicaTypeAccess
	}
	if err := r.ensureRVRType(ctx, rvr, desiredType); err != nil {
		return err
	}

	desiredPrimary := desiredPrimaryWanted

	// We only request Primary on replicas that are actually Diskful or Access (by status.actualType).
	// This prevents trying to promote TieBreaker (or not-yet-initialized replicas).
	if desiredPrimary {
		if rvr.Status == nil ||
			(rvr.Status.ActualType != v1alpha1.ReplicaTypeDiskful && rvr.Status.ActualType != v1alpha1.ReplicaTypeAccess) {
			desiredPrimary = false
		}
	}

	// Build desired Attached condition using the canonical helper.
	desiredAttachedCondition, err := rvr.ComputeStatusConditionAttached(desiredPrimary)
	if err != nil {
		return err
	}

	return r.ensureRVRStatus(ctx, rvr, desiredPrimary, desiredAttachedCondition)
}

// ensureRVRType ensures rvr.spec.type matches the desired value.
// It patches the object with optimistic lock only when something actually changes.
func (r *Reconciler) ensureRVRType(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	desiredType v1alpha1.ReplicaType,
) error {
	if rvr == nil {
		panic("ensureRVRType: rvr is nil")
	}

	if rvr.Spec.Type == desiredType {
		return nil
	}

	original := rvr.DeepCopy()
	rvr.Spec.Type = desiredType

	if err := r.cl.Patch(ctx, rvr, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})); err != nil {
		return err
	}

	return nil
}

// ensureRVRStatus ensures rvr.status.drbd.config.primary and the Attached condition match the desired values.
// It patches status with optimistic lock only when something actually changes.
func (r *Reconciler) ensureRVRStatus(
	ctx context.Context,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	desiredPrimary bool,
	desiredAttachedCondition metav1.Condition,
) error {
	if rvr == nil {
		panic("ensureRVRStatus: rvr is nil")
	}

	primary := false
	if rvr.Status != nil && rvr.Status.DRBD != nil && rvr.Status.DRBD.Config != nil && rvr.Status.DRBD.Config.Primary != nil {
		primary = *rvr.Status.DRBD.Config.Primary
	}
	var attachedCond *metav1.Condition
	if rvr.Status != nil {
		attachedCond = meta.FindStatusCondition(rvr.Status.Conditions, v1alpha1.ConditionTypeAttached)
	}

	desiredAttachedCondition.Type = v1alpha1.ConditionTypeAttached
	desiredAttachedCondition.ObservedGeneration = rvr.Generation

	if primary == desiredPrimary &&
		statusConditionEqual(attachedCond, desiredAttachedCondition) {
		return nil
	}

	original := rvr.DeepCopy()
	if rvr.Status == nil {
		rvr.Status = &v1alpha1.ReplicatedVolumeReplicaStatus{}
	}
	if rvr.Status.DRBD == nil {
		rvr.Status.DRBD = &v1alpha1.DRBD{}
	}
	if rvr.Status.DRBD.Config == nil {
		rvr.Status.DRBD.Config = &v1alpha1.DRBDConfig{}
	}

	rvr.Status.DRBD.Config.Primary = &desiredPrimary
	meta.SetStatusCondition(&rvr.Status.Conditions, desiredAttachedCondition)

	if err := r.cl.Status().Patch(ctx, rvr, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})); err != nil {
		return err
	}

	return nil
}
