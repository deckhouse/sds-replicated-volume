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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/dmte"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/idset"
)

// ──────────────────────────────────────────────────────────────────────────────
// Test helpers
//

// peerSetup describes a peer in a replica's peer list.
type peerSetup struct {
	peerID        uint8
	connected     bool     // ConnectionState == Connected
	establishedOn []string // ConnectionEstablishedOn
}

// connSetup describes one replica for connectivity tests.
type connSetup struct {
	id         uint8
	mType      v1alpha1.DatameshMemberType // "" = no member
	agentReady bool                        // false = AgentNotReady condition
	rvrNil     bool                        // force rvr=nil
	rev        int64                       // DatameshRevision; ignored if rvrNil
	peers      []peerSetup                 // peer list on this RVR
	addresses  []string                    // SystemNetworkNames on member.Addresses
}

// buildConnCtx builds a globalContext for connectivity tests.
func buildConnCtx(entries []connSetup) *globalContext {
	gctx := &globalContext{}
	gctx.allReplicas = make([]ReplicaContext, len(entries))

	for i, e := range entries {
		rctx := &gctx.allReplicas[i]
		rctx.gctx = gctx
		rctx.id = e.id
		rctx.name = fmt.Sprintf("rv-1-%d", e.id)

		if e.mType != "" {
			m := &v1alpha1.DatameshMember{
				Name: rctx.name,
				Type: e.mType,
			}
			for _, net := range e.addresses {
				m.Addresses = append(m.Addresses, v1alpha1.DRBDResourceAddressStatus{
					SystemNetworkName: net,
					Address:           v1alpha1.DRBDAddress{IPv4: "10.0.0.1", Port: 7000},
				})
			}
			rctx.member = m
		}

		if !e.rvrNil {
			rvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: rctx.name},
			}
			rvr.Status.DatameshRevision = e.rev

			if !e.agentReady {
				rvr.Status.Conditions = []metav1.Condition{{
					Type:   v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType,
					Status: metav1.ConditionFalse,
					Reason: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonAgentNotReady,
				}}
			}

			for _, p := range e.peers {
				peer := v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{
					Name: fmt.Sprintf("rv-1-%d", p.peerID),
				}
				if p.connected {
					peer.ConnectionState = v1alpha1.ConnectionStateConnected
				} else {
					peer.ConnectionState = v1alpha1.ConnectionStateStandAlone
				}
				peer.ConnectionEstablishedOn = p.establishedOn
				rvr.Status.Peers = append(rvr.Status.Peers, peer)
			}

			rctx.rvr = rvr
		}

		gctx.replicas[e.id] = rctx
	}

	return gctx
}

// ──────────────────────────────────────────────────────────────────────────────
// peerConnected / peerConnectedOnNetwork
//

var _ = Describe("peerConnected", func() {
	mkRVR := func(peers []v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus) *v1alpha1.ReplicatedVolumeReplica {
		rvr := &v1alpha1.ReplicatedVolumeReplica{}
		rvr.Status.Peers = peers
		return rvr
	}

	It("returns true for Connected peer", func() {
		rvr := mkRVR([]v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{
			{Name: "rv-1-1", ConnectionState: v1alpha1.ConnectionStateConnected},
		})
		Expect(peerConnected(rvr, 1)).To(BeTrue())
	})

	It("returns false for non-Connected peer", func() {
		rvr := mkRVR([]v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{
			{Name: "rv-1-1", ConnectionState: v1alpha1.ConnectionStateStandAlone},
		})
		Expect(peerConnected(rvr, 1)).To(BeFalse())
	})

	It("returns false for missing peer", func() {
		rvr := mkRVR([]v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{
			{Name: "rv-1-2", ConnectionState: v1alpha1.ConnectionStateConnected},
		})
		Expect(peerConnected(rvr, 1)).To(BeFalse())
	})

	It("returns false for empty peer list", func() {
		rvr := mkRVR(nil)
		Expect(peerConnected(rvr, 1)).To(BeFalse())
	})
})

