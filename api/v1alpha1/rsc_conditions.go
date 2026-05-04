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
	// ReplicatedStorageClassCondConfigurationRolledOutType indicates whether all volumes'
	// configuration matches the storage class.
	//
	// Reasons describe configuration rollout state.
	ReplicatedStorageClassCondConfigurationRolledOutType                                 = "ConfigurationRolledOut"
	ReplicatedStorageClassCondConfigurationRolledOutReasonConfigurationRolloutDisabled   = "ConfigurationRolloutDisabled"   // Configuration rollout strategy is NewVolumesOnly.
	ReplicatedStorageClassCondConfigurationRolledOutReasonConfigurationRolloutInProgress = "ConfigurationRolloutInProgress" // Configuration rollout in progress.
	ReplicatedStorageClassCondConfigurationRolledOutReasonNewConfigurationNotYetObserved = "NewConfigurationNotYetObserved" // Some volumes haven't observed the new configuration.
	ReplicatedStorageClassCondConfigurationRolledOutReasonRolledOutToAllVolumes          = "RolledOutToAllVolumes"          // Configuration rolled out to all volumes.
)

const (
	// ReplicatedStorageClassCondReadyType indicates overall readiness of the storage class.
	//
	// Reasons describe readiness or blocking conditions.
	ReplicatedStorageClassCondReadyType                            = "Ready"
	ReplicatedStorageClassCondReadyReasonInsufficientEligibleNodes = "InsufficientEligibleNodes" // Not enough eligible nodes.
	ReplicatedStorageClassCondReadyReasonInvalidConfiguration      = "InvalidConfiguration"      // Configuration is invalid.
	ReplicatedStorageClassCondReadyReasonReady                     = "Ready"                     // Storage class is ready.
	ReplicatedStorageClassCondReadyReasonWaitingForStoragePool     = "WaitingForStoragePool"     // Waiting for referenced storage pool.
)

const (
	// ReplicatedStorageClassCondStoragePoolReadyType indicates whether the referenced storage pool is ready.
	//
	// Reasons describe storage pool state. This condition may also use any reason
	// from ReplicatedStoragePool Ready condition (see rsp_conditions.go).
	ReplicatedStorageClassCondStoragePoolReadyType                      = "StoragePoolReady"
	ReplicatedStorageClassCondStoragePoolReadyReasonPending             = "Pending"             // ReplicatedStoragePool has no Ready condition yet.
	ReplicatedStorageClassCondStoragePoolReadyReasonStoragePoolNotFound = "StoragePoolNotFound" // Referenced storage pool not found; used only when migration from storagePool field failed because RSP does not exist.
)

const (
	// ReplicatedStorageClassCondVolumesSatisfyEligibleNodesType indicates whether all volumes'
	// replicas are placed on eligible nodes.
	//
	// Reasons describe eligible nodes satisfaction state.
	ReplicatedStorageClassCondVolumesSatisfyEligibleNodesType                                     = "VolumesSatisfyEligibleNodes"
	ReplicatedStorageClassCondVolumesSatisfyEligibleNodesReasonAllVolumesSatisfy                  = "AllVolumesSatisfy"                  // All volumes satisfy eligible nodes requirements.
	ReplicatedStorageClassCondVolumesSatisfyEligibleNodesReasonConflictResolutionInProgress       = "ConflictResolutionInProgress"       // Eligible nodes conflict resolution in progress.
	ReplicatedStorageClassCondVolumesSatisfyEligibleNodesReasonManualConflictResolution           = "ManualConflictResolution"           // Conflict resolution strategy is Manual.
	ReplicatedStorageClassCondVolumesSatisfyEligibleNodesReasonUpdatedEligibleNodesNotYetObserved = "UpdatedEligibleNodesNotYetObserved" // Some volumes haven't observed the updated eligible nodes.
)
