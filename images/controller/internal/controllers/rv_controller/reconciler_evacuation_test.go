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
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/datamesh"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/idset"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/nodegrace"
)

type evacuationFixture struct {
	rv    *v1alpha1.ReplicatedVolume
	rsc   *v1alpha1.ReplicatedStorageClass
	rsp   *v1alpha1.ReplicatedStoragePool
	rvrs  []*v1alpha1.ReplicatedVolumeReplica
	nodes []*corev1.Node
	rvas  []*v1alpha1.ReplicatedVolumeAttachment
}

func newEvacuationFixture(diskful, dead int) evacuationFixture {
	rv, rvrs := convergenceFixture(byte(diskful-2), diskful, 0)
	rv.Spec.ReplicatedStorageClassName = "rsc-1"
	rv.Spec.Size = resource.MustParse("10Gi")
	rv.Status.Configuration.VolumeAccess = v1alpha1.VolumeAccessPreferablyLocal
	rv.Status.ConfigurationGeneration = 1
	rv.Status.ConfigurationObservedGeneration = 1
	rv.Status.DatameshRevision = 5
	rv.Status.Datamesh.Quorum = byte(diskful/2 + 1)
	rv.Status.Datamesh.QuorumMinimumRedundancy = byte(diskful - 1)
	rv.Status.BaselineGuaranteedMinimumDataRedundancy = byte(diskful - 2)
	rv.Status.Datamesh.SystemNetworkNames = []string{"Internal"}
	rv.Status.Datamesh.Size = rv.Spec.Size
	rsc := newRSCWithConfiguration("rsc-1")
	rsc.Status.Configuration = rv.Status.Configuration.DeepCopy()
	rsp := newTestRSP("test-pool")
	rsp.Spec.EligibleNodesPolicy.NotReadyGracePeriod.Duration = time.Minute
	f := evacuationFixture{rv: rv, rsc: rsc, rsp: rsp, rvrs: rvrs}
	for i, rvr := range rvrs {
		obju.AddFinalizer(rvr, v1alpha1.RVRControllerFinalizer)
		rvr.Status.DatameshRevision = 5
		rvr.Status.Quorum = new(true)
		rvr.Status.BackingVolume = &v1alpha1.ReplicatedVolumeReplicaStatusBackingVolume{State: v1alpha1.DiskStateUpToDate}
		rvr.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{{SystemNetworkName: "Internal"}}
		rv.Status.Datamesh.Members[i].Addresses = slices.Clone(rvr.Status.Addresses)
		rv.Status.Datamesh.Members[i].LVMVolumeGroupName = "lvg-1"
		node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: rvr.Spec.NodeName}, Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue, LastTransitionTime: metav1.NewTime(time.Now().Add(-time.Hour))}},
		}}
		if i >= diskful-dead {
			node.Status.Conditions[0].Status = corev1.ConditionFalse
			rvr.Status.DatameshRevision = 0
			obju.SetStatusCondition(rvr, metav1.Condition{
				Type: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType, Status: metav1.ConditionFalse,
				Reason: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonAgentNotReady,
			})
			obju.SetStatusCondition(rvr, metav1.Condition{
				Type: v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType, Status: metav1.ConditionFalse,
				Reason: v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonNodeMismatch,
			})
		} else {
			rsp.Status.EligibleNodes = append(rsp.Status.EligibleNodes, v1alpha1.ReplicatedStoragePoolEligibleNode{NodeName: node.Name})
		}
		f.nodes = append(f.nodes, node)
	}
	// A scheduler target for the replacement; the RV controller itself does not schedule it.
	rsp.Status.EligibleNodes = append(rsp.Status.EligibleNodes, v1alpha1.ReplicatedStoragePoolEligibleNode{NodeName: fmt.Sprintf("node-%d", diskful)})
	return f
}

func newTieBreakerEvacuationFixture() evacuationFixture {
	f := newEvacuationFixture(3, 1)
	f.rv.Status.Configuration.GuaranteedMinimumDataRedundancy = 0
	f.rsc.Status.Configuration = f.rv.Status.Configuration.DeepCopy()
	f.rv.Status.BaselineGuaranteedMinimumDataRedundancy = 0
	f.rv.Status.Datamesh.QuorumMinimumRedundancy = 1
	f.rv.Status.Datamesh.Members[2].Type = v1alpha1.DatameshMemberTypeTieBreaker
	f.rvrs[2].Spec.Type = v1alpha1.ReplicaTypeTieBreaker
	f.rvrs[2].Status.BackingVolume = nil
	return f
}

func (f evacuationFixture) start() *Reconciler {
	objects := []client.Object{f.rv, f.rsp}
	if f.rsc != nil {
		objects = append(objects, f.rsc)
	}
	for _, rvr := range f.rvrs {
		objects = append(objects, rvr)
	}
	for _, node := range f.nodes {
		objects = append(objects, node)
	}
	for _, rva := range f.rvas {
		objects = append(objects, rva)
	}
	scheme := newScheme()
	cl := newClientBuilder(scheme).WithObjects(objects...).WithStatusSubresource(
		&v1alpha1.ReplicatedVolume{}, &v1alpha1.ReplicatedStorageClass{}, &v1alpha1.ReplicatedVolumeReplica{}, &corev1.Node{},
	).Build()
	return NewReconciler(cl, scheme)
}

// run performs exactly one pass and reads back its persisted RV status.
// Agent reports never advance here; tests confirm them explicitly.
func (f evacuationFixture) run(ctx context.Context, rec *Reconciler) (*v1alpha1.ReplicatedVolume, reconcile.Result) {
	result, err := rec.Reconcile(ctx, RequestFor(f.rv))
	Expect(err).NotTo(HaveOccurred())
	updated, err := rec.getRV(ctx, f.rv.Name)
	Expect(err).NotTo(HaveOccurred())
	return updated, result
}