var _ = Describe("peerConnectedOnNetwork", func() {
	mkRVR := func(peers []v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus) *v1alpha1.ReplicatedVolumeReplica {
		rvr := &v1alpha1.ReplicatedVolumeReplica{}
		rvr.Status.Peers = peers
		return rvr
	}

	It("returns true for correct network", func() {
		rvr := mkRVR([]v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{
			{Name: "rv-1-1", ConnectionEstablishedOn: []string{"net-A", "net-B"}},
		})
		check := peerConnectedOnNetwork("net-A")
		Expect(check(rvr, 1)).To(BeTrue())
	})

	It("returns false for wrong network", func() {
		rvr := mkRVR([]v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{
			{Name: "rv-1-1", ConnectionEstablishedOn: []string{"net-A"}},
		})
		check := peerConnectedOnNetwork("net-B")
		Expect(check(rvr, 1)).To(BeFalse())
	})

	It("returns false for missing peer", func() {
		rvr := mkRVR([]v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{
			{Name: "rv-1-2", ConnectionEstablishedOn: []string{"net-A"}},
		})
		check := peerConnectedOnNetwork("net-A")
		Expect(check(rvr, 1)).To(BeFalse())
	})

	It("returns false for empty ConnectionEstablishedOn", func() {
		rvr := mkRVR([]v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{
			{Name: "rv-1-1"},
		})
		check := peerConnectedOnNetwork("net-A")
		Expect(check(rvr, 1)).To(BeFalse())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// connectionVerified — exhaustive
//

// sideState describes one side of a connection for exhaustive enumeration.
type sideState struct {
	label     string
	hasRVR    bool
	ready     bool // agent ready (only when hasRVR)
	peerState int  // 0=missing, 1=Connected, 2=other (only when hasRVR)
}

var allSideStates = []sideState{
	{"nil", false, false, 0},
	{"ready+missing", true, true, 0},
	{"ready+Connected", true, true, 1},
	{"ready+other", true, true, 2},
	{"stale+missing", true, false, 0},
	{"stale+Connected", true, false, 1},
	{"stale+other", true, false, 2},
}

func buildSideRctx(id uint8, s sideState, otherID uint8) *ReplicaContext {
	rctx := &ReplicaContext{id: id, name: fmt.Sprintf("rv-1-%d", id)}
	if !s.hasRVR {
		return rctx
	}

	rvr := &v1alpha1.ReplicatedVolumeReplica{}
	if !s.ready {
		rvr.Status.Conditions = []metav1.Condition{{
			Type:   v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType,
			Status: metav1.ConditionFalse,
			Reason: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonAgentNotReady,
		}}
	}
	if s.peerState > 0 {
		peer := v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{
			Name: fmt.Sprintf("rv-1-%d", otherID),
		}
		if s.peerState == 1 {
			peer.ConnectionState = v1alpha1.ConnectionStateConnected
			peer.ConnectionEstablishedOn = []string{"test-net"}
		} else {
			peer.ConnectionState = v1alpha1.ConnectionStateStandAlone
		}
		rvr.Status.Peers = []v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{peer}
	}
	rctx.rvr = rvr
	return rctx
}

func canVerify(s sideState) bool {
	return s.hasRVR && s.ready && s.peerState == 1
}

var _ = Describe("connectionVerified exhaustive", func() {
	for _, sa := range allSideStates {
		for _, sb := range allSideStates {
			label := fmt.Sprintf("a=%s, b=%s", sa.label, sb.label)

			It(label, func() {
				a := buildSideRctx(0, sa, 1)
				b := buildSideRctx(1, sb, 0)

				expected := canVerify(sa) || canVerify(sb)
				Expect(connectionVerified(a, b, 0, peerConnected)).To(Equal(expected), "forward")
				Expect(connectionVerified(b, a, 0, peerConnected)).To(Equal(expected), "symmetry")
			})
		}
	}

	It("b=nil pointer", func() {
		a := buildSideRctx(0, sideState{"ready+Connected", true, true, 1}, 1)
		Expect(connectionVerified(a, nil, 0, peerConnected)).To(BeFalse())
	})
})

var _ = Describe("connectionVerified minRevision", func() {
	It("both sides rev=0, minRevision=10 → not verified", func() {
		a := buildSideRctx(0, sideState{"ready+Connected", true, true, 1}, 1)
		b := buildSideRctx(1, sideState{"ready+Connected", true, true, 1}, 0)
		// Both sides have DatameshRevision=0 (default), minRevision=10.
		Expect(connectionVerified(a, b, 10, peerConnected)).To(BeFalse())
	})

	It("one side rev=10, minRevision=10 → verified", func() {
		a := buildSideRctx(0, sideState{"ready+Connected", true, true, 1}, 1)
		b := buildSideRctx(1, sideState{"ready+Connected", true, true, 1}, 0)
		a.rvr.Status.DatameshRevision = 10
		Expect(connectionVerified(a, b, 10, peerConnected)).To(BeTrue())
	})

	It("both sides rev=10, minRevision=10 → verified", func() {
		a := buildSideRctx(0, sideState{"ready+Connected", true, true, 1}, 1)
		b := buildSideRctx(1, sideState{"ready+Connected", true, true, 1}, 0)
		a.rvr.Status.DatameshRevision = 10
		b.rvr.Status.DatameshRevision = 10
		Expect(connectionVerified(a, b, 10, peerConnected)).To(BeTrue())
	})
})

var _ = Describe("connectionVerified with peerConnectedOnNetwork", func() {
	It("correct network → verified", func() {
		a := buildSideRctx(0, sideState{"ready+Connected", true, true, 1}, 1)
		b := buildSideRctx(1, sideState{"ready+Connected", true, true, 1}, 0)
		// buildSideRctx sets ConnectionEstablishedOn = ["test-net"] when peerState==1.
		Expect(connectionVerified(a, b, 0, peerConnectedOnNetwork("test-net"))).To(BeTrue())
	})

	It("wrong network → not verified", func() {
		a := buildSideRctx(0, sideState{"ready+Connected", true, true, 1}, 1)
		b := buildSideRctx(1, sideState{"ready+Connected", true, true, 1}, 0)
		Expect(connectionVerified(a, b, 0, peerConnectedOnNetwork("other-net"))).To(BeFalse())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// expectedPeerIDs — table-driven
//

var _ = Describe("expectedPeerIDs", func() {
	type peerCase struct {
		name     string
		entries  []connSetup
		subject  uint8
		expected idset.IDSet
	}

	cases := []peerCase{
		{
			name: "FM(D) sees all members",
			entries: []connSetup{
				{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true},
				{id: 1, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true},
				{id: 2, mType: v1alpha1.DatameshMemberTypeAccess, agentReady: true},
				{id: 3, mType: v1alpha1.DatameshMemberTypeTieBreaker, agentReady: true},
			},
			subject:  0,
			expected: idset.Of(1, 2, 3),
		},
		{
			name: "Star(A) sees FM only",
			entries: []connSetup{
				{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true},
				{id: 1, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true},
				{id: 2, mType: v1alpha1.DatameshMemberTypeAccess, agentReady: true},
				{id: 3, mType: v1alpha1.DatameshMemberTypeTieBreaker, agentReady: true},
			},
			subject:  2,
			expected: idset.Of(0, 1),
		},
		{
			name: "Star(TB) sees FM only",
			entries: []connSetup{
				{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true},
				{id: 1, mType: v1alpha1.DatameshMemberTypeShadowDiskful, agentReady: true},
				{id: 2, mType: v1alpha1.DatameshMemberTypeTieBreaker, agentReady: true},
			},
			subject:  2,
			expected: idset.Of(0, 1),
		},
		{
			name: "FM(sD) sees all",
			entries: []connSetup{
				{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true},
				{id: 1, mType: v1alpha1.DatameshMemberTypeAccess, agentReady: true},
				{id: 2, mType: v1alpha1.DatameshMemberTypeShadowDiskful, agentReady: true},
			},
			subject:  2,
			expected: idset.Of(0, 1),
		},
		{
			name: "FM(D0) sees all",
			entries: []connSetup{
				{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true},
				{id: 1, mType: v1alpha1.DatameshMemberTypeLiminalDiskful, agentReady: true},
				{id: 2, mType: v1alpha1.DatameshMemberTypeTieBreaker, agentReady: true},
			},
			subject:  1,
			expected: idset.Of(0, 2),
		},
		{
			name: "Self excluded (single member)",
			entries: []connSetup{
				{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true},
			},
			subject:  0,
			expected: 0, // empty IDSet
		},
		{
			name: "Mixed liminal: sD0 sees all",
			entries: []connSetup{
				{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true},
				{id: 1, mType: v1alpha1.DatameshMemberTypeLiminalShadowDiskful, agentReady: true},
				{id: 2, mType: v1alpha1.DatameshMemberTypeAccess, agentReady: true},
			},
			subject:  1,
			expected: idset.Of(0, 2),
		},
	}

	for _, tc := range cases {
		It(tc.name, func() {
			gctx := buildConnCtx(tc.entries)
			rctx := gctx.replicas[tc.subject]
			allMembers := allMemberIDs(gctx)
			fmMembers := fullMeshMemberIDs(gctx)
			Expect(expectedPeerIDs(rctx.member.Type, rctx.id, allMembers, fmMembers)).To(Equal(tc.expected))
		})
	}
})

// ──────────────────────────────────────────────────────────────────────────────
// allConnectionsOfMemberVerified
//

var _ = Describe("allConnectionsOfMemberVerified", func() {
	It("2 FM members, both connected → true", func() {
		gctx := buildConnCtx([]connSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true,
				peers: []peerSetup{{peerID: 1, connected: true}}},
			{id: 1, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true,
				peers: []peerSetup{{peerID: 0, connected: true}}},
		})
		allMembers := allMemberIDs(gctx)
		fmMembers := fullMeshMemberIDs(gctx)
		Expect(allConnectionsOfMemberVerified(gctx, gctx.replicas[0], 0, allMembers, fmMembers, peerConnected)).To(BeTrue())
	})

	It("2 FM members, not connected → false", func() {
		gctx := buildConnCtx([]connSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true},
			{id: 1, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true},
		})
		allMembers := allMemberIDs(gctx)
		fmMembers := fullMeshMemberIDs(gctx)
		Expect(allConnectionsOfMemberVerified(gctx, gctx.replicas[0], 0, allMembers, fmMembers, peerConnected)).To(BeFalse())
	})

	It("with peerConnectedOnNetwork, correct network → true", func() {
		gctx := buildConnCtx([]connSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true,
				peers: []peerSetup{{peerID: 1, connected: true, establishedOn: []string{"net-A"}}}},
			{id: 1, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true,
				peers: []peerSetup{{peerID: 0, connected: true, establishedOn: []string{"net-A"}}}},
		})
		allMembers := allMemberIDs(gctx)
		fmMembers := fullMeshMemberIDs(gctx)
		Expect(allConnectionsOfMemberVerified(gctx, gctx.replicas[0], 0, allMembers, fmMembers, peerConnectedOnNetwork("net-A"))).To(BeTrue())
	})

	It("with peerConnectedOnNetwork, wrong network → false", func() {
		gctx := buildConnCtx([]connSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true,
				peers: []peerSetup{{peerID: 1, connected: true, establishedOn: []string{"net-A"}}}},
			{id: 1, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true,
				peers: []peerSetup{{peerID: 0, connected: true, establishedOn: []string{"net-A"}}}},
		})
		allMembers := allMemberIDs(gctx)
		fmMembers := fullMeshMemberIDs(gctx)
		Expect(allConnectionsOfMemberVerified(gctx, gctx.replicas[0], 0, allMembers, fmMembers, peerConnectedOnNetwork("net-B"))).To(BeFalse())
	})

	It("single member (no peers) → true", func() {
		gctx := buildConnCtx([]connSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true},
		})
		allMembers := allMemberIDs(gctx)
		fmMembers := fullMeshMemberIDs(gctx)
		Expect(allConnectionsOfMemberVerified(gctx, gctx.replicas[0], 0, allMembers, fmMembers, peerConnected)).To(BeTrue())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// allMembersHaveFullConnectivity
//

var _ = Describe("allMembersHaveFullConnectivity", func() {
	It("all connected → true", func() {
		gctx := buildConnCtx([]connSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true,
				peers: []peerSetup{{peerID: 1, connected: true}}},
			{id: 1, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true,
				peers: []peerSetup{{peerID: 0, connected: true}}},
		})
		allMembers := allMemberIDs(gctx)
		fmMembers := fullMeshMemberIDs(gctx)
		Expect(allMembersHaveFullConnectivity(gctx, 0, allMembers, fmMembers, peerConnected)).To(BeTrue())
	})

	It("one missing connection → false", func() {
		gctx := buildConnCtx([]connSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true,
				peers: []peerSetup{{peerID: 1, connected: true}}},
			{id: 1, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true},
			{id: 2, mType: v1alpha1.DatameshMemberTypeAccess, agentReady: true},
		})
		allMembers := allMemberIDs(gctx)
		fmMembers := fullMeshMemberIDs(gctx)
		Expect(allMembersHaveFullConnectivity(gctx, 0, allMembers, fmMembers, peerConnected)).To(BeFalse())
	})

	It("single member → true", func() {
		gctx := buildConnCtx([]connSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true},
		})
		allMembers := allMemberIDs(gctx)
		fmMembers := fullMeshMemberIDs(gctx)
		Expect(allMembersHaveFullConnectivity(gctx, 0, allMembers, fmMembers, peerConnected)).To(BeTrue())
	})

	It("with minRevision filtering", func() {
		gctx := buildConnCtx([]connSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true, rev: 10,
				peers: []peerSetup{{peerID: 1, connected: true}}},
			{id: 1, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true, rev: 5,
				peers: []peerSetup{{peerID: 0, connected: true}}},
		})
		allMembers := allMemberIDs(gctx)
		fmMembers := fullMeshMemberIDs(gctx)
		// minRevision=10: only id=0 has rev>=10, so 0→1 is verified (0 confirms).
		// 1→0: id=1 has rev=5 < 10, id=0 has rev=10 >=10 and sees 1 Connected → verified.
		Expect(allMembersHaveFullConnectivity(gctx, 10, allMembers, fmMembers, peerConnected)).To(BeTrue())
	})

	It("minRevision not met by either side → false", func() {
		gctx := buildConnCtx([]connSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true, rev: 5,
				peers: []peerSetup{{peerID: 1, connected: true}}},
			{id: 1, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true, rev: 5,
				peers: []peerSetup{{peerID: 0, connected: true}}},
		})
		allMembers := allMemberIDs(gctx)
		fmMembers := fullMeshMemberIDs(gctx)
		Expect(allMembersHaveFullConnectivity(gctx, 10, allMembers, fmMembers, peerConnected)).To(BeFalse())
	})

	It("with peerConnectedOnNetwork", func() {
		gctx := buildConnCtx([]connSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true,
				peers: []peerSetup{{peerID: 1, connected: true, establishedOn: []string{"net-A"}}}},
			{id: 1, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true,
				peers: []peerSetup{{peerID: 0, connected: true, establishedOn: []string{"net-A"}}}},
		})
		allMembers := allMemberIDs(gctx)
		fmMembers := fullMeshMemberIDs(gctx)
		Expect(allMembersHaveFullConnectivity(gctx, 0, allMembers, fmMembers, peerConnectedOnNetwork("net-A"))).To(BeTrue())
		Expect(allMembersHaveFullConnectivity(gctx, 0, allMembers, fmMembers, peerConnectedOnNetwork("net-B"))).To(BeFalse())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// guardRemainingNetworksConnected
