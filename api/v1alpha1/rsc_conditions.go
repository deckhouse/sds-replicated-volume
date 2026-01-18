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
	// ReplicatedStorageClassCondConfigurationAcceptedType indicates whether the storage class
	// configuration has been accepted and validated.
	//
	// Reasons describe acceptance or validation failure conditions.
	ReplicatedStorageClassCondConfigurationAcceptedType                                 = "ConfigurationAccepted"
	ReplicatedStorageClassCondConfigurationAcceptedReasonAccepted                       = "Accepted"                       // Configuration accepted.
	ReplicatedStorageClassCondConfigurationAcceptedReasonEligibleNodesCalculationFailed = "EligibleNodesCalculationFailed" // Eligible nodes calculation failed.
	ReplicatedStorageClassCondConfigurationAcceptedReasonInvalidConfiguration           = "InvalidConfiguration"           // Configuration is invalid.
	ReplicatedStorageClassCondConfigurationAcceptedReasonStoragePoolNotFound            = "StoragePoolNotFound"            // Storage pool not found.
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
	ReplicatedStorageClassCondEligibleNodesCalculatedReasonLVMVolumeGroupNotFound        = "LVMVolumeGroupNotFound"        // LVMVolumeGroup not found.
	ReplicatedStorageClassCondEligibleNodesCalculatedReasonReplicatedStoragePoolNotFound = "ReplicatedStoragePoolNotFound" // ReplicatedStoragePool not found.
	ReplicatedStorageClassCondEligibleNodesCalculatedReasonStoragePoolOrLVGNotReady      = "StoragePoolOrLVGNotReady"      // ReplicatedStoragePool or LVMVolumeGroup is not ready.
)

const (
	// ReplicatedStorageClassCondVolumesAcknowledgedType indicates whether all volumes
	// have acknowledged the storage class configuration and eligible nodes.
	//
	// Reasons describe acknowledgment state.
	ReplicatedStorageClassCondVolumesAcknowledgedType                  = "VolumesAcknowledged"
	ReplicatedStorageClassCondVolumesAcknowledgedReasonAllAcknowledged = "AllAcknowledged" // All volumes acknowledged.
	ReplicatedStorageClassCondVolumesAcknowledgedReasonPending         = "Pending"         // Acknowledgment pending.
)

const (
	// ReplicatedStorageClassCondVolumesConfigurationAlignedType indicates whether all volumes'
	// configuration matches the storage class.
	//
	// Reasons describe configuration alignment state.
	ReplicatedStorageClassCondVolumesConfigurationAlignedType                        = "VolumesConfigurationAligned"
	ReplicatedStorageClassCondVolumesConfigurationAlignedReasonAllAligned            = "AllAligned"            // All volumes are aligned.
	ReplicatedStorageClassCondVolumesConfigurationAlignedReasonInProgress            = "InProgress"            // Configuration rollout in progress.
	ReplicatedStorageClassCondVolumesConfigurationAlignedReasonPendingAcknowledgment = "PendingAcknowledgment" // Some volumes haven't acknowledged.
	ReplicatedStorageClassCondVolumesConfigurationAlignedReasonRolloutDisabled       = "RolloutDisabled"       // Rollout strategy is NewOnly.
)

const (
	// ReplicatedStorageClassCondVolumesEligibleNodesAlignedType indicates whether all volumes'
	// replicas are placed on eligible nodes.
	//
	// Reasons describe eligible nodes alignment state.
	ReplicatedStorageClassCondVolumesEligibleNodesAlignedType                        = "VolumesEligibleNodesAligned"
	ReplicatedStorageClassCondVolumesEligibleNodesAlignedReasonAllAligned            = "AllAligned"            // All volumes are aligned.
	ReplicatedStorageClassCondVolumesEligibleNodesAlignedReasonInProgress            = "InProgress"            // Eligible nodes alignment in progress.
	ReplicatedStorageClassCondVolumesEligibleNodesAlignedReasonPendingAcknowledgment = "PendingAcknowledgment" // Some volumes haven't acknowledged.
	ReplicatedStorageClassCondVolumesEligibleNodesAlignedReasonResolutionDisabled    = "ResolutionDisabled"    // Drift policy is Ignore.
)