var _ = Describe("Root Reconcile evacuation", func() {
	It("preserves a blocked Leave and reports the dispatch blocker on every pass", func(ctx SpecContext) {
		f := newEvacuationFixture(3, 2)
		f.rv.Status.Datamesh.Members[1].Type = v1alpha1.DatameshMemberTypeLiminalDiskful
		f.rvrs[1].Status.DatameshRequest = &v1alpha1.DatameshMembershipRequest{
			Operation: v1alpha1.DatameshMembershipRequestOperationLeave,
		}
		f.rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{{
			Type:        v1alpha1.ReplicatedVolumeDatameshTransitionTypeRemoveReplica,
			Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupVotingMembership,
			ReplicaName: "rv-1-1", PlanID: "remove-diskful/v1", ReplicaType: v1alpha1.ReplicaTypeDiskful,
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{{
				Name: "D → D∅", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive,
				DatameshRevision: 5, StartedAt: new(metav1.NewTime(time.Unix(1, 0))),
			}},
		}}
		datamesh.ProcessTransitions(ctx, f.rv, nil, f.rvrs, nil, datamesh.FeatureFlags{})
		before := f.rv.DeepCopy()
		rec := f.start()
		for range 3 {
			updated, _ := f.run(ctx, rec)
			Expect(updated.Status.DatameshTransitions).To(Equal(before.Status.DatameshTransitions))
			Expect(updated.Status.Datamesh).To(Equal(before.Status.Datamesh))
			Expect(updated.Status.DatameshReplicaRequests).To(HaveLen(1))
			blocked := updated.Status.DatameshReplicaRequests[0].Message
			Expect(blocked).To(Equal("evacuation would leave 1 live voter(s), quorum after removal needs 2"))
			condition := obju.GetStatusCondition(updated, v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesType)
			Expect(condition).NotTo(BeNil())
			Expect(condition.Reason).To(Equal(v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonConflictResolutionBlocked))
			Expect(condition.Message).To(ContainSubstring(blocked))
		}

		var recoveredNode corev1.Node
		Expect(rec.cl.Get(ctx, client.ObjectKey{Name: "node-2"}, &recoveredNode)).To(Succeed())
		recoveredNode.Status.Conditions[0].Status = corev1.ConditionTrue
		Expect(rec.cl.Status().Update(ctx, &recoveredNode)).To(Succeed())
		var recoveredReplica v1alpha1.ReplicatedVolumeReplica
		Expect(rec.cl.Get(ctx, client.ObjectKey{Name: "rv-1-2"}, &recoveredReplica)).To(Succeed())
		obju.RemoveStatusCondition(&recoveredReplica, v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType)
		Expect(rec.cl.Status().Update(ctx, &recoveredReplica)).To(Succeed())
		updated, _ := f.run(ctx, rec)
		Expect(updated.Status.Datamesh.FindMemberByName("rv-1-1")).To(BeNil())
		Expect(updated.Status.DatameshTransitions).To(HaveLen(1))
		Expect(updated.Status.DatameshTransitions[0].Type).To(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeForceRemoveReplica))
		Expect(obju.GetStatusCondition(updated, v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesType).Reason).
			To(Equal(v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonConflictResolutionInProgress))
	})

	DescribeTable("retires the marked replica when its node returns before deletion", func(ctx SpecContext, manual bool) {
		f := newEvacuationFixture(3, 1)
		// Finish ForceRemove in the removal pass, leaving no transition as a marker.
		for _, rvr := range f.rvrs[:2] {
			rvr.Status.DatameshRevision = f.rv.Status.DatameshRevision + 1
		}
		// Start with settled metadata so every RV write here must target status.
		applyRVMetadata(f.rv, true)
		rec := f.start()
		metadataPatches, removalPatches := 0, 0
		rec.cl = interceptor.NewClient(rec.cl.(client.WithWatch), interceptor.Funcs{
			Patch: func(ctx context.Context, cl client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				if _, ok := obj.(*v1alpha1.ReplicatedVolume); ok {
					metadataPatches++
				}
				return cl.Patch(ctx, obj, patch, opts...)
			},
			SubResourcePatch: func(ctx context.Context, cl client.Client, sub string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				if rv, ok := obj.(*v1alpha1.ReplicatedVolume); ok {
					Expect(sub).To(Equal("status"))
					persisted, err := rec.getRV(ctx, f.rv.Name)
					Expect(err).NotTo(HaveOccurred())
					if persisted.Status.Datamesh.FindMemberByName("rv-1-2") != nil && rv.Status.Datamesh.FindMemberByName("rv-1-2") == nil {
						removalPatches++
						Expect(persisted.Status.EvacuatedReplicas).To(BeEmpty())
						Expect(rv.Status.EvacuatedReplicas).To(Equal([]string{"rv-1-2"}))
						data, err := patch.Data(obj)
						Expect(err).NotTo(HaveOccurred())
						Expect(string(data)).To(ContainSubstring(`"resourceVersion"`))
						Expect(string(data)).To(ContainSubstring(`"evacuatedReplicas":["rv-1-2"]`))
						Expect(string(data)).To(ContainSubstring(`"members"`))
					}
				}
				return cl.SubResource(sub).Patch(ctx, obj, patch, opts...)
			},
		})
		updated, result := f.run(ctx, rec)
		Expect(result).To(Requeue())
		Expect(updated.Status.Datamesh.FindMemberByName("rv-1-2")).To(BeNil())
		Expect(updated.Status.DatameshTransitions).To(BeEmpty())
		Expect(updated.Status.DatameshRevision).To(Equal(f.rv.Status.DatameshRevision + 1))
		Expect(obju.GetStatusCondition(updated, v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesType).Reason).
			To(Equal(v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonConflictResolutionInProgress))
		Expect(updated.Status.EvacuatedReplicas).To(Equal([]string{"rv-1-2"}))
		var old v1alpha1.ReplicatedVolumeReplica
		Expect(rec.cl.Get(ctx, client.ObjectKey{Name: "rv-1-2"}, &old)).To(Succeed())
		Expect(old.DeletionTimestamp).To(BeNil())
		Expect(metadataPatches).To(BeZero())
		Expect(removalPatches).To(Equal(1))

		// Model recovery and a renewed Join before the next controller pass.
		var node corev1.Node
		Expect(rec.cl.Get(ctx, client.ObjectKey{Name: "node-2"}, &node)).To(Succeed())
		node.Status.Conditions[0].Status = corev1.ConditionTrue
		Expect(rec.cl.Status().Update(ctx, &node)).To(Succeed())
		f.rsp.Status.EligibleNodes = append(f.rsp.Status.EligibleNodes, v1alpha1.ReplicatedStoragePoolEligibleNode{NodeName: node.Name, NodeReady: true})
		Expect(rec.cl.Update(ctx, f.rsp)).To(Succeed())
		old.Status.DatameshRequest = &v1alpha1.DatameshMembershipRequest{Operation: v1alpha1.DatameshMembershipRequestOperationJoin, Type: old.Spec.Type}
		Expect(rec.cl.Status().Update(ctx, &old)).To(Succeed())
		// Cover a Join already copied into the RV status as well as the RVR request.
		updated.Status.DatameshReplicaRequests = []v1alpha1.ReplicatedVolumeDatameshReplicaRequest{{Name: old.Name, Request: *old.Status.DatameshRequest}}
		Expect(rec.cl.Status().Update(ctx, updated)).To(Succeed())
		if manual {
			f.rsc.Spec.EligibleNodesConflictResolutionStrategy = &v1alpha1.ReplicatedStorageClassEligibleNodesConflictResolutionStrategy{Type: v1alpha1.EligibleNodesConflictResolutionManual}
			Expect(rec.cl.Update(ctx, f.rsc)).To(Succeed())
		}
		// Deletion changes resourceVersion: finalizer cleanup may first requeue on conflict.
		for range 2 {
			updated, _ = f.run(ctx, rec)
			Expect(updated.Status.Datamesh.FindMemberByName(old.Name)).To(BeNil())
			Expect(updated.Status.DatameshTransitions).To(BeEmpty())
			Expect(rec.cl.Get(ctx, client.ObjectKey{Name: old.Name}, &old)).To(Succeed())
			Expect(old.DeletionTimestamp).NotTo(BeNil())
			Expect(obju.GetStatusCondition(updated, v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesType).Reason).
				To(Equal(v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonConflictResolutionInProgress))
		}
		Expect(updated.Status.DatameshReplicaRequests).To(BeEmpty())
		Expect(metadataPatches).To(BeZero())
		Expect(removalPatches).To(Equal(1))
		rvrs, err := rec.getRVRsSorted(ctx, f.rv.Name)
		Expect(err).NotTo(HaveOccurred())
		Expect(rvrs).To(HaveLen(4))
		Expect(findRVRByName(rvrs, "rv-1-3").Spec.Type).To(Equal(v1alpha1.ReplicaTypeDiskful))
	}, Entry("RollingRepair", false), Entry("switched to Manual", true))

	It("leaves an unmarked non-member on a dead node alone", func(ctx SpecContext) {
		f := newEvacuationFixture(3, 1)
		f.rv.Status.Datamesh.Members = f.rv.Status.Datamesh.Members[:2]
		rec := f.start()
		for range 2 {
			updated, _ := f.run(ctx, rec)
			var old v1alpha1.ReplicatedVolumeReplica
			Expect(rec.cl.Get(ctx, client.ObjectKey{Name: "rv-1-2"}, &old)).To(Succeed())
			Expect(old.DeletionTimestamp).To(BeNil())
			Expect(updated.Status.EvacuatedReplicas).To(BeEmpty())
			Expect(obju.GetStatusCondition(updated, v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesType).Status).To(Equal(metav1.ConditionTrue))
		}
	})

	DescribeTable("does not mark manual ForceLeave or orphan removal as evacuation", func(ctx SpecContext, orphan bool) {
		f := newEvacuationFixture(3, 1)
		if orphan {
			f.rvrs = f.rvrs[:2]
		} else {
			f.rvrs[2].Status.DatameshRequest = &v1alpha1.DatameshMembershipRequest{Operation: v1alpha1.DatameshMembershipRequestOperationForceLeave, Type: v1alpha1.ReplicaTypeDiskful}
		}
		rec := f.start()
		for range 2 {
			updated, _ := f.run(ctx, rec)
			Expect(updated.Status.Datamesh.FindMemberByName("rv-1-2")).To(BeNil())
			Expect(updated.Status.EvacuatedReplicas).To(BeEmpty())
			rvrs, err := rec.getRVRsSorted(ctx, f.rv.Name)
			Expect(err).NotTo(HaveOccurred())
			for _, rvr := range rvrs {
				Expect(rvr.DeletionTimestamp).To(BeNil())
			}
		}
	}, Entry("manual ForceLeave on an evacuee", false), Entry("orphan", true))

	It("retries both the marker and removal after a failed status patch without prematurely deleting the RVR", func(ctx SpecContext) {
		f := newEvacuationFixture(3, 1)
		// Start with settled metadata so every RV write here must target status.
		applyRVMetadata(f.rv, true)
		rec := f.start()
		patchErr := errors.New("RV status patch failed")
		failStatus := true
		metadataPatches, removalAttempts := 0, 0
		rec.cl = interceptor.NewClient(rec.cl.(client.WithWatch), interceptor.Funcs{
			Patch: func(ctx context.Context, cl client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				if _, ok := obj.(*v1alpha1.ReplicatedVolume); ok {
					metadataPatches++
				}
				return cl.Patch(ctx, obj, patch, opts...)
			},
			SubResourcePatch: func(ctx context.Context, cl client.Client, sub string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				if rv, ok := obj.(*v1alpha1.ReplicatedVolume); ok && removalAttempts < 2 {
					removalAttempts++
					Expect(sub).To(Equal("status"))
					Expect(rv.Status.EvacuatedReplicas).To(Equal([]string{"rv-1-2"}))
					Expect(rv.Status.Datamesh.FindMemberByName("rv-1-2")).To(BeNil())
					if failStatus {
						return patchErr
					}
				}
				return cl.SubResource(sub).Patch(ctx, obj, patch, opts...)
			},
		})
		_, err := rec.Reconcile(ctx, RequestFor(f.rv))
		Expect(errors.Is(err, patchErr)).To(BeTrue())
		updated, err := rec.getRV(ctx, f.rv.Name)
		Expect(err).NotTo(HaveOccurred())
		Expect(updated.Status.Datamesh.FindMemberByName("rv-1-2")).NotTo(BeNil())
		var old v1alpha1.ReplicatedVolumeReplica
		Expect(rec.cl.Get(ctx, client.ObjectKey{Name: "rv-1-2"}, &old)).To(Succeed())
		Expect(updated.Status.EvacuatedReplicas).To(BeEmpty())
		Expect(old.DeletionTimestamp).To(BeNil())
		failStatus = false
		updated, _ = f.run(ctx, rec)
		Expect(updated.Status.Datamesh.FindMemberByName(old.Name)).To(BeNil())
		Expect(updated.Status.EvacuatedReplicas).To(Equal([]string{"rv-1-2"}))
		Expect(rec.cl.Get(ctx, client.ObjectKey{Name: old.Name}, &old)).To(Succeed())
		Expect(old.DeletionTimestamp).To(BeNil())
		f.run(ctx, rec)
		Expect(rec.cl.Get(ctx, client.ObjectKey{Name: old.Name}, &old)).To(Succeed())
		Expect(old.DeletionTimestamp).NotTo(BeNil())
		Expect(metadataPatches).To(BeZero())
		Expect(removalAttempts).To(Equal(2))
	})

	It("keeps a recovered member after its removal failed to persist", func(ctx SpecContext) {
		f := newEvacuationFixture(3, 1)
		// A failed status patch leaves both the membership and the empty marker unchanged.
		obju.SetAnnotation(f.rv, "example.test/operator", "preserved")
		f.nodes[2].Status.Conditions[0].Status = corev1.ConditionTrue
		f.rsp.Status.EligibleNodes = append(f.rsp.Status.EligibleNodes, v1alpha1.ReplicatedStoragePoolEligibleNode{NodeName: "node-2"})
		obju.RemoveStatusCondition(f.rvrs[2], v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType)
		rec := f.start()
		updated, _ := f.run(ctx, rec)
		Expect(updated.Status.Datamesh.FindMemberByName("rv-1-2")).NotTo(BeNil())
		Expect(updated.Status.DatameshTransitions).To(BeEmpty())
		Expect(updated.Status.EvacuatedReplicas).To(BeEmpty())
		Expect(updated.Annotations["example.test/operator"]).To(Equal("preserved"))
		Expect(obju.GetStatusCondition(updated, v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesType).Status).To(Equal(metav1.ConditionTrue))
		var old v1alpha1.ReplicatedVolumeReplica
		Expect(rec.cl.Get(ctx, client.ObjectKey{Name: "rv-1-2"}, &old)).To(Succeed())
		Expect(old.DeletionTimestamp).To(BeNil())
	})

	DescribeTable("does not schedule a grace deadline without an excluded NotReady member", func(ctx SpecContext, eligible bool) {
		f := newEvacuationFixture(3, 1)
		if eligible {
			f.nodes[2].Status.Conditions[0].LastTransitionTime = metav1.NewTime(time.Now())
			f.rsp.Status.EligibleNodes = append(f.rsp.Status.EligibleNodes, v1alpha1.ReplicatedStoragePoolEligibleNode{NodeName: "node-2"})
		} else {
			f.nodes[2].Status.Conditions = nil
		}
		rec := f.start()
		_, result := f.run(ctx, rec)
		Expect(result.RequeueAfter).To(BeZero())
	}, Entry("node is still eligible", true), Entry("Node has no Ready condition", false))

	It("requeues at grace expiration for an already excluded node, then evacuates", func(ctx SpecContext) {
		f := newEvacuationFixture(3, 1)
		f.nodes[2].Status.Conditions[0].LastTransitionTime = metav1.NewTime(time.Now().Add(-20 * time.Second))
		rec := f.start()
		updated, result := f.run(ctx, rec)
		Expect(result.RequeueAfter).To(BeNumerically("~", 40*time.Second, 2*time.Second))
		Expect(updated.Status.Datamesh.FindMemberByName("rv-1-2")).NotTo(BeNil())
		Expect(updated.Status.DatameshTransitions).To(BeEmpty())
		var node corev1.Node
		Expect(rec.cl.Get(ctx, client.ObjectKey{Name: "node-2"}, &node)).To(Succeed())
		node.Status.Conditions[0].LastTransitionTime = metav1.NewTime(time.Now().Add(-2 * time.Minute))
		Expect(rec.cl.Status().Update(ctx, &node)).To(Succeed())
		updated, result = f.run(ctx, rec)
		Expect(updated.Status.Datamesh.FindMemberByName("rv-1-2")).To(BeNil())
	})

	DescribeTable("reports the guard blocking evacuation", func(ctx SpecContext, change func(*evacuationFixture), name, node, blocked, inventory string) {
		f := newEvacuationFixture(3, 1)
		change(&f)
		rec := f.start()
		updated, _ := f.run(ctx, rec)
		condition := obju.GetStatusCondition(updated, v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesType)
		Expect(condition).NotTo(BeNil())
		Expect(condition.Status).To(Equal(metav1.ConditionFalse))
		Expect(condition.Reason).To(Equal(v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonConflictResolutionBlocked))
		Expect(condition.Message).To(Equal(fmt.Sprintf("Evacuation of replica %s from node %s is blocked: %s; %s", name, node, blocked, inventory)))
		Expect(updated.Status.Datamesh.FindMemberByName(name)).NotTo(BeNil())
		Expect(updated.Status.DatameshTransitions).NotTo(ContainElement(
			HaveField("Type", v1alpha1.ReplicatedVolumeDatameshTransitionTypeForceRemoveReplica)))
		Expect(updated.Status.EligibleNodesViolations).To(ContainElement(And(
			HaveField("ReplicaName", name), HaveField("NodeName", node),
		)))
	},
		Entry("attached", func(f *evacuationFixture) {
			f.rv.Status.Datamesh.Members[2].Attached = true
			f.rvas = []*v1alpha1.ReplicatedVolumeAttachment{{ObjectMeta: metav1.ObjectMeta{Name: "rva-2"}, Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{ReplicatedVolumeName: f.rv.Name, NodeName: "node-2"}}}
		}, "rv-1-2", "node-2", "replica is attached, detach required first",
			"1/3 members are not on eligible nodes: replica rv-1-2 on node node-2 (NodeMismatch)"),
		Entry("reachable with peer names", func(f *evacuationFixture) {
			for _, rvr := range f.rvrs[:2] {
				rvr.Status.Peers = []v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{{Name: "rv-1-2", ConnectionState: v1alpha1.ConnectionStateConnected}}
			}
		}, "rv-1-2", "node-2", "member is reachable over DRBD (Connected from 2 replica(s): [rv-1-0, rv-1-1])",
			"1/3 members are not on eligible nodes: replica rv-1-2 on node node-2 (NodeMismatch)"),
		Entry("3D with two dead nodes", func(f *evacuationFixture) {
			*f = newEvacuationFixture(3, 2)
		}, "rv-1-1", "node-1", "evacuation would leave 1 live voter(s), quorum after removal needs 2",
			"2/3 members are not on eligible nodes: replica rv-1-1 on node node-1 (NodeMismatch)"),
		Entry("no live UpToDate copy", func(f *evacuationFixture) {
			for _, rvr := range f.rvrs[:2] {
				rvr.Status.BackingVolume.State = v1alpha1.DiskStateOutdated
			}
		}, "rv-1-2", "node-2", "no live UpToDate diskful replica would remain",
			"1/3 members are not on eligible nodes: replica rv-1-2 on node node-2 (NodeMismatch)"),
		Entry("live label offender sorts before the blocked evacuee", func(f *evacuationFixture) {
			f.rsp.Status.EligibleNodes = slices.DeleteFunc(f.rsp.Status.EligibleNodes, func(n v1alpha1.ReplicatedStoragePoolEligibleNode) bool { return n.NodeName == "node-0" })
			obju.SetStatusCondition(f.rvrs[0], metav1.Condition{
				Type: v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType, Status: metav1.ConditionFalse,
				Reason: v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonNodeMismatch, Message: "node labels do not match the storage pool selector",
			})
			f.rvrs[0].Status.Peers = []v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{{Name: "rv-1-2", ConnectionState: v1alpha1.ConnectionStateConnected}}
		}, "rv-1-2", "node-2", "member is reachable over DRBD (Connected from 1 replica(s): [rv-1-0])",
			"2/3 members are not on eligible nodes: replica rv-1-0 on node node-0 (node labels do not match the storage pool selector)"),
	)

	It("keeps InProgress during replacement when a live label offender remains after removal", func(ctx SpecContext) {
		f := newEvacuationFixture(3, 1)
		f.rsp.Status.EligibleNodes = slices.DeleteFunc(f.rsp.Status.EligibleNodes, func(n v1alpha1.ReplicatedStoragePoolEligibleNode) bool { return n.NodeName == "node-0" })
		obju.SetStatusCondition(f.rvrs[0], metav1.Condition{
			Type: v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType, Status: metav1.ConditionFalse,
			Reason: v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesReasonNodeMismatch,
		})
		// Finish ForceRemove immediately; only the missing replacement can identify the repair.
		for _, rvr := range f.rvrs[:2] {
			rvr.Status.DatameshRevision = f.rv.Status.DatameshRevision + 1
		}
		rec := f.start()
		updated, _ := f.run(ctx, rec)
		Expect(updated.Status.Datamesh.FindMemberByName("rv-1-2")).To(BeNil())
		Expect(updated.Status.DatameshTransitions).To(BeEmpty())
		condition := obju.GetStatusCondition(updated, v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesType)
		Expect(condition).NotTo(BeNil())
		Expect(condition.Status).To(Equal(metav1.ConditionFalse))
		Expect(condition.Reason).To(Equal(v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonConflictResolutionInProgress))
		Expect(condition.Message).To(Equal("Replacing evacuated replica rv-1-2 on node node-2: have 2D, want 3D; UpToDate voters 2/3; 1/2 members are not on eligible nodes: replica rv-1-0 on node node-0 (NodeMismatch)"))
		Expect(updated.Status.EligibleNodesViolations).To(HaveLen(1))
	})

	DescribeTable("derives evacuation from Node grace, eligibility and strategy", func(ctx SpecContext, change func(*evacuationFixture), wantRemoved bool, wantReason string) {
		f := newEvacuationFixture(3, 1)
		change(&f)
		rec := f.start()
		updated, result := f.run(ctx, rec)

		Expect(updated.Status.Datamesh.FindMemberByName("rv-1-2") == nil).To(Equal(wantRemoved))
		var old v1alpha1.ReplicatedVolumeReplica
		Expect(rec.cl.Get(ctx, client.ObjectKey{Name: "rv-1-2"}, &old)).To(Succeed())
		Expect(old.DeletionTimestamp).To(BeNil())
		if wantRemoved {
			Expect(result).To(Requeue())
			Expect(updated.Status.DatameshTransitions).To(ContainElement(And(
				HaveField("Type", v1alpha1.ReplicatedVolumeDatameshTransitionTypeForceRemoveReplica),
				HaveField("ReplicaName", "rv-1-2"),
			)))
		} else {
			for _, transition := range updated.Status.DatameshTransitions {
				Expect(transition.Type).NotTo(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeForceRemoveReplica))
			}
		}
		condition := obju.GetStatusCondition(updated, v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesType)
		if wantReason == "" { // Maintenance freezes placement conditions.
			Expect(condition).To(BeNil())
		} else {
			Expect(condition).NotTo(BeNil())
			Expect(condition.Reason).To(Equal(wantReason))
			if wantReason == v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonSatisfyEligibleNodes {
				Expect(condition.Status).To(Equal(metav1.ConditionTrue))
			} else {
				Expect(condition.Status).To(Equal(metav1.ConditionFalse))
			}
		}
	},
		Entry("NotReady within grace, still eligible", func(f *evacuationFixture) {
			f.nodes[2].Status.Conditions[0].LastTransitionTime = metav1.Now()
			f.rsp.Status.EligibleNodes = append(f.rsp.Status.EligibleNodes, v1alpha1.ReplicatedStoragePoolEligibleNode{NodeName: "node-2"})
			obju.RemoveStatusCondition(f.rvrs[2], v1alpha1.ReplicatedVolumeReplicaCondSatisfyEligibleNodesType)
		}, false, v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonSatisfyEligibleNodes),
		Entry("NotReady within grace but excluded for another reason", func(f *evacuationFixture) {
			f.nodes[2].Status.Conditions[0].LastTransitionTime = metav1.Now()
		}, false, v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonInConflictWithEligibleNodes),
		Entry("NotReady beyond grace", func(_ *evacuationFixture) {}, true, v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonConflictResolutionInProgress),
		Entry("explicit RollingRepair", func(f *evacuationFixture) {
			f.rsc.Spec.EligibleNodesConflictResolutionStrategy = &v1alpha1.ReplicatedStorageClassEligibleNodesConflictResolutionStrategy{Type: v1alpha1.EligibleNodesConflictResolutionRollingRepair}
		}, true, v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonConflictResolutionInProgress),
		Entry("deleted Node", func(f *evacuationFixture) { f.nodes = f.nodes[:2] }, true, v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonConflictResolutionInProgress),
		Entry("Node has no Ready condition", func(f *evacuationFixture) { f.nodes[2].Status.Conditions = nil }, false, v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonInConflictWithEligibleNodes),
		Entry("Ready node excluded by labels", func(f *evacuationFixture) {
			f.nodes[2].Status.Conditions[0].Status = corev1.ConditionTrue
			f.rvrs[2].Status.DatameshRevision = f.rv.Status.DatameshRevision
			obju.SetStatusCondition(f.rvrs[2], metav1.Condition{
				Type: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredType, Status: metav1.ConditionTrue,
				Reason: v1alpha1.ReplicatedVolumeReplicaCondDRBDConfiguredReasonConfigured,
			})
		}, false, v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonInConflictWithEligibleNodes),
		Entry("Manual strategy", func(f *evacuationFixture) {
			f.rsc.Spec.EligibleNodesConflictResolutionStrategy = &v1alpha1.ReplicatedStorageClassEligibleNodesConflictResolutionStrategy{Type: v1alpha1.EligibleNodesConflictResolutionManual}
		}, false, v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonInConflictWithEligibleNodes),
		Entry("RV without RSC", func(f *evacuationFixture) {
			f.rsc = nil
			f.rv.Spec.ReplicatedStorageClassName = ""
			f.rv.Spec.ConfigurationMode = v1alpha1.ReplicatedVolumeConfigurationModeManual
			f.rv.Spec.ManualConfiguration = f.rv.Status.Configuration.DeepCopy()
		}, false, v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonInConflictWithEligibleNodes),
		Entry("maintenance", func(f *evacuationFixture) { f.rv.Spec.Maintenance = v1alpha1.MaintenanceModeNoResourceReconciliation }, false, ""),
		Entry("attached evacuee", func(f *evacuationFixture) {
			f.rv.Status.Datamesh.Members[2].Attached = true
			f.rvas = []*v1alpha1.ReplicatedVolumeAttachment{{ObjectMeta: metav1.ObjectMeta{Name: "rva-2"}, Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{ReplicatedVolumeName: f.rv.Name, NodeName: "node-2"}}}
		}, false, v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonConflictResolutionBlocked),
		Entry("live peer sees Connected", func(f *evacuationFixture) {
			f.rvrs[0].Status.Peers = []v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{{Name: "rv-1-2", ConnectionState: v1alpha1.ConnectionStateConnected}}
		}, false, v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonConflictResolutionBlocked),
		Entry("LiminalDiskful", func(f *evacuationFixture) {
			f.rv.Status.Datamesh.Members[2].Type = v1alpha1.DatameshMemberTypeLiminalDiskful
		}, true, v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonConflictResolutionInProgress),
	)

	DescribeTable("deletes only after persisted removal and restores the layout", func(ctx SpecContext, tieBreaker bool) {
		f := newEvacuationFixture(3, 1)
		replicaType := v1alpha1.ReplicaTypeDiskful
		memberType := v1alpha1.DatameshMemberTypeDiskful
		plan, layout := "diskful/v1", "3D"
		if tieBreaker {
			f = newTieBreakerEvacuationFixture()
			replicaType = v1alpha1.ReplicaTypeTieBreaker
			memberType = v1alpha1.DatameshMemberTypeTieBreaker
			plan, layout = "tiebreaker/v1", "2D+1TB"
		}
		f.rvrs[0].Status.Peers = []v1alpha1.ReplicatedVolumeReplicaStatusPeerStatus{{Name: "rv-1-2", ConnectionState: v1alpha1.ConnectionStateConnected}}
		rec := f.start()
		updated, _ := f.run(ctx, rec)
		Expect(updated.Status.Datamesh.FindMemberByName("rv-1-2")).NotTo(BeNil())
		condition := obju.GetStatusCondition(updated, v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesType)
		Expect(condition).NotTo(BeNil())
		Expect(condition.Status).To(Equal(metav1.ConditionFalse))
		Expect(condition.Reason).To(Equal(v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonConflictResolutionBlocked))
		Expect(condition.Message).To(ContainSubstring("Connected from 1 replica(s): [rv-1-0]"))
		var old, survivor v1alpha1.ReplicatedVolumeReplica
		Expect(rec.cl.Get(ctx, client.ObjectKey{Name: "rv-1-2"}, &old)).To(Succeed())
		Expect(old.DeletionTimestamp).To(BeNil())

		Expect(rec.cl.Get(ctx, client.ObjectKey{Name: "rv-1-0"}, &survivor)).To(Succeed())
		survivor.Status.Peers = nil
		Expect(rec.cl.Status().Update(ctx, &survivor)).To(Succeed())
		updated, result := f.run(ctx, rec)
		Expect(result).To(Requeue())
		Expect(updated.Status.Datamesh.FindMemberByName("rv-1-2")).To(BeNil())
		Expect(rec.cl.Get(ctx, client.ObjectKey{Name: "rv-1-2"}, &old)).To(Succeed())
		Expect(old.DeletionTimestamp).To(BeNil()) // Removal must be persisted before deletion.
		Expect(updated.Status.EvacuatedReplicas).To(Equal([]string{"rv-1-2"}))
		rvrs, err := rec.getRVRsSorted(ctx, f.rv.Name)
		Expect(err).NotTo(HaveOccurred())
		Expect(rvrs).To(HaveLen(3)) // P2/P3 still sees the old RVR as a replica in flight.
		Expect(updated.Status.DatameshTransitions).To(ContainElement(And(
			HaveField("Type", v1alpha1.ReplicatedVolumeDatameshTransitionTypeForceRemoveReplica),
			HaveField("ReplicaName", old.Name), HaveField("PlanID", plan),
		)))
		Expect(obju.GetStatusCondition(updated, v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesType).Reason).
			To(Equal(v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonConflictResolutionInProgress))

		// The next pass observes the persisted removal and deletes the RVR.
		updated, _ = f.run(ctx, rec)
		Expect(updated.Status.Datamesh.FindMemberByName("rv-1-2")).To(BeNil())
		Expect(rec.cl.Get(ctx, client.ObjectKey{Name: "rv-1-2"}, &old)).To(Succeed())
		Expect(old.DeletionTimestamp).NotTo(BeNil())
		Expect(updated.Status.DatameshTransitions).To(HaveLen(1)) // Does not delay RVR deletion.
		Expect(obju.GetStatusCondition(updated, v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesType).Message).To(And(
			ContainSubstring("rv-1-2"), ContainSubstring("node-2"), ContainSubstring("have 2D, want "+layout),
		))

		// Only the survivors confirm ForceRemove. P2/P3 runs unchanged after it completes.
		for _, name := range []string{"rv-1-0", "rv-1-1"} {
			Expect(rec.cl.Get(ctx, client.ObjectKey{Name: name}, &survivor)).To(Succeed())
			survivor.Status.DatameshRevision = updated.Status.DatameshRevision
			Expect(rec.cl.Status().Update(ctx, &survivor)).To(Succeed())
		}
		updated, _ = f.run(ctx, rec)
		Expect(updated.Status.DatameshTransitions).To(BeEmpty())
		rvrs, err = rec.getRVRsSorted(ctx, f.rv.Name)
		Expect(err).NotTo(HaveOccurred())
		Expect(rvrs).To(HaveLen(4))
		replacement := findRVRByName(rvrs, "rv-1-3")
		Expect(replacement).NotTo(BeNil())
		Expect(replacement.Spec.Type).To(Equal(replicaType))
		Expect(replacement.DeletionTimestamp).To(BeNil())
		Expect(obju.GetStatusCondition(updated, v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesType).Reason).To(Equal(v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonConflictResolutionInProgress))

		// Model scheduler/agent reports, then let the normal datamesh Join restore the layout.
		replacement.Spec.NodeName = "node-3"
		replacement.Spec.LVMVolumeGroupName = "lvg-1"
		Expect(rec.cl.Update(ctx, replacement)).To(Succeed())
		replacement.Status.Addresses = []v1alpha1.DRBDResourceAddressStatus{{SystemNetworkName: "Internal"}}
		replacement.Status.DatameshRequest = &v1alpha1.DatameshMembershipRequest{Operation: v1alpha1.DatameshMembershipRequestOperationJoin, Type: replicaType, LVMVolumeGroupName: "lvg-1"}
		replacement.Status.BackingVolume = &v1alpha1.ReplicatedVolumeReplicaStatusBackingVolume{State: v1alpha1.DiskStateUpToDate}
		replacement.Status.Quorum = new(true)
		Expect(rec.cl.Status().Update(ctx, replacement)).To(Succeed())
		updated, _ = f.run(ctx, rec)
		Expect(updated.Status.Datamesh.FindMemberByName(replacement.Name)).NotTo(BeNil())
		Expect(obju.GetStatusCondition(updated, v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesType).Status).To(Equal(metav1.ConditionTrue))
		for range 2 {
			for _, name := range []string{"rv-1-0", "rv-1-1", replacement.Name} {
				Expect(rec.cl.Get(ctx, client.ObjectKey{Name: name}, &survivor)).To(Succeed())
				survivor.Status.DatameshRevision = updated.Status.DatameshRevision
				Expect(rec.cl.Status().Update(ctx, &survivor)).To(Succeed())
			}
			updated, _ = f.run(ctx, rec)
		}
		Expect(updated.Status.Datamesh.FindMemberByName(replacement.Name).Type).To(Equal(memberType))
		Expect(updated.Status.MembershipLayout).To(Equal(new(layout)))
		Expect(updated.Status.DatameshTransitions).To(BeEmpty())
		Expect(obju.GetStatusCondition(updated, v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesType).Status).To(Equal(metav1.ConditionTrue))
		Expect(rec.cl.Get(ctx, client.ObjectKey{Name: old.Name}, &old)).To(Succeed())
		Expect(old.DeletionTimestamp).NotTo(BeNil()) // Terminating alone does not keep the conflict.
	}, Entry("Diskful through P3", false), Entry("TieBreaker through P2", true))

	It("keeps a removed TB in flight until its RVR deletion even if ForceRemove already completed", func(ctx SpecContext) {
		f := newTieBreakerEvacuationFixture()
		// Make the live confirmations immediately available for the removal revision.
		for _, rvr := range f.rvrs[:2] {
			rvr.Status.DatameshRevision = f.rv.Status.DatameshRevision + 1
		}
		rec := f.start()
		updated, result := f.run(ctx, rec)
		Expect(result).To(Requeue())
		Expect(updated.Status.Datamesh.FindMemberByName("rv-1-2")).To(BeNil())
		Expect(updated.Status.DatameshTransitions).To(BeEmpty())
		rvrs, err := rec.getRVRsSorted(ctx, f.rv.Name)
		Expect(err).NotTo(HaveOccurred())
		Expect(rvrs).To(HaveLen(3))
		Expect(findRVRByName(rvrs, "rv-1-2").DeletionTimestamp).To(BeNil())
		Expect(obju.GetStatusCondition(updated, v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesType).Reason).
			To(Equal(v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonConflictResolutionInProgress))

		f.run(ctx, rec)
		rvrs, err = rec.getRVRsSorted(ctx, f.rv.Name)
		Expect(err).NotTo(HaveOccurred())
		Expect(rvrs).To(HaveLen(4))
		Expect(findRVRByName(rvrs, "rv-1-2").DeletionTimestamp).NotTo(BeNil())
		Expect(findRVRByName(rvrs, "rv-1-3").Spec.Type).To(Equal(v1alpha1.ReplicaTypeTieBreaker))
	})

	DescribeTable("does not evacuate or delete Access and ShadowDiskful on a dead node", func(
		ctx SpecContext, memberType v1alpha1.DatameshMemberType, replicaType v1alpha1.ReplicaType,
	) {
		f := newTieBreakerEvacuationFixture()
		f.rv.Status.Datamesh.Members[2].Type = memberType
		f.rvrs[2].Spec.Type = replicaType
		// Access has a consumer, so the independent unused-Access cleanup does not apply.
		if replicaType == v1alpha1.ReplicaTypeAccess {
			f.rvas = []*v1alpha1.ReplicatedVolumeAttachment{{
				ObjectMeta: metav1.ObjectMeta{Name: "rva-2"},
				Spec:       v1alpha1.ReplicatedVolumeAttachmentSpec{ReplicatedVolumeName: f.rv.Name, NodeName: "node-2"},
			}}
		}
		rec := f.start()
		for range 2 {
			updated, _ := f.run(ctx, rec)
			Expect(updated.Status.Datamesh.FindMemberByName("rv-1-2")).NotTo(BeNil())
			Expect(updated.Status.Datamesh.FindMemberByName("rv-1-2").Type).To(Equal(memberType))
			for _, transition := range updated.Status.DatameshTransitions {
				Expect(transition.Type).NotTo(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeForceRemoveReplica))
			}
			var untouched v1alpha1.ReplicatedVolumeReplica
			Expect(rec.cl.Get(ctx, client.ObjectKey{Name: "rv-1-2"}, &untouched)).To(Succeed())
			Expect(untouched.DeletionTimestamp).To(BeNil())
		}
	},
		Entry("Access", v1alpha1.DatameshMemberTypeAccess, v1alpha1.ReplicaTypeAccess),
		Entry("ShadowDiskful", v1alpha1.DatameshMemberTypeShadowDiskful, v1alpha1.ReplicaTypeShadowDiskful),
		Entry("LiminalShadowDiskful", v1alpha1.DatameshMemberTypeLiminalShadowDiskful, v1alpha1.ReplicaTypeShadowDiskful),
	)

	DescribeTable("handles two evacuees using the live majority", func(ctx SpecContext, diskful int, removed bool) {
		f := newEvacuationFixture(diskful, 2)
		rec := f.start()
		updated, result := f.run(ctx, rec)
		wantMembers := diskful
		if removed {
			Expect(result).To(Requeue())
			wantMembers -= 2
		}
		Expect(updated.Status.Datamesh.Members).To(HaveLen(wantMembers))
		if removed {
			Expect(updated.Status.DatameshTransitions).To(HaveLen(2))
			Expect(updated.Status.DatameshTransitions[0].ReplicaName).To(Equal("rv-1-2"))
			Expect(updated.Status.DatameshTransitions[0].PlanID).To(Equal("diskful-q-down/v1"))
			Expect(updated.Status.DatameshTransitions[1].ReplicaName).To(Equal("rv-1-3"))
			Expect(updated.Status.DatameshTransitions[1].PlanID).To(Equal("diskful/v1"))
		} else {
			Expect(updated.Status.DatameshTransitions).To(BeEmpty())
			Expect(obju.GetStatusCondition(updated, v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesType).Reason).To(Equal(v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonConflictResolutionBlocked))
		}
		for _, original := range f.rvrs[diskful-2:] {
			var rvr v1alpha1.ReplicatedVolumeReplica
			Expect(rec.cl.Get(ctx, client.ObjectKeyFromObject(original), &rvr)).To(Succeed())
			Expect(rvr.DeletionTimestamp).To(BeNil())
		}
		f.run(ctx, rec)
		for _, original := range f.rvrs[diskful-2:] {
			var rvr v1alpha1.ReplicatedVolumeReplica
			Expect(rec.cl.Get(ctx, client.ObjectKeyFromObject(original), &rvr)).To(Succeed())
			Expect(rvr.DeletionTimestamp != nil).To(Equal(removed))
		}
	}, Entry("4D: both leave in sequence", 4, true), Entry("3D: neither can leave", 3, false))
})

