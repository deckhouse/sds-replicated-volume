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
	"strconv"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/controllers/drbdrop"
	"github.com/deckhouse/sds-replicated-volume/images/agent/internal/scheme"
	drbdutilsfake "github.com/deckhouse/sds-replicated-volume/images/agent/pkg/drbdutils/fake"
)

const (
	testNodeName        = "test-node"
	testDRBDRName       = "test-drbdr"
	testDRBDRNameOnNode = "sdsrv-test-drbdr"
	testDRBDROPName     = "test-op"
)

func trackBitmapArgs(peerNodeID uint8, start bool) []string {
	args := []string{
		"track-bitmap",
		testDRBDRNameOnNode,
		strconv.FormatUint(uint64(peerNodeID), 10),
		"0",
	}
	if start {
		args = append(args, "--start")
	}
	return args
}

func u8ptr(v uint8) *uint8 { return &v }

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
			DRBDResourceName: testDRBDRName,
			Type:             v1alpha1.DRBDResourceOperationCreateNewUUID,
		},
	}

	cl := crfake.NewClientBuilder().
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

func runBitmapOpTest(
	t *testing.T,
	opType v1alpha1.DRBDResourceOperationType,
	peerNodeID *uint8,
	peers []v1alpha1.DRBDResourcePeer,
	expectedCmds []*drbdutilsfake.ExpectedCmd,
) {
	t.Helper()

	sch, err := scheme.New()
	if err != nil {
		t.Fatal(err)
	}

	drbdr := &v1alpha1.DRBDResource{
		ObjectMeta: metav1.ObjectMeta{Name: testDRBDRName},
		Spec: v1alpha1.DRBDResourceSpec{
			NodeName: testNodeName,
			State:    v1alpha1.DRBDResourceStateUp,
			Peers:    peers,
		},
	}

	op := &v1alpha1.DRBDResourceOperation{
		ObjectMeta: metav1.ObjectMeta{Name: testDRBDROPName},
		Spec: v1alpha1.DRBDResourceOperationSpec{
			DRBDResourceName: testDRBDRName,
			Type:             opType,
			PeerNodeID:       peerNodeID,
		},
	}

	cl := crfake.NewClientBuilder().
		WithScheme(sch).
		WithObjects(drbdr, op).
		WithStatusSubresource(&v1alpha1.DRBDResourceOperation{}).
		Build()

	exec := &drbdutilsfake.Exec{}
	exec.ExpectCommands(expectedCmds...)
	exec.Setup(t)

	rec := drbdrop.NewOperationReconciler(cl, testNodeName)

	if _, err := rec.Reconcile(t.Context(), reconcile.Request{
		NamespacedName: client.ObjectKeyFromObject(op),
	}); err != nil {
		t.Fatalf("unexpected reconcile error: %v", err)
	}

	updated := &v1alpha1.DRBDResourceOperation{}
	if err := cl.Get(t.Context(), client.ObjectKeyFromObject(op), updated); err != nil {
		t.Fatalf("failed to get operation: %v", err)
	}
	if updated.Status.Phase != v1alpha1.DRBDOperationPhaseSucceeded {
		t.Errorf("expected phase Succeeded, got %q (message: %q)", updated.Status.Phase, updated.Status.Message)
	}
}

func TestReconcileTrackBitmap_ExplicitPeerNodeID(t *testing.T) {
	runBitmapOpTest(
		t,
		v1alpha1.DRBDResourceOperationTrackBitmap,
		u8ptr(3),
		[]v1alpha1.DRBDResourcePeer{
			{Name: "peer-1", NodeID: 1, Type: v1alpha1.DRBDResourceTypeDiskful},
			{Name: "peer-3", NodeID: 3, Type: v1alpha1.DRBDResourceTypeDiskful},
		},
		[]*drbdutilsfake.ExpectedCmd{
			{Name: "drbdsetup", Args: trackBitmapArgs(3, true)},
		},
	)
}

func TestReconcileTrackBitmap_AllPeers_FiltersDiskless(t *testing.T) {
	runBitmapOpTest(
		t,
		v1alpha1.DRBDResourceOperationTrackBitmap,
		nil,
		[]v1alpha1.DRBDResourcePeer{
			{Name: "peer-1", NodeID: 1, Type: v1alpha1.DRBDResourceTypeDiskful},
			{Name: "peer-2", NodeID: 2, Type: v1alpha1.DRBDResourceTypeDiskless},
			{Name: "peer-3", NodeID: 3},
		},
		[]*drbdutilsfake.ExpectedCmd{
			{Name: "drbdsetup", Args: trackBitmapArgs(1, true)},
			{Name: "drbdsetup", Args: trackBitmapArgs(3, true)},
		},
	)
}

func TestReconcileUntrackBitmap_AllPeers(t *testing.T) {
	runBitmapOpTest(
		t,
		v1alpha1.DRBDResourceOperationUntrackBitmap,
		nil,
		[]v1alpha1.DRBDResourcePeer{
			{Name: "peer-1", NodeID: 1, Type: v1alpha1.DRBDResourceTypeDiskful},
			{Name: "peer-3", NodeID: 3, Type: v1alpha1.DRBDResourceTypeDiskful},
		},
		[]*drbdutilsfake.ExpectedCmd{
			{Name: "drbdsetup", Args: trackBitmapArgs(1, false)},
			{Name: "drbdsetup", Args: trackBitmapArgs(3, false)},
		},
	)
}

func TestReconcileTrackBitmap_NoPeers_Succeeds(t *testing.T) {
	runBitmapOpTest(
		t,
		v1alpha1.DRBDResourceOperationTrackBitmap,
		nil,
		nil,
		nil,
	)
}
