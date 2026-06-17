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

type rvMetricObservation func()

type rvMetricObservations []rvMetricObservation

// observe records lifecycle/object-state events into the in-process Prometheus registry.
// Call it only after the Kubernetes state described by the observations has been
// successfully committed.
func (observations rvMetricObservations) observe() {
	for _, observe := range observations {
		observe()
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

// computeRVInitialFormationMetricObservations returns the first point when RV initial formation is ready.
// This mirrors CSI readiness logic: DatameshRevision > 0 and no active Formation transition.
func computeRVInitialFormationMetricObservations(now time.Time, base, rv *v1alpha1.ReplicatedVolume) rvMetricObservations {
	if rv == nil || rv.DeletionTimestamp != nil {
		return nil
	}
	if rvInitialFormationReady(base) || !rvInitialFormationReady(rv) {
		return nil
	}

	dur := now.Sub(rv.CreationTimestamp.Time).Seconds()
	storageClass := rv.Spec.ReplicatedStorageClassName
	return rvMetricObservations{
		func() {
			metrics.RVInitialFormationDuration.WithLabelValues(storageClass).Observe(dur)
		},
	}
}

// computeRVAPhaseChangeMetricObservations records RVA metrics when phase changes.
func computeRVAPhaseChangeMetricObservations(
	now time.Time,
	rva *v1alpha1.ReplicatedVolumeAttachment,
	oldPhase v1alpha1.ReplicatedVolumeAttachmentPhase,
	newPhase v1alpha1.ReplicatedVolumeAttachmentPhase,
) rvMetricObservations {
	if rva == nil {
		return nil
	}

	node := rva.Spec.NodeName

	if oldPhase != newPhase {
		// First time reaching Attached: observe ready/creation duration.
		if oldPhase != v1alpha1.ReplicatedVolumeAttachmentPhaseAttached &&
			newPhase == v1alpha1.ReplicatedVolumeAttachmentPhaseAttached {
			dur := now.Sub(rva.CreationTimestamp.Time).Seconds()
			return rvMetricObservations{
				func() {
					metrics.RVAReadyDuration.WithLabelValues(node).Observe(dur)
				},
			}
		}
	}

	return nil
}

// observeRVADetach records the detach duration when an RVA finalizer is removed.
func observeRVADetach(rva *v1alpha1.ReplicatedVolumeAttachment) {
	if rva == nil || rva.DeletionTimestamp == nil {
		return
	}
	dur := time.Since(rva.DeletionTimestamp.Time).Seconds()
	metrics.RVADetachDuration.WithLabelValues(rva.Spec.NodeName).Observe(dur)
}

// computeDatameshMetricObservations compares datamesh transitions before and after ProcessTransitions.
// Transitions that were in oldTransitions but are absent in rv.Status.DatameshTransitions
// were completed during this reconcile cycle.
func computeDatameshMetricObservations(
	now time.Time,
	rv *v1alpha1.ReplicatedVolume,
	oldTransitions []v1alpha1.ReplicatedVolumeDatameshTransition,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
) rvMetricObservations {
	if rv == nil {
		return nil
	}

	var observations rvMetricObservations
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
			observations = append(observations, func() {
				metrics.DatameshTransitionDuration.WithLabelValues(storageClass, node, typ).Observe(dur)
				metrics.DatameshTransitionsCompleted.WithLabelValues(storageClass, node, typ).Inc()
			})
		}

		// Observe individual step durations from the old transition snapshot.
		for si := range old.Steps {
			step := &old.Steps[si]
			if step.StartedAt != nil && step.CompletedAt != nil {
				stepDur := step.CompletedAt.Time.Sub(step.StartedAt.Time).Seconds()
				stepName := step.Name
				observations = append(observations, func() {
					metrics.DatameshStepDuration.WithLabelValues(storageClass, node, typ, stepName).Observe(stepDur)
				})
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
				stepName := step.Name
				observations = append(observations, func() {
					metrics.DatameshStepDuration.WithLabelValues(storageClass, node, typ, stepName).Observe(stepDur)
				})
			}
		}
	}

	return observations
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

func rvInitialFormationReady(rv *v1alpha1.ReplicatedVolume) bool {
	if rv == nil || rv.Status.DatameshRevision == 0 {
		return false
	}

	for i := range rv.Status.DatameshTransitions {
		if rv.Status.DatameshTransitions[i].Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation {
			return false
		}
	}

	return true
}
