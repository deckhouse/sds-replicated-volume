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

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/metrics"
)

// observeRVDeletion records the deletion duration when an RV finalizer is removed.
func observeRVDeletion(rv *v1alpha1.ReplicatedVolume) {
	if rv == nil || rv.DeletionTimestamp == nil {
		return
	}
	dur := time.Since(rv.DeletionTimestamp.Time).Seconds()
	metrics.RVDeletionDuration.WithLabelValues(rv.Spec.ReplicatedStorageClassName).Observe(dur)
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

	node := rva.Spec.NodeName

	if oldPhase != newPhase {
		// First time reaching Attached: observe ready/creation duration.
		if oldPhase != v1alpha1.ReplicatedVolumeAttachmentPhaseAttached &&
			newPhase == v1alpha1.ReplicatedVolumeAttachmentPhaseAttached {
			dur := time.Since(rva.CreationTimestamp.Time).Seconds()
			metrics.RVAReadyDuration.WithLabelValues(node).Observe(dur)
		}
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
}

// observeDatameshMetrics compares datamesh transitions before and after ProcessTransitions.
// Transitions that were in oldTransitions but are absent in rv.Status.DatameshTransitions
// were completed during this reconcile cycle.
func observeDatameshMetrics(
	rv *v1alpha1.ReplicatedVolume,
	oldTransitions []v1alpha1.ReplicatedVolumeDatameshTransition,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
) {
	if rv == nil {
		return
	}

	now := time.Now()
	replicaNodes := datameshReplicaNodes(rvrs)

	// Detect completed transitions: present in old but absent in new.
	for i := range oldTransitions {
		old := &oldTransitions[i]
		storageClass, node, typ := datameshMetricLabels(rv, old, replicaNodes)
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
			metrics.DatameshTransitionDuration.WithLabelValues(storageClass, node, typ).Observe(dur)
			metrics.DatameshTransitionsCompleted.WithLabelValues(storageClass, node, typ).Inc()
		}

		// Observe individual step durations from the old transition snapshot.
		for si := range old.Steps {
			step := &old.Steps[si]
			if step.StartedAt != nil && step.CompletedAt != nil {
				stepDur := step.CompletedAt.Time.Sub(step.StartedAt.Time).Seconds()
				metrics.DatameshStepDuration.WithLabelValues(storageClass, node, typ, step.Name).Observe(stepDur)
			}
		}
	}

	// Also observe step durations for steps that just completed in still-active transitions.
	for i := range rv.Status.DatameshTransitions {
		cur := &rv.Status.DatameshTransitions[i]
		storageClass, node, typ := datameshMetricLabels(rv, cur, replicaNodes)
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
				metrics.DatameshStepDuration.WithLabelValues(storageClass, node, typ, step.Name).Observe(stepDur)
			}
		}
	}
}

func datameshReplicaNodes(rvrs []*v1alpha1.ReplicatedVolumeReplica) map[string]string {
	replicaNodes := make(map[string]string, len(rvrs))
	for _, rvr := range rvrs {
		if rvr == nil {
			continue
		}
		replicaNodes[rvr.Name] = rvr.Spec.NodeName
	}
	return replicaNodes
}

func datameshMetricLabels(
	rv *v1alpha1.ReplicatedVolume,
	transition *v1alpha1.ReplicatedVolumeDatameshTransition,
	replicaNodes map[string]string,
) (storageClass, node, typ string) {
	storageClass = rv.Spec.ReplicatedStorageClassName
	typ = string(transition.Type)
	node = "global"

	if transition.ReplicaName == "" {
		return storageClass, node, typ
	}

	node = replicaNodes[transition.ReplicaName]
	if node == "" {
		node = "unknown"
	}

	return storageClass, node, typ
}
