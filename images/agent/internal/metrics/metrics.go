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
	"github.com/prometheus/client_golang/prometheus"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

// Metric label name constants.
const (
	LabelName = "name"
	LabelRV   = "rv"
	LabelType = "type"
)

// Histogram bucket configurations.
// Agent runs one instance per node, so no node label is needed on histograms.
var (
	bucketsDRBDRConfigured = []float64{0.5, 1, 2, 5, 10, 30}
	bucketsDeletion        = []float64{1, 5, 10, 15, 30, 60, 120}
)

// ──────────────────────────────────────────────────────────────────────────────
// Lifecycle histograms
//

var (
	DRBDRConfiguredDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "sds_drbdr_configured_duration_seconds",
		Help:    "Time from DRBDResource creation to Configured condition becoming True.",
		Buckets: bucketsDRBDRConfigured,
	})

	// DRBDRDeletionDuration isolates DRBD cleanup time from the overall RVR deletion.
	DRBDRDeletionDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "sds_drbdr_deletion_duration_seconds",
		Help:    "Time from DRBDResource deletion start (deletionTimestamp) to finalizer removal. Isolates DRBD cleanup time.",
		Buckets: bucketsDeletion,
	})
)

// ──────────────────────────────────────────────────────────────────────────────
// Health gauges
//

var (
	// DRBDRCondition reports 1=True, 0=False/Unknown for each condition type.
	// Currently only "Configured" condition exists; new conditions will be auto-discovered.
	DRBDRCondition = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "sds_drbdr_condition",
		Help: "Status of DRBDResource conditions (1=True, 0=False/Unknown).",
	}, []string{LabelName, LabelRV, LabelType})
)

func init() {
	crmetrics.Registry.MustRegister(
		DRBDRConfiguredDuration,
		DRBDRDeletionDuration,
		DRBDRCondition,
	)
}
