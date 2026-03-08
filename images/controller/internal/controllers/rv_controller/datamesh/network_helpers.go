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
	"slices"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/dmte"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/idset"
)

// ──────────────────────────────────────────────────────────────────────────────
// Step constructor
//

// ngStep creates a network GlobalStep with standard DiagnosticConditions.
func ngStep(
	name string,
	apply func(*globalContext) bool,
	confirm func(*globalContext, int64) dmte.ConfirmResult,
) *dmte.GlobalStepBuilder[*globalContext] {
	return dmte.GlobalStep(name, apply, confirm).
		DiagnosticConditions(v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType)
}

// ──────────────────────────────────────────────────────────────────────────────
// Address lookup helper
//

// findAddressByNetwork returns the address entry for the given system network
// name, or nil if not found. Linear scan — max 10 entries.
func findAddressByNetwork(addrs []v1alpha1.DRBDResourceAddressStatus, network string) *v1alpha1.DRBDResourceAddressStatus {
	for i := range addrs {
		if addrs[i].SystemNetworkName == network {
			return &addrs[i]
		}
	}
	return nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Apply callbacks (global scope)
//

// repairAddresses syncs member.Addresses with rvr.Status.Addresses for
// datamesh system networks. For each member: builds the target address list
// from RVR filtered by datamesh.systemNetworkNames, and replaces
// member.Addresses if different. Handles missing, stale, and wrong IP/port.
// For RepairNetworkAddresses.
func repairAddresses(gctx *globalContext) bool {
	targetNets := gctx.datamesh.systemNetworkNames
	changed := false
	for i := range gctx.allReplicas {
		rctx := &gctx.allReplicas[i]
		if rctx.member == nil || rctx.rvr == nil {
			continue
		}
		if repairMemberAddresses(rctx.member, rctx.rvr.Status.Addresses, targetNets) {
			changed = true
		}
	}
	return changed
}

// ──────────────────────────────────────────────────────────────────────────────
// Per-member mutation helpers
//

// repairMemberAddresses syncs member.Addresses with rvrAddrs filtered by
// targetNets. Builds the target slice from RVR addresses that match targetNets,
// then replaces member.Addresses if it differs.
// Returns true if member.Addresses was changed.
func repairMemberAddresses(member *v1alpha1.DatameshMember, rvrAddrs []v1alpha1.DRBDResourceAddressStatus, targetNets []string) bool {
	// Build target: for each datamesh network, take the address from RVR.
	target := make([]v1alpha1.DRBDResourceAddressStatus, 0, len(targetNets))
	for _, net := range targetNets {
		if ra := findAddressByNetwork(rvrAddrs, net); ra != nil {
			target = append(target, *ra)
		}
	}

	// Compare with current.
	if addressesEqual(member.Addresses, target) {
		return false
	}

	member.Addresses = target
	return true
}

// addressesEqual returns true if two address slices are identical
// (same length, same entries in the same order).
func addressesEqual(a, b []v1alpha1.DRBDResourceAddressStatus) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ──────────────────────────────────────────────────────────────────────────────
// Dispatch detection helpers
//

// needsAddressRepair returns true if any member's addresses don't match the
// target state (RVR addresses filtered by datamesh system networks).
// Used by the network dispatcher to decide whether to trigger RepairNetworkAddresses.
func needsAddressRepair(gctx *globalContext) bool {
	targetNets := gctx.datamesh.systemNetworkNames

	// No datamesh system networks configured — nothing to repair against.
	// Should not happen in production (Formation sets systemNetworkNames),
	// but common in tests that don't set up network context.
	if len(targetNets) == 0 {
		return false
	}

	for i := range gctx.allReplicas {
		rctx := &gctx.allReplicas[i]
		if rctx.member == nil || rctx.rvr == nil {
			continue
		}

		// Check for missing or wrong: each datamesh network present in RVR
		// must also be present in member with the same address.
		expected := 0
		for _, net := range targetNets {
			ra := findAddressByNetwork(rctx.rvr.Status.Addresses, net)
			if ra == nil {
				continue // RVR hasn't reported this network yet
			}
			expected++
			ma := findAddressByNetwork(rctx.member.Addresses, net)
			if ma == nil || ma.Address != ra.Address {
				return true
			}
		}

		// Check for stale: member has more addresses than expected.
		if len(rctx.member.Addresses) != expected {
			return true
		}
	}
	return false
}

// ──────────────────────────────────────────────────────────────────────────────
// Confirm callbacks
//

// confirmAllMembersConnected checks confirmation for RepairNetworkAddresses.
// MustConfirm = all datamesh members.
// A member is confirmed when DatameshRevision >= stepRevision AND all of its
// expected connections are verified (at least one side with a ready agent
// reports Connected).
func confirmAllMembersConnected(gctx *globalContext, stepRevision int64) dmte.ConfirmResult {
	mustConfirm := allMemberIDs(gctx)
	revisionConfirmed := confirmedReplicas(gctx, stepRevision).Intersect(mustConfirm)

	// Precompute member sets once for all iterations.
	allMembers := allMemberIDs(gctx)
	fmMembers := fullMeshMemberIDs(gctx)

	// Check connectivity only for revision-confirmed members.
	var confirmed idset.IDSet
	for id := range revisionConfirmed.All() {
		rctx := gctx.replicas[id]
		if rctx != nil && rctx.member != nil && allConnectionsOfMemberVerified(gctx, rctx, stepRevision, allMembers, fmMembers, peerConnected) {
			confirmed.Add(id)
		}
	}
	return dmte.ConfirmResult{MustConfirm: mustConfirm, Confirmed: confirmed}
}

// ──────────────────────────────────────────────────────────────────────────────
// Guards
//

// guardRemainingNetworksConnected blocks if the intersection of datamesh system
// networks (gctx.datamesh.systemNetworkNames) and configuration system networks
// (RSP.Spec.SystemNetworkNames) does not have full connectivity for all members.
// Used by ChangeSystemNetwork adjust/v1 to ensure remaining networks can carry
// traffic before removing networks.
//
// Logic: compute intersection of datamesh and configuration. If intersection is
// empty, block (dispatcher should choose migrate/v1). For each network in the
// intersection, check full connectivity across all members. If at least one has
// it — pass.
func guardRemainingNetworksConnected(gctx *globalContext) dmte.GuardResult {
	if gctx.rsp == nil {
		return dmte.GuardResult{
			Blocked: true,
			Message: "Cannot check remaining system networks: RSP unavailable",
		}
	}

	datameshNets := gctx.datamesh.systemNetworkNames
	configurationNets := gctx.rsp.GetSystemNetworkNames()

	// Compute intersection (networks that will remain after adjustment).
	intersection := make([]string, 0)
	for _, net := range configurationNets {
		if slices.Contains(datameshNets, net) {
			intersection = append(intersection, net)
		}
	}

	if len(intersection) == 0 {
		return dmte.GuardResult{
			Blocked: true,
			Message: "Cannot adjust systemnetworks: intersection of datamesh and configuration is empty",
		}
	}

	// Precompute member sets once for all network checks.
	allMembers := allMemberIDs(gctx)
	fmMembers := fullMeshMemberIDs(gctx)

	// Check full connectivity on at least one remaining network.
	for _, network := range intersection {
		if allMembersHaveFullConnectivity(gctx, 0, allMembers, fmMembers, peerConnectedOnNetwork(network)) {
			return dmte.GuardResult{}
		}
	}

	return dmte.GuardResult{
		Blocked: true,
		Message: fmt.Sprintf("Cannot adjust networks: no remaining network with full connectivity (checked %d networks)", len(intersection)),
	}
}

// guardReplicasMatchTargetNetworks blocks network transitions if any
// replica's rvr.Status.Addresses is not synchronized with the target system
// network list (rv.Status.Datamesh.SystemNetworkNames). This ensures stability:
// before changing networks, all replicas must have their actual addresses match
// the current target.
//
// Logic: for each replica with an RVR, check that the set of
// SystemNetworkName values in rvr.Status.Addresses exactly matches
// gctx.datamesh.systemNetworkNames (target). If any replica is out of sync,
// block.
func guardReplicasMatchTargetNetworks(gctx *globalContext) dmte.GuardResult {
	targetNets := gctx.datamesh.systemNetworkNames
	if len(targetNets) == 0 {
		// No target networks — allow (edge case, will be handled by dispatcher).
		return dmte.GuardResult{}
	}

	// Check each replica.
	for i := range gctx.allReplicas {
		rctx := &gctx.allReplicas[i]
		if rctx.member == nil || rctx.rvr == nil || len(rctx.rvr.Status.Addresses) == 0 {
			continue
		}

		// Missing: in target but not in actual.
		var missing []string
		for _, net := range targetNets {
			if findAddressByNetwork(rctx.rvr.Status.Addresses, net) == nil {
				missing = append(missing, net)
			}
		}

		// Extra: in actual but not in target.
		var extra []string
		for _, addr := range rctx.rvr.Status.Addresses {
			if !slices.Contains(targetNets, addr.SystemNetworkName) {
				extra = append(extra, addr.SystemNetworkName)
			}
		}

		if len(missing) == 0 && len(extra) == 0 {
			continue
		}

		var msg string
		switch {
		case len(missing) > 0 && len(extra) > 0:
			msg = fmt.Sprintf("Replica %d: missing system networks %v, extra system networks %v", rctx.id, missing, extra)
		case len(missing) > 0:
			msg = fmt.Sprintf("Replica %d: missing system networks %v", rctx.id, missing)
		default:
			msg = fmt.Sprintf("Replica %d: extra system networks %v", rctx.id, extra)
		}
		return dmte.GuardResult{Blocked: true, Message: msg}
	}

	return dmte.GuardResult{}
}

// ──────────────────────────────────────────────────────────────────────────────
// Connection verification helpers
//
// Organized by abstraction level, from high-level (all members) to low-level
// (single peer lookup). The check function (peerCheck) is passed from the top
// level down, selecting the verification mode:
//   - peerConnected: any connection (peer reports Connected)
//   - peerConnectedOnNetwork("net"): connection on a specific system network
//
// A connection between two members is verified if at least one side with a
// ready agent (DatameshRevision >= minRevision) reports the peer as connected.

// peerCheck checks whether a peer connection meets the desired criteria.
type peerCheck func(rvr *v1alpha1.ReplicatedVolumeReplica, peerID uint8) bool

// peerConnected checks if rvr's peer list shows the given peer as Connected.
func peerConnected(rvr *v1alpha1.ReplicatedVolumeReplica, peerID uint8) bool {
	for _, p := range rvr.Status.Peers {
		if p.ID() == peerID {
			return p.ConnectionState == v1alpha1.ConnectionStateConnected
		}
	}
	return false
}

// peerConnectedOnNetwork returns a peerCheck that verifies the given peer
// has the system network in ConnectionEstablishedOn.
func peerConnectedOnNetwork(network string) peerCheck {
	return func(rvr *v1alpha1.ReplicatedVolumeReplica, peerID uint8) bool {
		for _, p := range rvr.Status.Peers {
			if p.ID() == peerID {
				return slices.Contains(p.ConnectionEstablishedOn, network)
			}
		}
		return false
	}
}

// expectedPeerIDs returns the IDs of peers this member should be connected to.
// FM member (ConnectsToAllPeers): all other members.
// Star member (A, TB): all FM members only.
// allMembers and fmMembers are precomputed to avoid repeated iteration.
func expectedPeerIDs(memberType v1alpha1.DatameshMemberType, id uint8, allMembers, fmMembers idset.IDSet) idset.IDSet {
	if memberType.ConnectsToAllPeers() {
		// FM connects to all other members (all except self).
		return allMembers.Difference(idset.Of(id))
	}
	// Star connects only to FM members (FM except self).
	return fmMembers.Difference(idset.Of(id))
}

// allMembersHaveFullConnectivity returns true if every member has all expected
// connections verified by check. A connection is verified if at least one side
// has DatameshRevision >= minRevision and a ready agent reporting the peer.
// allMembers and fmMembers are precomputed to avoid repeated iteration.
func allMembersHaveFullConnectivity(gctx *globalContext, minRevision int64, allMembers, fmMembers idset.IDSet, check peerCheck) bool {
	for i := range gctx.allReplicas {
		rctx := &gctx.allReplicas[i]
		if rctx.member == nil {
			continue
		}
		if !allConnectionsOfMemberVerified(gctx, rctx, minRevision, allMembers, fmMembers, check) {
			return false
		}
	}
	return true
}

// allConnectionsOfMemberVerified returns true if every expected connection
// of rctx is verified by check. allMembers and fmMembers are precomputed to
// avoid repeated iteration.
func allConnectionsOfMemberVerified(gctx *globalContext, rctx *ReplicaContext, minRevision int64, allMembers, fmMembers idset.IDSet, check peerCheck) bool {
	expected := expectedPeerIDs(rctx.member.Type, rctx.id, allMembers, fmMembers)
	for peerID := range expected.All() {
		peerRctx := gctx.replicas[peerID]
		if !connectionVerified(rctx, peerRctx, minRevision, check) {
			return false
		}
	}
	return true
}

// connectionVerified returns true if the connection between a and b is
// confirmed by at least one side with a ready agent and
// DatameshRevision >= minRevision.
//
//	a ready AND a.revision >= minRevision AND check(a.rvr, b.id) → true
//	b ready AND b.revision >= minRevision AND check(b.rvr, a.id) → true
//	neither → false (cannot verify)
func connectionVerified(a, b *ReplicaContext, minRevision int64, check peerCheck) bool {
	if b == nil {
		return false
	}
	if a.rvr != nil && isAgentReady(a.rvr) && a.rvr.Status.DatameshRevision >= minRevision && check(a.rvr, b.id) {
		return true
	}
	if b.rvr != nil && isAgentReady(b.rvr) && b.rvr.Status.DatameshRevision >= minRevision && check(b.rvr, a.id) {
		return true
	}
	return false
}
