package snapmesh

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/rv_controller/dmte"
)

func TestConfirmTrackBitmapCreatesOperationForSecondary(t *testing.T) {
	p := newPrepareTestProvider(t)
	gctx := p.Global()
	secondary := p.Replica(1)

	result := confirmTrackBitmap(gctx, secondary, 0)
	if !result.Confirmed.IsEmpty() {
		t.Fatalf("expected track bitmap step to stay pending after create, got confirmed=%v", result.Confirmed)
	}

	created := &v1alpha1.DRBDResourceOperation{}
	if err := gctx.cl.Get(gctx.ctx, client.ObjectKey{Name: "snap-1-track-bitmap-1"}, created); err != nil {
		t.Fatalf("get created track-bitmap operation: %v", err)
	}
	if created.Spec.DRBDResourceName != "rvr-0" {
		t.Fatalf("unexpected drbd resource name: %q", created.Spec.DRBDResourceName)
	}
	if created.Spec.Type != v1alpha1.DRBDResourceOperationTrackBitmap {
		t.Fatalf("unexpected operation type: %q", created.Spec.Type)
	}
	if created.Spec.PeerNodeID == nil || *created.Spec.PeerNodeID != 1 {
		t.Fatalf("unexpected peer node id: %v", created.Spec.PeerNodeID)
	}
}

func TestConfirmFlushBitmapCreatesOperationOnPrimary(t *testing.T) {
	p := newPrepareTestProvider(t)
	gctx := p.Global()

	result := confirmFlushBitmap(gctx, 0)
	if !result.Confirmed.IsEmpty() {
		t.Fatalf("expected flush bitmap step to stay pending after create, got confirmed=%v", result.Confirmed)
	}

	created := &v1alpha1.DRBDResourceOperation{}
	if err := gctx.cl.Get(gctx.ctx, client.ObjectKey{Name: "snap-1-flush-bitmap-primary"}, created); err != nil {
		t.Fatalf("get created flush-bitmap operation: %v", err)
	}
	if created.Spec.DRBDResourceName != "rvr-0" {
		t.Fatalf("unexpected drbd resource name: %q", created.Spec.DRBDResourceName)
	}
	if created.Spec.Type != v1alpha1.DRBDResourceOperationFlushBitmap {
		t.Fatalf("unexpected operation type: %q", created.Spec.Type)
	}
	if created.Spec.PeerNodeID != nil {
		t.Fatalf("unexpected peer node id: %v", created.Spec.PeerNodeID)
	}
}

func TestConfirmSuspendIOCreatesOperationOnPrimary(t *testing.T) {
	p := newPrepareTestProvider(t)
	gctx := p.Global()

	result := confirmSuspendIO(gctx, 0)
	if !result.Confirmed.IsEmpty() {
		t.Fatalf("expected suspend io step to stay pending after create, got confirmed=%v", result.Confirmed)
	}

	created := &v1alpha1.DRBDResourceOperation{}
	if err := gctx.cl.Get(gctx.ctx, client.ObjectKey{Name: "snap-1-suspend-io-primary"}, created); err != nil {
		t.Fatalf("get created suspend-io operation: %v", err)
	}
	if created.Spec.DRBDResourceName != "rvr-0" {
		t.Fatalf("unexpected drbd resource name: %q", created.Spec.DRBDResourceName)
	}
	if created.Spec.Type != v1alpha1.DRBDResourceOperationSuspendIO {
		t.Fatalf("unexpected operation type: %q", created.Spec.Type)
	}
}

func TestConfirmCreateSecondarySnapshotsCreatesMissingSecondarySnapshot(t *testing.T) {
	p := newPrepareTestProvider(t)
	gctx := p.Global()
	secondary := p.Replica(1)

	result := confirmCreateSecondarySnapshots(gctx, secondary, 0)
	if !result.Confirmed.IsEmpty() {
		t.Fatalf("expected secondary step to stay pending after create, got confirmed=%v", result.Confirmed)
	}

	created := &v1alpha1.ReplicatedVolumeReplicaSnapshot{}
	if err := gctx.cl.Get(gctx.ctx, client.ObjectKey{Name: "snap-1-node-b"}, created); err != nil {
		t.Fatalf("get created secondary snapshot: %v", err)
	}
	if created.Spec.ReplicatedVolumeSnapshotName != "snap-1" {
		t.Fatalf("unexpected snapshot name: %q", created.Spec.ReplicatedVolumeSnapshotName)
	}
	if created.Spec.ReplicatedVolumeReplicaName != "rvr-1" {
		t.Fatalf("unexpected replica name: %q", created.Spec.ReplicatedVolumeReplicaName)
	}
	if created.Spec.NodeName != "node-b" {
		t.Fatalf("unexpected node name: %q", created.Spec.NodeName)
	}
}

