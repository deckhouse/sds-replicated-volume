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
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/metrics"
)

// metricsTestMu serializes tests that mutate package-level Prometheus collectors.
// Do not call t.Parallel in subtests that touch these process-global collectors.
var metricsTestMu sync.Mutex

func TestComputeDatameshMetricObservations(t *testing.T) {
	metricsTestMu.Lock()
	defer metricsTestMu.Unlock()

	start := metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	done := metav1.NewTime(start.Add(10 * time.Second))
	now := start.Add(20 * time.Second)

	rv := &v1alpha1.ReplicatedVolume{
		Spec: v1alpha1.ReplicatedVolumeSpec{
			ReplicatedStorageClassName: "sc-a",
		},
	}

	newTransition := func(status v1alpha1.ReplicatedVolumeDatameshTransitionStepStatus, completedAt *metav1.Time) v1alpha1.ReplicatedVolumeDatameshTransition {
		return v1alpha1.ReplicatedVolumeDatameshTransition{
			Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation,
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{
					Name:        "Formation",
					Status:      status,
					StartedAt:   &start,
					CompletedAt: completedAt,
				},
			},
		}
	}

	t.Run("no diff produces no observations", func(t *testing.T) {
		oldTransition := newTransition(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted, &done)
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{oldTransition}

		observations := computeDatameshMetricObservations(now, rv, []v1alpha1.ReplicatedVolumeDatameshTransition{oldTransition}, nil, nil)
		if len(observations) != 0 {
			t.Fatalf("expected no observations, got %d", len(observations))
		}
	})

	t.Run("completed formation records the transition and its last active step", func(t *testing.T) {
		// The reconciler removes a formation right after confirming its last step,
		// without marking that step Completed.
		oldTransition := newTransition(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, nil)
		rv.Status.DatameshRevision = 1
		rv.Status.DatameshTransitions = nil
		resetDatameshMetrics("global", string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation), "Formation")

		observations := computeDatameshMetricObservations(now, rv, []v1alpha1.ReplicatedVolumeDatameshTransition{oldTransition}, nil, nil)
		if len(observations) != 2 {
			t.Fatalf("expected transition and step observations, got %d", len(observations))
		}
		observations.observe()

		assertHistogramSample(t, metrics.DatameshTransitionDuration, map[string]string{
			metrics.LabelStorageClass: "sc-a",
			metrics.LabelNode:         "global",
			metrics.LabelType:         string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation),
		}, 20)
		assertHistogramSample(t, metrics.DatameshStepDuration, map[string]string{
			metrics.LabelStorageClass: "sc-a",
			metrics.LabelNode:         "global",
			metrics.LabelType:         string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation),
			metrics.LabelStep:         "Formation",
		}, 20)
	})

	t.Run("formation steps completed before its removal are not observed again", func(t *testing.T) {
		// Step "First" was observed in the reconcile where it completed.
		oldTransition := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation,
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "First", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted, StartedAt: &start, CompletedAt: &done},
				{Name: "Second", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, StartedAt: &done},
			},
		}
		rv.Status.DatameshRevision = 1
		rv.Status.DatameshTransitions = nil
		resetDatameshMetrics("global", string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation), "First")
		resetDatameshMetrics("global", string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation), "Second")

		observations := computeDatameshMetricObservations(now, rv, []v1alpha1.ReplicatedVolumeDatameshTransition{oldTransition}, nil, nil)
		observations.observe()

		assertNoMetricSample(t, metrics.DatameshStepDuration, map[string]string{
			metrics.LabelStep: "First",
		})
		assertHistogramSample(t, metrics.DatameshStepDuration, map[string]string{
			metrics.LabelStorageClass: "sc-a",
			metrics.LabelNode:         "global",
			metrics.LabelType:         string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation),
			metrics.LabelStep:         "Second",
		}, 10)
	})

	t.Run("formation steps passed in the reconcile that removes it are observed", func(t *testing.T) {
		// "Second" was active; the reconciler completed it, fell through "Third" and removed
		// the formation within one reconcile.
		oldTransition := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation,
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "First", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted, StartedAt: &start, CompletedAt: &done},
				{Name: "Second", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, StartedAt: &done},
				{Name: "Third", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending},
			},
		}
		rv.Status.DatameshRevision = 1
		rv.Status.DatameshTransitions = nil
		for _, step := range []string{"First", "Second", "Third"} {
			resetDatameshMetrics("global", string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation), step)
		}

		observations := computeDatameshMetricObservations(now, rv, []v1alpha1.ReplicatedVolumeDatameshTransition{oldTransition}, nil, nil)
		if len(observations) != 3 {
			t.Fatalf("expected transition and two step observations, got %d", len(observations))
		}
		observations.observe()

		formationStep := func(step string) map[string]string {
			return map[string]string{
				metrics.LabelStorageClass: "sc-a",
				metrics.LabelNode:         "global",
				metrics.LabelType:         string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation),
				metrics.LabelStep:         step,
			}
		}
		assertNoMetricSample(t, metrics.DatameshStepDuration, formationStep("First"))
		assertHistogramSample(t, metrics.DatameshStepDuration, formationStep("Second"), 10)
		assertHistogramSample(t, metrics.DatameshStepDuration, formationStep("Third"), 0)
	})

	t.Run("formation restart is not observed", func(t *testing.T) {
		// Formation restart drops all transitions and resets the datamesh revision.
		oldTransition := newTransition(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, nil)
		rv.Status.DatameshRevision = 0
		rv.Status.DatameshTransitions = nil

		observations := computeDatameshMetricObservations(now, rv, []v1alpha1.ReplicatedVolumeDatameshTransition{oldTransition}, nil, nil)
		if len(observations) != 0 {
			t.Fatalf("expected no observations for restarted formation, got %d", len(observations))
		}
	})

	attachTransition := func(startedAt *metav1.Time) v1alpha1.ReplicatedVolumeDatameshTransition {
		return v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment,
			ReplicaName: "rvr-a",
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "Attach", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, StartedAt: startedAt},
			},
		}
	}

	t.Run("transition removed by the engine without completing is not observed", func(t *testing.T) {
		// An emergency transition cancels the one it replaces: the engine does not report
		// the cancelled transition as completed.
		later := metav1.NewTime(now)
		rv.Status.DatameshRevision = 1
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeForceRemoveReplica,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupEmergency,
			ReplicaName: "rvr-a",
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "Force remove", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, StartedAt: &later},
			},
		}}

		observations := computeDatameshMetricObservations(now, rv, []v1alpha1.ReplicatedVolumeDatameshTransition{attachTransition(&start)}, nil, nil)
		if len(observations) != 0 {
			t.Fatalf("expected no observations for cancelled transition, got %d", len(observations))
		}
	})

	t.Run("transition completed by the engine is observed", func(t *testing.T) {
		later := metav1.NewTime(now)
		next := attachTransition(&later)
		next.Type = v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach
		rv.Status.DatameshRevision = 1
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{next}
		resetDatameshMetrics("unknown", string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach), "Attach")

		completed := attachTransition(&start)
		completed.Steps[0].Status = v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted
		completed.Steps[0].CompletedAt = &later

		observations := computeDatameshMetricObservations(now, rv,
			[]v1alpha1.ReplicatedVolumeDatameshTransition{attachTransition(&start)},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{completed}, nil)
		observations.observe()

		assertHistogramSample(t, metrics.DatameshTransitionDuration, map[string]string{
			metrics.LabelStorageClass: "sc-a",
			metrics.LabelNode:         "unknown",
			metrics.LabelType:         string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach),
		}, 20)
		assertHistogramSample(t, metrics.DatameshStepDuration, map[string]string{
			metrics.LabelStorageClass: "sc-a",
			metrics.LabelNode:         "unknown",
			metrics.LabelType:         string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach),
			metrics.LabelStep:         "Attach",
		}, 20)
	})

	threeStepTransition := func() v1alpha1.ReplicatedVolumeDatameshTransition {
		return v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			ReplicaName: "rvr-a",
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "First", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, StartedAt: &start},
				{Name: "Second", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending},
				{Name: "Third", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending},
			},
		}
	}
	completeSteps := func(tr v1alpha1.ReplicatedVolumeDatameshTransition, from int) v1alpha1.ReplicatedVolumeDatameshTransition {
		end := metav1.NewTime(now)
		tr.Steps = slices.Clone(tr.Steps)
		for i := from; i < len(tr.Steps); i++ {
			if i > 0 {
				tr.Steps[i].StartedAt = tr.Steps[i-1].CompletedAt
			}
			tr.Steps[i].Status = v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted
			tr.Steps[i].CompletedAt = &end
		}
		return tr
	}
	resetAddReplicaMetrics := func() {
		for _, step := range []string{"First", "Second", "Third"} {
			resetDatameshMetrics("unknown", string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica), step)
		}
	}
	addReplicaStepLabels := func(step string) map[string]string {
		return map[string]string{
			metrics.LabelStorageClass: "sc-a",
			metrics.LabelNode:         "unknown",
			metrics.LabelType:         string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica),
			metrics.LabelStep:         step,
		}
	}

	t.Run("steps passed in one engine run, including the last one, are each observed once", func(t *testing.T) {
		// The old snapshot has "First" completed and "Second" active; the engine confirms
		// "Second" and passes "Third" in the same run, then removes the transition.
		old := threeStepTransition()
		old.Steps[0].Status = v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted
		old.Steps[0].CompletedAt = &done
		old.Steps[1].Status = v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive
		old.Steps[1].StartedAt = &done
		rv.Status.DatameshRevision = 1
		rv.Status.DatameshTransitions = nil
		resetAddReplicaMetrics()

		observations := computeDatameshMetricObservations(now, rv,
			[]v1alpha1.ReplicatedVolumeDatameshTransition{old},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{completeSteps(old, 1)}, nil)
		if len(observations) != 3 {
			t.Fatalf("expected transition and two step observations, got %d", len(observations))
		}
		observations.observe()

		assertNoMetricSample(t, metrics.DatameshStepDuration, addReplicaStepLabels("First"))
		assertHistogramSample(t, metrics.DatameshStepDuration, addReplicaStepLabels("Second"), 10)
		assertHistogramSample(t, metrics.DatameshStepDuration, addReplicaStepLabels("Third"), 0)
	})

	t.Run("transition created and completed in one reconcile is observed with all its steps", func(t *testing.T) {
		rv.Status.DatameshRevision = 1
		rv.Status.DatameshTransitions = nil
		resetAddReplicaMetrics()

		observations := computeDatameshMetricObservations(now, rv, nil,
			[]v1alpha1.ReplicatedVolumeDatameshTransition{completeSteps(threeStepTransition(), 0)}, nil)
		if len(observations) != 4 {
			t.Fatalf("expected transition and three step observations, got %d", len(observations))
		}
		observations.observe()

		assertHistogramSample(t, metrics.DatameshTransitionDuration, map[string]string{
			metrics.LabelStorageClass: "sc-a",
			metrics.LabelNode:         "unknown",
			metrics.LabelType:         string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica),
		}, 20)
		for _, step := range []string{"First", "Second", "Third"} {
			if _, ok := findPrometheusSample(collectPrometheusSamples(t, metrics.DatameshStepDuration), addReplicaStepLabels(step)); !ok {
				t.Fatalf("expected a sample for step %q", step)
			}
		}
	})

	t.Run("completed active step records only step duration", func(t *testing.T) {
		activeOld := newTransition(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, nil)
		activeNew := newTransition(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted, &done)
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{activeNew}
		resetDatameshMetrics("global", string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation), "Formation")

		observations := computeDatameshMetricObservations(now, rv, []v1alpha1.ReplicatedVolumeDatameshTransition{activeOld}, nil, nil)
		if len(observations) != 1 {
			t.Fatalf("expected one step observation, got %d", len(observations))
		}
		observations.observe()

		assertNoMetricSample(t, metrics.DatameshTransitionDuration, map[string]string{
			metrics.LabelStorageClass: "sc-a",
			metrics.LabelNode:         "global",
			metrics.LabelType:         string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation),
		})
		assertHistogramSample(t, metrics.DatameshStepDuration, map[string]string{
			metrics.LabelStorageClass: "sc-a",
			metrics.LabelNode:         "global",
			metrics.LabelType:         string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation),
			metrics.LabelStep:         "Formation",
		}, 10)
	})
}

