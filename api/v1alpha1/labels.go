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
	// ReplicatedStorageClassLabelKey is the label key for ReplicatedStorageClass name on RV, RVR, RVA, LLV, and DRBDR.
	ReplicatedStorageClassLabelKey = labelPrefix + "replicated-storage-class"

	// ReplicatedVolumeLabelKey is the label key for ReplicatedVolume name on RVR, RVA, LLV, and DRBDR.
	ReplicatedVolumeLabelKey = labelPrefix + "replicated-volume"

	// LVMVolumeGroupLabelKey is the label key for LVMVolumeGroup name on RVR.
	LVMVolumeGroupLabelKey = labelPrefix + "lvm-volume-group"

	// AgentNodeLabelKey is the label key for selecting nodes where the agent should run.
	AgentNodeLabelKey = "storage.deckhouse.io/sds-replicated-volume-node"

	// ManagedByLabelKey is the label key set on managed resources (StorageClass, etc.).
	ManagedByLabelKey = "storage.deckhouse.io/managed-by"
	// ManagedByLabelValue is the value for ManagedByLabelKey.
	ManagedByLabelValue = "sds-replicated-volume"

	// NoPersistentVolumeLabelKey is set on ReplicatedVolume by linstor-migrator when the LINSTOR resource
	// had no matching PersistentVolume in the cluster at migration time.
	NoPersistentVolumeLabelKey = labelPrefix + "no-persistent-volume"

	// NoPersistentVolumeLabelValue is the value for NoPersistentVolumeLabelKey (boolean-style marker).
	NoPersistentVolumeLabelValue = "true"

	// SwitchToAutoConfigurationLabelKey is set on ReplicatedVolume by linstor-migrator at migration
	// time when the LINSTOR resource had a matching PersistentVolume. Stage 4 of the migrator uses
	// this label to find ReplicatedVolumes that must be switched from Manual to Auto configuration
	// once the referenced ReplicatedStorageClass becomes Ready.
	SwitchToAutoConfigurationLabelKey = labelPrefix + "switch-to-auto-configuration"

	// SwitchToAutoConfigurationLabelValue is the value for SwitchToAutoConfigurationLabelKey.
	SwitchToAutoConfigurationLabelValue = "true"

	// MigrationConversionReplicatedStoragePoolLabelKey is set by linstor-migrator stage 4 on
	// temporary ReplicatedStoragePools it creates to help the RSC controller migrate away from the
	// deprecated spec.storagePool field. Stage 4 uses this label to find and clean up only the
	// pools it owns, avoiding deletion of foreign RSPs with the same name and surviving
	// migrator restarts.
	MigrationConversionReplicatedStoragePoolLabelKey = labelPrefix + "migration-conversion-rsp"

	// MigrationConversionReplicatedStoragePoolLabelValue is the value for
	// MigrationConversionReplicatedStoragePoolLabelKey.
	MigrationConversionReplicatedStoragePoolLabelValue = "true"

	// AutoConfigurationBlockedLabelKey is set on ReplicatedVolume by linstor-migrator stage 4
	// when the volume cannot be switched to Auto configuration because either the referenced
	// ReplicatedStorageClass is missing, or it is in a terminal phase (InsufficientNodes,
	// InvalidConfiguration, Terminating). The ReplicatedVolume stays in Manual mode; the label
	// allows operators to alert on volumes that require manual intervention.
	AutoConfigurationBlockedLabelKey = labelPrefix + "auto-configuration-blocked"

	// AutoConfigurationBlockedLabelValue is the value for AutoConfigurationBlockedLabelKey.
	AutoConfigurationBlockedLabelValue = "true"
)
