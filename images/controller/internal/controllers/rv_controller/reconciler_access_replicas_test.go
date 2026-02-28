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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

// ──────────────────────────────────────────────────────────────────────────────
// Tests: reconcileCreateAccessReplicas
//

var _ = Describe("reconcileCreateAccessReplicas", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	})

	mkRV := func(volumeAccess v1alpha1.ReplicatedStorageClassVolumeAccess) *v1alpha1.ReplicatedVolume {
		return &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
			Status: v1alpha1.ReplicatedVolumeStatus{
				Configuration: &v1alpha1.ReplicatedVolumeConfiguration{
					VolumeAccess: volumeAccess,
				},
			},
		}
	}

	mkRVR := func(name, nodeName string, typ v1alpha1.ReplicaType) *v1alpha1.ReplicatedVolumeReplica {
		return &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1",
				NodeName:             nodeName,
				Type:                 typ,
			},
		}
	}

	mkRVA := func(name, nodeName string) *v1alpha1.ReplicatedVolumeAttachment {
		return &v1alpha1.ReplicatedVolumeAttachment{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec:       v1alpha1.ReplicatedVolumeAttachmentSpec{NodeName: nodeName, ReplicatedVolumeName: "rv-1"},
		}
	}

	mkRSP := func(nodes ...v1alpha1.ReplicatedStoragePoolEligibleNode) *rspView {
		return &rspView{EligibleNodes: nodes}
	}

	readyNode := func(name string) v1alpha1.ReplicatedStoragePoolEligibleNode {
		return v1alpha1.ReplicatedStoragePoolEligibleNode{NodeName: name, NodeReady: true, AgentReady: true}
	}

	It("creates Access RVR for active RVA on node without RVR", func(ctx SpecContext) {
		rv := mkRV(v1alpha1.VolumeAccessPreferablyLocal)
		cl := newClientBuilder(scheme).WithObjects(rv).Build()
		rec := NewReconciler(cl, scheme)

		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", v1alpha1.ReplicaTypeDiskful),
		}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-2")}
		rsp := mkRSP(readyNode("node-1"), readyNode("node-2"))

		outcome := rec.reconcileCreateAccessReplicas(ctx, rv, &rvrs, rvas, rsp)
		Expect(outcome.Error()).NotTo(HaveOccurred())

		// RVR was created and inserted into rvrs slice.
		Expect(rvrs).To(HaveLen(2))

		// Find the created Access RVR.
		var created *v1alpha1.ReplicatedVolumeReplica
		for _, rvr := range rvrs {
			if rvr.Spec.Type == v1alpha1.ReplicaTypeAccess {
				created = rvr
				break
			}
		}
		Expect(created).NotTo(BeNil())
		Expect(created.Spec.NodeName).To(Equal("node-2"))
		Expect(created.Spec.Type).To(Equal(v1alpha1.ReplicaTypeAccess))
		Expect(created.Spec.ReplicatedVolumeName).To(Equal("rv-1"))
		Expect(obju.HasFinalizer(created, v1alpha1.RVControllerFinalizer)).To(BeTrue())
	})

	It("skips node that already has an RVR (any type)", func(ctx SpecContext) {
		rv := mkRV(v1alpha1.VolumeAccessPreferablyLocal)
		cl := newClientBuilder(scheme).WithObjects(rv).Build()
		rec := NewReconciler(cl, scheme)

		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", v1alpha1.ReplicaTypeDiskful),
			mkRVR("rv-1-1", "node-2", v1alpha1.ReplicaTypeDiskful),
		}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-2")}
		rsp := mkRSP(readyNode("node-1"), readyNode("node-2"))

		outcome := rec.reconcileCreateAccessReplicas(ctx, rv, &rvrs, rvas, rsp)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(rvrs).To(HaveLen(2)) // no new RVR created
	})

	It("skips deleting RVAs", func(ctx SpecContext) {
		rv := mkRV(v1alpha1.VolumeAccessPreferablyLocal)
		cl := newClientBuilder(scheme).WithObjects(rv).Build()
		rec := NewReconciler(cl, scheme)

		rva := mkRVA("rva-1", "node-2")
		rva.DeletionTimestamp = ptr.To(metav1.Now())
		rva.Finalizers = []string{"test"}

		var rvrs []*v1alpha1.ReplicatedVolumeReplica
		rsp := mkRSP(readyNode("node-2"))

		outcome := rec.reconcileCreateAccessReplicas(ctx, rv, &rvrs, []*v1alpha1.ReplicatedVolumeAttachment{rva}, rsp)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(rvrs).To(BeEmpty())
	})

	It("skips when VolumeAccess is Local", func(ctx SpecContext) {
		rv := mkRV(v1alpha1.VolumeAccessLocal)
		cl := newClientBuilder(scheme).WithObjects(rv).Build()
		rec := NewReconciler(cl, scheme)

		var rvrs []*v1alpha1.ReplicatedVolumeReplica
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-2")}
		rsp := mkRSP(readyNode("node-2"))

		outcome := rec.reconcileCreateAccessReplicas(ctx, rv, &rvrs, rvas, rsp)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(rvrs).To(BeEmpty())
	})

	It("skips when RV is deleting", func(ctx SpecContext) {
		rv := mkRV(v1alpha1.VolumeAccessPreferablyLocal)
		rv.DeletionTimestamp = ptr.To(metav1.Now())
		rv.Finalizers = []string{v1alpha1.RVControllerFinalizer}
		cl := newClientBuilder(scheme).WithObjects(rv).Build()
		rec := NewReconciler(cl, scheme)

		var rvrs []*v1alpha1.ReplicatedVolumeReplica
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-2")}
		rsp := mkRSP(readyNode("node-2"))

		outcome := rec.reconcileCreateAccessReplicas(ctx, rv, &rvrs, rvas, rsp)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(rvrs).To(BeEmpty())
	})

	It("skips when RSP is nil", func(ctx SpecContext) {
		rv := mkRV(v1alpha1.VolumeAccessPreferablyLocal)
		cl := newClientBuilder(scheme).WithObjects(rv).Build()
		rec := NewReconciler(cl, scheme)

		var rvrs []*v1alpha1.ReplicatedVolumeReplica
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-2")}

		outcome := rec.reconcileCreateAccessReplicas(ctx, rv, &rvrs, rvas, nil)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(rvrs).To(BeEmpty())
	})

	It("skips node not in eligible nodes", func(ctx SpecContext) {
		rv := mkRV(v1alpha1.VolumeAccessPreferablyLocal)
		cl := newClientBuilder(scheme).WithObjects(rv).Build()
		rec := NewReconciler(cl, scheme)

		var rvrs []*v1alpha1.ReplicatedVolumeReplica
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-2")}
		rsp := mkRSP(readyNode("node-1")) // node-2 not eligible

		outcome := rec.reconcileCreateAccessReplicas(ctx, rv, &rvrs, rvas, rsp)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(rvrs).To(BeEmpty())
	})

	It("skips node where agent is not ready", func(ctx SpecContext) {
		rv := mkRV(v1alpha1.VolumeAccessPreferablyLocal)
		cl := newClientBuilder(scheme).WithObjects(rv).Build()
		rec := NewReconciler(cl, scheme)

		var rvrs []*v1alpha1.ReplicatedVolumeReplica
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-2")}
		rsp := mkRSP(v1alpha1.ReplicatedStoragePoolEligibleNode{
			NodeName: "node-2", NodeReady: true, AgentReady: false,
		})

		outcome := rec.reconcileCreateAccessReplicas(ctx, rv, &rvrs, rvas, rsp)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(rvrs).To(BeEmpty())
	})

	It("skips node where node is not ready", func(ctx SpecContext) {
		rv := mkRV(v1alpha1.VolumeAccessPreferablyLocal)
		cl := newClientBuilder(scheme).WithObjects(rv).Build()
		rec := NewReconciler(cl, scheme)

		var rvrs []*v1alpha1.ReplicatedVolumeReplica
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-2")}
		rsp := mkRSP(v1alpha1.ReplicatedStoragePoolEligibleNode{
			NodeName: "node-2", NodeReady: false, AgentReady: true,
		})

		outcome := rec.reconcileCreateAccessReplicas(ctx, rv, &rvrs, rvas, rsp)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(rvrs).To(BeEmpty())
	})

	It("creates multiple Access RVRs for multiple RVAs", func(ctx SpecContext) {
		rv := mkRV(v1alpha1.VolumeAccessAny)
		cl := newClientBuilder(scheme).WithObjects(rv).Build()
		rec := NewReconciler(cl, scheme)

		rvrs := []*v1alpha1.ReplicatedVolumeReplica{
			mkRVR("rv-1-0", "node-1", v1alpha1.ReplicaTypeDiskful),
		}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{
			mkRVA("rva-1", "node-2"),
			mkRVA("rva-2", "node-3"),
		}
		rsp := mkRSP(readyNode("node-1"), readyNode("node-2"), readyNode("node-3"))

		outcome := rec.reconcileCreateAccessReplicas(ctx, rv, &rvrs, rvas, rsp)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(rvrs).To(HaveLen(3)) // 1 Diskful + 2 Access
	})

	It("deduplicates when two RVAs point to the same node", func(ctx SpecContext) {
		rv := mkRV(v1alpha1.VolumeAccessPreferablyLocal)
		cl := newClientBuilder(scheme).WithObjects(rv).Build()
		rec := NewReconciler(cl, scheme)

		var rvrs []*v1alpha1.ReplicatedVolumeReplica
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{
			mkRVA("rva-1", "node-2"),
			mkRVA("rva-2", "node-2"),
		}
		rsp := mkRSP(readyNode("node-2"))

		outcome := rec.reconcileCreateAccessReplicas(ctx, rv, &rvrs, rvas, rsp)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(rvrs).To(HaveLen(1)) // only one Access created
	})

	It("allows creation with EventuallyLocal volumeAccess", func(ctx SpecContext) {
		rv := mkRV(v1alpha1.VolumeAccessEventuallyLocal)
		cl := newClientBuilder(scheme).WithObjects(rv).Build()
		rec := NewReconciler(cl, scheme)

		var rvrs []*v1alpha1.ReplicatedVolumeReplica
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-2")}
		rsp := mkRSP(readyNode("node-2"))

		outcome := rec.reconcileCreateAccessReplicas(ctx, rv, &rvrs, rvas, rsp)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(rvrs).To(HaveLen(1))
	})

	It("stops creating when replica limit (32) is reached", func(ctx SpecContext) {
		rv := mkRV(v1alpha1.VolumeAccessPreferablyLocal)
		cl := newClientBuilder(scheme).WithObjects(rv).Build()
		rec := NewReconciler(cl, scheme)

		// Fill all 32 IDs (0-31) with existing RVRs.
		rvrs := make([]*v1alpha1.ReplicatedVolumeReplica, 32)
		for i := range rvrs {
			nodeName := fmt.Sprintf("node-%d", i)
			rvrs[i] = mkRVR(v1alpha1.FormatReplicatedVolumeReplicaName("rv-1", uint8(i)), nodeName, v1alpha1.ReplicaTypeDiskful)
		}

		// RVA on a new node that doesn't have an RVR — would normally create one.
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-new")}
		rsp := mkRSP(readyNode("node-new"))

		outcome := rec.reconcileCreateAccessReplicas(ctx, rv, &rvrs, rvas, rsp)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(rvrs).To(HaveLen(32)) // no new RVR created — limit reached
	})

	It("skips node with existing deleting RVR", func(ctx SpecContext) {
		rv := mkRV(v1alpha1.VolumeAccessPreferablyLocal)
		cl := newClientBuilder(scheme).WithObjects(rv).Build()
		rec := NewReconciler(cl, scheme)

		deletingRVR := mkRVR("rv-1-1", "node-2", v1alpha1.ReplicaTypeAccess)
		deletingRVR.DeletionTimestamp = ptr.To(metav1.Now())
		deletingRVR.Finalizers = []string{v1alpha1.RVControllerFinalizer}
		rvrs := []*v1alpha1.ReplicatedVolumeReplica{deletingRVR}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-2")}
		rsp := mkRSP(readyNode("node-2"))

		outcome := rec.reconcileCreateAccessReplicas(ctx, rv, &rvrs, rvas, rsp)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(rvrs).To(HaveLen(1)) // no new RVR created
	})
})

