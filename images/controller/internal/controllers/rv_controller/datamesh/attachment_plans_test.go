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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// ──────────────────────────────────────────────────────────────────────────────
// Attach
//

var _ = Describe("Attach", func() {
	It("creates transition for member with active RVA", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")},
			nil, nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVRReady("rv-1-0", "node-1", 5)}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-1")}

		changed, replicas := ProcessTransitions(context.Background(), rv, mkRSP("node-1"), rvrs, rvas, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		t := &rv.Status.DatameshTransitions[0]
		Expect(t.Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach))
		Expect(t.ReplicaName).To(Equal("rv-1-0"))
		Expect(rv.Status.Datamesh.Members[0].Attached).To(BeTrue())
		Expect(rv.Status.DatameshRevision).To(Equal(int64(6)))

		rc := findReplicaContext(replicas, 0)
		Expect(rc).NotTo(BeNil())
		Expect(rc.AttachmentConditionReason()).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttaching))
	})

	It("guard: RV deleting", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")},
			nil, nil,
		)
		rv.DeletionTimestamp = ptr.To(metav1.Now())
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVRReady("rv-1-0", "node-1", 5)}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-1")}

		_, replicas := ProcessTransitions(context.Background(), rv, mkRSP("node-1"), rvrs, rvas, FeatureFlags{})

		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		rc := findReplicaContext(replicas, 0)
		Expect(rc.AttachmentConditionMessage()).To(ContainSubstring("being deleted"))
	})

	It("guard: quorum not satisfied", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")},
			nil, nil,
		)
		// mkRVR (not Ready) — no Quorum field set → quorum not satisfied.
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-0", "node-1", 5)}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-1")}

		_, replicas := ProcessTransitions(context.Background(), rv, mkRSP("node-1"), rvrs, rvas, FeatureFlags{})

		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		rc := findReplicaContext(replicas, 0)
		Expect(rc.AttachmentConditionMessage()).To(ContainSubstring("Quorum not satisfied"))
	})

	It("guard: slot full", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMemberAttached("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-2"),
			},
			nil, nil,
		)
		// MaxAttachments=1 (default from mkRV), node-1 already attached.
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRReady("rv-1-0", "node-1", 5),
			mkRVRReady("rv-1-1", "node-2", 5),
		}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{
			mkRVA("rva-1", "node-1"),
			mkRVA("rva-2", "node-2"),
		}

		_, replicas := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, rvas, FeatureFlags{})

		// node-2 should be blocked by slot.
		rc1 := findReplicaContext(replicas, 1)
		Expect(rc1.AttachmentConditionMessage()).To(ContainSubstring("slot"))
	})

	It("guard: VolumeAccess Local blocks non-diskful member", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-0"), // voter for quorum
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-1"),
			},
			nil, nil,
		)
		rv.Status.Configuration = &v1alpha1.ReplicatedVolumeConfiguration{
			ReplicatedStoragePoolName: "test-pool", Topology: v1alpha1.TopologyIgnored,
			VolumeAccess: v1alpha1.VolumeAccessLocal,
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRReady("rv-1-0", "node-0", 5), // voter with quorum
			mkRVRReady("rv-1-1", "node-1", 5),
		}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-1")}

		_, replicas := ProcessTransitions(context.Background(), rv, mkRSP("node-0", "node-1"), rvrs, rvas, FeatureFlags{})

		// No Attach for node-1 (Access member in Local mode).
		rc := findReplicaContext(replicas, 1)
		Expect(rc.AttachmentConditionReason()).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonVolumeAccessLocalityNotSatisfied))
	})

	It("guard: VolumeAccess Local allows Diskful member", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")},
			nil, nil,
		)
		rv.Status.Configuration = &v1alpha1.ReplicatedVolumeConfiguration{
			ReplicatedStoragePoolName: "test-pool", Topology: v1alpha1.TopologyIgnored,
			VolumeAccess: v1alpha1.VolumeAccessLocal,
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVRReady("rv-1-0", "node-1", 5)}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-1")}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1"), rvrs, rvas, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach))
	})

	It("guard: member not found", func() {
		// Voter member on node-0 for quorum; RVA on node-1 which has RVR but no member.
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-0")},
			nil, nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRReady("rv-1-0", "node-0", 5),
			mkRVRReady("rv-1-1", "node-1", 5), // no member for this RVR
		}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-1")}

		_, replicas := ProcessTransitions(context.Background(), rv, mkRSP("node-0", "node-1"), rvrs, rvas, FeatureFlags{})

		// Dispatcher dispatches Attach → guardMemberExists blocks.
		for i := range replicas {
			if replicas[i].nodeName == "node-1" {
				Expect(replicas[i].AttachmentConditionMessage()).To(ContainSubstring("Waiting"))
				break
			}
		}
	})

	It("guard: node not eligible", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")},
			nil, nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVRReady("rv-1-0", "node-1", 5)}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-1")}

		// RSP has node-2 but not node-1.
		_, replicas := ProcessTransitions(context.Background(), rv, mkRSP("node-2"), rvrs, rvas, FeatureFlags{})

		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		rc := findReplicaContext(replicas, 0)
		Expect(rc.AttachmentConditionReason()).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonNodeNotEligible))
	})

	It("guard: node not ready", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")},
			nil, nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVRReady("rv-1-0", "node-1", 5)}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-1")}
		rsp := &testRSP{nodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1", NodeReady: false, AgentReady: true},
		}}

		_, replicas := ProcessTransitions(context.Background(), rv, rsp, rvrs, rvas, FeatureFlags{})

		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		rc := findReplicaContext(replicas, 0)
		Expect(rc.AttachmentConditionMessage()).To(ContainSubstring("Node is not ready"))
	})

	It("guard: agent not ready", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")},
			nil, nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVRReady("rv-1-0", "node-1", 5)}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-1")}
		rsp := &testRSP{nodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1", NodeReady: true, AgentReady: false},
		}}

		_, replicas := ProcessTransitions(context.Background(), rv, rsp, rvrs, rvas, FeatureFlags{})

		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		rc := findReplicaContext(replicas, 0)
		Expect(rc.AttachmentConditionMessage()).To(ContainSubstring("Agent is not ready"))
	})

	It("guard: RVR not Ready", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")},
			nil, nil,
		)
		// mkRVR without Ready condition — guardRVRReady blocks.
		// But we need Quorum to pass guardQuorumSatisfied first.
		rvr := mkRVR("rv-1-0", "node-1", 5)
		rvr.Status.Quorum = ptr.To(true)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-1")}

		_, replicas := ProcessTransitions(context.Background(), rv, mkRSP("node-1"), rvrs, rvas, FeatureFlags{})

		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		rc := findReplicaContext(replicas, 0)
		Expect(rc.AttachmentConditionReason()).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonWaitingForReplica))
	})

	It("guard: active membership transition", func() {
		// AddReplica transition active for rv-1-1.
		// Voter rv-1-0 on node-0 for quorum.
		addT := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddReplica,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupNonVotingMembership,
			ReplicaName: "rv-1-1", PlanID: "access/v1",
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{
				Name: "✦ → A", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
				DatameshRevision: 5, StartedAt: ptr.To(metav1.Now()),
			}},
		}
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-0"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-1"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestAccess("rv-1-1")},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{addT},
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRReady("rv-1-0", "node-0", 5),
			mkRVRReady("rv-1-1", "node-1", 4),
		}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-1")}

		_, replicas := ProcessTransitions(context.Background(), rv, mkRSP("node-0", "node-1"), rvrs, rvas, FeatureFlags{})

		// No Attach created (membership transition active).
		hasAttach := false
		for _, t := range rv.Status.DatameshTransitions {
			if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach {
				hasAttach = true
			}
		}
		Expect(hasAttach).To(BeFalse())
		rc := findReplicaContext(replicas, 1)
		Expect(rc.AttachmentConditionMessage()).To(ContainSubstring("membership transition"))
	})

	It("settled: already attached + active RVA", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{mkMemberAttached("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")},
			nil, nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVRReady("rv-1-0", "node-1", 5)}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-1")}

		changed, replicas := ProcessTransitions(context.Background(), rv, mkRSP("node-1"), rvrs, rvas, FeatureFlags{})

		Expect(changed).To(BeFalse())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		rc := findReplicaContext(replicas, 0)
		Expect(rc.AttachmentConditionReason()).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttached))
		Expect(rc.AttachmentConditionMessage()).To(ContainSubstring("attached and ready"))
	})

	It("settled: already attached + RV deleting", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{mkMemberAttached("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")},
			nil, nil,
		)
		rv.DeletionTimestamp = ptr.To(metav1.Now())
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVRReady("rv-1-0", "node-1", 5)}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-1")}

		_, replicas := ProcessTransitions(context.Background(), rv, mkRSP("node-1"), rvrs, rvas, FeatureFlags{})

		rc := findReplicaContext(replicas, 0)
		Expect(rc.AttachmentConditionMessage()).To(ContainSubstring("pending deletion"))
	})

	It("FIFO: earlier RVA gets slot", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-2"),
			},
			nil, nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRReady("rv-1-0", "node-1", 5),
			mkRVRReady("rv-1-1", "node-2", 5),
		}
		// node-1 RVA older than node-2 RVA.
		rva1 := mkRVA("rva-1", "node-1")
		rva1.CreationTimestamp = metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
		rva2 := mkRVA("rva-2", "node-2")
		rva2.CreationTimestamp = metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC))
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{rva1, rva2} // sorted by NodeName

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, rvas, FeatureFlags{})

		Expect(changed).To(BeTrue())
		// Only 1 slot (MaxAttachments=1) → node-1 (older) gets it.
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].ReplicaName).To(Equal("rv-1-0"))
	})

	It("maxAttachments=2: first Attach + EnableMultiattach, second waits", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-2"),
			},
			nil, nil,
		)
		rv.Spec.MaxAttachments = 2
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRReady("rv-1-0", "node-1", 5),
			mkRVRReady("rv-1-1", "node-2", 5),
		}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-1"), mkRVA("rva-2", "node-2")}

		changed, replicas := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, rvas, FeatureFlags{})

		Expect(changed).To(BeTrue())
		// First node gets Attach. EnableMultiattach created.
		// Second node blocked by tracker (multiattach toggle in progress) — waits.
		attachCount := 0
		hasEnable := false
		for _, t := range rv.Status.DatameshTransitions {
			if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach {
				attachCount++
			}
			if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeEnableMultiattach {
				hasEnable = true
			}
		}
		Expect(attachCount).To(Equal(1))
		Expect(hasEnable).To(BeTrue())
		// Second node waiting for multiattach.
		rc1 := findReplicaContext(replicas, 1)
		Expect(rc1.AttachmentConditionMessage()).To(ContainSubstring("multiattach"))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Detach
