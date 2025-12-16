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
	"strings"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
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
	rv := &v1alpha3.ReplicatedVolume{}
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
	rvrList := &v1alpha3.ReplicatedVolumeReplicaList{}
	if err := r.cl.List(ctx, rvrList); err != nil {
		log.Error(err, "failed to list ReplicatedVolumeReplicas")
		return reconcile.Result{}, err
	}

	var rvrs []v1alpha3.ReplicatedVolumeReplica
	for _, rvr := range rvrList.Items {
		if rvr.Spec.ReplicatedVolumeName == rv.Name {
			rvrs = append(rvrs, rvr)
		}
	}

	// Calculate conditions and counters
	patchedRV := rv.DeepCopy()
	if patchedRV.Status == nil {
		patchedRV.Status = &v1alpha3.ReplicatedVolumeStatus{}
	}

	// Calculate all conditions
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

// rvrConditionInfo holds info about an RVR and its condition for aggregation
type rvrConditionInfo struct {
	name    string
	reason  string
	message string
}

// aggregateCondition aggregates condition info from multiple RVRs into a single reason and message.
// If all RVRs have the same reason, that reason is used.
// If RVRs have different reasons, ReasonMultipleReasons is used.
// Message format: "<rvr_name> - <reason> - <message, if exists>"
func aggregateCondition(failedRVRs []rvrConditionInfo) (reason, message string) {
	if len(failedRVRs) == 0 {
		return "", ""
	}

	if len(failedRVRs) == 1 {
		info := failedRVRs[0]
		reason = info.reason
		// Message format: "<name> - <reason> - <message>" or "<name> - <reason>"
		if info.message != "" {
			message = info.name + " - " + info.reason + " - " + info.message
		} else {
			message = info.name + " - " + info.reason
		}
		return reason, message
	}

	// Check if all reasons are the same
	firstReason := failedRVRs[0].reason
	allSame := true
	for _, info := range failedRVRs[1:] {
		if info.reason != firstReason {
			allSame = false
			break
		}
	}

	if allSame {
		reason = firstReason
	} else {
		reason = v1alpha3.ReasonMultipleReasons
	}

	// Build aggregated message
	// Message format: "<name> - <reason> - <message>, <name> - <reason>, ..."
	var parts []string
	for _, info := range failedRVRs {
		if info.message != "" {
			parts = append(parts, info.name+" - "+info.reason+" - "+info.message)
		} else {
			parts = append(parts, info.name+" - "+info.reason)
		}
	}
	message = strings.Join(parts, ", ")

	return reason, message
}

