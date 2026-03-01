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

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// Process runs the datamesh engine loop.
//
// Phase 1: Unified backward pass — confirm/advance/complete ALL active transitions.
// Phase 2: Membership forward pass — create transitions from DatameshReplicaRequests.
// Phase 3: Attachment forward pass — create transitions from planner decisions (RVA-driven).
//
// Pure non-I/O: mutates rv.Status in place. The caller owns DeepCopy + patch.
func (e *Engine) Process() Result {
	result := Result{}
	e.processBackward(&result)
	e.processMembershipForward(&result)
	e.processAttachmentForward(&result)
	return result
}

// ──────────────────────────────────────────────────────────────────────────────
// Backward pass
//

// processBackward confirms/advances/completes ALL active transitions (membership + attachment + global).
func (e *Engine) processBackward(result *Result) {
	rv := e.globalCtx.RV

	for i := len(rv.Status.DatameshTransitions) - 1; i >= 0; i-- {
		t := &rv.Status.DatameshTransitions[i]
		if !e.registry.hasTransitionType(t.Type) {
			continue
		}

		// Find the current (first non-Completed) step index.
		stepIdx := -1
		for j := range t.Steps {
			if t.Steps[j].Status != v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted {
				stepIdx = j
				break
			}
		}
		if stepIdx < 0 {
			// All steps completed — complete transition, requeue.
			e.completeTransition(rv, i)
			result.Changed = true
			result.Requeue = true
			continue
		}

		// Look up the plan by (type, PlanID) stored on the transition.
		p := e.registry.get(t.Type, PlanID(t.PlanID))
		if p == nil || stepIdx >= len(p.steps) {
			// Plan not found (removed in newer controller version) or step index out of range.
			// Skip confirmation for this cycle.
			continue
		}

		// Look up replica context for this transition's replica.
		var rctx *ReplicaContext
		if t.ReplicaName != "" {
			rctx = e.replicaCtxByID[t.ReplicaID()]
		}

		// Confirm the current step.
		s := &p.steps[stepIdx]
		stepRevision := t.Steps[stepIdx].DatameshRevision
		cr := s.confirm(&e.globalCtx, rctx, stepRevision)
		confirmed := cr.Confirmed == cr.MustConfirm

		// Generate progress message and store in rctx.
		progress := generateProgressMessage(e.globalCtx.RVRs, stepRevision, cr, s, rctx)
		if t.Steps[stepIdx].Message != progress {
			t.Steps[stepIdx].Message = progress
			result.Changed = true
		}
		e.setReplicaCtxMessage(t.Group, s, rctx, progress)

		if !confirmed {
			continue
		}

		if stepIdx+1 < len(t.Steps) {
			// Advance to next step, apply, bump revision, generate initial message.
			advanceStep(t, stepIdx)
			e.applyAndConfirmStep(p, t, rctx, stepIdx+1)
		} else {
			// Last step confirmed — call onComplete, then remove transition.
			p.onComplete(&e.globalCtx, rctx)
			e.completeTransition(rv, i)
			if rctx != nil {
				switch t.Group {
				case v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
					v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership,
					v1alpha1.ReplicatedVolumeDatameshTransitionGroupEmergency:
					rctx.MembershipTransition = nil
				case v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment:
					rctx.AttachmentTransition = nil
				}
			}
			result.Requeue = true
		}
		result.Changed = true
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Membership forward pass
//

// processMembershipForward creates membership transitions from DatameshReplicaRequests.
func (e *Engine) processMembershipForward(result *Result) {
	for i := range e.replicaCtxs {
		rctx := &e.replicaCtxs[i]
		req := rctx.MembershipRequest
		if req == nil {
			continue
		}

		_, hasID := rctx.ID()
		if !hasID {
			continue
		}

		// Classify the request operation into a transition type.
		transitionType := e.membershipPlanner.Classify(req.Request.Operation)

		// If there's an active membership transition, compose message and skip.
		if rctx.MembershipTransition != nil {
			var msg string
			if transitionType == rctx.MembershipTransition.Type {
				// Same operation — use the message from backward pass.
				msg = rctx.membershipMessage
			} else {
				// Request changed during active transition — compose combined message.
				msg = ComposeBlockedByActiveMessage(req, rctx.MembershipTransition)
			}
			if msg != "" && req.Message != msg {
				req.Message = msg
				result.Changed = true
			}
			continue
		}

		// No active membership transition — call planner.
		planID, blockedMsg := e.membershipPlanner.Plan(&e.globalCtx, rctx, transitionType)

		// Planner says nothing to do (skip) — leave message unchanged.
		if planID == "" && blockedMsg == "" {
			continue
		}

		// Planner blocked the request.
		if planID == "" {
			if req.Message != blockedMsg {
				req.Message = blockedMsg
				result.Changed = true
			}
			continue
		}

		// Create transition.
		t, reason := e.createReplicaTransition(transitionType, planID, rctx)
		if t == nil {
			if req.Message != reason {
				req.Message = reason
				result.Changed = true
			}
			continue
		}
		rctx.MembershipTransition = t
		result.Changed = true
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Attachment forward pass
//

// processAttachmentForward creates attachment transitions from planner decisions.
func (e *Engine) processAttachmentForward(result *Result) {
	// Multiattach toggle.
	if md := e.attachmentPlanner.PlanMultiattach(&e.globalCtx); md != nil {
		t, _ := e.CreateGlobalTransition(md.TransitionType, md.PlanID)
		if t != nil {
			result.Changed = true
		}
	}

	// Collect relevant ReplicaContexts: has RVA or member is attached.
	var relevant []*ReplicaContext
	for i := range e.replicaCtxs {
		rctx := &e.replicaCtxs[i]
		if len(rctx.RVAs) > 0 || (rctx.Member != nil && rctx.Member.Attached) {
			relevant = append(relevant, rctx)
		}
	}
	if len(relevant) == 0 {
		return
	}

	// Ask planner for all decisions at once.
	decisions := e.attachmentPlanner.PlanAttachments(&e.globalCtx, relevant)

	for i, d := range decisions {
		rctx := relevant[i]

		if d.PlanID == "" && d.BlockedMessage == "" {
			continue
		}

		// Blocked by planner (or settled — condition-only output).
		if d.PlanID == "" {
			if rctx.attachmentConditionMessage != d.BlockedMessage ||
				rctx.attachmentConditionReason != d.BlockedReason {
				rctx.attachmentConditionMessage = d.BlockedMessage
				rctx.attachmentConditionReason = d.BlockedReason
				result.Changed = true
			}
			continue
		}

		// Create transition.
		t, reason := e.createReplicaTransition(d.TransitionType, d.PlanID, rctx)
		if t == nil {
			if reason != "" {
				rctx.attachmentConditionMessage = reason
			}
			result.Changed = true
			continue
		}
		rctx.AttachmentTransition = t
		result.Changed = true
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Messages
//

// ComposeBlockedByActiveMessage builds a request message when a new request
// is waiting for an active transition to complete. Used when the request changed
// while a transition is in progress (e.g., replica requested Leave but AddReplica
// is still running).
//
// Example output: "Leave: waiting for AddReplica to complete. Joining datamesh: 3/4 confirmed."
func ComposeBlockedByActiveMessage(
	req *v1alpha1.ReplicatedVolumeDatameshReplicaRequest,
	activeTx *v1alpha1.ReplicatedVolumeDatameshTransition,
) string {
	// Describe the new request.
	newReqDesc := describeOperation(req.Request.Operation)

	// Describe the active transition.
	activeTxDesc := string(activeTx.Type)

	// Get the active step's progress message.
	stepMsg := ""
	if step := activeTx.CurrentStep(); step != nil && step.Message != "" {
		stepMsg = step.Message
	}

	if stepMsg != "" {
		return fmt.Sprintf("%s: waiting for %s to complete. %s", newReqDesc, activeTxDesc, stepMsg)
	}
	return fmt.Sprintf("%s: waiting for %s to complete", newReqDesc, activeTxDesc)
}

// describeOperation returns a human-readable description of a membership request operation.
func describeOperation(op v1alpha1.DatameshMembershipRequestOperation) string {
	switch op {
	case v1alpha1.DatameshMembershipRequestOperationJoin:
		return "Join"
	case v1alpha1.DatameshMembershipRequestOperationLeave:
		return "Leave"
	case v1alpha1.DatameshMembershipRequestOperationChangeRole:
		return "Change role"
	case v1alpha1.DatameshMembershipRequestOperationChangeBackingVolume:
		return "Change backing volume"
	default:
		return string(op)
	}
}
