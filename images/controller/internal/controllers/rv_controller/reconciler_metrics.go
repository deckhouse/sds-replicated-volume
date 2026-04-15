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

package rvcontroller

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/metrics"
)

// observeRVMetrics records per-RV metrics on every reconcile.
// Called at the beginning of Reconcile, after rv is loaded.
func observeRVMetrics(rv *v1alpha1.ReplicatedVolume) {
	if rv == nil {
		return
	}

	sc := rv.Spec.ReplicatedStorageClassName

	// RV creation timestamp gauge (for age-based filtering).
	metrics.RVCreatedTimestamp.WithLabelValues(rv.Name, sc).Set(
		float64(rv.CreationTimestamp.Unix()))

	deleting := float64(0)
	if rv.DeletionTimestamp != nil {
		deleting = 1
	}
	metrics.RVDeleting.WithLabelValues(rv.Name, sc).Set(deleting)

	// RV conditions health gauges.
	for _, cond := range rv.Status.Conditions {
		val := float64(0)
		if cond.Status == metav1.ConditionTrue {
			val = 1
		}
		metrics.RVCondition.WithLabelValues(rv.Name, sc, cond.Type).Set(val)
	}
}

// observeRVDeletion records the deletion duration when an RV finalizer is removed.
func observeRVDeletion(rv *v1alpha1.ReplicatedVolume) {
	if rv == nil || rv.DeletionTimestamp == nil {
		return
	}
	dur := time.Since(rv.DeletionTimestamp.Time).Seconds()
	metrics.RVDeletionDuration.WithLabelValues(rv.Spec.ReplicatedStorageClassName).Observe(dur)
}

// cleanupRVMetrics removes all per-object metrics for a deleted RV.
func cleanupRVMetrics(rv *v1alpha1.ReplicatedVolume) {
	if rv == nil {
		return
	}
	sc := rv.Spec.ReplicatedStorageClassName
	metrics.RVCreatedTimestamp.DeleteLabelValues(rv.Name, sc)
	metrics.RVDeleting.DeleteLabelValues(rv.Name, sc)
	for _, cond := range rv.Status.Conditions {
		metrics.RVCondition.DeleteLabelValues(rv.Name, sc, cond.Type)
	}
}

// observeRVAPhaseChange records RVA metrics when phase changes.
// Called from reconcileRVAConditionsFromDatameshReplicaContext before the status patch.
func observeRVAPhaseChange(
	rva *v1alpha1.ReplicatedVolumeAttachment,
	oldPhase v1alpha1.ReplicatedVolumeAttachmentPhase,
	newPhase v1alpha1.ReplicatedVolumeAttachmentPhase,
) {
	if rva == nil {
		return
	}

	rvName := rva.Spec.ReplicatedVolumeName
	node := rva.Spec.NodeName

	// Phase info gauge: delete old, set new.
	if oldPhase != newPhase {
		if oldPhase != "" {
			metrics.RVAPhaseInfo.DeleteLabelValues(rva.Name, rvName, node, string(oldPhase))
		}
		if newPhase != "" {
			metrics.RVAPhaseInfo.WithLabelValues(rva.Name, rvName, node, string(newPhase)).Set(1)
		}

		// First time reaching Attached: observe ready/creation duration.
		if oldPhase != v1alpha1.ReplicatedVolumeAttachmentPhaseAttached &&
			newPhase == v1alpha1.ReplicatedVolumeAttachmentPhaseAttached {
			dur := time.Since(rva.CreationTimestamp.Time).Seconds()
			metrics.RVAReadyDuration.WithLabelValues(node).Observe(dur)
			metrics.RVACreationDuration.WithLabelValues(rva.Name, rvName, node).Set(dur)
			metrics.RVACreationCompletedTimestamp.WithLabelValues(rva.Name, rvName, node).Set(
				float64(time.Now().Unix()))
		}
	} else if newPhase != "" {
		metrics.RVAPhaseInfo.WithLabelValues(rva.Name, rvName, node, string(newPhase)).Set(1)
	}
}

// observeRVADetach records the detach duration when an RVA finalizer is removed.
func observeRVADetach(rva *v1alpha1.ReplicatedVolumeAttachment) {
	if rva == nil || rva.DeletionTimestamp == nil {
		return
	}
	dur := time.Since(rva.DeletionTimestamp.Time).Seconds()
	metrics.RVADetachDuration.WithLabelValues(rva.Spec.NodeName).Observe(dur)
}