// getRVRCondition gets a condition from RVR status by type
func getRVRCondition(rvr *v1alpha3.ReplicatedVolumeReplica, conditionType string) *metav1.Condition {
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

// collectRVRConditionStatus iterates over RVRs, checks the specified condition type,
// and returns count of successful RVRs along with info about failed ones.
// When condition doesn't exist on RVR, message format: "<conditionType> condition not found"
func collectRVRConditionStatus(rvrs []v1alpha3.ReplicatedVolumeReplica, conditionType string) (successCount int, failedRVRs []rvrConditionInfo) {
	for _, rvr := range rvrs {
		cond := getRVRCondition(&rvr, conditionType)
		if cond != nil && cond.Status == metav1.ConditionTrue {
			successCount++
		} else {
			info := rvrConditionInfo{name: rvr.Name}
			if cond != nil {
				// Copy reason and message from RVR condition
				info.reason = cond.Reason
				info.message = cond.Message
			} else {
				// Condition not found - generate message from condition type
				info.reason = reasonUnknown
				info.message = conditionType + conditionNotFoundSuffix
			}
			failedRVRs = append(failedRVRs, info)
		}
	}
	return successCount, failedRVRs
}

// filterDiskfulRVRs returns only Diskful type replicas from the list.
// Used for conditions that apply only to data-holding replicas (BackingVolume, DataQuorum).
func filterDiskfulRVRs(rvrs []v1alpha3.ReplicatedVolumeReplica) []v1alpha3.ReplicatedVolumeReplica {
	var diskfulRVRs []v1alpha3.ReplicatedVolumeReplica
	for _, rvr := range rvrs {
		if rvr.Spec.Type == v1alpha3.ReplicaTypeDiskful {
			diskfulRVRs = append(diskfulRVRs, rvr)
		}
	}
	return diskfulRVRs
}

// calculateScheduled aggregates Scheduled condition from all RVRs.
// RV is Scheduled when ALL RVRs are scheduled.
func (r *Reconciler) calculateScheduled(rv *v1alpha3.ReplicatedVolume, rvrs []v1alpha3.ReplicatedVolumeReplica) {
	if len(rvrs) == 0 {
		meta.SetStatusCondition(&rv.Status.Conditions, metav1.Condition{
			Type:    v1alpha3.ConditionTypeRVScheduled,
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha3.ReasonSchedulingInProgress,
			Message: messageNoReplicasFound,
		})
		return
	}

	scheduledCount, failedRVRs := collectRVRConditionStatus(rvrs, v1alpha3.ConditionTypeScheduled)

	if scheduledCount == len(rvrs) {
		meta.SetStatusCondition(&rv.Status.Conditions, metav1.Condition{
			Type:   v1alpha3.ConditionTypeRVScheduled,
			Status: metav1.ConditionTrue,
			Reason: v1alpha3.ReasonAllReplicasScheduled,
		})
		return
	}

	reason, message := aggregateCondition(failedRVRs)

	meta.SetStatusCondition(&rv.Status.Conditions, metav1.Condition{
		Type:    v1alpha3.ConditionTypeRVScheduled,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

// calculateBackingVolumeCreated aggregates BackingVolumeCreated condition from Diskful RVRs only.
// Access/TieBreaker replicas don't have backing volumes, so they are excluded.
func (r *Reconciler) calculateBackingVolumeCreated(rv *v1alpha3.ReplicatedVolume, rvrs []v1alpha3.ReplicatedVolumeReplica) {
	diskfulRVRs := filterDiskfulRVRs(rvrs)

	if len(diskfulRVRs) == 0 {
		meta.SetStatusCondition(&rv.Status.Conditions, metav1.Condition{
			Type:    v1alpha3.ConditionTypeRVBackingVolumeCreated,
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha3.ReasonWaitingForBackingVolumes,
			Message: messageNoDiskfulReplicasFound,
		})
		return
	}

	readyCount, failedRVRs := collectRVRConditionStatus(diskfulRVRs, v1alpha3.ConditionTypeRVRBackingVolumeCreated)

	if readyCount == len(diskfulRVRs) {
		meta.SetStatusCondition(&rv.Status.Conditions, metav1.Condition{
			Type:   v1alpha3.ConditionTypeRVBackingVolumeCreated,
			Status: metav1.ConditionTrue,
			Reason: v1alpha3.ReasonAllBackingVolumesReady,
		})
		return
	}

	reason, message := aggregateCondition(failedRVRs)

	meta.SetStatusCondition(&rv.Status.Conditions, metav1.Condition{
		Type:    v1alpha3.ConditionTypeRVBackingVolumeCreated,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

// calculateConfigured aggregates ConfigurationAdjusted condition from all RVRs.
// RV is Configured when ALL RVRs have their DRBD configuration applied.
func (r *Reconciler) calculateConfigured(rv *v1alpha3.ReplicatedVolume, rvrs []v1alpha3.ReplicatedVolumeReplica) {
	if len(rvrs) == 0 {
		meta.SetStatusCondition(&rv.Status.Conditions, metav1.Condition{
			Type:    v1alpha3.ConditionTypeRVConfigured,
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha3.ReasonConfigurationInProgress,
			Message: messageNoReplicasFound,
		})
		return
	}

	configuredCount, failedRVRs := collectRVRConditionStatus(rvrs, v1alpha3.ConditionTypeConfigurationAdjusted)

	if configuredCount == len(rvrs) {
		meta.SetStatusCondition(&rv.Status.Conditions, metav1.Condition{
			Type:   v1alpha3.ConditionTypeRVConfigured,
			Status: metav1.ConditionTrue,
			Reason: v1alpha3.ReasonAllReplicasConfigured,
		})
		return
	}

	reason, message := aggregateCondition(failedRVRs)

	meta.SetStatusCondition(&rv.Status.Conditions, metav1.Condition{
		Type:    v1alpha3.ConditionTypeRVConfigured,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

// getInitializedThreshold returns the number of replicas needed to be initialized based on RSC replication mode
func (r *Reconciler) getInitializedThreshold(rsc *v1alpha1.ReplicatedStorageClass) int {
	switch rsc.Spec.Replication {
	case v1alpha3.ReplicationNone:
		return 1
	case v1alpha3.ReplicationAvailability:
		return 2
	case v1alpha3.ReplicationConsistencyAndAvailability:
		return 3
	default:
		r.log.Error(nil, "Unknown replication type, using threshold=1", "replication", rsc.Spec.Replication)
		return 1
	}
}

// calculateInitialized aggregates InitialSync condition from RVRs.
// Unlike other conditions, RV is Initialized when THRESHOLD number of RVRs completed initial sync.
// Threshold depends on replication mode: None=1, Availability=2, ConsistencyAndAvailability=3.
func (r *Reconciler) calculateInitialized(rv *v1alpha3.ReplicatedVolume, rvrs []v1alpha3.ReplicatedVolumeReplica, rsc *v1alpha1.ReplicatedStorageClass) {
	threshold := r.getInitializedThreshold(rsc)

	initializedCount, failedRVRs := collectRVRConditionStatus(rvrs, v1alpha3.ConditionTypeInitialSync)

	if initializedCount >= threshold {
		// Message format: "<count>/<threshold> replicas initialized"
		meta.SetStatusCondition(&rv.Status.Conditions, metav1.Condition{
			Type:    v1alpha3.ConditionTypeRVInitialized,
			Status:  metav1.ConditionTrue,
			Reason:  v1alpha3.ReasonInitialized,
			Message: strconv.Itoa(initializedCount) + "/" + strconv.Itoa(threshold) + " replicas initialized",
		})
		return
	}

	reason, message := aggregateCondition(failedRVRs)

	// Message format: "<count>/<threshold> replicas initialized. <aggregated_message>"
	meta.SetStatusCondition(&rv.Status.Conditions, metav1.Condition{
		Type:    v1alpha3.ConditionTypeRVInitialized,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: strconv.Itoa(initializedCount) + "/" + strconv.Itoa(threshold) + " replicas initialized. " + message,
	})
}

// calculateQuorum aggregates Quorum condition from all RVRs (including diskless).
// Quorum requires MAJORITY of replicas to be in quorum (total/2 + 1).
// Note: Unlike other conditions, we use RV-level reasons (QuorumReached/Degraded/Lost)
// because quorum is a computed state, not just aggregation. Message still contains RVR details.
func (r *Reconciler) calculateQuorum(rv *v1alpha3.ReplicatedVolume, rvrs []v1alpha3.ReplicatedVolumeReplica) {
	total := len(rvrs)
	quorumNeeded := (total / 2) + 1

	inQuorumCount, failedRVRs := collectRVRConditionStatus(rvrs, v1alpha3.ConditionTypeQuorum)

	if inQuorumCount >= quorumNeeded {
		reason := v1alpha3.ReasonQuorumReached
		if inQuorumCount < total {
			// Quorum achieved but some replicas are out - degraded state
			reason = v1alpha3.ReasonQuorumDegraded
		}
		// Message format: "<count>/<total> replicas in quorum"
		meta.SetStatusCondition(&rv.Status.Conditions, metav1.Condition{
			Type:    v1alpha3.ConditionTypeRVQuorum,
			Status:  metav1.ConditionTrue,
			Reason:  reason,
			Message: strconv.Itoa(inQuorumCount) + "/" + strconv.Itoa(total) + " replicas in quorum",
		})
		return
	}

	// Quorum lost - use RV-level reason, but include RVR details in message
	_, message := aggregateCondition(failedRVRs)

	// Message format: "<count>/<total> replicas in quorum. <aggregated_message>"
	meta.SetStatusCondition(&rv.Status.Conditions, metav1.Condition{
		Type:    v1alpha3.ConditionTypeRVQuorum,
		Status:  metav1.ConditionFalse,
		Reason:  v1alpha3.ReasonQuorumLost,
		Message: strconv.Itoa(inQuorumCount) + "/" + strconv.Itoa(total) + " replicas in quorum. " + message,
	})
}

// calculateDataQuorum aggregates Quorum condition from Diskful RVRs only.
// DataQuorum ensures enough DATA-holding replicas are in quorum for data safety.
// Required count is QMR (QuorumMinimumRedundancy) from DRBD config, or majority if not set.
// Note: Uses RV-level reasons (DataQuorumReached/Degraded/Lost) - see calculateQuorum comment.
func (r *Reconciler) calculateDataQuorum(rv *v1alpha3.ReplicatedVolume, rvrs []v1alpha3.ReplicatedVolumeReplica) {
	diskfulRVRs := filterDiskfulRVRs(rvrs)

	total := len(diskfulRVRs)
	if total == 0 {
		meta.SetStatusCondition(&rv.Status.Conditions, metav1.Condition{
			Type:    v1alpha3.ConditionTypeRVDataQuorum,
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha3.ReasonDataQuorumLost,
			Message: messageNoDiskfulReplicasFound,
		})
		return
	}

	// QMR (QuorumMinimumRedundancy) - minimum diskful replicas needed for data safety
	// If not set in DRBD config, fall back to majority
	var qmr int
	if rv.Status != nil && rv.Status.DRBD != nil && rv.Status.DRBD.Config != nil {
		qmr = int(rv.Status.DRBD.Config.QuorumMinimumRedundancy)
	}
	if qmr == 0 {
		qmr = (total / 2) + 1
	}

	inQuorumCount, failedRVRs := collectRVRConditionStatus(diskfulRVRs, v1alpha3.ConditionTypeQuorum)

	if inQuorumCount >= qmr {
		reason := v1alpha3.ReasonDataQuorumReached
		if inQuorumCount < total {
			reason = v1alpha3.ReasonDataQuorumDegraded
		}
		// Message format: "<count>/<total> diskful replicas in quorum (QMR=<qmr>)"
		meta.SetStatusCondition(&rv.Status.Conditions, metav1.Condition{
			Type:    v1alpha3.ConditionTypeRVDataQuorum,
			Status:  metav1.ConditionTrue,
			Reason:  reason,
			Message: strconv.Itoa(inQuorumCount) + "/" + strconv.Itoa(total) + " diskful replicas in quorum (QMR=" + strconv.Itoa(qmr) + ")",
		})
		return
	}

	// DataQuorum lost - use RV-level reason, but include RVR details in message
	_, message := aggregateCondition(failedRVRs)

	// Message format: "<count>/<total> diskful replicas in quorum (QMR=<qmr>). <aggregated_message>"
	meta.SetStatusCondition(&rv.Status.Conditions, metav1.Condition{
		Type:    v1alpha3.ConditionTypeRVDataQuorum,
		Status:  metav1.ConditionFalse,
		Reason:  v1alpha3.ReasonDataQuorumLost,
		Message: strconv.Itoa(inQuorumCount) + "/" + strconv.Itoa(total) + " diskful replicas in quorum (QMR=" + strconv.Itoa(qmr) + "). " + message,
	})
}

// calculateIOReady aggregates DevicesReady condition from all RVRs.
// RV is IOReady when THRESHOLD number of RVRs have their devices ready for I/O.
// Threshold depends on replication mode (same as Initialized).
// Special case: if NO replicas are IOReady, use specific reason NoIOReadyReplicas.
func (r *Reconciler) calculateIOReady(rv *v1alpha3.ReplicatedVolume, rvrs []v1alpha3.ReplicatedVolumeReplica, rsc *v1alpha1.ReplicatedStorageClass) {
	threshold := r.getInitializedThreshold(rsc)

	ioReadyCount, failedRVRs := collectRVRConditionStatus(rvrs, v1alpha3.ConditionTypeDevicesReady)

	if ioReadyCount >= threshold {
		// Message format: "<count>/<total> replicas IOReady"
		meta.SetStatusCondition(&rv.Status.Conditions, metav1.Condition{
			Type:    v1alpha3.ConditionTypeRVIOReady,
			Status:  metav1.ConditionTrue,
			Reason:  v1alpha3.ReasonRVIOReady,
			Message: strconv.Itoa(ioReadyCount) + "/" + strconv.Itoa(len(rvrs)) + " replicas IOReady",
		})
		return
	}

	// Special case: complete I/O unavailability is more severe than partial
	if ioReadyCount == 0 {
		meta.SetStatusCondition(&rv.Status.Conditions, metav1.Condition{
			Type:    v1alpha3.ConditionTypeRVIOReady,
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha3.ReasonNoIOReadyReplicas,
			Message: messageNoIOReadyReplicas,
		})
		return
	}

	// Partial I/O readiness - some replicas ready but not enough
	reason, message := aggregateCondition(failedRVRs)

	// Message format: "<count>/<total> replicas IOReady. <aggregated_message>"
	meta.SetStatusCondition(&rv.Status.Conditions, metav1.Condition{
		Type:    v1alpha3.ConditionTypeRVIOReady,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: strconv.Itoa(ioReadyCount) + "/" + strconv.Itoa(len(rvrs)) + " replicas IOReady. " + message,
	})
}

// calculateCounters computes status counters for the RV:
// - DiskfulReplicaCount: "current/total" where current = diskful RVRs with BackingVolumeCreated
// - DiskfulReplicasInSync: "synced/total" where synced = diskful RVRs with DevicesReady
// - PublishedAndIOReadyCount: "ready/published" where ready = RVRs on published nodes that are IOReady
func (r *Reconciler) calculateCounters(patchedRV *v1alpha3.ReplicatedVolume, rv *v1alpha3.ReplicatedVolume, rvrs []v1alpha3.ReplicatedVolumeReplica) {
	var diskfulTotal, diskfulCurrent int
	var diskfulInSync int
	var publishedAndIOReady int

	// Build set of published nodes for O(1) lookup
	publishedSet := make(map[string]struct{})
	if rv.Status != nil {
		for _, node := range rv.Status.PublishedOn {
			publishedSet[node] = struct{}{}
		}
	}

	for _, rvr := range rvrs {
		if rvr.Spec.Type == v1alpha3.ReplicaTypeDiskful {
			diskfulTotal++
			// diskfulCurrent: has backing volume (LVM volume created)
			cond := getRVRCondition(&rvr, v1alpha3.ConditionTypeRVRBackingVolumeCreated)
			if cond != nil && cond.Status == metav1.ConditionTrue {
				diskfulCurrent++
			}
			// diskfulInSync: devices are ready and up-to-date
			inSyncCond := getRVRCondition(&rvr, v1alpha3.ConditionTypeDevicesReady)
			if inSyncCond != nil && inSyncCond.Status == metav1.ConditionTrue {
				diskfulInSync++
			}
		}

		// publishedAndIOReady: replica is on a node where RV is published AND can serve I/O
		if _, published := publishedSet[rvr.Spec.NodeName]; published {
			ioReadyCond := getRVRCondition(&rvr, v1alpha3.ConditionTypeDevicesReady)
			if ioReadyCond != nil && ioReadyCond.Status == metav1.ConditionTrue {
				publishedAndIOReady++
			}
		}
	}

	// Counter format: "<current>/<total>"
	patchedRV.Status.DiskfulReplicaCount = strconv.Itoa(diskfulCurrent) + "/" + strconv.Itoa(diskfulTotal)
	patchedRV.Status.DiskfulReplicasInSync = strconv.Itoa(diskfulInSync) + "/" + strconv.Itoa(diskfulTotal)
	patchedRV.Status.PublishedAndIOReadyCount = strconv.Itoa(publishedAndIOReady) + "/" + strconv.Itoa(len(rv.Spec.PublishOn))
}
