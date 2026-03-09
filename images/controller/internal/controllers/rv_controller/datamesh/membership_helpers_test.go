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
)

// mkCountGctx builds a globalContext with the given member types for count tests.
func mkCountGctx(types ...v1alpha1.DatameshMemberType) *globalContext {
	gctx := &globalContext{}
	gctx.allReplicas = make([]ReplicaContext, len(types))
	for i, t := range types {
		gctx.allReplicas[i] = ReplicaContext{
			gctx:   gctx,
			id:     uint8(i),
			member: &v1alpha1.DatameshMember{Type: t},
		}
	}
	return gctx
}

var _ = Describe("voterCount", func() {
	It("counts D and D0 as voters", func() {
		gctx := mkCountGctx(
			v1alpha1.DatameshMemberTypeDiskful,
			v1alpha1.DatameshMemberTypeLiminalDiskful,
			v1alpha1.DatameshMemberTypeAccess,
			v1alpha1.DatameshMemberTypeTieBreaker,
			v1alpha1.DatameshMemberTypeShadowDiskful,
		)
		Expect(voterCount(gctx)).To(Equal(byte(2)))
	})

	It("returns 0 for no members", func() {
		gctx := &globalContext{}
		Expect(voterCount(gctx)).To(Equal(byte(0)))
	})
})

var _ = Describe("upToDateDiskfulCount", func() {
	It("counts D with UpToDate disk", func() {
		gctx := &globalContext{}
		gctx.allReplicas = []ReplicaContext{
			{member: &v1alpha1.DatameshMember{Type: v1alpha1.DatameshMemberTypeDiskful},
				rvr: mkRVRUpToDate("rv-1-0", "node-0", 1)},
			{member: &v1alpha1.DatameshMember{Type: v1alpha1.DatameshMemberTypeDiskful},
				rvr: mkRVR("rv-1-1", "node-1", 1)}, // no BackingVolume
			{member: &v1alpha1.DatameshMember{Type: v1alpha1.DatameshMemberTypeLiminalDiskful},
				rvr: mkRVRUpToDate("rv-1-2", "node-2", 1)}, // D0 — HasBackingVolume=false
			{member: &v1alpha1.DatameshMember{Type: v1alpha1.DatameshMemberTypeShadowDiskful},
				rvr: mkRVRUpToDate("rv-1-3", "node-3", 1)}, // sD — not a voter
		}
		Expect(upToDateDiskfulCount(gctx)).To(Equal(byte(1)))
	})
})

var _ = Describe("tbCount", func() {
	It("counts TBs only", func() {
		gctx := mkCountGctx(
			v1alpha1.DatameshMemberTypeTieBreaker,
			v1alpha1.DatameshMemberTypeTieBreaker,
			v1alpha1.DatameshMemberTypeDiskful,
		)
		Expect(tbCount(gctx)).To(Equal(byte(2)))
	})
})

var _ = Describe("computeTargetQ", func() {
	It("computes quorum threshold", func() {
		Expect(computeTargetQ(1)).To(Equal(byte(1)))
		Expect(computeTargetQ(2)).To(Equal(byte(2)))
		Expect(computeTargetQ(3)).To(Equal(byte(2)))
		Expect(computeTargetQ(4)).To(Equal(byte(3)))
		Expect(computeTargetQ(5)).To(Equal(byte(3)))
		Expect(computeTargetQ(6)).To(Equal(byte(4)))
	})
})

var _ = Describe("countPerZone", func() {
	It("counts and sorts by zone", func() {
		gctx := &globalContext{}
		gctx.allReplicas = []ReplicaContext{
			{member: &v1alpha1.DatameshMember{Type: v1alpha1.DatameshMemberTypeDiskful, Zone: "b"}},
			{member: &v1alpha1.DatameshMember{Type: v1alpha1.DatameshMemberTypeDiskful, Zone: "a"}},
			{member: &v1alpha1.DatameshMember{Type: v1alpha1.DatameshMemberTypeDiskful, Zone: "b"}},
			{member: &v1alpha1.DatameshMember{Type: v1alpha1.DatameshMemberTypeDiskful, Zone: "a"}},
			{member: &v1alpha1.DatameshMember{Type: v1alpha1.DatameshMemberTypeDiskful, Zone: "c"}},
		}
		result := voterCountPerZone(gctx)
		Expect(result).To(Equal([]zoneCount{
			{Zone: "a", Count: 2},
			{Zone: "b", Count: 2},
			{Zone: "c", Count: 1},
		}))
	})

	It("empty", func() {
		gctx := &globalContext{}
		Expect(voterCountPerZone(gctx)).To(BeNil())
	})
})

var _ = Describe("findZoneCount", func() {
	It("found", func() {
		zones := []zoneCount{{Zone: "a", Count: 3}, {Zone: "b", Count: 1}}
		Expect(findZoneCount(zones, "a")).To(Equal(byte(3)))
	})

	It("not found returns 0", func() {
		zones := []zoneCount{{Zone: "a", Count: 3}}
		Expect(findZoneCount(zones, "x")).To(Equal(byte(0)))
	})
})

var _ = Describe("onLeaveComplete", func() {
	It("nil rctx → does not panic", func() {
		Expect(func() { onLeaveComplete(nil, nil) }).NotTo(Panic())
	})
})
