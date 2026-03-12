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

// Integration tests: network transitions.
//
// Tests sequences and interactions of network transitions (CSN, Repair)
// with each other and with membership transitions. Unlike unit settle
// tests that test one plan in isolation, these exercise the full engine
// with multiple concurrent transition groups.

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// simulateNetworkAndMembership creates a simulation function that handles
// both membership confirms (revision bump) and network confirms (addresses
// for specified networks + peer connectivity on those networks).
// Addresses are auto-generated as 10.<netIndex>.0.<replicaID+1>:7000.
func simulateNetworkAndMembership(
	nets []string,
	netAddrs map[string]func(rvrName string) v1alpha1.DRBDResourceAddressStatus,
) func(*v1alpha1.ReplicatedVolume, []*v1alpha1.ReplicatedVolumeReplica) {
	return func(rv *v1alpha1.ReplicatedVolume, rvrs []*v1alpha1.ReplicatedVolumeReplica) {
		for _, rvr := range rvrs {
			rvr.Status.DatameshRevision = rv.Status.DatameshRevision
			// Add addresses for specified networks if not present.
			for _, net := range nets {
				if findAddressByNetwork(rvr.Status.Addresses, net) == nil {
					if mkAddr, ok := netAddrs[net]; ok {
						rvr.Status.Addresses = append(rvr.Status.Addresses, mkAddr(rvr.Name))
					}
				}
			}
			// Set peers with connectivity on all specified networks.
			rvr.Status.Peers = nil
			for _, other := range rvrs {
				if other.Name != rvr.Name {
					rvr.Status.Peers = append(rvr.Status.Peers,
						mkPeerConnectedOnNetwork(other.Name, nets...))
				}
			}
		}
	}
}

// addrFor returns a factory that creates addresses for a given network
// with deterministic IPs based on the RVR name suffix.
func addrFor(net string, subnet string) func(string) v1alpha1.DRBDResourceAddressStatus {
	return func(rvrName string) v1alpha1.DRBDResourceAddressStatus {
		// Extract numeric suffix from name like "rv-1-2" → "2".
		id := rvrName[len(rvrName)-1] - '0'
		return v1alpha1.DRBDResourceAddressStatus{
			SystemNetworkName: net,
			Address:           v1alpha1.DRBDAddress{IPv4: fmt.Sprintf("%s.%d", subnet, id+1), Port: 7000},
		}
	}
}

