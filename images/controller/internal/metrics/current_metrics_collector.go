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
	"sort"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

const (
	currentMetricsRVPhaseActive   = "Active"
	currentMetricsRVPhaseDeleting = "Deleting"
	currentMetricsNodeGlobal      = "global"
	currentMetricsNodeUnknown     = "unknown"
	currentMetricsSCUnknown       = "unknown"
	currentMetricsCollectTimeout  = 5 * time.Second

	currentMetricsKindRV  = "rv"
	currentMetricsKindRVR = "rvr"
	currentMetricsKindRVA = "rva"

	// currentMetricsReasonAbsent is the synthetic reason label used when a condition is missing
	// from the object status entirely. Every reason a condition actually carries is exported
	// verbatim, so this value never collides with a real one.
	currentMetricsReasonAbsent = ReasonAbsent

	// currentMetricsStatusAbsent is the synthetic status label paired with currentMetricsReasonAbsent.
	// It is distinct from the real Unknown condition status.
	currentMetricsStatusAbsent = "Absent"
)

// currentMetricsRVConditionTypes lists the ReplicatedVolume conditions exported by
// sds_rv_condition_count.
var currentMetricsRVConditionTypes = []string{
	v1alpha1.ReplicatedVolumeCondReadyType,
	v1alpha1.ReplicatedVolumeCondResilientType,
	v1alpha1.ReplicatedVolumeCondAttachedReadyType,
	v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType,
	v1alpha1.ReplicatedVolumeCondConfigurationReadyType,
	v1alpha1.ReplicatedVolumeCondConfiguredType,
	v1alpha1.ReplicatedVolumeCondScheduledType,
	v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesType,
	v1alpha1.ReplicatedVolumeCondUnderMaintenanceType,
}

// currentMetricsRVProblemConditionTypes lists the ReplicatedVolume conditions exported per volume by
// sds_rv_condition_problem_timestamp_seconds. Configured, Scheduled and SatisfyEligibleNodes are left
// out: they are mostly transient and, during an outage, duplicate Resilient and
// MembershipLayoutConverged while doubling the number of per-volume series; their aggregate is in
// sds_rv_condition_count.
var currentMetricsRVProblemConditionTypes = []string{
	v1alpha1.ReplicatedVolumeCondReadyType,
	v1alpha1.ReplicatedVolumeCondResilientType,
	v1alpha1.ReplicatedVolumeCondAttachedReadyType,
	v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedType,
	v1alpha1.ReplicatedVolumeCondConfigurationReadyType,
	v1alpha1.ReplicatedVolumeCondUnderMaintenanceType,
}

// Names of current metrics selected by alert rules are kept in constants, and the unit tests
// cross-check the rule expressions against them:
//   - metricNameRVConditionProblem: monitoring/prometheus-rules/replicated-volume-layout.yaml and
//     monitoring/prometheus-rules/replicated-volume-health.yaml;
//   - metricNameRSPConditionProblem and metricNameRSCConditionProblem:
//     monitoring/prometheus-rules/replicated-storage-health.yaml;
//   - metricNameCollectorUp: monitoring/prometheus-rules/current-metrics.tpl;
//   - metricNameRSPLVGInfo: monitoring/prometheus-rules/replicated-storage-pool-capacity.yaml;
//   - metricNameRSPLVGInfo and metricNameRSCReplicas: the capacity panels of
//     monitoring/grafana-dashboards/storage/sds-replicated-volume.json.
const (
	metricNameRVConditionProblem  = "sds_rv_condition_problem_timestamp_seconds"
	metricNameRSPConditionProblem = "sds_rsp_condition_problem_timestamp_seconds"
	metricNameRSCConditionProblem = "sds_rsc_condition_problem_timestamp_seconds"
	metricNameCollectorUp         = "sds_metrics_collector_up"
	metricNameRSPLVGInfo          = "sds_rsp_lvg_info"
	metricNameRSCReplicas         = "sds_rsc_replicas"
)

// Package-level guard: exactly one current metrics collector is registered per process.
// The controller runs one manager per process; a second in-process manager would share the
// collector registered by the first manager.
var registerCurrentMetricsCollectorOnce sync.Once

type currentMetricLabels2 struct {
	first  string
	second string
}

type currentMetricLabels3 struct {
	first  string
	second string
	third  string
}

type currentMetricLabels4 struct {
	first  string
	second string
	third  string
	fourth string
}

// currentMetricsCollectorRunnable registers the collector once this replica has won the leader
// election. Every metric the collector exports describes cluster-wide state derived from the
// manager cache, and that cache is fully populated on every replica, leader or not. Registering
// the collector unconditionally would therefore make each replica export the same numbers under a
// distinct instance label, and any aggregation across instances would multiply them by the replica
// count (replicas match the master count, so typically by three).
type currentMetricsCollectorRunnable struct {
	registerOnce *sync.Once
	register     func()
}

var _ manager.LeaderElectionRunnable = &currentMetricsCollectorRunnable{}

func (r *currentMetricsCollectorRunnable) NeedLeaderElection() bool { return true }

func (r *currentMetricsCollectorRunnable) Start(ctx context.Context) error {
	r.registerOnce.Do(func() {
		r.register()
	})
	// The collector has no loop of its own: it is driven by scrapes. Block anyway, because Runnable
	// is documented to run until the manager context is closed.
	<-ctx.Done()
	return nil
}

// AddCurrentMetricsCollector schedules a custom Prometheus collector that builds current metrics
// directly from the controller cache at scrape time. The collector is registered only on the
// leader replica; see currentMetricsCollectorRunnable.
func AddCurrentMetricsCollector(mgr manager.Manager) error {
	return mgr.Add(&currentMetricsCollectorRunnable{
		registerOnce: &registerCurrentMetricsCollectorOnce,
		register: func() {
			crmetrics.Registry.MustRegister(newCurrentMetricsCollector(mgr.GetCache(), mgr.GetLogger()))
		},
	})
}

