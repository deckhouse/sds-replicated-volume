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

package metrics

import (
	"slices"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func TestCollectStorageClassNamesUsesUnknownForMissingLabels(t *testing.T) {
	storageClasses := collectStorageClassNames(
		nil,
		nil,
		[]v1alpha1.ReplicatedVolumeReplica{
			{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{}}},
		},
		[]v1alpha1.ReplicatedVolumeAttachment{
			{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{}}},
		},
	)

	if !slices.Equal(storageClasses, []string{currentMetricsSCUnknown}) {
		t.Fatalf("unexpected storage classes: %v", storageClasses)
	}
}

func TestCollectRVCountsEmitsStorageClassPhaseMatrix(t *testing.T) {
	deleteTime := metav1.Now()
	ch := make(chan prometheus.Metric, 100)
	desc := prometheus.NewDesc(
		"test_rv_count",
		"test",
		[]string{LabelStorageClass, LabelPhase},
		nil,
	)

	go func() {
		defer close(ch)
		collectRVCounts(
			ch,
			desc,
			[]string{"sc-a", "sc-b"},
			[]v1alpha1.ReplicatedVolume{
				{
					Spec: v1alpha1.ReplicatedVolumeSpec{
						ReplicatedStorageClassName: "sc-a",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						DeletionTimestamp: &deleteTime,
						Finalizers:        []string{"test"},
					},
					Spec: v1alpha1.ReplicatedVolumeSpec{
						ReplicatedStorageClassName: "sc-a",
					},
				},
			},
		)
	}()

	metrics := collectTestMetrics(t, ch)
	if len(metrics) != 4 {
		t.Fatalf("expected storage_class x phase matrix, got %d: %#v", len(metrics), metrics)
	}
	assertMetric(t, metrics[0], 1, map[string]string{
		LabelStorageClass: "sc-a",
		LabelPhase:        currentMetricsRVPhaseActive,
	})
	assertMetric(t, metrics[1], 1, map[string]string{
		LabelStorageClass: "sc-a",
		LabelPhase:        currentMetricsRVPhaseDeleting,
	})
	assertMetric(t, metrics[2], 0, map[string]string{
		LabelStorageClass: "sc-b",
		LabelPhase:        currentMetricsRVPhaseActive,
	})
	assertMetric(t, metrics[3], 0, map[string]string{
		LabelStorageClass: "sc-b",
		LabelPhase:        currentMetricsRVPhaseDeleting,
	})
}

func TestCollectRVRCountsEmitsOnlyNonZeroCombinations(t *testing.T) {
	ch := make(chan prometheus.Metric, 100)
	desc := prometheus.NewDesc(
		"test_rvr_count",
		"test",
		[]string{LabelNode, LabelStorageClass, LabelPhase},
		nil,
	)

	go func() {
		defer close(ch)
		collectRVRCounts(ch, desc, []v1alpha1.ReplicatedVolumeReplica{
			{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						v1alpha1.ReplicatedStorageClassLabelKey: "sc-a",
					},
				},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-a"},
				Status: v1alpha1.ReplicatedVolumeReplicaStatus{
					Phase: v1alpha1.ReplicatedVolumeReplicaPhaseHealthy,
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						v1alpha1.ReplicatedStorageClassLabelKey: "sc-a",
					},
				},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-a"},
				Status: v1alpha1.ReplicatedVolumeReplicaStatus{
					Phase: v1alpha1.ReplicatedVolumeReplicaPhaseHealthy,
				},
			},
		})
	}()

	metrics := collectTestMetrics(t, ch)
	if len(metrics) != 1 {
		t.Fatalf("expected only one non-zero metric, got %d: %#v", len(metrics), metrics)
	}
	assertMetric(t, metrics[0], 2, map[string]string{
		LabelNode:         "node-a",
		LabelStorageClass: "sc-a",
		LabelPhase:        string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy),
	})
}

