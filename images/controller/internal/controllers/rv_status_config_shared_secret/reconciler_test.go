package rvstatusconfigsharedsecret_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	rvstatusconfigsharedsecret "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_status_config_shared_secret"
)

func newFakeClient(objs ...client.Object) client.Client {
	s := scheme.Scheme
	_ = v1alpha3.AddToScheme(s)
	return fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&v1alpha3.ReplicatedVolume{}).
		WithObjects(objs...).
		Build()
}

func newReconciler(cl client.Client) *rvstatusconfigsharedsecret.Reconciler {
	return &rvstatusconfigsharedsecret.Reconciler{
		Cl:     cl,
		Log:    slog.Default(),
		LogAlt: logr.Discard(),
	}
}

func createRV(name string) *v1alpha3.ReplicatedVolume { //nolint:unparam // name is used for different test cases
	return &v1alpha3.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1alpha3.ReplicatedVolumeSpec{
			Size:                       parseQuantity("10Gi"),
			ReplicatedStorageClassName: "test-storage-class",
		},
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

func createRVRWithUnsupportedAlgorithm(name, volumeName, nodeName string) *v1alpha3.ReplicatedVolumeReplica {
	rvr := createRVR(name, volumeName, nodeName)
	rvr.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{
		Conditions: []metav1.Condition{
			{
				Type:               v1alpha3.ConditionTypeConfigurationAdjusted,
				Status:             metav1.ConditionFalse,
				Reason:             "UnsupportedAlgorithm",
				Message:            "Algorithm not supported",
				ObservedGeneration: 1,
				LastTransitionTime: metav1.Now(),
			},
		},
	}
	return rvr
}

func parseQuantity(s string) resource.Quantity {
	q, _ := resource.ParseQuantity(s)
	return q
}

func TestReconcile_GenerateSharedSecret_Initial(t *testing.T) {
	ctx := context.Background()
	rv := createRV("test-rv")
	cl := newFakeClient(rv)
	rec := newReconciler(cl)

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "test-rv"},
	}

	result, err := rec.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter > 0 {
		t.Error("unexpected requeue")
	}

	// Verify shared secret was generated
	updatedRV := &v1alpha3.ReplicatedVolume{}
	if err := cl.Get(ctx, types.NamespacedName{Name: "test-rv"}, updatedRV); err != nil {
		t.Fatalf("failed to get RV: %v", err)
	}

	if updatedRV.Status == nil || updatedRV.Status.Config == nil {
		t.Fatal("status.config is nil")
	}

	if updatedRV.Status.Config.SharedSecret == "" {
		t.Error("sharedSecret was not generated")
	}

	if updatedRV.Status.Config.SharedSecretAlg != "sha256" {
		t.Errorf("expected algorithm sha256, got %s", updatedRV.Status.Config.SharedSecretAlg)
	}

	// Verify condition
	cond := meta.FindStatusCondition(updatedRV.Status.Conditions, "SharedSecretAlgorithmSelected")
	if cond == nil {
		t.Fatal("SharedSecretAlgorithmSelected condition not found")
	}
	if cond.Status != metav1.ConditionTrue {
		t.Errorf("expected condition status True, got %s", cond.Status)
	}
	if cond.Reason != "AlgorithmSelected" {
		t.Errorf("expected reason AlgorithmSelected, got %s", cond.Reason)
	}
}

