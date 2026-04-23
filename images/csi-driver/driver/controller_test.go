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

package driver //nolint:revive

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/csi-driver/internal"
	"github.com/deckhouse/sds-replicated-volume/images/csi-driver/pkg/utils"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/logger"
)

const testStuckFinalizer = "test.deckhouse.io/stuck"

func newTestDriver(t *testing.T, cl client.Client, timeout time.Duration) *Driver {
	t.Helper()
	log, err := logger.NewLogger("3")
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	return &Driver{
		cl:                cl,
		log:               log,
		waitActionTimeout: timeout,
		inFlight:          internal.NewInFlight(),
	}
}

func newTestFakeClient() client.Client {
	s := scheme.Scheme
	_ = metav1.AddMetaToScheme(s)
	_ = v1alpha1.AddToScheme(s)
	return fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&v1alpha1.ReplicatedVolumeAttachment{}, &v1alpha1.ReplicatedVolume{}).
		Build()
}

// Sanity-check that ControllerUnpublishVolume does not return OK until the RVA
// object itself is fully deleted (simulates rv-controller holding its own
// finalizer for a while after DeleteRVA is issued).
func TestControllerUnpublishVolume_WaitsForRVADeletion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	volumeID := "vol-x"
	nodeID := "node-1"
	rvaName := utils.BuildRVAName(volumeID, nodeID)

	cl := newTestFakeClient()

	// RVA that still carries a non-CSI finalizer (mimics rv-controller finalizer).
	rva := &v1alpha1.ReplicatedVolumeAttachment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       rvaName,
			Finalizers: []string{testStuckFinalizer, utils.SDSReplicatedVolumeCSIFinalizer},
		},
		Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
			ReplicatedVolumeName: volumeID,
			NodeName:             nodeID,
		},
	}
	if err := cl.Create(ctx, rva); err != nil {
		t.Fatalf("create rva: %v", err)
	}

	d := newTestDriver(t, cl, 5*time.Second)

	// Remove the stuck finalizer after a short delay so the RVA can actually
	// disappear. Without this, ControllerUnpublishVolume should eventually
	// time out instead of returning OK.
	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(200 * time.Millisecond)
		cur := &v1alpha1.ReplicatedVolumeAttachment{}
		if err := cl.Get(ctx, client.ObjectKey{Name: rvaName}, cur); err != nil {
			t.Errorf("unexpected get err: %v", err)
			return
		}
		cur.Finalizers = nil
		if err := cl.Update(ctx, cur); err != nil {
			t.Errorf("unexpected update err: %v", err)
		}
	}()

	start := time.Now()
	_, err := d.ControllerUnpublishVolume(ctx, &csi.ControllerUnpublishVolumeRequest{
		VolumeId: volumeID,
		NodeId:   nodeID,
	})
	<-done
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("ControllerUnpublishVolume: unexpected error: %v", err)
	}
	if elapsed < 150*time.Millisecond {
		t.Fatalf("ControllerUnpublishVolume returned too fast (%s); expected to wait for RVA deletion", elapsed)
	}

	got := &v1alpha1.ReplicatedVolumeAttachment{}
	if err := cl.Get(ctx, client.ObjectKey{Name: rvaName}, got); err == nil {
		t.Fatalf("RVA %s still present after ControllerUnpublishVolume", rvaName)
	}
}

// When the rv-controller finalizer is never removed, ControllerUnpublishVolume
// must surface DeadlineExceeded rather than returning OK too early or hanging.
func TestControllerUnpublishVolume_ReturnsDeadlineExceededIfRVAStuck(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	volumeID := "vol-x"
	nodeID := "node-1"
	rvaName := utils.BuildRVAName(volumeID, nodeID)

	cl := newTestFakeClient()

	rva := &v1alpha1.ReplicatedVolumeAttachment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       rvaName,
			Finalizers: []string{testStuckFinalizer, utils.SDSReplicatedVolumeCSIFinalizer},
		},
		Spec: v1alpha1.ReplicatedVolumeAttachmentSpec{
			ReplicatedVolumeName: volumeID,
			NodeName:             nodeID,
		},
	}
	if err := cl.Create(ctx, rva); err != nil {
		t.Fatalf("create rva: %v", err)
	}

	d := newTestDriver(t, cl, 300*time.Millisecond)

	_, err := d.ControllerUnpublishVolume(ctx, &csi.ControllerUnpublishVolumeRequest{
		VolumeId: volumeID,
		NodeId:   nodeID,
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}
	if st.Code() != codes.DeadlineExceeded {
		t.Fatalf("expected codes.DeadlineExceeded, got %s: %v", st.Code(), err)
	}
}

