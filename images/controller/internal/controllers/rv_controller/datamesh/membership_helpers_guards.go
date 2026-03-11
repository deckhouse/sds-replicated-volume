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
	"strings"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/dmte"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/idset"
)

// ──────────────────────────────────────────────────────────────────────────────
// Guard groups
//
// Predefined slices for DRY in plan registration. Plan files use them like:
//
//	addReplica.Plan("access/v1").
//	    Guards(commonAddGuards[:]...).
//	    Guards(guardVolumeAccessNotLocal). // plan-specific
//	    ...

// commonAddGuards are guards shared by all AddReplica plans.
// Typed as []any to be directly spreadable into PlanBuilder.Guards(...any).
//
// AddReplica(D) plans MUST also add defense-in-depth guards explicitly:
//   - Voter parity: guardVotersEven or guardVotersOdd
//   - QMR: guardQMRRaiseNeeded (qmr↑ plans)
//   - Feature: guardShadowDiskfulSupported (sD variants)
var commonAddGuards = []any{
	guardRVNotDeleting,
	guardRSPAvailable,
	guardNodeEligible,
	guardNodeHasAllSystemNetworks,
	guardNoMemberOnSameNode,
	guardAddressesPopulated,
}

// commonRemoveGuards are guards shared by all RemoveReplica plans.
// Typed as []any to be directly spreadable into PlanBuilder.Guards(...any).
var commonRemoveGuards = []any{
	guardNotAttached,
}

// loseVoterGuards are guards for transitions where a replica loses voter status
// (RemoveReplica(D), ChangeReplicaType(D→...)).
//
// Plans MUST also add defense-in-depth guards explicitly:
//   - Voter parity: guardVotersEven or guardVotersOdd
//   - QMR: guardQMRLowerNeeded (qmr↓ plans)
//   - Feature: guardShadowDiskfulSupported (sD variants)
var loseVoterGuards = []any{
	guardVolumeAccessLocalForDemotion,
	guardGMDRPreserved,
	guardFTTPreserved,
	guardZoneGMDRPreserved,
	guardZoneFTTPreserved,
}

// loseTBGuards are guards for transitions where a replica loses TieBreaker status
// (RemoveReplica(TB), ChangeReplicaType(TB→...)).
var loseTBGuards = []any{
	guardTBSufficient,
	guardZoneTBSufficient,
}

// gainVoterGuards are guards for transitions where a replica gains voter status
// (AddReplica(D), ChangeReplicaType(→D)).
var gainVoterGuards = []any{
	guardZonalSameZone,
	guardTransZonalVoterPlacement,
}

// gainTBGuards are guards for transitions where a replica gains TieBreaker status
// (AddReplica(TB), ChangeReplicaType(→TB)).
var gainTBGuards = []any{
	guardZonalSameZone,
	guardTransZonalTBPlacement,
}

// ──────────────────────────────────────────────────────────────────────────────
// Backing volume capacity guard
//

// maxDiskMembers is the maximum number of members with a backing volume.
// DRBD metadata is configured for max 7 peers (8 replicas total, including self).
const maxDiskMembers = 8

