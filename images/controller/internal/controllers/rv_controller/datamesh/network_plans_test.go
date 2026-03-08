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
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// mkAddr creates a DRBDResourceAddressStatus for test setup.
func mkAddr(net, ip string, port uint) v1alpha1.DRBDResourceAddressStatus {
	return v1alpha1.DRBDResourceAddressStatus{
		SystemNetworkName: net,
		Address:           v1alpha1.DRBDAddress{IPv4: ip, Port: port},
	}
}

// mkPeerConnected creates a peer status with Connected state.
func mkPeerConnected(peerName string) v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus {
	return v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{
		Name:            peerName,
		ConnectionState: v1alpha1.ConnectionStateConnected,
	}
}

// mkPeerConnectedOnNetwork creates a peer status with Connected state and
// ConnectionEstablishedOn set to the given networks.
func mkPeerConnectedOnNetwork(peerName string, networks ...string) v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus {
	return v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{
		Name:                    peerName,
		ConnectionState:         v1alpha1.ConnectionStateConnected,
		ConnectionEstablishedOn: networks,
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Guard
//

var _ = Describe("RepairNetworkAddresses guard", func() {
	It("guard blocks: RVR addresses don't match datamesh target", func() {
		// Member has wrong IP (dispatcher triggers), but RVR has [A, B]
		// while datamesh target is [A] → guard blocks.
		m := mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")
		m.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)}

		rv := mkRV(5, []v1alpha1.DatameshMember{m}, nil, nil)
		rv.Status.Datamesh.SystemNetworkNames = []string{"net-A"}

		rvr := mkRVR("rv-1-0", "node-1", 5)
		// RVR has net-A + net-B, but datamesh target is only [net-A].
		// Guard checks RVR addresses match target → mismatch → block.
		rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			mkAddr("net-A", "10.0.0.99", 7000),
			mkAddr("net-B", "10.0.0.2", 7000),
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue()) // effective layout changed
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Settle (apply + confirm)
//

var _ = Describe("RepairNetworkAddresses settle", func() {
	// runNetworkRepairUntilStable is like runUntilStable but also sets up peer
	// connectivity so confirmAllConnected can pass.
	runNetworkRepairUntilStable := func(
		rv *v1alpha1.ReplicatedVolume,
		rvrs []*v1alpha1.ReplicatedVolumeReplica,
	) {
		const maxIter = 10
		for range maxIter {
			changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})
			if !changed {
				return
			}
			// Simulate all replicas confirming the latest revision
			// and reporting all peers as Connected.
			for _, rvr := range rvrs {
				rvr.Status.DatameshRevision = rv.Status.DatameshRevision
				rvr.Status.Peers = nil
				for _, other := range rvrs {
					if other.Name != rvr.Name {
						rvr.Status.Peers = append(rvr.Status.Peers,
							mkPeerConnected(other.Name))
					}
				}
			}
			if len(rv.Status.DatameshTransitions) == 0 {
				return
			}
		}
		Fail("runNetworkRepairUntilStable: did not stabilize")
	}

	It("apply syncs IP + confirm completes", func() {
		m := mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")
		m.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)}

		rv := mkRV(5, []v1alpha1.DatameshMember{m}, nil, nil)
		rv.Status.Datamesh.SystemNetworkNames = []string{"net-A"}

		rvr := mkRVR("rv-1-0", "node-1", 5)
		rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.99", 9000)}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}

		runNetworkRepairUntilStable(rv, rvrs)

		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		// Member address synced from RVR.
		repaired := rv.Status.Datamesh.FindMemberByName("rv-1-0")
		Expect(repaired).NotTo(BeNil())
		Expect(repaired.Addresses).To(HaveLen(1))
		Expect(repaired.Addresses[0].Address.IPv4).To(Equal("10.0.0.99"))
		Expect(repaired.Addresses[0].Address.Port).To(Equal(uint(9000)))
	})

	It("apply adds missing network", func() {
		m := mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")
		m.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)}

		rv := mkRV(5, []v1alpha1.DatameshMember{m}, nil, nil)
		rv.Status.Datamesh.SystemNetworkNames = []string{"net-A", "net-B"}

		rvr := mkRVR("rv-1-0", "node-1", 5)
		rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			mkAddr("net-A", "10.0.0.1", 7000),
			mkAddr("net-B", "10.0.0.2", 7000),
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}

		runNetworkRepairUntilStable(rv, rvrs)

		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		repaired := rv.Status.Datamesh.FindMemberByName("rv-1-0")
		Expect(repaired).NotTo(BeNil())
		Expect(repaired.Addresses).To(HaveLen(2))
		Expect(repaired.Addresses[0].SystemNetworkName).To(Equal("net-A"))
		Expect(repaired.Addresses[1].SystemNetworkName).To(Equal("net-B"))
	})

	It("apply removes stale network", func() {
		m := mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")
		m.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			mkAddr("net-A", "10.0.0.1", 7000),
			mkAddr("net-B", "10.0.0.2", 7000),
		}

		rv := mkRV(5, []v1alpha1.DatameshMember{m}, nil, nil)
		rv.Status.Datamesh.SystemNetworkNames = []string{"net-A"}

		rvr := mkRVR("rv-1-0", "node-1", 5)
		rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}

		runNetworkRepairUntilStable(rv, rvrs)

		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		repaired := rv.Status.Datamesh.FindMemberByName("rv-1-0")
		Expect(repaired).NotTo(BeNil())
		Expect(repaired.Addresses).To(HaveLen(1))
		Expect(repaired.Addresses[0].SystemNetworkName).To(Equal("net-A"))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// ChangeSystemNetworks settle (apply + confirm)
