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
	"context"
	"errors"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/yaml"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

type currentMetricsManagerFake struct {
	manager.Manager
	added  manager.Runnable
	addErr error
}

func (m *currentMetricsManagerFake) Add(runnable manager.Runnable) error {
	m.added = runnable
	return m.addErr
}

func (m *currentMetricsManagerFake) GetCache() cache.Cache { return nil }

func (m *currentMetricsManagerFake) GetLogger() logr.Logger { return logr.Discard() }

func TestAddCurrentMetricsCollectorAddsLeaderRunnable(t *testing.T) {
	managerFake := &currentMetricsManagerFake{}

	if err := AddCurrentMetricsCollector(managerFake); err != nil {
		t.Fatalf("adding current metrics collector: %v", err)
	}

	runnable, ok := managerFake.added.(manager.LeaderElectionRunnable)
	if !ok {
		t.Fatalf("added runnable does not implement manager.LeaderElectionRunnable: %T", managerFake.added)
	}
	if !runnable.NeedLeaderElection() {
		t.Fatal("current metrics collector runnable must require leader election")
	}
}

func TestAddCurrentMetricsCollectorReturnsManagerError(t *testing.T) {
	expectedErr := errors.New("add runnable")
	managerFake := &currentMetricsManagerFake{addErr: expectedErr}

	if err := AddCurrentMetricsCollector(managerFake); !errors.Is(err, expectedErr) {
		t.Fatalf("expected manager.Add error %v, got %v", expectedErr, err)
	}
}

// Every series this collector exports is a cluster-wide count read from a cache that each replica
// keeps fully populated. Registering it outside leader election makes all replicas export the same
// numbers under different instance labels, so dashboards and recording rules that aggregate across
// instances report the replica count times the real value.
func TestCurrentMetricsCollectorIsRegisteredOnLeaderOnly(t *testing.T) {
	registerCount := atomic.Int32{}
	registered := make(chan struct{}, 1)
	runnable := &currentMetricsCollectorRunnable{
		registerOnce: &sync.Once{},
		register: func() {
			registerCount.Add(1)
			registered <- struct{}{}
		},
	}

	if !runnable.NeedLeaderElection() {
		t.Fatal("current metrics collector must be registered on the leader replica only")
	}

	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan error, 1)
	go func() {
		started <- runnable.Start(ctx)
	}()

	select {
	case <-registered:
		if got := registerCount.Load(); got != 1 {
			t.Fatalf("expected one collector registration, got %d", got)
		}
	case <-time.After(time.Second):
		t.Fatal("collector registration did not start")
	}

	select {
	case err := <-started:
		t.Fatalf("runnable returned before its context was canceled: %v", err)
	default:
	}

	cancel()
	if err := <-started; err != nil {
		t.Fatalf("runnable returned an error: %v", err)
	}

	ctx, cancel = context.WithCancel(context.Background())
	started = make(chan error, 1)
	go func() {
		started <- runnable.Start(ctx)
	}()
	cancel()

	select {
	case err := <-started:
		if err != nil {
			t.Fatalf("runnable returned an error after the second start: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runnable did not stop after the second context was canceled")
	}

	if got := registerCount.Load(); got != 1 {
		t.Fatalf("expected one collector registration after repeated start, got %d", got)
	}
}

func TestCurrentMetricsCollectorExportsExpectedMetricFamilies(t *testing.T) {
	collector := newCurrentMetricsCollector(nil, logr.Discard())
	descriptions := make(chan *prometheus.Desc, 32)
	collector.Describe(descriptions)
	close(descriptions)

	got := make([]string, 0, len(descriptions))
	for description := range descriptions {
		const prefix = "Desc{fqName: \""
		text := description.String()
		if !strings.HasPrefix(text, prefix) {
			t.Fatalf("unexpected metric description: %q", text)
		}
		name := strings.TrimPrefix(text, prefix)
		name, _, _ = strings.Cut(name, "\"")
		got = append(got, name)
	}

	slices.Sort(got)
	want := []string{
		"sds_deleting_oldest_timestamp_seconds",
		"sds_metrics_collector_objects",
		"sds_metrics_collector_up",
		"sds_rsc_condition_problem_timestamp_seconds",
		"sds_rsc_replicas",
		"sds_rsp_condition_problem_timestamp_seconds",
		"sds_rsp_lvg_info",
		"sds_rv_auto_configuration_blocked",
		"sds_rv_condition_count",
		"sds_rv_condition_problem_timestamp_seconds",
		"sds_rv_count",
		"sds_rv_datamesh_active_transitions",
		"sds_rv_datamesh_transition_oldest_timestamp_seconds",
		"sds_rv_deleting_timestamp_seconds",
		"sds_rv_no_persistent_volume",
		"sds_rva_count",
		"sds_rvr_count",
		"sds_rvr_deleting_count",
		"sds_rvr_not_ready_oldest_timestamp_seconds",
	}

	if !slices.Equal(got, want) {
		t.Fatalf("unexpected current metric families: got %v, want %v", got, want)
	}
}

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

