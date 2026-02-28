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
	"context"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes/testhelpers"
	rvrllvname "github.com/deckhouse/sds-replicated-volume/images/controller/internal/rvr_llv_name"
)

var _ = Describe("computeActualBackingVolume", func() {
	It("returns nil for nil DRBDResource", func() {
		bv := computeActualBackingVolume(nil, nil)
		Expect(bv).To(BeNil())
	})

	It("returns nil when DRBDResource has empty LVMLogicalVolumeName", func() {
		drbdr := &v1alpha1.DRBDResource{
			ObjectMeta: metav1.ObjectMeta{Name: "drbdr-1"},
			Spec:       v1alpha1.DRBDResourceSpec{LVMLogicalVolumeName: ""},
		}
		bv := computeActualBackingVolume(drbdr, nil)
		Expect(bv).To(BeNil())
	})

	It("returns nil when referenced LLV is not found", func() {
		drbdr := &v1alpha1.DRBDResource{
			ObjectMeta: metav1.ObjectMeta{Name: "drbdr-1"},
			Spec:       v1alpha1.DRBDResourceSpec{LVMLogicalVolumeName: "llv-1"},
		}
		llvs := []snc.LVMLogicalVolume{
			{ObjectMeta: metav1.ObjectMeta{Name: "llv-other"}},
		}
		bv := computeActualBackingVolume(drbdr, llvs)
		Expect(bv).To(BeNil())
	})

	It("returns backing volume for thick volume", func() {
		drbdr := &v1alpha1.DRBDResource{
			ObjectMeta: metav1.ObjectMeta{Name: "drbdr-1"},
			Spec:       v1alpha1.DRBDResourceSpec{LVMLogicalVolumeName: "llv-1"},
		}
		llvs := []snc.LVMLogicalVolume{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "llv-1"},
				Spec: snc.LVMLogicalVolumeSpec{
					LVMVolumeGroupName: "lvg-1",
					Type:               "Thick",
				},
				Status: &snc.LVMLogicalVolumeStatus{
					ActualSize: resource.MustParse("10Gi"),
				},
			},
		}

		bv := computeActualBackingVolume(drbdr, llvs)

		Expect(bv).NotTo(BeNil())
		Expect(bv.LLVName).To(Equal("llv-1"))
		Expect(bv.LVMVolumeGroupName).To(Equal("lvg-1"))
		Expect(bv.ThinPoolName).To(BeEmpty())
		Expect(bv.Size.String()).To(Equal("10Gi"))
	})

	It("returns backing volume for thin volume", func() {
		drbdr := &v1alpha1.DRBDResource{
			ObjectMeta: metav1.ObjectMeta{Name: "drbdr-1"},
			Spec:       v1alpha1.DRBDResourceSpec{LVMLogicalVolumeName: "llv-1"},
		}
		llvs := []snc.LVMLogicalVolume{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "llv-1"},
				Spec: snc.LVMLogicalVolumeSpec{
					LVMVolumeGroupName: "lvg-1",
					Type:               "Thin",
					Thin:               &snc.LVMLogicalVolumeThinSpec{PoolName: "thinpool-1"},
				},
				Status: &snc.LVMLogicalVolumeStatus{
					ActualSize: resource.MustParse("5Gi"),
				},
			},
		}

		bv := computeActualBackingVolume(drbdr, llvs)

		Expect(bv).NotTo(BeNil())
		Expect(bv.LLVName).To(Equal("llv-1"))
		Expect(bv.LVMVolumeGroupName).To(Equal("lvg-1"))
		Expect(bv.ThinPoolName).To(Equal("thinpool-1"))
		Expect(bv.Size.String()).To(Equal("5Gi"))
	})

	It("returns zero size when LLV has no status", func() {
		drbdr := &v1alpha1.DRBDResource{
			ObjectMeta: metav1.ObjectMeta{Name: "drbdr-1"},
			Spec:       v1alpha1.DRBDResourceSpec{LVMLogicalVolumeName: "llv-1"},
		}
		llvs := []snc.LVMLogicalVolume{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "llv-1"},
				Spec: snc.LVMLogicalVolumeSpec{
					LVMVolumeGroupName: "lvg-1",
				},
				Status: nil,
			},
		}

		bv := computeActualBackingVolume(drbdr, llvs)

		Expect(bv).NotTo(BeNil())
		Expect(bv.Size.IsZero()).To(BeTrue())
	})
})

var _ = Describe("backingVolume.Equal", func() {
	It("returns true when both are nil", func() {
		var a, b *backingVolume
		Expect(a.Equal(b)).To(BeTrue())
	})

	It("returns false when first is nil and second is not", func() {
		var a *backingVolume
		b := &backingVolume{LLVName: "llv-1"}
		Expect(a.Equal(b)).To(BeFalse())
	})

	It("returns false when first is not nil and second is nil", func() {
		a := &backingVolume{LLVName: "llv-1"}
		var b *backingVolume
		Expect(a.Equal(b)).To(BeFalse())
	})

	It("returns true when all fields are equal", func() {
		a := &backingVolume{
			LLVName:            "llv-1",
			LVMVolumeGroupName: "lvg-1",
			ThinPoolName:       "tp-1",
			Size:               resource.MustParse("10Gi"),
		}
		b := &backingVolume{
			LLVName:            "llv-1",
			LVMVolumeGroupName: "lvg-1",
			ThinPoolName:       "tp-1",
			Size:               resource.MustParse("10Gi"),
		}
		Expect(a.Equal(b)).To(BeTrue())
	})

	It("returns false when LLVName differs", func() {
		a := &backingVolume{LLVName: "llv-1", LVMVolumeGroupName: "lvg-1", Size: resource.MustParse("10Gi")}
		b := &backingVolume{LLVName: "llv-2", LVMVolumeGroupName: "lvg-1", Size: resource.MustParse("10Gi")}
		Expect(a.Equal(b)).To(BeFalse())
	})

	It("returns false when LVMVolumeGroupName differs", func() {
		a := &backingVolume{LLVName: "llv-1", LVMVolumeGroupName: "lvg-1", Size: resource.MustParse("10Gi")}
		b := &backingVolume{LLVName: "llv-1", LVMVolumeGroupName: "lvg-2", Size: resource.MustParse("10Gi")}
		Expect(a.Equal(b)).To(BeFalse())
	})

	It("returns false when ThinPoolName differs", func() {
		a := &backingVolume{LLVName: "llv-1", ThinPoolName: "tp-1", Size: resource.MustParse("10Gi")}
		b := &backingVolume{LLVName: "llv-1", ThinPoolName: "tp-2", Size: resource.MustParse("10Gi")}
		Expect(a.Equal(b)).To(BeFalse())
	})

	It("returns false when Size differs", func() {
		a := &backingVolume{LLVName: "llv-1", Size: resource.MustParse("10Gi")}
		b := &backingVolume{LLVName: "llv-1", Size: resource.MustParse("20Gi")}
		Expect(a.Equal(b)).To(BeFalse())
	})

	It("returns true for thick volumes with empty ThinPoolName", func() {
		a := &backingVolume{LLVName: "llv-1", LVMVolumeGroupName: "lvg-1", ThinPoolName: "", Size: resource.MustParse("10Gi")}
		b := &backingVolume{LLVName: "llv-1", LVMVolumeGroupName: "lvg-1", ThinPoolName: "", Size: resource.MustParse("10Gi")}
		Expect(a.Equal(b)).To(BeTrue())
	})
})

