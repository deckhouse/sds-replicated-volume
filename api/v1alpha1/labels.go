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

package v1alpha1

// LabelPrefix uses module name in prefix (not in key) for consistency with finalizers.
// Pattern: if key is short/generic -> module name in prefix (like finalizers)
//
//	if key contains module name -> short prefix (like node label storage.deckhouse.io/sds-replicated-volume-node)
const LabelPrefix = "sds-replicated-volume.storage.deckhouse.io/"

const (
	// LabelReplicatedStorageClass is the label key for ReplicatedStorageClass name on RV and RVR
	LabelReplicatedStorageClass = LabelPrefix + "replicated-storage-class"

	// LabelReplicatedVolume is the label key for ReplicatedVolume name on RVR
	LabelReplicatedVolume = LabelPrefix + "replicated-volume"

	// LabelLVMVolumeGroup is the label key for LVMVolumeGroup name on RVR
	LabelLVMVolumeGroup = LabelPrefix + "lvm-volume-group"

	// LabelThinPool will be used when thin pools are extracted to separate objects
	// LabelThinPool = LabelPrefix + "thin-pool"
)

// LabelNodeName is the label key for the Kubernetes node name where the RVR is scheduled.
// Note: This stores node.metadata.name, not the OS hostname (kubernetes.io/hostname).
const LabelNodeName = LabelPrefix + "node-name"

// EnsureLabel sets a label on the given labels map if it's not already set to the expected value.
// Returns the updated labels map and a boolean indicating if a change was made.
// This function is used across controllers for idempotent label updates.
func EnsureLabel(labels map[string]string, key, value string) (map[string]string, bool) {
	if labels == nil {
		labels = make(map[string]string)
	}
	if labels[key] == value {
		return labels, false // no change needed
	}
	labels[key] = value
	return labels, true
}
