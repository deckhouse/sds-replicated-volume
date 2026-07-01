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
	"cmp"
	"iter"
	"slices"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/dmte"
)

// attachmentDispatcher returns a dmte.DispatchFunc that creates attachment
// transitions from per-replica intent decisions.
//
// The dispatch loop:
//  1. Decides if a multiattach toggle is needed (global transition).
//  2. Collects relevant replicas (has RVAs or member is attached), sorts FIFO.
//  3. Yields per-replica decisions: DispatchReplica (Attach/Detach),
//     NoDispatch (settled/waiting), or skip.
//
// Slot conflicts (e.g., Attach while Detach is active on the same slot) are
// handled by the engine's generic slot conflict logic. Slot availability,
// multiattach readiness, quorum, eligibility, and other admission checks
// are handled by guards on the plans and by the concurrency tracker.
func attachmentDispatcher() dmte.DispatchFunc[provider] {
	return func(cp provider) iter.Seq[dmte.DispatchDecision] {
		return func(yield func(dmte.DispatchDecision) bool) {
			gctx := cp.Global()

			// 0. ForceDetach from requests (before normal attachment decisions).
			// Skip if member is not attached (already detached or never was).
			for i := range gctx.allReplicas {
				rctx := &gctx.allReplicas[i]
				if rctx.membershipRequest != nil &&
					rctx.membershipRequest.Request.Operation == v1alpha1.DatameshMembershipRequestOperationForceDetach &&
					rctx.member != nil && rctx.member.Attached {
					if !yield(dmte.DispatchReplica(rctx,
						v1alpha1.ReplicatedVolumeDatameshTransitionTypeForceDetach,
						"force-detach/v1")) {
						return
					}
				}
			}

			// 1. Multiattach toggle.
			if d, ok := decideMultiattachToggle(gctx); ok {
				if !yield(d) {
					return
				}
			}

			// 2. Collect relevant replicas + FIFO sort.
			candidates := collectAndSortCandidates(gctx)

			// 3. Per-replica decisions.
			for _, rctx := range candidates {
				d, ok := decideAttachmentForReplica(rctx)
				if !ok {
					continue
				}
				if !yield(d) {
					return
				}
			}
		}
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Multiattach toggle
//

// decideMultiattachToggle decides if EnableMultiattach or DisableMultiattach
// should be dispatched. Returns (decision, true) if a toggle is needed.
//
// Counts intended attachments: replicas with active RVA + datamesh member.
// Multiattach is needed only when both intendedCount > 1 AND maxAttachments > 1.
// When maxAttachments=1, only one slot is available regardless of pending RVAs.
//
// The dispatcher decides "do we WANT to toggle?"; guards on the plans decide
// "CAN we toggle?" (guardMaxAttachmentsAllowsMultiattach, guardCanDisableMultiattach).
func decideMultiattachToggle(gctx *globalContext) (dmte.DispatchDecision, bool) {
	// Count intended attachments.
	intendedCount := 0
	for i := range gctx.allReplicas {
		rc := &gctx.allReplicas[i]
		if rc.member != nil && rc.hasActiveRVA() {
			intendedCount++
		}
	}

	needMultiattach := intendedCount > 1 && gctx.maxAttachments > 1

	if needMultiattach && !gctx.datamesh.multiattach && !gctx.hasMultiattachTransition {
		return dmte.DispatchGlobal(
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeEnableMultiattach,
			"enable-multiattach/v1",
		), true
	}

	if !needMultiattach && gctx.datamesh.multiattach && !gctx.hasMultiattachTransition &&
		gctx.potentiallyAttached.Len() <= 1 {
		return dmte.DispatchGlobal(
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeDisableMultiattach,
			"disable-multiattach/v1",
		), true
	}

	return dmte.DispatchDecision{}, false
}

// ──────────────────────────────────────────────────────────────────────────────
// Candidate collection + sort
//

// collectAndSortCandidates collects replicas that need attachment processing
// and sorts them by FIFO (earliest active RVA timestamp, nodeName tie-break).
//
// Replicas without active RVA (detach-only candidates) get zero timestamp
// and sort first — detach decisions are processed before attach decisions.
func collectAndSortCandidates(gctx *globalContext) []*ReplicaContext {
	var candidates []*ReplicaContext
	for i := range gctx.allReplicas {
		rctx := &gctx.allReplicas[i]
		if len(rctx.rvas) > 0 || (rctx.member != nil && rctx.member.Attached) {
			candidates = append(candidates, rctx)
		}
	}

	slices.SortFunc(candidates, func(a, b *ReplicaContext) int {
		ta := a.earliestActiveRVATimestamp()
		tb := b.earliestActiveRVATimestamp()
		if c := ta.Compare(tb.Time); c != 0 {
			return c
		}
		return cmp.Compare(a.nodeName, b.nodeName)
	})

	return candidates
}

// ──────────────────────────────────────────────────────────────────────────────
// Per-replica intent
//

// decideAttachmentForReplica decides the attachment action for a single replica.
// Returns (decision, true) to yield, or (_, false) to skip.
//
// The dispatcher only decides intent (Attach/Detach) and settled status.
// All admission checks (member existence, eligibility, quorum, slots, etc.)
// are handled by guards on the plans.
func decideAttachmentForReplica(rctx *ReplicaContext) (dmte.DispatchDecision, bool) {
	hasActive := rctx.hasActiveRVA()
	isAttached := rctx.member != nil && rctx.member.Attached

	// Already attached + active RVA + no active transition → settled.
	if isAttached && hasActive && rctx.attachmentTransition == nil {
		msg := "Volume is attached and ready to serve I/O on the node"
		if rctx.gctx.deletionTimestamp != nil {
			msg += " (volume is pending deletion)"
		}
		return dmte.NoDispatch(rctx, attachmentSlot, msg,
			v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttached), true
	}

	// Has active RVA → Attach (guards handle member existence, eligibility, etc.).
	if hasActive {
		return dmte.DispatchReplica(rctx,
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach,
			"attach/v1",
		), true
	}

	// No active RVA + member attached → Detach.
	if isAttached {
		return dmte.DispatchReplica(rctx,
			v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach,
			"detach/v1",
		), true
	}

	// Active attachment transition in progress (e.g., Detach applied but not
	// confirmed) — skip; settle already set the authoritative slot status.
	if rctx.attachmentTransition != nil {
		return dmte.DispatchDecision{}, false
	}

	// Only deleting RVAs → detached.
	if len(rctx.rvas) > 0 {
		return dmte.NoDispatch(rctx, attachmentSlot,
			"Volume has been detached from the node",
			v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonDetached), true
	}

	// No RVAs, member not attached → skip.
	return dmte.DispatchDecision{}, false
}