func TestCollectRVCountsEmitsStorageClassModePhaseMatrix(t *testing.T) {
	deleteTime := metav1.Now()
	ch := make(chan prometheus.Metric, 100)
	desc := prometheus.NewDesc(
		"test_rv_count",
		"test",
		[]string{LabelStorageClass, LabelConfigurationMode, LabelPhase},
		nil,
	)

	go func() {
		defer close(ch)
		collectRVCounts(
			ch,
			desc,
			[]string{"sc-a", "sc-b", currentMetricsSCUnknown},
			[]v1alpha1.ReplicatedVolume{
				{
					Spec: v1alpha1.ReplicatedVolumeSpec{
						ConfigurationMode:          v1alpha1.ReplicatedVolumeConfigurationModeAuto,
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
				{
					Spec: v1alpha1.ReplicatedVolumeSpec{
						ConfigurationMode: v1alpha1.ReplicatedVolumeConfigurationModeManual,
					},
				},
			},
		)
	}()

	metrics := collectTestMetrics(t, ch)
	if len(metrics) != 6 {
		t.Fatalf("expected storage_class x phase matrix with one mode per storage class, got %d: %#v", len(metrics), metrics)
	}
	want := []struct {
		value float64
		sc    string
		mode  v1alpha1.ReplicatedVolumeConfigurationMode
		phase string
	}{
		{1, "sc-a", v1alpha1.ReplicatedVolumeConfigurationModeAuto, currentMetricsRVPhaseActive},
		{1, "sc-a", v1alpha1.ReplicatedVolumeConfigurationModeAuto, currentMetricsRVPhaseDeleting},
		{0, "sc-b", v1alpha1.ReplicatedVolumeConfigurationModeAuto, currentMetricsRVPhaseActive},
		{0, "sc-b", v1alpha1.ReplicatedVolumeConfigurationModeAuto, currentMetricsRVPhaseDeleting},
		{1, currentMetricsSCUnknown, v1alpha1.ReplicatedVolumeConfigurationModeManual, currentMetricsRVPhaseActive},
		{0, currentMetricsSCUnknown, v1alpha1.ReplicatedVolumeConfigurationModeManual, currentMetricsRVPhaseDeleting},
	}
	for i, w := range want {
		assertMetric(t, metrics[i], w.value, map[string]string{
			LabelStorageClass:      w.sc,
			LabelConfigurationMode: string(w.mode),
			LabelPhase:             w.phase,
		})
	}
}

func TestCollectRVConditionCountsCountsEveryConditionAndSkipsDeleting(t *testing.T) {
	deleteTime := metav1.Now()
	ch := make(chan prometheus.Metric, 100)
	desc := prometheus.NewDesc(
		"test_rv_condition_count",
		"test",
		[]string{LabelStorageClass, LabelCondition, LabelStatus, LabelReason},
		nil,
	)

	cond := func(condType string, status metav1.ConditionStatus, reason string) metav1.Condition {
		return metav1.Condition{Type: condType, Status: status, Reason: reason}
	}

	go func() {
		defer close(ch)
		collectRVConditionCounts(ch, desc, []v1alpha1.ReplicatedVolume{
			{
				Spec: v1alpha1.ReplicatedVolumeSpec{ReplicatedStorageClassName: "sc-a"},
				Status: v1alpha1.ReplicatedVolumeStatus{Conditions: []metav1.Condition{
					cond(v1alpha1.ReplicatedVolumeCondReadyType, metav1.ConditionTrue, v1alpha1.ReplicatedVolumeCondReadyReasonReady),
					cond(v1alpha1.ReplicatedVolumeCondResilientType, metav1.ConditionTrue, v1alpha1.ReplicatedVolumeCondResilientReasonResilient),
				}},
			},
			{
				Spec: v1alpha1.ReplicatedVolumeSpec{ReplicatedStorageClassName: "sc-a"},
				Status: v1alpha1.ReplicatedVolumeStatus{Conditions: []metav1.Condition{
					cond(v1alpha1.ReplicatedVolumeCondReadyType, metav1.ConditionFalse, v1alpha1.ReplicatedVolumeCondReadyReasonQuorumLost),
					cond(v1alpha1.ReplicatedVolumeCondResilientType, metav1.ConditionTrue, v1alpha1.ReplicatedVolumeCondResilientReasonResilient),
					cond(v1alpha1.ReplicatedVolumeCondUnderMaintenanceType, metav1.ConditionTrue, v1alpha1.ReplicatedVolumeCondUnderMaintenanceReasonNoResourceReconciliation),
				}},
			},
			{
				// Manual volume: no storage class.
				Spec: v1alpha1.ReplicatedVolumeSpec{ConfigurationMode: v1alpha1.ReplicatedVolumeConfigurationModeManual},
				Status: v1alpha1.ReplicatedVolumeStatus{Conditions: []metav1.Condition{
					cond(v1alpha1.ReplicatedVolumeCondReadyType, metav1.ConditionTrue, v1alpha1.ReplicatedVolumeCondReadyReasonReady),
				}},
			},
			{
				// Deleting volume: its frozen conditions must not be counted.
				ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &deleteTime, Finalizers: []string{"test"}},
				Spec:       v1alpha1.ReplicatedVolumeSpec{ReplicatedStorageClassName: "sc-a"},
				Status: v1alpha1.ReplicatedVolumeStatus{Conditions: []metav1.Condition{
					cond(v1alpha1.ReplicatedVolumeCondReadyType, metav1.ConditionFalse, v1alpha1.ReplicatedVolumeCondReadyReasonTerminating),
					cond(v1alpha1.ReplicatedVolumeCondResilientType, metav1.ConditionFalse, v1alpha1.ReplicatedVolumeCondResilientReasonDegraded),
				}},
			},
		})
	}()

	got := make(map[currentMetricLabels4]float64)
	for _, metric := range collectTestMetrics(t, ch) {
		got[currentMetricLabels4{
			first:  metric.labels[LabelStorageClass],
			second: metric.labels[LabelCondition],
			third:  metric.labels[LabelStatus],
			fourth: metric.labels[LabelReason],
		}] = metric.value
	}

	want := map[currentMetricLabels4]float64{
		{"sc-a", v1alpha1.ReplicatedVolumeCondReadyType, "True", v1alpha1.ReplicatedVolumeCondReadyReasonReady}:                                          1,
		{"sc-a", v1alpha1.ReplicatedVolumeCondReadyType, "False", v1alpha1.ReplicatedVolumeCondReadyReasonQuorumLost}:                                    1,
		{"sc-a", v1alpha1.ReplicatedVolumeCondResilientType, "True", v1alpha1.ReplicatedVolumeCondResilientReasonResilient}:                              2,
		{"sc-a", v1alpha1.ReplicatedVolumeCondUnderMaintenanceType, "True", v1alpha1.ReplicatedVolumeCondUnderMaintenanceReasonNoResourceReconciliation}: 1,
		{"sc-a", v1alpha1.ReplicatedVolumeCondUnderMaintenanceType, currentMetricsStatusAbsent, currentMetricsReasonAbsent}:                              1,
		{currentMetricsSCUnknown, v1alpha1.ReplicatedVolumeCondReadyType, "True", v1alpha1.ReplicatedVolumeCondReadyReasonReady}:                         1,
	}
	for _, condType := range currentMetricsRVConditionTypes {
		switch condType {
		case v1alpha1.ReplicatedVolumeCondReadyType:
			continue
		case v1alpha1.ReplicatedVolumeCondResilientType, v1alpha1.ReplicatedVolumeCondUnderMaintenanceType:
		default:
			want[currentMetricLabels4{"sc-a", condType, currentMetricsStatusAbsent, currentMetricsReasonAbsent}] = 2
		}
		want[currentMetricLabels4{currentMetricsSCUnknown, condType, currentMetricsStatusAbsent, currentMetricsReasonAbsent}] = 1
	}

	if !maps.Equal(got, want) {
		t.Fatalf("unexpected condition counts:\ngot  %v\nwant %v", got, want)
	}
}

func TestCollectRVDeletingEmitsOnlyDeletingRVs(t *testing.T) {
	deleteTime := metav1.Unix(100, 0)
	ch := make(chan prometheus.Metric, 100)
	desc := prometheus.NewDesc("test_rv_deleting_timestamp_seconds", "test", []string{LabelName, LabelStorageClass}, nil)

	go func() {
		defer close(ch)
		collectRVDeleting(ch, desc, []v1alpha1.ReplicatedVolume{
			{ObjectMeta: metav1.ObjectMeta{Name: "rv-live"}, Spec: v1alpha1.ReplicatedVolumeSpec{ReplicatedStorageClassName: "sc-a"}},
			{ObjectMeta: metav1.ObjectMeta{Name: "rv-deleting", DeletionTimestamp: &deleteTime, Finalizers: []string{"test"}}},
		})
	}()

	metrics := collectTestMetrics(t, ch)
	if len(metrics) != 1 {
		t.Fatalf("expected one deleting RV, got %d: %#v", len(metrics), metrics)
	}
	assertMetric(t, metrics[0], 100, map[string]string{LabelName: "rv-deleting", LabelStorageClass: currentMetricsSCUnknown})
}

func TestCollectDeletingOldestKeepsTheOldestPerGroup(t *testing.T) {
	ts := func(sec int64) *metav1.Time { t := metav1.Unix(sec, 0); return &t }
	deleting := func(name string, sec int64) metav1.ObjectMeta {
		return metav1.ObjectMeta{Name: name, DeletionTimestamp: ts(sec), Finalizers: []string{"test"}}
	}
	ch := make(chan prometheus.Metric, 100)
	desc := prometheus.NewDesc("test_deleting_oldest_timestamp_seconds", "test", []string{LabelKind, LabelStorageClass, LabelNode}, nil)

	go func() {
		defer close(ch)
		collectDeletingOldest(ch, desc,
			[]v1alpha1.ReplicatedVolume{
				{ObjectMeta: deleting("rv-1", 300), Spec: v1alpha1.ReplicatedVolumeSpec{ReplicatedStorageClassName: "sc-a"}},
				{ObjectMeta: deleting("rv-2", 200), Spec: v1alpha1.ReplicatedVolumeSpec{ReplicatedStorageClassName: "sc-a"}},
				{ObjectMeta: metav1.ObjectMeta{Name: "rv-live"}, Spec: v1alpha1.ReplicatedVolumeSpec{ReplicatedStorageClassName: "sc-a"}},
			},
			[]v1alpha1.ReplicatedVolumeReplica{
				// No RSC label yet: the storage class comes from the parent volume.
				{ObjectMeta: deleting("rv-live-0", 150), Spec: v1alpha1.ReplicatedVolumeReplicaSpec{ReplicatedVolumeName: "rv-live", NodeName: "node-1"}},
				{ObjectMeta: deleting("rv-live-1", 120), Spec: v1alpha1.ReplicatedVolumeReplicaSpec{ReplicatedVolumeName: "rv-live", NodeName: "node-2"}},
				{ObjectMeta: metav1.ObjectMeta{Name: "rv-live-2"}, Spec: v1alpha1.ReplicatedVolumeReplicaSpec{ReplicatedVolumeName: "rv-live", NodeName: "node-1"}},
			},
			[]v1alpha1.ReplicatedVolumeAttachment{
				{
					ObjectMeta: func() metav1.ObjectMeta {
						m := deleting("rva-1", 400)
						m.Labels = map[string]string{v1alpha1.ReplicatedStorageClassLabelKey: "sc-b"}
						return m
					}(),
					Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{ReplicatedVolumeName: "rv-gone", NodeName: "node-1"},
				},
			},
		)
	}()

	metrics := collectTestMetrics(t, ch)
	if len(metrics) != 4 {
		t.Fatalf("expected four groups, got %d: %#v", len(metrics), metrics)
	}
	assertMetric(t, metrics[0], 200, map[string]string{LabelKind: currentMetricsKindRV, LabelStorageClass: "sc-a", LabelNode: currentMetricsNodeGlobal})
	assertMetric(t, metrics[1], 400, map[string]string{LabelKind: currentMetricsKindRVA, LabelStorageClass: "sc-b", LabelNode: "node-1"})
	assertMetric(t, metrics[2], 150, map[string]string{LabelKind: currentMetricsKindRVR, LabelStorageClass: "sc-a", LabelNode: "node-1"})
	assertMetric(t, metrics[3], 120, map[string]string{LabelKind: currentMetricsKindRVR, LabelStorageClass: "sc-a", LabelNode: "node-2"})
}

func TestCollectDatameshTransitionOldestKeepsTheOldestPerGroup(t *testing.T) {
	transition := func(typ v1alpha1.ReplicatedVolumeDatameshTransitionType, replica string, startedAt *metav1.Time) v1alpha1.ReplicatedVolumeDatameshTransition {
		return v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        typ,
			ReplicaName: replica,
			Steps:       []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{Name: "Step", StartedAt: startedAt}},
		}
	}
	at := func(sec int64) *metav1.Time { t := metav1.Unix(sec, 0); return &t }
	ch := make(chan prometheus.Metric, 100)
	desc := prometheus.NewDesc("test_rv_datamesh_transition_oldest_timestamp_seconds", "test", []string{LabelStorageClass, LabelNode, LabelType}, nil)

	go func() {
		defer close(ch)
		collectDatameshTransitionOldest(ch, desc,
			[]v1alpha1.ReplicatedVolume{
				{
					Spec: v1alpha1.ReplicatedVolumeSpec{ReplicatedStorageClassName: "sc-a"},
					Status: v1alpha1.ReplicatedVolumeStatus{DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
						transition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation, "", at(300)),
						transition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach, "rv-1-0", at(500)),
						// Not started yet: no age to report.
						transition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach, "rv-1-0", nil),
					}},
				},
				{
					Spec: v1alpha1.ReplicatedVolumeSpec{ReplicatedStorageClassName: "sc-a"},
					Status: v1alpha1.ReplicatedVolumeStatus{DatameshTransitions: []v1alpha1.ReplicatedVolumeDatameshTransition{
						transition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation, "", at(100)),
					}},
				},
			},
			[]v1alpha1.ReplicatedVolumeReplica{
				{ObjectMeta: metav1.ObjectMeta{Name: "rv-1-0"}, Spec: v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-1"}},
			},
		)
	}()

	metrics := collectTestMetrics(t, ch)
	if len(metrics) != 2 {
		t.Fatalf("expected two groups, got %d: %#v", len(metrics), metrics)
	}
	assertMetric(t, metrics[0], 100, map[string]string{
		LabelStorageClass: "sc-a", LabelNode: currentMetricsNodeGlobal, LabelType: string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation),
	})
	assertMetric(t, metrics[1], 500, map[string]string{
		LabelStorageClass: "sc-a", LabelNode: "node-1", LabelType: string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach),
	})
}

