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
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/dmte"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/idset"
)

// ──────────────────────────────────────────────────────────────────────────────
// Shared confirm callbacks
//
// All confirm callbacks follow the same structure:
//   1. Compute MustConfirm set.
//   2. Compute Confirmed = replicas with rev >= stepRevision, intersected with MustConfirm.
//   3. Optionally: leaving replica confirms by rev=0 [or rvr=nil].

// confirmSubjectOnly checks confirmation for subject-only steps.
// MustConfirm = {subject}. Confirmed = subject rev >= stepRevision.
// Used by: Attach, D∅→D, D→D∅, sD∅→sD, sD→sD∅.
func confirmSubjectOnly(_ *globalContext, rctx *ReplicaContext, stepRevision int64) dmte.ConfirmResult {
	mustConfirm := idset.Of(rctx.ID())
	var confirmed idset.IDSet
	if rctx.rvr != nil && rctx.rvr.Status.DatameshRevision >= stepRevision {
		confirmed.Add(rctx.ID())
	}
	return dmte.ConfirmResult{MustConfirm: mustConfirm, Confirmed: confirmed}
}

// confirmSubjectOnlyLeaving checks confirmation for subject-only leaving steps.
// MustConfirm = {subject}. Confirmed = subject rev >= stepRevision OR rev == 0.
// The leaving replica confirms by resetting revision to 0 (left the datamesh).
// Used by: membership Remove star steps (A→✕, TB→✕ within multi-step plans).
func confirmSubjectOnlyLeaving(_ *globalContext, rctx *ReplicaContext, stepRevision int64) dmte.ConfirmResult {
	mustConfirm := idset.Of(rctx.ID())
	var confirmed idset.IDSet
	if rctx.rvr != nil &&
		(rctx.rvr.Status.DatameshRevision >= stepRevision || rctx.rvr.Status.DatameshRevision == 0) {
		confirmed.Add(rctx.ID())
	}
	return dmte.ConfirmResult{MustConfirm: mustConfirm, Confirmed: confirmed}
}

// confirmSubjectOnlyLeavingOrGone checks confirmation for subject-only leaving steps
// where the replica may disappear entirely (node died).
// MustConfirm = {subject}. Confirmed = rev >= stepRevision OR rev == 0 OR rvr == nil.
// Used by: Detach (node may die during detach).
func confirmSubjectOnlyLeavingOrGone(_ *globalContext, rctx *ReplicaContext, stepRevision int64) dmte.ConfirmResult {
	mustConfirm := idset.Of(rctx.ID())
	var confirmed idset.IDSet
	if rctx.rvr == nil ||
		rctx.rvr.Status.DatameshRevision == 0 ||
		rctx.rvr.Status.DatameshRevision >= stepRevision {
		confirmed.Add(rctx.ID())
	}
	return dmte.ConfirmResult{MustConfirm: mustConfirm, Confirmed: confirmed}
}

// confirmFMPlusSubject checks confirmation for FM + subject steps.
// MustConfirm = fullMeshMemberIDs ∪ {subject}.
// Used by: ✦→A, ✦→TB, A→TB, TB→A.
func confirmFMPlusSubject(gctx *globalContext, rctx *ReplicaContext, stepRevision int64) dmte.ConfirmResult {
	mustConfirm := fullMeshMemberIDs(gctx).Union(idset.Of(rctx.ID()))
	confirmed := confirmedReplicas(gctx, stepRevision).Intersect(mustConfirm)
	return dmte.ConfirmResult{MustConfirm: mustConfirm, Confirmed: confirmed}
}

// confirmFMPlusSubjectLeaving checks confirmation for FM + subject leaving steps.
// MustConfirm = fullMeshMemberIDs ∪ {subject}. Leaving replica confirms by rev == 0.
// Used by: A→✕, TB→✕ (final removal step).
func confirmFMPlusSubjectLeaving(gctx *globalContext, rctx *ReplicaContext, stepRevision int64) dmte.ConfirmResult {
	mustConfirm := fullMeshMemberIDs(gctx).Union(idset.Of(rctx.ID()))
	confirmed := confirmedReplicas(gctx, stepRevision).Intersect(mustConfirm)

	// The leaving replica confirms by resetting revision to 0.
	if rctx.rvr != nil && rctx.rvr.Status.DatameshRevision == 0 {
		confirmed.Add(rctx.ID())
	}

	return dmte.ConfirmResult{MustConfirm: mustConfirm, Confirmed: confirmed}
}

