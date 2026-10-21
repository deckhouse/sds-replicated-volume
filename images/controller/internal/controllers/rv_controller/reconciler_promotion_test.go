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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
)

var _ = Describe("in-place TB layout convergence", func() {
	It("cancels an unstarted storage-free reservation when the target returns to R2", func(ctx SpecContext) {
		rv, rvrs := promotionLayoutFixture()
		rv.Status.Configuration.GuaranteedMinimumDataRedundancy = 0
		tb := rvrs[2]
		tb.Spec.Type = v1alpha1.ReplicaTypeDiskful
		scheme := runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		cl := newClientBuilder(scheme).WithObjects(tb).Build()
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(tb), tb)).To(Succeed())
		rec := NewReconciler(cl, scheme)
		Expect(rec.reconcileLayoutConvergence(ctx, rv, &rvrs, nil).Error()).NotTo(HaveOccurred())
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(tb), tb)).To(Succeed())
		Expect(tb.Spec.Type).To(Equal(v1alpha1.ReplicaTypeTieBreaker))
		Expect(tb.Spec.NodeName).To(Equal("node-2"))
		_, report := computeTargetLayoutAction(rv, rvrs, nil)
		Expect(report.status).To(Equal(metav1.ConditionTrue))
	})

	It("keeps prepared storage after a pre-dispatch target reversal", func() {
		rv, rvrs := promotionLayoutFixture()
		rv.Status.Configuration.GuaranteedMinimumDataRedundancy = 0
		rvrs[2].Spec.Type = v1alpha1.ReplicaTypeDiskful
		rvrs[2].Spec.LVMVolumeGroupName = "admitted-vg"
		action, report := computeTargetLayoutAction(rv, rvrs, nil)
		Expect(action.kind).To(Equal(layoutActionNone))
		Expect(report.reason).To(Equal(v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonCannotConverge))
	})

	It("repairs a lost original voter before starting its reserved promotion", func() {
		rv, rvrs := promotionLayoutFixture()
		rvrs[2].Spec.Type = v1alpha1.ReplicaTypeDiskful
		rvrs[2].Spec.LVMVolumeGroupName = "admitted-vg"
		rv.Status.Datamesh.Members = rv.Status.Datamesh.Members[1:]
		rvrs = rvrs[1:]
		action, report := computeTargetLayoutAction(rv, rvrs, nil)
		Expect(action.kind).To(Equal(layoutActionCreateDiskful))
		Expect(report.message).To(ContainSubstring("restoring the original diskful copies"))
		pending := &v1alpha1.ReplicatedVolumeReplica{ObjectMeta: metav1.ObjectMeta{Name: "rv-1-3"}, Spec: v1alpha1.ReplicatedVolumeReplicaSpec{Type: v1alpha1.ReplicaTypeDiskful}}
		action, _ = computeTargetLayoutAction(rv, append(rvrs, pending), nil)
		Expect(action.kind).To(Equal(layoutActionNone), "only one repair copy is created")
	})

	It("persists the reservation on the existing RVR identity", func(ctx SpecContext) {
		rv, rvrs := promotionLayoutFixture()
		rvrs[2].UID = "original-tb"
		scheme := runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		cl := newClientBuilder(scheme).WithObjects(rvrs[0], rvrs[1], rvrs[2]).Build()
		rec := NewReconciler(cl, scheme)
		Expect(rec.reconcileLayoutConvergence(ctx, rv, &rvrs, nil).Error()).NotTo(HaveOccurred())
		tb := &v1alpha1.ReplicatedVolumeReplica{}
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvrs[2]), tb)).To(Succeed())
		Expect(tb.Spec.Type).To(Equal(v1alpha1.ReplicaTypeDiskful))
		Expect(string(tb.UID)).To(Equal("original-tb"))
		Expect(tb.Spec.NodeName).To(Equal("node-2"))
	})

	DescribeTable("reports a conflict with any diskful replica in flight after the reservation",
		func(mutate func(*v1alpha1.ReplicatedVolume, *v1alpha1.ReplicatedVolumeReplica)) {
			rv, rvrs := promotionLayoutFixture()
			rvrs[2].Spec.Type = v1alpha1.ReplicaTypeDiskful
			pending := &v1alpha1.ReplicatedVolumeReplica{ObjectMeta: metav1.ObjectMeta{Name: "rv-1-3"}, Spec: v1alpha1.ReplicatedVolumeReplicaSpec{Type: v1alpha1.ReplicaTypeDiskful}}
			mutate(rv, pending)
			action, report := computeTargetLayoutAction(rv, append(rvrs, pending), nil)
			Expect(action.kind).To(Equal(layoutActionNone))
			Expect(report.reason).To(Equal(v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonCannotConverge))
		},
		Entry("unplaced", func(_ *v1alpha1.ReplicatedVolume, _ *v1alpha1.ReplicatedVolumeReplica) {}),
		Entry("node assigned", func(_ *v1alpha1.ReplicatedVolume, rvr *v1alpha1.ReplicatedVolumeReplica) {
			rvr.Spec.NodeName = "node-3"
		}),
		Entry("backing volume observed", func(_ *v1alpha1.ReplicatedVolume, rvr *v1alpha1.ReplicatedVolumeReplica) {
			rvr.Status.BackingVolume = &v1alpha1.ReplicatedVolumeReplicaStatusBackingVolume{}
		}),
		Entry("Join request published", func(rv *v1alpha1.ReplicatedVolume, rvr *v1alpha1.ReplicatedVolumeReplica) {
			rv.Status.DatameshReplicaRequests = []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{{Name: rvr.Name, Request: v1alpha1.DatameshMembershipRequest{Operation: v1alpha1.DatameshMembershipRequestOperationJoin, Type: v1alpha1.ReplicaTypeDiskful}}}
		}),
	)

	It("reports a storage failure and a budget wait on the selected TB", func() {
		rv, rvrs := promotionLayoutFixture()
		tb := rvrs[2]
		tb.Spec.Type = v1alpha1.ReplicaTypeDiskful
		tb.Status.Conditions = append(tb.Status.Conditions, metav1.Condition{
			Type: v1alpha1.ReplicatedVolumeReplicaCondScheduledType, Status: metav1.ConditionFalse,
			Reason: v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonSchedulingFailed, Message: "no free capacity on node-2",
		})
		action, report := computeTargetLayoutAction(rv, rvrs, nil)
		Expect(action.kind).To(Equal(layoutActionNone))
		Expect(report.reason).To(Equal(v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonCannotConverge))
		Expect(report.message).To(ContainSubstring("no free capacity on node-2"))
		tb.Status.Conditions[1].Status = metav1.ConditionUnknown
		tb.Status.Conditions[1].Reason = v1alpha1.ReplicatedVolumeReplicaCondScheduledReasonWaitingForResyncSlot
		tb.Status.Conditions[1].Message = "sender is full"
		_, report = computeTargetLayoutAction(rv, rvrs, nil)
		Expect(report.reason).To(Equal(v1alpha1.ReplicatedVolumeCondMembershipLayoutConvergedReasonConverging))
		Expect(report.message).To(ContainSubstring("waiting for a free resync slot"))
	})
})
