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
	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
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
	apply func(*globalContext, *ReplicaContext),
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
	apply func(*globalContext),
	confirm func(*globalContext, int64) dmte.ConfirmResult,
) *dmte.GlobalStepBuilder[*globalContext] {
	return dmte.GlobalStep(name, apply, confirm).
		DiagnosticConditions(v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType)
}

// ──────────────────────────────────────────────────────────────────────────────
// OnComplete callbacks
//

// onJoinComplete sets the completion message after an AddReplica plan finishes.
func onJoinComplete(_ *globalContext, rctx *ReplicaContext) {
	rctx.membershipMessage = "Joined datamesh successfully"
}

// onLeaveComplete sets the completion message after a RemoveReplica plan finishes.
func onLeaveComplete(_ *globalContext, rctx *ReplicaContext) {
	rctx.membershipMessage = "Left datamesh successfully"
}

// onForceRemoveComplete sets the completion message after a ForceRemoveReplica plan finishes.
func onForceRemoveComplete(_ *globalContext, rctx *ReplicaContext) {
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
// volume and are UpToDate (BackingVolumeUpToDate condition is True).
// D∅ members are excluded (they are voters but have no attached disk).
func upToDateDiskfulCount(gctx *globalContext) byte {
	var n byte
	for i := range gctx.allReplicas {
		rc := &gctx.allReplicas[i]
		if rc.member == nil || !rc.member.Type.IsVoter() || !rc.member.Type.HasBackingVolume() {
			continue
		}
		if rc.rvr != nil &&
			obju.StatusCondition(rc.rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateType).IsTrue().Eval() {
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

// voterCountPerZone returns per-zone voter counts (D + D∅).
func voterCountPerZone(gctx *globalContext) map[string]byte {
	m := make(map[string]byte)
	for i := range gctx.allReplicas {
		rc := &gctx.allReplicas[i]
		if rc.member != nil && rc.member.Type.IsVoter() {
			m[rc.member.Zone]++
		}
	}
	return m
}

// upToDateDiskfulCountPerZone returns per-zone UpToDate D counts.
func upToDateDiskfulCountPerZone(gctx *globalContext) map[string]byte {
	m := make(map[string]byte)
	for i := range gctx.allReplicas {
		rc := &gctx.allReplicas[i]
		if rc.member == nil || !rc.member.Type.IsVoter() || !rc.member.Type.HasBackingVolume() {
			continue
		}
		if rc.rvr != nil &&
			obju.StatusCondition(rc.rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeUpToDateType).IsTrue().Eval() {
			m[rc.member.Zone]++
		}
	}
	return m
}

// tbCountPerZone returns per-zone TieBreaker counts.
func tbCountPerZone(gctx *globalContext) map[string]byte {
	m := make(map[string]byte)
	for i := range gctx.allReplicas {
		rc := &gctx.allReplicas[i]
		if rc.member != nil && rc.member.Type == v1alpha1.DatameshMemberTypeTieBreaker {
			m[rc.member.Zone]++
		}
	}
	return m
}
