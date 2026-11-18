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

// Package nodegrace is the single place that decides whether a node's NotReady
// grace period has expired. The RSP controller uses it for eligibleNodes;
// RV evacuation must use the same decision to keep both controllers consistent.
package nodegrace

import (
	"time"

	corev1 "k8s.io/api/core/v1"
	nodeutil "k8s.io/component-helpers/node/util"
)

// ReadyState is a snapshot of the Node Ready condition needed to check its grace period.
type ReadyState struct {
	Status             bool      // True if the Ready condition is True.
	HasCondition       bool      // True if the Ready condition exists.
	LastTransitionTime time.Time // Time of the last Ready condition transition.
}

// NewReadyState copies only scalar values from node without modifying it.
// It is safe to pass a cached Node without DeepCopy.
func NewReadyState(node *corev1.Node) ReadyState {
	_, readyCond := nodeutil.GetNodeCondition(&node.Status, corev1.NodeReady)
	if readyCond == nil {
		return ReadyState{}
	}

	return ReadyState{
		HasCondition:       true,
		Status:             readyCond.Status == corev1.ConditionTrue,
		LastTransitionTime: readyCond.LastTransitionTime.Time,
	}
}

// IsReadyOrWithinGrace checks node readiness and grace period status at now.
// A missing Ready condition is treated as not ready but within grace.
// The grace period expires only strictly after LastTransitionTime + gracePeriod.
// graceExpiresAt is zero if the Ready condition is missing, true, or beyond grace.
func IsReadyOrWithinGrace(state ReadyState, gracePeriod time.Duration, now time.Time) (nodeReady bool, notReadyBeyondGrace bool, graceExpiresAt time.Time) {
	if !state.HasCondition {
		return false, false, time.Time{}
	}

	if state.Status {
		return true, false, time.Time{}
	}

	graceExpiresAt = state.LastTransitionTime.Add(gracePeriod)
	if now.After(graceExpiresAt) {
		return false, true, time.Time{}
	}

	return false, false, graceExpiresAt
}