//

var _ = Describe("guardRemainingNetworksConnected", func() {
	It("one remaining net fully connected → pass", func() {
		// Datamesh has [A, B], configuration has [B]. Intersection = [B].
		// B is fully connected.
		gctx := buildConnCtx([]connSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true,
				peers: []peerSetup{{peerID: 1, connected: true, establishedOn: []string{"net-B"}}}},
			{id: 1, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true,
				peers: []peerSetup{{peerID: 0, connected: true, establishedOn: []string{"net-B"}}}},
		})
		gctx.datamesh.systemNetworkNames = []string{"net-A", "net-B"}
		gctx.rsp = &testRSP{systemNetworkNames: []string{"net-B"}}

		r := guardRemainingNetworksConnected(gctx)
		Expect(r.Blocked).To(BeFalse())
	})

	It("no remaining net fully connected → blocked", func() {
		// Datamesh has [A, B], configuration has [B]. Intersection = [B].
		// B is NOT connected.
		gctx := buildConnCtx([]connSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true,
				peers: []peerSetup{{peerID: 1, connected: true, establishedOn: []string{"net-A"}}}},
			{id: 1, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true,
				peers: []peerSetup{{peerID: 0, connected: true, establishedOn: []string{"net-A"}}}},
		})
		gctx.datamesh.systemNetworkNames = []string{"net-A", "net-B"}
		gctx.rsp = &testRSP{systemNetworkNames: []string{"net-B"}}

		r := guardRemainingNetworksConnected(gctx)
		Expect(r.Blocked).To(BeTrue())
		Expect(r.Message).To(ContainSubstring("no remaining network with full connectivity"))
	})

	It("multiple remaining, one OK → pass", func() {
		// Datamesh has [A, B, C], configuration has [B, C]. Intersection = [B, C].
		// B not connected, C connected → pass.
		gctx := buildConnCtx([]connSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true,
				peers: []peerSetup{{peerID: 1, connected: true, establishedOn: []string{"net-C"}}}},
			{id: 1, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true,
				peers: []peerSetup{{peerID: 0, connected: true, establishedOn: []string{"net-C"}}}},
		})
		gctx.datamesh.systemNetworkNames = []string{"net-A", "net-B", "net-C"}
		gctx.rsp = &testRSP{systemNetworkNames: []string{"net-B", "net-C"}}

		r := guardRemainingNetworksConnected(gctx)
		Expect(r.Blocked).To(BeFalse())
	})

	It("empty intersection → blocked", func() {
		// Datamesh has [A], configuration has [B]. No intersection.
		gctx := buildConnCtx([]connSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true},
			{id: 1, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true},
		})
		gctx.datamesh.systemNetworkNames = []string{"net-A"}
		gctx.rsp = &testRSP{systemNetworkNames: []string{"net-B"}}

		r := guardRemainingNetworksConnected(gctx)
		Expect(r.Blocked).To(BeTrue())
		Expect(r.Message).To(ContainSubstring("intersection"))
	})

	It("RSP unavailable → blocked", func() {
		gctx := buildConnCtx([]connSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true},
		})
		gctx.datamesh.systemNetworkNames = []string{"net-A"}
		gctx.rsp = nil

		r := guardRemainingNetworksConnected(gctx)
		Expect(r.Blocked).To(BeTrue())
		Expect(r.Message).To(ContainSubstring("RSP unavailable"))
	})

	It("single member → pass (no peers)", func() {
		gctx := buildConnCtx([]connSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true},
		})
		gctx.datamesh.systemNetworkNames = []string{"net-A", "net-B"}
		gctx.rsp = &testRSP{systemNetworkNames: []string{"net-B"}}

		r := guardRemainingNetworksConnected(gctx)
		Expect(r.Blocked).To(BeFalse())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// guardMembersMatchTargetNetworks
