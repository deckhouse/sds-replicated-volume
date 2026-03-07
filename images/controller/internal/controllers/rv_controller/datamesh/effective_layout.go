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
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/idset"
)

// updateEffectiveLayout computes the real-time protection level and health
// of the datamesh and writes the result into el in-place. Returns true if
// any field changed.
//
// Single-pass classification builds IDSets, then set algebra derives counts:
//
//   - Ready agent (fresh data): !isAgentNotReady && Quorum != nil.
//   - Voter reachable = own Quorum==true OR stale but peer sees Connected.
//   - Voter upToDate = own BackingVolume==UpToDate OR stale but peer sees UpToDate.
//   - TB reachable = agent ready OR stale but peer sees Connected.
//
// FTT/GMDR are set to nil when no voter members exist or no agents have
// fresh data. Counts and Message are always populated.
//
// Called unconditionally each reconciliation cycle (not gated by engine changed).
func updateEffectiveLayout(gctx *globalContext, el *v1alpha1.ReplicatedVolumeEffectiveLayout) bool {
	q := gctx.datamesh.quorum
	qmr := gctx.datamesh.quorumMinimumRedundancy

	// ── Single-pass classification ──────────────────────────────────────

	var (
		voters, tbs                 idset.IDSet // members by type
		ready                       idset.IDSet // agents with fresh data
		quorumTrue, diskUpToDate    idset.IDSet // own status of ready agents
		peerConnected, peerUpToDate idset.IDSet // peer observations from ready agents
	)

	for i := range gctx.allReplicas {
		rc := &gctx.allReplicas[i]
		if rc.member == nil || rc.rvr == nil {
			continue
		}

		switch {
		case rc.member.Type.IsVoter():
			voters.Add(rc.id)
		case rc.member.Type == v1alpha1.DatameshMemberTypeTieBreaker:
			tbs.Add(rc.id)
		default:
			continue
		}

		if isAgentNotReady(rc.rvr) || rc.rvr.Status.Quorum == nil {
			continue
		}
		ready.Add(rc.id)

		if *rc.rvr.Status.Quorum {
			quorumTrue.Add(rc.id)
		}
		if rc.rvr.Status.BackingVolume != nil &&
			rc.rvr.Status.BackingVolume.State == v1alpha1.DiskStateUpToDate {
			diskUpToDate.Add(rc.id)
		}
		for _, peer := range rc.rvr.Status.Peers {
			id := peer.ID()
			if peer.ConnectionState == v1alpha1.ConnectionStateConnected {
				peerConnected.Add(id)
			}
			if peer.BackingVolumeState == v1alpha1.DiskStateUpToDate {
				peerUpToDate.Add(id)
			}
		}
	}

	// ── Derive counts via set algebra ───────────────────────────────────

	staleVoters := voters.Difference(ready)
	staleTBs := tbs.Difference(ready)

	reachableVoters := voters.Intersect(quorumTrue).
		Union(staleVoters.Intersect(peerConnected))
	upToDateVoters := voters.Intersect(diskUpToDate).
		Union(staleVoters.Intersect(peerUpToDate))
	reachableTBs := tbs.Intersect(ready.Union(peerConnected))

	// ── Write counts ───────────────────────────────────────────────────

	changed := false

	changed = setInt8(&el.TotalVoters, int8(voters.Len())) || changed
	changed = setInt8(&el.ReachableVoters, int8(reachableVoters.Len())) || changed
	changed = setInt8(&el.UpToDateVoters, int8(upToDateVoters.Len())) || changed
	changed = setInt8(&el.TotalTieBreakers, int8(tbs.Len())) || changed
	changed = setInt8(&el.ReachableTieBreakers, int8(reachableTBs.Len())) || changed
	changed = setInt8(&el.StaleAgents, int8(staleVoters.Len()+staleTBs.Len())) || changed

	// ── Compute FTT/GMDR (when possible) ───────────────────────────────

	canCompute := !voters.IsEmpty() && !ready.IsEmpty()
	if canCompute {
		reachableD := el.ReachableVoters
		upToDateD := el.UpToDateVoters
		reachableTB := el.ReachableTieBreakers

		var tbBonus int8
		if reachableD%2 == 0 && reachableTB > 0 {
			tbBonus = 1
		}

		ftt := min(reachableD-int8(q)+tbBonus, upToDateD-int8(qmr))
		gmdr := min(upToDateD, int8(qmr)) - 1
		changed = setOptInt8(&el.FailuresToTolerate, &ftt) || changed
		changed = setOptInt8(&el.GuaranteedMinimumDataRedundancy, &gmdr) || changed
	} else {
		changed = setOptInt8(&el.FailuresToTolerate, nil) || changed
		changed = setOptInt8(&el.GuaranteedMinimumDataRedundancy, nil) || changed
	}

	// ── Build diagnostic message ───────────────────────────────────────

	changed = setEffectiveLayoutMessage(el, canCompute, staleVoters, staleTBs) || changed

	return changed
}