//

var _ = Describe("Detach", func() {
	It("creates transition when attached + no active RVA", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{mkMemberAttached("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")},
			nil, nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVRReady("rv-1-0", "node-1", 5)}
		// No RVAs → detach.

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach))
		Expect(rv.Status.Datamesh.Members[0].Attached).To(BeFalse())
	})

	It("guard: device in use", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{mkMemberAttached("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")},
			nil, nil,
		)
		rvr := mkRVRReady("rv-1-0", "node-1", 5)
		rvr.Status.Attachment = &v1alpha1.ReplicatedVolumeReplicaStatusAttachment{InUse: true}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}

		_, replicas := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		rc := findReplicaContext(replicas, 0)
		Expect(rc.AttachmentConditionMessage()).To(ContainSubstring("Device in use"))
	})

	It("no transition for non-member node with deleting RVA", func() {
		rv := mkRV(5, nil, nil, nil) // no members
		rva := mkRVA("rva-1", "node-1")
		rva.DeletionTimestamp = ptr.To(metav1.Now())
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{rva}

		changed, replicas := ProcessTransitions(context.Background(), rv, nil, nil, rvas, FeatureFlags{})

		// No transitions, condition = Detached.
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		for i := range replicas {
			if replicas[i].nodeName == "node-1" {
				Expect(replicas[i].AttachmentConditionReason()).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonDetached))
				break
			}
		}
		_ = changed
	})

	It("works when RV deleting", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{mkMemberAttached("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")},
			nil, nil,
		)
		rv.DeletionTimestamp = ptr.To(metav1.Now())
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVRReady("rv-1-0", "node-1", 5)}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach))
	})

	It("orphan: only deleting RVAs, no member", func() {
		rv := mkRV(5, nil, nil, nil)
		rva := mkRVA("rva-1", "node-1")
		rva.DeletionTimestamp = ptr.To(metav1.Now())

		_, replicas := ProcessTransitions(context.Background(), rv, nil, nil, []*v1alpha1.ReplicatedVolumeAttachment{rva}, FeatureFlags{})

		for i := range replicas {
			if replicas[i].nodeName == "node-1" {
				Expect(replicas[i].AttachmentConditionMessage()).To(ContainSubstring("has been detached"))
				break
			}
		}
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Settle
//

var _ = Describe("Attachment settle", func() {
	mkAttachTransition := func(replicaName string, revision int64) v1alpha1.ReplicatedVolumeDatameshTransition {
		return v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment,
			ReplicaName: replicaName, PlanID: "attach/v1",
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{
				Name: "Attach", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
				DatameshRevision: revision, StartedAt: ptr.To(metav1.Now()),
			}},
		}
	}

	mkDetachTransition := func(replicaName string, revision int64) v1alpha1.ReplicatedVolumeDatameshTransition {
		return v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment,
			ReplicaName: replicaName, PlanID: "detach/v1",
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{
				Name: "Detach", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
				DatameshRevision: revision, StartedAt: ptr.To(metav1.Now()),
			}},
		}
	}

	It("completes Attach when confirmed", func() {
		t := mkAttachTransition("rv-1-0", 6)
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{mkMemberAttached("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")},
			nil, []v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVRReady("rv-1-0", "node-1", 6)}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-1")}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1"), rvrs, rvas, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})

	It("completes Detach when confirmed", func() {
		t := mkDetachTransition("rv-1-0", 6)
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")},
			nil, []v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVRReady("rv-1-0", "node-1", 6)}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})

	It("completes Detach when rvr gone", func() {
		t := mkDetachTransition("rv-1-0", 6)
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")},
			nil, []v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		// No RVR for rv-1-0 → confirmSubjectOnlyLeavingOrGone accepts rvr=nil.

		changed, _ := ProcessTransitions(context.Background(), rv, nil, nil, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		// Detach completed. ForceRemove auto-dispatched for orphan member (no RVR).
		for _, tr := range rv.Status.DatameshTransitions {
			Expect(tr.Type).NotTo(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach))
		}
	})

	It("completes Detach when rvr revision is 0", func() {
		t := mkDetachTransition("rv-1-0", 6)
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")},
			nil, []v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		// RVR exists but revision=0 → left datamesh.
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVR("rv-1-0", "node-1", 0)}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})

	It("Attach partial progress shows message", func() {
		t := mkAttachTransition("rv-1-0", 6)
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{mkMemberAttached("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")},
			nil, []v1alpha1.ReplicatedVolumeDatameshTransition{t},
		)
		// Not yet confirmed.
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVRReady("rv-1-0", "node-1", 5)}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-1")}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1"), rvrs, rvas, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Steps[0].Message).To(ContainSubstring("0/1"))
	})

	It("EnableMultiattach completes when all members confirm", func() {
		enableT := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:   v1alpha1.ReplicatedVolumeDatameshTransitionTypeEnableMultiattach,
			Group:  v1alpha1.ReplicatedVolumeDatameshTransitionGroupMultiattach,
			PlanID: "enable-multiattach/v1",
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{
				Name: "Enable multiattach", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
				DatameshRevision: 6, StartedAt: ptr.To(metav1.Now()),
			}},
		}
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{
				mkMemberAttached("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMemberAttached("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-2"),
			},
			nil, []v1alpha1.ReplicatedVolumeDatameshTransition{enableT},
		)
		rv.Spec.MaxAttachments = 2
		rv.Status.Datamesh.Multiattach = true
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRReady("rv-1-0", "node-1", 6),
			mkRVRReady("rv-1-1", "node-2", 6),
		}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-1"), mkRVA("rva-2", "node-2")}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, rvas, FeatureFlags{})

		Expect(changed).To(BeTrue())
		// EnableMultiattach should be completed.
		for _, t := range rv.Status.DatameshTransitions {
			Expect(t.Type).NotTo(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeEnableMultiattach))
		}
	})

	It("EnableMultiattach blocked while ShadowDiskful not confirmed", func() {
		enableT := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:   v1alpha1.ReplicatedVolumeDatameshTransitionTypeEnableMultiattach,
			Group:  v1alpha1.ReplicatedVolumeDatameshTransitionGroupMultiattach,
			PlanID: "enable-multiattach/v1",
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{
				Name: "Enable multiattach", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
				DatameshRevision: 6, StartedAt: ptr.To(metav1.Now()),
			}},
		}
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{
				mkMemberAttached("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeShadowDiskful, "node-2"),
			},
			nil, []v1alpha1.ReplicatedVolumeDatameshTransition{enableT},
		)
		rv.Spec.MaxAttachments = 2
		rv.Status.Datamesh.Multiattach = true
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRReady("rv-1-0", "node-1", 6),
			mkRVRReady("rv-1-1", "node-2", 5), // ShadowDiskful not confirmed
		}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-1")}

		ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, rvas, FeatureFlags{})

		// EnableMultiattach should NOT be completed.
		hasEnable := false
		for _, t := range rv.Status.DatameshTransitions {
			if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeEnableMultiattach {
				hasEnable = true
			}
		}
		Expect(hasEnable).To(BeTrue())
	})

	It("DisableMultiattach completes when all members confirm", func() {
		disableT := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:   v1alpha1.ReplicatedVolumeDatameshTransitionTypeDisableMultiattach,
			Group:  v1alpha1.ReplicatedVolumeDatameshTransitionGroupMultiattach,
			PlanID: "disable-multiattach/v1",
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{
				Name: "Disable multiattach", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
				DatameshRevision: 6, StartedAt: ptr.To(metav1.Now()),
			}},
		}
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
			},
			nil, []v1alpha1.ReplicatedVolumeDatameshTransition{disableT},
		)
		rv.Status.Datamesh.Multiattach = false
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVRReady("rv-1-0", "node-1", 6)}

		changed, _ := ProcessTransitions(context.Background(), rv, nil, rvrs, nil, FeatureFlags{})

		Expect(changed).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Multiattach
