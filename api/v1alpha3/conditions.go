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

package v1alpha3

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
	// [ConditionTypeQuorumConfigured] indicates whether quorum configuration for RV is completed
	ConditionTypeQuorumConfigured = "QuorumConfigured"

	// [ConditionTypeDiskfulReplicaCountReached] indicates whether desired number of diskful replicas is reached
	ConditionTypeDiskfulReplicaCountReached = "DiskfulReplicaCountReached"

	// [ConditionTypeAllReplicasReady] indicates whether all replicas are Ready
	ConditionTypeAllReplicasReady = "AllReplicasReady"

	// [ConditionTypeSharedSecretAlgorithmSelected] indicates whether shared secret algorithm is selected
	ConditionTypeSharedSecretAlgorithmSelected = "SharedSecretAlgorithmSelected"

	// [ConditionTypeConfigurationAdjusted] indicates whether the configuration adjustment for RVR is completed
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

	// [ConditionTypeInitialSync] indicates whether the initial synchronization has been completed
	ConditionTypeInitialSync = "InitialSync"

	// [ConditionTypeIsPrimary] indicates whether the replica is primary
	ConditionTypeIsPrimary = "Primary"

	// [ConditionTypeDevicesReady] indicates whether all the devices in UpToDate state
	ConditionTypeDevicesReady = "DevicesReady"

	// [ConditionTypeConfigured] indicates whether replica configuration has been applied successfully
	ConditionTypeConfigured = "Configured"

	// [ConditionTypeQuorum] indicates whether replica has achieved quorum
	ConditionTypeQuorum = "Quorum"

	// [ConditionTypeDiskIOSuspended] indicates whether replica IO is suspended
	ConditionTypeDiskIOSuspended = "DiskIOSuspended"

	// [ConditionTypeAddressConfigured] indicates whether replica address has been configured
	ConditionTypeAddressConfigured = "AddressConfigured"

	// [ConditionTypeBackingVolumeCreated] indicates whether the backing volume (LVMLogicalVolume) has been created
	ConditionTypeBackingVolumeCreated = "BackingVolumeCreated"

	// [ConditionTypePublished] indicates whether the replica has been published
	ConditionTypePublished = "Published"
)

var ReplicatedVolumeReplicaConditions = map[string]struct{ UseObservedGeneration bool }{
	// Conditions managed by rvr_status_conditions controller
	ConditionTypeOnline:  {false},
	ConditionTypeIOReady: {false},

	// Conditions read by rvr_status_conditions controller
	ConditionTypeScheduled:       {false},
	ConditionTypeDataInitialized: {false},
	ConditionTypeInQuorum:        {false},
	ConditionTypeInSync:          {false},

	// Other RVR conditions
	ConditionTypeReady:                {false},
	ConditionTypeInitialSync:          {false},
	ConditionTypeIsPrimary:            {false},
	ConditionTypeDevicesReady:         {false},
	ConditionTypeConfigured:           {false},
	ConditionTypeQuorum:               {false},
	ConditionTypeDiskIOSuspended:      {false},
	ConditionTypeAddressConfigured:    {false},
	ConditionTypeBackingVolumeCreated: {false},
	ConditionTypePublished:            {false},
}

var ReplicatedVolumeConditions = map[string]struct{ UseObservedGeneration bool }{
	ConditionTypeRVScheduled:            {false},
	ConditionTypeRVBackingVolumeCreated: {false},
	ConditionTypeRVConfigured:           {false},
	ConditionTypeRVInitialized:          {false},
	ConditionTypeRVQuorum:               {false},
	ConditionTypeRVDataQuorum:           {false},
	ConditionTypeRVIOReady:              {false},
}

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
	ReasonSchedulingInProgress = "SchedulingInProgress"
	ReasonAllReplicasScheduled = "AllReplicasScheduled"
	ReasonReplicasNotScheduled = "ReplicasNotScheduled"
)

// Condition reasons for [ConditionTypeRVBackingVolumeCreated] condition
const (
	ReasonWaitingForBackingVolumes = "WaitingForBackingVolumes"
	ReasonAllBackingVolumesReady   = "AllBackingVolumesReady"
	ReasonBackingVolumesNotReady   = "BackingVolumesNotReady"
)