// guardMaxDiskMembers blocks if adding another disk-bearing member would exceed
// the DRBD limit. Counts current disk-bearing members plus in-flight transitions
// that will produce a disk-bearing member (ChangeType to disk type, or AddReplica
// for a disk type currently in a non-disk vestibule state like A).
func guardMaxDiskMembers(gctx *globalContext, _ *ReplicaContext) dmte.GuardResult {
	var current, pending int
	for i := range gctx.allReplicas {
		rc := &gctx.allReplicas[i]
		if rc.member == nil {
			continue
		}
		if rc.member.Type.HasBackingVolume() ||
			rc.member.Type == v1alpha1.DatameshMemberTypeLiminalDiskful ||
			rc.member.Type == v1alpha1.DatameshMemberTypeLiminalShadowDiskful {
			current++
			continue
		}
		if rc.membershipTransition == nil {
			continue
		}
		// Non-disk member with ChangeType transition to a disk type.
		if rc.membershipTransition.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeReplicaType &&
			(rc.membershipTransition.ToReplicaType == v1alpha1.ReplicaTypeDiskful ||
				rc.membershipTransition.ToReplicaType == v1alpha1.ReplicaTypeShadowDiskful) {
			pending++
			continue
		}
		// Non-disk member with AddReplica for a disk type (A vestibule:
		// member created as A, will become D∅ in a later step).
		if rc.membershipTransition.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica &&
			(rc.membershipTransition.ReplicaType == v1alpha1.ReplicaTypeDiskful ||
				rc.membershipTransition.ReplicaType == v1alpha1.ReplicaTypeShadowDiskful) {
			pending++
		}
	}

	total := current + pending
	if total >= maxDiskMembers {
		return dmte.GuardResult{
			Blocked: true,
			Message: fmt.Sprintf("maximum disk members reached (%d/%d, %d current + %d pending)",
				total, maxDiskMembers, current, pending),
		}
	}
	return dmte.GuardResult{}
}

// ──────────────────────────────────────────────────────────────────────────────
// Shared Add guards
//

// guardRVNotDeleting blocks membership transitions if the ReplicatedVolume is being deleted.
func guardRVNotDeleting(gctx *globalContext, _ *ReplicaContext) dmte.GuardResult {
	if gctx.deletionTimestamp != nil {
		return dmte.GuardResult{
			Blocked: true,
			Message: "volume is being deleted",
		}
	}
	return dmte.GuardResult{}
}

// guardAddressesPopulated blocks if the replica's RVR has no addresses yet.
func guardAddressesPopulated(_ *globalContext, rctx *ReplicaContext) dmte.GuardResult {
	if rctx.rvr == nil || len(rctx.rvr.Status.Addresses) == 0 {
		return dmte.GuardResult{
			Blocked: true,
			Message: "waiting for replica addresses to be populated",
		}
	}
	return dmte.GuardResult{}
}

// guardNoMemberOnSameNode blocks if a datamesh member already exists on the
// same node as the replica.
func guardNoMemberOnSameNode(gctx *globalContext, rctx *ReplicaContext) dmte.GuardResult {
	for i := range gctx.allReplicas {
		rc := &gctx.allReplicas[i]
		if rc.member != nil && rc.nodeName == rctx.nodeName {
			return dmte.GuardResult{
				Blocked: true,
				Message: fmt.Sprintf("%s member %s already present on node %s",
					rc.member.Type, rc.member.Name, rc.nodeName),
			}
		}
	}
	return dmte.GuardResult{}
}

// guardRSPAvailable blocks if the ReplicatedStoragePool is not available.
func guardRSPAvailable(gctx *globalContext, _ *ReplicaContext) dmte.GuardResult {
	if gctx.rsp == nil {
		return dmte.GuardResult{
			Blocked: true,
			Message: "waiting for ReplicatedStoragePool to be available",
		}
	}
	return dmte.GuardResult{}
}

// guardNodeEligible blocks if the replica's node is not in the RSP eligible nodes.
// Uses lazy-cached eligibleNode on ReplicaContext.
func guardNodeEligible(_ *globalContext, rctx *ReplicaContext) dmte.GuardResult {
	if rctx.gctx.rsp == nil {
		return dmte.GuardResult{
			Blocked: true,
			Message: "waiting for ReplicatedStoragePool to be available",
		}
	}
	if rctx.getEligibleNode() == nil {
		return dmte.GuardResult{
			Blocked: true,
			Message: fmt.Sprintf("node %s is not in eligible nodes", rctx.nodeName),
		}
	}
	return dmte.GuardResult{}
}

// ──────────────────────────────────────────────────────────────────────────────
// Shared Remove guards
//

