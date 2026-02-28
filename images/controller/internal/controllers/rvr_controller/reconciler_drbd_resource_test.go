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

package rvrcontroller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

var _ = Describe("applyDRBDConfiguredCondFalse", func() {
	It("sets condition to False", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}

		changed := applyDRBDConfiguredCondFalse(rvr, "TestReason", "Test message")

		Expect(changed).To(BeTrue())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal("TestReason"))
		Expect(cond.Message).To(Equal("Test message"))
	})
})

var _ = Describe("applyDRBDConfiguredCondUnknown", func() {
	It("sets condition to Unknown", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}

		changed := applyDRBDConfiguredCondUnknown(rvr, "TestReason", "Test message")

		Expect(changed).To(BeTrue())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
		Expect(cond.Reason).To(Equal("TestReason"))
		Expect(cond.Message).To(Equal("Test message"))
	})

	It("returns false when condition already matches (idempotent)", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}
		applyDRBDConfiguredCondUnknown(rvr, "TestReason", "Test message")

		changed := applyDRBDConfiguredCondUnknown(rvr, "TestReason", "Test message")

		Expect(changed).To(BeFalse())
	})
})

var _ = Describe("newDRBDR", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		_ = v1alpha1.AddToScheme(scheme)
	})

	It("creates DRBDR with correct name from RVR", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1", UID: "uid-1"},
		}
		spec := v1alpha1.DRBDResourceSpec{
			NodeName: "node-1",
			NodeID:   1,
		}

		drbdr, err := newDRBDR(scheme, rvr, spec)

		Expect(err).NotTo(HaveOccurred())
		Expect(drbdr.Name).To(Equal("rvr-1"))
	})

	It("creates DRBDR with finalizer", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1", UID: "uid-1"},
		}
		spec := v1alpha1.DRBDResourceSpec{
			NodeName: "node-1",
			NodeID:   1,
		}

		drbdr, err := newDRBDR(scheme, rvr, spec)

		Expect(err).NotTo(HaveOccurred())
		Expect(obju.HasFinalizer(drbdr, v1alpha1.RVRControllerFinalizer)).To(BeTrue())
	})

	It("creates DRBDR with owner reference to RVR", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1", UID: "uid-1"},
		}
		spec := v1alpha1.DRBDResourceSpec{
			NodeName: "node-1",
			NodeID:   1,
		}

		drbdr, err := newDRBDR(scheme, rvr, spec)

		Expect(err).NotTo(HaveOccurred())
		Expect(obju.HasControllerRef(drbdr, rvr)).To(BeTrue())
	})

	It("assigns spec to DRBDR", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1", UID: "uid-1"},
		}
		spec := v1alpha1.DRBDResourceSpec{
			NodeName: "node-1",
			NodeID:   1,
			Type:     v1alpha1.DRBDResourceTypeDiskful,
			State:    v1alpha1.DRBDResourceStateUp,
		}

		drbdr, err := newDRBDR(scheme, rvr, spec)

		Expect(err).NotTo(HaveOccurred())
		Expect(drbdr.Spec.NodeName).To(Equal("node-1"))
		Expect(drbdr.Spec.NodeID).To(Equal(uint8(1)))
		Expect(drbdr.Spec.Type).To(Equal(v1alpha1.DRBDResourceTypeDiskful))
		Expect(drbdr.Spec.State).To(Equal(v1alpha1.DRBDResourceStateUp))
	})
})

