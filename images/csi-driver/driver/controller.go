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

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-common-lib/logger"
	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	"github.com/deckhouse/sds-replicated-volume/images/csi-driver/internal"
	"github.com/deckhouse/sds-replicated-volume/images/csi-driver/pkg/utils"
)

const (
	ReplicatedStorageClassParamNameKey = "replicated.csi.storage.deckhouse.io/replicatedStorageClassName"
)

func (d *Driver) CreateVolume(ctx context.Context, request *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	createVolumeLog := d.log.Named("CreateVolume")
	log := d.log.Named("CreateVolume")

	traceID := uuid.New().String()

	log.Trace(fmt.Sprintf("[traceID:%s] ========== CreateVolume ============", traceID))
	d.log.Trace(request.String())
	log.Trace(fmt.Sprintf("[traceID:%s] ========== CreateVolume ============", traceID))

	if len(request.Name) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume Name cannot be empty")
	}
	volumeID := request.Name
	if request.VolumeCapabilities == nil {
		return nil, status.Error(codes.InvalidArgument, "Volume Capability cannot de empty")
	}

	if request.VolumeContentSource != nil {
		return nil, status.Error(codes.InvalidArgument, "Creating volumes from snapshots or clones is not supported")
	}

	rvSize := resource.NewQuantity(request.CapacityRange.GetRequiredBytes(), resource.BinarySI)
	log.Info(fmt.Sprintf("[traceID:%s][volumeID:%s] ReplicatedVolume size: %s", traceID, volumeID, rvSize.String()))

	preferredNode := ""
	if ar := request.AccessibilityRequirements; ar != nil {
		log.Info(fmt.Sprintf("[traceID:%s][volumeID:%s] AccessibilityRequirements: Requisite=%v, Preferred=%v", traceID, volumeID, ar.Requisite, ar.Preferred))
		for _, topo := range ar.Preferred {
			if node, ok := topo.Segments[internal.TopologyKey]; ok {
				preferredNode = node
				break
			}
		}
	}
	log.Info(fmt.Sprintf("[traceID:%s][volumeID:%s] Preferred node from AccessibilityRequirements: %q", traceID, volumeID, preferredNode))

	bindingMode := request.Parameters[internal.BindingModeKey]
	log.Info(fmt.Sprintf("[traceID:%s][volumeID:%s] volume-binding-mode: %q", traceID, volumeID, bindingMode))

	if bindingMode != internal.BindingModeWFFC && bindingMode != internal.BindingModeI {
		errMsg := fmt.Sprintf("StorageClass must set %q to %q or %q", internal.BindingModeKey, internal.BindingModeWFFC, internal.BindingModeI)
		err := status.Error(codes.InvalidArgument, errMsg)
		log.Error(fmt.Sprintf("[traceID:%s][volumeID:%s] %s", traceID, volumeID, errMsg), logger.Err(err))
		return nil, err
	}

	createdEarlyRVA := false
	if bindingMode == internal.BindingModeWFFC && preferredNode != "" {
		log.Info(fmt.Sprintf("[traceID:%s][volumeID:%s][node:%s] WFFC binding: creating early RVA for preferred node", traceID, volumeID, preferredNode))
		_, err := utils.EnsureRVA(ctx, d.cl, d.log, traceID, volumeID, preferredNode)
		if err != nil {
			log.Error(fmt.Sprintf("[traceID:%s][volumeID:%s][node:%s] Failed to create ReplicatedVolumeAttachment for WFFC-selected node", traceID, volumeID, preferredNode), logger.Err(err))
			return nil, status.Errorf(codes.Internal,
				"failed to create ReplicatedVolumeAttachment for volume %q on WFFC-selected node %q: %v",
				volumeID, preferredNode, err)
		}
		createdEarlyRVA = true
	}

	// Build ReplicatedVolumeSpec
	rvSpec := utils.BuildReplicatedVolumeSpec(
		*rvSize,
		request.Parameters[ReplicatedStorageClassParamNameKey],
		request.Parameters[srv.CSIParamVolumeCleanupKey],
	)

	log.Info(fmt.Sprintf("[traceID:%s][volumeID:%s] ReplicatedVolumeSpec: %+v", traceID, volumeID, rvSpec))

	// buildResponse assembles the CSI CreateVolumeResponse from an observed RV.
	// Falls back to the requested size when the RV status has not yet reported one.
	buildResponse := func(rv *srv.ReplicatedVolume) *csi.CreateVolumeResponse {
		capacityBytes := rvSize.Value()
		if rv != nil && rv.Status.Size != nil {
			capacityBytes = rv.Status.Size.Value()
		}
		volumeCtx := make(map[string]string, len(request.Parameters)+1)
		for k, v := range request.Parameters {
			volumeCtx[k] = v
		}
		volumeCtx[internal.ReplicatedVolumeNameKey] = volumeID
		createVolumeLog.Info(fmt.Sprintf("[traceID:%s][volumeID:%s] Volume created successfully, capacity: %d. volumeCtx: %+v", traceID, volumeID, capacityBytes, volumeCtx))
		return &csi.CreateVolumeResponse{
			Volume: &csi.Volume{
				CapacityBytes:      capacityBytes,
				VolumeId:           request.Name,
				VolumeContext:      volumeCtx,
				ContentSource:      nil,
				AccessibleTopology: nil,
			},
		}
	}

	// cleanupEarlyRVA best-effort removes the WFFC-selected RVA that was created
	// upfront in this RPC. Used on all error paths below.
	cleanupEarlyRVA := func() {
		if !createdEarlyRVA {
			return
		}
		if rvaErr := utils.DeleteRVA(ctx, d.cl, d.log, traceID, volumeID, preferredNode); rvaErr != nil {
			createVolumeLog.Error(fmt.Sprintf("[traceID:%s][volumeID:%s][node:%s] error cleaning up WFFC RVA", traceID, volumeID, preferredNode), logger.Err(rvaErr))
		}
	}

	// Create ReplicatedVolume
	log.Trace(fmt.Sprintf("[traceID:%s][volumeID:%s] ------------ CreateReplicatedVolume start ------------", traceID, volumeID))
	pvcName := request.Parameters[internal.PVCAnnotationNameKey]
	pvcNamespace := request.Parameters[internal.PVCAnnotationNamespaceKey]
	_, err := utils.CreateReplicatedVolume(ctx, d.cl, d.log, traceID, volumeID, pvcName, pvcNamespace, rvSpec)

	if err != nil && !kerrors.IsAlreadyExists(err) {
		log.Error(fmt.Sprintf("[traceID:%s][volumeID:%s] error CreateReplicatedVolume", traceID, volumeID), logger.Err(err))
		cleanupEarlyRVA()
		return nil, err
	}

	if kerrors.IsAlreadyExists(err) {
		log.Info(fmt.Sprintf("[traceID:%s][volumeID:%s] ReplicatedVolume already exists; re-observing", traceID, volumeID))
		existing := &srv.ReplicatedVolume{}
		getErr := d.cl.Get(ctx, client.ObjectKey{Name: volumeID}, existing)
		if getErr != nil {
			log.Error(fmt.Sprintf("[traceID:%s][volumeID:%s] cannot observe existing ReplicatedVolume", traceID, volumeID), logger.Err(getErr))
			cleanupEarlyRVA()
			return nil, status.Errorf(codes.Unavailable, "cannot observe existing ReplicatedVolume %s: %v", volumeID, getErr)
		}
		if existing.DeletionTimestamp != nil {
			log.Warn(fmt.Sprintf("[traceID:%s][volumeID:%s] existing ReplicatedVolume is being deleted (deletionTimestamp=%v)", traceID, volumeID, existing.DeletionTimestamp))
			cleanupEarlyRVA()
			return nil, status.Errorf(codes.Aborted, "ReplicatedVolume %s is being deleted", volumeID)
		}
		if utils.IsReplicatedVolumePastFormation(existing) {
			log.Info(fmt.Sprintf("[traceID:%s][volumeID:%s] ReplicatedVolume already past Formation; returning success without Wait", traceID, volumeID))
			return buildResponse(existing), nil
		}
		log.Info(fmt.Sprintf("[traceID:%s][volumeID:%s] ReplicatedVolume exists but still forming (datameshRevision=%d); proceeding to wait for readiness", traceID, volumeID, existing.Status.DatameshRevision))
	} else {
		log.Info(fmt.Sprintf("[traceID:%s][volumeID:%s] ReplicatedVolume created successfully. Proceeding to wait for readiness", traceID, volumeID))
	}

	log.Trace(fmt.Sprintf("[traceID:%s][volumeID:%s] ------------ CreateReplicatedVolume end ------------", traceID, volumeID))
	log.Trace(fmt.Sprintf("[traceID:%s][volumeID:%s] start wait ReplicatedVolume", traceID, volumeID))

	waitCtx, waitCancel := context.WithTimeout(ctx, d.waitActionTimeout)
	defer waitCancel()
	rv, attemptCounter, waitErr := utils.WaitForReplicatedVolumeReady(waitCtx, d.cl, d.log, traceID, volumeID)
	if waitErr != nil {
		log.Error(fmt.Sprintf("[traceID:%s][volumeID:%s] error WaitForReplicatedVolumeReady", traceID, volumeID), logger.Err(waitErr))

		// Re-observe the RV to decide the outcome:
		//   - PastFormation && no DeletionTimestamp  -> race win: rv-controller
		//     committed Formation between Wait's last poll and this cleanup;
		//     return Success and keep the early RVA (same as happy path).
		//   - DeletionTimestamp != nil               -> someone else is deleting
		//     the RV; skip our Delete to avoid fighting.
		//   - Otherwise (RV missing / Get error / not past Formation)
		//     -> RV is uncommitted garbage; delete it so the next CreateVolume
		//     retry can recreate it cleanly.
		observed := &srv.ReplicatedVolume{}
		getErr := d.cl.Get(ctx, client.ObjectKey{Name: volumeID}, observed)
		if getErr == nil && observed.DeletionTimestamp == nil && utils.IsReplicatedVolumePastFormation(observed) {
			log.Warn(fmt.Sprintf("[traceID:%s][volumeID:%s] RV became past Formation (datameshRevision=%d) between Wait deadline and cleanup; returning success", traceID, volumeID, observed.Status.DatameshRevision))
			return buildResponse(observed), nil
		}

		cleanupEarlyRVA()

		switch {
		case kerrors.IsNotFound(getErr):
			log.Info(fmt.Sprintf("[traceID:%s][volumeID:%s] RV already gone between Wait and cleanup; nothing to delete", traceID, volumeID))
		case getErr != nil:
			log.Warn(fmt.Sprintf("[traceID:%s][volumeID:%s] cannot observe RV for safe cleanup decision: %v; skipping delete", traceID, volumeID, getErr))
		case observed.DeletionTimestamp != nil:
			log.Info(fmt.Sprintf("[traceID:%s][volumeID:%s] RV already being deleted; skipping delete", traceID, volumeID))
		default:
			if delErr := utils.DeleteReplicatedVolume(ctx, d.cl, d.log, traceID, volumeID); delErr != nil {
				log.Error(fmt.Sprintf("[traceID:%s][volumeID:%s] error DeleteReplicatedVolume", traceID, volumeID), logger.Err(delErr))
			}
		}

		return nil, waitErr
	}
	log.Info(fmt.Sprintf("[traceID:%s][volumeID:%s] ReplicatedVolume ready successfully, attempt counter: %d", traceID, volumeID, attemptCounter))
	log.Trace(fmt.Sprintf("[traceID:%s][volumeID:%s] ------------ WaitForReplicatedVolumeReady end ------------", traceID, volumeID))

	return buildResponse(rv), nil
}