func TestConfirmCreatePrimarySnapshotCreatesPrimaryAfterSecondaryReady(t *testing.T) {
	secondary := &v1alpha1.ReplicatedVolumeReplicaSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name: "snap-1-node-b",
		},
		Spec: v1alpha1.ReplicatedVolumeReplicaSnapshotSpec{
			ReplicatedVolumeSnapshotName: "snap-1",
			ReplicatedVolumeReplicaName:  "rvr-1",
			NodeName:                     "node-b",
		},
		Status: v1alpha1.ReplicatedVolumeReplicaSnapshotStatus{
			SnapshotHandle: "snap-handle-b",
		},
	}

	p := newPrepareTestProvider(t, secondary)
	gctx := p.Global()

	result := confirmCreatePrimarySnapshot(gctx, 0)
	if !result.Confirmed.IsEmpty() {
		t.Fatalf("expected primary step to stay pending after create, got confirmed=%v", result.Confirmed)
	}

	created := &v1alpha1.ReplicatedVolumeReplicaSnapshot{}
	if err := gctx.cl.Get(gctx.ctx, client.ObjectKey{Name: "snap-1-node-a"}, created); err != nil {
		t.Fatalf("get created primary snapshot: %v", err)
	}
	if created.Spec.ReplicatedVolumeSnapshotName != "snap-1" {
		t.Fatalf("unexpected snapshot name: %q", created.Spec.ReplicatedVolumeSnapshotName)
	}
	if created.Spec.ReplicatedVolumeReplicaName != "rvr-0" {
		t.Fatalf("unexpected replica name: %q", created.Spec.ReplicatedVolumeReplicaName)
	}
	if created.Spec.NodeName != "node-a" {
		t.Fatalf("unexpected node name: %q", created.Spec.NodeName)
	}
}

func TestConfirmCreatePrimarySnapshotAllowsCleanupAfterPrimaryFailure(t *testing.T) {
	primary := &v1alpha1.ReplicatedVolumeReplicaSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name: "snap-1-node-a",
		},
		Spec: v1alpha1.ReplicatedVolumeReplicaSnapshotSpec{
			ReplicatedVolumeSnapshotName: "snap-1",
			ReplicatedVolumeReplicaName:  "rvr-0",
			NodeName:                     "node-a",
		},
		Status: v1alpha1.ReplicatedVolumeReplicaSnapshotStatus{
			Phase: v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseFailed,
		},
	}

	p := newPrepareTestProvider(t, primary)
	result := confirmCreatePrimarySnapshot(p.Global(), 0)
	if result.MustConfirm != result.Confirmed {
		t.Fatalf("expected primary snapshot failure to confirm step for cleanup, got must=%v confirmed=%v", result.MustConfirm, result.Confirmed)
	}
}

