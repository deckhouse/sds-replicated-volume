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

package datamesh

import (
	"iter"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/dmte"
)

// membershipDispatcher returns a dmte.DispatchFunc that creates membership
// transitions from DatameshReplicaRequests.
//
// For each replica with a pending request:
//  1. Maps the request operation to a transition type and plan selection function.
//  2. Calls the plan selection function:
//     - Plan selected: yield DispatchReplica.
//     - Blocked: yield NoDispatch with the blocked message.
//     - Skip: yield nothing.
//
// Same-type slot conflicts (re-dispatching a type that is already active)
// are silently ignored by the engine. Different-type slot conflicts are
// reported by the engine via composeBlockedByActive.
func membershipDispatcher() dmte.DispatchFunc[provider] {
	return func(cp provider) iter.Seq[dmte.DispatchDecision] {
		return func(yield func(dmte.DispatchDecision) bool) {
			gctx := cp.Global()

			for i := range gctx.allReplicas {
				rctx := &gctx.allReplicas[i]
				req := rctx.membershipRequest
				if req == nil || rctx.name == "" {
					continue
				}

				// Map operation to transition type + plan selection function.
				var tt dmte.TransitionType
				var planFn func(*globalContext, *ReplicaContext) (dmte.PlanID, string)
				switch req.Request.Operation {
				case v1alpha1.DatameshMembershipRequestOperationJoin:
					tt = v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica
					planFn = planAddReplica
				case v1alpha1.DatameshMembershipRequestOperationLeave:
					tt = v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveReplica
					planFn = planRemoveReplica
				case v1alpha1.DatameshMembershipRequestOperationChangeRole:
					tt = v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeReplicaType
					planFn = planChangeReplicaType
				default:
					continue
				}

				// Select plan.
				planID, blocked := planFn(gctx, rctx)
				if planID == "" && blocked == "" {
					continue
				}
				if planID == "" {
					if !yield(dmte.NoDispatch(rctx, membershipSlot, blocked, nil)) {
						return
					}
					continue
				}
				if !yield(dmte.DispatchReplica(rctx, tt, planID)) {
					return
				}
			}
		}
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Plan selection
//
// Each function selects the appropriate PlanID for a given transition type.
// Returns (planID, "") on selection, ("", blockedMsg) if blocked, ("", "") to skip.

// planAddReplica selects the plan for a Join request.
// Already a member → skip (transient: request not yet cleaned up after transition).
func planAddReplica(_ *globalContext, rctx *ReplicaContext) (dmte.PlanID, string) {
	if rctx.member != nil {
		return "", ""
	}
	switch rctx.membershipRequest.Request.Type {
	case v1alpha1.ReplicaTypeAccess:
		return "access/v1", ""
	case v1alpha1.ReplicaTypeTieBreaker:
		return "tiebreaker/v1", ""
	default:
		return "", "Not implemented"
	}
}

// planRemoveReplica selects the plan for a Leave request.
// Not a member → skip (transient: request not yet cleaned up after transition).
func planRemoveReplica(_ *globalContext, rctx *ReplicaContext) (dmte.PlanID, string) {
	if rctx.member == nil {
		return "", ""
	}
	switch rctx.member.Type {
	case v1alpha1.DatameshMemberTypeAccess:
		return "access/v1", ""
	case v1alpha1.DatameshMemberTypeTieBreaker:
		return "tiebreaker/v1", ""
	default:
		return "", "Not implemented"
	}
}

// planChangeReplicaType selects the plan for a ChangeRole request.
func planChangeReplicaType(_ *globalContext, _ *ReplicaContext) (dmte.PlanID, string) {
	return "", "Not implemented"
}
