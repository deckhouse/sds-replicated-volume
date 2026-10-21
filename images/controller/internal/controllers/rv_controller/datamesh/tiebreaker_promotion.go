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
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/dmte"
)

// IsR2ToR3Promotion identifies the layout intent before a replica request exists.
// Layout convergence and quorum reconciliation share it so their ordering cannot
// raise qmr before the existing TB has acquired a synchronized disk.
func IsR2ToR3Promotion(rv *v1alpha1.ReplicatedVolume) bool {
	if rv.Status.Configuration == nil || rv.Status.Configuration.FailuresToTolerate != 1 ||
		rv.Status.Configuration.GuaranteedMinimumDataRedundancy != 1 {
		return false
	}
	var diskful, tb int
	for _, member := range rv.Status.Datamesh.Members {
		if member.Type.IsVoter() {
			diskful++
		}
		if member.Type == v1alpha1.DatameshMemberTypeTieBreaker {
			tb++
		}
	}
	return diskful == 2 && tb == 1
}

func isR2ToR3Promotion(gctx *globalContext) bool {
	return gctx.configuration.FailuresToTolerate == 1 &&
		gctx.configuration.GuaranteedMinimumDataRedundancy == 1 &&
		voterCount(gctx) == 2 && tbCount(gctx) == 1
}

// TieBreakerPromotionReadiness is the common source-health and topology check
// used before layout convergence reserves a TB and again at DMTE admission.
// Storage preparation and conflicting pending replicas are admission-only checks:
// layout convergence never reserves the TB while another diskful copy is in flight.
func TieBreakerPromotionReadiness(rv *v1alpha1.ReplicatedVolume, rvrs []*v1alpha1.ReplicatedVolumeReplica, name string) (bool, string) {
	p := buildContexts(rv, nil, rvrs, nil, FeatureFlags{})
	for i := range p.Global().allReplicas {
		rctx := &p.Global().allReplicas[i]
		if rctx.name == name {
			result := guardPromotionSource(p.Global(), rctx)
			return !result.Blocked, result.Message
		}
	}
	return false, "tie-breaker replica is missing"
}

func guardPromotionSource(gctx *globalContext, rctx *ReplicaContext) dmte.GuardResult {
	if rctx.member == nil || rctx.member.Type != v1alpha1.DatameshMemberTypeTieBreaker {
		return dmte.GuardResult{Blocked: true, Message: "member is not a tie-breaker"}
	}
	if gctx.datamesh.quorum != computeTargetQ(voterCount(gctx)) {
		return dmte.GuardResult{Blocked: true, Message: "waiting for the current voting quorum"}
	}
	// Only the 2D+TB -> 3D promotion replaces the last TB's contribution
	// without changing q. Other layouts retain the original loss-of-TB guards.
	if !isR2ToR3Promotion(gctx) {
		if result := guardTBSufficient(gctx, rctx); result.Blocked {
			return result
		}
		if result := guardZoneTBSufficient(gctx, rctx); result.Blocked {
			return result
		}
	}
	// In TransZonal R2/R3 each existing participant must be in a distinct
	// zone. Counting the departing TB as well as its future vote would make
	// the general add-voter guard too permissive for a same-zone promotion.
	if isR2ToR3Promotion(gctx) && gctx.configuration.Topology == v1alpha1.TopologyTransZonal {
		zones := make(map[string]bool, 3)
		for i := range gctx.allReplicas {
			member := gctx.allReplicas[i].member
			if member == nil || (!member.Type.IsVoter() && member.Type != v1alpha1.DatameshMemberTypeTieBreaker) {
				continue
			}
			if member.Zone == "" || zones[member.Zone] {
				return dmte.GuardResult{Blocked: true, Message: "in-place promotion requires three distinct zones"}
			}
			zones[member.Zone] = true
		}
	}
	if result := guardZonalSameZone(gctx, rctx); result.Blocked {
		return result
	}
	for i := range gctx.allReplicas {
		rc := &gctx.allReplicas[i]
		if rc.member == nil {
			continue
		}
		if !promotionReportCurrent(rc.rvr, gctx.datameshRevision) {
			return dmte.GuardResult{Blocked: true, Message: fmt.Sprintf("waiting for a current configured report from %s", rc.name)}
		}
		if rc.member.Type.IsVoter() && !isUpToDateDiskful(rc) {
			return dmte.GuardResult{Blocked: true, Message: fmt.Sprintf("waiting for the existing data copy %s to be UpToDate", rc.name)}
		}
	}
	res := confirmPromotionConnected(gctx, gctx.datameshRevision)
	if res.MustConfirm != res.Confirmed {
		return dmte.GuardResult{Blocked: true, Message: "waiting for all existing peer connections before tie-breaker promotion"}
	}
	return dmte.GuardResult{}
}

