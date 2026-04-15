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

package rvrcontroller

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/metrics"
)

// observeRVRMetrics compares the RVR state before (base) and after ensures,
// and records appropriate metrics. Called after ensures, before status patch.
func observeRVRMetrics(
	rvr *v1alpha1.ReplicatedVolumeReplica,
	base *v1alpha1.ReplicatedVolumeReplica,
	rv *v1alpha1.ReplicatedVolume,
) {
	if rvr == nil || base == nil {
		return
	}

	node := rvr.Spec.NodeName
	sc := rvrStorageClassLabel(rvr, rv)
	rvName := rvr.Spec.ReplicatedVolumeName
	oldPhase := string(base.Status.Phase)
	newPhase := string(rvr.Status.Phase)

	deleting := float64(0)
	if rvr.DeletionTimestamp != nil {
		deleting = 1
	}
	metrics.RVRDeleting.WithLabelValues(rvr.Name, rvName, node, sc).Set(deleting)

	// Initialize phase tracker entry for this RVR (no-op if already present).
	// On controller restart, uses ControllerStartTime as fallback.
	// If the RVR is already Healthy in the cache (pre-restart state), mark
	// creation as observed to prevent re-recording full age on phase flap.
	if _, loaded := metrics.RVRPhases.GetOrInitLoaded(rvr.Name, oldPhase, metrics.ControllerStartTime); !loaded {
		if oldPhase == string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy) {
			metrics.RVRPhases.MarkCreationObserved(rvr.Name)
		}
	}

	// Detect phase change.
	if oldPhase != newPhase && newPhase != "" {
		if prev, duration, existed := metrics.RVRPhases.RecordPhaseChange(rvr.Name, newPhase); existed {
			phaseLabel := prev.Phase
			if phaseLabel == "" {
				phaseLabel = "Initial"
			}
			metrics.RVRPhaseDuration.WithLabelValues(node, sc, phaseLabel).Observe(duration.Seconds())
		}

		// Delete old phase label from gauge, set new one below.
		if oldPhase != "" {
			metrics.RVRCurrentPhaseStart.DeleteLabelValues(rvr.Name, rvName, node, oldPhase)
		}

		// First time reaching Healthy: observe creation duration.
		if newPhase == string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy) &&
			!metrics.RVRPhases.IsCreationObserved(rvr.Name) {
			dur := time.Since(rvr.CreationTimestamp.Time).Seconds()
			metrics.RVRReadyDuration.WithLabelValues(node, sc).Observe(dur)
			metrics.RVRCreationDuration.WithLabelValues(rvr.Name, rvName, node, sc).Set(dur)
			metrics.RVRCreationCompletedTimestamp.WithLabelValues(rvr.Name, rvName, node, sc).Set(
				float64(time.Now().Unix()))
			metrics.RVRPhases.MarkCreationObserved(rvr.Name)
		}
	}

	// Update deadlock detection gauge on every reconcile.
	if newPhase != "" {
		entry := metrics.RVRPhases.GetOrInit(rvr.Name, newPhase, metrics.ControllerStartTime)
		metrics.RVRCurrentPhaseStart.WithLabelValues(rvr.Name, rvName, node, newPhase).Set(
			float64(entry.StartTime.Unix()))
	}

	// BackingVolumeReady transition: observe LLV provisioning time.
	observeBackingVolumeReady(rvr, base, node, sc)

	// Health gauge: set 1 for current phase (delete old if changed).
	observeRVRPhaseInfo(rvr, base, rvName, node)
}

// observeBackingVolumeReady detects BackingVolumeReady condition transitioning to True
// and records the LLV provisioning time.
func observeBackingVolumeReady(
	rvr *v1alpha1.ReplicatedVolumeReplica,
	base *v1alpha1.ReplicatedVolumeReplica,
	node, sc string,
) {
	oldCond := obju.GetStatusCondition(base, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyType)
	newCond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyType)

	wasTrue := oldCond != nil && oldCond.Status == metav1.ConditionTrue
	isTrue := newCond != nil && newCond.Status == metav1.ConditionTrue

	if !wasTrue && isTrue {
		dur := time.Since(rvr.CreationTimestamp.Time).Seconds()
		metrics.RVRBackingVolumeDuration.WithLabelValues(node, sc).Observe(dur)
	}
}

// observeRVRPhaseInfo updates the per-object phase info gauge.
func observeRVRPhaseInfo(
	rvr *v1alpha1.ReplicatedVolumeReplica,
	base *v1alpha1.ReplicatedVolumeReplica,
	rvName, node string,
) {
	oldPhase := string(base.Status.Phase)
	newPhase := string(rvr.Status.Phase)

	if oldPhase != newPhase {
		if oldPhase != "" {
			metrics.RVRPhaseInfo.DeleteLabelValues(rvr.Name, rvName, node, oldPhase)
		}
	}

	if newPhase != "" {
		metrics.RVRPhaseInfo.WithLabelValues(rvr.Name, rvName, node, newPhase).Set(1)
	}
}

// cleanupRVRMetrics removes all per-object metrics for a deleted RVR.
// Must be called when the RVR finalizer is removed.
func cleanupRVRMetrics(rvr *v1alpha1.ReplicatedVolumeReplica, rv *v1alpha1.ReplicatedVolume) {
	if rvr == nil {
		return
	}

	name := rvr.Name
	rvName := rvr.Spec.ReplicatedVolumeName
	node := rvr.Spec.NodeName
	sc := rvrStorageClassLabel(rvr, rv)

	metrics.RVRCreationDuration.DeleteLabelValues(name, rvName, node, sc)
	metrics.RVRCreationCompletedTimestamp.DeleteLabelValues(name, rvName, node, sc)
	metrics.RVRPhaseInfo.DeleteLabelValues(name, rvName, node, string(rvr.Status.Phase))
	metrics.RVRDeleting.DeleteLabelValues(name, rvName, node, sc)
	metrics.RVRCurrentPhaseStart.DeleteLabelValues(name, rvName, node, string(rvr.Status.Phase))
	metrics.RVRPhases.Delete(name)
}

// observeRVRDeletion records the deletion duration when an RVR finalizer is removed.
func observeRVRDeletion(rvr *v1alpha1.ReplicatedVolumeReplica, rv *v1alpha1.ReplicatedVolume) {
	if rvr == nil || rvr.DeletionTimestamp == nil {
		return
	}
	dur := time.Since(rvr.DeletionTimestamp.Time).Seconds()
	metrics.RVRDeletionDuration.WithLabelValues(rvr.Spec.NodeName, rvrStorageClassLabel(rvr, rv)).Observe(dur)
}

// rvrStorageClassLabel returns the ReplicatedStorageClass name for metric labels.
// Primary source: RVR's own label (always available, even after RV deletion).
// Fallback: RV spec (for cases where label is missing).
func rvrStorageClassLabel(rvr *v1alpha1.ReplicatedVolumeReplica, rv *v1alpha1.ReplicatedVolume) string {
	if rvr != nil {
		if sc := rvr.Labels[v1alpha1.ReplicatedStorageClassLabelKey]; sc != "" {
			return sc
		}
	}
	if rv != nil {
		return rv.Spec.ReplicatedStorageClassName
	}
	return ""
}