func TestReconcile_HandleUnsupportedAlgorithm_SwitchToNext(t *testing.T) {
	ctx := context.Background()
	rv := createRV("test-rv")
	rv.Status = &v1alpha3.ReplicatedVolumeStatus{
		Config: &v1alpha3.DRBDResourceConfig{
			SharedSecret:    "test-secret",
			SharedSecretAlg: "sha256",
		},
	}

	rvr := createRVRWithUnsupportedAlgorithm("test-rvr", "test-rv", "node-1")
	cl := newFakeClient(rv, rvr)
	rec := newReconciler(cl)

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "test-rv"},
	}

	result, err := rec.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter > 0 {
		t.Error("unexpected requeue")
	}

	// Verify algorithm was switched
	updatedRV := &v1alpha3.ReplicatedVolume{}
	if err := cl.Get(ctx, types.NamespacedName{Name: "test-rv"}, updatedRV); err != nil {
		t.Fatalf("failed to get RV: %v", err)
	}

	if updatedRV.Status.Config.SharedSecretAlg != "sha1" {
		t.Errorf("expected algorithm sha1, got %s", updatedRV.Status.Config.SharedSecretAlg)
	}

	if updatedRV.Status.Config.SharedSecret == "test-secret" {
		t.Error("shared secret should be regenerated")
	}

	// Verify condition
	cond := meta.FindStatusCondition(updatedRV.Status.Conditions, "SharedSecretAlgorithmSelected")
	if cond == nil {
		t.Fatal("SharedSecretAlgorithmSelected condition not found")
	}
	if cond.Status != metav1.ConditionTrue {
		t.Errorf("expected condition status True, got %s", cond.Status)
	}
}

func TestReconcile_AllAlgorithmsExhausted(t *testing.T) {
	ctx := context.Background()
	rv := createRV("test-rv")
	rv.Status = &v1alpha3.ReplicatedVolumeStatus{
		Config: &v1alpha3.DRBDResourceConfig{
			SharedSecret:    "test-secret",
			SharedSecretAlg: "sha1", // Last algorithm
		},
	}

	rvr := createRVRWithUnsupportedAlgorithm("test-rvr", "test-rv", "node-1")
	cl := newFakeClient(rv, rvr)
	rec := newReconciler(cl)

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "test-rv"},
	}

	result, err := rec.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter > 0 {
		t.Error("unexpected requeue")
	}

	// Verify condition is set to False
	updatedRV := &v1alpha3.ReplicatedVolume{}
	if err := cl.Get(ctx, types.NamespacedName{Name: "test-rv"}, updatedRV); err != nil {
		t.Fatalf("failed to get RV: %v", err)
	}

	cond := meta.FindStatusCondition(updatedRV.Status.Conditions, "SharedSecretAlgorithmSelected")
	if cond == nil {
		t.Fatal("SharedSecretAlgorithmSelected condition not found")
	}
	if cond.Status != metav1.ConditionFalse {
		t.Errorf("expected condition status False, got %s", cond.Status)
	}
	if cond.Reason != "UnableToSelectSharedSecretAlgorithm" {
		t.Errorf("expected reason UnableToSelectSharedSecretAlgorithm, got %s", cond.Reason)
	}
}

func TestReconcile_NoUnsupportedAlgorithmErrors(t *testing.T) {
	ctx := context.Background()
	rv := createRV("test-rv")
	rv.Status = &v1alpha3.ReplicatedVolumeStatus{
		Config: &v1alpha3.DRBDResourceConfig{
			SharedSecret:    "test-secret",
			SharedSecretAlg: "sha256",
		},
	}

	rvr := createRVR("test-rvr", "test-rv", "node-1")
	cl := newFakeClient(rv, rvr)
	rec := newReconciler(cl)

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "test-rv"},
	}

	result, err := rec.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter > 0 {
		t.Error("unexpected requeue")
	}

	// Verify nothing changed
	updatedRV := &v1alpha3.ReplicatedVolume{}
	if err := cl.Get(ctx, types.NamespacedName{Name: "test-rv"}, updatedRV); err != nil {
		t.Fatalf("failed to get RV: %v", err)
	}

	if updatedRV.Status.Config.SharedSecret != "test-secret" {
		t.Error("shared secret should not be changed")
	}
	if updatedRV.Status.Config.SharedSecretAlg != "sha256" {
		t.Error("algorithm should not be changed")
	}
}

func TestReconcile_NonExistentRV(t *testing.T) {
	ctx := context.Background()
	cl := newFakeClient()
	rec := newReconciler(cl)

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "non-existent"},
	}

	result, err := rec.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter > 0 {
		t.Error("unexpected requeue")
	}
}