func TestReconcileMetadataCountsRVCreatedOnce(t *testing.T) {
	metricsTestMu.Lock()
	defer metricsTestMu.Unlock()

	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	created := func(sc string, mode v1alpha1.ReplicatedVolumeConfigurationMode) float64 {
		return testutil.ToFloat64(metrics.RVCreated.WithLabelValues(sc, string(mode)))
	}
	// reconcileMetadata takes the volume fetched by the caller; Get mirrors that.
	reconcileFetched := func(rec *Reconciler, cl client.Client, name string) error {
		var rv v1alpha1.ReplicatedVolume
		if err := cl.Get(ctx, client.ObjectKey{Name: name}, &rv); err != nil {
			t.Fatal(err)
		}
		return rec.reconcileMetadata(ctx, &rv, nil).Error()
	}

	t.Run("first finalizer add counts once, later passes do not", func(t *testing.T) {
		const sc = "sc-created-auto"
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-auto"},
			Spec: v1alpha1.ReplicatedVolumeSpec{
				ConfigurationMode:          v1alpha1.ReplicatedVolumeConfigurationModeAuto,
				ReplicatedStorageClassName: sc,
			},
		}
		cl := newClientBuilder(scheme).WithObjects(rv).Build()
		rec := NewReconciler(cl, scheme)
		before := created(sc, v1alpha1.ReplicatedVolumeConfigurationModeAuto)

		if err := reconcileFetched(rec, cl, rv.Name); err != nil {
			t.Fatal(err)
		}
		// A second pass sees the finalizer already in place, as after a restart or leader change.
		if err := reconcileFetched(rec, cl, rv.Name); err != nil {
			t.Fatal(err)
		}

		if got := created(sc, v1alpha1.ReplicatedVolumeConfigurationModeAuto) - before; got != 1 {
			t.Fatalf("expected one created RV, got %v", got)
		}
	})

	t.Run("manual volume is counted with the unknown storage class", func(t *testing.T) {
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-manual"},
			Spec:       v1alpha1.ReplicatedVolumeSpec{ConfigurationMode: v1alpha1.ReplicatedVolumeConfigurationModeManual},
		}
		cl := newClientBuilder(scheme).WithObjects(rv).Build()
		rec := NewReconciler(cl, scheme)
		before := created("unknown", v1alpha1.ReplicatedVolumeConfigurationModeManual)

		if err := reconcileFetched(rec, cl, rv.Name); err != nil {
			t.Fatal(err)
		}

		if got := created("unknown", v1alpha1.ReplicatedVolumeConfigurationModeManual) - before; got != 1 {
			t.Fatalf("expected one created manual RV, got %v", got)
		}
	})

	t.Run("deleting volume is not counted", func(t *testing.T) {
		const sc = "sc-created-deleting"
		now := metav1.Now()
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-deleting", DeletionTimestamp: &now, Finalizers: []string{"test"}},
			Spec:       v1alpha1.ReplicatedVolumeSpec{ReplicatedStorageClassName: sc},
		}
		cl := newClientBuilder(scheme).WithObjects(rv).Build()
		rec := NewReconciler(cl, scheme)
		before := created(sc, v1alpha1.ReplicatedVolumeConfigurationModeAuto)

		// Children keep the finalizer target present, so the metadata patch still runs.
		var fetched v1alpha1.ReplicatedVolume
		if err := cl.Get(ctx, client.ObjectKey{Name: rv.Name}, &fetched); err != nil {
			t.Fatal(err)
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{{ObjectMeta: metav1.ObjectMeta{Name: "rv-deleting-0"}}}
		_ = rec.reconcileMetadata(ctx, &fetched, rvrs)

		if got := created(sc, v1alpha1.ReplicatedVolumeConfigurationModeAuto) - before; got != 0 {
			t.Fatalf("expected no created RV for a deleting volume, got %v", got)
		}
	})

	t.Run("failed patch is not counted", func(t *testing.T) {
		const sc = "sc-created-patch-error"
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-patch-error"},
			Spec:       v1alpha1.ReplicatedVolumeSpec{ReplicatedStorageClassName: sc},
		}
		cl := newClientBuilder(scheme).
			WithObjects(rv).
			WithInterceptorFuncs(interceptor.Funcs{
				Patch: func(context.Context, client.WithWatch, client.Object, client.Patch, ...client.PatchOption) error {
					return errors.New("patch failed")
				},
			}).
			Build()
		rec := NewReconciler(cl, scheme)
		before := created(sc, v1alpha1.ReplicatedVolumeConfigurationModeAuto)

		if err := reconcileFetched(rec, cl, rv.Name); err == nil {
			t.Fatal("expected the patch error to be returned")
		}

		if got := created(sc, v1alpha1.ReplicatedVolumeConfigurationModeAuto) - before; got != 0 {
			t.Fatalf("expected no created RV after a failed patch, got %v", got)
		}
	})
}

