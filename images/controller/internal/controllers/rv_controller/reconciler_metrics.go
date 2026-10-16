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
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/types"

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

// observeRVCreated counts an RV once, when the controller first adds RVControllerFinalizer to it.
// The finalizer persists across controller restarts and leader changes, so the event is not
// replayed; call it only after the patch that added the finalizer has been committed.
func observeRVCreated(rv *v1alpha1.ReplicatedVolume) {
	metrics.RVCreated.WithLabelValues(
		metrics.StorageClassLabel(rv.Spec.ReplicatedStorageClassName),
		metrics.ConfigurationModeLabel(rv.Spec.ConfigurationMode),
	).Inc()
}

// observeRVDeletion records the deletion duration when an RV finalizer is removed.
func observeRVDeletion(rv *v1alpha1.ReplicatedVolume) {
	if rv == nil || rv.DeletionTimestamp == nil {
		return
	}
	dur := time.Since(rv.DeletionTimestamp.Time).Seconds()
	metrics.RVDeletionDuration.WithLabelValues(metrics.StorageClassLabel(rv.Spec.ReplicatedStorageClassName)).Observe(dur)
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
	storageClass := metrics.StorageClassLabel(rv.Spec.ReplicatedStorageClassName)
	return rvMetricObservations{
		func() {
			metrics.RVInitialFormationDuration.WithLabelValues(storageClass).Observe(dur)
		},
	}
}