var _ = Describe("backingVolume.LLVNameOrEmpty", func() {
	It("returns empty string for nil receiver", func() {
		var bv *backingVolume
		Expect(bv.LLVNameOrEmpty()).To(Equal(""))
	})

	It("returns LLV name for non-nil receiver", func() {
		bv := &backingVolume{LLVName: "llv-1"}
		Expect(bv.LLVNameOrEmpty()).To(Equal("llv-1"))
	})
})

var _ = Describe("computeIntendedBackingVolume", func() {
	var rv *v1alpha1.ReplicatedVolume

	BeforeEach(func() {
		rv = &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
			Spec:       v1alpha1.ReplicatedVolumeSpec{},
			Status: v1alpha1.ReplicatedVolumeStatus{
				DatameshRevision: 1,
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Size: resource.MustParse("10Gi"),
				},
			},
		}
	})

	It("panics for nil RVR", func() {
		Expect(func() {
			computeIntendedBackingVolume(nil, rv, nil, nil)
		}).To(PanicWith("computeIntendedBackingVolume: rvr is nil"))
	})

	It("panics when RV is nil", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}
		Expect(func() {
			computeIntendedBackingVolume(rvr, nil, nil, nil)
		}).To(PanicWith("computeIntendedBackingVolume: rv is nil"))
	})

	It("returns nil for datamesh member that is Access (diskless)", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}
		rv.Status.Datamesh.Members = []v1alpha1.DatameshMember{
			{Name: "rvr-1", Type: v1alpha1.DatameshMemberTypeAccess},
		}

		intended, reason, _ := computeIntendedBackingVolume(rvr, rv, nil, nil)
		Expect(intended).To(BeNil())
		Expect(reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonNotApplicable))
	})

	It("returns backing volume for liminal Diskful datamesh member", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}
		rv.Status.Datamesh.Members = []v1alpha1.DatameshMember{
			{
				Name:               "rvr-1",
				Type:               v1alpha1.DatameshMemberTypeLiminalDiskful,
				LVMVolumeGroupName: "lvg-1",
			},
		}

		bv, reason, _ := computeIntendedBackingVolume(rvr, rv, nil, nil)

		Expect(bv).NotTo(BeNil())
		Expect(reason).To(BeEmpty())
		Expect(bv.LVMVolumeGroupName).To(Equal("lvg-1"))
	})

	It("returns nil for datamesh member with empty LVMVolumeGroupName", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}
		rv.Status.Datamesh.Members = []v1alpha1.DatameshMember{
			{Name: "rvr-1", Type: v1alpha1.DatameshMemberTypeDiskful, LVMVolumeGroupName: ""},
		}

		intended, reason, _ := computeIntendedBackingVolume(rvr, rv, nil, nil)
		Expect(intended).To(BeNil())
		Expect(reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonPendingScheduling))
	})

	It("returns backing volume for datamesh member (diskful)", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}
		rv.Status.Datamesh.Members = []v1alpha1.DatameshMember{
			{
				Name:               "rvr-1",
				Type:               v1alpha1.DatameshMemberTypeDiskful,
				LVMVolumeGroupName: "lvg-1",
			},
		}

		bv, reason, _ := computeIntendedBackingVolume(rvr, rv, nil, nil)

		Expect(bv).NotTo(BeNil())
		Expect(reason).To(BeEmpty())
		Expect(bv.LVMVolumeGroupName).To(Equal("lvg-1"))
		Expect(bv.ThinPoolName).To(BeEmpty())
		// Size comes from datamesh, adjusted by drbd_size.LowerVolumeSize
		Expect(bv.Size.Cmp(resource.Quantity{}) > 0).To(BeTrue())
	})

	It("returns backing volume for datamesh member with thin pool", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}
		rv.Status.Datamesh.Members = []v1alpha1.DatameshMember{
			{
				Name:                       "rvr-1",
				Type:                       v1alpha1.DatameshMemberTypeDiskful,
				LVMVolumeGroupName:         "lvg-1",
				LVMVolumeGroupThinPoolName: "thinpool-1",
			},
		}

		bv, _, _ := computeIntendedBackingVolume(rvr, rv, nil, nil)

		Expect(bv).NotTo(BeNil())
		Expect(bv.LVMVolumeGroupName).To(Equal("lvg-1"))
		Expect(bv.ThinPoolName).To(Equal("thinpool-1"))
	})

	It("uses rv.Spec.Size when larger than datamesh.Size", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}
		rv.Status.Datamesh.Members = []v1alpha1.DatameshMember{
			{
				Name:               "rvr-1",
				Type:               v1alpha1.DatameshMemberTypeDiskful,
				LVMVolumeGroupName: "lvg-1",
			},
		}
		// datamesh.Size = 10Gi (from BeforeEach), rv.Spec.Size = 20Gi (larger).
		rv.Spec.Size = resource.MustParse("20Gi")

		bvFromSpec, _, _ := computeIntendedBackingVolume(rvr, rv, nil, nil)

		// Reset rv.Spec.Size to zero and get the datamesh-only size for comparison.
		rv.Spec.Size = resource.Quantity{}
		bvFromDatamesh, _, _ := computeIntendedBackingVolume(rvr, rv, nil, nil)

		Expect(bvFromSpec).NotTo(BeNil())
		Expect(bvFromDatamesh).NotTo(BeNil())
		// Size from rv.Spec.Size (20Gi) must be larger than from datamesh.Size (10Gi).
		Expect(bvFromSpec.Size.Cmp(bvFromDatamesh.Size) > 0).To(BeTrue())
	})

	It("returns nil for non-member with Access type (diskless)", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				Type: v1alpha1.ReplicaTypeAccess,
			},
		}

		intended, reason, _ := computeIntendedBackingVolume(rvr, rv, nil, nil)
		Expect(intended).To(BeNil())
		Expect(reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonNotApplicable))
	})

	It("returns nil for non-member with incomplete config (no nodeName)", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				Type:               v1alpha1.ReplicaTypeDiskful,
				NodeName:           "",
				LVMVolumeGroupName: "lvg-1",
			},
		}

		intended, reason, _ := computeIntendedBackingVolume(rvr, rv, nil, nil)
		Expect(intended).To(BeNil())
		Expect(reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonPendingScheduling))
	})

	It("returns nil for non-member with incomplete config (no lvgName)", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				Type:               v1alpha1.ReplicaTypeDiskful,
				NodeName:           "node-1",
				LVMVolumeGroupName: "",
			},
		}

		intended, reason, _ := computeIntendedBackingVolume(rvr, rv, nil, nil)
		Expect(intended).To(BeNil())
		Expect(reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonPendingScheduling))
	})

	It("returns WaitingForReplicatedVolume for non-member without rspView", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				Type:               v1alpha1.ReplicaTypeDiskful,
				NodeName:           "node-1",
				LVMVolumeGroupName: "lvg-1",
			},
		}

		intended, reason, _ := computeIntendedBackingVolume(rvr, rv, nil, nil)
		Expect(intended).To(BeNil())
		Expect(reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonWaitingForReplicatedVolume))
	})

	It("returns backing volume for non-member (diskful, complete config)", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				Type:               v1alpha1.ReplicaTypeDiskful,
				NodeName:           "node-1",
				LVMVolumeGroupName: "lvg-1",
			},
		}
		rspView := &rspEligibilityView{
			Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
			EligibleNode: &v1alpha1.ReplicatedStoragePoolEligibleNode{
				NodeName: "node-1",
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
					{Name: "lvg-1"},
				},
			},
		}

		bv, _, _ := computeIntendedBackingVolume(rvr, rv, nil, rspView)

		Expect(bv).NotTo(BeNil())
		Expect(bv.LVMVolumeGroupName).To(Equal("lvg-1"))
		Expect(bv.ThinPoolName).To(BeEmpty())
	})

	It("reuses actual LLVName when LVG and thin pool match", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}
		rv.Status.Datamesh.Members = []v1alpha1.DatameshMember{
			{
				Name:               "rvr-1",
				Type:               v1alpha1.DatameshMemberTypeDiskful,
				LVMVolumeGroupName: "lvg-1",
			},
		}
		actual := &backingVolume{
			LLVName:            "llv-old-name",
			LVMVolumeGroupName: "lvg-1",
			ThinPoolName:       "",
		}

		bv, _, _ := computeIntendedBackingVolume(rvr, rv, actual, nil)

		Expect(bv).NotTo(BeNil())
		Expect(bv.LLVName).To(Equal("llv-old-name"))
	})

	It("generates new LLVName when LVG differs from actual", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}
		rv.Status.Datamesh.Members = []v1alpha1.DatameshMember{
			{
				Name:               "rvr-1",
				Type:               v1alpha1.DatameshMemberTypeDiskful,
				LVMVolumeGroupName: "lvg-new",
			},
		}
		actual := &backingVolume{
			LLVName:            "llv-old-name",
			LVMVolumeGroupName: "lvg-old",
		}

		bv, _, _ := computeIntendedBackingVolume(rvr, rv, actual, nil)

		Expect(bv).NotTo(BeNil())
		Expect(bv.LLVName).NotTo(Equal("llv-old-name"))
		Expect(bv.LLVName).To(HavePrefix("rvr-1-"))
	})

	// RSP eligibility validation tests (non-member only)
	It("returns PendingScheduling for non-member when node is not eligible in RSP", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				Type:               v1alpha1.ReplicaTypeDiskful,
				NodeName:           "node-1",
				LVMVolumeGroupName: "lvg-1",
			},
		}
		rspView := &rspEligibilityView{
			Type:         v1alpha1.ReplicatedStoragePoolTypeLVM,
			EligibleNode: nil, // node not eligible
		}

		intended, reason, message := computeIntendedBackingVolume(rvr, rv, nil, rspView)
		Expect(intended).To(BeNil())
		Expect(reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonPendingScheduling))
		Expect(message).To(ContainSubstring("not eligible in RSP"))
	})

	It("returns PendingScheduling for non-member when LVG is not in eligible node list", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				Type:               v1alpha1.ReplicaTypeDiskful,
				NodeName:           "node-1",
				LVMVolumeGroupName: "lvg-1",
			},
		}
		rspView := &rspEligibilityView{
			Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
			EligibleNode: &v1alpha1.ReplicatedStoragePoolEligibleNode{
				NodeName: "node-1",
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
					{Name: "lvg-other"}, // different LVG
				},
			},
		}

		intended, reason, message := computeIntendedBackingVolume(rvr, rv, nil, rspView)
		Expect(intended).To(BeNil())
		Expect(reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonPendingScheduling))
		Expect(message).To(ContainSubstring("LVG"))
		Expect(message).To(ContainSubstring("not eligible"))
	})

	It("returns PendingScheduling for non-member with LVMThin when ThinPool is not in eligible node list", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				Type:                       v1alpha1.ReplicaTypeDiskful,
				NodeName:                   "node-1",
				LVMVolumeGroupName:         "lvg-1",
				LVMVolumeGroupThinPoolName: "thinpool-1",
			},
		}
		rspView := &rspEligibilityView{
			Type: v1alpha1.ReplicatedStoragePoolTypeLVMThin,
			EligibleNode: &v1alpha1.ReplicatedStoragePoolEligibleNode{
				NodeName: "node-1",
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
					{Name: "lvg-1", ThinPoolName: "thinpool-other"}, // different ThinPool
				},
			},
		}

		intended, reason, message := computeIntendedBackingVolume(rvr, rv, nil, rspView)
		Expect(intended).To(BeNil())
		Expect(reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonPendingScheduling))
		Expect(message).To(ContainSubstring("ThinPool"))
		Expect(message).To(ContainSubstring("not eligible"))
	})

	// ThinPool validation tests (non-member only)
	It("returns nil for non-member with ThinPool specified but RSP type is LVM", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				Type:                       v1alpha1.ReplicaTypeDiskful,
				NodeName:                   "node-1",
				LVMVolumeGroupName:         "lvg-1",
				LVMVolumeGroupThinPoolName: "thinpool-1",
			},
		}
		rspView := &rspEligibilityView{
			Type: v1alpha1.ReplicatedStoragePoolTypeLVM, // LVM (thick)
			EligibleNode: &v1alpha1.ReplicatedStoragePoolEligibleNode{
				NodeName: "node-1",
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
					{Name: "lvg-1"},
				},
			},
		}

		intended, reason, message := computeIntendedBackingVolume(rvr, rv, nil, rspView)
		Expect(intended).To(BeNil())
		Expect(reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonPendingScheduling))
		Expect(message).To(ContainSubstring("ThinPool"))
		Expect(message).To(ContainSubstring("LVM (thick)"))
	})

	It("returns nil for non-member with ThinPool not specified but RSP type is LVMThin", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				Type:               v1alpha1.ReplicaTypeDiskful,
				NodeName:           "node-1",
				LVMVolumeGroupName: "lvg-1",
			},
		}
		rspView := &rspEligibilityView{
			Type: v1alpha1.ReplicatedStoragePoolTypeLVMThin, // LVMThin but no ThinPool in RVR
			EligibleNode: &v1alpha1.ReplicatedStoragePoolEligibleNode{
				NodeName: "node-1",
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
					{Name: "lvg-1", ThinPoolName: "thinpool-1"},
				},
			},
		}

		intended, reason, message := computeIntendedBackingVolume(rvr, rv, nil, rspView)
		Expect(intended).To(BeNil())
		Expect(reason).To(Equal(v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyReasonPendingScheduling))
		Expect(message).To(ContainSubstring("ThinPool not specified"))
		Expect(message).To(ContainSubstring("LVMThin"))
	})

	It("returns backing volume for non-member with valid LVM config (no ThinPool)", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				Type:               v1alpha1.ReplicaTypeDiskful,
				NodeName:           "node-1",
				LVMVolumeGroupName: "lvg-1",
			},
		}
		rspView := &rspEligibilityView{
			Type: v1alpha1.ReplicatedStoragePoolTypeLVM,
			EligibleNode: &v1alpha1.ReplicatedStoragePoolEligibleNode{
				NodeName: "node-1",
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
					{Name: "lvg-1"},
				},
			},
		}

		bv, _, _ := computeIntendedBackingVolume(rvr, rv, nil, rspView)
		Expect(bv).NotTo(BeNil())
		Expect(bv.ThinPoolName).To(BeEmpty())
	})

	It("returns backing volume for non-member with valid LVMThin config (with ThinPool)", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				Type:                       v1alpha1.ReplicaTypeDiskful,
				NodeName:                   "node-1",
				LVMVolumeGroupName:         "lvg-1",
				LVMVolumeGroupThinPoolName: "thinpool-1",
			},
		}
		rspView := &rspEligibilityView{
			Type: v1alpha1.ReplicatedStoragePoolTypeLVMThin,
			EligibleNode: &v1alpha1.ReplicatedStoragePoolEligibleNode{
				NodeName: "node-1",
				LVMVolumeGroups: []v1alpha1.ReplicatedStoragePoolEligibleNodeLVMVolumeGroup{
					{Name: "lvg-1", ThinPoolName: "thinpool-1"},
				},
			},
		}

		bv, _, _ := computeIntendedBackingVolume(rvr, rv, nil, rspView)
		Expect(bv).NotTo(BeNil())
		Expect(bv.ThinPoolName).To(Equal("thinpool-1"))
	})
})

