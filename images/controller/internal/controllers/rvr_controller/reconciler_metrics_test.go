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
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/metrics"
)

// metricsTestMu serializes tests that mutate package-level Prometheus collectors.
// Do not call t.Parallel in subtests that touch these process-global collectors.
var metricsTestMu sync.Mutex

func TestComputeRVRMetricObservationsRecordsReadyTransition(t *testing.T) {
	metricsTestMu.Lock()
	defer metricsTestMu.Unlock()

	start := metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	readyAt := metav1.NewTime(start.Add(10 * time.Second))
	metrics.RVRReadyDuration.DeleteLabelValues("node-a", "sc-a")

	base := &v1alpha1.ReplicatedVolumeReplica{
		Status: v1alpha1.ReplicatedVolumeReplicaStatus{
			Conditions: []metav1.Condition{
				{
					Type:               v1alpha1.ReplicatedVolumeReplicaCondReadyType,
					Status:             metav1.ConditionFalse,
					LastTransitionTime: start,
				},
			},
		},
	}
	rvr := &v1alpha1.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{
			CreationTimestamp: start,
			Labels: map[string]string{
				v1alpha1.ReplicatedStorageClassLabelKey: "sc-a",
			},
		},
		Spec: v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-a"},
		Status: v1alpha1.ReplicatedVolumeReplicaStatus{
			Conditions: []metav1.Condition{
				{
					Type:               v1alpha1.ReplicatedVolumeReplicaCondReadyType,
					Status:             metav1.ConditionTrue,
					LastTransitionTime: readyAt,
				},
			},
		},
	}

	observations := computeRVRMetricObservations(readyAt.Time, rvr, base, nil)
	if len(observations) != 1 {
		t.Fatalf("expected one Ready transition observation, got %d", len(observations))
	}
	observations.observe()

	assertHistogramSample(t, metrics.RVRReadyDuration, map[string]string{
		metrics.LabelNode:         "node-a",
		metrics.LabelStorageClass: "sc-a",
	}, 1, 10)
}

func TestComputeRVRMetricObservationsDoesNotRepeatReadyTransition(t *testing.T) {
	readyAt := metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 10, 0, time.UTC))

	base := &v1alpha1.ReplicatedVolumeReplica{
		Status: v1alpha1.ReplicatedVolumeReplicaStatus{
			Conditions: []metav1.Condition{
				{
					Type:               v1alpha1.ReplicatedVolumeReplicaCondReadyType,
					Status:             metav1.ConditionTrue,
					LastTransitionTime: readyAt,
				},
			},
		},
	}
	rvr := base.DeepCopy()

	observations := computeRVRMetricObservations(readyAt.Time, rvr, base, nil)
	if len(observations) != 0 {
		t.Fatalf("expected no repeated Ready observation, got %d", len(observations))
	}
}

func TestComputeRVRMetricObservationsIgnoresInvalidReadyDuration(t *testing.T) {
	readyAt := metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 10, 0, time.UTC))
	base := &v1alpha1.ReplicatedVolumeReplica{
		Status: v1alpha1.ReplicatedVolumeReplicaStatus{
			Conditions: []metav1.Condition{
				{
					Type:               v1alpha1.ReplicatedVolumeReplicaCondReadyType,
					Status:             metav1.ConditionFalse,
					LastTransitionTime: readyAt,
				},
			},
		},
	}
	rvr := &v1alpha1.ReplicatedVolumeReplica{
		Status: v1alpha1.ReplicatedVolumeReplicaStatus{
			Conditions: []metav1.Condition{
				{
					Type:               v1alpha1.ReplicatedVolumeReplicaCondReadyType,
					Status:             metav1.ConditionTrue,
					LastTransitionTime: readyAt,
				},
			},
		},
	}

	observations := computeRVRMetricObservations(readyAt.Time, rvr, base, nil)
	if len(observations) != 0 {
		t.Fatalf("expected no observation for zero Ready duration, got %d", len(observations))
	}
}

func TestComputeRVRMetricObservationsSkipsDeletingReadyTransition(t *testing.T) {
	deleteTime := metav1.Now()
	readyAt := metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 10, 0, time.UTC))
	base := &v1alpha1.ReplicatedVolumeReplica{}
	rvr := &v1alpha1.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{
			DeletionTimestamp: &deleteTime,
			Finalizers:        []string{"test"},
		},
		Status: v1alpha1.ReplicatedVolumeReplicaStatus{
			Conditions: []metav1.Condition{
				{
					Type:               v1alpha1.ReplicatedVolumeReplicaCondReadyType,
					Status:             metav1.ConditionTrue,
					LastTransitionTime: readyAt,
				},
			},
		},
	}

	observations := computeRVRMetricObservations(readyAt.Time, rvr, base, nil)
	if len(observations) != 0 {
		t.Fatalf("expected no Ready observation for deleting RVR, got %d", len(observations))
	}
}

func TestComputeRVRMetricObservationsRecordsBackingVolumeReadyTransition(t *testing.T) {
	metricsTestMu.Lock()
	defer metricsTestMu.Unlock()

	start := metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	now := start.Add(20 * time.Second)
	metrics.RVRBackingVolumeDuration.DeleteLabelValues("node-a", "sc-a")

	base := &v1alpha1.ReplicatedVolumeReplica{}
	rvr := &v1alpha1.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{
			CreationTimestamp: start,
			Labels: map[string]string{
				v1alpha1.ReplicatedStorageClassLabelKey: "sc-a",
			},
		},
		Spec: v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-a"},
		Status: v1alpha1.ReplicatedVolumeReplicaStatus{
			Conditions: []metav1.Condition{
				{
					Type:   v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyType,
					Status: metav1.ConditionTrue,
				},
			},
		},
	}

	observations := computeRVRMetricObservations(now, rvr, base, nil)
	if len(observations) != 1 {
		t.Fatalf("expected one BackingVolumeReady observation, got %d", len(observations))
	}
	observations.observe()

	assertHistogramSample(t, metrics.RVRBackingVolumeDuration, map[string]string{
		metrics.LabelNode:         "node-a",
		metrics.LabelStorageClass: "sc-a",
	}, 1, 20)
}

func TestRVRStorageClassLabelFallsBackToRV(t *testing.T) {
	rvr := &v1alpha1.ReplicatedVolumeReplica{}
	rv := &v1alpha1.ReplicatedVolume{
		Spec: v1alpha1.ReplicatedVolumeSpec{
			ReplicatedStorageClassName: "sc-from-rv",
		},
	}

	if got := rvrStorageClassLabel(rvr, rv); got != "sc-from-rv" {
		t.Fatalf("expected RV storage class fallback, got %q", got)
	}
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

func assertHistogramSample(t *testing.T, collector prometheus.Collector, labels map[string]string, count uint64, sum float64) {
	t.Helper()

	sample, ok := findPrometheusSample(collectPrometheusSamples(t, collector), labels)
	if !ok {
		t.Fatalf("expected histogram sample with labels %#v", labels)
	}
	if sample.histogramCount != count || sample.histogramSum != sum {
		t.Fatalf("expected histogram count/sum %d/%v, got %d/%v", count, sum, sample.histogramCount, sample.histogramSum)
	}
}
