/*
Copyright 2025 Flant JSC

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

// TODO split RV/RVR conditions :ConditionTypeRVInitialized

// =============================================================================
// Condition types managed by rvr_status_conditions controller
// =============================================================================

const (
	// [ConditionTypeOnline] indicates whether replica is online (Scheduled AND Initialized AND InQuorum)
	ConditionTypeOnline = "Online"

	// [ConditionTypeIOReady] indicates whether replica is ready for I/O operations (Online AND InSync)
	ConditionTypeIOReady = "IOReady"
)

// =============================================================================
// Condition types managed by rv_status_conditions controller
// =============================================================================

const (
	// [ConditionTypeRVScheduled] indicates whether all RVRs have been scheduled
	ConditionTypeRVScheduled = "Scheduled"

	// [ConditionTypeRVBackingVolumeCreated] indicates whether all diskful RVRs have backing volumes created
	ConditionTypeRVBackingVolumeCreated = "BackingVolumeCreated"

	// [ConditionTypeRVConfigured] indicates whether all RVRs are configured
	ConditionTypeRVConfigured = "Configured"

	// [ConditionTypeRVInitialized] indicates whether enough RVRs are initialized
	ConditionTypeRVInitialized = "Initialized"

	// [ConditionTypeRVQuorum] indicates whether RV has quorum
	ConditionTypeRVQuorum = "Quorum"

	// [ConditionTypeRVDataQuorum] indicates whether RV has data quorum (diskful replicas)
	ConditionTypeRVDataQuorum = "DataQuorum"

	// [ConditionTypeRVIOReady] indicates whether RV has enough IOReady replicas
	ConditionTypeRVIOReady = "IOReady"
)

// =============================================================================
// Condition types for other RV controllers (not used by rv_status_conditions)
// =============================================================================

const (
	// [ConditionTypeConfigurationAdjusted] indicates whether replica configuration has been applied successfully
	ConditionTypeConfigurationAdjusted = "ConfigurationAdjusted"
)

// =============================================================================
// Condition types read by rvr_status_conditions controller (managed by other controllers)
// =============================================================================

const (
	// [ConditionTypeScheduled] indicates whether replica has been scheduled to a node
	ConditionTypeScheduled = "Scheduled"

	// [ConditionTypeDataInitialized] indicates whether replica has been initialized.
	// Does not reset after True, unless replica type has changed.
	ConditionTypeDataInitialized = "DataInitialized"

	// [ConditionTypeInQuorum] indicates whether replica is in quorum
	ConditionTypeInQuorum = "InQuorum"

	// [ConditionTypeInSync] indicates whether replica data is synchronized
	ConditionTypeInSync = "InSync"
)

// =============================================================================
// Condition types read by rv_status_conditions controller (managed by other RVR controllers)
// =============================================================================

const (
	// [ConditionTypeRVRBackingVolumeCreated] indicates whether the backing volume for RVR is created
	ConditionTypeRVRBackingVolumeCreated = "BackingVolumeCreated"
)

// =============================================================================
// Condition types for RVR controllers
// =============================================================================

const (
	// [ConditionTypeReady] indicates whether the replica is ready and operational
	ConditionTypeReady = "Ready"

	// [RVRConditionTypeReady] is an alias for [ConditionTypeReady].
	// It exists to explicitly scope the condition type to ReplicatedVolumeReplica.
	RVRConditionTypeReady = ConditionTypeReady

	// [ConditionTypeConfigured] indicates whether replica configuration has been applied successfully
	ConditionTypeConfigured = "Configured"

	// [ConditionTypeAddressConfigured] indicates whether replica address has been configured
	ConditionTypeAddressConfigured = "AddressConfigured"

	// [ConditionTypeBackingVolumeCreated] indicates whether the backing volume (LVMLogicalVolume) has been created
	ConditionTypeBackingVolumeCreated = "BackingVolumeCreated"

	// [ConditionTypeAttached] indicates whether the replica has been attached
	ConditionTypeAttached = "Attached"

	// [RVRConditionTypeAttached] is an alias for [ConditionTypeAttached].
	// It exists to explicitly scope the condition type to ReplicatedVolumeReplica.
	RVRConditionTypeAttached = ConditionTypeAttached
)

// =============================================================================
// Condition types and reasons for RVA (ReplicatedVolumeAttachment) controllers
// =============================================================================

const (
	// [RVAConditionTypeReady] indicates whether the attachment is ready for use:
	// Attached=True AND ReplicaIOReady=True.
	RVAConditionTypeReady = "Ready"

	// [RVAConditionTypeAttached] indicates whether the volume is attached to the requested node.
	// This condition is the former RVA "Ready" condition and contains detailed attach progress reasons.
	RVAConditionTypeAttached = "Attached"

	// [RVAConditionTypeReplicaIOReady] indicates whether the replica on the requested node is IOReady.
	// It mirrors ReplicatedVolumeReplica condition IOReady (Status/Reason/Message) for the replica on rva.spec.nodeName.
	RVAConditionTypeReplicaIOReady = "ReplicaIOReady"
)

const (
	// RVA Ready condition reasons reported via [RVAConditionTypeReady] (aggregate).
	RVAReadyReasonReady             = "Ready"
	RVAReadyReasonNotAttached       = "NotAttached"
	RVAReadyReasonReplicaNotIOReady = "ReplicaNotIOReady"
)

const (
	// RVA Attached condition reasons reported via [RVAConditionTypeAttached].
	RVAAttachedReasonWaitingForActiveAttachmentsToDetach = "WaitingForActiveAttachmentsToDetach"
	RVAAttachedReasonWaitingForReplicatedVolume          = "WaitingForReplicatedVolume"
	RVAAttachedReasonWaitingForReplicatedVolumeIOReady   = "WaitingForReplicatedVolumeIOReady"
	RVAAttachedReasonWaitingForReplica                   = "WaitingForReplica"
	RVAAttachedReasonConvertingTieBreakerToAccess        = "ConvertingTieBreakerToAccess"
	RVAAttachedReasonUnableToProvideLocalVolumeAccess    = "UnableToProvideLocalVolumeAccess"
	RVAAttachedReasonLocalityNotSatisfied                = "LocalityNotSatisfied"
	RVAAttachedReasonSettingPrimary                      = "SettingPrimary"
	RVAAttachedReasonAttached                            = "Attached"
)

const (
	// RVA ReplicaIOReady condition reasons reported via [RVAConditionTypeReplicaIOReady].
	// Most of the time this condition mirrors the replica's IOReady condition reason;
	// this reason is used only when replica/condition is not yet observable.
	RVAReplicaIOReadyReasonWaitingForReplica = "WaitingForReplica"
)

// Replication values for [ReplicatedStorageClass] spec
const (
	ReplicationNone                       = "None"
	ReplicationAvailability               = "Availability"
	ReplicationConsistencyAndAvailability = "ConsistencyAndAvailability"
)

// =============================================================================
// Condition reasons used by rvr_status_conditions controller
// =============================================================================

// Condition reasons for [ConditionTypeOnline] condition
const (
	ReasonOnline             = "Online"
	ReasonUnscheduled        = "Unscheduled"
	ReasonUninitialized      = "Uninitialized"
	ReasonQuorumLost         = "QuorumLost"
	ReasonNodeNotReady       = "NodeNotReady"
	ReasonAgentNotReady      = "AgentNotReady"
	ReasonAgentPodMissing    = "AgentPodMissing"    // No agent pod found on node
	ReasonAgentStatusUnknown = "AgentStatusUnknown" // Can't determine status (API error)
)

// Condition reasons for [ConditionTypeIOReady] condition
const (
	ReasonIOReady   = "IOReady"
	ReasonOffline   = "Offline"
	ReasonOutOfSync = "OutOfSync"
	// ReasonNodeNotReady and ReasonAgentNotReady are also used for IOReady
)

// =============================================================================
// Condition reasons used by rv_status_conditions controller
// =============================================================================

// Condition reasons for [ConditionTypeRVScheduled] condition
const (
	ReasonAllReplicasScheduled = "AllReplicasScheduled"
	ReasonReplicasNotScheduled = "ReplicasNotScheduled"
	ReasonSchedulingInProgress = "SchedulingInProgress"
)

// Condition reasons for [ConditionTypeRVBackingVolumeCreated] condition
const (
	ReasonAllBackingVolumesReady   = "AllBackingVolumesReady"
	ReasonBackingVolumesNotReady   = "BackingVolumesNotReady"
	ReasonWaitingForBackingVolumes = "WaitingForBackingVolumes"
)

// Condition reasons for [ConditionTypeRVConfigured] condition
const (
	ReasonAllReplicasConfigured   = "AllReplicasConfigured"
	ReasonReplicasNotConfigured   = "ReplicasNotConfigured"
	ReasonConfigurationInProgress = "ConfigurationInProgress"
)

// Condition reasons for [ConditionTypeRVInitialized] condition
const (
	ReasonInitialized              = "Initialized"
	ReasonInitializationInProgress = "InitializationInProgress"
	ReasonWaitingForReplicas       = "WaitingForReplicas"
)

// Condition reasons for [ConditionTypeRVQuorum] condition
const (
	ReasonQuorumReached  = "QuorumReached"
	ReasonQuorumDegraded = "QuorumDegraded"
	// ReasonQuorumLost is also used (defined above)
)

// Condition reasons for [ConditionTypeRVDataQuorum] condition
const (
	ReasonDataQuorumReached  = "DataQuorumReached"
	ReasonDataQuorumDegraded = "DataQuorumDegraded"
	ReasonDataQuorumLost     = "DataQuorumLost"
)

// Condition reasons for [ConditionTypeRVIOReady] condition
const (
	ReasonRVIOReady                   = "IOReady"
	ReasonNoIOReadyReplicas           = "NoIOReadyReplicas"
	ReasonInsufficientIOReadyReplicas = "InsufficientIOReadyReplicas"
)

// =============================================================================
// Condition reasons reserved for other controllers (not used yet)
// =============================================================================

// Condition reasons for [ConditionTypeConfigured] condition
const (
	ReasonConfigurationFailed              = "ConfigurationFailed"
	ReasonConfigurationAdjustmentSucceeded = "ConfigurationAdjustmentSucceeded"
)

// Condition reasons for [ConditionTypeScheduled] condition
const (
	ReasonSchedulingReplicaScheduled = "ReplicaScheduled"
	ReasonSchedulingPending          = "SchedulingPending"
	ReasonSchedulingFailed           = "SchedulingFailed"
	ReasonSchedulingTopologyConflict = "TopologyConstraintsFailed"
	ReasonSchedulingNoCandidateNodes = "NoAvailableNodes"
)

// Condition reasons for [ConditionTypeAddressConfigured] condition
const (
	ReasonAddressConfigurationSucceeded = "AddressConfigurationSucceeded"
	ReasonNoFreePortAvailable           = "NoFreePortAvailable"
)

// Condition reasons for [ConditionTypeBackingVolumeCreated] condition
const (
	ReasonNotApplicable               = "NotApplicable"
	ReasonBackingVolumeDeletionFailed = "BackingVolumeDeletionFailed"
	ReasonBackingVolumeCreationFailed = "BackingVolumeCreationFailed"
	ReasonBackingVolumeReady          = "BackingVolumeReady"
	ReasonBackingVolumeNotReady       = "BackingVolumeNotReady"
)

// Condition reasons for [ConditionTypeDataInitialized] condition
const (
	// status=Unknown
	ReasonDataInitializedUnknownDiskState = "UnknownDiskState"
	// status=False
	ReasonNotApplicableToDiskless     = "NotApplicableToDiskless"
	ReasonDiskNeverWasInUpToDateState = "DiskNeverWasInUpToDateState"
	// status=True
	ReasonDiskHasBeenSeenInUpToDateState = "DiskHasBeenSeenInUpToDateState"
)

// Condition reasons for [ConditionTypeInQuorum] condition
const (
	ReasonInQuorumInQuorum   = "InQuorum"
	ReasonInQuorumQuorumLost = "QuorumLost"
)

// Condition reasons for [ConditionTypeInSync] condition
const (
	// status=True
	ReasonInSync   = "InSync"
	ReasonDiskless = "Diskless"

	// status=False
	ReasonDiskLost                    = "DiskLost"
	ReasonAttaching                   = "Attaching"
	ReasonDetaching                   = "Detaching"
	ReasonFailed                      = "Failed"
	ReasonNegotiating                 = "Negotiating"
	ReasonInconsistent                = "Inconsistent"
	ReasonOutdated                    = "Outdated"
	ReasonUnknownDiskState            = "UnknownDiskState"
	ReasonInSyncReplicaNotInitialized = "ReplicaNotInitialized"
)

// Condition reasons for [ConditionTypeConfigured] condition
const (
	// status=True
	ReasonConfigured = "Configured"
	// status=False
	ReasonFileSystemOperationFailed      = "FileSystemOperationFailed"
	ReasonConfigurationCommandFailed     = "ConfigurationCommandFailed"
	ReasonSharedSecretAlgSelectionFailed = "SharedSecretAlgSelectionFailed"
	ReasonPromoteFailed                  = "PromoteFailed"
	ReasonDemoteFailed                   = "DemoteFailed"
)

// Condition reasons for [ConditionTypeAttached] condition (reserved, not used yet)
const (
	// status=True
	ReasonAttached = "Attached"
	// status=False
	ReasonDetached               = "Detached"
	ReasonAttachPending          = "AttachPending"
	ReasonAttachingNotApplicable = "AttachingNotApplicable"
	// status=Unknown
	ReasonAttachingNotInitialized = "AttachingNotInitialized"
)