func TestGetNodeReadyState(t *testing.T) {
	scheme := newScheme()
	readErr := errors.New("node cache failed")
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}, Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}}}
	cl := newClientBuilder(scheme).WithObjects(node).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			options := &client.GetOptions{}
			for _, opt := range opts {
				opt.ApplyToGet(options)
			}
			if options.UnsafeDisableDeepCopy == nil || !*options.UnsafeDisableDeepCopy {
				t.Fatal("Node read must disable deepcopy")
			}
			if key.Name == "failed" {
				return readErr
			}
			return cl.Get(ctx, key, obj, opts...)
		},
	}).Build()
	rec := NewReconciler(cl, scheme)
	state, err := rec.getNodeReadyState(t.Context(), node.Name)
	if err != nil || state == nil || !state.Status {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	state.Status = false
	next, err := rec.getNodeReadyState(t.Context(), node.Name)
	if err != nil || !next.Status {
		t.Fatalf("snapshot aliases cached Node: %+v %v", next, err)
	}
	absent, err := rec.getNodeReadyState(t.Context(), "absent")
	if err != nil || absent != nil {
		t.Fatalf("NotFound: state=%+v err=%v", absent, err)
	}
	_, err = rec.getNodeReadyState(t.Context(), "failed")
	if !errors.Is(err, readErr) {
		t.Fatalf("got %v, want %v", err, readErr)
	}
}

