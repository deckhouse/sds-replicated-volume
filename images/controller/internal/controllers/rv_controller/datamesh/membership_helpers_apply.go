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
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/dmte"
)

// ──────────────────────────────────────────────────────────────────────────────
// Parameterized apply callbacks
//

// createMember returns an apply callback that creates a new DatameshMember
// with the given type. Zone is resolved from the RSP eligible node; addresses
// are cloned from the RVR status. Always returns true (always creates).
func createMember(memberType v1alpha1.DatameshMemberType) func(*globalContext, *ReplicaContext) bool {
	return func(_ *globalContext, rctx *ReplicaContext) bool {
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
		return true
	}
}

// setType returns an apply callback that sets the member type.
// Returns false if the type is already the target (no-op).
// Key no-op case: Remove(D) step `D→D∅` when member is already LiminalDiskful.
func setType(memberType v1alpha1.DatameshMemberType) func(*globalContext, *ReplicaContext) bool {
	return func(_ *globalContext, rctx *ReplicaContext) bool {
		return dmte.SetChanged(&rctx.member.Type, memberType)
	}
}

// setBackingVolumeFromRequest sets LVMVolumeGroupName and ThinPoolName
// on the member from the membership request. Returns false if both already set.
func setBackingVolumeFromRequest(_ *globalContext, rctx *ReplicaContext) bool {
	c1 := dmte.SetChanged(&rctx.member.LVMVolumeGroupName, rctx.membershipRequest.Request.LVMVolumeGroupName)
	c2 := dmte.SetChanged(&rctx.member.LVMVolumeGroupThinPoolName, rctx.membershipRequest.Request.ThinPoolName)
	return c1 || c2
}

// clearBackingVolume clears LVMVolumeGroupName and ThinPoolName from the member.
// Returns false if both already empty.
func clearBackingVolume(_ *globalContext, rctx *ReplicaContext) bool {
	c1 := dmte.SetChanged(&rctx.member.LVMVolumeGroupName, "")
	c2 := dmte.SetChanged(&rctx.member.LVMVolumeGroupThinPoolName, "")
	return c1 || c2
}

// ──────────────────────────────────────────────────────────────────────────────
// Non-parameterized shared apply callbacks
//

// removeMember removes a member from the datamesh. Always returns true.
func removeMember(gctx *globalContext, rctx *ReplicaContext) bool {
	rctx.member = nil
	if rctx.rvr == nil {
		gctx.replicas[rctx.id] = nil
	}
	return true
}

// ──────────────────────────────────────────────────────────────────────────────
// q/qmr apply callbacks (global-scoped)
//
// These mutate gctx.datamesh.quorum / quorumMinimumRedundancy.
// Global-scoped because they don't depend on the subject replica —
// they compute from the current state of gctx.allReplicas.
// Use asReplicaApply() when passing to ReplicaStep.

// raiseQ increments q by 1. Always returns true (guards guarantee change needed).
func raiseQ(gctx *globalContext) bool {
	gctx.datamesh.quorum++
	return true
}

// lowerQ decrements q by 1. Always returns true (guards guarantee change needed).
func lowerQ(gctx *globalContext) bool {
	gctx.datamesh.quorum--
	return true
}

// raiseQMR increments qmr by 1. Always returns true (guards guarantee change needed).
func raiseQMR(gctx *globalContext) bool {
	gctx.datamesh.quorumMinimumRedundancy++
	return true
}

// lowerQMR decrements qmr by 1. Always returns true (guards guarantee change needed).
func lowerQMR(gctx *globalContext) bool {
	gctx.datamesh.quorumMinimumRedundancy--
	return true
}

// setCorrectQ sets q from computeCorrectQuorum.
// Returns false if q is already correct (no-op in ChangeQuorum when only qmr differs).
func setCorrectQ(gctx *globalContext) bool {
	q, _ := computeCorrectQuorum(gctx)
	return dmte.SetChanged(&gctx.datamesh.quorum, q)
}

// setCorrectQMR sets qmr from computeCorrectQuorum.
// Returns false if qmr is already correct (no-op in ChangeQuorum when only q differs).
func setCorrectQMR(gctx *globalContext) bool {
	_, qmr := computeCorrectQuorum(gctx)
	return dmte.SetChanged(&gctx.datamesh.quorumMinimumRedundancy, qmr)
}
