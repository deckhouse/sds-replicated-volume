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

package drbdr

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	obju "github.com/deckhouse/sds-replicated-volume/api/objutilv1"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/scheme"
)

// TestReconcileLLVFinalizerAdd verifies that a deleting LLV never forces
// intendedDisk="" (which would provoke a spurious detach): the already-attached
// disk is kept as intended with no error (so Configured stays True and cleanup
// is not deadlocked), while a deleting LLV with nothing attached yet returns an
// error.
func TestReconcileLLVFinalizerAdd(t *testing.T) {
	const (
		llvName      = "test-llv"
		lvgName      = "test-lvg"
		lvNameOnNode = "test-lv"
		vgNameOnNode = "test-vg"
		diskPath     = "/dev/" + vgNameOnNode + "/" + lvNameOnNode
	)

	sch, err := scheme.New()
	if err != nil {
		t.Fatal(err)
	}

	newReconciler := func(objs ...client.Object) *Reconciler {
		cl := fake.NewClientBuilder().WithScheme(sch).WithObjects(objs...).Build()
		return NewReconciler(cl, "test-node", nil)
	}

	lvg := &snc.LVMVolumeGroup{
		ObjectMeta: metav1.ObjectMeta{Name: lvgName},
		Spec:       snc.LVMVolumeGroupSpec{ActualVGNameOnTheNode: vgNameOnNode},
	}

	llv := func(deleting bool, finalizers ...string) *snc.LVMLogicalVolume {
		obj := &snc.LVMLogicalVolume{
			ObjectMeta: metav1.ObjectMeta{Name: llvName, Finalizers: finalizers},
			Spec: snc.LVMLogicalVolumeSpec{
				ActualLVNameOnTheNode: lvNameOnNode,
				LVMVolumeGroupName:    lvgName,
			},
		}
		if deleting {
			now := metav1.NewTime(time.Now())
			obj.DeletionTimestamp = &now
		}
		return obj
	}

	t.Run("deleting LLV with attached disk - keeps disk, no error", func(t *testing.T) {
		r := newReconciler(llv(true, v1alpha1.AgentFinalizer), lvg)

		got, err := r.reconcileLLVFinalizerAdd(t.Context(), llvName, diskPath)

		if got != diskPath {
			t.Errorf("diskPath = %q, want %q (attached disk must be kept to avoid detach)", got, diskPath)
		}
		if err != nil {
			t.Fatalf("expected no error for deleting LLV with attached disk (Configured must stay True), got %v", err)
		}
	})

	t.Run("deleting LLV with no attached disk - returns empty, error", func(t *testing.T) {
		r := newReconciler(llv(true, v1alpha1.AgentFinalizer), lvg)

		got, err := r.reconcileLLVFinalizerAdd(t.Context(), llvName, "")

		if got != "" {
			t.Errorf("diskPath = %q, want empty (nothing attached to keep)", got)
		}
		if err == nil {
			t.Fatal("expected error for deleting LLV, got nil")
		}
		if reason := getConfiguredReason(err); reason != v1alpha1.DRBDResourceCondConfiguredReasonBackingVolumeDeleting {
			t.Errorf("reason = %q, want %q", reason, v1alpha1.DRBDResourceCondConfiguredReasonBackingVolumeDeleting)
		}
	})

	t.Run("non-deleting LLV - adds finalizer, computes disk, ignores attachedDisk", func(t *testing.T) {
		r := newReconciler(llv(false), lvg)

		// attachedDisk is intentionally bogus; it must be ignored on the
		// healthy path in favor of the disk computed from the LVG/LLV.
		got, err := r.reconcileLLVFinalizerAdd(t.Context(), llvName, "/dev/stale/disk")

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != diskPath {
			t.Errorf("diskPath = %q, want %q", got, diskPath)
		}

		updated := &snc.LVMLogicalVolume{}
		if getErr := r.cl.Get(t.Context(), client.ObjectKey{Name: llvName}, updated); getErr != nil {
			t.Fatalf("getting LLV: %v", getErr)
		}
		if !obju.HasFinalizer(updated, v1alpha1.AgentFinalizer) {
			t.Errorf("expected agent finalizer to be added to non-deleting LLV")
		}
	})

	t.Run("missing LLV - returns error", func(t *testing.T) {
		r := newReconciler(lvg)

		got, err := r.reconcileLLVFinalizerAdd(t.Context(), llvName, diskPath)

		if got != "" {
			t.Errorf("diskPath = %q, want empty", got)
		}
		if err == nil {
			t.Fatal("expected error for missing LLV, got nil")
		}
		if reason := getConfiguredReason(err); reason != v1alpha1.DRBDResourceCondConfiguredReasonBackingVolumeUnavailable {
			t.Errorf("reason = %q, want %q", reason, v1alpha1.DRBDResourceCondConfiguredReasonBackingVolumeUnavailable)
		}
	})

	t.Run("missing LVG - returns error", func(t *testing.T) {
		// LLV exists (non-deleting) but its LVG is absent from the cluster.
		r := newReconciler(llv(false, v1alpha1.AgentFinalizer))

		got, err := r.reconcileLLVFinalizerAdd(t.Context(), llvName, diskPath)

		if got != "" {
			t.Errorf("diskPath = %q, want empty", got)
		}
		if err == nil {
			t.Fatal("expected error for missing LVG, got nil")
		}
		if reason := getConfiguredReason(err); reason != v1alpha1.DRBDResourceCondConfiguredReasonBackingVolumeUnavailable {
			t.Errorf("reason = %q, want %q", reason, v1alpha1.DRBDResourceCondConfiguredReasonBackingVolumeUnavailable)
		}
	})
}
