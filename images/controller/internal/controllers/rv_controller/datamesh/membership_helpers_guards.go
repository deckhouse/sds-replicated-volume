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

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/dmte"
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
	guardAddressesPopulated,
	guardNoMemberOnSameNode,
	guardRSPAvailable,
	guardNodeEligible,
}

// commonRemoveGuards are guards shared by all RemoveReplica plans.
// Typed as []any to be directly spreadable into PlanBuilder.Guards(...any).
var commonRemoveGuards = []any{
	guardNotAttached,
}

// leavingDGuards are guards for transitions that remove a D voter
// (RemoveReplica(D), ChangeReplicaType(D→...)).
//
// Plans MUST also add defense-in-depth guards explicitly:
//   - Voter parity: guardVotersEven or guardVotersOdd
//   - QMR: guardQMRNotTooHigh (no qmr change) or guardQMRLowerNeeded (qmr↓)
//   - Feature: guardShadowDiskfulSupported (sD variants)
var leavingDGuards = []any{
	guardVolumeAccessLocalForDemotion,
	guardGMDRPreserved,
	guardFTTPreserved,
	guardZoneGMDRPreserved,
	guardZoneFTTPreserved,
}

// leavingTBGuards are guards for transitions that remove a TB
// (RemoveReplica(TB), ChangeReplicaType(TB→...)).
var leavingTBGuards = []any{
	guardTBSufficient,
	guardZoneTBSufficient,
}

// ──────────────────────────────────────────────────────────────────────────────
// Shared Add guards
//

// guardRVNotDeleting blocks membership transitions if the ReplicatedVolume is being deleted.
func guardRVNotDeleting(gctx *globalContext, _ *ReplicaContext) dmte.GuardResult {
	if gctx.deletionTimestamp != nil {
		return dmte.GuardResult{
			Blocked: true,
			Message: "Will not join datamesh: volume is being deleted",
		}
	}
	return dmte.GuardResult{}
}

// guardAddressesPopulated blocks if the replica's RVR has no addresses yet.
func guardAddressesPopulated(_ *globalContext, rctx *ReplicaContext) dmte.GuardResult {
	if rctx.rvr == nil || len(rctx.rvr.Status.Addresses) == 0 {
		return dmte.GuardResult{
			Blocked: true,
			Message: "Waiting for replica addresses to be populated",
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
				Message: fmt.Sprintf("Will not join datamesh: %s member %s already present on node %s",
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
			Message: "Waiting for ReplicatedStoragePool to be available",
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
			Message: "Waiting for ReplicatedStoragePool to be available",
		}
	}
	if rctx.getEligibleNode() == nil {
		return dmte.GuardResult{
			Blocked: true,
			Message: fmt.Sprintf("Will not join datamesh: node %s is not in eligible nodes", rctx.nodeName),
		}
	}
	return dmte.GuardResult{}
}

// ──────────────────────────────────────────────────────────────────────────────
// Shared Remove guards
//

// guardNotAttached blocks if the member is currently attached.
func guardNotAttached(_ *globalContext, rctx *ReplicaContext) dmte.GuardResult {
	if rctx.member != nil && rctx.member.Attached {
		return dmte.GuardResult{
			Blocked: true,
			Message: "Cannot leave datamesh: replica is attached, detach required first",
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
			Message: "Cannot demote Diskful: volumeAccess=Local requires D on attached node",
		}
	}
	return dmte.GuardResult{}
}

// guardQMRNotTooHigh is a defense-in-depth guard that blocks voter removal
// if QMR is higher than configuration requires (config.GMDR + 1). Normally
// the dispatch selects a qmr↓ plan variant when QMR needs lowering; this
// guard catches unexpected inconsistencies.
// Plans with embedded qmr↓ don't use this guard — they handle lowering internally.
func guardQMRNotTooHigh(gctx *globalContext, _ *ReplicaContext) dmte.GuardResult {
	configGMDR := gctx.configuration.GuaranteedMinimumDataRedundancy
	maxQMR := configGMDR + 1
	if gctx.datamesh.QuorumMinimumRedundancy > maxQMR {
		return dmte.GuardResult{
			Blocked: true,
			Message: fmt.Sprintf("Voter removal blocked: qmr=%d exceeds configuration (GMDR=%d, expected qmr≤%d); waiting for qmr to be lowered",
				gctx.datamesh.QuorumMinimumRedundancy, configGMDR, maxQMR),
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
			Message: fmt.Sprintf("Would violate GMDR: ADR=%d, need > %d", adr, targetGMDR),
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
			Message: fmt.Sprintf("Would violate FTT: D_count=%d, need > %d", voters, dMin),
		}
	}
	return dmte.GuardResult{}
}

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

	for zone, zoneUTD := range perZone {
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
					"Would violate zone GMDR: losing zone %s after removal would leave %d UpToDate D, need > %d",
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

	for zone, zoneVoters := range votersPerZone {
		adjustedZoneVoters := zoneVoters
		if zone == removedZone && adjustedZoneVoters > 0 {
			adjustedZoneVoters--
		}
		dSurviving := votersAfter - adjustedZoneVoters
		tbSurviving := totalTB - tbPerZone[zone]

		if dSurviving >= qAfter {
			continue
		}
		if dSurviving == qAfter-1 && tbSurviving > 0 {
			continue
		}
		return dmte.GuardResult{
			Blocked: true,
			Message: fmt.Sprintf(
				"Would violate zone FTT: losing zone %s would leave %d D voters, q=%d, TB=%d",
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
			Message: fmt.Sprintf("TB required: D_count=%d even, FTT=%d = D/2", voters, targetFTT),
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
		Message: fmt.Sprintf("Would violate zone TB coverage for zone %s", removedZone),
	}
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
			Message: fmt.Sprintf("Defense: expected even voters, got %d", voters),
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
			Message: fmt.Sprintf("Defense: expected odd voters, got %d", voters),
		}
	}
	return dmte.GuardResult{}
}

// guardQMRRaiseNeeded blocks if baseline GMDR is already at or above config GMDR.
// Defense: plans with qmr↑ expect baseline.GMDR < config.GMDR.
func guardQMRRaiseNeeded(gctx *globalContext, _ *ReplicaContext) dmte.GuardResult {
	if gctx.baselineLayout.GuaranteedMinimumDataRedundancy >= gctx.configuration.GuaranteedMinimumDataRedundancy {
		return dmte.GuardResult{
			Blocked: true,
			Message: fmt.Sprintf("Defense: qmr↑ not needed, baseline GMDR=%d >= config GMDR=%d",
				gctx.baselineLayout.GuaranteedMinimumDataRedundancy,
				gctx.configuration.GuaranteedMinimumDataRedundancy),
		}
	}
	return dmte.GuardResult{}
}

// guardQMRLowerNeeded blocks if baseline GMDR is already at or below config GMDR.
// Defense: plans with qmr↓ expect baseline.GMDR > config.GMDR.
func guardQMRLowerNeeded(gctx *globalContext, _ *ReplicaContext) dmte.GuardResult {
	if gctx.baselineLayout.GuaranteedMinimumDataRedundancy <= gctx.configuration.GuaranteedMinimumDataRedundancy {
		return dmte.GuardResult{
			Blocked: true,
			Message: fmt.Sprintf("Defense: qmr↓ not needed, baseline GMDR=%d <= config GMDR=%d",
				gctx.baselineLayout.GuaranteedMinimumDataRedundancy,
				gctx.configuration.GuaranteedMinimumDataRedundancy),
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
			Message: "ShadowDiskful not supported (requires Flant DRBD extension)",
		}
	}
	return dmte.GuardResult{}
}