type currentMetricsCollector struct {
	reader client.Reader
	log    logr.Logger

	rvCountDesc                    *prometheus.Desc
	rvConditionCountDesc           *prometheus.Desc
	rvrCountDesc                   *prometheus.Desc
	rvaCountDesc                   *prometheus.Desc
	rvrDeletingCountDesc           *prometheus.Desc
	datameshActiveDesc             *prometheus.Desc
	rvNoPersistentVolumeDesc       *prometheus.Desc
	rvAutoConfigurationBlockedDesc *prometheus.Desc
	rvConditionProblemDesc         *prometheus.Desc
	rspConditionProblemDesc        *prometheus.Desc
	rscConditionProblemDesc        *prometheus.Desc
	rspLVGInfoDesc                 *prometheus.Desc
	rscReplicasDesc                *prometheus.Desc
	rvDeletingDesc                 *prometheus.Desc
	deletingOldestDesc             *prometheus.Desc
	datameshTransitionOldestDesc   *prometheus.Desc
	rvrNotReadyOldestDesc          *prometheus.Desc
	collectorUpDesc                *prometheus.Desc
	collectorObjectsDesc           *prometheus.Desc
}

func newCurrentMetricsCollector(reader client.Reader, log logr.Logger) *currentMetricsCollector {
	return &currentMetricsCollector{
		reader: reader,
		log:    log.WithName("current-metrics-collector"),
		rvCountDesc: prometheus.NewDesc(
			"sds_rv_count",
			"Current number of ReplicatedVolume objects by storage class, configuration mode, and lifecycle phase. Manual volumes have no storage class and use storage_class=\"unknown\", configuration_mode=\"Manual\". Built from controller cache at scrape time.",
			[]string{LabelStorageClass, LabelConfigurationMode, LabelPhase},
			nil,
		),
		rvConditionCountDesc: prometheus.NewDesc(
			"sds_rv_condition_count",
			"Current number of non-deleting ReplicatedVolume objects by storage class and by the status and reason of each condition (Ready, Resilient, AttachedReady, MembershipLayoutConverged, ConfigurationReady, Configured, Scheduled, SatisfyEligibleNodes, UnderMaintenance). A missing condition is reported as status=\"Absent\", reason=\"Absent\", so the sum over one condition equals the number of non-deleting volumes; for the presence-based UnderMaintenance, Absent is the normal state. Deleting volumes are excluded: their conditions are frozen. Only observed label combinations are emitted. Built from controller cache at scrape time.",
			[]string{LabelStorageClass, LabelCondition, LabelStatus, LabelReason},
			nil,
		),
		rvDeletingDesc: prometheus.NewDesc(
			"sds_rv_deleting_timestamp_seconds",
			"Unix time of deletionTimestamp of every ReplicatedVolume that is being deleted; time() minus the value is how long the deletion lasts. Emitted only while the volume is deleting. Built from controller cache at scrape time.",
			[]string{LabelName, LabelStorageClass},
			nil,
		),
		deletingOldestDesc: prometheus.NewDesc(
			"sds_deleting_oldest_timestamp_seconds",
			"Unix time of the oldest deletionTimestamp among deleting ReplicatedVolume (kind=\"rv\", node=\"global\"), ReplicatedVolumeReplica (kind=\"rvr\") and ReplicatedVolumeAttachment (kind=\"rva\") objects, by storage class and node; time() minus the value is the age of the oldest deletion. Emitted only for groups with deleting objects. Built from controller cache at scrape time.",
			[]string{LabelKind, LabelStorageClass, LabelNode},
			nil,
		),
		datameshTransitionOldestDesc: prometheus.NewDesc(
			"sds_rv_datamesh_transition_oldest_timestamp_seconds",
			"Unix time of the start of the oldest active datamesh transition by storage class, node, and transition type (the same labels as sds_rv_datamesh_active_transitions); time() minus the value is the age of the oldest transition. Emitted only for groups with active transitions. Built from controller cache at scrape time.",
			[]string{LabelStorageClass, LabelNode, LabelType},
			nil,
		),
		rvrNotReadyOldestDesc: prometheus.NewDesc(
			"sds_rvr_not_ready_oldest_timestamp_seconds",
			"Unix time since which the longest non-Ready ReplicatedVolumeReplica has been non-Ready (lastTransitionTime of its Ready condition, or creationTimestamp while the condition is absent), by storage class, node, and phase; time() minus the value is how long it lasts. Deleting replicas are excluded (see sds_deleting_oldest_timestamp_seconds). Emitted only for groups with non-Ready replicas. Built from controller cache at scrape time.",
			[]string{LabelStorageClass, LabelNode, LabelPhase},
			nil,
		),
		rvrCountDesc: prometheus.NewDesc(
			"sds_rvr_count",
			"Current number of ReplicatedVolumeReplica objects by node, storage class (the replica's storage class label, or its volume's storage class while the label is not set; unknown for Manual volumes), and phase. Only observed label combinations are emitted. Built from controller cache at scrape time.",
			[]string{LabelNode, LabelStorageClass, LabelPhase},
			nil,
		),
		rvaCountDesc: prometheus.NewDesc(
			"sds_rva_count",
			"Current number of ReplicatedVolumeAttachment objects by node, storage class, and phase. Only observed label combinations are emitted. Built from controller cache at scrape time.",
			[]string{LabelNode, LabelStorageClass, LabelPhase},
			nil,
		),
		rvrDeletingCountDesc: prometheus.NewDesc(
			"sds_rvr_deleting_count",
			"Current number of deleting ReplicatedVolumeReplica objects by node and storage class (resolved as in sds_rvr_count). Only observed label combinations are emitted. Built from controller cache at scrape time.",
			[]string{LabelNode, LabelStorageClass},
			nil,
		),
		datameshActiveDesc: prometheus.NewDesc(
			"sds_rv_datamesh_active_transitions",
			"Current number of active datamesh transitions by storage class, node, and transition type. Global transitions use node=\"global\". Only observed label combinations are emitted. Built from controller cache at scrape time.",
			[]string{LabelStorageClass, LabelNode, LabelType},
			nil,
		),
		rvNoPersistentVolumeDesc: prometheus.NewDesc(
			"sds_rv_no_persistent_volume",
			"ReplicatedVolume objects carrying the no-persistent-volume label (set by linstor-migrator when the volume has no matching PersistentVolume). Emitted once per affected ReplicatedVolume with value 1. Built from controller cache at scrape time.",
			[]string{LabelName},
			nil,
		),
		rvAutoConfigurationBlockedDesc: prometheus.NewDesc(
			"sds_rv_auto_configuration_blocked",
			"ReplicatedVolume objects carrying the auto-configuration-blocked label (set by linstor-migrator when the volume cannot be switched to Auto configuration). Emitted once per affected ReplicatedVolume with value 1. Built from controller cache at scrape time.",
			[]string{LabelName},
			nil,
		),
		collectorObjectsDesc: prometheus.NewDesc(
			"sds_metrics_collector_objects",
			"Number of Kubernetes objects processed by the SDS current metrics collector by kind. Emitted only after a successful run, like every other current metric, so a failed run does not show the counts of an earlier one.",
			[]string{"kind"},
			nil,
		),
		collectorUpDesc: prometheus.NewDesc(
			metricNameCollectorUp,
			"Whether the last run of the current metrics collector succeeded (1) or failed (0); on failure no other current metric is emitted. Exported only by the leader replica, so the series is absent while no leader collector is scraped (controller down, no leader, broken scrape). An empty cluster legitimately has no other current metrics, so use this series, not their absence, to tell whether current metrics are available.",
			nil,
			nil,
		),
		rvConditionProblemDesc: prometheus.NewDesc(
			metricNameRVConditionProblem,
			"Unix time of the last status transition (lastTransitionTime) of a ReplicatedVolume condition that is in a problem state; time() minus the value is how long the problem lasts. Emitted only for problem states: status other than True for Ready, Resilient, AttachedReady, MembershipLayoutConverged and ConfigurationReady, and presence of the presence-based UnderMaintenance. A volume without the Ready condition (no status published yet) is reported as Ready with status and reason Absent and its creationTimestamp as the value; other absent conditions are not reported. A healthy or deleting volume produces no series, and a series disappears once its condition recovers. The reason label carries the condition reason verbatim; a reason change within the same status keeps the value. Built from controller cache at scrape time.",
			[]string{LabelName, LabelStorageClass, LabelCondition, LabelStatus, LabelReason},
			nil,
		),
		rspConditionProblemDesc: prometheus.NewDesc(
			metricNameRSPConditionProblem,
			"Unix time of the last status transition (lastTransitionTime) of the Ready condition of a ReplicatedStoragePool that is not True; time() minus the value is how long the problem lasts. A pool without the Ready condition (no status published yet) is reported with status and reason Absent and its creationTimestamp as the value. A ready or deleting pool produces no series. The reason label carries the condition reason verbatim. Built from controller cache at scrape time.",
			[]string{LabelName, LabelCondition, LabelStatus, LabelReason},
			nil,
		),
		rscConditionProblemDesc: prometheus.NewDesc(
			metricNameRSCConditionProblem,
			"Unix time of the last status transition (lastTransitionTime) of the Ready condition of a ReplicatedStorageClass that is not True; time() minus the value is how long the problem lasts. The storage_pool label is status.storagePoolName, empty while the class has no storage pool. A class without the Ready condition (no status published yet) is reported with status and reason Absent and its creationTimestamp as the value. A ready or deleting class produces no series. The reason label carries the condition reason verbatim. Built from controller cache at scrape time.",
			[]string{LabelName, LabelStoragePool, LabelCondition, LabelStatus, LabelReason},
			nil,
		),
		rspLVGInfoDesc: prometheus.NewDesc(
			metricNameRSPLVGInfo,
			"One series with value 1 per LVMVolumeGroup of a ReplicatedStoragePool (spec.lvmVolumeGroups), with its thin pool for LVMThin pools; thin_pool is empty for LVM pools. Joins the capacity metrics of the sds-node-configurator agent (sds_node_configurator_lvg_*, labeled by lvg_name and thin_pool) to storage_pool. Built from controller cache at scrape time.",
			[]string{LabelStoragePool, LabelLVGName, LabelThinPool},
			nil,
		),
		rscReplicasDesc: prometheus.NewDesc(
			metricNameRSCReplicas,
			"Number of diskful replicas (failuresToTolerate + guaranteedMinimumDataRedundancy + 1) of the volumes of a ReplicatedStorageClass, labeled with the storage pool they are placed in. Both come from the accepted configuration (status.configuration), or, until the first configuration is accepted, from the spec and status.storagePoolName; a class without a storage pool produces no series. The capacity of the pool divided by the value is the capacity of the class in volume bytes. Built from controller cache at scrape time.",
			[]string{LabelStorageClass, LabelStoragePool},
			nil,
		),
	}
}

