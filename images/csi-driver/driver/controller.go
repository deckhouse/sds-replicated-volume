/*
Copyright 2025 Flant JSC

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
	"errors"
	"fmt"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"

	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/csi-driver/internal"
	"github.com/deckhouse/sds-replicated-volume/images/csi-driver/pkg/utils"
)

const (
	ReplicatedStorageClassParamNameKey = "replicated.csi.storage.deckhouse.io/replicatedStorageClassName"
)

func (d *Driver) CreateVolume(ctx context.Context, request *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	traceID := uuid.New().String()

	d.log.Trace(fmt.Sprintf("[CreateVolume][traceID:%s] ========== CreateVolume ============", traceID))
	d.log.Trace(request.String())
	d.log.Trace(fmt.Sprintf("[CreateVolume][traceID:%s] ========== CreateVolume ============", traceID))

	if len(request.Name) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume Name cannot be empty")
	}
	volumeID := request.Name
	if request.VolumeCapabilities == nil {
		return nil, status.Error(codes.InvalidArgument, "Volume Capability cannot de empty")
	}

	var dataSource *srv.VolumeDataSource
	if cs := request.VolumeContentSource; cs != nil {
		switch s := cs.Type.(type) {
		case *csi.VolumeContentSource_Snapshot:
			dataSource = &srv.VolumeDataSource{
				Kind: srv.VolumeDataSourceKindReplicatedVolumeSnapshot,
				Name: s.Snapshot.SnapshotId,
			}
			d.log.Info(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] Creating volume from snapshot: %s", traceID, volumeID, s.Snapshot.SnapshotId))
		case *csi.VolumeContentSource_Volume:
			dataSource = &srv.VolumeDataSource{
				Kind: srv.VolumeDataSourceKindReplicatedVolume,
				Name: s.Volume.VolumeId,
			}
			d.log.Info(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] Creating volume from source volume: %s", traceID, volumeID, s.Volume.VolumeId))
		default:
			return nil, status.Error(codes.InvalidArgument, "unsupported VolumeContentSource type")
		}
	}

	rvSize := resource.NewQuantity(request.CapacityRange.GetRequiredBytes(), resource.BinarySI)
	d.log.Info(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] ReplicatedVolume size: %s", traceID, volumeID, rvSize.String()))

	preferredNode := ""
	if ar := request.AccessibilityRequirements; ar != nil {
		d.log.Info(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] AccessibilityRequirements: Requisite=%v, Preferred=%v", traceID, volumeID, ar.Requisite, ar.Preferred))
		for _, topo := range ar.Preferred {
			if node, ok := topo.Segments[internal.TopologyKey]; ok {
				preferredNode = node
				break
			}
		}
	}
	d.log.Info(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] Preferred node from AccessibilityRequirements: %q", traceID, volumeID, preferredNode))

	bindingMode := request.Parameters[internal.BindingModeKey]
	d.log.Info(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] volume-binding-mode: %q", traceID, volumeID, bindingMode))

	if bindingMode != internal.BindingModeWFFC && bindingMode != internal.BindingModeI {
		errMsg := fmt.Sprintf("StorageClass must set %q to %q or %q", internal.BindingModeKey, internal.BindingModeWFFC, internal.BindingModeI)
		err := status.Error(codes.InvalidArgument, errMsg)
		d.log.Error(err, fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] %s", traceID, volumeID, errMsg))
		return nil, err
	}

	createdEarlyRVA := false
	if bindingMode == internal.BindingModeWFFC && preferredNode != "" {
		d.log.Info(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s][node:%s] WFFC binding: creating early RVA for preferred node", traceID, volumeID, preferredNode))
		_, err := utils.EnsureRVA(ctx, d.cl, d.log, traceID, volumeID, preferredNode)
		if err != nil {
			d.log.Error(err, fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s][node:%s] Failed to create ReplicatedVolumeAttachment for WFFC-selected node", traceID, volumeID, preferredNode))
			return nil, status.Errorf(codes.Internal,
				"failed to create ReplicatedVolumeAttachment for volume %q on WFFC-selected node %q: %v",
				volumeID, preferredNode, err)
		}
		createdEarlyRVA = true
	}

	rvSpec := utils.BuildReplicatedVolumeSpec(
		*rvSize,
		request.Parameters[ReplicatedStorageClassParamNameKey],
	)

	rvSpec.DataSource = dataSource

	d.log.Info(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] ReplicatedVolumeSpec: %+v", traceID, volumeID, rvSpec))

	// Create ReplicatedVolume
	d.log.Trace(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] ------------ CreateReplicatedVolume start ------------", traceID, volumeID))
	pvcName := request.Parameters[internal.PVCAnnotationNameKey]
	pvcNamespace := request.Parameters[internal.PVCAnnotationNamespaceKey]
	_, err := utils.CreateReplicatedVolume(ctx, d.cl, d.log, traceID, volumeID, pvcName, pvcNamespace, rvSpec)
	if err != nil {
		if kerrors.IsAlreadyExists(err) {
			d.log.Info(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] ReplicatedVolume %s already exists. Skip creating", traceID, volumeID, volumeID))
		} else {
			d.log.Error(err, fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] error CreateReplicatedVolume", traceID, volumeID))
			if createdEarlyRVA {
				if rvaErr := utils.DeleteRVA(ctx, d.cl, d.log, traceID, volumeID, preferredNode); rvaErr != nil {
					d.log.Error(rvaErr, fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s][node:%s] error cleaning up WFFC RVA", traceID, volumeID, preferredNode))
				}
			}
			return nil, err
		}
	}
	d.log.Trace(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] ------------ CreateReplicatedVolume end ------------", traceID, volumeID))

	// Wait for ReplicatedVolume to become ready
	d.log.Trace(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] start wait ReplicatedVolume", traceID, volumeID))
	waitCtx, waitCancel := context.WithTimeout(ctx, d.waitActionTimeout)
	defer waitCancel()
	rv, attemptCounter, err := utils.WaitForReplicatedVolumeReady(waitCtx, d.cl, d.log, traceID, volumeID)
	if err != nil {
		d.log.Error(err, fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] error WaitForReplicatedVolumeReady. Delete ReplicatedVolume %s", traceID, volumeID, volumeID))

		deleteErr := utils.DeleteReplicatedVolume(ctx, d.cl, d.log, traceID, volumeID)
		if deleteErr != nil {
			d.log.Error(deleteErr, fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] error DeleteReplicatedVolume", traceID, volumeID))
		}
		if createdEarlyRVA {
			if rvaErr := utils.DeleteRVA(ctx, d.cl, d.log, traceID, volumeID, preferredNode); rvaErr != nil {
				d.log.Error(rvaErr, fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s][node:%s] error cleaning up WFFC RVA", traceID, volumeID, preferredNode))
			}
		}

		d.log.Error(err, fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] error creating ReplicatedVolume", traceID, volumeID))
		return nil, err
	}
	d.log.Trace(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] finish wait ReplicatedVolume, attempt counter = %d", traceID, volumeID, attemptCounter))

	capacityBytes := rvSize.Value()
	if rv.Status.Size != nil {
		capacityBytes = rv.Status.Size.Value()
	}

	volumeCtx := make(map[string]string, len(request.Parameters))
	for k, v := range request.Parameters {
		volumeCtx[k] = v
	}
	volumeCtx[internal.ReplicatedVolumeNameKey] = volumeID

	d.log.Info(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] Volume created successfully, capacity: %d. volumeCtx: %+v", traceID, volumeID, capacityBytes, volumeCtx))

	var contentSource *csi.VolumeContentSource
	if dataSource != nil {
		switch dataSource.Kind {
		case srv.VolumeDataSourceKindReplicatedVolumeSnapshot:
			contentSource = &csi.VolumeContentSource{
				Type: &csi.VolumeContentSource_Snapshot{
					Snapshot: &csi.VolumeContentSource_SnapshotSource{
						SnapshotId: dataSource.Name,
					},
				},
			}
		case srv.VolumeDataSourceKindReplicatedVolume:
			contentSource = &csi.VolumeContentSource{
				Type: &csi.VolumeContentSource_Volume{
					Volume: &csi.VolumeContentSource_VolumeSource{
						VolumeId: dataSource.Name,
					},
				},
			}
		}
	}

	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			CapacityBytes:      capacityBytes,
			VolumeId:           request.Name,
			VolumeContext:      volumeCtx,
			ContentSource:      contentSource,
			AccessibleTopology: nil,
		},
	}, nil
}

func (d *Driver) DeleteVolume(ctx context.Context, request *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	traceID := uuid.New().String()
	d.log.Info(fmt.Sprintf("[DeleteVolume][traceID:%s] ========== Start DeleteVolume ============", traceID))
	if len(request.VolumeId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID cannot be empty")
	}

	if err := utils.CheckNoRVAsExist(ctx, d.cl, d.log, traceID, request.VolumeId); err != nil {
		d.log.Error(err, fmt.Sprintf("[DeleteVolume][traceID:%s][volumeID:%s] cannot delete volume while RVAs exist", traceID, request.VolumeId))
		return nil, status.Errorf(codes.FailedPrecondition, "%v", err)
	}

	err := utils.DeleteReplicatedVolume(ctx, d.cl, d.log, traceID, request.VolumeId)
	if err != nil {
		d.log.Error(err, "error DeleteReplicatedVolume")
		return nil, err
	}

	deleteWaitCtx, deleteWaitCancel := context.WithTimeout(ctx, d.waitActionTimeout)
	defer deleteWaitCancel()
	attemptCounter, err := utils.WaitForReplicatedVolumeDeleted(deleteWaitCtx, d.cl, d.log, traceID, request.VolumeId)
	if err != nil {
		d.log.Error(err, fmt.Sprintf("[DeleteVolume][traceID:%s][volumeID:%s] error waiting for ReplicatedVolume deletion", traceID, request.VolumeId))
		return nil, err
	}
	d.log.Info(fmt.Sprintf("[DeleteVolume][traceID:%s][volumeID:%s] Volume deleted successfully, attempts: %d", traceID, request.VolumeId, attemptCounter))
	d.log.Info(fmt.Sprintf("[DeleteVolume][traceID:%s] ========== END DeleteVolume ============", traceID))
	return &csi.DeleteVolumeResponse{}, nil
}

func (d *Driver) ControllerPublishVolume(ctx context.Context, request *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	traceID := uuid.New().String()
	d.log.Info(fmt.Sprintf("[ControllerPublishVolume][traceID:%s] ========== ControllerPublishVolume ============", traceID))
	d.log.Trace(request.String())

	if request.VolumeId == "" {
		return nil, status.Error(codes.InvalidArgument, "Volume ID cannot be empty")
	}
	if request.NodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "Node ID cannot be empty")
	}

	volumeID := request.VolumeId
	nodeID := request.NodeId

	d.log.Info(fmt.Sprintf("[ControllerPublishVolume][traceID:%s][volumeID:%s][nodeID:%s] Creating ReplicatedVolumeAttachment and waiting for Ready=true", traceID, volumeID, nodeID))

	// EnsureRVA may block waiting for a stale RVA (from a just-finished
	// ControllerUnpublishVolume) to be fully finalized by rv-controller.
	// Bound that wait to waitActionTimeout so the gRPC call does not hang
	// if something is genuinely stuck: external-attacher will retry with backoff.
	ensureCtx, ensureCancel := context.WithTimeout(ctx, d.waitActionTimeout)
	_, err := utils.EnsureRVA(ensureCtx, d.cl, d.log, traceID, volumeID, nodeID)
	ensureCancel()
	if err != nil {
		d.log.Error(err, fmt.Sprintf("[ControllerPublishVolume][traceID:%s][volumeID:%s][nodeID:%s] Failed to create ReplicatedVolumeAttachment", traceID, volumeID, nodeID))
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, status.Errorf(codes.DeadlineExceeded, "Timed out ensuring ReplicatedVolumeAttachment: %v", err)
		}
		if errors.Is(err, context.Canceled) {
			return nil, status.Errorf(codes.Canceled, "Canceled ensuring ReplicatedVolumeAttachment: %v", err)
		}
		return nil, status.Errorf(codes.Internal, "Failed to create ReplicatedVolumeAttachment: %v", err)
	}

	publishWaitCtx, publishWaitCancel := context.WithTimeout(ctx, d.waitActionTimeout)
	defer publishWaitCancel()
	err = utils.WaitForRVAReady(publishWaitCtx, d.cl, d.log, traceID, volumeID, nodeID)
	if err != nil {
		d.log.Error(err, fmt.Sprintf("[ControllerPublishVolume][traceID:%s][volumeID:%s][nodeID:%s] Failed waiting for RVA Ready=true", traceID, volumeID, nodeID))
		// Preserve RVA reason/message for better user diagnostics.
		var waitErr *utils.RVAWaitError
		if errors.As(err, &waitErr) {
			// Permanent failures: waiting won't help (e.g. locality constraints).
			if waitErr.Permanent {
				return nil, status.Errorf(codes.FailedPrecondition, "ReplicatedVolumeAttachment not ready: %v", waitErr)
			}
			// Context-aware mapping (external-attacher controls ctx deadline).
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, status.Errorf(codes.DeadlineExceeded, "Timed out waiting for ReplicatedVolumeAttachment to become Ready=true: %v", waitErr)
			}
			if errors.Is(err, context.Canceled) {
				return nil, status.Errorf(codes.Canceled, "Canceled waiting for ReplicatedVolumeAttachment to become Ready=true: %v", waitErr)
			}
			return nil, status.Errorf(codes.Internal, "Failed waiting for ReplicatedVolumeAttachment Ready=true: %v", waitErr)
		}
		// Fallback for unexpected errors.
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, status.Errorf(codes.DeadlineExceeded, "Timed out waiting for ReplicatedVolumeAttachment to become Ready=true: %v", err)
		}
		if errors.Is(err, context.Canceled) {
			return nil, status.Errorf(codes.Canceled, "Canceled waiting for ReplicatedVolumeAttachment to become Ready=true: %v", err)
		}
		return nil, status.Errorf(codes.Internal, "Failed waiting for ReplicatedVolumeAttachment Ready=true: %v", err)
	}

	d.log.Info(fmt.Sprintf("[ControllerPublishVolume][traceID:%s][volumeID:%s][nodeID:%s] Volume attached successfully", traceID, volumeID, nodeID))
	d.log.Info(fmt.Sprintf("[ControllerPublishVolume][traceID:%s] ========== END ControllerPublishVolume ============", traceID))

	return &csi.ControllerPublishVolumeResponse{
		PublishContext: map[string]string{
			internal.ReplicatedVolumeNameKey: volumeID,
		},
	}, nil
}

func (d *Driver) ControllerUnpublishVolume(ctx context.Context, request *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	traceID := uuid.New().String()
	d.log.Info(fmt.Sprintf("[ControllerUnpublishVolume][traceID:%s] ========== ControllerUnpublishVolume ============", traceID))
	d.log.Trace(request.String())

	if request.VolumeId == "" {
		return nil, status.Error(codes.InvalidArgument, "Volume ID cannot be empty")
	}
	if request.NodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "Node ID cannot be empty")
	}

	volumeID := request.VolumeId
	nodeID := request.NodeId

	d.log.Info(fmt.Sprintf("[ControllerUnpublishVolume][traceID:%s][volumeID:%s][nodeID:%s] Deleting ReplicatedVolumeAttachment", traceID, volumeID, nodeID))

	err := utils.DeleteRVA(ctx, d.cl, d.log, traceID, volumeID, nodeID)
	if err != nil {
		d.log.Error(err, fmt.Sprintf("[ControllerUnpublishVolume][traceID:%s][volumeID:%s][nodeID:%s] Failed to delete ReplicatedVolumeAttachment", traceID, volumeID, nodeID))
		return nil, status.Errorf(codes.Internal, "Failed to delete ReplicatedVolumeAttachment: %v", err)
	}

	// Wait for the RVA object to be fully gone before returning OK.
	// rv.status.actuallyAttachedTo is no longer populated by the controller
	// (see api/v1alpha1/rv_types.go: ActuallyAttachedTo TODO), so we rely
	// solely on the RVA lifecycle here. Returning OK while the RVA still
	// carries the rv-controller finalizer would let external-attacher delete
	// the VolumeAttachment while the next ControllerPublishVolume still
	// observes a stale RVA with DeletionTimestamp, leading to
	// "volume attachment is being deleted" / attach timeouts on rapidly
	// cycling pods.
	d.log.Info(fmt.Sprintf("[ControllerUnpublishVolume][traceID:%s][volumeID:%s][nodeID:%s] Waiting for ReplicatedVolumeAttachment object to be fully deleted", traceID, volumeID, nodeID))
	rvaDelCtx, rvaDelCancel := context.WithTimeout(ctx, d.waitActionTimeout)
	defer rvaDelCancel()
	if err := utils.WaitForRVADeleted(rvaDelCtx, d.cl, d.log, traceID, volumeID, nodeID); err != nil {
		d.log.Error(err, fmt.Sprintf("[ControllerUnpublishVolume][traceID:%s][volumeID:%s][nodeID:%s] Failed to wait for RVA deletion", traceID, volumeID, nodeID))
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, status.Errorf(codes.DeadlineExceeded, "Timed out waiting for RVA deletion: %v", err)
		}
		return nil, status.Errorf(codes.Internal, "Failed to wait for RVA deletion: %v", err)
	}

	d.log.Info(fmt.Sprintf("[ControllerUnpublishVolume][traceID:%s][volumeID:%s][nodeID:%s] Volume detached successfully", traceID, volumeID, nodeID))
	d.log.Info(fmt.Sprintf("[ControllerUnpublishVolume][traceID:%s] ========== END ControllerUnpublishVolume ============", traceID))

	return &csi.ControllerUnpublishVolumeResponse{}, nil
}

func (d *Driver) ValidateVolumeCapabilities(_ context.Context, _ *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	d.log.Info("call method ValidateVolumeCapabilities")
	return nil, nil
}

func (d *Driver) ListVolumes(_ context.Context, _ *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	d.log.Info("call method ListVolumes")
	return nil, nil
}

func (d *Driver) GetCapacity(_ context.Context, _ *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error) {
	d.log.Info("method GetCapacity")

	// Return maximum int64 value to indicate unlimited capacity
	// This prevents Kubernetes scheduler from rejecting pods due to insufficient storage
	// Real capacity validation happens during volume creation
	// Note: CSIDriver has storageCapacity: false, but external-provisioner may still call this method
	return &csi.GetCapacityResponse{
		AvailableCapacity: int64(^uint64(0) >> 1), // Max int64: ~9.2 exabytes
		MaximumVolumeSize: nil,
		MinimumVolumeSize: nil,
	}, nil
}

func (d *Driver) ControllerGetCapabilities(_ context.Context, _ *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {
	d.log.Info("method ControllerGetCapabilities")

	var capabilities = []csi.ControllerServiceCapability_RPC_Type{
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
		csi.ControllerServiceCapability_RPC_GET_CAPACITY,
		csi.ControllerServiceCapability_RPC_EXPAND_VOLUME,
		csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME,
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_SNAPSHOT,
		csi.ControllerServiceCapability_RPC_CLONE_VOLUME,
	}

	csiCaps := make([]*csi.ControllerServiceCapability, len(capabilities))

	for i, capability := range capabilities {
		csiCaps[i] = &csi.ControllerServiceCapability{
			Type: &csi.ControllerServiceCapability_Rpc{
				Rpc: &csi.ControllerServiceCapability_RPC{
					Type: capability,
				},
			},
		}
	}

	return &csi.ControllerGetCapabilitiesResponse{
		Capabilities: csiCaps,
	}, nil
}

func (d *Driver) ControllerExpandVolume(ctx context.Context, request *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	traceID := uuid.New().String()

	d.log.Info(fmt.Sprintf("[ControllerExpandVolume][traceID:%s] method ControllerExpandVolume", traceID))
	d.log.Trace(fmt.Sprintf("[ControllerExpandVolume][traceID:%s] ========== ControllerExpandVolume ============", traceID))
	d.log.Trace(request.String())
	d.log.Trace(fmt.Sprintf("[ControllerExpandVolume][traceID:%s] ========== ControllerExpandVolume ============", traceID))

	volumeID := request.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume id cannot be empty")
	}

	rv, err := utils.GetReplicatedVolume(ctx, d.cl, volumeID)
	if err != nil {
		d.log.Error(err, fmt.Sprintf("[ControllerExpandVolume][traceID:%s][volumeID:%s] error getting ReplicatedVolume", traceID, volumeID))
		return nil, status.Errorf(codes.Internal, "error getting ReplicatedVolume: %s", err.Error())
	}

	requestCapacity := resource.NewQuantity(request.CapacityRange.GetRequiredBytes(), resource.BinarySI)
	d.log.Trace(fmt.Sprintf("[ControllerExpandVolume][traceID:%s][volumeID:%s] requestCapacity: %s", traceID, volumeID, requestCapacity.String()))

	nodeExpansionRequired := request.GetVolumeCapability().GetBlock() == nil
	d.log.Info(fmt.Sprintf("[ControllerExpandVolume][traceID:%s][volumeID:%s] NodeExpansionRequired: %t", traceID, volumeID, nodeExpansionRequired))

	if requestCapacity.Cmp(rv.Spec.Size) > 0 {
		d.log.Info(fmt.Sprintf("[ControllerExpandVolume][traceID:%s][volumeID:%s] Updating spec.size from %s to %s", traceID, volumeID, rv.Spec.Size.String(), requestCapacity.String()))
		err = utils.ExpandReplicatedVolume(ctx, d.cl, rv, *requestCapacity)
		if err != nil {
			d.log.Error(err, fmt.Sprintf("[ControllerExpandVolume][traceID:%s][volumeID:%s] error updating ReplicatedVolume", traceID, volumeID))
			return nil, status.Errorf(codes.Internal, "error updating ReplicatedVolume: %v", err)
		}
	}

	if rv.Status.Size != nil && rv.Status.Size.Cmp(*requestCapacity) >= 0 {
		d.log.Info(fmt.Sprintf("[ControllerExpandVolume][traceID:%s][volumeID:%s] status.size %s already >= requested %s, no resize needed", traceID, volumeID, rv.Status.Size.String(), requestCapacity.String()))
		return &csi.ControllerExpandVolumeResponse{
			CapacityBytes:         rv.Status.Size.Value(),
			NodeExpansionRequired: nodeExpansionRequired,
		}, nil
	}

	waitCtx, waitCancel := context.WithTimeout(ctx, d.waitActionTimeout)
	defer waitCancel()

	var attemptCounter int
	rv, attemptCounter, err = utils.WaitForReplicatedVolumeResized(waitCtx, d.cl, d.log, traceID, volumeID, *requestCapacity)
	if err != nil {
		d.log.Error(err, fmt.Sprintf("[ControllerExpandVolume][traceID:%s][volumeID:%s] error WaitForReplicatedVolumeResized", traceID, volumeID))
		return nil, status.Errorf(codes.Internal, "error waiting for resize: %v", err)
	}
	d.log.Info(fmt.Sprintf("[ControllerExpandVolume][traceID:%s][volumeID:%s] Resize complete after %d attempts", traceID, volumeID, attemptCounter))

	capacityBytes := rv.Status.Size.Value()
	d.log.Info(fmt.Sprintf("[ControllerExpandVolume][traceID:%s][volumeID:%s] Volume expanded successfully, reporting capacity: %d", traceID, volumeID, capacityBytes))

	return &csi.ControllerExpandVolumeResponse{
		CapacityBytes:         capacityBytes,
		NodeExpansionRequired: nodeExpansionRequired,
	}, nil
}

func (d *Driver) CreateSnapshot(ctx context.Context, request *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	traceID := uuid.New().String()
	d.log.Info(fmt.Sprintf("[CreateSnapshot][traceID:%s] ========== CreateSnapshot ============", traceID))

	if request.SourceVolumeId == "" {
		return nil, status.Error(codes.InvalidArgument, "SourceVolumeId cannot be empty")
	}
	if request.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "Name cannot be empty")
	}

	snapshotID := request.Name
	sourceVolumeID := request.SourceVolumeId

	d.log.Info(fmt.Sprintf("[CreateSnapshot][traceID:%s][snapshotID:%s] source volume: %s", traceID, snapshotID, sourceVolumeID))

	rv, err := utils.GetReplicatedVolume(ctx, d.cl, sourceVolumeID)
	if err != nil {
		if kerrors.IsNotFound(err) {
			return nil, status.Errorf(codes.NotFound, "source volume %s not found", sourceVolumeID)
		}
		return nil, status.Errorf(codes.Internal, "failed to get source volume: %v", err)
	}

	_, err = utils.CreateReplicatedVolumeSnapshot(ctx, d.cl, d.log, traceID, snapshotID, sourceVolumeID)
	if err != nil {
		if kerrors.IsAlreadyExists(err) {
			d.log.Info(fmt.Sprintf("[CreateSnapshot][traceID:%s][snapshotID:%s] RVS already exists", traceID, snapshotID))
		} else {
			return nil, status.Errorf(codes.Internal, "failed to create snapshot: %v", err)
		}
	}

	rvs, err := utils.WaitForReplicatedVolumeSnapshotReady(ctx, d.cl, d.log, traceID, snapshotID)
	if err != nil {
		d.log.Error(err, fmt.Sprintf("[CreateSnapshot][traceID:%s][snapshotID:%s] failed waiting for snapshot ready", traceID, snapshotID))
		return nil, status.Errorf(codes.Internal, "failed waiting for snapshot to become ready: %v", err)
	}

	creationTime := rvs.CreationTimestamp.UnixNano()
	if rvs.Status.CreationTime != nil {
		creationTime = rvs.Status.CreationTime.UnixNano()
	}

	d.log.Info(fmt.Sprintf("[CreateSnapshot][traceID:%s][snapshotID:%s] Snapshot created successfully", traceID, snapshotID))

	return &csi.CreateSnapshotResponse{
		Snapshot: &csi.Snapshot{
			SnapshotId:     snapshotID,
			SourceVolumeId: sourceVolumeID,
			CreationTime:   timestamppbFromNanos(creationTime),
			SizeBytes:      rv.Spec.Size.Value(),
			ReadyToUse:     rvs.Status.ReadyToUse,
		},
	}, nil
}

func (d *Driver) DeleteSnapshot(ctx context.Context, request *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	traceID := uuid.New().String()
	d.log.Info(fmt.Sprintf("[DeleteSnapshot][traceID:%s] ========== DeleteSnapshot ============", traceID))

	if request.SnapshotId == "" {
		return nil, status.Error(codes.InvalidArgument, "SnapshotId cannot be empty")
	}

	snapshotID := request.SnapshotId
	d.log.Info(fmt.Sprintf("[DeleteSnapshot][traceID:%s][snapshotID:%s] Deleting snapshot", traceID, snapshotID))

	err := utils.DeleteReplicatedVolumeSnapshot(ctx, d.cl, d.log, traceID, snapshotID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to delete snapshot: %v", err)
	}

	err = utils.WaitForReplicatedVolumeSnapshotDeleted(ctx, d.cl, d.log, traceID, snapshotID)
	if err != nil {
		d.log.Error(err, fmt.Sprintf("[DeleteSnapshot][traceID:%s][snapshotID:%s] failed waiting for snapshot deletion", traceID, snapshotID))
		return nil, status.Errorf(codes.Internal, "failed waiting for snapshot deletion: %v", err)
	}

	d.log.Info(fmt.Sprintf("[DeleteSnapshot][traceID:%s][snapshotID:%s] Snapshot deleted successfully", traceID, snapshotID))
	return &csi.DeleteSnapshotResponse{}, nil
}

func (d *Driver) ControllerGetVolume(_ context.Context, _ *csi.ControllerGetVolumeRequest) (*csi.ControllerGetVolumeResponse, error) {
	d.log.Info(" call method ControllerGetVolume")
	return &csi.ControllerGetVolumeResponse{}, nil
}

func (d *Driver) ControllerModifyVolume(_ context.Context, _ *csi.ControllerModifyVolumeRequest) (*csi.ControllerModifyVolumeResponse, error) {
	d.log.Info(" call method ControllerModifyVolume")
	return &csi.ControllerModifyVolumeResponse{}, nil
}

func timestamppbFromNanos(nanos int64) *timestamppb.Timestamp {
	return timestamppb.New(time.Unix(0, nanos))
}
