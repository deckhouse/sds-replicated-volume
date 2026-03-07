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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/idset"
)

// ──────────────────────────────────────────────────────────────────────────────
// Test helper: builds a globalContext + ReplicaContexts for confirm unit tests.
// Each entry in replicas is (id, memberType, datameshRevision). memberType ""
// means member=nil (non-member). datameshRevision -1 means rvr=nil.
type confirmSetup struct {
	id     uint8
	mType  v1alpha1.DatameshMemberType
	rev    int64 // DatameshRevision; -1 → rvr=nil
	rvrNil bool  // force rvr=nil regardless of rev
}

func buildConfirmCtx(entries []confirmSetup) (*globalContext, []*ReplicaContext) {
	gctx := &globalContext{}
	gctx.allReplicas = make([]ReplicaContext, len(entries))
	rctxs := make([]*ReplicaContext, len(entries))

	for i, e := range entries {
		rc := &gctx.allReplicas[i]
		rc.gctx = gctx
		rc.id = e.id
		rc.name = "rv-1-" + string(rune('0'+e.id))

		if e.mType != "" {
			rc.member = &v1alpha1.DatameshMember{
				Name: rc.name,
				Type: e.mType,
			}
		}

		if !e.rvrNil && e.rev >= 0 {
			rc.rvr = &v1alpha1.ReplicatedVolumeReplica{}
			rc.rvr.Status.DatameshRevision = e.rev
		}

		gctx.replicas[e.id] = rc
		rctxs[i] = rc
	}

	return gctx, rctxs
}

// ──────────────────────────────────────────────────────────────────────────────

var _ = Describe("confirmSubjectOnly", func() {
	It("confirmed when rev >= stepRevision", func() {
		gctx, rctxs := buildConfirmCtx([]confirmSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, rev: 10},
		})
		res := confirmSubjectOnly(gctx, rctxs[0], 10)
		Expect(res.MustConfirm).To(Equal(idset.Of(0)))
		Expect(res.Confirmed).To(Equal(idset.Of(0)))
	})

	It("not confirmed when rev < stepRevision", func() {
		gctx, rctxs := buildConfirmCtx([]confirmSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, rev: 5},
		})
		res := confirmSubjectOnly(gctx, rctxs[0], 10)
		Expect(res.MustConfirm).To(Equal(idset.Of(0)))
		Expect(res.Confirmed.IsEmpty()).To(BeTrue())
	})

	It("not confirmed when rvr=nil", func() {
		gctx, rctxs := buildConfirmCtx([]confirmSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, rev: -1},
		})
		res := confirmSubjectOnly(gctx, rctxs[0], 10)
		Expect(res.MustConfirm).To(Equal(idset.Of(0)))
		Expect(res.Confirmed.IsEmpty()).To(BeTrue())
	})
})

var _ = Describe("confirmSubjectOnlyLeaving", func() {
	It("confirmed when rev >= stepRevision", func() {
		gctx, rctxs := buildConfirmCtx([]confirmSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeAccess, rev: 10},
		})
		res := confirmSubjectOnlyLeaving(gctx, rctxs[0], 10)
		Expect(res.Confirmed).To(Equal(idset.Of(0)))
	})

	It("confirmed when rev=0 (leaving)", func() {
		gctx, rctxs := buildConfirmCtx([]confirmSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeAccess, rev: 0},
		})
		res := confirmSubjectOnlyLeaving(gctx, rctxs[0], 10)
		Expect(res.Confirmed).To(Equal(idset.Of(0)))
	})

	It("not confirmed when rev between 0 and stepRevision", func() {
		gctx, rctxs := buildConfirmCtx([]confirmSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeAccess, rev: 5},
		})
		res := confirmSubjectOnlyLeaving(gctx, rctxs[0], 10)
		Expect(res.Confirmed.IsEmpty()).To(BeTrue())
	})
})

var _ = Describe("confirmSubjectOnlyLeavingOrGone", func() {
	It("confirmed when rvr=nil (gone)", func() {
		gctx, rctxs := buildConfirmCtx([]confirmSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeAccess, rev: -1},
		})
		res := confirmSubjectOnlyLeavingOrGone(gctx, rctxs[0], 10)
		Expect(res.Confirmed).To(Equal(idset.Of(0)))
	})

	It("confirmed when rev=0 (leaving)", func() {
		gctx, rctxs := buildConfirmCtx([]confirmSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeAccess, rev: 0},
		})
		res := confirmSubjectOnlyLeavingOrGone(gctx, rctxs[0], 10)
		Expect(res.Confirmed).To(Equal(idset.Of(0)))
	})

	It("confirmed when rev >= stepRevision", func() {
		gctx, rctxs := buildConfirmCtx([]confirmSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeAccess, rev: 10},
		})
		res := confirmSubjectOnlyLeavingOrGone(gctx, rctxs[0], 10)
		Expect(res.Confirmed).To(Equal(idset.Of(0)))
	})

	It("not confirmed when rev between 0 and stepRevision", func() {
		gctx, rctxs := buildConfirmCtx([]confirmSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeAccess, rev: 5},
		})
		res := confirmSubjectOnlyLeavingOrGone(gctx, rctxs[0], 10)
		Expect(res.Confirmed.IsEmpty()).To(BeTrue())
	})
})

