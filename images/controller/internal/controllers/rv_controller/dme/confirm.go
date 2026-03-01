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

package dme

import (
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/idset"
)

// generateProgressMessage builds a human-readable progress message from confirmation sets.
//
// Examples:
//   - "4/4 replicas confirmed revision 7"
//   - "3/4 replicas confirmed revision 7. Waiting: [#2]"
//   - "2/4 replicas confirmed revision 7. Waiting: [#2, #5]. Errors: #2 DRBDConfigured/SomeReason: message | #5 Replica not found"
func generateProgressMessage(
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	stepRevision int64,
	cr ConfirmResult,
	s *step,
	rctx *ReplicaContext,
) string {
	waiting := cr.MustConfirm.Difference(cr.Confirmed)

	var msg strings.Builder
	fmt.Fprintf(&msg, "%d/%d replicas confirmed revision %d",
		cr.Confirmed.Len(), cr.MustConfirm.Len(), stepRevision)

	if waiting.IsEmpty() {
		return msg.String()
	}

	fmt.Fprintf(&msg, ". Waiting: [%s]", waiting)

	if len(s.diagnosticConditionTypes) == 0 {
		return msg.String()
	}

	var found idset.IDSet
	errorGroups := 0
	for _, rvr := range rvrs {
		id := rvr.ID()
		if !waiting.Contains(id) {
			continue
		}
		found.Add(id)

		replicaHasError := false
		for _, condType := range s.diagnosticConditionTypes {
			cond := obju.GetStatusCondition(rvr, condType)
			if cond == nil || cond.Status != metav1.ConditionFalse || cond.ObservedGeneration != rvr.Generation {
				continue
			}
			if s.diagnosticSkipError != nil && s.diagnosticSkipError(rctx, id, cond) {
				continue
			}

			if !replicaHasError {
				if errorGroups == 0 {
					msg.WriteString(". Errors: ")
				} else {
					msg.WriteString(" | ")
				}
				fmt.Fprintf(&msg, "#%d ", id)
				replicaHasError = true
				errorGroups++
			} else {
				msg.WriteString(", ")
			}
			fmt.Fprintf(&msg, "%s/%s", condType, cond.Reason)
			if cond.Message != "" {
				msg.WriteString(": ")
				msg.WriteString(cond.Message)
			}
		}
	}

	for id := range waiting.Difference(found).All() {
		if errorGroups == 0 {
			msg.WriteString(". Errors: ")
		} else {
			msg.WriteString(" | ")
		}
		fmt.Fprintf(&msg, "#%d Replica not found", id)
		errorGroups++
	}

	return msg.String()
}

// composeMessage prepends the step's messagePrefix to the progress message.
// If no prefix is set, returns the progress message as-is.
func composeMessage(s *step, progress string) string {
	if s.messagePrefix == "" {
		return progress
	}
	return s.messagePrefix + ", " + progress
}
