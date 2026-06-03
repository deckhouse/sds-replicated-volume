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
