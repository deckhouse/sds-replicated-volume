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

import "maps"

// Limit is how many resync ends one node may carry at a time. Zero or less means no limit:
// zero switches the limiter off, and a negative value can only come from a misparsed setting,
// where losing the limiter beats wedging every resync.
type Limit int

// HasRoom reports whether a node can take one more resync end on top of the occupied ends it
// already carries.
func (l Limit) HasRoom(occupied int) bool {
	return l <= 0 || occupied < int(l)
}

// HasRoomOnAll reports whether every one of nodes can take one more resync end, measured against
// occupancy. An empty node list is trivially satisfied.
//
// One more, not one per node in the list: the caller asks whether a single transfer that reads from
// all of them may start.
func (l Limit) HasRoomOnAll(nodes []string, occupancy map[string]int) bool {
	for _, node := range nodes {
		if !l.HasRoom(occupancy[node]) {
			return false
		}
	}
	return true
}

// hasRoomForClaim reports whether every node the claim still wants an end from has room for one
// more, measured against occupancy.
//
// A node the claim's own ends already cover is not tested at all: by Claim.remainingEnds the claim
// asks it for nothing, and a volume already feeding every copy it is short of must not read as
// waiting for the very slots it occupies. The charge in RunPassForSubject skips the same nodes.
//
// Where an end does remain to be taken it asks for room for one more, not for the whole increment:
// a volume short of k copies needs k ends on its sender node and k may exceed the limit outright,
// so demanding room for all of them would leave it waiting forever. The claim still charges the
// whole increment.
func (l Limit) hasRoomForClaim(claim Claim, occupancy map[string]int) bool {
	nodes := make([]string, 0, len(claim.NeededEnds))
	for node := range claim.NeededEnds {
		if claim.remainingEnds(node) <= 0 {
			continue
		}
		nodes = append(nodes, node)
	}
	return l.HasRoomOnAll(nodes, occupancy)
}

// Pass is what one walk over the ordered queue decided: which volumes it held back, how many ends
// their holds occupy on which nodes, and how many waiters each class has.
//
// Nothing about a Pass is stored: the holds and waiter counts exist only inside the walk that
// produced them.
type Pass struct {
	// Waiting is the set of volume names the pass held back. A claim that was in the queue and is
	// absent from it was admitted; a volume that made no claim was never in the queue at all.
	Waiting map[string]struct{}

	// HeldEnds is the ends held by waiting claims, keyed by node name — a node occupied on behalf
	// of a volume that is itself waiting, so that wide volumes do not starve behind narrow ones,
	// at the cost of the end possibly sitting idle. Unlike Claim.OwnEnds these ends are not really
	// occupied and do not outlive the pass.
	HeldEnds map[string]int

	// Waiters counts the volumes the pass held back, per class.
	Waiters map[Class]int

	// SubjectOccupancy is the working occupancy as it stood when the subject's claim came up: real
	// occupancy plus the charges of every claim ahead of it, and none of its own increment. It is
	// nil unless a subject was named.
	//
	// This is the map a receiving-node filter must measure candidates against: the input occupancy
	// misses the holds this pass creates, while the final working map includes the subject's own
	// charge and would reject the nodes it was just admitted to use.
	SubjectOccupancy map[string]int
}

// IsWaiting reports whether the pass held the named volume back.
func (p Pass) IsWaiting(rvName string) bool {
	_, waiting := p.Waiting[rvName]
	return waiting
}

// RunPassForSubject walks the ordered queue once, occupying nodes for every claim in turn, and
// reports what it decided for all of them plus the occupancy the named subject's turn saw.
//
// queue MUST already be ordered by CompareClaims, which is what ComputeActualQueue returns; the
// order is what rules out mutual blocking, since the head of the queue always moves. Every claim
// occupies its nodes whether or not it was admitted — a blocked one holds them — because occupying
// only for the admitted ones would let a volume that needs many ends starve behind a stream of
// volumes that each need few.
//
// The subject need not be in the queue; when it is not, SubjectOccupancy is the input occupancy
// unchanged. An empty subject names no volume and leaves it nil. occupancy is a read-only input.
func RunPassForSubject(queue []Claim, occupancy map[string]int, limit Limit, subject string) Pass {
	working := maps.Clone(occupancy)
	if working == nil {
		working = make(map[string]int)
	}

	pass := Pass{
		Waiting:  make(map[string]struct{}),
		HeldEnds: make(map[string]int),
		Waiters:  make(map[Class]int),
	}

	if subject != "" {
		pass.SubjectOccupancy = maps.Clone(working)
	}

	for _, claim := range queue {
		if claim.Name == subject {
			// Before the charge below: admission is measured without the subject's own increment.
			// Volumes are cluster-scoped, so at most one claim can match.
			pass.SubjectOccupancy = maps.Clone(working)
		}

		// Against the working map as it stands, so a claim is never blocked by its own charge.
		blocked := !limit.hasRoomForClaim(claim, working)

		for node := range claim.NeededEnds {
			// Ends already in the occupancy map are not charged a second time.
			charge := claim.remainingEnds(node)
			if charge <= 0 {
				continue
			}

			// Unclamped: real occupancy may already sit above the limit, and clamping would lower
			// it. A node past its limit is simply full, which is what a later claim reads.
			working[node] += charge
			if blocked {
				pass.HeldEnds[node] += charge
			}
		}

		if blocked {
			pass.Waiting[claim.Name] = struct{}{}
			pass.Waiters[claim.Class]++
		}
	}

	return pass
}