func TestPrepareDispatcherSwitchesToCleanupPlanAfterPrimaryFailure(t *testing.T) {
	primary := &v1alpha1.ReplicatedVolumeReplicaSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name: "snap-1-node-a",
		},
		Spec: v1alpha1.ReplicatedVolumeReplicaSnapshotSpec{
			ReplicatedVolumeSnapshotName: "snap-1",
			ReplicatedVolumeReplicaName:  "rvr-0",
			NodeName:                     "node-a",
		},
		Status: v1alpha1.ReplicatedVolumeReplicaSnapshotStatus{
			Phase: v1alpha1.ReplicatedVolumeReplicaSnapshotPhaseFailed,
		},
	}
	suspendOp := &v1alpha1.DRBDResourceOperation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "snap-1-suspend-io-primary",
		},
		Status: v1alpha1.DRBDResourceOperationStatus{
			Phase: v1alpha1.DRBDOperationPhaseSucceeded,
		},
	}

	p := newPrepareTestProvider(t, primary, suspendOp)
	p0 := &v1alpha1.ReplicatedVolumeDatameshTransition{
		Type:        v1alpha1.ReplicatedVolumeDatameshTransitionType("PrepareSnapshot"),
		ReplicaName: "rvr-0",
		Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupSync,
		PlanID:      string(preparePlanID),
		Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
			{Name: "Create primary snapshot", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive},
		},
	}
	p1 := &v1alpha1.ReplicatedVolumeDatameshTransition{
		Type:        v1alpha1.ReplicatedVolumeDatameshTransitionType("PrepareSnapshot"),
		ReplicaName: "rvr-1",
		Group:       v1alpha1.ReplicatedVolumeDatameshTransitionGroupSync,
		PlanID:      string(preparePlanID),
		Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
			{Name: "Create primary snapshot", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive},
		},
	}
	p.Global().replicas[0].prepareTransition = p0
	p.Global().replicas[1].prepareTransition = p1

	var decisions []dmte.DispatchDecision
	for decision := range prepareDispatcher()(p) {
		decisions = append(decisions, decision)
	}
	if len(decisions) != 2 {
		t.Fatalf("expected 2 cleanup dispatch decisions, got %d", len(decisions))
	}
	if decisions[0] != dmte.DispatchReplica(p.Replica(0), prepareTransitionType, prepareCleanupPlanID) {
		t.Fatalf("unexpected first decision: %#v", decisions[0])
	}
	if decisions[1] != dmte.DispatchReplica(p.Replica(1), prepareTransitionType, prepareCleanupPlanID) {
		t.Fatalf("unexpected second decision: %#v", decisions[1])
	}
}

func TestPrepareDispatcherSwitchesToCleanupOnUnsafeTimeout(t *testing.T) {
	suspendOp := &v1alpha1.DRBDResourceOperation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "snap-1-suspend-io-primary",
		},
		Status: v1alpha1.DRBDResourceOperationStatus{
			Phase: v1alpha1.DRBDOperationPhaseSucceeded,
		},
	}

	p := newPrepareTestProvider(t, suspendOp)
	expiredAt := metav1.NewTime(time.Now().Add(-prepareUnsafeTimeout - time.Minute))
	p.Global().rvs.Status.PrepareTransitions = []v1alpha1.ReplicatedVolumeDatameshTransition{
		{
			Type:   v1alpha1.ReplicatedVolumeDatameshTransitionType("PrepareSnapshot"),
			PlanID: string(preparePlanID),
			Steps: []v1alpha1.ReplicatedVolumeDatameshTransitionStep{
				{Name: "Flush bitmap", Status: v1alpha1.ReplicatedVolumeDatameshTransitionStepStatusActive, StartedAt: &expiredAt},
			},
		},
	}
	p0 := &p.Global().rvs.Status.PrepareTransitions[0]
	p.Global().allReplicas[0].prepareTransition = p0
	p.Global().allReplicas[1].prepareTransition = p0

	var decisions []dmte.DispatchDecision
	for decision := range prepareDispatcher()(p) {
		decisions = append(decisions, decision)
	}
	if len(decisions) != 2 {
		t.Fatalf("expected 2 cleanup dispatch decisions, got %d", len(decisions))
	}
	if decisions[0] != dmte.DispatchReplica(p.Replica(0), prepareTransitionType, prepareCleanupPlanID) {
		t.Fatalf("unexpected first decision: %#v", decisions[0])
	}
	if decisions[1] != dmte.DispatchReplica(p.Replica(1), prepareTransitionType, prepareCleanupPlanID) {
		t.Fatalf("unexpected second decision: %#v", decisions[1])
	}
}