func TestComputeActualEvacuationMemberTypes(t *testing.T) {
	rv, rvrs := convergenceFixture(1, 3, 0)
	now := time.Date(2026, time.November, 11, 12, 0, 0, 0, time.UTC)
	states := map[string]*nodegrace.ReadyState{"node-0": nil, "node-1": nil, "node-2": nil}
	for _, memberType := range []v1alpha1.DatameshMemberType{v1alpha1.DatameshMemberTypeDiskful, v1alpha1.DatameshMemberTypeLiminalDiskful, v1alpha1.DatameshMemberTypeAccess, v1alpha1.DatameshMemberTypeShadowDiskful, v1alpha1.DatameshMemberTypeLiminalShadowDiskful, v1alpha1.DatameshMemberTypeTieBreaker} {
		t.Run(string(memberType), func(t *testing.T) {
			for i := range rv.Status.Datamesh.Members {
				rv.Status.Datamesh.Members[i].Type = memberType
			}
			actual := computeActualEvacuation(rv, rvrs[:2], &rspView{}, states, now)
			want := 0
			if memberType == v1alpha1.DatameshMemberTypeDiskful || memberType == v1alpha1.DatameshMemberTypeLiminalDiskful || memberType == v1alpha1.DatameshMemberTypeTieBreaker {
				want = 2
			}
			if len(actual.evacuees) != want {
				t.Fatalf("got %v, want %d evacuees", actual.evacuees, want)
			}
			if _, orphan := actual.evacuees["rv-1-2"]; orphan {
				t.Fatal("orphan must use the existing path")
			}
		})
	}
}

