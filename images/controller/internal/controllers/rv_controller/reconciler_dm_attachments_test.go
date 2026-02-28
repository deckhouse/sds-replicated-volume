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

package rvcontroller

import (
	"cmp"
	"context"
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/reconciliation/flow"
)

// testEnsureDatameshAttachments is a test-only wrapper that preserves the old
// two-return-value call convention for ensureDatameshAttachments.
func testEnsureDatameshAttachments(
	ctx context.Context,
	rv *v1alpha1.ReplicatedVolume,
	rvrs []*v1alpha1.ReplicatedVolumeReplica,
	rvas []*v1alpha1.ReplicatedVolumeAttachment,
	rsp *rspView,
) (*attachmentsSummary, flow.EnsureOutcome) {
	var atts *attachmentsSummary
	outcome := ensureDatameshAttachments(ctx, rv, rvrs, rvas, rsp, &atts)
	return atts, outcome
}

// ──────────────────────────────────────────────────────────────────────────────
// Shared test helpers
//

func mkAttachMember(name, nodeName string, attached bool) v1alpha1.DatameshMember {
	return v1alpha1.DatameshMember{
		Name:      name,
		Type:      v1alpha1.DatameshMemberTypeDiskful,
		NodeName:  nodeName,
		Attached:  attached,
		Addresses: []v1alpha1.DRBDResourceAddressStatus{{SystemNetworkName: "default"}},
	}
}

// mkAttachRV creates an RV suitable for attachment tests:
// has finalizer, Configuration set, MaxAttachments=1, not deleting.
func mkAttachRV(members []v1alpha1.DatameshMember, rev int64) *v1alpha1.ReplicatedVolume {
	return &v1alpha1.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{
			Finalizers: []string{v1alpha1.RVControllerFinalizer},
		},
		Spec: v1alpha1.ReplicatedVolumeSpec{
			MaxAttachments: 1,
		},
		Status: v1alpha1.ReplicatedVolumeStatus{
			Configuration:    &v1alpha1.ReplicatedVolumeConfiguration{},
			DatameshRevision: rev,
			Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
				Members: members,
			},
		},
	}
}

func mkAttachRVR(name, nodeName string, ready bool) *v1alpha1.ReplicatedVolumeReplica {
	rvr := &v1alpha1.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: nodeName},
	}
	if ready {
		rvr.Status.Conditions = []metav1.Condition{
			{Type: v1alpha1.ReplicatedVolumeReplicaCondReadyType, Status: metav1.ConditionTrue, Reason: "Ready"},
		}
		rvr.Status.Quorum = ptr.To(true)
	}
	return rvr
}

func mkAttachRVRWithRev(name, nodeName string, dmRev int64) *v1alpha1.ReplicatedVolumeReplica {
	rvr := mkAttachRVR(name, nodeName, true)
	rvr.Status.DatameshRevision = dmRev
	return rvr
}

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func mkAttachRVA(name, nodeName string) *v1alpha1.ReplicatedVolumeAttachment {
	return &v1alpha1.ReplicatedVolumeAttachment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			CreationTimestamp: metav1.Time{Time: t0},
		},
		Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
			ReplicatedVolumeName: "rv-1",
			NodeName:             nodeName,
		},
	}
}

func mkAttachRVAAt(name, nodeName string, created time.Time) *v1alpha1.ReplicatedVolumeAttachment {
	rva := mkAttachRVA(name, nodeName)
	rva.CreationTimestamp = metav1.Time{Time: created}
	return rva
}

func mkAttachDeletingRVA(name, nodeName string) *v1alpha1.ReplicatedVolumeAttachment {
	rva := mkAttachRVA(name, nodeName)
	rva.DeletionTimestamp = ptr.To(metav1.Now())
	return rva
}

// mkAttachRSP creates a minimal rspView where all given nodes are eligible (NodeReady + AgentReady).
func mkAttachRSP(nodeNames ...string) *rspView {
	nodes := make([]v1alpha1.ReplicatedStoragePoolEligibleNode, len(nodeNames))
	for i, n := range nodeNames {
		nodes[i] = v1alpha1.ReplicatedStoragePoolEligibleNode{
			NodeName:   n,
			NodeReady:  true,
			AgentReady: true,
		}
	}
	slices.SortFunc(nodes, func(a, b v1alpha1.ReplicatedStoragePoolEligibleNode) int {
		return cmp.Compare(a.NodeName, b.NodeName)
	})
	return &rspView{EligibleNodes: nodes}
}

// ──────────────────────────────────────────────────────────────────────────────
// Tests: buildAttachmentsSummary
//

var _ = Describe("buildAttachmentsSummary", func() {
	It("returns empty for no members and no RVAs", func() {
		rv := mkAttachRV(nil, 10)
		atts := buildAttachmentsSummary(rv, nil, nil)

		Expect(atts.attachmentStates).To(BeEmpty())
	})

	It("includes node with member and RVA, fills pointers", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", false),
		}, 10)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkAttachRVR("rv-1-0", "node-1", true)}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkAttachRVA("rva-1", "node-1")}

		atts := buildAttachmentsSummary(rv, rvrs, rvas)

		Expect(atts.attachmentStates).To(HaveLen(1))
		as := &atts.attachmentStates[0]
		Expect(as.nodeName).To(Equal("node-1"))
		Expect(as.member).NotTo(BeNil())
		Expect(as.member.Name).To(Equal("rv-1-0"))
		Expect(as.rvr).NotTo(BeNil())
		Expect(as.rvr.Name).To(Equal("rv-1-0"))
		Expect(as.rvas).To(HaveLen(1))
		Expect(as.intent).To(Equal(attachmentIntent(""))) // not yet computed
	})

	It("includes ALL RVA nodes (not just top-N)", func() {
		rv := mkAttachRV(nil, 10) // maxAttachments=1, no members
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{
			mkAttachRVA("rva-1", "node-1"),
			mkAttachRVA("rva-2", "node-2"),
			mkAttachRVA("rva-3", "node-3"),
		}

		atts := buildAttachmentsSummary(rv, nil, rvas)

		Expect(atts.attachmentStates).To(HaveLen(3))
	})

	It("groups multiple RVAs on same node into one attachmentState", func() {
		rv := mkAttachRV(nil, 10)
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{
			mkAttachRVA("rva-1", "node-1"),
			mkAttachRVA("rva-2", "node-1"),
		}

		atts := buildAttachmentsSummary(rv, nil, rvas)

		Expect(atts.attachmentStates).To(HaveLen(1))
		Expect(atts.attachmentStates[0].rvas).To(HaveLen(2))
	})

	It("sorts nodes by NodeName", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-b", false),
		}, 10)
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkAttachRVA("rva-1", "node-a")}

		atts := buildAttachmentsSummary(rv, nil, rvas)

		Expect(atts.attachmentStates).To(HaveLen(2))
		Expect(atts.attachmentStates[0].nodeName).To(Equal("node-a"))
		Expect(atts.attachmentStates[1].nodeName).To(Equal("node-b"))
	})

	It("populates attachmentStateByReplicaID index", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-3", "node-1", false),
		}, 10)

		atts := buildAttachmentsSummary(rv, nil, nil)

		Expect(atts.attachmentStateByReplicaID[3]).NotTo(BeNil())
		Expect(atts.attachmentStateByReplicaID[3].nodeName).To(Equal("node-1"))
		Expect(atts.attachmentStateByReplicaID[0]).To(BeNil())
	})

	It("findAttachmentStateByNodeName works with binary search", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-a", false),
			mkAttachMember("rv-1-1", "node-b", false),
		}, 10)

		atts := buildAttachmentsSummary(rv, nil, nil)

		Expect(atts.findAttachmentStateByNodeName("node-a")).NotTo(BeNil())
		Expect(atts.findAttachmentStateByNodeName("node-b")).NotTo(BeNil())
		Expect(atts.findAttachmentStateByNodeName("node-c")).To(BeNil())
	})

	It("member with no matching RVR has rvr=nil", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", false),
		}, 10)

		atts := buildAttachmentsSummary(rv, nil, nil)

		as := &atts.attachmentStates[0]
		Expect(as.member).NotTo(BeNil())
		Expect(as.rvr).To(BeNil())
	})

	It("RVA-only node finds RVR by NodeName", func() {
		rv := mkAttachRV(nil, 10) // no members
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkAttachRVR("rv-1-0", "node-1", true)}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkAttachRVA("rva-1", "node-1")}

		atts := buildAttachmentsSummary(rv, rvrs, rvas)

		Expect(atts.attachmentStates).To(HaveLen(1))
		as := &atts.attachmentStates[0]
		Expect(as.member).To(BeNil())
		Expect(as.rvr).NotTo(BeNil())
		Expect(as.rvr.Name).To(Equal("rv-1-0"))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Tests: intent computation
