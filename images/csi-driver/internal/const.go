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

package internal

const (
	LvmTypeKey                  = "replicated.csi.storage.deckhouse.io/lvm-type"
	BindingModeKey              = "replicated.csi.storage.deckhouse.io/volume-binding-mode"
	StoragePoolKey              = "replicated.csi.storage.deckhouse.io/storagePool"
	LVMVThickContiguousParamKey = "replicated.csi.storage.deckhouse.io/lvm-thick-contiguous"
	ActualNameOnTheNodeKey      = "replicated.csi.storage.deckhouse.io/actualNameOnTheNode"
	TopologyKey                 = "topology.sds-replicated-volume-csi/node"
	SubPath                     = "subPath"
	VGNameKey                   = "vgname"
	ThinPoolNameKey             = "thinPoolName"
	LVMTypeThin                 = "Thin"
	LVMTypeThick                = "Thick"
	BindingModeWFFC             = "WaitForFirstConsumer"
	BindingModeI                = "Immediate"
	ReplicatedVolumeNameKey     = "replicatedVolumeName"
	DRBDDeviceMinorKey          = "drbdDeviceMinor"
	PVCAnnotationNameKey        = "csi.storage.k8s.io/pvc/name"
	PVCAnnotationNamespaceKey   = "csi.storage.k8s.io/pvc/namespace"

	FSTypeKey = "csi.storage.k8s.io/fstype"

	// supported filesystem types
	FSTypeExt4 = "ext4"
	FSTypeXfs  = "xfs"
)
