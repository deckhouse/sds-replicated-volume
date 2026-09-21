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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/syncbudget"
)

var _ = Describe("resyncReservations", func() {
	// One end per node is what the whole ledger is for: the second reservation on a node is the
	// one that must be refused.
	const oneEndPerNode syncbudget.Limit = 1

	const ttl = time.Minute

	var (
		clock  time.Time
		ledger *resyncReservations
	)

	BeforeEach(func() {
		clock = time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
		ledger = newResyncReservations(ttl, func() time.Time { return clock })
	})

	It("refuses a second end on a node it has already handed out", func() {
		Expect(ledger.reserve("rv-a-1", "node-c", nil, oneEndPerNode, nil)).To(BeTrue())
		Expect(ledger.reserve("rv-b-1", "node-c", nil, oneEndPerNode, nil)).To(BeFalse(),
			"node-c is spoken for by a placement that has not come back through the watch yet")
		Expect(ledger.reserve("rv-b-1", "node-d", nil, oneEndPerNode, nil)).To(BeTrue(),
			"another node is still free")
	})

	It("counts the ends the cluster already reports, not only its own", func() {
		observed := map[string]int{"node-c": 1}
		Expect(ledger.reserve("rv-a-1", "node-c", nil, oneEndPerNode, observed)).To(BeFalse(),
			"the node is full before the ledger adds anything")
		Expect(ledger.mergedInto(observed)).To(Equal(map[string]int{"node-c": 1}),
			"a refused reservation leaves nothing behind")
	})

	It("gives the end back when the placement it was taken for did not happen", func() {
		Expect(ledger.reserve("rv-a-1", "node-c", nil, oneEndPerNode, nil)).To(BeTrue())
		ledger.release("rv-a-1")
		Expect(ledger.reserve("rv-b-1", "node-c", nil, oneEndPerNode, nil)).To(BeTrue())
	})

	It("drops the reservation once the replica occupies the end for real", func() {
		// Holding both would charge node-c twice: the listing is what ComputeActualOccupancy
		// counts, so the end is in the occupancy map from here on.
		Expect(ledger.reserve("rv-a-1", "node-c", nil, oneEndPerNode, nil)).To(BeTrue())

		ledger.observe([]*v1alpha1.ReplicatedVolumeReplica{{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-a-1"},
		}})

		Expect(ledger.mergedInto(nil)).To(BeEmpty())
	})

	It("holds the sending node too, so nothing else receives on it", func() {
		// The burst the receiving-end ledger alone could not stop. A transfer into node-c reading
		// off node-a loads both of them, but until the agent reports it nothing occupies node-a —
		// so the next volume in the wave would happily receive there.
		Expect(ledger.reserve("rv-a-1", "node-c", []string{"node-a"}, oneEndPerNode, nil)).To(BeTrue())

		Expect(ledger.mergedInto(nil)).To(Equal(map[string]int{"node-a": 1, "node-c": 1}))
		Expect(ledger.reserve("rv-b-1", "node-a", []string{"node-b"}, oneEndPerNode, nil)).To(BeFalse(),
			"node-a is already reading for the transfer into node-c")
		Expect(ledger.reserve("rv-b-1", "node-d", []string{"node-a"}, oneEndPerNode, nil)).To(BeFalse(),
			"and it cannot feed a second target either")
		Expect(ledger.reserve("rv-b-1", "node-d", []string{"node-b"}, oneEndPerNode, nil)).To(BeTrue(),
			"a transfer that touches neither node is free to start")
	})

	It("keeps the sending node after the placement is visible and the transfer is not", func() {
		// forgetPlaced sees the replica in the occupancy listing, so its receiving end is real
		// occupancy now. Its sending end still is not: nothing occupies the sender until the agent
		// reports the transfer, so the ledger has to go on standing in for it.
		Expect(ledger.reserve("rv-a-1", "node-c", []string{"node-a"}, oneEndPerNode, nil)).To(BeTrue())

		ledger.observe([]*v1alpha1.ReplicatedVolumeReplica{{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-a-1"},
		}})

		Expect(ledger.mergedInto(nil)).To(Equal(map[string]int{"node-a": 1}),
			"the receiving end is given up, the sending end is not")
	})

	It("drops the whole reservation once the replica has stopped owing data", func() {
		// A replica that finishes filling stops occupying an end and leaves the occupancy listing
		// for good, so the report the sending end waits for never arrives. Releasing on the listing
		// alone would hold the sender's node against nothing until the TTL ran out — a node's worth
		// of budget idle for a minute in the middle of a wave.
		Expect(ledger.reserve("rv-a-1", "node-c", []string{"node-a"}, oneEndPerNode, nil)).To(BeTrue())

		ledger.observe([]*v1alpha1.ReplicatedVolumeReplica{{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-a-1"},
		}})
		Expect(ledger.mergedInto(nil)).To(Equal(map[string]int{"node-a": 1}))

		// Filled: gone from the listing, and nothing of its is loading a node any more.
		ledger.observe(nil)

		Expect(ledger.mergedInto(nil)).To(BeEmpty())
	})

	It("keeps a reservation whose placement has not been observed yet", func() {
		// Absent from the listing because the patch has not come back through the watch — not
		// because the transfer is over. Dropping it here would reopen the window the ledger is for.
		Expect(ledger.reserve("rv-a-1", "node-c", []string{"node-a"}, oneEndPerNode, nil)).To(BeTrue())

		ledger.observe(nil)

		Expect(ledger.mergedInto(nil)).To(Equal(map[string]int{"node-a": 1, "node-c": 1}))
	})

	It("drops the whole reservation once the agent reports the transfer", func() {
		// Both ends are in ComputeActualOccupancy from here on — the source through
		// reportedResyncEnds, the target through it as well — so holding either would double-charge.
		Expect(ledger.reserve("rv-a-1", "node-c", []string{"node-a"}, oneEndPerNode, nil)).To(BeTrue())

		ledger.observe([]*v1alpha1.ReplicatedVolumeReplica{{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-a-1"},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				Peers: []v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{
					{ReplicationState: v1alpha1.ReplicationStateSyncTarget},
				},
			},
		}})

		Expect(ledger.mergedInto(nil)).To(BeEmpty())
	})

	It("replaces a replica's own reservation rather than stacking a second one", func() {
		// A requeue after a failed patch runs the same replica through the seam again.
		Expect(ledger.reserve("rv-a-1", "node-c", nil, oneEndPerNode, nil)).To(BeTrue())
		Expect(ledger.reserve("rv-a-1", "node-c", nil, oneEndPerNode, nil)).To(BeTrue(),
			"the replica is not blocked by the end it holds itself")
		Expect(ledger.mergedInto(nil)).To(Equal(map[string]int{"node-c": 1}))
	})

	It("stops counting an end nothing ever confirmed", func() {
		Expect(ledger.reserve("rv-a-1", "node-c", nil, oneEndPerNode, nil)).To(BeTrue())

		clock = clock.Add(ttl + time.Second)

		Expect(ledger.mergedInto(nil)).To(BeEmpty())
		Expect(ledger.reserve("rv-b-1", "node-c", nil, oneEndPerNode, nil)).To(BeTrue())
	})

	It("leaves the occupancy map it was given untouched", func() {
		observed := map[string]int{"node-a": 1}
		Expect(ledger.reserve("rv-a-1", "node-c", nil, oneEndPerNode, observed)).To(BeTrue())

		Expect(ledger.mergedInto(observed)).To(Equal(map[string]int{"node-a": 1, "node-c": 1}))
		Expect(observed).To(Equal(map[string]int{"node-a": 1}), "the input is read-only")
	})
})