var _ = Describe("confirmFMPlusSubject", func() {
	It("MustConfirm = FM union subject", func() {
		// D(0) confirmed, D(1) confirmed, A(2) = subject (star, not FM)
		gctx, rctxs := buildConfirmCtx([]confirmSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, rev: 10},
			{id: 1, mType: v1alpha1.DatameshMemberTypeDiskful, rev: 10},
			{id: 2, mType: v1alpha1.DatameshMemberTypeAccess, rev: 10},
		})
		res := confirmFMPlusSubject(gctx, rctxs[2], 10)
		// FM = {0, 1} (D connects to all peers), subject = {2}
		Expect(res.MustConfirm).To(Equal(idset.Of(0, 1, 2)))
		Expect(res.Confirmed).To(Equal(idset.Of(0, 1, 2)))
	})

	It("partial confirm: some FM not confirmed", func() {
		gctx, rctxs := buildConfirmCtx([]confirmSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, rev: 10},
			{id: 1, mType: v1alpha1.DatameshMemberTypeDiskful, rev: 5}, // not confirmed
			{id: 2, mType: v1alpha1.DatameshMemberTypeAccess, rev: 10},
		})
		res := confirmFMPlusSubject(gctx, rctxs[2], 10)
		Expect(res.MustConfirm).To(Equal(idset.Of(0, 1, 2)))
		Expect(res.Confirmed).To(Equal(idset.Of(0, 2))) // 1 missing
	})

	It("Confirmed only includes IDs from MustConfirm", func() {
		// TB(3) has rev >= step but is not FM and not subject → not in MustConfirm
		gctx, rctxs := buildConfirmCtx([]confirmSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, rev: 10},
			{id: 2, mType: v1alpha1.DatameshMemberTypeAccess, rev: 10},
			{id: 3, mType: v1alpha1.DatameshMemberTypeTieBreaker, rev: 10},
		})
		res := confirmFMPlusSubject(gctx, rctxs[1], 10)
		// FM = {0}, subject = {2}. TB(3) not in MustConfirm.
		Expect(res.MustConfirm).To(Equal(idset.Of(0, 2)))
		Expect(res.Confirmed).To(Equal(idset.Of(0, 2)))
	})
})

var _ = Describe("confirmFMPlusSubjectLeaving", func() {
	It("leaving replica (rev=0) confirmed", func() {
		gctx, rctxs := buildConfirmCtx([]confirmSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, rev: 10},
			{id: 2, mType: v1alpha1.DatameshMemberTypeAccess, rev: 0}, // leaving
		})
		res := confirmFMPlusSubjectLeaving(gctx, rctxs[1], 10)
		Expect(res.MustConfirm).To(Equal(idset.Of(0, 2)))
		Expect(res.Confirmed).To(Equal(idset.Of(0, 2)))
	})

	It("leaving replica not confirmed when rev > 0 and < step", func() {
		gctx, rctxs := buildConfirmCtx([]confirmSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, rev: 10},
			{id: 2, mType: v1alpha1.DatameshMemberTypeAccess, rev: 5},
		})
		res := confirmFMPlusSubjectLeaving(gctx, rctxs[1], 10)
		Expect(res.Confirmed).To(Equal(idset.Of(0))) // only D confirmed
	})
})

var _ = Describe("confirmAllMembers", func() {
	It("MustConfirm = all member IDs", func() {
		gctx, _ := buildConfirmCtx([]confirmSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, rev: 10},
			{id: 1, mType: v1alpha1.DatameshMemberTypeAccess, rev: 10},
			{id: 2, mType: v1alpha1.DatameshMemberTypeTieBreaker, rev: 10},
		})
		res := confirmAllMembers(gctx, 10)
		Expect(res.MustConfirm).To(Equal(idset.Of(0, 1, 2)))
		Expect(res.Confirmed).To(Equal(idset.Of(0, 1, 2)))
	})

	It("removed member (member=nil) excluded from MustConfirm", func() {
		gctx, _ := buildConfirmCtx([]confirmSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, rev: 10},
			{id: 1, mType: "", rev: 10}, // member=nil (removed)
		})
		res := confirmAllMembers(gctx, 10)
		Expect(res.MustConfirm).To(Equal(idset.Of(0)))
		Expect(res.Confirmed).To(Equal(idset.Of(0)))
	})

	It("partial confirm", func() {
		gctx, _ := buildConfirmCtx([]confirmSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, rev: 10},
			{id: 1, mType: v1alpha1.DatameshMemberTypeAccess, rev: 5},
		})
		res := confirmAllMembers(gctx, 10)
		Expect(res.MustConfirm).To(Equal(idset.Of(0, 1)))
		Expect(res.Confirmed).To(Equal(idset.Of(0)))
	})
})

