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
	"testing"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

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
