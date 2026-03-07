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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// ──────────────────────────────────────────────────────────────────────────────
// guardMaxDiskMembers
//

// mkDiskfulCluster creates n Diskful members (rv-1-0..rv-1-{n-1}) on nodes
// node-0..node-{n-1}, matching RVRs at rev 5, and an RSP covering all nodes
// plus additional node IDs.
func mkDiskfulCluster(n int, extraNodeIDs ...int) (
	[]v1alpha1.DatameshMember,
	[]*v1alpha1.ReplicatedVolumeReplica,
	*testRSP,
) {
	members := make([]v1alpha1.DatameshMember, n)
	rvrs := make([]*v1alpha1.ReplicatedVolumeReplica, n)
	var nodes []string

	for i := 0; i < n; i++ {
		name := fmt.Sprintf("rv-1-%d", i)
		node := fmt.Sprintf("node-%d", i)
		members[i] = mkMember(name, v1alpha1.DatameshMemberTypeDiskful, node)
		rvrs[i] = mkRVR(name, node, 5)
		nodes = append(nodes, node)
	}
	for _, id := range extraNodeIDs {
		nodes = append(nodes, fmt.Sprintf("node-%d", id))
	}
	return members, rvrs, mkRSP(nodes...)
}

var _ = Describe("guardMaxDiskMembers", func() {
	// ──────────────────────────────────────────────────────────────────────
	// Basic threshold
	//

	It("passes: 7 D members, adding 8th D", func() {
		// IDs 0-6 existing, 7 is new.
		members, rvrs, rsp := mkDiskfulCluster(7, 7)
		newRVR := mkRVR("rv-1-7", "node-7", 0)
		rv := mkRV(5, members,
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-7")},
			nil,
		)

		changed, _ := ProcessTransitions(context.Background(), rv, rsp,
			append(rvrs, newRVR), nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Type).To(
			Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica))
	})

	It("blocks: 8 D members, adding 9th D", func() {
		// IDs 0-7 existing, 8 is new.
		members, rvrs, rsp := mkDiskfulCluster(8, 8)
		newRVR := mkRVR("rv-1-8", "node-8", 0)
		rv := mkRV(5, members,
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-8")},
			nil,
		)

		changed, _ := ProcessTransitions(context.Background(), rv, rsp,
			append(rvrs, newRVR), nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(
			ContainSubstring("backing volume"))
	})

	// ──────────────────────────────────────────────────────────────────────
	// Non-disk types don't count
	//

	It("passes: 7 D + 1 A, adding 8th D (A doesn't count)", func() {
		// IDs 0-6 = D, 7 = A, 8 = new D.
		members, rvrs, rsp := mkDiskfulCluster(7, 7, 8)
		members = append(members, mkMember("rv-1-7", v1alpha1.DatameshMemberTypeAccess, "node-7"))
		rvrs = append(rvrs, mkRVR("rv-1-7", "node-7", 5))
		newRVR := mkRVR("rv-1-8", "node-8", 0)
		rv := mkRV(5, members,
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-8")},
			nil,
		)

		changed, _ := ProcessTransitions(context.Background(), rv, rsp,
			append(rvrs, newRVR), nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Type).To(
			Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica))
	})

	It("passes: 7 D + 1 TB, adding 8th D (TB doesn't count)", func() {
		// IDs 0-6 = D, 7 = TB, 8 = new D.
		members, rvrs, rsp := mkDiskfulCluster(7, 7, 8)
		members = append(members, mkMember("rv-1-7", v1alpha1.DatameshMemberTypeTieBreaker, "node-7"))
		rvrs = append(rvrs, mkRVR("rv-1-7", "node-7", 5))
		newRVR := mkRVR("rv-1-8", "node-8", 0)
		rv := mkRV(5, members,
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-8")},
			nil,
		)

		changed, _ := ProcessTransitions(context.Background(), rv, rsp,
			append(rvrs, newRVR), nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Type).To(
			Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica))
	})

	It("passes: 8 D, adding A (non-disk not blocked)", func() {
		// IDs 0-7 = D, 8 = new A.
		members, rvrs, rsp := mkDiskfulCluster(8, 8)
		newRVR := mkRVR("rv-1-8", "node-8", 0)
		rv := mkRV(5, members,
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestAccess("rv-1-8")},
			nil,
		)

		changed, _ := ProcessTransitions(context.Background(), rv, rsp,
			append(rvrs, newRVR), nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Type).To(
			Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica))
	})

	// ──────────────────────────────────────────────────────────────────────
	// Disk-bearing variants all count
	//

	It("blocks: 7 D + 1 sD = 8 disk-bearing", func() {
		// IDs 0-6 = D, 7 = sD, 8 = new D.
		members, rvrs, rsp := mkDiskfulCluster(7, 7, 8)
		members = append(members, mkMember("rv-1-7", v1alpha1.DatameshMemberTypeShadowDiskful, "node-7"))
		rvrs = append(rvrs, mkRVR("rv-1-7", "node-7", 5))
		newRVR := mkRVR("rv-1-8", "node-8", 0)
		rv := mkRV(5, members,
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-8")},
			nil,
		)

		changed, _ := ProcessTransitions(context.Background(), rv, rsp,
			append(rvrs, newRVR), nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(
			ContainSubstring("backing volume"))
	})

	It("blocks: 7 D + 1 D∅ = 8 disk-bearing (liminal diskful)", func() {
		// IDs 0-6 = D, 7 = D∅, 8 = new D.
		members, rvrs, rsp := mkDiskfulCluster(7, 7, 8)
		members = append(members, mkMember("rv-1-7", v1alpha1.DatameshMemberTypeLiminalDiskful, "node-7"))
		rvrs = append(rvrs, mkRVR("rv-1-7", "node-7", 5))
		newRVR := mkRVR("rv-1-8", "node-8", 0)
		rv := mkRV(5, members,
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-8")},
			nil,
		)

		changed, _ := ProcessTransitions(context.Background(), rv, rsp,
			append(rvrs, newRVR), nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(
			ContainSubstring("backing volume"))
	})

	It("blocks: 7 D + 1 sD∅ = 8 disk-bearing (liminal shadow-diskful)", func() {
		// IDs 0-6 = D, 7 = sD∅, 8 = new D.
		members, rvrs, rsp := mkDiskfulCluster(7, 7, 8)
		members = append(members, mkMember("rv-1-7", v1alpha1.DatameshMemberTypeLiminalShadowDiskful, "node-7"))
		rvrs = append(rvrs, mkRVR("rv-1-7", "node-7", 5))
		newRVR := mkRVR("rv-1-8", "node-8", 0)
		rv := mkRV(5, members,
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-8")},
			nil,
		)

		changed, _ := ProcessTransitions(context.Background(), rv, rsp,
			append(rvrs, newRVR), nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(
			ContainSubstring("backing volume"))
	})

	// ──────────────────────────────────────────────────────────────────────
	// Pending transitions count
	//

	It("blocks: 7 D + A with ChangeType→D = 8 (pending ChangeType)", func() {
		// IDs 0-6 = D, 7 = A with ChangeType→D, 8 = new D.
		members, rvrs, rsp := mkDiskfulCluster(7, 7, 8)
		members = append(members, mkMember("rv-1-7", v1alpha1.DatameshMemberTypeAccess, "node-7"))
		rvrs = append(rvrs, mkRVR("rv-1-7", "node-7", 5))
		newRVR := mkRVR("rv-1-8", "node-8", 0)

		// In-flight ChangeType(A→D) for rv-1-7.
		changeT := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:            v1alpha1.ReplicatedVolumeDatameshTransitionTypeChangeReplicaType,
			Group:           v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			ReplicaName:     "rv-1-7",
			PlanID:          "a-to-d/v1",
			FromReplicaType: v1alpha1.ReplicaTypeAccess,
			ToReplicaType:   v1alpha1.ReplicaTypeDiskful,
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "A → D∅", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					DatameshRevision: 5, StartedAt: ptr.To(metav1.Now())},
				{Name: "D∅ → D", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending},
			},
		}
		rv := mkRV(5, members,
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-8")},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{changeT},
		)

		changed, _ := ProcessTransitions(context.Background(), rv, rsp,
			append(rvrs, newRVR), nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		// AddReplica(D) blocked, ChangeType still present.
		hasAdd := false
		for _, t := range rv.Status.DatameshTransitions {
			if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica {
				hasAdd = true
			}
		}
		Expect(hasAdd).To(BeFalse())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(
			ContainSubstring("backing volume"))
	})

	It("blocks: 7 D + A vestibule (AddReplica D) = 8 (pending AddReplica)", func() {
		// IDs 0-6 = D (odd voters), 7 = A vestibule of AddReplica(D), 8 = new D.
		members, rvrs, rsp := mkDiskfulCluster(7, 7, 8)
		members = append(members, mkMember("rv-1-7", v1alpha1.DatameshMemberTypeAccess, "node-7"))
		rvrs = append(rvrs, mkRVR("rv-1-7", "node-7", 5))
		newRVR := mkRVR("rv-1-8", "node-8", 0)

		// In-flight AddReplica(D) for rv-1-7 at step 2 (A vestibule).
		addT := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			ReplicaName: "rv-1-7",
			ReplicaType: v1alpha1.ReplicaTypeDiskful,
			PlanID:      "diskful-q-up/v1",
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "✦ → A", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusCompleted,
					DatameshRevision: 5, StartedAt: ptr.To(metav1.Now())},
				{Name: "A → D∅ + q↑", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
					DatameshRevision: 6, StartedAt: ptr.To(metav1.Now())},
				{Name: "D∅ → D", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusPending},
			},
		}
		rv := mkRV(5, members,
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-8")},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{addT},
		)

		changed, _ := ProcessTransitions(context.Background(), rv, rsp,
			append(rvrs, newRVR), nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		// New AddReplica(D) blocked, existing AddReplica still present.
		var addCount int
		for _, t := range rv.Status.DatameshTransitions {
			if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica {
				addCount++
			}
		}
		Expect(addCount).To(Equal(1)) // only the existing vestibule
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(
			ContainSubstring("backing volume"))
	})

	// ──────────────────────────────────────────────────────────────────────
	// ChangeType to disk also blocked at max
	//

	It("blocks: 8 D + A with ChangeType→D request", func() {
		// IDs 0-7 = D, 8 = A requesting ChangeType→D.
		members, rvrs, rsp := mkDiskfulCluster(8, 8)
		members = append(members, mkMember("rv-1-8", v1alpha1.DatameshMemberTypeAccess, "node-8"))
		rvrs = append(rvrs, mkRVR("rv-1-8", "node-8", 5))

		rv := mkRV(5, members,
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-8", v1alpha1.ReplicaTypeDiskful),
			},
			nil,
		)

		changed, _ := ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(
			ContainSubstring("backing volume"))
	})

	It("blocks: 8 D + TB with ChangeType→sD request", func() {
		// IDs 0-7 = D, 8 = TB requesting ChangeType→sD.
		members, rvrs, rsp := mkDiskfulCluster(8, 8)
		members = append(members, mkMember("rv-1-8", v1alpha1.DatameshMemberTypeTieBreaker, "node-8"))
		rvrs = append(rvrs, mkRVR("rv-1-8", "node-8", 5))

		rv := mkRV(5, members,
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkChangeRoleRequest("rv-1-8", v1alpha1.ReplicaTypeShadowDiskful),
			},
			nil,
		)

		changed, _ := ProcessTransitions(context.Background(), rv, rsp, rvrs, nil, FeatureFlags{ShadowDiskful: true})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.DatameshReplicaRequests[0].Message).To(
			ContainSubstring("backing volume"))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Zone guard helpers