var _ = Describe("computeLLVName", func() {
	It("produces deterministic output", func() {
		name1 := rvrllvname.ComputeLLVName("rvr-1", "lvg-1", "thinpool-1")
		name2 := rvrllvname.ComputeLLVName("rvr-1", "lvg-1", "thinpool-1")

		Expect(name1).To(Equal(name2))
	})

	It("produces different output for different inputs", func() {
		name1 := rvrllvname.ComputeLLVName("rvr-1", "lvg-1", "thinpool-1")
		name2 := rvrllvname.ComputeLLVName("rvr-1", "lvg-2", "thinpool-1")
		name3 := rvrllvname.ComputeLLVName("rvr-1", "lvg-1", "thinpool-2")
		name4 := rvrllvname.ComputeLLVName("rvr-2", "lvg-1", "thinpool-1")

		Expect(name1).NotTo(Equal(name2))
		Expect(name1).NotTo(Equal(name3))
		Expect(name1).NotTo(Equal(name4))
	})

	It("uses rvrName as prefix", func() {
		name := rvrllvname.ComputeLLVName("my-rvr", "lvg-1", "")

		Expect(name).To(HavePrefix("my-rvr-"))
	})

	It("handles empty thin pool name", func() {
		name := rvrllvname.ComputeLLVName("rvr-1", "lvg-1", "")

		Expect(name).To(HavePrefix("rvr-1-"))
		Expect(len(name)).To(BeNumerically(">", len("rvr-1-")))
	})
})