// --- CreateVolume scenarios ---------------------------------------------------

// newCreateVolumeRequest builds a minimal valid CSI CreateVolumeRequest.
func newCreateVolumeRequest(volumeID string) *csi.CreateVolumeRequest {
	return &csi.CreateVolumeRequest{
		Name: volumeID,
		CapacityRange: &csi.CapacityRange{
			RequiredBytes: 1 << 30,
		},
		VolumeCapabilities: []*csi.VolumeCapability{
			{
				AccessType: &csi.VolumeCapability_Mount{Mount: &csi.VolumeCapability_MountVolume{}},
				AccessMode: &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER},
			},
		},
		Parameters: map[string]string{
			internal.BindingModeKey:            internal.BindingModeI,
			ReplicatedStorageClassParamNameKey: "rsc-x",
		},
	}
}

// createRVPastFormation pre-creates an RV and marks its status as past Formation.
func createRVPastFormation(t *testing.T, ctx context.Context, cl client.Client, volumeID string) *v1alpha1.ReplicatedVolume {
	t.Helper()
	rv := &v1alpha1.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name:       volumeID,
			Finalizers: []string{utils.SDSReplicatedVolumeCSIFinalizer},
		},
	}
	if err := cl.Create(ctx, rv); err != nil {
		t.Fatalf("create RV: %v", err)
	}
	rv.Status.DatameshRevision = 1
	if err := cl.Status().Update(ctx, rv); err != nil {
		t.Fatalf("set RV status: %v", err)
	}
	return rv
}

// createRVStillForming pre-creates an RV with empty status so the predicate
// IsReplicatedVolumePastFormation is false for it.
func createRVStillForming(t *testing.T, ctx context.Context, cl client.Client, volumeID string) *v1alpha1.ReplicatedVolume {
	t.Helper()
	rv := &v1alpha1.ReplicatedVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name:       volumeID,
			Finalizers: []string{utils.SDSReplicatedVolumeCSIFinalizer},
		},
	}
	if err := cl.Create(ctx, rv); err != nil {
		t.Fatalf("create RV: %v", err)
	}
	return rv
}

// formRVAsync marks the RV past Formation after a delay, letting the Wait loop observe it.
func formRVAsync(t *testing.T, ctx context.Context, cl client.Client, volumeID string, after time.Duration) chan struct{} {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		timer := time.NewTimer(after)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		cur := &v1alpha1.ReplicatedVolume{}
		for i := 0; i < 50; i++ {
			if err := cl.Get(ctx, client.ObjectKey{Name: volumeID}, cur); err != nil {
				time.Sleep(20 * time.Millisecond)
				continue
			}
			cur.Status.DatameshRevision = 1
			if err := cl.Status().Update(ctx, cur); err == nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	return done
}

// TestCreateVolume_AlreadyExists_PastFormation_ReturnsSuccessWithoutWait
// ensures that CreateVolume returns Success immediately when the RV is
// already past Formation, without entering the Wait loop.
func TestCreateVolume_AlreadyExists_PastFormation_ReturnsSuccessWithoutWait(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	volumeID := "rv-already-formed"
	cl := newTestFakeClient()
	createRVPastFormation(t, ctx, cl, volumeID)

	// waitActionTimeout is intentionally large. If we accidentally enter Wait,
	// we still expect Wait to finish quickly because DatameshRevision>0 already,
	// so we additionally assert that the call returns fast.
	d := newTestDriver(t, cl, 5*time.Second)

	start := time.Now()
	resp, err := d.CreateVolume(ctx, newCreateVolumeRequest(volumeID))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("CreateVolume: unexpected error: %v", err)
	}
	if resp == nil || resp.Volume == nil || resp.Volume.VolumeId != volumeID {
		t.Fatalf("CreateVolume: unexpected response: %+v", resp)
	}
	// Wait's polling interval is 500ms; a fast path avoids it entirely.
	if elapsed > 400*time.Millisecond {
		t.Fatalf("CreateVolume took too long (%s); expected fast path without Wait", elapsed)
	}
}

// TestCreateVolume_AlreadyExists_StillForming_WaitSucceeds ensures that a
// pre-existing RV that is still forming makes CreateVolume fall through into
// the common Wait block, and Success is returned once Formation completes.
func TestCreateVolume_AlreadyExists_StillForming_WaitSucceeds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	volumeID := "rv-forming-ok"
	cl := newTestFakeClient()
	createRVStillForming(t, ctx, cl, volumeID)

	d := newTestDriver(t, cl, 5*time.Second)

	done := formRVAsync(t, ctx, cl, volumeID, 300*time.Millisecond)

	resp, err := d.CreateVolume(ctx, newCreateVolumeRequest(volumeID))
	<-done
	if err != nil {
		t.Fatalf("CreateVolume: unexpected error: %v", err)
	}
	if resp == nil || resp.Volume == nil || resp.Volume.VolumeId != volumeID {
		t.Fatalf("CreateVolume: unexpected response: %+v", resp)
	}
}