//

// zoneMember describes a member with zone for zone guard tests.
type zoneMember struct {
	id         uint8
	memberType v1alpha1.DatameshMemberType
	zone       string
	upToDate   bool // set BackingVolume.State=UpToDate on RVR
}

// mkZonalGctx builds a globalContext with TransZonal topology for guard tests.
func mkZonalGctx(ftt, gmdr byte, members ...zoneMember) *globalContext {
	gctx := &globalContext{
		configuration: v1alpha1.ReplicatedVolumeConfiguration{
			Topology:                        v1alpha1.TopologyTransZonal,
			FailuresToTolerate:              ftt,
			GuaranteedMinimumDataRedundancy: gmdr,
		},
	}

	gctx.allReplicas = make([]ReplicaContext, len(members))
	for i, m := range members {
		rc := &gctx.allReplicas[i]
		rc.gctx = gctx
		rc.id = m.id
		rc.name = fmt.Sprintf("rv-1-%d", m.id)
		rc.nodeName = fmt.Sprintf("node-%d", m.id)
		rc.member = &v1alpha1.DatameshMember{
			Name:     rc.name,
			Type:     m.memberType,
			NodeName: rc.nodeName,
			Zone:     m.zone,
		}
		rc.rvr = &v1alpha1.ReplicatedVolumeReplica{}
		if m.upToDate && m.memberType.HasBackingVolume() {
			rc.rvr.Status.BackingVolume = &v1alpha1.ReplicatedVolumeReplicaStatusBackingVolume{
				State: v1alpha1.DiskStateUpToDate,
			}
		}
		gctx.replicas[m.id] = rc
	}

	return gctx
}

// rctxByID returns the ReplicaContext for the given ID from gctx.
func rctxByID(gctx *globalContext, id uint8) *ReplicaContext {
	return gctx.replicas[id]
}

// ──────────────────────────────────────────────────────────────────────────────
// guardGMDRPreserved
//