var _ = Describe("buildDRBDRPeerPaths", func() {
	It("returns empty slice for empty addresses", func() {
		paths := buildDRBDRPeerPaths(nil)
		Expect(paths).To(HaveLen(0))
	})

	It("returns empty slice for empty addresses slice", func() {
		paths := buildDRBDRPeerPaths([]v1alpha1.DRBDResourceAddressStatus{})
		Expect(paths).To(HaveLen(0))
	})

	It("builds single path from single address", func() {
		addresses := []v1alpha1.DRBDResourceAddressStatus{
			{SystemNetworkName: "net-1", Address: v1alpha1.DRBDAddress{IPv4: "10.0.0.1", Port: 7000}},
		}

		paths := buildDRBDRPeerPaths(addresses)

		Expect(paths).To(HaveLen(1))
		Expect(paths[0].SystemNetworkName).To(Equal("net-1"))
		Expect(paths[0].Address.IPv4).To(Equal("10.0.0.1"))
		Expect(paths[0].Address.Port).To(Equal(uint(7000)))
	})

	It("builds multiple paths from multiple addresses", func() {
		addresses := []v1alpha1.DRBDResourceAddressStatus{
			{SystemNetworkName: "net-1", Address: v1alpha1.DRBDAddress{IPv4: "10.0.0.1", Port: 7000}},
			{SystemNetworkName: "net-2", Address: v1alpha1.DRBDAddress{IPv4: "10.0.0.2", Port: 7001}},
		}

		paths := buildDRBDRPeerPaths(addresses)

		Expect(paths).To(HaveLen(2))
		Expect(paths[0].SystemNetworkName).To(Equal("net-1"))
		Expect(paths[0].Address.IPv4).To(Equal("10.0.0.1"))
		Expect(paths[1].SystemNetworkName).To(Equal("net-2"))
		Expect(paths[1].Address.IPv4).To(Equal("10.0.0.2"))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// DRBDR Compute helpers tests
//

var _ = Describe("computeIntendedType", func() {
	It("returns RVR spec type when member is nil", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				Type: v1alpha1.ReplicaTypeDiskful,
			},
		}

		result := computeIntendedType(rvr, nil)

		Expect(result).To(Equal(v1alpha1.DatameshMemberTypeDiskful))
	})

	It("returns Diskless from RVR spec when member is nil", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				Type: v1alpha1.ReplicaTypeAccess,
			},
		}

		result := computeIntendedType(rvr, nil)

		Expect(result).To(Equal(v1alpha1.DatameshMemberTypeAccess))
	})

	It("returns member type when no transition", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{}
		member := &v1alpha1.DatameshMember{
			Type: v1alpha1.DatameshMemberTypeDiskful,
		}

		result := computeIntendedType(rvr, member)

		Expect(result).To(Equal(v1alpha1.DatameshMemberTypeDiskful))
	})

	It("returns LiminalDiskful for liminal Diskful member", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{}
		member := &v1alpha1.DatameshMember{
			Type: v1alpha1.DatameshMemberTypeLiminalDiskful,
		}

		result := computeIntendedType(rvr, member)

		Expect(result).To(Equal(v1alpha1.DatameshMemberTypeLiminalDiskful))
	})

	It("returns ShadowDiskful from RVR spec when member is nil", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				Type: v1alpha1.ReplicaTypeShadowDiskful,
			},
		}

		result := computeIntendedType(rvr, nil)

		Expect(result).To(Equal(v1alpha1.DatameshMemberTypeShadowDiskful))
	})

	It("returns ShadowDiskful member type directly", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{}
		member := &v1alpha1.DatameshMember{
			Type: v1alpha1.DatameshMemberTypeShadowDiskful,
		}

		result := computeIntendedType(rvr, member)

		Expect(result).To(Equal(v1alpha1.DatameshMemberTypeShadowDiskful))
	})
})

var _ = Describe("computeTargetType", func() {
	It("returns LiminalDiskful when intended is Diskful but no BV", func() {
		result := computeTargetType(v1alpha1.DatameshMemberTypeDiskful, "")

		Expect(result).To(Equal(v1alpha1.DatameshMemberTypeLiminalDiskful))
	})

	It("returns LiminalShadowDiskful when intended is ShadowDiskful but no BV", func() {
		result := computeTargetType(v1alpha1.DatameshMemberTypeShadowDiskful, "")

		Expect(result).To(Equal(v1alpha1.DatameshMemberTypeLiminalShadowDiskful))
	})

	It("returns ShadowDiskful when intended is ShadowDiskful and BV ready", func() {
		result := computeTargetType(v1alpha1.DatameshMemberTypeShadowDiskful, "llv-1")

		Expect(result).To(Equal(v1alpha1.DatameshMemberTypeShadowDiskful))
	})

	It("returns Diskful when intended is Diskful and BV ready", func() {
		result := computeTargetType(v1alpha1.DatameshMemberTypeDiskful, "llv-1")

		Expect(result).To(Equal(v1alpha1.DatameshMemberTypeDiskful))
	})

	It("returns Access unchanged regardless of BV", func() {
		result := computeTargetType(v1alpha1.DatameshMemberTypeAccess, "")

		Expect(result).To(Equal(v1alpha1.DatameshMemberTypeAccess))
	})

	It("returns TieBreaker unchanged regardless of BV", func() {
		result := computeTargetType(v1alpha1.DatameshMemberTypeTieBreaker, "")

		Expect(result).To(Equal(v1alpha1.DatameshMemberTypeTieBreaker))
	})
})

