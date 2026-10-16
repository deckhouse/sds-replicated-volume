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
	"k8s.io/apimachinery/pkg/types"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/metrics"
)

// metricsTestMu serializes tests that mutate package-level Prometheus collectors.
// Do not call t.Parallel in subtests that touch these process-global collectors.
var metricsTestMu sync.Mutex

func TestComputeDRBDRMetricObservationsRecordsFirstConfiguration(t *testing.T) {
	metricsTestMu.Lock()
	defer metricsTestMu.Unlock()

	agentStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	created := metav1.NewTime(agentStart.Add(time.Minute))
	now := created.Add(20 * time.Second)

	configuredCond := func(status metav1.ConditionStatus) []metav1.Condition {
		return []metav1.Condition{{Type: v1alpha1.DRBDResourceCondConfiguredType, Status: status}}
	}
	makeDRBDR := func(uid types.UID, createdAt metav1.Time, conds []metav1.Condition) *v1alpha1.DRBDResource {
		return &v1alpha1.DRBDResource{
			ObjectMeta: metav1.ObjectMeta{Name: "res", UID: uid, CreationTimestamp: createdAt},
			Status:     v1alpha1.DRBDResourceStatus{Conditions: conds},
		}
	}
	// observe runs one reconcile transition and returns the number of new histogram samples.
	observe := func(tracker *configuredDRBDRTracker, base, current *v1alpha1.DRBDResource) (uint64, float64) {
		beforeCount, beforeSum := collectHistogramCountSum(t, metrics.DRBDRConfiguredDuration)
		computeDRBDRMetricObservations(now, current, base, tracker).observe()
		afterCount, afterSum := collectHistogramCountSum(t, metrics.DRBDRConfiguredDuration)
		return afterCount - beforeCount, afterSum - beforeSum
	}

	t.Run("first False to True is observed from creation", func(t *testing.T) {
		tracker := newConfiguredDRBDRTracker(agentStart)
		count, sum := observe(tracker,
			makeDRBDR("a", created, configuredCond(metav1.ConditionFalse)),
			makeDRBDR("a", created, configuredCond(metav1.ConditionTrue)))
		if count != 1 || sum != 20 {
			t.Fatalf("expected count/sum 1/20, got %d/%v", count, sum)
		}
	})

	t.Run("first configuration without a previous condition is observed", func(t *testing.T) {
		tracker := newConfiguredDRBDRTracker(agentStart)
		count, _ := observe(tracker,
			makeDRBDR("a", created, nil),
			makeDRBDR("a", created, configuredCond(metav1.ConditionTrue)))
		if count != 1 {
			t.Fatalf("expected one observation, got %d", count)
		}
	})

	t.Run("returning to True after a failure is not observed", func(t *testing.T) {
		tracker := newConfiguredDRBDRTracker(agentStart)
		base := makeDRBDR("a", created, configuredCond(metav1.ConditionFalse))
		current := makeDRBDR("a", created, configuredCond(metav1.ConditionTrue))
		observe(tracker, base, current)
		if count, _ := observe(tracker, base, current); count != 0 {
			t.Fatalf("expected no observation for a repeated configuration, got %d", count)
		}
	})

	t.Run("True stays True", func(t *testing.T) {
		tracker := newConfiguredDRBDRTracker(agentStart)
		count, _ := observe(tracker,
			makeDRBDR("a", created, configuredCond(metav1.ConditionTrue)),
			makeDRBDR("a", created, configuredCond(metav1.ConditionTrue)))
		if count != 0 {
			t.Fatalf("expected no observation, got %d", count)
		}
	})

	t.Run("resource created before the agent started is not observed", func(t *testing.T) {
		tracker := newConfiguredDRBDRTracker(agentStart)
		old := metav1.NewTime(agentStart.Add(-time.Hour))
		count, _ := observe(tracker,
			makeDRBDR("a", old, configuredCond(metav1.ConditionFalse)),
			makeDRBDR("a", old, configuredCond(metav1.ConditionTrue)))
		if count != 0 {
			t.Fatalf("expected no observation, got %d", count)
		}
	})

	t.Run("forget releases the resource", func(t *testing.T) {
		tracker := newConfiguredDRBDRTracker(agentStart)
		base := makeDRBDR("a", created, configuredCond(metav1.ConditionFalse))
		current := makeDRBDR("a", created, configuredCond(metav1.ConditionTrue))
		observe(tracker, base, current)
		tracker.forget("res")
		if count, _ := observe(tracker, base, current); count != 1 {
			t.Fatalf("expected the forgotten resource to be observed again, got %d", count)
		}
	})

	t.Run("resource recreated under the same name is observed", func(t *testing.T) {
		tracker := newConfiguredDRBDRTracker(agentStart)
		observe(tracker,
			makeDRBDR("a", created, configuredCond(metav1.ConditionFalse)),
			makeDRBDR("a", created, configuredCond(metav1.ConditionTrue)))
		count, _ := observe(tracker,
			makeDRBDR("b", created, configuredCond(metav1.ConditionFalse)),
			makeDRBDR("b", created, configuredCond(metav1.ConditionTrue)))
		if count != 1 {
			t.Fatalf("expected the recreated resource to be observed, got %d", count)
		}
		if len(tracker.byName) != 1 || tracker.byName["res"] != "b" {
			t.Fatalf("expected one entry for the recreated resource, got %v", tracker.byName)
		}
	})
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