func TestComputeRVInitialFormationMetricObservations(t *testing.T) {
	metricsTestMu.Lock()
	defer metricsTestMu.Unlock()

	createdAt := metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	now := createdAt.Add(30 * time.Second)
	metrics.RVInitialFormationDuration.DeleteLabelValues("sc-a")

	base := &v1alpha1.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{CreationTimestamp: createdAt},
		Spec: v1alpha1.ReplicatedVolumeSpec{
			ReplicatedStorageClassName: "sc-a",
		},
		Status: v1alpha1.ReplicatedVolumeStatus{
			DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
				{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation},
			},
		},
	}
	rv := base.DeepCopy()
	rv.Status.DatameshRevision = 1
	rv.Status.DatameshTransitions = nil

	observations := computeRVInitialFormationMetricObservations(now, base, rv)
	if len(observations) != 1 {
		t.Fatalf("expected one initial formation observation, got %d", len(observations))
	}
	observations.observe()

	assertHistogramSample(t, metrics.RVInitialFormationDuration, map[string]string{
		metrics.LabelStorageClass: "sc-a",
	}, 30)
}

func TestComputeRVAPhaseChangeMetricObservations(t *testing.T) {
	metricsTestMu.Lock()
	defer metricsTestMu.Unlock()

	createdAt := metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	now := createdAt.Add(15 * time.Second)
	metrics.RVAReadyDuration.DeleteLabelValues("node-a")

	rva := &v1alpha1.ReplicatedVolumeAttachment{
		ObjectMeta: metav1.ObjectMeta{CreationTimestamp: createdAt},
		Spec:       v1alpha1.ReplicatedVolumeAttachmentSpec{NodeName: "node-a"},
	}

	observations := computeRVAPhaseChangeMetricObservations(
		now,
		rva,
		v1alpha1.ReplicatedVolumeAttachmentPhaseAttaching,
		v1alpha1.ReplicatedVolumeAttachmentPhaseAttached,
		false,
	)
	if len(observations) != 1 {
		t.Fatalf("expected one RVA attached observation, got %d", len(observations))
	}
	observations.observe()

	assertHistogramSample(t, metrics.RVAReadyDuration, map[string]string{
		metrics.LabelNode: "node-a",
	}, 15)

	// Re-attach after the attachment was lost must not be measured from creationTimestamp.
	observations = computeRVAPhaseChangeMetricObservations(
		now,
		rva,
		v1alpha1.ReplicatedVolumeAttachmentPhasePending,
		v1alpha1.ReplicatedVolumeAttachmentPhaseAttached,
		true,
	)
	if len(observations) != 0 {
		t.Fatalf("expected no observation for re-attach, got %d", len(observations))
	}
}

