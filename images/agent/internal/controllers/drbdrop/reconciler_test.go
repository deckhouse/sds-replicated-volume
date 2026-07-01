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

package drbdrop_test

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/controllers/drbdrop"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/scheme"
)

const (
	testNodeName    = "test-node"
	testDRBDRName   = "test-drbdr"
	testDRBDROPName = "test-op"
)

func TestReconcileCreateNewUUID_MaintenanceMode(t *testing.T) {
	sch, err := scheme.New()
	if err != nil {
		t.Fatal(err)
	}

	drbdr := &v1alpha1.DRBDResource{
		ObjectMeta: metav1.ObjectMeta{Name: testDRBDRName},
		Spec: v1alpha1.DRBDResourceSpec{
			NodeName:    testNodeName,
			State:       v1alpha1.DRBDResourceStateUp,
			Maintenance: v1alpha1.MaintenanceModeNoResourceReconciliation,
		},
	}

	op := &v1alpha1.DRBDResourceOperation{
		ObjectMeta: metav1.ObjectMeta{Name: testDRBDROPName},
		Spec: v1alpha1.DRBDResourceOperationSpec{
			NodeName:         testNodeName,
			DRBDResourceName: testDRBDRName,
			Type:             v1alpha1.DRBDResourceOperationCreateNewUUID,
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(sch).
		WithObjects(drbdr, op).
		WithStatusSubresource(&v1alpha1.DRBDResourceOperation{}).
		Build()

	rec := drbdrop.NewOperationReconciler(cl, testNodeName)

	_, err = rec.Reconcile(t.Context(), reconcile.Request{
		NamespacedName: client.ObjectKeyFromObject(op),
	})
	if err != nil {
		t.Fatalf("unexpected reconcile error: %v", err)
	}

	// Verify operation is Failed with maintenance mode message
	updated := &v1alpha1.DRBDResourceOperation{}
	if err := cl.Get(t.Context(), client.ObjectKeyFromObject(op), updated); err != nil {
		t.Fatalf("failed to get operation: %v", err)
	}

	if updated.Status.Phase != v1alpha1.DRBDOperationPhaseFailed {
		t.Errorf("expected phase %q, got %q", v1alpha1.DRBDOperationPhaseFailed, updated.Status.Phase)
	}

	if updated.Status.Message != "DRBD resource is in maintenance mode" {
		t.Errorf("expected maintenance mode message, got %q", updated.Status.Message)
	}
}

// TestReconcileCreateNewUUID_DRBDResourceMissing verifies that when the operation's
// spec.nodeName matches the local node but the referenced DRBDResource does not exist,
// the operation is failed (not re-queued indefinitely).
func TestReconcileCreateNewUUID_DRBDResourceMissing(t *testing.T) {
	sch, err := scheme.New()
	if err != nil {
		t.Fatal(err)
	}

	op := &v1alpha1.DRBDResourceOperation{
		ObjectMeta: metav1.ObjectMeta{Name: testDRBDROPName},
		Spec: v1alpha1.DRBDResourceOperationSpec{
			NodeName:         testNodeName,
			DRBDResourceName: testDRBDRName,
			Type:             v1alpha1.DRBDResourceOperationCreateNewUUID,
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(sch).
		WithObjects(op).
		WithStatusSubresource(&v1alpha1.DRBDResourceOperation{}).
		Build()

	rec := drbdrop.NewOperationReconciler(cl, testNodeName)

	res, err := rec.Reconcile(t.Context(), reconcile.Request{
		NamespacedName: client.ObjectKeyFromObject(op),
	})
	if err != nil {
		t.Fatalf("unexpected reconcile error: %v", err)
	}
	if res.Requeue || res.RequeueAfter != 0 { //nolint:staticcheck // Requeue field is set by flow.ToCtrl on transient errors
		t.Errorf("expected no requeue when DRBDResource is missing, got %+v", res)
	}

	updated := &v1alpha1.DRBDResourceOperation{}
	if err := cl.Get(t.Context(), client.ObjectKeyFromObject(op), updated); err != nil {
		t.Fatalf("failed to get operation: %v", err)
	}

	if updated.Status.Phase != v1alpha1.DRBDOperationPhaseFailed {
		t.Errorf("expected phase %q, got %q", v1alpha1.DRBDOperationPhaseFailed, updated.Status.Phase)
	}
	if updated.Status.Message == "" {
		t.Errorf("expected non-empty failure message")
	}
}
