package rvrstatusconfignodeid_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	rvrstatusconfignodeid "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rvr_status_config_node_id"
	e "github.com/deckhouse/sds-replicated-volume/images/controller/internal/errors"
)

func newFakeClient(objs ...client.Object) client.Client {
	s := scheme.Scheme
	_ = metav1.AddMetaToScheme(s)
	_ = v1alpha3.AddToScheme(s)

	builder := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&v1alpha3.ReplicatedVolumeReplica{})
	if len(objs) > 0 {
		builder = builder.WithObjects(objs...)
	}

	return builder.Build()
}

func newReconciler(cl client.Client) *rvrstatusconfignodeid.Reconciler {
	return &rvrstatusconfignodeid.Reconciler{
		Cl:     cl,
		Log:    slog.Default(),
		LogAlt: logr.Discard(),
	}
}

func createRVR(name, volumeName, nodeName string) *v1alpha3.ReplicatedVolumeReplica {
	return &v1alpha3.ReplicatedVolumeReplica{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
			ReplicatedVolumeName: volumeName,
			NodeName:             nodeName,
		},
	}
}

//nolint:unparam // volumeName is used in tests with different volumes
func createRVRWithNodeID(name, volumeName, nodeName string, nodeID uint) *v1alpha3.ReplicatedVolumeReplica {
	rvr := createRVR(name, volumeName, nodeName)
	rvr.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{
		Config: &v1alpha3.DRBDConfig{
			NodeId: &nodeID,
		},
	}
	return rvr
}

func TestReconcile_AssignNodeId_FirstReplica(t *testing.T) {
	ctx := context.Background()
	rvr := createRVR("rvr-1", "volume-1", "node-1")
	cl := newFakeClient(rvr)
	rec := newReconciler(cl)

	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "rvr-1"}}
	_, err := rec.Reconcile(ctx, req)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify nodeID was assigned
	updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
	if err := cl.Get(ctx, client.ObjectKey{Name: "rvr-1"}, updatedRVR); err != nil {
		t.Fatalf("failed to get RVR: %v", err)
	}

	if updatedRVR.Status == nil || updatedRVR.Status.Config == nil || updatedRVR.Status.Config.NodeId == nil {
		t.Fatal("nodeId was not assigned")
	}

	if *updatedRVR.Status.Config.NodeId != 0 {
		t.Errorf("expected nodeId 0, got %d", *updatedRVR.Status.Config.NodeId)
	}
}

func TestReconcile_AssignNodeId_MultipleReplicas(t *testing.T) {
	ctx := context.Background()
	rvr1 := createRVRWithNodeID("rvr-1", "volume-1", "node-1", 0)
	rvr2 := createRVRWithNodeID("rvr-2", "volume-1", "node-2", 1)
	rvr3 := createRVR("rvr-3", "volume-1", "node-3")
	cl := newFakeClient(rvr1, rvr2, rvr3)
	rec := newReconciler(cl)

	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "rvr-3"}}
	_, err := rec.Reconcile(ctx, req)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify nodeId was assigned (should be 2, as 0 and 1 are taken)
	updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
	if err := cl.Get(ctx, client.ObjectKey{Name: "rvr-3"}, updatedRVR); err != nil {
		t.Fatalf("failed to get RVR: %v", err)
	}

	if updatedRVR.Status == nil || updatedRVR.Status.Config == nil || updatedRVR.Status.Config.NodeId == nil {
		t.Fatal("nodeId was not assigned")
	}

	if *updatedRVR.Status.Config.NodeId != 2 {
		t.Errorf("expected nodeId 2, got %d", *updatedRVR.Status.Config.NodeId)
	}
}