var _ = Describe("evacuation placement report after removal", func() {
	DescribeTable("reports removed names until membership and UpToDate copies are restored", func(
		ctx SpecContext, tieBreaker, deleting, missing, restored, synchronized, manual bool,
	) {
		f := newEvacuationFixture(3, 1)
		if tieBreaker {
			f = newTieBreakerEvacuationFixture()
		}
		f.rv.Status.Datamesh.Members = f.rv.Status.Datamesh.Members[:2]
		f.rv.Status.EffectiveLayout.UpToDateVoters = 2
		f.rv.Status.EvacuatedReplicas = []string{"rv-1-2"}
		if deleting {
			f.rvrs[2].DeletionTimestamp = new(metav1.Now())
		}
		if missing {
			f.rvrs = f.rvrs[:2]
		}
		if restored {
			memberType := v1alpha1.DatameshMemberTypeLiminalDiskful
			if tieBreaker {
				memberType = v1alpha1.DatameshMemberTypeTieBreaker
			}
			f.rv.Status.Datamesh.Members = append(f.rv.Status.Datamesh.Members, v1alpha1.DatameshMember{
				Name: "rv-1-3", NodeName: "node-3", Type: memberType,
			})
		}
		if synchronized && !tieBreaker {
			f.rv.Status.EffectiveLayout.UpToDateVoters = 3
		}
		if manual {
			f.rsc.Spec.EligibleNodesConflictResolutionStrategy = &v1alpha1.ReplicatedStorageClassEligibleNodesConflictResolutionStrategy{Type: v1alpha1.EligibleNodesConflictResolutionManual}
		}
		rec := f.start()
		rsp, err := rec.getRSP(ctx, "test-pool", f.rvrs, nil)
		Expect(err).NotTo(HaveOccurred())
		var evacuation evacuationView
		Expect(rec.reconcileEvacuationView(ctx, f.rv, f.rvrs, f.rsc, rsp, &evacuation).Error()).NotTo(HaveOccurred())
		report := computeSatisfyEligibleNodesReport(f.rv, f.rvrs, rsp, evacuation)
		Expect(report.violations).To(BeEmpty())
		if restored && (tieBreaker || synchronized) {
			Expect(report.status).To(Equal(metav1.ConditionTrue))
			Expect(report.reason).To(Equal(v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonSatisfyEligibleNodes))
		} else {
			Expect(report.status).To(Equal(metav1.ConditionFalse))
			Expect(report.reason).To(Equal(v1alpha1.ReplicatedVolumeCondSatisfyEligibleNodesReasonConflictResolutionInProgress))
			Expect(report.message).To(And(ContainSubstring("rv-1-2"), ContainSubstring("have "), ContainSubstring("want "), ContainSubstring("UpToDate voters")))
		}
	},
		Entry("D not yet deleted", false, false, false, false, false, false),
		Entry("D Terminating", false, true, false, false, false, false),
		Entry("D already gone", false, false, true, false, false, false),
		Entry("D joined but not synchronized", false, false, true, true, false, false),
		Entry("D synchronized", false, false, true, true, true, false),
		Entry("D Manual after evacuation", false, true, false, false, false, true),
		Entry("TB not yet deleted", true, false, false, false, true, false),
		Entry("TB Terminating", true, true, false, false, true, false),
		Entry("TB already gone", true, false, true, false, true, false),
		Entry("TB restored", true, true, false, true, true, false),
		Entry("TB Manual after evacuation", true, true, false, false, true, true),
	)

	It("does not evacuate during formation", func(ctx SpecContext) {
		f := newEvacuationFixture(3, 1)
		f.rv.Status.DatameshRevision = 0
		rec := f.start()
		_, err := rec.Reconcile(ctx, RequestFor(f.rv))
		Expect(err).NotTo(HaveOccurred())
		updated, err := rec.getRV(ctx, f.rv.Name)
		Expect(err).NotTo(HaveOccurred())
		for _, transition := range updated.Status.DatameshTransitions {
			Expect(transition.Type).NotTo(Equal(v1alpha1.ReplicatedVolumeDatameshTransitionTypeForceRemoveReplica))
		}
		// Formation may restart its own replicas when placement is no longer valid;
		// that existing cleanup is separate from automatic ForceRemove evacuation.
		Expect(updated.Status.DatameshTransitions).To(ContainElement(
			HaveField("Type", v1alpha1.ReplicatedVolumeDatameshTransitionTypeFormation)))
	})
})

