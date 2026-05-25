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
	currentMetricsNodeGlobal      = "global"
	currentMetricsNodeUnknown     = "unknown"
	currentMetricsSCUnknown       = "unknown"
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

	rvCountDesc          *prometheus.Desc
	rvrCountDesc         *prometheus.Desc
	rvaCountDesc         *prometheus.Desc
	rvrDeletingCountDesc *prometheus.Desc
	datameshActiveDesc   *prometheus.Desc
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
			"Current number of ReplicatedVolumeAttachment objects by node, storage class, and phase. Built from controller cache at scrape time.",
			[]string{LabelNode, LabelStorageClass, LabelPhase},
			nil,
		),
		rvrDeletingCountDesc: prometheus.NewDesc(
			"sds_rvr_deleting_count",
			"Current number of deleting ReplicatedVolumeReplica objects by node and storage class. Built from controller cache at scrape time.",
			[]string{LabelNode, LabelStorageClass},
			nil,
		),
		datameshActiveDesc: prometheus.NewDesc(
			"sds_rv_datamesh_active_transitions",
			"Current number of active datamesh transitions by storage class, node, and transition type. Global transitions use node=\"global\". Built from controller cache at scrape time.",
			[]string{LabelStorageClass, LabelNode, LabelType},
			nil,
		),
	}
}

func (c *currentMetricsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.rvCountDesc
	ch <- c.rvrCountDesc
	ch <- c.rvaCountDesc
	ch <- c.rvrDeletingCountDesc
	ch <- c.datameshActiveDesc
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

	// Build the zero-filled node matrix from Kubernetes nodes because RVA targets
	// are regular node names. Pre-emitting zero series keeps Grafana node filters continuous.
	nodes := collectNodeNames(nodeList.Items, rvrList.Items, rvaList.Items)
	storageClasses := collectStorageClassNames(rscList.Items, rvList.Items, rvrList.Items, rvaList.Items)

	collectRVCounts(ch, c.rvCountDesc, storageClasses, rvList.Items)
	collectRVRCounts(ch, c.rvrCountDesc, nodes, storageClasses, rvrList.Items)
	collectRVACounts(ch, c.rvaCountDesc, nodes, storageClasses, rvList.Items, rvaList.Items)
	collectRVRDeletingCounts(ch, c.rvrDeletingCountDesc, nodes, storageClasses, rvrList.Items)
	collectDatameshActiveTransitions(ch, c.datameshActiveDesc, nodes, storageClasses, rvList.Items, rvrList.Items)

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
		name := currentMetricsNodeLabel(rvrs[i].Spec.NodeName)
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		values = append(values, name)
	}
	for i := range rvas {
		name := currentMetricsNodeLabel(rvas[i].Spec.NodeName)
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
	rvas []v1alpha1.ReplicatedVolumeAttachment,
) []string {
	seen := make(map[string]struct{}, len(rscs)+len(rvs)+len(rvrs)+len(rvas))
	for i := range rscs {
		seen[currentMetricsStorageClassLabel(rscs[i].Name)] = struct{}{}
	}
	for i := range rvs {
		seen[currentMetricsStorageClassLabel(rvs[i].Spec.ReplicatedStorageClassName)] = struct{}{}
	}
	for i := range rvrs {
		seen[currentMetricsStorageClassLabel(rvrs[i].Labels[v1alpha1.ReplicatedStorageClassLabelKey])] = struct{}{}
	}
	for i := range rvas {
		seen[currentMetricsStorageClassLabel(rvas[i].Labels[v1alpha1.ReplicatedStorageClassLabelKey])] = struct{}{}
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
		sc := currentMetricsStorageClassLabel(rvs[i].Spec.ReplicatedStorageClassName)
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
	nodes []string,
	storageClasses []string,
	rvrs []v1alpha1.ReplicatedVolumeReplica,
) {
	phases := rvrMetricPhases()
	counts := make(map[string]float64, len(nodes)*len(storageClasses)*len(phases))
	for _, node := range nodes {
		for _, sc := range storageClasses {
			for _, phase := range phases {
				counts[metricKey(node, sc, phase)] = 0
			}
		}
	}

	for i := range rvrs {
		node := currentMetricsNodeLabel(rvrs[i].Spec.NodeName)
		sc := currentMetricsStorageClassLabel(rvrs[i].Labels[v1alpha1.ReplicatedStorageClassLabelKey])
		phase := string(rvrs[i].Status.Phase)
		if phase == "" {
			phase = "Unknown"
		}
		counts[metricKey(node, sc, phase)]++
	}

	for _, node := range nodes {
		for _, sc := range storageClasses {
			for _, phase := range phases {
				ch <- prometheus.MustNewConstMetric(countDesc, prometheus.GaugeValue, counts[metricKey(node, sc, phase)], node, sc, phase)
			}
		}
	}
}

func collectRVACounts(
	ch chan<- prometheus.Metric,
	desc *prometheus.Desc,
	nodes []string,
	storageClasses []string,
	rvs []v1alpha1.ReplicatedVolume,
	rvas []v1alpha1.ReplicatedVolumeAttachment,
) {
	phases := rvaMetricPhases()
	counts := make(map[string]float64, len(nodes)*len(storageClasses)*len(phases))
	for _, node := range nodes {
		for _, sc := range storageClasses {
			for _, phase := range phases {
				counts[metricKey(node, sc, phase)] = 0
			}
		}
	}

	rvStorageClasses := make(map[string]string, len(rvs))
	for i := range rvs {
		rvStorageClasses[rvs[i].Name] = currentMetricsStorageClassLabel(rvs[i].Spec.ReplicatedStorageClassName)
	}

	for i := range rvas {
		node := currentMetricsNodeLabel(rvas[i].Spec.NodeName)
		sc := rvas[i].Labels[v1alpha1.ReplicatedStorageClassLabelKey]
		if sc == "" {
			sc = rvStorageClasses[rvas[i].Spec.ReplicatedVolumeName]
		}
		sc = currentMetricsStorageClassLabel(sc)
		phase := string(rvas[i].Status.Phase)
		if phase == "" {
			phase = "Unknown"
		}
		counts[metricKey(node, sc, phase)]++
	}

	for _, node := range nodes {
		for _, sc := range storageClasses {
			for _, phase := range phases {
				ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, counts[metricKey(node, sc, phase)], node, sc, phase)
			}
		}
	}
}