var _ = Describe("computeDRBDRType", func() {
	It("converts Diskful to DRBDResourceTypeDiskful", func() {
		result := computeDRBDRType(v1alpha1.DatameshMemberTypeDiskful)

		Expect(result).To(Equal(v1alpha1.DRBDResourceTypeDiskful))
	})

	It("converts Access to DRBDResourceTypeDiskless", func() {
		result := computeDRBDRType(v1alpha1.DatameshMemberTypeAccess)

		Expect(result).To(Equal(v1alpha1.DRBDResourceTypeDiskless))
	})

	It("converts TieBreaker to DRBDResourceTypeDiskless", func() {
		result := computeDRBDRType(v1alpha1.DatameshMemberTypeTieBreaker)

		Expect(result).To(Equal(v1alpha1.DRBDResourceTypeDiskless))
	})

	It("converts ShadowDiskful to DRBDResourceTypeDiskful", func() {
		result := computeDRBDRType(v1alpha1.DatameshMemberTypeShadowDiskful)

		Expect(result).To(Equal(v1alpha1.DRBDResourceTypeDiskful))
	})

	It("converts LiminalShadowDiskful to DRBDResourceTypeDiskless", func() {
		result := computeDRBDRType(v1alpha1.DatameshMemberTypeLiminalShadowDiskful)

		Expect(result).To(Equal(v1alpha1.DRBDResourceTypeDiskless))
	})
})