// TestCreateVolume_AlreadyExists_StillForming_WaitFails_DeletesGarbage ensures
// that when we fall through into Wait for a pre-existing, still-forming RV and
// Wait fails, the RV is garbage-collected. A not-past-Formation RV carries no
// committed data and can be safely recreated on the next CreateVolume retry.
func TestCreateVolume_AlreadyExists_StillForming_WaitFails_DeletesGarbage(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	volumeID := "rv-forming-stuck"
	cl := newTestFakeClient()
	createRVStillForming(t, ctx, cl, volumeID)

	d := newTestDriver(t, cl, 200*time.Millisecond)

	_, err := d.CreateVolume(ctx, newCreateVolumeRequest(volumeID))
	if err == nil {
		t.Fatalf("CreateVolume: expected error, got nil")
	}

	got := &v1alpha1.ReplicatedVolume{}
	gerr := cl.Get(ctx, client.ObjectKey{Name: volumeID}, got)
	if gerr == nil {
		t.Fatalf("expected pre-existing still-forming RV to be deleted as garbage, but it still exists: %+v", got)
	}
}

// TestCreateVolume_AlreadyExists_BeingDeleted_ReturnsAborted ensures that
// CreateVolume returns codes.Aborted when the existing RV has a DeletionTimestamp.
func TestCreateVolume_AlreadyExists_BeingDeleted_ReturnsAborted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	volumeID := "rv-being-deleted"
	cl := newTestFakeClient()
	rv := createRVStillForming(t, ctx, cl, volumeID)
	// Delete; fake client keeps the object around (finalizer) with DeletionTimestamp.
	if err := cl.Delete(ctx, rv); err != nil {
		t.Fatalf("delete rv: %v", err)
	}

	d := newTestDriver(t, cl, 200*time.Millisecond)

	_, err := d.CreateVolume(ctx, newCreateVolumeRequest(volumeID))
	if err == nil {
		t.Fatalf("CreateVolume: expected error, got nil")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}
	if st.Code() != codes.Aborted {
		t.Fatalf("expected codes.Aborted, got %s: %v", st.Code(), err)
	}
}

// TestCreateVolume_FreshCreate_WaitSucceeds_ReturnsResponse verifies the
// happy path: CreateVolume creates a new RV and returns Success after the
// RV becomes past Formation.
func TestCreateVolume_FreshCreate_WaitSucceeds_ReturnsResponse(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	volumeID := "rv-fresh-ok"
	cl := newTestFakeClient()

	d := newTestDriver(t, cl, 5*time.Second)

	done := formRVAsync(t, ctx, cl, volumeID, 300*time.Millisecond)

	resp, err := d.CreateVolume(ctx, newCreateVolumeRequest(volumeID))
	<-done
	if err != nil {
		t.Fatalf("CreateVolume: unexpected error: %v", err)
	}
	if resp == nil || resp.Volume == nil || resp.Volume.VolumeId != volumeID {
		t.Fatalf("CreateVolume: unexpected response: %+v", resp)
	}
}

// TestCreateVolume_FreshCreate_WaitFails_NotPastFormation_DeletesGarbage
// ensures that when CreateVolume creates the RV in this RPC and Wait fails,
// the RV is garbage-collected because it has not been past Formation.
func TestCreateVolume_FreshCreate_WaitFails_NotPastFormation_DeletesGarbage(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	volumeID := "rv-fresh-garbage"
	cl := newTestFakeClient()

	d := newTestDriver(t, cl, 150*time.Millisecond)

	_, err := d.CreateVolume(ctx, newCreateVolumeRequest(volumeID))
	if err == nil {
		t.Fatalf("CreateVolume: expected error, got nil")
	}

	got := &v1alpha1.ReplicatedVolume{}
	gerr := cl.Get(ctx, client.ObjectKey{Name: volumeID}, got)
	if gerr == nil {
		// DeleteReplicatedVolume removes the finalizer and calls Delete; on the
		// fake client with no other finalizers the object is fully gone.
		t.Fatalf("expected RV to be deleted, but it still exists: %+v", got)
	}
}