//

var _ = Describe("guardReplicasMatchTargetNetworks", func() {
	It("all members match target → pass", func() {
		gctx := buildConnCtx([]connSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true},
			{id: 1, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true},
		})
		gctx.datamesh.systemNetworkNames = []string{"net-A", "net-B"}
		gctx.allReplicas[0].rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			{SystemNetworkName: "net-A"}, {SystemNetworkName: "net-B"},
		}
		gctx.allReplicas[1].rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			{SystemNetworkName: "net-A"}, {SystemNetworkName: "net-B"},
		}

		r := guardReplicasMatchTargetNetworks(gctx)
		Expect(r.Blocked).To(BeFalse())
	})

	It("member missing network → blocked", func() {
		gctx := buildConnCtx([]connSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true},
			{id: 1, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true},
		})
		gctx.datamesh.systemNetworkNames = []string{"net-A", "net-B"}
		gctx.allReplicas[0].rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			{SystemNetworkName: "net-A"}, {SystemNetworkName: "net-B"},
		}
		// Member 1 only has net-A.
		gctx.allReplicas[1].rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			{SystemNetworkName: "net-A"},
		}

		r := guardReplicasMatchTargetNetworks(gctx)
		Expect(r.Blocked).To(BeTrue())
		Expect(r.Message).To(ContainSubstring("missing"))
	})

	It("member has extra network → blocked", func() {
		gctx := buildConnCtx([]connSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true},
		})
		gctx.datamesh.systemNetworkNames = []string{"net-A"}
		gctx.allReplicas[0].rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			{SystemNetworkName: "net-A"}, {SystemNetworkName: "net-B"},
		}

		r := guardReplicasMatchTargetNetworks(gctx)
		Expect(r.Blocked).To(BeTrue())
	})

	It("no target networks → pass", func() {
		gctx := buildConnCtx([]connSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true},
		})
		gctx.datamesh.systemNetworkNames = nil

		r := guardReplicasMatchTargetNetworks(gctx)
		Expect(r.Blocked).To(BeFalse())
	})

	It("member without RVR → skipped", func() {
		gctx := buildConnCtx([]connSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true},
			{id: 1, mType: v1alpha1.DatameshMemberTypeDiskful, rvrNil: true},
		})
		gctx.datamesh.systemNetworkNames = []string{"net-A"}
		gctx.allReplicas[0].rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			{SystemNetworkName: "net-A"},
		}

		r := guardReplicasMatchTargetNetworks(gctx)
		Expect(r.Blocked).To(BeFalse())
	})

	It("member with empty addresses → skipped", func() {
		gctx := buildConnCtx([]connSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true},
			{id: 1, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true},
		})
		gctx.datamesh.systemNetworkNames = []string{"net-A"}
		gctx.allReplicas[0].rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			{SystemNetworkName: "net-A"},
		}
		// Member 1 has no addresses → skipped.
		gctx.allReplicas[1].rvr.Status.Addresses = nil

		r := guardReplicasMatchTargetNetworks(gctx)
		Expect(r.Blocked).To(BeFalse())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// addedNetworks
