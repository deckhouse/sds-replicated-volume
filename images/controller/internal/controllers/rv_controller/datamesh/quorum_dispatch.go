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

// quorumDispatcher returns a dmte.DispatchFunc that reconciles q and qmr
// to their correct values when they drift from the expected state.
//
// Skips the check entirely when a VotingMembership transition is active
// (voter count is changing — correct q is not yet determined, and the
// concurrency tracker would block ChangeQuorum anyway).
//
// When only qmr is wrong by exactly ±1 and a matching D request exists
// (Join for qmr↑, Leave for qmr↓), the quorum dispatcher defers to the
// membership dispatcher — the embedded qmr↑/qmr↓ step in the D plan
// handles it in one transition instead of two.
//
// Uses computeCorrectQuorum as the single source of truth and selects
// among 4 plan variants based on the direction of q and qmr changes.
func quorumDispatcher() dmte.DispatchFunc[provider] {
	return func(cp provider) iter.Seq[dmte.DispatchDecision] {
		return func(yield func(dmte.DispatchDecision) bool) {
			gctx := cp.Global()

			// Skip if voter count is changing (voting transition active).
			if gctx.votingMembershipTransitions.Len() > 0 {
				return
			}

			expectedQ, expectedQMR := computeCorrectQuorum(gctx)
			currentQ := gctx.datamesh.quorum
			currentQMR := gctx.datamesh.quorumMinimumRedundancy

			qWrong := expectedQ != currentQ
			qmrWrong := expectedQMR != currentQMR

			if !qWrong && !qmrWrong {
				return
			}

			// If q is wrong → must dispatch ChangeQuorum. Membership plans
			// adjust q by ±1 per voter change, but can't fix arbitrary
			// corruption (e.g. q=18 with 3 voters).
			if !qWrong && qmrWrong {
				// Only qmr is wrong. Check if the membership dispatcher
				// will handle it via embedded qmr↑/qmr↓ in a D plan:
				//   1. qmr differs by exactly 1
				//   2. Direction matches a pending D request
				if canMembershipHandleQMR(gctx, expectedQMR, currentQMR) {
					return
				}
			}

			planID := selectQuorumPlan(expectedQ, currentQ, expectedQMR, currentQMR)

			yield(dmte.DispatchGlobal(
				v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeQuorum,
				planID,
			))
		}
	}
}

// canMembershipHandleQMR returns true if the membership dispatcher will
// handle the qmr correction via an embedded qmr↑/qmr↓ step in an
// AddReplica(D) or RemoveReplica(D) plan.
//
// Conditions (all must hold):
//  1. qmr differs by exactly 1
//  2. Direction matches: up → pending Join(D), down → pending Leave for a D member
func canMembershipHandleQMR(gctx *globalContext, expectedQMR, currentQMR byte) bool {
	if expectedQMR == currentQMR+1 {
		// qmr needs +1 → need a pending Join(D) request.
		return hasPendingJoinD(gctx)
	}
	if expectedQMR+1 == currentQMR {
		// qmr needs -1 → need a pending Leave request for a D member.
		return hasPendingLeaveD(gctx)
	}
	return false
}

// hasPendingJoinD returns true if any replica has a pending Join request
// for Diskful type (and is not already a member).
func hasPendingJoinD(gctx *globalContext) bool {
	for i := range gctx.allReplicas {
		rc := &gctx.allReplicas[i]
		if rc.membershipRequest == nil || rc.member != nil {
			continue
		}
		if rc.membershipRequest.Request.Operation == v1alpha1.DatameshMembershipRequestOperationJoin &&
			rc.membershipRequest.Request.Type == v1alpha1.ReplicaTypeDiskful {
			return true
		}
	}
	return false
}

// hasPendingLeaveD returns true if any replica has a pending Leave request
// and is currently a D (voter) member.
func hasPendingLeaveD(gctx *globalContext) bool {
	for i := range gctx.allReplicas {
		rc := &gctx.allReplicas[i]
		if rc.membershipRequest == nil || rc.member == nil {
			continue
		}
		if rc.membershipRequest.Request.Operation == v1alpha1.DatameshMembershipRequestOperationLeave &&
			rc.member.Type.IsVoter() {
			return true
		}
	}
	return false
}

// selectQuorumPlan picks the plan variant based on q and qmr direction.
func selectQuorumPlan(expectedQ, currentQ, expectedQMR, currentQMR byte) dmte.PlanID {
	qUp := expectedQ > currentQ
	qDown := expectedQ < currentQ
	qmrUp := expectedQMR > currentQMR
	qmrDown := expectedQMR < currentQMR

	switch {
	case qDown && qmrUp:
		return "lower-q-raise-qmr/v1"
	case qUp && qmrDown:
		return "raise-q-lower-qmr/v1"
	case qUp || qmrUp:
		// q↑ and/or qmr↑ (neither goes down).
		return "raise/v1"
	default:
		// q↓ and/or qmr↓ (neither goes up).
		return "lower/v1"
	}
}
