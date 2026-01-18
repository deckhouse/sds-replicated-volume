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
	ReplicatedStorageClassCondConfigurationReadyReasonReady                          = "Ready"                          // Configuration is ready.
	ReplicatedStorageClassCondConfigurationReadyReasonEligibleNodesCalculationFailed = "EligibleNodesCalculationFailed" // Eligible nodes calculation failed.
	ReplicatedStorageClassCondConfigurationReadyReasonInvalidConfiguration           = "InvalidConfiguration"           // Configuration is invalid.
	ReplicatedStorageClassCondConfigurationReadyReasonStoragePoolNotFound            = "StoragePoolNotFound"            // Storage pool not found.
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
	// ReplicatedStorageClassCondVolumesConfigurationAlignedType indicates whether all volumes'
	// configuration matches the storage class.
	//
	// Reasons describe configuration alignment state.
	ReplicatedStorageClassCondVolumesConfigurationAlignedType                               = "VolumesConfigurationAligned"
	ReplicatedStorageClassCondVolumesConfigurationAlignedReasonAllAligned                   = "AllAligned"                   // All volumes are aligned.
	ReplicatedStorageClassCondVolumesConfigurationAlignedReasonConfigurationRolloutDisabled = "ConfigurationRolloutDisabled" // Configuration rollout strategy is NewVolumesOnly.
	ReplicatedStorageClassCondVolumesConfigurationAlignedReasonInProgress                   = "InProgress"                   // Configuration rollout in progress.
	ReplicatedStorageClassCondVolumesConfigurationAlignedReasonPendingAcknowledgment        = "PendingAcknowledgment"        // Some volumes haven't acknowledged.
)

const (
	// ReplicatedStorageClassCondVolumesNodeEligibilityAlignedType indicates whether all volumes'
	// replicas are placed on eligible nodes.
	//
	// Reasons describe node eligibility alignment state.
	ReplicatedStorageClassCondVolumesNodeEligibilityAlignedType                           = "VolumesNodeEligibilityAligned"
	ReplicatedStorageClassCondVolumesNodeEligibilityAlignedReasonAllAligned               = "AllAligned"               // All volumes are aligned.
	ReplicatedStorageClassCondVolumesNodeEligibilityAlignedReasonConflictResolutionManual = "ConflictResolutionManual" // Conflict resolution strategy is Manual.
	ReplicatedStorageClassCondVolumesNodeEligibilityAlignedReasonInProgress               = "InProgress"               // Node eligibility alignment in progress.
	ReplicatedStorageClassCondVolumesNodeEligibilityAlignedReasonPendingAcknowledgment    = "PendingAcknowledgment"    // Some volumes haven't acknowledged.
)
