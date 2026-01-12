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

package rvstatusconditions

import (
	"context"
	"reflect"
	"strconv"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes"
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

var _ reconcile.Reconciler = (*Reconciler)(nil)

func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := r.log.WithName("Reconcile").WithValues("rv", req.Name)
	log.V(1).Info("Reconciling ReplicatedVolume conditions")

	// Get RV
	rv := &v1alpha1.ReplicatedVolume{}
	if err := r.cl.Get(ctx, req.NamespacedName, rv); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	// Get RSC for threshold calculation
	rsc := &v1alpha1.ReplicatedStorageClass{}
	if err := r.cl.Get(ctx, client.ObjectKey{Name: rv.Spec.ReplicatedStorageClassName}, rsc); err != nil {
		log.Error(err, "failed to get ReplicatedStorageClass")
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	// List all RVRs for this RV
	rvrList := &v1alpha1.ReplicatedVolumeReplicaList{}
	if err := r.cl.List(ctx, rvrList, client.MatchingFields{
		indexes.IndexFieldRVRByReplicatedVolumeName: rv.Name,
	}); err != nil {
		log.Error(err, "failed to list ReplicatedVolumeReplicas")
		return reconcile.Result{}, err
	}

	rvrs := rvrList.Items

	// Calculate conditions and counters
	patchedRV := rv.DeepCopy()
	if patchedRV.Status == nil {
		patchedRV.Status = &v1alpha1.ReplicatedVolumeStatus{}
	}

	// Calculate all conditions using simple RV-level reasons from spec
	r.calculateScheduled(patchedRV, rvrs)
	r.calculateBackingVolumeCreated(patchedRV, rvrs)
	r.calculateConfigured(patchedRV, rvrs)
	r.calculateInitialized(patchedRV, rvrs, rsc)
	r.calculateQuorum(patchedRV, rvrs)
	r.calculateDataQuorum(patchedRV, rvrs)
	r.calculateIOReady(patchedRV, rvrs, rsc)

	// Calculate counters
	r.calculateCounters(patchedRV, rv, rvrs)

	// Optimization: skip patch if nothing changed to avoid unnecessary API calls.
	// Note: meta.SetStatusCondition only updates LastTransitionTime when condition
	// actually changes (status/reason/message), so DeepEqual works correctly here.
	// TODO: reconsider this approach, maybe we should not use DeepEqual and just patch all conditions?
	if reflect.DeepEqual(rv.Status, patchedRV.Status) {
		log.V(1).Info("No status changes detected, skipping patch")
		return reconcile.Result{}, nil
	}

	// Patch status using MergeFrom strategy - only changed fields are sent to API server
	if err := r.cl.Status().Patch(ctx, patchedRV, client.MergeFrom(rv)); err != nil {
		log.Error(err, "failed to patch ReplicatedVolume status")
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	log.V(1).Info("Successfully patched ReplicatedVolume conditions")
	return reconcile.Result{}, nil
}

// getRVRCondition gets a condition from RVR status by type
func getRVRCondition(rvr *v1alpha1.ReplicatedVolumeReplica, conditionType string) *metav1.Condition {
	if rvr.Status == nil {
		return nil
	}
	for i := range rvr.Status.Conditions {
		if rvr.Status.Conditions[i].Type == conditionType {
			return &rvr.Status.Conditions[i]
		}
	}
	return nil
}

// countRVRCondition counts how many RVRs have the specified condition with status True
func countRVRCondition(rvrs []v1alpha1.ReplicatedVolumeReplica, conditionType string) int {
	count := 0
	for _, rvr := range rvrs {
		// TODO: use meta.FindStatusCondition
		cond := getRVRCondition(&rvr, conditionType)
		if cond != nil && cond.Status == metav1.ConditionTrue {
			count++
		}
	}
	return count
}

// filterDiskfulRVRs returns only Diskful type replicas from the list
func filterDiskfulRVRs(rvrs []v1alpha1.ReplicatedVolumeReplica) []v1alpha1.ReplicatedVolumeReplica {
	var diskfulRVRs []v1alpha1.ReplicatedVolumeReplica
	for _, rvr := range rvrs {
		if rvr.Spec.Type == v1alpha1.ReplicaTypeDiskful {
			diskfulRVRs = append(diskfulRVRs, rvr)
		}
	}
	return diskfulRVRs
}

// calculateScheduled: RV is Scheduled when ALL RVRs are scheduled
// Reasons: AllReplicasScheduled, ReplicasNotScheduled, SchedulingInProgress
func (r *Reconciler) calculateScheduled(rv *v1alpha1.ReplicatedVolume, rvrs []v1alpha1.ReplicatedVolumeReplica) {
	total := len(rvrs)
	if total == 0 {
		meta.SetStatusCondition(&rv.Status.Conditions, metav1.Condition{
			Type:               v1alpha1.ConditionTypeRVScheduled,
			Status:             metav1.ConditionFalse,
			Reason:             v1alpha1.ReasonSchedulingInProgress,
			Message:            messageNoReplicasFound,
			ObservedGeneration: rv.Generation,
		})
		return
	}

	scheduledCount := countRVRCondition(rvrs, v1alpha1.ConditionTypeScheduled)

	if scheduledCount == total {
		meta.SetStatusCondition(&rv.Status.Conditions, metav1.Condition{
			Type:               v1alpha1.ConditionTypeRVScheduled,
			Status:             metav1.ConditionTrue,
			Reason:             v1alpha1.ReasonAllReplicasScheduled,
			ObservedGeneration: rv.Generation,
		})
		return
	}

	meta.SetStatusCondition(&rv.Status.Conditions, metav1.Condition{
		Type:               v1alpha1.ConditionTypeRVScheduled,
		Status:             metav1.ConditionFalse,
		Reason:             v1alpha1.ReasonReplicasNotScheduled,
		Message:            strconv.Itoa(scheduledCount) + "/" + strconv.Itoa(total) + " replicas scheduled",
		ObservedGeneration: rv.Generation,
	})
}

// calculateBackingVolumeCreated: RV is BackingVolumeCreated when ALL Diskful RVRs have backing volumes
// Reasons: AllBackingVolumesReady, BackingVolumesNotReady, WaitingForBackingVolumes
func (r *Reconciler) calculateBackingVolumeCreated(rv *v1alpha1.ReplicatedVolume, rvrs []v1alpha1.ReplicatedVolumeReplica) {
	diskfulRVRs := filterDiskfulRVRs(rvrs)
	total := len(diskfulRVRs)

	if total == 0 {
		meta.SetStatusCondition(&rv.Status.Conditions, metav1.Condition{
			Type:               v1alpha1.ConditionTypeRVBackingVolumeCreated,
			Status:             metav1.ConditionFalse,
			Reason:             v1alpha1.ReasonWaitingForBackingVolumes,
			Message:            messageNoDiskfulReplicasFound,
			ObservedGeneration: rv.Generation,
		})
		return
	}

	readyCount := countRVRCondition(diskfulRVRs, v1alpha1.ConditionTypeRVRBackingVolumeCreated)

	if readyCount == total {
		meta.SetStatusCondition(&rv.Status.Conditions, metav1.Condition{
			Type:               v1alpha1.ConditionTypeRVBackingVolumeCreated,
			Status:             metav1.ConditionTrue,
			Reason:             v1alpha1.ReasonAllBackingVolumesReady,
			ObservedGeneration: rv.Generation,
		})
		return
	}

	meta.SetStatusCondition(&rv.Status.Conditions, metav1.Condition{
		Type:               v1alpha1.ConditionTypeRVBackingVolumeCreated,
		Status:             metav1.ConditionFalse,
		Reason:             v1alpha1.ReasonBackingVolumesNotReady,
		Message:            strconv.Itoa(readyCount) + "/" + strconv.Itoa(total) + " backing volumes ready",
		ObservedGeneration: rv.Generation,
	})
}

// calculateConfigured: RV is Configured when ALL RVRs are configured
// Reasons: AllReplicasConfigured, ReplicasNotConfigured, ConfigurationInProgress
func (r *Reconciler) calculateConfigured(rv *v1alpha1.ReplicatedVolume, rvrs []v1alpha1.ReplicatedVolumeReplica) {
	total := len(rvrs)
	if total == 0 {
		meta.SetStatusCondition(&rv.Status.Conditions, metav1.Condition{
			Type:               v1alpha1.ConditionTypeRVConfigured,
			Status:             metav1.ConditionFalse,
			Reason:             v1alpha1.ReasonConfigurationInProgress,
			Message:            messageNoReplicasFound,
			ObservedGeneration: rv.Generation,
		})
		return
	}

	configuredCount := countRVRCondition(rvrs, v1alpha1.ConditionTypeConfigurationAdjusted)

	if configuredCount == total {
		meta.SetStatusCondition(&rv.Status.Conditions, metav1.Condition{
			Type:               v1alpha1.ConditionTypeRVConfigured,
			Status:             metav1.ConditionTrue,
			Reason:             v1alpha1.ReasonAllReplicasConfigured,
			ObservedGeneration: rv.Generation,
		})
		return
	}

	meta.SetStatusCondition(&rv.Status.Conditions, metav1.Condition{
		Type:               v1alpha1.ConditionTypeRVConfigured,
		Status:             metav1.ConditionFalse,
		Reason:             v1alpha1.ReasonReplicasNotConfigured,
		Message:            strconv.Itoa(configuredCount) + "/" + strconv.Itoa(total) + " replicas configured",
		ObservedGeneration: rv.Generation,
	})
}

// getInitializedThreshold returns the number of replicas needed to be initialized based on RSC replication mode
func (r *Reconciler) getInitializedThreshold(rsc *v1alpha1.ReplicatedStorageClass) int {
	switch rsc.Spec.Replication {
	case v1alpha1.ReplicationNone:
		return 1
	case v1alpha1.ReplicationAvailability:
		return 2
	case v1alpha1.ReplicationConsistencyAndAvailability:
		return 3
	default:
		r.log.Error(nil, "Unknown replication type, using threshold=1", "replication", rsc.Spec.Replication)
		return 1
	}
}

// calculateInitialized: RV is Initialized when THRESHOLD number of RVRs are initialized
// Reads RVR.DataInitialized condition (set by drbd-config-controller on agent)
// Threshold: None=1, Availability=2, ConsistencyAndAvailability=3
// Reasons: Initialized, InitializationInProgress, WaitingForReplicas
// NOTE: Once True, this condition is never reset to False (per spec).
// This protects against accidental primary --force on new replicas when RV was already initialized.
func (r *Reconciler) calculateInitialized(rv *v1alpha1.ReplicatedVolume, rvrs []v1alpha1.ReplicatedVolumeReplica, rsc *v1alpha1.ReplicatedStorageClass) {
	// Once True, never reset to False - this is intentional per spec
	alreadyTrue := meta.IsStatusConditionTrue(rv.Status.Conditions, v1alpha1.ConditionTypeRVInitialized)
	if alreadyTrue {
		return
	}

	threshold := r.getInitializedThreshold(rsc)
	initializedCount := countRVRCondition(rvrs, v1alpha1.ConditionTypeDataInitialized)

	if initializedCount >= threshold {
		meta.SetStatusCondition(&rv.Status.Conditions, metav1.Condition{
			Type:               v1alpha1.ConditionTypeRVInitialized,
			Status:             metav1.ConditionTrue,
			Reason:             v1alpha1.ReasonInitialized,
			Message:            strconv.Itoa(initializedCount) + "/" + strconv.Itoa(threshold) + " replicas initialized",
			ObservedGeneration: rv.Generation,
		})
		return
	}

	// Determine reason: WaitingForReplicas if no replicas, InitializationInProgress if some progress
	reason := v1alpha1.ReasonInitializationInProgress
	if len(rvrs) == 0 {
		reason = v1alpha1.ReasonWaitingForReplicas
	}

	meta.SetStatusCondition(&rv.Status.Conditions, metav1.Condition{
		Type:               v1alpha1.ConditionTypeRVInitialized,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            strconv.Itoa(initializedCount) + "/" + strconv.Itoa(threshold) + " replicas initialized",
		ObservedGeneration: rv.Generation,
	})
}

// calculateQuorum: RV has Quorum when majority of RVRs (total/2 + 1) are in quorum
// Reasons: QuorumReached, QuorumDegraded, QuorumLost
func (r *Reconciler) calculateQuorum(rv *v1alpha1.ReplicatedVolume, rvrs []v1alpha1.ReplicatedVolumeReplica) {
	total := len(rvrs)
	if total == 0 {
		meta.SetStatusCondition(&rv.Status.Conditions, metav1.Condition{
			Type:               v1alpha1.ConditionTypeRVQuorum,
			Status:             metav1.ConditionFalse,
			Reason:             v1alpha1.ReasonQuorumLost,
			Message:            messageNoReplicasFound,
			ObservedGeneration: rv.Generation,
		})
		return
	}

	var quorumNeeded int
	if rv.Status != nil && rv.Status.DRBD != nil && rv.Status.DRBD.Config != nil {
		quorumNeeded = int(rv.Status.DRBD.Config.Quorum)
	}
	if quorumNeeded == 0 {
		quorumNeeded = (total / 2) + 1
	}

	// Read RVR.InQuorum condition per spec
	inQuorumCount := countRVRCondition(rvrs, v1alpha1.ConditionTypeInQuorum)

	if inQuorumCount >= quorumNeeded {
		reason := v1alpha1.ReasonQuorumReached
		if inQuorumCount < total {
			// Quorum achieved but some replicas are out - degraded state
			reason = v1alpha1.ReasonQuorumDegraded
		}
		meta.SetStatusCondition(&rv.Status.Conditions, metav1.Condition{
			Type:               v1alpha1.ConditionTypeRVQuorum,
			Status:             metav1.ConditionTrue,
			Reason:             reason,
			Message:            strconv.Itoa(inQuorumCount) + "/" + strconv.Itoa(total) + " replicas in quorum",
			ObservedGeneration: rv.Generation,
		})
		return
	}

	meta.SetStatusCondition(&rv.Status.Conditions, metav1.Condition{
		Type:               v1alpha1.ConditionTypeRVQuorum,
		Status:             metav1.ConditionFalse,
		Reason:             v1alpha1.ReasonQuorumLost,
		Message:            strconv.Itoa(inQuorumCount) + "/" + strconv.Itoa(total) + " replicas in quorum",
		ObservedGeneration: rv.Generation,
	})
}

// calculateDataQuorum: RV has DataQuorum when QMR number of Diskful RVRs are in quorum
// QMR (QuorumMinimumRedundancy) from DRBD config, or majority if not set
// Reasons: DataQuorumReached, DataQuorumDegraded, DataQuorumLost
func (r *Reconciler) calculateDataQuorum(rv *v1alpha1.ReplicatedVolume, rvrs []v1alpha1.ReplicatedVolumeReplica) {
	diskfulRVRs := filterDiskfulRVRs(rvrs)
	totalDiskful := len(diskfulRVRs)

	if totalDiskful == 0 {
		meta.SetStatusCondition(&rv.Status.Conditions, metav1.Condition{
			Type:               v1alpha1.ConditionTypeRVDataQuorum,
			Status:             metav1.ConditionFalse,
			Reason:             v1alpha1.ReasonDataQuorumLost,
			Message:            messageNoDiskfulReplicasFound,
			ObservedGeneration: rv.Generation,
		})
		return
	}

	// QMR from DRBD config or fallback to majority
	var qmr int
	if rv.Status != nil && rv.Status.DRBD != nil && rv.Status.DRBD.Config != nil {
		qmr = int(rv.Status.DRBD.Config.QuorumMinimumRedundancy)
	}
	if qmr == 0 {
		qmr = (totalDiskful / 2) + 1
	}

	// Read RVR.InQuorum condition per spec
	inDataQuorumCount := countRVRCondition(diskfulRVRs, v1alpha1.ConditionTypeInSync)

	if inDataQuorumCount >= qmr {
		reason := v1alpha1.ReasonDataQuorumReached
		if inDataQuorumCount < totalDiskful {
			reason = v1alpha1.ReasonDataQuorumDegraded
		}
		meta.SetStatusCondition(&rv.Status.Conditions, metav1.Condition{
			Type:               v1alpha1.ConditionTypeRVDataQuorum,
			Status:             metav1.ConditionTrue,
			Reason:             reason,
			Message:            strconv.Itoa(inDataQuorumCount) + "/" + strconv.Itoa(totalDiskful) + " diskful replicas in quorum (QMR=" + strconv.Itoa(qmr) + ")",
			ObservedGeneration: rv.Generation,
		})
		return
	}

	meta.SetStatusCondition(&rv.Status.Conditions, metav1.Condition{
		Type:               v1alpha1.ConditionTypeRVDataQuorum,
		Status:             metav1.ConditionFalse,
		Reason:             v1alpha1.ReasonDataQuorumLost,
		Message:            strconv.Itoa(inDataQuorumCount) + "/" + strconv.Itoa(totalDiskful) + " diskful replicas in quorum (QMR=" + strconv.Itoa(qmr) + ")",
		ObservedGeneration: rv.Generation,
	})
}

// calculateIOReady: RV is IOReady when THRESHOLD number of Diskful RVRs have IOReady=True
// Reads RVR.IOReady condition per spec
// Threshold depends on replication mode (same as Initialized)
// Reasons: IOReady, InsufficientIOReadyReplicas, NoIOReadyReplicas
func (r *Reconciler) calculateIOReady(rv *v1alpha1.ReplicatedVolume, rvrs []v1alpha1.ReplicatedVolumeReplica, rsc *v1alpha1.ReplicatedStorageClass) {
	threshold := r.getInitializedThreshold(rsc)
	diskfulRVRs := filterDiskfulRVRs(rvrs)
	totalDiskful := len(diskfulRVRs)
	ioReadyCount := countRVRCondition(diskfulRVRs, v1alpha1.ConditionTypeIOReady)

	if ioReadyCount >= threshold {
		meta.SetStatusCondition(&rv.Status.Conditions, metav1.Condition{
			Type:               v1alpha1.ConditionTypeRVIOReady,
			Status:             metav1.ConditionTrue,
			Reason:             v1alpha1.ReasonRVIOReady,
			Message:            strconv.Itoa(ioReadyCount) + "/" + strconv.Itoa(totalDiskful) + " replicas IOReady",
			ObservedGeneration: rv.Generation,
		})
		return
	}

	// No IOReady replicas is more severe than partial
	if ioReadyCount == 0 {
		meta.SetStatusCondition(&rv.Status.Conditions, metav1.Condition{
			Type:               v1alpha1.ConditionTypeRVIOReady,
			Status:             metav1.ConditionFalse,
			Reason:             v1alpha1.ReasonNoIOReadyReplicas,
			Message:            messageNoIOReadyReplicas,
			ObservedGeneration: rv.Generation,
		})
		return
	}

	meta.SetStatusCondition(&rv.Status.Conditions, metav1.Condition{
		Type:               v1alpha1.ConditionTypeRVIOReady,
		Status:             metav1.ConditionFalse,
		Reason:             v1alpha1.ReasonInsufficientIOReadyReplicas,
		Message:            strconv.Itoa(ioReadyCount) + "/" + strconv.Itoa(totalDiskful) + " replicas IOReady (need " + strconv.Itoa(threshold) + ")",
		ObservedGeneration: rv.Generation,
	})
}

// calculateCounters computes status counters for the RV.
// Counter format is "current/total" (e.g. "2/3") - this is a display string, not division.
// Note: "0/0" is valid when no replicas exist yet; could be hidden in UI if needed.
func (r *Reconciler) calculateCounters(patchedRV *v1alpha1.ReplicatedVolume, rv *v1alpha1.ReplicatedVolume, rvrs []v1alpha1.ReplicatedVolumeReplica) {
	var diskfulTotal, diskfulCurrent int
	var diskfulInSync int
	var attachedAndIOReady int

	// Build set of attached nodes for O(1) lookup
	attachedSet := make(map[string]struct{})
	if rv.Status != nil {
		for _, node := range rv.Status.ActuallyAttachedTo {
			attachedSet[node] = struct{}{}
		}
	}

	for _, rvr := range rvrs {
		if rvr.Spec.Type == v1alpha1.ReplicaTypeDiskful {
			diskfulTotal++
			cond := getRVRCondition(&rvr, v1alpha1.ConditionTypeRVRBackingVolumeCreated)
			if cond != nil && cond.Status == metav1.ConditionTrue {
				diskfulCurrent++
			}
			// Use InSync condition per spec
			inSyncCond := getRVRCondition(&rvr, v1alpha1.ConditionTypeInSync)
			if inSyncCond != nil && inSyncCond.Status == metav1.ConditionTrue {
				diskfulInSync++
			}
		}

		if _, attached := attachedSet[rvr.Spec.NodeName]; attached {
			// Use IOReady condition per spec
			ioReadyCond := getRVRCondition(&rvr, v1alpha1.ConditionTypeIOReady)
			if ioReadyCond != nil && ioReadyCond.Status == metav1.ConditionTrue {
				attachedAndIOReady++
			}
		}
	}

	patchedRV.Status.DiskfulReplicaCount = strconv.Itoa(diskfulCurrent) + "/" + strconv.Itoa(diskfulTotal)
	patchedRV.Status.DiskfulReplicasInSync = strconv.Itoa(diskfulInSync) + "/" + strconv.Itoa(diskfulTotal)
	desiredAttachCount := 0
	if rv.Status != nil {
		desiredAttachCount = len(rv.Status.DesiredAttachTo)
	}
	patchedRV.Status.AttachedAndIOReadyCount = strconv.Itoa(attachedAndIOReady) + "/" + strconv.Itoa(desiredAttachCount)
}