func TestAttachedRVATracker(t *testing.T) {
	newRVA := func(rvName string, uid types.UID) *v1alpha1.ReplicatedVolumeAttachment {
		return &v1alpha1.ReplicatedVolumeAttachment{
			ObjectMeta: metav1.ObjectMeta{UID: uid},
			Spec:       v1alpha1.ReplicatedVolumeAttachmentSpec{ReplicatedVolumeName: rvName},
		}
	}

	t.Run("mark and forget", func(t *testing.T) {
		tracker := newAttachedRVATracker()
		rva := newRVA("rv-1", "rva-uid")

		if tracker.wasAttached(rva) {
			t.Fatalf("expected unknown RVA to be reported as never attached")
		}
		tracker.markAttached(rva)
		if !tracker.wasAttached(rva) {
			t.Fatalf("expected RVA to be reported as attached after markAttached")
		}
		tracker.forget(rva)
		if tracker.wasAttached(rva) {
			t.Fatalf("expected RVA to be reported as never attached after forget")
		}
		if len(tracker.byRV) != 0 {
			t.Fatalf("expected no entries left, got %v", tracker.byRV)
		}
	})

	t.Run("retain drops RVAs that left the volume and keeps the others", func(t *testing.T) {
		tracker := newAttachedRVATracker()
		kept, gone := newRVA("rv-1", "kept"), newRVA("rv-1", "gone")
		other := newRVA("rv-2", "other")
		tracker.markAttached(kept)
		tracker.markAttached(gone)
		tracker.markAttached(other)

		tracker.retain("rv-1", []*v1alpha1.ReplicatedVolumeAttachment{kept})

		if !tracker.wasAttached(kept) || tracker.wasAttached(gone) || !tracker.wasAttached(other) {
			t.Fatalf("expected only the RVA that left rv-1 to be dropped, got %v", tracker.byRV)
		}

		tracker.retain("rv-1", nil)
		if _, ok := tracker.byRV["rv-1"]; ok {
			t.Fatalf("expected the volume entry to be dropped once empty, got %v", tracker.byRV)
		}
	})

	t.Run("forgetVolume drops every RVA of the volume", func(t *testing.T) {
		tracker := newAttachedRVATracker()
		tracker.markAttached(newRVA("rv-1", "a"))
		tracker.markAttached(newRVA("rv-1", "b"))
		tracker.markAttached(newRVA("rv-2", "c"))

		tracker.forgetVolume("rv-1")

		if _, ok := tracker.byRV["rv-1"]; ok || !tracker.wasAttached(newRVA("rv-2", "c")) {
			t.Fatalf("expected only rv-1 to be dropped, got %v", tracker.byRV)
		}
	})
}