func collectRVRDeletingCounts(
	ch chan<- prometheus.Metric,
	desc *prometheus.Desc,
	nodes []string,
	storageClasses []string,
	rvrs []v1alpha1.ReplicatedVolumeReplica,
) {
	counts := make(map[string]float64, len(nodes)*len(storageClasses))
	for _, node := range nodes {
		for _, sc := range storageClasses {
			counts[metricKey(node, sc)] = 0
		}
	}

	for i := range rvrs {
		rvr := &rvrs[i]
		if rvr.DeletionTimestamp == nil {
			continue
		}
		node := currentMetricsNodeLabel(rvr.Spec.NodeName)
		sc := currentMetricsStorageClassLabel(rvr.Labels[v1alpha1.ReplicatedStorageClassLabelKey])
		counts[metricKey(node, sc)]++
	}

	for _, node := range nodes {
		for _, sc := range storageClasses {
			ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, counts[metricKey(node, sc)], node, sc)
		}
	}
}

func collectDatameshActiveTransitions(
	ch chan<- prometheus.Metric,
	activeDesc *prometheus.Desc,
	nodes []string,
	storageClasses []string,
	rvs []v1alpha1.ReplicatedVolume,
	rvrs []v1alpha1.ReplicatedVolumeReplica,
) {
	nodes = prependMissingCurrentMetricsNodes(nodes, currentMetricsNodeGlobal, currentMetricsNodeUnknown)
	types := datameshTransitionTypes()
	typeCounts := make(map[string]float64, len(storageClasses)*len(nodes)*len(types))
	for _, sc := range storageClasses {
		for _, node := range nodes {
			for _, typ := range types {
				typeCounts[metricKey(sc, node, typ)] = 0
			}
		}
	}

	replicaNodes := make(map[string]string, len(rvrs))
	for i := range rvrs {
		replicaNodes[rvrs[i].Name] = currentMetricsNodeLabel(rvrs[i].Spec.NodeName)
	}

	for i := range rvs {
		rv := &rvs[i]
		sc := currentMetricsStorageClassLabel(rv.Spec.ReplicatedStorageClassName)
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
			typeCounts[metricKey(sc, node, typ)]++
		}
	}

	for _, sc := range storageClasses {
		for _, node := range nodes {
			for _, typ := range types {
				ch <- prometheus.MustNewConstMetric(activeDesc, prometheus.GaugeValue, typeCounts[metricKey(sc, node, typ)], sc, node, typ)
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

func datameshTransitionTypes() []string {
	return []string{
		string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation),
		string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica),
		string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach),
		string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeQuorum),
		string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeReplicaType),
		string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeSystemNetworks),
		string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach),
		string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeDisableMultiattach),
		string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeEnableMultiattach),
		string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeForceDetach),
		string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeForceRemoveReplica),
		string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveReplica),
		string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeRepairNetworkAddresses),
		string(v1alpha1.ReplicatedVolumeDatameshTransitionTypeResizeVolume),
	}
}

func currentMetricsNodeLabel(node string) string {
	if node == "" {
		return currentMetricsNodeUnknown
	}
	return node
}

func currentMetricsStorageClassLabel(storageClass string) string {
	if storageClass == "" {
		return currentMetricsSCUnknown
	}
	return storageClass
}

func prependMissingCurrentMetricsNodes(nodes []string, prefixes ...string) []string {
	seen := make(map[string]struct{}, len(nodes)+len(prefixes))
	for _, node := range nodes {
		seen[node] = struct{}{}
	}

	result := make([]string, 0, len(nodes)+len(prefixes))
	for _, node := range prefixes {
		if _, exists := seen[node]; exists {
			continue
		}
		seen[node] = struct{}{}
		result = append(result, node)
	}
	return append(result, nodes...)
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