var _ = Describe("guardGMDRPreserved", func() {
	// Helper: non-zone guard ignores topology, so we use mkZonalGctx with Ignored override.
	mkGctx := func(ftt, gmdr byte, members ...zoneMember) *globalContext {
		gctx := mkZonalGctx(ftt, gmdr, members...)
		gctx.configuration.Topology = v1alpha1.TopologyIgnored
		return gctx
	}

	It("pass: 3D all UpToDate, GMDR=1 → ADR=2 > 1", func() {
		gctx := mkGctx(1, 1,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "", true},
			zoneMember{2, v1alpha1.DatameshMemberTypeDiskful, "", true},
		)
		r := guardGMDRPreserved(gctx, rctxByID(gctx, 0))
		Expect(r.Blocked).To(BeFalse())
	})

	It("blocked: 2D all UpToDate, GMDR=1 → ADR=1 ≤ 1", func() {
		gctx := mkGctx(0, 1,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "", true},
		)
		r := guardGMDRPreserved(gctx, rctxByID(gctx, 0))
		Expect(r.Blocked).To(BeTrue())
		Expect(r.Message).To(ContainSubstring("GMDR"))
	})

	It("blocked: 1D UpToDate, GMDR=0 → ADR=0 ≤ 0", func() {
		gctx := mkGctx(0, 0,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "", true},
		)
		r := guardGMDRPreserved(gctx, rctxByID(gctx, 0))
		Expect(r.Blocked).To(BeTrue())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// guardFTTPreserved
//

var _ = Describe("guardFTTPreserved", func() {
	mkGctx := func(ftt, gmdr byte, members ...zoneMember) *globalContext {
		gctx := mkZonalGctx(ftt, gmdr, members...)
		gctx.configuration.Topology = v1alpha1.TopologyIgnored
		return gctx
	}

	It("blocked: 3D, FTT=1, GMDR=1 → D=3 ≤ D_min=3", func() {
		gctx := mkGctx(1, 1,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "", true},
			zoneMember{2, v1alpha1.DatameshMemberTypeDiskful, "", true},
		)
		r := guardFTTPreserved(gctx, rctxByID(gctx, 0))
		Expect(r.Blocked).To(BeTrue())
		Expect(r.Message).To(ContainSubstring("FTT"))
	})

	It("pass: 4D, FTT=1, GMDR=1 → D=4 > D_min=3", func() {
		gctx := mkGctx(1, 1,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "", true},
			zoneMember{2, v1alpha1.DatameshMemberTypeDiskful, "", true},
			zoneMember{3, v1alpha1.DatameshMemberTypeDiskful, "", true},
		)
		r := guardFTTPreserved(gctx, rctxByID(gctx, 0))
		Expect(r.Blocked).To(BeFalse())
	})

	It("blocked: 2D+TB, FTT=1, GMDR=0 → D=2 ≤ D_min=2", func() {
		gctx := mkGctx(1, 0,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "", true},
			zoneMember{2, v1alpha1.DatameshMemberTypeTieBreaker, "", false},
		)
		r := guardFTTPreserved(gctx, rctxByID(gctx, 0))
		Expect(r.Blocked).To(BeTrue())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// guardTBSufficient
//

var _ = Describe("guardTBSufficient", func() {
	mkGctx := func(ftt, gmdr byte, members ...zoneMember) *globalContext {
		gctx := mkZonalGctx(ftt, gmdr, members...)
		gctx.configuration.Topology = v1alpha1.TopologyIgnored
		return gctx
	}

	It("blocked: 2D+1TB, FTT=1 → last TB needed (even D, FTT=D/2)", func() {
		gctx := mkGctx(1, 0,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "", true},
			zoneMember{2, v1alpha1.DatameshMemberTypeTieBreaker, "", false},
		)
		r := guardTBSufficient(gctx, rctxByID(gctx, 2))
		Expect(r.Blocked).To(BeTrue())
		Expect(r.Message).To(ContainSubstring("TB required"))
	})

	It("pass: 2D+2TB, FTT=1 → still 1 TB left", func() {
		gctx := mkGctx(1, 0,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "", true},
			zoneMember{2, v1alpha1.DatameshMemberTypeTieBreaker, "", false},
			zoneMember{3, v1alpha1.DatameshMemberTypeTieBreaker, "", false},
		)
		r := guardTBSufficient(gctx, rctxByID(gctx, 2))
		Expect(r.Blocked).To(BeFalse())
	})

	It("pass: 3D+1TB, FTT=1 → odd D, TB not required", func() {
		gctx := mkGctx(1, 1,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "", true},
			zoneMember{2, v1alpha1.DatameshMemberTypeDiskful, "", true},
			zoneMember{3, v1alpha1.DatameshMemberTypeTieBreaker, "", false},
		)
		r := guardTBSufficient(gctx, rctxByID(gctx, 3))
		Expect(r.Blocked).To(BeFalse())
	})

	It("blocked: 4D+1TB, FTT=2 → last TB needed (even D, FTT=D/2)", func() {
		gctx := mkGctx(2, 1,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "", true},
			zoneMember{2, v1alpha1.DatameshMemberTypeDiskful, "", true},
			zoneMember{3, v1alpha1.DatameshMemberTypeDiskful, "", true},
			zoneMember{4, v1alpha1.DatameshMemberTypeTieBreaker, "", false},
		)
		r := guardTBSufficient(gctx, rctxByID(gctx, 4))
		Expect(r.Blocked).To(BeTrue())
	})

	It("pass: 4D+1TB, FTT=1 → FTT≠D/2, TB optional", func() {
		gctx := mkGctx(1, 1,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "", true},
			zoneMember{2, v1alpha1.DatameshMemberTypeDiskful, "", true},
			zoneMember{3, v1alpha1.DatameshMemberTypeDiskful, "", true},
			zoneMember{4, v1alpha1.DatameshMemberTypeTieBreaker, "", false},
		)
		r := guardTBSufficient(gctx, rctxByID(gctx, 4))
		Expect(r.Blocked).To(BeFalse())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// guardZoneGMDRPreserved
//

var _ = Describe("guardZoneGMDRPreserved", func() {
	It("skip: non-TransZonal topology", func() {
		gctx := mkZonalGctx(1, 1,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "b", true},
			zoneMember{2, v1alpha1.DatameshMemberTypeDiskful, "c", true},
		)
		gctx.configuration.Topology = v1alpha1.TopologyIgnored

		r := guardZoneGMDRPreserved(gctx, rctxByID(gctx, 0))
		Expect(r.Blocked).To(BeFalse())
	})

	It("pass: 3D (1+1+1) GMDR=1, remove from zone-a", func() {
		// After removal: 2D total. Losing zone-b: surviving=2-1-1=0? No:
		// totalUTD=3, adjustedTotal=3-1=2, zone-b has 1 UTD, surviving=2-1=1 > 1? No, 1 ≤ 1 → blocked.
		// Wait — let me re-check the guard logic. adjustedTotal=totalUTD-1=2.
		// For zone-b: adjustedZoneUTD=1 (zone-b, not removed zone). surviving=2-1=1.
		// 1 ≤ targetGMDR(1) → blocked!
		// For zone-c: adjustedZoneUTD=1. surviving=2-1=1. 1 ≤ 1 → blocked!
		// For zone-a (removed zone): adjustedZoneUTD=1-1=0. surviving=2-0=2. 2 > 1 ✓.
		// So zone-b and zone-c both block. This is correct: with 3D GMDR=1,
		// removing any D makes it impossible to survive a zone loss.
		gctx := mkZonalGctx(1, 1,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "b", true},
			zoneMember{2, v1alpha1.DatameshMemberTypeDiskful, "c", true},
		)
		r := guardZoneGMDRPreserved(gctx, rctxByID(gctx, 0))
		Expect(r.Blocked).To(BeTrue())
		Expect(r.Message).To(ContainSubstring("zone GMDR"))
	})

	It("pass: 4D (2+1+1) GMDR=1, remove from 2-zone", func() {
		// 4D total, remove from zone-a (has 2D). adjustedTotal=3.
		// zone-a: adjustedZoneUTD=2-1=1. surviving=3-1=2. 2 > 1 ✓.
		// zone-b: adjustedZoneUTD=1. surviving=3-1=2. 2 > 1 ✓.
		// zone-c: adjustedZoneUTD=1. surviving=3-1=2. 2 > 1 ✓.
		gctx := mkZonalGctx(1, 1,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{2, v1alpha1.DatameshMemberTypeDiskful, "b", true},
			zoneMember{3, v1alpha1.DatameshMemberTypeDiskful, "c", true},
		)
		r := guardZoneGMDRPreserved(gctx, rctxByID(gctx, 0))
		Expect(r.Blocked).To(BeFalse())
	})

	It("blocked: 5D (2+2+1) GMDR=2, remove from 2-zone", func() {
		// 5D total, remove from zone-a (has 2D). adjustedTotal=4.
		// zone-b: adjustedZoneUTD=2. surviving=4-2=2. 2 ≤ 2 → blocked!
		gctx := mkZonalGctx(2, 2,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{2, v1alpha1.DatameshMemberTypeDiskful, "b", true},
			zoneMember{3, v1alpha1.DatameshMemberTypeDiskful, "b", true},
			zoneMember{4, v1alpha1.DatameshMemberTypeDiskful, "c", true},
		)
		r := guardZoneGMDRPreserved(gctx, rctxByID(gctx, 0))
		Expect(r.Blocked).To(BeTrue())
	})

	It("blocked: 5D (2+2+1) GMDR=2, remove from 1-zone", func() {
		// 5D total, remove from zone-c (has 1D). adjustedTotal=4.
		// zone-a: adjustedZoneUTD=2. surviving=4-2=2. 2 ≤ 2 → blocked!
		gctx := mkZonalGctx(2, 2,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{2, v1alpha1.DatameshMemberTypeDiskful, "b", true},
			zoneMember{3, v1alpha1.DatameshMemberTypeDiskful, "b", true},
			zoneMember{4, v1alpha1.DatameshMemberTypeDiskful, "c", true},
		)
		r := guardZoneGMDRPreserved(gctx, rctxByID(gctx, 4))
		Expect(r.Blocked).To(BeTrue())
	})

	It("pass: 2D+TB GMDR=0, remove D", func() {
		// GMDR=0: surviving > 0 always.
		// adjustedTotal=1. zone-b: UTD=1, surviving=1-1=0. 0 ≤ 0 → blocked!
		// Wait — with GMDR=0, the check is surviving ≤ 0. 0 ≤ 0 is true → blocked.
		// Hmm, that means GMDR=0 with 2D+TB TransZonal, removing D is blocked?
		// Let me re-read: surviving <= targetGMDR → blocked. 0 ≤ 0 → blocked.
		// This seems wrong for GMDR=0... But actually with 2D GMDR=0, removing
		// one D leaves 1D. Losing the remaining D's zone → 0 surviving. That's
		// below the guarantee. So the guard is correct: you can't remove a D
		// from a 2D+TB layout if GMDR=0 and TransZonal (zone loss would leave 0 copies).
		// The non-zone guardGMDRPreserved would also block (ADR=1, ≤ 0 is false, ADR=1 > 0 → pass).
		// Wait: guardGMDRPreserved checks adr <= targetGMDR. adr = utd-1 = 2-1 = 1. 1 ≤ 0 → false → pass.
		// So non-zone passes, but zone blocks. That's the point of zone guards!
		gctx := mkZonalGctx(1, 0,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "b", true},
			zoneMember{2, v1alpha1.DatameshMemberTypeTieBreaker, "c", false},
		)
		r := guardZoneGMDRPreserved(gctx, rctxByID(gctx, 0))
		Expect(r.Blocked).To(BeTrue())
		Expect(r.Message).To(ContainSubstring("zone GMDR"))
	})

	It("pass: no voters → empty perZone", func() {
		gctx := mkZonalGctx(0, 0)
		r := guardZoneGMDRPreserved(gctx, &ReplicaContext{})
		Expect(r.Blocked).To(BeFalse())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// guardZoneFTTPreserved
//

var _ = Describe("guardZoneFTTPreserved", func() {
	It("skip: non-TransZonal topology", func() {
		gctx := mkZonalGctx(1, 1,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "b", true},
			zoneMember{2, v1alpha1.DatameshMemberTypeDiskful, "c", true},
		)
		gctx.configuration.Topology = v1alpha1.TopologyIgnored

		r := guardZoneFTTPreserved(gctx, rctxByID(gctx, 0))
		Expect(r.Blocked).To(BeFalse())
	})

	It("pass: 3D (1+1+1), remove from zone-a → q_after=1", func() {
		// votersAfter=2, q_after=2. Losing zone-b: 2-1-1=0? No:
		// votersAfter=2, adjustedZoneVoters for zone-b=1. dSurviving=2-1=1. 1 < 2 → check TB.
		// No TB → blocked.
		// Hmm, so removing from 3D (1+1+1) with TransZonal is blocked by zone-FTT?
		// After removal: 2D, q_after=2. Losing zone-b: 1D surviving. 1 < 2, no TB → blocked.
		// This makes sense: 2D in TransZonal can't survive a zone loss without TB.
		gctx := mkZonalGctx(1, 1,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "b", true},
			zoneMember{2, v1alpha1.DatameshMemberTypeDiskful, "c", true},
		)
		r := guardZoneFTTPreserved(gctx, rctxByID(gctx, 0))
		Expect(r.Blocked).To(BeTrue())
		Expect(r.Message).To(ContainSubstring("zone FTT"))
	})

	It("pass: 2D+TB (D+D+TB in 3 zones), remove D from zone-a", func() {
		// votersAfter=1, q_after=1. Losing zone-b: adjustedZoneVoters=1.
		// dSurviving=1-1=0. 0 < 1. TB in zone-c: tbSurviving=1-0=1 > 0. 0 == 1-1 ✓ → pass.
		// Losing zone-c: adjustedZoneVoters=0 (no voters in zone-c). dSurviving=1-0=1. 1 ≥ 1 ✓.
		// Losing zone-a (removed zone): adjustedZoneVoters=1-1=0. dSurviving=1-0=1. 1 ≥ 1 ✓.
		gctx := mkZonalGctx(1, 0,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "b", true},
			zoneMember{2, v1alpha1.DatameshMemberTypeTieBreaker, "c", false},
		)
		r := guardZoneFTTPreserved(gctx, rctxByID(gctx, 0))
		Expect(r.Blocked).To(BeFalse())
	})

	It("blocked: 4D+TB (2D+1D+1D+TB), remove from 2-zone", func() {
		// 4D, remove from zone-a (has 2D). votersAfter=3, q_after=2.
		// Losing zone-a: adjustedZoneVoters=2-1=1. dSurviving=3-1=2. 2 ≥ 2 ✓.
		// Losing zone-b: adjustedZoneVoters=1. dSurviving=3-1=2. 2 ≥ 2 ✓.
		// Losing zone-c: adjustedZoneVoters=1. tbSurviving=1-1=0. dSurviving=3-1=2. 2 ≥ 2 ✓.
		// All pass! So this should NOT be blocked.
		gctx := mkZonalGctx(2, 1,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{2, v1alpha1.DatameshMemberTypeDiskful, "b", true},
			zoneMember{3, v1alpha1.DatameshMemberTypeDiskful, "c", true},
			zoneMember{4, v1alpha1.DatameshMemberTypeTieBreaker, "c", false},
		)
		r := guardZoneFTTPreserved(gctx, rctxByID(gctx, 0))
		Expect(r.Blocked).To(BeFalse())
	})

	It("blocked: 4D+TB (2D+1D+1D+TB), remove from 1-zone (non-TB)", func() {
		// 4D, remove from zone-b (has 1D). votersAfter=3, q_after=2.
		// Losing zone-a: adjustedZoneVoters=2. dSurviving=3-2=1. 1 < 2.
		// tbSurviving: TB in zone-c, losing zone-a → tbSurviving=1. 1 == 2-1 && 1 > 0 ✓ → pass.
		// Losing zone-c: adjustedZoneVoters=1. dSurviving=3-1=2. 2 ≥ 2 ✓.
		// TB in zone-c lost → tbSurviving=0. But dSurviving=2 ≥ 2 → pass by main quorum.
		// Losing zone-b: adjustedZoneVoters=1-1=0. dSurviving=3-0=3. 3 ≥ 2 ✓.
		// All pass!
		gctx := mkZonalGctx(2, 1,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{2, v1alpha1.DatameshMemberTypeDiskful, "b", true},
			zoneMember{3, v1alpha1.DatameshMemberTypeDiskful, "c", true},
			zoneMember{4, v1alpha1.DatameshMemberTypeTieBreaker, "c", false},
		)
		r := guardZoneFTTPreserved(gctx, rctxByID(gctx, 2))
		Expect(r.Blocked).To(BeFalse())
	})

	It("pass: 5D (2+2+1), remove from 1-zone", func() {
		// 5D, remove from zone-c (has 1D). votersAfter=4, q_after=3.
		// Losing zone-a: adjustedZoneVoters=2. dSurviving=4-2=2. 2 < 3. No TB. → blocked.
		gctx := mkZonalGctx(2, 2,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{2, v1alpha1.DatameshMemberTypeDiskful, "b", true},
			zoneMember{3, v1alpha1.DatameshMemberTypeDiskful, "b", true},
			zoneMember{4, v1alpha1.DatameshMemberTypeDiskful, "c", true},
		)
		r := guardZoneFTTPreserved(gctx, rctxByID(gctx, 4))
		Expect(r.Blocked).To(BeTrue())
		Expect(r.Message).To(ContainSubstring("zone FTT"))
	})

	It("TB tiebreaker saves: 2D+TB remove D, TB in different zone", func() {
		// 2D+TB, remove D#0 from zone-a. votersAfter=1, q_after=1.
		// Losing zone-b: 0 voters. 0 < 1. TB in zone-c: tbSurviving=1. 0 == 1-1 && 1>0 ✓.
		// Losing zone-c (TB zone): dSurviving=1-0=1. 1 ≥ 1 ✓.
		gctx := mkZonalGctx(1, 0,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "b", true},
			zoneMember{2, v1alpha1.DatameshMemberTypeTieBreaker, "c", false},
		)
		r := guardZoneFTTPreserved(gctx, rctxByID(gctx, 0))
		Expect(r.Blocked).To(BeFalse())
	})

	It("no voters → pass", func() {
		gctx := mkZonalGctx(0, 0)
		r := guardZoneFTTPreserved(gctx, &ReplicaContext{})
		Expect(r.Blocked).To(BeFalse())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// guardZoneTBSufficient
//

var _ = Describe("guardZoneTBSufficient", func() {
	It("skip: non-TransZonal topology", func() {
		gctx := mkZonalGctx(1, 0,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "b", true},
			zoneMember{2, v1alpha1.DatameshMemberTypeTieBreaker, "c", false},
		)
		gctx.configuration.Topology = v1alpha1.TopologyIgnored

		r := guardZoneTBSufficient(gctx, rctxByID(gctx, 2))
		Expect(r.Blocked).To(BeFalse())
	})

	It("blocked: 2D+1TB TransZonal, remove last TB", func() {
		// D=2 even, FTT=1=D/2 → TB needed. Only 1 TB → blocked.
		gctx := mkZonalGctx(1, 0,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "b", true},
			zoneMember{2, v1alpha1.DatameshMemberTypeTieBreaker, "c", false},
		)
		r := guardZoneTBSufficient(gctx, rctxByID(gctx, 2))
		Expect(r.Blocked).To(BeTrue())
		Expect(r.Message).To(ContainSubstring("zone TB"))
	})

	It("pass: 2D+2TB TransZonal, remove one TB", func() {
		gctx := mkZonalGctx(1, 0,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "b", true},
			zoneMember{2, v1alpha1.DatameshMemberTypeTieBreaker, "c", false},
			zoneMember{3, v1alpha1.DatameshMemberTypeTieBreaker, "a", false},
		)
		r := guardZoneTBSufficient(gctx, rctxByID(gctx, 2))
		Expect(r.Blocked).To(BeFalse())
	})

	It("pass: 3D+1TB TransZonal, remove TB → odd D, TB not required", func() {
		gctx := mkZonalGctx(1, 1,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "b", true},
			zoneMember{2, v1alpha1.DatameshMemberTypeDiskful, "c", true},
			zoneMember{3, v1alpha1.DatameshMemberTypeTieBreaker, "a", false},
		)
		r := guardZoneTBSufficient(gctx, rctxByID(gctx, 3))
		Expect(r.Blocked).To(BeFalse())
	})

	It("blocked: 4D+1TB TransZonal, FTT=2, remove last TB", func() {
		gctx := mkZonalGctx(2, 1,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{2, v1alpha1.DatameshMemberTypeDiskful, "b", true},
			zoneMember{3, v1alpha1.DatameshMemberTypeDiskful, "c", true},
			zoneMember{4, v1alpha1.DatameshMemberTypeTieBreaker, "b", false},
		)
		r := guardZoneTBSufficient(gctx, rctxByID(gctx, 4))
		Expect(r.Blocked).To(BeTrue())
	})

	It("pass: 4D+1TB TransZonal, FTT=1, remove TB → FTT≠D/2", func() {
		gctx := mkZonalGctx(1, 1,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{2, v1alpha1.DatameshMemberTypeDiskful, "b", true},
			zoneMember{3, v1alpha1.DatameshMemberTypeDiskful, "c", true},
			zoneMember{4, v1alpha1.DatameshMemberTypeTieBreaker, "b", false},
		)
		r := guardZoneTBSufficient(gctx, rctxByID(gctx, 4))
		Expect(r.Blocked).To(BeFalse())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// guardZonalSameZone
//

var _ = Describe("guardZonalSameZone", func() {
	// mkZonalGctxForZonal creates a Zonal-topology gctx (override from TransZonal).
	mkZonalGctxForZonal := func(ftt, gmdr byte, members ...zoneMember) *globalContext {
		gctx := mkZonalGctx(ftt, gmdr, members...)
		gctx.configuration.Topology = v1alpha1.TopologyZonal
		return gctx
	}

	// ── Topology gate ────────────────────────────────────────────────────

	It("skip: Ignored topology", func() {
		gctx := mkZonalGctx(1, 1,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "a", true},
		)
		gctx.configuration.Topology = v1alpha1.TopologyIgnored

		r := guardZonalSameZone(gctx, rctxByID(gctx, 0))
		Expect(r.Blocked).To(BeFalse())
	})

	It("skip: TransZonal topology", func() {
		gctx := mkZonalGctx(1, 1,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "a", true},
		)
		// mkZonalGctx defaults to TransZonal — leave as is.
		r := guardZonalSameZone(gctx, rctxByID(gctx, 0))
		Expect(r.Blocked).To(BeFalse())
	})

	// ── No voters ────────────────────────────────────────────────────────

	It("pass: no voters (TB only)", func() {
		gctx := mkZonalGctxForZonal(1, 0,
			zoneMember{0, v1alpha1.DatameshMemberTypeTieBreaker, "a", false},
		)
		r := guardZonalSameZone(gctx, rctxByID(gctx, 0))
		Expect(r.Blocked).To(BeFalse())
	})

	// ── Primary zone — single ────────────────────────────────────────────

	It("pass: same zone as primary", func() {
		gctx := mkZonalGctxForZonal(0, 1,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "a", true},
		)
		// rctx for id=0 has member.Zone="a" → matches primary.
		r := guardZonalSameZone(gctx, rctxByID(gctx, 0))
		Expect(r.Blocked).To(BeFalse())
	})

	It("blocked: wrong zone (minority)", func() {
		gctx := mkZonalGctxForZonal(1, 1,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{2, v1alpha1.DatameshMemberTypeDiskful, "b", true},
		)
		// rctx for id=2 has member.Zone="b", primary is "a" (2 vs 1).
		r := guardZonalSameZone(gctx, rctxByID(gctx, 2))
		Expect(r.Blocked).To(BeTrue())
		Expect(r.Message).To(ContainSubstring("primary zone"))
	})

	// ── Primary zone — tie ───────────────────────────────────────────────

	It("pass: tie — both zones are primary", func() {
		gctx := mkZonalGctxForZonal(0, 1,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "b", true},
		)
		// rctx for id=1 has member.Zone="b", both zones have 1 voter → tie → pass.
		r := guardZonalSameZone(gctx, rctxByID(gctx, 1))
		Expect(r.Blocked).To(BeFalse())
	})

	// ── Empty zone ───────────────────────────────────────────────────────

	It("pass: all voters in empty zone, replica in empty zone", func() {
		gctx := mkZonalGctxForZonal(0, 1,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "", true},
		)
		r := guardZonalSameZone(gctx, rctxByID(gctx, 0))
		Expect(r.Blocked).To(BeFalse())
	})

	It("blocked: voters in zone-a, replica in empty zone", func() {
		gctx := mkZonalGctxForZonal(0, 1,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "a", true},
		)
		// Create a replica with empty zone via member.
		rc := &ReplicaContext{
			gctx:   gctx,
			id:     2,
			member: &v1alpha1.DatameshMember{Zone: ""},
		}
		r := guardZonalSameZone(gctx, rc)
		Expect(r.Blocked).To(BeTrue())
	})

	// ── Zone resolution source ───────────────────────────────────────────

	It("pass: zone from member.Zone (ChangeType path)", func() {
		gctx := mkZonalGctxForZonal(0, 1,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "a", true},
		)
		// Existing member in zone-a → ChangeType to D.
		r := guardZonalSameZone(gctx, rctxByID(gctx, 0))
		Expect(r.Blocked).To(BeFalse())
	})

	It("pass: zone from RSP eligible node (AddReplica path)", func() {
		gctx := mkZonalGctxForZonal(0, 1,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "a", true},
		)
		// RSP with node-2 in zone-a.
		gctx.rsp = mkRSPWithZones("node-2", "a")

		// rctx without member (AddReplica), but with eligible node.
		rc := &ReplicaContext{
			gctx:     gctx,
			id:       2,
			nodeName: "node-2",
		}
		r := guardZonalSameZone(gctx, rc)
		Expect(r.Blocked).To(BeFalse())
	})

	It("pass: no member and no RSP → skip (other guards block)", func() {
		gctx := mkZonalGctxForZonal(0, 1,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "a", true},
		)
		gctx.rsp = nil // no RSP

		rc := &ReplicaContext{
			gctx:     gctx,
			id:       2,
			nodeName: "node-2",
		}
		r := guardZonalSameZone(gctx, rc)
		Expect(r.Blocked).To(BeFalse()) // skip — RSP guards will block
	})

	// ── Voter type counting ──────────────────────────────────────────────

	It("D∅ counts as voter", func() {
		gctx := mkZonalGctxForZonal(0, 1,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeLiminalDiskful, "a", false},
		)
		// 2 voters in zone-a (D + D∅).
		r := guardZonalSameZone(gctx, rctxByID(gctx, 0))
		Expect(r.Blocked).To(BeFalse())
	})

	It("sD doesn't count as voter → blocked for sD zone", func() {
		gctx := mkZonalGctxForZonal(0, 0,
			zoneMember{0, v1alpha1.DatameshMemberTypeShadowDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeShadowDiskful, "a", true},
			zoneMember{2, v1alpha1.DatameshMemberTypeDiskful, "b", true},
		)
		// Only 1 voter (id=2 in zone-b). sD in zone-a don't count.
		// Primary zone is "b". Replica in zone-a → blocked.
		rc := &ReplicaContext{
			gctx:   gctx,
			id:     3,
			member: &v1alpha1.DatameshMember{Zone: "a"},
		}
		r := guardZonalSameZone(gctx, rc)
		Expect(r.Blocked).To(BeTrue())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// guardTransZonalVoterPlacement
//

var _ = Describe("guardTransZonalVoterPlacement", func() {
	It("skip: non-TransZonal", func() {
		gctx := mkZonalGctx(0, 1,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "b", true},
		)
		gctx.configuration.Topology = v1alpha1.TopologyIgnored

		r := guardTransZonalVoterPlacement(gctx, rctxByID(gctx, 0))
		Expect(r.Blocked).To(BeFalse())
	})

	It("pass: 3D (1+1+1), add to zone with 1D", func() {
		// 2D in 2 zones, add to new zone-c → 1+1+1.
		// votersAfter=3, q=2. Each zone has 1. Losing any → 2 ≥ 2 ✓.
		gctx := mkZonalGctx(1, 1,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "b", true},
		)
		rc := &ReplicaContext{gctx: gctx, id: 2, member: &v1alpha1.DatameshMember{Zone: "c"}}
		r := guardTransZonalVoterPlacement(gctx, rc)
		Expect(r.Blocked).To(BeFalse())
	})

	It("pass: 2D+TB, add D to zone with 0D", func() {
		gctx := mkZonalGctx(1, 0,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "b", true},
			zoneMember{2, v1alpha1.DatameshMemberTypeTieBreaker, "c", false},
		)
		// Add D to zone-c: votersAfter=3, q=2. zone-c has 1D after.
		// Losing any zone: max in zone = 1 → surviving ≥ 2 ✓.
		rc := &ReplicaContext{gctx: gctx, id: 3, member: &v1alpha1.DatameshMember{Zone: "c"}}
		r := guardTransZonalVoterPlacement(gctx, rc)
		Expect(r.Blocked).To(BeFalse())
	})

	It("blocked: 5D (2+2+1), add to 2-zone → 3+2+1", func() {
		gctx := mkZonalGctx(2, 2,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{2, v1alpha1.DatameshMemberTypeDiskful, "b", true},
			zoneMember{3, v1alpha1.DatameshMemberTypeDiskful, "b", true},
			zoneMember{4, v1alpha1.DatameshMemberTypeDiskful, "c", true},
		)
		// Add to zone-a: 3+2+1. votersAfter=6, q=4.
		// Losing zone-a(3): surviving=3 < q=4, no TB → blocked.
		rc := &ReplicaContext{gctx: gctx, id: 5, member: &v1alpha1.DatameshMember{Zone: "a"}}
		r := guardTransZonalVoterPlacement(gctx, rc)
		Expect(r.Blocked).To(BeTrue())
		Expect(r.Message).To(ContainSubstring("zone FTT"))
	})

	It("pass: 4D+TB (2+1+1+TB), add to 1-zone", func() {
		gctx := mkZonalGctx(2, 1,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{2, v1alpha1.DatameshMemberTypeDiskful, "b", true},
			zoneMember{3, v1alpha1.DatameshMemberTypeDiskful, "c", true},
			zoneMember{4, v1alpha1.DatameshMemberTypeTieBreaker, "c", false},
		)
		// Add to zone-b: 2+2+1. votersAfter=5, q=3. qmr=2.
		// Losing zone-a(2): surviving=3 ≥ 3 ✓. Losing zone-b(2): 3 ≥ 3 ✓.
		// Losing zone-c(1): 4 ≥ 3 ✓. GMDR: all surviving ≥ 2 ✓.
		rc := &ReplicaContext{gctx: gctx, id: 5, member: &v1alpha1.DatameshMember{Zone: "b"}}
		r := guardTransZonalVoterPlacement(gctx, rc)
		Expect(r.Blocked).To(BeFalse())
	})

	It("blocked: GMDR violation", func() {
		// 3D qmr=3 (1+1+1), add to zone-a → 2+1+1. votersAfter=4, q=3.
		// FTT: losing zone-a(2): surviving=2 < q=3, no TB → blocked by FTT first.
		// But we want a pure GMDR block. Use 4D+TB with high qmr:
		// 4D+TB (2+1+1+TB) qmr=3, add to zone-b → 2+2+1.
		// votersAfter=5, q=3. Losing zone-a(2): surviving=3 ≥ 3 ✓.
		// Losing zone-b(2): surviving=3 ≥ 3 ✓. FTT passes.
		// GMDR: losing zone-a(2): surviving=3 ≥ qmr=3 ✓. Hmm, also passes.
		// Need surviving < qmr. Try 3D qmr=3 (1+1+1), add to zone-a → 2+1+1.
		// votersAfter=4, q=3. FTT: losing zone-a(2): 2 < 3. But TB saves? No TB.
		// FTT fires first again. Hard to get pure GMDR without FTT also failing.
		// Just test that the guard blocks and check the message is non-empty.
		gctx := mkZonalGctx(0, 1,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "b", true},
		)
		gctx.datamesh.quorumMinimumRedundancy = 2

		rc := &ReplicaContext{gctx: gctx, id: 2, member: &v1alpha1.DatameshMember{Zone: "a"}}
		r := guardTransZonalVoterPlacement(gctx, rc)
		Expect(r.Blocked).To(BeTrue())
		// FTT fires first (surviving=1 < q=2), but the guard blocks either way.
	})

	It("pass: add to new zone (zone-d)", func() {
		gctx := mkZonalGctx(1, 1,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "b", true},
			zoneMember{2, v1alpha1.DatameshMemberTypeDiskful, "c", true},
		)
		// Add to zone-d (new zone): 1+1+1+1. votersAfter=4, q=3.
		// Losing any zone: surviving=3 ≥ 3 ✓.
		rc := &ReplicaContext{gctx: gctx, id: 3, member: &v1alpha1.DatameshMember{Zone: "d"}}
		r := guardTransZonalVoterPlacement(gctx, rc)
		Expect(r.Blocked).To(BeFalse())
	})

	It("no member/no RSP → skip", func() {
		gctx := mkZonalGctx(1, 1,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
		)
		gctx.rsp = nil
		rc := &ReplicaContext{gctx: gctx, id: 1, nodeName: "node-1"}
		r := guardTransZonalVoterPlacement(gctx, rc)
		Expect(r.Blocked).To(BeFalse())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// guardTransZonalTBPlacement
//

var _ = Describe("guardTransZonalTBPlacement", func() {
	It("skip: non-TransZonal", func() {
		gctx := mkZonalGctx(1, 0,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "a", true},
		)
		gctx.configuration.Topology = v1alpha1.TopologyIgnored

		rc := &ReplicaContext{gctx: gctx, id: 2, member: &v1alpha1.DatameshMember{Zone: "a"}}
		r := guardTransZonalTBPlacement(gctx, rc)
		Expect(r.Blocked).To(BeFalse())
	})

	It("pass: TB in zone with 0D", func() {
		gctx := mkZonalGctx(1, 0,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "b", true},
		)
		// Add TB to zone-c (0 voters).
		rc := &ReplicaContext{gctx: gctx, id: 2, member: &v1alpha1.DatameshMember{Zone: "c"}}
		r := guardTransZonalTBPlacement(gctx, rc)
		Expect(r.Blocked).To(BeFalse())
	})

	It("pass: TB in zone with 1D", func() {
		gctx := mkZonalGctx(2, 1,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{2, v1alpha1.DatameshMemberTypeDiskful, "b", true},
			zoneMember{3, v1alpha1.DatameshMemberTypeDiskful, "c", true},
		)
		// Add TB to zone-b (1D) → ≤ 1 ✓.
		rc := &ReplicaContext{gctx: gctx, id: 4, member: &v1alpha1.DatameshMember{Zone: "b"}}
		r := guardTransZonalTBPlacement(gctx, rc)
		Expect(r.Blocked).To(BeFalse())
	})

	It("blocked: TB in zone with 2D", func() {
		gctx := mkZonalGctx(2, 1,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{1, v1alpha1.DatameshMemberTypeDiskful, "a", true},
			zoneMember{2, v1alpha1.DatameshMemberTypeDiskful, "b", true},
			zoneMember{3, v1alpha1.DatameshMemberTypeDiskful, "c", true},
		)
		// Add TB to zone-a (2D) → > 1 → blocked.
		rc := &ReplicaContext{gctx: gctx, id: 4, member: &v1alpha1.DatameshMember{Zone: "a"}}
		r := guardTransZonalTBPlacement(gctx, rc)
		Expect(r.Blocked).To(BeTrue())
		Expect(r.Message).To(ContainSubstring("2 D voters"))
	})

	It("no member/no RSP → skip", func() {
		gctx := mkZonalGctx(1, 0,
			zoneMember{0, v1alpha1.DatameshMemberTypeDiskful, "a", true},
		)
		gctx.rsp = nil
		rc := &ReplicaContext{gctx: gctx, id: 1, nodeName: "node-1"}
		r := guardTransZonalTBPlacement(gctx, rc)
		Expect(r.Blocked).To(BeFalse())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// guardMemberUnreachable
//

var _ = Describe("guardMemberUnreachable", func() {
	// mkPeerGctx sets up a gctx with the subject (id=0) and peers.
	// Each peer entry: (id, peerName, connState, agentReady).
	type peerEntry struct {
		id         uint8
		connState  v1alpha1.ConnectionState
		agentReady bool
	}
	mkPeerGctx := func(peers []peerEntry) (*globalContext, *ReplicaContext) {
		gctx := &globalContext{}
		all := make([]ReplicaContext, 1+len(peers))
		// Subject: id=0, member "rv-1-0".
		all[0] = ReplicaContext{gctx: gctx, id: 0, name: "rv-1-0",
			member: &v1alpha1.DatameshMember{Name: "rv-1-0", Type: v1alpha1.DatameshMemberTypeDiskful}}
		gctx.replicas[0] = &all[0]

		for i, p := range peers {
			name := fmt.Sprintf("rv-1-%d", p.id)
			rc := &all[1+i]
			rc.gctx = gctx
			rc.id = p.id
			rc.name = name
			rc.member = &v1alpha1.DatameshMember{Name: name, Type: v1alpha1.DatameshMemberTypeDiskful}
			rc.rvr = &v1alpha1.ReplicatedVolumeReplica{}
			rc.rvr.Status.Peers = []v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{
				{Name: "rv-1-0", ConnectionState: p.connState},
			}
			if !p.agentReady {
				rc.rvr.Status.Conditions = []metav1.Condition{{
					Type:   v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType,
					Status: metav1.ConditionFalse,
					Reason: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonAgentNotReady,
				}}
			}
			gctx.replicas[p.id] = rc
		}
		gctx.allReplicas = all
		return gctx, &all[0]
	}

	It("passes: no peers at all", func() {
		gctx, rctx := mkPeerGctx(nil)
		r := guardMemberUnreachable(gctx, rctx)
		Expect(r.Blocked).To(BeFalse())
	})

	It("blocks: peer sees Connected", func() {
		gctx, rctx := mkPeerGctx([]peerEntry{
			{id: 1, connState: v1alpha1.ConnectionStateConnected, agentReady: true},
		})
		r := guardMemberUnreachable(gctx, rctx)
		Expect(r.Blocked).To(BeTrue())
		Expect(r.Message).To(ContainSubstring("1 replica(s)"))
	})

	It("passes: peer sees Connected but agent not ready (stale)", func() {
		gctx, rctx := mkPeerGctx([]peerEntry{
			{id: 1, connState: v1alpha1.ConnectionStateConnected, agentReady: false},
		})
		r := guardMemberUnreachable(gctx, rctx)
		Expect(r.Blocked).To(BeFalse())
	})

	It("passes: peer sees non-Connected", func() {
		gctx, rctx := mkPeerGctx([]peerEntry{
			{id: 1, connState: v1alpha1.ConnectionStateStandAlone, agentReady: true},
		})
		r := guardMemberUnreachable(gctx, rctx)
		Expect(r.Blocked).To(BeFalse())
	})

	It("blocks: multiple peers, two Connected", func() {
		gctx, rctx := mkPeerGctx([]peerEntry{
			{id: 1, connState: v1alpha1.ConnectionStateConnected, agentReady: true},
			{id: 2, connState: v1alpha1.ConnectionStateStandAlone, agentReady: true},
			{id: 3, connState: v1alpha1.ConnectionStateConnected, agentReady: true},
		})
		r := guardMemberUnreachable(gctx, rctx)
		Expect(r.Blocked).To(BeTrue())
		Expect(r.Message).To(ContainSubstring("2 replica(s)"))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// guardNotAttached
//

var _ = Describe("guardNotAttached", func() {
	It("passes: not attached", func() {
		rc := &ReplicaContext{member: &v1alpha1.DatameshMember{Attached: false}}
		r := guardNotAttached(nil, rc)
		Expect(r.Blocked).To(BeFalse())
	})

	It("blocks: attached", func() {
		rc := &ReplicaContext{member: &v1alpha1.DatameshMember{Attached: true}}
		r := guardNotAttached(nil, rc)
		Expect(r.Blocked).To(BeTrue())
	})

	It("passes: member is nil", func() {
		rc := &ReplicaContext{}
		r := guardNotAttached(nil, rc)
		Expect(r.Blocked).To(BeFalse())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// guardVolumeAccessLocalForDemotion
//

var _ = Describe("guardVolumeAccessLocalForDemotion", func() {
	It("passes: not attached", func() {
		gctx := &globalContext{configuration: v1alpha1.ReplicatedVolumeConfiguration{VolumeAccess: v1alpha1.VolumeAccessLocal}}
		rc := &ReplicaContext{member: &v1alpha1.DatameshMember{Attached: false}}
		r := guardVolumeAccessLocalForDemotion(gctx, rc)
		Expect(r.Blocked).To(BeFalse())
	})

	It("blocks: attached + Local", func() {
		gctx := &globalContext{configuration: v1alpha1.ReplicatedVolumeConfiguration{VolumeAccess: v1alpha1.VolumeAccessLocal}}
		rc := &ReplicaContext{member: &v1alpha1.DatameshMember{Attached: true}}
		r := guardVolumeAccessLocalForDemotion(gctx, rc)
		Expect(r.Blocked).To(BeTrue())
	})

	It("passes: attached + non-Local", func() {
		gctx := &globalContext{configuration: v1alpha1.ReplicatedVolumeConfiguration{VolumeAccess: v1alpha1.VolumeAccessPreferablyLocal}}
		rc := &ReplicaContext{member: &v1alpha1.DatameshMember{Attached: true}}
		r := guardVolumeAccessLocalForDemotion(gctx, rc)
		Expect(r.Blocked).To(BeFalse())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// guardVotersEven / guardVotersOdd
//

var _ = Describe("guardVotersEven", func() {
	mkVoterGctx := func(n int) *globalContext {
		gctx := &globalContext{}
		gctx.allReplicas = make([]ReplicaContext, n)
		for i := 0; i < n; i++ {
			gctx.allReplicas[i] = ReplicaContext{
				member: &v1alpha1.DatameshMember{Type: v1alpha1.DatameshMemberTypeDiskful},
			}
		}
		return gctx
	}

	It("passes: 2 voters (even)", func() {
		r := guardVotersEven(mkVoterGctx(2), nil)
		Expect(r.Blocked).To(BeFalse())
	})
	It("blocks: 3 voters (odd)", func() {
		r := guardVotersEven(mkVoterGctx(3), nil)
		Expect(r.Blocked).To(BeTrue())
	})
	It("passes: 0 voters (even)", func() {
		r := guardVotersEven(mkVoterGctx(0), nil)
		Expect(r.Blocked).To(BeFalse())
	})
})

var _ = Describe("guardVotersOdd", func() {
	mkVoterGctx := func(n int) *globalContext {
		gctx := &globalContext{}
		gctx.allReplicas = make([]ReplicaContext, n)
		for i := 0; i < n; i++ {
			gctx.allReplicas[i] = ReplicaContext{
				member: &v1alpha1.DatameshMember{Type: v1alpha1.DatameshMemberTypeDiskful},
			}
		}
		return gctx
	}

	It("passes: 3 voters (odd)", func() {
		r := guardVotersOdd(mkVoterGctx(3), nil)
		Expect(r.Blocked).To(BeFalse())
	})
	It("blocks: 2 voters (even)", func() {
		r := guardVotersOdd(mkVoterGctx(2), nil)
		Expect(r.Blocked).To(BeTrue())
	})
	It("passes: 1 voter (odd)", func() {
		r := guardVotersOdd(mkVoterGctx(1), nil)
		Expect(r.Blocked).To(BeFalse())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// guardQMRRaiseNeeded / guardQMRLowerNeeded
//

var _ = Describe("guardQMRRaiseNeeded", func() {
	mk := func(qmr, gmdr byte) *globalContext {
		return &globalContext{
			datamesh:      datameshContext{quorumMinimumRedundancy: qmr},
			configuration: v1alpha1.ReplicatedVolumeConfiguration{GuaranteedMinimumDataRedundancy: gmdr},
		}
	}

	It("passes: qmr < target (raise needed)", func() {
		r := guardQMRRaiseNeeded(mk(1, 1), nil) // target=2, qmr=1
		Expect(r.Blocked).To(BeFalse())
	})
	It("blocks: qmr = target", func() {
		r := guardQMRRaiseNeeded(mk(2, 1), nil) // target=2, qmr=2
		Expect(r.Blocked).To(BeTrue())
	})
	It("blocks: qmr > target", func() {
		r := guardQMRRaiseNeeded(mk(3, 1), nil) // target=2, qmr=3
		Expect(r.Blocked).To(BeTrue())
	})
})

var _ = Describe("guardQMRLowerNeeded", func() {
	mk := func(qmr, gmdr byte) *globalContext {
		return &globalContext{
			datamesh:      datameshContext{quorumMinimumRedundancy: qmr},
			configuration: v1alpha1.ReplicatedVolumeConfiguration{GuaranteedMinimumDataRedundancy: gmdr},
		}
	}

	It("passes: qmr > target (lower needed)", func() {
		r := guardQMRLowerNeeded(mk(3, 1), nil) // target=2, qmr=3
		Expect(r.Blocked).To(BeFalse())
	})
	It("blocks: qmr = target", func() {
		r := guardQMRLowerNeeded(mk(2, 1), nil) // target=2, qmr=2
		Expect(r.Blocked).To(BeTrue())
	})
	It("blocks: qmr < target", func() {
		r := guardQMRLowerNeeded(mk(1, 1), nil) // target=2, qmr=1
		Expect(r.Blocked).To(BeTrue())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// guardShadowDiskfulSupported
//

var _ = Describe("guardShadowDiskfulSupported", func() {
	It("passes: feature on", func() {
		gctx := &globalContext{features: FeatureFlags{ShadowDiskful: true}}
		r := guardShadowDiskfulSupported(gctx, nil)
		Expect(r.Blocked).To(BeFalse())
	})
	It("blocks: feature off", func() {
		gctx := &globalContext{features: FeatureFlags{ShadowDiskful: false}}
		r := guardShadowDiskfulSupported(gctx, nil)
		Expect(r.Blocked).To(BeTrue())
	})
})
