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

package driver

import (
	"context"
	"errors"
	"fmt"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"

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
		return nil, status.Error(codes.InvalidArgument, "Volume Capability cannot de empty!")
	}

	BindingMode := request.Parameters[internal.BindingModeKey]
	d.log.Info(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] storage class BindingMode: %s", traceID, volumeID, BindingMode))

	// Get LVMVolumeGroups from StoragePool
	storagePoolName := request.Parameters[internal.StoragePoolKey]
	if len(storagePoolName) == 0 {
		err := errors.New("no StoragePool specified in a storage class's parameters")
		d.log.Error(err, fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] no StoragePool was found for the request: %+v", traceID, volumeID, request))
		return nil, status.Errorf(codes.InvalidArgument, "no StoragePool specified in a storage class's parameters")
	}

	d.log.Info(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] using StoragePool: %s", traceID, volumeID, storagePoolName))
	storagePoolInfo, err := utils.GetStoragePoolInfo(ctx, d.cl, d.log, storagePoolName)
	if err != nil {
		d.log.Error(err, fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] error GetStoragePoolInfo", traceID, volumeID))
		return nil, status.Errorf(codes.Internal, "error during GetStoragePoolInfo: %v", err)
	}

	LvmType := storagePoolInfo.LVMType
	if LvmType != internal.LVMTypeThin && LvmType != internal.LVMTypeThick {
		d.log.Warning(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] Unknown LVM type from StoragePool: %s, defaulting to Thick", traceID, volumeID, LvmType))
		LvmType = internal.LVMTypeThick
	}
	d.log.Info(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] LVM type from StoragePool: %s", traceID, volumeID, LvmType))

	rvSize := resource.NewQuantity(request.CapacityRange.GetRequiredBytes(), resource.BinarySI)
	d.log.Info(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] ReplicatedVolume size: %s", traceID, volumeID, rvSize.String()))

	// Extract preferred node from AccessibilityRequirements for WaitForFirstConsumer
	// Kubernetes provides the selected node in AccessibilityRequirements.Preferred[].Segments
	// with key "kubernetes.io/hostname"
	// NOTE: We no longer use rv.spec.attachTo. Attachment intent is expressed via ReplicatedVolumeAttachment (RVA)
	// created in ControllerPublishVolume.

	// Build ReplicatedVolumeSpec
	rvSpec := utils.BuildReplicatedVolumeSpec(
		*rvSize,
		request.Parameters[ReplicatedStorageClassParamNameKey],
	)

	d.log.Info(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] ReplicatedVolumeSpec: %+v", traceID, volumeID, rvSpec))

	// Create ReplicatedVolume
	d.log.Trace(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] ------------ CreateReplicatedVolume start ------------", traceID, volumeID))
	_, err = utils.CreateReplicatedVolume(ctx, d.cl, d.log, traceID, volumeID, rvSpec)
	if err != nil {
		if kerrors.IsAlreadyExists(err) {
			d.log.Info(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] ReplicatedVolume %s already exists. Skip creating", traceID, volumeID, volumeID))
		} else {
			d.log.Error(err, fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] error CreateReplicatedVolume", traceID, volumeID))
			return nil, err
		}
	}
	d.log.Trace(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] ------------ CreateReplicatedVolume end ------------", traceID, volumeID))

	// Wait for ReplicatedVolume to become ready
	d.log.Trace(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] start wait ReplicatedVolume", traceID, volumeID))
	attemptCounter, err := utils.WaitForReplicatedVolumeReady(ctx, d.cl, d.log, traceID, volumeID)
	if err != nil {
		d.log.Error(err, fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] error WaitForReplicatedVolumeReady. Delete ReplicatedVolume %s", traceID, volumeID, volumeID))

		deleteErr := utils.DeleteReplicatedVolume(ctx, d.cl, d.log, traceID, volumeID)
		if deleteErr != nil {
			d.log.Error(deleteErr, fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] error DeleteReplicatedVolume", traceID, volumeID))
		}

		d.log.Error(err, fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] error creating ReplicatedVolume", traceID, volumeID))
		return nil, err
	}
	d.log.Trace(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] finish wait ReplicatedVolume, attempt counter = %d", traceID, volumeID, attemptCounter))

	// Build volume context
	volumeCtx := make(map[string]string, len(request.Parameters))
	for k, v := range request.Parameters {
		volumeCtx[k] = v
	}
	volumeCtx[internal.ReplicatedVolumeNameKey] = volumeID

	d.log.Info(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] Volume created successfully. volumeCtx: %+v", traceID, volumeID, volumeCtx))

	// Don't set AccessibleTopology - let scheduler-extender handle pod scheduling

	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			CapacityBytes:      request.CapacityRange.GetRequiredBytes(),
			VolumeId:           request.Name,
			VolumeContext:      volumeCtx,
			ContentSource:      request.VolumeContentSource,
			AccessibleTopology: nil, // No nodeAffinity - scheduling handled by scheduler-extender
		},
	}, nil
}

