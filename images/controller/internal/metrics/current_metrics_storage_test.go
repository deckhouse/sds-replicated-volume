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
	"encoding/json"
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

const replicatedStoragePoolCapacityRulePath = "../../../../monitoring/prometheus-rules/replicated-storage-pool-capacity.yaml"

func TestReplicatedStoragePoolCapacityRulesMatchTheExportedMetrics(t *testing.T) {
	data, err := os.ReadFile(replicatedStoragePoolCapacityRulePath)
	if err != nil {
		t.Fatalf("reading rule file %q: %v", replicatedStoragePoolCapacityRulePath, err)
	}

	var groups []prometheusRuleGroup
	if err := yaml.Unmarshal(data, &groups); err != nil {
		t.Fatalf("unmarshalling rule file %q: %v", replicatedStoragePoolCapacityRulePath, err)
	}
	rules := map[string]prometheusAlertRule{}
	for _, g := range groups {
		for _, r := range g.Rules {
			key := r.Alert + "/" + strings.Join(strings.Fields(r.Expr), " ")
			if _, ok := rules[key]; ok {
				t.Fatalf("rule %s with expr %q is defined twice", r.Alert, r.Expr)
			}
			rules[key] = r
		}
	}

	thin := "(1 - max by (node, " + LabelLVGName + ", " + LabelThinPool + ") (sds_node_configurator_lvg_thin_pool_used_size_bytes)" +
		" / max by (node, " + LabelLVGName + ", " + LabelThinPool + ") (sds_node_configurator_lvg_thin_pool_actual_size_bytes)) * 100" +
		" * on (" + LabelLVGName + ", " + LabelThinPool + ") group_right (node)" +
		" max by (" + LabelStoragePool + ", " + LabelLVGName + ", " + LabelThinPool + ") (" + metricNameRSPLVGInfo + `{` + LabelThinPool + `!=""})`
	thick := "max by (node, " + LabelLVGName + ") (sds_node_configurator_lvg_vg_free_bytes) * 100" +
		" / max by (node, " + LabelLVGName + ") (sds_node_configurator_lvg_vg_size_bytes)" +
		" * on (" + LabelLVGName + ") group_right (node)" +
		" max by (" + LabelStoragePool + ", " + LabelLVGName + ") (" + metricNameRSPLVGInfo + `{` + LabelThinPool + `=""})`

	tests := []struct {
		alert     string
		threshold string
		thinSev   string
		thickSev  string
	}{
		{"D8ReplicatedStoragePoolFreeCapacityLessThan20Percent", "20", "6", "7"},
		{"D8ReplicatedStoragePoolFreeCapacityLessThan10Percent", "10", "4", "6"},
		{"D8ReplicatedStoragePoolFreeCapacityLessThan5Percent", "5", "3", "5"},
		{"D8ReplicatedStoragePoolFreeCapacityLessThan1Percent", "1", "1", "4"},
	}
	if want := 2 * len(tests); len(rules) != want {
		t.Fatalf("expected %d rules in %q, got %d", want, replicatedStoragePoolCapacityRulePath, len(rules))
	}
	for _, tt := range tests {
		for _, v := range []struct {
			kind     string
			expr     string
			severity string
		}{
			{"thin", thin, tt.thinSev},
			{"thick", thick, tt.thickSev},
		} {
			t.Run(tt.alert+"/"+v.kind, func(t *testing.T) {
				expr := v.expr + " < " + tt.threshold
				rule, ok := rules[tt.alert+"/"+expr]
				if !ok {
					t.Fatalf("no rule %s with expr %q in %q", tt.alert, expr, replicatedStoragePoolCapacityRulePath)
				}
				if rule.For != "5m" || rule.KeepFiringFor != "5m" {
					t.Fatalf("expected for: 5m and keep_firing_for: 5m, got %q and %q", rule.For, rule.KeepFiringFor)
				}
				if rule.Labels["severity_level"] != v.severity || rule.Labels["tier"] != "cluster" {
					t.Fatalf("expected severity_level %s and tier cluster, got %v", v.severity, rule.Labels)
				}
			})
		}
	}
}