func TestCollectRVRNotReadyOldestKeepsTheOldestPerGroup(t *testing.T) {
	deleteTime := metav1.Unix(50, 0)
	ready := func(status metav1.ConditionStatus, sec int64) []metav1.Condition {
		return []metav1.Condition{{
			Type:               v1alpha1.ReplicatedVolumeReplicaCondReadyType,
			Status:             status,
			Reason:             "Test",
			LastTransitionTime: metav1.Unix(sec, 0),
		}}
	}
	rvr := func(name, node string, phase v1alpha1.ReplicatedVolumeReplicaPhase, conds []metav1.Condition) v1alpha1.ReplicatedVolumeReplica {
		return v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name:              name,
				CreationTimestamp: metav1.Unix(10, 0),
				Labels:            map[string]string{v1alpha1.ReplicatedStorageClassLabelKey: "sc-a"},
			},
			Spec:   v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: node},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{Phase: phase, Conditions: conds},
		}
	}
	deletingRVR := rvr("rvr-deleting", "node-1", v1alpha1.ReplicatedVolumeReplicaPhaseDegraded, ready(metav1.ConditionFalse, 20))
	deletingRVR.DeletionTimestamp = &deleteTime
	deletingRVR.Finalizers = []string{"test"}

	ch := make(chan prometheus.Metric, 100)
	desc := prometheus.NewDesc("test_rvr_not_ready_oldest_timestamp_seconds", "test", []string{LabelStorageClass, LabelNode, LabelPhase}, nil)

	go func() {
		defer close(ch)
		collectRVRNotReadyOldest(ch, desc, nil, []v1alpha1.ReplicatedVolumeReplica{
			rvr("rvr-healthy", "node-1", v1alpha1.ReplicatedVolumeReplicaPhaseHealthy, ready(metav1.ConditionTrue, 5)),
			rvr("rvr-degraded-new", "node-1", v1alpha1.ReplicatedVolumeReplicaPhaseDegraded, ready(metav1.ConditionFalse, 300)),
			rvr("rvr-degraded-old", "node-1", v1alpha1.ReplicatedVolumeReplicaPhaseDegraded, ready(metav1.ConditionFalse, 200)),
			// No Ready condition yet: falls back to creationTimestamp.
			rvr("rvr-pending", "node-2", v1alpha1.ReplicatedVolumeReplicaPhasePending, nil),
			deletingRVR,
		})
	}()

	metrics := collectTestMetrics(t, ch)
	if len(metrics) != 2 {
		t.Fatalf("expected two groups, got %d: %#v", len(metrics), metrics)
	}
	assertMetric(t, metrics[0], 200, map[string]string{
		LabelStorageClass: "sc-a", LabelNode: "node-1", LabelPhase: string(v1alpha1.ReplicatedVolumeReplicaPhaseDegraded),
	})
	assertMetric(t, metrics[1], 10, map[string]string{
		LabelStorageClass: "sc-a", LabelNode: "node-2", LabelPhase: string(v1alpha1.ReplicatedVolumeReplicaPhasePending),
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
		collectRVRCounts(ch, desc, []v1alpha1.ReplicatedVolume{
			{ObjectMeta: metav1.ObjectMeta{Name: "rv-auto"}, Spec: v1alpha1.ReplicatedVolumeSpec{ReplicatedStorageClassName: "sc-b"}},
			{ObjectMeta: metav1.ObjectMeta{Name: "rv-manual"}, Spec: v1alpha1.ReplicatedVolumeSpec{ConfigurationMode: v1alpha1.ReplicatedVolumeConfigurationModeManual}},
		}, []v1alpha1.ReplicatedVolumeReplica{
			{
				// No RSC label yet: falls back to the parent volume's storage class.
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-a", ReplicatedVolumeName: "rv-auto"},
				Status: v1alpha1.ReplicatedVolumeReplicaStatus{
					Phase: v1alpha1.ReplicatedVolumeReplicaPhasePending,
				},
			},
			{
				// Replica of a Manual volume: no storage class anywhere.
				Spec: v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-a", ReplicatedVolumeName: "rv-manual"},
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
	if len(metrics) != 3 {
		t.Fatalf("expected three non-zero metrics, got %d: %#v", len(metrics), metrics)
	}
	assertMetric(t, metrics[0], 2, map[string]string{
		LabelNode:         "node-a",
		LabelStorageClass: "sc-a",
		LabelPhase:        string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy),
	})
	assertMetric(t, metrics[1], 1, map[string]string{
		LabelNode:         "node-a",
		LabelStorageClass: "sc-b",
		LabelPhase:        string(v1alpha1.ReplicatedVolumeReplicaPhasePending),
	})
	assertMetric(t, metrics[2], 1, map[string]string{
		LabelNode:         "node-a",
		LabelStorageClass: currentMetricsSCUnknown,
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
		collectRVRDeletingCounts(ch, desc, []v1alpha1.ReplicatedVolume{
			{ObjectMeta: metav1.ObjectMeta{Name: "rv-auto"}, Spec: v1alpha1.ReplicatedVolumeSpec{ReplicatedStorageClassName: "sc-b"}},
		}, []v1alpha1.ReplicatedVolumeReplica{
			{
				// Deleting before the RSC label was set: falls back to the parent volume's storage class.
				ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &deleteTime, Finalizers: []string{"test"}},
				Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-a", ReplicatedVolumeName: "rv-auto"},
			},
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
	if len(metrics) != 2 {
		t.Fatalf("expected two deleting metrics, got %d: %#v", len(metrics), metrics)
	}
	assertMetric(t, metrics[0], 1, map[string]string{
		LabelNode:         "node-a",
		LabelStorageClass: "sc-a",
	})
	assertMetric(t, metrics[1], 1, map[string]string{
		LabelNode:         "node-a",
		LabelStorageClass: "sc-b",
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

func TestCollectRVConditionProblemsEmitsOnlyProblemConditions(t *testing.T) {
	deleteTime := metav1.Now()
	ch := make(chan prometheus.Metric, 100)
	desc := prometheus.NewDesc(
		"test_rv_condition_problem_timestamp_seconds",
		"test",
		[]string{LabelName, LabelStorageClass, LabelCondition, LabelStatus, LabelReason},
		nil,
	)

	cond := func(condType string, status metav1.ConditionStatus, reason string, since int64) metav1.Condition {
		return metav1.Condition{
			Type:               condType,
			Status:             status,
			Reason:             reason,
			LastTransitionTime: metav1.Unix(since, 0),
		}
	}

	go func() {
		defer close(ch)
		collectRVConditionProblems(ch, desc, []v1alpha1.ReplicatedVolume{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1-healthy"},
				Spec:       v1alpha1.ReplicatedVolumeSpec{ReplicatedStorageClassName: "sc-a"},
				Status: v1alpha1.ReplicatedVolumeStatus{Conditions: []metav1.Condition{
					cond(v1alpha1.ReplicatedVolumeCondReadyType, metav1.ConditionTrue, v1alpha1.ReplicatedVolumeCondReadyReasonReady, 100),
					cond(v1alpha1.ReplicatedVolumeCondResilientType, metav1.ConditionTrue, v1alpha1.ReplicatedVolumeCondResilientReasonResilient, 100),
				}},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-2-degraded"},
				Spec:       v1alpha1.ReplicatedVolumeSpec{ReplicatedStorageClassName: "sc-a"},
				Status: v1alpha1.ReplicatedVolumeStatus{Conditions: []metav1.Condition{
					cond(v1alpha1.ReplicatedVolumeCondReadyType, metav1.ConditionTrue, v1alpha1.ReplicatedVolumeCondReadyReasonReady, 100),
					cond(v1alpha1.ReplicatedVolumeCondResilientType, metav1.ConditionFalse, v1alpha1.ReplicatedVolumeCondResilientReasonDegraded, 200),
					cond(v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType, metav1.ConditionFalse, v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonCannotConverge, 300),
					// Not in the per-volume set: covered by the aggregate only.
					cond(v1alpha1.ReplicatedVolumeCondConfiguredType, metav1.ConditionFalse, v1alpha1.ReplicatedVolumeCondConfiguredReasonConfigurationInProgress, 300),
					cond(v1alpha1.ReplicatedVolumeCondScheduledType, metav1.ConditionFalse, v1alpha1.ReplicatedVolumeCondScheduledReasonReplicasNotScheduled, 300),
				}},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-3-unknown"},
				Spec:       v1alpha1.ReplicatedVolumeSpec{ReplicatedStorageClassName: "sc-b"},
				Status: v1alpha1.ReplicatedVolumeStatus{Conditions: []metav1.Condition{
					cond(v1alpha1.ReplicatedVolumeCondReadyType, metav1.ConditionUnknown, v1alpha1.ReplicatedVolumeCondReadyReasonStatusUnknown, 400),
				}},
			},
			{
				// Manual volume under maintenance: presence of UnderMaintenance is the problem.
				ObjectMeta: metav1.ObjectMeta{Name: "rv-4-maintenance"},
				Spec:       v1alpha1.ReplicatedVolumeSpec{ConfigurationMode: v1alpha1.ReplicatedVolumeConfigurationModeManual},
				Status: v1alpha1.ReplicatedVolumeStatus{Conditions: []metav1.Condition{
					cond(v1alpha1.ReplicatedVolumeCondReadyType, metav1.ConditionTrue, v1alpha1.ReplicatedVolumeCondReadyReasonReady, 100),
					cond(v1alpha1.ReplicatedVolumeCondUnderMaintenanceType, metav1.ConditionTrue, v1alpha1.ReplicatedVolumeCondUnderMaintenanceReasonNoResourceReconciliation, 500),
				}},
			},
			{
				// No status published yet: reported once, as Ready Absent since creation.
				ObjectMeta: metav1.ObjectMeta{Name: "rv-5-no-status", CreationTimestamp: metav1.Unix(550, 0)},
				Spec:       v1alpha1.ReplicatedVolumeSpec{ReplicatedStorageClassName: "sc-a"},
			},
			{
				// Deleting volume: its frozen conditions must not be reported.
				ObjectMeta: metav1.ObjectMeta{Name: "rv-6-deleting", DeletionTimestamp: &deleteTime, Finalizers: []string{"test"}},
				Spec:       v1alpha1.ReplicatedVolumeSpec{ReplicatedStorageClassName: "sc-a"},
				Status: v1alpha1.ReplicatedVolumeStatus{Conditions: []metav1.Condition{
					cond(v1alpha1.ReplicatedVolumeCondReadyType, metav1.ConditionFalse, v1alpha1.ReplicatedVolumeCondReadyReasonTerminating, 600),
					cond(v1alpha1.ReplicatedVolumeCondResilientType, metav1.ConditionFalse, v1alpha1.ReplicatedVolumeCondResilientReasonDegraded, 600),
				}},
			},
			{
				// Deleting volume without status: not reported either.
				ObjectMeta: metav1.ObjectMeta{Name: "rv-7-deleting-no-status", DeletionTimestamp: &deleteTime, Finalizers: []string{"test"}},
				Spec:       v1alpha1.ReplicatedVolumeSpec{ReplicatedStorageClassName: "sc-a"},
			},
		})
	}()

	metrics := collectTestMetrics(t, ch)
	if len(metrics) != 5 {
		t.Fatalf("expected five problem series, got %d: %#v", len(metrics), metrics)
	}
	assertMetric(t, metrics[0], 200, map[string]string{
		LabelName:         "rv-2-degraded",
		LabelStorageClass: "sc-a",
		LabelCondition:    v1alpha1.ReplicatedVolumeCondResilientType,
		LabelStatus:       "False",
		LabelReason:       v1alpha1.ReplicatedVolumeCondResilientReasonDegraded,
	})
	assertMetric(t, metrics[1], 300, map[string]string{
		LabelName:         "rv-2-degraded",
		LabelStorageClass: "sc-a",
		LabelCondition:    v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType,
		LabelStatus:       "False",
		LabelReason:       v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonCannotConverge,
	})
	assertMetric(t, metrics[2], 400, map[string]string{
		LabelName:         "rv-3-unknown",
		LabelStorageClass: "sc-b",
		LabelCondition:    v1alpha1.ReplicatedVolumeCondReadyType,
		LabelStatus:       "Unknown",
		LabelReason:       v1alpha1.ReplicatedVolumeCondReadyReasonStatusUnknown,
	})
	assertMetric(t, metrics[3], 500, map[string]string{
		LabelName:         "rv-4-maintenance",
		LabelStorageClass: currentMetricsSCUnknown,
		LabelCondition:    v1alpha1.ReplicatedVolumeCondUnderMaintenanceType,
		LabelStatus:       "True",
		LabelReason:       v1alpha1.ReplicatedVolumeCondUnderMaintenanceReasonNoResourceReconciliation,
	})
	assertMetric(t, metrics[4], 550, map[string]string{
		LabelName:         "rv-5-no-status",
		LabelStorageClass: "sc-a",
		LabelCondition:    v1alpha1.ReplicatedVolumeCondReadyType,
		LabelStatus:       currentMetricsStatusAbsent,
		LabelReason:       currentMetricsReasonAbsent,
	})
}

func TestCollectRVConditionProblemsFollowsTheCacheSnapshot(t *testing.T) {
	desc := prometheus.NewDesc(
		"test_rv_condition_problem_timestamp_seconds",
		"test",
		[]string{LabelName, LabelStorageClass, LabelCondition, LabelStatus, LabelReason},
		nil,
	)

	collect := func(rvs []v1alpha1.ReplicatedVolume) []testMetric {
		t.Helper()

		ch := make(chan prometheus.Metric, 100)
		go func() {
			defer close(ch)
			collectRVConditionProblems(ch, desc, rvs)
		}()
		return collectTestMetrics(t, ch)
	}

	rvWithLayout := func(status metav1.ConditionStatus, reason string, since int64) []v1alpha1.ReplicatedVolume {
		return []v1alpha1.ReplicatedVolume{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-a"},
				Spec:       v1alpha1.ReplicatedVolumeSpec{ReplicatedStorageClassName: "sc-a"},
				Status: v1alpha1.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						// Ready is present so that only the layout condition is under test.
						{
							Type:   v1alpha1.ReplicatedVolumeCondReadyType,
							Status: metav1.ConditionTrue,
							Reason: v1alpha1.ReplicatedVolumeCondReadyReasonReady,
						},
						{
							Type:               v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType,
							Status:             status,
							Reason:             reason,
							LastTransitionTime: metav1.Unix(since, 0),
						},
					},
				},
			},
		}
	}
	layoutLabels := func(reason string) map[string]string {
		return map[string]string{
			LabelName:      "rv-a",
			LabelCondition: v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType,
			LabelStatus:    "False",
			LabelReason:    reason,
		}
	}

	metrics := collect(rvWithLayout(metav1.ConditionFalse, v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverging, 100))
	if len(metrics) != 1 {
		t.Fatalf("expected one series, got %d: %#v", len(metrics), metrics)
	}
	assertMetric(t, metrics[0], 100, layoutLabels(v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverging))

	// A reason change within the same status replaces the series instead of adding a second one,
	// and keeps the transition time: the collector rebuilds every series from the cache on each
	// scrape, and lastTransitionTime moves only on a status change.
	metrics = collect(rvWithLayout(metav1.ConditionFalse, v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonTransitionUnsupported, 100))
	if len(metrics) != 1 {
		t.Fatalf("expected the series to be replaced, got %d: %#v", len(metrics), metrics)
	}
	assertMetric(t, metrics[0], 100, layoutLabels(v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonTransitionUnsupported))

	// A recovered condition stops being emitted, which is what resolves the alert.
	if metrics = collect(rvWithLayout(metav1.ConditionTrue, v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverged, 200)); len(metrics) != 0 {
		t.Fatalf("expected no series for a converged RV, got %d: %#v", len(metrics), metrics)
	}

	// A deleted RV is simply absent from the reader, so its series disappears without any
	// explicit metric removal.
	if metrics = collect(nil); len(metrics) != 0 {
		t.Fatalf("expected no series for a deleted RV, got %d: %#v", len(metrics), metrics)
	}
}

// replicatedVolumeLayoutRulePath points at the static Prometheus rule file shipped with the module.
// It lives at the repository root, outside every Go module, so it cannot be embedded with go:embed;
// the test reads it at runtime (the sanctioned exception in go-tests.mdc).
const replicatedVolumeLayoutRulePath = "../../../../monitoring/prometheus-rules/replicated-volume-layout.yaml"

type prometheusRuleGroup struct {
	Rules []prometheusAlertRule `json:"rules"`
}

type prometheusAlertRule struct {
	Alert         string            `json:"alert"`
	Expr          string            `json:"expr"`
	For           string            `json:"for"`
	KeepFiringFor string            `json:"keep_firing_for"`
	Labels        map[string]string `json:"labels"`
}

func TestReplicatedVolumeLayoutRuleMatchesTheExportedMetric(t *testing.T) {
	data, err := os.ReadFile(replicatedVolumeLayoutRulePath)
	if err != nil {
		t.Fatalf("reading rule file %q: %v", replicatedVolumeLayoutRulePath, err)
	}

	// The file is deliberately a static .yaml (no newControlPlane gate, not rendered by Helm), so
	// Prometheus templates are written plainly and must not be Helm-escaped or Helm-gated.
	for _, helmMarker := range []string{`{{ "`, "{{-", "{{ if", "{{ end"} {
		if strings.Contains(string(data), helmMarker) {
			t.Fatalf("rule file %q must contain no Helm templating, found %q", replicatedVolumeLayoutRulePath, helmMarker)
		}
	}

	var groups []prometheusRuleGroup
	if err := yaml.Unmarshal(data, &groups); err != nil {
		t.Fatalf("unmarshalling rule file %q: %v", replicatedVolumeLayoutRulePath, err)
	}

	var alert *prometheusAlertRule
	for i := range groups {
		for j := range groups[i].Rules {
			if groups[i].Rules[j].Alert == "D8ReplicatedVolumeLayoutDegraded" {
				alert = &groups[i].Rules[j]
			}
		}
	}
	if alert == nil {
		t.Fatalf("alert D8ReplicatedVolumeLayoutDegraded not found in %q", replicatedVolumeLayoutRulePath)
	}

	if alert.For != "15m" {
		t.Fatalf("expected for: 15m, got %q", alert.For)
	}
	// Bridges the short gap in current metrics on a controller restart or a leader change.
	if alert.KeepFiringFor != "5m" {
		t.Fatalf("expected keep_firing_for: 5m, got %q", alert.KeepFiringFor)
	}
	if alert.Labels["severity_level"] != "6" {
		t.Fatalf("expected severity_level 6, got %q", alert.Labels["severity_level"])
	}
	if alert.Labels["tier"] != "cluster" {
		t.Fatalf("expected tier cluster, got %q", alert.Labels["tier"])
	}

	if !strings.Contains(alert.Expr, metricNameRVConditionProblem) {
		t.Fatalf("expected expr to select %q, got %q", metricNameRVConditionProblem, alert.Expr)
	}
	condSelector := `condition="` + v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType + `"`
	if !strings.Contains(alert.Expr, condSelector) {
		t.Fatalf("expected expr to select %s, got %q", condSelector, alert.Expr)
	}
	// A volume under maintenance has its layout convergence frozen and must not fire.
	maintenance := "unless on (name)\n" + metricNameRVConditionProblem +
		`{condition="` + v1alpha1.ReplicatedVolumeCondUnderMaintenanceType + `"}`
	if !strings.Contains(alert.Expr, maintenance) {
		t.Fatalf("expected expr to exclude volumes under maintenance with %q, got %q", maintenance, alert.Expr)
	}
	// Only the two verdict reasons are alerted on: transient and deletion states must not fire.
	for _, reason := range []string{
		v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonTransitionUnsupported,
		v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonCannotConverge,
	} {
		if !strings.Contains(alert.Expr, reason) {
			t.Fatalf("expected expr to alert on reason %q, got %q", reason, alert.Expr)
		}
	}
	for _, reason := range []string{
		v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverging,
		v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonVolumeDeleting,
		currentMetricsReasonAbsent,
	} {
		if strings.Contains(alert.Expr, reason) {
			t.Fatalf("expr must not alert on reason %q, got %q", reason, alert.Expr)
		}
	}
}

// replicatedVolumeHealthRulePath points at the static Prometheus rule file with the Ready and
// Resilient alerts (read at runtime for the same reason as replicatedVolumeLayoutRulePath).
const replicatedVolumeHealthRulePath = "../../../../monitoring/prometheus-rules/replicated-volume-health.yaml"

func TestReplicatedVolumeHealthRulesMatchTheExportedMetric(t *testing.T) {
	data, err := os.ReadFile(replicatedVolumeHealthRulePath)
	if err != nil {
		t.Fatalf("reading rule file %q: %v", replicatedVolumeHealthRulePath, err)
	}

	// Static .yaml like the layout rule: Prometheus templates are written plainly, no Helm gate.
	for _, helmMarker := range []string{`{{ "`, "{{-", "{{ if", "{{ end"} {
		if strings.Contains(string(data), helmMarker) {
			t.Fatalf("rule file %q must contain no Helm templating, found %q", replicatedVolumeHealthRulePath, helmMarker)
		}
	}

	var groups []prometheusRuleGroup
	if err := yaml.Unmarshal(data, &groups); err != nil {
		t.Fatalf("unmarshalling rule file %q: %v", replicatedVolumeHealthRulePath, err)
	}
	alerts := map[string]prometheusAlertRule{}
	for _, g := range groups {
		for _, r := range g.Rules {
			alerts[r.Alert] = r
		}
	}

	// Formation is not a health problem of these alerts: Forming is the reason while the volume is
	// being created, Absent is synthesized by the collector for a volume with no Ready condition yet.
	formingSelector := `reason!~"` + v1alpha1.ReplicatedVolumeCondReadyReasonForming + "|" + currentMetricsReasonAbsent + `"`
	readySelector := metricNameRVConditionProblem + `{condition="` + v1alpha1.ReplicatedVolumeCondReadyType + `",` + formingSelector + `}`
	resilientSelector := metricNameRVConditionProblem + `{condition="` + v1alpha1.ReplicatedVolumeCondResilientType + `",` + formingSelector + `}`
	// Both conditions share the reason names the alerts exclude.
	if v1alpha1.ReplicatedVolumeCondResilientReasonForming != v1alpha1.ReplicatedVolumeCondReadyReasonForming {
		t.Fatalf("Ready and Resilient must share the Forming reason, got %q and %q",
			v1alpha1.ReplicatedVolumeCondReadyReasonForming, v1alpha1.ReplicatedVolumeCondResilientReasonForming)
	}

	tests := []struct {
		alert    string
		expr     string
		for_     string
		severity string
	}{
		{
			alert:    "D8ReplicatedVolumeNotReady",
			expr:     "max by (name, storage_class) (" + readySelector + ")",
			for_:     "5m",
			severity: "4",
		},
		{
			// A volume that is not Ready is reported only by the NotReady alert.
			alert:    "D8ReplicatedVolumeNotResilient",
			expr:     "max by (name, storage_class) (" + resilientSelector + ") unless on (name) " + readySelector,
			for_:     "30m",
			severity: "6",
		},
	}
	for _, tt := range tests {
		t.Run(tt.alert, func(t *testing.T) {
			alert, ok := alerts[tt.alert]
			if !ok {
				t.Fatalf("alert %s not found in %q", tt.alert, replicatedVolumeHealthRulePath)
			}
			// The multi-line block scalar is compared with its whitespace collapsed.
			if got := strings.Join(strings.Fields(alert.Expr), " "); got != tt.expr {
				t.Fatalf("expected expr %q, got %q", tt.expr, got)
			}
			if alert.For != tt.for_ {
				t.Fatalf("expected for: %s, got %q", tt.for_, alert.For)
			}
			// Bridges the short gap in current metrics on a controller restart or a leader change.
			if alert.KeepFiringFor != "5m" {
				t.Fatalf("expected keep_firing_for: 5m, got %q", alert.KeepFiringFor)
			}
			if alert.Labels["severity_level"] != tt.severity {
				t.Fatalf("expected severity_level %s, got %q", tt.severity, alert.Labels["severity_level"])
			}
			if alert.Labels["tier"] != "cluster" {
				t.Fatalf("expected tier cluster, got %q", alert.Labels["tier"])
			}
		})
	}
}

// currentMetricsRulePath points at the Helm-templated Prometheus rule file shipped with the module
// (read at runtime for the same reason as replicatedVolumeLayoutRulePath).
const currentMetricsRulePath = "../../../../monitoring/prometheus-rules/current-metrics.tpl"

func TestCurrentMetricsUnavailableRuleMatchesTheExportedMetric(t *testing.T) {
	data, err := os.ReadFile(currentMetricsRulePath)
	if err != nil {
		t.Fatalf("reading rule file %q: %v", currentMetricsRulePath, err)
	}

	// absent() fires wherever the series is missing, including the old control plane that has
	// no such controller, so the rule must be gated on the new control plane.
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	const gate = "{{- if .Values.sdsReplicatedVolume.internal.newControlPlane }}"
	if lines[0] != gate || lines[len(lines)-1] != "{{- end }}" {
		t.Fatalf("rule file %q must be wrapped in %q ... {{- end }}", currentMetricsRulePath, gate)
	}

	var groups []prometheusRuleGroup
	if err := yaml.Unmarshal([]byte(strings.Join(lines[1:len(lines)-1], "\n")), &groups); err != nil {
		t.Fatalf("unmarshalling rule file %q: %v", currentMetricsRulePath, err)
	}
	var alert *prometheusAlertRule
	for i := range groups {
		for j := range groups[i].Rules {
			if groups[i].Rules[j].Alert == "D8SdsReplicatedVolumeCurrentMetricsUnavailable" {
				alert = &groups[i].Rules[j]
			}
		}
	}
	if alert == nil {
		t.Fatalf("alert D8SdsReplicatedVolumeCurrentMetricsUnavailable not found in %q", currentMetricsRulePath)
	}

	if want := "absent(" + metricNameCollectorUp + " == 1)"; alert.Expr != want {
		t.Fatalf("expected expr %q, got %q", want, alert.Expr)
	}
	// Equal to keep_firing_for of the alerts built on current metrics, so that it takes over
	// as they resolve when the metrics are gone.
	if alert.For != "5m" {
		t.Fatalf("expected for: 5m, got %q", alert.For)
	}
	// Equal to the for of D8ReplicatedVolumeNotReady, so that it keeps firing while that
	// alert is pending again after the metrics return.
	if alert.KeepFiringFor != "5m" {
		t.Fatalf("expected keep_firing_for: 5m, got %q", alert.KeepFiringFor)
	}
	if alert.Labels["severity_level"] != "6" || alert.Labels["tier"] != "cluster" {
		t.Fatalf("expected severity_level 6 and tier cluster, got %v", alert.Labels)
	}
}

// listStubReader serves empty lists, or fails every List call when err is set.
type listStubReader struct {
	client.Reader
	err error
}

func (r listStubReader) List(context.Context, client.ObjectList, ...client.ListOption) error {
	return r.err
}

func TestCurrentMetricsCollectorReportsWhetherCollectionSucceeded(t *testing.T) {
	collectUp := func(reader client.Reader) (up []float64, others int) {
		ch := make(chan prometheus.Metric, 64)
		newCurrentMetricsCollector(reader, logr.Discard()).Collect(ch)
		close(ch)
		for metric := range ch {
			if !strings.Contains(metric.Desc().String(), `"`+metricNameCollectorUp+`"`) {
				others++
				continue
			}
			var m dto.Metric
			if err := metric.Write(&m); err != nil {
				t.Fatal(err)
			}
			up = append(up, m.GetGauge().GetValue())
		}
		return up, others
	}

	// An empty cluster has no metrics of its objects, but the collector did succeed and reports
	// the zero object counts of every kind.
	if up, others := collectUp(listStubReader{}); !slices.Equal(up, []float64{1}) || others != 6 {
		t.Fatalf("expected %s=1 and 6 object counts on success, got %v and %d other metrics", metricNameCollectorUp, up, others)
	}
	up, others := collectUp(listStubReader{err: errors.New("cache is not synced")})
	if !slices.Equal(up, []float64{0}) || others != 0 {
		t.Fatalf("expected only %s=0 on failure, got %v and %d other metrics", metricNameCollectorUp, up, others)
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