//

var _ = Describe("ensureDatameshAttachments intent", func() {
	ctx := context.Background()

	It("sets Attach for intended node with active RVA", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", false),
		}, 10)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkAttachRVR("rv-1-0", "node-1", true)}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkAttachRVA("rva-1", "node-1")}

		atts, _ := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1", "node-2", "node-3", "node-a", "node-b"))

		as := atts.findAttachmentStateByNodeName("node-1")
		Expect(as).NotTo(BeNil())
		Expect(as.intent).To(Equal(attachmentIntentAttach))
	})

	It("sets no intent for attached node with active RVA (settled)", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", true),
		}, 10)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkAttachRVR("rv-1-0", "node-1", true)}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkAttachRVA("rva-1", "node-1")}

		atts, outcome := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1", "node-2", "node-3", "node-a", "node-b"))

		as := atts.findAttachmentStateByNodeName("node-1")
		// potentiallyAttached + active RVA → intent=Attach, but fully settled = Attach (already has slot).
		Expect(as.intent).To(Equal(attachmentIntentAttach))
		Expect(outcome.DidChange()).To(BeFalse())
	})

	It("settled attached node keeps Attached condition when RV is deleting", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", true),
		}, 10)
		rv.DeletionTimestamp = ptr.To(metav1.Now())
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkAttachRVR("rv-1-0", "node-1", true)}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkAttachRVA("rva-1", "node-1")}

		atts, outcome := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1", "node-2", "node-3", "node-a", "node-b"))

		as := atts.findAttachmentStateByNodeName("node-1")
		Expect(as.intent).To(Equal(attachmentIntentAttach))
		Expect(as.conditionReason).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonAttached))
		Expect(as.conditionMessage).To(ContainSubstring("attached and ready to serve I/O"))
		Expect(as.conditionMessage).To(ContainSubstring("pending deletion"))
		Expect(outcome.DidChange()).To(BeFalse())
	})

	It("sets Detach for attached node without active RVA", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", true),
		}, 10)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkAttachRVR("rv-1-0", "node-1", true)}

		atts, _ := testEnsureDatameshAttachments(ctx, rv, rvrs, nil, mkAttachRSP("node-1", "node-2", "node-3", "node-a", "node-b"))

		as := atts.findAttachmentStateByNodeName("node-1")
		Expect(as.intent).To(Equal(attachmentIntentDetach))
	})

	It("sets Pending when slot is full", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", true),
			mkAttachMember("rv-1-1", "node-2", false),
		}, 10)
		// maxAttachments=1, node-1 already attached → occupies slot.
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkAttachRVR("rv-1-0", "node-1", true),
			mkAttachRVR("rv-1-1", "node-2", true),
		}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{
			mkAttachRVA("rva-1", "node-1"),
			mkAttachRVA("rva-2", "node-2"),
		}

		atts, _ := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1", "node-2", "node-3", "node-a", "node-b"))

		Expect(atts.findAttachmentStateByNodeName("node-1").intent).To(Equal(attachmentIntentAttach))
		as2 := atts.findAttachmentStateByNodeName("node-2")
		Expect(as2.intent).To(Equal(attachmentIntentPending))
		Expect(as2.conditionMessage).To(ContainSubstring("Waiting for attachment slot"))
	})

	It("already-attached node keeps slot over older RVA on different node", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", true),
			mkAttachMember("rv-1-1", "node-2", false),
		}, 10)
		// maxAttachments=1, node-1 attached.
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkAttachRVR("rv-1-0", "node-1", true),
			mkAttachRVR("rv-1-1", "node-2", true),
		}
		// node-2 RVA is OLDER, but node-1 is already attached → keeps slot.
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{
			mkAttachRVAAt("rva-1", "node-1", t0.Add(time.Second)), // newer
			mkAttachRVAAt("rva-2", "node-2", t0),                  // older
		}

		atts, _ := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1", "node-2", "node-3", "node-a", "node-b"))

		Expect(atts.findAttachmentStateByNodeName("node-1").intent).To(Equal(attachmentIntentAttach))
		Expect(atts.findAttachmentStateByNodeName("node-2").intent).To(Equal(attachmentIntentPending))
	})

	It("maxAttachments decrease: over-limit nodes keep intent=Attach", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", true),
			mkAttachMember("rv-1-1", "node-2", true),
		}, 10)
		rv.Spec.MaxAttachments = 1 // decreased from 2 to 1
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkAttachRVR("rv-1-0", "node-1", true),
			mkAttachRVR("rv-1-1", "node-2", true),
		}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{
			mkAttachRVA("rva-1", "node-1"),
			mkAttachRVA("rva-2", "node-2"),
		}

		atts, _ := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1", "node-2", "node-3", "node-a", "node-b"))

		// Both keep Attach — no forced detach.
		Expect(atts.findAttachmentStateByNodeName("node-1").intent).To(Equal(attachmentIntentAttach))
		Expect(atts.findAttachmentStateByNodeName("node-2").intent).To(Equal(attachmentIntentAttach))
		Expect(atts.intendedAttachments.Len()).To(Equal(2))
	})

	It("maxAttachments=2 allows 2nd node to attach", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", true),
			mkAttachMember("rv-1-1", "node-2", false),
		}, 10)
		rv.Spec.MaxAttachments = 2
		rv.Status.Datamesh.Multiattach = true // already enabled
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkAttachRVR("rv-1-0", "node-1", true),
			mkAttachRVR("rv-1-1", "node-2", true),
		}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{
			mkAttachRVA("rva-1", "node-1"),
			mkAttachRVA("rva-2", "node-2"),
		}

		atts, outcome := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1", "node-2", "node-3", "node-a", "node-b"))

		Expect(outcome.Error()).To(BeNil())
		Expect(outcome.DidChange()).To(BeTrue())
		Expect(atts.findAttachmentStateByNodeName("node-1").intent).To(Equal(attachmentIntentAttach))
		Expect(atts.findAttachmentStateByNodeName("node-2").intent).To(Equal(attachmentIntentAttach))
		// Attach transition created for node-2.
		Expect(rv.Status.Datamesh.Members[1].Attached).To(BeTrue())
	})

	It("FIFO ordering for new slots among unattached nodes", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-a", false),
			mkAttachMember("rv-1-1", "node-b", false),
		}, 10)
		// maxAttachments=1, both unattached, node-b has older RVA.
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkAttachRVR("rv-1-0", "node-a", true),
			mkAttachRVR("rv-1-1", "node-b", true),
		}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{
			mkAttachRVAAt("rva-a", "node-a", t0.Add(time.Second)),
			mkAttachRVAAt("rva-b", "node-b", t0),
		}

		atts, _ := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1", "node-2", "node-3", "node-a", "node-b"))

		// node-b gets the slot (older RVA).
		Expect(atts.findAttachmentStateByNodeName("node-b").intent).To(Equal(attachmentIntentAttach))
		Expect(atts.findAttachmentStateByNodeName("node-a").intent).To(Equal(attachmentIntentPending))
	})

	It("queues when node not in eligibleNodes", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", false),
		}, 10)
		rv.Status.Configuration.ReplicatedStoragePoolName = "pool-1"
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkAttachRVR("rv-1-0", "node-1", true)}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkAttachRVA("rva-1", "node-1")}

		// RSP does NOT include node-1.
		atts, _ := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-2"))

		as := atts.findAttachmentStateByNodeName("node-1")
		Expect(as.intent).To(Equal(attachmentIntentPending))
		Expect(as.conditionMessage).To(ContainSubstring("not eligible"))
	})

	It("queues when node is not ready", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", false),
		}, 10)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkAttachRVR("rv-1-0", "node-1", true)}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkAttachRVA("rva-1", "node-1")}
		rsp := &rspView{EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1", NodeReady: false, AgentReady: true},
		}}

		atts, _ := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, rsp)

		as := atts.findAttachmentStateByNodeName("node-1")
		Expect(as.intent).To(Equal(attachmentIntentPending))
		Expect(as.conditionMessage).To(ContainSubstring("Node is not ready"))
	})

	It("queues when agent is not ready", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", false),
		}, 10)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkAttachRVR("rv-1-0", "node-1", true)}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkAttachRVA("rva-1", "node-1")}
		rsp := &rspView{EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{
			{NodeName: "node-1", NodeReady: true, AgentReady: false},
		}}

		atts, _ := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, rsp)

		as := atts.findAttachmentStateByNodeName("node-1")
		Expect(as.intent).To(Equal(attachmentIntentPending))
		Expect(as.conditionMessage).To(ContainSubstring("Agent is not ready"))
	})

	It("queues when no member but has RVR with DatameshRequest", func() {
		// Background diskful member on another node to satisfy global quorum.
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-9", "node-bg", false),
		}, 10)
		rv.Status.DatameshReplicaRequests = []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
			{Name: "rv-1-0", Message: "scheduling in progress", Request: v1alpha1.DatameshMembershipRequest{
				Operation: v1alpha1.DatameshMembershipRequestOperationJoin, Type: v1alpha1.ReplicaTypeAccess,
			}},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkAttachRVR("rv-1-0", "node-1", true),
			mkAttachRVR("rv-1-9", "node-bg", true),
		}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkAttachRVA("rva-1", "node-1")}

		atts, _ := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1"))

		as := atts.findAttachmentStateByNodeName("node-1")
		Expect(as.intent).To(Equal(attachmentIntentPending))
		Expect(as.conditionMessage).To(ContainSubstring("join datamesh"))
		Expect(as.conditionMessage).To(ContainSubstring("scheduling in progress"))
	})

	It("queues when no member, has RVR with Ready condition message", func() {
		// Background diskful member on another node to satisfy global quorum.
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-9", "node-bg", false),
		}, 10)
		rvr := mkAttachRVR("rv-1-0", "node-1", false)
		rvr.Status.Conditions = []metav1.Condition{
			{Type: v1alpha1.ReplicatedVolumeReplicaCondReadyType, Status: metav1.ConditionFalse, Reason: "NotReady", Message: "waiting for quorum"},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			rvr,
			mkAttachRVR("rv-1-9", "node-bg", true),
		}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkAttachRVA("rva-1", "node-1")}

		atts, _ := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1"))

		as := atts.findAttachmentStateByNodeName("node-1")
		Expect(as.intent).To(Equal(attachmentIntentPending))
		Expect(as.conditionMessage).To(ContainSubstring("NotReady"))
		Expect(as.conditionMessage).To(ContainSubstring("waiting for quorum"))
	})

	It("queues when no member, has RVR without Ready message", func() {
		// Background diskful member on another node to satisfy global quorum.
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-9", "node-bg", false),
		}, 10)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkAttachRVR("rv-1-0", "node-1", true),
			mkAttachRVR("rv-1-9", "node-bg", true),
		}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkAttachRVA("rva-1", "node-1")}

		atts, _ := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1"))

		as := atts.findAttachmentStateByNodeName("node-1")
		Expect(as.intent).To(Equal(attachmentIntentPending))
		Expect(as.conditionMessage).To(ContainSubstring("join datamesh"))
	})

	It("queues all active-RVA nodes when quorum not satisfied", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", false),
		}, 10)
		rv.Status.Datamesh.QuorumMinimumRedundancy = 1
		// RVR exists but has no quorum.
		rvr := mkAttachRVR("rv-1-0", "node-1", true)
		rvr.Status.Quorum = ptr.To(false)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkAttachRVA("rva-1", "node-1")}

		atts, _ := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1"))

		as := atts.findAttachmentStateByNodeName("node-1")
		Expect(as.intent).To(Equal(attachmentIntentPending))
		Expect(as.conditionMessage).To(ContainSubstring("Quorum not satisfied"))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Tests: completion