func TestComputeActualEvacuationGraceExpiration(t *testing.T) {
	now := time.Date(2026, time.November, 12, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name        string
		memberType  v1alpha1.DatameshMemberType
		state       *nodegrace.ReadyState
		eligible    bool
		wantExpiry  time.Time
		wantEvacuee bool
	}{
		{name: "Diskful within grace", memberType: v1alpha1.DatameshMemberTypeDiskful, state: &nodegrace.ReadyState{HasCondition: true, LastTransitionTime: now.Add(-20 * time.Second)}, wantExpiry: now.Add(40 * time.Second)},
		{name: "TieBreaker within grace", memberType: v1alpha1.DatameshMemberTypeTieBreaker, state: &nodegrace.ReadyState{HasCondition: true, LastTransitionTime: now.Add(-20 * time.Second)}, wantExpiry: now.Add(40 * time.Second)},
		{name: "exact deadline", memberType: v1alpha1.DatameshMemberTypeDiskful, state: &nodegrace.ReadyState{HasCondition: true, LastTransitionTime: now.Add(-time.Minute)}, wantExpiry: now},
		{name: "expired", memberType: v1alpha1.DatameshMemberTypeDiskful, state: &nodegrace.ReadyState{HasCondition: true, LastTransitionTime: now.Add(-2 * time.Minute)}, wantEvacuee: true},
		{name: "missing Node", memberType: v1alpha1.DatameshMemberTypeDiskful, wantEvacuee: true},
		{name: "no Ready condition", memberType: v1alpha1.DatameshMemberTypeDiskful, state: &nodegrace.ReadyState{}},
		{name: "Ready", memberType: v1alpha1.DatameshMemberTypeDiskful, state: &nodegrace.ReadyState{HasCondition: true, Status: true}},
		{name: "eligible", memberType: v1alpha1.DatameshMemberTypeDiskful, state: &nodegrace.ReadyState{HasCondition: true, LastTransitionTime: now}, eligible: true},
		{name: "Access", memberType: v1alpha1.DatameshMemberTypeAccess, state: &nodegrace.ReadyState{HasCondition: true, LastTransitionTime: now}},
		{name: "ShadowDiskful", memberType: v1alpha1.DatameshMemberTypeShadowDiskful, state: &nodegrace.ReadyState{HasCondition: true, LastTransitionTime: now}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rv, rvrs := convergenceFixture(1, 1, 0)
			rv.Status.Datamesh.Members[0].Type = tt.memberType
			rsp := &rspView{NotReadyGracePeriod: time.Minute}
			if tt.eligible {
				rsp.EligibleNodes = []v1alpha1.ReplicatedStoragePoolEligibleNode{{NodeName: "node-0"}}
			}
			actual := computeActualEvacuation(rv, rvrs, rsp, map[string]*nodegrace.ReadyState{"node-0": tt.state}, now)
			if !actual.graceExpiresAt.Equal(tt.wantExpiry) {
				t.Fatalf("expiration = %v, want %v", actual.graceExpiresAt, tt.wantExpiry)
			}
			_, evacuated := actual.evacuees["rv-1-0"]
			if evacuated != tt.wantEvacuee {
				t.Fatalf("evacuee = %t, want %t", evacuated, tt.wantEvacuee)
			}
		})
	}
	// Different nodes and map order must not affect the minimum deadline.
	rv, rvrs := convergenceFixture(1, 3, 0)
	rsp := &rspView{NotReadyGracePeriod: time.Minute}
	states := map[string]*nodegrace.ReadyState{
		"node-0": {HasCondition: true, LastTransitionTime: now.Add(-10 * time.Second)},
		"node-1": {HasCondition: true, LastTransitionTime: now.Add(-30 * time.Second)},
		"node-2": {HasCondition: true, LastTransitionTime: now.Add(-20 * time.Second)},
	}
	actual := computeActualEvacuation(rv, rvrs, rsp, states, now)
	if !actual.graceExpiresAt.Equal(now.Add(30 * time.Second)) {
		t.Fatalf("earliest expiration = %v", actual.graceExpiresAt)
	}
}