func (d *Driver) DeleteVolume(ctx context.Context, request *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	log := d.log.Named("DeleteVolume")

	traceID := uuid.New().String()
	log.Info(fmt.Sprintf("[traceID:%s] ========== Start DeleteVolume ============", traceID))
	if len(request.VolumeId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID cannot be empty")
	}

	if err := utils.ReleaseOrphanRVAs(ctx, d.cl, d.log, traceID, d.name, request.VolumeId); err != nil {
		log.Error(fmt.Sprintf("[traceID:%s][volumeID:%s] error releasing RVAs without a VolumeAttachment", traceID, request.VolumeId), logger.Err(err))
		return nil, status.Errorf(codes.Internal, "%v", err)
	}

	if err := utils.CheckNoRVAsExist(ctx, d.cl, d.log, traceID, request.VolumeId); err != nil {
		log.Error(fmt.Sprintf("[traceID:%s][volumeID:%s] cannot delete volume while RVAs exist", traceID, request.VolumeId), logger.Err(err))
		return nil, status.Errorf(codes.FailedPrecondition, "%v", err)
	}

	err := utils.DeleteReplicatedVolume(ctx, d.cl, d.log, traceID, request.VolumeId)
	if err != nil {
		d.log.Error("error DeleteReplicatedVolume", logger.Err(err))
		return nil, err
	}

	deleteWaitCtx, deleteWaitCancel := context.WithTimeout(ctx, d.waitActionTimeout)
	defer deleteWaitCancel()
	attemptCounter, err := utils.WaitForReplicatedVolumeDeleted(deleteWaitCtx, d.cl, d.log, traceID, request.VolumeId)
	if err != nil {
		log.Error(fmt.Sprintf("[traceID:%s][volumeID:%s] error waiting for ReplicatedVolume deletion", traceID, request.VolumeId), logger.Err(err))
		return nil, err
	}
	log.Info(fmt.Sprintf("[traceID:%s][volumeID:%s] Volume deleted successfully, attempts: %d", traceID, request.VolumeId, attemptCounter))
	log.Info(fmt.Sprintf("[traceID:%s] ========== END DeleteVolume ============", traceID))
	return &csi.DeleteVolumeResponse{}, nil
}

