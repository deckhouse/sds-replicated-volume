//go:build ee

/*
Copyright 2025 Flant JSC

Licensed under the Deckhouse Platform Enterprise Edition (EE) license.
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    https://github.com/deckhouse/deckhouse/blob/main/ee/LICENSE
*/

package driver

import (
	"context"
	"fmt"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/protobuf/ptypes/timestamp"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/deckhouse/sds-local-volume/images/sds-local-volume-csi/internal"
	"github.com/deckhouse/sds-local-volume/images/sds-local-volume-csi/pkg/utils"
	"github.com/deckhouse/sds-node-configurator/api/v1alpha1"
)

func (d *Driver) CreateSnapshot(ctx context.Context, request *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	traceID := uuid.New().String()

	d.log.Trace(fmt.Sprintf("[CreateSnapshot][traceID:%s] ========== CreateSnapshot ============", traceID))
	d.log.Trace(request.String())

	llv, err := utils.GetLVMLogicalVolume(ctx, d.cl, request.SourceVolumeId, "")
	if err != nil {
		d.log.Error(err, fmt.Sprintf("[CreateSnapshot][traceID:%s][volumeID:%s] error getting LVMLogicalVolume", traceID, request.SourceVolumeId))
		return nil, status.Errorf(codes.Internal, "error getting LVMLogicalVolume %s: %s", request.SourceVolumeId, err.Error())
	}

	if llv.Spec.Type != internal.LVMTypeThin {
		return nil, status.Errorf(codes.InvalidArgument, "Source LVMLogicalVolume '%s' is not of 'Thin' type", request.SourceVolumeId)
	}

	if llv.Status == nil || llv.Status.ActualSize.Value() == 0 {
		return nil, status.Errorf(codes.FailedPrecondition, "Source LVMLogicalVolume '%s' ActualSize is unknown", request.SourceVolumeId)
	}

	lvg, err := utils.GetLVMVolumeGroup(ctx, d.cl, llv.Spec.LVMVolumeGroupName)
	if err != nil {
		d.log.Error(
			err,
			fmt.Sprintf(
				"[CreateSnapshot][traceID:%s][volumeID:%s] error getting LVMVolumeGroup %s",
				traceID,
				request.SourceVolumeId,
				llv.Spec.LVMVolumeGroupName,
			),
		)
		return nil, status.Errorf(codes.Internal, "error getting LVMVolumeGroup %s: %s", llv.Spec.LVMVolumeGroupName, err.Error())
	}

	freeSpace, err := utils.GetLVMThinPoolFreeSpace(*lvg, llv.Spec.Thin.PoolName)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get free space for thin pool %s in lvg %s: %v", llv.Spec.Thin.PoolName, lvg.Name, err)
	}

	if freeSpace.Value() < llv.Status.ActualSize.Value() {
		return nil, status.Errorf(
			codes.FailedPrecondition,
			"not enough space in pool %s (lvg %s): %s; need at least %s",
			llv.Spec.Thin.PoolName,
			lvg.Name,
			freeSpace.String(),
			llv.Status.ActualSize.String(),
		)
	}

	// the snapshots are required to be created in the same node and device class as the source volume.

	// suggested name is in form "{prefix}-{uuid}", where {prefix} is specified as external-snapshotter argument
	// {prefix} can not be the default "snapshot", since it's reserved keyword in LVM
	name := request.Name

	actualNameOnTheNode := request.Parameters[internal.ActualNameOnTheNodeKey]
	if actualNameOnTheNode == "" {
		actualNameOnTheNode = name
	}

	_, err = utils.CreateLVMLogicalVolumeSnapshot(
		ctx,
		d.cl,
		d.log,
		traceID,
		name,
		v1alpha1.LVMLogicalVolumeSnapshotSpec{
			ActualSnapshotNameOnTheNode: actualNameOnTheNode,
			LVMLogicalVolumeName:        llv.Name,
		},
	)
	if err != nil {
		if kerrors.IsAlreadyExists(err) {
			d.log.Info(fmt.Sprintf("[CreateSnapshot][traceID:%s][volumeID:%s] LVMLogicalVolumeSnapshot %s already exists. Skip creating", traceID, name, name))
		} else {
			d.log.Error(err, fmt.Sprintf("[CreateSnapshot][traceID:%s][volumeID:%s] error CreateLVMLogicalVolume", traceID, name))
			return nil, err
		}
	}

	attemptCounter, err := utils.WaitForLLVSStatusUpdate(ctx, d.cl, d.log, traceID, name)
	if err != nil {
		d.log.Error(err, fmt.Sprintf("[CreateSnapshot][traceID:%s][volumeID:%s] error WaitForStatusUpdate. DeleteLVMLogicalVolumeSnapshot %s", traceID, name, request.Name))

		deleteErr := utils.DeleteLVMLogicalVolumeSnapshot(ctx, d.cl, d.log, traceID, request.Name)
		if deleteErr != nil {
			d.log.Error(deleteErr, fmt.Sprintf("[CreateSnapshot][traceID:%s][volumeID:%s] error DeleteLVMLogicalVolumeSnapshot", traceID, name))
		}

		d.log.Error(err, fmt.Sprintf("[CreateSnapshot][traceID:%s][volumeID:%s] error creating LVMLogicalVolumeSnapshot", traceID, name))
		return nil, err
	}
	d.log.Trace(fmt.Sprintf("[CreateSnapshot][traceID:%s][volumeID:%s] finish wait CreateLVMLogicalVolume, attempt counter = %d", traceID, name, attemptCounter))

	sourceSizeQty, err := resource.ParseQuantity(llv.Spec.Size)
	if err != nil {
		d.log.Error(err, fmt.Sprintf("[CreateSnapshot][traceID:%s] error parsing quantity %s", traceID, llv.Spec.Size))
		return nil, status.Errorf(codes.Internal, "error parsing quantity: %v", err)
	}

	return &csi.CreateSnapshotResponse{
		Snapshot: &csi.Snapshot{
			SnapshotId:     name,
			SourceVolumeId: request.SourceVolumeId,
			SizeBytes:      sourceSizeQty.Value(),
			CreationTime: &timestamp.Timestamp{
				Seconds: time.Now().Unix(),
				Nanos:   0,
			},
			ReadyToUse: true,
		},
	}, nil
}

func (d *Driver) DeleteSnapshot(ctx context.Context, request *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	if len(request.SnapshotId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "SnapshotId ID cannot be empty")
	}

	traceID := uuid.New().String()
	d.log.Trace(fmt.Sprintf("[DeleteSnapshot][traceID:%s] ========== DeleteSnapshot ============", traceID))
	d.log.Trace(request.String())

	if err := utils.DeleteLVMLogicalVolumeSnapshot(ctx, d.cl, d.log, traceID, request.SnapshotId); err != nil {
		d.log.Error(err, "error DeleteLVMLogicalVolume")
	}

	d.log.Info(fmt.Sprintf("[Snapshot][traceID:%s][SnapshotId:%s] Snapshot deleted successfully", traceID, request.SnapshotId))
	d.log.Info("[Snapshot][traceID:%s] ========== END Snapshot ============", traceID)
	return &csi.DeleteSnapshotResponse{}, nil
}

func (d *Driver) ListSnapshots(_ context.Context, _ *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error) {
	d.log.Info("call method ListSnapshots")
	return nil, nil
}
