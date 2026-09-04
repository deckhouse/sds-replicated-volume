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
	"cmp"
	"slices"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// Claim is one volume asking for resync slots: its priority, its identity in the queue, and how
// many resync ends it needs on which nodes.
//
// It is a property of the volume, not of a replica: what is at risk is the volume's data, and one
// admission decision covers every replica of it that still has to be filled.
type Claim struct {
	// Class is the priority of the claim. It never equals ClassNone: a volume that needs no
	// resync slot makes no claim at all.
	Class Class

	// Name is the volume's metadata.name. ReplicatedVolumes are cluster-scoped, so (Class, Name)
	// is a total order — which is what makes every worker compute the same queue from the same
	// snapshot.
	Name string

	// NeededEnds is how many resync ends the volume needs on each node it needs them on, keyed by
	// node name. See ComputeActualNeededEnds for why it is a number per node and not a flag.
	NeededEnds map[string]int

	// OwnEnds is the volume's own contribution to the occupancy map, keyed by node name: ends it
	// already occupies for real, unlike the holds of Pass.HeldEnds.
	//
	// Admission reads and charges only the increment over it — see remainingEnds. A volume that is
	// already syncing appears both as real ends in the occupancy map and as a claim in the queue,
	// so without this subtraction its effective limit would be halved.
	OwnEnds map[string]int
}

// remainingEnds reports how many of the ends the claim needs on node it does not already occupy
// there.
//
// Zero or less means the node owes the claim nothing, so its occupancy is neither a reason to hold
// the claim back nor something to charge it for. One spelling for both, so that a volume feeding
// every copy it is short of is not held back by its own transfers.
func (c Claim) remainingEnds(node string) int {
	return c.NeededEnds[node] - c.OwnEnds[node]
}

// CompareClaims orders claims the way the admission pass walks them: by class first, by volume
// name second, ascending. The order is total and the same for every worker.
func CompareClaims(a, b Claim) int {
	if c := cmp.Compare(a.Class, b.Class); c != 0 {
		return c
	}
	return cmp.Compare(a.Name, b.Name)
}

// ComputeActualClaim reports the claim rv makes on the budget, given its own replicas.
//
// It returns false when rv makes no claim at all — still forming, needing no slot, or short of
// copies with nowhere to read them from. None of those makes the volume wait, so the caller does
// not need to tell them apart.
func ComputeActualClaim(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
) (Claim, bool) {
	// A volume being born is never held back: formation has a timeout whose expiry deletes every
	// replica, so queueing it would turn a full budget into a rebuild loop. Its load still counts
	// in the occupancy map — it occupies without asking.
	if isFormingDatamesh(rv) {
		return Claim{}, false
	}

	class := ComputeActualSyncClass(rv)
	if class == ClassNone {
		return Claim{}, false
	}

	neededEnds := ComputeActualNeededEnds(rv, rvrs)
	if len(neededEnds) == 0 {
		return Claim{}, false
	}

	return Claim{
		Class:      class,
		Name:       rv.Name,
		NeededEnds: neededEnds,
		// Occupancy is additive over replicas, so the volume's own share is the same sweep run
		// over its own replicas — no second rule to keep in step with the global one.
		OwnEnds: ComputeActualOccupancy(rvrs),
	}, true
}

// isFormingDatamesh reports whether rv's datamesh has never been formed or is still forming.
func isFormingDatamesh(rv *v1alpha1.ReplicatedVolume) bool {
	if rv.Status.DatameshRevision == 0 {
		return true
	}
	for i := range rv.Status.DatameshTransitions {
		if rv.Status.DatameshTransitions[i].Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation {
			return true
		}
	}
	return false
}

// ComputeActualQueue builds the claims of every volume that wants a resync slot, in the order the
// admission pass walks them.
//
// Volumes that make no claim are absent from the result, but their replicas still count in the
// occupancy map.
func ComputeActualQueue(
	rvs []*v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
) []Claim {
	rvrsByRV := make(map[string][]*v1alpha1.ReplicatedVolumeReplica, len(rvs))
	for _, rvr := range rvrs {
		if rvr.Spec.ReplicatedVolumeName == "" {
			continue
		}
		rvrsByRV[rvr.Spec.ReplicatedVolumeName] = append(rvrsByRV[rvr.Spec.ReplicatedVolumeName], rvr)
	}

	queue := make([]Claim, 0, len(rvs))
	for _, rv := range rvs {
		if claim, ok := ComputeActualClaim(rv, rvrsByRV[rv.Name]); ok {
			queue = append(queue, claim)
		}
	}

	slices.SortFunc(queue, CompareClaims)
	return queue
}

// ComputeActualNeededEnds reports how many resync ends rv needs on each node that could send it
// data, keyed by node name.
//
// Every sender is charged the full shortfall k = max(0, D - upToDateVoters), not a share of it:
// which sender feeds which target is DRBD's choice, so one node may end up feeding all k targets
// at once. Over-counting is the safe direction.
//
// An empty result means there is no budget to spend — the volume is not short of copies, or no
// node holds data to send.
func ComputeActualNeededEnds(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
) map[string]int {
	// The same helper the class ladder uses, so priority and charge cannot drift apart.
	short, ok := computeActualCopiesShort(rv)
	if !ok || short <= 0 {
		return nil
	}

	senderNodes := ComputeActualSenderNodes(rv, rvrs)
	if len(senderNodes) == 0 {
		return nil
	}

	neededEnds := make(map[string]int, len(senderNodes))
	for _, node := range senderNodes {
		neededEnds[node] = short
	}
	return neededEnds
}

// ComputeActualSenderNodes reports the nodes that could read data off their disk and send it to a
// replica of rv, sorted and deduplicated.
//
// Only sender nodes are checked by admission: the data sits on them and cannot be read anywhere
// else, while the receiving node is the scheduler's choice and the budget filters it there.
//
// A node qualifies through hasUpToDateBackingVolume — the load criterion, not the redundancy one:
// judging by redundancy would let a volume whose only data holder is a ShadowDiskful escape the
// budget altogether.
func ComputeActualSenderNodes(
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
) []string {
	rvrsByName := make(map[string]*v1alpha1.ReplicatedVolumeReplica, len(rvrs))
	for _, rvr := range rvrs {
		rvrsByName[rvr.Name] = rvr
	}

	members := rv.Status.Datamesh.Members
	nodes := make([]string, 0, len(members))
	for i := range members {
		// A member whose replica is missing reads as not up to date through the nil, which is the
		// safe direction: it is not offered as a sender.
		if !hasUpToDateBackingVolume(members[i].Type, rvrsByName[members[i].Name]) {
			continue
		}
		nodes = append(nodes, members[i].NodeName)
	}

	slices.Sort(nodes)
	return slices.Compact(nodes)
}
