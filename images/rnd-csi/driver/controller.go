/*
Copyright 2023 Flant JSC

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
	"fmt"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"rnd-csi/api/v1alpha1"
	"rnd-csi/pkg/utils"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	lvmSelector = "local-lvm.csi.storage.deckhouse.io/lvg-selector"
	topologyKey = "topology.rnd-csi/node"
	subPath     = "subPath"
)

func (d *Driver) CreateVolume(ctx context.Context, request *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	d.log.Info("method CreateVolume")

	d.log.Info("========== CreateVolume ============")
	d.log.Info(request.String())
	d.log.Info("========== CreateVolume ============")

	l := make(map[string]string)
	err := yaml.Unmarshal([]byte(request.GetParameters()[lvmSelector]), &l)
	if err != nil {
		d.log.Error(err, "unmarshal labels")
		return nil, status.Error(codes.Internal, "Unmarshal labels")
	}

	selector := labels.SelectorFromSet(l)
	if err != nil {
		d.log.Error(err, "build selector")
		return nil, status.Error(codes.Internal, "Build selector")
	}

	listLvgs := &v1alpha1.LvmVolumeGroupList{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.LVMVolumeGroupKind,
			APIVersion: v1alpha1.TypeMediaAPIVersion,
		},
		ListMeta: metav1.ListMeta{},
		Items:    []v1alpha1.LvmVolumeGroup{},
	}
	err = d.cl.List(ctx, listLvgs, &client.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		d.log.Error(err, "get list lvg")
		return nil, status.Error(codes.Internal, "Get list lvg")
	}

	nodesVGSize := make(map[string]int64)

	for _, lvg := range listLvgs.Items {
		obj := &v1alpha1.LvmVolumeGroup{}
		err = d.cl.Get(ctx, client.ObjectKey{
			Name:      lvg.Name,
			Namespace: lvg.Namespace,
		}, obj)
		if err != nil {
			d.log.Error(err, fmt.Sprintf("get lvg name = %s", lvg.Name))
			return nil, status.Error(codes.Internal, "Get lvg name")
		}

		vgSize, err := resource.ParseQuantity(lvg.Status.VGSize)
		if err != nil {
			d.log.Error(err, "parse size vgSize")
			return nil, status.Error(codes.Internal, "Parse size vgSize")
		}

		d.log.Info("------------------------------")
		d.log.Info(lvg.Name)
		d.log.Info(vgSize.String())
		d.log.Info("------------------------------")

		nodesVGSize[lvg.Name] = vgSize.Value()
	}

	nodeName, _ := utils.NodeWithMaxSize(nodesVGSize)
	d.log.Info("nodeName maxSize = " + nodeName)

	if len(request.Name) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume Name cannot be empty")
	}
	if request.VolumeCapabilities == nil {
		return nil, status.Error(codes.InvalidArgument, "Volume Capability cannot de empty")
	}

	requiredCap := request.CapacityRange.GetRequiredBytes()

	volCtx := make(map[string]string)
	for k, v := range request.Parameters {
		volCtx[k] = v
	}

	volCtx[subPath] = request.Name

	d.log.Info("========== CreateVolume ============")
	fmt.Println(".>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>")
	fmt.Println("nodeName = ", nodeName)
	fmt.Println(".>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>")

	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			CapacityBytes: requiredCap,
			VolumeId:      request.Name,
			VolumeContext: volCtx,
			ContentSource: request.VolumeContentSource,
			AccessibleTopology: []*csi.Topology{
				{Segments: map[string]string{
					topologyKey: "a-ohrimenko-worker-0",
				}},
			},
		},
	}, nil
}

func (d *Driver) DeleteVolume(ctx context.Context, request *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	d.log.Info("method DeleteVolume")
	return &csi.DeleteVolumeResponse{}, nil
}

func (d *Driver) ControllerPublishVolume(ctx context.Context, request *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	d.log.Info("method ControllerPublishVolume")
	fmt.Println("///////////// ControllerPublishVolume ///////////////////////")
	fmt.Println("request.String() = ", request.String())
	fmt.Println("///////////// ControllerPublishVolume ///////////////////////")

	return &csi.ControllerPublishVolumeResponse{
		PublishContext: map[string]string{
			d.publishInfoVolumeName: request.VolumeId,
		},
	}, nil
}

func (d *Driver) ControllerUnpublishVolume(ctx context.Context, request *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	d.log.Info("method ControllerUnpublishVolume")
	fmt.Println("00000000000 ControllerUnpublishVolume 00000000000000000000000000000000")
	fmt.Println(request.String())
	fmt.Println("00000000000 ControllerUnpublishVolume 00000000000000000000000000000000")
	return &csi.ControllerUnpublishVolumeResponse{}, nil
}

func (d *Driver) ValidateVolumeCapabilities(ctx context.Context, request *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	d.log.Info("method ValidateVolumeCapabilities")
	return nil, nil
}

func (d *Driver) ListVolumes(ctx context.Context, request *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	d.log.Info("method ListVolumes")
	return nil, nil
}

func (d *Driver) GetCapacity(ctx context.Context, request *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error) {
	d.log.Info("method GetCapacity - 1")
	//todo MaxSize one PV
	//todo call volumeBindingMode: WaitForFirstConsumer

	return &csi.GetCapacityResponse{
		AvailableCapacity: 1000000000000,
		MaximumVolumeSize: nil,
		MinimumVolumeSize: nil,
	}, nil
}

func (d *Driver) ControllerGetCapabilities(ctx context.Context, request *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {
	d.log.Info("method ControllerGetCapabilities")
	capabilities := []csi.ControllerServiceCapability_RPC_Type{
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
		csi.ControllerServiceCapability_RPC_CLONE_VOLUME,
		csi.ControllerServiceCapability_RPC_GET_CAPACITY,
		csi.ControllerServiceCapability_RPC_EXPAND_VOLUME,
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_SNAPSHOT,
		csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME,
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

func (d *Driver) CreateSnapshot(ctx context.Context, request *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	d.log.Info(" call method CreateSnapshot")
	return nil, nil
}

func (d *Driver) DeleteSnapshot(ctx context.Context, request *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	d.log.Info(" call method DeleteSnapshot")
	return nil, nil
}

func (d *Driver) ListSnapshots(ctx context.Context, request *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error) {
	d.log.Info(" call method ListSnapshots")
	return nil, nil
}

func (d *Driver) ControllerExpandVolume(ctx context.Context, request *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	d.log.Info(" call method ControllerExpandVolume")
	return &csi.ControllerExpandVolumeResponse{
		CapacityBytes:         request.CapacityRange.RequiredBytes,
		NodeExpansionRequired: true,
	}, nil
}

func (d *Driver) ControllerGetVolume(ctx context.Context, request *csi.ControllerGetVolumeRequest) (*csi.ControllerGetVolumeResponse, error) {
	d.log.Info(" call method ControllerGetVolume")
	return &csi.ControllerGetVolumeResponse{}, nil
}

func (d *Driver) ControllerModifyVolume(ctx context.Context, request *csi.ControllerModifyVolumeRequest) (*csi.ControllerModifyVolumeResponse, error) {
	d.log.Info(" call method ControllerModifyVolume")
	return &csi.ControllerModifyVolumeResponse{}, nil
}