// cleanupRVAMetrics removes all per-object metrics for a deleted RVA.
func cleanupRVAMetrics(rva *v1alpha1.ReplicatedVolumeAttachment) {
	if rva == nil {
		return
	}
	rvName := rva.Spec.ReplicatedVolumeName
	node := rva.Spec.NodeName
	metrics.RVACreationDuration.DeleteLabelValues(rva.Name, rvName, node)
	metrics.RVACreationCompletedTimestamp.DeleteLabelValues(rva.Name, rvName, node)
	metrics.RVAPhaseInfo.DeleteLabelValues(rva.Name, rvName, node, string(rva.Status.Phase))
}

// observeDatameshMetrics compares datamesh transitions before and after ProcessTransitions.
// Transitions that were in oldTransitions but are absent in rv.Status.DatameshTransitions
// were completed during this reconcile cycle.
func observeDatameshMetrics(
	rv *v1alpha1.ReplicatedVolume,
	oldTransitions []v1alpha1.ReplicatedVolumeDatameshTransition,
) {
	if rv == nil {
		return
	}

	now := time.Now()

	// Count active transitions per type (for gauge).
	typeCounts := make(map[string]float64)
	for i := range rv.Status.DatameshTransitions {
		t := &rv.Status.DatameshTransitions[i]
		typeCounts[string(t.Type)]++
	}

	// Reset active transitions gauge for this RV: delete old types, set new counts.
	for i := range oldTransitions {
		oldType := string(oldTransitions[i].Type)
		if _, exists := typeCounts[oldType]; !exists {
			metrics.DatameshActiveTransitions.DeleteLabelValues(rv.Name, oldType)
		}
	}
	for typ, count := range typeCounts {
		metrics.DatameshActiveTransitions.WithLabelValues(rv.Name, typ).Set(count)
	}

	// Detect completed transitions: present in old but absent in new.
	for i := range oldTransitions {
		old := &oldTransitions[i]
		found := false
		for j := range rv.Status.DatameshTransitions {
			cur := &rv.Status.DatameshTransitions[j]
			if old.Type == cur.Type && old.ReplicaName == cur.ReplicaName &&
				old.StartedAt() == cur.StartedAt() {
				found = true
				break
			}
		}
		if found {
			continue
		}

		// Transition was removed (completed). Duration ≈ now - startedAt.
		startedAt := old.StartedAt()
		if !startedAt.IsZero() {
			dur := now.Sub(startedAt.Time).Seconds()
			metrics.DatameshTransitionDuration.WithLabelValues(string(old.Type)).Observe(dur)
			metrics.DatameshTransitionsCompleted.WithLabelValues(string(old.Type)).Inc()
		}

		// Observe individual step durations from the old transition snapshot.
		for si := range old.Steps {
			step := &old.Steps[si]
			if step.StartedAt != nil && step.CompletedAt != nil {
				stepDur := step.CompletedAt.Time.Sub(step.StartedAt.Time).Seconds()
				metrics.DatameshStepDuration.WithLabelValues(string(old.Type), step.Name).Observe(stepDur)
			}
		}
	}

	// Also observe step durations for steps that just completed in still-active transitions.
	for i := range rv.Status.DatameshTransitions {
		cur := &rv.Status.DatameshTransitions[i]
		for si := range cur.Steps {
			step := &cur.Steps[si]
			if step.Status != v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted {
				continue
			}
			if step.StartedAt == nil || step.CompletedAt == nil {
				continue
			}

			// Check if this step was already completed in the old snapshot.
			alreadyCompleted := false
			for j := range oldTransitions {
				old := &oldTransitions[j]
				if old.Type != cur.Type || old.ReplicaName != cur.ReplicaName ||
					old.StartedAt() != cur.StartedAt() {
					continue
				}
				if si < len(old.Steps) && old.Steps[si].Status == v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted {
					alreadyCompleted = true
				}
				break
			}

			if !alreadyCompleted {
				stepDur := step.CompletedAt.Time.Sub(step.StartedAt.Time).Seconds()
				metrics.DatameshStepDuration.WithLabelValues(string(cur.Type), step.Name).Observe(stepDur)
			}
		}
	}
}
