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

	observeRVRReadyTransition(rvr, base, node, sc)

	// BackingVolumeReady transition: observe LLV provisioning time.
	observeBackingVolumeReady(rvr, base, node, sc)
}

// observeRVRReadyTransition records every Ready condition transition from non-True to True.
func observeRVRReadyTransition(
	rvr *v1alpha1.ReplicatedVolumeReplica,
	base *v1alpha1.ReplicatedVolumeReplica,
	node, sc string,
) {
	if rvr.DeletionTimestamp != nil {
		return
	}

	oldCond := obju.GetStatusCondition(base, v1alpha1.ReplicatedVolumeReplicaCondReadyType)
	newCond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondReadyType)

	wasTrue := oldCond != nil && oldCond.Status == metav1.ConditionTrue
	isTrue := newCond != nil && newCond.Status == metav1.ConditionTrue
	if wasTrue || !isTrue {
		return
	}

	startedAt := rvr.CreationTimestamp.Time
	if oldCond != nil && !oldCond.LastTransitionTime.IsZero() {
		startedAt = oldCond.LastTransitionTime.Time
	}

	duration := newCond.LastTransitionTime.Sub(startedAt)
	if duration <= 0 {
		return
	}

	metrics.RVRReadyDuration.WithLabelValues(node, sc).Observe(duration.Seconds())
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
