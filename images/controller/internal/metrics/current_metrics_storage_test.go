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
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

func readyCondition(status metav1.ConditionStatus, reason string, since int64) []metav1.Condition {
	return []metav1.Condition{{
		Type:               "Ready",
		Status:             status,
		Reason:             reason,
		LastTransitionTime: metav1.Unix(since, 0),
	}}
}

func TestCollectRSPConditionProblemsEmitsOnlyPoolsThatAreNotReady(t *testing.T) {
	deleteTime := metav1.Now()
	desc := prometheus.NewDesc("test_rsp_condition_problem_timestamp_seconds", "test",
		[]string{LabelName, LabelCondition, LabelStatus, LabelReason}, nil)

	ch := make(chan prometheus.Metric, 10)
	go func() {
		defer close(ch)
		collectRSPConditionProblems(ch, desc, []v1alpha1.ReplicatedStoragePool{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-1-ready"},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					Conditions: readyCondition(metav1.ConditionTrue, v1alpha1.ReplicatedStoragePoolCondReadyReasonReady, 100),
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-2-lvg-missing"},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					Conditions: readyCondition(metav1.ConditionFalse, v1alpha1.ReplicatedStoragePoolCondReadyReasonLVMVolumeGroupNotFound, 200),
				},
			},
			{
				// No status published yet: reported as Absent since creation.
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-3-no-status", CreationTimestamp: metav1.Unix(300, 0)},
			},
			{
				// Deleting pool: not reported.
				ObjectMeta: metav1.ObjectMeta{Name: "rsp-4-deleting", DeletionTimestamp: &deleteTime, Finalizers: []string{"test"}},
				Status: v1alpha1.ReplicatedStoragePoolStatus{
					Conditions: readyCondition(metav1.ConditionFalse, v1alpha1.ReplicatedStoragePoolCondReadyReasonInvalidLVMVolumeGroup, 400),
				},
			},
		})
	}()

	metrics := collectTestMetrics(t, ch)
	if len(metrics) != 2 {
		t.Fatalf("expected two problem series, got %d: %#v", len(metrics), metrics)
	}
	assertMetric(t, metrics[0], 200, map[string]string{
		LabelName:      "rsp-2-lvg-missing",
		LabelCondition: v1alpha1.ReplicatedStoragePoolCondReadyType,
		LabelStatus:    "False",
		LabelReason:    v1alpha1.ReplicatedStoragePoolCondReadyReasonLVMVolumeGroupNotFound,
	})
	assertMetric(t, metrics[1], 300, map[string]string{
		LabelName:      "rsp-3-no-status",
		LabelCondition: v1alpha1.ReplicatedStoragePoolCondReadyType,
		LabelStatus:    currentMetricsStatusAbsent,
		LabelReason:    currentMetricsReasonAbsent,
	})
}

func TestCollectRSCConditionProblemsCarriesTheStoragePool(t *testing.T) {
	deleteTime := metav1.Now()
	desc := prometheus.NewDesc("test_rsc_condition_problem_timestamp_seconds", "test",
		[]string{LabelName, LabelStoragePool, LabelCondition, LabelStatus, LabelReason}, nil)

	ch := make(chan prometheus.Metric, 10)
	go func() {
		defer close(ch)
		collectRSCConditionProblems(ch, desc, []v1alpha1.ReplicatedStorageClass{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-1-ready"},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					StoragePoolName: "rsp-a",
					Conditions:      readyCondition(metav1.ConditionTrue, v1alpha1.ReplicatedStorageClassCondReadyReasonReady, 100),
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-2-degraded"},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					StoragePoolName: "rsp-a",
					Conditions:      readyCondition(metav1.ConditionFalse, v1alpha1.ReplicatedStorageClassCondReadyReasonDegraded, 200),
				},
			},
			{
				// An invalid spec may leave the class without a storage pool.
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-3-invalid"},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					Conditions: readyCondition(metav1.ConditionFalse, v1alpha1.ReplicatedStorageClassCondReadyReasonInvalidConfiguration, 300),
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-4-no-status", CreationTimestamp: metav1.Unix(400, 0)},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "rsc-5-deleting", DeletionTimestamp: &deleteTime, Finalizers: []string{"test"}},
				Status: v1alpha1.ReplicatedStorageClassStatus{
					StoragePoolName: "rsp-a",
					Conditions:      readyCondition(metav1.ConditionFalse, v1alpha1.ReplicatedStorageClassCondReadyReasonDegraded, 500),
				},
			},
		})
	}()

	metrics := collectTestMetrics(t, ch)
	if len(metrics) != 3 {
		t.Fatalf("expected three problem series, got %d: %#v", len(metrics), metrics)
	}
	assertMetric(t, metrics[0], 200, map[string]string{
		LabelName:        "rsc-2-degraded",
		LabelStoragePool: "rsp-a",
		LabelCondition:   v1alpha1.ReplicatedStorageClassCondReadyType,
		LabelStatus:      "False",
		LabelReason:      v1alpha1.ReplicatedStorageClassCondReadyReasonDegraded,
	})
	assertMetric(t, metrics[1], 300, map[string]string{
		LabelName:        "rsc-3-invalid",
		LabelStoragePool: "",
		LabelReason:      v1alpha1.ReplicatedStorageClassCondReadyReasonInvalidConfiguration,
	})
	assertMetric(t, metrics[2], 400, map[string]string{
		LabelName:   "rsc-4-no-status",
		LabelStatus: currentMetricsStatusAbsent,
		LabelReason: currentMetricsReasonAbsent,
	})
}

