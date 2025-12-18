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
// Condition types read by rvr_status_conditions controller (managed by other controllers)
// =============================================================================

const (
	// [ConditionTypeScheduled] indicates whether replica has been scheduled to a node
	ConditionTypeScheduled = "Scheduled"

	// [ConditionTypeInitialized] indicates whether replica has been initialized (does not reset after True)
	ConditionTypeInitialized = "Initialized"

	// [ConditionTypeInQuorum] indicates whether replica is in quorum
	ConditionTypeInQuorum = "InQuorum"

	// [ConditionTypeInSync] indicates whether replica data is synchronized
	ConditionTypeInSync = "InSync"
)

// =============================================================================
// Condition types for other controllers (not used by rvr_status_conditions)
// =============================================================================

// RVR condition types
const (
	// [ConditionTypeReady] indicates whether the replica is ready and operational
	ConditionTypeReady = "Ready"

	// [ConditionTypeInitialSync] indicates whether the initial synchronization has been completed
	ConditionTypeInitialSync = "InitialSync"

	// [ConditionTypeIsPrimary] indicates whether the replica is primary
	ConditionTypeIsPrimary = "Primary"

	// [ConditionTypeDevicesReady] indicates whether all the devices in UpToDate state
	ConditionTypeDevicesReady = "DevicesReady"

	// [ConditionTypeConfigurationAdjusted] indicates whether replica configuration has been applied successfully
	ConditionTypeConfigurationAdjusted = "ConfigurationAdjusted"

	// [ConditionTypeQuorum] indicates whether replica has achieved quorum
	ConditionTypeQuorum = "Quorum"

	// [ConditionTypeDiskIOSuspended] indicates whether replica IO is suspended
	ConditionTypeDiskIOSuspended = "DiskIOSuspended"

	// [ConditionTypeAddressConfigured] indicates whether replica address has been configured
	ConditionTypeAddressConfigured = "AddressConfigured"

	// [ConditionTypeBackingVolumeCreated] indicates whether the backing volume (LVMLogicalVolume) has been created
	ConditionTypeBackingVolumeCreated = "BackingVolumeCreated"
)

// RV condition types
const (
	// [ConditionTypeQuorumConfigured] indicates whether quorum configuration for RV is completed
	ConditionTypeQuorumConfigured = "QuorumConfigured"

	// [ConditionTypeAllReplicasReady] indicates whether all replicas are Ready
	ConditionTypeAllReplicasReady = "AllReplicasReady"

	// [ConditionTypeSharedSecretAlgorithmSelected] indicates whether shared secret algorithm is selected
	ConditionTypeSharedSecretAlgorithmSelected = "SharedSecretAlgorithmSelected"

	// [ConditionTypeConfigured] indicates whether ReplicatedVolume is configured and ready
	ConditionTypeConfigured = "Configured"
)

var ReplicatedVolumeReplicaConditions = map[string]struct{ UseObservedGeneration bool }{
	ConditionTypeReady:                 {false},
	ConditionTypeInitialSync:           {false},
	ConditionTypeIsPrimary:             {false},
	ConditionTypeDevicesReady:          {false},
	ConditionTypeConfigurationAdjusted: {false},
	ConditionTypeQuorum:                {false},
	ConditionTypeDiskIOSuspended:       {false},
	ConditionTypeAddressConfigured:     {false},
	ConditionTypeBackingVolumeCreated:  {false},
	ConditionTypeScheduled:             {false},
	ConditionTypeInitialized:           {false},
	ConditionTypeInQuorum:              {false},
	ConditionTypeInSync:                {false},
	ConditionTypeOnline:                {false},
	ConditionTypeIOReady:               {false},
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
	ReasonOnline        = "Online"
	ReasonUnscheduled   = "Unscheduled"
	ReasonUninitialized = "Uninitialized"
	ReasonQuorumLost    = "QuorumLost"
	ReasonNodeNotReady  = "NodeNotReady"
	ReasonAgentNotReady = "AgentNotReady"
)

// Condition reasons for [ConditionTypeIOReady] condition
const (
	ReasonIOReady   = "IOReady"
	ReasonOffline   = "Offline"
	ReasonOutOfSync = "OutOfSync"
	// ReasonNodeNotReady and ReasonAgentNotReady are also used for IOReady
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

// Condition reasons for [ConditionTypeConfigurationAdjusted] condition
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

// Condition reasons for [ConditionTypeIOReady] condition (reserved, not used yet)
const (
	ReasonSynchronizing = "Synchronizing"
)
