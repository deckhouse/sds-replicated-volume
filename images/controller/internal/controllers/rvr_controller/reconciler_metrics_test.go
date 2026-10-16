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
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/metrics"
)

// metricsTestMu serializes tests that mutate package-level Prometheus collectors.
// Do not call t.Parallel in subtests that touch these process-global collectors.
var metricsTestMu sync.Mutex

func TestComputeRVRMetricObservationsCountsReadyLost(t *testing.T) {
	metricsTestMu.Lock()
	defer metricsTestMu.Unlock()

	readyCond := func(status metav1.ConditionStatus, reason string) *metav1.Condition {
		return &metav1.Condition{
			Type:   v1alpha1.ReplicatedVolumeReplicaCondReadyType,
			Status: status,
			Reason: reason,
		}
	}
	makeRVR := func(cond *metav1.Condition) *v1alpha1.ReplicatedVolumeReplica {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					v1alpha1.ReplicatedStorageClassLabelKey: "sc-a",
				},
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-a"},
		}
		if cond != nil {
			rvr.Status.Conditions = []metav1.Condition{*cond}
		}
		return rvr
	}
	deleteTime := metav1.Now()

	tests := []struct {
		name       string
		base       *metav1.Condition
		current    *metav1.Condition
		deleting   bool
		wantReason string // empty means nothing is counted
	}{
		{
			name:       "True to False counts the new reason",
			base:       readyCond(metav1.ConditionTrue, v1alpha1.ReplicatedVolumeReplicaCondReadyReasonReady),
			current:    readyCond(metav1.ConditionFalse, v1alpha1.ReplicatedVolumeReplicaCondReadyReasonQuorumLost),
			wantReason: v1alpha1.ReplicatedVolumeReplicaCondReadyReasonQuorumLost,
		},
		{
			name:       "True to Unknown counts",
			base:       readyCond(metav1.ConditionTrue, v1alpha1.ReplicatedVolumeReplicaCondReadyReasonReady),
			current:    readyCond(metav1.ConditionUnknown, v1alpha1.ReplicatedVolumeReplicaCondReadyReasonAgentNotReady),
			wantReason: v1alpha1.ReplicatedVolumeReplicaCondReadyReasonAgentNotReady,
		},
		{
			name:       "True to missing condition counts as Absent",
			base:       readyCond(metav1.ConditionTrue, v1alpha1.ReplicatedVolumeReplicaCondReadyReasonReady),
			wantReason: metrics.ReasonAbsent,
		},
		{
			name:    "True stays True",
			base:    readyCond(metav1.ConditionTrue, v1alpha1.ReplicatedVolumeReplicaCondReadyReasonReady),
			current: readyCond(metav1.ConditionTrue, v1alpha1.ReplicatedVolumeReplicaCondReadyReasonQuorumViaPeers),
		},
		{
			name:    "non-True to True is a recovery, not a loss",
			base:    readyCond(metav1.ConditionFalse, v1alpha1.ReplicatedVolumeReplicaCondReadyReasonQuorumLost),
			current: readyCond(metav1.ConditionTrue, v1alpha1.ReplicatedVolumeReplicaCondReadyReasonReady),
		},
		{
			name:    "non-True stays non-True",
			base:    readyCond(metav1.ConditionUnknown, v1alpha1.ReplicatedVolumeReplicaCondReadyReasonAgentNotReady),
			current: readyCond(metav1.ConditionFalse, v1alpha1.ReplicatedVolumeReplicaCondReadyReasonQuorumLost),
		},
		{
			name:     "deleting replica is not counted",
			base:     readyCond(metav1.ConditionTrue, v1alpha1.ReplicatedVolumeReplicaCondReadyReasonReady),
			current:  readyCond(metav1.ConditionFalse, v1alpha1.ReplicatedVolumeReplicaCondReadyReasonTerminating),
			deleting: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metrics.RVRReadyLost.Reset()

			base := makeRVR(tt.base)
			rvr := makeRVR(tt.current)
			if tt.deleting {
				rvr.DeletionTimestamp = &deleteTime
				rvr.Finalizers = []string{"test"}
			}

			computeRVRMetricObservations(time.Now(), rvr, base, nil, nil).observe()

			if tt.wantReason == "" {
				if n := testutil.CollectAndCount(metrics.RVRReadyLost); n != 0 {
					t.Fatalf("expected no Ready lost series, got %d", n)
				}
				return
			}
			if n := testutil.CollectAndCount(metrics.RVRReadyLost); n != 1 {
				t.Fatalf("expected exactly one Ready lost series, got %d", n)
			}
			if got := testutil.ToFloat64(metrics.RVRReadyLost.WithLabelValues("node-a", "sc-a", tt.wantReason)); got != 1 {
				t.Fatalf("expected Ready lost counter 1 for reason %q, got %v", tt.wantReason, got)
			}
		})
	}
	metrics.RVRReadyLost.Reset()
}