var _ = Describe("findLLVByName", func() {
	It("returns nil for empty slice", func() {
		Expect(findLLVByName(nil, "llv-1")).To(BeNil())
	})

	It("returns nil when LLV not found", func() {
		llvs := []snc.LVMLogicalVolume{
			{ObjectMeta: metav1.ObjectMeta{Name: "llv-other"}},
		}

		Expect(findLLVByName(llvs, "llv-1")).To(BeNil())
	})

	It("returns pointer to found LLV", func() {
		llvs := []snc.LVMLogicalVolume{
			{ObjectMeta: metav1.ObjectMeta{Name: "llv-1"}},
			{ObjectMeta: metav1.ObjectMeta{Name: "llv-2"}},
		}

		result := findLLVByName(llvs, "llv-1")

		Expect(result).NotTo(BeNil())
		Expect(result.Name).To(Equal("llv-1"))
	})

	It("returns pointer to slice element (same memory)", func() {
		llvs := []snc.LVMLogicalVolume{
			{ObjectMeta: metav1.ObjectMeta{Name: "llv-1"}},
		}

		result := findLLVByName(llvs, "llv-1")

		Expect(result).To(BeIdenticalTo(&llvs[0]))
	})
})

var _ = Describe("computeAPIValidationErrorCauses", func() {
	It("returns error message for non-StatusError", func() {
		err := newTestError("some error")
		result := computeAPIValidationErrorCauses(err)

		Expect(result).To(Equal("some error"))
	})

	It("returns error message when StatusError has nil Details", func() {
		err := &apierrors.StatusError{
			ErrStatus: metav1.Status{
				Message: "validation failed",
				Details: nil,
			},
		}

		result := computeAPIValidationErrorCauses(err)

		Expect(result).To(Equal("validation failed"))
	})

	It("returns error message when StatusError has empty Causes", func() {
		err := &apierrors.StatusError{
			ErrStatus: metav1.Status{
				Message: "validation failed",
				Details: &metav1.StatusDetails{
					Causes: []metav1.StatusCause{},
				},
			},
		}

		result := computeAPIValidationErrorCauses(err)

		Expect(result).To(Equal("validation failed"))
	})

	It("returns formatted cause for single cause with Field and Message", func() {
		err := &apierrors.StatusError{
			ErrStatus: metav1.Status{
				Message: "validation failed",
				Details: &metav1.StatusDetails{
					Causes: []metav1.StatusCause{
						{Field: "spec.size", Message: "Invalid value: must be positive"},
					},
				},
			},
		}

		result := computeAPIValidationErrorCauses(err)

		Expect(result).To(Equal("spec.size: Invalid value: must be positive"))
	})

	It("returns formatted causes for multiple causes", func() {
		err := &apierrors.StatusError{
			ErrStatus: metav1.Status{
				Message: "validation failed",
				Details: &metav1.StatusDetails{
					Causes: []metav1.StatusCause{
						{Field: "spec.size", Message: "Invalid value"},
						{Field: "spec.name", Message: "Required value"},
					},
				},
			},
		}

		result := computeAPIValidationErrorCauses(err)

		Expect(result).To(Equal("spec.size: Invalid value; spec.name: Required value"))
	})

	It("returns only Message when cause has no Field", func() {
		err := &apierrors.StatusError{
			ErrStatus: metav1.Status{
				Message: "validation failed",
				Details: &metav1.StatusDetails{
					Causes: []metav1.StatusCause{
						{Field: "", Message: "Some validation error"},
					},
				},
			},
		}

		result := computeAPIValidationErrorCauses(err)

		Expect(result).To(Equal("Some validation error"))
	})

	It("returns only Field when cause has no Message", func() {
		err := &apierrors.StatusError{
			ErrStatus: metav1.Status{
				Message: "validation failed",
				Details: &metav1.StatusDetails{
					Causes: []metav1.StatusCause{
						{Field: "spec.size", Message: ""},
					},
				},
			},
		}

		result := computeAPIValidationErrorCauses(err)

		Expect(result).To(Equal("spec.size"))
	})

	It("returns error message when cause has neither Field nor Message", func() {
		err := &apierrors.StatusError{
			ErrStatus: metav1.Status{
				Message: "validation failed",
				Details: &metav1.StatusDetails{
					Causes: []metav1.StatusCause{
						{Field: "", Message: ""},
					},
				},
			},
		}

		result := computeAPIValidationErrorCauses(err)

		Expect(result).To(Equal("validation failed"))
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// IsInSync functions tests
//

var _ = Describe("isLLVMetadataInSync", func() {
	var (
		rvr    *v1alpha1.ReplicatedVolumeReplica
		scheme *runtime.Scheme
	)

	BeforeEach(func() {
		rvr = &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1", UID: "uid-1"},
		}
		scheme = runtime.NewScheme()
		_ = v1alpha1.AddToScheme(scheme)
	})

	It("returns false when finalizer is missing", func() {
		llv := &snc.LVMLogicalVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "llv-1"},
		}

		Expect(isLLVMetadataInSync(rvr, nil, llv)).To(BeFalse())
	})

	It("returns false when ownerRef is missing", func() {
		llv := &snc.LVMLogicalVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "llv-1",
				Finalizers: []string{v1alpha1.RVRControllerFinalizer},
			},
		}

		Expect(isLLVMetadataInSync(rvr, nil, llv)).To(BeFalse())
	})

	It("returns false when replicated-volume label is missing", func() {
		rvr.Spec.ReplicatedVolumeName = "rv-1"
		llv := &snc.LVMLogicalVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "llv-1",
				Finalizers: []string{v1alpha1.RVRControllerFinalizer},
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion:         v1alpha1.SchemeGroupVersion.String(),
						Kind:               "ReplicatedVolumeReplica",
						Name:               "rvr-1",
						UID:                "uid-1",
						Controller:         boolPtr(true),
						BlockOwnerDeletion: boolPtr(true),
					},
				},
			},
		}

		Expect(isLLVMetadataInSync(rvr, nil, llv)).To(BeFalse())
	})

	It("returns true when all metadata is in sync", func() {
		rvr.Spec.ReplicatedVolumeName = "rv-1"
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
			Spec: v1alpha1.ReplicatedVolumeSpec{
				ReplicatedStorageClassName: "rsc-1",
			},
		}
		llv := &snc.LVMLogicalVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "llv-1",
				Finalizers: []string{v1alpha1.RVRControllerFinalizer},
				Labels: map[string]string{
					v1alpha1.ReplicatedVolumeLabelKey:       "rv-1",
					v1alpha1.ReplicatedStorageClassLabelKey: "rsc-1",
				},
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion:         v1alpha1.SchemeGroupVersion.String(),
						Kind:               "ReplicatedVolumeReplica",
						Name:               "rvr-1",
						UID:                "uid-1",
						Controller:         boolPtr(true),
						BlockOwnerDeletion: boolPtr(true),
					},
				},
			},
		}

		Expect(isLLVMetadataInSync(rvr, rv, llv)).To(BeTrue())
	})
})