// computeRVAPhaseChangeMetricObservations records RVA metrics when phase changes.
// attachedBefore reports whether this RVA has already been attached earlier: a re-attach
// after a lost attachment is not an attach latency and must not be measured from creation.
func computeRVAPhaseChangeMetricObservations(
	now time.Time,
	rva *v1alpha1.ReplicatedVolumeAttachment,
	oldPhase v1alpha1.ReplicatedVolumeAttachmentPhase,
	newPhase v1alpha1.ReplicatedVolumeAttachmentPhase,
	attachedBefore bool,
) rvMetricObservations {
	if rva == nil || attachedBefore {
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

// computeDatameshMetricObservations records datamesh transitions and steps that completed
// during this reconcile. Every step is observed exactly once, in the reconcile where it
// completes:
//   - transitions the engine completed come from completed, including those created and
//     completed within this reconcile and steps passed through without changes in one
//     engine run; cancelled transitions never appear there;
//   - Formation is managed by the reconciler, not the engine, so its completion is detected
//     as its disappearance from rv.Status.DatameshTransitions; a Formation restart drops it
//     and resets the datamesh revision to zero and is not a completion;
//   - steps of transitions that are still active are observed when they become Completed.
func computeDatameshMetricObservations(
	now time.Time,
	rv *v1alpha1.ReplicatedVolume,
	oldTransitions []v1alpha1.ReplicatedVolumeDatameshTransition,
	completed []v1alpha1.ReplicatedVolumeDatameshTransition,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
) rvMetricObservations {
	if rv == nil {
		return nil
	}

	var observations rvMetricObservations
	replicaNodes := datameshReplicaNodes(rvrs)

	observeTransition := func(t *v1alpha1.ReplicatedVolumeDatameshTransition) {
		startedAt := t.StartedAt()
		if startedAt.IsZero() {
			return
		}
		storageClass, node, typ := datameshMetricLabels(rv, t, replicaNodes)
		dur := now.Sub(startedAt.Time).Seconds()
		observations = append(observations, func() {
			metrics.DatameshTransitionDuration.WithLabelValues(storageClass, node, typ).Observe(dur)
		})
	}
	observeStep := func(t *v1alpha1.ReplicatedVolumeDatameshTransition, stepName string, stepDur float64) {
		storageClass, node, typ := datameshMetricLabels(rv, t, replicaNodes)
		observations = append(observations, func() {
			metrics.DatameshStepDuration.WithLabelValues(storageClass, node, typ, stepName).Observe(stepDur)
		})
	}
	observeCompletedStep := func(t *v1alpha1.ReplicatedVolumeDatameshTransition, step *v1alpha1.ReplicatedVolumeDatameshTransitionStep) {
		if step.StartedAt == nil || step.CompletedAt == nil {
			return
		}
		observeStep(t, step.Name, step.CompletedAt.Sub(step.StartedAt.Time).Seconds())
	}

	// Transitions completed by the engine: steps that were Completed in the old snapshot
	// were observed in earlier reconciles.
	for i := range completed {
		t := &completed[i]
		observeTransition(t)
		old := findDatameshTransition(oldTransitions, t)
		for si := range t.Steps {
			step := &t.Steps[si]
			if step.Status != v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted || step.CompletedAt == nil {
				continue
			}
			if old != nil && si < len(old.Steps) &&
				old.Steps[si].Status == v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted {
				continue
			}
			observeCompletedStep(t, step)
		}
	}

	// Formation that disappeared without a restart completed: the reconciler removes it
	// right after its last step, without marking that step Completed. The reconciler also
	// falls through to the next step when it is already satisfied, so the steps after the
	// one active in the old snapshot started and completed within this reconcile.
	if rv.Status.DatameshRevision != 0 {
		for i := range oldTransitions {
			old := &oldTransitions[i]
			if old.Type != v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation ||
				findDatameshTransition(rv.Status.DatameshTransitions, old) != nil {
				continue
			}
			observeTransition(old)
			passed := false
			for si := range old.Steps {
				step := &old.Steps[si]
				switch {
				case step.Status == v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted:
					// Observed in the reconcile where it completed.
				case !passed:
					passed = true
					if step.StartedAt != nil {
						observeStep(old, step.Name, now.Sub(step.StartedAt.Time).Seconds())
					}
				default:
					observeStep(old, step.Name, 0)
				}
			}
		}
	}

	// Steps that just completed in still-active transitions.
	for i := range rv.Status.DatameshTransitions {
		cur := &rv.Status.DatameshTransitions[i]
		old := findDatameshTransition(oldTransitions, cur)
		for si := range cur.Steps {
			step := &cur.Steps[si]
			if step.Status != v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted || step.CompletedAt == nil {
				continue
			}
			if old != nil && si < len(old.Steps) &&
				old.Steps[si].Status == v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted {
				continue
			}
			observeCompletedStep(cur, step)
		}
	}

	return observations
}

// findDatameshTransition returns the transition in transitions that is the same instance
// as t (same type, replica and start time), or nil.
func findDatameshTransition(
	transitions []v1alpha1.ReplicatedVolumeDatameshTransition,
	t *v1alpha1.ReplicatedVolumeDatameshTransition,
) *v1alpha1.ReplicatedVolumeDatameshTransition {
	startedAt := t.StartedAt()
	for i := range transitions {
		cur := &transitions[i]
		if cur.Type == t.Type && cur.ReplicaName == t.ReplicaName && cur.StartedAt().Time.Equal(startedAt.Time) {
			return cur
		}
	}
	return nil
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
	storageClass = metrics.StorageClassLabel(rv.Spec.ReplicatedStorageClassName)
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

// attachedRVATracker remembers RVAs that have already been attached, so that a re-attach
// after a lost attachment is not reported as attach latency. The RVA status has no field
// that survives the loss of the attachment, so the state is kept in memory: after a
// controller restart it is rebuilt from RVAs whose committed phase is Attached.
//
// An RVA that lost its attachment before the restart is not rebuilt, so its re-attach is
// observed as a first attach, measured from creation. Telling it apart would need a marker
// persisted in the RVA, which is not worth one outlier per such RVA.
//
// Entries are grouped by volume and pruned on every reconcile of the volume (retain, and
// forgetVolume once the volume is gone): an RVA may leave without this controller removing
// its finalizer, and nothing reconciles the RVA itself then.
type attachedRVATracker struct {
	mu   sync.Mutex
	byRV map[string]map[types.UID]struct{}
}

func newAttachedRVATracker() *attachedRVATracker {
	return &attachedRVATracker{byRV: make(map[string]map[types.UID]struct{})}
}

func (t *attachedRVATracker) wasAttached(rva *v1alpha1.ReplicatedVolumeAttachment) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.byRV[rva.Spec.ReplicatedVolumeName][rva.UID]
	return ok
}

func (t *attachedRVATracker) markAttached(rva *v1alpha1.ReplicatedVolumeAttachment) {
	t.mu.Lock()
	defer t.mu.Unlock()
	uids := t.byRV[rva.Spec.ReplicatedVolumeName]
	if uids == nil {
		uids = make(map[types.UID]struct{})
		t.byRV[rva.Spec.ReplicatedVolumeName] = uids
	}
	uids[rva.UID] = struct{}{}
}

func (t *attachedRVATracker) forget(rva *v1alpha1.ReplicatedVolumeAttachment) {
	t.mu.Lock()
	defer t.mu.Unlock()
	uids := t.byRV[rva.Spec.ReplicatedVolumeName]
	delete(uids, rva.UID)
	if len(uids) == 0 {
		delete(t.byRV, rva.Spec.ReplicatedVolumeName)
	}
}

// retain drops the entries of the volume whose RVAs are no longer among rvas, the current
// RVAs of the volume. An RVA is marked only while it is among them, so a live one is kept.
func (t *attachedRVATracker) retain(rvName string, rvas []*v1alpha1.ReplicatedVolumeAttachment) {
	t.mu.Lock()
	defer t.mu.Unlock()
	uids := t.byRV[rvName]
	if len(uids) == 0 {
		return
	}
	live := make(map[types.UID]struct{}, len(rvas))
	for _, rva := range rvas {
		live[rva.UID] = struct{}{}
	}
	for uid := range uids {
		if _, ok := live[uid]; !ok {
			delete(uids, uid)
		}
	}
	if len(uids) == 0 {
		delete(t.byRV, rvName)
	}
}

// forgetVolume drops all entries of a volume that no longer exists.
func (t *attachedRVATracker) forgetVolume(rvName string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.byRV, rvName)
}