func TestComputeRVRMetricObservationsRecordsBackingVolumeProvisioning(t *testing.T) {
	metricsTestMu.Lock()
	defer metricsTestMu.Unlock()

	rvrCreated := metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	llvCreated := metav1.NewTime(rvrCreated.Add(time.Hour))
	now := llvCreated.Add(3 * time.Second)

	bvCond := func(status metav1.ConditionStatus, reason string) *metav1.Condition {
		return &metav1.Condition{
			Type:   v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyType,
			Status: status,
			Reason: reason,
		}
	}
	makeRVR := func(cond *metav1.Condition) *v1alpha1.ReplicatedVolumeReplica {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				CreationTimestamp: rvrCreated,
				Labels: map[string]string{
					v1alpha1.ReplicatedStorageClassLabelKey: "sc-a",
				},
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-a"},
		}
		if cond != nil {
			rvr.Status.Conditions = []metav1.Condition{*cond}
		}
		return rvr
	}
	ready := bvCond(metav1.ConditionTrue, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonReady)
	llv := &snc.LVMLogicalVolume{ObjectMeta: metav1.ObjectMeta{CreationTimestamp: llvCreated}}

	tests := []struct {
		name    string
		base    *metav1.Condition
		current *metav1.Condition
		llv     *snc.LVMLogicalVolume
		want    bool // observed, measured from the LLV creation
	}{
		{
			name:    "Provisioning to Ready is observed",
			base:    bvCond(metav1.ConditionFalse, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonProvisioning),
			current: ready, llv: llv, want: true,
		},
		{
			name:    "Reprovisioning to Ready is observed",
			base:    bvCond(metav1.ConditionFalse, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonReprovisioning),
			current: ready, llv: llv, want: true,
		},
		{
			name:    "ProvisioningFailed to Ready is observed",
			base:    bvCond(metav1.ConditionFalse, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonProvisioningFailed),
			current: ready, llv: llv, want: true,
		},
		{
			name:    "Resizing to Ready is not provisioning",
			base:    bvCond(metav1.ConditionFalse, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonResizing),
			current: ready, llv: llv,
		},
		{
			name:    "NotReady to Ready is not provisioning",
			base:    bvCond(metav1.ConditionFalse, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonNotReady),
			current: ready, llv: llv,
		},
		{
			name:    "absent condition to Ready means an existing LLV was picked up",
			current: ready, llv: llv,
		},
		{
			name:    "Ready stays Ready",
			base:    ready,
			current: ready, llv: llv,
		},
		{
			name:    "still provisioning",
			base:    bvCond(metav1.ConditionFalse, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonProvisioning),
			current: bvCond(metav1.ConditionFalse, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonProvisioning),
			llv:     llv,
		},
		{
			name:    "LLV not in cache",
			base:    bvCond(metav1.ConditionFalse, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonProvisioning),
			current: ready,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metrics.RVRBackingVolumeDuration.Reset()

			computeRVRMetricObservations(now, makeRVR(tt.current), makeRVR(tt.base), nil, tt.llv).observe()

			if !tt.want {
				if n := testutil.CollectAndCount(metrics.RVRBackingVolumeDuration); n != 0 {
					t.Fatalf("expected no backing volume observation, got %d series", n)
				}
				return
			}
			assertHistogramSample(t, metrics.RVRBackingVolumeDuration, map[string]string{
				metrics.LabelNode:         "node-a",
				metrics.LabelStorageClass: "sc-a",
			}, 1, 3)
		})
	}
	metrics.RVRBackingVolumeDuration.Reset()
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
