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

	"github.com/deckhouse/sds-local-volume/images/sds-local-volume-csi/internal"
	"github.com/deckhouse/sds-local-volume/images/sds-local-volume-csi/pkg/utils"
	"github.com/deckhouse/sds-local-volume/lib/go/common/pkg/feature"
	"github.com/deckhouse/sds-node-configurator/api/v1alpha1"
)

const (
	sourceVolumeKindSnapshot = "LVMLogicalVolumeSnapshot"
	sourceVolumeKindVolume   = "LVMLogicalVolume"
	LVMVolumeCleanupParamKey = "local.csi.storage.deckhouse.io/lvm-volume-cleanup"
)

func (d *Driver) CreateVolume(ctx context.Context, request *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	traceID := uuid.New().String()

	d.log.Trace(fmt.Sprintf("[CreateVolume][traceID:%s] ========== CreateVolume ============", traceID))
	d.log.Trace(request.String())
	d.log.Trace(fmt.Sprintf("[CreateVolume][traceID:%s] ========== CreateVolume ============", traceID))

	if request.Parameters[internal.TypeKey] != internal.Lvm {
		return nil, status.Error(codes.InvalidArgument, "Unsupported Storage Class type")
	}

	if len(request.Name) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume Name cannot be empty")
	}
	volumeID := request.Name
	if request.VolumeCapabilities == nil {
		return nil, status.Error(codes.InvalidArgument, "Volume Capability cannot de empty")
	}

	// ReplicatedVolume flow (early return)
	{
		// Build ReplicatedVolume spec from StorageClass params and cluster state
		rvSpec, err := utils.BuildReplicatedVolumeSpec(ctx, d.cl, d.log, request.Parameters, request.CapacityRange.GetRequiredBytes())
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid StorageClass params: %v", err)
		}

		// Create ReplicatedVolume and wait for Ready
		if _, cerr := utils.CreateReplicatedVolume(ctx, d.cl, request.Name, rvSpec); cerr != nil && !kerrors.IsAlreadyExists(cerr) {
			return nil, status.Errorf(codes.Internal, "create ReplicatedVolume: %v", cerr)
		}
		if werr := utils.WaitForReplicatedVolumeReady(ctx, d.cl, request.Name); werr != nil {
			return nil, status.Errorf(codes.Internal, "wait ReplicatedVolume Ready: %v", werr)
		}

		// VolumeContext: keep SC params; per-node VG/thinPool will be supplied via PublishContext
		volumeCtx := make(map[string]string, len(request.Parameters)+1)
		for k, v := range request.Parameters {
			volumeCtx[k] = v
		}
		volumeCtx[internal.SubPath] = request.Name

		// Preferred node only for topology exposure (WFFC)
		var preferredNode string
		if request.AccessibilityRequirements != nil && len(request.AccessibilityRequirements.Preferred) != 0 {
			t := request.AccessibilityRequirements.Preferred[0].Segments
			preferredNode = t[internal.TopologyKey]
		}

		return &csi.CreateVolumeResponse{
			Volume: &csi.Volume{
				CapacityBytes:      request.CapacityRange.GetRequiredBytes(),
				VolumeId:           request.Name,
				VolumeContext:      volumeCtx,
				ContentSource:      request.VolumeContentSource,
				AccessibleTopology: []*csi.Topology{{Segments: map[string]string{internal.TopologyKey: preferredNode}}},
			},
		}, nil
	}

	BindingMode := request.Parameters[internal.BindingModeKey]
	d.log.Info(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] storage class BindingMode: %s", traceID, volumeID, BindingMode))

	LvmType := request.Parameters[internal.LvmTypeKey]
	d.log.Info(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] storage class LvmType: %s", traceID, volumeID, LvmType))

	if len(request.Parameters[internal.LVMVolumeGroupKey]) == 0 {
		err := errors.New("no LVMVolumeGroups specified in a storage class's parameters")
		d.log.Error(err, fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] no LVMVolumeGroups were found for the request: %+v", traceID, volumeID, request))
		return nil, status.Errorf(codes.InvalidArgument, "no LVMVolumeGroups specified in a storage class's parameters")
	}

	storageClassLVGs, storageClassLVGParametersMap, err := utils.GetStorageClassLVGsAndParameters(ctx, d.cl, d.log, request.Parameters[internal.LVMVolumeGroupKey])
	if err != nil {
		d.log.Error(err, fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] error GetStorageClassLVGs", traceID, volumeID))
		return nil, status.Errorf(codes.Internal, "error during GetStorageClassLVGs")
	}

	contiguous := utils.IsContiguous(request, LvmType)
	d.log.Info(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] contiguous: %t", traceID, volumeID, contiguous))

	// TODO: Consider refactoring the naming strategy for llvName and lvName.
	// Currently, we use the same name for llvName (the name of the LVMLogicalVolume resource in Kubernetes)
	// and lvName (the name of the LV in LVM on the node) because the PV name is unique within the cluster,
	// preventing name collisions. This approach simplifies matching between nodes and Kubernetes by maintaining
	// the same name in both contexts. Future consideration should be given to optimizing this logic to enhance
	// code readability and maintainability.
	llvName := volumeID
	lvName := volumeID
	d.log.Info(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] llv name: %s", traceID, volumeID, llvName))

	llvSize := resource.NewQuantity(request.CapacityRange.GetRequiredBytes(), resource.BinarySI)
	d.log.Info(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] llv size: %s", traceID, volumeID, llvSize.String()))

	var selectedLVG *v1alpha1.LVMVolumeGroup
	var preferredNode string
	var sourceVolume *v1alpha1.LVMLogicalVolumeSource

	if request.VolumeContentSource != nil {
		sourceVolume = &v1alpha1.LVMLogicalVolumeSource{}
		switch s := request.VolumeContentSource.Type.(type) {
		case *csi.VolumeContentSource_Snapshot:
			sourceVolume.Kind = sourceVolumeKindSnapshot
			sourceVolume.Name = s.Snapshot.SnapshotId

			// get source volume
			sourceVol, err := utils.GetLVMLogicalVolumeSnapshot(ctx, d.cl, sourceVolume.Name, "")
			if err != nil {
				d.log.Error(err, fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] error getting source LVMLogicalVolumeSnapshot", traceID, sourceVolume.Name))
				return nil, status.Errorf(codes.NotFound, "error getting LVMLogicalVolumeSnapshot %s: %s", sourceVolume.Name, err.Error())
			}

			if sourceVol.Status == nil || sourceVol.Status.Phase != internal.LLVSStatusCreated {
				d.log.Error(nil, fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] source LVMLogicalVolumeSnapshot is not in Created phase", traceID, sourceVolume.Name))
				return nil, status.Errorf(codes.FailedPrecondition, "LVMLogicalVolumeSnapshot %s is not in Created phase", sourceVolume.Name)
			}

			// check size
			if llvSize.Value() == 0 {
				*llvSize = sourceVol.Status.Size
			} else if llvSize.Value() < sourceVol.Status.Size.Value() {
				return nil, status.Error(codes.OutOfRange, "requested size is smaller than the size of the source")
			}

			selectedLVG, err = utils.SelectLVGByActualNameOnTheNode(storageClassLVGs, sourceVol.Status.NodeName, sourceVol.Status.ActualVGNameOnTheNode)
			if err != nil {
				d.log.Error(
					err,
					fmt.Sprintf(
						"[CreateVolume][traceID:%s] source LVMVolumeGroup %s from node %s is not found in storage class LVGs",
						traceID,
						sourceVol.Status.ActualVGNameOnTheNode,
						sourceVol.Status.NodeName,
					),
				)
				return nil, status.Errorf(codes.FailedPrecondition, "error getting LVMVolumeGroup %s: %s", sourceVol.Status.ActualVGNameOnTheNode, err.Error())
			}

			if _, ok := storageClassLVGParametersMap[selectedLVG.Name]; !ok {
				d.log.Error(err, fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] should use the same storage class as source", traceID, volumeID))
				return nil, status.Errorf(codes.InvalidArgument, "should use the same storage class as source")
			}

			// prefer the same node as the source
			preferredNode = sourceVol.Status.NodeName
		case *csi.VolumeContentSource_Volume:
			sourceVolume.Kind = sourceVolumeKindVolume
			sourceVolume.Name = s.Volume.VolumeId

			// get source volume
			sourceVol, err := utils.GetLVMLogicalVolume(ctx, d.cl, sourceVolume.Name, "")
			if err != nil {
				d.log.Error(err, fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] error getting source LVMLogicalVolume", traceID, sourceVolume.Name))
				return nil, status.Errorf(codes.NotFound, "error getting LVMLogicalVolume %s: %s", sourceVolume.Name, err.Error())
			}

			if sourceVol.Spec.Type != internal.LVMTypeThin {
				return nil, status.Errorf(codes.InvalidArgument, "Source LVMLogicalVolume '%s' is not of 'Thin' type", sourceVol.Name)
			}

			// check size
			sourceSizeQty, err := resource.ParseQuantity(sourceVol.Spec.Size)
			if err != nil {
				d.log.Error(err, fmt.Sprintf("[CreateVolume][traceID:%s] error parsing quantity %s", traceID, sourceVol.Spec.Size))
				return nil, status.Errorf(codes.Internal, "error parsing quantity: %v", err)
			}

			// check size
			if llvSize.Value() == 0 {
				*llvSize = sourceSizeQty
			} else if llvSize.Value() < sourceSizeQty.Value() {
				return nil, status.Error(codes.OutOfRange, "requested size is smaller than the size of the source")
			}

			selectedLVG, err = utils.SelectLVGByName(storageClassLVGs, sourceVol.Spec.LVMVolumeGroupName)
			if err != nil {
				d.log.Error(err, fmt.Sprintf("[CreateVolume][traceID:%s] error getting LVMVolumeGroup %s", traceID, sourceVol.Spec.LVMVolumeGroupName))
				return nil, status.Errorf(codes.Internal, "error getting LVMVolumeGroup %s: %s", sourceVol.Spec.LVMVolumeGroupName, err.Error())
			}

			if _, ok := storageClassLVGParametersMap[selectedLVG.Name]; !ok {
				d.log.Error(err, fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] should use the same storage class as source", traceID, volumeID))
				return nil, status.Errorf(codes.InvalidArgument, "should use the same storage class as source")
			}

			// prefer the same node as the source
			preferredNode = selectedLVG.Spec.Local.NodeName
		}
	} else {
		switch BindingMode {
		case internal.BindingModeI:
			d.log.Info(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] BindingMode is %s. Start selecting node", traceID, volumeID, internal.BindingModeI))
			selectedNodeName, freeSpace, err := utils.GetNodeWithMaxFreeSpace(storageClassLVGs, storageClassLVGParametersMap, LvmType)
			if err != nil {
				d.log.Error(err, fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] error GetNodeMaxVGSize", traceID, volumeID))
			}

			preferredNode = selectedNodeName
			d.log.Info(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] Selected node: %s, free space %s", traceID, volumeID, selectedNodeName, freeSpace.String()))
			if LvmType == internal.LVMTypeThick {
				if llvSize.Value() > freeSpace.Value() {
					return nil, status.Errorf(codes.Internal, "requested size: %s is greater than free space: %s", llvSize.String(), freeSpace.String())
				}
			}
		case internal.BindingModeWFFC:
			d.log.Info(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] BindingMode is %s. Get preferredNode", traceID, volumeID, internal.BindingModeWFFC))
			if len(request.AccessibilityRequirements.Preferred) != 0 {
				t := request.AccessibilityRequirements.Preferred[0].Segments
				preferredNode = t[internal.TopologyKey]
			}
		}

		d.log.Trace(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] preferredNode: %s. Select LVG", traceID, volumeID, preferredNode))
		selectedLVG, err = utils.SelectLVG(storageClassLVGs, preferredNode)
		d.log.Info(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] selectedLVG: %+v", traceID, volumeID, selectedLVG))
		if err != nil {
			d.log.Error(err, fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] error SelectLVG", traceID, volumeID))
			return nil, status.Errorf(codes.Internal, "error during SelectLVG")
		}
	}

	volumeCleanup := request.Parameters[LVMVolumeCleanupParamKey]
	if !feature.VolumeCleanupEnabled() && volumeCleanup != "" {
		return nil, errors.New("volume cleanup is not supported in your edition")
	}

	llvSpec := utils.GetLLVSpec(
		d.log,
		lvName,
		*selectedLVG,
		storageClassLVGParametersMap,
		LvmType,
		*llvSize,
		contiguous,
		sourceVolume,
		volumeCleanup,
	)

	d.log.Info(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] LVMLogicalVolumeSpec: %+v", traceID, volumeID, llvSpec))
	resizeDelta, err := resource.ParseQuantity(internal.ResizeDelta)
	if err != nil {
		d.log.Error(err, fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] error ParseQuantity for ResizeDelta", traceID, volumeID))
		return nil, err
	}

	d.log.Trace(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] ------------ CreateLVMLogicalVolume start ------------", traceID, volumeID))
	_, err = utils.CreateLVMLogicalVolume(ctx, d.cl, d.log, traceID, llvName, llvSpec)
	if err != nil {
		if kerrors.IsAlreadyExists(err) {
			d.log.Info(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] LVMLogicalVolume %s already exists. Skip creating", traceID, volumeID, llvName))
		} else {
			d.log.Error(err, fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] error CreateLVMLogicalVolume", traceID, volumeID))
			return nil, err
		}
	}
	d.log.Trace(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] ------------ CreateLVMLogicalVolume end ------------", traceID, volumeID))

	d.log.Trace(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] start wait CreateLVMLogicalVolume", traceID, volumeID))

	attemptCounter, err := utils.WaitForStatusUpdate(ctx, d.cl, d.log, traceID, request.Name, "", *llvSize, resizeDelta)
	if err != nil {
		d.log.Error(err, fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] error WaitForStatusUpdate. Delete LVMLogicalVolume %s", traceID, volumeID, request.Name))

		deleteErr := utils.DeleteLVMLogicalVolume(ctx, d.cl, d.log, traceID, request.Name, volumeCleanup)
		if deleteErr != nil {
			d.log.Error(deleteErr, fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] error DeleteLVMLogicalVolume", traceID, volumeID))
		}

		d.log.Error(err, fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] error creating LVMLogicalVolume", traceID, volumeID))
		return nil, err
	}
	d.log.Trace(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] finish wait CreateLVMLogicalVolume, attempt counter = %d", traceID, volumeID, attemptCounter))

	volumeCtx := make(map[string]string, len(request.Parameters))
	for k, v := range request.Parameters {
		volumeCtx[k] = v
	}

	volumeCtx[internal.SubPath] = request.Name
	volumeCtx[internal.VGNameKey] = selectedLVG.Spec.ActualVGNameOnTheNode
	if llvSpec.Type == internal.LVMTypeThin {
		volumeCtx[internal.ThinPoolNameKey] = llvSpec.Thin.PoolName
	} else {
		volumeCtx[internal.ThinPoolNameKey] = ""
	}

	d.log.Info(fmt.Sprintf("[CreateVolume][traceID:%s][volumeID:%s] Volume created successfully. volumeCtx: %+v", traceID, volumeID, volumeCtx))

	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			CapacityBytes: request.CapacityRange.GetRequiredBytes(),
			VolumeId:      request.Name,
			VolumeContext: volumeCtx,
			ContentSource: request.VolumeContentSource,
			AccessibleTopology: []*csi.Topology{
				{Segments: map[string]string{
					internal.TopologyKey: preferredNode,
				}},
			},
		},
	}, nil
}