// setInt8 sets *dst = val, returns true if the value changed.
func setInt8(dst *int8, val int8) bool {
	if *dst == val {
		return false
	}
	*dst = val
	return true
}

// setOptInt8 sets *dst = val (pointer copy), returns true if the value changed.
// Both nil → no change. One nil, one non-nil → change. Both non-nil → compare values.
func setOptInt8(dst **int8, val *int8) bool {
	if *dst == nil && val == nil {
		return false
	}
	if *dst != nil && val != nil && **dst == *val {
		return false
	}
	*dst = val
	return true
}

// setEffectiveLayoutMessage builds a human-readable summary of the effective
// layout into a stack-allocated buffer and sets el.Message only if changed.
// Returns true if the message changed.
//
// Zero heap allocations on the no-change path (stack buffer + string comparison
// optimization). One allocation (string(buf)) only when the message differs.
func setEffectiveLayoutMessage(
	el *v1alpha1.ReplicatedVolumeEffectiveLayout,
	canCompute bool,
	staleVoters, staleTBs idset.IDSet,
) bool {
	// Stack-allocated buffer. Typical messages are 60-120 bytes;
	// 256 covers the worst case (32 stale voters + 32 stale TBs with IDs).
	var arr [256]byte
	buf := arr[:0]

	// Voter summary.
	if el.TotalVoters > 0 {
		buf = fmt.Appendf(buf, "%d/%d voters reachable", el.ReachableVoters, el.TotalVoters)
		if !staleVoters.IsEmpty() {
			buf = fmt.Appendf(buf, " (%d stale [", staleVoters.Len())
			buf = staleVoters.AppendString(buf)
			buf = append(buf, ']', ')')
		}
		buf = fmt.Appendf(buf, ", %d/%d UpToDate", el.UpToDateVoters, el.TotalVoters)
	} else {
		buf = append(buf, "No voters"...)
	}

	// TB summary.
	if el.TotalTieBreakers > 0 {
		buf = fmt.Appendf(buf, ", %d/%d TB reachable", el.ReachableTieBreakers, el.TotalTieBreakers)
		if !staleTBs.IsEmpty() {
			buf = fmt.Appendf(buf, " (%d stale [", staleTBs.Len())
			buf = staleTBs.AppendString(buf)
			buf = append(buf, ']', ')')
		}
	}

	// FTT/GMDR.
	switch {
	case canCompute:
		buf = fmt.Appendf(buf, "; FTT=%d, GMDR=%d", *el.FailuresToTolerate, *el.GuaranteedMinimumDataRedundancy)
	case el.TotalVoters == 0:
		buf = append(buf, "; FTT and GMDR unavailable: no voter members"...)
	default:
		buf = append(buf, "; FTT and GMDR unavailable: no fresh agent data"...)
	}

	// Compare without allocating (Go compiler optimizes string([]byte) == string).
	if string(buf) == el.Message {
		return false
	}
	el.Message = string(buf)
	return true
}

// isAgentNotReady returns true if the RVR's agent is not ready (DRBDConfigured
// condition has reason AgentNotReady). When true, rvr.Status fields (Peers,
// QuorumSummary, BackingVolume, Quorum) are stale and must not be trusted.
func isAgentNotReady(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
	return obju.StatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType).
		ReasonEqual(v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonAgentNotReady).Eval()
}