func TestCollectRVRDeletingCountsEmitsOnlyDeletingReplicas(t *testing.T) {
	deleteTime := metav1.Now()
	ch := make(chan prometheus.Metric, 100)
	desc := prometheus.NewDesc(
		"test_rvr_deleting_count",
		"test",
		[]string{LabelNode, LabelStorageClass},
		nil,
	)

	go func() {
		defer close(ch)
		collectRVRDeletingCounts(ch, desc, []v1alpha1.ReplicatedVolumeReplica{
			{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						v1alpha1.ReplicatedStorageClassLabelKey: "sc-a",
					},
				},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-a"},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					DeletionTimestamp: &deleteTime,
					Finalizers:        []string{"test"},
					Labels: map[string]string{
						v1alpha1.ReplicatedStorageClassLabelKey: "sc-a",
					},
				},
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-a"},
			},
		})
	}()

	metrics := collectTestMetrics(t, ch)
	if len(metrics) != 1 {
		t.Fatalf("expected one deleting metric, got %d: %#v", len(metrics), metrics)
	}
	assertMetric(t, metrics[0], 1, map[string]string{
		LabelNode:         "node-a",
		LabelStorageClass: "sc-a",
	})
}

func TestCollectRVACountsFallsBackToRVStorageClass(t *testing.T) {
	ch := make(chan prometheus.Metric, 100)
	desc := prometheus.NewDesc(
		"test_rva_count",
		"test",
		[]string{LabelNode, LabelStorageClass, LabelPhase},
		nil,
	)

	go func() {
		defer close(ch)
		collectRVACounts(
			ch,
			desc,
			[]v1alpha1.ReplicatedVolume{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rv-a"},
					Spec: v1alpha1.ReplicatedVolumeSpec{
						ReplicatedStorageClassName: "sc-from-rv",
					},
				},
			},
			[]v1alpha1.ReplicatedVolumeAttachment{
				{
					Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
						ReplicatedVolumeName: "rv-a",
						NodeName:             "node-a",
					},
					Status: v1alpha1.ReplicatedVolumeAttachmentStatus{
						Phase: v1alpha1.ReplicatedVolumeAttachmentPhaseAttached,
					},
				},
			},
		)
	}()

	metrics := collectTestMetrics(t, ch)
	if len(metrics) != 1 {
		t.Fatalf("expected one metric, got %d: %#v", len(metrics), metrics)
	}
	assertMetric(t, metrics[0], 1, map[string]string{
		LabelNode:         "node-a",
		LabelStorageClass: "sc-from-rv",
		LabelPhase:        string(v1alpha1.ReplicatedVolumeAttachmentPhaseAttached),
	})
}

func TestCollectDatameshActiveTransitionsUsesGlobalAndUnknownNodes(t *testing.T) {
	ch := make(chan prometheus.Metric, 100)
	desc := prometheus.NewDesc(
		"test_datamesh_active_transitions",
		"test",
		[]string{LabelStorageClass, LabelNode, LabelType},
		nil,
	)

	go func() {
		defer close(ch)
		collectDatameshActiveTransitions(
			ch,
			desc,
			[]v1alpha1.ReplicatedVolume{
				{
					Spec: v1alpha1.ReplicatedVolumeSpec{
						ReplicatedStorageClassName: "sc-a",
					},
					Status: v1alpha1.ReplicatedVolumeStatus{
						DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
							{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation},
							{
								Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach,
								ReplicaName: "missing-rvr",
							},
						},
					},
				},
			},
			nil,
		)
	}()

	metrics := collectTestMetrics(t, ch)
	if len(metrics) != 2 {
		t.Fatalf("expected two non-zero metrics, got %d: %#v", len(metrics), metrics)
	}
	assertMetric(t, metrics[0], 1, map[string]string{
		LabelStorageClass: "sc-a",
		LabelNode:         currentMetricsNodeGlobal,
		LabelType:         string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation),
	})
	assertMetric(t, metrics[1], 1, map[string]string{
		LabelStorageClass: "sc-a",
		LabelNode:         currentMetricsNodeUnknown,
		LabelType:         string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach),
	})
}

