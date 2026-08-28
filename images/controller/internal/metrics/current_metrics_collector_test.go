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
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

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

func TestCollectRVLayoutConvergedEmitsOneSeriesPerRV(t *testing.T) {
	ch := make(chan prometheus.Metric, 100)
	desc := prometheus.NewDesc(
		"test_rv_membership_layout_converged",
		"test",
		[]string{LabelName, LabelReason},
		nil,
	)

	layoutCond := func(status metav1.ConditionStatus, reason string) metav1.Condition {
		return metav1.Condition{
			Type:   v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType,
			Status: status,
			Reason: reason,
		}
	}

	go func() {
		defer close(ch)
		collectRVLayoutConverged(ch, desc, []v1alpha1.ReplicatedVolume{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1-converged"},
				Status: v1alpha1.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						// An unrelated condition with its own reason: the reason label must come
						// from MembershipLayoutConverged, not from whatever comes first.
						{
							Type:   v1alpha1.ReplicatedVolumeCondReadyType,
							Status: metav1.ConditionFalse,
							Reason: "SomeUnrelatedReason",
						},
						layoutCond(metav1.ConditionTrue, v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverged),
					},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-2-converging"},
				Status: v1alpha1.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						layoutCond(metav1.ConditionFalse, v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverging),
					},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-3-cannot-converge"},
				Status: v1alpha1.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						layoutCond(metav1.ConditionFalse, v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonCannotConverge),
					},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-4-unsupported"},
				Status: v1alpha1.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						layoutCond(metav1.ConditionFalse, v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonTransitionUnsupported),
					},
				},
			},
			// A deleting RV keeps its series: value 0 with the reason recorded in the status,
			// never the synthetic absent-condition reason.
			{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-5-deleting"},
				Status: v1alpha1.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						layoutCond(metav1.ConditionUnknown, v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonVolumeDeleting),
					},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-6-no-condition"},
			},
			// Reason Converged without status True (never written by the controller) must not be
			// reported as converged: the value follows status AND reason together.
			{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-7-converged-not-true"},
				Status: v1alpha1.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						layoutCond(metav1.ConditionUnknown, v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverged),
					},
				},
			},
			// The mirror case of rv-7: status True with a reason other than Converged (also never
			// written by the controller) must not be reported as converged either. Both directions
			// are needed to pin the convention "value 1 iff status True AND reason Converged".
			{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-8-true-not-converged"},
				Status: v1alpha1.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						layoutCond(metav1.ConditionTrue, v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverging),
					},
				},
			},
		})
	}()

	metrics := collectTestMetrics(t, ch)
	if len(metrics) != 8 {
		t.Fatalf("expected one series per RV, got %d: %#v", len(metrics), metrics)
	}
	// Series are emitted sorted by RV name.
	assertMetric(t, metrics[0], 1, map[string]string{
		LabelName:   "rv-1-converged",
		LabelReason: v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverged,
	})
	assertMetric(t, metrics[1], 0, map[string]string{
		LabelName:   "rv-2-converging",
		LabelReason: v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverging,
	})
	assertMetric(t, metrics[2], 0, map[string]string{
		LabelName:   "rv-3-cannot-converge",
		LabelReason: v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonCannotConverge,
	})
	assertMetric(t, metrics[3], 0, map[string]string{
		LabelName:   "rv-4-unsupported",
		LabelReason: v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonTransitionUnsupported,
	})
	assertMetric(t, metrics[4], 0, map[string]string{
		LabelName:   "rv-5-deleting",
		LabelReason: v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonVolumeDeleting,
	})
	assertMetric(t, metrics[5], 0, map[string]string{
		LabelName:   "rv-6-no-condition",
		LabelReason: currentMetricsReasonAbsent,
	})
	assertMetric(t, metrics[6], 0, map[string]string{
		LabelName:   "rv-7-converged-not-true",
		LabelReason: v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverged,
	})
	assertMetric(t, metrics[7], 0, map[string]string{
		LabelName:   "rv-8-true-not-converged",
		LabelReason: v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverging,
	})
}

func TestCollectRVLayoutConvergedFollowsTheCacheSnapshot(t *testing.T) {
	desc := prometheus.NewDesc(
		"test_rv_membership_layout_converged",
		"test",
		[]string{LabelName, LabelReason},
		nil,
	)

	collect := func(rvs []v1alpha1.ReplicatedVolume) []testMetric {
		t.Helper()

		ch := make(chan prometheus.Metric, 100)
		go func() {
			defer close(ch)
			collectRVLayoutConverged(ch, desc, rvs)
		}()
		return collectTestMetrics(t, ch)
	}

	rvWithReason := func(status metav1.ConditionStatus, reason string) []v1alpha1.ReplicatedVolume {
		return []v1alpha1.ReplicatedVolume{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-a"},
				Status: v1alpha1.ReplicatedVolumeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType,
							Status: status,
							Reason: reason,
						},
					},
				},
			},
		}
	}

	metrics := collect(rvWithReason(metav1.ConditionTrue, v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverged))
	if len(metrics) != 1 {
		t.Fatalf("expected one series, got %d: %#v", len(metrics), metrics)
	}
	assertMetric(t, metrics[0], 1, map[string]string{
		LabelName:   "rv-a",
		LabelReason: v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverged,
	})

	// A reason change replaces the series instead of adding a second one: the collector rebuilds
	// every series from the cache on each scrape.
	metrics = collect(rvWithReason(metav1.ConditionFalse, v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonTransitionUnsupported))
	if len(metrics) != 1 {
		t.Fatalf("expected the series to be replaced, got %d: %#v", len(metrics), metrics)
	}
	assertMetric(t, metrics[0], 0, map[string]string{
		LabelName:   "rv-a",
		LabelReason: v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonTransitionUnsupported,
	})

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
	Alert  string            `json:"alert"`
	Expr   string            `json:"expr"`
	For    string            `json:"for"`
	Labels map[string]string `json:"labels"`
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
	if alert.Labels["severity_level"] != "6" {
		t.Fatalf("expected severity_level 6, got %q", alert.Labels["severity_level"])
	}
	if alert.Labels["tier"] != "cluster" {
		t.Fatalf("expected tier cluster, got %q", alert.Labels["tier"])
	}

	if !strings.Contains(alert.Expr, metricNameRVLayoutConverged) {
		t.Fatalf("expected expr to select %q, got %q", metricNameRVLayoutConverged, alert.Expr)
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