var _ = Describe("computeTargetDRBDRSpec", func() {
	It("creates new spec with NodeName and NodeID (DRBDR) from RVR when drbdr is nil", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "pvc-abc-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				NodeName: "node-1",
			},
		}
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{
			SystemNetworkNames: []string{"net-1"},
		}

		spec := computeTargetDRBDRSpec(rvr, nil, datamesh, nil, "", v1alpha1.DatameshMemberType(rvr.Spec.Type))

		Expect(spec.NodeName).To(Equal("node-1"))
		Expect(spec.NodeID).To(Equal(uint8(1)))
	})

	It("preserves NodeName and NodeID (DRBDR) from existing drbdr", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "pvc-abc-2"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				NodeName: "node-1",
			},
		}
		drbdr := &v1alpha1.DRBDResource{
			Spec: v1alpha1.DRBDResourceSpec{
				NodeName: "existing-node",
				NodeID:   99,
			},
		}
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{
			SystemNetworkNames: []string{"net-1"},
		}

		spec := computeTargetDRBDRSpec(rvr, drbdr, datamesh, nil, "", v1alpha1.DatameshMemberType(rvr.Spec.Type))

		Expect(spec.NodeName).To(Equal("existing-node"))
		Expect(spec.NodeID).To(Equal(uint8(99)))
	})

	It("sets Type based on intended replica type", func() {
		rvrDiskful := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "pvc-abc-1"},
			Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-1", Type: v1alpha1.ReplicaTypeDiskful},
		}
		rvrAccess := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "pvc-abc-1"},
			Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-1", Type: v1alpha1.ReplicaTypeAccess},
		}
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{}

		specDiskful := computeTargetDRBDRSpec(rvrDiskful, nil, datamesh, nil, "llv-1", v1alpha1.DatameshMemberTypeDiskful)
		specDiskless := computeTargetDRBDRSpec(rvrAccess, nil, datamesh, nil, "", v1alpha1.DatameshMemberTypeAccess)

		Expect(specDiskful.Type).To(Equal(v1alpha1.DRBDResourceTypeDiskful))
		Expect(specDiskless.Type).To(Equal(v1alpha1.DRBDResourceTypeDiskless))
	})

	It("sets LVMLogicalVolumeName and Size for Diskful type", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "pvc-abc-1"},
			Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-1", Type: v1alpha1.ReplicaTypeDiskful},
		}
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{
			Size: resource.MustParse("10Gi"),
		}

		spec := computeTargetDRBDRSpec(rvr, nil, datamesh, nil, "llv-1", v1alpha1.DatameshMemberTypeDiskful)

		Expect(spec.LVMLogicalVolumeName).To(Equal("llv-1"))
		Expect(spec.Size).NotTo(BeNil())
		Expect(spec.Size.String()).To(Equal("10Gi"))
	})

	It("clears LVMLogicalVolumeName and Size for Diskless type", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "pvc-abc-1"},
			Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-1"},
		}
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{
			Size: resource.MustParse("10Gi"),
		}

		spec := computeTargetDRBDRSpec(rvr, nil, datamesh, nil, "", v1alpha1.DatameshMemberType(rvr.Spec.Type))

		Expect(spec.LVMLogicalVolumeName).To(BeEmpty())
		Expect(spec.Size).To(BeNil())
	})

	It("sets Role to Secondary when member is nil", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "pvc-abc-1"},
			Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-1"},
		}
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{}

		spec := computeTargetDRBDRSpec(rvr, nil, datamesh, nil, "", v1alpha1.DatameshMemberType(rvr.Spec.Type))

		Expect(spec.Role).To(Equal(v1alpha1.DRBDRoleSecondary))
	})

	It("sets Peers to nil when member is nil", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "pvc-abc-1"},
			Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-1"},
		}
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{}

		spec := computeTargetDRBDRSpec(rvr, nil, datamesh, nil, "", v1alpha1.DatameshMemberType(rvr.Spec.Type))

		Expect(spec.Peers).To(BeNil())
	})

	It("sets Role from member when member is present", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "pvc-abc-1"},
			Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-1"},
		}
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{}
		member := &v1alpha1.DatameshMember{
			Name:     "pvc-abc-1",
			Attached: true,
		}

		spec := computeTargetDRBDRSpec(rvr, nil, datamesh, member, "", member.Type)

		Expect(spec.Role).To(Equal(v1alpha1.DRBDRolePrimary))
	})

	It("sets AllowTwoPrimaries from datamesh.Multiattach when member is present", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "pvc-abc-1"},
			Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-1"},
		}
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{
			Multiattach: true,
		}
		member := &v1alpha1.DatameshMember{
			Name: "pvc-abc-1",
		}

		spec := computeTargetDRBDRSpec(rvr, nil, datamesh, member, "", member.Type)

		Expect(spec.AllowTwoPrimaries).To(BeTrue())
	})

	It("sets AllowTwoPrimaries=false when member and datamesh.Multiattach=false", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "pvc-abc-1"},
			Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-1"},
		}
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{
			Multiattach: false,
		}
		member := &v1alpha1.DatameshMember{
			Name: "pvc-abc-1",
		}

		spec := computeTargetDRBDRSpec(rvr, nil, datamesh, member, "", member.Type)

		Expect(spec.AllowTwoPrimaries).To(BeFalse())
	})

	It("sets quorum from datamesh for diskful member", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "pvc-abc-1"},
			Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-1", Type: v1alpha1.ReplicaTypeDiskful},
		}
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{
			Quorum:                  3,
			QuorumMinimumRedundancy: 2,
		}
		member := &v1alpha1.DatameshMember{
			Name: "pvc-abc-1",
			Type: v1alpha1.DatameshMemberTypeDiskful,
		}

		spec := computeTargetDRBDRSpec(rvr, nil, datamesh, member, "llv-1", v1alpha1.DatameshMemberTypeDiskful)

		Expect(spec.Quorum).To(Equal(byte(3)))
		Expect(spec.QuorumMinimumRedundancy).To(Equal(byte(2)))
	})

	It("sets quorum=32 and QMR from datamesh for diskless member", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "pvc-abc-1"},
			Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-1"},
		}
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{
			Quorum:                  3,
			QuorumMinimumRedundancy: 2,
		}
		member := &v1alpha1.DatameshMember{
			Name: "pvc-abc-1",
		}

		spec := computeTargetDRBDRSpec(rvr, nil, datamesh, member, "", member.Type)

		Expect(spec.Quorum).To(Equal(byte(32)))
		Expect(spec.QuorumMinimumRedundancy).To(Equal(byte(2)))
	})

	It("sets NonVoting=true for ShadowDiskful member with BV", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "pvc-abc-1"},
			Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-1", Type: v1alpha1.ReplicaTypeShadowDiskful},
		}
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{
			Size: resource.MustParse("10Gi"),
		}
		member := &v1alpha1.DatameshMember{
			Name: "pvc-abc-1",
			Type: v1alpha1.DatameshMemberTypeShadowDiskful,
		}

		spec := computeTargetDRBDRSpec(rvr, nil, datamesh, member, "llv-1", v1alpha1.DatameshMemberTypeShadowDiskful)

		Expect(spec.NonVoting).To(BeTrue())
		Expect(spec.Type).To(Equal(v1alpha1.DRBDResourceTypeDiskful))
	})

	It("sets NonVoting=false for Diskful member with BV", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "pvc-abc-1"},
			Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-1", Type: v1alpha1.ReplicaTypeDiskful},
		}
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{
			Size: resource.MustParse("10Gi"),
		}
		member := &v1alpha1.DatameshMember{
			Name: "pvc-abc-1",
			Type: v1alpha1.DatameshMemberTypeDiskful,
		}

		spec := computeTargetDRBDRSpec(rvr, nil, datamesh, member, "llv-1", v1alpha1.DatameshMemberTypeDiskful)

		Expect(spec.NonVoting).To(BeFalse())
	})

	It("sets quorum=32 for ShadowDiskful member (non-voter with disk)", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "pvc-abc-1"},
			Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-1", Type: v1alpha1.ReplicaTypeShadowDiskful},
		}
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{
			Quorum:                  3,
			QuorumMinimumRedundancy: 2,
			Size:                    resource.MustParse("10Gi"),
		}
		member := &v1alpha1.DatameshMember{
			Name: "pvc-abc-1",
			Type: v1alpha1.DatameshMemberTypeShadowDiskful,
		}

		spec := computeTargetDRBDRSpec(rvr, nil, datamesh, member, "llv-1", v1alpha1.DatameshMemberTypeShadowDiskful)

		Expect(spec.Quorum).To(Equal(byte(32)))
		Expect(spec.QuorumMinimumRedundancy).To(Equal(byte(2)))
	})

	It("sets quorum=32 and QMR=32 for non-member", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "pvc-abc-1"},
			Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-1"},
		}
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{
			Quorum:                  3,
			QuorumMinimumRedundancy: 2,
		}

		spec := computeTargetDRBDRSpec(rvr, nil, datamesh, nil, "", v1alpha1.DatameshMemberType(rvr.Spec.Type))

		Expect(spec.Quorum).To(Equal(byte(32)))
		Expect(spec.QuorumMinimumRedundancy).To(Equal(byte(32)))
	})

	It("clones SystemNetworks from datamesh without aliasing", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "pvc-abc-1"},
			Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-1"},
		}
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{
			SystemNetworkNames: []string{"net-1", "net-2"},
		}

		spec := computeTargetDRBDRSpec(rvr, nil, datamesh, nil, "", v1alpha1.DatameshMemberType(rvr.Spec.Type))

		Expect(spec.SystemNetworks).To(Equal([]string{"net-1", "net-2"}))
		// Verify no aliasing: mutating the result should not affect datamesh.
		spec.SystemNetworks[0] = "mutated"
		Expect(datamesh.SystemNetworkNames[0]).To(Equal("net-1"))
	})

	It("sets State to Up always", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "pvc-abc-1"},
			Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "node-1"},
		}
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{}

		spec := computeTargetDRBDRSpec(rvr, nil, datamesh, nil, "", v1alpha1.DatameshMemberType(rvr.Spec.Type))

		Expect(spec.State).To(Equal(v1alpha1.DRBDResourceStateUp))
	})
})

