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
	"time"

	"github.com/prometheus/client_golang/prometheus"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// ControllerStartTime is captured once at package init.
// Used as fallback startTime for phase duration tracking after controller restart:
// existing RVRs that are already in a phase get this time as their phase start,
// so that deadlock detection works immediately after restart.
var ControllerStartTime = time.Now()

// Metric label name constants.
const (
	LabelName              = "name"
	LabelRV                = "rv"
	LabelNode              = "node"
	LabelStorageClass      = "storage_class"
	LabelConfigurationMode = "configuration_mode"
	LabelPhase             = "phase"
	LabelType              = "type"
	LabelStep              = "step"
	LabelReason            = "reason"
	LabelCondition         = "condition"
	LabelStatus            = "status"
	LabelKind              = "kind"
)

// ReasonAbsent is the synthetic reason label value used when a condition is missing from
// the object status entirely. Real condition reasons never take this value.
const ReasonAbsent = "Absent"

// Histogram bucket configurations.
// Prometheus automatically adds +Inf, so data is never lost even if values exceed the upper bound.
//
// Buckets follow a 1-2-5 scale calibrated on production distributions (3k RVs, 20 nodes) and
// reach up to an hour, so a slow operation can be told apart from a stuck one. Finer steps are
// not worth the extra series: operators need the order of magnitude, the exact mean is available
// from _sum/_count. Bands panels on the dashboard use bucket boundaries as thresholds; keep them
// in sync when changing buckets. Datamesh transitions carry the most label combinations, so they
// get the fewest buckets.
var (
	bucketsRVAAttach          = []float64{1, 2, 5, 10, 20, 30, 60, 120, 300, 600, 3600}
	bucketsDeletion           = []float64{0.5, 1, 2, 5, 10, 30, 60, 120, 300, 600, 1800, 3600}
	bucketsDatameshTransition = []float64{0.5, 1, 2, 5, 10, 30, 60, 300, 3600}
	bucketsDatameshStep       = []float64{0.5, 1, 2, 5, 10, 30, 120, 600}
	bucketsRVFormation        = []float64{1, 2, 5, 10, 20, 30, 60, 120, 300, 600}
	bucketsBackingVolume      = []float64{0.5, 1, 2, 5, 10, 20, 30, 60, 120, 300, 600, 3600}
	bucketsCollect            = []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
)

// ──────────────────────────────────────────────────────────────────────────────
// Group 1: Lifecycle histograms (low cardinality, observed once per event)
//

var (
	// RVCreated is a counter rather than a histogram: creation is a point event with no duration.
	RVCreated = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "sds_rv_created_total",
		Help: "Number of ReplicatedVolumes taken over by the controller, counted once per volume when RVControllerFinalizer is first added. The finalizer survives controller restarts and leader changes, so a volume is not counted again. Manual volumes have storage_class=\"unknown\" and configuration_mode=\"Manual\".",
	}, []string{LabelStorageClass, LabelConfigurationMode})

	RVInitialFormationDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "sds_rv_initial_formation_duration_seconds",
		Help:    "Time from RV creation to first completed initial datamesh formation (datameshRevision > 0 and no active Formation transition).",
		Buckets: bucketsRVFormation,
	}, []string{LabelStorageClass})

	RVAReadyDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "sds_rva_ready_duration_seconds",
		Help:    "Time from RVA creation to its first Attached phase; re-attaches are not observed, except a re-attach that completes after a controller restart while the attachment was lost: it is observed as a first attach, measured from creation.",
		Buckets: bucketsRVAAttach,
	}, []string{LabelNode})

	// RVRReadyLost is a counter rather than a histogram: how often replicas lose Ready (flaps)
	// matters per node, while how long they stay not Ready is covered by current metrics.
	RVRReadyLost = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "sds_rvr_ready_lost_total",
		Help: "Number of RVR Ready condition transitions from True to non-True, by the new reason (\"Absent\" when the condition disappeared). Deleting replicas are not counted.",
	}, []string{LabelNode, LabelStorageClass, LabelReason})

	// RVRBackingVolumeDuration reflects LLV/LVMLogicalVolume provisioning time.
	RVRBackingVolumeDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "sds_rvr_backing_volume_duration_seconds",
		Help:    "Time from LVMLogicalVolume creation to BackingVolumeReady=True, observed once per provisioned LLV (initial provisioning or replacement). Returns to Ready after a resize or a temporary NotReady are not observed.",
		Buckets: bucketsBackingVolume,
	}, []string{LabelNode, LabelStorageClass})

	RVDeletionDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "sds_rv_deletion_duration_seconds",
		Help:    "Time from RV deletion start to finalizer removal.",
		Buckets: bucketsDeletion,
	}, []string{LabelStorageClass})

	RVRDeletionDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "sds_rvr_deletion_duration_seconds",
		Help:    "Time from RVR deletion start to finalizer removal.",
		Buckets: bucketsDeletion,
	}, []string{LabelNode, LabelStorageClass})

	RVADetachDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "sds_rva_detach_duration_seconds",
		Help:    "Time from RVA detach start (deletionTimestamp) to completion.",
		Buckets: bucketsDeletion,
	}, []string{LabelNode})
)

// ──────────────────────────────────────────────────────────────────────────────
// Group 2: Datamesh metrics (label-driven, auto-discover new types/steps)
//

var (
	DatameshTransitionDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "sds_rv_datamesh_transition_duration_seconds",
		Help:    "Duration of completed datamesh transitions.",
		Buckets: bucketsDatameshTransition,
	}, []string{LabelStorageClass, LabelNode, LabelType})

	DatameshStepDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "sds_rv_datamesh_step_duration_seconds",
		Help:    "Duration of individual datamesh transition steps.",
		Buckets: bucketsDatameshStep,
	}, []string{LabelStorageClass, LabelNode, LabelType, LabelStep})
)

// ──────────────────────────────────────────────────────────────────────────────
// Group 3: Current metrics collector self-observability
//

var (
	CurrentMetricsCollectDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "sds_metrics_collector_collect_duration_seconds",
		Help:    "Duration of one SDS current metrics collector run.",
		Buckets: bucketsCollect,
	})

	CurrentMetricsCollectErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "sds_metrics_collector_collect_errors_total",
		Help: "Total number of failed SDS current metrics collector runs.",
	})
)

// StorageClassLabel returns the storage_class label value for a ReplicatedStorageClass name,
// mapping an empty name (a Manual volume) to "unknown". Every metric with a storage_class label
// uses it, so current and lifecycle metrics agree and can be filtered and joined together.
func StorageClassLabel(storageClass string) string {
	if storageClass == "" {
		return currentMetricsSCUnknown
	}
	return storageClass
}

// ConfigurationModeLabel returns the configuration_mode label value of a ReplicatedVolume,
// mapping an empty mode to Auto, the CRD default.
func ConfigurationModeLabel(mode v1alpha1.ReplicatedVolumeConfigurationMode) string {
	if mode == "" {
		return string(v1alpha1.ReplicatedVolumeConfigurationModeAuto)
	}
	return string(mode)
}

func init() {
	crmetrics.Registry.MustRegister(
		// Lifecycle metrics
		RVCreated,
		RVInitialFormationDuration,
		RVAReadyDuration,
		RVRReadyLost,
		RVRBackingVolumeDuration,
		RVDeletionDuration,
		RVRDeletionDuration,
		RVADetachDuration,

		// Datamesh
		DatameshTransitionDuration,
		DatameshStepDuration,

		// Current metrics collector self-observability
		CurrentMetricsCollectDuration,
		CurrentMetricsCollectErrors,
	)
}
