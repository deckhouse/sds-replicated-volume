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

// Integration tests: attachment transitions.
//
// Tests attachment lifecycle (attach/detach/multiattach) interacting
// with membership transitions, quorum gating, and failure recovery.

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// simulateReadyReplicas bumps revision and makes all RVRs Ready with Quorum=true.
// Needed for attachment integration tests where Attach guards check RVR readiness.
func simulateReadyReplicas(rv *v1alpha1.ReplicatedVolume, rvrs []*v1alpha1.ReplicatedVolumeReplica) {
	for _, rvr := range rvrs {
		rvr.Status.DatameshRevision = rv.Status.DatameshRevision
		rvr.Status.Quorum = ptr.To(true)
		rvr.Generation = 1
		// Set BackingVolume UpToDate (for RemoveReplica guards).
		if rvr.Status.BackingVolume == nil {
			rvr.Status.BackingVolume = &v1alpha1.ReplicatedVolumeReplicaStatusBackingVolume{}
		}
		rvr.Status.BackingVolume.State = v1alpha1.DiskStateUpToDate
		// Ensure Ready condition exists.
		found := false
		for i := range rvr.Status.Conditions {
			if rvr.Status.Conditions[i].Type == v1alpha1.ReplicatedVolumeReplicaCondReadyType {
				rvr.Status.Conditions[i].Status = metav1.ConditionTrue
				rvr.Status.Conditions[i].ObservedGeneration = 1
				found = true
				break
			}
		}
		if !found {
			rvr.Status.Conditions = append(rvr.Status.Conditions, metav1.Condition{
				Type:               v1alpha1.ReplicatedVolumeReplicaCondReadyType,
				Status:             metav1.ConditionTrue,
				Reason:             "Ready",
				ObservedGeneration: 1,
			})
		}
	}
}

