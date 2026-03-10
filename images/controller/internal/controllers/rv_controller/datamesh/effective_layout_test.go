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
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// ──────────────────────────────────────────────────────────────────────────────
// Test helpers
//

// peerEntry describes a peer observation for test setup.
type peerEntry struct {
	name      string // peer name, e.g. "rv-1-2"
	connected bool   // ConnectionState == Connected
	upToDate  bool   // BackingVolumeState == UpToDate
}

// replicaEntry describes one replica for test setup.
type replicaEntry struct {
	id            uint8
	memberType    v1alpha1.DatameshMemberType
	quorum        *bool              // nil = Quorum not reported
	bvState       v1alpha1.DiskState // "" = no BackingVolume
	agentNotReady bool               // set DRBDConfigured=AgentNotReady condition
	peers         []peerEntry        // peer observations from this agent
	noRVR         bool               // true = member without RVR
	noMember      bool               // true = RVR without member
}

// mkEffGctx builds a globalContext for updateEffectiveLayout tests.
func mkEffGctx(q, qmr byte, entries ...replicaEntry) *globalContext {
	gctx := &globalContext{
		datamesh: datameshContext{
			quorum:                  q,
			quorumMinimumRedundancy: qmr,
		},
	}

	gctx.allReplicas = make([]ReplicaContext, len(entries))
	for i, e := range entries {
		rc := &gctx.allReplicas[i]
		rc.gctx = gctx
		rc.id = e.id
		rc.name = fmt.Sprintf("rv-1-%d", e.id)
		rc.nodeName = fmt.Sprintf("node-%d", e.id)

		if !e.noMember {
			rc.member = &v1alpha1.DatameshMember{
				Name:     rc.name,
				Type:     e.memberType,
				NodeName: rc.nodeName,
			}
		}

		if !e.noRVR {
			rvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: rc.name},
				Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: rc.nodeName},
			}

			if e.agentNotReady {
				rvr.Status.Conditions = []metav1.Condition{{
					Type:   v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType,
					Status: metav1.ConditionFalse,
					Reason: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonAgentNotReady,
				}}
			}

			if e.quorum != nil {
				rvr.Status.Quorum = ptr.To(*e.quorum)
			}

			if e.bvState != "" {
				rvr.Status.BackingVolume = &v1alpha1.ReplicatedVolumeReplicaStatusBackingVolume{
					State: e.bvState,
				}
			}

			for _, p := range e.peers {
				peer := v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{
					Name: p.name,
				}
				if p.connected {
					peer.ConnectionState = v1alpha1.ConnectionStateConnected
				}
				if p.upToDate {
					peer.BackingVolumeState = v1alpha1.DiskStateUpToDate
				}
				rvr.Status.Peers = append(rvr.Status.Peers, peer)
			}

			rc.rvr = rvr
		}

		gctx.replicas[e.id] = rc
	}

	return gctx
}

// readyD builds a replicaEntry for a ready Diskful voter with Quorum=true and UpToDate disk.
func readyD(id uint8, peers ...peerEntry) replicaEntry {
	return replicaEntry{
		id:         id,
		memberType: v1alpha1.DatameshMemberTypeDiskful,
		quorum:     ptr.To(true),
		bvState:    v1alpha1.DiskStateUpToDate,
		peers:      peers,
	}
}

// readyTB builds a replicaEntry for a ready TieBreaker with Quorum=true.
func readyTB(id uint8, peers ...peerEntry) replicaEntry {
	return replicaEntry{
		id:         id,
		memberType: v1alpha1.DatameshMemberTypeTieBreaker,
		quorum:     ptr.To(true),
		peers:      peers,
	}
}

// staleD builds a replicaEntry for a stale (AgentNotReady) Diskful voter.
func staleD(id uint8, peers ...peerEntry) replicaEntry {
	return replicaEntry{
		id:            id,
		memberType:    v1alpha1.DatameshMemberTypeDiskful,
		agentNotReady: true,
		peers:         peers,
	}
}