// replicatedStorageHealthRulePath points at the static Prometheus rule file with the pool and class
// alerts (read at runtime for the same reason as replicatedVolumeLayoutRulePath).
const replicatedStorageHealthRulePath = "../../../../monitoring/prometheus-rules/replicated-storage-health.yaml"

func TestReplicatedStorageHealthRulesMatchTheExportedMetrics(t *testing.T) {
	data, err := os.ReadFile(replicatedStorageHealthRulePath)
	if err != nil {
		t.Fatalf("reading rule file %q: %v", replicatedStorageHealthRulePath, err)
	}
	for _, helmMarker := range []string{`{{ "`, "{{-", "{{ if", "{{ end"} {
		if strings.Contains(string(data), helmMarker) {
			t.Fatalf("rule file %q must contain no Helm templating, found %q", replicatedStorageHealthRulePath, helmMarker)
		}
	}

	var groups []prometheusRuleGroup
	if err := yaml.Unmarshal(data, &groups); err != nil {
		t.Fatalf("unmarshalling rule file %q: %v", replicatedStorageHealthRulePath, err)
	}
	alerts := map[string]prometheusAlertRule{}
	for _, g := range groups {
		for _, r := range g.Rules {
			alerts[r.Alert] = r
		}
	}

	rspReady := metricNameRSPConditionProblem + `{condition="` + v1alpha1.ReplicatedStoragePoolCondReadyType + `"}`
	rscReady := metricNameRSCConditionProblem + `{condition="` + v1alpha1.ReplicatedStorageClassCondReadyType + `"}`
	tests := []struct {
		alert string
		expr  string
	}{
		{
			alert: "D8ReplicatedStoragePoolNotReady",
			expr:  "max by (" + LabelName + ") (" + rspReady + ")",
		},
		{
			// A class whose pool is not Ready is reported only by the pool alert.
			alert: "D8ReplicatedStorageClassNotReady",
			expr: "max by (" + LabelName + ", " + LabelStoragePool + ") (" + rscReady + ") unless on (" + LabelStoragePool + ") " +
				`label_replace(` + rspReady + `, "` + LabelStoragePool + `", "$1", "` + LabelName + `", "(.+)")`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.alert, func(t *testing.T) {
			alert, ok := alerts[tt.alert]
			if !ok {
				t.Fatalf("alert %s not found in %q", tt.alert, replicatedStorageHealthRulePath)
			}
			if got := strings.Join(strings.Fields(alert.Expr), " "); got != tt.expr {
				t.Fatalf("expected expr %q, got %q", tt.expr, got)
			}
			if alert.For != "5m" || alert.KeepFiringFor != "5m" {
				t.Fatalf("expected for: 5m and keep_firing_for: 5m, got %q and %q", alert.For, alert.KeepFiringFor)
			}
			if alert.Labels["severity_level"] != "4" || alert.Labels["tier"] != "cluster" {
				t.Fatalf("expected severity_level 4 and tier cluster, got %v", alert.Labels)
			}
		})
	}
}
