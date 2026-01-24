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

package v1alpha1

const (
	// ReplicatedStoragePoolCondReadyType indicates whether the storage pool is ready.
	//
	// Reasons describe readiness or failure conditions.
	ReplicatedStoragePoolCondReadyType                           = "Ready"
	ReplicatedStoragePoolCondReadyReasonInvalidLVMVolumeGroup    = "InvalidLVMVolumeGroup"    // LVMVolumeGroup is invalid.
	ReplicatedStoragePoolCondReadyReasonInvalidNodeLabelSelector = "InvalidNodeLabelSelector" // NodeLabelSelector is invalid.
	ReplicatedStoragePoolCondReadyReasonLVMVolumeGroupNotFound   = "LVMVolumeGroupNotFound"   // LVMVolumeGroup not found.
	ReplicatedStoragePoolCondReadyReasonReady                    = "Ready"                    // Storage pool is ready.
)
