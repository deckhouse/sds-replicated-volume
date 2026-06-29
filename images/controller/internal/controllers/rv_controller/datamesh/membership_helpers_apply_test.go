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

var _ = Describe("createMember", func() {
	It("sets type, nodeName, zone, addresses, attached=false", func() {
		gctx := &globalContext{rsp: mkRSPWithZones("node-0", "zone-a")}
		rctx := &ReplicaContext{
			gctx:     gctx,
			id:       0,
			name:     "rv-1-0",
			nodeName: "node-0",
			rvr:      mkRVR("rv-1-0", "node-0", 1),
		}

		fn := createMember(v1alpha1.DatameshMemberTypeDiskful)
		fn(gctx, rctx)

		Expect(rctx.member).NotTo(BeNil())
		Expect(rctx.member.Name).To(Equal("rv-1-0"))
		Expect(rctx.member.Type).To(Equal(v1alpha1.DatameshMemberTypeDiskful))
		Expect(rctx.member.NodeName).To(Equal("node-0"))
		Expect(rctx.member.Zone).To(Equal("zone-a"))
		Expect(rctx.member.Addresses).To(HaveLen(1))
		Expect(rctx.member.Attached).To(BeFalse())
	})

	It("zone empty when RSP is nil", func() {
		gctx := &globalContext{rsp: nil}
		rctx := &ReplicaContext{
			gctx:     gctx,
			name:     "rv-1-0",
			nodeName: "node-0",
			rvr:      mkRVR("rv-1-0", "node-0", 1),
		}

		createMember(v1alpha1.DatameshMemberTypeAccess)(gctx, rctx)
		Expect(rctx.member.Zone).To(Equal(""))
	})
})

var _ = Describe("removeMember", func() {
	It("sets member to nil", func() {
		gctx := &globalContext{}
		rctx := &ReplicaContext{
			gctx:   gctx,
			id:     0,
			member: &v1alpha1.DatameshMember{Name: "rv-1-0"},
			rvr:    mkRVR("rv-1-0", "node-0", 1),
		}
		gctx.replicas[0] = rctx

		removeMember(gctx, rctx)
		Expect(rctx.member).To(BeNil())
		Expect(gctx.replicas[0]).NotTo(BeNil()) // rvr still present → keep in index
	})

	It("clears index when rvr is nil", func() {
		gctx := &globalContext{}
		rctx := &ReplicaContext{
			gctx:   gctx,
			id:     0,
			member: &v1alpha1.DatameshMember{Name: "rv-1-0"},
		}
		gctx.replicas[0] = rctx

		removeMember(gctx, rctx)
		Expect(rctx.member).To(BeNil())
		Expect(gctx.replicas[0]).To(BeNil()) // no rvr → cleared from index
	})
})

var _ = Describe("setType", func() {
	It("changes member type", func() {
		rctx := &ReplicaContext{
			member: &v1alpha1.DatameshMember{Type: v1alpha1.DatameshMemberTypeAccess},
		}
		setType(v1alpha1.DatameshMemberTypeTieBreaker)(nil, rctx)
		Expect(rctx.member.Type).To(Equal(v1alpha1.DatameshMemberTypeTieBreaker))
	})
})

var _ = Describe("setBackingVolumeFromRequest", func() {
	It("copies LVM fields from request", func() {
		rctx := &ReplicaContext{
			member: &v1alpha1.DatameshMember{},
			membershipRequest: &v1alpha1.ReplicatedVolumeDatameshReplicaRequest{
				Request: v1alpha1.DatameshMembershipRequest{
					LVMVolumeGroupName: "vg-1",
					ThinPoolName:       "thin-1",
				},
			},
		}
		setBackingVolumeFromRequest(nil, rctx)
		Expect(rctx.member.LVMVolumeGroupName).To(Equal("vg-1"))
		Expect(rctx.member.LVMVolumeGroupThinPoolName).To(Equal("thin-1"))
	})
})

var _ = Describe("clearBackingVolume", func() {
	It("clears LVM fields", func() {
		rctx := &ReplicaContext{
			member: &v1alpha1.DatameshMember{
				LVMVolumeGroupName:         "vg-1",
				LVMVolumeGroupThinPoolName: "thin-1",
			},
		}
		clearBackingVolume(nil, rctx)
		Expect(rctx.member.LVMVolumeGroupName).To(Equal(""))
		Expect(rctx.member.LVMVolumeGroupThinPoolName).To(Equal(""))
	})
})

var _ = Describe("raiseQ / lowerQ", func() {
	It("raiseQ increments quorum", func() {
		gctx := &globalContext{datamesh: datameshContext{quorum: 2}}
		raiseQ(gctx)
		Expect(gctx.datamesh.quorum).To(Equal(byte(3)))
	})

	It("lowerQ decrements quorum", func() {
		gctx := &globalContext{datamesh: datameshContext{quorum: 3}}
		lowerQ(gctx)
		Expect(gctx.datamesh.quorum).To(Equal(byte(2)))
	})
})

var _ = Describe("raiseQMR / lowerQMR", func() {
	It("raiseQMR increments qmr", func() {
		gctx := &globalContext{datamesh: datameshContext{quorumMinimumRedundancy: 1}}
		raiseQMR(gctx)
		Expect(gctx.datamesh.quorumMinimumRedundancy).To(Equal(byte(2)))
	})

	It("lowerQMR decrements qmr", func() {
		gctx := &globalContext{datamesh: datameshContext{quorumMinimumRedundancy: 2}}
		lowerQMR(gctx)
		Expect(gctx.datamesh.quorumMinimumRedundancy).To(Equal(byte(1)))
	})
})
