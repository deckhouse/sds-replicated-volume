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

		observations := computeDatameshMetricObservations(now, rv, []v1alpha1.ReplicatedVolumeDatameshTransition{oldTransition}, nil)
		if len(observations) != 0 {
			t.Fatalf("expected no observations, got %d", len(observations))
		}
	})

	t.Run("completed transition records transition and step durations", func(t *testing.T) {
		oldTransition := newTransition(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted, &done)
		rv.Status.DatameshTransitions = nil
		resetDatameshMetrics("sc-a", "global", string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation), "Formation")

		observations := computeDatameshMetricObservations(now, rv, []v1alpha1.ReplicatedVolumeDatameshTransition{oldTransition}, nil)
		if len(observations) != 2 {
			t.Fatalf("expected transition and step observations, got %d", len(observations))
		}
		observations.observe()

		assertHistogramSample(t, metrics.DatameshTransitionDuration, map[string]string{
			metrics.LabelStorageClass: "sc-a",
			metrics.LabelNode:         "global",
			metrics.LabelType:         string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation),
		}, 20)
		assertCounterSample(t, metrics.DatameshTransitionsCompleted, map[string]string{
			metrics.LabelStorageClass: "sc-a",
			metrics.LabelNode:         "global",
			metrics.LabelType:         string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation),
		}, 1)
		assertHistogramSample(t, metrics.DatameshStepDuration, map[string]string{
			metrics.LabelStorageClass: "sc-a",
			metrics.LabelNode:         "global",
			metrics.LabelType:         string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation),
			metrics.LabelStep:         "Formation",
		}, 10)
	})

	t.Run("completed active step records only step duration", func(t *testing.T) {
		activeOld := newTransition(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, nil)
		activeNew := newTransition(v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted, &done)
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{activeNew}
		resetDatameshMetrics("sc-a", "global", string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation), "Formation")

		observations := computeDatameshMetricObservations(now, rv, []v1alpha1.ReplicatedVolumeDatameshTransition{activeOld}, nil)
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
	)
	if len(observations) != 1 {
		t.Fatalf("expected one RVA attached observation, got %d", len(observations))
	}
	observations.observe()

	assertHistogramSample(t, metrics.RVAReadyDuration, map[string]string{
		metrics.LabelNode: "node-a",
	}, 15)
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

func resetDatameshMetrics(storageClass, node, typ, step string) {
	metrics.DatameshTransitionDuration.DeleteLabelValues(storageClass, node, typ)
	metrics.DatameshTransitionsCompleted.DeleteLabelValues(storageClass, node, typ)
	metrics.DatameshStepDuration.DeleteLabelValues(storageClass, node, typ, step)
}

type prometheusSample struct {
	labels         map[string]string
	histogramCount uint64
	histogramSum   float64
	counter        float64
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
		if dtoMetric.Counter != nil {
			sample.counter = dtoMetric.Counter.GetValue()
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

func assertCounterSample(t *testing.T, collector prometheus.Collector, labels map[string]string, value float64) {
	t.Helper()

	sample, ok := findPrometheusSample(collectPrometheusSamples(t, collector), labels)
	if !ok {
		t.Fatalf("expected counter sample with labels %#v", labels)
	}
	if sample.counter != value {
		t.Fatalf("expected counter value %v, got %v", value, sample.counter)
	}
}

func assertNoMetricSample(t *testing.T, collector prometheus.Collector, labels map[string]string) {
	t.Helper()

	if sample, ok := findPrometheusSample(collectPrometheusSamples(t, collector), labels); ok {
		t.Fatalf("expected no sample with labels %#v, got %#v", labels, sample)
	}
}