func TestCollectRVMigratorLabelsEmitsOnlyLabeledRVs(t *testing.T) {
	ch := make(chan prometheus.Metric, 100)
	noPVDesc := prometheus.NewDesc(
		"test_rv_no_persistent_volume",
		"test",
		[]string{LabelName},
		nil,
	)
	blockedDesc := prometheus.NewDesc(
		"test_rv_auto_configuration_blocked",
		"test",
		[]string{LabelName},
		nil,
	)

	go func() {
		defer close(ch)
		collectRVMigratorLabels(ch, noPVDesc, blockedDesc, []v1alpha1.ReplicatedVolume{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "rv-orphan",
					Labels: map[string]string{
						v1alpha1.NoPersistentVolumeLabelKey: v1alpha1.NoPersistentVolumeLabelValue,
					},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "rv-blocked",
					Labels: map[string]string{
						v1alpha1.AutoConfigurationBlockedLabelKey: v1alpha1.AutoConfigurationBlockedLabelValue,
					},
				},
			},
			// Key present with a wrong value must not produce any series.
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "rv-wrong-value",
					Labels: map[string]string{
						v1alpha1.NoPersistentVolumeLabelKey: "false",
					},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-clean"},
			},
		})
	}()

	metrics := collectTestMetrics(t, ch)
	if len(metrics) != 2 {
		t.Fatalf("expected two labeled metrics, got %d: %#v", len(metrics), metrics)
	}
	// collectRVMigratorLabels emits all no-persistent-volume series first (names sorted),
	// then all auto-configuration-blocked series (names sorted). rv-wrong-value has the
	// no-persistent-volume label with value "false", so it is skipped; rv-clean has neither label.
	assertMetric(t, metrics[0], 1, map[string]string{
		LabelName: "rv-orphan",
	})
	assertMetric(t, metrics[1], 1, map[string]string{
		LabelName: "rv-blocked",
	})
}

func TestCollectRVSMetricsEmitsPhaseMatrixAgeAndFailures(t *testing.T) {
	now := metav1.Now()
	deleteTime := metav1.NewTime(now.Add(-time.Minute))
	created := func(ago time.Duration) metav1.Time {
		return metav1.NewTime(now.Add(-ago))
	}

	ch := make(chan prometheus.Metric, 100)
	countDesc := prometheus.NewDesc("test_rvs_count", "test", []string{LabelPhase}, nil)
	ageDesc := prometheus.NewDesc("test_rvs_unfinished_age_seconds", "test", []string{LabelName, LabelPhase}, nil)
	failedDesc := prometheus.NewDesc("test_rvs_failed", "test", []string{LabelName}, nil)

	go func() {
		defer close(ch)
		collectRVSMetrics(ch, countDesc, ageDesc, failedDesc, now.Time, []v1alpha1.ReplicatedVolumeSnapshot{
			// Ready snapshots are settled: counted, but neither aged nor failed.
			{
				ObjectMeta: metav1.ObjectMeta{Name: "snap-ready", CreationTimestamp: created(time.Hour)},
				Status:     v1alpha1.ReplicatedVolumeSnapshotStatus{Phase: v1alpha1.ReplicatedVolumeSnapshotPhaseReady},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "snap-syncing", CreationTimestamp: created(10 * time.Minute)},
				Status:     v1alpha1.ReplicatedVolumeSnapshotStatus{Phase: v1alpha1.ReplicatedVolumeSnapshotPhaseSynchronizing},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "snap-failed", CreationTimestamp: created(time.Hour)},
				Status:     v1alpha1.ReplicatedVolumeSnapshotStatus{Phase: v1alpha1.ReplicatedVolumeSnapshotPhaseFailed},
			},
			// An empty phase means the controller has not written status yet: Pending.
			{
				ObjectMeta: metav1.ObjectMeta{Name: "snap-nostatus", CreationTimestamp: created(30 * time.Second)},
			},
			// A deleting snapshot reports Deleting whatever the last written phase was.
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "snap-deleting",
					CreationTimestamp: created(5 * time.Minute),
					DeletionTimestamp: &deleteTime,
					Finalizers:        []string{"test"},
				},
				Status: v1alpha1.ReplicatedVolumeSnapshotStatus{Phase: v1alpha1.ReplicatedVolumeSnapshotPhaseReady},
			},
		})
	}()

	metrics := collectTestMetrics(t, ch)

	byPhase := map[string]float64{}
	ages := map[string]testMetric{}
	failed := map[string]float64{}
	for _, m := range metrics {
		switch {
		case len(m.labels) == 1 && m.labels[LabelPhase] != "":
			byPhase[m.labels[LabelPhase]] = m.value
		case len(m.labels) == 2:
			ages[m.labels[LabelName]] = m
		default:
			failed[m.labels[LabelName]] = m.value
		}
	}

	// Every known phase is emitted, so an empty phase reads as 0 instead of vanishing.
	wantCounts := map[string]float64{
		string(v1alpha1.ReplicatedVolumeSnapshotPhasePending):       1, // snap-nostatus
		string(v1alpha1.ReplicatedVolumeSnapshotPhaseInProgress):    0,
		string(v1alpha1.ReplicatedVolumeSnapshotPhaseSynchronizing): 1,
		string(v1alpha1.ReplicatedVolumeSnapshotPhaseReady):         1, // snap-ready only
		string(v1alpha1.ReplicatedVolumeSnapshotPhaseFailed):        1,
		string(v1alpha1.ReplicatedVolumeSnapshotPhaseDeleting):      1, // snap-deleting
	}
	if len(byPhase) != len(wantCounts) {
		t.Fatalf("expected %d phase series, got %d: %#v", len(wantCounts), len(byPhase), byPhase)
	}
	for phase, want := range wantCounts {
		if byPhase[phase] != want {
			t.Errorf("count for phase %q = %v, want %v", phase, byPhase[phase], want)
		}
	}

	// Only unfinished snapshots are aged: ready and failed ones are excluded, so
	// the alert built on this metric resolves as soon as a snapshot settles.
	wantAges := map[string]float64{
		"snap-syncing":  600,
		"snap-nostatus": 30,
		"snap-deleting": 300,
	}
	if len(ages) != len(wantAges) {
		t.Fatalf("expected %d age series, got %d: %#v", len(wantAges), len(ages), ages)
	}
	for name, want := range wantAges {
		if ages[name].value != want {
			t.Errorf("age for %q = %v, want %v", name, ages[name].value, want)
		}
	}
	if got := ages["snap-deleting"].labels[LabelPhase]; got != string(v1alpha1.ReplicatedVolumeSnapshotPhaseDeleting) {
		t.Errorf("deleting snapshot reported phase %q, want Deleting", got)
	}

	if len(failed) != 1 || failed["snap-failed"] != 1 {
		t.Errorf("expected exactly one failed marker for snap-failed, got %#v", failed)
	}
}