func collectCapacityInfo(t *testing.T, rsps []v1alpha1.ReplicatedStoragePool, rscs []v1alpha1.ReplicatedStorageClass) (lvgInfo, replicas []testMetric) {
	t.Helper()

	lvgInfoDesc := prometheus.NewDesc("test_rsp_lvg_info", "test",
		[]string{LabelStoragePool, LabelLVGName, LabelThinPool}, nil)
	replicasDesc := prometheus.NewDesc("test_rsc_replicas", "test",
		[]string{LabelStorageClass, LabelStoragePool}, nil)

	lvgCh := make(chan prometheus.Metric, 100)
	go func() {
		defer close(lvgCh)
		collectRSPLVGInfo(lvgCh, lvgInfoDesc, rsps)
	}()
	lvgInfo = collectTestMetrics(t, lvgCh)

	replicasCh := make(chan prometheus.Metric, 100)
	go func() {
		defer close(replicasCh)
		collectRSCReplicas(replicasCh, replicasDesc, rscs)
	}()
	replicas = collectTestMetrics(t, replicasCh)
	return lvgInfo, replicas
}

func storagePool(name string, typ v1alpha1.ReplicatedStoragePoolType, lvgs ...v1alpha1.ReplicatedStoragePoolLVMVolumeGroups) v1alpha1.ReplicatedStoragePool {
	return v1alpha1.ReplicatedStoragePool{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       v1alpha1.ReplicatedStoragePoolSpec{Type: typ, LVMVolumeGroups: lvgs},
	}
}

func TestCollectRSPLVGInfoEmitsEveryVolumeGroupOfEveryPool(t *testing.T) {
	deleteTime := metav1.Now()
	deleting := storagePool("rsp-c-deleting", v1alpha1.ReplicatedStoragePoolTypeLVM,
		v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{Name: "lvg-3"})
	deleting.DeletionTimestamp = &deleteTime
	deleting.Finalizers = []string{"test"}

	lvgInfo, _ := collectCapacityInfo(t, []v1alpha1.ReplicatedStoragePool{
		storagePool("rsp-a-thick", v1alpha1.ReplicatedStoragePoolTypeLVM,
			v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{Name: "lvg-1"},
			v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{Name: "lvg-2"},
			// A repeated entry must not produce a second series with the same labels.
			v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{Name: "lvg-1"},
		),
		storagePool("rsp-b-thin", v1alpha1.ReplicatedStoragePoolTypeLVMThin,
			v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{Name: "lvg-1", ThinPoolName: "tp-a"},
			v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{Name: "lvg-1", ThinPoolName: "tp-b"},
		),
		// A deleting pool still holds its volumes, so its capacity is still reported.
		deleting,
	}, nil)

	want := []map[string]string{
		{LabelStoragePool: "rsp-a-thick", LabelLVGName: "lvg-1", LabelThinPool: ""},
		{LabelStoragePool: "rsp-a-thick", LabelLVGName: "lvg-2", LabelThinPool: ""},
		{LabelStoragePool: "rsp-b-thin", LabelLVGName: "lvg-1", LabelThinPool: "tp-a"},
		{LabelStoragePool: "rsp-b-thin", LabelLVGName: "lvg-1", LabelThinPool: "tp-b"},
		{LabelStoragePool: "rsp-c-deleting", LabelLVGName: "lvg-3", LabelThinPool: ""},
	}
	if len(lvgInfo) != len(want) {
		t.Fatalf("expected %d series, got %d: %#v", len(want), len(lvgInfo), lvgInfo)
	}
	for i := range want {
		assertMetric(t, lvgInfo[i], 1, want[i])
	}
}