//

var _ = Describe("ensureDatameshAttachments completion", func() {
	ctx := context.Background()

	It("completes Attach transition when replica confirms revision", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", true),
		}, 10)
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{
			{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach, ReplicaName: "rv-1-0", Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, DatameshRevision: 10, StartedAt: ptr.To(metav1.Now())}}},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkAttachRVRWithRev("rv-1-0", "node-1", 10)}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkAttachRVA("rva-1", "node-1")}

		_, outcome := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1", "node-2", "node-3", "node-a", "node-b"))

		Expect(outcome.Error()).To(BeNil())
		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})

	It("completes Detach transition when replica confirms revision", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", false),
		}, 10)
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{
			{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach, ReplicaName: "rv-1-0", Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, DatameshRevision: 10, StartedAt: ptr.To(metav1.Now())}}},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkAttachRVRWithRev("rv-1-0", "node-1", 10)}

		_, outcome := testEnsureDatameshAttachments(ctx, rv, rvrs, nil, mkAttachRSP("node-1", "node-2", "node-3", "node-a", "node-b"))

		Expect(outcome.Error()).To(BeNil())
		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})

	It("completes EnableMultiattach when all members confirm", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", true),
			mkAttachMember("rv-1-1", "node-2", false),
		}, 10)
		rv.Spec.MaxAttachments = 2
		rv.Status.Datamesh.Multiattach = true
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{
			{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeEnableMultiattach, Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, DatameshRevision: 10, StartedAt: ptr.To(metav1.Now())}}},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkAttachRVRWithRev("rv-1-0", "node-1", 10),
			mkAttachRVRWithRev("rv-1-1", "node-2", 10),
		}

		_, outcome := testEnsureDatameshAttachments(ctx, rv, rvrs, nil, mkAttachRSP("node-1", "node-2", "node-3", "node-a", "node-b"))

		Expect(outcome.Error()).To(BeNil())
		Expect(outcome.DidChange()).To(BeTrue())
		for _, t := range rv.Status.DatameshTransitions {
			Expect(t.Type).NotTo(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeEnableMultiattach))
		}
	})

	It("blocks EnableMultiattach completion while potentiallyAttached Access member has not confirmed", func() {
		// node-1: Diskful, attached — always in mustConfirm (via Diskful).
		// node-2: Access, detaching (Attached=false, Detach transition pending) — potentiallyAttached.
		//   NOT in mustConfirm via (Diskful || Attached), only via potentiallyAttached union.
		// node-2 has NOT confirmed EnableMultiattach revision → transition must stay pending.
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", true),
			{
				Name:      "rv-1-1",
				Type:      v1alpha1.DatameshMemberTypeAccess,
				NodeName:  "node-2",
				Attached:  false,
				Addresses: []v1alpha1.DRBDResourceAddressStatus{{SystemNetworkName: "default"}},
			},
		}, 11)
		rv.Spec.MaxAttachments = 2
		rv.Status.Datamesh.Multiattach = true
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{
			{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeEnableMultiattach, Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, DatameshRevision: 10, StartedAt: ptr.To(metav1.Now())}}},
			{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach, ReplicaName: "rv-1-1", Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, DatameshRevision: 11, StartedAt: ptr.To(metav1.Now())}}},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkAttachRVRWithRev("rv-1-0", "node-1", 10), // confirmed rev 10
			mkAttachRVRWithRev("rv-1-1", "node-2", 5),  // NOT confirmed rev 10
		}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkAttachRVA("rva-1", "node-1")}

		_, outcome := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1", "node-2"))

		Expect(outcome.Error()).To(BeNil())
		// EnableMultiattach should NOT be completed.
		hasEnable := false
		for _, t := range rv.Status.DatameshTransitions {
			if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeEnableMultiattach {
				hasEnable = true
			}
		}
		Expect(hasEnable).To(BeTrue(), "EnableMultiattach must stay pending while potentiallyAttached Access member has not confirmed")
	})

	It("completes EnableMultiattach when potentiallyAttached Access member confirms", func() {
		// Same setup as above, but node-2 has confirmed the EnableMultiattach revision.
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", true),
			{
				Name:      "rv-1-1",
				Type:      v1alpha1.DatameshMemberTypeAccess,
				NodeName:  "node-2",
				Attached:  false,
				Addresses: []v1alpha1.DRBDResourceAddressStatus{{SystemNetworkName: "default"}},
			},
		}, 11)
		rv.Spec.MaxAttachments = 2
		rv.Status.Datamesh.Multiattach = true
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{
			{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeEnableMultiattach, Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, DatameshRevision: 10, StartedAt: ptr.To(metav1.Now())}}},
			{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach, ReplicaName: "rv-1-1", Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, DatameshRevision: 11, StartedAt: ptr.To(metav1.Now())}}},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkAttachRVRWithRev("rv-1-0", "node-1", 11), // confirmed both
			mkAttachRVRWithRev("rv-1-1", "node-2", 10), // confirmed rev 10 (EnableMultiattach), NOT rev 11 (Detach)
		}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkAttachRVA("rva-1", "node-1")}

		_, outcome := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1", "node-2"))

		Expect(outcome.Error()).To(BeNil())
		Expect(outcome.DidChange()).To(BeTrue())
		// EnableMultiattach should be completed.
		for _, t := range rv.Status.DatameshTransitions {
			Expect(t.Type).NotTo(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeEnableMultiattach))
		}
		// Detach should still be pending (node-2 hasn't confirmed rev 11).
		hasDetach := false
		for _, t := range rv.Status.DatameshTransitions {
			if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach {
				hasDetach = true
			}
		}
		Expect(hasDetach).To(BeTrue(), "Detach should still be pending")
	})

	It("blocks EnableMultiattach completion while ShadowDiskful member has not confirmed", func() {
		// ShadowDiskful has HasBackingVolume()=true → included in mustConfirm set.
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", true),
			{Name: "rv-1-2", Type: v1alpha1.DatameshMemberTypeShadowDiskful, NodeName: "node-3",
				Addresses: []v1alpha1.DRBDResourceAddressStatus{{SystemNetworkName: "default"}}},
		}, 10)
		rv.Spec.MaxAttachments = 2
		rv.Status.Datamesh.Multiattach = true
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{
			{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeEnableMultiattach, Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, DatameshRevision: 10, StartedAt: ptr.To(metav1.Now())}}},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkAttachRVRWithRev("rv-1-0", "node-1", 10),
			mkAttachRVRWithRev("rv-1-2", "node-3", 5), // ShadowDiskful NOT confirmed
		}

		_, outcome := testEnsureDatameshAttachments(ctx, rv, rvrs, nil, mkAttachRSP("node-1", "node-3"))

		Expect(outcome.Error()).To(BeNil())
		// EnableMultiattach should NOT be completed because ShadowDiskful has not confirmed.
		hasEnable := false
		for _, t := range rv.Status.DatameshTransitions {
			if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeEnableMultiattach {
				hasEnable = true
			}
		}
		Expect(hasEnable).To(BeTrue(), "EnableMultiattach must stay pending while ShadowDiskful member has not confirmed")
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Tests: attach
//