// ──────────────────────────────────────────────────────────────────────────────
// Tests: reconcileDeleteAccessReplicas
//

var _ = Describe("reconcileDeleteAccessReplicas", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	})

	mkRV := func(
		members []v1alpha1.DatameshMember,
		transitions []v1alpha1.ReplicatedVolumeDatameshTransition,
	) *v1alpha1.ReplicatedVolume {
		return &v1alpha1.ReplicatedVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "rv-1"},
			Status: v1alpha1.ReplicatedVolumeStatus{
				Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
					Members: members,
				},
				DatameshTransitions: transitions,
			},
		}
	}

	mkRVR := func(name, nodeName string) *v1alpha1.ReplicatedVolumeReplica {
		rvr := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name:       name,
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1",
				NodeName:             nodeName,
				Type:                 v1alpha1.ReplicaTypeAccess,
			},
		}
		return rvr
	}

	mkRVA := func(name, nodeName string) *v1alpha1.ReplicatedVolumeAttachment { //nolint:unparam
		return &v1alpha1.ReplicatedVolumeAttachment{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec:       v1alpha1.ReplicatedVolumeAttachmentSpec{NodeName: nodeName, ReplicatedVolumeName: "rv-1"},
		}
	}

	It("deletes unused Access RVR (no active RVA on node)", func(ctx SpecContext) {
		rv := mkRV(nil, nil)
		rvr := mkRVR("rv-1-1", "node-2")
		cl := newClientBuilder(scheme).WithObjects(rvr).Build()
		rec := NewReconciler(cl, scheme)

		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}
		outcome := rec.reconcileDeleteAccessReplicas(ctx, rv, &rvrs, nil)
		Expect(outcome.Error()).NotTo(HaveOccurred())

		// DeletionTimestamp should be set on the in-memory object.
		Expect(rvr.DeletionTimestamp).NotTo(BeNil())
	})

	It("keeps Access RVR when active RVA exists on same node", func(ctx SpecContext) {
		rv := mkRV(nil, nil)
		rvr := mkRVR("rv-1-1", "node-2")
		cl := newClientBuilder(scheme).WithObjects(rvr).Build()
		rec := NewReconciler(cl, scheme)

		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-2")}

		outcome := rec.reconcileDeleteAccessReplicas(ctx, rv, &rvrs, rvas)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(rvr.DeletionTimestamp).To(BeNil())
	})

	It("deletes redundant Access RVR (another datamesh member on same node)", func(ctx SpecContext) {
		rv := mkRV([]v1alpha1.DatameshMember{
			{Name: "rv-1-0", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "node-2"},
			{Name: "rv-1-1", Type: v1alpha1.DatameshMemberTypeAccess, NodeName: "node-2"},
		}, nil)
		rvr := mkRVR("rv-1-1", "node-2")
		cl := newClientBuilder(scheme).WithObjects(rvr).Build()
		rec := NewReconciler(cl, scheme)

		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}
		// Even though there's an active RVA on this node, the Access is redundant.
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-2")}

		outcome := rec.reconcileDeleteAccessReplicas(ctx, rv, &rvrs, rvas)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(rvr.DeletionTimestamp).NotTo(BeNil())
	})

	It("does not delete attached Access RVR", func(ctx SpecContext) {
		rv := mkRV([]v1alpha1.DatameshMember{
			{Name: "rv-1-1", Type: v1alpha1.DatameshMemberTypeAccess, NodeName: "node-2", Attached: true},
		}, nil)
		rvr := mkRVR("rv-1-1", "node-2")
		cl := newClientBuilder(scheme).WithObjects(rvr).Build()
		rec := NewReconciler(cl, scheme)

		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}
		outcome := rec.reconcileDeleteAccessReplicas(ctx, rv, &rvrs, nil)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(rvr.DeletionTimestamp).To(BeNil())
	})

	It("does not delete Access RVR with active Detach transition", func(ctx SpecContext) {
		rv := mkRV(
			[]v1alpha1.DatameshMember{
				{Name: "rv-1-1", Type: v1alpha1.DatameshMemberTypeAccess, NodeName: "node-2"},
			},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{
				makeDatameshSingleStepTransition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeDetach, "rv-1-1", "Detach", 5),
			},
		)
		rvr := mkRVR("rv-1-1", "node-2")
		cl := newClientBuilder(scheme).WithObjects(rvr).Build()
		rec := NewReconciler(cl, scheme)

		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}
		outcome := rec.reconcileDeleteAccessReplicas(ctx, rv, &rvrs, nil)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(rvr.DeletionTimestamp).To(BeNil())
	})

	It("does not delete Access RVR with active AddAccessReplica transition", func(ctx SpecContext) {
		rv := mkRV(
			[]v1alpha1.DatameshMember{
				{Name: "rv-1-1", Type: v1alpha1.DatameshMemberTypeAccess, NodeName: "node-2"},
			},
			[]v1alpha1.ReplicatedVolumeDatameshTransition{
				makeDatameshSingleStepTransition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeAddAccessReplica, "rv-1-1", "✦ → A", 5),
			},
		)
		rvr := mkRVR("rv-1-1", "node-2")
		cl := newClientBuilder(scheme).WithObjects(rvr).Build()
		rec := NewReconciler(cl, scheme)

		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}
		outcome := rec.reconcileDeleteAccessReplicas(ctx, rv, &rvrs, nil)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(rvr.DeletionTimestamp).To(BeNil())
	})

	It("does not delete non-Access RVR", func(ctx SpecContext) {
		rv := mkRV(nil, nil)
		diskful := &v1alpha1.ReplicatedVolumeReplica{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "rv-1-0",
				Finalizers: []string{v1alpha1.RVControllerFinalizer},
			},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				ReplicatedVolumeName: "rv-1",
				NodeName:             "node-1",
				Type:                 v1alpha1.ReplicaTypeDiskful,
			},
		}
		cl := newClientBuilder(scheme).WithObjects(diskful).Build()
		rec := NewReconciler(cl, scheme)

		rvrs := []*v1alpha1.ReplicatedVolumeReplica{diskful}
		outcome := rec.reconcileDeleteAccessReplicas(ctx, rv, &rvrs, nil)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(diskful.DeletionTimestamp).To(BeNil())
	})

	It("does not delete already deleting Access RVR", func(ctx SpecContext) {
		rv := mkRV(nil, nil)
		rvr := mkRVR("rv-1-1", "node-2")
		rvr.DeletionTimestamp = ptr.To(metav1.Now())

		// Not registered with fake client (already deleting in-memory only).
		cl := newClientBuilder(scheme).Build()
		rec := NewReconciler(cl, scheme)

		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}
		outcome := rec.reconcileDeleteAccessReplicas(ctx, rv, &rvrs, nil)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		// DeletionTimestamp was already set before, no new delete call.
	})

	It("handles mixed: some to keep, some to delete", func(ctx SpecContext) {
		rv := mkRV(nil, nil)
		rvr1 := mkRVR("rv-1-1", "node-2") // no RVA → delete
		rvr2 := mkRVR("rv-1-2", "node-3") // has RVA → keep
		cl := newClientBuilder(scheme).WithObjects(rvr1, rvr2).Build()
		rec := NewReconciler(cl, scheme)

		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr1, rvr2}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-3")}

		outcome := rec.reconcileDeleteAccessReplicas(ctx, rv, &rvrs, rvas)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(rvr1.DeletionTimestamp).NotTo(BeNil()) // deleted
		Expect(rvr2.DeletionTimestamp).To(BeNil())    // kept
	})

	It("ignores deleting RVAs when checking active RVA on node", func(ctx SpecContext) {
		rv := mkRV(nil, nil)
		rvr := mkRVR("rv-1-1", "node-2")
		cl := newClientBuilder(scheme).WithObjects(rvr).Build()
		rec := NewReconciler(cl, scheme)

		deletingRVA := &v1alpha1.ReplicatedVolumeAttachment{
			ObjectMeta: metav1.ObjectMeta{Name: "rva-1", DeletionTimestamp: ptr.To(metav1.Now()), Finalizers: []string{"test"}},
			Spec:       v1alpha1.ReplicatedVolumeAttachmentSpec{NodeName: "node-2", ReplicatedVolumeName: "rv-1"},
		}

		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}
		outcome := rec.reconcileDeleteAccessReplicas(ctx, rv, &rvrs, []*v1alpha1.ReplicatedVolumeAttachment{deletingRVA})
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(rvr.DeletionTimestamp).NotTo(BeNil())
	})

	It("deletes both Access RVRs when two Access members exist on same node", func(ctx SpecContext) {
		rv := mkRV([]v1alpha1.DatameshMember{
			{Name: "rv-1-1", Type: v1alpha1.DatameshMemberTypeAccess, NodeName: "node-2"},
			{Name: "rv-1-2", Type: v1alpha1.DatameshMemberTypeAccess, NodeName: "node-2"},
		}, nil)
		rvr1 := mkRVR("rv-1-1", "node-2")
		rvr2 := mkRVR("rv-1-2", "node-2")
		cl := newClientBuilder(scheme).WithObjects(rvr1, rvr2).Build()
		rec := NewReconciler(cl, scheme)

		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr1, rvr2}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-2")}

		outcome := rec.reconcileDeleteAccessReplicas(ctx, rv, &rvrs, rvas)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		// Both are redundant: each sees the other as another member on the same node.
		Expect(rvr1.DeletionTimestamp).NotTo(BeNil())
		Expect(rvr2.DeletionTimestamp).NotTo(BeNil())
	})

	It("deletes non-member Access RVR when datamesh member exists on same node", func(ctx SpecContext) {
		rv := mkRV([]v1alpha1.DatameshMember{
			{Name: "rv-1-0", Type: v1alpha1.DatameshMemberTypeDiskful, NodeName: "node-2"},
		}, nil)
		// rv-1-1 is an Access RVR on node-2, NOT a datamesh member.
		rvr := mkRVR("rv-1-1", "node-2")
		cl := newClientBuilder(scheme).WithObjects(rvr).Build()
		rec := NewReconciler(cl, scheme)

		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-2")}

		outcome := rec.reconcileDeleteAccessReplicas(ctx, rv, &rvrs, rvas)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		// Redundant: Diskful member rv-1-0 already on the same node.
		Expect(rvr.DeletionTimestamp).NotTo(BeNil())
	})

	It("detects redundancy with TieBreaker member on same node", func(ctx SpecContext) {
		rv := mkRV([]v1alpha1.DatameshMember{
			{Name: "rv-1-0", Type: v1alpha1.DatameshMemberTypeTieBreaker, NodeName: "node-2"},
			{Name: "rv-1-1", Type: v1alpha1.DatameshMemberTypeAccess, NodeName: "node-2"},
		}, nil)
		rvr := mkRVR("rv-1-1", "node-2")
		cl := newClientBuilder(scheme).WithObjects(rvr).Build()
		rec := NewReconciler(cl, scheme)

		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}
		rvas := []*v1alpha1.ReplicatedVolumeAttachment{mkRVA("rva-1", "node-2")}

		outcome := rec.reconcileDeleteAccessReplicas(ctx, rv, &rvrs, rvas)
		Expect(outcome.Error()).NotTo(HaveOccurred())
		Expect(rvr.DeletionTimestamp).NotTo(BeNil())
	})

	It("verifies RVR is actually deleted in the API", func(ctx SpecContext) {
		rv := mkRV(nil, nil)
		rvr := mkRVR("rv-1-1", "node-2")
		cl := newClientBuilder(scheme).WithObjects(rvr).Build()
		rec := NewReconciler(cl, scheme)

		rvrs := []*v1alpha1.ReplicatedVolumeReplica{rvr}
		outcome := rec.reconcileDeleteAccessReplicas(ctx, rv, &rvrs, nil)
		Expect(outcome.Error()).NotTo(HaveOccurred())

		// Verify the object has DeletionTimestamp in the API
		// (fake client with finalizer keeps the object but marks it as deleting).
		var updated v1alpha1.ReplicatedVolumeReplica
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), &updated)).To(Succeed())
		Expect(updated.DeletionTimestamp).NotTo(BeNil())
	})
})
