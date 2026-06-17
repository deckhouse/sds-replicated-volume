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
// Transition counters count health changes between known healthy/unhealthy states.
// Final health is reported separately via LastFTTStatus and LastGMDRStatus.
type CheckerStats struct {
	RVName          string
	FTTTransitions  atomic.Int64
	GMDRTransitions atomic.Int64
	LastFTTStatus   atomic.Int32
	LastGMDRStatus  atomic.Int32
}

// healthState tracks the current EffectiveLayout-derived health (FTT and GMDR).
type healthState struct {
	ftt  healthStatus
	gmdr healthStatus
}

type healthStatus int32

const (
	healthStatusUnknown healthStatus = iota
	healthStatusHealthy
	healthStatusUnhealthy
)

func (s healthStatus) String() string {
	switch s {
	case healthStatusHealthy:
		return "healthy"
	case healthStatusUnhealthy:
		return "unhealthy"
	default:
		return "unknown"
	}
}

func HealthStatusString(value int32) string {
	return healthStatus(value).String()
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
	stats.LastFTTStatus.Store(int32(healthStatusHealthy))
	stats.LastGMDRStatus.Store(int32(healthStatusHealthy))

	return &VolumeChecker{
		rvName: rvName,
		client: client,
		log:    slog.Default().With("runner", "volume-checker", "rv_name", rvName),
		stats:  stats,
		state: healthState{
			ftt:  healthStatusHealthy,
			gmdr: healthStatusHealthy,
		},
		updateCh: make(chan *v1alpha1.ReplicatedVolume, updateChBufferSize),
	}
}

// Run starts watching the RV until context is cancelled.
func (v *VolumeChecker) Run(ctx context.Context) error {
	v.log.Info("started")
	defer v.log.Info("finished")

	if err := v.register(ctx); err != nil {
		return err
	}
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
			v.processRVUpdate(rv)
		}
	}
}

// register adds this checker to the dispatcher.
// Dispatcher will route RV events matching our name to updateCh.
func (v *VolumeChecker) register(ctx context.Context) error {
	for {
		if err := v.client.RegisterRVChecker(v.rvName, v.updateCh, ctx.Done()); err != nil {
			v.log.Warn("failed to register RV checker, retrying", "error", err)
			if waitErr := waitWithContext(ctx, 500*time.Millisecond); waitErr != nil {
				return waitErr
			}
			continue
		}
		return nil
	}
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
	v.processRVUpdate(rv)
}

// processRVUpdate checks EffectiveLayout (FTT and GMDR) and logs transitions.
func (v *VolumeChecker) processRVUpdate(rv *v1alpha1.ReplicatedVolume) {
	if rv == nil {
		v.log.Debug("RV is nil, skipping health check")
		return
	}

	el := rv.Status.EffectiveLayout

	newFTT := healthStatusFromPointer(el.FailuresToTolerate)
	newGMDR := healthStatusFromPointer(el.GuaranteedMinimumDataRedundancy)

	v.processHealthStatus("FTT", newFTT, &v.state.ftt, &v.stats.FTTTransitions, &v.stats.LastFTTStatus, el.FailuresToTolerate, el)
	v.processHealthStatus("GMDR", newGMDR, &v.state.gmdr, &v.stats.GMDRTransitions, &v.stats.LastGMDRStatus, el.GuaranteedMinimumDataRedundancy, el)
}

func (v *VolumeChecker) processHealthStatus(
	metric string,
	newStatus healthStatus,
	current *healthStatus,
	transitions *atomic.Int64,
	lastStatus *atomic.Int32,
	value *int8,
	el v1alpha1.ReplicatedVolumeEffectiveLayout,
) {
	if newStatus == *current {
		return
	}

	oldStatus := *current
	*current = newStatus
	lastStatus.Store(int32(newStatus))

	valueText := "nil"
	if value != nil {
		valueText = fmt.Sprintf("%d", *value)
	}

	v.log.Warn(metric+" health changed",
		"transition", oldStatus.String()+"->"+newStatus.String(),
		"value", valueText,
		"message", el.Message)

	if oldStatus != healthStatusUnknown && newStatus != healthStatusUnknown {
		transitions.Add(1)
	}
	if newStatus == healthStatusUnhealthy {
		v.logHealthDetails(metric, el)
	}
}

func healthStatusFromPointer(value *int8) healthStatus {
	if value == nil {
		return healthStatusUnknown
	}
	if *value >= 0 {
		return healthStatusHealthy
	}
	return healthStatusUnhealthy
}

// logHealthDetails logs EffectiveLayout summary and failed RVRs when health is unhealthy.
func (v *VolumeChecker) logHealthDetails(metric string, el v1alpha1.ReplicatedVolumeEffectiveLayout) {
	rvrs, err := v.client.ListRVRsByRVName(v.rvName)
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
