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

// ──────────────────────────────────────────────────────────────────────────────
// Dispatcher
//

var _ = Describe("RepairNetworkAddresses dispatch", func() {
	It("skip: addresses in sync", func() {
		m := mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")
		m.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)}

		rv := mkRV(5, []v1alpha1.DatameshMember{m}, nil, nil)
		rv.Status.Datamesh.SystemNetworkNames = []string{"net-A"}

		rvr := mkRVR("rv-1-0", "node-1", 5)
		rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}

		settleEffectiveLayout(rv, rvrs)

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeFalse())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})

	It("dispatch: IP changed", func() {
		m := mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")
		m.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)}

		rv := mkRV(5, []v1alpha1.DatameshMember{m}, nil, nil)
		rv.Status.Datamesh.SystemNetworkNames = []string{"net-A"}

		rvr := mkRVR("rv-1-0", "node-1", 5)
		rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.99", 7000)}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		t := &rv.Status.DatameshTransitions[0]
		Expect(t.Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeRepairNetworkAddresses))
		Expect(t.PlanID).To(Equal("repair/v1"))
	})

	It("dispatch: missing network in member", func() {
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

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeRepairNetworkAddresses))
	})

	It("dispatch: stale network in member", func() {
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

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeRepairNetworkAddresses))
	})

	It("skip: no systemNetworkNames", func() {
		m := mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")
		m.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)}

		rv := mkRV(5, []v1alpha1.DatameshMember{m}, nil, nil)
		// No systemNetworkNames set.

		rvr := mkRVR("rv-1-0", "node-1", 5)
		rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.99", 7000)}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}

		settleEffectiveLayout(rv, rvrs)

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeFalse())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})
})

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
	// connectivity so confirmAllMembersConnected can pass.
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