func TestConfirmResumeIOCreatesOperationOnPrimary(t *testing.T) {
	primary := &v1alpha1.ReplicatedVolumeReplicaSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name: "snap-1-node-a",
		},
		Spec: v1alpha1.ReplicatedVolumeReplicaSnapshotSpec{
			ReplicatedVolumeSnapshotName: "snap-1",
			ReplicatedVolumeReplicaName:  "rvr-0",
			NodeName:                     "node-a",
		},
		Status: v1alpha1.ReplicatedVolumeReplicaSnapshotStatus{
			SnapshotHandle: "snap-handle-a",
		},
	}

	p := newPrepareTestProvider(t, primary)
	gctx := p.Global()

	result := confirmResumeIO(gctx, 0)
	if !result.Confirmed.IsEmpty() {
		t.Fatalf("expected resume io step to stay pending after create, got confirmed=%v", result.Confirmed)
	}

	created := &v1alpha1.DRBDResourceOperation{}
	if err := gctx.cl.Get(gctx.ctx, client.ObjectKey{Name: "snap-1-resume-io-primary"}, created); err != nil {
		t.Fatalf("get created resume-io operation: %v", err)
	}
	if created.Spec.Type != v1alpha1.DRBDResourceOperationResumeIO {
		t.Fatalf("unexpected operation type: %q", created.Spec.Type)
	}
}

func TestConfirmUntrackBitmapCreatesOperationForSecondary(t *testing.T) {
	p := newPrepareTestProvider(t)
	gctx := p.Global()
	secondary := p.Replica(1)

	result := confirmUntrackBitmap(gctx, secondary, 0)
	if !result.Confirmed.IsEmpty() {
		t.Fatalf("expected untrack bitmap step to stay pending after create, got confirmed=%v", result.Confirmed)
	}

	created := &v1alpha1.DRBDResourceOperation{}
	if err := gctx.cl.Get(gctx.ctx, client.ObjectKey{Name: "snap-1-untrack-bitmap-1"}, created); err != nil {
		t.Fatalf("get created untrack-bitmap operation: %v", err)
	}
	if created.Spec.DRBDResourceName != "rvr-0" {
		t.Fatalf("unexpected drbd resource name: %q", created.Spec.DRBDResourceName)
	}
	if created.Spec.Type != v1alpha1.DRBDResourceOperationUntrackBitmap {
		t.Fatalf("unexpected operation type: %q", created.Spec.Type)
	}
	if created.Spec.PeerNodeID == nil || *created.Spec.PeerNodeID != 1 {
		t.Fatalf("unexpected peer node id: %v", created.Spec.PeerNodeID)
	}
}

func newPrepareTestProvider(t *testing.T, objs ...client.Object) provider {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme: %v", err)
	}

	builder := fake.NewClientBuilder().WithScheme(scheme)
	for _, obj := range objs {
		if obj == nil {
			continue
		}
		builder = builder.WithObjects(obj)
	}
	cl := builder.Build()

	rvs := &v1alpha1.ReplicatedVolumeSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name: "snap-1",
			UID:  "uid-snap-1",
		},
	}
	rv := &v1alpha1.ReplicatedVolume{
		Status: v1alpha1.ReplicatedVolumeStatus{
			Datamesh: v1alpha1.ReplicatedVolumeDatamesh{
				Members: []v1alpha1.DatameshMember{
					{Name: "rvr-0", NodeName: "node-a", Attached: true},
					{Name: "rvr-1", NodeName: "node-b", Attached: false},
				},
			},
		},
	}
	rvrs := []*v1alpha1.ReplicatedVolumeReplica{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-0"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				Type:     v1alpha1.ReplicaTypeDiskful,
				NodeName: "node-a",
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
			Spec: v1alpha1.ReplicatedVolumeReplicaSpec{
				Type:     v1alpha1.ReplicaTypeDiskful,
				NodeName: "node-b",
			},
		},
	}

	childRVRSs := make([]*v1alpha1.ReplicatedVolumeReplicaSnapshot, 0, len(objs))
	for _, obj := range objs {
		if obj == nil {
			continue
		}
		if rvrs, ok := obj.(*v1alpha1.ReplicatedVolumeReplicaSnapshot); ok {
			childRVRSs = append(childRVRSs, rvrs)
		}
	}

	return buildPrepareContexts(context.Background(), rvs, rv, rvrs, childRVRSs, cl, scheme)
}
