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
	"sigs.k8s.io/controller-runtime/pkg/client"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

const (
	currentMetricsRVPhaseActive   = "Active"
	currentMetricsRVPhaseDeleting = "Deleting"
)

var registerCurrentMetricsCollectorOnce sync.Once

// RegisterCurrentMetricsCollector registers a custom Prometheus collector that
// builds current metrics directly from the controller cache at scrape time.
func RegisterCurrentMetricsCollector(reader client.Reader, log logr.Logger) {
	registerCurrentMetricsCollectorOnce.Do(func() {
		crmetrics.Registry.MustRegister(newCurrentMetricsCollector(reader, log))
	})
}

type currentMetricsCollector struct {
	reader client.Reader
	log    logr.Logger

	rvCountDesc             *prometheus.Desc
	rvrCountDesc            *prometheus.Desc
	rvaCountDesc            *prometheus.Desc
	rvrUnhealthyCountDesc   *prometheus.Desc
	rvDeletionStartDesc     *prometheus.Desc
	rvrDeletionStartDesc    *prometheus.Desc
	datameshActiveDesc      *prometheus.Desc
	datameshActiveStartDesc *prometheus.Desc
}

func newCurrentMetricsCollector(reader client.Reader, log logr.Logger) *currentMetricsCollector {
	return &currentMetricsCollector{
		reader: reader,
		log:    log.WithName("current-metrics-collector"),
		rvCountDesc: prometheus.NewDesc(
			"sds_rv_count",
			"Current number of ReplicatedVolume objects by storage class and lifecycle phase. Built from controller cache at scrape time.",
			[]string{LabelStorageClass, LabelPhase},
			nil,
		),
		rvrCountDesc: prometheus.NewDesc(
			"sds_rvr_count",
			"Current number of ReplicatedVolumeReplica objects by node, storage class, and phase. Built from controller cache at scrape time.",
			[]string{LabelNode, LabelStorageClass, LabelPhase},
			nil,
		),
		rvaCountDesc: prometheus.NewDesc(
			"sds_rva_count",
			"Current number of ReplicatedVolumeAttachment objects by node and phase. Built from controller cache at scrape time.",
			[]string{LabelNode, LabelPhase},
			nil,
		),
		rvrUnhealthyCountDesc: prometheus.NewDesc(
			"sds_rvr_unhealthy_count",
			"Current number of ReplicatedVolumeReplica objects in degraded-like phases by node and phase. Built from controller cache at scrape time.",
			[]string{LabelNode, LabelPhase},
			nil,
		),
		rvDeletionStartDesc: prometheus.NewDesc(
			"sds_rv_deletion_started_timestamp_seconds",
			"Unix timestamp when the RV started deleting. Built from controller cache at scrape time.",
			[]string{LabelName, LabelStorageClass},
			nil,
		),
		rvrDeletionStartDesc: prometheus.NewDesc(
			"sds_rvr_deletion_started_timestamp_seconds",
			"Unix timestamp when the RVR started deleting. Built from controller cache at scrape time.",
			[]string{LabelName, LabelRV, LabelNode, LabelStorageClass},
			nil,
		),
		datameshActiveDesc: prometheus.NewDesc(
			"sds_rv_datamesh_active_transitions",
			"Number of currently active datamesh transitions per RV. Built from controller cache at scrape time.",
			[]string{LabelRV, LabelType},
			nil,
		),
		datameshActiveStartDesc: prometheus.NewDesc(
			"sds_rv_datamesh_active_transition_start_timestamp_seconds",
			"Unix timestamp of the oldest currently active datamesh transition for an RV and transition type. Built from controller cache at scrape time.",
			[]string{LabelRV, LabelType},
			nil,
		),
	}
}

func (c *currentMetricsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.rvCountDesc
	ch <- c.rvrCountDesc
	ch <- c.rvaCountDesc
	ch <- c.rvrUnhealthyCountDesc
	ch <- c.rvDeletionStartDesc
	ch <- c.rvrDeletionStartDesc
	ch <- c.datameshActiveDesc
	ch <- c.datameshActiveStartDesc
}

