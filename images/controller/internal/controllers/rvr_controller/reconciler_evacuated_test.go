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
	"errors"
	"testing"

	"github.com/go-logr/logr"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes/testhelpers"
)

const nodeConfiguratorFinalizer = "storage.deckhouse.io/sds-node-configurator"

func rvrControllerRef(rvr *v1alpha1.ReplicatedVolumeReplica) metav1.OwnerReference {
	return *metav1.NewControllerRef(rvr, v1alpha1.SchemeGroupVersion.WithKind("ReplicatedVolumeReplica"))
}

func TestEvacuatedReplicaDeletion(t *testing.T) {
	for _, tc := range []struct {
		name                                       string
		evacuated, returned, foreignDR, foreignLLV bool
	}{
		{name: "lost node", evacuated: true},
		{name: "returned node", evacuated: true, returned: true},
		{name: "ordinary deletion still waits for node"},
		{name: "unknown child finalizers remain", evacuated: true, foreignDR: true, foreignLLV: true},
		{name: "foreign LLV finalizers do not hold evacuated RVR", evacuated: true, foreignLLV: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			scheme := runtime.NewScheme()
			g.Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
			g.Expect(snc.AddToScheme(scheme)).To(Succeed())
			g.Expect(corev1.AddToScheme(scheme)).To(Succeed())
			rvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1-0", UID: "old-replica", Finalizers: []string{v1alpha1.RVRControllerFinalizer}, DeletionTimestamp: new(metav1.Now())},
				Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{ReplicatedVolumeName: "rv-1", NodeName: "lost-node", Type: v1alpha1.ReplicaTypeDiskful},
				Status:     v1alpha1.ReplicatedVolumeReplicaStatus{Evacuated: new(tc.evacuated)},
			}
			owner := rvrControllerRef(rvr)
			dr := &v1alpha1.DRBDResource{
				ObjectMeta: metav1.ObjectMeta{Name: rvr.Name, OwnerReferences: []metav1.OwnerReference{owner}, Finalizers: []string{v1alpha1.RVRControllerFinalizer, v1alpha1.AgentFinalizer}},
				Spec:       v1alpha1.DRBDResourceSpec{NodeName: rvr.Spec.NodeName},
			}
			llv := &snc.LVMLogicalVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "old-disk", OwnerReferences: []metav1.OwnerReference{owner}, Finalizers: []string{v1alpha1.RVRControllerFinalizer, v1alpha1.AgentFinalizer, nodeConfiguratorFinalizer}},
			}
			if tc.foreignDR {
				dr.Finalizers = append(dr.Finalizers, "example.test/hold")
			}
			if tc.foreignLLV {
				llv.Finalizers = append(llv.Finalizers, "example.test/hold")
			}
			objects := []client.Object{rvr, dr, llv}
			if tc.returned {
				objects = append(objects, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: rvr.Spec.NodeName}, Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}}})
			}
			cl := testhelpers.WithLLVByRVROwnerIndex(fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithStatusSubresource(rvr, dr, llv)).Build()
			// No RV evacuation marker is present: the per-replica decision survives it.
			rec := NewReconciler(cl, scheme, logr.Discard(), "test")
			for range 4 {
				_, err := rec.Reconcile(t.Context(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(rvr)})
				g.Expect(err).NotTo(HaveOccurred())
			}
			for _, obj := range []client.Object{rvr, dr} {
				err := cl.Get(t.Context(), client.ObjectKeyFromObject(obj), obj)
				if tc.evacuated && !tc.foreignDR {
					g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "%T remains", obj)
				} else {
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(obj.GetDeletionTimestamp()).NotTo(BeNil())
				}
			}
			g.Expect(cl.Get(t.Context(), client.ObjectKeyFromObject(llv), llv)).To(Succeed())
			g.Expect(llv.DeletionTimestamp).NotTo(BeNil())
			expectedLLVFinalizers := []string{nodeConfiguratorFinalizer}
			if !tc.evacuated {
				expectedLLVFinalizers = append(expectedLLVFinalizers, v1alpha1.AgentFinalizer)
			}
			if tc.foreignLLV {
				expectedLLVFinalizers = append(expectedLLVFinalizers, "example.test/hold")
			}
			g.Expect(llv.Finalizers).To(ConsistOf(expectedLLVFinalizers))
			if tc.foreignDR {
				g.Expect(dr.Finalizers).To(Equal([]string{"example.test/hold"}))
				g.Expect(rvr.Finalizers).To(ContainElement(v1alpha1.RVRControllerFinalizer))
			}
		})
	}
}