func (d *Driver) ControllerPublishVolume(ctx context.Context, request *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	log := d.log.Named("ControllerPublishVolume")

	traceID := uuid.New().String()
	log.Info(fmt.Sprintf("[traceID:%s] ========== ControllerPublishVolume ============", traceID))
	d.log.Trace(request.String())

	if request.VolumeId == "" {
		return nil, status.Error(codes.InvalidArgument, "Volume ID cannot be empty")
	}
	if request.NodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "Node ID cannot be empty")
	}

	volumeID := request.VolumeId
	nodeID := request.NodeId

	log.Info(fmt.Sprintf("[traceID:%s][volumeID:%s][nodeID:%s] Creating ReplicatedVolumeAttachment and waiting for Ready=true", traceID, volumeID, nodeID))

	// EnsureRVA may block waiting for a stale RVA (from a just-finished
	// ControllerUnpublishVolume) to be fully finalized by rv-controller.
	// Bound that wait to waitActionTimeout so the gRPC call does not hang
	// if something is genuinely stuck: external-attacher will retry with backoff.
	ensureCtx, ensureCancel := context.WithTimeout(ctx, d.waitActionTimeout)
	_, err := utils.EnsureRVA(ensureCtx, d.cl, d.log, traceID, volumeID, nodeID)
	ensureCancel()
	if err != nil {
		log.Error(fmt.Sprintf("[traceID:%s][volumeID:%s][nodeID:%s] Failed to create ReplicatedVolumeAttachment", traceID, volumeID, nodeID), logger.Err(err))
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
		log.Error(fmt.Sprintf("[traceID:%s][volumeID:%s][nodeID:%s] Failed waiting for RVA Ready=true", traceID, volumeID, nodeID), logger.Err(err))
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

	log.Info(fmt.Sprintf("[traceID:%s][volumeID:%s][nodeID:%s] Volume attached successfully", traceID, volumeID, nodeID))
	log.Info(fmt.Sprintf("[traceID:%s] ========== END ControllerPublishVolume ============", traceID))

	return &csi.ControllerPublishVolumeResponse{
		PublishContext: map[string]string{
			internal.ReplicatedVolumeNameKey: volumeID,
		},
	}, nil
}

