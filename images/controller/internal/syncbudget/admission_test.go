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

package syncbudget_test

import (
	"maps"
	"testing"

	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/syncbudget"
)

// SubjectOccupancy must include the holds of the claims ahead of the subject and exclude the
// subject's own increment; both obvious alternatives get one of those two wrong.
func TestRunPassForSubjectOccupancy(t *testing.T) {
	// Two claims ahead of "rv-c" in the order, both blocked, both holding node-held; "rv-c" itself
	// already occupies one real end on node-own and needs two more there.
	queue := []syncbudget.Claim{
		{
			Class:      syncbudget.ClassIOBlocked,
			Name:       "rv-a",
			NeededEnds: map[string]int{"node-held": 2},
		},
		{
			Class:      syncbudget.ClassDeficit,
			Name:       "rv-c",
			NeededEnds: map[string]int{"node-own": 3},
			OwnEnds:    map[string]int{"node-own": 1},
		},
	}
	occupancy := map[string]int{"node-held": 2, "node-own": 1}

	pass := syncbudget.RunPassForSubject(queue, occupancy, syncbudget.Limit(2), "rv-c")

	// node-held: 2 real ends plus the 2 the blocked claim ahead holds.
	// node-own: the 1 real end the subject holds, and not the 2 it is charged for.
	want := map[string]int{"node-held": 4, "node-own": 1}
	if got := pass.SubjectOccupancy; !maps.Equal(got, want) {
		t.Errorf("SubjectOccupancy = %v, want %v", got, want)
	}

	// The input map is a read-only input, and the snapshot is not an alias of it.
	if got, want := occupancy, map[string]int{"node-held": 2, "node-own": 1}; !maps.Equal(got, want) {
		t.Errorf("input occupancy mutated: %v, want %v", got, want)
	}
}

// A claim asks nothing of a node its own ends already cover, so the node being full of them is no
// reason to hold it back — otherwise a volume feeding every copy it is short of reports as waiting
// for the slots it is itself using, and the waiter count never falls to zero under load.
func TestRunPassClaimNotBlockedByItsOwnEnds(t *testing.T) {
	// One data holder on node-a feeding the two copies the volume is short of: it needs two ends
	// there and already occupies both, which at a limit of two fills the node.
	queue := []syncbudget.Claim{{
		Class:      syncbudget.ClassDeficit,
		Name:       "rv-a",
		NeededEnds: map[string]int{"node-a": 2},
		OwnEnds:    map[string]int{"node-a": 2},
	}}
	occupancy := map[string]int{"node-a": 2}

	pass := syncbudget.RunPassForSubject(queue, occupancy, syncbudget.Limit(2), "")

	if pass.IsWaiting("rv-a") {
		t.Errorf("claim held back by its own ends: Waiting = %v", pass.Waiting)
	}
	if len(pass.Waiters) != 0 {
		t.Errorf("Waiters = %v, want none", pass.Waiters)
	}
	if len(pass.HeldEnds) != 0 {
		t.Errorf("HeldEnds = %v, want none", pass.HeldEnds)
	}
}

// The claim's own ends excuse only themselves: a node another volume also loads is still full.
func TestRunPassClaimBlockedWhenAnotherVolumeFillsTheNode(t *testing.T) {
	// Three ends wanted on node-a and one of them already occupied, so an increment remains and
	// the node is measured for real.
	queue := []syncbudget.Claim{{
		Class:      syncbudget.ClassDeficit,
		Name:       "rv-a",
		NeededEnds: map[string]int{"node-a": 3},
		OwnEnds:    map[string]int{"node-a": 1},
	}}
	// One of the two ends on node-a is the claim's own, the other belongs to a volume outside the
	// queue.
	occupancy := map[string]int{"node-a": 2}

	pass := syncbudget.RunPassForSubject(queue, occupancy, syncbudget.Limit(2), "")

	if !pass.IsWaiting("rv-a") {
		t.Error("claim admitted onto a node another volume already fills")
	}
	if got, want := pass.HeldEnds, map[string]int{"node-a": 2}; !maps.Equal(got, want) {
		t.Errorf("HeldEnds = %v, want %v", got, want)
	}
}

