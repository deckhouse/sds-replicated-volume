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
	"slices"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/dmte"
)

// ──────────────────────────────────────────────────────────────────────────────
// Step constructors
//
// mrStep / mgStep create ReplicaStep / GlobalStep with the standard membership
// DiagnosticConditions pre-applied. Callers may chain additional builder
// methods (e.g. DiagnosticSkipError) on the returned builder.

// mrStep creates a membership ReplicaStep with standard DiagnosticConditions.
func mrStep(
	name string,
	apply func(*globalContext, *ReplicaContext) bool,
	confirm func(*globalContext, *ReplicaContext, int64) dmte.ConfirmResult,
) *dmte.ReplicaStepBuilder[*globalContext, *ReplicaContext] {
	return dmte.ReplicaStep(name, apply, confirm).
		DiagnosticConditions(v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType)
}

// mgStep creates a membership GlobalStep with standard DiagnosticConditions.
//
//nolint:unparam // name is currently always "qmr↑" but will vary with future plans
func mgStep(
	name string,
	apply func(*globalContext) bool,
	confirm func(*globalContext, int64) dmte.ConfirmResult,
) *dmte.GlobalStepBuilder[*globalContext] {
	return dmte.GlobalStep(name, apply, confirm).
		DiagnosticConditions(v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType)
}

// ──────────────────────────────────────────────────────────────────────────────
// Init callbacks
//

// setReplicaType returns an Init callback that sets transition.ReplicaType.
// Used by AddReplica, RemoveReplica, and ForceRemoveReplica plans.
func setReplicaType(rt v1alpha1.ReplicaType) func(*globalContext, *ReplicaContext, *dmte.Transition) {
	return func(_ *globalContext, _ *ReplicaContext, t *dmte.Transition) {
		t.ReplicaType = rt
	}
}