func TestCollectRSCReplicasFollowsTheAcceptedConfiguration(t *testing.T) {
	zero, one := byte(0), byte(1)
	_, replicas := collectCapacityInfo(t, nil, []v1alpha1.ReplicatedStorageClass{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "rsc-1-accepted"},
			Spec:       v1alpha1.ReplicatedStorageClassSpec{Replication: v1alpha1.ReplicationConsistencyAndAvailability},
			Status: v1alpha1.ReplicatedStorageClassStatus{
				StoragePoolName: "rsp-a",
				Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
					ReplicatedStoragePoolName: "rsp-a", FailuresToTolerate: 1, GuaranteedMinimumDataRedundancy: 1,
				},
			},
		},
		{
			// The spec moved to None on another pool, but the change is not accepted: new volumes still
			// get two replicas on the old pool.
			ObjectMeta: metav1.ObjectMeta{Name: "rsc-2-pending"},
			Spec:       v1alpha1.ReplicatedStorageClassSpec{Replication: v1alpha1.ReplicationNone},
			Status: v1alpha1.ReplicatedStorageClassStatus{
				StoragePoolName: "rsp-b",
				Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
					ReplicatedStoragePoolName: "rsp-a", FailuresToTolerate: 1, GuaranteedMinimumDataRedundancy: 0,
				},
			},
		},
		{
			// No configuration accepted yet: the spec and the computed pool are used.
			ObjectMeta: metav1.ObjectMeta{Name: "rsc-3-first-configuration"},
			Spec:       v1alpha1.ReplicatedStorageClassSpec{Replication: v1alpha1.ReplicationConsistency},
			Status:     v1alpha1.ReplicatedStorageClassStatus{StoragePoolName: "rsp-c"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "rsc-4-ftt-gmdr"},
			Spec:       v1alpha1.ReplicatedStorageClassSpec{FailuresToTolerate: &zero, GuaranteedMinimumDataRedundancy: &zero},
			Status:     v1alpha1.ReplicatedStorageClassStatus{StoragePoolName: "rsp-c"},
		},
		{
			// An unset replication means ConsistencyAndAvailability.
			ObjectMeta: metav1.ObjectMeta{Name: "rsc-5-default"},
			Status:     v1alpha1.ReplicatedStorageClassStatus{StoragePoolName: "rsp-c"},
		},
		{
			// An old-format configuration has no pool name and is about to be rewritten.
			ObjectMeta: metav1.ObjectMeta{Name: "rsc-6-old-format"},
			Spec:       v1alpha1.ReplicatedStorageClassSpec{FailuresToTolerate: &one, GuaranteedMinimumDataRedundancy: &zero},
			Status: v1alpha1.ReplicatedStorageClassStatus{
				StoragePoolName: "rsp-d",
				Configuration:   &v1alpha1.ReplicatedVolumeConfiguration{Topology: v1alpha1.TopologyIgnored},
			},
		},
		{
			// An invalid spec may leave the class without a storage pool: nothing to report.
			ObjectMeta: metav1.ObjectMeta{Name: "rsc-7-no-pool"},
			Spec:       v1alpha1.ReplicatedStorageClassSpec{Replication: v1alpha1.ReplicationNone},
		},
	})

	want := []struct {
		class, pool string
		replicas    float64
	}{
		{"rsc-1-accepted", "rsp-a", 3},
		{"rsc-2-pending", "rsp-a", 2},
		{"rsc-3-first-configuration", "rsp-c", 2},
		{"rsc-4-ftt-gmdr", "rsp-c", 1},
		{"rsc-5-default", "rsp-c", 3},
		{"rsc-6-old-format", "rsp-d", 2},
	}
	if len(replicas) != len(want) {
		t.Fatalf("expected %d series, got %d: %#v", len(want), len(replicas), replicas)
	}
	for i, w := range want {
		assertMetric(t, replicas[i], w.replicas, map[string]string{LabelStorageClass: w.class, LabelStoragePool: w.pool})
	}
}