func (d *Driver) ControllerUnpublishVolume(ctx context.Context, request *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	log := d.log.Named("ControllerUnpublishVolume")

	traceID := uuid.New().String()
	log.Info(fmt.Sprintf("[traceID:%s] ========== ControllerUnpublishVolume ============", traceID))
	d.log.Trace(request.String())

	if request.VolumeId == "" {
		return nil, status.Error(codes.InvalidArgument, "Volume ID cannot be empty")
	}
	if request.NodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "Node ID cannot be empty")
	}

	volumeID := request.VolumeId
	nodeID := request.NodeId

	log.Info(fmt.Sprintf("[traceID:%s][volumeID:%s][nodeID:%s] Deleting ReplicatedVolumeAttachment", traceID, volumeID, nodeID))

	err := utils.DeleteRVA(ctx, d.cl, d.log, traceID, volumeID, nodeID)
	if err != nil {
		log.Error(fmt.Sprintf("[traceID:%s][volumeID:%s][nodeID:%s] Failed to delete ReplicatedVolumeAttachment", traceID, volumeID, nodeID), logger.Err(err))
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
	log.Info(fmt.Sprintf("[traceID:%s][volumeID:%s][nodeID:%s] Waiting for ReplicatedVolumeAttachment object to be fully deleted", traceID, volumeID, nodeID))
	rvaDelCtx, rvaDelCancel := context.WithTimeout(ctx, d.waitActionTimeout)
	defer rvaDelCancel()
	if err := utils.WaitForRVADeleted(rvaDelCtx, d.cl, d.log, traceID, volumeID, nodeID); err != nil {
		log.Error(fmt.Sprintf("[traceID:%s][volumeID:%s][nodeID:%s] Failed to wait for RVA deletion", traceID, volumeID, nodeID), logger.Err(err))
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, status.Errorf(codes.DeadlineExceeded, "Timed out waiting for RVA deletion: %v", err)
		}
		return nil, status.Errorf(codes.Internal, "Failed to wait for RVA deletion: %v", err)
	}

	log.Info(fmt.Sprintf("[traceID:%s][volumeID:%s][nodeID:%s] Volume detached successfully", traceID, volumeID, nodeID))
	log.Info(fmt.Sprintf("[traceID:%s] ========== END ControllerUnpublishVolume ============", traceID))

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
		// TODO: Add snapshot/clone support if needed
		// csi.ControllerServiceCapability_RPC_CREATE_DELETE_SNAPSHOT,
		// csi.ControllerServiceCapability_RPC_CLONE_VOLUME,
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
	log := d.log.Named("ControllerExpandVolume")

	traceID := uuid.New().String()

	log.Info(fmt.Sprintf("[traceID:%s] method ControllerExpandVolume", traceID))
	log.Trace(fmt.Sprintf("[traceID:%s] ========== ControllerExpandVolume ============", traceID))
	d.log.Trace(request.String())
	log.Trace(fmt.Sprintf("[traceID:%s] ========== ControllerExpandVolume ============", traceID))

	volumeID := request.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume id cannot be empty")
	}

	rv, err := utils.GetReplicatedVolume(ctx, d.cl, volumeID)
	if err != nil {
		log.Error(fmt.Sprintf("[traceID:%s][volumeID:%s] error getting ReplicatedVolume", traceID, volumeID), logger.Err(err))
		return nil, status.Errorf(codes.Internal, "error getting ReplicatedVolume: %s", err.Error())
	}

	requestCapacity := resource.NewQuantity(request.CapacityRange.GetRequiredBytes(), resource.BinarySI)
	log.Trace(fmt.Sprintf("[traceID:%s][volumeID:%s] requestCapacity: %s", traceID, volumeID, requestCapacity.String()))

	nodeExpansionRequired := request.GetVolumeCapability().GetBlock() == nil
	log.Info(fmt.Sprintf("[traceID:%s][volumeID:%s] NodeExpansionRequired: %t", traceID, volumeID, nodeExpansionRequired))

	if requestCapacity.Cmp(rv.Spec.Size) > 0 {
		log.Info(fmt.Sprintf("[traceID:%s][volumeID:%s] Updating spec.size from %s to %s", traceID, volumeID, rv.Spec.Size.String(), requestCapacity.String()))
		err = utils.ExpandReplicatedVolume(ctx, d.cl, rv, *requestCapacity)
		if err != nil {
			log.Error(fmt.Sprintf("[traceID:%s][volumeID:%s] error updating ReplicatedVolume", traceID, volumeID), logger.Err(err))
			return nil, status.Errorf(codes.Internal, "error updating ReplicatedVolume: %v", err)
		}
	}

	if rv.Status.Size != nil && rv.Status.Size.Cmp(*requestCapacity) >= 0 {
		log.Info(fmt.Sprintf("[traceID:%s][volumeID:%s] status.size %s already >= requested %s, no resize needed", traceID, volumeID, rv.Status.Size.String(), requestCapacity.String()))
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
		log.Error(fmt.Sprintf("[traceID:%s][volumeID:%s] error WaitForReplicatedVolumeResized", traceID, volumeID), logger.Err(err))
		return nil, status.Errorf(codes.Internal, "error waiting for resize: %v", err)
	}
	log.Info(fmt.Sprintf("[traceID:%s][volumeID:%s] Resize complete after %d attempts", traceID, volumeID, attemptCounter))

	capacityBytes := rv.Status.Size.Value()
	log.Info(fmt.Sprintf("[traceID:%s][volumeID:%s] Volume expanded successfully, reporting capacity: %d", traceID, volumeID, capacityBytes))

	return &csi.ControllerExpandVolumeResponse{
		CapacityBytes:         capacityBytes,
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