// guardNotAttached blocks if the member is attached or an Attach/Detach
// transition is in progress. Prevents removal while the attachment state
// is changing (an in-flight Attach would set Attached=true after the
// member is removed, leaving an orphan attachment).
func guardNotAttached(_ *globalContext, rctx *ReplicaContext) dmte.GuardResult {
	if rctx.member != nil && rctx.member.Attached {
		return dmte.GuardResult{
			Blocked: true,
			Message: "replica is attached, detach required first",
		}
	}
	if rctx.attachmentTransition != nil {
		return dmte.GuardResult{
			Blocked: true,
			Message: strings.ToLower(string(rctx.attachmentTransition.Type)) + " transition in progress",
		}
	}
	return dmte.GuardResult{}
}

// ──────────────────────────────────────────────────────────────────────────────
// Leaving-D guards
//
// Preconditions for removing a D voter (RemoveReplica(D), ChangeReplicaType(D→...)).

// guardVolumeAccessLocalForDemotion blocks if the member is attached and
// volumeAccess is Local (Diskful must stay on attached node in Local mode).
func guardVolumeAccessLocalForDemotion(gctx *globalContext, rctx *ReplicaContext) dmte.GuardResult {
	if rctx.member != nil && rctx.member.Attached &&
		gctx.configuration.VolumeAccess == v1alpha1.VolumeAccessLocal {
		return dmte.GuardResult{
			Blocked: true,
			Message: "volumeAccess=Local requires Diskful on attached node",
		}
	}
	return dmte.GuardResult{}
}

// guardGMDRPreserved blocks if removing this voter would violate the GMDR guarantee.
// Condition: ADR > target_GMDR, where ADR = UpToDate_D_count − 1.
func guardGMDRPreserved(gctx *globalContext, _ *ReplicaContext) dmte.GuardResult {
	targetGMDR := gctx.configuration.GuaranteedMinimumDataRedundancy
	utd := upToDateDiskfulCount(gctx)
	var adr byte
	if utd > 0 {
		adr = utd - 1
	}
	if adr <= targetGMDR {
		return dmte.GuardResult{
			Blocked: true,
			Message: fmt.Sprintf("would violate GMDR (ADR=%d, need > %d)", adr, targetGMDR),
		}
	}
	return dmte.GuardResult{}
}

// guardFTTPreserved blocks if removing this voter would violate the FTT guarantee.
// Condition: D_count > D_min, where D_min = target_FTT + target_GMDR + 1.
func guardFTTPreserved(gctx *globalContext, _ *ReplicaContext) dmte.GuardResult {
	targetFTT := gctx.configuration.FailuresToTolerate
	targetGMDR := gctx.configuration.GuaranteedMinimumDataRedundancy
	dMin := targetFTT + targetGMDR + 1
	voters := voterCount(gctx)
	if voters <= dMin {
		return dmte.GuardResult{
			Blocked: true,
			Message: fmt.Sprintf("would violate FTT (Diskful=%d, need > %d)", voters, dMin),
		}
	}
	return dmte.GuardResult{}
}

// ──────────────────────────────────────────────────────────────────────────────
// Zonal guards
//

