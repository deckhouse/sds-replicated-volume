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
	// ReplicatedVolumeCondConfigurationReadyType indicates whether the volume's configuration
	// matches the storage class configuration.
	//
	// Reasons describe configuration readiness state.
	ReplicatedVolumeCondConfigurationReadyType                                 = "ConfigurationReady"
	ReplicatedVolumeCondConfigurationReadyReasonConfigurationRolloutInProgress = "ConfigurationRolloutInProgress" // Configuration rollout is in progress.
	ReplicatedVolumeCondConfigurationReadyReasonReady                          = "Ready"                          // Configuration matches storage class.
	ReplicatedVolumeCondConfigurationReadyReasonStaleConfiguration             = "StaleConfiguration"             // Configuration does not match storage class (stale).
	ReplicatedVolumeCondConfigurationReadyReasonWaitingForStorageClass         = "WaitingForStorageClass"         // Waiting for storage class to be ready.
)

const (
	// ReplicatedVolumeCondConfiguredType indicates whether all replicas are configured.
	//
	// Reasons describe configuration progress / mismatch.
	ReplicatedVolumeCondConfiguredType                          = "Configured"
	ReplicatedVolumeCondConfiguredReasonConfigurationInProgress = "ConfigurationInProgress" // Configuration is still in progress.

// ReplicatedVolumeCondConfiguredReasonAllReplicasConfigured = "AllReplicasConfigured" // All replicas are configured.
// ReplicatedVolumeCondConfiguredReasonReplicasNotConfigured = "ReplicasNotConfigured" // Some replicas are not configured yet.
)

const (
	// ReplicatedVolumeCondIOReadyType indicates whether the volume is ready for I/O operations.
	ReplicatedVolumeCondIOReadyType                        = "IOReady"
	ReplicatedVolumeCondIOReadyReasonFormationInProgress   = "FormationInProgress"   // Datamesh formation is still in progress.
	ReplicatedVolumeCondIOReadyReasonNoConfiguration       = "NoConfiguration"       // Volume has no configuration yet.
	ReplicatedVolumeCondIOReadyReasonReady                 = "Ready"                 // Datamesh is formed and ready for I/O.
	ReplicatedVolumeCondIOReadyReasonTransitionsInProgress = "TransitionsInProgress" // Datamesh transitions are in progress.
)

const (
	// ReplicatedVolumeCondQuorumType indicates whether the volume has quorum.
	ReplicatedVolumeCondQuorumType = "Quorum"
)

const (
	// ReplicatedVolumeCondSatisfyEligibleNodesType indicates whether all replicas are placed
	// on eligible nodes according to the storage class.
	//
	// Reasons describe eligible nodes satisfaction state.
	ReplicatedVolumeCondSatisfyEligibleNodesType                               = "SatisfyEligibleNodes"
	ReplicatedVolumeCondSatisfyEligibleNodesReasonConflictResolutionInProgress = "ConflictResolutionInProgress" // Eligible nodes conflict resolution is in progress.
	ReplicatedVolumeCondSatisfyEligibleNodesReasonInConflictWithEligibleNodes  = "InConflictWithEligibleNodes"  // Some replicas are on non-eligible nodes.
	ReplicatedVolumeCondSatisfyEligibleNodesReasonSatisfyEligibleNodes         = "SatisfyEligibleNodes"         // All replicas are on eligible nodes.
)

const (
	// ReplicatedVolumeCondScheduledType indicates whether all replicas have been scheduled.
	//
	// Reasons describe scheduling progress / deficit.
	ReplicatedVolumeCondScheduledType                       = "Scheduled"
	ReplicatedVolumeCondScheduledReasonAllReplicasScheduled = "AllReplicasScheduled" // All replicas are scheduled.
	ReplicatedVolumeCondScheduledReasonReplicasNotScheduled = "ReplicasNotScheduled" // Some replicas are not scheduled yet.
	ReplicatedVolumeCondScheduledReasonSchedulingInProgress = "SchedulingInProgress" // Scheduling is still in progress.
)