var _ = Describe("isLLVReady", func() {
	It("returns false for nil LLV", func() {
		Expect(isLLVReady(nil)).To(BeFalse())
	})

	It("returns false when Status is nil", func() {
		llv := &snc.LVMLogicalVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "llv-1"},
			Status:     nil,
		}

		Expect(isLLVReady(llv)).To(BeFalse())
	})

	It("returns false when Phase is not Created", func() {
		llv := &snc.LVMLogicalVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "llv-1"},
			Status:     &snc.LVMLogicalVolumeStatus{Phase: "Pending"},
		}

		Expect(isLLVReady(llv)).To(BeFalse())
	})

	It("returns true when Phase is Created", func() {
		llv := &snc.LVMLogicalVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "llv-1"},
			Status:     &snc.LVMLogicalVolumeStatus{Phase: "Created"},
		}

		Expect(isLLVReady(llv)).To(BeTrue())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Apply functions tests
//

var _ = Describe("applyLLVMetadata", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		_ = v1alpha1.AddToScheme(scheme)
		_ = snc.AddToScheme(scheme)
	})

	It("adds finalizer", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1", UID: "uid-1"},
		}
		llv := &snc.LVMLogicalVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "llv-1"},
		}

		changed, err := applyLLVMetadata(scheme, rvr, nil, llv)

		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeTrue())
		Expect(obju.HasFinalizer(llv, v1alpha1.RVRControllerFinalizer)).To(BeTrue())
	})

	It("sets replicated-volume label", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1", UID: "uid-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1",
			},
		}
		llv := &snc.LVMLogicalVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "llv-1"},
		}

		changed, err := applyLLVMetadata(scheme, rvr, nil, llv)

		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeTrue())
		Expect(obju.HasLabelValue(llv, v1alpha1.ReplicatedVolumeLabelKey, "rv-1")).To(BeTrue())
	})

	It("sets replicated-storage-class label", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1", UID: "uid-1"},
		}
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
			Spec: v1alpha1.ReplicatedVolumeSpec{
				ReplicatedStorageClassName: "rsc-1",
			},
		}
		llv := &snc.LVMLogicalVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "llv-1"},
		}

		changed, err := applyLLVMetadata(scheme, rvr, rv, llv)

		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeTrue())
		Expect(obju.HasLabelValue(llv, v1alpha1.ReplicatedStorageClassLabelKey, "rsc-1")).To(BeTrue())
	})

	It("sets ownerRef", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1", UID: "uid-1"},
		}
		llv := &snc.LVMLogicalVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "llv-1"},
		}

		changed, err := applyLLVMetadata(scheme, rvr, nil, llv)

		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeTrue())
		Expect(obju.HasControllerRef(llv, rvr)).To(BeTrue())
	})

	It("returns false when nothing changes (idempotent)", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1", UID: "uid-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1",
			},
		}
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
			Spec: v1alpha1.ReplicatedVolumeSpec{
				ReplicatedStorageClassName: "rsc-1",
			},
		}
		llv := &snc.LVMLogicalVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "llv-1",
				Finalizers: []string{v1alpha1.RVRControllerFinalizer},
				Labels: map[string]string{
					v1alpha1.ReplicatedVolumeLabelKey:       "rv-1",
					v1alpha1.ReplicatedStorageClassLabelKey: "rsc-1",
				},
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion:         v1alpha1.SchemeGroupVersion.String(),
						Kind:               "ReplicatedVolumeReplica",
						Name:               "rvr-1",
						UID:                "uid-1",
						Controller:         boolPtr(true),
						BlockOwnerDeletion: boolPtr(true),
					},
				},
			},
		}

		changed, err := applyLLVMetadata(scheme, rvr, rv, llv)

		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeFalse())
	})
})