var _ = Describe("computeTargetDRBDRPeers", func() {
	It("returns nil when datamesh has single member", func() {
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{
			Members: []v1alpha1.DatameshMember{
				{Name: "pvc-abc-1", Type: v1alpha1.DatameshMemberTypeDiskful},
			},
		}
		self := &datamesh.Members[0]

		peers := computeTargetDRBDRPeers(datamesh, self)

		Expect(peers).To(BeNil())
	})

	It("excludes self from peers", func() {
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{
			Members: []v1alpha1.DatameshMember{
				{Name: "pvc-abc-1", Type: v1alpha1.DatameshMemberTypeDiskful},
				{Name: "pvc-abc-2", Type: v1alpha1.DatameshMemberTypeDiskful},
			},
		}
		self := &datamesh.Members[0]

		peers := computeTargetDRBDRPeers(datamesh, self)

		Expect(peers).To(HaveLen(1))
		Expect(peers[0].Name).To(Equal("pvc-abc-2"))
	})

	It("Diskful connects to all peers", func() {
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{
			Members: []v1alpha1.DatameshMember{
				{Name: "pvc-abc-1", Type: v1alpha1.DatameshMemberTypeDiskful},
				{Name: "pvc-abc-2", Type: v1alpha1.DatameshMemberTypeDiskful},
				{Name: "pvc-abc-3", Type: v1alpha1.DatameshMemberTypeAccess},
			},
		}
		self := &datamesh.Members[0]

		peers := computeTargetDRBDRPeers(datamesh, self)

		Expect(peers).To(HaveLen(2))
	})

	It("Access connects only to Diskful peers", func() {
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{
			Members: []v1alpha1.DatameshMember{
				{Name: "pvc-abc-1", Type: v1alpha1.DatameshMemberTypeAccess},
				{Name: "pvc-abc-2", Type: v1alpha1.DatameshMemberTypeDiskful},
				{Name: "pvc-abc-3", Type: v1alpha1.DatameshMemberTypeAccess},
			},
		}
		self := &datamesh.Members[0]

		peers := computeTargetDRBDRPeers(datamesh, self)

		Expect(peers).To(HaveLen(1))
		Expect(peers[0].Name).To(Equal("pvc-abc-2"))
	})

	It("liminal Diskful connects to all peers like regular Diskful", func() {
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{
			Members: []v1alpha1.DatameshMember{
				{Name: "pvc-abc-1", Type: v1alpha1.DatameshMemberTypeLiminalDiskful},
				{Name: "pvc-abc-2", Type: v1alpha1.DatameshMemberTypeDiskful},
				{Name: "pvc-abc-3", Type: v1alpha1.DatameshMemberTypeAccess},
			},
		}
		self := &datamesh.Members[0]

		peers := computeTargetDRBDRPeers(datamesh, self)

		Expect(peers).To(HaveLen(2))
	})

	It("Access connects to liminal Diskful as Diskful peer", func() {
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{
			Members: []v1alpha1.DatameshMember{
				{Name: "pvc-abc-1", Type: v1alpha1.DatameshMemberTypeAccess},
				{Name: "pvc-abc-2", Type: v1alpha1.DatameshMemberTypeLiminalDiskful},
			},
		}
		self := &datamesh.Members[0]

		peers := computeTargetDRBDRPeers(datamesh, self)

		Expect(peers).To(HaveLen(1))
		Expect(peers[0].Name).To(Equal("pvc-abc-2"))
		Expect(peers[0].Type).To(Equal(v1alpha1.DRBDResourceTypeDiskful))
	})

	It("sets AllowRemoteRead to false for Access peers", func() {
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{
			Members: []v1alpha1.DatameshMember{
				{Name: "pvc-abc-1", Type: v1alpha1.DatameshMemberTypeDiskful},
				{Name: "pvc-abc-2", Type: v1alpha1.DatameshMemberTypeAccess},
			},
		}
		self := &datamesh.Members[0]

		peers := computeTargetDRBDRPeers(datamesh, self)

		Expect(peers).To(HaveLen(1))
		Expect(peers[0].AllowRemoteRead).To(BeFalse())
	})

	It("sets AllowRemoteRead to true for non-Access peers", func() {
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{
			Members: []v1alpha1.DatameshMember{
				{Name: "pvc-abc-1", Type: v1alpha1.DatameshMemberTypeDiskful},
				{Name: "pvc-abc-2", Type: v1alpha1.DatameshMemberTypeDiskful},
			},
		}
		self := &datamesh.Members[0]

		peers := computeTargetDRBDRPeers(datamesh, self)

		Expect(peers).To(HaveLen(1))
		Expect(peers[0].AllowRemoteRead).To(BeTrue())
	})

	It("copies SharedSecret and SharedSecretAlg from datamesh", func() {
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{
			SharedSecret:    "secret123",
			SharedSecretAlg: v1alpha1.SharedSecretAlgSHA256,
			Members: []v1alpha1.DatameshMember{
				{Name: "pvc-abc-1", Type: v1alpha1.DatameshMemberTypeDiskful},
				{Name: "pvc-abc-2", Type: v1alpha1.DatameshMemberTypeDiskful},
			},
		}
		self := &datamesh.Members[0]

		peers := computeTargetDRBDRPeers(datamesh, self)

		Expect(peers[0].SharedSecret).To(Equal("secret123"))
		Expect(peers[0].SharedSecretAlg).To(Equal(v1alpha1.SharedSecretAlgSHA256))
	})

	It("builds peer paths from member addresses", func() {
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{
			Members: []v1alpha1.DatameshMember{
				{Name: "pvc-abc-1", Type: v1alpha1.DatameshMemberTypeDiskful},
				{Name: "pvc-abc-2", Type: v1alpha1.DatameshMemberTypeDiskful, Addresses: []v1alpha1.DRBDResourceAddressStatus{
					{SystemNetworkName: "net-1", Address: v1alpha1.DRBDAddress{IPv4: "10.0.0.1", Port: 7000}},
				}},
			},
		}
		self := &datamesh.Members[0]

		peers := computeTargetDRBDRPeers(datamesh, self)

		Expect(peers[0].Paths).To(HaveLen(1))
		Expect(peers[0].Paths[0].Address.IPv4).To(Equal("10.0.0.1"))
	})

	It("sorts peers by Name for deterministic output", func() {
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{
			Members: []v1alpha1.DatameshMember{
				{Name: "pvc-abc-1", Type: v1alpha1.DatameshMemberTypeDiskful},
				{Name: "pvc-abc-4", Type: v1alpha1.DatameshMemberTypeDiskful},
				{Name: "pvc-abc-2", Type: v1alpha1.DatameshMemberTypeDiskful},
				{Name: "pvc-abc-3", Type: v1alpha1.DatameshMemberTypeDiskful},
			},
		}
		self := &datamesh.Members[0]

		peers := computeTargetDRBDRPeers(datamesh, self)

		Expect(peers).To(HaveLen(3))
		Expect(peers[0].Name).To(Equal("pvc-abc-2"))
		Expect(peers[1].Name).To(Equal("pvc-abc-3"))
		Expect(peers[2].Name).To(Equal("pvc-abc-4"))
	})

	It("sets Protocol to C for all peers", func() {
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{
			Members: []v1alpha1.DatameshMember{
				{Name: "pvc-abc-1", Type: v1alpha1.DatameshMemberTypeDiskful},
				{Name: "pvc-abc-2", Type: v1alpha1.DatameshMemberTypeDiskful},
				{Name: "pvc-abc-3", Type: v1alpha1.DatameshMemberTypeTieBreaker},
			},
		}
		self := &datamesh.Members[0]

		peers := computeTargetDRBDRPeers(datamesh, self)

		Expect(peers).To(HaveLen(2))
		for _, p := range peers {
			Expect(p.Protocol).To(Equal(v1alpha1.DRBDProtocolC))
		}
	})

	It("ShadowDiskful connects to all peers including Access and TieBreaker", func() {
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{
			Members: []v1alpha1.DatameshMember{
				{Name: "pvc-abc-1", Type: v1alpha1.DatameshMemberTypeShadowDiskful},
				{Name: "pvc-abc-2", Type: v1alpha1.DatameshMemberTypeDiskful},
				{Name: "pvc-abc-3", Type: v1alpha1.DatameshMemberTypeAccess},
				{Name: "pvc-abc-4", Type: v1alpha1.DatameshMemberTypeTieBreaker},
			},
		}
		self := &datamesh.Members[0]

		peers := computeTargetDRBDRPeers(datamesh, self)

		Expect(peers).To(HaveLen(3))
	})

	It("sets AllowRemoteRead=false for ShadowDiskful peers", func() {
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{
			Members: []v1alpha1.DatameshMember{
				{Name: "pvc-abc-1", Type: v1alpha1.DatameshMemberTypeDiskful},
				{Name: "pvc-abc-2", Type: v1alpha1.DatameshMemberTypeShadowDiskful},
			},
		}
		self := &datamesh.Members[0]

		peers := computeTargetDRBDRPeers(datamesh, self)

		Expect(peers).To(HaveLen(1))
		Expect(peers[0].Name).To(Equal("pvc-abc-2"))
		Expect(peers[0].AllowRemoteRead).To(BeFalse())
	})

	It("LiminalShadowDiskful peer type is Diskful", func() {
		datamesh := &v1alpha1.ReplicatedVolumeDatamesh{
			Members: []v1alpha1.DatameshMember{
				{Name: "pvc-abc-1", Type: v1alpha1.DatameshMemberTypeAccess},
				{Name: "pvc-abc-2", Type: v1alpha1.DatameshMemberTypeLiminalShadowDiskful},
			},
		}
		self := &datamesh.Members[0]

		peers := computeTargetDRBDRPeers(datamesh, self)

		Expect(peers).To(HaveLen(1))
		Expect(peers[0].Name).To(Equal("pvc-abc-2"))
		Expect(peers[0].Type).To(Equal(v1alpha1.DRBDResourceTypeDiskful))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Additional Apply helpers tests
//

var _ = Describe("applyDRBDConfiguredCondTrue", func() {
	It("sets condition to True", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}

		changed := applyDRBDConfiguredCondTrue(rvr, "TestReason", "Test message")

		Expect(changed).To(BeTrue())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal("TestReason"))
		Expect(cond.Message).To(Equal("Test message"))
	})

	It("returns false when condition already True with same reason", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}
		obju.SetStatusCondition(rvr, metav1.Condition{
			Type:    v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType,
			Status:  metav1.ConditionTrue,
			Reason:  "TestReason",
			Message: "Test message",
		})

		changed := applyDRBDConfiguredCondTrue(rvr, "TestReason", "Test message")

		Expect(changed).To(BeFalse())
	})
})

