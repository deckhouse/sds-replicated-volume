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
	// ReplicatedVolumeCondAttachedReadyType indicates whether every established attachment of the
	// volume is actually serving I/O on its node. It is the per-consumer counterpart of the
	// mesh-level Ready condition: a datamesh that holds quorum as a whole (Ready would be True)
	// can still have a consumer frozen on the isolated side of a network partition, and this
	// condition is what reports it.
	//
	// An attachment is "established" when a datamesh member is marked Attached, a non-deleting
	// RVA exists on its node, and no Detach transition is in flight for it. The per-attachment
	// verdict mirrors the replica's Attached condition; the volume-level report is the worst
	// verdict across all established attachments.
	//
	// On deletion the condition is frozen at its last value and is not recomputed.
	ReplicatedVolumeCondAttachedReadyType                   = "AttachedReady"
	ReplicatedVolumeCondAttachedReadyReasonAttachedReady    = "AttachedReady"    // Every established attachment is serving I/O.
	ReplicatedVolumeCondAttachedReadyReasonAttachmentFailed = "AttachmentFailed" // An established attachment is expected to be attached but is not.
	ReplicatedVolumeCondAttachedReadyReasonIOSuspended      = "IOSuspended"      // An established attachment is frozen: the device is attached but I/O is suspended.
	ReplicatedVolumeCondAttachedReadyReasonNoAttachments    = "NoAttachments"    // The volume has no established attachment; nothing consumes it right now.
	ReplicatedVolumeCondAttachedReadyReasonStatusUnknown    = "StatusUnknown"    // An established attachment is in a transient or unreported state (e.g. stale agent).
)

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
	// ReplicatedVolumeCondConfiguredType indicates whether every datamesh member has applied the
	// volume's current datamesh revision, i.e. whether the intended configuration has actually
	// converged on the replicas. A member has applied it when its DatameshRevision equals the
	// volume's: DRBD matches the intended state derived from that revision, the backing volume
	// (if any) exists, matches and is ready, and the agent confirmed the configuration. It does
	// NOT mean peers are connected or the backing volume is in sync — those happen afterwards.
	//
	// It complements ConfigurationReady, which reports whether the configuration itself is valid
	// and current with respect to the storage class: a volume can hold a perfectly valid
	// configuration (ConfigurationReady=True) that has not reached all replicas yet
	// (Configured=False), and vice versa.
	//
	// Both are orthogonal to availability: a volume mid-rollout keeps serving I/O, so neither
	// condition enters Ready.
	//
	// On deletion the condition is frozen at its last value and is not recomputed.
	ReplicatedVolumeCondConfiguredType                          = "Configured"
	ReplicatedVolumeCondConfiguredReasonAllReplicasConfigured   = "AllReplicasConfigured"   // Every datamesh member has applied the current datamesh revision.
	ReplicatedVolumeCondConfiguredReasonConfigurationInProgress = "ConfigurationInProgress" // At least one datamesh member has not applied the current datamesh revision yet.
	ReplicatedVolumeCondConfiguredReasonConfigurationUnknown    = "ConfigurationUnknown"    // Convergence cannot be evaluated: no datamesh revision has been assigned yet, or the datamesh has no members.
)

const (
	// ReplicatedVolumeCondMembershipLayoutConvergedType indicates whether the actual datamesh layout
	// (diskful voters and tie-breakers) matches the layout intended by the volume's configuration.
	//
	// Reasons describe layout convergence state.
	ReplicatedVolumeCondMembershipLayoutConvergedType                        = "MembershipLayoutConverged"
	ReplicatedVolumeCondMembershipLayoutConvergedReasonCannotConverge        = "CannotConverge"        // A convergence pattern applies but no admissible candidate is available.
	ReplicatedVolumeCondMembershipLayoutConvergedReasonConverged             = "Converged"             // Actual layout matches the intended layout, no active transition.
	ReplicatedVolumeCondMembershipLayoutConvergedReasonConverging            = "Converging"            // A layout change is in flight: an active membership transition, a requested replica retype, a pending tie-breaker creation, or a pending diskful copy creation (self-heal); the layout has not settled on the intended one.
	ReplicatedVolumeCondMembershipLayoutConvergedReasonTransitionUnsupported = "TransitionUnsupported" // Layout mismatch with no supported automatic transition; manual intervention required.
	ReplicatedVolumeCondMembershipLayoutConvergedReasonVolumeDeleting        = "VolumeDeleting"        // Volume is being deleted; layout convergence is not evaluated.
)

