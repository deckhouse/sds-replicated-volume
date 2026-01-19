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
	// ReplicatedStorageClassCondConfigurationReadyType indicates whether the storage class
	// configuration is ready and validated.
	//
	// Reasons describe readiness or validation failure conditions.
	ReplicatedStorageClassCondConfigurationReadyType                                 = "ConfigurationReady"
	ReplicatedStorageClassCondConfigurationReadyReasonEligibleNodesCalculationFailed = "EligibleNodesCalculationFailed" // Eligible nodes calculation failed.
	ReplicatedStorageClassCondConfigurationReadyReasonInvalidConfiguration           = "InvalidConfiguration"           // Configuration is invalid.
	ReplicatedStorageClassCondConfigurationReadyReasonReady                          = "Ready"                          // Configuration is ready.
	ReplicatedStorageClassCondConfigurationReadyReasonStoragePoolNotFound            = "StoragePoolNotFound"            // Storage pool not found.
)

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
	// ReplicatedStorageClassCondEligibleNodesCalculatedType indicates whether eligible nodes
	// have been calculated for the storage class.
	//
	// Reasons describe calculation success or failure conditions.
	ReplicatedStorageClassCondEligibleNodesCalculatedType                                = "EligibleNodesCalculated"
	ReplicatedStorageClassCondEligibleNodesCalculatedReasonCalculated                    = "Calculated"                    // Eligible nodes calculated successfully.
	ReplicatedStorageClassCondEligibleNodesCalculatedReasonInsufficientEligibleNodes     = "InsufficientEligibleNodes"     // Not enough eligible nodes.
	ReplicatedStorageClassCondEligibleNodesCalculatedReasonInvalidConfiguration          = "InvalidConfiguration"          // Configuration is invalid.
	ReplicatedStorageClassCondEligibleNodesCalculatedReasonInvalidStoragePoolOrLVG       = "InvalidStoragePoolOrLVG"       // ReplicatedStoragePool or LVMVolumeGroup is invalid or not ready.
	ReplicatedStorageClassCondEligibleNodesCalculatedReasonLVMVolumeGroupNotFound        = "LVMVolumeGroupNotFound"        // LVMVolumeGroup not found.
	ReplicatedStorageClassCondEligibleNodesCalculatedReasonReplicatedStoragePoolNotFound = "ReplicatedStoragePoolNotFound" // ReplicatedStoragePool not found.
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
