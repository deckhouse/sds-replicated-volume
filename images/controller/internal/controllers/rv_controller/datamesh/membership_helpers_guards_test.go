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