var _ = Describe("integration: network transitions", func() {

	// ── 1. Sequential CSN: add then remove ───────────────────────────────

	It("add/v1 then remove/v1: add network, then remove it", func() {
		m0 := mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")
		m0.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)}
		m1 := mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2")
		m1.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.2", 7000)}

		rv := mkRV(5, []v1alpha1.DatameshMember{m0, m1}, nil, nil)
		rv.Status.Datamesh.SystemNetworkNames = []string{"net-A"}

		rvr0 := mkRVR("rv-1-0", "node-1", 5)
		rvr0.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)}
		rvr1 := mkRVR("rv-1-1", "node-2", 5)
		rvr1.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.2", 7000)}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr0, rvr1}

		rsp := mkRSP("node-1", "node-2")

		// Phase 1: add net-B.
		rsp.systemNetworkNames = []string{"net-A", "net-B"}
		runSettleLoop(rv, rsp, rvrs, nil, FeatureFlags{}, simulateNetworkAndMembership(
			[]string{"net-B"},
			map[string]func(string) v1alpha1.DRBDResourceAddressStatus{
				"net-B": addrFor("net-B", "10.1.0"),
			},
		), nil)

		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.Datamesh.SystemNetworkNames).To(Equal([]string{"net-A", "net-B"}))
		for _, m := range rv.Status.Datamesh.Members {
			Expect(m.Addresses).To(HaveLen(2))
		}

		// Phase 2: remove net-B.
		// Set peers with connectivity on net-A (remaining) before phase 2 starts
		// so guardRemainingNetworksConnected can pass on the first iteration.
		for _, rvr := range rvrs {
			rvr.Status.Peers = nil
			for _, other := range rvrs {
				if other.Name != rvr.Name {
					rvr.Status.Peers = append(rvr.Status.Peers,
						mkPeerConnectedOnNetwork(other.Name, "net-A"))
				}
			}
		}
		rsp.systemNetworkNames = []string{"net-A"}
		runSettleLoop(rv, rsp, rvrs, nil, FeatureFlags{}, simulateNetworkAndMembership(
			[]string{"net-A"},
			map[string]func(string) v1alpha1.DRBDResourceAddressStatus{},
		), nil)

		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.Datamesh.SystemNetworkNames).To(Equal([]string{"net-A"}))
		for _, m := range rv.Status.Datamesh.Members {
			Expect(m.Addresses).To(HaveLen(1))
			Expect(m.Addresses[0].SystemNetworkName).To(Equal("net-A"))
		}
	})

	// ── 2. Sequential CSN: add then migrate ──────────────────────────────

	It("add/v1 then migrate/v1: add network, then full replacement", func() {
		m0 := mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")
		m0.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)}
		m1 := mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2")
		m1.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.2", 7000)}

		rv := mkRV(5, []v1alpha1.DatameshMember{m0, m1}, nil, nil)
		rv.Status.Datamesh.SystemNetworkNames = []string{"net-A"}

		rvr0 := mkRVR("rv-1-0", "node-1", 5)
		rvr0.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)}
		rvr1 := mkRVR("rv-1-1", "node-2", 5)
		rvr1.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.2", 7000)}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr0, rvr1}

		rsp := mkRSP("node-1", "node-2")

		// Phase 1: add net-B.
		rsp.systemNetworkNames = []string{"net-A", "net-B"}
		runSettleLoop(rv, rsp, rvrs, nil, FeatureFlags{}, simulateNetworkAndMembership(
			[]string{"net-B"},
			map[string]func(string) v1alpha1.DRBDResourceAddressStatus{
				"net-B": addrFor("net-B", "10.1.0"),
			},
		), nil)

		Expect(rv.Status.Datamesh.SystemNetworkNames).To(Equal([]string{"net-A", "net-B"}))

		// Phase 2: migrate to net-C (full replacement).
		rsp.systemNetworkNames = []string{"net-C"}
		runSettleLoop(rv, rsp, rvrs, nil, FeatureFlags{}, simulateNetworkAndMembership(
			[]string{"net-C"},
			map[string]func(string) v1alpha1.DRBDResourceAddressStatus{
				"net-C": addrFor("net-C", "10.2.0"),
			},
		), nil)

		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.Datamesh.SystemNetworkNames).To(Equal([]string{"net-C"}))
		for _, m := range rv.Status.Datamesh.Members {
			Expect(m.Addresses).To(HaveLen(1))
			Expect(m.Addresses[0].SystemNetworkName).To(Equal("net-C"))
		}
	})

	// ── 3. CSN + concurrent AddReplica(D) ────────────────────────────────

	It("add/v1 + AddReplica(D): both complete concurrently", func() {
		m0 := mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")
		m0.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)}
		m1 := mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2")
		m1.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.2", 7000)}

		rv := mkRV(5, []v1alpha1.DatameshMember{m0, m1}, nil, nil)
		rv.Status.Datamesh.SystemNetworkNames = []string{"net-A"}
		rv.Status.DatameshReplicaRequests = []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
			mkJoinRequestD("rv-1-2"),
		}

		rvr0 := mkRVR("rv-1-0", "node-1", 5)
		rvr0.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)}
		rvr1 := mkRVR("rv-1-1", "node-2", 5)
		rvr1.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.2", 7000)}
		rvr2 := mkRVR("rv-1-2", "node-3", 0)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr0, rvr1, rvr2}

		rsp := mkRSP("node-1", "node-2", "node-3")
		rsp.systemNetworkNames = []string{"net-A", "net-B"}

		runSettleLoop(rv, rsp, rvrs, nil, FeatureFlags{}, simulateNetworkAndMembership(
			[]string{"net-B"},
			map[string]func(string) v1alpha1.DRBDResourceAddressStatus{
				"net-B": addrFor("net-B", "10.1.0"),
			},
		), nil)

		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		// CSN completed: both networks present.
		Expect(rv.Status.Datamesh.SystemNetworkNames).To(Equal([]string{"net-A", "net-B"}))
		// AddReplica completed: 3 D members.
		var dCount int
		for _, m := range rv.Status.Datamesh.Members {
			if m.Type.IsVoter() {
				dCount++
			}
		}
		Expect(dCount).To(BeNumerically(">=", 3))
	})

	// ── 4. CSN + concurrent RemoveReplica(D) ─────────────────────────────

	It("remove/v1 + RemoveReplica(D): both complete concurrently", func() {
		m0 := mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")
		m0.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			mkAddr("net-A", "10.0.0.1", 7000),
			mkAddr("net-B", "10.1.0.1", 7000),
		}
		m1 := mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2")
		m1.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			mkAddr("net-A", "10.0.0.2", 7000),
			mkAddr("net-B", "10.1.0.2", 7000),
		}
		m2 := mkMember("rv-1-2", v1alpha1.DatameshMemberTypeDiskful, "node-3")
		m2.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			mkAddr("net-A", "10.0.0.3", 7000),
			mkAddr("net-B", "10.1.0.3", 7000),
		}

		rv := mkRV(5, []v1alpha1.DatameshMember{m0, m1, m2}, nil, nil)
		rv.Status.Datamesh.SystemNetworkNames = []string{"net-A", "net-B"}
		rv.Status.Datamesh.Quorum = 2
		rv.Status.DatameshReplicaRequests = []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
			mkLeaveRequest("rv-1-2"),
		}

		rvr0 := mkRVRUpToDate("rv-1-0", "node-1", 5)
		rvr0.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			mkAddr("net-A", "10.0.0.1", 7000), mkAddr("net-B", "10.1.0.1", 7000),
		}
		rvr1 := mkRVRUpToDate("rv-1-1", "node-2", 5)
		rvr1.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			mkAddr("net-A", "10.0.0.2", 7000), mkAddr("net-B", "10.1.0.2", 7000),
		}
		rvr2 := mkRVRUpToDate("rv-1-2", "node-3", 5)
		rvr2.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			mkAddr("net-A", "10.0.0.3", 7000), mkAddr("net-B", "10.1.0.3", 7000),
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr0, rvr1, rvr2}

		// Set initial peer connectivity on net-A (remaining) for guardRemainingNetworksConnected.
		for _, rvr := range rvrs {
			rvr.Status.Peers = nil
			for _, other := range rvrs {
				if other.Name != rvr.Name {
					rvr.Status.Peers = append(rvr.Status.Peers,
						mkPeerConnectedOnNetwork(other.Name, "net-A"))
				}
			}
		}

		rsp := mkRSP("node-1", "node-2", "node-3")
		rsp.systemNetworkNames = []string{"net-A"} // remove net-B

		// Simulate: revision bump + connectivity on remaining net-A.
		runSettleLoop(rv, rsp, rvrs, nil, FeatureFlags{}, simulateNetworkAndMembership(
			[]string{"net-A"},
			map[string]func(string) v1alpha1.DRBDResourceAddressStatus{},
		), nil)

		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		// CSN: net-B removed.
		Expect(rv.Status.Datamesh.SystemNetworkNames).To(Equal([]string{"net-A"}))
		// RemoveReplica: rv-1-2 removed, 2D remain.
		Expect(rv.Status.Datamesh.FindMemberByName("rv-1-2")).To(BeNil())
		var dCount int
		for _, m := range rv.Status.Datamesh.Members {
			if m.Type.IsVoter() {
				dCount++
			}
		}
		Expect(dCount).To(Equal(2))
	})

	// ── 5. Repair + concurrent AddReplica ────────────────────────────────

	It("repair + AddReplica(A): both complete concurrently", func() {
		m0 := mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")
		m0.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)} // stale IP

		rv := mkRV(5, []v1alpha1.DatameshMember{m0}, nil, nil)
		rv.Status.Datamesh.SystemNetworkNames = []string{"net-A"}
		rv.Status.DatameshReplicaRequests = []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
			mkJoinRequestAccess("rv-1-1"),
		}

		rvr0 := mkRVR("rv-1-0", "node-1", 5)
		rvr0.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.99", 7000)} // new IP
		rvr1 := mkRVR("rv-1-1", "node-2", 0)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr0, rvr1}

		runSettleLoop(rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{}, simulateWithPeers, nil)

		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		// Repair completed: address synced.
		repaired := rv.Status.Datamesh.FindMemberByName("rv-1-0")
		Expect(repaired).NotTo(BeNil())
		Expect(repaired.Addresses[0].Address.IPv4).To(Equal("10.0.0.99"))
		// AddReplica completed: Access member added.
		Expect(rv.Status.Datamesh.FindMemberByName("rv-1-1")).NotTo(BeNil())
		Expect(rv.Status.Datamesh.FindMemberByName("rv-1-1").Type).To(Equal(v1alpha1.DatameshMemberTypeAccess))
	})

	// ── 6. Multi-member network add (3D+TB) ──────────────────────────────

	It("add/v1 with 3D+TB: all members get new addresses", func() {
		members := []v1alpha1.DatameshMember{
			func() v1alpha1.DatameshMember {
				m := mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")
				m.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)}
				return m
			}(),
			func() v1alpha1.DatameshMember {
				m := mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2")
				m.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.2", 7000)}
				return m
			}(),
			func() v1alpha1.DatameshMember {
				m := mkMember("rv-1-2", v1alpha1.DatameshMemberTypeDiskful, "node-3")
				m.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.3", 7000)}
				return m
			}(),
			func() v1alpha1.DatameshMember {
				m := mkMember("rv-1-3", v1alpha1.DatameshMemberTypeTieBreaker, "node-4")
				m.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.4", 7000)}
				return m
			}(),
		}

		rv := mkRV(5, members, nil, nil)
		rv.Status.Datamesh.SystemNetworkNames = []string{"net-A"}
		rv.Status.Datamesh.Quorum = 2

		rvrs := make([]*v1alpha1.ReplicatedVolumeReplica, 4)
		for i := range 4 {
			name := fmt.Sprintf("rv-1-%d", i)
			node := fmt.Sprintf("node-%d", i+1)
			rvrs[i] = mkRVR(name, node, 5)
			rvrs[i].Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
				mkAddr("net-A", fmt.Sprintf("10.0.0.%d", i+1), 7000),
			}
		}

		rsp := mkRSP("node-1", "node-2", "node-3", "node-4")
		rsp.systemNetworkNames = []string{"net-A", "net-B"}

		runSettleLoop(rv, rsp, rvrs, nil, FeatureFlags{}, simulateNetworkAndMembership(
			[]string{"net-B"},
			map[string]func(string) v1alpha1.DRBDResourceAddressStatus{
				"net-B": addrFor("net-B", "10.1.0"),
			},
		), nil)

		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.Datamesh.SystemNetworkNames).To(Equal([]string{"net-A", "net-B"}))
		// All 4 members have 2 addresses.
		for _, m := range rv.Status.Datamesh.Members {
			Expect(m.Addresses).To(HaveLen(2), "member %s should have 2 addresses", m.Name)
			Expect(m.Addresses[0].SystemNetworkName).To(Equal("net-A"))
			Expect(m.Addresses[1].SystemNetworkName).To(Equal("net-B"))
		}
	})
})