func (c *currentMetricsCollector) Collect(ch chan<- prometheus.Metric) {
	start := time.Now()
	defer func() {
		CurrentMetricsCollectDuration.Observe(time.Since(start).Seconds())
	}()

	ctx := context.Background()

	var nodeList corev1.NodeList
	if err := c.reader.List(ctx, &nodeList, client.UnsafeDisableDeepCopy); err != nil {
		c.collectError(err, "listing Nodes for current metrics")
		return
	}

	var rscList v1alpha1.ReplicatedStorageClassList
	if err := c.reader.List(ctx, &rscList, client.UnsafeDisableDeepCopy); err != nil {
		c.collectError(err, "listing ReplicatedStorageClasses for current metrics")
		return
	}

	var rvList v1alpha1.ReplicatedVolumeList
	if err := c.reader.List(ctx, &rvList, client.UnsafeDisableDeepCopy); err != nil {
		c.collectError(err, "listing ReplicatedVolumes for current metrics")
		return
	}

	var rvrList v1alpha1.ReplicatedVolumeReplicaList
	if err := c.reader.List(ctx, &rvrList, client.UnsafeDisableDeepCopy); err != nil {
		c.collectError(err, "listing ReplicatedVolumeReplicas for current metrics")
		return
	}

	var rvaList v1alpha1.ReplicatedVolumeAttachmentList
	if err := c.reader.List(ctx, &rvaList, client.UnsafeDisableDeepCopy); err != nil {
		c.collectError(err, "listing ReplicatedVolumeAttachments for current metrics")
		return
	}

	nodes := collectNodeNames(nodeList.Items, rvrList.Items, rvaList.Items)
	storageClasses := collectStorageClassNames(rscList.Items, rvList.Items, rvrList.Items)

	collectRVCounts(ch, c.rvCountDesc, storageClasses, rvList.Items)
	collectRVRCounts(ch, c.rvrCountDesc, c.rvrUnhealthyCountDesc, nodes, storageClasses, rvrList.Items)
	collectRVACounts(ch, c.rvaCountDesc, nodes, rvaList.Items)
	collectRVDeletionStarts(ch, c.rvDeletionStartDesc, rvList.Items)
	collectRVRDeletionStarts(ch, c.rvrDeletionStartDesc, rvrList.Items)
	collectDatameshActiveTransitions(ch, c.datameshActiveDesc, c.datameshActiveStartDesc, rvList.Items)

	CurrentMetricsObjects.WithLabelValues("node").Set(float64(len(nodeList.Items)))
	CurrentMetricsObjects.WithLabelValues("rsc").Set(float64(len(rscList.Items)))
	CurrentMetricsObjects.WithLabelValues("rv").Set(float64(len(rvList.Items)))
	CurrentMetricsObjects.WithLabelValues("rvr").Set(float64(len(rvrList.Items)))
	CurrentMetricsObjects.WithLabelValues("rva").Set(float64(len(rvaList.Items)))
}

func (c *currentMetricsCollector) collectError(err error, msg string) {
	CurrentMetricsCollectErrors.Inc()
	c.log.Error(err, msg)
}

func collectNodeNames(
	nodes []corev1.Node,
	rvrs []v1alpha1.ReplicatedVolumeReplica,
	rvas []v1alpha1.ReplicatedVolumeAttachment,
) []string {
	values := make([]string, 0, len(nodes)+len(rvrs)+len(rvas))
	seen := make(map[string]struct{}, len(nodes)+len(rvrs)+len(rvas))
	for i := range nodes {
		name := nodes[i].Name
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		values = append(values, name)
	}
	for i := range rvrs {
		name := rvrs[i].Spec.NodeName
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		values = append(values, name)
	}
	for i := range rvas {
		name := rvas[i].Spec.NodeName
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		values = append(values, name)
	}
	sort.Strings(values)
	return values
}

