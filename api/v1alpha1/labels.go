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

const labelPrefix = "sds-replicated-volume.deckhouse.io/"

const (
	// ReplicatedStorageClassLabelKey is the label key for ReplicatedStorageClass name on RV and RVR.
	ReplicatedStorageClassLabelKey = labelPrefix + "replicated-storage-class"

	// ReplicatedVolumeLabelKey is the label key for ReplicatedVolume name on RVR.
	ReplicatedVolumeLabelKey = labelPrefix + "replicated-volume"

	// LVMVolumeGroupLabelKey is the label key for LVMVolumeGroup name on RVR.
	LVMVolumeGroupLabelKey = labelPrefix + "lvm-volume-group"

	// NodeNameLabelKey is the label key for the Kubernetes node name where the RVR is scheduled.
	// Note: This stores node.metadata.name, not the OS hostname (kubernetes.io/hostname).
	NodeNameLabelKey = labelPrefix + "node-name"

	// AgentNodeLabelKey is the label key for selecting nodes where the agent should run.
	AgentNodeLabelKey = "storage.deckhouse.io/sds-replicated-volume-node"
)
