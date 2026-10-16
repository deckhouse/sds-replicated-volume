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

// Histogram bucket configurations.
// Agent runs one instance per node, so no node label is needed on histograms.
var (
	// Same 1-2-5 scale as the controller histograms, calibrated on production distributions and
	// reaching up to an hour to separate slow from stuck. Bands panels on the dashboard use bucket
	// boundaries as thresholds.
	bucketsDRBDRConfigured = []float64{0.5, 1, 2, 5, 10, 30, 60, 120, 300, 600, 3600}
	bucketsDeletion        = []float64{0.5, 1, 2, 5, 10, 30, 60, 120, 300, 600, 1800, 3600}
)

// ──────────────────────────────────────────────────────────────────────────────
// Lifecycle histograms
//

var (
	DRBDRConfiguredDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "sds_drbdr_configured_duration_seconds",
		Help:    "Time from DRBDResource creation to its first Configured=True. Later returns to True are not observed, nor are resources created before the agent started.",
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
// Port allocation
//

// DRBDPortRangeUsage is sampled on every allocation attempt, so it holds its
// last value while no allocation happens on the IP. It is a warning signal, and
// erring towards a stale-high value is the safe direction for that.
var DRBDPortRangeUsage = prometheus.NewGaugeVec(prometheus.GaugeOpts{
	Name: "sds_drbd_port_range_usage_ratio",
	Help: "Share of the configured DRBD port range (DRBD_MIN_PORT..DRBD_MAX_PORT) in use on a local IP, as of the last port allocation attempt for that IP.",
}, []string{"ip"})

func init() {
	crmetrics.Registry.MustRegister(
		DRBDRConfiguredDuration,
		DRBDRDeletionDuration,
		DRBDPortRangeUsage,
		newDRBDModuleInfoCollector(drbdModuleVersionPath),
	)
}
