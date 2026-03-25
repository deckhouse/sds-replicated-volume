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
	bucketsRVRPhase      = []float64{0.5, 1, 2, 5, 10, 30, 60}
	bucketsRVAAttach     = []float64{1, 2, 5, 10, 30, 60}
	bucketsRVReady       = []float64{5, 10, 15, 30, 60, 120, 300, 600}
	bucketsDeletion      = []float64{1, 5, 10, 15, 30, 60, 120}
	bucketsDatamesh      = []float64{0.5, 1, 5, 10, 30, 60, 120, 300}
	bucketsBackingVolume = []float64{0.5, 1, 2, 5, 10, 30}
)

// ──────────────────────────────────────────────────────────────────────────────
// Group 1: Lifecycle histograms (low cardinality, observed once per event)
//

var (
	// RVConditionReadyDuration tracks time from RV creation to IOReady=True.
	// RV IOReady condition is not yet populated by the controller.
	// This metric will start working when the IOReady condition is implemented.
	RVConditionReadyDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "sds_rv_condition_ready_duration_seconds",
		Help:    "Time from RV creation to IOReady condition becoming True. Currently not populated; will work when IOReady condition is implemented.",
		Buckets: bucketsRVReady,
	}, []string{LabelStorageClass})

	RVAReadyDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "sds_rva_ready_duration_seconds",
		Help:    "Time from RVA creation to Attached phase.",
		Buckets: bucketsRVAAttach,
	}, []string{LabelNode})

	RVRReadyDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "sds_rvr_ready_duration_seconds",
		Help:    "Time from RVR creation to Healthy phase.",
		Buckets: bucketsRVRReady,
	}, []string{LabelNode, LabelStorageClass})

	// RVRPhaseDuration is observed on every phase change (including oscillations).
	RVRPhaseDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "sds_rvr_phase_duration_seconds",
		Help:    "Duration spent in each RVR phase. Provisioning phase reflects LLV/LVMLogicalVolume provisioning wait.",
		Buckets: bucketsRVRPhase,
	}, []string{LabelNode, LabelStorageClass, LabelPhase})

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
// Group 2: Per-object duration gauges (high cardinality, diagnostic drill-down)
//

var (
	// RVRCreationDuration is set once when RVR first reaches Healthy.
	// Guard: if gauge > 0, creation was already observed (prevents double-counting on oscillation).
	RVRCreationDuration = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "sds_rvr_creation_duration_seconds",
		Help: "Per-object time from RVR creation to first Healthy phase. Set once per object lifetime.",
	}, []string{LabelName, LabelRV, LabelNode, LabelStorageClass})

	// RVACreationDuration is set once when RVA first reaches Attached.
	RVACreationDuration = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "sds_rva_creation_duration_seconds",
		Help: "Per-object time from RVA creation to first Attached phase. Set once per object lifetime.",
	}, []string{LabelName, LabelRV, LabelNode})

	// RVCreatedTimestamp allows PromQL filtering by age (e.g., "RVs older than 5 minutes").
	RVCreatedTimestamp = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "sds_rv_created_timestamp_seconds",
		Help: "Creation timestamp of the ReplicatedVolume (Unix seconds). For filtering by age.",
	}, []string{LabelName, LabelStorageClass})

	// RVRCurrentPhaseStart enables deadlock detection: time() - gauge > threshold.
	RVRCurrentPhaseStart = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "sds_rvr_current_phase_start_seconds",
		Help: "Unix timestamp when RVR entered its current phase. For deadlock detection: time() - gauge > threshold.",
	}, []string{LabelName, LabelRV, LabelNode, LabelPhase})
)

// ──────────────────────────────────────────────────────────────────────────────
// Group 3: Datamesh metrics (label-driven, auto-discover new types/steps)
//

var (
	DatameshTransitionDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "sds_rv_datamesh_transition_duration_seconds",
		Help:    "Duration of completed datamesh transitions.",
		Buckets: bucketsDatamesh,
	}, []string{LabelType})

	DatameshTransitionsCompleted = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "sds_rv_datamesh_transitions_completed_total",
		Help: "Total number of completed datamesh transitions.",
	}, []string{LabelType})

	DatameshActiveTransitions = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "sds_rv_datamesh_active_transitions",
		Help: "Number of currently active datamesh transitions per RV.",
	}, []string{LabelRV, LabelType})

	DatameshStepDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "sds_rv_datamesh_step_duration_seconds",
		Help:    "Duration of individual datamesh transition steps.",
		Buckets: bucketsDatamesh,
	}, []string{LabelType, LabelStep})
)

// ──────────────────────────────────────────────────────────────────────────────
// Group 4: Health gauges (per-object state, for aggregation in PromQL)
//

var (
	// RVCondition reports 1=True, 0=False/Unknown for each condition type.
	RVCondition = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "sds_rv_condition",
		Help: "Status of RV conditions (1=True, 0=False/Unknown).",
	}, []string{LabelName, LabelStorageClass, LabelType})

	// RVRPhaseInfo reports 1 for the RVR's current phase.
	RVRPhaseInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "sds_rvr_phase_info",
		Help: "Current phase of each RVR (1 for active phase).",
	}, []string{LabelName, LabelRV, LabelNode, LabelPhase})

	// RVAPhaseInfo reports 1 for the RVA's current phase.
	RVAPhaseInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "sds_rva_phase_info",
		Help: "Current phase of each RVA (1 for active phase).",
	}, []string{LabelName, LabelRV, LabelNode, LabelPhase})
)

func init() {
	crmetrics.Registry.MustRegister(
		// Lifecycle histograms
		RVConditionReadyDuration,
		RVAReadyDuration,
		RVRReadyDuration,
		RVRPhaseDuration,
		RVRBackingVolumeDuration,
		RVDeletionDuration,
		RVRDeletionDuration,
		RVADetachDuration,

		// Per-object gauges
		RVRCreationDuration,
		RVACreationDuration,
		RVCreatedTimestamp,
		RVRCurrentPhaseStart,

		// Datamesh
		DatameshTransitionDuration,
		DatameshTransitionsCompleted,
		DatameshActiveTransitions,
		DatameshStepDuration,

		// Health gauges
		RVCondition,
		RVRPhaseInfo,
		RVAPhaseInfo,
	)
}