// A subject that is not in the queue gets the occupancy every claim's turn starts from, and an
// unnamed subject gets nothing at all — which is how the caller spells "do not filter".
func TestRunPassSubjectOccupancyWithoutSubjectInQueue(t *testing.T) {
	queue := []syncbudget.Claim{
		{
			Class:      syncbudget.ClassDeficit,
			Name:       "rv-a",
			NeededEnds: map[string]int{"node-a": 1},
		},
	}
	occupancy := map[string]int{"node-a": 1}

	absent := syncbudget.RunPassForSubject(queue, occupancy, syncbudget.Limit(2), "rv-absent")
	if got, want := absent.SubjectOccupancy, occupancy; !maps.Equal(got, want) {
		t.Errorf("SubjectOccupancy for a subject not in the queue = %v, want %v", got, want)
	}

	unnamed := syncbudget.RunPassForSubject(queue, occupancy, syncbudget.Limit(2), "")
	if unnamed.SubjectOccupancy != nil {
		t.Errorf("SubjectOccupancy without a subject = %v, want nil", unnamed.SubjectOccupancy)
	}
}

func TestLimitHasRoom(t *testing.T) {
	tests := []struct {
		name     string
		limit    syncbudget.Limit
		occupied int
		want     bool
	}{
		{name: "idle node under a limit of two", limit: 2, occupied: 0, want: true},
		{name: "one end used of two", limit: 2, occupied: 1, want: true},
		{name: "limit reached", limit: 2, occupied: 2, want: false},
		{
			// The accepted race between workers can leave a node at K+1. Such a node is
			// still full, not somehow eligible again.
			name: "over the limit after the accepted race", limit: 2, occupied: 3, want: false,
		},
		{
			// Zero means no limit at all — the documented way to switch the limiter off.
			name: "zero is unlimited", limit: 0, occupied: 500, want: true,
		},
		{
			// A negative limit can only come from a misparsed setting: losing the limiter
			// beats wedging every resync in the cluster.
			name: "negative is unlimited too", limit: -1, occupied: 500, want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.limit.HasRoom(tt.occupied); got != tt.want {
				t.Errorf("Limit(%d).HasRoom(%d) = %v, want %v",
					tt.limit, tt.occupied, got, tt.want)
			}
		})
	}
}

func TestLimitHasRoomOnAll(t *testing.T) {
	occupancy := map[string]int{"node-free": 0, "node-full": 2}

	tests := []struct {
		name  string
		limit syncbudget.Limit
		nodes []string
		want  bool
	}{
		{
			// Nothing to read from is nothing to be blocked by: the caller asks about the nodes a
			// transfer would load, and a transfer that loads none may always start.
			name: "no nodes at all", limit: 2, nodes: nil, want: true,
		},
		{name: "one node with room", limit: 2, nodes: []string{"node-free"}, want: true},
		{name: "one node at its limit", limit: 2, nodes: []string{"node-full"}, want: false},
		{
			// Every node must have room, not any of them: the data is read from all of them.
			name: "one full node among free ones", limit: 2,
			nodes: []string{"node-free", "node-full"}, want: false,
		},
		{
			// A node absent from the map carries no load — the map holds exactly the loaded ones.
			name: "node absent from the occupancy map", limit: 2,
			nodes: []string{"node-unknown"}, want: true,
		},
		{
			// Zero is the documented way to switch the limiter off, so no node is ever full.
			name: "zero limit admits a full node", limit: 0,
			nodes: []string{"node-free", "node-full"}, want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.limit.HasRoomOnAll(tt.nodes, occupancy); got != tt.want {
				t.Errorf("Limit(%d).HasRoomOnAll(%v) = %v, want %v",
					tt.limit, tt.nodes, got, tt.want)
			}
		})
	}
}
