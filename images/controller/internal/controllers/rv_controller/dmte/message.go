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

package dmte

import (
	"strconv"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// ──────────────────────────────────────────────────────────────────────────────
// generateProgressMessage
//

// generateProgressMessage builds a human-readable raw progress message from
// confirmation sets and optional diagnostic error reporting.
//
// The raw progress is written to transition.Steps[].Message (for kubectl/debug).
// The engine then composes the final status message via composeProgressMessage
// (prepending plan name and step info).
//
// Diagnostic config (conditionTypes, skipError) comes from the step's
// DiagnosticConditions / DiagnosticSkipError DSL settings. When conditionTypes
// is non-empty, unconfirmed replicas are checked for error conditions via
// rctx.Conditions() and errors are appended to the message.
//
// Examples:
//   - "4/4 replicas confirmed revision 7"
//   - "3/4 replicas confirmed revision 7. Waiting: [#2]"
//   - "2/4 replicas confirmed revision 7. Waiting: [#2, #5]. Errors: #2 DRBDConfigured/SomeReason: message | #5 Replica not found"
func generateProgressMessage[G any, R ReplicaCtx, C ContextProvider[G, R]](
	cp C,
	stepRevision int64,
	cr ConfirmResult,
	conditionTypes []string,
	skipError SkipErrorFunc[R],
) string {
	waiting := cr.MustConfirm.Difference(cr.Confirmed)

	var msg strings.Builder
	msg.Grow(128)

	msg.WriteString(strconv.Itoa(cr.Confirmed.Len()))
	msg.WriteByte('/')
	msg.WriteString(strconv.Itoa(cr.MustConfirm.Len()))
	msg.WriteString(" replicas confirmed revision ")
	msg.WriteString(strconv.FormatInt(stepRevision, 10))

	if waiting.IsEmpty() {
		return msg.String()
	}

	msg.WriteString(". Waiting: [")
	msg.WriteString(waiting.String())
	msg.WriteByte(']')

	if len(conditionTypes) == 0 {
		return msg.String()
	}

	// Check diagnostic conditions on unconfirmed replicas.
	var zeroRCtx R
	errorGroups := 0
	for id := range waiting.All() {
		rctx := cp.Replica(id)
		if rctx == zeroRCtx || !rctx.Exists() {
			if errorGroups == 0 {
				msg.WriteString(". Errors: ")
			} else {
				msg.WriteString(" | ")
			}
			msg.WriteByte('#')
			msg.WriteString(strconv.Itoa(int(id)))
			msg.WriteString(" replica not found")
			errorGroups++
			continue
		}

		generation := rctx.Generation()
		replicaHasError := false
		for _, condType := range conditionTypes {
			cond := findCondition(rctx.Conditions(), condType)
			if cond == nil || cond.Status != metav1.ConditionFalse || cond.ObservedGeneration != generation {
				continue
			}
			if skipError != nil && skipError(rctx, id, cond) {
				continue
			}

			if !replicaHasError {
				if errorGroups == 0 {
					msg.WriteString(". Errors: ")
				} else {
					msg.WriteString(" | ")
				}
				msg.WriteByte('#')
				msg.WriteString(strconv.Itoa(int(id)))
				msg.WriteByte(' ')
				replicaHasError = true
				errorGroups++
			} else {
				msg.WriteString(", ")
			}
			msg.WriteString(condType)
			msg.WriteByte('/')
			msg.WriteString(cond.Reason)
			if cond.Message != "" {
				msg.WriteString(": ")
				msg.WriteString(cond.Message)
			}
		}
	}

	return msg.String()
}

// ──────────────────────────────────────────────────────────────────────────────
// composeProgressMessage
//

// composeProgressMessage auto-composes the final status message from the plan's
// DisplayName, step info, and raw progress.
//
// Single-step plan:
//
//	"{planName}: {rawProgress}"
//	→ "Joining datamesh: 3/4 replicas confirmed revision 7"
//
// Multi-step plan:
//
//	"{planName} (step {i+1}/{n}: {stepName}): {rawProgress}"
//	→ "Joining datamesh (step 2/3: D∅ → D): 2/4 replicas confirmed revision 12"
func composeProgressMessage(planName, stepName string, stepIdx, totalSteps int, rawProgress string) string {
	if totalSteps <= 1 {
		return planName + ": " + rawProgress
	}
	return planName + " (step " + strconv.Itoa(stepIdx+1) + "/" + strconv.Itoa(totalSteps) + ": " + stepName + "): " + rawProgress
}

// ──────────────────────────────────────────────────────────────────────────────
// composeBlocked
//

// composeBlocked builds a blocked message by prepending the plan's display name.
//
// Format:
//
//	"{planName} is blocked: {reason}"
//	→ "Removing diskful replica is blocked: would violate GMDR (ADR=1, need > 1)"
func composeBlocked(planName, reason string) string {
	return planName + " is blocked: " + reason
}

// ──────────────────────────────────────────────────────────────────────────────
// composeBlockedByActive
//

// composeBlockedByActive builds a blocked-by-active message when a new
// dispatch decision conflicts with an active transition in the same slot.
//
// Format:
//
//	"{newPlanName}: waiting for {activePlanName} to complete[. {activeStep.Message}]"
func composeBlockedByActive(
	newPlanName, activePlanName string,
	activeStep *v1alpha1.ReplicatedVolumeDatameshTransitionStep,
) string {
	if activeStep != nil && activeStep.Message != "" {
		return newPlanName + ": waiting for " + activePlanName + " to complete. " + activeStep.Message
	}
	return newPlanName + ": waiting for " + activePlanName + " to complete"
}

// findCondition returns the first condition with the given type, or nil.
func findCondition(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}