func collectStorageClassNames(
	rscs []v1alpha1.ReplicatedStorageClass,
	rvs []v1alpha1.ReplicatedVolume,
	rvrs []v1alpha1.ReplicatedVolumeReplica,
) []string {
	seen := make(map[string]struct{}, len(rscs)+len(rvs)+len(rvrs))
	for i := range rscs {
		seen[rscs[i].Name] = struct{}{}
	}
	for i := range rvs {
		seen[rvs[i].Spec.ReplicatedStorageClassName] = struct{}{}
	}
	for i := range rvrs {
		if sc := rvrs[i].Labels[v1alpha1.ReplicatedStorageClassLabelKey]; sc != "" {
			seen[sc] = struct{}{}
		}
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
	counts := make(map[string]float64, len(storageClasses)*len(phases))
	for _, sc := range storageClasses {
		for _, phase := range phases {
			counts[metricKey(sc, phase)] = 0
		}
	}

	for i := range rvs {
		sc := rvs[i].Spec.ReplicatedStorageClassName
		phase := currentMetricsRVPhaseActive
		if rvs[i].DeletionTimestamp != nil {
			phase = currentMetricsRVPhaseDeleting
		}
		counts[metricKey(sc, phase)]++
	}

	for _, sc := range storageClasses {
		for _, phase := range phases {
			ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, counts[metricKey(sc, phase)], sc, phase)
		}
	}
}

func collectRVRCounts(
	ch chan<- prometheus.Metric,
	countDesc *prometheus.Desc,
	unhealthyCountDesc *prometheus.Desc,
	nodes []string,
	storageClasses []string,
	rvrs []v1alpha1.ReplicatedVolumeReplica,
) {
	phases := rvrMetricPhases()
	counts := make(map[string]float64, len(nodes)*len(storageClasses)*len(phases))
	unhealthyCounts := make(map[string]float64, len(nodes)*len(phases))
	for _, node := range nodes {
		for _, sc := range storageClasses {
			for _, phase := range phases {
				counts[metricKey(node, sc, phase)] = 0
			}
		}
		for _, phase := range phases {
			if isRVRUnhealthyPhase(phase) {
				unhealthyCounts[metricKey(node, phase)] = 0
			}
		}
	}

	for i := range rvrs {
		node := rvrs[i].Spec.NodeName
		sc := rvrs[i].Labels[v1alpha1.ReplicatedStorageClassLabelKey]
		phase := string(rvrs[i].Status.Phase)
		if phase == "" {
			phase = "Unknown"
		}
		counts[metricKey(node, sc, phase)]++
		if isRVRUnhealthyPhase(phase) {
			unhealthyCounts[metricKey(node, phase)]++
		}
	}

	for _, node := range nodes {
		for _, sc := range storageClasses {
			for _, phase := range phases {
				ch <- prometheus.MustNewConstMetric(countDesc, prometheus.GaugeValue, counts[metricKey(node, sc, phase)], node, sc, phase)
			}
		}
		for _, phase := range phases {
			if !isRVRUnhealthyPhase(phase) {
				continue
			}
			ch <- prometheus.MustNewConstMetric(unhealthyCountDesc, prometheus.GaugeValue, unhealthyCounts[metricKey(node, phase)], node, phase)
		}
	}
}

func isRVRUnhealthyPhase(phase string) bool {
	switch phase {
	case string(v1alpha1.ReplicatedVolumeReplicaPhasePartiallyDegraded),
		string(v1alpha1.ReplicatedVolumeReplicaPhaseDegraded),
		string(v1alpha1.ReplicatedVolumeReplicaPhaseCritical),
		string(v1alpha1.ReplicatedVolumeReplicaPhaseAgentNotReady):
		return true
	default:
		return false
	}
}

func collectRVACounts(
	ch chan<- prometheus.Metric,
	desc *prometheus.Desc,
	nodes []string,
	rvas []v1alpha1.ReplicatedVolumeAttachment,
) {
	phases := rvaMetricPhases()
	counts := make(map[string]float64, len(nodes)*len(phases))
	for _, node := range nodes {
		for _, phase := range phases {
			counts[metricKey(node, phase)] = 0
		}
	}

	for i := range rvas {
		node := rvas[i].Spec.NodeName
		phase := string(rvas[i].Status.Phase)
		if phase == "" {
			phase = "Unknown"
		}
		counts[metricKey(node, phase)]++
	}

	for _, node := range nodes {
		for _, phase := range phases {
			ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, counts[metricKey(node, phase)], node, phase)
		}
	}
}