func TestCapacityInfoFollowsTheCacheSnapshot(t *testing.T) {
	thick := storagePool("rsp-thick", v1alpha1.ReplicatedStoragePoolTypeLVM,
		v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{Name: "lvg-1"})
	thin := storagePool("rsp-thin", v1alpha1.ReplicatedStoragePoolTypeLVMThin,
		v1alpha1.ReplicatedStoragePoolLVMVolumeGroups{Name: "lvg-1", ThinPoolName: "tp"})
	class := func(pool string) v1alpha1.ReplicatedStorageClass {
		return v1alpha1.ReplicatedStorageClass{
			ObjectMeta: metav1.ObjectMeta{Name: "rsc"},
			Status: v1alpha1.ReplicatedStorageClassStatus{
				StoragePoolName: pool,
				Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
					ReplicatedStoragePoolName: pool, FailuresToTolerate: 1, GuaranteedMinimumDataRedundancy: 1,
				},
			},
		}
	}

	_, replicas := collectCapacityInfo(t, []v1alpha1.ReplicatedStoragePool{thick}, []v1alpha1.ReplicatedStorageClass{class("rsp-thick")})
	if len(replicas) != 1 {
		t.Fatalf("expected one series, got %d: %#v", len(replicas), replicas)
	}
	assertMetric(t, replicas[0], 3, map[string]string{LabelStorageClass: "rsc", LabelStoragePool: "rsp-thick"})

	// Once the class moves to another pool, its series moves with it instead of being duplicated, and
	// the old pool keeps its own series while it exists.
	lvgInfo, replicas := collectCapacityInfo(t, []v1alpha1.ReplicatedStoragePool{thick, thin}, []v1alpha1.ReplicatedStorageClass{class("rsp-thin")})
	if len(replicas) != 1 {
		t.Fatalf("expected the series to be replaced, got %d: %#v", len(replicas), replicas)
	}
	assertMetric(t, replicas[0], 3, map[string]string{LabelStorageClass: "rsc", LabelStoragePool: "rsp-thin"})
	if len(lvgInfo) != 2 {
		t.Fatalf("expected a series per pool, got %d: %#v", len(lvgInfo), lvgInfo)
	}

	// A deleted pool or class is simply absent from the reader, so its series disappears without any
	// explicit metric removal.
	lvgInfo, replicas = collectCapacityInfo(t, []v1alpha1.ReplicatedStoragePool{thin}, nil)
	if len(replicas) != 0 {
		t.Fatalf("expected no series for a deleted class, got %d: %#v", len(replicas), replicas)
	}
	if len(lvgInfo) != 1 {
		t.Fatalf("expected one series, got %d: %#v", len(lvgInfo), lvgInfo)
	}
	assertMetric(t, lvgInfo[0], 1, map[string]string{LabelStoragePool: "rsp-thin", LabelLVGName: "lvg-1", LabelThinPool: "tp"})
}

// replicatedVolumeDashboardPath points at the Grafana dashboard of the new control plane (read at runtime
// for the same reason as replicatedVolumeLayoutRulePath).
const replicatedVolumeDashboardPath = "../../../../monitoring/grafana-dashboards/storage/sds-replicated-volume.json"

func TestCapacityDashboardMatchesTheExportedMetrics(t *testing.T) {
	data, err := os.ReadFile(replicatedVolumeDashboardPath)
	if err != nil {
		t.Fatalf("reading dashboard %q: %v", replicatedVolumeDashboardPath, err)
	}

	type panel struct {
		Title   string `json:"title"`
		Targets []struct {
			Expr string `json:"expr"`
		} `json:"targets"`
		Panels []panel `json:"panels"`
	}
	var dashboard struct {
		Panels []panel `json:"panels"`
	}
	if err := json.Unmarshal(data, &dashboard); err != nil {
		t.Fatalf("unmarshalling dashboard %q: %v", replicatedVolumeDashboardPath, err)
	}

	var row *panel
	for i := range dashboard.Panels {
		if dashboard.Panels[i].Title == "Capacity by ReplicatedStorageClass" {
			row = &dashboard.Panels[i]
		}
	}
	if row == nil || len(row.Panels) == 0 {
		t.Fatalf("capacity row not found in %q", replicatedVolumeDashboardPath)
	}

	// Every capacity panel is filtered by the storage classes selected on the dashboard, and capacity
	// reaches a class only through the pool membership series.
	usesLVGInfo := false
	for _, p := range row.Panels {
		for _, target := range p.Targets {
			if !strings.Contains(target.Expr, metricNameRSCReplicas+`{`+LabelStorageClass+`=~"$storage_class"}`) {
				t.Errorf("panel %q: expression does not select %s by the storage class: %s", p.Title, metricNameRSCReplicas, target.Expr)
			}
			usesLVGInfo = usesLVGInfo || strings.Contains(target.Expr, metricNameRSPLVGInfo+`{`+LabelThinPool)
		}
	}
	if !usesLVGInfo {
		t.Errorf("no capacity panel joins the LVMVolumeGroup capacity through %s", metricNameRSPLVGInfo)
	}
}
