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

package runners

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/megatest/internal/kubeutils"
)

const (
	// apiCallTimeout is the timeout for individual API calls to avoid hanging
	apiCallTimeout = 10 * time.Second

	// updateChBufferSize is the buffer size for RV update channel.
	// Provides headroom for burst updates while checker processes events.
	updateChBufferSize = 10
)

// CheckerStats holds statistics about EffectiveLayout health transitions for a ReplicatedVolume.
// FTT (FailuresToTolerate) reflects quorum health; GMDR (GuaranteedMinimumDataRedundancy) reflects IO/data health.
// Even number of transitions means the metric recovered; odd means it ended in unhealthy state.
// Ideal: all counters at zero.
type CheckerStats struct {
	RVName          string
	FTTTransitions  atomic.Int64
	GMDRTransitions atomic.Int64
}

// healthState tracks the current EffectiveLayout-derived health (FTT and GMDR).
type healthState struct {
	fttHealthy  bool // FailuresToTolerate != nil && *FailuresToTolerate >= 0
	gmdrHealthy bool // GuaranteedMinimumDataRedundancy != nil && *GuaranteedMinimumDataRedundancy >= 0
}

// VolumeChecker watches a ReplicatedVolume and logs state changes.
// It monitors EffectiveLayout.FTT (quorum health) and EffectiveLayout.GMDR (IO health) and counts transitions.
//
// Uses shared informer with dispatcher pattern:
//   - One informer handler for all checkers (not N handlers for N checkers)
//   - Events routed via map lookup O(1) instead of N filter calls
//   - Efficient for 100+ concurrent RV watchers
//   - Automatic reconnection on API disconnects via informer
//
// If registration fails, it retries until RV lifetime expires.
type VolumeChecker struct {
	rvName string
	client *kubeutils.Client
	log    *slog.Logger
	stats  *CheckerStats
	state  healthState

	// Channel for receiving RV updates (dispatcher sends here)
	updateCh chan *v1alpha1.ReplicatedVolume
}

// NewVolumeChecker creates a new VolumeChecker for the given RV
func NewVolumeChecker(rvName string, client *kubeutils.Client, stats *CheckerStats) *VolumeChecker {
	return &VolumeChecker{
		rvName: rvName,
		client: client,
		log:    slog.Default().With("runner", "volume-checker", "rv_name", rvName),
		stats:  stats,
		state: healthState{
			fttHealthy:  true,
			gmdrHealthy: true,
		},
		updateCh: make(chan *v1alpha1.ReplicatedVolume, updateChBufferSize),
	}
}

// Run starts watching the RV until context is cancelled.
func (v *VolumeChecker) Run(ctx context.Context) error {
	v.log.Info("started")
	defer v.log.Info("finished")

	// Registration always succeeds if app started (informer is ready after NewClient)
	v.register()
	defer v.unregister()

	// Check initial state
	v.checkInitialState(ctx)

	v.log.Debug("watching via shared informer dispatcher")

	// Process events from dispatcher
	for {
		select {
		case <-ctx.Done():
			return nil
		case rv := <-v.updateCh:
			v.processRVUpdate(ctx, rv)
		}
	}
}

// register adds this checker to the dispatcher.
// Dispatcher will route RV events matching our name to updateCh.
func (v *VolumeChecker) register() {
	// Error only possible if informer not ready, but it's always ready after NewClient()
	_ = v.client.RegisterRVChecker(v.rvName, v.updateCh)
}

// unregister removes this checker from the dispatcher.
func (v *VolumeChecker) unregister() {
	v.client.UnregisterRVChecker(v.rvName)
}

// checkInitialState checks current RV state and counts transition if not in expected state.
// Uses processRVUpdate to detect changes from initial healthy state.
func (v *VolumeChecker) checkInitialState(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}

	// Try to get from cache first, fall back to API with timeout
	rv, err := v.client.GetRVFromCache(v.rvName)
	if err != nil {
		v.log.Error("failed to get from cache", "error", err)
		return
	}
	if rv == nil {
		v.log.Debug("not in cache, fetching from API")

		callCtx, cancel := context.WithTimeout(ctx, apiCallTimeout)
		defer cancel()

		rv, err = v.client.GetRV(callCtx, v.rvName)
		if err != nil {
			if ctx.Err() != nil {
				return // Context cancelled, normal shutdown
			}
			v.log.Error("failed to get from API", "error", err)
			return
		}
	}

	// Reuse processRVUpdate - it will detect and log changes from initial healthy state
	v.processRVUpdate(ctx, rv)
}