func (c *currentMetricsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.rvCountDesc
	ch <- c.rvConditionCountDesc
	ch <- c.rvrCountDesc
	ch <- c.rvaCountDesc
	ch <- c.rvrDeletingCountDesc
	ch <- c.datameshActiveDesc
	ch <- c.rvNoPersistentVolumeDesc
	ch <- c.rvAutoConfigurationBlockedDesc
	ch <- c.rvConditionProblemDesc
	ch <- c.rspConditionProblemDesc
	ch <- c.rscConditionProblemDesc
	ch <- c.rspLVGInfoDesc
	ch <- c.rscReplicasDesc
	ch <- c.rvDeletingDesc
	ch <- c.deletingOldestDesc
	ch <- c.datameshTransitionOldestDesc
	ch <- c.rvrNotReadyOldestDesc
	ch <- c.collectorObjectsDesc
	ch <- c.collectorUpDesc
}

func (c *currentMetricsCollector) Collect(ch chan<- prometheus.Metric) {
	start := time.Now()
	defer func() {
		CurrentMetricsCollectDuration.Observe(time.Since(start).Seconds())
	}()

	ctx, cancel := context.WithTimeout(context.Background(), currentMetricsCollectTimeout)
	defer cancel()

	var nodeList corev1.NodeList
	if err := c.reader.List(ctx, &nodeList, client.UnsafeDisableDeepCopy); err != nil {
		c.collectError(ch, err, "listing Nodes for current metrics")
		return
	}

	var rscList v1alpha1.ReplicatedStorageClassList
	if err := c.reader.List(ctx, &rscList, client.UnsafeDisableDeepCopy); err != nil {
		c.collectError(ch, err, "listing ReplicatedStorageClasses for current metrics")
		return
	}

	var rspList v1alpha1.ReplicatedStoragePoolList
	if err := c.reader.List(ctx, &rspList, client.UnsafeDisableDeepCopy); err != nil {
		c.collectError(ch, err, "listing ReplicatedStoragePools for current metrics")
		return
	}

	var rvList v1alpha1.ReplicatedVolumeList
	if err := c.reader.List(ctx, &rvList, client.UnsafeDisableDeepCopy); err != nil {
		c.collectError(ch, err, "listing ReplicatedVolumes for current metrics")
		return
	}

	var rvrList v1alpha1.ReplicatedVolumeReplicaList
	if err := c.reader.List(ctx, &rvrList, client.UnsafeDisableDeepCopy); err != nil {
		c.collectError(ch, err, "listing ReplicatedVolumeReplicas for current metrics")
		return
	}

	var rvaList v1alpha1.ReplicatedVolumeAttachmentList
	if err := c.reader.List(ctx, &rvaList, client.UnsafeDisableDeepCopy); err != nil {
		c.collectError(ch, err, "listing ReplicatedVolumeAttachments for current metrics")
		return
	}

	storageClasses := collectStorageClassNames(rscList.Items, rvList.Items, rvrList.Items, rvaList.Items)

	collectRVCounts(ch, c.rvCountDesc, storageClasses, rvList.Items)
	collectRVConditionCounts(ch, c.rvConditionCountDesc, rvList.Items)
	collectRVMigratorLabels(ch, c.rvNoPersistentVolumeDesc, c.rvAutoConfigurationBlockedDesc, rvList.Items)
	collectRVConditionProblems(ch, c.rvConditionProblemDesc, rvList.Items)
	collectRSPConditionProblems(ch, c.rspConditionProblemDesc, rspList.Items)
	collectRSCConditionProblems(ch, c.rscConditionProblemDesc, rscList.Items)
	collectRSPLVGInfo(ch, c.rspLVGInfoDesc, rspList.Items)
	collectRSCReplicas(ch, c.rscReplicasDesc, rscList.Items)
	collectRVDeleting(ch, c.rvDeletingDesc, rvList.Items)
	collectDeletingOldest(ch, c.deletingOldestDesc, rvList.Items, rvrList.Items, rvaList.Items)
	collectDatameshTransitionOldest(ch, c.datameshTransitionOldestDesc, rvList.Items, rvrList.Items)
	collectRVRNotReadyOldest(ch, c.rvrNotReadyOldestDesc, rvList.Items, rvrList.Items)
	collectRVRCounts(ch, c.rvrCountDesc, rvList.Items, rvrList.Items)
	collectRVACounts(ch, c.rvaCountDesc, rvList.Items, rvaList.Items)
	collectRVRDeletingCounts(ch, c.rvrDeletingCountDesc, rvList.Items, rvrList.Items)
	collectDatameshActiveTransitions(ch, c.datameshActiveDesc, rvList.Items, rvrList.Items)

	for _, o := range []struct {
		kind  string
		count int
	}{
		{"node", len(nodeList.Items)},
		{"rsc", len(rscList.Items)},
		{"rsp", len(rspList.Items)},
		{"rv", len(rvList.Items)},
		{"rvr", len(rvrList.Items)},
		{"rva", len(rvaList.Items)},
	} {
		ch <- prometheus.MustNewConstMetric(c.collectorObjectsDesc, prometheus.GaugeValue, float64(o.count), o.kind)
	}

	ch <- prometheus.MustNewConstMetric(c.collectorUpDesc, prometheus.GaugeValue, 1)
}