// confirmAllMembers checks confirmation for all-members steps.
// MustConfirm = all datamesh members.
// Used by: ✦→D∅, ✦→sD∅, A→D∅, sD∅→D∅, sD→D, D→sD, qmr↑, qmr↓, etc.
func confirmAllMembers(gctx *globalContext, stepRevision int64) dmte.ConfirmResult {
	mustConfirm := allMemberIDs(gctx)
	confirmed := confirmedReplicas(gctx, stepRevision).Intersect(mustConfirm)
	return dmte.ConfirmResult{MustConfirm: mustConfirm, Confirmed: confirmed}
}

// confirmAllMembersLeaving checks confirmation for all-members leaving steps.
// MustConfirm = all datamesh members ∪ {subject}. Leaving replica confirms by rev == 0.
// The subject must be in MustConfirm because removeMember sets rctx.member = nil,
// so allMemberIDs does not include it. The engine normalizes Confirmed ∩ MustConfirm,
// so the subject must be in both sets for completion to work.
// Used by: D∅→✕, sD∅→✕, sD→✕.
func confirmAllMembersLeaving(gctx *globalContext, rctx *ReplicaContext, stepRevision int64) dmte.ConfirmResult {
	mustConfirm := allMemberIDs(gctx)
	mustConfirm.Add(rctx.ID())
	confirmed := confirmedReplicas(gctx, stepRevision).Intersect(mustConfirm)

	// The leaving replica confirms by resetting revision to 0.
	if rctx.rvr != nil && rctx.rvr.Status.DatameshRevision == 0 {
		confirmed.Add(rctx.ID())
	}

	return dmte.ConfirmResult{MustConfirm: mustConfirm, Confirmed: confirmed}
}

// confirmImmediate returns an immediately-confirmed result (empty MustConfirm == empty Confirmed).
// Used by: ForceDetach — the node is dead, no one to wait for.
func confirmImmediate(_ *globalContext, _ *ReplicaContext, _ int64) dmte.ConfirmResult {
	return dmte.ConfirmResult{}
}

// ──────────────────────────────────────────────────────────────────────────────
// Confirm helper functions
//

// fullMeshMemberIDs returns the IDSet of all full-mesh members (D, D∅, sD, sD∅)
// by iterating all replica contexts.
func fullMeshMemberIDs(gctx *globalContext) idset.IDSet {
	var s idset.IDSet
	for i := range gctx.allReplicas {
		rc := &gctx.allReplicas[i]
		if rc.member != nil && rc.member.Type.ConnectsToAllPeers() {
			s.Add(rc.id)
		}
	}
	return s
}

// confirmedReplicas returns the IDSet of replicas that have confirmed the given
// revision (DatameshRevision >= stepRevision) by iterating all replica contexts.
func confirmedReplicas(gctx *globalContext, stepRevision int64) idset.IDSet {
	var s idset.IDSet
	for i := range gctx.allReplicas {
		rc := &gctx.allReplicas[i]
		if rc.rvr != nil && rc.rvr.Status.DatameshRevision >= stepRevision {
			s.Add(rc.id)
		}
	}
	return s
}

// ──────────────────────────────────────────────────────────────────────────────
// Adapters
//

// asReplicaConfirm adapts a global-scoped confirm callback for use in ReplicaStep.
func asReplicaConfirm(fn func(*globalContext, int64) dmte.ConfirmResult) func(*globalContext, *ReplicaContext, int64) dmte.ConfirmResult {
	return func(gctx *globalContext, _ *ReplicaContext, rev int64) dmte.ConfirmResult {
		return fn(gctx, rev)
	}
}

// asReplicaOnComplete adapts a global-scoped callback for use as ReplicaStep OnComplete.
func asReplicaOnComplete(fn func(*globalContext)) func(*globalContext, *ReplicaContext) {
	return func(gctx *globalContext, _ *ReplicaContext) {
		fn(gctx)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Confirm helper functions
//

// allMemberIDs returns the IDSet of all datamesh members.
func allMemberIDs(gctx *globalContext) idset.IDSet {
	var s idset.IDSet
	for i := range gctx.allReplicas {
		rc := &gctx.allReplicas[i]
		if rc.member != nil {
			s.Add(rc.id)
		}
	}
	return s
}