var _ = Describe("applyBackingVolumeReadyCondFalse", func() {
	It("sets condition to False", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}

		changed := applyBackingVolumeReadyCondFalse(rvr, "TestReason", "Test message")

		Expect(changed).To(BeTrue())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal("TestReason"))
		Expect(cond.Message).To(Equal("Test message"))
	})

	It("returns false when condition already set (idempotent)", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}
		obju.SetStatusCondition(rvr, metav1.Condition{
			Type:    v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyType,
			Status:  metav1.ConditionFalse,
			Reason:  "TestReason",
			Message: "Test message",
		})

		changed := applyBackingVolumeReadyCondFalse(rvr, "TestReason", "Test message")

		Expect(changed).To(BeFalse())
	})
})

var _ = Describe("applyBackingVolumeReadyCondTrue", func() {
	It("sets condition to True", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}

		changed := applyBackingVolumeReadyCondTrue(rvr, "Ready", "Backing volume is ready")

		Expect(changed).To(BeTrue())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal("Ready"))
		Expect(cond.Message).To(Equal("Backing volume is ready"))
	})
})

var _ = Describe("applyBackingVolumeReadyCondUnknown", func() {
	It("sets condition to Unknown", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}

		changed := applyBackingVolumeReadyCondUnknown(rvr, "TestReason", "Test message")

		Expect(changed).To(BeTrue())
		cond := obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyType)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
		Expect(cond.Reason).To(Equal("TestReason"))
		Expect(cond.Message).To(Equal("Test message"))
	})

	It("returns false when condition already matches (idempotent)", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}
		applyBackingVolumeReadyCondUnknown(rvr, "TestReason", "Test message")

		changed := applyBackingVolumeReadyCondUnknown(rvr, "TestReason", "Test message")

		Expect(changed).To(BeFalse())
	})
})