var _ = Describe("ensureDatameshAttachments attach", func() {
	ctx := context.Background()

	It("creates Attach transition for intended node", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", false),
		}, 10)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkAttachRVR("rv-1-0", "node-1", true)}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkAttachRVA("rva-1", "node-1")}

		atts, outcome := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1", "node-2", "node-3", "node-a", "node-b"))

		Expect(outcome.Error()).To(BeNil())
		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rv.Status.Datamesh.Members[0].Attached).To(BeTrue())
		Expect(rv.Status.DatameshRevision).To(Equal(int64(11)))
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach))

		as := atts.findAttachmentStateByNodeName("node-1")
		Expect(as.conditionMessage).To(ContainSubstring("Attaching"))
	})

	It("blocks attach when RV is deleting", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", false),
		}, 10)
		rv.DeletionTimestamp = ptr.To(metav1.Now())
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkAttachRVR("rv-1-0", "node-1", true)}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkAttachRVA("rva-1", "node-1")}

		atts, _ := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1", "node-2", "node-3", "node-a", "node-b"))

		Expect(rv.Status.Datamesh.Members[0].Attached).To(BeFalse())
		as := atts.findAttachmentStateByNodeName("node-1")
		Expect(as.conditionMessage).To(ContainSubstring("deleted"))
	})

	It("blocks attach when RVR is not Ready", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", false),
		}, 10)
		rvr := mkAttachRVR("rv-1-0", "node-1", false)
		rvr.Status.Quorum = ptr.To(true) // quorum OK, but not Ready
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkAttachRVA("rva-1", "node-1")}

		atts, _ := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1", "node-2", "node-3", "node-a", "node-b"))

		Expect(rv.Status.Datamesh.Members[0].Attached).To(BeFalse())
		as := atts.findAttachmentStateByNodeName("node-1")
		Expect(as.conditionMessage).To(ContainSubstring("Ready"))
	})

	It("queues when node has no datamesh member and no RVR", func() {
		// Background diskful member on another node to satisfy global quorum.
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-9", "node-bg", false),
		}, 10)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkAttachRVR("rv-1-9", "node-bg", true)}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkAttachRVA("rva-1", "node-1")}

		atts, _ := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1", "node-2", "node-3", "node-a", "node-b"))

		as := atts.findAttachmentStateByNodeName("node-1")
		Expect(as).NotTo(BeNil())
		Expect(as.intent).To(Equal(attachmentIntentPending))
		Expect(as.conditionMessage).To(ContainSubstring("Waiting for replica on node"))
	})

	It("queues attach when active Detach occupies slot in single-attach mode", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", false), // detaching (member.Attached=false, transition in progress)
			mkAttachMember("rv-1-1", "node-2", false),
		}, 10)
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{
			{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach, ReplicaName: "rv-1-0", Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, DatameshRevision: 10, StartedAt: ptr.To(metav1.Now())}}},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkAttachRVRWithRev("rv-1-0", "node-1", 5),
			mkAttachRVR("rv-1-1", "node-2", true),
		}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkAttachRVA("rva-1", "node-2")}

		atts, _ := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1", "node-2", "node-3", "node-a", "node-b"))

		// node-1 is potentiallyAttached (detach not confirmed) → occupies slot.
		// node-2 gets Pending (no available slot), not Attach.
		Expect(rv.Status.Datamesh.Members[1].Attached).To(BeFalse())
		as := atts.findAttachmentStateByNodeName("node-2")
		Expect(as.intent).To(Equal(attachmentIntentPending))
		Expect(as.conditionMessage).To(ContainSubstring("Waiting for attachment slot"))
	})

	It("blocks attach when AddAccessReplica in progress", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-1", "node-1", false),
		}, 10)
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{
			{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddAccessReplica, ReplicaName: "rv-1-1", Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, DatameshRevision: 10, StartedAt: ptr.To(metav1.Now())}}},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkAttachRVR("rv-1-1", "node-1", true)}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkAttachRVA("rva-1", "node-1")}

		atts, _ := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1", "node-2", "node-3", "node-a", "node-b"))

		Expect(rv.Status.Datamesh.Members[0].Attached).To(BeFalse())
		as := atts.findAttachmentStateByNodeName("node-1")
		Expect(as.conditionMessage).To(ContainSubstring("join datamesh"))
	})

	It("allows attach for ShadowDiskful member with VolumeAccess=Local", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", true), // Diskful voter provides quorum.
			{Name: "rv-1-1", Type: v1alpha1.DatameshMemberTypeShadowDiskful, NodeName: "node-2",
				Addresses: []v1alpha1.DRBDResourceAddressStatus{{SystemNetworkName: "default"}}},
		}, 10)
		rv.Spec.MaxAttachments = 2
		rv.Status.Configuration.VolumeAccess = v1alpha1.VolumeAccessLocal
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkAttachRVR("rv-1-0", "node-1", true),
			mkAttachRVR("rv-1-1", "node-2", true),
		}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkAttachRVA("rva-2", "node-2")}

		atts, outcome := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1", "node-2"))

		Expect(outcome.Error()).To(BeNil())
		Expect(outcome.DidChange()).To(BeTrue())
		// ShadowDiskful has HasBackingVolume()=true → VolumeAccess=Local guard passes.
		as := atts.findAttachmentStateByNodeName("node-2")
		Expect(as.conditionReason).NotTo(Equal(v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonVolumeAccessLocalityNotSatisfied))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Tests: detach
