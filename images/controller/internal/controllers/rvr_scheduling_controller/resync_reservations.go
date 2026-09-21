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

package rvrschedulingcontroller

import (
	"maps"
	"slices"
	"sync"
	"time"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/syncbudget"
)

// resyncReservationTTL is how long a reserved end survives when nothing ever confirms it.
//
// The ordinary release is observation: the transfer is reported, both its ends become real
// occupancy, and forgetPlaced drops the reservation. The TTL covers only what observation cannot —
// a replica removed before it was ever seen transferring — so it is deliberately generous.
// Releasing too early breaks the budget; releasing too late costs one idle end on one node.
const resyncReservationTTL = 60 * time.Second

// resyncReservations remembers the resync ends this process has handed out but has not yet seen
// come back.
//
// A transfer loads both of its ends: the node the data is read from and the node it is written to.
// Neither is visible when the decision to start it is taken. The receiving node is not even chosen
// until the scheduling pipeline runs, and its only record is the nodeName patch; the sending node is
// chosen by DRBD and shows up solely as peers[].replicationState=SyncSource, which the agent writes
// seconds later. Until both arrive, every other worker reads those nodes as free — so with
// MaxConcurrentReconciles volumes being decided at once, that many transfers can pile onto one node.
// This ledger is the record that was missing: it is written the moment a placement commits a
// transfer and read by every budget decision taken after it.
//
// Memory is enough to hold it. It only has to outlive the cache round-trip, one controller holds
// the lease at a time, and a replacement syncs its cache before starting a single worker — so a
// restart has no decision in flight left to remember.
type resyncReservations struct {
	ttl time.Duration

	// now is injected so that expiry can be tested without waiting for it.
	now func() time.Time

	mu        sync.Mutex
	byReplica map[string]resyncReservation
}

// resyncReservation is the ends held for one transfer, the one that fills one replica.
//
// The two halves are released on different signals, which is why they are kept apart. The receiving
// end stands in only until the placement is visible: from then on the replica itself occupies its
// node through owedDataEnds, and holding the reservation too would charge that node twice. The
// sending end has no such stand-in — nothing occupies the sender until the agent reports the
// transfer — so it is held until that report arrives.
type resyncReservation struct {
	// receivingNode is the node the replica was placed on, held while placed is false.
	receivingNode string

	// senderNodes are the nodes the data can be read from, held until the transfer is reported.
	senderNodes []string

	// placed is true once the placement has been observed in the occupancy listing.
	placed bool

	expiresAt time.Time
}

// heldEnds reports the ends the reservation still stands in for, keyed by node name.
func (r resyncReservation) heldEnds() map[string]int {
	ends := make(map[string]int, len(r.senderNodes)+1)
	if !r.placed {
		ends[r.receivingNode]++
	}
	for _, node := range r.senderNodes {
		ends[node]++
	}
	return ends
}

// isEmpty reports whether the reservation holds nothing any more and can be dropped.
func (r resyncReservation) isEmpty() bool {
	return r.placed && len(r.senderNodes) == 0
}

func newResyncReservations(ttl time.Duration, now func() time.Time) *resyncReservations {
	return &resyncReservations{
		ttl:       ttl,
		now:       now,
		byReplica: make(map[string]resyncReservation),
	}
}

// reserve takes every end the transfer filling the named replica will load — the node it is placed
// on and the nodes it may read from — and reports whether all of them had room for one more.
//
// All or nothing, under one lock: this is the last gate before the patch, and it is the only place
// where the two halves of a transfer are judged together. Admission cannot do it, since it runs
// before the receiving node is chosen and against a snapshot that misses every node a concurrent
// worker has committed since.
//
// observed MUST be occupancy without this ledger's own reservations: they are added here, so a
// caller that already merged them in would have them counted twice and would lock itself out.
//
// A replica that reserves again — a requeue after the patch failed, say — replaces its own
// reservation instead of stacking a second one onto it.
func (l *resyncReservations) reserve(
	replicaName, receivingNode string,
	senderNodes []string,
	limit syncbudget.Limit,
	observed map[string]int,
) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.dropExpiredLocked()

	delete(l.byReplica, replicaName)

	reservation := resyncReservation{
		receivingNode: receivingNode,
		senderNodes:   slices.Clone(senderNodes),
		expiresAt:     l.now().Add(l.ttl),
	}

	pending := l.pendingLocked()
	for node, wanted := range reservation.heldEnds() {
		// HasRoom asks for one more end, so the rest of what this reservation wants on the node
		// goes in alongside what is already there.
		if !limit.HasRoom(observed[node] + pending[node] + wanted - 1) {
			return false
		}
	}

	l.byReplica[replicaName] = reservation
	return true
}

