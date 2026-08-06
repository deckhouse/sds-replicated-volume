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
	"fmt"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// ──────────────────────────────────────────────────────────────────────────────
// Tie-breaker readiness
//
// Single source of truth for "does this TieBreaker actually provide tiebreak protection right
// now?". Two callers ask that exact question and MUST NOT answer it differently:
//
//   - the datamesh guard guardTBSufficient, which releases a leaving TieBreaker only once the
//     datamesh keeps enough OPERATIONAL ones (strict create-first replacement);
//   - rv_controller formation, which must not declare a volume formed while its TieBreaker
//     breaks the tie for nobody.
//
// The criteria therefore live here, in the package that owns datamesh semantics, and both
// callers go through IsTieBreakerOperational.

// TieBreakerPeer identifies a data-bearing (full-mesh) member the TieBreaker is expected to be
// connected to.
type TieBreakerPeer struct {
	// ID is the peer's replica ID (0-31).
	ID uint8
	// Name is the peer's name, used in diagnostics. Falls back to "replica <ID>" when empty.
	Name string
	// RVR is the peer's replica object, or nil when it does not exist. A nil RVR only means that
	// this side cannot confirm the connection — the TieBreaker's own fresh report still can.
	RVR *v1alpha1.ReplicatedVolumeReplica
}

// displayName renders the peer for diagnostics: its name when known, otherwise its ID.
func (p TieBreakerPeer) displayName() string {
	if p.Name != "" {
		return p.Name
	}
	return fmt.Sprintf("replica %d", p.ID)
}

// IsTieBreakerOperational reports whether the TieBreaker replica tb provides tiebreak protection
// right now. If it does not, the second return value explains why (used in guard and formation
// wait messages).
//
// Membership is NOT enough: completing AddReplica(TB) — or adding the TieBreaker in the formation
// bulk-add — only proves that the agents applied the configuration revision (confirmFMPlusSubject),
// not that DRBD connections were established, and a TieBreaker that is not connected breaks the tie
// for nobody. The criteria:
//
//  1. the replica object exists and is not itself being deleted (a terminating replica is on its
//     way out);
//  2. it has applied the current datamesh revision (`>=`, not `==`: a replica cannot be "too new",
//     being ahead is cache skew, not staleness — same convention as confirmedReplicas);
//  3. DRBDConfigured=True with a current ObservedGeneration (the agent configured THIS spec);
//  4. every connection to the given data-bearing peers is confirmed Connected by at least one side
//     whose own report is fresh — agent ready and at the current revision (connectionVerified).
//
// Deliberately NOT required: a backing volume (a TieBreaker has none), a replication state, Ready,
// or quorum.
//
// peers are checked in the given order, so callers that want stable diagnostics pass them ordered.
func IsTieBreakerOperational(
	tb *v1alpha1.ReplicatedVolumeReplica,
	peers []TieBreakerPeer,
	datameshRevision int64,
) (bool, string) {
	if tb == nil {
		return false, "replica object is gone"
	}
	if tb.DeletionTimestamp != nil {
		return false, "replica is terminating"
	}
	if tb.Status.DatameshRevision < datameshRevision {
		return false, fmt.Sprintf("datamesh revision %d applied, want %d",
			tb.Status.DatameshRevision, datameshRevision)
	}
	if !obju.StatusCondition(tb, v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType).
		IsTrue().ObservedGenerationCurrent().Eval() {
		return false, "DRBD is not configured"
	}

	for _, peer := range peers {
		if peer.ID == tb.ID() {
			// The TieBreaker is not its own peer (defensive: callers exclude it).
			continue
		}
		if !peerConnectionVerified(tb, peer.RVR, tb.ID(), peer.ID, datameshRevision, peerConnected) {
			return false, fmt.Sprintf("connection to %s is not confirmed", peer.displayName())
		}
	}
	return true, ""
}
