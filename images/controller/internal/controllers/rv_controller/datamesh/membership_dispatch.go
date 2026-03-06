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
				if rctx.name == "" {
					continue
				}

				// Determine transition type + plan selection function.
				var tt dmte.TransitionType
				var planFn func(*globalContext, *ReplicaContext) (dmte.PlanID, string)

				if req := rctx.membershipRequest; req != nil {
					// Explicit request from DatameshReplicaRequests.
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
					case v1alpha1.DatameshMembershipRequestOperationForceLeave:
						tt = v1alpha1.ReplicatedVolumeDatameshTransitionTypeForceRemoveReplica
						planFn = planForceRemoveReplica
					default:
						continue
					}
				} else if rctx.member != nil && rctx.rvr == nil {
					// Orphan member: member exists but RVR is gone (node permanently lost).
					// Auto-dispatch ForceRemoveReplica to restore configuration.
					tt = v1alpha1.ReplicatedVolumeDatameshTransitionTypeForceRemoveReplica
					planFn = planForceRemoveReplica
				} else {
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

// planForceRemoveReplica selects the ForceRemoveReplica plan based on member type
// and voter parity. Not a member → skip.
func planForceRemoveReplica(gctx *globalContext, rctx *ReplicaContext) (dmte.PlanID, string) {
	if rctx.member == nil {
		return "", ""
	}
	switch rctx.member.Type {
	case v1alpha1.DatameshMemberTypeAccess:
		return "access/v1", ""
	case v1alpha1.DatameshMemberTypeTieBreaker:
		return "tiebreaker/v1", ""
	case v1alpha1.DatameshMemberTypeShadowDiskful,
		v1alpha1.DatameshMemberTypeLiminalShadowDiskful:
		return "shadow-diskful/v1", ""
	case v1alpha1.DatameshMemberTypeDiskful,
		v1alpha1.DatameshMemberTypeLiminalDiskful:
		voters := voterCount(gctx)
		if voters%2 != 0 {
			return "diskful/v1", "" // odd→even, no q↓
		}
		return "diskful-q-down/v1", "" // even→odd, q↓
	default:
		return "", "Not implemented"
	}
}

// planAddReplica selects the plan for a Join request.
// Already a member → skip (transient: request not yet cleaned up after transition).
func planAddReplica(gctx *globalContext, rctx *ReplicaContext) (dmte.PlanID, string) {
	if rctx.member != nil {
		return "", ""
	}
	switch rctx.membershipRequest.Request.Type {
	case v1alpha1.ReplicaTypeAccess:
		return "access/v1", ""
	case v1alpha1.ReplicaTypeTieBreaker:
		return "tiebreaker/v1", ""
	case v1alpha1.ReplicaTypeShadowDiskful:
		return "shadow-diskful/v1", ""
	case v1alpha1.ReplicaTypeDiskful:
		return planAddDiskful(gctx, rctx)
	default:
		return "", "Not implemented"
	}
}

// planAddDiskful selects the AddReplica(D) plan variant based on voter parity,
// sD feature availability, and whether qmr needs to be raised.
func planAddDiskful(gctx *globalContext, _ *ReplicaContext) (dmte.PlanID, string) {
	voters := voterCount(gctx)
	needsQUp := voters%2 != 0 // odd voters → adding makes even → q↑ needed
	needsQMRUp := gctx.baselineLayout.GuaranteedMinimumDataRedundancy < gctx.configuration.GuaranteedMinimumDataRedundancy
	viaSd := gctx.features.ShadowDiskful

	switch {
	case !viaSd && !needsQUp && !needsQMRUp:
		return "diskful/v1", ""
	case !viaSd && !needsQUp && needsQMRUp:
		return "diskful-qmr-up/v1", ""
	case !viaSd && needsQUp && !needsQMRUp:
		return "diskful-q-up/v1", ""
	case !viaSd && needsQUp && needsQMRUp:
		return "diskful-q-up-qmr-up/v1", ""
	case viaSd && !needsQUp && !needsQMRUp:
		return "diskful-via-sd/v1", ""
	case viaSd && !needsQUp && needsQMRUp:
		return "diskful-via-sd-qmr-up/v1", ""
	case viaSd && needsQUp && !needsQMRUp:
		return "diskful-via-sd-q-up/v1", ""
	case viaSd && needsQUp && needsQMRUp:
		return "diskful-via-sd-q-up-qmr-up/v1", ""
	default:
		return "", "Not implemented"
	}
}

// planRemoveReplica selects the plan for a Leave request.
// Not a member → skip (transient: request not yet cleaned up after transition).
func planRemoveReplica(gctx *globalContext, rctx *ReplicaContext) (dmte.PlanID, string) {
	if rctx.member == nil {
		return "", ""
	}
	switch rctx.member.Type {
	case v1alpha1.DatameshMemberTypeAccess:
		return "access/v1", ""
	case v1alpha1.DatameshMemberTypeTieBreaker:
		return "tiebreaker/v1", ""
	case v1alpha1.DatameshMemberTypeShadowDiskful,
		v1alpha1.DatameshMemberTypeLiminalShadowDiskful:
		return "shadow-diskful/v1", ""
	case v1alpha1.DatameshMemberTypeDiskful,
		v1alpha1.DatameshMemberTypeLiminalDiskful:
		return planRemoveDiskful(gctx, rctx)
	default:
		return "", "Not implemented"
	}
}

// planRemoveDiskful selects the RemoveReplica(D) plan variant based on voter parity
// and whether qmr needs to be lowered.
func planRemoveDiskful(gctx *globalContext, _ *ReplicaContext) (dmte.PlanID, string) {
	voters := voterCount(gctx)
	needsQDown := voters%2 == 0 // even voters → removing makes odd → q↓ needed
	needsQMRDown := gctx.baselineLayout.GuaranteedMinimumDataRedundancy > gctx.configuration.GuaranteedMinimumDataRedundancy

	switch {
	case !needsQDown && !needsQMRDown:
		return "remove-diskful/v1", ""
	case !needsQDown && needsQMRDown:
		return "remove-diskful-qmr-down/v1", ""
	case needsQDown && !needsQMRDown:
		return "remove-diskful-q-down/v1", ""
	case needsQDown && needsQMRDown:
		return "remove-diskful-qmr-down-q-down/v1", ""
	default:
		return "", "Not implemented"
	}
}

// planChangeReplicaType selects the plan for a ChangeRole request.
// Not a member → skip. Already target type → skip.
func planChangeReplicaType(gctx *globalContext, rctx *ReplicaContext) (dmte.PlanID, string) {
	if rctx.member == nil {
		return "", ""
	}

	if rctx.membershipTransition != nil &&
		rctx.membershipTransition.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica {
		return "", "" // skip: AddReplica in progress, change type after it completes
	}

	targetType := rctx.membershipRequest.Request.Type
	switch {
	case rctx.member.Type == v1alpha1.DatameshMemberTypeAccess &&
		targetType == v1alpha1.ReplicaTypeTieBreaker:
		return "a-to-tb/v1", ""
	case rctx.member.Type == v1alpha1.DatameshMemberTypeTieBreaker &&
		targetType == v1alpha1.ReplicaTypeAccess:
		return "tb-to-a/v1", ""
	case rctx.member.Type == v1alpha1.DatameshMemberTypeAccess &&
		targetType == v1alpha1.ReplicaTypeShadowDiskful:
		return "a-to-sd/v1", ""
	case (rctx.member.Type == v1alpha1.DatameshMemberTypeShadowDiskful ||
		rctx.member.Type == v1alpha1.DatameshMemberTypeLiminalShadowDiskful) &&
		targetType == v1alpha1.ReplicaTypeAccess:
		return "sd-to-a/v1", ""
	case rctx.member.Type == v1alpha1.DatameshMemberTypeTieBreaker &&
		targetType == v1alpha1.ReplicaTypeShadowDiskful:
		return "tb-to-sd/v1", ""
	case (rctx.member.Type == v1alpha1.DatameshMemberTypeShadowDiskful ||
		rctx.member.Type == v1alpha1.DatameshMemberTypeLiminalShadowDiskful) &&
		targetType == v1alpha1.ReplicaTypeTieBreaker:
		return "sd-to-tb/v1", ""
	case rctx.member.Type == v1alpha1.DatameshMemberTypeAccess &&
		targetType == v1alpha1.ReplicaTypeDiskful:
		return planChangeAToD(gctx, rctx)
	case (rctx.member.Type == v1alpha1.DatameshMemberTypeDiskful ||
		rctx.member.Type == v1alpha1.DatameshMemberTypeLiminalDiskful) &&
		targetType == v1alpha1.ReplicaTypeAccess:
		return planChangeDToA(gctx, rctx)
	case (rctx.member.Type == v1alpha1.DatameshMemberTypeShadowDiskful ||
		rctx.member.Type == v1alpha1.DatameshMemberTypeLiminalShadowDiskful) &&
		targetType == v1alpha1.ReplicaTypeDiskful:
		return planChangeSDToD(gctx, rctx)
	case (rctx.member.Type == v1alpha1.DatameshMemberTypeDiskful ||
		rctx.member.Type == v1alpha1.DatameshMemberTypeLiminalDiskful) &&
		targetType == v1alpha1.ReplicaTypeShadowDiskful:
		return planChangeDToSD(gctx, rctx)
	case rctx.member.Type == v1alpha1.DatameshMemberTypeTieBreaker &&
		targetType == v1alpha1.ReplicaTypeDiskful:
		return planChangeTBToD(gctx, rctx)
	case (rctx.member.Type == v1alpha1.DatameshMemberTypeDiskful ||
		rctx.member.Type == v1alpha1.DatameshMemberTypeLiminalDiskful) &&
		targetType == v1alpha1.ReplicaTypeTieBreaker:
		return planChangeDToTB(gctx, rctx)
	case rctx.member.Type == v1alpha1.DatameshMemberType(targetType):
		return "", "" // already target type
	default:
		return "", "Not implemented"
	}
}

// planChangeAToD selects the ChangeReplicaType(A→D) plan variant
// based on voter parity and sD feature availability.
func planChangeAToD(gctx *globalContext, _ *ReplicaContext) (dmte.PlanID, string) {
	voters := voterCount(gctx)
	viaSd := gctx.features.ShadowDiskful

	switch {
	case !viaSd && voters%2 == 0:
		return "a-to-d/v1", ""
	case !viaSd && voters%2 != 0:
		return "a-to-d-q-up/v1", ""
	case viaSd && voters%2 == 0:
		return "a-to-d-via-sd/v1", ""
	case viaSd && voters%2 != 0:
		return "a-to-d-via-sd-q-up/v1", ""
	default:
		return "", "Not implemented"
	}
}

// planChangeDToA selects the ChangeReplicaType(D→A) plan variant.
func planChangeDToA(gctx *globalContext, _ *ReplicaContext) (dmte.PlanID, string) {
	voters := voterCount(gctx)
	if voters%2 != 0 {
		return "d-to-a/v1", "" // odd→even, no q change
	}
	return "d-to-a-q-down/v1", "" // even→odd, q↓ needed
}

// planChangeTBToD selects the ChangeReplicaType(TB→D) plan variant.
func planChangeTBToD(gctx *globalContext, _ *ReplicaContext) (dmte.PlanID, string) {
	voters := voterCount(gctx)
	viaSd := gctx.features.ShadowDiskful

	switch {
	case !viaSd && voters%2 == 0:
		return "tb-to-d/v1", ""
	case !viaSd && voters%2 != 0:
		return "tb-to-d-q-up/v1", ""
	case viaSd && voters%2 == 0:
		return "tb-to-d-via-sd/v1", ""
	case viaSd && voters%2 != 0:
		return "tb-to-d-via-sd-q-up/v1", ""
	default:
		return "", "Not implemented"
	}
}

// planChangeDToTB selects the ChangeReplicaType(D→TB) plan variant.
func planChangeDToTB(gctx *globalContext, _ *ReplicaContext) (dmte.PlanID, string) {
	voters := voterCount(gctx)
	if voters%2 != 0 {
		return "d-to-tb/v1", ""
	}
	return "d-to-tb-q-down/v1", ""
}

// planChangeSDToD selects the ChangeReplicaType(sD→D) plan variant.
func planChangeSDToD(gctx *globalContext, _ *ReplicaContext) (dmte.PlanID, string) {
	voters := voterCount(gctx)
	if voters%2 == 0 {
		return "sd-to-d/v1", "" // even→odd, no q change
	}
	return "sd-to-d-q-up/v1", "" // odd→even, q↑ needed
}

// planChangeDToSD selects the ChangeReplicaType(D→sD) plan variant.
func planChangeDToSD(gctx *globalContext, _ *ReplicaContext) (dmte.PlanID, string) {
	voters := voterCount(gctx)
	if voters%2 != 0 {
		return "d-to-sd/v1", "" // odd→even, no q change
	}
	return "d-to-sd-q-down/v1", "" // even→odd, q↓ needed
}