func TestDatameshMetricLabels(t *testing.T) {
	rv := &v1alpha1.ReplicatedVolume{
		Spec: v1alpha1.ReplicatedVolumeSpec{
			ReplicatedStorageClassName: "sc-a",
		},
	}

	cases := []struct {
		name       string
		transition v1alpha1.ReplicatedVolumeDatameshTransition
		rvrs       []*v1alpha1.ReplicatedVolumeReplica
		wantNode   string
	}{
		{
			name: "global transition",
			transition: v1alpha1.ReplicatedVolumeDatameshTransition{
				Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation,
			},
			wantNode: "global",
		},
		{
			name: "known replica node",
			transition: v1alpha1.ReplicatedVolumeDatameshTransition{
				Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach,
				ReplicaName: "rvr-a",
			},
			rvrs: []*v1alpha1.ReplicatedVolumeReplica{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rvr-a"},
					Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-a"},
				},
			},
			wantNode: "node-a",
		},
		{
			name: "unknown replica node",
			transition: v1alpha1.ReplicatedVolumeDatameshTransition{
				Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach,
				ReplicaName: "missing-rvr",
			},
			wantNode: "unknown",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			storageClass, node, typ := datameshMetricLabels(rv, &tt.transition, datameshReplicaNodes(tt.rvrs))
			if storageClass != "sc-a" || node != tt.wantNode || typ != string(tt.transition.Type) {
				t.Fatalf("unexpected labels: storageClass=%q node=%q type=%q", storageClass, node, typ)
			}
		})
	}
}