// TestCreateVolume_WaitFails_RaceWin_ReturnsSuccess verifies that when Wait
// returns an error but rv-controller actually committed Formation between
// Wait's last poll and the post-Wait re-observe, CreateVolume returns Success
// and does NOT delete the RV. Deterministically simulated via a Get
// interceptor that flips the observed RV to past-Formation on the second Get.
func TestCreateVolume_WaitFails_RaceWin_ReturnsSuccess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	volumeID := "rv-race-win"
	s := scheme.Scheme
	_ = metav1.AddMetaToScheme(s)
	_ = v1alpha1.AddToScheme(s)

	var rvGetCount atomic.Int32
	cl := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&v1alpha1.ReplicatedVolumeAttachment{}, &v1alpha1.ReplicatedVolume{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if err := c.Get(ctx, key, obj, opts...); err != nil {
					return err
				}
				// 1st Get of this RV: AlreadyExists re-observe (still-forming).
				// 2nd+ Gets: post-Wait re-observe - flip to past-Formation.
				if rv, ok := obj.(*v1alpha1.ReplicatedVolume); ok && key.Name == volumeID {
					if rvGetCount.Add(1) >= 2 {
						rv.Status.DatameshRevision = 1
					}
				}
				return nil
			},
		}).
		Build()

	createRVStillForming(t, ctx, cl, volumeID)

	// waitActionTimeout=1ns forces Wait to return ctx.Err immediately in its
	// first select iteration, without ever Get-ing the RV itself (otherwise
	// Wait would see still-forming and loop until deadline).
	d := newTestDriver(t, cl, 1*time.Nanosecond)
	resp, err := d.CreateVolume(ctx, newCreateVolumeRequest(volumeID))
	if err != nil {
		t.Fatalf("CreateVolume: expected success on race-win, got %v", err)
	}
	if resp == nil || resp.Volume == nil || resp.Volume.VolumeId != volumeID {
		t.Fatalf("CreateVolume: unexpected response: %+v", resp)
	}

	got := &v1alpha1.ReplicatedVolume{}
	if gerr := cl.Get(ctx, client.ObjectKey{Name: volumeID}, got); gerr != nil {
		t.Fatalf("race-winning RV must remain, got error: %v", gerr)
	}
	if got.DeletionTimestamp != nil {
		t.Fatalf("race-winning RV must not be deleted, but has DeletionTimestamp=%v", got.DeletionTimestamp)
	}
}

// TestCreateVolume_FreshCreate_WaitFails_DeletionTimestamp_DoesNotDelete
// ensures that if a racing actor issues Delete on the fresh RV during Wait,
// CreateVolume observes the DeletionTimestamp and skips its own Delete.
func TestCreateVolume_FreshCreate_WaitFails_DeletionTimestamp_DoesNotDelete(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	volumeID := "rv-fresh-race-delete"
	cl := newTestFakeClient()

	d := newTestDriver(t, cl, 5*time.Second)

	// Attach a non-CSI finalizer shortly after Create, then issue Delete.
	// The extra finalizer keeps the object around with a DeletionTimestamp so
	// the post-Wait guard can observe it.
	done := make(chan struct{})
	go func() {
		defer close(done)
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			cur := &v1alpha1.ReplicatedVolume{}
			if err := cl.Get(ctx, client.ObjectKey{Name: volumeID}, cur); err == nil {
				cur.Finalizers = append(cur.Finalizers, testStuckFinalizer)
				if err := cl.Update(ctx, cur); err == nil {
					_ = cl.Delete(ctx, cur)
					return
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()

	_, err := d.CreateVolume(ctx, newCreateVolumeRequest(volumeID))
	<-done
	if err == nil {
		t.Fatalf("CreateVolume: expected error, got nil")
	}

	got := &v1alpha1.ReplicatedVolume{}
	if gerr := cl.Get(ctx, client.ObjectKey{Name: volumeID}, got); gerr != nil {
		t.Fatalf("expected RV to remain (guard by DeletionTimestamp), got error: %v", gerr)
	}
	if got.DeletionTimestamp == nil {
		t.Fatalf("expected RV to be in deleting state, got: %+v", got)
	}
	// CreateVolume must NOT have stripped the CSI finalizer as part of a Delete.
	csiFinalizerFound := false
	for _, f := range got.Finalizers {
		if f == utils.SDSReplicatedVolumeCSIFinalizer {
			csiFinalizerFound = true
			break
		}
	}
	if !csiFinalizerFound {
		t.Fatalf("CSI finalizer must be preserved when CreateVolume skips Delete, finalizers=%v", got.Finalizers)
	}
}