func (d *Driver) DeleteVolume(ctx context.Context, request *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	traceID := uuid.New().String()
	d.log.Info(fmt.Sprintf("[DeleteVolume][traceID:%s] ========== Start DeleteVolume ============", traceID))
	if len(request.VolumeId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID cannot be empty")
	}

	// ReplicatedVolume flow (early return)
	if rv, err := utils.GetReplicatedVolume(ctx, d.cl, request.VolumeId); err == nil {
		if derr := utils.DeleteReplicatedVolume(ctx, d.cl, rv); derr != nil {
			return nil, status.Errorf(codes.Internal, "delete ReplicatedVolume: %v", derr)
		}
		d.log.Info(fmt.Sprintf("[DeleteVolume][traceID:%s][volumeID:%s] ReplicatedVolume deleted successfully", traceID, request.VolumeId))
		d.log.Info(fmt.Sprintf("[DeleteVolume][traceID:%s] ========== END DeleteVolume ============", traceID))
		return &csi.DeleteVolumeResponse{}, nil
	} else if kerrors.IsNotFound(err) {
		d.log.Info(fmt.Sprintf("[DeleteVolume][traceID:%s][volumeID:%s] ReplicatedVolume not found, skip", traceID, request.VolumeId))
		return &csi.DeleteVolumeResponse{}, nil
	}

	volumeCleanup := func() string {
		localStorageClass, err := utils.GetLSCBeforeLLVDelete(ctx, d.cl, *d.log, request.VolumeId, traceID)
		if err == nil && localStorageClass != nil && localStorageClass.Spec.LVM != nil {
			return localStorageClass.Spec.LVM.VolumeCleanup
		}
		return ""
	}()

	if volumeCleanup != "" && !feature.VolumeCleanupEnabled() {
		return nil, errors.New("volumeCleanup is not supported in your edition")
	}

	err := utils.DeleteLVMLogicalVolume(ctx, d.cl, d.log, traceID, request.VolumeId, volumeCleanup)
	if err != nil {
		d.log.Error(err, "error DeleteLVMLogicalVolume")
		return nil, err
	}
	d.log.Info(fmt.Sprintf("[DeleteVolume][traceID:%s][volumeID:%s] Volume deleted successfully", traceID, request.VolumeId))
	d.log.Info(fmt.Sprintf("[DeleteVolume][traceID:%s] ========== END DeleteVolume ============", traceID))
	return &csi.DeleteVolumeResponse{}, nil
}