// setReplicaFromToType returns an Init callback that sets
// transition.FromReplicaType and transition.ToReplicaType.
// Used by ChangeReplicaType plans.
func setReplicaFromToType(from, to v1alpha1.ReplicaType) func(*globalContext, *ReplicaContext, *dmte.Transition) {
	return func(_ *globalContext, _ *ReplicaContext, t *dmte.Transition) {
		t.FromReplicaType = from
		t.ToReplicaType = to
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// OnComplete callbacks
//

// onJoinComplete sets the completion message after an AddReplica plan finishes.
func onJoinComplete(_ *globalContext, rctx *ReplicaContext) {
	rctx.membershipMessage = "Joined datamesh successfully"
}

// onLeaveComplete sets the completion message after a RemoveReplica plan finishes.
// rctx may be nil if the member was removed from the context index (see onForceRemoveComplete).
func onLeaveComplete(_ *globalContext, rctx *ReplicaContext) {
	if rctx == nil {
		return
	}
	rctx.membershipMessage = "Left datamesh successfully"
}

// onForceRemoveComplete sets the completion message after a ForceRemoveReplica plan finishes.
// rctx may be nil if the member was removed from the context index (removeMember sets
// gctx.replicas[id] = nil when the RVR is also gone). This happens when ForceRemove
// completes within the same Process() call via the outer settle-dispatch loop.
func onForceRemoveComplete(_ *globalContext, rctx *ReplicaContext) {
	if rctx == nil {
		return
	}
	rctx.membershipMessage = "Force-removed from datamesh"
}

// onChangeTypeComplete sets the completion message after a ChangeReplicaType plan finishes.
func onChangeTypeComplete(_ *globalContext, rctx *ReplicaContext) {
	rctx.membershipMessage = "Replica type changed successfully"
}

// ──────────────────────────────────────────────────────────────────────────────
// Computation helpers
//

// voterCount returns the number of voter members (D + D∅).
func voterCount(gctx *globalContext) byte {
	var n byte
	for i := range gctx.allReplicas {
		if m := gctx.allReplicas[i].member; m != nil && m.Type.IsVoter() {
			n++
		}
	}
	return n
}

// upToDateDiskfulCount returns the number of voter members that have a backing
// volume and are UpToDate (BackingVolume.State == UpToDate).
// D∅ members are excluded (they are voters but have no attached disk).
func upToDateDiskfulCount(gctx *globalContext) byte {
	var n byte
	for i := range gctx.allReplicas {
		rc := &gctx.allReplicas[i]
		if rc.member == nil || !rc.member.Type.IsVoter() || !rc.member.Type.HasBackingVolume() {
			continue
		}
		if rc.rvr != nil && rc.rvr.Status.BackingVolume != nil &&
			rc.rvr.Status.BackingVolume.State == v1alpha1.DiskStateUpToDate {
			n++
		}
	}
	return n
}

// tbCount returns the number of TieBreaker members.
func tbCount(gctx *globalContext) byte {
	var n byte
	for i := range gctx.allReplicas {
		if m := gctx.allReplicas[i].member; m != nil && m.Type == v1alpha1.DatameshMemberTypeTieBreaker {
			n++
		}
	}
	return n
}

// computeTargetQ computes quorum threshold from voter count: floor(voters/2) + 1.
func computeTargetQ(voters byte) byte {
	return voters/2 + 1
}

// zoneCount is a zone name + count pair returned by per-zone count helpers.
// Sorted by Zone for deterministic iteration (stable guard messages).
type zoneCount struct {
	Zone  string
	Count byte
}

// findZoneCount returns the count for the given zone name, or 0 if not found.
// Linear scan — max 5 zones.
func findZoneCount(zones []zoneCount, zone string) byte {
	for _, zc := range zones {
		if zc.Zone == zone {
			return zc.Count
		}
	}
	return 0
}

// countPerZone counts members matching include, grouped by zone.
// Returns a sorted []zoneCount (by zone name). No intermediate map —
// builds the slice directly via linear scan (max 5 zones × 8 members).
func countPerZone(gctx *globalContext, include func(*ReplicaContext) bool) []zoneCount {
	var zcs []zoneCount
	for i := range gctx.allReplicas {
		rc := &gctx.allReplicas[i]
		if !include(rc) {
			continue
		}
		zone := rc.member.Zone
		found := false
		for j := range zcs {
			if zcs[j].Zone == zone {
				zcs[j].Count++
				found = true
				break
			}
		}
		if !found {
			zcs = append(zcs, zoneCount{Zone: zone, Count: 1})
		}
	}
	slices.SortFunc(zcs, func(a, b zoneCount) int {
		return cmp.Compare(a.Zone, b.Zone)
	})
	return zcs
}

// voterCountPerZone returns per-zone voter counts (D + D∅), sorted by zone name.
func voterCountPerZone(gctx *globalContext) []zoneCount {
	return countPerZone(gctx, func(rc *ReplicaContext) bool {
		return rc.member != nil && rc.member.Type.IsVoter()
	})
}

// upToDateDiskfulCountPerZone returns per-zone UpToDate D counts, sorted by zone name.
func upToDateDiskfulCountPerZone(gctx *globalContext) []zoneCount {
	return countPerZone(gctx, func(rc *ReplicaContext) bool {
		if rc.member == nil || !rc.member.Type.IsVoter() || !rc.member.Type.HasBackingVolume() {
			return false
		}
		return rc.rvr != nil && rc.rvr.Status.BackingVolume != nil &&
			rc.rvr.Status.BackingVolume.State == v1alpha1.DiskStateUpToDate
	})
}

// tbCountPerZone returns per-zone TieBreaker counts, sorted by zone name.
func tbCountPerZone(gctx *globalContext) []zoneCount {
	return countPerZone(gctx, func(rc *ReplicaContext) bool {
		return rc.member != nil && rc.member.Type == v1alpha1.DatameshMemberTypeTieBreaker
	})
}