//

var _ = Describe("ensureDatameshAttachments detach", func() {
	ctx := context.Background()

	It("creates Detach transition when no active RVA", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", true),
		}, 10)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkAttachRVR("rv-1-0", "node-1", true)}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkAttachDeletingRVA("rva-1", "node-1")}

		atts, outcome := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1", "node-2", "node-3", "node-a", "node-b"))

		Expect(outcome.Error()).To(BeNil())
		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rv.Status.Datamesh.Members[0].Attached).To(BeFalse())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach))

		as := atts.findAttachmentStateByNodeName("node-1")
		Expect(as.conditionMessage).To(ContainSubstring("Detaching"))
	})

	It("proceeds with detach even when RVR is not Ready", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", true),
		}, 10)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkAttachRVR("rv-1-0", "node-1", false)}

		_, outcome := testEnsureDatameshAttachments(ctx, rv, rvrs, nil, mkAttachRSP("node-1", "node-2", "node-3", "node-a", "node-b"))

		Expect(outcome.Error()).To(BeNil())
		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rv.Status.Datamesh.Members[0].Attached).To(BeFalse())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach))
	})

	It("blocks detach when replica is in use", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", true),
		}, 10)
		rvr := mkAttachRVR("rv-1-0", "node-1", true)
		rvr.Status.Attachment = &v1alpha1.ReplicatedVolumeReplicaStatusAttachment{
			DevicePath: "/dev/drbd1000",
			InUse:      true,
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}

		atts, _ := testEnsureDatameshAttachments(ctx, rv, rvrs, nil, mkAttachRSP("node-1", "node-2", "node-3", "node-a", "node-b"))

		Expect(rv.Status.Datamesh.Members[0].Attached).To(BeTrue())
		as := atts.findAttachmentStateByNodeName("node-1")
		Expect(as.conditionMessage).To(ContainSubstring("in use"))
	})

	It("skips detach when deleting RVA exists on node without datamesh member", func() {
		// node-1: diskful member with quorum (satisfies global quorum).
		// node-2: only a deleting RVA, no member → intent=Detach, but member=nil → skip.
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", false),
		}, 10)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkAttachRVR("rv-1-0", "node-1", true)}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkAttachDeletingRVA("rva-del", "node-2")}

		atts, outcome := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1", "node-2"))

		Expect(outcome.Error()).To(BeNil())
		// No Detach transition created for node-2 (no member to detach).
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())

		as := atts.findAttachmentStateByNodeName("node-2")
		Expect(as).NotTo(BeNil())
		Expect(as.intent).To(Equal(attachmentIntentDetach))
		Expect(as.member).To(BeNil())
		Expect(as.conditionReason).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonDetached))
		Expect(as.conditionMessage).To(ContainSubstring("detached"))
	})

	It("skips detach when deleting RVA exists on non-member node while attach is blocked (RV deleting)", func() {
		// RV is deleting → attach blocked.
		// node-1: diskful member (will get Detach).
		// node-2: only a deleting RVA, no member → intent=Detach, member=nil → skip.
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", true),
		}, 10)
		rv.DeletionTimestamp = ptr.To(metav1.Now())
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkAttachRVR("rv-1-0", "node-1", true)}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkAttachDeletingRVA("rva-del", "node-2")}

		atts, outcome := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1", "node-2"))

		Expect(outcome.Error()).To(BeNil())
		// Detach transition created only for node-1 (which has a member).
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].ReplicaName).To(Equal("rv-1-0"))

		as := atts.findAttachmentStateByNodeName("node-2")
		Expect(as).NotTo(BeNil())
		Expect(as.intent).To(Equal(attachmentIntentDetach))
		Expect(as.member).To(BeNil())
		Expect(as.conditionReason).To(Equal(v1alpha1.ReplicatedVolumeAttachmentCondAttachedReasonDetached))
	})

	It("allows detach in detach-only mode (RV deleting)", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", true),
		}, 10)
		rv.DeletionTimestamp = ptr.To(metav1.Now())
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkAttachRVR("rv-1-0", "node-1", true)}

		_, outcome := testEnsureDatameshAttachments(ctx, rv, rvrs, nil, mkAttachRSP("node-1", "node-2", "node-3", "node-a", "node-b"))

		Expect(outcome.Error()).To(BeNil())
		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rv.Status.Datamesh.Members[0].Attached).To(BeFalse())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Tests: combined / multiattach