// collectError reports a failed collector run: no other current metric is emitted on this
// scrape, so sds_metrics_collector_up=0 tells it apart from an empty cluster.
func (c *currentMetricsCollector) collectError(ch chan<- prometheus.Metric, err error, msg string) {
	CurrentMetricsCollectErrors.Inc()
	c.log.Error(err, msg)
	ch <- prometheus.MustNewConstMetric(c.collectorUpDesc, prometheus.GaugeValue, 0)
}

func collectStorageClassNames(
	rscs []v1alpha1.ReplicatedStorageClass,
	rvs []v1alpha1.ReplicatedVolume,
	rvrs []v1alpha1.ReplicatedVolumeReplica,
	rvas []v1alpha1.ReplicatedVolumeAttachment,
) []string {
	seen := make(map[string]struct{}, len(rscs)+len(rvs)+len(rvrs)+len(rvas))
	for i := range rscs {
		seen[StorageClassLabel(rscs[i].Name)] = struct{}{}
	}
	for i := range rvs {
		seen[StorageClassLabel(rvs[i].Spec.ReplicatedStorageClassName)] = struct{}{}
	}
	for i := range rvrs {
		seen[StorageClassLabel(rvrs[i].Labels[v1alpha1.ReplicatedStorageClassLabelKey])] = struct{}{}
	}
	for i := range rvas {
		seen[StorageClassLabel(rvas[i].Labels[v1alpha1.ReplicatedStorageClassLabelKey])] = struct{}{}
	}

	values := make([]string, 0, len(seen))
	for value := range seen {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}

func collectRVCounts(
	ch chan<- prometheus.Metric,
	desc *prometheus.Desc,
	storageClasses []string,
	rvs []v1alpha1.ReplicatedVolume,
) {
	phases := []string{currentMetricsRVPhaseActive, currentMetricsRVPhaseDeleting}

	// Zero-fill one configuration mode per storage class, so the series set stays the same as
	// without the mode label: the API requires an RSC for Auto and forbids it for Manual, hence
	// storage_class="unknown" is Manual and every named storage class is Auto.
	counts := make(map[currentMetricLabels3]float64, len(storageClasses)*len(phases))
	for _, sc := range storageClasses {
		mode := string(v1alpha1.ReplicatedVolumeConfigurationModeAuto)
		if sc == currentMetricsSCUnknown {
			mode = string(v1alpha1.ReplicatedVolumeConfigurationModeManual)
		}
		for _, phase := range phases {
			counts[currentMetricLabels3{first: sc, second: mode, third: phase}] = 0
		}
	}

	// A combination outside the zero-filled matrix (e.g. an Auto RV without an RSC) is still
	// counted as an observed series rather than dropped.
	for i := range rvs {
		rv := &rvs[i]
		phase := currentMetricsRVPhaseActive
		if rv.DeletionTimestamp != nil {
			phase = currentMetricsRVPhaseDeleting
		}
		counts[currentMetricLabels3{
			first:  StorageClassLabel(rv.Spec.ReplicatedStorageClassName),
			second: ConfigurationModeLabel(rv.Spec.ConfigurationMode),
			third:  phase,
		}]++
	}

	for _, key := range sortedCurrentMetricLabels3(counts) {
		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, counts[key], key.first, key.second, key.third)
	}
}