func TestEvacuatedResourceGuardsAndRetry(t *testing.T) {
	for _, kind := range []string{"DRBDResource", "LVMLogicalVolume"} {
		cases := []string{"unmarked", "not deleting", "RV finalizer", "foreign RVR finalizer", "member", "maintenance", "foreign child UID", "child not deleting", "child controller finalizer", "only foreign finalizers", "patch conflict", "already gone", "released"}
		if kind == "DRBDResource" {
			cases = append(cases, "different node")
		}
		for _, tc := range cases {
			t.Run(kind+"/"+tc, func(t *testing.T) {
				g := NewWithT(t)
				rvr := &v1alpha1.ReplicatedVolumeReplica{
					ObjectMeta: metav1.ObjectMeta{Name: "rv-1-0", UID: "old", Finalizers: []string{v1alpha1.RVRControllerFinalizer}, DeletionTimestamp: new(metav1.Now())},
					Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "lost-node"},
					Status:     v1alpha1.ReplicatedVolumeReplicaStatus{Evacuated: new(true)},
				}
				rv := &v1alpha1.ReplicatedVolume{}
				dr := &v1alpha1.DRBDResource{Spec: v1alpha1.DRBDResourceSpec{NodeName: rvr.Spec.NodeName}}
				var child client.Object = dr
				var llvs []snc.LVMLogicalVolume
				var foreignFinalizers []string
				resource := v1alpha1.SchemeGroupVersion.WithResource("drbdresources").GroupResource()
				if kind == "LVMLogicalVolume" {
					dr = nil
					llvs = []snc.LVMLogicalVolume{{}}
					child = &llvs[0]
					foreignFinalizers = []string{nodeConfiguratorFinalizer, "example.test/hold"}
					resource = snc.SchemeGroupVersion.WithResource("lvmlogicalvolumes").GroupResource()
				}
				child.SetName(rvr.Name)
				child.SetOwnerReferences([]metav1.OwnerReference{rvrControllerRef(rvr)})
				child.SetDeletionTimestamp(new(metav1.Now()))
				child.SetFinalizers(append([]string{v1alpha1.AgentFinalizer}, foreignFinalizers...))
				switch tc {
				case "unmarked":
					rvr.Status.Evacuated = nil
				case "not deleting":
					rvr.DeletionTimestamp = nil
				case "RV finalizer":
					rvr.Finalizers = append(rvr.Finalizers, v1alpha1.RVControllerFinalizer)
				case "foreign RVR finalizer":
					rvr.Finalizers = append(rvr.Finalizers, "example.test/hold")
				case "member":
					rv.Status.Datamesh.Members = []v1alpha1.DatameshMember{{Name: rvr.Name}}
				case "maintenance":
					rv.Spec.Maintenance = v1alpha1.MaintenanceModeNoResourceReconciliation
				case "foreign child UID":
					child.GetOwnerReferences()[0].UID = "other"
				case "different node":
					dr.Spec.NodeName = "other"
				case "child not deleting":
					child.SetDeletionTimestamp(nil)
				case "child controller finalizer":
					obju.AddFinalizer(child, v1alpha1.RVRControllerFinalizer)
				case "only foreign finalizers":
					obju.RemoveFinalizer(child, v1alpha1.AgentFinalizer)
				}
				patches := 0
				failure := errors.New("conflict")
				cl := fake.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
					Patch: func(_ context.Context, _ client.WithWatch, obj client.Object, _ client.Patch, _ ...client.PatchOption) error {
						patches++
						g.Expect(obj.GetFinalizers()).To(ConsistOf(foreignFinalizers))
						if tc == "released" {
							return nil
						}
						if tc == "already gone" {
							return apierrors.NewNotFound(resource, obj.GetName())
						}
						return failure
					},
				}).Build()
				rec := NewReconciler(cl, nil, logr.Discard(), "test")
				outcome := rec.reconcileEvacuatedResources(t.Context(), rvr, rv, dr, llvs)
				if tc == "patch conflict" {
					g.Expect(errors.Is(outcome.Error(), failure)).To(BeTrue())
					g.Expect(patches).To(Equal(1))
				} else {
					g.Expect(outcome.Error()).NotTo(HaveOccurred())
					if tc == "already gone" || tc == "released" {
						g.Expect(patches).To(Equal(1))
						g.Expect(outcome.ShouldReturn()).To(BeTrue())
						result, err := outcome.ToCtrl()
						g.Expect(err).NotTo(HaveOccurred())
						g.Expect(result).To(Equal(reconcile.Result{}))
					} else {
						g.Expect(patches).To(BeZero())
					}
				}
			})
		}
	}
}