//

var _ = Describe("Multiattach", func() {
	It("enables when 2 intents", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-2"),
			},
			nil, nil,
		)
		rv.Spec.MaxAttachments = 2
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRReady("rv-1-0", "node-1", 5),
			mkRVRReady("rv-1-1", "node-2", 5),
		}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-1"), mkRVA("rva-2", "node-2")}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, rvas, FeatureFlags{})

		Expect(changed).To(BeTrue())
		hasEnable := false
		for _, t := range rv.Status.DatameshTransitions {
			if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeEnableMultiattach {
				hasEnable = true
			}
		}
		Expect(hasEnable).To(BeTrue())
		Expect(rv.Status.Datamesh.Multiattach).To(BeTrue())
	})

	It("disables when single intent", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")},
			nil, nil,
		)
		rv.Status.Datamesh.Multiattach = true
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVRReady("rv-1-0", "node-1", 5)}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-1")}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1"), rvrs, rvas, FeatureFlags{})

		Expect(changed).To(BeTrue())
		hasDisable := false
		for _, t := range rv.Status.DatameshTransitions {
			if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeDisableMultiattach {
				hasDisable = true
			}
		}
		Expect(hasDisable).To(BeTrue())
	})

	It("does not disable while potentiallyAttached > 1", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMemberAttached("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMemberAttached("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-2"),
			},
			nil, nil,
		)
		rv.Spec.MaxAttachments = 2
		rv.Status.Datamesh.Multiattach = true
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRReady("rv-1-0", "node-1", 5),
			mkRVRReady("rv-1-1", "node-2", 5),
		}
		// Only 1 active RVA (node-1) → intended=1, but node-2 still attached.
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-1")}

		ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, rvas, FeatureFlags{})

		// Should NOT create DisableMultiattach — node-2 still potentiallyAttached.
		for _, t := range rv.Status.DatameshTransitions {
			Expect(t.Type).NotTo(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeDisableMultiattach))
		}
	})

	It("does not create duplicate EnableMultiattach", func() {
		enableT := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:   v1alpha1.ReplicatedVolumeDatameshTransitionTypeEnableMultiattach,
			Group:  v1alpha1.ReplicatedVolumeDatameshTransitionGroupMultiattach,
			PlanID: "enable-multiattach/v1",
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{
				Name: "Enable multiattach", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
				DatameshRevision: 5, StartedAt: ptr.To(metav1.Now()),
			}},
		}
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-2"),
			},
			nil, []v1alpha1.ReplicatedVolumeDatameshTransition{enableT},
		)
		rv.Spec.MaxAttachments = 2
		rv.Status.Datamesh.Multiattach = true
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRReady("rv-1-0", "node-1", 4),
			mkRVRReady("rv-1-1", "node-2", 4),
		}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-1"), mkRVA("rva-2", "node-2")}

		ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, rvas, FeatureFlags{})

		enableCount := 0
		for _, t := range rv.Status.DatameshTransitions {
			if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeEnableMultiattach {
				enableCount++
			}
		}
		Expect(enableCount).To(Equal(1))
	})

	It("guard: maxAttachments=1 blocks EnableMultiattach", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-2"),
			},
			nil, nil,
		)
		// MaxAttachments=1 (default) — dispatcher dispatches EnableMultiattach,
		// but guardMaxAttachmentsAllowsMultiattach blocks.
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRReady("rv-1-0", "node-1", 5),
			mkRVRReady("rv-1-1", "node-2", 5),
		}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-1"), mkRVA("rva-2", "node-2")}

		ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, rvas, FeatureFlags{})

		// No EnableMultiattach created.
		for _, t := range rv.Status.DatameshTransitions {
			Expect(t.Type).NotTo(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeEnableMultiattach))
		}
		Expect(rv.Status.Datamesh.Multiattach).To(BeFalse())
	})

	It("guard: potentiallyAttached > 1 blocks DisableMultiattach", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMemberAttached("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMemberAttached("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-2"),
			},
			nil, nil,
		)
		rv.Spec.MaxAttachments = 2
		rv.Status.Datamesh.Multiattach = true
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRReady("rv-1-0", "node-1", 5),
			mkRVRReady("rv-1-1", "node-2", 5),
		}
		// No RVAs → dispatcher wants DisableMultiattach, but guard blocks (2 potentiallyAttached).

		ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{})

		for _, t := range rv.Status.DatameshTransitions {
			Expect(t.Type).NotTo(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeDisableMultiattach))
		}
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Combined
//