func (d *Driver) DeleteVolume(ctx context.Context, request *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	traceID := uuid.New().String()
	d.log.Info(fmt.Sprintf("[DeleteVolume][traceID:%s] ========== Start DeleteVolume ============", traceID))
	if len(request.VolumeId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID cannot be empty")
	}

	err := utils.DeleteReplicatedVolume(ctx, d.cl, d.log, traceID, request.VolumeId)
	if err != nil {
		d.log.Error(err, "error DeleteReplicatedVolume")
		return nil, err
	}
	d.log.Info(fmt.Sprintf("[DeleteVolume][traceID:%s][volumeID:%s] Volume deleted successfully", traceID, request.VolumeId))
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

	_, err := utils.EnsureRVA(ctx, d.cl, d.log, traceID, volumeID, nodeID)
	if err != nil {
		d.log.Error(err, fmt.Sprintf("[ControllerPublishVolume][traceID:%s][volumeID:%s][nodeID:%s] Failed to create ReplicatedVolumeAttachment", traceID, volumeID, nodeID))
		return nil, status.Errorf(codes.Internal, "Failed to create ReplicatedVolumeAttachment: %v", err)
	}

	err = utils.WaitForRVAReady(ctx, d.cl, d.log, traceID, volumeID, nodeID)
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

	d.log.Info(fmt.Sprintf("[ControllerUnpublishVolume][traceID:%s][volumeID:%s][nodeID:%s] Waiting for node to disappear from status.actuallyAttachedTo", traceID, volumeID, nodeID))

	// Wait for node to disappear from status.actuallyAttachedTo
	err = utils.WaitForAttachedToRemoved(ctx, d.cl, d.log, traceID, volumeID, nodeID)
	if err != nil {
		d.log.Error(err, fmt.Sprintf("[ControllerUnpublishVolume][traceID:%s][volumeID:%s][nodeID:%s] Failed to wait for status.actuallyAttachedTo removal", traceID, volumeID, nodeID))
		return nil, status.Errorf(codes.Internal, "Failed to wait for status.actuallyAttachedTo removal: %v", err)
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
		csi.ControllerServiceCapability_RPC_CLONE_VOLUME,
		csi.ControllerServiceCapability_RPC_GET_CAPACITY,
		csi.ControllerServiceCapability_RPC_EXPAND_VOLUME,
		csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME,
		// TODO: Add snapshot support if needed
		// csi.ControllerServiceCapability_RPC_CREATE_DELETE_SNAPSHOT,
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

	resizeDelta, err := resource.ParseQuantity(internal.ResizeDelta)
	if err != nil {
		d.log.Error(err, fmt.Sprintf("[ControllerExpandVolume][traceID:%s][volumeID:%s] error ParseQuantity for ResizeDelta", traceID, volumeID))
		return nil, err
	}
	d.log.Trace(fmt.Sprintf("[ControllerExpandVolume][traceID:%s][volumeID:%s] resizeDelta: %s", traceID, volumeID, resizeDelta.String()))
	requestCapacity := resource.NewQuantity(request.CapacityRange.GetRequiredBytes(), resource.BinarySI)
	d.log.Trace(fmt.Sprintf("[ControllerExpandVolume][traceID:%s][volumeID:%s] requestCapacity: %s", traceID, volumeID, requestCapacity.String()))

	nodeExpansionRequired := true
	if request.GetVolumeCapability().GetBlock() != nil {
		nodeExpansionRequired = false
	}
	d.log.Info(fmt.Sprintf("[ControllerExpandVolume][traceID:%s][volumeID:%s] NodeExpansionRequired: %t", traceID, volumeID, nodeExpansionRequired))

	// Check if resize is needed
	currentSize := rv.Spec.Size
	if currentSize.Value() > requestCapacity.Value()+resizeDelta.Value() || utils.AreSizesEqualWithinDelta(*requestCapacity, currentSize, resizeDelta) {
		d.log.Warning(fmt.Sprintf("[ControllerExpandVolume][traceID:%s][volumeID:%s] requested size is less than or equal to the actual size of the volume include delta %s, no need to resize ReplicatedVolume %s, requested size: %s, actual size: %s, return NodeExpansionRequired: %t and CapacityBytes: %d", traceID, volumeID, resizeDelta.String(), volumeID, requestCapacity.String(), currentSize.String(), nodeExpansionRequired, currentSize.Value()))
		return &csi.ControllerExpandVolumeResponse{
			CapacityBytes:         currentSize.Value(),
			NodeExpansionRequired: nodeExpansionRequired,
		}, nil
	}

	d.log.Info(fmt.Sprintf("[ControllerExpandVolume][traceID:%s][volumeID:%s] start resize ReplicatedVolume", traceID, volumeID))
	d.log.Info(fmt.Sprintf("[ControllerExpandVolume][traceID:%s][volumeID:%s] requested size: %s, actual size: %s", traceID, volumeID, requestCapacity.String(), currentSize.String()))
	err = utils.ExpandReplicatedVolume(ctx, d.cl, rv, *requestCapacity)
	if err != nil {
		d.log.Error(err, fmt.Sprintf("[ControllerExpandVolume][traceID:%s][volumeID:%s] error updating ReplicatedVolume", traceID, volumeID))
		return nil, status.Errorf(codes.Internal, "error updating ReplicatedVolume: %v", err)
	}

	// Wait for ReplicatedVolume to become ready after resize
	attemptCounter, err := utils.WaitForReplicatedVolumeReady(ctx, d.cl, d.log, traceID, volumeID)
	if err != nil {
		d.log.Error(err, fmt.Sprintf("[ControllerExpandVolume][traceID:%s][volumeID:%s] error WaitForReplicatedVolumeReady", traceID, volumeID))
		return nil, err
	}
	d.log.Info(fmt.Sprintf("[ControllerExpandVolume][traceID:%s][volumeID:%s] finish resize ReplicatedVolume, attempt counter = %d", traceID, volumeID, attemptCounter))

	d.log.Info(fmt.Sprintf("[ControllerExpandVolume][traceID:%s][volumeID:%s] Volume expanded successfully", traceID, volumeID))

	return &csi.ControllerExpandVolumeResponse{
		CapacityBytes:         request.CapacityRange.RequiredBytes,
		NodeExpansionRequired: nodeExpansionRequired,
	}, nil
}

func (d *Driver) ControllerGetVolume(_ context.Context, _ *csi.ControllerGetVolumeRequest) (*csi.ControllerGetVolumeResponse, error) {
	d.log.Info(" call method ControllerGetVolume")
	return &csi.ControllerGetVolumeResponse{}, nil
}

func (d *Driver) ControllerModifyVolume(_ context.Context, _ *csi.ControllerModifyVolumeRequest) (*csi.ControllerModifyVolumeResponse, error) {
	d.log.Info(" call method ControllerModifyVolume")
	return &csi.ControllerModifyVolumeResponse{}, nil
}