var _ = Describe("newDRBDRReconciliationCache", func() {
	It("returns cache with provided values", func() {
		cache := newDRBDRReconciliationCache(5, 3, v1alpha1.DatameshMemberTypeDiskful)

		Expect(cache.DatameshRevision).To(Equal(int64(5)))
		Expect(cache.DRBDRGeneration).To(Equal(int64(3)))
		Expect(cache.TargetType).To(Equal(v1alpha1.DatameshMemberTypeDiskful))
	})

	It("handles zero values", func() {
		cache := newDRBDRReconciliationCache(0, 0, "")

		Expect(cache.DatameshRevision).To(Equal(int64(0)))
		Expect(cache.DRBDRGeneration).To(Equal(int64(0)))
		Expect(cache.TargetType).To(Equal(v1alpha1.DatameshMemberType("")))
	})
})

var _ = Describe("applyRVRDRBDRReconciliationCache", func() {
	It("sets all fields when different", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}
		target := v1alpha1.ReplicatedVolumeReplicaStatusDRBDRReconciliationCache{
			DatameshRevision: 5,
			DRBDRGeneration:  3,
			TargetType:       v1alpha1.DatameshMemberTypeDiskful,
		}

		changed := applyRVRDRBDRReconciliationCache(rvr, target)

		Expect(changed).To(BeTrue())
		Expect(rvr.Status.DRBDRReconciliationCache).To(Equal(target))
	})

	It("returns false when all fields are same", func() {
		target := v1alpha1.ReplicatedVolumeReplicaStatusDRBDRReconciliationCache{
			DatameshRevision: 5,
			DRBDRGeneration:  3,
			TargetType:       v1alpha1.DatameshMemberTypeDiskful,
		}
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DRBDRReconciliationCache: target,
			},
		}

		changed := applyRVRDRBDRReconciliationCache(rvr, target)

		Expect(changed).To(BeFalse())
	})

	It("returns true when only datamesh revision differs", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DRBDRReconciliationCache: v1alpha1.ReplicatedVolumeReplicaStatusDRBDRReconciliationCache{
					DatameshRevision: 4,
					DRBDRGeneration:  3,
					TargetType:       v1alpha1.DatameshMemberTypeDiskful,
				},
			},
		}
		target := v1alpha1.ReplicatedVolumeReplicaStatusDRBDRReconciliationCache{
			DatameshRevision: 5,
			DRBDRGeneration:  3,
			TargetType:       v1alpha1.DatameshMemberTypeDiskful,
		}

		changed := applyRVRDRBDRReconciliationCache(rvr, target)

		Expect(changed).To(BeTrue())
		Expect(rvr.Status.DRBDRReconciliationCache).To(Equal(target))
	})

	It("returns true when only drbdr generation differs", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DRBDRReconciliationCache: v1alpha1.ReplicatedVolumeReplicaStatusDRBDRReconciliationCache{
					DatameshRevision: 5,
					DRBDRGeneration:  2,
					TargetType:       v1alpha1.DatameshMemberTypeDiskful,
				},
			},
		}
		target := v1alpha1.ReplicatedVolumeReplicaStatusDRBDRReconciliationCache{
			DatameshRevision: 5,
			DRBDRGeneration:  3,
			TargetType:       v1alpha1.DatameshMemberTypeDiskful,
		}

		changed := applyRVRDRBDRReconciliationCache(rvr, target)

		Expect(changed).To(BeTrue())
		Expect(rvr.Status.DRBDRReconciliationCache).To(Equal(target))
	})

	It("returns true when only rvr type differs", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DRBDRReconciliationCache: v1alpha1.ReplicatedVolumeReplicaStatusDRBDRReconciliationCache{
					DatameshRevision: 5,
					DRBDRGeneration:  3,
					TargetType:       v1alpha1.DatameshMemberTypeAccess,
				},
			},
		}
		target := v1alpha1.ReplicatedVolumeReplicaStatusDRBDRReconciliationCache{
			DatameshRevision: 5,
			DRBDRGeneration:  3,
			TargetType:       v1alpha1.DatameshMemberTypeDiskful,
		}

		changed := applyRVRDRBDRReconciliationCache(rvr, target)

		Expect(changed).To(BeTrue())
		Expect(rvr.Status.DRBDRReconciliationCache).To(Equal(target))
	})

	It("handles zero values", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Status: v1alpha1.ReplicatedVolumeReplicaStatus{
				DRBDRReconciliationCache: v1alpha1.ReplicatedVolumeReplicaStatusDRBDRReconciliationCache{
					DatameshRevision: 5,
					DRBDRGeneration:  3,
					TargetType:       v1alpha1.DatameshMemberTypeDiskful,
				},
			},
		}
		target := v1alpha1.ReplicatedVolumeReplicaStatusDRBDRReconciliationCache{}

		changed := applyRVRDRBDRReconciliationCache(rvr, target)

		Expect(changed).To(BeTrue())
		Expect(rvr.Status.DRBDRReconciliationCache).To(Equal(target))
	})
})