var _ = Describe("newLLV", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		_ = v1alpha1.AddToScheme(scheme)
		_ = snc.AddToScheme(scheme)
	})

	It("creates LLV with correct name", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1", UID: "uid-1"},
		}
		bv := &backingVolume{
			LLVName:            "llv-1",
			LVMVolumeGroupName: "lvg-1",
			Size:               resource.MustParse("10Gi"),
		}

		llv, err := newLLV(scheme, rvr, nil, bv)

		Expect(err).NotTo(HaveOccurred())
		Expect(llv.Name).To(Equal("llv-1"))
	})

	It("creates LLV with finalizer", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1", UID: "uid-1"},
		}
		bv := &backingVolume{
			LLVName:            "llv-1",
			LVMVolumeGroupName: "lvg-1",
			Size:               resource.MustParse("10Gi"),
		}

		llv, err := newLLV(scheme, rvr, nil, bv)

		Expect(err).NotTo(HaveOccurred())
		Expect(obju.HasFinalizer(llv, v1alpha1.RVRControllerFinalizer)).To(BeTrue())
	})

	It("creates LLV with ownerRef", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1", UID: "uid-1"},
		}
		bv := &backingVolume{
			LLVName:            "llv-1",
			LVMVolumeGroupName: "lvg-1",
			Size:               resource.MustParse("10Gi"),
		}

		llv, err := newLLV(scheme, rvr, nil, bv)

		Expect(err).NotTo(HaveOccurred())
		Expect(obju.HasControllerRef(llv, rvr)).To(BeTrue())
	})

	It("creates thick volume spec", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1", UID: "uid-1"},
		}
		bv := &backingVolume{
			LLVName:            "llv-1",
			LVMVolumeGroupName: "lvg-1",
			ThinPoolName:       "", // thick
			Size:               resource.MustParse("10Gi"),
		}

		llv, err := newLLV(scheme, rvr, nil, bv)

		Expect(err).NotTo(HaveOccurred())
		Expect(llv.Spec.Type).To(Equal("Thick"))
		Expect(llv.Spec.Thin).To(BeNil())
	})

	It("creates thin volume spec", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1", UID: "uid-1"},
		}
		bv := &backingVolume{
			LLVName:            "llv-1",
			LVMVolumeGroupName: "lvg-1",
			ThinPoolName:       "thinpool-1",
			Size:               resource.MustParse("10Gi"),
		}

		llv, err := newLLV(scheme, rvr, nil, bv)

		Expect(err).NotTo(HaveOccurred())
		Expect(llv.Spec.Type).To(Equal("Thin"))
		Expect(llv.Spec.Thin).NotTo(BeNil())
		Expect(llv.Spec.Thin.PoolName).To(Equal("thinpool-1"))
	})

	It("sets correct size", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1", UID: "uid-1"},
		}
		bv := &backingVolume{
			LLVName:            "llv-1",
			LVMVolumeGroupName: "lvg-1",
			Size:               resource.MustParse("10Gi"),
		}

		llv, err := newLLV(scheme, rvr, nil, bv)

		Expect(err).NotTo(HaveOccurred())
		Expect(llv.Spec.Size).To(Equal("10Gi"))
	})

	It("sets replicated-volume label", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1", UID: "uid-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1",
			},
		}
		bv := &backingVolume{
			LLVName:            "llv-1",
			LVMVolumeGroupName: "lvg-1",
			Size:               resource.MustParse("10Gi"),
		}

		llv, err := newLLV(scheme, rvr, nil, bv)

		Expect(err).NotTo(HaveOccurred())
		Expect(obju.HasLabelValue(llv, v1alpha1.ReplicatedVolumeLabelKey, "rv-1")).To(BeTrue())
	})

	It("sets replicated-storage-class label from RV", func() {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1", UID: "uid-1"},
		}
		rv := &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
			Spec: v1alpha1.ReplicatedVolumeSpec{
				ReplicatedStorageClassName: "rsc-1",
			},
		}
		bv := &backingVolume{
			LLVName:            "llv-1",
			LVMVolumeGroupName: "lvg-1",
			Size:               resource.MustParse("10Gi"),
		}

		llv, err := newLLV(scheme, rvr, rv, bv)

		Expect(err).NotTo(HaveOccurred())
		Expect(obju.HasLabelValue(llv, v1alpha1.ReplicatedStorageClassLabelKey, "rsc-1")).To(BeTrue())
	})
})