// collectRVConditionCounts emits the number of non-deleting ReplicatedVolumes per storage class and
// per status/reason of every condition in currentMetricsRVConditionTypes. Deleting volumes are
// skipped: Ready switches to Terminating while Resilient, AttachedReady and Configured are frozen at
// their last values, so counting them would report stale problems; sds_rv_count{phase="Deleting"}
// already covers them.
func collectRVConditionCounts(
	ch chan<- prometheus.Metric,
	desc *prometheus.Desc,
	rvs []v1alpha1.ReplicatedVolume,
) {
	counts := make(map[currentMetricLabels4]float64)
	for i := range rvs {
		rv := &rvs[i]
		if rv.DeletionTimestamp != nil {
			continue
		}
		sc := StorageClassLabel(rv.Spec.ReplicatedStorageClassName)
		for _, condType := range currentMetricsRVConditionTypes {
			status, reason := currentMetricsStatusAbsent, currentMetricsReasonAbsent
			if cond := meta.FindStatusCondition(rv.Status.Conditions, condType); cond != nil {
				status, reason = string(cond.Status), cond.Reason
			}
			counts[currentMetricLabels4{first: sc, second: condType, third: status, fourth: reason}]++
		}
	}

	for _, key := range sortedCurrentMetricLabels4(counts) {
		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, counts[key], key.first, key.second, key.third, key.fourth)
	}
}

// collectRVMigratorLabels emits one gauge (value 1) per ReplicatedVolume carrying a migrator
// label: no-persistent-volume or auto-configuration-blocked. Only affected RVs produce series,
// so healthy RVs contribute no samples and the metric is absent (alert resolves) once the label
// is removed or the RV is deleted.
func collectRVMigratorLabels(
	ch chan<- prometheus.Metric,
	noPersistentVolumeDesc *prometheus.Desc,
	autoConfigurationBlockedDesc *prometheus.Desc,
	rvs []v1alpha1.ReplicatedVolume,
) {
	noPersistentVolume := make(map[string]float64)
	autoConfigurationBlocked := make(map[string]float64)
	for i := range rvs {
		rv := &rvs[i]
		if rv.Labels[v1alpha1.NoPersistentVolumeLabelKey] == v1alpha1.NoPersistentVolumeLabelValue {
			noPersistentVolume[rv.Name] = 1
		}
		if rv.Labels[v1alpha1.AutoConfigurationBlockedLabelKey] == v1alpha1.AutoConfigurationBlockedLabelValue {
			autoConfigurationBlocked[rv.Name] = 1
		}
	}

	emit := func(desc *prometheus.Desc, counts map[string]float64) {
		names := make([]string, 0, len(counts))
		for name := range counts {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, counts[name], name)
		}
	}

	emit(noPersistentVolumeDesc, noPersistentVolume)
	emit(autoConfigurationBlockedDesc, autoConfigurationBlocked)
}

// collectRVConditionProblems emits one gauge per problem condition of every non-deleting
// ReplicatedVolume; see isRVConditionProblem. The value is the condition's lastTransitionTime, so
// dashboards and alerts read "since when" as time() - value. A missing condition is skipped: it has
// no transition time, and sds_rv_condition_count already reports it as Absent.
//
// Only problem states produce series, so the series count follows the number of broken volumes,
// not the total; a recovered or deleted volume simply stops being emitted (no
// DeleteLabelValues/finalizer bookkeeping). Deleting volumes are skipped for the same reason as in
// collectRVConditionCounts: their conditions are frozen.
func collectRVConditionProblems(
	ch chan<- prometheus.Metric,
	desc *prometheus.Desc,
	rvs []v1alpha1.ReplicatedVolume,
) {
	for i := range rvs {
		rv := &rvs[i]
		if rv.DeletionTimestamp != nil {
			continue
		}
		sc := StorageClassLabel(rv.Spec.ReplicatedStorageClassName)
		for _, condType := range currentMetricsRVProblemConditionTypes {
			cond := meta.FindStatusCondition(rv.Status.Conditions, condType)
			if cond == nil && condType == v1alpha1.ReplicatedVolumeCondReadyType {
				// No Ready means the controller has not published any status yet. Normally this lasts
				// until the first reconcile; a volume stuck before it (e.g. a missing storage pool)
				// would otherwise be invisible, so report it as not Ready since creation. Other
				// absent conditions are not reported: one series per stuck volume is enough.
				ch <- prometheus.MustNewConstMetric(
					desc,
					prometheus.GaugeValue,
					float64(rv.CreationTimestamp.Unix()),
					rv.Name, sc, condType, currentMetricsStatusAbsent, currentMetricsReasonAbsent,
				)
				continue
			}
			if cond == nil || !isRVConditionProblem(cond) {
				continue
			}
			ch <- prometheus.MustNewConstMetric(
				desc,
				prometheus.GaugeValue,
				float64(cond.LastTransitionTime.Unix()),
				rv.Name, sc, condType, string(cond.Status), cond.Reason,
			)
		}
	}
}