//

var _ = Describe("ensureDatameshAttachments combined", func() {
	ctx := context.Background()

	It("single-attach switch: detach created, new node queued (slot occupied by detaching)", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", true), // attached, no active RVA → will detach
			mkAttachMember("rv-1-1", "node-2", false),
		}, 10)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkAttachRVR("rv-1-0", "node-1", true),
			mkAttachRVR("rv-1-1", "node-2", true),
		}
		// Only RVA on node-2 → switch from node-1 to node-2.
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkAttachRVA("rva-1", "node-2")}

		atts, outcome := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1", "node-2", "node-3", "node-a", "node-b"))

		Expect(outcome.Error()).To(BeNil())
		Expect(outcome.DidChange()).To(BeTrue())

		// Detach transition created for node-1.
		Expect(rv.Status.Datamesh.Members[0].Attached).To(BeFalse())
		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach))

		// node-2 is Pending: node-1 still occupies the slot (detach not confirmed).
		Expect(rv.Status.Datamesh.Members[1].Attached).To(BeFalse())
		as := atts.findAttachmentStateByNodeName("node-2")
		Expect(as.intent).To(Equal(attachmentIntentPending))
		Expect(as.conditionMessage).To(ContainSubstring("Waiting for attachment slot"))
	})

	It("no-op when settled", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", true),
		}, 10)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkAttachRVR("rv-1-0", "node-1", true)}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkAttachRVA("rva-1", "node-1")}

		_, outcome := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1", "node-2", "node-3", "node-a", "node-b"))

		Expect(outcome.Error()).To(BeNil())
		Expect(outcome.DidChange()).To(BeFalse())
	})

	It("empty state: no members, no RVAs", func() {
		rv := mkAttachRV(nil, 10)

		atts, outcome := testEnsureDatameshAttachments(ctx, rv, nil, nil, mkAttachRSP())

		Expect(outcome.Error()).To(BeNil())
		Expect(outcome.DidChange()).To(BeFalse())
		Expect(atts.attachmentStates).To(BeEmpty())
	})

	It("multiattach: based on actual intents, not maxAttachments", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", true),
			mkAttachMember("rv-1-1", "node-2", true),
		}, 10)
		rv.Spec.MaxAttachments = 1 // decreased, but 2 already attached
		rv.Status.Datamesh.Multiattach = true
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkAttachRVR("rv-1-0", "node-1", true),
			mkAttachRVR("rv-1-1", "node-2", true),
		}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{
			mkAttachRVA("rva-1", "node-1"),
			mkAttachRVA("rva-2", "node-2"),
		}

		atts, _ := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1", "node-2", "node-3", "node-a", "node-b"))

		// intendedAttachments=2 > 1 → multiattach stays enabled.
		Expect(atts.intendedAttachments.Len()).To(Equal(2))
		// Multiattach should NOT be disabled (still have 2 intents).
		Expect(rv.Status.Datamesh.Multiattach).To(BeTrue())
	})

	It("multiattach: enables when 2 intents, maxAttachments=2", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", true),
			mkAttachMember("rv-1-1", "node-2", false),
		}, 10)
		rv.Spec.MaxAttachments = 2
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkAttachRVR("rv-1-0", "node-1", true),
			mkAttachRVR("rv-1-1", "node-2", true),
		}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{
			mkAttachRVA("rva-1", "node-1"),
			mkAttachRVA("rva-2", "node-2"),
		}

		_, outcome := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1", "node-2", "node-3", "node-a", "node-b"))

		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rv.Status.Datamesh.Multiattach).To(BeTrue())
		// EnableMultiattach transition created.
		hasEnable := false
		for _, t := range rv.Status.DatameshTransitions {
			if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeEnableMultiattach {
				hasEnable = true
			}
		}
		Expect(hasEnable).To(BeTrue())
	})

	It("return value provides member, rvr, rvas pointers", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", false),
		}, 10)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkAttachRVR("rv-1-0", "node-1", true)}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkAttachRVA("rva-1", "node-1")}

		atts, _ := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1", "node-2", "node-3", "node-a", "node-b"))

		as := atts.findAttachmentStateByNodeName("node-1")
		Expect(as).NotTo(BeNil())
		Expect(as.member).NotTo(BeNil())
		Expect(as.rvr).NotTo(BeNil())
		Expect(as.rvas).To(HaveLen(1))
	})

	It("multiattach: disables when single intent and potentiallyAttached <= 1", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", true),
		}, 10)
		rv.Status.Datamesh.Multiattach = true // was enabled
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkAttachRVR("rv-1-0", "node-1", true)}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkAttachRVA("rva-1", "node-1")}

		_, outcome := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1"))

		Expect(outcome.DidChange()).To(BeTrue())
		Expect(rv.Status.Datamesh.Multiattach).To(BeFalse())
		hasDisable := false
		for _, t := range rv.Status.DatameshTransitions {
			if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeDisableMultiattach {
				hasDisable = true
			}
		}
		Expect(hasDisable).To(BeTrue())
	})

	It("multiattach: does NOT disable while potentiallyAttached > 1", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", true),
			mkAttachMember("rv-1-1", "node-2", false), // detaching
		}, 10)
		rv.Status.Datamesh.Multiattach = true
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{
			{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach, ReplicaName: "rv-1-1", Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, DatameshRevision: 10, StartedAt: ptr.To(metav1.Now())}}},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkAttachRVR("rv-1-0", "node-1", true),
			mkAttachRVRWithRev("rv-1-1", "node-2", 5), // not confirmed
		}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkAttachRVA("rva-1", "node-1")}

		_, _ = testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1", "node-2"))

		// potentiallyAttached = {node-1, node-2} (>1) → DisableMultiattach NOT created.
		Expect(rv.Status.Datamesh.Multiattach).To(BeTrue())
		for _, t := range rv.Status.DatameshTransitions {
			Expect(t.Type).NotTo(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeDisableMultiattach))
		}
	})

	It("multiattach: does NOT create duplicate EnableMultiattach", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", true),
			mkAttachMember("rv-1-1", "node-2", false),
		}, 10)
		rv.Spec.MaxAttachments = 2
		rv.Status.Datamesh.Multiattach = true
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{
			{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeEnableMultiattach, Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, DatameshRevision: 10, StartedAt: ptr.To(metav1.Now())}}},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkAttachRVRWithRev("rv-1-0", "node-1", 5),
			mkAttachRVRWithRev("rv-1-1", "node-2", 5),
		}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{
			mkAttachRVA("rva-1", "node-1"),
			mkAttachRVA("rva-2", "node-2"),
		}

		_, _ = testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1", "node-2"))

		// Should not create a second EnableMultiattach.
		enableCount := 0
		for _, t := range rv.Status.DatameshTransitions {
			if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeEnableMultiattach {
				enableCount++
			}
		}
		Expect(enableCount).To(Equal(1))
	})

	It("multiattach: EnableMultiattach transition gets Message filled", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", true),
			mkAttachMember("rv-1-1", "node-2", false),
		}, 10)
		rv.Spec.MaxAttachments = 2
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkAttachRVR("rv-1-0", "node-1", true),
			mkAttachRVR("rv-1-1", "node-2", true),
		}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{
			mkAttachRVA("rva-1", "node-1"),
			mkAttachRVA("rva-2", "node-2"),
		}

		_, _ = testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1", "node-2"))

		// EnableMultiattach transition should have a Message filled by the progress callback.
		for _, t := range rv.Status.DatameshTransitions {
			if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeEnableMultiattach {
				Expect(t.CurrentStep().Message).NotTo(BeEmpty(), "EnableMultiattach transition should have a progress message")
				Expect(t.CurrentStep().Message).To(ContainSubstring("replicas confirmed revision"))
			}
		}
	})

	It("completes DisableMultiattach when all members confirm", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", true),
			mkAttachMember("rv-1-1", "node-2", false),
		}, 10)
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{
			{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeDisableMultiattach, Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, DatameshRevision: 10, StartedAt: ptr.To(metav1.Now())}}},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkAttachRVRWithRev("rv-1-0", "node-1", 10),
			mkAttachRVRWithRev("rv-1-1", "node-2", 10),
		}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkAttachRVA("rva-1", "node-1")}

		_, outcome := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1", "node-2"))

		Expect(outcome.DidChange()).To(BeTrue())
		for _, t := range rv.Status.DatameshTransitions {
			Expect(t.Type).NotTo(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeDisableMultiattach))
		}
	})

	It("in-progress Attach transition sets conditionMessage", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", true),
		}, 10)
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{
			{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach, ReplicaName: "rv-1-0", Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, DatameshRevision: 10, StartedAt: ptr.To(metav1.Now())}}},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkAttachRVRWithRev("rv-1-0", "node-1", 5)} // not confirmed
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkAttachRVA("rva-1", "node-1")}

		atts, _ := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1"))

		as := atts.findAttachmentStateByNodeName("node-1")
		Expect(as.conditionMessage).To(ContainSubstring("Attaching"))
		Expect(as.conditionMessage).To(ContainSubstring("replicas confirmed revision"))
		// Transition.Message should also be set.
		Expect(rv.Status.DatameshTransitions[0].CurrentStep().Message).To(ContainSubstring("replicas confirmed revision"))
	})

	It("in-progress Detach transition sets conditionMessage", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", false),
		}, 10)
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{
			{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach, ReplicaName: "rv-1-0", Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, DatameshRevision: 10, StartedAt: ptr.To(metav1.Now())}}},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkAttachRVRWithRev("rv-1-0", "node-1", 5)} // not confirmed

		atts, _ := testEnsureDatameshAttachments(ctx, rv, rvrs, nil, mkAttachRSP("node-1"))

		as := atts.findAttachmentStateByNodeName("node-1")
		Expect(as.conditionMessage).To(ContainSubstring("Detaching"))
		Expect(as.conditionMessage).To(ContainSubstring("replicas confirmed revision"))
	})

	It("in-progress transition with DRBDConfigured=False includes error in message", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", true),
		}, 10)
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{
			{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach, ReplicaName: "rv-1-0", Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, DatameshRevision: 10, StartedAt: ptr.To(metav1.Now())}}},
		}
		rvr := mkAttachRVRWithRev("rv-1-0", "node-1", 5)
		rvr.Generation = 1
		rvr.Status.Conditions = append(rvr.Status.Conditions,
			metav1.Condition{Type: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType, Status: metav1.ConditionFalse,
				Reason: "ConfigurationFailed", Message: "connection timed out", ObservedGeneration: 1},
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkAttachRVA("rva-1", "node-1")}

		atts, _ := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1"))

		as := atts.findAttachmentStateByNodeName("node-1")
		Expect(as.conditionMessage).To(ContainSubstring("Attaching"))
		Expect(as.conditionMessage).To(ContainSubstring("DRBDConfigured"))
		Expect(as.conditionMessage).To(ContainSubstring("ConfigurationFailed"))
		Expect(as.conditionMessage).To(ContainSubstring("connection timed out"))
	})

	It("detach: already fully detached is no-op", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", false), // already detached, no active transition
		}, 10)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkAttachRVR("rv-1-0", "node-1", true)}
		// No RVA → intent=Detach, but already settled.

		_, outcome := testEnsureDatameshAttachments(ctx, rv, rvrs, nil, mkAttachRSP("node-1"))

		Expect(outcome.DidChange()).To(BeFalse())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})

	It("detach: does not create duplicate when active Detach exists", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", false), // Attached=false, Detach in progress
		}, 10)
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{
			{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach, ReplicaName: "rv-1-0", Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, DatameshRevision: 10, StartedAt: ptr.To(metav1.Now())}}},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkAttachRVRWithRev("rv-1-0", "node-1", 5)}

		_, _ = testEnsureDatameshAttachments(ctx, rv, rvrs, nil, mkAttachRSP("node-1"))

		// Still exactly 1 Detach transition (no duplicate).
		detachCount := 0
		for _, t := range rv.Status.DatameshTransitions {
			if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach {
				detachCount++
			}
		}
		Expect(detachCount).To(Equal(1))
	})

	It("detach: conflict with Attach on same replica", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", true), // Attached=true
		}, 10)
		// Active Attach transition (not yet confirmed) + no RVA → intent=Detach
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{
			{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach, ReplicaName: "rv-1-0", Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, DatameshRevision: 10, StartedAt: ptr.To(metav1.Now())}}},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkAttachRVRWithRev("rv-1-0", "node-1", 5)}

		atts, _ := testEnsureDatameshAttachments(ctx, rv, rvrs, nil, mkAttachRSP("node-1"))

		// Should NOT create Detach (conflict).
		Expect(rv.Status.Datamesh.Members[0].Attached).To(BeTrue())
		as := atts.findAttachmentStateByNodeName("node-1")
		Expect(as.conditionMessage).To(ContainSubstring("Detach pending"))
		Expect(as.conditionMessage).To(ContainSubstring("waiting for attach to complete first"))
	})

	It("attach: already fully attached is no-op", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", true), // fully attached, no pending request
		}, 10)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkAttachRVR("rv-1-0", "node-1", true)}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkAttachRVA("rva-1", "node-1")}

		_, outcome := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1"))

		Expect(outcome.DidChange()).To(BeFalse())
		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
	})

	It("attach: does not create duplicate when active Attach exists", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", true), // Attached=true, Attach in progress
		}, 10)
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{
			{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach, ReplicaName: "rv-1-0", Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, DatameshRevision: 10, StartedAt: ptr.To(metav1.Now())}}},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkAttachRVRWithRev("rv-1-0", "node-1", 5)}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkAttachRVA("rva-1", "node-1")}

		_, _ = testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1"))

		// Still exactly 1 Attach transition (no duplicate).
		attachCount := 0
		for _, t := range rv.Status.DatameshTransitions {
			if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach {
				attachCount++
			}
		}
		Expect(attachCount).To(Equal(1))
	})

	It("attach: conflict with Detach on same replica", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", false), // Attached=false, Detach pending
		}, 10)
		rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{
			{Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach, ReplicaName: "rv-1-0", Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, DatameshRevision: 10, StartedAt: ptr.To(metav1.Now())}}},
		}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkAttachRVRWithRev("rv-1-0", "node-1", 5)}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkAttachRVA("rva-1", "node-1")}

		atts, _ := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1"))

		// Should NOT create Attach (conflict with Detach on same replica).
		as := atts.findAttachmentStateByNodeName("node-1")
		Expect(as.conditionMessage).To(ContainSubstring("Attach pending"))
		Expect(as.conditionMessage).To(ContainSubstring("waiting for detach to complete first"))
	})

	It("attach: blocks second Attach when Multiattach=false", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", true),
			mkAttachMember("rv-1-1", "node-2", false),
		}, 10)
		rv.Spec.MaxAttachments = 2
		// Multiattach=false, no EnableMultiattach yet → toggle will create one.
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkAttachRVR("rv-1-0", "node-1", true),
			mkAttachRVR("rv-1-1", "node-2", true),
		}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{
			mkAttachRVA("rva-1", "node-1"),
			mkAttachRVA("rva-2", "node-2"),
		}

		atts, _ := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1", "node-2"))

		// node-2 should be blocked (waiting for multiattach).
		as := atts.findAttachmentStateByNodeName("node-2")
		Expect(as.conditionMessage).To(ContainSubstring("Waiting for multiattach to be enabled"))
		// Attach should NOT be created for node-2.
		for _, t := range rv.Status.DatameshTransitions {
			if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach {
				Expect(t.ReplicaName).NotTo(Equal("rv-1-1"), "Attach should not be created for node-2")
			}
		}
	})

	It("attach: Attach transition Message field set on creation", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", false),
		}, 10)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkAttachRVR("rv-1-0", "node-1", true)}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkAttachRVA("rva-1", "node-1")}

		_, _ = testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1"))

		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].CurrentStep().Message).To(ContainSubstring("replicas confirmed revision"))
	})

	It("attach: Detach transition Message field set on creation", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", true),
		}, 10)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkAttachRVR("rv-1-0", "node-1", true)}
		// No RVA → will detach.

		_, _ = testEnsureDatameshAttachments(ctx, rv, rvrs, nil, mkAttachRSP("node-1"))

		Expect(rv.Status.DatameshTransitions).To(HaveLen(1))
		Expect(rv.Status.DatameshTransitions[0].Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach))
		Expect(rv.Status.DatameshTransitions[0].CurrentStep().Message).To(ContainSubstring("replicas confirmed revision"))
	})

	It("attach: potentiallyAttached updated after new Attach creation", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", false),
		}, 10)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkAttachRVR("rv-1-0", "node-1", true)}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkAttachRVA("rva-1", "node-1")}

		atts, _ := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1"))

		// After Attach creation, potentiallyAttached should include the member.
		Expect(atts.potentiallyAttached.Contains(0)).To(BeTrue())
	})

	It("attach: blocked globally shows message on all Attach-intent nodes", func() {
		rv := mkAttachRV([]v1alpha1.DatameshMember{
			mkAttachMember("rv-1-0", "node-1", true), // attached
		}, 10)
		rv.DeletionTimestamp = ptr.To(metav1.Now()) // deleting → blocked
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{mkAttachRVR("rv-1-0", "node-1", true)}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkAttachRVA("rva-1", "node-1")}

		atts, _ := testEnsureDatameshAttachments(ctx, rv, rvrs, rvas, mkAttachRSP("node-1"))

		as := atts.findAttachmentStateByNodeName("node-1")
		// Node-1 is potentiallyAttached + has RVA → intent=Attach.
		// But attach is blocked (deleting) → message should be set.
		Expect(as.intent).To(Equal(attachmentIntentAttach))
		Expect(as.conditionMessage).To(ContainSubstring("deleted"))
	})
})