// release drops the reservation held for a replica, for the paths that reserved the ends and then
// failed to place the replica.
func (l *resyncReservations) release(replicaName string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.byReplica, replicaName)
}

// observe reconciles the ledger against the occupancy listing the budget reads anyway, so that
// every reservation gives up the half the cluster now accounts for by itself.
//
// Three answers, and a reservation has to survive only the middle one. A replica listed as
// occupying an end has a node, so its receiving end is counted by syncbudget.ComputeActualOccupancy
// and the reservation stops standing in for it; its sending end is counted only once the agent
// reports the transfer, which is what syncbudget.ReportsResync asks, and until then the reservation
// is all that holds the sender's node. A replica absent from the listing owes nothing any more —
// filled, or gone — so no transfer of its is loading anything and the whole reservation goes.
//
// The absent case is why this is a sweep over the ledger rather than a walk over the listing: a
// replica that finishes filling leaves the listing for good, and a release that only ever looked at
// what the listing still holds would never hear of it. Its sender's node would then sit reserved
// against nothing until the TTL ran out, which is a node's worth of budget idle for a minute.
//
// The one reservation kept while absent is one whose placement has not been observed yet: the
// replica may simply not have reached the cache, and dropping it would reopen the very window the
// ledger exists for. The TTL is the backstop there, as it is for a placement that never lands.
func (l *resyncReservations) observe(rvrs []*v1alpha1.ReplicatedVolumeReplica) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.dropExpiredLocked()

	occupying := make(map[string]*v1alpha1.ReplicatedVolumeReplica, len(rvrs))
	for _, rvr := range rvrs {
		occupying[rvr.Name] = rvr
	}

	for replicaName, reservation := range l.byReplica {
		rvr, listed := occupying[replicaName]

		if !listed {
			if !reservation.placed {
				continue
			}
			delete(l.byReplica, replicaName)
			continue
		}

		if syncbudget.ReportsResync(rvr) {
			// Both ends are real occupancy now.
			delete(l.byReplica, replicaName)
			continue
		}

		reservation.placed = true
		if reservation.isEmpty() {
			delete(l.byReplica, replicaName)
			continue
		}
		l.byReplica[replicaName] = reservation
	}
}

// mergedInto returns occupancy with the pending reservations added, leaving the input untouched.
//
// This is what admission and the receiving-node filter must be measured against, so that a node a
// transfer has already been committed on reads as occupied rather than free.
func (l *resyncReservations) mergedInto(occupancy map[string]int) map[string]int {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.dropExpiredLocked()

	merged := maps.Clone(occupancy)
	if merged == nil {
		merged = make(map[string]int, len(l.byReplica))
	}
	for node, ends := range l.pendingLocked() {
		merged[node] += ends
	}
	return merged
}

// pendingLocked reports the reserved ends by node name. The caller MUST hold the mutex.
func (l *resyncReservations) pendingLocked() map[string]int {
	pending := make(map[string]int)
	for _, reservation := range l.byReplica {
		for node, ends := range reservation.heldEnds() {
			pending[node] += ends
		}
	}
	return pending
}

// dropExpiredLocked removes reservations past their TTL. The caller MUST hold the mutex.
func (l *resyncReservations) dropExpiredLocked() {
	now := l.now()
	for replicaName, reservation := range l.byReplica {
		if now.After(reservation.expiresAt) {
			delete(l.byReplica, replicaName)
		}
	}
}