// isRVConditionProblem reports whether a present condition is in a problem state. UnderMaintenance
// is presence-based, so its mere presence is the problem; every other condition is a problem
// whenever it is not True.
func isRVConditionProblem(cond *metav1.Condition) bool {
	if cond.Type == v1alpha1.ReplicatedVolumeCondUnderMaintenanceType {
		return true
	}
	return cond.Status != metav1.ConditionTrue
}

// collectRSPConditionProblems emits one series per ReplicatedStoragePool whose Ready condition is not
// True, in the same shape as sds_rv_condition_problem_timestamp_seconds.
func collectRSPConditionProblems(
	ch chan<- prometheus.Metric,
	desc *prometheus.Desc,
	rsps []v1alpha1.ReplicatedStoragePool,
) {
	for i := range rsps {
		rsp := &rsps[i]
		if rsp.DeletionTimestamp != nil {
			continue
		}
		since, status, reason, ok := readyProblem(rsp.Status.Conditions, v1alpha1.ReplicatedStoragePoolCondReadyType, rsp.CreationTimestamp)
		if !ok {
			continue
		}
		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, since,
			rsp.Name, v1alpha1.ReplicatedStoragePoolCondReadyType, status, reason)
	}
}

// collectRSCConditionProblems emits one series per ReplicatedStorageClass whose Ready condition is
// not True. The storage pool of the class is a label, so that an alert can leave out the classes
// whose pool is reported on its own.
func collectRSCConditionProblems(
	ch chan<- prometheus.Metric,
	desc *prometheus.Desc,
	rscs []v1alpha1.ReplicatedStorageClass,
) {
	for i := range rscs {
		rsc := &rscs[i]
		if rsc.DeletionTimestamp != nil {
			continue
		}
		since, status, reason, ok := readyProblem(rsc.Status.Conditions, v1alpha1.ReplicatedStorageClassCondReadyType, rsc.CreationTimestamp)
		if !ok {
			continue
		}
		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, since,
			rsc.Name, rsc.Status.StoragePoolName, v1alpha1.ReplicatedStorageClassCondReadyType, status, reason)
	}
}

// collectRSPLVGInfo emits one series per LVMVolumeGroup and thin pool of every ReplicatedStoragePool,
// deleting ones included: their LVMVolumeGroups still hold volumes. A repeated entry is emitted once,
// because two series with the same labels would fail the whole scrape.
func collectRSPLVGInfo(
	ch chan<- prometheus.Metric,
	desc *prometheus.Desc,
	rsps []v1alpha1.ReplicatedStoragePool,
) {
	entries := make(map[currentMetricLabels3]float64)
	for i := range rsps {
		rsp := &rsps[i]
		for _, lvg := range rsp.Spec.LVMVolumeGroups {
			entries[currentMetricLabels3{rsp.Name, lvg.Name, lvg.ThinPoolName}] = 1
		}
	}

	for _, key := range sortedCurrentMetricLabels3(entries) {
		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, entries[key], key.first, key.second, key.third)
	}
}

// collectRSCReplicas emits the number of diskful replicas of every ReplicatedStorageClass with its
// storage pool. New volumes get the accepted configuration, so it wins over a spec that is not accepted
// yet; the spec is used only until the first configuration is accepted (or while an old-format one
// without a pool name waits for migration).
func collectRSCReplicas(
	ch chan<- prometheus.Metric,
	desc *prometheus.Desc,
	rscs []v1alpha1.ReplicatedStorageClass,
) {
	for i := range rscs {
		rsc := &rscs[i]
		pool, ftt, gmdr := rsc.Status.StoragePoolName, rsc.Spec.GetFTT(), rsc.Spec.GetGMDR()
		if cfg := rsc.Status.Configuration; cfg != nil && cfg.ReplicatedStoragePoolName != "" {
			pool, ftt, gmdr = cfg.ReplicatedStoragePoolName, cfg.FailuresToTolerate, cfg.GuaranteedMinimumDataRedundancy
		}
		if pool == "" {
			continue
		}
		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue,
			float64(v1alpha1.IntendedDiskfulCount(ftt, gmdr)), rsc.Name, pool)
	}
}

// readyProblem reports whether the Ready condition is in a problem state, and since when. A missing
// Ready means the controller has not published any status yet; it is reported as Absent since the
// creation of the object, so that an object stuck before its first reconcile is not invisible.
func readyProblem(conditions []metav1.Condition, readyType string, created metav1.Time) (since float64, status, reason string, ok bool) {
	cond := meta.FindStatusCondition(conditions, readyType)
	if cond == nil {
		return float64(created.Unix()), currentMetricsStatusAbsent, currentMetricsReasonAbsent, true
	}
	if cond.Status == metav1.ConditionTrue {
		return 0, "", "", false
	}
	return float64(cond.LastTransitionTime.Unix()), string(cond.Status), cond.Reason, true
}

func collectRVRCounts(
	ch chan<- prometheus.Metric,
	countDesc *prometheus.Desc,
	rvs []v1alpha1.ReplicatedVolume,
	rvrs []v1alpha1.ReplicatedVolumeReplica,
) {
	rvStorageClasses := currentMetricsRVStorageClasses(rvs)
	counts := make(map[currentMetricLabels3]float64, len(rvrs))
	for i := range rvrs {
		node := currentMetricsNodeLabel(rvrs[i].Spec.NodeName)
		sc := childStorageClassLabel(rvrs[i].Labels, rvrs[i].Spec.ReplicatedVolumeName, rvStorageClasses)
		phase := string(rvrs[i].Status.Phase)
		if phase == "" {
			phase = "Unknown"
		}
		counts[currentMetricLabels3{node, sc, phase}]++
	}

	for _, labels := range sortedCurrentMetricLabels3(counts) {
		ch <- prometheus.MustNewConstMetric(countDesc, prometheus.GaugeValue, counts[labels], labels.first, labels.second, labels.third)
	}
}