var _ = Describe("Attachment combined", func() {
	It("single-attach switch: detach + new node queued", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMemberAttached("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-2"),
			},
			nil, nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRReady("rv-1-0", "node-1", 5),
			mkRVRReady("rv-1-1", "node-2", 5),
		}
		// RVA only on node-2 → detach node-1, attach node-2.
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-2", "node-2")}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, rvas, FeatureFlags{})

		Expect(changed).To(BeTrue())
		// Detach should be created for node-1.
		hasDetach := false
		for _, t := range rv.Status.DatameshTransitions {
			if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach && t.ReplicaName == "rv-1-0" {
				hasDetach = true
			}
		}
		Expect(hasDetach).To(BeTrue())
	})

	It("no-op when settled", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{mkMemberAttached("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")},
			nil, nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkRVRReady("rv-1-0", "node-1", 5)}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-1")}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1"), rvrs, rvas, FeatureFlags{})

		Expect(changed).To(BeFalse())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})

	It("empty state: no members, no RVAs", func() {
		rv := mkRV(5, nil, nil, nil)

		changed, _ := ProcessTransitions(context.Background(), rv, nil, nil, nil, FeatureFlags{})

		Expect(changed).To(BeFalse())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})

	It("settle + dispatch in same cycle", func() {
		// Existing Attach transition confirmed → will settle.
		// Single-attach mode: node-1 already attached, node-2 wants attach but node-1 must detach first.
		attachT := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment,
			ReplicaName: "rv-1-0", PlanID: "attach/v1",
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{
				Name: "Attach", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
				DatameshRevision: 5, StartedAt: ptr.To(metav1.Now()),
			}},
		}
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMemberAttached("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeAccess, "node-2"),
			},
			nil, []v1alpha1.ReplicatedVolumeDatameshTransition{attachT},
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRReady("rv-1-0", "node-1", 5), // confirmed
			mkRVRReady("rv-1-1", "node-2", 5),
		}
		// Only node-2 has RVA (node-1 should detach).
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-2", "node-2")}

		changed, _ := ProcessTransitions(context.Background(), rv, mkRSP("node-1", "node-2"), rvrs, rvas, FeatureFlags{})

		Expect(changed).To(BeTrue())
		// Old Attach settled. Detach created for node-1. Node-2 Attach created
		// (single-attach: node-1 detaching frees slot after Detach confirmed, but
		// potentiallyAttached still contains node-1 so node-2 is blocked by slot/multiattach).
		hasDetachNode1 := false
		for _, t := range rv.Status.DatameshTransitions {
			if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach && t.ReplicaName == "rv-1-0" {
				hasDetachNode1 = true
			}
		}
		Expect(hasDetachNode1).To(BeTrue())
	})

	It("diagnostic error in progress message", func() {
		attachT := v1alpha1.ReplicatedVolumeDatameshTransition{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupAttachment,
			ReplicaName: "rv-1-0", PlanID: "attach/v1",
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{
				Name: "Attach", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
				DatameshRevision: 6, StartedAt: ptr.To(metav1.Now()),
			}},
		}
		rv := mkRV(6,
			[]v1alpha1.DatameshMember{mkMemberAttached("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1")},
			nil, []v1alpha1.ReplicatedVolumeDatameshTransition{attachT},
		)
		// RVR with DRBDConfigured=False + ConfigurationFailed.
		rvr := mkRVR("rv-1-0", "node-1", 5) // not confirmed
		rvr.Generation = 1
		rvr.Status.Quorum = ptr.To(true)
		rvr.Status.Conditions = []metav1.Condition{
			{Type: v1alpha1.ReplicatedVolumeReplicaCondReadyType, Status: metav1.ConditionTrue, Reason: "Ready", ObservedGeneration: 1},
			{Type: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType, Status: metav1.ConditionFalse, Reason: "ConfigurationFailed", ObservedGeneration: 1},
		}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-1")}

		ProcessTransitions(context.Background(), rv, mkRSP("node-1"), []*v1alpha1.ReplicatedVolumeReplica{rvr}, rvas, FeatureFlags{})

		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Steps[0].Message).To(ContainSubstring("Errors"))
		Expect(rv.Status.DatameshTransitions[0].Steps[0].Message).To(ContainSubstring("ConfigurationFailed"))
	})
})