var _ = Describe("reconcileLLVsDeletion", func() {
	var (
		scheme *runtime.Scheme
		ctx    context.Context
	)

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(snc.AddToScheme(scheme)).To(Succeed())
		ctx = context.Background()
	})

	It("preserves LLVs in keep list when deleting obsolete LLVs", func() {
		// Three LLVs owned by RVR
		llv1 := snc.LVMLogicalVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "llv-1",
				Finalizers: []string{v1alpha1.RVRControllerFinalizer},
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion:         v1alpha1.SchemeGroupVersion.String(),
						Kind:               "ReplicatedVolumeReplica",
						Name:               "rvr-1",
						UID:                "uid-1",
						Controller:         boolPtr(true),
						BlockOwnerDeletion: boolPtr(true),
					},
				},
			},
			Spec: snc.LVMLogicalVolumeSpec{LVMVolumeGroupName: "lvg-1"},
		}
		llv2 := snc.LVMLogicalVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "llv-2",
				Finalizers: []string{v1alpha1.RVRControllerFinalizer},
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion:         v1alpha1.SchemeGroupVersion.String(),
						Kind:               "ReplicatedVolumeReplica",
						Name:               "rvr-1",
						UID:                "uid-1",
						Controller:         boolPtr(true),
						BlockOwnerDeletion: boolPtr(true),
					},
				},
			},
			Spec: snc.LVMLogicalVolumeSpec{LVMVolumeGroupName: "lvg-2"},
		}
		llv3 := snc.LVMLogicalVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "llv-3",
				Finalizers: []string{v1alpha1.RVRControllerFinalizer},
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion:         v1alpha1.SchemeGroupVersion.String(),
						Kind:               "ReplicatedVolumeReplica",
						Name:               "rvr-1",
						UID:                "uid-1",
						Controller:         boolPtr(true),
						BlockOwnerDeletion: boolPtr(true),
					},
				},
			},
			Spec: snc.LVMLogicalVolumeSpec{LVMVolumeGroupName: "lvg-3"},
		}

		cl := testhelpers.WithLLVByRVROwnerIndex(
			fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(&llv1, &llv2, &llv3),
		).Build()
		rec := NewReconciler(cl, scheme, logr.Discard(), "")

		// Keep only llv-2, delete llv-1 and llv-3
		llvs := []snc.LVMLogicalVolume{llv1, llv2, llv3}
		keep := []string{"llv-2"}

		_, outcome := rec.reconcileLLVsDeletion(ctx, &llvs, keep)
		Expect(outcome.Error()).NotTo(HaveOccurred())

		// Verify only llv-2 remains
		var llvList snc.LVMLogicalVolumeList
		Expect(cl.List(ctx, &llvList)).To(Succeed())
		Expect(llvList.Items).To(HaveLen(1))
		Expect(llvList.Items[0].Name).To(Equal("llv-2"))
	})

	It("skips delete call for LLV with DeletionTimestamp (already deleting)", func() {
		now := metav1.Now()
		// LLV already being deleted (has DeletionTimestamp) with another finalizer to keep it alive
		llv := snc.LVMLogicalVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "llv-1",
				DeletionTimestamp: &now,
				Finalizers:        []string{v1alpha1.RVRControllerFinalizer, "other-finalizer"},
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion:         v1alpha1.SchemeGroupVersion.String(),
						Kind:               "ReplicatedVolumeReplica",
						Name:               "rvr-1",
						UID:                "uid-1",
						Controller:         boolPtr(true),
						BlockOwnerDeletion: boolPtr(true),
					},
				},
			},
			Spec: snc.LVMLogicalVolumeSpec{LVMVolumeGroupName: "lvg-1"},
		}

		deleteCalled := false
		cl := testhelpers.WithLLVByRVROwnerIndex(
			fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(&llv).
				WithInterceptorFuncs(interceptor.Funcs{
					Delete: func(ctx context.Context, client client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
						if _, ok := obj.(*snc.LVMLogicalVolume); ok {
							deleteCalled = true
						}
						return client.Delete(ctx, obj, opts...)
					},
				}),
		).Build()
		rec := NewReconciler(cl, scheme, logr.Discard(), "")

		// Empty keep list means delete all
		llvs := []snc.LVMLogicalVolume{llv}
		keep := []string{}

		_, outcome := rec.reconcileLLVsDeletion(ctx, &llvs, keep)
		Expect(outcome.Error()).NotTo(HaveOccurred())

		// Verify delete was NOT called (LLV already deleting)
		Expect(deleteCalled).To(BeFalse(), "delete should not be called for LLV with DeletionTimestamp")

		// Finalizer should be removed (LLV still exists because of other-finalizer)
		var updatedLLV snc.LVMLogicalVolume
		Expect(cl.Get(ctx, client.ObjectKey{Name: "llv-1"}, &updatedLLV)).To(Succeed())
		Expect(obju.HasFinalizer(&updatedLLV, v1alpha1.RVRControllerFinalizer)).To(BeFalse())
		Expect(obju.HasFinalizer(&updatedLLV, "other-finalizer")).To(BeTrue())
	})

	It("removes finalizer before deleting LLV", func() {
		llv := snc.LVMLogicalVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "llv-1",
				Finalizers: []string{v1alpha1.RVRControllerFinalizer},
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion:         v1alpha1.SchemeGroupVersion.String(),
						Kind:               "ReplicatedVolumeReplica",
						Name:               "rvr-1",
						UID:                "uid-1",
						Controller:         boolPtr(true),
						BlockOwnerDeletion: boolPtr(true),
					},
				},
			},
			Spec: snc.LVMLogicalVolumeSpec{LVMVolumeGroupName: "lvg-1"},
		}

		patchCalledFirst := false
		deleteCalledAfterPatch := false
		cl := testhelpers.WithLLVByRVROwnerIndex(
			fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(&llv).
				WithInterceptorFuncs(interceptor.Funcs{
					Patch: func(ctx context.Context, client client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
						if llvObj, ok := obj.(*snc.LVMLogicalVolume); ok && llvObj.Name == "llv-1" {
							patchCalledFirst = true
						}
						return client.Patch(ctx, obj, patch, opts...)
					},
					Delete: func(ctx context.Context, client client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
						if _, ok := obj.(*snc.LVMLogicalVolume); ok && patchCalledFirst {
							deleteCalledAfterPatch = true
						}
						return client.Delete(ctx, obj, opts...)
					},
				}),
		).Build()
		rec := NewReconciler(cl, scheme, logr.Discard(), "")

		llvs := []snc.LVMLogicalVolume{llv}
		keep := []string{}

		_, outcome := rec.reconcileLLVsDeletion(ctx, &llvs, keep)
		Expect(outcome.Error()).NotTo(HaveOccurred())

		// Verify finalizer was removed before delete
		Expect(deleteCalledAfterPatch).To(BeTrue(), "delete should be called after patch (finalizer removal)")
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// ensureConditionAttached tests
//

var _ = Describe("applyBackingVolumeReadyCondAbsent", func() {
	var rvr *v1alpha1.ReplicatedVolumeReplica

	BeforeEach(func() {
		rvr = &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
		}
	})

	It("removes the condition", func() {
		applyBackingVolumeReadyCondTrue(rvr, "TestReason", "Test")
		changed := applyBackingVolumeReadyCondAbsent(rvr)
		Expect(changed).To(BeTrue())
		Expect(obju.GetStatusCondition(rvr, v1alpha1.ReplicatedVolumeReplicaCondBackingVolumeReadyType)).To(BeNil())
	})

	It("is idempotent when condition absent", func() {
		changed := applyBackingVolumeReadyCondAbsent(rvr)
		Expect(changed).To(BeFalse())
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Compute peer helpers tests
//

// ──────────────────────────────────────────────────────────────────────────────
// Other helper functions tests
//