// An unknown phase from a newer API version must be counted, not dropped.
func TestCollectRVSMetricsCountsUnknownPhase(t *testing.T) {
	now := metav1.Now()
	ch := make(chan prometheus.Metric, 50)
	countDesc := prometheus.NewDesc("test_rvs_count", "test", []string{LabelPhase}, nil)
	ageDesc := prometheus.NewDesc("test_rvs_unfinished_age_seconds", "test", []string{LabelName, LabelPhase}, nil)
	failedDesc := prometheus.NewDesc("test_rvs_failed", "test", []string{LabelName}, nil)

	go func() {
		defer close(ch)
		collectRVSMetrics(ch, countDesc, ageDesc, failedDesc, now.Time, []v1alpha1.ReplicatedVolumeSnapshot{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "snap-future", CreationTimestamp: now},
				Status:     v1alpha1.ReplicatedVolumeSnapshotStatus{Phase: "SomeNewPhase"},
			},
		})
	}()

	var found bool
	for _, m := range collectTestMetrics(t, ch) {
		if m.labels[LabelPhase] == "SomeNewPhase" && len(m.labels) == 1 {
			found = true
			if m.value != 1 {
				t.Errorf("count for the unknown phase = %v, want 1", m.value)
			}
		}
	}
	if !found {
		t.Error("unknown phase produced no count series")
	}
}

type testMetric struct {
	labels map[string]string
	value  float64
}

func collectTestMetrics(t *testing.T, ch <-chan prometheus.Metric) []testMetric {
	t.Helper()

	var metrics []testMetric
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
		metrics = append(metrics, testMetric{
			labels: labels,
			value:  dtoMetric.GetGauge().GetValue(),
		})
	}
	if writeErr != nil {
		t.Fatalf("writing metric: %v", writeErr)
	}
	return metrics
}

func assertMetric(t *testing.T, metric testMetric, value float64, labels map[string]string) {
	t.Helper()

	if metric.value != value {
		t.Fatalf("expected metric value %v, got %v", value, metric.value)
	}
	for name, value := range labels {
		if metric.labels[name] != value {
			t.Fatalf("expected label %s=%q, got %q in %#v", name, value, metric.labels[name], metric.labels)
		}
	}
}