func guardTieBreakerPromotion(gctx *globalContext, rctx *ReplicaContext) dmte.GuardResult {
	if result := guardPromotionSource(gctx, rctx); result.Blocked {
		return result
	}
	if rctx.rvr.Spec.Type != v1alpha1.ReplicaTypeDiskful ||
		rctx.rvr.Spec.NodeName != rctx.member.NodeName ||
		rctx.rvr.Spec.LVMVolumeGroupName == "" ||
		!obju.StatusCondition(rctx.rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyType).
			IsTrue().ObservedGenerationCurrent().Eval() {
		return dmte.GuardResult{Blocked: true, Message: "waiting for the tie-breaker's backing volume to be ready"}
	}
	for i := range gctx.allReplicas {
		rc := &gctx.allReplicas[i]
		if rc.id == rctx.id || (rc.member != nil && rc.member.Type.IsVoter()) {
			continue
		}
		if (rc.rvr != nil && rc.rvr.Spec.Type == v1alpha1.ReplicaTypeDiskful) ||
			(rc.membershipRequest != nil && rc.membershipRequest.Request.Type == v1alpha1.ReplicaTypeDiskful) {
			return dmte.GuardResult{Blocked: true, Message: fmt.Sprintf("waiting for conflicting diskful replica %s to be resolved", rc.name)}
		}
	}
	return dmte.GuardResult{}
}

func promotionReportCurrent(rvr *v1alpha1.ReplicatedVolumeReplica, revision int64) bool {
	return rvr != nil && rvr.DeletionTimestamp == nil && rvr.Status.DatameshRevision >= revision &&
		obju.StatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType).
			IsTrue().ObservedGenerationCurrent().Eval()
}

// Each side confirms its own current settings and connections. Revision alone
// proves bitmap configuration, but cannot prove the peer is already connected.
func confirmPromotionConnected(gctx *globalContext, revision int64) dmte.ConfirmResult {
	res := confirmAllMembers(gctx, revision)
	all, fm := allMemberIDs(gctx), fullMeshMemberIDs(gctx)
	for id := range res.MustConfirm.All() {
		rc := gctx.replicas[id]
		if !promotionReportCurrent(rc.rvr, revision) {
			res.Confirmed.Remove(id)
			continue
		}
		for peerID := range expectedPeerIDs(rc.member.Type, id, all, fm).All() {
			if !peerConnected(rc.rvr, peerID) {
				res.Confirmed.Remove(id)
				break
			}
		}
	}
	return res
}

func confirmPromotionSynchronized(gctx *globalContext, revision int64) dmte.ConfirmResult {
	res := confirmPromotionConnected(gctx, revision)
	for id := range res.MustConfirm.All() {
		rc := gctx.replicas[id]
		if rc.member.Type.IsVoter() && !isUpToDateDiskful(rc) {
			res.Confirmed.Remove(id)
		}
	}
	return res
}

func freshUpToDateVoters(gctx *globalContext) byte {
	var count byte
	for i := range gctx.allReplicas {
		rc := &gctx.allReplicas[i]
		if isUpToDateDiskful(rc) && promotionReportCurrent(rc.rvr, gctx.datameshRevision) {
			count++
		}
	}
	return count
}

// Emergency removal may reduce the set being synchronized while this plan is
// active. Finish with the surviving copies and release the voting slot for
// self-heal, but do not consume the configured failure headroom to raise qmr.
func raisePromotionQMR(gctx *globalContext) bool {
	next := gctx.datamesh.quorumMinimumRedundancy + 1
	if freshUpToDateVoters(gctx) < next+gctx.configuration.FailuresToTolerate {
		return false
	}
	return raiseQMR(gctx)
}
