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

// CSI provisioner and StorageClass parameter constants.
const (
	// CSIProvisioner is the provisioner name for StorageClasses managed by this module.
	CSIProvisioner = "replicated.csi.storage.deckhouse.io"

	// CSIParamRSCNameKey is the StorageClass parameter key that identifies
	// which ReplicatedStorageClass the volume belongs to.
	CSIParamRSCNameKey = "replicated.csi.storage.deckhouse.io/replicatedStorageClassName"
)