// processRVUpdate checks EffectiveLayout (FTT and GMDR) and logs transitions.
func (v *VolumeChecker) processRVUpdate(ctx context.Context, rv *v1alpha1.ReplicatedVolume) {
	if rv == nil {
		v.log.Debug("RV is nil, skipping health check")
		return
	}

	el := rv.Status.EffectiveLayout

	newFTTHealthy := el.FailuresToTolerate != nil && *el.FailuresToTolerate >= 0
	newGMDRHealthy := el.GuaranteedMinimumDataRedundancy != nil && *el.GuaranteedMinimumDataRedundancy >= 0

	// FTT transition (quorum health)
	if newFTTHealthy != v.state.fttHealthy {
		oldHealthy := v.state.fttHealthy
		v.stats.FTTTransitions.Add(1)
		v.state.fttHealthy = newFTTHealthy

		fttVal := "nil"
		if el.FailuresToTolerate != nil {
			fttVal = fmt.Sprintf("%d", *el.FailuresToTolerate)
		}
		v.log.Warn("FTT health changed",
			"transition", fmtBool(oldHealthy)+"->"+fmtBool(newFTTHealthy),
			"ftt", fttVal,
			"message", el.Message)

		if !newFTTHealthy {
			v.logHealthDetails(ctx, "FTT", el)
		}
	}

	// GMDR transition (IO/data health)
	if newGMDRHealthy != v.state.gmdrHealthy {
		oldHealthy := v.state.gmdrHealthy
		v.stats.GMDRTransitions.Add(1)
		v.state.gmdrHealthy = newGMDRHealthy

		gmdrVal := "nil"
		if el.GuaranteedMinimumDataRedundancy != nil {
			gmdrVal = fmt.Sprintf("%d", *el.GuaranteedMinimumDataRedundancy)
		}
		v.log.Warn("GMDR health changed",
			"transition", fmtBool(oldHealthy)+"->"+fmtBool(newGMDRHealthy),
			"gmdr", gmdrVal,
			"message", el.Message)

		if !newGMDRHealthy {
			v.logHealthDetails(ctx, "GMDR", el)
		}
	}
}

func fmtBool(b bool) string {
	if b {
		return "healthy"
	}
	return "unhealthy"
}

// logHealthDetails logs EffectiveLayout summary and failed RVRs when health is unhealthy.
func (v *VolumeChecker) logHealthDetails(ctx context.Context, metric string, el v1alpha1.ReplicatedVolumeEffectiveLayout) {
	if ctx.Err() != nil {
		v.log.Warn("health details (context cancelled, skipped RVR listing)",
			"metric", metric,
			"message", el.Message)
		return
	}

	callCtx, cancel := context.WithTimeout(ctx, apiCallTimeout)
	defer cancel()

	rvrs, err := v.client.ListRVRsByRVName(callCtx, v.rvName)
	if err != nil {
		v.log.Warn("health details",
			"metric", metric,
			"message", el.Message,
			"list_rvrs_error", err.Error())
		return
	}

	var failedRVRs []v1alpha1.ReplicatedVolumeReplica
	for _, rvr := range rvrs {
		if hasAnyFalseCondition(rvr.Status) {
			failedRVRs = append(failedRVRs, rvr)
		}
	}

	if len(failedRVRs) == 0 {
		v.log.Warn("health details",
			"metric", metric,
			"message", el.Message,
			"failed_rvrs", 0)
		return
	}

	var sb strings.Builder
	for _, rvr := range failedRVRs {
		sb.WriteString(buildRVRConditionsTable(&rvr))
	}

	v.log.Warn("health details",
		"metric", metric,
		"message", el.Message,
		"failed_rvrs", len(failedRVRs),
		"failed_rvrs_details", "\n"+sb.String())
}

// hasAnyFalseCondition checks if RVR has at least one condition with False status
func hasAnyFalseCondition(status v1alpha1.ReplicatedVolumeReplicaStatus) bool {
	for _, cond := range status.Conditions {
		if cond.Status == metav1.ConditionFalse {
			return true
		}
	}
	return false
}

// buildRVRConditionsTable builds a formatted table of all conditions for an RVR.
// Format:
//
//	RVR: <name> (node: <node>, type: <type>)
//	  - <condition>: <status> | <reason> | <message>
func buildRVRConditionsTable(rvr *v1alpha1.ReplicatedVolumeReplica) string {
	var sb strings.Builder
	sb.WriteString("    RVR: ")
	sb.WriteString(rvr.Name)
	sb.WriteString(" (node: ")
	sb.WriteString(rvr.Spec.NodeName)
	sb.WriteString(", type: ")
	sb.WriteString(string(rvr.Spec.Type))
	sb.WriteString(")\n")

	if len(rvr.Status.Conditions) == 0 {
		sb.WriteString("      (no status conditions available)\n")
		return sb.String()
	}

	for _, cond := range rvr.Status.Conditions {
		sb.WriteString("      - ")
		sb.WriteString(cond.Type)
		sb.WriteString(": ")
		sb.WriteString(string(cond.Status))
		sb.WriteString(" | ")
		sb.WriteString(cond.Reason)
		if cond.Message != "" {
			sb.WriteString(" | ")
			// Truncate message if too long
			msg := cond.Message
			if len(msg) > 60 {
				msg = msg[:57] + "..."
			}
			sb.WriteString(msg)
		}
		sb.WriteString("\n")
	}

	return sb.String()
}
