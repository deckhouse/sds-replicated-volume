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

package syncbudget

import (
	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// ComputeActualOccupancy reports how many resync ends each node currently carries.
//
// It reads replicas only — never the volume — because the volume's datamesh lives in its status
// and would cost a second selection. The map is derived fresh on every call.
//
// Idle nodes stay out of the map entirely, so its keys are exactly the nodes carrying resync load.
func ComputeActualOccupancy(rvrs []*v1alpha1.ReplicatedVolumeReplica) map[string]int {
	occupancy := make(map[string]int)

	for _, rvr := range rvrs {
		if !OccupiesEnds(rvr) {
			continue
		}

		occupancy[rvr.Spec.NodeName] += occupiedEnds(rvr)
	}

	return occupancy
}

// ComputeActualOccupancyByRV reports the same ends as ComputeActualOccupancy, split by the volume
// carrying them: summing its values over every volume gives back the whole map.
//
// A claim's OwnEnds must come from here rather than from a separate reading of that volume's
// replicas. Admission charges a claim only the increment over its own ends, and that subtraction is
// sound only while those ends are already in the occupancy map it is judged against. Two readings
// can disagree — a transfer that started between them appears in the claim's own ends and not in
// the map — and the claim would then be charged for neither, admitting one end past the limit.
//
// Replicas whose volume name is empty are left out: they belong to no volume, so no claim can
// subtract them, though they still occupy their node in the whole map.
func ComputeActualOccupancyByRV(rvrs []*v1alpha1.ReplicatedVolumeReplica) map[string]map[string]int {
	byRV := make(map[string]map[string]int)

	for _, rvr := range rvrs {
		if !OccupiesEnds(rvr) || rvr.Spec.ReplicatedVolumeName == "" {
			continue
		}

		occupancy, seen := byRV[rvr.Spec.ReplicatedVolumeName]
		if !seen {
			occupancy = make(map[string]int)
			byRV[rvr.Spec.ReplicatedVolumeName] = occupancy
		}
		occupancy[rvr.Spec.NodeName] += occupiedEnds(rvr)
	}

	return byRV
}

// OccupiesEnds reports whether rvr contributes to the occupancy map: it sits on a node and carries
// at least one resync end there.
//
// ComputeActualOccupancy counts exactly the replicas this accepts and skips exactly the ones it
// rejects, which is what lets a caller select replicas by it — an index, say — and still get the
// same map from the narrowed slice as from every replica in the cluster. The two must therefore
// stay one function: a predicate that drifts from the loop would silently understate occupancy.
func OccupiesEnds(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
	// Not placed yet, so there is no node to charge.
	return rvr.Spec.NodeName != "" && occupiedEnds(rvr) > 0
}

// ReportsResync reports whether the agent sees rvr in a transfer right now.
//
// It is how a caller tells a transfer that has really started from one that is merely owed: the
// occupancy map takes the larger of the two signals and so cannot tell them apart, while anything
// standing in for a transfer until it is observed has to know when to stop.
func ReportsResync(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
	return reportedResyncEnds(rvr) > 0
}

// occupiedEnds is how many resync ends rvr carries on its node.
//
// The two signals are combined by taking the largest, not the sum: they describe the same transfer
// from different angles, so summing would charge one resync twice and halve the effective limit,
// while the largest keeps the error in the safe direction.
func occupiedEnds(rvr *v1alpha1.ReplicatedVolumeReplica) int {
	return max(reportedResyncEnds(rvr), owedDataEnds(rvr))
}

// reportedResyncEnds counts the connections of rvr that DRBD reports as syncing.
//
// This is the precise signal, and the only one that can exceed one end: a node feeding several
// targets at once reads its disk once per target. It comes from the agent, though, so it stops
// arriving when the agent does — which is what the second contributor is for.
func reportedResyncEnds(rvr *v1alpha1.ReplicatedVolumeReplica) int {
	ends := 0
	for i := range rvr.Status.Peers {
		if rvr.Status.Peers[i].ReplicationState.IsSyncingState() {
			ends++
		}
	}
	return ends
}

// owedDataEnds charges one end to a replica that is supposed to hold data and does not have it,
// whether or not any transfer has been reported yet.
//
// Two situations look identical from here and both must be charged: the agent went quiet while the
// kernel keeps copying, and the transfer has not started yet but will.
// Without the second one a formation that is placed but not yet syncing would be invisible.
//
// It is an assumption about a transfer, so it stops where the transfer becomes impossible: a
// replica on its way out will never take the data, and one whose every connection is down has no
// path for it to arrive by. Both keep a stuck replica from reserving its node's bandwidth forever —
// and neither touches reportedResyncEnds, so a replica that really is pulling stays charged.
//
// The range is the load criterion {Diskful, ShadowDiskful}: a ShadowDiskful
// is filled over the network exactly like a Diskful and merely does not vote.
//
// The ReplicaType → DatameshMemberType conversion is total (the member constants are defined from
// the replica ones) and is the module's established spelling for asking a member-type question of
// a replica.
func owedDataEnds(rvr *v1alpha1.ReplicatedVolumeReplica) int {
	memberType := v1alpha1.DatameshMemberType(rvr.Spec.Type)
	if !memberType.HasBackingVolume() {
		return 0
	}

	if !rvr.DeletionTimestamp.IsZero() {
		return 0
	}

	if hasOnlyDeadPeerConnections(rvr) {
		return 0
	}

	if !hasUpToDateBackingVolume(memberType, rvr) {
		return 1
	}

	return 0
}

// hasOnlyDeadPeerConnections reports whether rvr has peers and every one of them sits in a
// connection state through which no data can arrive.
//
// It asks the question through a deny list of states DRBD only reports with the connection down,
// never an allow list of healthy ones, because the two windows owedDataEnds exists for both look
// like silence: no peers at all means the agent has not reported yet, and an unset or Unknown
// connection state means it did not report that field. Reading either as dead would drop the charge
// exactly when it is needed.
func hasOnlyDeadPeerConnections(rvr *v1alpha1.ReplicatedVolumeReplica) bool {
	if len(rvr.Status.Peers) == 0 {
		return false
	}

	for i := range rvr.Status.Peers {
		if !isDeadConnectionState(rvr.Status.Peers[i].ConnectionState) {
			return false
		}
	}

	return true
}

// isDeadConnectionState reports whether state is one DRBD only reaches with the connection down.
func isDeadConnectionState(state v1alpha1.ConnectionState) bool {
	switch state {
	case v1alpha1.ConnectionStateStandAlone,
		v1alpha1.ConnectionStateUnconnected,
		v1alpha1.ConnectionStateDisconnecting,
		v1alpha1.ConnectionStateTearDown,
		v1alpha1.ConnectionStateTimeout,
		v1alpha1.ConnectionStateBrokenPipe,
		v1alpha1.ConnectionStateNetworkFailure,
		v1alpha1.ConnectionStateProtocolError:
		return true
	default:
		return false
	}
}

// hasUpToDateBackingVolume reports whether the pair (memberType, rvr) holds the volume's data on a
// real disk right now: the type is one DRBD configures a backing volume for — {Diskful,
// ShadowDiskful} — and the replica reports that volume UpToDate.
//
// This is the load criterion, not the redundancy one: a ShadowDiskful serves local I/O from a real
// disk, so it must be filled and it can send; it simply does not vote. Read in the other direction,
// false while the type is still in range means the replica is owed data and will pull it over the
// network.
//
// It takes the type rather than a *DatameshMember so that a caller holding only a replica can ask
// too. A nil rvr answers false, the safe direction on both sides.
func hasUpToDateBackingVolume(
	memberType v1alpha1.DatameshMemberType,
	rvr *v1alpha1.ReplicatedVolumeReplica,
) bool {
	if !memberType.HasBackingVolume() {
		return false
	}
	return rvr != nil && rvr.Status.BackingVolume != nil &&
		rvr.Status.BackingVolume.State == v1alpha1.DiskStateUpToDate
}
