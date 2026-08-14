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
	// ReplicatedVolumeCondReadyType indicates whether the volume is ready to serve I/O
	// at the guaranteed minimum redundancy right now. It is an availability + lifecycle
	// aggregate: it is True only when the datamesh has quorum and enough UpToDate replicas
	// to satisfy the guaranteed minimum data redundancy. Reasons are ordered by precedence
	// (first match wins); the cause of unavailability is carried in the reason.
	ReplicatedVolumeCondReadyType                               = "Ready"
	ReplicatedVolumeCondReadyReasonForming                      = "Forming"                      // Volume is forming: an initial or post-formation transition is in progress, or the volume never reached an IO-serving state.
	ReplicatedVolumeCondReadyReasonInsufficientUpToDateReplicas = "InsufficientUpToDateReplicas" // Quorum is met but the number of UpToDate voters is below qmr; IO is blocked.
	ReplicatedVolumeCondReadyReasonQuorumLost                   = "QuorumLost"                   // Quorum is lost: the number of reachable voters plus tie-breakers is below the quorum threshold.
	ReplicatedVolumeCondReadyReasonReady                        = "Ready"                        // Volume is serving IO at the guaranteed minimum redundancy.
	ReplicatedVolumeCondReadyReasonStatusUnknown                = "StatusUnknown"                // Effective layout cannot be determined (e.g. stale agents).
	ReplicatedVolumeCondReadyReasonTerminating                  = "Terminating"                  // Volume is being deleted.
)

const (
	// ReplicatedVolumeCondResilientType indicates whether the effective redundancy meets the
	// redundancy intended by the volume's configuration. It covers the "degraded-but-serving"
	// gap: the volume still serves IO (Ready=True) but cannot tolerate the intended number of
	// failures. Reasons carry the cause.
	ReplicatedVolumeCondResilientType                = "Resilient"
	ReplicatedVolumeCondResilientReasonDegraded      = "Degraded"      // Effective redundancy is below the intended redundancy (effective FTT or GMDR is less than intended).
	ReplicatedVolumeCondResilientReasonForming       = "Forming"       // Volume is forming; resilience has not been evaluated yet.
	ReplicatedVolumeCondResilientReasonResilient     = "Resilient"     // Effective redundancy meets or exceeds the intended redundancy.
	ReplicatedVolumeCondResilientReasonStatusUnknown = "StatusUnknown" // Effective layout cannot be determined (e.g. stale agents).
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