func collectRVACounts(
	ch chan<- prometheus.Metric,
	desc *prometheus.Desc,
	rvs []v1alpha1.ReplicatedVolume,
	rvas []v1alpha1.ReplicatedVolumeAttachment,
) {
	rvStorageClasses := make(map[string]string, len(rvs))
	for i := range rvs {
		rvStorageClasses[rvs[i].Name] = StorageClassLabel(rvs[i].Spec.ReplicatedStorageClassName)
	}

	counts := make(map[currentMetricLabels3]float64, len(rvas))
	for i := range rvas {
		node := currentMetricsNodeLabel(rvas[i].Spec.NodeName)
		sc := childStorageClassLabel(rvas[i].Labels, rvas[i].Spec.ReplicatedVolumeName, rvStorageClasses)
		phase := string(rvas[i].Status.Phase)
		if phase == "" {
			phase = "Unknown"
		}
		counts[currentMetricLabels3{node, sc, phase}]++
	}

	for _, labels := range sortedCurrentMetricLabels3(counts) {
		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, counts[labels], labels.first, labels.second, labels.third)
	}
}

func collectRVRDeletingCounts(
	ch chan<- prometheus.Metric,
	desc *prometheus.Desc,
	rvs []v1alpha1.ReplicatedVolume,
	rvrs []v1alpha1.ReplicatedVolumeReplica,
) {
	rvStorageClasses := currentMetricsRVStorageClasses(rvs)
	counts := make(map[currentMetricLabels2]float64, len(rvrs))
	for i := range rvrs {
		rvr := &rvrs[i]
		if rvr.DeletionTimestamp == nil {
			continue
		}
		node := currentMetricsNodeLabel(rvr.Spec.NodeName)
		sc := childStorageClassLabel(rvr.Labels, rvr.Spec.ReplicatedVolumeName, rvStorageClasses)
		counts[currentMetricLabels2{node, sc}]++
	}

	for _, labels := range sortedCurrentMetricLabels2(counts) {
		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, counts[labels], labels.first, labels.second)
	}
}

func collectDatameshActiveTransitions(
	ch chan<- prometheus.Metric,
	activeDesc *prometheus.Desc,
	rvs []v1alpha1.ReplicatedVolume,
	rvrs []v1alpha1.ReplicatedVolumeReplica,
) {
	replicaNodes := make(map[string]string, len(rvrs))
	for i := range rvrs {
		replicaNodes[rvrs[i].Name] = currentMetricsNodeLabel(rvrs[i].Spec.NodeName)
	}

	typeCounts := make(map[currentMetricLabels3]float64)
	for i := range rvs {
		rv := &rvs[i]
		sc := StorageClassLabel(rv.Spec.ReplicatedStorageClassName)
		for j := range rv.Status.DatameshTransitions {
			transition := &rv.Status.DatameshTransitions[j]
			typ := string(transition.Type)
			node := currentMetricsNodeGlobal
			if transition.ReplicaName != "" {
				node = replicaNodes[transition.ReplicaName]
				if node == "" {
					node = currentMetricsNodeUnknown
				}
			}
			typeCounts[currentMetricLabels3{sc, node, typ}]++
		}
	}

	for _, labels := range sortedCurrentMetricLabels3(typeCounts) {
		ch <- prometheus.MustNewConstMetric(activeDesc, prometheus.GaugeValue, typeCounts[labels], labels.first, labels.second, labels.third)
	}
}

// collectRVDeleting emits one gauge per deleting ReplicatedVolume valued at its deletionTimestamp.
// Deletion normally takes seconds, so a series rarely outlives a scrape; one that does is a stuck
// deletion, and it disappears once the volume is gone.
func collectRVDeleting(
	ch chan<- prometheus.Metric,
	desc *prometheus.Desc,
	rvs []v1alpha1.ReplicatedVolume,
) {
	for i := range rvs {
		rv := &rvs[i]
		if rv.DeletionTimestamp == nil {
			continue
		}
		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, float64(rv.DeletionTimestamp.Unix()),
			rv.Name, StorageClassLabel(rv.Spec.ReplicatedStorageClassName))
	}
}

// collectDeletingOldest emits, per kind, storage class and node, the oldest deletionTimestamp among
// deleting RVs, RVRs and RVAs. A replica or attachment is pinned to a node, so a stuck deletion there
// usually points at that node's agent; a volume has no node and uses node="global".
func collectDeletingOldest(
	ch chan<- prometheus.Metric,
	desc *prometheus.Desc,
	rvs []v1alpha1.ReplicatedVolume,
	rvrs []v1alpha1.ReplicatedVolumeReplica,
	rvas []v1alpha1.ReplicatedVolumeAttachment,
) {
	rvStorageClasses := currentMetricsRVStorageClasses(rvs)
	oldest := make(map[currentMetricLabels3]float64)
	for i := range rvs {
		rv := &rvs[i]
		if rv.DeletionTimestamp == nil {
			continue
		}
		key := currentMetricLabels3{currentMetricsKindRV, StorageClassLabel(rv.Spec.ReplicatedStorageClassName), currentMetricsNodeGlobal}
		keepOldest(oldest, key, rv.DeletionTimestamp.Time)
	}
	for i := range rvrs {
		rvr := &rvrs[i]
		if rvr.DeletionTimestamp == nil {
			continue
		}
		key := currentMetricLabels3{currentMetricsKindRVR, childStorageClassLabel(rvr.Labels, rvr.Spec.ReplicatedVolumeName, rvStorageClasses), currentMetricsNodeLabel(rvr.Spec.NodeName)}
		keepOldest(oldest, key, rvr.DeletionTimestamp.Time)
	}
	for i := range rvas {
		rva := &rvas[i]
		if rva.DeletionTimestamp == nil {
			continue
		}
		key := currentMetricLabels3{currentMetricsKindRVA, childStorageClassLabel(rva.Labels, rva.Spec.ReplicatedVolumeName, rvStorageClasses), currentMetricsNodeLabel(rva.Spec.NodeName)}
		keepOldest(oldest, key, rva.DeletionTimestamp.Time)
	}

	for _, key := range sortedCurrentMetricLabels3(oldest) {
		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, oldest[key], key.first, key.second, key.third)
	}
}