// staleTB builds a replicaEntry for a stale (AgentNotReady) TieBreaker.
func staleTB(id uint8) replicaEntry {
	return replicaEntry{
		id:            id,
		memberType:    v1alpha1.DatameshMemberTypeTieBreaker,
		agentNotReady: true,
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// updateEffectiveLayout — change detection
//

var _ = Describe("updateEffectiveLayout change detection", func() {
	It("first call on zero value → changed", func() {
		gctx := mkEffGctx(1, 1, readyD(0))
		var el v1alpha1.ReplicatedVolumeEffectiveLayout

		Expect(updateEffectiveLayout(gctx, &el)).To(BeTrue())
		Expect(el.TotalVoters).To(Equal(int8(1)))
	})

	It("second call with same state → not changed", func() {
		gctx := mkEffGctx(1, 1, readyD(0))
		var el v1alpha1.ReplicatedVolumeEffectiveLayout

		updateEffectiveLayout(gctx, &el)
		Expect(updateEffectiveLayout(gctx, &el)).To(BeFalse())
	})

	It("count change detected", func() {
		gctx := mkEffGctx(2, 2, readyD(0), readyD(1), readyD(2))
		var el v1alpha1.ReplicatedVolumeEffectiveLayout
		updateEffectiveLayout(gctx, &el)

		// Remove a voter from the context.
		gctx2 := mkEffGctx(2, 2, readyD(0), readyD(1))
		Expect(updateEffectiveLayout(gctx2, &el)).To(BeTrue())
		Expect(el.TotalVoters).To(Equal(int8(2)))
	})

	It("FTT nil vs non-nil detected", func() {
		// Start with voters → FTT computed.
		gctx := mkEffGctx(1, 1, readyD(0))
		var el v1alpha1.ReplicatedVolumeEffectiveLayout
		updateEffectiveLayout(gctx, &el)
		Expect(el.FailuresToTolerate).NotTo(BeNil())

		// Switch to no voters → FTT nil.
		gctx2 := mkEffGctx(1, 1)
		Expect(updateEffectiveLayout(gctx2, &el)).To(BeTrue())
		Expect(el.FailuresToTolerate).To(BeNil())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// updateEffectiveLayout — FTT/GMDR unavailable
//

var _ = Describe("updateEffectiveLayout unavailable", func() {
	It("no replicas", func() {
		gctx := mkEffGctx(1, 1)
		var el v1alpha1.ReplicatedVolumeEffectiveLayout
		updateEffectiveLayout(gctx, &el)

		Expect(el.FailuresToTolerate).To(BeNil())
		Expect(el.GuaranteedMinimumDataRedundancy).To(BeNil())
		Expect(el.TotalVoters).To(Equal(int8(0)))
		Expect(el.Message).To(ContainSubstring("No voters"))
		Expect(el.Message).To(ContainSubstring("no voter members"))
	})

	It("no members (only rvr, no member ptr)", func() {
		gctx := mkEffGctx(1, 1, replicaEntry{
			id:       0,
			quorum:   ptr.To(true),
			noMember: true,
		})
		var el v1alpha1.ReplicatedVolumeEffectiveLayout
		updateEffectiveLayout(gctx, &el)

		Expect(el.FailuresToTolerate).To(BeNil())
		Expect(el.TotalVoters).To(Equal(int8(0)))
	})

	It("only non-quorum types (Access, sD)", func() {
		gctx := mkEffGctx(1, 1,
			replicaEntry{
				id:         0,
				memberType: v1alpha1.DatameshMemberTypeAccess,
				quorum:     ptr.To(true),
			},
			replicaEntry{
				id:         1,
				memberType: v1alpha1.DatameshMemberTypeShadowDiskful,
				quorum:     ptr.To(true),
				bvState:    v1alpha1.DiskStateUpToDate,
			},
		)
		var el v1alpha1.ReplicatedVolumeEffectiveLayout
		updateEffectiveLayout(gctx, &el)

		Expect(el.FailuresToTolerate).To(BeNil())
		Expect(el.TotalVoters).To(Equal(int8(0)))
	})

	It("voters exist but all stale (AgentNotReady)", func() {
		gctx := mkEffGctx(2, 1,
			staleD(0),
			staleD(1),
		)
		var el v1alpha1.ReplicatedVolumeEffectiveLayout
		updateEffectiveLayout(gctx, &el)

		Expect(el.FailuresToTolerate).To(BeNil())
		Expect(el.TotalVoters).To(Equal(int8(2)))
		Expect(el.StaleAgents).To(Equal(int8(2)))
		Expect(el.Message).To(ContainSubstring("no fresh agent data"))
		Expect(el.Message).To(ContainSubstring("#0, #1"))
	})

	It("voters exist but Quorum==nil for all", func() {
		gctx := mkEffGctx(2, 1,
			replicaEntry{
				id:         0,
				memberType: v1alpha1.DatameshMemberTypeDiskful,
			},
			replicaEntry{
				id:         1,
				memberType: v1alpha1.DatameshMemberTypeDiskful,
			},
		)
		var el v1alpha1.ReplicatedVolumeEffectiveLayout
		updateEffectiveLayout(gctx, &el)

		Expect(el.FailuresToTolerate).To(BeNil())
		Expect(el.TotalVoters).To(Equal(int8(2)))
		Expect(el.StaleAgents).To(Equal(int8(2)))
		Expect(el.Message).To(ContainSubstring("no fresh agent data"))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// updateEffectiveLayout — direct classification (ready agents)
//

var _ = Describe("updateEffectiveLayout direct classification", func() {
	It("1D ready, Quorum=true, UpToDate", func() {
		gctx := mkEffGctx(1, 1, readyD(0))
		var el v1alpha1.ReplicatedVolumeEffectiveLayout
		updateEffectiveLayout(gctx, &el)

		Expect(el.FailuresToTolerate).To(Equal(ptr.To(int8(0))))
		Expect(el.GuaranteedMinimumDataRedundancy).To(Equal(ptr.To(int8(0))))
		Expect(el.TotalVoters).To(Equal(int8(1)))
		Expect(el.ReachableVoters).To(Equal(int8(1)))
		Expect(el.UpToDateVoters).To(Equal(int8(1)))
		Expect(el.StaleAgents).To(Equal(int8(0)))
		Expect(el.Message).To(ContainSubstring("1/1 voters reachable"))
		Expect(el.Message).To(ContainSubstring("FTT=0, GMDR=0"))
	})

	It("1D ready, Quorum=true, NOT UpToDate", func() {
		gctx := mkEffGctx(1, 1, replicaEntry{
			id:         0,
			memberType: v1alpha1.DatameshMemberTypeDiskful,
			quorum:     ptr.To(true),
		})
		var el v1alpha1.ReplicatedVolumeEffectiveLayout
		updateEffectiveLayout(gctx, &el)

		Expect(el.FailuresToTolerate).To(Equal(ptr.To(int8(-1))))
		Expect(el.GuaranteedMinimumDataRedundancy).To(Equal(ptr.To(int8(-1))))
		Expect(el.UpToDateVoters).To(Equal(int8(0)))
	})

	It("1D ready, Quorum=false, UpToDate", func() {
		gctx := mkEffGctx(1, 1, replicaEntry{
			id:         0,
			memberType: v1alpha1.DatameshMemberTypeDiskful,
			quorum:     ptr.To(false),
			bvState:    v1alpha1.DiskStateUpToDate,
		})
		var el v1alpha1.ReplicatedVolumeEffectiveLayout
		updateEffectiveLayout(gctx, &el)

		Expect(el.FailuresToTolerate).To(Equal(ptr.To(int8(-1))))
		Expect(el.GuaranteedMinimumDataRedundancy).To(Equal(ptr.To(int8(0))))
		Expect(el.ReachableVoters).To(Equal(int8(0)))
	})

	It("3D all ready, all Quorum=true, all UpToDate", func() {
		gctx := mkEffGctx(2, 2, readyD(0), readyD(1), readyD(2))
		var el v1alpha1.ReplicatedVolumeEffectiveLayout
		updateEffectiveLayout(gctx, &el)

		Expect(el.FailuresToTolerate).To(Equal(ptr.To(int8(1))))
		Expect(el.GuaranteedMinimumDataRedundancy).To(Equal(ptr.To(int8(1))))
		Expect(el.TotalVoters).To(Equal(int8(3)))
		Expect(el.ReachableVoters).To(Equal(int8(3)))
		Expect(el.UpToDateVoters).To(Equal(int8(3)))
		Expect(el.Message).To(ContainSubstring("3/3 voters reachable"))
		Expect(el.Message).To(ContainSubstring("FTT=1, GMDR=1"))
	})

	It("3D all ready, one Quorum=false", func() {
		gctx := mkEffGctx(2, 2,
			readyD(0), readyD(1),
			replicaEntry{
				id: 2, memberType: v1alpha1.DatameshMemberTypeDiskful,
				quorum: ptr.To(false), bvState: v1alpha1.DiskStateUpToDate,
			},
		)
		var el v1alpha1.ReplicatedVolumeEffectiveLayout
		updateEffectiveLayout(gctx, &el)

		Expect(el.FailuresToTolerate).To(Equal(ptr.To(int8(0))))
		Expect(el.GuaranteedMinimumDataRedundancy).To(Equal(ptr.To(int8(1))))
		Expect(el.ReachableVoters).To(Equal(int8(2)))
	})

	It("3D all ready, one NOT UpToDate", func() {
		gctx := mkEffGctx(2, 2,
			readyD(0), readyD(1),
			replicaEntry{
				id: 2, memberType: v1alpha1.DatameshMemberTypeDiskful,
				quorum: ptr.To(true),
			},
		)
		var el v1alpha1.ReplicatedVolumeEffectiveLayout
		updateEffectiveLayout(gctx, &el)

		Expect(el.FailuresToTolerate).To(Equal(ptr.To(int8(0))))
		Expect(el.GuaranteedMinimumDataRedundancy).To(Equal(ptr.To(int8(1))))
		Expect(el.UpToDateVoters).To(Equal(int8(2)))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// updateEffectiveLayout — peer reconstruction (stale agents)
//

var _ = Describe("updateEffectiveLayout peer reconstruction", func() {
	It("3D, one stale, peers see it Connected+UpToDate", func() {
		gctx := mkEffGctx(2, 2,
			readyD(0, peerEntry{"rv-1-2", true, true}),
			readyD(1, peerEntry{"rv-1-2", true, true}),
			staleD(2),
		)
		var el v1alpha1.ReplicatedVolumeEffectiveLayout
		updateEffectiveLayout(gctx, &el)

		Expect(el.FailuresToTolerate).To(Equal(ptr.To(int8(1))))
		Expect(el.GuaranteedMinimumDataRedundancy).To(Equal(ptr.To(int8(1))))
		Expect(el.ReachableVoters).To(Equal(int8(3)))
		Expect(el.StaleAgents).To(Equal(int8(1)))
		Expect(el.Message).To(ContainSubstring("1 stale [#2]"))
	})

	It("3D, one stale, peers see it Connected but NOT UpToDate", func() {
		gctx := mkEffGctx(2, 2,
			readyD(0, peerEntry{"rv-1-2", true, false}),
			readyD(1, peerEntry{"rv-1-2", true, false}),
			staleD(2),
		)
		var el v1alpha1.ReplicatedVolumeEffectiveLayout
		updateEffectiveLayout(gctx, &el)

		Expect(el.FailuresToTolerate).To(Equal(ptr.To(int8(0))))
		Expect(el.GuaranteedMinimumDataRedundancy).To(Equal(ptr.To(int8(1))))
		Expect(el.ReachableVoters).To(Equal(int8(3)))
		Expect(el.UpToDateVoters).To(Equal(int8(2)))
	})

	It("3D, one stale, peers do NOT see it", func() {
		gctx := mkEffGctx(2, 2, readyD(0), readyD(1), staleD(2))
		var el v1alpha1.ReplicatedVolumeEffectiveLayout
		updateEffectiveLayout(gctx, &el)

		Expect(el.FailuresToTolerate).To(Equal(ptr.To(int8(0))))
		Expect(el.GuaranteedMinimumDataRedundancy).To(Equal(ptr.To(int8(1))))
		Expect(el.ReachableVoters).To(Equal(int8(2)))
	})

	It("stale agent's peer data is IGNORED", func() {
		gctx := mkEffGctx(2, 2,
			readyD(0), readyD(1),
			staleD(2, peerEntry{"rv-1-0", true, true}),
		)
		var el v1alpha1.ReplicatedVolumeEffectiveLayout
		updateEffectiveLayout(gctx, &el)

		Expect(el.ReachableVoters).To(Equal(int8(2)))
	})

	It("2D+1TB, TB is stale, peers see TB Connected", func() {
		gctx := mkEffGctx(2, 1,
			readyD(0, peerEntry{"rv-1-2", true, false}),
			readyD(1, peerEntry{"rv-1-2", true, false}),
			staleTB(2),
		)
		var el v1alpha1.ReplicatedVolumeEffectiveLayout
		updateEffectiveLayout(gctx, &el)

		Expect(el.FailuresToTolerate).To(Equal(ptr.To(int8(1))))
		Expect(el.ReachableTieBreakers).To(Equal(int8(1)))
		Expect(el.StaleAgents).To(Equal(int8(1)))
		Expect(el.Message).To(ContainSubstring("1/1 TB reachable"))
		Expect(el.Message).To(ContainSubstring("stale [#2]"))
	})

	It("2D+1TB, TB is stale, peers do NOT see TB", func() {
		gctx := mkEffGctx(2, 1, readyD(0), readyD(1), staleTB(2))
		var el v1alpha1.ReplicatedVolumeEffectiveLayout
		updateEffectiveLayout(gctx, &el)

		Expect(el.FailuresToTolerate).To(Equal(ptr.To(int8(0))))
		Expect(el.ReachableTieBreakers).To(Equal(int8(0)))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// updateEffectiveLayout — TB bonus logic
//

var _ = Describe("updateEffectiveLayout TB bonus", func() {
	It("2D ready + 1TB ready: even voters + TB → tbBonus=1", func() {
		gctx := mkEffGctx(2, 1, readyD(0), readyD(1), readyTB(2))
		var el v1alpha1.ReplicatedVolumeEffectiveLayout
		updateEffectiveLayout(gctx, &el)

		Expect(el.FailuresToTolerate).To(Equal(ptr.To(int8(1))))
	})

	It("3D ready + 1TB ready: odd voters → tbBonus=0", func() {
		gctx := mkEffGctx(2, 2, readyD(0), readyD(1), readyD(2), readyTB(3))
		var el v1alpha1.ReplicatedVolumeEffectiveLayout
		updateEffectiveLayout(gctx, &el)

		Expect(el.FailuresToTolerate).To(Equal(ptr.To(int8(1))))
	})

	It("2D ready + 0TB: even voters but no TB → tbBonus=0", func() {
		gctx := mkEffGctx(2, 1, readyD(0), readyD(1))
		var el v1alpha1.ReplicatedVolumeEffectiveLayout
		updateEffectiveLayout(gctx, &el)

		Expect(el.FailuresToTolerate).To(Equal(ptr.To(int8(0))))
	})

	It("4D ready + 1TB stale, peer sees TB: even voters + reconstructed TB → tbBonus=1", func() {
		gctx := mkEffGctx(3, 2,
			readyD(0, peerEntry{"rv-1-4", true, false}),
			readyD(1), readyD(2), readyD(3),
			staleTB(4),
		)
		var el v1alpha1.ReplicatedVolumeEffectiveLayout
		updateEffectiveLayout(gctx, &el)

		Expect(el.FailuresToTolerate).To(Equal(ptr.To(int8(2))))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// updateEffectiveLayout — formula edge cases
//

var _ = Describe("updateEffectiveLayout formula edge cases", func() {
	It("FTT negative (quorum lost)", func() {
		gctx := mkEffGctx(2, 1,
			replicaEntry{
				id: 0, memberType: v1alpha1.DatameshMemberTypeDiskful,
				quorum: ptr.To(false), bvState: v1alpha1.DiskStateUpToDate,
			},
			replicaEntry{
				id: 1, memberType: v1alpha1.DatameshMemberTypeDiskful,
				quorum: ptr.To(false), bvState: v1alpha1.DiskStateUpToDate,
			},
		)
		var el v1alpha1.ReplicatedVolumeEffectiveLayout
		updateEffectiveLayout(gctx, &el)

		Expect(el.FailuresToTolerate).To(Equal(ptr.To(int8(-2))))
		Expect(el.GuaranteedMinimumDataRedundancy).To(Equal(ptr.To(int8(0))))
	})

	It("GMDR negative (no UpToDate)", func() {
		gctx := mkEffGctx(1, 1, replicaEntry{
			id: 0, memberType: v1alpha1.DatameshMemberTypeDiskful,
			quorum: ptr.To(true),
		})
		var el v1alpha1.ReplicatedVolumeEffectiveLayout
		updateEffectiveLayout(gctx, &el)

		Expect(el.FailuresToTolerate).To(Equal(ptr.To(int8(-1))))
		Expect(el.GuaranteedMinimumDataRedundancy).To(Equal(ptr.To(int8(-1))))
	})

	It("FTT limited by data, not quorum", func() {
		gctx := mkEffGctx(2, 2,
			readyD(0),
			replicaEntry{
				id: 1, memberType: v1alpha1.DatameshMemberTypeDiskful,
				quorum: ptr.To(true),
			},
			replicaEntry{
				id: 2, memberType: v1alpha1.DatameshMemberTypeDiskful,
				quorum: ptr.To(true),
			},
		)
		var el v1alpha1.ReplicatedVolumeEffectiveLayout
		updateEffectiveLayout(gctx, &el)

		Expect(el.FailuresToTolerate).To(Equal(ptr.To(int8(-1))))
		Expect(el.GuaranteedMinimumDataRedundancy).To(Equal(ptr.To(int8(0))))
	})

	It("GMDR capped by qmr", func() {
		gctx := mkEffGctx(2, 1, readyD(0), readyD(1), readyD(2))
		var el v1alpha1.ReplicatedVolumeEffectiveLayout
		updateEffectiveLayout(gctx, &el)

		Expect(el.FailuresToTolerate).To(Equal(ptr.To(int8(1))))
		Expect(el.GuaranteedMinimumDataRedundancy).To(Equal(ptr.To(int8(0))))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// updateEffectiveLayout — non-obvious interactions
//

var _ = Describe("updateEffectiveLayout non-obvious interactions", func() {
	It("sD and LsD members are ignored", func() {
		gctx := mkEffGctx(2, 1,
			readyD(0), readyD(1),
			replicaEntry{
				id: 2, memberType: v1alpha1.DatameshMemberTypeShadowDiskful,
				quorum: ptr.To(true), bvState: v1alpha1.DiskStateUpToDate,
			},
		)
		var el v1alpha1.ReplicatedVolumeEffectiveLayout
		updateEffectiveLayout(gctx, &el)

		Expect(el.TotalVoters).To(Equal(int8(2)))
		Expect(el.FailuresToTolerate).To(Equal(ptr.To(int8(0))))
	})

	It("member without RVR is skipped", func() {
		gctx := mkEffGctx(1, 1,
			readyD(0),
			replicaEntry{
				id: 1, memberType: v1alpha1.DatameshMemberTypeDiskful,
				noRVR: true,
			},
		)
		var el v1alpha1.ReplicatedVolumeEffectiveLayout
		updateEffectiveLayout(gctx, &el)

		// #1 skipped (no rvr) → totalVoters still counts only #0.
		Expect(el.TotalVoters).To(Equal(int8(1)))
		Expect(el.FailuresToTolerate).To(Equal(ptr.To(int8(0))))
	})

	It("peer reports for non-member IDs are harmless", func() {
		gctx := mkEffGctx(1, 1,
			readyD(0, peerEntry{"rv-1-5", true, true}),
		)
		var el v1alpha1.ReplicatedVolumeEffectiveLayout
		updateEffectiveLayout(gctx, &el)

		Expect(el.TotalVoters).To(Equal(int8(1)))
		Expect(el.ReachableVoters).To(Equal(int8(1)))
		Expect(el.FailuresToTolerate).To(Equal(ptr.To(int8(0))))
	})
})