func TestReconcile_AssignNodeId_UniqueNodeIds(t *testing.T) {
	ctx := context.Background()
	// Create 5 replicas with nodeIds 0, 1, 2, 3, 4
	rvrs := make([]client.Object, 5)
	for i := 0; i < 5; i++ {
		rvrs[i] = createRVRWithNodeID(
			fmt.Sprintf("rvr-%d", i+1),
			"volume-1",
			fmt.Sprintf("node-%d", i+1),
			uint(i),
		)
	}
	// Add one more without nodeId
	rvr6 := createRVR("rvr-6", "volume-1", "node-6")
	rvrs = append(rvrs, rvr6)

	cl := newFakeClient(rvrs...)
	rec := newReconciler(cl)

	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "rvr-6"}}
	_, err := rec.Reconcile(ctx, req)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify nodeId was assigned (should be 5)
	updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
	if err := cl.Get(ctx, client.ObjectKey{Name: "rvr-6"}, updatedRVR); err != nil {
		t.Fatalf("failed to get RVR: %v", err)
	}

	if *updatedRVR.Status.Config.NodeId != 5 {
		t.Errorf("expected nodeId 5, got %d", *updatedRVR.Status.Config.NodeId)
	}
}

func TestReconcile_AssignNodeId_AlreadyAssigned(t *testing.T) {
	ctx := context.Background()
	rvr := createRVRWithNodeID("rvr-1", "volume-1", "node-1", 3)
	cl := newFakeClient(rvr)
	rec := newReconciler(cl)

	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "rvr-1"}}
	_, err := rec.Reconcile(ctx, req)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify nodeID remains unchanged
	updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
	if err := cl.Get(ctx, client.ObjectKey{Name: "rvr-1"}, updatedRVR); err != nil {
		t.Fatalf("failed to get RVR: %v", err)
	}

	if *updatedRVR.Status.Config.NodeId != 3 {
		t.Errorf("expected nodeId to remain 3, got %d", *updatedRVR.Status.Config.NodeId)
	}
}

func TestReconcile_AssignNodeId_TooManyReplicas(t *testing.T) {
	ctx := context.Background()
	// Create 9 replicas (max is 8: 0-7)
	rvrs := make([]client.Object, 9)
	for i := 0; i < 8; i++ {
		rvrs[i] = createRVRWithNodeID(
			fmt.Sprintf("rvr-%d", i+1),
			"volume-1",
			fmt.Sprintf("node-%d", i+1),
			uint(i),
		)
	}
	rvrs[8] = createRVR("rvr-9", "volume-1", "node-9")

	cl := newFakeClient(rvrs...)
	rec := newReconciler(cl)

	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "rvr-9"}}
	_, err := rec.Reconcile(ctx, req)

	if err == nil {
		t.Fatal("expected error for too many replicas, got nil")
	}

	if !errors.Is(err, e.ErrInvalidCluster) {
		t.Errorf("expected ErrInvalidCluster, got %v", err)
	}

	// Verify that ConfigurationAdjusted condition was set to False
	updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
	if err := cl.Get(ctx, client.ObjectKey{Name: "rvr-9"}, updatedRVR); err != nil {
		t.Fatalf("failed to get RVR: %v", err)
	}

	cond := meta.FindStatusCondition(updatedRVR.Status.Conditions, v1alpha3.ConditionTypeConfigurationAdjusted)
	if cond == nil {
		t.Fatal("expected ConfigurationAdjusted condition to be set")
	}
	if cond.Status != metav1.ConditionFalse {
		t.Errorf("expected ConfigurationAdjusted condition status to be False, got %v", cond.Status)
	}
	if cond.Reason != v1alpha3.ReasonConfigurationFailed {
		t.Errorf("expected ConfigurationAdjusted condition reason to be %s, got %s", v1alpha3.ReasonConfigurationFailed, cond.Reason)
	}
	if cond.Message == "" {
		t.Error("expected ConfigurationAdjusted condition message to be set")
	}
}