//

var _ = Describe("addedNetworks", func() {
	mkTransition := func(from, to []string) *v1alpha1.ReplicatedVolumeDatameshTransition {
		return &v1alpha1.ReplicatedVolumeDatameshTransition{
			FromSystemNetworkNames: from,
			ToSystemNetworkNames:   to,
		}
	}

	It("nil transition → nil", func() {
		gctx := &globalContext{}
		Expect(addedNetworks(gctx)).To(BeNil())
	})

	It("from=[A], to=[A,B] → [B]", func() {
		gctx := &globalContext{}
		gctx.changeSystemNetworksTransition = mkTransition([]string{"net-A"}, []string{"net-A", "net-B"})
		Expect(addedNetworks(gctx)).To(Equal([]string{"net-B"}))
	})

	It("from=[A,B], to=[A] → nil (no added)", func() {
		gctx := &globalContext{}
		gctx.changeSystemNetworksTransition = mkTransition([]string{"net-A", "net-B"}, []string{"net-A"})
		Expect(addedNetworks(gctx)).To(BeNil())
	})

	It("from=[A], to=[B] → [B] (full replacement)", func() {
		gctx := &globalContext{}
		gctx.changeSystemNetworksTransition = mkTransition([]string{"net-A"}, []string{"net-B"})
		Expect(addedNetworks(gctx)).To(Equal([]string{"net-B"}))
	})
})