func TestReconcileEvacuationViewManual(t *testing.T) {
	rv, rvrs := convergenceFixture(1, 1, 0)
	rsc := newRSCWithConfiguration("rsc")
	rsc.Spec.EligibleNodesConflictResolutionStrategy = &v1alpha1.ReplicatedStorageClassEligibleNodesConflictResolutionStrategy{Type: v1alpha1.EligibleNodesConflictResolutionManual}
	rsp := &rspView{NotReadyGracePeriod: time.Minute}
	node := &corev1.Node{}
	node.Name = "node-0"
	node.Status.Conditions = []corev1.NodeCondition{{
		Type: corev1.NodeReady, Status: corev1.ConditionFalse, LastTransitionTime: metav1.Now(),
	}}
	scheme := newScheme()
	cl := newClientBuilder(scheme).WithObjects(node).Build()
	var actual evacuationView
	outcome := NewReconciler(cl, scheme).reconcileEvacuationView(t.Context(), rv, rvrs, rsc, rsp, &actual)
	result, err := outcome.ToCtrl()
	if err != nil || result.RequeueAfter != 0 {
		t.Fatalf("result = %v, err = %v; want no requeue", result, err)
	}
	if len(actual.evacuees) != 0 || !actual.graceExpiresAt.IsZero() {
		t.Fatalf("Manual strategy must produce an empty evacuation view: %+v", actual)
	}
}

func TestReconcileEvacuationViewReadsOnlyCandidateNodes(t *testing.T) {
	rv, rvrs := convergenceFixture(1, 3, 0)
	rv.Status.Datamesh.Members = rv.Status.Datamesh.Members[:2]
	rsp := &rspView{EligibleNodes: []v1alpha1.ReplicatedStoragePoolEligibleNode{{NodeName: "node-0"}}}
	var reads []string
	scheme := newScheme()
	cl := newClientBuilder(scheme).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			reads = append(reads, key.Name)
			return cl.Get(ctx, key, obj, opts...)
		},
	}).Build()
	var actual evacuationView
	outcome := NewReconciler(cl, scheme).reconcileEvacuationView(t.Context(), rv, rvrs, newRSCWithConfiguration("rsc"), rsp, &actual)
	if outcome.Error() != nil || !slices.Equal(reads, []string{"node-1"}) {
		t.Fatalf("reads = %v, err = %v", reads, outcome.Error())
	}
}

func TestComputeTargetEvacuatedReplicaNamesIgnoresExistingForceRemove(t *testing.T) {
	rv := &v1alpha1.ReplicatedVolume{}
	transition := v1alpha1.ReplicatedVolumeDatameshTransition{
		Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeForceRemoveReplica, ReplicaName: "old-manual-removal",
	}
	// A manual request can disappear while its transition is still active. The
	// replica becoming an evacuee later must not relabel that existing transition.
	rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{transition}
	names := computeTargetEvacuatedReplicaNames(rv, nil, map[string]struct{}{transition.ReplicaName: {}}, nil, []v1alpha1.ReplicatedVolumeDatameshTransition{transition})
	if len(names) != 0 {
		t.Fatalf("existing manual removal marked: %v", names)
	}
}

func TestReconcileEvacuationGraceMinimumDelay(t *testing.T) {
	rv, rvrs := convergenceFixture(1, 1, 0)
	scheme := newScheme()
	cl := newClientBuilder(scheme).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(_ context.Context, _ client.WithWatch, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
			// Return the subsecond timestamp directly, without metav1 JSON truncation.
			node := obj.(*corev1.Node)
			node.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionFalse, LastTransitionTime: metav1.Now()}}
			return nil
		},
	}).Build()
	var actual evacuationView
	outcome := NewReconciler(cl, scheme).reconcileEvacuationView(t.Context(), rv, rvrs, newRSCWithConfiguration("rsc"), &rspView{NotReadyGracePeriod: 500 * time.Millisecond}, &actual)
	result, err := outcome.ToCtrl()
	if err != nil || result.RequeueAfter != time.Second {
		t.Fatalf("result = %v, err = %v; want one-second requeue", result, err)
	}
}

func TestComputeTargetEvacuatedReplicaNamesRetention(t *testing.T) {
	for _, tt := range []struct {
		name                                               string
		member, evacuee, present, deleting, synced, marked bool
		want                                               bool
	}{
		{name: "absent RVR while syncing", want: true},
		{name: "absent RVR after sync", synced: true},
		{name: "deleting RVR while syncing", present: true, deleting: true, want: true},
		{name: "deleting RVR after sync needs cleanup decision", present: true, deleting: true, synced: true, want: true},
		{name: "deleting marked RVR after sync", present: true, deleting: true, synced: true, marked: true},
		{name: "live RVR still needs retirement", present: true, synced: true, want: true},
		{name: "member still an evacuee", member: true, evacuee: true, present: true, synced: true, want: true},
		{name: "member recovered before removal persisted", member: true, present: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rv, rvrs := convergenceFixture(1, 3, 0)
			name := "rv-1-3"
			if tt.member {
				name = rvrs[2].Name
			}
			rv.Status.EvacuatedReplicas = []string{name}
			rv.Status.EffectiveLayout.UpToDateVoters = 2
			if tt.synced {
				rv.Status.EffectiveLayout.UpToDateVoters = 3
			}
			var oldReplicas []*v1alpha1.ReplicatedVolumeReplica
			if tt.present {
				old := &v1alpha1.ReplicatedVolumeReplica{ObjectMeta: metav1.ObjectMeta{Name: name}, Status: v1alpha1.ReplicatedVolumeReplicaStatus{Evacuated: new(tt.marked)}}
				if tt.deleting {
					old.DeletionTimestamp = new(metav1.Now())
				}
				oldReplicas = append(oldReplicas, old)
			}
			evacuees := make(map[string]struct{})
			if tt.evacuee {
				evacuees[name] = struct{}{}
			}
			before := rv.DeepCopy()
			got := computeTargetEvacuatedReplicaNames(rv, oldReplicas, evacuees, nil, nil)
			var want []string
			if tt.want {
				want = []string{name}
			}
			if !slices.Equal(got, want) {
				t.Fatalf("names = %v, want %v", got, want)
			}
			if !reflect.DeepEqual(rv, before) {
				t.Fatal("status computation mutated input")
			}
		})
	}
}