func TestReconcile_AssignNodeId_AllNodeIdsUsed(t *testing.T) {
	ctx := context.Background()
	// Create 8 replicas using all nodeIds 0-7
	rvrs := make([]client.Object, 8)
	for i := 0; i < 8; i++ {
		rvrs[i] = createRVRWithNodeID(
			fmt.Sprintf("rvr-%d", i+1),
			"volume-1",
			fmt.Sprintf("node-%d", i+1),
			uint(i),
		)
	}
	// Add one more without nodeId (should fail)
	rvr9 := createRVR("rvr-9", "volume-1", "node-9")
	rvrs = append(rvrs, rvr9)

	cl := newFakeClient(rvrs...)
	rec := newReconciler(cl)

	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "rvr-9"}}
	_, err := rec.Reconcile(ctx, req)

	if err == nil {
		t.Fatal("expected error when all nodeIds are used, got nil")
	}

	if !errors.Is(err, e.ErrInvalidCluster) {
		t.Errorf("expected ErrInvalidCluster, got %v", err)
	}

	// Verify that ConfigurationAdjusted condition was set to False
	updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
	if err := cl.Get(ctx, client.ObjectKey{Name: "rvr-9"}, updatedRVR); err != nil {
		t.Fatalf("failed to get RVR: %v", err)
	}

	cond := meta.FindStatusCondition(updatedRVR.Status.Conditions, v1alpha3.ConditionTypeConfigurationAdjusted)
	if cond == nil {
		t.Fatal("expected ConfigurationAdjusted condition to be set")
	}
	if cond.Status != metav1.ConditionFalse {
		t.Errorf("expected ConfigurationAdjusted condition status to be False, got %v", cond.Status)
	}
	if cond.Reason != v1alpha3.ReasonConfigurationFailed {
		t.Errorf("expected ConfigurationAdjusted condition reason to be %s, got %s", v1alpha3.ReasonConfigurationFailed, cond.Reason)
	}
	if cond.Message == "" {
		t.Error("expected ConfigurationAdjusted condition message to be set")
	}
}

func TestReconcile_AssignNodeId_NonExistentRVR(t *testing.T) {
	ctx := context.Background()
	cl := newFakeClient()
	rec := newReconciler(cl)

	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "non-existent"}}
	_, err := rec.Reconcile(ctx, req)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReconcile_AssignNodeId_DifferentVolumes(t *testing.T) {
	ctx := context.Background()
	// Create replicas for different volumes - they should not interfere
	rvr1 := createRVRWithNodeID("rvr-1", "volume-1", "node-1", 0)
	rvr2 := createRVRWithNodeID("rvr-2", "volume-1", "node-2", 1)
	rvr3 := createRVR("rvr-3", "volume-2", "node-3") // Different volume
	cl := newFakeClient(rvr1, rvr2, rvr3)
	rec := newReconciler(cl)

	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "rvr-3"}}
	_, err := rec.Reconcile(ctx, req)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify nodeId was assigned (should be 0, as volume-2 has no replicas with nodeId)
	updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
	if err := cl.Get(ctx, client.ObjectKey{Name: "rvr-3"}, updatedRVR); err != nil {
		t.Fatalf("failed to get RVR: %v", err)
	}

	if *updatedRVR.Status.Config.NodeId != 0 {
		t.Errorf("expected nodeId 0, got %d", *updatedRVR.Status.Config.NodeId)
	}
}

func TestReconcile_AssignNodeId_GapInNodeIds(t *testing.T) {
	ctx := context.Background()
	// Create replicas with nodeIds 0, 2, 3 (gap at 1)
	rvr1 := createRVRWithNodeID("rvr-1", "volume-1", "node-1", 0)
	rvr2 := createRVRWithNodeID("rvr-2", "volume-1", "node-2", 2)
	rvr3 := createRVRWithNodeID("rvr-3", "volume-1", "node-3", 3)
	rvr4 := createRVR("rvr-4", "volume-1", "node-4")
	cl := newFakeClient(rvr1, rvr2, rvr3, rvr4)
	rec := newReconciler(cl)

	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "rvr-4"}}
	_, err := rec.Reconcile(ctx, req)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify nodeId was assigned (should be 1, filling the gap)
	updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
	if err := cl.Get(ctx, client.ObjectKey{Name: "rvr-4"}, updatedRVR); err != nil {
		t.Fatalf("failed to get RVR: %v", err)
	}

	if *updatedRVR.Status.Config.NodeId != 1 {
		t.Errorf("expected nodeId 1 (filling gap), got %d", *updatedRVR.Status.Config.NodeId)
	}
}

func TestReconcile_UnknownRequestType(t *testing.T) {
	// With standard reconcile.Reconciler, we always receive reconcile.Request
	// No custom request types, so this test is no longer applicable
	t.Skip("Standard reconcile.Reconciler always uses reconcile.Request")
}