func collectRVDeletionStarts(
	ch chan<- prometheus.Metric,
	desc *prometheus.Desc,
	rvs []v1alpha1.ReplicatedVolume,
) {
	for i := range rvs {
		rv := &rvs[i]
		if rv.DeletionTimestamp == nil {
			continue
		}
		ch <- prometheus.MustNewConstMetric(
			desc,
			prometheus.GaugeValue,
			float64(rv.DeletionTimestamp.Unix()),
			rv.Name,
			rv.Spec.ReplicatedStorageClassName,
		)
	}
}

func collectRVRDeletionStarts(
	ch chan<- prometheus.Metric,
	desc *prometheus.Desc,
	rvrs []v1alpha1.ReplicatedVolumeReplica,
) {
	for i := range rvrs {
		rvr := &rvrs[i]
		if rvr.DeletionTimestamp == nil {
			continue
		}
		ch <- prometheus.MustNewConstMetric(
			desc,
			prometheus.GaugeValue,
			float64(rvr.DeletionTimestamp.Unix()),
			rvr.Name,
			rvr.Spec.ReplicatedVolumeName,
			rvr.Spec.NodeName,
			rvr.Labels[v1alpha1.ReplicatedStorageClassLabelKey],
		)
	}
}

func collectDatameshActiveTransitions(
	ch chan<- prometheus.Metric,
	activeDesc *prometheus.Desc,
	activeStartDesc *prometheus.Desc,
	rvs []v1alpha1.ReplicatedVolume,
) {
	for i := range rvs {
		rv := &rvs[i]
		typeCounts := make(map[string]float64, len(rv.Status.DatameshTransitions))
		oldestStartedAtByType := make(map[string]time.Time, len(rv.Status.DatameshTransitions))

		for j := range rv.Status.DatameshTransitions {
			transition := &rv.Status.DatameshTransitions[j]
			typ := string(transition.Type)
			typeCounts[typ]++

			startedAt := transition.StartedAt()
			if startedAt.IsZero() {
				continue
			}
			if prev, exists := oldestStartedAtByType[typ]; !exists || startedAt.Time.Before(prev) {
				oldestStartedAtByType[typ] = startedAt.Time
			}
		}

		for typ, count := range typeCounts {
			ch <- prometheus.MustNewConstMetric(activeDesc, prometheus.GaugeValue, count, rv.Name, typ)
			if startedAt, exists := oldestStartedAtByType[typ]; exists {
				ch <- prometheus.MustNewConstMetric(
					activeStartDesc,
					prometheus.GaugeValue,
					float64(startedAt.Unix()),
					rv.Name,
					typ,
				)
			}
		}
	}
}

func rvrMetricPhases() []string {
	return []string{
		"Unknown",
		string(v1alpha1.ReplicatedVolumeReplicaPhasePending),
		string(v1alpha1.ReplicatedVolumeReplicaPhaseProvisioning),
		string(v1alpha1.ReplicatedVolumeReplicaPhaseConfiguring),
		string(v1alpha1.ReplicatedVolumeReplicaPhaseWaitingForDatamesh),
		string(v1alpha1.ReplicatedVolumeReplicaPhaseSynchronizing),
		string(v1alpha1.ReplicatedVolumeReplicaPhaseHealthy),
		string(v1alpha1.ReplicatedVolumeReplicaPhasePartiallyDegraded),
		string(v1alpha1.ReplicatedVolumeReplicaPhaseDegraded),
		string(v1alpha1.ReplicatedVolumeReplicaPhaseCritical),
		string(v1alpha1.ReplicatedVolumeReplicaPhaseProgressing),
		string(v1alpha1.ReplicatedVolumeReplicaPhaseAgentNotReady),
		string(v1alpha1.ReplicatedVolumeReplicaPhaseTerminating),
	}
}

func rvaMetricPhases() []string {
	return []string{
		"Unknown",
		string(v1alpha1.ReplicatedVolumeAttachmentPhasePending),
		string(v1alpha1.ReplicatedVolumeAttachmentPhaseAttaching),
		string(v1alpha1.ReplicatedVolumeAttachmentPhaseAttached),
		string(v1alpha1.ReplicatedVolumeAttachmentPhaseDetaching),
		string(v1alpha1.ReplicatedVolumeAttachmentPhaseTerminating),
	}
}

func metricKey(values ...string) string {
	key := ""
	for i, value := range values {
		if i > 0 {
			key += "\x00"
		}
		key += value
	}
	return key
}