func (d *Driver) ControllerPublishVolume(ctx context.Context, request *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	d.log.Info("method ControllerPublishVolume")
	volumeID := request.GetVolumeId()
	nodeID := request.GetNodeId()
	if len(volumeID) == 0 || len(nodeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "volumeID and nodeID are required")
	}

	rv, err := utils.GetReplicatedVolume(ctx, d.cl, volumeID)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "ReplicatedVolume %s: %v", volumeID, err)
	}

	exists := false
	for _, n := range rv.Spec.PublishRequested {
		if n == nodeID {
			exists = true
			break
		}
	}
	if !exists {
		rv.Spec.PublishRequested = append(rv.Spec.PublishRequested, nodeID)
		if err := d.cl.Update(ctx, rv); err != nil {
			return nil, status.Errorf(codes.Internal, "update ReplicatedVolume: %v", err)
		}
	}

	// Try to enrich PublishContext with VG/thinPool if detectable from node LVG names present in RV spec
	pubCtx := map[string]string{
		d.publishInfoVolumeName: request.VolumeId,
	}
	// Best-effort: find LVG name that belongs to this node via LVMVolumeGroup list
	if lvgs, lerr := utils.GetLVGList(ctx, d.cl); lerr == nil {
		for _, lvg := range lvgs.Items {
			if lvg.Spec.Local.NodeName == nodeID {
				pubCtx[internal.VGNameKey] = lvg.Spec.ActualVGNameOnTheNode
				// if RV is Thin and the LVG is in RV spec with thin pool, try to attach pool name
				// We cannot read RV spec LVM mapping here without a second get; do it best-effort by scanning pools
				if len(lvg.Spec.ThinPools) > 0 {
					pubCtx[internal.ThinPoolNameKey] = lvg.Spec.ThinPools[0].Name
				}
				break
			}
		}
	}
	return &csi.ControllerPublishVolumeResponse{PublishContext: pubCtx}, nil
}