func TestEvacuatedReplicaMetadataWaitsForChildren(t *testing.T) {
	for _, tc := range []struct {
		name string
		wait bool
	}{
		{name: "only foreign LLV finalizers"},
		{name: "unmarked replica", wait: true},
		{name: "LLV not deleting", wait: true},
		{name: "LLV agent finalizer", wait: true},
		{name: "LLV controller finalizer", wait: true},
		{name: "another LLV still held", wait: true},
		{name: "foreign child UID", wait: true},
		{name: "DRBDResource still exists", wait: true},
		{name: "datamesh member", wait: true},
		{name: "RV finalizer", wait: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			scheme := runtime.NewScheme()
			g.Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
			rvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1-0", UID: "old", Finalizers: []string{v1alpha1.RVRControllerFinalizer}, DeletionTimestamp: new(metav1.Now())},
				Status:     v1alpha1.ReplicatedVolumeReplicaStatus{Evacuated: new(true)},
			}
			llvs := []snc.LVMLogicalVolume{{ObjectMeta: metav1.ObjectMeta{
				Name: "old-disk", DeletionTimestamp: new(metav1.Now()),
				OwnerReferences: []metav1.OwnerReference{rvrControllerRef(rvr)},
				Finalizers:      []string{nodeConfiguratorFinalizer, "example.test/hold"},
			}}}
			var dr *v1alpha1.DRBDResource
			var rv *v1alpha1.ReplicatedVolume
			switch tc.name {
			case "unmarked replica":
				rvr.Status.Evacuated = nil
			case "LLV not deleting":
				llvs[0].DeletionTimestamp = nil
			case "LLV agent finalizer":
				llvs[0].Finalizers = append(llvs[0].Finalizers, v1alpha1.AgentFinalizer)
			case "LLV controller finalizer":
				llvs[0].Finalizers = append(llvs[0].Finalizers, v1alpha1.RVRControllerFinalizer)
			case "another LLV still held":
				llvs = append(llvs, snc.LVMLogicalVolume{ObjectMeta: metav1.ObjectMeta{Name: "other-disk"}})
			case "foreign child UID":
				llvs[0].OwnerReferences[0].UID = "other"
			case "DRBDResource still exists":
				dr = &v1alpha1.DRBDResource{ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: new(metav1.Now()), Finalizers: []string{"example.test/hold"}}}
			case "datamesh member":
				rv = &v1alpha1.ReplicatedVolume{Status: v1alpha1.ReplicatedVolumeStatus{Datamesh: v1alpha1.ReplicatedVolumeDatamesh{Members: []v1alpha1.DatameshMember{{Name: rvr.Name}}}}}
			case "RV finalizer":
				rvr.Finalizers = append(rvr.Finalizers, v1alpha1.RVControllerFinalizer)
			}
			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(rvr).Build()
			rec := NewReconciler(cl, scheme, logr.Discard(), "test")
			outcome := rec.reconcileMetadata(t.Context(), rvr, rv, llvs, dr)
			g.Expect(outcome.Error()).NotTo(HaveOccurred())
			err := cl.Get(t.Context(), client.ObjectKeyFromObject(rvr), rvr)
			if tc.wait {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(rvr.Finalizers).To(ContainElement(v1alpha1.RVRControllerFinalizer))
			} else {
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
			}
		})
	}
}

func TestEvacuationDecisionTriggersReplicaReconcile(t *testing.T) {
	old := &v1alpha1.ReplicatedVolumeReplica{}
	updated := old.DeepCopy()
	updated.Status.Evacuated = new(true)
	for _, pred := range rvrPredicates() {
		if !pred.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: updated}) {
			t.Fatal("evacuation status change was filtered out")
		}
	}
}