// Condition reasons for [ConditionTypeRVConfigured] condition
const (
	ReasonConfigurationInProgress = "ConfigurationInProgress"
	ReasonAllReplicasConfigured   = "AllReplicasConfigured"
	ReasonReplicasNotConfigured   = "ReplicasNotConfigured"
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

// Condition reasons for [ConditionTypeReady] condition
const (
	ReasonWaitingForInitialSync = "WaitingForInitialSync"
	ReasonDevicesAreNotReady    = "DevicesAreNotReady"
	ReasonAdjustmentFailed      = "AdjustmentFailed"
	ReasonNoQuorum              = "NoQuorum"
	ReasonDiskIOSuspended       = "DiskIOSuspended"
	ReasonReady                 = "Ready"
)

// Condition reasons for [ConditionTypeConfigured] condition
const (
	ReasonConfigurationFailed                           = "ConfigurationFailed"
	ReasonMetadataCheckFailed                           = "MetadataCheckFailed"
	ReasonMetadataCreationFailed                        = "MetadataCreationFailed"
	ReasonStatusCheckFailed                             = "StatusCheckFailed"
	ReasonResourceUpFailed                              = "ResourceUpFailed"
	ReasonConfigurationAdjustFailed                     = "ConfigurationAdjustFailed"
	ReasonConfigurationAdjustmentPausedUntilInitialSync = "ConfigurationAdjustmentPausedUntilInitialSync"
	ReasonPromotionDemotionFailed                       = "PromotionDemotionFailed"
	ReasonConfigurationAdjustmentSucceeded              = "ConfigurationAdjustmentSucceeded"
)

// Condition reasons for [ConditionTypeInitialSync] condition
const (
	ReasonInitialSyncRequiredButNotReady = "InitialSyncRequiredButNotReady"
	ReasonSafeForInitialSync             = "SafeForInitialSync"
	ReasonInitialDeviceReadinessReached  = "InitialDeviceReadinessReached"
)

// Condition reasons for [ConditionTypeDevicesReady] condition
const (
	ReasonDeviceIsNotReady = "DeviceIsNotReady"
	ReasonDeviceIsReady    = "DeviceIsReady"
)

// Condition reasons for [ConditionTypeIsPrimary] condition
const (
	ReasonResourceRoleIsPrimary    = "ResourceRoleIsPrimary"
	ReasonResourceRoleIsNotPrimary = "ResourceRoleIsNotPrimary"
)

// Condition reasons for [ConditionTypeQuorum] condition
const (
	ReasonNoQuorumStatus = "NoQuorumStatus"
	ReasonQuorumStatus   = "QuorumStatus"
)

// Condition reasons for [ConditionTypeDiskIOSuspended] condition
const (
	ReasonDiskIONotSuspendedStatus     = "DiskIONotSuspendedStatus"
	ReasonDiskIOSuspendedUnknownReason = "DiskIOSuspendedUnknownReason"
	ReasonDiskIOSuspendedByUser        = "DiskIOSuspendedByUser"
	ReasonDiskIOSuspendedNoData        = "DiskIOSuspendedNoData"
	ReasonDiskIOSuspendedFencing       = "DiskIOSuspendedFencing"
	ReasonDiskIOSuspendedQuorum        = "DiskIOSuspendedQuorum"
)

// Condition reasons for [ConditionTypeScheduled] condition
const (
	ReasonSchedulingReplicaScheduled         = "ReplicaScheduled"
	ReasonSchedulingWaitingForAnotherReplica = "WaitingForAnotherReplica"
	ReasonSchedulingPending                  = "SchedulingPending"
	ReasonSchedulingFailed                   = "SchedulingFailed"
	ReasonSchedulingTopologyConflict         = "TopologyConstraintsFailed"
	ReasonSchedulingNoCandidateNodes         = "NoAvailableNodes"
	ReasonSchedulingInsufficientStorage      = "InsufficientStorage"
)

// Condition reasons for [ConditionTypeDiskfulReplicaCountReached] condition
const (
	ReasonFirstReplicaIsBeingCreated          = "FirstReplicaIsBeingCreated"
	ReasonRequiredNumberOfReplicasIsAvailable = "RequiredNumberOfReplicasIsAvailable"
)

// Condition reasons for [ConditionTypeAddressConfigured] condition
const (
	ReasonAddressConfigurationSucceeded = "AddressConfigurationSucceeded"
	ReasonNodeIPNotFound                = "NodeIPNotFound"
	ReasonPortSettingsNotFound          = "PortSettingsNotFound"
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

// Condition reasons for [ConditionTypeIOReady] condition (reserved, not used yet)
const (
	ReasonSynchronizing = "Synchronizing"
)

// Condition reasons for [ConditionTypePublished] condition (reserved, not used yet)
const (
	// status=True
	ReasonPublished = "Published"
	// status=False
	ReasonUnpublished             = "Unpublished"
	ReasonPublishPending          = "PublishPending"
	ReasonPublishingNotApplicable = "PublishingNotApplicable"
	// status=Unknown
	ReasonPublishingNotInitialized = "PublishingNotInitialized"
)