func (d *Driver) ControllerUnpublishVolume(ctx context.Context, req *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	d.log.Info("method ControllerUnpublishVolume")
	volumeID := req.GetVolumeId()
	nodeID := req.GetNodeId()
	if len(volumeID) == 0 || len(nodeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "volumeID and nodeID are required")
	}

	rv, err := utils.GetReplicatedVolume(ctx, d.cl, volumeID)
	if err != nil {
		if kerrors.IsNotFound(err) {
			return &csi.ControllerUnpublishVolumeResponse{}, nil
		}
		return nil, status.Errorf(codes.NotFound, "ReplicatedVolume %s: %v", volumeID, err)
	}

	filtered := make([]string, 0, len(rv.Spec.PublishRequested))
	for _, n := range rv.Spec.PublishRequested {
		if n != nodeID {
			filtered = append(filtered, n)
		}
	}
	rv.Spec.PublishRequested = filtered
	if err := d.cl.Update(ctx, rv); err != nil {
		return nil, status.Errorf(codes.Internal, "update ReplicatedVolume: %v", err)
	}

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

	// todo MaxSize one PV
	// todo call volumeBindingMode: WaitForFirstConsumer

	return &csi.GetCapacityResponse{
		AvailableCapacity: 1000000,
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
	}

	if feature.SnapshotsEnabled() {
		capabilities = append(capabilities, csi.ControllerServiceCapability_RPC_CREATE_DELETE_SNAPSHOT)
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

	llv, err := utils.GetLVMLogicalVolume(ctx, d.cl, volumeID, "")
	if err != nil {
		d.log.Error(err, fmt.Sprintf("[ControllerExpandVolume][traceID:%s][volumeID:%s] error getting LVMLogicalVolume", traceID, volumeID))
		return nil, status.Errorf(codes.Internal, "error getting LVMLogicalVolume: %s", err.Error())
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

	if llv.Status.ActualSize.Value() > requestCapacity.Value()+resizeDelta.Value() || utils.AreSizesEqualWithinDelta(*requestCapacity, llv.Status.ActualSize, resizeDelta) {
		d.log.Warning(fmt.Sprintf("[ControllerExpandVolume][traceID:%s][volumeID:%s] requested size is less than or equal to the actual size of the volume include delta %s , no need to resize LVMLogicalVolume %s, requested size: %s, actual size: %s, return NodeExpansionRequired: %t and CapacityBytes: %d", traceID, volumeID, resizeDelta.String(), volumeID, requestCapacity.String(), llv.Status.ActualSize.String(), nodeExpansionRequired, llv.Status.ActualSize.Value()))
		return &csi.ControllerExpandVolumeResponse{
			CapacityBytes:         llv.Status.ActualSize.Value(),
			NodeExpansionRequired: nodeExpansionRequired,
		}, nil
	}

	lvg, err := utils.GetLVMVolumeGroup(ctx, d.cl, llv.Spec.LVMVolumeGroupName)
	if err != nil {
		d.log.Error(err, fmt.Sprintf("[ControllerExpandVolume][traceID:%s][volumeID:%s] error getting LVMVolumeGroup", traceID, volumeID))
		return nil, status.Errorf(codes.Internal, "error getting LVMVolumeGroup: %v", err)
	}

	if llv.Spec.Type == internal.LVMTypeThick {
		lvgFreeSpace := utils.GetLVMVolumeGroupFreeSpace(*lvg)

		if lvgFreeSpace.Value() < (requestCapacity.Value() - llv.Status.ActualSize.Value()) {
			d.log.Error(err, fmt.Sprintf("[ControllerExpandVolume][traceID:%s][volumeID:%s] requested size: %s is greater than the capacity of the LVMVolumeGroup: %s", traceID, volumeID, requestCapacity.String(), lvgFreeSpace.String()))
			return nil, status.Errorf(codes.Internal, "requested size: %s is greater than the capacity of the LVMVolumeGroup: %s", requestCapacity.String(), lvgFreeSpace.String())
		}
	}

	d.log.Info(fmt.Sprintf("[ControllerExpandVolume][traceID:%s][volumeID:%s] start resize LVMLogicalVolume", traceID, volumeID))
	d.log.Info(fmt.Sprintf("[ControllerExpandVolume][traceID:%s][volumeID:%s] requested size: %s, actual size: %s", traceID, volumeID, requestCapacity.String(), llv.Status.ActualSize.String()))
	err = utils.ExpandLVMLogicalVolume(ctx, d.cl, llv, requestCapacity.String())
	if err != nil {
		d.log.Error(err, fmt.Sprintf("[ControllerExpandVolume][traceID:%s][volumeID:%s] error updating LVMLogicalVolume", traceID, volumeID))
		return nil, status.Errorf(codes.Internal, "error updating LVMLogicalVolume: %v", err)
	}

	attemptCounter, err := utils.WaitForStatusUpdate(ctx, d.cl, d.log, traceID, llv.Name, llv.Namespace, *requestCapacity, resizeDelta)
	if err != nil {
		d.log.Error(err, fmt.Sprintf("[ControllerExpandVolume][traceID:%s][volumeID:%s] error WaitForStatusUpdate", traceID, volumeID))
		return nil, err
	}
	d.log.Info(fmt.Sprintf("[ControllerExpandVolume][traceID:%s][volumeID:%s] finish resize LVMLogicalVolume, attempt counter = %d ", traceID, volumeID, attemptCounter))

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