//

var _ = Describe("ChangeSystemNetworks settle", func() {
	// runCSNUntilStable iterates ProcessTransitions with RSP, calling simulate
	// between iterations to advance RVR state. Max 20 iterations for multi-step plans.
	runCSNUntilStable := func(
		rv *v1alpha1.ReplicatedVolume,
		rsp RSP,
		rvrs []*v1alpha1.ReplicatedVolumeReplica,
		simulate func(*v1alpha1.ReplicatedVolume, []*v1alpha1.ReplicatedVolumeReplica),
	) {
		const maxIter = 20
		for range maxIter {
			changed, _ := ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, FeatureFlags{})
			if !changed {
				return
			}
			simulate(rv, rvrs)
			if len(rv.Status.DatameshTransitions) == 0 {
				return
			}
		}
		Fail("runCSNUntilStable: did not stabilize")
	}

	// simulateCSN creates a simulation function that bumps revision, adds
	// addresses for newNets, and sets up peer connectivity on those networks.
	simulateCSN := func(newNets []string, addrs map[string]v1alpha1.DRBDResourceAddressStatus) func(*v1alpha1.ReplicatedVolume, []*v1alpha1.ReplicatedVolumeReplica) {
		return func(rv *v1alpha1.ReplicatedVolume, rvrs []*v1alpha1.ReplicatedVolumeReplica) {
			for _, rvr := range rvrs {
				rvr.Status.DatameshRevision = rv.Status.DatameshRevision
				// Add new addresses if not already present.
				for _, net := range newNets {
					if findAddressByNetwork(rvr.Status.Addresses, net) == nil {
						if a, ok := addrs[net]; ok {
							rvr.Status.Addresses = append(rvr.Status.Addresses, a)
						}
					}
				}
				// Set peers with connectivity on new networks.
				rvr.Status.Peers = nil
				for _, other := range rvrs {
					if other.Name != rvr.Name {
						rvr.Status.Peers = append(rvr.Status.Peers,
							mkPeerConnectedOnNetwork(other.Name, newNets...))
					}
				}
			}
		}
	}

	// ── Happy path ───────────────────────────────────────────────────────────

	It("add/v1: [A] -> [A, B]", func() {
		m := mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")
		m.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)}

		rv := mkRV(5, []v1alpha1.DatameshMember{m}, nil, nil)
		rv.Status.Datamesh.SystemNetworkNames = []string{"net-A"}

		rvr := mkRVR("rv-1-0", "node-1", 5)
		rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}

		rsp := mkRSP("node-1")
		rsp.systemNetworkNames = []string{"net-A", "net-B"}

		runCSNUntilStable(rv, rsp, rvrs, simulateCSN(
			[]string{"net-B"},
			map[string]v1alpha1.DRBDResourceAddressStatus{"net-B": mkAddr("net-B", "10.0.0.2", 7000)},
		))

		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.Datamesh.SystemNetworkNames).To(Equal([]string{"net-A", "net-B"}))
		member := rv.Status.Datamesh.FindMemberByName("rv-1-0")
		Expect(member).NotTo(BeNil())
		Expect(member.Addresses).To(HaveLen(2))
		Expect(member.Addresses[0].SystemNetworkName).To(Equal("net-A"))
		Expect(member.Addresses[1].SystemNetworkName).To(Equal("net-B"))
		Expect(member.Addresses[1].Address.IPv4).To(Equal("10.0.0.2"))
	})

	It("remove/v1: [A, B] -> [A]", func() {
		m := mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")
		m.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			mkAddr("net-A", "10.0.0.1", 7000),
			mkAddr("net-B", "10.0.0.2", 7000),
		}

		rv := mkRV(5, []v1alpha1.DatameshMember{m}, nil, nil)
		rv.Status.Datamesh.SystemNetworkNames = []string{"net-A", "net-B"}

		rvr := mkRVR("rv-1-0", "node-1", 5)
		rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			mkAddr("net-A", "10.0.0.1", 7000),
			mkAddr("net-B", "10.0.0.2", 7000),
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}

		rsp := mkRSP("node-1")
		rsp.systemNetworkNames = []string{"net-A"}

		// Guard: remaining network (net-A) — single member, no peers to check.
		runCSNUntilStable(rv, rsp, rvrs, func(rv *v1alpha1.ReplicatedVolume, rvrs []*v1alpha1.ReplicatedVolumeReplica) {
			for _, rvr := range rvrs {
				rvr.Status.DatameshRevision = rv.Status.DatameshRevision
			}
		})

		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.Datamesh.SystemNetworkNames).To(Equal([]string{"net-A"}))
		member := rv.Status.Datamesh.FindMemberByName("rv-1-0")
		Expect(member).NotTo(BeNil())
		Expect(member.Addresses).To(HaveLen(1))
		Expect(member.Addresses[0].SystemNetworkName).To(Equal("net-A"))
	})

	It("update/v1: [A, B] -> [A, C]", func() {
		m := mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")
		m.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			mkAddr("net-A", "10.0.0.1", 7000),
			mkAddr("net-B", "10.0.0.2", 7000),
		}

		rv := mkRV(5, []v1alpha1.DatameshMember{m}, nil, nil)
		rv.Status.Datamesh.SystemNetworkNames = []string{"net-A", "net-B"}

		rvr := mkRVR("rv-1-0", "node-1", 5)
		rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			mkAddr("net-A", "10.0.0.1", 7000),
			mkAddr("net-B", "10.0.0.2", 7000),
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}

		rsp := mkRSP("node-1")
		rsp.systemNetworkNames = []string{"net-A", "net-C"}

		runCSNUntilStable(rv, rsp, rvrs, simulateCSN(
			[]string{"net-C"},
			map[string]v1alpha1.DRBDResourceAddressStatus{"net-C": mkAddr("net-C", "10.0.0.3", 7000)},
		))

		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.Datamesh.SystemNetworkNames).To(Equal([]string{"net-A", "net-C"}))
		member := rv.Status.Datamesh.FindMemberByName("rv-1-0")
		Expect(member).NotTo(BeNil())
		Expect(member.Addresses).To(HaveLen(2))
		Expect(member.Addresses[0].SystemNetworkName).To(Equal("net-A"))
		Expect(member.Addresses[1].SystemNetworkName).To(Equal("net-C"))
	})

	It("migrate/v1: [A] -> [B]", func() {
		m := mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")
		m.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)}

		rv := mkRV(5, []v1alpha1.DatameshMember{m}, nil, nil)
		rv.Status.Datamesh.SystemNetworkNames = []string{"net-A"}

		rvr := mkRVR("rv-1-0", "node-1", 5)
		rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}

		rsp := mkRSP("node-1")
		rsp.systemNetworkNames = []string{"net-B"}

		runCSNUntilStable(rv, rsp, rvrs, simulateCSN(
			[]string{"net-B"},
			map[string]v1alpha1.DRBDResourceAddressStatus{"net-B": mkAddr("net-B", "10.0.0.2", 7000)},
		))

		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.Datamesh.SystemNetworkNames).To(Equal([]string{"net-B"}))
		member := rv.Status.Datamesh.FindMemberByName("rv-1-0")
		Expect(member).NotTo(BeNil())
		Expect(member.Addresses).To(HaveLen(1))
		Expect(member.Addresses[0].SystemNetworkName).To(Equal("net-B"))
		Expect(member.Addresses[0].Address.IPv4).To(Equal("10.0.0.2"))
	})

	// ── Init capture ─────────────────────────────────────────────────────────

	It("Init sets FromSystemNetworkNames and ToSystemNetworkNames", func() {
		m := mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")
		m.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)}

		rv := mkRV(5, []v1alpha1.DatameshMember{m}, nil, nil)
		rv.Status.Datamesh.SystemNetworkNames = []string{"net-A"}

		rvr := mkRVR("rv-1-0", "node-1", 5)
		rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}

		rsp := mkRSP("node-1")
		rsp.systemNetworkNames = []string{"net-A", "net-B"}

		// Run ONE iteration — transition created but stuck on confirm.
		ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, FeatureFlags{})

		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		t := &rv.Status.DatameshTransitions[0]
		Expect(t.Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeSystemNetworks))
		Expect(t.FromSystemNetworkNames).To(Equal([]string{"net-A"}))
		Expect(t.ToSystemNetworkNames).To(Equal([]string{"net-A", "net-B"}))
	})

	// ── Multi-member ─────────────────────────────────────────────────────────

	It("add/v1 with 2 members: both get new addresses", func() {
		m0 := mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")
		m0.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)}
		m1 := mkMember("rv-1-1", v1alpha1.DatameshMemberTypeTieBreaker, "node-2")
		m1.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.11", 7000)}

		rv := mkRV(5, []v1alpha1.DatameshMember{m0, m1}, nil, nil)
		rv.Status.Datamesh.SystemNetworkNames = []string{"net-A"}

		rvr0 := mkRVR("rv-1-0", "node-1", 5)
		rvr0.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)}
		rvr1 := mkRVR("rv-1-1", "node-2", 5)
		rvr1.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.11", 7000)}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr0, rvr1}

		rsp := mkRSP("node-1", "node-2")
		rsp.systemNetworkNames = []string{"net-A", "net-B"}

		runCSNUntilStable(rv, rsp, rvrs, func(rv *v1alpha1.ReplicatedVolume, rvrs []*v1alpha1.ReplicatedVolumeReplica) {
			for _, rvr := range rvrs {
				rvr.Status.DatameshRevision = rv.Status.DatameshRevision
				if findAddressByNetwork(rvr.Status.Addresses, "net-B") == nil {
					// Each node gets a different IP on net-B.
					ip := "10.0.1.1"
					if rvr.Name == "rv-1-1" {
						ip = "10.0.1.11"
					}
					rvr.Status.Addresses = append(rvr.Status.Addresses, mkAddr("net-B", ip, 7000))
				}
				rvr.Status.Peers = nil
				for _, other := range rvrs {
					if other.Name != rvr.Name {
						rvr.Status.Peers = append(rvr.Status.Peers,
							mkPeerConnectedOnNetwork(other.Name, "net-B"))
					}
				}
			}
		})

		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.Datamesh.SystemNetworkNames).To(Equal([]string{"net-A", "net-B"}))

		member0 := rv.Status.Datamesh.FindMemberByName("rv-1-0")
		Expect(member0).NotTo(BeNil())
		Expect(member0.Addresses).To(HaveLen(2))
		Expect(member0.Addresses[1].SystemNetworkName).To(Equal("net-B"))
		Expect(member0.Addresses[1].Address.IPv4).To(Equal("10.0.1.1"))

		member1 := rv.Status.Datamesh.FindMemberByName("rv-1-1")
		Expect(member1).NotTo(BeNil())
		Expect(member1.Addresses).To(HaveLen(2))
		Expect(member1.Addresses[1].SystemNetworkName).To(Equal("net-B"))
		Expect(member1.Addresses[1].Address.IPv4).To(Equal("10.0.1.11"))
	})

	It("migrate/v1 with 2 members: full swap for both", func() {
		m0 := mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")
		m0.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)}
		m1 := mkMember("rv-1-1", v1alpha1.DatameshMemberTypeTieBreaker, "node-2")
		m1.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.11", 7000)}

		rv := mkRV(5, []v1alpha1.DatameshMember{m0, m1}, nil, nil)
		rv.Status.Datamesh.SystemNetworkNames = []string{"net-A"}

		rvr0 := mkRVR("rv-1-0", "node-1", 5)
		rvr0.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)}
		rvr1 := mkRVR("rv-1-1", "node-2", 5)
		rvr1.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.11", 7000)}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr0, rvr1}

		rsp := mkRSP("node-1", "node-2")
		rsp.systemNetworkNames = []string{"net-B"}

		runCSNUntilStable(rv, rsp, rvrs, func(rv *v1alpha1.ReplicatedVolume, rvrs []*v1alpha1.ReplicatedVolumeReplica) {
			for _, rvr := range rvrs {
				rvr.Status.DatameshRevision = rv.Status.DatameshRevision
				if findAddressByNetwork(rvr.Status.Addresses, "net-B") == nil {
					ip := "10.0.1.1"
					if rvr.Name == "rv-1-1" {
						ip = "10.0.1.11"
					}
					rvr.Status.Addresses = append(rvr.Status.Addresses, mkAddr("net-B", ip, 7000))
				}
				rvr.Status.Peers = nil
				for _, other := range rvrs {
					if other.Name != rvr.Name {
						rvr.Status.Peers = append(rvr.Status.Peers,
							mkPeerConnectedOnNetwork(other.Name, "net-B"))
					}
				}
			}
		})

		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.Datamesh.SystemNetworkNames).To(Equal([]string{"net-B"}))

		member0 := rv.Status.Datamesh.FindMemberByName("rv-1-0")
		Expect(member0).NotTo(BeNil())
		Expect(member0.Addresses).To(HaveLen(1))
		Expect(member0.Addresses[0].SystemNetworkName).To(Equal("net-B"))
		Expect(member0.Addresses[0].Address.IPv4).To(Equal("10.0.1.1"))

		member1 := rv.Status.Datamesh.FindMemberByName("rv-1-1")
		Expect(member1).NotTo(BeNil())
		Expect(member1.Addresses).To(HaveLen(1))
		Expect(member1.Addresses[0].SystemNetworkName).To(Equal("net-B"))
		Expect(member1.Addresses[0].Address.IPv4).To(Equal("10.0.1.11"))
	})

	// ── Guard blocking ───────────────────────────────────────────────────────

	It("remove/v1 blocked: no connectivity on remaining network", func() {
		// 2 members, datamesh=[A, B], RSP=[A]. Peers NOT connected on A.
		// guardRemainingNetworksConnected should block.
		m0 := mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")
		m0.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			mkAddr("net-A", "10.0.0.1", 7000),
			mkAddr("net-B", "10.0.0.2", 7000),
		}
		m1 := mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2")
		m1.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			mkAddr("net-A", "10.0.0.11", 7000),
			mkAddr("net-B", "10.0.0.12", 7000),
		}

		rv := mkRV(5, []v1alpha1.DatameshMember{m0, m1}, nil, nil)
		rv.Status.Datamesh.SystemNetworkNames = []string{"net-A", "net-B"}

		rvr0 := mkRVR("rv-1-0", "node-1", 5)
		rvr0.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			mkAddr("net-A", "10.0.0.1", 7000),
			mkAddr("net-B", "10.0.0.2", 7000),
		}
		rvr1 := mkRVR("rv-1-1", "node-2", 5)
		rvr1.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			mkAddr("net-A", "10.0.0.11", 7000),
			mkAddr("net-B", "10.0.0.12", 7000),
		}
		// Peers connected only on net-B, NOT on net-A (remaining).
		rvr0.Status.Peers = []v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{
			mkPeerConnectedOnNetwork("rv-1-1", "net-B"),
		}
		rvr1.Status.Peers = []v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{
			mkPeerConnectedOnNetwork("rv-1-0", "net-B"),
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr0, rvr1}

		rsp := mkRSP("node-1", "node-2")
		rsp.systemNetworkNames = []string{"net-A"}

		ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, FeatureFlags{})

		// Guard blocks — no CSN transition created.
		hasCSN := false
		for _, t := range rv.Status.DatameshTransitions {
			if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeSystemNetworks {
				hasCSN = true
			}
		}
		Expect(hasCSN).To(BeFalse(), "remove/v1 should be blocked by guardRemainingNetworksConnected")
	})

	It("remove/v1 unblocked after connectivity established", func() {
		// Same setup as blocked test, but after first iteration add connectivity on net-A.
		m0 := mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")
		m0.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			mkAddr("net-A", "10.0.0.1", 7000),
			mkAddr("net-B", "10.0.0.2", 7000),
		}
		m1 := mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2")
		m1.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			mkAddr("net-A", "10.0.0.11", 7000),
			mkAddr("net-B", "10.0.0.12", 7000),
		}

		rv := mkRV(5, []v1alpha1.DatameshMember{m0, m1}, nil, nil)
		rv.Status.Datamesh.SystemNetworkNames = []string{"net-A", "net-B"}

		rvr0 := mkRVR("rv-1-0", "node-1", 5)
		rvr0.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			mkAddr("net-A", "10.0.0.1", 7000),
			mkAddr("net-B", "10.0.0.2", 7000),
		}
		rvr1 := mkRVR("rv-1-1", "node-2", 5)
		rvr1.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			mkAddr("net-A", "10.0.0.11", 7000),
			mkAddr("net-B", "10.0.0.12", 7000),
		}
		// Initially: peers connected only on net-B.
		rvr0.Status.Peers = []v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{
			mkPeerConnectedOnNetwork("rv-1-1", "net-B"),
		}
		rvr1.Status.Peers = []v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{
			mkPeerConnectedOnNetwork("rv-1-0", "net-B"),
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr0, rvr1}

		rsp := mkRSP("node-1", "node-2")
		rsp.systemNetworkNames = []string{"net-A"}

		// Phase 1: blocked.
		ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, FeatureFlags{})
		hasCSN := false
		for _, t := range rv.Status.DatameshTransitions {
			if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeSystemNetworks {
				hasCSN = true
			}
		}
		Expect(hasCSN).To(BeFalse(), "should be blocked initially")

		// Phase 2: establish connectivity on net-A.
		rvr0.Status.Peers = []v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{
			mkPeerConnectedOnNetwork("rv-1-1", "net-A", "net-B"),
		}
		rvr1.Status.Peers = []v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{
			mkPeerConnectedOnNetwork("rv-1-0", "net-A", "net-B"),
		}

		runCSNUntilStable(rv, rsp, rvrs, func(rv *v1alpha1.ReplicatedVolume, rvrs []*v1alpha1.ReplicatedVolumeReplica) {
			for _, rvr := range rvrs {
				rvr.Status.DatameshRevision = rv.Status.DatameshRevision
			}
		})

		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.Datamesh.SystemNetworkNames).To(Equal([]string{"net-A"}))
	})

	// ── Edge cases ───────────────────────────────────────────────────────────

	It("add/v1: multiple networks [A] -> [A, B, C]", func() {
		m := mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")
		m.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)}

		rv := mkRV(5, []v1alpha1.DatameshMember{m}, nil, nil)
		rv.Status.Datamesh.SystemNetworkNames = []string{"net-A"}

		rvr := mkRVR("rv-1-0", "node-1", 5)
		rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}

		rsp := mkRSP("node-1")
		rsp.systemNetworkNames = []string{"net-A", "net-B", "net-C"}

		runCSNUntilStable(rv, rsp, rvrs, simulateCSN(
			[]string{"net-B", "net-C"},
			map[string]v1alpha1.DRBDResourceAddressStatus{
				"net-B": mkAddr("net-B", "10.0.0.2", 7000),
				"net-C": mkAddr("net-C", "10.0.0.3", 7000),
			},
		))

		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.Datamesh.SystemNetworkNames).To(Equal([]string{"net-A", "net-B", "net-C"}))
		member := rv.Status.Datamesh.FindMemberByName("rv-1-0")
		Expect(member).NotTo(BeNil())
		Expect(member.Addresses).To(HaveLen(3))
		Expect(member.Addresses[0].SystemNetworkName).To(Equal("net-A"))
		Expect(member.Addresses[1].SystemNetworkName).To(Equal("net-B"))
		Expect(member.Addresses[2].SystemNetworkName).To(Equal("net-C"))
	})

	It("migrate/v1: full swap [A, B] -> [C, D]", func() {
		m := mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")
		m.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			mkAddr("net-A", "10.0.0.1", 7000),
			mkAddr("net-B", "10.0.0.2", 7000),
		}

		rv := mkRV(5, []v1alpha1.DatameshMember{m}, nil, nil)
		rv.Status.Datamesh.SystemNetworkNames = []string{"net-A", "net-B"}

		rvr := mkRVR("rv-1-0", "node-1", 5)
		rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			mkAddr("net-A", "10.0.0.1", 7000),
			mkAddr("net-B", "10.0.0.2", 7000),
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}

		rsp := mkRSP("node-1")
		rsp.systemNetworkNames = []string{"net-C", "net-D"}

		runCSNUntilStable(rv, rsp, rvrs, simulateCSN(
			[]string{"net-C", "net-D"},
			map[string]v1alpha1.DRBDResourceAddressStatus{
				"net-C": mkAddr("net-C", "10.0.0.3", 7000),
				"net-D": mkAddr("net-D", "10.0.0.4", 7000),
			},
		))

		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.Datamesh.SystemNetworkNames).To(Equal([]string{"net-C", "net-D"}))
		member := rv.Status.Datamesh.FindMemberByName("rv-1-0")
		Expect(member).NotTo(BeNil())
		Expect(member.Addresses).To(HaveLen(2))
		Expect(member.Addresses[0].SystemNetworkName).To(Equal("net-C"))
		Expect(member.Addresses[1].SystemNetworkName).To(Equal("net-D"))
	})
})