var _ = Describe("removedNetworks", func() {
	mkTransition := func(from, to []string) *v1alpha1.ReplicatedVolumeDatameshTransition {
		return &v1alpha1.ReplicatedVolumeDatameshTransition{
			FromSystemNetworkNames: from,
			ToSystemNetworkNames:   to,
		}
	}

	It("nil transition → nil", func() {
		gctx := &globalContext{}
		Expect(removedNetworks(gctx)).To(BeNil())
	})

	It("from=[A,B], to=[A] → [B]", func() {
		gctx := &globalContext{}
		gctx.changeSystemNetworksTransition = mkTransition([]string{"net-A", "net-B"}, []string{"net-A"})
		Expect(removedNetworks(gctx)).To(Equal([]string{"net-B"}))
	})

	It("from=[A], to=[A,B] → nil (no removed)", func() {
		gctx := &globalContext{}
		gctx.changeSystemNetworksTransition = mkTransition([]string{"net-A"}, []string{"net-A", "net-B"})
		Expect(removedNetworks(gctx)).To(BeNil())
	})

	It("from=[A,B], to=[C,D] → [A,B] (all removed)", func() {
		gctx := &globalContext{}
		gctx.changeSystemNetworksTransition = mkTransition([]string{"net-A", "net-B"}, []string{"net-C", "net-D"})
		Expect(removedNetworks(gctx)).To(Equal([]string{"net-A", "net-B"}))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// confirmAddedAddressesAvailable
//

var _ = Describe("confirmAddedAddressesAvailable", func() {
	mkTransition := func(from, to []string) *v1alpha1.ReplicatedVolumeDatameshTransition {
		return &v1alpha1.ReplicatedVolumeDatameshTransition{
			FromSystemNetworkNames: from,
			ToSystemNetworkNames:   to,
		}
	}

	It("all confirmed: both RVRs have added address + rev", func() {
		gctx := buildConnCtx([]connSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true, rev: 10},
			{id: 1, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true, rev: 10},
		})
		gctx.changeSystemNetworksTransition = mkTransition([]string{"net-A"}, []string{"net-A", "net-B"})
		gctx.allReplicas[0].rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			{SystemNetworkName: "net-B"},
		}
		gctx.allReplicas[1].rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			{SystemNetworkName: "net-B"},
		}

		res := confirmAddedAddressesAvailable(gctx, 10)
		Expect(res.Confirmed).To(Equal(idset.Of(0, 1)))
	})

	It("one missing address → only one confirmed", func() {
		gctx := buildConnCtx([]connSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true, rev: 10},
			{id: 1, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true, rev: 10},
		})
		gctx.changeSystemNetworksTransition = mkTransition([]string{"net-A"}, []string{"net-A", "net-B"})
		gctx.allReplicas[0].rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			{SystemNetworkName: "net-B"},
		}
		// RVR 1 has no net-B address.
		gctx.allReplicas[1].rvr.Status.Addresses = nil

		res := confirmAddedAddressesAvailable(gctx, 10)
		Expect(res.Confirmed).To(Equal(idset.Of(0)))
	})

	It("rev not confirmed → empty", func() {
		gctx := buildConnCtx([]connSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true, rev: 5},
			{id: 1, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true, rev: 5},
		})
		gctx.changeSystemNetworksTransition = mkTransition([]string{"net-A"}, []string{"net-A", "net-B"})
		gctx.allReplicas[0].rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			{SystemNetworkName: "net-B"},
		}
		gctx.allReplicas[1].rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			{SystemNetworkName: "net-B"},
		}

		res := confirmAddedAddressesAvailable(gctx, 10)
		Expect(res.Confirmed.IsEmpty()).To(BeTrue())
	})

	It("no transition → pure revision confirm", func() {
		gctx := buildConnCtx([]connSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true, rev: 10},
			{id: 1, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true, rev: 10},
		})
		// No transition set.

		res := confirmAddedAddressesAvailable(gctx, 10)
		Expect(res.Confirmed).To(Equal(idset.Of(0, 1)))
	})

	It("multiple added: one RVR has both, other only one", func() {
		gctx := buildConnCtx([]connSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true, rev: 10},
			{id: 1, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true, rev: 10},
		})
		gctx.changeSystemNetworksTransition = mkTransition([]string{"net-A"}, []string{"net-A", "net-B", "net-C"})
		gctx.allReplicas[0].rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			{SystemNetworkName: "net-B"}, {SystemNetworkName: "net-C"},
		}
		// RVR 1 has only net-B, missing net-C.
		gctx.allReplicas[1].rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			{SystemNetworkName: "net-B"},
		}

		res := confirmAddedAddressesAvailable(gctx, 10)
		Expect(res.Confirmed).To(Equal(idset.Of(0)))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// confirmAllConnectedOnAddedNetworks
//