var _ = Describe("confirmAllMembersLeaving", func() {
	It("subject in MustConfirm even when member=nil (post-removeMember)", func() {
		// Simulates: removeMember was called, rctx.member=nil, but subject must
		// still be in MustConfirm for completion to work.
		gctx, rctxs := buildConfirmCtx([]confirmSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, rev: 10},
			{id: 1, mType: "", rev: 0}, // member=nil (removed), rev=0 (leaving)
		})
		res := confirmAllMembersLeaving(gctx, rctxs[1], 10)
		// allMemberIDs = {0}, explicit Add = {1} → MustConfirm = {0, 1}
		Expect(res.MustConfirm).To(Equal(idset.Of(0, 1)))
		// D(0) confirmed by rev, subject(1) confirmed by rev=0
		Expect(res.Confirmed).To(Equal(idset.Of(0, 1)))
	})

	It("leaving replica not confirmed when rev > 0 and < step", func() {
		gctx, rctxs := buildConfirmCtx([]confirmSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, rev: 10},
			{id: 1, mType: "", rev: 5}, // removed but old rev
		})
		res := confirmAllMembersLeaving(gctx, rctxs[1], 10)
		Expect(res.MustConfirm).To(Equal(idset.Of(0, 1)))
		Expect(res.Confirmed).To(Equal(idset.Of(0))) // subject not confirmed
	})

	It("subject still member + confirmed by rev >= step", func() {
		// Can happen if removeMember hasn't been called yet (guard evaluation).
		gctx, rctxs := buildConfirmCtx([]confirmSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, rev: 10},
			{id: 1, mType: v1alpha1.DatameshMemberTypeLiminalDiskful, rev: 10},
		})
		res := confirmAllMembersLeaving(gctx, rctxs[1], 10)
		Expect(res.MustConfirm).To(Equal(idset.Of(0, 1)))
		Expect(res.Confirmed).To(Equal(idset.Of(0, 1)))
	})
})

var _ = Describe("confirmImmediate", func() {
	It("returns empty MustConfirm and Confirmed", func() {
		gctx, rctxs := buildConfirmCtx([]confirmSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, rev: 10},
		})
		res := confirmImmediate(gctx, rctxs[0], 10)
		Expect(res.MustConfirm.IsEmpty()).To(BeTrue())
		Expect(res.Confirmed.IsEmpty()).To(BeTrue())
	})
})

var _ = Describe("fullMeshMemberIDs", func() {
	It("includes D, D0, sD, sD0 but not A, TB", func() {
		gctx, _ := buildConfirmCtx([]confirmSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, rev: 1},
			{id: 1, mType: v1alpha1.DatameshMemberTypeLiminalDiskful, rev: 1},
			{id: 2, mType: v1alpha1.DatameshMemberTypeShadowDiskful, rev: 1},
			{id: 3, mType: v1alpha1.DatameshMemberTypeLiminalShadowDiskful, rev: 1},
			{id: 4, mType: v1alpha1.DatameshMemberTypeAccess, rev: 1},
			{id: 5, mType: v1alpha1.DatameshMemberTypeTieBreaker, rev: 1},
		})
		fm := fullMeshMemberIDs(gctx)
		Expect(fm).To(Equal(idset.Of(0, 1, 2, 3)))
	})
})

var _ = Describe("allMemberIDs", func() {
	It("includes all member types", func() {
		gctx, _ := buildConfirmCtx([]confirmSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, rev: 1},
			{id: 1, mType: v1alpha1.DatameshMemberTypeAccess, rev: 1},
			{id: 2, mType: v1alpha1.DatameshMemberTypeTieBreaker, rev: 1},
			{id: 3, mType: "", rev: 1}, // not a member
		})
		all := allMemberIDs(gctx)
		Expect(all).To(Equal(idset.Of(0, 1, 2)))
	})
})

var _ = Describe("confirmedReplicas", func() {
	It("returns IDs with rev >= stepRevision", func() {
		gctx, _ := buildConfirmCtx([]confirmSetup{
			{id: 0, mType: v1alpha1.DatameshMemberTypeDiskful, rev: 10},
			{id: 1, mType: v1alpha1.DatameshMemberTypeDiskful, rev: 5},
			{id: 2, mType: v1alpha1.DatameshMemberTypeDiskful, rev: 15},
			{id: 3, mType: v1alpha1.DatameshMemberTypeDiskful, rev: -1}, // rvr=nil
		})
		confirmed := confirmedReplicas(gctx, 10)
		Expect(confirmed).To(Equal(idset.Of(0, 2)))
	})
})
