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
	ReplicatedVolumeCondConfigurationReadyReasonInvalidConfiguration           = "InvalidConfiguration"           // Configuration is invalid (e.g. TransZonal zone count mismatch).
	ReplicatedVolumeCondConfigurationReadyReasonNewerConfigurationHeld         = "NewerConfigurationHeld"         // A newer storage class configuration exists but is intentionally not applied to this volume.
	ReplicatedVolumeCondConfigurationReadyReasonReady                          = "Ready"                          // Configuration is ready.
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
	// ReplicatedVolumeCondIOReadyType indicates whether the volume is ready for I/O operations
	// at full redundancy (UpToDate voters meet the datamesh quorum minimum redundancy).
	//
	// Reasons describe IO readiness state.
	ReplicatedVolumeCondIOReadyType                         = "IOReady"
	ReplicatedVolumeCondIOReadyReasonIOReady                = "IOReady"                // Up-to-date voters meet quorum minimum redundancy.
	ReplicatedVolumeCondIOReadyReasonInsufficientRedundancy = "InsufficientRedundancy" // Up-to-date voters below quorum minimum redundancy.
	ReplicatedVolumeCondIOReadyReasonWaitingForDatamesh     = "WaitingForDatamesh"     // Datamesh quorum threshold is not established yet.
)

const (
	// ReplicatedVolumeCondMembershipLayoutConvergedType indicates whether the actual datamesh layout
	// (diskful voters and tie-breakers) matches the layout intended by the volume's configuration.
	//
	// Reasons describe layout convergence state.
	ReplicatedVolumeCondMembershipLayoutConvergedType                        = "MembershipLayoutConverged"
	ReplicatedVolumeCondMembershipLayoutConvergedReasonCannotConverge        = "CannotConverge"        // A convergence pattern applies but no admissible candidate is available.
	ReplicatedVolumeCondMembershipLayoutConvergedReasonConverged             = "Converged"             // Actual layout matches the intended layout, no active transition.
	ReplicatedVolumeCondMembershipLayoutConvergedReasonConverging            = "Converging"            // A layout change is in flight: an active membership transition, a requested replica retype, or a pending tie-breaker creation; the layout has not settled on the intended one.
	ReplicatedVolumeCondMembershipLayoutConvergedReasonTransitionUnsupported = "TransitionUnsupported" // Layout mismatch with no supported automatic transition; manual intervention required.
	ReplicatedVolumeCondMembershipLayoutConvergedReasonVolumeDeleting        = "VolumeDeleting"        // Volume is being deleted; layout convergence is not evaluated.
)

const (
	// ReplicatedVolumeCondQuorumType indicates whether the volume has quorum
	// (reachable voters meet the datamesh quorum threshold).
	//
	// Reasons describe quorum presence.
	ReplicatedVolumeCondQuorumType                     = "Quorum"
	ReplicatedVolumeCondQuorumReasonQuorumLost         = "QuorumLost"         // Reachable voters below quorum.
	ReplicatedVolumeCondQuorumReasonQuorumPresent      = "QuorumPresent"      // Reachable voters meet quorum.
	ReplicatedVolumeCondQuorumReasonWaitingForDatamesh = "WaitingForDatamesh" // Datamesh quorum threshold is not established yet.
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