var _ = Describe("confirmAllConnectedOnAddedNetworks", func() {
	mkTransition := func(from, to []string) *v1alpha1.ReplicatedVolumeDatameshTransition {
		return &v1alpha1.ReplicatedVolumeDatameshTransition{
			FromSystemNetworkNames: from,
			ToSystemNetworkNames:   to,
		}
	}

	It("all connected on added net → all confirmed", func() {
		gctx := buildConnCtx([]connSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true, rev: 10,
				peers: []peerSetup{{peerID: 1, connected: true, establishedOn: []string{"net-B"}}}},
			{id: 1, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true, rev: 10,
				peers: []peerSetup{{peerID: 0, connected: true, establishedOn: []string{"net-B"}}}},
		})
		gctx.changeSystemNetworksTransition = mkTransition([]string{"net-A"}, []string{"net-A", "net-B"})

		res := confirmAllConnectedOnAddedNetworks(gctx, 10)
		Expect(res.Confirmed).To(Equal(idset.Of(0, 1)))
	})

	It("not connected on added net → empty", func() {
		gctx := buildConnCtx([]connSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true, rev: 10,
				peers: []peerSetup{{peerID: 1, connected: true, establishedOn: []string{"net-A"}}}},
			{id: 1, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true, rev: 10,
				peers: []peerSetup{{peerID: 0, connected: true, establishedOn: []string{"net-A"}}}},
		})
		gctx.changeSystemNetworksTransition = mkTransition([]string{"net-A"}, []string{"net-A", "net-B"})

		res := confirmAllConnectedOnAddedNetworks(gctx, 10)
		Expect(res.Confirmed.IsEmpty()).To(BeTrue())
	})

	It("rev not confirmed → empty", func() {
		gctx := buildConnCtx([]connSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true, rev: 5,
				peers: []peerSetup{{peerID: 1, connected: true, establishedOn: []string{"net-B"}}}},
			{id: 1, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true, rev: 5,
				peers: []peerSetup{{peerID: 0, connected: true, establishedOn: []string{"net-B"}}}},
		})
		gctx.changeSystemNetworksTransition = mkTransition([]string{"net-A"}, []string{"net-A", "net-B"})

		res := confirmAllConnectedOnAddedNetworks(gctx, 10)
		Expect(res.Confirmed.IsEmpty()).To(BeTrue())
	})

	It("no transition → pure revision confirm", func() {
		gctx := buildConnCtx([]connSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true, rev: 10},
			{id: 1, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true, rev: 10},
		})
		// No transition set.

		res := confirmAllConnectedOnAddedNetworks(gctx, 10)
		Expect(res.Confirmed).To(Equal(idset.Of(0, 1)))
	})

	It("partial: one side sees added net connected → both confirmed", func() {
		gctx := buildConnCtx([]connSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true, rev: 10,
				peers: []peerSetup{{peerID: 1, connected: true, establishedOn: []string{"net-B"}}}},
			{id: 1, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: false, rev: 10},
		})
		gctx.changeSystemNetworksTransition = mkTransition([]string{"net-A"}, []string{"net-A", "net-B"})

		res := confirmAllConnectedOnAddedNetworks(gctx, 10)
		// id=0 ready, sees 1 on net-B → 0↔1 verified on net-B. Both confirmed.
		Expect(res.Confirmed).To(Equal(idset.Of(0, 1)))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Per-member mutation helpers
//

var _ = Describe("repairMemberAddresses", func() {
	mkAddr := func(net, ip string, port uint) v1alpha1.DRBDResourceAddressStatus {
		return v1alpha1.DRBDResourceAddressStatus{
			SystemNetworkName: net,
			Address:           v1alpha1.DRBDAddress{IPv4: ip, Port: port},
		}
	}

	It("updates changed IP", func() {
		member := &v1alpha1.DatameshMember{
			Addresses: []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)},
		}
		rvrAddrs := []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.2", 7000)}
		targetNets := []string{"net-A"}

		changed := repairMemberAddresses(member, rvrAddrs, targetNets)
		Expect(changed).To(BeTrue())
		Expect(member.Addresses[0].Address.IPv4).To(Equal("10.0.0.2"))
	})

	It("no change when same", func() {
		member := &v1alpha1.DatameshMember{
			Addresses: []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)},
		}
		rvrAddrs := []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)}
		targetNets := []string{"net-A"}

		Expect(repairMemberAddresses(member, rvrAddrs, targetNets)).To(BeFalse())
	})

	It("updates port", func() {
		member := &v1alpha1.DatameshMember{
			Addresses: []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)},
		}
		rvrAddrs := []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 8000)}
		targetNets := []string{"net-A"}

		Expect(repairMemberAddresses(member, rvrAddrs, targetNets)).To(BeTrue())
		Expect(member.Addresses[0].Address.Port).To(Equal(uint(8000)))
	})

	It("adds missing network from RVR", func() {
		member := &v1alpha1.DatameshMember{
			Addresses: []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)},
		}
		rvrAddrs := []v1alpha1.DRBDResourceAddressStatus{
			mkAddr("net-A", "10.0.0.1", 7000),
			mkAddr("net-B", "10.0.0.2", 7000),
		}
		targetNets := []string{"net-A", "net-B"}

		Expect(repairMemberAddresses(member, rvrAddrs, targetNets)).To(BeTrue())
		Expect(member.Addresses).To(HaveLen(2))
		Expect(member.Addresses[1].SystemNetworkName).To(Equal("net-B"))
	})

	It("removes stale network not in targetNets", func() {
		member := &v1alpha1.DatameshMember{
			Addresses: []v1alpha1.DRBDResourceAddressStatus{
				mkAddr("net-A", "10.0.0.1", 7000),
				mkAddr("net-B", "10.0.0.2", 7000),
			},
		}
		rvrAddrs := []v1alpha1.DRBDResourceAddressStatus{mkAddr("net-A", "10.0.0.1", 7000)}
		targetNets := []string{"net-A"}

		Expect(repairMemberAddresses(member, rvrAddrs, targetNets)).To(BeTrue())
		Expect(member.Addresses).To(HaveLen(1))
		Expect(member.Addresses[0].SystemNetworkName).To(Equal("net-A"))
	})

	It("skips network not in RVR", func() {
		member := &v1alpha1.DatameshMember{}
		rvrAddrs := []v1alpha1.DRBDResourceAddressStatus{}
		targetNets := []string{"net-A"}

		// net-A is in target but not in RVR → skipped, member stays empty.
		Expect(repairMemberAddresses(member, rvrAddrs, targetNets)).To(BeFalse())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Apply callbacks
//

var _ = Describe("repairAddresses", func() {
	It("repairs address on shared network", func() {
		gctx := buildConnCtx([]connSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true, addresses: []string{"net-A"}},
		})
		gctx.datamesh.systemNetworkNames = []string{"net-A"}
		gctx.allReplicas[0].rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			{SystemNetworkName: "net-A", Address: v1alpha1.DRBDAddress{IPv4: "10.0.0.99", Port: 9000}},
		}

		changed := repairAddresses(gctx)
		Expect(changed).To(BeTrue())
		Expect(gctx.allReplicas[0].member.Addresses[0].Address.IPv4).To(Equal("10.0.0.99"))
	})

	It("no change when addresses match", func() {
		gctx := buildConnCtx([]connSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true, addresses: []string{"net-A"}},
		})
		gctx.datamesh.systemNetworkNames = []string{"net-A"}
		gctx.allReplicas[0].rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			{SystemNetworkName: "net-A", Address: v1alpha1.DRBDAddress{IPv4: "10.0.0.1", Port: 7000}},
		}

		Expect(repairAddresses(gctx)).To(BeFalse())
	})

	It("skips members without rvr", func() {
		gctx := buildConnCtx([]connSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, rvrNil: true, addresses: []string{"net-A"}},
		})
		gctx.datamesh.systemNetworkNames = []string{"net-A"}
		Expect(repairAddresses(gctx)).To(BeFalse())
	})

	It("adds missing network", func() {
		gctx := buildConnCtx([]connSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true, addresses: []string{"net-A"}},
		})
		gctx.datamesh.systemNetworkNames = []string{"net-A", "net-B"}
		gctx.allReplicas[0].rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			{SystemNetworkName: "net-A", Address: v1alpha1.DRBDAddress{IPv4: "10.0.0.1", Port: 7000}},
			{SystemNetworkName: "net-B", Address: v1alpha1.DRBDAddress{IPv4: "10.0.0.2", Port: 7000}},
		}

		Expect(repairAddresses(gctx)).To(BeTrue())
		Expect(gctx.allReplicas[0].member.Addresses).To(HaveLen(2))
	})

	It("removes stale network", func() {
		gctx := buildConnCtx([]connSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, agentReady: true, addresses: []string{"net-A", "net-B"}},
		})
		gctx.datamesh.systemNetworkNames = []string{"net-A"}
		gctx.allReplicas[0].rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			{SystemNetworkName: "net-A", Address: v1alpha1.DRBDAddress{IPv4: "10.0.0.1", Port: 7000}},
		}

		Expect(repairAddresses(gctx)).To(BeTrue())
		Expect(gctx.allReplicas[0].member.Addresses).To(HaveLen(1))
		Expect(gctx.allReplicas[0].member.Addresses[0].SystemNetworkName).To(Equal("net-A"))
	})
})