const (
	// ReplicatedVolumeCondReadyType indicates whether the volume is serving I/O at the
	// guaranteed minimum redundancy. It is an availability + lifecycle aggregate over two
	// levels: the datamesh as a whole (quorum and UpToDate replicas, derived from the
	// effective layout) and its consumers (an established attachment can be frozen on the
	// isolated side of a partition even while the mesh holds quorum). The consumer level
	// enters through a single reason, AttachmentNotReady; which attachment and why is
	// reported by the AttachedReady condition.
	// Reasons carry the cause of unavailability; precedence is implemented in
	// computeReadyReport (Terminating → Forming → StatusUnknown → QuorumLost →
	// AttachmentNotReady → Ready), not in the alphabetical order below.
	ReplicatedVolumeCondReadyType                     = "Ready"
	ReplicatedVolumeCondReadyReasonAttachmentNotReady = "AttachmentNotReady" // Mesh-level availability holds, but an established attachment is not serving I/O; see the AttachedReady condition.
	ReplicatedVolumeCondReadyReasonForming            = "Forming"            // Volume is forming: an initial or post-formation transition is in progress, or the volume never reached an IO-serving state.
	ReplicatedVolumeCondReadyReasonQuorumLost         = "QuorumLost"         // Quorum is lost: the number of reachable voters plus tie-breakers is below the quorum threshold.
	ReplicatedVolumeCondReadyReasonReady              = "Ready"              // Volume is serving IO at the guaranteed minimum redundancy.
	ReplicatedVolumeCondReadyReasonStatusUnknown      = "StatusUnknown"      // Effective layout cannot be determined (e.g. stale agents).
	ReplicatedVolumeCondReadyReasonTerminating        = "Terminating"        // Volume is being deleted.
)

const (
	// ReplicatedVolumeCondResilientType indicates whether the effective redundancy meets the
	// redundancy intended by the volume's configuration. It covers the "degraded-but-serving"
	// gap: the volume still serves IO (Ready=True) but cannot tolerate the intended number of
	// failures. Reasons carry the cause.
	// On deletion the condition is frozen at its last value and is not recomputed.
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
	ReplicatedVolumeCondSatisfyEligibleNodesReasonConflictResolutionBlocked    = "ConflictResolutionBlocked"    // Evacuation is blocked by a safety guard.
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

const (
	// ReplicatedVolumeCondUnderMaintenanceType indicates that reconciliation of the volume is
	// paused by an operator through spec.maintenance: no action is taken on it at all — no
	// membership or layout decision, no datamesh transition, no replica or attachment change, no
	// configuration derivation and no deletion handling.
	//
	// The condition is presence-based: it is present exactly while the field is set and is
	// removed when the field is cleared. Absence is the normal state of a volume.
	//
	// While it is present the authoritative parts of the status (the datamesh, the
	// configuration, layout convergence) are frozen at their last values and must not be reasoned
	// from; health aggregated from the replicas (Ready, Resilient, AttachedReady) keeps being
	// published. A paused volume also does not finish deleting.
	//
	// The reason names the mode that is in effect.
	ReplicatedVolumeCondUnderMaintenanceType                           = "UnderMaintenance"
	ReplicatedVolumeCondUnderMaintenanceReasonNoResourceReconciliation = "NoResourceReconciliation" // Every action on this volume is stopped.
)
