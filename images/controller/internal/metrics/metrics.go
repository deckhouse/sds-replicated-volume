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
)

// ControllerStartTime is captured once at package init.
// Used as fallback startTime for phase duration tracking after controller restart:
// existing RVRs that are already in a phase get this time as their phase start,
// so that deadlock detection works immediately after restart.
var ControllerStartTime = time.Now()

// Metric label name constants.
const (
	LabelName         = "name"
	LabelRV           = "rv"
	LabelNode         = "node"
	LabelStorageClass = "storage_class"
	LabelPhase        = "phase"
	LabelType         = "type"
	LabelStep         = "step"
)

// Histogram bucket configurations.
// Calibrated on a working cluster (5Gi, 3 replicas, replicated-r3, 3 test runs).
// Prometheus automatically adds +Inf, so data is never lost even if values exceed the upper bound.
var (
	bucketsRVRReady      = []float64{1, 2, 5, 10, 15, 30, 60, 120}
	bucketsRVAAttach     = []float64{1, 2, 5, 10, 30, 60}
	bucketsDeletion      = []float64{1, 5, 10, 15, 30, 60, 120}
	bucketsDatamesh      = []float64{0.5, 1, 5, 10, 30, 60, 120, 300}
	bucketsBackingVolume = []float64{0.5, 1, 2, 5, 10, 30}
	bucketsCollect       = []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1}
)

// ──────────────────────────────────────────────────────────────────────────────
// Group 1: Lifecycle histograms (low cardinality, observed once per event)
//

var (
	RVAReadyDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "sds_rva_ready_duration_seconds",
		Help:    "Time from RVA creation to Attached phase.",
		Buckets: bucketsRVAAttach,
	}, []string{LabelNode})

	RVRReadyDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "sds_rvr_ready_transition_duration_seconds",
		Help:    "Duration of every RVR Ready condition transition from non-True to True, including repeated recovery/flap transitions.",
		Buckets: bucketsRVRReady,
	}, []string{LabelNode, LabelStorageClass})

	// RVRBackingVolumeDuration reflects LLV/LVMLogicalVolume provisioning time.
	RVRBackingVolumeDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "sds_rvr_backing_volume_duration_seconds",
		Help:    "Time from RVR creation to BackingVolumeReady condition (reflects LLV/LVMLogicalVolume provisioning time).",
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
		Buckets: bucketsDatamesh,
	}, []string{LabelStorageClass, LabelNode, LabelType})

	DatameshTransitionsCompleted = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "sds_rv_datamesh_transitions_completed_total",
		Help: "Total number of completed datamesh transitions.",
	}, []string{LabelStorageClass, LabelNode, LabelType})

	DatameshStepDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "sds_rv_datamesh_step_duration_seconds",
		Help:    "Duration of individual datamesh transition steps.",
		Buckets: bucketsDatamesh,
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

	CurrentMetricsObjects = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "sds_metrics_collector_objects",
		Help: "Number of Kubernetes objects processed by the SDS current metrics collector by kind.",
	}, []string{"kind"})
)

func init() {
	crmetrics.Registry.MustRegister(
		// Lifecycle histograms
		RVAReadyDuration,
		RVRReadyDuration,
		RVRBackingVolumeDuration,
		RVDeletionDuration,
		RVRDeletionDuration,
		RVADetachDuration,

		// Datamesh
		DatameshTransitionDuration,
		DatameshTransitionsCompleted,
		DatameshStepDuration,

		// Current metrics collector self-observability
		CurrentMetricsCollectDuration,
		CurrentMetricsCollectErrors,
		CurrentMetricsObjects,
	)
}