// guardZonalSameZone blocks if the replica is in a zone that is not the
// primary zone for Zonal topology. The primary zone is the zone with the
// maximum number of voter members. If multiple zones tie for the maximum,
// all are acceptable. Only applies to Zonal topology.
func guardZonalSameZone(gctx *globalContext, rctx *ReplicaContext) dmte.GuardResult {
	if gctx.configuration.Topology != v1alpha1.TopologyZonal {
		return dmte.GuardResult{}
	}

	// Find primary zone(s): zone(s) with max voter count.
	perZone := voterCountPerZone(gctx)
	if len(perZone) == 0 {
		return dmte.GuardResult{} // no voters yet, any zone OK
	}

	var maxCount byte
	for _, zc := range perZone {
		if zc.Count > maxCount {
			maxCount = zc.Count
		}
	}

	// Determine replica zone: from member (ChangeType) or RSP (AddReplica).
	replicaZone := ""
	if rctx.member != nil {
		replicaZone = rctx.member.Zone
	} else if en := rctx.getEligibleNode(); en != nil {
		replicaZone = en.ZoneName
	} else {
		return dmte.GuardResult{} // RSP/node guards will block
	}

	// Check if replica's zone is among primary zones (max count).
	for _, zc := range perZone {
		if zc.Zone == replicaZone && zc.Count == maxCount {
			return dmte.GuardResult{}
		}
	}

	return dmte.GuardResult{
		Blocked: true,
		Message: fmt.Sprintf("zone %q is not the primary zone for Zonal topology", replicaZone),
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// TransZonal placement guards (gain)
//

// guardTransZonalVoterPlacement blocks adding a voter (D) into a zone if,
// after the addition, losing any zone would violate quorum (FTT) or data
// redundancy (GMDR). Only applies to TransZonal topology.
//
// For each zone z (including the added zone):
//
//	votersInZ = current voters in z (+ 1 if z == addedZone)
//	dSurviving = votersAfter - votersInZ
//	FTT: dSurviving >= qAfter, or dSurviving == qAfter-1 with TB tiebreaker
//	GMDR: dSurviving >= qmr
func guardTransZonalVoterPlacement(gctx *globalContext, rctx *ReplicaContext) dmte.GuardResult {
	if gctx.configuration.Topology != v1alpha1.TopologyTransZonal {
		return dmte.GuardResult{}
	}

	// Determine added zone.
	addedZone := ""
	if rctx.member != nil {
		addedZone = rctx.member.Zone
	} else if en := rctx.getEligibleNode(); en != nil {
		addedZone = en.ZoneName
	} else {
		return dmte.GuardResult{} // RSP/node guards will block
	}

	voters := voterCount(gctx)
	votersAfter := voters + 1
	qAfter := computeTargetQ(votersAfter)
	qmr := gctx.datamesh.quorumMinimumRedundancy

	votersPerZone := voterCountPerZone(gctx)
	tbPerZone := tbCountPerZone(gctx)
	totalTB := tbCount(gctx)

	// Check every zone (including addedZone which may be new).
	checked := false
	for _, zc := range votersPerZone {
		zone := zc.Zone
		votersInZ := zc.Count
		if zone == addedZone {
			votersInZ++
			checked = true
		}

		dSurviving := votersAfter - votersInZ
		tbSurviving := totalTB - findZoneCount(tbPerZone, zone)

		// FTT check.
		if dSurviving < qAfter {
			if dSurviving != qAfter-1 || tbSurviving == 0 {
				return dmte.GuardResult{
					Blocked: true,
					Message: fmt.Sprintf(
						"adding to zone %q would violate zone FTT (losing zone %q would leave %d Diskful voters, q=%d, TB=%d)",
						addedZone, zone, dSurviving, qAfter, tbSurviving),
				}
			}
		}

		// GMDR check.
		if dSurviving < qmr {
			return dmte.GuardResult{
				Blocked: true,
				Message: fmt.Sprintf(
					"adding to zone %q would violate zone GMDR (losing zone %q would leave %d Diskful voters, need >= %d)",
					addedZone, zone, dSurviving, qmr),
			}
		}
	}

	// If addedZone is new (not in votersPerZone), check it separately.
	if !checked {
		// New zone with 1 voter after addition. Losing it: dSurviving = votersAfter - 1 = voters.
		// This is always safe (we had `voters` before, adding 1 to a fresh zone can't hurt others).
		// But still check for completeness.
		dSurviving := votersAfter - 1
		if dSurviving < qAfter {
			tbSurviving := totalTB - findZoneCount(tbPerZone, addedZone)
			if dSurviving != qAfter-1 || tbSurviving == 0 {
				return dmte.GuardResult{
					Blocked: true,
					Message: fmt.Sprintf(
						"adding to new zone %q would violate zone FTT (losing it would leave %d Diskful voters, q=%d)",
						addedZone, dSurviving, qAfter),
				}
			}
		}
		if dSurviving < qmr {
			return dmte.GuardResult{
				Blocked: true,
				Message: fmt.Sprintf(
					"adding to new zone %q would violate zone GMDR (losing it would leave %d Diskful voters, need >= %d)",
					addedZone, dSurviving, qmr),
			}
		}
	}

	return dmte.GuardResult{}
}

// guardTransZonalTBPlacement blocks adding a TB to a zone that already has
// more than 1 D voter. If TB shares a zone with 2D, losing that zone removes
// 2D + TB — the tiebreaker is gone precisely when it's most needed.
// Only applies to TransZonal topology.
func guardTransZonalTBPlacement(gctx *globalContext, rctx *ReplicaContext) dmte.GuardResult {
	if gctx.configuration.Topology != v1alpha1.TopologyTransZonal {
		return dmte.GuardResult{}
	}

	// Determine TB zone.
	tbZone := ""
	if rctx.member != nil {
		tbZone = rctx.member.Zone
	} else if en := rctx.getEligibleNode(); en != nil {
		tbZone = en.ZoneName
	} else {
		return dmte.GuardResult{} // RSP/node guards will block
	}

	votersInTBZone := findZoneCount(voterCountPerZone(gctx), tbZone)
	if votersInTBZone > 1 {
		return dmte.GuardResult{
			Blocked: true,
			Message: fmt.Sprintf(
				"zone %q already has %d Diskful voters (TB zone must have at most 1)",
				tbZone, votersInTBZone),
		}
	}

	return dmte.GuardResult{}
}

// ──────────────────────────────────────────────────────────────────────────────
// TransZonal guards (lose)
//

// guardZoneGMDRPreserved blocks if removing this voter would cause any zone
// loss to violate the GMDR guarantee. Only applies to TransZonal topology.
//
// For each zone z:
//
//	UpToDate_surviving = (UpToDate_D_count − 1) − UpToDate_in_zone(z)
//	  (adjusted if z is the zone of the removed D)
//	if UpToDate_surviving <= target_GMDR → blocked
func guardZoneGMDRPreserved(gctx *globalContext, rctx *ReplicaContext) dmte.GuardResult {
	if gctx.configuration.Topology != v1alpha1.TopologyTransZonal {
		return dmte.GuardResult{}
	}

	targetGMDR := gctx.configuration.GuaranteedMinimumDataRedundancy
	totalUTD := upToDateDiskfulCount(gctx)
	perZone := upToDateDiskfulCountPerZone(gctx)
	removedZone := ""
	if rctx.member != nil {
		removedZone = rctx.member.Zone
	}

	for _, zc := range perZone {
		zone, zoneUTD := zc.Zone, zc.Count
		adjustedZoneUTD := zoneUTD
		if zone == removedZone {
			if adjustedZoneUTD > 0 {
				adjustedZoneUTD--
			}
		}
		var adjustedTotal byte
		if totalUTD > 0 {
			adjustedTotal = totalUTD - 1
		}
		surviving := adjustedTotal - adjustedZoneUTD
		if surviving <= targetGMDR {
			return dmte.GuardResult{
				Blocked: true,
				Message: fmt.Sprintf(
					"would violate zone GMDR (losing zone %q after removal would leave %d UpToDate Diskful, need > %d)",
					zone, surviving, targetGMDR),
			}
		}
	}
	return dmte.GuardResult{}
}

// guardZoneFTTPreserved blocks if removing this voter would cause any zone
// loss to violate quorum. Only applies to TransZonal topology.
//
// For each zone z:
//
//	D_surviving = (D_count − 1) − D_in_zone(z)
//	  (adjusted if z is the zone of the removed D)
//	TB_surviving = TB_count − TB_in_zone(z)
//	q_after = floor((D_count − 1) / 2) + 1
//	Quorum holds if D_surviving >= q_after, or D_surviving == q_after−1 with TB_surviving > 0
func guardZoneFTTPreserved(gctx *globalContext, rctx *ReplicaContext) dmte.GuardResult {
	if gctx.configuration.Topology != v1alpha1.TopologyTransZonal {
		return dmte.GuardResult{}
	}

	voters := voterCount(gctx)
	if voters == 0 {
		return dmte.GuardResult{}
	}
	votersAfter := voters - 1
	qAfter := computeTargetQ(votersAfter)

	votersPerZone := voterCountPerZone(gctx)
	tbPerZone := tbCountPerZone(gctx)
	totalTB := tbCount(gctx)
	removedZone := ""
	if rctx.member != nil {
		removedZone = rctx.member.Zone
	}

	for _, zc := range votersPerZone {
		zone, zoneVoters := zc.Zone, zc.Count
		adjustedZoneVoters := zoneVoters
		if zone == removedZone && adjustedZoneVoters > 0 {
			adjustedZoneVoters--
		}
		dSurviving := votersAfter - adjustedZoneVoters
		tbSurviving := totalTB - findZoneCount(tbPerZone, zone)

		if dSurviving >= qAfter {
			continue
		}
		if dSurviving == qAfter-1 && tbSurviving > 0 {
			continue
		}
		return dmte.GuardResult{
			Blocked: true,
			Message: fmt.Sprintf(
				"would violate zone FTT (losing zone %q would leave %d Diskful voters, q=%d, TB=%d)",
				zone, dSurviving, qAfter, tbSurviving),
		}
	}
	return dmte.GuardResult{}
}

// ──────────────────────────────────────────────────────────────────────────────
// Leaving-TB guards
//
// Preconditions for removing a TB (RemoveReplica(TB), ChangeReplicaType(TB→...)).

// guardTBSufficient blocks if removing this TB would leave fewer TBs than required.
// TB_min = 1 if D_count is even AND target_FTT == D_count/2, else 0.
func guardTBSufficient(gctx *globalContext, _ *ReplicaContext) dmte.GuardResult {
	voters := voterCount(gctx)
	tbs := tbCount(gctx)
	targetFTT := gctx.configuration.FailuresToTolerate

	var tbMin byte
	if voters%2 == 0 && voters > 0 && targetFTT == voters/2 {
		tbMin = 1
	}

	if tbs <= tbMin {
		return dmte.GuardResult{
			Blocked: true,
			Message: fmt.Sprintf("TieBreaker required for quorum (Diskful=%d even, FTT=%d)", voters, targetFTT),
		}
	}
	return dmte.GuardResult{}
}

// guardZoneTBSufficient blocks if removing this TB would violate zone-level TB
// coverage in TransZonal topology. After removal, each zone that needs TB
// coverage must still have it.
func guardZoneTBSufficient(gctx *globalContext, rctx *ReplicaContext) dmte.GuardResult {
	if gctx.configuration.Topology != v1alpha1.TopologyTransZonal {
		return dmte.GuardResult{}
	}

	voters := voterCount(gctx)
	targetFTT := gctx.configuration.FailuresToTolerate

	// TB is only required when D is even and FTT == D/2.
	if voters%2 != 0 || voters == 0 || targetFTT != voters/2 {
		return dmte.GuardResult{}
	}

	// Check that after removing this TB, at least one TB remains.
	tbs := tbCount(gctx)
	if tbs > 1 {
		// Multiple TBs: removing one still leaves coverage.
		return dmte.GuardResult{}
	}

	// Last TB — cannot remove without a replacement already in place.
	removedZone := ""
	if rctx.member != nil {
		removedZone = rctx.member.Zone
	}
	return dmte.GuardResult{
		Blocked: true,
		Message: fmt.Sprintf("would violate zone TB coverage (zone %q)", removedZone),
	}
}

// forceRemoveGuards are guards shared by all ForceRemoveReplica plans.
var forceRemoveGuards = []any{
	guardNotAttached,
	guardMemberUnreachable,
}

// ──────────────────────────────────────────────────────────────────────────────
// ForceRemove guards
//

// guardMemberUnreachable blocks force-removal if any member with a ready agent
// reports a Connected DRBD connection to the subject. Prevents accidental
// force-removal of a member that is actually alive and participating.
func guardMemberUnreachable(gctx *globalContext, rctx *ReplicaContext) dmte.GuardResult {
	memberName := rctx.Name()
	var connectedFrom idset.IDSet

	for i := range gctx.allReplicas {
		rc := &gctx.allReplicas[i]
		if rc.member == nil || rc.rvr == nil || rc.id == rctx.ID() {
			continue
		}

		// Skip replicas with stale status (agent not ready — Peers may be outdated).
		if obju.StatusCondition(rc.rvr, v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType).
			ReasonEqual(v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonAgentNotReady).Eval() {
			continue
		}

		for _, peer := range rc.rvr.Status.Peers {
			if peer.Name == memberName && peer.ConnectionState == v1alpha1.ConnectionStateConnected {
				connectedFrom.Add(rc.id)
				break
			}
		}
	}

	if !connectedFrom.IsEmpty() {
		return dmte.GuardResult{
			Blocked: true,
			Message: fmt.Sprintf("member is reachable (connected from %d replica(s): [%s])",
				connectedFrom.Len(), connectedFrom),
		}
	}

	return dmte.GuardResult{}
}

// ──────────────────────────────────────────────────────────────────────────────
// Defense-in-depth guards
//
// These guards verify dispatch decisions. They should never fire in normal
// operation — dispatch already guarantees correct plan selection. They exist
// as safety nets against dispatch bugs or future refactoring regressions.

// guardVotersEven blocks if the current voter count is not even.
// Defense: plans without q↑/q↓ expect even voters (even→odd transition).
func guardVotersEven(gctx *globalContext, _ *ReplicaContext) dmte.GuardResult {
	voters := voterCount(gctx)
	if voters%2 != 0 {
		return dmte.GuardResult{
			Blocked: true,
			Message: fmt.Sprintf("internal check: expected even voter count, got %d", voters),
		}
	}
	return dmte.GuardResult{}
}

// guardVotersOdd blocks if the current voter count is not odd.
// Defense: plans with q↑/q↓ expect odd voters (odd→even transition).
func guardVotersOdd(gctx *globalContext, _ *ReplicaContext) dmte.GuardResult {
	voters := voterCount(gctx)
	if voters%2 == 0 {
		return dmte.GuardResult{
			Blocked: true,
			Message: fmt.Sprintf("internal check: expected odd voter count, got %d", voters),
		}
	}
	return dmte.GuardResult{}
}

// guardQMRRaiseNeeded blocks if qmr is already at or above the target (config.GMDR + 1).
// Defense: plans with qmr↑ expect qmr < config.GMDR + 1.
func guardQMRRaiseNeeded(gctx *globalContext, _ *ReplicaContext) dmte.GuardResult {
	targetQMR := gctx.configuration.GuaranteedMinimumDataRedundancy + 1
	if gctx.datamesh.quorumMinimumRedundancy >= targetQMR {
		return dmte.GuardResult{
			Blocked: true,
			Message: fmt.Sprintf("internal check: qmr raise not needed (qmr=%d >= target %d)",
				gctx.datamesh.quorumMinimumRedundancy, targetQMR),
		}
	}
	return dmte.GuardResult{}
}

// guardQMRLowerNeeded blocks if qmr is already at or below the target (config.GMDR + 1).
// Defense: plans with qmr↓ expect qmr > config.GMDR + 1.
func guardQMRLowerNeeded(gctx *globalContext, _ *ReplicaContext) dmte.GuardResult {
	targetQMR := gctx.configuration.GuaranteedMinimumDataRedundancy + 1
	if gctx.datamesh.quorumMinimumRedundancy <= targetQMR {
		return dmte.GuardResult{
			Blocked: true,
			Message: fmt.Sprintf("internal check: qmr lower not needed (qmr=%d <= target %d)",
				gctx.datamesh.quorumMinimumRedundancy, targetQMR),
		}
	}
	return dmte.GuardResult{}
}

// ──────────────────────────────────────────────────────────────────────────────
// Feature flag guards
//

// guardShadowDiskfulSupported blocks if the ShadowDiskful feature is not available.
// Requires the Flant DRBD kernel extension with the non-voting disk option.
// Used by AddReplica(sD) and AddReplica(D) via sD paths.
func guardShadowDiskfulSupported(gctx *globalContext, _ *ReplicaContext) dmte.GuardResult {
	if !gctx.features.ShadowDiskful {
		return dmte.GuardResult{
			Blocked: true,
			Message: "ShadowDiskful not supported (requires Flant DRBD extension, non-voting disk option)",
		}
	}
	return dmte.GuardResult{}
}
