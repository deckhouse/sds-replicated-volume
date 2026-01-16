/*
Copyright 2025 Flant JSC

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

// CheckerStats holds statistics about condition transitions for a ReplicatedVolume.
// Even number of transitions means RV maintains desired state despite disruption attempts.
// Odd number means disruption attempts succeeded.
// Ideal: all counters at zero.
type CheckerStats struct {
	RVName             string
	IOReadyTransitions atomic.Int64
	QuorumTransitions  atomic.Int64
}

// conditionState tracks the current state of monitored conditions
type conditionState struct {
	ioReadyStatus metav1.ConditionStatus
	quorumStatus  metav1.ConditionStatus
}

// VolumeChecker watches a ReplicatedVolume and logs state changes.
// It monitors IOReady and Quorum conditions and counts transitions.
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
	state  conditionState

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
		state: conditionState{
			// Initial expected state: both conditions should be True
			ioReadyStatus: metav1.ConditionTrue,
			quorumStatus:  metav1.ConditionTrue,
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
// Uses processRVUpdate to detect changes from initial True state.
func (v *VolumeChecker) checkInitialState(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}

	// Try to get from cache first, fall back to API with timeout
	rv, err := v.client.GetRVFromCache(v.rvName)
	if err != nil {
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

	// Reuse processRVUpdate - it will detect and log changes from initial True state
	// (v.state is initialized as {True, True}). If state is OK, nothing is logged.
	v.processRVUpdate(ctx, rv)
}

// processRVUpdate checks for condition changes and logs them
func (v *VolumeChecker) processRVUpdate(ctx context.Context, rv *v1alpha1.ReplicatedVolume) {
	if rv == nil {
		v.log.Debug("RV is nil, skipping condition check")
		return
	}

	// Status is a struct in the API, but Conditions can be empty (e.g. just created / during deletion).
	if len(rv.Status.Conditions) == 0 {
		v.log.Debug("RV has no conditions yet, skipping condition check")
		return
	}

	newIOReadyStatus := getConditionStatus(rv.Status.Conditions, v1alpha1.ReplicatedVolumeCondIOReadyType)
	newQuorumStatus := getConditionStatus(rv.Status.Conditions, v1alpha1.ReplicatedVolumeCondQuorumType)

	// Check IOReady transition.
	// v.state stores previous status (default: True = expected healthy state).
	// If new status differs from saved → log + count transition + update saved state.
	if newIOReadyStatus != v.state.ioReadyStatus {
		oldStatus := v.state.ioReadyStatus       // Save old for logging
		v.stats.IOReadyTransitions.Add(1)        // Count transition for final stats
		v.state.ioReadyStatus = newIOReadyStatus // Update saved state

		v.log.Warn("condition changed",
			"condition", v1alpha1.ReplicatedVolumeCondIOReadyType,
			"transition", string(oldStatus)+"->"+string(newIOReadyStatus))

		// On False: log failed RVRs for debugging
		if newIOReadyStatus == metav1.ConditionFalse {
			reason := getConditionReason(rv.Status.Conditions, v1alpha1.ReplicatedVolumeCondIOReadyType)
			message := getConditionMessage(rv.Status.Conditions, v1alpha1.ReplicatedVolumeCondIOReadyType)
			v.logConditionDetails(ctx, v1alpha1.ReplicatedVolumeCondIOReadyType, reason, message)
		} // FYI: we can make here else block, if we need some details then conditions going from Fase to True
	}

	// Check Quorum transition (same logic as IOReady).
	if newQuorumStatus != v.state.quorumStatus {
		oldStatus := v.state.quorumStatus      // Save old for logging
		v.stats.QuorumTransitions.Add(1)       // Count transition for final stats
		v.state.quorumStatus = newQuorumStatus // Update saved state

		v.log.Warn("condition changed",
			"condition", v1alpha1.ReplicatedVolumeCondQuorumType,
			"transition", string(oldStatus)+"->"+string(newQuorumStatus))

		// Log RVRs only if IOReady didn't just log them (avoid duplicate output)
		if newQuorumStatus == metav1.ConditionFalse && v.state.ioReadyStatus != metav1.ConditionFalse {
			reason := getConditionReason(rv.Status.Conditions, v1alpha1.ReplicatedVolumeCondQuorumType)
			message := getConditionMessage(rv.Status.Conditions, v1alpha1.ReplicatedVolumeCondQuorumType)
			v.logConditionDetails(ctx, v1alpha1.ReplicatedVolumeCondQuorumType, reason, message)
		} // FYI: we can make here else block, if we need some details then conditions going from Fase to True
	}
}

// logConditionDetails logs condition details with failed RVRs listing.
// Uses structured logging with rv_name from logger context.
// RVR table is included in "failed_rvrs_details" field when there are failures.
func (v *VolumeChecker) logConditionDetails(ctx context.Context, condType, reason, message string) {
	// Check if context is already done - skip RVR listing
	if ctx.Err() != nil {
		v.log.Warn("condition details (context cancelled, skipped RVR listing)",
			"condition", condType,
			"reason", reason,
			"message", message)
		return
	}

	// Use timeout for API call
	callCtx, cancel := context.WithTimeout(ctx, apiCallTimeout)
	defer cancel()

	rvrs, err := v.client.ListRVRsByRVName(callCtx, v.rvName)
	if err != nil {
		v.log.Warn("condition details",
			"condition", condType,
			"reason", reason,
			"message", message,
			"list_rvrs_error", err.Error())
		return
	}

	// Find failed RVRs (those with at least one False condition)
	var failedRVRs []v1alpha1.ReplicatedVolumeReplica
	for _, rvr := range rvrs {
		if hasAnyFalseCondition(rvr.Status) {
			failedRVRs = append(failedRVRs, rvr)
		}
	}

	if len(failedRVRs) == 0 {
		v.log.Warn("condition details",
			"condition", condType,
			"reason", reason,
			"message", message,
			"failed_rvrs", 0)
		return
	}

	// Build RVR details table
	var sb strings.Builder
	for _, rvr := range failedRVRs {
		sb.WriteString(buildRVRConditionsTable(&rvr))
	}

	v.log.Warn("condition details",
		"condition", condType,
		"reason", reason,
		"message", message,
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
//
// Example:
//
//	RVR: test-rv-1-abc (node: worker-1, type: Diskful)
//	  - Ready: False | StoragePoolUnavailable | Pool xyz not found
//	  - Synchronized: True | InSync
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

// Helper functions to extract condition fields

func getConditionStatus(conditions []metav1.Condition, condType string) metav1.ConditionStatus {
	for _, cond := range conditions {
		if cond.Type == condType {
			return cond.Status
		}
	}
	return metav1.ConditionUnknown
}

func getConditionReason(conditions []metav1.Condition, condType string) string {
	for _, cond := range conditions {
		if cond.Type == condType {
			return cond.Reason
		}
	}
	return ""
}

func getConditionMessage(conditions []metav1.Condition, condType string) string {
	for _, cond := range conditions {
		if cond.Type == condType {
			return cond.Message
		}
	}
	return ""
}
