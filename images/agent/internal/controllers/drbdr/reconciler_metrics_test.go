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

package drbdr

import (
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/metrics"
)

// metricsTestMu serializes tests that mutate package-level Prometheus collectors.
// Do not call t.Parallel in subtests that touch these process-global collectors.
var metricsTestMu sync.Mutex

func TestComputeDRBDRMetricObservationsRecordsConfiguredTransition(t *testing.T) {
	metricsTestMu.Lock()
	defer metricsTestMu.Unlock()

	start := metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	now := start.Add(20 * time.Second)
	beforeCount, beforeSum := collectHistogramCountSum(t, metrics.DRBDRConfiguredDuration)

	statusBase := &v1alpha1.DRBDResource{
		Status: v1alpha1.DRBDResourceStatus{
			Conditions: []metav1.Condition{
				{
					Type:   v1alpha1.DRBDResourceCondConfiguredType,
					Status: metav1.ConditionFalse,
				},
			},
		},
	}
	drbdr := &v1alpha1.DRBDResource{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "drbdr-a",
			CreationTimestamp: start,
			Labels: map[string]string{
				v1alpha1.ReplicatedVolumeLabelKey: "rv-a",
			},
		},
		Status: v1alpha1.DRBDResourceStatus{
			Conditions: []metav1.Condition{
				{
					Type:   v1alpha1.DRBDResourceCondConfiguredType,
					Status: metav1.ConditionTrue,
				},
			},
		},
	}

	observations := computeDRBDRMetricObservations(now, drbdr, statusBase)
	if len(observations) != 1 {
		t.Fatalf("expected Configured transition observation, got %d", len(observations))
	}
	observations.observe()

	afterCount, afterSum := collectHistogramCountSum(t, metrics.DRBDRConfiguredDuration)
	if afterCount-beforeCount != 1 || afterSum-beforeSum != 20 {
		t.Fatalf("expected configured histogram delta count/sum 1/20, got %d/%v", afterCount-beforeCount, afterSum-beforeSum)
	}
}

func TestComputeDRBDRMetricObservationsDoesNotRepeatConfiguredTransition(t *testing.T) {
	metricsTestMu.Lock()
	defer metricsTestMu.Unlock()

	now := time.Date(2026, 1, 1, 0, 0, 20, 0, time.UTC)
	beforeCount, beforeSum := collectHistogramCountSum(t, metrics.DRBDRConfiguredDuration)
	statusBase := &v1alpha1.DRBDResource{
		Status: v1alpha1.DRBDResourceStatus{
			Conditions: []metav1.Condition{
				{
					Type:   v1alpha1.DRBDResourceCondConfiguredType,
					Status: metav1.ConditionTrue,
				},
			},
		},
	}
	drbdr := statusBase.DeepCopy()
	drbdr.Name = "drbdr-a"
	drbdr.Labels = map[string]string{
		v1alpha1.ReplicatedVolumeLabelKey: "rv-a",
	}

	observations := computeDRBDRMetricObservations(now, drbdr, statusBase)
	if len(observations) != 0 {
		t.Fatalf("expected no repeated Configured transition observation, got %d", len(observations))
	}
	observations.observe()

	afterCount, afterSum := collectHistogramCountSum(t, metrics.DRBDRConfiguredDuration)
	if afterCount != beforeCount || afterSum != beforeSum {
		t.Fatalf("expected no configured histogram change, got before %d/%v after %d/%v", beforeCount, beforeSum, afterCount, afterSum)
	}
}

type prometheusSample struct {
	histogram      bool
	histogramCount uint64
	histogramSum   float64
}

func collectHistogramCountSum(t *testing.T, collector prometheus.Collector) (uint64, float64) {
	t.Helper()

	var found bool
	var count uint64
	var sum float64
	for _, sample := range collectPrometheusSamples(t, collector) {
		if sample.histogram {
			found = true
			count += sample.histogramCount
			sum += sample.histogramSum
		}
	}
	if !found {
		return 0, 0
	}
	return count, sum
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
		sample := prometheusSample{}
		if dtoMetric.Histogram != nil {
			sample.histogram = true
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