func TestComputeEvacuatedReplicaRemovalPending(t *testing.T) {
	for _, tt := range []struct {
		name                                                    string
		marked, wasMember, member, present, deleting, evacuated bool
		want                                                    bool
	}{
		{name: "new removal", marked: true, wasMember: true, present: true, want: true},
		{name: "removal already persisted", marked: true, present: true},
		{name: "still a member", marked: true, wasMember: true, member: true, present: true},
		{name: "RVR already gone", marked: true, wasMember: true},
		{name: "unmarked removal", wasMember: true, present: true},
		{name: "deleting RVR", marked: true, wasMember: true, present: true, deleting: true, want: true},
		{name: "deleting evacuated RVR", marked: true, wasMember: true, present: true, deleting: true, evacuated: true, want: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rv := &v1alpha1.ReplicatedVolume{ObjectMeta: metav1.ObjectMeta{Name: "rv-1"}}
			member := v1alpha1.DatameshMember{Name: "rv-1-31"}
			if tt.wasMember {
				rv.Status.Datamesh.Members = []v1alpha1.DatameshMember{member}
			}
			membersAtStart := idset.FromAll(rv.Status.Datamesh.Members)
			rv.Status.Datamesh.Members = nil
			if tt.member {
				rv.Status.Datamesh.Members = []v1alpha1.DatameshMember{member}
			}
			var targetNames []string
			if tt.marked {
				targetNames = []string{member.Name}
			}
			var rvrs []*v1alpha1.ReplicatedVolumeReplica
			if tt.present {
				rvr := &v1alpha1.ReplicatedVolumeReplica{ObjectMeta: metav1.ObjectMeta{Name: member.Name}}
				if tt.deleting {
					rvr.DeletionTimestamp = new(metav1.Now())
				}
				if tt.evacuated {
					rvr.Status.Evacuated = new(true)
				}
				rvrs = append(rvrs, rvr)
			}
			if got := computeEvacuatedReplicaRemovalPending(rv, rvrs, targetNames, membersAtStart); got != tt.want {
				t.Fatalf("pending removal = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestComputeTargetEvacuatedReplicaNamesMerge(t *testing.T) {
	rv, _ := convergenceFixture(1, 2, 0)
	rv.Status.EvacuatedReplicas = []string{"rv-1-4"}
	rv.Status.DatameshTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{{
		Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeForceRemoveReplica, ReplicaName: "rv-1-3",
	}}
	completed := []v1alpha1.ReplicatedVolumeDatameshTransition{{
		Type: v1alpha1.ReplicatedVolumeDatameshTransitionTypeForceRemoveReplica, ReplicaName: "rv-1-2",
	}, rv.Status.DatameshTransitions[0]}
	names := computeTargetEvacuatedReplicaNames(rv, nil, map[string]struct{}{"rv-1-3": {}, "rv-1-2": {}}, completed, nil)
	if want := []string{"rv-1-2", "rv-1-3", "rv-1-4"}; !slices.Equal(names, want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
}

func TestNewRVREvacuatedNames(t *testing.T) {
	invalid := []string{"", "x", "rv-1", "other-1", "rv-1-", "rv-1--1", "rv-1-32", "rv-1-256", "rv-1-999999999999999999999", "rv-1-01", "rv-1-+1", "rv-1-1,", " rv-1-1", "rv-1-1 "}
	for _, tt := range []struct {
		name   string
		marked []string
		want   string
	}{
		{name: "empty", want: "rv-1-1"},
		{name: "invalid", marked: invalid, want: "rv-1-1"},
		{name: "valid and invalid", marked: slices.Concat(invalid, []string{"rv-1-31", "rv-1-1", "rv-1-0", "rv-1-1"}), want: "rv-1-2"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rv, rvrs := convergenceFixture(1, 1, 0)
			rv.Status.EvacuatedReplicas = tt.marked
			before := rv.DeepCopy()
			rvr, err := newRVR(rv, rvrs, v1alpha1.ReplicaTypeDiskful, "")
			if err != nil {
				t.Fatal(err)
			}
			if rvr.Name != tt.want {
				t.Fatalf("name = %q, want %q", rvr.Name, tt.want)
			}
			if !reflect.DeepEqual(rv, before) {
				t.Fatal("newRVR mutated RV status")
			}
		})
	}
	// Every valid ID, including the upper boundary, must stay reserved even without an RVR.
	rv, _ := convergenceFixture(1, 0, 0)
	for id := range 32 {
		rv.Status.EvacuatedReplicas = append(rv.Status.EvacuatedReplicas, fmt.Sprintf("rv-1-%d", id))
	}
	if rvr, err := newRVR(rv, nil, v1alpha1.ReplicaTypeDiskful, ""); err == nil || rvr != nil {
		t.Fatalf("all IDs reserved: rvr = %v, err = %v", rvr, err)
	}
	rv.Status.EvacuatedReplicas = rv.Status.EvacuatedReplicas[:31]
	rvr, err := newRVR(rv, nil, v1alpha1.ReplicaTypeDiskful, "")
	if err != nil || rvr.Name != "rv-1-31" {
		t.Fatalf("last free ID: rvr = %v, err = %v", rvr, err)
	}
}

func TestComputeActualEvacuatedReplicaNames(t *testing.T) {
	rv, _ := convergenceFixture(1, 0, 0)
	rv.Status.EvacuatedReplicas = []string{"rv-1-31", "", "x", "other-1", "rv-1-32", "rv-1-01", "rv-1-0", "rv-1-31"}
	before := rv.DeepCopy()
	names := computeActualEvacuatedReplicaNames(rv)
	if want := []string{"rv-1-0", "rv-1-31"}; !slices.Equal(names, want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	names[0] = "changed"
	if !reflect.DeepEqual(rv, before) {
		t.Fatal("name normalization aliases or mutates RV status")
	}
}

var _ = Describe("evacuation marker across lifecycle paths", func() {
	It("preserves the marker during maintenance and resumes retirement afterward", func(ctx SpecContext) {
		f := newEvacuationFixture(3, 1)
		f.rv.Status.Datamesh.Members = f.rv.Status.Datamesh.Members[:2]
		f.rv.Status.EvacuatedReplicas = []string{"rv-1-2"}
		f.rv.Spec.Maintenance = v1alpha1.MaintenanceModeNoResourceReconciliation
		rec := f.start()
		updated, _ := f.run(ctx, rec)
		Expect(updated.Status.EvacuatedReplicas).To(Equal([]string{"rv-1-2"}))
		var old v1alpha1.ReplicatedVolumeReplica
		Expect(rec.cl.Get(ctx, client.ObjectKey{Name: "rv-1-2"}, &old)).To(Succeed())
		Expect(old.DeletionTimestamp).To(BeNil())
		updated.Spec.Maintenance = ""
		Expect(rec.cl.Update(ctx, updated)).To(Succeed())
		updated, _ = f.run(ctx, rec)
		Expect(updated.Status.EvacuatedReplicas).To(Equal([]string{"rv-1-2"}))
		Expect(rec.cl.Get(ctx, client.ObjectKey{Name: old.Name}, &old)).To(Succeed())
		Expect(old.DeletionTimestamp).NotTo(BeNil())
	})

	It("does not block volume deletion with a pending evacuation marker", func(ctx SpecContext) {
		f := newEvacuationFixture(3, 1)
		f.rv.Status.EvacuatedReplicas = []string{"rv-1-2"}
		obju.AddFinalizer(f.rv, v1alpha1.RVControllerFinalizer)
		// Model children whose backing-resource cleanup no longer needs an agent.
		for _, rvr := range f.rvrs {
			obju.RemoveFinalizer(rvr, v1alpha1.RVRControllerFinalizer)
		}
		rec := f.start()
		Expect(rec.cl.Delete(ctx, f.rv)).To(Succeed())
		for range 3 {
			_, err := rec.Reconcile(ctx, RequestFor(f.rv))
			Expect(err).NotTo(HaveOccurred())
		}
		err := rec.cl.Get(ctx, client.ObjectKeyFromObject(f.rv), &v1alpha1.ReplicatedVolume{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
		rvrs, err := rec.getRVRsSorted(ctx, f.rv.Name)
		Expect(err).NotTo(HaveOccurred())
		Expect(rvrs).To(BeEmpty())
	})
})

var _ = Describe("persisted evacuation cleanup authorization", func() {
	DescribeTable("records the decision before deletion, including resumed cleanup", func(ctx SpecContext, deleting, deletingVolume bool) {
		f := newEvacuationFixture(3, 1)
		f.rv.Status.Datamesh.Members = f.rv.Status.Datamesh.Members[:2]
		f.rv.Status.EvacuatedReplicas = []string{"rv-1-2"}
		// A returning node and Manual strategy do not revoke the persisted decision.
		f.nodes[2].Status.Conditions[0].Status = corev1.ConditionTrue
		f.rsc.Spec.EligibleNodesConflictResolutionStrategy = &v1alpha1.ReplicatedStorageClassEligibleNodesConflictResolutionStrategy{Type: v1alpha1.EligibleNodesConflictResolutionManual}
		if deleting {
			f.rvrs[2].DeletionTimestamp = new(metav1.Now())
		}
		if deletingVolume {
			obju.AddFinalizer(f.rv, v1alpha1.RVControllerFinalizer)
		}
		rec := f.start()
		if deletingVolume {
			Expect(rec.cl.Delete(ctx, f.rv)).To(Succeed())
		}
		f.run(ctx, rec)
		var old v1alpha1.ReplicatedVolumeReplica
		Expect(rec.cl.Get(ctx, client.ObjectKey{Name: "rv-1-2"}, &old)).To(Succeed())
		Expect(old.Status.Evacuated).NotTo(BeNil())
		Expect(*old.Status.Evacuated).To(BeTrue())
		Expect(old.DeletionTimestamp).NotTo(BeNil())
	}, Entry("normal", false, false), Entry("already terminating", true, false), Entry("volume deleting", false, true))

	It("retries a failed status write before Delete and preserves authorization across a failed Delete", func(ctx SpecContext) {
		f := newEvacuationFixture(3, 1)
		f.rv.Status.Datamesh.Members = f.rv.Status.Datamesh.Members[:2]
		f.rv.Status.EvacuatedReplicas = []string{"rv-1-2"}
		rec := f.start()
		failStatus, failDelete := true, true
		failure := errors.New("injected write failure")
		deletes := 0
		rec.cl = interceptor.NewClient(rec.cl.(client.WithWatch), interceptor.Funcs{
			SubResourcePatch: func(ctx context.Context, cl client.Client, sub string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				if _, ok := obj.(*v1alpha1.ReplicatedVolumeReplica); ok && failStatus {
					return failure
				}
				return cl.SubResource(sub).Patch(ctx, obj, patch, opts...)
			},
			Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
				if obj.GetName() == "rv-1-2" {
					deletes++
					var persisted v1alpha1.ReplicatedVolumeReplica
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(obj), &persisted)).To(Succeed())
					Expect(persisted.Status.Evacuated).NotTo(BeNil())
					Expect(*persisted.Status.Evacuated).To(BeTrue())
					if failDelete {
						return failure
					}
				}
				return cl.Delete(ctx, obj, opts...)
			},
		})
		_, err := rec.Reconcile(ctx, RequestFor(f.rv))
		Expect(errors.Is(err, failure)).To(BeTrue())
		Expect(deletes).To(BeZero())
		failStatus = false
		_, err = rec.Reconcile(ctx, RequestFor(f.rv))
		Expect(errors.Is(err, failure)).To(BeTrue())
		Expect(deletes).To(Equal(1))
		failDelete = false
		// A fresh reconciler resumes from the persisted per-replica status.
		rec = NewReconciler(rec.cl, rec.scheme)
		f.run(ctx, rec)
		Expect(deletes).To(Equal(2))
	})
})