var _ = Describe("addNewAddresses", func() {
	mkGctx := func() *globalContext {
		gctx := &globalContext{}
		gctx.changeSystemNetworksTransition = &v1alpha1.ReplicatedVolumeDatameshTransition{
			FromSystemNetworkNames: []string{"net-A"},
			ToSystemNetworkNames:   []string{"net-A", "net-B"},
		}
		gctx.allReplicas = []ReplicaContext{{
			id: 0,
			member: &v1alpha1.DatameshMember{
				Name:      "rv-1-0",
				Addresses: []v1alpha1.DRBDResourceAddressStatus{{SystemNetworkName: "net-A", Address: v1alpha1.DRBDAddress{IPv4: "10.0.0.1", Port: 7000}}},
			},
			rvr: &v1alpha1.ReplicatedVolumeReplica{Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				Addresses: []v1alpha1.DRBDResourceAddressStatus{
					{SystemNetworkName: "net-A", Address: v1alpha1.DRBDAddress{IPv4: "10.0.0.1", Port: 7000}},
					{SystemNetworkName: "net-B", Address: v1alpha1.DRBDAddress{IPv4: "10.0.0.2", Port: 7000}},
				},
			}},
		}}
		gctx.allReplicas[0].gctx = gctx
		gctx.replicas[0] = &gctx.allReplicas[0]
		return gctx
	}

	It("already present with same IP → no change", func() {
		gctx := mkGctx()
		// Member already has net-B with the same address as RVR.
		gctx.allReplicas[0].member.Addresses = append(gctx.allReplicas[0].member.Addresses,
			v1alpha1.DRBDResourceAddressStatus{SystemNetworkName: "net-B", Address: v1alpha1.DRBDAddress{IPv4: "10.0.0.2", Port: 7000}})

		Expect(addNewAddresses(gctx)).To(BeFalse())
	})

	It("already present with different IP → updated", func() {
		gctx := mkGctx()
		// Member has net-B with a stale IP.
		gctx.allReplicas[0].member.Addresses = append(gctx.allReplicas[0].member.Addresses,
			v1alpha1.DRBDResourceAddressStatus{SystemNetworkName: "net-B", Address: v1alpha1.DRBDAddress{IPv4: "10.0.0.99", Port: 7000}})

		Expect(addNewAddresses(gctx)).To(BeTrue())
		ma := findAddressByNetwork(gctx.allReplicas[0].member.Addresses, "net-B")
		Expect(ma).NotTo(BeNil())
		Expect(ma.Address.IPv4).To(Equal("10.0.0.2"))
	})

	It("RVR has no address for added network → skipped", func() {
		gctx := mkGctx()
		// RVR only has net-A, not net-B.
		gctx.allReplicas[0].rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{
			{SystemNetworkName: "net-A", Address: v1alpha1.DRBDAddress{IPv4: "10.0.0.1", Port: 7000}},
		}

		Expect(addNewAddresses(gctx)).To(BeFalse())
		Expect(gctx.allReplicas[0].member.Addresses).To(HaveLen(1))
	})
})

var _ = Describe("setSystemNetworks", func() {
	It("panics when RSP is nil", func() {
		gctx := &globalContext{} // rsp = nil
		t := &dmte.Transition{}
		Expect(func() { setSystemNetworks(gctx, t) }).To(Panic())
	})
})
