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
	"slices"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// ──────────────────────────────────────────────────────────────────────────────
// Parameterized apply callbacks
//

// createMember returns an apply callback that creates a new DatameshMember
// with the given type. Zone is resolved from the RSP eligible node; addresses
// are cloned from the RVR status.
//
// Used by all AddReplica plans (replaces per-type create callbacks).
func createMember(memberType v1alpha1.DatameshMemberType) func(*globalContext, *ReplicaContext) {
	return func(_ *globalContext, rctx *ReplicaContext) {
		zone := ""
		if en := rctx.getEligibleNode(); en != nil {
			zone = en.ZoneName
		}

		rctx.member = &v1alpha1.DatameshMember{
			Name:      rctx.Name(),
			Type:      memberType,
			NodeName:  rctx.nodeName,
			Zone:      zone,
			Addresses: slices.Clone(rctx.rvr.Status.Addresses),
			Attached:  false,
		}
	}
}

// setType returns an apply callback that sets the member type.
// Covers ALL type conversions (A↔TB, A↔D∅, D∅↔D, sD∅↔sD, sD↔D, etc.)
// because the "from" type does not affect the mutation.
func setType(memberType v1alpha1.DatameshMemberType) func(*globalContext, *ReplicaContext) {
	return func(_ *globalContext, rctx *ReplicaContext) {
		rctx.member.Type = memberType
	}
}

// composeApply combines multiple apply callbacks into one.
// Used for composite steps (e.g., type change + q↑ in a single step).
func composeApply(fns ...func(*globalContext, *ReplicaContext)) func(*globalContext, *ReplicaContext) {
	return func(gctx *globalContext, rctx *ReplicaContext) {
		for _, fn := range fns {
			fn(gctx, rctx)
		}
	}
}

// setBackingVolumeFromRequest sets LVMVolumeGroupName and ThinPoolName
// on the member from the membership request. Used via composeApply on steps
// that first introduce a backing volume requirement (✦→D∅, ✦→sD∅, A→D∅, A→sD∅, TB→sD∅).
func setBackingVolumeFromRequest(_ *globalContext, rctx *ReplicaContext) {
	rctx.member.LVMVolumeGroupName = rctx.membershipRequest.Request.LVMVolumeGroupName
	rctx.member.LVMVolumeGroupThinPoolName = rctx.membershipRequest.Request.ThinPoolName
}

// clearBackingVolume clears LVMVolumeGroupName and ThinPoolName
// from the member. Used via composeApply on steps that transition from a
// disk-backed type to a diskless type (sD∅→A, sD∅→TB).
func clearBackingVolume(_ *globalContext, rctx *ReplicaContext) {
	rctx.member.LVMVolumeGroupName = ""
	rctx.member.LVMVolumeGroupThinPoolName = ""
}

// ──────────────────────────────────────────────────────────────────────────────
// Non-parameterized shared apply callbacks
//

// removeMember removes a member from the datamesh.
// Sets rctx.member to nil. If the replica has no RVR, also removes it from
// the global ID index (no member + no RVR = no reason to keep in the index).
// The engine bumps DatameshRevision after this callback.
// Reusable across all member types (A, TB, D, sD, etc.).
func removeMember(gctx *globalContext, rctx *ReplicaContext) {
	rctx.member = nil
	if rctx.rvr == nil {
		gctx.replicas[rctx.id] = nil
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Adapters
//

// asReplicaApply adapts a global-scoped apply callback for use in ReplicaStep.
func asReplicaApply(fn func(*globalContext)) func(*globalContext, *ReplicaContext) {
	return func(gctx *globalContext, _ *ReplicaContext) {
		fn(gctx)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// q/qmr apply callbacks (global-scoped)
//
// These mutate gctx.datamesh.Quorum / QuorumMinimumRedundancy.
// Global-scoped because they don't depend on the subject replica —
// they compute from the current state of gctx.allReplicas.
// Use asReplicaApply() when passing to ReplicaStep.

// raiseQ recomputes and raises q after a voter was added.
// q = max(floor(voters/2)+1, floor(minD/2)+1), where
// minD = baseline.FTT + baseline.GMDR + 1.
func raiseQ(gctx *globalContext) {
	voters := voterCount(gctx)
	minD := gctx.baselineLayout.FailuresToTolerate + gctx.baselineLayout.GuaranteedMinimumDataRedundancy + 1
	minQ := minD/2 + 1
	q := voters/2 + 1
	gctx.datamesh.Quorum = max(q, minQ)
}

// lowerQ recomputes and lowers q after a voter was removed.
// Same formula as raiseQ — the voter count in gctx.allReplicas already
// reflects the removal (from an earlier setType in the same composite step).
func lowerQ(gctx *globalContext) {
	voters := voterCount(gctx)
	minD := gctx.baselineLayout.FailuresToTolerate + gctx.baselineLayout.GuaranteedMinimumDataRedundancy + 1
	minQ := minD/2 + 1
	q := voters/2 + 1
	gctx.datamesh.Quorum = max(q, minQ)
}

// raiseQMR raises qmr to match the target GMDR from Configuration.
// qmr = target_GMDR + 1.
func raiseQMR(gctx *globalContext) {
	gctx.datamesh.QuorumMinimumRedundancy = gctx.configuration.GuaranteedMinimumDataRedundancy + 1
}

// lowerQMR lowers qmr to match the target GMDR from Configuration.
// qmr = target_GMDR + 1 (Configuration already has the lowered target).
func lowerQMR(gctx *globalContext) {
	gctx.datamesh.QuorumMinimumRedundancy = gctx.configuration.GuaranteedMinimumDataRedundancy + 1
}