// collectDatameshTransitionOldest emits the start time of the oldest active datamesh transition,
// grouped exactly like collectDatameshActiveTransitions so the two can be matched in one query.
// A transition without a start time (its first step not started) has no age and is skipped.
func collectDatameshTransitionOldest(
	ch chan<- prometheus.Metric,
	desc *prometheus.Desc,
	rvs []v1alpha1.ReplicatedVolume,
	rvrs []v1alpha1.ReplicatedVolumeReplica,
) {
	replicaNodes := make(map[string]string, len(rvrs))
	for i := range rvrs {
		replicaNodes[rvrs[i].Name] = currentMetricsNodeLabel(rvrs[i].Spec.NodeName)
	}

	oldest := make(map[currentMetricLabels3]float64)
	for i := range rvs {
		rv := &rvs[i]
		sc := StorageClassLabel(rv.Spec.ReplicatedStorageClassName)
		for j := range rv.Status.DatameshTransitions {
			transition := &rv.Status.DatameshTransitions[j]
			startedAt := transition.StartedAt()
			if startedAt.IsZero() {
				continue
			}
			node := currentMetricsNodeGlobal
			if transition.ReplicaName != "" {
				node = replicaNodes[transition.ReplicaName]
				if node == "" {
					node = currentMetricsNodeUnknown
				}
			}
			keepOldest(oldest, currentMetricLabels3{sc, node, string(transition.Type)}, startedAt.Time)
		}
	}

	for _, key := range sortedCurrentMetricLabels3(oldest) {
		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, oldest[key], key.first, key.second, key.third)
	}
}

// collectRVRNotReadyOldest emits, per storage class, node and phase, the moment since which the
// longest non-Ready replica has been non-Ready. The Ready condition's lastTransitionTime covers both
// a replica that never became Ready (startup phases) and one that lost readiness later; a replica
// without the condition yet falls back to its creation time. Deleting replicas are left to
// collectDeletingOldest.
func collectRVRNotReadyOldest(
	ch chan<- prometheus.Metric,
	desc *prometheus.Desc,
	rvs []v1alpha1.ReplicatedVolume,
	rvrs []v1alpha1.ReplicatedVolumeReplica,
) {
	rvStorageClasses := currentMetricsRVStorageClasses(rvs)
	oldest := make(map[currentMetricLabels3]float64)
	for i := range rvrs {
		rvr := &rvrs[i]
		if rvr.DeletionTimestamp != nil {
			continue
		}
		since := rvr.CreationTimestamp.Time
		if cond := meta.FindStatusCondition(rvr.Status.Conditions, v1alpha1.ReplicatedVolumeReplicaCondReadyType); cond != nil {
			if cond.Status == metav1.ConditionTrue {
				continue
			}
			since = cond.LastTransitionTime.Time
		}
		phase := string(rvr.Status.Phase)
		if phase == "" {
			phase = "Unknown"
		}
		key := currentMetricLabels3{childStorageClassLabel(rvr.Labels, rvr.Spec.ReplicatedVolumeName, rvStorageClasses), currentMetricsNodeLabel(rvr.Spec.NodeName), phase}
		keepOldest(oldest, key, since)
	}

	for _, key := range sortedCurrentMetricLabels3(oldest) {
		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, oldest[key], key.first, key.second, key.third)
	}
}

// keepOldest stores t (as Unix seconds) under key unless an earlier time is already stored.
func keepOldest(oldest map[currentMetricLabels3]float64, key currentMetricLabels3, t time.Time) {
	value := float64(t.Unix())
	if current, ok := oldest[key]; !ok || value < current {
		oldest[key] = value
	}
}

// currentMetricsRVStorageClasses maps ReplicatedVolume names to their storage class label.
func currentMetricsRVStorageClasses(rvs []v1alpha1.ReplicatedVolume) map[string]string {
	storageClasses := make(map[string]string, len(rvs))
	for i := range rvs {
		storageClasses[rvs[i].Name] = StorageClassLabel(rvs[i].Spec.ReplicatedStorageClassName)
	}
	return storageClasses
}

// childStorageClassLabel returns the storage class label of an RVR or RVA: its own RSC label, or the
// parent volume's storage class while the label is not set yet.
func childStorageClassLabel(labels map[string]string, rvName string, rvStorageClasses map[string]string) string {
	if sc := labels[v1alpha1.ReplicatedStorageClassLabelKey]; sc != "" {
		return sc
	}
	if sc, ok := rvStorageClasses[rvName]; ok {
		return sc
	}
	return currentMetricsSCUnknown
}

func currentMetricsNodeLabel(node string) string {
	if node == "" {
		return currentMetricsNodeUnknown
	}
	return node
}

func sortedCurrentMetricLabels2(counts map[currentMetricLabels2]float64) []currentMetricLabels2 {
	labels := make([]currentMetricLabels2, 0, len(counts))
	for label := range counts {
		labels = append(labels, label)
	}
	sort.Slice(labels, func(i, j int) bool {
		if labels[i].first != labels[j].first {
			return labels[i].first < labels[j].first
		}
		return labels[i].second < labels[j].second
	})
	return labels
}

func sortedCurrentMetricLabels3(counts map[currentMetricLabels3]float64) []currentMetricLabels3 {
	labels := make([]currentMetricLabels3, 0, len(counts))
	for label := range counts {
		labels = append(labels, label)
	}
	sort.Slice(labels, func(i, j int) bool {
		if labels[i].first != labels[j].first {
			return labels[i].first < labels[j].first
		}
		if labels[i].second != labels[j].second {
			return labels[i].second < labels[j].second
		}
		return labels[i].third < labels[j].third
	})
	return labels
}

func sortedCurrentMetricLabels4(counts map[currentMetricLabels4]float64) []currentMetricLabels4 {
	labels := make([]currentMetricLabels4, 0, len(counts))
	for label := range counts {
		labels = append(labels, label)
	}
	sort.Slice(labels, func(i, j int) bool {
		if labels[i].first != labels[j].first {
			return labels[i].first < labels[j].first
		}
		if labels[i].second != labels[j].second {
			return labels[i].second < labels[j].second
		}
		if labels[i].third != labels[j].third {
			return labels[i].third < labels[j].third
		}
		return labels[i].fourth < labels[j].fourth
	})
	return labels
}