var _ = Describe("integration: attachment", func() {

	// ── 1. Full D lifecycle: Add → Attach → Detach → Remove ─────────────

	It("full D lifecycle: add+attach → detach+remove", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-2")},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRReady("rv-1-0", "node-1", 5),
			mkRVRReady("rv-1-1", "node-2", 5),
			mkRVR("rv-1-2", "node-3", 0),
		}
		rsp := mkRSP("node-1", "node-2", "node-3")
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-3", "node-3")}

		// Phase 1: AddReplica(D) + Attach.
		runSettleLoop(rv, rsp, rvrs, rvas, FeatureFlags{}, simulateReadyReplicas, nil)

		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.Datamesh.Members).To(HaveLen(3))
		m2 := rv.Status.Datamesh.FindMemberByName("rv-1-2")
		Expect(m2).NotTo(BeNil())
		Expect(m2.Attached).To(BeTrue())

		// Phase 2: Remove RVA + LeaveRequest → Detach then RemoveReplica.
		rvas = nil
		rv.Status.DatameshReplicaRequests = []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
			mkLeaveRequest("rv-1-2"),
		}
		// Make rv-1-2 UpToDate for RemoveReplica guards.
		rvrs[2].Status.BackingVolume = &v1alpha1.ReplicatedVolumeReplicaStatusBackingVolume{
			State: v1alpha1.DiskStateUpToDate,
		}
		runSettleLoop(rv, rsp, rvrs, rvas, FeatureFlags{}, simulateReadyReplicas, nil)

		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.Datamesh.FindMemberByName("rv-1-2")).To(BeNil())
		var voters int
		for _, m := range rv.Status.Datamesh.Members {
			if m.Type.IsVoter() {
				voters++
			}
		}
		Expect(voters).To(Equal(2))
	})

	// ── 2. Full A lifecycle: Add → Attach → Detach → Remove ─────────────

	It("full A lifecycle: add+attach → detach+remove", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestAccess("rv-1-1")},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRReady("rv-1-0", "node-1", 5),
			mkRVR("rv-1-1", "node-2", 0),
		}
		rsp := mkRSP("node-1", "node-2")
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-2", "node-2")}

		// Phase 1: AddReplica(A) + Attach.
		runSettleLoop(rv, rsp, rvrs, rvas, FeatureFlags{}, simulateReadyReplicas, nil)

		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		m1 := rv.Status.Datamesh.FindMemberByName("rv-1-1")
		Expect(m1).NotTo(BeNil())
		Expect(m1.Type).To(Equal(v1alpha1.DatameshMemberTypeAccess))
		Expect(m1.Attached).To(BeTrue())

		// Phase 2: Remove RVA + LeaveRequest → Detach then RemoveReplica.
		rvas = nil
		rv.Status.DatameshReplicaRequests = []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
			mkLeaveRequest("rv-1-1"),
		}
		runSettleLoop(rv, rsp, rvrs, rvas, FeatureFlags{}, simulateReadyReplicas, nil)

		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.Datamesh.FindMemberByName("rv-1-1")).To(BeNil())
	})

	// ── 3. Concurrent: Attach + AddReplica ───────────────────────────────

	It("concurrent: attach on node-1 + AddReplica(D) on node-3", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkJoinRequestD("rv-1-2")},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRReady("rv-1-0", "node-1", 5),
			mkRVRReady("rv-1-1", "node-2", 5),
			mkRVR("rv-1-2", "node-3", 0),
		}
		rsp := mkRSP("node-1", "node-2", "node-3")
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-1")}

		runSettleLoop(rv, rsp, rvrs, rvas, FeatureFlags{}, simulateReadyReplicas, nil)

		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		// Attach completed.
		Expect(rv.Status.Datamesh.FindMemberByName("rv-1-0").Attached).To(BeTrue())
		// AddReplica completed.
		var voters int
		for _, m := range rv.Status.Datamesh.Members {
			if m.Type.IsVoter() {
				voters++
			}
		}
		Expect(voters).To(BeNumerically(">=", 3))
	})

	// ── 4. Remove blocked by attachment ──────────────────────────────────

	It("remove blocked by attachment: detach-before-remove sequencing", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMemberAttached("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
			},
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{mkLeaveRequest("rv-1-1")},
			nil,
		)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRReady("rv-1-0", "node-1", 5),
			mkRVRUpToDate("rv-1-1", "node-2", 5),
		}
		// No RVAs for node-2 → dispatcher wants Detach.
		// guardNotAttached blocks RemoveReplica until Detach completes.

		runSettleLoop(rv, mkRSP("node-1", "node-2"), rvrs, nil, FeatureFlags{}, simulateReadyReplicas, nil)

		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		// Detach ran first, then RemoveReplica.
		Expect(rv.Status.Datamesh.FindMemberByName("rv-1-1")).To(BeNil())
		Expect(rv.Status.Datamesh.Members).To(HaveLen(1))
	})

	// ── 5. Multiattach lifecycle: enable → attach 2 → detach 1 → disable

	It("multiattach lifecycle: enable → attach → detach → disable", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
			},
			nil, nil,
		)
		rv.Spec.MaxAttachments = 2
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRReady("rv-1-0", "node-1", 5),
			mkRVRReady("rv-1-1", "node-2", 5),
		}
		rsp := mkRSP("node-1", "node-2")
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-1"), mkRVA("rva-2", "node-2")}

		// Phase 1: EnableMultiattach + Attach both.
		runSettleLoop(rv, rsp, rvrs, rvas, FeatureFlags{}, simulateReadyReplicas, nil)

		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.Datamesh.Multiattach).To(BeTrue())
		Expect(rv.Status.Datamesh.FindMemberByName("rv-1-0").Attached).To(BeTrue())
		Expect(rv.Status.Datamesh.FindMemberByName("rv-1-1").Attached).To(BeTrue())

		// Phase 2: Remove RVA for node-2 → Detach + DisableMultiattach.
		rvas = []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-1")}
		runSettleLoop(rv, rsp, rvrs, rvas, FeatureFlags{}, simulateReadyReplicas, nil)

		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.Datamesh.Multiattach).To(BeFalse())
		Expect(rv.Status.Datamesh.FindMemberByName("rv-1-0").Attached).To(BeTrue())
		Expect(rv.Status.Datamesh.FindMemberByName("rv-1-1").Attached).To(BeFalse())
	})

	// ── 6. VolumeAccess=Local: only Diskful nodes attach ─────────────────

	It("VolumeAccess=Local: only Diskful members attach", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
				mkMember("rv-1-2", v1alpha1.DatameshMemberTypeAccess, "node-3"),
			},
			nil, nil,
		)
		rv.Spec.MaxAttachments = 3
		rv.Status.Configuration.VolumeAccess = v1alpha1.VolumeAccessLocal
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRReady("rv-1-0", "node-1", 5),
			mkRVRReady("rv-1-1", "node-2", 5),
			mkRVRReady("rv-1-2", "node-3", 5),
		}
		rsp := mkRSP("node-1", "node-2", "node-3")
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{
			mkRVA("rva-1", "node-1"),
			mkRVA("rva-2", "node-2"),
			mkRVA("rva-3", "node-3"),
		}

		runSettleLoop(rv, rsp, rvrs, rvas, FeatureFlags{}, simulateReadyReplicas, nil)

		// D members attached.
		Expect(rv.Status.Datamesh.FindMemberByName("rv-1-0").Attached).To(BeTrue())
		Expect(rv.Status.Datamesh.FindMemberByName("rv-1-1").Attached).To(BeTrue())
		// A member NOT attached (VolumeAccess=Local blocks non-Diskful).
		Expect(rv.Status.Datamesh.FindMemberByName("rv-1-2").Attached).To(BeFalse())
	})

	// ── 7. Attached member dies: ForceDetach + ForceRemove ───────────────

	It("attached member dies: ForceDetach + ForceRemove", func() {
		member1 := mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2")
		member1.Attached = true
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				member1,
			},
			// ForceLeave triggers both ForceDetach (for attached member) and ForceRemove.
			[]v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				mkForceLeaveRequest("rv-1-1"),
			},
			nil,
		)
		rv.Status.Datamesh.Quorum = 2
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVRReady("rv-1-0", "node-1", 5),
			mkRVR("rv-1-1", "node-2", 0), // unreachable
		}

		runSettleLoop(rv, nil, rvrs, nil, FeatureFlags{}, simulateReadyReplicas, nil)

		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		// ForceDetach cleared Attached, then ForceRemove removed member.
		Expect(rv.Status.Datamesh.FindMemberByName("rv-1-1")).To(BeNil())
		Expect(rv.Status.Datamesh.Members).To(HaveLen(1))
	})

	// ── 8. Quorum lost → Attach blocked → restored ───────────────────────

	It("quorum lost → attach blocked → quorum restored → attach succeeds", func() {
		rv := mkRV(5,
			[]v1alpha1.DatameshMember{
				mkMember("rv-1-0", v1alpha1.DatameshMemberTypeDiskful, "node-1"),
				mkMember("rv-1-1", v1alpha1.DatameshMemberTypeDiskful, "node-2"),
			},
			nil, nil,
		)
		rvr0 := mkRVRReady("rv-1-0", "node-1", 5)
		rvr0.Status.Quorum = ptr.To(false)
		rvr1 := mkRVRReady("rv-1-1", "node-2", 5)
		rvr1.Status.Quorum = ptr.To(false)
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr0, rvr1}
		rsp := mkRSP("node-1", "node-2")
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-1")}

		// Phase 1: quorum not satisfied → attach blocked.
		changed, replicas := ProcessTransitions(context.Background(), rv, rsp, rvrs, rvas, FeatureFlags{})

		_ = changed
		hasAttach := false
		for _, t := range rv.Status.DatameshTransitions {
			if t.Type == v1alpha1.ReplicatedVolumeDatameshTransitionTypeAttach {
				hasAttach = true
			}
		}
		Expect(hasAttach).To(BeFalse(), "attach should be blocked by quorum")
		rc := findReplicaContext(replicas, 0)
		Expect(rc.AttachmentConditionMessage()).To(ContainSubstring("quorum not satisfied"))

		// Phase 2: quorum restored.
		rvr0.Status.Quorum = ptr.To(true)
		rvr1.Status.Quorum = ptr.To(true)
		runSettleLoop(rv, rsp, rvrs, rvas, FeatureFlags{}, simulateReadyReplicas, nil)

		Expect(rv.Status.DatameshTransitions).To(BeEmpty())
		Expect(rv.Status.Datamesh.FindMemberByName("rv-1-0").Attached).To(BeTrue())
	})
})
