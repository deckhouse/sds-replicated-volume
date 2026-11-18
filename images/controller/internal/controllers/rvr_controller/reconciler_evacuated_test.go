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
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/indexes/testhelpers"
)

func TestEvacuatedReplicaDeletion(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		evacuated, returned, foreign bool
	}{
		{name: "lost node", evacuated: true},
		{name: "returned node", evacuated: true, returned: true},
		{name: "ordinary deletion still waits for node"},
		{name: "unknown child finalizers remain", evacuated: true, foreign: true},
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
			owner := *metav1.NewControllerRef(rvr, v1alpha1.SchemeGroupVersion.WithKind("ReplicatedVolumeReplica"))
			dr := &v1alpha1.DRBDResource{
				ObjectMeta: metav1.ObjectMeta{Name: rvr.Name, OwnerReferences: []metav1.OwnerReference{owner}, Finalizers: []string{v1alpha1.RVRControllerFinalizer, v1alpha1.AgentFinalizer}},
				Spec:       v1alpha1.DRBDResourceSpec{NodeName: rvr.Spec.NodeName},
			}
			llv := &snc.LVMLogicalVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "old-disk", OwnerReferences: []metav1.OwnerReference{owner}, Finalizers: []string{v1alpha1.RVRControllerFinalizer, v1alpha1.AgentFinalizer, nodeConfiguratorFinalizer}},
			}
			if tc.foreign {
				dr.Finalizers = append(dr.Finalizers, "example.test/hold")
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
			for _, obj := range []client.Object{rvr, dr, llv} {
				err := cl.Get(t.Context(), client.ObjectKeyFromObject(obj), obj)
				if tc.evacuated && !tc.foreign {
					g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "%T remains", obj)
				} else {
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(obj.GetDeletionTimestamp()).NotTo(BeNil())
				}
			}
			if tc.foreign {
				g.Expect(dr.Finalizers).To(Equal([]string{"example.test/hold"}))
				g.Expect(llv.Finalizers).To(Equal([]string{"example.test/hold"}))
				g.Expect(rvr.Finalizers).To(ContainElement(v1alpha1.RVRControllerFinalizer))
			}
		})
	}
}

func TestEvacuatedResourceGuardsAndRetry(t *testing.T) {
	for _, tc := range []string{"unmarked", "not deleting", "RV finalizer", "foreign RVR finalizer", "member", "maintenance", "foreign child UID", "different node", "child not deleting", "child controller finalizer", "patch conflict", "already gone", "released"} {
		t.Run(tc, func(t *testing.T) {
			g := NewWithT(t)
			rvr := &v1alpha1.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{Name: "rv-1-0", UID: "old", Finalizers: []string{v1alpha1.RVRControllerFinalizer}, DeletionTimestamp: new(metav1.Now())},
				Spec:       v1alpha1.ReplicatedVolumeReplicaSpec{NodeName: "lost-node"},
				Status:     v1alpha1.ReplicatedVolumeReplicaStatus{Evacuated: new(true)},
			}
			rv := &v1alpha1.ReplicatedVolume{}
			dr := &v1alpha1.DRBDResource{
				ObjectMeta: metav1.ObjectMeta{Name: rvr.Name, OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(rvr, v1alpha1.SchemeGroupVersion.WithKind("ReplicatedVolumeReplica"))}, DeletionTimestamp: new(metav1.Now()), Finalizers: []string{v1alpha1.AgentFinalizer}},
				Spec:       v1alpha1.DRBDResourceSpec{NodeName: rvr.Spec.NodeName},
			}
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
				dr.OwnerReferences[0].UID = "other"
			case "different node":
				dr.Spec.NodeName = "other"
			case "child not deleting":
				dr.DeletionTimestamp = nil
			case "child controller finalizer":
				dr.Finalizers = append(dr.Finalizers, v1alpha1.RVRControllerFinalizer)
			}
			patches := 0
			failure := errors.New("conflict")
			cl := fake.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
				Patch: func(_ context.Context, _ client.WithWatch, _ client.Object, _ client.Patch, _ ...client.PatchOption) error {
					patches++
					if tc == "released" {
						return nil
					}
					if tc == "already gone" {
						return apierrors.NewNotFound(v1alpha1.SchemeGroupVersion.WithResource("drbdresources").GroupResource(), dr.Name)
					}
					return failure
				},
			}).Build()
			rec := NewReconciler(cl, nil, logr.Discard(), "test")
			outcome := rec.reconcileEvacuatedResources(t.Context(), rvr, rv, dr, nil)
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