func TestDatameshMetricLabelsManualVolume(t *testing.T) {
	// A Manual volume has no storage class; it is reported as "unknown", like in current metrics.
	rv := &v1alpha1.ReplicatedVolume{}
	transition := v1alpha1.ReplicatedVolumeDatameshTransition{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation}
	if storageClass, _, _ := datameshMetricLabels(rv, &transition, nil); storageClass != "unknown" {
		t.Fatalf("expected storage class \"unknown\", got %q", storageClass)
	}
}

// resetDatameshMetrics deletes datamesh series of the "sc-a" storage class used by these tests.
func resetDatameshMetrics(node, typ, step string) {
	const storageClass = "sc-a"
	metrics.DatameshTransitionDuration.DeleteLabelValues(storageClass, node, typ)
	metrics.DatameshStepDuration.DeleteLabelValues(storageClass, node, typ, step)
}

type prometheusSample struct {
	labels         map[string]string
	histogramCount uint64
	histogramSum   float64
}

func collectPrometheusSamples(t *testing.T, collector prometheus.Collector) []prometheusSample {
	t.Helper()

	ch := make(chan prometheus.Metric, 100)
	go func() {
		defer close(ch)
		collector.Collect(ch)
	}()

	var samples []prometheusSample
	var writeErr error
	for metric := range ch {
		var dtoMetric dto.Metric
		if err := metric.Write(&dtoMetric); err != nil {
			if writeErr == nil {
				writeErr = err
			}
			continue
		}
		labels := make(map[string]string, len(dtoMetric.Label))
		for _, label := range dtoMetric.Label {
			labels[label.GetName()] = label.GetValue()
		}
		sample := prometheusSample{labels: labels}
		if dtoMetric.Histogram != nil {
			sample.histogramCount = dtoMetric.Histogram.GetSampleCount()
			sample.histogramSum = dtoMetric.Histogram.GetSampleSum()
		}
		samples = append(samples, sample)
	}
	if writeErr != nil {
		t.Fatalf("writing metric: %v", writeErr)
	}
	return samples
}

func findPrometheusSample(samples []prometheusSample, labels map[string]string) (prometheusSample, bool) {
	for _, sample := range samples {
		matches := true
		for name, value := range labels {
			if sample.labels[name] != value {
				matches = false
				break
			}
		}
		if matches {
			return sample, true
		}
	}
	return prometheusSample{}, false
}

func assertHistogramSample(t *testing.T, collector prometheus.Collector, labels map[string]string, sum float64) {
	t.Helper()

	const count uint64 = 1
	sample, ok := findPrometheusSample(collectPrometheusSamples(t, collector), labels)
	if !ok {
		t.Fatalf("expected histogram sample with labels %#v", labels)
	}
	if sample.histogramCount != count || sample.histogramSum != sum {
		t.Fatalf("expected histogram count/sum %d/%v, got %d/%v", count, sum, sample.histogramCount, sample.histogramSum)
	}
}

func assertNoMetricSample(t *testing.T, collector prometheus.Collector, labels map[string]string) {
	t.Helper()

	if sample, ok := findPrometheusSample(collectPrometheusSamples(t, collector), labels); ok {
		t.Fatalf("expected no sample with labels %#v, got %#v", labels, sample)
	}
}
