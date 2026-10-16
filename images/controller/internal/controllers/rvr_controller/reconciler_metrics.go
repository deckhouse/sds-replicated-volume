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

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/metrics"
)

type rvrMetricObservation func()

type rvrMetricObservations []rvrMetricObservation

// observe records lifecycle/object-state events into the in-process Prometheus registry.
// Call it only after the Kubernetes state described by the observations has been
// successfully committed.
func (observations rvrMetricObservations) observe() {
	for _, observe := range observations {
		observe()
	}
}

// computeRVRMetricObservations compares the RVR state before (base) and after ensures.
// bvLLV is the intended LVMLogicalVolume from the cache (nil when there is none).
func computeRVRMetricObservations(
	now time.Time,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	base *v1alpha1.ReplicatedVolumeReplica,
	rv *v1alpha1.ReplicatedVolume,
	bvLLV *snc.LVMLogicalVolume,
) rvrMetricObservations {
	if rvr == nil || base == nil {
		return nil
	}

	node := rvr.Spec.NodeName
	sc := rvrStorageClassLabel(rvr, rv)

	var observations rvrMetricObservations
	observations = append(observations, computeRVRReadyLostMetricObservations(rvr, base, node, sc)...)

	// BackingVolumeReady transition: observe LLV provisioning time.
	observations = append(observations, computeBackingVolumeReadyMetricObservations(now, base, rvr, bvLLV, node, sc)...)
	return observations
}

// computeRVRReadyLostMetricObservations counts Ready condition transitions from True to
// non-True (replica flaps), labeled with the reason the replica lost Ready.
// Losing Ready on the way to deletion is expected and is not counted.
func computeRVRReadyLostMetricObservations(
	rvr *v1alpha1.ReplicatedVolumeReplica,
	base *v1alpha1.ReplicatedVolumeReplica,
	node, sc string,
) rvrMetricObservations {
	if rvr.DeletionTimestamp != nil {
		return nil
	}

	oldCond := obju.GetStatusCondition(base, v1alpha1.ReplicatedVolumeReplicaCondReadyType)
	newCond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondReadyType)

	wasTrue := oldCond != nil && oldCond.Status == metav1.ConditionTrue
	isTrue := newCond != nil && newCond.Status == metav1.ConditionTrue
	if !wasTrue || isTrue {
		return nil
	}

	reason := metrics.ReasonAbsent
	if newCond != nil {
		reason = newCond.Reason
	}

	return rvrMetricObservations{
		func() {
			metrics.RVRReadyLost.WithLabelValues(node, sc, reason).Inc()
		},
	}
}

// computeBackingVolumeReadyMetricObservations records the LLV provisioning time when
// BackingVolumeReady becomes True after provisioning a new LLV, measured from the LLV creation.
//
// Only transitions out of the provisioning reasons count. Returning to Ready after a resize or
// a temporary NotReady is not provisioning, and an absent condition means an existing LLV was
// picked up (e.g. after migration); both would otherwise record the age of the volume.
// Measuring from the LLV rather than the RVR keeps a re-provisioned LLV (replica type change,
// LLV replacement) from inheriting the replica age.
func computeBackingVolumeReadyMetricObservations(
	now time.Time,
	base *v1alpha1.ReplicatedVolumeReplica,
	rvr *v1alpha1.ReplicatedVolumeReplica,
	bvLLV *snc.LVMLogicalVolume,
	node, sc string,
) rvrMetricObservations {
	if bvLLV == nil {
		return nil
	}

	oldCond := obju.GetStatusCondition(base, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyType)
	newCond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyType)
	if oldCond == nil || newCond == nil || newCond.Status != metav1.ConditionTrue {
		return nil
	}

	switch oldCond.Reason {
	case v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonProvisioning,
		v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonReprovisioning,
		v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonProvisioningFailed:
	default:
		return nil
	}
	dur := now.Sub(bvLLV.CreationTimestamp.Time).Seconds()
	if dur <= 0 {
		return nil
	}
	return rvrMetricObservations{
		func() {
			metrics.RVRBackingVolumeDuration.WithLabelValues(node, sc).Observe(dur)
		},
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
// A replica of a Manual volume has neither and is reported as "unknown".
func rvrStorageClassLabel(rvr *v1alpha1.ReplicatedVolumeReplica, rv *v1alpha1.ReplicatedVolume) string {
	if rvr != nil {
		if sc := rvr.Labels[v1alpha1.ReplicatedStorageClassLabelKey]; sc != "" {
			return sc
		}
	}
	if rv != nil {
		return metrics.StorageClassLabel(rv.Spec.ReplicatedStorageClassName)
	}
	return metrics.StorageClassLabel("")
}
